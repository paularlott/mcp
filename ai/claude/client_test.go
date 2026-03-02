package claude

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/paularlott/mcp/ai/openai"
)

func TestConvertToClaudeRequest(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name     string
		req      openai.ChatCompletionRequest
		expected ClaudeRequest
	}{
		{
			name: "Basic Request with System Prompt",
			req: openai.ChatCompletionRequest{
				Model:       "claude-test",
				MaxTokens:   1000,
				Temperature: floatPtr(0.7),
				Messages: []openai.Message{
					{Role: "system", Content: "You are a helpful assistant."},
					{Role: "user", Content: "Hello!"},
				},
			},
			expected: ClaudeRequest{
				Model:       "claude-test",
				MaxTokens:   1000,
				Temperature: floatPtr(0.7),
				System:      SystemField{text: "You are a helpful assistant."},
				Messages: []ClaudeMessage{
					{Role: "user", Content: MessageContent{blocks: []ContentBlock{{Type: "text", Text: "Hello!"}}}},
				},
			},
		},
		{
			name: "Tools and Tool Calls",
			req: openai.ChatCompletionRequest{
				Model: "claude-test",
				Tools: []openai.Tool{
					{
						Type: "function",
						Function: openai.ToolFunction{
							Name:        "get_weather",
							Description: "Get weather",
							Parameters:  map[string]any{"type": "object", "properties": map[string]any{"loc": map[string]any{"type": "string"}}},
						},
					},
				},
				Messages: []openai.Message{
					{Role: "user", Content: "weather?"},
					{
						Role:    "assistant",
						Content: "Let me check.",
						ToolCalls: []openai.ToolCall{
							{
								ID:   "call_123",
								Type: "function",
								Function: openai.ToolCallFunction{
									Name:      "get_weather",
									Arguments: map[string]any{"loc": "London"},
								},
							},
						},
					},
					{
						Role:       "tool",
						Content:    "Sunny",
						ToolCallID: "call_123",
					},
				},
			},
			expected: ClaudeRequest{
				Model: "claude-test",
				Tools: []ClaudeTool{
					{
						Name:        "get_weather",
						Description: "Get weather",
						InputSchema: map[string]any{"type": "object", "properties": map[string]any{"loc": map[string]any{"type": "string"}}},
					},
				},
				Messages: []ClaudeMessage{
					{Role: "user", Content: MessageContent{blocks: []ContentBlock{{Type: "text", Text: "weather?"}}}},
					{
						Role: "assistant",
						Content: MessageContent{
							blocks: []ContentBlock{
								{Type: "text", Text: "Let me check."},
								{Type: "tool_use", ID: "call_123", Name: "get_weather", Input: map[string]any{"loc": "London"}},
							},
						},
					},
					{
						Role: "user",
						Content: MessageContent{
							blocks: []ContentBlock{
								{Type: "tool_result", ToolUseID: "call_123", Content: "Sunny"},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := client.convertToClaudeRequest(tt.req)
			if !reflect.DeepEqual(got.Model, tt.expected.Model) {
				t.Errorf("Model = %v, want %v", got.Model, tt.expected.Model)
			}
			if got.System.text != tt.expected.System.text {
				t.Errorf("System = %v, want %v", got.System.text, tt.expected.System.text)
			}

			bGot, _ := json.Marshal(got.Messages)
			bExp, _ := json.Marshal(tt.expected.Messages)
			if string(bGot) != string(bExp) {
				t.Errorf("Messages = %v\nwant %v", string(bGot), string(bExp))
			}

			bGotT, _ := json.Marshal(got.Tools)
			bExpT, _ := json.Marshal(tt.expected.Tools)
			if string(bGotT) != string(bExpT) {
				t.Errorf("Tools = %v\nwant %v", string(bGotT), string(bExpT))
			}
		})
	}
}

func TestConvertToOpenAIResponse(t *testing.T) {
	client := &Client{}

	tests := []struct {
		name     string
		resp     ClaudeResponse
		expected openai.ChatCompletionResponse
	}{
		{
			name: "Basic Response",
			resp: ClaudeResponse{
				ID:    "msg_123",
				Model: "claude-test",
				Role:  "assistant",
				Content: []ContentBlock{
					{Type: "text", Text: "Hello there!"},
				},
				StopReason: "end_turn",
				Usage: ClaudeUsage{
					InputTokens:  10,
					OutputTokens: 20,
				},
			},
			expected: openai.ChatCompletionResponse{
				ID:      "msg_123",
				Object:  "chat.completion",
				Model:   "claude-test",
				Choices: []openai.Choice{
					{
						Index: 0,
						Message: openai.Message{
							Role:    "assistant",
							Content: "Hello there!",
						},
						FinishReason: "stop",
					},
				},
				Usage: &openai.Usage{
					PromptTokens:     10,
					CompletionTokens: 20,
					TotalTokens:      30,
				},
			},
		},
		{
			name: "Tool Use Response",
			resp: ClaudeResponse{
				ID:    "msg_123",
				Model: "claude-test",
				Role:  "assistant",
				Content: []ContentBlock{
					{Type: "text", Text: "I will check."},
					{
						Type:  "tool_use",
						ID:    "call_123",
						Name:  "get_weather",
						Input: map[string]any{"loc": "London"},
					},
				},
				StopReason: "tool_use",
				Usage: ClaudeUsage{
					InputTokens:  15,
					OutputTokens: 25,
				},
			},
			expected: openai.ChatCompletionResponse{
				ID:      "msg_123",
				Object:  "chat.completion",
				Model:   "claude-test",
				Choices: []openai.Choice{
					{
						Index: 0,
						Message: openai.Message{
							Role:    "assistant",
							Content: "I will check.",
							ToolCalls: []openai.ToolCall{
								{
									ID:   "call_123",
									Type: "function",
									Function: openai.ToolCallFunction{
										Name:      "get_weather",
										Arguments: map[string]any{"loc": "London"},
									},
								},
							},
						},
						FinishReason: "tool_calls",
					},
				},
				Usage: &openai.Usage{
					PromptTokens:     15,
					CompletionTokens: 25,
					TotalTokens:      40,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := client.convertToOpenAIResponse(&tt.resp)

			bGot, _ := json.Marshal(got)
			bExp, _ := json.Marshal(tt.expected)
			if string(bGot) != string(bExp) {
				t.Errorf("Response = %v\nwant %v", string(bGot), string(bExp))
			}
		})
	}
}

func floatPtr(f float64) *float64 { return &f }

func TestConvertToClaudeRequest_EdgeCases(t *testing.T) {
	client := &Client{}
	tests := []struct {
		name     string
		req      openai.ChatCompletionRequest
		expected ClaudeRequest
	}{
		{
			name: "Multiple System Prompts (merged or overridden?)",
			// Let's see what happens if multiple system prompts are passed. Note that the code loops and takes the last one! Wait, no.
// `system` is a SystemField. For each system message, `claudeReq.System = SystemField{text: msg.GetContentAsString()}`
// So it overwrites it!
req: openai.ChatCompletionRequest{
Model: "test",
Messages: []openai.Message{
{Role: "system", Content: "Prompt 1"},
{Role: "system", Content: "Prompt 2"},
{Role: "user", Content: "Hi"},
},
},
expected: ClaudeRequest{
Model: "test",
System: SystemField{text: "Prompt 2"},
Messages: []ClaudeMessage{
{Role: "user", Content: MessageContent{blocks: []ContentBlock{{Type: "text", Text: "Hi"}}}},
},
},
},
{
name: "Mixed Content Array Stringification",
req: openai.ChatCompletionRequest{
Model: "test",
Messages: []openai.Message{
{
Role: "user",
Content: []any{
map[string]any{"type": "text", "text": "Hello "},
map[string]any{"type": "text", "text": "World"},
},
},
},
},
expected: ClaudeRequest{
Model: "test",
Messages: []ClaudeMessage{
{Role: "user", Content: MessageContent{blocks: []ContentBlock{{Type: "text", Text: "Hello World"}}}},
},
},
},
}

for _, tt := range tests {
t.Run(tt.name, func(t *testing.T) {
got := client.convertToClaudeRequest(tt.req)
if got.System.text != tt.expected.System.text {
t.Errorf("System = %v, want %v", got.System.text, tt.expected.System.text)
}
bGot, _ := json.Marshal(got.Messages)
bExp, _ := json.Marshal(tt.expected.Messages)
if string(bGot) != string(bExp) {
t.Errorf("Messages = %v\nwant %v", string(bGot), string(bExp))
}
})
}
}

func TestConvertToOpenAIResponse_EdgeCases(t *testing.T) {
client := &Client{}
tests := []struct {
name     string
resp     ClaudeResponse
expected openai.ChatCompletionResponse
}{
{
name: "Multiple Tool Use Blocks",
resp: ClaudeResponse{
ID: "123", Model: "test", Role: "assistant", StopReason: "tool_use",
Content: []ContentBlock{
{Type: "tool_use", ID: "call_1", Name: "tool_a", Input: map[string]any{"a": 1}},
{Type: "tool_use", ID: "call_2", Name: "tool_b", Input: map[string]any{"b": 2}},
},
},
expected: openai.ChatCompletionResponse{
ID: "123", Object: "chat.completion", Model: "test",
Choices: []openai.Choice{
{
Index: 0,
Message: openai.Message{
Role: "assistant",
Content: "",
ToolCalls: []openai.ToolCall{
{ID: "call_1", Type: "function", Function: openai.ToolCallFunction{Name: "tool_a", Arguments: map[string]any{"a": 1}}},
{ID: "call_2", Type: "function", Function: openai.ToolCallFunction{Name: "tool_b", Arguments: map[string]any{"b": 2}}},
},
},
FinishReason: "tool_calls",
},
},
Usage: &openai.Usage{},
},
},
}
for _, tt := range tests {
t.Run(tt.name, func(t *testing.T) {
got := client.convertToOpenAIResponse(&tt.resp)
bGot, _ := json.Marshal(got)
bExp, _ := json.Marshal(tt.expected)
if string(bGot) != string(bExp) {
t.Errorf("Response = %v\nwant %v", string(bGot), string(bExp))
}
})
}
}
