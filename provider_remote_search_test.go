package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

const noToolsFoundText = "No tools found. Try different keywords or a broader search term."

// remoteToolSearch invokes the server's tool_search meta-tool and returns the
// parsed results. An empty/"no tools found" response yields a nil slice.
func remoteToolSearch(t *testing.T, host *Server, ctx context.Context, query string, maxResults int) []SearchResult {
	t.Helper()
	args := map[string]any{}
	if query != "" {
		args["query"] = query
	}
	if maxResults > 0 {
		args["max_results"] = maxResults
	}
	resp, err := host.CallTool(ctx, ToolSearchName, args)
	if err != nil {
		t.Fatalf("tool_search(%q): %v", query, err)
	}
	if resp == nil || len(resp.Content) == 0 {
		t.Fatalf("tool_search(%q): empty response", query)
	}
	text := resp.Content[0].Text
	if text == noToolsFoundText {
		return nil
	}
	var results []SearchResult
	if err := json.Unmarshal([]byte(text), &results); err != nil {
		t.Fatalf("tool_search(%q): parse results: %v (raw=%q)", query, err, text)
	}
	return results
}

func searchResultNames(results []SearchResult) []string {
	names := make([]string, len(results))
	for i, r := range results {
		names[i] = r.Name
	}
	return names
}

func searchHasTool(results []SearchResult, name string) bool {
	for _, r := range results {
		if r.Name == name {
			return true
		}
	}
	return false
}

func searchResultFor(results []SearchResult, name string) (SearchResult, bool) {
	for _, r := range results {
		if r.Name == name {
			return r, true
		}
	}
	return SearchResult{}, false
}

// discoverableRemoteHost builds a host server with a discoverable RemoteProvider
// pointing at a remote that registers the given tools.
func discoverableRemoteHost(t *testing.T, register func(s *Server), keywords ...string) (*Server, context.Context, func()) {
	t.Helper()
	ts := newRemoteWithTools(register)
	p := NewRemoteProvider(resolverFor(RemoteProviderConfig{
		Name:       "svc",
		URL:        ts.URL,
		Auth:       NewBearerTokenAuth("t"),
		Visibility: ToolVisibilityDiscoverable,
		Keywords:   keywords,
	}))
	host := NewServer("host", "1")
	ctx := WithToolProviders(context.Background(), p)
	return host, ctx, ts.Close
}

func TestRemoteToolSearch_FindsByName(t *testing.T) {
	host, ctx, closeFn := discoverableRemoteHost(t, func(s *Server) {
		s.RegisterTool(NewTool("weather_lookup", "Get the current forecast"), echoTool("sunny"))
	})
	defer closeFn()

	results := remoteToolSearch(t, host, ctx, "weather_lookup", 10)
	if !searchHasTool(results, "svc__weather_lookup") {
		t.Fatalf("expected svc__weather_lookup by name, got %v", searchResultNames(results))
	}
}

func TestRemoteToolSearch_FindsByDescriptionWord(t *testing.T) {
	host, ctx, closeFn := discoverableRemoteHost(t, func(s *Server) {
		s.RegisterTool(NewTool("wx", "Get the current forecast for a city"), echoTool("sunny"))
	})
	defer closeFn()

	results := remoteToolSearch(t, host, ctx, "forecast", 10)
	if !searchHasTool(results, "svc__wx") {
		t.Fatalf("expected svc__wx by description word, got %v", searchResultNames(results))
	}
}

func TestRemoteToolSearch_FindsByKeyword(t *testing.T) {
	// The provider attaches the namespace ("svc"), "remote", and any custom
	// keywords to every tool. Each should match in search.
	host, ctx, closeFn := discoverableRemoteHost(t, func(s *Server) {
		s.RegisterTool(NewTool("zzz", "Opaque name"), echoTool("ok"))
	}, "barometer")
	defer closeFn()

	for _, kw := range []string{"svc", "remote", "barometer"} {
		results := remoteToolSearch(t, host, ctx, kw, 10)
		if !searchHasTool(results, "svc__zzz") {
			t.Fatalf("expected svc__zzz via keyword %q, got %v", kw, searchResultNames(results))
		}
	}
}

