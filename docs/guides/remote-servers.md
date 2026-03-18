# Remote Servers and MCP Clients Guide

## MCP Client

Connect to remote MCP servers:

```go
// Bearer token authentication
auth := mcp.NewBearerTokenAuth("your-token")
client := mcp.NewClient("https://api.example.com/mcp", auth, "namespace")

// OAuth2 authentication
oauth := mcp.NewOAuth2Auth("client-id", "client-secret", "https://auth.example.com/token", []string{"mcp:read"})
client := mcp.NewClient("https://api.example.com/mcp", oauth, "namespace")

// Use client directly
tools, err := client.ListTools(ctx)

// Call a tool with a plain map
result, err := client.CallTool(ctx, "tool-name", map[string]any{"key": "value"})

// Or with the Args fluent builder
result, err := client.CallTool(ctx, "tool-name", mcp.Args{}.Arg("key", "value").Arg("limit", 10))
```

## Args Builder

`Args` is a `map[string]any` type alias with a fluent `Arg` method. Both forms are interchangeable anywhere a `map[string]any` is accepted:

```go
// Plain map
client.CallTool(ctx, "search", map[string]any{"query": "go", "limit": 10})

// Fluent builder - same result
client.CallTool(ctx, "search", mcp.Args{}.Arg("query", "go").Arg("limit", 10))

// Also works in ToolCall for parallel calls
mcp.ToolCall{Name: "search", Arguments: mcp.Args{}.Arg("query", "go")}
```

## Tool Filtering

Filter which tools are exposed from a remote server. The filter is applied to both `ListTools()` and `CallTool()`:

```go
// Create client with namespace
client := mcp.NewClient("https://api.example.com/mcp", auth, "github")

// Set a filter - only allow specific tools
client.WithToolFilter(func(toolName string) bool {
    // toolName is the original name WITHOUT namespace prefix
    allowedTools := map[string]bool{
        "search_repos":  true,
        "list_issues":   true,
        "create_issue":  true,
    }
    return allowedTools[toolName]
})

// ListTools only returns filtered tools
tools, _ := client.ListTools(ctx)  // Only returns 3 tools

// CallTool rejects filtered-out tools
_, err := client.CallTool(ctx, "github/delete_repo", args)  // Returns ErrToolFiltered
```

### Filter Patterns

```go
// Exclude dangerous tools
client.WithToolFilter(func(name string) bool {
    return name != "delete" && name != "drop_database"
})

// Allow only read operations
client.WithToolFilter(func(name string) bool {
    return strings.HasPrefix(name, "get_") || strings.HasPrefix(name, "list_")
})

// Dynamic filter from database/config
client.WithToolFilter(func(name string) bool {
    return db.IsToolEnabled(serverID, name)
})

// Clear filter to re-enable all tools
client.WithToolFilter(nil)
_ = client.RefreshToolCache(ctx)  // Refresh to pick up new tools
```

### Filter Behavior

- Filter receives the **original tool name** (without namespace prefix)
- Setting a filter **clears the tool cache** automatically
- `ErrToolFiltered` is returned when calling a filtered-out tool
- `nil` filter means all tools are allowed (default)

## Unified Server with Remote Tools

Register remote servers directly with your local server for a unified tool interface:

