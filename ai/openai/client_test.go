package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/paularlott/mcp"
)

// -----------------------------------------------------------------------------
// MCPServerFuncs
// -----------------------------------------------------------------------------

func TestMCPServerFuncs_ListToolsWithContext(t *testing.T) {
	f := &MCPServerFuncs{
		ListToolsFunc: func() []mcp.MCPTool { return []mcp.MCPTool{{Name: "t1"}} },
	}
	tools := f.ListToolsWithContext(context.Background())
	if len(tools) != 1 || tools[0].Name != "t1" {
		t.Errorf("tools = %+v", tools)
	}
}

func TestMCPServerFuncs_ListToolsWithContext_NilFunc(t *testing.T) {
	f := &MCPServerFuncs{}
	if tools := f.ListToolsWithContext(context.Background()); tools != nil {
		t.Errorf("tools = %v, want nil", tools)
	}
}

func TestMCPServerFuncs_CallTool(t *testing.T) {
	f := &MCPServerFuncs{
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("ok:" + name), nil
		},
	}
	resp, err := f.CallTool(context.Background(), "t1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content[0].Text != "ok:t1" {
		t.Errorf("resp = %+v", resp)
	}
}

func TestMCPServerFuncs_CallTool_NilFunc(t *testing.T) {
	f := &MCPServerFuncs{}
	_, err := f.CallTool(context.Background(), "t1", nil)
	if err == nil {
		t.Fatal("expected error when CallToolFunc is nil")
	}
}

// -----------------------------------------------------------------------------
// Basic client accessors
// -----------------------------------------------------------------------------

func TestClient_Provider(t *testing.T) {
	c, err := New(Config{Provider: providerOllama, BaseURL: "http://example.invalid"})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if c.Provider() != providerOllama {
		t.Errorf("Provider() = %q, want %q", c.Provider(), providerOllama)
	}
}

func TestClient_SupportsCapability(t *testing.T) {
	openaiClient, _ := New(Config{Provider: providerOpenAI, BaseURL: "http://example.invalid"})
	if !openaiClient.SupportsCapability("responses") {
		t.Error("expected OpenAI provider to support responses")
	}
	if !openaiClient.SupportsCapability("anything") {
		t.Error("expected OpenAI provider to support anything")
	}

	ollamaClient, _ := New(Config{Provider: providerOllama, BaseURL: "http://example.invalid"})
	if ollamaClient.SupportsCapability("responses") {
		t.Error("expected non-OpenAI provider to NOT support responses")
	}
	if !ollamaClient.SupportsCapability("embeddings") {
		t.Error("expected non-OpenAI provider to support embeddings")
	}
}

func TestClient_Close(t *testing.T) {
	c, _ := New(Config{BaseURL: "http://example.invalid"})
	if err := c.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

func TestClient_GetModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path = %q, want /models", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ModelsResponse{
			Object: "list",
			Data:   []Model{{ID: "gpt-x", Object: "model"}},
		})
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	resp, err := c.GetModels(context.Background())
	if err != nil {
		t.Fatalf("GetModels() error: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].ID != "gpt-x" {
		t.Errorf("resp = %+v", resp)
	}
}

