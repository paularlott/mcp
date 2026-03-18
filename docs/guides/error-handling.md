# Error Handling

## Error Types

### Invalid Parameters

Use when a required parameter is missing, the wrong type, or fails validation:

```go
func handleGreet(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
    name, _ := req.String("name")
    if name == "" {
        return nil, mcp.NewToolErrorInvalidParams("name parameter is required")
    }
    return mcp.NewToolResponseText(fmt.Sprintf("Hello, %s!", name)), nil
}
```

### Internal Server Error

Use when a server-side operation fails:

```go
result, err := database.Query(ctx)
if err != nil {
    log.Printf("query failed: %v", err) // log the detail
    return nil, mcp.NewToolErrorInternal("database operation failed") // don't expose internals
}
```

### Custom Errors

Use when you need a specific error code or structured detail:

```go
return nil, mcp.NewToolError(-32001, "file too large", map[string]any{
    "max_size": maxSize,
    "provided": size,
})
```

## Error Code Reference

Standard JSON-RPC codes used by the library:

| Code | Meaning |
|---|---|
| `-32700` | Parse error |
| `-32600` | Invalid request |
| `-32601` | Method not found |
| `-32602` | Invalid params |
| `-32603` | Internal error |

Use `-32001` to `-32099` for your own custom server errors.

## Best Practices

- Write specific messages: `"email must be a valid address"` not `"invalid parameter"`
- Never expose internal error details (stack traces, query strings, file paths) to the client — log them server-side
- Use `NewToolErrorInvalidParams` for client mistakes, `NewToolErrorInternal` for server failures, custom codes for domain errors

## See Also

- [Response Types Guide](response-types.md) — successful response patterns
