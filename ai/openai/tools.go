package openai

import (
	"github.com/paularlott/mcp"
)

// MCPToolsToOpenAI converts MCP tools to OpenAI function calling format
func MCPToolsToOpenAI(tools []mcp.MCPTool) []Tool {
	return MCPToolsToOpenAIFiltered(tools, nil)
}

// MCPToolsToOpenAIFiltered converts MCP tools to OpenAI format with optional filtering.
// If filter is nil, all tools are included. Otherwise, only tools where filter(name) returns true are included.
func MCPToolsToOpenAIFiltered(tools []mcp.MCPTool, filter func(name string) bool) []Tool {
	var openAITools []Tool

	for _, tool := range tools {
		// Apply filter if provided
		if filter != nil && !filter(tool.Name) {
			continue
		}

		var parameters map[string]any
		if tool.InputSchema != nil {
			if params, ok := tool.InputSchema.(map[string]any); ok {
				parameters = params
			} else {
				parameters = make(map[string]any)
			}
		} else {
			parameters = make(map[string]any)
		}

		openAITools = append(openAITools, Tool{
			Type: "function",
			Function: ToolFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  parameters,
			},
		})
	}

	return openAITools
}

// ToolFilter is a function type for filtering tools by name
type ToolFilter func(name string) bool

// AllTools returns a filter that includes all tools
func AllTools() ToolFilter {
	return nil
}

// ToolsByName returns a filter that includes only tools with the specified names
func ToolsByName(names ...string) ToolFilter {
	nameSet := make(map[string]bool)
	for _, name := range names {
		nameSet[name] = true
	}
	return func(name string) bool {
		return nameSet[name]
	}
}

// ExcludeTools returns a filter that excludes tools with the specified names
func ExcludeTools(names ...string) ToolFilter {
	nameSet := make(map[string]bool)
	for _, name := range names {
		nameSet[name] = true
	}
	return func(name string) bool {
		return !nameSet[name]
	}
}
