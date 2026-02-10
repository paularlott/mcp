# Universal AI Client

A unified interface for multiple LLM providers with OpenAI-compatible API.

## Supported Providers

- **OpenAI** - Full support including Responses API
- **Claude** (Anthropic) - Chat, streaming, tool calling
- **Gemini** (Google) - Chat, streaming, embeddings, tool calling
- **Ollama** - Local models with OpenAI-compatible API
- **ZAi** - OpenAI-compatible endpoint
- **Mistral** - OpenAI-compatible endpoint

## Features

- Unified interface across all providers
- OpenAI-compatible request/response format
- Streaming support
- Tool calling (function calling)
- Embeddings (OpenAI, Gemini, Ollama, ZAi, Mistral)
- Model listing
- Automatic format conversion for Claude and Gemini

## Quick Start

```go
import "github.com/paularlott/mcp/ai"

// Create client
client, err := ai.NewClient(ai.Config{
    Provider: ai.ProviderOpenAI,
    APIKey:   "sk-...",
})

// Chat completion
response, err := client.ChatCompletion(ctx, ai.ChatCompletionRequest{
    Model: "gpt-4",
    Messages: []ai.Message{
        {Role: "user", Content: "Hello!"},
    },
})

// Streaming
stream := client.StreamChatCompletion(ctx, ai.ChatCompletionRequest{
    Model: "gpt-4",
    Messages: []ai.Message{
        {Role: "user", Content: "Hello!"},
    },
})

for chunk := range stream.Responses() {
    fmt.Print(chunk.Choices[0].Delta.Content)
}
if err := stream.Err(); err != nil {
    log.Fatal(err)
}

// Embeddings
embeddings, err := client.CreateEmbedding(ctx, ai.EmbeddingRequest{
    Model: "text-embedding-3-small",
    Input: "Hello, world!",
})

// List models
models, err := client.GetModels(ctx)
```

## Client Interface

All providers implement the `ai.Client` interface:

```go
type Client interface {
    // Provider information
    Provider() string
    SupportsCapability(cap string) bool

    // Model management
    GetModels(ctx context.Context) (*ModelsResponse, error)

    // Chat completions
    ChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error)
    StreamChatCompletion(ctx context.Context, req ChatCompletionRequest) *ChatStream

    // Embeddings
    CreateEmbedding(ctx context.Context, req EmbeddingRequest) (*EmbeddingResponse, error)

    // OpenAI Responses API
    CreateResponse(ctx context.Context, req CreateResponseRequest) (*ResponseObject, error)
    GetResponse(ctx context.Context, id string) (*ResponseObject, error)
    CancelResponse(ctx context.Context, id string) (*ResponseObject, error)

    // Close/cleanup
    Close() error
}
```

## Provider Capabilities

| Feature       | OpenAI | Claude | Gemini | Ollama | ZAi | Mistral |
| ------------- | ------ | ------ | ------ | ------ | --- | ------- |
| Chat          | ✅     | ✅     | ✅     | ✅     | ✅  | ✅      |
| Streaming     | ✅     | ✅     | ✅     | ✅     | ✅  | ✅      |
| Tools         | ✅     | ✅     | ✅     | ✅     | ✅  | ✅      |
| Embeddings    | ✅     | ❌     | ✅     | ✅     | ✅  | ✅      |
| Responses API | ✅     | ❌     | ❌     | ❌     | ❌  | ❌      |

## Configuration

```go
type Config struct {
    Provider          Provider
    APIKey            string
    BaseURL           string               // Optional: custom endpoint
    MaxTokens         int                  // Optional: default max_tokens for all requests
    Temperature       float32              // Optional: default temperature for all requests
    RequestTimeout    time.Duration        // Optional: timeout for AI operations (default: 10 minutes)
    ExtraHeaders      http.Header          // Optional: custom headers
    HTTPPool          pool.HTTPPool        // Optional: custom HTTP client pool
    LocalServer       MCPServer            // Optional: local MCP server
    MCPServerConfigs  []RemoteServerConfig // Optional: remote MCP servers
}
```

### Request Timeout & Context Handling

AI completion requests use a **detached context** that preserves the parent's context values (tool providers, user info, etc.) but has an independent cancellation signal. This prevents parent context cancellation (e.g. from script timeouts or request handlers) from killing long-running AI operations.

The default timeout is **10 minutes**. You can customize it:

```go
client, err := ai.NewClient(ai.Config{
    Provider:       ai.ProviderOpenAI,
    APIKey:         "sk-...",
    RequestTimeout: 30 * time.Minute, // Custom timeout
})
```

This applies consistently across all providers (OpenAI, Claude, Gemini, Ollama, ZAi, Mistral).

### Default Parameters

You can set default `MaxTokens` and `Temperature` at the client level. These will be applied to all requests that don't explicitly set these values:

```go
client, err := ai.NewClient(ai.Config{
    Provider:    ai.ProviderClaude,
    APIKey:      "sk-ant-...",
    MaxTokens:   2048,      // Default for all requests
    Temperature: 0.7,       // Default for all requests
})

// Uses client defaults (2048 tokens, 0.7 temperature)
response, err := client.ChatCompletion(ctx, req)

// Override per request
req.MaxTokens = 4096
response, err := client.ChatCompletion(ctx, req)
```

**Note:** Claude requires `max_tokens` to be set. If not provided in the config, it defaults to 4096.

## Provider-Specific Details

### OpenAI

- Default base URL: `https://api.openai.com/v1`
- Supports all features including Responses API
- Native OpenAI format (no conversion needed)

### Claude

- Default base URL: `https://api.anthropic.com/v1`
- Automatic format conversion to/from OpenAI format
- System messages extracted to `system` parameter
- See [claude/README.md](claude/README.md) for details

### Gemini

- Default base URL: `https://generativelanguage.googleapis.com/v1beta`
- Chat via OpenAI-compatible `/openai/` endpoint
- Embeddings via native `embedContent` API
- See [gemini/README.md](gemini/README.md) for details

### Ollama

- Default base URL: `https://ollama.com/v1/`
- OpenAI-compatible API
- No API key required

### ZAi

- Default base URL: `https://api.z.ai/api/paas/v4/`
- OpenAI-compatible API

### Mistral

- Default base URL: `https://api.mistral.ai/v1`
- OpenAI-compatible API

## Error Handling

```go
response, err := client.ChatCompletion(ctx, req)
if err != nil {
    // Handle error
    log.Fatal(err)
}
```
