package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestNewSimpleSSEWriter_WriteEvent(t *testing.T) {
	var buf bytes.Buffer
	flushed := false
	w := NewSimpleSSEWriter(&buf, func() { flushed = true })

	err := w.WriteEvent("tool_start", map[string]string{"a": "b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !flushed {
		t.Error("expected flusher to be called")
	}
	out := buf.String()
	if !strings.HasPrefix(out, ":tool_start:") {
		t.Errorf("output = %q, want prefix :tool_start:", out)
	}
	if !strings.HasSuffix(out, "\n\n") {
		t.Errorf("output = %q, want trailing blank line", out)
	}
	var data map[string]string
	jsonPart := strings.TrimSuffix(strings.TrimPrefix(out, ":tool_start:"), "\n\n")
	if err := json.Unmarshal([]byte(jsonPart), &data); err != nil {
		t.Fatalf("failed to parse embedded JSON: %v", err)
	}
	if data["a"] != "b" {
		t.Errorf("data = %v", data)
	}
}

func TestNewSimpleSSEWriter_NoFlusher(t *testing.T) {
	var buf bytes.Buffer
	w := NewSimpleSSEWriter(&buf, nil)
	if err := w.WriteEvent("e", "d"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// failingWriter always errors on Write, to exercise the error path.
type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestSimpleSSEWriter_WriteEvent_WriteError(t *testing.T) {
	w := NewSimpleSSEWriter(failingWriter{}, nil)
	err := w.WriteEvent("e", "d")
	if err == nil {
		t.Fatal("expected error from failing writer")
	}
}

// unmarshalableData cannot be marshaled to JSON.
type unmarshalableData struct {
	Ch chan int
}

func TestSimpleSSEWriter_WriteEvent_MarshalError(t *testing.T) {
	var buf bytes.Buffer
	w := NewSimpleSSEWriter(&buf, nil)
	err := w.WriteEvent("e", unmarshalableData{Ch: make(chan int)})
	if err == nil {
		t.Fatal("expected marshal error")
	}
}

// recordingWriter records the events it receives, for testing SSEToolHandler.
type recordingWriter struct {
	events []string
	fail   bool
}

func (r *recordingWriter) WriteEvent(eventType string, data any) error {
	if r.fail {
		return errors.New("write failed")
	}
	r.events = append(r.events, eventType)
	return nil
}

func TestNewSSEToolHandler_OnToolCall(t *testing.T) {
	rw := &recordingWriter{}
	h := NewSSEToolHandler(rw, nil)
	err := h.OnToolCall(ToolCall{ID: "1", Function: ToolCallFunction{Name: "f", Arguments: map[string]any{"x": 1}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rw.events) != 1 || rw.events[0] != EventToolStart {
		t.Errorf("events = %v", rw.events)
	}
}

func TestNewSSEToolHandler_OnToolResult(t *testing.T) {
	rw := &recordingWriter{}
	h := NewSSEToolHandler(rw, nil)
	err := h.OnToolResult("1", "f", "the result")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rw.events) != 1 || rw.events[0] != EventToolEnd {
		t.Errorf("events = %v", rw.events)
	}
}

func TestSSEToolHandler_WriteFailure_CallsErrorLogger(t *testing.T) {
	rw := &recordingWriter{fail: true}
	var loggedErr error
	var loggedEventType, loggedToolName string
	h := NewSSEToolHandler(rw, func(err error, eventType, toolName string) {
		loggedErr = err
		loggedEventType = eventType
		loggedToolName = toolName
	})

	err := h.OnToolCall(ToolCall{ID: "1", Function: ToolCallFunction{Name: "f"}})
	// OnToolCall always returns nil, errors are only logged.
	if err != nil {
		t.Errorf("OnToolCall should return nil even on write failure, got %v", err)
	}
	if loggedErr == nil {
		t.Fatal("expected errorLogger to be called")
	}
	if loggedEventType != EventToolStart || loggedToolName != "f" {
		t.Errorf("loggedEventType=%q loggedToolName=%q", loggedEventType, loggedToolName)
	}

	err = h.OnToolResult("1", "f", "result")
	if err != nil {
		t.Errorf("OnToolResult should return nil even on write failure, got %v", err)
	}
}

func TestSSEToolHandler_WriteFailure_NilLogger(t *testing.T) {
	rw := &recordingWriter{fail: true}
	h := NewSSEToolHandler(rw, nil)
	// Should not panic with nil errorLogger.
	if err := h.OnToolCall(ToolCall{ID: "1", Function: ToolCallFunction{Name: "f"}}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := h.OnToolResult("1", "f", "r"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
