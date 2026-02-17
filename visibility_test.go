package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestNativeToolRegistration tests that native tools appear in tools/list
func TestNativeToolRegistration(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register a native tool
	server.RegisterTool(
		NewTool("greet", "Greet someone",
			String("name", "Name to greet", Required()),
		),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			name, _ := req.String("name")
			return NewToolResponseText("Hello, " + name), nil
		},
	)

	// List tools should include greet
	tools := server.ListTools()
	if len(tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "greet" {
		t.Errorf("expected tool 'greet', got '%s'", tools[0].Name)
	}
}

// TestDiscoverableToolRegistration tests that discoverable tools don't appear in tools/list
// but tool_search and execute_tool do
func TestDiscoverableToolRegistration(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register a discoverable tool
	server.RegisterTool(
		NewTool("search_database", "Search the database",
			String("query", "Search query", Required()),
		).Discoverable("database", "search", "query"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("Found 10 results"), nil
		},
	)

	// List tools should NOT include search_database
	// But SHOULD include tool_search and execute_tool
	tools := server.ListTools()

	hasToolSearch := false
	hasExecuteTool := false
	hasSearchDatabase := false

	for _, tool := range tools {
		switch tool.Name {
		case ToolSearchName:
			hasToolSearch = true
		case ExecuteToolName:
			hasExecuteTool = true
		case "search_database":
			hasSearchDatabase = true
		}
	}

	if !hasToolSearch {
		t.Error("tool_search should be in tools/list when discoverable tools exist")
	}
	if !hasExecuteTool {
		t.Error("execute_tool should be in tools/list when discoverable tools exist")
	}
	if hasSearchDatabase {
		t.Error("search_database should NOT be in tools/list (it's discoverable)")
	}
}

