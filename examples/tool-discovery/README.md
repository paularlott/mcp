# Tool Discovery Example

This example demonstrates how to use searchable tools to reduce context window usage while maintaining access to a large tool library.

## Overview

When you have many tools, sending all their definitions to an LLM consumes significant tokens from the context window. This example shows how to:

1. **Register native tools** (`RegisterTool`) - Essential tools that always appear in `tools/list`
2. **Register ondemand tools** (`RegisterOnDemandTool`) - Specialized tools that are hidden but searchable via `tool_search`

## How It Works

- `RegisterTool(tool, handler)` - Tool is visible in `tools/list`, NOT searchable (except in force ondemand mode)
- `RegisterOnDemandTool(tool, handler, keywords...)` - Tool is hidden from `tools/list` but searchable via `tool_search`
- The server automatically provides `tool_search` and `execute_tool` when any ondemand tools are registered
- LLMs can discover ondemand tools by keyword and get full schemas before calling them

## Running the Example

```bash
cd examples/tool-discovery
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

This returns all tools plus `tool_search` and `execute_tool`.

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
      "arguments":{"query":"database", "limit": 5}
    }
  }'
```

### Execute a Discovered Tool

```bash
curl -X POST http://localhost:8088/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc":"2.0",
    "id":3,
    "method":"tools/call",
    "params":{
      "name":"execute_tool",
      "arguments":{
        "name":"sql_query",
        "arguments":{"query":"SELECT * FROM users", "database":"main"}
      }
    }
  }'
```

## Tool Categories

This example registers tools in several categories:

- **Database**: `sql_query`, `database_backup`
- **Email**: `send_email`, `list_emails`
- **Documents**: `create_pdf`, `convert_document`
- **DevOps**: `kubernetes_deploy`, `docker_build`
- **Analytics**: `analyze_data`, `create_chart`
- **Automation**: `run_backup_script`, `cleanup_logs`, `system_health_check`

## API

### RegisterTool - Native Tools

Essential tools that always appear in `tools/list`:

```go
server.RegisterTool(
    mcp.NewTool("help", "Get help and guidance"),
    handler,
)
```

- Always visible in `tools/list`
- Directly callable by name
- NOT searchable via `tool_search` (unless using force ondemand mode)
- Use for essential tools the LLM should always know about

### RegisterOnDemandTool - Searchable Tools

Specialized tools hidden from `tools/list` but discoverable via search:

```go
server.RegisterOnDemandTool(
    mcp.NewTool("send_email", "Send an email",
        mcp.String("to", "Recipient", mcp.Required()),
        mcp.String("subject", "Subject", mcp.Required()),
    ),
    handler,
    "email", "notification", "smtp", "message", // keywords for search relevance
)
```

- Hidden from `tools/list`
- Discoverable via `tool_search`
- Callable via `execute_tool` or directly if you know the name
- Keywords improve search relevance (matches tool name, description, and keywords)
- Use for specialized tools to reduce context window usage
