package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/paularlott/mcp/pool"
)

// DefaultRemoteToolCacheTTL is the default lifetime for cached remote tool lists
// when a RemoteProviderConfig does not specify CacheTTL.
const DefaultRemoteToolCacheTTL = 60 * time.Second

// AuthResolver lazily resolves the auth provider for a remote server for the
// current request. It is only called when the provider actually needs to talk
// to the server (listing or calling a tool), so per-user token lookups are not
// performed for servers that are never touched.
type AuthResolver func(ctx context.Context) (AuthProvider, error)

// RemoteProviderConfig describes a single remote MCP server that a RemoteProvider
// should expose for the current request. The consumer owns "which servers does
// this user have and how do they authenticate"; the library owns fetching,
// caching, namespacing, visibility, filtering and dispatch.
type RemoteProviderConfig struct {
	// Name is the namespace applied to the server's tool names (e.g. a Name of
	// "github" exposes the remote tool "list_repos" as "github__list_repos").
	// It must be unique within a single resolver result.
	Name string

	// URL is the remote MCP server endpoint.
	URL string

	// Auth is a static auth provider for the server. Ignored when AuthFunc is set.
	// May be nil for unauthenticated servers.
	Auth AuthProvider

	// AuthFunc lazily resolves auth for the current request. Takes precedence
	// over Auth. Use this for per-user credentials (e.g. OAuth tokens looked up
	// from a store) so the lookup only happens when the server is used.
	AuthFunc AuthResolver

	// Visibility controls whether the server's tools appear in tools/list
	// (ToolVisibilityNative) or are only reachable via tool_search
	// (ToolVisibilityDiscoverable).
	Visibility ToolVisibility

	// ToolFilter optionally restricts which tools are exposed. It receives the
	// original (un-namespaced) tool name and returns true to include it. Applied
	// on both the list and call paths. Nil means expose all tools.
	ToolFilter ToolFilterFunc

	// CacheTTL is how long this server's tool list is cached. Zero uses
	// DefaultRemoteToolCacheTTL. Negative disables caching.
	CacheTTL time.Duration

	// CacheKey overrides the cache key for this server's tool list. Defaults to
	// Name + "\x00" + URL. Set this to include a user/tenant identifier when tool
	// catalogs differ per user and must not be shared.
	//
	// The cache is bounded: it holds at most a fixed number of entries (see
	// WithMaxCacheEntries / DefaultRemoteToolCacheMaxEntries) and evicts the
	// least-recently-used entry when full, so a per-user CacheKey cannot grow
	// memory without limit. For very large user populations you may still want a
	// shorter CacheTTL or a larger max so active users are not evicted too soon.
	CacheKey string

	// HTTPPool optionally provides a custom HTTP pool (e.g. for self-signed
	// internal services). Nil uses the default secure pool.
	HTTPPool pool.HTTPPool

	// Keywords are extra search keywords attached to this server's tools. The
	// server namespace and "remote" are always included.
	Keywords []string
}

// RemoteProviderResolver returns the set of remote servers available for the
// current request. It is called on every list/search/execute that reaches the
// provider, so it should read request-scoped information (user, tenant) from
// the context. Returning an error fails the operation; returning an empty slice
// simply exposes no remote tools.
type RemoteProviderResolver func(ctx context.Context) ([]RemoteProviderConfig, error)

// RemoteProvider is a request-scoped ToolProvider that exposes tools from one
// or more remote MCP servers. It is intended to be created once and reused for
// the lifetime of the process: it resolves the per-request server set from the
// context, so a single instance safely serves many users without leaking tools
// between them. Reusing one instance also lets its tool-list cache persist
// across requests.
//
//	provider := mcp.NewRemoteProvider(func(ctx context.Context) ([]mcp.RemoteProviderConfig, error) {
//	    user := userFromContext(ctx)
//	    return loadServersForUser(user), nil
//	})
//	// per request:
//	ctx := mcp.WithToolProviders(r.Context(), provider)
//	server.HandleRequest(w, r.WithContext(ctx))
type RemoteProvider struct {
	resolve RemoteProviderResolver
	cache   *remoteToolCache
}

// Ensure RemoteProvider implements ToolProvider.
var _ ToolProvider = (*RemoteProvider)(nil)

// RemoteProviderOption configures a RemoteProvider.
type RemoteProviderOption func(*remoteProviderOptions)

type remoteProviderOptions struct {
	maxCacheEntries int
}

// WithMaxCacheEntries bounds how many distinct cache keys the provider keeps
// tool lists for. Once exceeded, the least-recently-used entry is evicted. This
// keeps memory bounded even when CacheKey embeds a per-user/per-tenant id.
// A value <= 0 uses DefaultRemoteToolCacheMaxEntries.
func WithMaxCacheEntries(n int) RemoteProviderOption {
	return func(o *remoteProviderOptions) {
		o.maxCacheEntries = n
	}
}

// NewRemoteProvider creates a remote tool provider driven by the given resolver.
// Create it once and reuse it across requests.
func NewRemoteProvider(resolve RemoteProviderResolver, opts ...RemoteProviderOption) *RemoteProvider {
	o := remoteProviderOptions{maxCacheEntries: DefaultRemoteToolCacheMaxEntries}
	for _, opt := range opts {
		opt(&o)
	}
	return &RemoteProvider{
		resolve: resolve,
		cache:   newRemoteToolCache(o.maxCacheEntries),
	}
}

