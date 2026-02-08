package ai

import (
	"net/http"

	"github.com/paularlott/mcp"
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

// Option is a functional option for Config
type Option func(*Config)

// WithAPIKey sets the API key
func WithAPIKey(key string) Option {
	return func(c *Config) { c.APIKey = key }
}

// WithBaseURL sets the base URL
func WithBaseURL(url string) Option {
	return func(c *Config) { c.BaseURL = url }
}

// WithHTTPPool sets a custom HTTP pool
func WithHTTPPool(p pool.HTTPPool) Option {
	return func(c *Config) { c.HTTPPool = p }
}

// WithExtraHeaders sets extra headers
func WithExtraHeaders(headers http.Header) Option {
	return func(c *Config) { c.ExtraHeaders = headers }
}

// WithMCPServer adds a remote MCP server configuration
func WithMCPServer(baseURL string, auth mcp.AuthProvider, namespace string) Option {
	return func(c *Config) {
		c.MCPServerConfigs = append(c.MCPServerConfigs, RemoteServerConfig{
			BaseURL:   baseURL,
			Auth:      auth,
			Namespace: namespace,
		})
	}
}

// WithMCPLocalServer sets the local MCP server
func WithMCPLocalServer(server MCPServer) Option {
	return func(c *Config) { c.LocalServer = server }
}