func TestClient_GetModels_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"type":"server_error","message":"boom"}}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL, MaxRetries: -1})
	_, err := c.GetModels(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestClient_CreateEmbedding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Errorf("path = %q, want /embeddings", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(EmbeddingResponse{
			Object: "list",
			Data:   []Embedding{{Object: "embedding", Embedding: []float64{0.1, 0.2}, Index: 0}},
			Model:  "embed-x",
		})
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	resp, err := c.CreateEmbedding(context.Background(), EmbeddingRequest{Model: "embed-x", Input: "hello"})
	if err != nil {
		t.Fatalf("CreateEmbedding() error: %v", err)
	}
	if len(resp.Data) != 1 || len(resp.Data[0].Embedding) != 2 {
		t.Errorf("resp = %+v", resp)
	}
}

func TestClient_CreateEmbedding_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"bad"}}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL, MaxRetries: -1})
	_, err := c.CreateEmbedding(context.Background(), EmbeddingRequest{Model: "embed-x", Input: "hi"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// -----------------------------------------------------------------------------
// getAllTools / callTool
// -----------------------------------------------------------------------------

func newMCPTestServer(t *testing.T, toolName string) *httptest.Server {
	srv := mcp.NewServer("svc", "1")
	srv.RegisterTool(mcp.NewTool(toolName, "a tool", mcp.String("s", "s", mcp.Required())), func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
		v, _ := req.String("s")
		return mcp.NewToolResponseText("remote:" + v), nil
	})
	return httptest.NewServer(http.HandlerFunc(srv.HandleRequest))
}

func TestClient_getAllTools_LocalOnly(t *testing.T) {
	local := &MCPServerFuncs{ListToolsFunc: func() []mcp.MCPTool { return []mcp.MCPTool{{Name: "local1"}} }}
	c, _ := New(Config{BaseURL: "http://example.invalid", LocalServer: local})
	tools, err := c.getAllTools(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "local1" {
		t.Errorf("tools = %+v", tools)
	}
}

func TestClient_getAllTools_LocalAndRemote(t *testing.T) {
	remoteSrv := newMCPTestServer(t, "search")
	defer remoteSrv.Close()

	local := &MCPServerFuncs{ListToolsFunc: func() []mcp.MCPTool { return []mcp.MCPTool{{Name: "local1"}} }}
	c, _ := New(Config{
		BaseURL:     "http://example.invalid",
		LocalServer: local,
		RemoteServerConfigs: []RemoteServerConfig{
			{BaseURL: remoteSrv.URL, Namespace: "remote"},
		},
	})
	tools, err := c.getAllTools(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools = %+v, want 2", tools)
	}
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Name] = true
	}
	if !names["local1"] {
		t.Error("expected local1 in tools")
	}
	if !names["remote"+mcp.DefaultNamespaceSeparator+"search"] {
		t.Errorf("expected namespaced remote tool, got %+v", tools)
	}
}

func TestClient_getAllTools_RemoteError(t *testing.T) {
	c, _ := New(Config{
		BaseURL: "http://example.invalid",
		RemoteServerConfigs: []RemoteServerConfig{
			{BaseURL: "http://127.0.0.1:1", Namespace: "remote"}, // nothing listens here
		},
	})
	_, err := c.getAllTools(context.Background())
	if err == nil {
		t.Fatal("expected error from unreachable remote server")
	}
}

func TestClient_callTool_LocalOnly(t *testing.T) {
	local := &MCPServerFuncs{
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("local:" + name), nil
		},
	}
	c, _ := New(Config{BaseURL: "http://example.invalid", LocalServer: local})
	resp, err := c.callTool(context.Background(), "mytool", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content[0].Text != "local:mytool" {
		t.Errorf("resp = %+v", resp)
	}
}

func TestClient_callTool_NoLocalServer(t *testing.T) {
	c, _ := New(Config{BaseURL: "http://example.invalid"})
	_, err := c.callTool(context.Background(), "mytool", nil)
	if err == nil {
		t.Fatal("expected error with no local server configured")
	}
}

func TestClient_callTool_RemoteNamespaceRouting(t *testing.T) {
	remoteSrv := newMCPTestServer(t, "search")
	defer remoteSrv.Close()

	c, _ := New(Config{
		BaseURL: "http://example.invalid",
		RemoteServerConfigs: []RemoteServerConfig{
			{BaseURL: remoteSrv.URL, Namespace: "remote"},
		},
	})
	toolName := "remote" + mcp.DefaultNamespaceSeparator + "search"
	resp, err := c.callTool(context.Background(), toolName, map[string]any{"s": "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content[0].Text != "remote:hi" {
		t.Errorf("resp = %+v", resp)
	}
}

// -----------------------------------------------------------------------------
// ChatCompletion multi-turn tool processing
// -----------------------------------------------------------------------------

func TestClient_ChatCompletion_NoTools_Simple(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID:      "chat_1",
			Object:  "chat.completion",
			Model:   "gpt-x",
			Choices: []Choice{{Index: 0, Message: Message{Role: "assistant", Content: "hi there"}, FinishReason: "stop"}},
		})
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	resp, err := c.ChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    "gpt-x",
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error: %v", err)
	}
	if resp.Choices[0].Message.Content != "hi there" {
		t.Errorf("resp = %+v", resp)
	}
	if resp.Usage == nil {
		t.Error("expected estimated usage to be injected")
	}
}

