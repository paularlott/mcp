package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

const (
	MCPProtocolVersionLatest = "2025-06-18"
	MCPProtocolVersionMin    = "2024-11-05"
)

var supportedProtocolVersions = []string{
	"2024-11-05",
	"2025-03-26",
	"2025-06-18",
}

// registeredTool represents a registered tool
type registeredTool struct {
	Name         string
	Description  string
	Schema       map[string]interface{}
	OutputSchema map[string]interface{}
	Handler      ToolHandler
}

type ResourceHandler func(ctx context.Context, uri string) (*ResourceResponse, error)

type registeredResource struct {
	URI         string
	Name        string
	Description string
	MimeType    string
	Handler     ResourceHandler
}

// Server represents an MCP server instance
type Server struct {
	name      string
	version   string
	tools     map[string]*registeredTool
	resources map[string]*registeredResource
	mu        sync.RWMutex
}

// NewServer creates a new MCP server instance
func NewServer(name, version string) *Server {
	return &Server{
		name:      name,
		version:   version,
		tools:     make(map[string]*registeredTool),
		resources: make(map[string]*registeredResource),
	}
}

// RegisterTool registers a new tool with the server
func (s *Server) RegisterTool(tool *ToolBuilder, handler ToolHandler) {
	// Finalize any pending parameter
	if len(tool.params) == 0 || tool.params[len(tool.params)-1].name == "" {
		// Remove empty param if exists
		if len(tool.params) > 0 && tool.params[len(tool.params)-1].name == "" {
			tool.params = tool.params[:len(tool.params)-1]
		}
	}

	s.mu.Lock()
	s.tools[tool.name] = &registeredTool{
		Name:         tool.name,
		Description:  tool.description,
		Schema:       tool.buildSchema(),
		OutputSchema: tool.buildOutputSchema(),
		Handler:      handler,
	}
	s.mu.Unlock()
}

func (s *Server) RegisterResource(uri, name, description, mimeType string, handler ResourceHandler) {
	s.mu.Lock()
	s.resources[uri] = &registeredResource{
		URI:         uri,
		Name:        name,
		Description: description,
		MimeType:    mimeType,
		Handler:     handler,
	}
	s.mu.Unlock()
}

