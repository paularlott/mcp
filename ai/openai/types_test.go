package openai

import (
	"encoding/json"
	"testing"
)

func TestChatCompletionRequestExtraBodyMergesIntoTopLevelJSON(t *testing.T) {
	req := ChatCompletionRequest{
		Model: "glm-4.7",
		Messages: []Message{
			{Role: "user", Content: "hello"},
		},
		ExtraBody: map[string]any{
			"thinking": map[string]any{
				"type":           "enabled",
				"clear_thinking": false,
			},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if _, ok := body["extra_body"]; ok {
		t.Fatalf("extra_body should not be sent literally: %s", string(data))
	}
	thinking, ok := body["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("thinking was not merged into request body: %s", string(data))
	}
	if thinking["type"] != "enabled" {
		t.Fatalf("thinking.type = %#v, want enabled", thinking["type"])
	}
	if thinking["clear_thinking"] != false {
		t.Fatalf("thinking.clear_thinking = %#v, want false", thinking["clear_thinking"])
	}
}

func TestChatCompletionRequestUnmarshalCapturesExtraBody(t *testing.T) {
	data := []byte(`{
		"model": "glm-4.7",
		"messages": [{"role": "user", "content": "hello"}],
		"thinking": {"type": "enabled"},
		"extra_body": {"custom_flag": true}
	}`)

	var req ChatCompletionRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if req.Model != "glm-4.7" {
		t.Fatalf("Model = %q, want glm-4.7", req.Model)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
		t.Fatalf("Messages = %#v, want one user message", req.Messages)
	}

	thinking, ok := req.ExtraBody["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("thinking was not captured in ExtraBody: %#v", req.ExtraBody)
	}
	if thinking["type"] != "enabled" {
		t.Fatalf("thinking.type = %#v, want enabled", thinking["type"])
	}
	if req.ExtraBody["custom_flag"] != true {
		t.Fatalf("custom_flag = %#v, want true", req.ExtraBody["custom_flag"])
	}

	marshaled, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(marshaled, &body); err != nil {
		t.Fatalf("Unmarshal marshaled body failed: %v", err)
	}
	if _, ok := body["extra_body"]; ok {
		t.Fatalf("extra_body should not be emitted literally: %s", string(marshaled))
	}
	if _, ok := body["thinking"]; !ok {
		t.Fatalf("thinking should be re-emitted at top level: %s", string(marshaled))
	}
	if body["custom_flag"] != true {
		t.Fatalf("custom_flag = %#v, want true", body["custom_flag"])
	}
}

func TestMessage_GetContentAsString(t *testing.T) {
	tests := []struct {
		name    string
		content any
		want    string
	}{
		{"nil", nil, ""},
		{"string", "hello", "hello"},
		{
			"array of parts",
			[]any{
				map[string]any{"type": "text", "text": "part1 "},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "x"}},
				map[string]any{"type": "text", "text": "part2"},
			},
			"part1 part2",
		},
		{"unsupported type", 42, ""},
		{"empty array", []any{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Message{Content: tt.content}
			if got := m.GetContentAsString(); got != tt.want {
				t.Errorf("GetContentAsString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMessage_GetContentAsParts(t *testing.T) {
	t.Run("nil content", func(t *testing.T) {
		m := Message{}
		if parts := m.GetContentAsParts(); parts != nil {
			t.Errorf("parts = %v, want nil", parts)
		}
	})

	t.Run("string content", func(t *testing.T) {
		m := Message{Content: "hi"}
		if parts := m.GetContentAsParts(); parts != nil {
			t.Errorf("parts = %v, want nil for string content", parts)
		}
	})

	t.Run("already typed", func(t *testing.T) {
		want := []ContentPart{TextContentPart("hi")}
		m := Message{Content: want}
		parts := m.GetContentAsParts()
		if len(parts) != 1 || parts[0].Text != "hi" {
			t.Errorf("parts = %+v", parts)
		}
	})

	t.Run("json-decoded any slice", func(t *testing.T) {
		m := Message{Content: []any{
			map[string]any{"type": "text", "text": "hello"},
		}}
		parts := m.GetContentAsParts()
		if len(parts) != 1 || parts[0].Type != "text" || parts[0].Text != "hello" {
			t.Errorf("parts = %+v", parts)
		}
	})

	t.Run("unmarshalable content", func(t *testing.T) {
		m := Message{Content: make(chan int)}
		if parts := m.GetContentAsParts(); parts != nil {
			t.Errorf("parts = %v, want nil on marshal error", parts)
		}
	})
}

func TestMessage_SetContentAsString(t *testing.T) {
	m := Message{}
	m.SetContentAsString("hello")
	if m.Content != "hello" {
		t.Errorf("Content = %v, want hello", m.Content)
	}
}

func TestDelta_GetReasoningContent(t *testing.T) {
	d := Delta{ReasoningContent: "primary"}
	if got := d.GetReasoningContent(); got != "primary" {
		t.Errorf("got = %q, want primary", got)
	}

	d2 := Delta{Reasoning: "fallback"}
	if got := d2.GetReasoningContent(); got != "fallback" {
		t.Errorf("got = %q, want fallback", got)
	}

	d3 := Delta{}
	if got := d3.GetReasoningContent(); got != "" {
		t.Errorf("got = %q, want empty", got)
	}
}

func TestUsage_Add(t *testing.T) {
	u := &Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	u.Add(&Usage{PromptTokens: 3, CompletionTokens: 2})
	if u.PromptTokens != 13 || u.CompletionTokens != 7 || u.TotalTokens != 20 {
		t.Errorf("u = %+v", u)
	}
}

func TestUsage_Add_Nil(t *testing.T) {
	u := &Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	u.Add(nil)
	if u.PromptTokens != 10 || u.CompletionTokens != 5 || u.TotalTokens != 15 {
		t.Errorf("u changed after Add(nil): %+v", u)
	}
}

func TestResponseUsage_Add(t *testing.T) {
	u := &ResponseUsage{InputTokens: 10, OutputTokens: 5}
	u.Add(&ResponseUsage{InputTokens: 3, OutputTokens: 2})
	if u.InputTokens != 13 || u.OutputTokens != 7 || u.TotalTokens != 20 {
		t.Errorf("u = %+v", u)
	}
}

func TestResponseUsage_Add_Nil(t *testing.T) {
	u := &ResponseUsage{InputTokens: 10, OutputTokens: 5}
	u.Add(nil)
	if u.InputTokens != 10 || u.OutputTokens != 5 {
		t.Errorf("u changed after Add(nil): %+v", u)
	}
}

func TestToolCallFunction_MarshalJSON(t *testing.T) {
	tcf := ToolCallFunction{Name: "get_weather", Arguments: map[string]any{"city": "NYC"}}
	data, err := json.Marshal(tcf)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if raw["name"] != "get_weather" {
		t.Errorf("name = %v", raw["name"])
	}
	argsStr, ok := raw["arguments"].(string)
	if !ok {
		t.Fatalf("arguments should be a JSON string, got %T", raw["arguments"])
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(argsStr), &args); err != nil {
		t.Fatalf("arguments not valid JSON: %v", err)
	}
	if args["city"] != "NYC" {
		t.Errorf("args = %v", args)
	}
}

func TestToolCallFunction_UnmarshalJSON(t *testing.T) {
	data := []byte(`{"name":"get_weather","arguments":"{\"city\":\"NYC\"}"}`)
	var tcf ToolCallFunction
	if err := json.Unmarshal(data, &tcf); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if tcf.Name != "get_weather" {
		t.Errorf("Name = %q", tcf.Name)
	}
	if tcf.Arguments["city"] != "NYC" {
		t.Errorf("Arguments = %v", tcf.Arguments)
	}
}

func TestToolCallFunction_UnmarshalJSON_EmptyArguments(t *testing.T) {
	data := []byte(`{"name":"f","arguments":""}`)
	var tcf ToolCallFunction
	if err := json.Unmarshal(data, &tcf); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if tcf.Arguments == nil {
		t.Error("expected non-nil empty map")
	}
	if len(tcf.Arguments) != 0 {
		t.Errorf("Arguments = %v, want empty", tcf.Arguments)
	}
}

func TestToolCallFunction_UnmarshalJSON_InvalidArgumentsJSON(t *testing.T) {
	data := []byte(`{"name":"f","arguments":"not json"}`)
	var tcf ToolCallFunction
	if err := json.Unmarshal(data, &tcf); err == nil {
		t.Fatal("expected error unmarshaling invalid arguments JSON")
	}
}

func TestToolCallFunction_UnmarshalJSON_Malformed(t *testing.T) {
	var tcf ToolCallFunction
	if err := json.Unmarshal([]byte(`not json at all`), &tcf); err == nil {
		t.Fatal("expected error for malformed outer JSON")
	}
}
