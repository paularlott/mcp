package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Progress notifications (request_stream.go): the token rules, the spec's
// MUSTs (increasing values, nothing after completion), rate limiting, and
// delivery on each transport's request stream.

func TestProgressTokenFrom(t *testing.T) {
	for _, tc := range []struct {
		params string
		want   any
	}{
		{``, nil},
		{`{}`, nil},
		{`{"_meta":{}}`, nil},
		{`{"_meta":{"progressToken":"abc"}}`, "abc"},
		{`{"_meta":{"progressToken":7}}`, json.Number("7")},
		{`{"_meta":{"progressToken":9007199254740993}}`, json.Number("9007199254740993")},
		{`{"_meta":{"progressToken":1.5}}`, nil},
		{`{"_meta":{"progressToken":true}}`, nil},
		{`{"_meta":{"progressToken":null}}`, nil},
		{`{"_meta":{"progressToken":{"a":1}}}`, nil},
	} {
		if got := progressTokenFrom(json.RawMessage(tc.params)); got != tc.want {
			t.Errorf("progressTokenFrom(%s) = %#v, want %#v", tc.params, got, tc.want)
		}
	}
}

// recordingStream is a request stream that keeps what it sends.
func recordingStream(s *Server, params string) (*requestStream, *[]map[string]any) {
	var mu sync.Mutex
	var sent []map[string]any
	rs := s.newRequestStream(json.RawMessage(params), func(line []byte) bool {
		var msg map[string]any
		_ = json.Unmarshal(line, &msg)
		mu.Lock()
		sent = append(sent, msg)
		mu.Unlock()
		return true
	})
	return rs, &sent
}

func TestReportProgress_NoTokenIsNoOp(t *testing.T) {
	if err := ReportProgress(context.Background(), 1, 2, ""); err != nil {
		t.Fatalf("no request stream: %v", err)
	}
	rs, sent := recordingStream(NewServer("p", "1"), `{"name":"x"}`)
	ctx := withRequestStream(context.Background(), rs)
	if err := ReportProgress(ctx, 1, 2, "m"); err != nil {
		t.Fatalf("no token: %v", err)
	}
	if len(*sent) != 0 {
		t.Fatalf("sent %v without a token", *sent)
	}
	if _, ok := ProgressToken(ctx); ok {
		t.Fatal("ProgressToken reported a token")
	}
}

