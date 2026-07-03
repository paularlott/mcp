// Command stdio-client launches an MCP stdio server as a subprocess and talks
// to it over the child's stdin/stdout.
//
// Run it against the stdio-server example:
//
//	go build -o /tmp/mcp-stdio-server ./examples/stdio-server
//	go run ./examples/stdio-client /tmp/mcp-stdio-server
//
// With no argument it launches the sibling example via `go run`.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/paularlott/mcp"
)

func main() {
	command := "go"
	args := []string{"run", "github.com/paularlott/mcp/examples/stdio-server"}
	if len(os.Args) > 1 {
		command = os.Args[1]
		args = os.Args[2:]
	}

	// Launch the server subprocess. Its stderr is inherited so its logs show;
	// stdin/stdout carry the MCP protocol. Empty namespace = tool names as-is.
	client, err := mcp.NewStdioClient(command, args, "", mcp.WithClientStderr(os.Stderr))
	if err != nil {
		log.Fatalf("spawn server: %v", err)
	}
	defer client.Close()

	ctx := context.Background()

	tools, err := client.ListTools(ctx)
	if err != nil {
		log.Fatalf("list tools: %v", err)
	}
	fmt.Printf("Available tools: %d\n", len(tools))
	for _, tool := range tools {
		fmt.Printf("- %s: %s\n", tool.Name, tool.Description)
	}

	resp, err := client.CallTool(ctx, "greet", map[string]any{"name": "Ada"})
	if err != nil {
		log.Fatalf("call tool: %v", err)
	}
	if len(resp.Content) > 0 {
		fmt.Println("greet ->", resp.Content[0].Text)
	}
}
