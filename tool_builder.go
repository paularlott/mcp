package mcp

import "strings"

// ToolBuilder provides fluent API for building tools
type ToolBuilder struct {
	name         string
	description  string
	params       []paramDef
	outputParams []paramDef
	discoverable bool           // If true, tool is discoverable via tool_search but not in tools/list
	keywords     []string       // Keywords for discovery search
	meta         map[string]any // Extension metadata (_meta on the tools/list descriptor)
	icons        []Icon         // Visual identifiers shown on the tools/list descriptor
	title        string         // Human-readable display name (2025-06-18+)
	annotations  *ToolAnnotations
}

type paramDef struct {
	name        string
	paramType   string
	description string
	required    bool
	header      string               // x-mcp-header name, if mirrored to an HTTP header
	properties  map[string]*paramDef // For object types
	itemSchema  *paramDef            // For array types with complex items
}

func (t *ToolBuilder) buildSchema() map[string]any {
	return t.buildSchemaFromParams(t.params)
}

func (t *ToolBuilder) buildOutputSchema() map[string]any {
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
func (t *ToolBuilder) BuildSchema() map[string]any {
	return t.buildSchema()
}

// BuildOutputSchema returns the JSON Schema for the tool's structured output.
// Returns nil if no output schema was defined with Output().
// This is used internally and for tools that return structured content.
func (t *ToolBuilder) BuildOutputSchema() map[string]any {
	return t.buildOutputSchema()
}

func (t *ToolBuilder) buildSchemaFromParams(params []paramDef) map[string]any {
	properties := make(map[string]any)
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

	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func (t *ToolBuilder) buildParamSchema(param *paramDef) map[string]any {
	if strings.HasPrefix(param.paramType, "array:") {
		itemType := strings.TrimPrefix(param.paramType, "array:")

		var itemSchema map[string]any
		if itemType == "object" && param.itemSchema != nil {
			// Array of objects with defined schema
			itemSchema = t.buildObjectSchema(param.itemSchema)
		} else {
			// Array of primitives
			itemSchema = map[string]any{"type": itemType}
		}

		return map[string]any{
			"type":  "array",
			"items": itemSchema,
		}
	} else if param.paramType == "object" {
		return t.buildObjectSchema(param)
	} else {
		schema := map[string]any{"type": param.paramType}
		if param.header != "" {
			schema[xMcpHeaderKey] = param.header
		}
		return schema
	}
}

func (t *ToolBuilder) buildObjectSchema(param *paramDef) map[string]any {
	if len(param.properties) == 0 {
		// Generic object
		return map[string]any{
			"type":                 "object",
			"additionalProperties": true,
		}
	}

	// Object with defined properties
	properties := make(map[string]any)
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

	schema := map[string]any{
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

// Meta attaches an arbitrary field to the tool's _meta object, serialized on
// its tools/list descriptor. Used by MCP extensions; see [ToolBuilder.UIResource]
// for the MCP Apps case.
func (t *ToolBuilder) Meta(key string, value any) *ToolBuilder {
	if t.meta == nil {
		t.meta = map[string]any{}
	}
	t.meta[key] = value
	return t
}

// UIResource links this tool to a companion UI resource per the MCP Apps
// extension (SEP-1865): a compliant host fetches resourceURI (a ui:// resource
// registered with [Server.RegisterResource]) and renders it in a sandboxed
// iframe to display this tool's results. visibility restricts who may call
// the tool ("model", "app", or both); omit it to accept the spec default of
// both.
//
// Hosts that don't support MCP Apps ignore this metadata and treat the tool
// as a normal text-only tool, so it's safe to attach unconditionally. To
// register different tool variants per host, check [Server.ClientCapabilities]
// with [SupportsUIApps] before calling this.
// UIResource and Visibility both target the same "ui" meta entry; each reads
// back whatever the other already set there so calling both (in either
// order) composes instead of one silently discarding the other's fields —
// see UIResource's own doc comment for why a caller might reasonably do
// that (its own visibility parameter covers the common case, but Visibility
// remains available to set/change it independently).
func (t *ToolBuilder) UIResource(resourceURI string, visibility ...string) *ToolBuilder {
	validateUIResourceURI(resourceURI)
	validateUIVisibility(visibility)
	existing, _ := t.meta["ui"].(UIToolMeta)
	existing.ResourceURI = resourceURI
	if len(visibility) > 0 {
		existing.Visibility = visibility
	}
	return t.Meta("ui", existing)
}

// Visibility restricts who may call this tool per the MCP Apps extension
// (SEP-1865), without linking it to a UI resource of its own: "model" (the
// agent), "app" (the UI itself, via the same server connection), or both.
// Use this for an app-only "action" tool whose calls always originate from a
// view that's already open — e.g. a form submission — which therefore has no
// rendering purpose of its own. Use [ToolBuilder.UIResource] instead when the
// tool SHOULD open or refresh a view when called.
func (t *ToolBuilder) Visibility(visibility ...string) *ToolBuilder {
	validateUIVisibility(visibility)
	existing, _ := t.meta["ui"].(UIToolMeta)
	existing.Visibility = visibility
	return t.Meta("ui", existing)
}

// Keywords returns the keywords set for this tool.
func (t *ToolBuilder) Keywords() []string {
	return t.keywords
}

// Icons attaches visual identifiers to the tool's tools/list descriptor. See
// [Icon] for the shape and the security precautions consumers must apply.
func (t *ToolBuilder) Icons(icons ...Icon) *ToolBuilder {
	t.icons = icons
	return t
}

// Title sets the tool's human-readable display name, shown by clients in
// place of its name (the spec's Tool.title, 2025-06-18+).
func (t *ToolBuilder) Title(title string) *ToolBuilder {
	t.title = title
	return t
}

// Annotations sets the tool's behaviour hints (the spec's
// Tool.annotations) in one go, replacing any set before; ReadOnly,
// Destructive, Idempotent and OpenWorld set them one at a time.
//
// The hints tell a client how careful to be, for example whether to ask the
// user before a call. They are only hints: clients MUST treat them as
// untrusted unless the server is trusted, so what a tool may actually do
// must be enforced by the server.
func (t *ToolBuilder) Annotations(annotations ToolAnnotations) *ToolBuilder {
	t.annotations = &annotations
	return t
}

// ReadOnly marks the tool as not modifying its environment (readOnlyHint).
func (t *ToolBuilder) ReadOnly() *ToolBuilder {
	t.hints().ReadOnlyHint = boolHint(true)
	return t
}

// Destructive states whether the tool may perform destructive updates
// (destructiveHint), as opposed to only additive ones. Clients assume a
// tool that is not read-only may be destructive unless told otherwise.
func (t *ToolBuilder) Destructive(destructive bool) *ToolBuilder {
	t.hints().DestructiveHint = boolHint(destructive)
	return t
}

// Idempotent marks the tool as having no additional effect when called
// again with the same arguments (idempotentHint).
func (t *ToolBuilder) Idempotent() *ToolBuilder {
	t.hints().IdempotentHint = boolHint(true)
	return t
}

// OpenWorld states whether the tool interacts with an open world of
// external entities, such as the web, or a closed domain such as a local
// store (openWorldHint). Clients assume an open world unless told otherwise.
func (t *ToolBuilder) OpenWorld(openWorld bool) *ToolBuilder {
	t.hints().OpenWorldHint = boolHint(openWorld)
	return t
}

func (t *ToolBuilder) hints() *ToolAnnotations {
	if t.annotations == nil {
		t.annotations = &ToolAnnotations{}
	}
	return t.annotations
}

func boolHint(b bool) *bool { return &b }

// toolAnnotations returns a copy of the tool's annotations, or nil when none
// were set, so a registered tool's descriptor is never changed by later
// builder calls.
func (t *ToolBuilder) toolAnnotations() *ToolAnnotations {
	if t.annotations == nil {
		return nil
	}
	a := *t.annotations
	return &a
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
		Name:        t.name,
		Title:       t.title,
		Description: t.Description(),
		Annotations: t.toolAnnotations(),
		InputSchema: t.buildSchema(),
		Meta:        t.meta,
		Icons:       t.icons,
		Keywords:    t.keywords,
		Visibility:  visibility,
	}
	if outputSchema := t.buildOutputSchema(); outputSchema != nil {
		tool.OutputSchema = outputSchema
	}
	return tool
}
