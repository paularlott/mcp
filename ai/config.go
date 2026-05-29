package ai

import (
	"github.com/paularlott/mcp/ai/openai"
)

// Config is an alias to openai.Config with Provider field
type Config struct {
	openai.Config
	Provider Provider
}

// BoolPtr returns a pointer to the given bool value.
// Useful for configuring bool pointer fields in Config:
//
//	RetryOnRateLimit: ai.BoolPtr(false)
func BoolPtr(v bool) *bool { return &v }
