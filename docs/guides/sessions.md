# Session Management Guide

Enable optional session tracking for stateful interactions. **Sessions are disabled by default** and remain optional per the MCP spec.

## Production Deployment (Recommended)

JWT-based sessions provide stateless, scalable session management with zero infrastructure dependencies:

```go
server := mcp.NewServer("my-server", "1.0.0")

// Enable JWT session management (stateless, production-ready)
if err := server.EnableSessionManagement(); err != nil {
    log.Fatal(err)
}

// No cleanup needed - JWT sessions self-expire
// Sessions are validated on every request, no storage required
```

### Why JWT Sessions?

- ✅ **Zero Dependencies**: No Redis, Database, or external storage needed
- ✅ **Horizontal Scaling**: Works perfectly across all server instances
- ✅ **Stateless**: Each server validates sessions independently
- ✅ **Simple**: Just enable and it works in any deployment
- ✅ **Secure**: Cryptographically signed with HMAC-SHA256

Trade-off: Sessions cannot be revoked before expiry (acceptable for most use cases)

## All Deployments

JWT sessions work perfectly for **all** deployment scenarios:

- ✅ Single instance (development)
- ✅ Multiple instances (production clusters)
- ✅ Serverless/edge deployments
- ✅ Multi-region architectures

No need for different session managers based on deployment type.

## Advanced: Custom Session Storage

If you need session revocation or custom storage:

```go
// Redis (requires external dependency)
rdb := redis.NewClient(&redis.Options{Addr: "redis:6379"})
server.SetSessionManager(mcp.NewRedisSessionManager(rdb, 30*time.Minute))

// Custom signing key (persist JWT key across restarts)
signingKey := loadKeyFromSecureStorage()
server.EnableSessionManagementWithKey(signingKey, 30*time.Minute)

// Implement your own SessionManager interface
server.SetSessionManager(myCustomManager)
```

## Session Behavior

When session management is enabled:

- Server generates a secure session ID on initialization
- Returns `MCP-Session-Id` header in the initialize response
- Clients must include `MCP-Session-Id` on all subsequent requests
- Server validates session existence and returns 404 if not found
- Clients can terminate sessions via DELETE requests

Session management is **optional** and backwards compatible. Servers without session management enabled work exactly as before.

## Use Cases

- **Stateful workflows**: Maintain state across multiple tool calls
- **User tracking**: Track which user made which requests
- **Rate limiting**: Per-session rate limiting
- **Context preservation**: Keep context between requests
- **Audit logging**: Track session history

## Security Considerations

1. **Session expiration**: Configure appropriate TTL for your use case
2. **Transport security**: Always use HTTPS in production
3. **Key rotation**: Implement key rotation for signing keys
4. **Revocation**: Use custom SessionManager if revocation is required

## See Also

- Tool Providers Guide - Per-request context and authentication
- Protocol Support documentation for MCP 2025-11-25 specification
