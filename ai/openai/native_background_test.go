package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/paularlott/mcp"
)

// fakeNative is a native Responses API that records requests and answers
// every create with a message, as response "resp_native_<n>".
type fakeNative struct {
	mu       sync.Mutex
	n        int
	creates  []map[string]any
	deleted  []string
	compacts []map[string]any
	delay    time.Duration
}

func (f *fakeNative) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		switch {
		case r.Method == http.MethodGet:
			f.mu.Unlock()
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":{"message":"not found","type":"invalid_request_error"}}`))
			return
		case r.Method == http.MethodDelete:
			f.deleted = append(f.deleted, strings.TrimPrefix(r.URL.Path, "/responses/"))
			f.mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"id": "x", "object": "response", "deleted": true})
			return
		case r.URL.Path == "/responses/compact":
			f.compacts = append(f.compacts, body)
			f.mu.Unlock()
			json.NewEncoder(w).Encode(CompactedResponse{ID: "cmp_1", Object: "response.compaction", Output: []any{}})
			return
		}
		f.n++
		id := fmt.Sprintf("resp_native_%d", f.n)
		f.creates = append(f.creates, body)
		delay := f.delay
		f.mu.Unlock()
		time.Sleep(delay)

		msg := map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "ok"}}}
		if body["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			resp, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "status": "completed", "output": []any{msg}}})
			fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", resp)
			return
		}
		json.NewEncoder(w).Encode(ResponseObject{ID: id, Object: "response", Status: "completed", Output: []any{msg}})
	}
}

func (f *fakeNative) lastCreate() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.creates[len(f.creates)-1]
}

