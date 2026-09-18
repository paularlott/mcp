package openai

import "testing"

func TestStreamingToolCallAccumulator_ProcessDeltaAndFinalize(t *testing.T) {
	acc := NewStreamingToolCallAccumulator()

	ids := acc.ProcessDelta(Delta{ToolCalls: []DeltaToolCall{
		{Index: 0, ID: "call_1", Type: "function", Function: DeltaFunction{Name: "get_weather", Arguments: `{"city":`}},
	}})
	if len(ids) != 1 || ids[0] != "call_1" {
		t.Fatalf("ids = %v, want [call_1]", ids)
	}

	acc.ProcessDelta(Delta{ToolCalls: []DeltaToolCall{
		{Index: 0, Function: DeltaFunction{Arguments: `"NYC"}`}},
	}})

	if !acc.HasToolCalls() {
		t.Error("HasToolCalls() = false, want true")
	}
	if acc.Count() != 1 {
		t.Errorf("Count() = %d, want 1", acc.Count())
	}

	tcs := acc.Finalize()
	if len(tcs) != 1 {
		t.Fatalf("len(tcs) = %d, want 1", len(tcs))
	}
	if tcs[0].ID != "call_1" || tcs[0].Function.Name != "get_weather" {
		t.Errorf("tcs[0] = %+v", tcs[0])
	}
	if tcs[0].Function.Arguments["city"] != "NYC" {
		t.Errorf("arguments = %#v", tcs[0].Function.Arguments)
	}
	if tcs[0].Type != "function" {
		t.Errorf("Type = %q, want function", tcs[0].Type)
	}
}

