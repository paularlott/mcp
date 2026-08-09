package ollama

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/paularlott/mcp/ai/openai"
)

// convertMessages turns OpenAI messages into Ollama messages, pulling images
// out of multimodal content parts into Ollama's top-level images field.
func (c *Client) convertMessages(messages []openai.Message) []message {
	out := make([]message, 0, len(messages))
	for _, m := range messages {
		om := message{Role: m.Role}
		text, images := splitContent(m)
		om.Content = text
		om.Images = images
		if len(m.ToolCalls) > 0 {
			om.ToolCalls = toolCallsFromOpenAI(m.ToolCalls)
		}
		// OpenAI echoes the tool result into a "tool"-role message whose
		// content is the result; Ollama expects role "tool" too, with content.
		out = append(out, om)
	}
	return out
}

// splitContent extracts plain text and base64 images from an OpenAI message's
// content (string or content-parts array). Remote http(s) image URLs are
// fetched once and re-encoded as base64, since Ollama only accepts inline data.
func splitContent(m openai.Message) (string, []string) {
	if m.Content == nil {
		return "", nil
	}
	if s, ok := m.Content.(string); ok {
		return s, nil
	}
	parts := m.GetContentAsParts()
	if parts == nil {
		return "", nil
	}
	var sb strings.Builder
	var images []string
	for _, p := range parts {
		switch p.Type {
		case "text":
			sb.WriteString(p.Text)
		case "image_url":
			if p.ImageURL != nil {
				if b64, ok := dataURIToBase64(p.ImageURL.URL); ok {
					images = append(images, b64)
				} else if b64, err := fetchImageBase64(p.ImageURL.URL); err == nil {
					images = append(images, b64)
				}
			}
		}
	}
	return sb.String(), images
}

// dataURIToBase64 returns the base64 payload of a data: URI, true on success.
func dataURIToBase64(s string) (string, bool) {
	if !strings.HasPrefix(s, "data:") {
		return "", false
	}
	// data:image/png;base64,XXXX
	sep := strings.Index(s, ",")
	if sep < 0 {
		return "", false
	}
	return s[sep+1:], true
}

func fetchImageBase64(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("image fetch returned %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func toolCallsFromOpenAI(calls []openai.ToolCall) []toolCall {
	out := make([]toolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, toolCall{
			Function: toolCallFunction{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		})
	}
	return out
}

func convertTools(tools []openai.Tool) []tool {
	out := make([]tool, 0, len(tools))
	for _, t := range tools {
		typ := t.Type
		if typ == "" {
			typ = "function"
		}
		out = append(out, tool{
			Type: typ,
			Function: toolDefinition{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
			},
		})
	}
	return out
}

// buildChatRequest assembles the Ollama chat body from an OpenAI request.
func (c *Client) buildChatRequest(req openai.ChatCompletionRequest, stream bool) chatRequest {
	opts := options{}
	if req.Temperature != nil {
		opts["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		opts["top_p"] = *req.TopP
	}
	if mt := effectiveMaxTokens(req); mt > 0 {
		opts["num_predict"] = mt
	}
	if req.ExtraBody != nil {
		if v, ok := req.ExtraBody["options"]; ok {
			if m, ok := v.(map[string]any); ok {
				for k, val := range m {
					opts[k] = val
				}
			}
		}
	}

	out := chatRequest{
		Model:    req.Model,
		Messages: c.convertMessages(req.Messages),
		Stream:   stream,
		Options:  opts,
	}
	if len(req.Tools) > 0 {
		out.Tools = convertTools(req.Tools)
	}
	return out
}

func effectiveMaxTokens(req openai.ChatCompletionRequest) int {
	if req.MaxCompletionTokens > 0 {
		return req.MaxCompletionTokens
	}
	return req.MaxTokens
}

// finishReason maps Ollama's done_reason to OpenAI's finish_reason.
func finishReason(doneReason string) string {
	switch doneReason {
	case "stop", "length", "tool_calls":
		return doneReason
	case "load":
		return "stop"
	case "":
		return "stop"
	default:
		return doneReason
	}
}

// chatResponseToOpenAI converts a non-streaming Ollama chat response into an
// OpenAI chat completion response.
func chatResponseToOpenAI(resp *chatResponse) *openai.ChatCompletionResponse {
	out := &openai.ChatCompletionResponse{
		ID:      resp.Model, // Ollama gives no id; reuse model
		Object:  "chat.completion",
		Model:   resp.Model,
		Choices: []openai.Choice{{Index: 0, FinishReason: finishReason(resp.DoneReason)}},
	}
	msg := openai.Message{Role: "assistant"}
	msg.SetContentAsString(resp.Message.Content)
	if len(resp.Message.ToolCalls) > 0 {
		msg.ToolCalls = toolCallsToOpenAI(resp.Message.ToolCalls)
		if msg.Content == "" && len(msg.ToolCalls) > 0 {
			out.Choices[0].FinishReason = "tool_calls"
		}
	}
	out.Choices[0].Message = msg
	if resp.PromptEvalCount > 0 || resp.EvalCount > 0 {
		out.Usage = &openai.Usage{
			PromptTokens:     resp.PromptEvalCount,
			CompletionTokens: resp.EvalCount,
			TotalTokens:      resp.PromptEvalCount + resp.EvalCount,
		}
	}
	return out
}

func toolCallsToOpenAI(calls []toolCall) []openai.ToolCall {
	out := make([]openai.ToolCall, 0, len(calls))
	for i, call := range calls {
		out = append(out, openai.ToolCall{
			Index: i,
			ID:    call.Function.Name, // Ollama carries no tool-call id
			Type:  "function",
			Function: openai.ToolCallFunction{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		})
	}
	return out
}

// streamChunkToOpenAI converts one streamed Ollama chat object into an OpenAI
// chat.completion.chunk. Returns nil for chunks that produce no OpenAI delta
// (e.g. the terminal usage-only object when there is no usage to report).
func streamChunkToOpenAI(model string, resp *chatResponse) *openai.ChatCompletionResponse {
	chunk := &openai.ChatCompletionResponse{
		ID:     model,
		Object: "chat.completion.chunk",
		Model:  model,
		Choices: []openai.Choice{{
			Index: 0,
			Delta: openai.Delta{},
		}},
	}

	// First assistant chunk: announce the role.
	chunk.Choices[0].Delta.Role = "assistant"

	if resp.Message.Content != "" {
		chunk.Choices[0].Delta.Content = resp.Message.Content
	}
	if len(resp.Message.ToolCalls) > 0 {
		deltas := make([]openai.DeltaToolCall, 0, len(resp.Message.ToolCalls))
		for i, call := range resp.Message.ToolCalls {
			args, _ := json.Marshal(call.Function.Arguments)
			deltas = append(deltas, openai.DeltaToolCall{
				Index: i,
				ID:    call.Function.Name,
				Type:  "function",
				Function: openai.DeltaFunction{
					Name:      call.Function.Name,
					Arguments: string(args),
				},
			})
		}
		chunk.Choices[0].Delta.ToolCalls = deltas
	}

	if resp.Done {
		chunk.Choices[0].FinishReason = finishReason(resp.DoneReason)
		if resp.PromptEvalCount > 0 || resp.EvalCount > 0 {
			chunk.Usage = &openai.Usage{
				PromptTokens:     resp.PromptEvalCount,
				CompletionTokens: resp.EvalCount,
				TotalTokens:      resp.PromptEvalCount + resp.EvalCount,
			}
		}
	}
	return chunk
}
