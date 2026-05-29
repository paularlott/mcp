package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func boolPtr(v bool) *bool { return &v }

func TestShouldRetry(t *testing.T) {
	tests := []struct {
		name             string
		retryOnRateLimit bool
		retryOnServerErr bool
		err              error
		want             bool
	}{
		{
			name:             "nil error",
			retryOnRateLimit: true,
			retryOnServerErr: true,
			err:              nil,
			want:             false,
		},
		{
			name:             "non-APIError",
			retryOnRateLimit: true,
			retryOnServerErr: true,
			err:              fmt.Errorf("some error"),
			want:             false,
		},
		{
			name:             "429 with retry on",
			retryOnRateLimit: true,
			retryOnServerErr: true,
			err:              NewRateLimitError("slow down"),
			want:             true,
		},
		{
			name:             "429 with retry off",
			retryOnRateLimit: false,
			retryOnServerErr: true,
			err:              NewRateLimitError("slow down"),
			want:             false,
		},
		{
			name:             "500 with retry on",
			retryOnRateLimit: true,
			retryOnServerErr: true,
			err:              NewServerError("internal error"),
			want:             true,
		},
		{
			name:             "500 with retry off",
			retryOnRateLimit: true,
			retryOnServerErr: false,
			err:              NewServerError("internal error"),
			want:             false,
		},
		{
			name:             "400 never retries",
			retryOnRateLimit: true,
			retryOnServerErr: true,
			err:              NewInvalidRequestError("bad request"),
			want:             false,
		},
		{
			name:             "401 never retries",
			retryOnRateLimit: true,
			retryOnServerErr: true,
			err:              NewAuthenticationError("unauthorized"),
			want:             false,
		},
		{
			name:             "both off 429",
			retryOnRateLimit: false,
			retryOnServerErr: false,
			err:              NewRateLimitError("slow down"),
			want:             false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{
				retryOnRateLimit:   tt.retryOnRateLimit,
				retryOnServerError: tt.retryOnServerErr,
			}
			got := c.shouldRetry(tt.err)
			if got != tt.want {
				t.Errorf("shouldRetry() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBackoffForAttempt(t *testing.T) {
	c := &Client{retryBackoff: time.Second}

	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 1 * time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 16 * time.Second},
		{5, 30 * time.Second}, // capped
		{10, 30 * time.Second},
		{30, 30 * time.Second},
		{31, 30 * time.Second}, // overflow guard
		{100, 30 * time.Second},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("attempt_%d", tt.attempt), func(t *testing.T) {
			got := c.backoffForAttempt(tt.attempt)
			if got != tt.want {
				t.Errorf("backoffForAttempt(%d) = %v, want %v", tt.attempt, got, tt.want)
			}
		})
	}
}

func TestBackoffForAttempt_SmallBase(t *testing.T) {
	c := &Client{retryBackoff: 100 * time.Millisecond}

	got := c.backoffForAttempt(5)
	// 100ms * 2^5 = 100ms * 32 = 3200ms, not 6.4s
	if got != 3200*time.Millisecond {
		t.Errorf("backoffForAttempt(5) = %v, want %v", got, 3200*time.Millisecond)
	}

	got = c.backoffForAttempt(10)
	if got != 30*time.Second {
		t.Errorf("backoffForAttempt(10) = %v, want 30s (capped)", got)
	}
}

func TestConfigDefaults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID:      "test",
			Object:  "chat.completion",
			Model:   "test",
			Choices: []Choice{},
		})
	}))
	defer srv.Close()

	client, err := New(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if client.maxRetries != 3 {
		t.Errorf("maxRetries = %d, want 3 (default)", client.maxRetries)
	}
	if client.retryBackoff != time.Second {
		t.Errorf("retryBackoff = %v, want 1s (default)", client.retryBackoff)
	}
	if client.retryOnRateLimit != true {
		t.Errorf("retryOnRateLimit = %v, want true (default)", client.retryOnRateLimit)
	}
	if client.retryOnServerError != true {
		t.Errorf("retryOnServerError = %v, want true (default)", client.retryOnServerError)
	}
}

