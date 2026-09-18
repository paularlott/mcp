package openai

import (
	"encoding/json"
	"testing"
)

func TestResponseObject_OutputItems_NilReceiver(t *testing.T) {
	var r *ResponseObject
	if items := r.OutputItems(); items != nil {
		t.Errorf("OutputItems() on nil receiver = %v, want nil", items)
	}
}

func TestResponseObject_OutputItems_SkipsNonMapItems(t *testing.T) {
	r := &ResponseObject{Output: []any{"not a map", 42, nil}}
	items := r.OutputItems()
	if len(items) != 0 {
		t.Errorf("items = %+v, want empty", items)
	}
}

func TestResponseObject_OutputItems_Arguments(t *testing.T) {
	r := &ResponseObject{Output: []any{
		map[string]any{"type": "function_call", "call_id": "c1", "name": "f", "arguments": map[string]any{"x": 1}},
	}}
	items := r.OutputItems()
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}
	if items[0].Arguments["x"] != 1 {
		t.Errorf("Arguments = %v", items[0].Arguments)
	}
	if items[0].CallID != "c1" || items[0].Name != "f" {
		t.Errorf("item = %+v", items[0])
	}
}

func TestResponseObject_OutputItems_ContentSkipsNonMapParts(t *testing.T) {
	r := &ResponseObject{Output: []any{
		map[string]any{"type": "message", "content": []any{"not a map", map[string]any{"type": "output_text", "text": "hi"}}},
	}}
	items := r.OutputItems()
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}
	if len(items[0].Content) != 1 || items[0].Content[0].Text != "hi" {
		t.Errorf("Content = %+v", items[0].Content)
	}
}

func TestResponseObject_OutputText_SkipsNonMessageItems(t *testing.T) {
	r := &ResponseObject{Output: []any{
		map[string]any{"type": "function_call", "call_id": "c1"},
		map[string]any{"type": "message", "content": []any{
			map[string]any{"type": "output_text", "text": "hello"},
		}},
	}}
	if got := r.OutputText(); got != "hello" {
		t.Errorf("OutputText() = %q, want hello", got)
	}
}

func TestResponseObject_OutputText_SkipsNonOutputTextContent(t *testing.T) {
	r := &ResponseObject{Output: []any{
		map[string]any{"type": "message", "content": []any{
			map[string]any{"type": "refusal", "text": "nope"},
			map[string]any{"type": "output_text", "text": "yes"},
		}},
	}}
	if got := r.OutputText(); got != "yes" {
		t.Errorf("OutputText() = %q, want yes", got)
	}
}

func TestResponseObject_OutputText_Empty(t *testing.T) {
	r := &ResponseObject{}
	if got := r.OutputText(); got != "" {
		t.Errorf("OutputText() = %q, want empty", got)
	}
}

func TestCreateResponseRequest_MarshalJSON_NoExtraBody(t *testing.T) {
	req := CreateResponseRequest{Model: "gpt-x"}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if _, ok := body["extra_body"]; ok {
		t.Errorf("extra_body should not appear: %s", data)
	}
	if body["model"] != "gpt-x" {
		t.Errorf("model = %v", body["model"])
	}
}

func TestCreateResponseRequest_MarshalJSON_WithExtraBody(t *testing.T) {
	req := CreateResponseRequest{
		Model:     "gpt-x",
		ExtraBody: map[string]any{"vendor_flag": true},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if _, ok := body["extra_body"]; ok {
		t.Errorf("extra_body should be merged, not emitted literally: %s", data)
	}
	if body["vendor_flag"] != true {
		t.Errorf("vendor_flag = %v, want true", body["vendor_flag"])
	}
}

func TestCreateResponseRequest_UnmarshalJSON_CapturesExtraBody(t *testing.T) {
	data := []byte(`{"model":"gpt-x","input":[{"type":"message","role":"user","content":"hi"}],"vendor_flag":true,"extra_body":{"other":1}}`)
	var req CreateResponseRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if req.Model != "gpt-x" {
		t.Errorf("Model = %q", req.Model)
	}
	if req.ExtraBody["vendor_flag"] != true {
		t.Errorf("ExtraBody[vendor_flag] = %v", req.ExtraBody["vendor_flag"])
	}
	if req.ExtraBody["other"] != float64(1) {
		t.Errorf("ExtraBody[other] = %v", req.ExtraBody["other"])
	}
}

func TestCreateResponseRequest_UnmarshalJSON_InvalidTopLevel(t *testing.T) {
	var req CreateResponseRequest
	if err := json.Unmarshal([]byte(`not json`), &req); err == nil {
		t.Fatal("expected error for invalid top-level JSON")
	}
}

func TestCreateResponseRequest_UnmarshalJSON_NoExtraFields(t *testing.T) {
	data := []byte(`{"model":"gpt-x"}`)
	var req CreateResponseRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if req.ExtraBody != nil {
		t.Errorf("ExtraBody = %v, want nil when no unknown fields", req.ExtraBody)
	}
}
