# MCP Apps Guide

[MCP Apps](https://github.com/modelcontextprotocol/ext-apps) (SEP-1865) is an official MCP extension that lets a server deliver an interactive HTML UI alongside a tool. Instead of (or in addition to) plain text, a compliant host renders the tool's linked `ui://` resource in a sandboxed iframe, and that view can call tools and read resources of its own through the host.

This server implements the extension's server-side surface: `_meta.ui` on tools and resources, the `ui://` resource convention (already supported generically — any URI scheme and MIME type works), CSP/permission metadata on UI resource content, and the `io.modelcontextprotocol/ui` capability negotiation. The host-side rendering (iframe, `postMessage` bridging) is a client concern — see the [mcp-app-host-harness example](../../examples/mcp-app-host-harness) for a minimal one you can test against.

## The Shape of an App

An app is two things registered together:

1. A **`ui://` resource** — a self-contained HTML5 document, MIME type `text/html;profile=mcp-app`.
2. A **tool linked to it** via `_meta.ui.resourceUri`.

```go
const dashboardURI = "ui://sales-dashboard/dashboard"

server.RegisterResource(
    mcp.NewResource(dashboardURI, "Sales Dashboard", "", mcp.UIAppMimeType),
    func(ctx context.Context, req *mcp.ResourceRequest) (*mcp.ResourceResponse, error) {
        return mcp.NewUIResourceResponseText(req.URI(), dashboardHTML, nil), nil
    },
)

server.RegisterTool(
    mcp.NewTool("sales_report", "Get the current sales report").
        UIResource(dashboardURI),
    func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
        return mcp.NewToolResponseStructured(map[string]any{"records": records()}), nil
    },
)
```

`UIResource` sets `_meta.ui.resourceUri` on the tool's `tools/list` descriptor:

```json
{"name": "sales_report", "_meta": {"ui": {"resourceUri": "ui://sales-dashboard/dashboard"}}}
```

Hosts that don't support MCP Apps simply ignore `_meta` and treat the tool as a normal text tool — attaching it costs nothing when the extension isn't supported. See the [mcp-app-dashboard example](../../examples/mcp-app-dashboard) for a complete, tested app (a data table, a Chart.js chart, and a form that writes back through a second tool).

## Visibility

`UIResource` takes an optional visibility list — who may call the tool:

```go
mcp.NewTool("refresh_dashboard", "Refresh dashboard data").
    UIResource(dashboardURI, "app") // hidden from the model, callable by the view, and re-renders the dashboard when called
```

- Omitted (the default) means `["model", "app"]`: the tool is a normal model-callable tool that happens to render a UI.
- `"app"` only means the tool is for the view itself to call (e.g. a form submission) — it still appears in this server's `tools/list` (the natural way for a host to learn it exists, so it can let the view call it), but a compliant host hides it from the model's own tool list and rejects `tools/call` for it from anywhere except that view's connection. That filtering is host behavior, not something this library enforces.

Use `UIResource` for an app-only tool only when calling it should also (re-)render
a view — for example, `refresh_dashboard` above returns fresh data the same
`dashboardURI` view should redraw. Many app-only tools are pure **actions** with
no rendering purpose of their own: they're only ever called by a view that's
*already* open (a form submission, a "claim" button), and the call just updates
server-side state and returns a result for the view's own JS to consume. For
those, use [`Visibility`](#visibility) alone and omit the resource link
entirely — `resourceUri` is optional per spec, and attaching one anyway can make
a host that enumerates `_meta.ui.resourceUri` to find "apps" mistake a plain
action tool for one:

```go
mcp.NewTool("add_sale", "Add a sale record (called by the dashboard's own form, not the model)").
    Visibility("app") // no UIResource — the dashboard is already open when this is called
```

See `add_sale` in [`examples/mcp-app-dashboard`](../../examples/mcp-app-dashboard)
and `claim_prize` in Scriptling's `examples/mcp-prize-wheel` for this pattern in
context.

`UIResource` and `Visibility` both validate their arguments and **panic** on
a mistake: an empty or scheme-less `resourceUri`, or a visibility value other
than `"model"`/`"app"` (a typo like `"appp"` reaching the wire would silently
mean "never callable" instead of what you meant, so this fails at the call
site instead). This is deliberately a panic rather than a returned error,
matching this being a direct Go call-site mistake — a data-driven caller
building tools from parsed metadata (a config file, a template) should
`recover()` around the call, the way [`toolmetadata.BuildMCPTool`](../../toolmetadata)
does, so bad external data becomes an ordinary error instead of a crash.

## CSP and Rendering Hints

A UI resource's `resources/read` response can declare `_meta.ui`: which external origins its HTML is allowed to reach, which sandboxed browser permissions it wants, and a border preference. Omit it entirely to accept the host's restrictive same-origin/inline-only default.

```go
prefersBorder := true
return mcp.NewUIResourceResponseText(req.URI(), dashboardHTML, &mcp.UIResourceMeta{
    CSP: &mcp.UICSP{
        ResourceDomains: []string{"https://cdn.jsdelivr.net"}, // scripts/styles/images/fonts
        ConnectDomains:  []string{"https://api.example.com"},  // fetch/XHR/WebSocket
    },
    Permissions:   &mcp.UIPermissions{Camera: true},
    PrefersBorder: &prefersBorder, // no BoolPtr helper in the package — take the address of a local variable, as the dashboard example does
})
```

If your UI resource loads a library like AlpineJS from a CDN, declare that domain in `ResourceDomains` — see [Known Gotchas](#known-gotchas-building-the-view) below before reaching for AlpineJS specifically.

`Meta` on `ResourceBuilder`/`ResourceTemplateBuilder` (`.UIMeta(...)`) sets the same shape on the `resources/list`/`resources/templates/list` descriptor, if you want a host to see rendering hints before it even fetches the content.

## Capability Negotiation

MCP Apps is negotiated through the general MCP extensions mechanism (SEP-1724): a client declares support in `initialize`'s `capabilities.extensions["io.modelcontextprotocol/ui"]`, and a server can declare its own support the same way.

```go
server.DeclareExtension(mcp.UIAppsExtensionID, map[string]any{
    "mimeTypes": []string{mcp.UIAppMimeType},
})
```

To check what a client declared — e.g. to register a UI-linked tool only for hosts that support it, falling back to a text-only tool otherwise:

```go
if mimeTypes, ok := mcp.SupportsUIApps(server.ClientCapabilities()); ok && slices.Contains(mimeTypes, mcp.UIAppMimeType) {
    server.RegisterTool(mcp.NewTool("get_weather", "...").UIResource(weatherDashboardURI), handler)
} else {
    server.RegisterTool(mcp.NewTool("get_weather", "..."), handler) // no _meta
}
```

`Server.ClientCapabilities()` reflects the most recently completed `initialize`. For a stdio server — one process per client connection, the common case for a locally-spawned MCP server — this reliably means *the* connected client. An HTTP server handling multiple concurrent sessions should not rely on it for per-request decisions: it's a single global, process-wide snapshot, not session-scoped, and in a horizontally-scaled deployment (e.g. `JWTSessionManager`, this library's recommended clustering approach — deliberately stateless per instance) a later request from the same client may land on an instance that never saw its `initialize` at all. For that reason, both example apps in this repo skip the conditional-registration pattern entirely and just attach `_meta.ui` unconditionally — it costs nothing when a host doesn't support the extension. `ExtensionCapability(caps, id)` is the underlying general-purpose helper if you need to check for a different extension.

## Container Dimensions and Auto-Resize

By default a host has no idea how tall a view's content actually is, so it either guesses a fixed height (wrong for most content) or stretches the iframe to fill whatever panel it happens to be in (wrong for content shorter than that panel). The extension's `containerDimensions` / `ui/notifications/size-changed` pair fixes this, and it's a two-sided contract — both the host and the view need to implement their half.

**Host side**: a `ui/initialize` reply's `hostContext` may declare `containerDimensions` in one of three shapes:

- `{"height": N}` / `{"width": N}` — Fixed: the host controls the size, the view should fill it (no resizing).
- `{"maxHeight": N}` / `{"maxWidth": N}` — Flexible: the view controls its own size up to this cap.
- Omitted entirely — Unbounded: the view controls its size with no limit.

Whichever mode is declared, a host that wants the view sized to its content applies the `height` from every `ui/notifications/size-changed` the view sends:

```js
if (msg.method === 'ui/notifications/size-changed') {
  const height = msg.params && msg.params.height;
  if (typeof height === 'number' && height > 0) iframe.style.height = Math.ceil(height) + 'px';
}
```

Applying only `height` (not `width`) is deliberate: width usually stays under the host's own CSS (e.g. a `w-full` iframe), and echoing a view-reported width back as a hard pixel size risks a feedback loop with content that itself reacts to container width (Chart.js's `responsive: true`, for one).

**View side**: report your own content height whenever it changes, using a `ResizeObserver` on `document.documentElement` (it fires once immediately on `.observe()` with the current size, then again on every subsequent layout change):

```js
ready.then((result) => {
  const dims = result?.hostContext?.containerDimensions;
  if (dims?.maxHeight) {
    document.documentElement.style.maxHeight = dims.maxHeight + 'px';
    document.documentElement.style.overflowY = 'auto'; // scroll internally past the cap instead of growing past it
  }
  const reportSize = () => {
    const rect = document.documentElement.getBoundingClientRect();
    send('ui/notifications/size-changed', { width: Math.ceil(rect.width), height: Math.ceil(rect.height) });
  };
  new ResizeObserver(reportSize).observe(document.documentElement);
});
```

**Gotcha: don't give `body` (or any ancestor of what you're measuring) a `min-height: 100vh`.** `100vh` resolves against the iframe's *own current* viewport height — which is exactly the thing the host is trying to set *from* your reported content height. The two can never converge: your report reflects whatever height the iframe already happens to be, not your actual content, and the iframe stays wherever it started. Size your content by its own layout, not by the viewport it's rendered into.

