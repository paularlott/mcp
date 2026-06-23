package mcp

import (
	"context"
	"net/http"
	"strings"
	"sync"
)

// ShowAllHeader is the HTTP header used to show all tools regardless of visibility
const ShowAllHeader = "X-MCP-Show-All"

// ShowAllQueryParam is the query parameter used to show all tools (fallback)
const ShowAllQueryParam = "show_all"

// ToolProvider is the interface that providers implement to expose tools.
// Tools returned by providers should set their Visibility field:
//   - ToolVisibilityNative: Tool appears in tools/list
//   - ToolVisibilityDiscoverable: Tool only available via tool_search
type ToolProvider interface {
	// GetTools returns all tools available from this provider.
	// The context contains tenant/user information for filtering.
	// Each tool's Visibility field determines whether it appears in tools/list
	// or only via tool_search. Keywords should be populated for discoverable tools.
	GetTools(ctx context.Context) ([]MCPTool, error)

	// ExecuteTool executes a tool by name and returns its response.
	//
	// Miss contract: if this provider does not handle the named tool, return
	// (nil, nil). This is the canonical "not handled" signal and lets the
	// server (or a MultiProvider) try the next provider. Returning
	// (nil, ErrUnknownTool) is also accepted for the same purpose and behaves
	// identically. Any other non-nil error aborts dispatch and is returned to
	// the caller, so only use it for genuine failures, not for misses.
	//
	// Build the response with the NewToolResponse* constructors. If you have a
	// loose value (a string or any JSON-encodable type) rather than an
	// already-built response, wrap it with NewToolResponseAuto.
	ExecuteTool(ctx context.Context, name string, params map[string]any) (*ToolResponse, error)
}

// Discovery tool names
const (
	ToolSearchName  = "tool_search"
	ExecuteToolName = "execute_tool"
)

// toolProvidersKey is the context key for tool providers
type toolProvidersKey struct{}

// showAllToolsKey is the context key for show-all mode
type showAllToolsKey struct{}

// requestMemoKey is the context key for the per-request provider memo.
type requestMemoKey struct{}

// requestMemo is a per-request scratch space that lets providers compute an
// expensive value once even though the server may query providers several times
// while handling a single request (e.g. tools/list checks for discoverable
// tools and then lists them). It is installed on the context when providers are
// attached, so its lifetime is exactly the request's lifetime — no global state,
// nothing to evict.
type requestMemo struct {
	mu sync.Mutex
	m  map[any]*memoSlot
}

type memoSlot struct {
	once sync.Once
	val  any
	err  error
}

func getRequestMemo(ctx context.Context) *requestMemo {
	if ctx == nil {
		return nil
	}
	memo, _ := ctx.Value(requestMemoKey{}).(*requestMemo)
	return memo
}

// memoizeRequest runs fn at most once per key for the lifetime of the request's
// context, returning the cached result on subsequent calls. If no per-request
// memo is present (e.g. the provider is used outside the normal request flow),
// fn is simply called each time.
func memoizeRequest(ctx context.Context, key any, fn func() (any, error)) (any, error) {
	memo := getRequestMemo(ctx)
	if memo == nil {
		return fn()
	}
	memo.mu.Lock()
	slot, ok := memo.m[key]
	if !ok {
		slot = &memoSlot{}
		memo.m[key] = slot
	}
	memo.mu.Unlock()

	slot.once.Do(func() {
		slot.val, slot.err = fn()
	})
	return slot.val, slot.err
}

