package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func promptTestServer(t *testing.T) *Server {
	t.Helper()
	s := NewServer("ps", "1")
	s.RegisterPrompt(
		NewPrompt("greet", "Greet someone").Argument("name", "Name to greet", true),
		func(ctx context.Context, req *PromptRequest) (*PromptResponse, error) {
			name, _ := req.String("name")
			return NewPromptResponseText("Hello, " + name + "!"), nil
		},
	)
	s.RegisterPrompt(
		NewPrompt("multi", "A multi-message prompt").Argument("topic", "Topic", false),
		func(ctx context.Context, req *PromptRequest) (*PromptResponse, error) {
			topic := req.StringOr("topic", "nothing")
			return NewPromptResponseMessages(
				NewPromptTextMessage(PromptRoleAssistant, "I can talk about "+topic+"."),
				NewPromptTextMessage(PromptRoleUser, "Tell me about "+topic),
			), nil
		},
	)
	return s
}

func TestPromptsListHTTP(t *testing.T) {
	s := promptTestServer(t)

	res := doMCP(t, s, "prompts/list", nil)
	raw, _ := json.Marshal(res["prompts"])
	var prompts []MCPPrompt
	if err := json.Unmarshal(raw, &prompts); err != nil {
		t.Fatalf("unmarshal prompts: %v", err)
	}
	if len(prompts) != 2 {
		t.Fatalf("expected 2 prompts, got %d", len(prompts))
	}
	// Sorted by name: greet, multi
	if prompts[0].Name != "greet" || prompts[1].Name != "multi" {
		t.Fatalf("unexpected prompt order: %+v", prompts)
	}
	if len(prompts[0].Arguments) != 1 || !prompts[0].Arguments[0].Required {
		t.Fatalf("unexpected greet arguments: %+v", prompts[0].Arguments)
	}
}

func TestPromptGetHTTP(t *testing.T) {
	s := promptTestServer(t)

	res := doMCP(t, s, "prompts/get", map[string]any{
		"name":      "greet",
		"arguments": map[string]any{"name": "Ada"},
	})
	raw, _ := json.Marshal(res)
	var pr PromptResponse
	if err := json.Unmarshal(raw, &pr); err != nil {
		t.Fatalf("unmarshal prompt response: %v", err)
	}
	if len(pr.Messages) != 1 || pr.Messages[0].Content.Text != "Hello, Ada!" {
		t.Fatalf("unexpected messages: %+v", pr.Messages)
	}
	if pr.Messages[0].Role != "user" {
		t.Fatalf("expected user role, got %s", pr.Messages[0].Role)
	}
}

func TestPromptGetMultiMessage(t *testing.T) {
	s := promptTestServer(t)

	res := doMCP(t, s, "prompts/get", map[string]any{
		"name":      "multi",
		"arguments": map[string]any{"topic": "cats"},
	})
	raw, _ := json.Marshal(res)
	var pr PromptResponse
	if err := json.Unmarshal(raw, &pr); err != nil {
		t.Fatalf("unmarshal prompt response: %v", err)
	}
	if len(pr.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(pr.Messages))
	}
	if pr.Messages[0].Role != "assistant" || pr.Messages[1].Role != "user" {
		t.Fatalf("unexpected roles: %s, %s", pr.Messages[0].Role, pr.Messages[1].Role)
	}
}

func TestPromptGetMissingRequired(t *testing.T) {
	s := promptTestServer(t)

	err := doMCPErr(t, s, "prompts/get", map[string]any{
		"name":      "greet",
		"arguments": map[string]any{},
	})
	if err == nil || err.Code != ErrorCodeInvalidParams {
		t.Fatalf("expected invalid params for missing required arg, got %+v", err)
	}
	if !strings.Contains(err.Message, "name") {
		t.Fatalf("expected error to mention the missing arg, got %q", err.Message)
	}
}

func TestPromptGetMissingName(t *testing.T) {
	s := promptTestServer(t)

	err := doMCPErr(t, s, "prompts/get", map[string]any{})
	if err == nil || err.Code != ErrorCodeInvalidParams {
		t.Fatalf("expected invalid params for missing name, got %+v", err)
	}
}

func TestPromptGetUnknown(t *testing.T) {
	s := promptTestServer(t)

	err := doMCPErr(t, s, "prompts/get", map[string]any{"name": "nope"})
	if err == nil || err.Code != ErrorCodeInvalidParams {
		t.Fatalf("expected invalid params for unknown prompt, got %+v", err)
	}
}

func TestPromptGetUnknownDirect(t *testing.T) {
	s := promptTestServer(t)
	_, err := s.GetPrompt(context.Background(), "nope", nil)
	if err != ErrUnknownPrompt {
		t.Fatalf("expected ErrUnknownPrompt, got %v", err)
	}
}

