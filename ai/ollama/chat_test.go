package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/paularlott/mcp"
	"github.com/paularlott/mcp/ai/openai"
)

// -----------------------------------------------------------------------------
// ChatCompletion: basic request/response shape
// -----------------------------------------------------------------------------

func TestChatCompletionSuccess(t *testing.T) {
	var gotBody chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %q, want /api/chat", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(chatResponse{
			Model:           "llama3",
			Message:         message{Role: "assistant", Content: "hello there"},
			Done:            true,
			DoneReason:      "stop",
			PromptEvalCount: 5,
			EvalCount:       3,
		})
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	resp, err := c.ChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "llama3",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error: %v", err)
	}
	if gotBody.Model != "llama3" || gotBody.Stream {
		t.Errorf("request body = %+v", gotBody)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.GetContentAsString() != "hello there" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	// InjectUsageIfMissing may raise the reported counts to its own estimate
	// when that estimate is higher (chat-template + per-message overhead), so
	// just assert usage is present and internally consistent rather than
	// pinning the server's raw counts.
	if resp.Usage == nil || resp.Usage.TotalTokens == 0 {
		t.Fatalf("usage = %+v, want non-zero", resp.Usage)
	}
	if resp.Usage.TotalTokens != resp.Usage.PromptTokens+resp.Usage.CompletionTokens {
		t.Errorf("usage = %+v, PromptTokens+CompletionTokens should equal TotalTokens", resp.Usage)
	}
}

func TestChatCompletionAppliesDefaults(t *testing.T) {
	var gotBody chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(chatResponse{Model: "llama3", Message: message{Role: "assistant", Content: "ok"}, Done: true})
	}))
	defer srv.Close()

	temp := 0.3
	c, err := New(openai.Config{BaseURL: srv.URL, MaxTokens: 42, Temperature: &temp})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	_, err = c.ChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "llama3",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error: %v", err)
	}
	if gotBody.Options["num_predict"] != float64(42) {
		t.Errorf("num_predict = %v, want 42 (default max tokens applied)", gotBody.Options["num_predict"])
	}
	if gotBody.Options["temperature"] != 0.3 {
		t.Errorf("temperature = %v, want 0.3 (default applied)", gotBody.Options["temperature"])
	}
}

func TestChatCompletionDoRequestError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`bad model`))
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL, MaxRetries: -1})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	_, err = c.ChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "llama3",
		Messages: []openai.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error from 400 response")
	}
}

// -----------------------------------------------------------------------------
// ChatCompletion: MCP tool-call loop
// -----------------------------------------------------------------------------

// toolCallThenTextServer returns a handler that replies with a tool call on
// its first request, and a final text answer on the second — driving the
// ChatCompletion tool loop exactly once.
func toolCallThenTextServer(t *testing.T, toolName string, args map[string]any) (*httptest.Server, *atomic.Int32) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			json.NewEncoder(w).Encode(chatResponse{
				Model: "llama3",
				Message: message{
					Role: "assistant",
					ToolCalls: []toolCall{{Function: toolCallFunction{Name: toolName, Arguments: args}}},
				},
				Done:       true,
				DoneReason: "tool_calls",
			})
			return
		}
		json.NewEncoder(w).Encode(chatResponse{
			Model:      "llama3",
			Message:    message{Role: "assistant", Content: "final answer"},
			Done:       true,
			DoneReason: "stop",
		})
	}))
	return srv, &calls
}

