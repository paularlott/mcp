package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/paularlott/mcp/pool"
)

func timeNowUnix() int64 { return time.Now().Unix() }

func jsonMarshal(v interface{}) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	return json.RawMessage(b), err
}

// ResponseStreamEvent represents a single SSE event from the Responses API stream.
// The Type field identifies the event kind; the Data field holds the raw JSON payload.
//
// Common event types:
//   - "response.created"           – response object created
//   - "response.output_item.added" – new output item started
//   - "response.output_text.delta" – text delta (use TextDelta())
//   - "response.output_text.done"  – text item complete
//   - "response.completed"         – full response object available (use Response())
//   - "error"                      – stream error
type ResponseStreamEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage // raw event payload
}

// TextDelta returns the text delta for "response.output_text.delta" events, otherwise "".
func (e *ResponseStreamEvent) TextDelta() string {
	if e.Type != "response.output_text.delta" {
		return ""
	}
	var v struct {
		Delta string `json:"delta"`
	}
	_ = json.Unmarshal(e.Data, &v)
	return v.Delta
}

// Response returns the ResponseObject for "response.completed" events, otherwise nil.
func (e *ResponseStreamEvent) Response() *ResponseObject {
	if e.Type != "response.completed" {
		return nil
	}
	var v struct {
		Response ResponseObject `json:"response"`
	}
	if err := json.Unmarshal(e.Data, &v); err != nil {
		return nil
	}
	return &v.Response
}

// ResponseStream is an iterator for Responses API SSE events.
//
//	stream := client.StreamResponse(ctx, req)
//	for stream.Next() {
//	    event := stream.Current()
//	    fmt.Print(event.TextDelta())
//	}
//	if err := stream.Err(); err != nil { ... }
type ResponseStream struct {
	eventChan <-chan ResponseStreamEvent
	errorChan <-chan error
	ctx       context.Context
	current   *ResponseStreamEvent
	err       error
	done      bool
}

// NewResponseStream creates a ResponseStream from event and error channels.
func NewResponseStream(ctx context.Context, eventChan <-chan ResponseStreamEvent, errorChan <-chan error) *ResponseStream {
	return &ResponseStream{
		eventChan: eventChan,
		errorChan: errorChan,
		ctx:       ctx,
	}
}

// Next advances to the next event. Returns false when the stream ends or errors.
func (s *ResponseStream) Next() bool {
	if s.done {
		return false
	}
	for {
		select {
		case <-s.ctx.Done():
			s.err = s.ctx.Err()
			s.done = true
			return false
		case err, ok := <-s.errorChan:
			if ok && err != nil {
				s.err = err
				s.done = true
				return false
			}
			s.errorChan = nil
			continue
		case event, ok := <-s.eventChan:
			if !ok {
				s.done = true
				return false
			}
			s.current = &event
			return true
		}
	}
}

// Current returns the current event. Must be called after Next returns true.
func (s *ResponseStream) Current() ResponseStreamEvent {
	if s.current == nil {
		return ResponseStreamEvent{}
	}
	return *s.current
}

// Err returns any error that occurred during streaming.
func (s *ResponseStream) Err() error {
	return s.err
}

// StreamResponse streams a response from the OpenAI Responses API.
// For native OpenAI (api.openai.com), uses the real SSE /responses endpoint.
// For other providers, emulates streaming via ChatCompletion.
// Tool calls from attached MCP servers are processed automatically.
func (c *Client) StreamResponse(ctx context.Context, req CreateResponseRequest) *ResponseStream {
	eventChan := make(chan ResponseStreamEvent, 50)
	errorChan := make(chan error, 1)

	go func() {
		defer close(eventChan)
		defer close(errorChan)

		if c.useNativeResponses {
			c.streamResponseNative(ctx, req, eventChan, errorChan)
		} else {
			StreamResponseEmulated(ctx, c, req, eventChan, errorChan)
		}
	}()

	return NewResponseStream(ctx, eventChan, errorChan)
}

