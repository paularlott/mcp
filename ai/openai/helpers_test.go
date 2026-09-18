package openai

import (
	"errors"
	"testing"
)

func TestFloat64Ptr(t *testing.T) {
	p := Float64Ptr(3.14)
	if p == nil || *p != 3.14 {
		t.Fatalf("Float64Ptr(3.14) = %v", p)
	}
}

func TestExecuteToolCalls_Success(t *testing.T) {
	calls := []ToolCall{
		{ID: "1", Function: ToolCallFunction{Name: "f1", Arguments: map[string]any{}}},
		{ID: "2", Function: ToolCallFunction{Name: "f2", Arguments: map[string]any{}}},
	}
	executor := func(name string, args map[string]any) (string, error) {
		return "result-" + name, nil
	}
	msgs, err := ExecuteToolCalls(calls, executor, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len(msgs) = %d, want 2", len(msgs))
	}
	if msgs[0].Content != "result-f1" || msgs[0].ToolCallID != "1" {
		t.Errorf("msgs[0] = %+v", msgs[0])
	}
	if msgs[0].Role != "tool" {
		t.Errorf("Role = %q, want tool", msgs[0].Role)
	}
}

func TestExecuteToolCalls_Empty(t *testing.T) {
	msgs, err := ExecuteToolCalls(nil, nil, false)
	if err != nil || msgs != nil {
		t.Errorf("ExecuteToolCalls(nil) = %v, %v", msgs, err)
	}
}

func TestExecuteToolCalls_ErrorContinues(t *testing.T) {
	calls := []ToolCall{
		{ID: "1", Function: ToolCallFunction{Name: "f1"}},
		{ID: "2", Function: ToolCallFunction{Name: "f2"}},
	}
	executor := func(name string, args map[string]any) (string, error) {
		if name == "f1" {
			return "", errors.New("boom")
		}
		return "ok", nil
	}
	msgs, err := ExecuteToolCalls(calls, executor, false)
	if err != nil {
		t.Fatalf("unexpected error (stopOnError=false): %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len(msgs) = %d, want 2", len(msgs))
	}
	if msgs[0].Content != "Error: boom" {
		t.Errorf("msgs[0].Content = %q", msgs[0].Content)
	}
	if msgs[1].Content != "ok" {
		t.Errorf("msgs[1].Content = %q", msgs[1].Content)
	}
}

func TestExecuteToolCalls_StopOnError(t *testing.T) {
	calls := []ToolCall{
		{ID: "1", Function: ToolCallFunction{Name: "f1"}},
		{ID: "2", Function: ToolCallFunction{Name: "f2"}},
	}
	executor := func(name string, args map[string]any) (string, error) {
		if name == "f1" {
			return "", errors.New("boom")
		}
		return "ok", nil
	}
	msgs, err := ExecuteToolCalls(calls, executor, true)
	if err == nil {
		t.Fatal("expected error with stopOnError=true")
	}
	var toolErr *ToolExecutionError
	if !errors.As(err, &toolErr) {
		t.Fatalf("expected *ToolExecutionError, got %T", err)
	}
	if toolErr.ToolName != "f1" || toolErr.ToolID != "1" {
		t.Errorf("toolErr = %+v", toolErr)
	}
	if len(msgs) != 0 {
		t.Errorf("len(msgs) = %d, want 0 (returned before append)", len(msgs))
	}
}

