# Tool Providers Guide

For multi-tenant or per-user applications, use `ToolProvider` to inject tools dynamically per-request. Tools registered via `RegisterTool()` are global and visible to all requests. For per-tenant/user isolation you **must** use the provider pattern.

## The ToolProvider Interface

```go
type ToolProvider interface {
    GetTools(ctx context.Context) ([]MCPTool, error)
    ExecuteTool(ctx context.Context, name string, params map[string]any) (interface{}, error)
}
```

Inject providers into the request context:

```go
ctx := mcp.WithToolProviders(r.Context(), provider1, provider2)
```

Multiple calls accumulate — providers stack, first match wins on execution.

## Tool Visibility

Each tool carries a `Visibility` field:

| Visibility | In `tools/list` | Via `tool_search` | Callable |
|---|---|---|---|
| `ToolVisibilityNative` | ✅ | ✅ (if keywords set) | ✅ |
| `ToolVisibilityDiscoverable` | ❌ | ✅ | ✅ via `execute_tool` |

When any discoverable tools exist, `tool_search` and `execute_tool` are automatically registered.

## Basic Example

```go
type UserProvider struct {
    user *User
}

func (p *UserProvider) GetTools(ctx context.Context) ([]mcp.MCPTool, error) {
    tools := []mcp.MCPTool{
        {
            Name:        "get_profile",
            Description: "Get user profile",
            InputSchema: schema,
            Visibility:  mcp.ToolVisibilityNative,
        },
    }

    if p.user.HasRole("admin") {
        tools = append(tools, mcp.MCPTool{
            Name:       "admin_panel",
            Visibility: mcp.ToolVisibilityNative,
        })
    }

    // Discoverable: searchable but not in tools/list
    tools = append(tools, mcp.MCPTool{
        Name:        "advanced_settings",
        Description: "Advanced configuration options",
        Keywords:    []string{"config", "settings", "advanced"},
        Visibility:  mcp.ToolVisibilityDiscoverable,
    })

    return tools, nil
}

func (p *UserProvider) ExecuteTool(ctx context.Context, name string, params map[string]any) (interface{}, error) {
    switch name {
    case "get_profile":
        return p.getUserProfile(), nil
    case "admin_panel":
        if !p.user.HasRole("admin") {
            return nil, fmt.Errorf("access denied")
        }
        return p.getAdminData(), nil
    case "advanced_settings":
        return p.getAdvancedSettings(), nil
    }
    return nil, mcp.ErrUnknownTool
}

func handler(w http.ResponseWriter, r *http.Request) {
    user := authenticateUser(r)
    if user == nil {
        http.Error(w, "Unauthorized", 401)
        return
    }

    ctx := mcp.WithToolProviders(r.Context(), &UserProvider{user: user})

    if mcp.GetShowAllFromRequest(r) {
        ctx = mcp.WithShowAllTools(ctx)
    }

    server.HandleRequest(w, r.WithContext(ctx))
}
```

## Building Tools for Providers

Use the fluent builder with `ToMCPTool()`:

```go
func (p *MyProvider) GetTools(ctx context.Context) ([]mcp.MCPTool, error) {
    native := mcp.NewTool("search", "Search records",
        mcp.String("query", "Search query", mcp.Required()),
    ).ToMCPTool()

    discoverable := mcp.NewTool("export_csv", "Export data as CSV",
        mcp.String("filter", "Optional filter"),
    ).Discoverable("export", "csv", "download").ToMCPTool()

    return []mcp.MCPTool{native, discoverable}, nil
}
```

## Show-All Mode

When one MCP server connects to another, use show-all mode to expose all tools including discoverable ones:

```go
// Programmatic
ctx = mcp.WithShowAllTools(ctx)

// Via HTTP header
X-MCP-Show-All: true

// Via query param
/mcp?show_all=true
```

In your handler, respect the header automatically:

```go
ctx := mcp.WithToolProviders(r.Context(), provider)
if mcp.GetShowAllFromRequest(r) {
    ctx = mcp.WithShowAllTools(ctx)
}
server.HandleRequest(w, r.WithContext(ctx))
```

## Common Patterns

### Permission-Based Tool Sets

```go
func getProviders(user *User) []mcp.ToolProvider {
    providers := []mcp.ToolProvider{basicProvider}

    if user.HasPermission("read:data") {
        providers = append(providers, readDataProvider)
    }
    if user.HasPermission("admin") {
        providers = append(providers, adminProvider)
    }

    return providers
}

func handler(w http.ResponseWriter, r *http.Request) {
    user := getUser(r)
    ctx := mcp.WithToolProviders(r.Context(), getProviders(user)...)
    server.HandleRequest(w, r.WithContext(ctx))
}
```

### Tiered Plans

```go
func providerForTier(tier string) mcp.ToolProvider {
    switch tier {
    case "pro":
        return proProvider        // basic + pro tools
    case "enterprise":
        return enterpriseProvider // all tools
    default:
        return basicProvider
    }
}
```

### Feature Flags

```go
func buildContext(r *http.Request, userID string, flags FeatureFlags) context.Context {
    providers := []mcp.ToolProvider{coreProvider}

    if flags.IsEnabled("advanced_tools", userID) {
        providers = append(providers, advancedProvider)
    }

    return mcp.WithToolProviders(r.Context(), providers...)
}
```

## ExecuteTool Return Values

`ExecuteTool` return values are automatically converted:

- `*mcp.ToolResponse` — used directly
- `string` — wrapped as text response
- anything else — serialised as JSON text

## Security

- **Providers are per-request**: created fresh with the authenticated user/tenant each time
- **Visibility ≠ access control**: hiding a tool doesn't prevent execution — always validate permissions inside `ExecuteTool`
- **No shared state**: the server stores no user/tenant data; all isolation is in the provider

## See Also

- [Tool Discovery Guide](tool-discovery.md) — `tool_search` and context window optimisation
- [Remote Servers Guide](remote-servers.md) — client-side tool filtering
