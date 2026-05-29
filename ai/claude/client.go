package claude

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
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
	apiKey             string
	baseURL            string
	extraHeaders       http.Header
	httpPool           pool.HTTPPool
	provider           string
	localServer        openai.MCPServer
	remoteServers      []*mcp.Client
	maxTokens          int
	temperature        *float64
	topP               *float64
	requestTimeout     time.Duration
	responseManager    *openai.ResponseManager
	maxRetries         int
	retryBackoff       time.Duration
	retryOnRateLimit   bool
	retryOnServerError bool
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

	// Get the global response manager
	responseManager := openai.GetManager()

	// Retry defaults
	maxRetries := config.MaxRetries
	if maxRetries == 0 {
		maxRetries = 3
	}
	if maxRetries < -1 {
		return nil, fmt.Errorf("invalid MaxRetries %d: must be -1 (disable), 0 (default), or positive", maxRetries)
	}
	retryBackoff := config.RetryBackoff
	if retryBackoff == 0 {
		retryBackoff = time.Second
	}
	if retryBackoff < 0 {
		return nil, fmt.Errorf("invalid RetryBackoff %v: must be non-negative", retryBackoff)
	}
	retryOnRateLimit := true
	if config.RetryOnRateLimit != nil {
		retryOnRateLimit = *config.RetryOnRateLimit
	}
	retryOnServerError := true
	if config.RetryOnServerError != nil {
		retryOnServerError = *config.RetryOnServerError
	}

	return &Client{
		apiKey:             config.APIKey,
		baseURL:            config.BaseURL,
		extraHeaders:       config.ExtraHeaders,
		httpPool:           config.HTTPPool,
		provider:           providerName,
		localServer:        config.LocalServer,
		remoteServers:      remoteServers,
		maxTokens:          config.MaxTokens,
		temperature:        config.Temperature,
		topP:               config.TopP,
		requestTimeout:     config.RequestTimeout,
		responseManager:    responseManager,
		maxRetries:         maxRetries,
		retryBackoff:       retryBackoff,
		retryOnRateLimit:   retryOnRateLimit,
		retryOnServerError: retryOnServerError,
	}, nil
}

func (c *Client) ChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (*openai.ChatCompletionResponse, error) {
	if c.requestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.requestTimeout)
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
	if req.Temperature == nil && c.temperature != nil {
		req.Temperature = c.temperature
	}
	if req.TopP == nil && c.topP != nil {
		req.TopP = c.topP
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
	stream := openai.NewChatStream(ctx, responseChan, errorChan)

	go func() {
		defer close(responseChan)
		defer close(errorChan)
		defer stream.SetRetryMetadata(nil)

		if c.requestTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, c.requestTimeout)
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
		if req.Temperature == nil && c.temperature != nil {
			req.Temperature = c.temperature
		}
		if req.TopP == nil && c.topP != nil {
			req.TopP = c.topP
		}

		var cumulativeUsage openai.Usage
		sendCumulativeUsage := func(id, model string) {
			if cumulativeUsage.TotalTokens > 0 {
				select {
				case responseChan <- openai.ChatCompletionResponse{ID: id, Object: "chat.completion.chunk", Model: model, Choices: []openai.Choice{}, Usage: &cumulativeUsage}:
				case <-ctx.Done():
				}
			}
		}

		for iteration := 0; iteration < openai.MAX_TOOL_CALL_ITERATIONS; iteration++ {
			req.Messages = currentMessages
			claudeReq := c.convertToClaudeRequest(req)
			claudeReq.Stream = true

			finalResponse, retryMeta, err := c.streamSingleCompletion(ctx, claudeReq, currentMessages, responseChan)
			stream.SetRetryMetadata(retryMeta)
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

	return stream
}

func (c *Client) streamSingleCompletion(ctx context.Context, claudeReq ClaudeRequest, originalMessages []openai.Message, responseChan chan<- openai.ChatCompletionResponse) (*openai.ChatCompletionResponse, *openai.RetryMetadata, error) {
	var assistantContent strings.Builder
	toolAccumulator := openai.NewStreamingToolCallAccumulator()
	hasServers := c.localServer != nil || len(c.remoteServers) > 0
	var responseID, responseModel string
	var finishReason string

	retryMeta, streamErr := c.streamRequest(ctx, "POST", "messages", claudeReq, func(event *ClaudeStreamEvent) (bool, error) {
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

	if streamErr != nil {
		return nil, retryMeta, streamErr
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

	return finalResponse, retryMeta, nil
}

func (c *Client) convertToClaudeRequest(req openai.ChatCompletionRequest) ClaudeRequest {
	claudeReq := ClaudeRequest{
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream,
	}

	// Extract system message
	var messages []openai.Message
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			claudeReq.System = SystemField{text: msg.GetContentAsString()}
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
			}
			claudeMsg.Content = MessageContent{blocks: []ContentBlock{{
				Type:      "tool_result",
				ToolUseID: msg.ToolCallID,
				Content:   msg.Content,
			}}}
			claudeMessages = append(claudeMessages, claudeMsg)
			continue
		}

		claudeMsg := ClaudeMessage{
			Role: msg.Role,
		}

		var blocks []ContentBlock

		// Handle multimodal content (text + images)
		if parts := msg.GetContentAsParts(); len(parts) > 0 {
			var textAcc string
			for _, part := range parts {
				switch part.Type {
				case "text":
					textAcc += part.Text
				case "image_url":
					if textAcc != "" {
						blocks = append(blocks, ContentBlock{Type: "text", Text: textAcc})
						textAcc = ""
					}
					if part.ImageURL != nil && part.ImageURL.URL != "" {
						blocks = append(blocks, claudeImageBlockFromDataURL(part.ImageURL.URL))
					}
				}
			}
			if textAcc != "" {
				blocks = append(blocks, ContentBlock{Type: "text", Text: textAcc})
			}
		} else if text := msg.GetContentAsString(); text != "" {
			blocks = append(blocks, ContentBlock{Type: "text", Text: text})
		}

		for _, tc := range msg.ToolCalls {
			blocks = append(blocks, ContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: tc.Function.Arguments,
			})
		}
		if len(blocks) > 0 {
			claudeMsg.Content = MessageContent{blocks: blocks}
			claudeMessages = append(claudeMessages, claudeMsg)
		}
	}

	return claudeMessages
}

