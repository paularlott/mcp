package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/paularlott/mcp/pool"
)

const (
	mcpClientName    = "mcp-client"
	mcpClientVersion = "1.0.0"
)

// DefaultNamespaceSeparator is the default separator used for namespacing tool names.
// Uses "__" by default for broad client compatibility (some clients such as AntiGravity
// and PhpStorm reject tool names containing dots even though the MCP spec allows them).
var DefaultNamespaceSeparator = "__"

// ToolFilterFunc is a function that determines if a tool should be included.
// It receives the original tool name (without namespace prefix).
// Return true to include the tool, false to exclude it.
type ToolFilterFunc func(toolName string) bool

// Client represents an MCP client for connecting to remote servers
type Client struct {
	baseURL      string
	httpClient   *http.Client
	auth         AuthProvider
	namespace    string         // Optional namespace for tool names (e.g., "scriptling.")
	separator    string         // Separator for namespace
	cachedTools  []MCPTool      // Cached tools with namespace already applied
	toolFilter   ToolFilterFunc // Optional filter for tools (applied to original name without namespace)
	mu           sync.RWMutex
	initialized  bool
	era          clientEra // detected during Initialize; see tryModernInitialize
	sessionID    string
	instructions string // Captured from the remote server's initialize response, if any
	// protocolVersion is the protocol revision actually in effect with this
	// server: the Legacy server's own advertised "protocolVersion" from its
	// initialize response (which may differ from what was requested, if the
	// server prefers an older revision it supports), or [MCPProtocolVersionModern]
	// once Modern era is confirmed via tryModernInitialize. Empty until
	// Initialize has completed. See [Client.ProtocolVersion].
	protocolVersion string
	transport       clientTransport // non-nil for non-HTTP transports (e.g. stdio)

	// requestHeaders carries extra headers set at construction via
	// WithClientRequestHeaders. Immutable once the constructor returns, so
	// concurrent request paths read it without a lock.
	requestHeaders map[string]string

	// extensionCapabilities holds this client's own declared extensions.<id>
	// settings (see DeclareExtension), sent as capabilities.extensions on
	// Legacy's initialize and _meta.clientCapabilities.extensions on every
	// Modern request.
	extensionCapabilities map[string]any

	// Notification reader lifecycle. Kept on its own mutex so notification
	// handling can't deadlock with c.mu (the request/cache lock).
	readerMu          sync.Mutex
	readerStarted     bool
	readerWG          sync.WaitGroup
	wantNotifications bool // set by EnableNotifications; gates reader startup
	ctx               context.Context
	cancel            context.CancelFunc

	// User callbacks invoked on inbound listChanged notifications (HTTP SSE or
	// stdio). All optional.
	onToolsChanged     func()
	onResourcesChanged func()
	onPromptsChanged   func()
	// onNotification is an internal hook list used by the federation wiring
	// (RegisterRemoteServer and friends) to propagate upstream listChanged
	// notifications downstream. It fires for every received notification,
	// after cache handling. A list, not a single slot, because the same
	// client can be registered on multiple servers (e.g. a chat-side
	// federated view and a public endpoint view of the same remote) and each
	// registration's propagation must survive the next.
	onNotification []func(method string, params any)
}

// clientTransport abstracts how a client request/response round-trip is
// performed. The default (nil) transport uses HTTP via sendRequest; stdio and
// other stream transports supply an implementation.
type clientTransport interface {
	roundTrip(ctx context.Context, req *MCPRequest, resp *MCPResponse, respHeaders *http.Header) error
	Close() error
}

// batchTransport is implemented by transports that can send several requests
// as a single wire-level batch. The stdio transport does (it is backed by
// [jsonrpc.Client.CallBatch]); the default HTTP path does not implement it, so
// callers fall back to concurrent individual round-trips.
//
// Responses are returned in the same order as reqs (this is the guarantee
// [jsonrpc.Client.CallBatch] itself makes), so implementations need not
// preserve or interpret the MCPRequest.ID field for correlation.
type batchTransport interface {
	batchRoundTrip(ctx context.Context, reqs []*MCPRequest) ([]*MCPResponse, error)
}

// Close releases resources held by the client's transport. For the default HTTP
// transport it stops the notification reader; for a stdio subprocess transport
// it shuts the child process down.
func (c *Client) Close() error {
	c.readerMu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.readerMu.Unlock()
	c.readerWG.Wait()

	if c.transport != nil {
		return c.transport.Close()
	}
	return nil
}

// ClientOption customises a Client at construction time. Applied inside
// NewClient/NewClientWithPool before the client is returned, so anything an
// option sets is safe to read lock-free afterwards.
type ClientOption func(*Client)