func waitCompleted(t *testing.T, c *Client, id string) *ResponseObject {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r, err := c.GetResponse(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if r.Status == "completed" {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("response %s still %s", id, r.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// xAI rejects "background", so the client runs background responses itself.
func TestGrok_BackgroundRunsLocally(t *testing.T) {
	f := &fakeNative{delay: 100 * time.Millisecond}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	native := true
	c, _ := New(Config{Provider: providerGrok, BaseURL: srv.URL, UseNativeResponses: &native})

	r, err := c.CreateResponse(context.Background(), CreateResponseRequest{Model: "grok", Background: true, Input: userInput("x")})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "in_progress" {
		t.Errorf("initial status = %q, want in_progress", r.Status)
	}
	done := waitCompleted(t, c, r.ID)
	if done.OutputText() != "ok" {
		t.Errorf("result = %+v", done)
	}
	if _, sent := f.lastCreate()["background"]; sent {
		t.Error("background sent to xAI")
	}

	// A running one can be cancelled
	running, err := c.CreateResponse(context.Background(), CreateResponseRequest{Model: "grok", Background: true, Input: userInput("y")})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.CancelResponse(context.Background(), running.ID)
	if err != nil || got.Status != "cancelled" {
		t.Errorf("cancel = %+v, %v", got, err)
	}
}

// A background response the client ran itself (for MCP tools) is known by a
// local ID; continuing, compacting and deleting by that ID reach its result
// at the provider.
func TestNative_LocalBackgroundIDMapsToProviderID(t *testing.T) {
	f := &fakeNative{}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	local := &MCPServerFuncs{ListToolsFunc: func() []mcp.MCPTool { return []mcp.MCPTool{{Name: "t", InputSchema: map[string]any{"type": "object"}}} }}
	native := true
	c, _ := New(Config{BaseURL: srv.URL, UseNativeResponses: &native, LocalServer: local})
	ctx := context.Background()

	r, err := c.CreateResponse(ctx, CreateResponseRequest{Model: "m", Background: true, Input: userInput("x")})
	if err != nil {
		t.Fatal(err)
	}
	done := waitCompleted(t, c, r.ID)
	providerID := done.ID
	if providerID == r.ID || !strings.HasPrefix(providerID, "resp_native_") {
		t.Fatalf("local %q, provider %q", r.ID, providerID)
	}

	if _, err := c.CreateResponse(ctx, CreateResponseRequest{Model: "m", PreviousResponseID: r.ID, Input: userInput("again")}); err != nil {
		t.Fatal(err)
	}
	if got := f.lastCreate()["previous_response_id"]; got != providerID {
		t.Errorf("sync continue sent previous_response_id %v, want %s", got, providerID)
	}

	s := c.StreamResponse(ctx, CreateResponseRequest{Model: "m", PreviousResponseID: r.ID, Input: userInput("again")})
	for s.Next() {
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	if got := f.lastCreate()["previous_response_id"]; got != providerID {
		t.Errorf("stream continue sent previous_response_id %v, want %s", got, providerID)
	}

	if _, err := c.CompactResponse(ctx, CompactResponseRequest{Model: "m", PreviousResponseID: r.ID}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	compactPrev := f.compacts[0]["previous_response_id"]
	deleted := fmt.Sprint(f.deleted)
	f.mu.Unlock()
	if got := compactPrev; got != providerID {
		t.Errorf("compact sent previous_response_id %v, want %s", got, providerID)
	}

	if err := c.DeleteResponse(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	deleted = fmt.Sprint(f.deleted)
	f.mu.Unlock()
	if deleted != "["+providerID+"]" {
		t.Errorf("deleted at provider = %v, want [%s]", f.deleted, providerID)
	}
	if _, err := c.GetResponse(ctx, r.ID); err == nil {
		t.Error("local record still present after delete")
	}

	// Continuing one still in progress fails clearly
	f.mu.Lock()
	f.delay = 300 * time.Millisecond
	f.mu.Unlock()
	running, _ := c.CreateResponse(ctx, CreateResponseRequest{Model: "m", Background: true, Input: userInput("slow")})
	if _, err := c.CreateResponse(ctx, CreateResponseRequest{Model: "m", PreviousResponseID: running.ID, Input: userInput("x")}); err == nil || !strings.Contains(err.Error(), "in progress") {
		t.Errorf("continue from running response error = %v", err)
	}
}

// A background response keeps running when the caller's context ends, as it
// does when an HTTP handler returns.
func TestNative_BackgroundOutlivesCallerContext(t *testing.T) {
	f := &fakeNative{delay: 100 * time.Millisecond}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	local := &MCPServerFuncs{ListToolsFunc: func() []mcp.MCPTool { return []mcp.MCPTool{{Name: "t", InputSchema: map[string]any{"type": "object"}}} }}
	native := true
	c, _ := New(Config{BaseURL: srv.URL, UseNativeResponses: &native, LocalServer: local})

	ctx, cancel := context.WithCancel(context.Background())
	r, err := c.CreateResponse(ctx, CreateResponseRequest{Model: "m", Background: true, Input: userInput("x")})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if done := waitCompleted(t, c, r.ID); done.OutputText() != "ok" {
		t.Errorf("result = %+v", done)
	}
}

// A negative RequestTimeout means no timeout: native background responses
// run, rather than expiring at once.
func TestNative_BackgroundWithNegativeRequestTimeout(t *testing.T) {
	f := &fakeNative{}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	local := &MCPServerFuncs{ListToolsFunc: func() []mcp.MCPTool { return []mcp.MCPTool{{Name: "t", InputSchema: map[string]any{"type": "object"}}} }}
	native := true
	c, _ := New(Config{BaseURL: srv.URL, UseNativeResponses: &native, LocalServer: local, RequestTimeout: -1})
	r, err := c.CreateResponse(context.Background(), CreateResponseRequest{Model: "m", Background: true, Input: userInput("x")})
	if err != nil {
		t.Fatal(err)
	}
	if done := waitCompleted(t, c, r.ID); done.OutputText() != "ok" {
		t.Errorf("result = %+v", done)
	}
}

// ctxCapturingCompleter records the context its chat call ran with.
type ctxCapturingCompleter struct {
	mockCompleter
	mu  sync.Mutex
	ctx context.Context
}

func (c *ctxCapturingCompleter) ChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	c.mu.Lock()
	c.ctx = ctx
	c.mu.Unlock()
	return c.mockCompleter.ChatCompletion(ctx, req)
}

// An emulated background response keeps the caller's context values (e.g.
// the tool handler) and outlives the caller's cancellation, as the native
// path does.
func TestEmulated_BackgroundKeepsContextValues(t *testing.T) {
	m := NewResponseManager()
	cc := &ctxCapturingCompleter{}
	handler := &NoOpToolHandler{}
	ctx, cancel := context.WithCancel(WithToolHandler(context.Background(), handler))
	r, err := CreateResponseEmulated(ctx, cc, m, CreateResponseRequest{Model: "m", Background: true, Input: userInput("x")})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if got, err := GetResponseEmulated(context.Background(), m, r.ID); err != nil || got.Status != "completed" {
		t.Fatalf("background response = %+v, %v", got, err)
	}
	cc.mu.Lock()
	defer cc.mu.Unlock()
	if ToolHandlerFromContext(cc.ctx) != handler {
		t.Error("tool handler from the caller's context missing in the background run")
	}
}