func TestReportProgress_Rules(t *testing.T) {
	s := NewServer("p", "1")
	s.SetProgressInterval(0)
	rs, sent := recordingStream(s, `{"_meta":{"progressToken":"tok"}}`)
	ctx := withRequestStream(context.Background(), rs)

	if err := ReportProgress(ctx, 0, 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := ReportProgress(ctx, 0.5, 2, "half"); err != nil {
		t.Fatal(err)
	}
	for _, v := range []float64{0.5, 0.25} {
		if err := ReportProgress(ctx, v, 2, ""); !errors.Is(err, ErrProgressNotIncreasing) {
			t.Fatalf("ReportProgress(%v) = %v, want ErrProgressNotIncreasing", v, err)
		}
	}
	rs.finish()
	if err := ReportProgress(ctx, 3, 2, ""); !errors.Is(err, ErrRequestCompleted) {
		t.Fatalf("after completion: %v, want ErrRequestCompleted", err)
	}

	if len(*sent) != 2 {
		t.Fatalf("sent %d notifications, want 2: %v", len(*sent), *sent)
	}
	first, second := (*sent)[0], (*sent)[1]
	if first["method"] != "notifications/progress" || first["jsonrpc"] != "2.0" {
		t.Fatalf("bad envelope %v", first)
	}
	p := first["params"].(map[string]any)
	if p["progressToken"] != "tok" || p["progress"] != float64(0) {
		t.Fatalf("first params %v", p)
	}
	if _, has := p["total"]; has {
		t.Fatalf("unknown total sent: %v", p)
	}
	if _, has := p["message"]; has {
		t.Fatalf("empty message sent: %v", p)
	}
	p = second["params"].(map[string]any)
	if p["progress"] != 0.5 || p["total"] != float64(2) || p["message"] != "half" {
		t.Fatalf("second params %v", p)
	}
}

func TestReportProgress_CancelledContext(t *testing.T) {
	rs, sent := recordingStream(NewServer("p", "1"), `{"_meta":{"progressToken":1}}`)
	ctx, cancel := context.WithCancel(withRequestStream(context.Background(), rs))
	cancel()
	if err := ReportProgress(ctx, 1, 0, ""); !errors.Is(err, ErrRequestCompleted) {
		t.Fatalf("cancelled: %v", err)
	}
	if len(*sent) != 0 {
		t.Fatalf("sent after cancellation: %v", *sent)
	}
}

func TestReportProgress_RateLimit(t *testing.T) {
	s := NewServer("p", "1")
	s.SetProgressInterval(time.Hour)
	rs, sent := recordingStream(s, `{"_meta":{"progressToken":"t"}}`)
	ctx := withRequestStream(context.Background(), rs)
	for _, v := range []float64{1, 2, 3, 10} {
		if err := ReportProgress(ctx, v, 10, ""); err != nil {
			t.Fatal(err)
		}
	}
	// The first goes out, 2 and 3 are dropped, and reaching the total is
	// always delivered.
	var got []float64
	for _, m := range *sent {
		got = append(got, m["params"].(map[string]any)["progress"].(float64))
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 10 {
		t.Fatalf("sent progress %v, want [1 10]", got)
	}
	// A dropped value still counts for the increasing rule.
	if err := ReportProgress(ctx, 5, 0, ""); !errors.Is(err, ErrProgressNotIncreasing) {
		t.Fatalf("got %v", err)
	}
}

// progressServer has tools that report progress, and one whose goroutine
// reports after the call has returned.
type progressServer struct {
	*Server
	late chan error
}

func newProgressServer(t *testing.T, delay time.Duration) *progressServer {
	t.Helper()
	ps := &progressServer{Server: NewServer("progress", "1"), late: make(chan error, 1)}
	ps.SetResponseStreaming(delay, time.Second)
	ps.SetProgressInterval(0)
	steps := func(ctx context.Context) error {
		for _, v := range []float64{0, 50, 100} {
			if err := ReportProgress(ctx, v, 100, "step"); err != nil {
				return err
			}
		}
		return nil
	}
	ps.RegisterTool(NewTool("progress", "reports progress at once"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		if err := steps(ctx); err != nil {
			return nil, err
		}
		return NewToolResponseText("done"), nil
	})
	ps.RegisterTool(NewTool("hidden_progress", "discoverable").Discoverable("hidden"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		if err := steps(ctx); err != nil {
			return nil, err
		}
		return NewToolResponseText("done"), nil
	})
	ps.RegisterTool(NewTool("late", "reports after returning"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		_ = ReportProgress(ctx, 1, 0, "")
		go func() {
			time.Sleep(50 * time.Millisecond)
			ps.late <- ReportProgress(ctx, 2, 0, "")
		}()
		return NewToolResponseText("done"), nil
	})
	ps.RegisterPrompt(NewPrompt("progress_prompt", "reports progress"), func(ctx context.Context, req *PromptRequest) (*PromptResponse, error) {
		if err := steps(ctx); err != nil {
			return nil, err
		}
		return NewPromptResponseText("hi"), nil
	})
	ps.RegisterResource(NewResource("test://progress", "progress", "reports progress", "text/plain"), func(ctx context.Context, req *ResourceRequest) (*ResourceResponse, error) {
		if err := steps(ctx); err != nil {
			return nil, err
		}
		return NewResourceResponseText("test://progress", "body", "text/plain"), nil
	})
	return ps
}

// postMCP sends one JSON-RPC body with the given headers and returns the
// messages of the response, whether it came as JSON or as an SSE stream.
func postMCP(t *testing.T, url, body string, headers map[string]string) (contentType string, msgs []map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	contentType = resp.Header.Get("Content-Type")
	if contentType == "text/event-stream" {
		events, _ := readAllSSE(t, string(raw))
		for _, ev := range events {
			var m map[string]any
			if err := json.Unmarshal(ev.Data, &m); err != nil {
				t.Fatalf("bad event %q", ev.Data)
			}
			msgs = append(msgs, m)
		}
		return contentType, msgs
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("bad body %q", raw)
	}
	return contentType, []map[string]any{m}
}

// checkProgressThenResponse asserts msgs is progress 0, 50, 100 for token,
// then the response.
func checkProgressThenResponse(t *testing.T, msgs []map[string]any, token any) {
	t.Helper()
	if len(msgs) != 4 {
		t.Fatalf("got %d messages, want 3 progress + response: %v", len(msgs), msgs)
	}
	for i, want := range []float64{0, 50, 100} {
		m := msgs[i]
		if m["method"] != "notifications/progress" {
			t.Fatalf("message %d is not progress: %v", i, m)
		}
		p := m["params"].(map[string]any)
		if p["progressToken"] != token || p["progress"] != want || p["total"] != float64(100) {
			t.Fatalf("message %d params %v", i, p)
		}
	}
	last := msgs[3]
	if _, isResult := last["result"]; !isResult || last["method"] != nil {
		t.Fatalf("last message is not the response: %v", last)
	}
}

const legacyProgressCall = `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"progress","arguments":{},"_meta":{"progressToken":"p1"}}}`

var legacyHeaders = map[string]string{headerProtocolVersion: "2025-11-25"}

// A handler far faster than the streaming delay still gets its progress out:
// the first notification switches the response to SSE, and the response
// follows the notifications on the same stream.
func TestProgress_HTTPLegacyFastHandler(t *testing.T) {
	ps := newProgressServer(t, time.Hour)
	ts := httptest.NewServer(http.HandlerFunc(ps.HandleRequest))
	defer ts.Close()
	ct, msgs := postMCP(t, ts.URL, legacyProgressCall, legacyHeaders)
	if ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	checkProgressThenResponse(t, msgs, "p1")
}

// Without a progressToken nothing is sent, and a fast call stays plain JSON.
func TestProgress_HTTPNoTokenStaysJSON(t *testing.T) {
	ps := newProgressServer(t, time.Hour)
	ts := httptest.NewServer(http.HandlerFunc(ps.HandleRequest))
	defer ts.Close()
	body := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"progress","arguments":{}}}`
	ct, msgs := postMCP(t, ts.URL, body, legacyHeaders)
	if !strings.HasPrefix(ct, "application/json") || len(msgs) != 1 || msgs[0]["result"] == nil {
		t.Fatalf("Content-Type %q, messages %v", ct, msgs)
	}
}

// A client that does not accept SSE cannot be sent notifications on its
// response: they are dropped, and the handler is not failed for it.
func TestProgress_HTTPNoSSEDrops(t *testing.T) {
	ps := newProgressServer(t, time.Hour)
	ts := httptest.NewServer(http.HandlerFunc(ps.HandleRequest))
	defer ts.Close()
	h := map[string]string{headerProtocolVersion: "2025-11-25", "Accept": "application/json"}
	ct, msgs := postMCP(t, ts.URL, legacyProgressCall, h)
	if !strings.HasPrefix(ct, "application/json") || len(msgs) != 1 {
		t.Fatalf("Content-Type %q, messages %v", ct, msgs)
	}
	text := msgs[0]["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"]
	if text != "done" {
		t.Fatalf("result %v", msgs[0])
	}
}

// An integer token is echoed exactly, even beyond float64 precision.
func TestProgress_HTTPIntegerToken(t *testing.T) {
	ps := newProgressServer(t, time.Hour)
	ts := httptest.NewServer(http.HandlerFunc(ps.HandleRequest))
	defer ts.Close()
	body := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"progress","arguments":{},"_meta":{"progressToken":9007199254740993}}}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(headerProtocolVersion, "2025-11-25")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(raw), `"progressToken":9007199254740993`) {
		t.Fatalf("token not echoed exactly: %s", raw)
	}
}

// Modern (2026-07-28): progress goes on the request's own response stream,
// ahead of the shaped result.
func TestProgress_HTTPModern(t *testing.T) {
	ps := newProgressServer(t, time.Hour)
	ts := httptest.NewServer(http.HandlerFunc(ps.HandleRequest))
	defer ts.Close()
	body := `{"jsonrpc":"2.0","id":"m1","method":"tools/call","params":{"name":"progress","arguments":{},` +
		`"_meta":{"progressToken":"mp","io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`
	ct, msgs := postMCP(t, ts.URL, body, map[string]string{
		headerProtocolVersion: MCPProtocolVersionModern,
		headerMcpMethod:       "tools/call",
		headerMcpName:         "progress",
	})
	if ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
	checkProgressThenResponse(t, msgs, "mp")
	if msgs[3]["result"].(map[string]any)["resultType"] != "complete" {
		t.Fatalf("result not Modern-shaped: %v", msgs[3])
	}
}

// Prompt and resource handlers report progress the same way.
func TestProgress_HTTPPromptAndResource(t *testing.T) {
	ps := newProgressServer(t, time.Hour)
	ts := httptest.NewServer(http.HandlerFunc(ps.HandleRequest))
	defer ts.Close()
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":4,"method":"prompts/get","params":{"name":"progress_prompt","_meta":{"progressToken":"pp"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"resources/read","params":{"uri":"test://progress","_meta":{"progressToken":"pp"}}}`,
	} {
		_, msgs := postMCP(t, ts.URL, body, legacyHeaders)
		checkProgressThenResponse(t, msgs, "pp")
	}
}

// A tool reached through execute_tool runs with the caller's context, so its
// progress reaches the execute_tool request's stream.
func TestProgress_ExecuteTool(t *testing.T) {
	ps := newProgressServer(t, time.Hour)
	ts := httptest.NewServer(http.HandlerFunc(ps.HandleRequest))
	defer ts.Close()
	body := `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"execute_tool","arguments":{"name":"hidden_progress","parameters":{}},"_meta":{"progressToken":"ex"}}}`
	_, msgs := postMCP(t, ts.URL, body, legacyHeaders)
	checkProgressThenResponse(t, msgs, "ex")
}

// Notifications stop at completion: a goroutine the handler left behind
// gets ErrRequestCompleted, and nothing follows the response.
func TestProgress_NothingAfterCompletion(t *testing.T) {
	ps := newProgressServer(t, time.Hour)
	ts := httptest.NewServer(http.HandlerFunc(ps.HandleRequest))
	defer ts.Close()
	body := `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"late","arguments":{},"_meta":{"progressToken":"l"}}}`
	_, msgs := postMCP(t, ts.URL, body, legacyHeaders)
	if len(msgs) != 2 || msgs[0]["method"] != "notifications/progress" || msgs[1]["result"] == nil {
		t.Fatalf("messages %v", msgs)
	}
	select {
	case err := <-ps.late:
		if !errors.Is(err, ErrRequestCompleted) {
			t.Fatalf("late report: %v, want ErrRequestCompleted", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("late report never ran")
	}
}

// stdio: progress lines are written to stdout ahead of the response, for
// both eras; nothing without a token, nothing after completion.
func TestProgress_Stdio(t *testing.T) {
	ps := newProgressServer(t, time.Hour)
	h := newStdioHarness(t, ps.Server)
	h.send(t, stdioInit2025)
	h.next(t, 2*time.Second)

	read := func(n int) []map[string]any {
		var out []map[string]any
		for i := 0; i < n; i++ {
			var m map[string]any
			if err := json.Unmarshal([]byte(h.next(t, 2*time.Second)), &m); err != nil {
				t.Fatal(err)
			}
			out = append(out, m)
		}
		return out
	}

	h.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"progress","arguments":{},"_meta":{"progressToken":"s1"}}}`)
	checkProgressThenResponse(t, read(4), "s1")

	h.send(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"progress","arguments":{}}}`)
	if m := read(1)[0]; m["result"] == nil {
		t.Fatalf("expected only the response, got %v", m)
	}
	h.none(t, 50*time.Millisecond)

	h.send(t, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"progress","arguments":{},"_meta":{"progressToken":12345678901234567,`+
		`"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`)
	first := h.next(t, 2*time.Second)
	if !strings.Contains(first, `"progressToken":12345678901234567`) {
		t.Fatalf("Modern stdio progress: %s", first)
	}
	read(3)

	h.send(t, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"late","arguments":{},"_meta":{"progressToken":"l"}}}`)
	msgs := read(2)
	if msgs[0]["method"] != "notifications/progress" || msgs[1]["result"] == nil {
		t.Fatalf("messages %v", msgs)
	}
	if err := <-ps.late; !errors.Is(err, ErrRequestCompleted) {
		t.Fatalf("late report: %v", err)
	}
	h.none(t, 50*time.Millisecond)
}

// A cancelled stdio request gets no further progress.
func TestProgress_StdioCancelled(t *testing.T) {
	s := NewServer("p", "1")
	s.SetProgressInterval(0)
	started, after := make(chan struct{}), make(chan error, 1)
	s.RegisterTool(NewTool("wait", "waits"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		_ = ReportProgress(ctx, 1, 0, "")
		close(started)
		<-ctx.Done()
		after <- ReportProgress(ctx, 2, 0, "")
		return nil, ctx.Err()
	})
	h := newStdioHarness(t, s)
	h.send(t, stdioInit2025)
	h.next(t, 2*time.Second)
	h.send(t, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"wait","arguments":{},"_meta":{"progressToken":"c"}}}`)
	if l := h.next(t, 2*time.Second); !strings.Contains(l, "notifications/progress") {
		t.Fatalf("first line %s", l)
	}
	<-started
	h.send(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":9}}`)
	if err := <-after; !errors.Is(err, ErrRequestCompleted) {
		t.Fatalf("after cancel: %v", err)
	}
	h.none(t, 100*time.Millisecond)
}

// A federated remote tool's progress is relayed to the caller: the
// federating server asks the remote for progress under its own token and
// reports each notification on the caller's request.
func TestProgress_RemoteToolRelayed(t *testing.T) {
	for _, era := range []string{"legacy", "modern"} {
		t.Run(era, func(t *testing.T) {
			upstream := newProgressServer(t, time.Hour)
			up := httptest.NewServer(http.HandlerFunc(upstream.HandleRequest))
			defer up.Close()

			host := NewServer("host", "1")
			host.SetProgressInterval(0)
			client := NewClient(up.URL, nil, "up")
			if err := host.RegisterRemoteServer(client); err != nil {
				t.Fatal(err)
			}
			ts := httptest.NewServer(http.HandlerFunc(host.HandleRequest))
			defer ts.Close()

			var body string
			headers := legacyHeaders
			if era == "legacy" {
				body = `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"up__progress","arguments":{},"_meta":{"progressToken":"down"}}}`
			} else {
				body = `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"up__progress","arguments":{},` +
					`"_meta":{"progressToken":"down","io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`
				headers = map[string]string{
					headerProtocolVersion: MCPProtocolVersionModern,
					headerMcpMethod:       "tools/call",
					headerMcpName:         "up__progress",
				}
			}
			_, msgs := postMCP(t, ts.URL, body, headers)
			checkProgressThenResponse(t, msgs, "down")
		})
	}
}
