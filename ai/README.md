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
    ExtraHeaders      http.Header          // Optional: custom headers
    HTTPPool          pool.HTTPPool        // Optional: custom HTTP client pool
    LocalServer       MCPServer            // Optional: local MCP server
    MCPServerConfigs  []RemoteServerConfig // Optional: remote MCP servers
}
```

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

- Default base URL: `http://localhost:11434/v1`
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
