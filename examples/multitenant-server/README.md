# Multi-Tenant MCP Server Example

This example demonstrates how to implement multi-tenant authentication in an MCP server using bearer tokens and context propagation.

## Architecture

- Authentication is handled via standard HTTP middleware
- Bearer tokens are extracted from the `Authorization` header
- Tenant and user information is added to the request context
- Tool handlers extract auth context to scope their operations

## Key Components

### 1. Auth Middleware (`AuthMiddleware`)
- Extracts bearer token from `Authorization: Bearer <token>` header
- Looks up tenant and user information (simulated in this example)
- Adds auth context to the request

### 2. Tool Handlers
- `greetingToolHandler`: Demonstrates extracting auth context and including it in responses
- `tenantScopedDataToolHandler`: Shows enforcing authentication and scoping data access by tenant

## Running the Example

```bash
cd examples/multitenant-server
go run main.go
```

The server will start on port 8000.

## Testing

### 1. Test without authentication
```bash
curl -X POST http://localhost:8000/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "tools/call",
    "params": {
      "name": "greet",
      "arguments": {
        "name": "World"
      }
    }
  }'
```

**Expected response:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "Hello, World! (unauthenticated request)"
      }
    ],
    "isError": false
  }
}
```

### 2. Test with authentication
```bash
curl -X POST http://localhost:8000/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer acme:john" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "tools/call",
    "params": {
      "name": "greet",
      "arguments": {
        "name": "World"
      }
    }
  }'
```

**Expected response:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "Hello, World! [Authenticated as user 'john' in tenant 'acme' with token 'acme:john']"
      }
    ],
    "isError": false
  }
}
```

### 3. Test tenant-scoped data (requires auth)
```bash
curl -X POST http://localhost:8000/mcp \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer techcorp:jane" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "tools/call",
    "params": {
      "name": "get_data",
      "arguments": {
        "type": "orders"
      }
    }
  }'
```

**Expected response:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "Fetching orders data for tenant 'techcorp' (requested by user 'jane')"
      }
    ],
    "isError": false
  }
}
```

### 4. Test tenant-scoped data without auth (should fail)
```bash
curl -X POST http://localhost:8000/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "tools/call",
    "params": {
      "name": "get_data",
      "arguments": {
        "type": "customers"
      }
    }
  }'
```

**Expected error response:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": -32001,
    "message": "Authentication required",
    "data": {
      "reason": "This tool requires a valid bearer token"
    }
  }
}
```

## Token Format

In this example, tokens use a simple format: `tenant:user`

Examples:
- `acme:john` - User "john" in tenant "acme"
- `techcorp:jane` - User "jane" in tenant "techcorp"

## See Also

- [Main MCP Library Documentation](../../README.md)
- [Simple Server Example](../server/main.go)
- [Client Example](../client/main.go)
