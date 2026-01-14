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

// DefaultNamespaceSeparator is the default separator used for namespacing tool names
var DefaultNamespaceSeparator = "/"

// Client represents an MCP client for connecting to remote servers
type Client struct {
	baseURL     string
	httpClient  *http.Client
	auth        AuthProvider
	namespace   string      // Optional namespace for tool names (e.g., "scriptling/")
	separator   string      // Separator for namespace
	cachedTools []MCPTool   // Cached tools with namespace already applied
	mu          sync.RWMutex
	initialized bool
	sessionID   string
}

// AuthProvider interface for different authentication methods
type AuthProvider interface {
	GetAuthHeader() (string, error)
	Refresh() error
}

// NewClient creates a new MCP client using the shared HTTP pool.
// The namespace will be added to all tool names (e.g., namespace "scriptling" makes tool "search" available as "scriptling/search").
// Use an empty namespace for no namespacing.
//
// The namespace should be a simple identifier (letters, numbers, hyphens, underscores).
// Whitespace is trimmed automatically.
func NewClient(baseURL string, auth AuthProvider, namespace string) *Client {
	// Use the global default separator
	separator := DefaultNamespaceSeparator

	// Normalize namespace: trim whitespace
	namespace = strings.TrimSpace(namespace)

	// Ensure namespace ends with separator if provided and not empty
	if namespace != "" && !strings.HasSuffix(namespace, separator) {
		namespace = namespace + separator
	}
	return &Client{
		baseURL:    baseURL,
		httpClient: pool.GetPool().GetHTTPClient(),
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
		Params: map[string]interface{}{
			"protocolVersion": MCPProtocolVersionLatest,
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
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
	} else if result, ok := resp.Result.(map[string]interface{}); ok {
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

	// Add namespace to tool names and cache the results
	namespacedTools := make([]MCPTool, len(tools))
	for i, tool := range tools {
		namespacedTools[i] = MCPTool{
			Name:        c.namespace + tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		}
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
// If the client has a namespace, the tool name should include it (e.g., "scriptling/search").
// The namespace will be stripped before calling the underlying tool.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]interface{}) (*ToolResponse, error) {
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

	req := MCPRequest{
		JSONRPC: "2.0",
		ID:      fmt.Sprintf("call-%s", toolName),
		Method:  "tools/call",
		Params: map[string]interface{}{
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

// sendRequest sends an HTTP request to the MCP server
func (c *Client) sendRequest(ctx context.Context, req *MCPRequest, resp *MCPResponse, respHeaders *http.Header) error {
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
func (c *Client) ToolSearch(ctx context.Context, query string, maxResults int) ([]map[string]interface{}, error) {
	if !c.initialized {
		if err := c.Initialize(ctx); err != nil {
			return nil, err
		}
	}

	args := map[string]interface{}{
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

// ExecuteDiscoveredTool executes a tool by name using the execute_tool MCP tool.
// This is the only way to call tools that were discovered via ToolSearch.
// Discovered tools cannot be called directly via CallTool.
func (c *Client) ExecuteDiscoveredTool(ctx context.Context, name string, arguments map[string]interface{}) (*ToolResponse, error) {
	if !c.initialized {
		if err := c.Initialize(ctx); err != nil {
			return nil, err
		}
	}

	args := map[string]interface{}{
		"name":      name,
		"arguments": arguments,
	}

	return c.CallTool(ctx, "execute_tool", args)
}

// parseToolSearchResponse parses the response from tool_search MCP tool.
// The tool_search tool returns JSON containing search results.
func parseToolSearchResponse(resp *ToolResponse) ([]map[string]interface{}, error) {
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
		return []map[string]interface{}{}, nil
	}

	var results []map[string]interface{}
	if err := json.Unmarshal([]byte(jsonText), &results); err != nil {
		return nil, fmt.Errorf("failed to parse tool search response: %w", err)
	}

	return results, nil
}

// parseToolsResult parses the tools list result using type assertions where possible
// to avoid double JSON serialization. Falls back to marshal/unmarshal if needed.
func parseToolsResult(result interface{}) ([]MCPTool, error) {
	// Try direct type assertion first
	if resultMap, ok := result.(map[string]interface{}); ok {
		if toolsRaw, ok := resultMap["tools"]; ok {
			if toolsSlice, ok := toolsRaw.([]interface{}); ok {
				tools := make([]MCPTool, 0, len(toolsSlice))
				for _, toolRaw := range toolsSlice {
					if toolMap, ok := toolRaw.(map[string]interface{}); ok {
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
