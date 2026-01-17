# Thread Safety and Concurrency Guide

The MCP library is designed for safe concurrent usage. This guide covers thread safety guarantees and best practices.

## Thread Safety Guarantees

### Safe for Concurrent Access

The following are safe to call concurrently from multiple goroutines:

- **`server.HandleRequest()`**: Designed for concurrent HTTP requests
- **`server.ListTools()`**: Thread-safe read operations
- **`server.CallTool()`**: Thread-safe tool execution
- **`ToolProvider.GetTools()`**: Called per-request, safe to access any data
- **`ToolProvider.ExecuteTool()`**: Called per-request, safe to access any data

### Server Initialization

Initialize the server **once** before handling requests:

```go
// ✅ Correct: Initialize once
server := mcp.NewServer("my-server", "1.0.0")
server.RegisterTool(tool1, handler1)
server.RegisterTool(tool2, handler2)

// Use in HTTP handler
http.HandleFunc("/mcp", server.HandleRequest)
```

```go
// ❌ Wrong: Don't reinitialize in handler
http.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
    server := mcp.NewServer("my-server", "1.0.0")  // DON'T DO THIS
    server.HandleRequest(w, r)
})
```

## Safe Concurrent Patterns

### Multi-Goroutine Tool Handlers

Tools can safely call other functions and access shared data with proper synchronization:

```go
type DataStore struct {
    mu    sync.RWMutex
    items map[string]interface{}
}

func (ds *DataStore) Get(key string) interface{} {
    ds.mu.RLock()
    defer ds.mu.RUnlock()
    return ds.items[key]
}

func (ds *DataStore) Set(key string, value interface{}) {
    ds.mu.Lock()
    defer ds.mu.Unlock()
    ds.items[key] = value
}

// Tool handler - safe to call concurrently
func handleGetData(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
    key, _ := req.String("key")
    value := dataStore.Get(key)  // Thread-safe
    return mcp.NewToolResponseStructured(value), nil
}
```

### Context-Based Isolation

Each request gets its own context, allowing safe per-request state:

```go
type PerRequestData struct {
    userID string
    sessionID string
}

func handler(w http.ResponseWriter, r *http.Request) {
    // Extract user info
    userID := getAuthenticatedUserID(r)

    // Create per-request context
    ctx := context.WithValue(r.Context(), "user_id", userID)

    // Create per-user provider
    provider := NewUserProvider(userID)

    // Create per-request context
    ctx = mcp.WithToolProviders(ctx, provider)

    // Handle request - all isolated per request
    server.HandleRequest(w, r.WithContext(ctx))
}
```

### Safe Provider Implementation

Providers receive per-request contexts, enabling safe multi-tenant usage:

```go
type TenantProvider struct {
    tenantID string
    db       *Database  // Shared connection pool, safe for concurrent access
}

func (tp *TenantProvider) GetTools(ctx context.Context) ([]mcp.MCPTool, error) {
    // ctx is request-specific, safe to use for per-request isolation
    tools := tp.db.GetTenantTools(ctx, tp.tenantID)  // Database handles concurrency
    return tools, nil
}

func (tp *TenantProvider) ExecuteTool(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
    // ctx is request-specific
    return tp.db.ExecuteToolForTenant(ctx, tp.tenantID, name, params), nil
}

// In HTTP handler
http.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
    tenantID := getTenantID(r)

    // Create fresh provider per request - safe from concurrent access
    provider := &TenantProvider{
        tenantID: tenantID,
        db:       sharedDatabase,  // Safe: database handles concurrency
    }

    ctx := mcp.WithToolProviders(r.Context(), provider)
    server.HandleRequest(w, r.WithContext(ctx))
})
```

## Unsafe Patterns (Avoid)

### ❌ Modifying Server State in Handlers

```go
// WRONG: Don't modify server state from handler
func handleRequest(w http.ResponseWriter, r *http.Request) {
    server.RegisterTool(...)  // ❌ Race condition
    server.HandleRequest(w, r)
}
```

### ❌ Sharing Mutable State Without Locks

