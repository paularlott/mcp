package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// testAS is an in-process OAuth authorization server for AuthorizationCodeAuth tests. It approves every authorization
// request at once (the headless authorizer reads its redirect), records what it is sent, and issues tokens that
// testRS accepts.
type testAS struct {
	t      *testing.T
	srv    *httptest.Server
	prefix string // issuer path, e.g. "/tenant1"

	// Configuration, set before use.
	metadataIssuer    string   // issuer in its metadata, when not its own (a poisoned document)
	advertiseIss      bool     // authorization_response_iss_parameter_supported
	issInRedirect     string   // "correct" (default), "omit", "wrong" or "normalized"
	authMethods       []string // token_endpoint_auth_methods_supported; nil omits it
	scopesSupported   []string
	cimd              bool
	noRegistration    bool
	registeredSecret  string // client_secret the registration endpoint issues
	registeredMethod  string // token_endpoint_auth_method it returns
	issueRefreshToken bool
	expiresIn         int    // seconds; 0 gives 3600
	authError         string // an error to answer authorization requests with

	mu            sync.Mutex
	metadataReads int
	registrations []map[string]any
	authRequests  []url.Values
	tokenRequests []testTokenRequest
	challenge     string
	nextToken     int
}

type testTokenRequest struct {
	form  url.Values
	basic string // the decoded Basic credentials, "" when none
}

// tokenScopes records the scopes of every token any testAS issued, for testRS to check.
var tokenScopes sync.Map

