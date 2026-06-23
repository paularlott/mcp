package mcp

import "context"

// MultiProvider combines multiple ToolProviders into a single ToolProvider.
//
// Composition semantics:
//
//   - GetTools aggregates the tools from every provider in order. To stay
//     transparent with the server's own list path (listToolsFromProviders), a
//     provider whose GetTools returns an error is skipped rather than failing
//     the whole set, so one broken provider never hides the others' tools.
//     GetTools therefore does not surface provider errors. Duplicate tool names
//     are NOT de-duplicated here; the server's list/search logic already
//     de-duplicates by name.
//
//   - ExecuteTool dispatches to each provider in order using the
//     "skip on miss, abort on real error, first success wins" contract:
//     1. A provider that does not handle the tool signals a miss by
//     returning (nil, nil) or (nil, ErrUnknownTool). The next provider
//     is tried.
//     2. A provider that returns any other error aborts dispatch and that
//     error is returned to the caller.
//     3. The first provider that returns a non-nil result wins.
//     If no provider handles the tool, ExecuteTool returns (nil, nil), which
//     the server treats as ErrUnknownTool.
//
// MultiProvider is safe for concurrent use if its underlying providers are.
type MultiProvider struct {
	providers []ToolProvider
}

// Ensure MultiProvider implements ToolProvider.
var _ ToolProvider = (*MultiProvider)(nil)

// NewMultiProvider combines the given providers into a single ToolProvider.
// Nil providers are skipped. If no non-nil providers are supplied, nil is
// returned so callers can attach the result with WithToolProviders without a
// guard (attaching a nil provider is a no-op-safe pattern callers should still
// check for).
func NewMultiProvider(providers ...ToolProvider) *MultiProvider {
	filtered := make([]ToolProvider, 0, len(providers))
	for _, provider := range providers {
		if provider != nil {
			filtered = append(filtered, provider)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return &MultiProvider{providers: filtered}
}

// GetTools returns the aggregated tools from all providers in order. A provider
// whose GetTools returns an error is skipped (matching the server's list path),
// so GetTools itself never returns an error.
func (p *MultiProvider) GetTools(ctx context.Context) ([]MCPTool, error) {
	var tools []MCPTool
	for _, provider := range p.providers {
		providerTools, err := provider.GetTools(ctx)
		if err != nil {
			// Skip the erroring provider; do not hide the others' tools.
			continue
		}
		tools = append(tools, providerTools...)
	}
	return tools, nil
}

// ExecuteTool dispatches the call to the first provider that handles the tool.
// See the MultiProvider type docs for the full skip/abort/first-success contract.
func (p *MultiProvider) ExecuteTool(ctx context.Context, name string, params map[string]any) (*ToolResponse, error) {
	for _, provider := range p.providers {
		result, err := provider.ExecuteTool(ctx, name, params)
		if err == ErrUnknownTool {
			continue
		}
		if err != nil {
			return nil, err
		}
		if result != nil {
			return result, nil
		}
	}
	return nil, nil
}

// ProviderFuncs adapts plain functions to the ToolProvider interface, so a
// provider can be defined inline without a dedicated type.
//
//	p := &mcp.ProviderFuncs{
//	    GetToolsFunc: func(ctx context.Context) ([]mcp.MCPTool, error) { ... },
//	    ExecuteToolFunc: func(ctx context.Context, name string, params map[string]any) (*mcp.ToolResponse, error) { ... },
//	}
//
// A nil GetToolsFunc yields no tools; a nil ExecuteToolFunc reports the tool as
// not handled (returns nil, ErrUnknownTool).
type ProviderFuncs struct {
	GetToolsFunc    func(ctx context.Context) ([]MCPTool, error)
	ExecuteToolFunc func(ctx context.Context, name string, params map[string]any) (*ToolResponse, error)
}

// Ensure ProviderFuncs implements ToolProvider.
var _ ToolProvider = (*ProviderFuncs)(nil)

// GetTools calls GetToolsFunc, or returns nil if it is unset.
func (p *ProviderFuncs) GetTools(ctx context.Context) ([]MCPTool, error) {
	if p.GetToolsFunc != nil {
		return p.GetToolsFunc(ctx)
	}
	return nil, nil
}

// ExecuteTool calls ExecuteToolFunc, or reports the tool as not handled if it is unset.
func (p *ProviderFuncs) ExecuteTool(ctx context.Context, name string, params map[string]any) (*ToolResponse, error) {
	if p.ExecuteToolFunc != nil {
		return p.ExecuteToolFunc(ctx, name, params)
	}
	return nil, ErrUnknownTool
}
