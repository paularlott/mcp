package openai

import (
	"encoding/json"
	"fmt"

	"github.com/paularlott/mcp"
)

// ExtractToolResult extracts a string result from an MCP ToolResponse
// for use in OpenAI tool result messages.
//
// Priority order:
//  1. StructuredContent - serialized to JSON
//  2. First text content in Content array
//  3. Default success message
func ExtractToolResult(response *mcp.ToolResponse) (string, error) {
	if response == nil {
		return "Tool executed successfully", nil
	}

	// Priority 1: Structured content
	if response.StructuredContent != nil {
		jsonBytes, err := json.Marshal(response.StructuredContent)
		if err != nil {
			return "", fmt.Errorf("failed to serialize structured content: %w", err)
		}
		return string(jsonBytes), nil
	}

	// Priority 2: Text content
	for _, content := range response.Content {
		if content.Type == "text" {
			return content.Text, nil
		}
	}

	return "Tool executed successfully", nil
}

// ExtractAllTextContent extracts all text content from an MCP ToolResponse,
// concatenating multiple text parts with newlines.
func ExtractAllTextContent(response *mcp.ToolResponse) string {
	if response == nil {
		return ""
	}

	var result string
	for _, content := range response.Content {
		if content.Type == "text" {
			if result != "" {
				result += "\n"
			}
			result += content.Text
		}
	}

	return result
}
