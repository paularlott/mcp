package main

import (
	"context"
	"fmt"
	"log"

	"github.com/paularlott/mcp"
)

func main() {
	// Example 1: Using a client with bearer token auth
	auth := mcp.NewBearerTokenAuth("your-token-here")
	client := mcp.NewClient("http://127.0.0.1:8000/mcp", auth)

	ctx := context.Background()

	// List available tools
	tools, err := client.ListTools(ctx)
	if err != nil {
		log.Printf("Failed to list tools: %v", err)
		return
	}

	fmt.Printf("Available tools: %d\n", len(tools))
	for _, tool := range tools {
		fmt.Printf("- %s: %s\n", tool.Name, tool.Description)
	}

	// Call a tool
	args := map[string]interface{}{
		"name":     "World",
		"greeting": "Hello",
	}

	response, err := client.CallTool(ctx, "greet", args)
	if err != nil {
		log.Printf("Failed to call tool: %v", err)
		return
	}

	fmt.Printf("Tool response: %+v\n", response)
}
