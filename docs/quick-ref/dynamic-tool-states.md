# Dynamic Tool States - Quick Reference

## The Problem

You want to dynamically control which tools are visible to different users/requests while keeping them callable in both modes.

## The Solution

Use context to switch between visibility modes per request:

```go
// Native mode - tools visible in tools/list
ctx := mcp.WithToolProviders(context.Background(), provider)

// OnDemand mode - tools hidden from tools/list
ctx := mcp.WithForceOnDemandMode(context.Background(), provider)
```

## Quick Start

### Step 1: Define Tool Provider

```go
provider := &myToolProvider{
    tools: []mcp.MCPTool{
        {Name: "send_email", Description: "Send emails"},
        {Name: "delete_data", Description: "Delete user data"},
    },
}
```

### Step 2: Choose Visibility Based on User

```go
// In HTTP handler
user := getUser(r)
var ctx context.Context

if user.IsAdmin {
    // Admins see all tools
    ctx = mcp.WithToolProviders(r.Context(), provider)
} else {
    // Users see only search/execute tools
    ctx = mcp.WithForceOnDemandMode(r.Context(), provider)
}

server.HandleRequest(w, r.WithContext(ctx))
```

### Step 3: Both Modes Remain Functional

```go
// Admin can see and call tools normally
adminTools := server.ListToolsWithContext(adminCtx)  // Shows tools
adminResp := server.CallTool(adminCtx, "send_email", args)  // Works

// User cannot see tools in list, but can search and call
userTools := server.ListToolsWithContext(userCtx)  // Shows only discovery tools
userResp := server.CallTool(userCtx, "send_email", args)  // Still works!
userSearch := server.CallTool(userCtx, "tool_search", {"query": "email"})  // Finds tools
```

## Three-Stage Transition Example

```go
server := mcp.NewServer("test", "1.0.0")
provider := getProvider()

// Stage 1: Native - tools visible
ctx1 := mcp.WithToolProviders(background, provider)
tools1 := server.ListToolsWithContext(ctx1)  // See all tools

// Stage 2: OnDemand - tools hidden
ctx2 := mcp.WithForceOnDemandMode(background, provider)
tools2 := server.ListToolsWithContext(ctx2)  // See only discovery tools

// Stage 3: Back to Native
ctx3 := mcp.WithToolProviders(background, provider)
tools3 := server.ListToolsWithContext(ctx3)  // See all tools again
```

## Common Patterns

### Pattern: Role-Based Access

```go
func getContext(user *User, provider ToolProvider) context.Context {
    if user.Role == "admin" || user.Role == "power_user" {
        return mcp.WithToolProviders(context.Background(), provider)
    }
    return mcp.WithForceOnDemandMode(context.Background(), provider)
}
```

### Pattern: Feature Flags

```go
func getContext(userID string, flags FeatureFlags, provider ToolProvider) context.Context {
    if flags.IsEnabled("show_tools", userID) {
        return mcp.WithToolProviders(context.Background(), provider)
    }
    return mcp.WithForceOnDemandMode(context.Background(), provider)
}
```

### Pattern: Subscription Tier

```go
func getContext(user *User, provider ToolProvider) context.Context {
    if user.SubscriptionTier == "free" {
        // Free users: tools hidden
        return mcp.WithForceOnDemandMode(context.Background(), provider)
    }
    // Paid users: tools visible
    return mcp.WithToolProviders(context.Background(), provider)
}
```

### Pattern: Middleware

```go
func visibilityMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        user := extractUser(r)
        ctx := getContextForUser(user, provider)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

## Key Facts

| Aspect                         | Native Mode              | OnDemand Mode                |
| ------------------------------ | ------------------------ | ---------------------------- |
| **In tools/list**              | ✅ Yes                   | ❌ No (only discovery tools) |
| **Callable via CallTool()**    | ✅ Yes                   | ✅ Yes                       |
| **Searchable via tool_search** | ✅ Yes (if keywords set) | ✅ Yes                       |
| **Use Case**                   | Full tool visibility     | Progressive disclosure       |

## Testing

Three tests demonstrate state transitions:

```bash
# Run all state transition tests
go test -run "TestDynamicToolState" -v

# Outputs:
# - TestDynamicToolStateTransition (complete cycle)
# - TestDynamicToolStateTransition_PerRequest (per-request isolation)
# - TestDynamicToolStateTransition_ConditionalVisibility (role-based)
```

## Important Notes

1. **Visibility ≠ Security**: Always validate permissions in tool handlers
2. **Context Per Request**: Create new context for each request, don't reuse
3. **Tools Still Work**: Hidden tools can still be called, just not listed
4. **No Merging**: `WithToolProviders(ctx, p2)` replaces, not adds to `ctx`

## See Also

- [Dynamic Tool States Guide](../guides/dynamic-tool-states.md) - Full implementation guide
- Provider tests - Check `provider_dynamic_test.go` for test examples
- Tool visibility tests - See `mode_behavior_test.go`
