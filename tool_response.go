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

// NewToolResponseStructured builds a structured tool result: data (which
// must marshal to a JSON object, per the MCP spec's structuredContent
// requirement — a non-object value produces an error-text result instead)
// is set as StructuredContent, and — per that spec's backwards compatibility
// guidance ("a tool that returns structured content SHOULD also return the
// serialized JSON in a TextContent block") — the same JSON is also included
// as a text content block, for clients that don't read structuredContent.
func NewToolResponseStructured(data any) *ToolResponse {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return &ToolResponse{Content: []ToolContent{{Type: "text", Text: fmt.Sprintf("Error marshaling data: %v", err)}}}
	}
	if !isJSONObject(jsonData) {
		return &ToolResponse{Content: []ToolContent{{Type: "text", Text: "Error: structuredContent must marshal to a JSON object, not " + jsonValueKind(jsonData)}}}
	}
	return &ToolResponse{
		Content:           []ToolContent{{Type: "text", Text: string(jsonData)}},
		StructuredContent: data,
	}
}

// isJSONObject reports whether marshaled JSON is an object ("{...}"), per the
// MCP spec's requirement that structuredContent be a JSON object.
func isJSONObject(jsonData []byte) bool {
	for _, b := range jsonData {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '{':
			return true
		default:
			return false
		}
	}
	return false
}

// jsonValueKind gives a short human-readable description of a non-object
// JSON value's shape, for the NewToolResponseStructured error message.
func jsonValueKind(jsonData []byte) string {
	for _, b := range jsonData {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '[':
			return "an array"
		case '"':
			return "a string"
		case 'n':
			return "null"
		case 't', 'f':
			return "a boolean"
		default:
			return "a number"
		}
	}
	return "an empty value"
}
