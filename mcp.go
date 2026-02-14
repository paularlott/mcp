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
)

// registeredTool represents a registered tool
type registeredTool struct {
	Name          string
	Description   string
	Schema        map[string]interface{}
	OutputSchema  map[string]interface{}
	Handler       ToolHandler
	Visibility    ToolVisibility
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
type Server struct {
	name                 string
	version              string
	instructions         string
	tools                map[string]*registeredTool   // All registered tools (native + discoverable)
	remoteClients        map[string]*registeredClient // Remote MCP servers
	toolToServer         map[string]*registeredClient // Tool name -> remote client mapping
	nativeToolCache      []MCPTool                    // Native tools (visible in tools/list)
	mu                   sync.RWMutex
	sessionManager       SessionManager    // Pluggable session management
	internalRegistry     *internalRegistry // Registry for discoverable tools (searchable)
	hasDiscoverableTools bool              // Track if any statically-registered discoverable tools exist
}

// registeredClient holds a remote client with its configuration
type registeredClient struct {
	client     *Client
	namespace  string
	visibility ToolVisibility
}

// NewServer creates a new MCP server instance
func NewServer(name, version string) *Server {
	return &Server{
		name:             name,
		version:          version,
		instructions:     "",
		tools:            make(map[string]*registeredTool),
		remoteClients:    make(map[string]*registeredClient),
		toolToServer:     make(map[string]*registeredClient),
		nativeToolCache:  make([]MCPTool, 0),
		internalRegistry: newInternalRegistry(),
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
	return s.getDiscoveryToolsWithContext(context.Background())
}

// getDiscoveryToolsWithContext returns the discovery tools (tool_search, execute_tool) as MCPTool structs.
// These are generated dynamically, not stored in nativeToolCache.
func (s *Server) getDiscoveryToolsWithContext(ctx context.Context) []MCPTool {
	toolSearch := NewTool(ToolSearchName, "Search for available tools by name, description, or keywords. Returns matching tools with their names, descriptions, input schemas, and relevance scores (0.0 to 1.0, where 1.0 is an exact match and higher scores indicate better relevance). Use this to find tools that aren't visible in tools/list. After finding a tool: if it was not in tools/list, use execute_tool to call it; if it was in tools/list, you can call it directly. Omit query to list all available tools.",
		String("query", "Search query to find relevant tools (searches name, description, and keywords). Omit to list all tools."),
		Number("max_results", "Maximum number of results to return (default: 5)"),
	)

	executeTool := NewTool(ExecuteToolName, "Execute a tool by name with the given arguments. This is the ONLY way to call tools discovered via tool_search. Discovered tools cannot be called directly - you must use this execute_tool function.",
		String("name", "The exact name of the tool to execute (must be a tool found via tool_search)", Required()),
		Object("arguments", "The arguments to pass to the tool (matching the schema from tool_search results)"),
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
// Searches discoverable tools from both static registration and providers.
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

	// Search with provider tools included
	results := s.internalRegistry.SearchWithAdditionalTools(ctx, query, maxResults, discoverableFromProviders)
	if len(results) == 0 {
		return NewToolResponseText("No tools found. Try different keywords or a broader search term."), nil
	}
	return NewToolResponseJSON(results), nil
}

// handleExecuteTool handles the execute_tool meta-tool execution
func (s *Server) handleExecuteTool(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
	name, err := req.String("name")
	if err != nil || name == "" {
		return NewToolResponseText("Tool name is required"), nil
	}

	args, _ := req.Object("arguments")
	if args == nil {
		args = make(map[string]interface{})
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
}

// registerNativeToolLocked registers a native tool while the lock is already held.
// Native tools appear in tools/list and are directly callable.
// Keywords are stored and used in show-all mode when native tools become searchable.
func (s *Server) registerNativeToolLocked(tool *ToolBuilder, handler ToolHandler, keywords ...string) {
	regTool := &registeredTool{
		Name:          tool.name,
		Description:   tool.Description(),
		Schema:        tool.buildSchema(),
		OutputSchema:  tool.buildOutputSchema(),
		Handler:       handler,
		Visibility:    ToolVisibilityNative,
	}
	s.tools[tool.name] = regTool

	newTool := MCPTool{
		Name:          tool.name,
		Description:   tool.Description(),
		InputSchema:   regTool.Schema,
		Keywords:      keywords, // Store keywords for show-all mode
	}
	if regTool.OutputSchema != nil {
		newTool.OutputSchema = regTool.OutputSchema
	}

	// Use binary search to find insertion point for sorted order
	idx := sort.Search(len(s.nativeToolCache), func(i int) bool {
		return s.nativeToolCache[i].Name >= tool.name
	})

	// Check if we're replacing an existing tool
	if idx < len(s.nativeToolCache) && s.nativeToolCache[idx].Name == tool.name {
		// Replace in place
		s.nativeToolCache[idx] = newTool
	} else {
		// Insert at sorted position
		s.nativeToolCache = append(s.nativeToolCache, MCPTool{})
		copy(s.nativeToolCache[idx+1:], s.nativeToolCache[idx:])
		s.nativeToolCache[idx] = newTool
	}

	// DO NOT add to internal registry here - native tools are only searchable in show-all mode
}

// registerDiscoverableToolLocked registers a discoverable tool while the lock is already held.
// Discoverable tools do NOT appear in tools/list but can be discovered through tool_search.
// Keywords from the ToolBuilder are merged with the additional keywords parameter.
func (s *Server) registerDiscoverableToolLocked(tool *ToolBuilder, handler ToolHandler, keywords ...string) {
	// Mark that we have statically-registered discoverable tools
	s.hasDiscoverableTools = true

	// Register tool metadata for execution (but not in nativeToolCache)
	regTool := &registeredTool{
		Name:         tool.name,
		Description:  tool.Description(),
		Schema:       tool.buildSchema(),
		OutputSchema: tool.buildOutputSchema(),
		Handler:      handler,
		Visibility:   ToolVisibilityDiscoverable,
	}
	s.tools[tool.name] = regTool

	// Merge keywords from ToolBuilder and parameter
	allKeywords := append(tool.Keywords(), keywords...)

	// Add to internal registry for search
	s.internalRegistry.RegisterTool(tool, handler, allKeywords...)
}

// RegisterTools registers multiple tools with the server in a single batch.
// This is more efficient than calling RegisterTool multiple times as it only
// sorts the cache once at the end.
// Each tool's visibility is determined by whether Discoverable() was called on its ToolBuilder.
func (s *Server) RegisterTools(tools ...*ToolRegistration) {
	if len(tools) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, tr := range tools {
		if tr.Tool.IsDiscoverable() {
			// Register as discoverable tool
			s.hasDiscoverableTools = true

			regTool := &registeredTool{
				Name:         tr.Tool.name,
				Description:  tr.Tool.Description(),
				Schema:       tr.Tool.buildSchema(),
				OutputSchema: tr.Tool.buildOutputSchema(),
				Handler:      tr.Handler,
				Visibility:   ToolVisibilityDiscoverable,
			}
			s.tools[tr.Tool.name] = regTool

			// Add to internal registry for search (use keywords from ToolBuilder)
			s.internalRegistry.RegisterTool(tr.Tool, tr.Handler, tr.Tool.Keywords()...)
		} else {
			// Register as native tool
			regTool := &registeredTool{
				Name:          tr.Tool.name,
				Description:   tr.Tool.Description(),
				Schema:        tr.Tool.buildSchema(),
				OutputSchema:  tr.Tool.buildOutputSchema(),
				Handler:       tr.Handler,
				Visibility:    ToolVisibilityNative,
			}
			s.tools[tr.Tool.name] = regTool
		}
	}

	// Rebuild native cache from native tools only
	s.rebuildNativeToolCacheLocked()
}

// rebuildNativeToolCacheLocked rebuilds the native tool cache from all native tools.
// Must be called with s.mu held.
func (s *Server) rebuildNativeToolCacheLocked() {
	s.nativeToolCache = make([]MCPTool, 0)
	for _, tool := range s.tools {
		if tool.Visibility == ToolVisibilityNative {
			toolItem := MCPTool{
				Name:          tool.Name,
				Description:   tool.Description,
				InputSchema:   tool.Schema,
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

// RegisterRemoteServer registers a remote MCP server with native visibility.
// Remote server tools appear in tools/list and are directly callable.
// Use RegisterRemoteServerDiscoverable for tools that should only be searchable.
func (s *Server) RegisterRemoteServer(client *Client) error {
	return s.registerRemoteServerWithVisibility(client, ToolVisibilityNative)
}

// RegisterRemoteServerDiscoverable registers a remote MCP server with discoverable visibility.
// Remote server tools do NOT appear in tools/list but are searchable via tool_search.
// This automatically registers tool_search and execute_tool if not already registered.
func (s *Server) RegisterRemoteServerDiscoverable(client *Client) error {
	return s.registerRemoteServerWithVisibility(client, ToolVisibilityDiscoverable)
}

// registerRemoteServerWithVisibility is the internal implementation for registering remote servers.
func (s *Server) registerRemoteServerWithVisibility(client *Client, visibility ToolVisibility) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// For discoverable visibility, mark that we have statically-registered discoverable tools
	if visibility == ToolVisibilityDiscoverable {
		s.hasDiscoverableTools = true
	}

	// Extract namespace from client namespace (remove trailing separator)
	namespace := strings.TrimSuffix(client.Namespace(), client.separator)

	regClient := &registeredClient{
		client:     client,
		namespace:  namespace,
		visibility: visibility,
	}
	s.remoteClients[client.baseURL] = regClient

	// Fetch tools from the new server
	ctx := context.Background()
	tools, err := client.ListTools(ctx)
	if err != nil {
		// Server registration succeeded, but we couldn't fetch tools
		// This is not a fatal error - tools can be fetched later via RefreshTools
		return nil
	}

	// Add tools based on visibility
	for _, tool := range tools {
		toolName := tool.Name

		// Add to lookup for execution
		s.toolToServer[toolName] = regClient

		toolWithNamespace := tool
		toolWithNamespace.Name = toolName

		switch visibility {
		case ToolVisibilityNative:
			// Add to nativeToolCache for tools/list
			filtered := make([]MCPTool, 0, len(s.nativeToolCache))
			for _, t := range s.nativeToolCache {
				if t.Name != toolName {
					filtered = append(filtered, t)
				}
			}
			filtered = append(filtered, toolWithNamespace)
			s.nativeToolCache = filtered

		case ToolVisibilityDiscoverable:
			// Add to internal registry for tool_search
			localToolName := toolName // capture for closure
			handler := func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
				return s.CallTool(ctx, localToolName, req.args)
			}
			s.internalRegistry.RegisterMCPTool(&toolWithNamespace, handler)
		}
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
	s.mu.RUnlock()

	// Phase 2: Build new maps without holding lock (network calls happen here)
	newNativeToolIndex := make(map[string]MCPTool)
	newToolToServer := make(map[string]*registeredClient)

	// Add local native tools to new cache
	for _, tool := range localNativeTools {
		toolItem := MCPTool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.Schema,
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
		tools, err := regClient.client.ListTools(ctx)
		if err != nil {
			continue // Skip failed remote servers
		}

		for _, tool := range tools {
			// Tools from client.ListTools() already have the prefix applied
			toolName := tool.Name

			// Add to lookup for execution
			newToolToServer[toolName] = regClient

			// Only add native remote tools to the native cache
			if regClient.visibility == ToolVisibilityNative {
				tool.Name = toolName
				newNativeToolIndex[toolName] = tool
			}
			// Note: Discoverable remote tools are in the internalRegistry which is not refreshed here
		}
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
	s.mu.Unlock()

	return nil
}

// HandleRequest handles MCP protocol requests
func (s *Server) HandleRequest(w http.ResponseWriter, r *http.Request) {
	// Handle CORS preflight
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, MCP-Protocol-Version, MCP-Session-Id")
		w.Header().Set("Access-Control-Max-Age", "86400")
		w.WriteHeader(http.StatusOK)
		return
	}

	// Set CORS headers for actual requests
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Handle DELETE requests (session termination)
	if r.Method == http.MethodDelete {
		sm := s.getSessionManager()
		if sm == nil {
			http.Error(w, "Session management not enabled", http.StatusMethodNotAllowed)
			return
		}

		sessionID := r.Header.Get("MCP-Session-Id")
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

	// Handle GET requests (SSE streaming not yet implemented)
	if r.Method == http.MethodGet {
		w.Header().Set("Allow", "POST, OPTIONS")
		http.Error(w, "Method not allowed - SSE streaming not implemented", http.StatusMethodNotAllowed)
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
		s.sendMCPError(w, nil, ErrorCodeParseError, "Parse error", map[string]interface{}{
			"details": err.Error(),
		})
		return
	}

	// Validate JSONRPC version
	if req.JSONRPC != "2.0" {
		s.sendMCPError(w, req.ID, ErrorCodeInvalidRequest, "Invalid Request", map[string]interface{}{
			"details": "JSONRPC field must be '2.0'",
		})
		return
	}

	// Ensure ID is never nil - use empty string as default
	if req.ID == nil {
		req.ID = ""
	}

	// For non-initialize requests, validate MCP-Protocol-Version header
	if req.Method != "initialize" {
		protocolVersion := r.Header.Get("MCP-Protocol-Version")

		// Per spec: assume 2025-03-26 if missing for backwards compatibility
		if protocolVersion == "" {
			protocolVersion = "2025-03-26"
		}

		// Validate protocol version
		if !isSupportedProtocolVersion(protocolVersion) {
			http.Error(w, fmt.Sprintf("Unsupported MCP-Protocol-Version: %s", protocolVersion), http.StatusBadRequest)
			return
		}

		// Validate session ID if session management is enabled
		sm := s.getSessionManager()
		if sm != nil {
			sessionID := r.Header.Get("MCP-Session-Id")
			if sessionID == "" {
				http.Error(w, "MCP-Session-Id header required", http.StatusBadRequest)
				return
			}

			valid, err := sm.ValidateSession(r.Context(), sessionID)
			if err != nil {
				http.Error(w, fmt.Sprintf("Session validation error: %v", err), http.StatusInternalServerError)
				return
			}

			if !valid {
				http.Error(w, "Session not found", http.StatusNotFound)
				return
			}

			// Get show-all flag from session and apply to context
			showAll, _ := sm.GetShowAll(r.Context(), sessionID)
			if showAll {
				r = r.WithContext(WithShowAllTools(r.Context()))
			}
		} else {
			// No session management - check header/query on each request
			if GetShowAllFromRequest(r) {
				r = r.WithContext(WithShowAllTools(r.Context()))
			}
		}
	}

	switch req.Method {
	case "initialize":
		s.handleInitialize(w, r, &req)
	case "ping":
		s.handlePing(w, r, &req)
	case "tools/list":
		s.handleToolsList(w, r, &req)
	case "tools/call":
		s.handleToolsCall(w, r, &req)
	default:
		s.sendMCPError(w, req.ID, ErrorCodeMethodNotFound, "Method not found", map[string]interface{}{
			"method": req.Method,
		})
	}
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

func (s *Server) handleInitialize(w http.ResponseWriter, r *http.Request, req *MCPRequest) {
	var params initializeParams
	if err := s.parseParams(req, &params); err != nil {
		s.sendMCPError(w, req.ID, ErrorCodeInvalidParams, "Invalid params", nil)
		return
	}

	// Determine which protocol version to use
	protocolVersion := MCPProtocolVersionLatest
	if params.ProtocolVersion != "" {
		if !isSupportedProtocolVersion(params.ProtocolVersion) {
			s.sendMCPError(w, req.ID, ErrorCodeInvalidParams, "Unsupported protocol version", map[string]interface{}{
				"requested": params.ProtocolVersion,
				"supported": supportedProtocolVersions,
			})
			return
		}
		protocolVersion = params.ProtocolVersion
	}

	// Check for show-all flag from header or query param
	showAll := GetShowAllFromRequest(r)

	// Read instructions under lock
	s.mu.RLock()
	instructions := s.instructions
	s.mu.RUnlock()

	result := initializeResult{
		ProtocolVersion: protocolVersion,
		Capabilities:    s.buildCapabilities(protocolVersion),
		ServerInfo: serverInfo{
			Name:    s.name,
			Version: s.version,
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
		w.Header().Set("MCP-Session-Id", sessionID)
	}

	s.sendMCPResponse(w, req.ID, result)
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request, req *MCPRequest) {
	s.sendMCPResponse(w, req.ID, map[string]interface{}{})
}

func (s *Server) buildCapabilities(protocolVersion string) capabilities {
	caps := capabilities{
		Tools: map[string]interface{}{},
	}

	// Add version-specific capabilities
	switch protocolVersion {
	case "2024-11-05":
		// Basic capabilities for 2024-11-05
		caps.Tools = map[string]interface{}{}
		caps.Resources = map[string]interface{}{}
	default: // 2025-03-26, 2025-06-18 and use latest if unknown
		// Default to latest
		caps.Tools = map[string]interface{}{
			"listChanged": false,
		}
		caps.Resources = map[string]interface{}{
			"subscribe":   false,
			"listChanged": false,
		}
	}

	return caps
}

// ListTools returns all native tools including remote ones (direct API).
// If discoverable tools are registered, discovery tools (tool_search, execute_tool) are also included.
// The returned slice is a copy, safe for concurrent use and modification.
//
// Performance Note: This method allocates and copies the tool cache on every call.
// For high-frequency polling scenarios, consider caching the result on the caller side.
// The tool list only changes when RegisterTool, RegisterRemoteServer, or RefreshTools is called.
func (s *Server) ListTools() []MCPTool {
	s.mu.RLock()
	hasDiscoverable := s.hasDiscoverableTools
	result := make([]MCPTool, len(s.nativeToolCache))
	copy(result, s.nativeToolCache)
	s.mu.RUnlock()

	// If we have static discoverable tools, add discovery tools
	if hasDiscoverable {
		discoveryTools := s.getDiscoveryTools()
		result = append(result, discoveryTools...)
	}

	// Sort for consistent ordering
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return result
}

func (s *Server) handleToolsList(w http.ResponseWriter, r *http.Request, req *MCPRequest) {
	tools := s.ListToolsWithContext(r.Context())
	result := map[string]interface{}{
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
func (s *Server) CallTool(ctx context.Context, name string, args map[string]interface{}) (*ToolResponse, error) {
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

	s.mu.RUnlock()

	// Try native providers from context (per-request dynamic tools)
	return callToolFromProviders(ctx, name, args)
}

func (s *Server) handleToolsCall(w http.ResponseWriter, r *http.Request, req *MCPRequest) {
	var params ToolCallParams
	if err := s.parseParams(req, &params); err != nil {
		s.sendMCPError(w, req.ID, ErrorCodeInvalidParams, "Invalid params", nil)
		return
	}

	response, err := s.CallTool(r.Context(), params.Name, params.Arguments)
	if err != nil {
		// Check if it's a ToolError with specific MCP error code
		if toolErr, ok := err.(*ToolError); ok {
			s.sendMCPError(w, req.ID, toolErr.Code, toolErr.Message, toolErr.Data)
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

func (s *Server) sendMCPResponse(w http.ResponseWriter, id interface{}, result interface{}) {
	response := MCPResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func (s *Server) parseParams(req *MCPRequest, target interface{}) error {
	if req.Params == nil {
		return nil
	}
	paramsBytes, err := json.Marshal(req.Params)
	if err != nil {
		return err
	}
	return json.Unmarshal(paramsBytes, target)
}

func (s *Server) sendMCPError(w http.ResponseWriter, id interface{}, code int, message string, data interface{}) {
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
	w.WriteHeader(http.StatusOK) // Always 200 for JSON-RPC responses
	json.NewEncoder(w).Encode(response)
}
