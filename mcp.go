package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	MCPProtocolVersionLatest = "2025-11-25"
	MCPProtocolVersionMin    = "2024-11-05"

	// DefaultSessionTTL is the default session lifetime for JWT session management
	DefaultSessionTTL = 30 * time.Minute

	// DefaultOAuthRefreshTimeout is the default timeout for OAuth token refresh operations
	DefaultOAuthRefreshTimeout = 30 * time.Second

	// HTTP header names used by the MCP Streamable HTTP transport. Go's
	// net/http canonicalizes header keys internally (MCP-Session-Id becomes
	// Mcp-Session-Id on the wire), so the value used in Set/Get calls does
	// not affect behaviour — but using the spec form aids readability.
	headerSessionID       = "MCP-Session-Id"
	headerProtocolVersion = "MCP-Protocol-Version"
)

// supportedProtocolVersions lists all MCP protocol versions this server accepts.
// Versions are in ISO date format (YYYY-MM-DD) representing when the protocol
// version was standardized. During initialize, the client may request a specific
// version and the server will use it if supported, otherwise returns an error.
// For non-initialize requests, the MCP-Protocol-Version header is validated
// against this list.
var supportedProtocolVersions = []string{
	"2024-11-05",
	"2025-03-26",
	"2025-06-18",
	"2025-11-25",
}

var (
	ErrUnknownTool      = errors.New("unknown tool")
	ErrUnknownParameter = errors.New("parameter not found")
	ErrToolFiltered     = errors.New("tool is filtered out")
	ErrUnknownResource  = errors.New("unknown resource")
	ErrUnknownPrompt    = errors.New("unknown prompt")
)

func firstPresent(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}

// registeredTool represents a registered tool
type registeredTool struct {
	Name         string
	Description  string
	Schema       map[string]any
	OutputSchema map[string]any
	Meta         map[string]any
	Icons        []Icon
	Handler      ToolHandler
	Visibility   ToolVisibility
}

// Server represents an MCP server instance.
//
// # Design Philosophy
//
// The Server struct is the central hub for MCP protocol handling. It intentionally
// combines several related concerns to provide a cohesive API:
//
//   - Core Identity: name, version, and instructions for protocol negotiation
//   - Tool Management: local tools with thread-safe registration and caching
//   - Federation: remote MCP server integration with namespacing
//   - Sessions: pluggable session management for stateful deployments
//   - Discovery: optional tool registry for large tool sets
//
// This design prioritizes ease of use over strict separation of concerns. A typical
// server setup requires only a few lines:
//
//	server := mcp.NewServer("myapp", "1.0.0")
//	server.RegisterTool(myTool, myHandler)
//	http.HandleFunc("/mcp", server.HandleRequest)
//
// For advanced use cases, the server delegates to specialized components:
//   - SessionManager interface for custom session storage
//   - Client for remote server federation
//
// Thread Safety: All methods are safe for concurrent use. The server uses RWMutex
// for read-heavy operations (ListTools, CallTool) with minimal lock contention.
//
// Lifecycle: configure the server (SetInstructions, SetSessionManager,
// RegisterTool/RegisterTools, RegisterRemoteServer/ReplaceRemoteServers) before
// you start serving with HandleRequest. Each individual method is safe to call
// concurrently, but mutating registration while requests are in flight is
// discouraged: a tools/list or tools/call running concurrently with a
// RegisterTool may observe the tool set either before or after the change. For
// per-request or per-user tools, prefer ToolProvider with WithToolProviders
// rather than mutating the shared server.
type Server struct {
	name                   string
	version                string
	instructions           string
	icons                  []Icon                       // Visual identifiers for this server's own identity (serverInfo)
	tools                  map[string]*registeredTool   // All registered tools (native + discoverable)
	remoteClients          map[string]*registeredClient // Remote MCP servers
	toolToServer           map[string]*registeredClient // Tool name -> remote client mapping
	excludedTools          map[string]*registeredClient // App tools skipped by an ExcludeApps registration (namespaced), blocked from the prefix-fallback dispatch too
	nativeToolCache        []MCPTool                    // Native tools (visible in tools/list)
	mu                     sync.RWMutex
	sessionManager         SessionManager                 // Pluggable session management
	internalRegistry       *internalRegistry              // Registry for discoverable tools (searchable)
	hasDiscoverableTools   bool                           // Track if any discoverable tools exist (local or remote)
	resources              map[string]*registeredResource // Static resources keyed by URI
	resourceTemplates      []*registeredResourceTemplate  // Parameterized resource templates
	prompts                map[string]*registeredPrompt   // Static prompts keyed by name
	notifications          *notificationHub               // Fan-out for listChanged notifications
	extensionCapabilities  map[string]any                 // This server's declared extensions.<id> settings
	lastClientCapabilities map[string]any                 // Most recently negotiated client capabilities (see ClientCapabilities)
	shutdownCh             chan struct{}                  // Closed by Shutdown; see modern.go's subscriptions/listen graceful closure
	shutdownOnce           sync.Once
	originValidator        OriginValidator // nil = defaultOriginValidator; see origin.go
}

func (s *Server) recalcHasDiscoverableToolsLocked() {
	s.hasDiscoverableTools = false
	for _, t := range s.tools {
		if t.Visibility == ToolVisibilityDiscoverable {
			s.hasDiscoverableTools = true
			return
		}
	}
	for _, rc := range s.remoteClients {
		if rc.visibility == ToolVisibilityDiscoverable || rc.remoteSearch {
			s.hasDiscoverableTools = true
			return
		}
	}
}

