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

// ---- annotation rules ----------------------------------------------------

func TestSchemaHeaderBindings(t *testing.T) {
	prop := func(typ, header string) map[string]any { return map[string]any{"type": typ, "x-mcp-header": header} }
	obj := func(props map[string]any) map[string]any {
		return map[string]any{"type": "object", "properties": props}
	}

	valid := obj(map[string]any{
		"region": prop("string", "Region"),
		"count":  prop("integer", "Count"),
		"dry":    prop("boolean", "Dry-Run"),
		"nested": obj(map[string]any{"tenant": prop("string", "Tenant")}),
		"plain":  map[string]any{"type": "string"},
	})
	b, err := schemaHeaderBindings(valid)
	if err != nil || len(b) != 4 {
		t.Fatalf("valid schema: %v %+v", err, b)
	}
	got := map[string]string{}
	for _, x := range b {
		got[x.name] = strings.Join(x.path, ".") + ":" + x.typ
	}
	for name, want := range map[string]string{"Region": "region:string", "Count": "count:integer", "Dry-Run": "dry:boolean", "Tenant": "nested.tenant:string"} {
		if got[name] != want {
			t.Errorf("%s = %q, want %q", name, got[name], want)
		}
	}

	if b, err := schemaHeaderBindings(obj(map[string]any{"a": map[string]any{"type": "string"}})); err != nil || b != nil {
		t.Fatalf("schema without annotations: %v %v", b, err)
	}

	for name, schema := range map[string]any{
		"number type":      obj(map[string]any{"n": prop("number", "N")}),
		"array type":       obj(map[string]any{"a": map[string]any{"type": "array", "x-mcp-header": "A"}}),
		"under items":      obj(map[string]any{"a": map[string]any{"type": "array", "items": prop("string", "A")}}),
		"under oneOf":      obj(map[string]any{"a": map[string]any{"oneOf": []any{prop("string", "A")}}}),
		"under $defs":      map[string]any{"type": "object", "$defs": map[string]any{"x": prop("string", "X")}},
		"under if/then":    obj(map[string]any{"a": map[string]any{"if": map[string]any{}, "then": prop("string", "A")}}),
		"on the root":      map[string]any{"type": "string", "x-mcp-header": "Root"},
		"empty name":       obj(map[string]any{"a": prop("string", "")}),
		"space in name":    obj(map[string]any{"a": prop("string", "My Header")}),
		"colon in name":    obj(map[string]any{"a": prop("string", "A:B")}),
		"duplicate (case)": obj(map[string]any{"a": prop("string", "Region"), "b": prop("string", "region")}),
		"non-string name":  obj(map[string]any{"a": map[string]any{"type": "string", "x-mcp-header": 7}}),
	} {
		if _, err := schemaHeaderBindings(schema); err == nil {
			t.Errorf("%s: expected the annotation to be rejected", name)
		}
	}
}

func TestHTTPHeaderBuilderOption(t *testing.T) {
	tool := NewTool("q", "query",
		String("region", "region", Required(), HTTPHeader("Region")),
		Integer("limit", "limit", HTTPHeader("Limit")),
		Boolean("dry", "dry", HTTPHeader("Dry")),
		Object("scope", "scope", String("tenant", "t", HTTPHeader("Tenant"))),
		String("query", "free text"),
	)
	b, err := schemaHeaderBindings(tool.buildSchema())
	if err != nil || len(b) != 4 {
		t.Fatalf("builder schema bindings: %v %+v", err, b)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("RegisterTool accepted duplicate header names")
		}
	}()
	NewServer("s", "1").RegisterTool(NewTool("dup", "dup",
		String("a", "a", HTTPHeader("X")), String("b", "b", HTTPHeader("x"))),
		func(context.Context, *ToolRequest) (*ToolResponse, error) { return nil, nil })
}

// ---- value encoding (Value Encoding table) --------------------------------

