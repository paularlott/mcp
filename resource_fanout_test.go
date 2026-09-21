package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fanoutRecorder wraps a Server's HTTP handler, counting resources/read
// requests and capturing the X-MCP-Resource-Fanout-Hop header each carried
// (empty string when absent), so fan-out depth is observable per test.
type fanoutRecorder struct {
	mu     sync.Mutex
	hops   []string
	bodies []string
}

func (f *fanoutRecorder) wrap(s *Server) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		if strings.Contains(string(body), `"resources/read"`) {
			f.mu.Lock()
			f.hops = append(f.hops, r.Header.Get(headerResourceFanoutHop))
			f.bodies = append(f.bodies, string(body))
			f.mu.Unlock()
		}
		s.HandleRequest(w, r)
	})
}

func (f *fanoutRecorder) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.hops...)
}

// TestReadResourceRemoteFanoutCycleBounded proves the hop counter
// structurally bounds a federation cycle: two servers each registered as
// the other's remote resolve an unknown URI to not-found after a bounded
// number of forwards, rather than bouncing requests until the per-attempt
// timeout unwinds the loop. With maxResourceFanoutHops == 3 the exchange is
// fixed: A forwards to B (hop 1), B back to A (hop 2), A to B again
// (hop 3), and B — at the limit — answers not-found instead of forwarding,
// which unwinds the whole chain. B therefore sees exactly two forwarded
// reads, carrying hop headers 1 and 3.
func TestReadResourceRemoteFanoutCycleBounded(t *testing.T) {
	serverA := NewServer("server-a", "0.1")
	serverB := NewServer("server-b", "0.1")

	var recA, recB fanoutRecorder
	tsA := httptest.NewServer(recA.wrap(serverA))
	tsB := httptest.NewServer(recB.wrap(serverB))
	defer tsA.Close()
	defer tsB.Close()

	if err := serverA.ReplaceRemoteServers([]RemoteServerEntry{
		{Client: NewClient(tsB.URL, nil, ""), Visibility: ToolVisibilityNative},
	}); err != nil {
		t.Fatalf("ReplaceRemoteServers on A: %v", err)
	}
	if err := serverB.ReplaceRemoteServers([]RemoteServerEntry{
		{Client: NewClient(tsA.URL, nil, ""), Visibility: ToolVisibilityNative},
	}); err != nil {
		t.Fatalf("ReplaceRemoteServers on B: %v", err)
	}

	_, err := serverA.ReadResource(context.Background(), "ui://nobody-has-this")
	if !errors.Is(err, ErrUnknownResource) {
		t.Fatalf("cycle read: want ErrUnknownResource, got %v", err)
	}

	hopsB := recB.snapshot()
	if len(hopsB) != 2 {
		t.Fatalf("cycle read: B should receive exactly 2 forwarded reads (hops 1 and 3), got %d: %v", len(hopsB), hopsB)
	}
	if hopsB[0] != "1" || hopsB[1] != "3" {
		t.Fatalf("cycle read: forwarded reads should carry hop headers 1 then 3, got %v", hopsB)
	}
	// A's own forwarding is one hop-2 read plus whatever registration traffic.
	var forwardsA int
	for _, h := range recA.snapshot() {
		if h != "" {
			forwardsA++
		}
	}
	if forwardsA != 1 {
		t.Fatalf("cycle read: A should forward exactly once (hop 2), got %d", forwardsA)
	}
}

// TestReadResourceRemoteFanoutSurfacesFailures proves a remote that fails
// outright (here: HTTP 500 on resources/read) is not silently folded into
// "not found": the returned error still satisfies errors.Is(err,
// ErrUnknownResource) — the sentinel callers match on — but names the
// failing remote, and the detail survives the wire as the JSON-RPC error's
// data.details for the next federating hop to see.
func TestReadResourceRemoteFanoutSurfacesFailures(t *testing.T) {
	remote := NewServer("remote", "0.1")
	tsRemote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		if strings.Contains(string(body), `"resources/read"`) {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		remote.HandleRequest(w, r)
	}))
	defer tsRemote.Close()

	aggregator := NewServer("aggregator", "0.1")
	if err := aggregator.ReplaceRemoteServers([]RemoteServerEntry{
		{Client: NewClient(tsRemote.URL, nil, ""), Visibility: ToolVisibilityNative},
	}); err != nil {
		t.Fatalf("ReplaceRemoteServers: %v", err)
	}

	_, err := aggregator.ReadResource(context.Background(), "ui://also-nobody-has-this")
	if !errors.Is(err, ErrUnknownResource) {
		t.Fatalf("failed-remote read: want wrapped ErrUnknownResource, got %v", err)
	}
	if !strings.Contains(err.Error(), tsRemote.URL) {
		t.Fatalf("failed-remote read: error should name the failing remote %q, got: %v", tsRemote.URL, err)
	}

	// Wire-level: through a Client, the same condition arrives as a ToolError
	// whose data carries the diagnosis.
	tsAgg := httptest.NewServer(http.HandlerFunc(aggregator.HandleRequest))
	defer tsAgg.Close()
	_, cerr := NewClient(tsAgg.URL, nil, "").ReadResource(context.Background(), "ui://also-nobody-has-this")
	if cerr == nil {
		t.Fatal("client read against broken federation: want error, got nil")
	}
	te := &ToolError{}
	if errors.As(cerr, &te) {
		data, _ := json.Marshal(te.Data)
		if !strings.Contains(string(data), tsRemote.URL) {
			t.Fatalf("client read: error data should carry the failing remote, got: %s", data)
		}
	} else {
		t.Fatalf("client read: want *ToolError, got %T (%v)", cerr, cerr)
	}
}

// TestReadResourceRemoteFanoutFoundDespiteFailure proves a recorded hard
// failure doesn't preempt a later remote that actually holds the resource —
// registration order still decides, and a success anywhere in the chain wins.
func TestReadResourceRemoteFanoutFoundDespiteFailure(t *testing.T) {
	broken := NewServer("broken", "0.1")
	tsBroken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		if strings.Contains(string(body), `"resources/read"`) {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		broken.HandleRequest(w, r)
	}))
	defer tsBroken.Close()

	healthy := NewServer("healthy", "0.1")
	healthy.RegisterResource(
		NewResource("ui://healthy/view", "View", "", UIAppMimeType),
		func(ctx context.Context, req *ResourceRequest) (*ResourceResponse, error) {
			return NewUIResourceResponseText(req.URI(), "<!DOCTYPE html><html></html>", nil), nil
		},
	)
	tsHealthy := httptest.NewServer(http.HandlerFunc(healthy.HandleRequest))
	defer tsHealthy.Close()

	aggregator := NewServer("aggregator", "0.1")
	if err := aggregator.ReplaceRemoteServers([]RemoteServerEntry{
		{Client: NewClient(tsBroken.URL, nil, ""), Visibility: ToolVisibilityNative},
		{Client: NewClient(tsHealthy.URL, nil, ""), Visibility: ToolVisibilityNative},
	}); err != nil {
		t.Fatalf("ReplaceRemoteServers: %v", err)
	}

	resp, err := aggregator.ReadResource(context.Background(), "ui://healthy/view")
	if err != nil {
		t.Fatalf("read with a broken earlier remote: want success, got %v", err)
	}
	if len(resp.Contents) == 0 || resp.Contents[0].URI != "ui://healthy/view" {
		t.Fatalf("read with a broken earlier remote: unexpected response %+v", resp)
	}
}
