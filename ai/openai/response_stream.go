package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/paularlott/mcp/pool"
)

func timeNowUnix() int64 { return time.Now().Unix() }

func jsonMarshal(v any) (json.RawMessage, error) {
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
	eventChan  <-chan ResponseStreamEvent
	errorChan  <-chan error
	ctx        context.Context
	current    *ResponseStreamEvent
	err        error
	pendingErr error // received, but delivered after the events buffered before it
	done       bool
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
		// An error is reported only after the events buffered before it:
		// the producer sends its events, then the error, so by the time the
		// error arrives every earlier event is already in the buffer.
		if s.pendingErr != nil {
			select {
			case event, ok := <-s.eventChan:
				if ok {
					s.current = &event
					return true
				}
			default:
			}
			s.err = s.pendingErr
			s.done = true
			return false
		}

		select {
		case <-s.ctx.Done():
			s.err = s.ctx.Err()
			s.done = true
			return false
		case err, ok := <-s.errorChan:
			if ok && err != nil {
				s.pendingErr = err
				s.errorChan = nil
				continue
			}
			s.errorChan = nil
			continue
		case event, ok := <-s.eventChan:
			if !ok {
				// Event channel closed. Before declaring the stream done, drain
				// errorChan: the producer goroutine may have sent an error just
				// before closing both channels, and select may have picked
				// eventChan first (Go's select is non-deterministic when
				// multiple cases are ready). Without this check the error would
				// be silently lost.
				if s.errorChan != nil {
					select {
					case err, eok := <-s.errorChan:
						if eok && err != nil {
							s.err = err
						}
					default:
					}
				}
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
			StreamResponseEmulatedWithManager(ctx, c, c.responses, req, eventChan, errorChan)
		}
	}()

	return NewResponseStream(ctx, eventChan, errorChan)
}

// streamResponseNative streams using the real OpenAI /responses SSE endpoint with tool processing.
func (c *Client) streamResponseNative(ctx context.Context, req CreateResponseRequest, eventChan chan<- ResponseStreamEvent, errorChan chan<- error) {
	if c.requestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.requestTimeout)
		defer cancel()
	}

	requestHasTools := len(req.Tools) > 0
	hasServers := c.localServer != nil || len(c.remoteServers) > 0

	var err error
	if req.PreviousResponseID, err = c.nativeResponseID(ctx, req.PreviousResponseID); err != nil {
		errorChan <- err
		return
	}

	if !requestHasTools && !MCPToolsDisabled(ctx) {
		tools, err := c.getAllTools(ctx)
		if err == nil && len(tools) > 0 {
			req.Tools = MCPToolsToOpenAI(tools)
		}
	}

	toolHandler := ToolHandlerFromContext(ctx)

	// When the client runs the tools, merge the rounds into one lifecycle
	var merge *toolLoopStream
	if !requestHasTools && hasServers {
		merge = &toolLoopStream{}
	}

	for iteration := 0; iteration < MAX_TOOL_CALL_ITERATIONS; iteration++ {
		req.Background = false
		if merge != nil {
			merge.startRound()
		}

		finalResp, err := c.streamSingleResponse(ctx, req, eventChan, merge)
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

		req.Input = appendToolTurnToInput(req.Input, finalResp, toolResults)
	}

	errorChan <- NewMaxToolIterationsError(MAX_TOOL_CALL_ITERATIONS)
}

