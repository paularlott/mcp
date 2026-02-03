package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

// TestNormalModeToolVisibility tests that in normal mode:
// - RegisterTool tools appear in tools/list
// - Discoverable tools do NOT appear in tools/list
// - tool_search and execute_tool appear if discoverable tools exist
func TestNormalModeToolVisibility(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register a native tool
	server.RegisterTool(
		NewTool("native_tool", "A native tool", String("arg", "An argument")),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("native"), nil
		},
		"keyword1", "keyword2",
	)

	// Register a discoverable tool
	server.RegisterTool(
		NewTool("discoverable_tool", "A discoverable tool", String("arg", "An argument")).Discoverable("keyword3", "keyword4"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("discoverable"), nil
		},
	)

	// Get tools in normal mode
	ctx := context.Background()
	tools := server.ListToolsWithContext(ctx)

	// Check that native_tool is in the list
	foundNative := false
	foundDiscoverable := false
	foundToolSearch := false
	foundExecuteTool := false

	for _, tool := range tools {
		switch tool.Name {
		case "native_tool":
			foundNative = true
		case "discoverable_tool":
			foundDiscoverable = true
		case ToolSearchName:
			foundToolSearch = true
		case ExecuteToolName:
			foundExecuteTool = true
		}
	}

	if !foundNative {
		t.Error("native_tool should appear in tools/list in normal mode")
	}
	if foundDiscoverable {
		t.Error("discoverable_tool should NOT appear in tools/list in normal mode")
	}
	if !foundToolSearch {
		t.Error("tool_search should appear in tools/list when discoverable tools exist")
	}
	if !foundExecuteTool {
		t.Error("execute_tool should appear in tools/list when discoverable tools exist")
	}
}

// TestShowAllModeToolVisibility tests that in show-all mode:
// - ALL tools appear in tools/list (both native and discoverable)
// - This is used for MCP server chaining where downstream needs all tools
func TestShowAllModeToolVisibility(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register a native tool with keywords
	server.RegisterTool(
		NewTool("native_tool", "A native tool", String("arg", "An argument")),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("native"), nil
		},
		"keyword1", "keyword2",
	)

	// Register a discoverable tool
	server.RegisterTool(
		NewTool("discoverable_tool", "A discoverable tool", String("arg", "An argument")).Discoverable("keyword3", "keyword4"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("discoverable"), nil
		},
	)

	// Get tools in show-all mode
	ctx := WithShowAllTools(context.Background())
	tools := server.ListToolsWithContext(ctx)

	// Check that ALL tools appear in show-all mode (but not meta-tools)
	// Should have: native_tool, discoverable_tool
	if len(tools) != 2 {
		t.Errorf("Expected 2 tools in show-all mode, got %d", len(tools))
		for _, tool := range tools {
			t.Logf("  - %s", tool.Name)
		}
	}

	foundNative := false
	foundDiscoverable := false
	foundToolSearch := false
	foundExecuteTool := false
	for _, tool := range tools {
		switch tool.Name {
		case "native_tool":
			foundNative = true
		case "discoverable_tool":
			foundDiscoverable = true
		case ToolSearchName:
			foundToolSearch = true
		case ExecuteToolName:
			foundExecuteTool = true
		}
	}

	if !foundNative {
		t.Error("native_tool should appear in tools/list in show-all mode")
	}
	if !foundDiscoverable {
		t.Error("discoverable_tool should appear in tools/list in show-all mode")
	}
	if foundToolSearch {
		t.Error("tool_search should NOT appear in tools/list in show-all mode")
	}
	if foundExecuteTool {
		t.Error("execute_tool should NOT appear in tools/list in show-all mode")
	}
}

// TestToolSearchInNormalMode tests that tool_search only finds discoverable tools in normal mode
func TestToolSearchInNormalMode(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register a native tool with keywords
	server.RegisterTool(
		NewTool("native_tool", "A native tool", String("arg", "An argument")),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("native"), nil
		},
		"keyword1", "keyword2",
	)

	// Register a discoverable tool
	server.RegisterTool(
		NewTool("discoverable_tool", "A discoverable tool", String("arg", "An argument")).Discoverable("keyword3", "keyword4"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("discoverable"), nil
		},
	)

	// Search in normal mode
	ctx := context.Background()
	results := server.internalRegistry.Search(ctx, "", 100)

	// Should only find discoverable_tool, not native_tool
	foundNative := false
	foundDiscoverable := false
	for _, result := range results {
		if result.Name == "native_tool" {
			foundNative = true
		}
		if result.Name == "discoverable_tool" {
			foundDiscoverable = true
		}
	}

	if foundNative {
		t.Error("native_tool should NOT be searchable in normal mode")
	}
	if !foundDiscoverable {
		t.Error("discoverable_tool should be searchable in normal mode")
	}
}

// TestToolSearchInShowAllMode tests that tool_search still only finds discoverable tools
// even in show-all mode (show-all is for listing, not searching)
func TestToolSearchInShowAllMode(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register a native tool with keywords
	server.RegisterTool(
		NewTool("native_tool", "A native tool", String("arg", "An argument")),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("native"), nil
		},
		"keyword1", "keyword2",
	)

	// Register a discoverable tool
	server.RegisterTool(
		NewTool("discoverable_tool", "A discoverable tool", String("arg", "An argument")).Discoverable("keyword3", "keyword4"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("discoverable"), nil
		},
	)

	// Call tool_search in show-all mode
	ctx := WithShowAllTools(context.Background())
	response, err := server.CallTool(ctx, "tool_search", map[string]interface{}{
		"query":       "",
		"max_results": 100,
	})
	if err != nil {
		t.Fatalf("tool_search failed: %v", err)
	}

	// Parse the JSON response
	if len(response.Content) == 0 {
		t.Fatal("No content in response")
	}

	var results []SearchResult
	if err := json.Unmarshal([]byte(response.Content[0].Text), &results); err != nil {
		t.Fatalf("Failed to parse results: %v", err)
	}

	// tool_search only finds discoverable tools, not native tools
	// (native tools are already visible in the tools/list)
	foundNative := false
	foundDiscoverable := false
	for _, result := range results {
		if result.Name == "native_tool" {
			foundNative = true
		}
		if result.Name == "discoverable_tool" {
			foundDiscoverable = true
		}
	}

	if foundNative {
		t.Error("native_tool should NOT be in tool_search results (it's native)")
	}
	if !foundDiscoverable {
		t.Error("discoverable_tool should be searchable via tool_search")
	}
}
