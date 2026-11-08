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

// listed reports whether name is in the request's tools/list.
func listed(s *Server, ctx context.Context, name string) bool {
	for _, tl := range s.ListToolsWithContext(ctx) {
		if tl.Name == name {
			return true
		}
	}
	return false
}

// tool_search and execute_tool are callable exactly when the request's
// tools/list shows them, per user and per mode.
func TestDiscoveryToolsCallableExactlyWhenListed(t *testing.T) {
	ok := func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("ok"), nil
	}
	schema := map[string]any{"type": "object"}

	plain := NewServer("plain", "1")
	plain.RegisterTool(NewTool("visible", "v"), ok)

	static := NewServer("static", "1")
	static.RegisterTool(NewTool("visible", "v"), ok)
	static.RegisterTool(NewTool("hidden", "h").Discoverable(), ok)

	perUser := NewServer("per-user", "1")
	perUser.RegisterTool(NewTool("visible", "v"), ok)
	userA := WithToolProviders(context.Background(), &mockToolProvider{tools: []MCPTool{{Name: "a_hidden", InputSchema: schema, Visibility: ToolVisibilityDiscoverable}}})
	userB := WithToolProviders(context.Background(), &mockToolProvider{tools: []MCPTool{{Name: "b_visible", InputSchema: schema, Visibility: ToolVisibilityNative}}})

	for _, tc := range []struct {
		name string
		s    *Server
		ctx  context.Context
		want bool
	}{
		{"no discoverable tools", plain, context.Background(), false},
		{"static discoverable tool", static, context.Background(), true},
		{"static discoverable tool, show-all", static, WithShowAllTools(context.Background()), false},
		{"user with a discoverable provider tool", perUser, userA, true},
		{"user without one", perUser, userB, false},
		{"user with one, show-all", perUser, WithShowAllTools(userA), false},
	} {
		for _, name := range []string{ToolSearchName, ExecuteToolName} {
			if got := listed(tc.s, tc.ctx, name); got != tc.want {
				t.Errorf("%s: %s listed=%v, want %v", tc.name, name, got, tc.want)
			}
			_, err := tc.s.CallTool(tc.ctx, name, map[string]any{"name": "visible"})
			if unknown := errors.Is(err, ErrUnknownTool); unknown == tc.want {
				t.Errorf("%s: %s callable=%v (err %v), want %v", tc.name, name, !unknown, err, tc.want)
			}
		}
	}
}

// Over tools/call an unlisted discovery tool is the spec's unknown-tool
// protocol error.
func TestUnlistedDiscoveryToolIsUnknownOverHTTP(t *testing.T) {
	s := NewServer("plain", "1")
	ts := httptest.NewServer(http.HandlerFunc(s.HandleRequest))
	defer ts.Close()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tool_search","arguments":{}}}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerProtocolVersion, "2025-11-25")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(raw), `"code":-32602`) || !strings.Contains(string(raw), "Unknown tool: tool_search") {
		t.Fatalf("got %s", raw)
	}
}

// Provider tools' arguments are checked against the schema the provider
// listed: quoted scalars are converted, anything invalid is refused before
// the provider runs.
func TestProviderToolArgumentsValidated(t *testing.T) {
	var ran []map[string]any
	provider := &mockToolProvider{
		tools: []MCPTool{
			{Name: "count", InputSchema: NewTool("count", "", Integer("n", "", Required())).BuildSchema(), Visibility: ToolVisibilityNative},
			{Name: "anything", Visibility: ToolVisibilityNative}, // no schema: any arguments
		},
		execFunc: func(ctx context.Context, name string, params map[string]any) (any, error) {
			ran = append(ran, params)
			return "ok", nil
		},
	}
	s := NewServer("p", "1")
	ctx := WithToolProviders(context.Background(), provider)

	if _, err := s.CallTool(ctx, "count", map[string]any{"n": "3"}); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 || ran[0]["n"] != 3.0 {
		t.Fatalf("provider ran with %v, want n=3", ran)
	}
	for _, args := range []map[string]any{{}, {"n": "x"}, {"n": 1, "extra": true}} {
		_, err := s.CallTool(ctx, "count", args)
		var toolErr *ToolError
		if !errors.As(err, &toolErr) || toolErr.Code != ErrorCodeInvalidParams {
			t.Errorf("%v: got %v, want an input validation error", args, err)
		}
	}
	if len(ran) != 1 {
		t.Fatalf("the provider ran on refused arguments: %v", ran)
	}
	if _, err := s.CallTool(ctx, "anything", map[string]any{"whatever": 1}); err != nil || len(ran) != 2 {
		t.Fatalf("a tool without a schema should take any arguments: %v, ran %v", err, ran)
	}
}

