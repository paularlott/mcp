package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Phase 1 regression tests: behaviors that drifted between duplicated code
// paths before they were unified.

// A tool re-registered from discoverable to native via the batch API must
// leave the search registry, exactly as the single-tool API does — the old
// batch path forgot the unregister, leaving a stale searchable entry.
func TestRegisterToolsReRegistrationLeavesSearchRegistry(t *testing.T) {
	s := NewServer("test", "1.0.0")

	newTool := func() *ToolBuilder {
		return NewTool("flip", "flips visibility")
	}

	s.RegisterTools(NewToolRegistration(
		newTool().Discoverable(),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("discoverable"), nil
		},
	))

	// The search path finds it while discoverable.
	resp, err := s.CallTool(context.Background(), ToolSearchName, map[string]any{"query": "flip"})
	if err != nil {
		t.Fatalf("tool_search: %v", err)
	}
	if !responseMentions(resp, "flip") {
		t.Fatal("discoverable tool must be findable via tool_search")
	}

	// Re-register as native through the batch API.
	s.RegisterTools(NewToolRegistration(
		newTool(),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("native"), nil
		},
	))

	// tools/list now shows it...
	found := false
	for _, tool := range s.ListToolsWithContext(context.Background()) {
		if tool.Name == "flip" {
			found = true
		}
	}
	if !found {
		t.Fatal("native re-registration must appear in tools/list")
	}

	// ...and it must no longer be registered as a discoverable duplicate:
	// hasDiscoverableTools is recalculated, so a server with no other
	// discoverable tools stops advertising tool_search/execute_tool.
	for _, tool := range s.ListToolsWithContext(context.Background()) {
		if tool.Name == ToolSearchName {
			t.Fatal("tool_search must not be advertised after the only discoverable tool became native")
		}
	}
}

// responseMentions checks whether any text content of a response contains s.
func responseMentions(resp *ToolResponse, s string) bool {
	for _, c := range resp.Content {
		if strings.Contains(c.Text, s) {
			return true
		}
	}
	return false
}

// stdio initialize must append the discovery hint when discoverable tools
// exist, matching the HTTP initialize — a stdio Legacy client used to never
// learn that tool_search/execute_tool exist.
func TestStdioInitializeAppendsDiscoveryInstructions(t *testing.T) {
	s := NewServer("test", "1.0.0")
	s.RegisterTool(
		NewTool("hidden", "a discoverable tool").Discoverable(),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("ok"), nil
		},
	)

	raw, err := json.Marshal(map[string]any{
		"protocolVersion": MCPProtocolVersionLatest,
		"capabilities":    map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.stdioInitialize(raw)
	if err != nil {
		t.Fatalf("stdioInitialize: %v", err)
	}
	initResult, ok := result.(initializeResult)
	if !ok {
		t.Fatalf("result type = %T", result)
	}
	if !strings.Contains(initResult.Instructions, ToolSearchName) {
		t.Fatalf("instructions must mention %s: %q", ToolSearchName, initResult.Instructions)
	}
}

// List-path JSON-RPC errors surface as *ToolError (errors.As-able) like the
// call paths, not as bare fmt.Errorf strings.
func TestListPathsReturnToolError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &req)

		w.Header().Set("Content-Type", "application/json")
		if req.Method == "tools/list" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error":   map[string]any{"code": -32000, "message": "boom"},
			})
			return
		}
		// Minimal Legacy initialize success so the handshake completes.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"protocolVersion": MCPProtocolVersionLatest,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "fake", "version": "1"},
			},
		})
	}))
	defer ts.Close()

	client := NewClient(ts.URL, nil, "")
	_, err := client.ListTools(context.Background())

	var toolErr *ToolError
	if !errors.As(err, &toolErr) {
		t.Fatalf("ListTools error must be *ToolError, got %T: %v", err, err)
	}
	if toolErr.Code != -32000 || toolErr.Message != "boom" {
		t.Fatalf("code/message = %d/%q", toolErr.Code, toolErr.Message)
	}
}
