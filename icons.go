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

// iconsFromRaw decodes the icons field of a JSON-deserialized tool or search
// result (always []any of map[string]any on the wire) into []Icon without a
// marshal round-trip. Returns nil for anything else, matching the previous
// round-trip's silent-skip behaviour on malformed input.
func iconsFromRaw(raw any) []Icon {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	icons := make([]Icon, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		icon := Icon{
			Src:   stringField(m, "src"),
			Theme: stringField(m, "theme"),
		}
		if mimeType, ok := m["mimeType"].(string); ok {
			icon.MimeType = mimeType
		}
		if sizesRaw, ok := m["sizes"].([]any); ok {
			sizes := make([]string, 0, len(sizesRaw))
			for _, s := range sizesRaw {
				if str, ok := s.(string); ok {
					sizes = append(sizes, str)
				}
			}
			if len(sizes) > 0 {
				icon.Sizes = sizes
			}
		}
		icons = append(icons, icon)
	}
	return icons
}

// stringField reads a string field out of a JSON-deserialized object,
// returning "" when absent or not a string.
func stringField(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}
