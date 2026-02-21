package openai

import (
	"context"
	"fmt"
	"time"
)

// ChatCompleter defines the interface for providers that can emulate responses via chat completions
type ChatCompleter interface {
	ChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error)
}

// ChatStreamCompleter extends ChatCompleter with streaming support, used for StreamResponseEmulated.
type ChatStreamCompleter interface {
	ChatCompleter
	StreamChatCompletion(ctx context.Context, req ChatCompletionRequest) *ChatStream
}

// CreateResponseEmulated creates an emulated response using chat completions
// If background: true, returns immediately with in_progress status and processes async
// If background: false, processes synchronously and returns completed result
func CreateResponseEmulated(ctx context.Context, completer ChatCompleter, manager *ResponseManager, req CreateResponseRequest) (*ResponseObject, error) {
	if req.Background {
		return createResponseBackground(ctx, completer, manager, req)
	}
	return createResponseSync(ctx, completer, req)
}

// createResponseBackground creates an async response that processes in background
func createResponseBackground(ctx context.Context, completer ChatCompleter, manager *ResponseManager, req CreateResponseRequest) (*ResponseObject, error) {
	// Create detached context with timeout for async processing
	asyncCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)

	// Create response state
	state := manager.Create(cancel)

	// Start async processing
	go processResponseAsync(asyncCtx, state, req, completer)

	// Return immediately with in_progress status
	return &ResponseObject{
		ID:        state.ID,
		Object:    "response",
		Status:    "in_progress",
		CreatedAt: time.Now().Unix(),
		Model:     req.Model,
	}, nil
}

// createResponseSync processes the response synchronously
func createResponseSync(ctx context.Context, completer ChatCompleter, req CreateResponseRequest) (*ResponseObject, error) {
	// Convert CreateResponseRequest to ChatCompletionRequest
	chatReq, err := ConvertResponseToChatRequest(req)
	if err != nil {
		return nil, err
	}

	// Use the completer's ChatCompletion which handles tools automatically
	chatResp, err := completer.ChatCompletion(ctx, chatReq)
	if err != nil {
		return nil, err
	}

	// Convert ChatCompletionResponse to ResponseObject
	return ConvertChatToResponseObject(chatResp, req.Model), nil
}

