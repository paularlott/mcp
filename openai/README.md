# OpenAI Compatibility Package

This package provides types and utilities for building OpenAI-compatible APIs that use MCP tools. It bridges the gap between MCP's tool format and OpenAI's function calling format.

## Installation

```go
import "github.com/paularlott/mcp/openai"
```

## Features

- **OpenAI Types**: Complete request/response types for chat completions
- **Tool Conversion**: Convert MCP tools to OpenAI function format
- **Response Extraction**: Extract string results from MCP tool responses
- **Streaming Support**: Iterator and accumulator for streaming responses
- **Tool Handlers**: Event notifications during tool execution

## Quick Start

### Converting MCP Tools to OpenAI Format

```go
import (
    "github.com/paularlott/mcp"
    "github.com/paularlott/mcp/openai"
)

// Create your MCP server
server := mcp.NewServer("my-server", "1.0.0")
server.RegisterTool(/* ... */)

// Convert MCP tools to OpenAI format
mcpTools := server.ListTools()
openAITools := openai.MCPToolsToOpenAI(mcpTools)

// Or filter tools by name
filteredTools := openai.MCPToolsToOpenAIFiltered(mcpTools, openai.ToolsByName("search", "calculate"))

// Or exclude certain tools
excludedTools := openai.MCPToolsToOpenAIFiltered(mcpTools, openai.ExcludeTools("dangerous_tool"))
```

### Extracting Tool Results

```go
// After executing an MCP tool
response, err := server.CallTool(ctx, toolName, args)
if err != nil {
    return err
}

// Extract the result as a string for OpenAI
result, err := openai.ExtractToolResult(response)
if err != nil {
    return err
}

// Create tool result message
toolResultMessage := openai.Message{
    Role:       "tool",
    ToolCallID: toolCall.ID,
}
toolResultMessage.SetContentAsString(result)
```

### Streaming with ChatStream

```go
// Assuming you have response and error channels from your streaming implementation
stream := openai.NewChatStream(ctx, responseChan, errorChan)

for stream.Next() {
    chunk := stream.Current()

    // Process each chunk
    for _, choice := range chunk.Choices {
        if choice.Delta.Content != "" {
            fmt.Print(choice.Delta.Content)
        }
    }
}

if err := stream.Err(); err != nil {
    log.Printf("Stream error: %v", err)
}
```

### Accumulating Streaming Responses

```go
acc := &openai.CompletionAccumulator{}

for stream.Next() {
    chunk := stream.Current()
    acc.AddChunk(chunk)
}

// Check what we got
if content, ok := acc.FinishedContent(); ok {
    fmt.Println("Content:", content)
}

if toolCalls, ok := acc.FinishedToolCalls(); ok {
    for _, tc := range toolCalls {
        fmt.Printf("Tool call: %s(%v)\n", tc.Function.Name, tc.Function.Arguments)
    }
}

if refusal, ok := acc.FinishedRefusal(); ok {
    fmt.Println("Refusal:", refusal)
}
```

### Using Tool Handlers

Tool handlers receive events during tool execution, useful for sending SSE events or logging:

```go
type MyToolHandler struct{}

func (h *MyToolHandler) OnToolCall(toolCall openai.ToolCall) error {
    log.Printf("Executing tool: %s", toolCall.Function.Name)
    return nil
}

func (h *MyToolHandler) OnToolResult(toolCallID, toolName, result string) error {
    log.Printf("Tool %s completed", toolName)
    return nil
}

// Attach handler to context
ctx = openai.WithToolHandler(ctx, &MyToolHandler{})

// Later, retrieve and use it
if handler := openai.ToolHandlerFromContext(ctx); handler != nil {
    handler.OnToolCall(toolCall)
    // ... execute tool ...
    handler.OnToolResult(toolCall.ID, toolCall.Function.Name, result)
}
```

## Types Reference

### Request/Response Types

| Type | Description |
|------|-------------|
| `ChatCompletionRequest` | OpenAI chat completion request |
| `ChatCompletionResponse` | OpenAI chat completion response |
| `Message` | Chat message with role, content, tool calls |
| `Choice` | Response choice with message or delta |
| `Delta` | Streaming delta content |
| `Usage` | Token usage statistics |

### Tool Types

| Type | Description |
|------|-------------|
| `Tool` | OpenAI tool definition |
| `ToolFunction` | Function schema for a tool |
| `ToolCall` | Tool call from assistant |
| `ToolCallFunction` | Function name and arguments |
| `DeltaToolCall` | Streaming tool call delta |
| `DeltaFunction` | Streaming function delta |

### Message Content Helpers

```go
// Get content as string (handles both string and array formats)
content := message.GetContentAsString()

// Set content as string
message.SetContentAsString("Hello, world!")
```

### Tool Call JSON Handling

The `ToolCallFunction` type includes custom JSON marshaling/unmarshaling to handle OpenAI's format where arguments are a JSON string rather than an object:

```go
// When marshaling to JSON, arguments become a string:
// {"name": "search", "arguments": "{\"query\": \"hello\"}"}

// When unmarshaling, the string is parsed back to map[string]any
```

## Tool Filtering

Several filter helpers are provided:

```go
// Include all tools
openai.AllTools()

// Include only specific tools
openai.ToolsByName("tool1", "tool2")

// Exclude specific tools
openai.ExcludeTools("admin_tool", "debug_tool")

// Custom filter
customFilter := func(name string) bool {
    return strings.HasPrefix(name, "public_")
}
```

## Generating Tool Call IDs

When streaming, some LLMs don't provide tool call IDs. Generate one:

```go
id := openai.GenerateToolCallID(index)
// Returns something like "call_a1b2c3d4e5f6..."
```

## Complete Example

Here's a complete example of an OpenAI-compatible endpoint using MCP tools:

```go
func handleChatCompletion(w http.ResponseWriter, r *http.Request) {
    var req openai.ChatCompletionRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    // Add MCP tools to the request
    mcpTools := mcpServer.ListTools()
    req.Tools = append(req.Tools, openai.MCPToolsToOpenAI(mcpTools)...)

    // Forward to upstream LLM...
    response := callUpstreamLLM(req)

    // Process tool calls if any
    for _, choice := range response.Choices {
        for _, toolCall := range choice.Message.ToolCalls {
            // Execute MCP tool
            mcpResponse, err := mcpServer.CallTool(r.Context(),
                toolCall.Function.Name,
                toolCall.Function.Arguments)
            if err != nil {
                // Handle error
                continue
            }

            // Extract result
            result, _ := openai.ExtractToolResult(mcpResponse)

            // Add to messages for next iteration...
        }
    }

    json.NewEncoder(w).Encode(response)
}
```

## License

This package is part of the MCP library and is licensed under the same terms.
