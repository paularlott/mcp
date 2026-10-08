package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/paularlott/mcp"
)

type streamedEvent struct {
	Type string
	Data map[string]any
}

func collectEmulatedStream(t *testing.T, chunks []ChatCompletionResponse, manager *ResponseManager, req CreateResponseRequest) []streamedEvent {
	t.Helper()
	mc := &streamMockCompleter{chunks: chunks}
	eventChan := make(chan ResponseStreamEvent, 100)
	errorChan := make(chan error, 1)
	StreamResponseEmulatedWithManager(context.Background(), mc, manager, req, eventChan, errorChan)
	close(eventChan)
	close(errorChan)
	if err := <-errorChan; err != nil {
		t.Fatal(err)
	}
	var events []streamedEvent
	for ev := range eventChan {
		var data map[string]any
		json.Unmarshal(ev.Data, &data)
		events = append(events, streamedEvent{ev.Type, data})
	}
	return events
}

func toolDelta(index int, id, name, args string) ChatCompletionResponse {
	return ChatCompletionResponse{Choices: []Choice{{Delta: Delta{ToolCalls: []DeltaToolCall{{
		Index: index, ID: id, Type: "function", Function: DeltaFunction{Name: name, Arguments: args},
	}}}}}}
}

func TestStreamEmulated_ToolCallEvents(t *testing.T) {
	manager := NewResponseManager()
	chunks := []ChatCompletionResponse{
		{Choices: []Choice{{Delta: Delta{Content: "Checking "}}}},
		{Choices: []Choice{{Delta: Delta{Content: "both."}}}},
		toolDelta(0, "call_a", "weather", ""),
		toolDelta(0, "", "", `{"city":`),
		toolDelta(0, "", "", `"Paris"}`),
		toolDelta(1, "call_b", "weather", `{"city":"Tokyo"}`),
		{Choices: []Choice{{FinishReason: "tool_calls"}}},
	}
	events := collectEmulatedStream(t, chunks, manager, CreateResponseRequest{
		Model: "m", Input: userInput("weather in Paris and Tokyo?"),
		Tools: []Tool{{Type: "function", Function: ToolFunction{Name: "weather"}}},
	})

	var seq []string
	for _, ev := range events {
		switch ev.Type {
		case "response.output_item.added", "response.output_item.done":
			item := ev.Data["item"].(map[string]any)
			seq = append(seq, fmt.Sprintf("%s:%v:%v", ev.Type[len("response."):], ev.Data["output_index"], item["type"]))
		case "response.function_call_arguments.delta":
			seq = append(seq, fmt.Sprintf("args.delta:%v:%v", ev.Data["output_index"], ev.Data["delta"]))
		case "response.function_call_arguments.done":
			seq = append(seq, fmt.Sprintf("args.done:%v:%v", ev.Data["output_index"], ev.Data["arguments"]))
		}
	}
	want := []string{
		"output_item.added:0:message",
		"output_item.added:1:function_call",
		`args.delta:1:{"city":`,
		`args.delta:1:"Paris"}`,
		"output_item.added:2:function_call",
		`args.delta:2:{"city":"Tokyo"}`,
		"output_item.done:0:message",
		`args.done:1:{"city":"Paris"}`,
		"output_item.done:1:function_call",
		`args.done:2:{"city":"Tokyo"}`,
		"output_item.done:2:function_call",
	}
	if fmt.Sprint(seq) != fmt.Sprint(want) {
		t.Fatalf("events:\n%s\nwant:\n%s", strings.Join(seq, "\n"), strings.Join(want, "\n"))
	}

	completed := events[len(events)-1]
	if completed.Type != "response.completed" {
		t.Fatalf("last event = %s", completed.Type)
	}
	raw, _ := json.Marshal(completed.Data["response"])
	var resp ResponseObject
	json.Unmarshal(raw, &resp)
	if resp.OutputText() != "Checking both." {
		t.Errorf("OutputText = %q", resp.OutputText())
	}
	calls := extractToolCallsFromResponse(&resp)
	if len(calls) != 2 || calls[0].ID != "call_a" || calls[0].Function.Arguments["city"] != "Paris" ||
		calls[1].ID != "call_b" || calls[1].Function.Arguments["city"] != "Tokyo" {
		t.Fatalf("tool calls in completed response = %+v", calls)
	}

	// The tool calls are stored, so results can be sent with previous_response_id
	sc := &mockCompleter{resp: replyWith("Paris sunny, Tokyo rainy")}
	if _, err := CreateResponseEmulated(context.Background(), sc, manager, CreateResponseRequest{
		Model: "m", PreviousResponseID: resp.ID,
		Input: []any{
			map[string]any{"type": "function_call_output", "call_id": "call_a", "output": "sunny"},
			map[string]any{"type": "function_call_output", "call_id": "call_b", "output": "rainy"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	msgs := sc.lastReq.Messages
	if len(msgs) != 4 || len(msgs[1].ToolCalls) != 2 || msgs[2].ToolCallID != "call_a" || msgs[3].ToolCallID != "call_b" {
		t.Fatalf("continued messages = %+v", msgs)
	}
}

func TestStreamEmulated_ToolCallsOnly(t *testing.T) {
	events := collectEmulatedStream(t, []ChatCompletionResponse{
		toolDelta(0, "", "f", `{}`), // no id from the provider: one is generated
		{Choices: []Choice{{FinishReason: "tool_calls"}}},
	}, NewResponseManager(), CreateResponseRequest{Model: "m", Input: userInput("x")})

	var added []string
	var callID string
	for _, ev := range events {
		if ev.Type == "response.output_item.added" {
			item := ev.Data["item"].(map[string]any)
			added = append(added, fmt.Sprint(item["type"]))
			callID, _ = item["call_id"].(string)
		}
		if ev.Type == "response.output_text.delta" {
			t.Errorf("unexpected text delta: %v", ev.Data)
		}
	}
	if fmt.Sprint(added) != "[function_call]" || callID == "" {
		t.Errorf("items added = %v call_id = %q, want one function_call with an id", added, callID)
	}
}

// With MCP servers attached, a caller passing its own tools must still get
// the tool-call deltas: the client isn't handling those tools.
func TestClient_StreamChatCompletion_CallerToolsForwardedWithServers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"id":"c","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"mine","arguments":"{}"}}]}}]}`+"\n\n")
		fmt.Fprint(w, `data: {"id":"c","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	local := &MCPServerFuncs{
		ListToolsFunc: func() []mcp.MCPTool { return []mcp.MCPTool{{Name: "server_tool"}} },
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			t.Errorf("server tool %q called for a caller-handled tool call", name)
			return mcp.NewToolResponseText(""), nil
		},
	}
	c, _ := New(Config{BaseURL: srv.URL, LocalServer: local})
	s := c.StreamChatCompletion(context.Background(), ChatCompletionRequest{
		Model: "m", Messages: []Message{{Role: "user", Content: "x"}},
		Tools: []Tool{{Type: "function", Function: ToolFunction{Name: "mine"}}},
	})
	var ids []string
	finish := ""
	for s.Next() {
		ch := s.Current()
		if len(ch.Choices) == 0 {
			continue
		}
		for _, d := range ch.Choices[0].Delta.ToolCalls {
			ids = append(ids, d.ID)
		}
		if ch.Choices[0].FinishReason != "" {
			finish = ch.Choices[0].FinishReason
		}
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ids) != "[call_1]" || finish != "tool_calls" {
		t.Errorf("tool deltas = %v finish = %q, want [call_1] tool_calls", ids, finish)
	}
}

func TestStreamEmulated_NullArgumentsBecomeEmptyObject(t *testing.T) {
	events := collectEmulatedStream(t, []ChatCompletionResponse{
		toolDelta(0, "call_1", "f", "null"),
		{Choices: []Choice{{FinishReason: "tool_calls"}}},
	}, NewResponseManager(), CreateResponseRequest{Model: "m", Input: userInput("x")})
	for _, ev := range events {
		if ev.Type == "response.output_item.done" {
			if item := ev.Data["item"].(map[string]any); item["arguments"] != "{}" {
				t.Errorf("arguments = %v, want {}", item["arguments"])
			}
		}
	}
}

// A reply that is only tool calls has the same output whether created or
// streamed, and like the native API no empty message item.
func TestEmulated_ToolOnlyReplySameOutputSyncAndStream(t *testing.T) {
	toolCall := ToolCall{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "f", Arguments: map[string]any{"a": 1.0}}}
	sync, err := CreateResponseEmulated(context.Background(), &mockCompleter{resp: &ChatCompletionResponse{Choices: []Choice{{
		Message: Message{Role: "assistant", ToolCalls: []ToolCall{toolCall}}, FinishReason: "tool_calls",
	}}}}, NewResponseManager(), CreateResponseRequest{Model: "m", Input: userInput("x")})
	if err != nil {
		t.Fatal(err)
	}

	events := collectEmulatedStream(t, []ChatCompletionResponse{
		toolDelta(0, "call_1", "f", `{"a":1}`),
		{Choices: []Choice{{FinishReason: "tool_calls"}}},
	}, NewResponseManager(), CreateResponseRequest{Model: "m", Input: userInput("x")})
	raw, _ := json.Marshal(events[len(events)-1].Data["response"])
	var streamed ResponseObject
	json.Unmarshal(raw, &streamed)

	for name, resp := range map[string]*ResponseObject{"sync": sync, "stream": &streamed} {
		if len(resp.Output) != 1 || resp.Output[0].(map[string]any)["type"] != "function_call" {
			t.Errorf("%s output = %v, want just the function_call", name, resp.Output)
		}
	}
}

// When the client runs MCP tools itself, the native rounds of the tool loop
// reach the caller as one lifecycle, with the tool calls hidden.
func TestClient_StreamResponse_Native_ToolLoopIsOneLifecycle(t *testing.T) {
	round := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		round++
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(v map[string]any) {
			data, _ := json.Marshal(v)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", v["type"], data)
		}
		seq := 0
		ev := func(typ string, fields map[string]any) {
			fields["type"] = typ
			fields["sequence_number"] = seq
			seq++
			send(fields)
		}
		id := fmt.Sprintf("resp_round%d", round)
		ev("response.created", map[string]any{"response": map[string]any{"id": id, "status": "in_progress"}})
		ev("response.in_progress", map[string]any{"response": map[string]any{"id": id, "status": "in_progress"}})
		if round == 1 {
			reasoning := map[string]any{"type": "reasoning", "id": "rs_1", "summary": []any{}}
			call := map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "lookup", "arguments": `{"q":"x"}`}
			ev("response.output_item.added", map[string]any{"output_index": 0, "item": reasoning})
			ev("response.output_item.done", map[string]any{"output_index": 0, "item": reasoning})
			ev("response.output_item.added", map[string]any{"output_index": 1, "item": call})
			ev("response.function_call_arguments.delta", map[string]any{"output_index": 1, "item_id": "fc_1", "delta": `{"q":"x"}`})
			ev("response.function_call_arguments.done", map[string]any{"output_index": 1, "item_id": "fc_1", "arguments": `{"q":"x"}`})
			ev("response.output_item.done", map[string]any{"output_index": 1, "item": call})
			ev("response.completed", map[string]any{"response": map[string]any{"id": id, "status": "completed",
				"output": []any{reasoning, call}, "usage": map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}}})
			return
		}
		msg := map[string]any{"type": "message", "id": "msg_1", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "found it"}}}
		ev("response.output_item.added", map[string]any{"output_index": 0, "item": msg})
		ev("response.output_text.delta", map[string]any{"output_index": 0, "item_id": "msg_1", "content_index": 0, "delta": "found it"})
		ev("response.output_item.done", map[string]any{"output_index": 0, "item": msg})
		ev("response.completed", map[string]any{"response": map[string]any{"id": id, "status": "completed",
			"output": []any{msg}, "usage": map[string]any{"input_tokens": 20, "output_tokens": 3, "total_tokens": 23}}})
	}))
	defer srv.Close()

	local := &MCPServerFuncs{
		ListToolsFunc: func() []mcp.MCPTool {
			return []mcp.MCPTool{{Name: "lookup", InputSchema: map[string]any{"type": "object"}}}
		},
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("result"), nil
		},
	}
	native := true
	c, _ := New(Config{BaseURL: srv.URL, UseNativeResponses: &native, LocalServer: local})
	s := c.StreamResponse(context.Background(), CreateResponseRequest{Model: "m", Input: userInput("find x")})

	counts := map[string]int{}
	var indexes, seqs []int
	var completed *ResponseObject
	var text string
	for s.Next() {
		ev := s.Current()
		counts[ev.Type]++
		text += ev.TextDelta()
		var data map[string]any
		json.Unmarshal(ev.Data, &data)
		if ev.Type == "response.output_item.added" {
			indexes = append(indexes, int(data["output_index"].(float64)))
			if item := data["item"].(map[string]any); item["type"] == "function_call" {
				t.Errorf("internal function_call item exposed: %v", item)
			}
		}
		if n, ok := data["sequence_number"].(float64); ok {
			seqs = append(seqs, int(n))
		}
		if r := ev.Response(); r != nil {
			completed = r
		}
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	if round != 2 {
		t.Fatalf("rounds = %d, want 2", round)
	}
	for typ, want := range map[string]int{"response.created": 1, "response.in_progress": 1, "response.completed": 1,
		"response.function_call_arguments.delta": 0, "response.function_call_arguments.done": 0} {
		if counts[typ] != want {
			t.Errorf("%s events = %d, want %d", typ, counts[typ], want)
		}
	}
	if fmt.Sprint(indexes) != "[0 1]" {
		t.Errorf("output indexes = %v, want [0 1] (reasoning, then message)", indexes)
	}
	for i, n := range seqs {
		if n != i {
			t.Fatalf("sequence numbers = %v, want 0..%d", seqs, len(seqs)-1)
		}
	}
	if completed == nil || completed.ID != "resp_round2" || completed.OutputText() != "found it" || text != "found it" {
		t.Fatalf("completed = %+v, text = %q", completed, text)
	}
	if len(completed.Output) != 2 || hasResponseToolCalls(completed) {
		t.Errorf("completed output = %v, want reasoning and message only", completed.Output)
	}
	if completed.Usage == nil || completed.Usage.TotalTokens != 38 {
		t.Errorf("usage = %+v, want both rounds combined (38)", completed.Usage)
	}
}

func TestToolLoopStream_NoUsageWhenNoRoundReportsIt(t *testing.T) {
	s := &toolLoopStream{}
	s.startRound()
	ev := ResponseStreamEvent{Type: "response.completed", Data: json.RawMessage(`{"type":"response.completed","response":{"id":"r","status":"completed","output":[]}}`)}
	got, ok := s.rewrite(ev)
	if !ok {
		t.Fatal("final completed event dropped")
	}
	var data map[string]map[string]any
	json.Unmarshal(got.Data, &data)
	if _, has := data["response"]["usage"]; has {
		t.Errorf("usage added though no round reported any: %s", got.Data)
	}
}
