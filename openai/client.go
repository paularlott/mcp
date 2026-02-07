package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/paularlott/mcp"
	"github.com/paularlott/mcp/pool"
)

const MAX_TOOL_CALL_ITERATIONS = 20

// MCPServer interface for MCP server operations (local server)
type MCPServer interface {
	ListTools() []mcp.MCPTool
	ListToolsWithContext(ctx context.Context) []mcp.MCPTool
	CallTool(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error)
}

// MCPServerFuncs allows creating a simple MCPServer from functions
type MCPServerFuncs struct {
	ListToolsFunc func() []mcp.MCPTool
	CallToolFunc  func(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error)
}

func (m *MCPServerFuncs) ListTools() []mcp.MCPTool {
	if m.ListToolsFunc != nil {
		return m.ListToolsFunc()
	}
	return nil
}

func (m *MCPServerFuncs) ListToolsWithContext(ctx context.Context) []mcp.MCPTool {
	return m.ListTools()
}

func (m *MCPServerFuncs) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
	if m.CallToolFunc != nil {
		return m.CallToolFunc(ctx, name, args)
	}
	return nil, fmt.Errorf("tool not found")
}

// Client represents an OpenAI API client using the shared HTTP pool
type Client struct {
	baseURL       string
	apiKey        string
	localServer   MCPServer     // Local MCP server (no namespace)
	remoteServers []*mcp.Client // Remote MCP servers (each has their own namespace)
	customTools   []Tool        // Custom tools (not executed by client)
	customToolsMu sync.RWMutex
	extraHeaders  http.Header   // Custom headers added to all requests
	httpPool      pool.HTTPPool // Optional custom HTTP pool
}

// RemoteServerConfig holds configuration for a remote MCP server
type RemoteServerConfig struct {
	BaseURL   string
	Auth      mcp.AuthProvider
	Namespace string
	HTTPPool  pool.HTTPPool // Optional custom HTTP pool for this remote server
}

// Config holds configuration for the OpenAI client
type Config struct {
	APIKey              string
	BaseURL             string
	LocalServer         MCPServer            // Local MCP server (no namespace)
	RemoteServerConfigs []RemoteServerConfig // Remote MCP server configs
	ExtraHeaders        http.Header          // Custom headers added to all requests
	HTTPPool            pool.HTTPPool        // Optional custom HTTP pool (nil = use default secure pool)
}

// New creates a new OpenAI client using the shared HTTP pool
func New(config Config) (*Client, error) {
	if config.BaseURL == "" {
		config.BaseURL = "https://api.openai.com/v1"
	}

	// Ensure BaseURL has a trailing slash for proper URL resolution
	if !strings.HasSuffix(config.BaseURL, "/") {
		config.BaseURL = config.BaseURL + "/"
	}

	// Initialize remote servers slice if nil
	if config.RemoteServerConfigs == nil {
		config.RemoteServerConfigs = []RemoteServerConfig{}
	}

	// Create remote clients (separator defaults to "/" in mcp.NewClient)
	remoteServers := make([]*mcp.Client, len(config.RemoteServerConfigs))
	for i, rsc := range config.RemoteServerConfigs {
		if rsc.HTTPPool != nil {
			remoteServers[i] = mcp.NewClientWithPool(rsc.BaseURL, rsc.Auth, rsc.Namespace, rsc.HTTPPool)
		} else {
			remoteServers[i] = mcp.NewClient(rsc.BaseURL, rsc.Auth, rsc.Namespace)
		}
	}

	return &Client{
		baseURL:       config.BaseURL,
		apiKey:        config.APIKey,
		localServer:   config.LocalServer,
		remoteServers: remoteServers,
		extraHeaders:  config.ExtraHeaders,
		httpPool:      config.HTTPPool, // Store the pool (nil = use default)
	}, nil
}

// SetCustomTools sets custom tools that will be sent to the AI but not executed by the client.
// These tools are returned to the caller for manual execution.
func (c *Client) SetCustomTools(tools []Tool) {
	c.customToolsMu.Lock()
	defer c.customToolsMu.Unlock()
	c.customTools = tools
}

