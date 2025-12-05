package openai

import (
	"encoding/json"
	"fmt"
)

// ToolExecutor is a function that executes tool calls and returns the result.
// The function receives the tool name and arguments (as a map) and
// returns the result string and any error.
type ToolExecutor func(name string, arguments map[string]any) (string, error)

// ExecuteToolCalls executes multiple tool calls using the provided executor
// and returns messages containing the results. If a tool call fails, the
// error message is included in the result and the execution continues
// (unless stopOnError is true).
func ExecuteToolCalls(toolCalls []ToolCall, executor ToolExecutor, stopOnError bool) ([]Message, error) {
	if len(toolCalls) == 0 {
		return nil, nil
	}

	results := make([]Message, 0, len(toolCalls))

	for _, tc := range toolCalls {
		result, err := executor(tc.Function.Name, tc.Function.Arguments)

		if err != nil {
			if stopOnError {
				return results, NewToolExecutionError(tc.Function.Name, tc.ID, err)
			}
			// Include error in result for non-fatal errors
			result = fmt.Sprintf("Error: %v", err)
		}

		results = append(results, BuildToolResultMessage(tc.ID, result))
	}

	return results, nil
}

// ExecuteToolCall executes a single tool call using the provided executor.
// Returns a Message with the tool result that can be appended to the conversation.
func ExecuteToolCall(tc ToolCall, executor ToolExecutor) (Message, error) {
	result, err := executor(tc.Function.Name, tc.Function.Arguments)
	if err != nil {
		return Message{}, NewToolExecutionError(tc.Function.Name, tc.ID, err)
	}
	return BuildToolResultMessage(tc.ID, result), nil
}

// BuildToolResultMessage creates a tool result message for the given tool call ID.
func BuildToolResultMessage(toolCallID string, result string) Message {
	return Message{
		Role:       "tool",
		Content:    result,
		ToolCallID: toolCallID,
	}
}

// BuildAssistantToolCallMessage creates an assistant message with tool calls.
// This is useful when reconstructing a conversation that included tool calls.
func BuildAssistantToolCallMessage(content string, toolCalls []ToolCall) Message {
	return Message{
		Role:      "assistant",
		Content:   content,
		ToolCalls: toolCalls,
	}
}

// BuildUserMessage creates a user message with the given content.
func BuildUserMessage(content string) Message {
	return Message{
		Role:    "user",
		Content: content,
	}
}

// BuildSystemMessage creates a system message with the given content.
func BuildSystemMessage(content string) Message {
	return Message{
		Role:    "system",
		Content: content,
	}
}

// BuildAssistantMessage creates an assistant message with the given content.
func BuildAssistantMessage(content string) Message {
	return Message{
		Role:    "assistant",
		Content: content,
	}
}

// ParseToolArguments parses the tool call arguments map into the provided struct.
// The target should be a pointer to the struct.
func ParseToolArguments(arguments map[string]any, target interface{}) error {
	// Convert map to JSON then unmarshal to struct
	data, err := json.Marshal(arguments)
	if err != nil {
		return fmt.Errorf("failed to marshal arguments: %w", err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("failed to parse arguments: %w", err)
	}
	return nil
}

// MustParseToolArguments is like ParseToolArguments but panics on error.
// Use only in situations where you're certain the arguments are valid.
func MustParseToolArguments(arguments map[string]any, target interface{}) {
	if err := ParseToolArguments(arguments, target); err != nil {
		panic(err)
	}
}

// ContentPartHelpers

// TextContentPart creates a ContentPart with text content.
func TextContentPart(text string) ContentPart {
	return ContentPart{
		Type: "text",
		Text: text,
	}
}

// ImageURLContentPart creates a ContentPart with an image URL.
func ImageURLContentPart(url string, detail string) ContentPart {
	cp := ContentPart{
		Type: "image_url",
		ImageURL: &ImageURL{
			URL: url,
		},
	}
	if detail != "" {
		cp.ImageURL.Detail = detail
	}
	return cp
}

// ImageBase64ContentPart creates a ContentPart with a base64-encoded image.
// The mediaType should be something like "image/png" or "image/jpeg".
func ImageBase64ContentPart(base64Data string, mediaType string, detail string) ContentPart {
	url := fmt.Sprintf("data:%s;base64,%s", mediaType, base64Data)
	return ImageURLContentPart(url, detail)
}

// BuildMultimodalMessage creates a user message with multiple content parts.
func BuildMultimodalMessage(parts ...ContentPart) Message {
	return Message{
		Role:    "user",
		Content: parts,
	}
}

// Tool Definition Helpers

// NewTool creates a tool definition for a function.
// The parameters should be a JSON Schema object describing the function parameters.
func NewTool(name, description string, parameters map[string]any) Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        name,
			Description: description,
			Parameters:  parameters,
		},
	}
}

// HasToolCalls returns true if the message contains tool calls.
func HasToolCalls(msg Message) bool {
	return len(msg.ToolCalls) > 0
}

// GetToolNames returns the names of all tools in the provided tool calls.
func GetToolNames(toolCalls []ToolCall) []string {
	names := make([]string, len(toolCalls))
	for i, tc := range toolCalls {
		names[i] = tc.Function.Name
	}
	return names
}
