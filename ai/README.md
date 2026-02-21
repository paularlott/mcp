# Universal AI Client

A unified interface for multiple LLM providers with OpenAI-compatible API.

## Supported Providers

- **OpenAI** - Full support including native Responses API with SSE streaming
- **Claude** (Anthropic) - Chat, streaming, tool calling, emulated Responses API
- **Gemini** (Google) - Chat, streaming, embeddings, tool calling, emulated Responses API
- **Ollama** - Local models with OpenAI-compatible API, emulated Responses API
- **ZAi** - OpenAI-compatible endpoint, emulated Responses API
- **Mistral** - OpenAI-compatible endpoint, emulated Responses API

## Features

- Unified interface across all providers
- OpenAI-compatible request/response format
- Streaming support
- Tool calling (function calling)
- Embeddings (OpenAI, Gemini, Ollama, ZAi, Mistral)
- Model listing
- Automatic format conversion for Claude and Gemini
- Responses API — native on OpenAI, transparently emulated on all other providers

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
    Model: "gpt-4o",
    Messages: []ai.Message{
        {Role: "user", Content: "Hello!"},
    },
})

// Streaming chat
stream := client.StreamChatCompletion(ctx, ai.ChatCompletionRequest{
    Model: "gpt-4o",
    Messages: []ai.Message{
        {Role: "user", Content: "Hello!"},
    },
})
for stream.Next() {
    fmt.Print(stream.Current().Choices[0].Delta.Content)
}
if err := stream.Err(); err != nil {
    log.Fatal(err)
}

// Responses API — single-shot (works on all providers)
resp, err := client.CreateResponse(ctx, ai.CreateResponseRequest{
    Model: "gpt-4o",
    Input: []any{
        map[string]any{"type": "message", "role": "user", "content": "Hello!"},
    },
})

// Responses API — streaming (works on all providers)
rstream := client.StreamResponse(ctx, ai.CreateResponseRequest{
    Model: "gpt-4o",
    Input: []any{
        map[string]any{"type": "message", "role": "user", "content": "Hello!"},
    },
})
for rstream.Next() {
    event := rstream.Current()
    fmt.Print(event.TextDelta())
    if r := event.Response(); r != nil {
        fmt.Printf("\nDone. tokens=%d\n", r.Usage.TotalTokens)
    }
}
if err := rstream.Err(); err != nil {
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
    Provider() string
    SupportsCapability(cap string) bool

    GetModels(ctx context.Context) (*ModelsResponse, error)

    ChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error)
    StreamChatCompletion(ctx context.Context, req ChatCompletionRequest) *ChatStream

    CreateEmbedding(ctx context.Context, req EmbeddingRequest) (*EmbeddingResponse, error)

    CreateResponse(ctx context.Context, req CreateResponseRequest) (*ResponseObject, error)
    StreamResponse(ctx context.Context, req CreateResponseRequest) *ResponseStream
    GetResponse(ctx context.Context, id string) (*ResponseObject, error)
    CancelResponse(ctx context.Context, id string) (*ResponseObject, error)
    DeleteResponse(ctx context.Context, id string) error
    CompactResponse(ctx context.Context, id string) (*ResponseObject, error)

    Close() error
}
```

## Provider Capabilities

| Feature           | OpenAI | Claude | Gemini | Ollama | ZAi | Mistral |
| ----------------- | ------ | ------ | ------ | ------ | --- | ------- |
| Chat              | ✅     | ✅     | ✅     | ✅     | ✅  | ✅      |
| Streaming         | ✅     | ✅     | ✅     | ✅     | ✅  | ✅      |
| Tools             | ✅     | ✅     | ✅     | ✅     | ✅  | ✅      |
| Embeddings        | ✅     | ❌     | ✅     | ✅     | ✅  | ✅      |
| Responses API     | ✅ native | ✅ emulated | ✅ emulated | ✅ emulated | ✅ emulated | ✅ emulated |
| Streaming Responses | ✅ native SSE | ✅ emulated | ✅ emulated | ✅ emulated | ✅ emulated | ✅ emulated |

The Responses API is **natively** supported on `api.openai.com` (real `/responses` SSE endpoint). For all other providers it is **transparently emulated** via chat completions — callers see identical types, event sequences, and field structures regardless of provider.

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
    RequestTimeout: 30 * time.Minute,
})
```

### Default Parameters

You can set default `MaxTokens` and `Temperature` at the client level:

```go
client, err := ai.NewClient(ai.Config{
    Provider:    ai.ProviderClaude,
    APIKey:      "sk-ant-...",
    MaxTokens:   2048,
    Temperature: 0.7,
})
```

**Note:** Claude requires `max_tokens` to be set. If not provided in the config, it defaults to 4096.

## Provider-Specific Details

### OpenAI

- Default base URL: `https://api.openai.com/v1`
- Supports all features including native Responses API and SSE streaming

### Claude

- Default base URL: `https://api.anthropic.com/v1`
- Automatic format conversion to/from OpenAI format
- System messages extracted to `system` parameter
- Responses API emulated via chat completions
- See [claude/README.md](claude/README.md) for details

### Gemini

- Default base URL: `https://generativelanguage.googleapis.com/v1beta`
- Chat via OpenAI-compatible `/openai/` endpoint
- Embeddings via native `embedContent` API
- Responses API emulated via chat completions
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
    log.Fatal(err)
}
```
