package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterRemoteServerAndCall(t *testing.T) {
	// remote server with one tool
	remote := NewServer("remote", "1")
	remote.RegisterTool(NewTool("rt", "remote tool", String("x", "x", Required())), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		v, _ := req.String("x")
		return NewToolResponseText("r:" + v), nil
	})
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer ts.Close()

	// host server registers remote under namespace
	host := NewServer("host", "1")
	client := NewClient(ts.URL, NewBearerTokenAuth("t"), "ns")
	if err := host.RegisterRemoteServer(client); err != nil {
		t.Fatalf("register remote: %v", err)
	}

	// List should include namespaced tool
	tools := host.ListTools()
	found := false
	for _, tl := range tools {
		if tl.Name == "ns.rt" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected namespaced tool in list, got %+v", tools)
	}

	// Call through host with namespace
	resp, err := host.CallTool(context.Background(), "ns.rt", map[string]any{"x": "y"})
	if err != nil {
		t.Fatalf("call namespaced: %v", err)
	}
	if len(resp.Content) == 0 || resp.Content[0].Text != "r:y" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestUnregisterRemoteServer(t *testing.T) {
	remote := NewServer("remote", "1")
	remote.RegisterTool(NewTool("tool-x", "tool x"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("x"), nil
	})
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer ts.Close()

	host := NewServer("host", "1")
	client := NewClient(ts.URL, NewBearerTokenAuth("t"), "ns")
	if err := host.RegisterRemoteServer(client); err != nil {
		t.Fatalf("register: %v", err)
	}
	if tools := host.ListTools(); len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %+v", tools)
	}

	host.UnregisterRemoteServer(client)

	if tools := host.ListTools(); len(tools) != 0 {
		t.Fatalf("expected 0 tools after unregister, got %+v", tools)
	}
	if _, err := host.CallTool(context.Background(), "ns.tool-x", nil); err == nil {
		t.Fatal("expected error calling unregistered tool")
	}
}

func TestReplaceRemoteServers(t *testing.T) {
	remoteA := NewServer("remoteA", "1")
	remoteA.RegisterTool(NewTool("tool-a", "tool a"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("a"), nil
	})
	tsA := httptest.NewServer(http.HandlerFunc(remoteA.HandleRequest))
	defer tsA.Close()

	remoteB := NewServer("remoteB", "1")
	remoteB.RegisterTool(NewTool("tool-b", "tool b"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("b"), nil
	})
	tsB := httptest.NewServer(http.HandlerFunc(remoteB.HandleRequest))
	defer tsB.Close()

	host := NewServer("host", "1")

	// Register server A initially
	if err := host.RegisterRemoteServer(NewClient(tsA.URL, NewBearerTokenAuth("t"), "a")); err != nil {
		t.Fatalf("initial register: %v", err)
	}
	if tools := host.ListTools(); len(tools) != 1 || tools[0].Name != "a.tool-a" {
		t.Fatalf("expected a.tool-a, got %+v", tools)
	}

	// Replace with server B only
	if err := host.ReplaceRemoteServers([]RemoteServerEntry{
		{Client: NewClient(tsB.URL, NewBearerTokenAuth("t"), "b"), Visibility: ToolVisibilityNative},
	}); err != nil {
		t.Fatalf("replace: %v", err)
	}

	tools := host.ListTools()
	if len(tools) != 1 || tools[0].Name != "b.tool-b" {
		t.Fatalf("expected only b.tool-b after replace, got %+v", tools)
	}

	// Old tool should no longer be callable
	if _, err := host.CallTool(context.Background(), "a.tool-a", nil); err == nil {
		t.Fatal("expected error calling removed tool")
	}

	// New tool should be callable
	resp, err := host.CallTool(context.Background(), "b.tool-b", nil)
	if err != nil {
		t.Fatalf("call b.tool-b: %v", err)
	}
	if len(resp.Content) == 0 || resp.Content[0].Text != "b" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}
