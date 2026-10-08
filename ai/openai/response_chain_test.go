package openai

import (
	"context"
	"encoding/json"
	"testing"
)

func userInput(text string) []any {
	return []any{map[string]any{"role": "user", "content": text}}
}

func replyWith(text string) *ChatCompletionResponse {
	return &ChatCompletionResponse{Choices: []Choice{{Message: Message{Role: "assistant", Content: text}}}}
}

func roles(msgs []Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Role + ":" + m.GetContentAsString()
	}
	return out
}

func assertMessages(t *testing.T, got []Message, want ...string) {
	t.Helper()
	g := roles(got)
	if len(g) != len(want) {
		t.Fatalf("messages = %q, want %q", g, want)
	}
	for i := range want {
		if g[i] != want[i] {
			t.Fatalf("messages = %q, want %q", g, want)
		}
	}
}

func TestEmulated_PreviousResponseID_ChainsThreeTurns(t *testing.T) {
	manager := NewResponseManager()
	mc := &mockCompleter{resp: replyWith("noted")}
	ctx := context.Background()

	r1, err := CreateResponseEmulated(ctx, mc, manager, CreateResponseRequest{
		Model: "m", Instructions: "be brief", Input: userInput("My name is Zorblat"),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertMessages(t, mc.lastReq.Messages, "system:be brief", "user:My name is Zorblat")

	mc.resp = replyWith("Zorblat")
	r2, err := CreateResponseEmulated(ctx, mc, manager, CreateResponseRequest{
		Model: "m", PreviousResponseID: r1.ID, Input: userInput("What is my name?"),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Instructions are not carried over from the previous response
	assertMessages(t, mc.lastReq.Messages,
		"user:My name is Zorblat", "assistant:noted", "user:What is my name?")

	_, err = CreateResponseEmulated(ctx, mc, manager, CreateResponseRequest{
		Model: "m", Instructions: "be terse", PreviousResponseID: r2.ID, Input: userInput("Spell it"),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertMessages(t, mc.lastReq.Messages,
		"system:be terse", "user:My name is Zorblat", "assistant:noted",
		"user:What is my name?", "assistant:Zorblat", "user:Spell it")
}

func TestEmulated_PreviousResponseID_KeepsCallerToolTurns(t *testing.T) {
	manager := NewResponseManager()
	mc := &mockCompleter{resp: &ChatCompletionResponse{Choices: []Choice{{Message: Message{
		Role:      "assistant",
		ToolCalls: []ToolCall{{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "f", Arguments: map[string]any{}}}},
	}}}}}
	ctx := context.Background()

	r1, err := CreateResponseEmulated(ctx, mc, manager, CreateResponseRequest{
		Model: "m", Input: userInput("call f"),
		Tools: []Tool{{Type: "function", Function: ToolFunction{Name: "f"}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	mc.resp = replyWith("done")
	_, err = CreateResponseEmulated(ctx, mc, manager, CreateResponseRequest{
		Model: "m", PreviousResponseID: r1.ID,
		Input: []any{map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "42"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	msgs := mc.lastReq.Messages
	if len(msgs) != 3 || len(msgs[1].ToolCalls) != 1 || msgs[1].ToolCalls[0].ID != "call_1" ||
		msgs[2].Role != "tool" || msgs[2].ToolCallID != "call_1" {
		t.Fatalf("messages = %+v", msgs)
	}
}

func TestEmulated_PreviousResponseID_Errors(t *testing.T) {
	manager := NewResponseManager()
	mc := &mockCompleter{}
	ctx := context.Background()
	noStore := false

	unstored, err := CreateResponseEmulated(ctx, mc, manager, CreateResponseRequest{
		Model: "m", Store: &noStore, Input: userInput("secret"),
	})
	if err != nil {
		t.Fatal(err)
	}
	deleted, _ := CreateResponseEmulated(ctx, mc, manager, CreateResponseRequest{Model: "m", Input: userInput("x")})
	if err := DeleteResponseEmulated(ctx, manager, deleted.ID); err != nil {
		t.Fatal(err)
	}

	calls := mc.calls
	for name, id := range map[string]string{"unknown": "resp_nope", "store false": unstored.ID, "deleted": deleted.ID} {
		if _, err := CreateResponseEmulated(ctx, mc, manager, CreateResponseRequest{
			Model: "m", PreviousResponseID: id, Input: userInput("again"),
		}); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if mc.calls != calls {
		t.Error("model should not be called when the previous response can't be continued")
	}
}

func TestEmulated_PreviousResponseID_Background(t *testing.T) {
	manager := NewResponseManager()
	mc := &mockCompleter{resp: replyWith("noted")}
	ctx := context.Background()

	r1, err := CreateResponseEmulated(ctx, mc, manager, CreateResponseRequest{
		Model: "m", Background: true, Input: userInput("My name is Zorblat"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetResponseEmulated(ctx, manager, r1.ID); err != nil { // waits for completion
		t.Fatal(err)
	}

	if _, err := CreateResponseEmulated(ctx, mc, manager, CreateResponseRequest{
		Model: "m", PreviousResponseID: r1.ID, Input: userInput("What is my name?"),
	}); err != nil {
		t.Fatal(err)
	}
	assertMessages(t, mc.lastReq.Messages, "user:My name is Zorblat", "assistant:noted", "user:What is my name?")
}

// recordingStreamCompleter streams a fixed reply and records each request.
type recordingStreamCompleter struct {
	streamMockCompleter
	reply   string
	lastReq ChatCompletionRequest
}

func (m *recordingStreamCompleter) StreamChatCompletion(ctx context.Context, req ChatCompletionRequest) *ChatStream {
	m.lastReq = req
	m.chunks = []ChatCompletionResponse{
		{Choices: []Choice{{Delta: Delta{Content: m.reply}}}},
		{Choices: []Choice{{FinishReason: "stop"}}},
	}
	return m.streamMockCompleter.StreamChatCompletion(ctx, req)
}

func streamEmulated(t *testing.T, mc ChatStreamCompleter, manager *ResponseManager, req CreateResponseRequest) *ResponseObject {
	t.Helper()
	eventChan := make(chan ResponseStreamEvent, 50)
	errorChan := make(chan error, 1)
	StreamResponseEmulatedWithManager(context.Background(), mc, manager, req, eventChan, errorChan)
	close(eventChan)
	close(errorChan)
	if err := <-errorChan; err != nil {
		t.Fatal(err)
	}
	var created, completed *ResponseObject
	for ev := range eventChan {
		switch ev.Type {
		case "response.created":
			var v struct {
				Response ResponseObject `json:"response"`
			}
			if err := json.Unmarshal(ev.Data, &v); err == nil {
				created = &v.Response
			}
		case "response.completed":
			completed = ev.Response()
		}
	}
	if created == nil || completed == nil || created.ID != completed.ID {
		t.Fatalf("created/completed IDs differ: %+v / %+v", created, completed)
	}
	return completed
}

func TestStreamEmulated_RegistersAndChains(t *testing.T) {
	manager := NewResponseManager()
	mc := &recordingStreamCompleter{reply: "noted"}

	r1 := streamEmulated(t, mc, manager, CreateResponseRequest{Model: "m", Input: userInput("My name is Zorblat")})
	got, err := GetResponseEmulated(context.Background(), manager, r1.ID)
	if err != nil || got.OutputText() != "noted" {
		t.Fatalf("GetResponseEmulated = %+v, %v", got, err)
	}

	mc.reply = "Zorblat"
	streamEmulated(t, mc, manager, CreateResponseRequest{Model: "m", PreviousResponseID: r1.ID, Input: userInput("What is my name?")})
	assertMessages(t, mc.lastReq.Messages, "user:My name is Zorblat", "assistant:noted", "user:What is my name?")

	// A sync request can continue a streamed one and vice versa
	sc := &mockCompleter{resp: replyWith("ok")}
	if _, err := CreateResponseEmulated(context.Background(), sc, manager, CreateResponseRequest{
		Model: "m", PreviousResponseID: r1.ID, Input: userInput("again"),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestStreamEmulated_FailedStreamIsNotLeftInProgress(t *testing.T) {
	manager := NewResponseManager()
	mc := &streamMockCompleter{err: context.DeadlineExceeded}
	eventChan := make(chan ResponseStreamEvent, 50)
	errorChan := make(chan error, 1)
	StreamResponseEmulatedWithManager(context.Background(), mc, manager, CreateResponseRequest{Model: "m", Input: userInput("x")}, eventChan, errorChan)
	if err := <-errorChan; err == nil {
		t.Fatal("expected stream error")
	}
	close(eventChan)
	created := <-eventChan
	var v struct {
		Response ResponseObject `json:"response"`
	}
	json.Unmarshal(created.Data, &v)
	state, ok := manager.Get(v.Response.ID)
	if !ok || state.GetStatus() != StatusFailed {
		t.Fatalf("response %q state = %+v, want failed", v.Response.ID, state)
	}
	if lookupLive(v.Response.ID) != nil {
		t.Error("finished response still held in process")
	}
}

func TestWithoutMCPTools_SkipsToolInjection(t *testing.T) {
	ctx := context.Background()
	if MCPToolsDisabled(ctx) {
		t.Fatal("tools disabled by default")
	}
	if !MCPToolsDisabled(WithoutMCPTools(ctx)) {
		t.Fatal("WithoutMCPTools not detected")
	}
}
