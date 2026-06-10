package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRemoteToolSearch(t *testing.T) {
	remoteServer := NewServer("remote", "1.0.0")
	remoteServer.RegisterTool(
		NewTool("db_query", "Query the database", String("sql", "SQL query")).Discoverable("database", "sql", "query"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("query result"), nil
		},
	)
	remoteServer.RegisterTool(
		NewTool("cache_flush", "Flush the cache", String("key", "Cache key")).Discoverable("cache", "redis"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("flushed"), nil
		},
	)

	remoteTS := httptest.NewServer(http.HandlerFunc(remoteServer.HandleRequest))
	defer remoteTS.Close()

	mainServer := NewServer("main", "1.0.0")
	mainServer.RegisterTool(
		NewTool("local_tool", "A local tool", String("arg", "Argument")).Discoverable("local"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("local result"), nil
		},
	)

	client := NewClient(remoteTS.URL, nil, "remote")
	if err := mainServer.ReplaceRemoteServers([]RemoteServerEntry{
		{Client: client, Visibility: ToolVisibilityDiscoverable, RemoteSearch: true},
	}); err != nil {
		t.Fatalf("Failed to register remote server: %v", err)
	}

	t.Run("tool_search_finds_remote_tools_with_namespace", func(t *testing.T) {
		response, err := mainServer.CallTool(context.Background(), "tool_search", map[string]interface{}{
			"query":       "database",
			"max_results": 10,
		})
		if err != nil {
			t.Fatalf("tool_search failed: %v", err)
		}

		var results []SearchResult
		if err := json.Unmarshal([]byte(response.Content[0].Text), &results); err != nil {
			t.Fatalf("Failed to parse results: %v", err)
		}

		t.Logf("Search 'database' got %d results:", len(results))
		for _, r := range results {
			t.Logf("  - %s (score=%.2f)", r.Name, r.Score)
		}

		foundRemote := false
		for _, r := range results {
			if r.Name == "remote__db_query" {
				foundRemote = true
			}
		}
		if !foundRemote {
			t.Error("Expected to find remote__db_query from remote server tool_search")
		}
	})

	t.Run("tool_search_empty_query_finds_all", func(t *testing.T) {
		response, err := mainServer.CallTool(context.Background(), "tool_search", map[string]interface{}{
			"query":       "",
			"max_results": 100,
		})
		if err != nil {
			t.Fatalf("tool_search failed: %v", err)
		}

		var results []SearchResult
		if err := json.Unmarshal([]byte(response.Content[0].Text), &results); err != nil {
			t.Fatalf("Failed to parse results: %v", err)
		}

		t.Logf("Empty query got %d results:", len(results))
		for _, r := range results {
			t.Logf("  - %s (score=%.2f)", r.Name, r.Score)
		}

		names := make(map[string]bool)
		for _, r := range results {
			names[r.Name] = true
		}

		if !names["local_tool"] {
			t.Error("Expected local_tool in results")
		}
		if !names["remote__db_query"] {
			t.Error("Expected remote__db_query in results")
		}
		if !names["remote__cache_flush"] {
			t.Error("Expected remote__cache_flush in results")
		}
	})

	t.Run("remote_search_off_does_not_delegate_search", func(t *testing.T) {
		server := NewServer("main", "1.0.0")
		server.RegisterTool(
			NewTool("local_only", "A local tool", String("arg", "Arg")).Discoverable("local"),
			func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
				return NewToolResponseText("local"), nil
			},
		)

		c := NewClient(remoteTS.URL, nil, "remote")
		if err := server.ReplaceRemoteServers([]RemoteServerEntry{
			{Client: c, Visibility: ToolVisibilityNative, RemoteSearch: false},
		}); err != nil {
			t.Fatalf("Failed to register remote server: %v", err)
		}

		response, err := server.CallTool(context.Background(), "tool_search", map[string]interface{}{
			"query":       "",
			"max_results": 100,
		})
		if err != nil {
			t.Fatalf("tool_search failed: %v", err)
		}

		var results []SearchResult
		if err := json.Unmarshal([]byte(response.Content[0].Text), &results); err != nil {
			t.Fatalf("Failed to parse results: %v", err)
		}

		for _, r := range results {
			if r.Name == "remote__db_query" || r.Name == "remote__cache_flush" {
				t.Errorf("Should not find remote discoverable tools when RemoteSearch=false, got %s", r.Name)
			}
		}
	})
}