// WithClientRequestHeaders returns a ClientOption that adds the given headers
// to every HTTP request the client makes — the JSON-RPC POSTs and the
// notification event-stream GET alike. Headers the transport manages itself
// (Content-Type, Accept, Authorization, Mcp-Session-Id, ...) are set after
// these, so they win any collision. Useful for gateways keyed on request
// headers, e.g. a proxy forwarding an "X-MCP-Show-All: true" mode header.
func WithClientRequestHeaders(headers map[string]string) ClientOption {
	return func(c *Client) {
		if len(headers) == 0 {
			return
		}
		if c.requestHeaders == nil {
			c.requestHeaders = make(map[string]string, len(headers))
		}
		for k, v := range headers {
			c.requestHeaders[k] = v
		}
	}
}

// applyRequestHeaders sets the construction-time extra headers (see
// WithClientRequestHeaders) on an outgoing request. Call it before the
// transport-managed headers so those override on collision.
func (c *Client) applyRequestHeaders(h http.Header) {
	for k, v := range c.requestHeaders {
		h.Set(k, v)
	}
}

// NewClient creates a new MCP client using the shared HTTP pool.
// The namespace will be added to all tool names (e.g., namespace "scriptling" makes tool "search" available as "scriptling.search").
// Use an empty namespace for no namespacing.
//
// The namespace should be a simple identifier (letters, numbers, hyphens, underscores).
// Whitespace is trimmed automatically.
func NewClient(baseURL string, auth AuthProvider, namespace string, opts ...ClientOption) *Client {
	return NewClientWithPool(baseURL, auth, namespace, nil, opts...)
}

// NewClientWithPool creates a new MCP client with a custom HTTP pool.
// If httpPool is nil, the default secure pool is used.
// This is useful when you need to use a pool with custom settings (e.g., InsecureSkipVerify for internal services).
//
// Example:
//
//	// Create an insecure pool for internal services with self-signed certs
//	insecurePool := pool.NewPool(&pool.PoolConfig{InsecureSkipVerify: true})
//	client := mcp.NewClientWithPool("https://internal.service", auth, "ns", insecurePool)
func NewClientWithPool(baseURL string, auth AuthProvider, namespace string, httpPool pool.HTTPPool, opts ...ClientOption) *Client {
	// Use the global default separator
	separator := DefaultNamespaceSeparator

	// Normalize namespace: trim whitespace
	namespace = strings.TrimSpace(namespace)

	// Ensure namespace ends with separator if provided and not empty
	if namespace != "" && !strings.HasSuffix(namespace, separator) {
		namespace = namespace + separator
	}

	// Use provided pool or default
	var httpClient *http.Client
	if httpPool != nil {
		httpClient = httpPool.GetHTTPClient()
	} else {
		httpClient = pool.GetPool().GetHTTPClient()
	}

	c := &Client{
		baseURL:    baseURL,
		httpClient: httpClient,
		auth:       auth,
		namespace:  namespace,
		separator:  separator,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c
}

// DeclareExtension advertises this client's support for an MCP extension (per
// SEP-1724) to every server it talks to, under capabilities.extensions[id]
// on Legacy's initialize and _meta.clientCapabilities.extensions[id] on
// every Modern request. Call it during setup, before Initialize — a remote
// server that conditionally attaches extension-specific data (e.g. only
// linking a tool to its MCP Apps UI resource for clients that declared
// support) otherwise has no way to know this client can use it, and quietly
// falls back to a plain response instead. For example, a client rendering
// MCP Apps views declares:
//
//	client.DeclareExtension(mcp.UIAppsExtensionID, map[string]any{
//		"mimeTypes": []string{mcp.UIAppMimeType},
//	})
//
// This is the client-side counterpart of [Server.DeclareExtension].
func (c *Client) DeclareExtension(id string, settings map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.extensionCapabilities == nil {
		c.extensionCapabilities = map[string]any{}
	}
	c.extensionCapabilities[id] = settings
}

// capabilitiesMap builds the capabilities object sent on Legacy's initialize
// (capabilities) and Modern's every request (_meta.clientCapabilities):
// empty unless DeclareExtension was called, in which case it carries
// extensions.<id> for each declared extension.
//
// Deliberately unlocked: it's called from initializeLegacy and
// withModernMeta, both reachable from Initialize while it already holds
// c.mu — taking c.mu here too would deadlock (sync.RWMutex isn't
// reentrant). Safe under DeclareExtension's own documented contract (call
// it during setup, before Initialize/concurrent use): nothing mutates
// extensionCapabilities after that point, the same convention this Client
// already relies on for namespace/separator.
func (c *Client) capabilitiesMap() map[string]any {
	if len(c.extensionCapabilities) == 0 {
		return map[string]any{}
	}
	extensions := make(map[string]any, len(c.extensionCapabilities))
	for id, settings := range c.extensionCapabilities {
		extensions[id] = settings
	}
	return map[string]any{"extensions": extensions}
}

// Initialize connects to the remote server, transparently detecting whether
// it speaks the Modern (protocol revision 2026-07-28+, stateless per-request)
// or Legacy (initialize handshake) era, per the spec's backward-compatibility
// algorithm: attempt a Modern server/discover call first, and fall back to
// the Legacy initialize handshake below if that doesn't succeed. Every other
// Client method is unaffected by which era was detected — they all still
// just call sendRequest, which applies the right wire shape internally.
func (c *Client) Initialize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.initialized {
		return nil
	}

	if !c.tryModernInitialize(ctx) {
		if err := c.initializeLegacy(ctx); err != nil {
			return err
		}
	}

	// Start the notification reader — for Legacy, the GET SSE stream; for
	// Modern, subscriptions/listen (see startNotifications) — only if the
	// caller opted in via EnableNotifications. No-op for stream transports,
	// which receive notifications via their own peer handlers regardless of
	// era. Safe here under c.mu: startNotifications only touches readerMu.
	c.readerMu.Lock()
	want := c.wantNotifications
	c.readerMu.Unlock()
	if want {
		c.startNotifications(c.era)
	}
	return nil
}

