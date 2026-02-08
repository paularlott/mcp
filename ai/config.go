package ai

import (
	"net/http"

	"github.com/paularlott/mcp/ai/openai"
	"github.com/paularlott/mcp/pool"
)

// Config holds universal client configuration
type Config struct {
	Provider         Provider
	APIKey           string
	BaseURL          string
	HTTPPool         pool.HTTPPool
	ExtraHeaders     http.Header
	LocalServer      MCPServer
	MCPServerConfigs []RemoteServerConfig
}

// Type aliases to openai types
type MCPServer = openai.MCPServer
type RemoteServerConfig = openai.RemoteServerConfig
