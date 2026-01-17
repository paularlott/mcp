package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

// TestProviderIntegration_NativeMode tests complete workflow with providers in native mode
func TestProviderIntegration_NativeMode(t *testing.T) {
	server := NewServer("integration-test", "1.0.0")

	// Register native tools
	server.RegisterTool(
		NewTool("greet", "Greet someone", String("name", "Person to greet", Required())),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			name, _ := req.String("name")
			return NewToolResponseText("Hello, " + name), nil
		},
		"greeting", "hello",
	)

	server.RegisterOnDemandTool(
		NewTool("search_docs", "Search documentation", String("query", "Search query", Required())),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("Found matching docs"), nil
		},
		"search", "docs",
	)

	// Create two providers
	provider1 := &mockToolProvider{
		tools: []MCPTool{
			{Name: "calculate", Description: "Perform math", Keywords: []string{"math", "calculate"}},
			{Name: "convert", Description: "Convert units"},
		},
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "calculate" {
				return map[string]interface{}{"result": 42}, nil
			}
			if name == "convert" {
				return map[string]interface{}{"converted": "5 km"}, nil
			}
			return nil, nil
		},
	}

	provider2 := &mockToolProvider{
		tools: []MCPTool{
			{Name: "fetch_api", Description: "Fetch data from API"},
			{Name: "parse_json", Description: "Parse JSON data"},
		},
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "fetch_api" {
				return map[string]interface{}{"status": "ok"}, nil
			}
			if name == "parse_json" {
				return map[string]interface{}{"parsed": true}, nil
			}
			return nil, nil
		},
	}

	// Test 1: List tools without providers
	toolsWithoutProviders := server.ListTools()
	if len(toolsWithoutProviders) == 0 {
		t.Error("should have at least native and ondemand tools without providers")
	}

	// Test 2: List tools with one provider
	ctx1 := WithToolProviders(context.Background(), provider1)
	toolsWithProvider1 := server.ListToolsWithContext(ctx1)

	found1 := make(map[string]bool)
	for _, tool := range toolsWithProvider1 {
		found1[tool.Name] = true
	}

	if !found1["greet"] {
		t.Error("native tool 'greet' should be in list")
	}
	if !found1["calculate"] {
		t.Error("provider1 tool 'calculate' should be in list")
	}
	if !found1["convert"] {
		t.Error("provider1 tool 'convert' should be in list")
	}
	if found1["fetch_api"] {
		t.Error("provider2 tool 'fetch_api' should NOT be in list")
	}

	// Test 3: List tools with both providers
	ctx2 := WithToolProviders(context.Background(), provider1, provider2)
	toolsWithBothProviders := server.ListToolsWithContext(ctx2)

	found2 := make(map[string]bool)
	for _, tool := range toolsWithBothProviders {
		found2[tool.Name] = true
	}

	if !found2["greet"] {
		t.Error("native tool 'greet' should be in list")
	}
	if !found2["calculate"] {
		t.Error("provider1 tool 'calculate' should be in list")
	}
	if !found2["fetch_api"] {
		t.Error("provider2 tool 'fetch_api' should be in list")
	}

	// Test 4: Execute tool from provider
	resp, err := server.CallTool(ctx2, "calculate", nil)
	if err != nil {
		t.Fatalf("failed to call provider tool: %v", err)
	}
	if resp == nil {
		t.Error("expected response from provider tool")
	}

	// Test 5: Execute native tool should still work
	resp, err = server.CallTool(ctx2, "greet", map[string]interface{}{"name": "World"})
	if err != nil {
		t.Fatalf("failed to call native tool: %v", err)
	}
	if resp == nil {
		t.Error("expected response from native tool")
	}
}

