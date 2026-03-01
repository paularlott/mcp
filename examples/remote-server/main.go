package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/paularlott/mcp"
)

func main() {
	server := mcp.NewServer("remote-server-example", "1.0.0")

	// =========================================================================
	// 1. Native local tool (always visible in tools/list)
	// =========================================================================

	server.RegisterTool(
		mcp.NewTool("local-status", "Get the local server status"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("Local server is running"), nil
		},
	)

	// =========================================================================
	// 2. Discoverable local tool (only visible via tool_search)
	// =========================================================================

	server.RegisterTool(
		mcp.NewTool("local-diagnostics", "Run local diagnostics",
			mcp.Boolean("verbose", "Include detailed output"),
		).Discoverable("diagnostics", "debug", "health", "local"),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			verbose := req.BoolOr("verbose", false)
			result := "Diagnostics: all systems OK"
			if verbose {
				result += "\n- Memory: OK\n- Disk: OK\n- Network: OK"
			}
			return mcp.NewToolResponseText(result), nil
		},
	)

	// =========================================================================
	// 3. Remote server with a tool filter (read-only tools only)
	// =========================================================================

	// WithToolFilter receives the tool name WITHOUT the namespace prefix,
	// e.g. "generate-text" not "ai/generate-text".
	aiClient := mcp.NewClient("http://127.0.0.1:8000/mcp", mcp.NewBearerTokenAuth("ai-tools-token"), "ai").
		WithToolFilter(func(toolName string) bool {
			return strings.HasPrefix(toolName, "get-") ||
				strings.HasPrefix(toolName, "list-") ||
				strings.HasPrefix(toolName, "search-")
		})

	server.RegisterRemoteServer(aiClient)

	// =========================================================================
	// 4. Remote server with all tools discoverable and filtered
	// =========================================================================

	// RegisterRemoteServerDiscoverable makes all tools from this server discoverable
	// (not in tools/list, only findable via tool_search).
	// WithToolFilter still applies — tool name is WITHOUT the namespace prefix.
	dataClient := mcp.NewClient("http://127.0.0.1:8001/mcp", mcp.NewBearerTokenAuth("data-tools-token"), "data").
		WithToolFilter(func(toolName string) bool {
			return toolName != "drop-database" && toolName != "delete-all"
		})

	server.RegisterRemoteServerDiscoverable(dataClient)

	// =========================================================================
	// 4. Start HTTP server
	// =========================================================================

	http.HandleFunc("/mcp", server.HandleRequest)

	tools := server.ListTools()
	fmt.Printf("Server starting with %d visible tools:\n", len(tools))
	for _, t := range tools {
		fmt.Printf("  - %s: %s\n", t.Name, t.Description)
	}
	fmt.Println("\nDiscoverable tools available via tool_search")
	fmt.Println("Listening on http://localhost:8080/mcp")

	log.Fatal(http.ListenAndServe(":8080", nil))
}
