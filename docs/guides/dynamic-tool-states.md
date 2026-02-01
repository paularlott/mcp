# Dynamic Tool Visibility: Implementation Guide

This document explains how to control tool visibility dynamically in the MCP library. Tools can be native (always visible) or discoverable (searchable only), and you can also use show-all mode to reveal all tools.

## Overview

Tools in the MCP server have a visibility property:

- **Native**: Tools appear in `tools/list` and are immediately available to clients
- **Discoverable**: Tools are hidden from `tools/list` but remain searchable via `tool_search`

Additionally, you can use **Show-All Mode** to reveal all tools (useful for MCP chaining).

## Core Concepts

### Tool Visibility on MCPTool

```go
// Native tool - visible in tools/list
tool := mcp.MCPTool{
    Name:       "visible_tool",
    Visibility: mcp.ToolVisibilityNative,
}

// Discoverable tool - only via tool_search
tool := mcp.MCPTool{
    Name:       "hidden_tool",
    Keywords:   []string{"search", "keywords"},
    Visibility: mcp.ToolVisibilityDiscoverable,
}
```

### Show-All Mode for MCP Chaining

When one MCP server connects to another, use show-all mode to see all tools:

```go
// Normal mode - native tools visible, discoverable hidden
ctx := mcp.WithToolProviders(context.Background(), provider)

// Show-all mode - ALL tools visible (for MCP chaining)
ctx = mcp.WithShowAllTools(ctx)
```

### Context-Based Control

Each request gets its own context, allowing different clients to see different tools:

```go
// Request 1: Normal mode (native tools + discovery if discoverable exist)
ctxNormal := mcp.WithToolProviders(context.Background(), provider)

// Request 2: Show-all mode (all tools visible)
ctxShowAll := mcp.WithToolProviders(context.Background(), provider)
ctxShowAll = mcp.WithShowAllTools(ctxShowAll)

// Same server, different visibility per request
normalTools := server.ListToolsWithContext(ctxNormal)
showAllTools := server.ListToolsWithContext(ctxShowAll)
```

## Implementation Patterns

### Pattern 1: Native vs Discoverable Tools

**Scenario**: Some tools are always visible, others require search

```go
type MyProvider struct{}

func (p *MyProvider) GetTools(ctx context.Context) ([]mcp.MCPTool, error) {
    return []mcp.MCPTool{
        {
            Name:        "essential_tool",
            Description: "Always visible to users",
            Visibility:  mcp.ToolVisibilityNative,
        },
        {
            Name:        "specialized_tool",
            Description: "Only via search to reduce context",
            Keywords:    []string{"specialized", "advanced"},
            Visibility:  mcp.ToolVisibilityDiscoverable,
        },
    }, nil
}
```

### Pattern 2: Role-Based Visibility (Recommended)

**Scenario**: Different user roles see different tools

```go
func getProviderForUser(user *User) mcp.ToolProvider {
    return &UserProvider{user: user}
}

type UserProvider struct {
    user *User
}

func (p *UserProvider) GetTools(ctx context.Context) ([]mcp.MCPTool, error) {
    tools := []mcp.MCPTool{
        {Name: "basic_tool", Visibility: mcp.ToolVisibilityNative},
    }

    if p.user.HasRole("admin") {
        tools = append(tools, mcp.MCPTool{
            Name:       "admin_tool",
            Visibility: mcp.ToolVisibilityNative,
        })
    }

    // Sensitive tools are discoverable for everyone, but filtering happens in ExecuteTool
    tools = append(tools, mcp.MCPTool{
        Name:       "sensitive_tool",
        Keywords:   []string{"sensitive"},
        Visibility: mcp.ToolVisibilityDiscoverable,
    })

    return tools, nil
}

// Usage in HTTP handler:
func handleMCPRequest(w http.ResponseWriter, r *http.Request) {
    user := getUser(r)
    provider := getProviderForUser(user)
    ctx := mcp.WithToolProviders(r.Context(), provider)
    server.HandleRequest(w, r.WithContext(ctx))
}
```

### Pattern 3: Feature Flag Control

**Scenario**: Tools are visible/hidden based on feature flags

