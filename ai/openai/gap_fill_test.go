package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/paularlott/mcp"
)

// -----------------------------------------------------------------------------
// accumulator.go
// -----------------------------------------------------------------------------

func TestCompletionAccumulator_FinishedRefusal_ChoiceExistsButEmpty(t *testing.T) {
	acc := &CompletionAccumulator{}
	acc.AddChunk(ChatCompletionResponse{Choices: []Choice{{Index: 0, Delta: Delta{Content: "hi"}}}})
	if refusal, ok := acc.FinishedRefusal(); ok || refusal != "" {
		t.Errorf("FinishedRefusal() = %q, %v, want empty, false", refusal, ok)
	}
}

// -----------------------------------------------------------------------------
// helpers.go
// -----------------------------------------------------------------------------

func TestParseToolArguments_MarshalError(t *testing.T) {
	// A channel value cannot be marshaled to JSON, so ParseToolArguments must
	// surface the marshal error rather than panicking.
	args := map[string]any{"bad": make(chan int)}
	var target map[string]any
	if err := ParseToolArguments(args, &target); err == nil {
		t.Fatal("expected marshal error")
	}
}

// -----------------------------------------------------------------------------
// response.go
// -----------------------------------------------------------------------------

func TestExtractToolResult_StructuredContent_MarshalError(t *testing.T) {
	resp := &mcp.ToolResponse{StructuredContent: make(chan int)}
	_, err := ExtractToolResult(resp)
	if err == nil {
		t.Fatal("expected marshal error for unmarshalable structured content")
	}
}

// -----------------------------------------------------------------------------
// tokens.go
// -----------------------------------------------------------------------------

func TestEstimateTokens_Empty(t *testing.T) {
	if got := EstimateTokens(""); got != 0 {
		t.Errorf("EstimateTokens(\"\") = %d, want 0", got)
	}
}

func TestTokenCounter_AddCompletionTokensFromMessage_Nil(t *testing.T) {
	tc := NewTokenCounter()
	tc.AddCompletionTokensFromMessage(nil)
	if tc.GetUsage().CompletionTokens != 0 {
		t.Error("expected zero CompletionTokens for nil message")
	}
}

func TestTokenCounter_estimateArgsTokens_MarshalError(t *testing.T) {
	tc := NewTokenCounter()
	args := map[string]any{"bad": make(chan int)}
	if tokens := tc.estimateArgsTokens(args); tokens != 0 {
		t.Errorf("tokens = %d, want 0 on marshal error", tokens)
	}
}

// -----------------------------------------------------------------------------
// types.go
// -----------------------------------------------------------------------------

func TestChatCompletionRequest_MarshalJSON_NoExtraBody(t *testing.T) {
	req := ChatCompletionRequest{Model: "gpt-x", Messages: []Message{{Role: "user", Content: "hi"}}}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	var body map[string]any
	json.Unmarshal(data, &body)
	if _, ok := body["extra_body"]; ok {
		t.Errorf("extra_body should not appear: %s", data)
	}
}

func TestChatCompletionRequest_UnmarshalJSON_NoExtraFields(t *testing.T) {
	data := []byte(`{"model":"gpt-x","messages":[]}`)
	var req ChatCompletionRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if req.ExtraBody != nil {
		t.Errorf("ExtraBody = %v, want nil", req.ExtraBody)
	}
}

func TestMessage_GetContentAsParts_EmptySlice(t *testing.T) {
	m := Message{Content: []any{}}
	if parts := m.GetContentAsParts(); len(parts) != 0 {
		t.Errorf("parts = %v, want empty", parts)
	}
}

// -----------------------------------------------------------------------------
// response_emulation.go
// -----------------------------------------------------------------------------

func TestCancelResponseEmulated_NotFound(t *testing.T) {
	manager := NewResponseManager()
	_, err := CancelResponseEmulated(context.Background(), manager, "does-not-exist")
	if err == nil {
		t.Fatal("expected error for missing response")
	}
}

func TestDeleteResponseEmulated_CancelsInProgress(t *testing.T) {
	manager := NewResponseManager()
	cancelled := false
	state := manager.Create(func() { cancelled = true }, "gpt-x")
	// state defaults to StatusInProgress
	if err := DeleteResponseEmulated(context.Background(), manager, state.ID); err != nil {
		t.Fatalf("DeleteResponseEmulated: %v", err)
	}
	if !cancelled {
		t.Error("expected in-progress response to be cancelled before deletion")
	}
	if _, ok := manager.Get(state.ID); ok {
		t.Error("expected response to be removed from manager")
	}
}

func TestDeleteResponseEmulated_CompletedDoesNotCancel(t *testing.T) {
	manager := NewResponseManager()
	cancelled := false
	state := manager.Create(func() { cancelled = true }, "gpt-x")
	state.SetResult(&ResponseObject{ID: state.ID})
	if err := DeleteResponseEmulated(context.Background(), manager, state.ID); err != nil {
		t.Fatalf("DeleteResponseEmulated: %v", err)
	}
	if cancelled {
		t.Error("expected completed response NOT to be cancelled")
	}
}