// HandleRequest handles MCP protocol requests
func (s *Server) HandleRequest(w http.ResponseWriter, r *http.Request) {
	// Handle CORS preflight
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Max-Age", "86400")
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Validate Content-Type
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" && !strings.HasPrefix(contentType, "application/json;") {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}

	// Set CORS headers for actual requests
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req mcpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendMCPError(w, nil, -32700, "Parse error", map[string]interface{}{
			"details": err.Error(),
		})
		return
	}

	// Validate JSONRPC version
	if req.JSONRPC != "2.0" {
		s.sendMCPError(w, req.ID, -32600, "Invalid Request", map[string]interface{}{
			"details": "JSONRPC field must be '2.0'",
		})
		return
	}

	// Ensure ID is never nil - use empty string as default
	if req.ID == nil {
		req.ID = ""
	}

	fmt.Println("MCP Request:", req.Method)

	switch req.Method {
	case "initialize":
		s.handleInitialize(w, r, &req)
	case "ping":
		s.handlePing(w, r, &req)
	case "tools/list":
		s.handleToolsList(w, r, &req)
	case "tools/call":
		s.handleToolsCall(w, r, &req)
	case "resources/list":
		s.handleResourcesList(w, r, &req)
	case "resources/read":
		s.handleResourcesRead(w, r, &req)
	default:
		s.sendMCPError(w, req.ID, -32601, "Method not found", map[string]interface{}{
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

func (s *Server) handleInitialize(w http.ResponseWriter, r *http.Request, req *mcpRequest) {
	// Parse initialization parameters
	var params initializeParams
	if req.Params != nil {
		paramsBytes, err := json.Marshal(req.Params)
		if err != nil {
			s.sendMCPError(w, req.ID, -32602, "Invalid params", nil)
			return
		}
		if err := json.Unmarshal(paramsBytes, &params); err != nil {
			s.sendMCPError(w, req.ID, -32602, "Invalid params", nil)
			return
		}
	}

	// Determine which protocol version to use
	protocolVersion := MCPProtocolVersionLatest
	if params.ProtocolVersion != "" {
		if !isSupportedProtocolVersion(params.ProtocolVersion) {
			s.sendMCPError(w, req.ID, -32602, "Unsupported protocol version", map[string]interface{}{
				"requested": params.ProtocolVersion,
				"supported": supportedProtocolVersions,
			})
			return
		}
		protocolVersion = params.ProtocolVersion
	}

	fmt.Println("Using Protocol Version:", protocolVersion)

	result := initializeResult{
		ProtocolVersion: protocolVersion,
		Capabilities:    s.buildCapabilities(protocolVersion),
		ServerInfo: serverInfo{
			Name:    s.name,
			Version: s.version,
		},
	}

	s.sendMCPResponse(w, req.ID, result)
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request, req *mcpRequest) {
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

// ListTools returns all registered tools (direct API)
func (s *Server) ListTools() []MCPTool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var tools []MCPTool
	for _, tool := range s.tools {
		toolItem := MCPTool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.Schema,
		}
		if tool.OutputSchema != nil {
			toolItem.OutputSchema = tool.OutputSchema
		}
		tools = append(tools, toolItem)
	}
	return tools
}

func (s *Server) handleToolsList(w http.ResponseWriter, r *http.Request, req *mcpRequest) {
	tools := s.ListTools()
	result := map[string]interface{}{
		"tools": tools,
	}
	s.sendMCPResponse(w, req.ID, result)
}

// CallTool executes a tool directly (direct API)
func (s *Server) CallTool(ctx context.Context, name string, args map[string]interface{}) (*ToolResponse, error) {
	s.mu.RLock()
	tool, exists := s.tools[name]
	s.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("tool '%s' not found", name)
	}

	toolReq := &ToolRequest{args: args}
	return tool.Handler(ctx, toolReq)
}

func (s *Server) handleToolsCall(w http.ResponseWriter, r *http.Request, req *mcpRequest) {
	var params toolCallParams
	paramsBytes, err := json.Marshal(req.Params)
	if err != nil {
		s.sendMCPError(w, req.ID, -32602, "Invalid params", nil)
		return
	}

	if err := json.Unmarshal(paramsBytes, &params); err != nil {
		s.sendMCPError(w, req.ID, -32602, "Invalid params", nil)
		return
	}

	response, err := s.CallTool(r.Context(), params.Name, params.Arguments)
	if err != nil {
		s.sendMCPError(w, req.ID, -32603, fmt.Sprintf("Tool execution failed: %v", err), nil)
		return
	}

	s.sendMCPResponse(w, req.ID, ToolResult{
		Content:           response.Content,
		StructuredContent: response.StructuredContent,
		IsError:           false,
	})
}

func (s *Server) handleResourcesList(w http.ResponseWriter, r *http.Request, req *mcpRequest) {
	s.mu.RLock()
	var resources []mcpResource
	for _, resource := range s.resources {
		resources = append(resources, mcpResource{
			URI:         resource.URI,
			Name:        resource.Name,
			Description: resource.Description,
			MimeType:    resource.MimeType,
		})
	}
	s.mu.RUnlock()

	result := map[string]interface{}{
		"resources": resources,
	}

	s.sendMCPResponse(w, req.ID, result)
}

func (s *Server) handleResourcesRead(w http.ResponseWriter, r *http.Request, req *mcpRequest) {
	var params resourceReadParams
	paramsBytes, err := json.Marshal(req.Params)
	if err != nil {
		s.sendMCPError(w, req.ID, -32602, "Invalid params", nil)
		return
	}

	if err := json.Unmarshal(paramsBytes, &params); err != nil {
		s.sendMCPError(w, req.ID, -32602, "Invalid params", nil)
		return
	}

	s.mu.RLock()
	resource, exists := s.resources[params.URI]
	s.mu.RUnlock()

	if !exists {
		s.sendMCPError(w, req.ID, -32601, "Resource not found", nil)
		return
	}

	response, err := resource.Handler(r.Context(), params.URI)
	if err != nil {
		s.sendMCPError(w, req.ID, -32603, fmt.Sprintf("Resource read failed: %v", err), nil)
		return
	}

	s.sendMCPResponse(w, req.ID, response)
}

func (s *Server) sendMCPResponse(w http.ResponseWriter, id interface{}, result interface{}) {
	response := mcpResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func (s *Server) sendMCPError(w http.ResponseWriter, id interface{}, code int, message string, data interface{}) {
	response := mcpResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &mcpError{
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
