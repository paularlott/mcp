package mcp

import (
	"context"
	"testing"
)

// TestAddRemoveProvidersToNativeContext tests that providers can be dynamically added and removed
// to a native (non-discoverable) context
func TestAddRemoveProvidersToNativeContext(t *testing.T) {
	// Create base context without providers
	ctx := context.Background()

	// Create first provider
	provider1 := &mockToolProvider{
		tools: []MCPTool{
			{Name: "tool1", Description: "From provider 1"},
		},
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "tool1" {
				return "result1", nil
			}
			return nil, nil
		},
	}

	// Add provider1 to context
	ctx1 := WithToolProviders(ctx, provider1)
	providers := GetToolProviders(ctx1)
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider after adding provider1, got %d", len(providers))
	}

	// Create second provider
	provider2 := &mockToolProvider{
		tools: []MCPTool{
			{Name: "tool2", Description: "From provider 2"},
		},
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "tool2" {
				return "result2", nil
			}
			return nil, nil
		},
	}

	// Add both providers to a new context
	ctx2 := WithToolProviders(ctx, provider1, provider2)
	providers = GetToolProviders(ctx2)
	if len(providers) != 2 {
		t.Fatalf("expected 2 providers after adding both, got %d", len(providers))
	}

	// Verify each provider's tools are in the context
	seen := make(map[string]bool)
	tools := listToolsFromProviders(ctx2, seen)
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools from both providers, got %d", len(tools))
	}

	// Verify tool names
	if !seen["tool1"] || !seen["tool2"] {
		t.Error("both tool1 and tool2 should be in seen map")
	}

	// "Remove" provider2 by creating new context with only provider1
	ctx3 := WithToolProviders(ctx, provider1)
	providers = GetToolProviders(ctx3)
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider after removing provider2, got %d", len(providers))
	}

	seen = make(map[string]bool)
	tools = listToolsFromProviders(ctx3, seen)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool after removing provider2, got %d", len(tools))
	}
	if !seen["tool1"] {
		t.Error("tool1 should still be in seen map")
	}
	if seen["tool2"] {
		t.Error("tool2 should not be in seen map after removal")
	}
}

// TestAddRemoveProvidersToDiscoverableContext tests that providers work correctly with show-all mode
func TestAddRemoveProvidersToDiscoverableContext(t *testing.T) {
	ctx := context.Background()

	provider := &mockToolProvider{
		tools: []MCPTool{
			{Name: "demand_tool", Description: "A tool for discoverable mode", Keywords: []string{"demand"}},
		},
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "demand_tool" {
				return "demand_result", nil
			}
			return nil, nil
		},
	}

	// Add provider to context and enable show-all mode
	ctx = WithToolProviders(ctx, provider)
	ctxShowAll := WithShowAllTools(ctx)

	// In show-all mode, provider tools should be visible (not hidden)
	seen := make(map[string]bool)
	tools := listToolsFromProviders(ctxShowAll, seen)
	if len(tools) != 1 {
		t.Errorf("provider tools should be visible in show-all mode, got %d tools", len(tools))
	}

	// Provider should still be retrievable and executable
	providers := GetToolProviders(ctxShowAll)
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(providers))
	}

	// Provider tool should be callable
	result, err := callToolFromProviders(ctxShowAll, "demand_tool", nil)
	if err != nil {
		t.Fatalf("failed to call provider tool: %v", err)
	}
	if result == nil {
		t.Error("expected non-nil result")
	}

	// Verify show-all mode is set
	showAll := GetShowAllTools(ctxShowAll)
	if !showAll {
		t.Errorf("expected true, got %v", showAll)
	}
}

