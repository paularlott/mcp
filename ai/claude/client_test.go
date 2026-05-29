package claude

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/paularlott/mcp/ai/openai"
)

func TestConvertToClaudeRequest(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name     string
		req      openai.ChatCompletionRequest
		expected ClaudeRequest
	}{
		{
			name: "Basic Request with System Prompt",
			req: openai.ChatCompletionRequest{
				Model:       "claude-test",
				MaxTokens:   1000,
				Temperature: floatPtr(0.7),
				Messages: []openai.Message{
					{Role: "system", Content: "You are a helpful assistant."},
					{Role: "user", Content: "Hello!"},
				},
			},
			expected: ClaudeRequest{
				Model:       "claude-test",
				MaxTokens:   1000,
				Temperature: floatPtr(0.7),
				System:      SystemField{text: "You are a helpful assistant."},
				Messages: []ClaudeMessage{
					{Role: "user", Content: MessageContent{blocks: []ContentBlock{{Type: "text", Text: "Hello!"}}}},
				},
			},
		},
		{
			name: "Tools and Tool Calls",
			req: openai.ChatCompletionRequest{
				Model: "claude-test",
				Tools: []openai.Tool{
					{
						Type: "function",
						Function: openai.ToolFunction{
							Name:        "get_weather",
							Description: "Get weather",
							Parameters:  map[string]any{"type": "object", "properties": map[string]any{"loc": map[string]any{"type": "string"}}},
						},
					},
				},
				Messages: []openai.Message{
					{Role: "user", Content: "weather?"},
					{
						Role:    "assistant",
						Content: "Let me check.",
						ToolCalls: []openai.ToolCall{
							{
								ID:   "call_123",
								Type: "function",
								Function: openai.ToolCallFunction{
									Name:      "get_weather",
									Arguments: map[string]any{"loc": "London"},
								},
							},
						},
					},
					{
						Role:       "tool",
						Content:    "Sunny",
						ToolCallID: "call_123",
					},
				},
			},
			expected: ClaudeRequest{
				Model: "claude-test",
				Tools: []ClaudeTool{
					{
						Name:        "get_weather",
						Description: "Get weather",
						InputSchema: map[string]any{"type": "object", "properties": map[string]any{"loc": map[string]any{"type": "string"}}},
					},
				},
				Messages: []ClaudeMessage{
					{Role: "user", Content: MessageContent{blocks: []ContentBlock{{Type: "text", Text: "weather?"}}}},
					{
						Role: "assistant",
						Content: MessageContent{
							blocks: []ContentBlock{
								{Type: "text", Text: "Let me check."},
								{Type: "tool_use", ID: "call_123", Name: "get_weather", Input: map[string]any{"loc": "London"}},
							},
						},
					},
					{
						Role: "user",
						Content: MessageContent{
							blocks: []ContentBlock{
								{Type: "tool_result", ToolUseID: "call_123", Content: "Sunny"},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := client.convertToClaudeRequest(tt.req)
			if !reflect.DeepEqual(got.Model, tt.expected.Model) {
				t.Errorf("Model = %v, want %v", got.Model, tt.expected.Model)
			}
			if got.System.text != tt.expected.System.text {
				t.Errorf("System = %v, want %v", got.System.text, tt.expected.System.text)
			}

			bGot, _ := json.Marshal(got.Messages)
			bExp, _ := json.Marshal(tt.expected.Messages)
			if string(bGot) != string(bExp) {
				t.Errorf("Messages = %v\nwant %v", string(bGot), string(bExp))
			}

			bGotT, _ := json.Marshal(got.Tools)
			bExpT, _ := json.Marshal(tt.expected.Tools)
			if string(bGotT) != string(bExpT) {
				t.Errorf("Tools = %v\nwant %v", string(bGotT), string(bExpT))
			}
		})
	}
}

func TestConvertToOpenAIResponse(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name     string
		resp     ClaudeResponse
		expected openai.ChatCompletionResponse
	}{
		{
			name: "Basic Response",
			resp: ClaudeResponse{
				ID:    "msg_123",
				Model: "claude-test",
				Role:  "assistant",
				Content: []ContentBlock{
					{Type: "text", Text: "Hello there!"},
				},
				StopReason: "end_turn",
				Usage: ClaudeUsage{
					InputTokens:  10,
					OutputTokens: 20,
				},
			},
			expected: openai.ChatCompletionResponse{
				ID:      "msg_123",
				Object:  "chat.completion",
				Model:   "claude-test",
				Choices: []openai.Choice{
					{
						Index: 0,
						Message: openai.Message{
							Role:    "assistant",
							Content: "Hello there!",
						},
						FinishReason: "stop",
					},
				},
				Usage: &openai.Usage{
					PromptTokens:     10,
					CompletionTokens: 20,
					TotalTokens:      30,
				},
			},
		},
		{
			name: "Tool Use Response",
			resp: ClaudeResponse{
				ID:    "msg_123",
				Model: "claude-test",
				Role:  "assistant",
				Content: []ContentBlock{
					{Type: "text", Text: "I will check."},
					{
						Type:  "tool_use",
						ID:    "call_123",
						Name:  "get_weather",
						Input: map[string]any{"loc": "London"},
					},
				},
				StopReason: "tool_use",
				Usage: ClaudeUsage{
					InputTokens:  15,
					OutputTokens: 25,
				},
			},
			expected: openai.ChatCompletionResponse{
				ID:      "msg_123",
				Object:  "chat.completion",
				Model:   "claude-test",
				Choices: []openai.Choice{
					{
						Index: 0,
						Message: openai.Message{
							Role:    "assistant",
							Content: "I will check.",
							ToolCalls: []openai.ToolCall{
								{
									ID:   "call_123",
									Type: "function",
									Function: openai.ToolCallFunction{
										Name:      "get_weather",
										Arguments: map[string]any{"loc": "London"},
									},
								},
							},
						},
						FinishReason: "tool_calls",
					},
				},
				Usage: &openai.Usage{
					PromptTokens:     15,
					CompletionTokens: 25,
					TotalTokens:      40,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := client.convertToOpenAIResponse(&tt.resp)

			bGot, _ := json.Marshal(got)
			bExp, _ := json.Marshal(tt.expected)
			if string(bGot) != string(bExp) {
				t.Errorf("Response = %v\nwant %v", string(bGot), string(bExp))
			}
		})
	}
}

func floatPtr(f float64) *float64 { return &f }

func TestConvertToClaudeRequest_EdgeCases(t *testing.T) {
	client := &Client{}
	tests := []struct {
		name     string
		req      openai.ChatCompletionRequest
		expected ClaudeRequest
	}{
		{
			name: "Multiple System Prompts (merged or overridden?)",
			// Let's see what happens if multiple system prompts are passed. Note that the code loops and takes the last one! Wait, no.
// `system` is a SystemField. For each system message, `claudeReq.System = SystemField{text: msg.GetContentAsString()}`
// So it overwrites it!
req: openai.ChatCompletionRequest{
Model: "test",
Messages: []openai.Message{
{Role: "system", Content: "Prompt 1"},
{Role: "system", Content: "Prompt 2"},
{Role: "user", Content: "Hi"},
},
},
expected: ClaudeRequest{
Model: "test",
System: SystemField{text: "Prompt 2"},
Messages: []ClaudeMessage{
{Role: "user", Content: MessageContent{blocks: []ContentBlock{{Type: "text", Text: "Hi"}}}},
},
},
},
{
name: "Mixed Content Array Stringification",
req: openai.ChatCompletionRequest{
Model: "test",
Messages: []openai.Message{
{
Role: "user",
Content: []any{
map[string]any{"type": "text", "text": "Hello "},
map[string]any{"type": "text", "text": "World"},
},
},
},
},
expected: ClaudeRequest{
Model: "test",
Messages: []ClaudeMessage{
{Role: "user", Content: MessageContent{blocks: []ContentBlock{{Type: "text", Text: "Hello World"}}}},
},
},
},
}

for _, tt := range tests {
t.Run(tt.name, func(t *testing.T) {
got := client.convertToClaudeRequest(tt.req)
if got.System.text != tt.expected.System.text {
t.Errorf("System = %v, want %v", got.System.text, tt.expected.System.text)
}
bGot, _ := json.Marshal(got.Messages)
bExp, _ := json.Marshal(tt.expected.Messages)
if string(bGot) != string(bExp) {
t.Errorf("Messages = %v\nwant %v", string(bGot), string(bExp))
}
})
}
}

func TestConvertToOpenAIResponse_EdgeCases(t *testing.T) {
client := &Client{}
tests := []struct {
name     string
resp     ClaudeResponse
expected openai.ChatCompletionResponse
}{
{
name: "Multiple Tool Use Blocks",
resp: ClaudeResponse{
ID: "123", Model: "test", Role: "assistant", StopReason: "tool_use",
Content: []ContentBlock{
{Type: "tool_use", ID: "call_1", Name: "tool_a", Input: map[string]any{"a": 1}},
{Type: "tool_use", ID: "call_2", Name: "tool_b", Input: map[string]any{"b": 2}},
},
},
expected: openai.ChatCompletionResponse{
ID: "123", Object: "chat.completion", Model: "test",
Choices: []openai.Choice{
{
Index: 0,
Message: openai.Message{
Role: "assistant",
Content: "",
ToolCalls: []openai.ToolCall{
{ID: "call_1", Type: "function", Function: openai.ToolCallFunction{Name: "tool_a", Arguments: map[string]any{"a": 1}}},
{ID: "call_2", Type: "function", Function: openai.ToolCallFunction{Name: "tool_b", Arguments: map[string]any{"b": 2}}},
},
},
FinishReason: "tool_calls",
},
},
Usage: &openai.Usage{},
},
},
}
for _, tt := range tests {
t.Run(tt.name, func(t *testing.T) {
got := client.convertToOpenAIResponse(&tt.resp)
bGot, _ := json.Marshal(got)
bExp, _ := json.Marshal(tt.expected)
if string(bGot) != string(bExp) {
t.Errorf("Response = %v\nwant %v", string(bGot), string(bExp))
}
})
}
}
// claudeOKResponse returns a minimal valid Claude messages response body.
func claudeOKResponse() []byte {
	resp := ClaudeResponse{
		ID:         "msg_ok",
		Type:       "message",
		Role:       "assistant",
		Model:      "claude-test",
		StopReason: "end_turn",
		Content:    []ContentBlock{{Type: "text", Text: "hello"}},
	}
	data, _ := json.Marshal(resp)
	return data
}

func TestClaudeConfigDefaults(t *testing.T) {
	c, err := New(openai.Config{BaseURL: "http://localhost"})
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

func TestClaudeConfigValidation(t *testing.T) {
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
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %v, want containing %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestClaudeRetryOn429(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(claudeOKResponse())
	}))
	defer srv.Close()

	c, err := New(openai.Config{
		BaseURL:      srv.URL + "/",
		MaxRetries:   3,
		RetryBackoff: 10 * time.Millisecond,
	})
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
	_ = resp

	if int(attempts.Load()) != 3 {
		t.Errorf("attempts = %d, want 3", attempts.Load())
	}
}

func TestClaudeRetryOn5xx(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"type":"error","error":{"type":"server_error","message":"boom"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(claudeOKResponse())
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

	_, err = c.ChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error: %v", err)
	}
	if int(attempts.Load()) != 2 {
		t.Errorf("attempts = %d, want 2", attempts.Load())
	}
}

func TestClaudeRetryDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`))
	}))
	defer srv.Close()

	c, err := New(openai.Config{
		BaseURL:    srv.URL + "/",
		MaxRetries: -1,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	_, err = c.ChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error from 429, got nil")
	}
}

func TestClaudeRetryExhausted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`))
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

	_, err = c.ChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
}

