package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/paularlott/mcp/ai/openai"
)

// GetModels lists installed models via /api/tags and enriches each with its
// context window from /api/show. The per-model /api/show fan-out is bounded
// and best-effort: a failure for one model leaves its ContextWindow at zero,
// so callers fall back to their configured default.
func (c *Client) GetModels(ctx context.Context) (*openai.ModelsResponse, error) {
	var tags tagsResponse
	if err := c.doRequest(ctx, http.MethodGet, "api/tags", nil, &tags); err != nil {
		return nil, fmt.Errorf("failed to list ollama models: %w", err)
	}

	contexts := c.fetchContextSizes(ctx, tags.Models)
	models := make([]openai.Model, 0, len(tags.Models))
	for _, m := range tags.Models {
		models = append(models, openai.Model{
			ID:            m.Name,
			Object:        "model",
			OwnedBy:       "ollama",
			ContextWindow: contexts[m.Name],
		})
	}
	return &openai.ModelsResponse{Object: "list", Data: models}, nil
}

// fetchContextSizes calls /api/show concurrently for each model and returns
// model-name -> context length. It honours ctx and is best-effort.
func (c *Client) fetchContextSizes(ctx context.Context, models []tagModel) map[string]int {
	if len(models) == 0 {
		return nil
	}
	out := make(map[string]int, len(models))
	var mu sync.Mutex
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup

	httpClient := c.modelShowClient()
	for _, m := range models {
		if ctx.Err() != nil {
			break
		}
		name := m.Name
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ctx2, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			if ctx2.Err() != nil {
				return
			}
			if n, ok := c.showContextLength(ctx2, httpClient, name); ok {
				mu.Lock()
				out[name] = n
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return out
}

// modelShowClient returns the HTTP client to use for /api/show, honouring the
// configured pool when set (matching doRequest) and otherwise the shared pool.
func (c *Client) modelShowClient() *http.Client {
	return c.httpClient()
}

// showContextLength POSTs /api/show for one model and extracts its context
// length from model_info. The key is family-qualified (e.g. llama.context_length)
// on most builds; some also expose a bare context_length.
func (c *Client) showContextLength(ctx context.Context, client *http.Client, model string) (int, bool) {
	body, _ := json.Marshal(showRequest{Name: model})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"api/show", strings.NewReader(string(body)))
	if err != nil {
		return 0, false
	}
	c.setHeaders(req)

	resp, err := client.Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, false
	}
	var sr showResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return 0, false
	}
	if n, ok := contextLengthFrom(sr.ModelInfo["context_length"]); ok {
		return n, true
	}
	for k, v := range sr.ModelInfo {
		if strings.HasSuffix(k, ".context_length") {
			if n, ok := contextLengthFrom(v); ok {
				return n, true
			}
		}
	}
	return 0, false
}

// contextLengthFrom coerces a model_info value (float64 / int64 / json.Number)
// into an int token count.
func contextLengthFrom(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		if n > 0 {
			return int(n), true
		}
	case float32:
		if n > 0 {
			return int(n), true
		}
	case int64:
		if n > 0 {
			return int(n), true
		}
	case int:
		if n > 0 {
			return n, true
		}
	case json.Number:
		if i, err := n.Int64(); err == nil && i > 0 {
			return int(i), true
		}
	}
	return 0, false
}
