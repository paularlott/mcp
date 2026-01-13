package discovery

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/paularlott/mcp"
)

func TestToolRegistry_RegisterAndSearch(t *testing.T) {
	registry := NewToolRegistry()

	// Register some searchable tools
	registry.RegisterTool(
		mcp.NewTool("analyze_data", "Analyze datasets with statistical methods"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("analyzed"), nil
		},
		"statistics", "data", "analysis",
	)

	registry.RegisterTool(
		mcp.NewTool("generate_report", "Generate PDF reports from data"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("generated"), nil
		},
		"pdf", "report", "export",
	)

	ctx := context.Background()

	// Search for statistics
	results := registry.Search(ctx, "statistics", 10)
	if len(results) != 1 || results[0].Name != "analyze_data" {
		t.Fatalf("Expected analyze_data in results: %v", results)
	}

	// Search for report
	results = registry.Search(ctx, "report", 10)
	if len(results) != 1 || results[0].Name != "generate_report" {
		t.Fatalf("Expected generate_report in results: %v", results)
	}
}

func TestToolRegistry_GetTool(t *testing.T) {
	registry := NewToolRegistry()

	registry.RegisterTool(
		mcp.NewTool("test_tool", "A test tool",
			mcp.String("input", "Input parameter", mcp.Required()),
		),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("result"), nil
		},
		"test",
	)

	ctx := context.Background()

	// Get existing tool
	tool, err := registry.GetTool(ctx, "test_tool")
	if err != nil {
		t.Fatalf("GetTool failed: %v", err)
	}
	if tool.Name != "test_tool" {
		t.Fatalf("Expected test_tool, got %s", tool.Name)
	}

	// Get non-existent tool
	_, err = registry.GetTool(ctx, "nonexistent")
	if err != ErrToolNotFound {
		t.Fatalf("Expected ErrToolNotFound, got %v", err)
	}
}

func TestToolRegistry_CallTool(t *testing.T) {
	registry := NewToolRegistry()

	registry.RegisterTool(
		mcp.NewTool("greeter", "Greet someone"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("Hello!"), nil
		},
		"greet",
	)

	ctx := context.Background()

	// Call existing tool
	response, err := registry.CallTool(ctx, "greeter", nil)
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if response.Content[0].Text != "Hello!" {
		t.Fatalf("Expected 'Hello!', got %s", response.Content[0].Text)
	}

	// Call non-existent tool
	_, err = registry.CallTool(ctx, "nonexistent", nil)
	if err != ErrToolNotFound {
		t.Fatalf("Expected ErrToolNotFound, got %v", err)
	}
}

func TestToolRegistry_Attach(t *testing.T) {
	server := mcp.NewServer("test", "1.0.0")
	registry := NewToolRegistry()

	// Register a searchable tool
	registry.RegisterTool(
		mcp.NewTool("hidden_calculator", "Perform calculations"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("calculated"), nil
		},
		"math", "add",
	)

	// Attach to server
	registry.Attach(server)

	// Verify the discovery tools are registered
	tools := server.ListTools()
	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}

	if !toolNames["tool_search"] {
		t.Fatalf("Expected tool_search to be registered")
	}
	if !toolNames["execute_tool"] {
		t.Fatalf("Expected execute_tool to be registered")
	}

	// Verify hidden_calculator is NOT in the tools list
	if toolNames["hidden_calculator"] {
		t.Fatalf("hidden_calculator should NOT be in the tools list")
	}
}

func TestToolRegistry_ToolSearch(t *testing.T) {
	server := mcp.NewServer("test", "1.0.0")
	registry := NewToolRegistry()

	registry.RegisterTool(
		mcp.NewTool("send_email", "Send an email"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("sent"), nil
		},
		"email", "notification",
	)

	registry.Attach(server)

	ctx := context.Background()

	// Call tool_search
	response, err := server.CallTool(ctx, "tool_search", map[string]interface{}{
		"query": "email",
	})
	if err != nil {
		t.Fatalf("tool_search failed: %v", err)
	}

	var results []SearchResult
	if err := json.Unmarshal([]byte(response.Content[0].Text), &results); err != nil {
		t.Fatalf("Failed to parse tool_search response: %v", err)
	}

	if len(results) != 1 || results[0].Name != "send_email" {
		t.Fatalf("Expected send_email in results: %v", results)
	}
}