func TestRemoteToolSearch_NotInListButSearchable(t *testing.T) {
	host, ctx, closeFn := discoverableRemoteHost(t, func(s *Server) {
		s.RegisterTool(NewTool("hidden_tool", "A discoverable tool"), echoTool("ok"))
	})
	defer closeFn()

	for _, tl := range host.ListToolsWithContext(ctx) {
		if tl.Name == "svc__hidden_tool" {
			t.Fatal("discoverable remote tool must not appear in tools/list")
		}
	}
	if results := remoteToolSearch(t, host, ctx, "discoverable", 10); !searchHasTool(results, "svc__hidden_tool") {
		t.Fatalf("expected svc__hidden_tool searchable, got %v", searchResultNames(results))
	}
}

func TestRemoteToolSearch_NativeRemoteToolsAlsoSearchable(t *testing.T) {
	// Native-visibility remote tools appear in tools/list AND are searchable.
	ts := newRemoteWithTools(func(s *Server) {
		s.RegisterTool(NewTool("list_repos", "List repositories"), echoTool("ok"))
	})
	defer ts.Close()

	p := NewRemoteProvider(resolverFor(RemoteProviderConfig{
		Name:       "gh",
		URL:        ts.URL,
		Auth:       NewBearerTokenAuth("t"),
		Visibility: ToolVisibilityNative,
	}))
	host := NewServer("host", "1")
	ctx := WithToolProviders(context.Background(), p)

	inList := false
	for _, tl := range host.ListToolsWithContext(ctx) {
		if tl.Name == "gh__list_repos" {
			inList = true
		}
	}
	if !inList {
		t.Fatal("native remote tool should appear in tools/list")
	}
	if results := remoteToolSearch(t, host, ctx, "repositories", 10); !searchHasTool(results, "gh__list_repos") {
		t.Fatalf("expected native remote tool searchable, got %v", searchResultNames(results))
	}
}

func TestRemoteToolSearch_AcrossMultipleServers(t *testing.T) {
	ts1 := newRemoteWithTools(func(s *Server) {
		s.RegisterTool(NewTool("alpha_tool", "First"), echoTool("1"))
	})
	defer ts1.Close()
	ts2 := newRemoteWithTools(func(s *Server) {
		s.RegisterTool(NewTool("beta_tool", "Second"), echoTool("2"))
	})
	defer ts2.Close()

	p := NewRemoteProvider(resolverFor(
		RemoteProviderConfig{Name: "one", URL: ts1.URL, Auth: NewBearerTokenAuth("t"), Visibility: ToolVisibilityDiscoverable},
		RemoteProviderConfig{Name: "two", URL: ts2.URL, Auth: NewBearerTokenAuth("t"), Visibility: ToolVisibilityDiscoverable},
	))
	host := NewServer("host", "1")
	ctx := WithToolProviders(context.Background(), p)

	// "remote" keyword is attached to all of them.
	results := remoteToolSearch(t, host, ctx, "remote", 10)
	if !searchHasTool(results, "one__alpha_tool") || !searchHasTool(results, "two__beta_tool") {
		t.Fatalf("expected tools from both servers, got %v", searchResultNames(results))
	}
}

func TestRemoteToolSearch_MaxResultsRespected(t *testing.T) {
	host, ctx, closeFn := discoverableRemoteHost(t, func(s *Server) {
		for _, n := range []string{"remote_a", "remote_b", "remote_c", "remote_d", "remote_e"} {
			s.RegisterTool(NewTool(n, "A remote tool"), echoTool("ok"))
		}
	})
	defer closeFn()

	results := remoteToolSearch(t, host, ctx, "remote", 2)
	if len(results) != 2 {
		t.Fatalf("expected max_results=2 to cap results, got %d: %v", len(results), searchResultNames(results))
	}
}

func TestRemoteToolSearch_EmptyQueryListsAll(t *testing.T) {
	host, ctx, closeFn := discoverableRemoteHost(t, func(s *Server) {
		s.RegisterTool(NewTool("one", "1"), echoTool("1"))
		s.RegisterTool(NewTool("two", "2"), echoTool("2"))
		s.RegisterTool(NewTool("three", "3"), echoTool("3"))
	})
	defer closeFn()

	results := remoteToolSearch(t, host, ctx, "", 50)
	for _, want := range []string{"svc__one", "svc__two", "svc__three"} {
		if !searchHasTool(results, want) {
			t.Fatalf("empty query should list %s, got %v", want, searchResultNames(results))
		}
	}
}