// streamSingleResponse makes one streaming call to /responses and forwards events.
// With merge set, events are rewritten into the tool loop's single lifecycle
// (see toolLoopStream); otherwise they are forwarded as is.
// Returns the upstream ResponseObject from the "response.completed" event.
func (c *Client) streamSingleResponse(ctx context.Context, req CreateResponseRequest, eventChan chan<- ResponseStreamEvent, merge *toolLoopStream) (*ResponseObject, error) {
	// Set stream via ExtraBody: embedding the request in a wrapper struct with
	// a Stream field doesn't work, as its MarshalJSON is promoted and drops it.
	extra := make(map[string]any, len(req.ExtraBody)+1)
	for k, v := range req.ExtraBody {
		extra[k] = v
	}
	extra["stream"] = true
	req.ExtraBody = extra

	reqBody, err := c.marshalBody(req)
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
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "text/event-stream") {
		return nil, fmt.Errorf("expected text/event-stream response, got %q", ct)
	}

	var finalResp *ResponseObject
	decoder := newSSEDecoder(resp.Body)

	for {
		sseEvent, err := decoder.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
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

		if merge != nil {
			var forward bool
			if event, forward = merge.rewrite(event); !forward {
				continue
			}
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
	StreamResponseEmulatedWithManager(ctx, completer, GetManager(), req, eventChan, errorChan)
}

// StreamResponseEmulatedWithManager is StreamResponseEmulated with an explicit
// response manager: it continues the conversation of req.PreviousResponseID
// from the manager and stores the streamed response there (unless req.Store
// is false), so it can be retrieved or continued by ID like a non-streamed
// one.
func StreamResponseEmulatedWithManager(ctx context.Context, completer ChatStreamCompleter, manager *ResponseManager, req CreateResponseRequest, eventChan chan<- ResponseStreamEvent, errorChan chan<- error) {
	chatReq, conv, err := emulatedChatRequest(ctx, manager, req)
	if err != nil {
		errorChan <- err
		return
	}

	// Cancelling the response (from any instance) stops the stream
	ctx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()

	// With store: false nothing is kept; the response only gets an ID
	var state *ResponseState
	respID := generateID()
	if storeRequested(req) {
		if state, err = manager.begin(ctx, cancelStream, req.Model, conv); err != nil {
			errorChan <- err
			return
		}
		respID = state.ID
	}
	finished := false
	defer func() {
		// Don't leave an abandoned stream in progress forever
		if !finished && state != nil {
			if ctx.Err() != nil {
				state.SetError(ctx.Err())
			} else {
				state.SetError(fmt.Errorf("stream ended before completion"))
			}
		}
	}()

	createdAt := timeNowUnix()

	send := func(eventType string, payload map[string]any) bool {
		payload["type"] = eventType
		data, _ := jsonMarshal(payload)
		select {
		case eventChan <- ResponseStreamEvent{Type: eventType, Data: data}:
			return true
		case <-ctx.Done():
			return false
		}
	}

	if !send("response.created", map[string]any{
		"response": map[string]any{
			"id": respID, "object": "response",
			"status": "in_progress", "model": req.Model, "created_at": createdAt,
		},
	}) {
		return
	}
	if !send("response.in_progress", map[string]any{
		"response": map[string]any{
			"id": respID, "object": "response",
			"status": "in_progress", "model": req.Model, "created_at": createdAt,
		},
	}) {
		return
	}

	// Output items are opened in the order they first appear in the stream:
	// a message item for text, a function_call item per tool call.
	type outputItem struct {
		id       string
		toolIdx  int // tool call index; -1 for the message item
		callID   string
		name     string
		argsSent int // bytes of arguments already sent as deltas
	}
	var items []*outputItem
	var msgItem *outputItem
	toolItems := map[int]*outputItem{}
	toolAcc := NewStreamingToolCallAccumulator()

	openMessage := func() bool {
		msgItem = &outputItem{id: generateID(), toolIdx: -1}
		items = append(items, msgItem)
		idx := len(items) - 1
		return send("response.output_item.added", map[string]any{
			"output_index": idx,
			"item": map[string]any{
				"id": msgItem.id, "type": "message",
				"role": "assistant", "status": "in_progress", "content": []any{},
			},
		}) && send("response.content_part.added", map[string]any{
			"item_id": msgItem.id, "output_index": idx, "content_index": 0,
			"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
		})
	}
	outputIndex := func(item *outputItem) int {
		for i, it := range items {
			if it == item {
				return i
			}
		}
		return -1
	}

	stream := completer.StreamChatCompletion(ctx, chatReq)

	var textBuf strings.Builder
	var finalUsage *Usage

	for stream.Next() {
		chunk := stream.Current()
		if chunk.Usage != nil {
			finalUsage = chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		if delta.Content != "" {
			if msgItem == nil && !openMessage() {
				return
			}
			textBuf.WriteString(delta.Content)
			if !send("response.output_text.delta", map[string]any{
				"item_id": msgItem.id, "output_index": outputIndex(msgItem), "content_index": 0, "delta": delta.Content,
			}) {
				return
			}
		}
		if len(delta.ToolCalls) == 0 {
			continue
		}
		toolAcc.ProcessDelta(delta)
		for _, dtc := range delta.ToolCalls {
			acc := toolAcc.toolCalls[dtc.Index]
			item := toolItems[dtc.Index]
			if item == nil {
				item = &outputItem{id: acc.ID, toolIdx: dtc.Index, callID: acc.ID, name: acc.Name}
				toolItems[dtc.Index] = item
				items = append(items, item)
				if !send("response.output_item.added", map[string]any{
					"output_index": len(items) - 1,
					"item": map[string]any{
						"id": item.id, "type": "function_call", "status": "in_progress",
						"call_id": item.callID, "name": item.name, "arguments": "",
					},
				}) {
					return
				}
			}
			if args := acc.Arguments.String(); len(args) > item.argsSent {
				fragment := args[item.argsSent:]
				item.argsSent = len(args)
				if !send("response.function_call_arguments.delta", map[string]any{
					"item_id": item.id, "output_index": outputIndex(item), "delta": fragment,
				}) {
					return
				}
			}
		}
	}
	if err := stream.Err(); err != nil {
		if state != nil {
			state.SetError(err)
		}
		finished = true
		errorChan <- err
		return
	}

	// A reply with neither text nor tool calls still gets an (empty) message
	if len(items) == 0 && !openMessage() {
		return
	}

	fullText := textBuf.String()
	output := make([]any, 0, len(items))
	for idx, item := range items {
		if item.toolIdx < 0 {
			if !send("response.output_text.done", map[string]any{
				"item_id": item.id, "output_index": idx, "content_index": 0, "text": fullText,
			}) {
				return
			}
			if !send("response.content_part.done", map[string]any{
				"item_id": item.id, "output_index": idx, "content_index": 0,
				"part": map[string]any{"type": "output_text", "text": fullText, "annotations": []any{}},
			}) {
				return
			}
			done := map[string]any{
				"id": item.id, "type": "message", "role": "assistant", "status": "completed",
				"content": []any{
					map[string]any{"type": "output_text", "text": fullText, "annotations": []any{}},
				},
			}
			if !send("response.output_item.done", map[string]any{"output_index": idx, "item": done}) {
				return
			}
			output = append(output, done)
			continue
		}

		acc := toolAcc.toolCalls[item.toolIdx]
		args := acc.Arguments.String()
		if args == "" || args == "null" {
			args = "{}"
		}
		if !send("response.function_call_arguments.done", map[string]any{
			"item_id": item.id, "output_index": idx, "name": acc.Name, "arguments": args,
		}) {
			return
		}
		done := map[string]any{
			"id": item.id, "type": "function_call", "status": "completed",
			"call_id": item.callID, "name": acc.Name, "arguments": args,
		}
		if !send("response.output_item.done", map[string]any{"output_index": idx, "item": done}) {
			return
		}
		output = append(output, done)
	}

	respObj := &ResponseObject{
		ID: respID, Object: "response", Status: "completed",
		CreatedAt: createdAt, Model: req.Model,
		Usage:  toResponseUsage(finalUsage),
		Output: output,
	}
	if state != nil {
		reply := Message{Role: "assistant", Content: fullText, ToolCalls: toolAcc.Finalize()}
		state.Lock()
		state.reply = &reply
		state.Unlock()
		// A failed save is reported and retried; the stream still completes
		_ = state.finish(StatusCompleted, respObj, nil)
	}
	finished = true
	send("response.completed", map[string]any{"response": respObj})
}
