package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORSPreflight(t *testing.T) {
	s := NewServer("s", "1")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	http.HandlerFunc(s.HandleRequest).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	h := rr.Result().Header
	if h.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("no ACAO header")
	}
	if h.Get("Access-Control-Allow-Methods") == "" {
		t.Fatal("no methods header")
	}
}

func TestPing(t *testing.T) {
	s := NewServer("s", "1")
	body := MCPRequest{JSONRPC: "2.0", ID: 1, Method: "ping"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	http.HandlerFunc(s.HandleRequest).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var rpc MCPResponse
	_ = json.NewDecoder(rr.Body).Decode(&rpc)
	if rpc.Error != nil {
		t.Fatalf("unexpected error: %+v", rpc.Error)
	}
}

func TestInvalidJSONRPCVersion(t *testing.T) {
	s := NewServer("s", "1")
	payload := map[string]any{"jsonrpc": "1.0", "id": 1, "method": "ping"}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	http.HandlerFunc(s.HandleRequest).ServeHTTP(rr, req)
	var rpc MCPResponse
	_ = json.NewDecoder(rr.Body).Decode(&rpc)
	if rpc.Error == nil || rpc.Error.Code != ErrorCodeInvalidRequest {
		t.Fatalf("expected invalid request error, got %+v", rpc.Error)
	}
}

func TestUnknownMethod(t *testing.T) {
	s := NewServer("s", "1")
	body := MCPRequest{JSONRPC: "2.0", ID: 1, Method: "does/not/exist"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	http.HandlerFunc(s.HandleRequest).ServeHTTP(rr, req)
	var rpc MCPResponse
	_ = json.NewDecoder(rr.Body).Decode(&rpc)
	if rpc.Error == nil || rpc.Error.Code != ErrorCodeMethodNotFound {
		t.Fatalf("expected method not found, got %+v", rpc.Error)
	}
}

func TestToolErrorMapping(t *testing.T) {
	s := NewServer("s", "1")
	s.RegisterTool(NewTool("fail", "", String("x", "", Required())), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return nil, NewToolErrorInvalidParams("bad")
	})
	body := MCPRequest{JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: ToolCallParams{Name: "fail", Arguments: map[string]any{"x": "y"}}}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	http.HandlerFunc(s.HandleRequest).ServeHTTP(rr, req)
	var rpc MCPResponse
	_ = json.NewDecoder(rr.Body).Decode(&rpc)
	if rpc.Error == nil || rpc.Error.Code != ErrorCodeInvalidParams || rpc.Error.Message == "" {
		t.Fatalf("expected mapped tool error, got %+v", rpc.Error)
	}
}

func TestInstructionsInInitialize(t *testing.T) {
	s := NewServer("s", "1")
	s.SetInstructions("please do x")
	body := MCPRequest{JSONRPC: "2.0", ID: 1, Method: "initialize", Params: map[string]any{
		"capabilities": map[string]any{},
		"clientInfo":   map[string]any{"name": "n", "version": "v"},
	}}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	http.HandlerFunc(s.HandleRequest).ServeHTTP(rr, req)
	var rpc MCPResponse
	_ = json.NewDecoder(rr.Body).Decode(&rpc)
	res := rpc.Result.(map[string]any)
	if res["instructions"] != "please do x" {
		t.Fatalf("instructions missing: %+v", res)
	}
}

func TestMissingIDDefaultsToEmpty(t *testing.T) {
	s := NewServer("s", "1")
	// body without id
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  "ping",
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	http.HandlerFunc(s.HandleRequest).ServeHTTP(rr, req)
	// read raw json to assert id field is present and empty string
	data, _ := io.ReadAll(rr.Body)
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	if _, ok := out["id"]; !ok {
		t.Fatalf("id not present")
	}
	if out["id"] != "" {
		t.Fatalf("expected empty id, got %v", out["id"])
	}
}