func newTestAS(t *testing.T, prefix string) *testAS {
	as := &testAS{t: t, prefix: prefix, advertiseIss: true, authMethods: []string{"none"}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server"+prefix, as.metadata)
	mux.HandleFunc(prefix+"/authorize", as.authorize)
	mux.HandleFunc(prefix+"/token", as.token)
	mux.HandleFunc(prefix+"/register", as.register)
	as.srv = httptest.NewServer(mux)
	t.Cleanup(as.srv.Close)
	return as
}

func (as *testAS) issuer() string { return as.srv.URL + as.prefix }

func (as *testAS) metadata(w http.ResponseWriter, r *http.Request) {
	as.mu.Lock()
	as.metadataReads++
	as.mu.Unlock()
	issuer := as.issuer()
	if as.metadataIssuer != "" {
		issuer = as.metadataIssuer
	}
	doc := map[string]any{
		"issuer":                           issuer,
		"authorization_endpoint":           as.issuer() + "/authorize",
		"token_endpoint":                   as.issuer() + "/token",
		"response_types_supported":         []string{"code"},
		"code_challenge_methods_supported": []string{"S256"},
	}
	if !as.noRegistration {
		doc["registration_endpoint"] = as.issuer() + "/register"
	}
	if as.advertiseIss {
		doc["authorization_response_iss_parameter_supported"] = true
	}
	if as.authMethods != nil {
		doc["token_endpoint_auth_methods_supported"] = as.authMethods
	}
	if as.scopesSupported != nil {
		doc["scopes_supported"] = as.scopesSupported
	}
	if as.cimd {
		doc["client_id_metadata_document_supported"] = true
	}
	_ = json.NewEncoder(w).Encode(doc)
}

func (as *testAS) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	as.mu.Lock()
	as.authRequests = append(as.authRequests, q)
	as.challenge = q.Get("code_challenge")
	as.mu.Unlock()
	redirect, err := url.Parse(q.Get("redirect_uri"))
	if err != nil {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	resp := redirect.Query()
	if as.authError != "" {
		resp.Set("error", as.authError)
	} else {
		resp.Set("code", "test-code")
	}
	resp.Set("state", q.Get("state"))
	switch as.issInRedirect {
	case "", "correct":
		resp.Set("iss", as.issuer())
	case "wrong":
		resp.Set("iss", "https://evil.example.com")
	case "normalized":
		resp.Set("iss", as.issuer()+"/")
	}
	redirect.RawQuery = resp.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

func (as *testAS) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	req := testTokenRequest{form: r.PostForm}
	if user, pass, ok := r.BasicAuth(); ok {
		req.basic = user + ":" + pass
	}
	as.mu.Lock()
	as.tokenRequests = append(as.tokenRequests, req)
	challenge := as.challenge
	as.nextToken++
	n := as.nextToken
	as.mu.Unlock()

	var scope string
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		if oauth2.S256ChallengeFromVerifier(r.PostForm.Get("code_verifier")) != challenge {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"PKCE verification failed"}`)
			return
		}
		as.mu.Lock()
		scope = as.authRequests[len(as.authRequests)-1].Get("scope")
		as.mu.Unlock()
	case "refresh_token":
		if !strings.HasPrefix(r.PostForm.Get("refresh_token"), "refresh-") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
	}
	token := fmt.Sprintf("tok-%s-%d", strings.TrimPrefix(as.srv.URL, "http://127.0.0.1:"), n)
	tokenScopes.Store(token, strings.Fields(scope))
	resp := map[string]any{"access_token": token, "token_type": "Bearer", "expires_in": 3600}
	if as.expiresIn != 0 {
		resp["expires_in"] = as.expiresIn
	}
	if scope != "" {
		resp["scope"] = scope
	}
	if as.issueRefreshToken {
		resp["refresh_token"] = fmt.Sprintf("refresh-%d", n)
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (as *testAS) register(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	as.mu.Lock()
	as.registrations = append(as.registrations, body)
	id := fmt.Sprintf("client-of-%s-%d", as.srv.URL, len(as.registrations))
	as.mu.Unlock()
	resp := map[string]any{"client_id": id}
	if as.registeredSecret != "" {
		resp["client_secret"] = as.registeredSecret
	}
	if as.registeredMethod != "" {
		resp["token_endpoint_auth_method"] = as.registeredMethod
	}
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

func (as *testAS) snapshot() (auths []url.Values, tokens []testTokenRequest, regs []map[string]any) {
	as.mu.Lock()
	defer as.mu.Unlock()
	return slices.Clone(as.authRequests), slices.Clone(as.tokenRequests), slices.Clone(as.registrations)
}

// testRS is an MCP server at /mcp protected by Bearer tokens from a testAS, publishing protected resource metadata
// at the path-based well-known address.
type testRS struct {
	srv *httptest.Server

	mu          sync.Mutex
	issuer      string                       // the authorization server its metadata names
	resource    string                       // the resource its metadata names; "" is its own /mcp URL
	scopes      []string                     // scopes_supported; nil omits it
	scopeFor    func(method string) []string // the scopes a method needs; nil needs none
	challenge   string                       // scope put in the 401 challenge
	forbidScope string                       // scope put in a 403 challenge; "" names what the method needs
	noPRM       bool                         // publish no protected resource metadata
	accept      func(token string) bool      // nil accepts every token a testAS issued
	authHeaders []string                     // the Authorization header of every MCP request
}

func newTestRS(t *testing.T, issuer string) *testRS {
	rs := &testRS{issuer: issuer}
	srv := NewServer("protected", "1")
	srv.RegisterTool(NewTool("echo", "echo"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("ok"), nil
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		rs.mu.Lock()
		defer rs.mu.Unlock()
		if rs.noPRM {
			http.NotFound(w, r)
			return
		}
		resource := rs.resource
		if resource == "" {
			resource = rs.srv.URL + "/mcp"
		}
		doc := map[string]any{"resource": resource, "authorization_servers": []string{rs.issuer}}
		if rs.scopes != nil {
			doc["scopes_supported"] = rs.scopes
		}
		_ = json.NewEncoder(w).Encode(doc)
	})
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var rpc struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &rpc)
		header := r.Header.Get("Authorization")
		rs.mu.Lock()
		rs.authHeaders = append(rs.authHeaders, header)
		accept, scopeFor, challenge, noPRM, forbidScope := rs.accept, rs.scopeFor, rs.challenge, rs.noPRM, rs.forbidScope
		rs.mu.Unlock()

		challengeParams := ""
		if !noPRM {
			challengeParams = fmt.Sprintf(`, resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, rs.srv.URL)
		}
		token, ok := strings.CutPrefix(header, "Bearer ")
		granted, issued := tokenScopes.Load(token)
		if !ok || !issued || (accept != nil && !accept(token)) {
			scope := ""
			if challenge != "" {
				scope = fmt.Sprintf(`, scope="%s"`, challenge)
			}
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`+scope+challengeParams)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if scopeFor != nil {
			need := scopeFor(rpc.Method)
			if !isSubset(need, granted.([]string)) {
				if forbidScope == "" {
					forbidScope = strings.Join(need, " ")
				}
				w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer error="insufficient_scope", scope="%s"`, forbidScope)+challengeParams)
				w.WriteHeader(http.StatusForbidden)
				return
			}
		}
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		srv.HandleRequest(w, r)
	})
	rs.srv = httptest.NewServer(mux)
	t.Cleanup(rs.srv.Close)
	return rs
}

func (rs *testRS) url() string { return rs.srv.URL + "/mcp" }

func (rs *testRS) set(f func(*testRS)) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	f(rs)
}

// testAuthorizer is the user: it fetches the authorization URL and returns the redirect, counting its calls.
type testAuthorizer struct {
	mu    sync.Mutex
	calls int
	urls  []string
}

func (ta *testAuthorizer) authorize(ctx context.Context, authorizationURL string) (string, error) {
	ta.mu.Lock()
	ta.calls++
	ta.urls = append(ta.urls, authorizationURL)
	ta.mu.Unlock()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authorizationURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); loc != "" {
		return loc, nil
	}
	return "", fmt.Errorf("authorization endpoint answered %s", resp.Status)
}

const testRedirectURI = "http://127.0.0.1:9999/callback"

