package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/paularlott/jsonrpc"
)

// The completion utility (completion/complete): argument autocompletion for
// prompts and resource templates, served the same way on every revision the
// library speaks (2025-03-26 through 2026-07-28). See
// https://modelcontextprotocol.io/specification/2026-07-28/server/utilities/completion.

// MaxCompletionValues is the spec's limit on completion.values: a response
// carries at most 100 suggestions. Longer lists are cut to this length, with
// hasMore set and total reporting the full count.
const MaxCompletionValues = 100

// Completion reference types: what a completion/complete request completes.
const (
	// CompletionRefPrompt references a prompt by name.
	CompletionRefPrompt = "ref/prompt"
	// CompletionRefResource references a resource template (or resource) by URI.
	CompletionRefResource = "ref/resource"
)

// CompletionRef identifies what a completion/complete request completes: a
// prompt (Type CompletionRefPrompt, by Name) or a resource template (Type
// CompletionRefResource, by URI, which is the template's URI template string).
type CompletionRef struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
	URI  string `json:"uri,omitempty"`
}

// CompletionRequest is the argument being completed: its name, the partial
// value typed so far, and the values of arguments the user has already
// filled in (the request's context.arguments), so suggestions for one
// argument can depend on another — e.g. "framework" suggestions narrowed by
// an already-chosen "language".
type CompletionRequest struct {
	argument    string
	value       string
	contextArgs map[string]string
}

// NewCompletionRequest builds a CompletionRequest. Completers normally
// receive a ready-made request; this constructor exists for tests and
// providers that build their own.
func NewCompletionRequest(argument, value string, contextArgs map[string]string) *CompletionRequest {
	return &CompletionRequest{argument: argument, value: value, contextArgs: contextArgs}
}

// Argument returns the name of the argument (or template variable) being completed.
func (r *CompletionRequest) Argument() string { return r.argument }

// Value returns the partial value to complete; it may be empty.
func (r *CompletionRequest) Value() string { return r.value }

// ContextArg returns an already-resolved argument from context.arguments,
// and whether the client sent it.
func (r *CompletionRequest) ContextArg(name string) (string, bool) {
	v, ok := r.contextArgs[name]
	return v, ok
}

// ContextArgs returns all already-resolved arguments from context.arguments.
// The map is empty, never nil, when the client sent none. It is not copied;
// do not mutate it.
func (r *CompletionRequest) ContextArgs() map[string]string {
	if r.contextArgs == nil {
		return map[string]string{}
	}
	return r.contextArgs
}

// Completer suggests values for one prompt argument or resource template
// variable. Return every match, ranked by relevance (the spec asks for
// relevance order and suggests fuzzy matching where it fits): the library
// sends the first [MaxCompletionValues], sets total to the number returned
// and hasMore when some were cut. Return a [ToolError] (e.g.
// [NewToolErrorInvalidParams]) to send a specific JSON-RPC error; any other
// error is sent as an internal error ("Completion failed") without its text,
// which may name internals the caller must not see.
//
// Completion inputs come from the client: the spec requires servers to
// validate them and to keep completions from disclosing anything the caller
// may not see, so filter suggestions by the caller's access the same way the
// prompt or resource itself is protected.
type Completer func(ctx context.Context, req *CompletionRequest) ([]string, error)

// CompletionResult is the completion object of a completion/complete result.
// Values holds at most [MaxCompletionValues] suggestions; Total, when set,
// is the number of matches available, which may exceed len(Values); HasMore
// reports that matches beyond Values exist even if Total is unknown.
type CompletionResult struct {
	Values  []string `json:"values"`
	Total   *int     `json:"total,omitempty"`
	HasMore bool     `json:"hasMore"`
}

// NewCompletionResult builds a result from a full list of matches, applying
// the spec's 100-value cap: Total is len(values), and HasMore is set when the
// list had to be cut.
func NewCompletionResult(values []string) *CompletionResult {
	total := len(values)
	return capCompletionResult(&CompletionResult{Values: values, Total: &total})
}

// capCompletionResult enforces the spec's shape on any result, including one
// a provider built itself: values is an array (never null) of at most
// MaxCompletionValues entries; cutting it sets hasMore and keeps total at
// least the number of matches the server actually had.
func capCompletionResult(res *CompletionResult) *CompletionResult {
	if res == nil {
		res = &CompletionResult{}
	}
	out := *res
	if out.Values == nil {
		out.Values = []string{}
	}
	if n := len(out.Values); n > MaxCompletionValues {
		if out.Total == nil || *out.Total < n {
			total := n
			out.Total = &total
		}
		out.Values = out.Values[:MaxCompletionValues]
		out.HasMore = true
	}
	return &out
}