// TestNoDiscoveryToolsWithoutDiscoverable tests that tool_search/execute_tool
// are NOT registered when there are no discoverable tools
func TestNoDiscoveryToolsWithoutDiscoverable(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register only native tools
	server.RegisterTool(
		NewTool("native_tool", "A native tool"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
	)

	tools := server.ListTools()

	for _, tool := range tools {
		if tool.Name == ToolSearchName || tool.Name == ExecuteToolName {
			t.Errorf("discovery tools should NOT exist without discoverable tools, found: %s", tool.Name)
		}
	}

	if len(tools) != 1 || tools[0].Name != "native_tool" {
		t.Error("expected only native_tool")
	}
}

// TestMixedNativeAndDiscoverable tests registration of both native and discoverable tools
func TestMixedNativeAndDiscoverable(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register native tool
	server.RegisterTool(
		NewTool("get_status", "Get system status"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
	)

	// Register discoverable tool
	server.RegisterTool(
		NewTool("complex_analysis", "Perform complex analysis",
			String("data", "Data to analyze", Required()),
		).Discoverable("analysis", "complex", "data"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("Analysis complete"), nil
		},
	)

	tools := server.ListTools()

	// Should have: get_status, tool_search, execute_tool
	// Should NOT have: complex_analysis
	expectedTools := map[string]bool{
		"get_status":     false,
		ToolSearchName:   false,
		ExecuteToolName:  false,
	}

	for _, tool := range tools {
		if tool.Name == "complex_analysis" {
			t.Error("complex_analysis should NOT appear in tools/list")
		}
		if _, ok := expectedTools[tool.Name]; ok {
			expectedTools[tool.Name] = true
		}
	}

	for name, found := range expectedTools {
		if !found {
			t.Errorf("expected tool '%s' in tools/list", name)
		}
	}
}

// TestToolSearchFindsDiscoverableTools tests that tool_search can find discoverable tools
func TestToolSearchFindsDiscoverableTools(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register discoverable tool
	server.RegisterTool(
		NewTool("send_email", "Send an email to a recipient",
			String("to", "Recipient email", Required()),
			String("subject", "Email subject", Required()),
			String("body", "Email body", Required()),
		).Discoverable("email", "send", "notification", "message"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("Email sent"), nil
		},
	)

	// Call tool_search
	response, err := server.CallTool(context.Background(), ToolSearchName, map[string]interface{}{
		"query": "email",
	})
	if err != nil {
		t.Fatalf("tool_search failed: %v", err)
	}

	// Parse response
	if len(response.Content) == 0 {
		t.Fatal("no content in response")
	}

	// The response should contain send_email
	responseText := response.Content[0].Text
	if !strings.Contains(responseText, "send_email") {
		t.Errorf("tool_search should find send_email, got: %s", responseText)
	}
}

// TestExecuteToolCallsDiscoverableTool tests that execute_tool can call discoverable tools
func TestExecuteToolCallsDiscoverableTool(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register discoverable tool
	callCount := 0
	server.RegisterTool(
		NewTool("count_calls", "Count how many times this is called").Discoverable("count", "test"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			callCount++
			return NewToolResponseText("Called!"), nil
		},
	)

	// Call via execute_tool
	response, err := server.CallTool(context.Background(), ExecuteToolName, map[string]interface{}{
		"name":      "count_calls",
		"arguments": map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("execute_tool failed: %v", err)
	}

	if callCount != 1 {
		t.Errorf("expected callCount=1, got %d", callCount)
	}

	if len(response.Content) == 0 || response.Content[0].Text != "Called!" {
		t.Error("unexpected response from execute_tool")
	}
}

// TestShowAllMode tests that WithShowAllTools shows ALL tools including discoverable ones
func TestShowAllMode(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register a mix of tools
	server.RegisterTool(
		NewTool("native_visible", "A visible native tool"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
	)

	server.RegisterTool(
		NewTool("discoverable_tool", "A discoverable tool").Discoverable("test"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
	)

	// Normal mode - should have native_visible, tool_search, execute_tool (NOT discoverable_tool)
	normalTools := server.ListToolsWithContext(context.Background())
	hasNativeVisible := false
	hasDiscoverable := false
	for _, tool := range normalTools {
		if tool.Name == "native_visible" {
			hasNativeVisible = true
		}
		if tool.Name == "discoverable_tool" {
			hasDiscoverable = true
		}
	}
	if !hasNativeVisible {
		t.Error("native_visible should appear in normal mode")
	}
	if hasDiscoverable {
		t.Error("discoverable_tool should NOT appear in normal mode list")
	}

	// Show-all mode - should have ALL tools including discoverable ones
	showAllCtx := WithShowAllTools(context.Background())
	showAllTools := server.ListToolsWithContext(showAllCtx)

	foundNative := false
	foundDiscoverable := false
	foundToolSearch := false
	foundExecuteTool := false

	for _, tool := range showAllTools {
		switch tool.Name {
		case "native_visible":
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
		t.Error("native_visible should appear in show-all mode")
	}
	if !foundDiscoverable {
		t.Error("discoverable_tool should appear in show-all mode")
	}
	if foundToolSearch {
		t.Error("tool_search should NOT appear in show-all mode")
	}
	if foundExecuteTool {
		t.Error("execute_tool should NOT appear in show-all mode")
	}

	if len(showAllTools) != 2 {
		t.Errorf("expected 2 tools in show-all mode, got %d", len(showAllTools))
	}
}

// TestShowAllModeShowsNativeAndDiscoverable tests that in show-all mode,
// both native and discoverable tools appear in the list
func TestShowAllModeShowsNativeAndDiscoverable(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register a discoverable tool
	server.RegisterTool(
		NewTool("discoverable_tool", "A discoverable tool").Discoverable("dummy"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
	)

	// Register a native tool
	server.RegisterTool(
		NewTool("native_tool", "A native tool"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("Found me!"), nil
		},
	)

	// In show-all mode, BOTH tools should appear in list
	showAllCtx := WithShowAllTools(context.Background())
	showAllTools := server.ListToolsWithContext(showAllCtx)

	foundNative := false
	foundDiscoverable := false
	for _, tool := range showAllTools {
		if tool.Name == "native_tool" {
			foundNative = true
		}
		if tool.Name == "discoverable_tool" {
			foundDiscoverable = true
		}
	}

	if !foundNative {
		t.Error("native_tool should appear in show-all mode list")
	}
	if !foundDiscoverable {
		t.Error("discoverable_tool should appear in show-all mode list")
	}

	// Both should also be callable directly
	response, err := server.CallTool(context.Background(), "native_tool", nil)
	if err != nil {
		t.Fatalf("native_tool should be callable: %v", err)
	}
	if response.Content[0].Text != "Found me!" {
		t.Error("unexpected response from native_tool")
	}
}

// TestRemoteServerNativeVisibility tests registering remote server with native visibility
func TestRemoteServerNativeVisibility(t *testing.T) {
	// Create a mock remote MCP server
	remoteMux := http.NewServeMux()
	remoteServer := NewServer("remote", "1.0.0")
	remoteServer.RegisterTool(
		NewTool("remote_greet", "Greet from remote",
			String("name", "Name", Required()),
		),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			name, _ := req.String("name")
			return NewToolResponseText("Remote says: Hello, " + name), nil
		},
	)
	remoteMux.HandleFunc("/mcp", remoteServer.HandleRequest)
	ts := httptest.NewServer(remoteMux)
	defer ts.Close()

	// Create main server and register remote with native visibility
	mainServer := NewServer("main", "1.0.0")
	client := NewClient(ts.URL+"/mcp", nil, "remote")
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("failed to initialize client: %v", err)
	}

	if err := mainServer.RegisterRemoteServer(client); err != nil {
		t.Fatalf("failed to register remote server: %v", err)
	}

	// Remote tools should appear in tools/list
	tools := mainServer.ListTools()
	hasRemoteGreet := false
	for _, tool := range tools {
		if strings.Contains(tool.Name, "remote_greet") {
			hasRemoteGreet = true
		}
	}

	if !hasRemoteGreet {
		t.Error("remote_greet should appear in tools/list with native visibility")
	}

	// Call the remote tool
	response, err := mainServer.CallTool(context.Background(), "remote.remote_greet", map[string]interface{}{
		"name": "World",
	})
	if err != nil {
		t.Fatalf("failed to call remote tool: %v", err)
	}

	if !strings.Contains(response.Content[0].Text, "Hello, World") {
		t.Errorf("unexpected response: %s", response.Content[0].Text)
	}
}

// TestRemoteServerDiscoverableVisibility tests registering remote server with discoverable visibility
func TestRemoteServerDiscoverableVisibility(t *testing.T) {
	// Create a mock remote MCP server
	remoteMux := http.NewServeMux()
	remoteServer := NewServer("remote", "1.0.0")
	remoteServer.RegisterTool(
		NewTool("remote_analyze", "Analyze data from remote",
			String("data", "Data", Required()),
		),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("Analysis complete"), nil
		},
	)
	remoteMux.HandleFunc("/mcp", remoteServer.HandleRequest)
	ts := httptest.NewServer(remoteMux)
	defer ts.Close()

	// Create main server and register remote with discoverable visibility
	mainServer := NewServer("main", "1.0.0")
	client := NewClient(ts.URL+"/mcp", nil, "analytics")
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("failed to initialize client: %v", err)
	}

	if err := mainServer.RegisterRemoteServerDiscoverable(client); err != nil {
		t.Fatalf("failed to register remote server: %v", err)
	}

	// Remote tools should NOT appear in tools/list
	tools := mainServer.ListTools()
	for _, tool := range tools {
		if strings.Contains(tool.Name, "remote_analyze") {
			t.Error("remote_analyze should NOT appear in tools/list with discoverable visibility")
		}
	}

	// But tool_search and execute_tool should exist
	hasToolSearch := false
	hasExecuteTool := false
	for _, tool := range tools {
		if tool.Name == ToolSearchName {
			hasToolSearch = true
		}
		if tool.Name == ExecuteToolName {
			hasExecuteTool = true
		}
	}

	if !hasToolSearch || !hasExecuteTool {
		t.Error("discovery tools should exist when discoverable remote server is registered")
	}

	// The remote tool should be searchable
	response, err := mainServer.CallTool(context.Background(), ToolSearchName, map[string]interface{}{
		"query": "analyze",
	})
	if err != nil {
		t.Fatalf("tool_search failed: %v", err)
	}

	if !strings.Contains(response.Content[0].Text, "remote_analyze") {
		t.Errorf("tool_search should find remote_analyze, got: %s", response.Content[0].Text)
	}

	// The remote tool should be callable via execute_tool
	response, err = mainServer.CallTool(context.Background(), ExecuteToolName, map[string]interface{}{
		"name": "analytics.remote_analyze",
		"arguments": map[string]interface{}{
			"data": "test data",
		},
	})
	if err != nil {
		t.Fatalf("execute_tool failed: %v", err)
	}

	if !strings.Contains(response.Content[0].Text, "Analysis complete") {
		t.Errorf("unexpected response: %s", response.Content[0].Text)
	}
}

// MockToolProvider implements ToolProvider for testing
type MockToolProvider struct {
	tools    []MCPTool
	handlers map[string]func(ctx context.Context, params map[string]interface{}) (interface{}, error)
}

func NewMockToolProvider() *MockToolProvider {
	return &MockToolProvider{
		tools:    make([]MCPTool, 0),
		handlers: make(map[string]func(ctx context.Context, params map[string]interface{}) (interface{}, error)),
	}
}

func (p *MockToolProvider) AddTool(name, description string, handler func(ctx context.Context, params map[string]interface{}) (interface{}, error), keywords ...string) {
	p.AddToolWithOptions(name, description, handler, keywords...)
}

func (p *MockToolProvider) AddToolWithOptions(name, description string, handler func(ctx context.Context, params map[string]interface{}) (interface{}, error), keywords ...string) {
	p.tools = append(p.tools, MCPTool{
		Name:          name,
		Description:   description,
		InputSchema:   map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Keywords:      keywords,
	})
	p.handlers[name] = handler
}

func (p *MockToolProvider) GetTools(ctx context.Context) ([]MCPTool, error) {
	return p.tools, nil
}

func (p *MockToolProvider) ExecuteTool(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
	if handler, ok := p.handlers[name]; ok {
		return handler(ctx, params)
	}
	return nil, nil // Not handled
}

// TestToolProviderInNormalMode tests that provider tools appear in normal mode
func TestToolProviderInNormalMode(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register at least one discoverable tool so discovery works
	server.RegisterTool(
		NewTool("dummy", "Dummy"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
	)

	// Create provider with a tool
	provider := NewMockToolProvider()
	provider.AddTool("provider_tool", "Tool from provider", func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
		return NewToolResponseText("Provider result"), nil
	})

	// Use provider with normal mode
	ctx := WithToolProviders(context.Background(), provider)
	tools := server.ListToolsWithContext(ctx)

	hasProviderTool := false
	for _, tool := range tools {
		if tool.Name == "provider_tool" {
			hasProviderTool = true
		}
	}

	if !hasProviderTool {
		t.Error("provider_tool should appear in normal mode")
	}
}

// TestToolProviderInShowAllMode tests that all provider tools appear in show-all mode
func TestToolProviderInShowAllMode(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register a discoverable tool so discovery tools exist
	server.RegisterTool(
		NewTool("dummy", "Dummy").Discoverable(),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
	)

	// Create provider with a native tool
	provider := NewMockToolProvider()
	provider.AddTool("provider_tool", "Tool from provider", func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
		return NewToolResponseText("Provider result"), nil
	})

	// In show-all mode, provider tool should appear (regardless of visibility)
	ctx := WithToolProviders(context.Background(), provider)
	ctx = WithShowAllTools(ctx)
	tools := server.ListToolsWithContext(ctx)

	hasProviderTool := false
	for _, tool := range tools {
		if tool.Name == "provider_tool" {
			hasProviderTool = true
		}
	}

	if !hasProviderTool {
		t.Error("provider_tool should appear in show-all mode")
	}

	// Should have: provider_tool, dummy (meta-tools excluded)
	if len(tools) != 2 {
		t.Errorf("expected 2 tools in show-all mode, got %d", len(tools))
	}
}

// TestToolProviderToolsAreSearchable tests that discoverable provider tools can be found via tool_search
func TestToolProviderToolsAreSearchable(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register a discoverable tool so discovery is enabled
	server.RegisterTool(
		NewTool("dummy", "Dummy").Discoverable(),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
	)

	// Create provider with a discoverable tool that has keywords
	provider := NewMockToolProvider()
	tool := MCPTool{
		Name:        "user_preferences",
		Description: "Get user preferences",
		Keywords:    []string{"user", "settings", "preferences", "config"},
		Visibility:  ToolVisibilityDiscoverable,
	}
	provider.tools = append(provider.tools, tool)
	provider.handlers["user_preferences"] = func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
		return NewToolResponseText("Preferences loaded"), nil
	}

	// Search for the provider tool - only discoverable tools are searchable
	ctx := WithToolProviders(context.Background(), provider)
	response, err := server.CallTool(ctx, ToolSearchName, map[string]interface{}{
		"query": "preferences",
	})
	if err != nil {
		t.Fatalf("tool_search failed: %v", err)
	}

	if !strings.Contains(response.Content[0].Text, "user_preferences") {
		t.Errorf("tool_search should find user_preferences, got: %s", response.Content[0].Text)
	}
}

// TestToolProviderToolsAreCallable tests that provider tools can be called
func TestToolProviderToolsAreCallable(t *testing.T) {
	server := NewServer("test", "1.0.0")

	callCount := 0
	provider := NewMockToolProvider()
	provider.AddTool("count_provider_calls", "Count calls", func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
		callCount++
		return NewToolResponseText("Provider called!"), nil
	})

	ctx := WithToolProviders(context.Background(), provider)

	// Call the provider tool directly
	response, err := server.CallTool(ctx, "count_provider_calls", nil)
	if err != nil {
		t.Fatalf("failed to call provider tool: %v", err)
	}

	if callCount != 1 {
		t.Errorf("expected callCount=1, got %d", callCount)
	}

	// Response is wrapped by callToolFromProviders
	if response == nil {
		t.Error("expected response from provider tool")
	}
}

