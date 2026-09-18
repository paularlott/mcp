package mcp

import (
	"context"
	"testing"
)

// TestInternalRegistry_Search covers internalRegistry.Search directly (the
// thin wrapper around SearchWithAdditionalTools with nil additional/listed
// tools). Every other test reaches SearchWithAdditionalTools via the server's
// handleToolSearch, never Search itself.
func TestInternalRegistry_Search(t *testing.T) {
	r := newInternalRegistry()
	r.RegisterTool(NewTool("send_email", "Send an email", String("to", "to")), nil, "email")
	r.RegisterTool(NewTool("send_sms", "Send an SMS", String("to", "to")), nil, "sms")

	results := r.Search(context.Background(), "email", 10)
	if len(results) == 0 {
		t.Fatalf("expected at least one result for 'email'")
	}
	if results[0].Name != "send_email" {
		t.Errorf("top result = %q, want send_email", results[0].Name)
	}

	// Empty query lists everything.
	all := r.Search(context.Background(), "", 10)
	if len(all) != 2 {
		t.Errorf("expected 2 results for empty query, got %d", len(all))
	}
}

// TestInternalRegistry_GetTool covers GetTool's three paths: a directly
// registered tool, a tool found via a context ToolProvider, and the
// ErrUnknownTool miss.
func TestInternalRegistry_GetTool(t *testing.T) {
	r := newInternalRegistry()
	r.RegisterTool(NewTool("local_tool", "a local tool"), nil)

	// Direct hit.
	tool, err := r.GetTool(context.Background(), "local_tool")
	if err != nil {
		t.Fatalf("GetTool(local_tool): %v", err)
	}
	if tool.Name != "local_tool" {
		t.Errorf("tool.Name = %q, want local_tool", tool.Name)
	}

	// Miss: no provider, no registration.
	if _, err := r.GetTool(context.Background(), "nope"); err != ErrUnknownTool {
		t.Errorf("GetTool(nope) = %v, want ErrUnknownTool", err)
	}

	// Hit via a context ToolProvider.
	provider := &fakeToolProvider{
		tools: []MCPTool{{Name: "provider_tool", Description: "from provider", Visibility: ToolVisibilityNative}},
	}
	ctx := WithToolProviders(context.Background(), provider)
	tool, err = r.GetTool(ctx, "provider_tool")
	if err != nil {
		t.Fatalf("GetTool(provider_tool): %v", err)
	}
	if tool.Name != "provider_tool" {
		t.Errorf("tool.Name = %q, want provider_tool", tool.Name)
	}

	// A provider that errors on GetTools is skipped, not fatal.
	erroringCtx := WithToolProviders(context.Background(), &fakeToolProvider{err: errBoom})
	if _, err := r.GetTool(erroringCtx, "anything"); err != ErrUnknownTool {
		t.Errorf("GetTool via erroring provider = %v, want ErrUnknownTool", err)
	}
}

// TestInternalRegistry_CallTool covers CallTool's registered-handler path,
// the context-provider path (including a provider that returns
// ErrUnknownTool and one that returns a real error), and the final miss.
func TestInternalRegistry_CallTool(t *testing.T) {
	r := newInternalRegistry()
	r.RegisterTool(NewTool("echo", "echo"), func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("echoed"), nil
	})

	resp, err := r.CallTool(context.Background(), "echo", nil)
	if err != nil {
		t.Fatalf("CallTool(echo): %v", err)
	}
	if resp.Content[0].Text != "echoed" {
		t.Errorf("resp = %+v, want echoed", resp)
	}

	// Miss with no providers.
	if _, err := r.CallTool(context.Background(), "missing", nil); err != ErrUnknownTool {
		t.Errorf("CallTool(missing) = %v, want ErrUnknownTool", err)
	}

	// Provider handles the tool.
	provider := &fakeToolProvider{
		execName: "provider_tool",
		execResp: NewToolResponseText("from provider"),
	}
	ctx := WithToolProviders(context.Background(), provider)
	resp, err = r.CallTool(ctx, "provider_tool", nil)
	if err != nil {
		t.Fatalf("CallTool(provider_tool): %v", err)
	}
	if resp.Content[0].Text != "from provider" {
		t.Errorf("resp = %+v, want from provider", resp)
	}

	// Provider returns ErrUnknownTool -> registry also reports miss.
	missProvider := &fakeToolProvider{execErr: ErrUnknownTool}
	ctx2 := WithToolProviders(context.Background(), missProvider)
	if _, err := r.CallTool(ctx2, "whatever", nil); err != ErrUnknownTool {
		t.Errorf("CallTool via miss provider = %v, want ErrUnknownTool", err)
	}

	// Provider returns a genuine error -> propagated.
	errProvider := &fakeToolProvider{execErr: errBoom}
	ctx3 := WithToolProviders(context.Background(), errProvider)
	if _, err := r.CallTool(ctx3, "whatever", nil); err != errBoom {
		t.Errorf("CallTool via erroring provider = %v, want errBoom", err)
	}
}

var errBoom = &ToolError{Code: ErrorCodeInternalError, Message: "boom"}

// fakeToolProvider is a minimal ToolProvider for exercising GetTool/CallTool's
// provider-delegation paths directly against internalRegistry (as opposed to
// the higher-level provider tests in tool_provider_test.go, which exercise
// the free functions like listToolsFromProviders instead).
type fakeToolProvider struct {
	tools []MCPTool
	err   error

	execName string
	execResp *ToolResponse
	execErr  error
}

func (f *fakeToolProvider) GetTools(ctx context.Context) ([]MCPTool, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.tools, nil
}

func (f *fakeToolProvider) ExecuteTool(ctx context.Context, name string, params map[string]any) (*ToolResponse, error) {
	if f.execErr != nil {
		return nil, f.execErr
	}
	if name == f.execName {
		return f.execResp, nil
	}
	return nil, ErrUnknownTool
}
