# Elicitation Guide

Elicitation lets a server ask the user, through the client, for structured
input while it handles a request: a confirmation before a destructive step, a
missing parameter, a choice between options. The library implements **form
mode** for every protocol revision that has elicitation, behind one call:

```go
res, err := mcp.Elicit(ctx, mcp.ElicitForm{
    Key:     "confirm_delete",
    Message: "Delete 42 files?",
    Schema: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "confirm": map[string]any{"type": "boolean", "default": false},
        },
        "required": []string{"confirm"},
    },
})
```

Call it from a tool, prompt or resource handler with the handler's `ctx` (or
one derived from it). Tools reached through `execute_tool` and tools from a
`ToolProvider` get the same context, so they can elicit too.

## The result

```go
if err != nil {
    return nil, err // see "Errors" below; return ErrElicitationPending as is
}
switch res.Action {
case mcp.ElicitAccept:
    ok, _ := res.Content["confirm"].(bool)
    // ...
case mcp.ElicitDecline: // the user said no
case mcp.ElicitCancel:  // the user dismissed the form
}
```

`Content` is only set on accept, and has already been checked against your
schema (an answer that does not match is `ErrInvalidElicitationResult`).
Numbers decode as `float64`, multi-select answers as `[]any`.

## Schemas

The spec restricts `requestedSchema` to a flat object whose properties are
primitives. `Elicit` rejects anything else before asking:

| Property | Shape |
| --- | --- |
| text | `{"type": "string"}`, optional `minLength`, `maxLength`, `format` (`email`, `uri`, `date`, `date-time`), `default` |
| number | `{"type": "number"}` or `"integer"`, optional `minimum`, `maximum`, `default` |
| boolean | `{"type": "boolean"}`, optional `default` |
| single choice | `{"type": "string", "enum": [...]}`, or titled: `"oneOf": [{"const": "a", "title": "A"}, ...]` (legacy `enumNames` is accepted) |
| multiple choice | `{"type": "array", "items": {"type": "string", "enum": [...]}}`, or titled: `"items": {"anyOf": [{"const": ..., "title": ...}]}` |

Defaults (SEP-1034) and the titled and multi-select enums (SEP-1330) arrived
in 2025-11-25; a 2025-06-18 client may ignore them. Never ask for passwords,
API keys or other secrets through a form: the spec forbids it.

## How it works in each era

### 2025-06-18 and 2025-11-25: a request to the client

`Elicit` sends an `elicitation/create` request to the client and blocks until
the client answers.

- **Only when allowed.** The client must have declared the `elicitation`
  capability in `initialize` (an empty object means form mode only); a client
  that declared only `url` mode is not asked. 2025-03-26 has no elicitation.
  In each case `Elicit` returns `ErrElicitationUnsupported`.
- **Streamable HTTP.** The request goes on the originating request's response
  stream, which switches to SSE as soon as it is sent (as progress does), so the
  client must send `Accept: text/event-stream`; without it, or with
  `SetResponseStreaming` disabled, elicitation is unsupported for that
  request. The client POSTs its JSON-RPC response separately (answered 202),
  and the server routes it by id to the waiting handler in the same session.
- **stdio.** The request is a line on stdout; the client's response line on
  stdin is routed back the same way.
- **Timeouts.** `SetElicitationTimeout` (default 10 minutes; a person is
  filling in a form) bounds the wait. When it passes, the server sends
  `notifications/cancelled` for its request and `Elicit` returns
  `ErrElicitationTimeout`. Make sure your HTTP server's `WriteTimeout` and any
  proxy allow a response stream that long; the stream sends keep-alive comments.
- **Cancellation.** If the client cancels the originating request (or closes
  its stream), `Elicit` returns the context's error and the waiting request is
  cleaned up. A response that arrives afterwards is dropped.

**Deploying several instances.** The client's response arrives as a separate
POST and must reach the instance that holds the waiting handler: use sticky
routing on `MCP-Session-Id`. The capabilities a client declared are kept in
memory by the instance that served its `initialize`, so a request that lands
on another instance sees elicitation as unsupported. Without session
management, every client shares one scope (the most recent `initialize`), so
enable sessions when several clients elicit at once.

### 2026-07-28: InputRequiredResult (multi round-trip requests)

2026-07-28 removes server-to-client requests. Instead, `tools/call`,
`prompts/get` and `resources/read` may answer with an `InputRequiredResult`:

```json
{
  "resultType": "input_required",
  "inputRequests": {
    "confirm_delete": {
      "method": "elicitation/create",
      "params": {"mode": "form", "message": "Delete 42 files?", "requestedSchema": {...}}
    }
  },
  "requestState": "..."
}
```

