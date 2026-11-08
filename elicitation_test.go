package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Elicitation, both eras: elicitation/create over the request stream on
// 2025-06-18/2025-11-25 (HTTP and stdio), InputRequiredResult with signed
// requestState on 2026-07-28.

var testNameSchema = map[string]any{
	"type":       "object",
	"properties": map[string]any{"name": map[string]any{"type": "string"}},
	"required":   []string{"name"},
}

func describeElicit(res *ElicitResult) string {
	return fmt.Sprintf("%s %v", res.Action, res.Content["name"])
}

// newElicitServer registers a tool, prompt, resource and discoverable tool
// that elicit, plus tools for multi-round and batched questions. errs
// receives the error each "ask" call's Elicit returned, when non-nil.
func newElicitServer(t *testing.T, errs chan error) *Server {
	t.Helper()
	s := NewServer("elicit-test", "1")
	sm, err := NewJWTSessionManagerWithAutoKey(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	s.SetSessionManager(sm)
	s.SetResponseStreaming(time.Minute, time.Minute)

	ask := func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		res, err := Elicit(ctx, ElicitForm{Key: "name", Message: "Your name?", Schema: testNameSchema})
		if err != nil {
			if errs != nil {
				errs <- err
			}
			return nil, err
		}
		return NewToolResponseText(describeElicit(res)), nil
	}
	s.RegisterTool(NewTool("ask", "asks for a name", String("topic", "topic")), ask)
	s.RegisterTool(NewTool("hidden_ask", "asks for a name, found via tool_search").Discoverable("hidden"), ask)
	s.RegisterTool(NewTool("two_rounds", "asks twice"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		first, err := Elicit(ctx, ElicitForm{Message: "First?", Schema: testNameSchema})
		if err != nil {
			return nil, err
		}
		second, err := Elicit(ctx, ElicitForm{Message: "Second?", Schema: testNameSchema})
		if err != nil {
			return nil, err
		}
		return NewToolResponseText(describeElicit(first) + "/" + describeElicit(second)), nil
	})
	s.RegisterTool(NewTool("both", "asks two at once"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		res, err := ElicitAll(ctx,
			ElicitForm{Key: "a", Message: "A?", Schema: testNameSchema},
			ElicitForm{Key: "b", Message: "B?", Schema: testNameSchema})
		if err != nil {
			return nil, err
		}
		return NewToolResponseText(describeElicit(res[0]) + "/" + describeElicit(res[1])), nil
	})
	s.RegisterPrompt(NewPrompt("greet", "asks, then greets"), func(ctx context.Context, req *PromptRequest) (*PromptResponse, error) {
		res, err := Elicit(ctx, ElicitForm{Key: "name", Message: "Your name?", Schema: testNameSchema})
		if err != nil {
			return nil, err
		}
		return NewPromptResponseText("Hello " + describeElicit(res)), nil
	})
	s.RegisterResource(NewResource("test://asked", "asked", "asks, then reads", "text/plain"), func(ctx context.Context, req *ResourceRequest) (*ResourceResponse, error) {
		res, err := Elicit(ctx, ElicitForm{Key: "name", Message: "Your name?", Schema: testNameSchema})
		if err != nil {
			return nil, err
		}
		return NewResourceResponseText(req.URI(), "read by "+describeElicit(res), "text/plain"), nil
	})
	return s
}

// ---- Legacy HTTP client ----

type legacyHTTPClient struct {
	t       *testing.T
	url     string
	version string
	sid     string
}

func newLegacyHTTPClient(t *testing.T, url, version string, caps map[string]any) *legacyHTTPClient {
	t.Helper()
	c := &legacyHTTPClient{t: t, url: url, version: version}
	resp := c.post(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": version, "capabilities": caps, "clientInfo": map[string]any{"name": "t", "version": "1"},
	}}, "application/json")
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	c.sid = resp.Header.Get(headerSessionID)
	if c.sid == "" {
		t.Fatal("initialize returned no session id")
	}
	return c
}

func (c *legacyHTTPClient) post(body any, accept string) *http.Response {
	c.t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, c.url, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", accept)
	req.Header.Set(headerProtocolVersion, c.version)
	if c.sid != "" {
		req.Header.Set(headerSessionID, c.sid)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	return resp
}

// call POSTs a request and returns its messages as they arrive: the SSE
// events of a streamed response, or the one JSON body.
func (c *legacyHTTPClient) call(id int, method string, params map[string]any, accept string) <-chan map[string]any {
	c.t.Helper()
	resp := c.post(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}, accept)
	ch := make(chan map[string]any, 16)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			var m map[string]any
			if json.NewDecoder(resp.Body).Decode(&m) == nil {
				ch <- m
			}
			return
		}
		br := bufio.NewReader(resp.Body)
		var data strings.Builder
		for {
			line, err := br.ReadString('\n')
			line = strings.TrimRight(line, "\r\n")
			switch {
			case strings.HasPrefix(line, "data: "):
				data.WriteString(line[len("data: "):])
			case line == "" && data.Len() > 0:
				var m map[string]any
				if json.Unmarshal([]byte(data.String()), &m) == nil {
					ch <- m
				}
				data.Reset()
			}
			if err != nil {
				return
			}
		}
	}()
	return ch
}

