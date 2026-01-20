package mcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockProviderForDiscovery is a mock provider for testing
type mockProviderForDiscovery struct {
	tools []MCPTool
}

func (p *mockProviderForDiscovery) GetTools(ctx context.Context) ([]MCPTool, error) {
	return p.tools, nil
}

func (p *mockProviderForDiscovery) ExecuteTool(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
	return nil, ErrUnknownTool
}

// TestDiscoveryModeWithProvidersAttached tests that discovery mode works when providers are attached before HandleRequest
// This mimics what llmrouter does - it attaches providers then calls MCP server's HandleRequest
func TestDiscoveryModeWithProvidersAttached(t *testing.T) {
	server := NewServer("test", "1.0.0")
	// NOTE: No session management enabled

	// Register a builtin tool (like llmrouter's execute_code)
	server.RegisterTool(
		NewTool("execute_code", "Execute code"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
	)

	// Create a mock provider
	provider := &mockProviderForDiscovery{
		tools: []MCPTool{
			{Name: "native_tool", Description: "A native tool"},
		},
	}

	// List tools with discovery mode header AND provider attached (like llmrouter)
	listBody := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(listBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")
	req.Header.Set(ToolModeHeader, "discovery")

	// Attach provider BEFORE calling HandleRequest (like llmrouter does)
	ctx := WithToolProviders(req.Context(), provider)

	w := httptest.NewRecorder()
	server.HandleRequest(w, req.WithContext(ctx))

	if w.Code != 200 {
		t.Fatalf("List tools failed: %d - %s", w.Code, w.Body.String())
	}

	var listResp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	json.NewDecoder(w.Body).Decode(&listResp)

	t.Logf("Tools returned: %v", listResp.Result.Tools)

	// Should only see discovery tools
	for _, tool := range listResp.Result.Tools {
		if tool.Name == "native_tool" {
			t.Error("native_tool should NOT be visible in discovery mode (from provider)")
		}
		if tool.Name == "execute_code" {
			t.Error("execute_code should NOT be visible in discovery mode (from server)")
		}
	}

	// Should see tool_search and execute_tool
	hasToolSearch := false
	hasExecuteTool := false
	for _, tool := range listResp.Result.Tools {
		if tool.Name == ToolSearchName {
			hasToolSearch = true
		}
		if tool.Name == ExecuteToolName {
			hasExecuteTool = true
		}
	}
	if !hasToolSearch {
		t.Error("tool_search should be visible in discovery mode")
	}
	if !hasExecuteTool {
		t.Error("execute_tool should be visible in discovery mode")
	}

	// Should have exactly 2 tools
	if len(listResp.Result.Tools) != 2 {
		t.Errorf("Expected 2 tools in discovery mode, got %d", len(listResp.Result.Tools))
	}
}