// registeredClient holds a remote client with its configuration
type registeredClient struct {
	client       *Client
	namespace    string
	visibility   ToolVisibility
	remoteSearch bool // Whether to delegate tool_search to this remote
	excludeApps  bool // Skip tools linked to a ui:// resource (MCP Apps views)
}

// NewServer creates a new MCP server instance.
func NewServer(name, version string) *Server {
	return &Server{
		name:              name,
		version:           version,
		instructions:      "",
		tools:             make(map[string]*registeredTool),
		remoteClients:     make(map[string]*registeredClient),
		toolToServer:      make(map[string]*registeredClient),
		excludedTools:     make(map[string]*registeredClient),
		nativeToolCache:   make([]MCPTool, 0),
		internalRegistry:  newInternalRegistry(),
		resources:         make(map[string]*registeredResource),
		resourceTemplates: make([]*registeredResourceTemplate, 0),
		prompts:           make(map[string]*registeredPrompt),
		notifications:     newNotificationHub(),
		shutdownCh:        make(chan struct{}),
	}
}

// SetSessionManager sets a custom session manager for the server.
// For JWT-based sessions, use NewJWTSessionManager or NewJWTSessionManagerWithAutoKey.
//
// Example:
//
//	sm, _ := mcp.NewJWTSessionManagerWithAutoKey(30 * time.Minute)
//	server.SetSessionManager(sm)
//
// Use a custom SessionManager when you need:
//   - Session revocation (logout functionality, security incidents)
//   - Session listing (admin dashboards, audit trails)
//   - Custom session metadata
func (s *Server) SetSessionManager(manager SessionManager) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionManager = manager
}

// getDiscoveryTools returns the discovery tools (tool_search, execute_tool) as MCPTool structs.
// These are generated dynamically, not stored in nativeToolCache.
func (s *Server) getDiscoveryTools() []MCPTool {
	toolSearch := NewTool(ToolSearchName, "Search the tools available on this MCP server, including ones hidden from the initial tool list, by name, description, or keyword. Returns matching tools with their names, descriptions, input schemas, and relevance scores (0.0 to 1.0, where 1.0 is an exact match and higher scores indicate better relevance). Call execute_tool on this MCP server with the exact name to execute a match. Omit query to list all tools available on this server.",
		String("query", "Search query to find relevant tools (searches name, description, and keywords). Omit to list all tools available on this server."),
		Number("max_results", "Maximum number of results to return (default: 5)"),
	)

	executeTool := NewTool(ExecuteToolName, "Execute a tool on this MCP server that was found via tool_search but may not appear in this server's tool list.",
		String("name", "The exact name of the tool to execute (must be a tool found via tool_search)", Required()),
		Object("parameters", "The parameters to pass to the tool (matching the schema from tool_search results)"),
	)

	discoveryTools := []MCPTool{
		{
			Name:        ToolSearchName,
			Description: toolSearch.Description(),
			InputSchema: toolSearch.BuildSchema(),
		},
		{
			Name:        ExecuteToolName,
			Description: executeTool.Description(),
			InputSchema: executeTool.BuildSchema(),
		},
	}

	return discoveryTools
}

// handleToolSearch handles the tool_search meta-tool execution.
// Searches discoverable tools from both static registration and providers,
// as well as native tools that are already in tools/list.
// If remote MCP servers expose tool_search, it delegates to them and prefixes results.
func (s *Server) handleToolSearch(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
	query := req.StringOr("query", "")
	maxResults := req.IntOr("max_results", 5)
	if maxResults <= 0 {
		maxResults = 5
	}
	if maxResults > 100 {
		maxResults = 100
	}

	// Get discoverable tools from providers to include in search
	discoverableFromProviders := getDiscoverableToolsFromProviders(ctx)

	// Get native tools: statically registered + from providers
	s.mu.RLock()
	listedTools := make([]MCPTool, len(s.nativeToolCache))
	copy(listedTools, s.nativeToolCache)
	s.mu.RUnlock()
	listedTools = append(listedTools, getNativeToolsFromProviders(ctx)...)

	// Search with provider tools and listed tools included
	results := s.internalRegistry.SearchWithAdditionalTools(ctx, query, maxResults, discoverableFromProviders, listedTools)

	// Delegate tool_search to remote servers that have it enabled
	remoteResults := s.searchRemoteServers(ctx, query, maxResults)
	if len(remoteResults) > 0 {
		results = append(results, remoteResults...)

		// Re-sort to merge remote results with local by score
		sort.Slice(results, func(i, j int) bool {
			if results[i].Score != results[j].Score {
				return results[i].Score > results[j].Score
			}
			return results[i].Name < results[j].Name
		})

		// Truncate to maxResults
		if len(results) > maxResults {
			results = results[:maxResults]
		}
	}

	if len(results) == 0 {
		return NewToolResponseText("No tools found. Try different keywords or a broader search term."), nil
	}

	return NewToolResponseJSON(results), nil
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

// handleExecuteTool handles the execute_tool meta-tool execution
func (s *Server) handleExecuteTool(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
	name, err := req.String("name")
	if err != nil || name == "" {
		return NewToolResponseText("Tool name is required"), nil
	}

	args, _ := req.Object("parameters")
	if args == nil {
		// Fallback: accept "arguments" for backward compatibility
		args, _ = req.Object("arguments")
	}
	if args == nil {
		args = make(map[string]any)
	}

	// Use server's CallTool which handles local, remote, and provider tools
	response, err := s.CallTool(ctx, name, args)
	if err == ErrUnknownTool {
		return NewToolResponseText("Tool not found: " + name + ". Use tool_search to discover available tools."), nil
	}
	if err != nil {
		return nil, err
	}
	return response, nil
}

// getSessionManager returns the session manager under read lock
func (s *Server) getSessionManager() SessionManager {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionManager
}

// CleanupExpiredSessions removes sessions that haven't been used in the specified duration
// Only works if a session manager is configured
func (s *Server) CleanupExpiredSessions(maxIdleTime time.Duration) error {
	sm := s.getSessionManager()
	if sm == nil {
		return nil
	}
	return sm.CleanupExpiredSessions(context.Background(), maxIdleTime)
}

// SetInstructions sets the server instructions that are returned during protocol initialization.
// Instructions provide guidance to the LLM about how to use the server's capabilities.
func (s *Server) SetInstructions(instructions string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.instructions = instructions
}

// SetIcons attaches visual identifiers to this server's own identity,
// included as serverInfo.icons in initialize (Legacy) and as
// _meta["io.modelcontextprotocol/serverInfo"].icons on every Modern-era
// result and in server/discover. See [Icon] for the shape and the security
// precautions consumers must apply.
func (s *Server) SetIcons(icons ...Icon) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.icons = icons
}

