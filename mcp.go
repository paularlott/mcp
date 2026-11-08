package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	MCPProtocolVersionLatest = "2025-11-25"
	MCPProtocolVersionMin    = "2025-03-26"

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
	// Streamed-response settings; see SetResponseStreaming (server_stream.go).
	streamingConfigured bool
	streamingDelay      time.Duration
	streamingKeepAlive  time.Duration

	// Progress rate limit; see SetProgressInterval (request_stream.go).
	progressIntervalSet bool
	progressInterval    time.Duration

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
	maxRequest             int64                          // SetMaxRequestBytes; 0 = DefaultMaxRequestBytes
	httpInflight           map[string]context.CancelFunc  // Legacy HTTP requests that notifications/cancelled can reach
	shutdownOnce           sync.Once
	originValidator        OriginValidator               // nil = defaultOriginValidator; see origin.go
	skills                 map[string]*skillRegistration // Registered skills (SEP-2640), keyed by name
	skillsFedCache         *remoteSkillsCache            // Listings federated from registered remotes (SEP-2640), keyed by namespace
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
		skillsFedCache:    newRemoteSkillsCache(DefaultRemoteToolCacheMaxEntries),
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

// toolSearchTool and executeToolTool define the discovery meta-tools; their
// schemas are both advertised in tools/list and enforced on each call. They
// never change, so each is built once (toolSearchDef, executeToolDef) rather
// than on every tools/list or call.
func toolSearchTool() *ToolBuilder {
	return NewTool(ToolSearchName, "Search the tools available on this MCP server, including ones hidden from the initial tool list, by name, description, or keyword. Returns matching tools with their names, descriptions, input schemas, and relevance scores (0.0 to 1.0, where 1.0 is an exact match and higher scores indicate better relevance). Call execute_tool on this MCP server with the exact name to execute a match. Omit query to list all tools available on this server.",
		String("query", "Search query to find relevant tools (searches name, description, and keywords). Omit to list all tools available on this server."),
		Number("max_results", "Maximum number of results to return (default: 5)"),
	)
}

func executeToolTool() *ToolBuilder {
	return NewTool(ExecuteToolName, "Execute a tool on this MCP server that was found via tool_search but may not appear in this server's tool list.",
		String("name", "The exact name of the tool to execute (must be a tool found via tool_search)", Required()),
		Object("parameters", "The parameters to pass to the tool (matching the schema from tool_search results)"),
	)
}

// discoveryToolDef is a discovery meta-tool's definition as tools/list
// shows it. Its InputSchema is shared by every caller and must not be
// modified.
func discoveryToolDef(b *ToolBuilder) MCPTool {
	return MCPTool{Name: b.Name(), Description: b.Description(), InputSchema: b.BuildSchema()}
}

var (
	toolSearchDef  = sync.OnceValue(func() MCPTool { return discoveryToolDef(toolSearchTool()) })
	executeToolDef = sync.OnceValue(func() MCPTool { return discoveryToolDef(executeToolTool()) })
)

// getDiscoveryTools returns the discovery tools (tool_search, execute_tool) as MCPTool structs.
// They are not stored in nativeToolCache: they appear only while the server
// has discoverable tools.
func (s *Server) getDiscoveryTools() []MCPTool {
	return []MCPTool{toolSearchDef(), executeToolDef()}
}

