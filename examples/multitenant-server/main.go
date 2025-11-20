package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/paularlott/mcp"
)

// Context keys for storing auth information
type contextKey string

const (
	contextKeyBearerToken contextKey = "bearer_token"
	contextKeyTenant      contextKey = "tenant"
	contextKeyUser        contextKey = "user"
)

// AuthMiddleware extracts bearer token and adds auth context
func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract bearer token from Authorization header
		authHeader := r.Header.Get("Authorization")
		var token string

		if authHeader != "" {
			// Expected format: "Bearer <token>"
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
				token = parts[1]
			}
		}

		// Add token to context (even if empty)
		ctx := context.WithValue(r.Context(), contextKeyBearerToken, token)

		// In a real implementation, you would:
		// 1. Validate the token
		// 2. Lookup user/tenant from database or cache
		// 3. Handle token expiration
		// For this example, we'll simulate tenant/user lookup
		if token != "" {
			tenant, user := lookupTenantAndUser(token)
			ctx = context.WithValue(ctx, contextKeyTenant, tenant)
			ctx = context.WithValue(ctx, contextKeyUser, user)
		}

		// Continue with the enriched context
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// lookupTenantAndUser simulates looking up tenant and user info from a token
// In production, this would query your database or validate JWT claims
func lookupTenantAndUser(token string) (tenant, user string) {
	// Simulate token validation and lookup
	// In reality, you'd:
	// - Validate JWT signature
	// - Check expiration
	// - Query database for user/tenant
	// - Check permissions

	// For demo purposes, we'll just parse a simple format: "tenant:user"
	// Example token: "acme:john" or "techcorp:jane"
	parts := strings.SplitN(token, ":", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "unknown", "anonymous"
}

// greetingToolHandler demonstrates extracting auth context in a tool handler
func greetingToolHandler(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
	// Extract parameters
	name, err := req.String("name")
	if err != nil {
		return nil, err
	}
	greeting := req.StringOr("greeting", "Hello")

	// Extract authentication context
	token, _ := ctx.Value(contextKeyBearerToken).(string)
	tenant, _ := ctx.Value(contextKeyTenant).(string)
	user, _ := ctx.Value(contextKeyUser).(string)

	// Build response including auth context
	var responseText string
	if token == "" {
		responseText = fmt.Sprintf("%s, %s! (unauthenticated request)", greeting, name)
	} else {
		responseText = fmt.Sprintf(
			"%s, %s! [Authenticated as user '%s' in tenant '%s' with token '%s']",
			greeting, name, user, tenant, token,
		)
	}

	// In a real application, you would:
	// - Check if tenant/user have permission to use this tool
	// - Scope data access to the tenant
	// - Log the action with tenant/user context
	// - Apply rate limits per tenant

	return mcp.NewToolResponseText(responseText), nil
}

// tenantScopedDataToolHandler demonstrates a tool that uses tenant context for data scoping
func tenantScopedDataToolHandler(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
	// Extract tenant context
	tenant, _ := ctx.Value(contextKeyTenant).(string)
	user, _ := ctx.Value(contextKeyUser).(string)

	// Check if authenticated
	if tenant == "" || tenant == "unknown" {
		return nil, mcp.NewToolError(
			-32001, // Custom error code for unauthorized (within implementation-specific range)
			"Authentication required",
			map[string]interface{}{
				"reason": "This tool requires a valid bearer token",
			},
		)
	}

	// Extract parameters
	dataType := req.StringOr("type", "customers")

	// In a real application, you would query data scoped to the tenant
	// Example: SELECT * FROM customers WHERE tenant_id = ?
	responseText := fmt.Sprintf(
		"Fetching %s data for tenant '%s' (requested by user '%s')",
		dataType, tenant, user,
	)

	// Simulate returning tenant-scoped data
	return mcp.NewToolResponseText(responseText), nil
}

func main() {
	// Create MCP server
	server := mcp.NewServer("multitenant-server", "1.0.0")
	server.SetInstructions("This server demonstrates multi-tenant authentication with bearer tokens")

	// Register greeting tool
	server.RegisterTool(
		mcp.NewTool("greet", "Greet someone and show authentication context",
			mcp.String("name", "Name to greet", mcp.Required()),
			mcp.String("greeting", "Custom greeting (optional)"),
		),
		greetingToolHandler,
	)

	// Register tenant-scoped data tool
	server.RegisterTool(
		mcp.NewTool("get_data", "Get tenant-scoped data (requires authentication)",
			mcp.String("type", "Type of data to fetch (e.g., customers, orders)"),
		),
		tenantScopedDataToolHandler,
	)

	// Wrap the server handler with auth middleware
	handler := AuthMiddleware(http.HandlerFunc(server.HandleRequest))

	// Start server
	http.Handle("/mcp", handler)

	fmt.Println("Multi-tenant MCP Server starting on :8000")
	fmt.Println("\nExample usage:")
	fmt.Println("  Without auth:")
	fmt.Println(`    curl -X POST http://localhost:8000/mcp -H "Content-Type: application/json" -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"greet","arguments":{"name":"World"}}}'`)
	fmt.Println("\n  With auth:")
	fmt.Println(`    curl -X POST http://localhost:8000/mcp -H "Content-Type: application/json" -H "Authorization: Bearer acme:john" -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"greet","arguments":{"name":"World"}}}'`)
	fmt.Println("\n  Tenant-scoped data (requires auth):")
	fmt.Println(`    curl -X POST http://localhost:8000/mcp -H "Content-Type: application/json" -H "Authorization: Bearer techcorp:jane" -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_data","arguments":{"type":"orders"}}}'`)
	fmt.Println()

	log.Fatal(http.ListenAndServe(":8000", nil))
}
