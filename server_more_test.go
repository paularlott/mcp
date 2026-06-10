package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestCORSPreflight(t *testing.T) {
	s := NewServer("s", "1")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	http.HandlerFunc(s.HandleRequest).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	h := rr.Result().Header
	if h.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("no ACAO header")
	}
	if h.Get("Access-Control-Allow-Methods") == "" {
		t.Fatal("no methods header")
	}
}

func TestPing(t *testing.T) {
	s := NewServer("s", "1")
	body := MCPRequest{JSONRPC: "2.0", ID: 1, Method: "ping"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	http.HandlerFunc(s.HandleRequest).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var rpc MCPResponse
	_ = json.NewDecoder(rr.Body).Decode(&rpc)
	if rpc.Error != nil {
		t.Fatalf("unexpected error: %+v", rpc.Error)
	}
}

func TestInvalidJSONRPCVersion(t *testing.T) {
	s := NewServer("s", "1")
	payload := map[string]any{"jsonrpc": "1.0", "id": 1, "method": "ping"}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	http.HandlerFunc(s.HandleRequest).ServeHTTP(rr, req)
	var rpc MCPResponse
	_ = json.NewDecoder(rr.Body).Decode(&rpc)
	if rpc.Error == nil || rpc.Error.Code != ErrorCodeInvalidRequest {
		t.Fatalf("expected invalid request error, got %+v", rpc.Error)
	}
}

func TestUnknownMethod(t *testing.T) {
	s := NewServer("s", "1")
	body := MCPRequest{JSONRPC: "2.0", ID: 1, Method: "does/not/exist"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	http.HandlerFunc(s.HandleRequest).ServeHTTP(rr, req)
	var rpc MCPResponse
	_ = json.NewDecoder(rr.Body).Decode(&rpc)
	if rpc.Error == nil || rpc.Error.Code != ErrorCodeMethodNotFound {
		t.Fatalf("expected method not found, got %+v", rpc.Error)
	}
}

func TestToolErrorMapping(t *testing.T) {
	s := NewServer("s", "1")
	s.RegisterTool(NewTool("fail", "", String("x", "", Required())), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return nil, NewToolErrorInvalidParams("bad")
	})
	body := MCPRequest{JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: ToolCallParams{Name: "fail", Arguments: map[string]any{"x": "y"}}}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	http.HandlerFunc(s.HandleRequest).ServeHTTP(rr, req)
	var rpc MCPResponse
	_ = json.NewDecoder(rr.Body).Decode(&rpc)
	if rpc.Error == nil || rpc.Error.Code != ErrorCodeInvalidParams || rpc.Error.Message == "" {
		t.Fatalf("expected mapped tool error, got %+v", rpc.Error)
	}
}

func TestInstructionsInInitialize(t *testing.T) {
	s := NewServer("s", "1")
	s.SetInstructions("please do x")
	body := MCPRequest{JSONRPC: "2.0", ID: 1, Method: "initialize", Params: map[string]any{
		"capabilities": map[string]any{},
		"clientInfo":   map[string]any{"name": "n", "version": "v"},
	}}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	http.HandlerFunc(s.HandleRequest).ServeHTTP(rr, req)
	var rpc MCPResponse
	_ = json.NewDecoder(rr.Body).Decode(&rpc)
	res := rpc.Result.(map[string]any)
	if res["instructions"] != "please do x" {
		t.Fatalf("instructions missing: %+v", res)
	}
}

func TestMissingIDDefaultsToEmpty(t *testing.T) {
	s := NewServer("s", "1")
	// body without id
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  "ping",
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	http.HandlerFunc(s.HandleRequest).ServeHTTP(rr, req)
	// read raw json to assert id field is present and empty string
	data, _ := io.ReadAll(rr.Body)
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	if _, ok := out["id"]; !ok {
		t.Fatalf("id not present")
	}
	if out["id"] != "" {
		t.Fatalf("expected empty id, got %v", out["id"])
	}
}

func TestRegisterTools_BatchRegistration(t *testing.T) {
	s := NewServer("test", "1.0")

	// Register multiple tools in a batch
	s.RegisterTools(
		NewToolRegistration(
			NewTool("tool_a", "Description A"),
			func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
				return NewToolResponseText("a"), nil
			},
		),
		NewToolRegistration(
			NewTool("tool_b", "Description B"),
			func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
				return NewToolResponseText("b"), nil
			},
		),
		NewToolRegistration(
			NewTool("tool_c", "Description C"),
			func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
				return NewToolResponseText("c"), nil
			},
		),
	)

	tools := s.ListTools()
	if len(tools) != 3 {
		t.Fatalf("Expected 3 tools, got %d", len(tools))
	}

	// Verify sorted order
	if tools[0].Name != "tool_a" || tools[1].Name != "tool_b" || tools[2].Name != "tool_c" {
		t.Fatalf("Tools not in sorted order: %v", tools)
	}

	// Test calling the tools
	resp, err := s.CallTool(context.Background(), "tool_b", nil)
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if resp.Content[0].Text != "b" {
		t.Fatalf("Expected 'b', got %s", resp.Content[0].Text)
	}
}

