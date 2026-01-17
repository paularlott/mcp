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

// TestOnDemandToolRegistration tests that ondemand tools don't appear in tools/list
// but tool_search and execute_tool do
func TestOnDemandToolRegistration(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register an ondemand tool
	server.RegisterOnDemandTool(
		NewTool("search_database", "Search the database",
			String("query", "Search query", Required()),
		),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("Found 10 results"), nil
		},
		"database", "search", "query",
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
		t.Error("tool_search should be in tools/list when ondemand tools exist")
	}
	if !hasExecuteTool {
		t.Error("execute_tool should be in tools/list when ondemand tools exist")
	}
	if hasSearchDatabase {
		t.Error("search_database should NOT be in tools/list (it's ondemand)")
	}
}

// TestNoDiscoveryToolsWithoutOnDemand tests that tool_search/execute_tool
// are NOT registered when there are no ondemand tools
func TestNoDiscoveryToolsWithoutOnDemand(t *testing.T) {
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
			t.Errorf("discovery tools should NOT exist without ondemand tools, found: %s", tool.Name)
		}
	}

	if len(tools) != 1 || tools[0].Name != "native_tool" {
		t.Error("expected only native_tool")
	}
}

// TestMixedNativeAndOnDemand tests registration of both native and ondemand tools
func TestMixedNativeAndOnDemand(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register native tool
	server.RegisterTool(
		NewTool("get_status", "Get system status"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
	)

	// Register ondemand tool
	server.RegisterOnDemandTool(
		NewTool("complex_analysis", "Perform complex analysis",
			String("data", "Data to analyze", Required()),
		),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("Analysis complete"), nil
		},
		"analysis", "complex", "data",
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

// TestToolSearchFindsOnDemandTools tests that tool_search can find ondemand tools
func TestToolSearchFindsOnDemandTools(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register ondemand tool
	server.RegisterOnDemandTool(
		NewTool("send_email", "Send an email to a recipient",
			String("to", "Recipient email", Required()),
			String("subject", "Email subject", Required()),
			String("body", "Email body", Required()),
		),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("Email sent"), nil
		},
		"email", "send", "notification", "message",
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

// TestExecuteToolCallsOnDemandTool tests that execute_tool can call ondemand tools
func TestExecuteToolCallsOnDemandTool(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register ondemand tool
	callCount := 0
	server.RegisterOnDemandTool(
		NewTool("count_calls", "Count how many times this is called"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			callCount++
			return NewToolResponseText("Called!"), nil
		},
		"count", "test",
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

// TestForceOnDemandMode tests that WithForceOnDemandMode hides all tools except discovery tools
func TestForceOnDemandMode(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register a mix of tools
	server.RegisterTool(
		NewTool("native_visible", "A visible native tool"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
	)

	server.RegisterOnDemandTool(
		NewTool("ondemand_hidden", "An ondemand tool"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
		"test",
	)

	// Normal mode - should have native_visible, tool_search, execute_tool
	normalTools := server.ListToolsWithContext(context.Background())
	hasNativeVisible := false
	for _, tool := range normalTools {
		if tool.Name == "native_visible" {
			hasNativeVisible = true
		}
	}
	if !hasNativeVisible {
		t.Error("native_visible should appear in normal mode")
	}

	// Force ondemand mode - should only have tool_search, execute_tool
	forceCtx := WithForceOnDemandMode(context.Background())
	forceTools := server.ListToolsWithContext(forceCtx)

	for _, tool := range forceTools {
		if tool.Name != ToolSearchName && tool.Name != ExecuteToolName {
			t.Errorf("in force ondemand mode, only discovery tools should appear, found: %s", tool.Name)
		}
	}

	if len(forceTools) != 2 {
		t.Errorf("expected 2 tools in force ondemand mode, got %d", len(forceTools))
	}
}

// TestForceOnDemandModeSearchIncludesNativeTools tests that in force ondemand mode,
// tool_search can find native tools
func TestForceOnDemandModeSearchIncludesNativeTools(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register a native tool - but we also need at least one ondemand tool
	// to trigger discovery tools registration
	server.RegisterOnDemandTool(
		NewTool("dummy_ondemand", "Dummy tool"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
		"dummy",
	)

	// The native tool should be searchable in force ondemand mode
	server.RegisterTool(
		NewTool("native_searchable", "A native tool that should be searchable"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("Found me!"), nil
		},
	)

	// In force ondemand mode, native_searchable should NOT appear in list
	forceCtx := WithForceOnDemandMode(context.Background())
	forceTools := server.ListToolsWithContext(forceCtx)

	for _, tool := range forceTools {
		if tool.Name == "native_searchable" {
			t.Error("native_searchable should NOT appear in force ondemand mode list")
		}
	}

	// But it should still be callable directly (native tools are always callable)
	response, err := server.CallTool(context.Background(), "native_searchable", nil)
	if err != nil {
		t.Fatalf("native_searchable should be callable: %v", err)
	}
	if response.Content[0].Text != "Found me!" {
		t.Error("unexpected response from native_searchable")
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
	response, err := mainServer.CallTool(context.Background(), "remote/remote_greet", map[string]interface{}{
		"name": "World",
	})
	if err != nil {
		t.Fatalf("failed to call remote tool: %v", err)
	}

	if !strings.Contains(response.Content[0].Text, "Hello, World") {
		t.Errorf("unexpected response: %s", response.Content[0].Text)
	}
}

// TestRemoteServerOnDemandVisibility tests registering remote server with ondemand visibility
func TestRemoteServerOnDemandVisibility(t *testing.T) {
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

	// Create main server and register remote with ondemand visibility
	mainServer := NewServer("main", "1.0.0")
	client := NewClient(ts.URL+"/mcp", nil, "analytics")
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("failed to initialize client: %v", err)
	}

	if err := mainServer.RegisterRemoteServerOnDemand(client); err != nil {
		t.Fatalf("failed to register remote server: %v", err)
	}

	// Remote tools should NOT appear in tools/list
	tools := mainServer.ListTools()
	for _, tool := range tools {
		if strings.Contains(tool.Name, "remote_analyze") {
			t.Error("remote_analyze should NOT appear in tools/list with ondemand visibility")
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
		t.Error("discovery tools should exist when ondemand remote server is registered")
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
		"name": "analytics/remote_analyze",
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
	p.tools = append(p.tools, MCPTool{
		Name:        name,
		Description: description,
		InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Keywords:    keywords,
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

	// Register at least one ondemand tool so discovery works
	server.RegisterOnDemandTool(
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

// TestToolProviderInForceOnDemandMode tests that provider tools are hidden in force ondemand mode
func TestToolProviderInForceOnDemandMode(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register at least one ondemand tool so discovery works
	server.RegisterOnDemandTool(
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

	// Use provider with force ondemand mode
	ctx := WithForceOnDemandMode(context.Background(), provider)
	tools := server.ListToolsWithContext(ctx)

	for _, tool := range tools {
		if tool.Name == "provider_tool" {
			t.Error("provider_tool should NOT appear in force ondemand mode")
		}
	}

	// Should only have tool_search and execute_tool
	if len(tools) != 2 {
		t.Errorf("expected 2 tools in force ondemand mode, got %d", len(tools))
	}
}

// TestToolProviderToolsAreSearchable tests that provider tools can be found via tool_search in force ondemand mode
func TestToolProviderToolsAreSearchable(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register an ondemand tool so discovery is enabled
	server.RegisterOnDemandTool(
		NewTool("dummy", "Dummy"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("OK"), nil
		},
	)

	// Create provider with a tool that has keywords
	provider := NewMockToolProvider()
	provider.AddTool("user_preferences", "Get user preferences", func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
		return NewToolResponseText("Preferences loaded"), nil
	}, "user", "settings", "preferences", "config")

	// Search for the provider tool in force ondemand mode (provider tools are searchable in force ondemand mode)
	ctx := WithForceOnDemandMode(context.Background(), provider)
	response, err := server.CallTool(ctx, ToolSearchName, map[string]interface{}{
		"query": "preferences",
	})
	if err != nil {
		t.Fatalf("tool_search failed: %v", err)
	}

	if !strings.Contains(response.Content[0].Text, "user_preferences") {
		t.Errorf("tool_search should find user_preferences in force ondemand mode, got: %s", response.Content[0].Text)
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

	// Register ondemand tool
	server.RegisterOnDemandTool(
		NewTool("ondemand", "Ondemand tool"),
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
		t.Error("tool_search should appear when ondemand tools exist")
	}

	// Test force ondemand mode
	forceHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := WithForceOnDemandMode(r.Context())
		server.HandleRequest(w, r.WithContext(ctx))
	})

	req = httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	forceHandler.ServeHTTP(rec, req)

	json.NewDecoder(rec.Body).Decode(&resp)
	result = resp.Result.(map[string]interface{})
	tools = result["tools"].([]interface{})

	// Should only have tool_search and execute_tool
	for _, toolItem := range tools {
		tool := toolItem.(map[string]interface{})
		name := tool["name"].(string)
		if name != ToolSearchName && name != ExecuteToolName {
			t.Errorf("in force ondemand mode, only discovery tools should appear, found: %s", name)
		}
	}
}

// TestToolSearchEmptyQueryListsAllOnDemandTools tests that empty query lists all tools
func TestToolSearchEmptyQueryListsAllOnDemandTools(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Register multiple ondemand tools
	for i := 0; i < 5; i++ {
		name := "tool_" + string(rune('a'+i))
		server.RegisterOnDemandTool(
			NewTool(name, "Tool "+name),
			func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
				return NewToolResponseText("OK"), nil
			},
		)
	}

	// Search with empty query - request enough results to get all tools
	response, err := server.CallTool(context.Background(), ToolSearchName, map[string]interface{}{
		"max_results": 10, // Request more than the 5 ondemand + 2 native tools
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

	// Register an ondemand tool to enable discovery
	server.RegisterOnDemandTool(
		NewTool("exists", "Tool that exists"),
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
		{ToolVisibilityOnDemand, "ondemand"},
		{ToolVisibility(99), "unknown"},
	}

	for _, tt := range tests {
		got := tt.v.String()
		if got != tt.want {
			t.Errorf("ToolVisibility(%d).String() = %q, want %q", tt.v, got, tt.want)
		}
	}
}
