package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type staticAuth struct{ header string }

func (s staticAuth) GetAuthHeader() (string, error) { return s.header, nil }
func (s staticAuth) Refresh() error                 { return nil }

func TestClient_InitializeListCall(t *testing.T) {
	// spin up a simple MCP server with one tool
	srv := NewServer("svc", "1")
	srv.RegisterTool(NewTool("upper", "to upper", String("s", "s", Required())), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		v, _ := req.String("s")
		return NewToolResponseText(strings.ToUpper(v)), nil
	})
	h := http.HandlerFunc(srv.HandleRequest)
	ts := httptest.NewServer(h)
	defer ts.Close()

	c := NewClient(ts.URL, staticAuth{"Bearer t"}, "")

	ctx := context.Background()

	// list tools
	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "upper" {
		t.Fatalf("unexpected tools: %+v", tools)
	}

	// call tool
	resp, err := c.CallTool(ctx, "upper", map[string]any{"s": "abc"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "ABC" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestClient_SendsSessionAndAuth(t *testing.T) {
	// Handler that validates headers after initialize
	var sessionSeen string
	var authSeen string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpc MCPRequest
		_ = json.NewDecoder(r.Body).Decode(&rpc)
		switch rpc.Method {
		case "initialize":
			// respond with a session id in body
			res := MCPResponse{JSONRPC: "2.0", ID: rpc.ID, Result: map[string]any{
				"protocolVersion": MCPProtocolVersionLatest,
				"capabilities":    capabilities{Tools: map[string]any{}},
				"serverInfo":      serverInfo{Name: "x", Version: "1"},
				"sessionId":       "sess-123",
			}}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(res)
		case "tools/list":
			sessionSeen = r.Header.Get("Mcp-Session-Id")
			authSeen = r.Header.Get("Authorization")
			res := MCPResponse{JSONRPC: "2.0", ID: rpc.ID, Result: map[string]any{"tools": []MCPTool{}}}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(res)
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: rpc.ID, Result: map[string]any{}})
		}
	})
	ts := httptest.NewServer(h)
	defer ts.Close()

	c := NewClient(ts.URL, staticAuth{"Bearer token-xyz"}, "")
	ctx := context.Background()
	_, _ = c.ListTools(ctx)

	if sessionSeen != "sess-123" {
		t.Fatalf("expected session header to be forwarded, got %q", sessionSeen)
	}
	if authSeen != "Bearer token-xyz" {
		t.Fatalf("expected auth header, got %q", authSeen)
	}
}

func TestClient_SessionFromHeader(t *testing.T) {
	// Server sets session id in header during initialize
	var sessionSeen string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpc MCPRequest
		_ = json.NewDecoder(r.Body).Decode(&rpc)
		switch rpc.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "hdr-456")
			_ = json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: rpc.ID, Result: map[string]any{
				"protocolVersion": MCPProtocolVersionLatest,
				"capabilities":    capabilities{Tools: map[string]any{}},
				"serverInfo":      serverInfo{Name: "x", Version: "1"},
			}})
		case "tools/list":
			sessionSeen = r.Header.Get("Mcp-Session-Id")
			_ = json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: rpc.ID, Result: map[string]any{"tools": []MCPTool{}}})
		}
	})
	ts := httptest.NewServer(h)
	defer ts.Close()
	c := NewClient(ts.URL, staticAuth{"b"}, "")
	_, _ = c.ListTools(context.Background())
	if sessionSeen != "hdr-456" {
		t.Fatalf("expected session header from response header, got %q", sessionSeen)
	}
}

func TestClient_SSE_UnexpectedLines(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpc MCPRequest
		_ = json.NewDecoder(r.Body).Decode(&rpc)
		if rpc.Method == "initialize" {
			_ = json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: rpc.ID, Result: map[string]any{
				"protocolVersion": MCPProtocolVersionLatest,
				"capabilities":    capabilities{Tools: map[string]any{}},
				"serverInfo":      serverInfo{Name: "x", Version: "1"},
			}})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		// include a comment and an empty data line before actual data
		fmt.Fprintln(w, ": comment")
		fmt.Fprintln(w, "data:")
		fmt.Fprintf(w, "data: %s\n\n", `{"jsonrpc":"2.0","id":"list-tools","result":{"tools":[]}}`)
	})
	ts := httptest.NewServer(h)
	defer ts.Close()
	c := NewClient(ts.URL, staticAuth{"b"}, "")
	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatalf("expected tolerant SSE parse, got err: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(tools))
	}
}

