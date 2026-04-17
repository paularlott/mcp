package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestNativeToolsInToolSearch verifies that tool_search returns native tools alongside discoverable tools
func TestNativeToolsInToolSearch(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register native tools
	server.RegisterTool(
		NewTool("get_customer", "Get customer details by ID", String("id", "Customer ID")),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("customer data"), nil
		},
		"customer", "crm",
	)

	server.RegisterTool(
		NewTool("general_knowledge", "Use this agent to fetch data about people and knot application",
			String("message", "The question to ask")),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("knowledge result"), nil
		},
		"people", "knot", "scriptling",
	)

	// Register a discoverable tool
	server.RegisterTool(
		NewTool("people_kb", "Knowledge base about people and contacts",
			String("message", "Question about people")).Discoverable("people", "contacts", "staff"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("people data"), nil
		},
	)

	t.Run("CallTool_tool_search_empty_query_returns_all", func(t *testing.T) {
		ctx := context.Background()
		response, err := server.CallTool(ctx, "tool_search", map[string]interface{}{
			"query":       "",
			"max_results": 100,
		})
		if err != nil {
			t.Fatalf("tool_search failed: %v", err)
		}

		var results []SearchResult
		if err := json.Unmarshal([]byte(response.Content[0].Text), &results); err != nil {
			t.Fatalf("Failed to parse results: %v", err)
		}

		t.Logf("Got %d results:", len(results))
		for _, r := range results {
			t.Logf("  - %s (score=%.2f): %s", r.Name, r.Score, r.Description)
		}

		// Should find all 3 tools
		names := make(map[string]SearchResult)
		for _, r := range results {
			names[r.Name] = r
		}

		if _, ok := names["get_customer"]; !ok {
			t.Error("get_customer (native) not found in results")
		}

		if _, ok := names["general_knowledge"]; !ok {
			t.Error("general_knowledge (native) not found in results")
		}

		if _, ok := names["people_kb"]; !ok {
			t.Error("people_kb (discoverable) not found in results")
		}
	})

	t.Run("CallTool_tool_search_by_keyword_finds_native", func(t *testing.T) {
		ctx := context.Background()
		response, err := server.CallTool(ctx, "tool_search", map[string]interface{}{
			"query": "people",
		})
		if err != nil {
			t.Fatalf("tool_search failed: %v", err)
		}

		var results []SearchResult
		if err := json.Unmarshal([]byte(response.Content[0].Text), &results); err != nil {
			t.Fatalf("Failed to parse results: %v", err)
		}

		t.Logf("Search 'people' got %d results:", len(results))
		for _, r := range results {
			t.Logf("  - %s (score=%.2f)", r.Name, r.Score)
		}

		foundGeneralKnowledge := false
		foundPeopleKb := false
		for _, r := range results {
			if r.Name == "general_knowledge" {
				foundGeneralKnowledge = true
			}
			if r.Name == "people_kb" {
				foundPeopleKb = true
			}
		}

		if !foundGeneralKnowledge {
			t.Error("general_knowledge should match 'people' keyword")
		}
		if !foundPeopleKb {
			t.Error("people_kb should match 'people' keyword")
		}
	})

	t.Run("CallTool_tool_search_by_topic_finds_native", func(t *testing.T) {
		ctx := context.Background()
		response, err := server.CallTool(ctx, "tool_search", map[string]interface{}{
			"query": "knot",
		})
		if err != nil {
			t.Fatalf("tool_search failed: %v", err)
		}

		var results []SearchResult
		if err := json.Unmarshal([]byte(response.Content[0].Text), &results); err != nil {
			t.Fatalf("Failed to parse results: %v", err)
		}

		t.Logf("Search 'knot' got %d results:", len(results))
		for _, r := range results {
			t.Logf("  - %s (score=%.2f)", r.Name, r.Score)
		}

		found := false
		for _, r := range results {
			if r.Name == "general_knowledge" {
				found = true
			}
		}
		if !found {
			t.Error("general_knowledge should match 'knot' keyword")
		}
	})

	t.Run("direct_CallTool_can_call_discoverable_tools", func(t *testing.T) {
		ctx := context.Background()
		response, err := server.CallTool(ctx, "people_kb", map[string]interface{}{
			"message": "who is paul?",
		})
		if err != nil {
			t.Fatalf("direct CallTool failed: %v", err)
		}

		if response.Content[0].Text != "people data" {
			t.Errorf("Expected 'people data', got '%s'", response.Content[0].Text)
		}
	})

	t.Run("execute_tool_can_call_native_tools", func(t *testing.T) {
		ctx := context.Background()
		response, err := server.CallTool(ctx, "execute_tool", map[string]interface{}{
			"name": "general_knowledge",
			"arguments": map[string]interface{}{
				"message": "who is paul?",
			},
		})
		if err != nil {
			t.Fatalf("execute_tool failed: %v", err)
		}

		if response.Content[0].Text != "knowledge result" {
			t.Errorf("Expected 'knowledge result', got '%s'", response.Content[0].Text)
		}
	})

	t.Run("execute_tool_can_call_discoverable_tools", func(t *testing.T) {
		ctx := context.Background()
		response, err := server.CallTool(ctx, "execute_tool", map[string]interface{}{
			"name": "people_kb",
			"arguments": map[string]interface{}{
				"message": "who is paul?",
			},
		})
		if err != nil {
			t.Fatalf("execute_tool failed: %v", err)
		}

		if response.Content[0].Text != "people data" {
			t.Errorf("Expected 'people data', got '%s'", response.Content[0].Text)
		}
	})
}