The client asks the user and retries the same request with
`inputResponses: {"confirm_delete": {"action": "accept", "content": {...}}}`
and the `requestState` it was given.

The library does this by **re-running your handler**:

1. First run: `Elicit` finds no answer and returns `ErrElicitationPending`.
   Your handler returns it (wrapped or not), and the library answers with an
   `InputRequiredResult` holding every question still open.
2. The retry runs the handler again from the top. This time `Elicit` finds
   the answer in `inputResponses` and returns it at once.

So on 2026-07-28 **everything before `Elicit` runs again on each retry, and
must be safe to repeat**: read, compute and validate before asking, and do
side effects (writes, payments, messages) after the last `Elicit`. A handler
that ignores `ErrElicitationPending` and returns a result anyway still gets
an `InputRequiredResult`: while a question is open, the handler's own result
is discarded.

**Keys must be deterministic.** `ElicitForm.Key` is the `inputRequests` key
the answer comes back under, so it must be the same on every run of the same
request. Leave it empty and questions are numbered in the order the handler
asks them (`elicitation-1`, `elicitation-2`, ...), which is deterministic as
long as the handler is. A key may be used once per request.

**Several questions.** A handler that asks one question after another needs
one round trip each; earlier answers travel in `requestState`, so the last
run sees them all even though the client only sends the newest. To ask
several at once, use `ElicitAll`:

```go
res, err := mcp.ElicitAll(ctx,
    mcp.ElicitForm{Key: "name", Message: "Your name?", Schema: nameSchema},
    mcp.ElicitForm{Key: "team", Message: "Your team?", Schema: teamSchema},
)
```

On 2026-07-28 both go into one `InputRequiredResult`; on 2025-x they are asked
in turn.

**What the library enforces:**

- The client is only asked when the request's `_meta` `clientCapabilities`
  declare `elicitation` (form mode); otherwise `ErrElicitationUnsupported`.
- A missing answer gets a new `InputRequiredResult` re-requesting it rather
  than an error; answers to questions the server did not ask are ignored.
- A malformed `inputResponses` (not an object, or an answer that is not a
  valid result for its form) is a `-32602` error.
- `InputRequiredResult` is only ever sent on `tools/call`, `prompts/get` and
  `resources/read`.

### requestState

`requestState` passes through the client, so the spec treats it as
attacker-controlled. The library protects it with HMAC-SHA256 and binds it
to:

- the method and a digest of the request's params (everything except
  `_meta`, `inputResponses` and `requestState`), so state from one call
  cannot be replayed on another;
- an expiry, `SetRequestStateTTL` (default 10 minutes);
- the authenticated principal, `TokenInfo.Subject`, when the request carries
  one (see `Protect` and `WithTokenInfo`).

State that fails any check is rejected with `-32602` before the handler
runs. The state is signed, not encrypted: it carries the user's earlier
answers, which the client already knows. It bounds replay but does not make
a request single-use; enforce that yourself if an action must happen at most
once.

The key is random per process by default, so state from one instance is
rejected by another. For several instances, share a secret:

```go
server.SetRequestStateKey(secret) // 32 random bytes or more, the same on every instance
```

With that, a 2026-07-28 retry can land on any instance; no sticky routing is
needed.

## Errors

| Error | Meaning |
| --- | --- |
| `ErrElicitationPending` | 2026-07-28: the question went to the client. Return it from the handler. |
| `ErrElicitationUnsupported` | The client cannot be asked (capability, revision or transport). Carry on without the input, or fail. |
| `ErrElicitationTimeout` | 2025-x: no answer within `SetElicitationTimeout`. |
| `ErrInvalidElicitationResult` | The answer is malformed or does not match the schema. |
| `*ElicitationError` | 2025-x: the client answered `elicitation/create` with a JSON-RPC error. |
| the context's error | The originating request was cancelled. |

Returned from a tool handler, any of these but `ErrElicitationPending`
becomes a tool execution error (`isError: true`) like any other handler error.

Use `mcp.ElicitationSupported(ctx)` to choose a different path up front, for
example a tool argument instead of a question.

## Federated remote servers

When this server proxies a remote MCP server (`RegisterRemoteServer`,
`RegisterRemoteServerDiscoverable`, the namespace and unprefixed
`execute_tool` fallbacks, a request-scoped `NewRemoteProvider`), a remote
tool, prompt or resource that asks for input reaches **your** caller, and the
answer goes back to the remote. Nothing needs configuring: the forwarding
`Client` runs with your handler's context and puts each question to your
caller with `Elicit`, so any mix of eras works on the two hops.

