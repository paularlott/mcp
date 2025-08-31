package mcp

import (
	"context"
	"testing"
)

// Registering a tool with the same name should replace the cache entry and not create duplicates.
// The latest registration also replaces the handler.
func TestDuplicateToolRegistration_ReplacesCacheAndHandler(t *testing.T) {
	s := NewServer("s", "1")
	s.RegisterTool(NewTool("dup", "first"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("1"), nil
	})
	s.RegisterTool(NewTool("dup", "second"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("2"), nil
	})

	tools := s.ListTools()
	count := 0
	for _, tl := range tools {
		if tl.Name == "dup" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected single 'dup' tool, got %d", count)
	}

	// Ensure handler replaced: calling tool should return "2"
	resp, err := s.CallTool(context.Background(), "dup", nil)
	if err != nil {
		t.Fatalf("call dup: %v", err)
	}
	if resp.Content[0].Text != "2" {
		t.Fatalf("expected latest handler to win, got %v", resp)
	}
}
