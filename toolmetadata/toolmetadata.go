package toolmetadata

import (
	"fmt"

	"github.com/paularlott/mcp"
)

// ToolParameter defines a single parameter for an MCP tool
type ToolParameter struct {
	Name        string
	Type        string
	Description string
	Required    bool
}

// ToolMetadata defines metadata for an MCP tool
type ToolMetadata struct {
	Description  string
	Keywords     []string
	Parameters   []ToolParameter
	Discoverable bool
}

// validTypeList is a human-readable list of accepted parameter type strings,
// used in error messages.
const validTypeList = "string, int, integer, float, number, bool, boolean, " +
	"array:string, array:int, array:integer, array:float, array:number, " +
	"array:bool, array:boolean"

// BuildMCPTool creates an mcp.ToolBuilder from ToolMetadata.
// Returns an error if any parameter declares an unknown type.
func BuildMCPTool(toolName string, meta *ToolMetadata) (*mcp.ToolBuilder, error) {
	params := make([]mcp.Parameter, 0, len(meta.Parameters))
	for _, param := range meta.Parameters {
		p, err := convertParameter(param)
		if err != nil {
			return nil, fmt.Errorf("tool %q: %w", toolName, err)
		}
		params = append(params, p)
	}

	tool := mcp.NewTool(toolName, meta.Description, params...)

	if meta.Discoverable {
		tool.Discoverable(meta.Keywords...)
	}

	return tool, nil
}

// convertParameter converts a TOML ToolParameter into an mcp.Parameter.
// "int"/"integer" and "array:int"/"array:integer" map to the integer
// JSON Schema type, while "float"/"number" and their array variants map to
// the number type. Unknown type strings produce an error.
func convertParameter(param ToolParameter) (mcp.Parameter, error) {
	var options []mcp.Option
	if param.Required {
		options = append(options, mcp.Required())
	}

	switch param.Type {
	case "string":
		return mcp.String(param.Name, param.Description, options...), nil
	case "int", "integer":
		return mcp.Integer(param.Name, param.Description, options...), nil
	case "float", "number":
		return mcp.Number(param.Name, param.Description, options...), nil
	case "bool", "boolean":
		return mcp.Boolean(param.Name, param.Description, options...), nil
	case "array:string":
		return mcp.StringArray(param.Name, param.Description, options...), nil
	case "array:int", "array:integer":
		return mcp.IntegerArray(param.Name, param.Description, options...), nil
	case "array:float", "array:number":
		return mcp.NumberArray(param.Name, param.Description, options...), nil
	case "array:bool", "array:boolean":
		return mcp.BooleanArray(param.Name, param.Description, options...), nil
	default:
		return nil, fmt.Errorf("parameter %q: unknown type %q. Valid types: %s",
			param.Name, param.Type, validTypeList)
	}
}
