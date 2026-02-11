package openai

import (
	"encoding/json"
	"strings"
)

// CompletionAccumulator accumulates streaming chat completion chunks into complete responses.
// It handles the incremental building of content, tool calls, and refusals.
type CompletionAccumulator struct {
	Choices []accumulatorChoice
}

type accumulatorChoice struct {
	Index        int
	Content      strings.Builder
	Refusal      strings.Builder
	ToolCalls    map[int]*accumulatorToolCall
	FinishReason string
}

type accumulatorToolCall struct {
	ID        string
	Type      string
	Name      string
	Arguments strings.Builder
}

// AddChunk processes a streaming chunk and accumulates its content.
func (acc *CompletionAccumulator) AddChunk(chunk ChatCompletionResponse) {
	for _, choice := range chunk.Choices {
		// Ensure we have enough choices
		for len(acc.Choices) <= choice.Index {
			acc.Choices = append(acc.Choices, accumulatorChoice{
				Index:     len(acc.Choices),
				ToolCalls: make(map[int]*accumulatorToolCall),
			})
		}

		accChoice := &acc.Choices[choice.Index]

		// Accumulate content
		if choice.Delta.Content != "" {
			accChoice.Content.WriteString(choice.Delta.Content)
		}

		// Accumulate refusal
		if choice.Delta.Refusal != "" {
			accChoice.Refusal.WriteString(choice.Delta.Refusal)
		}

		// Accumulate tool calls
		for _, deltaCall := range choice.Delta.ToolCalls {
			if _, exists := accChoice.ToolCalls[deltaCall.Index]; !exists {
				accChoice.ToolCalls[deltaCall.Index] = &accumulatorToolCall{}
			}

			tc := accChoice.ToolCalls[deltaCall.Index]

			if deltaCall.ID != "" {
				tc.ID = deltaCall.ID
			}
			if deltaCall.Type != "" {
				tc.Type = deltaCall.Type
			}
			if deltaCall.Function.Name != "" {
				tc.Name = deltaCall.Function.Name
			}
			if deltaCall.Function.Arguments != "" {
				tc.Arguments.WriteString(deltaCall.Function.Arguments)
			}
		}

		// Record finish reason
		if choice.FinishReason != "" {
			accChoice.FinishReason = choice.FinishReason
		}
	}
}

// FinishedContent returns the accumulated content for the first choice if complete.
// Returns the content and true if finish_reason is "stop", otherwise empty string and false.
func (acc *CompletionAccumulator) FinishedContent() (string, bool) {
	if len(acc.Choices) == 0 {
		return "", false
	}

	choice := &acc.Choices[0]
	if choice.FinishReason == "stop" {
		return choice.Content.String(), true
	}

	return "", false
}

// FinishedToolCall returns the first accumulated tool call for the first choice if complete.
// Returns the tool call and true if finish_reason is "tool_calls", otherwise nil and false.
func (acc *CompletionAccumulator) FinishedToolCall() (*ToolCall, bool) {
	if len(acc.Choices) == 0 {
		return nil, false
	}

	choice := &acc.Choices[0]
	if choice.FinishReason != "tool_calls" {
		return nil, false
	}

	// Get the first tool call (index 0)
	if tc, exists := choice.ToolCalls[0]; exists {
		return acc.buildToolCall(tc), true
	}

	return nil, false
}

// FinishedToolCalls returns all accumulated tool calls for the first choice if complete.
// Returns the tool calls and true if finish_reason is "tool_calls", otherwise nil and false.
func (acc *CompletionAccumulator) FinishedToolCalls() ([]ToolCall, bool) {
	if len(acc.Choices) == 0 {
		return nil, false
	}

	choice := &acc.Choices[0]
	if choice.FinishReason != "tool_calls" {
		return nil, false
	}

	var toolCalls []ToolCall
	for i := 0; i < len(choice.ToolCalls); i++ {
		if tc, exists := choice.ToolCalls[i]; exists {
			toolCalls = append(toolCalls, *acc.buildToolCall(tc))
		}
	}

	return toolCalls, len(toolCalls) > 0
}

// FinishedRefusal returns the accumulated refusal for the first choice if present.
// Returns the refusal and true if there is refusal content, otherwise empty string and false.
func (acc *CompletionAccumulator) FinishedRefusal() (string, bool) {
	if len(acc.Choices) == 0 {
		return "", false
	}

	choice := &acc.Choices[0]
	refusal := choice.Refusal.String()
	if refusal != "" {
		return refusal, true
	}

	return "", false
}

// Content returns the current accumulated content for the first choice.
func (acc *CompletionAccumulator) Content() string {
	if len(acc.Choices) == 0 {
		return ""
	}
	return acc.Choices[0].Content.String()
}

// FinishReason returns the finish reason for the first choice.
func (acc *CompletionAccumulator) FinishReason() string {
	if len(acc.Choices) == 0 {
		return ""
	}
	return acc.Choices[0].FinishReason
}

// IsComplete returns true if the first choice has a finish reason.
func (acc *CompletionAccumulator) IsComplete() bool {
	if len(acc.Choices) == 0 {
		return false
	}
	return acc.Choices[0].FinishReason != ""
}

func (acc *CompletionAccumulator) buildToolCall(tc *accumulatorToolCall) *ToolCall {
	var args map[string]any
	argsStr := tc.Arguments.String()
	if argsStr != "" {
		_ = json.Unmarshal([]byte(argsStr), &args)
	}
	if args == nil {
		args = make(map[string]any)
	}

	// Ensure Type is set - "function" is the only valid value
	tcType := tc.Type
	if tcType == "" {
		tcType = "function"
	}

	return &ToolCall{
		ID:   tc.ID,
		Type: tcType,
		Function: ToolCallFunction{
			Name:      tc.Name,
			Arguments: args,
		},
	}
}

// Reset clears the accumulator for reuse.
func (acc *CompletionAccumulator) Reset() {
	acc.Choices = nil
}
