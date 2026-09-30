package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fastStreamRetry(t *testing.T) {
	t.Helper()
	orig := minStreamRetry
	minStreamRetry = time.Millisecond
	t.Cleanup(func() { minStreamRetry = orig })
}

// A server that answers every resume with only a priming event (id + retry
// + empty data) must not count as progress: the client gives up after
// maxIdleStreamResumes instead of reconnecting forever.
func TestClientStream_PrimingOnlyResumesGiveUp(t *testing.T) {
	fastStreamRetry(t)
	var gets atomic.Int32
	ts := scriptedServer(t, func(w http.ResponseWriter, r *http.Request, rpc map[string]any) {
		n := gets.Load()
		if r.Method == http.MethodGet {
			n = gets.Add(1)
		}
		sseHeaders(w)
		io.WriteString(w, "id: "+string(rune('a'+n%26))+"\nretry: 1\ndata:\n\n")
	})
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := NewClient(ts.URL, nil, "").CallTool(ctx, "x", nil)
	if err == nil || !strings.Contains(err.Error(), "without progress") {
		t.Fatalf("err = %v, want give-up without progress", err)
	}
	if n := gets.Load(); n != maxIdleStreamResumes {
		t.Fatalf("resumes = %d, want %d", n, maxIdleStreamResumes)
	}
}

// A server that sends a real notification on every resume but never the
// response is still bounded by maxStreamResumes in total.
func TestClientStream_TricklingResumesAreBounded(t *testing.T) {
	fastStreamRetry(t)
	var gets atomic.Int32
	ts := scriptedServer(t, func(w http.ResponseWriter, r *http.Request, rpc map[string]any) {
		if r.Method == http.MethodGet {
			gets.Add(1)
		}
		sseHeaders(w)
		io.WriteString(w, "id: 1\nretry: 1\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{}}\n\n")
	})
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := NewClient(ts.URL, nil, "").CallTool(ctx, "x", nil)
	if err == nil || !strings.Contains(err.Error(), "gave up after") || strings.Contains(err.Error(), "without progress") {
		t.Fatalf("err = %v, want the total resume bound", err)
	}
	if n := gets.Load(); n != maxStreamResumes {
		t.Fatalf("resumes = %d, want %d", n, maxStreamResumes)
	}
}

// The retry delay is floored: "retry: 1" does not reconnect every millisecond.
func TestClientStream_RetryFloor(t *testing.T) {
	var mu sync.Mutex
	var times []time.Time
	var callID any
	ts := scriptedServer(t, func(w http.ResponseWriter, r *http.Request, rpc map[string]any) {
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		sseHeaders(w)
		if r.Method == http.MethodPost {
			callID = rpc["id"]
			io.WriteString(w, "id: 1\nretry: 1\ndata:\n\n")
			return
		}
		io.WriteString(w, "data: "+toolResult(callID, "ok")+"\n\n")
	})
	defer ts.Close()

	if _, err := NewClient(ts.URL, nil, "").CallTool(context.Background(), "x", nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if gap := times[len(times)-1].Sub(times[len(times)-2]); gap < minStreamRetry {
		t.Fatalf("reconnected after %v, below the %v floor", gap, minStreamRetry)
	}
}

// countingPool hands out a client whose response bodies are counted, to
// check superseded resume bodies are closed as the loop goes, not deferred.
type countingPool struct {
	open, maxOpen atomic.Int32
}

func (p *countingPool) GetHTTPClient() *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		resp, err := http.DefaultTransport.RoundTrip(r)
		if err != nil {
			return nil, err
		}
		if n := p.open.Add(1); n > p.maxOpen.Load() {
			p.maxOpen.Store(n)
		}
		resp.Body = &countedBody{ReadCloser: resp.Body, pool: p}
		return resp, nil
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type countedBody struct {
	io.ReadCloser
	pool *countingPool
	once sync.Once
}

func (b *countedBody) Close() error {
	b.once.Do(func() { b.pool.open.Add(-1) })
	return b.ReadCloser.Close()
}

func TestClientStream_ResumedBodiesClosedPromptly(t *testing.T) {
	fastStreamRetry(t)
	ts := scriptedServer(t, func(w http.ResponseWriter, r *http.Request, rpc map[string]any) {
		sseHeaders(w)
		io.WriteString(w, "id: 1\nretry: 1\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{}}\n\n")
	})
	defer ts.Close()

	p := &countingPool{}
	c := NewClientWithPool(ts.URL, nil, "", p)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c.CallTool(ctx, "x", nil) // gives up after maxStreamResumes
	if n := p.open.Load(); n != 0 {
		t.Fatalf("%d response bodies left open after the call", n)
	}
	// The original POST body (closed by its caller) plus the current resume.
	if m := p.maxOpen.Load(); m > 2 {
		t.Fatalf("up to %d bodies open at once; superseded resumes are not being closed", m)
	}
}

// When the annotations could not be loaded, a HeaderMismatch reports the
// real tools/list failure instead of a bare "Header mismatch".
func TestClientReportsToolListFailureBehindHeaderMismatch(t *testing.T) {
	ts := fakeModernServer(t, func(w http.ResponseWriter, r *http.Request, rpc map[string]any) {
		switch rpc["method"] {
		case "tools/list":
			http.Error(w, "boom", http.StatusInternalServerError)
		case "tools/call":
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc["id"], "error": map[string]any{"code": ErrorCodeHeaderMismatch, "message": "Header mismatch: Mcp-Param-Region header is required"}})
		}
	})
	_, err := NewClient(ts.URL, nil, "").CallTool(context.Background(), "route", map[string]any{"region": "eu"})
	if err == nil || !strings.Contains(err.Error(), "could not load tool definitions") || !strings.Contains(err.Error(), "500") {
		t.Fatalf("err = %v, want the tools/list failure as the cause", err)
	}
	// The server's JSON-RPC error is still reachable like on any other call.
	var toolErr *ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != ErrorCodeHeaderMismatch {
		t.Fatalf("errors.As(*ToolError) = %v %+v, want the server's HeaderMismatch", errors.As(err, &toolErr), toolErr)
	}
	// ...but not by direct assertion, so a federating Server does not forward
	// a HeaderMismatch about its own upstream hop.
	if _, direct := err.(*ToolError); direct {
		t.Fatal("error is directly a *ToolError; federation would forward the upstream HeaderMismatch")
	}
}

