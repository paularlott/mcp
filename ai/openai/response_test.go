package openai

import (
	"testing"

	"github.com/paularlott/mcp"
)

func TestExtractToolResult_Nil(t *testing.T) {
	result, err := ExtractToolResult(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Tool executed successfully" {
		t.Errorf("result = %q", result)
	}
}

func TestExtractToolResult_StructuredContent(t *testing.T) {
	resp := &mcp.ToolResponse{StructuredContent: map[string]any{"answer": 42}}
	result, err := ExtractToolResult(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != `{"answer":42}` {
		t.Errorf("result = %q", result)
	}
}

func TestExtractToolResult_TextContent(t *testing.T) {
	resp := &mcp.ToolResponse{Content: []mcp.ToolContent{
		{Type: "image", Data: "xxx"},
		{Type: "text", Text: "hello"},
	}}
	result, err := ExtractToolResult(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello" {
		t.Errorf("result = %q, want hello", result)
	}
}

func TestExtractToolResult_NoContent(t *testing.T) {
	resp := &mcp.ToolResponse{}
	result, err := ExtractToolResult(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Tool executed successfully" {
		t.Errorf("result = %q", result)
	}
}

func TestExtractAllTextContent_Nil(t *testing.T) {
	if got := ExtractAllTextContent(nil); got != "" {
		t.Errorf("got = %q, want empty", got)
	}
}

func TestExtractAllTextContent_MultiplePartsJoinedWithNewline(t *testing.T) {
	resp := &mcp.ToolResponse{Content: []mcp.ToolContent{
		{Type: "text", Text: "line1"},
		{Type: "image", Data: "ignored"},
		{Type: "text", Text: "line2"},
	}}
	got := ExtractAllTextContent(resp)
	if got != "line1\nline2" {
		t.Errorf("got = %q, want %q", got, "line1\nline2")
	}
}

func TestExtractAllTextContent_NoTextParts(t *testing.T) {
	resp := &mcp.ToolResponse{Content: []mcp.ToolContent{{Type: "image"}}}
	if got := ExtractAllTextContent(resp); got != "" {
		t.Errorf("got = %q, want empty", got)
	}
}
