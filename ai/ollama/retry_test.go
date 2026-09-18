package ollama

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/paularlott/mcp/ai/openai"
	"github.com/paularlott/mcp/pool"
)

func boolPtr(v bool) *bool { return &v }

// -----------------------------------------------------------------------------
// New() defaults, validation, and wiring
// -----------------------------------------------------------------------------

func TestNewDefaults(t *testing.T) {
	c, err := New(openai.Config{})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if c.baseURL != defaultBaseURL+"/" {
		t.Errorf("baseURL = %q, want %q", c.baseURL, defaultBaseURL+"/")
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
	if c.requestTimeout != openai.DefaultRequestTimeout {
		t.Errorf("requestTimeout = %v, want %v", c.requestTimeout, openai.DefaultRequestTimeout)
	}
	if c.provider != providerName {
		t.Errorf("provider = %q, want %q", c.provider, providerName)
	}
	if c.responseManager == nil {
		t.Error("responseManager is nil")
	}
}

func TestNewExplicitValues(t *testing.T) {
	temp := 0.4
	top := 0.8
	c, err := New(openai.Config{
		APIKey:             "k",
		BaseURL:            "http://h:1/v1",
		MaxTokens:          100,
		Temperature:        &temp,
		TopP:               &top,
		RequestTimeout:     5 * time.Second,
		MaxRetries:         5,
		RetryBackoff:       2 * time.Second,
		RetryOnRateLimit:   boolPtr(false),
		RetryOnServerError: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if c.baseURL != "http://h:1/" {
		t.Errorf("baseURL = %q, want %q", c.baseURL, "http://h:1/")
	}
	if c.apiKey != "k" {
		t.Errorf("apiKey = %q", c.apiKey)
	}
	if c.maxTokens != 100 {
		t.Errorf("maxTokens = %d", c.maxTokens)
	}
	if c.temperature != &temp && *c.temperature != temp {
		t.Errorf("temperature = %v", c.temperature)
	}
	if c.topP != &top && *c.topP != top {
		t.Errorf("topP = %v", c.topP)
	}
	if c.requestTimeout != 5*time.Second {
		t.Errorf("requestTimeout = %v", c.requestTimeout)
	}
	if c.maxRetries != 5 {
		t.Errorf("maxRetries = %d, want 5", c.maxRetries)
	}
	if c.retryBackoff != 2*time.Second {
		t.Errorf("retryBackoff = %v, want 2s", c.retryBackoff)
	}
	if c.retryOnRateLimit {
		t.Error("retryOnRateLimit = true, want false")
	}
	if c.retryOnServerError {
		t.Error("retryOnServerError = true, want false")
	}
}

func TestNewValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  openai.Config
		wantErr string
	}{
		{"negative max retries", openai.Config{MaxRetries: -2}, "invalid MaxRetries"},
		{"negative retry backoff", openai.Config{RetryBackoff: -time.Second}, "invalid RetryBackoff"},
		{"-1 disables retries", openai.Config{MaxRetries: -1}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := New(tt.config)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if c.maxRetries != -1 {
				t.Errorf("maxRetries = %d, want -1", c.maxRetries)
			}
		})
	}
}

// TestNewRemoteServerConfigs exercises the remote-server construction loop in
// New(), including both the pooled and unpooled branches.
func TestNewRemoteServerConfigs(t *testing.T) {
	customPool := pool.GetPool() // any non-nil HTTPPool works for this test
	c, err := New(openai.Config{
		RemoteServerConfigs: []openai.RemoteServerConfig{
			{BaseURL: "http://remote-a", Namespace: "a"},
			{BaseURL: "http://remote-b", Namespace: "b", HTTPPool: customPool},
		},
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if len(c.remoteServers) != 2 {
		t.Fatalf("remoteServers len = %d, want 2", len(c.remoteServers))
	}
	for i, rs := range c.remoteServers {
		if rs == nil {
			t.Errorf("remoteServers[%d] is nil", i)
		}
	}
}

func TestNormalizeBaseTrailingSlashes(t *testing.T) {
	if got := normalizeBase("http://h/v1/"); got != "http://h/" {
		t.Errorf("normalizeBase = %q, want http://h/", got)
	}
}

// -----------------------------------------------------------------------------
// shouldRetry / backoffForAttempt / parseRetryAfter
// -----------------------------------------------------------------------------

func TestOllamaShouldRetry(t *testing.T) {
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
			c := &Client{retryOnRateLimit: tt.retryOnRateLimit, retryOnServerError: tt.retryOnServerError}
			if got := c.shouldRetry(tt.statusCode); got != tt.want {
				t.Errorf("shouldRetry(%d) = %v, want %v", tt.statusCode, got, tt.want)
			}
		})
	}
}

