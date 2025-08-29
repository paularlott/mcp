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
        mcp.NewTool("greet", "Greet someone",
            mcp.String("name", "Name to greet", mcp.Required()),
            mcp.String("greeting", "Custom greeting"),
        ),
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

The library provides a clean, declarative API for defining tools:

```go
// Basic types
mcp.String(name, description, options...)
mcp.Number(name, description, options...)
mcp.Boolean(name, description, options...)

// Array types
mcp.StringArray(name, description, options...)
mcp.NumberArray(name, description, options...)

// Object types
mcp.Object(name, description, properties...)
mcp.ObjectArray(name, description, properties...)

// Options
mcp.Required()  // Makes parameter required
mcp.Output(...) // Defines structured output
```

### Object Support

The library provides comprehensive support for objects and arrays of objects with a clean, declarative syntax:

#### Basic Object Parameters

Define structured object parameters with typed properties:

```go
server.RegisterTool(
    mcp.NewTool("create_user", "Create a new user",
        mcp.Object("user", "User information",
            mcp.String("name", "User's full name", mcp.Required()),
            mcp.String("email", "User's email address", mcp.Required()),
            mcp.Number("age", "User's age"),
            mcp.Boolean("active", "Whether user is active"),
            mcp.StringArray("tags", "User tags"),
            mcp.Required(),
        ),
        mcp.Output(
            mcp.Object("result", "Creation result",
                mcp.String("id", "User ID"),
                mcp.StringArray("permissions", "User permissions"),
            ),
        ),
    ),
    handleCreateUser,
)
```

Extract object parameters in your handler:

```go
func handleCreateUser(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
    // Get the entire object
    user, err := req.Object("user")
    if err != nil {
        return nil, err
    }

    // Extract specific properties with type safety
    name, err := req.GetObjectStringProperty("user", "name")
    if err != nil {
        return nil, err
    }

    email, err := req.GetObjectStringProperty("user", "email")
    if err != nil {
        return nil, err
    }

    // Optional properties with manual checking
    age := 0
    if ageVal, exists := user["age"]; exists {
        if ageFloat, ok := ageVal.(float64); ok {
            age = int(ageFloat)
        }
    }

    return mcp.NewToolResponseText(fmt.Sprintf("Created user: %s (%s)", name, email)), nil
}
```

#### Array of Objects

Define and handle arrays of objects:

```go
server.RegisterTool(
    mcp.NewTool("process_orders", "Process multiple orders",
        mcp.ObjectArray("orders", "List of orders to process",
            mcp.String("id", "Order ID", mcp.Required()),
            mcp.Number("amount", "Order amount", mcp.Required()),
            mcp.String("currency", "Currency code"),
            mcp.Required(),
        ),
        mcp.Output(
            mcp.StringArray("processed_ids", "List of processed order IDs"),
            mcp.NumberArray("totals", "List of order totals"),
        ),
    ),
    handleProcessOrders,
)

func handleProcessOrders(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
    orders, err := req.ObjectSlice("orders")
    if err != nil {
        return nil, err
    }

    for i, order := range orders {
        id, ok := order["id"].(string)
        if !ok {
            return nil, fmt.Errorf("order %d missing or invalid id", i)
        }

        amount, ok := order["amount"].(float64)
        if !ok {
            return nil, fmt.Errorf("order %d missing or invalid amount", i)
        }

        // Process order...
    }

    return mcp.NewToolResponseText("Orders processed"), nil
}
```

#### Nested Objects

Objects can contain other objects and arrays:

```go
server.RegisterTool(
    mcp.NewTool("create_order", "Create an order with customer info",
        mcp.Object("order", "Order information",
            mcp.String("id", "Order ID", mcp.Required()),
            mcp.Number("total", "Order total", mcp.Required()),
            mcp.Object("customer", "Customer information",
                mcp.String("name", "Customer name", mcp.Required()),
                mcp.String("email", "Customer email"),
                mcp.Required(),
            ),
            mcp.ObjectArray("items", "Order items",
                mcp.String("sku", "Item SKU", mcp.Required()),
                mcp.Number("quantity", "Item quantity", mcp.Required()),
            ),
            mcp.Required(),
        ),
    ),
    handleCreateOrder,
)
```

#### Generic Objects

For cases where you need to accept arbitrary object structures:

```go
server.RegisterTool(
    mcp.NewTool("configure", "Configure with arbitrary settings",
        mcp.Object("config", "Configuration object", mcp.Required()),
    ),
    handleConfigure,
)
```

Generic objects allow any properties and generate a schema with `"additionalProperties": true`.

#### API Reference

**Parameter Functions:**
- `String(name, description, options...)` - String parameter
- `Number(name, description, options...)` - Number parameter  
- `Boolean(name, description, options...)` - Boolean parameter
- `StringArray(name, description, options...)` - Array of strings
- `NumberArray(name, description, options...)` - Array of numbers
- `Object(name, description, properties...)` - Object with properties (use mcp.Required() to make required)
- `ObjectArray(name, description, properties...)` - Array of objects (use mcp.Required() to make required)

**Options:**
- `Required()` - Makes any parameter required
- `Output(parameters...)` - Defines structured output schema

**ToolRequest Methods:**
- `Object(name)` - Extract an object parameter as `map[string]interface{}`
- `ObjectOr(name, default)` - Extract an object parameter with default
- `ObjectSlice(name)` - Extract an array of objects as `[]map[string]interface{}`
- `ObjectSliceOr(name, default)` - Extract an array of objects with default
- `GetObjectProperty(objectName, propertyName)` - Get a property from an object
- `GetObjectStringProperty(objectName, propertyName)` - Get a string property with type safety
- `GetObjectIntProperty(objectName, propertyName)` - Get an int property with type safety
- `GetObjectBoolProperty(objectName, propertyName)` - Get a bool property with type safety

#### Generated JSON Schema

The library generates proper JSON Schema for object parameters:

```json
{
  "type": "object",
  "properties": {
    "user": {
      "type": "object",
      "description": "User information",
      "properties": {
        "name": {
          "type": "string",
          "description": "User's full name"
        },
        "email": {
          "type": "string",
          "description": "User's email address"
        },
        "age": {
          "type": "number",
          "description": "User's age"
        }
      },
      "required": ["name", "email"],
      "additionalProperties": false
    }
  },
  "required": ["user"],
  "additionalProperties": false
}
```

### Complete Example

```go
tool := mcp.NewTool("example", "Comprehensive example tool",
    // Input parameters
    mcp.String("name", "User name", mcp.Required()),
    mcp.Number("age", "User age"),
    mcp.StringArray("tags", "User tags"),
    mcp.Object("address", "User address",
        mcp.String("street", "Street address", mcp.Required()),
        mcp.String("city", "City", mcp.Required()),
    ),
    
    // Structured output
    mcp.Output(
        mcp.String("user_id", "Created user ID"),
        mcp.StringArray("permissions", "User permissions"),
        mcp.Object("profile", "User profile",
            mcp.String("display_name", "Display name"),
            mcp.Boolean("verified", "Is verified"),
        ),
    ),
)```

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

    // Array parameters
    tags, err := req.StringSlice("tags")
    numbers, err := req.IntSlice("numbers")

    // Object parameters
    user, err := req.Object("user")
    orders, err := req.ObjectSlice("orders")

    // Extract object properties with type safety
    userName, err := req.GetObjectStringProperty("user", "name")
    userAge, err := req.GetObjectIntProperty("user", "age")

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
- `examples/object-example/` - Comprehensive object and array handling examples

## License

MIT License