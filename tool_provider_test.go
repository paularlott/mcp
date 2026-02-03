package mcp

import (
	"context"
	"testing"
)

// mockToolProvider is a test implementation of ToolProvider
type mockToolProvider struct {
	tools []MCPTool
	execFunc func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error)
}

func (m *mockToolProvider) GetTools(ctx context.Context) ([]MCPTool, error) {
	return m.tools, nil
}

func (m *mockToolProvider) ExecuteTool(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
	if m.execFunc != nil {
		return m.execFunc(ctx, name, params)
	}
	return nil, nil
}

func TestWithToolProviders(t *testing.T) {
	provider := &mockToolProvider{
		tools: []MCPTool{
			{Name: "test_tool", Description: "A test tool"},
		},
	}

	ctx := context.Background()
	ctx = WithToolProviders(ctx, provider)

	providers := GetToolProviders(ctx)
	if len(providers) != 1 {
		t.Errorf("expected 1 provider, got %d", len(providers))
	}
}

func TestGetToolProviders_EmptyContext(t *testing.T) {
	// Test with empty context (no providers set)
	providers := GetToolProviders(context.TODO())
	if providers != nil {
		t.Error("expected nil providers for empty context")
	}
}

func TestGetToolProviders_NoProviders(t *testing.T) {
	ctx := context.Background()
	providers := GetToolProviders(ctx)
	if providers != nil {
		t.Error("expected nil providers for empty context")
	}
}

func TestListToolsFromProviders(t *testing.T) {
	provider1 := &mockToolProvider{
		tools: []MCPTool{
			{Name: "tool1", Description: "First tool", Keywords: []string{"keyword1"}},
		},
	}
	provider2 := &mockToolProvider{
		tools: []MCPTool{
			{Name: "tool2", Description: "Second tool", Keywords: []string{"keyword2"}},
		},
	}

	ctx := context.Background()
	ctx = WithToolProviders(ctx, provider1, provider2)

	seen := make(map[string]bool)
	tools := listToolsFromProviders(ctx, seen)
	if len(tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].Name != "tool1" || tools[1].Name != "tool2" {
		t.Error("tools not returned in expected order")
	}
	// Verify seen map was updated
	if !seen["tool1"] || !seen["tool2"] {
		t.Error("seen map was not updated")
	}
}

func TestListToolsFromProviders_Deduplication(t *testing.T) {
	provider := &mockToolProvider{
		tools: []MCPTool{
			{Name: "tool1", Description: "First tool"},
		},
	}

	ctx := context.Background()
	ctx = WithToolProviders(ctx, provider)

	// Pre-populate seen map
	seen := map[string]bool{"tool1": true}
	tools := listToolsFromProviders(ctx, seen)
	if len(tools) != 0 {
		t.Errorf("expected 0 tools (already seen), got %d", len(tools))
	}
}

func TestCallToolFromProviders(t *testing.T) {
	provider := &mockToolProvider{
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "test_tool" {
				return map[string]interface{}{"result": "success"}, nil
			}
			return nil, nil
		},
	}

	ctx := context.Background()
	ctx = WithToolProviders(ctx, provider)

	result, err := callToolFromProviders(ctx, "test_tool", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Error("expected non-nil result")
	}
}

func TestCallToolFromProviders_NotHandled(t *testing.T) {
	provider := &mockToolProvider{
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			return nil, nil // Not handled
		},
	}

	ctx := context.Background()
	ctx = WithToolProviders(ctx, provider)

	_, err := callToolFromProviders(ctx, "unknown_tool", nil)
	if err != ErrUnknownTool {
		t.Errorf("expected ErrUnknownTool, got %v", err)
	}
}

func TestCallToolFromProviders_NoProviders(t *testing.T) {
	ctx := context.Background()
	_, err := callToolFromProviders(ctx, "test_tool", nil)
	if err != ErrUnknownTool {
		t.Errorf("expected ErrUnknownTool, got %v", err)
	}
}

// Tests for tool visibility filtering

