package openai

import (
	"fmt"
	"net/http"
)

// APIError represents an error returned by the OpenAI API.
type APIError struct {
	StatusCode int    `json:"-"`
	Type       string `json:"type"`
	Message    string `json:"message"`
	Param      string `json:"param,omitempty"`
	Code       string `json:"code,omitempty"`
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("openai: %s (%s): %s", e.Type, e.Code, e.Message)
	}
	return fmt.Sprintf("openai: %s: %s", e.Type, e.Message)
}

// IsRateLimit returns true if this is a rate limit error (429).
func (e *APIError) IsRateLimit() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.Code == "rate_limit_exceeded"
}

// IsTokenLimit returns true if this is a token limit error.
func (e *APIError) IsTokenLimit() bool {
	return e.Code == "context_length_exceeded" || e.Code == "max_tokens_exceeded"
}

// IsInvalidRequest returns true if this is an invalid request error (400).
func (e *APIError) IsInvalidRequest() bool {
	return e.StatusCode == http.StatusBadRequest || e.Type == "invalid_request_error"
}

// IsAuthentication returns true if this is an authentication error (401).
func (e *APIError) IsAuthentication() bool {
	return e.StatusCode == http.StatusUnauthorized || e.Type == "authentication_error"
}

// IsPermission returns true if this is a permission error (403).
func (e *APIError) IsPermission() bool {
	return e.StatusCode == http.StatusForbidden || e.Type == "permission_error"
}

// IsNotFound returns true if this is a not found error (404).
func (e *APIError) IsNotFound() bool {
	return e.StatusCode == http.StatusNotFound || e.Type == "not_found_error"
}

// IsServerError returns true if this is a server error (5xx).
func (e *APIError) IsServerError() bool {
	return e.StatusCode >= 500 || e.Type == "server_error"
}

// IsRetryable returns true if this error is likely to succeed on retry.
func (e *APIError) IsRetryable() bool {
	return e.IsRateLimit() || e.IsServerError() || e.StatusCode == http.StatusServiceUnavailable
}

// Common error constructors

// NewRateLimitError creates a rate limit error.
func NewRateLimitError(message string) *APIError {
	return &APIError{
		StatusCode: http.StatusTooManyRequests,
		Type:       "rate_limit_error",
		Code:       "rate_limit_exceeded",
		Message:    message,
	}
}

// NewTokenLimitError creates a token limit error.
func NewTokenLimitError(message string) *APIError {
	return &APIError{
		StatusCode: http.StatusBadRequest,
		Type:       "invalid_request_error",
		Code:       "context_length_exceeded",
		Message:    message,
	}
}

// NewInvalidRequestError creates an invalid request error.
func NewInvalidRequestError(message string) *APIError {
	return &APIError{
		StatusCode: http.StatusBadRequest,
		Type:       "invalid_request_error",
		Message:    message,
	}
}

// NewAuthenticationError creates an authentication error.
func NewAuthenticationError(message string) *APIError {
	return &APIError{
		StatusCode: http.StatusUnauthorized,
		Type:       "authentication_error",
		Message:    message,
	}
}

// NewServerError creates a server error.
func NewServerError(message string) *APIError {
	return &APIError{
		StatusCode: http.StatusInternalServerError,
		Type:       "server_error",
		Message:    message,
	}
}

// ErrorResponse represents the error response structure from OpenAI API.
type ErrorResponse struct {
	Error *APIError `json:"error"`
}

// MaxToolIterationsError is returned when the maximum number of tool call
// iterations is reached without completing the conversation.
type MaxToolIterationsError struct {
	Iterations int
}

func (e *MaxToolIterationsError) Error() string {
	return fmt.Sprintf("maximum tool call iterations (%d) reached", e.Iterations)
}

// NewMaxToolIterationsError creates a new MaxToolIterationsError.
func NewMaxToolIterationsError(iterations int) *MaxToolIterationsError {
	return &MaxToolIterationsError{Iterations: iterations}
}

// ToolExecutionError is returned when a tool call fails.
type ToolExecutionError struct {
	ToolName string
	ToolID   string
	Err      error
}

func (e *ToolExecutionError) Error() string {
	return fmt.Sprintf("tool %q (id: %s) execution failed: %v", e.ToolName, e.ToolID, e.Err)
}

func (e *ToolExecutionError) Unwrap() error {
	return e.Err
}

// NewToolExecutionError creates a new ToolExecutionError.
func NewToolExecutionError(toolName, toolID string, err error) *ToolExecutionError {
	return &ToolExecutionError{
		ToolName: toolName,
		ToolID:   toolID,
		Err:      err,
	}
}

// StreamError is returned when an error occurs during streaming.
type StreamError struct {
	Err error
}

func (e *StreamError) Error() string {
	return fmt.Sprintf("streaming error: %v", e.Err)
}

func (e *StreamError) Unwrap() error {
	return e.Err
}

// NewStreamError creates a new StreamError.
func NewStreamError(err error) *StreamError {
	return &StreamError{Err: err}
}