// initializeLegacy performs the Legacy-era (2024-11-05..2025-11-25)
// initialize handshake. Called under c.mu, held by Initialize.
func (c *Client) initializeLegacy(ctx context.Context) error {
	req := MCPRequest{
		JSONRPC: "2.0",
		ID:      "init",
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": MCPProtocolVersionLatest,
			"capabilities":    c.capabilitiesMap(),
			"clientInfo": map[string]any{
				"name":    mcpClientName,
				"version": mcpClientVersion,
			},
		},
	}

	var resp MCPResponse
	var respHeaders http.Header
	if err := c.sendRequest(ctx, &req, &resp, &respHeaders); err != nil {
		return fmt.Errorf("initialize failed: %w", err)
	}

	if resp.Error != nil {
		return fmt.Errorf("initialize error: %s", resp.Error.Message)
	}

	result, resultOK := resp.Result.(map[string]any)

	// Check for session ID in response headers first
	if sessionID := respHeaders.Get(headerSessionID); sessionID != "" {
		c.sessionID = sessionID
	} else if resultOK {
		// Check if the server provided a session ID in the response body
		if sessionID, exists := result["sessionId"]; exists {
			if sessionStr, ok := sessionID.(string); ok {
				c.sessionID = sessionStr
			}
		}
	}

	// Capture the server's instructions, if any, so callers can inspect what a
	// remote server says about itself (e.g. for logging, admin UIs, or curating
	// content to fold into this server's own SetInstructions).
	if resultOK {
		if instructions, exists := result["instructions"]; exists {
			if instrStr, ok := instructions.(string); ok {
				c.instructions = instrStr
			}
		}
	}

	// Capture the protocol version actually in effect. Per spec the server
	// echoes back the version it will use, which may be older than what was
	// requested if that's the newest it supports — so this can legitimately
	// differ from MCPProtocolVersionLatest.
	c.protocolVersion = MCPProtocolVersionLatest
	if resultOK {
		if pv, exists := result["protocolVersion"]; exists {
			if pvStr, ok := pv.(string); ok && pvStr != "" {
				c.protocolVersion = pvStr
			}
		}
	}

	c.initialized = true
	c.era = eraLegacy
	return nil
}

// Namespace returns the namespace for this client's tools.
func (c *Client) Namespace() string {
	return c.namespace
}

// BaseURL returns the remote server's endpoint URL this client connects to.
// Unlike Namespace (which callers may leave empty, and which two different
// remote servers may share no such uniqueness guarantee exists for), this
// is a stable, unique-per-remote-server identifier — used by
// [Server.ToolSource] and [Server.ReadResourceFrom] to tell which
// federated server a tool or resource actually came from.
func (c *Client) BaseURL() string {
	return c.baseURL
}

// Instructions returns the instructions the remote server returned during
// initialize, or "" if the server set none or the client hasn't been
// initialized yet (Initialize is called automatically by most Client methods).
func (c *Client) Instructions() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.instructions
}

// ProtocolVersion returns the protocol revision actually in effect with this
// server (e.g. "2025-06-18" for a Legacy server, or [MCPProtocolVersionModern]
// once Modern era is confirmed), or "" if the client hasn't been initialized
// yet (Initialize is called automatically by most Client methods).
func (c *Client) ProtocolVersion() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.protocolVersion
}

