package ollama

import (
	"context"
	"fmt"
	"net/http"

	"github.com/paularlott/mcp/ai/openai"
)

// Decide runs a System One decision request (POST /v1/systemone): it scores
// the request's state against its named questions — classification (choice),
// yes/no probability (noul) or rubric scoring — in a single response, with
// no generation controls or streaming. Requires an Ollama server v0.35.0+
// and a scoring-capable model (nimble, tev1, clef, clef-flash); the server
// rejects anything else, so model suitability is left to it.
func (c *Client) Decide(ctx context.Context, req openai.SystemOneRequest) (*openai.SystemOneResponse, error) {
	var resp openai.SystemOneResponse
	if err := c.doRequest(ctx, http.MethodPost, "v1/systemone", req, &resp); err != nil {
		return nil, fmt.Errorf("ollama decide failed: %w", err)
	}
	return &resp, nil
}
