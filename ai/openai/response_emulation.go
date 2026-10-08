package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
//
// Responses are stored in manager unless req.Store is false, in which case
// nothing is kept and the response can't be retrieved or continued.
func CreateResponseEmulated(ctx context.Context, completer ChatCompleter, manager *ResponseManager, req CreateResponseRequest) (*ResponseObject, error) {
	if req.Background {
		return createResponseBackground(ctx, completer, manager, req)
	}
	return createResponseSync(ctx, completer, manager, req)
}

// storeRequested reports whether a request allows storing its response.
func storeRequested(req CreateResponseRequest) bool {
	return req.Store == nil || *req.Store
}

// createResponseBackground creates an async response that processes in background
func createResponseBackground(ctx context.Context, completer ChatCompleter, manager *ResponseManager, req CreateResponseRequest) (*ResponseObject, error) {
	if !storeRequested(req) {
		return nil, fmt.Errorf("background responses must be stored: store cannot be false")
	}
	chatReq, conv, err := emulatedChatRequest(ctx, manager, req)
	if err != nil {
		return nil, err
	}

	// Detached from the caller's cancellation (the call returns at once),
	// keeping its values, e.g. the tool handler
	asyncCtx, cancel := detachedContext(ctx, manager.backgroundTimeout)

	// Create response state
	state, err := manager.begin(ctx, cancel, req.Model, conv)
	if err != nil {
		cancel()
		return nil, err
	}

	// Start async processing
	go processResponseAsync(asyncCtx, state, chatReq, req.Model, completer)

	// Return immediately with in_progress status
	return &ResponseObject{
		ID:        state.ID,
		Object:    "response",
		Status:    "in_progress",
		CreatedAt: time.Now().Unix(),
		Model:     req.Model,
	}, nil
}

// createResponseSync processes the response synchronously and stores it so
// it can be retrieved or continued by ID afterwards.
func createResponseSync(ctx context.Context, completer ChatCompleter, manager *ResponseManager, req CreateResponseRequest) (*ResponseObject, error) {
	chatReq, conv, err := emulatedChatRequest(ctx, manager, req)
	if err != nil {
		return nil, err
	}

	// Use the completer's ChatCompletion which handles tools automatically
	chatResp, err := completer.ChatCompletion(ctx, chatReq)
	if err != nil {
		return nil, err
	}

	respObj := ConvertChatToResponseObject(chatResp, req.Model)
	respObj.ID = generateID()
	if storeRequested(req) {
		manager.saveCompleted(ctx, respObj, req.Model, conv, replyMessage(chatResp))
	}
	return respObj, nil
}

// replyMessage returns the assistant message of a chat response.
func replyMessage(chatResp *ChatCompletionResponse) Message {
	reply := Message{Role: "assistant"}
	if chatResp != nil && len(chatResp.Choices) > 0 {
		reply = chatResp.Choices[0].Message
		reply.Role = "assistant"
	}
	return reply
}

// emulatedChatRequest converts a Responses request into a chat request whose
// messages are the instructions (if any) followed by the conversation: the
// stored history of PreviousResponseID, if set, then the new input.
func emulatedChatRequest(ctx context.Context, manager *ResponseManager, req CreateResponseRequest) (ChatCompletionRequest, *emulatedConversation, error) {
	conv, err := manager.conversation(ctx, req.PreviousResponseID, req.Input)
	if err != nil {
		return ChatCompletionRequest{}, nil, err
	}
	chatReq, err := ConvertResponseToChatRequest(req)
	if err != nil {
		return ChatCompletionRequest{}, nil, err
	}
	chatReq.Messages = nil
	if req.Instructions != "" {
		chatReq.Messages = append(chatReq.Messages, Message{Role: "system", Content: req.Instructions})
	}
	chatReq.Messages = append(chatReq.Messages, conv.messages()...)
	return chatReq, conv, nil
}

