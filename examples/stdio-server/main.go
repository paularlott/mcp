// Command stdio-server is an MCP server that speaks the protocol over
// stdin/stdout (newline-delimited JSON-RPC 2.0). This is the transport a host
// launches as a subprocess.
//
// Run it directly and pipe a request in:
//
//	echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' | go run ./examples/stdio-server
//
// Or drive it from the stdio-client example.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/paularlott/mcp"
)

func main() {
	// Logs must go to stderr: stdout carries the protocol stream.
	log.SetOutput(os.Stderr)

	server := mcp.NewServer("stdio-example", "1.0.0")
	server.SetInstructions("A tiny example MCP server served over stdio.")

	server.RegisterTool(
		mcp.NewTool("greet", "Greet someone",
			mcp.String("name", "Name to greet", mcp.Required()),
			mcp.String("greeting", "Custom greeting"),
		),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			name, _ := req.String("name")
			greeting := req.StringOr("greeting", "Hello")
			return mcp.NewToolResponseText(fmt.Sprintf("%s, %s!", greeting, name)), nil
		},
	)

	// Blocks until stdin reaches EOF (the host closed the connection).
	if err := server.ServeStdio(context.Background()); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
