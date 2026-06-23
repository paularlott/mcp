package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// testUserKey is a context key used to vary the per-user cache key in tests.
type testUserKey struct{}

func TestRemoteProvider_ResolverMemoizedPerRequest(t *testing.T) {
	ts := newRemoteWithTools(func(s *Server) {
		s.RegisterTool(NewTool("weather_lookup", "Get the weather"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("sunny"), nil
		})
	})
	defer ts.Close()

	var calls int32
	p := NewRemoteProvider(func(ctx context.Context) ([]RemoteProviderConfig, error) {
		atomic.AddInt32(&calls, 1)
		return []RemoteProviderConfig{{
			Name:       "svc",
			URL:        ts.URL,
			Auth:       NewBearerTokenAuth("t"),
			Visibility: ToolVisibilityDiscoverable,
		}}, nil
	})

	host := NewServer("host", "1")

	// Within a single request context, the server queries providers from several
	// paths: tools/list (twice internally), tool_search (twice internally), and
	// execute_tool (once). The per-request memo must collapse all of these into a
	// single resolver invocation.
	ctx := WithToolProviders(context.Background(), p)

	_ = host.ListToolsWithContext(ctx)

	if _, err := host.CallTool(ctx, ToolSearchName, map[string]any{"query": "weather", "max_results": 10}); err != nil {
		t.Fatalf("tool_search: %v", err)
	}

	resp, err := host.CallTool(ctx, ExecuteToolName, map[string]any{
		"name":       "svc__weather_lookup",
		"parameters": map[string]any{},
	})
	if err != nil {
		t.Fatalf("execute_tool: %v", err)
	}
	if len(resp.Content) == 0 || resp.Content[0].Text != "sunny" {
		t.Fatalf("unexpected execute_tool response: %+v", resp)
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected resolver called exactly once for the request, got %d", got)
	}

	// A new request (fresh context) resolves again.
	ctx2 := WithToolProviders(context.Background(), p)
	_ = host.ListToolsWithContext(ctx2)
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected resolver called again for a new request, got %d", got)
	}
}

func TestRemoteProvider_ResolverNotMemoizedWithoutProviderContext(t *testing.T) {
	ts := newRemoteWithTools(func(s *Server) {
		s.RegisterTool(NewTool("alpha", "Alpha"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("a"), nil
		})
	})
	defer ts.Close()

	var calls int32
	p := NewRemoteProvider(func(ctx context.Context) ([]RemoteProviderConfig, error) {
		atomic.AddInt32(&calls, 1)
		return []RemoteProviderConfig{{Name: "svc", URL: ts.URL, Auth: NewBearerTokenAuth("t")}}, nil
	})

	// Calling the provider directly with a bare context (no memo installed) must
	// still work — the memo is an optimization, not a requirement.
	if _, err := p.GetTools(context.Background()); err != nil {
		t.Fatalf("GetTools without memo: %v", err)
	}
	if _, err := p.GetTools(context.Background()); err != nil {
		t.Fatalf("GetTools without memo: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected 2 resolver calls without a per-request memo, got %d", got)
	}
}

func TestRemoteProvider_BoundedCacheAcrossUsers(t *testing.T) {
	ts := newRemoteWithTools(func(s *Server) {
		s.RegisterTool(NewTool("alpha", "Alpha"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("a"), nil
		})
	})
	defer ts.Close()

	// One server, but a per-user CacheKey — the classic unbounded-growth risk.
	p := NewRemoteProvider(func(ctx context.Context) ([]RemoteProviderConfig, error) {
		user, _ := ctx.Value(testUserKey{}).(string)
		return []RemoteProviderConfig{{
			Name:     "svc",
			URL:      ts.URL,
			Auth:     NewBearerTokenAuth("t"),
			CacheKey: "svc|" + user,
		}}, nil
	}, WithMaxCacheEntries(5))

	for i := 0; i < 200; i++ {
		ctx := context.WithValue(context.Background(), testUserKey{}, fmt.Sprintf("user%d", i))
		if _, err := p.GetTools(ctx); err != nil {
			t.Fatalf("GetTools for user%d: %v", i, err)
		}
	}
	if got := p.cache.len(); got > 5 {
		t.Fatalf("cache grew unbounded: len=%d, want <= 5", got)
	}
}

func newRemoteWithTools(register func(s *Server)) *httptest.Server {
	remote := NewServer("remote", "1")
	register(remote)
	return httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
}

func resolverFor(configs ...RemoteProviderConfig) RemoteProviderResolver {
	return func(ctx context.Context) ([]RemoteProviderConfig, error) {
		return configs, nil
	}
}