func (cfg RemoteProviderConfig) cacheKey() string {
	if cfg.CacheKey != "" {
		return cfg.CacheKey
	}
	return cfg.Name + "\x00" + cfg.URL
}

func (cfg RemoteProviderConfig) resolveAuth(ctx context.Context) (AuthProvider, error) {
	if cfg.AuthFunc != nil {
		return cfg.AuthFunc(ctx)
	}
	return cfg.Auth, nil
}

func (cfg RemoteProviderConfig) newClient(auth AuthProvider) *Client {
	if cfg.HTTPPool != nil {
		return NewClientWithPool(cfg.URL, auth, cfg.Name, cfg.HTTPPool)
	}
	return NewClient(cfg.URL, auth, cfg.Name)
}

// resolveServers resolves the request's remote servers, memoized for the
// lifetime of the request context so the resolver's I/O (e.g. a DB lookup) runs
// once even though the server queries providers several times per request.
func (p *RemoteProvider) resolveServers(ctx context.Context) ([]RemoteProviderConfig, error) {
	v, err := memoizeRequest(ctx, p, func() (any, error) {
		return p.resolve(ctx)
	})
	if err != nil {
		return nil, err
	}
	servers, _ := v.([]RemoteProviderConfig)
	return servers, nil
}

// GetTools returns the tools for all of the current request's remote servers,
// applying each server's visibility, keywords and tool filter. Servers that
// fail to respond are skipped so one bad remote does not break the whole list.
func (p *RemoteProvider) GetTools(ctx context.Context) ([]MCPTool, error) {
	servers, err := p.resolveServers(ctx)
	if err != nil {
		return nil, err
	}
	if len(servers) == 0 {
		return nil, nil
	}

	var tools []MCPTool
	for _, cfg := range servers {
		serverTools, err := p.toolsForServer(ctx, cfg)
		if err != nil {
			// Skip servers we cannot reach; do not fail the entire list.
			continue
		}
		tools = append(tools, serverTools...)
	}
	return tools, nil
}

// toolsForServer returns the (namespaced, filtered, visibility-tagged) tools for
// a single server, using the cached list when still valid.
func (p *RemoteProvider) toolsForServer(ctx context.Context, cfg RemoteProviderConfig) ([]MCPTool, error) {
	key := cfg.cacheKey()
	caching := cfg.CacheTTL >= 0
	now := time.Now()

	if caching {
		if tools, ok := p.cache.get(key, now); ok {
			return tools, nil
		}
	}

	auth, err := cfg.resolveAuth(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve auth for %q: %w", cfg.Name, err)
	}

	client := cfg.newClient(auth)
	if cfg.ToolFilter != nil {
		client.WithToolFilter(cfg.ToolFilter)
	}

	remoteTools, err := client.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tools for %q: %w", cfg.Name, err)
	}

	visibility := cfg.Visibility
	keywords := append([]string{cfg.Name, "remote"}, cfg.Keywords...)

	tools := make([]MCPTool, 0, len(remoteTools))
	for _, tool := range remoteTools {
		tool.Visibility = visibility
		tool.Keywords = keywords
		tools = append(tools, tool)
	}

	if caching {
		ttl := cfg.CacheTTL
		if ttl == 0 {
			ttl = DefaultRemoteToolCacheTTL
		}
		p.cache.put(key, tools, ttl, now)
	}

	return tools, nil
}

// ExecuteTool dispatches a namespaced tool call to the owning remote server.
// Returns ErrUnknownTool when the tool is not a namespaced tool belonging to one
// of this request's servers, so other providers can handle it.
func (p *RemoteProvider) ExecuteTool(ctx context.Context, name string, params map[string]any) (*ToolResponse, error) {
	idx := strings.Index(name, DefaultNamespaceSeparator)
	if idx < 0 {
		return nil, ErrUnknownTool
	}
	namespace := name[:idx]

	servers, err := p.resolveServers(ctx)
	if err != nil {
		return nil, err
	}

	for _, cfg := range servers {
		if cfg.Name != namespace {
			continue
		}

		auth, err := cfg.resolveAuth(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolve auth for %q: %w", cfg.Name, err)
		}

		client := cfg.newClient(auth)
		if cfg.ToolFilter != nil {
			client.WithToolFilter(cfg.ToolFilter)
		}

		result, err := client.CallTool(ctx, name, params)
		if err == ErrToolFiltered {
			return nil, fmt.Errorf("tool %q is disabled on server %q", name, cfg.Name)
		}
		return result, err
	}

	return nil, ErrUnknownTool
}

// InvalidateCache removes the cached tool list for a single server. The key must
// match the server's CacheKey (or, when CacheKey is unset, Name + "\x00" + URL).
// Call this when a server's configuration or tool set changes.
func (p *RemoteProvider) InvalidateCache(cacheKey string) {
	p.cache.invalidate(cacheKey)
}

// InvalidateAllCache clears every cached remote tool list.
func (p *RemoteProvider) InvalidateAllCache() {
	p.cache.clear()
}
