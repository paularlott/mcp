package mcp

import "context"

// ResourceHandler returns the content of a resource.
//
// The handler receives a [*ResourceRequest]: use [ResourceRequest.URI] for the
// full URI being read, and [ResourceRequest.String] / [ResourceRequest.StringOr]
// to read template variables extracted by the server. For a static resource
// there are no variables, so only URI is meaningful.
//
// Build the response with [NewResourceResponseText] or [NewResourceResponseBlob].
type ResourceHandler func(ctx context.Context, req *ResourceRequest) (*ResourceResponse, error)

// ResourceRequest provides the URI and any expanded template variables for a
// resources/read call. It is the resource analogue of [*ToolRequest] and
// [*PromptRequest].
type ResourceRequest struct {
	uri  string
	vars map[string]string
}

// NewResourceRequest builds a ResourceRequest from a URI and an optional map of
// template variables. Handlers normally receive a ready-made request; this
// constructor exists for tests and providers that build their own.
func NewResourceRequest(uri string, vars map[string]string) *ResourceRequest {
	return &ResourceRequest{uri: uri, vars: vars}
}

// URI returns the full URI being read. For a template this is the expanded URI
// the client requested.
func (r *ResourceRequest) URI() string { return r.uri }

// String returns a template variable by name. Returns ErrUnknownParameter if the
// variable is not present (for example, on a static resource, which has none).
func (r *ResourceRequest) String(name string) (string, error) {
	if r.vars == nil {
		return "", ErrUnknownParameter
	}
	v, ok := r.vars[name]
	if !ok {
		return "", ErrUnknownParameter
	}
	return v, nil
}

// StringOr returns a template variable or defaultValue if the variable is absent.
func (r *ResourceRequest) StringOr(name, defaultValue string) string {
	if v, err := r.String(name); err == nil {
		return v
	}
	return defaultValue
}

// Vars returns all expanded template variables. The map is empty for static
// resources. The returned map is not copied; do not mutate it.
func (r *ResourceRequest) Vars() map[string]string {
	if r.vars == nil {
		return map[string]string{}
	}
	return r.vars
}

// ResourceBuilder is a fluent builder for a static resource. A static resource
// has a fixed URI, appears in resources/list, and is read verbatim by
// resources/read. Register it with [Server.RegisterResource].
type ResourceBuilder struct {
	uri         string
	name        string
	description string
	mimeType    string
	meta        map[string]any
	icons       []Icon
}

// NewResource creates a static resource descriptor.
//
//   - uri is the resource's absolute URI (e.g. "config://app").
//   - name is a short human-readable identifier.
//   - description is optional explanatory text shown to the model/client.
//   - mimeType is optional (e.g. "application/json"); pass "" to omit.
func NewResource(uri, name, description, mimeType string) *ResourceBuilder {
	return &ResourceBuilder{
		uri:         uri,
		name:        name,
		description: description,
		mimeType:    mimeType,
	}
}

// URI returns the resource's URI.
func (r *ResourceBuilder) URI() string { return r.uri }

// Name returns the resource's name.
func (r *ResourceBuilder) Name() string { return r.name }

// Description returns the resource's description.
func (r *ResourceBuilder) Description() string { return r.description }

// MimeType returns the resource's MIME type (may be empty).
func (r *ResourceBuilder) MimeType() string { return r.mimeType }

// Meta attaches an arbitrary field to the resource's _meta object, serialized
// on its resources/list descriptor. See [ResourceBuilder.UIMeta] for the MCP
// Apps case.
func (r *ResourceBuilder) Meta(key string, value any) *ResourceBuilder {
	if r.meta == nil {
		r.meta = map[string]any{}
	}
	r.meta[key] = value
	return r
}

// UIMeta attaches MCP Apps security/rendering hints (_meta.ui) to this
// resource's resources/list descriptor. These are usually only needed on the
// resources/read response (see [NewUIResourceResponseText]); set them here too
// if a host should see them before fetching the content.
func (r *ResourceBuilder) UIMeta(meta UIResourceMeta) *ResourceBuilder {
	return r.Meta("ui", meta)
}

// Icons attaches visual identifiers to the resource's resources/list
// descriptor. See [Icon] for the shape and the security precautions
// consumers must apply.
func (r *ResourceBuilder) Icons(icons ...Icon) *ResourceBuilder {
	r.icons = icons
	return r
}

// ToMCPResource converts the builder to an MCPResource descriptor.
func (r *ResourceBuilder) ToMCPResource() MCPResource {
	return MCPResource{
		URI:         r.uri,
		Name:        r.name,
		Description: r.description,
		MimeType:    r.mimeType,
		Meta:        r.meta,
		Icons:       r.icons,
	}
}

// ResourceTemplateBuilder is a fluent builder for a resource template. A
// resource template has a URI containing {var} placeholders (RFC 6570 level 1),
// appears in resources/templates/list, and is expanded by the client into a
// concrete URI before being read via resources/read. Register it with
// [Server.RegisterResourceTemplate].
type ResourceTemplateBuilder struct {
	uriTemplate string
	name        string
	description string
	mimeType    string
	meta        map[string]any
	icons       []Icon
}

// NewResourceTemplate creates a parameterized resource template descriptor.
//
//   - uriTemplate contains one or more {var} placeholders, e.g. "user://{id}".
//     A placeholder matches one or more characters in the requested URI.
//   - name, description and mimeType are as for [NewResource].
func NewResourceTemplate(uriTemplate, name, description, mimeType string) *ResourceTemplateBuilder {
	return &ResourceTemplateBuilder{
		uriTemplate: uriTemplate,
		name:        name,
		description: description,
		mimeType:    mimeType,
	}
}

// URITemplate returns the template's URI template string.
func (t *ResourceTemplateBuilder) URITemplate() string { return t.uriTemplate }

// Name returns the template's name.
func (t *ResourceTemplateBuilder) Name() string { return t.name }

// Description returns the template's description.
func (t *ResourceTemplateBuilder) Description() string { return t.description }

// MimeType returns the template's MIME type (may be empty).
func (t *ResourceTemplateBuilder) MimeType() string { return t.mimeType }

// Meta attaches an arbitrary field to the template's _meta object, serialized
// on its resources/templates/list descriptor.
func (t *ResourceTemplateBuilder) Meta(key string, value any) *ResourceTemplateBuilder {
	if t.meta == nil {
		t.meta = map[string]any{}
	}
	t.meta[key] = value
	return t
}

// UIMeta attaches MCP Apps security/rendering hints (_meta.ui) to this
// template's resources/templates/list descriptor.
func (t *ResourceTemplateBuilder) UIMeta(meta UIResourceMeta) *ResourceTemplateBuilder {
	return t.Meta("ui", meta)
}

// Icons attaches visual identifiers to the template's
// resources/templates/list descriptor. See [Icon] for the shape and the
// security precautions consumers must apply.
func (t *ResourceTemplateBuilder) Icons(icons ...Icon) *ResourceTemplateBuilder {
	t.icons = icons
	return t
}

// ToMCPResourceTemplate converts the builder to an MCPResourceTemplate descriptor.
func (t *ResourceTemplateBuilder) ToMCPResourceTemplate() MCPResourceTemplate {
	return MCPResourceTemplate{
		URITemplate: t.uriTemplate,
		Name:        t.name,
		Description: t.description,
		MimeType:    t.mimeType,
		Meta:        t.meta,
		Icons:       t.icons,
	}
}