func TestClient_ChatCompletion_MultiTurnToolLoop(t *testing.T) {
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			// First response: model wants to call a tool.
			json.NewEncoder(w).Encode(ChatCompletionResponse{
				ID: "chat_1", Object: "chat.completion", Model: "gpt-x",
				Choices: []Choice{{
					Index: 0,
					Message: Message{
						Role: "assistant",
						ToolCalls: []ToolCall{
							{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "get_weather", Arguments: map[string]any{"city": "NYC"}}},
						},
					},
					FinishReason: "tool_calls",
				}},
				Usage: &Usage{PromptTokens: 5, CompletionTokens: 5, TotalTokens: 10},
			})
			return
		}
		// Second response: final answer.
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID: "chat_2", Object: "chat.completion", Model: "gpt-x",
			Choices: []Choice{{Index: 0, Message: Message{Role: "assistant", Content: "It is sunny"}, FinishReason: "stop"}},
			Usage:   &Usage{PromptTokens: 8, CompletionTokens: 3, TotalTokens: 11},
		})
	}))
	defer srv.Close()

	var toolCalled bool
	local := &MCPServerFuncs{
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			toolCalled = true
			return mcp.NewToolResponseText("sunny"), nil
		},
	}
	c, _ := New(Config{BaseURL: srv.URL, LocalServer: local})

	handler := &recordingToolHandler{}
	ctx := WithToolHandler(context.Background(), handler)

	resp, err := c.ChatCompletion(ctx, ChatCompletionRequest{
		Model:    "gpt-x",
		Messages: []Message{{Role: "user", Content: "what's the weather"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error: %v", err)
	}
	if resp.Choices[0].Message.Content != "It is sunny" {
		t.Errorf("final content = %q", resp.Choices[0].Message.Content)
	}
	if !toolCalled {
		t.Error("expected local tool to be called")
	}
	if len(handler.calls) != 1 || handler.calls[0] != "get_weather" {
		t.Errorf("handler.calls = %v", handler.calls)
	}
	if len(handler.results) != 1 || handler.results[0] != "sunny" {
		t.Errorf("handler.results = %v", handler.results)
	}
	// cumulative usage across both turns (exact values depend on internal token
	// estimation once InjectUsageIfMissing recalculates per-turn usage against
	// the growing conversation, so just assert usage was accumulated).
	if resp.Usage == nil || resp.Usage.PromptTokens == 0 || resp.Usage.CompletionTokens == 0 {
		t.Errorf("cumulative usage = %+v, want non-zero prompt/completion tokens", resp.Usage)
	}
}

func TestClient_ChatCompletion_RequestHasTools_SkipsAutoProcessing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID: "chat_1", Object: "chat.completion", Model: "gpt-x",
			Choices: []Choice{{
				Index:   0,
				Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Function: ToolCallFunction{Name: "f"}}}},
				FinishReason: "tool_calls",
			}},
		})
	}))
	defer srv.Close()

	local := &MCPServerFuncs{
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			t.Fatal("tool should not be auto-executed when caller supplies tools")
			return nil, nil
		},
	}
	c, _ := New(Config{BaseURL: srv.URL, LocalServer: local})
	resp, err := c.ChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    "gpt-x",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Tools:    []Tool{{Type: "function", Function: ToolFunction{Name: "f"}}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion() error: %v", err)
	}
	if len(resp.Choices[0].Message.ToolCalls) != 1 {
		t.Errorf("expected tool calls returned to caller, got %+v", resp.Choices[0].Message.ToolCalls)
	}
}

func TestClient_ChatCompletion_MaxToolIterations(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID: "chat_1", Object: "chat.completion", Model: "gpt-x",
			Choices: []Choice{{
				Index:        0,
				Message:      Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Function: ToolCallFunction{Name: "loop"}}}},
				FinishReason: "tool_calls",
			}},
		})
	}))
	defer srv.Close()

	local := &MCPServerFuncs{
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("again"), nil
		},
	}
	c, _ := New(Config{BaseURL: srv.URL, LocalServer: local})
	_, err := c.ChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    "gpt-x",
		Messages: []Message{{Role: "user", Content: "loop forever"}},
	})
	if err == nil {
		t.Fatal("expected max tool iterations error")
	}
	var maxIterErr *MaxToolIterationsError
	if !errorsAs(err, &maxIterErr) {
		t.Fatalf("expected *MaxToolIterationsError, got %T: %v", err, err)
	}
}

