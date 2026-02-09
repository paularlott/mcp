package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/paularlott/mcp"
	"github.com/paularlott/mcp/ai/openai"
	"github.com/paularlott/mcp/pool"
)

const (
	defaultBaseURL = "https://api.anthropic.com/v1"
	providerName   = "claude"
)

type Client struct {
	apiKey         string
	baseURL        string
	extraHeaders   http.Header
	httpPool       pool.HTTPPool
	provider       string
	localServer    openai.MCPServer // Local MCP server
	remoteServers  []*mcp.Client    // Remote MCP servers
	maxTokens      int              // Default max_tokens
	temperature    float32          // Default temperature
	requestTimeout time.Duration    // Timeout for AI operations
}

func New(config openai.Config) (*Client, error) {
	if config.BaseURL == "" {
		config.BaseURL = defaultBaseURL
	}
	if !strings.HasSuffix(config.BaseURL, "/") {
		config.BaseURL += "/"
	}

	// Default request timeout for AI operations
	if config.RequestTimeout == 0 {
		config.RequestTimeout = openai.DefaultRequestTimeout
	}

	// Create remote clients
	remoteServers := make([]*mcp.Client, len(config.RemoteServerConfigs))
	for i, rsc := range config.RemoteServerConfigs {
		if rsc.HTTPPool != nil {
			remoteServers[i] = mcp.NewClientWithPool(rsc.BaseURL, rsc.Auth, rsc.Namespace, rsc.HTTPPool)
		} else {
			remoteServers[i] = mcp.NewClient(rsc.BaseURL, rsc.Auth, rsc.Namespace)
		}
	}

	return &Client{
		apiKey:         config.APIKey,
		baseURL:        config.BaseURL,
		extraHeaders:   config.ExtraHeaders,
		httpPool:       config.HTTPPool,
		provider:       providerName,
		localServer:    config.LocalServer,
		remoteServers:  remoteServers,
		maxTokens:      config.MaxTokens,
		temperature:    config.Temperature,
		requestTimeout: config.RequestTimeout,
	}, nil
}

func (c *Client) ChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (*openai.ChatCompletionResponse, error) {
	// Detach from parent context so AI operations survive parent cancellation
	if c.requestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = openai.NewDetachedContext(ctx, c.requestTimeout)
		defer cancel()
	}

	currentMessages := req.Messages
	requestHasTools := len(req.Tools) > 0

	if !requestHasTools {
		tools, err := c.getAllTools(ctx)
		if err == nil && len(tools) > 0 {
			req.Tools = openai.MCPToolsToOpenAI(tools)
		}
	}

	toolHandler := openai.ToolHandlerFromContext(ctx)
	hasServers := c.localServer != nil || len(c.remoteServers) > 0

	// Apply client defaults if not set in request
	if req.MaxTokens == 0 && c.maxTokens > 0 {
		req.MaxTokens = c.maxTokens
	}
	if req.Temperature == 0 && c.temperature > 0 {
		req.Temperature = c.temperature
	}

	var cumulativeUsage openai.Usage
	for iteration := 0; iteration < openai.MAX_TOOL_CALL_ITERATIONS; iteration++ {
		req.Messages = currentMessages
		claudeReq := c.convertToClaudeRequest(req)

		var claudeResp ClaudeResponse
		if err := c.doRequest(ctx, "POST", "messages", claudeReq, &claudeResp); err != nil {
			return nil, err
		}

		response := c.convertToOpenAIResponse(&claudeResp)

		// Always calculate estimated usage
		tc := openai.NewTokenCounter()
		tc.AddPromptTokensFromMessages(req.Messages)
		if len(response.Choices) > 0 {
			tc.AddCompletionTokensFromMessage(&response.Choices[0].Message)
		}
		tc.InjectUsageIfMissing(response)

		// Accumulate usage across tool call iterations
		cumulativeUsage.Add(response.Usage)

		if requestHasTools || !hasServers || len(response.Choices) == 0 || len(response.Choices[0].Message.ToolCalls) == 0 {
			response.Usage = &cumulativeUsage
			return response, nil
		}

		message := response.Choices[0].Message
		toolCalls := message.ToolCalls

		if toolHandler != nil {
			for _, toolCall := range toolCalls {
				if err := toolHandler.OnToolCall(toolCall); err != nil {
					return nil, fmt.Errorf("tool handler error: %w", err)
				}
			}
		}

		currentMessages = append(currentMessages, openai.BuildAssistantToolCallMessage(
			message.GetContentAsString(),
			toolCalls,
		))

		toolResults, err := openai.ExecuteToolCalls(toolCalls, func(name string, args map[string]any) (string, error) {
			response, err := c.callTool(ctx, name, args)
			if err != nil {
				return "", err
			}
			result, _ := openai.ExtractToolResult(response)
			return result, nil
		}, false)
		if err != nil {
			return nil, err
		}

		if toolHandler != nil {
			for i, toolCall := range toolCalls {
				if err := toolHandler.OnToolResult(toolCall.ID, toolCall.Function.Name, toolResults[i].Content.(string)); err != nil {
					return nil, fmt.Errorf("tool handler error: %w", err)
				}
			}
		}

		currentMessages = append(currentMessages, toolResults...)
	}

	return nil, openai.NewMaxToolIterationsError(openai.MAX_TOOL_CALL_ITERATIONS)
}

