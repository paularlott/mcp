package mcp

import "testing"

// TestIntegerParameter verifies that the Integer() factory emits a JSON Schema
// "integer" type rather than reusing Number()/"number". This preserves the
// semantic distinction between whole numbers and any numeric value.
func TestIntegerParameter(t *testing.T) {
	tool := NewTool("int_test", "Test integer parameter",
		Integer("count", "Item count", Required()),
		Number("ratio", "Ratio value"),
	)

	schema := tool.BuildSchema()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema missing properties: %+v", schema)
	}

	countProp, ok := props["count"].(map[string]any)
	if !ok {
		t.Fatalf("count property missing: %+v", props)
	}
	if got, want := countProp["type"], "integer"; got != want {
		t.Errorf("count.type = %v, want %q", got, want)
	}

	ratioProp, ok := props["ratio"].(map[string]any)
	if !ok {
		t.Fatalf("ratio property missing: %+v", props)
	}
	if got, want := ratioProp["type"], "number"; got != want {
		t.Errorf("ratio.type = %v, want %q (Number() must not be downgraded)", got, want)
	}

	required, _ := schema["required"].([]string)
	if len(required) != 1 || required[0] != "count" {
		t.Errorf("required = %v, want [count]", required)
	}
}

// TestIntegerArrayParameter verifies that IntegerArray() emits an items type
// of "integer" while NumberArray() emits items of "number".
func TestIntegerArrayParameter(t *testing.T) {
	tool := NewTool("int_array_test", "Test integer array parameter",
		IntegerArray("ids", "ID list", Required()),
		NumberArray("weights", "Weight list"),
	)

	schema := tool.BuildSchema()
	props := schema["properties"].(map[string]any)

	idsProp := props["ids"].(map[string]any)
	if got := idsProp["type"]; got != "array" {
		t.Errorf("ids.type = %v, want %q", got, "array")
	}
	idsItems := idsProp["items"].(map[string]any)
	if got, want := idsItems["type"], "integer"; got != want {
		t.Errorf("ids.items.type = %v, want %q", got, want)
	}

	weightsProp := props["weights"].(map[string]any)
	if got := weightsProp["type"]; got != "array" {
		t.Errorf("weights.type = %v, want %q", got, "array")
	}
	weightsItems := weightsProp["items"].(map[string]any)
	if got, want := weightsItems["type"], "number"; got != want {
		t.Errorf("weights.items.type = %v, want %q", got, want)
	}
}