// claudeImageBlockFromDataURL converts an OpenAI image_url (data URL or regular URL)
// to a Claude image content block.
func claudeImageBlockFromDataURL(imageURL string) ContentBlock {
	// data:image/png;base64,abc123...
	if strings.HasPrefix(imageURL, "data:") {
		rest := imageURL[5:]
		idx := strings.Index(rest, ";base64,")
		if idx >= 0 {
			mediaType := rest[:idx]
			base64Data := rest[idx+8:]
			return ContentBlock{
				Type: "image",
				Source: &ImageSource{
					Type:      "base64",
					MediaType: mediaType,
					Data:      base64Data,
				},
			}
		}
	}
	// Regular URL
	return ContentBlock{
		Type: "image",
		Source: &ImageSource{
			Type: "url",
			URL:  imageURL,
		},
	}
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
	maxAttempts := c.maxRetries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var lastErr error
	var retryAfterHint time.Duration
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			bo := c.backoffForAttempt(attempt - 1)
			if retryAfterHint > bo {
				bo = retryAfterHint
			}
			retryAfterHint = 0
			timer := time.NewTimer(bo)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}

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

		respBody := decompressBody(resp)
		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(respBody)
			respBody.Close()
			resp.Body.Close()

			var errResp ClaudeErrorResponse
			statusCode := resp.StatusCode
			if json.Unmarshal(bodyBytes, &errResp) == nil {
				lastErr = fmt.Errorf("claude API error: %s", errResp.Error.Message)
			} else {
				lastErr = fmt.Errorf("claude API error: status %d: %s", statusCode, string(bodyBytes))
			}

			if c.shouldRetry(statusCode) && attempt < maxAttempts-1 {
				retryAfterHint = parseRetryAfter(resp.Header.Get("Retry-After"))
				continue
			}
			return lastErr
		}

		bodyBytes, err := io.ReadAll(respBody)
		respBody.Close()
		resp.Body.Close()
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

	return lastErr
}

func (c *Client) shouldRetry(statusCode int) bool {
	if statusCode == 429 && c.retryOnRateLimit {
		return true
	}
	if statusCode >= 500 && c.retryOnServerError {
		return true
	}
	return false
}

// backoffForAttempt returns the duration to wait before the given retry attempt (0-indexed).
// Uses exponential backoff: base * 2^attempt with a cap at 30s.
func (c *Client) backoffForAttempt(attempt int) time.Duration {
	if attempt > 30 {
		return 30 * time.Second
	}
	bo := c.retryBackoff * time.Duration(int64(1)<<uint(attempt))
	if bo > 30*time.Second {
		bo = 30 * time.Second
	}
	return bo
}

