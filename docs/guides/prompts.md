# Prompts Guide

Prompts are reusable, named message templates with arguments. A client fills in the arguments and the server renders them into messages for the model. They are the third MCP primitive, alongside tools (which *do* things) and resources (which *expose* data).

This server implements `prompts/list` and `prompts/get` over both HTTP and stdio.

## When to Use Prompts

| Primitive | Verb | Identified by | Best for |
| --- | --- | --- | --- |
| Tools | `tools/call` (action) | name | Side effects, computations |
| Resources | `resources/read` (fetch) | URI | Addressable data |
| Prompts | `prompts/get` (render) | name | Pre-built workflows, structured instructions |

Use a prompt when you want to ship a canned workflow or instruction template that the client fills in — e.g. a `code_review` prompt that takes `code` + `language` and emits a review request.

## Defining a Prompt

Register a prompt with `RegisterPrompt`. Declare its arguments with the fluent `Argument` method:

```go
server.RegisterPrompt(
    mcp.NewPrompt("code_review", "Review code").
        Argument("code", "The code to review", true).
        Argument("language", "Programming language", false),
    func(ctx context.Context, req *mcp.PromptRequest) (*mcp.PromptResponse, error) {
        code, _ := req.String("code")
        language := req.StringOr("language", "unknown")
        return mcp.NewPromptResponseText(fmt.Sprintf("Review this %s code:\n%s", language, code)), nil
    },
)
```

`Argument(name, description, required)` — the third argument marks whether the client must supply it. Required arguments are validated automatically before the handler runs; a missing required argument returns an `Invalid params` error.

The handler receives a `*PromptRequest` with string accessors:

- `req.String(name)` — returns the value or `ErrUnknownParameter`
- `req.StringOr(name, default)` — returns the value or a default
- `req.Args()` — the full argument map

## Argument Completion

A client filling in a prompt's arguments can ask the server for suggestions with `completion/complete` (the MCP completion utility). Attach a `Completer` to an argument with `Complete`, right after the `Argument` it completes:

```go
server.RegisterPrompt(
    mcp.NewPrompt("code_review", "Review code").
        Argument("language", "Programming language", true).
        Complete(func(ctx context.Context, req *mcp.CompletionRequest) ([]string, error) {
            return mcp.FilterPrefix([]string{"go", "python", "rust"}, req.Value()), nil
        }).
        Argument("framework", "Framework", false).
        Complete(func(ctx context.Context, req *mcp.CompletionRequest) ([]string, error) {
            // Arguments the user already filled in arrive as context.arguments.
            if lang, _ := req.ContextArg("language"); lang == "python" {
                return mcp.FilterPrefix([]string{"django", "fastapi", "flask"}, req.Value()), nil
            }
            return nil, nil
        }),
    handler,
)
```

The completer receives a `*CompletionRequest`:

- `req.Argument()` — the argument being completed
- `req.Value()` — what the user has typed so far (may be empty)
- `req.ContextArg(name)` / `req.ContextArgs()` — arguments already resolved (`context.arguments`, sent by 2025-06-18+ clients)

Return every match, most relevant first. The spec caps a response at 100 values; the library sends the first 100, sets `total` to the number you returned and `hasMore` when some were cut. `mcp.FilterPrefix` is a ready-made prefix matcher for a fixed list. Return a `ToolError` (e.g. `mcp.NewToolErrorInvalidParams`) to send a specific JSON-RPC error; any other error becomes `-32603`.

Rules the server applies for you, on every protocol revision, over HTTP and stdio:

- The `completions` capability is declared (in `initialize` and in the 2026-07-28 `server/discover`) only when at least one prompt or resource template has a completer, or a request-scoped provider implements completion. Without one, `completion/complete` doesn't exist: `-32601`, or HTTP 404 for a 2026-07-28 request.
- An unknown prompt, or an argument the prompt doesn't declare, is `-32602`. A declared argument with no completer gets an empty list.
- On 2026-07-28 the result carries `resultType` and the server-info `_meta` like every other result.

Completion inputs come from the client, and the spec requires servers to keep completions from disclosing anything the caller may not see: filter suggestions by the caller's access the same way the prompt itself is protected. Rate-limit expensive completers in your HTTP middleware.

