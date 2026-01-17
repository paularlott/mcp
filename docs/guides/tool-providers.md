# Tool Providers Guide

For multi-tenant or multi-user applications where tools need to be dynamically loaded per-request, use `ToolProvider` to inject tools into the request context. This allows a single MCP server to serve different tools to different users/tenants while maintaining complete isolation.

The `ToolProvider` interface is the unified way to add dynamic tools from external sources. It works with both normal mode and force ondemand mode.

**Important:** Tools registered via `RegisterTool()` or `RegisterOnDemandTool()` are **global** and visible to all requests. For per-tenant/user tool visibility, you **must** use the `ToolProvider` pattern as shown in `examples/per-user-tools/`.

## When to Use Tool Providers

- **Multi-tenant applications**: Each tenant has different available tools
- **Per-user tool access**: Users have different tool permissions
- **Dynamic tool loading**: Tools are loaded from external sources (databases, scripts, APIs)
- **Tool isolation**: Prevent cross-user/tenant tool leakage

## Quick Example

```go
// Implement ToolProvider interface
type UserToolProvider struct {
    userID string
    roles  []string
}

func (p *UserToolProvider) GetTools(ctx context.Context) ([]mcp.MCPTool, error) {
    var tools []mcp.MCPTool

    // All users get basic tools
    tools = append(tools, mcp.MCPTool{
        Name: "get_profile",
        Description: "Get user profile",
        InputSchema: schema,
    })

    // Admin users get additional tools
    if p.hasRole("admin") {
        tools = append(tools, mcp.MCPTool{
            Name: "admin_panel",
            Description: "Access admin panel",
            InputSchema: adminSchema,
        })
    }

    return tools, nil
}

func (p *UserToolProvider) ExecuteTool(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
    switch name {
    case "get_profile":
        return p.getUserProfile(), nil
    case "admin_panel":
        if !p.hasRole("admin") {
            return nil, fmt.Errorf("access denied")
        }
        return p.getAdminData(), nil
    }
    return nil, mcp.ErrUnknownTool
}

// In your HTTP handler:
func handler(w http.ResponseWriter, r *http.Request) {
    // Authenticate and get user context
    user := authenticateUser(r)
    if user == nil {
        http.Error(w, "Unauthorized", 401)
        return
    }

    // Create provider with user's tools
    provider := NewUserToolProvider(user.ID, user.Roles)

    // Normal mode: all tools visible in tools/list
    ctx := mcp.WithToolProviders(r.Context(), provider)
    server.HandleRequest(w, r.WithContext(ctx))

    // OR force ondemand mode: only tool_search/execute_tool visible
    // All tools (native, provider, remote) are searchable and callable
    ctx = mcp.WithForceOnDemandMode(r.Context(), provider)
    server.HandleRequest(w, r.WithContext(ctx))
}
```

## Tool Visibility Modes

### Normal Mode (`WithToolProviders`)

- Native tools appear in `tools/list`
- Provider tools appear in `tools/list`
- OnDemand tools are hidden but searchable via `tool_search`
- All tools are directly callable

### Force OnDemand Mode (`WithForceOnDemandMode`)

- Only `tool_search` and `execute_tool` appear in `tools/list`
- ALL tools (native, ondemand, provider, remote) are searchable via `tool_search`
- ALL tools remain callable (either directly or via `execute_tool`)
- Useful for AI clients that work better with minimal initial context

## Keywords for Discovery

When using tool providers, you can add keywords to your tools for better search results with `tool_search`:

```go
func (p *UserToolProvider) GetTools(ctx context.Context) ([]mcp.MCPTool, error) {
    return []mcp.MCPTool{
        {
            Name:        "send_email",
            Description: "Send an email",
            Keywords:    []string{"email", "communication", "smtp"},
            InputSchema: schema,
        },
    }, nil
}
```

The `Keywords` field is used by `tool_search` but is not exposed in the MCP protocol response.

## Security

Tool providers are designed for secure multi-tenant scenarios:

1. **Providers are per-request**: Each request gets fresh providers created with the authenticated user/tenant
2. **Providers validate context**: Providers should verify the request context matches their intended user/tenant to prevent cross-user tool leakage
3. **No shared state**: The MCP server stores no user/tenant data - all isolation is handled by providers
4. **Order matters**: Providers are tried in order for tool calls - first match wins

**Example validation pattern:**

```go
func (p *UserToolProvider) GetTools(ctx context.Context) ([]mcp.MCPTool, error) {
    // Validate that the request context matches this provider's user
    session := ctx.Value("session").(Session)
    if session.UserID != p.userID {
        // Context mismatch - return no tools to prevent leakage
        return nil, nil
    }
    return p.tools, nil
}
```

## API Reference

```go
// Interface for dynamic tool sources
type ToolProvider interface {
    GetTools(ctx context.Context) ([]MCPTool, error)
    ExecuteTool(ctx context.Context, name string, params map[string]interface{}) (interface{}, error)
}

// Add native providers to request context (tools appear in tools/list)
ctx := mcp.WithToolProviders(ctx, provider1, provider2)

// Add ondemand providers to request context (tools searchable but hidden from list)
ctx := mcp.WithOnDemandToolProviders(ctx, ondemandProvider)

// Get providers from context (internal use)
providers := mcp.GetToolProviders(ctx)
ondemandProviders := mcp.GetOnDemandToolProviders(ctx)
```

### OnDemand Tool Providers (`WithOnDemandToolProviders`)

For tools that should be searchable via `tool_search` but NOT appear in `tools/list`, use `WithOnDemandToolProviders`:

```go
func handler(w http.ResponseWriter, r *http.Request) {
    // Native provider - tools appear in tools/list
    nativeProvider := NewNativeToolProvider(user)

    // OnDemand provider - tools searchable but hidden from list
    ondemandProvider := NewOnDemandToolProvider(user)

    ctx := mcp.WithToolProviders(r.Context(), nativeProvider)
    ctx = mcp.WithOnDemandToolProviders(ctx, ondemandProvider)

    // When ondemand providers are present, tool_search and execute_tool
    // are automatically available
    server.HandleRequest(w, r.WithContext(ctx))
}
```

**Key behaviors:**

- OnDemand provider tools do NOT appear in `tools/list`
- OnDemand provider tools ARE searchable via `tool_search`
- OnDemand provider tools ARE callable directly or via `execute_tool`
- When any ondemand providers are present, `tool_search` and `execute_tool` are automatically registered

## ExecuteTool Return Values

The `ExecuteTool` method can return various types that will be automatically converted to an MCP response:

- `*mcp.ToolResponse` - Used directly as the response
- `string` - Converted to a text response
- `map[string]interface{}` - Serialized as JSON text
- `[]interface{}` - Serialized as JSON text
- Any other type - Serialized as JSON text

## See Also

- [Dynamic Tool States Guide](dynamic-tool-states.md) - Dynamic visibility control per request
- [Tool Discovery Guide](tool-discovery.md) - Searchable tools and discovery patterns
- Examples: [per-user-tools](../examples/per-user-tools/) for multi-tenant pattern
