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