// WithToolFilter sets a filter function for this client.
// The filter receives the original tool name (without namespace prefix).
// When set, ListTools will only return tools where filter returns true,
// and CallTool will reject calls to filtered-out tools.
// Pass nil to clear the filter. Returns the client for chaining.
// Note: Setting a filter clears the tool cache to ensure consistency.
func (c *Client) WithToolFilter(filter ToolFilterFunc) *Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.toolFilter = filter
	c.cachedTools = nil // Clear cache when filter changes
	return c
}

// GetToolFilter returns the current tool filter, or nil if none is set.
func (c *Client) GetToolFilter() ToolFilterFunc {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.toolFilter
}

// ensureInitialized performs the initialize handshake if it hasn't happened
// yet. The check is under c.mu.RLock: the old bare `!c.initialized` reads
// raced with Initialize's write under c.mu. Two concurrent first calls both
// proceed to Initialize, whose own lock makes the second a no-op.
func (c *Client) ensureInitialized(ctx context.Context) error {
	c.mu.RLock()
	initialized := c.initialized
	c.mu.RUnlock()
	if initialized {
		return nil
	}
	return c.Initialize(ctx)
}

// ListTools retrieves tools from the remote server
func (c *Client) ListTools(ctx context.Context) ([]MCPTool, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, err
	}

	// Check cache first
	c.mu.RLock()
	if c.cachedTools != nil {
		result := make([]MCPTool, len(c.cachedTools))
		copy(result, c.cachedTools)
		c.mu.RUnlock()
		return result, nil
	}
	c.mu.RUnlock()

	req := MCPRequest{
		JSONRPC: "2.0",
		ID:      "list-tools",
		Method:  "tools/list",
	}

	var resp MCPResponse
	if err := c.sendRequest(ctx, &req, &resp, nil); err != nil {
		return nil, fmt.Errorf("list tools failed: %w", err)
	}

	if resp.Error != nil {
		return nil, &ToolError{Code: resp.Error.Code, Message: resp.Error.Message, Data: resp.Error.Data}
	}

	// Parse the result using type assertion where possible
	tools, err := parseToolsResult(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("failed to parse tools response: %w", err)
	}

	// Add namespace to tool names, apply filter, and cache the results
	c.mu.Lock()
	filter := c.toolFilter
	c.mu.Unlock()

	var namespacedTools []MCPTool
	for _, tool := range tools {
		// Apply filter if set (filter receives original name without namespace)
		if filter != nil && !filter(tool.Name) {
			continue
		}
		namespacedTools = append(namespacedTools, MCPTool{
			Name:         c.namespace + tool.Name,
			Description:  tool.Description,
			InputSchema:  tool.InputSchema,
			OutputSchema: tool.OutputSchema,
			Meta:         tool.Meta,
			Icons:        tool.Icons,
		})
	}

	c.mu.Lock()
	c.cachedTools = namespacedTools
	c.mu.Unlock()

	return namespacedTools, nil
}

// RefreshToolCache explicitly refreshes the tool cache
func (c *Client) RefreshToolCache(ctx context.Context) error {
	c.mu.Lock()
	c.cachedTools = nil // Clear cache
	c.mu.Unlock()

	_, err := c.ListTools(ctx) // This will fetch fresh data
	return err
}

// CallTool executes a tool on the remote server.
// If the client has a namespace, the tool name should include it (e.g., "scriptling.search").
// The namespace will be stripped before calling the underlying tool.
// If a tool filter is set and the tool is filtered out, returns ErrToolFiltered.
// decodeResult converts a JSON-RPC result — which the transport already
// decoded as a generic any — into a typed value via a marshal/unmarshal
// round-trip. what names the payload in error messages ("tool response",
// "resources response", ...).
func decodeResult(result any, target any, what string) error {
	resultBytes, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal result: %w", err)
	}
	if err := json.Unmarshal(resultBytes, target); err != nil {
		return fmt.Errorf("failed to parse %s: %w", what, err)
	}
	return nil
}

func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*ToolResponse, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, err
	}

	// Strip namespace if present
	toolName := name
	if c.namespace != "" && strings.HasPrefix(name, c.namespace) {
		toolName = name[len(c.namespace):]
	}

	// Check tool filter if set
	c.mu.RLock()
	filter := c.toolFilter
	c.mu.RUnlock()
	if filter != nil && !filter(toolName) {
		return nil, ErrToolFiltered
	}

	req := MCPRequest{
		JSONRPC: "2.0",
		ID:      fmt.Sprintf("call-%s", toolName),
		Method:  "tools/call",
		Params: map[string]any{
			"name":      toolName,
			"arguments": args,
		},
	}

	var resp MCPResponse
	if err := c.sendRequest(ctx, &req, &resp, nil); err != nil {
		return nil, fmt.Errorf("call tool failed: %w", err)
	}

	if resp.Error != nil {
		return nil, &ToolError{
			Code:    resp.Error.Code,
			Message: resp.Error.Message,
			Data:    resp.Error.Data,
		}
	}

	var result ToolResult
	if err := decodeResult(resp.Result, &result, "tool response"); err != nil {
		return nil, err
	}

	return &ToolResponse{
		Content:           result.Content,
		StructuredContent: result.StructuredContent,
	}, nil
}

