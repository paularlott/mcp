package claude

import (
	"encoding/json"
	"testing"

	"github.com/paularlott/mcp/ai/openai"
)


func TestMessagesRequestToOpenAI(t *testing.T) {
	tests := []struct {
		name     string
		req      MessagesRequest
		expected openai.ChatCompletionRequest
	}{
		{
			name: "Basic Request with System",
			req: MessagesRequest{
				Model:       "test-model",
				MaxTokens:   500,
				Temperature: floatPtr(0.5),
				System:      SystemField{text: "System prompt"},
				Messages: []ClaudeMessage{
					{
						Role: "user",
						Content: MessageContent{blocks: []ContentBlock{
							{Type: "text", Text: "Hello user"},
						}},
					},
				},
			},
			expected: openai.ChatCompletionRequest{
				Model:       "test-model",
				MaxTokens:   500,
				Temperature: floatPtr(0.5),
				Messages: []openai.Message{
					{Role: "system", Content: "System prompt"},
					{Role: "user", Content: "Hello user"},
				},
			},
		},
		{
			name: "Request with Tools and tool_use",
			req: MessagesRequest{
				Model: "test-model",
				Tools: []ClaudeTool{
					{
						Name:        "get_time",
						Description: "Get time",
						InputSchema: map[string]any{"type": "object"},
					},
				},
				Messages: []ClaudeMessage{
					{
						Role: "assistant",
						Content: MessageContent{blocks: []ContentBlock{
							{Type: "text", Text: "I will check the time."},
							{Type: "tool_use", ID: "call_abc", Name: "get_time", Input: map[string]any{}},
						}},
					},
				},
			},
			expected: openai.ChatCompletionRequest{
				Model: "test-model",
				Tools: []openai.Tool{
					{
						Type: "function",
						Function: openai.ToolFunction{
							Name:        "get_time",
							Description: "Get time",
							Parameters:  map[string]any{"type": "object"},
						},
					},
				},
				Messages: []openai.Message{
					{
						Role:    "assistant",
						Content: "I will check the time.",
						ToolCalls: []openai.ToolCall{
							{
								ID:   "call_abc",
								Type: "function",
								Function: openai.ToolCallFunction{
									Name:      "get_time",
									Arguments: map[string]any{},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "Request with tool_result",
			req: MessagesRequest{
				Model: "test-model",
				Messages: []ClaudeMessage{
					{
						Role: "user",
						Content: MessageContent{blocks: []ContentBlock{
							{Type: "tool_result", ToolUseID: "call_abc", Content: "12:00 PM"},
						}},
					},
				},
			},
			expected: openai.ChatCompletionRequest{
				Model: "test-model",
				Messages: []openai.Message{
					{
						Role:       "tool",
						Content:    "12:00 PM",
						ToolCallID: "call_abc",
					},
				},
			},
		},
		{
			name: "Multiple tool_results in single message",
			req: MessagesRequest{
				Model: "test-model",
				Messages: []ClaudeMessage{
					{
						Role: "user",
						Content: MessageContent{blocks: []ContentBlock{
							{Type: "tool_result", ToolUseID: "call_1", Content: "Result 1"},
							{Type: "tool_result", ToolUseID: "call_2", Content: "Result 2"},
						}},
					},
				},
			},
			expected: openai.ChatCompletionRequest{
				Model: "test-model",
				Messages: []openai.Message{
					{Role: "tool", Content: "Result 1", ToolCallID: "call_1"},
					{Role: "tool", Content: "Result 2", ToolCallID: "call_2"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MessagesRequestToOpenAI(&tt.req)

			bGot, _ := json.Marshal(got)
			bExp, _ := json.Marshal(tt.expected)
			if string(bGot) != string(bExp) {
				t.Errorf("Request = %v\nwant %v", string(bGot), string(bExp))
			}
		})
	}
}

func TestOpenAIToMessagesResponse(t *testing.T) {
	tests := []struct {
		name     string
		resp     openai.ChatCompletionResponse
		expected MessagesResponse
	}{
		{
			name: "Basic Response",
			resp: openai.ChatCompletionResponse{
				ID:    "msg_123",
				Model: "test-model",
				Choices: []openai.Choice{
					{
						Message:      openai.Message{Content: "Hello!"},
						FinishReason: "stop",
					},
				},
				Usage: &openai.Usage{PromptTokens: 10, CompletionTokens: 20},
			},
			expected: MessagesResponse{
				ID:         "msg_123",
				Type:       "message",
				Role:       "assistant",
				Model:      "test-model",
				Content:    []ContentBlock{{Type: "text", Text: "Hello!"}},
				StopReason: "end_turn",
				Usage:      MessagesUsage{InputTokens: 10, OutputTokens: 20},
			},
		},
		{
			name: "Tool Call Response",
			resp: openai.ChatCompletionResponse{
				ID:    "msg_123",
				Model: "test-model",
				Choices: []openai.Choice{
					{
						Message: openai.Message{
							Content: "Let me check.",
							ToolCalls: []openai.ToolCall{
								{
									ID:   "call_abc",
									Type: "function",
									Function: openai.ToolCallFunction{
										Name:      "get_time",
										Arguments: map[string]any{"zone": "UTC"},
									},
								},
							},
						},
						FinishReason: "tool_calls",
					},
				},
			},
			expected: MessagesResponse{
				ID:         "msg_123",
				Type:       "message",
				Role:       "assistant",
				Model:      "test-model",
				Content: []ContentBlock{
					{Type: "text", Text: "Let me check."},
					{Type: "tool_use", ID: "call_abc", Name: "get_time", Input: map[string]any{"zone": "UTC"}},
				},
				StopReason: "tool_use",
			},
		},
		{
			name: "Only Tool Call Response (No Text)",
			resp: openai.ChatCompletionResponse{
				ID:    "msg_123",
				Model: "test-model",
				Choices: []openai.Choice{
					{
						Message: openai.Message{
							ToolCalls: []openai.ToolCall{
								{
									ID:   "call_abc",
									Type: "function",
									Function: openai.ToolCallFunction{
										Name:      "get_time",
										Arguments: map[string]any{},
									},
								},
							},
						},
						FinishReason: "tool_calls",
					},
				},
			},
			expected: MessagesResponse{
				ID:         "msg_123",
				Type:       "message",
				Role:       "assistant",
				Model:      "test-model",
				Content: []ContentBlock{
					{Type: "tool_use", ID: "call_abc", Name: "get_time", Input: map[string]any{}},
				},
				StopReason: "tool_use",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := OpenAIToMessagesResponse(&tt.resp)

			bGot, _ := json.Marshal(got)
			bExp, _ := json.Marshal(tt.expected)

			if string(bGot) != string(bExp) {
				t.Errorf("Response = %v\nwant %v", string(bGot), string(bExp))
			}
		})
	}
}