func TestRegisterTool_MaintainsSortedOrder(t *testing.T) {
	s := NewServer("test", "1.0")

	// Register tools in non-alphabetical order
	s.RegisterTool(NewTool("zebra", "Z tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("z"), nil
	})
	s.RegisterTool(NewTool("alpha", "A tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("a"), nil
	})
	s.RegisterTool(NewTool("middle", "M tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("m"), nil
	})

	tools := s.ListTools()
	if len(tools) != 3 {
		t.Fatalf("Expected 3 tools, got %d", len(tools))
	}

	// Verify sorted order
	if tools[0].Name != "alpha" || tools[1].Name != "middle" || tools[2].Name != "zebra" {
		t.Fatalf("Tools not in sorted order: got %s, %s, %s", tools[0].Name, tools[1].Name, tools[2].Name)
	}
}

func TestRegisterTool_ReplacesExistingTool(t *testing.T) {
	s := NewServer("test", "1.0")

	// Register initial tool
	s.RegisterTool(NewTool("my_tool", "Original description"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("original"), nil
	})

	// Register replacement
	s.RegisterTool(NewTool("my_tool", "Updated description"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("updated"), nil
	})

	tools := s.ListTools()
	if len(tools) != 1 {
		t.Fatalf("Expected 1 tool after replacement, got %d", len(tools))
	}

	if tools[0].Description != "Updated description" {
		t.Fatalf("Expected updated description, got %s", tools[0].Description)
	}

	// Verify handler was replaced
	resp, _ := s.CallTool(context.Background(), "my_tool", nil)
	if resp.Content[0].Text != "updated" {
		t.Fatalf("Expected 'updated', got %s", resp.Content[0].Text)
	}
}

func TestUnregisterTool_NativeTool(t *testing.T) {
	s := NewServer("test", "1.0")

	s.RegisterTool(NewTool("alpha", "A tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("a"), nil
	})
	s.RegisterTool(NewTool("beta", "B tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("b"), nil
	})
	s.RegisterTool(NewTool("gamma", "G tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("g"), nil
	})

	if len(s.ListTools()) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(s.ListTools()))
	}

	removed := s.UnregisterTool("beta")
	if !removed {
		t.Fatal("expected UnregisterTool to return true")
	}

	tools := s.ListTools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools after unregister, got %d", len(tools))
	}
	if tools[0].Name != "alpha" || tools[1].Name != "gamma" {
		t.Fatalf("expected alpha and gamma, got %v", tools)
	}

	_, err := s.CallTool(context.Background(), "beta", nil)
	if err == nil {
		t.Fatal("expected error calling unregistered tool")
	}

	removed = s.UnregisterTool("beta")
	if removed {
		t.Fatal("expected UnregisterTool to return false for already-removed tool")
	}

	removed = s.UnregisterTool("nonexistent")
	if removed {
		t.Fatal("expected UnregisterTool to return false for nonexistent tool")
	}
}