// TestNativeToolsInToolSearchHTTP tests the full HTTP path
func TestNativeToolsInToolSearchHTTP(t *testing.T) {
	server := NewServer("test", "1.0.0")

	server.RegisterTool(
		NewTool("general_knowledge", "Knowledge base about people, knot and scriptling",
			String("message", "Question to ask")),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			msg, _ := req.String("message")
			return NewToolResponseText("Answer to: " + msg), nil
		},
		"people", "knot", "scriptling",
	)

	server.RegisterTool(
		NewTool("people_kb", "Specialist KB about people",
			String("message", "Question")).Discoverable("people", "contacts"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			msg, _ := req.String("message")
			return NewToolResponseText("People answer: " + msg), nil
		},
	)

	ts := httptest.NewServer(http.HandlerFunc(server.HandleRequest))
	defer ts.Close()

	// Helper to make MCP JSON-RPC calls
	callMCP := func(method string, params interface{}) (json.RawMessage, error) {
		body := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  method,
			"params":  params,
		}
		bodyBytes, _ := json.Marshal(body)
		resp, err := http.Post(ts.URL, "application/json", strings.NewReader(string(bodyBytes)))
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		respBytes, _ := io.ReadAll(resp.Body)

		var rpcResp struct {
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(respBytes, &rpcResp); err != nil {
			return nil, fmt.Errorf("unmarshal response: %w\nraw: %s", err, string(respBytes))
		}
		if rpcResp.Error != nil {
			return nil, fmt.Errorf("RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
		}
		return rpcResp.Result, nil
	}

	t.Run("HTTP_tool_search_returns_native_tools", func(t *testing.T) {
		result, err := callMCP("tools/call", map[string]interface{}{
			"name": "tool_search",
			"arguments": map[string]interface{}{
				"query":       "people",
				"max_results": 10,
			},
		})
		if err != nil {
			t.Fatalf("tools/call failed: %v", err)
		}

		var toolResult struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(result, &toolResult); err != nil {
			t.Fatalf("Failed to parse tool result: %v\nraw: %s", err, string(result))
		}

		var results []SearchResult
		if err := json.Unmarshal([]byte(toolResult.Content[0].Text), &results); err != nil {
			t.Fatalf("Failed to parse search results: %v\nraw: %s", err, toolResult.Content[0].Text)
		}

		t.Logf("HTTP search 'people' got %d results:", len(results))
		for _, r := range results {
			t.Logf("  - %s (score=%.2f)", r.Name, r.Score)
		}

		foundNative := false
		foundDiscoverable := false
		for _, r := range results {
			if r.Name == "general_knowledge" {
				foundNative = true
			}
			if r.Name == "people_kb" {
				foundDiscoverable = true
			}
		}

		if !foundNative {
			t.Error("general_knowledge (native) not found in HTTP tool_search results")
		}
		if !foundDiscoverable {
			t.Error("people_kb (discoverable) not found in HTTP tool_search results")
		}
	})

	t.Run("HTTP_execute_tool_calls_native", func(t *testing.T) {
		result, err := callMCP("tools/call", map[string]interface{}{
			"name": "execute_tool",
			"arguments": map[string]interface{}{
				"name": "general_knowledge",
				"arguments": map[string]interface{}{
					"message": "who is paul?",
				},
			},
		})
		if err != nil {
			t.Fatalf("tools/call execute_tool failed: %v", err)
		}

		var toolResult struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(result, &toolResult); err != nil {
			t.Fatalf("Failed to parse result: %v", err)
		}

		if toolResult.Content[0].Text != "Answer to: who is paul?" {
			t.Errorf("Expected 'Answer to: who is paul?', got '%s'", toolResult.Content[0].Text)
		}
	})

	t.Run("HTTP_direct_call_can_call_discoverable", func(t *testing.T) {
		result, err := callMCP("tools/call", map[string]interface{}{
			"name": "people_kb",
			"arguments": map[string]interface{}{
				"message": "who is paul?",
			},
		})
		if err != nil {
			t.Fatalf("tools/call people_kb failed: %v", err)
		}

		var toolResult struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(result, &toolResult); err != nil {
			t.Fatalf("Failed to parse result: %v", err)
		}

		if toolResult.Content[0].Text != "People answer: who is paul?" {
			t.Errorf("Expected 'People answer: who is paul?', got '%s'", toolResult.Content[0].Text)
		}
	})
}

