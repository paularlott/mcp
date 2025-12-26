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
)

// ToolVisibility defines how tools are exposed to clients
type ToolVisibility int

const (
	// ToolVisibilityVisible - Tools appear in ListTools() and are searchable via tool_search
	ToolVisibilityVisible ToolVisibility = iota

	// ToolVisibilityHidden - Tools don't appear in ListTools() and are NOT searchable
	ToolVisibilityHidden

	// ToolVisibilityOnDemand - Tools don't appear in ListTools() but ARE searchable via tool_search
	ToolVisibilityOnDemand
)

var supportedProtocolVersions = []string{
	"2024-11-05",
	"2025-03-26",
	"2025-06-18",
	"2025-11-25",
}

var (
	ErrUnknownTool      = errors.New("unknown tool")
	ErrUnknownParameter = errors.New("parameter not found")
)

// registeredTool represents a registered tool
type registeredTool struct {
	Name         string
	Description  string
	Schema       map[string]interface{}
	OutputSchema map[string]interface{}
	Handler      ToolHandler
}

// ToolRegistry is an interface for registering tools that are discoverable but not in ListTools.
// This avoids circular imports with the discovery package.
type ToolRegistry interface {
	// RegisterMCPTool registers a tool for discovery/search
	RegisterMCPTool(tool *MCPTool, handler ToolHandler, keywords ...string)
}

// Server represents an MCP server instance
type Server struct {
	name           string
	version        string
	instructions   string
	tools          map[string]*registeredTool
	remoteClients  map[string]*registeredClient
	toolToServer   map[string]*registeredClient
	toolCache      []MCPTool
	mu             sync.RWMutex
	sessionManager SessionManager // Pluggable session management
	registry       ToolRegistry   // Optional: for OnDemand tools
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
		name:          name,
		version:       version,
		instructions:  "",
		tools:         make(map[string]*registeredTool),
		remoteClients: make(map[string]*registeredClient),
		toolToServer:  make(map[string]*registeredClient),
		toolCache:     make([]MCPTool, 0),
	}
}

// SetSessionManager sets a custom session manager for the server
// For most deployments, use the default EnableSessionManagement() (JWT-based)
// Only use this if you need custom session storage (e.g., Redis for revocation)
func (s *Server) SetSessionManager(manager SessionManager) {
	s.sessionManager = manager
}

// SetToolRegistry sets the tool registry for OnDemand tools.
// Tools with OnDemand visibility will be registered here for discovery via tool_search.
func (s *Server) SetToolRegistry(registry ToolRegistry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registry = registry
}

// EnableSessionManagement enables JWT-based session management (stateless, production-ready)
// This is the recommended approach for all deployments as it:
// - Requires no external dependencies (Redis, Database)
// - Scales horizontally without coordination
// - Works across all server instances
// - Validates sessions in ~12 microseconds
//
// Only use SetSessionManager() if you need session revocation (Redis, Database)
func (s *Server) EnableSessionManagement() error {
	signingKey, err := GenerateSigningKey()
	if err != nil {
		return fmt.Errorf("failed to generate signing key: %w", err)
	}
	s.sessionManager = NewJWTSessionManager(signingKey, 30*time.Minute)
	return nil
}

// EnableSessionManagementWithKey enables JWT session management with a specific signing key
// Use this to maintain sessions across server restarts (persist the key securely)
func (s *Server) EnableSessionManagementWithKey(signingKey []byte, ttl time.Duration) {
	s.sessionManager = NewJWTSessionManager(signingKey, ttl)
}

// CleanupExpiredSessions removes sessions that haven't been used in the specified duration
// Only works if a session manager is configured
func (s *Server) CleanupExpiredSessions(maxIdleTime time.Duration) error {
	if s.sessionManager == nil {
		return nil
	}
	return s.sessionManager.CleanupExpiredSessions(context.Background(), maxIdleTime)
}

func (s *Server) SetInstructions(instructions string) {
	s.instructions = instructions
}