func TestUnregisterTool_DiscoverableTool(t *testing.T) {
	s := NewServer("test", "1.0")

	s.RegisterTool(NewTool("native", "Native tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("native"), nil
	})
	s.RegisterTool(NewTool("hidden", "Hidden tool").Discoverable("secret"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("hidden"), nil
	})

	tools := s.ListTools()
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools (native + 2 discovery tools), got %d: %v", len(tools), tools)
	}

	removed := s.UnregisterTool("hidden")
	if !removed {
		t.Fatal("expected UnregisterTool to return true for discoverable tool")
	}

	tools = s.ListTools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 native tool after unregister (no more discovery tools), got %d: %v", len(tools), tools)
	}
	if tools[0].Name != "native" {
		t.Fatalf("expected native tool, got %s", tools[0].Name)
	}
}

func TestUnregisterTool_PreservesRemoteServerTools(t *testing.T) {
	remote := NewServer("remote", "1")
	remote.RegisterTool(NewTool("remote_tool", "A tool from a remote server", String("x", "x")), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("remote"), nil
	})
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer ts.Close()

	host := NewServer("host", "1")

	host.RegisterTool(NewTool("local_a", "Local A"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("a"), nil
	})
	host.RegisterTool(NewTool("local_b", "Local B"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("b"), nil
	})

	client := NewClient(ts.URL, nil, "ns")
	if err := host.RegisterRemoteServer(client); err != nil {
		t.Fatalf("register remote: %v", err)
	}

	tools := host.ListTools()
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools (2 local + 1 remote), got %d: %v", len(tools), tools)
	}

	removed := host.UnregisterTool("local_a")
	if !removed {
		t.Fatal("expected UnregisterTool to return true")
	}

	tools = host.ListTools()
	if len(tools) != 2 {
		names := make([]string, len(tools))
		for i, t := range tools {
			names[i] = t.Name
		}
		t.Fatalf("expected 2 tools (1 local + 1 remote) after unregister, got %d: %v", len(tools), names)
	}

	found := false
	for _, t := range tools {
		if t.Name == "ns__remote_tool" {
			found = true
		}
	}
	if !found {
		t.Fatal("remote server tool was lost after unregistering a local tool")
	}
}

func TestUnregisterTool_MixedTypes(t *testing.T) {
	remoteNative := NewServer("remote-native", "1")
	remoteNative.RegisterTool(NewTool("r_native", "Remote native tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("rn"), nil
	})
	tsNative := httptest.NewServer(http.HandlerFunc(remoteNative.HandleRequest))
	defer tsNative.Close()

	remoteDisc := NewServer("remote-disc", "1")
	remoteDisc.RegisterTool(NewTool("r_disc", "Remote discoverable tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("rd"), nil
	})
	tsDisc := httptest.NewServer(http.HandlerFunc(remoteDisc.HandleRequest))
	defer tsDisc.Close()

	host := NewServer("host", "1")

	host.RegisterTool(NewTool("local_native", "Local native"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("ln"), nil
	})
	host.RegisterTool(NewTool("local_disc", "Local discoverable").Discoverable("secret"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("ld"), nil
	})

	if err := host.RegisterRemoteServer(NewClient(tsNative.URL, nil, "rn")); err != nil {
		t.Fatalf("register remote native: %v", err)
	}
	if err := host.RegisterRemoteServerDiscoverable(NewClient(tsDisc.URL, nil, "rd")); err != nil {
		t.Fatalf("register remote disc: %v", err)
	}

	tools := host.ListTools()
	names := toolNames(tools)
	if len(tools) != 4 {
		t.Fatalf("expected 4 visible tools (1 local native + 1 remote native + 2 discovery tools), got %d: %v", len(tools), names)
	}

	assertHas := func(t *testing.T, names []string, name string) {
		t.Helper()
		for _, n := range names {
			if n == name {
				return
			}
		}
		t.Fatalf("expected %s in tools, got %v", name, names)
	}

	assertHas(t, names, "local_native")
	assertHas(t, names, "rn__r_native")

	if !host.UnregisterTool("local_native") {
		t.Fatal("expected true unregistering local_native")
	}
	tools = host.ListTools()
	names = toolNames(tools)
	assertHas(t, names, "rn__r_native")
	for _, n := range names {
		if n == "local_native" {
			t.Fatal("local_native should be gone")
		}
	}

	if !host.UnregisterTool("local_disc") {
		t.Fatal("expected true unregistering local_disc")
	}
	tools = host.ListTools()
	names = toolNames(tools)
	assertHas(t, names, "rn__r_native")
	assertHas(t, names, "tool_search")
	assertHas(t, names, "execute_tool")
}