func TestToolVisibilityFiltering_NativeToolsInList(t *testing.T) {
	provider := &mockToolProvider{
		tools: []MCPTool{
			{Name: "native_tool", Description: "A native tool", Visibility: ToolVisibilityNative},
			{Name: "discoverable_tool", Description: "A discoverable tool", Visibility: ToolVisibilityDiscoverable},
		},
	}

	ctx := context.Background()
	ctx = WithToolProviders(ctx, provider)

	seen := make(map[string]bool)
	tools := listToolsFromProviders(ctx, seen)

	// Only native tools should be in the list
	if len(tools) != 1 {
		t.Errorf("expected 1 tool in list, got %d", len(tools))
	}
	if tools[0].Name != "native_tool" {
		t.Errorf("expected native_tool, got %s", tools[0].Name)
	}
}

func TestToolVisibilityFiltering_ShowAllMode(t *testing.T) {
	provider := &mockToolProvider{
		tools: []MCPTool{
			{Name: "native_tool", Description: "A native tool", Visibility: ToolVisibilityNative},
			{Name: "discoverable_tool", Description: "A discoverable tool", Visibility: ToolVisibilityDiscoverable},
		},
	}

	ctx := context.Background()
	ctx = WithToolProviders(ctx, provider)
	ctx = WithShowAllTools(ctx)

	seen := make(map[string]bool)
	tools := listToolsFromProviders(ctx, seen)

	// Both tools should be in the list in show-all mode
	if len(tools) != 2 {
		t.Errorf("expected 2 tools in list, got %d", len(tools))
	}
}

func TestCallToolFromProviders_BothVisibilities(t *testing.T) {
	provider := &mockToolProvider{
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "native_tool" {
				return "native result", nil
			}
			if name == "discoverable_tool" {
				return "discoverable result", nil
			}
			return nil, ErrUnknownTool
		},
	}

	ctx := context.Background()
	ctx = WithToolProviders(ctx, provider)

	// Should find native tool
	result, err := callToolFromProviders(ctx, "native_tool", nil)
	if err != nil {
		t.Fatalf("unexpected error for native tool: %v", err)
	}
	if result == nil {
		t.Error("expected result for native tool")
	}

	// Should also find discoverable tool (execute works for both)
	result, err = callToolFromProviders(ctx, "discoverable_tool", nil)
	if err != nil {
		t.Fatalf("unexpected error for discoverable tool: %v", err)
	}
	if result == nil {
		t.Error("expected result for discoverable tool")
	}

	// Should not find unknown tool
	_, err = callToolFromProviders(ctx, "unknown_tool", nil)
	if err != ErrUnknownTool {
		t.Errorf("expected ErrUnknownTool, got %v", err)
	}
}

func TestHasDiscoverableToolsFromProviders(t *testing.T) {
	providerWithDiscoverable := &mockToolProvider{
		tools: []MCPTool{
			{Name: "native_tool", Visibility: ToolVisibilityNative},
			{Name: "discoverable_tool", Visibility: ToolVisibilityDiscoverable},
		},
	}
	providerWithoutDiscoverable := &mockToolProvider{
		tools: []MCPTool{
			{Name: "native_only", Visibility: ToolVisibilityNative},
		},
	}

	// With discoverable tools
	ctx := WithToolProviders(context.Background(), providerWithDiscoverable)
	if !hasDiscoverableToolsFromProviders(ctx) {
		t.Error("expected to have discoverable tools")
	}

	// Without discoverable tools
	ctx = WithToolProviders(context.Background(), providerWithoutDiscoverable)
	if hasDiscoverableToolsFromProviders(ctx) {
		t.Error("expected no discoverable tools")
	}
}

func TestGetDiscoverableToolsFromProviders(t *testing.T) {
	provider := &mockToolProvider{
		tools: []MCPTool{
			{Name: "native_tool", Visibility: ToolVisibilityNative},
			{Name: "discoverable1", Visibility: ToolVisibilityDiscoverable, Keywords: []string{"keyword1"}},
			{Name: "discoverable2", Visibility: ToolVisibilityDiscoverable, Keywords: []string{"keyword2"}},
		},
	}

	ctx := WithToolProviders(context.Background(), provider)
	tools := getDiscoverableToolsFromProviders(ctx)

	if len(tools) != 2 {
		t.Errorf("expected 2 discoverable tools, got %d", len(tools))
	}
}