// ListResources retrieves the list of resources from the remote server via
// resources/list. Unlike tools, resources are not cached: each call performs a
// fresh request, since resource sets can change between calls.
func (c *Client) ListResources(ctx context.Context) ([]MCPResource, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, err
	}

	req := MCPRequest{
		JSONRPC: "2.0",
		ID:      "list-resources",
		Method:  "resources/list",
	}

	var resp MCPResponse
	if err := c.sendRequest(ctx, &req, &resp, nil); err != nil {
		return nil, fmt.Errorf("list resources failed: %w", err)
	}

	if resp.Error != nil {
		return nil, &ToolError{Code: resp.Error.Code, Message: resp.Error.Message, Data: resp.Error.Data}
	}

	var parsed struct {
		Resources []MCPResource `json:"resources"`
	}
	if err := decodeResult(resp.Result, &parsed, "resources response"); err != nil {
		return nil, err
	}
	return parsed.Resources, nil
}

// ReadResource reads a resource by URI from the remote server via
// resources/read.
func (c *Client) ReadResource(ctx context.Context, uri string) (*ResourceResponse, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, err
	}

	req := MCPRequest{
		JSONRPC: "2.0",
		ID:      fmt.Sprintf("read-resource-%s", uri),
		Method:  "resources/read",
		Params: map[string]any{
			"uri": uri,
		},
	}

	var resp MCPResponse
	if err := c.sendRequest(ctx, &req, &resp, nil); err != nil {
		return nil, fmt.Errorf("read resource failed: %w", err)
	}

	if resp.Error != nil {
		return nil, &ToolError{
			Code:    resp.Error.Code,
			Message: resp.Error.Message,
			Data:    resp.Error.Data,
		}
	}

	var result ResourceResponse
	if err := decodeResult(resp.Result, &result, "resource response"); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListResourceTemplates retrieves the resource templates exposed via
// resources/templates/list from the remote server.
func (c *Client) ListResourceTemplates(ctx context.Context) ([]MCPResourceTemplate, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, err
	}

	req := MCPRequest{
		JSONRPC: "2.0",
		ID:      "list-resource-templates",
		Method:  "resources/templates/list",
	}

	var resp MCPResponse
	if err := c.sendRequest(ctx, &req, &resp, nil); err != nil {
		return nil, fmt.Errorf("list resource templates failed: %w", err)
	}
	if resp.Error != nil {
		return nil, &ToolError{Code: resp.Error.Code, Message: resp.Error.Message, Data: resp.Error.Data}
	}

	var parsed struct {
		ResourceTemplates []MCPResourceTemplate `json:"resourceTemplates"`
	}
	if err := decodeResult(resp.Result, &parsed, "resource templates response"); err != nil {
		return nil, err
	}
	return parsed.ResourceTemplates, nil
}

// ListPrompts retrieves the list of prompts from the remote server via
// prompts/list. Prompts are not cached: each call performs a fresh request.
func (c *Client) ListPrompts(ctx context.Context) ([]MCPPrompt, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, err
	}

	req := MCPRequest{
		JSONRPC: "2.0",
		ID:      "list-prompts",
		Method:  "prompts/list",
	}

	var resp MCPResponse
	if err := c.sendRequest(ctx, &req, &resp, nil); err != nil {
		return nil, fmt.Errorf("list prompts failed: %w", err)
	}

	if resp.Error != nil {
		return nil, &ToolError{Code: resp.Error.Code, Message: resp.Error.Message, Data: resp.Error.Data}
	}

	var parsed struct {
		Prompts []MCPPrompt `json:"prompts"`
	}
	if err := decodeResult(resp.Result, &parsed, "prompts response"); err != nil {
		return nil, err
	}
	return parsed.Prompts, nil
}

