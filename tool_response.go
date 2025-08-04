package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// ToolResponse represents the response from a tool
type ToolResponse struct {
	Content           []ToolContent `json:"content"`
	StructuredContent interface{}   `json:"structuredContent,omitempty"`
}

func NewToolResponseMulti(responses ...*ToolResponse) *ToolResponse {
	var allContent []ToolContent
	var structuredContent interface{}

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

func NewToolResponseJSON(data interface{}) *ToolResponse {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return &ToolResponse{Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error marshaling data: %v", err)}}}
	}
	return NewToolResponseText(string(jsonData))
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

func NewToolResponseStructured(data interface{}) *ToolResponse {
	response := &ToolResponse{
		StructuredContent: data,
	}

	return response
}