func toolNames(tools []MCPTool) []string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	return names
}

func TestUnregisterRemoteServer_RemovesNativeTools(t *testing.T) {
	remote := NewServer("remote", "1")
	remote.RegisterTool(NewTool("r1", "Remote tool 1"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("r1"), nil
	})
	remote.RegisterTool(NewTool("r2", "Remote tool 2"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("r2"), nil
	})
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer ts.Close()

	host := NewServer("host", "1")
	host.RegisterTool(NewTool("local", "Local tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("local"), nil
	})

	client := NewClient(ts.URL, nil, "ns")
	if err := host.RegisterRemoteServer(client); err != nil {
		t.Fatalf("register remote: %v", err)
	}

	names := toolNames(host.ListTools())
	if len(names) != 3 {
		t.Fatalf("expected 3 tools, got %v", names)
	}
	assertToolExists(t, names, "local")
	assertToolExists(t, names, "ns__r1")
	assertToolExists(t, names, "ns__r2")

	host.UnregisterRemoteServer(client)

	names = toolNames(host.ListTools())
	if len(names) != 1 {
		t.Fatalf("expected 1 tool after unregister, got %v", names)
	}
	assertToolExists(t, names, "local")
}

func TestUnregisterRemoteServer_RemovesDiscoveryTools(t *testing.T) {
	remoteDisc := NewServer("remote-disc", "1")
	remoteDisc.RegisterTool(NewTool("rd", "Remote discoverable"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("rd"), nil
	})
	tsDisc := httptest.NewServer(http.HandlerFunc(remoteDisc.HandleRequest))
	defer tsDisc.Close()

	host := NewServer("host", "1")
	host.RegisterTool(NewTool("local", "Local tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("local"), nil
	})

	client := NewClient(tsDisc.URL, nil, "rd")
	if err := host.RegisterRemoteServerDiscoverable(client); err != nil {
		t.Fatalf("register remote disc: %v", err)
	}

	names := toolNames(host.ListTools())
	assertToolExists(t, names, "local")
	assertToolExists(t, names, "tool_search")
	assertToolExists(t, names, "execute_tool")

	host.UnregisterRemoteServer(client)

	names = toolNames(host.ListTools())
	if len(names) != 1 {
		t.Fatalf("expected only local tool after unregister, got %v", names)
	}
	assertToolExists(t, names, "local")
	for _, n := range names {
		if n == "tool_search" || n == "execute_tool" {
			t.Fatal("discovery tools should be gone after remote discoverable server removed")
		}
	}
}

func assertToolExists(t *testing.T, names []string, name string) {
	t.Helper()
	for _, n := range names {
		if n == name {
			return
		}
	}
	t.Fatalf("expected tool %s, got %v", name, names)
}

func assertToolMissing(t *testing.T, names []string, name string) {
	t.Helper()
	for _, n := range names {
		if n == name {
			t.Fatalf("expected tool %s to be missing, got %v", name, names)
		}
	}
}

func TestRemoteSearch_ShowsDiscoveryTools(t *testing.T) {
	remote := NewServer("remote", "1")
	remote.RegisterTool(NewTool("rt", "Remote tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("rt"), nil
	})
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer ts.Close()

	host := NewServer("host", "1")
	host.RegisterTool(NewTool("local", "Local tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("local"), nil
	})

	client := NewClient(ts.URL, nil, "ns")
	if err := host.RegisterRemoteServer(client, WithRemoteSearch()); err != nil {
		t.Fatalf("register remote with search: %v", err)
	}

	names := toolNames(host.ListTools())
	assertToolExists(t, names, "local")
	assertToolExists(t, names, "ns__rt")
	assertToolExists(t, names, "tool_search")
	assertToolExists(t, names, "execute_tool")
}

