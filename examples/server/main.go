package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/paularlott/mcp"
)

func main() {
	// Create server
	server := mcp.NewServer("my-server", "1.0.0")

	// Register a tool
	server.RegisterTool(
		mcp.NewTool("greet", "Greet someone").
			AddParam("name", mcp.String, "Name to greet", true).
			AddParam("greeting", mcp.String, "Custom greeting", false),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			name, _ := req.String("name")
			greeting := req.StringOr("greeting", "Hello")
			return mcp.NewToolResponseText(fmt.Sprintf("%s, %s!", greeting, name)), nil
		},
	)

	// Start server
	http.HandleFunc("/mcp", server.HandleRequest)
	fmt.Println("Server starting on :8000")
	log.Fatal(http.ListenAndServe(":8000", nil))
}
