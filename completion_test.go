package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// completion/complete: prompt and template completers, the 100-value cap,
// context.arguments, error codes, capability declaration, both eras, HTTP
// and stdio.

var testLanguages = []string{"go", "python", "pytorch", "pyside", "rust"}

// completionServer has a prompt with two completed arguments (framework
// depends on language through context.arguments) and one without a
// completer, plus a template with a completed variable.
func completionServer(t *testing.T) *Server {
	t.Helper()
	s := NewServer("complete", "1")
	s.RegisterPrompt(NewPrompt("code_review", "Review code").
		Argument("language", "Language", true).
		Complete(func(ctx context.Context, req *CompletionRequest) ([]string, error) {
			return FilterPrefix(testLanguages, req.Value()), nil
		}).
		Argument("framework", "Framework", false).
		Complete(func(ctx context.Context, req *CompletionRequest) ([]string, error) {
			lang, _ := req.ContextArg("language")
			if lang == "python" {
				return FilterPrefix([]string{"flask", "fastapi", "django"}, req.Value()), nil
			}
			return []string{}, nil
		}).
		Argument("notes", "Free text", false),
		func(ctx context.Context, req *PromptRequest) (*PromptResponse, error) {
			return NewPromptResponseText("ok"), nil
		})
	s.RegisterResourceTemplate(NewResourceTemplate("file:///{dir}/{name}", "Files", "", "").
		Complete("dir", func(ctx context.Context, req *CompletionRequest) ([]string, error) {
			return FilterPrefix([]string{"src", "docs", "scripts"}, req.Value()), nil
		}),
		func(ctx context.Context, req *ResourceRequest) (*ResourceResponse, error) {
			return NewResourceResponseText(req.URI(), "x", "text/plain"), nil
		})
	s.RegisterResource(NewResource("config://app", "Config", "", ""), func(ctx context.Context, req *ResourceRequest) (*ResourceResponse, error) {
		return NewResourceResponseText(req.URI(), "x", "text/plain"), nil
	})
	return s
}

func TestComplete_PromptAndTemplate(t *testing.T) {
	s := completionServer(t)
	ctx := context.Background()

	res, err := s.Complete(ctx, CompletionRef{Type: CompletionRefPrompt, Name: "code_review"}, NewCompletionRequest("language", "py", nil))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Values, ",") != "python,pytorch,pyside" || res.Total == nil || *res.Total != 3 || res.HasMore {
		t.Fatalf("language completion = %+v", res)
	}

	// context.arguments reaches the completer.
	res, err = s.Complete(ctx, CompletionRef{Type: CompletionRefPrompt, Name: "code_review"}, NewCompletionRequest("framework", "f", map[string]string{"language": "python"}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Values, ",") != "flask,fastapi" {
		t.Fatalf("framework completion with context = %v", res.Values)
	}
	res, _ = s.Complete(ctx, CompletionRef{Type: CompletionRefPrompt, Name: "code_review"}, NewCompletionRequest("framework", "f", nil))
	if len(res.Values) != 0 {
		t.Fatalf("framework completion without context = %v", res.Values)
	}

	// A declared argument with no completer: no suggestions, not an error.
	res, err = s.Complete(ctx, CompletionRef{Type: CompletionRefPrompt, Name: "code_review"}, NewCompletionRequest("notes", "a", nil))
	if err != nil || res.Values == nil || len(res.Values) != 0 {
		t.Fatalf("notes completion = %+v, %v", res, err)
	}

	res, err = s.Complete(ctx, CompletionRef{Type: CompletionRefResource, URI: "file:///{dir}/{name}"}, NewCompletionRequest("dir", "s", nil))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Values, ",") != "src,scripts" {
		t.Fatalf("template completion = %v", res.Values)
	}
	res, err = s.Complete(ctx, CompletionRef{Type: CompletionRefResource, URI: "file:///{dir}/{name}"}, NewCompletionRequest("name", "", nil))
	if err != nil || len(res.Values) != 0 {
		t.Fatalf("variable without completer = %+v, %v", res, err)
	}
}

