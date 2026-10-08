package openai

import "testing"

func TestNew_GrokDefaults(t *testing.T) {
	c, err := New(Config{Provider: providerGrok, APIKey: "k"})
	if err != nil {
		t.Fatalf("New(grok) error: %v", err)
	}
	if c.baseURL != "https://api.x.ai/v1/" {
		t.Errorf("baseURL = %q, want %q", c.baseURL, "https://api.x.ai/v1/")
	}
	if !c.useNativeResponses {
		t.Error("native responses should be auto-enabled for api.x.ai")
	}
	if !c.SupportsCapability("responses") {
		t.Error("grok should report responses support")
	}
	if c.SupportsCapability("responses_emulated") {
		t.Error("grok on api.x.ai should not report emulated responses")
	}
	if c.SupportsCapability("embeddings") {
		t.Error("grok should not report embeddings support")
	}
}

func TestNew_GrokNativeResponsesAutoDetect(t *testing.T) {
	native := false
	tests := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"custom host", Config{Provider: providerGrok, BaseURL: "https://proxy.example.com/v1"}, false},
		{"lookalike host", Config{Provider: providerGrok, BaseURL: "https://api.x.ai.evil.com/v1"}, false},
		{"explicit opt-out", Config{Provider: providerGrok, UseNativeResponses: &native}, false},
		{"x.ai host on other provider", Config{Provider: providerMistral, BaseURL: "https://api.x.ai/v1"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := New(tt.cfg)
			if err != nil {
				t.Fatalf("New error: %v", err)
			}
			if c.useNativeResponses != tt.want {
				t.Errorf("useNativeResponses = %v, want %v", c.useNativeResponses, tt.want)
			}
			if c.SupportsCapability("responses") != tt.want {
				t.Errorf("SupportsCapability(responses) = %v, want %v", !tt.want, tt.want)
			}
			if c.SupportsCapability("responses_emulated") == tt.want {
				t.Errorf("SupportsCapability(responses_emulated) = %v, want %v", tt.want, !tt.want)
			}
		})
	}
}

func TestNew_OpenAINativeResponsesAutoDetect(t *testing.T) {
	c, _ := New(Config{})
	if !c.useNativeResponses {
		t.Error("native responses should be auto-enabled for api.openai.com")
	}
	c, _ = New(Config{BaseURL: "https://api.openai.com.evil.com/v1"})
	if c.useNativeResponses {
		t.Error("native responses should not be enabled for a lookalike host")
	}
}
