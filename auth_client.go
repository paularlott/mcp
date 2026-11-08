package mcp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// ErrAuthorizationFailed is wrapped by every error from the OAuth authorization an AuthorizationCodeAuth runs when a
// server refuses a request: discovery, client registration, the user's authorization, or the token request.
var ErrAuthorizationFailed = errors.New("authorization failed")

// Authorizer is the user-agent step of the OAuth authorization code flow. It sends the user to authorizationURL,
// typically by opening it in a browser, waits for the authorization server to redirect back to the client's redirect
// URI, and returns that redirect URL, whose query carries the authorization response (code, state and iss, or error).
// It may block for as long as the user takes; ctx is the request that needed authorization.
type Authorizer func(ctx context.Context, authorizationURL string) (redirectURL string, err error)

// AuthorizationCodeConfig configures NewAuthorizationCodeAuth.
type AuthorizationCodeConfig struct {
	// RedirectURI is where the authorization server sends the user back: an https URL, a loopback http URL (a native
	// app listening locally) or a private-use scheme. Required.
	RedirectURI string

	// Authorizer runs the user-agent step. Required.
	Authorizer Authorizer

	// ClientName is the client_name sent with dynamic client registration; "mcp-client" when empty.
	ClientName string

	// ClientID and ClientSecret are pre-registered client credentials, used in preference to any other client
	// identity. ClientSecret is empty for a public client.
	ClientID     string
	ClientSecret string

	// ClientIssuer is the issuer of the authorization server ClientID was registered with: the credentials are only
	// sent to that authorization server, since client credentials are not portable between authorization servers.
	// Empty sends them to whichever authorization server the MCP server names.
	ClientIssuer string

	// ClientMetadataURL is the https URL of this client's Client ID Metadata Document. When the authorization server
	// advertises client_id_metadata_document_supported, it is the client_id, and no registration is needed.
	ClientMetadataURL string

	// TokenEndpointAuthMethod forces the token endpoint authentication method (client_secret_basic,
	// client_secret_post or none); empty picks one the authorization server supports.
	TokenEndpointAuthMethod string

	// SkipOfflineAccess stops the client from asking for the offline_access scope when the authorization server
	// lists it, for a client that does not want refresh tokens.
	SkipOfflineAccess bool

	// HTTPClient is used for discovery, registration and token requests; http.DefaultClient when nil.
	HTTPClient *http.Client

	// Session resumes an earlier authorization (one OnSession saw), so the user is not asked again.
	Session *OAuthSession

	// OnSession, when set, is called with the new state each time the session changes (a new or refreshed token, a new
	// client registration), so it can be saved and given back as Session. It must not call back into the provider.
	OnSession func(OAuthSession)
}

// OAuthSession is an AuthorizationCodeAuth's state with one MCP server: the authorization server it uses, the client
// identity it has there, and the tokens it holds for the server. It contains secrets: store it as such.
type OAuthSession struct {
	Issuer                  string    `json:"issuer,omitempty"`
	TokenEndpoint           string    `json:"token_endpoint"`
	Resource                string    `json:"resource"`
	ClientID                string    `json:"client_id"`
	ClientSecret            string    `json:"client_secret,omitempty"`
	TokenEndpointAuthMethod string    `json:"token_endpoint_auth_method,omitempty"`
	AccessToken             string    `json:"access_token,omitempty"`
	RefreshToken            string    `json:"refresh_token,omitempty"`
	Expiry                  time.Time `json:"expiry,omitzero"`
	Scope                   string    `json:"scope,omitempty"`
}

// AuthorizationCodeAuth is an AuthProvider that runs MCP authorization (the OAuth 2.1 authorization code flow with
// PKCE) when a server asks for it. Give it to NewClient: the first request goes out without a token; when the server
// answers 401, it discovers the server's authorization server, obtains a client identity there, has the user authorize
// through the Authorizer, and the Client retries the request with the token. A 403 insufficient_scope answer asks the
// user again for the extra scope (step-up authorization); an expired token is refreshed when there is a refresh token.
//
// A token is only ever sent to the MCP server it was issued for: the resource its protected resource metadata names.
type AuthorizationCodeAuth struct {
	cfg    AuthorizationCodeConfig
	client *http.Client

	mu      sync.Mutex // held for the whole of a flow, so concurrent requests share one authorization
	session OAuthSession
}

