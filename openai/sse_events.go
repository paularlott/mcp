package openai

import (
	"encoding/json"
	"fmt"
	"io"
)

// SSE event types for tool status notifications
// Standard OpenAI clients ignore these; custom clients can use them for UI feedback
const (
	// EventToolStart is sent when a tool execution begins
	EventToolStart = "tool_start"
	// EventToolEnd is sent when a tool execution completes
	EventToolEnd = "tool_end"
)

// ToolStatusEvent represents a tool execution status for SSE streaming.
// This is sent as an SSE comment (prefixed with ":") so standard SSE clients ignore it,
// but custom clients can parse it to show tool execution progress.
type ToolStatusEvent struct {
	ToolCallID string         `json:"tool_call_id"`
	ToolName   string         `json:"tool_name"`
	Status     string         `json:"status"` // "running" or "complete"
	Arguments  map[string]any `json:"arguments,omitempty"`
	Result     string         `json:"result,omitempty"`
	Error      string         `json:"error,omitempty"`
}

// SSEEventWriter is an interface for writing SSE events.
// This allows integration with various HTTP frameworks' streaming implementations.
type SSEEventWriter interface {
	// WriteEvent writes an SSE comment event (prefixed with ":")
	// The event type and data are formatted as ":eventType:jsonData\n\n"
	WriteEvent(eventType string, data any) error
}

// SSEToolHandler implements ToolHandler to send tool events via SSE streaming.
// It wraps an SSEEventWriter and sends tool_start/tool_end events as SSE comments.
//
// Usage pattern:
//  1. Call OnToolCall() BEFORE executing the tool (sends tool_start with "running" status)
//  2. Execute the tool
//  3. Call OnToolResult() AFTER execution completes (sends tool_end with "complete" status and result)
//
// Example SSE output:
//
// :tool_start:{"tool_call_id":"call_abc123","tool_name":"search","status":"running"}
//
// :tool_end:{"tool_call_id":"call_abc123","tool_name":"search","status":"complete","result":"..."}
type SSEToolHandler struct {
	writer      SSEEventWriter
	errorLogger func(err error, eventType, toolName string)
}

// NewSSEToolHandler creates a new SSEToolHandler that sends tool events to the given writer.
// The optional errorLogger is called when write failures occur (errors are logged but not returned
// since tool events are just status notifications and shouldn't block tool execution).
func NewSSEToolHandler(writer SSEEventWriter, errorLogger func(err error, eventType, toolName string)) *SSEToolHandler {
	return &SSEToolHandler{
		writer:      writer,
		errorLogger: errorLogger,
	}
}

// OnToolCall sends a tool_start event when a tool execution begins.
// This should be called BEFORE executing the tool.
// Write failures are logged but not returned as errors since they're just status notifications.
func (h *SSEToolHandler) OnToolCall(toolCall ToolCall) error {
	event := ToolStatusEvent{
		ToolCallID: toolCall.ID,
		ToolName:   toolCall.Function.Name,
		Status:     "running",
		Arguments:  toolCall.Function.Arguments,
	}
	if err := h.writer.WriteEvent(EventToolStart, event); err != nil {
		if h.errorLogger != nil {
			h.errorLogger(err, EventToolStart, toolCall.Function.Name)
		}
	}
	return nil
}

// OnToolResult sends a tool_end event when a tool execution completes.
// This should be called AFTER the tool has finished executing.
// The result parameter contains the tool's output, which is included in the event
// so clients can display tool results in the UI.
// Write failures are logged but not returned as errors since they're just status notifications.
func (h *SSEToolHandler) OnToolResult(toolCallID, toolName, result string) error {
	event := ToolStatusEvent{
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Status:     "complete",
		Result:     result,
	}
	if err := h.writer.WriteEvent(EventToolEnd, event); err != nil {
		if h.errorLogger != nil {
			h.errorLogger(err, EventToolEnd, toolName)
		}
	}
	return nil
}

// Ensure SSEToolHandler implements ToolHandler
var _ ToolHandler = (*SSEToolHandler)(nil)

// SimpleSSEWriter is a basic implementation of SSEEventWriter that writes to an io.Writer.
// For production use, you may want to implement your own SSEEventWriter with proper
// flushing, error handling, and client disconnect detection.
type SimpleSSEWriter struct {
	w       io.Writer
	flusher func() // optional flush function
}

// NewSimpleSSEWriter creates a SimpleSSEWriter that writes to the given io.Writer.
// If the writer implements http.Flusher, pass a flush function to flush after each write.
func NewSimpleSSEWriter(w io.Writer, flusher func()) *SimpleSSEWriter {
	return &SimpleSSEWriter{
		w:       w,
		flusher: flusher,
	}
}

// WriteEvent writes an SSE comment event in the format ":eventType:jsonData\n\n"
func (s *SimpleSSEWriter) WriteEvent(eventType string, data any) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal event data: %w", err)
	}

	// Write as SSE comment: :eventType:jsonData
	_, err = fmt.Fprintf(s.w, ":%s:%s\n\n", eventType, jsonData)
	if err != nil {
		return fmt.Errorf("failed to write SSE event: %w", err)
	}

	if s.flusher != nil {
		s.flusher()
	}

	return nil
}

// Ensure SimpleSSEWriter implements SSEEventWriter
var _ SSEEventWriter = (*SimpleSSEWriter)(nil)
