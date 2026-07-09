package openai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// blockingCompleter blocks on ChatCompletion until either release is closed or
// the context is cancelled. Used to test cancel-mid-flight behaviour.
type blockingCompleter struct {
	lastReq ChatCompletionRequest
	started chan struct{}
	release chan struct{}
}

func newBlockingCompleter() *blockingCompleter {
	return &blockingCompleter{started: make(chan struct{}), release: make(chan struct{})}
}

func (b *blockingCompleter) ChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	b.lastReq = req
	close(b.started)
	select {
	case <-b.release:
		return &ChatCompletionResponse{ID: "chat_1", Model: req.Model, Choices: []Choice{{Index: 0, Message: Message{Role: "assistant", Content: "ok"}, FinishReason: "stop"}}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// mockCompleter is a minimal ChatCompleter that records the request and returns
// a canned response (or error). Used to drive the emulated responses flow.
type mockCompleter struct {
	lastReq ChatCompletionRequest
	resp    *ChatCompletionResponse
	err     error
	calls   int
}

func (m *mockCompleter) ChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	m.calls++
	m.lastReq = req
	if m.err != nil {
		return nil, m.err
	}
	if m.resp != nil {
		return m.resp, nil
	}
	// Default plausible response
	return &ChatCompletionResponse{
		ID:      "chat_1",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []Choice{{
			Index:        0,
			Message:      Message{Role: "assistant", Content: "ok"},
			FinishReason: "stop",
		}},
		Usage: &Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
	}, nil
}

// -----------------------------------------------------------------------------
// ConvertInputToMessages
// -----------------------------------------------------------------------------

func TestConvertInputToMessages_MessageHonoursRoleField(t *testing.T) {
	// The canonical Responses-API "message" item carries its own role. The
	// converter must honour it rather than defaulting every message to "user".
	input := []any{
		map[string]any{"type": "message", "role": "system", "content": "be helpful"},
		map[string]any{"type": "message", "role": "user", "content": "hi"},
		map[string]any{"type": "message", "role": "assistant", "content": "hello"},
		map[string]any{"type": "message", "role": "developer", "content": "dev"},
	}
	msgs := ConvertInputToMessages(input)
	if len(msgs) != 4 {
		t.Fatalf("len(msgs) = %d, want 4", len(msgs))
	}
	wantRoles := []string{"system", "user", "assistant", "developer"}
	for i, want := range wantRoles {
		if msgs[i].Role != want {
			t.Errorf("msgs[%d].Role = %q, want %q", i, msgs[i].Role, want)
		}
	}
}

func TestConvertInputToMessages_LegacyTypedMessages(t *testing.T) {
	// The convenience types user_message / system_message / assistant_message
	// map to the corresponding role.
	input := []any{
		map[string]any{"type": "user_message", "content": "hi"},
		map[string]any{"type": "system_message", "content": "sys"},
		map[string]any{"type": "assistant_message", "content": "asst"},
	}
	msgs := ConvertInputToMessages(input)
	if len(msgs) != 3 {
		t.Fatalf("len(msgs) = %d, want 3", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("user_message role = %q, want user", msgs[0].Role)
	}
	if msgs[1].Role != "system" {
		t.Errorf("system_message role = %q, want system", msgs[1].Role)
	}
	if msgs[2].Role != "assistant" {
		t.Errorf("assistant_message role = %q, want assistant", msgs[2].Role)
	}
}

func TestConvertInputToMessages_FunctionCallOutput(t *testing.T) {
	// function_call_output (native) uses "output"; tool_call_result (legacy)
	// uses "content"; both must map to a "tool" role message with the call id.
	input := []any{
		map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "42"},
		map[string]any{"type": "tool_call_result", "tool_call_id": "call_2", "content": "result"},
	}
	msgs := ConvertInputToMessages(input)
	if len(msgs) != 2 {
		t.Fatalf("len(msgs) = %d, want 2", len(msgs))
	}
	if msgs[0].Role != "tool" || msgs[0].ToolCallID != "call_1" {
		t.Errorf("msgs[0] = %+v, want role=tool tool_call_id=call_1", msgs[0])
	}
	if msgs[1].Role != "tool" || msgs[1].ToolCallID != "call_2" {
		t.Errorf("msgs[1] = %+v, want role=tool tool_call_id=call_2", msgs[1])
	}
}

func TestConvertInputToMessages_Empty(t *testing.T) {
	msgs := ConvertInputToMessages(nil)
	if len(msgs) != 0 {
		t.Fatalf("len(msgs) = %d, want 0", len(msgs))
	}
}

func TestConvertInputToMessages_FunctionCallReconstructedAsAssistantToolCall(t *testing.T) {
	// In a multi-turn tool conversation, the prior assistant turn arrives as a
	// "function_call" input item. It must become an assistant message carrying
	// tool_calls so the completions provider sees the full history.
	input := []any{
		map[string]any{"type": "message", "role": "user", "content": "what's the weather?"},
		map[string]any{"type": "function_call", "call_id": "call_1", "name": "get_weather", "arguments": `{"city":"NYC"}`},
		map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "sunny"},
	}
	msgs := ConvertInputToMessages(input)
	if len(msgs) != 3 {
		t.Fatalf("len(msgs) = %d, want 3", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("msgs[0].Role = %q, want user", msgs[0].Role)
	}
	if msgs[1].Role != "assistant" {
		t.Fatalf("msgs[1].Role = %q, want assistant", msgs[1].Role)
	}
	if len(msgs[1].ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(msgs[1].ToolCalls))
	}
	tc := msgs[1].ToolCalls[0]
	if tc.ID != "call_1" || tc.Function.Name != "get_weather" {
		t.Errorf("tool call = %+v", tc)
	}
	if tc.Function.Arguments["city"] != "NYC" {
		t.Errorf("arguments = %#v, want city=NYC", tc.Function.Arguments)
	}
	if msgs[2].Role != "tool" || msgs[2].ToolCallID != "call_1" {
		t.Errorf("msgs[2] = %+v, want role=tool call_1", msgs[2])
	}
}

