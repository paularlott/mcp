package ai

import (
	"github.com/paularlott/mcp/ai/openai"
)

// Config is an alias to openai.Config with Provider field
type Config struct {
	openai.Config
	Provider Provider
}