func TestChatCompletionRunsToolLoop(t *testing.T) {
	srv, calls := toolCallThenTextServer(t, "get_weather", map[string]any{"city": "SF"})
	defer srv.Close()

	var toolCalled atomic.Int32
	localServer := &openai.MCPServerFuncs{
		ListToolsFunc: func() []mcp.MCPTool {
			return []mcp.MCPTool{{Name: "get_weather", Description: "weather", InputSchema: map[string]any{"type": "object"}}}
		},
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			toolCalled.Add(1)
			if name != "get_weather" || args["city"] != "SF" {
				t.Errorf("unexpected call: %s %v", name, args)
			}
			return mcp.NewToolResponseText("sunny"), nil
		},
	}

	c, err := New(openai.Config{BaseURL: srv.URL, LocalServer: localServer})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	var handlerCalls, resultCalls atomic.Int32
	handler := openai.MCPServerFuncs{} // unused, placeholder to keep imports tidy
	_ = handler

	resp, err := c.ChatCompletion(openai.WithToolHandler(context.Background(), toolHandlerFuncs{
		onCall:   func(tc openai.ToolCall) error { handlerCalls.Add(1); return nil },
		onResult: func(id, name, result string) error { resultCalls.Add(1); return nil },
	}), openai.ChatCompletionRequest{
		Model:    "llama3",
		Messages: []openai.Message{{Role: "user", Content: "weather?"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error: %v", err)
	}
	if resp.Choices[0].Message.GetContentAsString() != "final answer" {
		t.Fatalf("final content = %q", resp.Choices[0].Message.GetContentAsString())
	}
	if calls.Load() != 2 {
		t.Errorf("server calls = %d, want 2", calls.Load())
	}
	if toolCalled.Load() != 1 {
		t.Errorf("tool called %d times, want 1", toolCalled.Load())
	}
	if handlerCalls.Load() != 1 || resultCalls.Load() != 1 {
		t.Errorf("handler calls = %d, result calls = %d, want 1 each", handlerCalls.Load(), resultCalls.Load())
	}
}

// toolHandlerFuncs adapts plain funcs to the openai.ToolHandler interface.
type toolHandlerFuncs struct {
	onCall   func(openai.ToolCall) error
	onResult func(id, name, result string) error
}

func (h toolHandlerFuncs) OnToolCall(tc openai.ToolCall) error { return h.onCall(tc) }
func (h toolHandlerFuncs) OnToolResult(id, name, result string) error {
	return h.onResult(id, name, result)
}

func TestChatCompletionRequestHasToolsSkipsLoop(t *testing.T) {
	// When the caller already supplies req.Tools, a returned tool call must be
	// surfaced directly rather than auto-executed, even with servers configured.
	srv, calls := toolCallThenTextServer(t, "get_weather", map[string]any{"city": "SF"})
	defer srv.Close()

	localServer := &openai.MCPServerFuncs{
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			t.Fatal("tool should not be auto-called when caller supplies req.Tools")
			return nil, nil
		},
	}
	c, err := New(openai.Config{BaseURL: srv.URL, LocalServer: localServer})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	resp, err := c.ChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "llama3",
		Messages: []openai.Message{{Role: "user", Content: "weather?"}},
		Tools:    []openai.Tool{{Type: "function", Function: openai.ToolFunction{Name: "get_weather"}}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error: %v", err)
	}
	if len(resp.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("expected tool call surfaced, got %+v", resp.Choices[0].Message)
	}
	if calls.Load() != 1 {
		t.Errorf("server calls = %d, want 1 (no loop)", calls.Load())
	}
}

func TestChatCompletionNoServersReturnsToolCallsDirectly(t *testing.T) {
	srv, calls := toolCallThenTextServer(t, "get_weather", nil)
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL}) // no local/remote servers configured
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	resp, err := c.ChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "llama3",
		Messages: []openai.Message{{Role: "user", Content: "weather?"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error: %v", err)
	}
	if len(resp.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("expected tool call surfaced, got %+v", resp.Choices[0].Message)
	}
	if calls.Load() != 1 {
		t.Errorf("server calls = %d, want 1 (no servers => no loop)", calls.Load())
	}
}

func TestChatCompletionToolHandlerErrorOnCall(t *testing.T) {
	srv, _ := toolCallThenTextServer(t, "get_weather", nil)
	defer srv.Close()

	localServer := &openai.MCPServerFuncs{
		ListToolsFunc: func() []mcp.MCPTool { return []mcp.MCPTool{{Name: "get_weather"}} },
	}
	c, err := New(openai.Config{BaseURL: srv.URL, LocalServer: localServer})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	ctx := openai.WithToolHandler(context.Background(), toolHandlerFuncs{
		onCall: func(tc openai.ToolCall) error { return fmt.Errorf("denied") },
	})
	_, err = c.ChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    "llama3",
		Messages: []openai.Message{{Role: "user", Content: "weather?"}},
	})
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("error = %v, want tool handler error", err)
	}
}

func TestChatCompletionCallToolError(t *testing.T) {
	srv, _ := toolCallThenTextServer(t, "get_weather", nil)
	defer srv.Close()

	localServer := &openai.MCPServerFuncs{
		ListToolsFunc: func() []mcp.MCPTool { return []mcp.MCPTool{{Name: "get_weather"}} },
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			return nil, fmt.Errorf("tool exploded")
		},
	}
	c, err := New(openai.Config{BaseURL: srv.URL, LocalServer: localServer})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	resp, err := c.ChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "llama3",
		Messages: []openai.Message{{Role: "user", Content: "weather?"}},
	})
	// ExecuteToolCalls with stopOnError=false folds the tool error into the
	// tool-result message and continues the loop rather than failing outright.
	if err != nil {
		t.Fatalf("ChatCompletion() error: %v", err)
	}
	if resp.Choices[0].Message.GetContentAsString() != "final answer" {
		t.Fatalf("unexpected final response: %+v", resp.Choices[0].Message)
	}
}