func TestConvertResponseToChatRequest_MultiTurnPreservesRoles(t *testing.T) {
	// A realistic multi-turn conversation must survive conversion with every
	// role intact (system prompt, prior assistant turn, tool result).
	req := CreateResponseRequest{
		Model: "gpt-x",
		Input: []any{
			map[string]any{"type": "message", "role": "system", "content": "be concise"},
			map[string]any{"type": "message", "role": "user", "content": "calc 2+2"},
			map[string]any{"type": "message", "role": "assistant", "content": "let me check"},
			map[string]any{"type": "function_call", "call_id": "c1", "name": "calc", "arguments": `{"x":2,"y":2}`},
			map[string]any{"type": "function_call_output", "call_id": "c1", "output": "4"},
		},
	}
	chatReq, err := ConvertResponseToChatRequest(req)
	if err != nil {
		t.Fatalf("ConvertResponseToChatRequest: %v", err)
	}
	wantRoles := []string{"system", "user", "assistant", "assistant", "tool"}
	if len(chatReq.Messages) != len(wantRoles) {
		t.Fatalf("messages len = %d, want %d (%+v)", len(chatReq.Messages), len(wantRoles), chatReq.Messages)
	}
	for i, want := range wantRoles {
		if chatReq.Messages[i].Role != want {
			t.Errorf("messages[%d].Role = %q, want %q", i, chatReq.Messages[i].Role, want)
		}
	}
}

// -----------------------------------------------------------------------------
// ConvertResponseToChatRequest
// -----------------------------------------------------------------------------

