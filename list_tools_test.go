package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListTools_OutputSchemaAndOrdering(t *testing.T) {
	s := NewServer("s", "1")
	// Register out of order intentionally
	s.RegisterTool(NewTool("delta", "d"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("d"), nil
	})
	s.RegisterTool(NewTool("alpha", "a", Output(String("id", "", Required()))), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("a"), nil
	})
	s.RegisterTool(NewTool("charlie", "c"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("c"), nil
	})

	tools := s.ListToolsWithContext(context.Background())
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools))
	}
	// Should be alphabetically sorted by name
	if tools[0].Name != "alpha" || tools[1].Name != "charlie" || tools[2].Name != "delta" {
		t.Fatalf("unexpected order: %+v", tools)
	}
	// Output schema included for alpha
	if tools[0].OutputSchema == nil {
		t.Fatalf("expected output schema for alpha")
	}
}

// TestClientListTools_PreservesMetaAndIcons guards against a regression
// where Client.ListTools rebuilt each MCPTool from scratch (both in its
// direct-type-assertion fast path and its namespacing pass), silently
// dropping _meta and icons for every federated tool — which would break MCP
// Apps passthrough entirely, since a federating server's tools/list is the
// only place a remote client observes a tool's UI/icon metadata.
func TestClientListTools_PreservesMetaAndIcons(t *testing.T) {
	remote := NewServer("remote", "0.0.1")
	remote.RegisterTool(
		NewTool("sales_report", "Get the sales report").
			UIResource("ui://dashboard/dashboard.html", "model", "app").
			Icons(Icon{Src: "https://example.com/icon.png", MimeType: "image/png"}),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("ok"), nil
		},
	)
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer ts.Close()

	client := NewClient(ts.URL, nil, "")
	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("client.Initialize: %v", err)
	}

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	tool := tools[0]
	if len(tool.Icons) != 1 || tool.Icons[0].Src != "https://example.com/icon.png" {
		t.Fatalf("expected icons to survive federation, got: %+v", tool.Icons)
	}
	uiMeta, ok := tool.Meta["ui"].(map[string]any)
	if !ok {
		t.Fatalf("expected _meta.ui to survive federation, got: %+v", tool.Meta)
	}
	if uiMeta["resourceUri"] != "ui://dashboard/dashboard.html" {
		t.Fatalf("unexpected resourceUri: %+v", uiMeta["resourceUri"])
	}
}

// testServerNativeProvider is a ToolProvider for server integration tests
type testServerNativeProvider struct {
	tools []MCPTool
}

func (p *testServerNativeProvider) GetTools(ctx context.Context) ([]MCPTool, error) {
	return p.tools, nil
}

func (p *testServerNativeProvider) ExecuteTool(ctx context.Context, name string, params map[string]any) (*ToolResponse, error) {
	for _, tool := range p.tools {
		if tool.Name == name {
			return NewToolResponseText("provider:" + name), nil
		}
	}
	return nil, nil // Not handled
}

func TestListToolsWithContext_IncludesProviderTools(t *testing.T) {
	s := NewServer("test", "1.0")

	// Register a static tool
	s.RegisterTool(NewTool("static_tool", "A static tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("static"), nil
	})

	// Create a provider with dynamic tools
	provider := &testServerNativeProvider{
		tools: []MCPTool{
			{Name: "dynamic_tool", Description: "From provider"},
			{Name: "zebra_tool", Description: "Last alphabetically"},
		},
	}

	// Without context, should only have static tool
	toolsWithoutCtx := s.ListToolsWithContext(context.Background())
	if len(toolsWithoutCtx) != 1 {
		t.Fatalf("expected 1 static tool, got %d", len(toolsWithoutCtx))
	}

	// With context containing provider, should have all tools (no discovery tools in normal mode)
	ctx := WithToolProviders(context.Background(), provider)
	toolsWithCtx := s.ListToolsWithContext(ctx)
	// Should have: static_tool, dynamic_tool, zebra_tool = 3 tools (provider tools are native in normal mode)
	if len(toolsWithCtx) != 3 {
		t.Fatalf("expected 3 tools (1 static + 2 from provider), got %d", len(toolsWithCtx))
	}

	// Verify provider tools are present as native tools
	foundDynamic := false
	foundZebra := false
	for _, tool := range toolsWithCtx {
		if tool.Name == "dynamic_tool" {
			foundDynamic = true
		}
		if tool.Name == "zebra_tool" {
			foundZebra = true
		}
	}
	if !foundDynamic {
		t.Error("dynamic_tool should be present as a native tool")
	}
	if !foundZebra {
		t.Error("zebra_tool should be present as a native tool")
	}
}

func TestCallTool_TriesProviders(t *testing.T) {
	s := NewServer("test", "1.0")

	// Register a static tool
	s.RegisterTool(NewTool("static_tool", "A static tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("static"), nil
	})

	// Create a provider
	provider := &testServerNativeProvider{
		tools: []MCPTool{
			{Name: "provider_tool", Description: "From provider"},
		},
	}

	ctx := WithToolProviders(context.Background(), provider)

	// Call static tool - should work
	resp, err := s.CallTool(ctx, "static_tool", nil)
	if err != nil {
		t.Fatalf("failed to call static tool: %v", err)
	}
	if resp.Content[0].Text != "static" {
		t.Errorf("unexpected response: %v", resp.Content[0].Text)
	}

	// Call provider tool - should work via provider
	resp, err = s.CallTool(ctx, "provider_tool", nil)
	if err != nil {
		t.Fatalf("failed to call provider tool: %v", err)
	}
	if resp.Content[0].Text != "provider:provider_tool" {
		t.Errorf("unexpected response: %v", resp.Content[0].Text)
	}

	// Call unknown tool - should fail
	_, err = s.CallTool(ctx, "unknown_tool", nil)
	if err != ErrUnknownTool {
		t.Errorf("expected ErrUnknownTool, got %v", err)
	}
}

func TestListToolsWithContext_DeduplicatesWithStatic(t *testing.T) {
	s := NewServer("test", "1.0")

	// Register a static tool
	s.RegisterTool(NewTool("shared_name", "Static version"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("static"), nil
	})

	// Provider has tool with same name
	provider := &testServerNativeProvider{
		tools: []MCPTool{
			{Name: "shared_name", Description: "Provider version - should be ignored"},
			{Name: "unique_from_provider", Description: "Only in provider"},
		},
	}

	ctx := WithToolProviders(context.Background(), provider)
	tools := s.ListToolsWithContext(ctx)

	// Should have 2 tools (shared_name + unique_from_provider) - no discovery tools in normal mode
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}

	// Static version should win (first in seen map)
	for _, tool := range tools {
		if tool.Name == "shared_name" && tool.Description != "Static version" {
			t.Error("expected static tool to take precedence over provider tool")
		}
	}
}