// RegisterTool registers a tool with the server.
// The tool's visibility is determined by whether Discoverable() was called on the ToolBuilder:
//   - Native tools (default): appear in tools/list and are directly callable
//   - Discoverable tools (via .Discoverable(keywords...)): only available via tool_search and execute_tool
//
// Optional keywords parameter is merged with keywords set via Discoverable() for search relevance.
// Keywords are used in show-all mode and for discoverable tool search.
func (s *Server) RegisterTool(tool *ToolBuilder, handler ToolHandler, keywords ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if tool.IsDiscoverable() {
		s.registerDiscoverableToolLocked(tool, handler, keywords...)
	} else {
		s.registerNativeToolLocked(tool, handler, keywords...)
	}
	s.NotifyToolsChanged()
}

// registerNativeToolLocked registers a native tool while the lock is already held.
// Native tools appear in tools/list and are directly callable.
// Keywords are stored and used in show-all mode when native tools become searchable.
func (s *Server) registerNativeToolLocked(tool *ToolBuilder, handler ToolHandler, keywords ...string) {
	prev, existed := s.tools[tool.name]

	regTool := &registeredTool{
		Name:         tool.name,
		Description:  tool.Description(),
		Schema:       tool.buildSchema(),
		OutputSchema: tool.buildOutputSchema(),
		Meta:         tool.meta,
		Icons:        tool.icons,
		Handler:      handler,
		Visibility:   ToolVisibilityNative,
	}
	s.tools[tool.name] = regTool

	s.internalRegistry.UnregisterTool(tool.name)

	if existed && prev.Visibility == ToolVisibilityDiscoverable {
		s.recalcHasDiscoverableToolsLocked()
	}

	newTool := MCPTool{
		Name:        tool.name,
		Description: tool.Description(),
		InputSchema: regTool.Schema,
		Meta:        regTool.Meta,
		Icons:       regTool.Icons,
		Keywords:    keywords,
	}
	if regTool.OutputSchema != nil {
		newTool.OutputSchema = regTool.OutputSchema
	}

	idx := sort.Search(len(s.nativeToolCache), func(i int) bool {
		return s.nativeToolCache[i].Name >= tool.name
	})

	if idx < len(s.nativeToolCache) && s.nativeToolCache[idx].Name == tool.name {
		s.nativeToolCache[idx] = newTool
	} else {
		s.nativeToolCache = append(s.nativeToolCache, MCPTool{})
		copy(s.nativeToolCache[idx+1:], s.nativeToolCache[idx:])
		s.nativeToolCache[idx] = newTool
	}
}

// registerDiscoverableToolLocked registers a discoverable tool while the lock is already held.
// Discoverable tools do NOT appear in tools/list but can be discovered through tool_search.
// Keywords from the ToolBuilder are merged with the additional keywords parameter.
func (s *Server) registerDiscoverableToolLocked(tool *ToolBuilder, handler ToolHandler, keywords ...string) {
	s.hasDiscoverableTools = true

	regTool := &registeredTool{
		Name:         tool.name,
		Description:  tool.Description(),
		Schema:       tool.buildSchema(),
		OutputSchema: tool.buildOutputSchema(),
		Meta:         tool.meta,
		Icons:        tool.icons,
		Handler:      handler,
		Visibility:   ToolVisibilityDiscoverable,
	}
	s.tools[tool.name] = regTool

	idx := sort.Search(len(s.nativeToolCache), func(i int) bool {
		return s.nativeToolCache[i].Name >= tool.name
	})
	if idx < len(s.nativeToolCache) && s.nativeToolCache[idx].Name == tool.name {
		s.nativeToolCache = append(s.nativeToolCache[:idx], s.nativeToolCache[idx+1:]...)
	}

	allKeywords := append(tool.Keywords(), keywords...)

	s.internalRegistry.RegisterTool(tool, handler, allKeywords...)
}

// RegisterTools registers multiple tools with the server in a single batch,
// notifying subscribers once at the end.
// Each tool's visibility is determined by whether Discoverable() was called on its ToolBuilder.
func (s *Server) RegisterTools(tools ...*ToolRegistration) {
	if len(tools) == 0 {
		return
	}

	s.mu.Lock()
	// Delegate to the single-tool paths so both APIs behave identically —
	// a re-registered tool must leave the search registry (discoverable →
	// native) and merge extra keywords the same way in either API.
	for _, tr := range tools {
		if tr.Tool.IsDiscoverable() {
			s.registerDiscoverableToolLocked(tr.Tool, tr.Handler)
		} else {
			s.registerNativeToolLocked(tr.Tool, tr.Handler)
		}
	}
	s.mu.Unlock()

	s.NotifyToolsChanged()
}

