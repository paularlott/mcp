package mcp

import (
	"errors"
	"fmt"
	"strings"
)

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

	// ErrorCodeInvalidParams indicates invalid method parameters. From a
	// tool handler (NewToolErrorInvalidParams) it is an input validation
	// error, which tools/call reports as a tool execution error (isError).
	ErrorCodeInvalidParams = -32602

	// ErrorCodeInternalError indicates an internal JSON-RPC error.
	// Use this in tool handlers for unexpected server-side errors; tools/call
	// sends it as a protocol error. Prefer using NewToolErrorInternal() helper.
	ErrorCodeInternalError = -32603

	// ErrorCodeImplementationErrorStart is the start of JSON-RPC's
	// implementation-defined server error range (-32000 to -32099).
	//
	// Do not pick new application codes here: MCP 2026-07-28 reserves
	// -32020..-32099 for protocol-defined codes (implementations MUST NOT
	// emit others in that range) and says new implementations SHOULD NOT use
	// -32000..-32019 either. Application-specific errors should use a code
	// outside -32768..-32000, and tool failures should be tool execution
	// errors (NewToolResponseError) rather than protocol errors at all.
	ErrorCodeImplementationErrorStart = -32000

	// ErrorCodeImplementationErrorEnd is the end of that range.
	ErrorCodeImplementationErrorEnd = -32099

	// ErrorCodeResourceNotFound is the Legacy (through 2025-11-25) code for a
	// resources/read of an unknown resource. 2026-07-28 withdraws it in
	// favour of ErrorCodeInvalidParams, which the Modern path uses.
	ErrorCodeResourceNotFound = -32002

	// ErrorCodeHeaderMismatch indicates a Modern-era (protocol revision
	// 2026-07-28+) request's mirrored HTTP headers (MCP-Protocol-Version,
	// Mcp-Method, Mcp-Name, Mcp-Param-*) don't match the corresponding values
	// in the JSON-RPC request body, or a required header is missing.
	ErrorCodeHeaderMismatch = -32020

	// ErrorCodeMissingRequiredClientCapability indicates a Modern-era request
	// needs a capability the client didn't declare in its per-request
	// io.modelcontextprotocol/clientCapabilities metadata.
	ErrorCodeMissingRequiredClientCapability = -32021

	// ErrorCodeUnsupportedProtocolVersion indicates the requested protocol
	// version (Modern or Legacy) isn't one this server implements. The
	// error's data.supported field lists the versions it does support.
	ErrorCodeUnsupportedProtocolVersion = -32022
)

// ToolError is an error with a JSON-RPC code, message and data.
//
// How tools/call reports one a tool handler or provider returns follows the
// MCP tools spec, whose only protocol errors for tools/call are an unknown
// tool, a request that fails the CallToolRequest schema, and a server error.
// A handler runs only after the request has parsed and its tool was found,
// so:
//
//   - a server error — ErrorCodeInternalError (NewToolErrorInternal) or a
//     code in JSON-RPC's server error range, -32000 to -32099, which includes
//     MissingRequiredClientCapability (NewToolErrorMissingClientCapability) —
//     is sent as a JSON-RPC error with its code, message and data;
//   - any other code — ErrorCodeInvalidParams (NewToolErrorInvalidParams) for
//     bad input, or an application code (NewToolError) — is a tool execution
//     error: the result carries the message with isError set, so the model
//     can read it and correct the call. Data is not sent.
//
// A *ToolError relayed from a remote server's tools/call (RegisterRemoteServer)
// is sent as that server sent it.
//
// Server.CallTool returns the *ToolError itself in every case. Detecting one
// as a caller (middleware, tests, custom transports) — use errors.As, which
// also unwraps wrapped errors, to recover the code/message/data:
//
//	resp, err := server.CallTool(ctx, name, args)
//	var toolErr *mcp.ToolError
//	if errors.As(err, &toolErr) {
//	    log.Printf("tool error %d: %s (data=%v)", toolErr.Code, toolErr.Message, toolErr.Data)
//	}
type ToolError struct {
	Code    int
	Message string
	Data    any

	// relayed marks an error a remote server sent for a tools/call this
	// server forwarded: tools/call passes it on as it came.
	relayed bool
}