Resource templates take completers too; see the [Resources guide](resources.md#variable-completion). Call `server.Complete(ctx, ref, req)` to run a completion directly, and `client.Complete(ctx, ref, argument, value, contextArgs)` to request one from a server.

## Response Constructors

Handlers return a `*PromptResponse` made of one or more messages. Each message has a role (`user` or `assistant`) and a content block.

- `mcp.NewPromptResponseText(text)` — a single `user` text message (the common case)
- `mcp.NewPromptResponseMessages(messages...)` — multiple/arbitrary messages
- `mcp.NewPromptTextMessage(role, text)` — build a single text message

```go
// A multi-turn prompt: assistant sets context, then user asks.
return mcp.NewPromptResponseMessages(
    mcp.NewPromptTextMessage(mcp.PromptRoleAssistant, "I'm ready to review code."),
    mcp.NewPromptTextMessage(mcp.PromptRoleUser, "Please review: "+code),
), nil
```

### Rich content

Message content uses the same block shape as tool responses, so you can embed images, audio, or resources in a prompt:

```go
img, _ := os.ReadFile("screenshot.png")
msg := mcp.PromptMessage{
    Role: mcp.PromptRoleUser,
    Content: mcp.PromptMessageContent{
        Type: "image", Data: base64.StdEncoding.EncodeToString(img), MimeType: "image/png",
    },
}
return mcp.NewPromptResponseMessages(msg), nil
```

## Unregistering

```go
server.UnregisterPrompt("code_review") // returns true if it existed
```

## Listing and Rendering

### Direct API

```go
prompts := server.ListPrompts(ctx)
resp, err := server.GetPrompt(ctx, "code_review", map[string]string{"code": "fmt.Println()"})
```

### Protocol

`prompts/list` returns the prompt descriptors (name, description, arguments). `prompts/get` takes `name` and `arguments` and returns the rendered messages.

## Per-User / Session Prompts

Statically-registered prompts are global. For multi-tenant or per-user prompts, use `PromptProvider` — the prompt analogue of `ToolProvider` and `ResourceProvider`.

### The PromptProvider Interface

```go
type PromptProvider interface {
    GetPrompts(ctx context.Context) ([]MCPPrompt, error)
    GetPrompt(ctx context.Context, name string, args map[string]string) (*PromptResponse, error)
}
```

Attach it in request middleware:

```go
type UserPromptProvider struct{ user *User }

func (p *UserPromptProvider) GetPrompts(ctx context.Context) ([]mcp.MCPPrompt, error) {
    return []mcp.MCPPrompt{
        {Name: "summarize_my_work", Description: "Summarize " + p.user.Name + "'s work"},
    }, nil
}

func (p *UserPromptProvider) GetPrompt(ctx context.Context, name string, args map[string]string) (*mcp.PromptResponse, error) {
    if name != "summarize_my_work" {
        return nil, mcp.ErrUnknownPrompt // miss: let other providers try
    }
    return mcp.NewPromptResponseText(summarize(p.user)), nil
}

func handler(w http.ResponseWriter, r *http.Request) {
    user := authenticateUser(r)
    ctx := mcp.WithPromptProviders(r.Context(), &UserPromptProvider{user: user})
    server.HandleRequest(w, r.WithContext(ctx))
}
```

Providers stack — `prompts/list` returns static prompts **plus** provider prompts (duplicates by name removed, static wins). On `prompts/get`, static prompts are tried first, then providers in attachment order; first hit wins.

### The Miss Contract

When a provider does not handle a name, return `(nil, mcp.ErrUnknownPrompt)`. The server moves on to the next provider. Reserve real errors for genuine failures; a non-nil, non-`ErrUnknownPrompt` error aborts dispatch immediately.

### Completing provider prompts

A provider completes the arguments of the prompts it lists by also implementing `PromptCompletionProvider`:

```go
func (p *UserPromptProvider) CompletePrompt(ctx context.Context, name string, req *mcp.CompletionRequest) (*mcp.CompletionResult, error) {
    return mcp.NewCompletionResult(p.user.ProjectsMatching(req.Value())), nil
}
```

`completion/complete` for a provider prompt goes to the first provider that lists it. Return an `InvalidParams` `ToolError` for an argument the prompt doesn't declare; the library still applies the 100-value cap. A provider that lists a prompt but doesn't implement the interface answers every declared argument with no suggestions, and doesn't turn on the `completions` capability.

## Client Usage

```go
prompts, _ := client.ListPrompts(ctx)
for _, p := range prompts {
    fmt.Println(p.Name, p.Description)
    for _, a := range p.Arguments {
        fmt.Println("  -", a.Name, "(required:", a.Required, ")")
    }
}

resp, _ := client.GetPrompt(ctx, "code_review", map[string]string{"code": "x = 1"})
for _, msg := range resp.Messages {
    fmt.Println(msg.Role, msg.Content.Text)
}
```

Prompts are not cached on the client — each call performs a fresh request.

## Capabilities

The server always advertises the `prompts` capability in `initialize` (`listChanged` is `true` from 2025-03-26 on — see the [Notifications guide](notifications.md)). It advertises `completions` only when it has completers (see [Argument Completion](#argument-completion)).

## See Also

- [Resources Guide](resources.md) — addressable data by URI
- [Tool Providers Guide](tool-providers.md) — the tool-side provider pattern
- [Response Types Guide](response-types.md) — content block types shared with tool responses
