package mcp

import "strings"

// Parameter types
const (
	String       = "string"
	Number       = "number"
	Boolean      = "boolean"
	Object       = "object"
	ArrayString  = "array:string"
	ArrayNumber  = "array:number"
	ArrayBoolean = "array:boolean"
	ArrayObject  = "array:object"
)

// ArrayOf creates an array type for the given item type
func ArrayOf(itemType string) string {
	return "array:" + itemType
}

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
	return t.buildSchemaFromParams(t.params)
}

func (t *ToolBuilder) buildOutputSchema() map[string]interface{} {
	if len(t.outputParams) == 0 {
		return nil
	}
	return t.buildSchemaFromParams(t.outputParams)
}

func (t *ToolBuilder) buildSchemaFromParams(params []paramDef) map[string]interface{} {
	properties := make(map[string]interface{})
	var required []string

	for _, param := range params {
		var prop map[string]interface{}
		
		if strings.HasPrefix(param.paramType, "array:") {
			itemType := strings.TrimPrefix(param.paramType, "array:")
			prop = map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": itemType,
				},
			}
		} else {
			prop = map[string]interface{}{"type": param.paramType}
		}
		
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
