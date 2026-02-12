package toolmetadata

import (
	"testing"
)

func TestBuildMCPTool_BasicTool(t *testing.T) {
	meta := &ToolMetadata{
		Description: "Test tool",
		Parameters: []ToolParameter{
			{Name: "input", Type: "string", Description: "Input text", Required: true},
		},
	}

	tool := BuildMCPTool("test_tool", meta)
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

	tool := BuildMCPTool("multi_param_tool", meta)
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

	tool := BuildMCPTool("alias_tool", meta)
	if tool == nil {
		t.Fatal("BuildMCPTool returned nil")
	}
}

func TestBuildMCPTool_UnknownType(t *testing.T) {
	meta := &ToolMetadata{
		Description: "Tool with unknown type",
		Parameters: []ToolParameter{
			{Name: "unknown", Type: "custom_type", Description: "Unknown type", Required: true},
		},
	}

	tool := BuildMCPTool("unknown_type_tool", meta)
	if tool == nil {
		t.Fatal("BuildMCPTool should handle unknown types as strings")
	}
}

func TestBuildMCPTool_Discoverable(t *testing.T) {
	meta := &ToolMetadata{
		Description:  "Discoverable tool",
		Keywords:     []string{"test", "example"},
		Discoverable: true,
		Parameters:   []ToolParameter{},
	}

	tool := BuildMCPTool("discoverable_tool", meta)
	if tool == nil {
		t.Fatal("BuildMCPTool returned nil")
	}
}

func TestBuildMCPTool_NoParameters(t *testing.T) {
	meta := &ToolMetadata{
		Description: "Tool without parameters",
		Parameters:  []ToolParameter{},
	}

	tool := BuildMCPTool("no_param_tool", meta)
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

	result := convertParameter(param)
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

	result := convertParameter(param)
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

	tool := BuildMCPTool("execute_command", meta)
	if tool == nil {
		t.Fatal("BuildMCPTool returned nil")
	}
}
