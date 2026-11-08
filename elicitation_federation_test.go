package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Federated elicitation: a remote tool asking for input reaches the
// federating server's own caller, in every combination of eras and
// transports on both hops.

// newUpstream is the remote server. runs counts remote_two's handler runs.
func newUpstream(t *testing.T, runs *atomic.Int32) *Server {
	t.Helper()
	s := NewServer("upstream", "1")
	sm, err := NewJWTSessionManagerWithAutoKey(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	s.SetSessionManager(sm)
	ask := func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		res, err := Elicit(ctx, ElicitForm{Key: "name", Message: "Remote asks your name", Schema: testNameSchema})
		if err != nil {
			return nil, err
		}
		return NewToolResponseText("remote got " + describeElicit(res)), nil
	}
	s.RegisterTool(NewTool("remote_ask", "asks", String("who", "who")), ask)
	s.RegisterTool(NewTool("remote_hidden", "asks, found via tool_search").Discoverable("hidden"), ask)
	s.RegisterTool(NewTool("remote_two", "asks twice"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		if runs != nil {
			runs.Add(1)
		}
		first, err := Elicit(ctx, ElicitForm{Message: "First?", Schema: testNameSchema})
		if err != nil {
			return nil, err
		}
		second, err := Elicit(ctx, ElicitForm{Message: "Second?", Schema: testNameSchema})
		if err != nil {
			return nil, err
		}
		return NewToolResponseText("remote got " + describeElicit(first) + "/" + describeElicit(second)), nil
	})
	return s
}

