package claude

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/paularlott/mcp/ai/openai"
)

// A provider's stream is untrusted input: parsing it must end, without
// panicking, whatever it sends.
func FuzzChatStream(f *testing.F) {
	for _, seed := range []string{"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"usage\":{\"input_tokens\":1}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"t\",\"name\":\"f\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":9,\"delta\":{\"type\":\"text_delta\",\"text\":\"x\"}}\n\n"} {
		f.Add([]byte(seed))
	}
	var body atomic.Value
	body.Store([]byte{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write(body.Load().([]byte))
	}))
	defer srv.Close()
	client, err := New(openai.Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: -1})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		body.Store(data)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		st := client.StreamChatCompletion(ctx, openai.ChatCompletionRequest{Model: "m", Messages: []openai.Message{{Role: "user", Content: "x"}}})
		for st.Next() {
			_ = st.Current()
		}
		if ctx.Err() != nil {
			t.Fatal("stream didn't end")
		}
	})
}
