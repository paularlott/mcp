package mcp

import (
	"context"
	"net/http"
	"strings"
)

// ToolModeHeader is the HTTP header used to specify tool mode
const ToolModeHeader = "X-MCP-Tool-Mode"

// ToolModeQueryParam is the query parameter used to specify tool mode (fallback)
const ToolModeQueryParam = "tool_mode"

// ToolModeDiscovery is the value that enables discovery mode
const ToolModeDiscovery = "discovery"

// ToolProvider is the interface that providers implement to expose tools.
// This is the unified interface used for both native MCP endpoints and discovery search.
type ToolProvider interface {
	// GetTools returns all tools available from this provider.
	// The context contains tenant/user information for filtering.
	// The Keywords field on MCPTool should be populated for discovery search functionality.
	GetTools(ctx context.Context) ([]MCPTool, error)

	// ExecuteTool executes a tool by name and returns the result.
	// Returns nil, ErrUnknownTool if the tool is not handled by this provider.
	ExecuteTool(ctx context.Context, name string, params map[string]interface{}) (interface{}, error)
}

// ToolListMode defines how tools are exposed in tools/list
type ToolListMode int

const (
	// ToolListModeDefault - Standard behavior: shows native tools + provider tools
	ToolListModeDefault ToolListMode = iota

	// ToolListModeForceOnDemand - Force all tools to be ondemand for this request.
	// Only tool_search and execute_tool appear in tools/list.
	// All other tools (native, provider, remote) are only available via search.
	ToolListModeForceOnDemand
)

// Discovery tool names
const (
	ToolSearchName  = "tool_search"
	ExecuteToolName = "execute_tool"
)

// toolProvidersKey is the context key for tool providers
type toolProvidersKey struct{}

// onDemandProvidersKey is the context key for ondemand tool providers
type onDemandProvidersKey struct{}

// toolListModeKey is the context key for tool list mode
type toolListModeKey struct{}

// WithToolProviders returns a context with the given tool providers attached.
// Multiple providers can be attached and all will be queried for tools.
// In normal mode:
//   - Native tools appear in tools/list
//   - Provider tools appear in tools/list
//   - OnDemand tools are hidden but searchable via tool_search
func WithToolProviders(ctx context.Context, providers ...ToolProvider) context.Context {
	return context.WithValue(ctx, toolProvidersKey{}, providers)
}

// WithOnDemandToolProviders adds ondemand providers to the context.
// Tools from these providers are searchable via tool_search but do NOT appear in tools/list.
// This is useful for dynamic tools that should be discoverable but not clutter the tool list.
// Can be combined with WithToolProviders - native providers appear in list, ondemand are searchable only.
// When ondemand providers are added, tool_search and execute_tool are automatically available.
func WithOnDemandToolProviders(ctx context.Context, providers ...ToolProvider) context.Context {
	existing := GetOnDemandToolProviders(ctx)
	return context.WithValue(ctx, onDemandProvidersKey{}, append(existing, providers...))
}

// GetOnDemandToolProviders returns the ondemand tool providers from the context.
// Returns nil if no ondemand providers are attached.
func GetOnDemandToolProviders(ctx context.Context) []ToolProvider {
	if ctx == nil {
		return nil
	}
	providers, _ := ctx.Value(onDemandProvidersKey{}).([]ToolProvider)
	return providers
}

// WithForceOnDemandMode returns a context that forces all tools to ondemand mode.
// In this mode:
//   - Only tool_search and execute_tool appear in tools/list
//   - All native, provider, and remote tools are only available via search
//   - This is useful for AI clients that work better with fewer initial tools
func WithForceOnDemandMode(ctx context.Context, providers ...ToolProvider) context.Context {
	ctx = context.WithValue(ctx, toolProvidersKey{}, providers)
	ctx = context.WithValue(ctx, toolListModeKey{}, ToolListModeForceOnDemand)
	return ctx
}

