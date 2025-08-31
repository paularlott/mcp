package mcp

import (
	"context"
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

	tools := s.ListTools()
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