// TestHTTPHandlerRespectsModes tests that the HTTP handler respects context modes
func TestHTTPHandlerRespectsModes(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register native tool
	server.RegisterTool(
		NewTool("native", "Native tool"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
	)

	// Register discoverable tool
	server.RegisterTool(
		NewTool("discoverable_tool", "Discoverable tool").Discoverable(),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
	)

	// Test normal mode
	normalHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.HandleRequest(w, r)
	})

	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	normalHandler.ServeHTTP(rec, req)

	var resp MCPResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	result := resp.Result.(map[string]interface{})
	tools := result["tools"].([]interface{})

	// Should have native, tool_search, execute_tool
	hasNative := false
	hasToolSearch := false
	for _, t := range tools {
		tool := t.(map[string]interface{})
		if tool["name"] == "native" {
			hasNative = true
		}
		if tool["name"] == ToolSearchName {
			hasToolSearch = true
		}
	}

	if !hasNative {
		t.Error("native tool should appear in normal mode")
	}
	if !hasToolSearch {
		t.Error("tool_search should appear when discoverable tools exist")
	}

	// Test show-all mode
	forceHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := WithShowAllTools(r.Context())
		server.HandleRequest(w, r.WithContext(ctx))
	})

	req = httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	forceHandler.ServeHTTP(rec, req)

	json.NewDecoder(rec.Body).Decode(&resp)
	result = resp.Result.(map[string]interface{})
	tools = result["tools"].([]interface{})

	// In show-all mode, all tools should appear including native and discoverable
	hasNativeInShowAll := false
	hasDiscoverableInShowAll := false
	hasToolSearchInShowAll := false
	for _, toolItem := range tools {
		tool := toolItem.(map[string]interface{})
		name := tool["name"].(string)
		if name == "native" {
			hasNativeInShowAll = true
		}
		if name == "discoverable_tool" {
			hasDiscoverableInShowAll = true
		}
		if name == ToolSearchName {
			hasToolSearchInShowAll = true
		}
	}

	if !hasNativeInShowAll {
		t.Error("native should appear in show-all mode")
	}
	if !hasDiscoverableInShowAll {
		t.Error("discoverable tool should appear in show-all mode")
	}
	if hasToolSearchInShowAll {
		t.Error("tool_search should NOT appear in show-all mode")
	}
}

