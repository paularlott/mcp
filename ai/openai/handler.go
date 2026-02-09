package openai

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"
)

// ToolHandler receives events during tool processing.
// Implement this interface to receive notifications when tools are called
// and when results are received.
type ToolHandler interface {
	// OnToolCall is called when a tool call is about to be executed.
	OnToolCall(toolCall ToolCall) error

	// OnToolResult is called when a tool call has completed.
	OnToolResult(toolCallID, toolName, result string) error
}

type toolHandlerKey struct{}

// WithToolHandler attaches a ToolHandler to the context.
// The handler will receive events during tool processing.
func WithToolHandler(ctx context.Context, h ToolHandler) context.Context {
	return context.WithValue(ctx, toolHandlerKey{}, h)
}

// ToolHandlerFromContext retrieves a ToolHandler from the context.
// Returns nil if no handler is attached.
func ToolHandlerFromContext(ctx context.Context) ToolHandler {
	if v := ctx.Value(toolHandlerKey{}); v != nil {
		if th, ok := v.(ToolHandler); ok {
			return th
		}
	}
	return nil
}

// GenerateToolCallID creates a unique ID for tool calls.
// This is useful when LLMs don't provide an ID in streaming responses.
// The format matches OpenAI's tool call ID format: "call_" followed by random characters.
func GenerateToolCallID(index int) string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID
		return fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), index)
	}
	return fmt.Sprintf("call_%x", b)
}

// NoOpToolHandler is a ToolHandler that does nothing.
// Useful as a default or for testing.
type NoOpToolHandler struct{}

func (NoOpToolHandler) OnToolCall(toolCall ToolCall) error {
	return nil
}

func (NoOpToolHandler) OnToolResult(toolCallID, toolName, result string) error {
	return nil
}

// Ensure NoOpToolHandler implements ToolHandler
var _ ToolHandler = NoOpToolHandler{}
