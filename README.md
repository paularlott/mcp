# MCP Server Library for Go

A Go library for building [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) servers with a clean, fluent API.

## Features

- **Simple API**: Fluent interface for defining tools and parameters
- **Type Safety**: Strongly typed parameter access with automatic conversion
- **Rich Responses**: Support for text, image, audio, resource, and structured content
- **Thread Safe**: Concurrent request handling with mutex protection
- **Remote Servers**: Connect to and proxy remote MCP servers with authentication
- **Unified Interface**: Combine local and remote tools in a single server

## Installation

```bash
go get github.com/paularlott/mcp
```

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "log"
    "net/http"

    "github.com/paularlott/mcp"
)

func main() {
    // Create server
    server := mcp.NewServer("my-server", "1.0.0")

    // Register a tool
    server.RegisterTool(
        mcp.NewTool("greet", "Greet someone").
            AddParam("name", mcp.String, "Name to greet", true).
            AddParam("greeting", mcp.String, "Custom greeting", false),
        func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
            name, _ := req.String("name")
            greeting := req.StringOr("greeting", "Hello")
            return mcp.NewToolResponseText(fmt.Sprintf("%s, %s!", greeting, name)), nil
        },
    )

    // Start server
    http.HandleFunc("/mcp", server.HandleRequest)
    log.Fatal(http.ListenAndServe(":8000", nil))
}
```

## Tool Definition

### Parameter Types

```go
mcp.String   // "string"
mcp.Number   // "number"
mcp.Boolean  // "boolean"
mcp.Array    // "array"
mcp.Object   // "object"
```

### Adding Parameters

```go
tool := mcp.NewTool("example", "Example tool").
    AddParam("required_param", mcp.String, "A required parameter", true).
    AddParam("optional_param", mcp.Number, "An optional parameter", false).
    AddOutputParam("result", mcp.String, "The result", false)
```

## Request Handling

### Type-Safe Parameter Access

```go
func handler(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
    // Required parameters (returns error if missing/wrong type)
    name, err := req.String("name")
    count, err := req.Int("count")
    enabled, err := req.Bool("enabled")
    price, err := req.Float("price")

    // Optional parameters with defaults
    greeting := req.StringOr("greeting", "Hello")
    limit := req.IntOr("limit", 10)
    debug := req.BoolOr("debug", false)
    rate := req.FloatOr("rate", 1.0)

    return mcp.NewToolResponseText("Success"), nil
}
```

## Response Types

### Text Response
```go
return mcp.NewToolResponseText("Hello, world!")
```

### Image Response (auto base64 encoded)
```go
imageBytes, _ := os.ReadFile("image.png")
return mcp.NewToolResponseImage(imageBytes, "image/png")
```

### Audio Response (auto base64 encoded)
```go
audioBytes, _ := os.ReadFile("audio.wav")
return mcp.NewToolResponseAudio(audioBytes, "audio/wav")
```

### Resource Response
```go
return mcp.NewToolResponseResource("file://path", "content", "text/plain")
```

### Resource Link Response
```go
return mcp.NewToolResponseResourceLink("https://example.com", "View details")
```

### Structured Response
```go
data := map[string]interface{}{
    "status": "success",
    "count": 42,
}
return mcp.NewToolResponseStructured(data)
```

### Multi-Content Response
```go
response1 := mcp.NewToolResponseText("Results:")
response2 := mcp.NewToolResponseImage(imageBytes, "image/png")
return mcp.NewToolResponseMulti(response1, response2)
```

### Error Responses
```go
// Invalid parameter error
if name == "" {
    return nil, mcp.NewToolErrorInvalidParams("name parameter is required")
}

// Internal server error
if err := someOperation(); err != nil {
    return nil, mcp.NewToolErrorInternal("failed to process request")
}

// Custom error with specific code
return nil, mcp.NewToolError(-32000, "Custom server error", map[string]interface{}{
    "details": "Additional error information",
})
```

## Server Configuration

```go
server := mcp.NewServer("server-name", "1.0.0")

// Register multiple tools
server.RegisterTool(tool1, handler1)
server.RegisterTool(tool2, handler2)

// Handle MCP requests
http.HandleFunc("/mcp", server.HandleRequest)
```

## Protocol Support

The library supports MCP protocol versions:
- 2024-11-05 (minimum)
- 2025-03-26
- 2025-06-18 (latest)

## Thread Safety

The server is thread-safe and can handle concurrent requests. Tool registration and execution are protected by mutexes.

## Remote Servers and Clients

### MCP Client

Connect to remote MCP servers:

```go
// Bearer token authentication
auth := mcp.NewBearerTokenAuth("your-token")
client := mcp.NewClient("https://api.example.com/mcp", auth)