// GetPrompt renders a prompt by name with the given string arguments via
// prompts/get.
func (c *Client) GetPrompt(ctx context.Context, name string, args map[string]string) (*PromptResponse, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, err
	}

	req := MCPRequest{
		JSONRPC: "2.0",
		ID:      fmt.Sprintf("get-prompt-%s", name),
		Method:  "prompts/get",
		Params: map[string]any{
			"name":      name,
			"arguments": args,
		},
	}

	var resp MCPResponse
	if err := c.sendRequest(ctx, &req, &resp, nil); err != nil {
		return nil, fmt.Errorf("get prompt failed: %w", err)
	}

	if resp.Error != nil {
		return nil, &ToolError{
			Code:    resp.Error.Code,
			Message: resp.Error.Message,
			Data:    resp.Error.Data,
		}
	}

	var result PromptResponse
	if err := decodeResult(resp.Result, &result, "prompt response"); err != nil {
		return nil, err
	}
	return &result, nil
}

// sendRequest sends a request to the MCP server. When a non-HTTP transport is
// configured (e.g. stdio) it is used; otherwise the request is sent over HTTP.
func (c *Client) sendRequest(ctx context.Context, req *MCPRequest, resp *MCPResponse, respHeaders *http.Header) error {
	// Modern era (detected by Initialize via tryModernInitialize): every
	// request — not just Initialize's own probe — needs the same per-request
	// _meta and, on HTTP, the Mcp-Method/Mcp-Name routing headers. This is the
	// one place that applies transparently to every Client method; a request
	// on a Legacy or not-yet-initialized Client takes the unchanged path below.
	if c.era == eraModern {
		modernReq := c.withModernMeta(req)
		if c.transport != nil {
			return c.transport.roundTrip(ctx, modernReq, resp, respHeaders)
		}
		return c.sendModernHTTPRequest(ctx, modernReq, resp, respHeaders)
	}

	if c.transport != nil {
		return c.transport.roundTrip(ctx, req, resp, respHeaders)
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	c.applyRequestHeaders(httpReq.Header)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	httpReq.Header.Set("User-Agent", fmt.Sprintf("%s/%s", mcpClientName, mcpClientVersion))

	// Mirror the resource fan-out hop counter (see resources.go) when the
	// caller's ctx carries one — i.e. when this request is itself a
	// federated server's fan-out forward, so the receiving server can count
	// it toward maxResourceFanoutHops.
	if hop := resourceFanoutHopFrom(ctx); hop > 0 {
		httpReq.Header.Set(headerResourceFanoutHop, strconv.Itoa(hop))
	}

	if c.sessionID != "" && req.Method != "initialize" {
		httpReq.Header.Set(headerSessionID, c.sessionID)
	}

	if err := c.applyAuthHeader(httpReq.Header); err != nil {
		return fmt.Errorf("failed to get auth header: %w", err)
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer httpResp.Body.Close()

	// Capture response headers if requested
	if respHeaders != nil {
		*respHeaders = httpResp.Header
	}

	if httpResp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned status %d", httpResp.StatusCode)
	}

	// Read the entire response body first
	bodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Check if it's an event stream
	if strings.HasPrefix(httpResp.Header.Get("Content-Type"), "text/event-stream") {
		// Handle Server-Sent Events format
		return c.parseEventStream(bodyBytes, resp)
	}

	// Try to decode as JSON
	if err := json.Unmarshal(bodyBytes, resp); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	return nil
}

func (c *Client) parseEventStream(data []byte, resp *MCPResponse) error {
	lines := bytes.Split(data, []byte("\n"))
	var jsonData []byte

	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if bytes.HasPrefix(line, []byte("data:")) {
			// Tolerate optional space after colon and skip empty data lines
			payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			if len(payload) == 0 {
				continue
			}
			jsonData = payload
			break
		}
	}

	if len(jsonData) == 0 {
		return fmt.Errorf("no JSON data found in event stream")
	}

	return json.Unmarshal(jsonData, resp)
}

// ToolSearch performs a tool search using the tool_search MCP tool.
// This is useful when the server has many tools registered via a discovery registry.
// The query searches tool names, descriptions, and keywords.
func (c *Client) ToolSearch(ctx context.Context, query string, maxResults int) ([]map[string]any, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, err
	}

	args := map[string]any{
		"query": query,
	}
	if maxResults > 0 {
		args["max_results"] = maxResults
	}

	resp, err := c.CallTool(ctx, ToolSearchName, args)
	if err != nil {
		return nil, fmt.Errorf("tool_search failed: %w", err)
	}

	// Parse the response - tool_search returns JSON with search results
	return parseToolSearchResponse(resp)
}

// Args is a map of tool arguments. It can be used directly as a map[string]any
// or built fluently via the Arg method.
//
//	// Direct map
//	client.CallTool(ctx, "tool", map[string]any{"city": "London"})
//
//	// Fluent builder
//	client.CallTool(ctx, "tool", mcp.Args{}.Arg("city", "London").Arg("units", "metric"))
type Args map[string]any

// Arg adds a key/value pair and returns the Args for chaining.
func (a Args) Arg(key string, value any) Args {
	a[key] = value
	return a
}

