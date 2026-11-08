package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A refresh that fails for a transient reason keeps the refresh token; one
// the authorization server refuses (an OAuth error response) drops it.
func TestRefreshKeepsTokensOnTransientFailure(t *testing.T) {
	var status atomic.Int32
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code := int(status.Load())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		if code == http.StatusBadRequest {
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
		}
	}))
	defer tokenSrv.Close()

	newAuth := func() *AuthorizationCodeAuth {
		a, err := NewAuthorizationCodeAuth(AuthorizationCodeConfig{
			RedirectURI: "http://localhost/callback",
			Authorizer:  func(ctx context.Context, u string) (string, error) { return "", nil },
			Session: &OAuthSession{
				TokenEndpoint: tokenSrv.URL, ClientID: "c", AccessToken: "old", RefreshToken: "keep-me",
				Expiry: time.Now().Add(-time.Minute),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	refresh := func(a *AuthorizationCodeAuth) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.refresh(context.Background())
	}

	for _, code := range []int{http.StatusServiceUnavailable, http.StatusInternalServerError} {
		status.Store(int32(code))
		a := newAuth()
		if err := refresh(a); err == nil {
			t.Fatalf("%d: refresh succeeded", code)
		}
		if got := a.Session().RefreshToken; got != "keep-me" {
			t.Errorf("%d (transient): refresh token %q, want it kept", code, got)
		}
	}

	// A token endpoint that can't be reached at all is transient too.
	a := newAuth()
	a.mu.Lock()
	a.session.TokenEndpoint = "http://127.0.0.1:1/token"
	a.mu.Unlock()
	if err := refresh(a); err == nil {
		t.Fatal("refresh against a closed port succeeded")
	}
	if got := a.Session().RefreshToken; got != "keep-me" {
		t.Errorf("transport error: refresh token %q, want it kept", got)
	}

	status.Store(http.StatusBadRequest)
	a = newAuth()
	if err := refresh(a); err == nil {
		t.Fatal("refused refresh succeeded")
	}
	if s := a.Session(); s.RefreshToken != "" || s.AccessToken != "" {
		t.Errorf("invalid_grant: session %+v, want tokens dropped", s)
	}
}

// countingAuth is an AuthProvider that counts the authorizations a Client
// asks it for (respondToChallenge).
type countingAuth struct{ challenges atomic.Int32 }

func (a *countingAuth) GetAuthHeader() (string, error) { return "", nil }
func (a *countingAuth) respondToChallenge(ctx context.Context, serverURL string, status int, challenges []string, sent string) (bool, error) {
	a.challenges.Add(1)
	return false, nil
}

// The background event stream never starts an authorization (which would
// open the user's browser from the reader goroutine, on every reconnect):
// a 401 on the GET stream leaves it to foreground requests.
func TestBackgroundStreamNeverAuthorizes(t *testing.T) {
	var gets atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			gets.Add(1)
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="http://example.invalid/.well-known/oauth-protected-resource"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req MCPRequest
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "server/discover":
			w.WriteHeader(http.StatusBadRequest) // Legacy-only server
			return
		case "initialize":
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "s", "version": "1"},
			}})
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()

	auth := &countingAuth{}
	c := NewClient(srv.URL, auth, "").EnableNotifications()
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for gets.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if gets.Load() == 0 {
		t.Fatal("the client never opened the GET stream")
	}
	time.Sleep(100 * time.Millisecond)
	if n := auth.challenges.Load(); n != 0 {
		t.Fatalf("the background stream asked for authorization %d times", n)
	}
}

// A 2025-x client declares elicitation only when it can answer: with an
// OnElicitation handler, or when a Server forwards calls through it.
func TestLegacyElicitationDeclaredOnlyWhenAnswerable(t *testing.T) {
	plain := NewClient("http://example.invalid", nil, "")
	if _, ok := plain.legacyCapabilities()["elicitation"]; ok {
		t.Error("a client with no handler declared elicitation")
	}
	withHandler := NewClient("http://example.invalid", nil, "").OnElicitation(func(ctx context.Context, req ElicitationRequest) (*ElicitResult, error) {
		return &ElicitResult{Action: ElicitDecline}, nil
	})
	if _, ok := withHandler.legacyCapabilities()["elicitation"]; !ok {
		t.Error("a client with a handler didn't declare elicitation")
	}

	// RegisterRemoteServer marks the client before its first request.
	remote := httptest.NewServer(http.HandlerFunc(NewServer("r", "1").HandleRequest))
	defer remote.Close()
	forwarding := NewClient(remote.URL, nil, "r")
	defer forwarding.Close()
	if err := NewServer("proxy", "1").RegisterRemoteServer(forwarding); err != nil {
		t.Fatal(err)
	}
	if _, ok := forwarding.legacyCapabilities()["elicitation"]; !ok {
		t.Error("a client registered with RegisterRemoteServer didn't declare elicitation")
	}
	provided := RemoteProviderConfig{Name: "p", URL: "http://example.invalid"}.newClient(nil)
	if _, ok := provided.legacyCapabilities()["elicitation"]; !ok {
		t.Error("a RemoteProvider client didn't declare elicitation")
	}
}