func newTestAuth(t *testing.T, cfg AuthorizationCodeConfig) (*AuthorizationCodeAuth, *testAuthorizer) {
	t.Helper()
	ta := &testAuthorizer{}
	if cfg.RedirectURI == "" {
		cfg.RedirectURI = testRedirectURI
	}
	if cfg.Authorizer == nil {
		cfg.Authorizer = ta.authorize
	}
	auth, err := NewAuthorizationCodeAuth(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return auth, ta
}

func listTools(t *testing.T, serverURL string, auth AuthProvider) ([]MCPTool, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := NewClient(serverURL, auth, "")
	defer c.Close()
	return c.ListTools(ctx)
}

// TestAuthorizationCode_Flow covers the whole flow against a server that requires a token for every request: the
// 401 starts discovery (protected resource metadata, then authorization server metadata), dynamic registration, the
// authorization request with PKCE S256, state and the resource parameter, the token request with the verifier and the
// same resource, and the request retried with the token.
func TestAuthorizationCode_Flow(t *testing.T) {
	as := newTestAS(t, "")
	rs := newTestRS(t, as.issuer())
	var sessions []OAuthSession
	auth, ta := newTestAuth(t, AuthorizationCodeConfig{ClientName: "test-app", OnSession: func(s OAuthSession) { sessions = append(sessions, s) }})

	tools, err := listTools(t, rs.url(), auth)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}
	if ta.calls != 1 {
		t.Fatalf("authorizer called %d times, want 1", ta.calls)
	}
	auths, tokens, regs := as.snapshot()
	if len(regs) != 1 {
		t.Fatalf("registrations = %d, want 1", len(regs))
	}
	if regs[0]["client_name"] != "test-app" || regs[0]["token_endpoint_auth_method"] != "none" || regs[0]["application_type"] != "native" {
		t.Errorf("registration = %v", regs[0])
	}
	if gt, _ := regs[0]["grant_types"].([]any); !slices.Contains(gt, any("refresh_token")) {
		t.Errorf("registration grant_types = %v, want refresh_token among them", regs[0]["grant_types"])
	}
	if len(auths) != 1 || len(tokens) != 1 {
		t.Fatalf("authorization requests %d, token requests %d", len(auths), len(tokens))
	}
	a := auths[0]
	if a.Get("response_type") != "code" || a.Get("code_challenge_method") != "S256" || a.Get("code_challenge") == "" || a.Get("state") == "" {
		t.Errorf("authorization request = %v", a)
	}
	if a.Get("resource") != rs.url() || tokens[0].form.Get("resource") != rs.url() {
		t.Errorf("resource: authorization %q, token %q, want %q", a.Get("resource"), tokens[0].form.Get("resource"), rs.url())
	}
	if a.Has("scope") {
		t.Errorf("scope sent with nothing to select it from: %q", a.Get("scope"))
	}
	if tokens[0].form.Get("redirect_uri") != testRedirectURI || tokens[0].form.Get("client_id") == "" || tokens[0].basic != "" {
		t.Errorf("token request = %v (basic %q)", tokens[0].form, tokens[0].basic)
	}
	s := auth.Session()
	if s.AccessToken == "" || s.Issuer != as.issuer() || s.Resource != rs.url() {
		t.Errorf("session = %+v", s)
	}
	if len(sessions) == 0 || sessions[len(sessions)-1].AccessToken != s.AccessToken {
		t.Errorf("OnSession did not see the token")
	}

	// A second client sharing the provider reuses the token: no new authorization.
	if _, err := listTools(t, rs.url(), auth); err != nil {
		t.Fatalf("second ListTools: %v", err)
	}
	if ta.calls != 1 {
		t.Errorf("authorizer called again for a valid token")
	}
}

// TestAuthorizationCode_TokenEndpointAuthMethods covers client authentication at the token endpoint, as the
// authorization server advertises and the registration response says.
func TestAuthorizationCode_TokenEndpointAuthMethods(t *testing.T) {
	for _, method := range []string{"client_secret_basic", "client_secret_post", "none"} {
		t.Run(method, func(t *testing.T) {
			as := newTestAS(t, "")
			as.authMethods = []string{method}
			as.registeredMethod = method
			if method != "none" {
				as.registeredSecret = "s3cret"
			}
			rs := newTestRS(t, as.issuer())
			auth, _ := newTestAuth(t, AuthorizationCodeConfig{})
			if _, err := listTools(t, rs.url(), auth); err != nil {
				t.Fatalf("ListTools: %v", err)
			}
			_, tokens, regs := as.snapshot()
			if regs[0]["token_endpoint_auth_method"] != method {
				t.Errorf("registered with %v, want %s", regs[0]["token_endpoint_auth_method"], method)
			}
			tr := tokens[0]
			switch method {
			case "client_secret_basic":
				if !strings.HasSuffix(tr.basic, ":s3cret") || tr.form.Has("client_secret") || tr.form.Has("client_id") {
					t.Errorf("basic: header %q, form %v", tr.basic, tr.form)
				}
			case "client_secret_post":
				if tr.basic != "" || tr.form.Get("client_secret") != "s3cret" || tr.form.Get("client_id") == "" {
					t.Errorf("post: header %q, form %v", tr.basic, tr.form)
				}
			case "none":
				if tr.basic != "" || tr.form.Has("client_secret") || tr.form.Get("client_id") == "" {
					t.Errorf("none: header %q, form %v", tr.basic, tr.form)
				}
			}
		})
	}
}

