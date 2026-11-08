package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Title and the behaviour hints set on a ToolBuilder reach every tool
// descriptor: tools/list for native tools, show-all listing and tool_search
// results for discoverable ones, and ToMCPTool for providers.
func TestToolBuilderTitleAndAnnotations(t *testing.T) {
	ok := func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("ok"), nil
	}
	tr, fa := true, false
	want := &ToolAnnotations{ReadOnlyHint: &tr, DestructiveHint: &fa, IdempotentHint: &tr, OpenWorldHint: &fa}

	build := func(name string) *ToolBuilder {
		return NewTool(name, "d").Title("Nice " + name).ReadOnly().Destructive(false).Idempotent().OpenWorld(false)
	}
	s := NewServer("a", "1")
	s.RegisterTool(build("native"), ok)
	s.RegisterTool(build("hidden").Discoverable("hidden"), ok)

	find := func(tools []MCPTool, name string) *MCPTool {
		for i := range tools {
			if tools[i].Name == name {
				return &tools[i]
			}
		}
		return nil
	}
	check := func(where, title string, ann *ToolAnnotations, wantTitle string) {
		t.Helper()
		if title != wantTitle || !reflect.DeepEqual(ann, want) {
			t.Errorf("%s: title=%q annotations=%+v, want %q %+v", where, title, ann, wantTitle, want)
		}
	}

	if tl := find(s.ListToolsWithContext(context.Background()), "native"); tl == nil {
		t.Fatal("native tool not listed")
	} else {
		check("tools/list", tl.Title, tl.Annotations, "Nice native")
	}
	if tl := find(s.ListToolsWithContext(WithShowAllTools(context.Background())), "hidden"); tl == nil {
		t.Fatal("hidden tool not in show-all list")
	} else {
		check("show-all tools/list", tl.Title, tl.Annotations, "Nice hidden")
	}
	resp, err := s.CallTool(context.Background(), ToolSearchName, map[string]any{"query": "hidden"})
	if err != nil {
		t.Fatal(err)
	}
	var results []SearchResult
	if err := json.Unmarshal([]byte(resp.Content[0].Text), &results); err != nil || len(results) == 0 {
		t.Fatalf("tool_search results: %v %s", err, resp.Content[0].Text)
	}
	check("tool_search", results[0].Title, results[0].Annotations, "Nice hidden")
	mt := build("provided").ToMCPTool()
	check("ToMCPTool", mt.Title, mt.Annotations, "Nice provided")

	// On the wire: annotations is an object of the spec's hint names.
	b, _ := json.Marshal(find(s.ListToolsWithContext(context.Background()), "native"))
	for _, frag := range []string{`"title":"Nice native"`, `"readOnlyHint":true`, `"destructiveHint":false`, `"idempotentHint":true`, `"openWorldHint":false`} {
		if !strings.Contains(string(b), frag) {
			t.Errorf("wire form %s lacks %s", b, frag)
		}
	}
}

// A tool without hints sends no annotations object, and later builder calls
// don't change a tool already registered.
func TestToolAnnotationsOptionalAndCopied(t *testing.T) {
	ok := func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
		return NewToolResponseText("ok"), nil
	}
	s := NewServer("a", "1")
	s.RegisterTool(NewTool("plain", "d"), ok)
	b := NewTool("copied", "d").ReadOnly()
	s.RegisterTool(b, ok)
	b.Destructive(true)

	for _, tl := range s.ListToolsWithContext(context.Background()) {
		switch tl.Name {
		case "plain":
			if raw, _ := json.Marshal(tl); tl.Annotations != nil || strings.Contains(string(raw), "annotations") {
				t.Errorf("plain tool sent annotations: %s", raw)
			}
		case "copied":
			if tl.Annotations == nil || tl.Annotations.DestructiveHint != nil {
				t.Errorf("registered annotations changed by a later builder call: %+v", tl.Annotations)
			}
		}
	}
}