func TestClient_HandlesServerErrors(t *testing.T) {
	// JSON-RPC error envelope
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpc MCPRequest
		_ = json.NewDecoder(r.Body).Decode(&rpc)
		if rpc.Method == "initialize" {
			_ = json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: rpc.ID, Result: map[string]any{
				"protocolVersion": MCPProtocolVersionLatest,
				"capabilities":    capabilities{Tools: map[string]any{}},
				"serverInfo":      serverInfo{Name: "x", Version: "1"},
			}})
			return
		}
		_ = json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: rpc.ID, Error: &MCPError{Code: -32000, Message: "boom"}})
	})
	ts := httptest.NewServer(h)
	defer ts.Close()
	c := NewClient(ts.URL, staticAuth{"b"}, "")
	ctx := context.Background()
	_, err := c.CallTool(ctx, "x", nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestClient_Non200Status(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return non-200 on second request
		if r.Header.Get("X-Once") == "1" {
			w.WriteHeader(http.StatusTeapot)
			fmt.Fprint(w, "nope")
			return
		}
		// initial initialize
		var rpc MCPRequest
		_ = json.NewDecoder(r.Body).Decode(&rpc)
		if rpc.Method == "initialize" {
			w.Header().Set("X-Once", "1")
			_ = json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: rpc.ID, Result: map[string]any{
				"protocolVersion": MCPProtocolVersionLatest,
				"capabilities":    capabilities{Tools: map[string]any{}},
				"serverInfo":      serverInfo{Name: "x", Version: "1"},
			}})
		}
	})
	ts := httptest.NewServer(h)
	defer ts.Close()
	c := NewClient(ts.URL, staticAuth{"b"}, "")
	ctx := context.Background()
	_ = c.Initialize(ctx)
	// now any call should hit non-200
	_, err := c.ListTools(ctx)
	if err == nil {
		t.Fatal("expected non-200 error")
	}
}

func TestClient_EventStreamParsing(t *testing.T) {
	// Return an SSE-like payload once initialized
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpc MCPRequest
		_ = json.NewDecoder(r.Body).Decode(&rpc)
		if rpc.Method == "initialize" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: rpc.ID, Result: map[string]any{
				"protocolVersion": MCPProtocolVersionLatest,
				"capabilities":    capabilities{Tools: map[string]any{}},
				"serverInfo":      serverInfo{Name: "x", Version: "1"},
			}})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", `{"jsonrpc":"2.0","id":"list-tools","result":{"tools":[]}}`)
	})
	ts := httptest.NewServer(h)
	defer ts.Close()
	c := NewClient(ts.URL, staticAuth{"b"}, "")
	ctx := context.Background()
	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("sse list tools err: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(tools))
	}
}

func TestClient_RefreshToolCache(t *testing.T) {
	// dynamic list changing across calls; ensure RefreshToolCache gets new data
	count := 0
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpc MCPRequest
		_ = json.NewDecoder(r.Body).Decode(&rpc)
		if rpc.Method == "initialize" {
			_ = json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: rpc.ID, Result: map[string]any{
				"protocolVersion": MCPProtocolVersionLatest,
				"capabilities":    capabilities{Tools: map[string]any{}},
				"serverInfo":      serverInfo{Name: "x", Version: "1"},
			}})
			return
		}
		if rpc.Method == "tools/list" {
			var tools []MCPTool
			if count == 0 {
				tools = []MCPTool{{Name: "a", Description: "", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}}}
			} else {
				tools = []MCPTool{{Name: "b", Description: "", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}}}
			}
			count++
			_ = json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: rpc.ID, Result: map[string]any{"tools": tools}})
			return
		}
		_ = json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: rpc.ID, Result: map[string]any{}})
	})
	ts := httptest.NewServer(h)
	defer ts.Close()
	c := NewClient(ts.URL, staticAuth{"b"}, "")
	ctx := context.Background()
	t1, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("first list err: %v", err)
	}
	if len(t1) != 1 || t1[0].Name != "a" {
		t.Fatalf("unexpected first tools: %+v", t1)
	}
	if err := c.RefreshToolCache(ctx); err != nil {
		t.Fatalf("refresh err: %v", err)
	}
	t2, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("second list err: %v", err)
	}
	if len(t2) != 1 || t2[0].Name != "b" {
		t.Fatalf("unexpected second tools: %+v", t2)
	}
}

