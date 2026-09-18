package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/paularlott/mcp"
	"github.com/paularlott/mcp/ai/openai"
	"github.com/paularlott/mcp/pool"
)

// --- New() remote server construction ---

func TestNew_RemoteServerConfigs(t *testing.T) {
	c, err := New(openai.Config{
		BaseURL: "http://localhost/",
		RemoteServerConfigs: []openai.RemoteServerConfig{
			{BaseURL: "http://remote-a", Namespace: "a"},
			{BaseURL: "http://remote-b", Namespace: "b", HTTPPool: pool.GetPool()},
		},
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if len(c.remoteServers) != 2 {
		t.Fatalf("expected 2 remote servers, got %d", len(c.remoteServers))
	}
}

func TestNew_RetryOnRateLimitDisabled(t *testing.T) {
	c, err := New(openai.Config{
		BaseURL:            "http://localhost/",
		RetryOnRateLimit:   boolPtr(false),
		RetryOnServerError: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if c.retryOnRateLimit {
		t.Error("retryOnRateLimit = true, want false")
	}
	if c.retryOnServerError {
		t.Error("retryOnServerError = true, want false")
	}
}

// --- ChatCompletion / StreamChatCompletion apply client-level defaults ---

func TestChatCompletion_AppliesClientDefaults(t *testing.T) {
	var gotMaxTokens int
	var gotTemp, gotTopP *float64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req MessagesRequest
		body, _ := decodeBody(r)
		_ = json.Unmarshal(body, &req)
		gotMaxTokens = req.MaxTokens
		gotTemp = req.Temperature
		gotTopP = req.TopP

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(claudeFinalTextResponse("ok"))
	}))
	defer srv.Close()

	temp := 0.3
	topP := 0.9
	c, err := New(openai.Config{
		BaseURL:     srv.URL + "/",
		MaxTokens:   256,
		Temperature: &temp,
		TopP:        &topP,
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
	if gotMaxTokens != 256 {
		t.Errorf("MaxTokens = %d, want 256", gotMaxTokens)
	}
	if gotTemp == nil || *gotTemp != 0.3 {
		t.Errorf("Temperature = %v, want 0.3", gotTemp)
	}
	if gotTopP == nil || *gotTopP != 0.9 {
		t.Errorf("TopP = %v, want 0.9", gotTopP)
	}
}

func TestStreamChatCompletion_AppliesClientDefaults(t *testing.T) {
	var gotMaxTokens int
	var gotTemp, gotTopP *float64

	sse := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"model\":\"claude-test\",\"content\":[]}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req MessagesRequest
		body, _ := decodeBody(r)
		_ = json.Unmarshal(body, &req)
		gotMaxTokens = req.MaxTokens
		gotTemp = req.Temperature
		gotTopP = req.TopP

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(sse))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	temp := 0.1
	topP := 0.7
	c, err := New(openai.Config{BaseURL: srv.URL + "/", MaxTokens: 128, Temperature: &temp, TopP: &topP})
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
	if gotMaxTokens != 128 {
		t.Errorf("MaxTokens = %d, want 128", gotMaxTokens)
	}
	if gotTemp == nil || *gotTemp != 0.1 {
		t.Errorf("Temperature = %v, want 0.1", gotTemp)
	}
	if gotTopP == nil || *gotTopP != 0.7 {
		t.Errorf("TopP = %v, want 0.7", gotTopP)
	}
}

func TestStreamChatCompletion_ToolCallExecutionError(t *testing.T) {
	toolUseSSE := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"model\":\"claude-test\",\"content\":[]}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_1\",\"name\":\"get_weather\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(toolUseSSE))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	local := &openai.MCPServerFuncs{
		ListToolsFunc: func() []mcp.MCPTool {
			return []mcp.MCPTool{{Name: "get_weather", Description: "weather tool"}}
		},
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			return nil, fmt.Errorf("tool backend down")
		},
	}

	c, err := New(openai.Config{BaseURL: srv.URL + "/", LocalServer: local})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	stream := c.StreamChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	for stream.Next() {
	}
	// The executor's error gets folded into the tool result text (ExecuteToolCalls
	// runs with stopOnError=false), so the loop keeps going and eventually hits
	// the max-tool-iterations guard rather than surfacing the raw tool error.
	if err := stream.Err(); err == nil {
		t.Fatal("expected an eventual error (max tool iterations) after tool execution kept failing")
	}
}

// --- OnToolResult handler error paths ---

func TestChatCompletion_ToolHandlerOnToolResultError(t *testing.T) {
	var requestCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(claudeToolUseResponse("call_1", "get_weather", map[string]any{}))
	}))
	defer srv.Close()

	local := &openai.MCPServerFuncs{
		ListToolsFunc: func() []mcp.MCPTool {
			return []mcp.MCPTool{{Name: "get_weather", Description: "weather tool"}}
		},
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("sunny"), nil
		},
	}

	c, err := New(openai.Config{BaseURL: srv.URL + "/", LocalServer: local})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	handler := &recordingToolHandler{resultErr: fmt.Errorf("result rejected")}
	ctx := openai.WithToolHandler(context.Background(), handler)

	_, err = c.ChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil || !strings.Contains(err.Error(), "tool handler error") {
		t.Fatalf("expected tool handler error, got %v", err)
	}
	if requestCount != 1 {
		t.Errorf("expected exactly 1 request before failing on OnToolResult, got %d", requestCount)
	}
}

