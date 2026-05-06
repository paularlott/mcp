package toolmetadata

import (
	"strings"
	"testing"
)

func TestBuildMCPTool_BasicTool(t *testing.T) {
	meta := &ToolMetadata{
		Description: "Test tool",
		Parameters: []ToolParameter{
			{Name: "input", Type: "string", Description: "Input text", Required: true},
		},
	}

	tool, err := BuildMCPTool("test_tool", meta)
	if err != nil {
		t.Fatalf("BuildMCPTool returned error: %v", err)
	}
	if tool == nil {
		t.Fatal("BuildMCPTool returned nil")
	}
}

func TestBuildMCPTool_AllParameterTypes(t *testing.T) {
	meta := &ToolMetadata{
		Description: "Tool with all parameter types",
		Parameters: []ToolParameter{
			{Name: "text", Type: "string", Description: "Text param", Required: true},
			{Name: "count", Type: "int", Description: "Int param", Required: false},
			{Name: "amount", Type: "float", Description: "Float param", Required: true},
			{Name: "enabled", Type: "bool", Description: "Bool param", Required: false},
		},
	}

	tool, err := BuildMCPTool("multi_param_tool", meta)
	if err != nil {
		t.Fatalf("BuildMCPTool returned error: %v", err)
	}
	if tool == nil {
		t.Fatal("BuildMCPTool returned nil")
	}
}

func TestBuildMCPTool_TypeAliases(t *testing.T) {
	meta := &ToolMetadata{
		Description: "Tool with type aliases",
		Parameters: []ToolParameter{
			{Name: "num1", Type: "integer", Description: "Integer alias", Required: true},
			{Name: "num2", Type: "number", Description: "Number alias", Required: false},
			{Name: "flag", Type: "boolean", Description: "Boolean alias", Required: true},
		},
	}

	tool, err := BuildMCPTool("alias_tool", meta)
	if err != nil {
		t.Fatalf("BuildMCPTool returned error: %v", err)
	}
	if tool == nil {
		t.Fatal("BuildMCPTool returned nil")
	}
}

// TestBuildMCPTool_UnknownType verifies that an unknown type string produces
// a clear error rather than silently being treated as a string.
func TestBuildMCPTool_UnknownType(t *testing.T) {
	meta := &ToolMetadata{
		Description: "Tool with unknown type",
		Parameters: []ToolParameter{
			{Name: "unknown", Type: "custom_type", Description: "Unknown type", Required: true},
		},
	}

	tool, err := BuildMCPTool("unknown_type_tool", meta)
	if err == nil {
		t.Fatal("BuildMCPTool should return an error for unknown types")
	}
	if tool != nil {
		t.Errorf("BuildMCPTool should return nil tool on error, got %v", tool)
	}
	if !strings.Contains(err.Error(), "custom_type") {
		t.Errorf("error should reference the bad type %q, got: %v", "custom_type", err)
	}
	if !strings.Contains(err.Error(), "unknown_type_tool") {
		t.Errorf("error should reference the tool name, got: %v", err)
	}
}

func TestBuildMCPTool_Discoverable(t *testing.T) {
	meta := &ToolMetadata{
		Description:  "Discoverable tool",
		Keywords:     []string{"test", "example"},
		Discoverable: true,
		Parameters:   []ToolParameter{},
	}

	tool, err := BuildMCPTool("discoverable_tool", meta)
	if err != nil {
		t.Fatalf("BuildMCPTool returned error: %v", err)
	}
	if tool == nil {
		t.Fatal("BuildMCPTool returned nil")
	}
}

func TestBuildMCPTool_NoParameters(t *testing.T) {
	meta := &ToolMetadata{
		Description: "Tool without parameters",
		Parameters:  []ToolParameter{},
	}

	tool, err := BuildMCPTool("no_param_tool", meta)
	if err != nil {
		t.Fatalf("BuildMCPTool returned error: %v", err)
	}
	if tool == nil {
		t.Fatal("BuildMCPTool returned nil")
	}
}

func TestConvertParameter_RequiredString(t *testing.T) {
	param := ToolParameter{
		Name:        "test",
		Type:        "string",
		Description: "Test param",
		Required:    true,
	}

	result, err := convertParameter(param)
	if err != nil {
		t.Fatalf("convertParameter returned error: %v", err)
	}
	if result == nil {
		t.Fatal("convertParameter returned nil")
	}
}

