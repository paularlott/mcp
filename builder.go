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
	properties  map[string]*paramDef // For object types
	itemSchema  *paramDef            // For array types with complex items
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
		properties:  make(map[string]*paramDef),
	})
	return t
}

// AddObjectParam adds an object parameter with properties
func (t *ToolBuilder) AddObjectParam(name, description string, required bool) *ObjectParamBuilder {
	param := paramDef{
		name:        name,
		paramType:   Object,
		description: description,
		required:    required,
		properties:  make(map[string]*paramDef),
	}
	t.params = append(t.params, param)
	return &ObjectParamBuilder{
		toolBuilder: t,
		paramIndex:  len(t.params) - 1,
	}
}

// AddArrayObjectParam adds an array of objects parameter
func (t *ToolBuilder) AddArrayObjectParam(name, description string, required bool) *ObjectParamBuilder {
	itemSchema := &paramDef{
		paramType:  Object,
		properties: make(map[string]*paramDef),
	}
	param := paramDef{
		name:        name,
		paramType:   ArrayObject,
		description: description,
		required:    required,
		itemSchema:  itemSchema,
	}
	t.params = append(t.params, param)
	return &ObjectParamBuilder{
		toolBuilder: t,
		paramIndex:  len(t.params) - 1,
		isArray:     true,
	}
}

func (t *ToolBuilder) AddOutputParam(name, paramType, description string, required bool) *ToolBuilder {
	t.outputParams = append(t.outputParams, paramDef{
		name:        name,
		paramType:   paramType,
		description: description,
		required:    required,
		properties:  make(map[string]*paramDef),
	})
	return t
}

// AddOutputObjectParam adds an object output parameter with properties
func (t *ToolBuilder) AddOutputObjectParam(name, description string, required bool) *ObjectParamBuilder {
	param := paramDef{
		name:        name,
		paramType:   Object,
		description: description,
		required:    required,
		properties:  make(map[string]*paramDef),
	}
	t.outputParams = append(t.outputParams, param)
	return &ObjectParamBuilder{
		toolBuilder: t,
		paramIndex:  len(t.outputParams) - 1,
		isOutput:    true,
	}
}

// AddOutputArrayObjectParam adds an array of objects output parameter
func (t *ToolBuilder) AddOutputArrayObjectParam(name, description string, required bool) *ObjectParamBuilder {
	itemSchema := &paramDef{
		paramType:  Object,
		properties: make(map[string]*paramDef),
	}
	param := paramDef{
		name:        name,
		paramType:   ArrayObject,
		description: description,
		required:    required,
		itemSchema:  itemSchema,
	}
	t.outputParams = append(t.outputParams, param)
	return &ObjectParamBuilder{
		toolBuilder: t,
		paramIndex:  len(t.outputParams) - 1,
		isOutput:    true,
		isArray:     true,
	}
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

// ObjectParamBuilder provides fluent API for building object parameters
type ObjectParamBuilder struct {
	toolBuilder *ToolBuilder
	paramIndex  int
	isOutput    bool
	isArray     bool
}

// AddProperty adds a property to the object parameter
func (o *ObjectParamBuilder) AddProperty(name, paramType, description string, required bool) *ObjectParamBuilder {
	prop := &paramDef{
		name:        name,
		paramType:   paramType,
		description: description,
		required:    required,
		properties:  make(map[string]*paramDef),
	}

	var targetParam *paramDef
	if o.isOutput {
		targetParam = &o.toolBuilder.outputParams[o.paramIndex]
	} else {
		targetParam = &o.toolBuilder.params[o.paramIndex]
	}

	if o.isArray {
		targetParam.itemSchema.properties[name] = prop
	} else {
		targetParam.properties[name] = prop
	}

	return o
}

// AddObjectProperty adds a nested object property
func (o *ObjectParamBuilder) AddObjectProperty(name, description string, required bool) *ObjectParamBuilder {
	prop := &paramDef{
		name:        name,
		paramType:   Object,
		description: description,
		required:    required,
		properties:  make(map[string]*paramDef),
	}

	var targetParam *paramDef
	if o.isOutput {
		targetParam = &o.toolBuilder.outputParams[o.paramIndex]
	} else {
		targetParam = &o.toolBuilder.params[o.paramIndex]
	}

	if o.isArray {
		targetParam.itemSchema.properties[name] = prop
	} else {
		targetParam.properties[name] = prop
	}

	return &ObjectParamBuilder{
		toolBuilder: o.toolBuilder,
		paramIndex:  o.paramIndex,
		isOutput:    o.isOutput,
		isArray:     o.isArray,
	}
}

// Done returns to the main ToolBuilder
func (o *ObjectParamBuilder) Done() *ToolBuilder {
	return o.toolBuilder
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
