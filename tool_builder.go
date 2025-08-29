package mcp

import "strings"

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

// BuildSchema is a public method for testing
func (t *ToolBuilder) BuildSchema() map[string]interface{} {
	return t.buildSchema()
}

// BuildOutputSchema is a public method for testing
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