// GetCustomTools returns the custom tools.
func (c *Client) GetCustomTools() []Tool {
	c.customToolsMu.RLock()
	defer c.customToolsMu.RUnlock()
	return c.customTools
}

// GetAllTools returns all tools from local and remote servers
// Local server tools are returned as-is
// Remote server tools are already namespaced by their client
func (c *Client) GetAllTools(ctx context.Context) ([]mcp.MCPTool, error) {
	var allTools []mcp.MCPTool

	// Add local server tools (no namespace)
	if c.localServer != nil {
		allTools = append(allTools, c.localServer.ListToolsWithContext(ctx)...)
	}

	// Add remote server tools (already namespaced by their client)
	for _, client := range c.remoteServers {
		tools, err := client.ListTools(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list tools from remote server: %w", err)
		}
		allTools = append(allTools, tools...)
	}

	return allTools, nil
}

// callTool routes a tool call to the appropriate server
// If the tool name has a namespace that matches a remote client's namespace, routes to that client
// Otherwise, routes to the local server
func (c *Client) callTool(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
	// Check if tool name matches a remote server's namespace
	// Try to find a remote client whose namespace matches the tool name
	for _, client := range c.remoteServers {
		namespace := client.Namespace()
		if namespace != "" && strings.HasPrefix(name, namespace) {
			// This client's namespace matches - call it with the full namespaced name
			return client.CallTool(ctx, name, args)
		}
	}

	// No matching remote server prefix, route to local server
	if c.localServer == nil {
		return nil, fmt.Errorf("no local MCP server configured")
	}

	return c.localServer.CallTool(ctx, name, args)
}

// GetModels retrieves the list of available models from OpenAI
func (c *Client) GetModels(ctx context.Context) (*ModelsResponse, error) {
	var response ModelsResponse

	if err := c.doRequest(ctx, "GET", "models", nil, &response); err != nil {
		return nil, fmt.Errorf("failed to get models: %w", err)
	}

	return &response, nil
}

// ChatCompletion performs a non-streaming chat completion with automatic tool processing
func (c *Client) ChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	currentMessages := req.Messages

	// Skip tool injection if request already has tools (caller is handling tools)
	requestHasTools := len(req.Tools) > 0

	if !requestHasTools {
		// Add tools from all servers
		tools, err := c.GetAllTools(ctx)
		if err == nil && len(tools) > 0 {
			req.Tools = MCPToolsToOpenAI(tools)
		}

		// Add custom tools
		c.customToolsMu.RLock()
		if len(c.customTools) > 0 {
			req.Tools = append(req.Tools, c.customTools...)
		}
		c.customToolsMu.RUnlock()
	}

	toolHandler := ToolHandlerFromContext(ctx)

	// Multi-turn tool processing loop if any servers are available
	hasServers := c.localServer != nil || len(c.remoteServers) > 0

	for iteration := 0; iteration < MAX_TOOL_CALL_ITERATIONS; iteration++ {
		req.Messages = currentMessages
		req.Stream = false

		response, err := c.nonStreamingChatCompletion(ctx, req)
		if err != nil {
			return nil, err
		}

		// If request had tools, caller handles execution - return immediately
		if requestHasTools {
			return response, nil
		}

		// If no servers, no tool calls, or no choices, we're done
		// Also return if we have custom tools (caller handles execution)
		c.customToolsMu.RLock()
		hasCustomTools := len(c.customTools) > 0
		c.customToolsMu.RUnlock()

		if !hasServers || hasCustomTools || len(response.Choices) == 0 || len(response.Choices[0].Message.ToolCalls) == 0 {
			return response, nil
		}

		// Process tool calls
		message := response.Choices[0].Message
		toolCalls := message.ToolCalls

		// Notify handler of tool calls
		if toolHandler != nil {
			for _, toolCall := range toolCalls {
				if err := toolHandler.OnToolCall(toolCall); err != nil {
					return nil, fmt.Errorf("tool handler error: %w", err)
				}
			}
		}

		// Add assistant message to conversation
		currentMessages = append(currentMessages, BuildAssistantToolCallMessage(
			message.GetContentAsString(),
			toolCalls,
		))

		// Execute tools using our routing callTool
		toolResults, err := ExecuteToolCalls(toolCalls, func(name string, args map[string]any) (string, error) {
			response, err := c.callTool(ctx, name, args)
			if err != nil {
				return "", err
			}
			result, _ := ExtractToolResult(response)
			return result, nil
		}, false)
		if err != nil {
			return nil, err
		}

		// Notify handler of tool results
		if toolHandler != nil {
			for i, toolCall := range toolCalls {
				if err := toolHandler.OnToolResult(toolCall.ID, toolCall.Function.Name, toolResults[i].Content.(string)); err != nil {
					return nil, fmt.Errorf("tool handler error: %w", err)
				}
			}
		}

		// Add tool results to conversation
		currentMessages = append(currentMessages, toolResults...)
	}

	return nil, NewMaxToolIterationsError(MAX_TOOL_CALL_ITERATIONS)
}

