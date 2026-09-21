# mcp-app-host-harness

A minimal **host** for the [MCP Apps extension](https://github.com/modelcontextprotocol/ext-apps)
(SEP-1865): a single static HTML page, no build step, that plays the role a
real MCP client (Claude, ChatGPT, etc.) would play when rendering an app.

It talks to a real MCP server over Streamable HTTP, lets you call any tool,
and — for tools linked to a `ui://` resource — fetches that resource and
renders it in a sandboxed iframe, bridging the extension's `postMessage`
protocol between the iframe (the *View*) and the server:

- Responds to the View's `ui/initialize` handshake.
- Sends `ui/notifications/tool-input` and `ui/notifications/tool-result` once
  the View is ready, carrying the data from the tool call that opened it.
- Proxies any further `tools/call` / `resources/read` the View sends (per the
  extension's "app-only" tools, e.g. a form submitting back to the server)
  through to the real server and relays the response.
- Approximates CSP enforcement by injecting a `<meta http-equiv="Content-Security-Policy">`
  tag built from the resource's declared `_meta.ui.csp`, and defaults to the
  spec's restrictive same-origin policy when none is declared.
- Logs every JSON-RPC message in both directions (host↔server, host↔view) for
  inspection.

## Running

Point it at any running MCP server that speaks Streamable HTTP, for example
[`../mcp-app-dashboard`](../mcp-app-dashboard):

```bash
cd ../mcp-app-dashboard && go run . -http :8091
```

Then serve this directory and open it in a browser — it needs to be loaded
over `http://`, not `file://`, both so `fetch()` isn't treated as a
cross-origin file read and so the sandboxed iframe's `srcdoc` behaves like a
real page would. Any static file server works, e.g.:

```bash
go run ../../internal/staticserver . :8092   # no extra runtime needed
# or: python3 -m http.server 8092            # macOS may prompt to allow
                                              # python3 incoming connections
```

Open `http://localhost:8092`, enter the server's URL
(`http://localhost:8091/mcp` by default), click **Connect**, then **Call** a
tool. A tool with a **UI** badge renders its linked resource in the middle
pane; an **app-only** badge means the model never sees it — only the app
itself would call it (as `mcp-app-dashboard`'s dashboard does for its "Add
Sale" form).

This repo also ships a `.claude/launch.json` with both of these as named dev
servers, for use with Claude Code's browser preview tooling.

## What this is *not*

This is a test harness, not a reference host implementation:

- **No Sandbox proxy.** The spec's double-iframe topology (a same-origin
  "sandbox" iframe forwarding to a cross-origin "view" iframe) exists for
  hosts that are themselves untrusted web pages needing an extra isolation
  layer. This harness talks to the view directly from a single trusted page,
  which is the simpler, still spec-compliant shape most embedded/native hosts
  use.
- **One view at a time**, no OAuth, no session persistence, no
  `ui/notifications/size-changed` handling, no display modes beyond `inline`.
- **CSP via injected `<meta>`**, not a real response header — a real host
  serving the iframe's document itself would set an actual
  `Content-Security-Policy` header instead.

## A note on testing sandboxed iframes

Building this surfaced two real integration bugs, both fixed in
[`../mcp-app-dashboard/dashboard.html`](../mcp-app-dashboard/dashboard.html)
(worth knowing if you build your own MCP Apps view):

1. **AlpineJS's default build needs `'unsafe-eval'`** for its expression
   evaluator, which the MCP Apps CSP model doesn't grant (its restrictive
   default omits it, and there's no reason to declare it just for a UI
   library). Use the [`@alpinejs/csp`](https://alpinejs.dev/advanced/csp)
   build instead, and register components with `Alpine.data()` rather than an
   inline `x-data="foo()"` call.
2. **Don't store a Chart.js instance in Alpine's reactive state.** Alpine
   wraps `x-data` in a reactive Proxy; Chart.js's internal circular
   references and getters don't survive being deep-proxied, and a second
   `chart.update()` fails with a cryptic `Cannot set properties of undefined`
   deep inside Chart.js. Keep the chart instance in a plain closure variable
   outside the object passed to `Alpine.data()`.

Also, this specific harness was built and verified using an automated browser
tool that could reliably click the static `Connect` button but not always the
dynamically-created per-tool `Call` buttons (added to the DOM after the tools
list loads) or type into the sandboxed cross-origin iframe's form fields — a
limitation of that automation setup, not of the harness or the sandboxing
model (a real user's mouse and keyboard work normally, and this was
cross-checked in real interactive sessions). Where a click didn't register,
the same action was triggered from the browser's own JS console instead
(`callTool(tools.find(t => t.name === '...'))`, or `addSale()` inside the
view), exercising the identical code path a real click would have.