// streamResponseNative streams using the real OpenAI /responses SSE endpoint with tool processing.
func (c *Client) streamResponseNative(ctx context.Context, req CreateResponseRequest, eventChan chan<- ResponseStreamEvent, errorChan chan<- error) {
	// Detach context so AI ops survive parent cancellation
	if c.requestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = NewDetachedContext(ctx, c.requestTimeout)
		defer cancel()
	}

	requestHasTools := len(req.Tools) > 0
	hasServers := c.localServer != nil || len(c.remoteServers) > 0

	if !requestHasTools {
		tools, err := c.getAllTools(ctx)
		if err == nil && len(tools) > 0 {
			req.Tools = MCPToolsToOpenAI(tools)
		}
	}

	toolHandler := ToolHandlerFromContext(ctx)

	for iteration := 0; iteration < MAX_TOOL_CALL_ITERATIONS; iteration++ {
		req.Background = false

		finalResp, err := c.streamSingleResponse(ctx, req, eventChan, requestHasTools || !hasServers)
		if err != nil {
			errorChan <- err
			return
		}

		if requestHasTools || !hasServers || finalResp == nil || !hasResponseToolCalls(finalResp) {
			return
		}

		toolCalls := extractToolCallsFromResponse(finalResp)

		if toolHandler != nil {
			for _, tc := range toolCalls {
				if err := toolHandler.OnToolCall(tc); err != nil {
					errorChan <- fmt.Errorf("tool handler error: %w", err)
					return
				}
			}
		}

		toolResults, err := ExecuteToolCalls(toolCalls, func(name string, args map[string]any) (string, error) {
			resp, err := c.callTool(ctx, name, args)
			if err != nil {
				return "", err
			}
			result, _ := ExtractToolResult(resp)
			return result, nil
		}, false)
		if err != nil {
			errorChan <- err
			return
		}

		if toolHandler != nil {
			for i, tc := range toolCalls {
				if err := toolHandler.OnToolResult(tc.ID, tc.Function.Name, toolResults[i].Content.(string)); err != nil {
					errorChan <- fmt.Errorf("tool handler error: %w", err)
					return
				}
			}
		}

		// Append tool results to input for next iteration
		for _, result := range toolResults {
			req.Input = append(req.Input, map[string]interface{}{
				"type":         "function_call_output",
				"call_id":      result.ToolCallID,
				"output":       result.Content,
			})
		}
	}

	errorChan <- NewMaxToolIterationsError(MAX_TOOL_CALL_ITERATIONS)
}

// streamSingleResponse makes one streaming call to /responses and forwards events.
// If forwardAll is true, all events are forwarded; otherwise tool-related events are suppressed.
// Returns the final ResponseObject from the "response.completed" event.
func (c *Client) streamSingleResponse(ctx context.Context, req CreateResponseRequest, eventChan chan<- ResponseStreamEvent, forwardAll bool) (*ResponseObject, error) {
	streamReq := struct {
		CreateResponseRequest
		Stream bool `json:"stream"`
	}{req, true}

	reqBody, err := c.marshalBody(streamReq)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"responses", reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	c.setHeaders(httpReq)
	httpReq.Header.Set("Accept", "text/event-stream")

	var httpClient *http.Client
	if c.httpPool != nil {
		httpClient = c.httpPool.GetHTTPClient()
	} else {
		httpClient = pool.GetPool().GetHTTPClient()
	}

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, c.handleError(resp.StatusCode, body)
	}

	var finalResp *ResponseObject
	decoder := newSSEDecoder(resp.Body)

	for {
		sseEvent, err := decoder.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("SSE read error: %w", err)
		}
		if sseEvent == nil || sseEvent.Data == "" {
			continue
		}
		if sseEvent.Data == "[DONE]" {
			break
		}

		// Parse the event type from the JSON payload
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(sseEvent.Data), &envelope); err != nil {
			continue
		}

		event := ResponseStreamEvent{
			Type: envelope.Type,
			Data: json.RawMessage(sseEvent.Data),
		}

		// Capture the completed response
		if envelope.Type == "response.completed" {
			finalResp = event.Response()
		}

		// Suppress tool-call events when we're handling tools internally
		isToolEvent := strings.HasPrefix(envelope.Type, "response.function_call") ||
			strings.HasPrefix(envelope.Type, "response.tool_call")
		if !forwardAll && isToolEvent {
			continue
		}

		select {
		case eventChan <- event:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return finalResp, nil
}

