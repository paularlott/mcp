package gemini

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/paularlott/mcp/ai/openai"
)

func boolPtr(v bool) *bool { return &v }

func TestGeminiConfigDefaults(t *testing.T) {
	c, err := New(openai.Config{BaseURL: "http://localhost/"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if c.maxRetries != 3 {
		t.Errorf("maxRetries = %d, want 3", c.maxRetries)
	}
	if c.retryBackoff != time.Second {
		t.Errorf("retryBackoff = %v, want 1s", c.retryBackoff)
	}
	if !c.retryOnRateLimit {
		t.Error("retryOnRateLimit = false, want true")
	}
	if !c.retryOnServerError {
		t.Error("retryOnServerError = false, want true")
	}
}

func TestGeminiConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  openai.Config
		wantErr string
	}{
		{
			name:    "negative max retries",
			config:  openai.Config{MaxRetries: -2},
			wantErr: "invalid MaxRetries",
		},
		{
			name:    "negative retry backoff",
			config:  openai.Config{RetryBackoff: -time.Second},
			wantErr: "invalid RetryBackoff",
		},
		{
			name:   "-1 disables retries",
			config: openai.Config{MaxRetries: -1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.config)
			if tt.wantErr != "" {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.wantErr)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestGeminiShouldRetry(t *testing.T) {
	tests := []struct {
		name               string
		retryOnRateLimit   bool
		retryOnServerError bool
		statusCode         int
		want               bool
	}{
		{"429 on", true, true, 429, true},
		{"429 off", false, true, 429, false},
		{"500 on", true, true, 500, true},
		{"500 off", true, false, 500, false},
		{"503 on", true, true, 503, true},
		{"400 never", true, true, 400, false},
		{"200 no retry", true, true, 200, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{
				retryOnRateLimit:   tt.retryOnRateLimit,
				retryOnServerError: tt.retryOnServerError,
			}
			got := c.shouldRetry(tt.statusCode)
			if got != tt.want {
				t.Errorf("shouldRetry(%d) = %v, want %v", tt.statusCode, got, tt.want)
			}
		})
	}
}

func TestGeminiBackoffForAttempt(t *testing.T) {
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
		{5, 30 * time.Second},
		{31, 30 * time.Second},
	}
	for _, tt := range tests {
		got := c.backoffForAttempt(tt.attempt)
		if got != tt.want {
			t.Errorf("backoffForAttempt(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}

// doRequest is used by GetModels and CreateEmbedding — test retry on those paths.
func TestGeminiDoRequestRetryOn429(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`rate limited`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"models":[]}`))
	}))
	defer srv.Close()

	c := &Client{
		baseURL:            srv.URL + "/",
		maxRetries:         3,
		retryBackoff:       10 * time.Millisecond,
		retryOnRateLimit:   true,
		retryOnServerError: true,
	}

	var result struct {
		Models []struct{ Name string } `json:"models"`
	}
	err := c.doRequest(context.Background(), "GET", "models", nil, &result)
	if err != nil {
		t.Fatalf("doRequest() error: %v", err)
	}
	if int(attempts.Load()) != 3 {
		t.Errorf("attempts = %d, want 3", attempts.Load())
	}
}

func TestGeminiDoRequestRetryExhausted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`rate limited`))
	}))
	defer srv.Close()

	c := &Client{
		baseURL:            srv.URL + "/",
		maxRetries:         2,
		retryBackoff:       10 * time.Millisecond,
		retryOnRateLimit:   true,
		retryOnServerError: true,
	}

	err := c.doRequest(context.Background(), "GET", "models", nil, nil)
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
}

func TestGeminiDoRequestNonRetryable(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`bad request`))
	}))
	defer srv.Close()

	c := &Client{
		baseURL:            srv.URL + "/",
		maxRetries:         3,
		retryBackoff:       10 * time.Millisecond,
		retryOnRateLimit:   true,
		retryOnServerError: true,
	}

	err := c.doRequest(context.Background(), "GET", "models", nil, nil)
	if err == nil {
		t.Fatal("expected error from 400")
	}
	if int(attempts.Load()) != 1 {
		t.Errorf("attempts = %d, want 1 (no retry on 400)", attempts.Load())
	}
}

func TestGeminiDoRequestRespectsContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`rate limited`))
	}))
	defer srv.Close()

	c := &Client{
		baseURL:            srv.URL + "/",
		maxRetries:         10,
		retryBackoff:       5 * time.Second,
		retryOnRateLimit:   true,
		retryOnServerError: true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := c.doRequest(ctx, "GET", "models", nil, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %v, should have cancelled quickly", elapsed)
	}
}

// Chat and streaming retry for Gemini delegates to the OpenAI client — test that
// the retry config is propagated through.
func TestGeminiChatRetryPropagated(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"slow down","code":"rate_limit_exceeded"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"chat-ok","object":"chat.completion","model":"gemini-test","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	c, err := New(openai.Config{
		BaseURL:      srv.URL + "/",
		MaxRetries:   2,
		RetryBackoff: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	resp, err := c.ChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "gemini-test",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error: %v", err)
	}
	if resp.Retry == nil {
		t.Fatal("expected RetryMetadata — retry config was not propagated to OpenAI chat client")
	}
	if resp.Retry.Attempts != 2 {
		t.Errorf("Attempts = %d, want 2", resp.Retry.Attempts)
	}
	if !resp.Retry.RateLimitHit {
		t.Error("RateLimitHit = false, want true")
	}
}

// TestGeminiDoRequestRetryHonorsRetryAfterHeader verifies that a Retry-After: 0 header
// does not break Gemini's own doRequest retry logic (functional correctness, not timing).
func TestGeminiDoRequestRetryHonorsRetryAfterHeader(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"message":"rate limit"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"models":[{"name":"models/gemini-test"}]}`))
	}))
	defer srv.Close()

	c, err := New(openai.Config{
		BaseURL:      srv.URL + "/",
		MaxRetries:   2,
		RetryBackoff: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	_, err = c.GetModels(context.Background())
	if err != nil {
		t.Fatalf("GetModels() error: %v", err)
	}
	if attempts.Load() != 2 {
		t.Errorf("attempts = %d, want 2", attempts.Load())
	}
}

// TestGeminiRetryAfterUsedAsFloor verifies that Retry-After: 1 is used as a floor
// for Gemini's own doRequest. Skipped in short mode.
func TestGeminiRetryAfterUsedAsFloor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing test in short mode")
	}

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 2 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"message":"rate limit"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"models":[{"name":"models/gemini-test"}]}`))
	}))
	defer srv.Close()

	c, err := New(openai.Config{
		BaseURL:      srv.URL + "/",
		MaxRetries:   2,
		RetryBackoff: 1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	start := time.Now()
	_, err = c.GetModels(context.Background())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("GetModels() error: %v", err)
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("elapsed = %v, want >= 900ms (Retry-After: 1 should be used as floor)", elapsed)
	}
}