func TestMcpParamHeadersEncoding(t *testing.T) {
	b := func(name, typ string, path ...string) headerBinding {
		return headerBinding{name: name, typ: typ, path: path}
	}
	h, err := mcpParamHeaders([]headerBinding{
		b("Region", "string", "region"),
		b("Greeting", "string", "greeting"),
		b("Text", "string", "text"),
		b("Val", "string", "val"),
		b("Count", "integer", "count"),
		b("Neg", "integer", "neg"),
		b("Dry", "boolean", "dry"),
		b("Missing", "string", "missing"),
		b("Null", "string", "null"),
		b("Tenant", "string", "scope", "tenant"),
	}, map[string]any{
		"region": "us-west1", "greeting": "Hello, 世界", "text": " padded ", "val": "=?base64?literal?=",
		"count": float64(42), "neg": -7, "dry": true, "null": nil,
		"scope": map[string]any{"tenant": "acme"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"Mcp-Param-Region":   "us-west1",
		"Mcp-Param-Greeting": "=?base64?SGVsbG8sIOS4lueVjA==?=",
		"Mcp-Param-Text":     "=?base64?IHBhZGRlZCA=?=",
		"Mcp-Param-Val":      "=?base64?PT9iYXNlNjQ/bGl0ZXJhbD89?=",
		"Mcp-Param-Count":    "42",
		"Mcp-Param-Neg":      "-7",
		"Mcp-Param-Dry":      "true",
		"Mcp-Param-Tenant":   "acme",
	} {
		if got := h.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	for _, absent := range []string{"Mcp-Param-Missing", "Mcp-Param-Null"} {
		if _, ok := h[absent]; ok {
			t.Errorf("%s must be omitted", absent)
		}
	}

	for name, v := range map[string]any{"fraction": 1.5, "beyond safe range": float64(1 << 60), "string for integer": "42"} {
		if _, err := mcpParamHeaders([]headerBinding{b("N", "integer", "n")}, map[string]any{"n": v}); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// ---- server validation ------------------------------------------------------

func headerToolServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	s := NewServer("hdr", "1")
	s.RegisterTool(NewTool("route", "routed",
		String("region", "region", Required(), HTTPHeader("Region")),
		Integer("limit", "limit", HTTPHeader("Limit")),
		String("query", "q"),
	), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		r, _ := req.String("region")
		return NewToolResponseText("ok:" + r), nil
	})
	ts := httptest.NewServer(http.HandlerFunc(s.HandleRequest))
	t.Cleanup(ts.Close)
	return s, ts
}

func modernCall(t *testing.T, url, tool string, args map[string]any, headers map[string]string) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "c1", "method": "tools/call", "params": map[string]any{
		"name": tool, "arguments": args,
		"_meta": map[string]any{"io.modelcontextprotocol/protocolVersion": MCPProtocolVersionModern, "io.modelcontextprotocol/clientCapabilities": map[string]any{}},
	}})
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set(headerProtocolVersion, MCPProtocolVersionModern)
	req.Header.Set(headerMcpMethod, "tools/call")
	req.Header.Set(headerMcpName, tool)
	for k, v := range headers {
		req.Header[http.CanonicalHeaderKey(k)] = []string{v} // raw, unvalidated
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestServerValidatesMcpParamHeaders(t *testing.T) {
	_, ts := headerToolServer(t)
	ok := func(args map[string]any, headers map[string]string) {
		t.Helper()
		status, out := modernCall(t, ts.URL, "route", args, headers)
		if status != 200 || out["error"] != nil {
			t.Fatalf("args=%v headers=%v: got %d %v, want success", args, headers, status, out)
		}
	}
	mismatch := func(args map[string]any, headers map[string]string) {
		t.Helper()
		status, out := modernCall(t, ts.URL, "route", args, headers)
		e, _ := out["error"].(map[string]any)
		if status != 400 || e == nil || e["code"] != float64(ErrorCodeHeaderMismatch) {
			t.Fatalf("args=%v headers=%v: got %d %v, want 400 HeaderMismatch", args, headers, status, out)
		}
	}

	ok(map[string]any{"region": "us-west1"}, map[string]string{"Mcp-Param-Region": "us-west1"})
	ok(map[string]any{"region": "us-west1"}, map[string]string{"mcp-param-region": "us-west1"}) // header names are case-insensitive
	ok(map[string]any{"region": "Hello, 世界"}, map[string]string{"Mcp-Param-Region": "=?base64?SGVsbG8sIOS4lueVjA==?="})
	ok(map[string]any{"region": "r", "limit": 42}, map[string]string{"Mcp-Param-Region": "r", "Mcp-Param-Limit": "42.0"}) // numeric compare
	ok(map[string]any{"region": "r", "limit": nil}, map[string]string{"Mcp-Param-Region": "r"})                           // null: header omitted

	mismatch(map[string]any{"region": "us-west1"}, nil)                                                         // value in body, header omitted
	mismatch(map[string]any{"region": "us-west1"}, map[string]string{"Mcp-Param-Region": "eu-west1"})           // different value
	mismatch(map[string]any{"region": "Region"}, map[string]string{"Mcp-Param-Region": "region"})               // values are case-sensitive
	mismatch(map[string]any{"region": "x"}, map[string]string{"Mcp-Param-Region": "=?base64?%%%?="})            // malformed Base64
	mismatch(map[string]any{"region": "x"}, map[string]string{"Mcp-Param-Region": "x", "Mcp-Param-Limit": "5"}) // header without a value
	mismatch(map[string]any{"region": "r", "limit": 42}, map[string]string{"Mcp-Param-Region": "r", "Mcp-Param-Limit": "43"})
}

// Invalid characters in a recognised header are rejected. Go's client refuses
// to send them, so this goes through the handler directly.
func TestServerRejectsInvalidHeaderCharacters(t *testing.T) {
	s, _ := headerToolServer(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"route","arguments":{"region":"é"},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerProtocolVersion, MCPProtocolVersionModern)
	req.Header.Set(headerMcpMethod, "tools/call")
	req.Header.Set(headerMcpName, "route")
	req.Header["Mcp-Param-Region"] = []string{"é"} // raw non-ASCII bytes
	rec := httptest.NewRecorder()
	s.HandleRequest(rec, req)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "invalid characters") {
		t.Fatalf("got %d %s, want 400 invalid characters", rec.Code, rec.Body.String())
	}
}

// Legacy-era requests predate x-mcp-header and are not checked.
func TestServerLegacyIgnoresMcpParamHeaders(t *testing.T) {
	_, ts := headerToolServer(t)
	resp := legacyToolCallArgs(t, ts.URL, "route", map[string]any{"region": "us"})
	if resp["error"] != nil {
		t.Fatalf("Legacy call rejected: %v", resp)
	}
}

func legacyToolCallArgs(t *testing.T, url, tool string, args map[string]any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerProtocolVersion, MCPProtocolVersionLatest)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

// Tools served by a context ToolProvider are validated too.
type headerProvider struct{}

func (headerProvider) GetTools(context.Context) ([]MCPTool, error) {
	return []MCPTool{{Name: "p_route", InputSchema: map[string]any{"type": "object", "properties": map[string]any{
		"region": map[string]any{"type": "string", "x-mcp-header": "Region"},
	}}}}, nil
}

func (headerProvider) ExecuteTool(ctx context.Context, name string, params map[string]any) (*ToolResponse, error) {
	if name != "p_route" {
		return nil, nil
	}
	return NewToolResponseText("provided"), nil
}

func TestServerValidatesProviderToolHeaders(t *testing.T) {
	s := NewServer("p", "1")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.HandleRequest(w, r.WithContext(WithToolProviders(r.Context(), headerProvider{})))
	}))
	defer ts.Close()
	if status, out := modernCall(t, ts.URL, "p_route", map[string]any{"region": "x"}, nil); status != 400 {
		t.Fatalf("missing header on provider tool: %d %v", status, out)
	}
	if status, out := modernCall(t, ts.URL, "p_route", map[string]any{"region": "x"}, map[string]string{"Mcp-Param-Region": "x"}); status != 200 || out["error"] != nil {
		t.Fatalf("valid provider call: %d %v", status, out)
	}
}

