package mcp

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// BearerTokenAuth implements simple bearer token authentication
type BearerTokenAuth struct {
	token string
}

// NewBearerTokenAuth creates a new bearer token auth provider
func NewBearerTokenAuth(token string) *BearerTokenAuth {
	return &BearerTokenAuth{token: token}
}

func (b *BearerTokenAuth) GetAuthHeader() (string, error) {
	return fmt.Sprintf("Bearer %s", b.token), nil
}

func (b *BearerTokenAuth) Refresh() error {
	return nil // No refresh needed for static tokens
}

// OAuth2Auth implements OAuth2 authentication with token refresh
type OAuth2Auth struct {
	token  *oauth2.Token
	config *clientcredentials.Config
	mu     sync.RWMutex
}

// NewOAuth2Auth creates a new OAuth2 auth provider
func NewOAuth2Auth(clientID, clientSecret, tokenURL string, scopes []string) *OAuth2Auth {
	cfg := &clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     tokenURL,
		Scopes:       scopes,
		AuthStyle:    oauth2.AuthStyleInHeader,
	}

	return &OAuth2Auth{
		config: cfg,
	}
}

func (o *OAuth2Auth) GetAuthHeader() (string, error) {
	o.mu.RLock()
	token := o.token
	o.mu.RUnlock()

	if token == nil || !token.Valid() {
		if err := o.Refresh(); err != nil {
			return "", err
		}
		o.mu.RLock()
		token = o.token
		o.mu.RUnlock()
	}

	return fmt.Sprintf("Bearer %s", token.AccessToken), nil
}

func (o *OAuth2Auth) Refresh() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), DefaultOAuthRefreshTimeout)
	defer cancel()

	token, err := o.config.Token(ctx)
	if err != nil {
		return fmt.Errorf("failed to refresh token: %w", err)
	}

	o.token = token
	return nil
}