func TestRemoteExecuteTool(t *testing.T) {
	remoteServer := NewServer("remote", "1.0.0")
	remoteServer.RegisterTool(
		NewTool("db_query", "Query the database", String("sql", "SQL query")).Discoverable("database", "sql"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			sql, _ := req.String("sql")
			return NewToolResponseText("result: " + sql), nil
		},
	)

	remoteTS := httptest.NewServer(http.HandlerFunc(remoteServer.HandleRequest))
	defer remoteTS.Close()

	mainServer := NewServer("main", "1.0.0")
	mainServer.RegisterTool(
		NewTool("local_disc", "Local discoverable", String("x", "X")).Discoverable("local"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("local"), nil
		},
	)

	client := NewClient(remoteTS.URL, nil, "remote")
	if err := mainServer.ReplaceRemoteServers([]RemoteServerEntry{
		{Client: client, Visibility: ToolVisibilityDiscoverable, RemoteSearch: true},
	}); err != nil {
		t.Fatalf("Failed to register remote server: %v", err)
	}

	t.Run("execute_tool_calls_remote_discovered_tool", func(t *testing.T) {
		response, err := mainServer.CallTool(context.Background(), "execute_tool", map[string]interface{}{
			"name": "remote__db_query",
			"parameters": map[string]interface{}{
				"sql": "SELECT 1",
			},
		})
		if err != nil {
			t.Fatalf("execute_tool failed: %v", err)
		}

		if response.Content[0].Text != "result: SELECT 1" {
			t.Errorf("Expected 'result: SELECT 1', got '%s'", response.Content[0].Text)
		}
	})
}

func TestRemoteToolSearchHTTP(t *testing.T) {
	remoteServer := NewServer("remote", "1.0.0")
	remoteServer.RegisterTool(
		NewTool("secret_tool", "A secret tool hidden from listing", String("msg", "Message")).Discoverable("secret", "hidden"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			msg, _ := req.String("msg")
			return NewToolResponseText("secret: " + msg), nil
		},
	)

	remoteTS := httptest.NewServer(http.HandlerFunc(remoteServer.HandleRequest))
	defer remoteTS.Close()

	mainServer := NewServer("main", "1.0.0")
	mainServer.RegisterTool(
		NewTool("main_disc", "Main discoverable tool", String("x", "X")).Discoverable("main"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("main"), nil
		},
	)

	client := NewClient(remoteTS.URL, nil, "remote")
	if err := mainServer.ReplaceRemoteServers([]RemoteServerEntry{
		{Client: client, Visibility: ToolVisibilityDiscoverable, RemoteSearch: true},
	}); err != nil {
		t.Fatalf("Failed to register remote server: %v", err)
	}

	mainTS := httptest.NewServer(http.HandlerFunc(mainServer.HandleRequest))
	defer mainTS.Close()

	callMCP := func(method string, params interface{}) (json.RawMessage, error) {
		body := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  method,
			"params":  params,
		}
		bodyBytes, _ := json.Marshal(body)
		resp, err := http.Post(mainTS.URL, "application/json", strings.NewReader(string(bodyBytes)))
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		var rpcResp struct {
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
			return nil, err
		}
		if rpcResp.Error != nil {
			return nil, err
		}
		return rpcResp.Result, nil
	}

	t.Run("HTTP_tool_search_finds_remote_tools", func(t *testing.T) {
		result, err := callMCP("tools/call", map[string]interface{}{
			"name": "tool_search",
			"arguments": map[string]interface{}{
				"query":       "secret",
				"max_results": 10,
			},
		})
		if err != nil {
			t.Fatalf("tools/call tool_search failed: %v", err)
		}

		var toolResult struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(result, &toolResult); err != nil {
			t.Fatalf("Failed to parse result: %v", err)
		}

		var results []SearchResult
		if err := json.Unmarshal([]byte(toolResult.Content[0].Text), &results); err != nil {
			t.Fatalf("Failed to parse search results: %v", err)
		}

		t.Logf("HTTP search 'secret' got %d results:", len(results))
		for _, r := range results {
			t.Logf("  - %s (score=%.2f)", r.Name, r.Score)
		}

		found := false
		for _, r := range results {
			if r.Name == "remote__secret_tool" {
				found = true
			}
		}
		if !found {
			t.Error("Expected to find remote__secret_tool from remote server")
		}
	})

	t.Run("HTTP_execute_tool_calls_remote_discovered", func(t *testing.T) {
		result, err := callMCP("tools/call", map[string]interface{}{
			"name": "execute_tool",
			"arguments": map[string]interface{}{
				"name": "remote__secret_tool",
				"parameters": map[string]interface{}{
					"msg": "hello",
				},
			},
		})
		if err != nil {
			t.Fatalf("tools/call execute_tool failed: %v", err)
		}

		var toolResult struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(result, &toolResult); err != nil {
			t.Fatalf("Failed to parse result: %v", err)
		}

		if toolResult.Content[0].Text != "secret: hello" {
			t.Errorf("Expected 'secret: hello', got '%s'", toolResult.Content[0].Text)
		}
	})
}
