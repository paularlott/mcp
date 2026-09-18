package ollama

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/paularlott/mcp"
	"github.com/paularlott/mcp/ai/openai"
)

// ndjson marshals a chatResponse to a single NDJSON line.
func ndjson(t *testing.T, r chatResponse) string {
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal chatResponse: %v", err)
	}
	return string(b) + "\n"
}

func TestStreamChatCompletionSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, ndjson(t, chatResponse{Model: "llama3", Message: message{Role: "assistant", Content: "hel"}}))
		flusher.Flush()
		io.WriteString(w, ndjson(t, chatResponse{Model: "llama3", Message: message{Role: "assistant", Content: "lo"}}))
		flusher.Flush()
		io.WriteString(w, ndjson(t, chatResponse{
			Model: "llama3", Done: true, DoneReason: "stop", PromptEvalCount: 4, EvalCount: 2,
		}))
		flusher.Flush()
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	stream := c.StreamChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "llama3",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})

	var contents []string
	var sawFinish bool
	for stream.Next() {
		chunk := stream.Current()
		if len(chunk.Choices) > 0 {
			if chunk.Choices[0].Delta.Content != "" {
				contents = append(contents, chunk.Choices[0].Delta.Content)
			}
			if chunk.Choices[0].FinishReason == "stop" {
				sawFinish = true
			}
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if strings.Join(contents, "") != "hello" {
		t.Errorf("contents = %v, want [hel lo]", contents)
	}
	if !sawFinish {
		t.Error("did not observe finish_reason=stop chunk")
	}
}

func TestStreamChatCompletionToolLoop(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		if n == 1 {
			io.WriteString(w, ndjson(t, chatResponse{
				Model: "llama3",
				Message: message{
					Role:      "assistant",
					ToolCalls: []toolCall{{Function: toolCallFunction{Name: "get_weather", Arguments: map[string]any{"city": "SF"}}}},
				},
				Done: true, DoneReason: "tool_calls",
			}))
			flusher.Flush()
			return
		}
		io.WriteString(w, ndjson(t, chatResponse{Model: "llama3", Message: message{Role: "assistant", Content: "sunny today"}, Done: true, DoneReason: "stop"}))
		flusher.Flush()
	}))
	defer srv.Close()

	localServer := &openai.MCPServerFuncs{
		ListToolsFunc: func() []mcp.MCPTool { return []mcp.MCPTool{{Name: "get_weather"}} },
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("sunny"), nil
		},
	}
	c, err := New(openai.Config{BaseURL: srv.URL, LocalServer: localServer})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	stream := c.StreamChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "llama3",
		Messages: []openai.Message{{Role: "user", Content: "weather?"}},
	})
	var full strings.Builder
	for stream.Next() {
		chunk := stream.Current()
		if len(chunk.Choices) > 0 {
			full.WriteString(chunk.Choices[0].Delta.Content)
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("server calls = %d, want 2", calls.Load())
	}
	if full.String() != "sunny today" {
		t.Errorf("assembled content = %q, want %q", full.String(), "sunny today")
	}
}

func TestStreamChatCompletionMaxIterations(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, ndjson(t, chatResponse{
			Model:   "llama3",
			Message: message{Role: "assistant", ToolCalls: []toolCall{{Function: toolCallFunction{Name: "loop_tool"}}}},
			Done:    true, DoneReason: "tool_calls",
		}))
		flusher.Flush()
	}))
	defer srv.Close()

	localServer := &openai.MCPServerFuncs{
		ListToolsFunc: func() []mcp.MCPTool { return []mcp.MCPTool{{Name: "loop_tool"}} },
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("again"), nil
		},
	}
	c, err := New(openai.Config{BaseURL: srv.URL, LocalServer: localServer})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	stream := c.StreamChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "llama3",
		Messages: []openai.Message{{Role: "user", Content: "loop"}},
	})
	for stream.Next() {
	}
	if err := stream.Err(); err == nil {
		t.Fatal("expected max tool iterations error")
	}
}

func TestStreamChatCompletionRetryOn429(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`rate limited`))
			return
		}
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, ndjson(t, chatResponse{Model: "llama3", Message: message{Role: "assistant", Content: "ok"}, Done: true, DoneReason: "stop"}))
		flusher.Flush()
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL, MaxRetries: 3, RetryBackoff: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	stream := c.StreamChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "llama3",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	for stream.Next() {
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	meta := stream.Retry()
	if meta == nil {
		t.Fatal("expected RetryMetadata after 429 retries")
	}
	if meta.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3", meta.Attempts)
	}
	if !meta.RateLimitHit {
		t.Error("RateLimitHit = false, want true")
	}
}

func TestStreamChatCompletionRetryOn5xx(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`unavailable`))
			return
		}
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, ndjson(t, chatResponse{Model: "llama3", Message: message{Role: "assistant", Content: "ok"}, Done: true, DoneReason: "stop"}))
		flusher.Flush()
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL, MaxRetries: 2, RetryBackoff: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	stream := c.StreamChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "llama3",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	for stream.Next() {
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	meta := stream.Retry()
	if meta == nil || meta.Attempts != 2 {
		t.Fatalf("meta = %+v, want Attempts=2", meta)
	}
	if meta.RateLimitHit {
		t.Error("RateLimitHit = true for 5xx, want false")
	}
}

func TestStreamChatCompletionRetryExhausted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`rate limited`))
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL, MaxRetries: 2, RetryBackoff: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	stream := c.StreamChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "llama3",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	for stream.Next() {
	}
	if err := stream.Err(); err == nil {
		t.Fatal("expected error after retries exhausted")
	}
}

func TestStreamChatCompletionNonRetryableStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`bad request`))
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL, MaxRetries: 3, RetryBackoff: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	stream := c.StreamChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "llama3",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	for stream.Next() {
	}
	if err := stream.Err(); err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("err = %v, want 400 error", err)
	}
}

func TestStreamChatCompletionContextCancelDuringBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`rate limited`))
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL, MaxRetries: 10, RetryBackoff: 5 * time.Second})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	stream := c.StreamChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    "llama3",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	for stream.Next() {
	}
	elapsed := time.Since(start)
	if err := stream.Err(); err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %v, should have cancelled quickly", elapsed)
	}
}

// -----------------------------------------------------------------------------
// readChatStream: NDJSON parsing internals
// -----------------------------------------------------------------------------

func TestReadChatStreamSkipsBlankAndMalformedLines(t *testing.T) {
	body := "\n" + `not json at all` + "\n" + ndjson(t, chatResponse{Model: "m", Message: message{Role: "assistant", Content: "ok"}, Done: true, DoneReason: "stop"})
	c := &Client{}
	ch := make(chan openai.ChatCompletionResponse, 10)
	resp, err := c.readChatStream(context.Background(), strings.NewReader(body), "m", ch)
	if err != nil {
		t.Fatalf("readChatStream() error: %v", err)
	}
	if resp.Choices[0].Message.GetContentAsString() != "ok" {
		t.Errorf("content = %q, want ok", resp.Choices[0].Message.GetContentAsString())
	}
	close(ch)
	var n int
	for range ch {
		n++
	}
	// One chunk for the malformed-but-skipped line never gets sent; only the
	// valid line produces a chunk.
	if n != 1 {
		t.Errorf("chunks sent = %d, want 1", n)
	}
}

func TestReadChatStreamToolCallAssembly(t *testing.T) {
	// Multiple NDJSON lines each carrying tool call index 0: last write wins
	// per the documented behaviour.
	body := ndjson(t, chatResponse{Message: message{ToolCalls: []toolCall{{Function: toolCallFunction{Name: "f", Arguments: map[string]any{"a": 1}}}}}}) +
		ndjson(t, chatResponse{Message: message{ToolCalls: []toolCall{{Function: toolCallFunction{Name: "f", Arguments: map[string]any{"a": 2}}}}}}) +
		ndjson(t, chatResponse{Done: true, DoneReason: "tool_calls"})

	c := &Client{}
	ch := make(chan openai.ChatCompletionResponse, 10)
	resp, err := c.readChatStream(context.Background(), strings.NewReader(body), "m", ch)
	if err != nil {
		t.Fatalf("readChatStream() error: %v", err)
	}
	if len(resp.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v, want 1", resp.Choices[0].Message.ToolCalls)
	}
	if resp.Choices[0].Message.ToolCalls[0].Function.Arguments["a"] != float64(2) {
		t.Errorf("arguments = %+v, want a=2 (last write wins)", resp.Choices[0].Message.ToolCalls[0].Function.Arguments)
	}
	if resp.Choices[0].FinishReason != "tool_calls" {
		t.Errorf("finish reason = %q, want tool_calls", resp.Choices[0].FinishReason)
	}
}

func TestReadChatStreamToolCallsNoContentSetsFinishReason(t *testing.T) {
	// When the assembled message has tool calls and no text content, finish
	// reason must be forced to tool_calls even without a terminal done_reason.
	body := ndjson(t, chatResponse{Message: message{ToolCalls: []toolCall{{Function: toolCallFunction{Name: "f"}}}}})
	c := &Client{}
	ch := make(chan openai.ChatCompletionResponse, 10)
	resp, err := c.readChatStream(context.Background(), strings.NewReader(body), "m", ch)
	if err != nil {
		t.Fatalf("readChatStream() error: %v", err)
	}
	if resp.Choices[0].FinishReason != "tool_calls" {
		t.Errorf("finish reason = %q, want tool_calls", resp.Choices[0].FinishReason)
	}
}

// errReader fails after producing some data, to exercise scanner.Err().
type errReader struct {
	data []byte
	sent bool
}

func (r *errReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		n := copy(p, r.data)
		return n, nil
	}
	return 0, fmt.Errorf("boom read error")
}

func TestReadChatStreamScannerError(t *testing.T) {
	c := &Client{}
	ch := make(chan openai.ChatCompletionResponse, 10)
	_, err := c.readChatStream(context.Background(), &errReader{data: []byte("partial line without newline")}, "m", ch)
	if err == nil || !strings.Contains(err.Error(), "boom read error") {
		t.Fatalf("error = %v, want boom read error", err)
	}
}

func TestReadChatStreamContextCancelledMidStream(t *testing.T) {
	// A long stream of lines with a channel that's never drained and a small
	// buffer forces the ctx.Done() branch inside the send-select to trigger
	// once the context is cancelled.
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		w := bufio.NewWriter(pw)
		for i := 0; i < 5; i++ {
			w.WriteString(ndjson(t, chatResponse{Message: message{Content: "x"}}))
			w.Flush()
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before we start reading
	c := &Client{}
	ch := make(chan openai.ChatCompletionResponse) // unbuffered, nobody reads
	_, err := c.readChatStream(ctx, pr, "m", ch)
	if err == nil {
		t.Fatal("expected context-cancelled error")
	}
}