func TestRemoteSearch_DiscoveryToolsGoneAfterUnregister(t *testing.T) {
	remote := NewServer("remote", "1")
	remote.RegisterTool(NewTool("rt", "Remote tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("rt"), nil
	})
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer ts.Close()

	host := NewServer("host", "1")
	host.RegisterTool(NewTool("local", "Local tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("local"), nil
	})

	client := NewClient(ts.URL, nil, "ns")
	if err := host.RegisterRemoteServer(client, WithRemoteSearch()); err != nil {
		t.Fatalf("register remote with search: %v", err)
	}

	assertToolExists(t, toolNames(host.ListTools()), "tool_search")

	host.UnregisterRemoteServer(client)

	names := toolNames(host.ListTools())
	if len(names) != 1 {
		t.Fatalf("expected only local tool, got %v", names)
	}
	assertToolMissing(t, names, "tool_search")
	assertToolMissing(t, names, "execute_tool")
}

func TestReplaceRemoteServers_RemovesOldNativeTools(t *testing.T) {
	remote := NewServer("remote", "1")
	remote.RegisterTool(NewTool("old_tool", "Old"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("old"), nil
	})
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer ts.Close()

	host := NewServer("host", "1")
	host.RegisterTool(NewTool("local", "Local"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("local"), nil
	})

	client := NewClient(ts.URL, nil, "ns")
	if err := host.RegisterRemoteServer(client); err != nil {
		t.Fatalf("register: %v", err)
	}

	assertToolExists(t, toolNames(host.ListTools()), "ns__old_tool")

	if err := host.ReplaceRemoteServers(nil); err != nil {
		t.Fatalf("replace with empty: %v", err)
	}

	names := toolNames(host.ListTools())
	if len(names) != 1 {
		t.Fatalf("expected only local tool after replace with empty, got %v", names)
	}
	assertToolExists(t, names, "local")
	assertToolMissing(t, names, "ns__old_tool")
}

func TestReplaceRemoteServers_RemovesOldDiscoverableTools(t *testing.T) {
	remote := NewServer("remote", "1")
	remote.RegisterTool(NewTool("hidden", "Hidden tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("hidden"), nil
	})
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer ts.Close()

	host := NewServer("host", "1")
	host.RegisterTool(NewTool("local", "Local"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("local"), nil
	})

	if err := host.RegisterRemoteServerDiscoverable(NewClient(ts.URL, nil, "ns")); err != nil {
		t.Fatalf("register disc: %v", err)
	}

	assertToolExists(t, toolNames(host.ListTools()), "tool_search")

	if err := host.ReplaceRemoteServers(nil); err != nil {
		t.Fatalf("replace with empty: %v", err)
	}

	names := toolNames(host.ListTools())
	assertToolMissing(t, names, "tool_search")
	assertToolMissing(t, names, "execute_tool")
	assertToolExists(t, names, "local")
}

func TestReplaceRemoteServers_RemovesOldRemoteSearch(t *testing.T) {
	remote := NewServer("remote", "1")
	remote.RegisterTool(NewTool("rt", "Remote tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("rt"), nil
	})
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer ts.Close()

	host := NewServer("host", "1")
	host.RegisterTool(NewTool("local", "Local"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("local"), nil
	})

	client := NewClient(ts.URL, nil, "ns")
	if err := host.RegisterRemoteServer(client, WithRemoteSearch()); err != nil {
		t.Fatalf("register with search: %v", err)
	}

	assertToolExists(t, toolNames(host.ListTools()), "tool_search")

	if err := host.ReplaceRemoteServers(nil); err != nil {
		t.Fatalf("replace with empty: %v", err)
	}

	names := toolNames(host.ListTools())
	assertToolMissing(t, names, "tool_search")
	assertToolMissing(t, names, "execute_tool")
}

