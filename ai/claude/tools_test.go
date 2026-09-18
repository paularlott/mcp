package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/paularlott/mcp"
	"github.com/paularlott/mcp/ai/openai"
)

// newRemoteToolServer starts a real MCP HTTP server with a single tool
// registered, and returns a *mcp.Client (with the given namespace) wired to
// it plus a cleanup func.
func newRemoteToolServer(t *testing.T, toolName string, handler func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error), namespace string) (*mcp.Client, func()) {
	t.Helper()
	srv := mcp.NewServer("svc", "1")
	srv.RegisterTool(mcp.NewTool(toolName, "a tool"), handler)
	ts := httptest.NewServer(http.HandlerFunc(srv.HandleRequest))
	client := mcp.NewClient(ts.URL, nil, namespace)
	return client, ts.Close
}

func TestGetAllTools_LocalOnly(t *testing.T) {
	local := &openai.MCPServerFuncs{
		ListToolsFunc: func() []mcp.MCPTool {
			return []mcp.MCPTool{{Name: "local_tool", Description: "d"}}
		},
	}
	c := &Client{localServer: local}
	tools, err := c.getAllTools(context.Background())
	if err != nil {
		t.Fatalf("getAllTools error: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "local_tool" {
		t.Fatalf("unexpected tools: %+v", tools)
	}
}

func TestGetAllTools_LocalAndRemote(t *testing.T) {
	local := &openai.MCPServerFuncs{
		ListToolsFunc: func() []mcp.MCPTool {
			return []mcp.MCPTool{{Name: "local_tool", Description: "d"}}
		},
	}

	remoteClient, cleanup := newRemoteToolServer(t, "remote_tool", func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
		return mcp.NewToolResponseText("ok"), nil
	}, "remote")
	defer cleanup()

	c := &Client{localServer: local, remoteServers: []*mcp.Client{remoteClient}}

	tools, err := c.getAllTools(context.Background())
	if err != nil {
		t.Fatalf("getAllTools error: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d: %+v", len(tools), tools)
	}
	var sawLocal, sawRemote bool
	for _, tl := range tools {
		if tl.Name == "local_tool" {
			sawLocal = true
		}
		if strings.Contains(tl.Name, "remote_tool") {
			sawRemote = true
		}
	}
	if !sawLocal || !sawRemote {
		t.Fatalf("missing expected tools: %+v", tools)
	}
}

func TestGetAllTools_RemoteError(t *testing.T) {
	// A remote server that always fails tools/list.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpc mcp.MCPRequest
		_ = json.NewDecoder(r.Body).Decode(&rpc)
		if rpc.Method == "initialize" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(mcp.MCPResponse{JSONRPC: "2.0", ID: rpc.ID, Result: map[string]any{
				"protocolVersion": mcp.MCPProtocolVersionLatest,
			}})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mcp.MCPResponse{JSONRPC: "2.0", ID: rpc.ID, Error: &mcp.MCPError{Code: -32000, Message: "boom"}})
	}))
	defer ts.Close()

	remoteClient := mcp.NewClient(ts.URL, nil, "remote")
	c := &Client{remoteServers: []*mcp.Client{remoteClient}}

	_, err := c.getAllTools(context.Background())
	if err == nil {
		t.Fatal("expected error from failing remote tools/list")
	}
	if !strings.Contains(err.Error(), "failed to list tools from remote server") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCallTool_NoLocalServer(t *testing.T) {
	c := &Client{}
	_, err := c.callTool(context.Background(), "whatever", nil)
	if err == nil || !strings.Contains(err.Error(), "no local MCP server configured") {
		t.Fatalf("expected 'no local MCP server configured' error, got %v", err)
	}
}