func (c *Client) getAllTools(ctx context.Context) ([]mcp.MCPTool, error) {
	var allTools []mcp.MCPTool
	if c.localServer != nil {
		allTools = append(allTools, c.localServer.ListToolsWithContext(ctx)...)
	}
	for _, client := range c.remoteServers {
		tools, err := client.ListTools(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list tools from remote server: %w", err)
		}
		allTools = append(allTools, tools...)
	}
	return allTools, nil
}

func (c *Client) callTool(ctx context.Context, name string, args map[string]any) (*mcp.ToolResponse, error) {
	for _, client := range c.remoteServers {
		namespace := client.Namespace()
		if namespace != "" && strings.HasPrefix(name, namespace) {
			return client.CallTool(ctx, name, args)
		}
	}
	if c.localServer == nil {
		return nil, fmt.Errorf("no local MCP server configured")
	}
	return c.localServer.CallTool(ctx, name, args)
}

func (c *Client) StreamChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) *openai.ChatStream {
	responseChan := make(chan openai.ChatCompletionResponse, 50)
	errorChan := make(chan error, 1)

	go func() {
		defer close(responseChan)
		defer close(errorChan)

		// Detach from parent context so AI operations survive parent cancellation
		if c.requestTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = openai.NewDetachedContext(ctx, c.requestTimeout)
			defer cancel()
		}

		currentMessages := req.Messages
		requestHasTools := len(req.Tools) > 0
		hasServers := c.localServer != nil || len(c.remoteServers) > 0

		if !requestHasTools {
			tools, err := c.getAllTools(ctx)
			if err == nil && hasServers && len(tools) > 0 {
				req.Tools = openai.MCPToolsToOpenAI(tools)
			}
		}

		toolHandler := openai.ToolHandlerFromContext(ctx)

		// Apply client defaults if not set in request
		if req.MaxTokens == 0 && c.maxTokens > 0 {
			req.MaxTokens = c.maxTokens
		}
		if req.Temperature == 0 && c.temperature > 0 {
			req.Temperature = c.temperature
		}

		var cumulativeUsage openai.Usage
		sendCumulativeUsage := func(id, model string) {
			if cumulativeUsage.TotalTokens > 0 {
				select {
				case responseChan <- openai.ChatCompletionResponse{ID: id, Model: model, Usage: &cumulativeUsage}:
				case <-ctx.Done():
				}
			}
		}

		for iteration := 0; iteration < openai.MAX_TOOL_CALL_ITERATIONS; iteration++ {
			req.Messages = currentMessages
			claudeReq := c.convertToClaudeRequest(req)
			claudeReq.Stream = true

			finalResponse, err := c.streamSingleCompletion(ctx, claudeReq, currentMessages, responseChan)
			if err != nil {
				errorChan <- err
				return
			}

			// Accumulate usage across tool call iterations
			if finalResponse != nil {
				cumulativeUsage.Add(finalResponse.Usage)
			}

			if requestHasTools || !hasServers || finalResponse == nil || len(finalResponse.Choices) == 0 || len(finalResponse.Choices[0].Message.ToolCalls) == 0 {
				if finalResponse != nil {
					sendCumulativeUsage(finalResponse.ID, finalResponse.Model)
				}
				return
			}

			message := finalResponse.Choices[0].Message
			toolCalls := message.ToolCalls

			if toolHandler != nil {
				for _, toolCall := range toolCalls {
					if err := toolHandler.OnToolCall(toolCall); err != nil {
						errorChan <- fmt.Errorf("tool handler error: %w", err)
						return
					}
				}
			}

			currentMessages = append(currentMessages, openai.BuildAssistantToolCallMessage(
				message.GetContentAsString(),
				toolCalls,
			))

			toolResults, err := openai.ExecuteToolCalls(toolCalls, func(name string, args map[string]any) (string, error) {
				response, err := c.callTool(ctx, name, args)
				if err != nil {
					return "", err
				}
				result, _ := openai.ExtractToolResult(response)
				return result, nil
			}, false)
			if err != nil {
				errorChan <- err
				return
			}

			if toolHandler != nil {
				for i, toolCall := range toolCalls {
					if err := toolHandler.OnToolResult(toolCall.ID, toolCall.Function.Name, toolResults[i].Content.(string)); err != nil {
						errorChan <- fmt.Errorf("tool handler error: %w", err)
						return
					}
				}
			}

			currentMessages = append(currentMessages, toolResults...)
		}

		errorChan <- openai.NewMaxToolIterationsError(openai.MAX_TOOL_CALL_ITERATIONS)
	}()

	return openai.NewChatStream(ctx, responseChan, errorChan)
}

