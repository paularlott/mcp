package ollama

import (
	"context"
	"fmt"
	"net/http"

	"github.com/paularlott/mcp/ai/openai"
)

// CreateEmbedding maps an OpenAI embedding request onto Ollama's /api/embed.
// Ollama returns one vector per input string; we surface them in input order.
func (c *Client) CreateEmbedding(ctx context.Context, req openai.EmbeddingRequest) (*openai.EmbeddingResponse, error) {
	inputs, err := embeddingInputs(req.Input)
	if err != nil {
		return nil, err
	}

	body := embedRequest{
		Model:   req.Model,
		Input:   inputs,
		Options: options{},
	}
	if req.Dimensions > 0 {
		// Ollama has no first-class dimensions parameter; carry it through
		// options so providers that understand it (none today) can pick it up.
		body.Options["dimensions"] = req.Dimensions
	}

	var resp embedResponse
	if err := c.doRequest(ctx, http.MethodPost, "api/embed", body, &resp); err != nil {
		return nil, fmt.Errorf("ollama embed failed: %w", err)
	}

	data := make([]openai.Embedding, 0, len(resp.Embeddings))
	for i, vec := range resp.Embeddings {
		data = append(data, openai.Embedding{
			Object:    "embedding",
			Index:     i,
			Embedding: vec,
		})
	}

	usage := openai.Usage{PromptTokens: resp.PromptEvalCount}
	usage.TotalTokens = usage.PromptTokens

	return &openai.EmbeddingResponse{
		Object: "list",
		Data:   data,
		Model:  req.Model,
		Usage:  usage,
	}, nil
}

// embeddingInputs normalises the OpenAI embedding input (string, []string, or
// []any of strings) into the []string Ollama expects.
func embeddingInputs(input any) ([]string, error) {
	switch v := input.(type) {
	case string:
		return []string{v}, nil
	case []string:
		return v, nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("unsupported embedding input element: %T", item)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported embedding input type: %T", input)
	}
}
