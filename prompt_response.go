package mcp

// NewPromptTextMessage builds a single-content text message with the given role.
func NewPromptTextMessage(role PromptMessageRole, text string) PromptMessage {
	return PromptMessage{
		Role:    role,
		Content: PromptMessageContent{Type: "text", Text: text},
	}
}

// NewPromptResponseText builds a prompt response containing a single user text
// message. This is the common case where the prompt renders one block of text.
func NewPromptResponseText(text string) *PromptResponse {
	return &PromptResponse{
		Messages: []PromptMessage{NewPromptTextMessage(PromptRoleUser, text)},
	}
}

// NewPromptResponseMessages builds a prompt response from the given messages.
// Use this when a prompt renders multiple messages or non-user roles.
func NewPromptResponseMessages(messages ...PromptMessage) *PromptResponse {
	return &PromptResponse{Messages: messages}
}
