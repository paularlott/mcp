package claude

// Claude API types - internal representation
// These are converted to/from OpenAI types at the boundary

type ClaudeMessage struct {
	Role    string        `json:"role"`
	Content []ContentBlock `json:"content"`
}

type ContentBlock struct {
	Type      string                 `json:"type"` // "text", "tool_use", "tool_result"
	Text      string                 `json:"text,omitempty"`
	ID        string                 `json:"id,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Input     map[string]interface{} `json:"input,omitempty"`
	ToolUseID string                 `json:"tool_use_id,omitempty"`
	Content   interface{}            `json:"content,omitempty"`
}

type ClaudeRequest struct {
	Model       string          `json:"model"`
	Messages    []ClaudeMessage `json:"messages"`
	MaxTokens   int             `json:"max_tokens"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	Tools       []ClaudeTool    `json:"tools,omitempty"`
	System      string          `json:"system,omitempty"`
}

type ClaudeTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

type ClaudeResponse struct {
	ID           string          `json:"id"`
	Type         string          `json:"type"`
	Role         string          `json:"role"`
	Content      []ContentBlock  `json:"content"`
	Model        string          `json:"model"`
	StopReason   string          `json:"stop_reason,omitempty"`
	Usage        ClaudeUsage     `json:"usage"`
}

type ClaudeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type ClaudeStreamEvent struct {
	Type         string          `json:"type"`
	Index        int             `json:"index,omitempty"`
	Delta        *ClaudeDelta    `json:"delta,omitempty"`
	ContentBlock *ContentBlock   `json:"content_block,omitempty"`
	Message      *ClaudeResponse `json:"message,omitempty"`
	Usage        *ClaudeUsage    `json:"usage,omitempty"`
}

type ClaudeDelta struct {
	Type         string `json:"type,omitempty"`
	Text         string `json:"text,omitempty"`
	PartialJSON  string `json:"partial_json,omitempty"`
	StopReason   string `json:"stop_reason,omitempty"`
}

type ClaudeErrorResponse struct {
	Type  string       `json:"type"`
	Error ClaudeError  `json:"error"`
}

type ClaudeError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}