// handleToolSearch handles the tool_search meta-tool execution.
// Searches discoverable tools from both static registration and providers,
// as well as native tools that are already in tools/list.
// If remote MCP servers expose tool_search, it delegates to them and prefixes results.
func (s *Server) handleToolSearch(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
	args, err := checkArguments(toolSearchDef().InputSchema, req.Args())
	if err != nil {
		return nil, err
	}
	req = NewToolRequest(args)
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

		// A tool can be both listed here and found by the remote's search
		// (an unprefixed remote's listed tools); keep its best match once
		best := make(map[string]int, len(results))
		merged := results[:0]
		for _, r := range results {
			if i, seen := best[r.Name]; seen {
				if r.Score > merged[i].Score {
					merged[i] = r
				}
				continue
			}
			best[r.Name] = len(merged)
			merged = append(merged, r)
		}
		results = merged

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

// handleExecuteTool handles the execute_tool meta-tool execution. Its own
// arguments are checked like any tool's: a request it cannot read is refused
// with a tool execution error, never run with empty arguments.
func (s *Server) handleExecuteTool(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
	name, args, err := executeToolArguments(req.Args())
	if err != nil {
		return nil, err
	}

	// Use server's CallTool which handles local, remote, and provider tools
	response, err := s.CallTool(ctx, name, args)
	if err == ErrUnknownTool {
		return NewToolResponseError("Tool not found: " + name + ". Use tool_search to discover available tools."), nil
	}
	if err != nil {
		return nil, err
	}
	return response, nil
}

// executeToolArguments reads execute_tool's arguments: the target's name and
// its parameters. The parameters may be an object or a string holding a JSON
// object (models often send that shape); "arguments" is accepted in place of
// "parameters" for backward compatibility. Anything else is an error that
// says what to send instead.
func executeToolArguments(raw map[string]any) (string, map[string]any, error) {
	var unknown []string
	for k := range raw {
		if k != "name" && k != "parameters" && k != "arguments" {
			unknown = append(unknown, strconv.Quote(k))
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return "", nil, invalidArguments(fmt.Sprintf(`%s: unknown argument %s; pass the tool's name in "name" and its arguments in "parameters"`, ExecuteToolName, strings.Join(unknown, ", ")), unknown[0][1:len(unknown[0])-1])
	}

	name, _ := raw["name"].(string)
	if name == "" {
		return "", nil, invalidArguments(ExecuteToolName+`: "name" is required: the exact name of a tool found via tool_search`, "name")
	}

	key := "parameters"
	value, hasParams := raw["parameters"]
	if legacy, ok := raw["arguments"]; ok {
		if hasParams {
			return "", nil, invalidArguments(ExecuteToolName+`: pass the tool's arguments in "parameters" only, not also in "arguments"`, "arguments")
		}
		key, value = "arguments", legacy
	}

	switch v := value.(type) {
	case nil:
		return name, map[string]any{}, nil
	case map[string]any:
		return name, v, nil
	case string:
		var obj map[string]any
		if err := json.Unmarshal([]byte(v), &obj); err == nil && obj != nil {
			return name, obj, nil
		}
	}
	return "", nil, invalidArguments(fmt.Sprintf(`%s: %q must be a JSON object of the tool's arguments`, ExecuteToolName, key), key)
}

// getSessionManager returns the session manager under read lock
func (s *Server) getSessionManager() SessionManager {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionManager
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
					Title:       tool.Title,
					Description: tool.Description,
					Annotations: tool.Annotations,
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

	// inputSchema MUST be a JSON Schema object, never null. A provider or
	// remote tool that gave none takes any arguments, which the spec writes
	// as {"type":"object"}.
	for i := range allTools {
		if allTools[i].InputSchema == nil {
			allTools[i].InputSchema = map[string]any{"type": "object"}
		}
	}

	// Sort combined results
	sort.Slice(allTools, func(i, j int) bool {
		return allTools[i].Name < allTools[j].Name
	})

	return allTools
}

// discoveryToolListed reports whether the request's tools/list shows the
// discovery tool name (tool_search or execute_tool), as ListToolsWithContext
// decides: this server's own while the request can reach a discoverable tool
// and is not in show-all mode, or one of that name listed by a remote server
// (a proxy without a namespace lists the remote's own) or a provider.
func (s *Server) discoveryToolListed(ctx context.Context, name string) bool {
	if !GetShowAllTools(ctx) && s.hasDiscoverableToolsNow(ctx) {
		return true
	}
	s.mu.RLock()
	_, remote := s.toolToServer[name]
	s.mu.RUnlock()
	if remote {
		return true
	}
	_, fromProvider := providerForTool(ctx, name)
	return fromProvider
}

// CallTool executes a tool directly with namespace support (direct API)
// It checks discovery tools first, then local tools, then remote tools, then providers from context.
//
// An error from a remote server's tools/call is relayed as that server sent
// it (a *ToolError); see toolsCallResult for how each error reaches the wire.
func (s *Server) CallTool(ctx context.Context, name string, args map[string]any) (*ToolResponse, error) {
	// The discovery tools exist for a request only when its tools/list
	// shows them; otherwise they are unknown tools, like any other tool the
	// list doesn't hold.
	if (name == ToolSearchName || name == ExecuteToolName) && !s.discoveryToolListed(ctx, name) {
		return nil, ErrUnknownTool
	}
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
		outputSchema := tool.OutputSchema
		s.mu.RUnlock()

		// Check the arguments against the tool's inputSchema; the handler
		// sees them as checked (unambiguous numeric and boolean strings
		// converted to the declared type).
		args, err := checkArguments(schema, args)
		if err != nil {
			return nil, err
		}

		toolReq := &ToolRequest{args: args}
		resp, err := handler(ctx, toolReq)
		if err == nil && outputSchema != nil {
			resp = checkStructuredOutput(outputSchema, resp)
		}
		return resp, err
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
		resp, err := client.CallTool(ctx, toolName, args)
		return resp, relayedError(err)
	}

	// Fallback: match by namespace prefix to find the remote server,
	// then call via execute_tool on that server (for tools discovered via remote tool_search)
	var unprefixed []*registeredClient
	for _, rc := range s.remoteClients {
		if _, excluded := s.excludedTools[name]; excluded {
			continue
		}
		if rc.namespace == "" {
			unprefixed = append(unprefixed, rc)
			continue
		}
		if strings.HasPrefix(name, rc.namespace+rc.client.separator) {
			client := rc.client
			separator := rc.client.separator
			s.mu.RUnlock()
			toolName := strings.TrimPrefix(name, rc.namespace+separator)
			resp, err := client.ExecuteDiscoveredTool(ctx, toolName, args)
			return resp, relayedError(err)
		}
	}

	// A single remote registered without a namespace (a proxy for one
	// server) shares this server's tool names, so a name nothing else claims
	// may be one of its tools hidden from tools/list but found by tool_search.
	// With several, the owner can't be told from the name alone.
	if _, fromProvider := providerForTool(ctx, name); len(unprefixed) == 1 && !fromProvider {
		client := unprefixed[0].client
		s.mu.RUnlock()
		resp, err := client.ExecuteDiscoveredTool(ctx, name, args)
		return resp, relayedError(err)
	}

	s.mu.RUnlock()

	// Try native providers from context (per-request dynamic tools)
	return callToolFromProviders(ctx, name, args)
}

// toolsCallWireError maps a tools/call failure to its wire shape
// (code/message/data). Shared by the HTTP and stdio handlers so both
// transports answer identically, including the tool_search hint.
// An unknown tool is a protocol error with the spec's own example code
// (-32602, "Unknown tool: ..."); see toolsCallResult for everything else.
func (s *Server) toolsCallWireError(ctx context.Context, name string, err error) *MCPError {
	var toolErr *ToolError
	if errors.As(err, &toolErr) && (!isModernProtocolErrorCode(toolErr.Code) || isOwnMissingCapability(toolErr)) {
		return &MCPError{Code: toolErr.Code, Message: toolErr.Message, Data: toolErr.Data}
	}
	if errors.Is(err, ErrUnknownTool) {
		msg := fmt.Sprintf("Unknown tool: %s", name)
		// Point at discovery only when the request's tools/list shows the
		// discovery tools (not in show-all mode, and not when the unknown
		// tool is one of them).
		if name != ToolSearchName && name != ExecuteToolName && s.discoveryToolListed(ctx, ToolSearchName) {
			msg += fmt.Sprintf(". Use %s to discover available tools, then %s to invoke them.", ToolSearchName, ExecuteToolName)
		}
		return &MCPError{Code: ErrorCodeInvalidParams, Message: msg, Data: map[string]any{"name": name}}
	}
	return &MCPError{Code: ErrorCodeInternalError, Message: fmt.Sprintf("Tool execution failed: %v", err)}
}

// toolsCallResult turns a handler's outcome into the tools/call result the
// spec defines, shared by the HTTP and stdio handlers. The spec's protocol
// errors for tools/call are an unknown tool, a malformed request and a
// server error, so those are sent as JSON-RPC errors (wireErr): an unknown
// tool, a *ToolError with a server error code, and a *ToolError relayed from
// a remote server as it came. Everything else — arguments failing the
// tool's inputSchema, any other *ToolError a handler or provider returned,
// any plain error — is a tool execution error, reported inside the result
// with isError set, so the model sees the text and can self-correct. legacy
// strips a non-object structuredContent, which only 2026-07-28 allows.
func (s *Server) toolsCallResult(ctx context.Context, name string, response *ToolResponse, err error, legacy bool) (result *ToolResult, wireErr *MCPError) {
	if err != nil {
		var toolErr *ToolError
		switch {
		case errors.Is(err, ErrUnknownTool):
			return nil, s.toolsCallWireError(ctx, name, err)
		case errors.As(err, &toolErr):
			if toolErr.relayed || isServerErrorCode(toolErr.Code) {
				return nil, s.toolsCallWireError(ctx, name, err)
			}
			response = NewToolResponseError(toolErr.Message)
		default:
			response = NewToolResponseError(err.Error())
		}
	}
	if response == nil {
		response = &ToolResponse{}
	}
	content := response.Content
	if content == nil {
		content = []ToolContent{}
	}
	structured := response.StructuredContent
	if legacy {
		structured = legacyStructuredContent(structured)
	}
	return &ToolResult{Content: content, StructuredContent: structured, IsError: response.IsError}, nil
}

// resourcesReadWireError maps a resources/read failure to its wire shape,
// shared by the HTTP and stdio handlers. A wrapped ErrUnknownResource
// (remotes failed outright, not just missed) keeps its diagnosis in data.details.
// A Legacy client gets the Legacy spec's resource-not-found code (-32002);
// 2026-07-28 withdrew that code and requires -32602 instead (legacy=false).
func resourcesReadWireError(uri string, err error, legacy bool) *MCPError {
	if errors.Is(err, ErrUnknownResource) {
		data := map[string]any{"uri": uri}
		if err != ErrUnknownResource {
			data["details"] = err.Error()
		}
		code := ErrorCodeInvalidParams
		if legacy {
			code = ErrorCodeResourceNotFound
		}
		return &MCPError{Code: code, Message: "Resource not found", Data: data}
	}
	var toolErr *ToolError
	if errors.As(err, &toolErr) {
		return &MCPError{Code: toolErr.Code, Message: toolErr.Message, Data: toolErr.Data}
	}
	return &MCPError{Code: ErrorCodeInternalError, Message: fmt.Sprintf("Resource read failed: %v", err)}
}

// promptsGetWireError maps a prompts/get failure to its wire shape, shared
// by the HTTP and stdio handlers.
func promptsGetWireError(name string, err error) *MCPError {
	if err == ErrUnknownPrompt {
		return &MCPError{Code: ErrorCodeInvalidParams, Message: "Prompt not found", Data: map[string]any{"name": name}}
	}
	var toolErr *ToolError
	if errors.As(err, &toolErr) {
		return &MCPError{Code: toolErr.Code, Message: toolErr.Message, Data: toolErr.Data}
	}
	return &MCPError{Code: ErrorCodeInternalError, Message: fmt.Sprintf("Prompt render failed: %v", err)}
}

// checkStructuredOutput enforces the tools spec's rule that a tool with an
// outputSchema "MUST provide structured results that conform to this
// schema": a successful response with no structuredContent, or one that
// does not validate against the whole schema, is turned into a tool
// execution error naming the defect, so the problem surfaces to the model
// and the developer instead of an invalid result going out on the wire. An
// isError response is left alone.
func checkStructuredOutput(outputSchema any, resp *ToolResponse) *ToolResponse {
	if resp == nil || resp.IsError {
		return resp
	}
	if resp.StructuredContent == nil {
		return NewToolResponseError("tool declares an outputSchema but returned no structuredContent")
	}
	if _, ok := outputSchema.(map[string]any); !ok {
		return resp
	}
	raw, err := json.Marshal(resp.StructuredContent)
	if err != nil {
		return NewToolResponseError("structuredContent cannot be encoded: " + err.Error())
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return NewToolResponseError("structuredContent cannot be decoded: " + err.Error())
	}
	if err := validateJSONSchema(outputSchema, value); err != nil {
		return NewToolResponseError("structuredContent does not conform to outputSchema: " + err.Error())
	}
	return resp
}

// jsonValueMatchesType reports whether a decoded JSON value has the JSON
// Schema primitive type want.
func jsonValueMatchesType(v any, want string) bool {
	switch want {
	case "string":
		_, ok := v.(string)
		return ok
	case "number":
		_, ok := jsonNumberValue(v)
		return ok
	case "integer":
		f, ok := jsonNumberValue(v)
		return ok && f == float64(int64(f))
	case "null":
		return v == nil
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	}
	return true
}

// jsonNumberValue reads a decoded JSON number, whichever Go type the
// decoder produced (float64, json.Number, or a Go integer from in-process
// callers).
func jsonNumberValue(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	}
	return 0, false
}