// Through a federating Server, the upstream hop's HeaderMismatch reaches the
// downstream caller as the federating server's internal error (with the
// cause in the message), not as a HeaderMismatch the caller would act on.
func TestFederatedHeaderMismatchNotForwardedAsHeaderMismatch(t *testing.T) {
	upstream := fakeModernServer(t, func(w http.ResponseWriter, r *http.Request, rpc map[string]any) {
		switch rpc["method"] {
		case "tools/list":
			if r.Header.Get("X-Listed") == "" {
				// First listing (federation registration) succeeds; later
				// ones (annotation lookup at call time) fail.
				json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc["id"], "result": map[string]any{"resultType": "complete", "tools": []any{
					map[string]any{"name": "route", "inputSchema": map[string]any{"type": "object"}},
				}}})
				return
			}
			http.Error(w, "boom", http.StatusInternalServerError)
		case "tools/call":
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc["id"], "error": map[string]any{"code": ErrorCodeHeaderMismatch, "message": "Header mismatch upstream"}})
		}
	})

	client := NewClient(upstream.URL, nil, "up")
	fed := NewServer("fed", "1")
	if err := fed.RegisterRemoteServer(client); err != nil {
		t.Fatal(err)
	}
	// Make the call-time annotation lookup fail.
	client.mu.Lock()
	client.cachedTools = nil
	client.requestHeaders = map[string]string{"X-Listed": "1"}
	client.mu.Unlock()

	_, err := fed.CallTool(context.Background(), "up__route", map[string]any{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if wire := fed.toolsCallWireError(context.Background(), "up__route", err); wire.Code == ErrorCodeHeaderMismatch {
		t.Fatalf("federating server forwarded the upstream HeaderMismatch: %+v", wire)
	} else if !strings.Contains(wire.Message, "could not load tool definitions") {
		t.Fatalf("wire error lost the cause: %+v", wire)
	}
}

// Excluding a tool with an invalid annotation is logged (2026-07-28 SHOULD).
func TestClientWarnsWhenExcludingTool(t *testing.T) {
	var buf bytes.Buffer
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(orig)

	ts := fakeModernServer(t, func(w http.ResponseWriter, r *http.Request, rpc map[string]any) {
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc["id"], "result": map[string]any{"resultType": "complete", "tools": []any{
			map[string]any{"name": "bad_number", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "number", "x-mcp-header": "N"}}}},
		}}})
	})
	if _, err := NewClient(ts.URL, nil, "").ListTools(context.Background()); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, "tool=bad_number") || !strings.Contains(out, "number") {
		t.Fatalf("warning not logged: %q", out)
	}
}
