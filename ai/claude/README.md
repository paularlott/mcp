# Claude Provider

This package implements the Claude (Anthropic) provider for the universal AI client.

## Features

- Full support for Claude's Messages API
- Automatic conversion between Claude and OpenAI formats
- Streaming support
- Tool calling (function calling) support
- Model listing
- Proper error handling
- Prompt caching, enabled by default

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
- ✅ Responses API (emulated via chat completions)
- ✅ Streaming Responses API (emulated, identical event sequence to native OpenAI)
- ✅ Prompt caching (enabled by default)
- ❌ Embeddings (not supported by Claude)

## Prompt Caching

Unlike OpenAI-wire-protocol providers, Claude's Messages API only caches content up to an explicit `cache_control: {"type": "ephemeral"}` marker on a content block — sending the same prefix twice isn't enough on its own. This package sets that marker automatically, with nothing required in application code.

On every outbound request, `convertToClaudeRequest` marks up to three breakpoints:

1. **The system prompt** — sent as a single cacheable text block instead of a plain string.
2. **The last tool definition** — caches the entire `tools` array as one segment (marking every tool would be redundant and would waste Anthropic's 4-breakpoint-per-request budget).
3. **The last message** — caches the whole conversation so far.

The last-message breakpoint is recomputed fresh on every call rather than tracked across calls. Anthropic's cache lookup matches the *longest* cached prefix it can find, so as a conversation grows by strict append (each call's messages = the previous call's messages + one assistant reply + one new user message), every call's breakpoint is automatically a superset of the previous call's — whatever portion still matches gets served from cache, with no bookkeeping needed on the client side.

This is on by default. To disable it (there's normally no reason to):

```go
promptCaching := false
client, err := ai.NewClient(ai.Config{
    Provider:      ai.ProviderClaude,
    APIKey:        "sk-ant-...",
    PromptCaching: &promptCaching,
})
```

See [Anthropic's prompt caching docs](https://docs.anthropic.com/claude/docs/prompt-caching) for the underlying mechanism, including current minimum cacheable prompt lengths per model.

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
- [Anthropic Prompt Caching](https://docs.anthropic.com/claude/docs/prompt-caching)
