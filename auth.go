package mcp

// AuthProvider is the interface for MCP client authentication.
type AuthProvider interface {
	GetAuthHeader() (string, error)
	Refresh() error
}