// StreamChatCompletion performs a streaming chat completion with automatic tool processing
// Returns a channel of pure OpenAI ChatCompletionResponse chunks
func (c *Client) StreamChatCompletion(ctx context.Context, req ChatCompletionRequest) *ChatStream {
	responseChan := make(chan ChatCompletionResponse, 50)
	errorChan := make(chan error, 1)

	go func() {
		defer close(responseChan)
		defer close(errorChan)

		// Add timeout context for the entire operation
		ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()

		currentMessages := req.Messages

		// Skip tool injection if request already has tools (caller is handling tools)
		requestHasTools := len(req.Tools) > 0
		hasServers := c.localServer != nil || len(c.remoteServers) > 0
		var hasCustomTools bool

		if !requestHasTools {
			// Add tools from all servers
			tools, err := c.GetAllTools(ctx)

			if err == nil && hasServers && len(tools) > 0 {
				req.Tools = MCPToolsToOpenAI(tools)
			}

			// Add custom tools
			c.customToolsMu.RLock()
			if len(c.customTools) > 0 {
				req.Tools = append(req.Tools, c.customTools...)
			}
			hasCustomTools = len(c.customTools) > 0
			c.customToolsMu.RUnlock()
		}

		toolHandler := ToolHandlerFromContext(ctx)

		// Multi-turn tool processing loop if any servers are available
		for iteration := 0; iteration < MAX_TOOL_CALL_ITERATIONS; iteration++ {
			req.Messages = currentMessages
			req.Stream = true

			// Stream single completion
			finalResponse, err := c.streamSingleCompletion(ctx, req, responseChan)
			if err != nil {
				errorChan <- err
				return
			}

			// If request had tools, caller handles execution - return immediately
			if requestHasTools {
				return
			}

			// If no servers, no tool calls, or no choices, we're done
			// Also return if we have custom tools (caller handles execution)
			if !hasServers || hasCustomTools || finalResponse == nil || len(finalResponse.Choices) == 0 || len(finalResponse.Choices[0].Message.ToolCalls) == 0 {
				return
			}

			// Process tool calls
			message := finalResponse.Choices[0].Message
			toolCalls := message.ToolCalls

			// Notify handler of tool calls
			if toolHandler != nil {
				for _, toolCall := range toolCalls {
					if err := toolHandler.OnToolCall(toolCall); err != nil {
						errorChan <- fmt.Errorf("tool handler error: %w", err)
						return
					}
				}
			}

			// Add assistant message to conversation
			currentMessages = append(currentMessages, BuildAssistantToolCallMessage(
				message.GetContentAsString(),
				toolCalls,
			))

			// Execute tools using our routing callTool
			toolResults, err := ExecuteToolCalls(toolCalls, func(name string, args map[string]any) (string, error) {
				response, err := c.callTool(ctx, name, args)
				if err != nil {
					return "", err
				}
				result, _ := ExtractToolResult(response)
				return result, nil
			}, false)
			if err != nil {
				errorChan <- err
				return
			}

			// Notify handler of tool results
			if toolHandler != nil {
				for i, toolCall := range toolCalls {
					if err := toolHandler.OnToolResult(toolCall.ID, toolCall.Function.Name, toolResults[i].Content.(string)); err != nil {
						errorChan <- fmt.Errorf("tool handler error: %w", err)
						return
					}
				}
			}

			// Add tool results to conversation
			currentMessages = append(currentMessages, toolResults...)
		}

		errorChan <- NewMaxToolIterationsError(MAX_TOOL_CALL_ITERATIONS)
	}()

	return NewChatStream(ctx, responseChan, errorChan)
}

