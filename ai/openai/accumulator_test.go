package openai

import "testing"

func TestCompletionAccumulator_AddChunkAndFinishedContent(t *testing.T) {
	acc := &CompletionAccumulator{}

	acc.AddChunk(ChatCompletionResponse{Choices: []Choice{
		{Index: 0, Delta: Delta{Content: "Hello, "}},
	}})
	acc.AddChunk(ChatCompletionResponse{Choices: []Choice{
		{Index: 0, Delta: Delta{Content: "world!"}},
	}})

	if content, complete := acc.FinishedContent(); complete || content != "" {
		t.Fatalf("expected incomplete before finish_reason, got %q, %v", content, complete)
	}
	if acc.Content() != "Hello, world!" {
		t.Fatalf("Content() = %q, want %q", acc.Content(), "Hello, world!")
	}

	acc.AddChunk(ChatCompletionResponse{Choices: []Choice{
		{Index: 0, FinishReason: "stop"},
	}})

	content, complete := acc.FinishedContent()
	if !complete {
		t.Fatal("expected complete=true after finish_reason stop")
	}
	if content != "Hello, world!" {
		t.Errorf("content = %q, want %q", content, "Hello, world!")
	}
	if acc.FinishReason() != "stop" {
		t.Errorf("FinishReason() = %q, want stop", acc.FinishReason())
	}
	if !acc.IsComplete() {
		t.Error("IsComplete() = false, want true")
	}
}

func TestCompletionAccumulator_EmptyAccumulator(t *testing.T) {
	acc := &CompletionAccumulator{}

	if content, ok := acc.FinishedContent(); ok || content != "" {
		t.Errorf("FinishedContent() on empty = %q, %v", content, ok)
	}
	if tc, ok := acc.FinishedToolCall(); ok || tc != nil {
		t.Errorf("FinishedToolCall() on empty = %v, %v", tc, ok)
	}
	if tcs, ok := acc.FinishedToolCalls(); ok || tcs != nil {
		t.Errorf("FinishedToolCalls() on empty = %v, %v", tcs, ok)
	}
	if refusal, ok := acc.FinishedRefusal(); ok || refusal != "" {
		t.Errorf("FinishedRefusal() on empty = %q, %v", refusal, ok)
	}
	if acc.Content() != "" {
		t.Errorf("Content() on empty = %q, want empty", acc.Content())
	}
	if acc.FinishReason() != "" {
		t.Errorf("FinishReason() on empty = %q, want empty", acc.FinishReason())
	}
	if acc.IsComplete() {
		t.Error("IsComplete() on empty = true, want false")
	}
}

func TestCompletionAccumulator_FinishedToolCall_Single(t *testing.T) {
	acc := &CompletionAccumulator{}
	acc.AddChunk(ChatCompletionResponse{Choices: []Choice{
		{Index: 0, Delta: Delta{ToolCalls: []DeltaToolCall{
			{Index: 0, ID: "call_1", Type: "function", Function: DeltaFunction{Name: "get_weather", Arguments: `{"city":`}},
		}}},
	}})
	acc.AddChunk(ChatCompletionResponse{Choices: []Choice{
		{Index: 0, Delta: Delta{ToolCalls: []DeltaToolCall{
			{Index: 0, Function: DeltaFunction{Arguments: `"NYC"}`}},
		}}},
	}})

	// Not complete yet - no finish reason
	if tc, ok := acc.FinishedToolCall(); ok || tc != nil {
		t.Fatalf("expected incomplete, got %v, %v", tc, ok)
	}

	acc.AddChunk(ChatCompletionResponse{Choices: []Choice{
		{Index: 0, FinishReason: "tool_calls"},
	}})

	tc, ok := acc.FinishedToolCall()
	if !ok || tc == nil {
		t.Fatal("expected complete tool call")
	}
	if tc.ID != "call_1" || tc.Function.Name != "get_weather" {
		t.Errorf("tc = %+v", tc)
	}
	if tc.Function.Arguments["city"] != "NYC" {
		t.Errorf("arguments = %#v, want city=NYC", tc.Function.Arguments)
	}
	if tc.Type != "function" {
		t.Errorf("Type = %q, want function", tc.Type)
	}
}

func TestCompletionAccumulator_FinishedToolCall_NoneAtIndex0(t *testing.T) {
	acc := &CompletionAccumulator{}
	// Choice 0 exists with a finish reason but with no tool call at index 0.
	acc.AddChunk(ChatCompletionResponse{Choices: []Choice{
		{Index: 0, FinishReason: "tool_calls"},
	}})
	if tc, ok := acc.FinishedToolCall(); ok || tc != nil {
		t.Errorf("expected no tool call, got %v, %v", tc, ok)
	}
}

