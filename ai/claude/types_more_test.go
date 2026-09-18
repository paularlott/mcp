package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/paularlott/mcp/ai/openai"
)

// --- SystemField ---

func TestSystemFieldUnmarshalJSON_String(t *testing.T) {
	var sf SystemField
	if err := json.Unmarshal([]byte(`"be helpful"`), &sf); err != nil {
		t.Fatalf("UnmarshalJSON error: %v", err)
	}
	if sf.text != "be helpful" {
		t.Errorf("text = %q, want %q", sf.text, "be helpful")
	}
}

func TestSystemFieldUnmarshalJSON_ContentBlocks(t *testing.T) {
	var sf SystemField
	data := `[{"type":"text","text":"Part 1. "},{"type":"text","text":"Part 2."}]`
	if err := json.Unmarshal([]byte(data), &sf); err != nil {
		t.Fatalf("UnmarshalJSON error: %v", err)
	}
	if sf.text != "Part 1. Part 2." {
		t.Errorf("text = %q, want %q", sf.text, "Part 1. Part 2.")
	}
}

func TestSystemFieldUnmarshalJSON_NonTextBlocksIgnored(t *testing.T) {
	var sf SystemField
	data := `[{"type":"text","text":"kept"},{"type":"image"}]`
	if err := json.Unmarshal([]byte(data), &sf); err != nil {
		t.Fatalf("UnmarshalJSON error: %v", err)
	}
	if sf.text != "kept" {
		t.Errorf("text = %q, want %q", sf.text, "kept")
	}
}

func TestSystemFieldUnmarshalJSON_Invalid(t *testing.T) {
	var sf SystemField
	if err := json.Unmarshal([]byte(`123`), &sf); err == nil {
		t.Fatal("expected error unmarshalling a bare number into SystemField")
	}
}

func TestSystemFieldMarshalJSON(t *testing.T) {
	sf := SystemField{text: "hello"}
	b, err := json.Marshal(sf)
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}
	if string(b) != `"hello"` {
		t.Errorf("Marshal = %s, want %q", b, `"hello"`)
	}
}

func TestSystemFieldString(t *testing.T) {
	sf := SystemField{text: "the system prompt"}
	if got := sf.String(); got != "the system prompt" {
		t.Errorf("String() = %q, want %q", got, "the system prompt")
	}
}

// --- MessageContent ---

func TestMessageContentUnmarshalJSON_String(t *testing.T) {
	var mc MessageContent
	if err := json.Unmarshal([]byte(`"plain text"`), &mc); err != nil {
		t.Fatalf("UnmarshalJSON error: %v", err)
	}
	blocks := mc.Blocks()
	if len(blocks) != 1 || blocks[0].Type != "text" || blocks[0].Text != "plain text" {
		t.Errorf("Blocks() = %+v", blocks)
	}
}

func TestMessageContentUnmarshalJSON_Blocks(t *testing.T) {
	var mc MessageContent
	data := `[{"type":"text","text":"hi"},{"type":"tool_use","id":"call_1","name":"f","input":{"a":1}}]`
	if err := json.Unmarshal([]byte(data), &mc); err != nil {
		t.Fatalf("UnmarshalJSON error: %v", err)
	}
	blocks := mc.Blocks()
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d: %+v", len(blocks), blocks)
	}
	if blocks[1].Type != "tool_use" || blocks[1].Name != "f" {
		t.Errorf("unexpected second block: %+v", blocks[1])
	}
}

func TestMessageContentUnmarshalJSON_Invalid(t *testing.T) {
	var mc MessageContent
	if err := json.Unmarshal([]byte(`123`), &mc); err == nil {
		t.Fatal("expected error unmarshalling a bare number into MessageContent")
	}
}

func TestMessageContentMarshalJSON(t *testing.T) {
	mc := MessageContent{blocks: []ContentBlock{{Type: "text", Text: "hi"}}}
	b, err := json.Marshal(mc)
	if err != nil {
		t.Fatalf("MarshalJSON error: %v", err)
	}
	if !strings.Contains(string(b), `"text":"hi"`) {
		t.Errorf("Marshal = %s", b)
	}
}

// --- claudeImageBlockFromDataURL (exercised via convertMessages) ---

