package claude

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/paularlott/mcp/ai/openai"
)

func TestProviderAndSupportsCapability(t *testing.T) {
	c := &Client{provider: providerName}
	if got := c.Provider(); got != "claude" {
		t.Errorf("Provider() = %q, want %q", got, "claude")
	}
	tests := []struct {
		cap  string
		want bool
	}{
		{"embeddings", false},
		{"responses", false},
		{"chat", true},
		{"anything-else", true},
	}
	for _, tt := range tests {
		if got := c.SupportsCapability(tt.cap); got != tt.want {
			t.Errorf("SupportsCapability(%q) = %v, want %v", tt.cap, got, tt.want)
		}
	}
}

func TestClose(t *testing.T) {
	c := &Client{}
	if err := c.Close(); err != nil {
		t.Errorf("Close() error: %v", err)
	}
}

func TestCreateEmbedding(t *testing.T) {
	c := &Client{}
	_, err := c.CreateEmbedding(context.Background(), openai.EmbeddingRequest{Model: "x"})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("expected 'not supported' error, got %v", err)
	}
}

func TestGetModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":[{"type":"model","id":"claude-3-opus","display_name":"Opus"}],"has_more":false}`))
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL + "/"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	resp, err := c.GetModels(context.Background())
	if err != nil {
		t.Fatalf("GetModels() error: %v", err)
	}
	if resp.Object != "list" || len(resp.Data) != 1 || resp.Data[0].ID != "claude-3-opus" {
		t.Errorf("unexpected models response: %+v", resp)
	}
}

func TestGetModels_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"bad key"}}`))
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL + "/"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	_, err = c.GetModels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("expected error containing 'bad key', got %v", err)
	}
}

// claudeAPIServer returns an httptest server that emulates the Claude
// messages endpoint, always answering with a fixed assistant reply.
func claudeAPIServer(t *testing.T, text string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(claudeFinalTextResponse(text))
	}))
}

func TestCreateResponse_Sync(t *testing.T) {
	srv := claudeAPIServer(t, "the sync answer")
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL + "/"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	resp, err := c.CreateResponse(context.Background(), openai.CreateResponseRequest{
		Model: "claude-test",
		Input: []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatalf("CreateResponse() error: %v", err)
	}
	if resp.Status != "completed" {
		t.Errorf("Status = %q, want completed", resp.Status)
	}

	// Retrieve it again by ID.
	got, err := c.GetResponse(context.Background(), resp.ID)
	if err != nil {
		t.Fatalf("GetResponse() error: %v", err)
	}
	if got.ID != resp.ID {
		t.Errorf("GetResponse ID = %q, want %q", got.ID, resp.ID)
	}
}

func TestCreateResponse_BackgroundThenGetThenDelete(t *testing.T) {
	srv := claudeAPIServer(t, "the async answer")
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL + "/"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	resp, err := c.CreateResponse(context.Background(), openai.CreateResponseRequest{
		Model:      "claude-test",
		Background: true,
		Input:      []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatalf("CreateResponse() error: %v", err)
	}
	if resp.Status != "in_progress" {
		t.Errorf("Status = %q, want in_progress", resp.Status)
	}

	got, err := c.GetResponse(context.Background(), resp.ID)
	if err != nil {
		t.Fatalf("GetResponse() error: %v", err)
	}
	if got.Status != "completed" {
		t.Errorf("final Status = %q, want completed", got.Status)
	}

	if err := c.DeleteResponse(context.Background(), resp.ID); err != nil {
		t.Fatalf("DeleteResponse() error: %v", err)
	}

	if _, err := c.GetResponse(context.Background(), resp.ID); err == nil {
		t.Fatal("expected error retrieving a deleted response")
	}
}

func TestCancelResponse(t *testing.T) {
	// A server that blocks until released, so we can cancel while in-flight.
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(claudeFinalTextResponse("too late"))
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	c, err := New(openai.Config{BaseURL: srv.URL + "/"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	resp, err := c.CreateResponse(context.Background(), openai.CreateResponseRequest{
		Model:      "claude-test",
		Background: true,
		Input:      []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatalf("CreateResponse() error: %v", err)
	}

	got, err := c.CancelResponse(context.Background(), resp.ID)
	if err != nil {
		t.Fatalf("CancelResponse() error: %v", err)
	}
	if got.Status != "cancelled" {
		t.Errorf("Status = %q, want cancelled", got.Status)
	}
}

func TestCompactResponse(t *testing.T) {
	srv := claudeAPIServer(t, "compact me")
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL + "/"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	resp, err := c.CreateResponse(context.Background(), openai.CreateResponseRequest{
		Model: "claude-test",
		Input: []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatalf("CreateResponse() error: %v", err)
	}

	got, err := c.CompactResponse(context.Background(), resp.ID)
	if err != nil {
		t.Fatalf("CompactResponse() error: %v", err)
	}
	if got.ID != resp.ID {
		t.Errorf("CompactResponse ID = %q, want %q", got.ID, resp.ID)
	}
}

func TestStreamResponse(t *testing.T) {
	sse := strings.Join([]string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[]}}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi there"}}`,
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}`,
		`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
	}, "\n\n") + "\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sse))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL + "/"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	stream := c.StreamResponse(context.Background(), openai.CreateResponseRequest{
		Model: "claude-test",
		Input: []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
	})

	var gotText string
	for stream.Next() {
		evt := stream.Current()
		gotText += evt.TextDelta()
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if gotText != "hi there" {
		t.Errorf("gotText = %q, want %q", gotText, "hi there")
	}
}