// TestAuthorizationCode_ClientIdentity covers the order of client identities: pre-registered credentials (only for
// their own authorization server), then a Client ID Metadata Document, then dynamic registration, and the failure when
// none is possible.
func TestAuthorizationCode_ClientIdentity(t *testing.T) {
	t.Run("pre-registered", func(t *testing.T) {
		as := newTestAS(t, "")
		as.noRegistration = true
		as.authMethods = []string{"client_secret_basic"}
		rs := newTestRS(t, as.issuer())
		auth, _ := newTestAuth(t, AuthorizationCodeConfig{ClientID: "pre id", ClientSecret: "pre:secret", ClientIssuer: as.issuer()})
		if _, err := listTools(t, rs.url(), auth); err != nil {
			t.Fatalf("ListTools: %v", err)
		}
		auths, tokens, _ := as.snapshot()
		if auths[0].Get("client_id") != "pre id" {
			t.Errorf("client_id = %q", auths[0].Get("client_id"))
		}
		// OAuth 2.1 section 2.4.1: both are form-urlencoded before Basic encoding.
		if tokens[0].basic != "pre+id:pre%3Asecret" {
			t.Errorf("basic credentials = %q", tokens[0].basic)
		}
	})
	t.Run("pre-registered for another server", func(t *testing.T) {
		as := newTestAS(t, "")
		rs := newTestRS(t, as.issuer())
		auth, _ := newTestAuth(t, AuthorizationCodeConfig{ClientID: "pre", ClientIssuer: "https://other.example.com"})
		if _, err := listTools(t, rs.url(), auth); err != nil {
			t.Fatalf("ListTools: %v", err)
		}
		auths, _, regs := as.snapshot()
		if len(regs) != 1 || auths[0].Get("client_id") == "pre" {
			t.Errorf("credentials for another server were used: client_id %q, registrations %d", auths[0].Get("client_id"), len(regs))
		}
	})
	t.Run("client ID metadata document", func(t *testing.T) {
		as := newTestAS(t, "")
		as.cimd = true
		rs := newTestRS(t, as.issuer())
		const cimd = "https://app.example.com/client.json"
		auth, _ := newTestAuth(t, AuthorizationCodeConfig{ClientMetadataURL: cimd})
		if _, err := listTools(t, rs.url(), auth); err != nil {
			t.Fatalf("ListTools: %v", err)
		}
		auths, tokens, regs := as.snapshot()
		if len(regs) != 0 || auths[0].Get("client_id") != cimd || tokens[0].form.Get("client_id") != cimd {
			t.Errorf("registrations %d, client_id %q / %q", len(regs), auths[0].Get("client_id"), tokens[0].form.Get("client_id"))
		}
	})
	t.Run("no identity possible", func(t *testing.T) {
		as := newTestAS(t, "")
		as.noRegistration = true
		rs := newTestRS(t, as.issuer())
		auth, ta := newTestAuth(t, AuthorizationCodeConfig{ClientMetadataURL: "https://app.example.com/client.json"})
		_, err := listTools(t, rs.url(), auth)
		if !errors.Is(err, ErrAuthorizationFailed) || !strings.Contains(err.Error(), "no client identity") {
			t.Fatalf("err = %v", err)
		}
		if ta.calls != 0 {
			t.Errorf("user sent to authorize without a client identity")
		}
	})
}

// TestAuthorizationCode_ScopeSelection covers the scope selection strategy: the challenge's scope, else the protected
// resource metadata's scopes_supported, else no scope parameter; and offline_access only when the authorization
// server lists it.
func TestAuthorizationCode_ScopeSelection(t *testing.T) {
	cases := []struct {
		name      string
		challenge string
		prm       []string
		asScopes  []string
		skipOff   bool
		want      string // "-" for no scope parameter
	}{
		{name: "from challenge", challenge: "mcp:basic", prm: []string{"mcp:read", "mcp:write"}, want: "mcp:basic"},
		{name: "from scopes_supported", prm: []string{"mcp:read", "mcp:write"}, want: "mcp:read mcp:write"},
		{name: "omitted", want: "-"},
		{name: "offline_access listed", prm: []string{"mcp:basic"}, asScopes: []string{"mcp:basic", "offline_access"}, want: "mcp:basic offline_access"},
		{name: "offline_access not listed", prm: []string{"mcp:basic"}, asScopes: []string{"mcp:basic"}, want: "mcp:basic"},
		{name: "offline_access skipped", prm: []string{"mcp:basic"}, asScopes: []string{"offline_access"}, skipOff: true, want: "mcp:basic"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			as := newTestAS(t, "")
			as.scopesSupported = c.asScopes
			rs := newTestRS(t, as.issuer())
			rs.set(func(rs *testRS) { rs.challenge, rs.scopes = c.challenge, c.prm })
			auth, _ := newTestAuth(t, AuthorizationCodeConfig{SkipOfflineAccess: c.skipOff})
			if _, err := listTools(t, rs.url(), auth); err != nil {
				t.Fatalf("ListTools: %v", err)
			}
			auths, _, _ := as.snapshot()
			got := auths[0].Get("scope")
			if !auths[0].Has("scope") {
				got = "-"
			}
			if got != c.want {
				t.Errorf("scope = %q, want %q", got, c.want)
			}
		})
	}
}

