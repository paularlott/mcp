package openai

import (
	"encoding/json"
	"fmt"
)

// ModelsResponse represents the response from the /models endpoint
type ModelsResponse struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

// Model represents an individual model
type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ChatCompletionRequest represents an OpenAI chat completion request
type ChatCompletionRequest struct {
	Model               string    `json:"model"`
	Messages            []Message `json:"messages"`
	Tools               []Tool    `json:"tools,omitempty"`
	MaxTokens           int       `json:"max_tokens,omitempty"`
	MaxCompletionTokens int       `json:"max_completion_tokens,omitempty"`
	Temperature         *float64  `json:"temperature,omitempty"`
	TopP                *float64  `json:"top_p,omitempty"`
	FrequencyPenalty    *float64  `json:"frequency_penalty,omitempty"`
	PresencePenalty     *float64  `json:"presence_penalty,omitempty"`
	ReasoningEffort     string    `json:"reasoning_effort,omitempty"`
	Stream              bool      `json:"stream"`
}

// ChatCompletionResponse represents an OpenAI chat completion response
type ChatCompletionResponse struct {
	ID                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	SystemFingerprint string   `json:"system_fingerprint,omitempty"`
	Choices           []Choice `json:"choices"`
	Usage             *Usage   `json:"usage,omitempty"`
}

// Message represents a chat message
type Message struct {
	Role       string     `json:"role,omitempty"`
	Content    any        `json:"content,omitempty"`
	Refusal    string     `json:"refusal,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// GetContentAsString returns the content as a string, handling both string and array formats
func (m *Message) GetContentAsString() string {
	if m.Content == nil {
		return ""
	}

	// If it's already a string
	if str, ok := m.Content.(string); ok {
		return str
	}

	// If it's an array of content parts
	if parts, ok := m.Content.([]any); ok {
		var result string
		for _, part := range parts {
			if partMap, ok := part.(map[string]any); ok {
				if partMap["type"] == "text" {
					if text, ok := partMap["text"].(string); ok {
						result += text
					}
				}
			}
		}
		return result
	}

	return ""
}

// SetContentAsString sets the content as a string
func (m *Message) SetContentAsString(content string) {
	m.Content = content
}

// Choice represents a completion choice
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message,omitempty"`
	Delta        Delta   `json:"delta,omitempty"`
	FinishReason string  `json:"finish_reason,omitempty"`
}

// Delta represents a streaming delta
type Delta struct {
	// ReasoningContent supports OpenAI's o1-style reasoning_content field
	ReasoningContent string `json:"reasoning_content,omitempty"`
	// Reasoning supports alternative reasoning field from some providers (e.g., vLLM)
	Reasoning string          `json:"reasoning,omitempty"`
	Role      string          `json:"role,omitempty"`
	Content   string          `json:"content,omitempty"`
	Refusal   string          `json:"refusal,omitempty"`
	ToolCalls []DeltaToolCall `json:"tool_calls,omitempty"`
}

// GetReasoningContent returns the reasoning content, supporting both field names
func (d *Delta) GetReasoningContent() string {
	if d.ReasoningContent != "" {
		return d.ReasoningContent
	}
	return d.Reasoning
}

// Usage represents token usage (Chat Completions API)
type Usage struct {
	PromptTokens            int                      `json:"prompt_tokens"`
	CompletionTokens        int                      `json:"completion_tokens"`
	TotalTokens             int                      `json:"total_tokens"`
	PromptTokensDetails     *PromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
}

// ResponseUsage represents token usage (Responses API)
type ResponseUsage struct {
	InputTokens            int                         `json:"input_tokens"`
	OutputTokens           int                         `json:"output_tokens"`
	TotalTokens            int                         `json:"total_tokens"`
	InputTokensDetails     *ResponseInputTokensDetails `json:"input_tokens_details,omitempty"`
	OutputTokensDetails    *ResponseOutputTokensDetails `json:"output_tokens_details,omitempty"`
}