func TestRemoteToolSearch_ScoreOrdering(t *testing.T) {
	host, ctx, closeFn := discoverableRemoteHost(t, func(s *Server) {
		s.RegisterTool(NewTool("send_email", "Send an email"), echoTool("sent"))
		s.RegisterTool(NewTool("email_sender", "An email sender"), echoTool("sent"))
	})
	defer closeFn()

	results := remoteToolSearch(t, host, ctx, "send", 10)
	if len(results) < 2 {
		t.Fatalf("expected both tools to match 'send', got %v", searchResultNames(results))
	}
	// Results must be sorted by score descending.
	for i := 1; i < len(results); i++ {
		if results[i-1].Score < results[i].Score {
			t.Fatalf("results not sorted by score desc: %+v", results)
		}
	}
	// "send_email" (name starts with the query) should outrank "email_sender".
	sendEmail, ok1 := searchResultFor(results, "svc__send_email")
	emailSender, ok2 := searchResultFor(results, "svc__email_sender")
	if !ok1 || !ok2 {
		t.Fatalf("expected both tools present, got %v", searchResultNames(results))
	}
	if !(sendEmail.Score > emailSender.Score) {
		t.Fatalf("expected svc__send_email to score higher than svc__email_sender (%v vs %v)", sendEmail.Score, emailSender.Score)
	}
	if results[0].Name != "svc__send_email" {
		t.Fatalf("expected svc__send_email ranked first, got %v", searchResultNames(results))
	}
}

func TestRemoteToolSearch_NoMatchReturnsEmpty(t *testing.T) {
	host, ctx, closeFn := discoverableRemoteHost(t, func(s *Server) {
		s.RegisterTool(NewTool("weather_lookup", "Get the forecast"), echoTool("sunny"))
	})
	defer closeFn()

	if results := remoteToolSearch(t, host, ctx, "zzz_no_such_thing_xyz", 10); len(results) != 0 {
		t.Fatalf("expected no matches, got %v", searchResultNames(results))
	}
}

func TestRemoteToolSearch_FilteredToolsNotSearchable(t *testing.T) {
	ts := newRemoteWithTools(func(s *Server) {
		s.RegisterTool(NewTool("allowed_search", "allowed"), echoTool("a"))
		s.RegisterTool(NewTool("blocked_search", "blocked"), echoTool("b"))
	})
	defer ts.Close()

	p := NewRemoteProvider(resolverFor(RemoteProviderConfig{
		Name:       "svc",
		URL:        ts.URL,
		Auth:       NewBearerTokenAuth("t"),
		Visibility: ToolVisibilityDiscoverable,
		ToolFilter: func(name string) bool { return name == "allowed_search" },
	}))
	host := NewServer("host", "1")
	ctx := WithToolProviders(context.Background(), p)

	results := remoteToolSearch(t, host, ctx, "search", 10)
	if !searchHasTool(results, "svc__allowed_search") {
		t.Fatalf("expected allowed tool searchable, got %v", searchResultNames(results))
	}
	if searchHasTool(results, "svc__blocked_search") {
		t.Fatalf("filtered tool must not be searchable, got %v", searchResultNames(results))
	}
}

func TestRemoteToolSearch_ExecuteToolAfterSearch(t *testing.T) {
	host, ctx, closeFn := discoverableRemoteHost(t, func(s *Server) {
		s.RegisterTool(NewTool("weather_lookup", "Get the forecast"), echoTool("sunny"))
	})
	defer closeFn()

	// Discover it...
	results := remoteToolSearch(t, host, ctx, "weather", 10)
	if !searchHasTool(results, "svc__weather_lookup") {
		t.Fatalf("expected svc__weather_lookup discoverable, got %v", searchResultNames(results))
	}

	// ...then run it through the execute_tool meta-tool.
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
}

func TestRemoteToolSearch_ShowAllListsDiscoverableRemoteTools(t *testing.T) {
	host, ctx, closeFn := discoverableRemoteHost(t, func(s *Server) {
		s.RegisterTool(NewTool("hidden_tool", "discoverable"), echoTool("ok"))
	})
	defer closeFn()

	// In show-all mode the discoverable remote tool appears directly in the list.
	showAllCtx := WithShowAllTools(ctx)
	found := false
	for _, tl := range host.ListToolsWithContext(showAllCtx) {
		if tl.Name == "svc__hidden_tool" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected discoverable remote tool in tools/list under show-all mode")
	}
}

func echoTool(text string) ToolHandler {
	return func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText(text), nil
	}
}