// TestAuthorizationCode_StepUp covers a 403 insufficient_scope: the user authorizes again for the challenged scope
// together with the scope already granted, and the request is retried.
func TestAuthorizationCode_StepUp(t *testing.T) {
	as := newTestAS(t, "")
	rs := newTestRS(t, as.issuer())
	rs.set(func(rs *testRS) {
		rs.challenge = "mcp:basic"
		rs.scopeFor = func(method string) []string {
			if method == "tools/call" {
				return []string{"mcp:write"}
			}
			return []string{"mcp:basic"}
		}
	})
	auth, ta := newTestAuth(t, AuthorizationCodeConfig{})
	ctx := context.Background()
	c := NewClient(rs.url(), auth, "")
	defer c.Close()
	if _, err := c.ListTools(ctx); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if _, err := c.CallTool(ctx, "echo", nil); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	auths, _, regs := as.snapshot()
	if ta.calls != 2 || len(auths) != 2 {
		t.Fatalf("authorizations = %d, want 2", len(auths))
	}
	if auths[0].Get("scope") != "mcp:basic" || auths[1].Get("scope") != "mcp:basic mcp:write" {
		t.Errorf("scopes = %q then %q", auths[0].Get("scope"), auths[1].Get("scope"))
	}
	if len(regs) != 1 {
		t.Errorf("re-registered for step-up: %d registrations", len(regs))
	}
}

// TestAuthorizationCode_InsufficientScopeLoop covers a server whose scope demand is never satisfied: the client stops
// instead of sending the user round again and again.
func TestAuthorizationCode_InsufficientScopeLoop(t *testing.T) {
	as := newTestAS(t, "")
	rs := newTestRS(t, as.issuer())
	// The server needs a scope it never names: its 403s ask for mcp:admin,
	// which the token already has.
	rs.set(func(rs *testRS) {
		rs.challenge, rs.forbidScope = "mcp:admin", "mcp:admin"
		rs.scopeFor = func(string) []string { return []string{"mcp:admin", "mcp:never"} }
	})
	auth, ta := newTestAuth(t, AuthorizationCodeConfig{})
	_, err := listTools(t, rs.url(), auth)
	if !errors.Is(err, ErrAuthorizationFailed) {
		t.Fatalf("err = %v, want ErrAuthorizationFailed", err)
	}
	if ta.calls != 1 || !strings.Contains(err.Error(), "already been granted") {
		t.Errorf("authorizer called %d times (want 1), err %v", ta.calls, err)
	}
}

