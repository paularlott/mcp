package mcp

// MCP Protocol types
type MCPRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type MCPResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *MCPError   `json:"error,omitempty"`
}

type MCPError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type initializeParams struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	ClientInfo      clientInfo             `json:"clientInfo"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    capabilities `json:"capabilities"`
	ServerInfo      serverInfo   `json:"serverInfo"`
	Instructions    string       `json:"instructions,omitempty"`
}

type capabilities struct {
	Tools     map[string]interface{} `json:"tools"`
	Resources map[string]interface{} `json:"resources,omitempty"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type MCPTool struct {
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	InputSchema   interface{}    `json:"inputSchema"`
	OutputSchema  interface{}    `json:"outputSchema,omitempty"`
	Keywords      []string       `json:"-"` // For discovery search, not serialized to clients
	Visibility    ToolVisibility `json:"-"` // Native or Discoverable
}

type ToolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

type ToolResult struct {
	Content           []ToolContent `json:"content,omitempty"`
	StructuredContent interface{}   `json:"structuredContent,omitempty"`
	IsError           bool          `json:"isError,omitempty"`
}

type ToolContent struct {
	Type     string           `json:"type"`
	Text     string           `json:"text,omitempty"`
	Data     string           `json:"data,omitempty"`
	MimeType string           `json:"mimeType,omitempty"`
	Resource *ResourceContent `json:"resource,omitempty"`
}

type ResourceResponse struct {
	Contents []ResourceContent `json:"contents"`
}

type ResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"` // base64 encoded
}