// PromptCompletionProvider is an optional interface a [PromptProvider] can
// implement to complete the arguments of the prompts it lists. A provider
// that lists a prompt but doesn't implement it answers every declared
// argument of that prompt with no suggestions.
type PromptCompletionProvider interface {
	// CompletePrompt suggests values for an argument of the named prompt,
	// which this provider listed. Return a ToolError with
	// ErrorCodeInvalidParams for an argument the prompt doesn't declare.
	// The library applies the 100-value cap to the result.
	CompletePrompt(ctx context.Context, name string, req *CompletionRequest) (*CompletionResult, error)
}

// ResourceCompletionProvider is an optional interface a [ResourceProvider]
// can implement to complete the variables of the resource templates it
// lists. A provider that lists a template but doesn't implement it answers
// every variable of that template with no suggestions.
type ResourceCompletionProvider interface {
	// CompleteResourceTemplate suggests values for a variable of the
	// template whose URI template is uriTemplate, which this provider
	// listed. Return a ToolError with ErrorCodeInvalidParams for a name the
	// template doesn't contain. The library applies the 100-value cap.
	CompleteResourceTemplate(ctx context.Context, uriTemplate string, req *CompletionRequest) (*CompletionResult, error)
}

// Complete attaches a completer to the most recently declared argument:
//
//	mcp.NewPrompt("code_review", "Review code").
//	    Argument("language", "Programming language", true).
//	    Complete(func(ctx context.Context, req *mcp.CompletionRequest) ([]string, error) {
//	        return mcp.FilterPrefix([]string{"go", "python", "rust"}, req.Value()), nil
//	    })
//
// A server with at least one completer declares the completions capability
// and answers completion/complete. Panics if no argument has been declared.
func (p *PromptBuilder) Complete(fn Completer) *PromptBuilder {
	if len(p.arguments) == 0 {
		panic(fmt.Sprintf("mcp: prompt %q: Complete must follow an Argument", p.name))
	}
	if p.completers == nil {
		p.completers = map[string]Completer{}
	}
	p.completers[p.arguments[len(p.arguments)-1].Name] = fn
	return p
}

// Complete attaches a completer to one of the template's {variable}
// placeholders:
//
//	mcp.NewResourceTemplate("file:///{path}", "Files", "", "").
//	    Complete("path", func(ctx context.Context, req *mcp.CompletionRequest) ([]string, error) {
//	        return listPaths(req.Value()), nil
//	    })
//
// A server with at least one completer declares the completions capability
// and answers completion/complete. Panics if the template has no such
// variable.
func (t *ResourceTemplateBuilder) Complete(variable string, fn Completer) *ResourceTemplateBuilder {
	varNames, _ := parseResourceTemplate(t.uriTemplate)
	if !containsString(varNames, variable) {
		panic(fmt.Sprintf("mcp: resource template %q has no variable %q", t.uriTemplate, variable))
	}
	if t.completers == nil {
		t.completers = map[string]Completer{}
	}
	t.completers[variable] = fn
	return t
}

// FilterPrefix returns the values that start with prefix, in their original
// order — a ready-made matcher for a [Completer] over a fixed list.
func FilterPrefix(values []string, prefix string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if len(v) >= len(prefix) && v[:len(prefix)] == prefix {
			out = append(out, v)
		}
	}
	return out
}

// copyCompleters snapshots a builder's completers at registration, so later
// changes to the builder don't reach the registered prompt or template.
func copyCompleters(in map[string]Completer) map[string]Completer {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]Completer, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// HasCompletions reports whether the server can complete anything for a
// request with ctx: a registered prompt or resource template has a
// completer, or a provider on ctx implements [PromptCompletionProvider] or
// [ResourceCompletionProvider]. It decides both whether the completions
// capability is declared and whether completion/complete exists at all —
// the spec has servers that support completions declare the capability,
// and a server that doesn't answers the method with -32601.
func (s *Server) HasCompletions(ctx context.Context) bool {
	s.mu.RLock()
	for _, rp := range s.prompts {
		if len(rp.completers) > 0 {
			s.mu.RUnlock()
			return true
		}
	}
	for _, rt := range s.resourceTemplates {
		if len(rt.completers) > 0 {
			s.mu.RUnlock()
			return true
		}
	}
	s.mu.RUnlock()

	for _, p := range GetPromptProviders(ctx) {
		if _, ok := p.(PromptCompletionProvider); ok {
			return true
		}
	}
	for _, p := range GetResourceProviders(ctx) {
		if _, ok := p.(ResourceCompletionProvider); ok {
			return true
		}
	}
	return false
}

