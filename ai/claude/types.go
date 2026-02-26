package claude

import "github.com/paularlott/mcp/ai/openai"

// MessagesRequest is the Anthropic Messages API request format (exported for gateway use)
type MessagesRequest struct {
	Model       string           `json:"model"`
	Messages    []ClaudeMessage  `json:"messages"`
	MaxTokens   int              `json:"max_tokens"`
	Temperature *float64         `json:"temperature,omitempty"`
	TopP        *float64         `json:"top_p,omitempty"`
	Stream      bool             `json:"stream,omitempty"`
	Tools       []ClaudeTool     `json:"tools,omitempty"`
	System      string           `json:"system,omitempty"`
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
	Content []ContentBlock `json:"content"`
}

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
	if req.System != "" {
		messages = append(messages, openai.Message{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		msg := openai.Message{Role: m.Role}
		var text string
		for _, b := range m.Content {
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
