# Tool Providers Guide

For multi-tenant or multi-user applications where tools need to be dynamically loaded per-request, use `ToolProvider` to inject tools into the request context. This allows a single MCP server to serve different tools to different users/tenants while maintaining complete isolation.

The `ToolProvider` interface is the unified way to add dynamic tools from external sources. Tools carry their own visibility (`Native` or `Discoverable`) and providers accumulate when chained.

**Important:** Tools registered via `RegisterTool()` are **global** and visible to all requests. For per-tenant/user tool visibility, you **must** use the `ToolProvider` pattern as shown in `examples/per-user-tools/`.

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

    // All users get basic tools (native = visible in tools/list)
    tools = append(tools, mcp.MCPTool{
        Name:        "get_profile",
        Description: "Get user profile",
        InputSchema: schema,
        Visibility:  mcp.ToolVisibilityNative,
    })

    // Admin users get additional tools
    if p.hasRole("admin") {
        tools = append(tools, mcp.MCPTool{
            Name:        "admin_panel",
            Description: "Access admin panel",
            InputSchema: adminSchema,
            Visibility:  mcp.ToolVisibilityNative,
        })
    }

    // Discoverable tools - searchable but not in tools/list
    tools = append(tools, mcp.MCPTool{
        Name:        "advanced_settings",
        Description: "Advanced configuration options",
        Keywords:    []string{"config", "settings", "advanced"},
        Visibility:  mcp.ToolVisibilityDiscoverable,
    })

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
    case "advanced_settings":
        return p.getAdvancedSettings(), nil
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

    // Normal mode: native tools visible, discoverable tools searchable
    ctx := mcp.WithToolProviders(r.Context(), provider)

    // Check for show-all mode (MCP chaining)
    if mcp.GetShowAllFromRequest(r) {
        ctx = mcp.WithShowAllTools(ctx)  // ALL tools visible
    }

    server.HandleRequest(w, r.WithContext(ctx))
}
```

## Tool Visibility

Each tool carries its own visibility setting:

### Native Tools (`ToolVisibilityNative`)

- Appear in `tools/list`
- Directly callable
- Ideal for core, frequently-used functionality

### Discoverable Tools (`ToolVisibilityDiscoverable`)

- Hidden from `tools/list`
- Searchable via `tool_search`
- Callable via `execute_tool`
- When any discoverable tools exist, `tool_search` and `execute_tool` are auto-registered
- Ideal for specialized, rarely-used, or context-reducing tools

### Show-All Mode (`WithShowAllTools`)

- ALL tools (native and discoverable) appear in `tools/list`
- Used for MCP server chaining when an upstream server needs full visibility
- Triggered by `X-MCP-Show-All: true` header or `?show_all=true` query param

## Keywords for Discovery

When creating tools with the fluent builder that need keywords, use `.Discoverable()` before converting to MCPTool:

```go
type UserToolProvider struct {
    userID string
}

func (p *UserToolProvider) GetTools(ctx context.Context) ([]mcp.MCPTool, error) {
    // Native tool - no .Discoverable() call
    profileTool := mcp.NewTool("get_profile", "Get user profile",
        mcp.String("user_id", "User ID"),
    ).ToMCPTool()

    // Discoverable tool - use .Discoverable() to add keywords
    emailTool := mcp.NewTool("send_email", "Send an email",
        mcp.String("to", "Recipient", mcp.Required()),
        mcp.String("subject", "Subject"),
    ).Discoverable("email", "communication", "smtp").ToMCPTool()

    return []mcp.MCPTool{profileTool, emailTool}, nil
}
```

Alternatively, construct MCPTool structs directly:

```go
func (p *UserToolProvider) GetTools(ctx context.Context) ([]mcp.MCPTool, error) {
    return []mcp.MCPTool{
        {
            Name:        "send_email",
            Description: "Send an email",
            Keywords:    []string{"email", "communication", "smtp"},
            Visibility:  mcp.ToolVisibilityDiscoverable,
            InputSchema: schema,
        },
    }, nil
}
```

The `Keywords` field is used by `tool_search` but is not exposed in the MCP protocol response.

### Using ToMCPTool() Helper

The `ToMCPTool()` method converts a ToolBuilder to an MCPTool struct for use in providers:

```go
func (p *MyProvider) GetTools(ctx context.Context) ([]mcp.MCPTool, error) {
    // Native tool (appears in tools/list)
    nativeTool := mcp.NewTool("basic_tool", "A basic tool",
        mcp.String("input", "Input parameter"),
    ).ToMCPTool()

    // Discoverable tool (searchable only)
    discoverableTool := mcp.NewTool("advanced_tool", "An advanced tool",
        mcp.String("query", "Search query"),
    ).Discoverable("advanced", "search", "special").ToMCPTool()

    return []mcp.MCPTool{nativeTool, discoverableTool}, nil
}
```

**Key points:**

- Call `.ToMCPTool()` without arguments (keywords are set via `.Discoverable()`)
- Use `.Discoverable(keywords...)` to mark as discoverable and add keywords
- Without `.Discoverable()`, tools are native (visible in tools/list)

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

// Add providers to request context (tools visibility per-tool)
ctx := mcp.WithToolProviders(ctx, provider1, provider2)

// Enable show-all mode (reveals ALL tools including discoverable)
ctx = mcp.WithShowAllTools(ctx)

// Check if show-all requested via HTTP header/query param
showAll := mcp.GetShowAllFromRequest(r)

// Get providers from context (internal use)
providers := mcp.GetToolProviders(ctx)
```

### Discoverable Tools

For tools that should be searchable via `tool_search` but NOT appear in `tools/list`, set `Visibility: mcp.ToolVisibilityDiscoverable`:

```go
func handler(w http.ResponseWriter, r *http.Request) {
    provider := &MyProvider{}

    ctx := mcp.WithToolProviders(r.Context(), provider)

    // When any discoverable tools are present, tool_search and execute_tool
    // are automatically available
    server.HandleRequest(w, r.WithContext(ctx))
}

type MyProvider struct{}

func (p *MyProvider) GetTools(ctx context.Context) ([]mcp.MCPTool, error) {
    return []mcp.MCPTool{
        {
            Name:       "visible_tool",
            Visibility: mcp.ToolVisibilityNative,  // Appears in tools/list
        },
        {
            Name:       "hidden_tool",
            Keywords:   []string{"search", "keywords"},
            Visibility: mcp.ToolVisibilityDiscoverable,  // Only via tool_search
        },
    }, nil
}
```

**Key behaviors:**

- Discoverable tools do NOT appear in `tools/list`
- Discoverable tools ARE searchable via `tool_search`
- Discoverable tools ARE callable via `execute_tool`
- When any discoverable tools exist, `tool_search` and `execute_tool` are automatically registered

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