Both `examples/mcp-app-dashboard/dashboard.html` and Scriptling's `examples/mcp-prize-wheel/wheel.html` implement the view side above; `examples/mcp-app-host-harness` implements the host side.

## Known Gotchas Building the View

Building the reference example surfaced two integration issues worth knowing before you write your own `ui://` HTML, if you use AlpineJS and/or Chart.js:

1. **AlpineJS's default build needs `'unsafe-eval'`.** Its expression evaluator uses `Function()` under the hood, which the MCP Apps CSP model doesn't grant (the spec's restrictive default omits it, and there's no reason to declare it just for a UI library). Use the [`@alpinejs/csp`](https://alpinejs.dev/advanced/csp) build instead — same CDN path pattern, `@alpinejs/csp` in place of `alpinejs` — and register components with `Alpine.data('name', () => ({...}))` inside an `alpine:init` listener rather than an inline `x-data="foo()"` call.
2. **Don't put a Chart.js instance in Alpine's reactive state.** Alpine wraps everything returned from `Alpine.data()` in a reactive Proxy; a Chart.js instance's internal circular references and getters don't survive being deep-proxied, and a second `chart.update()` fails with a cryptic `Cannot set properties of undefined` deep inside Chart.js. Keep the chart instance in a plain closure variable outside the reactive object instead.
3. **Don't give `body` a `min-height: 100vh` if you're also doing [auto-resize](#container-dimensions-and-auto-resize).** See that section for why it's self-referential and never converges.

All three were caught by real testing, not just written from the spec, and are demonstrated in [`examples/mcp-app-dashboard/dashboard.html`](../../examples/mcp-app-dashboard/dashboard.html).

## Testing

- Unit/integration-test the server side exactly like any other tool/resource — `httptest` against `HandleRequest`, asserting on the `_meta` shapes. See [`apps_test.go`](../../apps_test.go) and [`examples/mcp-app-dashboard/main_test.go`](../../examples/mcp-app-dashboard/main_test.go) for the patterns (including unhappy paths: missing resources, bad parameters, unknown tools).
- To see it actually render, run an example server over HTTP and open [`examples/mcp-app-host-harness`](../../examples/mcp-app-host-harness) — a small standalone page that plays the host side (iframe, CSP, the `postMessage` bridge, [auto-resize](#container-dimensions-and-auto-resize)) against a real server, with a live protocol log.

## Other Language Bindings

[Scriptling](https://github.com/paularlott/scriptling) — a scripting language whose MCP server is built on this library — supports the same `_meta.ui` linkage from Python-like tool code, across all three of its tool registration styles (`.toml`-defined, `@mcp.tool(...)`-decorated, and dynamically via `register_request_tool(...)`), plus `tool.return_structured(...)` for the `structuredContent` field. See its `examples/mcp-app-dashboard` (the `.toml` style, mirroring this repo's Go example almost line for line) and `examples/mcp-prize-wheel` (the decorator style, an animated no-`.toml` example) and the [MCP Apps](https://scriptling.dev/reference/libraries/mcp/mcp-apps/) reference page.

## See Also

- [Response Types Guide](response-types.md) — the underlying tool/resource content constructors
- [Resources Guide](resources.md) — registering resources and templates in general
- [mcp-app-dashboard example](../../examples/mcp-app-dashboard) — a complete Go app
- [mcp-app-host-harness example](../../examples/mcp-app-host-harness) — a minimal host to test against
