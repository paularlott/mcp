# Discovery Package

The `discovery` package provides tool discovery for MCP servers. It allows you to have a large tool library without sending all tool definitions to the LLM, reducing context window usage.

## Overview

When you have many tools, sending all their definitions to an LLM consumes significant tokens. This package provides:

1. **Searchable Tools**: Register tools that are hidden from `tools/list` but can be discovered via search
2. **Dynamic Tool Providers**: Load tools from external sources (scripts, databases, APIs)
3. **Request-Scoped Providers**: Per-user or per-tenant tools via context
4. **Fuzzy Search**: Find tools by name or description using fuzzy matching
5. **execute_tool Wrapper**: Call hidden tools through a visible tool (required for MCP client compatibility)

## Installation

```go
import "github.com/paularlott/mcp/discovery"
```

## Quick Start

```go
package main

import (
    "context"
    "log"
    "net/http"

    "github.com/paularlott/mcp"
    "github.com/paularlott/mcp/discovery"
)

func main() {
    // Create MCP server
    server := mcp.NewServer("my-server", "1.0.0")

    // Create tool registry
    registry := discovery.NewToolRegistry()

    // Register searchable tools (hidden from tools/list)
    registry.RegisterTool(
        mcp.NewTool("send_email", "Send an email to a recipient",
            mcp.String("to", "Recipient email", mcp.Required()),
            mcp.String("subject", "Email subject", mcp.Required()),
            mcp.String("body", "Email body", mcp.Required()),
        ),
        func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
            to, _ := req.String("to")
            return mcp.NewToolResponseText("Email sent to " + to), nil
        },
        "email", "communication", "smtp", // keywords for search
    )

    // Attach registry to server (registers tool_search, execute_tool)
    registry.Attach(server)

    // Start server
    http.HandleFunc("/mcp", server.HandleRequest)
    log.Fatal(http.ListenAndServe(":8080", nil))
}
```

## Core Types

### ToolRegistry

The central registry for searchable tools and dynamic providers. `ToolRegistry` also implements the `ToolProvider` interface, so it can be used as a request-scoped provider:

```go
registry := discovery.NewToolRegistry()

// Use as request-scoped provider
ctx := discovery.WithRequestProviders(r.Context(), registry)
```

### ToolMetadata

Describes a tool for search and discovery:

```go
type ToolMetadata struct {
    Name        string   // Unique tool name
    Description string   // Human-readable description
    Keywords    []string // Searchable keywords
}
```

### ToolProvider

Interface for external tool sources:

```go
type ToolProvider interface {
    // ListToolMetadata returns metadata for all tools from this provider
    ListToolMetadata(ctx context.Context) ([]ToolMetadata, error)

    // GetTool returns the full tool definition by name (nil, nil if not found)
    GetTool(ctx context.Context, name string) (*mcp.MCPTool, error)

    // CallTool executes a tool (ErrToolNotFound if not found)
    CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.ToolResponse, error)
}
```

## Registering Tools

```go
registry.RegisterTool(
    tool,       // *mcp.ToolBuilder - tool definition with schema
    handler,    // func(context.Context, *mcp.ToolRequest) (*mcp.ToolResponse, error)
    keywords... // string variadic - searchable keywords
)
```

Example:

```go
registry.RegisterTool(
    mcp.NewTool("sql_query", "Execute SQL queries",
        mcp.String("query", "SQL query to execute", mcp.Required()),
        mcp.String("database", "Database name"),
    ),
    func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
        query, _ := req.String("query")
        return mcp.NewToolResponseText("Executed: " + query), nil
    },
    "database", "sql", "query", // keywords
)
```

## Dynamic Tool Providers

### Global Providers

Global providers are available to all requests:

```go
type ScriptToolProvider struct {
    scripts map[string]*Script
}

func (p *ScriptToolProvider) ListToolMetadata(ctx context.Context) ([]discovery.ToolMetadata, error) {
    var metadata []discovery.ToolMetadata
    for _, script := range p.scripts {
        metadata = append(metadata, discovery.ToolMetadata{
            Name:        script.Name,
            Description: script.Description,
            Keywords:    script.Tags,
        })
    }
    return metadata, nil
}

func (p *ScriptToolProvider) GetTool(ctx context.Context, name string) (*mcp.MCPTool, error) {
    script, ok := p.scripts[name]
    if !ok {
        return nil, nil // not found
    }
    return &mcp.MCPTool{
        Name:        script.Name,
        Description: script.Description,
        InputSchema: script.Schema,
    }, nil
}

func (p *ScriptToolProvider) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.ToolResponse, error) {
    script, ok := p.scripts[name]
    if !ok {
        return nil, discovery.ErrToolNotFound
    }
    // Execute script...
    return mcp.NewToolResponseText("Script executed"), nil
}

// Register globally
registry.AddProvider(scriptProvider)
```

### Request-Scoped Providers

For per-user or per-tenant tools, add providers to the request context:

```go
// Middleware that adds user-specific tools
handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    userID := r.Header.Get("X-User-ID")
    if userID != "" {
        userProvider := NewUserToolProvider(userID)
        ctx := discovery.WithRequestProviders(r.Context(), userProvider)
        r = r.WithContext(ctx)
    }
    server.HandleRequest(w, r)
})

http.Handle("/mcp", handler)
```

## Discovery Tools

When you call `registry.Attach(server)`, two tools are registered:

### tool_search

Search for available tools:

```json
{
    "name": "tool_search",
    "arguments": {
        "query": "email",
        "max_results": 10
    }
}
```

Returns matching tools with names, descriptions, relevance scores, and **full input schemas**:

```json
[
    {
        "name": "send_email",
        "description": "Send an email to a recipient",
        "score": 0.95,
        "input_schema": {
            "type": "object",
            "properties": {
                "to": {"type": "string", "description": "Recipient email"},
                "subject": {"type": "string", "description": "Email subject"},
                "body": {"type": "string", "description": "Email body"}
            },
            "required": ["to", "subject", "body"]
        }
    }
]
```

### execute_tool

Execute a hidden tool (required for MCP client compatibility):

```json
{
    "name": "execute_tool",
    "arguments": {
        "name": "send_email",
        "arguments": {
            "to": "user@example.com",
            "subject": "Hello",
            "body": "World"
        }
    }
}
```

## Why execute_tool?

MCP clients validate tool names against the `tools/list` response before allowing `tools/call`. Since hidden tools are not in `tools/list`, they cannot be called directly. The `execute_tool` wrapper is a visible tool that proxies calls to hidden tools.

## Search Algorithm

The search uses fuzzy matching with Levenshtein distance:

1. **Exact name match**: Score 1.0
2. **Name prefix**: Score 0.9
3. **Name contains**: Score 0.8
4. **Keyword exact match**: Score 0.85
5. **Keyword contains**: Score 0.7
6. **Description word match**: Score 0.6
7. **Description contains**: Score 0.5
8. **Fuzzy matches**: Scaled by similarity

Results are sorted by score and limited to the requested max results (default: 10).

## Thread Safety

The `ToolRegistry` is thread-safe. All operations (registering tools, adding providers, searching, calling) can be performed concurrently from multiple goroutines.

## Example

See [examples/tool-discovery](../examples/tool-discovery) for a complete working example with:

- Searchable tools for various domains (database, email, documents)
- Request-scoped user tool provider
- HTTP middleware for context injection
