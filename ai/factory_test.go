package ai

import (
	"testing"

	"github.com/paularlott/mcp/ai/ollama"
	"github.com/paularlott/mcp/ai/openai"
)

// TestNewClientOllamaUsesOllamaClient guards against the factory silently
// routing ollama back through the OpenAI client. An ollama provider MUST land
// on the native ollama client so it speaks /api/* upstream.
func TestNewClientOllamaUsesOllamaClient(t *testing.T) {
	c, err := NewClient(Config{
		Provider: ProviderOllama,
		Config: openai.Config{
			BaseURL: "http://127.0.0.1:11434", // not actually contacted at construction time
		},
	})
	if err != nil {
		t.Fatalf("NewClient(ollama) error: %v", err)
	}
	if c.Provider() != "ollama" {
		t.Fatalf("Provider() = %q, want %q", c.Provider(), "ollama")
	}
	if _, ok := c.(*ollama.Client); !ok {
		t.Fatalf("NewClient(ollama) returned %T, want *ollama.Client", c)
	}
}

// TestNewClientOpenAIUsesOpenAIClient is the complementary guard: non-ollama
// OpenAI-compatible providers still use the OpenAI client.
func TestNewClientOpenAIUsesOpenAIClient(t *testing.T) {
	for _, p := range []Provider{ProviderOpenAI, ProviderZAi, ProviderMistral} {
		c, err := NewClient(Config{Provider: p, Config: openai.Config{APIKey: "k"}})
		if err != nil {
			t.Fatalf("NewClient(%s) error: %v", p, err)
		}
		if _, ok := c.(*openai.Client); !ok {
			t.Fatalf("NewClient(%s) returned %T, want *openai.Client", p, c)
		}
	}
}