```go
func getContextWithFeatureFlags(userID string, featureFlags FeatureFlags) context.Context {
    providers := []mcp.ToolProvider{}

    if featureFlags.IsEnabled("advanced_tools", userID) {
        providers = append(providers, advancedToolProvider)
    }

    if featureFlags.IsEnabled("sensitive_operations", userID) {
        providers = append(providers, sensitiveOpsProvider)
    }

    return mcp.WithToolProviders(context.Background(), providers...)
}
```

### Pattern 4: Progressive Disclosure with Providers

**Scenario**: Different user tiers get different tool sets

```go
func getProviderForTier(userTier int) mcp.ToolProvider {
    switch userTier {
    case 1:
        return basicToolProvider // Only basic tools
    case 2:
        return proToolProvider   // Basic + pro tools
    case 3:
        return enterpriseProvider // All tools including advanced
    default:
        return basicToolProvider
    }
}
```

### Pattern 5: Conditional Visibility with Middleware

**Scenario**: HTTP middleware controls tool availability per request

```go
type visibilityMiddleware struct {
    server   *mcp.Server
    provider mcp.ToolProvider
}

func (m *visibilityMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    user := extractUserFromAuth(r)

    // Set up context with provider
    ctx := mcp.WithToolProviders(r.Context(), m.provider)

    // Check for show-all mode (MCP chaining)
    if mcp.GetShowAllFromRequest(r) {
        ctx = mcp.WithShowAllTools(ctx)
    }

    r = r.WithContext(ctx)
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
    ID              string
    Plan            string // "free", "pro", "enterprise"
    EnabledFeatures []string
}

type TenantProvider struct {
    tenant *Tenant
}

func (p *TenantProvider) GetTools(ctx context.Context) ([]mcp.MCPTool, error) {
    tools := []mcp.MCPTool{
        // Basic tools for all plans
        {
            Name:        "basic_tool",
            Description: "Available to all users",
            Visibility:  mcp.ToolVisibilityNative,
        },
    }

    if p.tenant.Plan == "pro" || p.tenant.Plan == "enterprise" {
        tools = append(tools, mcp.MCPTool{
            Name:        "advanced_analytics",
            Description: "Advanced analytics for pro users",
            Visibility:  mcp.ToolVisibilityNative,
        })
    }

    if p.tenant.Plan == "enterprise" {
        tools = append(tools, mcp.MCPTool{
            Name:        "custom_workflows",
            Description: "Custom workflow automation",
            Visibility:  mcp.ToolVisibilityNative,
        })
    }

    // Add discoverable tools for all plans (searchable but hidden)
    tools = append(tools, mcp.MCPTool{
        Name:        "specialized_report",
        Description: "Generate specialized reports",
        Keywords:    []string{"report", "export", "analysis"},
        Visibility:  mcp.ToolVisibilityDiscoverable,
    })

    return tools, nil
}

func handleMCPRequest(server *mcp.Server, w http.ResponseWriter, r *http.Request) {
    tenantID := r.Header.Get("X-Tenant-ID")
    tenant := loadTenant(tenantID)

    provider := &TenantProvider{tenant: tenant}
    ctx := mcp.WithToolProviders(r.Context(), provider)

    // Check if MCP chaining (another MCP server requesting)
    if mcp.GetShowAllFromRequest(r) {
        ctx = mcp.WithShowAllTools(ctx)
    }

    server.HandleRequest(w, r.WithContext(ctx))
}
```

## Testing Tool Visibility

The test file `provider_dynamic_test.go` includes comprehensive tests:

1. **TestDynamicToolProvider**: Shows provider returning native and discoverable tools
2. **TestShowAllToolsVisibility**: Tests show-all mode revealing all tools

Run these tests:

```bash
go test -run "TestDynamic" -v
```

## Key Behaviors to Understand

### Tools are Always Callable

Whether native or discoverable, tools can always be called via `execute_tool`:

```go
// Both visibility types work
server.CallTool(ctx, "native_tool", args)       // Works
server.CallTool(ctx, "discoverable_tool", args) // Also works via execute_tool
```

### Visibility Controls Listing, Not Execution

```go
// Normal mode (no show-all)
tools := server.ListToolsWithContext(ctx)
// Result: ["native_tool", "tool_search", "execute_tool"]
// (discoverable_tool hidden but callable)

// Show-all mode
ctxShowAll := mcp.WithShowAllTools(ctx)
tools := server.ListToolsWithContext(ctxShowAll)
// Result: ["native_tool", "discoverable_tool", "tool_search", "execute_tool"]
```

### Discovery Tools Appear When Discoverable Tools Exist