func TestExecuteToolCall_Success(t *testing.T) {
	tc := ToolCall{ID: "1", Function: ToolCallFunction{Name: "f"}}
	msg, err := ExecuteToolCall(tc, func(name string, args map[string]any) (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Content != "ok" || msg.ToolCallID != "1" || msg.Role != "tool" {
		t.Errorf("msg = %+v", msg)
	}
}

func TestExecuteToolCall_Error(t *testing.T) {
	tc := ToolCall{ID: "1", Function: ToolCallFunction{Name: "f"}}
	_, err := ExecuteToolCall(tc, func(name string, args map[string]any) (string, error) {
		return "", errors.New("boom")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var toolErr *ToolExecutionError
	if !errors.As(err, &toolErr) {
		t.Fatalf("expected *ToolExecutionError, got %T", err)
	}
}

func TestBuildToolResultMessage(t *testing.T) {
	msg := BuildToolResultMessage("call_1", "the result")
	if msg.Role != "tool" || msg.Content != "the result" || msg.ToolCallID != "call_1" {
		t.Errorf("msg = %+v", msg)
	}
}

func TestBuildAssistantToolCallMessage(t *testing.T) {
	tcs := []ToolCall{{ID: "1", Function: ToolCallFunction{Name: "f"}}}
	msg := BuildAssistantToolCallMessage("thinking", tcs)
	if msg.Role != "assistant" || msg.Content != "thinking" {
		t.Errorf("msg = %+v", msg)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Type != "function" {
		t.Errorf("ToolCalls = %+v, want Type=function defaulted", msg.ToolCalls)
	}
}

func TestBuildAssistantToolCallMessage_PreservesExplicitType(t *testing.T) {
	tcs := []ToolCall{{ID: "1", Type: "custom", Function: ToolCallFunction{Name: "f"}}}
	msg := BuildAssistantToolCallMessage("", tcs)
	if msg.ToolCalls[0].Type != "custom" {
		t.Errorf("Type = %q, want custom preserved", msg.ToolCalls[0].Type)
	}
}

func TestBuildUserMessage(t *testing.T) {
	msg := BuildUserMessage("hi")
	if msg.Role != "user" || msg.Content != "hi" {
		t.Errorf("msg = %+v", msg)
	}
}

func TestBuildSystemMessage(t *testing.T) {
	msg := BuildSystemMessage("be helpful")
	if msg.Role != "system" || msg.Content != "be helpful" {
		t.Errorf("msg = %+v", msg)
	}
}

func TestBuildAssistantMessage(t *testing.T) {
	msg := BuildAssistantMessage("hello")
	if msg.Role != "assistant" || msg.Content != "hello" {
		t.Errorf("msg = %+v", msg)
	}
}

func TestParseToolArguments_Success(t *testing.T) {
	type args struct {
		City string `json:"city"`
	}
	var a args
	err := ParseToolArguments(map[string]any{"city": "NYC"}, &a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.City != "NYC" {
		t.Errorf("City = %q, want NYC", a.City)
	}
}

func TestParseToolArguments_UnmarshalError(t *testing.T) {
	type args struct {
		City int `json:"city"` // type mismatch triggers unmarshal error
	}
	var a args
	err := ParseToolArguments(map[string]any{"city": "NYC"}, &a)
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
}

func TestMustParseToolArguments_Success(t *testing.T) {
	type args struct {
		City string `json:"city"`
	}
	var a args
	// should not panic
	MustParseToolArguments(map[string]any{"city": "NYC"}, &a)
	if a.City != "NYC" {
		t.Errorf("City = %q", a.City)
	}
}

func TestMustParseToolArguments_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on invalid arguments")
		}
	}()
	type args struct {
		City int `json:"city"`
	}
	var a args
	MustParseToolArguments(map[string]any{"city": "NYC"}, &a)
}

func TestTextContentPart(t *testing.T) {
	cp := TextContentPart("hello")
	if cp.Type != "text" || cp.Text != "hello" {
		t.Errorf("cp = %+v", cp)
	}
}

func TestImageURLContentPart(t *testing.T) {
	cp := ImageURLContentPart("http://example.com/img.png", "high")
	if cp.Type != "image_url" {
		t.Errorf("Type = %q", cp.Type)
	}
	if cp.ImageURL == nil || cp.ImageURL.URL != "http://example.com/img.png" || cp.ImageURL.Detail != "high" {
		t.Errorf("ImageURL = %+v", cp.ImageURL)
	}
}

func TestImageURLContentPart_NoDetail(t *testing.T) {
	cp := ImageURLContentPart("http://example.com/img.png", "")
	if cp.ImageURL.Detail != "" {
		t.Errorf("Detail = %q, want empty", cp.ImageURL.Detail)
	}
}

func TestImageBase64ContentPart(t *testing.T) {
	cp := ImageBase64ContentPart("QUJD", "image/png", "low")
	want := "data:image/png;base64,QUJD"
	if cp.ImageURL.URL != want {
		t.Errorf("URL = %q, want %q", cp.ImageURL.URL, want)
	}
	if cp.ImageURL.Detail != "low" {
		t.Errorf("Detail = %q, want low", cp.ImageURL.Detail)
	}
}

func TestBuildMultimodalMessage(t *testing.T) {
	msg := BuildMultimodalMessage(TextContentPart("hi"), ImageURLContentPart("u", ""))
	if msg.Role != "user" {
		t.Errorf("Role = %q, want user", msg.Role)
	}
	parts, ok := msg.Content.([]ContentPart)
	if !ok || len(parts) != 2 {
		t.Fatalf("Content = %#v", msg.Content)
	}
	if parts[0].Text != "hi" {
		t.Errorf("parts[0] = %+v", parts[0])
	}
}

func TestNewTool(t *testing.T) {
	params := map[string]any{"type": "object"}
	tool := NewTool("f", "desc", params)
	if tool.Type != "function" {
		t.Errorf("Type = %q, want function", tool.Type)
	}
	if tool.Function.Name != "f" || tool.Function.Description != "desc" {
		t.Errorf("Function = %+v", tool.Function)
	}
	if tool.Function.Parameters["type"] != "object" {
		t.Errorf("Parameters = %+v", tool.Function.Parameters)
	}
}

func TestHasToolCalls(t *testing.T) {
	if HasToolCalls(Message{}) {
		t.Error("HasToolCalls(empty) = true, want false")
	}
	msg := Message{ToolCalls: []ToolCall{{ID: "1"}}}
	if !HasToolCalls(msg) {
		t.Error("HasToolCalls(with calls) = false, want true")
	}
}

func TestGetToolNames(t *testing.T) {
	tcs := []ToolCall{
		{Function: ToolCallFunction{Name: "f1"}},
		{Function: ToolCallFunction{Name: "f2"}},
	}
	names := GetToolNames(tcs)
	if len(names) != 2 || names[0] != "f1" || names[1] != "f2" {
		t.Errorf("names = %v", names)
	}
}

func TestGetToolNames_Empty(t *testing.T) {
	names := GetToolNames(nil)
	if len(names) != 0 {
		t.Errorf("names = %v, want empty", names)
	}
}