// CreateResponse creates a new response using the OpenAI Responses API
// https://platform.openai.com/docs/api-reference/responses/create
func (c *Client) CreateResponse(ctx context.Context, req CreateResponseRequest) (*ResponseObject, error) {
	var response ResponseObject

	if err := c.doRequest(ctx, "POST", "responses", req, &response); err != nil {
		return nil, fmt.Errorf("failed to create response: %w", err)
	}

	return &response, nil
}

// GetResponse retrieves a response by ID using the OpenAI Responses API
// https://platform.openai.com/docs/api-reference/responses/get
func (c *Client) GetResponse(ctx context.Context, id string) (*ResponseObject, error) {
	var response ResponseObject

	if err := c.doRequest(ctx, "GET", "responses/"+id, nil, &response); err != nil {
		return nil, fmt.Errorf("failed to get response: %w", err)
	}

	return &response, nil
}

// CancelResponse cancels a response by ID using the OpenAI Responses API
// https://platform.openai.com/docs/api-reference/responses/cancel
func (c *Client) CancelResponse(ctx context.Context, id string) (*ResponseObject, error) {
	var response ResponseObject

	if err := c.doRequest(ctx, "POST", "responses/"+id+"/cancel", nil, &response); err != nil {
		return nil, fmt.Errorf("failed to cancel response: %w", err)
	}

	return &response, nil
}

// CreateEmbedding creates an embedding using the OpenAI Embeddings API
// https://platform.openai.com/docs/api-reference/embeddings/create
func (c *Client) CreateEmbedding(ctx context.Context, req EmbeddingRequest) (*EmbeddingResponse, error) {
	var response EmbeddingResponse

	if err := c.doRequest(ctx, "POST", "embeddings", req, &response); err != nil {
		return nil, fmt.Errorf("failed to create embedding: %w", err)
	}

	return &response, nil
}

// nonStreamingChatCompletion handles non-streaming chat completion
func (c *Client) nonStreamingChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	var response ChatCompletionResponse

	if err := c.doRequest(ctx, "POST", "chat/completions", req, &response); err != nil {
		return nil, fmt.Errorf("chat completion failed: %w", err)
	}

	// Ensure Choices is never nil for N8N compatibility
	if response.Choices == nil {
		response.Choices = []Choice{}
	}

	// Inject estimated usage if upstream didn't provide it
	if response.Usage == nil || (response.Usage.PromptTokens == 0 && response.Usage.CompletionTokens == 0) {
		tc := NewTokenCounter()
		tc.AddPromptTokensFromMessages(req.Messages)
		if len(response.Choices) > 0 {
			tc.AddCompletionTokensFromMessage(&response.Choices[0].Message)
		}
		tc.InjectUsageIfMissing(&response)
	}

	return &response, nil
}