func TestToolRegistry_SearchIncludesSchema(t *testing.T) {
	server := mcp.NewServer("test", "1.0.0")
	registry := NewToolRegistry()

	registry.RegisterTool(
		mcp.NewTool("complex_tool", "A tool with parameters",
			mcp.String("name", "Name parameter", mcp.Required()),
			mcp.Number("count", "Count parameter"),
		),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("done"), nil
		},
		"complex",
	)

	registry.Attach(server)

	ctx := context.Background()

	// Call tool_search - should include schema in results
	response, err := server.CallTool(ctx, "tool_search", map[string]interface{}{
		"query": "complex",
	})
	if err != nil {
		t.Fatalf("tool_search failed: %v", err)
	}

	var results []SearchResult
	if err := json.Unmarshal([]byte(response.Content[0].Text), &results); err != nil {
		t.Fatalf("Failed to parse tool_search response: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	if results[0].Name != "complex_tool" {
		t.Fatalf("Expected complex_tool, got %s", results[0].Name)
	}

	// Verify schema is included
	if results[0].InputSchema == nil {
		t.Fatalf("Expected InputSchema to be included in search results")
	}

	// Verify schema has properties
	schema, ok := results[0].InputSchema.(map[string]interface{})
	if !ok {
		t.Fatalf("InputSchema should be a map")
	}
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("Schema should have properties")
	}
	if _, ok := props["name"]; !ok {
		t.Fatalf("Schema should have 'name' property")
	}
}

func TestToolRegistry_ExecuteTool(t *testing.T) {
	server := mcp.NewServer("test", "1.0.0")
	registry := NewToolRegistry()

	registry.RegisterTool(
		mcp.NewTool("hidden_greeter", "Greet someone"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("Hello from hidden tool!"), nil
		},
		"greet",
	)

	registry.Attach(server)

	ctx := context.Background()

	// Execute the hidden tool through execute_tool
	response, err := server.CallTool(ctx, "execute_tool", map[string]interface{}{
		"name":      "hidden_greeter",
		"arguments": map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("execute_tool failed: %v", err)
	}

	if response.Content[0].Text != "Hello from hidden tool!" {
		t.Fatalf("Expected 'Hello from hidden tool!', got: %s", response.Content[0].Text)
	}

	// Test with non-existent tool
	response, err = server.CallTool(ctx, "execute_tool", map[string]interface{}{
		"name":      "nonexistent_tool",
		"arguments": map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("execute_tool should not error for nonexistent tool: %v", err)
	}
	if !strings.Contains(response.Content[0].Text, "Tool not found") {
		t.Fatalf("Expected 'Tool not found' message, got: %s", response.Content[0].Text)
	}
}

func TestToolRegistry_FuzzySearch(t *testing.T) {
	registry := NewToolRegistry()

	registry.RegisterTool(
		mcp.NewTool("kubernetes_deploy", "Deploy to Kubernetes"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("deployed"), nil
		},
		"k8s", "container", "deploy",
	)

	ctx := context.Background()

	// Search by keyword should find it
	results := registry.Search(ctx, "k8s", 10)
	if len(results) == 0 {
		t.Fatalf("Search should find kubernetes_deploy by keyword")
	}
	if results[0].Name != "kubernetes_deploy" {
		t.Fatalf("Expected kubernetes_deploy, got %s", results[0].Name)
	}

	// Search by partial name should find it
	results = registry.Search(ctx, "kubernetes", 10)
	if len(results) == 0 {
		t.Fatalf("Search should find kubernetes_deploy by name")
	}
}

func TestToolRegistry_MaxResults(t *testing.T) {
	registry := NewToolRegistry()

	// Register many tools with common keyword
	for i := 0; i < 20; i++ {
		name := "tool_" + string(rune('a'+i))
		registry.RegisterTool(
			mcp.NewTool(name, "Test tool with common keyword"),
			func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
				return mcp.NewToolResponseText("result"), nil
			},
			"common",
		)
	}

	ctx := context.Background()

	// Search with max results
	results := registry.Search(ctx, "common", 5)
	if len(results) != 5 {
		t.Fatalf("Expected 5 results with maxResults=5, got %d", len(results))
	}
}

// MockToolProvider for testing dynamic providers
type MockToolProvider struct {
	tools map[string]*mockTool
}

type mockTool struct {
	name        string
	description string
	keywords    []string
	handler     mcp.ToolHandler
}

func NewMockToolProvider() *MockToolProvider {
	return &MockToolProvider{
		tools: make(map[string]*mockTool),
	}
}

func (p *MockToolProvider) AddTool(name, description string, keywords []string, handler mcp.ToolHandler) {
	p.tools[name] = &mockTool{
		name:        name,
		description: description,
		keywords:    keywords,
		handler:     handler,
	}
}

func (p *MockToolProvider) ListToolMetadata(ctx context.Context) ([]ToolMetadata, error) {
	var metadata []ToolMetadata
	for _, tool := range p.tools {
		metadata = append(metadata, ToolMetadata{
			Name:        tool.name,
			Description: tool.description,
			Keywords:    tool.keywords,
		})
	}
	return metadata, nil
}

func (p *MockToolProvider) GetTool(ctx context.Context, name string) (*mcp.MCPTool, error) {
	if tool, exists := p.tools[name]; exists {
		return &mcp.MCPTool{
			Name:        tool.name,
			Description: tool.description,
		}, nil
	}
	return nil, nil
}

func (p *MockToolProvider) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.ToolResponse, error) {
	if tool, exists := p.tools[name]; exists {
		return tool.handler(ctx, mcp.NewToolRequest(args))
	}
	return nil, ErrToolNotFound
}