func TestConvertParameter_OptionalNumber(t *testing.T) {
	param := ToolParameter{
		Name:        "count",
		Type:        "int",
		Description: "Count param",
		Required:    false,
	}

	result, err := convertParameter(param)
	if err != nil {
		t.Fatalf("convertParameter returned error: %v", err)
	}
	if result == nil {
		t.Fatal("convertParameter returned nil")
	}
}

func TestBuildMCPTool_Integration(t *testing.T) {
	meta := &ToolMetadata{
		Description:  "Execute command",
		Keywords:     []string{"shell", "execute"},
		Discoverable: true,
		Parameters: []ToolParameter{
			{Name: "command", Type: "string", Description: "Command to run", Required: true},
			{Name: "timeout", Type: "int", Description: "Timeout seconds", Required: false},
		},
	}

	tool, err := BuildMCPTool("execute_command", meta)
	if err != nil {
		t.Fatalf("BuildMCPTool returned error: %v", err)
	}
	if tool == nil {
		t.Fatal("BuildMCPTool returned nil")
	}
}

// TestBuildMCPTool_SchemaOutput verifies the emitted JSON Schema types, in
// particular that "int"/"integer" -> "integer" (not "number") and that
// "float"/"number" -> "number" (not "integer").
func TestBuildMCPTool_SchemaOutput(t *testing.T) {
	meta := &ToolMetadata{
		Description: "Schema output tool",
		Parameters: []ToolParameter{
			{Name: "s", Type: "string", Required: true},
			{Name: "i_int", Type: "int", Required: true},
			{Name: "i_integer", Type: "integer", Required: false},
			{Name: "f_float", Type: "float", Required: false},
			{Name: "f_number", Type: "number", Required: true},
			{Name: "b_bool", Type: "bool", Required: true},
			{Name: "b_boolean", Type: "boolean", Required: false},
			{Name: "as", Type: "array:string", Required: false},
			{Name: "ai_int", Type: "array:int", Required: false},
			{Name: "ai_integer", Type: "array:integer", Required: false},
			{Name: "af_float", Type: "array:float", Required: false},
			{Name: "af_number", Type: "array:number", Required: false},
			{Name: "ab_bool", Type: "array:bool", Required: false},
			{Name: "ab_boolean", Type: "array:boolean", Required: false},
		},
	}

	tool, err := BuildMCPTool("schema_output", meta)
	if err != nil {
		t.Fatalf("BuildMCPTool returned error: %v", err)
	}

	schema := tool.BuildSchema()
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("schema has no properties map: %+v", schema)
	}

	primitives := map[string]string{
		"s":         "string",
		"i_int":     "integer",
		"i_integer": "integer",
		"f_float":   "number",
		"f_number":  "number",
		"b_bool":    "boolean",
		"b_boolean": "boolean",
	}
	for name, wantType := range primitives {
		prop, ok := props[name].(map[string]interface{})
		if !ok {
			t.Errorf("missing property %q", name)
			continue
		}
		gotType, _ := prop["type"].(string)
		if gotType != wantType {
			t.Errorf("property %q type = %q, want %q", name, gotType, wantType)
		}
	}

	arrays := map[string]string{
		"as":         "string",
		"ai_int":     "integer",
		"ai_integer": "integer",
		"af_float":   "number",
		"af_number":  "number",
		"ab_bool":    "boolean",
		"ab_boolean": "boolean",
	}
	for name, wantItemType := range arrays {
		prop, ok := props[name].(map[string]interface{})
		if !ok {
			t.Errorf("missing array property %q", name)
			continue
		}
		gotType, _ := prop["type"].(string)
		if gotType != "array" {
			t.Errorf("array property %q outer type = %q, want %q", name, gotType, "array")
		}
		items, ok := prop["items"].(map[string]interface{})
		if !ok {
			t.Errorf("array property %q has no items map: %+v", name, prop)
			continue
		}
		gotItemType, _ := items["type"].(string)
		if gotItemType != wantItemType {
			t.Errorf("array property %q items.type = %q, want %q", name, gotItemType, wantItemType)
		}
	}

	// Required list should contain only the required ones.
	required, _ := schema["required"].([]string)
	wantRequired := map[string]bool{"s": true, "i_int": true, "f_number": true, "b_bool": true}
	gotRequired := make(map[string]bool, len(required))
	for _, r := range required {
		gotRequired[r] = true
	}
	for k := range wantRequired {
		if !gotRequired[k] {
			t.Errorf("required list missing %q, got %v", k, required)
		}
	}
	for _, r := range required {
		if !wantRequired[r] {
			t.Errorf("required list contains unexpected %q", r)
		}
	}
}