// errorsAs is a tiny local wrapper to avoid importing errors just for As in this file
// (errors is already imported by other test files in the package, but keep this
// file self-contained for clarity).
func errorsAs(err error, target **MaxToolIterationsError) bool {
	for err != nil {
		if e, ok := err.(*MaxToolIterationsError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func TestClient_ChatCompletion_ToolHandlerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			ID: "chat_1", Object: "chat.completion", Model: "gpt-x",
			Choices: []Choice{{
				Index:        0,
				Message:      Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Function: ToolCallFunction{Name: "f"}}}},
				FinishReason: "tool_calls",
			}},
		})
	}))
	defer srv.Close()

	local := &MCPServerFuncs{
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("ok"), nil
		},
	}
	c, _ := New(Config{BaseURL: srv.URL, LocalServer: local})

	failingHandler := &failingToolHandler{failOnCall: true}
	ctx := WithToolHandler(context.Background(), failingHandler)
	_, err := c.ChatCompletion(ctx, ChatCompletionRequest{
		Model:    "gpt-x",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error from failing tool handler")
	}
}

type failingToolHandler struct {
	failOnCall   bool
	failOnResult bool
}

func (h *failingToolHandler) OnToolCall(toolCall ToolCall) error {
	if h.failOnCall {
		return fmt.Errorf("handler refused tool call")
	}
	return nil
}

func (h *failingToolHandler) OnToolResult(toolCallID, toolName, result string) error {
	if h.failOnResult {
		return fmt.Errorf("handler refused tool result")
	}
	return nil
}

// -----------------------------------------------------------------------------
// StreamChatCompletion multi-turn tool processing
// -----------------------------------------------------------------------------

func writeSSEChunk(w http.ResponseWriter, flusher http.Flusher, chunk ChatCompletionResponse) {
	data, _ := json.Marshal(chunk)
	w.Write([]byte("data: " + string(data) + "\n\n"))
	flusher.Flush()
}

func TestClient_StreamChatCompletion_NoTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		writeSSEChunk(w, flusher, ChatCompletionResponse{
			ID: "s1", Object: "chat.completion.chunk", Model: "gpt-x",
			Choices: []Choice{{Index: 0, Delta: Delta{Content: "Hello"}}},
		})
		writeSSEChunk(w, flusher, ChatCompletionResponse{
			ID: "s1", Object: "chat.completion.chunk", Model: "gpt-x",
			Choices: []Choice{{Index: 0, FinishReason: "stop"}},
		})
		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	stream := c.StreamChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    "gpt-x",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})

	var gotContent string
	for stream.Next() {
		chunk := stream.Current()
		if len(chunk.Choices) > 0 {
			gotContent += chunk.Choices[0].Delta.Content
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if gotContent != "Hello" {
		t.Errorf("gotContent = %q, want Hello", gotContent)
	}
}

func TestClient_StreamChatCompletion_MultiTurnToolLoop(t *testing.T) {
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		if callCount == 1 {
			writeSSEChunk(w, flusher, ChatCompletionResponse{
				ID: "s1", Object: "chat.completion.chunk", Model: "gpt-x",
				Choices: []Choice{{
					Index: 0,
					Delta: Delta{ToolCalls: []DeltaToolCall{
						{Index: 0, ID: "call_1", Type: "function", Function: DeltaFunction{Name: "get_weather", Arguments: `{"city":"NYC"}`}},
					}},
				}},
			})
			writeSSEChunk(w, flusher, ChatCompletionResponse{
				ID: "s1", Object: "chat.completion.chunk", Model: "gpt-x",
				Choices: []Choice{{Index: 0, FinishReason: "tool_calls"}},
			})
			w.Write([]byte("data: [DONE]\n\n"))
			flusher.Flush()
			return
		}
		writeSSEChunk(w, flusher, ChatCompletionResponse{
			ID: "s2", Object: "chat.completion.chunk", Model: "gpt-x",
			Choices: []Choice{{Index: 0, Delta: Delta{Content: "sunny"}}},
		})
		writeSSEChunk(w, flusher, ChatCompletionResponse{
			ID: "s2", Object: "chat.completion.chunk", Model: "gpt-x",
			Choices: []Choice{{Index: 0, FinishReason: "stop"}},
		})
		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer srv.Close()

	local := &MCPServerFuncs{
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("sunny result"), nil
		},
	}
	c, _ := New(Config{BaseURL: srv.URL, LocalServer: local})
	stream := c.StreamChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    "gpt-x",
		Messages: []Message{{Role: "user", Content: "weather?"}},
	})

	var gotContent string
	for stream.Next() {
		chunk := stream.Current()
		if len(chunk.Choices) > 0 {
			gotContent += chunk.Choices[0].Delta.Content
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if !strings.Contains(gotContent, "sunny") {
		t.Errorf("gotContent = %q, want to contain sunny", gotContent)
	}
}

func TestClient_StreamChatCompletion_RequestHasTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		writeSSEChunk(w, flusher, ChatCompletionResponse{
			ID: "s1", Object: "chat.completion.chunk", Model: "gpt-x",
			Choices: []Choice{{
				Index: 0,
				Delta: Delta{ToolCalls: []DeltaToolCall{{Index: 0, ID: "call_1", Function: DeltaFunction{Name: "f"}}}},
			}},
			Usage: &Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		})
		writeSSEChunk(w, flusher, ChatCompletionResponse{
			ID: "s1", Object: "chat.completion.chunk", Model: "gpt-x",
			Choices: []Choice{{Index: 0, FinishReason: "tool_calls"}},
		})
		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	stream := c.StreamChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    "gpt-x",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Tools:    []Tool{{Type: "function", Function: ToolFunction{Name: "f"}}},
	})
	for stream.Next() {
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
}