func TestOllamaBackoffForAttempt(t *testing.T) {
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
		{100, 30 * time.Second},
	}
	for _, tt := range tests {
		if got := c.backoffForAttempt(tt.attempt); got != tt.want {
			t.Errorf("backoffForAttempt(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}

func TestOllamaParseRetryAfter(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"empty", "", 0},
		{"zero", "0", 0},
		{"negative", "-1", 0},
		{"one second", "1", time.Second},
		{"whitespace", "  30  ", 30 * time.Second},
		{"invalid", "abc", 0},
		{"past date", "Mon, 01 Jan 2000 00:00:00 GMT", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseRetryAfter(tt.header); got != tt.want {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}

	future := time.Now().Add(5 * time.Minute).UTC().Format(http.TimeFormat)
	got := parseRetryAfter(future)
	if got <= 0 || got > 6*time.Minute {
		t.Errorf("parseRetryAfter(future) = %v, want (0, 6m]", got)
	}
}

// -----------------------------------------------------------------------------
// doRequest retry behaviour (drives GetModels/CreateEmbedding paths)
// -----------------------------------------------------------------------------

func TestDoRequestRetryOn429(t *testing.T) {
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
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL + "/", maxRetries: 3, retryBackoff: 10 * time.Millisecond, retryOnRateLimit: true, retryOnServerError: true}
	var result map[string]any
	if err := c.doRequest(context.Background(), http.MethodGet, "x", nil, &result); err != nil {
		t.Fatalf("doRequest() error: %v", err)
	}
	if int(attempts.Load()) != 3 {
		t.Errorf("attempts = %d, want 3", attempts.Load())
	}
}

func TestDoRequestRetryOn5xx(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`boom`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL + "/", maxRetries: 2, retryBackoff: 10 * time.Millisecond, retryOnRateLimit: true, retryOnServerError: true}
	if err := c.doRequest(context.Background(), http.MethodGet, "x", nil, nil); err != nil {
		t.Fatalf("doRequest() error: %v", err)
	}
	if int(attempts.Load()) != 2 {
		t.Errorf("attempts = %d, want 2", attempts.Load())
	}
}

func TestDoRequestRetryExhausted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`rate limited`))
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL + "/", maxRetries: 2, retryBackoff: 10 * time.Millisecond, retryOnRateLimit: true, retryOnServerError: true}
	err := c.doRequest(context.Background(), http.MethodGet, "x", nil, nil)
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error = %q, want mention of 429", err.Error())
	}
}

func TestDoRequestNonRetryable(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`bad request`))
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL + "/", maxRetries: 3, retryBackoff: 10 * time.Millisecond, retryOnRateLimit: true, retryOnServerError: true}
	err := c.doRequest(context.Background(), http.MethodGet, "x", nil, nil)
	if err == nil {
		t.Fatal("expected error from 400")
	}
	if int(attempts.Load()) != 1 {
		t.Errorf("attempts = %d, want 1 (no retry on 400)", attempts.Load())
	}
}

func TestDoRequestRespectsContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`rate limited`))
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL + "/", maxRetries: 10, retryBackoff: 5 * time.Second, retryOnRateLimit: true, retryOnServerError: true}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := c.doRequest(ctx, http.MethodGet, "x", nil, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %v, should have cancelled quickly", elapsed)
	}
}

func TestDoRequestRetryHonorsRetryAfterHeader(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`rate limited`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL + "/", maxRetries: 2, retryBackoff: 10 * time.Millisecond, retryOnRateLimit: true, retryOnServerError: true}
	if err := c.doRequest(context.Background(), http.MethodGet, "x", nil, nil); err != nil {
		t.Fatalf("doRequest() error: %v", err)
	}
	if attempts.Load() != 2 {
		t.Errorf("attempts = %d, want 2", attempts.Load())
	}
}

func TestDoRequestRetryAfterUsedAsFloor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing test in short mode")
	}
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 2 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`rate limited`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL + "/", maxRetries: 2, retryBackoff: 1 * time.Millisecond, retryOnRateLimit: true, retryOnServerError: true}
	start := time.Now()
	if err := c.doRequest(context.Background(), http.MethodGet, "x", nil, nil); err != nil {
		t.Fatalf("doRequest() error: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 900*time.Millisecond {
		t.Errorf("elapsed = %v, want >= 900ms (Retry-After: 1 should be used as floor)", elapsed)
	}
}

func TestDoRequestDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL + "/", maxRetries: 0, retryBackoff: time.Millisecond}
	var result map[string]any
	err := c.doRequest(context.Background(), http.MethodGet, "x", nil, &result)
	if err == nil || !strings.Contains(err.Error(), "failed to decode response") {
		t.Errorf("error = %v, want decode error", err)
	}
}

func TestDoRequestMarshalError(t *testing.T) {
	c := &Client{baseURL: "http://example.com/", maxRetries: 0}
	// A channel is never JSON-marshalable.
	err := c.doRequest(context.Background(), http.MethodPost, "x", make(chan int), nil)
	if err == nil || !strings.Contains(err.Error(), "failed to marshal request") {
		t.Errorf("error = %v, want marshal error", err)
	}
}

// -----------------------------------------------------------------------------
// setHeaders / httpClient / decompressBody
// -----------------------------------------------------------------------------

func TestSetHeaders(t *testing.T) {
	extra := http.Header{}
	extra.Set("X-Custom", "v1")
	extra.Add("X-Custom", "v2")
	c := &Client{apiKey: "secret", extraHeaders: extra}

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	c.setHeaders(req)

	if req.Header.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q", req.Header.Get("Content-Type"))
	}
	if req.Header.Get("Accept") != "application/json" {
		t.Errorf("Accept = %q", req.Header.Get("Accept"))
	}
	if req.Header.Get("Authorization") != "Bearer secret" {
		t.Errorf("Authorization = %q", req.Header.Get("Authorization"))
	}
	if got := req.Header.Values("X-Custom"); len(got) != 2 || got[0] != "v1" || got[1] != "v2" {
		t.Errorf("X-Custom = %v", got)
	}
}

func TestSetHeadersNoAPIKey(t *testing.T) {
	c := &Client{}
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	c.setHeaders(req)
	if req.Header.Get("Authorization") != "" {
		t.Errorf("Authorization = %q, want empty", req.Header.Get("Authorization"))
	}
}

func TestHTTPClientDefaultPool(t *testing.T) {
	c := &Client{}
	if c.httpClient() == nil {
		t.Fatal("httpClient() returned nil")
	}
}

func TestHTTPClientCustomPool(t *testing.T) {
	custom := pool.GetPool()
	c := &Client{httpPool: custom}
	got := c.httpClient()
	if got != custom.GetHTTPClient() {
		t.Error("httpClient() did not use the configured pool")
	}
}

func TestDecompressBodyGzip(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	gw.Write([]byte(`{"hello":"world"}`))
	gw.Close()

	resp := &http.Response{
		Header: http.Header{"Content-Encoding": []string{"gzip"}},
		Body:   io.NopCloser(bytes.NewReader(buf.Bytes())),
	}
	body := decompressBody(resp)
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read decompressed body: %v", err)
	}
	if string(data) != `{"hello":"world"}` {
		t.Errorf("decompressed = %q", data)
	}
}

func TestDecompressBodyGzipInvalid(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{"Content-Encoding": []string{"gzip"}},
		Body:   io.NopCloser(strings.NewReader("not gzip data")),
	}
	// gzip.NewReader fails on invalid data (bad magic number), so
	// decompressBody must fall back to resp.Body rather than panicking or
	// returning a nil ReadCloser (gzip.NewReader consumes the header bytes it
	// managed to read before failing, so the fallback body may be partially
	// drained — that's fine, this just guards against a crash/nil return).
	body := decompressBody(resp)
	if body == nil {
		t.Fatal("decompressBody returned nil")
	}
	defer body.Close()
	if _, err := io.ReadAll(body); err != nil {
		t.Errorf("ReadAll() error: %v", err)
	}
}

func TestDecompressBodyPlain(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{},
		Body:   io.NopCloser(strings.NewReader("plain")),
	}
	body := decompressBody(resp)
	defer body.Close()
	data, _ := io.ReadAll(body)
	if string(data) != "plain" {
		t.Errorf("body = %q, want plain", data)
	}
}

// -----------------------------------------------------------------------------
// Provider / SupportsCapability / Close
// -----------------------------------------------------------------------------

func TestProviderSupportsCapabilityClose(t *testing.T) {
	c := &Client{provider: providerName}
	if c.Provider() != providerName {
		t.Errorf("Provider() = %q", c.Provider())
	}
	if c.SupportsCapability("responses") {
		t.Error("SupportsCapability(responses) = true, want false")
	}
	if !c.SupportsCapability("embeddings") {
		t.Error("SupportsCapability(embeddings) = false, want true")
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}
