package mcp

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// TestJWTSessionManager_GetProtocolVersion covers the happy path plus the
// malformed-token error paths, none of which any other test reaches directly.
func TestJWTSessionManager_GetProtocolVersion(t *testing.T) {
	sm := NewJWTSessionManager([]byte("a-32-byte-or-longer-signing-key!"), time.Hour)

	sessionID, err := sm.CreateSession(context.Background(), "2025-06-18", false)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	version, err := sm.GetProtocolVersion(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("GetProtocolVersion: %v", err)
	}
	if version != "2025-06-18" {
		t.Errorf("GetProtocolVersion() = %q, want 2025-06-18", version)
	}

	// Malformed token: wrong number of segments.
	if _, err := sm.GetProtocolVersion(context.Background(), "not.a.valid.jwt.token"); err == nil {
		t.Error("expected error for malformed token (wrong segment count)")
	}
	if _, err := sm.GetProtocolVersion(context.Background(), "onlyonepart"); err == nil {
		t.Error("expected error for malformed token (no dots)")
	}

	// Malformed token: valid shape but invalid base64 in the claims segment.
	if _, err := sm.GetProtocolVersion(context.Background(), "header.not-valid-base64!!!.sig"); err == nil {
		t.Error("expected error for invalid base64 claims segment")
	}
}

// TestJWTSessionManager_GetShowAll_ErrorPaths covers GetShowAll's malformed
// token branches (ValidateSession's equivalents are covered by
// TestJWTSessionManager_ValidateSession, but GetShowAll has its own decode
// logic).
func TestJWTSessionManager_GetShowAll_ErrorPaths(t *testing.T) {
	sm := NewJWTSessionManager([]byte("a-32-byte-or-longer-signing-key!"), time.Hour)

	if _, err := sm.GetShowAll(context.Background(), "not-a-jwt"); err == nil {
		t.Error("expected error for malformed token (wrong segment count)")
	}
	if _, err := sm.GetShowAll(context.Background(), "header.not-valid-base64!!!.sig"); err == nil {
		t.Error("expected error for invalid base64 claims segment")
	}

	// Valid session with show-all true.
	sessionID, err := sm.CreateSession(context.Background(), "2025-06-18", true)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	showAll, err := sm.GetShowAll(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("GetShowAll: %v", err)
	}
	if !showAll {
		t.Error("expected showAll to be true")
	}
}

// TestJWTSessionManager_ValidateSession_ErrorPaths covers ValidateSession's
// malformed-token, bad-signature, invalid-base64, invalid-JSON, and expired
// branches, most of which no other test reaches (existing tests only check
// the happy path indirectly via HTTP).
func TestJWTSessionManager_ValidateSession_ErrorPaths(t *testing.T) {
	sm := NewJWTSessionManager([]byte("a-32-byte-or-longer-signing-key!"), time.Hour)

	cases := []struct {
		name  string
		token string
	}{
		{"wrong segment count", "abc.def"},
		{"too many segments", "a.b.c.d"},
		{"invalid base64 claims", "aGVhZGVy.not-valid-base64!!!.c2ln"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			valid, err := sm.ValidateSession(context.Background(), tc.token)
			if err != nil {
				t.Fatalf("ValidateSession should not itself error, got %v", err)
			}
			if valid {
				t.Errorf("token %q should be invalid", tc.token)
			}
		})
	}

	// Tampered signature.
	sessionID, err := sm.CreateSession(context.Background(), "2025-06-18", false)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	parts := strings.Split(sessionID, ".")
	tampered := parts[0] + "." + parts[1] + ".tampered-signature"
	if valid, _ := sm.ValidateSession(context.Background(), tampered); valid {
		t.Error("tampered signature should be invalid")
	}

	// Invalid JSON in the claims segment (valid base64, but not valid JSON).
	badClaims := base64.RawURLEncoding.EncodeToString([]byte("not-json"))
	badToken := parts[0] + "." + badClaims + "." + sm.sign(parts[0]+"."+badClaims)
	if valid, _ := sm.ValidateSession(context.Background(), badToken); valid {
		t.Error("invalid JSON claims should be invalid")
	}

	// Expired token.
	expiredSM := NewJWTSessionManager([]byte("a-32-byte-or-longer-signing-key!"), -1*time.Hour)
	expiredID, err := expiredSM.CreateSession(context.Background(), "2025-06-18", false)
	if err != nil {
		t.Fatalf("CreateSession (expired): %v", err)
	}
	if valid, _ := expiredSM.ValidateSession(context.Background(), expiredID); valid {
		t.Error("expired token should be invalid")
	}
}

// TestJWTSessionManager_DeleteSession_NoOp covers DeleteSession, a
// deliberate no-op for stateless JWT sessions (tokens can't be revoked before
// expiry). This documents the contract: the session remains valid afterward.
func TestJWTSessionManager_DeleteSession_NoOp(t *testing.T) {
	sm := NewJWTSessionManager([]byte("a-32-byte-or-longer-signing-key!"), time.Hour)
	sessionID, err := sm.CreateSession(context.Background(), "2025-06-18", false)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := sm.DeleteSession(context.Background(), sessionID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	// JWT sessions cannot be revoked: still valid after "deletion".
	valid, err := sm.ValidateSession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("ValidateSession after delete: %v", err)
	}
	if !valid {
		t.Error("JWT session should remain valid after DeleteSession (stateless, cannot revoke)")
	}
}

// TestJWTSessionManager_CleanupExpiredSessions_NoOp covers
// CleanupExpiredSessions, also a deliberate no-op for JWT sessions (they
// self-expire via the exp claim).
func TestJWTSessionManager_CleanupExpiredSessions_NoOp(t *testing.T) {
	sm := NewJWTSessionManager([]byte("a-32-byte-or-longer-signing-key!"), time.Hour)
	if err := sm.CleanupExpiredSessions(context.Background(), time.Minute); err != nil {
		t.Errorf("CleanupExpiredSessions: %v, want nil", err)
	}
}

// TestServer_CleanupExpiredSessions covers Server.CleanupExpiredSessions: the
// no-session-manager no-op, and delegation to a configured manager.
func TestServer_CleanupExpiredSessions(t *testing.T) {
	s := NewServer("test", "1.0")

	// No session manager configured: no-op, no error.
	if err := s.CleanupExpiredSessions(time.Minute); err != nil {
		t.Errorf("CleanupExpiredSessions with no manager = %v, want nil", err)
	}

	// With a manager configured, delegates through.
	mockSM := newMockSessionManager()
	s.SetSessionManager(mockSM)
	if _, err := mockSM.CreateSession(context.Background(), "2025-06-18", false); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s.CleanupExpiredSessions(-1 * time.Nanosecond); err != nil {
		t.Fatalf("CleanupExpiredSessions: %v", err)
	}
	if len(mockSM.sessions) != 0 {
		t.Errorf("expected the session to be cleaned up, still have %d", len(mockSM.sessions))
	}
}