func TestConfigExplicitValues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID:      "test",
			Object:  "chat.completion",
			Model:   "test",
			Choices: []Choice{},
		})
	}))
	defer srv.Close()

	client, err := New(Config{
		BaseURL:          srv.URL,
		MaxRetries:       5,
		RetryBackoff:     2 * time.Second,
		RetryOnRateLimit: boolPtr(false),
		RetryOnServerError: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if client.maxRetries != 5 {
		t.Errorf("maxRetries = %d, want 5", client.maxRetries)
	}
	if client.retryBackoff != 2*time.Second {
		t.Errorf("retryBackoff = %v, want 2s", client.retryBackoff)
	}
	if client.retryOnRateLimit != false {
		t.Errorf("retryOnRateLimit = %v, want false", client.retryOnRateLimit)
	}
	if client.retryOnServerError != false {
		t.Errorf("retryOnServerError = %v, want false", client.retryOnServerError)
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr string
	}{
		{
			name:    "negative max retries",
			config:  Config{MaxRetries: -2},
			wantErr: "invalid MaxRetries",
		},
		{
			name:    "negative retry backoff",
			config:  Config{RetryBackoff: -1 * time.Second},
			wantErr: "invalid RetryBackoff",
		},
		{
			name:   "max retries -1 is valid (disable)",
			config: Config{MaxRetries: -1},
		},
		{
			name:   "max retries 0 uses default",
			config: Config{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.config)
			if tt.wantErr != "" {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.wantErr)
				} else if !contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want containing %q", err.Error(), tt.wantErr)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestRetryOn429(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"slow down","code":"rate_limit_exceeded"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID:      "test-ok",
			Object:  "chat.completion",
			Model:   "test",
			Choices: []Choice{{Index: 0, Message: Message{Role: "assistant", Content: "hello"}, FinishReason: "stop"}},
		})
	}))
	defer srv.Close()

	client, err := New(Config{
		BaseURL:     srv.URL,
		MaxRetries:  3,
		RetryBackoff: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	resp, err := client.ChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    "test",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error: %v", err)
	}

	if resp.ID != "test-ok" {
		t.Errorf("response ID = %q, want test-ok", resp.ID)
	}
	if resp.Retry == nil {
		t.Fatal("expected Retry metadata on response")
	}
	if resp.Retry.Attempts != 3 {
		t.Errorf("Retry.Attempts = %d, want 3", resp.Retry.Attempts)
	}
	if !resp.Retry.RateLimitHit {
		t.Error("Retry.RateLimitHit = false, want true")
	}
	if resp.Retry.TotalBackoff <= 0 {
		t.Errorf("Retry.TotalBackoff = %v, want > 0", resp.Retry.TotalBackoff)
	}
}

func TestRetryOn5xx(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":{"type":"server_error","message":"internal"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID:      "test-ok",
			Object:  "chat.completion",
			Model:   "test",
			Choices: []Choice{{Index: 0, Message: Message{Role: "assistant", Content: "hello"}, FinishReason: "stop"}},
		})
	}))
	defer srv.Close()

	client, err := New(Config{
		BaseURL:     srv.URL,
		MaxRetries:  2,
		RetryBackoff: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	resp, err := client.ChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    "test",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error: %v", err)
	}

	if resp.Retry == nil {
		t.Fatal("expected Retry metadata")
	}
	if resp.Retry.Attempts != 2 {
		t.Errorf("Retry.Attempts = %d, want 2", resp.Retry.Attempts)
	}
	if resp.Retry.RateLimitHit {
		t.Error("Retry.RateLimitHit = true for 5xx, want false")
	}
}

func TestRetryDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"slow down"}}`))
	}))
	defer srv.Close()

	client, err := New(Config{
		BaseURL:    srv.URL,
		MaxRetries: -1,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	_, err = client.ChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    "test",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error from 429, got nil")
	}
}

func TestRetryOffByFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"slow down"}}`))
	}))
	defer srv.Close()

	client, err := New(Config{
		BaseURL:          srv.URL,
		MaxRetries:       3,
		RetryBackoff:     10 * time.Millisecond,
		RetryOnRateLimit: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	_, err = client.ChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    "test",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error from 429, got nil")
	}
}