// Add accumulates the token counts from another ResponseUsage into this one.
func (u *ResponseUsage) Add(other *ResponseUsage) {
	if other == nil {
		return
	}
	u.InputTokens += other.InputTokens
	u.OutputTokens += other.OutputTokens
	u.TotalTokens = u.InputTokens + u.OutputTokens
}

// ResponseInputTokensDetails represents input token details for Responses API
type ResponseInputTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// ResponseOutputTokensDetails represents output token details for Responses API
type ResponseOutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// Add accumulates the token counts from another Usage into this one.
// This is useful for tracking cumulative usage across multiple API calls
// in multi-turn tool call loops.
func (u *Usage) Add(other *Usage) {
	if other == nil {
		return
	}
	u.PromptTokens += other.PromptTokens
	u.CompletionTokens += other.CompletionTokens
	u.TotalTokens = u.PromptTokens + u.CompletionTokens
}

// PromptTokensDetails represents detailed prompt token usage
type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
	AudioTokens  int `json:"audio_tokens"`
}

// CompletionTokensDetails represents detailed completion token usage
type CompletionTokensDetails struct {
	ReasoningTokens          int `json:"reasoning_tokens"`
	AudioTokens              int `json:"audio_tokens"`
	AcceptedPredictionTokens int `json:"accepted_prediction_tokens"`
	RejectedPredictionTokens int `json:"rejected_prediction_tokens"`
}

// ContentPart represents a multi-modal content part
type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL represents an image URL in content
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// Tool represents an OpenAI tool definition
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction represents a function definition for a tool
type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// ToolCall represents a tool call from the assistant
type ToolCall struct {
	Index    int              `json:"index,omitempty"`
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction represents the function details of a tool call
type ToolCallFunction struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// MarshalJSON implements custom JSON marshaling for ToolCallFunction.
// OpenAI expects arguments as a JSON string, not an object.
func (tcf ToolCallFunction) MarshalJSON() ([]byte, error) {
	argsJSON, err := json.Marshal(tcf.Arguments)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal arguments: %w", err)
	}

	return json.Marshal(struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}{
		Name:      tcf.Name,
		Arguments: string(argsJSON),
	})
}

// UnmarshalJSON implements custom JSON unmarshaling for ToolCallFunction.
// OpenAI sends arguments as a JSON string, not an object.
func (tcf *ToolCallFunction) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	tcf.Name = raw.Name
	tcf.Arguments = make(map[string]any)

	if raw.Arguments != "" {
		if err := json.Unmarshal([]byte(raw.Arguments), &tcf.Arguments); err != nil {
			return fmt.Errorf("failed to unmarshal arguments: %w", err)
		}
	}

	return nil
}

// DeltaToolCall represents a streaming tool call delta
type DeltaToolCall struct {
	Index    int           `json:"index"`
	ID       string        `json:"id,omitempty"`
	Type     string        `json:"type,omitempty"`
	Function DeltaFunction `json:"function,omitempty"`
}

// DeltaFunction represents a streaming function delta
type DeltaFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// EmbeddingRequest represents an OpenAI embedding request
type EmbeddingRequest struct {
	Model          string      `json:"model"`
	Input          interface{} `json:"input"`
	EncodingFormat string      `json:"encoding_format,omitempty"`
	Dimensions     int         `json:"dimensions,omitempty"`
	User           string      `json:"user,omitempty"`
}

// EmbeddingResponse represents an OpenAI embedding response
type EmbeddingResponse struct {
	Object string      `json:"object"`
	Data   []Embedding `json:"data"`
	Model  string      `json:"model"`
	Usage  Usage       `json:"usage"`
}

// Embedding represents a single embedding
type Embedding struct {
	Object    string    `json:"object"`
	Embedding []float64 `json:"embedding"`
	Index     int       `json:"index"`
}