// NewAuthorizationCodeAuth creates an AuthorizationCodeAuth. It fails when RedirectURI or Authorizer is missing.
func NewAuthorizationCodeAuth(cfg AuthorizationCodeConfig) (*AuthorizationCodeAuth, error) {
	if cfg.RedirectURI == "" || cfg.Authorizer == nil {
		return nil, errors.New("authorization code flow needs a RedirectURI and an Authorizer")
	}
	a := &AuthorizationCodeAuth{cfg: cfg, client: clientCheckingRedirects(cfg.HTTPClient)}
	if cfg.Session != nil {
		a.session = *cfg.Session
	}
	return a, nil
}

// Session returns a copy of the current session.
func (a *AuthorizationCodeAuth) Session() OAuthSession {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.session
}

// GetAuthHeader returns the Bearer header for the current access token, refreshing it first when it has expired, or
// "" when there is none yet. A Client asks through authHeaderFor instead, which also checks the token is for it.
func (a *AuthorizationCodeAuth) GetAuthHeader() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.currentHeader(context.Background()), nil
}

// authHeaderFor is GetAuthHeader for a request to serverURL: "" when the token was issued for another resource, so a
// provider shared between clients never leaks one server's token to another (the MCP authorization spec's token
// audience binding). Without a header the server answers 401 and authorization starts for serverURL.
func (a *AuthorizationCodeAuth) authHeaderFor(serverURL string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !resourceCoversURL(a.session.Resource, serverURL) {
		return "", nil
	}
	return a.currentHeader(context.Background()), nil
}

// tokenExpiryMargin refreshes a token this long before it expires, so it does not expire in flight.
const tokenExpiryMargin = 30 * time.Second

// currentHeader is the Bearer header for the session's token, refreshed first when it has expired; "" when there is
// no usable token. Called with a.mu held.
func (a *AuthorizationCodeAuth) currentHeader(ctx context.Context) string {
	if a.session.AccessToken == "" {
		return ""
	}
	if !a.session.Expiry.IsZero() && time.Now().Add(tokenExpiryMargin).After(a.session.Expiry) {
		if a.refresh(ctx) != nil {
			return "" // the server's 401 will start a new authorization
		}
	}
	return "Bearer " + a.session.AccessToken
}

// resourceCoversURL reports whether resource, the identifier a token was issued for, covers the MCP server at
// serverURL: the same origin, and a path that is the server's or one of its parents (protected resource metadata at
// the host root may name the whole host).
func resourceCoversURL(resource, serverURL string) bool {
	if resource == "" {
		return false
	}
	u, err := url.Parse(strings.TrimSuffix(serverURL, "/"))
	if err != nil {
		return false
	}
	return coversResource(resource, u, true)
}

// respondToChallenge is called by the Client when serverURL answered a request status 401 or 403 with challenges (its
// WWW-Authenticate values), the request having carried sentAuthorization. It reports whether the request should be
// retried with a fresh authHeaderFor:
//
//   - another request has already replaced the token this one carried: retry with the new one;
//   - 401: the token was missing, expired or revoked. Discovery runs again, since the server may have moved to
//     another authorization server; at the same one an expired token is refreshed when possible, otherwise the user
//     authorizes again;
//   - 403 with error="insufficient_scope": step-up authorization for the challenged scope together with those
//     already requested, unless every challenged scope has already been granted, when asking again would loop;
//   - any other 403 is not an authorization problem: no retry.
//
// The MCP authorization spec's retry limit is the Client's: it retries a request at most maxAuthorizationRetries times.
func (a *AuthorizationCodeAuth) respondToChallenge(ctx context.Context, serverURL string, status int, challenges []string, sentAuthorization string) (bool, error) {
	var params map[string]string
	if bearer := bearerChallenges(challenges); len(bearer) > 0 {
		params = bearer[0]
	}
	if status == http.StatusForbidden && params["error"] != "insufficient_scope" {
		return false, nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if current := a.currentHeader(ctx); current != "" && current != sentAuthorization && resourceCoversURL(a.session.Resource, serverURL) {
		return true, nil
	}

	meta, err := a.discover(ctx, serverURL, challenges)
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrAuthorizationFailed, err)
	}

	// Client credentials and tokens belong to one authorization server: when
	// the server names another (or another resource), none of them carry
	// over, and the client registers afresh (SEP-2352).
	if a.session.Issuer != meta.Issuer || a.session.TokenEndpoint != meta.TokenEndpoint || a.session.Resource != meta.Resource {
		a.session = OAuthSession{}
	}

	challenged := strings.Fields(meta.ChallengeScope)
	if status == http.StatusForbidden && a.session.AccessToken != "" && len(challenged) > 0 && isSubset(challenged, strings.Fields(a.session.Scope)) {
		return false, fmt.Errorf("%w: the server requires scope %q, which has already been granted", ErrAuthorizationFailed, meta.ChallengeScope)
	}
	if status == http.StatusUnauthorized && a.session.RefreshToken != "" && isSubset(challenged, strings.Fields(a.session.Scope)) {
		if a.refresh(ctx) == nil {
			return true, nil
		}
	}

	if err := a.authorize(ctx, meta); err != nil {
		return false, fmt.Errorf("%w: %w", ErrAuthorizationFailed, err)
	}
	return true, nil
}