func TestCallTool_LocalServer(t *testing.T) {
	var gotName string
	var gotArgs map[string]any
	local := &openai.MCPServerFuncs{
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			gotName = name
			gotArgs = args
			return mcp.NewToolResponseText("42"), nil
		},
	}
	c := &Client{localServer: local}
	resp, err := c.callTool(context.Background(), "get_answer", map[string]any{"q": "life"})
	if err != nil {
		t.Fatalf("callTool error: %v", err)
	}
	if gotName != "get_answer" || gotArgs["q"] != "life" {
		t.Errorf("unexpected call: name=%s args=%v", gotName, gotArgs)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "42" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestCallTool_RemoteServer(t *testing.T) {
	var called atomic.Bool
	remoteClient, cleanup := newRemoteToolServer(t, "remote_tool", func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
		called.Store(true)
		return mcp.NewToolResponseText("remote-ok"), nil
	}, "remote")
	defer cleanup()

	// Also configure a local server that should NOT be called since the
	// namespace prefix matches the remote client.
	local := &openai.MCPServerFuncs{
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			t.Fatalf("local server should not be called for namespaced remote tool %q", name)
			return nil, nil
		},
	}

	c := &Client{localServer: local, remoteServers: []*mcp.Client{remoteClient}}

	// The namespaced name as returned by ListTools would be "remote__remote_tool".
	resp, err := c.callTool(context.Background(), "remote__remote_tool", map[string]any{})
	if err != nil {
		t.Fatalf("callTool error: %v", err)
	}
	if !called.Load() {
		t.Fatal("expected remote tool handler to be invoked")
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "remote-ok" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

// claudeToolUseResponse builds a Claude API response body requesting a tool call.
func claudeToolUseResponse(toolID, toolName string, input map[string]any) []byte {
	resp := ClaudeResponse{
		ID:         "msg_tool",
		Type:       "message",
		Role:       "assistant",
		Model:      "claude-test",
		StopReason: "tool_use",
		Content: []ContentBlock{
			{Type: "text", Text: "Let me check."},
			{Type: "tool_use", ID: toolID, Name: toolName, Input: input},
		},
	}
	data, _ := json.Marshal(resp)
	return data
}

// claudeFinalTextResponse builds a Claude API response body with plain text and end_turn.
func claudeFinalTextResponse(text string) []byte {
	resp := ClaudeResponse{
		ID:         "msg_final",
		Type:       "message",
		Role:       "assistant",
		Model:      "claude-test",
		StopReason: "end_turn",
		Content:    []ContentBlock{{Type: "text", Text: text}},
	}
	data, _ := json.Marshal(resp)
	return data
}

// recordingToolHandler captures OnToolCall/OnToolResult invocations.
type recordingToolHandler struct {
	calls     []string
	results   []string
	callErr   error
	resultErr error
}

func (h *recordingToolHandler) OnToolCall(tc openai.ToolCall) error {
	h.calls = append(h.calls, tc.Function.Name)
	return h.callErr
}

func (h *recordingToolHandler) OnToolResult(toolCallID, toolName, result string) error {
	h.results = append(h.results, toolName+":"+result)
	return h.resultErr
}

func TestChatCompletion_ToolRoundTrip(t *testing.T) {
	var requestCount atomic.Int32
	var sawToolResultInSecondRequest bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requestCount.Add(1)

		var req MessagesRequest
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &req)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if n == 1 {
			w.Write(claudeToolUseResponse("call_1", "get_weather", map[string]any{"loc": "London"}))
			return
		}
		// Second request should carry the tool_result message.
		for _, m := range req.Messages {
			for _, b := range m.Content.Blocks() {
				if b.Type == "tool_result" && b.ToolUseID == "call_1" {
					sawToolResultInSecondRequest = true
				}
			}
		}
		w.Write(claudeFinalTextResponse("It is sunny in London."))
	}))
	defer srv.Close()

	local := &openai.MCPServerFuncs{
		ListToolsFunc: func() []mcp.MCPTool {
			return []mcp.MCPTool{{Name: "get_weather", Description: "weather tool"}}
		},
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			if name != "get_weather" {
				return nil, fmt.Errorf("unexpected tool %q", name)
			}
			return mcp.NewToolResponseText("sunny"), nil
		},
	}

	c, err := New(openai.Config{BaseURL: srv.URL + "/", LocalServer: local})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	handler := &recordingToolHandler{}
	ctx := openai.WithToolHandler(context.Background(), handler)

	resp, err := c.ChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []openai.Message{{Role: "user", Content: "What's the weather?"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error: %v", err)
	}
	if requestCount.Load() != 2 {
		t.Errorf("expected 2 requests (tool round trip), got %d", requestCount.Load())
	}
	if !sawToolResultInSecondRequest {
		t.Error("expected the second request to carry the tool_result content block")
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.GetContentAsString() != "It is sunny in London." {
		t.Errorf("unexpected final response: %+v", resp)
	}
	if len(handler.calls) != 1 || handler.calls[0] != "get_weather" {
		t.Errorf("expected OnToolCall to record get_weather, got %v", handler.calls)
	}
	if len(handler.results) != 1 || !strings.Contains(handler.results[0], "sunny") {
		t.Errorf("expected OnToolResult to record sunny result, got %v", handler.results)
	}
}

func TestChatCompletion_ToolHandlerOnToolCallError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(claudeToolUseResponse("call_1", "get_weather", map[string]any{}))
	}))
	defer srv.Close()

	local := &openai.MCPServerFuncs{
		ListToolsFunc: func() []mcp.MCPTool {
			return []mcp.MCPTool{{Name: "get_weather", Description: "weather tool"}}
		},
	}

	c, err := New(openai.Config{BaseURL: srv.URL + "/", LocalServer: local})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	handler := &recordingToolHandler{callErr: fmt.Errorf("denied")}
	ctx := openai.WithToolHandler(context.Background(), handler)

	_, err = c.ChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil || !strings.Contains(err.Error(), "tool handler error") {
		t.Fatalf("expected tool handler error, got %v", err)
	}
}

