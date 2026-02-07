# OpenAI Compatibility Package

This package provides types and utilities for building OpenAI-compatible APIs that use MCP tools. It bridges the gap between MCP's tool format and OpenAI's function calling format, and includes a client for seamless integration with MCP servers.

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
- **Client Integration**: Ready-to-use OpenAI client with automatic MCP server tool execution
- **Custom Tools**: Support for tools sent to AI but executed manually by your code

## Client Overview

The `Client` struct provides a high-level interface for OpenAI API interactions with built-in MCP server support. It automatically executes tools from attached MCP servers while allowing custom tools to be handled manually.

### Key Components

- **Local Server**: A single MCP server without namespace (optional)
- **Remote Servers**: Multiple MCP servers with namespaces for routing tool calls
- **Custom Tools**: Tools sent to AI but not executed by the client
- **Automatic Tool Execution**: Tools from MCP servers are executed automatically during chat completions

### Configuration

```go
client, err := openai.New(openai.Config{
    APIKey: "sk-...",
    BaseURL: "https://api.openai.com/v1", // optional, defaults to OpenAI
    LocalServer: myMCPServer, // optional local MCP server
    ExtraHeaders: http.Header{           // optional custom headers for all requests
        "X-Custom-Header": []string{"value"},
    },
    RemoteServerConfigs: []openai.RemoteServerConfig{
        {
            BaseURL: "http://localhost:8080",
            Auth:    myAuthProvider,
            Namespace: "remote1",
        },
    },
})

// Set custom tools (sent to AI but not executed)
client.SetCustomTools([]openai.Tool{...})
```

### Chat Completions with Tool Execution

The client automatically handles tool calls from MCP servers:

```go
req := openai.ChatCompletionRequest{
    Model: "gpt-4",
    Messages: []openai.Message{
        {Role: "user", Content: "What's the weather?"},
    },
}

response, err := client.ChatCompletion(ctx, req)
// Tool calls from MCP servers are executed automatically
// Custom tools are returned in the response for manual handling
```

### Streaming Completions

```go
stream := client.StreamChatCompletion(ctx, req)

for stream.Next() {
    chunk := stream.Current()
    // Process chunks...
    // Tool execution happens automatically during streaming
}

if err := stream.Err(); err != nil {
    // Handle error
}
```

## Quick Start

### Using the Client with MCP Servers

```go
import (
    "context"
    "github.com/paularlott/mcp"
    "github.com/paularlott/mcp/openai"
)

// Create MCP server
server := mcp.NewServer("my-server", "1.0.0")
server.RegisterTool(/* ... */)

// Create OpenAI client with MCP integration
client, err := openai.New(openai.Config{
    APIKey: "sk-...",
    LocalServer: server, // Attach local MCP server
})
if err != nil {
    panic(err)
}

// Make a chat completion - tools are automatically available and executed
req := openai.ChatCompletionRequest{
    Model: "gpt-4",
    Messages: []openai.Message{
        {Role: "user", Content: "Use my tools to help"},
    },
}

response, err := client.ChatCompletion(context.Background(), req)
// Tool calls are executed automatically if needed
```

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

### Accumulating Streaming Responses (Optional)

The client automatically accumulates streaming responses internally to build tool calls and track usage. However, if you need to manually accumulate content or tool calls for custom processing, you can use `CompletionAccumulator`:

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

