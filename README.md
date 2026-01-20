# MCP Server Library for Go

A Go library for building [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) servers with a clean, fluent API.

## Features

- **Simple API**: Fluent interface for defining tools and parameters
- **Type Safety**: Strongly typed parameter access with automatic conversion
- **Rich Responses**: Support for text, image, audio, resource, and structured content
- **TOON Support**: Compact, human-readable JSON encoding for LLM prompts
- **Thread Safe**: Concurrent request handling with mutex protection
- **Remote Servers**: Connect to and proxy remote MCP servers with authentication
- **Searchable Tools**: Reduce context window usage with on-demand tool discovery
- **Dynamic Tool Providers**: Load tools from external sources (databases, scripts, APIs)
- **MCP Compliant**: Full support for protocol versions 2024-11-05 through 2025-11-25

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

## Documentation

For comprehensive guides, patterns, and API documentation, see the [docs/](docs/) directory:

### Get Started

- **[Tool Providers](docs/guides/tool-providers.md)** - Dynamic tool loading for multi-tenant applications
- **[Tool Discovery](docs/guides/tool-discovery.md)** - Searchable tools and context window optimization
- **[Dynamic Tool States](docs/guides/dynamic-tool-states.md)** - Switching visibility modes per request

### How-To Guides

- **[Remote Servers](docs/guides/remote-servers.md)** - Connecting and proxying remote MCP servers
- **[Sessions](docs/guides/sessions.md)** - Optional session management (MCP 2025-11-25)
- **[Response Types](docs/guides/response-types.md)** - Text, images, audio, and structured responses
- **[Error Handling](docs/guides/error-handling.md)** - Structured error patterns and best practices
- **[Thread Safety & Concurrency](docs/guides/concurrency.md)** - Safe concurrent usage patterns

### Quick References

- **[Dynamic Tool States Quick Ref](docs/quick-ref/dynamic-tool-states.md)** - Quick start for state transitions

## Tool Discovery Mode

The server supports two modes for tool visibility, selectable via HTTP header:

### Header-Based Mode Selection

Clients can request discovery mode during initialization:

```http
POST /mcp HTTP/1.1
Content-Type: application/json
X-MCP-Tool-Mode: discovery

{"jsonrpc":"2.0","id":1,"method":"initialize",...}
```

Or via query parameter: `/mcp?tool_mode=discovery`

**Normal Mode (default):** All native tools visible in `tools/list`
**Discovery Mode:** Only `tool_search` and `execute_tool` visible - all tools searchable

With session management enabled, the mode is stored in the session token. See [Tool Discovery Guide](docs/guides/tool-discovery.md) for details.

## Examples

See the [examples/](examples/) directory for complete, runnable examples:

- [Basic Server](examples/server/)
- [Multi-Tenant Tools](examples/per-user-tools/)
- [Tool Discovery](examples/tool-discovery/)
- [Remote Servers](examples/remote-server/)
- [Session Management](examples/session-server/)
- [OpenAI Integration](examples/openai/)

## Testing

Run tests to ensure everything works:

```bash
go test ./...
```

View specific test categories:

```bash
# Provider tests
go test -run TestAddRemoveProviders -v

# Dynamic tool state transitions
go test -run TestDynamicToolState -v

# Full test suite
go test -v
```
