package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/paularlott/mcp"
)

func TestTimeNowUnix(t *testing.T) {
	got := timeNowUnix()
	want := time.Now().Unix()
	if got < want-2 || got > want+2 {
		t.Errorf("timeNowUnix() = %d, want close to %d", got, want)
	}
}

func TestJSONMarshal(t *testing.T) {
	raw, err := jsonMarshal(map[string]any{"a": 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("round-trip failed: %v", err)
	}
	if m["a"] != float64(1) {
		t.Errorf("m = %v", m)
	}
}

func TestJSONMarshal_Error(t *testing.T) {
	_, err := jsonMarshal(make(chan int))
	if err == nil {
		t.Fatal("expected marshal error for unmarshalable value")
	}
}

func TestResponseStreamEvent_TextDelta(t *testing.T) {
	e := ResponseStreamEvent{Type: "response.output_text.delta", Data: json.RawMessage(`{"delta":"hi"}`)}
	if got := e.TextDelta(); got != "hi" {
		t.Errorf("TextDelta() = %q, want hi", got)
	}
}

func TestResponseStreamEvent_TextDelta_WrongType(t *testing.T) {
	e := ResponseStreamEvent{Type: "response.created", Data: json.RawMessage(`{"delta":"hi"}`)}
	if got := e.TextDelta(); got != "" {
		t.Errorf("TextDelta() = %q, want empty for wrong type", got)
	}
}

func TestResponseStreamEvent_Response(t *testing.T) {
	e := ResponseStreamEvent{
		Type: "response.completed",
		Data: json.RawMessage(`{"response":{"id":"resp_1","object":"response","status":"completed","model":"gpt-x"}}`),
	}
	resp := e.Response()
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.ID != "resp_1" || resp.Status != "completed" {
		t.Errorf("resp = %+v", resp)
	}
}

func TestResponseStreamEvent_Response_WrongType(t *testing.T) {
	e := ResponseStreamEvent{Type: "response.created", Data: json.RawMessage(`{}`)}
	if resp := e.Response(); resp != nil {
		t.Errorf("Response() = %v, want nil for wrong type", resp)
	}
}

func TestResponseStreamEvent_Response_InvalidJSON(t *testing.T) {
	e := ResponseStreamEvent{Type: "response.completed", Data: json.RawMessage(`not json`)}
	if resp := e.Response(); resp != nil {
		t.Errorf("Response() = %v, want nil on invalid JSON", resp)
	}
}

// -----------------------------------------------------------------------------
// ResponseStream iterator
// -----------------------------------------------------------------------------

func drainResponseStream(s *ResponseStream) ([]ResponseStreamEvent, error) {
	var events []ResponseStreamEvent
	for s.Next() {
		events = append(events, s.Current())
	}
	return events, s.Err()
}

func TestResponseStream_NormalCompletion(t *testing.T) {
	eventChan := make(chan ResponseStreamEvent, 2)
	errChan := make(chan error, 1)
	eventChan <- ResponseStreamEvent{Type: "a"}
	eventChan <- ResponseStreamEvent{Type: "b"}
	close(eventChan)
	close(errChan)

	s := NewResponseStream(context.Background(), eventChan, errChan)
	events, err := drainResponseStream(s)
	if err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	if len(events) != 2 || events[0].Type != "a" || events[1].Type != "b" {
		t.Errorf("events = %+v", events)
	}
}

func TestResponseStream_Error(t *testing.T) {
	testErr := errors.New("boom")
	eventChan := make(chan ResponseStreamEvent)
	errChan := make(chan error, 1)
	errChan <- testErr
	close(eventChan)

	s := NewResponseStream(context.Background(), eventChan, errChan)
	_, err := drainResponseStream(s)
	if err != testErr {
		t.Errorf("Err() = %v, want %v", err, testErr)
	}
}

// TestResponseStream_RaceBothClosedWithError guards against the same class of
// bug that TestChatStream_RaceBothClosedWithError (stream_test.go) catches for
// ChatStream: when the producer sends an error and closes both channels
// together (as StreamResponse's goroutine does), Go's select is
// non-deterministic about which ready case it picks. Next() must never lose
// the error regardless of which channel it happens to drain first.
func TestResponseStream_RaceBothClosedWithError(t *testing.T) {
	testErr := errors.New("mid-stream failure")
	eventChan := make(chan ResponseStreamEvent, 1)
	errChan := make(chan error, 1)
	eventChan <- ResponseStreamEvent{Type: "a"}
	close(eventChan)
	errChan <- testErr
	close(errChan)

	s := NewResponseStream(context.Background(), eventChan, errChan)
	_, err := drainResponseStream(s)
	if err != testErr {
		t.Fatalf("Err() = %v, want %v (error was lost — race bug)", err, testErr)
	}
}

// TestResponseStream_RaceBothClosedWithError_Repeated runs the race scenario
// many times to exercise both select paths (event-first and error-first).
func TestResponseStream_RaceBothClosedWithError_Repeated(t *testing.T) {
	testErr := errors.New("failure")
	for i := 0; i < 200; i++ {
		eventChan := make(chan ResponseStreamEvent, 1)
		errChan := make(chan error, 1)
		eventChan <- ResponseStreamEvent{Type: "a"}
		close(eventChan)
		errChan <- testErr
		close(errChan)

		s := NewResponseStream(context.Background(), eventChan, errChan)
		_, err := drainResponseStream(s)
		if err != testErr {
			t.Fatalf("iteration %d: Err() = %v, want %v", i, err, testErr)
		}
	}
}

func TestResponseStream_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	eventChan := make(chan ResponseStreamEvent)
	errChan := make(chan error)
	s := NewResponseStream(ctx, eventChan, errChan)

	go cancel()

	_, err := drainResponseStream(s)
	if err != context.Canceled {
		t.Errorf("Err() = %v, want %v", err, context.Canceled)
	}
}