**Note:** For token usage, you don't need to accumulate manually. The client automatically injects usage estimates into responses when the upstream doesn't provide them. See [Token Usage](#token-usage) for details.

````

### Using Tool Handlers

Tool handlers receive events during tool execution, useful for sending SSE events or logging.

**Important:** The expected call order is:

1. `OnToolCall()` - called BEFORE executing the tool
2. Execute the tool
3. `OnToolResult()` - called AFTER the tool completes

```go
type MyToolHandler struct{}

func (h *MyToolHandler) OnToolCall(toolCall openai.ToolCall) error {
    log.Printf("Starting tool: %s", toolCall.Function.Name)
    return nil
}

func (h *MyToolHandler) OnToolResult(toolCallID, toolName, result string) error {
    log.Printf("Tool %s completed with result: %s", toolName, result)
    return nil
}

// Attach handler to context
ctx = openai.WithToolHandler(ctx, &MyToolHandler{})

// Later, retrieve and use it during tool processing
if handler := openai.ToolHandlerFromContext(ctx); handler != nil {
    // 1. Notify BEFORE execution
    handler.OnToolCall(toolCall)

    // 2. Execute the tool
    result, err := mcpServer.CallTool(ctx, toolCall.Function.Name, toolCall.Function.Arguments)

    // 3. Notify AFTER execution with result
    handler.OnToolResult(toolCall.ID, toolCall.Function.Name, result)
}
````

## Types Reference

### Request/Response Types

| Type                     | Description                                 |
| ------------------------ | ------------------------------------------- |
| `ChatCompletionRequest`  | OpenAI chat completion request              |
| `ChatCompletionResponse` | OpenAI chat completion response             |
| `Message`                | Chat message with role, content, tool calls |
| `Choice`                 | Response choice with message or delta       |
| `Delta`                  | Streaming delta content                     |
| `Usage`                  | Token usage statistics                      |

### Tool Types

| Type               | Description                 |
| ------------------ | --------------------------- |
| `Tool`             | OpenAI tool definition      |
| `ToolFunction`     | Function schema for a tool  |
| `ToolCall`         | Tool call from assistant    |
| `ToolCallFunction` | Function name and arguments |
| `DeltaToolCall`    | Streaming tool call delta   |
| `DeltaFunction`    | Streaming function delta    |

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

## SSE Tool Status Events

When streaming chat completions with server-side tool processing, you can send tool execution status events to clients. These events are sent as SSE comments (prefixed with `:`) so standard SSE clients ignore them, but custom clients can parse them to show tool execution progress in the UI.

### Event Format

Tool events are sent as SSE comments in the format `:eventType:jsonData`:

```
:tool_start:{"tool_call_id":"call_abc123","tool_name":"search","status":"running"}

:tool_end:{"tool_call_id":"call_abc123","tool_name":"search","status":"complete","result":"Search found 5 results..."}
```

The `tool_end` event includes the tool's result, allowing clients to display what the tool returned.

### Using SSEToolHandler

The `SSEToolHandler` implements `ToolHandler` to automatically send tool events during execution:

```go
// Create an SSE event writer (adapt to your HTTP framework)
sseWriter := openai.NewSimpleSSEWriter(responseWriter, func() {
    if f, ok := responseWriter.(http.Flusher); ok {
        f.Flush()
    }
})

// Create the tool handler with optional error logging
toolHandler := openai.NewSSEToolHandler(sseWriter, func(err error, eventType, toolName string) {
    log.Printf("Failed to write %s event for %s: %v", eventType, toolName, err)
})

// Attach to context for use during tool execution
ctx = openai.WithToolHandler(ctx, toolHandler)

// During tool processing, call in order:
// 1. toolHandler.OnToolCall(toolCall)  <- sends :tool_start: event
// 2. Execute the tool
// 3. toolHandler.OnToolResult(...)     <- sends :tool_end: event with result
```

### Custom SSEEventWriter

For production use with your HTTP framework, implement the `SSEEventWriter` interface:

```go
type SSEEventWriter interface {
    WriteEvent(eventType string, data any) error
}
```

### Complete Stream Example

A streaming response with tool execution looks like:

```
data: {"id":"chatcmpl-xxx","choices":[{"delta":{"role":"assistant","content":"Let me look that up..."}}]}

:tool_start:{"tool_call_id":"call_abc","tool_name":"search","status":"running"}

:tool_end:{"tool_call_id":"call_abc","tool_name":"search","status":"complete","result":"Found 3 results for your query..."}

data: {"id":"chatcmpl-xxx","choices":[{"delta":{"content":"Based on the search results..."}}]}

data: {"id":"chatcmpl-xxx","choices":[{"delta":{},"finish_reason":"stop"}]}

data: [DONE]
```

### Parsing Tool Events (JavaScript)

```javascript
const response = await fetch(url, options);
const reader = response.body.getReader();
const decoder = new TextDecoder();

while (true) {
  const { done, value } = await reader.read();
  if (done) break;

  const text = decoder.decode(value);
  for (const line of text.split("\n")) {
    if (line.startsWith(":tool_start:")) {
      const event = JSON.parse(line.slice(":tool_start:".length));
      showSpinner(`Running ${event.tool_name}...`);
    } else if (line.startsWith(":tool_end:")) {
      const event = JSON.parse(line.slice(":tool_end:".length));
      hideSpinner(event.tool_name);
      // Optionally display the result
      if (event.result) {
        showToolResult(event.tool_name, event.result);
      }
    } else if (line.startsWith("data: ") && line !== "data: [DONE]") {
      const chunk = JSON.parse(line.slice(6));
      // Handle OpenAI chunk...
    }
  }
}
```

## Complete Example

Here's a complete example of using the OpenAI client with MCP server integration:

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net/http"

    "github.com/paularlott/mcp"
    "github.com/paularlott/mcp/openai"
)

func main() {
    // Create an MCP server with some tools
    server := mcp.NewServer("example-server", "1.0.0")

    // Register a simple tool
    server.RegisterTool(mcp.Tool{
        Name:        "get_weather",
        Description: "Get current weather for a location",
        InputSchema: mcp.ToolInputSchema{
            Type: "object",
            Properties: map[string]mcp.Property{
                "location": {Type: "string", Description: "City name"},
            },
            Required: []string{"location"},
        },
    }, func(ctx context.Context, args map[string]any) (*mcp.ToolResponse, error) {
        location := args["location"].(string)
        // Simulate weather lookup
        return &mcp.ToolResponse{
            Content: []mcp.Content{
                {Type: "text", Text: fmt.Sprintf("Weather in %s is sunny", location)},
            },
        }, nil
    })

    // Create OpenAI client with MCP server
    client, err := openai.New(openai.Config{
        APIKey:      "sk-your-api-key",
        LocalServer: server,
    })
    if err != nil {
        log.Fatal(err)
    }

    // Set up custom tools (optional - these won't be executed automatically)
    customTools := []openai.Tool{
        {
            Type: "function",
            Function: openai.ToolFunction{
                Name:        "send_email",
                Description: "Send an email",
                Parameters: map[string]any{
                    "type": "object",
                    "properties": map[string]any{
                        "to":      {"type": "string", "description": "Recipient email"},
                        "subject": {"type": "string", "description": "Email subject"},
                        "body":    {"type": "string", "description": "Email body"},
                    },
                    "required": []string{"to", "subject", "body"},
                },
            },
        },
    }
    client.SetCustomTools(customTools)

    // HTTP handler for chat completions
    http.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
        var req openai.ChatCompletionRequest
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            http.Error(w, err.Error(), http.StatusBadRequest)
            return
        }

        // The client automatically adds MCP tools and handles execution
        response, err := client.ChatCompletion(r.Context(), req)
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }

        // If there are custom tool calls, handle them manually
        if len(response.Choices) > 0 {
            for _, toolCall := range response.Choices[0].Message.ToolCalls {
                if toolCall.Function.Name == "send_email" {
                    // Execute custom tool manually
                    args := toolCall.Function.Arguments
                    fmt.Printf("Sending email to %s: %s\n", args["to"], args["subject"])

                    // Add tool result to continue conversation
                    toolResult := openai.BuildToolResultMessage(toolCall.ID, "Email sent successfully")
                    req.Messages = append(req.Messages, response.Choices[0].Message)
                    req.Messages = append(req.Messages, toolResult)

                    // Make another completion with the result
                    response, err = client.ChatCompletion(r.Context(), req)
                    if err != nil {
                        http.Error(w, err.Error(), http.StatusInternalServerError)
                        return
                    }
                    break
                }
            }
        }

        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(response)
    })

    log.Println("Server starting on :8080")
    log.Fatal(http.ListenAndServe(":8080", nil))
}
```

This example shows:

- Setting up an MCP server with tools
- Creating an OpenAI client that integrates the server
- Automatic execution of MCP tools
- Manual handling of custom tools
- Multi-turn conversations with tool results

## Custom Tools

When you want to define tools that interact with your local system or application (rather than using MCP servers), you can set custom tools that will be sent to the AI but NOT executed by the client. Tool calls will be returned in the response for manual execution by your code.

### SetCustomTools(tools []Tool)

Sets custom tools that will be included in AI requests but not auto-executed.

**Parameters:**

- `tools` ([]Tool): Array of OpenAI tool definitions

**Behavior:**

- Tools are sent to the AI in chat completion requests
- When the AI calls a tool, the tool call is returned in the response
- The client does NOT execute these tools automatically
- Your code must handle tool execution manually

**Example:**

```go
import (
    "github.com/paularlott/mcp/openai"
)

client, _ := openai.New(openai.Config{
    APIKey: "sk-...",
})

// Define custom tools
tools := []openai.Tool{
    {
        Type: "function",
        Function: openai.ToolFunction{
            Name:        "read_file",
            Description: "Read a file from the filesystem",
            Parameters: map[string]any{
                "type": "object",
                "properties": map[string]any{
                    "path": map[string]any{
                        "type":        "string",
                        "description": "File path",
                    },
                },
                "required": []string{"path"},
            },
        },
    },
}

client.SetCustomTools(tools)

// Make a chat completion
req := openai.ChatCompletionRequest{
    Model: "gpt-4",
    Messages: []openai.Message{
        {Role: "user", Content: "Read config.json"},
    },
}

response, _ := client.ChatCompletion(ctx, req)

// Check if AI wants to call a tool
if len(response.Choices) > 0 && len(response.Choices[0].Message.ToolCalls) > 0 {
    for _, toolCall := range response.Choices[0].Message.ToolCalls {
        // Execute the tool yourself
        if toolCall.Function.Name == "read_file" {
            path := toolCall.Function.Arguments["path"].(string)
            content := readFile(path)
            // Send result back to AI in next message...
        }
    }
}
```

**See also:** The [scriptlingcoder example](../../scriptling/examples/openai/scriptlingcoder/) demonstrates using custom tools from Scriptling to build an AI coding assistant.

## License

This package is part of the MCP library and is licensed under the same terms.

## Token Usage

The client automatically populates the `Usage` field in responses. If the upstream LLM provides token counts, those are used directly. If not, the client automatically estimates usage and injects it into the response.

This means you can always rely on `response.Usage` being populated:

```go
response, err := client.ChatCompletion(ctx, req)
if err != nil {
    return err
}

// Usage is always available (real or estimated)
fmt.Printf("Tokens used: %d prompt, %d completion\n",
    response.Usage.PromptTokens,
    response.Usage.CompletionTokens)
```

### Manual Token Estimation

If you need to estimate tokens independently (e.g., for pre-flight checks or custom tracking), you can use `TokenCounter` directly:

```go
// Initialize counter and estimate prompt tokens from initial messages
tokenCounter := openai.NewTokenCounter()
tokenCounter.AddPromptTokensFromMessages(req.Messages)

// After receiving streaming deltas, track completion tokens
tokenCounter.AddCompletionTokensFromDelta(&delta)

// Or for non-streaming, track from the full message
tokenCounter.AddCompletionTokensFromMessage(&response.Choices[0].Message)

// When tools are used, track tool result tokens (they become prompt tokens for next iteration)
toolResultMsg := openai.BuildToolResultMessage(toolCall.ID, result)
tokenCounter.AddPromptTokensFromMessages([]openai.Message{toolResultMsg})

// Get the estimated usage
usage := tokenCounter.GetUsage()
// usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens
```

### Token Estimation Algorithm

The `EstimateTokens` function provides a fast, reproducible approximation based on:

- Word boundaries (using `strings.Fields`)
- Punctuation counting (each punctuation mark = 1 token)
- Chat template overhead (~4 tokens for conversation structure)
- Per-message overhead (~3 tokens for role markers and special tokens)

This is suitable for billing estimates and UI display, but not for exact token limit calculations.