func TestComplete_Errors(t *testing.T) {
	s := completionServer(t)
	ctx := context.Background()
	var te *ToolError

	if _, err := s.Complete(ctx, CompletionRef{Type: CompletionRefPrompt, Name: "nope"}, NewCompletionRequest("a", "", nil)); !errors.Is(err, ErrUnknownPrompt) {
		t.Fatalf("unknown prompt: %v", err)
	}
	if _, err := s.Complete(ctx, CompletionRef{Type: CompletionRefResource, URI: "file:///{x}"}, NewCompletionRequest("x", "", nil)); !errors.Is(err, ErrUnknownResource) {
		t.Fatalf("unknown template: %v", err)
	}
	if _, err := s.Complete(ctx, CompletionRef{Type: CompletionRefPrompt, Name: "code_review"}, NewCompletionRequest("bogus", "", nil)); !errors.As(err, &te) || te.Code != ErrorCodeInvalidParams {
		t.Fatalf("unknown prompt argument: %v", err)
	}
	if _, err := s.Complete(ctx, CompletionRef{Type: CompletionRefResource, URI: "file:///{dir}/{name}"}, NewCompletionRequest("bogus", "", nil)); !errors.As(err, &te) || te.Code != ErrorCodeInvalidParams {
		t.Fatalf("unknown template variable: %v", err)
	}
	if _, err := s.Complete(ctx, CompletionRef{Type: CompletionRefResource, URI: "config://app"}, NewCompletionRequest("x", "", nil)); !errors.As(err, &te) || te.Code != ErrorCodeInvalidParams {
		t.Fatalf("static resource has no arguments: %v", err)
	}
	if _, err := s.Complete(ctx, CompletionRef{Type: "ref/tool"}, NewCompletionRequest("x", "", nil)); !errors.As(err, &te) || te.Code != ErrorCodeInvalidParams {
		t.Fatalf("bad ref type: %v", err)
	}
}

func TestComplete_CapsAt100(t *testing.T) {
	s := NewServer("cap", "1")
	many := make([]string, 150)
	for i := range many {
		many[i] = fmt.Sprintf("v%03d", i)
	}
	s.RegisterPrompt(NewPrompt("p", "").Argument("a", "", false).Complete(func(ctx context.Context, req *CompletionRequest) ([]string, error) {
		return many, nil
	}), func(ctx context.Context, req *PromptRequest) (*PromptResponse, error) {
		return NewPromptResponseText("ok"), nil
	})

	res, err := s.Complete(context.Background(), CompletionRef{Type: CompletionRefPrompt, Name: "p"}, NewCompletionRequest("a", "", nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Values) != MaxCompletionValues || res.Values[99] != "v099" || !res.HasMore || res.Total == nil || *res.Total != 150 {
		t.Fatalf("capped result: len=%d hasMore=%v total=%v", len(res.Values), res.HasMore, res.Total)
	}

	// The cap also applies to a provider-built result; a larger total it
	// reports is kept.
	total := 1000
	capped := capCompletionResult(&CompletionResult{Values: many, Total: &total})
	if len(capped.Values) != 100 || !capped.HasMore || *capped.Total != 1000 {
		t.Fatalf("provider result: %+v", capped)
	}
	if exact := NewCompletionResult(many[:100]); exact.HasMore || len(exact.Values) != 100 {
		t.Fatalf("exactly 100 must not set hasMore: %+v", exact)
	}
}

func TestComplete_CompleterErrors(t *testing.T) {
	s := NewServer("errs", "1")
	s.RegisterPrompt(NewPrompt("p", "").
		Argument("bad", "", false).Complete(func(ctx context.Context, req *CompletionRequest) ([]string, error) {
		return nil, NewToolErrorInvalidParams("value too short")
	}).
		Argument("boom", "", false).Complete(func(ctx context.Context, req *CompletionRequest) ([]string, error) {
		return nil, errors.New("db down")
	}), func(ctx context.Context, req *PromptRequest) (*PromptResponse, error) {
		return NewPromptResponseText("ok"), nil
	})

	resp := legacyComplete(t, s, map[string]any{"ref": map[string]any{"type": "ref/prompt", "name": "p"}, "argument": map[string]any{"name": "bad", "value": ""}})
	if resp.Error == nil || resp.Error.Code != ErrorCodeInvalidParams || resp.Error.Message != "value too short" {
		t.Fatalf("ToolError from completer: %+v", resp.Error)
	}
	resp = legacyComplete(t, s, map[string]any{"ref": map[string]any{"type": "ref/prompt", "name": "p"}, "argument": map[string]any{"name": "boom", "value": ""}})
	if resp.Error == nil || resp.Error.Code != ErrorCodeInternalError || strings.Contains(resp.Error.Message, "db down") {
		t.Fatalf("plain error from completer: %+v (its text must not reach the client)", resp.Error)
	}
}

func TestComplete_BuilderPanics(t *testing.T) {
	mustPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: expected panic", name)
			}
		}()
		fn()
	}
	noop := func(ctx context.Context, req *CompletionRequest) ([]string, error) { return nil, nil }
	mustPanic("prompt without argument", func() { NewPrompt("p", "").Complete(noop) })
	mustPanic("template without variable", func() { NewResourceTemplate("x://{a}", "", "", "").Complete("b", noop) })
}