func TestChatCompletion_MaxToolIterations(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(claudeToolUseResponse("call_x", "loop_tool", map[string]any{}))
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

	_, err = c.ChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected max-iterations error")
	}
	if !strings.Contains(err.Error(), "iteration") && !strings.Contains(strings.ToLower(err.Error()), "max") {
		t.Errorf("expected a max-iterations style error, got: %v", err)
	}
}

func TestChatCompletion_ToolCallExecutionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			return nil, fmt.Errorf("tool backend down")
		},
	}

	c, err := New(openai.Config{BaseURL: srv.URL + "/", LocalServer: local})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	_, err = c.ChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error propagated from failing tool execution")
	}
}

func TestStreamChatCompletion_ToolRoundTrip(t *testing.T) {
	var requestCount atomic.Int32

	toolUseSSE := strings.Join([]string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[]}}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"get_weather"}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}`,
		`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
	}, "\n\n") + "\n\n"

	finalSSE := strings.Join([]string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","model":"claude-test","content":[]}}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"sunny!"}}`,
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}`,
		`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
	}, "\n\n") + "\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requestCount.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		if n == 1 {
			w.Write([]byte(toolUseSSE))
		} else {
			w.Write([]byte(finalSSE))
		}
		if flusher != nil {
			flusher.Flush()
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

	handler := &recordingToolHandler{}
	ctx := openai.WithToolHandler(context.Background(), handler)

	stream := c.StreamChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    "claude-test",
		Messages: []openai.Message{{Role: "user", Content: "What's the weather?"}},
	})

	var gotText string
	for stream.Next() {
		chunk := stream.Current()
		if len(chunk.Choices) > 0 {
			gotText += chunk.Choices[0].Delta.Content
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if requestCount.Load() != 2 {
		t.Errorf("expected 2 requests (tool round trip), got %d", requestCount.Load())
	}
	if gotText != "sunny!" {
		t.Errorf("gotText = %q, want %q", gotText, "sunny!")
	}
	if len(handler.calls) != 1 || handler.calls[0] != "get_weather" {
		t.Errorf("expected OnToolCall recorded, got %v", handler.calls)
	}
}

func TestStreamChatCompletion_ToolHandlerOnToolCallError(t *testing.T) {
	toolUseSSE := strings.Join([]string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[]}}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"get_weather"}}`,
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}`,
		`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}`,
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
	}, "\n\n") + "\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(toolUseSSE))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}))
	defer srv.Close()

	local := &openai.MCPServerFuncs{
		ListToolsFunc: func() []mcp.MCPTool {
			return []mcp.MCPTool{{Name: "get_weather", Description: "weather tool"}}
		},
	}

	c, err := New(openai.Config{BaseURL: srv.URL + "/", LocalServer: local})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	handler := &recordingToolHandler{callErr: fmt.Errorf("denied")}
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
