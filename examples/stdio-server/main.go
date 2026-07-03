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
	"strings"

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

	// A static resource: a fixed URI the client can list and read verbatim.
	server.RegisterResource(
		mcp.NewResource("config://app", "App Config", "The example application configuration", "application/json"),
		func(ctx context.Context, uri string) (*mcp.ResourceResponse, error) {
			return mcp.NewResourceResponseText(uri, `{"name":"stdio-example","version":"1.0.0","debug":true}`, "application/json"), nil
		},
	)

	// A resource template: a URI with a {name} placeholder the client expands.
	// Reading greeting://Ada resolves to the handler below with that expanded URI.
	server.RegisterResourceTemplate(
		mcp.NewResourceTemplate("greeting://{name}", "Greeting", "A personalized greeting for a name", "text/plain"),
		func(ctx context.Context, uri string) (*mcp.ResourceResponse, error) {
			// The handler receives the fully-expanded URI; parse the variable out.
			name := strings.TrimPrefix(uri, "greeting://")
			return mcp.NewResourceResponseText(uri, "Hello, "+name+"!", "text/plain"), nil
		},
	)

	// A prompt: a reusable message template with arguments. The client fills in
	// the arguments and gets back rendered messages for the model.
	server.RegisterPrompt(
		mcp.NewPrompt("review_request", "Ask the model to review a name").Argument("name", "Name to review", true),
		func(ctx context.Context, req *mcp.PromptRequest) (*mcp.PromptResponse, error) {
			name, _ := req.String("name")
			return mcp.NewPromptResponseText("Please review the name: " + name), nil
		},
	)

	// Blocks until stdin reaches EOF (the host closed the connection).
	if err := server.ServeStdio(context.Background()); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