// ---- client ---------------------------------------------------------------------

// A Modern client mirrors annotated arguments into Mcp-Param-* headers, so a
// real server that enforces them accepts the call.
func TestClientMirrorsMcpParamHeaders(t *testing.T) {
	s, _ := headerToolServer(t)
	var mu sync.Mutex
	var seen http.Header
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(headerMcpMethod) == "tools/call" {
			mu.Lock()
			seen = r.Header.Clone()
			mu.Unlock()
		}
		s.HandleRequest(w, r)
	}))
	defer ts.Close()

	c := NewClient(ts.URL, nil, "")
	resp, err := c.CallTool(context.Background(), "route", map[string]any{"region": "Hello, 世界", "limit": 10, "query": "free text"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if resp.Content[0].Text != "ok:Hello, 世界" {
		t.Fatalf("result = %+v", resp)
	}
	mu.Lock()
	defer mu.Unlock()
	if seen.Get("Mcp-Param-Region") != "=?base64?SGVsbG8sIOS4lueVjA==?=" || seen.Get("Mcp-Param-Limit") != "10" {
		t.Fatalf("mirrored headers = %v", seen)
	}
	if _, ok := seen["Mcp-Param-Query"]; ok {
		t.Fatal("unannotated argument was mirrored")
	}
}

// A namespaced client resolves the annotations under the namespaced name.
func TestClientMirrorsMcpParamHeadersNamespaced(t *testing.T) {
	_, ts := headerToolServer(t)
	c := NewClient(ts.URL, nil, "ns")
	if _, err := c.CallTool(context.Background(), "ns__route", map[string]any{"region": "r"}); err != nil {
		t.Fatalf("namespaced CallTool: %v", err)
	}
}

// fakeModernServer answers server/discover as a Modern server and hands
// everything else to handle.
func fakeModernServer(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, rpc map[string]any)) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var rpc map[string]any
		json.Unmarshal(body, &rpc)
		w.Header().Set("Content-Type", "application/json")
		if rpc["method"] == "server/discover" {
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc["id"], "result": map[string]any{
				"resultType": "complete", "supportedVersions": []string{MCPProtocolVersionModern}, "capabilities": map[string]any{},
			}})
			return
		}
		handle(w, r, rpc)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// Tool definitions with invalid annotations are excluded from ListTools; the