// discover finds the authorization server for serverURL from its challenge (see discoverOAuthMeta). A server that
// publishes no metadata at all gets the MCP 2025-03-26 default endpoints: /authorize, /token and /register at the
// root of its host, with PKCE S256 assumed, as that revision requires of every client.
func (a *AuthorizationCodeAuth) discover(ctx context.Context, serverURL string, challenges []string) (*OAuthMeta, error) {
	meta, unpublished, err := discoverOAuthMeta(ctx, a.client, serverURL, append([]string{}, challenges...))
	if err == nil || !unpublished {
		return meta, err
	}
	u, perr := url.Parse(strings.TrimSuffix(serverURL, "/"))
	if perr != nil || checkFetchURL(u) != nil {
		return nil, err
	}
	scope := ""
	for _, params := range bearerChallenges(challenges) {
		if scope = params["scope"]; scope != "" {
			break
		}
	}
	return &OAuthMeta{
		AuthorizationEndpoint:         originURL(u, "/authorize"),
		TokenEndpoint:                 originURL(u, "/token"),
		RegistrationEndpoint:          originURL(u, "/register"),
		CodeChallengeMethodsSupported: []string{"S256"},
		Resource:                      originURL(u, u.Path),
		ChallengeScope:                scope,
	}, nil
}

// authorize runs the authorization code flow with meta's authorization server and stores the token in the session.
// Called with a.mu held.
func (a *AuthorizationCodeAuth) authorize(ctx context.Context, meta *OAuthMeta) error {
	if err := a.ensureClient(ctx, meta); err != nil {
		return err
	}
	scopes := a.selectScopes(meta)

	verifier := oauth2.GenerateVerifier()
	state, err := randomState()
	if err != nil {
		return err
	}
	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {a.session.ClientID},
		"redirect_uri":          {a.cfg.RedirectURI},
		"state":                 {state},
		"code_challenge":        {oauth2.S256ChallengeFromVerifier(verifier)},
		"code_challenge_method": {"S256"},
	}
	if len(scopes) > 0 {
		query.Set("scope", strings.Join(scopes, " "))
	}
	// RFC 8707: the resource parameter, exactly as the protected resource
	// metadata published it, binds the token to this MCP server.
	if meta.Resource != "" {
		query.Set("resource", meta.Resource)
	}
	authURL, err := url.Parse(meta.AuthorizationEndpoint)
	if err != nil {
		return fmt.Errorf("authorization_endpoint %q: %w", meta.AuthorizationEndpoint, err)
	}
	if authURL.RawQuery != "" {
		authURL.RawQuery += "&" + query.Encode()
	} else {
		authURL.RawQuery = query.Encode()
	}

	redirect, err := a.cfg.Authorizer(ctx, authURL.String())
	if err != nil {
		return fmt.Errorf("authorizer: %w", err)
	}
	code, err := a.authorizationCode(redirect, state, meta)
	if err != nil {
		return err
	}

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {a.cfg.RedirectURI},
		"code_verifier": {verifier},
	}
	if meta.Resource != "" {
		form.Set("resource", meta.Resource)
	}
	token, err := requestToken(ctx, a.client, meta.TokenEndpoint, form, a.credentials())
	if err != nil {
		return fmt.Errorf("token request: %w", err)
	}
	if err := checkTokenType(token); err != nil {
		return err
	}
	a.session.AccessToken = token.accessToken
	a.session.RefreshToken = token.refreshToken
	a.session.Expiry = token.expiry
	// RFC 6749 section 5.1: a response without scope granted what was asked.
	a.session.Scope = token.scope
	if a.session.Scope == "" {
		a.session.Scope = strings.Join(scopes, " ")
	}
	a.changed()
	return nil
}

