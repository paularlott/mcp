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
	if err := host.RegisterRemoteServer(ts.URL, "ns", NewBearerTokenAuth("t")); err != nil {
		t.Fatalf("register remote: %v", err)
	}

	// List should include namespaced tool
	tools := host.ListTools()
	found := false
	for _, tl := range tools {
		if tl.Name == "ns/rt" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected namespaced tool in list, got %+v", tools)
	}

	// Call through host with namespace
	resp, err := host.CallTool(context.Background(), "ns/rt", map[string]any{"x": "y"})
	if err != nil {
		t.Fatalf("call namespaced: %v", err)
	}
	if len(resp.Content) == 0 || resp.Content[0].Text != "r:y" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}