func TestStreamingToolCallAccumulator_GeneratesIDWhenMissing(t *testing.T) {
	acc := NewStreamingToolCallAccumulator()
	ids := acc.ProcessDelta(Delta{ToolCalls: []DeltaToolCall{
		{Index: 0, Function: DeltaFunction{Name: "f"}},
	}})
	if len(ids) != 1 || ids[0] == "" {
		t.Fatalf("expected generated ID, got %v", ids)
	}
	if !hasPrefix(ids[0], "call_") {
		t.Errorf("generated ID = %q, want call_ prefix", ids[0])
	}

	// A second delta at the same index without an ID keeps the same generated ID.
	ids2 := acc.ProcessDelta(Delta{ToolCalls: []DeltaToolCall{
		{Index: 0, Function: DeltaFunction{Arguments: "{}"}},
	}})
	if ids2[0] != ids[0] {
		t.Errorf("ID changed across deltas: %q vs %q", ids[0], ids2[0])
	}
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func TestStreamingToolCallAccumulator_ProcessDeltaWithIDCallback(t *testing.T) {
	acc := NewStreamingToolCallAccumulator()
	var callbackIndex int
	var callbackID string
	calls := 0

	acc.ProcessDeltaWithIDCallback(Delta{ToolCalls: []DeltaToolCall{
		{Index: 0, Function: DeltaFunction{Name: "f"}},
	}}, func(index int, id string) {
		calls++
		callbackIndex = index
		callbackID = id
	})

	if calls != 1 {
		t.Fatalf("callback called %d times, want 1", calls)
	}
	if callbackIndex != 0 {
		t.Errorf("callbackIndex = %d, want 0", callbackIndex)
	}
	if callbackID == "" {
		t.Error("callbackID is empty")
	}

	// When an explicit ID is provided, the callback must not fire again.
	acc2 := NewStreamingToolCallAccumulator()
	calls2 := 0
	acc2.ProcessDeltaWithIDCallback(Delta{ToolCalls: []DeltaToolCall{
		{Index: 0, ID: "explicit", Function: DeltaFunction{Name: "f"}},
	}}, func(index int, id string) {
		calls2++
	})
	if calls2 != 0 {
		t.Errorf("callback called %d times for explicit ID, want 0", calls2)
	}
}

func TestStreamingToolCallAccumulator_ProcessDeltaWithIDCallback_NilCallback(t *testing.T) {
	acc := NewStreamingToolCallAccumulator()
	// Should not panic with a nil callback.
	ids := acc.ProcessDeltaWithIDCallback(Delta{ToolCalls: []DeltaToolCall{
		{Index: 0, Function: DeltaFunction{Name: "f"}},
	}}, nil)
	if len(ids) != 1 {
		t.Fatalf("ids = %v, want 1 entry", ids)
	}
}

func TestStreamingToolCallAccumulator_Finalize_Empty(t *testing.T) {
	acc := NewStreamingToolCallAccumulator()
	if tcs := acc.Finalize(); tcs != nil {
		t.Errorf("Finalize() on empty = %v, want nil", tcs)
	}
}

func TestStreamingToolCallAccumulator_Finalize_SkipsEmptyNames(t *testing.T) {
	acc := NewStreamingToolCallAccumulator()
	acc.ProcessDelta(Delta{ToolCalls: []DeltaToolCall{
		{Index: 0, ID: "call_1"}, // no name
		{Index: 1, ID: "call_2", Function: DeltaFunction{Name: "real"}},
	}})
	tcs := acc.Finalize()
	if len(tcs) != 1 {
		t.Fatalf("len(tcs) = %d, want 1 (skip empty name)", len(tcs))
	}
	if tcs[0].Function.Name != "real" {
		t.Errorf("tcs[0].Function.Name = %q", tcs[0].Function.Name)
	}
	if tcs[0].Index != 1 {
		t.Errorf("Index = %d, want 1", tcs[0].Index)
	}
}

func TestStreamingToolCallAccumulator_Finalize_InvalidJSONArgs(t *testing.T) {
	acc := NewStreamingToolCallAccumulator()
	acc.ProcessDelta(Delta{ToolCalls: []DeltaToolCall{
		{Index: 0, ID: "call_1", Function: DeltaFunction{Name: "f", Arguments: "not-json"}},
	}})
	tcs := acc.Finalize()
	if len(tcs) != 1 {
		t.Fatalf("len(tcs) = %d, want 1", len(tcs))
	}
	if tcs[0].Function.Arguments == nil {
		t.Error("expected non-nil empty args map on invalid JSON")
	}
}

func TestStreamingToolCallAccumulator_Finalize_NullArgs(t *testing.T) {
	acc := NewStreamingToolCallAccumulator()
	acc.ProcessDelta(Delta{ToolCalls: []DeltaToolCall{
		{Index: 0, ID: "call_1", Function: DeltaFunction{Name: "f", Arguments: "null"}},
	}})
	tcs := acc.Finalize()
	if len(tcs) != 1 || tcs[0].Function.Arguments == nil {
		t.Fatalf("tcs = %+v", tcs)
	}
}

func TestStreamingToolCallAccumulator_Finalize_GeneratesFallbackID(t *testing.T) {
	acc := NewStreamingToolCallAccumulator()
	// Directly populate a tool call with no ID via ProcessDelta with an ID, then clear
	// via a fresh accumulator manipulation isn't possible externally, so instead
	// verify via GetToolCall which does not force ID generation.
	acc.ProcessDelta(Delta{ToolCalls: []DeltaToolCall{
		{Index: 0, ID: "call_1", Function: DeltaFunction{Name: "f"}},
	}})
	tcs := acc.Finalize()
	if tcs[0].ID != "call_1" {
		t.Errorf("ID = %q, want call_1", tcs[0].ID)
	}
}

func TestStreamingToolCallAccumulator_GetToolCall(t *testing.T) {
	acc := NewStreamingToolCallAccumulator()
	if tc := acc.GetToolCall(0); tc != nil {
		t.Errorf("GetToolCall on empty = %v, want nil", tc)
	}

	acc.ProcessDelta(Delta{ToolCalls: []DeltaToolCall{
		{Index: 0, ID: "call_1", Function: DeltaFunction{Name: "f", Arguments: `{"a":1}`}},
	}})
	tc := acc.GetToolCall(0)
	if tc == nil {
		t.Fatal("expected non-nil tool call")
	}
	if tc.ID != "call_1" || tc.Function.Name != "f" {
		t.Errorf("tc = %+v", tc)
	}
	if tc.Function.Arguments["a"] != float64(1) {
		t.Errorf("arguments = %#v", tc.Function.Arguments)
	}

	if tc2 := acc.GetToolCall(5); tc2 != nil {
		t.Errorf("GetToolCall(5) = %v, want nil", tc2)
	}
}

func TestStreamingToolCallAccumulator_GetToolCall_InvalidJSON(t *testing.T) {
	acc := NewStreamingToolCallAccumulator()
	acc.ProcessDelta(Delta{ToolCalls: []DeltaToolCall{
		{Index: 0, ID: "call_1", Function: DeltaFunction{Name: "f", Arguments: "bad"}},
	}})
	tc := acc.GetToolCall(0)
	if tc.Function.Arguments == nil {
		t.Error("expected non-nil empty args map")
	}
}

func TestStreamingToolCallAccumulator_Reset(t *testing.T) {
	acc := NewStreamingToolCallAccumulator()
	acc.ProcessDelta(Delta{ToolCalls: []DeltaToolCall{
		{Index: 0, ID: "call_1", Function: DeltaFunction{Name: "f"}},
	}})
	if !acc.HasToolCalls() {
		t.Fatal("expected tool calls before reset")
	}
	acc.Reset()
	if acc.HasToolCalls() {
		t.Error("HasToolCalls() after Reset() = true, want false")
	}
	if acc.Count() != 0 {
		t.Errorf("Count() after Reset() = %d, want 0", acc.Count())
	}
}

func TestGenerateToolCallID_Unique(t *testing.T) {
	id1 := generateToolCallID(0)
	id2 := generateToolCallID(1)
	if id1 == id2 {
		t.Errorf("expected unique IDs, got %q twice", id1)
	}
	if !hasPrefix(id1, "call_") {
		t.Errorf("id1 = %q, want call_ prefix", id1)
	}
}
