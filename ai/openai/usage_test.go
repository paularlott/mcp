package openai

import (
	"encoding/json"
	"testing"
)

// Reasoning tokens count in CompletionTokens whichever way the provider
// reports them.
func TestUsage_ReasoningTokensCounted(t *testing.T) {
	for name, tc := range map[string]struct {
		body       string
		completion int
	}{
		// xAI: reasoning outside completion_tokens, inside total_tokens
		"reported separately": {`{"prompt_tokens":1252,"completion_tokens":19,"total_tokens":1343,"completion_tokens_details":{"reasoning_tokens":72}}`, 91},
		// OpenAI: reasoning already inside completion_tokens
		"already included": {`{"prompt_tokens":100,"completion_tokens":91,"total_tokens":191,"completion_tokens_details":{"reasoning_tokens":72}}`, 91},
		"no reasoning":     {`{"prompt_tokens":100,"completion_tokens":19,"total_tokens":119}`, 19},
		"no total":         {`{"prompt_tokens":100,"completion_tokens":19,"completion_tokens_details":{"reasoning_tokens":72}}`, 19},
	} {
		t.Run(name, func(t *testing.T) {
			var u Usage
			if err := json.Unmarshal([]byte(tc.body), &u); err != nil {
				t.Fatal(err)
			}
			if u.CompletionTokens != tc.completion {
				t.Errorf("completion tokens = %d, want %d", u.CompletionTokens, tc.completion)
			}
			// Decoding the encoded usage again changes nothing
			data, _ := json.Marshal(u)
			var again Usage
			json.Unmarshal(data, &again)
			if again.CompletionTokens != u.CompletionTokens {
				t.Errorf("re-decoded completion tokens = %d, want %d", again.CompletionTokens, u.CompletionTokens)
			}
		})
	}

	// Within a chat completion response, as the client decodes it
	var resp ChatCompletionResponse
	json.Unmarshal([]byte(`{"id":"c","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":45,"completion_tokens_details":{"reasoning_tokens":30}}}`), &resp)
	if resp.Usage == nil || resp.Usage.CompletionTokens != 35 {
		t.Errorf("response usage = %+v, want 35 completion tokens", resp.Usage)
	}
}
