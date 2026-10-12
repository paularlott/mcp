package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A proxy that registers one remote without a namespace (as a stdio proxy
// for a single server does) can run that remote's discoverable tools through
// execute_tool: they keep their names, are absent from the proxy's tool
// list, and are found by tool_search.
func TestUnprefixedRemoteDiscoverableToolViaExecuteTool(t *testing.T) {
	remote := NewServer("remote", "1.0.0")
	remote.RegisterTool(NewTool("visible", "a listed tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("visible ran"), nil
	})
	remote.RegisterTool(NewTool("hidden_lease", "check out a member").Discoverable("lease"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("hidden ran"), nil
	})
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer ts.Close()

	proxy := NewServer("proxy", "1.0.0")
	client := NewClient(ts.URL, nil, "")
	defer client.Close()
	if err := proxy.RegisterRemoteServer(client); err != nil {
		t.Fatalf("RegisterRemoteServer: %v", err)
	}

	resp, err := proxy.CallTool(context.Background(), ExecuteToolName, map[string]any{"name": "hidden_lease", "arguments": map[string]any{}})
	if err != nil {
		t.Fatalf("execute_tool: %v", err)
	}
	if got := responseText(resp); !strings.Contains(got, "hidden ran") {
		t.Fatalf("execute_tool returned %q, want the remote tool's output", got)
	}

	// A name nobody has still reports as not found
	resp, err = proxy.CallTool(context.Background(), ExecuteToolName, map[string]any{"name": "no_such_tool"})
	if err != nil {
		t.Fatalf("execute_tool unknown: %v", err)
	}
	if got := responseText(resp); !strings.Contains(strings.ToLower(got), "not found") {
		t.Fatalf("unknown tool returned %q", got)
	}
}

func responseText(resp *ToolResponse) string {
	var b strings.Builder
	for _, c := range resp.Content {
		b.WriteString(c.Text)
	}
	return b.String()
}

// With remote search, an unprefixed remote's tools found both in the proxy's
// list and by the remote's search are returned once.
func TestUnprefixedRemoteSearchNoDuplicates(t *testing.T) {
	remote := NewServer("remote", "1.0.0")
	remote.RegisterTool(NewTool("create_pool", "create a pool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("ok"), nil
	})
	remote.RegisterTool(NewTool("acquire_pool_member", "acquire a pool member").Discoverable("pool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("ok"), nil
	})
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer ts.Close()

	proxy := NewServer("proxy", "1.0.0")
	client := NewClient(ts.URL, nil, "")
	defer client.Close()
	if err := proxy.RegisterRemoteServer(client, WithRemoteSearch()); err != nil {
		t.Fatalf("RegisterRemoteServer: %v", err)
	}

	resp, err := proxy.CallTool(context.Background(), ToolSearchName, map[string]any{"query": "pool"})
	if err != nil {
		t.Fatalf("tool_search: %v", err)
	}
	text := responseText(resp)
	if strings.Count(text, `"create_pool"`) != 1 || !strings.Contains(text, `"acquire_pool_member"`) {
		t.Fatalf("tool_search returned %s", text)
	}
}
