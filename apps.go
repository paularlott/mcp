package mcp

import (
	"encoding/json"
	"fmt"
	"net/url"
)

// Support for the MCP Apps extension (SEP-1865, "io.modelcontextprotocol/ui"),
// which lets a server deliver an interactive HTML UI alongside a tool: the
// tool's descriptor links to a predeclared ui:// resource, and the host
// renders that resource's HTML in a sandboxed iframe when the tool is called.
//
// See https://github.com/modelcontextprotocol/ext-apps for the specification.
// Typical usage:
//
//	server.RegisterResource(
//		mcp.NewResource("ui://dashboard/sales", "Sales Dashboard", "", mcp.UIAppMimeType),
//		func(ctx context.Context, req *mcp.ResourceRequest) (*mcp.ResourceResponse, error) {
//			return mcp.NewUIResourceResponseText(req.URI(), dashboardHTML, nil), nil
//		},
//	)
//	server.RegisterTool(
//		mcp.NewTool("sales_report", "Get the sales report").
//			UIResource("ui://dashboard/sales"),
//		handler,
//	)

// UIAppsExtensionID is the extension identifier clients and servers use to
// negotiate MCP Apps support via the capabilities.extensions mechanism
// (SEP-1724), e.g. capabilities.extensions["io.modelcontextprotocol/ui"].
const UIAppsExtensionID = "io.modelcontextprotocol/ui"

// UIAppMimeType is the MIME type MCP Apps UI resources MUST use.
const UIAppMimeType = "text/html;profile=mcp-app"

// UIToolMeta is the _meta.ui object on a tool descriptor (tools/list) that
// links the tool to a companion UI resource. Set it with [ToolBuilder.UIResource]
// or, for a tool with no resource of its own, [ToolBuilder.Visibility].
type UIToolMeta struct {
	// ResourceURI is the ui:// resource the host renders when this tool is
	// called. Optional per spec: an "app"-only action tool — one only ever
	// called by a view that's already open, such as a form submission —
	// has no rendering purpose of its own and should omit it; see
	// [ToolBuilder.Visibility].
	ResourceURI string `json:"resourceUri,omitempty"`
	// Visibility controls who may call the tool: "model" (the agent) and/or
	// "app" (the UI itself, via the same server connection). Defaults to
	// ["model", "app"] per the spec when left empty.
	Visibility []string `json:"visibility,omitempty"`
}

// validUIVisibilityValues are the only values the spec defines for
// _meta.ui.visibility. Anything else (a typo like "appp", or a value from a
// different convention entirely) is a programmer error, not data a caller
// could ever have meant to send a host — see [validateUIVisibility].
var validUIVisibilityValues = map[string]bool{"model": true, "app": true}

// ToolIsApp reports whether a tool descriptor is an MCP Apps view: a tool
// linked to a companion UI resource via _meta.ui.resourceUri. Federating
// such a tool re-serves its view under a namespace the view's own code
// doesn't know (its in-page JS calls bare, host-agnostic names), so a
// federating server that excludes apps skips them entirely — see
// [RemoteServerEntry.ExcludeApps].
func ToolIsApp(tool MCPTool) bool {
	raw, ok := tool.Meta["ui"]
	if !ok || raw == nil {
		return false
	}
	// _meta.ui is a map[string]any for a tool deserialized off the wire
	// (the federated path this serves), and a typed UIToolMeta for a tool
	// built locally via [ToolBuilder.UIResource].
	switch ui := raw.(type) {
	case UIToolMeta:
		return ui.ResourceURI != ""
	case map[string]any:
		resourceURI, _ := ui["resourceUri"].(string)
		return resourceURI != ""
	}
	return false
}

// validateUIVisibility panics on a visibility value the spec doesn't
// define. Called from [ToolBuilder.UIResource] and [ToolBuilder.Visibility]
// at registration time, not on the request path — the cost of validating is
// a few string comparisons at startup, and the alternative (a silent typo
// reaching the wire, where a host that checks for exactly "app"/"model"
// simply never grants the access the caller intended) fails far more
// expensively, and much later, than a panic at the call site does.
func validateUIVisibility(visibility []string) {
	for _, v := range visibility {
		if !validUIVisibilityValues[v] {
			panic(fmt.Sprintf("mcp: invalid UI visibility %q: must be \"model\" or \"app\" (see UIToolMeta.Visibility)", v))
		}
	}
}

// validateUIResourceURI panics on a resourceURI that isn't a "ui://" URI —
// empty, a plain path with no scheme, or an absolute URI using some other
// scheme (e.g. "https://..."), all of which a host has no reason to treat
// as an MCP Apps view and would simply never render. The MCP Apps
// extension (SEP-1865) mandates the "ui://" scheme for exactly this reason:
// it's how a host tells a linked UI resource apart from an ordinary one.
// See [validateUIVisibility]'s doc comment for why this validates by
// panicking rather than returning an error a caller could ignore.
func validateUIResourceURI(resourceURI string) {
	if resourceURI == "" {
		panic("mcp: UIResource requires a non-empty resourceURI (use Visibility instead for a tool with no view of its own)")
	}
	u, err := url.Parse(resourceURI)
	if err != nil || u.Scheme != "ui" {
		panic(fmt.Sprintf("mcp: invalid UI resourceURI %q: must use the \"ui://\" scheme per the MCP Apps extension, e.g. \"ui://dashboard/sales\"", resourceURI))
	}
}

