package openai

import (
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"empty", "", 0},
		{"single char", "a", 1},
		{"short word", "hello", 2},
		{"sentence", "Hello, world!", 4},
		{"longer text", "The quick brown fox jumps over the lazy dog", 11},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateTokens(tt.input)
			if got != tt.want {
				t.Errorf("EstimateTokens(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestMessageFromMap(t *testing.T) {
	t.Run("basic message", func(t *testing.T) {
		m := map[string]any{
			"role":    "user",
			"content": "Hello!",
		}
		msg := MessageFromMap(m)
		if msg.Role != "user" {
			t.Errorf("Role = %q, want %q", msg.Role, "user")
		}
		if msg.Content != "Hello!" {
			t.Errorf("Content = %v, want %q", msg.Content, "Hello!")
		}
	})

	t.Run("with tool_call_id", func(t *testing.T) {
		m := map[string]any{
			"role":         "tool",
			"content":      "result",
			"tool_call_id": "call_123",
		}
		msg := MessageFromMap(m)
		if msg.ToolCallID != "call_123" {
			t.Errorf("ToolCallID = %q, want %q", msg.ToolCallID, "call_123")
		}
	})

	t.Run("with tool calls", func(t *testing.T) {
		m := map[string]any{
			"role":    "assistant",
			"content": "",
			"tool_calls": []any{
				map[string]any{
					"id":   "call_1",
					"type": "function",
					"function": map[string]any{
						"name":      "get_weather",
						"arguments": `{"city": "Paris"}`,
					},
				},
			},
		}
		msg := MessageFromMap(m)
		if len(msg.ToolCalls) != 1 {
			t.Fatalf("ToolCalls length = %d, want 1", len(msg.ToolCalls))
		}
		if msg.ToolCalls[0].ID != "call_1" {
			t.Errorf("ToolCall ID = %q, want %q", msg.ToolCalls[0].ID, "call_1")
		}
		if msg.ToolCalls[0].Function.Name != "get_weather" {
			t.Errorf("Function Name = %q, want %q", msg.ToolCalls[0].Function.Name, "get_weather")
		}
		if msg.ToolCalls[0].Function.Arguments["city"] != "Paris" {
			t.Errorf("Function Arguments = %v, want city=Paris", msg.ToolCalls[0].Function.Arguments)
		}
	})

	t.Run("tool calls with map arguments", func(t *testing.T) {
		m := map[string]any{
			"role":    "assistant",
			"content": "",
			"tool_calls": []any{
				map[string]any{
					"id":   "call_2",
					"type": "function",
					"function": map[string]any{
						"name": "search",
						"arguments": map[string]any{
							"query": "test",
						},
					},
				},
			},
		}
		msg := MessageFromMap(m)
		if len(msg.ToolCalls) != 1 {
			t.Fatalf("ToolCalls length = %d, want 1", len(msg.ToolCalls))
		}
		if msg.ToolCalls[0].Function.Arguments["query"] != "test" {
			t.Errorf("Function Arguments = %v, want query=test", msg.ToolCalls[0].Function.Arguments)
		}
	})

	t.Run("empty map", func(t *testing.T) {
		msg := MessageFromMap(map[string]any{})
		if msg.Role != "" {
			t.Errorf("Role = %q, want empty", msg.Role)
		}
		if len(msg.ToolCalls) != 0 {
			t.Errorf("ToolCalls = %d, want 0", len(msg.ToolCalls))
		}
	})
}

func TestTokenCounterAddPromptTokensFromMaps(t *testing.T) {
	tc := NewTokenCounter()

	messages := []map[string]any{
		{"role": "system", "content": "You are helpful"},
		{"role": "user", "content": "Hello!"},
	}
	tc.AddPromptTokensFromMaps(messages)

	usage := tc.GetUsage()
	if usage.PromptTokens == 0 {
		t.Error("PromptTokens should be > 0 after adding messages")
	}
	if usage.CompletionTokens != 0 {
		t.Error("CompletionTokens should be 0")
	}
}

func TestTokenCounterAddPromptTokensFromMapsVsMessages(t *testing.T) {
	// Same messages via maps vs typed structs should give same results
	mapsMessages := []map[string]any{
		{"role": "system", "content": "You are helpful."},
		{"role": "user", "content": "What is 2+2?"},
	}
	typedMessages := []Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "What is 2+2?"},
	}

	tc1 := NewTokenCounter()
	tc1.AddPromptTokensFromMaps(mapsMessages)

	tc2 := NewTokenCounter()
	tc2.AddPromptTokensFromMessages(typedMessages)

	u1 := tc1.GetUsage()
	u2 := tc2.GetUsage()

	if u1.PromptTokens != u2.PromptTokens {
		t.Errorf("Map-based (%d) != Typed (%d) prompt tokens", u1.PromptTokens, u2.PromptTokens)
	}
}

