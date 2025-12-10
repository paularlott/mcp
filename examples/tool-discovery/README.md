# Tool Discovery Example

This example demonstrates how to use searchable tools and dynamic tool providers to reduce context window usage while maintaining access to a large tool library.

## Overview

When you have many tools, sending all their definitions to an LLM consumes significant tokens from the context window. This example shows how to:

1. **Searchable Tools**: Register tools that are hidden from `ListTools()` but can be discovered via search and called directly
2. **Request-Scoped Providers**: Add per-user or per-tenant tools via context

## Running the Example

```bash
cd examples/deferred-tools
go run main.go
```

The server will start on `http://localhost:8088/mcp`.

## Testing

### List Visible Tools

```bash
curl -X POST http://localhost:8088/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

You'll see only a few tools: `help`, `tool_search`, and `execute_tool`.

### Search for Tools

```bash
curl -X POST http://localhost:8088/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc":"2.0",
    "id":2,
    "method":"tools/call",
    "params":{
      "name":"tool_search",
      "arguments":{"query":"email"}
    }
  }'
```

This will find the hidden `send_email` and `list_emails` tools, including their full schemas.

### Execute a Hidden Tool

Since MCP clients validate tool names against `tools/list`, you must use `execute_tool` to call hidden tools:

```bash
curl -X POST http://localhost:8088/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc":"2.0",
    "id":4,
    "method":"tools/call",
    "params":{
      "name":"execute_tool",
      "arguments":{
        "name":"send_email",
        "arguments":{
          "to":"user@example.com",
          "subject":"Test",
          "body":"Hello!"
        }
      }
    }
  }'
```

### Search for Script Tools (Global Provider)

```bash
curl -X POST http://localhost:8088/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc":"2.0",
    "id":5,
    "method":"tools/call",
    "params":{
      "name":"tool_search",
      "arguments":{"query":"backup"}
    }
  }'
```

### Access User-Specific Tools (Request-Scoped Provider)

```bash
# Add X-User-ID header to get user-specific tools
curl -X POST http://localhost:8088/mcp \
  -H "Content-Type: application/json" \
  -H "X-User-ID: user123" \
  -d '{
    "jsonrpc":"2.0",
    "id":6,
    "method":"tools/call",
    "params":{
      "name":"tool_search",
      "arguments":{"query":"saved"}
    }
  }'
```

This will find `my_saved_queries` which is only available for authenticated users.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                      MCP Server                              │
├─────────────────────────────────────────────────────────────┤
│  Visible Tools (ListTools)                                   │
│  ├── help                                                    │
│  ├── tool_search                                             │
│  └── execute_tool                                            │
├─────────────────────────────────────────────────────────────┤
│  Searchable Tools (hidden from list, discoverable via search)│
│  ├── sql_query, database_backup                              │
│  ├── send_email, list_emails                                 │
│  ├── create_pdf, convert_document                            │
│  ├── kubernetes_deploy, docker_build                         │
│  ├── analyze_data, create_chart                              │
│  ├── run_backup_script, cleanup_logs, health_check           │
│  └── ... (extensible)                                        │
├─────────────────────────────────────────────────────────────┤
│  Request-Scoped ToolRegistry (per-user tools)                │
│  ├── my_saved_queries  (per-user)                            │
│  └── my_preferences    (per-user)                            │
└─────────────────────────────────────────────────────────────┘
```

## Benefits

1. **Reduced Token Usage**: Only 3 tools sent to LLM initially instead of 15+
2. **On-Demand Discovery**: LLM finds relevant tools when needed
3. **Multi-Tenant Support**: Different users can have different tools via request-scoped registries
4. **Extensibility**: Add tools from scripts, databases, or APIs using ToolProvider interface
5. **Client Compatibility**: `execute_tool` wrapper works with clients that validate tool names