// UnregisterTool removes a tool by name from the server.
// Returns true if the tool was found and removed, false otherwise.
// This is safe to call concurrently.
func (s *Server) UnregisterTool(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	tool, exists := s.tools[name]
	if !exists {
		return false
	}

	delete(s.tools, name)

	if tool.Visibility == ToolVisibilityNative {
		idx := sort.Search(len(s.nativeToolCache), func(i int) bool {
			return s.nativeToolCache[i].Name >= name
		})
		if idx < len(s.nativeToolCache) && s.nativeToolCache[idx].Name == name {
			s.nativeToolCache = append(s.nativeToolCache[:idx], s.nativeToolCache[idx+1:]...)
		}
	} else {
		s.internalRegistry.UnregisterTool(name)
	}

	s.hasDiscoverableTools = false
	s.recalcHasDiscoverableToolsLocked()

	s.NotifyToolsChanged()
	return true
}

// rebuildNativeToolCacheLocked rebuilds the native tool cache from all native tools.
// Must be called with s.mu held.
func (s *Server) rebuildNativeToolCacheLocked() {
	s.nativeToolCache = make([]MCPTool, 0)
	for _, tool := range s.tools {
		if tool.Visibility == ToolVisibilityNative {
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
			s.nativeToolCache = append(s.nativeToolCache, toolItem)
		}
	}

	// Sort for consistent ordering
	sort.Slice(s.nativeToolCache, func(i, j int) bool {
		return s.nativeToolCache[i].Name < s.nativeToolCache[j].Name
	})
}

// ToolRegistration pairs a tool builder with its handler for batch registration.
type ToolRegistration struct {
	Tool    *ToolBuilder
	Handler ToolHandler
}