// TestProviderIntegration_OnDemandMode tests complete workflow with providers in force ondemand mode
func TestProviderIntegration_OnDemandMode(t *testing.T) {
	server := NewServer("integration-test", "1.0.0")

	// Register native tool with keywords
	server.RegisterTool(
		NewTool("greet", "Greet someone", String("name", "Person to greet", Required())),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			name, _ := req.String("name")
			return NewToolResponseText("Hello, " + name), nil
		},
		"greeting", "hello", "salute",
	)

	server.RegisterOnDemandTool(
		NewTool("search_docs", "Search documentation", String("query", "Search query", Required())),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("Found matching docs"), nil
		},
		"search", "docs", "find",
	)

	// Create provider
	provider := &mockToolProvider{
		tools: []MCPTool{
			{Name: "calculate", Description: "Perform math", Keywords: []string{"math", "calculate", "arithmetic"}},
		},
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "calculate" {
				return map[string]interface{}{"result": 42}, nil
			}
			return nil, nil
		},
	}

	// Test 1: In force ondemand mode, only discovery tools appear in list
	ctxOnDemand := WithForceOnDemandMode(context.Background(), provider)
	toolsInList := server.ListToolsWithContext(ctxOnDemand)

	if len(toolsInList) != 2 {
		t.Fatalf("expected exactly 2 tools (tool_search, execute_tool), got %d", len(toolsInList))
	}

	foundSearch := false
	foundExecute := false
	for _, tool := range toolsInList {
		if tool.Name == ToolSearchName {
			foundSearch = true
		}
		if tool.Name == ExecuteToolName {
			foundExecute = true
		}
	}

	if !foundSearch {
		t.Error("tool_search should be in list")
	}
	if !foundExecute {
		t.Error("execute_tool should be in list")
	}

	// Test 2: Native tools are callable but not in list
	resp, err := server.CallTool(ctxOnDemand, "greet", map[string]interface{}{"name": "World"})
	if err != nil {
		t.Fatalf("native tool should be callable: %v", err)
	}
	if resp == nil {
		t.Error("expected response from native tool")
	}

	// Test 3: Provider tools are callable
	resp, err = server.CallTool(ctxOnDemand, "calculate", nil)
	if err != nil {
		t.Fatalf("provider tool should be callable: %v", err)
	}
	if resp == nil {
		t.Error("expected response from provider tool")
	}

	// Test 4: All tools should be searchable
	searchResp, err := server.CallTool(ctxOnDemand, ToolSearchName, map[string]interface{}{
		"query":       "",
		"max_results": 100,
	})
	if err != nil {
		t.Fatalf("tool_search failed: %v", err)
	}

	var results []SearchResult
	if len(searchResp.Content) > 0 {
		if err := json.Unmarshal([]byte(searchResp.Content[0].Text), &results); err != nil {
			t.Fatalf("failed to parse search results: %v", err)
		}

		foundGreet := false
		foundSearch := false
		foundCalculate := false

		for _, result := range results {
			if result.Name == "greet" {
				foundGreet = true
			}
			if result.Name == "search_docs" {
				foundSearch = true
			}
			if result.Name == "calculate" {
				foundCalculate = true
			}
		}

		if !foundGreet {
			t.Error("native tool 'greet' should be searchable")
		}
		if !foundSearch {
			t.Error("ondemand tool 'search_docs' should be searchable")
		}
		if !foundCalculate {
			t.Error("provider tool 'calculate' should be searchable")
		}
	}
}

// TestProviderIntegration_MixedProviders tests mixing native providers and providers from context
func TestProviderIntegration_MixedProviders(t *testing.T) {
	server := NewServer("mixed-test", "1.0.0")

	// Register a "native provider" using the server's native tool registration
	// (simulating built-in tools from a native provider)
	server.RegisterTool(
		NewTool("native_builtin", "Built-in native tool"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("from native"), nil
		},
	)

	// Create a request-scoped provider (e.g., for a specific user)
	contextProvider := &mockToolProvider{
		tools: []MCPTool{
			{Name: "user_specific", Description: "User-specific tool"},
		},
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "user_specific" {
				return "user-specific result", nil
			}
			return nil, nil
		},
	}

	ctx := WithToolProviders(context.Background(), contextProvider)

	// Both native and context-provider tools should work
	resp1, err := server.CallTool(ctx, "native_builtin", nil)
	if err != nil {
		t.Fatalf("native tool should work: %v", err)
	}
	if resp1 == nil {
		t.Error("expected response from native tool")
	}

	resp2, err := server.CallTool(ctx, "user_specific", nil)
	if err != nil {
		t.Fatalf("context provider tool should work: %v", err)
	}
	if resp2 == nil {
		t.Error("expected response from context provider tool")
	}

	// Both should appear in tools list
	tools := server.ListToolsWithContext(ctx)
	found := make(map[string]bool)
	for _, tool := range tools {
		found[tool.Name] = true
	}

	if !found["native_builtin"] {
		t.Error("native tool should be in list")
	}
	if !found["user_specific"] {
		t.Error("context provider tool should be in list")
	}
}