// A JSON-RPC error a remote server sends for a forwarded tools/call is
// passed on as it came, whatever its code; Server.CallTool still returns it
// as a *ToolError.
func TestRemoteToolErrorRelayedAsSent(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		reply := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		switch req.Method {
		case "initialize":
			reply["result"] = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "r", "version": "1"}}
		case "tools/list":
			reply["result"] = map[string]any{"tools": []any{map[string]any{"name": "quota", "description": "q", "inputSchema": map[string]any{"type": "object"}}}}
		case "tools/call":
			reply["error"] = map[string]any{"code": 1001, "message": "quota exceeded"}
		default:
			reply["error"] = map[string]any{"code": ErrorCodeMethodNotFound, "message": "method not found"}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reply)
	}))
	defer remote.Close()

	proxy := NewServer("proxy", "1")
	client := NewClient(remote.URL, nil, "r")
	defer client.Close()
	if err := proxy.RegisterRemoteServer(client); err != nil {
		t.Fatal(err)
	}
	_, err := proxy.CallTool(context.Background(), "r__quota", nil)
	var toolErr *ToolError
	if !errors.As(err, &toolErr) || toolErr.Code != 1001 {
		t.Fatalf("want the remote's *ToolError 1001, got %v", err)
	}
	result, wireErr := proxy.toolsCallResult(context.Background(), "r__quota", nil, err, true)
	if wireErr == nil || wireErr.Code != 1001 || wireErr.Message != "quota exceeded" || result != nil {
		t.Fatalf("want the remote's error relayed, got result=%+v error=%+v", result, wireErr)
	}
}

// modernRaw posts a 2026-07-28 request whose params (including _meta) are
// exactly params, with the standard headers.
func modernRaw(t *testing.T, url, method string, params map[string]any) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 7, "method": method, "params": params})
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerMcpMethod, method)
	req.Header.Set(headerProtocolVersion, MCPProtocolVersionModern)
	if name, ok := params["name"].(string); ok {
		req.Header.Set(headerMcpName, name)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, decodeMCPResponse(t, raw)
}

// A Modern request missing a required _meta field is malformed: -32602 with
// HTTP 400, even though its MCP-Protocol-Version header then has no body
// value to match. A header that disagrees with a value the body carries is
// still HeaderMismatch.
func TestModernMissingMetaIsInvalidParams(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(NewServer("m", "1").HandleRequest))
	defer ts.Close()
	caps := map[string]any{}
	for name, params := range map[string]map[string]any{
		"no _meta":             {},
		"no protocolVersion":   {"_meta": map[string]any{metaKeyClientCapabilities: caps}},
		"no clientCapabilites": {"_meta": map[string]any{metaKeyProtocolVersion: MCPProtocolVersionModern}},
	} {
		status, out := modernRaw(t, ts.URL, "server/discover", params)
		errObj, _ := out["error"].(map[string]any)
		if status != http.StatusBadRequest || errObj == nil || errObj["code"] != float64(ErrorCodeInvalidParams) || out["id"] != float64(7) {
			t.Errorf("%s: got HTTP %d %v, want 400 with -32602 and the request id", name, status, out)
		}
	}
	status, out := modernRaw(t, ts.URL, "server/discover", map[string]any{"_meta": map[string]any{metaKeyProtocolVersion: "v999.0.0", metaKeyClientCapabilities: caps}})
	if errObj, _ := out["error"].(map[string]any); status != http.StatusBadRequest || errObj == nil || errObj["code"] != float64(ErrorCodeHeaderMismatch) {
		t.Errorf("header disagreeing with the body: got HTTP %d %v, want 400 with -32020", status, out)
	}
}

