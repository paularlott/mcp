package mcp

import "fmt"

// MCP error codes
const (
	ErrorCodeParseError               = -32700
	ErrorCodeInvalidRequest           = -32600
	ErrorCodeMethodNotFound           = -32601
	ErrorCodeInvalidParams            = -32602
	ErrorCodeInternalError            = -32603
	ErrorCodeImplementationErrorStart = -32000
	ErrorCodeImplementationErrorEnd   = -32099
)

// ToolError represents an MCP protocol error that can be returned from tool handlers
type ToolError struct {
	Code    int
	Message string
	Data    interface{}
}

func (e *ToolError) Error() string {
	return fmt.Sprintf("MCP Error %d: %s", e.Code, e.Message)
}

// NewToolErrorInvalidParams creates an error for invalid parameters
func NewToolErrorInvalidParams(message string) error {
	return &ToolError{
		Code:    ErrorCodeInvalidParams,
		Message: message,
	}
}

// NewToolErrorInternal creates an error for internal server errors
func NewToolErrorInternal(message string) error {
	return &ToolError{
		Code:    ErrorCodeInternalError,
		Message: message,
	}
}

// NewToolError creates a custom MCP error with specific code
func NewToolError(code int, message string, data interface{}) error {
	return &ToolError{
		Code:    code,
		Message: message,
		Data:    data,
	}
}
