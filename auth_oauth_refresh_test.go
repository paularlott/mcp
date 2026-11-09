package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// refreshingServer is an MCP server that accepts only the access token its token endpoint issued last, and a token
// endpoint that rotates the refresh token on each use (as an authorization server does for public clients), counting
// refreshes.
type refreshingServer struct {
	mu        sync.Mutex
	accept    string // the access token the MCP server accepts; "" accepts none
	refresh   string // the refresh token the token endpoint accepts
	refreshes int
	mcp, tok  *httptest.Server
}

func newRefreshingServer(t *testing.T, refresh string, acceptNew bool) *refreshingServer {
	rs := &refreshingServer{refresh: refresh}
	srv := NewServer("test", "1.0")
	srv.RegisterTool(NewTool("ping", "Ping"), func(context.Context, *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("pong"), nil
	})
	rs.mcp = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.mu.Lock()
		ok := rs.accept != "" && r.Header.Get("Authorization") == "Bearer "+rs.accept
		rs.mu.Unlock()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		srv.HandleRequest(w, r)
	}))
	rs.tok = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		rs.mu.Lock()
		defer rs.mu.Unlock()
		if r.PostForm.Get("refresh_token") != rs.refresh {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		rs.refreshes++
		access := "access-" + string(rune('0'+rs.refreshes))
		rs.refresh = "refresh-" + string(rune('0'+rs.refreshes))
		if acceptNew {
			rs.accept = access
		}
		w.Header().Set("Content-Type", "application/json")
		// no expires_in: as for a stored token, only the server's refusal says it is no longer good
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": access, "refresh_token": rs.refresh, "token_type": "Bearer"})
	}))
	t.Cleanup(func() { rs.mcp.Close(); rs.tok.Close() })
	return rs
}

// A stored access token the server no longer accepts (it expired) is refreshed when the server refuses it, the
// request is sent again with the new one, and the caller is given the rotated tokens to store.
func TestOAuth2Auth_RefreshesWhenRefused(t *testing.T) {
	rs := newRefreshingServer(t, "refresh-0", true)
	var stored [][2]string
	auth := NewOAuth2RefreshTokenAuth(rs.tok.URL, "client-id", "expired", "refresh-0",
		WithTokenUpdate(func(a, r string) { stored = append(stored, [2]string{a, r}) }))
	c := NewClient(rs.mcp.URL, auth, "")
	ctx := context.Background()

	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize with an expired stored token: %v", err)
	}
	if _, err := c.CallTool(ctx, "ping", nil); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if rs.refreshes != 1 || len(stored) != 1 || stored[0] != [2]string{"access-1", "refresh-1"} {
		t.Fatalf("refreshes = %d, stored = %v; want one refresh, storing access-1 and refresh-1", rs.refreshes, stored)
	}

	// the new access token expires too: refreshed again, with the rotated refresh token
	rs.mu.Lock()
	rs.accept = ""
	rs.mu.Unlock()
	if _, err := c.CallTool(ctx, "ping", nil); err != nil {
		t.Fatalf("CallTool after the second expiry: %v", err)
	}
	if rs.refreshes != 2 || stored[len(stored)-1] != [2]string{"access-2", "refresh-2"} {
		t.Errorf("refreshes = %d, stored = %v", rs.refreshes, stored)
	}
}

// A server that refuses every token of the grant gets a bounded number of retries, not a loop of refreshes.
func TestOAuth2Auth_RefusedAfterRefreshIsBounded(t *testing.T) {
	rs := newRefreshingServer(t, "refresh-0", false)
	auth := NewOAuth2RefreshTokenAuth(rs.tok.URL, "client-id", "expired", "refresh-0")
	c := NewClient(rs.mcp.URL, auth, "")
	if err := c.Initialize(context.Background()); err == nil {
		t.Fatal("Initialize succeeded against a server refusing every token")
	}
	// each request is retried at most maxAuthorizationRetries times; Initialize tries both protocol eras
	if rs.refreshes == 0 || rs.refreshes > 2*maxAuthorizationRetries {
		t.Errorf("refreshes = %d, want 1 to %d", rs.refreshes, 2*maxAuthorizationRetries)
	}
}