// streamSingleCompletion handles a single streaming completion
func (c *Client) streamSingleCompletion(ctx context.Context, req ChatCompletionRequest, responseChan chan<- ChatCompletionResponse) (*ChatCompletionResponse, error) {
	var finalResponse *ChatCompletionResponse
	var assistantContent strings.Builder

	// Use streaming accumulator for tool calls
	toolAccumulator := NewStreamingToolCallAccumulator()

	// Check if we have any MCP servers
	hasServers := c.localServer != nil || len(c.remoteServers) > 0

	if err := c.streamRequest(ctx, "POST", "chat/completions", req, func(response *ChatCompletionResponse) (bool, error) {
		if response == nil {
			return false, fmt.Errorf("received nil response from OpenAI")
		}

		// Process the chunk for internal state first
		shouldStop, err := c.processStreamChunk(response, toolAccumulator, &assistantContent)
		if err != nil {
			return true, err
		}

		// Only send response to client if:
		// 1. No MCP servers (client handles tool calls), OR
		// 2. MCP servers exist but this chunk has no tool calls (just content) AND
		// 3. This chunk doesn't signal tool_calls finish (which would make client think stream is done)
		shouldSendToClient := !hasServers ||
			(len(response.Choices) > 0 &&
				len(response.Choices[0].Delta.ToolCalls) == 0 &&
				response.Choices[0].FinishReason != "tool_calls")

		if shouldSendToClient {
			// Send the response to the channel
			select {
			case responseChan <- *response:
			case <-ctx.Done():
				return true, ctx.Err()
			}
		}

		// Check if we should stop streaming
		if shouldStop {
			// Finalize tool calls using accumulator
			toolCalls := toolAccumulator.Finalize()

			// Create final response
			finishReason := ""
			if len(response.Choices) > 0 {
				finishReason = response.Choices[0].FinishReason
			}

			finalMessage := BuildAssistantToolCallMessage(assistantContent.String(), toolCalls)

			finalResponse = &ChatCompletionResponse{
				ID:      response.ID,
				Object:  response.Object,
				Created: response.Created,
				Model:   response.Model,
				Choices: []Choice{
					{
						Message:      finalMessage,
						FinishReason: finishReason,
					},
				},
			}

			return true, nil
		}

		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("streaming failed: %w", err)
	}

	// Inject estimated usage if upstream didn't provide it
	if finalResponse != nil && (finalResponse.Usage == nil ||
		(finalResponse.Usage.PromptTokens == 0 && finalResponse.Usage.CompletionTokens == 0)) {
		tc := NewTokenCounter()
		tc.AddPromptTokensFromMessages(req.Messages)
		tc.AddCompletionTokensFromText(assistantContent.String())
		// Add tool call tokens if any
		for _, toolCall := range toolAccumulator.Finalize() {
			tc.AddCompletionTokensFromText(toolCall.Function.Name)
			if args, err := json.Marshal(toolCall.Function.Arguments); err == nil {
				tc.AddCompletionTokensFromText(string(args))
			}
		}
		tc.InjectUsageIfMissing(finalResponse)
	}

	return finalResponse, nil
}

// processStreamChunk processes a single streaming chunk
func (c *Client) processStreamChunk(response *ChatCompletionResponse, toolAccumulator *StreamingToolCallAccumulator, assistantContent *strings.Builder) (bool, error) {
	if len(response.Choices) == 0 {
		return false, nil
	}

	choice := response.Choices[0]

	// Handle tool calls using the accumulator with ID callback
	hasServers := c.localServer != nil || len(c.remoteServers) > 0
	if len(choice.Delta.ToolCalls) > 0 && hasServers {
		// Use callback to update response with generated IDs
		toolAccumulator.ProcessDeltaWithIDCallback(choice.Delta, func(index int, id string) {
			// Update the response chunk with the generated ID so it's forwarded to clients
			for i := range choice.Delta.ToolCalls {
				if choice.Delta.ToolCalls[i].Index == index {
					response.Choices[0].Delta.ToolCalls[i].ID = id
					break
				}
			}
		})
	}

	// Handle content
	if choice.Delta.Content != "" {
		assistantContent.WriteString(choice.Delta.Content)
	}

	// Check for finish reason
	if choice.FinishReason != "" {
		return true, nil
	}

	return false, nil
}

// doRequest performs a single HTTP request
func (c *Client) doRequest(ctx context.Context, method, path string, body any, result any) error {
	reqBody, err := c.marshalBody(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	c.setHeaders(req)

	// Use custom pool if provided, otherwise use default
	var httpClient *http.Client
	if c.httpPool != nil {
		httpClient = c.httpPool.GetHTTPClient()
	} else {
		httpClient = pool.GetPool().GetHTTPClient()
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if err := c.handleResponse(resp, result); err != nil {
		return err
	}

	return nil
}

// streamRequest performs a streaming HTTP request
func (c *Client) streamRequest(ctx context.Context, method, path string, body any, chunkFunc func(*ChatCompletionResponse) (bool, error)) error {
	reqBody, err := c.marshalBody(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	c.setHeaders(req)
	req.Header.Set("Accept", "text/event-stream")

	// Use custom pool if provided, otherwise use default
	var httpClient *http.Client
	if c.httpPool != nil {
		httpClient = c.httpPool.GetHTTPClient()
	} else {
		httpClient = pool.GetPool().GetHTTPClient()
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return c.processSSEStream(resp.Body, chunkFunc)
}

// marshalBody marshals the request body
func (c *Client) marshalBody(body any) (io.Reader, error) {
	if body == nil {
		return nil, nil
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	return bytes.NewReader(data), nil
}

// setHeaders sets common headers for the request
func (c *Client) setHeaders(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "mcp-openai-client/1.0.0")

	// Apply extra headers (these can override defaults if needed)
	for key, values := range c.extraHeaders {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
}

// handleResponse handles the HTTP response
func (c *Client) handleResponse(resp *http.Response, result any) error {
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return c.handleError(resp.StatusCode, bodyBytes)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if result != nil {
		if err := json.Unmarshal(bodyBytes, result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}

// processSSEStream processes Server-Sent Events stream
func (c *Client) processSSEStream(reader io.Reader, chunkFunc func(*ChatCompletionResponse) (bool, error)) error {
	decoder := newSSEDecoder(reader)

	for {
		event, err := decoder.Next()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("failed to read SSE event: %w", err)
		}

		if event == nil || event.Data == "" {
			continue
		}

		// Skip [DONE] message
		if event.Data == "[DONE]" {
			return nil
		}

		var chunk ChatCompletionResponse
		if err := json.Unmarshal([]byte(event.Data), &chunk); err != nil {
			// Don't fail on parse errors, just skip this chunk
			continue
		}

		shouldStop, err := chunkFunc(&chunk)
		if err != nil {
			return err
		}
		if shouldStop {
			return nil
		}
	}
}

// handleError handles error responses from the API
func (c *Client) handleError(statusCode int, body []byte) error {
	var errResp ErrorResponse
	if err := json.Unmarshal(body, &errResp); err != nil {
		return &APIError{
			StatusCode: statusCode,
			Type:       "unknown",
			Message:    string(body),
		}
	}

	if errResp.Error != nil {
		errResp.Error.StatusCode = statusCode
		return errResp.Error
	}

	return &APIError{
		StatusCode: statusCode,
		Type:       "unknown",
		Message:    string(body),
	}
}

// sseDecoder handles Server-Sent Events parsing
type sseDecoder struct {
	reader *bufio.Reader
}

// newSSEDecoder creates a new SSE decoder
func newSSEDecoder(reader io.Reader) *sseDecoder {
	return &sseDecoder{
		reader: bufio.NewReader(reader),
	}
}

// sseEvent represents a single Server-Sent Event
type sseEvent struct {
	Data string
}

// Next reads the next SSE event
func (d *sseDecoder) Next() (*sseEvent, error) {
	for {
		line, err := d.reader.ReadString('\n')
		if err != nil {
			return nil, err
		}

		line = strings.TrimSpace(line)

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}

		// Look for "data:" prefix
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			data = strings.TrimSpace(data)
			return &sseEvent{Data: data}, nil
		}
	}
}