func TestConvertResponseToChatRequest_MapsSamplingParams(t *testing.T) {
	maxTok := 256
	temp := 0.7
	topP := 0.9
	req := CreateResponseRequest{
		Model:           "gpt-x",
		MaxOutputTokens: &maxTok,
		Temperature:     &temp,
		TopP:            &topP,
		Input: []any{
			map[string]any{"type": "message", "role": "user", "content": "hi"},
		},
		ExtraBody: map[string]any{"vendor": "x"},
	}
	chatReq, err := ConvertResponseToChatRequest(req)
	if err != nil {
		t.Fatalf("ConvertResponseToChatRequest: %v", err)
	}
	if chatReq.Model != "gpt-x" {
		t.Errorf("Model = %q", chatReq.Model)
	}
	if chatReq.MaxCompletionTokens != 256 {
		t.Errorf("MaxCompletionTokens = %d, want 256", chatReq.MaxCompletionTokens)
	}
	if chatReq.Temperature == nil || *chatReq.Temperature != 0.7 {
		t.Errorf("Temperature = %#v, want 0.7", chatReq.Temperature)
	}
	if chatReq.TopP == nil || *chatReq.TopP != 0.9 {
		t.Errorf("TopP = %#v, want 0.9", chatReq.TopP)
	}
	if chatReq.ExtraBody["vendor"] != "x" {
		t.Errorf("ExtraBody not copied: %#v", chatReq.ExtraBody)
	}
	if len(chatReq.Messages) != 1 || chatReq.Messages[0].Role != "user" {
		t.Errorf("Messages not converted: %+v", chatReq.Messages)
	}
}

func TestConvertResponseToChatRequest_ToolsCopied(t *testing.T) {
	req := CreateResponseRequest{
		Model: "gpt-x",
		Input: []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
		Tools: []Tool{{Type: "function", Function: ToolFunction{Name: "f"}}},
	}
	chatReq, err := ConvertResponseToChatRequest(req)
	if err != nil {
		t.Fatalf("ConvertResponseToChatRequest: %v", err)
	}
	if len(chatReq.Tools) != 1 {
		t.Fatalf("Tools = %d, want 1", len(chatReq.Tools))
	}
}

// Per the Responses API spec, `input` may be a bare string (a single user
// message). UnmarshalJSON must normalise that into the array form.
func TestCreateResponseRequest_StringInputNormalised(t *testing.T) {
	raw := []byte(`{"model":"gpt-x","input":"Tell me a bedtime story."}`)
	var req CreateResponseRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal string input: %v", err)
	}
	if len(req.Input) != 1 {
		t.Fatalf("Input len = %d, want 1", len(req.Input))
	}
	msgs := ConvertInputToMessages(req.Input)
	if len(msgs) != 1 || msgs[0].Role != "user" {
		t.Fatalf("msgs = %+v", msgs)
	}
	if s, ok := msgs[0].Content.(string); !ok || s != "Tell me a bedtime story." {
		t.Errorf("content = %#v", msgs[0].Content)
	}
}

func TestCreateResponseRequest_ArrayInputStillWorks(t *testing.T) {
	raw := []byte(`{"model":"gpt-x","input":[{"type":"message","role":"user","content":"hi"}]}`)
	var req CreateResponseRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(req.Input) != 1 {
		t.Fatalf("Input len = %d, want 1", len(req.Input))
	}
}

func TestCreateResponseRequest_NullInputOK(t *testing.T) {
	raw := []byte(`{"model":"gpt-x","input":null}`)
	var req CreateResponseRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Input != nil {
		t.Errorf("Input = %#v, want nil", req.Input)
	}
}

// -----------------------------------------------------------------------------
// ConvertChatToResponseObject
// -----------------------------------------------------------------------------

func TestConvertChatToResponseObject_TextAndUsage(t *testing.T) {
	chatResp := &ChatCompletionResponse{
		ID:      "chat_abc",
		Model:   "gpt-x",
		Choices: []Choice{{Index: 0, Message: Message{Role: "assistant", Content: "hi there"}, FinishReason: "stop"}},
		Usage:   &Usage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14},
	}
	resp := ConvertChatToResponseObject(chatResp, "gpt-x")
	if resp.ID != "chat_abc" {
		t.Errorf("ID = %q", resp.ID)
	}
	if resp.Object != "response" {
		t.Errorf("Object = %q", resp.Object)
	}
	if resp.Status != "completed" {
		t.Errorf("Status = %q", resp.Status)
	}
	if resp.Model != "gpt-x" {
		t.Errorf("Model = %q", resp.Model)
	}
	if resp.Usage == nil || resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 4 {
		t.Errorf("Usage = %#v", resp.Usage)
	}
	if resp.OutputText() != "hi there" {
		t.Errorf("OutputText = %q, want %q", resp.OutputText(), "hi there")
	}
}

