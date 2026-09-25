package mcp

import (
	"context"
	"sort"
	"strings"
)

// Remote-server federation: registration, the shared tool merge, refresh, and ownership routing.

// registeredClient holds a remote client with its configuration
type registeredClient struct {
	client       *Client
	namespace    string
	visibility   ToolVisibility
	remoteSearch bool // Whether to delegate tool_search to this remote
	excludeApps  bool // Skip tools linked to a ui:// resource (MCP Apps views)
}

// RemoteServerOption configures options when registering a remote server.
type RemoteServerOption func(*remoteServerOptions)

type remoteServerOptions struct {
	remoteSearch bool
	excludeApps  bool
}

// WithRemoteSearch enables delegating tool_search to this remote server.
// Results from the remote are prefixed with the server's namespace.
func WithRemoteSearch() RemoteServerOption {
	return func(o *remoteServerOptions) {
		o.remoteSearch = true
	}
}

// WithRemoteExcludeApps skips the remote's MCP Apps tools (any tool whose
// _meta.ui links it to a ui:// resource) when federating it onto this
// server: absent from tools/list and tool_search, and not callable.
// See [RemoteServerEntry.ExcludeApps] for why a federating server that
// renders no views of its own should always set this.
func WithRemoteExcludeApps() RemoteServerOption {
	return func(o *remoteServerOptions) {
		o.excludeApps = true
	}
}

// RegisterRemoteServer registers a remote MCP server with native visibility.
// Remote server tools appear in tools/list and are directly callable.
func (s *Server) RegisterRemoteServer(client *Client, opts ...RemoteServerOption) error {
	o := &remoteServerOptions{}
	for _, opt := range opts {
		opt(o)
	}
	return s.registerRemoteServerWithVisibility(client, ToolVisibilityNative, o.remoteSearch, o.excludeApps)
}

// RegisterRemoteServerDiscoverable registers a remote MCP server with discoverable visibility.
// Remote server tools do NOT appear in tools/list but are searchable via tool_search.
func (s *Server) RegisterRemoteServerDiscoverable(client *Client, opts ...RemoteServerOption) error {
	o := &remoteServerOptions{}
	for _, opt := range opts {
		opt(o)
	}
	return s.registerRemoteServerWithVisibility(client, ToolVisibilityDiscoverable, o.remoteSearch, o.excludeApps)
}

// UnregisterRemoteServer removes a previously registered remote server and all its cached tools.
func (s *Server) UnregisterRemoteServer(client *Client) {
	s.mu.Lock()
	defer s.mu.Unlock()

	regClient, ok := s.remoteClients[client.baseURL]
	if !ok {
		return
	}
	delete(s.remoteClients, client.baseURL)

	// Remove tools belonging to this client from toolToServer and nativeToolCache
	toRemove := make(map[string]bool)
	for toolName, rc := range s.toolToServer {
		if rc == regClient {
			toRemove[toolName] = true
			delete(s.toolToServer, toolName)
		}
	}
	for toolName, rc := range s.excludedTools {
		if rc == regClient {
			delete(s.excludedTools, toolName)
		}
	}

	if len(toRemove) > 0 {
		filtered := make([]MCPTool, 0, len(s.nativeToolCache))
		for _, t := range s.nativeToolCache {
			if !toRemove[t.Name] {
				filtered = append(filtered, t)
			}
		}
		s.nativeToolCache = filtered
	}

	s.recalcHasDiscoverableToolsLocked()
}

// ReplaceRemoteServers atomically replaces all registered remote servers with the provided list.
// Each entry is a (*Client, ToolVisibility) pair. Use ToolVisibilityNative for tools that should
// appear in tools/list, or ToolVisibilityDiscoverable for tools only findable via tool_search.
// All previously registered remote servers and their cached tools are removed first.
func (s *Server) ReplaceRemoteServers(servers []RemoteServerEntry) error {
	s.mu.Lock()

	newNativeCache := make([]MCPTool, 0, len(s.nativeToolCache))
	for _, t := range s.nativeToolCache {
		if _, isRemote := s.toolToServer[t.Name]; !isRemote {
			newNativeCache = append(newNativeCache, t)
		}
	}
	s.nativeToolCache = newNativeCache

	for name, rc := range s.remoteClients {
		if rc.visibility == ToolVisibilityDiscoverable {
			s.internalRegistry.UnregisterTool(name)
			for toolName, toolRc := range s.toolToServer {
				if toolRc == rc {
					s.internalRegistry.UnregisterTool(toolName)
				}
			}
		}
	}

	s.remoteClients = make(map[string]*registeredClient)
	s.toolToServer = make(map[string]*registeredClient)
	s.excludedTools = make(map[string]*registeredClient)

	s.mu.Unlock()

	for _, entry := range servers {
		if err := s.registerRemoteServerWithVisibility(entry.Client, entry.Visibility, entry.RemoteSearch, entry.ExcludeApps); err != nil {
			return err
		}
	}

	s.mu.Lock()
	s.recalcHasDiscoverableToolsLocked()
	s.mu.Unlock()

	return nil
}

