package openai

import (
	"encoding/json"
	"net/http"
)

// ResponseOutputItem represents a typed output item in a ResponseObject.
type ResponseOutputItem struct {
	Type    string                  `json:"type"` // "message", "function_call", "reasoning", etc.
	ID      string                  `json:"id,omitempty"`
	Role    string                  `json:"role,omitempty"`
	Status  string                  `json:"status,omitempty"`
	Content []ResponseOutputContent `json:"content,omitempty"`
	// function_call fields
	CallID    string         `json:"call_id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// ResponseOutputContent represents a content part within a ResponseOutputItem.
type ResponseOutputContent struct {
	Type        string `json:"type"` // "output_text", etc.
	Text        string `json:"text,omitempty"`
	Annotations []any  `json:"annotations,omitempty"`
}

// ResponseObject represents a complete OpenAI Responses API response object
// https://platform.openai.com/docs/api-reference/responses/object
type ResponseObject struct {
	ID                 string         `json:"id"`
	Object             string         `json:"object"` // "response"
	CreatedAt          int64          `json:"created_at"`
	Status             string         `json:"status"` // "completed", "in_progress", "failed", "cancelled", "queued", "incomplete"
	Error              *ResponseError `json:"error,omitempty"`
	IncompleteDetails  map[string]any `json:"incomplete_details,omitempty"`
	Instructions       string         `json:"instructions,omitempty"`
	MaxOutputTokens    *int           `json:"max_output_tokens,omitempty"`
	Model              string         `json:"model"`
	Output             []any          `json:"output,omitempty"`
	ParallelToolCalls  *bool          `json:"parallel_tool_calls,omitempty"`
	PreviousResponseID string         `json:"previous_response_id,omitempty"`
	Reasoning          map[string]any `json:"reasoning,omitempty"`
	Store              *bool          `json:"store,omitempty"`
	Temperature        *float64       `json:"temperature,omitempty"`
	Text               map[string]any `json:"text,omitempty"`
	ToolChoice         any            `json:"tool_choice,omitempty"`
	Tools              []Tool         `json:"tools,omitempty"`
	TopP               *float64       `json:"top_p,omitempty"`
	Truncation         string         `json:"truncation,omitempty"`
	Usage              *ResponseUsage `json:"usage,omitempty"`
	Metadata           map[string]any `json:"metadata,omitempty"`
}

// OutputItems returns the Output slice decoded into typed ResponseOutputItem values.
// Items whose type cannot be decoded are skipped.
func (r *ResponseObject) OutputItems() []ResponseOutputItem {
	if r == nil {
		return nil
	}
	items := make([]ResponseOutputItem, 0, len(r.Output))
	for _, raw := range r.Output {
		m, ok := raw.(map[string]any)
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
		if args, ok := m["arguments"].(map[string]any); ok {
			item.Arguments = args
		}
		if parts, ok := m["content"].([]any); ok {
			for _, p := range parts {
				if pm, ok := p.(map[string]any); ok {
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

// ResponseDeleted is the body returned by DELETE /responses/{id}. Per the
// Responses API spec the endpoint returns HTTP 200 (not 204) with this body,
// and object is "response" (not "response.deleted").
type ResponseDeleted struct {
	ID      string `json:"id"`
	Object  string `json:"object"` // always "response"
	Deleted bool   `json:"deleted"`
}

// NewResponseDeleted returns the spec-shaped body for a successful
// DELETE /responses/{id}. Use with DeleteStatusCode.
func NewResponseDeleted(id string) *ResponseDeleted {
	return &ResponseDeleted{ID: id, Object: "response", Deleted: true}
}

// ResponseInputTokensCount is the body returned by POST /responses/input_tokens,
// which counts input tokens for a request without creating a response.
type ResponseInputTokensCount struct {
	Object      string `json:"object"` // "response.input_tokens"
	InputTokens int    `json:"input_tokens"`
}

// NewResponseInputTokensCount returns the spec-shaped body for
// POST /responses/input_tokens.
func NewResponseInputTokensCount(inputTokens int) *ResponseInputTokensCount {
	return &ResponseInputTokensCount{Object: "response.input_tokens", InputTokens: inputTokens}
}

// HTTP status codes for the Responses API endpoints. These centralise the spec
// decisions that are non-obvious (create returns 200 not 201; delete returns
// 200 not 204) so each consumer doesn't re-derive them.
const (
	// CreateStatusCode is returned by POST /responses. The spec returns 200,
	// not 201 — including background requests, which return a response object
	// with status "in_progress".
	CreateStatusCode = http.StatusOK

	// RetrieveStatusCode is returned by GET /responses/{id}.
	RetrieveStatusCode = http.StatusOK

	// DeleteStatusCode is returned by DELETE /responses/{id}. The spec returns
	// 200 (not 204) with a NewResponseDeleted body.
	DeleteStatusCode = http.StatusOK

	// ListStatusCode is returned by GET /responses.
	ListStatusCode = http.StatusOK

	// CancelStatusCode is returned by POST /responses/{id}/cancel.
	CancelStatusCode = http.StatusOK

	// CompactStatusCode is returned by POST /responses/{id}/compact.
	CompactStatusCode = http.StatusOK

	// InputItemsStatusCode is returned by GET /responses/{id}/input_items.
	InputItemsStatusCode = http.StatusOK

	// InputTokensStatusCode is returned by POST /responses/input_tokens.
	InputTokensStatusCode = http.StatusOK
)

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
	Model              string         `json:"model"`
	Input              []any          `json:"input,omitempty"`
	Modalities         []string       `json:"modalities,omitempty"`
	Instructions       string         `json:"instructions,omitempty"`
	Tools              []Tool         `json:"tools,omitempty"`
	PreviousResponseID string         `json:"previous_response_id,omitempty"`
	Metadata           map[string]any `json:"metadata,omitempty"`
	Background         bool           `json:"background,omitempty"`
	MaxOutputTokens    *int           `json:"max_output_tokens,omitempty"`
	ParallelToolCalls  *bool          `json:"parallel_tool_calls,omitempty"`
	Store              *bool          `json:"store,omitempty"`
	Temperature        *float64       `json:"temperature,omitempty"`
	TopP               *float64       `json:"top_p,omitempty"`
	Truncation         string         `json:"truncation,omitempty"`
	ExtraBody          map[string]any `json:"-"`
}

var createResponseRequestJSONFields = map[string]struct{}{
	"model":                {},
	"input":                {},
	"modalities":           {},
	"instructions":         {},
	"tools":                {},
	"previous_response_id": {},
	"metadata":             {},
	"background":           {},
	"max_output_tokens":    {},
	"parallel_tool_calls":  {},
	"store":                {},
	"temperature":          {},
	"top_p":                {},
	"truncation":           {},
	"extra_body":           {},
}

// MarshalJSON merges ExtraBody into the top-level response request body.
// This matches OpenAI SDK extra_body behavior for provider-specific fields.
func (r CreateResponseRequest) MarshalJSON() ([]byte, error) {
	type createResponseRequestAlias CreateResponseRequest
	base, err := json.Marshal(createResponseRequestAlias(r))
	if err != nil {
		return nil, err
	}
	if len(r.ExtraBody) == 0 {
		return base, nil
	}

	var body map[string]any
	if err := json.Unmarshal(base, &body); err != nil {
		return nil, err
	}
	for key, value := range r.ExtraBody {
		body[key] = value
	}
	return json.Marshal(body)
}

// UnmarshalJSON captures unknown provider-specific request fields into
// ExtraBody so routers can preserve them when forwarding requests upstream.
// The `input` field is accepted either as a string (treated as a single user
// message, per the Responses API spec) or as an array of input items.
func (r *CreateResponseRequest) UnmarshalJSON(data []byte) error {
	type alias CreateResponseRequest

	// Probe input: the spec allows a string or an array of items. The Input
	// field is []any, so a bare string would fail the decode — extract and
	// normalise it ourselves.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return err
	}
	inputRaw := top["input"]

	decodePayload := data
	if len(inputRaw) > 0 && string(inputRaw) != "null" {
		var s string
		if err := json.Unmarshal(inputRaw, &s); err == nil {
			// input is a string: drop it from the payload so the []any decode
			// below doesn't fail, then store it in the normalised array form.
			delete(top, "input")
			decodePayload, _ = json.Marshal(top)
			r.Input = []any{map[string]any{"type": "message", "role": "user", "content": s}}
		}
	}

	// Decode all standard fields (alias avoids recursing back into this method).
	if err := json.Unmarshal(decodePayload, (*alias)(r)); err != nil {
		return err
	}

	// Capture unknown fields into ExtraBody.
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		return err
	}
	extraBody := make(map[string]any)
	for key, value := range body {
		if _, known := createResponseRequestJSONFields[key]; !known {
			extraBody[key] = value
		}
	}
	if rawExtra, ok := top["extra_body"]; ok && len(rawExtra) > 0 {
		var eb map[string]any
		if json.Unmarshal(rawExtra, &eb) == nil {
			for k, v := range eb {
				extraBody[k] = v
			}
		}
	}
	if len(extraBody) > 0 {
		r.ExtraBody = extraBody
	}

	return nil
}