func TestUnregisterPrompt(t *testing.T) {
	s := promptTestServer(t)

	if !s.UnregisterPrompt("greet") {
		t.Fatal("expected unregister to return true for existing prompt")
	}
	if s.UnregisterPrompt("greet") {
		t.Fatal("expected unregister to return false for missing prompt")
	}

	res := doMCP(t, s, "prompts/list", nil)
	raw, _ := json.Marshal(res["prompts"])
	var prompts []MCPPrompt
	_ = json.Unmarshal(raw, &prompts)
	for _, p := range prompts {
		if p.Name == "greet" {
			t.Fatal("greet should have been removed")
		}
	}
}

// stubPromptProvider is a minimal PromptProvider for tests.
type stubPromptProvider struct {
	prompts []MCPPrompt
	text    string
	// names this provider will render; anything else is a miss.
	handles map[string]bool
}

func (p *stubPromptProvider) GetPrompts(ctx context.Context) ([]MCPPrompt, error) {
	return p.prompts, nil
}

func (p *stubPromptProvider) GetPrompt(ctx context.Context, name string, args map[string]string) (*PromptResponse, error) {
	if p.handles[name] {
		return NewPromptResponseText(p.text), nil
	}
	return nil, ErrUnknownPrompt
}

func TestPromptProviderListsAndGets(t *testing.T) {
	s := NewServer("ps", "1")
	// A static prompt the provider must not shadow.
	s.RegisterPrompt(
		NewPrompt("static", "A static prompt"),
		func(ctx context.Context, req *PromptRequest) (*PromptResponse, error) {
			return NewPromptResponseText("static-content"), nil
		},
	)

	provider := &stubPromptProvider{
		prompts: []MCPPrompt{{Name: "per_user", Description: "A per-user prompt"}},
		text:    "provider-content",
		handles: map[string]bool{"per_user": true},
	}
	ctx := WithPromptProviders(context.Background(), provider)

	// prompts/list: static + provider (deduped).
	prompts := s.ListPrompts(ctx)
	names := make(map[string]bool)
	for _, p := range prompts {
		names[p.Name] = true
	}
	if !names["static"] || !names["per_user"] {
		t.Fatalf("expected static + provider prompt, got %v", names)
	}

	// Get the provider prompt.
	resp, err := s.GetPrompt(ctx, "per_user", nil)
	if err != nil {
		t.Fatalf("get provider prompt: %v", err)
	}
	if resp.Messages[0].Content.Text != "provider-content" {
		t.Fatalf("unexpected content: %s", resp.Messages[0].Content.Text)
	}

	// Static prompt takes precedence (exact name match before providers).
	resp, err = s.GetPrompt(ctx, "static", nil)
	if err != nil {
		t.Fatalf("get static prompt: %v", err)
	}
	if resp.Messages[0].Content.Text != "static-content" {
		t.Fatalf("expected static content, got %s", resp.Messages[0].Content.Text)
	}

	// Unhandled name falls through to ErrUnknownPrompt.
	if _, err := s.GetPrompt(ctx, "nope", nil); err != ErrUnknownPrompt {
		t.Fatalf("expected ErrUnknownPrompt, got %v", err)
	}
}

func TestPromptProviderDedupStaticWins(t *testing.T) {
	s := NewServer("ps", "1")
	s.RegisterPrompt(
		NewPrompt("shared", "Static"),
		func(ctx context.Context, req *PromptRequest) (*PromptResponse, error) {
			return NewPromptResponseText("static-value"), nil
		},
	)
	provider := &stubPromptProvider{
		prompts: []MCPPrompt{{Name: "shared", Description: "Provider"}},
		text:    "provider-value",
		handles: map[string]bool{"shared": true},
	}
	ctx := WithPromptProviders(context.Background(), provider)

	prompts := s.ListPrompts(ctx)
	count := 0
	for _, p := range prompts {
		if p.Name == "shared" {
			count++
			if p.Description != "Static" {
				t.Fatalf("expected static descriptor to win, got %q", p.Description)
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected dedup to 1 entry, got %d", count)
	}

	resp, err := s.GetPrompt(ctx, "shared", nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if resp.Messages[0].Content.Text != "static-value" {
		t.Fatalf("expected static value, got %s", resp.Messages[0].Content.Text)
	}
}

func TestPromptsCapabilityAdvertised(t *testing.T) {
	s := NewServer("ps", "1") // no prompts registered

	caps := s.buildCapabilities(MCPProtocolVersionLatest)
	if _, ok := caps.Prompts["listChanged"]; !ok {
		t.Fatal("expected listChanged in prompts capability")
	}
}