func TestRemoteProvider_GetToolsNamespacedWithVisibility(t *testing.T) {
	ts := newRemoteWithTools(func(s *Server) {
		s.RegisterTool(NewTool("alpha", "Alpha tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("a"), nil
		})
		s.RegisterTool(NewTool("beta", "Beta tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("b"), nil
		})
	})
	defer ts.Close()

	p := NewRemoteProvider(resolverFor(RemoteProviderConfig{
		Name:       "svc",
		URL:        ts.URL,
		Auth:       NewBearerTokenAuth("t"),
		Visibility: ToolVisibilityDiscoverable,
		Keywords:   []string{"extra"},
	}))

	tools, err := p.GetTools(context.Background())
	if err != nil {
		t.Fatalf("GetTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %+v", tools)
	}
	names := map[string]MCPTool{}
	for _, tl := range tools {
		names[tl.Name] = tl
	}
	alpha, ok := names["svc__alpha"]
	if !ok {
		t.Fatalf("expected namespaced svc__alpha, got %+v", tools)
	}
	if alpha.Visibility != ToolVisibilityDiscoverable {
		t.Fatalf("expected discoverable visibility, got %v", alpha.Visibility)
	}
	hasNs, hasRemote, hasExtra := false, false, false
	for _, kw := range alpha.Keywords {
		switch kw {
		case "svc":
			hasNs = true
		case "remote":
			hasRemote = true
		case "extra":
			hasExtra = true
		}
	}
	if !hasNs || !hasRemote || !hasExtra {
		t.Fatalf("expected keywords svc/remote/extra, got %+v", alpha.Keywords)
	}
}

func TestRemoteProvider_ToolFilterListAndExecute(t *testing.T) {
	ts := newRemoteWithTools(func(s *Server) {
		s.RegisterTool(NewTool("allowed", "ok"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("allowed"), nil
		})
		s.RegisterTool(NewTool("blocked", "no"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("blocked"), nil
		})
	})
	defer ts.Close()

	filter := func(name string) bool { return name == "allowed" }
	p := NewRemoteProvider(resolverFor(RemoteProviderConfig{
		Name:       "svc",
		URL:        ts.URL,
		Auth:       NewBearerTokenAuth("t"),
		Visibility: ToolVisibilityNative,
		ToolFilter: filter,
	}))

	tools, err := p.GetTools(context.Background())
	if err != nil {
		t.Fatalf("GetTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "svc__allowed" {
		t.Fatalf("expected only svc__allowed, got %+v", tools)
	}

	// Allowed executes
	res, err := p.ExecuteTool(context.Background(), "svc__allowed", nil)
	if err != nil {
		t.Fatalf("execute allowed: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result for allowed")
	}

	// Blocked is rejected as a real error (not a miss)
	if _, err := p.ExecuteTool(context.Background(), "svc__blocked", nil); err == nil {
		t.Fatal("expected error executing filtered tool")
	}
}

func TestRemoteProvider_ExecuteUnknown(t *testing.T) {
	ts := newRemoteWithTools(func(s *Server) {
		s.RegisterTool(NewTool("alpha", "Alpha"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("a"), nil
		})
	})
	defer ts.Close()

	p := NewRemoteProvider(resolverFor(RemoteProviderConfig{
		Name: "svc", URL: ts.URL, Auth: NewBearerTokenAuth("t"),
	}))

	// Non-namespaced name => not handled
	if _, err := p.ExecuteTool(context.Background(), "alpha", nil); err != ErrUnknownTool {
		t.Fatalf("expected ErrUnknownTool for non-namespaced, got %v", err)
	}
	// Unknown namespace => not handled
	if _, err := p.ExecuteTool(context.Background(), "other__alpha", nil); err != ErrUnknownTool {
		t.Fatalf("expected ErrUnknownTool for unknown namespace, got %v", err)
	}
}

func TestRemoteProvider_CachingAndInvalidate(t *testing.T) {
	remote := NewServer("remote", "1")
	remote.RegisterTool(NewTool("one", "1"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("1"), nil
	})
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer ts.Close()

	cfg := RemoteProviderConfig{Name: "svc", URL: ts.URL, Auth: NewBearerTokenAuth("t")}
	p := NewRemoteProvider(resolverFor(cfg))

	if tools, err := p.GetTools(context.Background()); err != nil || len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %+v err=%v", tools, err)
	}

	// Add a tool on the remote; cached result should still be 1.
	remote.RegisterTool(NewTool("two", "2"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("2"), nil
	})
	if tools, err := p.GetTools(context.Background()); err != nil || len(tools) != 1 {
		t.Fatalf("expected cached 1 tool, got %+v err=%v", tools, err)
	}

	// After invalidating, the new tool appears.
	p.InvalidateCache(cfg.cacheKey())
	if tools, err := p.GetTools(context.Background()); err != nil || len(tools) != 2 {
		t.Fatalf("expected 2 tools after invalidate, got %+v err=%v", tools, err)
	}

	// InvalidateAllCache also forces a refetch.
	p.InvalidateAllCache()
	if tools, err := p.GetTools(context.Background()); err != nil || len(tools) != 2 {
		t.Fatalf("expected 2 tools after invalidate-all, got %+v err=%v", tools, err)
	}
}