// TestAuthorizationCode_RetryLimit covers a server that keeps refusing fresh tokens with 401: one request is retried
// at most maxAuthorizationRetries times, and the refusal is reported.
func TestAuthorizationCode_RetryLimit(t *testing.T) {
	as := newTestAS(t, "")
	rs := newTestRS(t, as.issuer())
	rs.set(func(rs *testRS) { rs.accept = func(string) bool { return false } })
	auth, ta := newTestAuth(t, AuthorizationCodeConfig{})
	c := NewClient(rs.url(), auth, "")
	defer c.Close()
	req, _ := http.NewRequest(http.MethodPost, rs.url(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	resp, err := c.doHTTP(req)
	if err != nil {
		t.Fatalf("doHTTP: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want the final 401", resp.StatusCode)
	}
	if ta.calls != maxAuthorizationRetries {
		t.Errorf("authorizer called %d times, want %d", ta.calls, maxAuthorizationRetries)
	}
}

// TestAuthorizationCode_Iss covers RFC 9207 validation of the authorization response's iss: exact string comparison
// with the recorded issuer whether or not it was advertised, and required when advertised. A rejected response never
// reaches the token endpoint.
func TestAuthorizationCode_Iss(t *testing.T) {
	cases := []struct {
		name      string
		advertise bool
		iss       string
		ok        bool
	}{
		{"supported", true, "correct", true},
		{"not advertised, absent", false, "omit", true},
		{"not advertised, present", false, "correct", true},
		{"advertised, missing", true, "omit", false},
		{"wrong issuer", true, "wrong", false},
		{"unexpected wrong issuer", false, "wrong", false},
		{"normalized", true, "normalized", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			as := newTestAS(t, "")
			as.advertiseIss, as.issInRedirect = c.advertise, c.iss
			rs := newTestRS(t, as.issuer())
			auth, _ := newTestAuth(t, AuthorizationCodeConfig{})
			_, err := listTools(t, rs.url(), auth)
			_, tokens, _ := as.snapshot()
			if c.ok {
				if err != nil {
					t.Fatalf("ListTools: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrAuthorizationFailed) || !strings.Contains(err.Error(), "iss") {
				t.Fatalf("err = %v, want an iss rejection", err)
			}
			if len(tokens) != 0 {
				t.Errorf("code sent to the token endpoint after a rejected response")
			}
		})
	}
}

// TestAuthorizationCode_AuthorizationResponse covers the rest of the authorization response checks: state, an
// error response, a redirect elsewhere, and a failing Authorizer.
func TestAuthorizationCode_AuthorizationResponse(t *testing.T) {
	as := newTestAS(t, "")
	rs := newTestRS(t, as.issuer())
	redirectWith := func(mutate func(q url.Values)) Authorizer {
		ta := &testAuthorizer{}
		return func(ctx context.Context, authorizationURL string) (string, error) {
			redirect, err := ta.authorize(ctx, authorizationURL)
			if err != nil {
				return "", err
			}
			u, _ := url.Parse(redirect)
			q := u.Query()
			mutate(q)
			u.RawQuery = q.Encode()
			return u.String(), nil
		}
	}
	cases := []struct {
		name       string
		authorizer Authorizer
		want       string
	}{
		{"state mismatch", redirectWith(func(q url.Values) { q.Set("state", "forged") }), "state"},
		{"no code", redirectWith(func(q url.Values) { q.Del("code") }), "no code"},
		{"error response", redirectWith(func(q url.Values) {
			q.Del("code")
			q.Set("error", "access_denied")
			q.Set("error_description", "user said no")
		}), "access_denied: user said no"},
		{"other redirect", func(ctx context.Context, string string) (string, error) {
			return "https://elsewhere.example.com/callback?code=x", nil
		}, "not the redirect URI"},
		{"authorizer fails", func(context.Context, string) (string, error) { return "", errors.New("browser closed") }, "browser closed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			auth, _ := newTestAuth(t, AuthorizationCodeConfig{Authorizer: c.authorizer})
			_, err := listTools(t, rs.url(), auth)
			if !errors.Is(err, ErrAuthorizationFailed) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}

// TestAuthorizationCode_DiscoveryFailures covers metadata that must not be used: protected resource metadata for
// another resource, and authorization server metadata naming another issuer. The user is never sent to authorize.
func TestAuthorizationCode_DiscoveryFailures(t *testing.T) {
	t.Run("resource mismatch", func(t *testing.T) {
		as := newTestAS(t, "")
		rs := newTestRS(t, as.issuer())
		rs.set(func(rs *testRS) { rs.resource = "https://evil.example.com/mcp" })
		auth, ta := newTestAuth(t, AuthorizationCodeConfig{})
		_, err := listTools(t, rs.url(), auth)
		if !errors.Is(err, ErrAuthorizationFailed) || !strings.Contains(err.Error(), "another resource") {
			t.Fatalf("err = %v", err)
		}
		if ta.calls != 0 {
			t.Errorf("authorization started for a mismatched resource")
		}
	})
	t.Run("metadata issuer mismatch", func(t *testing.T) {
		as := newTestAS(t, "")
		as.metadataIssuer = "https://attacker.example.com"
		rs := newTestRS(t, as.issuer())
		auth, ta := newTestAuth(t, AuthorizationCodeConfig{})
		_, err := listTools(t, rs.url(), auth)
		if !errors.Is(err, ErrAuthorizationFailed) || !strings.Contains(err.Error(), "another issuer") {
			t.Fatalf("err = %v", err)
		}
		_, _, regs := as.snapshot()
		if ta.calls != 0 || len(regs) != 0 {
			t.Errorf("poisoned metadata was used")
		}
	})
	t.Run("authorization fails in the Modern probe", func(t *testing.T) {
		as := newTestAS(t, "")
		as.authError = "access_denied"
		rs := newTestRS(t, as.issuer())
		auth, ta := newTestAuth(t, AuthorizationCodeConfig{})
		c := NewClient(rs.url(), auth, "")
		defer c.Close()
		err := c.Initialize(context.Background())
		if !errors.Is(err, ErrAuthorizationFailed) {
			t.Fatalf("Initialize: %v", err)
		}
		if ta.calls != 1 {
			t.Errorf("authorizer called %d times: a failed authorization must not fall back to initialize and ask again", ta.calls)
		}
	})
}

// TestAuthorizationCode_Migration covers the server moving to another authorization server: the client re-discovers
// on the 401, registers with the new one, and never presents the old one's client id there.
func TestAuthorizationCode_Migration(t *testing.T) {
	as1 := newTestAS(t, "")
	as2 := newTestAS(t, "")
	rs := newTestRS(t, as1.issuer())
	auth, _ := newTestAuth(t, AuthorizationCodeConfig{})
	ctx := context.Background()
	c := NewClient(rs.url(), auth, "")
	defer c.Close()
	if _, err := c.ListTools(ctx); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	oldClient := auth.Session().ClientID

	as2Prefix := "tok-" + strings.TrimPrefix(as2.srv.URL, "http://127.0.0.1:")
	rs.set(func(rs *testRS) {
		rs.issuer = as2.issuer()
		rs.accept = func(token string) bool { return strings.HasPrefix(token, as2Prefix) }
	})
	if _, err := c.CallTool(ctx, "echo", nil); err != nil {
		t.Fatalf("CallTool after migration: %v", err)
	}
	auths, tokens, regs := as2.snapshot()
	if len(regs) != 1 {
		t.Errorf("registrations at the new server = %d, want 1", len(regs))
	}
	for _, a := range auths {
		if a.Get("client_id") == oldClient {
			t.Errorf("old client id presented to the new authorization server")
		}
	}
	for _, tr := range tokens {
		if tr.form.Get("client_id") == oldClient {
			t.Errorf("old client id presented to the new token endpoint")
		}
	}
	if s := auth.Session(); s.Issuer != as2.issuer() {
		t.Errorf("session issuer = %q", s.Issuer)
	}
}

// TestAuthorizationCode_Refresh covers refresh tokens: an expired token is refreshed (with the resource parameter)
// before it is sent, and a 401 for a token that can be refreshed refreshes instead of asking the user.
func TestAuthorizationCode_Refresh(t *testing.T) {
	as := newTestAS(t, "")
	as.issueRefreshToken = true
	as.scopesSupported = []string{"offline_access"}
	as.expiresIn = 1 // within tokenExpiryMargin: expired as soon as it is issued
	rs := newTestRS(t, as.issuer())
	auth, ta := newTestAuth(t, AuthorizationCodeConfig{})
	ctx := context.Background()
	c := NewClient(rs.url(), auth, "")
	defer c.Close()
	if _, err := c.ListTools(ctx); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if _, err := c.CallTool(ctx, "echo", nil); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	_, tokens, _ := as.snapshot()
	var refreshes int
	for _, tr := range tokens {
		if tr.form.Get("grant_type") == "refresh_token" {
			refreshes++
			if tr.form.Get("resource") != rs.url() || tr.form.Get("refresh_token") == "" {
				t.Errorf("refresh request = %v", tr.form)
			}
		}
	}
	if refreshes == 0 || ta.calls != 1 {
		t.Errorf("refreshes = %d, authorizations = %d; want refreshes and one authorization", refreshes, ta.calls)
	}

	// A revoked token with a working refresh token: refreshed on the 401.
	as.expiresIn = 3600
	revoked := auth.Session().AccessToken
	refreshesBefore := refreshes
	rs.set(func(rs *testRS) { rs.accept = func(token string) bool { return token != revoked } })
	if _, err := c.CallTool(ctx, "echo", nil); err != nil {
		t.Fatalf("CallTool after revocation: %v", err)
	}
	_, tokens, _ = as.snapshot()
	refreshes = 0
	for _, tr := range tokens {
		if tr.form.Get("grant_type") == "refresh_token" {
			refreshes++
		}
	}
	if refreshes <= refreshesBefore || ta.calls != 1 {
		t.Errorf("revoked token: refreshes %d -> %d, authorizations %d", refreshesBefore, refreshes, ta.calls)
	}
}

// TestAuthorizationCode_ResumeSession covers a saved session: given back as Session, its token is used without
// asking the user, and only for the resource it was issued for.
func TestAuthorizationCode_ResumeSession(t *testing.T) {
	as := newTestAS(t, "")
	rs := newTestRS(t, as.issuer())
	first, _ := newTestAuth(t, AuthorizationCodeConfig{})
	if _, err := listTools(t, rs.url(), first); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	saved := first.Session()
	data, _ := json.Marshal(saved)
	var restored OAuthSession
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	resumed, ta := newTestAuth(t, AuthorizationCodeConfig{Session: &restored})
	if _, err := listTools(t, rs.url(), resumed); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if ta.calls != 0 {
		t.Errorf("user asked again despite a saved session")
	}

	// Never to another server.
	if h, _ := resumed.authHeaderFor("https://other.example.com/mcp"); h != "" {
		t.Errorf("token offered to another server: %q", h)
	}
	if h, _ := resumed.authHeaderFor(rs.srv.URL + "/other"); h != "" {
		t.Errorf("token offered to another path on the host: %q", h)
	}
	if h, _ := resumed.authHeaderFor(rs.url()); h == "" {
		t.Errorf("token not offered to its own server")
	}
}

// TestAuthorizationCode_Legacy20250326 covers servers following the 2025-03-26 authorization spec, which publish no
// protected resource metadata: authorization server metadata at the root of the server's host, else the default
// endpoints /authorize, /token and /register there.
func TestAuthorizationCode_Legacy20250326(t *testing.T) {
	t.Run("metadata at the server's origin", func(t *testing.T) {
		as := newTestAS(t, "")
		rs := newTestRS(t, as.issuer())
		rs.set(func(rs *testRS) { rs.noPRM = true })
		// The MCP server is its own authorization server: serve both from one host.
		mux := http.NewServeMux()
		mux.Handle("/mcp", rs.srv.Config.Handler)
		var self *httptest.Server
		mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                           self.URL,
				"authorization_endpoint":           as.issuer() + "/authorize",
				"token_endpoint":                   as.issuer() + "/token",
				"registration_endpoint":            as.issuer() + "/register",
				"code_challenge_methods_supported": []string{"S256"},
			})
		})
		self = httptest.NewServer(mux)
		defer self.Close()
		as.advertiseIss, as.issInRedirect = false, "omit"
		auth, _ := newTestAuth(t, AuthorizationCodeConfig{})
		if _, err := listTools(t, self.URL+"/mcp", auth); err != nil {
			t.Fatalf("ListTools: %v", err)
		}
		if _, _, regs := as.snapshot(); len(regs) != 1 {
			t.Errorf("registrations = %d", len(regs))
		}
	})
	t.Run("default endpoints", func(t *testing.T) {
		as := newTestAS(t, "")
		rs := newTestRS(t, as.issuer())
		rs.set(func(rs *testRS) { rs.noPRM = true })
		mux := http.NewServeMux()
		mux.Handle("/mcp", rs.srv.Config.Handler)
		mux.HandleFunc("/authorize", as.authorize)
		mux.HandleFunc("/token", as.token)
		mux.HandleFunc("/register", as.register)
		self := httptest.NewServer(mux)
		defer self.Close()
		as.issInRedirect = "omit"
		auth, _ := newTestAuth(t, AuthorizationCodeConfig{})
		if _, err := listTools(t, self.URL+"/mcp", auth); err != nil {
			t.Fatalf("ListTools: %v", err)
		}
		auths, tokens, regs := as.snapshot()
		if len(regs) != 1 || len(auths) != 1 || len(tokens) != 1 {
			t.Errorf("registrations %d, authorizations %d, tokens %d", len(regs), len(auths), len(tokens))
		}
		if auths[0].Get("code_challenge_method") != "S256" {
			t.Errorf("no PKCE on the default endpoints")
		}
	})
	t.Run("no fallback when metadata fails otherwise", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
		mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) { t.Error("default endpoint used") })
		self := httptest.NewServer(mux)
		defer self.Close()
		auth, ta := newTestAuth(t, AuthorizationCodeConfig{})
		if _, err := listTools(t, self.URL+"/mcp", auth); !errors.Is(err, ErrAuthorizationFailed) {
			t.Fatalf("err = %v", err)
		}
		if ta.calls != 0 {
			t.Errorf("authorizer called")
		}
	})
}

