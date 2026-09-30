package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =========================================================================
// SSE parser (sse.go) — WHATWG event-stream interpretation
// =========================================================================

func readAllSSE(t *testing.T, raw string) ([]sseMessage, *sseReader) {
	t.Helper()
	rd := newSSEReader(strings.NewReader(raw))
	var out []sseMessage
	for {
		m, err := rd.next()
		if err == io.EOF {
			return out, rd
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		out = append(out, m)
	}
}

func TestSSEReader(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []sseMessage
	}{
		{"lf", "data: a\n\ndata: b\n\n", []sseMessage{{"message", []byte("a")}, {"message", []byte("b")}}},
		{"crlf", "data: a\r\n\r\n", []sseMessage{{"message", []byte("a")}}},
		{"lone cr", "data: a\r\rdata: b\r\r", []sseMessage{{"message", []byte("a")}, {"message", []byte("b")}}},
		{"bom stripped", "\xEF\xBB\xBFdata: a\n\n", []sseMessage{{"message", []byte("a")}}},
		{"comments ignored", ": ping\n\n:\n\ndata: a\n: mid\n\n", []sseMessage{{"message", []byte("a")}}},
		{"multi-line data joined", "data: a\ndata: b\ndata:\n\n", []sseMessage{{"message", []byte("a\nb\n")}}},
		{"no space after colon", "data:a\n\n", []sseMessage{{"message", []byte("a")}}},
		{"only one space stripped", "data:  a\n\n", []sseMessage{{"message", []byte(" a")}}},
		{"field without colon", "data\n\n", []sseMessage{{"message", []byte("")}}},
		{"event type", "event: custom\ndata: a\n\ndata: b\n\n", []sseMessage{{"custom", []byte("a")}, {"message", []byte("b")}}},
		{"empty data buffer not dispatched", "event: x\n\nid: 1\n\n", nil},
		{"unknown fields ignored", "foo: bar\ndata: a\n\n", []sseMessage{{"message", []byte("a")}}},
		{"final event without blank line (leniency)", "data: a", []sseMessage{{"message", []byte("a")}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := readAllSSE(t, tt.raw)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d events %q, want %d", len(got), got, len(tt.want))
			}
			for i := range got {
				if got[i].Event != tt.want[i].Event || !bytes.Equal(got[i].Data, tt.want[i].Data) {
					t.Fatalf("event %d = {%q %q}, want {%q %q}", i, got[i].Event, got[i].Data, tt.want[i].Event, tt.want[i].Data)
				}
			}
		})
	}
}

func TestSSEReaderIDAndRetry(t *testing.T) {
	// A priming event (id + empty data) still sets the last event id.
	_, rd := readAllSSE(t, "id: 42\ndata:\n\n")
	if rd.LastEventID != "42" {
		t.Fatalf("LastEventID = %q, want 42", rd.LastEventID)
	}
	// id with NUL is ignored; retry must be all digits.
	_, rd = readAllSSE(t, "id: 1\n\nid: a\x00b\nretry: 250\n\nretry: 1x\n\n")
	if rd.LastEventID != "1" || rd.Retry != 250*time.Millisecond {
		t.Fatalf("LastEventID=%q Retry=%v, want 1 / 250ms", rd.LastEventID, rd.Retry)
	}
}

// =========================================================================
// Server (server_stream.go)
// =========================================================================

func TestAcceptsEventStream(t *testing.T) {
	for accept, want := range map[string]bool{
		"application/json, text/event-stream":       true,
		"text/event-stream":                         true,
		"TEXT/EVENT-STREAM":                         true,
		"application/json;q=0.9, text/event-stream": true,
		"text/event-stream;q=0.5":                   true,
		"text/event-stream;q=0":                     false,
		"application/json":                          false,
		"*/*":                                       false,
		"text/*":                                    false,
		"":                                          false,
	} {
		if got := acceptsEventStream(accept); got != want {
			t.Errorf("acceptsEventStream(%q) = %v, want %v", accept, got, want)
		}
	}
}

// streamTestServer is a server with a "slow" tool that blocks until release
// is closed (or its context ends), then returns "done".
type streamTestServer struct {
	*Server
	release   chan struct{}
	cancelled chan struct{}
	started   chan struct{}
}

