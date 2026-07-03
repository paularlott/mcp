package mcp

import "context"

// ResourceHandler returns the content of a resource for a given URI.
//
// For a static resource the URI is the resource's own URI. For a resource
// template the URI is the fully-expanded URI the client requested; the handler
// is responsible for parsing any variables out of it.
//
// Build the response with NewResourceResponseText or NewResourceResponseBlob.
type ResourceHandler func(ctx context.Context, uri string) (*ResourceResponse, error)

// ResourceBuilder is a fluent builder for a static resource. A static resource
// has a fixed URI, appears in resources/list, and is read verbatim by
// resources/read. Register it with [Server.RegisterResource].
type ResourceBuilder struct {
	uri         string
	name        string
	description string
	mimeType    string
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

// ToMCPResource converts the builder to an MCPResource descriptor.
func (r *ResourceBuilder) ToMCPResource() MCPResource {
	return MCPResource{
		URI:         r.uri,
		Name:        r.name,
		Description: r.description,
		MimeType:    r.mimeType,
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

// ToMCPResourceTemplate converts the builder to an MCPResourceTemplate descriptor.
func (t *ResourceTemplateBuilder) ToMCPResourceTemplate() MCPResourceTemplate {
	return MCPResourceTemplate{
		URITemplate: t.uriTemplate,
		Name:        t.name,
		Description: t.description,
		MimeType:    t.mimeType,
	}
}
