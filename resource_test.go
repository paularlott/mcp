package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// doMCP sends method/params to s over HTTP and returns the decoded result map.
func doMCP(t *testing.T, s *Server, method string, params any) map[string]any {
	t.Helper()
	return doMCPWithCtx(t, s, method, params, nil)
}

// doMCPErr sends method/params to s and returns the JSON-RPC error (or nil).
func doMCPErr(t *testing.T, s *Server, method string, params any) *MCPError {
	t.Helper()
	h := http.HandlerFunc(s.HandleRequest)
	body := MCPRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: params}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var rpc MCPResponse
	if err := json.NewDecoder(rr.Body).Decode(&rpc); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return rpc.Error
}

// doMCPWithCtx is like doMCP but lets the caller attach extra context (e.g.
// resource providers) to the request before it is dispatched.
func doMCPWithCtx(t *testing.T, s *Server, method string, params any, ctx context.Context) map[string]any {
	t.Helper()
	h := http.HandlerFunc(s.HandleRequest)
	body := MCPRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: params}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if ctx != nil {
		req = req.WithContext(ctx)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var rpc MCPResponse
	if err := json.NewDecoder(rr.Body).Decode(&rpc); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if rpc.Error != nil {
		t.Fatalf("unexpected error %d: %s", rpc.Error.Code, rpc.Error.Message)
	}
	return rpc.Result.(map[string]any)
}

// staticResourceServer builds a server with a couple of registered resources
// and a template for use across tests.
func staticResourceServer(t *testing.T) *Server {
	t.Helper()
	s := NewServer("rs", "1")
	s.RegisterResource(
		NewResource("config://app", "App Config", "app configuration", "application/json"),
		func(ctx context.Context, uri string) (*ResourceResponse, error) {
			return NewResourceResponseText(uri, `{"ok":true}`, "application/json"), nil
		},
	)
	s.RegisterResource(
		NewResource("logo://x", "Logo", "a logo", "image/png"),
		func(ctx context.Context, uri string) (*ResourceResponse, error) {
			return NewResourceResponseBlob(uri, []byte{0x89, 0x50, 0x4e, 0x47}, "image/png"), nil
		},
	)
	s.RegisterResourceTemplate(
		NewResourceTemplate("user://{id}", "User Profile", "user profile by id", "application/json"),
		func(ctx context.Context, uri string) (*ResourceResponse, error) {
			return NewResourceResponseText(uri, `{"id":"`+uri+`"}`, "application/json"), nil
		},
	)
	return s
}