// ToolCall represents a single tool invocation for use with parallel calls.
type ToolCall struct {
	Name      string
	Arguments map[string]any
}

// ParallelToolResult holds the result of a single tool call from a parallel execution.
type ParallelToolResult struct {
	Name     string
	Response *ToolResponse
	Err      error
}

// CallToolsParallel executes multiple tools concurrently and returns results in
// the same order as the input. Over a transport with native batch support
// (currently the stdio transport, backed by jsonrpc.Client.CallBatch), all
// calls are sent as a single wire-level batch instead of one round-trip each;
// otherwise they run as concurrent individual calls.
func (c *Client) CallToolsParallel(ctx context.Context, calls []ToolCall) []ParallelToolResult {
	return c.callToolsParallel(ctx, calls, false)
}

// ExecuteDiscoveredToolsParallel executes multiple discovered tools concurrently
// and returns results in the same order as the input. It has the same
// batch-transport behaviour as CallToolsParallel.
func (c *Client) ExecuteDiscoveredToolsParallel(ctx context.Context, calls []ToolCall) []ParallelToolResult {
	return c.callToolsParallel(ctx, calls, true)
}

// callToolsParallel is the shared implementation behind CallToolsParallel and
// ExecuteDiscoveredToolsParallel. When discovered is true, each call is wrapped
// as an execute_tool invocation (matching ExecuteDiscoveredTool); otherwise it
// is a direct tools/call.
func (c *Client) callToolsParallel(ctx context.Context, calls []ToolCall, discovered bool) []ParallelToolResult {
	results := make([]ParallelToolResult, len(calls))
	if len(calls) == 0 {
		return results
	}

	if err := c.ensureInitialized(ctx); err != nil {
		for i, call := range calls {
			results[i] = ParallelToolResult{Name: call.Name, Err: err}
		}
		return results
	}

	if bt, ok := c.transport.(batchTransport); ok {
		return c.callToolsBatch(ctx, bt, calls, discovered)
	}

	var wg sync.WaitGroup
	for i, call := range calls {
		wg.Add(1)
		go func(i int, call ToolCall) {
			defer wg.Done()
			var resp *ToolResponse
			var err error
			if discovered {
				resp, err = c.ExecuteDiscoveredTool(ctx, call.Name, call.Arguments)
			} else {
				resp, err = c.CallTool(ctx, call.Name, call.Arguments)
			}
			results[i] = ParallelToolResult{Name: call.Name, Response: resp, Err: err}
		}(i, call)
	}
	wg.Wait()
	return results
}

// callToolsBatch sends every call as one wire-level batch via bt, applying the
// same namespace stripping and tool-filter checks as CallTool, then decodes
// each response back into a ParallelToolResult in call order.
func (c *Client) callToolsBatch(ctx context.Context, bt batchTransport, calls []ToolCall, discovered bool) []ParallelToolResult {
	results := make([]ParallelToolResult, len(calls))
	reqs := make([]*MCPRequest, len(calls))

	c.mu.RLock()
	filter := c.toolFilter
	c.mu.RUnlock()

	for i, call := range calls {
		toolName := call.Name
		if c.namespace != "" && strings.HasPrefix(toolName, c.namespace) {
			toolName = toolName[len(c.namespace):]
		}
		if filter != nil && !filter(toolName) {
			results[i] = ParallelToolResult{Name: call.Name, Err: ErrToolFiltered}
			continue
		}

		method := "tools/call"
		params := map[string]any{"name": toolName, "arguments": call.Arguments}
		if discovered {
			params = map[string]any{
				"name":      ExecuteToolName,
				"arguments": map[string]any{"name": toolName, "parameters": call.Arguments},
			}
		}
		reqs[i] = &MCPRequest{
			JSONRPC: "2.0",
			ID:      fmt.Sprintf("batch-%d", i),
			Method:  method,
			Params:  params,
		}
	}

	// Only the calls that passed the filter check need a request on the wire;
	// build the subset while remembering which result index each belongs to.
	// Batch calls go straight to the transport's batchRoundTrip, bypassing
	// sendRequest's own era handling, so a Modern-era client must apply the
	// same per-request _meta here itself.
	c.mu.RLock()
	era := c.era
	c.mu.RUnlock()

	var wireReqs []*MCPRequest
	var wireIdx []int
	for i, req := range reqs {
		if req != nil {
			if era == eraModern {
				req = c.withModernMeta(req)
			}
			wireReqs = append(wireReqs, req)
			wireIdx = append(wireIdx, i)
		}
	}
	if len(wireReqs) == 0 {
		return results
	}

	resps, err := bt.batchRoundTrip(ctx, wireReqs)
	if err != nil {
		for _, i := range wireIdx {
			results[i] = ParallelToolResult{Name: calls[i].Name, Err: fmt.Errorf("call tool failed: %w", err)}
		}
		return results
	}
	if len(resps) != len(wireIdx) {
		for _, i := range wireIdx {
			results[i] = ParallelToolResult{Name: calls[i].Name, Err: fmt.Errorf("call tool failed: batch returned %d responses, want %d", len(resps), len(wireIdx))}
		}
		return results
	}

	for pos, i := range wireIdx {
		resp := resps[pos]
		if resp.Error != nil {
			results[i] = ParallelToolResult{Name: calls[i].Name, Err: &ToolError{Code: resp.Error.Code, Message: resp.Error.Message, Data: resp.Error.Data}}
			continue
		}
		results[i] = ParallelToolResult{Name: calls[i].Name, Response: decodeToolResult(resp)}
	}
	return results
}

