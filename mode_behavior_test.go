package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

// TestNormalModeToolVisibility tests that in normal mode:
// - RegisterTool tools appear in tools/list
// - RegisterOnDemandTool tools do NOT appear in tools/list
// - tool_search and execute_tool appear if ondemand tools exist
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

	// Register an ondemand tool
	server.RegisterOnDemandTool(
		NewTool("ondemand_tool", "An ondemand tool", String("arg", "An argument")),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("ondemand"), nil
		},
		"keyword3", "keyword4",
	)

	// Get tools in normal mode
	ctx := context.Background()
	tools := server.ListToolsWithContext(ctx)

	// Check that native_tool is in the list
	foundNative := false
	foundOnDemand := false
	foundToolSearch := false
	foundExecuteTool := false

	for _, tool := range tools {
		switch tool.Name {
		case "native_tool":
			foundNative = true
		case "ondemand_tool":
			foundOnDemand = true
		case ToolSearchName:
			foundToolSearch = true
		case ExecuteToolName:
			foundExecuteTool = true
		}
	}

	if !foundNative {
		t.Error("native_tool should appear in tools/list in normal mode")
	}
	if foundOnDemand {
		t.Error("ondemand_tool should NOT appear in tools/list in normal mode")
	}
	if !foundToolSearch {
		t.Error("tool_search should appear in tools/list when ondemand tools exist")
	}
	if !foundExecuteTool {
		t.Error("execute_tool should appear in tools/list when ondemand tools exist")
	}
}

// TestForceOnDemandModeToolVisibility tests that in force ondemand mode:
// - Only tool_search and execute_tool appear in tools/list
// - All tools are searchable via tool_search
func TestForceOnDemandModeToolVisibility(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register a native tool with keywords
	server.RegisterTool(
		NewTool("native_tool", "A native tool", String("arg", "An argument")),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("native"), nil
		},
		"keyword1", "keyword2",
	)

	// Register an ondemand tool
	server.RegisterOnDemandTool(
		NewTool("ondemand_tool", "An ondemand tool", String("arg", "An argument")),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("ondemand"), nil
		},
		"keyword3", "keyword4",
	)

	// Get tools in force ondemand mode
	ctx := WithForceOnDemandMode(context.Background())
	tools := server.ListToolsWithContext(ctx)

	// Check that only tool_search and execute_tool are in the list
	if len(tools) != 2 {
		t.Errorf("Expected 2 tools in force ondemand mode, got %d", len(tools))
	}

	foundToolSearch := false
	foundExecuteTool := false
	for _, tool := range tools {
		if tool.Name == ToolSearchName {
			foundToolSearch = true
		}
		if tool.Name == ExecuteToolName {
			foundExecuteTool = true
		}
		if tool.Name != ToolSearchName && tool.Name != ExecuteToolName {
			t.Errorf("Unexpected tool in force ondemand mode: %s", tool.Name)
		}
	}

	if !foundToolSearch {
		t.Error("tool_search should appear in tools/list in force ondemand mode")
	}
	if !foundExecuteTool {
		t.Error("execute_tool should appear in tools/list in force ondemand mode")
	}
}

// TestToolSearchInNormalMode tests that tool_search only finds ondemand tools in normal mode
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

	// Register an ondemand tool
	server.RegisterOnDemandTool(
		NewTool("ondemand_tool", "An ondemand tool", String("arg", "An argument")),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("ondemand"), nil
		},
		"keyword3", "keyword4",
	)

	// Search in normal mode
	ctx := context.Background()
	results := server.internalRegistry.Search(ctx, "", 100)

	// Should only find ondemand_tool, not native_tool
	foundNative := false
	foundOnDemand := false
	for _, result := range results {
		if result.Name == "native_tool" {
			foundNative = true
		}
		if result.Name == "ondemand_tool" {
			foundOnDemand = true
		}
	}

	if foundNative {
		t.Error("native_tool should NOT be searchable in normal mode")
	}
	if !foundOnDemand {
		t.Error("ondemand_tool should be searchable in normal mode")
	}
}

// TestToolSearchInForceOnDemandMode tests that tool_search finds all tools in force ondemand mode
func TestToolSearchInForceOnDemandMode(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register a native tool with keywords
	server.RegisterTool(
		NewTool("native_tool", "A native tool", String("arg", "An argument")),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("native"), nil
		},
		"keyword1", "keyword2",
	)

	// Register an ondemand tool
	server.RegisterOnDemandTool(
		NewTool("ondemand_tool", "An ondemand tool", String("arg", "An argument")),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("ondemand"), nil
		},
		"keyword3", "keyword4",
	)

	// Call tool_search in force ondemand mode
	ctx := WithForceOnDemandMode(context.Background())
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

	// Should find both native_tool and ondemand_tool
	foundNative := false
	foundOnDemand := false
	for _, result := range results {
		if result.Name == "native_tool" {
			foundNative = true
		}
		if result.Name == "ondemand_tool" {
			foundOnDemand = true
		}
	}

	if !foundNative {
		t.Error("native_tool should be searchable in force ondemand mode")
	}
	if !foundOnDemand {
		t.Error("ondemand_tool should be searchable in force ondemand mode")
	}
}
