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
	if err := host.RefreshTools(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	tools := host.ListToolsWithContext(context.Background())
	foundGood := false
	foundBad := false
	for _, tl := range tools {
		if tl.Name == "g__ok" {
			foundGood = true
		}
		if tl.Name == "b__whatever" {
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

// TestRefreshTools_RefreshesDiscoverableRemoteTools verifies that when a
// discoverable remote server changes its tool set, RefreshTools updates the
// internal search registry so tool_search reflects the new state.
func TestRefreshTools_RefreshesDiscoverableRemoteTools(t *testing.T) {
	remote := NewServer("remote", "1")
	remote.RegisterTool(NewTool("old_tool", "the old tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("old"), nil
	})
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer ts.Close()

	host := NewServer("host", "1")
	client := NewClient(ts.URL, NewBearerTokenAuth("t"), "r")
	if err := host.RegisterRemoteServerDiscoverable(client); err != nil {
		t.Fatalf("register discoverable: %v", err)
	}

	// Initially the registry knows about r__old_tool only.
	registered := host.internalRegistry.GetRegisteredTools()
	if !containsToolNamed(registered, "r__old_tool") {
		t.Fatalf("expected r__old_tool registered, got %+v", registered)
	}

	// Change the remote's tool set: remove old_tool, add new_tool.
	host.UnregisterRemoteServer(client) // ensures a clean rebuild path is not used; re-add below
	if err := host.RegisterRemoteServerDiscoverable(client); err != nil {
		t.Fatalf("re-register discoverable: %v", err)
	}
	if !remote.UnregisterTool("old_tool") {
		t.Fatal("expected to remove old_tool from remote")
	}
	remote.RegisterTool(NewTool("new_tool", "the new tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("new"), nil
	})

	// Refresh should pick up the change.
	if err := host.RefreshTools(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	registered = host.internalRegistry.GetRegisteredTools()
	if containsToolNamed(registered, "r__old_tool") {
		t.Fatalf("expected r__old_tool to be gone after refresh, got %+v", registered)
	}
	if !containsToolNamed(registered, "r__new_tool") {
		t.Fatalf("expected r__new_tool present after refresh, got %+v", registered)
	}

	// And tool_search should surface the new tool.
	resp, err := host.CallTool(context.Background(), ToolSearchName, map[string]any{"query": "new tool", "max_results": 10})
	if err != nil {
		t.Fatalf("tool_search: %v", err)
	}
	if resp == nil || len(resp.Content) == 0 {
		t.Fatalf("expected search results, got %+v", resp)
	}
}

func containsToolNamed(tools []MCPTool, name string) bool {
	for _, tl := range tools {
		if tl.Name == name {
			return true
		}
	}
	return false
}