func TestConvertMessages_ImageBlocks(t *testing.T) {
	client := &Client{}

	msgs := []openai.Message{
		openai.BuildMultimodalMessage(
			openai.TextContentPart("look at this: "),
			openai.ImageBase64ContentPart("YWJj", "image/png", ""),
			openai.ImageURLContentPart("https://example.com/cat.png", ""),
		),
	}

	got := client.convertMessages(msgs)
	if len(got) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got))
	}
	blocks := got[0].Content.Blocks()
	if len(blocks) != 3 {
		t.Fatalf("expected 3 blocks (text, image, image), got %d: %+v", len(blocks), blocks)
	}
	if blocks[0].Type != "text" || blocks[0].Text != "look at this: " {
		t.Errorf("block0 = %+v", blocks[0])
	}
	if blocks[1].Type != "image" || blocks[1].Source == nil || blocks[1].Source.Type != "base64" ||
		blocks[1].Source.MediaType != "image/png" || blocks[1].Source.Data != "YWJj" {
		t.Errorf("block1 = %+v", blocks[1])
	}
	if blocks[2].Type != "image" || blocks[2].Source == nil || blocks[2].Source.Type != "url" ||
		blocks[2].Source.URL != "https://example.com/cat.png" {
		t.Errorf("block2 = %+v", blocks[2])
	}
}

func TestConvertMessages_ImageOnlyDataURLWithoutBase64Marker(t *testing.T) {
	client := &Client{}
	// A data: URL without the ";base64," marker should fall through to the
	// generic URL branch of claudeImageBlockFromDataURL.
	msgs := []openai.Message{
		openai.BuildMultimodalMessage(
			openai.ImageURLContentPart("data:image/png,rawnobase64", ""),
		),
	}
	got := client.convertMessages(msgs)
	blocks := got[0].Content.Blocks()
	if len(blocks) != 1 || blocks[0].Source.Type != "url" || blocks[0].Source.URL != "data:image/png,rawnobase64" {
		t.Errorf("unexpected block: %+v", blocks[0])
	}
}

// --- MessagesRequestToOpenAI edge cases ---

func TestMessagesRequestToOpenAI_ImageBlocks(t *testing.T) {
	req := &MessagesRequest{
		Model: "test-model",
		Messages: []ClaudeMessage{
			{
				Role: "user",
				Content: MessageContent{blocks: []ContentBlock{
					{Type: "text", Text: "look: "},
					{Type: "image", Source: &ImageSource{Type: "base64", MediaType: "image/jpeg", Data: "abc123"}},
					{Type: "image", Source: &ImageSource{Type: "url", URL: "https://x/y.png"}},
				}},
			},
		},
	}
	got := MessagesRequestToOpenAI(req)
	if len(got.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got.Messages))
	}
	parts := got.Messages[0].GetContentAsParts()
	if len(parts) != 3 {
		t.Fatalf("expected 3 content parts, got %d: %+v", len(parts), parts)
	}
	if parts[1].ImageURL == nil || !strings.Contains(parts[1].ImageURL.URL, "base64,abc123") {
		t.Errorf("unexpected image part: %+v", parts[1])
	}
	if parts[2].ImageURL == nil || parts[2].ImageURL.URL != "https://x/y.png" {
		t.Errorf("unexpected image part: %+v", parts[2])
	}
}

func TestMessagesRequestToOpenAI_EmptyMessageSkipped(t *testing.T) {
	req := &MessagesRequest{
		Model: "test-model",
		Messages: []ClaudeMessage{
			{Role: "user", Content: MessageContent{blocks: []ContentBlock{{Type: "text", Text: ""}}}},
		},
	}
	got := MessagesRequestToOpenAI(req)
	if len(got.Messages) != 0 {
		t.Errorf("expected empty-content message to be skipped, got %+v", got.Messages)
	}
}

func TestMessagesRequestToOpenAI_ToolResultWithContentParts(t *testing.T) {
	req := &MessagesRequest{
		Model: "test-model",
		Messages: []ClaudeMessage{
			{
				Role: "user",
				Content: MessageContent{blocks: []ContentBlock{
					{Type: "tool_result", ToolUseID: "call_1", Content: []any{
						map[string]any{"type": "text", "text": "part a "},
						map[string]any{"type": "text", "text": "part b"},
					}},
				}},
			},
		},
	}
	got := MessagesRequestToOpenAI(req)
	if len(got.Messages) != 1 || got.Messages[0].Content != "part a part b" {
		t.Errorf("unexpected messages: %+v", got.Messages)
	}
}

func TestMessagesRequestToOpenAI_TopPPassthrough(t *testing.T) {
	topP := 0.42
	req := &MessagesRequest{
		Model: "test-model",
		TopP:  &topP,
		Messages: []ClaudeMessage{
			{Role: "user", Content: MessageContent{blocks: []ContentBlock{{Type: "text", Text: "hi"}}}},
		},
	}
	got := MessagesRequestToOpenAI(req)
	if got.TopP == nil || *got.TopP != 0.42 {
		t.Errorf("TopP = %v, want 0.42", got.TopP)
	}
}