// --- error / retry / decompress paths ---

func TestDoRequest_GzipErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		gw.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"gzip boom"}}`))
		gw.Close()
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusBadRequest)
		w.Write(buf.Bytes())
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL + "/"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	_, err = c.ChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil || !strings.Contains(err.Error(), "gzip boom") {
		t.Fatalf("expected error containing 'gzip boom', got %v", err)
	}
}

func TestDoRequest_GzipSuccessBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		gw.Write(claudeFinalTextResponse("gzipped ok"))
		gw.Close()
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(buf.Bytes())
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL + "/"})
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
	if resp.Choices[0].Message.GetContentAsString() != "gzipped ok" {
		t.Errorf("unexpected content: %+v", resp.Choices[0].Message)
	}
}

func TestDoRequest_NonJSONErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("plain text upstream failure"))
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL + "/", RetryOnServerError: boolPtr(false)})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	_, err = c.ChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil || !strings.Contains(err.Error(), "status 502") || !strings.Contains(err.Error(), "plain text upstream failure") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDoRequest_UnmarshalableSuccessBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL + "/"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	_, err = c.ChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil || !strings.Contains(err.Error(), "failed to decode response") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"empty", "", 0},
		{"zero seconds", "0", 0},
		{"negative seconds", "-5", 0},
		{"garbage", "not-a-number-or-date", 0},
		{"valid seconds", "5", 5 * time.Second},
		{"valid seconds with whitespace", "  7  ", 7 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRetryAfter(tt.header)
			if got != tt.want {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}

	// HTTP-date in the future should yield a positive duration.
	future := time.Now().Add(10 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(future); got <= 0 {
		t.Errorf("parseRetryAfter(future date) = %v, want > 0", got)
	}

	// HTTP-date in the past should yield 0 (not negative).
	past := time.Now().Add(-10 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(past); got != 0 {
		t.Errorf("parseRetryAfter(past date) = %v, want 0", got)
	}
}

func TestStreamRequest_NonRetryableError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"bad stream request"}}`))
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL + "/"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	stream := c.StreamChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	for stream.Next() {
	}
	if err := stream.Err(); err == nil || !strings.Contains(err.Error(), "bad stream request") {
		t.Fatalf("expected non-retryable stream error, got %v", err)
	}
}

func boolPtr(b bool) *bool { return &b }