// errInvalidCursor is the pagination spec's answer to a cursor this server
// did not issue: it never paginates, so any cursor is invalid.
var errInvalidCursor = &MCPError{Code: ErrorCodeInvalidParams, Message: "Invalid cursor"}

// cursorIn reports whether params carry a non-empty cursor. params may be the
// decoded HTTP params (map) or a stdio handler's raw params.
func cursorIn(params any) bool {
	switch p := params.(type) {
	case map[string]any:
		c, present := p["cursor"]
		if !present || c == nil {
			return false
		}
		s, isString := c.(string)
		return !isString || s != ""
	case json.RawMessage:
		var shape struct {
			Cursor any `json:"cursor"`
		}
		if len(p) == 0 || json.Unmarshal(p, &shape) != nil || shape.Cursor == nil {
			return false
		}
		s, isString := shape.Cursor.(string)
		return !isString || s != ""
	}
	return false
}

// isModernProtocolErrorCode reports whether code is one the 2026-07-28
// transport reserves for a request's own headers and version. A handler
// (a federated call to an upstream server, say) that surfaces one is
// reporting its own hop's problem, which this server must not relay to its
// caller as if the caller's request were at fault; it is reported as an
// internal error with the message kept.
// isOwnMissingCapability reports whether toolErr is this server's own
// MissingRequiredClientCapabilityError (NewToolErrorMissingClientCapability
// from a handler or provider), which tools/call sends as it is. One relayed
// from a remote server is about this server's own client capabilities on
// that hop, not the caller's, so it is treated like any other foreign
// protocol code.
func isOwnMissingCapability(toolErr *ToolError) bool {
	return toolErr.Code == ErrorCodeMissingRequiredClientCapability && !toolErr.relayed
}

func isModernProtocolErrorCode(code int) bool {
	switch code {
	case ErrorCodeHeaderMismatch, ErrorCodeMissingRequiredClientCapability, ErrorCodeUnsupportedProtocolVersion,
		ErrorCodeParseError, ErrorCodeInvalidRequest, ErrorCodeMethodNotFound:
		// The last three describe the request envelope, which a handler
		// never judges; relaying them would misstate the failure.
		return true
	}
	return false
}