// respond POSTs the client's JSON-RPC response to a server request.
func (c *legacyHTTPClient) respond(id any, result any) {
	c.t.Helper()
	resp := c.post(map[string]any{"jsonrpc": "2.0", "id": id, "result": result}, "application/json")
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		c.t.Fatalf("response POST: status %d, want 202", resp.StatusCode)
	}
}

func nextMessage(t *testing.T, ch <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case m, ok := <-ch:
		if !ok {
			t.Fatal("stream ended")
		}
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no message within 5s")
	}
	return nil
}

// toolText returns a tools/call (or prompts/get, resources/read) result's
// first text and isError.
func toolText(t *testing.T, msg map[string]any) (string, bool) {
	t.Helper()
	result, ok := msg["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", msg)
	}
	isErr, _ := result["isError"].(bool)
	if content, ok := result["content"].([]any); ok && len(content) > 0 {
		text, _ := content[0].(map[string]any)["text"].(string)
		return text, isErr
	}
	if msgs, ok := result["messages"].([]any); ok && len(msgs) > 0 {
		c := msgs[0].(map[string]any)["content"].(map[string]any)
		return c["text"].(string), false
	}
	if contents, ok := result["contents"].([]any); ok && len(contents) > 0 {
		return contents[0].(map[string]any)["text"].(string), false
	}
	t.Fatalf("result has no text: %v", result)
	return "", false
}

var elicitCaps = map[string]any{"elicitation": map[string]any{}}

const sseAccept = "application/json, text/event-stream"

func TestElicitLegacyHTTP_ActionsRoundTrip(t *testing.T) {
	for _, version := range []string{"2025-06-18", "2025-11-25"} {
		for _, tc := range []struct {
			action ElicitAction
			result map[string]any
			want   string
		}{
			{ElicitAccept, map[string]any{"action": "accept", "content": map[string]any{"name": "Alice"}}, "accept Alice"},
			{ElicitDecline, map[string]any{"action": "decline"}, "decline <nil>"},
			{ElicitCancel, map[string]any{"action": "cancel"}, "cancel <nil>"},
		} {
			t.Run(version+"/"+string(tc.action), func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(newElicitServer(t, nil).HandleRequest))
				defer srv.Close()
				c := newLegacyHTTPClient(t, srv.URL, version, elicitCaps)

				msgs := c.call(2, "tools/call", map[string]any{"name": "ask", "arguments": map[string]any{}}, sseAccept)
				req := nextMessage(t, msgs)
				if req["method"] != "elicitation/create" || req["id"] == nil {
					t.Fatalf("want an elicitation/create request first, got %v", req)
				}
				params := req["params"].(map[string]any)
				if params["message"] != "Your name?" || params["requestedSchema"].(map[string]any)["type"] != "object" {
					t.Fatalf("params = %v", params)
				}
				// mode arrived in 2025-11-25; 2025-06-18 has no such field.
				if mode, has := params["mode"]; (version == "2025-11-25") != has || (has && mode != "form") {
					t.Fatalf("%s: mode = %v (present %v)", version, mode, has)
				}
				c.respond(req["id"], tc.result)
				text, isErr := toolText(t, nextMessage(t, msgs))
				if isErr || text != tc.want {
					t.Fatalf("result = %q (isError %v), want %q", text, isErr, tc.want)
				}
			})
		}
	}
}

func TestElicitLegacyHTTP_PromptAndResource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(newElicitServer(t, nil).HandleRequest))
	defer srv.Close()
	c := newLegacyHTTPClient(t, srv.URL, "2025-11-25", elicitCaps)
	for i, tc := range []struct {
		method string
		params map[string]any
		want   string
	}{
		{"prompts/get", map[string]any{"name": "greet"}, "Hello accept Bob"},
		{"resources/read", map[string]any{"uri": "test://asked"}, "read by accept Bob"},
	} {
		msgs := c.call(10+i, tc.method, tc.params, sseAccept)
		req := nextMessage(t, msgs)
		if req["method"] != "elicitation/create" {
			t.Fatalf("%s: got %v", tc.method, req)
		}
		c.respond(req["id"], map[string]any{"action": "accept", "content": map[string]any{"name": "Bob"}})
		if text, _ := toolText(t, nextMessage(t, msgs)); text != tc.want {
			t.Fatalf("%s: %q, want %q", tc.method, text, tc.want)
		}
	}
}

