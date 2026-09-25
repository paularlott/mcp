package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ToolIsApp reads app-ness off _meta.ui in both shapes it occurs in: the
// map a wire-deserialized tool carries, and the typed UIToolMeta a locally
// built tool carries.
func TestToolIsApp(t *testing.T) {
	if ToolIsApp(MCPTool{}) {
		t.Fatal("no _meta.ui: not an app")
	}
	if ToolIsApp(MCPTool{Meta: map[string]any{"ui": map[string]any{"visibility": []any{"model"}}}}) {
		t.Fatal("ui without resourceUri: not an app")
	}
	if !ToolIsApp(MCPTool{Meta: map[string]any{"ui": map[string]any{"resourceUri": "ui://x/y.html"}}}) {
		t.Fatal("wire shape with resourceUri: an app")
	}
	if !ToolIsApp(MCPTool{Meta: map[string]any{"ui": UIToolMeta{ResourceURI: "ui://x/y.html"}}}) {
		t.Fatal("typed shape with resourceUri: an app")
	}
}

// newExcludeAppsRemote spins up a remote server exposing one plain tool and
// one MCP Apps tool (linked to a ui:// resource), plus a plain resource and
// the app's ui:// resource, and returns its URL.
func newExcludeAppsRemote(t *testing.T) string {
	t.Helper()
	remote := NewServer("remote-test", "1.0.0")
	remote.RegisterTool(NewTool("plain_tool", "a plain tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseAuto("plain-ok"), nil
	})
	remote.RegisterTool(NewTool("app_tool", "an MCP Apps view").UIResource("ui://x/view.html"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseAuto("app-ok"), nil
	})
	remote.RegisterResource(
		NewResource("text://notes", "Notes", "a plain resource", "text/plain"),
		func(ctx context.Context, req *ResourceRequest) (*ResourceResponse, error) {
			return NewResourceResponseText("text://notes", "notes", "text/plain"), nil
		},
	)
	remote.RegisterResource(
		NewResource("ui://x/view.html", "View", "the app view", UIAppMimeType),
		func(ctx context.Context, req *ResourceRequest) (*ResourceResponse, error) {
			return NewUIResourceResponseText("ui://x/view.html", "<html></html>", nil), nil
		},
	)
	srv := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	t.Cleanup(srv.Close)
	return srv.URL
}

// Federating with ExcludeApps drops the remote's app tools from tools/list
// and leaves them uncallable, while plain tools federate as before. The
// same client registered on a second server without the flag still serves
// the app tool there (chat-side federation keeps apps).
func TestRegisterRemoteServerExcludeApps(t *testing.T) {
	url := newExcludeAppsRemote(t)

	excluding := NewServer("excluding", "1.0.0")
	including := NewServer("including", "1.0.0")

	client := NewClient(url, nil, "fed")
	if err := excluding.ReplaceRemoteServers([]RemoteServerEntry{{
		Client:      client,
		Visibility:  ToolVisibilityNative,
		ExcludeApps: true,
	}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := including.RegisterRemoteServer(client); err != nil {
		t.Fatalf("register (no exclusion): %v", err)
	}

	names := func(s *Server) string {
		var b strings.Builder
		for _, tool := range s.ListToolsWithContext(context.Background()) {
			b.WriteString(tool.Name + " ")
		}
		return b.String()
	}

	got := names(excluding)
	if !strings.Contains(got, "fed"+DefaultNamespaceSeparator+"plain_tool") {
		t.Fatalf("plain tool must federate: %q", got)
	}
	if strings.Contains(got, "app_tool") {
		t.Fatalf("app tool must be excluded from tools/list: %q", got)
	}

	got = names(including)
	if !strings.Contains(got, "fed"+DefaultNamespaceSeparator+"app_tool") {
		t.Fatalf("same client without the flag keeps the app tool: %q", got)
	}

	// The excluded app tool is not callable through the excluding server.
	if _, err := excluding.CallTool(context.Background(), "fed"+DefaultNamespaceSeparator+"app_tool", nil); err == nil {
		t.Fatal("call to excluded app tool must fail")
	}
	if _, err := excluding.CallTool(context.Background(), "fed"+DefaultNamespaceSeparator+"plain_tool", nil); err != nil {
		t.Fatalf("plain tool call failed: %v", err)
	}

	// A cache refresh keeps the exclusion.
	if err := excluding.RefreshTools(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := names(excluding); strings.Contains(got, "app_tool") {
		t.Fatalf("app tool must stay excluded after RefreshTools: %q", got)
	}
}

// The exclusion also covers resources and discovery: an excluding
// registration doesn't forward reads of the remote's ui:// resources (plain
// resources still federate), app tools stay out of tool_search even when the
// remote is registered discoverable, and unregistering the client clears the
// exclusion so a later registration without the flag federates apps again.
func TestRegisterRemoteServerExcludeAppsEdges(t *testing.T) {
	url := newExcludeAppsRemote(t)

	t.Run("ui:// resources are not forwarded, plain ones are", func(t *testing.T) {
		excluding := NewServer("excluding", "1.0.0")
		client := NewClient(url, nil, "fed")
		if err := excluding.RegisterRemoteServer(client, WithRemoteExcludeApps()); err != nil {
			t.Fatalf("register: %v", err)
		}

		if _, err := excluding.ReadResource(context.Background(), "text://notes"); err != nil {
			t.Fatalf("plain resource must still federate: %v", err)
		}
		if _, err := excluding.ReadResource(context.Background(), "ui://x/view.html"); err == nil {
			t.Fatal("ui:// resource read must not be forwarded")
		}
	})

	t.Run("app tools stay out of tool_search when discoverable", func(t *testing.T) {
		excluding := NewServer("excluding", "1.0.0")
		client := NewClient(url, nil, "fed")
		if err := excluding.RegisterRemoteServerDiscoverable(client, WithRemoteExcludeApps()); err != nil {
			t.Fatalf("register discoverable: %v", err)
		}

		resp, err := excluding.CallTool(context.Background(), ToolSearchName, map[string]any{"query": "tool"})
		if err != nil {
			t.Fatalf("tool_search: %v", err)
		}
		var text string
		for _, c := range resp.Content {
			text += c.Text
		}
		if strings.Contains(text, "app_tool") {
			t.Fatalf("app tool must be excluded from tool_search: %s", text)
		}
		if !strings.Contains(text, "plain_tool") {
			t.Fatalf("plain tool must be findable via tool_search: %s", text)
		}
	})

	t.Run("unregistering clears the exclusion", func(t *testing.T) {
		s := NewServer("flip", "1.0.0")
		client := NewClient(url, nil, "fed")
		if err := s.RegisterRemoteServer(client, WithRemoteExcludeApps()); err != nil {
			t.Fatalf("register: %v", err)
		}
		s.UnregisterRemoteServer(client)
		if err := s.RegisterRemoteServer(client); err != nil {
			t.Fatalf("re-register: %v", err)
		}

		found := false
		for _, tool := range s.ListToolsWithContext(context.Background()) {
			if tool.Name == "fed"+DefaultNamespaceSeparator+"app_tool" {
				found = true
			}
		}
		if !found {
			t.Fatal("re-registration without the flag must federate the app tool again")
		}
	})
}
