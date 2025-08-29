package mcp

// Parameter interface for all parameter types
type Parameter interface {
	apply(builder *paramBuilder)
}

// Option interface for parameter options
type Option interface {
	applyToParam(param parameterBase)
}

// Base parameter structure
type parameterBase struct {
	name        string
	description string
	required    bool
}

// Parameter builder for constructing schemas
type paramBuilder struct {
	params       []paramDef
	outputParams []paramDef
}

// Required option
type requiredOption struct{}

func (r requiredOption) applyToParam(param parameterBase) {
	// This will be handled by each parameter type
}

func Required() Option {
	return requiredOption{}
}

// Parameter implementations
type stringParam struct {
	parameterBase
}

func (s *stringParam) apply(builder *paramBuilder) {
	builder.params = append(builder.params, paramDef{
		name:        s.name,
		paramType:   "string",
		description: s.description,
		required:    s.required,
		properties:  make(map[string]*paramDef),
	})
}

type numberParam struct {
	parameterBase
}

func (n *numberParam) apply(builder *paramBuilder) {
	builder.params = append(builder.params, paramDef{
		name:        n.name,
		paramType:   "number",
		description: n.description,
		required:    n.required,
		properties:  make(map[string]*paramDef),
	})
}

type booleanParam struct {
	parameterBase
}

func (b *booleanParam) apply(builder *paramBuilder) {
	builder.params = append(builder.params, paramDef{
		name:        b.name,
		paramType:   "boolean",
		description: b.description,
		required:    b.required,
		properties:  make(map[string]*paramDef),
	})
}

type stringArrayParam struct {
	parameterBase
}

func (s *stringArrayParam) apply(builder *paramBuilder) {
	builder.params = append(builder.params, paramDef{
		name:        s.name,
		paramType:   "array:string",
		description: s.description,
		required:    s.required,
		properties:  make(map[string]*paramDef),
	})
}

type numberArrayParam struct {
	parameterBase
}

func (n *numberArrayParam) apply(builder *paramBuilder) {
	builder.params = append(builder.params, paramDef{
		name:        n.name,
		paramType:   "array:number",
		description: n.description,
		required:    n.required,
		properties:  make(map[string]*paramDef),
	})
}

type objectParam struct {
	parameterBase
	properties []Parameter
}

func (o *objectParam) apply(builder *paramBuilder) {
	// Build properties
	props := make(map[string]*paramDef)
	for _, prop := range o.properties {
		propBuilder := &paramBuilder{}
		prop.apply(propBuilder)
		if len(propBuilder.params) > 0 {
			props[propBuilder.params[0].name] = &propBuilder.params[0]
		}
	}

	builder.params = append(builder.params, paramDef{
		name:        o.name,
		paramType:   "object",
		description: o.description,
		required:    o.required,
		properties:  props,
	})
}

type objectArrayParam struct {
	parameterBase
	properties []Parameter
}

func (o *objectArrayParam) apply(builder *paramBuilder) {
	// Build properties for array items
	props := make(map[string]*paramDef)
	for _, prop := range o.properties {
		propBuilder := &paramBuilder{}
		prop.apply(propBuilder)
		if len(propBuilder.params) > 0 {
			props[propBuilder.params[0].name] = &propBuilder.params[0]
		}
	}

	// Create item schema
	itemSchema := &paramDef{
		paramType:  "object",
		properties: props,
	}

	builder.params = append(builder.params, paramDef{
		name:        o.name,
		paramType:   "array:object",
		description: o.description,
		required:    o.required,
		itemSchema:  itemSchema,
	})
}

// Output wrapper
type outputParam struct {
	parameters []Parameter
}