func TestReplaceRemoteServers_ReplacesWithNewServers(t *testing.T) {
	oldRemote := NewServer("old", "1")
	oldRemote.RegisterTool(NewTool("old_tool", "Old"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("old"), nil
	})
	tsOld := httptest.NewServer(http.HandlerFunc(oldRemote.HandleRequest))
	defer tsOld.Close()

	newRemote := NewServer("new", "1")
	newRemote.RegisterTool(NewTool("new_tool", "New"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("new"), nil
	})
	tsNew := httptest.NewServer(http.HandlerFunc(newRemote.HandleRequest))
	defer tsNew.Close()

	host := NewServer("host", "1")
	host.RegisterTool(NewTool("local", "Local"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("local"), nil
	})

	if err := host.RegisterRemoteServer(NewClient(tsOld.URL, nil, "old")); err != nil {
		t.Fatalf("register old: %v", err)
	}
	assertToolExists(t, toolNames(host.ListTools()), "old__old_tool")

	if err := host.ReplaceRemoteServers([]RemoteServerEntry{
		{Client: NewClient(tsNew.URL, nil, "new"), Visibility: ToolVisibilityNative},
	}); err != nil {
		t.Fatalf("replace: %v", err)
	}

	names := toolNames(host.ListTools())
	assertToolMissing(t, names, "old__old_tool")
	assertToolExists(t, names, "new__new_tool")
	assertToolExists(t, names, "local")
}

func TestVisibilityTransition_NativeToDiscoverablePreservesRemoteTools(t *testing.T) {
	remote := NewServer("remote", "1")
	remote.RegisterTool(NewTool("rt", "Remote tool"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("rt"), nil
	})
	ts := httptest.NewServer(http.HandlerFunc(remote.HandleRequest))
	defer ts.Close()

	host := NewServer("host", "1")

	handler := func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("ok"), nil
	}

	host.RegisterTool(NewTool("my_tool", "Native"), handler)
	if err := host.RegisterRemoteServer(NewClient(ts.URL, nil, "ns")); err != nil {
		t.Fatalf("register remote: %v", err)
	}

	names := toolNames(host.ListTools())
	assertToolExists(t, names, "my_tool")
	assertToolExists(t, names, "ns__rt")

	host.RegisterTool(NewTool("my_tool", "Now discoverable").Discoverable("kw"), handler)

	names = toolNames(host.ListTools())
	assertToolMissing(t, names, "my_tool")
	assertToolExists(t, names, "ns__rt")
	assertToolExists(t, names, "tool_search")
	assertToolExists(t, names, "execute_tool")

	host.RegisterTool(NewTool("my_tool", "Back to native"), handler)

	names = toolNames(host.ListTools())
	assertToolExists(t, names, "my_tool")
	assertToolExists(t, names, "ns__rt")
	assertToolMissing(t, names, "tool_search")
	assertToolMissing(t, names, "execute_tool")
}

func TestVisibilityTransition_DiscoverableToNativePreservesRemoteDiscoverable(t *testing.T) {
	remoteDisc := NewServer("remote-disc", "1")
	remoteDisc.RegisterTool(NewTool("rd", "Remote disc"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("rd"), nil
	})
	tsDisc := httptest.NewServer(http.HandlerFunc(remoteDisc.HandleRequest))
	defer tsDisc.Close()

	host := NewServer("host", "1")
	handler := func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("ok"), nil
	}

	host.RegisterTool(NewTool("my_tool", "Discoverable").Discoverable("kw"), handler)
	if err := host.RegisterRemoteServerDiscoverable(NewClient(tsDisc.URL, nil, "rd")); err != nil {
		t.Fatalf("register remote disc: %v", err)
	}

	names := toolNames(host.ListTools())
	assertToolMissing(t, names, "my_tool")
	assertToolExists(t, names, "tool_search")

	host.RegisterTool(NewTool("my_tool", "Now native"), handler)

	names = toolNames(host.ListTools())
	assertToolExists(t, names, "my_tool")
	assertToolExists(t, names, "tool_search")
	assertToolExists(t, names, "execute_tool")
}

