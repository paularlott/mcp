package ollama

import (
	"context"

	"github.com/paularlott/mcp/ai/openai"
)

// Ollama has no native Responses API; it is emulated on top of chat completions
// via the shared openai helpers, exactly as for claude and gemini.

func (c *Client) StreamResponse(ctx context.Context, req openai.CreateResponseRequest) *openai.ResponseStream {
	eventChan := make(chan openai.ResponseStreamEvent, 50)
	errorChan := make(chan error, 1)
	go func() {
		defer close(eventChan)
		defer close(errorChan)
		openai.StreamResponseEmulated(ctx, c, req, eventChan, errorChan)
	}()
	return openai.NewResponseStream(ctx, eventChan, errorChan)
}

func (c *Client) CreateResponse(ctx context.Context, req openai.CreateResponseRequest) (*openai.ResponseObject, error) {
	return openai.CreateResponseEmulated(ctx, c, c.responseManager, req)
}

func (c *Client) GetResponse(ctx context.Context, id string) (*openai.ResponseObject, error) {
	return openai.GetResponseEmulated(ctx, c.responseManager, id)
}

func (c *Client) CancelResponse(ctx context.Context, id string) (*openai.ResponseObject, error) {
	return openai.CancelResponseEmulated(ctx, c.responseManager, id)
}

func (c *Client) DeleteResponse(ctx context.Context, id string) error {
	return openai.DeleteResponseEmulated(ctx, c.responseManager, id)
}

func (c *Client) CompactResponse(ctx context.Context, id string) (*openai.ResponseObject, error) {
	return openai.CompactResponseEmulated(ctx, c.responseManager, id)
}
