package mcp

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockSessionManager is a test implementation of SessionManager
// It demonstrates that session management is pluggable
type mockSessionManager struct {
	mu             sync.RWMutex
	sessions       map[string]*mockSession
	createCalled   int
	validateCalled int
	deleteCalled   int
}

type mockSession struct {
	protocolVersion string
	showAll         bool
	createdAt       time.Time
}

func newMockSessionManager() *mockSessionManager {
	return &mockSessionManager{
		sessions: make(map[string]*mockSession),
	}
}

func (m *mockSessionManager) CreateSession(ctx context.Context, protocolVersion string, showAll bool) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createCalled++
	sessionID := "mock-session-" + time.Now().Format("20060102150405.000000")
	m.sessions[sessionID] = &mockSession{
		protocolVersion: protocolVersion,
		showAll:         showAll,
		createdAt:       time.Now(),
	}
	return sessionID, nil
}

func (m *mockSessionManager) ValidateSession(ctx context.Context, sessionID string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	m.validateCalled++
	_, exists := m.sessions[sessionID]
	return exists, nil
}

func (m *mockSessionManager) GetProtocolVersion(ctx context.Context, sessionID string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session, exists := m.sessions[sessionID]
	if !exists {
		return "", nil
	}
	return session.protocolVersion, nil
}

func (m *mockSessionManager) GetShowAll(ctx context.Context, sessionID string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session, exists := m.sessions[sessionID]
	if !exists {
		return false, nil
	}
	return session.showAll, nil
}

func (m *mockSessionManager) DeleteSession(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteCalled++
	delete(m.sessions, sessionID)
	return nil
}

func (m *mockSessionManager) CleanupExpiredSessions(ctx context.Context, maxIdleTime time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().Add(-maxIdleTime)
	for id, session := range m.sessions {
		if session.createdAt.Before(cutoff) {
			delete(m.sessions, id)
		}
	}
	return nil
}

// Verify mockSessionManager implements SessionManager
var _ SessionManager = (*mockSessionManager)(nil)

// TestPluggableSessionManager verifies that custom session managers can be plugged in
func TestPluggableSessionManager(t *testing.T) {
	server := NewServer("test", "1.0.0")
	mockSM := newMockSessionManager()

	// Use SetSessionManager to plug in our custom implementation
	server.SetSessionManager(mockSM)

	// Register a simple tool
	server.RegisterTool(
		NewTool("test_tool", "A test tool"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("success"), nil
		},
	)

	// Initialize a session
	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(initBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.HandleRequest(w, req)

	if w.Code != 200 {
		t.Fatalf("Initialize failed: %d - %s", w.Code, w.Body.String())
	}

	// Check that our mock session manager was called
	if mockSM.createCalled != 1 {
		t.Errorf("Expected CreateSession to be called once, got %d", mockSM.createCalled)
	}

	// Session ID is returned in the MCP-Session-Id header
	sessionID := w.Header().Get("MCP-Session-Id")
	if sessionID == "" {
		t.Fatal("Expected session ID in MCP-Session-Id header")
	}

	// Verify our mock stored the session
	mockSM.mu.RLock()
	_, exists := mockSM.sessions[sessionID]
	mockSM.mu.RUnlock()
	if !exists {
		t.Error("Session was not stored in mock session manager")
	}

	t.Logf("Custom session manager successfully created session: %s", sessionID)
}

// TestPluggableSessionManager_Revocation verifies custom session managers can revoke sessions
func TestPluggableSessionManager_Revocation(t *testing.T) {
	server := NewServer("test", "1.0.0")
	mockSM := newMockSessionManager()
	server.SetSessionManager(mockSM)

	// Create a session directly
	sessionID, err := mockSM.CreateSession(context.Background(), "2025-03-26", false)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Verify session exists
	valid, _ := mockSM.ValidateSession(context.Background(), sessionID)
	if !valid {
		t.Error("Session should be valid")
	}

	// Delete the session (revoke it)
	err = mockSM.DeleteSession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("Failed to delete session: %v", err)
	}

	// Verify session no longer exists
	valid, _ = mockSM.ValidateSession(context.Background(), sessionID)
	if valid {
		t.Error("Session should be invalid after deletion")
	}

	if mockSM.deleteCalled != 1 {
		t.Errorf("Expected DeleteSession to be called once, got %d", mockSM.deleteCalled)
	}
}

// TestNewJWTSessionManagerWithAutoKey verifies the auto-key convenience function
func TestNewJWTSessionManagerWithAutoKey(t *testing.T) {
	sm, err := NewJWTSessionManagerWithAutoKey(1 * time.Hour)
	if err != nil {
		t.Fatalf("NewJWTSessionManagerWithAutoKey failed: %v", err)
	}

	if sm == nil {
		t.Fatal("Expected session manager to be created")
	}

	if sm.ttl != 1*time.Hour {
		t.Errorf("Expected TTL of 1 hour, got %v", sm.ttl)
	}

	if len(sm.signingKey) != 32 {
		t.Errorf("Expected 32-byte signing key, got %d bytes", len(sm.signingKey))
	}
}

