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
		blocks := m.Content.Blocks()

		// tool_result blocks (user role carrying tool outputs) must each become a
		// separate OpenAI message with role="tool".
		hasToolResult := false
		for _, b := range blocks {
			if b.Type == "tool_result" {
				hasToolResult = true
				break
			}
		}
		if hasToolResult {
			for _, b := range blocks {
				if b.Type != "tool_result" {
					continue
				}
				var content string
				switch v := b.Content.(type) {
				case string:
					content = v
				case []interface{}:
					for _, item := range v {
						if itemMap, ok := item.(map[string]interface{}); ok {
							if itemMap["type"] == "text" {
								if text, ok := itemMap["text"].(string); ok {
									content += text
								}
							}
						}
					}
				}
				messages = append(messages, openai.Message{
					Role:       "tool",
					Content:    content,
					ToolCallID: b.ToolUseID,
				})
			}
			continue
		}

		// All other messages: collect text and tool_use blocks.
		msg := openai.Message{Role: m.Role}
		var text string
		var toolCalls []openai.ToolCall
		for _, b := range blocks {
			switch b.Type {
			case "text":
				text += b.Text
			case "tool_use":
				toolCalls = append(toolCalls, openai.ToolCall{
					ID:   b.ID,
					Type: "function",
					Function: openai.ToolCallFunction{
						Name:      b.Name,
						Arguments: b.Input,
					},
				})
			}
		}
		if text != "" {
			msg.Content = text
		}
		if len(toolCalls) > 0 {
			msg.ToolCalls = toolCalls
		}
		if text != "" || len(toolCalls) > 0 {
			messages = append(messages, msg)
		}
	}

	// Convert Claude tool definitions to OpenAI tool definitions.
	var openaiTools []openai.Tool
	for _, t := range req.Tools {
		openaiTools = append(openaiTools, openai.Tool{
			Type: "function",
			Function: openai.ToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	openaiReq := openai.ChatCompletionRequest{
		Model:    req.Model,
		Messages: messages,
		Tools:    openaiTools,
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
		choice := resp.Choices[0]
		if text := choice.Message.GetContentAsString(); text != "" {
			content = append(content, ContentBlock{Type: "text", Text: text})
		}
		// Convert tool calls to tool_use content blocks.
		for _, tc := range choice.Message.ToolCalls {
			content = append(content, ContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: tc.Function.Arguments,
			})
		}
		if len(content) == 0 {
			content = []ContentBlock{{Type: "text", Text: ""}}
		}
		switch choice.FinishReason {
		case "stop":
			stopReason = "end_turn"
		case "length":
			stopReason = "max_tokens"
		case "tool_calls":
			stopReason = "tool_use"
		default:
			stopReason = choice.FinishReason
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

	// Block tracking: text block and one per tool call (keyed by OpenAI delta index).
	nextBlockIdx := 0
	textBlockIdx := -1  // -1 = not yet opened
	toolBlockIdx := make(map[int]int) // OpenAI tool-call index -> Claude content block index

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

		// ---- text content ----
		if delta.Content != "" {
			if textBlockIdx < 0 {
				textBlockIdx = nextBlockIdx
				nextBlockIdx++
				WriteSSEEvent(w, "content_block_start", ClaudeStreamEvent{
					Type: "content_block_start", Index: textBlockIdx,
					ContentBlock: &ContentBlock{Type: "text", Text: ""},
				})
				flush()
			}
			WriteSSEEvent(w, "content_block_delta", ClaudeStreamEvent{
				Type: "content_block_delta", Index: textBlockIdx,
				Delta: &ClaudeDelta{Type: "text_delta", Text: delta.Content},
			})
			flush()
		}

		// ---- tool call deltas ----
		for _, tc := range delta.ToolCalls {
			blkIdx, exists := toolBlockIdx[tc.Index]
			if !exists {
				// First delta for this tool call: open a new tool_use content block.
				blkIdx = nextBlockIdx
				nextBlockIdx++
				toolBlockIdx[tc.Index] = blkIdx
				WriteSSEEvent(w, "content_block_start", ClaudeStreamEvent{
					Type:  "content_block_start",
					Index: blkIdx,
					ContentBlock: &ContentBlock{
						Type: "tool_use",
						ID:   tc.ID,
						Name: tc.Function.Name,
					},
				})
				flush()
			}
			// Send partial JSON arguments.
			if tc.Function.Arguments != "" {
				WriteSSEEvent(w, "content_block_delta", ClaudeStreamEvent{
					Type:  "content_block_delta",
					Index: blkIdx,
					Delta: &ClaudeDelta{Type: "input_json_delta", PartialJSON: tc.Function.Arguments},
				})
				flush()
			}
		}

		// ---- finish ----
		if chunk.Choices[0].FinishReason != "" {
			// Close text block.
			if textBlockIdx >= 0 {
				WriteSSEEvent(w, "content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": textBlockIdx})
				flush()
			}
			// Close all tool_use blocks in the order they were opened.
			closed := make(map[int]bool)
			for i := 0; i < len(toolBlockIdx); i++ {
				for _, blkIdx := range toolBlockIdx {
					if !closed[blkIdx] {
						closed[blkIdx] = true
						WriteSSEEvent(w, "content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": blkIdx})
						flush()
						break
					}
				}
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
