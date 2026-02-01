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

// TestShowAllModeWithProvidersAttached tests that show-all mode works when providers are attached before HandleRequest
// This mimics what llmrouter does - it attaches providers then calls MCP server's HandleRequest
func TestShowAllModeWithProvidersAttached(t *testing.T) {
	server := NewServer("test", "1.0.0")
	// NOTE: No session management enabled

	// Register a builtin tool (like llmrouter's execute_code)
	server.RegisterTool(
		NewTool("execute_code", "Execute code"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
	)

	// Register a discoverable tool so discovery meta-tools exist
	server.RegisterTool(
		NewTool("discoverable_tool", "A discoverable tool").Discoverable("test"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
	)

	// Create a mock provider with a native tool
	provider := &mockProviderForDiscovery{
		tools: []MCPTool{
			{Name: "native_tool", Description: "A native tool", Visibility: ToolVisibilityNative},
		},
	}

	// List tools with show-all mode header AND provider attached (like llmrouter)
	listBody := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(listBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")
	req.Header.Set(ShowAllHeader, "true")

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

	// In show-all mode, ALL tools should be visible
	hasNativeTool := false
	hasExecuteCode := false
	hasDiscoverableTool := false
	hasToolSearch := false
	hasExecuteTool := false
	for _, tool := range listResp.Result.Tools {
		switch tool.Name {
		case "native_tool":
			hasNativeTool = true
		case "execute_code":
			hasExecuteCode = true
		case "discoverable_tool":
			hasDiscoverableTool = true
		case ToolSearchName:
			hasToolSearch = true
		case ExecuteToolName:
			hasExecuteTool = true
		}
	}

	if !hasNativeTool {
		t.Error("native_tool should be visible in show-all mode (from provider)")
	}
	if !hasExecuteCode {
		t.Error("execute_code should be visible in show-all mode (from server)")
	}
	if !hasDiscoverableTool {
		t.Error("discoverable_tool should be visible in show-all mode")
	}
	if hasToolSearch {
		t.Error("tool_search should NOT be visible in show-all mode")
	}
	if hasExecuteTool {
		t.Error("execute_tool should NOT be visible in show-all mode")
	}

	// Should have 3 tools: native_tool, execute_code, discoverable_tool (meta-tools excluded)
	if len(listResp.Result.Tools) != 3 {
		t.Errorf("Expected 3 tools in show-all mode, got %d", len(listResp.Result.Tools))
	}
}

