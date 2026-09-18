package mcp

import (
	"encoding/json"
	"testing"
)

// TestResourceBuilderAccessors covers the trivial accessor methods on
// ResourceBuilder and ResourceRequest that no other test exercises directly
// (URI/Name/Description/MimeType/Vars/String/StringOr).
func TestResourceBuilderAccessors(t *testing.T) {
	rb := NewResource("config://app", "App Config", "the app config", "application/json")

	if got, want := rb.URI(), "config://app"; got != want {
		t.Errorf("URI() = %q, want %q", got, want)
	}
	if got, want := rb.Name(), "App Config"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
	if got, want := rb.Description(), "the app config"; got != want {
		t.Errorf("Description() = %q, want %q", got, want)
	}
	if got, want := rb.MimeType(), "application/json"; got != want {
		t.Errorf("MimeType() = %q, want %q", got, want)
	}

	mcpRes := rb.ToMCPResource()
	if mcpRes.URI != rb.URI() || mcpRes.Name != rb.Name() || mcpRes.Description != rb.Description() || mcpRes.MimeType != rb.MimeType() {
		t.Errorf("ToMCPResource() = %+v does not match builder fields", mcpRes)
	}
}

// TestResourceTemplateBuilderAccessors covers ResourceTemplateBuilder's
// accessors, which mirror ResourceBuilder's but are entirely separate 0%
// methods (URITemplate/Name/Description/MimeType).
func TestResourceTemplateBuilderAccessors(t *testing.T) {
	tb := NewResourceTemplate("user://{id}", "User", "a user record", "application/json")

	if got, want := tb.URITemplate(), "user://{id}"; got != want {
		t.Errorf("URITemplate() = %q, want %q", got, want)
	}
	if got, want := tb.Name(), "User"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
	if got, want := tb.Description(), "a user record"; got != want {
		t.Errorf("Description() = %q, want %q", got, want)
	}
	if got, want := tb.MimeType(), "application/json"; got != want {
		t.Errorf("MimeType() = %q, want %q", got, want)
	}

	mcpTemplate := tb.ToMCPResourceTemplate()
	if mcpTemplate.URITemplate != tb.URITemplate() || mcpTemplate.Name != tb.Name() {
		t.Errorf("ToMCPResourceTemplate() = %+v does not match builder fields", mcpTemplate)
	}
}

// TestResourceRequestVarsAndAccessors covers ResourceRequest.Vars (both the nil
// and populated cases) plus String/StringOr, which are only lightly exercised
// elsewhere via the HTTP path.
func TestResourceRequestVarsAndAccessors(t *testing.T) {
	// nil vars map
	reqNil := NewResourceRequest("user://42", nil)
	if got := reqNil.Vars(); got == nil || len(got) != 0 {
		t.Errorf("Vars() on nil map = %#v, want empty non-nil map", got)
	}
	if _, err := reqNil.String("id"); err != ErrUnknownParameter {
		t.Errorf("String() on nil vars = %v, want ErrUnknownParameter", err)
	}
	if got, want := reqNil.StringOr("id", "fallback"), "fallback"; got != want {
		t.Errorf("StringOr() = %q, want %q", got, want)
	}

	// populated vars map
	req := NewResourceRequest("user://42", map[string]string{"id": "42"})
	if got := req.Vars(); len(got) != 1 || got["id"] != "42" {
		t.Errorf("Vars() = %#v, want {id: 42}", got)
	}
	if got, err := req.String("id"); err != nil || got != "42" {
		t.Errorf("String(id) = (%q, %v), want (42, nil)", got, err)
	}
	if got, want := req.StringOr("id", "fallback"), "42"; got != want {
		t.Errorf("StringOr(id) = %q, want %q", got, want)
	}
	if got, want := req.StringOr("missing", "fallback"), "fallback"; got != want {
		t.Errorf("StringOr(missing) = %q, want %q", got, want)
	}
	if got, want := req.URI(), "user://42"; got != want {
		t.Errorf("URI() = %q, want %q", got, want)
	}
}

// TestPromptBuilderAccessors covers PromptBuilder's Name/Description/Arguments
// and PromptRequest's Args, all 0% because callers only use ToMCPPrompt and
// the handler's String/StringOr.
func TestPromptBuilderAccessors(t *testing.T) {
	pb := NewPrompt("code_review", "Review code").
		Argument("code", "The code to review", true).
		Argument("style", "Review style", false)

	if got, want := pb.Name(), "code_review"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
	if got, want := pb.Description(), "Review code"; got != want {
		t.Errorf("Description() = %q, want %q", got, want)
	}
	args := pb.Arguments()
	if len(args) != 2 {
		t.Fatalf("Arguments() len = %d, want 2", len(args))
	}
	if args[0].Name != "code" || !args[0].Required {
		t.Errorf("Arguments()[0] = %+v, want required 'code'", args[0])
	}
	if args[1].Name != "style" || args[1].Required {
		t.Errorf("Arguments()[1] = %+v, want optional 'style'", args[1])
	}

	mcpPrompt := pb.ToMCPPrompt()
	if mcpPrompt.Name != pb.Name() || len(mcpPrompt.Arguments) != 2 {
		t.Errorf("ToMCPPrompt() = %+v does not match builder", mcpPrompt)
	}
}

