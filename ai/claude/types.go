package claude

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/paularlott/mcp/ai/openai"
)

// SystemField handles the Anthropic system field which can be a string or []ContentBlock
type SystemField struct {
	text string
}

func (s *SystemField) UnmarshalJSON(data []byte) error {
	// Try string first
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		s.text = str
		return nil
	}
	// Try array of content blocks
	var blocks []ContentBlock
	if err := json.Unmarshal(data, &blocks); err != nil {
		return err
	}
	for _, b := range blocks {
		if b.Type == "text" {
			s.text += b.Text
		}
	}
	return nil
}

func (s SystemField) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.text)
}

func (s SystemField) String() string { return s.text }

// MessagesRequest is the Anthropic Messages API request format (exported for gateway use)
type MessagesRequest struct {
	Model       string           `json:"model"`
	Messages    []ClaudeMessage  `json:"messages"`
	MaxTokens   int              `json:"max_tokens"`
	Temperature *float64         `json:"temperature,omitempty"`
	TopP        *float64         `json:"top_p,omitempty"`
	Stream      bool             `json:"stream,omitempty"`
	Tools       []ClaudeTool     `json:"tools,omitempty"`
	System      SystemField      `json:"system,omitempty"`
}

// MessagesResponse is the Anthropic Messages API response format (exported for gateway use)
type MessagesResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Content    []ContentBlock `json:"content"`
	Model      string         `json:"model"`
	StopReason string         `json:"stop_reason,omitempty"`
	Usage      MessagesUsage  `json:"usage"`
}

// MessagesUsage is the token usage for the Messages API
type MessagesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ClaudeMessage is a message in the Claude format
type ClaudeMessage struct {
	Role    string         `json:"role"`
	Content MessageContent `json:"content"`
}

// MessageContent handles content that can be a string or []ContentBlock
type MessageContent struct {
	blocks []ContentBlock
}

func (m *MessageContent) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		m.blocks = []ContentBlock{{Type: "text", Text: str}}
		return nil
	}
	return json.Unmarshal(data, &m.blocks)
}

func (m MessageContent) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.blocks)
}

func (m MessageContent) Blocks() []ContentBlock { return m.blocks }