```go
// Create server
server := mcp.NewServer("my-server", "1.0.0")

// Register local tools (as usual)
server.RegisterTool(
    mcp.NewTool("local-greet", "Local greeting",
        mcp.String("name", "Name to greet", mcp.Required()),
    ),
    func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
        name, _ := req.String("name")
        return mcp.NewToolResponseText(fmt.Sprintf("Hello, %s!", name)), nil
    },
)

// Create clients for remote servers
aiAuth := mcp.NewBearerTokenAuth("ai-tools-token")
aiClient := mcp.NewClient("https://ai.example.com/mcp", aiAuth, "ai")

dataAuth := mcp.NewOAuth2Auth("client-id", "client-secret", "https://auth.example.com/token", []string{"mcp:read"})
dataClient := mcp.NewClient("https://data.example.com/mcp", dataAuth, "data")

// Register remote servers - tools appear with namespace prefix
server.RegisterRemoteServer(aiClient)
server.RegisterRemoteServer(dataClient)

// ListTools returns all tools (local + remote with namespaces)
tools := server.ListTools() // Returns: ["local-greet", "ai/generate-text", "data/query", ...]

// CallTool with intelligent routing
result, err := server.CallTool(ctx, "local-greet", args)       // Calls local tool
result, err := server.CallTool(ctx, "ai/generate-text", args)  // Calls remote AI tool
result, err := server.CallTool(ctx, "unknown-tool", args)      // Returns ErrUnknownTool

// Serve unified interface as HTTP endpoint
http.HandleFunc("/mcp", server.HandleRequest)
```

## Parallel Tool Calls

Execute multiple tools concurrently and collect all results in one call. Results are returned in the same order as the input, and a failure in one call does not affect the others.

### CallToolsParallel

For tools that are directly callable (native tools):

```go
results := client.CallToolsParallel(ctx, []mcp.ToolCall{
    {Name: "weather",     Arguments: mcp.Args{}.Arg("city", "London")},
    {Name: "news",        Arguments: mcp.Args{}.Arg("topic", "tech")},
    {Name: "ai/summarize", Arguments: map[string]any{"text": doc}},
})

for _, r := range results {
    if r.Err != nil {
        log.Printf("%s failed: %v", r.Name, r.Err)
        continue
    }
    // use r.Response
}
```

### ExecuteDiscoveredToolsParallel

For tools found via `ToolSearch` (discoverable tools):

```go
results := client.ExecuteDiscoveredToolsParallel(ctx, []mcp.ToolCall{
    {Name: "send_email",   Arguments: mcp.Args{}.Arg("to", "<email>").Arg("subject", "Hello")},
    {Name: "send_sms",     Arguments: mcp.Args{}.Arg("to", "<phone>").Arg("body", "Hello")},
    {Name: "send_webhook", Arguments: map[string]any{"url": "https://example.com/hook"}},
})

for _, r := range results {
    if r.Err != nil {
        log.Printf("%s failed: %v", r.Name, r.Err)
        continue
    }
    // use r.Response
}
```

### ParallelToolResult Fields

| Field | Type | Description |
|---|---|---|
| `Name` | `string` | Tool name from the input `ToolCall` |
| `Response` | `*ToolResponse` | Response on success, `nil` on error |
| `Err` | `error` | Error on failure, `nil` on success |

### When to Use Parallel Calls

- Fetching data from multiple independent sources simultaneously
- Fan-out patterns where results are aggregated before responding
- Reducing total latency when tools have no dependencies on each other

## Tool Resolution

- **Namespaced calls** (`namespace/tool-name`): Route directly to the specified remote server
- **Non-namespaced calls**: Try local tools first, then fast lookup for remote tools
- **Caching**: Remote tool lists are cached and can be refreshed with `RefreshTools(ctx)`
- **Error handling**: Failed remote servers are skipped gracefully during registration

## Authentication

### Bearer Token

```go
auth := mcp.NewBearerTokenAuth("your-token")
```

### OAuth2 with Client Credentials

```go
auth := mcp.NewOAuth2Auth(
    "client-id",
    "client-secret",
    "https://auth.example.com/token",
    []string{"mcp:read", "mcp:execute"},
)
```

## Unified Interface Benefits

- Single endpoint for multiple tool libraries
- Transparent routing with namespaces
- Works with any authentication method
- Graceful degradation if remote server unavailable
- Full protocol support (all MCP versions)

## See Also

- [Tool Providers Guide](tool-providers.md) - Local dynamic tool loading
- Protocol support documentation for version compatibility
