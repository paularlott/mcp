package mcp

import (
	"context"
	"testing"
)

// TestAddRemoveProvidersToNativeContext tests that providers can be dynamically added and removed
// to a native (non-ondemand) context
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

// TestAddRemoveProvidersToOnDemandContext tests that providers work correctly with force ondemand mode
func TestAddRemoveProvidersToOnDemandContext(t *testing.T) {
	ctx := context.Background()

	provider := &mockToolProvider{
		tools: []MCPTool{
			{Name: "demand_tool", Description: "A tool for ondemand mode", Keywords: []string{"demand"}},
		},
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "demand_tool" {
				return "demand_result", nil
			}
			return nil, nil
		},
	}

	// Add provider to force ondemand context
	ctxOnDemand := WithForceOnDemandMode(ctx, provider)

	// In force ondemand mode, provider tools should be hidden from listToolsFromProviders
	seen := make(map[string]bool)
	tools := listToolsFromProviders(ctxOnDemand, seen)
	if len(tools) != 0 {
		t.Errorf("provider tools should be hidden in force ondemand mode, got %d tools", len(tools))
	}

	// But the provider should still be retrievable and executable
	providers := GetToolProviders(ctxOnDemand)
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(providers))
	}

	// Provider tool should still be callable
	result, err := callToolFromProviders(ctxOnDemand, "demand_tool", nil)
	if err != nil {
		t.Fatalf("failed to call provider tool: %v", err)
	}
	if result == nil {
		t.Error("expected non-nil result")
	}

	// Verify ondemand mode is set
	mode := GetToolListMode(ctxOnDemand)
	if mode != ToolListModeForceOnDemand {
		t.Errorf("expected ToolListModeForceOnDemand, got %v", mode)
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

	// tool_search and execute_tool should NOT appear in native mode (no ondemand tools)
	if found[ToolSearchName] {
		t.Error("tool_search should not appear in native mode without ondemand tools")
	}
	if found[ExecuteToolName] {
		t.Error("execute_tool should not appear in native mode without ondemand tools")
	}
}

