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

	// UI links the tool to a companion UI resource per the MCP Apps extension
	// (SEP-1865). When set, BuildMCPTool calls ToolBuilder.UIResource so the
	// tool's _meta.ui.resourceUri is populated. See mcp.UIToolMeta.
	UI *mcp.UIToolMeta

	// Icons attaches visual identifiers to the tool's tools/list descriptor.
	// See mcp.Icon.
	Icons []mcp.Icon
}

// validTypeList is a human-readable list of accepted parameter type strings,
// used in error messages.
const validTypeList = "string, int, integer, float, number, bool, boolean, " +
	"array:string, array:int, array:integer, array:float, array:number, " +
	"array:bool, array:boolean"

// BuildMCPTool creates an mcp.ToolBuilder from ToolMetadata.
// Returns an error if any parameter declares an unknown type, or if
// meta.UI carries an invalid resourceURI or visibility value — ToolBuilder.
// UIResource/Visibility validate those by panicking (they're meant to catch
// a Go caller's own typo, e.g. a literal "ui://..." string), which would be
// the wrong failure mode here: meta.UI came from parsed data (a .toml file,
// a decorator's keyword arguments, ...), not a Go call site, so an invalid
// value is reported the same way any other bad metadata in this function
// is — as a returned error — by recovering that specific panic.
func BuildMCPTool(toolName string, meta *ToolMetadata) (_ *mcp.ToolBuilder, err error) {
	params := make([]mcp.Parameter, 0, len(meta.Parameters))
	for _, param := range meta.Parameters {
		p, convErr := convertParameter(param)
		if convErr != nil {
			return nil, fmt.Errorf("tool %q: %w", toolName, convErr)
		}
		params = append(params, p)
	}

	tool := mcp.NewTool(toolName, meta.Description, params...)

	if meta.Discoverable {
		tool.Discoverable(meta.Keywords...)
	}

	if meta.UI != nil {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("tool %q: invalid ui: %v", toolName, r)
			}
		}()
		// UIResource requires a non-empty resourceURI (an app-only action
		// tool with none — the visibility-only case, e.g. [ui] visibility =
		// ["app"] and no resourceUri — must go through Visibility instead).
		if meta.UI.ResourceURI != "" {
			tool.UIResource(meta.UI.ResourceURI, meta.UI.Visibility...)
		} else {
			tool.Visibility(meta.UI.Visibility...)
		}
	}

	if len(meta.Icons) > 0 {
		tool.Icons(meta.Icons...)
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
