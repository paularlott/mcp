package openai

// ResponseOutputItem represents a typed output item in a ResponseObject.
type ResponseOutputItem struct {
	Type    string                    `json:"type"`    // "message", "function_call", "reasoning", etc.
	ID      string                    `json:"id,omitempty"`
	Role    string                    `json:"role,omitempty"`
	Status  string                    `json:"status,omitempty"`
	Content []ResponseOutputContent   `json:"content,omitempty"`
	// function_call fields
	CallID    string         `json:"call_id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// ResponseOutputContent represents a content part within a ResponseOutputItem.
type ResponseOutputContent struct {
	Type        string `json:"type"`                  // "output_text", etc.
	Text        string `json:"text,omitempty"`
	Annotations []any  `json:"annotations,omitempty"`
}

// ResponseObject represents a complete OpenAI Responses API response object
// https://platform.openai.com/docs/api-reference/responses/object
type ResponseObject struct {
	ID                 string                 `json:"id"`
	Object             string                 `json:"object"` // "response"
	CreatedAt          int64                  `json:"created_at"`
	Status             string                 `json:"status"` // "completed", "in_progress", "failed", "cancelled", "queued", "incomplete"
	Error              *ResponseError         `json:"error,omitempty"`
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
	Usage              *ResponseUsage         `json:"usage,omitempty"`
	Metadata           map[string]interface{} `json:"metadata,omitempty"`
}

// OutputItems returns the Output slice decoded into typed ResponseOutputItem values.
// Items whose type cannot be decoded are skipped.
func (r *ResponseObject) OutputItems() []ResponseOutputItem {
	if r == nil {
		return nil
	}
	items := make([]ResponseOutputItem, 0, len(r.Output))
	for _, raw := range r.Output {
		m, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		item := ResponseOutputItem{
			Type:   getString(m, "type"),
			ID:     getString(m, "id"),
			Role:   getString(m, "role"),
			Status: getString(m, "status"),
			CallID: getString(m, "call_id"),
			Name:   getString(m, "name"),
		}
		if args, ok := m["arguments"].(map[string]interface{}); ok {
			item.Arguments = args
		}
		if parts, ok := m["content"].([]interface{}); ok {
			for _, p := range parts {
				if pm, ok := p.(map[string]interface{}); ok {
					item.Content = append(item.Content, ResponseOutputContent{
						Type: getString(pm, "type"),
						Text: getString(pm, "text"),
					})
				}
			}
		}
		items = append(items, item)
	}
	return items
}

// OutputText returns the concatenated output_text from all message output items.
func (r *ResponseObject) OutputText() string {
	var s string
	for _, item := range r.OutputItems() {
		if item.Type != "message" {
			continue
		}
		for _, c := range item.Content {
			if c.Type == "output_text" {
				s += c.Text
			}
		}
	}
	return s
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
