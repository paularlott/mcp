package mcp

import "strings"

// ToolBuilder provides fluent API for building tools
type ToolBuilder struct {
	name          string
	description   string
	params        []paramDef
	outputParams  []paramDef
	discoverable  bool     // If true, tool is discoverable via tool_search but not in tools/list
	keywords      []string // Keywords for discovery search
}

type paramDef struct {
	name        string
	paramType   string
	description string
	required    bool
	properties  map[string]*paramDef // For object types
	itemSchema  *paramDef            // For array types with complex items
}

func (t *ToolBuilder) buildSchema() map[string]interface{} {
	return t.buildSchemaFromParams(t.params)
}

func (t *ToolBuilder) buildOutputSchema() map[string]interface{} {
	if len(t.outputParams) == 0 {
		return nil
	}
	return t.buildSchemaFromParams(t.outputParams)
}

// Name returns the tool's name
func (t *ToolBuilder) Name() string {
	return t.name
}

// Description returns the tool's description with newlines normalized to spaces
// and multiple whitespace collapsed to single spaces
func (t *ToolBuilder) Description() string {
	// Replace newlines with spaces
	desc := strings.ReplaceAll(t.description, "\n", " ")
	// Replace tabs with spaces
	desc = strings.ReplaceAll(desc, "\t", " ")
	// Collapse multiple spaces into single space
	words := strings.Fields(desc)
	return strings.Join(words, " ")
}

// BuildSchema returns the JSON Schema for the tool's input parameters.
// This is used internally for tool registration and search functionality,
// but can also be used for documentation or schema validation purposes.
func (t *ToolBuilder) BuildSchema() map[string]interface{} {
	return t.buildSchema()
}

// BuildOutputSchema returns the JSON Schema for the tool's structured output.
// Returns nil if no output schema was defined with Output().
// This is used internally and for tools that return structured content.
func (t *ToolBuilder) BuildOutputSchema() map[string]interface{} {
	return t.buildOutputSchema()
}

func (t *ToolBuilder) buildSchemaFromParams(params []paramDef) map[string]interface{} {
	properties := make(map[string]interface{})
	var required []string

	for _, param := range params {
		prop := t.buildParamSchema(&param)

		if param.description != "" {
			prop["description"] = param.description
		}
		properties[param.name] = prop
		if param.required {
			required = append(required, param.name)
		}
	}

	schema := map[string]interface{}{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func (t *ToolBuilder) buildParamSchema(param *paramDef) map[string]interface{} {
	if strings.HasPrefix(param.paramType, "array:") {
		itemType := strings.TrimPrefix(param.paramType, "array:")

		var itemSchema map[string]interface{}
		if itemType == "object" && param.itemSchema != nil {
			// Array of objects with defined schema
			itemSchema = t.buildObjectSchema(param.itemSchema)
		} else {
			// Array of primitives
			itemSchema = map[string]interface{}{"type": itemType}
		}

		return map[string]interface{}{
			"type":  "array",
			"items": itemSchema,
		}
	} else if param.paramType == "object" {
		return t.buildObjectSchema(param)
	} else {
		return map[string]interface{}{"type": param.paramType}
	}
}

func (t *ToolBuilder) buildObjectSchema(param *paramDef) map[string]interface{} {
	if len(param.properties) == 0 {
		// Generic object
		return map[string]interface{}{
			"type":                 "object",
			"additionalProperties": true,
		}
	}

	// Object with defined properties
	properties := make(map[string]interface{})
	var required []string

	for propName, propDef := range param.properties {
		propSchema := t.buildParamSchema(propDef)
		if propDef.description != "" {
			propSchema["description"] = propDef.description
		}
		properties[propName] = propSchema
		if propDef.required {
			required = append(required, propName)
		}
	}

	schema := map[string]interface{}{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// Discoverable marks the tool as discoverable via tool_search.
// Discoverable tools do NOT appear in tools/list but can be found through search.
// Keywords improve search relevance - include terms users might search for.
// When any discoverable tools exist, tool_search and execute_tool are automatically
// added to tools/list.
func (t *ToolBuilder) Discoverable(keywords ...string) *ToolBuilder {
	t.discoverable = true
	t.keywords = keywords
	return t
}

// IsDiscoverable returns true if the tool is marked as discoverable.
func (t *ToolBuilder) IsDiscoverable() bool {
	return t.discoverable
}

// Keywords returns the keywords set for this tool.
func (t *ToolBuilder) Keywords() []string {
	return t.keywords
}

// ToMCPTool converts the ToolBuilder to an MCPTool struct.
// This is useful for tool providers that use the fluent API to build tools
// but need to return MCPTool structs from their GetTools method.
// Use .Discoverable(keywords...) before calling this to set keywords and mark as discoverable.
func (t *ToolBuilder) ToMCPTool() MCPTool {
	// Determine visibility from discoverable flag
	visibility := ToolVisibilityNative
	if t.discoverable {
		visibility = ToolVisibilityDiscoverable
	}

	tool := MCPTool{
		Name:          t.name,
		Description:   t.Description(),
		InputSchema:   t.buildSchema(),
		Keywords:      t.keywords,
		Visibility:    visibility,
	}
	if outputSchema := t.buildOutputSchema(); outputSchema != nil {
		tool.OutputSchema = outputSchema
	}
	return tool
}
