package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClient_ReadResource covers Client.ReadResource end to end against a
// real server (static resource), including the not-found error path — no
// existing test drives resources/read through the Client.
func TestClient_ReadResource(t *testing.T) {
	srv := NewServer("svc", "1")
	srv.RegisterResource(
		NewResource("config://app", "App Config", "the config", "text/plain"),
		func(ctx context.Context, req *ResourceRequest) (*ResourceResponse, error) {
			return NewResourceResponseText(req.URI(), "hello-config", "text/plain"), nil
		},
	)
	ts := httptest.NewServer(http.HandlerFunc(srv.HandleRequest))
	defer ts.Close()

	c := NewClient(ts.URL, nil, "")
	ctx := context.Background()

	resp, err := c.ReadResource(ctx, "config://app")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(resp.Contents) != 1 || resp.Contents[0].Text != "hello-config" {
		t.Fatalf("unexpected resource response: %+v", resp)
	}

	if _, err := c.ReadResource(ctx, "config://missing"); err == nil {
		t.Error("expected an error reading an unknown resource")
	}
}

// TestClient_ListResourceTemplates covers Client.ListResourceTemplates, which
// no existing test drives through the Client (only Server.ListResourceTemplates
// is tested directly).
func TestClient_ListResourceTemplates(t *testing.T) {
	srv := NewServer("svc", "1")
	srv.RegisterResourceTemplate(
		NewResourceTemplate("user://{id}", "User", "a user", "application/json"),
		func(ctx context.Context, req *ResourceRequest) (*ResourceResponse, error) {
			id, _ := req.String("id")
			return NewResourceResponseText(req.URI(), id, "application/json"), nil
		},
	)
	ts := httptest.NewServer(http.HandlerFunc(srv.HandleRequest))
	defer ts.Close()

	c := NewClient(ts.URL, nil, "")
	templates, err := c.ListResourceTemplates(context.Background())
	if err != nil {
		t.Fatalf("ListResourceTemplates: %v", err)
	}
	if len(templates) != 1 || templates[0].URITemplate != "user://{id}" {
		t.Fatalf("unexpected templates: %+v", templates)
	}
}

// TestClient_GetPrompt covers Client.GetPrompt end to end, including the
// missing-required-argument error path.
func TestClient_GetPrompt(t *testing.T) {
	srv := NewServer("svc", "1")
	srv.RegisterPrompt(
		NewPrompt("greet", "Greet someone").Argument("name", "who to greet", true),
		func(ctx context.Context, req *PromptRequest) (*PromptResponse, error) {
			name, _ := req.String("name")
			return &PromptResponse{
				Messages: []PromptMessage{
					{Role: PromptRoleUser, Content: PromptMessageContent{Type: "text", Text: "Hello, " + name}},
				},
			}, nil
		},
	)
	ts := httptest.NewServer(http.HandlerFunc(srv.HandleRequest))
	defer ts.Close()

	c := NewClient(ts.URL, nil, "")
	ctx := context.Background()

	resp, err := c.GetPrompt(ctx, "greet", map[string]string{"name": "Ada"})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	if len(resp.Messages) != 1 || resp.Messages[0].Content.Text != "Hello, Ada" {
		t.Fatalf("unexpected prompt response: %+v", resp)
	}

	if _, err := c.GetPrompt(ctx, "greet", nil); err == nil {
		t.Error("expected an error for missing required argument")
	}

	if _, err := c.GetPrompt(ctx, "no_such_prompt", nil); err == nil {
		t.Error("expected an error for unknown prompt")
	}
}

// TestArgs_Arg covers the Args fluent builder's Arg method, which no test
// exercises (CallTool call sites always pass a plain map literal).
func TestArgs_Arg(t *testing.T) {
	args := Args{}.Arg("city", "London").Arg("units", "metric")
	if len(args) != 2 || args["city"] != "London" || args["units"] != "metric" {
		t.Errorf("Args = %+v, want city=London units=metric", args)
	}

	// Arg returns the same map for chaining.
	extended := args.Arg("extra", 1)
	if len(extended) != 3 {
		t.Errorf("expected chained Arg to add to the same map, got %+v", extended)
	}
}
