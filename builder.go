package mcp

// Parameter types
const (
	String  = "string"
	Integer = "integer"
	Number  = "number"
	Boolean = "boolean"
	Array   = "array"
	Object  = "object"
)

// ToolBuilder provides fluent API for building tools
type ToolBuilder struct {
	name         string
	description  string
	params       []paramDef
	outputParams []paramDef
}

type paramDef struct {
	name        string
	paramType   string
	description string
	required    bool
}

func NewTool(name, description string) *ToolBuilder {
	return &ToolBuilder{
		name:         name,
		description:  description,
		params:       []paramDef{},
		outputParams: []paramDef{},
	}
}

func (t *ToolBuilder) AddParam(name, paramType, description string, required bool) *ToolBuilder {
	t.params = append(t.params, paramDef{
		name:        name,
		paramType:   paramType,
		description: description,
		required:    required,
	})
	return t
}

func (t *ToolBuilder) AddOutputParam(name, paramType, description string, required bool) *ToolBuilder {
	t.outputParams = append(t.outputParams, paramDef{
		name:        name,
		paramType:   paramType,
		description: description,
		required:    required,
	})
	return t
}

func (t *ToolBuilder) buildSchema() map[string]interface{} {
	properties := make(map[string]interface{})
	var required []string

	for _, param := range t.params {
		prop := map[string]interface{}{"type": param.paramType}
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

func (t *ToolBuilder) buildOutputSchema() map[string]interface{} {
	if len(t.outputParams) == 0 {
		return nil
	}

	properties := make(map[string]interface{})
	var required []string

	for _, param := range t.outputParams {
		prop := map[string]interface{}{"type": param.paramType}
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
