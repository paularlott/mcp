# Dynamic Tool Visibility - Quick Reference

## The Problem

You want to control which tools are visible in `tools/list` vs searchable-only, and optionally show all tools for MCP chaining.

## The Solution

Tools carry their own visibility. Use show-all mode to reveal everything:

```go
// Native tools visible in tools/list, discoverable tools searchable only
ctx := mcp.WithToolProviders(context.Background(), provider)

// Show-all mode - ALL tools visible (for MCP chaining)
ctx = mcp.WithShowAllTools(ctx)
```

## Quick Start

### Step 1: Define Tool Provider with Visibility

```go
type MyProvider struct{}

func (p *MyProvider) GetTools(ctx context.Context) ([]mcp.MCPTool, error) {
    return []mcp.MCPTool{
        {
            Name:       "send_email",
            Visibility: mcp.ToolVisibilityNative,  // Visible in tools/list
        },
        {
            Name:       "delete_data",
            Keywords:   []string{"danger", "delete"},
            Visibility: mcp.ToolVisibilityDiscoverable,  // Only via tool_search
        },
    }, nil
}
```

### Step 2: Use Providers in Handler

```go
func handler(w http.ResponseWriter, r *http.Request) {
    provider := &MyProvider{}
    ctx := mcp.WithToolProviders(r.Context(), provider)

    // Check for show-all mode (MCP chaining)
    if mcp.GetShowAllFromRequest(r) {
        ctx = mcp.WithShowAllTools(ctx)
    }

    server.HandleRequest(w, r.WithContext(ctx))
}
```

### Step 3: Visibility Behaviors

```go
// Normal mode - native tools visible, discoverable hidden
tools := server.ListToolsWithContext(ctx)
// Result: ["send_email", "tool_search", "execute_tool"]

// Show-all mode - ALL tools visible
ctxShowAll := mcp.WithShowAllTools(ctx)
tools := server.ListToolsWithContext(ctxShowAll)
// Result: ["send_email", "delete_data", "tool_search", "execute_tool"]

// Both tools always callable via execute_tool
server.CallTool(ctx, "delete_data", args)  // Works!
```

## Common Patterns

### Pattern: Context Window Reduction

Mark rarely-used tools as discoverable:

```go
func (p *Provider) GetTools(ctx context.Context) ([]mcp.MCPTool, error) {
    return []mcp.MCPTool{
        {Name: "common_tool", Visibility: mcp.ToolVisibilityNative},
        {Name: "rare_tool", Keywords: []string{"special"}, Visibility: mcp.ToolVisibilityDiscoverable},
    }, nil
}
```

### Pattern: MCP Server Chaining

When connecting to upstream MCP server:

```go
// Set show-all header to see all tools
client.SetHeader(mcp.ShowAllHeader, "true")
// Or use query param: ?show_all=true
```

### Pattern: Role-Based Provider

Different providers for different roles:

```go
func getProviderForUser(user *User) mcp.ToolProvider {
    if user.IsAdmin {
        return adminProvider  // Has more native tools
    }
    return basicProvider
}
```

### Pattern: Middleware

```go
func mcpMiddleware(server *mcp.Server, provider mcp.ToolProvider) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ctx := mcp.WithToolProviders(r.Context(), provider)

        if mcp.GetShowAllFromRequest(r) {
            ctx = mcp.WithShowAllTools(ctx)
        }

        server.HandleRequest(w, r.WithContext(ctx))
    })
}
```

## Key Facts

| Aspect                         | Native Visibility        | Discoverable Visibility | Show-All Mode |
| ------------------------------ | ------------------------ | ----------------------- | ------------- |
| **In tools/list**              | ✅ Yes                   | ❌ No                   | ✅ All tools  |
| **Callable via execute_tool**  | ✅ Yes                   | ✅ Yes                  | ✅ Yes        |
| **Searchable via tool_search** | ✅ Yes (if keywords set) | ✅ Yes                  | ✅ Yes        |
| **Use Case**                   | Core functionality       | Context reduction       | MCP chaining  |

## Testing

Tests demonstrate visibility behaviors:

```bash
# Run visibility tests
go test -run "TestVisibility" -v
go test -run "TestShowAll" -v
```

## Important Notes

1. **Visibility ≠ Security**: Always validate permissions in tool handlers
2. **Providers Accumulate**: Multiple `WithToolProviders()` calls stack providers
3. **Tools Always Callable**: Discoverable tools still work via `execute_tool`
4. **Show-All via HTTP**: Use `X-MCP-Show-All: true` header or `?show_all=true` param

## See Also

- [Dynamic Tool States Guide](../guides/dynamic-tool-states.md) - Full implementation guide
- Provider tests - Check `provider_dynamic_test.go` for test examples
- Tool visibility tests - See `visibility_test.go`