// GetResponseEmulated retrieves a response by ID (blocking until complete or error)
func GetResponseEmulated(ctx context.Context, manager *ResponseManager, id string) (*ResponseObject, error) {
	state, ok := manager.Get(id)
	if !ok {
		return nil, fmt.Errorf("response not found: %s", id)
	}

	state.RLock()
	status := state.Status
	result := state.Result
	err := state.Error
	state.RUnlock()

	// If still in progress, wait for it to complete or context timeout
	if status == StatusInProgress || status == StatusQueued {
		// Poll with timeout
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		timeout := time.NewTimer(30 * time.Second)
		defer timeout.Stop()

		for {
			select {
			case <-ticker.C:
				state.RLock()
				status = state.Status
				result = state.Result
				err = state.Error
				state.RUnlock()

				if status != StatusInProgress && status != StatusQueued {
					goto done
				}
			case <-timeout.C:
				return nil, fmt.Errorf("timeout waiting for response")
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

done:
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("response completed but result is nil")
	}

	return result, nil
}

// CancelResponseEmulated cancels an in-progress response
func CancelResponseEmulated(ctx context.Context, manager *ResponseManager, id string) (*ResponseObject, error) {
	if err := manager.Cancel(id); err != nil {
		return nil, err
	}
	return GetResponseEmulated(ctx, manager, id)
}

// DeleteResponseEmulated deletes an in-progress or completed response
func DeleteResponseEmulated(ctx context.Context, manager *ResponseManager, id string) error {
	state, ok := manager.Get(id)
	if !ok {
		return fmt.Errorf("response not found: %s", id)
	}

	// Cancel if still in progress
	if state.GetStatus() == StatusInProgress || state.GetStatus() == StatusQueued {
		state.Cancel()
	}

	// Delete from manager
	manager.Delete(id)
	return nil
}

// CompactResponseEmulated compacts a response by removing intermediate reasoning steps
// For emulated responses, this returns the response with reasoning content removed
func CompactResponseEmulated(ctx context.Context, manager *ResponseManager, id string) (*ResponseObject, error) {
	// Get the response first
	response, err := GetResponseEmulated(ctx, manager, id)
	if err != nil {
		return nil, err
	}

	// Remove reasoning items from output
	if response.Output != nil {
		compactedOutput := make([]interface{}, 0)
		for _, item := range response.Output {
			if itemMap, ok := item.(map[string]interface{}); ok {
				itemType, _ := itemMap["type"].(string)
				// Keep everything except reasoning type
				if itemType != "reasoning" {
					compactedOutput = append(compactedOutput, item)
				}
			}
		}
		response.Output = compactedOutput
	}

	return response, nil
}

// processResponseAsync processes the response request asynchronously
func processResponseAsync(ctx context.Context, state *ResponseState, req CreateResponseRequest, completer ChatCompleter) {
	defer func() {
		if r := recover(); r != nil {
			state.SetError(fmt.Errorf("panic during response processing: %v", r))
		}
	}()

	// Convert CreateResponseRequest to ChatCompletionRequest
	chatReq, err := ConvertResponseToChatRequest(req)
	if err != nil {
		state.SetError(err)
		return
	}

	// Use the completer's ChatCompletion which handles tools automatically
	chatResp, err := completer.ChatCompletion(ctx, chatReq)
	if err != nil {
		state.SetError(err)
		return
	}

	// Convert ChatCompletionResponse to ResponseObject
	respObj := ConvertChatToResponseObject(chatResp, req.Model)

	state.SetResult(respObj)
}

// ConvertResponseToChatRequest converts a CreateResponseRequest to a ChatCompletionRequest
func ConvertResponseToChatRequest(req CreateResponseRequest) (ChatCompletionRequest, error) {
	chatReq := ChatCompletionRequest{
		Model: req.Model,
	}

	// Apply max output tokens if specified
	if req.MaxOutputTokens != nil {
		chatReq.MaxCompletionTokens = *req.MaxOutputTokens
	}

	// Apply temperature if specified
	if req.Temperature != nil {
		chatReq.Temperature = float32(*req.Temperature)
	}

	// Convert input to messages
	chatReq.Messages = ConvertInputToMessages(req.Input)

	// Copy tools if provided
	if len(req.Tools) > 0 {
		chatReq.Tools = req.Tools
	}

	return chatReq, nil
}

// ConvertInputToMessages converts Response API input to ChatCompletion messages
func ConvertInputToMessages(input []any) []Message {
	var messages []Message

	for _, item := range input {
		if itemMap, ok := item.(map[string]interface{}); ok {
			itemType, _ := itemMap["type"].(string)

			switch itemType {
			case "message", "user_message", "system_message", "assistant_message":
				msg := Message{
					Role: getRoleFromItemType(itemType),
				}
				if content, ok := itemMap["content"]; ok {
					msg.Content = content
				}
				messages = append(messages, msg)

			case "tool_call_result", "function_call_output":
				// Tool result message — supports both "tool_call_result" (legacy) and
				// "function_call_output" (native Responses API format)
				callID := getString(itemMap, "call_id")
				if callID == "" {
					callID = getString(itemMap, "tool_call_id")
				}
				msg := Message{
					Role:       "tool",
					ToolCallID: callID,
				}
				// "output" is the native field; fall back to "content"
				if output, ok := itemMap["output"]; ok {
					msg.Content = output
				} else if content, ok := itemMap["content"]; ok {
					msg.Content = content
				}
				messages = append(messages, msg)
			}
		}
	}

	return messages
}

// toResponseUsage converts a Usage (chat completions) to ResponseUsage (responses API)
func toResponseUsage(u *Usage) *ResponseUsage {
	if u == nil {
		return nil
	}

	ru := &ResponseUsage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
		TotalTokens:  u.TotalTokens,
	}

	if u.PromptTokensDetails != nil {
		ru.InputTokensDetails = &ResponseInputTokensDetails{
			CachedTokens: u.PromptTokensDetails.CachedTokens,
		}
	}

	if u.CompletionTokensDetails != nil {
		ru.OutputTokensDetails = &ResponseOutputTokensDetails{
			ReasoningTokens: u.CompletionTokensDetails.ReasoningTokens,
		}
	}

	return ru
}

// getRoleFromItemType maps Response API item types to chat roles
func getRoleFromItemType(itemType string) string {
	switch itemType {
	case "user_message":
		return "user"
	case "system_message":
		return "system"
	case "assistant_message":
		return "assistant"
	default:
		return "user"
	}
}

// getString extracts a string value from a map
func getString(m map[string]interface{}, key string) string {
	if val, ok := m[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

// ConvertChatToResponseObject converts a ChatCompletionResponse to a ResponseObject
func ConvertChatToResponseObject(resp *ChatCompletionResponse, model string) *ResponseObject {
	now := time.Now()

	respObj := &ResponseObject{
		ID:        resp.ID,
		Object:    "response",
		Status:    "completed",
		CreatedAt: now.Unix(),
		Model:     model,
		Usage:     toResponseUsage(resp.Usage),
	}

	// Convert choices to output format
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		output := []interface{}{}

		// Add message output — content must be []interface{} of content parts, matching native format
		msgOutput := map[string]interface{}{
			"type":   "message",
			"role":   "assistant",
			"status": "completed",
			"content": []interface{}{
				map[string]interface{}{
					"type":        "output_text",
					"text":        choice.Message.GetContentAsString(),
					"annotations": []interface{}{},
				},
			},
		}
		output = append(output, msgOutput)

		// Add tool calls if any
		for _, tc := range choice.Message.ToolCalls {
			toolCallOutput := map[string]interface{}{
				"type":      "function_call",
				"id":        tc.ID,
				"call_id":   tc.ID,
				"name":      tc.Function.Name,
				"arguments": tc.Function.Arguments,
			}
			output = append(output, toolCallOutput)
		}

		respObj.Output = output
	}

	return respObj
}
