# Error Handling

## How tools/call Reports Errors

The MCP tools spec has two kinds of error. **Protocol errors** are JSON-RPC
errors, and for `tools/call` there are only three: an unknown tool, a request
that fails the `CallToolRequest` schema, and a server error. **Tool execution
errors** are results with `isError: true` — API failures, input validation
errors and business-logic errors — which clients should show the model so it
can correct the call.

A handler runs only after the request has parsed and its tool was found, so
the only protocol error it can raise is a server error. What a handler (or a
tool provider) returns is reported like this:

| Handler returns | tools/call sends |
|---|---|
| `mcp.NewToolResponseError("...")` | result, `isError: true` |
| a plain `error` | result, `isError: true`, with `err.Error()` |
| `mcp.NewToolErrorInvalidParams("...")` (-32602) | result, `isError: true`, with the message |
| `mcp.NewToolError(code, "...", data)`, `code` an application code | result, `isError: true`, with the message (`data` is not sent) |
| `mcp.NewToolErrorInternal("...")` (-32603) | JSON-RPC error |
| `mcp.NewToolErrorMissingClientCapability("sampling")` (-32021) | JSON-RPC error, HTTP 400 on 2026-07-28 |
| `mcp.NewToolError(code, ...)`, `code` in -32000..-32099 | JSON-RPC error |

A JSON-RPC error a remote server sends for a call this server forwards
(`RegisterRemoteServer`, `NewRemoteProvider`) is passed on as it came.
`Server.CallTool` returns a `*mcp.ToolError` as the handler returned it in
every case.

## Error Types

### Tool Execution Errors

For anything that is the tool's own failure — a value the schema can't
judge, an upstream API error, a business rule — return a tool execution
error, so the model reads the message and can self-correct:

```go
func handleBook(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
    date, _ := req.String("date")
    if !isFuture(date) {
        return mcp.NewToolResponseError("date must be in the future"), nil
    }
    // ...
}
```

`mcp.NewToolErrorInvalidParams("...")` and a plain `error` give the same
result. The server already checks arguments against the input schema (see
[Argument Validation](#argument-validation)), so this is for checks the schema
can't express.

### Internal Server Error

Use when a server-side operation fails; it is sent as a JSON-RPC error:

```go
result, err := database.Query(ctx)
if err != nil {
    log.Printf("query failed: %v", err) // log the detail
    return nil, mcp.NewToolErrorInternal("database operation failed") // don't expose internals
}
```

### Missing Client Capability

A server must not rely on a client capability the request didn't declare.
A handler that needs one checks the request's capabilities and returns the
spec's `MissingRequiredClientCapabilityError`:

```go
if _, ok := mcp.ClientCapabilitiesFromContext(ctx)["sampling"]; !ok {
    return nil, mcp.NewToolErrorMissingClientCapability("sampling")
}
```

It is sent as JSON-RPC error `-32021` with
`data.requiredCapabilities: {"sampling": {}}`, and HTTP 400 on the 2026-07-28
Streamable HTTP wire.

### Custom Codes

`mcp.NewToolError` takes any code. An application code is still a tool
execution error on the wire — the model gets the message, not the code or
data — so it is mostly useful to `Server.CallTool` callers, who get the whole
`*mcp.ToolError`:

```go
return nil, mcp.NewToolError(1001, "file too large", map[string]any{
    "max_size": maxSize,
    "provided": size,
})
```

## Argument Validation

Arguments are checked against the tool's whole input schema before the
handler runs, as the MCP tools spec requires of servers: required
properties, types, constraints, enums and `additionalProperties`.

A string sent for a parameter declared `number`, `integer` or `boolean` (and
not `string`) is first converted when the whole string is such a value:
`"10"` becomes `10`, `"2.5"` becomes `2.5`, `"true"` becomes `true`. Models
often quote scalars and these mean only one thing, so the handler gets the
declared type. A string that only partly parses (`"10abc"`, `" 10"`,
`"0x10"`), `"2.5"` for an integer, or `"yes"` for a boolean is not converted.

Anything that still fails — a missing required argument, a value of the wrong
type, an unknown argument — comes back as a tool execution error
(`isError: true`) whose text names the problem, so the model can correct the
call; the handler never runs. Unknown arguments are refused rather than
dropped: a misspelt filter must not silently widen the call. `Server.CallTool`
callers get a `*mcp.ToolError` with code `-32602` for the same failures.

## Error Code Reference

Standard JSON-RPC codes used by the library:

| Code | Meaning |
|---|---|
| `-32700` | Parse error |
| `-32600` | Invalid request |
| `-32601` | Method not found |
| `-32602` | Invalid params |
| `-32603` | Internal error |

Pick custom codes outside `-32768..-32000`: MCP 2026-07-28 reserves
`-32020..-32099` for protocol-defined codes and discourages `-32000..-32019`
for new implementations. A Modern (2026-07-28) client is never sent a foreign
code from that range — the server remaps it to `-32603` and keeps the original
in `data.originalCode`.

## Best Practices

- Write specific messages: `"email must be a valid address"` not `"invalid parameter"`
- Never expose internal error details (stack traces, query strings, file paths) to the client — log them server-side
- Use `NewToolResponseError` (or `NewToolErrorInvalidParams`) for anything the model can fix, and `NewToolErrorInternal` only for server failures

## See Also

- [Response Types Guide](response-types.md) — successful response patterns
