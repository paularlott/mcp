package mcp

import "fmt"

// MCP JSON-RPC Error Codes
// These are standard JSON-RPC 2.0 error codes used by the MCP protocol.
// See: https://www.jsonrpc.org/specification#error_object
const (
	// ErrorCodeParseError indicates invalid JSON was received by the server.
	// Use when the request body cannot be parsed as JSON.
	ErrorCodeParseError = -32700

	// ErrorCodeInvalidRequest indicates the JSON sent is not a valid Request object.
	// Use when required fields are missing or have wrong types.
	ErrorCodeInvalidRequest = -32600

	// ErrorCodeMethodNotFound indicates the method does not exist or is not available.
	// Used internally when an unknown MCP method is called.
	ErrorCodeMethodNotFound = -32601

	// ErrorCodeInvalidParams indicates invalid method parameters.
	// Use this in tool handlers when required parameters are missing or invalid.
	// Prefer using NewToolErrorInvalidParams() helper.
	ErrorCodeInvalidParams = -32602

	// ErrorCodeInternalError indicates an internal JSON-RPC error.
	// Use this in tool handlers for unexpected server-side errors.
	// Prefer using NewToolErrorInternal() helper.
	ErrorCodeInternalError = -32603

	// ErrorCodeImplementationErrorStart is the start of the implementation-defined
	// server error range (-32000 to -32099). Use codes in this range for
	// application-specific errors. Create with NewToolError().
	ErrorCodeImplementationErrorStart = -32000

	// ErrorCodeImplementationErrorEnd is the end of the implementation-defined
	// server error range.
	ErrorCodeImplementationErrorEnd = -32099
)

// ToolError represents an MCP protocol error that can be returned from tool handlers.
// When returned from a ToolHandler, the error code and message are sent to the client
// in the JSON-RPC error response.
//
// Example usage in a tool handler:
//
//	func myHandler(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
//	    name, err := req.String("name")
//	    if err != nil {
//	        return nil, mcp.NewToolErrorInvalidParams("name parameter is required")
//	    }
//	    // ... process request
//	}
type ToolError struct {
	Code    int
	Message string
	Data    interface{}
}

func (e *ToolError) Error() string {
	return fmt.Sprintf("MCP Error %d: %s", e.Code, e.Message)
}

// NewToolErrorInvalidParams creates an error for invalid or missing parameters.
// Use this when a required parameter is missing, has the wrong type, or fails validation.
// This returns ErrorCodeInvalidParams (-32602).
func NewToolErrorInvalidParams(message string) error {
	return &ToolError{
		Code:    ErrorCodeInvalidParams,
		Message: message,
	}
}

// NewToolErrorInternal creates an error for internal server errors.
// Use this for unexpected failures like database errors, network issues, etc.
// This returns ErrorCodeInternalError (-32603).
func NewToolErrorInternal(message string) error {
	return &ToolError{
		Code:    ErrorCodeInternalError,
		Message: message,
	}
}

// NewToolError creates a custom MCP error with a specific code.
// Use codes in the range -32000 to -32099 for application-specific errors.
// The data parameter can include additional error details and will be serialized to JSON.
//
// Example:
//
//	return nil, mcp.NewToolError(-32001, "Rate limit exceeded", map[string]interface{}{
//	    "retry_after": 60,
//	    "limit": 100,
//	})
func NewToolError(code int, message string, data interface{}) error {
	return &ToolError{
		Code:    code,
		Message: message,
		Data:    data,
	}
}