// TestMultipleProvidersDeduplication tests that tools from different providers are properly deduplicated
func TestMultipleProvidersDeduplication(t *testing.T) {
	ctx := context.Background()

	// Provider 1 and 2 both provide a tool named "shared_tool"
	provider1 := &mockToolProvider{
		tools: []MCPTool{
			{Name: "shared_tool", Description: "From provider 1"},
			{Name: "unique1", Description: "Only in provider 1"},
		},
	}

	provider2 := &mockToolProvider{
		tools: []MCPTool{
			{Name: "shared_tool", Description: "From provider 2 (should be skipped)"},
			{Name: "unique2", Description: "Only in provider 2"},
		},
	}

	// Add both providers
	ctxBoth := WithToolProviders(ctx, provider1, provider2)
	seen := make(map[string]bool)
	tools := listToolsFromProviders(ctxBoth, seen)

	// Should get 3 tools: shared_tool (from provider1), unique1, unique2
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools))
	}

	// shared_tool should come from provider1 (first one added)
	if tools[0].Name != "shared_tool" || tools[0].Description != "From provider 1" {
		t.Error("shared_tool should come from provider1")
	}

	// Verify all expected tools are present
	expected := map[string]bool{"shared_tool": true, "unique1": true, "unique2": true}
	for _, tool := range tools {
		if !expected[tool.Name] {
			t.Errorf("unexpected tool: %s", tool.Name)
		}
	}
}

// TestProviderToolExecution tests that tools from providers can be executed correctly
func TestProviderToolExecution(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Create a provider with an executable tool
	provider := &mockToolProvider{
		tools: []MCPTool{
			{Name: "provider_calc", Description: "Calculate something"},
		},
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "provider_calc" {
				// Return a structured result
				return map[string]interface{}{
					"result": 42,
					"status": "success",
				}, nil
			}
			return nil, nil
		},
	}

	// Test in native mode
	ctx := WithToolProviders(context.Background(), provider)
	resp, err := server.CallTool(ctx, "provider_calc", nil)
	if err != nil {
		t.Fatalf("failed to call provider tool: %v", err)
	}
	if resp == nil {
		t.Error("expected non-nil response")
	}
	if len(resp.Content) == 0 {
		t.Error("expected content in response")
	}
}

// TestProviderToolNotFound tests error handling when tool is not found in providers
func TestProviderToolNotFound(t *testing.T) {
	ctx := context.Background()

	provider := &mockToolProvider{
		tools: []MCPTool{
			{Name: "existing_tool", Description: "Exists"},
		},
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "existing_tool" {
				return "result", nil
			}
			return nil, nil // Tool not handled
		},
	}

	ctxWithProvider := WithToolProviders(ctx, provider)

	// Try to call non-existent tool
	_, err := callToolFromProviders(ctxWithProvider, "nonexistent", nil)
	if err != ErrUnknownTool {
		t.Errorf("expected ErrUnknownTool, got %v", err)
	}
}

// TestProviderErrorPropagation tests that provider errors are properly propagated
func TestProviderErrorPropagation(t *testing.T) {
	ctx := context.Background()

	testError := NewToolErrorInvalidParams("test error")

	provider := &mockToolProvider{
		tools: []MCPTool{
			{Name: "error_tool", Description: "Throws error"},
		},
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "error_tool" {
				return nil, testError
			}
			return nil, nil
		},
	}

	ctxWithProvider := WithToolProviders(ctx, provider)

	// Try to call error tool
	_, err := callToolFromProviders(ctxWithProvider, "error_tool", nil)
	if err != testError {
		t.Errorf("expected error from provider to be propagated, got %v", err)
	}
}

// TestProviderToolVisibilityNativeMode tests tool visibility in native mode
func TestProviderToolVisibilityNativeMode(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register a native tool
	server.RegisterTool(
		NewTool("native_tool", "A native tool"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("native"), nil
		},
	)

	// Create provider with dynamic tool
	provider := &mockToolProvider{
		tools: []MCPTool{
			{Name: "provider_tool", Description: "From provider"},
		},
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "provider_tool" {
				return "provider", nil
			}
			return nil, nil
		},
	}

	// List tools in native mode with provider
	ctx := WithToolProviders(context.Background(), provider)
	tools := server.ListToolsWithContext(ctx)

	// Both native_tool and provider_tool should appear
	found := make(map[string]bool)
	for _, tool := range tools {
		found[tool.Name] = true
	}

	if !found["native_tool"] {
		t.Error("native_tool should be in tools list")
	}
	if !found["provider_tool"] {
		t.Error("provider_tool should be in tools list")
	}

	// tool_search and execute_tool should NOT appear in native mode (no discoverable tools)
	if found[ToolSearchName] {
		t.Error("tool_search should not appear in native mode without discoverable tools")
	}
	if found[ExecuteToolName] {
		t.Error("execute_tool should not appear in native mode without discoverable tools")
	}
}