// WithToolProviders returns a context with the given tool providers attached.
// Multiple providers can be attached and all will be queried for tools.
// Tools from providers are filtered by their Visibility field:
//   - ToolVisibilityNative: appears in tools/list
//   - ToolVisibilityDiscoverable: only searchable via tool_search
//
// Use WithShowAllTools to make all tools appear in tools/list regardless of visibility.
//
// A per-request memo is also installed so providers can cache expensive,
// request-scoped work (such as resolving a user's remote servers) across the
// multiple times the server queries providers while handling one request.
func WithToolProviders(ctx context.Context, providers ...ToolProvider) context.Context {
	existing := GetToolProviders(ctx)
	ctx = context.WithValue(ctx, toolProvidersKey{}, append(existing, providers...))
	if getRequestMemo(ctx) == nil {
		ctx = context.WithValue(ctx, requestMemoKey{}, &requestMemo{m: make(map[any]*memoSlot)})
	}
	return ctx
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

// WithShowAllTools returns a context that shows all tools in tools/list,
// regardless of their Visibility setting. This is useful for MCP server chaining
// where the consuming server needs to see all available tools.
// Can be enabled via X-MCP-Show-All header or ?show_all=true query param.
func WithShowAllTools(ctx context.Context) context.Context {
	return context.WithValue(ctx, showAllToolsKey{}, true)
}

// GetShowAllTools returns true if show-all mode is enabled in the context.
func GetShowAllTools(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	showAll, _ := ctx.Value(showAllToolsKey{}).(bool)
	return showAll
}

// GetShowAllFromRequest extracts the show-all flag from an HTTP request.
// It first checks the X-MCP-Show-All header, then falls back to the show_all query parameter.
// Returns true if either is set to "true" (case-insensitive).
func GetShowAllFromRequest(r *http.Request) bool {
	// Check header first
	val := r.Header.Get(ShowAllHeader)
	if val == "" {
		// Fall back to query parameter
		val = r.URL.Query().Get(ShowAllQueryParam)
	}
	return strings.EqualFold(val, "true")
}

// WithShowAllFromRequest returns a context with show-all mode set based on the HTTP request.
// This is a convenience function that combines GetShowAllFromRequest and WithShowAllTools.
// Also attaches any provided tool providers.
func WithShowAllFromRequest(ctx context.Context, r *http.Request, providers ...ToolProvider) context.Context {
	if len(providers) > 0 {
		ctx = WithToolProviders(ctx, providers...)
	}
	if GetShowAllFromRequest(r) {
		ctx = WithShowAllTools(ctx)
	}
	return ctx
}

// listToolsFromProviders returns tools from all providers in the context.
// Tools already in the seen map are skipped to avoid duplicates.
// In normal mode, only returns tools with ToolVisibilityNative.
// In show-all mode, returns all tools regardless of visibility.
func listToolsFromProviders(ctx context.Context, seen map[string]bool) []MCPTool {
	providers := GetToolProviders(ctx)
	if len(providers) == 0 {
		return nil
	}

	showAll := GetShowAllTools(ctx)
	var allTools []MCPTool

	for _, provider := range providers {
		tools, err := provider.GetTools(ctx)
		if err != nil {
			// Skip provider on error, don't fail the entire list
			continue
		}
		for _, tool := range tools {
			if seen[tool.Name] {
				continue
			}
			// In normal mode, only include native tools in the list
			// In show-all mode, include all tools
			if showAll || tool.Visibility == ToolVisibilityNative {
				allTools = append(allTools, tool)
				seen[tool.Name] = true
			}
		}
	}
	return allTools
}

// hasDiscoverableToolsFromProviders checks if any provider has discoverable tools.
func hasDiscoverableToolsFromProviders(ctx context.Context) bool {
	providers := GetToolProviders(ctx)
	for _, provider := range providers {
		tools, err := provider.GetTools(ctx)
		if err != nil {
			continue
		}
		for _, tool := range tools {
			if tool.Visibility == ToolVisibilityDiscoverable {
				return true
			}
		}
	}
	return false
}

// getDiscoverableToolsFromProviders returns all discoverable tools from providers.
func getDiscoverableToolsFromProviders(ctx context.Context) []MCPTool {
	providers := GetToolProviders(ctx)
	if len(providers) == 0 {
		return nil
	}

	var tools []MCPTool
	seen := make(map[string]bool)

	for _, provider := range providers {
		providerTools, err := provider.GetTools(ctx)
		if err != nil {
			continue
		}
		for _, tool := range providerTools {
			if tool.Visibility == ToolVisibilityDiscoverable && !seen[tool.Name] {
				tools = append(tools, tool)
				seen[tool.Name] = true
			}
		}
	}
	return tools
}

// getNativeToolsFromProviders returns all native tools from providers.
func getNativeToolsFromProviders(ctx context.Context) []MCPTool {
	providers := GetToolProviders(ctx)
	if len(providers) == 0 {
		return nil
	}

	var tools []MCPTool
	seen := make(map[string]bool)

	for _, provider := range providers {
		providerTools, err := provider.GetTools(ctx)
		if err != nil {
			continue
		}
		for _, tool := range providerTools {
			if tool.Visibility == ToolVisibilityNative && !seen[tool.Name] {
				tools = append(tools, tool)
				seen[tool.Name] = true
			}
		}
	}
	return tools
}

// callToolFromProviders tries to call a tool from the providers in the context.
// Returns ToolResponse, error - returns ErrUnknownTool if no provider handles the tool.
func callToolFromProviders(ctx context.Context, name string, params map[string]any) (*ToolResponse, error) {
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
			// Provider handled the tool
			return result, nil
		}
	}

	return nil, ErrUnknownTool
}