// --- wire: Legacy HTTP --------------------------------------------------

func postLegacy(t *testing.T, s *Server, method string, params any) MCPResponse {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.HandleRequest(rr, req)
	var resp MCPResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %s: %v (%s)", method, err, rr.Body.String())
	}
	return resp
}

func legacyComplete(t *testing.T, s *Server, params any) MCPResponse {
	t.Helper()
	return postLegacy(t, s, "completion/complete", params)
}

func initializeCaps(t *testing.T, s *Server, version string) map[string]any {
	t.Helper()
	resp := postLegacy(t, s, "initialize", map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "t", "version": "1"},
	})
	res, _ := resp.Result.(map[string]any)
	caps, _ := res["capabilities"].(map[string]any)
	if caps == nil {
		t.Fatalf("initialize %s: no capabilities in %+v", version, resp)
	}
	return caps
}

func TestComplete_LegacyHTTP(t *testing.T) {
	s := completionServer(t)
	for _, v := range []string{"2025-03-26", "2025-06-18", "2025-11-25"} {
		caps := initializeCaps(t, s, v)
		if c, ok := caps["completions"].(map[string]any); !ok || len(c) != 0 {
			t.Errorf("%s: completions capability = %#v, want {}", v, caps["completions"])
		}
	}

	resp := legacyComplete(t, s, map[string]any{
		"ref":      map[string]any{"type": "ref/prompt", "name": "code_review"},
		"argument": map[string]any{"name": "framework", "value": "d"},
		"context":  map[string]any{"arguments": map[string]any{"language": "python"}},
	})
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}
	res := resp.Result.(map[string]any)
	if _, ok := res["resultType"]; ok {
		t.Error("Legacy result must not carry resultType")
	}
	completion := res["completion"].(map[string]any)
	if fmt.Sprint(completion["values"]) != "[django]" || completion["total"] != float64(1) || completion["hasMore"] != false {
		t.Fatalf("completion = %#v", completion)
	}

	cases := []struct {
		name   string
		params any
	}{
		{"unknown prompt", map[string]any{"ref": map[string]any{"type": "ref/prompt", "name": "nope"}, "argument": map[string]any{"name": "a", "value": ""}}},
		{"unknown template", map[string]any{"ref": map[string]any{"type": "ref/resource", "uri": "x://{y}"}, "argument": map[string]any{"name": "y", "value": ""}}},
		{"unknown argument", map[string]any{"ref": map[string]any{"type": "ref/prompt", "name": "code_review"}, "argument": map[string]any{"name": "zzz", "value": ""}}},
		{"missing ref", map[string]any{"argument": map[string]any{"name": "a", "value": ""}}},
		{"bad ref type", map[string]any{"ref": map[string]any{"type": "ref/tool", "name": "x"}, "argument": map[string]any{"name": "a", "value": ""}}},
		{"missing ref name", map[string]any{"ref": map[string]any{"type": "ref/prompt"}, "argument": map[string]any{"name": "a", "value": ""}}},
		{"missing argument", map[string]any{"ref": map[string]any{"type": "ref/prompt", "name": "code_review"}}},
		{"missing argument value", map[string]any{"ref": map[string]any{"type": "ref/prompt", "name": "code_review"}, "argument": map[string]any{"name": "language"}}},
		{"params not an object", "oops"},
	}
	for _, tc := range cases {
		resp := legacyComplete(t, s, tc.params)
		if resp.Error == nil || resp.Error.Code != ErrorCodeInvalidParams {
			t.Errorf("%s: error = %+v, want -32602", tc.name, resp.Error)
		}
	}
}