func TestToolRegistry_AttachIdempotency(t *testing.T) {
	registry := NewToolRegistry()
	server := mcp.NewServer("test", "1.0")

	// First attach should succeed
	registry.Attach(server)

	// Get tool count after first attach
	toolsAfterFirst := len(server.ListTools())

	// Second attach should be idempotent (not panic, not duplicate tools)
	registry.Attach(server)

	// Tool count should be the same
	toolsAfterSecond := len(server.ListTools())
	if toolsAfterFirst != toolsAfterSecond {
		t.Fatalf("Expected %d tools after second attach, got %d", toolsAfterFirst, toolsAfterSecond)
	}

	// Third attach for good measure
	registry.Attach(server)
	toolsAfterThird := len(server.ListTools())
	if toolsAfterFirst != toolsAfterThird {
		t.Fatalf("Expected %d tools after third attach, got %d", toolsAfterFirst, toolsAfterThird)
	}
}

func TestToolRegistry_Constants(t *testing.T) {
	// Verify the constants are defined correctly
	if ToolSearchName != "tool_search" {
		t.Errorf("Expected ToolSearchName to be 'tool_search', got %q", ToolSearchName)
	}
	if ExecuteToolName != "execute_tool" {
		t.Errorf("Expected ExecuteToolName to be 'execute_tool', got %q", ExecuteToolName)
	}
}

func TestToolRegistry_WithProvider(t *testing.T) {
	registry := NewToolRegistry()

	provider := NewMockToolProvider()
	provider.AddTool("dynamic_calculator", "Perform calculations", []string{"math", "calculate"},
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("calculated"), nil
		})

	registry.AddProvider(provider)

	ctx := context.Background()

	// Search should find the dynamic tool
	results := registry.Search(ctx, "math", 10)
	if len(results) != 1 || results[0].Name != "dynamic_calculator" {
		t.Fatalf("Expected dynamic_calculator in results: %v", results)
	}

	// CallTool should work
	response, err := registry.CallTool(ctx, "dynamic_calculator", nil)
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if response.Content[0].Text != "calculated" {
		t.Fatalf("Expected 'calculated', got %s", response.Content[0].Text)
	}
}