// capabilitiesFor is buildCapabilities for one request: it adds the
// completions capability when the server has completers for ctx (see
// HasCompletions), since the spec makes declaring it the signal that
// completion/complete is available. resourceSubscribe is passed through to
// buildCapabilities (whether this transport can honour resources.subscribe).
func (s *Server) capabilitiesFor(ctx context.Context, protocolVersion string, resourceSubscribe bool) capabilities {
	caps := s.buildCapabilities(protocolVersion, resourceSubscribe)
	if s.HasCompletions(ctx) {
		caps.Completions = &struct{}{}
	}
	return caps
}

// isDispatchable reports whether a Modern request's method exists on this
// server for ctx: one of dispatchableMethods, except completion/complete when
// there are no completers — the spec has an unsupported capability's method
// answer as not found, and on 2026-07-28 HTTP an unknown method is a 404, so
// it must be routed exactly like a method the server never had.
func (s *Server) isDispatchable(ctx context.Context, method string) bool {
	if !dispatchableMethods[method] {
		return false
	}
	if method == "completion/complete" {
		return s.HasCompletions(ctx)
	}
	return true
}

// errUnknownCompletionArgument is wrapped for a completion request naming an
// argument the prompt doesn't declare or a variable the template doesn't
// contain; the spec answers that, like an unknown prompt, with -32602.
var errUnknownCompletionArgument = errors.New("unknown argument")

// Complete suggests values for one argument of a prompt or resource
// template. Resolution follows prompts/get and resources/read:
//
//   - ref/prompt: a registered prompt by name, then the first
//     [PromptProvider] on ctx that lists it.
//   - ref/resource: a registered resource template whose URI template
//     equals ref.URI, then the first [ResourceProvider] on ctx that lists
//     it. A static resource URI resolves too, but has no arguments.
//
// An argument the prompt or template has no completer for gets an empty
// result. Returns ErrUnknownPrompt or ErrUnknownResource when nothing
// matches the reference, and an error wrapping an InvalidParams ToolError
// when the argument isn't one the prompt or template has.
func (s *Server) Complete(ctx context.Context, ref CompletionRef, req *CompletionRequest) (*CompletionResult, error) {
	if req == nil {
		req = NewCompletionRequest("", "", nil)
	}
	switch ref.Type {
	case CompletionRefPrompt:
		return s.completePrompt(ctx, ref.Name, req)
	case CompletionRefResource:
		return s.completeResource(ctx, ref.URI, req)
	default:
		return nil, NewToolErrorInvalidParams(fmt.Sprintf("unsupported ref type %q: must be %q or %q", ref.Type, CompletionRefPrompt, CompletionRefResource))
	}
}

func (s *Server) completePrompt(ctx context.Context, name string, req *CompletionRequest) (*CompletionResult, error) {
	s.mu.RLock()
	rp, ok := s.prompts[name]
	s.mu.RUnlock()
	if ok {
		if !promptDeclaresArgument(rp.descriptor, req.argument) {
			return nil, unknownCompletionArgument(req.argument)
		}
		return runCompleter(ctx, rp.completers[req.argument], req)
	}

	for _, provider := range GetPromptProviders(ctx) {
		prompts, err := provider.GetPrompts(ctx)
		if err != nil {
			// Skip the erroring provider, as prompts/get does.
			continue
		}
		for _, p := range prompts {
			if p.Name != name {
				continue
			}
			if cp, ok := provider.(PromptCompletionProvider); ok {
				res, err := cp.CompletePrompt(ctx, name, req)
				if err != nil {
					return nil, err
				}
				return capCompletionResult(res), nil
			}
			if !promptDeclaresArgument(p, req.argument) {
				return nil, unknownCompletionArgument(req.argument)
			}
			return NewCompletionResult(nil), nil
		}
	}
	return nil, ErrUnknownPrompt
}

