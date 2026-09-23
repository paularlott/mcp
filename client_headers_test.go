package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// WithClientRequestHeaders must add the given headers to every HTTP request —
// the JSON-RPC POSTs and the notification event-stream GET alike — without
// letting them clobber transport-managed headers.
func TestClient_RequestHeaders(t *testing.T) {
	srv := NewServer("svc", "1")
	srv.RegisterTool(NewTool("ping", "ping"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("pong"), nil
	})

	var mu sync.Mutex
	seen := map[string][]string{} // header -> values seen across requests
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		for k, v := range r.Header {
			seen[k] = append(seen[k], v...)
		}
		mu.Unlock()
		srv.HandleRequest(w, r)
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	c := NewClient(ts.URL, staticAuth{"Bearer t"}, "",
		WithClientRequestHeaders(map[string]string{
			"X-MCP-Show-All": "true",
			"Content-Type":   "text/plain", // must lose to the transport's JSON
		}),
	)

	if _, err := c.ListTools(context.Background()); err != nil {
		t.Fatalf("list tools: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if got := seen["X-Mcp-Show-All"]; len(got) == 0 {
		t.Error("extra header never reached the server")
	}
	for _, v := range seen["Content-Type"] {
		if v == "text/plain" {
			t.Error("extra header overrode the transport-managed Content-Type")
		}
	}
}