func TestRegisterTool_VisibilityTransition(t *testing.T) {
	s := NewServer("test", "1.0")

	handler := func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("ok"), nil
	}

	s.RegisterTool(NewTool("my_tool", "Native tool"), handler)
	tools := s.ListTools()
	if len(tools) != 1 || tools[0].Name != "my_tool" {
		t.Fatalf("expected 1 native tool, got %v", toolNames(tools))
	}

	s.RegisterTool(NewTool("my_tool", "Now discoverable").Discoverable("secret"), handler)
	tools = s.ListTools()
	for _, tl := range tools {
		if tl.Name == "my_tool" {
			t.Fatal("my_tool should NOT appear in ListTools after becoming discoverable")
		}
	}

	resp, err := s.CallTool(context.Background(), "my_tool", nil)
	if err != nil {
		t.Fatalf("calling discoverable tool: %v", err)
	}
	if resp.Content[0].Text != "ok" {
		t.Fatalf("expected ok, got %s", resp.Content[0].Text)
	}

	s.RegisterTool(NewTool("my_tool", "Back to native"), handler)
	tools = s.ListTools()
	if len(tools) != 1 || tools[0].Name != "my_tool" {
		t.Fatalf("expected my_tool back in ListTools, got %v", toolNames(tools))
	}
	if tools[0].Description != "Back to native" {
		t.Fatalf("expected updated description, got %s", tools[0].Description)
	}
}

func TestServer_ConcurrentToolRegistration(t *testing.T) {
	s := NewServer("test", "1.0")
	var wg sync.WaitGroup

	// Concurrently register 100 tools
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := fmt.Sprintf("tool_%03d", idx)
			s.RegisterTool(
				NewTool(name, fmt.Sprintf("Tool %d", idx)),
				func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
					return NewToolResponseText(name), nil
				},
			)
		}(i)
	}

	wg.Wait()

	tools := s.ListTools()
	if len(tools) != 100 {
		t.Fatalf("Expected 100 tools, got %d", len(tools))
	}

	// Verify tools are sorted
	for i := 1; i < len(tools); i++ {
		if tools[i-1].Name >= tools[i].Name {
			t.Fatalf("Tools not sorted: %s >= %s at index %d", tools[i-1].Name, tools[i].Name, i)
		}
	}
}

func TestServer_ConcurrentListAndCall(t *testing.T) {
	s := NewServer("test", "1.0")

	// Register some tools first
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("tool_%d", i)
		s.RegisterTool(
			NewTool(name, fmt.Sprintf("Tool %d", i)),
			func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
				return NewToolResponseText("ok"), nil
			},
		)
	}

	var wg sync.WaitGroup
	errChan := make(chan error, 200)

	// Concurrent reads (ListTools)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tools := s.ListTools()
			if len(tools) < 10 {
				errChan <- fmt.Errorf("expected at least 10 tools, got %d", len(tools))
			}
		}()
	}

	// Concurrent tool calls
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := fmt.Sprintf("tool_%d", idx%10)
			_, err := s.CallTool(context.Background(), name, nil)
			if err != nil {
				errChan <- fmt.Errorf("CallTool(%s) failed: %v", name, err)
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		t.Error(err)
	}
}

func TestServer_ConcurrentRegistrationAndRead(t *testing.T) {
	s := NewServer("test", "1.0")
	var wg sync.WaitGroup
	done := make(chan struct{})

	// Readers: continuously list tools
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					// Should never panic or return inconsistent results
					tools := s.ListTools()
					_ = tools
				}
			}
		}()
	}

	// Writers: register tools
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := fmt.Sprintf("tool_%03d", idx)
			s.RegisterTool(
				NewTool(name, fmt.Sprintf("Tool %d", idx)),
				func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
					return NewToolResponseText("ok"), nil
				},
			)
		}(i)
	}

	// Wait for writers to finish
	time.Sleep(100 * time.Millisecond)
	close(done)
	wg.Wait()

	// Verify final state
	tools := s.ListTools()
	if len(tools) != 50 {
		t.Fatalf("Expected 50 tools, got %d", len(tools))
	}
}