```go
// WRONG: Shared map without synchronization
var cache = make(map[string]interface{})

func handleTool(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
    result := cache[req.Name]  // ❌ Race condition
    return mcp.NewToolResponseStructured(result), nil
}
```

### ❌ Blocking in Tool Handlers

```go
// WRONG: Long-running operation blocks goroutine
func handleTool(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
    time.Sleep(30 * time.Second)  // ❌ Blocks handler goroutine
    return result, nil
}

// RIGHT: Use goroutine with context
func handleTool(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
    ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()

    result := make(chan interface{})
    go func() {
        // Long operation in separate goroutine
        result <- expensiveOperation(ctx)
    }()

    select {
    case res := <-result:
        return mcp.NewToolResponseStructured(res), nil
    case <-ctx.Done():
        return nil, mcp.NewToolErrorInternal("timeout")
    }
}
```

## Database Connection Pooling

Use connection pools for thread-safe database access:

```go
type DatabaseProvider struct {
    pool *sql.DB  // Connection pool (thread-safe)
}

func (dp *DatabaseProvider) ExecuteTool(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
    // sql.DB is thread-safe, handles connection pooling
    rows, err := dp.pool.QueryContext(ctx, query, args...)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    // Safe to use concurrently
    return processRows(rows), nil
}
```

## Recommended Patterns

### 1. Shared Resources with RWMutex

```go
type CachedProvider struct {
    mu    sync.RWMutex
    cache map[string]interface{}
}

func (cp *CachedProvider) Get(key string) interface{} {
    cp.mu.RLock()
    defer cp.mu.RUnlock()
    return cp.cache[key]
}

func (cp *CachedProvider) Set(key string, value interface{}) {
    cp.mu.Lock()
    defer cp.mu.Unlock()
    cp.cache[key] = value
}
```

### 2. Request-Scoped Data with Context

```go
type RequestData struct {
    UserID string
    Token  string
}

func middleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ctx := context.WithValue(r.Context(), "request_data", &RequestData{
            UserID: extractUserID(r),
            Token:  extractToken(r),
        })
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

### 3. Connection Pooling

```go
var db *sql.DB

func init() {
    var err error
    db, err = sql.Open("postgres", "...")
    if err != nil {
        log.Fatal(err)
    }

    // Configure pool
    db.SetMaxOpenConns(25)
    db.SetMaxIdleConns(5)
    db.SetConnMaxLifetime(5 * time.Minute)
}

func handler(w http.ResponseWriter, r *http.Request) {
    // db is thread-safe, handles concurrency
    row := db.QueryRowContext(r.Context(), query)
}
```

## Testing Concurrent Behavior

```go
func TestConcurrentToolExecution(t *testing.T) {
    server := mcp.NewServer("test", "1.0.0")
    server.RegisterTool(
        mcp.NewTool("test-tool", "Test tool"),
        func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
            return mcp.NewToolResponseText("result"), nil
        },
    )

    // Run concurrent calls
    var wg sync.WaitGroup
    errors := make(chan error, 100)

    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()

            ctx := context.Background()
            _, err := server.CallTool(ctx, "test-tool", map[string]interface{}{})
            if err != nil {
                errors <- err
            }
        }()
    }

    wg.Wait()
    close(errors)

    // Check for errors
    for err := range errors {
        t.Errorf("concurrent call failed: %v", err)
    }
}
```

## Performance Considerations

### Goroutine Per Request

The library creates one goroutine per request, which is standard for web servers:

```go
// HTTP server handles this - one goroutine per request
http.HandleFunc("/mcp", server.HandleRequest)  // Efficient, scales well
```

### Memory Usage

- Per-request data: ~1KB overhead per request
- Server state: Constant, doesn't grow with requests
- Providers: Created per-request, garbage collected after

### Scalability

Tested with 1000+ concurrent connections. Performance scales linearly with available CPU.

## See Also

- Context documentation for per-request isolation
- Tool Providers Guide - Safe provider implementation
- Error Handling Guide - Safe error handling patterns
