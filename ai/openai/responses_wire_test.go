package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/paularlott/mcp"
)

// The Responses API takes flat function tools, not the nested Chat
// Completions form.
func TestCreateResponseRequest_MarshalsFlatTools(t *testing.T) {
	req := CreateResponseRequest{
		Model: "m",
		Tools: []Tool{{Type: "function", Function: ToolFunction{
			Name: "f", Description: "d", Parameters: map[string]any{"type": "object"},
		}}},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Tools) != 1 {
		t.Fatalf("tools = %v", body.Tools)
	}
	tool := body.Tools[0]
	if tool["type"] != "function" || tool["name"] != "f" || tool["description"] != "d" || tool["parameters"] == nil {
		t.Errorf("tool = %v, want flat function tool", tool)
	}
	if _, nested := tool["function"]; nested {
		t.Errorf("tool should not be nested under function: %v", tool)
	}
}

func TestTool_UnmarshalAcceptsFlatAndNested(t *testing.T) {
	for name, in := range map[string]string{
		"flat":   `{"type":"function","name":"f","description":"d","parameters":{"type":"object"}}`,
		"nested": `{"type":"function","function":{"name":"f","description":"d","parameters":{"type":"object"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var tool Tool
			if err := json.Unmarshal([]byte(in), &tool); err != nil {
				t.Fatal(err)
			}
			if tool.Type != "function" || tool.Function.Name != "f" || tool.Function.Description != "d" || tool.Function.Parameters == nil {
				t.Errorf("tool = %+v", tool)
			}
		})
	}
}

func TestCreateResponseRequest_RoundTripsFlatTools(t *testing.T) {
	in := `{"model":"m","tools":[{"type":"function","name":"f","parameters":{"type":"object"}}]}`
	var req CreateResponseRequest
	if err := json.Unmarshal([]byte(in), &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Tools) != 1 || req.Tools[0].Function.Name != "f" {
		t.Fatalf("tools = %+v", req.Tools)
	}
	if _, ok := req.ExtraBody["tools"]; ok {
		t.Error("tools should not leak into ExtraBody")
	}
}

// The tool loop must replay the function_call items and reference them by
// call_id in function_call_output items, not by the output item id.
func TestClient_CreateResponse_Native_ToolLoopInput(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		if len(bodies) == 1 {
			json.NewEncoder(w).Encode(ResponseObject{
				ID: "resp_1", Object: "response", Status: "completed",
				Output: []any{
					map[string]any{"type": "reasoning", "id": "rs_1", "encrypted_content": "x"},
					map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "get_weather", "arguments": `{"city":"NYC"}`},
				},
			})
			return
		}
		json.NewEncoder(w).Encode(ResponseObject{
			ID: "resp_2", Object: "response", Status: "completed",
			Output: []any{map[string]any{"type": "message", "role": "assistant", "content": []any{
				map[string]any{"type": "output_text", "text": "sunny"},
			}}},
		})
	}))
	defer srv.Close()

	local := &MCPServerFuncs{
		ListToolsFunc: func() []mcp.MCPTool {
			return []mcp.MCPTool{{Name: "get_weather", Description: "weather", InputSchema: map[string]any{"type": "object"}}}
		},
		CallToolFunc: func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
			return mcp.NewToolResponseText("sunny weather"), nil
		},
	}
	native := true
	c, _ := New(Config{BaseURL: srv.URL, UseNativeResponses: &native, LocalServer: local})

	resp, err := c.CreateResponse(context.Background(), CreateResponseRequest{
		Model: "m",
		Input: []any{map[string]any{"role": "user", "content": "weather?"}},
	})
	if err != nil {
		t.Fatalf("CreateResponse() error: %v", err)
	}
	if resp.OutputText() != "sunny" {
		t.Errorf("OutputText() = %q", resp.OutputText())
	}
	if len(bodies) != 2 {
		t.Fatalf("requests = %d, want 2", len(bodies))
	}

	tools, _ := bodies[0]["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "get_weather" {
		t.Errorf("first request tools = %v, want flat get_weather tool", bodies[0]["tools"])
	}

	input, _ := bodies[1]["input"].([]any)
	var types []string
	for _, item := range input {
		m := item.(map[string]any)
		types = append(types, fmt.Sprint(m["type"]))
	}
	want := []string{"<nil>", "reasoning", "function_call", "function_call_output"}
	if fmt.Sprint(types) != fmt.Sprint(want) {
		t.Fatalf("second request input types = %v, want %v", types, want)
	}
	out := input[3].(map[string]any)
	if out["call_id"] != "call_1" || out["output"] != "sunny weather" {
		t.Errorf("function_call_output = %v", out)
	}
}

// Streaming must actually request a stream; an earlier wrapper struct lost
// the stream field to the request's promoted MarshalJSON.
func TestClient_StreamResponse_Native_SendsStreamTrue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		json.Unmarshal(raw, &body)
		if body["stream"] != true {
			t.Errorf("stream = %v, want true (body %s)", body["stream"], raw)
		}
		if body["custom"] != "x" {
			t.Errorf("ExtraBody field lost: %s", raw)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n")
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\"}}\n\n")
	}))
	defer srv.Close()

	c := nativeClient(t, srv)
	extra := map[string]any{"custom": "x"}
	s := c.StreamResponse(context.Background(), CreateResponseRequest{Model: "m", ExtraBody: extra})
	var text string
	n := 0
	for s.Next() {
		ev := s.Current()
		n++
		text += ev.TextDelta()
	}
	if err := s.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if n != 2 || text != "hi" {
		t.Errorf("events = %d text = %q", n, text)
	}
	if _, ok := extra["stream"]; ok {
		t.Error("caller's ExtraBody map was mutated")
	}
}

func TestClient_StreamResponse_Native_NonSSEResponseErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"r","status":"completed"}`))
	}))
	defer srv.Close()

	s := nativeClient(t, srv).StreamResponse(context.Background(), CreateResponseRequest{Model: "m"})
	for s.Next() {
	}
	if s.Err() == nil {
		t.Error("expected an error for a non-SSE response")
	}
}

func TestConvertResponseToChatRequest_UntypedMessagesAndInstructions(t *testing.T) {
	chat, err := ConvertResponseToChatRequest(CreateResponseRequest{
		Model:        "m",
		Instructions: "be brief",
		Input: []any{
			map[string]any{"role": "user", "content": "hi"},
			map[string]any{"role": "assistant", "content": "hello"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(chat.Messages) != 3 {
		t.Fatalf("messages = %+v, want 3", chat.Messages)
	}
	for i, want := range []struct{ role, content string }{{"system", "be brief"}, {"user", "hi"}, {"assistant", "hello"}} {
		if chat.Messages[i].Role != want.role || chat.Messages[i].Content != want.content {
			t.Errorf("message %d = %+v, want %+v", i, chat.Messages[i], want)
		}
	}
}

func TestClient_Grok_CancelAndCompactLimits(t *testing.T) {
	c, err := New(Config{Provider: providerGrok, APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CancelResponse(context.Background(), "resp_x"); err == nil {
		t.Error("CancelResponse should fail on xAI")
	}
	if _, err := c.CompactResponse(context.Background(), CompactResponseRequest{Model: "m", PreviousResponseID: "resp_x"}); err == nil {
		t.Error("CompactResponse with previous_response_id should fail on xAI")
	}
	if _, err := c.CompactResponse(context.Background(), CompactResponseRequest{Model: "m"}); err == nil {
		t.Error("CompactResponse without input should fail on xAI")
	}
}
