# Ollama Provider

This package implements the Ollama provider for the universal AI client. Unlike the OpenAI-compatible providers, it speaks Ollama's **native** API (`/api/*`) directly — the same surface a local Ollama server exposes.

## Features

- Full support for Ollama's native API (`/api/chat`, `/api/generate`, `/api/embed`)
- Automatic conversion between Ollama and OpenAI formats
- Streaming support (newline-delimited JSON, translated to OpenAI chunks)
- Tool calling (function calling) support, with the same MCP tool-call loop as the other providers
- Model listing via `/api/tags`, enriched with each model's context window from `/api/show`
- Embeddings via `/api/embed`
- Multimodal support (images)
- Proper error handling and retries

## Implementation Details

### Base URL
Defaults to `https://ollama.com`. Any trailing `/v1` (Ollama's OpenAI-compatibility prefix) is stripped, so a base URL configured for the OpenAI shim still works against the native API. Point at a local server with `http://localhost:11434`.

### Authentication
Ollama does not require an API key. When a token is configured, it is sent as `Authorization: Bearer <token>`.

### Messages
- **Text content**: Concatenated and sent as the Ollama message `content`
- **Images**: Pulled out of OpenAI multimodal content parts into Ollama's top-level `images` field (base64, no data-URI prefix). Inline `data:` URIs are decoded directly; `http(s)` image URLs are fetched and re-encoded.
- **Tool calls**: Mapped to Ollama's `tool_calls` shape (arguments as a JSON object, not a string)
- **Tool results**: Forwarded as `tool`-role messages

### Tools
OpenAI tool definitions map directly onto Ollama's tool format (same `type: function` / `function: {name, description, parameters}` shape); the parameter schema passes through unchanged.

### Options
OpenAI request parameters are translated into Ollama's `options` object:
- `temperature` → `options.temperature`
- `top_p` → `options.top_p`
- `max_tokens` / `max_completion_tokens` → `options.num_predict`

### Streaming
Ollama streams newline-delimited JSON objects (not SSE). Each object is translated into an OpenAI `chat.completion.chunk`; content deltas, tool-call deltas, the terminal `done_reason`, and usage counts are all carried through.

### Model Listing & Context Window
`GetModels` calls `/api/tags` and then fans out a bounded, best-effort `/api/show` per model to populate each `Model.ContextWindow` from `model_info.context_length` (the GGUF metadata, not the runtime `num_ctx`). A failure for an individual model leaves its context at zero so callers can fall back to a configured default.

## Supported Features

- ✅ Chat completions (native `/api/chat`)
- ✅ Streaming chat completions (NDJSON)
- ✅ Tool calling
- ✅ Model listing (`/api/tags` + `/api/show` for context windows)
- ✅ Embeddings (native `/api/embed`)
- ✅ Responses API (emulated via chat completions)
- ✅ Streaming Responses API (emulated, identical event sequence to native OpenAI)

## Usage

```go
import (
    "github.com/paularlott/mcp/ai"
)

// Create client — no API key required; base URL defaults to https://ollama.com
client, err := ai.NewClient(ai.Config{
    Provider: ai.ProviderOllama,
})

// Chat completion
response, err := client.ChatCompletion(ctx, ai.ChatCompletionRequest{
    Model: "llama3.2",
    Messages: []ai.Message{
        {Role: "user", Content: "Hello!"},
    },
})

// Embeddings
embeddings, err := client.CreateEmbedding(ctx, ai.EmbeddingRequest{
    Model: "nomic-embed-text",
    Input: "Hello, world!",
})

// List models (each carries its discovered ContextWindow)
models, err := client.GetModels(ctx)
```

## API Reference

- [Ollama API Documentation](https://github.com/ollama/ollama/blob/main/docs/api.md)
- [Ollama Chat](https://github.com/ollama/ollama/blob/main/docs/api.md#generate-a-chat-completion)
- [Ollama Embeddings](https://github.com/ollama/ollama/blob/main/docs/api.md#generate-embeddings)
- [Ollama Show Model](https://github.com/ollama/ollama/blob/main/docs/api.md#show-model-information)