func TestMessagesRequestToOpenAI_ToolResultMixedWithOtherBlocks(t *testing.T) {
	// A message carrying both a tool_result block and a non-tool_result block
	// (e.g. text) must skip the non-tool_result block when splitting tool
	// results into separate messages.
	req := &MessagesRequest{
		Model: "test-model",
		Messages: []ClaudeMessage{
			{
				Role: "user",
				Content: MessageContent{blocks: []ContentBlock{
					{Type: "text", Text: "ignored alongside tool_result"},
					{Type: "tool_result", ToolUseID: "call_1", Content: "Result 1"},
				}},
			},
		},
	}
	got := MessagesRequestToOpenAI(req)
	if len(got.Messages) != 1 || got.Messages[0].Role != "tool" || got.Messages[0].Content != "Result 1" {
		t.Errorf("unexpected messages: %+v", got.Messages)
	}
}

func TestMessagesRequestToOpenAI_NoMaxTokensOrSamplingParams(t *testing.T) {
	req := &MessagesRequest{
		Model: "test-model",
		Messages: []ClaudeMessage{
			{Role: "user", Content: MessageContent{blocks: []ContentBlock{{Type: "text", Text: "hi"}}}},
		},
	}
	got := MessagesRequestToOpenAI(req)
	if got.MaxTokens != 0 {
		t.Errorf("MaxTokens = %d, want 0", got.MaxTokens)
	}
	if got.Temperature != nil || got.TopP != nil {
		t.Errorf("expected nil sampling params, got Temperature=%v TopP=%v", got.Temperature, got.TopP)
	}
}

// --- OpenAIToMessagesResponse edge cases ---

func TestOpenAIToMessagesResponse_UnknownFinishReason(t *testing.T) {
	resp := &openai.ChatCompletionResponse{
		ID:    "id1",
		Model: "m",
		Choices: []openai.Choice{
			{Message: openai.Message{Content: "hi"}, FinishReason: "content_filter"},
		},
	}
	got := OpenAIToMessagesResponse(resp)
	if got.StopReason != "content_filter" {
		t.Errorf("StopReason = %q, want %q", got.StopReason, "content_filter")
	}
}

func TestOpenAIToMessagesResponse_LengthFinishReason(t *testing.T) {
	resp := &openai.ChatCompletionResponse{
		ID: "id1", Model: "m",
		Choices: []openai.Choice{{Message: openai.Message{Content: "cut off"}, FinishReason: "length"}},
	}
	got := OpenAIToMessagesResponse(resp)
	if got.StopReason != "max_tokens" {
		t.Errorf("StopReason = %q, want %q", got.StopReason, "max_tokens")
	}
}

func TestOpenAIToMessagesResponse_EmptyContentDefaultsToEmptyTextBlock(t *testing.T) {
	resp := &openai.ChatCompletionResponse{
		ID: "id1", Model: "m",
		Choices: []openai.Choice{{Message: openai.Message{}, FinishReason: "stop"}},
	}
	got := OpenAIToMessagesResponse(resp)
	if len(got.Content) != 1 || got.Content[0].Type != "text" || got.Content[0].Text != "" {
		t.Errorf("expected a single empty text block, got: %+v", got.Content)
	}
}

func TestOpenAIToMessagesResponse_NoChoices(t *testing.T) {
	resp := &openai.ChatCompletionResponse{ID: "id1", Model: "m"}
	got := OpenAIToMessagesResponse(resp)
	if got.StopReason != "" || len(got.Content) != 0 {
		t.Errorf("unexpected result for empty choices: %+v", got)
	}
}

// --- WriteSSEEvent ---

func TestWriteSSEEvent(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteSSEEvent(&buf, "message_stop", map[string]string{"type": "message_stop"}); err != nil {
		t.Fatalf("WriteSSEEvent error: %v", err)
	}
	got := buf.String()
	if !strings.HasPrefix(got, "event: message_stop\n") {
		t.Errorf("unexpected output: %q", got)
	}
	if !strings.Contains(got, `"type":"message_stop"`) {
		t.Errorf("expected data payload in output: %q", got)
	}
	if !strings.HasSuffix(got, "\n\n") {
		t.Errorf("expected trailing blank line, got: %q", got)
	}
}

func TestWriteSSEEvent_MarshalError(t *testing.T) {
	var buf bytes.Buffer
	// func values cannot be marshalled to JSON.
	err := WriteSSEEvent(&buf, "bad", func() {})
	if err == nil {
		t.Fatal("expected marshal error")
	}
}

