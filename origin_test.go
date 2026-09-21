package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newOriginTestServer builds a minimal server for exercising HandleRequest's
// Origin check without needing a full tool/resource setup.
func newOriginTestServer(t *testing.T) *Server {
	t.Helper()
	s := NewServer("origin-test", "1")
	s.RegisterTool(NewTool("ping", "pings"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("pong"), nil
	})
	return s
}

func postPing(t *testing.T, s *Server, origin string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rr := httptest.NewRecorder()
	s.HandleRequest(rr, req)
	return rr
}

// TestOriginValidation_NoHeaderAllowed is the regression test for the
// library's own server-to-server usage (this session's real deployments:
// llmrouter's *Client talking to a remote MCP server, this library's own
// *Client generally) never sending an Origin header at all — it must never
// be rejected, since that's not what the DNS-rebinding check is for.
func TestOriginValidation_NoHeaderAllowed(t *testing.T) {
	s := newOriginTestServer(t)
	rr := postPing(t, s, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a request with no Origin header: %s", rr.Code, rr.Body.String())
	}
}

// TestOriginValidation_LocalhostAllowedByDefault covers the default
// policy's local-dev-UI allowance across host spellings and schemes.
func TestOriginValidation_LocalhostAllowedByDefault(t *testing.T) {
	for _, origin := range []string{
		"http://localhost:3000",
		"http://127.0.0.1:8080",
		"http://[::1]:9000",
		"https://localhost:5173",
	} {
		t.Run(origin, func(t *testing.T) {
			s := newOriginTestServer(t)
			rr := postPing(t, s, origin)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 for %s: %s", rr.Code, origin, rr.Body.String())
			}
			if got := rr.Header().Get("Access-Control-Allow-Origin"); got != origin {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q (reflected, not a bare *, so a browser will actually accept the response)", got, origin)
			}
		})
	}
}

// TestOriginValidation_UntrustedOriginRejectedByDefault is the regression
// test for the reported gap: the server never checked Origin at all, so a
// malicious page's browser JS (DNS rebinding — resolving an attacker
// domain to 127.0.0.1 after the initial same-origin check) could reach a
// locally-running server. An arbitrary remote origin must now be rejected
// before any request processing happens.
func TestOriginValidation_UntrustedOriginRejectedByDefault(t *testing.T) {
	s := newOriginTestServer(t)
	rr := postPing(t, s, "https://evil.example.com")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for an untrusted Origin: %s", rr.Code, rr.Body.String())
	}
}

// TestOriginValidation_PreflightAlsoValidatesOrigin proves the OPTIONS
// preflight branch is covered too — the check runs before HandleRequest
// even looks at the method, but this pins that down explicitly since a
// preflight is the request a real browser sends first.
func TestOriginValidation_PreflightAlsoValidatesOrigin(t *testing.T) {
	s := newOriginTestServer(t)
	req := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rr := httptest.NewRecorder()
	s.HandleRequest(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for an untrusted Origin on OPTIONS: %s", rr.Code, rr.Body.String())
	}
}

// TestOriginValidation_AllowOrigins proves the escape hatch for an
// application that genuinely serves cross-origin browser JS straight at
// this server: AllowOrigins widens the default policy (localhost/no-header
// keep working) rather than replacing it.
func TestOriginValidation_AllowOrigins(t *testing.T) {
	s := newOriginTestServer(t)
	s.AllowOrigins("https://app.example.com")

	if rr := postPing(t, s, "https://app.example.com"); rr.Code != http.StatusOK {
		t.Errorf("allowlisted origin: status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if rr := postPing(t, s, "https://other.example.com"); rr.Code != http.StatusForbidden {
		t.Errorf("non-allowlisted origin: status = %d, want 403: %s", rr.Code, rr.Body.String())
	}
	if rr := postPing(t, s, "http://localhost:3000"); rr.Code != http.StatusOK {
		t.Errorf("default policy's localhost allowance: status = %d, want 200 (AllowOrigins must widen, not replace): %s", rr.Code, rr.Body.String())
	}
	if rr := postPing(t, s, ""); rr.Code != http.StatusOK {
		t.Errorf("no-header allowance: status = %d, want 200 (AllowOrigins must widen, not replace): %s", rr.Code, rr.Body.String())
	}
}

// TestOriginValidation_SetOriginValidatorReplacesPolicy proves
// SetOriginValidator fully replaces the default (unlike AllowOrigins),
// including turning off the built-in localhost allowance if the caller's
// own validator doesn't grant it.
func TestOriginValidation_SetOriginValidatorReplacesPolicy(t *testing.T) {
	s := newOriginTestServer(t)
	s.SetOriginValidator(func(origin string) bool {
		return origin == "https://only-this.example.com"
	})

	if rr := postPing(t, s, "https://only-this.example.com"); rr.Code != http.StatusOK {
		t.Errorf("custom-allowed origin: status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if rr := postPing(t, s, "http://localhost:3000"); rr.Code != http.StatusForbidden {
		t.Errorf("localhost: status = %d, want 403 — SetOriginValidator replaces the default, it doesn't widen it: %s", rr.Code, rr.Body.String())
	}

	s.SetOriginValidator(nil)
	if rr := postPing(t, s, "http://localhost:3000"); rr.Code != http.StatusOK {
		t.Errorf("after SetOriginValidator(nil): status = %d, want 200 (default policy restored)", rr.Code)
	}
}