func TestCompletionAccumulator_FinishedToolCalls_Multiple(t *testing.T) {
	acc := &CompletionAccumulator{}
	acc.AddChunk(ChatCompletionResponse{Choices: []Choice{
		{Index: 0, Delta: Delta{ToolCalls: []DeltaToolCall{
			{Index: 0, ID: "call_1", Function: DeltaFunction{Name: "f1", Arguments: `{}`}},
			{Index: 1, ID: "call_2", Function: DeltaFunction{Name: "f2", Arguments: `{}`}},
		}}},
	}})
	acc.AddChunk(ChatCompletionResponse{Choices: []Choice{
		{Index: 0, FinishReason: "tool_calls"},
	}})

	tcs, ok := acc.FinishedToolCalls()
	if !ok {
		t.Fatal("expected ok=true")
	}
	if len(tcs) != 2 {
		t.Fatalf("len(tcs) = %d, want 2", len(tcs))
	}
	if tcs[0].Function.Name != "f1" || tcs[1].Function.Name != "f2" {
		t.Errorf("tcs = %+v", tcs)
	}
}

func TestCompletionAccumulator_FinishedToolCalls_WrongFinishReason(t *testing.T) {
	acc := &CompletionAccumulator{}
	acc.AddChunk(ChatCompletionResponse{Choices: []Choice{
		{Index: 0, Delta: Delta{ToolCalls: []DeltaToolCall{
			{Index: 0, ID: "call_1", Function: DeltaFunction{Name: "f1"}},
		}}},
	}})
	acc.AddChunk(ChatCompletionResponse{Choices: []Choice{
		{Index: 0, FinishReason: "stop"},
	}})
	if tcs, ok := acc.FinishedToolCalls(); ok || tcs != nil {
		t.Errorf("expected no tool calls with wrong finish reason, got %v, %v", tcs, ok)
	}
}

func TestCompletionAccumulator_FinishedRefusal(t *testing.T) {
	acc := &CompletionAccumulator{}
	if refusal, ok := acc.FinishedRefusal(); ok || refusal != "" {
		t.Fatalf("expected no refusal, got %q, %v", refusal, ok)
	}

	acc.AddChunk(ChatCompletionResponse{Choices: []Choice{
		{Index: 0, Delta: Delta{Refusal: "I cannot "}},
	}})
	acc.AddChunk(ChatCompletionResponse{Choices: []Choice{
		{Index: 0, Delta: Delta{Refusal: "help with that."}},
	}})

	refusal, ok := acc.FinishedRefusal()
	if !ok {
		t.Fatal("expected ok=true")
	}
	if refusal != "I cannot help with that." {
		t.Errorf("refusal = %q", refusal)
	}
}

func TestCompletionAccumulator_BuildToolCall_InvalidArgumentsJSON(t *testing.T) {
	acc := &CompletionAccumulator{}
	acc.AddChunk(ChatCompletionResponse{Choices: []Choice{
		{Index: 0, Delta: Delta{ToolCalls: []DeltaToolCall{
			{Index: 0, ID: "call_1", Function: DeltaFunction{Name: "f", Arguments: "not json"}},
		}}},
	}})
	acc.AddChunk(ChatCompletionResponse{Choices: []Choice{
		{Index: 0, FinishReason: "tool_calls"},
	}})

	tc, ok := acc.FinishedToolCall()
	if !ok {
		t.Fatal("expected ok=true")
	}
	if tc.Function.Arguments == nil {
		t.Error("expected non-nil (empty) arguments map on invalid JSON")
	}
}

func TestCompletionAccumulator_BuildToolCall_DefaultType(t *testing.T) {
	acc := &CompletionAccumulator{}
	acc.AddChunk(ChatCompletionResponse{Choices: []Choice{
		{Index: 0, Delta: Delta{ToolCalls: []DeltaToolCall{
			{Index: 0, ID: "call_1", Function: DeltaFunction{Name: "f"}},
		}}},
	}})
	acc.AddChunk(ChatCompletionResponse{Choices: []Choice{
		{Index: 0, FinishReason: "tool_calls"},
	}})
	tc, _ := acc.FinishedToolCall()
	if tc.Type != "function" {
		t.Errorf("Type = %q, want function (default)", tc.Type)
	}
}

func TestCompletionAccumulator_MultipleChoicesGrowsSlice(t *testing.T) {
	acc := &CompletionAccumulator{}
	acc.AddChunk(ChatCompletionResponse{Choices: []Choice{
		{Index: 2, Delta: Delta{Content: "third"}},
	}})
	if len(acc.Choices) != 3 {
		t.Fatalf("len(Choices) = %d, want 3", len(acc.Choices))
	}
	if acc.Choices[2].Content.String() != "third" {
		t.Errorf("Choices[2].Content = %q", acc.Choices[2].Content.String())
	}
}

func TestCompletionAccumulator_Reset(t *testing.T) {
	acc := &CompletionAccumulator{}
	acc.AddChunk(ChatCompletionResponse{Choices: []Choice{{Index: 0, Delta: Delta{Content: "hi"}}}})
	if len(acc.Choices) == 0 {
		t.Fatal("expected choices before reset")
	}
	acc.Reset()
	if len(acc.Choices) != 0 {
		t.Errorf("Choices after Reset() = %d, want 0", len(acc.Choices))
	}
	if acc.Content() != "" {
		t.Errorf("Content() after Reset() = %q, want empty", acc.Content())
	}
}
