package mcp

// Parameter interface for all parameter types
type Parameter interface {
	apply(builder *paramBuilder)
	// toParamDef converts the parameter to a paramDef for output schema reuse
	toParamDef() paramDef
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

// processOptions applies options to a parameterBase and returns true if required
func processOptions(options []Option) bool {
	for _, opt := range options {
		if _, ok := opt.(requiredOption); ok {
			return true
		}
	}
	return false
}

// buildPropertiesFromParams builds a properties map from a slice of Parameter
func buildPropertiesFromParams(properties []Parameter) map[string]*paramDef {
	props := make(map[string]*paramDef)
	for _, prop := range properties {
		def := prop.toParamDef()
		props[def.name] = &def
	}
	return props
}

// Parameter implementations
type stringParam struct {
	parameterBase
}

func (s *stringParam) toParamDef() paramDef {
	return paramDef{
		name:        s.name,
		paramType:   "string",
		description: s.description,
		required:    s.required,
		properties:  make(map[string]*paramDef),
	}
}

func (s *stringParam) apply(builder *paramBuilder) {
	builder.params = append(builder.params, s.toParamDef())
}

type numberParam struct {
	parameterBase
}

func (n *numberParam) toParamDef() paramDef {
	return paramDef{
		name:        n.name,
		paramType:   "number",
		description: n.description,
		required:    n.required,
		properties:  make(map[string]*paramDef),
	}
}

func (n *numberParam) apply(builder *paramBuilder) {
	builder.params = append(builder.params, n.toParamDef())
}

type booleanParam struct {
	parameterBase
}

func (b *booleanParam) toParamDef() paramDef {
	return paramDef{
		name:        b.name,
		paramType:   "boolean",
		description: b.description,
		required:    b.required,
		properties:  make(map[string]*paramDef),
	}
}

func (b *booleanParam) apply(builder *paramBuilder) {
	builder.params = append(builder.params, b.toParamDef())
}

type stringArrayParam struct {
	parameterBase
}

func (s *stringArrayParam) toParamDef() paramDef {
	return paramDef{
		name:        s.name,
		paramType:   "array:string",
		description: s.description,
		required:    s.required,
		properties:  make(map[string]*paramDef),
	}
}

func (s *stringArrayParam) apply(builder *paramBuilder) {
	builder.params = append(builder.params, s.toParamDef())
}

type numberArrayParam struct {
	parameterBase
}

func (n *numberArrayParam) toParamDef() paramDef {
	return paramDef{
		name:        n.name,
		paramType:   "array:number",
		description: n.description,
		required:    n.required,
		properties:  make(map[string]*paramDef),
	}
}

func (n *numberArrayParam) apply(builder *paramBuilder) {
	builder.params = append(builder.params, n.toParamDef())
}

type objectParam struct {
	parameterBase
	properties []Parameter
}

func (o *objectParam) toParamDef() paramDef {
	return paramDef{
		name:        o.name,
		paramType:   "object",
		description: o.description,
		required:    o.required,
		properties:  buildPropertiesFromParams(o.properties),
	}
}

func (o *objectParam) apply(builder *paramBuilder) {
	builder.params = append(builder.params, o.toParamDef())
}

type objectArrayParam struct {
	parameterBase
	properties []Parameter
}

func (o *objectArrayParam) toParamDef() paramDef {
	props := buildPropertiesFromParams(o.properties)
	itemSchema := &paramDef{
		paramType:  "object",
		properties: props,
	}
	return paramDef{
		name:        o.name,
		paramType:   "array:object",
		description: o.description,
		required:    o.required,
		itemSchema:  itemSchema,
	}
}

func (o *objectArrayParam) apply(builder *paramBuilder) {
	builder.params = append(builder.params, o.toParamDef())
}

// Output wrapper
type outputParam struct {
	parameters []Parameter
}

// toParamDef is not applicable for outputParam as it's a container
func (o *outputParam) toParamDef() paramDef {
	return paramDef{} // Not used directly
}

func (o *outputParam) apply(builder *paramBuilder) {
	// Simply convert each parameter to its paramDef and add to outputParams
	for _, param := range o.parameters {
		builder.outputParams = append(builder.outputParams, param.toParamDef())
	}
}

func Output(parameters ...Parameter) Parameter {
	return &outputParam{parameters: parameters}
}

// String creates a string parameter
func String(name, description string, options ...Option) Parameter {
	return &stringParam{
		parameterBase: parameterBase{
			name:        name,
			description: description,
			required:    processOptions(options),
		},
	}
}

// Number creates a number parameter
func Number(name, description string, options ...Option) Parameter {
	return &numberParam{
		parameterBase: parameterBase{
			name:        name,
			description: description,
			required:    processOptions(options),
		},
	}
}

// Boolean creates a boolean parameter
func Boolean(name, description string, options ...Option) Parameter {
	return &booleanParam{
		parameterBase: parameterBase{
			name:        name,
			description: description,
			required:    processOptions(options),
		},
	}
}

// StringArray creates a string array parameter
func StringArray(name, description string, options ...Option) Parameter {
	return &stringArrayParam{
		parameterBase: parameterBase{
			name:        name,
			description: description,
			required:    processOptions(options),
		},
	}
}

// NumberArray creates a number array parameter
func NumberArray(name, description string, options ...Option) Parameter {
	return &numberArrayParam{
		parameterBase: parameterBase{
			name:        name,
			description: description,
			required:    processOptions(options),
		},
	}
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