// mockProvider implements ToolProvider for testing with native and discoverable tools
type mockProvider struct {
	tools []MCPTool
}

func (p *mockProvider) GetTools(ctx context.Context) ([]MCPTool, error) {
	return p.tools, nil
}

func (p *mockProvider) ExecuteTool(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
	for _, tool := range p.tools {
		if tool.Name == name {
			msg, _ := params["message"].(string)
			return NewToolResponseText(fmt.Sprintf("Provider result for %s: %s", name, msg)), nil
		}
	}
	return nil, ErrUnknownTool
}

// TestProviderNativeToolsInToolSearch verifies that native tools from providers
// appear in tool_search results — this is the real-world fortix-mcp scenario
func TestProviderNativeToolsInToolSearch(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register a static discoverable tool (so tool_search/execute_tool exist)
	server.RegisterTool(
		NewTool("static_disc", "A static discoverable tool",
			String("arg", "Argument")).Discoverable("static"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("static disc"), nil
		},
	)

	// Create a provider with both native and discoverable tools (like TenantScriptProvider)
	provider := &mockProvider{
		tools: []MCPTool{
			{
				Name:        "general_knowledge",
				Description: "Use this agent to fetch data about people, knot and scriptling",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"message": map[string]interface{}{"type": "string"},
					},
				},
				Visibility: ToolVisibilityNative,
				Keywords:   []string{"people", "knot", "scriptling"},
			},
			{
				Name:        "crm_search",
				Description: "Search the CRM for records",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"query": map[string]interface{}{"type": "string"},
					},
				},
				Visibility: ToolVisibilityNative,
				Keywords:   []string{"search", "crm"},
			},
			{
				Name:        "people_kb",
				Description: "Specialist knowledge base about people and contacts",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"message": map[string]interface{}{"type": "string"},
					},
				},
				Visibility: ToolVisibilityDiscoverable,
				Keywords:   []string{"people", "contacts", "staff"},
			},
		},
	}

	t.Run("provider_native_tools_found_by_tool_search", func(t *testing.T) {
		ctx := WithToolProviders(context.Background(), provider)
		response, err := server.CallTool(ctx, "tool_search", map[string]interface{}{
			"query":       "",
			"max_results": 100,
		})
		if err != nil {
			t.Fatalf("tool_search failed: %v", err)
		}

		var results []SearchResult
		if err := json.Unmarshal([]byte(response.Content[0].Text), &results); err != nil {
			t.Fatalf("Failed to parse results: %v", err)
		}

		t.Logf("Got %d results:", len(results))
		for _, r := range results {
			t.Logf("  - %s (score=%.2f): %s", r.Name, r.Score, r.Description)
		}

		names := make(map[string]SearchResult)
		for _, r := range results {
			names[r.Name] = r
		}

		if _, ok := names["general_knowledge"]; !ok {
			t.Error("general_knowledge (native provider tool) not found")
		}

		if _, ok := names["crm_search"]; !ok {
			t.Error("crm_search (native provider tool) not found")
		}

		if _, ok := names["people_kb"]; !ok {
			t.Error("people_kb (discoverable provider tool) not found")
		}
	})

	t.Run("provider_native_tools_found_by_keyword_search", func(t *testing.T) {
		ctx := WithToolProviders(context.Background(), provider)
		response, err := server.CallTool(ctx, "tool_search", map[string]interface{}{
			"query": "people",
		})
		if err != nil {
			t.Fatalf("tool_search failed: %v", err)
		}

		var results []SearchResult
		if err := json.Unmarshal([]byte(response.Content[0].Text), &results); err != nil {
			t.Fatalf("Failed to parse results: %v", err)
		}

		t.Logf("Search 'people' got %d results:", len(results))
		for _, r := range results {
			t.Logf("  - %s (score=%.2f)", r.Name, r.Score)
		}

		foundGeneral := false
		foundPeopleKb := false
		for _, r := range results {
			if r.Name == "general_knowledge" {
				foundGeneral = true
			}
			if r.Name == "people_kb" {
				foundPeopleKb = true
			}
		}

		if !foundGeneral {
			t.Error("general_knowledge should match 'people' keyword")
		}
		if !foundPeopleKb {
			t.Error("people_kb should match 'people' keyword")
		}
	})

	t.Run("execute_tool_calls_provider_native_tool", func(t *testing.T) {
		ctx := WithToolProviders(context.Background(), provider)
		response, err := server.CallTool(ctx, "execute_tool", map[string]interface{}{
			"name": "general_knowledge",
			"arguments": map[string]interface{}{
				"message": "who is paul?",
			},
		})
		if err != nil {
			t.Fatalf("execute_tool failed: %v", err)
		}

		expected := "Provider result for general_knowledge: who is paul?"
		if response.Content[0].Text != expected {
			t.Errorf("Expected '%s', got '%s'", expected, response.Content[0].Text)
		}
	})

	t.Run("execute_tool_calls_provider_discoverable_tool", func(t *testing.T) {
		ctx := WithToolProviders(context.Background(), provider)
		response, err := server.CallTool(ctx, "execute_tool", map[string]interface{}{
			"name": "people_kb",
			"arguments": map[string]interface{}{
				"message": "who is cindy?",
			},
		})
		if err != nil {
			t.Fatalf("execute_tool failed: %v", err)
		}

		expected := "Provider result for people_kb: who is cindy?"
		if response.Content[0].Text != expected {
			t.Errorf("Expected '%s', got '%s'", expected, response.Content[0].Text)
		}
	})

	t.Run("direct_CallTool_calls_provider_discoverable_tool", func(t *testing.T) {
		ctx := WithToolProviders(context.Background(), provider)
		response, err := server.CallTool(ctx, "people_kb", map[string]interface{}{
			"message": "who is paul?",
		})
		if err != nil {
			t.Fatalf("direct CallTool failed: %v", err)
		}

		expected := "Provider result for people_kb: who is paul?"
		if response.Content[0].Text != expected {
			t.Errorf("Expected '%s', got '%s'", expected, response.Content[0].Text)
		}
	})
}
