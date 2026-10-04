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

func TestDecideMixedQuestions(t *testing.T) {
	var gotPath string
	var gotBody openai.SystemOneRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"model": "clef-flash",
			"answers": map[string]any{
				"label": map[string]any{
					"type":   "choice",
					"choice": "bug",
					"probabilities": map[string]float64{
						"billing": 0.0125, "bug": 0.9781, "account": 0.0093,
					},
					"confidence": 0.8906,
				},
				"urgent": map[string]any{"type": "noul", "noul": 0.959},
				"severity": map[string]any{
					"type":  "score",
					"score": 3.2,
					"legend": map[string]string{
						"0": "cosmetic", "1": "minor", "2": "major", "3": "critical", "4": "outage",
					},
					"probabilities": map[string]float64{
						"0": 0.01, "1": 0.04, "2": 0.1, "3": 0.8, "4": 0.05,
					},
					"confidence": 0.62,
				},
			},
			"usage": map[string]int{"input_tokens": 174, "output_tokens": 1},
		})
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	req := openai.SystemOneRequest{
		Model:  "clef-flash",
		State:  "Checkout has returned 500 errors since 9am.",
		Images: []string{"aGVsbG8="},
		Questions: map[string]openai.SystemOneQuestion{
			"label": {
				Type:         "choice",
				Instructions: "Which label fits this ticket?",
				Criteria:     map[string]any{"billing": "Payments", "bug": "Errors", "account": "Logins"},
			},
			"urgent": {Type: "noul", Instructions: "Is paging justified?"},
			"severity": {
				Type:         "score",
				Instructions: "How severe is it?",
				Criteria:     []any{"cosmetic", "minor", "major", "critical", "outage"},
			},
		},
		KeepAlive: 300,
	}
	resp, err := c.Decide(context.Background(), req)
	if err != nil {
		t.Fatalf("Decide() error: %v", err)
	}

	if gotPath != "/v1/systemone" {
		t.Errorf("path = %q, want /v1/systemone", gotPath)
	}
	if gotBody.Model != "clef-flash" || len(gotBody.Questions) != 3 {
		t.Errorf("request = %+v", gotBody)
	}
	if len(gotBody.Images) != 1 || gotBody.Images[0] != "aGVsbG8=" {
		t.Errorf("images = %v", gotBody.Images)
	}
	if gotBody.KeepAlive != float64(300) {
		t.Errorf("keep_alive = %v (%T)", gotBody.KeepAlive, gotBody.KeepAlive)
	}
	if gotBody.Questions["severity"].Type != "score" {
		t.Errorf("severity question = %+v", gotBody.Questions["severity"])
	}

	label := resp.Answers["label"]
	if label.Choice != "bug" || label.Confidence != 0.8906 {
		t.Errorf("label answer = %+v", label)
	}
	if label.Probabilities["bug"] != 0.9781 {
		t.Errorf("label probabilities = %+v", label.Probabilities)
	}
	urgent := resp.Answers["urgent"]
	if urgent.Noul == nil || *urgent.Noul != 0.959 {
		t.Errorf("urgent answer = %+v", urgent)
	}
	sev := resp.Answers["severity"]
	if sev.Score == nil || *sev.Score != 3.2 || len(sev.Legend) != 5 {
		t.Errorf("severity answer = %+v", sev)
	}
	if sev.Legend["4"] != "outage" {
		t.Errorf("severity legend = %+v", sev.Legend)
	}
	if sev.Probabilities["3"] != 0.8 {
		t.Errorf("severity probabilities = %+v", sev.Probabilities)
	}
	if resp.Usage.InputTokens != 174 || resp.Usage.OutputTokens != 1 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	if !c.SupportsCapability("decision") {
		t.Errorf("SupportsCapability(decision) = false, want true")
	}
}

func TestDecideSurfacesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "model 'nope' not found"})
	}))
	defer srv.Close()

	c, err := New(openai.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	_, err = c.Decide(context.Background(), openai.SystemOneRequest{
		Model: "nope",
		State: "state",
		Questions: map[string]openai.SystemOneQuestion{
			"q": {Type: "noul", Instructions: "yes?"},
		},
	})
	if err == nil {
		t.Fatal("Decide() should fail on 404")
	}
	if !strings.Contains(err.Error(), "model 'nope' not found") {
		t.Errorf("error should carry the server message, got: %v", err)
	}
}
