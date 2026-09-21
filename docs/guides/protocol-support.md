# Protocol Support Guide

The MCP library supports every MCP protocol revision from 2024-11-05 through the
current 2026-07-28, across two distinct eras, on both `Server` and `Client`, over
both HTTP and stdio. Detection and behavior selection are automatic — there is no
configuration required to get this.

## Legacy and Modern

The spec's revision history splits into two eras with genuinely different wire
models:

- **Legacy** (`2024-11-05` through `2025-11-25`): the familiar `initialize`
  handshake negotiates a session once, and subsequent requests are implicitly
  scoped to that session.
- **Modern** (`2026-07-28`, current): stateless and per-request. There is no
  handshake — every request carries its own protocol version and client
  capabilities in `_meta`, and on HTTP two extra headers (`Mcp-Method`,
  `Mcp-Name`) mirror the request's method and target for routing/observability,
  validated against the JSON-RPC body on arrival.

A `Server` built with this library implements both simultaneously ("Dual-era"):
each incoming request is routed to whichever era it actually speaks, based only
on a real signal from that request (the `Mcp-Method` header on HTTP, or
`io.modelcontextprotocol/protocolVersion` in `_meta` on any transport) — never on
transport, URL, or configuration. A Legacy request never touches any Modern-era
code path, and vice versa.

A `Client` detects which era its server speaks the first time `Initialize` runs:
it attempts a Modern `server/discover` call first, and falls back to the Legacy
`initialize` handshake if that doesn't succeed (per the spec's own documented
backward-compatibility algorithm). Every other `Client` method — `ListTools`,
`CallTool`, `ReadResource`, and so on — is completely unaffected by which era was
detected; they call the same private helper either way.

## Supported Protocol Versions

- **2024-11-05** (Legacy, minimum supported)
- **2025-03-26** (Legacy)
- **2025-06-18** (Legacy)
- **2025-11-25** (Legacy, latest)
- **2026-07-28** (Modern, current)

## Legacy Version Features

### 2024-11-05 (Basic)

Basic tool listing and execution, resource access, no advanced features. Use
case: legacy clients, minimal functionality requirements.

### 2025-03-26 and Later (Enhanced)

Adds `tools/listChanged`, `resources/subscribe`, `resources/listChanged`. Use
case: Legacy clients that want change notifications over the `GET` SSE stream.

### 2025-11-25 (Latest Legacy)

Adds JWT-based stateless session management and `MCP-Protocol-Version` header
validation, on top of every earlier feature. Use case: production Legacy
deployments needing session tracking.

## Modern (2026-07-28)

### `server/discover`