// --- StreamOpenAIToMessages ---

func newTestChatStream(chunks []openai.ChatCompletionResponse, streamErr error) *openai.ChatStream {
	responseChan := make(chan openai.ChatCompletionResponse, len(chunks)+1)
	errorChan := make(chan error, 1)
	for _, c := range chunks {
		responseChan <- c
	}
	close(responseChan)
	if streamErr != nil {
		errorChan <- streamErr
	}
	close(errorChan)
	return openai.NewChatStream(context.Background(), responseChan, errorChan)
}

func TestStreamOpenAIToMessages_TextAndToolCalls(t *testing.T) {
	chunks := []openai.ChatCompletionResponse{
		{ID: "chat_1", Model: "gpt-4", Usage: &openai.Usage{PromptTokens: 7}, Choices: []openai.Choice{{Delta: openai.Delta{Content: "Hello "}}}},
		{Choices: []openai.Choice{{Delta: openai.Delta{Content: "world"}}}},
		{Choices: []openai.Choice{{Delta: openai.Delta{ToolCalls: []openai.DeltaToolCall{
			{Index: 0, ID: "call_1", Type: "function", Function: openai.DeltaFunction{Name: "get_weather"}},
		}}}}},
		{Choices: []openai.Choice{{Delta: openai.Delta{ToolCalls: []openai.DeltaToolCall{
			{Index: 0, Function: openai.DeltaFunction{Arguments: `{"loc":"NYC"}`}},
		}}}}},
		{Choices: []openai.Choice{{FinishReason: "tool_calls"}}, Usage: &openai.Usage{CompletionTokens: 12}},
	}
	stream := newTestChatStream(chunks, nil)

	var buf bytes.Buffer
	flushes := 0
	err := StreamOpenAIToMessages(&buf, func() { flushes++ }, stream, "claude-model")
	if err != nil {
		t.Fatalf("StreamOpenAIToMessages error: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"event: message_start",
		`"id":"chat_1"`,
		`"input_tokens":7`,
		"event: content_block_start",
		`"content_block":{"type":"text"}`,
		"event: content_block_delta",
		`"text":"Hello "`,
		`"text":"world"`,
		`"type":"tool_use"`,
		`"name":"get_weather"`,
		`"partial_json":"{\"loc\":\"NYC\"}"`,
		"event: content_block_stop",
		"event: message_delta",
		`"stop_reason":"tool_use"`,
		"event: message_stop",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, out)
		}
	}
	if flushes == 0 {
		t.Error("expected flush to be called at least once")
	}
}

func TestStreamOpenAIToMessages_MaxTokensFinish(t *testing.T) {
	chunks := []openai.ChatCompletionResponse{
		{ID: "chat_2", Choices: []openai.Choice{{Delta: openai.Delta{Content: "partial"}}}},
		{Choices: []openai.Choice{{FinishReason: "length"}}},
	}
	stream := newTestChatStream(chunks, nil)

	var buf bytes.Buffer
	if err := StreamOpenAIToMessages(&buf, func() {}, stream, "m"); err != nil {
		t.Fatalf("StreamOpenAIToMessages error: %v", err)
	}
	if !strings.Contains(buf.String(), `"stop_reason":"max_tokens"`) {
		t.Errorf("expected max_tokens stop reason, got: %s", buf.String())
	}
}

func TestStreamOpenAIToMessages_NoChoicesChunkSkipped(t *testing.T) {
	chunks := []openai.ChatCompletionResponse{
		{ID: "chat_3", Choices: []openai.Choice{}},
		{Choices: []openai.Choice{{Delta: openai.Delta{Content: "ok"}, FinishReason: "stop"}}},
	}
	stream := newTestChatStream(chunks, nil)

	var buf bytes.Buffer
	if err := StreamOpenAIToMessages(&buf, func() {}, stream, "m"); err != nil {
		t.Fatalf("StreamOpenAIToMessages error: %v", err)
	}
	if !strings.Contains(buf.String(), `"stop_reason":"end_turn"`) {
		t.Errorf("expected end_turn stop reason, got: %s", buf.String())
	}
}

func TestStreamOpenAIToMessages_PropagatesStreamError(t *testing.T) {
	stream := newTestChatStream(nil, context.DeadlineExceeded)

	var buf bytes.Buffer
	err := StreamOpenAIToMessages(&buf, func() {}, stream, "m")
	if err == nil {
		t.Fatal("expected StreamOpenAIToMessages to propagate the stream error")
	}
}
