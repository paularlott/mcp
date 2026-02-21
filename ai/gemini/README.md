# Gemini Provider

This package implements the Google Gemini provider for the universal AI client.

## Features

- Chat completions via Gemini's OpenAI-compatible endpoint
- Streaming support
- Embeddings via Gemini's embedContent API
- Model listing
- Tool calling (function calling) support
- Proper error handling

## Implementation Details

### Chat Completions
Chat completions delegate to the OpenAI client using Gemini's `/openai/` endpoint, which provides OpenAI-compatible chat completion API.

### Embeddings
Embeddings use Gemini's native `embedContent` API:
- Endpoint: `POST /v1beta/models/{model}:embedContent`
- Task type: `SEMANTIC_SIMILARITY` (OpenAI-like behavior)
- Supports output dimensionality: 128, 256, 512, 768, 1024, 1536, 3072
- Handles single string or array of strings as input

### Model Listing
Retrieves available models from Gemini's `/models` endpoint and returns them in OpenAI format.

## Supported Features

- ✅ Chat completions (via OpenAI-compatible endpoint)
- ✅ Streaming chat completions
- ✅ Embeddings (via native Gemini API)
- ✅ Model listing
- ✅ Tool calling
- ✅ Responses API (emulated via chat completions)
- ✅ Streaming Responses API (emulated, identical event sequence to native OpenAI)

## Usage

```go
import (
    "github.com/paularlott/mcp/ai"
)

// Create client
client, err := ai.NewClient(ai.Config{
    Provider: ai.ProviderGemini,
    APIKey:   "AIza...",
})

// Chat completion
response, err := client.ChatCompletion(ctx, ai.ChatCompletionRequest{
    Model: "gemini-2.0-flash-exp",
    Messages: []ai.Message{
        {Role: "user", Content: "Hello!"},
    },
})

// Embeddings
embeddings, err := client.CreateEmbedding(ctx, ai.EmbeddingRequest{
    Model: "text-embedding-004",
    Input: "Hello, world!",
    Dimensions: 768,
})

// List models
models, err := client.GetModels(ctx)
```

## API Reference

- [Gemini API Documentation](https://ai.google.dev/gemini-api/docs)
- [OpenAI Compatibility](https://ai.google.dev/gemini-api/docs/openai)
- [Embeddings API](https://ai.google.dev/gemini-api/docs/embeddings)
- [Function Calling](https://ai.google.dev/gemini-api/docs/function-calling)
