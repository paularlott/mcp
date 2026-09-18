package openai

import (
	"testing"

	"github.com/paularlott/mcp"
)

func TestMCPToolsToOpenAI(t *testing.T) {
	tools := []mcp.MCPTool{
		{Name: "search", Description: "search the web", InputSchema: map[string]any{"type": "object"}},
		{Name: "calc", Description: "calculator", InputSchema: nil},
	}
	got := MCPToolsToOpenAI(tools)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].Type != "function" || got[0].Function.Name != "search" {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[0].Function.Parameters["type"] != "object" {
		t.Errorf("Parameters = %+v", got[0].Function.Parameters)
	}
	// nil InputSchema should become an empty (non-nil) map
	if got[1].Function.Parameters == nil {
		t.Errorf("Parameters for nil schema = nil, want empty map")
	}
}

func TestMCPToolsToOpenAI_NonMapSchema(t *testing.T) {
	tools := []mcp.MCPTool{
		{Name: "weird", Description: "", InputSchema: "not-a-map"},
	}
	got := MCPToolsToOpenAI(tools)
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].Function.Parameters == nil {
		t.Error("expected empty map, got nil")
	}
}

func TestMCPToolsToOpenAIFiltered(t *testing.T) {
	tools := []mcp.MCPTool{
		{Name: "search"},
		{Name: "calc"},
		{Name: "weather"},
	}
	got := MCPToolsToOpenAIFiltered(tools, func(name string) bool { return name != "calc" })
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	names := map[string]bool{}
	for _, tl := range got {
		names[tl.Function.Name] = true
	}
	if names["calc"] {
		t.Error("calc should have been filtered out")
	}
	if !names["search"] || !names["weather"] {
		t.Errorf("got = %+v", got)
	}
}

func TestMCPToolsToOpenAIFiltered_NilFilterIncludesAll(t *testing.T) {
	tools := []mcp.MCPTool{{Name: "a"}, {Name: "b"}}
	got := MCPToolsToOpenAIFiltered(tools, nil)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
}

func TestAllTools(t *testing.T) {
	filter := AllTools()
	if filter != nil {
		t.Errorf("AllTools() = %v, want nil", filter)
	}
}

func TestToolsByName(t *testing.T) {
	filter := ToolsByName("a", "b")
	if !filter("a") || !filter("b") {
		t.Error("expected a and b to pass filter")
	}
	if filter("c") {
		t.Error("expected c to fail filter")
	}
}

func TestToolsByName_Empty(t *testing.T) {
	filter := ToolsByName()
	if filter("anything") {
		t.Error("expected empty ToolsByName to exclude everything")
	}
}

func TestExcludeTools(t *testing.T) {
	filter := ExcludeTools("a", "b")
	if filter("a") || filter("b") {
		t.Error("expected a and b to be excluded")
	}
	if !filter("c") {
		t.Error("expected c to pass filter")
	}
}

func TestExcludeTools_Empty(t *testing.T) {
	filter := ExcludeTools()
	if !filter("anything") {
		t.Error("expected empty ExcludeTools to include everything")
	}
}
