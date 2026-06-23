# Tool Providers Guide

For multi-tenant or per-user applications, use `ToolProvider` to inject tools dynamically per-request. Tools registered via `RegisterTool()` are global and visible to all requests. For per-tenant/user isolation you **must** use the provider pattern.

## The ToolProvider Interface

```go
type ToolProvider interface {
    GetTools(ctx context.Context) ([]MCPTool, error)
    ExecuteTool(ctx context.Context, name string, params map[string]any) (*ToolResponse, error)
}
```

Inject providers into the request context:

```go
ctx := mcp.WithToolProviders(r.Context(), provider1, provider2)
```

Multiple calls accumulate — providers stack, first match wins on execution.

### The per-request entry point

In an HTTP handler, prefer the single helper `WithShowAllFromRequest`. It
attaches your providers **and** applies show-all mode from the request
(`X-MCP-Show-All` header or `?show_all=true`) in one call, so you don't have to
hand-roll the glue:

```go
func handler(w http.ResponseWriter, r *http.Request) {
    user := authenticateUser(r)
    if user == nil {
        http.Error(w, "Unauthorized", 401)
        return
    }
    ctx := mcp.WithShowAllFromRequest(r.Context(), r, providersFor(user)...)
    server.HandleRequest(w, r.WithContext(ctx))
}
```

This is equivalent to calling `WithToolProviders` followed by a
`GetShowAllFromRequest`/`WithShowAllTools` check, just without the boilerplate.

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

func (p *UserProvider) ExecuteTool(ctx context.Context, name string, params map[string]any) (*mcp.ToolResponse, error) {
    switch name {
    case "get_profile":
        return mcp.NewToolResponseJSON(p.getUserProfile()), nil
    case "admin_panel":
        if !p.user.HasRole("admin") {
            return nil, fmt.Errorf("access denied")
        }
        return mcp.NewToolResponseJSON(p.getAdminData()), nil
    case "advanced_settings":
        return mcp.NewToolResponseJSON(p.getAdvancedSettings()), nil
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

In your handler, respect the header automatically with the request helper:

```go
ctx := mcp.WithShowAllFromRequest(r.Context(), r, provider)
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

## The Miss Contract

When a provider does not handle a requested tool, return `(nil, nil)`. This is
the canonical "not handled" signal — the server (and `MultiProvider`) moves on
to the next provider, and if no provider handles the tool the caller gets
`ErrUnknownTool`. Returning `(nil, mcp.ErrUnknownTool)` is also accepted and
behaves identically, so existing code using either style keeps working.

Reserve real errors for genuine failures (bad input, backend down, permission
denied). A non-nil, non-`ErrUnknownTool` error aborts dispatch immediately and
is returned to the caller rather than being treated as a miss.

```go
func (p *MyProvider) ExecuteTool(ctx context.Context, name string, params map[string]any) (*mcp.ToolResponse, error) {
    switch name {
    case "get_profile":
        return mcp.NewToolResponseJSON(p.getUserProfile()), nil
    case "admin_panel":
        if !p.user.HasRole("admin") {
            return nil, fmt.Errorf("access denied") // real error: aborts dispatch
        }
        return mcp.NewToolResponseJSON(p.getAdminData()), nil
    }
    return nil, nil // miss: let other providers try
}
```

## Combining Providers

`MultiProvider` composes several providers into one, which is handy when you
build a provider set conditionally and want to attach a single value:

```go
providers := mcp.NewMultiProvider(coreProvider, scriptProvider, remoteProvider)
if providers != nil { // nil when every argument was nil
    ctx = mcp.WithToolProviders(ctx, providers)
}
```

Semantics:

- `GetTools` aggregates every provider's tools in order. A provider whose
  `GetTools` errors is skipped (matching the server's own list path), so one
  broken provider never hides the others' tools.
- `ExecuteTool` follows the miss contract above: skip on miss, abort on a real
  error, first non-nil result wins.

Nil arguments are dropped; if all are nil, `NewMultiProvider` returns nil.

For one-off providers you can also use `ProviderFuncs` to avoid defining a type:

```go
p := &mcp.ProviderFuncs{
    GetToolsFunc: func(ctx context.Context) ([]mcp.MCPTool, error) { return tools, nil },
    ExecuteToolFunc: func(ctx context.Context, name string, params map[string]any) (*mcp.ToolResponse, error) {
        return mcp.NewToolResponseAuto(run(ctx, name, params)), nil
    },
}
```

## Per-User Remote MCP Servers

To expose tools from remote MCP servers that differ per user (different
endpoints, credentials, or enabled tools), use `NewRemoteProvider`. Create it
**once** and reuse it; it resolves the current request's servers from the
context, so a single instance safely serves many users and its tool-list cache
persists across requests. See the [Remote Servers Guide](remote-servers.md#per-user-remote-servers-request-scoped)
for details.

## ExecuteTool Return Values

`ExecuteTool` returns a `*mcp.ToolResponse`. Build it with the response
constructors:

- `mcp.NewToolResponseText(s)` — a plain text result
- `mcp.NewToolResponseJSON(v)` — a JSON-encoded result
- `mcp.NewToolResponseTOON(v)` / image / audio / resource constructors

If you have a loose, dynamic value (for example the output of a script or a
remote call) and don't know its concrete type, wrap it with
`mcp.NewToolResponseAuto(v)`, which returns a `*mcp.ToolResponse` as-is, wraps a
`string` as text, and JSON-encodes anything else.

## Security

- **Providers are per-request**: created fresh with the authenticated user/tenant each time
- **Visibility ≠ access control**: hiding a tool doesn't prevent execution — always validate permissions inside `ExecuteTool`
- **No shared state**: the server stores no user/tenant data; all isolation is in the provider

## See Also

- [Tool Discovery Guide](tool-discovery.md) — `tool_search` and context window optimisation
- [Remote Servers Guide](remote-servers.md) — client-side tool filtering
