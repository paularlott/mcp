package openai

import (
	"encoding/json"
)

// EstimateTokens returns a rough token count for a given input string.
// This uses a character-based heuristic of ~4 characters per token, which
// is a widely used approximation for LLM tokenizers (GPT, Claude, Gemini).
// It handles code, JSON, and long words better than word-based counting.
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}

	// Using byte length works well since non-ASCII characters
	// typically map to multiple tokens anyway.
	tokens := (len(text) + 3) / 4
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}

// TokenCounter estimates token usage for OpenAI API requests and responses.
// It provides a fast, reproducible approximation of token counts when exact
// counts are not available from the API.
type TokenCounter struct {
	promptTokens     int
	completionTokens int
}

// NewTokenCounter creates a new TokenCounter
func NewTokenCounter() *TokenCounter {
	return &TokenCounter{}
}

// AddPromptTokensFromMessages estimates and adds prompt tokens from chat messages
func (tc *TokenCounter) AddPromptTokensFromMessages(messages []Message) {
	// Chat template overhead (BOS token, priming, etc.)
	// Most chat models add ~3-5 tokens for the conversation structure
	const chatTemplateOverhead = 4

	// Per-message overhead for role markers and special tokens
	// e.g., <|start_header_id|>role<|end_header_id|> + <|eot_id|>
	const perMessageOverhead = 3

	tc.promptTokens += chatTemplateOverhead

	for _, msg := range messages {
		// Add tokens for role
		tc.promptTokens += EstimateTokens(msg.Role)

		// Handle content (can be string or array of content parts)
		tc.promptTokens += tc.estimateContentTokens(msg.Content)

		// Add tokens for tool calls if present
		for _, toolCall := range msg.ToolCalls {
			tc.promptTokens += EstimateTokens(toolCall.Function.Name)
			tc.promptTokens += tc.estimateArgsTokens(toolCall.Function.Arguments)
		}

		// Add per-message overhead for special tokens
		tc.promptTokens += perMessageOverhead
	}
}

// AddPromptTokensFromText adds estimated prompt tokens from text
func (tc *TokenCounter) AddPromptTokensFromText(text string) {
	tc.promptTokens += EstimateTokens(text)
}

// AddCompletionTokensFromText adds estimated completion tokens from text
func (tc *TokenCounter) AddCompletionTokensFromText(text string) {
	tc.completionTokens += EstimateTokens(text)
}

// AddCompletionTokensFromMessage adds estimated completion tokens from a chat message
func (tc *TokenCounter) AddCompletionTokensFromMessage(msg *Message) {
	if msg == nil {
		return
	}

	// Handle content
	tc.completionTokens += tc.estimateContentTokens(msg.Content)

	// Add tokens for tool calls if present
	for _, toolCall := range msg.ToolCalls {
		tc.completionTokens += EstimateTokens(toolCall.Function.Name)
		tc.completionTokens += tc.estimateArgsTokens(toolCall.Function.Arguments)
	}
}

// AddCompletionTokensFromDelta adds estimated completion tokens from a streaming delta
func (tc *TokenCounter) AddCompletionTokensFromDelta(delta *Delta) {
	if delta == nil {
		return
	}

	tc.completionTokens += EstimateTokens(delta.Content)
	tc.completionTokens += EstimateTokens(delta.ReasoningContent)

	// Add tokens for tool calls if present
	for _, toolCall := range delta.ToolCalls {
		tc.completionTokens += EstimateTokens(toolCall.Function.Name)
		tc.completionTokens += EstimateTokens(toolCall.Function.Arguments)
	}
}

// GetUsage returns the current usage statistics
func (tc *TokenCounter) GetUsage() Usage {
	return Usage{
		PromptTokens:     tc.promptTokens,
		CompletionTokens: tc.completionTokens,
		TotalTokens:      tc.promptTokens + tc.completionTokens,
	}
}

// Reset resets the token counters to zero
func (tc *TokenCounter) Reset() {
	tc.promptTokens = 0
	tc.completionTokens = 0
}

// InjectUsageIfMissing injects estimated usage into a chat completion response
// if it's missing, incomplete, or the estimated total exceeds the reported total.
func (tc *TokenCounter) InjectUsageIfMissing(resp *ChatCompletionResponse) {
	if resp == nil {
		return
	}

	estimated := tc.GetUsage()

	// Inject if usage is missing entirely
	if resp.Usage == nil {
		resp.Usage = &estimated
		return
	}

	// Fill in any zero components with estimates
	if resp.Usage.PromptTokens == 0 || resp.Usage.CompletionTokens == 0 {
		resp.Usage.PromptTokens = estimated.PromptTokens
		resp.Usage.CompletionTokens = estimated.CompletionTokens
	}

	// If estimated total exceeds reported total, use estimated values
	// (indicates API is underreporting, e.g. Gemini or missing fields)
	estimatedTotal := estimated.PromptTokens + estimated.CompletionTokens
	reportedTotal := resp.Usage.PromptTokens + resp.Usage.CompletionTokens
	if estimatedTotal > reportedTotal {
		resp.Usage = &estimated
		return
	}

	resp.Usage.TotalTokens = resp.Usage.PromptTokens + resp.Usage.CompletionTokens
}