| Remote (upstream) | Your caller (downstream) | What happens |
| --- | --- | --- |
| 2025-x | 2025-x | The remote's `elicitation/create` is forwarded to your caller as an `elicitation/create` of yours; the answer is sent back to the remote. |
| 2025-x | 2026-07-28 | The question goes into your `InputRequiredResult` and the forwarded call is cancelled. On your caller's retry the remote is called again and, when it asks the same question, gets the answer from your caller's `inputResponses`. |
| 2026-07-28 | 2025-x | Each of the remote's `elicitation/create` input requests is put to your caller; the remote is retried with `inputResponses` and its exact `requestState`, round after round, until it completes. |
| 2026-07-28 | 2026-07-28 | The remote's questions go into your `InputRequiredResult`. The remote's `requestState` and open questions travel inside **your** signed `requestState`, so your caller's retry resumes the remote's exchange instead of starting it over. |

Details:

- **Capabilities.** A Legacy (2025-x) connection declares form-mode
  elicitation in `initialize`, once for every caller. A Modern (2026-07-28)
  request declares it only when the caller it is forwarded for can be asked
  (`ElicitationSupported`); sampling and roots are never declared.
- **One connection, many callers.** A `Client` is shared by every caller, so
  each `elicitation/create` is answered for the forwarded call it belongs to:
  on HTTP, the call whose response stream carried it; on stdio, the call
  named by the request's `io.github.paularlott/relatedRequestId` `_meta`
  hint (this library's servers send it), or else the only call in flight.
  Concurrent callers never get each other's questions.
- **A caller that cannot be asked.** When the call cannot be identified, or
  its caller cannot be asked (no elicitation capability, no SSE stream), a
  2025-x remote is answered `{"action": "cancel"}` at once: no user made a
  choice, and the remote never waits. A 2026-07-28 remote is not told the
  client can elicit; if it asks anyway, the call fails with
  `ErrInputRequired`.
- **Repeated work.** With a 2025-x remote and a 2026-07-28 caller the remote
  is called again on each of your caller's rounds, so it repeats its work up
  to the question, as any 2025-x server must allow a client to.
- **Keys.** Relayed questions get keys like `relay-1.0.name` (the federated
  call's position in your handler, the remote's round, the remote's key) or
  `relay-1.legacy.1` for a 2025-x remote, deterministic across re-runs.
- **Sampling and roots** input requests from a remote are not relayed: a
  2025-x remote's `sampling/createMessage` or `roots/list` is answered with
  Method not found, and a 2026-07-28 remote asking for either fails the call
  with `ErrInputRequired`.

## Answering elicitation in a Client

An application that uses this library's `Client` can answer a server's
questions itself, a CLI prompt or a UI dialog for example, with
`OnElicitation`:

```go
client := mcp.NewClient(url, nil, "")
client.OnElicitation(func(ctx context.Context, req mcp.ElicitationRequest) (*mcp.ElicitResult, error) {
    // Show req.Message and a form built from req.Schema to the user.
    answers, ok := askUser(req.Message, req.Schema)
    if !ok {
        return &mcp.ElicitResult{Action: mcp.ElicitDecline}, nil
    }
    return &mcp.ElicitResult{Action: mcp.ElicitAccept, Content: answers}, nil
})
if err := client.Initialize(ctx); err != nil { ... }
```

Register the handler before `Initialize`. With it, the Client declares
form-mode elicitation, and:

- **2025-06-18 / 2025-11-25:** the server's `elicitation/create` request is
  answered by the handler. A server may send it on the call's response stream
  or on the session's GET event stream, so over HTTP the Client also opens the
  GET stream (as `EnableNotifications` does); release it with `Close`.
- **2026-07-28:** when a call returns an `InputRequiredResult` with
  elicitation input requests, the handler answers each one and the Client
  retries the call with the answers and the server's exact `requestState`,
  under a new request id, until the server completes. A result without
  `resultType` is complete, and one call's state is never sent with another.
- On accept, any schema property the handler leaves out that has a `default`
  is filled with it, as the spec asks clients that support defaults to do
  (SEP-1034).
- A handler that returns an error, or an unknown action, answers cancel.

Without a handler, questions are cancelled on 2025-x, and on 2026-07-28 the
Client doesn't declare elicitation, so the server can't ask. A Client that a
Server uses to forward calls relays questions to the Server's own caller when
that caller can be asked (see above), and uses the handler otherwise.

## Not implemented

- **URL mode** (`mode: "url"`, the `-32042` URLElicitationRequiredError and
  `notifications/elicitation/complete`). It exists only in 2025-11-25 and
  2026-07-28 forbids it; a client that declared only URL mode is treated as
  not supporting elicitation, and a remote's URL-mode request is refused.
- **Sampling and roots**, the other server-to-client requests, are
  deliberately not implemented in either era: 2026-07-28 deprecates them
  (SEP-2577).
