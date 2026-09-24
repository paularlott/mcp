package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/paularlott/mcp/pool"
)

// DefaultRemoteToolCacheTTL is the default lifetime for cached remote tool lists
// when a RemoteProviderConfig does not specify CacheTTL.
const DefaultRemoteToolCacheTTL = 60 * time.Second

// DefaultRemoteToolListTimeout bounds how long a single remote server's
// tools/list call may take before RemoteProvider gives up on it for this
// request. A zero RemoteProviderConfig.ListTimeout uses this default; a
// negative ListTimeout disables the bound (waits as long as the caller's
// context allows). This exists so one unresponsive server cannot hang
// GetTools indefinitely — without it, a hung server would delay (or, before
// GetTools ran servers concurrently, block) every other server's tools too.
const DefaultRemoteToolListTimeout = 10 * time.Second

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

	// ListTimeout bounds how long this server's tools/list call may take
	// before GetTools treats it as failed and skips it. Zero uses
	// DefaultRemoteToolListTimeout; negative disables the bound (waits as
	// long as the caller's context allows). Only applies to tools/list —
	// ExecuteTool has no default timeout (see CallTimeout), since tool calls
	// (unlike listing) may legitimately run long.
	ListTimeout time.Duration

	// CallTimeout optionally bounds how long a single ExecuteTool call to
	// this server may take. Unlike ListTimeout, the zero value here means no
	// bound at all — waits as long as the caller's context allows — because a
	// tool call may legitimately run long and a default cap here would risk
	// silently breaking one. Set this explicitly if you want tool calls to a
	// specific server bounded (e.g. because you know its tools are quick, or
	// because you want ExecuteTool to hand a hung server's failure to your
	// OnServerError hook promptly instead of waiting on the caller's own
	// context, which may have no deadline).
	CallTimeout time.Duration

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
	resolve       RemoteProviderResolver
	cache         *remoteToolCache
	onServerError func(cfg RemoteProviderConfig, err error)
}

// Ensure RemoteProvider implements ToolProvider and ResourceProvider.
var _ ToolProvider = (*RemoteProvider)(nil)
var _ ResourceProvider = (*RemoteProvider)(nil)

// RemoteProviderOption configures a RemoteProvider.
type RemoteProviderOption func(*remoteProviderOptions)

