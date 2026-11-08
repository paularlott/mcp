package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// Numeric arguments arriving over HTTP read as numbers through ToolRequest
// (ids are decoded exactly, but params stay ordinary float64 values).
func TestHTTPNumericArgumentsReadAsNumbers(t *testing.T) {
	s := NewServer("n", "1")
	s.RegisterTool(NewTool("sum", "adds", Number("a", "", Required()), Number("b", "", Required()), Integer("n", "", Required())), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		a, err := req.Float("a")
		if err != nil {
			return nil, err
		}
		b, err := req.Float("b")
		if err != nil {
			return nil, err
		}
		n, err := req.Int("n")
		if err != nil {
			return nil, err
		}
		return NewToolResponseStructured(map[string]any{"sum": a + b, "n": n}), nil
	})
	ts := httptest.NewServer(http.HandlerFunc(s.HandleRequest))
	defer ts.Close()
	post := func(body string) string {
		req, _ := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(headerProtocolVersion, "2025-11-25")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return string(b)
	}
	out := post(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"sum","arguments":{"a":2.5,"b":3,"n":42}}}`)
	if !strings.Contains(out, `"sum":5.5`) || !strings.Contains(out, `"n":42`) || strings.Contains(out, "isError") {
		t.Fatalf("got %s", out)
	}
	// A wrong type is an input validation error: a tool execution error
	// the model can read, not a protocol error.
	out = post(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sum","arguments":{"a":"x","b":3,"n":1}}}`)
	if !strings.Contains(out, `"isError":true`) || !strings.Contains(out, "parameter a must be of type number") || strings.Contains(out, `"error"`) {
		t.Fatalf("got %s", out)
	}
}

func convertSchema() any {
	return NewTool("t", "t",
		Number("limit", ""), Integer("count", ""), Boolean("flag", ""), String("label", ""),
		IntegerArray("ids", ""), Object("filter", "", Integer("min", "")),
	).BuildSchema()
}

// A string that is exactly a number or boolean, sent for a parameter that
// declares that type, reaches the handler as that type; the caller's map is
// left as it was.
func TestCheckArgumentsConvertsScalarStrings(t *testing.T) {
	in := map[string]any{
		"limit": "2.5", "count": "10", "flag": "true", "label": "10",
		"ids": []any{"1", 2.0}, "filter": map[string]any{"min": "-3"},
	}
	got, err := checkArguments(convertSchema(), in)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"limit": 2.5, "count": 10.0, "flag": true, "label": "10",
		"ids": []any{1.0, 2.0}, "filter": map[string]any{"min": -3.0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
	if in["count"] != "10" || in["ids"].([]any)[0] != "1" || in["filter"].(map[string]any)["min"] != "-3" {
		t.Fatalf("the caller's arguments were modified: %#v", in)
	}
}

// Anything that isn't exactly a value of the declared type is still
// refused, and the rest of the schema applies as written.
func TestCheckArgumentsRefusesWhatItCannotConvert(t *testing.T) {
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"count": "2.5"}, "parameter count must be of type integer"},
		{map[string]any{"limit": "10abc"}, "parameter limit must be of type number"},
		{map[string]any{"limit": " 10"}, "parameter limit must be of type number"},
		{map[string]any{"limit": "+10"}, "parameter limit must be of type number"},
		{map[string]any{"limit": "0x10"}, "parameter limit must be of type number"},
		{map[string]any{"limit": "Inf"}, "parameter limit must be of type number"},
		{map[string]any{"limit": "1e999"}, "parameter limit must be of type number"},
		{map[string]any{"flag": "yes"}, "parameter flag must be of type boolean"},
		{map[string]any{"flag": "1"}, "parameter flag must be of type boolean"},
		{map[string]any{"limit": 1, "extra": "x"}, "parameter extra unexpected property"},
	} {
		_, err := checkArguments(convertSchema(), tc.args)
		var toolErr *ToolError
		if !errors.As(err, &toolErr) || toolErr.Code != ErrorCodeInvalidParams || !strings.Contains(toolErr.Message, tc.want) {
			t.Errorf("%v: got %v, want an input validation error containing %q", tc.args, err, tc.want)
		}
	}
}

