package mcp

// Icon is a visual identifier attachable to a tool, prompt, resource, or a
// server/client's own identity (an "Implementation"), per the MCP spec's
// generic icons convention.
//
// This library only carries icon metadata on the wire — it does not fetch,
// cache, decode, or render icon bytes. Consumers that do render icons are
// responsible for the spec's security precautions (treat Src and any fetched
// bytes as untrusted, require an https:// or data: URI, reject unsafe
// schemes and cross-origin redirects, fetch without credentials, verify
// content type from magic bytes, and guard against oversized images).
type Icon struct {
	// Src is the icon's location: an https:// URL or a data: URI. Consumers
	// MUST reject any other scheme (e.g. javascript:, file:, ftp:).
	Src string `json:"src"`
	// MimeType is the icon's media type, useful when it can't be inferred
	// from Src (e.g. a data: URI or a generic-looking URL).
	MimeType string `json:"mimeType,omitempty"`
	// Sizes lists size hints such as "48x48", or "any" for a scalable format
	// like SVG.
	Sizes []string `json:"sizes,omitempty"`
	// Theme requests this icon be preferred for a specific host theme:
	// "light" or "dark". Omit it for a theme-neutral icon.
	Theme string `json:"theme,omitempty"`
}
