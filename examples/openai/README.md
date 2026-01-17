# OpenAI Compatibility Example

This example demonstrates how to use the MCP `openai` package to create an OpenAI-compatible API server that exposes MCP tools. It forwards LLM requests to [LM Studio](https://lmstudio.ai/) running locally.

## Prerequisites

1. **LM Studio** running on `127.0.0.1:1234`
2. **Model**: `qwen/qwen3-1.7b` loaded in LM Studio (or configure a different model)

## What It Does

- Creates an MCP server with a simple `greet` tool
- Exposes an OpenAI-compatible `/v1/chat/completions` endpoint
- Automatically converts MCP tools to OpenAI function format
- Forwards requests to LM Studio for inference
- Executes tool calls and returns results to the LLM

## Running the Example

### With LM Studio (Default)

1. Start LM Studio and load the `qwen/qwen3-1.7b` model
2. Enable the local server (should be on `127.0.0.1:1234`)
3. Run the example:

```bash
go run main.go
```

### With a Different LLM Provider

You can override the defaults with environment variables:

```bash
# Use OpenAI
export OPENAI_BASE_URL=https://api.openai.com/v1
export OPENAI_API_KEY=your-api-key
export DEFAULT_MODEL=gpt-4o-mini
go run main.go

# Use Anthropic (via compatible endpoint)
export OPENAI_BASE_URL=https://api.anthropic.com/v1
export OPENAI_API_KEY=your-anthropic-key
export DEFAULT_MODEL=claude-3-haiku-20240307
go run main.go
```

## Testing

### List Models

```bash
curl http://localhost:8080/v1/models
```

### Chat Completion with Tool

```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen/qwen3-1.7b",
    "messages": [
      {"role": "user", "content": "Please greet Paul"}
    ]
  }'
```

### Expected Behavior

The LLM will:
1. Recognize the request to greet someone
2. Call the `greet` MCP tool with `{"name": "Paul"}`
3. Receive the response "Hi Paul! Greetings from MCP!"
4. Return a final response to the user

## Code Walkthrough

### 1. Create the MCP Server

```go
mcpServer := mcp.NewServer("greeting-server", "1.0.0")

mcpServer.RegisterTool(
    mcp.NewTool("greet", "Greet someone with a friendly message from MCP",
        mcp.String("name", "The name of the person to greet", mcp.Required()),
    ),
    func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
        name, _ := req.String("name")
        return mcp.NewToolResponseText(fmt.Sprintf("Hi %s! Greetings from MCP! 👋", name)), nil
    },
)
```

### 2. Convert MCP Tools to OpenAI Format

```go
mcpTools := mcpServer.ListTools()
req.Tools = append(req.Tools, openai.MCPToolsToOpenAI(mcpTools)...)
```

### 3. Execute Tool Calls

```go
for _, toolCall := range response.Choices[0].Message.ToolCalls {
    // Call the MCP tool
    mcpResponse, err := mcpServer.CallTool(ctx, toolCall.Function.Name, toolCall.Function.Arguments)

    // Extract result for OpenAI
    result, _ := openai.ExtractToolResult(mcpResponse)

    // Add tool result message
    toolResultMsg := openai.Message{
        Role:       "tool",
        ToolCallID: toolCall.ID,
    }
    toolResultMsg.SetContentAsString(result)
    messages = append(messages, toolResultMsg)
}
```

## Architecture

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│                 │     │                 │     │                 │
│  Client/User    │────▶│  This Server    │────▶│  Upstream LLM   │
│                 │     │                 │     │  (OpenAI, etc)  │
└─────────────────┘     └────────┬────────┘     └─────────────────┘
                                 │
                                 │ Tool Calls
                                 ▼
                        ┌─────────────────┐
                        │                 │
                        │   MCP Server    │
                        │   (greet tool)  │
                        │                 │
                        └─────────────────┘
```

The server acts as a proxy that:
1. Receives OpenAI-format requests
2. Adds MCP tools as OpenAI functions
3. Forwards to upstream LLM
4. Intercepts tool calls and executes them via MCP
5. Returns tool results to LLM
6. Repeats until LLM gives final response