// StreamResponseEmulated emulates Responses API streaming via StreamChatCompletion.
// Emits the same lifecycle events as the native SSE endpoint so callers see
// identical behaviour regardless of provider.
// This is a standalone function so Gemini, Claude, and other providers can use it directly.
func StreamResponseEmulated(ctx context.Context, completer ChatStreamCompleter, req CreateResponseRequest, eventChan chan<- ResponseStreamEvent, errorChan chan<- error) {
	chatReq, err := ConvertResponseToChatRequest(req)
	if err != nil {
		errorChan <- err
		return
	}

	respID := generateID()
	createdAt := timeNowUnix()

	send := func(eventType string, payload map[string]interface{}) bool {
		payload["type"] = eventType
		data, _ := jsonMarshal(payload)
		select {
		case eventChan <- ResponseStreamEvent{Type: eventType, Data: data}:
			return true
		case <-ctx.Done():
			return false
		}
	}

	if !send("response.created", map[string]interface{}{
		"response": map[string]interface{}{
			"id": respID, "object": "response",
			"status": "in_progress", "model": req.Model, "created_at": createdAt,
		},
	}) {
		return
	}
	if !send("response.in_progress", map[string]interface{}{
		"response": map[string]interface{}{
			"id": respID, "object": "response",
			"status": "in_progress", "model": req.Model, "created_at": createdAt,
		},
	}) {
		return
	}

	msgItemID := generateID()
	if !send("response.output_item.added", map[string]interface{}{
		"output_index": 0,
		"item": map[string]interface{}{
			"id": msgItemID, "type": "message",
			"role": "assistant", "status": "in_progress", "content": []interface{}{},
		},
	}) {
		return
	}
	if !send("response.content_part.added", map[string]interface{}{
		"item_id": msgItemID, "output_index": 0, "content_index": 0,
		"part": map[string]interface{}{"type": "output_text", "text": "", "annotations": []interface{}{}},
	}) {
		return
	}

	stream := completer.StreamChatCompletion(ctx, chatReq)

	var textBuf strings.Builder
	var finalUsage *Usage
	var finalChunk *ChatCompletionResponse

	for stream.Next() {
		chunk := stream.Current()
		if chunk.Usage != nil {
			finalUsage = chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		if delta := chunk.Choices[0].Delta.Content; delta != "" {
			textBuf.WriteString(delta)
			if !send("response.output_text.delta", map[string]interface{}{
				"item_id": msgItemID, "output_index": 0, "content_index": 0, "delta": delta,
			}) {
				return
			}
		}
		if chunk.Choices[0].FinishReason != "" {
			finalChunk = &chunk
		}
	}
	if err := stream.Err(); err != nil {
		errorChan <- err
		return
	}

	fullText := textBuf.String()

	if !send("response.output_text.done", map[string]interface{}{
		"item_id": msgItemID, "output_index": 0, "content_index": 0, "text": fullText,
	}) {
		return
	}
	if !send("response.content_part.done", map[string]interface{}{
		"item_id": msgItemID, "output_index": 0, "content_index": 0,
		"part": map[string]interface{}{"type": "output_text", "text": fullText, "annotations": []interface{}{}},
	}) {
		return
	}
	if !send("response.output_item.done", map[string]interface{}{
		"output_index": 0,
		"item": map[string]interface{}{
			"id": msgItemID, "type": "message", "role": "assistant", "status": "completed",
			"content": []interface{}{
				map[string]interface{}{"type": "output_text", "text": fullText, "annotations": []interface{}{}},
			},
		},
	}) {
		return
	}

	chatID := respID
	if finalChunk != nil && finalChunk.ID != "" {
		chatID = finalChunk.ID
	}
	respObj := &ResponseObject{
		ID: chatID, Object: "response", Status: "completed",
		CreatedAt: createdAt, Model: req.Model,
		Usage: toResponseUsage(finalUsage),
		Output: []interface{}{
			map[string]interface{}{
				"id": msgItemID, "type": "message", "role": "assistant", "status": "completed",
				"content": []interface{}{
					map[string]interface{}{"type": "output_text", "text": fullText, "annotations": []interface{}{}},
				},
			},
		},
	}
	send("response.completed", map[string]interface{}{"response": respObj})
}
