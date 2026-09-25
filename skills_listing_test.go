package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The chat-side listing cache: sources fetched in parallel, each with its
// own budget; a dead source is skipped after onErr without starving the
// rest; successful listings are cached for the TTL and failed ones are not.
func TestSkillsListingCache(t *testing.T) {
	var hits int32
	remote := NewServer("remote-skills", "1.0")
	remote.RegisterSkill(NewSkill("dashboard-ops").
		Description("Operate dashboards").
		File("SKILL.md", []byte("---\nname: dashboard-ops\ndescription: Operate dashboards\n---\nDo things.")))
	remoteSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		remote.HandleRequest(w, r)
	}))
	defer remoteSrv.Close()

	cache := NewSkillsListingCache(time.Minute, time.Second)
	sources := []RemoteSkillsSource{
		{Namespace: "app", Client: NewClient(remoteSrv.URL, nil, "app")},
		{Namespace: "dead", Client: NewClient("http://127.0.0.1:1/mcp", nil, "dead")},
	}

	var failed string
	results := cache.Listings(context.Background(), sources, func(namespace string, err error) {
		failed = namespace
	})
	if failed != "dead" {
		t.Fatalf("failed namespace = %q, want dead", failed)
	}
	if len(results) != 2 || len(results[0].Skills) != 1 || results[0].Namespace != "app" || results[1].Skills != nil {
		t.Fatalf("results = %+v, want the live source's listing and an empty dead one", results)
	}
	if results[0].Skills[0].URI != "skill://dashboard-ops/SKILL.md" {
		t.Fatalf("listings carry the remote's own URIs (namespacing is the caller's label): %q", results[0].Skills[0].URI)
	}

	// Cached: the second listing does not touch the remote. The first pass
	// cost two requests (initialize + skills/list); a cache miss on the
	// second would add at least one more.
	cache.Listings(context.Background(), sources, nil)
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("remote hit %d times, want 2 (second listing served from cache)", got)
	}

	// Prompt line rendering.
	line := SkillPromptLine("app", results[0].Skills[0])
	if line != "- app/dashboard-ops: Operate dashboards (skill://dashboard-ops/SKILL.md)" {
		t.Fatalf("line = %q", line)
	}
	if line := SkillPromptLine("", results[0].Skills[0]); !strings.HasPrefix(line, "- dashboard-ops:") {
		t.Fatalf("bare line = %q", line)
	}
}
