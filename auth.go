package mcp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/oauth2"
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
	clientID     string
	clientSecret string
	tokenURL     string
	scopes       []string
	username     string
	password     string
	token        *oauth2.Token
	config       *oauth2.Config
	mu           sync.RWMutex
}

// NewOAuth2Auth creates a new OAuth2 auth provider
func NewOAuth2Auth(clientID, clientSecret, tokenURL string, scopes []string) *OAuth2Auth {
	config := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint: oauth2.Endpoint{
			TokenURL: tokenURL,
		},
		Scopes: scopes,
	}

	return &OAuth2Auth{
		clientID:     clientID,
		clientSecret: clientSecret,
		tokenURL:     tokenURL,
		scopes:       scopes,
		config:       config,
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	token, err := o.config.PasswordCredentialsToken(ctx, o.username, o.password)
	if err != nil {
		return fmt.Errorf("failed to refresh token: %w", err)
	}

	o.token = token
	return nil
}