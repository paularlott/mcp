package claude

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/paularlott/mcp/ai/openai"
	"github.com/paularlott/mcp/pool"
)

// --- doRequest low-level error paths (direct calls, bypassing the ChatCompletion wrapper) ---

func TestDoRequest_MarshalError(t *testing.T) {
	c := &Client{baseURL: "http://localhost/"}
	// A channel cannot be marshalled to JSON.
	badBody := map[string]any{"ch": make(chan int)}
	err := c.doRequest(context.Background(), "POST", "messages", badBody, nil)
	if err == nil || !strings.Contains(err.Error(), "failed to marshal request") {
		t.Fatalf("expected marshal error, got %v", err)
	}
}

func TestDoRequest_InvalidMethod(t *testing.T) {
	c := &Client{baseURL: "http://localhost/"}
	err := c.doRequest(context.Background(), "BAD METHOD", "messages", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "failed to create request") {
		t.Fatalf("expected request-creation error, got %v", err)
	}
}

func TestDoRequest_UsesCustomHTTPPool(t *testing.T) {
	srv := claudeAPIServer(t, "via custom pool")
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL + "/", HTTPPool: pool.NewPool(&pool.PoolConfig{})})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	resp, err := c.ChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error: %v", err)
	}
	if resp.Choices[0].Message.GetContentAsString() != "via custom pool" {
		t.Errorf("unexpected content: %+v", resp.Choices[0].Message)
	}
}

// --- streamRequest low-level error paths ---

func TestStreamRequest_MarshalError(t *testing.T) {
	c := &Client{baseURL: "http://localhost/"}
	badBody := map[string]any{"ch": make(chan int)}
	_, err := c.streamRequest(context.Background(), "POST", "messages", badBody, func(*ClaudeStreamEvent) (bool, error) { return false, nil })
	if err == nil || !strings.Contains(err.Error(), "failed to marshal request") {
		t.Fatalf("expected marshal error, got %v", err)
	}
}

func TestStreamRequest_InvalidMethod(t *testing.T) {
	c := &Client{baseURL: "http://localhost/"}
	_, err := c.streamRequest(context.Background(), "BAD METHOD", "messages", nil, func(*ClaudeStreamEvent) (bool, error) { return false, nil })
	if err == nil || !strings.Contains(err.Error(), "failed to create request") {
		t.Fatalf("expected request-creation error, got %v", err)
	}
}

func TestStreamRequest_UsesCustomHTTPPool(t *testing.T) {
	sse := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"model\":\"claude-test\",\"content\":[]}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"pooled\"}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sse))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL + "/", HTTPPool: pool.NewPool(&pool.PoolConfig{})})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	stream := c.StreamChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	var got string
	for stream.Next() {
		chunk := stream.Current()
		if len(chunk.Choices) > 0 {
			got += chunk.Choices[0].Delta.Content
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if got != "pooled" {
		t.Errorf("got = %q, want %q", got, "pooled")
	}
}

func TestStreamChatCompletion_RetryDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`))
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL + "/", MaxRetries: -1})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	stream := c.StreamChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	for stream.Next() {
	}
	if err := stream.Err(); err == nil {
		t.Fatal("expected error from 429 with retries disabled")
	}
}

func TestStreamChatCompletion_RetryAfterUsedAsFloor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing test in short mode")
	}

	var requestCount int
	sse := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"model\":\"claude-test\",\"content\":[]}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount < 2 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sse))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL + "/", MaxRetries: 2, RetryBackoff: 1 * time.Millisecond})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	start := time.Now()
	stream := c.StreamChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	for stream.Next() {
	}
	elapsed := time.Since(start)

	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("elapsed = %v, want >= 900ms (Retry-After: 1 should be used as floor)", elapsed)
	}
}

func TestStreamChatCompletion_RetryRespectsContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`))
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL + "/", MaxRetries: 10, RetryBackoff: 5 * time.Second})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	stream := c.StreamChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    "claude-test",
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