// Over tools/call, directly and through execute_tool, a quoted number runs
// the tool with the number; an unknown argument is a tool execution error
// the model can read, never a protocol error.
func TestToolsCallQuotedScalarsAndUnknownArguments(t *testing.T) {
	s := NewServer("n", "1")
	handler := func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		n, err := req.Int("limit")
		if err != nil {
			return nil, err
		}
		return NewToolResponseText(fmt.Sprintf("limit=%d", n)), nil
	}
	s.RegisterTool(NewTool("visible", "v", Integer("limit", "", Required())), handler)
	s.RegisterTool(NewTool("hidden", "h", Integer("limit", "", Required())).Discoverable("hidden"), handler)
	ts := httptest.NewServer(http.HandlerFunc(s.HandleRequest))
	defer ts.Close()
	call := func(params string) string {
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":` + params + `}`
		req, _ := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(headerProtocolVersion, "2025-11-25")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return string(b)
	}
	for _, tc := range []struct{ params, want string }{
		{`{"name":"visible","arguments":{"limit":"10"}}`, `"text":"limit=10"`},
		{`{"name":"execute_tool","arguments":{"name":"hidden","parameters":{"limit":"10"}}}`, `"text":"limit=10"`},
		{`{"name":"execute_tool","arguments":{"name":"hidden","parameters":"{\"limit\":\"10\"}"}}`, `"text":"limit=10"`},
		{`{"name":"tool_search","arguments":{"query":"hidden","max_results":"1"}}`, `\"name\":\"hidden\"`},
	} {
		if out := call(tc.params); !strings.Contains(out, tc.want) || strings.Contains(out, `"isError":true`) {
			t.Errorf("%s: got %s, want %s", tc.params, out, tc.want)
		}
	}
	for _, params := range []string{
		`{"name":"visible","arguments":{"limit":10,"extra":"x"}}`,
		`{"name":"execute_tool","arguments":{"name":"hidden","parameters":{"limit":10,"extra":"x"}}}`,
	} {
		out := call(params)
		if !strings.Contains(out, `"isError":true`) || !strings.Contains(out, "parameter extra unexpected property") || strings.Contains(out, `"error":`) {
			t.Errorf("%s: got %s, want an isError result naming extra", params, out)
		}
	}
}

// The discovery tools' definitions are built once and are the schemas
// their calls are checked against.
func TestDiscoveryToolDefinitionsBuiltOnce(t *testing.T) {
	s := NewServer("n", "1")
	a, b := s.getDiscoveryTools(), s.getDiscoveryTools()
	for i := range a {
		if reflect.ValueOf(a[i].InputSchema).UnsafePointer() != reflect.ValueOf(b[i].InputSchema).UnsafePointer() {
			t.Fatalf("%s: inputSchema rebuilt on each call", a[i].Name)
		}
	}
	if a[0].Name != ToolSearchName || !reflect.DeepEqual(a[0].InputSchema, toolSearchTool().BuildSchema()) {
		t.Fatalf("tool_search definition: %+v", a[0])
	}
	if a[1].Name != ExecuteToolName || !reflect.DeepEqual(a[1].InputSchema, executeToolTool().BuildSchema()) {
		t.Fatalf("execute_tool definition: %+v", a[1])
	}
	if allocs := testing.AllocsPerRun(100, func() { s.getDiscoveryTools() }); allocs > 1 {
		t.Fatalf("getDiscoveryTools allocates %v times per call, want at most 1", allocs)
	}
}

// inputSchema is never null in tools/list: a provider tool that gave none
// is listed as taking any object.
func TestToolsListNeverSendsNullInputSchema(t *testing.T) {
	s := NewServer("n", "1")
	ctx := WithToolProviders(context.Background(), &mockToolProvider{tools: []MCPTool{{Name: "bare", Description: "no schema"}}})
	tools := s.ListToolsWithContext(ctx)
	if len(tools) != 1 || !reflect.DeepEqual(tools[0].InputSchema, map[string]any{"type": "object"}) {
		t.Fatalf("got %+v", tools)
	}
}
