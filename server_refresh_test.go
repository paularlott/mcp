package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// failing remote implements minimal MCP but always errors on tools/list
func newFailingRemote() *httptest.Server {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpc MCPRequest
		_ = json.NewDecoder(r.Body).Decode(&rpc)
		w.Header().Set("Content-Type", "application/json")
		switch rpc.Method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: rpc.ID, Result: map[string]any{
				"protocolVersion": MCPProtocolVersionLatest,
				"capabilities":    capabilities{Tools: map[string]any{}},
				"serverInfo":      serverInfo{Name: "bad", Version: "1"},
			}})
		case "tools/list":
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "boom"})
		default:
			_ = json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: rpc.ID, Result: map[string]any{}})
		}
	})
	return httptest.NewServer(h)
}

func TestRefreshTools_SkipsFailingRemote(t *testing.T) {
	good := NewServer("good", "1")
	good.RegisterTool(NewTool("ok", "desc"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("ok"), nil
	})
	goodTS := httptest.NewServer(http.HandlerFunc(good.HandleRequest))
	defer goodTS.Close()

	badTS := newFailingRemote()
	defer badTS.Close()

	host := NewServer("host", "1")
	goodClient := NewClient(goodTS.URL, NewBearerTokenAuth("t"), "g")
	if err := host.RegisterRemoteServer(goodClient); err != nil {
		t.Fatalf("reg good: %v", err)
	}
	badClient := NewClient(badTS.URL, NewBearerTokenAuth("t"), "b")
	if err := host.RegisterRemoteServer(badClient); err != nil {
		t.Fatalf("reg bad: %v", err)
	}

	// Force refresh; bad server should be skipped silently
	if err := host.RefreshTools(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tools := host.ListTools()
	foundGood := false
	foundBad := false
	for _, tl := range tools {
		if tl.Name == "g/ok" {
			foundGood = true
		}
		if tl.Name == "b/whatever" {
			foundBad = true
		}
	}
	if !foundGood {
		t.Fatalf("expected good tool present, tools=%+v", tools)
	}
	if foundBad {
		t.Fatalf("did not expect bad namespace tool, tools=%+v", tools)
	}
}
