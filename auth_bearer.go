package mcp

import "fmt"

// BearerTokenAuth implements simple static bearer token authentication.
type BearerTokenAuth struct {
	token string
}

func NewBearerTokenAuth(token string) *BearerTokenAuth {
	return &BearerTokenAuth{token: token}
}

func (b *BearerTokenAuth) GetAuthHeader() (string, error) {
	return fmt.Sprintf("Bearer %s", b.token), nil
}

func (b *BearerTokenAuth) Refresh() error { return nil }