func TestChatCompletionMaxIterations(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(chatResponse{
			Model: "llama3",
			Message: message{
				Role:      "assistant",
				ToolCalls: []toolCall{{Function: toolCallFunction{Name: "loop_tool"}}},
			},
			Done:       true,
			DoneReason: "tool_calls",
		})
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
	_, err = c.ChatCompletion(context.Background(), openai.ChatCompletionRequest{
		Model:    "llama3",
		Messages: []openai.Message{{Role: "user", Content: "loop"}},
	})
	if err == nil {
		t.Fatal("expected max tool iterations error")
	}
	if !strings.Contains(err.Error(), "20") && !strings.Contains(strings.ToLower(err.Error()), "iteration") {
		t.Errorf("error = %v, want max-iterations error", err)
	}
}

// -----------------------------------------------------------------------------
// getAllTools / callTool routing
// -----------------------------------------------------------------------------

func TestGetAllToolsLocalOnly(t *testing.T) {
	c := &Client{localServer: &openai.MCPServerFuncs{
		ListToolsFunc: func() []mcp.MCPTool { return []mcp.MCPTool{{Name: "a"}, {Name: "b"}} },
	}}
	tools, err := c.getAllTools(context.Background())
	if err != nil {
		t.Fatalf("getAllTools() error: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools = %+v, want 2", tools)
	}
}

func TestGetAllToolsNoServers(t *testing.T) {
	c := &Client{}
	tools, err := c.getAllTools(context.Background())
	if err != nil {
		t.Fatalf("getAllTools() error: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("tools = %+v, want empty", tools)
	}
}

func TestGetAllToolsRemoteAggregation(t *testing.T) {
	// Spin up a real MCP server exposing one tool, and point a remote client at it.
	srv := mcp.NewServer("svc", "1")
	srv.RegisterTool(mcp.NewTool("remote_tool", "does a thing"), func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
		return mcp.NewToolResponseText("remote result"), nil
	})
	ts := httptest.NewServer(http.HandlerFunc(srv.HandleRequest))
	defer ts.Close()

	remoteClient := mcp.NewClient(ts.URL, nil, "r")
	c := &Client{
		localServer: &openai.MCPServerFuncs{
			ListToolsFunc: func() []mcp.MCPTool { return []mcp.MCPTool{{Name: "local_tool"}} },
		},
		remoteServers: []*mcp.Client{remoteClient},
	}

	tools, err := c.getAllTools(context.Background())
	if err != nil {
		t.Fatalf("getAllTools() error: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools = %+v, want 2 (local + remote)", tools)
	}

	var remoteName string
	for _, tl := range tools {
		if strings.Contains(tl.Name, "remote_tool") {
			remoteName = tl.Name
		}
	}
	if remoteName == "" {
		t.Fatalf("remote tool not found in %+v", tools)
	}

	resp, err := c.callTool(context.Background(), remoteName, nil)
	if err != nil {
		t.Fatalf("callTool(remote) error: %v", err)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "remote result" {
		t.Fatalf("callTool(remote) result = %+v", resp)
	}
}

func TestGetAllToolsRemoteError(t *testing.T) {
	// A remote server that always 500s makes ListTools fail; getAllTools must
	// propagate that error rather than silently dropping the remote's tools.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	remoteClient := mcp.NewClient(ts.URL, nil, "r")
	c := &Client{remoteServers: []*mcp.Client{remoteClient}}
	_, err := c.getAllTools(context.Background())
	if err == nil {
		t.Fatal("expected error from failing remote server")
	}
}

func TestCallToolLocal(t *testing.T) {
	c := &Client{localServer: &openai.MCPServerFuncs{
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("local:" + name), nil
		},
	}}
	resp, err := c.callTool(context.Background(), "some_tool", nil)
	if err != nil {
		t.Fatalf("callTool() error: %v", err)
	}
	if resp.Content[0].Text != "local:some_tool" {
		t.Errorf("result = %+v", resp)
	}
}

func TestCallToolNoLocalServerConfigured(t *testing.T) {
	c := &Client{}
	_, err := c.callTool(context.Background(), "anything", nil)
	if err == nil || !strings.Contains(err.Error(), "no local MCP server configured") {
		t.Fatalf("error = %v, want 'no local MCP server configured'", err)
	}
}
