package mcp

// MCP Protocol types
type MCPRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// MCPNotification is a JSON-RPC 2.0 notification — a message that carries no
// "id" member. Unlike MCPRequest (whose ID field has no omitempty and would
// serialize as "id":null), this struct makes it structurally impossible to
// emit an "id" field, conforming to the JSON-RPC 2.0 spec which requires that
// notifications omit "id" entirely.
type MCPNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type MCPResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *MCPError `json:"error,omitempty"`
}

type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type initializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ClientInfo      clientInfo     `json:"clientInfo"`
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
	Tools     map[string]any `json:"tools"`
	Resources map[string]any `json:"resources,omitempty"`
	Prompts   map[string]any `json:"prompts,omitempty"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type MCPTool struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  any            `json:"inputSchema"`
	OutputSchema any            `json:"outputSchema,omitempty"`
	Keywords     []string       `json:"-"` // For discovery search, not serialized to clients
	Visibility   ToolVisibility `json:"-"` // Native or Discoverable
}

type ToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type ToolResult struct {
	Content           []ToolContent `json:"content,omitempty"`
	StructuredContent any           `json:"structuredContent,omitempty"`
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

// MCPResource describes a static resource exposed via resources/list.
// A resource is data the server can serve to clients by URI, such as a file,
// a configuration document, or a database record.
type MCPResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// MCPResourceTemplate describes a parameterized resource exposed via
// resources/templates/list. The URITemplate may contain {var} placeholders
// (RFC 6570 level 1) that clients expand to concrete URIs and then read.
type MCPResourceTemplate struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// resourceReadParams is the params object for resources/read.
type resourceReadParams struct {
	URI string `json:"uri"`
}

// MCPPrompt describes a prompt exposed via prompts/list. A prompt is a reusable
// message template with named arguments that produces messages for the model.
type MCPPrompt struct {
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Arguments   []MCPPromptArgument `json:"arguments,omitempty"`
}

// MCPPromptArgument describes one argument a prompt accepts.
type MCPPromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
}

// PromptMessageRole is the role of a message within a prompt response.
type PromptMessageRole string

const (
	// PromptRoleUser marks a message from the user.
	PromptRoleUser PromptMessageRole = "user"
	// PromptRoleAssistant marks a message from the assistant.
	PromptRoleAssistant PromptMessageRole = "assistant"
)

// PromptMessageContent is the content block of a prompt message. It is the same
// shape as the content block used by tool responses (text, image, audio, or an
// embedded resource), so the ToolContent constructors can be reused.
type PromptMessageContent = ToolContent

// PromptMessage is a single message in a prompt response.
type PromptMessage struct {
	Role    PromptMessageRole    `json:"role"`
	Content PromptMessageContent `json:"content"`
}

// PromptResponse is the result of prompts/get.
type PromptResponse struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}

// promptGetParams is the params object for prompts/get.
type promptGetParams struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
}