// Without completers the capability is absent and the method doesn't exist.
func TestComplete_NoCompletersMeansNoCapability(t *testing.T) {
	s := NewServer("plain", "1")
	s.RegisterPrompt(NewPrompt("p", "").Argument("a", "", false), func(ctx context.Context, req *PromptRequest) (*PromptResponse, error) {
		return NewPromptResponseText("ok"), nil
	})
	if s.HasCompletions(context.Background()) {
		t.Fatal("HasCompletions with no completers")
	}
	if _, ok := initializeCaps(t, s, MCPProtocolVersionLatest)["completions"]; ok {
		t.Error("completions capability declared with no completers")
	}
	resp := legacyComplete(t, s, map[string]any{"ref": map[string]any{"type": "ref/prompt", "name": "p"}, "argument": map[string]any{"name": "a", "value": ""}})
	if resp.Error == nil || resp.Error.Code != ErrorCodeMethodNotFound {
		t.Fatalf("Legacy: error = %+v, want -32601", resp.Error)
	}

	// Modern: an unknown method is HTTP 404 with -32601.
	rr := httptest.NewRecorder()
	s.HandleRequest(rr, modernRequest(t, "/mcp", "completion/complete", map[string]any{
		"ref": map[string]any{"type": "ref/prompt", "name": "p"}, "argument": map[string]any{"name": "a", "value": ""},
	}))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("Modern status = %d, want 404 (%s)", rr.Code, rr.Body.String())
	}
	if e, _ := decodeMCPResponse(t, rr.Body.Bytes())["error"].(map[string]any); e["code"] != float64(ErrorCodeMethodNotFound) {
		t.Fatalf("Modern error = %#v", e)
	}
	rr = httptest.NewRecorder()
	s.HandleRequest(rr, modernRequest(t, "/mcp", "server/discover", nil))
	caps := decodeMCPResponse(t, rr.Body.Bytes())["result"].(map[string]any)["capabilities"].(map[string]any)
	if _, ok := caps["completions"]; ok {
		t.Error("server/discover declares completions with no completers")
	}

	// Registering a completer later turns it on.
	s.RegisterPrompt(NewPrompt("q", "").Argument("a", "", false).Complete(func(ctx context.Context, req *CompletionRequest) ([]string, error) {
		return []string{"x"}, nil
	}), func(ctx context.Context, req *PromptRequest) (*PromptResponse, error) {
		return NewPromptResponseText("ok"), nil
	})
	if _, ok := initializeCaps(t, s, MCPProtocolVersionLatest)["completions"]; !ok {
		t.Error("completions capability missing after registering a completer")
	}
}

// --- wire: Modern HTTP --------------------------------------------------

func TestComplete_ModernHTTP(t *testing.T) {
	s := completionServer(t)

	rr := httptest.NewRecorder()
	s.HandleRequest(rr, modernRequest(t, "/mcp", "server/discover", nil))
	caps := decodeMCPResponse(t, rr.Body.Bytes())["result"].(map[string]any)["capabilities"].(map[string]any)
	if c, ok := caps["completions"].(map[string]any); !ok || len(c) != 0 {
		t.Fatalf("server/discover completions = %#v, want {}", caps["completions"])
	}

	rr = httptest.NewRecorder()
	s.HandleRequest(rr, modernRequest(t, "/mcp", "completion/complete", map[string]any{
		"ref":      map[string]any{"type": "ref/resource", "uri": "file:///{dir}/{name}"},
		"argument": map[string]any{"name": "dir", "value": "d"},
	}))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rr.Code, rr.Body.String())
	}
	res := decodeMCPResponse(t, rr.Body.Bytes())["result"].(map[string]any)
	if res["resultType"] != "complete" {
		t.Errorf("resultType = %v", res["resultType"])
	}
	if meta, _ := res["_meta"].(map[string]any); meta[metaKeyServerInfo] == nil {
		t.Errorf("missing _meta serverInfo: %#v", res["_meta"])
	}
	if _, ok := res["ttlMs"]; ok {
		t.Error("completion/complete is not a cacheable operation; no ttlMs")
	}
	if fmt.Sprint(res["completion"].(map[string]any)["values"]) != "[docs]" {
		t.Fatalf("completion = %#v", res["completion"])
	}

	// Method-level errors stay HTTP 200 with -32602.
	rr = httptest.NewRecorder()
	s.HandleRequest(rr, modernRequest(t, "/mcp", "completion/complete", map[string]any{
		"ref":      map[string]any{"type": "ref/prompt", "name": "nope"},
		"argument": map[string]any{"name": "a", "value": ""},
	}))
	if rr.Code != http.StatusOK {
		t.Fatalf("unknown prompt status = %d", rr.Code)
	}
	if e, _ := decodeMCPResponse(t, rr.Body.Bytes())["error"].(map[string]any); e["code"] != float64(ErrorCodeInvalidParams) {
		t.Fatalf("unknown prompt error = %#v", e)
	}
}