func TestResponseStream_CurrentBeforeNext(t *testing.T) {
	eventChan := make(chan ResponseStreamEvent)
	errChan := make(chan error)
	close(eventChan)
	close(errChan)
	s := NewResponseStream(context.Background(), eventChan, errChan)
	if c := s.Current(); c.Type != "" {
		t.Errorf("Current() before Next() = %+v, want zero value", c)
	}
}

func TestResponseStream_ErrChanClosedThenDrains(t *testing.T) {
	eventChan := make(chan ResponseStreamEvent, 1)
	errChan := make(chan error, 1)
	eventChan <- ResponseStreamEvent{Type: "a"}
	close(errChan)
	close(eventChan)
	s := NewResponseStream(context.Background(), eventChan, errChan)
	events, err := drainResponseStream(s)
	if err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	if len(events) != 1 {
		t.Errorf("events = %+v", events)
	}
}

func TestResponseStream_DoneShortCircuits(t *testing.T) {
	eventChan := make(chan ResponseStreamEvent)
	errChan := make(chan error)
	close(eventChan)
	close(errChan)
	s := NewResponseStream(context.Background(), eventChan, errChan)
	s.Next() // drains to done
	if s.Next() {
		t.Fatal("Next() on done stream should return false")
	}
}

// -----------------------------------------------------------------------------
// StreamResponseEmulated (standalone function)
// -----------------------------------------------------------------------------

// streamMockCompleter implements ChatStreamCompleter for testing StreamResponseEmulated
// without needing a real HTTP server.
type streamMockCompleter struct {
	chunks []ChatCompletionResponse
	err    error
}

func (m *streamMockCompleter) ChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	return nil, errors.New("not used")
}

func (m *streamMockCompleter) StreamChatCompletion(ctx context.Context, req ChatCompletionRequest) *ChatStream {
	respCh := make(chan ChatCompletionResponse, len(m.chunks)+1)
	errCh := make(chan error, 1)
	for _, c := range m.chunks {
		respCh <- c
	}
	close(respCh)
	if m.err != nil {
		errCh <- m.err
	}
	close(errCh)
	return NewChatStream(ctx, respCh, errCh)
}