// UICSP declares the external origins a UI resource's HTML is allowed to
// reach. Hosts use it to construct the iframe's Content-Security-Policy; if
// omitted entirely, hosts apply a restrictive same-origin/inline-only default.
type UICSP struct {
	// ConnectDomains are origins for fetch/XHR/WebSocket (CSP connect-src).
	ConnectDomains []string `json:"connectDomains,omitempty"`
	// ResourceDomains are origins for scripts, images, styles, fonts, media.
	ResourceDomains []string `json:"resourceDomains,omitempty"`
	// FrameDomains are origins allowed for nested iframes (CSP frame-src).
	FrameDomains []string `json:"frameDomains,omitempty"`
	// BaseURIDomains are origins allowed as the document's base URI.
	BaseURIDomains []string `json:"baseUriDomains,omitempty"`
}

// UIPermissions declares which sandboxed browser capabilities a UI resource
// requests. Hosts MAY honor these; the UI should still feature-detect rather
// than assume they were granted.
type UIPermissions struct {
	Camera         bool `json:"-"`
	Microphone     bool `json:"-"`
	Geolocation    bool `json:"-"`
	ClipboardWrite bool `json:"-"`
}

// MarshalJSON encodes only the permissions that are requested, each as an
// empty object, matching the extension's `{camera?: {}, ...}` shape.
func (p UIPermissions) MarshalJSON() ([]byte, error) {
	out := map[string]any{}
	if p.Camera {
		out["camera"] = map[string]any{}
	}
	if p.Microphone {
		out["microphone"] = map[string]any{}
	}
	if p.Geolocation {
		out["geolocation"] = map[string]any{}
	}
	if p.ClipboardWrite {
		out["clipboardWrite"] = map[string]any{}
	}
	return json.Marshal(out)
}

// UIResourceMeta is the _meta.ui object on a UI resource's resources/read
// response (and optionally its resources/list descriptor), carrying security
// and rendering hints for the host. Attach it with [NewUIResourceResponseText]
// or [ResourceBuilder.UIMeta] / [ResourceTemplateBuilder.UIMeta].
type UIResourceMeta struct {
	// CSP declares the external domains this UI's HTML needs to reach.
	CSP *UICSP `json:"csp,omitempty"`
	// Permissions declares browser capabilities this UI requests.
	Permissions *UIPermissions `json:"permissions,omitempty"`
	// Domain optionally requests a dedicated sandbox origin (host-dependent format).
	Domain string `json:"domain,omitempty"`
	// PrefersBorder requests (true) or declines (false) a visible host-drawn
	// border/background around the rendered view; omit to let the host decide.
	PrefersBorder *bool `json:"prefersBorder,omitempty"`
}

// NewUIResourceResponseText builds a resources/read response for an MCP Apps
// UI resource. mimeType is fixed to [UIAppMimeType]; html must be a
// self-contained HTML5 document. meta may be nil to omit rendering hints and
// accept the host's restrictive default sandbox.
func NewUIResourceResponseText(uri, html string, meta *UIResourceMeta) *ResourceResponse {
	rc := ResourceContent{URI: uri, Text: html, MimeType: UIAppMimeType}
	if meta != nil {
		rc.Meta = map[string]any{"ui": meta}
	}
	return &ResourceResponse{Contents: []ResourceContent{rc}}
}

// WithMeta attaches an arbitrary _meta object to the first content entry of a
// resource response, returning rr for chaining. Used for extensions such as
// MCP Apps; see [NewUIResourceResponseText] for the common case.
func (rr *ResourceResponse) WithMeta(meta map[string]any) *ResourceResponse {
	if len(rr.Contents) == 0 {
		rr.Contents = append(rr.Contents, ResourceContent{})
	}
	rr.Contents[0].Meta = meta
	return rr
}

// ExtensionCapability extracts a named extension's settings object from a raw
// capabilities map as received in an initialize request's capabilities field
// (capabilities.extensions[extensionID]), per the SEP-1724 extensions
// mechanism. ok is false if the extension was not declared.
func ExtensionCapability(clientCapabilities map[string]any, extensionID string) (settings map[string]any, ok bool) {
	if clientCapabilities == nil {
		return nil, false
	}
	extensions, _ := clientCapabilities["extensions"].(map[string]any)
	if extensions == nil {
		return nil, false
	}
	settings, ok = extensions[extensionID].(map[string]any)
	return settings, ok
}

// SupportsUIApps reports whether clientCapabilities declares support for the
// MCP Apps extension ([UIAppsExtensionID]), and if so, the resource MIME
// types it accepts. Use it to decide whether to register UI-linked tools, or
// fall back to a text-only tool for hosts that don't support MCP Apps:
//
//	if mimeTypes, ok := mcp.SupportsUIApps(server.ClientCapabilities()); ok && slices.Contains(mimeTypes, mcp.UIAppMimeType) {
//		server.RegisterTool(mcp.NewTool("get_weather", "...").UIResource("ui://weather/dashboard"), handler)
//	} else {
//		server.RegisterTool(mcp.NewTool("get_weather", "..."), handler)
//	}
func SupportsUIApps(clientCapabilities map[string]any) (mimeTypes []string, ok bool) {
	settings, ok := ExtensionCapability(clientCapabilities, UIAppsExtensionID)
	if !ok {
		return nil, false
	}
	raw, _ := settings["mimeTypes"].([]any)
	mimeTypes = make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			mimeTypes = append(mimeTypes, s)
		}
	}
	return mimeTypes, true
}
