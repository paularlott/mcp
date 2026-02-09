package ai

import (
	"fmt"

	"github.com/paularlott/mcp/ai/claude"
	"github.com/paularlott/mcp/ai/gemini"
	"github.com/paularlott/mcp/ai/openai"
)

// NewClient creates a new LLM client for the specified provider
func NewClient(config Config) (Client, error) {
	// Validate configuration
	if err := validateConfig(&config); err != nil {
		return nil, err
	}

	// Set provider string for openai-compatible providers
	config.Config.Provider = string(config.Provider)

	// Create provider-specific client
	switch config.Provider {
	case ProviderOpenAI, ProviderOllama, ProviderZAi, ProviderMistral:
		return openai.New(config.Config)
	case ProviderClaude:
		// Claude requires max_tokens, set default if not provided
		if config.MaxTokens == 0 {
			config.MaxTokens = 4096
		}
		return claude.New(config.Config)
	case ProviderGemini:
		return gemini.New(config.Config)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", config.Provider)
	}
}

// validateConfig validates the client configuration
func validateConfig(config *Config) error {
	if config.Provider == "" {
		return fmt.Errorf("provider is required")
	}

	validProviders := map[Provider]bool{
		ProviderOpenAI:  true,
		ProviderClaude:  true,
		ProviderGemini:  true,
		ProviderOllama:  true,
		ProviderZAi:     true,
		ProviderMistral: true,
	}
	if !validProviders[config.Provider] {
		return fmt.Errorf("unknown provider: %s", config.Provider)
	}

	return nil
}

// requiresAPIKey returns true if the provider requires an API key
func requiresAPIKey(provider Provider) bool {
	return provider != ProviderOllama
}