type remoteProviderOptions struct {
	maxCacheEntries int
	onServerError   func(cfg RemoteProviderConfig, err error)
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

// WithOnServerError registers a callback invoked whenever a remote server
// could not be reached or misbehaved — a failed tools/list (GetTools skips
// the server but does not fail the request), or a genuine transport/protocol
// failure calling a tool (a *ToolError, meaning the server responded with its
// own application-level error, does NOT trigger this — that is not a health
// problem). fn is called synchronously from whichever request goroutine hit
// the failure and may be called concurrently for different servers; keep it
// fast and non-blocking (e.g. update in-memory state, log, or send on a
// channel) rather than doing I/O inline. Use it to log failures and/or drive
// a health-tracking / background-retry system: nothing else in RemoteProvider
// surfaces skipped-server failures anywhere.
func WithOnServerError(fn func(cfg RemoteProviderConfig, err error)) RemoteProviderOption {
	return func(o *remoteProviderOptions) {
		o.onServerError = fn
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
		resolve:       resolve,
		cache:         newRemoteToolCache(o.maxCacheEntries),
		onServerError: o.onServerError,
	}
}

// reportError invokes the configured error hook, if any. Safe to call with a
// nil hook.
func (p *RemoteProvider) reportError(cfg RemoteProviderConfig, err error) {
	if p.onServerError != nil {
		p.onServerError(cfg, err)
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
	var client *Client
	if cfg.HTTPPool != nil {
		client = NewClientWithPool(cfg.URL, auth, cfg.Name, cfg.HTTPPool)
	} else {
		client = NewClient(cfg.URL, auth, cfg.Name)
	}

	// Advertise this client's own support for the MCP Apps extension
	// (SEP-1865) to the remote server. A federated tool's whole point is to
	// behave like a native one from the caller's perspective, and that
	// includes MCP Apps: without this, a spec-conformant remote server that
	// only attaches _meta.ui for clients that declared
	// capabilities.extensions[io.modelcontextprotocol/ui] has no way to know
	// this provider can render one, and silently serves a plain-text-only
	// tool instead — MCP Apps then quietly never works for that server, with
	// no error anywhere to explain why. Unconditional, matching this
	// package's own guidance elsewhere (see Server.ClientCapabilities) that
	// attaching _meta.ui unconditionally is the robust choice: the cost to a
	// non-supporting host is nil, it just ignores unknown _meta.
	client.DeclareExtension(UIAppsExtensionID, map[string]any{
		"mimeTypes": []string{UIAppMimeType},
	})

	return client
}

// withListTimeout returns a context bounded by cfg.ListTimeout (or
// DefaultRemoteToolListTimeout when unset), and its cancel func. A negative
// ListTimeout disables the bound, returning ctx unchanged with a no-op cancel.
func (cfg RemoteProviderConfig) withListTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	d := cfg.ListTimeout
	switch {
	case d < 0:
		return ctx, func() {}
	case d == 0:
		d = DefaultRemoteToolListTimeout
	}
	return context.WithTimeout(ctx, d)
}

// withCallTimeout bounds ctx by cfg.CallTimeout when set (> 0). Unlike
// withListTimeout, zero means no bound at all — this is opt-in, not a default.
func (cfg RemoteProviderConfig) withCallTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if cfg.CallTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, cfg.CallTimeout)
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
// applying each server's visibility, keywords and tool filter. Servers are
// queried concurrently and each is bounded by its ListTimeout, so one slow or
// unresponsive remote cannot delay or block the others. Servers that fail to
// respond are skipped so one bad remote does not break the whole list; if
// WithOnServerError was set, it is called for each skipped server so the host
// can log it and/or track its health.
func (p *RemoteProvider) GetTools(ctx context.Context) ([]MCPTool, error) {
	servers, err := p.resolveServers(ctx)
	if err != nil {
		return nil, err
	}
	if len(servers) == 0 {
		return nil, nil
	}

	results := make([][]MCPTool, len(servers))
	var wg sync.WaitGroup
	for i, cfg := range servers {
		wg.Add(1)
		go func(i int, cfg RemoteProviderConfig) {
			defer wg.Done()
			serverTools, err := p.toolsForServer(ctx, cfg)
			if err != nil {
				// Skip servers we cannot reach; do not fail the entire list.
				p.reportError(cfg, err)
				return
			}
			results[i] = serverTools
		}(i, cfg)
	}
	wg.Wait()

	var tools []MCPTool
	for _, serverTools := range results {
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

	listCtx, cancel := cfg.withListTimeout(ctx)
	defer cancel()
	remoteTools, err := client.ListTools(listCtx)
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

		callCtx, cancel := cfg.withCallTimeout(ctx)
		result, err := client.CallTool(callCtx, name, params)
		cancel()
		if err == ErrToolFiltered {
			return nil, fmt.Errorf("tool %q is disabled on server %q", name, cfg.Name)
		}
		// A *ToolError means the server responded with its own application-level
		// error (the server is fine, this call just failed) — not a health
		// problem, so it does not trigger the error hook. Any other error means
		// the server could not be reached or misbehaved at the transport level.
		var toolErr *ToolError
		if err != nil && !errors.As(err, &toolErr) {
			p.reportError(cfg, err)
		}
		return result, err
	}

	return nil, ErrUnknownTool
}

