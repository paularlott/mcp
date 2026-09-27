package claude

import (
	"testing"

	"github.com/paularlott/mcp/ai/openai"
)

func basicCachingRequest() openai.ChatCompletionRequest {
	return openai.ChatCompletionRequest{
		Model: "claude-test",
		Tools: []openai.Tool{
			{Type: "function", Function: openai.ToolFunction{Name: "get_weather", Parameters: map[string]any{"type": "object"}}},
			{Type: "function", Function: openai.ToolFunction{Name: "get_time", Parameters: map[string]any{"type": "object"}}},
		},
		Messages: []openai.Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "What's the weather?"},
			{Role: "assistant", Content: "Let me check."},
			{Role: "user", Content: "Thanks!"},
		},
	}
}

// With prompt caching on (the default), the system prompt must switch to
// the block form carrying cache_control — a plain string can't carry it.
func TestConvertToClaudeRequest_PromptCachingMarksSystemPrompt(t *testing.T) {
	client := &Client{promptCaching: true}
	got := client.convertToClaudeRequest(basicCachingRequest())

	if got.System.text != "You are a helpful assistant." {
		t.Fatalf("System.text = %q, want the original prompt text preserved", got.System.text)
	}
	if !got.System.cacheControl {
		t.Fatal("System.cacheControl = false, want true when promptCaching is enabled")
	}

	b, err := got.System.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	want := `[{"type":"text","text":"You are a helpful assistant.","cache_control":{"type":"ephemeral"}}]`
	if string(b) != want {
		t.Fatalf("System MarshalJSON = %s, want %s", b, want)
	}
}

// Only the LAST tool gets a breakpoint — that's what caches the whole tools
// array as one segment; marking every tool would be redundant and wasteful
// of the 4-breakpoint budget Anthropic allows per request.
func TestConvertToClaudeRequest_PromptCachingMarksOnlyLastTool(t *testing.T) {
	client := &Client{promptCaching: true}
	got := client.convertToClaudeRequest(basicCachingRequest())

	if len(got.Tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(got.Tools))
	}
	if got.Tools[0].CacheControl != nil {
		t.Fatalf("first tool CacheControl = %+v, want nil", got.Tools[0].CacheControl)
	}
	if got.Tools[1].CacheControl == nil || got.Tools[1].CacheControl.Type != "ephemeral" {
		t.Fatalf("last tool CacheControl = %+v, want {Type: ephemeral}", got.Tools[1].CacheControl)
	}
}

// Only the last content block of the last message gets a breakpoint. This
// is deliberately re-derived from scratch on every call (not tracked
// across calls): cache lookups match the longest cached prefix, so as the
// conversation grows by strict append, each call's breakpoint is
// automatically a superset of the previous call's.
func TestConvertToClaudeRequest_PromptCachingMarksOnlyLastMessage(t *testing.T) {
	client := &Client{promptCaching: true}
	got := client.convertToClaudeRequest(basicCachingRequest())

	if len(got.Messages) != 3 { // system message extracted, 3 remain
		t.Fatalf("got %d messages, want 3", len(got.Messages))
	}
	for i, msg := range got.Messages[:len(got.Messages)-1] {
		for _, b := range msg.Content.blocks {
			if b.CacheControl != nil {
				t.Fatalf("message %d block has CacheControl = %+v, want nil (only the last message should be marked)", i, b.CacheControl)
			}
		}
	}
	lastBlocks := got.Messages[len(got.Messages)-1].Content.blocks
	if len(lastBlocks) == 0 {
		t.Fatal("last message has no content blocks")
	}
	last := lastBlocks[len(lastBlocks)-1]
	if last.CacheControl == nil || last.CacheControl.Type != "ephemeral" {
		t.Fatalf("last message's last block CacheControl = %+v, want {Type: ephemeral}", last.CacheControl)
	}
}

// Explicitly disabling PromptCaching must produce byte-for-byte the same
// request shape as before this feature existed: a plain string system
// field, and no cache_control anywhere.
func TestConvertToClaudeRequest_PromptCachingDisabled(t *testing.T) {
	client := &Client{promptCaching: false}
	got := client.convertToClaudeRequest(basicCachingRequest())

	b, err := got.System.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if string(b) != `"You are a helpful assistant."` {
		t.Fatalf("System MarshalJSON = %s, want a plain JSON string", b)
	}
	for _, tool := range got.Tools {
		if tool.CacheControl != nil {
			t.Fatalf("tool %q has CacheControl = %+v, want nil when caching is disabled", tool.Name, tool.CacheControl)
		}
	}
	for _, msg := range got.Messages {
		for _, b := range msg.Content.blocks {
			if b.CacheControl != nil {
				t.Fatalf("message block has CacheControl = %+v, want nil when caching is disabled", b.CacheControl)
			}
		}
	}
}

// New's default (no explicit Config.PromptCaching) must be caching ON.
func TestNew_PromptCachingDefaultsToEnabled(t *testing.T) {
	client, err := New(openai.Config{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !client.promptCaching {
		t.Fatal("promptCaching = false, want true by default")
	}
}

func TestNew_PromptCachingCanBeDisabled(t *testing.T) {
	client, err := New(openai.Config{APIKey: "test-key", PromptCaching: boolPtr(false)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client.promptCaching {
		t.Fatal("promptCaching = true, want false when explicitly disabled")
	}
}

// No tools and no messages must not panic (both breakpoints are guarded).
func TestApplyCacheBreakpoints_EmptyRequestDoesNotPanic(t *testing.T) {
	req := &ClaudeRequest{}
	applyCacheBreakpoints(req)
}