// NewToolRegistration creates a tool registration for use with RegisterTools.
func NewToolRegistration(tool *ToolBuilder, handler ToolHandler) *ToolRegistration {
	return &ToolRegistration{Tool: tool, Handler: handler}
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

// HandleRequest handles MCP protocol requests
func (s *Server) HandleRequest(w http.ResponseWriter, r *http.Request) {
	// Validate Origin before anything else — the Streamable HTTP transport's
	// MUST, protecting against DNS rebinding (a malicious page's JS in a
	// victim's browser reaching this server, which would otherwise trust
	// anything arriving over localhost). This runs ahead of the OPTIONS
	// preflight branch too: CORS response headers only stop a browser from
	// reading a disallowed response, they don't stop the server from having
	// already acted on the request, which is exactly what this check is
	// for. See origin.go's defaultOriginValidator for the policy and how to
	// widen it.
	origin := r.Header.Get("Origin")
	if !s.originAllowed(origin) {
		http.Error(w, "Origin not allowed", http.StatusForbidden)
		return
	}

	// Handle CORS preflight
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", corsOriginHeader(origin))
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, DELETE")
		// The Modern era's Mcp-Method/Mcp-Name headers (required on every
		// request — see modern.go) aren't in the static list below, so a
		// cross-origin Modern browser client's preflight would otherwise
		// fail; reflecting the browser's own Access-Control-Request-Headers
		// back, when present, additionally covers Mcp-Param-{Name} (a
		// server-declared, per-tool-parameter header set — see the
		// x-mcp-header spec section — whose names can't be enumerated
		// ahead of time in a static list at all).
		allowHeaders := "Content-Type, Authorization, MCP-Protocol-Version, MCP-Session-Id, Mcp-Method, Mcp-Name"
		if requested := r.Header.Get("Access-Control-Request-Headers"); requested != "" {
			allowHeaders = requested
		}
		w.Header().Set("Access-Control-Allow-Headers", allowHeaders)
		w.Header().Set("Access-Control-Max-Age", "86400")
		w.WriteHeader(http.StatusOK)
		return
	}

	// Set CORS headers for actual requests
	w.Header().Set("Access-Control-Allow-Origin", corsOriginHeader(origin))

	// Handle DELETE requests (session termination)
	if r.Method == http.MethodDelete {
		sm := s.getSessionManager()
		if sm == nil {
			http.Error(w, "Session management not enabled", http.StatusMethodNotAllowed)
			return
		}

		sessionID := r.Header.Get(headerSessionID)
		if sessionID == "" {
			http.Error(w, "MCP-Session-Id header required", http.StatusBadRequest)
			return
		}

		if err := sm.DeleteSession(r.Context(), sessionID); err != nil {
			http.Error(w, fmt.Sprintf("Failed to delete session: %v", err), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		return
	}

	// Handle GET requests: open a long-lived SSE stream for server->client
	// notifications, but only when the client asks for an event stream (the
	// Streamable HTTP push channel). A plain GET stays a 405.
	if r.Method == http.MethodGet {
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			s.handleSSEStream(w, r)
			return
		}
		w.Header().Set("Allow", "POST, OPTIONS")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, GET, DELETE, OPTIONS")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Validate Content-Type
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" && !strings.HasPrefix(contentType, "application/json;") {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}

	var req MCPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendProtocolAwareError(w, r, &req, nil, ErrorCodeParseError, "Parse error", map[string]any{
			"details": err.Error(),
		})
		return
	}

	// Validate JSONRPC version
	if req.JSONRPC != "2.0" {
		s.sendProtocolAwareError(w, r, &req, req.ID, ErrorCodeInvalidRequest, "Invalid Request", map[string]any{
			"details": "JSONRPC field must be '2.0'",
		})
		return
	}

	// Ensure ID is never nil - use empty string as default
	if req.ID == nil {
		req.ID = ""
	}

	// Modern-era (protocol revision 2026-07-28+) requests are detected by a
	// signal only a Modern client sends (the Mcp-Method header, or
	// io.modelcontextprotocol/protocolVersion in _meta) and are handled by a
	// dedicated stateless path in modern.go. A request with neither signal
	// falls through to every line below completely unchanged.
	if isModernRequest(r, &req) {
		s.handleModernRequest(w, r, &req)
		return
	}

	// For non-initialize requests, validate MCP-Protocol-Version header
	if req.Method != "initialize" {
		// Per spec: assume 2025-03-26 if missing for backwards compatibility
		protocolVersion, versionOK := negotiateProtocolVersion(r.Header.Get(headerProtocolVersion), "2025-03-26")

		// Validate protocol version. Still a 400 (this rejects a header, not
		// a JSON-RPC method call, so it stays outside the "Legacy JSON-RPC
		// errors are always 200" convention — same as it always has been),
		// but now with a proper JSON-RPC error body naming what this Legacy
		// server actually supports (mirroring handleInitialize's identical
		// check on the initialize request itself, mcp.go below), rather
		// than the plain-text, no-body response this used to send, which
		// left a client with no way to learn a version it could retry with.
		// writeModernProtocolError just means "JSON-RPC error at this HTTP
		// status" despite the name — nothing about it is Modern-specific.
		if !versionOK {
			s.writeModernProtocolError(w, req.ID, http.StatusBadRequest, ErrorCodeInvalidParams,
				fmt.Sprintf("Unsupported MCP-Protocol-Version: %s", protocolVersion), map[string]any{
					"requested": protocolVersion,
					"supported": supportedProtocolVersions,
				})
			return
		}

		// Validate session ID if session management is enabled
		if showAll, ok, status, message := s.checkSession(r.Context(), r); !ok {
			http.Error(w, message, status)
			return
		} else if showAll {
			r = r.WithContext(WithShowAllTools(r.Context()))
		} else if sm := s.getSessionManager(); sm == nil {
			// No session management - check header/query on each request
			if GetShowAllFromRequest(r) {
				r = r.WithContext(WithShowAllTools(r.Context()))
			}
		}
	}

	s.dispatchMethod(w, r, &req)
}

// dispatchMethod routes a parsed request to its handler. Both the Legacy path
// above and the Modern path (handleModernRequest, in modern.go — via a
// response-capturing writer so it can reshape the result afterward) call this
// same table, so a method's behavior is defined exactly once regardless of
// which era's request reached it.
// dispatchableMethods lists the methods handleModernRequest (modern.go) may
// dispatch through dispatchMethod's shared switch below, so it can
// distinguish an unknown RPC method (which the 2026-07-28 revision requires
// HTTP 404 for) from an ordinary method-level JSON-RPC error like an unknown
// tool name (which stays 200, per the Streamable HTTP spec's requirement
// that only routing failures — unknown method, unsupported version, header
// mismatch — get a non-200 status).
//
// Deliberately narrower than dispatchMethod's own case list: "initialize"
// and "ping" are both dispatchable there for Legacy clients, but the
// 2026-07-28 revision removes both (stateless per-request replaces the
// initialize handshake; ping is gone outright) — a Modern request naming
// either is exactly as unknown as a method this server never supported at
// all, and must 404 the same way.
var dispatchableMethods = map[string]bool{
	"tools/list":               true,
	"tools/call":               true,
	"resources/list":           true,
	"resources/read":           true,
	"resources/templates/list": true,
	"prompts/list":             true,
	"prompts/get":              true,
}

func (s *Server) dispatchMethod(w http.ResponseWriter, r *http.Request, req *MCPRequest) {
	switch req.Method {
	case "initialize":
		s.handleInitialize(w, r, req)
	case "ping":
		s.handlePing(w, r, req)
	case "tools/list":
		s.handleToolsList(w, r, req)
	case "tools/call":
		s.handleToolsCall(w, r, req)
	case "resources/list":
		s.handleResourcesList(w, r, req)
	case "resources/read":
		s.handleResourcesRead(w, r, req)
	case "resources/templates/list":
		s.handleResourcesTemplatesList(w, r, req)
	case "prompts/list":
		s.handlePromptsList(w, r, req)
	case "prompts/get":
		s.handlePromptsGet(w, r, req)
	default:
		s.sendMCPError(w, req.ID, ErrorCodeMethodNotFound, "Method not found", map[string]any{
			"method": req.Method,
		})
	}
}

// negotiateProtocolVersion resolves the protocol version for one request:
// requested when non-empty, fallback when empty. ok is false when the
// requested version is not supported; callers render their own era- and
// transport-appropriate error.
func negotiateProtocolVersion(requested, fallback string) (version string, ok bool) {
	if requested == "" {
		return fallback, true
	}
	return requested, isSupportedProtocolVersion(requested)
}

// isSupportedProtocolVersion checks if the given version string matches one of the
// supported MCP protocol versions. Protocol versions follow ISO date format (YYYY-MM-DD).
// Leading/trailing whitespace is trimmed before comparison.
func isSupportedProtocolVersion(version string) bool {
	version = strings.TrimSpace(version)
	for _, supported := range supportedProtocolVersions {
		if supported == version {
			return true
		}
	}
	return false
}

// hasDiscoverableToolsNow reports whether the server currently has any discoverable
// tools, either statically registered or exposed by a context-scoped ToolProvider.
func (s *Server) hasDiscoverableToolsNow(ctx context.Context) bool {
	s.mu.RLock()
	hasStatic := s.hasDiscoverableTools
	s.mu.RUnlock()
	return hasStatic || hasDiscoverableToolsFromProviders(ctx)
}

// appendDiscoveryInstructions appends guidance about the discovery tools to whatever
// instructions the server already has (via SetInstructions), without overwriting them.
// This is how a model learns about tool_search/execute_tool from the initialize
// response, which is a stronger, session-wide signal than a tool's own description.
func appendDiscoveryInstructions(instructions string) string {
	hint := fmt.Sprintf("This server has additional tools not shown in the initial tool list - call %s to find them, then %s to invoke them.", ToolSearchName, ExecuteToolName)
	if instructions == "" {
		return hint
	}
	return instructions + "\n\n" + hint
}

func (s *Server) handleInitialize(w http.ResponseWriter, r *http.Request, req *MCPRequest) {
	var params initializeParams
	if err := s.parseParams(req, &params); err != nil {
		s.sendMCPError(w, req.ID, ErrorCodeInvalidParams, "Invalid params", nil)
		return
	}

	// Determine which protocol version to use
	protocolVersion, ok := negotiateProtocolVersion(params.ProtocolVersion, MCPProtocolVersionLatest)
	if !ok {
		s.sendMCPError(w, req.ID, ErrorCodeInvalidParams, "Unsupported protocol version", map[string]any{
			"requested": params.ProtocolVersion,
			"supported": supportedProtocolVersions,
		})
		return
	}

	// Check for show-all flag from header or query param
	showAll := GetShowAllFromRequest(r)

	// Read instructions and icons under lock
	s.mu.RLock()
	instructions := s.instructions
	icons := s.icons
	s.mu.RUnlock()

	// Discovery tools aren't shown in show-all mode, so don't tell the model to use them there.
	if !showAll && s.hasDiscoverableToolsNow(r.Context()) {
		instructions = appendDiscoveryInstructions(instructions)
	}

	// Remember the client's declared capabilities (e.g. capabilities.extensions)
	// so handlers can later check them via ClientCapabilities. See that method's
	// doc comment for the multi-client-HTTP caveat.
	s.mu.Lock()
	s.lastClientCapabilities = params.Capabilities
	s.mu.Unlock()

	result := initializeResult{
		ProtocolVersion: protocolVersion,
		Capabilities:    s.buildCapabilities(protocolVersion),
		ServerInfo: serverInfo{
			Name:    s.name,
			Version: s.version,
			Icons:   icons,
		},
		Instructions: instructions,
	}

	// Generate and store session if session management is enabled
	sm := s.getSessionManager()
	if sm != nil {
		sessionID, err := sm.CreateSession(r.Context(), protocolVersion, showAll)
		if err != nil {
			s.sendMCPError(w, req.ID, ErrorCodeInternalError, "Failed to create session", nil)
			return
		}

		// Set session ID header
		w.Header().Set(headerSessionID, sessionID)
	}

	s.sendMCPResponse(w, req.ID, result)
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request, req *MCPRequest) {
	s.sendMCPResponse(w, req.ID, map[string]any{})
}

func (s *Server) buildCapabilities(protocolVersion string) capabilities {
	caps := capabilities{
		Tools: map[string]any{},
	}

	// Add version-specific capabilities
	switch protocolVersion {
	case "2024-11-05":
		// Basic capabilities for 2024-11-05
		caps.Tools = map[string]any{}
		caps.Resources = map[string]any{}
		caps.Prompts = map[string]any{}
	default: // 2025-03-26, 2025-06-18 and use latest if unknown
		// Default to latest
		caps.Tools = map[string]any{
			"listChanged": true,
		}
		caps.Resources = map[string]any{
			"subscribe":   false,
			"listChanged": true,
		}
		caps.Prompts = map[string]any{
			"listChanged": true,
		}
	}

	s.mu.RLock()
	if len(s.extensionCapabilities) > 0 {
		caps.Extensions = make(map[string]any, len(s.extensionCapabilities))
		for id, settings := range s.extensionCapabilities {
			caps.Extensions[id] = settings
		}
	}
	s.mu.RUnlock()

	return caps
}

// DeclareExtension advertises this server's support for an MCP extension (per
// SEP-1724) in its initialize response, under capabilities.extensions[id].
// Call it during setup, before serving. For example, a server offering MCP
// Apps (SEP-1865) tools would declare:
//
//	server.DeclareExtension(mcp.UIAppsExtensionID, map[string]any{
//		"mimeTypes": []string{mcp.UIAppMimeType},
//	})
func (s *Server) DeclareExtension(id string, settings map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.extensionCapabilities == nil {
		s.extensionCapabilities = map[string]any{}
	}
	s.extensionCapabilities[id] = settings
}

// ClientCapabilities returns the capabilities object (capabilities.extensions,
// roots, sampling, etc., as sent in initialize) declared by the most recently
// initialized client.
//
// For a stdio server — one process per client connection, the common case for
// locally-spawned MCP servers — this reliably reflects the single connected
// client. An HTTP server handling multiple concurrent client sessions should
// not rely on this for per-request decisions: it is a single process-wide
// value reflecting only the most recent initialize seen across all sessions,
// not any particular request's caller. This is deliberately not upgraded to
// per-session tracking here: doing so with an in-memory map would silently
// misbehave in exactly the deployment this library optimizes an HTTP server
// for — horizontal scaling behind a load balancer with [JWTSessionManager],
// where a later request from the same client can land on a different,
// stateless instance that never saw that client's initialize. If you need
// per-request extension awareness on HTTP, the robust option is to register
// UI-linked tools unconditionally (the [UIAppsExtensionID] cost to a
// non-supporting host is nil — it just ignores unknown `_meta`) rather than
// branching on this method. Use [SupportsUIApps] to interpret the MCP Apps
// extension's settings from the returned map.
func (s *Server) ClientCapabilities() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastClientCapabilities
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

// ListTools returns the server's native tools plus discovery tools when
// discoverable tools are registered.
//
// Deprecated: Use ListToolsWithContext instead. ListTools cannot see
// request-scoped ToolProviders, so it omits any per-user or per-request tools
// attached via WithToolProviders. It is retained as a thin wrapper for backwards
// compatibility and may be removed in a future version.
func (s *Server) ListTools() []MCPTool {
	return s.ListToolsWithContext(context.Background())
}

func (s *Server) handleToolsList(w http.ResponseWriter, r *http.Request, req *MCPRequest) {
	tools := s.ListToolsWithContext(r.Context())
	result := map[string]any{
		"tools": tools,
	}
	s.sendMCPResponse(w, req.ID, result)
}

// ListToolsWithContext returns tools based on the context mode.
// Normal mode: returns native tools + native provider tools (+ discovery tools if any discoverable tools exist)
// Show-all mode: returns ALL tools regardless of visibility
// The context is used to retrieve request-scoped tool providers.
func (s *Server) ListToolsWithContext(ctx context.Context) []MCPTool {
	showAll := GetShowAllTools(ctx)
	hasDiscoverableProviders := hasDiscoverableToolsFromProviders(ctx)

	s.mu.RLock()
	nativeTools := make([]MCPTool, len(s.nativeToolCache))
	copy(nativeTools, s.nativeToolCache)
	hasStaticDiscoverable := s.hasDiscoverableTools
	s.mu.RUnlock()

	// Determine if we have any discoverable tools (static or from providers)
	hasDiscoverable := hasStaticDiscoverable || hasDiscoverableProviders

	// Build seen map from native tools
	seen := make(map[string]bool, len(nativeTools))
	for _, tool := range nativeTools {
		seen[tool.Name] = true
	}

	allTools := make([]MCPTool, 0)

	// In show-all mode, include discoverable tools from static registration too
	if showAll {
		// Add all static discoverable tools
		s.mu.RLock()
		for _, tool := range s.tools {
			if tool.Visibility == ToolVisibilityDiscoverable && !seen[tool.Name] {
				mcpTool := MCPTool{
					Name:        tool.Name,
					Description: tool.Description,
					InputSchema: tool.Schema,
					Meta:        tool.Meta,
					Icons:       tool.Icons,
					Visibility:  ToolVisibilityDiscoverable,
				}
				if tool.OutputSchema != nil {
					mcpTool.OutputSchema = tool.OutputSchema
				}
				allTools = append(allTools, mcpTool)
				seen[tool.Name] = true
			}
		}
		s.mu.RUnlock()
	}

	// Include all native tools
	allTools = append(allTools, nativeTools...)

	// Add tools from providers (filtered by visibility unless show-all)
	providerTools := listToolsFromProviders(ctx, seen)
	allTools = append(allTools, providerTools...)

	// If we have discoverable tools, add tool_search and execute_tool
	// BUT skip them in show-all mode (they're meta-tools for discovery, not actual tools)
	if hasDiscoverable && !showAll {
		discoveryTools := s.getDiscoveryTools()
		for _, tool := range discoveryTools {
			if !seen[tool.Name] {
				allTools = append(allTools, tool)
				seen[tool.Name] = true
			}
		}
	}

	// Sort combined results
	sort.Slice(allTools, func(i, j int) bool {
		return allTools[i].Name < allTools[j].Name
	})

	return allTools
}

// CallTool executes a tool directly with namespace support (direct API)
// It checks discovery tools first, then local tools, then remote tools, then providers from context.
func (s *Server) CallTool(ctx context.Context, name string, args map[string]any) (*ToolResponse, error) {
	// Handle discovery tools (tool_search, execute_tool) dynamically
	if name == ToolSearchName {
		return s.handleToolSearch(ctx, NewToolRequest(args))
	}
	if name == ExecuteToolName {
		return s.handleExecuteTool(ctx, NewToolRequest(args))
	}

	s.mu.RLock()

	// Try local tools first
	if tool, exists := s.tools[name]; exists {
		handler := tool.Handler
		schema := tool.Schema
		s.mu.RUnlock()

		// Validate required parameters
		if err := validateRequiredParameters(schema, args); err != nil {
			return nil, err
		}

		toolReq := &ToolRequest{args: args}
		return handler(ctx, toolReq)
	}

	// Fast lookup for remote tools (registered via RegisterRemoteServer)
	if regClient, exists := s.toolToServer[name]; exists {
		client := regClient.client
		namespace := regClient.namespace
		s.mu.RUnlock()
		// Extract original tool name (remove namespace if present)
		toolName := name
		if namespace != "" {
			toolName = strings.TrimPrefix(name, namespace+regClient.client.separator)
		}
		return client.CallTool(ctx, toolName, args)
	}

	// Fallback: match by namespace prefix to find the remote server,
	// then call via execute_tool on that server (for tools discovered via remote tool_search)
	for _, rc := range s.remoteClients {
		if _, excluded := s.excludedTools[name]; excluded {
			continue
		}
		if rc.namespace != "" && strings.HasPrefix(name, rc.namespace+rc.client.separator) {
			client := rc.client
			separator := rc.client.separator
			s.mu.RUnlock()
			toolName := strings.TrimPrefix(name, rc.namespace+separator)
			return client.ExecuteDiscoveredTool(ctx, toolName, args)
		}
	}

	s.mu.RUnlock()

	// Try native providers from context (per-request dynamic tools)
	return callToolFromProviders(ctx, name, args)
}

func (s *Server) handleToolsCall(w http.ResponseWriter, r *http.Request, req *MCPRequest) {
	var params ToolCallParams
	if err := s.parseParams(req, &params); err != nil {
		s.sendProtocolAwareError(w, r, req, req.ID, ErrorCodeInvalidParams, "Invalid params", nil)
		return
	}

	response, err := s.CallTool(r.Context(), params.Name, params.Arguments)
	if err != nil {
		// Check if it's a ToolError with specific MCP error code
		if toolErr, ok := err.(*ToolError); ok {
			s.sendMCPError(w, req.ID, toolErr.Code, toolErr.Message, toolErr.Data)
		} else if err == ErrUnknownTool && s.hasDiscoverableToolsNow(r.Context()) {
			// The caller guessed a tool name directly instead of going through
			// tool_search first; point it at discovery instead of a dead end.
			s.sendMCPError(w, req.ID, ErrorCodeInternalError, fmt.Sprintf("Tool execution failed: unknown tool %q. Use %s to discover available tools, then %s to invoke them.", params.Name, ToolSearchName, ExecuteToolName), nil)
		} else {
			s.sendMCPError(w, req.ID, ErrorCodeInternalError, fmt.Sprintf("Tool execution failed: %v", err), nil)
		}
		return
	}

	s.sendMCPResponse(w, req.ID, ToolResult{
		Content:           response.Content,
		StructuredContent: response.StructuredContent,
		IsError:           false,
	})
}

// checkSession validates the MCP-Session-Id header when session management
// is enabled. ok=false means the request is rejected: status and message
// carry the HTTP error for the caller to render. showAll reports the
// session's stored show-all flag (the SSE stream deliberately ignores it:
// its notifications are broadcast, not session-scoped). With no session
// manager configured the request is anonymous and always passes.
func (s *Server) checkSession(ctx context.Context, r *http.Request) (showAll, ok bool, status int, message string) {
	sm := s.getSessionManager()
	if sm == nil {
		return false, true, 0, ""
	}
	sessionID := r.Header.Get(headerSessionID)
	if sessionID == "" {
		return false, false, http.StatusBadRequest, "MCP-Session-Id header required"
	}
	valid, err := sm.ValidateSession(ctx, sessionID)
	if err != nil {
		return false, false, http.StatusInternalServerError, fmt.Sprintf("Session validation error: %v", err)
	}
	if !valid {
		return false, false, http.StatusNotFound, "Session not found"
	}
	showAll, _ = sm.GetShowAll(ctx, sessionID)
	return showAll, true, 0, ""
}

func (s *Server) sendMCPResponse(w http.ResponseWriter, id any, result any) {
	s.writeMCPResponse(w, http.StatusOK, id, result)
}

// writeMCPResponse writes a JSON-RPC result at the given HTTP status. Legacy
// JSON-RPC is always 200; the Modern era maps routing failures to 4xx — one
// body, both conventions.
func (s *Server) writeMCPResponse(w http.ResponseWriter, status int, id any, result any) {
	response := MCPResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(response)
}

func (s *Server) parseParams(req *MCPRequest, target any) error {
	if req.Params == nil {
		return nil
	}
	paramsBytes, err := json.Marshal(req.Params)
	if err != nil {
		return err
	}
	return json.Unmarshal(paramsBytes, target)
}

// sendProtocolAwareError reports a garbled/malformed request — one that
// failed to decode, didn't carry a valid JSON-RPC envelope, or whose params
// couldn't be unmarshaled into the shape a method expects — as opposed to a
// well-formed request whose particular method/params turned out to be
// semantically invalid (an unknown tool, a missing-but-parseable required
// field, and the like), which correctly stays a 200 JSON-RPC error in both
// eras per the spec's ordinary-method-error convention (see
// finalizeModernResponse's doc comment).
//
// A Modern-era request gets the spec-required HTTP 400 for this class of
// error; Legacy keeps its always-200 JSON-RPC convention, unchanged. Era is
// detected the same way isModernRequest does (req may be a zero-value or
// partially-decoded MCPRequest here — that detection's header check doesn't
// depend on req having decoded successfully).
func (s *Server) sendProtocolAwareError(w http.ResponseWriter, r *http.Request, req *MCPRequest, id any, code int, message string, data any) {
	if isModernRequest(r, req) {
		s.writeModernProtocolError(w, id, http.StatusBadRequest, code, message, data)
		return
	}
	s.sendMCPError(w, id, code, message, data)
}

func (s *Server) sendMCPError(w http.ResponseWriter, id any, code int, message string, data any) {
	// Always 200 for JSON-RPC responses
	s.writeMCPError(w, http.StatusOK, id, code, message, data)
}

// writeMCPError writes a JSON-RPC error at the given HTTP status; see
// writeMCPResponse for the status convention.
func (s *Server) writeMCPError(w http.ResponseWriter, status int, id any, code int, message string, data any) {
	response := MCPResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &MCPError{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(response)
}