// legacyOnlyHandler hides server/discover, so a Client falls back to the
// Legacy initialize handshake.
func legacyOnlyHandler(s *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var rpc MCPRequest
		_ = json.Unmarshal(body, &rpc)
		if rpc.Method == "server/discover" {
			http.Error(w, "unknown method", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		s.HandleRequest(w, r)
	}
}

// upstreamURL serves s over HTTP, Legacy-only when legacy is set.
func upstreamURL(t *testing.T, s *Server, legacy bool) string {
	t.Helper()
	var h http.Handler = http.HandlerFunc(s.HandleRequest)
	if legacy {
		h = legacyOnlyHandler(s)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

// upstreamClient connects a Client to s over transport ("http" or
// "stdio"), in the Legacy era when legacy is set and the Modern one
// otherwise.
func upstreamClient(t *testing.T, s *Server, transport string, legacy bool) *Client {
	t.Helper()
	var c *Client
	switch transport {
	case "http":
		c = NewClient(upstreamURL(t, s, legacy), nil, "up")
	case "stdio":
		toServerR, toServerW := io.Pipe()
		fromClientR, fromClientW := io.Pipe()
		toClientR, toClientW := io.Pipe()
		ctx, cancel := context.WithCancel(context.Background())
		go s.ServeStream(ctx, toServerR, toClientW)
		// Forward the client's lines to the server; a Legacy-only server
		// answers server/discover with Method not found.
		go func() {
			sc := bufio.NewScanner(fromClientR)
			sc.Buffer(make([]byte, 1<<20), 1<<20)
			for sc.Scan() {
				line := sc.Bytes()
				var head struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				_ = json.Unmarshal(line, &head)
				if legacy && head.Method == "server/discover" {
					fmt.Fprintf(toClientW, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"Method not found"}}`+"\n", head.ID)
					continue
				}
				toServerW.Write(append(append([]byte(nil), line...), '\n'))
			}
		}()
		c = NewStreamClient(toClientR, fromClientW, "up")
		t.Cleanup(func() { c.Close(); cancel(); toServerW.Close(); fromClientW.Close() })
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if got := c.ProtocolVersion() == MCPProtocolVersionModern; got == legacy {
		t.Fatalf("upstream era: protocol %s, legacy=%v", c.ProtocolVersion(), legacy)
	}
	return c
}

// proxy is the federating server under test; wrap (optional) prepares each
// HTTP request's context (a RemoteProvider).
type proxy struct {
	s    *Server
	wrap func(context.Context) context.Context
}

func newProxy(t *testing.T, register func(*Server)) *proxy {
	t.Helper()
	s := NewServer("proxy", "1")
	sm, err := NewJWTSessionManagerWithAutoKey(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	s.SetSessionManager(sm)
	if register != nil {
		register(s)
	}
	return &proxy{s: s}
}

func (p *proxy) url(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p.wrap != nil {
			r = r.WithContext(p.wrap(r.Context()))
		}
		p.s.HandleRequest(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// answerFn answers the i-th question the downstream caller is asked.
type answerFn func(i int, params map[string]any) map[string]any

func answerName(names ...string) answerFn {
	return func(i int, _ map[string]any) map[string]any { return accept(names[i%len(names)]) }
}

func answerAction(action ElicitAction) answerFn {
	return func(int, map[string]any) map[string]any { return map[string]any{"action": string(action)} }
}

// driveFn calls tool on the proxy as one kind of downstream caller,
// answering every question with answer, and returns the result's text.
type driveFn func(t *testing.T, p *proxy, caps map[string]any, tool string, args map[string]any, answer answerFn) (string, bool)

var downstreams = map[string]driveFn{
	"legacy-http":  driveLegacyHTTP,
	"legacy-stdio": driveLegacyStdio,
	"modern-http":  driveModernHTTP,
	"modern-stdio": driveModernStdio,
}

func driveLegacyHTTP(t *testing.T, p *proxy, caps map[string]any, tool string, args map[string]any, answer answerFn) (string, bool) {
	t.Helper()
	c := newLegacyHTTPClient(t, p.url(t), "2025-11-25", caps)
	msgs := c.call(2, "tools/call", map[string]any{"name": tool, "arguments": args}, sseAccept)
	for i := 0; ; {
		m := nextMessage(t, msgs)
		if m["method"] == "elicitation/create" {
			c.respond(m["id"], answer(i, m["params"].(map[string]any)))
			i++
			continue
		}
		if m["method"] != nil {
			continue
		}
		return toolText(t, m)
	}
}

func driveLegacyStdio(t *testing.T, p *proxy, caps map[string]any, tool string, args map[string]any, answer answerFn) (string, bool) {
	t.Helper()
	h := newStdioHarness(t, p.s)
	capsJSON, _ := json.Marshal(caps)
	h.send(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":`+string(capsJSON)+`,"clientInfo":{"name":"t","version":"1"}}}`)
	h.next(t, 5*time.Second)
	call, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	h.send(t, string(call))
	for i := 0; ; {
		var m map[string]any
		json.Unmarshal([]byte(h.next(t, 5*time.Second)), &m)
		if m["method"] == "elicitation/create" {
			reply, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": m["id"], "result": answer(i, m["params"].(map[string]any))})
			h.send(t, string(reply))
			i++
			continue
		}
		if m["method"] != nil {
			continue
		}
		return toolText(t, m)
	}
}

// answerInputRequests builds inputResponses for an InputRequiredResult's
// requests, in key order.
func answerInputRequests(reqs map[string]any, asked *int, answer answerFn) map[string]any {
	keys := make([]string, 0, len(reqs))
	for k := range reqs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := map[string]any{}
	for _, k := range keys {
		out[k] = answer(*asked, reqs[k].(map[string]any)["params"].(map[string]any))
		*asked++
	}
	return out
}

func driveModernHTTP(t *testing.T, p *proxy, caps map[string]any, tool string, args map[string]any, answer answerFn) (string, bool) {
	t.Helper()
	url := p.url(t)
	params := map[string]any{"name": tool, "arguments": args}
	asked := 0
	for round := 0; round < 10; round++ {
		msg := modernElicitCall(t, url, "tools/call", params, caps)
		if result, _ := msg["result"].(map[string]any); result != nil && result["resultType"] == "input_required" {
			reqs, state := inputRequired(t, msg)
			params["inputResponses"] = answerInputRequests(reqs, &asked, answer)
			params["requestState"] = state
			continue
		}
		return toolText(t, msg)
	}
	t.Fatal("too many rounds")
	return "", false
}

func driveModernStdio(t *testing.T, p *proxy, caps map[string]any, tool string, args map[string]any, answer answerFn) (string, bool) {
	t.Helper()
	h := newStdioHarness(t, p.s)
	params := map[string]any{"name": tool, "arguments": args, "_meta": map[string]any{
		metaKeyProtocolVersion: MCPProtocolVersionModern, metaKeyClientCapabilities: caps}}
	asked := 0
	for id := 1; id < 10; id++ {
		line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": params})
		h.send(t, string(line))
		var msg map[string]any
		json.Unmarshal([]byte(h.next(t, 5*time.Second)), &msg)
		if result, _ := msg["result"].(map[string]any); result != nil && result["resultType"] == "input_required" {
			reqs, state := inputRequired(t, msg)
			params["inputResponses"] = answerInputRequests(reqs, &asked, answer)
			params["requestState"] = state
			continue
		}
		return toolText(t, msg)
	}
	t.Fatal("too many rounds")
	return "", false
}

func registerRemote(c *Client) func(*Server) {
	return func(s *Server) {
		if err := s.RegisterRemoteServer(c); err != nil {
			panic(err)
		}
	}
}

// Every upstream era and transport against every downstream era and
// transport, through RegisterRemoteServer.
func TestFederatedElicitation_Matrix(t *testing.T) {
	for _, up := range []struct {
		transport string
		legacy    bool
	}{{"http", true}, {"stdio", true}, {"http", false}, {"stdio", false}} {
		upName := map[bool]string{true: "legacy", false: "modern"}[up.legacy] + "-" + up.transport
		for _, downName := range []string{"legacy-http", "legacy-stdio", "modern-http", "modern-stdio"} {
			t.Run("up="+upName+"/down="+downName, func(t *testing.T) {
				c := upstreamClient(t, newUpstream(t, nil), up.transport, up.legacy)
				p := newProxy(t, registerRemote(c))
				text, isErr := downstreams[downName](t, p, elicitCaps, "up__remote_ask", nil, answerName("Alice"))
				if isErr || text != "remote got accept Alice" {
					t.Fatalf("got %q (isError %v)", text, isErr)
				}
			})
		}
	}
}

// The request-scoped RemoteProvider relays too.
func TestFederatedElicitation_RemoteProvider(t *testing.T) {
	for _, legacy := range []bool{true, false} {
		for _, downName := range []string{"legacy-http", "modern-http"} {
			t.Run(fmt.Sprintf("upLegacy=%v/down=%s", legacy, downName), func(t *testing.T) {
				url := upstreamURL(t, newUpstream(t, nil), legacy)
				provider := NewRemoteProvider(func(ctx context.Context) ([]RemoteProviderConfig, error) {
					return []RemoteProviderConfig{{Name: "up", URL: url}}, nil
				})
				p := newProxy(t, nil)
				p.wrap = func(ctx context.Context) context.Context { return WithToolProviders(ctx, provider) }
				text, isErr := downstreams[downName](t, p, elicitCaps, "up__remote_ask", nil, answerName("Pat"))
				if isErr || text != "remote got accept Pat" {
					t.Fatalf("got %q (isError %v)", text, isErr)
				}
			})
		}
	}
}

// execute_tool on a discoverable remote, and the namespace fallback to the
// remote's own execute_tool for a tool hidden on the remote.
func TestFederatedElicitation_ExecuteToolAndFallback(t *testing.T) {
	for _, legacy := range []bool{true, false} {
		for _, downName := range []string{"legacy-http", "modern-http"} {
			t.Run(fmt.Sprintf("upLegacy=%v/down=%s", legacy, downName), func(t *testing.T) {
				c := upstreamClient(t, newUpstream(t, nil), "http", legacy)
				p := newProxy(t, func(s *Server) {
					if err := s.RegisterRemoteServerDiscoverable(c); err != nil {
						t.Fatal(err)
					}
				})
				text, _ := downstreams[downName](t, p, elicitCaps, ExecuteToolName, map[string]any{"name": "up__remote_ask"}, answerName("Exe"))
				if text != "remote got accept Exe" {
					t.Fatalf("execute_tool: %q", text)
				}
				text, _ = downstreams[downName](t, p, elicitCaps, "up__remote_hidden", nil, answerName("Hid"))
				if text != "remote got accept Hid" {
					t.Fatalf("fallback: %q", text)
				}
			})
		}
	}
}

func TestFederatedElicitation_DeclineAndCancel(t *testing.T) {
	for _, legacy := range []bool{true, false} {
		for _, tc := range []struct {
			down   string
			action ElicitAction
		}{{"legacy-http", ElicitDecline}, {"modern-http", ElicitCancel}, {"legacy-stdio", ElicitCancel}, {"modern-stdio", ElicitDecline}} {
			t.Run(fmt.Sprintf("upLegacy=%v/%s/%s", legacy, tc.down, tc.action), func(t *testing.T) {
				c := upstreamClient(t, newUpstream(t, nil), "http", legacy)
				p := newProxy(t, registerRemote(c))
				text, isErr := downstreams[tc.down](t, p, elicitCaps, "up__remote_ask", nil, answerAction(tc.action))
				if want := "remote got " + string(tc.action) + " <nil>"; isErr || text != want {
					t.Fatalf("got %q (isError %v), want %q", text, isErr, want)
				}
			})
		}
	}
}

// A caller that did not declare elicitation is never asked: a Legacy remote
// gets action "cancel" at once; a Modern remote is not told the client can
// elicit, so its own Elicit reports elicitation unsupported.
func TestFederatedElicitation_CallerCannotBeAsked(t *testing.T) {
	for _, tc := range []struct {
		legacy bool
		down   string
		want   string
		isErr  bool
	}{
		{true, "legacy-http", "remote got cancel <nil>", false},
		{true, "modern-http", "remote got cancel <nil>", false},
		{false, "legacy-http", "not supported", true},
		{false, "modern-stdio", "not supported", true},
	} {
		t.Run(fmt.Sprintf("upLegacy=%v/%s", tc.legacy, tc.down), func(t *testing.T) {
			c := upstreamClient(t, newUpstream(t, nil), "http", tc.legacy)
			p := newProxy(t, registerRemote(c))
			done := make(chan struct{})
			go func() {
				defer close(done)
				text, isErr := downstreams[tc.down](t, p, map[string]any{}, "up__remote_ask", nil, func(int, map[string]any) map[string]any {
					t.Error("a caller without the capability was asked")
					return accept("x")
				})
				if isErr != tc.isErr || !strings.Contains(text, tc.want) {
					t.Errorf("got %q (isError %v)", text, isErr)
				}
			}()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("hung")
			}
		})
	}
}

// Two callers' forwarded calls share one upstream Client; each is asked
// its own question and gets its own answer back.
func TestFederatedElicitation_ConcurrentCallsShareAClient(t *testing.T) {
	for _, transport := range []string{"http", "stdio"} {
		t.Run(transport, func(t *testing.T) {
			c := upstreamClient(t, newUpstream(t, nil), transport, true)
			p := newProxy(t, registerRemote(c))
			url := p.url(t)

			var asked sync.WaitGroup
			asked.Add(2)
			release := make(chan struct{})
			results := make([]string, 2)
			var wg sync.WaitGroup
			for i, name := range []string{"Ann", "Bob"} {
				wg.Add(1)
				go func(i int, name string) {
					defer wg.Done()
					dc := newLegacyHTTPClient(t, url, "2025-11-25", elicitCaps)
					msgs := dc.call(2, "tools/call", map[string]any{"name": "up__remote_ask", "arguments": map[string]any{"who": name}}, sseAccept)
					for {
						m := nextMessage(t, msgs)
						if m["method"] == "elicitation/create" {
							asked.Done()
							<-release // both calls are waiting on their question
							dc.respond(m["id"], accept(name))
							continue
						}
						if m["method"] != nil {
							continue
						}
						results[i], _ = toolText(t, m)
						return
					}
				}(i, name)
			}
			asked.Wait()
			close(release)
			wg.Wait()
			if results[0] != "remote got accept Ann" || results[1] != "remote got accept Bob" {
				t.Fatalf("answers crossed: %v", results)
			}
		})
	}
}

// Modern on both hops: the remote's requestState travels inside the proxy's
// own, so each of the caller's retries resumes the remote's exchange rather
// than calling the remote afresh.
func TestFederatedElicitation_RemoteRequestStateCarried(t *testing.T) {
	for _, down := range []string{"modern-http", "modern-stdio"} {
		t.Run(down, func(t *testing.T) {
			var runs atomic.Int32
			c := upstreamClient(t, newUpstream(t, &runs), "http", false)
			p := newProxy(t, registerRemote(c))
			var keys []string
			text, isErr := downstreams[down](t, p, elicitCaps, "up__remote_two", nil, func(i int, params map[string]any) map[string]any {
				keys = append(keys, params["message"].(string))
				return accept([]string{"one", "two"}[i])
			})
			if isErr || text != "remote got accept one/accept two" {
				t.Fatalf("got %q (isError %v)", text, isErr)
			}
			// Remote runs: the first call, the retry answering First, the
			// retry answering Second. A proxy that started over would add more.
			if n := runs.Load(); n != 3 {
				t.Fatalf("remote handler ran %d times, want 3", n)
			}
			if strings.Join(keys, ",") != "First?,Second?" {
				t.Fatalf("questions = %v", keys)
			}
		})
	}
}

// The relayed keys and the proxy's requestState: a tampered state is
// refused before the remote is contacted.
func TestFederatedElicitation_ProxyStateIsProtected(t *testing.T) {
	c := upstreamClient(t, newUpstream(t, nil), "http", false)
	p := newProxy(t, registerRemote(c))
	url := p.url(t)
	params := map[string]any{"name": "up__remote_two", "arguments": map[string]any{}}
	reqs, state := inputRequired(t, modernElicitCall(t, url, "tools/call", params, elicitCaps))
	if _, ok := reqs["relay-1.0.elicitation-1"]; !ok || len(reqs) != 1 {
		t.Fatalf("relayed keys = %v", reqs)
	}
	params["inputResponses"] = map[string]any{"relay-1.0.elicitation-1": accept("one")}
	params["requestState"] = state + "x"
	if code, _ := wireError(t, modernElicitCall(t, url, "tools/call", params, elicitCaps)); code != ErrorCodeInvalidParams {
		t.Fatalf("tampered state: code %d", code)
	}
}

// A Modern remote asking for sampling (not implemented) fails the call
// instead of being relayed.
func TestFederatedElicitation_SamplingNotRelayed(t *testing.T) {
	up := newUpstream(t, nil)
	up.RegisterTool(NewTool("remote_sampler", "asks for sampling"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("unreachable"), nil
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(`"remote_sampler"`)) && bytes.Contains(body, []byte(`"tools/call"`)) {
			var rpc struct {
				ID any `json:"id"`
			}
			json.Unmarshal(body, &rpc)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": map[string]any{
				"resultType": "input_required",
				"inputRequests": map[string]any{"llm": map[string]any{"method": "sampling/createMessage",
					"params": map[string]any{"messages": []any{}, "maxTokens": 10}}},
				"requestState": "opaque",
			}})
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		up.HandleRequest(w, r)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, nil, "up")
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := newProxy(t, registerRemote(c))
	text, isErr := driveModernHTTP(t, p, elicitCaps, "up__remote_sampler", nil, answerName("x"))
	if !isErr || !strings.Contains(text, "sampling/createMessage") {
		t.Fatalf("got %q (isError %v)", text, isErr)
	}
}

// Without a related-request hint a stdio request is tied to a call only when
// it is the only one in flight; otherwise the answer is "cancel", never a
// guess.
func TestFederatedElicitation_AmbiguousRequestIsCancelled(t *testing.T) {
	c := &Client{}
	ctx, _, _ := NewServer("x", "1").beginElicitation(withModernClientContext(context.Background(), elicitCaps, nil), "tools/call", json.RawMessage(`{"name":"x"}`), true, "", nil)
	a := &elicitRelay{ctx: ctx, slot: "relay-1", canAsk: true, cancel: func() {}}
	untrackA := c.trackRelay(int64(1), a)
	if got := c.relayFor(nil, false); got != a {
		t.Fatal("the only call in flight was not chosen")
	}
	untrackB := c.trackRelay(int64(2), &elicitRelay{ctx: ctx, canAsk: true, cancel: func() {}})
	defer untrackB()
	if got := c.relayFor(nil, false); got != nil {
		t.Fatal("an ambiguous request was assigned to a call")
	}
	result, rpcErr := c.answerElicitation(c.relayFor(nil, false), json.RawMessage(`{"message":"?","requestedSchema":{"type":"object","properties":{}}}`))
	if rpcErr != nil || result.(map[string]any)["action"] != "cancel" {
		t.Fatalf("got %v %v", result, rpcErr)
	}
	if got := c.relayFor(float64(1), true); got != a {
		t.Fatal("the hinted call was not chosen")
	}
	untrackA()
}