func TestClient_NamespaceNormalization(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		want      string
	}{
		{name: "empty", namespace: "", want: ""},
		{name: "simple", namespace: "myns", want: "myns/"},
		{name: "with-trailing-slash", namespace: "myns/", want: "myns/"},
		{name: "whitespace-only", namespace: "   ", want: ""},
		{name: "whitespace-padded", namespace: "  myns  ", want: "myns/"},
		{name: "with-hyphen", namespace: "my-namespace", want: "my-namespace/"},
		{name: "with-underscore", namespace: "my_namespace", want: "my_namespace/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewClient("http://example.com", nil, tt.namespace)
			if c.Namespace() != tt.want {
				t.Errorf("Namespace() = %q, want %q", c.Namespace(), tt.want)
			}
		})
	}
}

func TestClient_ToolFilter(t *testing.T) {
	// Create server with multiple tools
	srv := NewServer("svc", "1")
	srv.RegisterTool(NewTool("search", "search tool", String("q", "query", Required())), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("search result"), nil
	})
	srv.RegisterTool(NewTool("delete", "delete tool", String("id", "id", Required())), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("deleted"), nil
	})
	srv.RegisterTool(NewTool("create", "create tool", String("name", "name", Required())), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("created"), nil
	})
	ts := httptest.NewServer(http.HandlerFunc(srv.HandleRequest))
	defer ts.Close()

	ctx := context.Background()

	t.Run("no filter returns all tools", func(t *testing.T) {
		c := NewClient(ts.URL, staticAuth{"Bearer t"}, "ns")
		tools, err := c.ListTools(ctx)
		if err != nil {
			t.Fatalf("ListTools: %v", err)
		}
		if len(tools) != 3 {
			t.Errorf("expected 3 tools, got %d", len(tools))
		}
	})

	t.Run("filter excludes tools from ListTools", func(t *testing.T) {
		c := NewClient(ts.URL, staticAuth{"Bearer t"}, "ns")
		c.WithToolFilter(func(name string) bool {
			return name != "delete" // exclude delete
		})

		tools, err := c.ListTools(ctx)
		if err != nil {
			t.Fatalf("ListTools: %v", err)
		}
		if len(tools) != 2 {
			t.Errorf("expected 2 tools, got %d", len(tools))
		}
		for _, tool := range tools {
			if tool.Name == "ns/delete" {
				t.Error("delete tool should have been filtered out")
			}
		}
	})

	t.Run("filter blocks CallTool", func(t *testing.T) {
		c := NewClient(ts.URL, staticAuth{"Bearer t"}, "ns")
		c.WithToolFilter(func(name string) bool {
			return name == "search" // only allow search
		})

		// Should work for allowed tool
		_, err := c.CallTool(ctx, "ns/search", map[string]any{"q": "test"})
		if err != nil {
			t.Fatalf("CallTool for allowed tool: %v", err)
		}

		// Should fail for filtered tool
		_, err = c.CallTool(ctx, "ns/delete", map[string]any{"id": "123"})
		if err != ErrToolFiltered {
			t.Errorf("expected ErrToolFiltered, got %v", err)
		}
	})

	t.Run("filter is chainable", func(t *testing.T) {
		c := NewClient(ts.URL, staticAuth{"Bearer t"}, "ns").
			WithToolFilter(func(name string) bool {
				return name == "create"
			})

		tools, err := c.ListTools(ctx)
		if err != nil {
			t.Fatalf("ListTools: %v", err)
		}
		if len(tools) != 1 || tools[0].Name != "ns/create" {
			t.Errorf("expected only ns/create, got %+v", tools)
		}
	})

	t.Run("clearing filter re-enables all tools", func(t *testing.T) {
		c := NewClient(ts.URL, staticAuth{"Bearer t"}, "ns")
		c.WithToolFilter(func(name string) bool {
			return name == "search"
		})

		// Only 1 tool with filter
		tools, _ := c.ListTools(ctx)
		if len(tools) != 1 {
			t.Errorf("expected 1 tool with filter, got %d", len(tools))
		}

		// Clear filter and refresh
		c.WithToolFilter(nil)
		_ = c.RefreshToolCache(ctx)

		tools, _ = c.ListTools(ctx)
		if len(tools) != 3 {
			t.Errorf("expected 3 tools after clearing filter, got %d", len(tools))
		}
	})

	t.Run("GetToolFilter returns current filter", func(t *testing.T) {
		c := NewClient(ts.URL, staticAuth{"Bearer t"}, "")
		if c.GetToolFilter() != nil {
			t.Error("expected nil filter initially")
		}

		filter := func(name string) bool { return true }
		c.WithToolFilter(filter)

		if c.GetToolFilter() == nil {
			t.Error("expected filter to be set")
		}
	})
}