// TestNewJWTSessionManager_WithExplicitKey verifies the explicit key constructor
func TestNewJWTSessionManager_WithExplicitKey(t *testing.T) {
	key := []byte("test-signing-key-that-is-long-enough")
	sm := NewJWTSessionManager(key, 2*time.Hour)

	if sm == nil {
		t.Fatal("Expected session manager to be created")
	}

	if sm.ttl != 2*time.Hour {
		t.Errorf("Expected TTL of 2 hours, got %v", sm.ttl)
	}

	if string(sm.signingKey) != string(key) {
		t.Error("Signing key mismatch")
	}
}

// TestSessionManager_ShowAllPreserved verifies that show-all mode is preserved across sessions
func TestSessionManager_ShowAllPreserved(t *testing.T) {
	server := NewServer("test", "1.0.0")
	mockSM := newMockSessionManager()
	server.SetSessionManager(mockSM)

	// Create a session with show-all mode
	sessionID, err := mockSM.CreateSession(context.Background(), "2025-03-26", true)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Verify show-all is preserved
	showAll, err := mockSM.GetShowAll(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("Failed to get show-all: %v", err)
	}

	if !showAll {
		t.Errorf("Expected showAll to be true")
	}
}

// TestSessionWithNativeAndDiscoveryTools verifies that both native and discovery tools
// work correctly within the same session, with mode determining visibility
func TestSessionWithNativeAndDiscoveryTools(t *testing.T) {
	server := NewServer("test", "1.0.0")

	// Use the new API: create JWT session manager and set it
	sm, err := NewJWTSessionManagerWithAutoKey(30 * time.Minute)
	if err != nil {
		t.Fatalf("Failed to create session manager: %v", err)
	}
	server.SetSessionManager(sm)

	// Register a native tool
	server.RegisterTool(
		NewTool("native_tool", "A native tool"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("native result"), nil
		},
		"native", "keyword1",
	)

	// Register a discoverable tool
	server.RegisterTool(
		NewTool("discoverable_tool", "A discoverable tool").Discoverable("discoverable", "keyword2"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("discoverable result"), nil
		},
	)

	// Initialize a normal mode session
	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(initBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.HandleRequest(w, req)

	if w.Code != 200 {
		t.Fatalf("Initialize failed: %d - %s", w.Code, w.Body.String())
	}

	normalSessionID := w.Header().Get("MCP-Session-Id")
	if normalSessionID == "" {
		t.Fatal("Expected session ID for normal mode session")
	}

	// List tools in normal mode session - should see native_tool + meta tools
	listBody := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	req = httptest.NewRequest("POST", "/mcp", strings.NewReader(listBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Session-Id", normalSessionID)
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")
	w = httptest.NewRecorder()
	server.HandleRequest(w, req)

	if w.Code != 200 {
		t.Fatalf("List tools in normal mode failed: %d - %s", w.Code, w.Body.String())
	}

	// Should see native_tool in normal mode
	if !strings.Contains(w.Body.String(), "native_tool") {
		t.Error("native_tool should be visible in normal mode")
	}

	// Initialize a show-all mode session
	initBody = `{"jsonrpc":"2.0","id":3,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`
	req = httptest.NewRequest("POST", "/mcp", strings.NewReader(initBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(ShowAllHeader, "true")
	w = httptest.NewRecorder()
	server.HandleRequest(w, req)

	if w.Code != 200 {
		t.Fatalf("Initialize with show-all mode failed: %d - %s", w.Code, w.Body.String())
	}

	showAllSessionID := w.Header().Get("MCP-Session-Id")
	if showAllSessionID == "" {
		t.Fatal("Expected session ID for show-all mode session")
	}

	if normalSessionID == showAllSessionID {
		t.Error("Expected different session IDs for different modes")
	}

	// List tools in show-all mode session - should see ALL tools including discoverable
	listBody = `{"jsonrpc":"2.0","id":4,"method":"tools/list"}`
	req = httptest.NewRequest("POST", "/mcp", strings.NewReader(listBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Session-Id", showAllSessionID)
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")
	w = httptest.NewRecorder()
	server.HandleRequest(w, req)

	if w.Code != 200 {
		t.Fatalf("List tools in show-all mode failed: %d - %s", w.Code, w.Body.String())
	}

	// In show-all mode, should see native_tool AND discoverable_tool (but not meta-tools)
	if !strings.Contains(w.Body.String(), `"name":"native_tool"`) {
		t.Error("native_tool should be visible in show-all mode")
	}
	if !strings.Contains(w.Body.String(), `"name":"discoverable_tool"`) {
		t.Error("discoverable_tool should be visible in show-all mode")
	}

	// Meta-tools should NOT appear in show-all mode
	if strings.Contains(w.Body.String(), "tool_search") {
		t.Error("tool_search should NOT be visible in show-all mode")
	}
	if strings.Contains(w.Body.String(), "execute_tool") {
		t.Error("execute_tool should NOT be visible in show-all mode")
	}

	t.Log("Successfully verified native and discoverable tools work within sessions")
}