func newStreamTestServer(t *testing.T, delay, keepAlive time.Duration) *streamTestServer {
	t.Helper()
	st := &streamTestServer{
		Server:    NewServer("stream-test", "1"),
		release:   make(chan struct{}),
		cancelled: make(chan struct{}, 1),
		started:   make(chan struct{}, 1),
	}
	st.SetResponseStreaming(delay, keepAlive)
	st.RegisterTool(NewTool("slow", "slow tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		st.started <- struct{}{}
		select {
		case <-st.release:
			return NewToolResponseText("done"), nil
		case <-ctx.Done():
			st.cancelled <- struct{}{}
			return nil, ctx.Err()
		}
	})
	st.RegisterTool(NewTool("fast", "fast tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("quick"), nil
	})
	st.RegisterTool(NewTool("boom", "panics after the delay"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		time.Sleep(100 * time.Millisecond)
		panic("kaboom")
	})
	return st
}

func legacyToolCall(t *testing.T, url, tool, accept string) *http.Response {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"` + tool + `","arguments":{}}}`
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", accept)
	req.Header.Set(headerProtocolVersion, MCPProtocolVersionLatest)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func modernToolCall(t *testing.T, ctx context.Context, url, tool string) *http.Response {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":"m1","method":"tools/call","params":{"name":"` + tool + `","arguments":{},` +
		`"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(headerProtocolVersion, MCPProtocolVersionModern)
	req.Header.Set(headerMcpMethod, "tools/call")
	req.Header.Set(headerMcpName, tool)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// A call that finishes within the delay is answered as plain JSON, exactly as
// before streaming existed.
func TestServerStream_FastCallStaysJSON(t *testing.T) {
	st := newStreamTestServer(t, time.Second, time.Second)
	ts := httptest.NewServer(http.HandlerFunc(st.HandleRequest))
	defer ts.Close()

	resp := legacyToolCall(t, ts.URL, "fast", "application/json, text/event-stream")
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var msg MCPResponse
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil {
		t.Fatal(err)
	}
	if msg.ID != float64(7) || msg.Error != nil {
		t.Fatalf("unexpected response %+v", msg)
	}
}

// A call that outlives the delay is streamed: SSE headers, keep-alive
// comments while it runs (observed before it finishes), then exactly one
// message event carrying the response, then the stream ends.
func TestServerStream_SlowCallStreamsWithKeepAlives(t *testing.T) {
	st := newStreamTestServer(t, 20*time.Millisecond, 20*time.Millisecond)
	ts := httptest.NewServer(http.HandlerFunc(st.HandleRequest))
	defer ts.Close()

	resp := legacyToolCall(t, ts.URL, "slow", "application/json, text/event-stream")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	for h, want := range map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-cache, no-transform",
		"X-Accel-Buffering": "no",
	} {
		if got := resp.Header.Get(h); got != want {
			t.Fatalf("%s = %q, want %q", h, got, want)
		}
	}

	br := bufio.NewReader(resp.Body)
	// Keep-alives arrive while the handler is still running.
	for i := 0; i < 2; i++ {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line != ": ping\n" {
			t.Fatalf("expected keep-alive comment, got %q", line)
		}
		if blank, _ := br.ReadString('\n'); blank != "\n" {
			t.Fatalf("keep-alive not terminated by blank line: %q", blank)
		}
	}
	close(st.release)

	events, _ := readAllSSE(t, readRest(t, br))
	if len(events) != 1 {
		t.Fatalf("got %d events, want exactly 1: %q", len(events), events)
	}
	var msg MCPResponse
	if err := json.Unmarshal(events[0].Data, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.JSONRPC != "2.0" || msg.ID != float64(7) || msg.Error != nil {
		t.Fatalf("unexpected response %+v", msg)
	}
	text := msg.Result.(map[string]any)["content"].([]any)[0].(map[string]any)["text"]
	if text != "done" {
		t.Fatalf("result text = %v", text)
	}
}

func readRest(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A client that does not list text/event-stream always gets JSON, however
// long the call takes; so does every client when streaming is disabled.
func TestServerStream_JSONWhenNotAcceptedOrDisabled(t *testing.T) {
	for _, tc := range []struct {
		name   string
		delay  time.Duration
		accept string
	}{
		{"accept json only", 10 * time.Millisecond, "application/json"},
		{"accept wildcard", 10 * time.Millisecond, "*/*"},
		{"streaming disabled", -1, "application/json, text/event-stream"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newStreamTestServer(t, tc.delay, 10*time.Millisecond)
			ts := httptest.NewServer(http.HandlerFunc(st.HandleRequest))
			defer ts.Close()
			go func() {
				<-st.started
				time.Sleep(60 * time.Millisecond)
				close(st.release)
			}()
			resp := legacyToolCall(t, ts.URL, "slow", tc.accept)
			defer resp.Body.Close()
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Fatalf("Content-Type = %q, want application/json", ct)
			}
		})
	}
}

// Modern era: the streamed response is the fully shaped Modern result.
func TestServerStream_ModernResultShaped(t *testing.T) {
	st := newStreamTestServer(t, 10*time.Millisecond, 10*time.Millisecond)
	ts := httptest.NewServer(http.HandlerFunc(st.HandleRequest))
	defer ts.Close()
	go func() {
		<-st.started
		time.Sleep(40 * time.Millisecond)
		close(st.release)
	}()

	resp := modernToolCall(t, context.Background(), ts.URL, "slow")
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
	events, _ := readAllSSE(t, readRest(t, resp.Body))
	var last map[string]any
	if err := json.Unmarshal(events[len(events)-1].Data, &last); err != nil {
		t.Fatal(err)
	}
	result := last["result"].(map[string]any)
	if last["id"] != "m1" || result["resultType"] != "complete" {
		t.Fatalf("unexpected Modern result %v", last)
	}
	if _, ok := result["_meta"].(map[string]any)[metaKeyServerInfo]; !ok {
		t.Fatalf("serverInfo missing from _meta: %v", result)
	}
}

// Closing the response stream cancels the request (2026-07-28 MUST) and the
// server stops without writing anything further.
func TestServerStream_DisconnectCancelsHandler(t *testing.T) {
	st := newStreamTestServer(t, 10*time.Millisecond, 10*time.Millisecond)
	var handlerReturned atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.HandleRequest(w, r)
		handlerReturned.Store(true)
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	resp := modernToolCall(t, ctx, ts.URL, "slow")
	br := bufio.NewReader(resp.Body)
	if line, _ := br.ReadString('\n'); line != ": ping\n" {
		t.Fatalf("expected a keep-alive before disconnecting, got %q", line)
	}
	cancel()
	resp.Body.Close()

	select {
	case <-st.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("handler context was not cancelled by the disconnect")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !handlerReturned.Load() {
		if time.Now().After(deadline) {
			t.Fatal("HandleRequest did not return after the disconnect")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A panicking handler on the streaming path must not crash the process; the
// client receives an internal error on the stream.
func TestServerStream_PanicBecomesInternalError(t *testing.T) {
	st := newStreamTestServer(t, 10*time.Millisecond, 10*time.Millisecond)
	ts := httptest.NewServer(http.HandlerFunc(st.HandleRequest))
	defer ts.Close()

	resp := legacyToolCall(t, ts.URL, "boom", "application/json, text/event-stream")
	defer resp.Body.Close()
	events, _ := readAllSSE(t, readRest(t, resp.Body))
	var msg MCPResponse
	if err := json.Unmarshal(events[len(events)-1].Data, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Error == nil || msg.Error.Code != ErrorCodeInternalError || msg.ID != float64(7) {
		t.Fatalf("expected internal error for id 7, got %+v", msg)
	}
}

// Methods outside streamableMethods are never streamed.
func TestServerStream_OnlyStreamableMethods(t *testing.T) {
	for _, m := range []string{"initialize", "ping", "tools/list", "resources/list", "prompts/list"} {
		if streamableMethods[m] {
			t.Errorf("%s must not be streamable", m)
		}
	}
	for _, m := range []string{"tools/call", "resources/read", "prompts/get"} {
		if !streamableMethods[m] {
			t.Errorf("%s should be streamable", m)
		}
	}
}

func TestWriteSSEMessageSplitsLineBreaks(t *testing.T) {
	rec := httptest.NewRecorder()
	writeSSEMessage(rec, []byte("{\"a\":1}\r\nx\ry\n"))
	if got := rec.Body.String(); got != "data: {\"a\":1}\ndata: x\ndata: y\n\n" {
		t.Fatalf("got %q", got)
	}
}

// =========================================================================
// Client (client_stream.go) against scripted servers
// =========================================================================

// scriptedServer answers the legacy handshake itself and hands every other
// POST (and GET) to handle.
func scriptedServer(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, rpc map[string]any)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpc map[string]any
		if r.Method == http.MethodPost {
			json.NewDecoder(r.Body).Decode(&rpc)
		}
		switch {
		case r.Header.Get(headerMcpMethod) != "":
			http.Error(w, "unknown", http.StatusBadRequest) // Legacy-only server
		case rpc["method"] == "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set(headerSessionID, "sess-1")
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc["id"], "result": map[string]any{
				"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "s", "version": "1"},
			}})
		case rpc["method"] == "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			handle(w, r, rpc)
		}
	}))
}

func sseHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
}

func toolResult(id any, text string) string {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
	}})
	return string(b)
}

// Notifications before the response are delivered (in order), a response for
// another id is skipped, comments/multi-line/CRLF are handled, and the call
// completes on the matching response without waiting for the stream to close.
func TestClientStream_NotificationsThenMatchingResponse(t *testing.T) {
	serverDone := make(chan struct{})
	ts := scriptedServer(t, func(w http.ResponseWriter, r *http.Request, rpc map[string]any) {
		sseHeaders(w)
		io.WriteString(w, ": ping\r\n\r\n")
		io.WriteString(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\r\ndata: \"params\":{\"progress\":1}}\r\n\r\n")
		io.WriteString(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\",\"params\":{\"level\":\"info\"}}\n\n")
		io.WriteString(w, "data: "+toolResult("some-other-id", "wrong")+"\n\n")
		io.WriteString(w, "data: "+toolResult(rpc["id"], "right")+"\n\n")
		w.(http.Flusher).Flush()
		<-serverDone // keep the stream open: the client must not wait for EOF
	})
	defer ts.Close()
	defer close(serverDone)

	c := NewClient(ts.URL, nil, "")
	var mu sync.Mutex
	var got []string
	c.setPropagationHook(func(method string, params any) {
		mu.Lock()
		got = append(got, method)
		mu.Unlock()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := c.CallTool(ctx, "anything", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if resp.Content[0].Text != "right" {
		t.Fatalf("matched the wrong response: %+v", resp)
	}

	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] != "notifications/progress" || got[1] != "notifications/message" {
		t.Fatalf("notifications = %v, want [progress message] in order", got)
	}
}

// A stream that ends without the response (and without an event id to resume
// from) is an error, not a silent empty result.
func TestClientStream_EndsWithoutResponse(t *testing.T) {
	ts := scriptedServer(t, func(w http.ResponseWriter, r *http.Request, rpc map[string]any) {
		sseHeaders(w)
		io.WriteString(w, ": ping\n\n")
	})
	defer ts.Close()

	_, err := NewClient(ts.URL, nil, "").CallTool(context.Background(), "x", nil)
	if err == nil || !strings.Contains(err.Error(), "ended without a response") {
		t.Fatalf("err = %v, want 'ended without a response'", err)
	}
}

// An error response with a null id (the server could not read the request
// id) completes the call with that error.
func TestClientStream_NullIDErrorCompletes(t *testing.T) {
	ts := scriptedServer(t, func(w http.ResponseWriter, r *http.Request, rpc map[string]any) {
		sseHeaders(w)
		io.WriteString(w, "data: {\"jsonrpc\":\"2.0\",\"id\":null,\"error\":{\"code\":-32700,\"message\":\"Parse error\"}}\n\n")
	})
	defer ts.Close()

	_, err := NewClient(ts.URL, nil, "").CallTool(context.Background(), "x", nil)
	if err == nil || !strings.Contains(err.Error(), "Parse error") {
		t.Fatalf("err = %v, want the server's Parse error", err)
	}
}

// Legacy: a server-to-client ping on the stream is answered with an empty
// result, anything else with Method not found, each POSTed back carrying the
// session id and protocol version; then the response completes the call.
func TestClientStream_LegacyRepliesToServerRequests(t *testing.T) {
	var mu sync.Mutex
	replies := map[string]map[string]any{}
	var replyHeaders []http.Header
	ts := scriptedServer(t, func(w http.ResponseWriter, r *http.Request, rpc map[string]any) {
		if rpc["method"] == nil { // a reply from the client
			mu.Lock()
			replies[rpc["id"].(string)] = rpc
			replyHeaders = append(replyHeaders, r.Header.Clone())
			mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
			return
		}
		sseHeaders(w)
		io.WriteString(w, "data: {\"jsonrpc\":\"2.0\",\"id\":\"srv-ping\",\"method\":\"ping\"}\n\n")
		io.WriteString(w, "data: {\"jsonrpc\":\"2.0\",\"id\":\"srv-sample\",\"method\":\"sampling/createMessage\",\"params\":{}}\n\n")
		w.(http.Flusher).Flush()
		time.Sleep(100 * time.Millisecond) // replies are sent synchronously by the reader
		io.WriteString(w, "data: "+toolResult(rpc["id"], "ok")+"\n\n")
	})
	defer ts.Close()

	if _, err := NewClient(ts.URL, nil, "").CallTool(context.Background(), "x", nil); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if ping := replies["srv-ping"]; ping == nil || ping["error"] != nil || ping["result"] == nil {
		t.Fatalf("ping reply = %v, want empty result", ping)
	}
	if s := replies["srv-sample"]; s == nil || s["error"].(map[string]any)["code"] != float64(ErrorCodeMethodNotFound) {
		t.Fatalf("sampling reply = %v, want Method not found", s)
	}
	for _, h := range replyHeaders {
		if h.Get(headerSessionID) != "sess-1" || h.Get(headerProtocolVersion) != "2025-06-18" {
			t.Fatalf("reply missing session/protocol headers: %v", h)
		}
	}
}

// Legacy resumability: the server primes the stream with an event id and a
// retry, closes the connection, and delivers the response when the client
// reconnects with GET + Last-Event-ID after the retry delay.
func TestClientStream_LegacyResumesWithLastEventID(t *testing.T) {
	var postAt time.Time
	var getHeaders http.Header
	var getAt time.Time
	var callID any
	ts := scriptedServer(t, func(w http.ResponseWriter, r *http.Request, rpc map[string]any) {
		switch r.Method {
		case http.MethodPost:
			postAt = time.Now()
			callID = rpc["id"]
			sseHeaders(w)
			io.WriteString(w, "id: evt-1\nretry: 80\ndata:\n\n") // priming event, then close
		case http.MethodGet:
			getAt = time.Now()
			getHeaders = r.Header.Clone()
			sseHeaders(w)
			io.WriteString(w, "id: evt-2\ndata: "+toolResult(callID, "resumed")+"\n\n")
		}
	})
	defer ts.Close()

	resp, err := NewClient(ts.URL, nil, "").CallTool(context.Background(), "x", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if resp.Content[0].Text != "resumed" {
		t.Fatalf("unexpected result %+v", resp)
	}
	if getHeaders.Get("Last-Event-ID") != "evt-1" || getHeaders.Get("Accept") != "text/event-stream" ||
		getHeaders.Get(headerSessionID) != "sess-1" || getHeaders.Get(headerProtocolVersion) != "2025-06-18" {
		t.Fatalf("resume request headers wrong: %v", getHeaders)
	}
	if wait := getAt.Sub(postAt); wait < 80*time.Millisecond {
		t.Fatalf("reconnected after %v, before the server's 80ms retry", wait)
	}
}

// Resumption is bounded: a server that keeps closing without progress fails
// the call instead of looping forever.
func TestClientStream_LegacyResumeGivesUp(t *testing.T) {
	var gets atomic.Int32
	ts := scriptedServer(t, func(w http.ResponseWriter, r *http.Request, rpc map[string]any) {
		if r.Method == http.MethodGet {
			gets.Add(1)
			sseHeaders(w) // nothing: no progress
			return
		}
		sseHeaders(w)
		io.WriteString(w, "id: 1\nretry: 1\ndata:\n\n")
	})
	defer ts.Close()

	_, err := NewClient(ts.URL, nil, "").CallTool(context.Background(), "x", nil)
	if err == nil || !strings.Contains(err.Error(), "gave up") {
		t.Fatalf("err = %v, want give-up error", err)
	}
	if n := gets.Load(); n != maxIdleStreamResumes {
		t.Fatalf("resume attempts = %d, want %d", n, maxIdleStreamResumes)
	}
}

// The Legacy client sends the negotiated MCP-Protocol-Version and session id
// on every request after initialize (required from 2025-06-18), and neither
// on initialize itself.
func TestClient_LegacySendsProtocolVersionAfterInitialize(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]http.Header{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpc map[string]any
		json.NewDecoder(r.Body).Decode(&rpc)
		if r.Header.Get(headerMcpMethod) != "" {
			http.Error(w, "unknown", http.StatusBadRequest)
			return
		}
		mu.Lock()
		seen[rpc["method"].(string)] = r.Header.Clone()
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if rpc["method"] == "initialize" {
			w.Header().Set(headerSessionID, "sess-9")
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc["id"], "result": map[string]any{"protocolVersion": "2025-06-18"}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc["id"], "result": map[string]any{"tools": []any{}}})
	}))
	defer ts.Close()

	if _, err := NewClient(ts.URL, nil, "").ListTools(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if h := seen["initialize"]; h.Get(headerProtocolVersion) != "" || h.Get(headerSessionID) != "" {
		t.Fatalf("initialize carried version/session headers: %v", h)
	}
	if h := seen["tools/list"]; h.Get(headerProtocolVersion) != "2025-06-18" || h.Get(headerSessionID) != "sess-9" {
		t.Fatalf("tools/list headers = %v, want version 2025-06-18 and session sess-9", h)
	}
}

// =========================================================================
// End to end: real Client <-> real Server, both eras
// =========================================================================

func TestStreamEndToEnd_BothEras(t *testing.T) {
	for _, era := range []string{"modern", "legacy"} {
		t.Run(era, func(t *testing.T) {
			st := newStreamTestServer(t, 10*time.Millisecond, 10*time.Millisecond)
			var sawSSE atomic.Bool
			handler := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				st.HandleRequest(&sseSpy{ResponseWriter: w, saw: &sawSSE}, r)
			}))
			var ts *httptest.Server
			if era == "legacy" {
				// A Legacy-only server: rejects server/discover like one
				// that has never heard of it (see legacyOnlyServer).
				ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, _ := io.ReadAll(r.Body)
					if bytes.Contains(body, []byte(`"server/discover"`)) {
						http.Error(w, "unknown method", http.StatusBadRequest)
						return
					}
					r.Body = io.NopCloser(bytes.NewReader(body))
					handler.ServeHTTP(w, r)
				}))
			} else {
				ts = httptest.NewServer(handler)
			}
			defer ts.Close()

			c := NewClient(ts.URL, nil, "")
			if err := c.Initialize(context.Background()); err != nil {
				t.Fatal(err)
			}
			wantEra := eraModern
			if era == "legacy" {
				wantEra = eraLegacy
			}
			if c.era != wantEra {
				t.Fatalf("era = %v, want %v", c.era, wantEra)
			}

			go func() {
				<-st.started
				time.Sleep(80 * time.Millisecond)
				close(st.release)
			}()
			resp, err := c.CallTool(context.Background(), "slow", nil)
			if err != nil {
				t.Fatalf("CallTool: %v", err)
			}
			if resp.Content[0].Text != "done" {
				t.Fatalf("result = %+v", resp)
			}
			if !sawSSE.Load() {
				t.Fatal("response was not streamed")
			}

			// Fast calls still work (plain JSON).
			if resp, err := c.CallTool(context.Background(), "fast", nil); err != nil || resp.Content[0].Text != "quick" {
				t.Fatalf("fast call: %v %+v", err, resp)
			}
		})
	}
}

// sseSpy records whether a response was sent as an event stream.
type sseSpy struct {
	http.ResponseWriter
	saw *atomic.Bool
}

func (s *sseSpy) WriteHeader(code int) {
	if s.Header().Get("Content-Type") == "text/event-stream" {
		s.saw.Store(true)
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *sseSpy) Flush() { s.ResponseWriter.(http.Flusher).Flush() }

// Cancelling the client's context mid-stream returns promptly and cancels the
// server-side handler (Modern: closing the stream is cancellation).
func TestStreamEndToEnd_ClientCancelCancelsServer(t *testing.T) {
	st := newStreamTestServer(t, 10*time.Millisecond, 10*time.Millisecond)
	ts := httptest.NewServer(http.HandlerFunc(st.HandleRequest))
	defer ts.Close()

	c := NewClient(ts.URL, nil, "")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-st.started
		time.Sleep(50 * time.Millisecond) // well into the stream
		cancel()
	}()
	start := time.Now()
	_, err := c.CallTool(ctx, "slow", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("cancel did not return promptly")
	}
	select {
	case <-st.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("server handler was not cancelled")
	}
}

// =========================================================================
// The actual goal: surviving an intermediary with an idle read timeout
// =========================================================================

// idleTimeoutProxy is a TCP proxy that drops the connection when the backend
// sends nothing for idle — how Cloudflare (~100s), nginx proxy_read_timeout
// and load balancers treat a silent origin.
func idleTimeoutProxy(t *testing.T, backend string, idle time.Duration) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer client.Close()
				server, err := net.Dial("tcp", backend)
				if err != nil {
					return
				}
				defer server.Close()
				go io.Copy(server, client)
				buf := make([]byte, 32*1024)
				for {
					server.SetReadDeadline(time.Now().Add(idle))
					n, err := server.Read(buf)
					if n > 0 {
						if _, werr := client.Write(buf[:n]); werr != nil {
							return
						}
					}
					if err != nil {
						return // idle timeout (or EOF): drop the connection
					}
				}
			}()
		}
	}()
	return "http://" + ln.Addr().String()
}

func TestStream_SurvivesIdleTimeoutProxy(t *testing.T) {
	for _, tc := range []struct {
		name      string
		delay     time.Duration
		wantError bool
	}{
		{"streaming keeps the connection alive", 20 * time.Millisecond, false},
		{"plain JSON is cut off (the 524 case)", -1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newStreamTestServer(t, tc.delay, 50*time.Millisecond)
			ts := httptest.NewServer(http.HandlerFunc(st.HandleRequest))
			defer ts.Close()
			proxyURL := idleTimeoutProxy(t, strings.TrimPrefix(ts.URL, "http://"), 200*time.Millisecond)

			go func() {
				<-st.started
				time.Sleep(600 * time.Millisecond) // 3x the proxy's idle timeout
				close(st.release)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			resp, err := NewClient(proxyURL, nil, "").CallTool(ctx, "slow", nil)
			if tc.wantError {
				if err == nil {
					t.Fatal("expected the proxy to cut off the silent response")
				}
				return
			}
			if err != nil {
				t.Fatalf("CallTool through idle-timeout proxy: %v", err)
			}
			if resp.Content[0].Text != "done" {
				t.Fatalf("result = %+v", resp)
			}
		})
	}
}

// After cancellation the server writes nothing further for the request — not
// even the handler's eventual response (2026-07-28: MUST NOT send any
// further messages). Observed in-process, where writes after a disconnect
// are visible (over a socket they are silently dropped).
func TestServerStream_NothingWrittenAfterCancel(t *testing.T) {
	st := newStreamTestServer(t, 10*time.Millisecond, time.Hour)
	body := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"slow","arguments":{}}}`
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		st.HandleRequest(rec, req)
		close(done)
	}()
	<-st.started
	time.Sleep(50 * time.Millisecond) // past the delay: the stream is open
	cancel()
	<-done

	if rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream never opened: %q", rec.Header().Get("Content-Type"))
	}
	if strings.Contains(rec.Body.String(), "data:") {
		t.Fatalf("server wrote a message after cancellation: %q", rec.Body.String())
	}
}

