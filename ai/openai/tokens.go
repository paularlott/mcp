package openai

import (
	"encoding/json"
	"strings"
	"unicode"
)

// EstimateTokens returns a rough token count for a given input string.
// This uses a simple heuristic based on word boundaries and punctuation.
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}

	words := strings.Fields(text)

	// If there are no words, check if it's just punctuation/whitespace
	if len(words) == 0 {
		// Count non-whitespace characters as potential punctuation tokens
		tokens := 0
		for _, r := range text {
			if !unicode.IsSpace(r) {
				tokens++
			}
		}
		return tokens
	}

	tokens := 0
	for _, word := range words {
		// Check if this "word" is pure punctuation
		isPurePunct := true
		punctCount := 0

		for _, r := range word {
			if unicode.IsPunct(r) {
				punctCount++
			} else {
				isPurePunct = false
			}
		}

		if isPurePunct {
			// Pure punctuation: count each punctuation mark as 1 token
			tokens += punctCount
		} else {
			// Mixed word: 1 token for the word + additional tokens for punctuation
			tokens += 1 + punctCount
		}
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

// InjectUsageIfMissing injects estimated usage into a chat completion response if it's missing or zero
func (tc *TokenCounter) InjectUsageIfMissing(resp *ChatCompletionResponse) {
	if resp == nil {
		return
	}

	// Only inject if usage is missing or all fields are zero
	if resp.Usage == nil || (resp.Usage.PromptTokens == 0 && resp.Usage.CompletionTokens == 0 && resp.Usage.TotalTokens == 0) {
		usage := tc.GetUsage()
		resp.Usage = &usage
	}
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