func TestConvertChatToResponseObject_ToolCalls(t *testing.T) {
	chatResp := &ChatCompletionResponse{
		ID:    "chat_abc",
		Model: "gpt-x",
		Choices: []Choice{{Index: 0, Message: Message{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "get_weather", Arguments: map[string]any{"city": "NYC"}}},
		}}, FinishReason: "tool_calls"}},
	}
	resp := ConvertChatToResponseObject(chatResp, "gpt-x")

	// Find the function_call output item.
	var fc map[string]any
	for _, item := range resp.Output {
		if m, ok := item.(map[string]any); ok && m["type"] == "function_call" {
			fc = m
			break
		}
	}
	if fc == nil {
		t.Fatalf("no function_call in output: %+v", resp.Output)
	}
	if fc["name"] != "get_weather" {
		t.Errorf("name = %#v", fc["name"])
	}
	if fc["call_id"] != "call_1" {
		t.Errorf("call_id = %#v", fc["call_id"])
	}
	// Per the Responses API spec, function_call.arguments is a JSON string.
	args, ok := fc["arguments"]
	if !ok {
		t.Fatalf("missing arguments")
	}
	argsStr, isStr := args.(string)
	if !isStr {
		t.Fatalf("arguments should be a JSON string, got %T (%#v)", args, args)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(argsStr), &parsed); err != nil {
		t.Fatalf("arguments is not valid JSON: %v", err)
	}
	if parsed["city"] != "NYC" {
		t.Errorf("arguments.city = %#v, want NYC", parsed["city"])
	}
}

// -----------------------------------------------------------------------------
// Emulated CRUD flow
// -----------------------------------------------------------------------------

func TestCreateResponseEmulated_Sync(t *testing.T) {
	mc := &mockCompleter{}
	req := CreateResponseRequest{
		Model: "gpt-x",
		Input: []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
	}
	resp, err := CreateResponseEmulated(context.Background(), mc, NewResponseManager(), req)
	if err != nil {
		t.Fatalf("CreateResponseEmulated: %v", err)
	}
	if resp.Status != "completed" {
		t.Errorf("Status = %q, want completed", resp.Status)
	}
	if mc.calls != 1 {
		t.Errorf("completer called %d times, want 1", mc.calls)
	}
	if mc.lastReq.Messages[0].Role != "user" {
		t.Errorf("role passed through = %q", mc.lastReq.Messages[0].Role)
	}
}

func TestCreateResponseEmulated_BackgroundThenGet(t *testing.T) {
	manager := NewResponseManager()
	mc := &mockCompleter{}
	req := CreateResponseRequest{
		Model:      "gpt-x",
		Background: true,
		Input:      []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
	}
	resp, err := CreateResponseEmulated(context.Background(), mc, manager, req)
	if err != nil {
		t.Fatalf("CreateResponseEmulated: %v", err)
	}
	if resp.Status != "in_progress" {
		t.Errorf("immediate Status = %q, want in_progress", resp.Status)
	}

	got, err := GetResponseEmulated(context.Background(), manager, resp.ID)
	if err != nil {
		t.Fatalf("GetResponseEmulated: %v", err)
	}
	if got.Status != "completed" {
		t.Errorf("final Status = %q, want completed", got.Status)
	}
	if got.ID != resp.ID {
		t.Errorf("ID mismatch %q vs %q", got.ID, resp.ID)
	}
}

func TestCreateResponseEmulated_PropagatesCompleterError(t *testing.T) {
	mc := &mockCompleter{err: errors.New("boom")}
	req := CreateResponseRequest{
		Model: "gpt-x",
		Input: []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
	}
	_, err := CreateResponseEmulated(context.Background(), mc, NewResponseManager(), req)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected boom error, got %v", err)
	}
}

func TestGetResponseEmulated_NotFound(t *testing.T) {
	manager := NewResponseManager()
	_, err := GetResponseEmulated(context.Background(), manager, "resp_does_not_exist")
	if err == nil {
		t.Fatal("expected error for missing response")
	}
}

func TestDeleteResponseEmulated_NotFound(t *testing.T) {
	manager := NewResponseManager()
	if err := DeleteResponseEmulated(context.Background(), manager, "resp_missing"); err == nil {
		t.Fatal("expected error for missing response")
	}
}

