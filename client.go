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
	"time"
)

const (
	mcpClientName    = "mcp-client"
	mcpClientVersion = "1.0.0"
)

// Client represents an MCP client for connecting to remote servers
type Client struct {
	baseURL     string
	httpClient  *http.Client
	auth        AuthProvider
	cachedTools []MCPTool
	mu          sync.RWMutex
	initialized bool
	sessionID   string
}

// AuthProvider interface for different authentication methods
type AuthProvider interface {
	GetAuthHeader() (string, error)
	Refresh() error
}

// NewClient creates a new MCP client
func NewClient(baseURL string, auth AuthProvider) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		auth:       auth,
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

	var result struct {
		Tools []MCPTool `json:"tools"`
	}
	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		return nil, fmt.Errorf("failed to parse tools response: %w", err)
	}

	// Cache the results
	c.mu.Lock()
	c.cachedTools = make([]MCPTool, len(result.Tools))
	copy(c.cachedTools, result.Tools)
	c.mu.Unlock()

	return result.Tools, nil
}

// RefreshToolCache explicitly refreshes the tool cache
func (c *Client) RefreshToolCache(ctx context.Context) error {
	c.mu.Lock()
	c.cachedTools = nil // Clear cache
	c.mu.Unlock()

	_, err := c.ListTools(ctx) // This will fetch fresh data
	return err
}

// CallTool executes a tool on the remote server
func (c *Client) CallTool(ctx context.Context, name string, args map[string]interface{}) (*ToolResponse, error) {
	if !c.initialized {
		if err := c.Initialize(ctx); err != nil {
			return nil, err
		}
	}

	req := MCPRequest{
		JSONRPC: "2.0",
		ID:      fmt.Sprintf("call-%s", name),
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name":      name,
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