// RegisterTool registers a new tool with the server
func (s *Server) RegisterTool(tool *ToolBuilder, handler ToolHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()

	regTool := &registeredTool{
		Name:         tool.name,
		Description:  tool.description,
		Schema:       tool.buildSchema(),
		OutputSchema: tool.buildOutputSchema(),
		Handler:      handler,
	}
	s.tools[tool.name] = regTool

	newTool := MCPTool{
		Name:        tool.name,
		Description: tool.description,
		InputSchema: regTool.Schema,
	}
	if regTool.OutputSchema != nil {
		newTool.OutputSchema = regTool.OutputSchema
	}

	// Replace any existing cache entries with the same name to avoid duplicates
	filtered := make([]MCPTool, 0, len(s.toolCache))
	for _, t := range s.toolCache {
		if t.Name != tool.name {
			filtered = append(filtered, t)
		}
	}
	filtered = append(filtered, newTool)
	s.toolCache = filtered

	// Sort to maintain consistent ordering
	sort.Slice(s.toolCache, func(i, j int) bool {
		return s.toolCache[i].Name < s.toolCache[j].Name
	})
}

// RegisterRemoteServer registers a remote MCP server
func (s *Server) RegisterRemoteServer(url, namespace string, auth AuthProvider) error {
	return s.RegisterRemoteServerWithVisibility(url, namespace, auth, ToolVisibilityVisible)
}

// RegisterRemoteServerHidden registers a remote MCP server with hidden tools
func (s *Server) RegisterRemoteServerHidden(url, namespace string, auth AuthProvider) error {
	return s.RegisterRemoteServerWithVisibility(url, namespace, auth, ToolVisibilityHidden)
}

// RegisterRemoteServerWithVisibility registers a remote MCP server with the specified visibility.
// - ToolVisibilityVisible: Tools appear in ListTools() and tool_search
// - ToolVisibilityHidden: Tools don't appear in ListTools() or tool_search (but can be called directly)
// - ToolVisibilityOnDemand: Tools don't appear in ListTools() but are in tool_search
//
// For OnDemand tools, a registry must be set via SetToolRegistry().
func (s *Server) RegisterRemoteServerWithVisibility(url, namespace string, auth AuthProvider, visibility ToolVisibility) error {
	client := NewClient(url, auth)

	s.mu.Lock()
	defer s.mu.Unlock()

	regClient := &registeredClient{
		client:     client,
		namespace:  namespace,
		visibility: visibility,
	}
	s.remoteClients[url] = regClient

	// Fetch tools from the new server and add to cache/lookup
	ctx := context.Background()
	tools, err := client.ListTools(ctx)
	if err != nil {
		// Server registration succeeded, but we couldn't fetch tools
		// This is not a fatal error - tools can be fetched later via RefreshTools
		return nil
	}

	// Add tools to cache, lookup, or registry based on visibility
	for _, tool := range tools {
		toolName := tool.Name
		if namespace != "" {
			toolName = namespace + "/" + tool.Name
		}

		// Add to lookup for execution
		s.toolToServer[toolName] = regClient

		switch visibility {
		case ToolVisibilityVisible:
			// Add to toolCache for ListTools()
			toolWithNamespace := tool
			toolWithNamespace.Name = toolName
			// filter existing
			filtered := make([]MCPTool, 0, len(s.toolCache))
			for _, t := range s.toolCache {
				if t.Name != toolName {
					filtered = append(filtered, t)
				}
			}
			filtered = append(filtered, toolWithNamespace)
			s.toolCache = filtered

		case ToolVisibilityOnDemand:
			// Register with ToolRegistry for tool_search
			if s.registry != nil {
				toolWithNamespace := tool
				toolWithNamespace.Name = toolName
				// Create a handler that delegates to the server's CallTool
				handler := func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
					return s.CallTool(ctx, toolName, req.args)
				}
				// Extract keywords from description for better search
				// In the future, this could be enhanced to parse keywords from the tool itself
				s.registry.RegisterMCPTool(&toolWithNamespace, handler)
			}
		case ToolVisibilityHidden:
			// Only in toolToServer for direct execution, not visible elsewhere
		}
	}

	// Sort to maintain consistent ordering
	sort.Slice(s.toolCache, func(i, j int) bool {
		return s.toolCache[i].Name < s.toolCache[j].Name
	})

	return nil
}