// TestAuthorizationCode_Concurrent covers concurrent requests all refused at once: they share one authorization.
func TestAuthorizationCode_Concurrent(t *testing.T) {
	as := newTestAS(t, "")
	rs := newTestRS(t, as.issuer())
	auth, ta := newTestAuth(t, AuthorizationCodeConfig{})
	c := NewClient(rs.url(), auth, "")
	defer c.Close()
	ctx := context.Background()
	if err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	// Drop the token: every request below is refused until one re-authorizes.
	auth.mu.Lock()
	auth.session.AccessToken = ""
	auth.mu.Unlock()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.CallTool(ctx, "echo", nil); err != nil {
				t.Errorf("CallTool: %v", err)
			}
		}()
	}
	wg.Wait()
	if ta.calls != 2 {
		t.Errorf("authorizer called %d times, want 2 (the first connection and one shared re-authorization)", ta.calls)
	}
}

// TestNewAuthorizationCodeAuth_Validation covers the required configuration.
func TestNewAuthorizationCodeAuth_Validation(t *testing.T) {
	if _, err := NewAuthorizationCodeAuth(AuthorizationCodeConfig{RedirectURI: testRedirectURI}); err == nil {
		t.Error("no Authorizer accepted")
	}
	if _, err := NewAuthorizationCodeAuth(AuthorizationCodeConfig{Authorizer: (&testAuthorizer{}).authorize}); err == nil {
		t.Error("no RedirectURI accepted")
	}
}

// TestRequestToken_BasicEncoding covers client_secret_basic's form-urlencoding of the id and secret.
func TestRequestToken_BasicEncoding(t *testing.T) {
	var got string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"access_token":"a","token_type":"bearer"}`)
	}))
	defer ts.Close()
	_, err := requestToken(context.Background(), ts.Client(), ts.URL, url.Values{"grant_type": {"x"}},
		clientCredentials{clientID: "a b", clientSecret: "c/d", authMethod: authMethodSecretBasic})
	if err != nil {
		t.Fatal(err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("a+b:c%2Fd"))
	if got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
}
