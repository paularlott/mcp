package ai

import (
	"github.com/paularlott/mcp/ai/openai"
)

// Provider identifies the LLM provider
type Provider string

const (
	ProviderOpenAI  Provider = "openai"
	ProviderClaude  Provider = "claude"
	ProviderGemini  Provider = "gemini"
	ProviderOllama  Provider = "ollama"
	ProviderZAi     Provider = "zai"
	ProviderMistral Provider = "mistral"
	ProviderGrok    Provider = "grok"
)

// ProviderCapability represents provider-level features
type ProviderCapability string

const (
	ProviderCapabilityEmbedding ProviderCapability = "embeddings"
	// ProviderCapabilityResponses marks clients that use the provider's
	// native Responses API (OpenAI, xAI).
	ProviderCapabilityResponses ProviderCapability = "responses"
	// ProviderCapabilityResponsesEmulated marks clients that serve the
	// Responses API by emulating it over chat completions, storing
	// conversations themselves (see openai.Config.ResponseStore). Every
	// client reports exactly one of responses and responses_emulated.
	ProviderCapabilityResponsesEmulated ProviderCapability = "responses_emulated"
	// ProviderCapabilityDecision marks providers that can run decision
	// models (Ollama's System One endpoint): classification, yes/no
	// probabilities and rubric scoring instead of chat generation. The
	// authoritative check is the DecisionCaller interface: a client may
	// report a capability this package does not yet model.
	ProviderCapabilityDecision ProviderCapability = "decision"
)

// Type aliases to openai types
type Message = openai.Message
type ChatCompletionRequest = openai.ChatCompletionRequest
type ChatCompletionResponse = openai.ChatCompletionResponse
type Tool = openai.Tool
type ToolCall = openai.ToolCall
type ToolFunction = openai.ToolFunction
type EmbeddingRequest = openai.EmbeddingRequest
type EmbeddingResponse = openai.EmbeddingResponse
type CreateResponseRequest = openai.CreateResponseRequest
type ResponseObject = openai.ResponseObject
type CompactResponseRequest = openai.CompactResponseRequest

// ErrResponseNotFound is returned (wrapped) for an unknown, expired or
// other client's emulated response, including an unknown previous_response_id.
var ErrResponseNotFound = openai.ErrResponseNotFound

type CompactedResponse = openai.CompactedResponse
type Usage = openai.Usage
type ContentPart = openai.ContentPart
type ImageURL = openai.ImageURL
type Embedding = openai.Embedding
type ChatStream = openai.ChatStream
type ResponseStream = openai.ResponseStream
type ModelsResponse = openai.ModelsResponse
type Model = openai.Model

// System One (decision model) types. See openai/systemone.go.
type SystemOneRequest = openai.SystemOneRequest
type SystemOneQuestion = openai.SystemOneQuestion
type SystemOneAnswer = openai.SystemOneAnswer
type SystemOneUsage = openai.SystemOneUsage
type SystemOneResponse = openai.SystemOneResponse
