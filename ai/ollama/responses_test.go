package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/paularlott/mcp/ai/openai"
)

// chatOKHandler returns a minimal, valid non-streaming /api/chat response.
func chatOKHandler(content string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(chatResponse{
			Model:      "llama3",
			Message:    message{Role: "assistant", Content: content},
			Done:       true,
			DoneReason: "stop",
		})
	}
}

func TestCreateResponseSync(t *testing.T) {
	srv := httptest.NewServer(chatOKHandler("hi there"))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	resp, err := c.CreateResponse(context.Background(), openai.CreateResponseRequest{
		Model: "llama3",
		Input: []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatalf("CreateResponse() error: %v", err)
	}
	if resp.Status != "completed" {
		t.Errorf("Status = %q, want completed", resp.Status)
	}
	if resp.OutputText() != "hi there" {
		t.Errorf("OutputText() = %q", resp.OutputText())
	}
}

func TestCreateResponseBackgroundThenGetAndCancel(t *testing.T) {
	srv := httptest.NewServer(chatOKHandler("done"))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	resp, err := c.CreateResponse(context.Background(), openai.CreateResponseRequest{
		Model:      "llama3",
		Background: true,
		Input:      []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatalf("CreateResponse() error: %v", err)
	}

	// Poll until the background completer finishes (it runs on a goroutine).
	var got *openai.ResponseObject
	for i := 0; i < 200; i++ {
		got, err = c.GetResponse(context.Background(), resp.ID)
		if err != nil {
			t.Fatalf("GetResponse() error: %v", err)
		}
		if got.Status == "completed" {
			break
		}
	}
	if got.Status != "completed" {
		t.Fatalf("Status = %q, want completed eventually", got.Status)
	}

	if err := c.DeleteResponse(context.Background(), resp.ID); err != nil {
		t.Fatalf("DeleteResponse() error: %v", err)
	}
	if _, err := c.GetResponse(context.Background(), resp.ID); err == nil {
		t.Error("expected error getting a deleted response")
	}
}

func TestCancelResponseNotFound(t *testing.T) {
	c := &Client{responseManager: openai.GetManager()}
	if _, err := c.CancelResponse(context.Background(), "resp_missing_cancel"); err == nil {
		t.Error("expected error cancelling a missing response")
	}
}

func TestGetResponseNotFound(t *testing.T) {
	c := &Client{responseManager: openai.GetManager()}
	if _, err := c.GetResponse(context.Background(), "resp_does_not_exist"); err == nil {
		t.Error("expected error for missing response")
	}
}

func TestDeleteResponseNotFound(t *testing.T) {
	c := &Client{responseManager: openai.GetManager()}
	if err := c.DeleteResponse(context.Background(), "resp_missing_delete"); err == nil {
		t.Error("expected error for missing response")
	}
}

func TestCompactResponse(t *testing.T) {
	srv := httptest.NewServer(chatOKHandler("summary"))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	resp, err := c.CreateResponse(context.Background(), openai.CreateResponseRequest{
		Model: "llama3",
		Input: []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatalf("CreateResponse() error: %v", err)
	}
	compacted, err := c.CompactResponse(context.Background(), resp.ID)
	if err != nil {
		t.Fatalf("CompactResponse() error: %v", err)
	}
	if compacted.ID != resp.ID {
		t.Errorf("ID = %q, want %q", compacted.ID, resp.ID)
	}
}

func TestCompactResponseNotFound(t *testing.T) {
	c := &Client{responseManager: openai.GetManager()}
	if _, err := c.CompactResponse(context.Background(), "resp_missing_compact"); err == nil {
		t.Error("expected error for missing response")
	}
}

func TestStreamResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, ndjson(t, chatResponse{Model: "llama3", Message: message{Role: "assistant", Content: "hello"}}))
		flusher.Flush()
		io.WriteString(w, ndjson(t, chatResponse{Model: "llama3", Done: true, DoneReason: "stop"}))
		flusher.Flush()
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	stream := c.StreamResponse(context.Background(), openai.CreateResponseRequest{
		Model: "llama3",
		Input: []any{map[string]any{"type": "message", "role": "user", "content": "hi"}},
	})

	var sawCompleted bool
	for stream.Next() {
		ev := stream.Current()
		if ev.Type == "response.completed" {
			sawCompleted = true
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if !sawCompleted {
		t.Error("did not observe a response.completed event")
	}
}

func TestSupportsCapabilityResponsesFalse(t *testing.T) {
	c := &Client{}
	if c.SupportsCapability("responses") {
		t.Error("SupportsCapability(responses) = true, want false (emulated, not native)")
	}
}
