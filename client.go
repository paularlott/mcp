package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	baseURL     string
	httpClient  *http.Client
	auth        AuthProvider
	namespace   string         // Optional namespace for tool names (e.g., "scriptling.")
	separator   string         // Separator for namespace
	cachedTools []MCPTool      // Cached tools with namespace already applied
	toolFilter  ToolFilterFunc // Optional filter for tools (applied to original name without namespace)
	mu          sync.RWMutex
	initialized bool
	sessionID   string
	transport   clientTransport // non-nil for non-HTTP transports (e.g. stdio)
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
// transport it is a no-op; for a stdio subprocess transport it shuts the child
// process down.
func (c *Client) Close() error {
	if c.transport != nil {
		return c.transport.Close()
	}
	return nil
}

// NewClient creates a new MCP client using the shared HTTP pool.
// The namespace will be added to all tool names (e.g., namespace "scriptling" makes tool "search" available as "scriptling.search").
// Use an empty namespace for no namespacing.
//
// The namespace should be a simple identifier (letters, numbers, hyphens, underscores).
// Whitespace is trimmed automatically.
func NewClient(baseURL string, auth AuthProvider, namespace string) *Client {
	return NewClientWithPool(baseURL, auth, namespace, nil)
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
func NewClientWithPool(baseURL string, auth AuthProvider, namespace string, httpPool pool.HTTPPool) *Client {
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

	return &Client{
		baseURL:    baseURL,
		httpClient: httpClient,
		auth:       auth,
		namespace:  namespace,
		separator:  separator,
	}
}

// Initialize performs the MCP handshake with the remote server
func (c *Client) Initialize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.initialized {
		return nil
	}

	req := MCPRequest{
		JSONRPC: "2.0",
		ID:      "init",
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": MCPProtocolVersionLatest,
			"capabilities":    map[string]any{},
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

	// Check for session ID in response headers first
	if sessionID := respHeaders.Get("Mcp-Session-Id"); sessionID != "" {
		c.sessionID = sessionID
	} else if result, ok := resp.Result.(map[string]any); ok {
		// Check if the server provided a session ID in the response body
		if sessionID, exists := result["sessionId"]; exists {
			if sessionStr, ok := sessionID.(string); ok {
				c.sessionID = sessionStr
			}
		}
	}

	c.initialized = true
	return nil
}

// Namespace returns the namespace for this client's tools.
func (c *Client) Namespace() string {
	return c.namespace
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

// ListTools retrieves tools from the remote server
func (c *Client) ListTools(ctx context.Context) ([]MCPTool, error) {
	if !c.initialized {
		if err := c.Initialize(ctx); err != nil {
			return nil, err
		}
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
		return nil, fmt.Errorf("list tools error: code %d", resp.Error.Code)
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
			Name:        c.namespace + tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
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
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*ToolResponse, error) {
	if !c.initialized {
		if err := c.Initialize(ctx); err != nil {
			return nil, err
		}
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
	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		return nil, fmt.Errorf("failed to parse tool response: %w", err)
	}

	return &ToolResponse{
		Content:           result.Content,
		StructuredContent: result.StructuredContent,
	}, nil
}

// sendRequest sends a request to the MCP server. When a non-HTTP transport is
// configured (e.g. stdio) it is used; otherwise the request is sent over HTTP.
func (c *Client) sendRequest(ctx context.Context, req *MCPRequest, resp *MCPResponse, respHeaders *http.Header) error {
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

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json,  text/event-stream")
	httpReq.Header.Set("User-Agent", fmt.Sprintf("%s/%s", mcpClientName, mcpClientVersion))

	if c.sessionID != "" && req.Method != "initialize" {
		httpReq.Header.Set("Mcp-Session-Id", c.sessionID)
	}

	if c.auth != nil {
		authHeader, err := c.auth.GetAuthHeader()
		if err != nil {
			return fmt.Errorf("failed to get auth header: %w", err)
		}
		httpReq.Header.Set("Authorization", authHeader)
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
	if !c.initialized {
		if err := c.Initialize(ctx); err != nil {
			return nil, err
		}
	}

	args := map[string]any{
		"query": query,
	}
	if maxResults > 0 {
		args["max_results"] = maxResults
	}

	resp, err := c.CallTool(ctx, "tool_search", args)
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

	if !c.initialized {
		if err := c.Initialize(ctx); err != nil {
			for i, call := range calls {
				results[i] = ParallelToolResult{Name: call.Name, Err: err}
			}
			return results
		}
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
				"name":      "execute_tool",
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
	var wireReqs []*MCPRequest
	var wireIdx []int
	for i, req := range reqs {
		if req != nil {
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
// result into a ToolResponse. Callers must check resp.Error first.
func decodeToolResult(resp *MCPResponse) *ToolResponse {
	var result ToolResult
	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		return nil
	}
	if err := json.Unmarshal(resultBytes, &result); err != nil {
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
	if !c.initialized {
		if err := c.Initialize(ctx); err != nil {
			return nil, err
		}
	}

	args := map[string]any{
		"name":       name,
		"parameters": arguments,
	}

	return c.CallTool(ctx, "execute_tool", args)
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