// RemoteServerEntry pairs a client with the visibility to use when registering.
type RemoteServerEntry struct {
	Client       *Client
	Visibility   ToolVisibility
	RemoteSearch bool // Delegate tool_search to this remote server
	// ExcludeApps skips the remote's MCP Apps tools (any tool whose
	// _meta.ui links it to a ui:// resource): they are absent from
	// tools/list and tool_search, and not callable through this server.
	// An app view speaks bare, host-agnostic tool names, so re-serving it
	// under a federation namespace breaks its in-page calls to its own
	// tools — a federating server that renders no views of its own should
	// always set this.
	ExcludeApps bool
}

// remoteMergeState accumulates one merge pass over remote tool lists.
// Registration merges one server into the live state under s.mu; RefreshTools
// rebuilds state for every server and swaps it in. Both funnel each remote's
// tools through mergeRemoteTools, so how a remote tool merges — namespace
// handling, app exclusion, the visibility split — is defined exactly once.
type remoteMergeState struct {
	toolToServer  map[string]*registeredClient
	excludedTools map[string]*registeredClient
	nativeIndex   map[string]MCPTool
	discoverable  []MCPTool
}

func newRemoteMergeState() *remoteMergeState {
	return &remoteMergeState{
		toolToServer:  make(map[string]*registeredClient),
		excludedTools: make(map[string]*registeredClient),
		nativeIndex:   make(map[string]MCPTool),
	}
}

// mergeRemoteTools folds one remote server's fetched, already-namespaced
// tool list into state. Not thread-safe by itself; callers hold whatever
// lock their path requires.
func (s *Server) mergeRemoteTools(regClient *registeredClient, tools []MCPTool, state *remoteMergeState) {
	for _, tool := range tools {
		toolName := tool.Name
		if regClient.excludeApps && ToolIsApp(tool) {
			// Record the skip so the namespace-prefix fallback in CallTool
			// doesn't dispatch to it either.
			state.excludedTools[toolName] = regClient
			continue
		}
		state.toolToServer[toolName] = regClient
		tool.Name = toolName
		switch regClient.visibility {
		case ToolVisibilityNative:
			state.nativeIndex[toolName] = tool
		case ToolVisibilityDiscoverable:
			state.discoverable = append(state.discoverable, tool)
		}
	}
}

// remoteToolHandler adapts a namespaced remote tool name to a search-registry
// handler that dispatches through the server's own CallTool.
func (s *Server) remoteToolHandler(name string) ToolHandler {
	return func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return s.CallTool(ctx, name, req.args)
	}
}

// registerRemoteServerWithVisibility is the internal implementation for registering remote servers.
func (s *Server) registerRemoteServerWithVisibility(client *Client, visibility ToolVisibility, remoteSearch, excludeApps bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if visibility == ToolVisibilityDiscoverable || remoteSearch {
		s.hasDiscoverableTools = true
	}

	namespace := strings.TrimSuffix(client.Namespace(), client.separator)

	regClient := &registeredClient{
		client:       client,
		namespace:    namespace,
		visibility:   visibility,
		remoteSearch: remoteSearch,
		excludeApps:  excludeApps,
	}
	s.remoteClients[client.baseURL] = regClient

	// Propagate upstream tool changes downstream: when this remote's tool set
	// changes, refresh our merged cache and notify our own subscribers. This hook
	// fires only when the caller has enabled notifications on the client (via
	// [Client.EnableNotifications]); otherwise no reader is active and the hook
	// never runs. Resources/prompts aren't federated, so only tools propagate.
	client.setPropagationHook(func(method string, params any) {
		if method != NotificationToolsChanged {
			return
		}
		go func() {
			_ = s.RefreshTools(context.Background())
			s.NotifyToolsChanged()
		}()
	})

	// Fetch tools from the new server
	ctx := context.Background()
	tools, err := client.ListTools(ctx)
	if err != nil {
		// Server registration succeeded, but we couldn't fetch tools
		// This is not a fatal error - tools can be fetched later via RefreshTools
		return nil
	}

	// Fold this server's tools through the shared merge, then apply the
	// result to the live state (registration runs under s.mu).
	state := newRemoteMergeState()
	s.mergeRemoteTools(regClient, tools, state)

	for toolName, rc := range state.toolToServer {
		s.toolToServer[toolName] = rc
	}
	for toolName, rc := range state.excludedTools {
		s.excludedTools[toolName] = rc
	}
	for _, tool := range state.nativeIndex {
		// Replace-or-insert into the sorted native cache for tools/list
		idx := sort.Search(len(s.nativeToolCache), func(i int) bool {
			return s.nativeToolCache[i].Name >= tool.Name
		})
		if idx < len(s.nativeToolCache) && s.nativeToolCache[idx].Name == tool.Name {
			s.nativeToolCache[idx] = tool
		} else {
			s.nativeToolCache = append(s.nativeToolCache, MCPTool{})
			copy(s.nativeToolCache[idx+1:], s.nativeToolCache[idx:])
			s.nativeToolCache[idx] = tool
		}
	}
	for i := range state.discoverable {
		tool := state.discoverable[i]
		s.internalRegistry.RegisterMCPTool(&tool, s.remoteToolHandler(tool.Name))
	}

	// Sort native cache to maintain consistent ordering
	sort.Slice(s.nativeToolCache, func(i, j int) bool {
		return s.nativeToolCache[i].Name < s.nativeToolCache[j].Name
	})

	return nil
}

