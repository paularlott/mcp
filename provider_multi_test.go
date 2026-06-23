package mcp

import (
	"context"
	"errors"
	"testing"
)

// staticProvider is a simple ToolProvider for tests.
type staticProvider struct {
	tools     []MCPTool
	results   map[string]any
	execErr   error
	getErr    error
	missAsUnk bool // return ErrUnknownTool instead of (nil,nil) for misses
	executed  []string
}

func (p *staticProvider) GetTools(ctx context.Context) ([]MCPTool, error) {
	if p.getErr != nil {
		return nil, p.getErr
	}
	return p.tools, nil
}

func (p *staticProvider) ExecuteTool(ctx context.Context, name string, params map[string]any) (*ToolResponse, error) {
	p.executed = append(p.executed, name)
	if p.execErr != nil {
		return nil, p.execErr
	}
	if res, ok := p.results[name]; ok {
		return NewToolResponseAuto(res), nil
	}
	if p.missAsUnk {
		return nil, ErrUnknownTool
	}
	return nil, nil
}

func TestMultiProvider_NilHandling(t *testing.T) {
	if NewMultiProvider() != nil {
		t.Fatal("expected nil for no providers")
	}
	if NewMultiProvider(nil, nil) != nil {
		t.Fatal("expected nil when all providers nil")
	}
	p := NewMultiProvider(nil, &staticProvider{}, nil)
	if p == nil || len(p.providers) != 1 {
		t.Fatalf("expected 1 non-nil provider, got %+v", p)
	}
}

func TestMultiProvider_GetToolsAggregatesInOrder(t *testing.T) {
	a := &staticProvider{tools: []MCPTool{{Name: "a"}}}
	b := &staticProvider{tools: []MCPTool{{Name: "b"}, {Name: "c"}}}
	mp := NewMultiProvider(a, b)

	tools, err := mp.GetTools(context.Background())
	if err != nil {
		t.Fatalf("GetTools: %v", err)
	}
	if len(tools) != 3 || tools[0].Name != "a" || tools[1].Name != "b" || tools[2].Name != "c" {
		t.Fatalf("unexpected aggregation: %+v", tools)
	}
}

func TestMultiProvider_GetToolsSkipsErroringProvider(t *testing.T) {
	boom := errors.New("boom")
	a := &staticProvider{getErr: boom}
	b := &staticProvider{tools: []MCPTool{{Name: "b"}}}
	mp := NewMultiProvider(a, b)

	// The erroring provider is skipped; the good provider's tools still surface,
	// and no error is returned (matches the server's list path).
	tools, err := mp.GetTools(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "b" {
		t.Fatalf("expected only b's tools, got %+v", tools)
	}
}

func TestMultiProvider_ExecuteFirstSuccessWins(t *testing.T) {
	a := &staticProvider{missAsUnk: true}
	b := &staticProvider{results: map[string]any{"x": "from-b"}}
	c := &staticProvider{results: map[string]any{"x": "from-c"}}
	mp := NewMultiProvider(a, b, c)

	res, err := mp.ExecuteTool(context.Background(), "x", nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res == nil || res.Content[0].Text != "from-b" {
		t.Fatalf("expected from-b, got %v", res)
	}
	if len(c.executed) != 0 {
		t.Fatalf("expected c to not be consulted after b handled the tool")
	}
}

func TestMultiProvider_ExecuteSkipsNilMiss(t *testing.T) {
	a := &staticProvider{} // returns (nil,nil) miss
	b := &staticProvider{results: map[string]any{"x": "from-b"}}
	mp := NewMultiProvider(a, b)

	res, err := mp.ExecuteTool(context.Background(), "x", nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res == nil || res.Content[0].Text != "from-b" {
		t.Fatalf("expected from-b, got %v", res)
	}
}

func TestMultiProvider_ExecuteAbortsOnRealError(t *testing.T) {
	boom := errors.New("boom")
	a := &staticProvider{execErr: boom}
	b := &staticProvider{results: map[string]any{"x": "from-b"}}
	mp := NewMultiProvider(a, b)

	if _, err := mp.ExecuteTool(context.Background(), "x", nil); err != boom {
		t.Fatalf("expected boom, got %v", err)
	}
	if len(b.executed) != 0 {
		t.Fatalf("expected dispatch to abort before consulting b")
	}
}

func TestMultiProvider_ExecuteAllMissReturnsNil(t *testing.T) {
	a := &staticProvider{missAsUnk: true}
	b := &staticProvider{}
	mp := NewMultiProvider(a, b)

	res, err := mp.ExecuteTool(context.Background(), "x", nil)
	if err != nil {
		t.Fatalf("expected nil error on all-miss, got %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil result on all-miss, got %v", res)
	}
}

func TestProviderFuncs(t *testing.T) {
	pf := &ProviderFuncs{
		GetToolsFunc: func(ctx context.Context) ([]MCPTool, error) {
			return []MCPTool{{Name: "pf"}}, nil
		},
		ExecuteToolFunc: func(ctx context.Context, name string, params map[string]any) (*ToolResponse, error) {
			return NewToolResponseText("ok"), nil
		},
	}
	tools, err := pf.GetTools(context.Background())
	if err != nil || len(tools) != 1 || tools[0].Name != "pf" {
		t.Fatalf("unexpected GetTools: %+v err=%v", tools, err)
	}
	res, err := pf.ExecuteTool(context.Background(), "pf", nil)
	if err != nil || res == nil || res.Content[0].Text != "ok" {
		t.Fatalf("unexpected ExecuteTool: %v err=%v", res, err)
	}

	// Unset funcs: no tools, tool not handled.
	empty := &ProviderFuncs{}
	if tools, err := empty.GetTools(context.Background()); err != nil || tools != nil {
		t.Fatalf("expected nil tools, got %+v err=%v", tools, err)
	}
	if _, err := empty.ExecuteTool(context.Background(), "x", nil); err != ErrUnknownTool {
		t.Fatalf("expected ErrUnknownTool, got %v", err)
	}
}
