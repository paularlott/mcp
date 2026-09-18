package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetToolProviders_NilContext and friends cover the nil-context guard
// clauses in the small context-accessor helpers, which nothing else reaches
// (every other test passes a real context.Context).
func TestGetToolProviders_NilContext(t *testing.T) {
	if got := GetToolProviders(nil); got != nil {
		t.Errorf("GetToolProviders(nil) = %v, want nil", got)
	}
}

func TestGetShowAllTools_NilContext(t *testing.T) {
	if got := GetShowAllTools(nil); got != false {
		t.Errorf("GetShowAllTools(nil) = %v, want false", got)
	}
}

func TestGetRequestMemo_NilContext(t *testing.T) {
	if got := getRequestMemo(nil); got != nil {
		t.Errorf("getRequestMemo(nil) = %v, want nil", got)
	}
}

// TestWithShowAllFromRequest covers the convenience combinator end to end:
// providers get attached, and show-all is set based on the request only when
// the header/query says so. No existing test calls this function.
func TestWithShowAllFromRequest(t *testing.T) {
	provider := &fakeToolProvider{tools: []MCPTool{{Name: "p", Visibility: ToolVisibilityNative}}}

	// show_all=false, with a provider attached.
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	ctx := WithShowAllFromRequest(context.Background(), req, provider)
	if GetShowAllTools(ctx) {
		t.Error("expected show-all to be false without header/query")
	}
	if got := GetToolProviders(ctx); len(got) != 1 {
		t.Errorf("expected the provider to be attached, got %v", got)
	}

	// show_all=true via header, no providers passed.
	req2 := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req2.Header.Set(ShowAllHeader, "true")
	ctx2 := WithShowAllFromRequest(context.Background(), req2)
	if !GetShowAllTools(ctx2) {
		t.Error("expected show-all to be true via header")
	}
	if got := GetToolProviders(ctx2); got != nil {
		t.Errorf("expected no providers attached, got %v", got)
	}
}

// TestRegisterRemoteServerDiscoverable covers registering a remote server as
// discoverable: its tools are absent from tools/list but reachable via
// tool_search and CallTool, unlike RegisterRemoteServer's native path which
// every other remote_server_test.go test exercises.
func TestRegisterRemoteServerDiscoverable(t *testing.T) {
	remote := NewServer("remote", "1")
	remote.RegisterTool(NewTool("remote_tool", "a remote tool", String("x", "x", Required())), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		v, _ := req.String("x")
		return NewToolResponseText("r:" + v), nil
	})
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer ts.Close()

	host := NewServer("host", "1")
	client := NewClient(ts.URL, nil, "ns")
	if err := host.RegisterRemoteServerDiscoverable(client); err != nil {
		t.Fatalf("RegisterRemoteServerDiscoverable: %v", err)
	}

	// Not in tools/list.
	tools := host.ListToolsWithContext(context.Background())
	for _, tl := range tools {
		if tl.Name == "ns__remote_tool" {
			t.Errorf("ns__remote_tool should not appear in tools/list, got %+v", tools)
		}
	}

	// Reachable via CallTool directly (namespace-prefix fallback path).
	resp, err := host.CallTool(context.Background(), "ns__remote_tool", map[string]any{"x": "y"})
	if err != nil {
		t.Fatalf("CallTool(ns__remote_tool): %v", err)
	}
	if resp.Content[0].Text != "r:y" {
		t.Errorf("resp = %+v, want r:y", resp)
	}

	// Reachable via tool_search.
	searchResp, err := host.CallTool(context.Background(), "tool_search", map[string]any{"query": "remote_tool"})
	if err != nil {
		t.Fatalf("tool_search: %v", err)
	}
	if len(searchResp.Content) == 0 {
		t.Fatalf("tool_search returned no content")
	}
}

// TestRegisterRemoteServerDiscoverable_WithRemoteSearch covers the
// remoteSearch option combined with discoverable visibility.
func TestRegisterRemoteServerDiscoverable_WithRemoteSearch(t *testing.T) {
	remote := NewServer("remote", "1")
	remote.RegisterTool(NewTool("hidden_tool", "hidden").Discoverable("hidden"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("h"), nil
	})
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer ts.Close()

	host := NewServer("host", "1")
	client := NewClient(ts.URL, nil, "ns")
	if err := host.RegisterRemoteServerDiscoverable(client, WithRemoteSearch()); err != nil {
		t.Fatalf("RegisterRemoteServerDiscoverable: %v", err)
	}

	tools := host.ListToolsWithContext(context.Background())
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Name] = true
	}
	if !names[ToolSearchName] {
		t.Errorf("expected tool_search to be exposed, got %+v", tools)
	}
}