func (e *ToolError) Error() string {
	return fmt.Sprintf("MCP Error %d: %s", e.Code, e.Message)
}

// NewToolErrorInvalidParams creates an error for invalid or missing parameters
// (ErrorCodeInvalidParams, -32602). The server already checks arguments
// against the tool's inputSchema; use this for a check the schema can't
// express. tools/call reports it as a tool execution error (isError), the
// spec's class for input validation errors.
func NewToolErrorInvalidParams(message string) error {
	return &ToolError{
		Code:    ErrorCodeInvalidParams,
		Message: message,
	}
}

// NewToolErrorInternal creates an error for internal server errors
// (ErrorCodeInternalError, -32603), which tools/call sends as a protocol
// error. A failure the model could act on — an upstream API error, a
// business rule — is better returned as NewToolResponseError.
func NewToolErrorInternal(message string) error {
	return &ToolError{
		Code:    ErrorCodeInternalError,
		Message: message,
	}
}

// NewToolErrorMissingClientCapability is the spec's
// MissingRequiredClientCapabilityError (-32021): the call needs client
// capabilities the request did not declare. A server MUST NOT rely on a
// capability the client has not declared; a handler that needs one checks
// [ClientCapabilitiesFromContext] and returns this error naming the missing
// capabilities (for example "sampling"). tools/call sends it as a JSON-RPC
// error with data.requiredCapabilities set to a ClientCapabilities object
// ({"sampling": {}}), with HTTP 400 on the 2026-07-28 Streamable HTTP wire.
func NewToolErrorMissingClientCapability(capabilities ...string) error {
	required := make(map[string]any, len(capabilities))
	for _, c := range capabilities {
		required[c] = map[string]any{}
	}
	return &ToolError{
		Code:    ErrorCodeMissingRequiredClientCapability,
		Message: "Missing required client capability: " + strings.Join(capabilities, ", "),
		Data:    map[string]any{"requiredCapabilities": required},
	}
}

// NewToolError creates an error with a specific JSON-RPC code. Returned from
// a tool handler, an application code is a tool execution error: tools/call
// reports the message with isError set (see ToolError), and only a server
// error code (-32603, or -32000 to -32099) is sent as a JSON-RPC error.
//
// Pick application-specific codes outside -32768..-32000: 2026-07-28
// reserves -32020..-32099 for the protocol (a Modern client is never sent a
// foreign code from that range — it is remapped to -32603, with the original
// kept in data) and discourages -32000..-32019 for new implementations. The
// data parameter can include additional error details and will be serialized to JSON.
//
// Example:
//
//	return nil, mcp.NewToolError(1001, "Rate limit exceeded", map[string]any{
//	    "retry_after": 60,
//	    "limit": 100,
//	})
func NewToolError(code int, message string, data any) error {
	return &ToolError{
		Code:    code,
		Message: message,
		Data:    data,
	}
}

// isServerErrorCode reports whether code is a JSON-RPC server error: the
// internal error, or the implementation-defined server error range.
func isServerErrorCode(code int) bool {
	return code == ErrorCodeInternalError || (code <= ErrorCodeImplementationErrorStart && code >= ErrorCodeImplementationErrorEnd)
}

// relayedError marks the *ToolError in err, if any, as relayed from a remote
// server's tools/call, so tools/call passes it on as it came. The client
// creates a fresh *ToolError for each response, so marking it is safe.
func relayedError(err error) error {
	var toolErr *ToolError
	if errors.As(err, &toolErr) {
		toolErr.relayed = true
	}
	return err
}