func TestElicitLegacyHTTP_ExecuteTool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(newElicitServer(t, nil).HandleRequest))
	defer srv.Close()
	c := newLegacyHTTPClient(t, srv.URL, "2025-11-25", elicitCaps)
	msgs := c.call(2, "tools/call", map[string]any{"name": ExecuteToolName, "arguments": map[string]any{"name": "hidden_ask"}}, sseAccept)
	req := nextMessage(t, msgs)
	if req["method"] != "elicitation/create" {
		t.Fatalf("got %v", req)
	}
	c.respond(req["id"], map[string]any{"action": "accept", "content": map[string]any{"name": "Eve"}})
	if text, _ := toolText(t, nextMessage(t, msgs)); text != "accept Eve" {
		t.Fatalf("got %q", text)
	}
}

// Elicit is only attempted when the revision, the client's capabilities and
// the transport allow it; otherwise ErrElicitationUnsupported.
func TestElicitLegacyHTTP_Unsupported(t *testing.T) {
	for _, tc := range []struct {
		name, version, accept string
		caps                  map[string]any
	}{
		{"no capability", "2025-11-25", sseAccept, map[string]any{}},
		{"url mode only", "2025-11-25", sseAccept, map[string]any{"elicitation": map[string]any{"url": map[string]any{}}}},
		{"2025-03-26 has none", "2025-03-26", sseAccept, elicitCaps},
		{"no SSE accepted", "2025-11-25", "application/json", elicitCaps},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := make(chan error, 1)
			srv := httptest.NewServer(http.HandlerFunc(newElicitServer(t, errs).HandleRequest))
			defer srv.Close()
			c := newLegacyHTTPClient(t, srv.URL, tc.version, tc.caps)
			text, isErr := toolText(t, nextMessage(t, c.call(2, "tools/call", map[string]any{"name": "ask", "arguments": map[string]any{}}, tc.accept)))
			if !isErr || !strings.Contains(text, "not supported") {
				t.Fatalf("got %q (isError %v)", text, isErr)
			}
			if err := <-errs; !errors.Is(err, ErrElicitationUnsupported) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

// No answer within the timeout: the server cancels its request
// (notifications/cancelled) and stops waiting. A response from another
// session never reaches the handler.
func TestElicitLegacyHTTP_TimeoutAndSessionScope(t *testing.T) {
	errs := make(chan error, 1)
	s := newElicitServer(t, errs)
	s.SetElicitationTimeout(300 * time.Millisecond)
	srv := httptest.NewServer(http.HandlerFunc(s.HandleRequest))
	defer srv.Close()
	c := newLegacyHTTPClient(t, srv.URL, "2025-11-25", elicitCaps)
	other := newLegacyHTTPClient(t, srv.URL, "2025-11-25", elicitCaps)

	msgs := c.call(2, "tools/call", map[string]any{"name": "ask", "arguments": map[string]any{}}, sseAccept)
	req := nextMessage(t, msgs)
	other.respond(req["id"], map[string]any{"action": "accept", "content": map[string]any{"name": "Mallory"}})

	cancelled := nextMessage(t, msgs)
	if cancelled["method"] != "notifications/cancelled" || cancelled["params"].(map[string]any)["requestId"] != req["id"] {
		t.Fatalf("want notifications/cancelled for %v, got %v", req["id"], cancelled)
	}
	if text, isErr := toolText(t, nextMessage(t, msgs)); !isErr || !strings.Contains(text, "timed out") {
		t.Fatalf("got %q (isError %v)", text, isErr)
	}
	if err := <-errs; !errors.Is(err, ErrElicitationTimeout) {
		t.Fatalf("err = %v", err)
	}
	if p := s.httpPeer(c.sid); p == nil || pendingCount(p) != 0 {
		t.Fatalf("pending responses not cleaned up: %v", p)
	}
	c.respond(req["id"], map[string]any{"action": "accept", "content": map[string]any{"name": "late"}}) // dropped, still 202
}

// notifications/cancelled for the originating request stops the wait.
func TestElicitLegacyHTTP_Cancellation(t *testing.T) {
	errs := make(chan error, 1)
	s := newElicitServer(t, errs)
	srv := httptest.NewServer(http.HandlerFunc(s.HandleRequest))
	defer srv.Close()
	c := newLegacyHTTPClient(t, srv.URL, "2025-11-25", elicitCaps)

	msgs := c.call(7, "tools/call", map[string]any{"name": "ask", "arguments": map[string]any{}}, sseAccept)
	req := nextMessage(t, msgs)
	resp := c.post(map[string]any{"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]any{"requestId": 7}}, "application/json")
	resp.Body.Close()
	select {
	case err := <-errs:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler still waiting after cancellation")
	}
	for m := range msgs {
		if _, isResult := m["result"]; isResult {
			t.Fatalf("a cancelled request got a response: %v", m)
		}
	}
	time.Sleep(10 * time.Millisecond)
	if p := s.httpPeer(c.sid); p == nil || pendingCount(p) != 0 {
		t.Fatal("pending responses not cleaned up")
	}
	c.respond(req["id"], map[string]any{"action": "cancel"})
}

func TestElicitLegacyHTTP_BadAnswers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer map[string]any
		check  func(error) bool
	}{
		{"content fails schema", map[string]any{"jsonrpc": "2.0", "result": map[string]any{"action": "accept", "content": map[string]any{"name": 42}}},
			func(err error) bool { return errors.Is(err, ErrInvalidElicitationResult) }},
		{"unknown action", map[string]any{"jsonrpc": "2.0", "result": map[string]any{"action": "maybe"}},
			func(err error) bool { return errors.Is(err, ErrInvalidElicitationResult) }},
		{"JSON-RPC error", map[string]any{"jsonrpc": "2.0", "error": map[string]any{"code": -32601, "message": "nope"}},
			func(err error) bool { var e *ElicitationError; return errors.As(err, &e) && e.Code == -32601 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := make(chan error, 1)
			srv := httptest.NewServer(http.HandlerFunc(newElicitServer(t, errs).HandleRequest))
			defer srv.Close()
			c := newLegacyHTTPClient(t, srv.URL, "2025-11-25", elicitCaps)
			msgs := c.call(2, "tools/call", map[string]any{"name": "ask", "arguments": map[string]any{}}, sseAccept)
			req := nextMessage(t, msgs)
			tc.answer["id"] = req["id"]
			resp := c.post(tc.answer, "application/json")
			resp.Body.Close()
			if _, isErr := toolText(t, nextMessage(t, msgs)); !isErr {
				t.Fatal("want a tool error")
			}
			if err := <-errs; !tc.check(err) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func pendingCount(p *clientPeer) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pending)
}

// ---- Legacy stdio ----

func TestElicitLegacyStdio_RoundTrip(t *testing.T) {
	h := newStdioHarness(t, newElicitServer(t, nil))
	h.send(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{"elicitation":{}},"clientInfo":{"name":"t","version":"1"}}}`)
	h.next(t, 2*time.Second)

	for i, tc := range []struct{ call, want string }{
		{`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ask","arguments":{}}}`, "accept Ann"},
		{`{"jsonrpc":"2.0","id":3,"method":"prompts/get","params":{"name":"greet"}}`, "Hello accept Ann"},
		{`{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"test://asked"}}`, "read by accept Ann"},
	} {
		h.send(t, tc.call)
		var req map[string]any
		json.Unmarshal([]byte(h.next(t, 2*time.Second)), &req)
		if req["method"] != "elicitation/create" {
			t.Fatalf("%d: want elicitation/create, got %v", i, req)
		}
		if _, has := req["params"].(map[string]any)["mode"]; has {
			t.Fatal("2025-06-18 request carries mode")
		}
		id, _ := json.Marshal(req["id"])
		h.send(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"action":"accept","content":{"name":"Ann"}}}`, id))
		var resp map[string]any
		json.Unmarshal([]byte(h.next(t, 2*time.Second)), &resp)
		if text, _ := toolText(t, resp); text != tc.want {
			t.Fatalf("%d: %q, want %q", i, text, tc.want)
		}
	}
}

func TestElicitLegacyStdio_TimeoutAndCancel(t *testing.T) {
	errs := make(chan error, 2)
	s := newElicitServer(t, errs)
	s.SetElicitationTimeout(200 * time.Millisecond)
	h := newStdioHarness(t, s)
	h.send(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{"elicitation":{"form":{}}},"clientInfo":{"name":"t","version":"1"}}}`)
	h.next(t, 2*time.Second)

	// Timeout: request, then notifications/cancelled, then the tool error.
	h.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ask","arguments":{}}}`)
	var req, cancelled, resp map[string]any
	json.Unmarshal([]byte(h.next(t, 2*time.Second)), &req)
	json.Unmarshal([]byte(h.next(t, 2*time.Second)), &cancelled)
	if req["params"].(map[string]any)["mode"] != "form" {
		t.Fatalf("2025-11-25 request without mode: %v", req)
	}
	if cancelled["method"] != "notifications/cancelled" || cancelled["params"].(map[string]any)["requestId"] != req["id"] {
		t.Fatalf("want notifications/cancelled, got %v", cancelled)
	}
	json.Unmarshal([]byte(h.next(t, 2*time.Second)), &resp)
	if text, isErr := toolText(t, resp); !isErr || !strings.Contains(text, "timed out") {
		t.Fatalf("got %q", text)
	}
	if err := <-errs; !errors.Is(err, ErrElicitationTimeout) {
		t.Fatalf("err = %v", err)
	}

	// Cancellation of the originating request: no response at all.
	s.SetElicitationTimeout(-1)
	h.send(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ask","arguments":{}}}`)
	json.Unmarshal([]byte(h.next(t, 2*time.Second)), &req)
	h.send(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":3}}`)
	if err := <-errs; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	id, _ := json.Marshal(req["id"])
	h.send(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"action":"cancel"}}`, id)) // late: dropped
	h.none(t, 200*time.Millisecond)
}

// ---- 2026-07-28: InputRequiredResult ----

// modernElicitCall POSTs a 2026-07-28 request with the given client capabilities
// and returns the decoded response.
func modernElicitCall(t *testing.T, url, method string, params map[string]any, caps map[string]any, headers ...string) map[string]any {
	t.Helper()
	p := map[string]any{}
	for k, v := range params {
		p[k] = v
	}
	p["_meta"] = map[string]any{metaKeyProtocolVersion: MCPProtocolVersionModern, metaKeyClientCapabilities: caps}
	name, needsName := modernRequestName(method, p)
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": p})
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set(headerMcpMethod, method)
	req.Header.Set(headerProtocolVersion, MCPProtocolVersionModern)
	if needsName {
		req.Header.Set(headerMcpName, encodeModernHeaderValue(name))
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// inputRequired checks msg is an InputRequiredResult and returns its
// inputRequests and requestState.
func inputRequired(t *testing.T, msg map[string]any) (map[string]any, string) {
	t.Helper()
	result, _ := msg["result"].(map[string]any)
	if result == nil || result["resultType"] != "input_required" {
		t.Fatalf("want an InputRequiredResult, got %v", msg)
	}
	if _, has := result["ttlMs"]; has {
		t.Fatal("InputRequiredResult carries caching hints")
	}
	reqs, _ := result["inputRequests"].(map[string]any)
	state, _ := result["requestState"].(string)
	if len(reqs) == 0 || state == "" {
		t.Fatalf("missing inputRequests or requestState: %v", result)
	}
	for key, r := range reqs {
		rm := r.(map[string]any)
		params := rm["params"].(map[string]any)
		if rm["method"] != "elicitation/create" || params["mode"] != "form" || params["requestedSchema"] == nil {
			t.Fatalf("inputRequests[%s] = %v", key, rm)
		}
	}
	return reqs, state
}

func wireError(t *testing.T, msg map[string]any) (int, string) {
	t.Helper()
	e, _ := msg["error"].(map[string]any)
	if e == nil {
		t.Fatalf("want a JSON-RPC error, got %v", msg)
	}
	details, _ := e["data"].(map[string]any)["details"].(string)
	return int(e["code"].(float64)), details
}

func accept(name string) map[string]any {
	return map[string]any{"action": "accept", "content": map[string]any{"name": name}}
}

func TestElicitModern_ToolPromptResourceExecuteTool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(newElicitServer(t, nil).HandleRequest))
	defer srv.Close()
	for _, tc := range []struct {
		method string
		params map[string]any
		want   string
	}{
		{"tools/call", map[string]any{"name": "ask", "arguments": map[string]any{}}, "accept Alice"},
		{"tools/call", map[string]any{"name": ExecuteToolName, "arguments": map[string]any{"name": "hidden_ask"}}, "accept Alice"},
		{"prompts/get", map[string]any{"name": "greet"}, "Hello accept Alice"},
		{"resources/read", map[string]any{"uri": "test://asked"}, "read by accept Alice"},
	} {
		reqs, state := inputRequired(t, modernElicitCall(t, srv.URL, tc.method, tc.params, elicitCaps))
		if _, ok := reqs["name"]; !ok {
			t.Fatalf("%s: key %q missing from %v", tc.method, "name", reqs)
		}
		retry := map[string]any{"inputResponses": map[string]any{"name": accept("Alice")}, "requestState": state}
		for k, v := range tc.params {
			retry[k] = v
		}
		msg := modernElicitCall(t, srv.URL, tc.method, retry, elicitCaps)
		if msg["result"].(map[string]any)["resultType"] != "complete" {
			t.Fatalf("%s: retry not complete: %v", tc.method, msg)
		}
		if text, _ := toolText(t, msg); text != tc.want {
			t.Fatalf("%s: %q, want %q", tc.method, text, tc.want)
		}
	}
}

// Answers from earlier rounds travel in requestState: the last round only
// carries the last answer, yet the handler sees both.
func TestElicitModern_MultiRound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(newElicitServer(t, nil).HandleRequest))
	defer srv.Close()
	call := map[string]any{"name": "two_rounds", "arguments": map[string]any{}}
	reqs, state1 := inputRequired(t, modernElicitCall(t, srv.URL, "tools/call", call, elicitCaps))
	if _, ok := reqs["elicitation-1"]; !ok || len(reqs) != 1 {
		t.Fatalf("round 1 = %v", reqs)
	}
	call["inputResponses"], call["requestState"] = map[string]any{"elicitation-1": accept("one")}, state1
	reqs, state2 := inputRequired(t, modernElicitCall(t, srv.URL, "tools/call", call, elicitCaps))
	if _, ok := reqs["elicitation-2"]; !ok || len(reqs) != 1 || state2 == state1 {
		t.Fatalf("round 2 = %v", reqs)
	}
	call["inputResponses"], call["requestState"] = map[string]any{"elicitation-2": accept("two")}, state2
	if text, _ := toolText(t, modernElicitCall(t, srv.URL, "tools/call", call, elicitCaps)); text != "accept one/accept two" {
		t.Fatalf("got %q", text)
	}
}

func TestElicitModern_ElicitAllBatchesQuestions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(newElicitServer(t, nil).HandleRequest))
	defer srv.Close()
	call := map[string]any{"name": "both", "arguments": map[string]any{}}
	reqs, state := inputRequired(t, modernElicitCall(t, srv.URL, "tools/call", call, elicitCaps))
	if len(reqs) != 2 || reqs["a"] == nil || reqs["b"] == nil {
		t.Fatalf("want a and b in one round, got %v", reqs)
	}
	// Only a answered: b is re-requested, a is carried.
	call["inputResponses"], call["requestState"] = map[string]any{"a": accept("A")}, state
	reqs, state = inputRequired(t, modernElicitCall(t, srv.URL, "tools/call", call, elicitCaps))
	if len(reqs) != 1 || reqs["b"] == nil {
		t.Fatalf("want b re-requested, got %v", reqs)
	}
	call["inputResponses"], call["requestState"] = map[string]any{"b": map[string]any{"action": "decline"}}, state
	if text, _ := toolText(t, modernElicitCall(t, srv.URL, "tools/call", call, elicitCaps)); text != "accept A/decline <nil>" {
		t.Fatalf("got %q", text)
	}
}

// A missing answer gets a new InputRequiredResult; extra responses are
// ignored; neither needs requestState.
func TestElicitModern_MissingAndExtraResponses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(newElicitServer(t, nil).HandleRequest))
	defer srv.Close()
	reqs, _ := inputRequired(t, modernElicitCall(t, srv.URL, "tools/call", map[string]any{
		"name": "ask", "arguments": map[string]any{}, "inputResponses": map[string]any{"wrong_key": accept("x")},
	}, elicitCaps))
	if reqs["name"] == nil {
		t.Fatalf("want name re-requested, got %v", reqs)
	}
	msg := modernElicitCall(t, srv.URL, "tools/call", map[string]any{
		"name": "ask", "arguments": map[string]any{},
		"inputResponses": map[string]any{"name": accept("Zed"), "unknown": map[string]any{"foo": 1}, "other": 12345},
	}, elicitCaps)
	if text, _ := toolText(t, msg); text != "accept Zed" {
		t.Fatalf("got %v", msg)
	}
}

// Servers MUST NOT send an inputRequest the client has not declared.
func TestElicitModern_CapabilityCheck(t *testing.T) {
	errs := make(chan error, 2)
	srv := httptest.NewServer(http.HandlerFunc(newElicitServer(t, errs).HandleRequest))
	defer srv.Close()
	for _, caps := range []map[string]any{{"sampling": map[string]any{}}, {"elicitation": map[string]any{"url": map[string]any{}}}} {
		msg := modernElicitCall(t, srv.URL, "tools/call", map[string]any{"name": "ask", "arguments": map[string]any{}}, caps)
		result := msg["result"].(map[string]any)
		if result["resultType"] != "complete" || result["isError"] != true {
			t.Fatalf("caps %v: want a complete error result, got %v", caps, msg)
		}
		if err := <-errs; !errors.Is(err, ErrElicitationUnsupported) {
			t.Fatalf("err = %v", err)
		}
	}
}

func TestElicitModern_RequestStateRejected(t *testing.T) {
	s := newElicitServer(t, nil)
	key := []byte("0123456789abcdef0123456789abcdef")
	s.SetRequestStateKey(key)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sub := r.Header.Get("X-Test-Subject"); sub != "" {
			r = r.WithContext(WithTokenInfo(r.Context(), &TokenInfo{Subject: sub}))
		}
		s.HandleRequest(w, r)
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	call := func(args map[string]any) map[string]any {
		return map[string]any{"name": "ask", "arguments": args}
	}
	_, state := inputRequired(t, modernElicitCall(t, srv.URL, "tools/call", call(map[string]any{"topic": "a"}), elicitCaps, "X-Test-Subject", "alice"))
	retry := func(args map[string]any, state any, subject string) map[string]any {
		p := call(args)
		p["inputResponses"] = map[string]any{"name": accept("A")}
		p["requestState"] = state
		return modernElicitCall(t, srv.URL, "tools/call", p, elicitCaps, "X-Test-Subject", subject)
	}

	if text, _ := toolText(t, retry(map[string]any{"topic": "a"}, state, "alice")); text != "accept A" {
		t.Fatalf("valid state refused: %q", text)
	}
	expired := signRequestState(key, requestStatePayload{V: 1, M: "tools/call", P: "alice", E: time.Now().Add(-time.Minute).Unix(),
		D: salientDigest(call(map[string]any{"topic": "a"}))})
	for _, tc := range []struct {
		name    string
		msg     map[string]any
		details string
	}{
		{"tampered", retry(map[string]any{"topic": "a"}, state+"-TAMPERED", "alice"), "integrity"},
		{"forged with another key", retry(map[string]any{"topic": "a"}, signRequestState([]byte("other"), requestStatePayload{V: 1, M: "tools/call", P: "alice", E: time.Now().Add(time.Hour).Unix(), D: salientDigest(call(map[string]any{"topic": "a"}))}), "alice"), "integrity"},
		{"expired", retry(map[string]any{"topic": "a"}, expired, "alice"), "expired"},
		{"different arguments", retry(map[string]any{"topic": "b"}, state, "alice"), "different request"},
		{"different principal", retry(map[string]any{"topic": "a"}, state, "bob"), "different principal"},
		{"not a string", retry(map[string]any{"topic": "a"}, 42, "alice"), "must be a string"},
	} {
		code, details := wireError(t, tc.msg)
		if code != ErrorCodeInvalidParams || !strings.Contains(details, tc.details) {
			t.Fatalf("%s: code %d, details %q", tc.name, code, details)
		}
	}

	// A state issued for tools/call is no good on another method, even with
	// the same name and arguments.
	msg := modernElicitCall(t, srv.URL, "prompts/get", map[string]any{"name": "ask", "arguments": map[string]any{"topic": "a"},
		"inputResponses": map[string]any{"name": accept("A")}, "requestState": state}, elicitCaps, "X-Test-Subject", "alice")
	if code, details := wireError(t, msg); code != ErrorCodeInvalidParams || !strings.Contains(details, "different request") {
		t.Fatalf("other method: code %d, details %q", code, details)
	}
}

func TestElicitModern_InvalidInputResponses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(newElicitServer(t, nil).HandleRequest))
	defer srv.Close()
	for _, inputs := range []any{nil, "x", map[string]any{"name": 12345}, map[string]any{"name": map[string]any{"action": "maybe"}}} {
		msg := modernElicitCall(t, srv.URL, "tools/call", map[string]any{"name": "ask", "arguments": map[string]any{}, "inputResponses": inputs}, elicitCaps)
		if code, _ := wireError(t, msg); code != ErrorCodeInvalidParams {
			t.Fatalf("inputResponses %v: code %d", inputs, code)
		}
	}
}

// The default key is random per process; SetRequestStateKey lets several
// instances accept each other's state.
func TestElicitModern_RequestStateKeyAcrossInstances(t *testing.T) {
	a, b, c := newElicitServer(t, nil), newElicitServer(t, nil), newElicitServer(t, nil)
	shared := []byte("shared-secret-shared-secret-shared")
	a.SetRequestStateKey(shared)
	b.SetRequestStateKey(shared)
	srvA := httptest.NewServer(http.HandlerFunc(a.HandleRequest))
	srvB := httptest.NewServer(http.HandlerFunc(b.HandleRequest))
	srvC := httptest.NewServer(http.HandlerFunc(c.HandleRequest))
	defer srvA.Close()
	defer srvB.Close()
	defer srvC.Close()
	_, state := inputRequired(t, modernElicitCall(t, srvA.URL, "tools/call", map[string]any{"name": "ask", "arguments": map[string]any{}}, elicitCaps))
	retry := map[string]any{"name": "ask", "arguments": map[string]any{}, "inputResponses": map[string]any{"name": accept("A")}, "requestState": state}
	if text, _ := toolText(t, modernElicitCall(t, srvB.URL, "tools/call", retry, elicitCaps)); text != "accept A" {
		t.Fatalf("instance B refused shared-key state: %q", text)
	}
	if code, _ := wireError(t, modernElicitCall(t, srvC.URL, "tools/call", retry, elicitCaps)); code != ErrorCodeInvalidParams {
		t.Fatal("instance with its own key accepted the state")
	}
}

func TestElicitModern_Stdio(t *testing.T) {
	h := newStdioHarness(t, newElicitServer(t, nil))
	meta := `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{"elicitation":{}}}`
	h.send(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ask","arguments":{},`+meta+`}}`)
	var resp map[string]any
	json.Unmarshal([]byte(h.next(t, 2*time.Second)), &resp)
	_, state := inputRequired(t, resp)
	stateJSON, _ := json.Marshal(state)
	h.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ask","arguments":{},"inputResponses":{"name":{"action":"accept","content":{"name":"Sid"}}},"requestState":`+string(stateJSON)+`,`+meta+`}}`)
	json.Unmarshal([]byte(h.next(t, 2*time.Second)), &resp)
	if text, _ := toolText(t, resp); text != "accept Sid" {
		t.Fatalf("got %v", resp)
	}
	if resp["result"].(map[string]any)["resultType"] != "complete" {
		t.Fatalf("got %v", resp)
	}
}

