package openai

// Conversation represents a conversation object
// https://platform.openai.com/docs/api-reference/conversations/object
type Conversation struct {
    ID        string                 `json:"id"`
    Object    string                 `json:"object"` // "conversation"
    CreatedAt int64                  `json:"created_at"`
    Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// ConversationItem represents an item in a conversation
// Items can be messages, tool calls, reasoning, etc.
type ConversationItem struct {
    Type   string      `json:"type"` // "message", "tool_call", "reasoning", etc.
    ID     string      `json:"id"`
    Status string      `json:"status,omitempty"` // "completed", "incomplete", "in_progress"
    Role   string      `json:"role,omitempty"`   // "user", "assistant", "system"
    Content []ContentPart `json:"content,omitempty"`
    // Additional fields based on type
    ToolCall     *ToolCall              `json:"tool_call,omitempty"`
    ToolCallID   string                 `json:"tool_call_id,omitempty"`
    Name         string                 `json:"name,omitempty"`
    Output       interface{}            `json:"output,omitempty"`
    Reasoning    map[string]interface{} `json:"reasoning,omitempty"`
}



// CreateConversationRequest represents a request to create a conversation
type CreateConversationRequest struct {
    Items    []ConversationItem     `json:"items,omitempty"`
    Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// UpdateConversationRequest represents a request to update a conversation
type UpdateConversationRequest struct {
    Metadata map[string]interface{} `json:"metadata"`
}

// CreateItemsRequest represents a request to add items to a conversation
type CreateItemsRequest struct {
    Items []ConversationItem `json:"items"`
}

// ConversationDeleteResponse represents the response when deleting a conversation
type ConversationDeleteResponse struct {
    ID      string `json:"id"`
    Object  string `json:"object"` // "conversation.deleted"
    Deleted bool   `json:"deleted"`
}

// ConversationItemListResponse represents a list of conversation items
type ConversationItemListResponse struct {
    Object  string             `json:"object"` // "list"
    Data    []ConversationItem `json:"data"`
    FirstID string             `json:"first_id,omitempty"`
    LastID  string             `json:"last_id,omitempty"`
    HasMore bool               `json:"has_more"`
}

// ItemIncludeOptions represents the include parameter for listing items
type ItemIncludeOptions []string

// Common include options
const (
    IncludeWebSearchSources     = "web_search_call.action.sources"
    IncludeCodeInterpreterOutput = "code_interpreter_call.outputs"
    IncludeComputerCallImage     = "computer_call_output.output.image_url"
    IncludeFileSearchResults     = "file_search_call.results"
    IncludeInputImageURL         = "message.input_image.image_url"
    IncludeOutputLogProbs        = "message.output_text.logprobs"
    IncludeReasoningEncrypted    = "reasoning.encrypted_content"
)