// parseRetryAfter parses a Retry-After header value and returns the duration to wait.
// Returns 0 if the header is absent, zero, or cannot be parsed.
// Supports integer seconds ("60") and HTTP-date formats.
func parseRetryAfter(header string) time.Duration {
	if header == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(header); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func (c *Client) streamRequest(ctx context.Context, method, path string, body any, eventFunc func(*ClaudeStreamEvent) (bool, error)) (*openai.RetryMetadata, error) {
	maxAttempts := c.maxRetries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var attempts int
	var rateLimitHit bool
	var totalBackoff time.Duration
	var lastErr error
	var retryAfterHint time.Duration
	for attempt := 0; attempt < maxAttempts; attempt++ {
		attempts = attempt + 1

		if attempt > 0 {
			bo := c.backoffForAttempt(attempt - 1)
			if retryAfterHint > bo {
				bo = retryAfterHint
			}
			retryAfterHint = 0
			totalBackoff += bo
			timer := time.NewTimer(bo)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}

		var reqBody io.Reader
		if body != nil {
			data, err := json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal request: %w", err)
			}
			reqBody = bytes.NewReader(data)
		}

		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
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
			return nil, fmt.Errorf("request failed: %w", err)
		}

		respBody := decompressBody(resp)
		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(respBody)
			respBody.Close()
			resp.Body.Close()
			statusCode := resp.StatusCode
			if statusCode == 429 {
				rateLimitHit = true
			}
			lastErr = fmt.Errorf("server returned status %d: %s", statusCode, string(bodyBytes))
			if c.shouldRetry(statusCode) && attempt < maxAttempts-1 {
				retryAfterHint = parseRetryAfter(resp.Header.Get("Retry-After"))
				continue
			}
			return nil, lastErr
		}

		err = c.processSSEStream(respBody, eventFunc)
		respBody.Close()
		resp.Body.Close()

		var retryMeta *openai.RetryMetadata
		if attempts > 1 {
			retryMeta = &openai.RetryMetadata{
				Attempts:     attempts,
				RateLimitHit: rateLimitHit,
				TotalBackoff: totalBackoff,
			}
		}
		return retryMeta, err
	}

	return nil, lastErr
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

func decompressBody(resp *http.Response) io.ReadCloser {
	if resp.Header.Get("Content-Encoding") == "gzip" {
		gr, err := gzip.NewReader(resp.Body)
		if err == nil {
			return gr
		}
	}
	return resp.Body
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")

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
	var claudeResp ModelsListResponse
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

// StreamResponse emulates the OpenAI Responses API streaming using chat completions
func (c *Client) StreamResponse(ctx context.Context, req openai.CreateResponseRequest) *openai.ResponseStream {
	eventChan := make(chan openai.ResponseStreamEvent, 50)
	errorChan := make(chan error, 1)
	go func() {
		defer close(eventChan)
		defer close(errorChan)
		openai.StreamResponseEmulated(ctx, c, req, eventChan, errorChan)
	}()
	return openai.NewResponseStream(ctx, eventChan, errorChan)
}

// CreateResponse emulates the OpenAI Responses API using chat completions
// Processes async in the background and returns immediately with an in_progress status
func (c *Client) CreateResponse(ctx context.Context, req openai.CreateResponseRequest) (*openai.ResponseObject, error) {
	return openai.CreateResponseEmulated(ctx, c, c.responseManager, req)
}

// GetResponse retrieves a response by ID (blocking until complete or error)
func (c *Client) GetResponse(ctx context.Context, id string) (*openai.ResponseObject, error) {
	return openai.GetResponseEmulated(ctx, c.responseManager, id)
}

// CancelResponse cancels an in-progress response
func (c *Client) CancelResponse(ctx context.Context, id string) (*openai.ResponseObject, error) {
	return openai.CancelResponseEmulated(ctx, c.responseManager, id)
}

// DeleteResponse deletes a response by ID
func (c *Client) DeleteResponse(ctx context.Context, id string) error {
	return openai.DeleteResponseEmulated(ctx, c.responseManager, id)
}

// CompactResponse compacts a response by removing intermediate reasoning steps
func (c *Client) CompactResponse(ctx context.Context, id string) (*openai.ResponseObject, error) {
	return openai.CompactResponseEmulated(ctx, c.responseManager, id)
}

// Close closes the client
// Note: Response managers persist for 15 minutes after last response expires
func (c *Client) Close() error {
	return nil
}