// TestProviderIntegration_ContextReplacement tests that new provider contexts replace old ones
func TestProviderIntegration_ContextReplacement(t *testing.T) {
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

	ctx1 := WithToolProviders(context.Background(), provider1)
	ctx2 := WithToolProviders(ctx1, provider2)

	// ctx1 should have provider1
	providers1 := GetToolProviders(ctx1)
	if len(providers1) != 1 {
		t.Errorf("ctx1 should have 1 provider, got %d", len(providers1))
	}

	seen1 := make(map[string]bool)
	_ = listToolsFromProviders(ctx1, seen1)
	if !seen1["tool1"] {
		t.Error("ctx1 should have tool1 from provider1")
	}
	if seen1["tool2"] {
		t.Error("ctx1 should not have tool2 from provider2")
	}

	// ctx2 should have provider2 only (not provider1)
	providers2 := GetToolProviders(ctx2)
	if len(providers2) != 1 {
		t.Errorf("ctx2 should have 1 provider, got %d", len(providers2))
	}

	seen2 := make(map[string]bool)
	_ = listToolsFromProviders(ctx2, seen2)
	if seen2["tool1"] {
		t.Error("ctx2 should not have tool1 from provider1")
	}
	if !seen2["tool2"] {
		t.Error("ctx2 should have tool2 from provider2")
	}
}

// TestProviderIntegration_ExecuteToolMissing tests execute_tool with missing/invalid tool
func TestProviderIntegration_ExecuteToolMissing(t *testing.T) {
	server := NewServer("test", "1.0.0")

	provider := &mockToolProvider{
		tools: []MCPTool{
			{Name: "exists", Description: "This tool exists"},
		},
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "exists" {
				return "result", nil
			}
			return nil, nil
		},
	}

	ctx := WithToolProviders(context.Background(), provider)

	// Try to execute non-existent tool via execute_tool
	resp, err := server.CallTool(ctx, ExecuteToolName, map[string]interface{}{
		"name":      "nonexistent",
		"arguments": map[string]interface{}{},
	})

	// Should get an error response or error
	if err == nil && resp != nil {
		// Check if the content indicates an error
		if len(resp.Content) == 0 {
			t.Error("response should contain error information")
		}
	}
}

// TestProviderIntegration_ConcurrentRequests tests that provider contexts work correctly with concurrent requests
func TestProviderIntegration_ConcurrentRequests(t *testing.T) {
	server := NewServer("concurrent-test", "1.0.0")

	provider1 := &mockToolProvider{
		tools: []MCPTool{
			{Name: "tool1", Description: "Provider 1"},
		},
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "tool1" {
				return "result1", nil
			}
			return nil, nil // Not handled
		},
	}

	provider2 := &mockToolProvider{
		tools: []MCPTool{
			{Name: "tool2", Description: "Provider 2"},
		},
		execFunc: func(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
			if name == "tool2" {
				return "result2", nil
			}
			return nil, nil // Not handled
		},
	}

	ctx1 := WithToolProviders(context.Background(), provider1)
	ctx2 := WithToolProviders(context.Background(), provider2)

	// Simulate concurrent requests
	done1 := make(chan error, 1)
	done2 := make(chan error, 1)

	go func() {
		resp, err := server.CallTool(ctx1, "tool1", nil)
		if err != nil {
			done1 <- err
		} else if resp == nil {
			done1 <- ErrUnknownTool
		} else {
			done1 <- nil
		}
	}()

	go func() {
		resp, err := server.CallTool(ctx2, "tool2", nil)
		if err != nil {
			done2 <- err
		} else if resp == nil {
			done2 <- ErrUnknownTool
		} else {
			done2 <- nil
		}
	}()

	err1 := <-done1
	err2 := <-done2

	if err1 != nil {
		t.Fatalf("request 1 failed: %v", err1)
	}
	if err2 != nil {
		t.Fatalf("request 2 failed: %v", err2)
	}

	// Verify tool1 is available in ctx1
	resp, err := server.CallTool(ctx1, "tool1", nil)
	if err != nil {
		t.Errorf("tool1 should be available in ctx1: %v", err)
	}
	if resp == nil {
		t.Error("expected response from tool1 in ctx1")
	}

	// Verify tool2 is NOT available in ctx1
	_, err = server.CallTool(ctx1, "tool2", nil)
	if err != ErrUnknownTool {
		t.Errorf("tool2 should not be available in ctx1, got: %v", err)
	}

	// Verify tool2 is available in ctx2
	resp, err = server.CallTool(ctx2, "tool2", nil)
	if err != nil {
		t.Errorf("tool2 should be available in ctx2: %v", err)
	}
	if resp == nil {
		t.Error("expected response from tool2 in ctx2")
	}

	// Verify tool1 is NOT available in ctx2
	_, err = server.CallTool(ctx2, "tool1", nil)
	if err != ErrUnknownTool {
		t.Errorf("tool1 should not be available in ctx2, got: %v", err)
	}
}