func (c *Client) streamSingleCompletion(ctx context.Context, claudeReq ClaudeRequest, originalMessages []openai.Message, responseChan chan<- openai.ChatCompletionResponse) (*openai.ChatCompletionResponse, error) {
	var assistantContent strings.Builder
	toolAccumulator := openai.NewStreamingToolCallAccumulator()
	hasServers := c.localServer != nil || len(c.remoteServers) > 0
	var responseID, responseModel string
	var finishReason string

	err := c.streamRequest(ctx, "POST", "messages", claudeReq, func(event *ClaudeStreamEvent) (bool, error) {
		chunk := c.convertStreamEventToOpenAI(event)
		if chunk != nil {
			if chunk.ID != "" {
				responseID = chunk.ID
			}
			if chunk.Model != "" {
				responseModel = chunk.Model
			}
			if len(chunk.Choices) > 0 {
				assistantContent.WriteString(chunk.Choices[0].Delta.Content)
				if len(chunk.Choices[0].Delta.ToolCalls) > 0 && hasServers {
					toolAccumulator.ProcessDelta(chunk.Choices[0].Delta)
				}
				if chunk.Choices[0].FinishReason != "" {
					finishReason = chunk.Choices[0].FinishReason
				}
			}

			// Only send to client if no servers or no tool calls
			shouldSendToClient := !hasServers || (len(chunk.Choices) > 0 && len(chunk.Choices[0].Delta.ToolCalls) == 0 && chunk.Choices[0].FinishReason != "tool_calls")
			if shouldSendToClient {
				select {
				case responseChan <- *chunk:
				case <-ctx.Done():
					return true, ctx.Err()
				}
			}
		}

		if event.Type == "message_stop" {
			return true, nil
		}
		return false, nil
	})

	if err != nil {
		return nil, err
	}

	toolCalls := toolAccumulator.Finalize()
	finalResponse := &openai.ChatCompletionResponse{
		ID:     responseID,
		Object: "chat.completion",
		Model:  responseModel,
		Choices: []openai.Choice{
			{
				Message: openai.Message{
					Role:      "assistant",
					Content:   assistantContent.String(),
					ToolCalls: toolCalls,
				},
				FinishReason: finishReason,
			},
		},
	}

	// Always calculate estimated usage
	tokenCounter := openai.NewTokenCounter()
	tokenCounter.AddPromptTokensFromMessages(originalMessages)
	tokenCounter.AddCompletionTokensFromText(assistantContent.String())
	for _, toolCall := range toolCalls {
		tokenCounter.AddCompletionTokensFromText(toolCall.Function.Name)
		if args, err := json.Marshal(toolCall.Function.Arguments); err == nil {
			tokenCounter.AddCompletionTokensFromText(string(args))
		}
	}
	tokenCounter.InjectUsageIfMissing(finalResponse)

	return finalResponse, nil
}

