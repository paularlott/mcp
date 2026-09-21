# mcp-app-dashboard

A worked example of the [MCP Apps extension](https://github.com/modelcontextprotocol/ext-apps)
(SEP-1865) built directly on `github.com/paularlott/mcp`: a tool linked to an
interactive HTML UI resource that a compliant host renders in a sandboxed
iframe, instead of (or alongside) plain text.

It's a small sales dashboard: a data table, a Chart.js bar chart, and a form
that adds a new sale — all in one dependency-free HTML5 file
([dashboard.html](dashboard.html)) using AlpineJS and Chart.js loaded from a
CDN, and plain CSS.

## What it demonstrates

- **`ui://` resource** — `dashboard.html` is registered as
  `ui://sales-dashboard/dashboard` with mimeType `text/html;profile=mcp-app`,
  the type the extension requires.
- **Tool → UI linkage** (`_meta.ui.resourceUri`) — `sales_report` links to the
  dashboard with `ToolBuilder.UIResource(...)`; a host without MCP Apps
  support just ignores the metadata and the tool behaves as plain text.
- **Visibility** (`_meta.ui.visibility`) — `add_sale` is registered
  `.Visibility("app")`, with no `.UIResource(...)` of its own: visible to the
  *app* (the dashboard's own "Add Sale" form calls it) but a compliant host
  hides it from the model's tool list, since it's a UI-driven action, not
  something the model should call. It has no view to render — it's only ever
  called by a dashboard that's already open — so it deliberately omits
  `resourceUri` (optional per spec) rather than pointing at one it would
  never use; see [Visibility](../../docs/guides/mcp-apps.md#visibility) in
  the MCP Apps guide for when to use `UIResource` vs. `Visibility` alone.
- **CSP declaration** (`_meta.ui.csp`) — the dashboard loads AlpineJS and
  Chart.js from `cdn.jsdelivr.net`, so the resource response declares that
  domain in `resourceDomains`; a host enforces this as the iframe's
  Content-Security-Policy.
- **Bidirectional `tools/call`** — the dashboard calls `add_sale` itself
  (through the host, per the extension's communication model) and refreshes
  its table/chart from the response, without a round trip through the model.
- **Extension capability declaration** — `server.DeclareExtension(mcp.UIAppsExtensionID, ...)`
  advertises MCP Apps support in this server's `initialize` response.

## Running

Over stdio (what a real MCP host uses to spawn a local server):

```bash
go run . -stdio
```

Over Streamable HTTP (what the bundled host-simulator test harness uses):

```bash
go run . -http :8091
```

Then open [`../mcp-app-host-harness/index.html`](../mcp-app-host-harness/index.html)
in a browser, point it at `http://localhost:8091/mcp`, and call `sales_report`
to see the dashboard render for real — table, chart, and a working "Add Sale"
form that calls back into the server. See that harness's own README for how
it plays the *host* side of the protocol.

## Testing

```bash
go test ./...
```

`main_test.go` covers both tools and the resource end to end over HTTP
(`httptest`): the `_meta.ui` shape on `tools/list`, the CSP metadata and HTML
content on `resources/read`, `add_sale` mutating shared state that
`sales_report` then observes, and the unhappy paths (missing/invalid
parameters, unknown tool, unknown resource).