func TestStreamResponseEmulated_FullLifecycle(t *testing.T) {
	mc := &streamMockCompleter{chunks: []ChatCompletionResponse{
		{ID: "c1", Choices: []Choice{{Index: 0, Delta: Delta{Content: "Hello "}}}},
		{ID: "c1", Choices: []Choice{{Index: 0, Delta: Delta{Content: "world"}}}},
		{ID: "c1", Choices: []Choice{{Index: 0, FinishReason: "stop"}}, Usage: &Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5}},
	}}

	eventChan := make(chan ResponseStreamEvent, 20)
	errorChan := make(chan error, 1)

	StreamResponseEmulated(context.Background(), mc, CreateResponseRequest{
		Model: "gpt-x",
		Input: []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
	}, eventChan, errorChan)
	close(eventChan)
	close(errorChan)

	var types []string
	var fullText string
	var finalResp *ResponseObject
	for e := range eventChan {
		types = append(types, e.Type)
		if d := e.TextDelta(); d != "" {
			fullText += d
		}
		if r := e.Response(); r != nil {
			finalResp = r
		}
	}
	if err, ok := <-errorChan; ok && err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if fullText != "Hello world" {
		t.Errorf("fullText = %q, want %q", fullText, "Hello world")
	}
	if finalResp == nil {
		t.Fatal("expected a response.completed event with a full ResponseObject")
	}
	if finalResp.OutputText() != "Hello world" {
		t.Errorf("OutputText() = %q", finalResp.OutputText())
	}
	if finalResp.Usage == nil || finalResp.Usage.InputTokens != 3 {
		t.Errorf("Usage = %+v", finalResp.Usage)
	}

	wantTypes := []string{
		"response.created", "response.in_progress", "response.output_item.added",
		"response.content_part.added", "response.output_text.delta", "response.output_text.delta",
		"response.output_text.done", "response.content_part.done", "response.output_item.done",
		"response.completed",
	}
	if strings.Join(types, ",") != strings.Join(wantTypes, ",") {
		t.Errorf("event types = %v, want %v", types, wantTypes)
	}
}

func TestStreamResponseEmulated_ConvertError(t *testing.T) {
	mc := &streamMockCompleter{}
	eventChan := make(chan ResponseStreamEvent, 5)
	errorChan := make(chan error, 1)

	// An input item type that ConvertResponseToChatRequest cannot process
	// still succeeds (unknown types are just skipped), so instead force an
	// error via ConvertInputToMessages robustness: use a request whose Input
	// causes no error but confirm the error path using a completer error below.
	mc.err = errors.New("stream failed")
	StreamResponseEmulated(context.Background(), mc, CreateResponseRequest{
		Model: "gpt-x",
		Input: []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
	}, eventChan, errorChan)
	close(eventChan)
	close(errorChan)

	err := <-errorChan
	if err == nil || !strings.Contains(err.Error(), "stream failed") {
		t.Fatalf("expected stream failed error, got %v", err)
	}
}