// RefreshTools manually refreshes the tool cache and lookup from all remote servers.
// This method is safe for concurrent use - it releases the lock during network calls
// to avoid blocking other operations, then atomically swaps in the new data.
// The context can be used to cancel the operation if needed.
func (s *Server) RefreshTools(ctx context.Context) error {
	// Check for cancellation early
	if err := ctx.Err(); err != nil {
		return err
	}

	// Phase 1: Copy data needed for network calls under read lock
	s.mu.RLock()
	localNativeTools := make(map[string]*registeredTool)
	for k, v := range s.tools {
		if v.Visibility == ToolVisibilityNative {
			localNativeTools[k] = v
		}
	}
	remoteClients := make([]*registeredClient, 0, len(s.remoteClients))
	for _, rc := range s.remoteClients {
		remoteClients = append(remoteClients, rc)
	}
	// Capture the names of currently-registered discoverable remote tools so we
	// can remove stale ones from the internal registry after refreshing.
	oldDiscoverableRemoteTools := make([]string, 0)
	for toolName, rc := range s.toolToServer {
		if rc.visibility == ToolVisibilityDiscoverable {
			oldDiscoverableRemoteTools = append(oldDiscoverableRemoteTools, toolName)
		}
	}
	s.mu.RUnlock()

	// Phase 2: Build new maps without holding lock (network calls happen here)
	newNativeToolIndex := make(map[string]MCPTool)
	newToolToServer := make(map[string]*registeredClient)
	newExcludedTools := make(map[string]*registeredClient)
	// Fresh discoverable remote tools to (re)register in the internal registry.
	freshDiscoverableRemoteTools := make([]MCPTool, 0)

	// Add local native tools to new cache
	for _, tool := range localNativeTools {
		toolItem := MCPTool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.Schema,
			Meta:        tool.Meta,
			Icons:       tool.Icons,
		}
		if tool.OutputSchema != nil {
			toolItem.OutputSchema = tool.OutputSchema
		}
		newNativeToolIndex[toolItem.Name] = toolItem
	}

	// Add remote tools to cache and lookup based on their visibility (network calls here)
	for _, regClient := range remoteClients {
		// Check for cancellation before each network call
		if err := ctx.Err(); err != nil {
			return err
		}
		// Force a fresh fetch from the remote so RefreshTools genuinely picks up
		// tool changes (the client otherwise serves its cached list).
		if err := regClient.client.RefreshToolCache(ctx); err != nil {
			continue // Skip failed remote servers
		}
		tools, err := regClient.client.ListTools(ctx)
		if err != nil {
			continue // Skip failed remote servers
		}

		// Tools from client.ListTools() already have the prefix applied.
		// The maps are shared accumulators; discoverable is collected
		// per server and appended (the merge appends, so a shared slice
		// value would silently drop entries).
		perServer := remoteMergeState{
			toolToServer:  newToolToServer,
			excludedTools: newExcludedTools,
			nativeIndex:   newNativeToolIndex,
		}
		s.mergeRemoteTools(regClient, tools, &perServer)
		freshDiscoverableRemoteTools = append(freshDiscoverableRemoteTools, perServer.discoverable...)
	}

	// Move from map to slice and sort for consistent ordering
	newNativeToolCache := make([]MCPTool, 0, len(newNativeToolIndex))
	for _, v := range newNativeToolIndex {
		newNativeToolCache = append(newNativeToolCache, v)
	}
	sort.Slice(newNativeToolCache, func(i, j int) bool { return newNativeToolCache[i].Name < newNativeToolCache[j].Name })

	// Phase 3: Atomically swap in new maps under write lock
	s.mu.Lock()
	s.nativeToolCache = newNativeToolCache
	s.toolToServer = newToolToServer
	s.excludedTools = newExcludedTools
	s.mu.Unlock()

	// Phase 4: Refresh discoverable remote tools in the internal registry.
	// Remove the previously registered discoverable remote tools, then register
	// the freshly fetched ones so tool_search reflects the current remote state.
	// The internal registry manages its own lock independently of s.mu.
	for _, name := range oldDiscoverableRemoteTools {
		s.internalRegistry.UnregisterTool(name)
	}
	for _, tool := range freshDiscoverableRemoteTools {
		toolCopy := tool
		s.internalRegistry.RegisterMCPTool(&toolCopy, s.remoteToolHandler(toolCopy.Name))
	}

	return nil
}