// OAuth2 authentication
oauth := mcp.NewOAuth2Auth("client-id", "client-secret", "https://auth.example.com/token", []string{"mcp:read"})
client := mcp.NewClient("https://api.example.com/mcp", oauth)

// Use client
tools, err := client.ListTools(ctx)
result, err := client.CallTool(ctx, "tool-name", args)
```

### Unified Server with Remote Tools

Register remote servers directly with your local server for a unified tool interface:

```go
// Create server
server := mcp.NewServer("my-server", "1.0.0")

// Register local tools (as usual)
server.RegisterTool(
    mcp.NewTool("local-greet", "Local greeting").
        AddParam("name", mcp.String, "Name to greet", true),
    func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
        name, _ := req.String("name")
        return mcp.NewToolResponseText(fmt.Sprintf("Hello, %s!", name)), nil
    },
)

// Register remote servers with namespaces
bearerAuth := mcp.NewBearerTokenAuth("ai-tools-token")
server.RegisterRemoteServer("https://ai.example.com/mcp", "ai", bearerAuth)

oauth2Auth := mcp.NewOAuth2Auth("client-id", "client-secret", "https://auth.example.com/token", []string{"mcp:read"})
server.RegisterRemoteServer("https://data.example.com/mcp", "data", oauth2Auth)

// ListTools returns all tools (local + remote with namespaces)
tools := server.ListTools() // Returns: ["local-greet", "ai/generate-text", "data/query", ...]

// CallTool with intelligent routing
result, err := server.CallTool(ctx, "local-greet", args)       // Calls local tool
result, err := server.CallTool(ctx, "ai/generate-text", args)  // Calls remote AI tool
result, err := server.CallTool(ctx, "unknown-tool", args)      // Returns ErrUnknownTool

// Serve unified interface as HTTP endpoint
http.HandleFunc("/mcp", server.HandleRequest)
```

### Tool Resolution

- **Namespaced calls** (`namespace/tool-name`): Route directly to the specified remote server
- **Non-namespaced calls**: Try local tools first, then fast lookup for remote tools
- **Caching**: Remote tool lists are cached and can be refreshed with `RefreshTools()`
- **Error handling**: Failed remote servers are skipped gracefully during registration

### Authentication

**Bearer Token:**
```go
auth := mcp.NewBearerTokenAuth("your-token")
```

**OAuth2 with Client Credentials:**
```go
auth := mcp.NewOAuth2Auth(
    "client-id",
    "client-secret",
    "https://auth.example.com/token",
    []string{"mcp:read", "mcp:execute"},
)
```

## Error Handling

The library provides structured error handling:

```go
// Tool-specific errors
if name == "" {
    return nil, mcp.NewToolErrorInvalidParams("name parameter is required")
}

// Internal errors
if err := someOperation(); err != nil {
    return nil, mcp.NewToolErrorInternal("operation failed")
}

// Custom errors
return nil, mcp.NewToolError(-32000, "Custom error", map[string]interface{}{
    "details": "Additional information",
})
```

## API Reference

### Server Methods

- `NewServer(name, version string) *Server` - Create a new server
- `RegisterTool(tool *ToolBuilder, handler ToolHandler)` - Register a local tool
- `RegisterRemoteServer(url, namespace string, auth AuthProvider) error` - Register a remote server
- `ListTools() []MCPTool` - Get all tools (local + remote)
- `CallTool(ctx context.Context, name string, args map[string]interface{}) (*ToolResponse, error)` - Execute a tool
- `RefreshTools() error` - Refresh remote tool cache
- `HandleRequest(w http.ResponseWriter, r *http.Request)` - HTTP handler for MCP requests

### Client Methods

- `NewClient(baseURL string, auth AuthProvider) *Client` - Create a new client
- `Initialize(ctx context.Context) error` - Initialize connection (called automatically)
- `ListTools(ctx context.Context) ([]MCPTool, error)` - List remote tools
- `CallTool(ctx context.Context, name string, args map[string]interface{}) (*ToolResponse, error)` - Call remote tool
- `RefreshToolCache(ctx context.Context) error` - Refresh tool cache

## Examples

See the `examples/` directory for complete working examples:

- `examples/server/` - Basic MCP server
- `examples/client/` - MCP client connecting to remote server
- `examples/unified-server/` - Server with both local and remote tools

## License

MIT License