// TestProviderToolVisibilityShowAllMode tests tool visibility in show-all mode
func TestProviderToolVisibilityShowAllMode(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register a native tool
	server.RegisterTool(
		NewTool("native_tool", "A native tool"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("native"), nil
		},
		"search", "keyword",
	)

	// Register a discoverable tool so discovery meta-tools exist
	server.RegisterTool(
		NewTool("discoverable_tool", "A discoverable tool").Discoverable("discoverable"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("discoverable"), nil
		},
	)

	// Create provider with dynamic tool
	provider := &mockToolProvider{
		tools: []MCPTool{
			{Name: "provider_tool", Description: "From provider", Keywords: []string{"provider"}},
		},
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "provider_tool" {
				return "provider", nil
			}
			return nil, nil
		},
	}

	// List tools in show-all mode - should have ALL tools (but not meta-tools)
	ctx := WithToolProviders(context.Background(), provider)
	ctx = WithShowAllTools(ctx)
	tools := server.ListToolsWithContext(ctx)

	// Should have: native_tool, discoverable_tool, provider_tool (meta-tools excluded)
	if len(tools) != 3 {
		t.Errorf("expected 3 tools in show-all mode, got %d", len(tools))
		for _, tool := range tools {
			t.Logf("  - %s", tool.Name)
		}
	}

	found := make(map[string]bool)
	for _, tool := range tools {
		found[tool.Name] = true
	}

	if !found["native_tool"] {
		t.Error("native_tool should appear in show-all mode")
	}
	if !found["discoverable_tool"] {
		t.Error("discoverable_tool should appear in show-all mode")
	}
	if !found["provider_tool"] {
		t.Error("provider_tool should appear in show-all mode")
	}
	if found[ToolSearchName] {
		t.Error("tool_search should NOT appear in show-all mode")
	}
	if found[ExecuteToolName] {
		t.Error("execute_tool should NOT appear in show-all mode")
	}
}

// TestMultipleContextsIsolation tests that providers from different contexts don't interfere
func TestMultipleContextsIsolation(t *testing.T) {
	provider1 := &mockToolProvider{
		tools: []MCPTool{
			{Name: "provider1_tool", Description: "From provider 1"},
		},
	}

	provider2 := &mockToolProvider{
		tools: []MCPTool{
			{Name: "provider2_tool", Description: "From provider 2"},
		},
	}

	ctx1 := WithToolProviders(context.Background(), provider1)
	ctx2 := WithToolProviders(context.Background(), provider2)

	// Context 1 should only have provider1's tools
	providers1 := GetToolProviders(ctx1)
	if len(providers1) != 1 {
		t.Fatalf("ctx1 should have 1 provider, got %d", len(providers1))
	}

	seen1 := make(map[string]bool)
	tools1 := listToolsFromProviders(ctx1, seen1)
	if len(tools1) != 1 || !seen1["provider1_tool"] {
		t.Error("ctx1 should only have provider1_tool")
	}

	// Context 2 should only have provider2's tools
	providers2 := GetToolProviders(ctx2)
	if len(providers2) != 1 {
		t.Fatalf("ctx2 should have 1 provider, got %d", len(providers2))
	}

	seen2 := make(map[string]bool)
	tools2 := listToolsFromProviders(ctx2, seen2)
	if len(tools2) != 1 || !seen2["provider2_tool"] {
		t.Error("ctx2 should only have provider2_tool")
	}

	// Verify isolation - tools from provider2 are not in ctx1
	if seen1["provider2_tool"] {
		t.Error("ctx1 should not have tools from provider2")
	}

	// Verify isolation - tools from provider1 are not in ctx2
	if seen2["provider1_tool"] {
		t.Error("ctx2 should not have tools from provider1")
	}
}

