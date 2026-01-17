# Error Handling Guide

The library provides structured error handling for common scenarios:

## Standard Error Types

### Invalid Parameters Error

```go
func handleGreet(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
    name, _ := req.String("name")

    if name == "" {
        return nil, mcp.NewToolErrorInvalidParams("name parameter is required")
    }

    return mcp.NewToolResponseText(fmt.Sprintf("Hello, %s!", name)), nil
}
```

Use when:

- Client passes invalid argument types
- Required parameters are missing
- Parameter values fail validation
- Parameter constraints are violated

### Internal Server Error

```go
func handleDatabaseQuery(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
    result, err := database.Query(ctx)

    if err != nil {
        return nil, mcp.NewToolErrorInternal("failed to query database")
    }

    return mcp.NewToolResponseText(result), nil
}
```

Use when:

- Server-side operations fail
- Dependencies are unavailable
- Unexpected errors occur

### Custom Errors

```go
func handleFileUpload(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
    size, _ := req.Int("size")

    if size > maxSize {
        return nil, mcp.NewToolError(-32001, "File too large", map[string]interface{}{
            "max_size": maxSize,
            "provided": size,
            "error_code": "FILE_SIZE_EXCEEDED",
        })
    }

    return mcp.NewToolResponseText("Upload successful"), nil
}
```

Use when:

- Standard errors don't apply
- Need to return error details
- Custom error codes required

## Error Best Practices

### 1. Return Meaningful Error Messages

```go
// Good
return nil, mcp.NewToolErrorInvalidParams("email parameter must be a valid email address")

// Bad
return nil, mcp.NewToolErrorInvalidParams("invalid parameter")
```

### 2. Include Context in Custom Errors

```go
return nil, mcp.NewToolError(-32002, "operation failed", map[string]interface{}{
    "operation": "send_email",
    "recipient": email,
    "error_code": "EMAIL_SERVICE_ERROR",
    "retry_after": 30,
})
```

### 3. Don't Expose Sensitive Information

```go
// Bad - exposes database query details
return nil, mcp.NewToolErrorInternal(fmt.Sprintf("Query failed: %s", err.Error()))

// Good - generic message with logging
log.Printf("Query failed for user %s: %v", userID, err)
return nil, mcp.NewToolErrorInternal("database operation failed")
```

### 4. Use Appropriate Error Types

```go
func validateInput(value string) error {
    if value == "" {
        // Parameter validation error
        return mcp.NewToolErrorInvalidParams("value is required")
    }

    if len(value) > 1000 {
        // Business logic error
        return mcp.NewToolError(-32002, "value exceeds maximum length", map[string]interface{}{
            "max_length": 1000,
            "actual_length": len(value),
        })
    }

    return nil
}
```

### 5. Recovery and Graceful Degradation

```go
func handleRequest(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
    defer func() {
        if r := recover(); r != nil {
            log.Printf("Panic recovered: %v", r)
        }
    }()

    // Your implementation...
    return result, nil
}
```

## Error Code Reference

### Standard MCP Errors

- `-32700`: Parse error
- `-32600`: Invalid request
- `-32601`: Method not found
- `-32602`: Invalid params
- `-32603`: Internal error
- `-32000 to -32099`: Server error (reserved for library)

### Custom Error Codes

Use `-32000` to `-32099` for custom server errors:

```go
const (
    errAuthFailed      = -32001
    errQuotaExceeded   = -32002
    errRateLimited     = -32003
    errServiceUnavail  = -32004
)
```

## Common Error Patterns

### Validation Pipeline

```go
func handleCreateUser(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
    email, _ := req.String("email")
    password, _ := req.String("password")

    // Validate email format
    if !isValidEmail(email) {
        return nil, mcp.NewToolErrorInvalidParams("email is not in valid format")
    }

    // Validate password strength
    if !isStrongPassword(password) {
        return nil, mcp.NewToolErrorInvalidParams("password must be at least 8 characters with mixed case")
    }

    // Check if user exists
    if userExists(email) {
        return nil, mcp.NewToolError(-32002, "user already exists", map[string]interface{}{
            "email": email,
        })
    }

    // Create user
    user, err := createUser(email, password)
    if err != nil {
        return nil, mcp.NewToolErrorInternal("failed to create user")
    }

    return mcp.NewToolResponseStructured(user), nil
}
```

### Timeout Handling

```go
func handleLongOperation(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
    ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
    defer cancel()

    result := make(chan interface{})
    errChan := make(chan error)

    go func() {
        r, err := longOperation(ctx)
        if err != nil {
            errChan <- err
        } else {
            result <- r
        }
    }()

    select {
    case res := <-result:
        return mcp.NewToolResponseStructured(res), nil
    case err := <-errChan:
        return nil, mcp.NewToolErrorInternal(fmt.Sprintf("operation failed: %v", err))
    case <-ctx.Done():
        return nil, mcp.NewToolError(-32003, "operation timeout", map[string]interface{}{
            "timeout_seconds": 30,
        })
    }
}
```

### Dependency Failure Handling

```go
func handleWithFallback(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
    // Try primary service
    result, err := primaryService.Call(ctx)
    if err == nil {
        return mcp.NewToolResponseStructured(result), nil
    }

    log.Printf("Primary service failed: %v", err)

    // Try fallback
    result, err = fallbackService.Call(ctx)
    if err == nil {
        return mcp.NewToolResponseStructured(result), nil
    }

    // All failed
    return nil, mcp.NewToolError(-32004, "all services unavailable", map[string]interface{}{
        "primary_error": err.Error(),
    })
}
```

## Testing Error Handling

```go
func TestErrorHandling(t *testing.T) {
    tests := []struct {
        name          string
        input         string
        expectedCode  int
        expectedMsg   string
    }{
        {
            name:         "empty parameter",
            input:        "",
            expectedCode: -32602,
            expectedMsg:  "name parameter is required",
        },
        {
            name:         "invalid format",
            input:        "invalid@",
            expectedCode: -32002,
            expectedMsg:  "email is not in valid format",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            req := &mcp.ToolRequest{}
            req.Params["input"] = tt.input

            _, err := handleTool(context.Background(), req)

            if toolErr, ok := err.(*mcp.ToolError); ok {
                if toolErr.Code != tt.expectedCode {
                    t.Errorf("expected code %d, got %d", tt.expectedCode, toolErr.Code)
                }
                if toolErr.Message != tt.expectedMsg {
                    t.Errorf("expected message %q, got %q", tt.expectedMsg, toolErr.Message)
                }
            }
        })
    }
}
```

## See Also

- Response Types Guide - For successful responses
- Thread Safety Guide - For concurrency-related errors