// TestProviderToolVisibilityOnDemandMode tests tool visibility in force ondemand mode
func TestProviderToolVisibilityOnDemandMode(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register a native tool with keywords for searchability
	server.RegisterTool(
		NewTool("native_tool", "A native tool"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("native"), nil
		},
		"search", "keyword",
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

	// List tools in force ondemand mode
	ctx := WithForceOnDemandMode(context.Background(), provider)
	tools := server.ListToolsWithContext(ctx)

	// Only tool_search and execute_tool should appear in list
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools in force ondemand mode, got %d", len(tools))
	}

	found := make(map[string]bool)
	for _, tool := range tools {
		found[tool.Name] = true
	}

	if !found[ToolSearchName] {
		t.Error("tool_search should appear in force ondemand mode")
	}
	if !found[ExecuteToolName] {
		t.Error("execute_tool should appear in force ondemand mode")
	}

	// native_tool and provider_tool should NOT appear in the list
	if found["native_tool"] {
		t.Error("native_tool should not appear in force ondemand mode list")
	}
	if found["provider_tool"] {
		t.Error("provider_tool should not appear in force ondemand mode list")
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

// TestNestedContexts tests that later contexts override earlier ones
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

	// Create nested contexts
	ctx1 := WithToolProviders(context.Background(), provider1)
	ctx2 := WithToolProviders(ctx1, provider2) // This overrides ctx1
	ctx3 := WithToolProviders(ctx2, provider3) // This overrides ctx2

	// ctx3 should only have provider3, not provider1 or provider2
	// (WithToolProviders doesn't merge, it replaces)
	providers := GetToolProviders(ctx3)
	if len(providers) != 1 {
		t.Fatalf("ctx3 should have exactly 1 provider, got %d", len(providers))
	}

	seen := make(map[string]bool)
	tools := listToolsFromProviders(ctx3, seen)
	if len(tools) != 1 || !seen["tool3"] {
		t.Error("ctx3 should only have tool3")
	}

	// ctx2 should have provider2 (and would have provider1 if we traced back, but context values don't merge)
	providers2 := GetToolProviders(ctx2)
	if len(providers2) != 1 {
		t.Fatalf("ctx2 should have exactly 1 provider, got %d", len(providers2))
	}

	seen2 := make(map[string]bool)
	tools2 := listToolsFromProviders(ctx2, seen2)
	if len(tools2) != 1 || !seen2["tool2"] {
		t.Error("ctx2 should only have tool2")
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

// TestDynamicToolStateTransition tests transitioning tools between native and ondemand modes
// This demonstrates a real-world pattern where tools can be:
// 1. Initially available as native tools (visible in tools/list)
// 2. Dynamically hidden and moved to ondemand (only searchable)
// 3. Returned to native visibility (visible in tools/list again)
//
// Use cases:
// - Progressive disclosure: Start with all tools, hide rarely-used ones
// - Mode switching: Change visibility based on LLM capability
// - Feature gates: Show/hide features per tenant or request
func TestDynamicToolStateTransition(t *testing.T) {
	server := NewServer("state-transition-test", "1.0.0")

	// Create a provider that tracks which tools are active
	provider := &mockToolProvider{
		tools: []MCPTool{
			{
				Name:        "email_send",
				Description: "Send email messages",
				Keywords:    []string{"email", "send", "message"},
			},
			{
				Name:        "file_upload",
				Description: "Upload files to storage",
				Keywords:    []string{"file", "upload", "storage"},
			},
		},
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "email_send" {
				return map[string]interface{}{"sent": true, "to": "user@example.com"}, nil
			}
			if name == "file_upload" {
				return map[string]interface{}{"uploaded": true, "size": 1024}, nil
			}
			return nil, nil
		},
	}

	// ============================================================================
	// STAGE 1: Native Mode - All tools visible in tools/list
	// ============================================================================
	// Default context without providers (server's own native tools)
	server.RegisterTool(
		NewTool("list_emails", "List recent emails"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("3 emails found"), nil
		},
		"email", "list",
	)

	// Get tools in native mode with provider
	ctxNative := WithToolProviders(context.Background(), provider)
	toolsNative := server.ListToolsWithContext(ctxNative)

	foundNativeEmail := false
	foundNativeFile := false
	for _, tool := range toolsNative {
		if tool.Name == "email_send" {
			foundNativeEmail = true
		}
		if tool.Name == "file_upload" {
			foundNativeFile = true
		}
	}

	if !foundNativeEmail {
		t.Error("Stage 1 (Native): email_send should be visible in tools/list")
	}
	if !foundNativeFile {
		t.Error("Stage 1 (Native): file_upload should be visible in tools/list")
	}

	// Verify tools are callable in native mode
	resp, err := server.CallTool(ctxNative, "email_send", map[string]interface{}{})
	if err != nil {
		t.Fatalf("Stage 1 (Native): failed to call email_send: %v", err)
	}
	if resp == nil {
		t.Error("Stage 1 (Native): expected response from email_send")
	}

	// ============================================================================
	// STAGE 2: Transition to OnDemand Mode - Hide tools, only discoverable
	// ============================================================================
	// Switch to force ondemand mode with the same provider
	ctxOnDemand := WithForceOnDemandMode(context.Background(), provider)
	toolsOnDemand := server.ListToolsWithContext(ctxOnDemand)

	// In force ondemand mode, only discovery tools should be visible
	if len(toolsOnDemand) != 2 {
		t.Fatalf("Stage 2 (OnDemand): expected 2 tools (discovery tools), got %d", len(toolsOnDemand))
	}

	foundOnDemandSearch := false
	foundOnDemandExecute := false
	for _, tool := range toolsOnDemand {
		if tool.Name == ToolSearchName {
			foundOnDemandSearch = true
		}
		if tool.Name == ExecuteToolName {
			foundOnDemandExecute = true
		}
		// Provider tools should NOT be in the list
		if tool.Name == "email_send" || tool.Name == "file_upload" {
			t.Errorf("Stage 2 (OnDemand): tool %s should NOT be in tools/list", tool.Name)
		}
	}

	if !foundOnDemandSearch {
		t.Error("Stage 2 (OnDemand): tool_search should be in list")
	}
	if !foundOnDemandExecute {
		t.Error("Stage 2 (OnDemand): execute_tool should be in list")
	}

	// Tools are still callable, just hidden
	resp, err = server.CallTool(ctxOnDemand, "email_send", map[string]interface{}{})
	if err != nil {
		t.Fatalf("Stage 2 (OnDemand): failed to call email_send: %v", err)
	}
	if resp == nil {
		t.Error("Stage 2 (OnDemand): expected response from email_send")
	}

	// Tools should be discoverable via tool_search
	searchResp, err := server.CallTool(ctxOnDemand, ToolSearchName, map[string]interface{}{
		"query":       "email",
		"max_results": 10,
	})
	if err != nil {
		t.Fatalf("Stage 2 (OnDemand): tool_search failed: %v", err)
	}
	if searchResp == nil {
		t.Error("Stage 2 (OnDemand): expected response from tool_search")
	}

	// ============================================================================
	// STAGE 3: Return to Native Mode - Tools visible again
	// ============================================================================
	// Switch back to native mode with the provider
	ctxNativeAgain := WithToolProviders(context.Background(), provider)
	toolsNativeAgain := server.ListToolsWithContext(ctxNativeAgain)

	foundNativeEmail2 := false
	foundNativeFile2 := false
	for _, tool := range toolsNativeAgain {
		if tool.Name == "email_send" {
			foundNativeEmail2 = true
		}
		if tool.Name == "file_upload" {
			foundNativeFile2 = true
		}
	}

	if !foundNativeEmail2 {
		t.Error("Stage 3 (Native): email_send should be visible in tools/list again")
	}
	if !foundNativeFile2 {
		t.Error("Stage 3 (Native): file_upload should be visible in tools/list again")
	}

	// Verify tools are still callable
	resp, err = server.CallTool(ctxNativeAgain, "email_send", map[string]interface{}{})
	if err != nil {
		t.Fatalf("Stage 3 (Native): failed to call email_send: %v", err)
	}
	if resp == nil {
		t.Error("Stage 3 (Native): expected response from email_send")
	}
}

// TestDynamicToolStateTransition_PerRequest demonstrates transitioning tools
// for different requests simultaneously without cross-contamination
func TestDynamicToolStateTransition_PerRequest(t *testing.T) {
	server := NewServer("per-request-test", "1.0.0")

	provider := &mockToolProvider{
		tools: []MCPTool{
			{Name: "admin_tools", Description: "Admin functionality", Keywords: []string{"admin"}},
			{Name: "user_tools", Description: "User functionality", Keywords: []string{"user"}},
		},
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "admin_tools" {
				return "admin result", nil
			}
			if name == "user_tools" {
				return "user result", nil
			}
			return nil, nil
		},
	}

	// Simulate request 1: Admin request in native mode (all tools visible)
	ctxAdmin := WithToolProviders(context.Background(), provider)

	// Simulate request 2: User request in ondemand mode (tools hidden)
	ctxUser := WithForceOnDemandMode(context.Background(), provider)

	// Request 1 (Admin) should see tools in list
	toolsAdmin := server.ListToolsWithContext(ctxAdmin)
	foundAdmin := false
	for _, tool := range toolsAdmin {
		if tool.Name == "admin_tools" {
			foundAdmin = true
		}
	}
	if !foundAdmin {
		t.Error("Admin request should see admin_tools in native mode")
	}

	// Request 2 (User) should NOT see tools in list (only discovery tools)
	toolsUser := server.ListToolsWithContext(ctxUser)
	for _, tool := range toolsUser {
		if tool.Name == "admin_tools" || tool.Name == "user_tools" {
			t.Errorf("User request should not see %s in ondemand mode", tool.Name)
		}
	}

	// Both requests can still call their tools
	resp1, err := server.CallTool(ctxAdmin, "admin_tools", nil)
	if err != nil {
		t.Fatalf("Admin: failed to call admin_tools: %v", err)
	}
	if resp1 == nil {
		t.Error("Admin: expected response")
	}

	resp2, err := server.CallTool(ctxUser, "user_tools", nil)
	if err != nil {
		t.Fatalf("User: failed to call user_tools: %v", err)
	}
	if resp2 == nil {
		t.Error("User: expected response")
	}

	// Request 1 cannot access user request's mode
	toolsAdminCheck := server.ListToolsWithContext(ctxAdmin)
	if len(toolsAdminCheck) < 2 { // Should have admin_tools, user_tools, etc.
		t.Error("Admin context should not be affected by user context")
	}
}

// TestDynamicToolStateTransition_ConditionalVisibility shows implementing
// conditional tool visibility based on request attributes
func TestDynamicToolStateTransition_ConditionalVisibility(t *testing.T) {
	server := NewServer("conditional-test", "1.0.0")

	provider := &mockToolProvider{
		tools: []MCPTool{
			{Name: "sensitive_data", Description: "Access sensitive data", Keywords: []string{"sensitive"}},
			{Name: "public_data", Description: "Access public data", Keywords: []string{"public"}},
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

	// Helper to determine if request has elevated privileges
	hasElevatedPrivileges := func(userRole string) bool {
		return userRole == "admin" || userRole == "power_user"
	}

	// Request 1: Regular user - tools in ondemand (hidden)
	userRole1 := "regular_user"
	var ctx1 context.Context
	if hasElevatedPrivileges(userRole1) {
		ctx1 = WithToolProviders(context.Background(), provider)
	} else {
		ctx1 = WithForceOnDemandMode(context.Background(), provider)
	}

	tools1 := server.ListToolsWithContext(ctx1)
	for _, tool := range tools1 {
		if tool.Name == "sensitive_data" {
			t.Error("Regular user should not see sensitive_data in list")
		}
	}

	// Request 2: Admin user - tools in native (visible)
	userRole2 := "admin"
	var ctx2 context.Context
	if hasElevatedPrivileges(userRole2) {
		ctx2 = WithToolProviders(context.Background(), provider)
	} else {
		ctx2 = WithForceOnDemandMode(context.Background(), provider)
	}

	tools2 := server.ListToolsWithContext(ctx2)
	foundSensitive := false
	for _, tool := range tools2 {
		if tool.Name == "sensitive_data" {
			foundSensitive = true
		}
	}
	if !foundSensitive {
		t.Error("Admin user should see sensitive_data in list")
	}

	// Both users can search for tools (at different levels of access)
	// The search would typically filter results based on permissions
	// but in this test we're just verifying the visibility mode works
	resp1, err := server.CallTool(ctx1, ToolSearchName, map[string]interface{}{
		"query":       "",
		"max_results": 10,
	})
	if err != nil {
		t.Fatalf("Regular user: tool_search failed: %v", err)
	}
	if resp1 == nil {
		t.Error("Regular user: expected response from tool_search")
	}

	resp2, err := server.CallTool(ctx2, ToolSearchName, map[string]interface{}{
		"query":       "",
		"max_results": 10,
	})
	if err != nil {
		t.Fatalf("Admin user: tool_search failed: %v", err)
	}
	if resp2 == nil {
		t.Error("Admin user: expected response from tool_search")
	}
}