func TestTokenCounterAddCompletionTokensFromResponseMap(t *testing.T) {
	t.Run("chat completions format", func(t *testing.T) {
		tc := NewTokenCounter()
		response := map[string]any{
			"choices": []any{
				map[string]any{
					"message": map[string]any{
						"role":    "assistant",
						"content": "4",
					},
				},
			},
		}
		tc.AddCompletionTokensFromResponseMap(response)

		usage := tc.GetUsage()
		if usage.CompletionTokens == 0 {
			t.Error("CompletionTokens should be > 0")
		}
		if usage.PromptTokens != 0 {
			t.Error("PromptTokens should be 0")
		}
	})

	t.Run("with tool calls", func(t *testing.T) {
		tc := NewTokenCounter()
		response := map[string]any{
			"choices": []any{
				map[string]any{
					"message": map[string]any{
						"role":    "assistant",
						"content": "",
						"tool_calls": []any{
							map[string]any{
								"id":   "call_1",
								"type": "function",
								"function": map[string]any{
									"name":      "get_weather",
									"arguments": `{"city": "Paris"}`,
								},
							},
						},
					},
				},
			},
		}
		tc.AddCompletionTokensFromResponseMap(response)

		usage := tc.GetUsage()
		if usage.CompletionTokens == 0 {
			t.Error("CompletionTokens should be > 0 with tool calls")
		}
	})

	t.Run("responses API format", func(t *testing.T) {
		tc := NewTokenCounter()
		response := map[string]any{
			"output": []any{
				map[string]any{
					"type": "message",
					"content": []any{
						map[string]any{
							"type": "output_text",
							"text": "The answer is 42.",
						},
					},
				},
			},
		}
		tc.AddCompletionTokensFromResponseMap(response)

		usage := tc.GetUsage()
		if usage.CompletionTokens == 0 {
			t.Error("CompletionTokens should be > 0 for Responses API format")
		}
	})

	t.Run("empty response", func(t *testing.T) {
		tc := NewTokenCounter()
		tc.AddCompletionTokensFromResponseMap(map[string]any{})

		usage := tc.GetUsage()
		if usage.CompletionTokens != 0 {
			t.Error("CompletionTokens should be 0 for empty response")
		}
	})
}

func TestTokenCounterFullRoundTrip(t *testing.T) {
	tc := NewTokenCounter()

	// Add prompt
	tc.AddPromptTokensFromMaps([]map[string]any{
		{"role": "system", "content": "You are a helpful assistant."},
		{"role": "user", "content": "What is the capital of France?"},
	})

	// Add completion from response
	tc.AddCompletionTokensFromResponseMap(map[string]any{
		"choices": []any{
			map[string]any{
				"message": map[string]any{
					"role":    "assistant",
					"content": "The capital of France is Paris.",
				},
			},
		},
	})

	usage := tc.GetUsage()
	if usage.PromptTokens == 0 {
		t.Error("PromptTokens should be > 0")
	}
	if usage.CompletionTokens == 0 {
		t.Error("CompletionTokens should be > 0")
	}
	if usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Errorf("TotalTokens (%d) != PromptTokens (%d) + CompletionTokens (%d)",
			usage.TotalTokens, usage.PromptTokens, usage.CompletionTokens)
	}
}