// estimateContentTokens estimates tokens from message content (string or array format)
func (tc *TokenCounter) estimateContentTokens(content any) int {
	if content == nil {
		return 0
	}

	// If it's already a string
	if str, ok := content.(string); ok {
		return EstimateTokens(str)
	}

	// If it's an array of content parts
	if parts, ok := content.([]any); ok {
		tokens := 0
		for _, part := range parts {
			if partMap, ok := part.(map[string]any); ok {
				if text, ok := partMap["text"].(string); ok {
					tokens += EstimateTokens(text)
				}
				// Image tokens are harder to estimate, use a rough approximation
				if partMap["type"] == "image_url" {
					tokens += 85 // Rough estimate for low detail image
				}
			}
		}
		return tokens
	}

	return 0
}

// estimateArgsTokens estimates tokens from tool call arguments
func (tc *TokenCounter) estimateArgsTokens(args map[string]any) int {
	if args == nil {
		return 0
	}
	// Marshal to JSON and estimate from string representation
	jsonBytes, err := json.Marshal(args)
	if err != nil {
		return 0
	}
	return EstimateTokens(string(jsonBytes))
}

// MessageFromMap converts a map[string]any to a Message struct.
// This is useful when working with dynamic/generic message representations
// (e.g., from JSON parsing or scripting language interop).
func MessageFromMap(m map[string]any) Message {
	msg := Message{}
	if role, ok := m["role"].(string); ok {
		msg.Role = role
	}
	if content, ok := m["content"]; ok {
		msg.Content = content
	}
	if toolCallID, ok := m["tool_call_id"].(string); ok {
		msg.ToolCallID = toolCallID
	}
	if toolCallsRaw, ok := m["tool_calls"]; ok && toolCallsRaw != nil {
		if tcList, ok := toolCallsRaw.([]any); ok {
			for _, tcRaw := range tcList {
				if tcMap, ok := tcRaw.(map[string]any); ok {
					toolCall := ToolCall{Type: "function"}
					if id, ok := tcMap["id"].(string); ok {
						toolCall.ID = id
					}
					if tcType, ok := tcMap["type"].(string); ok && tcType != "" {
						toolCall.Type = tcType
					}
					if fnRaw, ok := tcMap["function"]; ok {
						if fnMap, ok := fnRaw.(map[string]any); ok {
							if name, ok := fnMap["name"].(string); ok {
								toolCall.Function.Name = name
							}
							if args, ok := fnMap["arguments"]; ok {
								switch argsVal := args.(type) {
								case string:
									var argsMap map[string]any
									if err := json.Unmarshal([]byte(argsVal), &argsMap); err == nil {
										toolCall.Function.Arguments = argsMap
									}
								case map[string]any:
									toolCall.Function.Arguments = argsVal
								}
							}
						}
					}
					msg.ToolCalls = append(msg.ToolCalls, toolCall)
				}
			}
		}
	}
	return msg
}

// AddPromptTokensFromMaps estimates and adds prompt tokens from chat messages
// represented as []map[string]any. Each map should have "role" and "content" keys,
// and may include "tool_calls" and "tool_call_id".
func (tc *TokenCounter) AddPromptTokensFromMaps(messages []map[string]any) {
	msgs := make([]Message, 0, len(messages))
	for _, m := range messages {
		msgs = append(msgs, MessageFromMap(m))
	}
	tc.AddPromptTokensFromMessages(msgs)
}

// AddCompletionTokensFromResponseMap estimates and adds completion tokens from a
// response represented as a map[string]any. Supports both Chat Completions API format
// (choices[0].message) and Responses API format (output[].content[].text).
func (tc *TokenCounter) AddCompletionTokensFromResponseMap(response map[string]any) {
	// Chat Completions API: choices[0].message
	if choices, ok := response["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if message, ok := choice["message"].(map[string]any); ok {
				msg := MessageFromMap(message)
				tc.AddCompletionTokensFromMessage(&msg)
			}
		}
	}

	// Responses API: output[].content[].text
	if output, ok := response["output"].([]any); ok {
		for _, item := range output {
			if itemMap, ok := item.(map[string]any); ok {
				if contentList, ok := itemMap["content"].([]any); ok {
					for _, contentItem := range contentList {
						if cMap, ok := contentItem.(map[string]any); ok {
							if text, ok := cMap["text"].(string); ok {
								tc.AddCompletionTokensFromText(text)
							}
						}
					}
				}
			}
		}
	}
}
