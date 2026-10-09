package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Request bodies a router forwards come from its clients: decoding and
// converting them must never panic, whatever they hold.
func FuzzCreateResponseRequest(f *testing.F) {
	for _, seed := range []string{
		`{"model":"m","input":"hi"}`,
		`{"model":"m","input":[{"role":"user","content":"hi"}],"instructions":"x"}`,
		`{"model":"m","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`,
		`{"model":"m","input":[{"type":"function_call","call_id":"c1","name":"f","arguments":"{}"},{"type":"function_call_output","call_id":"c1","output":"r"}]}`,
		`{"model":"m","input":[{"type":"function_call_output","output":{"a":[1,2]}}],"tools":[{"type":"function","name":"f","parameters":{"type":"object"}}]}`,
		`{"model":"m","tools":[{"type":"function","function":{"name":"f"}}],"previous_response_id":"resp_x","store":false}`,
		`{"input":[null,1,"x",[],{}],"metadata":{"k":"v"},"extra":true}`,
	} {
		f.Add([]byte(seed))
	}
	mc := &mockCompleter{resp: &ChatCompletionResponse{ID: "c", Choices: []Choice{{Message: Message{Role: "assistant", Content: "ok"}, FinishReason: "stop"}}}}
	f.Fuzz(func(t *testing.T, data []byte) {
		var req CreateResponseRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return
		}
		_ = ConvertInputToMessages(req.Input)
		_, _ = ConvertResponseToChatRequest(req)
		if _, err := json.Marshal(req); err != nil {
			t.Fatalf("decoded request doesn't encode: %v", err)
		}
		req.Background = false
		req.PreviousResponseID = ""
		_, _ = CreateResponseEmulated(context.Background(), mc, NewResponseManager(), req)
	})
}

func FuzzToolUnmarshal(f *testing.F) {
	for _, seed := range []string{
		`{"type":"function","function":{"name":"f","description":"d","parameters":{"type":"object"}}}`,
		`{"type":"function","name":"f","parameters":{"type":"object","properties":{"a":{"type":"string"}}},"strict":true}`,
		`{"type":"web_search"}`,
		`{"name":1}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var tool Tool
		if err := json.Unmarshal(data, &tool); err != nil {
			return
		}
		if _, err := json.Marshal(tool); err != nil {
			t.Fatalf("decoded tool doesn't encode: %v", err)
		}
		_ = responsesTools([]Tool{tool})
	})
}

// fuzzServer serves whatever body the current fuzz input holds.
func fuzzServer(f *testing.F, contentType string) (*httptest.Server, *atomic.Value) {
	var body atomic.Value
	body.Store([]byte{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Write(body.Load().([]byte))
	}))
	f.Cleanup(srv.Close)
	return srv, &body
}

// A provider's stream is untrusted input: parsing it must end, without
// panicking, whatever it sends.
func FuzzNativeResponseStream(f *testing.F) {
	for _, seed := range []string{
		"event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r\"}}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\",\"output\":[]}}\n\n",
		"data: {\"type\":\"response.output_item.added\",\"output_index\":5,\"item\":{\"type\":\"function_call\",\"name\":\"f\"}}\n\ndata: [DONE]\n\n",
		"event: error\ndata: {\"type\":\"error\",\"message\":\"x\"}\n\n",
		"data: {not json\n\n",
	} {
		f.Add([]byte(seed))
	}
	srv, body := fuzzServer(f, "text/event-stream")
	native := true
	client, err := New(Config{BaseURL: srv.URL, UseNativeResponses: &native, MaxRetries: -1})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		body.Store(data)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		st := client.StreamResponse(ctx, CreateResponseRequest{Model: "m", Input: userInput("x")})
		for st.Next() {
			e := st.Current()
			_ = e.Response()
			_ = e.TextDelta()
		}
		if ctx.Err() != nil {
			t.Fatal("stream didn't end")
		}
	})
}

func FuzzChatStream(f *testing.F) {
	for _, seed := range []string{
		"data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n",
		"data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":3,\"id\":\"t\",\"function\":{\"name\":\"f\",\"arguments\":\"{\"}}]}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n",
		"data: {\"error\":{\"message\":\"x\"}}\n\n",
		": comment\n\ndata: \n\n",
	} {
		f.Add([]byte(seed))
	}
	srv, body := fuzzServer(f, "text/event-stream")
	client, err := New(Config{BaseURL: srv.URL, MaxRetries: -1})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		body.Store(data)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		st := client.StreamChatCompletion(ctx, ChatCompletionRequest{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}})
		for st.Next() {
			_ = st.Current()
		}
		if ctx.Err() != nil {
			t.Fatal("stream didn't end")
		}
	})
}
