package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInitialize_InvalidParamsShape(t *testing.T) {
	s := NewServer("s", "1")
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":"oops"}`)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	http.HandlerFunc(s.HandleRequest).ServeHTTP(rr, req)
	var r MCPResponse
	_ = json.NewDecoder(rr.Body).Decode(&r)
	if r.Error == nil || r.Error.Code != ErrorCodeInvalidParams {
		t.Fatalf("expected invalid params, got %+v", r.Error)
	}
}

func TestToolsCall_InvalidParamsShape(t *testing.T) {
	s := NewServer("s", "1")
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":"oops"}`)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	http.HandlerFunc(s.HandleRequest).ServeHTTP(rr, req)
	var r MCPResponse
	_ = json.NewDecoder(rr.Body).Decode(&r)
	if r.Error == nil || r.Error.Code != ErrorCodeInvalidParams {
		t.Fatalf("expected invalid params, got %+v", r.Error)
	}
}
