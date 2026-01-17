package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUnknownTool_HTTPEnvelope(t *testing.T) {
	s := NewServer("s", "1")
	body := MCPRequest{JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: ToolCallParams{Name: "nope"}}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	http.HandlerFunc(s.HandleRequest).ServeHTTP(rr, req)
	var r MCPResponse
	_ = json.NewDecoder(rr.Body).Decode(&r)
	if r.Error == nil || r.Error.Code != ErrorCodeInternalError {
		t.Fatalf("expected internal error envelope for unknown tool, got %+v", r.Error)
	}
}

func TestUnknownTool_DirectAPI(t *testing.T) {
	s := NewServer("s", "1")
	if _, err := s.CallTool(context.TODO(), "nope", nil); err == nil || err != ErrUnknownTool {
		t.Fatalf("expected ErrUnknownTool, got %v", err)
	}
}