func TestCreateResponseEmulated_Background_CompleterError(t *testing.T) {
	manager := NewResponseManager()
	mc := &mockCompleter{err: errors.New("upstream failed")}
	req := CreateResponseRequest{
		Model:      "gpt-x",
		Background: true,
		Input:      []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
	}
	resp, err := CreateResponseEmulated(context.Background(), mc, manager, req)
	if err != nil {
		t.Fatalf("unexpected immediate error: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		got, getErr := GetResponseEmulated(context.Background(), manager, resp.ID)
		if getErr != nil {
			if got != nil {
				t.Errorf("expected nil response alongside error, got %+v", got)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for background failure to propagate")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestCreateResponseEmulated_Background_CompleterPanics(t *testing.T) {
	manager := NewResponseManager()
	mc := &panickingCompleter{}
	req := CreateResponseRequest{
		Model:      "gpt-x",
		Background: true,
		Input:      []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
	}
	resp, err := CreateResponseEmulated(context.Background(), mc, manager, req)
	if err != nil {
		t.Fatalf("unexpected immediate error: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		_, getErr := GetResponseEmulated(context.Background(), manager, resp.ID)
		if getErr != nil {
			if !containsSubstring(getErr.Error(), "panic") {
				t.Errorf("error = %v, want to mention panic recovery", getErr)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for panic recovery to propagate")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type panickingCompleter struct{}

func (p *panickingCompleter) ChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	panic("simulated completer panic")
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (func() bool {
		for i := 0; i+len(substr) <= len(s); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}

func TestToResponseUsage_WithDetails(t *testing.T) {
	u := &Usage{
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      15,
		PromptTokensDetails:     &PromptTokensDetails{CachedTokens: 2},
		CompletionTokensDetails: &CompletionTokensDetails{ReasoningTokens: 3},
	}
	ru := toResponseUsage(u)
	if ru == nil {
		t.Fatal("expected non-nil ResponseUsage")
	}
	if ru.InputTokensDetails == nil || ru.InputTokensDetails.CachedTokens != 2 {
		t.Errorf("InputTokensDetails = %+v", ru.InputTokensDetails)
	}
	if ru.OutputTokensDetails == nil || ru.OutputTokensDetails.ReasoningTokens != 3 {
		t.Errorf("OutputTokensDetails = %+v", ru.OutputTokensDetails)
	}
}

func TestGetRoleFromItemType(t *testing.T) {
	tests := []struct {
		itemType string
		itemMap  map[string]any
		want     string
	}{
		{"message", map[string]any{"role": "system"}, "system"},
		{"message", map[string]any{}, "user"},
		{"user_message", nil, "user"},
		{"system_message", nil, "system"},
		{"assistant_message", nil, "assistant"},
		{"unknown_type", nil, "user"},
	}
	for _, tt := range tests {
		if got := getRoleFromItemType(tt.itemType, tt.itemMap); got != tt.want {
			t.Errorf("getRoleFromItemType(%q) = %q, want %q", tt.itemType, got, tt.want)
		}
	}
}

// -----------------------------------------------------------------------------
// client.go small branches
// -----------------------------------------------------------------------------

func TestClient_ExtraHeaders(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Custom")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ModelsResponse{})
	}))
	defer srv.Close()

	headers := http.Header{}
	headers.Set("X-Custom", "custom-value")
	c, _ := New(Config{BaseURL: srv.URL, ExtraHeaders: headers})
	if _, err := c.GetModels(context.Background()); err != nil {
		t.Fatalf("GetModels() error: %v", err)
	}
	if gotHeader != "custom-value" {
		t.Errorf("gotHeader = %q, want custom-value", gotHeader)
	}
}

func TestClient_HandleError_MalformedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("not json at all"))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL, MaxRetries: -1})
	_, err := c.GetModels(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Type != "unknown" {
		t.Errorf("Type = %q, want unknown", apiErr.Type)
	}
}

func TestClient_HandleError_ErrorFieldNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"something_else": true}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL, MaxRetries: -1})
	_, err := c.GetModels(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.Type != "unknown" {
		t.Errorf("Type = %q, want unknown", apiErr.Type)
	}
}

func TestClient_MarshalBody_Error(t *testing.T) {
	c, _ := New(Config{BaseURL: "http://example.invalid"})
	// A channel cannot be marshaled to JSON.
	_, err := c.CreateEmbedding(context.Background(), EmbeddingRequest{Input: make(chan int)})
	if err == nil {
		t.Fatal("expected marshal error")
	}
}

func TestClient_ProcessSSEStream_SkipsCommentsAndBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		w.Write([]byte(": this is a comment\n\n"))
		w.Write([]byte("data: not valid json\n\n"))
		writeSSEChunk(w, flusher, ChatCompletionResponse{
			ID: "s1", Choices: []Choice{{Index: 0, Delta: Delta{Content: "ok"}}},
		})
		writeSSEChunk(w, flusher, ChatCompletionResponse{
			ID: "s1", Choices: []Choice{{Index: 0, FinishReason: "stop"}},
		})
		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL})
	stream := c.StreamChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    "gpt-x",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	var gotContent string
	for stream.Next() {
		chunk := stream.Current()
		if len(chunk.Choices) > 0 {
			gotContent += chunk.Choices[0].Delta.Content
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if gotContent != "ok" {
		t.Errorf("gotContent = %q, want ok", gotContent)
	}
}

func TestClient_StreamRequest_NonRetryableError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"bad"}}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL, MaxRetries: 3, RetryBackoff: time.Millisecond})
	stream := c.StreamChatCompletion(context.Background(), ChatCompletionRequest{
		Model:    "gpt-x",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	for stream.Next() {
	}
	if err := stream.Err(); err == nil {
		t.Fatal("expected non-retryable error to surface immediately")
	}
}