// TestPromptRequestArgs covers PromptRequest.Args for both the nil and
// populated backing map, along with String/StringOr's error path.
func TestPromptRequestArgs(t *testing.T) {
	reqNil := NewPromptRequest(nil)
	if got := reqNil.Args(); got == nil || len(got) != 0 {
		t.Errorf("Args() on nil map = %#v, want empty non-nil map", got)
	}
	if _, err := reqNil.String("code"); err != ErrUnknownParameter {
		t.Errorf("String() on nil args = %v, want ErrUnknownParameter", err)
	}

	req := NewPromptRequest(map[string]string{"code": "fmt.Println()"})
	if got := req.Args(); len(got) != 1 || got["code"] != "fmt.Println()" {
		t.Errorf("Args() = %#v, want {code: fmt.Println()}", got)
	}
	if got, want := req.StringOr("missing", "def"), "def"; got != want {
		t.Errorf("StringOr(missing) = %q, want %q", got, want)
	}
}

// TestToolBuilderToMCPTool covers ToolBuilder.ToMCPTool for both a native tool
// (no Discoverable call) and a discoverable one, including the output schema
// branch — no existing test calls ToMCPTool directly, only BuildSchema.
func TestToolBuilderToMCPTool(t *testing.T) {
	native := NewTool("native_tool", "a native tool", String("x", "x"))
	mcpTool := native.ToMCPTool()
	if mcpTool.Name != "native_tool" {
		t.Errorf("Name = %q, want native_tool", mcpTool.Name)
	}
	if mcpTool.Visibility != ToolVisibilityNative {
		t.Errorf("Visibility = %v, want ToolVisibilityNative", mcpTool.Visibility)
	}
	if mcpTool.OutputSchema != nil {
		t.Errorf("OutputSchema = %v, want nil (no Output() declared)", mcpTool.OutputSchema)
	}

	discoverable := NewTool("disc_tool", "a discoverable tool",
		String("x", "x"),
		Output(String("y", "y")),
	).Discoverable("kw1", "kw2")

	mcpTool2 := discoverable.ToMCPTool()
	if mcpTool2.Visibility != ToolVisibilityDiscoverable {
		t.Errorf("Visibility = %v, want ToolVisibilityDiscoverable", mcpTool2.Visibility)
	}
	if len(mcpTool2.Keywords) != 2 {
		t.Errorf("Keywords = %v, want 2 entries", mcpTool2.Keywords)
	}
	if mcpTool2.OutputSchema == nil {
		t.Errorf("OutputSchema = nil, want non-nil (Output() declared)")
	}
}

// TestToolErrorError covers ToolError.Error's formatting, which no test calls
// directly (errors.As-based tests only inspect the Code/Message fields).
func TestToolErrorError(t *testing.T) {
	err := &ToolError{Code: ErrorCodeInvalidParams, Message: "bad input"}
	got := err.Error()
	want := "MCP Error -32602: bad input"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// TestBooleanArrayParameter covers BooleanArray, the only array parameter
// constructor with no existing test (StringArray/NumberArray/IntegerArray all
// have coverage elsewhere).
func TestBooleanArrayParameter(t *testing.T) {
	tool := NewTool("bool_array_test", "Test boolean array parameter",
		BooleanArray("flags", "Flag list", Required()),
	)

	schema := tool.BuildSchema()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema missing properties: %+v", schema)
	}
	flagsProp, ok := props["flags"].(map[string]any)
	if !ok {
		t.Fatalf("flags property missing: %+v", props)
	}
	if got := flagsProp["type"]; got != "array" {
		t.Errorf("flags.type = %v, want array", got)
	}
	items, ok := flagsProp["items"].(map[string]any)
	if !ok {
		t.Fatalf("flags.items missing: %+v", flagsProp)
	}
	if got := items["type"]; got != "boolean" {
		t.Errorf("flags.items.type = %v, want boolean", got)
	}

	required, _ := schema["required"].([]string)
	if len(required) != 1 || required[0] != "flags" {
		t.Errorf("required = %v, want [flags]", required)
	}

	// Sanity round trip through JSON to make sure the schema is well-formed.
	if _, err := json.Marshal(schema); err != nil {
		t.Errorf("schema failed to marshal: %v", err)
	}
}

// TestRequiredOptionApplyToParam directly exercises requiredOption's
// applyToParam method. It is a no-op required to satisfy the Option
// interface (processOptions detects requiredOption via a type assertion
// instead), so nothing in production code ever calls it — this test documents
// that and gives it coverage.
func TestRequiredOptionApplyToParam(t *testing.T) {
	opt := requiredOption{}
	// Must not panic; there is nothing else to assert since the method is a
	// deliberate no-op.
	opt.applyToParam(parameterBase{name: "x"})
}

// TestOutputParamToParamDefDirect exercises outputParam.toParamDef directly.
// In normal use Output()'s apply() converts each inner parameter via its own
// toParamDef and never calls the container's toParamDef; this method exists
// only to satisfy the Parameter interface and is otherwise unreachable.
func TestOutputParamToParamDefDirect(t *testing.T) {
	p := Output(String("a", "a"))
	op, ok := p.(*outputParam)
	if !ok {
		t.Fatalf("Output() returned %T, want *outputParam", p)
	}
	def := op.toParamDef()
	if def.name != "" || def.paramType != "" || def.description != "" || def.required || def.properties != nil || def.itemSchema != nil {
		t.Errorf("toParamDef() = %+v, want zero value", def)
	}
}
