package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/paularlott/mcp/toon"
)

// ToolResponse represents the response from a tool
type ToolResponse struct {
	Content           []ToolContent `json:"content"`
	StructuredContent any           `json:"structuredContent,omitempty"`
}

func NewToolResponseMulti(responses ...*ToolResponse) *ToolResponse {
	var allContent []ToolContent
	var structuredContent any

	for _, resp := range responses {
		if resp.Content != nil {
			allContent = append(allContent, resp.Content...)
		}
		if resp.StructuredContent != nil {
			structuredContent = resp.StructuredContent
		}
	}

	return &ToolResponse{
		Content:           allContent,
		StructuredContent: structuredContent,
	}
}

func NewToolResponseText(text string) *ToolResponse {
	return &ToolResponse{Content: []ToolContent{{Type: "text", Text: text}}}
}

func NewToolResponseJSON(data any) *ToolResponse {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return &ToolResponse{Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error marshaling data: %v", err)}}}
	}
	return NewToolResponseText(string(jsonData))
}

// NewToolResponseAuto builds a ToolResponse from a loose value, applying the
// same conversion the server uses internally:
//   - a *ToolResponse is returned unchanged,
//   - a string becomes a text response,
//   - anything else is JSON-encoded.
//
// Use it in ToolProvider.ExecuteTool when you have a dynamic value (e.g. the
// output of a script or a remote call) rather than an already-built response.
// Prefer the specific NewToolResponse* constructors when you know the type.
func NewToolResponseAuto(value any) *ToolResponse {
	if tr, ok := value.(*ToolResponse); ok {
		return tr
	}
	if str, ok := value.(string); ok {
		return NewToolResponseText(str)
	}
	return NewToolResponseJSON(value)
}

func NewToolResponseTOON(data any) *ToolResponse {
	toonData, err := toon.Encode(data)
	if err != nil {
		return &ToolResponse{Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error encoding data: %v", err)}}}
	}
	return NewToolResponseText(toonData)
}

func NewToolResponseImage(data []byte, mimeType string) *ToolResponse {
	return &ToolResponse{Content: []ToolContent{{Type: "image", Data: base64.StdEncoding.EncodeToString(data), MimeType: mimeType}}}
}

func NewToolResponseAudio(data []byte, mimeType string) *ToolResponse {
	return &ToolResponse{Content: []ToolContent{{Type: "audio", Data: base64.StdEncoding.EncodeToString(data), MimeType: mimeType}}}
}

func NewToolResponseResource(uri, text, mimeType string) *ToolResponse {
	return &ToolResponse{Content: []ToolContent{{Type: "resource", Resource: &ResourceContent{URI: uri, Text: text, MimeType: mimeType}}}}
}

func NewToolResponseResourceLink(uri, text string) *ToolResponse {
	return &ToolResponse{Content: []ToolContent{{Type: "resource_link", Resource: &ResourceContent{URI: uri, Text: text}}}}
}

func NewToolResponseStructured(data any) *ToolResponse {
	response := &ToolResponse{
		StructuredContent: data,
	}

	return response
}
