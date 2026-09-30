package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// paginatingServer serves tools/list, resources/list, resources/templates/list
// and prompts/list in three pages each (cursor "p2", "p3"), as a Modern or a
// Legacy-only server, recording the cursors it was sent.
func paginatingServer(t *testing.T, modern bool, cursors *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	keys := map[string]string{"tools/list": "tools", "resources/list": "resources", "resources/templates/list": "resourceTemplates", "prompts/list": "prompts"}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var rpc struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		json.Unmarshal(body, &rpc)
		w.Header().Set("Content-Type", "application/json")
		reply := func(result map[string]any) {
			if modern {
				result["resultType"] = "complete"
			}
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result})
		}
		switch {
		case rpc.Method == "server/discover":
			if !modern {
				http.Error(w, "unknown", http.StatusBadRequest)
				return
			}
			reply(map[string]any{"supportedVersions": []string{MCPProtocolVersionModern}, "capabilities": map[string]any{}})
		case rpc.Method == "initialize":
			reply(map[string]any{"protocolVersion": MCPProtocolVersionLatest, "capabilities": map[string]any{}})
		case rpc.ID == nil:
			w.WriteHeader(http.StatusAccepted)
		default:
			key, ok := keys[rpc.Method]
			if !ok {
				t.Errorf("unexpected method %s", rpc.Method)
				return
			}
			cursor, _ := rpc.Params["cursor"].(string)
			mu.Lock()
			*cursors = append(*cursors, rpc.Method+"@"+cursor)
			mu.Unlock()
			page, next := 1, "p2"
			switch cursor {
			case "p2":
				page, next = 2, "p3"
			case "p3":
				page, next = 3, ""
			}
			item := map[string]any{"name": key + "-" + string(rune('0'+page)), "uri": "x://" + key + "/" + string(rune('0'+page)), "uriTemplate": "x://t/{id}"}
			result := map[string]any{key: []any{item}}
			if next != "" {
				result["nextCursor"] = next
			}
			reply(result)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestClientFollowsPagination(t *testing.T) {
	for _, modern := range []bool{true, false} {
		name := "legacy"
		if modern {
			name = "modern"
		}
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			var cursors []string
			ts := paginatingServer(t, modern, &cursors, &mu)
			c := NewClient(ts.URL, nil, "")
			ctx := context.Background()

			tools, err := c.ListTools(ctx)
			if err != nil || len(tools) != 3 || tools[2].Name != "tools-3" {
				t.Fatalf("ListTools = %v %v, want all 3 pages", tools, err)
			}
			res, err := c.ListResources(ctx)
			if err != nil || len(res) != 3 {
				t.Fatalf("ListResources = %v %v", res, err)
			}
			tpl, err := c.ListResourceTemplates(ctx)
			if err != nil || len(tpl) != 3 {
				t.Fatalf("ListResourceTemplates = %v %v", tpl, err)
			}
			prompts, err := c.ListPrompts(ctx)
			if err != nil || len(prompts) != 3 {
				t.Fatalf("ListPrompts = %v %v", prompts, err)
			}

			mu.Lock()
			defer mu.Unlock()
			got := strings.Join(cursors, " ")
			for _, m := range []string{"tools/list", "resources/list", "resources/templates/list", "prompts/list"} {
				if !strings.Contains(got, m+"@ "+m+"@p2 "+m+"@p3") {
					t.Fatalf("%s pages requested: %s", m, got)
				}
			}
		})
	}
}

// A server that hands back the same cursor again would loop a naive client;
// this one fails the call instead.
func TestClientPaginationRepeatedCursor(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpc map[string]any
		json.NewDecoder(r.Body).Decode(&rpc)
		w.Header().Set("Content-Type", "application/json")
		switch rpc["method"] {
		case "server/discover":
			http.Error(w, "unknown", http.StatusBadRequest)
		case "initialize":
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc["id"], "result": map[string]any{"protocolVersion": MCPProtocolVersionLatest}})
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc["id"], "result": map[string]any{"tools": []any{}, "nextCursor": "same"}})
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer ts.Close()
	_, err := NewClient(ts.URL, nil, "").ListTools(context.Background())
	if err == nil || !strings.Contains(err.Error(), "repeated pagination cursor") {
		t.Fatalf("err = %v, want repeated-cursor error", err)
	}
}