// TestToolSearchEmptyQueryListsAllDiscoverableTools tests that empty query lists all tools
func TestToolSearchEmptyQueryListsAllDiscoverableTools(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register multiple discoverable tools
	for i := 0; i < 5; i++ {
		name := "tool_" + string(rune('a'+i))
		server.RegisterTool(
			NewTool(name, "Tool "+name).Discoverable(),
			func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
				return NewToolResponseText("OK"), nil
			},
		)
	}

	// Search with empty query - request enough results to get all tools
	response, err := server.CallTool(context.Background(), ToolSearchName, map[string]interface{}{
		"max_results": 10, // Request more than the 5 discoverable + 2 native tools
	})
	if err != nil {
		t.Fatalf("tool_search failed: %v", err)
	}

	text := response.Content[0].Text
	for i := 0; i < 5; i++ {
		name := "tool_" + string(rune('a'+i))
		if !strings.Contains(text, name) {
			t.Errorf("empty query should list all tools, missing: %s", name)
		}
	}
}

// TestExecuteToolWithUnknownTool tests execute_tool with unknown tool
func TestExecuteToolWithUnknownTool(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register a discoverable tool to enable discovery
	server.RegisterTool(
		NewTool("exists", "Tool that exists").Discoverable(),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
	)

	// Try to execute unknown tool
	response, err := server.CallTool(context.Background(), ExecuteToolName, map[string]interface{}{
		"name": "does_not_exist",
	})
	if err != nil {
		t.Fatalf("execute_tool should not error: %v", err)
	}

	if !strings.Contains(response.Content[0].Text, "not found") {
		t.Errorf("expected 'not found' message, got: %s", response.Content[0].Text)
	}
}