// GetToolListMode returns the tool list mode from the context
func GetToolListMode(ctx context.Context) ToolListMode {
	if ctx == nil {
		return ToolListModeDefault
	}
	mode, _ := ctx.Value(toolListModeKey{}).(ToolListMode)
	return mode
}

// GetToolProviders returns the tool providers from the context.
// Returns nil if no providers are attached.
func GetToolProviders(ctx context.Context) []ToolProvider {
	if ctx == nil {
		return nil
	}
	providers, _ := ctx.Value(toolProvidersKey{}).([]ToolProvider)
	return providers
}

// listToolsFromProviders returns tools from all providers in the context.
// Tools already in the seen map are skipped to avoid duplicates.
// In ToolListModeForceOnDemand, returns nil (provider tools are hidden from list but searchable).
func listToolsFromProviders(ctx context.Context, seen map[string]bool) []MCPTool {
	// In force ondemand mode, provider tools are hidden from list
	if GetToolListMode(ctx) == ToolListModeForceOnDemand {
		return nil
	}

	providers := GetToolProviders(ctx)
	if len(providers) == 0 {
		return nil
	}

	var allTools []MCPTool
	for _, provider := range providers {
		tools, err := provider.GetTools(ctx)
		if err != nil {
			// Skip provider on error, don't fail the entire list
			continue
		}
		for _, tool := range tools {
			if !seen[tool.Name] {
				allTools = append(allTools, tool)
				seen[tool.Name] = true
			}
		}
	}
	return allTools
}

// callToolFromProviders tries to call a tool from the providers in the context.
// Returns ToolResponse, error - returns ErrUnknownTool if no provider handles the tool.
func callToolFromProviders(ctx context.Context, name string, params map[string]interface{}) (*ToolResponse, error) {
	// Try native providers first
	for _, provider := range GetToolProviders(ctx) {
		result, err := provider.ExecuteTool(ctx, name, params)
		if err == ErrUnknownTool {
			continue
		}
		if err != nil {
			// Provider returned an error
			return nil, err
		}
		if result != nil {
			// Provider handled the tool - convert result to proper ToolResponse
			return convertToToolResponse(result), nil
		}
	}

	// Try ondemand providers
	for _, provider := range GetOnDemandToolProviders(ctx) {
		result, err := provider.ExecuteTool(ctx, name, params)
		if err == ErrUnknownTool {
			continue
		}
		if err != nil {
			return nil, err
		}
		if result != nil {
			return convertToToolResponse(result), nil
		}
	}

	return nil, ErrUnknownTool
}

// GetToolModeFromRequest extracts the tool mode from an HTTP request.
// It first checks the X-MCP-Tool-Mode header, then falls back to the tool_mode query parameter.
// Returns ToolListModeDefault if neither is set or the value is not "discovery".
func GetToolModeFromRequest(r *http.Request) ToolListMode {
	// Check header first
	mode := r.Header.Get(ToolModeHeader)
	if mode == "" {
		// Fall back to query parameter
		mode = r.URL.Query().Get(ToolModeQueryParam)
	}

	if strings.EqualFold(mode, ToolModeDiscovery) {
		return ToolListModeForceOnDemand
	}

	return ToolListModeDefault
}

// WithToolModeFromRequest returns a context with the tool mode from the HTTP request.
// This is a convenience function that combines GetToolModeFromRequest and WithForceOnDemandMode.
// If the request specifies discovery mode, all tools will be hidden except tool_search and execute_tool.
func WithToolModeFromRequest(ctx context.Context, r *http.Request, providers ...ToolProvider) context.Context {
	mode := GetToolModeFromRequest(r)
	if mode == ToolListModeForceOnDemand {
		return WithForceOnDemandMode(ctx, providers...)
	}
	if len(providers) > 0 {
		return WithToolProviders(ctx, providers...)
	}
	return ctx
}