func TestCancelResponseEmulated_CompletesThenGets(t *testing.T) {
	manager := NewResponseManager()
	bc := newBlockingCompleter()
	req := CreateResponseRequest{
		Model: "gpt-x", Background: true,
		Input: []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
	}
	resp, _ := CreateResponseEmulated(context.Background(), bc, manager, req)
	<-bc.started // ensure the completer is in flight before we cancel

	cancelled, err := CancelResponseEmulated(context.Background(), manager, resp.ID)
	if err != nil {
		t.Fatalf("CancelResponseEmulated: %v", err)
	}
	if cancelled == nil {
		t.Fatal("nil cancelled response")
	}
	if cancelled.Status != "cancelled" {
		t.Errorf("Status = %q, want cancelled", cancelled.Status)
	}
	if cancelled.ID != resp.ID {
		t.Errorf("ID = %q, want %q", cancelled.ID, resp.ID)
	}
}

func TestCompactResponseEmulated_RemovesReasoning(t *testing.T) {
	manager := NewResponseManager()
	mc := &mockCompleter{}
	req := CreateResponseRequest{
		Model: "gpt-x", Background: true,
		Input: []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
	}
	resp, _ := CreateResponseEmulated(context.Background(), mc, manager, req)

	// Inject a reasoning item into the stored result to verify compact strips it.
	if s, ok := manager.Get(resp.ID); ok {
		s.Lock()
		if s.Result != nil {
			s.Result.Output = append(s.Result.Output, map[string]any{"type": "reasoning", "summary": "thinking..."})
		}
		s.Unlock()
	}

	compacted, err := CompactResponseEmulated(context.Background(), manager, resp.ID)
	if err != nil {
		t.Fatalf("CompactResponseEmulated: %v", err)
	}
	for _, item := range compacted.Output {
		if m, ok := item.(map[string]any); ok && m["type"] == "reasoning" {
			t.Errorf("reasoning item not removed: %+v", m)
		}
	}
}

func TestCompactResponseEmulated_NotFound(t *testing.T) {
	manager := NewResponseManager()
	if _, err := CompactResponseEmulated(context.Background(), manager, "nope"); err == nil {
		t.Fatal("expected error for missing response")
	}
}

// --- spec-shaped response helpers ---

func TestNewResponseDeleted_SpecShape(t *testing.T) {
	d := NewResponseDeleted("resp_123")
	if d.ID != "resp_123" {
		t.Errorf("ID = %q", d.ID)
	}
	if d.Object != "response" {
		t.Errorf("Object = %q, want \"response\" (not response.deleted)", d.Object)
	}
	if !d.Deleted {
		t.Error("Deleted = false, want true")
	}
	// Round-trip through JSON to confirm the wire shape.
	b, _ := json.Marshal(d)
	if string(b) != `{"id":"resp_123","object":"response","deleted":true}` {
		t.Errorf("JSON = %s", b)
	}
}

func TestNewResponseInputTokensCount_SpecShape(t *testing.T) {
	c := NewResponseInputTokensCount(42)
	if c.Object != "response.input_tokens" {
		t.Errorf("Object = %q", c.Object)
	}
	if c.InputTokens != 42 {
		t.Errorf("InputTokens = %d", c.InputTokens)
	}
	b, _ := json.Marshal(c)
	if string(b) != `{"object":"response.input_tokens","input_tokens":42}` {
		t.Errorf("JSON = %s", b)
	}
}

func TestResponseStatusCodes_AllOK(t *testing.T) {
	// All Responses API endpoints return 200 — guard against the 201/204
	// regressions that crept in across consumers.
	for name, code := range map[string]int{
		"Create":      CreateStatusCode,
		"Retrieve":    RetrieveStatusCode,
		"Delete":      DeleteStatusCode,
		"List":        ListStatusCode,
		"Cancel":      CancelStatusCode,
		"Compact":     CompactStatusCode,
		"InputItems":  InputItemsStatusCode,
		"InputTokens": InputTokensStatusCode,
	} {
		if code != 200 {
			t.Errorf("%sStatusCode = %d, want 200", name, code)
		}
	}
}
