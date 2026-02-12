package toolmetadata

import (
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

// BuildMCPTool creates an mcp.ToolBuilder from ToolMetadata
func BuildMCPTool(toolName string, meta *ToolMetadata) *mcp.ToolBuilder {
	var params []mcp.Parameter
	for _, param := range meta.Parameters {
		params = append(params, convertParameter(param))
	}

	tool := mcp.NewTool(toolName, meta.Description, params...)

	if meta.Discoverable && len(meta.Keywords) > 0 {
		tool.Discoverable(meta.Keywords...)
	}

	return tool
}

func convertParameter(param ToolParameter) mcp.Parameter {
	if param.Required {
		switch param.Type {
		case "string":
			return mcp.String(param.Name, param.Description, mcp.Required())
		case "int", "integer":
			return mcp.Number(param.Name, param.Description, mcp.Required())
		case "float", "number":
			return mcp.Number(param.Name, param.Description, mcp.Required())
		case "bool", "boolean":
			return mcp.Boolean(param.Name, param.Description, mcp.Required())
		case "array:string":
			return mcp.StringArray(param.Name, param.Description, mcp.Required())
		case "array:number", "array:int", "array:integer", "array:float":
			return mcp.NumberArray(param.Name, param.Description, mcp.Required())
		case "array:bool", "array:boolean":
			return mcp.BooleanArray(param.Name, param.Description, mcp.Required())
		default:
			return mcp.String(param.Name, param.Description, mcp.Required())
		}
	}

	switch param.Type {
	case "string":
		return mcp.String(param.Name, param.Description)
	case "int", "integer":
		return mcp.Number(param.Name, param.Description)
	case "float", "number":
		return mcp.Number(param.Name, param.Description)
	case "bool", "boolean":
		return mcp.Boolean(param.Name, param.Description)
	case "array:string":
		return mcp.StringArray(param.Name, param.Description)
	case "array:number", "array:int", "array:integer", "array:float":
		return mcp.NumberArray(param.Name, param.Description)
	case "array:bool", "array:boolean":
		return mcp.BooleanArray(param.Name, param.Description)
	default:
		return mcp.String(param.Name, param.Description)
	}
}