func TestRemoteProvider_CacheDisabled(t *testing.T) {
	remote := NewServer("remote", "1")
	remote.RegisterTool(NewTool("one", "1"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("1"), nil
	})
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer ts.Close()

	cfg := RemoteProviderConfig{Name: "svc", URL: ts.URL, Auth: NewBearerTokenAuth("t"), CacheTTL: -1}
	p := NewRemoteProvider(resolverFor(cfg))

	if tools, err := p.GetTools(context.Background()); err != nil || len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %+v err=%v", tools, err)
	}
	remote.RegisterTool(NewTool("two", "2"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("2"), nil
	})
	// Cache disabled => sees the new tool immediately.
	if tools, err := p.GetTools(context.Background()); err != nil || len(tools) != 2 {
		t.Fatalf("expected 2 tools with cache disabled, got %+v err=%v", tools, err)
	}
}

func TestRemoteProvider_ResolverError(t *testing.T) {
	boom := errors.New("resolver boom")
	p := NewRemoteProvider(func(ctx context.Context) ([]RemoteProviderConfig, error) {
		return nil, boom
	})
	if _, err := p.GetTools(context.Background()); err != boom {
		t.Fatalf("expected resolver error from GetTools, got %v", err)
	}
	if _, err := p.ExecuteTool(context.Background(), "svc__x", nil); err != boom {
		t.Fatalf("expected resolver error from ExecuteTool, got %v", err)
	}
}

func TestRemoteProvider_SkipsUnreachableServer(t *testing.T) {
	good := newRemoteWithTools(func(s *Server) {
		s.RegisterTool(NewTool("ok", "ok"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("ok"), nil
		})
	})
	defer good.Close()

	p := NewRemoteProvider(resolverFor(
		RemoteProviderConfig{Name: "good", URL: good.URL, Auth: NewBearerTokenAuth("t")},
		RemoteProviderConfig{Name: "bad", URL: "http://127.0.0.1:0", Auth: NewBearerTokenAuth("t")},
	))

	tools, err := p.GetTools(context.Background())
	if err != nil {
		t.Fatalf("GetTools should skip unreachable, got err=%v", err)
	}
	if len(tools) != 1 || tools[0].Name != "good__ok" {
		t.Fatalf("expected only good__ok, got %+v", tools)
	}
}

func TestRemoteProvider_AuthFuncTakesPrecedence(t *testing.T) {
	ts := newRemoteWithTools(func(s *Server) {
		s.RegisterTool(NewTool("alpha", "Alpha"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("a"), nil
		})
	})
	defer ts.Close()

	called := false
	p := NewRemoteProvider(resolverFor(RemoteProviderConfig{
		Name: "svc",
		URL:  ts.URL,
		Auth: NewBearerTokenAuth("static"),
		AuthFunc: func(ctx context.Context) (AuthProvider, error) {
			called = true
			return NewBearerTokenAuth("dynamic"), nil
		},
	}))

	if _, err := p.GetTools(context.Background()); err != nil {
		t.Fatalf("GetTools: %v", err)
	}
	if !called {
		t.Fatal("expected AuthFunc to be used over static Auth")
	}
}

func TestRemoteProvider_AuthFuncError(t *testing.T) {
	ts := newRemoteWithTools(func(s *Server) {
		s.RegisterTool(NewTool("alpha", "Alpha"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("a"), nil
		})
	})
	defer ts.Close()

	authErr := errors.New("no token")
	p := NewRemoteProvider(resolverFor(RemoteProviderConfig{
		Name: "svc",
		URL:  ts.URL,
		AuthFunc: func(ctx context.Context) (AuthProvider, error) {
			return nil, authErr
		},
	}))

	// Auth failure on a server is skipped during listing.
	if tools, err := p.GetTools(context.Background()); err != nil || len(tools) != 0 {
		t.Fatalf("expected no tools when auth fails, got %+v err=%v", tools, err)
	}
	// But surfaces as an error on execute.
	if _, err := p.ExecuteTool(context.Background(), "svc__alpha", nil); err == nil {
		t.Fatal("expected error when auth resolution fails on execute")
	}
}
