package mcp

import "testing"

func TestEmptyToolSchema(t *testing.T) {
	tool := NewTool("empty", "no params")
	s := tool.BuildSchema()
	if s["type"] != "object" {
		t.Fatal("type")
	}
	props := s["properties"].(map[string]any)
	if len(props) != 0 {
		t.Fatalf("expected 0 props, got %d", len(props))
	}
}

func TestOnlyOutputSchema(t *testing.T) {
	tool := NewTool("o", "only out", Output(String("id", "id", Required())))
	if tool.BuildSchema()["properties"].(map[string]any) == nil {
		t.Fatal("input schema present but should be empty object with properties map")
	}
	out := tool.BuildOutputSchema()
	props := out["properties"].(map[string]any)
	if _, ok := props["id"]; !ok {
		t.Fatal("missing output id")
	}
}

func TestGenericObjectArray(t *testing.T) {
	tool := NewTool("ga", "generic array", ObjectArray("items", "", Required()))
	s := tool.BuildSchema()
	props := s["properties"].(map[string]any)
	items := props["items"].(map[string]any)
	if items["type"] != "array" {
		t.Fatal("array type")
	}
	is := items["items"].(map[string]any)
	if is["type"] != "object" || is["additionalProperties"] != true {
		t.Fatal("generic object items schema")
	}
}