// TestVisibilityStringMethod tests the String() method on ToolVisibility
func TestVisibilityStringMethod(t *testing.T) {
	tests := []struct {
		v    ToolVisibility
		want string
	}{
		{ToolVisibilityNative, "native"},
		{ToolVisibilityDiscoverable, "discoverable"},
		{ToolVisibility(99), "unknown"},
	}

	for _, tt := range tests {
		got := tt.v.String()
		if got != tt.want {
			t.Errorf("ToolVisibility(%d).String() = %q, want %q", tt.v, got, tt.want)
		}
	}
}

// TestShowAllModeShowsAllToolTypes tests that show-all mode shows all tool types
func TestShowAllModeShowsAllToolTypes(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register a native tool
	server.RegisterTool(
		NewTool("native_tool", "A normal native tool"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
	)

	// Register a discoverable tool
	server.RegisterTool(
		NewTool("discoverable_tool", "A discoverable tool").Discoverable("test"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
	)

	// Normal mode - should have native_tool, tool_search, execute_tool (NOT discoverable_tool)
	normalTools := server.ListToolsWithContext(context.Background())
	hasNative := false
	hasDiscoverable := false
	for _, tool := range normalTools {
		if tool.Name == "native_tool" {
			hasNative = true
		}
		if tool.Name == "discoverable_tool" {
			hasDiscoverable = true
		}
	}
	if !hasNative {
		t.Error("native_tool should appear in normal mode")
	}
	if hasDiscoverable {
		t.Error("discoverable_tool should NOT appear in normal mode list")
	}

	// Show-all mode - should have ALL tools
	showAllCtx := WithShowAllTools(context.Background())
	showAllTools := server.ListToolsWithContext(showAllCtx)

	foundNative := false
	foundDiscoverable := false
	foundToolSearch := false
	foundExecuteTool := false
	for _, tool := range showAllTools {
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
		t.Error("native_tool should appear in show-all mode")
	}
	if !foundDiscoverable {
		t.Error("discoverable_tool should appear in show-all mode")
	}
	if foundToolSearch {
		t.Error("tool_search should NOT appear in show-all mode")
	}
	if foundExecuteTool {
		t.Error("execute_tool should NOT appear in show-all mode")
	}

	// Should have exactly 2 tools (meta-tools excluded)
	if len(showAllTools) != 2 {
		t.Errorf("expected 2 tools in show-all mode, got %d", len(showAllTools))
		for _, tool := range showAllTools {
			t.Logf("  - %s", tool.Name)
		}
	}

	// All tools should still be callable in show-all mode
	response, err := server.CallTool(showAllCtx, "native_tool", nil)
	if err != nil {
		t.Fatalf("native_tool should be callable in show-all mode: %v", err)
	}
	if response.Content[0].Text != "OK" {
		t.Errorf("unexpected response from native_tool: %s", response.Content[0].Text)
	}
}

// TestProviderToolsInShowAllMode tests that provider tools appear in show-all mode
func TestProviderToolsInShowAllMode(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register a discoverable tool to trigger discovery mode
	server.RegisterTool(
		NewTool("server_discoverable", "Server discoverable tool").Discoverable("dummy"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
	)

	// Create provider with both native and discoverable tools
	provider := NewMockToolProvider()
	provider.AddTool("provider_native", "A native provider tool", func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
		return NewToolResponseText("Provider Native"), nil
	})

	// Add a discoverable provider tool
	discoverableTool := MCPTool{
		Name:        "provider_discoverable",
		Description: "A discoverable provider tool",
		Keywords:    []string{"discoverable", "test"},
		Visibility:  ToolVisibilityDiscoverable,
	}
	provider.tools = append(provider.tools, discoverableTool)
	provider.handlers["provider_discoverable"] = func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
		return NewToolResponseText("Provider Discoverable"), nil
	}

	// Normal mode - should have native provider tool, NOT discoverable provider tool
	normalCtx := WithToolProviders(context.Background(), provider)
	normalTools := server.ListToolsWithContext(normalCtx)
	hasProviderNative := false
	hasProviderDiscoverable := false
	for _, tool := range normalTools {
		if tool.Name == "provider_native" {
			hasProviderNative = true
		}
		if tool.Name == "provider_discoverable" {
			hasProviderDiscoverable = true
		}
	}
	if !hasProviderNative {
		t.Error("provider_native should appear in normal mode")
	}
	if hasProviderDiscoverable {
		t.Error("provider_discoverable should NOT appear in normal mode list")
	}

	// Show-all mode - should have ALL provider tools
	showAllCtx := WithToolProviders(context.Background(), provider)
	showAllCtx = WithShowAllTools(showAllCtx)
	showAllTools := server.ListToolsWithContext(showAllCtx)

	foundProviderNative := false
	foundProviderDiscoverable := false
	foundToolSearch := false
	foundExecuteTool := false
	for _, tool := range showAllTools {
		switch tool.Name {
		case "provider_native":
			foundProviderNative = true
		case "provider_discoverable":
			foundProviderDiscoverable = true
		case ToolSearchName:
			foundToolSearch = true
		case ExecuteToolName:
			foundExecuteTool = true
		}
	}

	if !foundProviderNative {
		t.Error("provider_native should appear in show-all mode")
	}
	if !foundProviderDiscoverable {
		t.Error("provider_discoverable should appear in show-all mode")
	}
	if foundToolSearch {
		t.Error("tool_search should NOT appear in show-all mode (it's a meta-tool)")
	}
	if foundExecuteTool {
		t.Error("execute_tool should NOT appear in show-all mode (it's a meta-tool)")
	}

	// Both provider tools should be callable
	response, err := server.CallTool(showAllCtx, "provider_native", nil)
	if err != nil {
		t.Fatalf("provider_native should be callable in show-all mode: %v", err)
	}
	if response.Content[0].Text != "Provider Native" {
		t.Errorf("unexpected response from provider_native: %s", response.Content[0].Text)
	}
}