// RefreshTools manually refreshes the tool cache and lookup from all remote servers
func (s *Server) RefreshTools() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Build new maps
	newToolCache := []MCPTool{}
	newToolIndex := make(map[string]MCPTool)
	newToolToServer := make(map[string]*registeredClient)

	// Add local tools to new cache
	for _, tool := range s.tools {
		toolItem := MCPTool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.Schema,
		}
		if tool.OutputSchema != nil {
			toolItem.OutputSchema = tool.OutputSchema
		}
		newToolIndex[toolItem.Name] = toolItem
	}

	// Add remote tools to new cache and lookup
	for _, regClient := range s.remoteClients {
		ctx := context.Background()
		tools, err := regClient.client.ListTools(ctx)
		if err != nil {
			continue // Skip failed remote servers
		}

		for _, tool := range tools {
			toolName := tool.Name
			if regClient.namespace != "" {
				toolName = regClient.namespace + "/" + tool.Name
			}

			// Add to new lookup
			newToolToServer[toolName] = regClient

			// Add/update in new cache only if visible
			if regClient.visibility == ToolVisibilityVisible {
				tool.Name = toolName
				newToolIndex[toolName] = tool
			}
		}
	}

	// Move from map to slice and sort for consistent ordering
	newToolCache = newToolCache[:0]
	for _, v := range newToolIndex {
		newToolCache = append(newToolCache, v)
	}
	sort.Slice(newToolCache, func(i, j int) bool { return newToolCache[i].Name < newToolCache[j].Name })

	// Swap in new maps
	s.toolCache = newToolCache
	s.toolToServer = newToolToServer

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
		if s.sessionManager == nil {
			http.Error(w, "Session management not enabled", http.StatusMethodNotAllowed)
			return
		}

		sessionID := r.Header.Get("MCP-Session-Id")
		if sessionID == "" {
			http.Error(w, "MCP-Session-Id header required", http.StatusBadRequest)
			return
		}

		if err := s.sessionManager.DeleteSession(r.Context(), sessionID); err != nil {
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
		if s.sessionManager != nil {
			sessionID := r.Header.Get("MCP-Session-Id")
			if sessionID == "" {
				http.Error(w, "MCP-Session-Id header required", http.StatusBadRequest)
				return
			}

			valid, err := s.sessionManager.ValidateSession(r.Context(), sessionID)
			if err != nil {
				http.Error(w, fmt.Sprintf("Session validation error: %v", err), http.StatusInternalServerError)
				return
			}

			if !valid {
				http.Error(w, "Session not found", http.StatusNotFound)
				return
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

func isSupportedProtocolVersion(version string) bool {
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

	result := initializeResult{
		ProtocolVersion: protocolVersion,
		Capabilities:    s.buildCapabilities(protocolVersion),
		ServerInfo: serverInfo{
			Name:    s.name,
			Version: s.version,
		},
		Instructions: s.instructions,
	}

	// Generate and store session if session management is enabled
	if s.sessionManager != nil {
		sessionID, err := s.sessionManager.CreateSession(r.Context(), protocolVersion)
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

// ListTools returns all registered tools including remote ones (direct API)
func (s *Server) ListTools() []MCPTool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]MCPTool, len(s.toolCache))
	copy(result, s.toolCache)
	return result
}

func (s *Server) handleToolsList(w http.ResponseWriter, r *http.Request, req *MCPRequest) {
	tools := s.ListTools()
	result := map[string]interface{}{
		"tools": tools,
	}
	s.sendMCPResponse(w, req.ID, result)
}

// CallTool executes a tool directly with namespace support (direct API)
// It checks local tools first, then remote tools, then deferred/dynamic tools from the registry
func (s *Server) CallTool(ctx context.Context, name string, args map[string]interface{}) (*ToolResponse, error) {
	s.mu.RLock()

	// Try local tools first
	if tool, exists := s.tools[name]; exists {
		handler := tool.Handler
		s.mu.RUnlock()
		toolReq := &ToolRequest{args: args}
		return handler(ctx, toolReq)
	}

	// Fast lookup for remote tools
	if regClient, exists := s.toolToServer[name]; exists {
		client := regClient.client
		namespace := regClient.namespace
		s.mu.RUnlock()
		// Extract original tool name (remove namespace if present)
		toolName := name
		if namespace != "" {
			toolName = strings.TrimPrefix(name, namespace+"/")
		}
		return client.CallTool(ctx, toolName, args)
	}

	s.mu.RUnlock()

	return nil, ErrUnknownTool
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