func (c *Client) convertToClaudeRequest(req openai.ChatCompletionRequest) ClaudeRequest {
	var temp *float64
	if req.Temperature != 0 {
		t := float64(req.Temperature)
		temp = &t
	}

	claudeReq := ClaudeRequest{
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Temperature: temp,
		Stream:      req.Stream,
	}

	// Extract system message
	var messages []openai.Message
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			claudeReq.System = msg.GetContentAsString()
		} else {
			messages = append(messages, msg)
		}
	}

	// Convert messages
	claudeReq.Messages = c.convertMessages(messages)

	// Convert tools
	if len(req.Tools) > 0 {
		claudeReq.Tools = c.convertTools(req.Tools)
	}

	return claudeReq
}

func (c *Client) convertMessages(messages []openai.Message) []ClaudeMessage {
	claudeMessages := make([]ClaudeMessage, 0, len(messages))

	for _, msg := range messages {
		// Handle tool results (role="tool")
		if msg.ToolCallID != "" {
			claudeMsg := ClaudeMessage{
				Role: "user",
				Content: []ContentBlock{
					{
						Type:      "tool_result",
						ToolUseID: msg.ToolCallID,
						Content:   msg.Content,
					},
				},
			}
			claudeMessages = append(claudeMessages, claudeMsg)
			continue
		}

		claudeMsg := ClaudeMessage{
			Role: msg.Role,
		}

		// Add text content first (if any)
		content := msg.GetContentAsString()
		if content != "" {
			claudeMsg.Content = append(claudeMsg.Content, ContentBlock{
				Type: "text",
				Text: content,
			})
		}

		// Add tool calls (if any)
		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				claudeMsg.Content = append(claudeMsg.Content, ContentBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: tc.Function.Arguments,
				})
			}
		}

		if len(claudeMsg.Content) > 0 {
			claudeMessages = append(claudeMessages, claudeMsg)
		}
	}

	return claudeMessages
}

func (c *Client) convertTools(tools []openai.Tool) []ClaudeTool {
	claudeTools := make([]ClaudeTool, len(tools))
	for i, tool := range tools {
		claudeTools[i] = ClaudeTool{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			InputSchema: tool.Function.Parameters,
		}
	}
	return claudeTools
}

func (c *Client) convertToOpenAIResponse(resp *ClaudeResponse) *openai.ChatCompletionResponse {
	var content string
	var toolCalls []openai.ToolCall

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			content += block.Text
		case "tool_use":
			toolCalls = append(toolCalls, openai.ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: openai.ToolCallFunction{
					Name:      block.Name,
					Arguments: block.Input,
				},
			})
		}
	}

	finishReason := ""
	if resp.StopReason == "end_turn" {
		finishReason = "stop"
	} else if resp.StopReason == "tool_use" {
		finishReason = "tool_calls"
	} else if resp.StopReason == "max_tokens" {
		finishReason = "length"
	}

	return &openai.ChatCompletionResponse{
		ID:      resp.ID,
		Object:  "chat.completion",
		Created: 0,
		Model:   resp.Model,
		Choices: []openai.Choice{
			{
				Index: 0,
				Message: openai.Message{
					Role:      "assistant",
					Content:   content,
					ToolCalls: toolCalls,
				},
				FinishReason: finishReason,
			},
		},
		Usage: &openai.Usage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
		},
	}
}

func (c *Client) convertStreamEventToOpenAI(event *ClaudeStreamEvent) *openai.ChatCompletionResponse {
	if event == nil {
		return nil
	}

	chunk := &openai.ChatCompletionResponse{
		Object: "chat.completion.chunk",
		Choices: []openai.Choice{
			{
				Index: 0,
				Delta: openai.Delta{},
			},
		},
	}

	switch event.Type {
	case "message_start":
		if event.Message != nil {
			chunk.ID = event.Message.ID
			chunk.Model = event.Message.Model
		}
	case "content_block_start":
		if event.ContentBlock != nil && event.ContentBlock.Type == "tool_use" {
			chunk.Choices[0].Delta.ToolCalls = []openai.DeltaToolCall{
				{
					Index: event.Index,
					ID:    event.ContentBlock.ID,
					Type:  "function",
					Function: openai.DeltaFunction{
						Name: event.ContentBlock.Name,
					},
				},
			}
		}
	case "content_block_delta":
		if event.Delta != nil {
			if event.Delta.Text != "" {
				chunk.Choices[0].Delta.Content = event.Delta.Text
			}
			if event.Delta.PartialJSON != "" {
				chunk.Choices[0].Delta.ToolCalls = []openai.DeltaToolCall{
					{
						Index: event.Index,
						Function: openai.DeltaFunction{
							Arguments: event.Delta.PartialJSON,
						},
					},
				}
			}
		}
	case "message_delta":
		if event.Delta != nil && event.Delta.StopReason != "" {
			finishReason := ""
			if event.Delta.StopReason == "end_turn" {
				finishReason = "stop"
			} else if event.Delta.StopReason == "tool_use" {
				finishReason = "tool_calls"
			} else if event.Delta.StopReason == "max_tokens" {
				finishReason = "length"
			}
			chunk.Choices[0].FinishReason = finishReason
		}
	case "message_stop":
		return nil
	default:
		return nil
	}

	return chunk
}

