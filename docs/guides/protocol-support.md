# Protocol Support Guide

The MCP library supports multiple protocol versions with different feature sets. This guide explains the supported versions and their capabilities.

## Supported Protocol Versions

The library supports the following MCP protocol versions:

- **2024-11-05** (Minimum supported)
- **2025-03-26**
- **2025-06-18**
- **2025-11-25** (Latest, recommended)

## Version Features

### 2024-11-05 (Basic)

**Capabilities:**

- Basic tool listing and execution
- Resource access
- No advanced features

**Use Case:** Legacy clients, minimal functionality requirements

### 2025-03-26 and Later (Enhanced)

**New Capabilities:**

- `tools/listChanged`: Support for tool list change notifications
- `resources/subscribe`: Support for resource subscription
- `resources/listChanged`: Support for resource list change notifications

**Use Case:** Modern MCP clients requiring change notifications

### 2025-11-25 (Latest)

**New Features:**

- **Session Management**: JWT-based stateless sessions (recommended for production)
- **Protocol Version Headers**: `MCP-Protocol-Version` header validation
- All features from previous versions

**Use Case:** Production deployments requiring session tracking and latest protocol features

## Version Negotiation

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

## Protocol Headers

For requests after initialization, clients must include the protocol version header:

```
MCP-Protocol-Version: 2025-11-25
```

## Backwards Compatibility

The library maintains backwards compatibility:

- Older protocol versions continue to work
- New features are additive, not breaking
- Session management is optional (disabled by default)

## Migration Guide

### From 2024-11-05 to 2025-03-26+

No code changes required - the server automatically provides enhanced capabilities to supporting clients.

### From 2025-03-26 to 2025-11-25

To enable session management (optional):

```go
server := mcp.NewServer("my-server", "1.0.0")

// Enable JWT sessions
sm, err := mcp.NewJWTSessionManagerWithAutoKey(30 * time.Minute)
if err != nil {
    log.Fatal(err)
}
server.SetSessionManager(sm)
```

## Testing Different Versions

Use the `MCP-Protocol-Version` header to test different protocol versions:

```bash
# Test with latest version
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "MCP-Protocol-Version: 2025-11-25" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}'

# Test with older version
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -H "MCP-Protocol-Version: 2024-11-05" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}'
```

## Troubleshooting

**Error: "Unsupported protocol version"**

- Check that the requested version is in the supported list
- Update client to use a supported version

**Error: "Missing MCP-Protocol-Version header"**

- For protocol versions 2025-03-26+, the header is required after initialization
- Add the header to all requests after the initialize call

**Sessions not working**

- Session management is disabled by default
- Use `mcp.NewJWTSessionManagerWithAutoKey()` or `mcp.NewJWTSessionManager()` to create a session manager
- Call `server.SetSessionManager()` to enable sessions
- For custom storage, implement `SessionManager` interface</content>
  <parameter name="filePath">/Users/paul/Code/Source/mcp/docs/guides/protocol-support.md
