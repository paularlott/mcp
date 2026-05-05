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