// ContentBlock is a content block in the Claude format
type ContentBlock struct {
	Type      string                 `json:"type"` // "text", "tool_use", "tool_result"
	Text      string                 `json:"text,omitempty"`
	ID        string                 `json:"id,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Input     map[string]interface{} `json:"input,omitempty"`
	ToolUseID string                 `json:"tool_use_id,omitempty"`
	Content   interface{}            `json:"content,omitempty"`
}

// ClaudeTool is a tool definition in the Claude format
type ClaudeTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

// MessagesRequestToOpenAI converts a MessagesRequest to an OpenAI ChatCompletionRequest
func MessagesRequestToOpenAI(req *MessagesRequest) openai.ChatCompletionRequest {
	var messages []openai.Message
	if req.System.text != "" {
		messages = append(messages, openai.Message{Role: "system", Content: req.System.text})
	}
	for _, m := range req.Messages {
		msg := openai.Message{Role: m.Role}
		var text string
		for _, b := range m.Content.Blocks() {
			if b.Type == "text" {
				text += b.Text
			}
		}
		msg.Content = text
		messages = append(messages, msg)
	}
	openaiReq := openai.ChatCompletionRequest{
		Model:    req.Model,
		Messages: messages,
		Stream:   req.Stream,
	}
	if req.MaxTokens > 0 {
		openaiReq.MaxTokens = req.MaxTokens
	}
	if req.Temperature != nil {
		openaiReq.Temperature = req.Temperature
	}
	if req.TopP != nil {
		openaiReq.TopP = req.TopP
	}
	return openaiReq
}

// OpenAIToMessagesResponse converts an OpenAI ChatCompletionResponse to a MessagesResponse
func OpenAIToMessagesResponse(resp *openai.ChatCompletionResponse) *MessagesResponse {
	var content []ContentBlock
	var stopReason string
	if len(resp.Choices) > 0 {
		content = []ContentBlock{{Type: "text", Text: resp.Choices[0].Message.GetContentAsString()}}
		switch resp.Choices[0].FinishReason {
		case "stop":
			stopReason = "end_turn"
		case "length":
			stopReason = "max_tokens"
		case "tool_calls":
			stopReason = "tool_use"
		default:
			stopReason = resp.Choices[0].FinishReason
		}
	}
	usage := MessagesUsage{}
	if resp.Usage != nil {
		usage.InputTokens = resp.Usage.PromptTokens
		usage.OutputTokens = resp.Usage.CompletionTokens
	}
	return &MessagesResponse{
		ID:         resp.ID,
		Type:       "message",
		Role:       "assistant",
		Content:    content,
		Model:      resp.Model,
		StopReason: stopReason,
		Usage:      usage,
	}
}

// WriteSSEEvent writes a single Anthropic SSE event to a writer.
func WriteSSEEvent(w io.Writer, eventType string, data interface{}) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, b)
	return err
}

// StreamOpenAIToMessages converts an OpenAI chat stream to Anthropic Messages SSE format.
func StreamOpenAIToMessages(w io.Writer, flush func(), stream *openai.ChatStream, model string) error {
	msgID := "msg_stream"
	sentStart := false
	sentBlockStart := false

	for stream.Next() {
		chunk := stream.Current()
		if chunk.ID != "" {
			msgID = chunk.ID
		}
		if chunk.Model != "" {
			model = chunk.Model
		}

		if !sentStart {
			usage := MessagesUsage{}
			if chunk.Usage != nil {
				usage.InputTokens = chunk.Usage.PromptTokens
			}
			WriteSSEEvent(w, "message_start", ClaudeStreamEvent{
				Type: "message_start",
				Message: &MessagesResponse{
					ID: msgID, Type: "message", Role: "assistant",
					Model: model, Content: []ContentBlock{},
					Usage: usage,
				},
			})
			flush()
			sentStart = true
		}

		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta

		if delta.Content != "" {
			if !sentBlockStart {
				WriteSSEEvent(w, "content_block_start", ClaudeStreamEvent{
					Type: "content_block_start", Index: 0,
					ContentBlock: &ContentBlock{Type: "text", Text: ""},
				})
				flush()
				sentBlockStart = true
			}
			WriteSSEEvent(w, "content_block_delta", ClaudeStreamEvent{
				Type: "content_block_delta", Index: 0,
				Delta: &ClaudeDelta{Type: "text_delta", Text: delta.Content},
			})
			flush()
		}

		if chunk.Choices[0].FinishReason != "" {
			if sentBlockStart {
				WriteSSEEvent(w, "content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": 0})
				flush()
			}
			stopReason := "end_turn"
			switch chunk.Choices[0].FinishReason {
			case "length":
				stopReason = "max_tokens"
			case "tool_calls":
				stopReason = "tool_use"
			}
			outTokens := 0
			if chunk.Usage != nil {
				outTokens = chunk.Usage.CompletionTokens
			}
			WriteSSEEvent(w, "message_delta", ClaudeStreamEvent{
				Type:  "message_delta",
				Delta: &ClaudeDelta{StopReason: stopReason},
				Usage: &MessagesUsage{OutputTokens: outTokens},
			})
			WriteSSEEvent(w, "message_stop", map[string]string{"type": "message_stop"})
			flush()
		}
	}
	return stream.Err()
}

// ModelInfo is a single model entry in the Anthropic models list response
type ModelInfo struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

// ModelsListResponse is the Anthropic models list API response format
type ModelsListResponse struct {
	Data    []ModelInfo `json:"data"`
	HasMore bool        `json:"has_more"`
	FirstID string      `json:"first_id,omitempty"`
	LastID  string      `json:"last_id,omitempty"`
}

// internal types (unexported)
type ClaudeRequest = MessagesRequest
type ClaudeResponse = MessagesResponse
type ClaudeUsage = MessagesUsage

type ClaudeStreamEvent struct {
	Type         string          `json:"type"`
	Index        int             `json:"index,omitempty"`
	Delta        *ClaudeDelta    `json:"delta,omitempty"`
	ContentBlock *ContentBlock   `json:"content_block,omitempty"`
	Message      *ClaudeResponse `json:"message,omitempty"`
	Usage        *ClaudeUsage    `json:"usage,omitempty"`
}

type ClaudeDelta struct {
	Type        string `json:"type,omitempty"`
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	StopReason  string `json:"stop_reason,omitempty"`
}

type ClaudeErrorResponse struct {
	Type  string      `json:"type"`
	Error ClaudeError `json:"error"`
}

type ClaudeError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}