// valid ones are kept.
func TestClientExcludesInvalidHeaderTools(t *testing.T) {
	ts := fakeModernServer(t, func(w http.ResponseWriter, r *http.Request, rpc map[string]any) {
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc["id"], "result": map[string]any{"resultType": "complete", "tools": []any{
			map[string]any{"name": "good", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"r": map[string]any{"type": "string", "x-mcp-header": "R"}}}},
			map[string]any{"name": "bad_number", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "number", "x-mcp-header": "N"}}}},
			map[string]any{"name": "bad_items", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "array", "items": map[string]any{"type": "string", "x-mcp-header": "A"}}}}},
			map[string]any{"name": "plain", "inputSchema": map[string]any{"type": "object"}},
		}}})
	})
	tools, err := NewClient(ts.URL, nil, "").ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	if strings.Join(names, ",") != "good,plain" {
		t.Fatalf("tools = %v, want [good plain]", names)
	}
}

// On HeaderMismatch the client refreshes tools/list (the annotations changed)
// and retries once with the right headers.
func TestClientRetriesAfterHeaderMismatch(t *testing.T) {
	var mu sync.Mutex
	lists, calls := 0, 0
	ts := fakeModernServer(t, func(w http.ResponseWriter, r *http.Request, rpc map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		switch rpc["method"] {
		case "tools/list":
			lists++
			prop := map[string]any{"type": "string"}
			if lists > 1 { // the server added the annotation after the first listing
				prop["x-mcp-header"] = "Region"
			}
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc["id"], "result": map[string]any{"resultType": "complete", "tools": []any{
				map[string]any{"name": "route", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"region": prop}}},
			}}})
		case "tools/call":
			calls++
			if r.Header.Get("Mcp-Param-Region") != "eu" {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc["id"], "error": map[string]any{"code": ErrorCodeHeaderMismatch, "message": "Header mismatch"}})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc["id"], "result": map[string]any{"resultType": "complete", "content": []any{map[string]any{"type": "text", "text": "routed"}}}})
		}
	})
	resp, err := NewClient(ts.URL, nil, "").CallTool(context.Background(), "route", map[string]any{"region": "eu"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if resp.Content[0].Text != "routed" || lists != 2 || calls != 2 {
		t.Fatalf("result=%+v lists=%d calls=%d, want routed after one refresh and one retry", resp, lists, calls)
	}
}

// A Legacy-era client does not mirror (the mechanism is 2026-07-28 only).
func TestLegacyClientDoesNotMirror(t *testing.T) {
	s, _ := headerToolServer(t)
	var sawParam bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"server/discover"`) {
			http.Error(w, "unknown", http.StatusBadRequest)
			return
		}
		for k := range r.Header {
			if strings.HasPrefix(k, "Mcp-Param-") {
				sawParam = true
			}
		}
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		s.HandleRequest(w, r)
	}))
	defer ts.Close()
	if _, err := NewClient(ts.URL, nil, "").CallTool(context.Background(), "route", map[string]any{"region": "r"}); err != nil {
		t.Fatal(err)
	}
	if sawParam {
		t.Fatal("Legacy client sent Mcp-Param-* headers")
	}
}
