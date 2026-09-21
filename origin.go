package mcp

import (
	"net/url"
	"strings"
)

// OriginValidator reports whether origin — the exact value of an incoming
// request's Origin header — may access this server. Set via
// [Server.SetOriginValidator]; see [Server.AllowOrigins] for the common
// case of just adding specific origins on top of the default policy rather
// than replacing it outright.
type OriginValidator func(origin string) bool

// defaultOriginValidator implements the Streamable HTTP transport's MUST to
// validate Origin — the defense against DNS rebinding: a malicious page's
// JavaScript, running in a victim's browser, fetching a server that trusts
// anything reaching it over localhost or an internal network — with a
// policy chosen to need no configuration for how this library is actually
// used in practice:
//
//   - No Origin header at all is always allowed. Origin is a header only
//     browsers send; every server-to-server MCP client (including this
//     library's own [Client]) and virtually every non-browser client never
//     sends one, so it's not what this check is protecting against.
//   - A browser request from http(s)://localhost, http(s)://127.0.0.1, or
//     http(s)://[::1] (any port — https included, since a local dev setup
//     using a locally-trusted cert, e.g. via mkcert, is common enough that
//     rejecting it would be an arbitrary restriction with no security
//     benefit: the host is what makes this safe, not the scheme) is
//     allowed.
//   - Anything else is rejected.
//
// An application that serves real cross-origin browser JavaScript straight
// at this server (uncommon — the MCP Apps pattern this library supports is
// the browser talking to its OWN backend, which then reaches this server
// with an MCP [Client] server-to-server, never the browser calling it
// directly) must opt in via [Server.SetOriginValidator] or
// [Server.AllowOrigins].
func defaultOriginValidator(origin string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

// SetOriginValidator overrides how [Server.HandleRequest] decides whether
// to accept a request's Origin header, replacing [defaultOriginValidator]'s
// policy entirely. Pass nil to restore the default. See
// [Server.AllowOrigins] for the simpler common case of widening the default
// rather than replacing it.
func (s *Server) SetOriginValidator(v OriginValidator) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.originValidator = v
}

// AllowOrigins widens the current origin validator (the default policy,
// unless [Server.SetOriginValidator] was already called) to also accept
// requests whose Origin header exactly matches one of origins, e.g.
// "https://app.example.com". Call this when real browser JavaScript needs
// to reach this server directly from a specific origin — see
// [defaultOriginValidator]'s doc comment for why most deployments don't.
func (s *Server) AllowOrigins(origins ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	allowed := make(map[string]struct{}, len(origins))
	for _, o := range origins {
		allowed[strings.TrimSpace(o)] = struct{}{}
	}
	prev := s.originValidator
	if prev == nil {
		prev = defaultOriginValidator
	}
	s.originValidator = func(origin string) bool {
		if _, ok := allowed[origin]; ok {
			return true
		}
		return prev(origin)
	}
}

// originAllowed reports whether origin may access this server, per the
// currently configured [OriginValidator] (defaultOriginValidator unless
// [Server.SetOriginValidator]/[Server.AllowOrigins] changed it).
func (s *Server) originAllowed(origin string) bool {
	s.mu.RLock()
	v := s.originValidator
	s.mu.RUnlock()
	if v == nil {
		v = defaultOriginValidator
	}
	return v(origin)
}

// corsOriginHeader is the Access-Control-Allow-Origin value to send once
// origin has already passed originAllowed: the validated origin itself
// (required for a browser to actually accept the response — a literal "*"
// only satisfies non-credentialed simple requests), or "*" when there was
// no Origin header at all, since no browser is involved and the value is
// moot.
func corsOriginHeader(origin string) string {
	if origin == "" {
		return "*"
	}
	return origin
}