// authorizationCode checks the authorization response in redirect and returns its code. The state must be the one
// sent (CSRF protection). RFC 9207: an iss parameter must be exactly the authorization server's issuer, compared as
// strings without normalization, whether or not the server advertised it; an authorization server that advertises
// authorization_response_iss_parameter_supported must send one. Both are checked before the response is otherwise
// used, error responses included, so a mix-up attack cannot feed the client another server's response.
func (a *AuthorizationCodeAuth) authorizationCode(redirect, state string, meta *OAuthMeta) (string, error) {
	ru, err := url.Parse(redirect)
	if err != nil {
		return "", fmt.Errorf("authorization response: %w", err)
	}
	if want, err := url.Parse(a.cfg.RedirectURI); err == nil &&
		(!strings.EqualFold(ru.Scheme, want.Scheme) || !strings.EqualFold(ru.Host, want.Host) || ru.Path != want.Path) {
		return "", fmt.Errorf("authorization response came back to %s, not the redirect URI", redirect)
	}
	resp := ru.Query()
	if resp.Get("state") != state {
		return "", errors.New("authorization response state does not match the request")
	}
	if resp.Has("iss") {
		if meta.Issuer != "" && resp.Get("iss") != meta.Issuer {
			return "", fmt.Errorf("authorization response iss %q is not the authorization server's issuer %q", resp.Get("iss"), meta.Issuer)
		}
	} else if meta.AuthorizationResponseIssParameterSupported {
		return "", fmt.Errorf("authorization response has no iss, which %s says it sends", meta.Issuer)
	}
	if e := resp.Get("error"); e != "" {
		if d := resp.Get("error_description"); d != "" {
			return "", fmt.Errorf("authorization refused: %s: %s", e, d)
		}
		return "", fmt.Errorf("authorization refused: %s", e)
	}
	code := resp.Get("code")
	if code == "" {
		return "", errors.New("authorization response has no code")
	}
	return code, nil
}

// checkTokenType accepts a Bearer token (case-insensitively, RFC 6749 section 5.1), the only kind this client can
// present; a response without token_type is taken to be one.
func checkTokenType(token *tokenResponse) error {
	if token.tokenType != "" && !strings.EqualFold(token.tokenType, "Bearer") {
		return fmt.Errorf("token request: unsupported token_type %q", token.tokenType)
	}
	return nil
}

// ensureClient gives the session a client identity at meta's authorization server, in the order the MCP
// authorization spec gives: pre-registered credentials for that server; else a Client ID Metadata Document when the
// server supports them; else dynamic client registration. Called with a.mu held.
func (a *AuthorizationCodeAuth) ensureClient(ctx context.Context, meta *OAuthMeta) error {
	if a.session.ClientID != "" {
		return nil // the session's, for this same authorization server
	}
	var creds clientCredentials
	switch {
	case a.cfg.ClientID != "" && (a.cfg.ClientIssuer == "" || a.cfg.ClientIssuer == meta.Issuer):
		creds = clientCredentials{clientID: a.cfg.ClientID, clientSecret: a.cfg.ClientSecret, authMethod: a.cfg.TokenEndpointAuthMethod}
		if creds.authMethod == "" {
			creds.authMethod = tokenAuthMethod(meta, creds.clientSecret != "")
		}
	case a.cfg.ClientMetadataURL != "" && meta.ClientIDMetadataDocumentSupported:
		creds = clientCredentials{clientID: a.cfg.ClientMetadataURL, authMethod: authMethodNone}
	case meta.RegistrationEndpoint != "":
		name := a.cfg.ClientName
		if name == "" {
			name = mcpClientName
		}
		registered, err := registerClient(ctx, a.client, meta.RegistrationEndpoint, clientRegistration{
			name:            name,
			redirectURI:     a.cfg.RedirectURI,
			applicationType: applicationTypeFor(a.cfg.RedirectURI),
			authMethod:      a.registrationAuthMethod(meta),
		})
		if err != nil {
			return err
		}
		creds = *registered
	default:
		issuer := meta.Issuer
		if issuer == "" {
			issuer = meta.AuthorizationEndpoint
		}
		return fmt.Errorf("no client identity for %s: it offers neither dynamic client registration nor Client ID Metadata Documents, and no pre-registered client is configured for it", issuer)
	}
	a.session.Issuer = meta.Issuer
	a.session.TokenEndpoint = meta.TokenEndpoint
	a.session.Resource = meta.Resource
	a.session.ClientID = creds.clientID
	a.session.ClientSecret = creds.clientSecret
	a.session.TokenEndpointAuthMethod = creds.authMethod
	a.changed()
	return nil
}

// registrationAuthMethod is the token_endpoint_auth_method to register with: the configured one, else none (a public
// client using PKCE, as MCP clients usually are) when the server supports it or does not say, else a secret-based
// method it supports.
func (a *AuthorizationCodeAuth) registrationAuthMethod(meta *OAuthMeta) string {
	if a.cfg.TokenEndpointAuthMethod != "" {
		return a.cfg.TokenEndpointAuthMethod
	}
	supported := meta.TokenEndpointAuthMethodsSupported
	if len(supported) == 0 || slices.Contains(supported, authMethodNone) {
		return authMethodNone
	}
	return tokenAuthMethod(meta, true)
}