func TestStreamResponseEmulated_ContextCancelledMidSend(t *testing.T) {
	// Use an unbuffered channel with no reader and a cancelled context to force
	// the "send" helper's ctx.Done() branch to return false and abort early.
	mc := &streamMockCompleter{chunks: []ChatCompletionResponse{
		{ID: "c1", Choices: []Choice{{Index: 0, Delta: Delta{Content: "hi"}}}},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	eventChan := make(chan ResponseStreamEvent) // unbuffered, nobody reading
	errorChan := make(chan error, 1)

	done := make(chan struct{})
	go func() {
		StreamResponseEmulated(ctx, mc, CreateResponseRequest{Model: "gpt-x"}, eventChan, errorChan)
		close(done)
	}()

	select {
	case <-done:
		// success: function returned without blocking forever
	case <-time.After(2 * time.Second):
		t.Fatal("StreamResponseEmulated did not return promptly on cancelled context")
	}
}

// -----------------------------------------------------------------------------
// Client.StreamResponse (native and emulated dispatch)
// -----------------------------------------------------------------------------

func TestClient_StreamResponse_Emulated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		writeSSEChunk(w, flusher, ChatCompletionResponse{
			ID: "s1", Choices: []Choice{{Index: 0, Delta: Delta{Content: "hi"}}},
		})
		writeSSEChunk(w, flusher, ChatCompletionResponse{
			ID: "s1", Choices: []Choice{{Index: 0, FinishReason: "stop"}},
		})
		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL}) // native responses off by default for non-openai.com URLs
	stream := c.StreamResponse(context.Background(), CreateResponseRequest{
		Model: "gpt-x",
		Input: []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
	})

	var gotText string
	for stream.Next() {
		e := stream.Current()
		gotText += e.TextDelta()
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if gotText != "hi" {
		t.Errorf("gotText = %q, want hi", gotText)
	}
}

func TestClient_StreamResponse_Native_Simple(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		w.Write([]byte("data: " + `{"type":"response.output_text.delta","delta":"hi"}` + "\n\n"))
		flusher.Flush()
		final, _ := json.Marshal(map[string]any{
			"type": "response.completed",
			"response": ResponseObject{
				ID: "resp_1", Object: "response", Status: "completed", Model: "gpt-x",
			},
		})
		w.Write([]byte("data: " + string(final) + "\n\n"))
		flusher.Flush()
		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer srv.Close()

	c := nativeClient(t, srv)
	stream := c.StreamResponse(context.Background(), CreateResponseRequest{
		Model: "gpt-x",
		Input: []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
	})

	var gotText string
	var sawCompleted bool
	for stream.Next() {
		e := stream.Current()
		gotText += e.TextDelta()
		if e.Response() != nil {
			sawCompleted = true
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if gotText != "hi" {
		t.Errorf("gotText = %q, want hi", gotText)
	}
	if !sawCompleted {
		t.Error("expected a response.completed event")
	}
}

func TestClient_StreamResponse_Native_WithToolLoop(t *testing.T) {
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		if callCount == 1 {
			final, _ := json.Marshal(map[string]any{
				"type": "response.completed",
				"response": ResponseObject{
					ID: "resp_1", Object: "response", Status: "completed", Model: "gpt-x",
					Output: []any{
						map[string]any{"type": "function_call", "call_id": "call_1", "name": "get_weather", "arguments": `{"city":"NYC"}`},
					},
				},
			})
			w.Write([]byte("data: " + string(final) + "\n\n"))
			flusher.Flush()
			w.Write([]byte("data: [DONE]\n\n"))
			flusher.Flush()
			return
		}
		final, _ := json.Marshal(map[string]any{
			"type": "response.completed",
			"response": ResponseObject{
				ID: "resp_2", Object: "response", Status: "completed", Model: "gpt-x",
				Output: []any{
					map[string]any{"type": "message", "role": "assistant", "content": []any{
						map[string]any{"type": "output_text", "text": "sunny"},
					}},
				},
			},
		})
		w.Write([]byte("data: " + string(final) + "\n\n"))
		flusher.Flush()
		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer srv.Close()

	native := true
	local := &MCPServerFuncs{
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("sunny weather"), nil
		},
	}
	c, _ := New(Config{BaseURL: srv.URL, UseNativeResponses: &native, LocalServer: local})

	stream := c.StreamResponse(context.Background(), CreateResponseRequest{
		Model: "gpt-x",
		Input: []any{map[string]any{"type": "message", "role": "user", "content": "weather?"}},
	})

	var gotText string
	for stream.Next() {
		e := stream.Current()
		gotText += e.TextDelta()
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if callCount != 2 {
		t.Errorf("callCount = %d, want 2", callCount)
	}
}