// ToolSource reports which server a tool name actually resolves to, using
// the same resolution order as [Server.CallTool] (native tools first, then
// the fast toolToServer lookup, then the namespace-prefix fallback for a
// federated tool discovered via remote tool_search): "" for a tool
// registered directly on this server, or a federated tool's remote
// server's own [Client.BaseURL] — a stable identifier two tool names share
// if and only if they came from the same server, unlike Namespace, which
// callers may leave empty (or, in principle, reuse) with no uniqueness
// guarantee. ok is false if name doesn't resolve via any of these (an
// unknown name, or one that only a per-request [ToolProvider] would
// resolve, which has no persistent "source" the way a registered remote
// server does).
//
// Intended for a host enforcing that an MCP Apps view may only reach a
// tool belonging to the same server as the tool that mounted it — see the
// extension's spec on visibility; the spec's "app" visibility says a view
// may call a tool, not that it may call one on a *different* server than
// its own, which visibility alone can't distinguish once two federated
// servers are aggregated under one endpoint with no namespace prefix.
func (s *Server) ToolSource(name string) (source string, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, exists := s.tools[name]; exists {
		return "", true
	}
	if regClient, exists := s.toolToServer[name]; exists {
		return regClient.client.BaseURL(), true
	}
	for _, rc := range s.remoteClients {
		if rc.namespace != "" && strings.HasPrefix(name, rc.namespace+rc.client.separator) {
			return rc.client.BaseURL(), true
		}
	}
	return "", false
}

// searchRemoteServers calls tool_search on each remote server that has it enabled,
// prefixing returned tool names with the server's namespace.
// Returns nil if no remotes have remoteSearch enabled.
func (s *Server) searchRemoteServers(ctx context.Context, query string, maxResults int) []SearchResult {
	s.mu.RLock()
	clients := make([]*registeredClient, 0, len(s.remoteClients))
	for _, rc := range s.remoteClients {
		if rc.remoteSearch {
			clients = append(clients, rc)
		}
	}
	s.mu.RUnlock()

	if len(clients) == 0 {
		return nil
	}

	var allResults []SearchResult

	for _, rc := range clients {
		searchResults, err := rc.client.ToolSearch(ctx, query, maxResults)
		if err != nil {
			continue
		}

		for _, raw := range searchResults {
			result := SearchResult{
				Score:       0,
				InputSchema: firstPresent(raw, "inputSchema", "input_schema"),
			}
			if name, ok := raw["name"].(string); ok {
				if rc.namespace != "" {
					result.Name = rc.namespace + rc.client.separator + name
				} else {
					result.Name = name
				}
			}
			if desc, ok := raw["description"].(string); ok {
				result.Description = desc
			}
			if score, ok := raw["score"].(float64); ok {
				result.Score = score
			}
			// The remote's raw result already carries these — this loop
			// just never read them, so a tool discovered via tool_search
			// through a connecting server silently lost its MCP Apps
			// linkage (_meta.ui.resourceUri) and icons, even though the
			// exact same tool federated via plain tools/list keeps both
			// (see RegisterTools/ListToolsWithContext, which copy Meta and
			// Icons straight through). Same iconsFromRaw decode as
			// parseToolsResult's tools/list handling in client.go.
			if keywordsRaw, ok := raw["keywords"].([]any); ok {
				for _, k := range keywordsRaw {
					if ks, ok := k.(string); ok {
						result.Keywords = append(result.Keywords, ks)
					}
				}
			}
			if meta, ok := raw["_meta"].(map[string]any); ok {
				result.Meta = meta
			}
			if iconsRaw, ok := raw["icons"]; ok {
				result.Icons = iconsFromRaw(iconsRaw)
			}
			allResults = append(allResults, result)
		}
	}

	return allResults
}