// TestProviderWithoutContext tests that providers without context don't affect operations
func TestProviderWithoutContext(t *testing.T) {
	ctx := context.Background() // Empty context, no providers

	// Should get nil providers
	providers := GetToolProviders(ctx)
	if providers != nil {
		t.Error("should get nil providers from empty context")
	}

	// listToolsFromProviders should return nil
	seen := make(map[string]bool)
	tools := listToolsFromProviders(ctx, seen)
	if tools != nil {
		t.Error("should get nil tools from context without providers")
	}

	// callToolFromProviders should return ErrUnknownTool
	_, err := callToolFromProviders(ctx, "any_tool", nil)
	if err != ErrUnknownTool {
		t.Errorf("expected ErrUnknownTool, got %v", err)
	}
}

// TestNestedContexts tests that providers accumulate when nested
func TestNestedContexts(t *testing.T) {
	provider1 := &mockToolProvider{
		tools: []MCPTool{
			{Name: "tool1", Description: "From provider 1"},
		},
	}

	provider2 := &mockToolProvider{
		tools: []MCPTool{
			{Name: "tool2", Description: "From provider 2"},
		},
	}

	provider3 := &mockToolProvider{
		tools: []MCPTool{
			{Name: "tool3", Description: "From provider 3"},
		},
	}

	// Create nested contexts - providers accumulate
	ctx1 := WithToolProviders(context.Background(), provider1)
	ctx2 := WithToolProviders(ctx1, provider2) // Adds to ctx1
	ctx3 := WithToolProviders(ctx2, provider3) // Adds to ctx2

	// ctx3 should have all three providers
	providers := GetToolProviders(ctx3)
	if len(providers) != 3 {
		t.Fatalf("ctx3 should have exactly 3 providers, got %d", len(providers))
	}

	seen := make(map[string]bool)
	tools := listToolsFromProviders(ctx3, seen)
	if len(tools) != 3 {
		t.Errorf("ctx3 should have 3 tools, got %d", len(tools))
	}
	if !seen["tool1"] || !seen["tool2"] || !seen["tool3"] {
		t.Error("ctx3 should have tool1, tool2, and tool3")
	}

	// ctx2 should have 2 providers
	providers2 := GetToolProviders(ctx2)
	if len(providers2) != 2 {
		t.Fatalf("ctx2 should have exactly 2 providers, got %d", len(providers2))
	}

	seen2 := make(map[string]bool)
	tools2 := listToolsFromProviders(ctx2, seen2)
	if len(tools2) != 2 {
		t.Errorf("ctx2 should have 2 tools, got %d", len(tools2))
	}
	if !seen2["tool1"] || !seen2["tool2"] {
		t.Error("ctx2 should have tool1 and tool2")
	}
}

// TestProviderToolWithParameters tests that provider tools can receive and process parameters
func TestProviderToolWithParameters(t *testing.T) {
	ctx := context.Background()

	provider := &mockToolProvider{
		tools: []MCPTool{
			{Name: "add", Description: "Add two numbers", Keywords: []string{"math"}},
		},
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "add" {
				a, ok := params["a"].(float64)
				if !ok {
					return nil, NewToolErrorInvalidParams("'a' must be a number")
				}
				b, ok := params["b"].(float64)
				if !ok {
					return nil, NewToolErrorInvalidParams("'b' must be a number")
				}
				return map[string]interface{}{
					"result": a + b,
				}, nil
			}
			return nil, nil
		},
	}

	ctxWithProvider := WithToolProviders(ctx, provider)

	// Call with parameters
	params := map[string]interface{}{
		"a": 5.0,
		"b": 3.0,
	}

	result, err := callToolFromProviders(ctxWithProvider, "add", params)
	if err != nil {
		t.Fatalf("failed to call provider tool with parameters: %v", err)
	}
	if result == nil {
		t.Error("expected non-nil result")
	}
}

