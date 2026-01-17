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

// Tests for WithOnDemandToolProviders

func TestWithOnDemandToolProviders(t *testing.T) {
	provider := &mockToolProvider{
		tools: []MCPTool{
			{Name: "ondemand_tool", Description: "An ondemand tool"},
		},
	}

	ctx := context.Background()
	ctx = WithOnDemandToolProviders(ctx, provider)

	providers := GetOnDemandToolProviders(ctx)
	if len(providers) != 1 {
		t.Errorf("expected 1 ondemand provider, got %d", len(providers))
	}
}

func TestWithOnDemandToolProviders_Accumulates(t *testing.T) {
	provider1 := &mockToolProvider{
		tools: []MCPTool{{Name: "tool1"}},
	}
	provider2 := &mockToolProvider{
		tools: []MCPTool{{Name: "tool2"}},
	}

	ctx := context.Background()
	ctx = WithOnDemandToolProviders(ctx, provider1)
	ctx = WithOnDemandToolProviders(ctx, provider2)

	providers := GetOnDemandToolProviders(ctx)
	if len(providers) != 2 {
		t.Errorf("expected 2 ondemand providers, got %d", len(providers))
	}
}

func TestGetOnDemandToolProviders_EmptyContext(t *testing.T) {
	providers := GetOnDemandToolProviders(context.Background())
	if providers != nil {
		t.Error("expected nil for context without ondemand providers")
	}
}

func TestCallToolFromProviders_OnDemandProviders(t *testing.T) {
	nativeProvider := &mockToolProvider{
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "native_tool" {
				return "native result", nil
			}
			return nil, ErrUnknownTool
		},
	}
	ondemandProvider := &mockToolProvider{
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "ondemand_tool" {
				return "ondemand result", nil
			}
			return nil, ErrUnknownTool
		},
	}

	ctx := context.Background()
	ctx = WithToolProviders(ctx, nativeProvider)
	ctx = WithOnDemandToolProviders(ctx, ondemandProvider)

	// Should find native tool
	result, err := callToolFromProviders(ctx, "native_tool", nil)
	if err != nil {
		t.Fatalf("unexpected error for native tool: %v", err)
	}
	if result == nil {
		t.Error("expected result for native tool")
	}

	// Should find ondemand tool
	result, err = callToolFromProviders(ctx, "ondemand_tool", nil)
	if err != nil {
		t.Fatalf("unexpected error for ondemand tool: %v", err)
	}
	if result == nil {
		t.Error("expected result for ondemand tool")
	}

	// Should not find unknown tool
	_, err = callToolFromProviders(ctx, "unknown_tool", nil)
	if err != ErrUnknownTool {
		t.Errorf("expected ErrUnknownTool, got %v", err)
	}
}

func TestOnDemandProviderToolsNotInList(t *testing.T) {
	nativeProvider := &mockToolProvider{
		tools: []MCPTool{{Name: "native_tool", Description: "Native"}},
	}
	ondemandProvider := &mockToolProvider{
		tools: []MCPTool{{Name: "ondemand_tool", Description: "OnDemand"}},
	}

	ctx := context.Background()
	ctx = WithToolProviders(ctx, nativeProvider)
	ctx = WithOnDemandToolProviders(ctx, ondemandProvider)

	seen := make(map[string]bool)
	tools := listToolsFromProviders(ctx, seen)

	// Only native tools should be in the list
	if len(tools) != 1 {
		t.Errorf("expected 1 tool in list, got %d", len(tools))
	}
	if tools[0].Name != "native_tool" {
		t.Errorf("expected native_tool, got %s", tools[0].Name)
	}

	// ondemand_tool should NOT be in seen map from listToolsFromProviders
	// because it's from an ondemand provider
	if seen["ondemand_tool"] {
		t.Error("ondemand_tool should not be in seen map")
	}
}