Replaces `initialize` for Modern clients: a single stateless call returning
supported protocol versions (Legacy and Modern both — this library's server is
always Dual-era), capabilities, server identity, and instructions.

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "server/discover",
  "params": {
    "_meta": {
      "io.modelcontextprotocol/protocolVersion": "2026-07-28",
      "io.modelcontextprotocol/clientCapabilities": {}
    }
  }
}
```

### Per-request `_meta` and headers

Every other Modern request carries the same `_meta.protocolVersion` /
`clientCapabilities`, plus, on HTTP, `Mcp-Method` (mirroring `method`) and
`Mcp-Name` (mirroring `params.name` or `params.uri`, for `tools/call`,
`resources/read`, and `prompts/get`). A mismatch between a header and the body
is rejected with `HeaderMismatch` (`-32020`); an unsupported version with
`UnsupportedProtocolVersionError` (`-32022`); a missing required `_meta` field
with the standard `-32602` (Invalid params).

### Caching hints

`server/discover`, `tools/list`, `prompts/list`, `resources/list`,
`resources/templates/list`, and `resources/read` results automatically carry
`ttlMs` and `cacheScope`, as the spec requires — a real client's schema
validation rejects a result missing them. This library always sends `ttlMs: 0`
(no invented cache lifetime) and `cacheScope: "public"` for lists (or
`"private"` for `resources/read`); nothing to configure.

### `subscriptions/listen`

The Modern replacement for the Legacy `GET` SSE stream: a `POST` that opens a
stream scoped to just the notification types the caller asked for, acknowledged
first, each notification correlated by `subscriptionId`.

```go
// Before shutting down the HTTP server, let open subscriptions/listen
// streams close gracefully (they respond to their original request with a
// completion result first, per the spec, rather than the connection just
// going dead):
server.Shutdown()
httpServer.Shutdown(ctx)
```

Not implemented over stdio: stdio's existing connection-scoped notification
delivery already reaches every connected client regardless of era, so nothing
is functionally lost, and a stdio client that calls `subscriptions/listen`
gets a standard `Method not found` rather than a partial implementation.

### Icons

Tools, resources, resource templates, prompts, and a server's own identity can
all carry `Icon` values (`Src`, `MimeType`, `Sizes`, `Theme`), reported
identically in both eras (`icons` on the Legacy descriptor / `serverInfo`, and
the same field under Modern's `_meta.serverInfo`):

```go
server.RegisterTool(
    mcp.NewTool("weather", "Get the weather").
        Icons(mcp.Icon{Src: "https://example.com/weather.png", MimeType: "image/png"}),
    handler,
)
server.SetIcons(mcp.Icon{Src: "https://example.com/server.png"})
```

This library only carries icon metadata — it never fetches or renders icon
bytes. A consumer that does render icons is responsible for the spec's
security precautions (reject non-`https(s)`/`data:` schemes, verify
same-origin, fetch without credentials, validate content by magic bytes).

### Known gaps

- The `x-mcp-header` tool-parameter-to-HTTP-header mirroring mechanism isn't
  implemented — no current tool needs per-parameter header routing.
- MRTR (server-initiated sampling/elicitation/roots) doesn't apply: this
  library implements none of those capabilities in either era.

## Version Negotiation (Legacy)

During initialization, clients specify their preferred protocol version:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2025-11-25",
    "capabilities": {},
    "clientInfo": { "name": "client", "version": "1.0" }
  }
}
```

The server responds with the negotiated version and capabilities:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": "2025-11-25",
    "capabilities": {
      "tools": { "listChanged": false },
      "resources": { "subscribe": false, "listChanged": false }
    },
    "serverInfo": { "name": "server", "version": "1.0" }
  }
}
```

## Protocol Headers (Legacy)

For requests after initialization, Legacy clients must include the protocol
version header:

```
MCP-Protocol-Version: 2025-11-25
```

## Backwards Compatibility

- Every Legacy protocol version continues to work exactly as before — Modern
  support is purely additive, routed to by new code paths that a Legacy
  request never enters.
- New features are additive, not breaking, in both eras.
- Session management is optional (disabled by default) and Legacy-only; Modern
  is stateless by design.

## Testing Different Versions

```bash
# Legacy, latest
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "MCP-Protocol-Version: 2025-11-25" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}'

# Legacy, oldest
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "MCP-Protocol-Version: 2024-11-05" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}'

# Modern
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "MCP-Protocol-Version: 2026-07-28" \
  -H "Mcp-Method: server/discover" \
  -d '{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}'
```

## Troubleshooting

**Error: "Unsupported protocol version"**

- Check that the requested version is in the supported list for its era.
- Update the client to use a supported version.

**Error: "Missing MCP-Protocol-Version header" (Legacy)**

- For Legacy protocol versions 2025-03-26+, the header is required after
  initialization. Add it to every request after `initialize`.

**Error: "Header mismatch" (Modern)**

- `Mcp-Method`, `Mcp-Name`, and `MCP-Protocol-Version` must exactly match the
  corresponding value in the JSON-RPC body. This is almost always a client bug
  — a hand-rolled Modern request that didn't mirror the body into headers.

**Sessions not working (Legacy only — Modern has none by design)**

- Session management is disabled by default.
- Use `mcp.NewJWTSessionManagerWithAutoKey()` or `mcp.NewJWTSessionManager()` to
  create a session manager.
- Call `server.SetSessionManager()` to enable sessions.
- For custom storage, implement the `SessionManager` interface.

**A Modern client seems stuck on Legacy, or vice versa**

- `Client` detects era once, during `Initialize`, and caches it for that
  `Client`'s lifetime. A fresh `Client` re-detects from scratch.
- `RemoteProvider`-based tool federation creates a fresh `Client` per refresh
  today, so era is re-detected on every refresh — correct, if slightly
  wasteful, not a bug.
