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
result, err := client.CallTool(ctx, "tool-name", args)
```

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
