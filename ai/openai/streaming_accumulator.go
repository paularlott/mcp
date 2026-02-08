package openai

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// StreamingToolCallAccumulator handles the complex task of accumulating
// streaming tool call deltas into complete ToolCall objects.
// It buffers arguments that come in chunks, generates IDs when missing,
// and parses the final JSON arguments.
//
// Usage:
//
//	acc := NewStreamingToolCallAccumulator()
//	for each streaming chunk {
//	    acc.ProcessDelta(chunk.Choices[0].Delta)
//	}
//	toolCalls := acc.Finalize()
type StreamingToolCallAccumulator struct {
	toolCalls map[int]*streamingToolCall
}

type streamingToolCall struct {
	ID        string
	Type      string
	Name      string
	Arguments strings.Builder
}

// NewStreamingToolCallAccumulator creates a new accumulator for streaming tool calls.
func NewStreamingToolCallAccumulator() *StreamingToolCallAccumulator {
	return &StreamingToolCallAccumulator{
		toolCalls: make(map[int]*streamingToolCall),
	}
}

// ProcessDelta processes a streaming delta and accumulates tool call data.
// Returns the list of tool call IDs that were updated (useful for tracking progress).
func (acc *StreamingToolCallAccumulator) ProcessDelta(delta Delta) []string {
	var updatedIDs []string

	for _, deltaCall := range delta.ToolCalls {
		index := deltaCall.Index

		// Initialize if this is a new tool call
		if acc.toolCalls[index] == nil {
			acc.toolCalls[index] = &streamingToolCall{}
		}

		tc := acc.toolCalls[index]

		// Handle ID - generate one if not provided
		if deltaCall.ID != "" {
			tc.ID = deltaCall.ID
		} else if tc.ID == "" {
			tc.ID = generateToolCallID(index)
		}

		// Accumulate other fields
		if deltaCall.Type != "" {
			tc.Type = deltaCall.Type
		}
		if deltaCall.Function.Name != "" {
			tc.Name = deltaCall.Function.Name
		}
		if deltaCall.Function.Arguments != "" {
			tc.Arguments.WriteString(deltaCall.Function.Arguments)
		}

		updatedIDs = append(updatedIDs, tc.ID)
	}

	return updatedIDs
}

// ProcessDeltaWithIDCallback processes a streaming delta and calls the callback
// with any newly generated tool call IDs. This is useful when you need to
// update the original delta with generated IDs for forwarding to clients.
func (acc *StreamingToolCallAccumulator) ProcessDeltaWithIDCallback(delta Delta, onNewID func(index int, id string)) []string {
	var updatedIDs []string

	for _, deltaCall := range delta.ToolCalls {
		index := deltaCall.Index

		// Initialize if this is a new tool call
		if acc.toolCalls[index] == nil {
			acc.toolCalls[index] = &streamingToolCall{}
		}

		tc := acc.toolCalls[index]

		// Handle ID - generate one if not provided
		if deltaCall.ID != "" {
			tc.ID = deltaCall.ID
		} else if tc.ID == "" {
			tc.ID = generateToolCallID(index)
			if onNewID != nil {
				onNewID(index, tc.ID)
			}
		}

		// Accumulate other fields
		if deltaCall.Type != "" {
			tc.Type = deltaCall.Type
		}
		if deltaCall.Function.Name != "" {
			tc.Name = deltaCall.Function.Name
		}
		if deltaCall.Function.Arguments != "" {
			tc.Arguments.WriteString(deltaCall.Function.Arguments)
		}

		updatedIDs = append(updatedIDs, tc.ID)
	}

	return updatedIDs
}

// Finalize parses accumulated arguments and returns complete ToolCall objects.
// Tool calls with empty names are skipped.
// Returns tool calls sorted by index.
func (acc *StreamingToolCallAccumulator) Finalize() []ToolCall {
	if len(acc.toolCalls) == 0 {
		return nil
	}

	// Find max index to ensure proper ordering
	maxIndex := 0
	for idx := range acc.toolCalls {
		if idx > maxIndex {
			maxIndex = idx
		}
	}

	toolCalls := make([]ToolCall, 0, len(acc.toolCalls))

	// Iterate in order
	for i := 0; i <= maxIndex; i++ {
		tc := acc.toolCalls[i]
		if tc == nil || tc.Name == "" {
			continue
		}

		// Parse arguments
		var args map[string]any
		argsStr := tc.Arguments.String()
		if argsStr != "" && argsStr != "null" {
			if err := json.Unmarshal([]byte(argsStr), &args); err != nil {
				// If parsing fails, use empty map
				args = make(map[string]any)
			}
		}
		if args == nil {
			args = make(map[string]any)
		}

		// Ensure ID is set
		id := tc.ID
		if id == "" {
			id = fmt.Sprintf("call_%d", i)
		}

		toolCalls = append(toolCalls, ToolCall{
			Index: i,
			ID:    id,
			Type:  tc.Type,
			Function: ToolCallFunction{
				Name:      tc.Name,
				Arguments: args,
			},
		})
	}

	return toolCalls
}

// GetToolCall returns a specific tool call by index without finalizing.
// Returns nil if the index doesn't exist.
func (acc *StreamingToolCallAccumulator) GetToolCall(index int) *ToolCall {
	tc := acc.toolCalls[index]
	if tc == nil {
		return nil
	}

	var args map[string]any
	argsStr := tc.Arguments.String()
	if argsStr != "" && argsStr != "null" {
		_ = json.Unmarshal([]byte(argsStr), &args)
	}
	if args == nil {
		args = make(map[string]any)
	}

	return &ToolCall{
		Index: index,
		ID:    tc.ID,
		Type:  tc.Type,
		Function: ToolCallFunction{
			Name:      tc.Name,
			Arguments: args,
		},
	}
}

// Count returns the number of tool calls being accumulated.
func (acc *StreamingToolCallAccumulator) Count() int {
	return len(acc.toolCalls)
}

// HasToolCalls returns true if any tool calls are being accumulated.
func (acc *StreamingToolCallAccumulator) HasToolCalls() bool {
	return len(acc.toolCalls) > 0
}

// Reset clears the accumulator for reuse.
func (acc *StreamingToolCallAccumulator) Reset() {
	acc.toolCalls = make(map[int]*streamingToolCall)
}

// generateToolCallID creates a unique ID for tool calls.
// The format matches OpenAI's tool call ID format: "call_" followed by random characters.
func generateToolCallID(index int) string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID
		return fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), index)
	}
	return fmt.Sprintf("call_%x", b)
}