func TestRetryExhausted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"slow down"}}`))
	}))
	defer srv.Close()

	client, err := New(Config{
		BaseURL:     srv.URL,
		MaxRetries:  2,
		RetryBackoff: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	_, err = client.ChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    "test",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error after retries exhausted, got nil")
	}
	if !contains(err.Error(), "rate_limit") {
		t.Errorf("error = %q, want rate_limit error", err.Error())
	}
}

func TestNoRetryMetadataOnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID:      "test-ok",
			Object:  "chat.completion",
			Model:   "test",
			Choices: []Choice{{Index: 0, Message: Message{Role: "assistant", Content: "hello"}, FinishReason: "stop"}},
		})
	}))
	defer srv.Close()

	client, err := New(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	resp, err := client.ChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    "test",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error: %v", err)
	}

	if resp.Retry != nil {
		t.Errorf("Retry = %+v, want nil on first-attempt success", resp.Retry)
	}
}

func TestRetryRespectsContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"slow down"}}`))
	}))
	defer srv.Close()

	client, err := New(Config{
		BaseURL:     srv.URL,
		MaxRetries:  10,
		RetryBackoff: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = client.ChatCompletion(ctx, ChatCompletionRequest{
		Model:    "test",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %v, should have cancelled quickly", elapsed)
	}
}

func TestRetryNonRetryableError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"bad"}}`))
	}))
	defer srv.Close()

	client, err := New(Config{
		BaseURL:     srv.URL,
		MaxRetries:  3,
		RetryBackoff: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	_, err = client.ChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    "test",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error from 400, got nil")
	}
	if !contains(err.Error(), "invalid_request") && !contains(err.Error(), "bad") {
		t.Errorf("error = %q, want bad request error", err.Error())
	}
}

func TestAPIErrorHelpers(t *testing.T) {
	tests := []struct {
		name      string
		err       *APIError
		isRetry   bool
		isRate    bool
		isServer  bool
	}{
		{
			name:     "429",
			err:      NewRateLimitError("slow"),
			isRetry:  true,
			isRate:   true,
			isServer: false,
		},
		{
			name:     "500",
			err:      NewServerError("boom"),
			isRetry:  true,
			isRate:   false,
			isServer: true,
		},
		{
			name:     "400",
			err:      NewInvalidRequestError("bad"),
			isRetry:  false,
			isRate:   false,
			isServer: false,
		},
		{
			name:     "401",
			err:      NewAuthenticationError("nope"),
			isRetry:  false,
			isRate:   false,
			isServer: false,
		},
		{
			name: "503 via status code",
			err: &APIError{StatusCode: 503, Type: "server_error", Message: "unavailable"},
			isRetry:  true,
			isRate:   false,
			isServer: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.IsRetryable(); got != tt.isRetry {
				t.Errorf("IsRetryable() = %v, want %v", got, tt.isRetry)
			}
			if got := tt.err.IsRateLimit(); got != tt.isRate {
				t.Errorf("IsRateLimit() = %v, want %v", got, tt.isRate)
			}
			if got := tt.err.IsServerError(); got != tt.isServer {
				t.Errorf("IsServerError() = %v, want %v", got, tt.isServer)
			}
		})
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// sseChunk formats a server-sent event data line for use in test servers.
func sseChunk(data string) string {
	return "data: " + data + "\n\n"
}

func TestStreamRetryOn429(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"slow down","code":"rate_limit_exceeded"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("server does not support flushing")
			return
		}
		chunk := ChatCompletionResponse{
			ID:     "stream-ok",
			Object: "chat.completion.chunk",
			Model:  "test",
			Choices: []Choice{{
				Index:        0,
			Delta:        Delta{Content: "hello"},
				FinishReason: "",
			}},
		}
		data, _ := json.Marshal(chunk)
		w.Write([]byte(sseChunk(string(data))))
		flusher.Flush()
		w.Write([]byte(sseChunk("[DONE]")))
		flusher.Flush()
	}))
	defer srv.Close()

	client, err := New(Config{
		BaseURL:      srv.URL,
		MaxRetries:   3,
		RetryBackoff: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	stream := client.StreamChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    "test",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})

	var chunks []ChatCompletionResponse
	for stream.Next() {
		chunks = append(chunks, stream.Current())
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}

	meta := stream.Retry()
	if meta == nil {
		t.Fatal("expected RetryMetadata on stream after 429 retries")
	}
	if meta.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3", meta.Attempts)
	}
	if !meta.RateLimitHit {
		t.Error("RateLimitHit = false, want true")
	}
	if meta.TotalBackoff <= 0 {
		t.Errorf("TotalBackoff = %v, want > 0", meta.TotalBackoff)
	}
}

func TestStreamRetryOn5xx(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error":{"type":"server_error","message":"unavailable"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("server does not support flushing")
			return
		}
		chunk := ChatCompletionResponse{
			ID:     "stream-ok",
			Object: "chat.completion.chunk",
			Model:  "test",
			Choices: []Choice{{
				Index:        0,
			Delta:        Delta{Content: "hello"},
				FinishReason: "",
			}},
		}
		data, _ := json.Marshal(chunk)
		w.Write([]byte(sseChunk(string(data))))
		flusher.Flush()
		w.Write([]byte(sseChunk("[DONE]")))
		flusher.Flush()
	}))
	defer srv.Close()

	client, err := New(Config{
		BaseURL:      srv.URL,
		MaxRetries:   2,
		RetryBackoff: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	stream := client.StreamChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    "test",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})

	for stream.Next() {
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}

	meta := stream.Retry()
	if meta == nil {
		t.Fatal("expected RetryMetadata on stream after 5xx retry")
	}
	if meta.Attempts != 2 {
		t.Errorf("Attempts = %d, want 2", meta.Attempts)
	}
	if meta.RateLimitHit {
		t.Error("RateLimitHit = true for 5xx, want false")
	}
}

func TestStreamNoRetryMetadataOnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("server does not support flushing")
			return
		}
		w.Write([]byte(sseChunk("[DONE]")))
		flusher.Flush()
	}))
	defer srv.Close()

	client, err := New(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	stream := client.StreamChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    "test",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})

	for stream.Next() {
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}

	meta := stream.Retry()
	if meta != nil {
		t.Errorf("Retry = %+v, want nil on first-attempt success", meta)
	}
}

// TestParseRetryAfter tests the parseRetryAfter helper with various header formats.
func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"empty", "", 0},
		{"zero seconds", "0", 0},
		{"negative seconds", "-1", 0},
		{"one second", "1", time.Second},
		{"sixty seconds", "60", 60 * time.Second},
		{"whitespace", "  30  ", 30 * time.Second},
		{"invalid string", "abc", 0},
		{"past date", "Mon, 01 Jan 2000 00:00:00 GMT", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRetryAfter(tt.header)
			if got != tt.want {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}

	// Future HTTP-date should return a positive duration.
	futureDate := time.Now().Add(5 * time.Minute).UTC().Format(http.TimeFormat)
	got := parseRetryAfter(futureDate)
	if got <= 0 {
		t.Errorf("parseRetryAfter(future date) = %v, want > 0", got)
	}
	if got > 6*time.Minute {
		t.Errorf("parseRetryAfter(future date) = %v, want <= 6m", got)
	}
}

// TestRetryHonorsRetryAfterHeader verifies that a Retry-After: 0 header does not
// break retry logic (functional correctness, not timing).
func TestRetryHonorsRetryAfterHeader(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"slow down","code":"rate_limit_exceeded"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID:      "test-ok",
			Object:  "chat.completion",
			Model:   "test",
			Choices: []Choice{{Index: 0, Message: Message{Role: "assistant", Content: "hello"}, FinishReason: "stop"}},
		})
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL, MaxRetries: 2, RetryBackoff: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	resp, err := c.ChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    "test",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error: %v", err)
	}
	if resp.Retry == nil || resp.Retry.Attempts != 2 {
		t.Errorf("Retry.Attempts = %v, want 2", resp.Retry)
	}
}

// TestRetryAfterUsedAsFloor verifies that a Retry-After: 1 header is used as a
// floor when the exponential backoff is smaller. Skipped in short mode.
func TestRetryAfterUsedAsFloor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing test in short mode")
	}

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 2 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"slow down","code":"rate_limit_exceeded"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID:      "test-ok",
			Object:  "chat.completion",
			Model:   "test",
			Choices: []Choice{{Index: 0, Message: Message{Role: "assistant", Content: "hello"}, FinishReason: "stop"}},
		})
	}))
	defer srv.Close()

	// RetryBackoff of 1ms means exponential backoff is negligible; Retry-After: 1 dominates.
	c, err := New(Config{BaseURL: srv.URL, MaxRetries: 2, RetryBackoff: 1 * time.Millisecond})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	start := time.Now()
	resp, err := c.ChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    "test",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("ChatCompletion() error: %v", err)
	}
	if resp.Retry == nil {
		t.Fatal("expected RetryMetadata")
	}
	// Retry-After: 1 means at least 1 second; allow 100ms tolerance for slow CI.
	if elapsed < 900*time.Millisecond {
		t.Errorf("elapsed = %v, want >= 900ms (Retry-After: 1 should be used as floor)", elapsed)
	}
}