// The library's own client completes over both eras.
func TestComplete_ClientRoundTrip(t *testing.T) {
	s := completionServer(t)
	ts := httptest.NewServer(http.HandlerFunc(s.HandleRequest))
	defer ts.Close()

	c := NewClient(ts.URL, nil, "")
	res, err := c.Complete(context.Background(), CompletionRef{Type: CompletionRefPrompt, Name: "code_review"}, "framework", "fa", map[string]string{"language": "python"})
	if err != nil {
		t.Fatal(err)
	}
	if c.era != eraModern || fmt.Sprint(res.Values) != "[fastapi]" {
		t.Fatalf("era=%v values=%v", c.era, res.Values)
	}

	_, err = c.Complete(context.Background(), CompletionRef{Type: CompletionRefPrompt, Name: "nope"}, "a", "", nil)
	var te *ToolError
	if !errors.As(err, &te) || te.Code != ErrorCodeInvalidParams {
		t.Fatalf("unknown prompt via client: %v", err)
	}
}

// --- wire: stdio --------------------------------------------------------

func TestComplete_Stdio(t *testing.T) {
	h := newStdioHarness(t, completionServer(t))
	h.send(t, stdioInit2025)
	if l := h.next(t, 2*time.Second); !strings.Contains(l, `"completions":{}`) {
		t.Fatalf("initialize: %s", l)
	}

	h.send(t, `{"jsonrpc":"2.0","id":2,"method":"completion/complete","params":{"ref":{"type":"ref/prompt","name":"code_review"},"argument":{"name":"language","value":"ru"}}}`)
	l := h.next(t, 2*time.Second)
	if !strings.Contains(l, `"values":["rust"]`) || strings.Contains(l, "resultType") {
		t.Fatalf("Legacy stdio completion: %s", l)
	}

	modernMeta := `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}`
	h.send(t, `{"jsonrpc":"2.0","id":3,"method":"completion/complete","params":{`+modernMeta+`,"ref":{"type":"ref/resource","uri":"file:///{dir}/{name}"},"argument":{"name":"dir","value":"sr"}}}`)
	l = h.next(t, 2*time.Second)
	if !strings.Contains(l, `"values":["src"]`) || !strings.Contains(l, `"resultType":"complete"`) {
		t.Fatalf("Modern stdio completion: %s", l)
	}

	h.send(t, `{"jsonrpc":"2.0","id":4,"method":"server/discover","params":{`+modernMeta+`}}`)
	if l := h.next(t, 2*time.Second); !strings.Contains(l, `"completions":{}`) {
		t.Fatalf("stdio server/discover: %s", l)
	}

	h.send(t, `{"jsonrpc":"2.0","id":5,"method":"completion/complete","params":{"ref":{"type":"ref/prompt","name":"code_review"},"argument":{"name":"nope","value":""}}}`)
	if l := h.next(t, 2*time.Second); !strings.Contains(l, `"code":-32602`) {
		t.Fatalf("stdio unknown argument: %s", l)
	}
}

