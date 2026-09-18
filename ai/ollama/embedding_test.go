package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/paularlott/mcp/ai/openai"
)

func TestCreateEmbeddingStringInput(t *testing.T) {
	var gotBody embedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("path = %q, want /api/embed", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(embedResponse{
			Model:           "nomic",
			Embeddings:      [][]float64{{0.1, 0.2, 0.3}},
			PromptEvalCount: 7,
		})
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	resp, err := c.CreateEmbedding(context.Background(), openai.EmbeddingRequest{Model: "nomic", Input: "hello world"})
	if err != nil {
		t.Fatalf("CreateEmbedding() error: %v", err)
	}
	if len(gotBody.Input) != 1 || gotBody.Input[0] != "hello world" {
		t.Errorf("request input = %+v", gotBody.Input)
	}
	if len(resp.Data) != 1 || resp.Data[0].Index != 0 || len(resp.Data[0].Embedding) != 3 {
		t.Fatalf("unexpected response data: %+v", resp.Data)
	}
	if resp.Usage.PromptTokens != 7 || resp.Usage.TotalTokens != 7 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	if resp.Object != "list" || resp.Model != "nomic" {
		t.Errorf("response = %+v", resp)
	}
}

func TestCreateEmbeddingSliceInput(t *testing.T) {
	var gotBody embedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(embedResponse{
			Model:      "nomic",
			Embeddings: [][]float64{{1, 2}, {3, 4}},
		})
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	resp, err := c.CreateEmbedding(context.Background(), openai.EmbeddingRequest{
		Model: "nomic",
		Input: []string{"a", "b"},
	})
	if err != nil {
		t.Fatalf("CreateEmbedding() error: %v", err)
	}
	if len(gotBody.Input) != 2 {
		t.Errorf("request input = %+v", gotBody.Input)
	}
	if len(resp.Data) != 2 || resp.Data[1].Index != 1 {
		t.Fatalf("response data = %+v", resp.Data)
	}
}

func TestCreateEmbeddingDimensionsOption(t *testing.T) {
	var gotBody embedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(embedResponse{Model: "nomic", Embeddings: [][]float64{{1}}})
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	_, err = c.CreateEmbedding(context.Background(), openai.EmbeddingRequest{Model: "nomic", Input: "x", Dimensions: 256})
	if err != nil {
		t.Fatalf("CreateEmbedding() error: %v", err)
	}
	if gotBody.Options["dimensions"] != float64(256) {
		t.Errorf("options[dimensions] = %v, want 256", gotBody.Options["dimensions"])
	}
}

func TestCreateEmbeddingInvalidInputType(t *testing.T) {
	c := &Client{}
	_, err := c.CreateEmbedding(context.Background(), openai.EmbeddingRequest{Model: "nomic", Input: 42})
	if err == nil || !strings.Contains(err.Error(), "unsupported embedding input type") {
		t.Fatalf("error = %v, want unsupported input type error", err)
	}
}

func TestCreateEmbeddingDoRequestError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`boom`))
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL, MaxRetries: -1})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	_, err = c.CreateEmbedding(context.Background(), openai.EmbeddingRequest{Model: "nomic", Input: "x"})
	if err == nil || !strings.Contains(err.Error(), "ollama embed failed") {
		t.Fatalf("error = %v, want wrapped 'ollama embed failed'", err)
	}
}

func TestEmbeddingInputsVariants(t *testing.T) {
	tests := []struct {
		name    string
		in      any
		want    []string
		wantErr string
	}{
		{"string", "hi", []string{"hi"}, ""},
		{"string slice", []string{"a", "b"}, []string{"a", "b"}, ""},
		{"any slice of strings", []any{"a", "b"}, []string{"a", "b"}, ""},
		{"any slice with non-string", []any{"a", 1}, nil, "unsupported embedding input element"},
		{"unsupported type", 3.14, nil, "unsupported embedding input type"},
		{"nil", nil, nil, "unsupported embedding input type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := embeddingInputs(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
