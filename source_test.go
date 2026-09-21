package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestToolSource_Native proves a locally-registered tool reports "" (the
// aggregating server's own identity, as opposed to any remote).
func TestToolSource_Native(t *testing.T) {
	s := NewServer("host", "1")
	s.RegisterTool(NewTool("local", "a local tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("ok"), nil
	})

	source, ok := s.ToolSource("local")
	if !ok {
		t.Fatal("expected ok=true for a registered native tool")
	}
	if source != "" {
		t.Fatalf("expected empty source for a native tool, got %q", source)
	}
}

// TestToolSource_Unknown proves an unrecognized tool name fails closed
// (ok=false), never guessing a source.
func TestToolSource_Unknown(t *testing.T) {
	s := NewServer("host", "1")
	if _, ok := s.ToolSource("nope"); ok {
		t.Fatal("expected ok=false for an unrecognized tool name")
	}
}

// TestToolSource_DistinguishesUnnamespacedFederatedServers is the
// regression test for the exact gap reported: two remote servers federated
// without namespace prefixes, each contributing a differently-named tool
// (so no literal name collision hides the problem). ToolSource must still
// attribute each tool to its own remote's BaseURL — this is what lets a
// host enforce that an MCP Apps view mounted from one server's tool can't
// reach the other server's tool just because both are namespace-less and
// visible under their own plain names.
func TestToolSource_DistinguishesUnnamespacedFederatedServers(t *testing.T) {
	remoteA := NewServer("remoteA", "1")
	remoteA.RegisterTool(NewTool("mount", "mounts the app"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("a"), nil
	})
	tsA := httptest.NewServer(http.HandlerFunc(remoteA.HandleRequest))
	defer tsA.Close()

	remoteB := NewServer("remoteB", "1")
	remoteB.RegisterTool(NewTool("action", "a different server's tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("b"), nil
	})
	tsB := httptest.NewServer(http.HandlerFunc(remoteB.HandleRequest))
	defer tsB.Close()

	host := NewServer("host", "1")
	clientA := NewClient(tsA.URL, nil, "") // no namespace prefix
	clientB := NewClient(tsB.URL, nil, "") // no namespace prefix
	if err := host.RegisterRemoteServer(clientA); err != nil {
		t.Fatalf("register A: %v", err)
	}
	if err := host.RegisterRemoteServer(clientB); err != nil {
		t.Fatalf("register B: %v", err)
	}

	mountSource, ok := host.ToolSource("mount")
	if !ok {
		t.Fatal("expected ok=true for 'mount'")
	}
	actionSource, ok := host.ToolSource("action")
	if !ok {
		t.Fatal("expected ok=true for 'action'")
	}

	if mountSource == "" || actionSource == "" {
		t.Fatalf("expected non-empty remote sources, got mount=%q action=%q", mountSource, actionSource)
	}
	if mountSource == actionSource {
		t.Fatalf("expected different sources for tools on different remotes, both got %q", mountSource)
	}
	if mountSource != clientA.BaseURL() {
		t.Fatalf("mount source = %q, want clientA.BaseURL() = %q", mountSource, clientA.BaseURL())
	}
	if actionSource != clientB.BaseURL() {
		t.Fatalf("action source = %q, want clientB.BaseURL() = %q", actionSource, clientB.BaseURL())
	}
}

// TestReadResourceFrom_NativeOnly proves source=="" reads native resources
// (and templates/providers) but never falls through to a federated remote,
// unlike the unscoped ReadResource's fan-out.
func TestReadResourceFrom_NativeOnly(t *testing.T) {
	remote := NewServer("remote", "1")
	remote.RegisterResource(
		NewResource("ui://remote/widget.html", "Widget", "a widget", UIAppMimeType),
		func(ctx context.Context, req *ResourceRequest) (*ResourceResponse, error) {
			return NewUIResourceResponseText("ui://remote/widget.html", "<remote/>", nil), nil
		},
	)
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer ts.Close()

	host := NewServer("host", "1")
	host.RegisterResource(
		NewResource("ui://host/widget.html", "Widget", "a widget", UIAppMimeType),
		func(ctx context.Context, req *ResourceRequest) (*ResourceResponse, error) {
			return NewUIResourceResponseText("ui://host/widget.html", "<native/>", nil), nil
		},
	)
	client := NewClient(ts.URL, nil, "")
	if err := host.RegisterRemoteServer(client); err != nil {
		t.Fatalf("register remote: %v", err)
	}

	// The native resource is reachable with source=="".
	resp, err := host.ReadResourceFrom(context.Background(), "", "ui://host/widget.html")
	if err != nil {
		t.Fatalf("ReadResourceFrom(native): %v", err)
	}
	if len(resp.Contents) != 1 || resp.Contents[0].Text != "<native/>" {
		t.Fatalf("unexpected native contents: %+v", resp.Contents)
	}

	// The remote-only resource is NOT reachable with source=="" — no
	// fan-out, unlike ReadResource.
	if _, err := host.ReadResourceFrom(context.Background(), "", "ui://remote/widget.html"); err != ErrUnknownResource {
		t.Fatalf("expected ErrUnknownResource for remote uri with source=\"\", got %v", err)
	}

	// Sanity: the unscoped ReadResource DOES fan out to the remote.
	if _, err := host.ReadResource(context.Background(), "ui://remote/widget.html"); err != nil {
		t.Fatalf("ReadResource should still fan out to the remote: %v", err)
	}
}

// TestReadResourceFrom_ScopedToOneRemote proves a non-empty source reads
// from exactly one matching remote and never falls through to a different
// remote — the resource-side twin of the tool-ownership gap: two
// namespace-less remotes must not be interchangeable just because they're
// both federated onto the same aggregator.
func TestReadResourceFrom_ScopedToOneRemote(t *testing.T) {
	remoteA := NewServer("remoteA", "1")
	remoteA.RegisterResource(
		NewResource("ui://widget.html", "Widget", "a widget", UIAppMimeType),
		func(ctx context.Context, req *ResourceRequest) (*ResourceResponse, error) {
			return NewUIResourceResponseText("ui://widget.html", "<from-a/>", nil), nil
		},
	)
	tsA := httptest.NewServer(http.HandlerFunc(remoteA.HandleRequest))
	defer tsA.Close()

	remoteB := NewServer("remoteB", "1")
	remoteB.RegisterResource(
		NewResource("ui://widget.html", "Widget", "a widget", UIAppMimeType),
		func(ctx context.Context, req *ResourceRequest) (*ResourceResponse, error) {
			return NewUIResourceResponseText("ui://widget.html", "<from-b/>", nil), nil
		},
	)
	tsB := httptest.NewServer(http.HandlerFunc(remoteB.HandleRequest))
	defer tsB.Close()

	host := NewServer("host", "1")
	clientA := NewClient(tsA.URL, nil, "")
	clientB := NewClient(tsB.URL, nil, "")
	if err := host.RegisterRemoteServer(clientA); err != nil {
		t.Fatalf("register A: %v", err)
	}
	if err := host.RegisterRemoteServer(clientB); err != nil {
		t.Fatalf("register B: %v", err)
	}

	resp, err := host.ReadResourceFrom(context.Background(), clientA.BaseURL(), "ui://widget.html")
	if err != nil {
		t.Fatalf("ReadResourceFrom(A): %v", err)
	}
	if len(resp.Contents) != 1 || resp.Contents[0].Text != "<from-a/>" {
		t.Fatalf("expected content from remote A, got: %+v", resp.Contents)
	}

	resp, err = host.ReadResourceFrom(context.Background(), clientB.BaseURL(), "ui://widget.html")
	if err != nil {
		t.Fatalf("ReadResourceFrom(B): %v", err)
	}
	if len(resp.Contents) != 1 || resp.Contents[0].Text != "<from-b/>" {
		t.Fatalf("expected content from remote B, got: %+v", resp.Contents)
	}
}

// TestReadResourceFrom_UnknownSource proves an unrecognized source (not a
// registered remote's BaseURL) fails closed rather than falling back to
// native or fanning out.
func TestReadResourceFrom_UnknownSource(t *testing.T) {
	host := NewServer("host", "1")
	if _, err := host.ReadResourceFrom(context.Background(), "https://not-a-registered-remote.example", "ui://widget.html"); err != ErrUnknownResource {
		t.Fatalf("expected ErrUnknownResource for an unregistered source, got %v", err)
	}
}