// TestDynamicToolVisibility tests that tools with different visibility settings
// behave correctly in normal mode and show-all mode.
func TestDynamicToolVisibility(t *testing.T) {
	server := NewServer("visibility-test", "1.0.0")

	// Create a provider with mixed visibility tools
	provider := &mockToolProvider{
		tools: []MCPTool{
			{
				Name:        "native_tool",
				Description: "A native tool visible in tools/list",
				Keywords:    []string{"native"},
				Visibility:  ToolVisibilityNative,
			},
			{
				Name:        "discoverable_tool",
				Description: "A discoverable tool only available via search",
				Keywords:    []string{"discoverable"},
				Visibility:  ToolVisibilityDiscoverable,
			},
		},
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "native_tool" {
				return "native result", nil
			}
			if name == "discoverable_tool" {
				return "discoverable result", nil
			}
			return nil, nil
		},
	}

	// Register a server-side discoverable tool to enable discovery meta-tools
	server.RegisterTool(
		NewTool("server_discoverable", "Server discoverable").Discoverable("server"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
	)

	// ============================================================================
	// NORMAL MODE: Native tools visible, discoverable hidden
	// ============================================================================
	ctx := WithToolProviders(context.Background(), provider)
	tools := server.ListToolsWithContext(ctx)

	foundNative := false
	foundDiscoverable := false
	foundToolSearch := false
	for _, tool := range tools {
		switch tool.Name {
		case "native_tool":
			foundNative = true
		case "discoverable_tool":
			foundDiscoverable = true
		case ToolSearchName:
			foundToolSearch = true
		}
	}

	if !foundNative {
		t.Error("Normal mode: native_tool should be visible in tools/list")
	}
	if foundDiscoverable {
		t.Error("Normal mode: discoverable_tool should NOT be in tools/list")
	}
	if !foundToolSearch {
		t.Error("Normal mode: tool_search should be available")
	}

	// Both tools should still be callable
	resp, err := server.CallTool(ctx, "native_tool", nil)
	if err != nil || resp == nil {
		t.Error("Normal mode: native_tool should be callable")
	}

	resp, err = server.CallTool(ctx, "discoverable_tool", nil)
	if err != nil || resp == nil {
		t.Error("Normal mode: discoverable_tool should be callable")
	}

	// ============================================================================
	// SHOW-ALL MODE: All tools visible
	// ============================================================================
	showAllCtx := WithToolProviders(context.Background(), provider)
	showAllCtx = WithShowAllTools(showAllCtx)
	showAllTools := server.ListToolsWithContext(showAllCtx)

	foundNativeShowAll := false
	foundDiscoverableShowAll := false
	for _, tool := range showAllTools {
		switch tool.Name {
		case "native_tool":
			foundNativeShowAll = true
		case "discoverable_tool":
			foundDiscoverableShowAll = true
		}
	}

	if !foundNativeShowAll {
		t.Error("Show-all mode: native_tool should be visible")
	}
	if !foundDiscoverableShowAll {
		t.Error("Show-all mode: discoverable_tool should be visible")
	}
}