func (s *Server) completeResource(ctx context.Context, uri string, req *CompletionRequest) (*CompletionResult, error) {
	s.mu.RLock()
	var tmpl *registeredResourceTemplate
	for _, rt := range s.resourceTemplates {
		if rt.descriptor.URITemplate == uri {
			tmpl = rt
			break
		}
	}
	_, isStatic := s.resources[uri]
	s.mu.RUnlock()

	if tmpl != nil {
		if !containsString(tmpl.varNames, req.argument) {
			return nil, unknownCompletionArgument(req.argument)
		}
		return runCompleter(ctx, tmpl.completers[req.argument], req)
	}
	if isStatic {
		// The spec lets ref/resource name a plain URI, but a static
		// resource has no arguments to complete.
		return nil, unknownCompletionArgument(req.argument)
	}

	for _, provider := range GetResourceProviders(ctx) {
		provided, err := provider.GetResources(ctx)
		if err != nil || provided == nil {
			continue
		}
		for _, t := range provided.Templates {
			if t.URITemplate != uri {
				continue
			}
			if cp, ok := provider.(ResourceCompletionProvider); ok {
				res, err := cp.CompleteResourceTemplate(ctx, uri, req)
				if err != nil {
					return nil, err
				}
				return capCompletionResult(res), nil
			}
			varNames, _ := parseResourceTemplate(uri)
			if !containsString(varNames, req.argument) {
				return nil, unknownCompletionArgument(req.argument)
			}
			return NewCompletionResult(nil), nil
		}
		for _, r := range provided.Resources {
			if r.URI == uri {
				return nil, unknownCompletionArgument(req.argument)
			}
		}
	}
	return nil, ErrUnknownResource
}

func promptDeclaresArgument(p MCPPrompt, name string) bool {
	for _, a := range p.Arguments {
		if a.Name == name {
			return true
		}
	}
	return false
}

func unknownCompletionArgument(name string) error {
	return &ToolError{
		Code:    ErrorCodeInvalidParams,
		Message: fmt.Sprintf("%v: %s", errUnknownCompletionArgument, name),
		Data:    map[string]any{"argument": name},
	}
}

// runCompleter calls fn, or answers with no suggestions when the argument
// has no completer: completion is optional per argument, and an empty list
// is the spec's way of saying there is nothing to suggest.
func runCompleter(ctx context.Context, fn Completer, req *CompletionRequest) (*CompletionResult, error) {
	if fn == nil {
		return NewCompletionResult(nil), nil
	}
	values, err := fn(ctx, req)
	if err != nil {
		return nil, err
	}
	return NewCompletionResult(values), nil
}

// completionCompleteParams is the params object for completion/complete.
// context (context.arguments) arrived in 2025-06-18; a 2025-03-26 client
// simply never sends it.
type completionCompleteParams struct {
	Ref      *CompletionRef `json:"ref"`
	Argument *struct {
		Name  *string `json:"name"`
		Value *string `json:"value"`
	} `json:"argument"`
	Context *struct {
		Arguments map[string]string `json:"arguments"`
	} `json:"context"`
}

// completionComplete validates a completion/complete request and runs it,
// shared by the HTTP and stdio handlers. A server without completers has no
// completion/complete method (-32601, as the spec's error table lists for
// an unsupported capability); malformed params, an unknown prompt or
// template, and an unknown argument are all -32602, on every revision —
// completion never used the Legacy resource-not-found code.
func (s *Server) completionComplete(ctx context.Context, raw json.RawMessage) (any, *MCPError) {
	if !s.HasCompletions(ctx) {
		return nil, &MCPError{Code: ErrorCodeMethodNotFound, Message: "Method not found", Data: map[string]any{"method": "completion/complete"}}
	}

	var params completionCompleteParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, &MCPError{Code: ErrorCodeInvalidParams, Message: "Invalid params", Data: map[string]any{"details": err.Error()}}
		}
	}
	invalid := func(details string) *MCPError {
		return &MCPError{Code: ErrorCodeInvalidParams, Message: "Invalid params", Data: map[string]any{"details": details}}
	}
	switch {
	case params.Ref == nil:
		return nil, invalid("ref is required")
	case params.Ref.Type == CompletionRefPrompt && params.Ref.Name == "":
		return nil, invalid("ref.name is required for ref/prompt")
	case params.Ref.Type == CompletionRefResource && params.Ref.URI == "":
		return nil, invalid("ref.uri is required for ref/resource")
	case params.Ref.Type != CompletionRefPrompt && params.Ref.Type != CompletionRefResource:
		return nil, invalid(fmt.Sprintf("ref.type must be %q or %q", CompletionRefPrompt, CompletionRefResource))
	case params.Argument == nil || params.Argument.Name == nil || params.Argument.Value == nil:
		return nil, invalid("argument.name and argument.value are required")
	}

	var contextArgs map[string]string
	if params.Context != nil {
		contextArgs = params.Context.Arguments
	}
	res, err := s.Complete(ctx, *params.Ref, NewCompletionRequest(*params.Argument.Name, *params.Argument.Value, contextArgs))
	if err != nil {
		return nil, completionWireError(*params.Ref, err)
	}
	return map[string]any{"completion": res}, nil
}

