package openai

import (
	"bytes"
	"context"
	"testing"
)

type recordingToolHandler struct {
	calls   []string
	results []string
}

func (r *recordingToolHandler) OnToolCall(toolCall ToolCall) error {
	r.calls = append(r.calls, toolCall.Function.Name)
	return nil
}

func (r *recordingToolHandler) OnToolResult(toolCallID, toolName, result string) error {
	r.results = append(r.results, result)
	return nil
}

func TestWithToolHandler_And_ToolHandlerFromContext(t *testing.T) {
	if got := ToolHandlerFromContext(context.Background()); got != nil {
		t.Errorf("expected nil handler on bare context, got %v", got)
	}

	h := &recordingToolHandler{}
	ctx := WithToolHandler(context.Background(), h)
	got := ToolHandlerFromContext(ctx)
	if got != h {
		t.Errorf("ToolHandlerFromContext() = %v, want %v", got, h)
	}
}

func TestGenerateToolCallID_HasPrefix(t *testing.T) {
	id := GenerateToolCallID(3)
	if !hasPrefix(id, "call_") {
		t.Errorf("id = %q, want call_ prefix", id)
	}
}

func TestNoOpToolHandler(t *testing.T) {
	var h NoOpToolHandler
	if err := h.OnToolCall(ToolCall{}); err != nil {
		t.Errorf("OnToolCall() = %v, want nil", err)
	}
	if err := h.OnToolResult("1", "f", "r"); err != nil {
		t.Errorf("OnToolResult() = %v, want nil", err)
	}
}

func TestWithSSEEventWriter_And_FromContext(t *testing.T) {
	if got := SSEEventWriterFromContext(context.Background()); got != nil {
		t.Errorf("expected nil writer on bare context, got %v", got)
	}

	var buf bytes.Buffer
	w := NewSimpleSSEWriter(&buf, nil)
	ctx := WithSSEEventWriter(context.Background(), w)
	got := SSEEventWriterFromContext(ctx)
	if got != w {
		t.Errorf("SSEEventWriterFromContext() = %v, want %v", got, w)
	}
}

func TestToolHandlerFromContext_WrongType(t *testing.T) {
	// Store a value under a different key type to make sure type assertion path
	// (v.(ToolHandler) failing) is exercised indirectly — using context.WithValue
	// with the unexported key isn't possible from here, so instead verify that a
	// context without the handler set returns nil (already covered above) and
	// that setting then overwriting works.
	h1 := &recordingToolHandler{}
	h2 := NoOpToolHandler{}
	ctx := WithToolHandler(context.Background(), h1)
	ctx = WithToolHandler(ctx, h2)
	got := ToolHandlerFromContext(ctx)
	if got != h2 {
		t.Errorf("expected overwritten handler, got %v", got)
	}
}

func TestRecordingToolHandlerSatisfiesInterfaceNoErrors(t *testing.T) {
	h := &recordingToolHandler{}
	if err := h.OnToolCall(ToolCall{Function: ToolCallFunction{Name: "f"}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := h.OnToolResult("1", "f", "ok"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(h.calls) != 1 || h.calls[0] != "f" {
		t.Errorf("calls = %v", h.calls)
	}
	if len(h.results) != 1 || h.results[0] != "ok" {
		t.Errorf("results = %v", h.results)
	}
}
