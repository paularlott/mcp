package openai

// ResponseObject represents a complete OpenAI Responses API response object
// https://platform.openai.com/docs/api-reference/responses/object
type ResponseObject struct {
	ID                 string                 `json:"id"`
	Object             string                 `json:"object"` // "response"
	CreatedAt          int64                  `json:"created_at"`
	Status             string                 `json:"status"` // "completed", "in_progress", "failed", "cancelled", "queued", "incomplete"
	Error              *APIError              `json:"error,omitempty"`
	IncompleteDetails  map[string]interface{} `json:"incomplete_details,omitempty"`
	Instructions       string                 `json:"instructions,omitempty"`
	MaxOutputTokens    *int                   `json:"max_output_tokens,omitempty"`
	Model              string                 `json:"model"`
	Output             []interface{}          `json:"output,omitempty"`
	ParallelToolCalls  *bool                  `json:"parallel_tool_calls,omitempty"`
	PreviousResponseID string                 `json:"previous_response_id,omitempty"`
	Reasoning          map[string]interface{} `json:"reasoning,omitempty"`
	Store              *bool                  `json:"store,omitempty"`
	Temperature        *float64               `json:"temperature,omitempty"`
	Text               map[string]interface{} `json:"text,omitempty"`
	ToolChoice         interface{}            `json:"tool_choice,omitempty"`
	Tools              []Tool                 `json:"tools,omitempty"`
	TopP               *float64               `json:"top_p,omitempty"`
	Truncation         string                 `json:"truncation,omitempty"`
	Usage              *Usage                 `json:"usage,omitempty"`
	Metadata           map[string]interface{} `json:"metadata,omitempty"`
}

// ResponseListResponse represents a list of response objects
type ResponseListResponse struct {
	Object string           `json:"object"` // "list"
	Data   []ResponseObject `json:"data"`
}

// ResponseInputItemsResponse represents a list of input items for a response
type ResponseInputItemsResponse struct {
	Object string `json:"object"` // "list"
	Data   []any  `json:"data"`
}

// ResponseInputTokensResponse represents token details for input
type ResponseInputTokensResponse struct {
	Object string        `json:"object"` // "list"
	Data   []TokenDetail `json:"data"`
}

// TokenDetail represents detailed information about a token
type TokenDetail struct {
	Text        string  `json:"text"`
	Token       int     `json:"token"`
	Logprob     float64 `json:"logprob"`
	TopLogprobs []struct {
		Token   string  `json:"token"`
		Logprob float64 `json:"logprob"`
	} `json:"top_logprobs,omitempty"`
}

// CreateResponseRequest represents a request to create a response
type CreateResponseRequest struct {
	Model              string                 `json:"model"`
	Input              []any                  `json:"input,omitempty"`
	Modalities         []string               `json:"modalities,omitempty"`
	Instructions       string                 `json:"instructions,omitempty"`
	Tools              []Tool                 `json:"tools,omitempty"`
	PreviousResponseID string                 `json:"previous_response_id,omitempty"`
	Metadata           map[string]interface{} `json:"metadata,omitempty"`
	Background         bool                   `json:"background,omitempty"`
	MaxOutputTokens    *int                   `json:"max_output_tokens,omitempty"`
	ParallelToolCalls  *bool                  `json:"parallel_tool_calls,omitempty"`
	Store              *bool                  `json:"store,omitempty"`
	Temperature        *float64               `json:"temperature,omitempty"`
	TopP               *float64               `json:"top_p,omitempty"`
	Truncation         string                 `json:"truncation,omitempty"`
}
