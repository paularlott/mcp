package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestRemoteProvider_StressMixedHealth hammers a single RemoteProvider with
// many concurrent callers against a deliberately unpleasant mix of servers —
// healthy, permanently hanging, permanently erroring, and one that flaps
// between healthy and hanging mid-run — for many iterations. It exists to
// shake out data races (run under -race) and deadlocks/hangs (the whole test
// is bounded by testing's own timeout) in the concurrent GetTools/ExecuteTool
// path, the shared LRU cache, and the OnServerError hook, none of which are
// exercised by the single-call correctness tests elsewhere in this file.
func TestRemoteProvider_StressMixedHealth(t *testing.T) {
	good := newRemoteWithTools(func(s *Server) {
		s.RegisterTool(NewTool("ok", "ok"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("ok"), nil
		})
	})
	defer good.Close()

	erroring := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer erroring.Close()

	// hangHandler blocks for a fixed duration comfortably longer than every
	// ListTimeout/CallTimeout configured below, so the client always gives up
	// first. It deliberately does NOT wait on r.Context().Done(): whether that
	// fires when the client aborts depends on Go's net/http server having a
	// reason to notice (e.g. reading the request body), which a handler that
	// never touches r.Body cannot rely on — using it here made server-side
	// handler goroutines outlive the test, hanging httptest.Server.Close().
	// A bounded sleep exercises the exact same client-side behaviour (a slow
	// server the client must not wait forever on) without that flakiness.
	release := make(chan struct{})
	hangHandler := func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
			w.WriteHeader(http.StatusInternalServerError)
		case <-time.After(150 * time.Millisecond):
		}
	}
	hang := httptest.NewServer(http.HandlerFunc(hangHandler))
	defer func() {
		close(release)
		hang.Close()
	}()

	// flapping alternates between behaving like `good` and hanging, based on
	// a counter, so different goroutines racing against it see different
	// outcomes for the same server within the same run.
	var flapCounter atomic.Int64
	flapping := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if flapCounter.Add(1)%2 == 0 {
			hangHandler(w, r)
			return
		}
		remote := NewServer("flapping", "1")
		remote.RegisterTool(NewTool("flap", "flap"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("flap"), nil
		})
		remote.HandleRequest(w, r)
	}))
	defer flapping.Close()

	var (
		hookMu    sync.Mutex
		hookCalls = map[string]int{}
	)
	p := NewRemoteProvider(resolverFor(
		RemoteProviderConfig{Name: "good", URL: good.URL, Auth: NewBearerTokenAuth("t"), CacheTTL: -1},
		RemoteProviderConfig{Name: "erroring", URL: erroring.URL, Auth: NewBearerTokenAuth("t"), CacheTTL: -1, CallTimeout: 200 * time.Millisecond},
		RemoteProviderConfig{Name: "hang", URL: hang.URL, Auth: NewBearerTokenAuth("t"), CacheTTL: -1, ListTimeout: 30 * time.Millisecond, CallTimeout: 30 * time.Millisecond},
		RemoteProviderConfig{Name: "flapping", URL: flapping.URL, Auth: NewBearerTokenAuth("t"), CacheTTL: -1, ListTimeout: 30 * time.Millisecond},
	), WithOnServerError(func(cfg RemoteProviderConfig, err error) {
		hookMu.Lock()
		hookCalls[cfg.Name]++
		hookMu.Unlock()
	}))

	// Kept modest deliberately: this exercises concurrency-safety (races,
	// deadlocks), not raw throughput. Higher volumes against the
	// deliberately-hanging servers just queue up connections faster than
	// -race's overhead lets them drain, which looks like a hang but is a
	// test-harness artifact, not a product bug.
	const goroutines = 5
	const iterations = 5

	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				tools, err := p.GetTools(context.Background())
				if err != nil {
					errCh <- fmt.Errorf("goroutine %d iter %d: GetTools error: %w", g, i, err)
					return
				}
				sawGood := false
				for _, tl := range tools {
					if tl.Name == "good__ok" {
						sawGood = true
					}
				}
				if !sawGood {
					errCh <- fmt.Errorf("goroutine %d iter %d: expected good__ok always present, got %+v", g, i, tools)
					return
				}

				// Also exercise ExecuteTool concurrently against the same mix.
				if _, err := p.ExecuteTool(context.Background(), "good__ok", nil); err != nil {
					errCh <- fmt.Errorf("goroutine %d iter %d: ExecuteTool(good) error: %w", g, i, err)
					return
				}
				// erroring/hang tool calls are expected to fail; just must not hang or panic.
				_, _ = p.ExecuteTool(context.Background(), "erroring__whatever", nil)
				_, _ = p.ExecuteTool(context.Background(), "hang__whatever", nil)
			}
		}(g)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("stress test did not complete within 30s — suspect a deadlock introduced by the concurrency changes")
	}

	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	hookMu.Lock()
	defer hookMu.Unlock()
	if hookCalls["good"] != 0 {
		t.Errorf("expected the healthy server to never trigger OnServerError, got %d calls", hookCalls["good"])
	}
	if hookCalls["erroring"] == 0 {
		t.Error("expected the erroring server to trigger OnServerError at least once")
	}
	if hookCalls["hang"] == 0 {
		t.Error("expected the hanging server to trigger OnServerError at least once")
	}
}
