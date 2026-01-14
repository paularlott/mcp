package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/paularlott/mcp"
)

func main() {
	// Create a server
	server := mcp.NewServer("unified-server", "1.0.0")

	// Register a local tool
	server.RegisterTool(
		mcp.NewTool("local-greet", "Local greeting tool",
			mcp.String("name", "Name to greet", mcp.Required()),
		),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			name, _ := req.String("name")
			return mcp.NewToolResponseText(fmt.Sprintf("Local: Hello, %s!", name)), nil
		},
	)

	// Register remote servers
	bearerAuth := mcp.NewBearerTokenAuth("ai-tools-token")
	client := mcp.NewClient("http://127.0.0.1:8000/mcp", bearerAuth, "ai")
	server.RegisterRemoteServer(client)

	// List all tools (local and remote)
	allTools := server.ListTools()
	fmt.Printf("Server has %d tools:\n", len(allTools))
	for _, tool := range allTools {
		fmt.Printf("- %s: %s\n", tool.Name, tool.Description)
	}

	// Call tools through server
	ctx := context.Background()

	// Call local tool (no namespace needed)
	response, err := server.CallTool(ctx, "local-greet", map[string]interface{}{
		"name": "Server User",
	})
	if err != nil {
		log.Printf("Failed to call local tool: %v", err)
	} else {
		fmt.Printf("Local tool response: %+v\n", response)
	}

	// Call remote tool with namespace
	response, err = server.CallTool(ctx, "ai/generate-text", map[string]interface{}{
		"prompt": "Hello world",
	})
	if err != nil {
		log.Printf("Failed to call AI tool: %v", err)
	} else {
		fmt.Printf("AI tool response: %+v\n", response)
	}

	// Start HTTP server
	http.HandleFunc("/mcp", server.HandleRequest)

	fmt.Println("Unified server starting on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
