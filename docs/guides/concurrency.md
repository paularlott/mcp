# Thread Safety and Concurrency

The MCP library is designed for safe concurrent usage.

## Thread Safety Guarantees

The following are safe to call concurrently from multiple goroutines:

- `server.HandleRequest()` — designed for concurrent HTTP requests
- `server.ListTools()` — thread-safe read operations
- `server.CallTool()` — thread-safe tool execution
- `ToolProvider.GetTools()` — called per-request
- `ToolProvider.ExecuteTool()` — called per-request

## Server Initialization

Initialize the server once before handling requests:

```go
// ✅ Correct
server := mcp.NewServer("my-server", "1.0.0")
server.RegisterTool(tool1, handler1)
http.HandleFunc("/mcp", server.HandleRequest)

// ❌ Wrong: don't reinitialize inside the handler
http.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
    server := mcp.NewServer("my-server", "1.0.0")
    server.HandleRequest(w, r)
})
```

## Per-Request Isolation

Each request gets its own context. Create providers fresh per-request to ensure isolation:

```go
http.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
    user := authenticate(r)

    // Fresh provider per request — no shared mutable state
    provider := &UserProvider{
        userID: user.ID,
        db:     sharedDB, // shared read-only dependency is fine
    }

    ctx := mcp.WithToolProviders(r.Context(), provider)
    server.HandleRequest(w, r.WithContext(ctx))
})
```

## Parallel Client Tool Calls

The `Client` provides built-in parallel execution for both native and discovered tools. All calls fan out concurrently and results are returned in input order once all goroutines complete.

```go
// Native tools
results := client.CallToolsParallel(ctx, []mcp.ToolCall{
    {Name: "weather", Arguments: mcp.Args{}.Arg("city", "London")},
    {Name: "stocks",  Arguments: mcp.Args{}.Arg("symbol", "AAPL")},
    {Name: "news",    Arguments: map[string]any{"topic": "tech"}},
})

// Discovered tools
results := client.ExecuteDiscoveredToolsParallel(ctx, []mcp.ToolCall{
    {Name: "send_email",   Arguments: mcp.Args{}.Arg("to", "<email>")},
    {Name: "send_webhook", Arguments: mcp.Args{}.Arg("url", "https://example.com")},
})

for _, r := range results {
    if r.Err != nil {
        log.Printf("%s failed: %v", r.Name, r.Err)
        continue
    }
    // use r.Response
}
```

A failure in one call does not cancel or affect the others. The context is shared across all goroutines — cancel it to abort all in-flight calls simultaneously.

## Avoid Modifying Server State in Handlers

```go
// ❌ Wrong: race condition
func handleRequest(w http.ResponseWriter, r *http.Request) {
    server.RegisterTool(...) // don't do this
    server.HandleRequest(w, r)
}
```

## See Also

- [Tool Providers Guide](tool-providers.md) — safe per-request provider patterns
- [Remote Servers Guide](remote-servers.md) — parallel client tool calls