// completionWireError maps a Complete failure to its wire shape.
func completionWireError(ref CompletionRef, err error) *MCPError {
	switch {
	case errors.Is(err, ErrUnknownPrompt):
		return &MCPError{Code: ErrorCodeInvalidParams, Message: "Prompt not found", Data: map[string]any{"name": ref.Name}}
	case errors.Is(err, ErrUnknownResource):
		return &MCPError{Code: ErrorCodeInvalidParams, Message: "Resource template not found", Data: map[string]any{"uri": ref.URI}}
	}
	var toolErr *ToolError
	if errors.As(err, &toolErr) {
		return &MCPError{Code: toolErr.Code, Message: toolErr.Message, Data: toolErr.Data}
	}
	// Not the error's text: a completer's or provider's error may name
	// internals (a database, a host, a path) the caller must not see. A
	// completer that wants the client to read a message returns a ToolError.
	return &MCPError{Code: ErrorCodeInternalError, Message: "Completion failed"}
}

// handleCompletionComplete handles completion/complete over HTTP.
func (s *Server) handleCompletionComplete(w http.ResponseWriter, r *http.Request, req *MCPRequest) {
	raw, err := json.Marshal(req.Params)
	if err != nil {
		s.sendMCPError(w, req.ID, ErrorCodeInvalidParams, "Invalid params", nil)
		return
	}
	result, e := s.completionComplete(r.Context(), raw)
	if e != nil {
		s.sendMCPError(w, req.ID, e.Code, e.Message, e.Data)
		return
	}
	s.sendMCPResponse(w, req.ID, result)
}

// stdioCompletionComplete handles completion/complete over stdio.
func (s *Server) stdioCompletionComplete(ctx context.Context, raw json.RawMessage) (any, error) {
	result, e := s.completionComplete(ctx, raw)
	if e != nil {
		return nil, jsonrpc.NewError(e.Code, e.Message, e.Data)
	}
	return result, nil
}

// Complete asks the server for completion suggestions via
// completion/complete: values for argument (currently typed as value) of the
// prompt or resource template ref names. contextArgs carries the arguments
// the user has already filled in and may be nil. The server must declare
// the completions capability; one that doesn't answers -32601, returned as a
// *ToolError like any other JSON-RPC error.
func (c *Client) Complete(ctx context.Context, ref CompletionRef, argument, value string, contextArgs map[string]string) (*CompletionResult, error) {
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, err
	}

	params := map[string]any{
		"ref":      ref,
		"argument": map[string]any{"name": argument, "value": value},
	}
	if len(contextArgs) > 0 {
		params["context"] = map[string]any{"arguments": contextArgs}
	}
	req := MCPRequest{
		JSONRPC: "2.0",
		ID:      c.newRequestID(),
		Method:  "completion/complete",
		Params:  params,
	}

	var resp MCPResponse
	if err := c.sendRequest(ctx, &req, &resp, nil); err != nil {
		return nil, fmt.Errorf("completion failed: %w", err)
	}
	if resp.Error != nil {
		return nil, &ToolError{Code: resp.Error.Code, Message: resp.Error.Message, Data: resp.Error.Data}
	}
	if err := c.checkResultType(resp.Result); err != nil {
		return nil, err
	}
	var result struct {
		Completion CompletionResult `json:"completion"`
	}
	if err := decodeResult(resp.Result, &result, "completion response"); err != nil {
		return nil, err
	}
	if result.Completion.Values == nil {
		result.Completion.Values = []string{}
	}
	return &result.Completion, nil
}