// -----------------------------------------------------------------------------
// Native Responses API (useNativeResponses = true)
// -----------------------------------------------------------------------------

func nativeClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	native := true
	c, err := New(Config{BaseURL: srv.URL, UseNativeResponses: &native, RequestTimeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return c
}

func TestClient_CreateResponse_Native_Simple(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ResponseObject{
			ID: "resp_1", Object: "response", Status: "completed", Model: "gpt-x",
			Output: []any{
				map[string]any{"type": "message", "role": "assistant", "content": []any{
					map[string]any{"type": "output_text", "text": "hello"},
				}},
			},
		})
	}))
	defer srv.Close()

	c := nativeClient(t, srv)
	resp, err := c.CreateResponse(context.Background(), CreateResponseRequest{
		Model: "gpt-x",
		Input: []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatalf("CreateResponse() error: %v", err)
	}
	if resp.OutputText() != "hello" {
		t.Errorf("OutputText() = %q", resp.OutputText())
	}
}

func TestClient_CreateResponse_Native_WithToolLoop(t *testing.T) {
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			json.NewEncoder(w).Encode(ResponseObject{
				ID: "resp_1", Object: "response", Status: "completed", Model: "gpt-x",
				Output: []any{
					map[string]any{"type": "function_call", "id": "call_1", "call_id": "call_1", "name": "get_weather", "arguments": `{"city":"NYC"}`},
				},
			})
			return
		}
		json.NewEncoder(w).Encode(ResponseObject{
			ID: "resp_2", Object: "response", Status: "completed", Model: "gpt-x",
			Output: []any{
				map[string]any{"type": "message", "role": "assistant", "content": []any{
					map[string]any{"type": "output_text", "text": "sunny"},
				}},
			},
		})
	}))
	defer srv.Close()

	local := &MCPServerFuncs{
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("sunny weather"), nil
		},
	}
	native := true
	c, _ := New(Config{BaseURL: srv.URL, UseNativeResponses: &native, LocalServer: local})

	resp, err := c.CreateResponse(context.Background(), CreateResponseRequest{
		Model: "gpt-x",
		Input: []any{map[string]any{"type": "message", "role": "user", "content": "weather?"}},
	})
	if err != nil {
		t.Fatalf("CreateResponse() error: %v", err)
	}
	if resp.OutputText() != "sunny" {
		t.Errorf("OutputText() = %q", resp.OutputText())
	}
	if callCount != 2 {
		t.Errorf("callCount = %d, want 2", callCount)
	}
}

func TestClient_CreateResponse_Native_RequestHasTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ResponseObject{
			ID: "resp_1", Object: "response", Status: "completed", Model: "gpt-x",
			Output: []any{
				map[string]any{"type": "function_call", "call_id": "call_1", "name": "f", "arguments": `{}`},
			},
		})
	}))
	defer srv.Close()

	c := nativeClient(t, srv)
	resp, err := c.CreateResponse(context.Background(), CreateResponseRequest{
		Model: "gpt-x",
		Input: []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
		Tools: []Tool{{Type: "function", Function: ToolFunction{Name: "f"}}},
	})
	if err != nil {
		t.Fatalf("CreateResponse() error: %v", err)
	}
	if len(resp.Output) != 1 {
		t.Errorf("expected tool call returned to caller: %+v", resp.Output)
	}
}

