# Claude Provider

This package implements the Claude (Anthropic) provider for the universal AI client.

## Features

- Full support for Claude's Messages API
- Automatic conversion between Claude and OpenAI formats
- Streaming support
- Tool calling (function calling) support
- Model listing
- Proper error handling

## Format Conversion

The Claude provider automatically converts between Claude's native format and OpenAI's format:

### Messages
- **System messages**: Extracted and sent via Claude's `system` parameter
- **User/Assistant messages**: Converted to Claude's content block format
- **Tool calls**: Mapped to Claude's `tool_use` content blocks
- **Tool results**: Mapped to Claude's `tool_result` content blocks

### Tools
- OpenAI tool definitions → Claude function declarations
- Parameters schema is passed through directly

### Streaming
- Claude's SSE events → OpenAI streaming chunks
- `content_block_delta` → content deltas
- `content_block_start` → tool call initialization
- `message_delta` → finish reasons

## Supported Models

The client dynamically fetches the list of available models from Claude's `/models` API endpoint.

## Supported Features

- ✅ Chat completions
- ✅ Streaming chat completions
- ✅ Tool calling
- ✅ Model listing
- ❌ Embeddings (Claude doesn't provide embeddings)
- ❌ Responses API (OpenAI-specific feature)

## Usage

```go
import (
    "github.com/paularlott/mcp/ai"
)

// Create client
client, err := ai.NewClient(ai.Config{
    Provider: ai.ProviderClaude,
    APIKey:   "sk-ant-...",
})

// Chat completion
response, err := client.ChatCompletion(ctx, ai.ChatCompletionRequest{
    Model: "claude-3-5-sonnet-20241022",
    Messages: []ai.Message{
        {Role: "user", Content: "Hello!"},
    },
})

// List models
models, err := client.GetModels(ctx)
```

## API Reference

- [Anthropic Messages API](https://docs.anthropic.com/claude/reference/messages_post)
- [Anthropic Streaming](https://docs.anthropic.com/claude/reference/messages-streaming)
- [Anthropic Tool Use](https://docs.anthropic.com/claude/docs/tool-use)