// A handler that needs a client capability the request didn't declare
// returns NewToolErrorMissingClientCapability: -32021 with
// data.requiredCapabilities as a ClientCapabilities object, HTTP 400 on the
// 2026-07-28 wire, and the same JSON-RPC error on the Legacy wire.
func TestMissingClientCapabilityError(t *testing.T) {
	s := NewServer("m", "1")
	s.RegisterTool(NewTool("needs_sampling", "n"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		if _, ok := ClientCapabilitiesFromContext(ctx)["sampling"]; !ok {
			return nil, NewToolErrorMissingClientCapability("sampling")
		}
		return NewToolResponseText("ok"), nil
	})
	ts := httptest.NewServer(http.HandlerFunc(s.HandleRequest))
	defer ts.Close()

	meta := func(caps map[string]any) map[string]any {
		return map[string]any{metaKeyProtocolVersion: MCPProtocolVersionModern, metaKeyClientCapabilities: caps}
	}
	status, out := modernRaw(t, ts.URL, "tools/call", map[string]any{"name": "needs_sampling", "arguments": map[string]any{}, "_meta": meta(map[string]any{})})
	errObj, _ := out["error"].(map[string]any)
	data, _ := errObj["data"].(map[string]any)
	required, _ := data["requiredCapabilities"].(map[string]any)
	if status != http.StatusBadRequest || errObj["code"] != float64(ErrorCodeMissingRequiredClientCapability) || required["sampling"] == nil {
		t.Fatalf("undeclared sampling: got HTTP %d %v, want 400 with -32021 naming sampling", status, out)
	}
	if status, out := modernRaw(t, ts.URL, "tools/call", map[string]any{"name": "needs_sampling", "arguments": map[string]any{}, "_meta": meta(map[string]any{"sampling": map[string]any{}})}); status != http.StatusOK || out["error"] != nil {
		t.Fatalf("declared sampling: got HTTP %d %v, want the tool's result", status, out)
	}

	result, wireErr := s.toolsCallResult(context.Background(), "needs_sampling", nil, NewToolErrorMissingClientCapability("sampling"), true)
	if wireErr == nil || wireErr.Code != ErrorCodeMissingRequiredClientCapability || result != nil {
		t.Fatalf("Legacy wire: got result=%+v error=%+v, want -32021", result, wireErr)
	}
}

// A -32021 relayed from a remote server is about this server's own
// capabilities on that hop, not the caller's: it is not passed on as-is.
func TestRelayedMissingClientCapabilityNotPassedOn(t *testing.T) {
	s := NewServer("m", "1")
	err := relayedError(NewToolErrorMissingClientCapability("sampling"))
	_, wireErr := s.toolsCallResult(context.Background(), "remote__tool", nil, err, false)
	if wireErr == nil || wireErr.Code != ErrorCodeInternalError {
		t.Fatalf("got %+v, want it sent as an internal error", wireErr)
	}
}

// The unknown-tool error suggests tool_search only when the request can
// call it: not in show-all mode, and not for tool_search itself.
func TestUnknownToolHintOnlyWhenDiscoveryListed(t *testing.T) {
	s := discoveryServer()
	hint := func(ctx context.Context, name string) bool {
		return strings.Contains(s.toolsCallWireError(ctx, name, ErrUnknownTool).Message, "Use tool_search")
	}
	if !hint(context.Background(), "nope") {
		t.Error("normal mode: unknown tool should suggest tool_search")
	}
	if hint(WithShowAllTools(context.Background()), "nope") {
		t.Error("show-all mode: tool_search is not callable, so it must not be suggested")
	}
	if hint(WithShowAllTools(context.Background()), ToolSearchName) {
		t.Error("an unknown tool_search must not suggest itself")
	}
}