func TestClient_CreateResponse_Native_Background_PureNative(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ResponseObject{
			ID: "resp_1", Object: "response", Status: "in_progress", Model: "gpt-x",
		})
	}))
	defer srv.Close()

	c := nativeClient(t, srv)
	resp, err := c.CreateResponse(context.Background(), CreateResponseRequest{
		Model:      "gpt-x",
		Background: true,
		Input:      []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatalf("CreateResponse() error: %v", err)
	}
	if resp.Status != "in_progress" {
		t.Errorf("Status = %q, want in_progress", resp.Status)
	}
}

func TestClient_CreateResponse_Native_Background_WithToolProcessing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ResponseObject{
			ID: "resp_1", Object: "response", Status: "completed", Model: "gpt-x",
			Output: []any{
				map[string]any{"type": "message", "role": "assistant", "content": []any{
					map[string]any{"type": "output_text", "text": "done"},
				}},
			},
		})
	}))
	defer srv.Close()

	local := &MCPServerFuncs{
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("ok"), nil
		},
	}
	native := true
	c, _ := New(Config{BaseURL: srv.URL, UseNativeResponses: &native, LocalServer: local, RequestTimeout: 5 * time.Second})

	resp, err := c.CreateResponse(context.Background(), CreateResponseRequest{
		Model:      "gpt-x",
		Background: true,
		Input:      []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatalf("CreateResponse() error: %v", err)
	}
	if resp.Status != "in_progress" {
		t.Errorf("immediate Status = %q, want in_progress", resp.Status)
	}

	// Poll GetResponse until the background goroutine completes.
	deadline := time.After(2 * time.Second)
	for {
		got, err := c.GetResponse(context.Background(), resp.ID)
		if err != nil {
			t.Fatalf("GetResponse() error: %v", err)
		}
		if got.Status == "completed" {
			if got.OutputText() != "done" {
				t.Errorf("OutputText() = %q", got.OutputText())
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for background response to complete")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestClient_ResponseCRUD_Native(t *testing.T) {
	var lastMethod, lastPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastMethod = r.Method
		lastPath = r.URL.Path
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/responses/"):
			json.NewEncoder(w).Encode(ResponseObject{ID: "resp_1", Object: "response", Status: "completed", Model: "gpt-x"})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cancel"):
			json.NewEncoder(w).Encode(ResponseObject{ID: "resp_1", Object: "response", Status: "cancelled", Model: "gpt-x"})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/compact"):
			json.NewEncoder(w).Encode(ResponseObject{ID: "resp_1", Object: "response", Status: "completed", Model: "gpt-x"})
		case r.Method == http.MethodDelete:
			json.NewEncoder(w).Encode(map[string]any{"id": "resp_1", "object": "response", "deleted": true})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := nativeClient(t, srv)

	if _, err := c.GetResponse(context.Background(), "resp_1"); err != nil {
		t.Fatalf("GetResponse() error: %v", err)
	}
	if lastPath != "/responses/resp_1" {
		t.Errorf("path = %q", lastPath)
	}

	if _, err := c.CancelResponse(context.Background(), "resp_1"); err != nil {
		t.Fatalf("CancelResponse() error: %v", err)
	}
	if lastMethod != http.MethodPost || lastPath != "/responses/resp_1/cancel" {
		t.Errorf("method/path = %s %s", lastMethod, lastPath)
	}

	if _, err := c.CompactResponse(context.Background(), "resp_1"); err != nil {
		t.Fatalf("CompactResponse() error: %v", err)
	}
	if lastPath != "/responses/resp_1/compact" {
		t.Errorf("path = %q", lastPath)
	}

	if err := c.DeleteResponse(context.Background(), "resp_1"); err != nil {
		t.Fatalf("DeleteResponse() error: %v", err)
	}
	if lastMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", lastMethod)
	}
}

func TestClient_ResponseCRUD_Native_InvalidID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not be called for invalid IDs")
	}))
	defer srv.Close()
	c := nativeClient(t, srv)

	if _, err := c.GetResponse(context.Background(), "../etc/passwd"); err == nil {
		t.Error("expected error for path-traversal ID in GetResponse")
	}
	if _, err := c.CancelResponse(context.Background(), "a/b"); err == nil {
		t.Error("expected error for invalid ID in CancelResponse")
	}
	if _, err := c.CompactResponse(context.Background(), "a/b"); err == nil {
		t.Error("expected error for invalid ID in CompactResponse")
	}
	if err := c.DeleteResponse(context.Background(), "a/b"); err == nil {
		t.Error("expected error for invalid ID in DeleteResponse")
	}
}

