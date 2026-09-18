package ai

import (
	"testing"

	"github.com/paularlott/mcp/ai/claude"
	"github.com/paularlott/mcp/ai/gemini"
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

func TestNewClientClaudeUsesClaudeClientAndDefaultsMaxTokens(t *testing.T) {
	c, err := NewClient(Config{Provider: ProviderClaude, Config: openai.Config{APIKey: "k"}})
	if err != nil {
		t.Fatalf("NewClient(claude) error: %v", err)
	}
	if _, ok := c.(*claude.Client); !ok {
		t.Fatalf("NewClient(claude) returned %T, want *claude.Client", c)
	}
	// Claude requires max_tokens; the factory must default it rather than
	// send an invalid (zero) value upstream.
	if _, err := NewClient(Config{Provider: ProviderClaude, Config: openai.Config{APIKey: "k", MaxTokens: 100}}); err != nil {
		t.Fatalf("NewClient(claude) with explicit MaxTokens error: %v", err)
	}
}

func TestNewClientGeminiUsesGeminiClient(t *testing.T) {
	c, err := NewClient(Config{Provider: ProviderGemini, Config: openai.Config{APIKey: "k"}})
	if err != nil {
		t.Fatalf("NewClient(gemini) error: %v", err)
	}
	if _, ok := c.(*gemini.Client); !ok {
		t.Fatalf("NewClient(gemini) returned %T, want *gemini.Client", c)
	}
}

func TestNewClientRejectsEmptyProvider(t *testing.T) {
	_, err := NewClient(Config{})
	if err == nil {
		t.Fatal("expected an error for an empty provider")
	}
}

func TestNewClientRejectsUnknownProvider(t *testing.T) {
	_, err := NewClient(Config{Provider: Provider("not-a-real-provider")})
	if err == nil {
		t.Fatal("expected an error for an unknown provider")
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"empty provider", Config{}, true},
		{"unknown provider", Config{Provider: Provider("bogus")}, true},
		{"openai", Config{Provider: ProviderOpenAI}, false},
		{"claude", Config{Provider: ProviderClaude}, false},
		{"gemini", Config{Provider: ProviderGemini}, false},
		{"ollama", Config{Provider: ProviderOllama}, false},
		{"zai", Config{Provider: ProviderZAi}, false},
		{"mistral", Config{Provider: ProviderMistral}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(&tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateConfig(%+v) error = %v, wantErr %v", tt.cfg, err, tt.wantErr)
			}
		})
	}
}

func TestRequiresAPIKey(t *testing.T) {
	if requiresAPIKey(ProviderOllama) {
		t.Error("ollama should not require an API key")
	}
	for _, p := range []Provider{ProviderOpenAI, ProviderClaude, ProviderGemini, ProviderZAi, ProviderMistral} {
		if !requiresAPIKey(p) {
			t.Errorf("%s should require an API key", p)
		}
	}
}

func TestBoolPtr(t *testing.T) {
	p := BoolPtr(true)
	if p == nil || *p != true {
		t.Fatalf("BoolPtr(true) = %v, want pointer to true", p)
	}
	p2 := BoolPtr(false)
	if p2 == nil || *p2 != false {
		t.Fatalf("BoolPtr(false) = %v, want pointer to false", p2)
	}
	if p == p2 {
		t.Fatal("BoolPtr should return a fresh pointer each call")
	}
}