// GetResponseEmulated retrieves a response by ID (blocking until complete or error)
func GetResponseEmulated(ctx context.Context, manager *ResponseManager, id string) (*ResponseObject, error) {
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		state, ok := manager.Get(id)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrResponseNotFound, id)
		}
		state.RLock()
		status, result, err := state.Status, state.Result, state.Error
		state.RUnlock()

		switch {
		case isInProgress(status):
			// wait below
		case status == StatusCancelled:
			// Cancelled responses have no result; return a minimal cancelled object
			// (matching the native API which returns the response in cancelled state).
			return &ResponseObject{
				ID:        id,
				Object:    "response",
				Status:    "cancelled",
				CreatedAt: state.created_at.Unix(),
				Model:     state.model,
			}, nil
		case err != nil:
			return nil, err
		case result == nil:
			return nil, fmt.Errorf("response completed but result is nil")
		default:
			return result, nil
		}

		select {
		case <-ticker.C:
		case <-timeout.C:
			return nil, fmt.Errorf("timeout waiting for response")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// CancelResponseEmulated cancels an in-progress response
func CancelResponseEmulated(ctx context.Context, manager *ResponseManager, id string) (*ResponseObject, error) {
	if err := manager.cancelResponse(ctx, id); err != nil {
		return nil, err
	}
	return GetResponseEmulated(ctx, manager, id)
}

// DeleteResponseEmulated deletes an in-progress or completed response
func DeleteResponseEmulated(ctx context.Context, manager *ResponseManager, id string) error {
	return manager.deleteResponse(ctx, id)
}

// compactionPrompt asks the model to summarise a conversation transcript so
// the summary can stand in for it.
const compactionPrompt = `Summarise the conversation transcript below so that it can replace the transcript as context for continuing the conversation. Keep every fact, name, decision, open question, tool result and user preference that later turns may need; drop pleasantries and repetition. Write the summary in the third person, as plain prose or bullet points, with no preamble.`

// CompactResponseEmulated compacts a conversation by having the model
// summarise it. The conversation is the stored history of
// req.PreviousResponseID (if set) followed by req.Input. The returned output
// holds a single user message carrying the summary; pass it as the input of
// the next request in place of the compacted conversation.
func CompactResponseEmulated(ctx context.Context, completer ChatCompleter, manager *ResponseManager, req CompactResponseRequest) (*CompactedResponse, error) {
	if req.Model == "" {
		return nil, fmt.Errorf("model is required")
	}
	conv, err := manager.conversation(ctx, req.PreviousResponseID, req.Input)
	if err != nil {
		return nil, err
	}
	conversation := conv.messages()
	if len(conversation) == 0 {
		return nil, fmt.Errorf("nothing to compact: input and previous_response_id are both empty")
	}

	system := compactionPrompt
	if req.Instructions != "" {
		system += "\n\nThe assistant in this conversation was working under these instructions:\n" + req.Instructions
	}
	chatReq := ChatCompletionRequest{
		Model: req.Model,
		Messages: []Message{
			{Role: "system", Content: system},
			{Role: "user", Content: renderTranscript(conversation)},
		},
	}
	chatResp, err := completer.ChatCompletion(WithoutMCPTools(ctx), chatReq)
	if err != nil {
		return nil, fmt.Errorf("failed to compact conversation: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("failed to compact conversation: empty response")
	}

	summary := chatResp.Choices[0].Message.GetContentAsString()
	return &CompactedResponse{
		ID:        "cmp_" + strings.TrimPrefix(generateID(), "resp_"),
		Object:    "response.compaction",
		CreatedAt: time.Now().Unix(),
		Model:     req.Model,
		Output: []any{map[string]any{
			"type":    "message",
			"role":    "user",
			"content": "Summary of the earlier conversation:\n\n" + summary,
		}},
		Usage: toResponseUsage(chatResp.Usage),
	}, nil
}

// renderTranscript renders a conversation as plain text for summarisation,
// so tool calls and results need no provider-specific message structure.
func renderTranscript(conversation []Message) string {
	var b strings.Builder
	for _, msg := range conversation {
		text := msg.GetContentAsString()
		switch {
		case msg.Role == "tool":
			fmt.Fprintf(&b, "[tool result %s]\n%s\n\n", msg.ToolCallID, text)
		case len(msg.ToolCalls) > 0:
			if text != "" {
				fmt.Fprintf(&b, "[%s]\n%s\n\n", msg.Role, text)
			}
			for _, tc := range msg.ToolCalls {
				args, _ := json.Marshal(tc.Function.Arguments)
				fmt.Fprintf(&b, "[%s called tool %s (%s)]\n%s\n\n", msg.Role, tc.Function.Name, tc.ID, args)
			}
		default:
			fmt.Fprintf(&b, "[%s]\n%s\n\n", msg.Role, text)
		}
	}
	return strings.TrimSpace(b.String())
}

// processResponseAsync runs a background response's chat request and
// records the outcome on its state.
func processResponseAsync(ctx context.Context, state *ResponseState, chatReq ChatCompletionRequest, model string, completer ChatCompleter) {
	defer func() {
		if r := recover(); r != nil {
			state.SetError(fmt.Errorf("panic during response processing: %v", r))
		}
	}()

	// Use the completer's ChatCompletion which handles tools automatically
	chatResp, err := completer.ChatCompletion(ctx, chatReq)
	// If the response was cancelled while in flight, keep the cancelled status
	if state.GetStatus() == StatusCancelled {
		return
	}
	if err != nil {
		state.SetError(err)
		return
	}

	// Convert ChatCompletionResponse to ResponseObject, preserving the response
	// ID assigned at creation so callers can retrieve it by that ID.
	respObj := ConvertChatToResponseObject(chatResp, model)
	respObj.ID = state.ID

	reply := replyMessage(chatResp)
	state.Lock()
	state.reply = &reply
	state.Unlock()
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

	// Apply sampling parameters if specified
	if req.Temperature != nil {
		chatReq.Temperature = req.Temperature
	}
	if req.TopP != nil {
		chatReq.TopP = req.TopP
	}

	// Convert input to messages, with instructions as a leading system message
	if req.Instructions != "" {
		chatReq.Messages = append(chatReq.Messages, Message{Role: "system", Content: req.Instructions})
	}
	chatReq.Messages = append(chatReq.Messages, ConvertInputToMessages(req.Input)...)

	// Copy tools if provided
	if len(req.Tools) > 0 {
		chatReq.Tools = req.Tools
	}

	// Copy extra body for provider-specific fields
	chatReq.ExtraBody = req.ExtraBody

	return chatReq, nil
}

// ConvertInputToMessages converts Response API input to ChatCompletion messages
func ConvertInputToMessages(input []any) []Message {
	var messages []Message

	for _, item := range input {
		if itemMap, ok := item.(map[string]any); ok {
			itemType, _ := itemMap["type"].(string)
			// The type is optional on input messages: {"role": ..., "content": ...}
			if itemType == "" && getString(itemMap, "role") != "" {
				itemType = "message"
			}

			switch itemType {
			case "message", "user_message", "system_message", "assistant_message":
				msg := Message{
					Role: getRoleFromItemType(itemType, itemMap),
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

			case "function_call":
				// A prior assistant tool call, replayed as an assistant message
				// carrying tool_calls so the completions provider sees the turn.
				callID := getString(itemMap, "call_id")
				if callID == "" {
					callID = getString(itemMap, "id")
				}
				name := getString(itemMap, "name")
				argsRaw := getString(itemMap, "arguments")
				var args map[string]any
				if argsRaw != "" {
					_ = json.Unmarshal([]byte(argsRaw), &args)
				}
				if args == nil {
					args = map[string]any{}
				}
				messages = append(messages, Message{
					Role:      "assistant",
					ToolCalls: []ToolCall{{ID: callID, Type: "function", Function: ToolCallFunction{Name: name, Arguments: args}}},
				})
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

// getRoleFromItemType maps Response API item types to chat roles. For the
// canonical "message" type, the role is carried in the item's "role" field
// (user/assistant/system/developer); the typed variants imply a fixed role.
func getRoleFromItemType(itemType string, itemMap map[string]any) string {
	if itemType == "message" {
		if role := getString(itemMap, "role"); role != "" {
			return role
		}
		return "user"
	}
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
func getString(m map[string]any, key string) string {
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
		output := []any{}

		// Add message output — content must be []any of content parts, matching native format
		msgOutput := map[string]any{
			"type":   "message",
			"role":   "assistant",
			"status": "completed",
			"content": []any{
				map[string]any{
					"type":        "output_text",
					"text":        choice.Message.GetContentAsString(),
					"annotations": []any{},
				},
			},
		}
		// A reply that is only tool calls has no message item, as natively
		if choice.Message.GetContentAsString() != "" || len(choice.Message.ToolCalls) == 0 {
			output = append(output, msgOutput)
		}

		// Add tool calls if any
		for _, tc := range choice.Message.ToolCalls {
			// Native Responses API returns arguments as a JSON string.
			argsJSON, _ := ArgumentsJSON(tc.Function.Arguments)
			toolCallOutput := map[string]any{
				"type":      "function_call",
				"id":        tc.ID,
				"call_id":   tc.ID,
				"name":      tc.Function.Name,
				"arguments": string(argsJSON),
			}
			output = append(output, toolCallOutput)
		}

		respObj.Output = output
	}

	return respObj
}
