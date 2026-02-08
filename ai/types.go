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
)

// ProviderCapability represents provider-level features
type ProviderCapability string

const (
	ProviderCapabilityEmbedding ProviderCapability = "embeddings"
	ProviderCapabilityResponses ProviderCapability = "responses"
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
type Usage = openai.Usage
type ContentPart = openai.ContentPart
type ImageURL = openai.ImageURL
type Embedding = openai.Embedding
type ChatStream = openai.ChatStream
type ModelsResponse = openai.ModelsResponse
type Model = openai.Model