// GetResources returns the resources and resource templates exposed by all
// of the current request's remote servers, so a federated tool's linked MCP
// Apps ui:// resource (or any other resource a remote exposes) is reachable
// via resources/list and resources/read the same way a native tool's would
// be — without this, RemoteProvider's caller can dispatch a federated tool
// call but has no way to serve the ui:// view it renders, since a
// request-scoped remote server has no other route into Server.ReadResource's
// fan-out (that fan-out only covers servers registered process-lifetime via
// Server.RegisterRemoteServer/ReplaceRemoteServers, not ones resolved per
// request here).
//
// Like GetTools, servers are queried concurrently and a server that fails is
// skipped (and reported via WithOnServerError) rather than failing the whole
// call. Unlike tools, remote resources are not namespaced: a federated
// server's resource URIs (e.g. a ui:// MCP Apps view) are opaque and
// surfaced as-is, matching Server.ReadResource's own remote fan-out.
func (p *RemoteProvider) GetResources(ctx context.Context) (*ProvidedResources, error) {
	servers, err := p.resolveServers(ctx)
	if err != nil {
		return nil, err
	}
	if len(servers) == 0 {
		return &ProvidedResources{}, nil
	}

	type serverResources struct {
		resources []MCPResource
		templates []MCPResourceTemplate
	}
	results := make([]serverResources, len(servers))
	var wg sync.WaitGroup
	for i, cfg := range servers {
		wg.Add(1)
		go func(i int, cfg RemoteProviderConfig) {
			defer wg.Done()

			auth, err := cfg.resolveAuth(ctx)
			if err != nil {
				p.reportError(cfg, fmt.Errorf("resolve auth for %q: %w", cfg.Name, err))
				return
			}
			client := cfg.newClient(auth)

			listCtx, cancel := context.WithTimeout(ctx, remoteResourceFanoutTimeout)
			defer cancel()

			resources, err := client.ListResources(listCtx)
			if err != nil {
				p.reportError(cfg, fmt.Errorf("list resources for %q: %w", cfg.Name, err))
				return
			}
			// Not every server implements resource templates; a failure here
			// means "none", not "abort the resources this server did return".
			templates, _ := client.ListResourceTemplates(listCtx)
			results[i] = serverResources{resources: resources, templates: templates}
		}(i, cfg)
	}
	wg.Wait()

	out := &ProvidedResources{}
	for _, r := range results {
		out.Resources = append(out.Resources, r.resources...)
		out.Templates = append(out.Templates, r.templates...)
	}
	return out, nil
}

// ReadResource fans a resources/read out to all of the current request's
// remote servers, in resolver order, and returns the first hit — mirroring
// Server.ReadResource's own remote fan-out (see resources.go), since a
// federated server's resource URIs are opaque and not namespaced by server
// the way tool names are. Returns ErrUnknownResource if no server has the
// resource; when one or more servers failed outright (rather than answering
// not-found), the returned error still wraps ErrUnknownResource but names
// each failing server, per the ResourceProvider miss contract.
func (p *RemoteProvider) ReadResource(ctx context.Context, uri string) (*ResourceResponse, error) {
	servers, err := p.resolveServers(ctx)
	if err != nil {
		return nil, err
	}

	var failures []error
	for _, cfg := range servers {
		auth, err := cfg.resolveAuth(ctx)
		if err != nil {
			failures = append(failures, fmt.Errorf("resolve auth for %q: %w", cfg.Name, err))
			continue
		}
		client := cfg.newClient(auth)

		attemptCtx, cancel := context.WithTimeout(ctx, remoteResourceFanoutTimeout)
		resp, err := client.ReadResource(attemptCtx, uri)
		cancel()
		if err == nil {
			return resp, nil
		}
		if isResourceNotFoundErr(err) {
			continue
		}
		p.reportError(cfg, err)
		failures = append(failures, fmt.Errorf("%s: %w", cfg.Name, err))
	}

	if len(failures) > 0 {
		return nil, fmt.Errorf("%w; %d remote server(s) failed: %w",
			ErrUnknownResource, len(failures), errors.Join(failures...))
	}
	return nil, ErrUnknownResource
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
