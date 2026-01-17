// Package main demonstrates per-user tool access using ToolProvider.
//
// This example shows how to expose different tools to different users based on
// their authentication token. This is useful for multi-tenant or role-based
// access control scenarios.
//
// Test with:
//
//	# Alice's tools (has admin access)
//	curl -X POST http://localhost:8080/mcp \
//	  -H "Content-Type: application/json" \
//	  -H "Authorization: Bearer alice-secret-token" \
//	  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
//
//	# Bob's tools (regular user)
//	curl -X POST http://localhost:8080/mcp \
//	  -H "Content-Type: application/json" \
//	  -H "Authorization: Bearer bob-secret-token" \
//	  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
//
//	# Call Alice's admin tool
//	curl -X POST http://localhost:8080/mcp \
//	  -H "Content-Type: application/json" \
//	  -H "Authorization: Bearer alice-secret-token" \
//	  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"admin_users","arguments":{}}}'
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/paularlott/mcp"
)

// User represents an authenticated user
type User struct {
	ID    string
	Name  string
	Roles []string
}

// Hardcoded users for demonstration
var users = map[string]*User{
	"alice-secret-token": {ID: "1", Name: "Alice", Roles: []string{"admin", "user"}},
	"bob-secret-token":   {ID: "2", Name: "Bob", Roles: []string{"user"}},
}

// UserToolProvider provides tools based on the authenticated user's roles
type UserToolProvider struct {
	user *User
}

// NewUserToolProvider creates a provider for the given user
func NewUserToolProvider(user *User) *UserToolProvider {
	return &UserToolProvider{user: user}
}

// GetTools returns tools available to this user based on their roles
func (p *UserToolProvider) GetTools(ctx context.Context) ([]mcp.MCPTool, error) {
	var tools []mcp.MCPTool

	// All users get the profile tool
	tools = append(tools, mcp.MCPTool{
		Name:        "get_profile",
		Description: "Get the current user's profile information",
		Keywords:    []string{"user", "profile", "me"},
		InputSchema: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"additionalProperties": false,
		},
	})

	// Check if user has admin role
	for _, role := range p.user.Roles {
		if role == "admin" {
			// Admin-only tools
			tools = append(tools, mcp.MCPTool{
				Name:        "admin_users",
				Description: "List all users in the system (admin only)",
				Keywords:    []string{"admin", "users", "list"},
				InputSchema: map[string]interface{}{
					"type":                 "object",
					"properties":           map[string]interface{}{},
					"additionalProperties": false,
				},
			})
			tools = append(tools, mcp.MCPTool{
				Name:        "admin_delete_user",
				Description: "Delete a user from the system (admin only)",
				Keywords:    []string{"admin", "delete", "user"},
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"user_id": map[string]interface{}{
							"type":        "string",
							"description": "ID of the user to delete",
						},
					},
					"required":             []string{"user_id"},
					"additionalProperties": false,
				},
			})
			break
		}
	}

	return tools, nil
}

// ExecuteTool executes a tool for this user
func (p *UserToolProvider) ExecuteTool(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
	switch name {
	case "get_profile":
		return map[string]interface{}{
			"id":    p.user.ID,
			"name":  p.user.Name,
			"roles": p.user.Roles,
		}, nil

	case "admin_users":
		// Verify admin access
		if !p.hasRole("admin") {
			return nil, fmt.Errorf("access denied: admin role required")
		}
		var userList []map[string]interface{}
		for _, u := range users {
			userList = append(userList, map[string]interface{}{
				"id":    u.ID,
				"name":  u.Name,
				"roles": u.Roles,
			})
		}
		return userList, nil

	case "admin_delete_user":
		// Verify admin access
		if !p.hasRole("admin") {
			return nil, fmt.Errorf("access denied: admin role required")
		}
		userID, _ := params["user_id"].(string)
		return map[string]interface{}{
			"status":  "deleted",
			"user_id": userID,
			"message": "User would be deleted (demo mode)",
		}, nil
	}

	return nil, mcp.ErrUnknownTool
}

func (p *UserToolProvider) hasRole(role string) bool {
	for _, r := range p.user.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// extractBearerToken extracts the token from Authorization header
func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

func main() {
	// Create MCP server
	server := mcp.NewServer("per-user-tools", "1.0.0")
	server.SetInstructions("This server provides tools based on the authenticated user's roles. Admin users have access to user management tools.")

	// Register a shared tool available to everyone
	server.RegisterTool(
		mcp.NewTool("ping", "Simple ping to check server status"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("pong"), nil
		},
	)

	// Create authenticated handler
	handler := func(w http.ResponseWriter, r *http.Request) {
		// Extract and validate token
		token := extractBearerToken(r)
		if token == "" {
			http.Error(w, "Authorization header required", http.StatusUnauthorized)
			return
		}

		user, exists := users[token]
		if !exists {
			http.Error(w, "Invalid token", http.StatusUnauthorized)
			return
		}

		// Create user-specific provider
		provider := NewUserToolProvider(user)

		// Add provider to context (normal mode: all tools visible in tools/list)
		ctx := mcp.WithToolProviders(r.Context(), provider)

		// Handle MCP request
		server.HandleRequest(w, r.WithContext(ctx))
	}

	// Start server
	log.Println("Starting per-user-tools server on :8080")
	log.Println("Test users:")
	log.Println("  Alice (admin): Bearer alice-secret-token")
	log.Println("  Bob (user):    Bearer bob-secret-token")
	log.Fatal(http.ListenAndServe(":8080", http.HandlerFunc(handler)))
}