func TestClient_DeleteResponse_Native_DeletedFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"id": "resp_1", "object": "response", "deleted": false})
	}))
	defer srv.Close()
	c := nativeClient(t, srv)
	if err := c.DeleteResponse(context.Background(), "resp_1"); err == nil {
		t.Error("expected error when API returns deleted=false")
	}
}

func TestClient_ResponseCRUD_UsesLocalStateWhenPresent(t *testing.T) {
	// When a background response's state is tracked locally (emulated path),
	// GetResponse/CancelResponse/DeleteResponse/CompactResponse must go through
	// the emulated manager rather than hitting the native endpoint, even when
	// UseNativeResponses is true.
	var nativeHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nativeHit = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	native := true
	manager := GetManager()
	state := manager.Create(func() {}, "gpt-x")
	state.SetResult(&ResponseObject{ID: state.ID, Object: "response", Status: "completed", Model: "gpt-x"})

	c, _ := New(Config{BaseURL: srv.URL, UseNativeResponses: &native})
	resp, err := c.GetResponse(context.Background(), state.ID)
	if err != nil {
		t.Fatalf("GetResponse() error: %v", err)
	}
	if resp.ID != state.ID {
		t.Errorf("resp.ID = %q, want %q", resp.ID, state.ID)
	}
	if nativeHit {
		t.Error("expected native endpoint NOT to be hit when local state exists")
	}
}

func TestHasResponseToolCalls(t *testing.T) {
	if hasResponseToolCalls(nil) {
		t.Error("nil response should have no tool calls")
	}
	if hasResponseToolCalls(&ResponseObject{}) {
		t.Error("empty output should have no tool calls")
	}
	resp := &ResponseObject{Output: []any{
		map[string]any{"type": "message"},
		map[string]any{"type": "function_call"},
	}}
	if !hasResponseToolCalls(resp) {
		t.Error("expected true for function_call output item")
	}
	resp2 := &ResponseObject{Output: []any{map[string]any{"type": "tool_call"}}}
	if !hasResponseToolCalls(resp2) {
		t.Error("expected true for tool_call output item")
	}
}

func TestExtractToolCallsFromResponse(t *testing.T) {
	if tcs := extractToolCallsFromResponse(nil); tcs != nil {
		t.Errorf("tcs = %v, want nil", tcs)
	}

	resp := &ResponseObject{Output: []any{
		map[string]any{"type": "function_call", "id": "id_1", "name": "f1", "arguments": map[string]any{"a": 1}},
		map[string]any{"type": "function_call", "call_id": "call_2", "name": "f2", "arguments": `{"b":2}`},
		map[string]any{"type": "message"}, // ignored
	}}
	tcs := extractToolCallsFromResponse(resp)
	if len(tcs) != 2 {
		t.Fatalf("len(tcs) = %d, want 2", len(tcs))
	}
	if tcs[0].ID != "id_1" || tcs[0].Function.Name != "f1" || tcs[0].Function.Arguments["a"] != 1 {
		t.Errorf("tcs[0] = %+v", tcs[0])
	}
	if tcs[1].ID != "call_2" || tcs[1].Function.Name != "f2" || tcs[1].Function.Arguments["b"] != float64(2) {
		t.Errorf("tcs[1] = %+v", tcs[1])
	}
}

func TestExtractToolCallsFromResponse_InvalidArgumentsJSON(t *testing.T) {
	resp := &ResponseObject{Output: []any{
		map[string]any{"type": "function_call", "call_id": "c1", "name": "f", "arguments": "not json"},
	}}
	tcs := extractToolCallsFromResponse(resp)
	if len(tcs) != 1 {
		t.Fatalf("len(tcs) = %d, want 1", len(tcs))
	}
	if tcs[0].Function.Arguments != nil {
		t.Errorf("Arguments = %v, want nil on invalid JSON", tcs[0].Function.Arguments)
	}
}
