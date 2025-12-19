package openai

// Response Object Types
type ResponseObject struct {
	ID      string    `json:"id"`
	Object  string    `json:"object"` // "response"
	Created int64     `json:"created"`
	Model   string    `json:"model"`
	Status  string    `json:"status"` // "completed", "in_progress", "failed", "cancelled"
	Output  []any     `json:"output,omitempty"`
	Error   *APIError `json:"error,omitempty"`
	Usage   *Usage    `json:"usage,omitempty"`
}

type ResponseListResponse struct {
	Object string           `json:"object"` // "list"
	Data   []ResponseObject `json:"data"`
}

type ResponseInputItemsResponse struct {
	Object string `json:"object"` // "list"
	Data   []any  `json:"data"`
}

type ResponseInputTokensResponse struct {
	Object string        `json:"object"` // "list"
	Data   []TokenDetail `json:"data"`
}

type TokenDetail struct {
	Text        string  `json:"text"`
	Token       int     `json:"token"`
	Logprob     float64 `json:"logprob"`
	TopLogprobs []struct {
		Token   string  `json:"token"`
		Logprob float64 `json:"logprob"`
	} `json:"top_logprobs,omitempty"`
}

// Request types for responses endpoints
type CreateResponseRequest struct {
	Model              string   `json:"model"`
	Input              []any    `json:"input,omitempty"`
	Modalities         []string `json:"modalities,omitempty"`
	Instructions       string   `json:"instructions,omitempty"`
	Tools              []Tool   `json:"tools,omitempty"`
	PreviousResponseID string   `json:"previous_response_id,omitempty"`
	Metadata           map[string]interface{} `json:"metadata,omitempty"`
}