func TestClaudeRetryNonRetryable(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`))
	}))
	defer srv.Close()

	c, err := New(openai.Config{
		BaseURL:      srv.URL + "/",
		MaxRetries:   3,
		RetryBackoff: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	_, err = c.ChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error from 400")
	}
	// Should only try once — 400 is not retryable
	if int(attempts.Load()) != 1 {
		t.Errorf("attempts = %d, want 1 (no retry on 400)", attempts.Load())
	}
}

func TestClaudeRetryRespectsContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`))
	}))
	defer srv.Close()

	c, err := New(openai.Config{
		BaseURL:      srv.URL + "/",
		MaxRetries:   10,
		RetryBackoff: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = c.ChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %v, should have cancelled quickly", elapsed)
	}
}

func TestClaudeStreamRetryOn429(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}
		// Send a minimal Claude stream event sequence
		events := []string{
			`event: message_start\ndata: {"type":"message_start","message":{"id":"msg_ok","type":"message","role":"assistant","model":"claude-test","content":[],"stop_reason":null}}`,
			`event: content_block_start\ndata: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`event: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
			`event: content_block_stop\ndata: {"type":"content_block_stop","index":0}`,
			`event: message_delta\ndata: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
			`event: message_stop\ndata: {"type":"message_stop"}`,
		}
		for _, e := range events {
			w.Write([]byte(strings.ReplaceAll(e, `\n`, "\n") + "\n\n"))
			flusher.Flush()
		}
	}))
	defer srv.Close()

	c, err := New(openai.Config{
		BaseURL:      srv.URL + "/",
		MaxRetries:   3,
		RetryBackoff: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	stream := c.StreamChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})

	for stream.Next() {
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

func TestClaudeBackoffForAttempt(t *testing.T) {
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
		{31, 30 * time.Second},
	}
	for _, tt := range tests {
		got := c.backoffForAttempt(tt.attempt)
		if got != tt.want {
			t.Errorf("backoffForAttempt(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}

func TestClaudeShouldRetry(t *testing.T) {
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
		{"401 never", true, true, 401, false},
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

// TestClaudeRetryHonorsRetryAfterHeader verifies that a Retry-After: 0 header does not
// break retry logic (functional correctness, not timing).
func TestClaudeRetryHonorsRetryAfterHeader(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(claudeOKResponse())
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL + "/", MaxRetries: 2, RetryBackoff: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	_, err = c.ChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error: %v", err)
	}
	if attempts.Load() != 2 {
		t.Errorf("attempts = %d, want 2", attempts.Load())
	}
}

// TestClaudeRetryAfterUsedAsFloor verifies that Retry-After: 1 is used as a floor.
// Skipped in short mode.
func TestClaudeRetryAfterUsedAsFloor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing test in short mode")
	}

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 2 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(claudeOKResponse())
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL + "/", MaxRetries: 2, RetryBackoff: 1 * time.Millisecond})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	start := time.Now()
	_, err = c.ChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("ChatCompletion() error: %v", err)
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("elapsed = %v, want >= 900ms (Retry-After: 1 should be used as floor)", elapsed)
	}
}