// tokenAuthMethod picks how a client authenticates to meta's token endpoint: none without a secret; with one,
// client_secret_basic or client_secret_post as the server advertises, client_secret_basic when it does not say (RFC
// 8414's default), and none when none is all it supports.
func tokenAuthMethod(meta *OAuthMeta, hasSecret bool) string {
	supported := meta.TokenEndpointAuthMethodsSupported
	switch {
	case !hasSecret:
		return authMethodNone
	case len(supported) == 0 || slices.Contains(supported, authMethodSecretBasic):
		return authMethodSecretBasic
	case slices.Contains(supported, authMethodSecretPost):
		return authMethodSecretPost
	case slices.Contains(supported, authMethodNone):
		return authMethodNone
	}
	return authMethodSecretBasic
}

// selectScopes is the scope to ask for, by the MCP authorization spec's scope selection strategy: the challenge's
// scope, else the protected resource metadata's scopes_supported, else none (the parameter is omitted). On
// re-authorization it is united with the scope already held (SEP-2350), so step-up does not lose earlier grants; and
// offline_access is added when the authorization server lists it, to get a refresh token (SEP-2207), never when it
// does not.
func (a *AuthorizationCodeAuth) selectScopes(meta *OAuthMeta) []string {
	scopes := strings.Fields(meta.ChallengeScope)
	if len(scopes) == 0 {
		scopes = append(scopes, meta.ResourceScopesSupported...)
	}
	scopes = unionScopes(strings.Fields(a.session.Scope), scopes)
	if !a.cfg.SkipOfflineAccess && slices.Contains(meta.ScopesSupported, "offline_access") {
		scopes = unionScopes(scopes, []string{"offline_access"})
	}
	return scopes
}

// refresh replaces the session's access token using its refresh token, with the resource parameter (RFC 8707). When the
// authorization server refuses the refresh (an OAuth error response, such as invalid_grant: RFC 6749 section 5.2) or
// returns an unusable token, the tokens are dropped, so the next challenge authorizes afresh. A transient failure (a
// network error, a timeout, a 5xx) keeps them, so an outage at the token endpoint doesn't cost the user a new
// authorization. Called with a.mu held.
func (a *AuthorizationCodeAuth) refresh(ctx context.Context) error {
	if a.session.RefreshToken == "" {
		return errors.New("no refresh token")
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {a.session.RefreshToken}}
	if a.session.Resource != "" {
		form.Set("resource", a.session.Resource)
	}
	// Bounded: a token endpoint that hangs must not hold every request.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	token, err := requestToken(ctx, a.client, a.session.TokenEndpoint, form, a.credentials())
	if err != nil {
		var refused *oauth2.RetrieveError
		if errors.As(err, &refused) && refused.Response != nil &&
			refused.Response.StatusCode >= 400 && refused.Response.StatusCode < 500 {
			a.dropTokens()
		}
		return err
	}
	if err := checkTokenType(token); err != nil {
		a.dropTokens()
		return err
	}
	a.session.AccessToken = token.accessToken
	if token.refreshToken != "" { // RFC 6749 section 6: a new refresh token replaces the old one
		a.session.RefreshToken = token.refreshToken
	}
	a.session.Expiry = token.expiry
	if token.scope != "" {
		a.session.Scope = token.scope
	}
	a.changed()
	return nil
}

// dropTokens forgets the session's tokens, keeping its client identity. Called with a.mu held.
func (a *AuthorizationCodeAuth) dropTokens() {
	a.session.AccessToken, a.session.RefreshToken, a.session.Expiry = "", "", time.Time{}
	a.changed()
}

// credentials is the session's client identity for token requests.
func (a *AuthorizationCodeAuth) credentials() clientCredentials {
	return clientCredentials{clientID: a.session.ClientID, clientSecret: a.session.ClientSecret, authMethod: a.session.TokenEndpointAuthMethod}
}

// changed reports the session to OnSession. Called with a.mu held.
func (a *AuthorizationCodeAuth) changed() {
	if a.cfg.OnSession != nil {
		a.cfg.OnSession(a.session)
	}
}

// randomState is an unguessable state parameter.
func randomState() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// unionScopes is a followed by the scopes of b it lacks.
func unionScopes(a, b []string) []string {
	out := append([]string(nil), a...)
	for _, s := range b {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// isSubset reports whether every scope in want is in have.
func isSubset(want, have []string) bool {
	for _, s := range want {
		if !slices.Contains(have, s) {
			return false
		}
	}
	return true
}