func TestStreamChatCompletion_ToolHandlerOnToolResultError(t *testing.T) {
	toolUseSSE := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"model\":\"claude-test\",\"content\":[]}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_1\",\"name\":\"get_weather\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(toolUseSSE))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	local := &openai.MCPServerFuncs{
		ListToolsFunc: func() []mcp.MCPTool {
			return []mcp.MCPTool{{Name: "get_weather", Description: "weather tool"}}
		},
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("sunny"), nil
		},
	}

	c, err := New(openai.Config{BaseURL: srv.URL + "/", LocalServer: local})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	handler := &recordingToolHandler{resultErr: fmt.Errorf("result rejected")}
	ctx := openai.WithToolHandler(context.Background(), handler)

	stream := c.StreamChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	for stream.Next() {
	}
	if err := stream.Err(); err == nil || !strings.Contains(err.Error(), "tool handler error") {
		t.Fatalf("expected tool handler error, got %v", err)
	}
}

func TestStreamChatCompletion_MaxToolIterations(t *testing.T) {
	toolUseSSE := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"model\":\"claude-test\",\"content\":[]}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_x\",\"name\":\"loop_tool\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(toolUseSSE))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	local := &openai.MCPServerFuncs{
		ListToolsFunc: func() []mcp.MCPTool {
			return []mcp.MCPTool{{Name: "loop_tool", Description: "always requested"}}
		},
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("again"), nil
		},
	}

	c, err := New(openai.Config{BaseURL: srv.URL + "/", LocalServer: local})
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
		t.Fatal("expected max-iterations error on stream")
	}
}

// --- pure-function edge cases ---

func TestConvertToOpenAIResponse_MaxTokensStopReason(t *testing.T) {
	client := &Client{}
	resp := &ClaudeResponse{ID: "id1", Model: "m", StopReason: "max_tokens", Content: []ContentBlock{{Type: "text", Text: "cut off"}}}
	got := client.convertToOpenAIResponse(resp)
	if got.Choices[0].FinishReason != "length" {
		t.Errorf("FinishReason = %q, want %q", got.Choices[0].FinishReason, "length")
	}
}

func TestConvertStreamEventToOpenAI_NilEvent(t *testing.T) {
	client := &Client{}
	if got := client.convertStreamEventToOpenAI(nil); got != nil {
		t.Errorf("expected nil for nil event, got %+v", got)
	}
}

func TestConvertStreamEventToOpenAI_MessageDeltaMaxTokens(t *testing.T) {
	client := &Client{}
	event := &ClaudeStreamEvent{Type: "message_delta", Delta: &ClaudeDelta{StopReason: "max_tokens"}}
	got := client.convertStreamEventToOpenAI(event)
	if got == nil || got.Choices[0].FinishReason != "length" {
		t.Errorf("unexpected result: %+v", got)
	}
}

// decodeBody reads and returns the full request body.
func decodeBody(r *http.Request) ([]byte, error) {
	return io.ReadAll(r.Body)
}

// --- setHeaders ---

func TestSetHeaders_ExtraHeaders(t *testing.T) {
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(claudeFinalTextResponse("ok"))
	}))
	defer srv.Close()

	extra := http.Header{}
	extra.Add("X-Custom", "one")
	extra.Add("X-Custom", "two")

	c, err := New(openai.Config{BaseURL: srv.URL + "/", ExtraHeaders: extra})
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
	got := gotHeaders.Values("X-Custom")
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Errorf("X-Custom header = %v, want [one two]", got)
	}
}

// --- processSSEStream (direct unit tests) ---

type errReader struct{ err error }

func (r errReader) Read(p []byte) (int, error) { return 0, r.err }

func TestProcessSSEStream_EventFuncError(t *testing.T) {
	c := &Client{}
	body := "data: {\"type\":\"content_block_delta\"}\n\n"
	err := c.processSSEStream(strings.NewReader(body), func(e *ClaudeStreamEvent) (bool, error) {
		return false, fmt.Errorf("event handling failed")
	})
	if err == nil || !strings.Contains(err.Error(), "event handling failed") {
		t.Fatalf("expected event handling error, got %v", err)
	}
}

func TestProcessSSEStream_ScannerError(t *testing.T) {
	c := &Client{}
	wantErr := fmt.Errorf("read exploded")
	err := c.processSSEStream(errReader{err: wantErr}, func(e *ClaudeStreamEvent) (bool, error) {
		return false, nil
	})
	if err == nil || !strings.Contains(err.Error(), "read exploded") {
		t.Fatalf("expected scanner error to propagate, got %v", err)
	}
}

func TestProcessSSEStream_MalformedEventSkipped(t *testing.T) {
	c := &Client{}
	var gotEvents int
	body := "data: not valid json\n\n" + "data: {\"type\":\"message_stop\"}\n\n"
	err := c.processSSEStream(strings.NewReader(body), func(e *ClaudeStreamEvent) (bool, error) {
		gotEvents++
		return e.Type == "message_stop", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotEvents != 1 {
		t.Errorf("expected the malformed event to be skipped and only the valid one delivered, got %d events", gotEvents)
	}
}
