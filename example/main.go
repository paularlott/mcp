package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/paularlott/mcp"
)

func main() {
	// Create a new MCP server instance
	server := mcp.NewServer("example-mcp-server", "1.0.0")

	// Register a dummy tool using the fluent API
	server.RegisterTool(
		mcp.NewTool("hello", "Say hello to someone").
			AddParam("name", mcp.String, "The name to greet", true).
			AddParam("greeting", mcp.String, "Custom greeting", false).
			AddOutputParam("message", mcp.String, "The greeting message", true),
		func(req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			name, err := req.String("name")
			if err != nil {
				return nil, err
			}
			greeting := req.StringOr("greeting", "Hello")
			// return mcp.NewToolResponseText(fmt.Sprintf("%s, %s!", greeting, name)), nil

			out := interface{}(map[string]interface{}{
				"message": fmt.Sprintf("%s, %s!", greeting, name),
			})

			return mcp.NewToolResponseStructured(out), nil
		},
	)

	server.RegisterTool(
		mcp.NewTool("hello2", "Say hello to someone again").
			AddParam("name", mcp.String, "The name to greet", true).
			AddParam("greeting", mcp.String, "Custom greeting", false),
		func(req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			name, err := req.String("name")
			if err != nil {
				return nil, err
			}
			greeting := req.StringOr("greeting", "Hello")
			return mcp.NewToolResponseText(fmt.Sprintf("%s, %s!", greeting, name)), nil
		},
	)

	// Set up HTTP server
	http.HandleFunc("/mcp", server.HandleRequest)

	fmt.Println("MCP server starting on port 8000...")
	log.Fatal(http.ListenAndServe(":8000", nil))
}