// decodeToolResult decodes a successful tools/call (or execute_tool) response
// result into a ToolResponse. Callers must check resp.Error first. Returns
// nil on a decode failure (its single-value contract predates error return;
// batch callers report the nil as a missing response).
func decodeToolResult(resp *MCPResponse) *ToolResponse {
	var result ToolResult
	if err := decodeResult(resp.Result, &result, "tool response"); err != nil {
		return nil
	}
	return &ToolResponse{
		Content:           result.Content,
		StructuredContent: result.StructuredContent,
	}
}

// ExecuteDiscoveredTool executes a tool by name using the execute_tool MCP tool.
// This is the always-safe way to call tools returned by ToolSearch.
// Tools may also be callable directly via CallTool when they were exposed in tools/list.
func (c *Client) ExecuteDiscoveredTool(ctx context.Context, name string, arguments map[string]any) (*ToolResponse, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, err
	}

	args := map[string]any{
		"name":       name,
		"parameters": arguments,
	}

	return c.CallTool(ctx, ExecuteToolName, args)
}

// parseToolSearchResponse parses the response from tool_search MCP tool.
// The tool_search tool returns JSON containing search results.
func parseToolSearchResponse(resp *ToolResponse) ([]map[string]any, error) {
	if resp == nil {
		return nil, fmt.Errorf("nil response")
	}

	var jsonText string

	// Try structured content first
	if resp.StructuredContent != nil {
		if bytes, err := json.Marshal(resp.StructuredContent); err == nil {
			jsonText = string(bytes)
		}
	}

	// Fall back to text content
	if jsonText == "" && len(resp.Content) > 0 {
		for _, content := range resp.Content {
			if content.Type == "text" && content.Text != "" {
				jsonText = content.Text
				break
			}
		}
	}

	if jsonText == "" {
		return []map[string]any{}, nil
	}

	var results []map[string]any
	if err := json.Unmarshal([]byte(jsonText), &results); err != nil {
		return nil, fmt.Errorf("failed to parse tool search response: %w", err)
	}
	for _, result := range results {
		if _, ok := result["inputSchema"]; !ok {
			if schema, ok := result["input_schema"]; ok {
				result["inputSchema"] = schema
			}
		}
	}

	return results, nil
}

// parseToolsResult parses the tools list result using type assertions where possible
// to avoid double JSON serialization. Falls back to marshal/unmarshal if needed.
func parseToolsResult(result any) ([]MCPTool, error) {
	// Try direct type assertion first
	if resultMap, ok := result.(map[string]any); ok {
		if toolsRaw, ok := resultMap["tools"]; ok {
			if toolsSlice, ok := toolsRaw.([]any); ok {
				tools := make([]MCPTool, 0, len(toolsSlice))
				for _, toolRaw := range toolsSlice {
					if toolMap, ok := toolRaw.(map[string]any); ok {
						tool := MCPTool{}
						if name, ok := toolMap["name"].(string); ok {
							tool.Name = name
						}
						if desc, ok := toolMap["description"].(string); ok {
							tool.Description = desc
						}
						if schema, ok := toolMap["inputSchema"]; ok {
							tool.InputSchema = schema
						}
						if outputSchema, ok := toolMap["outputSchema"]; ok {
							tool.OutputSchema = outputSchema
						}
						if meta, ok := toolMap["_meta"].(map[string]any); ok {
							tool.Meta = meta
						}
						if icons, ok := toolMap["icons"]; ok {
							tool.Icons = iconsFromRaw(icons)
						}
						tools = append(tools, tool)
					}
				}
				return tools, nil
			}
		}
	}

	// Fallback: use JSON marshal/unmarshal
	var parsed struct {
		Tools []MCPTool `json:"tools"`
	}
	resultBytes, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(resultBytes, &parsed); err != nil {
		return nil, err
	}
	return parsed.Tools, nil
}
