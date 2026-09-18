package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/paularlott/mcp/ai/openai"
)

func TestGetModelsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tags":
			json.NewEncoder(w).Encode(tagsResponse{Models: []tagModel{{Name: "llama3"}, {Name: "mistral"}}})
		case "/api/show":
			var req showRequest
			json.NewDecoder(r.Body).Decode(&req)
			switch req.Name {
			case "llama3":
				json.NewEncoder(w).Encode(showResponse{ModelInfo: map[string]any{"context_length": float64(8192)}})
			case "mistral":
				json.NewEncoder(w).Encode(showResponse{ModelInfo: map[string]any{"mistral.context_length": float64(32768)}})
			}
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	resp, err := c.GetModels(context.Background())
	if err != nil {
		t.Fatalf("GetModels() error: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("models = %+v, want 2", resp.Data)
	}
	got := map[string]int{}
	for _, m := range resp.Data {
		got[m.ID] = m.ContextWindow
		if m.Object != "model" || m.OwnedBy != "ollama" {
			t.Errorf("model = %+v", m)
		}
	}
	if got["llama3"] != 8192 {
		t.Errorf("llama3 context window = %d, want 8192", got["llama3"])
	}
	if got["mistral"] != 32768 {
		t.Errorf("mistral context window = %d, want 32768 (family-qualified key)", got["mistral"])
	}
}

func TestGetModelsTagsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL, MaxRetries: -1})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	_, err = c.GetModels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failed to list ollama models") {
		t.Fatalf("error = %v, want wrapped list error", err)
	}
}

func TestGetModelsShowFailureIsBestEffort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tags":
			json.NewEncoder(w).Encode(tagsResponse{Models: []tagModel{{Name: "broken"}, {Name: "ok"}}})
		case "/api/show":
			var req showRequest
			json.NewDecoder(r.Body).Decode(&req)
			if req.Name == "broken" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(showResponse{ModelInfo: map[string]any{"context_length": float64(4096)}})
		}
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL, MaxRetries: -1})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	resp, err := c.GetModels(context.Background())
	if err != nil {
		t.Fatalf("GetModels() error: %v", err)
	}
	got := map[string]int{}
	for _, m := range resp.Data {
		got[m.ID] = m.ContextWindow
	}
	if got["broken"] != 0 {
		t.Errorf("broken model context window = %d, want 0 (best-effort failure)", got["broken"])
	}
	if got["ok"] != 4096 {
		t.Errorf("ok model context window = %d, want 4096", got["ok"])
	}
}

func TestFetchContextSizesEmpty(t *testing.T) {
	c := &Client{}
	if got := c.fetchContextSizes(context.Background(), nil); got != nil {
		t.Errorf("fetchContextSizes(nil) = %v, want nil", got)
	}
}

func TestFetchContextSizesContextAlreadyCancelled(t *testing.T) {
	c := &Client{baseURL: "http://example.com/"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := c.fetchContextSizes(ctx, []tagModel{{Name: "a"}, {Name: "b"}})
	if len(got) != 0 {
		t.Errorf("fetchContextSizes(cancelled) = %v, want empty", got)
	}
}

func TestShowContextLengthBareKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(showResponse{ModelInfo: map[string]any{"context_length": float64(2048)}})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL + "/"}
	n, ok := c.showContextLength(context.Background(), srv.Client(), "m")
	if !ok || n != 2048 {
		t.Errorf("showContextLength = (%d, %v), want (2048, true)", n, ok)
	}
}

func TestShowContextLengthFamilyQualifiedKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(showResponse{ModelInfo: map[string]any{"llama.context_length": float64(4096)}})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL + "/"}
	n, ok := c.showContextLength(context.Background(), srv.Client(), "m")
	if !ok || n != 4096 {
		t.Errorf("showContextLength = (%d, %v), want (4096, true)", n, ok)
	}
}

func TestShowContextLengthMissingKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(showResponse{ModelInfo: map[string]any{"unrelated": "x"}})
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL + "/"}
	_, ok := c.showContextLength(context.Background(), srv.Client(), "m")
	if ok {
		t.Error("showContextLength ok = true, want false when no context length key present")
	}
}

func TestShowContextLengthNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL + "/"}
	_, ok := c.showContextLength(context.Background(), srv.Client(), "m")
	if ok {
		t.Error("showContextLength ok = true, want false on non-200 status")
	}
}

func TestShowContextLengthBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	c := &Client{baseURL: srv.URL + "/"}
	_, ok := c.showContextLength(context.Background(), srv.Client(), "m")
	if ok {
		t.Error("showContextLength ok = true, want false on invalid JSON")
	}
}

func TestShowContextLengthRequestCreationError(t *testing.T) {
	// An invalid base URL containing a control character makes
	// http.NewRequestWithContext fail, exercising that early-return path.
	c := &Client{baseURL: "http://\x7f/"}
	_, ok := c.showContextLength(context.Background(), http.DefaultClient, "m")
	if ok {
		t.Error("showContextLength ok = true, want false on request creation error")
	}
}

func TestShowContextLengthClientDoError(t *testing.T) {
	c := &Client{baseURL: "http://127.0.0.1:1/"} // nothing listens here
	client := &http.Client{Timeout: 200 * time.Millisecond}
	_, ok := c.showContextLength(context.Background(), client, "m")
	if ok {
		t.Error("showContextLength ok = true, want false when the request fails")
	}
}

func TestModelShowClientUsesConfiguredPool(t *testing.T) {
	c := &Client{}
	if c.modelShowClient() == nil {
		t.Fatal("modelShowClient() returned nil")
	}
}

func TestContextLengthFromAllTypes(t *testing.T) {
	tests := []struct {
		in   any
		want int
		ok   bool
	}{
		{float64(8192), 8192, true},
		{float64(0), 0, false},
		{float64(-1), 0, false},
		{float32(4096), 4096, true},
		{float32(0), 0, false},
		{int64(2048), 2048, true},
		{int64(0), 0, false},
		{int(1024), 1024, true},
		{int(-5), 0, false},
		{json.Number("512"), 512, true},
		{json.Number("0"), 0, false},
		{json.Number("not a number"), 0, false},
		{"x", 0, false},
		{nil, 0, false},
		{true, 0, false},
	}
	for _, tt := range tests {
		got, ok := contextLengthFrom(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("contextLengthFrom(%#v) = (%d,%v), want (%d,%v)", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