func (o *outputParam) apply(builder *paramBuilder) {
	// Apply output parameters directly to outputParams
	for _, param := range o.parameters {
		switch p := param.(type) {
		case *stringParam:
			builder.outputParams = append(builder.outputParams, paramDef{
				name:        p.name,
				paramType:   "string",
				description: p.description,
				required:    p.required,
				properties:  make(map[string]*paramDef),
			})
		case *numberParam:
			builder.outputParams = append(builder.outputParams, paramDef{
				name:        p.name,
				paramType:   "number",
				description: p.description,
				required:    p.required,
				properties:  make(map[string]*paramDef),
			})
		case *booleanParam:
			builder.outputParams = append(builder.outputParams, paramDef{
				name:        p.name,
				paramType:   "boolean",
				description: p.description,
				required:    p.required,
				properties:  make(map[string]*paramDef),
			})
		case *stringArrayParam:
			builder.outputParams = append(builder.outputParams, paramDef{
				name:        p.name,
				paramType:   "array:string",
				description: p.description,
				required:    p.required,
				properties:  make(map[string]*paramDef),
			})
		case *numberArrayParam:
			builder.outputParams = append(builder.outputParams, paramDef{
				name:        p.name,
				paramType:   "array:number",
				description: p.description,
				required:    p.required,
				properties:  make(map[string]*paramDef),
			})
		case *objectParam:
			// Build properties for output object
			props := make(map[string]*paramDef)
			for _, prop := range p.properties {
				propBuilder := &paramBuilder{}
				prop.apply(propBuilder)
				if len(propBuilder.params) > 0 {
					props[propBuilder.params[0].name] = &propBuilder.params[0]
				}
			}
			builder.outputParams = append(builder.outputParams, paramDef{
				name:        p.name,
				paramType:   "object",
				description: p.description,
				required:    p.required,
				properties:  props,
			})
		case *objectArrayParam:
			// Build properties for output object array
			props := make(map[string]*paramDef)
			for _, prop := range p.properties {
				propBuilder := &paramBuilder{}
				prop.apply(propBuilder)
				if len(propBuilder.params) > 0 {
					props[propBuilder.params[0].name] = &propBuilder.params[0]
				}
			}
			itemSchema := &paramDef{
				paramType:  "object",
				properties: props,
			}
			builder.outputParams = append(builder.outputParams, paramDef{
				name:        p.name,
				paramType:   "array:object",
				description: p.description,
				required:    p.required,
				itemSchema:  itemSchema,
			})
		}
	}
}

func Output(parameters ...Parameter) Parameter {
	return &outputParam{parameters: parameters}
}

// String creates a string parameter
func String(name, description string, options ...Option) Parameter {
	param := &stringParam{
		parameterBase: parameterBase{
			name:        name,
			description: description,
			required:    false,
		},
	}

	for _, opt := range options {
		if _, ok := opt.(requiredOption); ok {
			param.required = true
		}
	}

	return param
}

// Number creates a number parameter
func Number(name, description string, options ...Option) Parameter {
	param := &numberParam{
		parameterBase: parameterBase{
			name:        name,
			description: description,
			required:    false,
		},
	}

	for _, opt := range options {
		if _, ok := opt.(requiredOption); ok {
			param.required = true
		}
	}

	return param
}

// Boolean creates a boolean parameter
func Boolean(name, description string, options ...Option) Parameter {
	param := &booleanParam{
		parameterBase: parameterBase{
			name:        name,
			description: description,
			required:    false,
		},
	}

	for _, opt := range options {
		if _, ok := opt.(requiredOption); ok {
			param.required = true
		}
	}

	return param
}

// StringArray creates a string array parameter
func StringArray(name, description string, options ...Option) Parameter {
	param := &stringArrayParam{
		parameterBase: parameterBase{
			name:        name,
			description: description,
			required:    false,
		},
	}

	for _, opt := range options {
		if _, ok := opt.(requiredOption); ok {
			param.required = true
		}
	}

	return param
}

// NumberArray creates a number array parameter
func NumberArray(name, description string, options ...Option) Parameter {
	param := &numberArrayParam{
		parameterBase: parameterBase{
			name:        name,
			description: description,
			required:    false,
		},
	}

	for _, opt := range options {
		if _, ok := opt.(requiredOption); ok {
			param.required = true
		}
	}

	return param
}

// Object creates an object parameter with properties
func Object(name, description string, propertiesAndOptions ...interface{}) Parameter {
	var properties []Parameter
	required := false

	// Separate properties from options
	for _, item := range propertiesAndOptions {
		if param, ok := item.(Parameter); ok {
			properties = append(properties, param)
		} else if _, ok := item.(requiredOption); ok {
			required = true
		}
	}

	return &objectParam{
		parameterBase: parameterBase{
			name:        name,
			description: description,
			required:    required,
		},
		properties: properties,
	}
}

// ObjectArray creates an array of objects parameter
func ObjectArray(name, description string, propertiesAndOptions ...interface{}) Parameter {
	var properties []Parameter
	required := false

	// Separate properties from options
	for _, item := range propertiesAndOptions {
		if param, ok := item.(Parameter); ok {
			properties = append(properties, param)
		} else if _, ok := item.(requiredOption); ok {
			required = true
		}
	}

	return &objectArrayParam{
		parameterBase: parameterBase{
			name:        name,
			description: description,
			required:    required,
		},
		properties: properties,
	}
}

// NewTool creates a new tool with the declarative API
func NewTool(name, description string, parameters ...Parameter) *ToolBuilder {
	builder := &paramBuilder{}

	for _, param := range parameters {
		param.apply(builder)
	}

	return &ToolBuilder{
		name:         name,
		description:  description,
		params:       builder.params,
		outputParams: builder.outputParams,
	}
}