// =========================================================================
// Version negotiation across eras
// =========================================================================

// A Legacy client that is offered a version it does not speak refuses the
// session (2025-11-25: it SHOULD disconnect) instead of carrying on.
func TestClient_LegacyRejectsUnsupportedNegotiatedVersion(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpc map[string]any
		json.NewDecoder(r.Body).Decode(&rpc)
		if r.Header.Get(headerMcpMethod) != "" {
			http.Error(w, "unknown", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc["id"], "result": map[string]any{"protocolVersion": "2023-01-01"}})
	}))
	defer ts.Close()

	c := NewClient(ts.URL, nil, "")
	err := c.Initialize(context.Background())
	if err == nil || !strings.Contains(err.Error(), "2023-01-01") {
		t.Fatalf("err = %v, want rejection of 2023-01-01", err)
	}
	if c.initialized {
		t.Fatal("client marked initialized after an unsupported version")
	}
}

// After a successful Legacy initialize the client sends
// notifications/initialized (MUST), with the negotiated headers, and the
// server acknowledges it with 202.
func TestClient_LegacySendsInitializedNotification(t *testing.T) {
	s := NewServer("s", "1")
	var mu sync.Mutex
	var got []string
	var status int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(`"server/discover"`)) {
			http.Error(w, "unknown method", http.StatusBadRequest)
			return
		}
		var rpc map[string]any
		json.Unmarshal(body, &rpc)
		r.Body = io.NopCloser(bytes.NewReader(body))
		rec := httptest.NewRecorder()
		s.HandleRequest(rec, r)
		mu.Lock()
		if rpc["method"] == "notifications/initialized" {
			got = append(got, r.Header.Get(headerProtocolVersion))
			status = rec.Code
		}
		mu.Unlock()
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		w.Write(rec.Body.Bytes())
	}))
	defer ts.Close()

	if err := NewClient(ts.URL, nil, "").Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != MCPProtocolVersionLatest || status != http.StatusAccepted {
		t.Fatalf("initialized notification: sent=%v status=%d, want one with version %s acknowledged 202", got, status, MCPProtocolVersionLatest)
	}
}