func TestResourcesListHTTP(t *testing.T) {
	s := staticResourceServer(t)

	res := doMCP(t, s, "resources/list", nil)
	raw, _ := json.Marshal(res["resources"])
	var resources []MCPResource
	if err := json.Unmarshal(raw, &resources); err != nil {
		t.Fatalf("unmarshal resources: %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(resources))
	}
	// Sorted by URI: config://app, logo://x
	if resources[0].URI != "config://app" || resources[1].URI != "logo://x" {
		t.Fatalf("unexpected resource order: %+v", resources)
	}
	if resources[0].Name != "App Config" || resources[0].MimeType != "application/json" {
		t.Fatalf("unexpected resource descriptor: %+v", resources[0])
	}
}

func TestResourcesTemplatesListHTTP(t *testing.T) {
	s := staticResourceServer(t)

	res := doMCP(t, s, "resources/templates/list", nil)
	raw, _ := json.Marshal(res["resourceTemplates"])
	var templates []MCPResourceTemplate
	if err := json.Unmarshal(raw, &templates); err != nil {
		t.Fatalf("unmarshal templates: %v", err)
	}
	if len(templates) != 1 || templates[0].URITemplate != "user://{id}" {
		t.Fatalf("unexpected templates: %+v", templates)
	}
}

func TestResourceReadStaticHTTP(t *testing.T) {
	s := staticResourceServer(t)

	res := doMCP(t, s, "resources/read", map[string]any{"uri": "config://app"})
	raw, _ := json.Marshal(res)
	var rr ResourceResponse
	if err := json.Unmarshal(raw, &rr); err != nil {
		t.Fatalf("unmarshal resource response: %v", err)
	}
	if len(rr.Contents) != 1 || rr.Contents[0].Text != `{"ok":true}` {
		t.Fatalf("unexpected content: %+v", rr)
	}
}

func TestResourceReadBlobHTTP(t *testing.T) {
	s := staticResourceServer(t)

	res := doMCP(t, s, "resources/read", map[string]any{"uri": "logo://x"})
	raw, _ := json.Marshal(res)
	var rr ResourceResponse
	if err := json.Unmarshal(raw, &rr); err != nil {
		t.Fatalf("unmarshal resource response: %v", err)
	}
	if rr.Contents[0].Blob == "" {
		t.Fatalf("expected blob content, got %+v", rr.Contents[0])
	}
}

func TestResourceReadTemplateHTTP(t *testing.T) {
	s := staticResourceServer(t)

	// Reading an expanded template URI resolves through the handler.
	res := doMCP(t, s, "resources/read", map[string]any{"uri": "user://42"})
	raw, _ := json.Marshal(res)
	var rr ResourceResponse
	if err := json.Unmarshal(raw, &rr); err != nil {
		t.Fatalf("unmarshal resource response: %v", err)
	}
	if len(rr.Contents) != 1 || rr.Contents[0].Text != `{"id":"user://42"}` {
		t.Fatalf("unexpected template content: %+v", rr)
	}
}

func TestResourceReadUnknownHTTP(t *testing.T) {
	s := staticResourceServer(t)

	err := doMCPErr(t, s, "resources/read", map[string]any{"uri": "nope://missing"})
	if err == nil || err.Code != ErrorCodeInvalidParams {
		t.Fatalf("expected invalid params error, got %+v", err)
	}
}

func TestResourceReadMissingURI(t *testing.T) {
	s := staticResourceServer(t)

	err := doMCPErr(t, s, "resources/read", map[string]any{})
	if err == nil || err.Code != ErrorCodeInvalidParams {
		t.Fatalf("expected invalid params for missing uri, got %+v", err)
	}
}

func TestResourceReadUnknownDirect(t *testing.T) {
	s := staticResourceServer(t)
	_, err := s.ReadResource(context.Background(), "nope://missing")
	if err != ErrUnknownResource {
		t.Fatalf("expected ErrUnknownResource, got %v", err)
	}
}

func TestUnregisterResource(t *testing.T) {
	s := staticResourceServer(t)

	if !s.UnregisterResource("config://app") {
		t.Fatal("expected unregister to return true for existing resource")
	}
	if s.UnregisterResource("config://app") {
		t.Fatal("expected unregister to return false for missing resource")
	}

	// No longer listed or readable.
	res := doMCP(t, s, "resources/list", nil)
	raw, _ := json.Marshal(res["resources"])
	var resources []MCPResource
	_ = json.Unmarshal(raw, &resources)
	for _, r := range resources {
		if r.URI == "config://app" {
			t.Fatal("config://app should have been removed")
		}
	}
	err := doMCPErr(t, s, "resources/read", map[string]any{"uri": "config://app"})
	if err == nil {
		t.Fatal("expected error reading removed resource")
	}
}

// stubResourceProvider is a minimal ResourceProvider for tests.
type stubResourceProvider struct {
	resources []MCPResource
	templates []MCPResourceTemplate
	// read returns this content for a matching URI prefix.
	prefix string
	text   string
}

func (p *stubResourceProvider) GetResources(ctx context.Context) (*ProvidedResources, error) {
	return &ProvidedResources{Resources: p.resources, Templates: p.templates}, nil
}

func (p *stubResourceProvider) ReadResource(ctx context.Context, uri string) (*ResourceResponse, error) {
	if p.prefix != "" && len(uri) >= len(p.prefix) && uri[:len(p.prefix)] == p.prefix {
		return NewResourceResponseText(uri, p.text, "text/plain"), nil
	}
	return nil, ErrUnknownResource
}

func TestResourceProviderListsAndReads(t *testing.T) {
	s := NewServer("rs", "1")
	// A static resource the provider must not shadow.
	s.RegisterResource(
		NewResource("config://app", "App Config", "", "application/json"),
		func(ctx context.Context, uri string) (*ResourceResponse, error) {
			return NewResourceResponseText(uri, "static", "application/json"), nil
		},
	)

	provider := &stubResourceProvider{
		resources: []MCPResource{
			{URI: "user://me", Name: "Me"},
		},
		templates: []MCPResourceTemplate{
			{URITemplate: "doc://{id}", Name: "Doc"},
		},
		prefix: "user://",
		text:   "provider-content",
	}
	ctx := WithResourceProviders(context.Background(), provider)

	// resources/list: static + provider resource (deduped).
	resources := s.ListResources(ctx)
	uris := make(map[string]bool)
	for _, r := range resources {
		uris[r.URI] = true
	}
	if !uris["config://app"] || !uris["user://me"] {
		t.Fatalf("expected static + provider resource, got %v", uris)
	}

	// resources/templates/list: provider template.
	templates := s.ListResourceTemplates(ctx)
	if len(templates) != 1 || templates[0].URITemplate != "doc://{id}" {
		t.Fatalf("unexpected templates: %+v", templates)
	}

	// Read the provider resource.
	resp, err := s.ReadResource(ctx, "user://me")
	if err != nil {
		t.Fatalf("read provider resource: %v", err)
	}
	if resp.Contents[0].Text != "provider-content" {
		t.Fatalf("unexpected content: %s", resp.Contents[0].Text)
	}

	// Static resource still takes precedence (exact match before providers).
	resp, err = s.ReadResource(ctx, "config://app")
	if err != nil {
		t.Fatalf("read static resource: %v", err)
	}
	if resp.Contents[0].Text != "static" {
		t.Fatalf("expected static content, got %s", resp.Contents[0].Text)
	}

	// Unhandled URI falls through to ErrUnknownResource.
	if _, err := s.ReadResource(ctx, "nope://x"); err != ErrUnknownResource {
		t.Fatalf("expected ErrUnknownResource, got %v", err)
	}
}

func TestResourceProviderDedupStaticWins(t *testing.T) {
	s := NewServer("rs", "1")
	s.RegisterResource(
		NewResource("shared://x", "Static", "static wins", ""),
		func(ctx context.Context, uri string) (*ResourceResponse, error) {
			return NewResourceResponseText(uri, "static-value", ""), nil
		},
	)
	// Provider also claims the same URI; static must win on list and read.
	provider := &stubResourceProvider{
		resources: []MCPResource{{URI: "shared://x", Name: "Provider"}},
		prefix:    "shared://",
		text:      "provider-value",
	}
	ctx := WithResourceProviders(context.Background(), provider)

	resources := s.ListResources(ctx)
	count := 0
	for _, r := range resources {
		if r.URI == "shared://x" {
			count++
			if r.Name != "Static" {
				t.Fatalf("expected static descriptor to win, got %q", r.Name)
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected dedup to 1 entry, got %d", count)
	}

	resp, err := s.ReadResource(ctx, "shared://x")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.Contents[0].Text != "static-value" {
		t.Fatalf("expected static value, got %s", resp.Contents[0].Text)
	}
}

func TestResourcesCapabilityAlwaysAdvertised(t *testing.T) {
	s := NewServer("rs", "1") // no resources registered

	body := MCPRequest{JSONRPC: "2.0", ID: 1, Method: "initialize", Params: map[string]any{
		"protocolVersion": MCPProtocolVersionLatest,
		"clientInfo":      map[string]any{"name": "n", "version": "v"},
	}}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	http.HandlerFunc(s.HandleRequest).ServeHTTP(rr, req)

	var rpc MCPResponse
	_ = json.NewDecoder(rr.Body).Decode(&rpc)
	res := rpc.Result.(map[string]any)
	caps := res["capabilities"].(map[string]any)
	resources, ok := caps["resources"].(map[string]any)
	if !ok {
		t.Fatal("expected resources capability to be advertised even with no resources registered")
	}
	if resources["subscribe"] != false {
		t.Fatalf("expected subscribe=false, got %v", resources["subscribe"])
	}
}

func TestCompileResourceTemplate(t *testing.T) {
	cases := []struct {
		template string
		uri      string
		want     bool
	}{
		{"user://{id}", "user://42", true},
		{"user://{id}", "user://", false}, // placeholder requires >=1 char
		{"user://{id}", "other://42", false},
		{"repo://{owner}/{repo}", "repo://acme/widget", true},
		{"file:///{path}", "file:///a/b/c.txt", true},
		{"static://exact", "static://exact", true},
		{"static://exact", "static://other", false},
		// Literal with regex metachars is escaped, not interpreted.
		{"a.b://{x}", "axb://1", false},
	}
	for _, c := range cases {
		re := compileResourceTemplate(c.template)
		if re == nil {
			t.Fatalf("compile returned nil for %q", c.template)
		}
		if got := re.MatchString(c.uri); got != c.want {
			t.Fatalf("template %q match %q = %v, want %v", c.template, c.uri, got, c.want)
		}
	}
}