// TestDynamicToolStateTransition_PerRequest demonstrates that different requests
// can have different visibility modes simultaneously
func TestDynamicToolStateTransition_PerRequest(t *testing.T) {
	server := NewServer("per-request-test", "1.0.0")

	provider := &mockToolProvider{
		tools: []MCPTool{
			{Name: "admin_tools", Description: "Admin functionality", Visibility: ToolVisibilityNative},
			{Name: "hidden_tools", Description: "Hidden functionality", Visibility: ToolVisibilityDiscoverable, Keywords: []string{"hidden"}},
		},
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "admin_tools" {
				return "admin result", nil
			}
			if name == "hidden_tools" {
				return "hidden result", nil
			}
			return nil, nil
		},
	}

	// Need a discoverable tool for tool_search to exist
	server.RegisterTool(
		NewTool("dummy", "Dummy").Discoverable(),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
	)

	// Request 1: Normal mode (native tools visible, discoverable hidden)
	ctxNormal := WithToolProviders(context.Background(), provider)

	// Request 2: Show-all mode (all tools visible)
	ctxShowAll := WithToolProviders(context.Background(), provider)
	ctxShowAll = WithShowAllTools(ctxShowAll)

	// Request 1 (Normal) should see only native tools
	toolsNormal := server.ListToolsWithContext(ctxNormal)
	foundAdmin := false
	foundHidden := false
	for _, tool := range toolsNormal {
		if tool.Name == "admin_tools" {
			foundAdmin = true
		}
		if tool.Name == "hidden_tools" {
			foundHidden = true
		}
	}
	if !foundAdmin {
		t.Error("Normal request should see admin_tools")
	}
	if foundHidden {
		t.Error("Normal request should NOT see hidden_tools in list")
	}

	// Request 2 (Show-all) should see ALL tools
	toolsShowAll := server.ListToolsWithContext(ctxShowAll)
	foundAdminShowAll := false
	foundHiddenShowAll := false
	for _, tool := range toolsShowAll {
		if tool.Name == "admin_tools" {
			foundAdminShowAll = true
		}
		if tool.Name == "hidden_tools" {
			foundHiddenShowAll = true
		}
	}
	if !foundAdminShowAll {
		t.Error("Show-all request should see admin_tools")
	}
	if !foundHiddenShowAll {
		t.Error("Show-all request should see hidden_tools")
	}

	// Both requests can call all tools
	resp1, err := server.CallTool(ctxNormal, "admin_tools", nil)
	if err != nil || resp1 == nil {
		t.Error("Normal: failed to call admin_tools")
	}

	resp2, err := server.CallTool(ctxNormal, "hidden_tools", nil)
	if err != nil || resp2 == nil {
		t.Error("Normal: failed to call hidden_tools (should be callable)")
	}
}

// TestDynamicToolStateTransition_ConditionalVisibility shows implementing
// conditional tool visibility based on tool's Visibility field
func TestDynamicToolStateTransition_ConditionalVisibility(t *testing.T) {
	server := NewServer("conditional-test", "1.0.0")

	// Provider with native and discoverable tools
	provider := &mockToolProvider{
		tools: []MCPTool{
			{Name: "public_data", Description: "Access public data", Visibility: ToolVisibilityNative},
			{Name: "sensitive_data", Description: "Access sensitive data", Visibility: ToolVisibilityDiscoverable, Keywords: []string{"sensitive"}},
		},
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "sensitive_data" {
				return "sensitive result", nil
			}
			if name == "public_data" {
				return "public result", nil
			}
			return nil, nil
		},
	}

	// Register a discoverable tool so tool_search exists
	server.RegisterTool(
		NewTool("dummy", "Dummy").Discoverable(),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
	)

	// Normal request - only sees native tools
	ctx := WithToolProviders(context.Background(), provider)
	tools := server.ListToolsWithContext(ctx)

	foundPublic := false
	foundSensitive := false
	for _, tool := range tools {
		if tool.Name == "public_data" {
			foundPublic = true
		}
		if tool.Name == "sensitive_data" {
			foundSensitive = true
		}
	}

	if !foundPublic {
		t.Error("Normal user should see public_data in list")
	}
	if foundSensitive {
		t.Error("Normal user should NOT see sensitive_data in list (it's discoverable)")
	}

	// Admin request with show-all - sees all tools
	adminCtx := WithToolProviders(context.Background(), provider)
	adminCtx = WithShowAllTools(adminCtx)
	adminTools := server.ListToolsWithContext(adminCtx)

	foundSensitiveAdmin := false
	for _, tool := range adminTools {
		if tool.Name == "sensitive_data" {
			foundSensitiveAdmin = true
		}
	}
	if !foundSensitiveAdmin {
		t.Error("Admin user with show-all should see sensitive_data")
	}

	// Both can search for tools
	resp1, err := server.CallTool(ctx, ToolSearchName, map[string]interface{}{
		"query":       "sensitive",
		"max_results": 10,
	})
	if err != nil {
		t.Fatalf("Normal user: tool_search failed: %v", err)
	}
	if resp1 == nil {
		t.Error("Normal user: expected response from tool_search")
	}
}
