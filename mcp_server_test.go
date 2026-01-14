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

// helper to perform JSON-RPC request against handler
func doRPC(t *testing.T, h http.HandlerFunc, body interface{}, headers map[string]string) (*http.Response, MCPResponse) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	resp := rr.Result()
	data, _ := io.ReadAll(resp.Body)
	var rpc MCPResponse
	_ = json.Unmarshal(data, &rpc)
	return resp, rpc
}

func TestInitialize_DefaultsAndVersion(t *testing.T) {
	s := NewServer("test", "0.1.0")
	handler := http.HandlerFunc(s.HandleRequest)

	// initialize without explicit version uses latest
	_, rpc := doRPC(t, handler, MCPRequest{JSONRPC: "2.0", ID: 1, Method: "initialize", Params: map[string]any{
		"capabilities": map[string]any{},
		"clientInfo":   map[string]any{"name": "t", "version": "1"},
	}}, nil)
	if rpc.Error != nil {
		t.Fatalf("unexpected error: %+v", rpc.Error)
	}
	res := rpc.Result.(map[string]any)
	if res["protocolVersion"] != MCPProtocolVersionLatest {
		t.Fatalf("expected latest version, got %v", res["protocolVersion"])
	}

	// initialize with supported older version
	_, rpc = doRPC(t, handler, MCPRequest{JSONRPC: "2.0", ID: 2, Method: "initialize", Params: map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "t", "version": "1"},
	}}, nil)
	if rpc.Error != nil {
		t.Fatalf("unexpected error: %+v", rpc.Error)
	}

	// initialize with unsupported version
	_, rpc = doRPC(t, handler, MCPRequest{JSONRPC: "2.0", ID: 3, Method: "initialize", Params: map[string]any{
		"protocolVersion": "1900-01-01",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "t", "version": "1"},
	}}, nil)
	if rpc.Error == nil || rpc.Error.Code != ErrorCodeInvalidParams {
		t.Fatalf("expected invalid params error, got %+v", rpc.Error)
	}
}

func TestToolsListAndCall_Local(t *testing.T) {
	s := NewServer("test", "0.1.0")
	s.RegisterTool(
		NewTool("echo", "echo",
			String("msg", "message", Required()),
		),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			v, _ := req.String("msg")
			return NewToolResponseText(v), nil
		},
	)
	handler := http.HandlerFunc(s.HandleRequest)

	// list tools
	_, rpc := doRPC(t, handler, MCPRequest{JSONRPC: "2.0", ID: 1, Method: "tools/list"}, nil)
	if rpc.Error != nil {
		t.Fatalf("list tools error: %+v", rpc.Error)
	}
	res := rpc.Result.(map[string]any)
	tools := res["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	// call tool
	_, rpc = doRPC(t, handler, MCPRequest{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: ToolCallParams{Name: "echo", Arguments: map[string]any{"msg": "hi"}}}, nil)
	if rpc.Error != nil {
		t.Fatalf("call tool error: %+v", rpc.Error)
	}
	var result ToolResult
	rb, _ := json.Marshal(rpc.Result)
	_ = json.Unmarshal(rb, &result)
	if len(result.Content) != 1 || result.Content[0].Text != "hi" {
		t.Fatalf("unexpected tool result: %+v", result)
	}
}

func TestHandleRequest_BadInputs(t *testing.T) {
	s := NewServer("test", "0.1.0")
	handler := http.HandlerFunc(s.HandleRequest)

	// wrong method
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}

	// wrong content-type
	req = httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(`{}`)))
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d", rr.Code)
	}

	// invalid json
	req = httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte("not-json")))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK { // JSON-RPC error should still be 200
		t.Fatalf("expected 200 (json-rpc envelope), got %d", rr.Code)
	}
}

func TestProtocolVersionHeader_WhitespaceTrimming(t *testing.T) {
	s := NewServer("test", "0.1.0")
	handler := http.HandlerFunc(s.HandleRequest)

	// Protocol version with leading/trailing whitespace should be accepted
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", "  2024-11-05  ") // whitespace padded
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for whitespace-padded version, got %d: %s", rr.Code, rr.Body.String())
	}
}
