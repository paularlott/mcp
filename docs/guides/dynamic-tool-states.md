# Dynamic Tool State Transitions: Implementation Guide

This document explains how to dynamically transition tools between native and ondemand visibility modes in the MCP core library. This is useful for feature gating, progressive disclosure, and per-request access control.

## Overview

Tools in the MCP server can operate in two visibility modes:

- **Native Mode**: Tools appear in `tools/list` and are immediately available to clients
- **OnDemand Mode**: Tools are hidden from `tools/list` but remain discoverable and callable via `tool_search` and `execute_tool`

By switching contexts per request, you can dynamically control which tools are visible to which clients.

## Core Concepts

### Visibility Modes

```go
// Native Mode - All tools visible
ctx := WithToolProviders(context.Background(), provider)

// OnDemand Mode - Only discovery tools visible
ctx := WithForceOnDemandMode(context.Background(), provider)
```

### Context-Based Control

Each request gets its own context, allowing different clients to see different tools:

```go
// Request 1: Admin sees all tools
ctxAdmin := WithToolProviders(context.Background(), provider)

// Request 2: User sees only discovery tools
ctxUser := WithForceOnDemandMode(context.Background(), provider)

// Same server, different visibility per request
adminTools := server.ListToolsWithContext(ctxAdmin)
userTools := server.ListToolsWithContext(ctxUser)
```

## Implementation Patterns

### Pattern 1: Simple Native → OnDemand Transition

**Scenario**: All tools start visible, then get hidden

```go
func transitionToolsToOnDemand(ctx context.Context, provider ToolProvider) context.Context {
    // Was native mode:
    // ctx := WithToolProviders(context.Background(), provider)

    // Switch to ondemand mode:
    return WithForceOnDemandMode(context.Background(), provider)
}
```

### Pattern 2: Role-Based Visibility (Recommended)

**Scenario**: Different user roles see different tool visibility

```go
func getContextForUser(user *User, provider ToolProvider) context.Context {
    // Determine visibility based on role
    if hasElevatedRole(user.Role) {
        // Admins/power users see everything in native mode
        return WithToolProviders(context.Background(), provider)
    } else {
        // Regular users see only discovery tools
        return WithForceOnDemandMode(context.Background(), provider)
    }
}

// Usage in HTTP handler:
func handleMCPRequest(w http.ResponseWriter, r *http.Request) {
    user := getUser(r)  // Extract from auth header
    provider := createToolProvider(user)
    ctx := getContextForUser(user, provider)

    // Server respects context mode for this request
    server.HandleRequest(w, r.WithContext(ctx))
}
```

### Pattern 3: Feature Flag Control

**Scenario**: Tools are visible/hidden based on feature flags

```go
func getContextWithFeatureFlags(userID string, featureFlags FeatureFlags) context.Context {
    providers := []ToolProvider{}

    if featureFlags.IsEnabled("advanced_tools", userID) {
        providers = append(providers, advancedToolProvider)
    }

    if featureFlags.IsEnabled("sensitive_operations", userID) {
        providers = append(providers, sensitiveOpsProvider)
    }

    // Determine mode based on flags
    if featureFlags.IsEnabled("hide_tool_list", userID) {
        return WithForceOnDemandMode(context.Background(), providers...)
    } else {
        return WithToolProviders(context.Background(), providers...)
    }
}
```

### Pattern 4: Progressive Disclosure

**Scenario**: Tools gradually revealed as user advances

```go
func getContextByUserTier(userTier int, provider ToolProvider) context.Context {
    switch userTier {
    case 1:
        // Tier 1: Only basic tools visible
        return WithForceOnDemandMode(context.Background(), provider)

    case 2:
        // Tier 2: Tools visible, limited set
        return WithToolProviders(context.Background(), provider)

    case 3:
        // Tier 3: All tools visible, all discoverable
        return WithToolProviders(context.Background(), provider)

    default:
        // Fallback: conservative (hidden)
        return WithForceOnDemandMode(context.Background(), provider)
    }
}
```

### Pattern 5: Conditional Visibility with Middleware

**Scenario**: HTTP middleware controls visibility per request

```go
type visibilityMiddleware struct {
    server *mcp.Server
    provider mcp.ToolProvider
}

func (m *visibilityMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // Extract user info
    user := extractUserFromAuth(r)

    // Determine visibility mode
    var ctx context.Context
    if user.HasPermission("view_all_tools") {
        ctx = mcp.WithToolProviders(r.Context(), m.provider)
    } else {
        ctx = mcp.WithForceOnDemandMode(r.Context(), m.provider)
    }

    // Update request with new context
    r = r.WithContext(ctx)

    // Continue to server
    m.server.HandleRequest(w, r)
}

// Usage:
func setupRouting(server *mcp.Server, provider mcp.ToolProvider) {
    middleware := &visibilityMiddleware{
        server:   server,
        provider: provider,
    }

    http.Handle("/mcp", middleware)
}
```

## Complete Example: Multi-Tenant Tool Visibility

