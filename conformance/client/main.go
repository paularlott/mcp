// Command client is the client under test for the official MCP conformance
// suite (github.com/modelcontextprotocol/conformance). The suite runs it once
// per scenario with the scenario's server URL as the last argument, the
// scenario name in MCP_CONFORMANCE_SCENARIO, the protocol revision in
// MCP_CONFORMANCE_PROTOCOL_VERSION and, for some scenarios, a JSON context in
// MCP_CONFORMANCE_CONTEXT. It drives the library's Client through what the
// scenario expects and exits 0, or 1 with the error on stderr. The suite
// judges the client by what its mock server observes. See ../README.md.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/paularlott/mcp"
)

// scenarioContext is MCP_CONFORMANCE_CONTEXT: extra input some scenarios
// give the client, such as tool calls to make.
type scenarioContext struct {
	Name      string `json:"name"`
	ToolCalls []struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	} `json:"toolCalls"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: client <server-url> (scenario in MCP_CONFORMANCE_SCENARIO)")
		os.Exit(1)
	}
	if err := run(os.Getenv("MCP_CONFORMANCE_SCENARIO"), os.Args[len(os.Args)-1]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(scenario, url string) error {
	var sc scenarioContext
	if raw := os.Getenv("MCP_CONFORMANCE_CONTEXT"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &sc); err != nil {
			return fmt.Errorf("MCP_CONFORMANCE_CONTEXT: %w", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	c := mcp.NewClient(url, nil, "")
	defer c.Close()
	if err := c.Initialize(ctx); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}

	tools, err := c.ListTools(ctx)
	if err != nil {
		return fmt.Errorf("tools/list: %w", err)
	}

	switch scenario {
	case "initialize", "json-schema-ref-no-deref", "request-metadata":
		// Connecting and listing tools is the whole scenario.
		return nil

	case "tools_call", "tools-call":
		return call(ctx, c, "add_numbers", map[string]any{"a": 2, "b": 3})

	case "json-schema-2020-12-preservation":
		// Echo the focal tool's inputSchema back verbatim, so the scenario
		// can see whether the client kept every keyword.
		for _, t := range tools {
			if t.Name == "json_schema_2020_12_tool" {
				return call(ctx, c, "json_schema_echo", map[string]any{"schema": t.InputSchema})
			}
		}
		return fmt.Errorf("json_schema_2020_12_tool not listed")

	case "http-standard-headers":
		return exerciseEveryMethod(ctx, c, tools)

	case "http-invalid-tool-headers":
		// The client must have dropped tools with invalid x-mcp-header
		// annotations from tools/list: call everything it kept.
		for _, t := range tools {
			if err := call(ctx, c, t.Name, map[string]any{}); err != nil {
				return err
			}
		}
		return nil
	}

	// Scenarios that hand the client explicit tool calls.
	if len(sc.ToolCalls) > 0 {
		for _, tc := range sc.ToolCalls {
			if err := call(ctx, c, tc.Name, tc.Arguments); err != nil {
				return err
			}
		}
		return nil
	}

	// Anything else: call the first tool, as the reference client does.
	if len(tools) > 0 {
		return call(ctx, c, tools[0].Name, map[string]any{})
	}
	return nil
}

// call calls a tool and fails on a protocol error; a tool execution error
// (isError) is a result, not a client failure.
func call(ctx context.Context, c *mcp.Client, name string, args map[string]any) error {
	if _, err := c.CallTool(ctx, name, args); err != nil {
		return fmt.Errorf("tools/call %s: %w", name, err)
	}
	return nil
}

// exerciseEveryMethod sends one request of each kind the standard-headers
// scenario checks (initialize and tools/list have already gone out).
func exerciseEveryMethod(ctx context.Context, c *mcp.Client, tools []mcp.MCPTool) error {
	if len(tools) > 0 {
		if err := call(ctx, c, tools[0].Name, map[string]any{}); err != nil {
			return err
		}
	}
	if resources, err := c.ListResources(ctx); err != nil {
		return fmt.Errorf("resources/list: %w", err)
	} else if len(resources) > 0 {
		if _, err := c.ReadResource(ctx, resources[0].URI); err != nil {
			return fmt.Errorf("resources/read: %w", err)
		}
	}
	if prompts, err := c.ListPrompts(ctx); err != nil {
		return fmt.Errorf("prompts/list: %w", err)
	} else if len(prompts) > 0 {
		if _, err := c.GetPrompt(ctx, prompts[0].Name, nil); err != nil {
			return fmt.Errorf("prompts/get: %w", err)
		}
	}
	return nil
}