When any tools have `ToolVisibilityDiscoverable`, the server automatically includes:

- `tool_search` - Search for discoverable tools
- `execute_tool` - Execute any tool by name

### Providers Accumulate

Multiple calls to `WithToolProviders()` accumulate providers:

```go
ctx1 := mcp.WithToolProviders(background, provider1)
ctx2 := mcp.WithToolProviders(ctx1, provider2)

// ctx2 has BOTH provider1 AND provider2 tools
```

## Common Use Cases

### 1. Reduce Context Window Usage

Mark rarely-used tools as discoverable to reduce AI context:

```go
func (p *Provider) GetTools(ctx context.Context) ([]mcp.MCPTool, error) {
    return []mcp.MCPTool{
        // Frequently used - always visible
        {Name: "common_tool", Visibility: mcp.ToolVisibilityNative},

        // Rarely used - discoverable only
        {Name: "specialized_tool", Keywords: []string{"advanced"}, Visibility: mcp.ToolVisibilityDiscoverable},
    }, nil
}
```

### 2. Gradual Feature Rollout

Mark beta tools as discoverable, graduate to native when stable:

```go
func (p *Provider) GetTools(ctx context.Context) ([]mcp.MCPTool, error) {
    tools := []mcp.MCPTool{
        {Name: "stable_tool", Visibility: mcp.ToolVisibilityNative},
    }

    // Beta tools are discoverable until stable
    if p.featureFlags.Has("show_beta_tools") {
        tools = append(tools, mcp.MCPTool{
            Name:       "beta_tool",
            Visibility: mcp.ToolVisibilityNative, // Visible when flag enabled
        })
    } else {
        tools = append(tools, mcp.MCPTool{
            Name:       "beta_tool",
            Keywords:   []string{"beta", "experimental"},
            Visibility: mcp.ToolVisibilityDiscoverable, // Hidden otherwise
        })
    }
    return tools, nil
}
```

### 3. MCP Server Chaining

When one MCP server connects to another, use show-all mode:

```go
func connectToUpstreamMCP(upstreamURL string) *mcp.Client {
    client := mcp.NewClient(upstreamURL)

    // Request show-all mode to see ALL tools from upstream
    client.SetHeader(mcp.ShowAllHeader, "true")
    // Or use query param: ?show_all=true

    return client
}
```

### 4. Permission-Based Tool Sets

Different providers for different permission levels:

```go
func getProvidersForUser(user *User) []mcp.ToolProvider {
    providers := []mcp.ToolProvider{basicToolProvider}

    if user.HasPermission("read:data") {
        providers = append(providers, readDataProvider)
    }
    if user.HasPermission("write:data") {
        providers = append(providers, writeDataProvider)
    }
    if user.HasPermission("admin") {
        providers = append(providers, adminProvider)
    }

    return providers
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
    user := getUser(r)
    providers := getProvidersForUser(user)
    ctx := mcp.WithToolProviders(r.Context(), providers...)
    server.HandleRequest(w, r.WithContext(ctx))
}
```

## Performance Considerations

1. **Context Creation**: Lightweight operation, safe to do per-request
2. **Provider Lookup**: O(n) where n = number of tools (typically small)
3. **Deduplication**: Only when listing tools, not during execution

## Security Notes

1. **Visibility ≠ Access Control**: Hiding tools in the list doesn't prevent execution
2. **Implement Access Control**: Check permissions in tool handlers
3. **Search Results**: Filter search results based on user permissions

Example with permission-based filtering in provider:

```go
func (p *provider) GetTools(ctx context.Context) ([]mcp.MCPTool, error) {
    user := ctx.Value("user").(*User)

    var allowedTools []mcp.MCPTool
    for _, tool := range p.allTools {
        if user.HasPermission(tool.RequiredPermission) {
            allowedTools = append(allowedTools, tool)
        }
    }
    return allowedTools, nil
}
```

## Summary

Tool visibility is controlled at the tool level:

1. Set `Visibility: mcp.ToolVisibilityNative` for tools visible in `tools/list`
2. Set `Visibility: mcp.ToolVisibilityDiscoverable` for tools only via `tool_search`
3. Use `WithShowAllTools()` for MCP chaining (reveals ALL tools)
4. Use `WithToolProviders()` to inject dynamic providers per-request

This allows a single MCP server to serve different tools to different clients without code changes.