// ---- API ----

func TestElicit_OutsideARequest(t *testing.T) {
	if _, err := Elicit(context.Background(), ElicitForm{Message: "?", Schema: testNameSchema}); !errors.Is(err, ErrElicitationUnsupported) {
		t.Fatalf("err = %v", err)
	}
	if ElicitationSupported(context.Background()) {
		t.Fatal("supported outside a request")
	}
}

func TestElicit_FormChecks(t *testing.T) {
	s := NewServer("x", "1")
	ctx, _, _ := s.beginElicitation(withModernClientContext(context.Background(), elicitCaps, nil), "tools/call", json.RawMessage(`{"name":"x"}`), true, "", nil)
	if !ElicitationSupported(ctx) {
		t.Fatal("not supported with the capability declared")
	}
	for _, schema := range []map[string]any{
		nil,
		{"type": "string"},
		{"type": "object"},
		{"type": "object", "properties": map[string]any{"nested": map[string]any{"type": "object"}}},
		{"type": "object", "properties": map[string]any{"list": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}},
	} {
		if _, err := Elicit(ctx, ElicitForm{Key: fmt.Sprint(len(schema)), Message: "?", Schema: schema}); err == nil || errors.Is(err, ErrElicitationPending) {
			t.Fatalf("schema %v accepted", schema)
		}
	}
	if _, err := Elicit(ctx, ElicitForm{Key: "k", Message: "?", Schema: testNameSchema}); !errors.Is(err, ErrElicitationPending) {
		t.Fatalf("err = %v", err)
	}
	if _, err := Elicit(ctx, ElicitForm{Key: "k", Message: "?", Schema: testNameSchema}); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("duplicate key: err = %v", err)
	}
}

// Concurrent requests on one connection each get their own answer.
func TestElicitLegacyStdio_ConcurrentRequests(t *testing.T) {
	h := newStdioHarness(t, newElicitServer(t, nil))
	h.send(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{"elicitation":{}},"clientInfo":{"name":"t","version":"1"}}}`)
	h.next(t, 2*time.Second)
	h.send(t, `{"jsonrpc":"2.0","id":"a","method":"tools/call","params":{"name":"ask","arguments":{}}}`)
	h.send(t, `{"jsonrpc":"2.0","id":"b","method":"tools/call","params":{"name":"ask","arguments":{}}}`)
	for i := 0; i < 2; i++ {
		var req map[string]any
		json.Unmarshal([]byte(h.next(t, 2*time.Second)), &req)
		id, _ := json.Marshal(req["id"])
		name := fmt.Sprintf("n%d", i)
		h.send(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"action":"accept","content":{"name":%q}}}`, id, name))
	}
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		var resp map[string]any
		json.Unmarshal([]byte(h.next(t, 2*time.Second)), &resp)
		text, _ := toolText(t, resp)
		got[text] = true
	}
	if !got["accept n0"] || !got["accept n1"] {
		t.Fatalf("answers crossed: %v", got)
	}
}
