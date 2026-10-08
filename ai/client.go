package ai

import (
	"context"
)

// Client is the universal LLM client interface
type Client interface {
	// Provider information
	Provider() string

	// Provider capabilities (client-level features)
	SupportsCapability(cap string) bool

	// Model management
	GetModels(ctx context.Context) (*ModelsResponse, error)

	// Chat completions
	ChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error)
	StreamChatCompletion(ctx context.Context, req ChatCompletionRequest) *ChatStream

	// Embeddings
	CreateEmbedding(ctx context.Context, req EmbeddingRequest) (*EmbeddingResponse, error)

	// OpenAI Responses API
	CreateResponse(ctx context.Context, req CreateResponseRequest) (*ResponseObject, error)
	StreamResponse(ctx context.Context, req CreateResponseRequest) *ResponseStream
	GetResponse(ctx context.Context, id string) (*ResponseObject, error)
	CancelResponse(ctx context.Context, id string) (*ResponseObject, error)
	DeleteResponse(ctx context.Context, id string) error
	CompactResponse(ctx context.Context, req CompactResponseRequest) (*CompactedResponse, error)

	// Close/cleanup
	Close() error
}

// DecisionCaller is implemented by clients whose provider runs decision
// models (see ProviderCapabilityDecision). It is a separate interface, not
// a Client method, so existing Client implementations keep compiling; probe
// with a type assertion:
//
//	dc, ok := client.(ai.DecisionCaller)
type DecisionCaller interface {
	Decide(ctx context.Context, req SystemOneRequest) (*SystemOneResponse, error)
}