```go
package main

import (
    "context"
    "net/http"
    "github.com/paularlott/mcp"
)

type Tenant struct {
    ID             string
    Plan           string  // "free", "pro", "enterprise"
    EnabledFeatures []string
}

func createTenantProvider(tenant *Tenant) mcp.ToolProvider {
    // Create provider with tenant-specific tools
    tools := []mcp.MCPTool{}

    if tenant.Plan == "pro" || tenant.Plan == "enterprise" {
        tools = append(tools, mcp.MCPTool{
            Name:        "advanced_analytics",
            Description: "Advanced analytics for pro users",
        })
    }

    if tenant.Plan == "enterprise" {
        tools = append(tools, mcp.MCPTool{
            Name:        "custom_workflows",
            Description: "Custom workflow automation",
        })
    }

    // Return as provider
    return &staticProvider{tools: tools}
}

func getTenantContext(tenant *Tenant) context.Context {
    provider := createTenantProvider(tenant)

    // Free plan: ondemand mode (limited visibility)
    if tenant.Plan == "free" {
        return mcp.WithForceOnDemandMode(context.Background(), provider)
    }

    // Pro/Enterprise: native mode (full visibility)
    return mcp.WithToolProviders(context.Background(), provider)
}

func handleMCPRequest(server *mcp.Server, w http.ResponseWriter, r *http.Request) {
    // Extract tenant from subdomain or header
    tenantID := r.Header.Get("X-Tenant-ID")
    tenant := loadTenant(tenantID)

    // Get context with appropriate visibility
    ctx := getTenantContext(tenant)

    // Process request with tenant-specific context
    server.HandleRequest(w, r.WithContext(ctx))
}
```

## Testing Dynamic Transitions

The test file `provider_dynamic_test.go` includes three comprehensive tests:

1. **TestDynamicToolStateTransition**: Shows complete cycle from native → ondemand → native
2. **TestDynamicToolStateTransition_PerRequest**: Concurrent requests with different modes
3. **TestDynamicToolStateTransition_ConditionalVisibility**: Role-based visibility control

Run these tests:

```bash
go test -run "TestDynamicToolState" -v
```

## Key Behaviors to Understand

### Tools are Always Callable

Whether in native or ondemand mode, tools can always be called:

```go
// Both modes work
server.CallTool(ctxNative, "tool_name", args)     // Works
server.CallTool(ctxOnDemand, "tool_name", args)   // Also works
```

### Only Visibility Changes, Not Availability

```go
// Native mode
tools := server.ListToolsWithContext(ctxNative)
// Result: ["tool1", "tool2", "tool_search", "execute_tool"]

// OnDemand mode
tools := server.ListToolsWithContext(ctxOnDemand)
// Result: ["tool_search", "execute_tool"]

// But both can call tool1 and tool2
server.CallTool(ctxOnDemand, "tool1", args)  // Still works!
```

### Discovery Tools are Always Available

In any mode with ondemand tools, `tool_search` and `execute_tool` are always present in the list.

### Context Replacement, Not Merging

Each `WithToolProviders()` or `WithForceOnDemandMode()` call **replaces** the context value, it doesn't merge:

```go
ctx1 := WithToolProviders(background, provider1)
ctx2 := WithToolProviders(ctx1, provider2)

// ctx2 has provider2 only, NOT provider1 + provider2
// To have both: WithToolProviders(background, provider1, provider2)
```

## Common Use Cases

### 1. Rate Limiting Tool Visibility

Hide expensive tools for free tier users:

```go
if user.PlanTier == "free" {
    ctx = mcp.WithForceOnDemandMode(context.Background(), provider)
} else {
    ctx = mcp.WithToolProviders(context.Background(), provider)
}
```

### 2. Gradual Feature Rollout

Show new features to percentage of users:

```go
if isInBeta(user.ID) {
    // New tools visible
    ctx = mcp.WithToolProviders(context.Background(), betaProvider)
} else {
    // New tools hidden, only searchable
    ctx = mcp.WithForceOnDemandMode(context.Background(), betaProvider)
}
```

### 3. Context-Aware Tool Availability

Different tools per deployment environment:

```go
switch os.Getenv("ENVIRONMENT") {
case "production":
    ctx = mcp.WithToolProviders(context.Background(), prodProvider)
case "staging":
    ctx = mcp.WithForceOnDemandMode(context.Background(), stagingProvider)
case "development":
    ctx = mcp.WithForceOnDemandMode(context.Background(), devProvider)
}
```

### 4. Permission-Based Visibility

Tools visible only to users with specific permissions:

```go
perms := user.GetPermissions()

providers := []mcp.ToolProvider{}
if perms.Has("read:data") {
    providers = append(providers, readDataProvider)
}
if perms.Has("write:data") {
    providers = append(providers, writeDataProvider)
}
if perms.Has("admin") {
    providers = append(providers, adminProvider)
}

if len(providers) > 0 {
    ctx = mcp.WithToolProviders(context.Background(), providers...)
} else {
    ctx = mcp.WithForceOnDemandMode(context.Background()) // Empty
}
```

## Performance Considerations

1. **Context Creation**: Lightweight operation, safe to do per-request
2. **Provider Lookup**: O(n) where n = number of tools (typically small)
3. **Deduplication**: Only when listing tools, not during execution

## Security Notes

1. **Visibility ≠ Access Control**: Hiding tools in the list doesn't prevent execution
2. **Implement Access Control**: Check permissions in tool handlers
3. **Search Results**: Filter search results based on user permissions, not just visibility mode

Example:

```go
func (p *provider) GetTools(ctx context.Context) ([]MCPTool, error) {
    user := ctx.Value("user").(*User)

    var allowedTools []MCPTool
    for _, tool := range p.allTools {
        if user.HasPermission(tool.RequiredPermission) {
            allowedTools = append(allowedTools, tool)
        }
    }
    return allowedTools, nil
}
```

## Summary

Dynamic tool state transitions are controlled through context:

1. Use `WithToolProviders()` for **native mode** (visible in list)
2. Use `WithForceOnDemandMode()` for **ondemand mode** (hidden from list)
3. Create the appropriate context per request based on user/tenant attributes
4. Pass context to `server.ListToolsWithContext()` and `server.CallTool()`

This allows a single MCP server to serve different tools to different clients without code changes.