func TestComplete_StdioNoCompleters(t *testing.T) {
	h := newStdioHarness(t, stdioToolServer(t))
	h.send(t, stdioInit2025)
	if l := h.next(t, 2*time.Second); strings.Contains(l, "completions") {
		t.Fatalf("initialize declares completions: %s", l)
	}
	h.send(t, `{"jsonrpc":"2.0","id":2,"method":"completion/complete","params":{"ref":{"type":"ref/prompt","name":"p"},"argument":{"name":"a","value":""}}}`)
	if l := h.next(t, 2*time.Second); !strings.Contains(l, `"code":-32601`) {
		t.Fatalf("stdio without completers: %s", l)
	}
}

// --- providers ----------------------------------------------------------

type completingPromptProvider struct{ complete bool }

func (p *completingPromptProvider) GetPrompts(ctx context.Context) ([]MCPPrompt, error) {
	return []MCPPrompt{{Name: "dyn", Arguments: []MCPPromptArgument{{Name: "city"}}}}, nil
}

func (p *completingPromptProvider) GetPrompt(ctx context.Context, name string, args map[string]string) (*PromptResponse, error) {
	return NewPromptResponseText("ok"), nil
}

type promptCompleter struct{ completingPromptProvider }

func (p *promptCompleter) CompletePrompt(ctx context.Context, name string, req *CompletionRequest) (*CompletionResult, error) {
	if req.Argument() != "city" {
		return nil, NewToolErrorInvalidParams("unknown argument")
	}
	many := make([]string, 120)
	for i := range many {
		many[i] = fmt.Sprintf("%s%d", req.Value(), i)
	}
	return &CompletionResult{Values: many}, nil
}

type completingResourceProvider struct{}

func (completingResourceProvider) GetResources(ctx context.Context) (*ProvidedResources, error) {
	return &ProvidedResources{Templates: []MCPResourceTemplate{{URITemplate: "user://{id}", Name: "users"}}}, nil
}

func (completingResourceProvider) ReadResource(ctx context.Context, uri string) (*ResourceResponse, error) {
	return nil, ErrUnknownResource
}

func (completingResourceProvider) CompleteResourceTemplate(ctx context.Context, uriTemplate string, req *CompletionRequest) (*CompletionResult, error) {
	return NewCompletionResult(FilterPrefix([]string{"alice", "alan", "bob"}, req.Value())), nil
}

func TestComplete_Providers(t *testing.T) {
	s := NewServer("prov", "1")

	// A provider without the completion interface: listed prompts exist for
	// completion (no suggestions) but don't turn the capability on.
	plain := WithPromptProviders(context.Background(), &completingPromptProvider{})
	if s.HasCompletions(plain) {
		t.Fatal("non-completing provider must not declare completions")
	}
	res, err := s.Complete(plain, CompletionRef{Type: CompletionRefPrompt, Name: "dyn"}, NewCompletionRequest("city", "x", nil))
	if err != nil || len(res.Values) != 0 {
		t.Fatalf("plain provider: %+v, %v", res, err)
	}
	if _, err := s.Complete(plain, CompletionRef{Type: CompletionRefPrompt, Name: "dyn"}, NewCompletionRequest("zip", "", nil)); err == nil {
		t.Fatal("plain provider: undeclared argument must fail")
	}

	ctx := WithResourceProviders(WithPromptProviders(context.Background(), &promptCompleter{}), completingResourceProvider{})
	if !s.HasCompletions(ctx) {
		t.Fatal("completing providers must declare completions")
	}
	res, err = s.Complete(ctx, CompletionRef{Type: CompletionRefPrompt, Name: "dyn"}, NewCompletionRequest("city", "p", nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Values) != 100 || !res.HasMore || res.Total == nil || *res.Total != 120 {
		t.Fatalf("provider result not capped: len=%d hasMore=%v total=%v", len(res.Values), res.HasMore, res.Total)
	}
	res, err = s.Complete(ctx, CompletionRef{Type: CompletionRefResource, URI: "user://{id}"}, NewCompletionRequest("id", "al", nil))
	if err != nil || fmt.Sprint(res.Values) != "[alice alan]" {
		t.Fatalf("resource provider: %+v, %v", res, err)
	}

	// Capability through HTTP initialize with providers attached by middleware.
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
		"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "1"},
	}})
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.HandleRequest(rr, req)
	if !strings.Contains(rr.Body.String(), `"completions":{}`) {
		t.Fatalf("initialize with completing providers: %s", rr.Body.String())
	}
}
