# MCP Server Library for Go

A Go library for building [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) servers with a clean, fluent API.

## Features

- **Simple API**: Fluent interface for defining tools and parameters
- **Type Safety**: Strongly typed parameter access with automatic conversion
- **Rich Responses**: Support for text, image, audio, resource, and structured content
- **Thread Safe**: Concurrent request handling with mutex protection
- **Protocol Compliant**: Supports MCP versions 2024-11-05 through 2025-06-18

## Installation

```bash
go get github.com/paularlott/mcp
```

## Quick Start

```go
package main

import (
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
            AddParam("greeting", mcp.String, "Custom greeting", false).
            AddOutputParam("message", mcp.String, "The greeting message"),
        func(req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
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
    AddOutputParam("result", mcp.String, "The result")
```

## Request Handling

### Type-Safe Parameter Access

```go
func handler(req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
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
return mcp.NewToolResponseMulti(
    mcp.ToolContent{Type: "text", Text: "Results:"},
    mcp.ToolContent{Type: "image", Data: base64Data, MimeType: "image/png"},
)
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

## License

MIT License