func (c *Client) doRequest(ctx context.Context, method, path string, body any, result any) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	c.setHeaders(req)

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
		var errResp ClaudeErrorResponse
		if json.Unmarshal(bodyBytes, &errResp) == nil {
			return fmt.Errorf("claude API error: %s", errResp.Error.Message)
		}
		return fmt.Errorf("claude API error: status %d: %s", resp.StatusCode, string(bodyBytes))
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

func (c *Client) streamRequest(ctx context.Context, method, path string, body any, eventFunc func(*ClaudeStreamEvent) (bool, error)) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	c.setHeaders(req)
	req.Header.Set("Accept", "text/event-stream")

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

	return c.processSSEStream(resp.Body, eventFunc)
}

func (c *Client) processSSEStream(reader io.Reader, eventFunc func(*ClaudeStreamEvent) (bool, error)) error {
	scanner := bufio.NewScanner(reader)
	var data strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			// End of event
			if data.Len() > 0 {
				var event ClaudeStreamEvent
				if err := json.Unmarshal([]byte(data.String()), &event); err == nil {
					shouldStop, err := eventFunc(&event)
					if err != nil {
						return err
					}
					if shouldStop {
						return nil
					}
				}
				data.Reset()
			}
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			// Skip event type line
			continue
		} else if strings.HasPrefix(line, "data: ") {
			data.WriteString(strings.TrimPrefix(line, "data: "))
		}
	}

	return scanner.Err()
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	for key, values := range c.extraHeaders {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
}

// Provider returns the provider name
func (c *Client) Provider() string {
	return c.provider
}

// SupportsCapability checks if the provider supports a capability
func (c *Client) SupportsCapability(cap string) bool {
	// Claude doesn't support embeddings or responses API
	return cap != "embeddings" && cap != "responses"
}

// GetModels fetches the list of available models from Claude API
func (c *Client) GetModels(ctx context.Context) (*openai.ModelsResponse, error) {
	type claudeModelResponse struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			CreatedAt   string `json:"created_at"`
		} `json:"data"`
	}

	var claudeResp claudeModelResponse
	if err := c.doRequest(ctx, "GET", "models", nil, &claudeResp); err != nil {
		return nil, err
	}

	models := make([]openai.Model, len(claudeResp.Data))
	for i, m := range claudeResp.Data {
		models[i] = openai.Model{
			ID:     m.ID,
			Object: "model",
		}
	}

	return &openai.ModelsResponse{
		Object: "list",
		Data:   models,
	}, nil
}

// CreateEmbedding is not supported by Claude
func (c *Client) CreateEmbedding(ctx context.Context, req openai.EmbeddingRequest) (*openai.EmbeddingResponse, error) {
	return nil, fmt.Errorf("embeddings not supported by Claude")
}

// CreateResponse is not supported by Claude
func (c *Client) CreateResponse(ctx context.Context, req openai.CreateResponseRequest) (*openai.ResponseObject, error) {
	return nil, fmt.Errorf("responses API not supported by Claude")
}

// GetResponse is not supported by Claude
func (c *Client) GetResponse(ctx context.Context, id string) (*openai.ResponseObject, error) {
	return nil, fmt.Errorf("responses API not supported by Claude")
}

// CancelResponse is not supported by Claude
func (c *Client) CancelResponse(ctx context.Context, id string) (*openai.ResponseObject, error) {
	return nil, fmt.Errorf("responses API not supported by Claude")
}

// Close closes the client
func (c *Client) Close() error {
	return nil
}
