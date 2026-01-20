# Tool Discovery and Searchable Tools Guide

When working with many tools, the context window can become bloated with tool definitions. The server provides built-in support for searchable tools - tools that can be discovered via keyword search and executed on-demand.

This approach is inspired by [Anthropic's Tool Search Tool](https://www.anthropic.com/engineering/advanced-tool-use) pattern, which can reduce token usage by up to 85% while maintaining access to your full tool library.

## Tool Registration

There are two ways to register tools with different visibility:

### Native Tools (`RegisterTool`) - Always visible in `tools/list`

```go
server.RegisterTool(
    mcp.NewTool("get_status", "Get system status"),
    handleGetStatus,
)
```

- **Visible in `tools/list`**: Yes, always
- **Directly callable**: Yes
- **Searchable via `tool_search`**: Only in force ondemand mode
- **Keywords parameter**: Stored but only used in force ondemand mode

Use for essential tools that should always be visible to the LLM.

### OnDemand Tools (`RegisterOnDemandTool`) - Hidden from `tools/list`, searchable only

```go
server.RegisterOnDemandTool(
    mcp.NewTool("send_email", "Send an email",
        mcp.String("to", "Recipient", mcp.Required()),
        mcp.String("subject", "Subject", mcp.Required()),
    ),
    handleSendEmail,
    "email", "notification", "smtp", // keywords for search relevance
)
```

- **Visible in `tools/list`**: No
- **Directly callable**: Yes (if you know the name)
- **Searchable via `tool_search`**: Yes, always
- **Keywords parameter**: Used to improve search relevance

Use for secondary/specialized tools to reduce context window usage.

When you register any ondemand tool, `tool_search` and `execute_tool` are automatically registered as native tools.

## Visibility Modes

The server supports two modes for controlling how tools are exposed to clients.

### Header-Based Mode Selection (Recommended)

Clients can request discovery mode via HTTP header or query parameter:

```
X-MCP-Tool-Mode: discovery
```

Or via query parameter (fallback):

```
/mcp?tool_mode=discovery
```

**With Session Management:**

- Mode is captured during `initialize` and stored in the session
- All subsequent requests in the session use the stored mode
- Mode cannot be changed mid-session (create a new session to change)

**Without Session Management:**

- Mode is checked on each request via header/query parameter

**Server-side Code:**

```go
// Enable session management (recommended for production)
sm, _ := mcp.NewJWTSessionManagerWithAutoKey(30 * time.Minute)
server.SetSessionManager(sm)

// The server automatically handles mode from headers/query params
http.HandleFunc("/mcp", server.HandleRequest)
```

### Programmatic Mode Selection (For Internal Endpoints)

For server-side consumers like web chat or OpenAI-compatible endpoints, use context-based mode:

### Normal Mode (`WithToolProviders`)

All native and provider tools appear in `tools/list`:

```go
http.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
    ctx := mcp.WithToolProviders(r.Context(), providers...)
    server.HandleRequest(w, r.WithContext(ctx))
})
```

**Behavior:**

- Native tools appear in `tools/list`
- Provider tools appear in `tools/list`
- OnDemand tools are hidden but searchable via `tool_search`
- Keywords on native tools are ignored (not searchable)

**When to use:**

- Most deployments with a moderate number of tools (< 20)
- When you want essential tools always visible to the LLM
- When using per-user providers with role-based access control

### Force OnDemand Mode (`WithForceOnDemandMode`)

Only `tool_search` and `execute_tool` appear in `tools/list`:

```go
http.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
    ctx := mcp.WithForceOnDemandMode(r.Context(), providers...)
    server.HandleRequest(w, r.WithContext(ctx))
})
```

**Behavior:**

- Only `tool_search` and `execute_tool` appear in `tools/list`
- ALL tools (native, ondemand, provider, remote) are searchable via `tool_search`
- Keywords on ALL tools (including native) are used for search
- All tools remain callable (directly or via `execute_tool`)

**When to use:**

- Large tool libraries (20+ tools) where context window is a concern
- AI clients that work better with minimal initial context
- When you want ALL tools (including native ones) to be discoverable via search
- Internal endpoints (web chat, OpenAI-compatible) that always need discovery mode

### The LLM Workflow with Tool Discovery

1. **Initialize with minimal context**: Only `tool_search` and `execute_tool` are visible
2. **Search for needed tools**: `tool_search(query="email")` → finds "send_email" with full schema
3. **Execute discovered tool**: `execute_tool(name="send_email", arguments={...})` → executes the tool
4. **Repeat as needed**: LLM can search for different tools as needed

## Example Implementation

See `examples/tool-discovery/` for a complete example demonstrating:

- Tool registration strategies
- Search filtering
- Mode switching
- Response formatting

## Context Window Optimization

**Typical scenario without tool discovery:**

- 20 tools × 50 lines per tool definition = 1000 tokens wasted for a simple query

**With tool discovery:**

- `tool_search` and `execute_tool` = ~20 tokens
- Only requested tool definitions are sent = ~50 tokens per tool needed
- Savings: Up to 85% for typical use cases

## See Also

- [Tool Providers Guide](tool-providers.md) - Dynamic tool loading
- [Dynamic Tool States Guide](dynamic-tool-states.md) - Context-based visibility control
