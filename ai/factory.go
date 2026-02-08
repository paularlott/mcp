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

	// Create provider-specific client
	switch config.Provider {
	case ProviderOpenAI, ProviderOllama, ProviderZAi, ProviderMistral:
		return openai.New(openai.Config{
			APIKey:              config.APIKey,
			BaseURL:             config.BaseURL,
			Provider:            string(config.Provider),
			ExtraHeaders:        config.ExtraHeaders,
			HTTPPool:            config.HTTPPool,
			LocalServer:         config.LocalServer,
			RemoteServerConfigs: config.MCPServerConfigs,
		})
	case ProviderClaude:
		return claude.New(openai.Config{
			APIKey:              config.APIKey,
			BaseURL:             config.BaseURL,
			ExtraHeaders:        config.ExtraHeaders,
			HTTPPool:            config.HTTPPool,
			LocalServer:         config.LocalServer,
			RemoteServerConfigs: config.MCPServerConfigs,
		})
	case ProviderGemini:
		return gemini.New(openai.Config{
			APIKey:              config.APIKey,
			BaseURL:             config.BaseURL,
			ExtraHeaders:        config.ExtraHeaders,
			HTTPPool:            config.HTTPPool,
			LocalServer:         config.LocalServer,
			RemoteServerConfigs: config.MCPServerConfigs,
		})
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

	// Validate APIKey for providers that require it
	if requiresAPIKey(config.Provider) && config.APIKey == "" {
		return fmt.Errorf("API key is required for provider: %s", config.Provider)
	}

	return nil
}

// requiresAPIKey returns true if the provider requires an API key
func requiresAPIKey(provider Provider) bool {
	return provider != ProviderOllama
}