// Negotiation always lands on the highest version both sides speak:
//   - a Modern-capable client and the dual-era server settle on 2026-07-28;
//   - against a Legacy-only server the client falls back to initialize and
//     gets 2025-11-25, the latest Legacy revision;
//   - an older Legacy server that only speaks 2025-06-18 counter-offers it
//     and the client accepts it;
//
// and streaming works at every one of those versions.
func TestNegotiation_HighestCommonVersionAndStreaming(t *testing.T) {
	for _, tc := range []struct {
		name        string
		wrap        func(h http.Handler) http.Handler
		wantEra     clientEra
		wantVersion string
	}{
		{"dual-era server", func(h http.Handler) http.Handler { return h }, eraModern, MCPProtocolVersionModern},
		{"legacy-only server", legacyOnly(""), eraLegacy, MCPProtocolVersionLatest},
		{"older legacy server (2025-06-18)", legacyOnly("2025-06-18"), eraLegacy, "2025-06-18"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newStreamTestServer(t, 10*time.Millisecond, 10*time.Millisecond)
			var sawSSE atomic.Bool
			ts := httptest.NewServer(tc.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				st.HandleRequest(&sseSpy{ResponseWriter: w, saw: &sawSSE}, r)
			})))
			defer ts.Close()

			c := NewClient(ts.URL, nil, "")
			if err := c.Initialize(context.Background()); err != nil {
				t.Fatal(err)
			}
			if c.era != tc.wantEra || c.protocolVersion != tc.wantVersion {
				t.Fatalf("negotiated era=%v version=%q, want era=%v version=%q", c.era, c.protocolVersion, tc.wantEra, tc.wantVersion)
			}

			go func() {
				<-st.started
				time.Sleep(60 * time.Millisecond)
				close(st.release)
			}()
			resp, err := c.CallTool(context.Background(), "slow", nil)
			if err != nil || resp.Content[0].Text != "done" {
				t.Fatalf("CallTool: %v %+v", err, resp)
			}
			if !sawSSE.Load() {
				t.Fatal("slow call was not streamed")
			}
		})
	}
}

// legacyOnly makes the wrapped server look like a pre-2026-07-28 server:
// server/discover is unknown (a plain 400), and when onlyVersion is set the
// initialize request is rewritten to ask for it — the server then answers
// with that version, as a server that supports nothing newer would.
func legacyOnly(onlyVersion string) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			if bytes.Contains(body, []byte(`"server/discover"`)) {
				http.Error(w, "unknown method", http.StatusBadRequest)
				return
			}
			if onlyVersion != "" {
				body = bytes.Replace(body, []byte(`"protocolVersion":"`+MCPProtocolVersionLatest+`"`), []byte(`"protocolVersion":"`+onlyVersion+`"`), 1)
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))
			h.ServeHTTP(w, r)
		})
	}
}
