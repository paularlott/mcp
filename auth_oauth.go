package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// OAuthMeta holds the OAuth2 server metadata discovered via RFC 8414.
type OAuthMeta struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RegistrationEndpoint  string `json:"registration_endpoint"`
}

// DiscoverOAuthMeta fetches OAuth2 server metadata from the MCP server URL
// by probing RFC 8414 and OIDC well-known endpoints.
func DiscoverOAuthMeta(ctx context.Context, serverURL string) (*OAuthMeta, error) {
	base := strings.TrimSuffix(serverURL, "/")
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/.well-known/openid-configuration"} {
		probe := *u
		probe.Path = path
		probe.RawQuery = ""
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, probe.String(), nil)
		if err != nil {
			continue
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			if resp != nil {
				resp.Body.Close()
			}
			continue
		}
		var meta OAuthMeta
		err = json.NewDecoder(resp.Body).Decode(&meta)
		resp.Body.Close()
		if err == nil && meta.AuthorizationEndpoint != "" && meta.TokenEndpoint != "" {
			return &meta, nil
		}
	}
	return nil, fmt.Errorf("could not discover OAuth metadata from %s", serverURL)
}

// RegisterOAuthClient performs RFC 7591 dynamic client registration.
// Returns the issued client_id.
func RegisterOAuthClient(ctx context.Context, registrationEndpoint, clientName, redirectURI string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"client_name":                clientName,
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, registrationEndpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.ClientID == "" {
		return "", fmt.Errorf("dynamic client registration failed (status %d)", resp.StatusCode)
	}
	return result.ClientID, nil
}

// OAuth2Auth implements OAuth2 authentication backed by an oauth2.TokenSource.
// Supports both client credentials (machine-to-machine) and refresh token
// (user-delegated, e.g. PKCE) flows.
type OAuth2Auth struct {
	source oauth2.TokenSource
	token  *oauth2.Token
	mu     sync.RWMutex
}

// NewOAuth2Auth creates an OAuth2 provider using the client credentials flow.
func NewOAuth2Auth(clientID, clientSecret, tokenURL string, scopes []string) *OAuth2Auth {
	cfg := &clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     tokenURL,
		Scopes:       scopes,
		AuthStyle:    oauth2.AuthStyleInHeader,
	}
	return &OAuth2Auth{
		source: cfg.TokenSource(context.Background()),
	}
}

// NewOAuth2RefreshTokenAuth creates an OAuth2 provider from an existing
// access + refresh token pair (e.g. obtained via browser-based PKCE flow).
// clientID is the dynamically registered client ID. accessToken may be empty.
func NewOAuth2RefreshTokenAuth(tokenURL, clientID, accessToken, refreshToken string) *OAuth2Auth {
	cfg := &oauth2.Config{
		ClientID: clientID,
		Endpoint: oauth2.Endpoint{TokenURL: tokenURL, AuthStyle: oauth2.AuthStyleInParams},
	}
	initial := &oauth2.Token{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}
	return &OAuth2Auth{
		source: oauth2.ReuseTokenSource(initial, cfg.TokenSource(context.Background(), initial)),
	}
}

func (o *OAuth2Auth) GetAuthHeader() (string, error) {
	o.mu.RLock()
	token := o.token
	valid := token != nil && token.Valid()
	o.mu.RUnlock()

	if !valid {
		o.mu.Lock()
		if o.token == nil || !o.token.Valid() {
			t, err := o.source.Token()
			if err != nil {
				o.mu.Unlock()
				return "", fmt.Errorf("failed to get oauth2 token: %w", err)
			}
			o.token = t
		}
		token = o.token
		o.mu.Unlock()
	}

	return fmt.Sprintf("Bearer %s", token.AccessToken), nil
}

func (o *OAuth2Auth) Refresh() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	t, err := o.source.Token()
	if err != nil {
		return fmt.Errorf("failed to refresh oauth2 token: %w", err)
	}
	o.token = t
	return nil
}
