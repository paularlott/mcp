package mcp

import "context"

// PromptHandler returns the rendered messages for a prompt, given its arguments.
// Build the response with the NewPromptResponse* constructors.
type PromptHandler func(ctx context.Context, req *PromptRequest) (*PromptResponse, error)

// PromptBuilder is a fluent builder for a prompt. A prompt is a reusable,
// named message template with declared arguments that the client fills in; the
// server renders it into one or more messages via prompts/get. Register it with
// [Server.RegisterPrompt].
type PromptBuilder struct {
	name        string
	description string
	arguments   []MCPPromptArgument
}

// NewPrompt creates a prompt descriptor. Chain Argument calls to declare the
// arguments the prompt accepts:
//
//	p := mcp.NewPrompt("code_review", "Review code").
//	    Argument("code", "The code to review", true)
func NewPrompt(name, description string) *PromptBuilder {
	return &PromptBuilder{name: name, description: description}
}

// Argument declares an argument the prompt accepts.
func (p *PromptBuilder) Argument(name, description string, required bool) *PromptBuilder {
	p.arguments = append(p.arguments, MCPPromptArgument{
		Name:        name,
		Description: description,
		Required:    required,
	})
	return p
}

// Name returns the prompt's name.
func (p *PromptBuilder) Name() string { return p.name }

// Description returns the prompt's description.
func (p *PromptBuilder) Description() string { return p.description }

// Arguments returns the prompt's declared arguments.
func (p *PromptBuilder) Arguments() []MCPPromptArgument { return p.arguments }

// ToMCPPrompt converts the builder to an MCPPrompt descriptor.
func (p *PromptBuilder) ToMCPPrompt() MCPPrompt {
	return MCPPrompt{
		Name:        p.name,
		Description: p.description,
		Arguments:   p.arguments,
	}
}

// PromptRequest provides typed access to a prompt's arguments. All prompt
// arguments are strings. Use String to retrieve an argument (returning
// ErrUnknownParameter if absent) or StringOr for a default.
type PromptRequest struct {
	args map[string]string
}

// NewPromptRequest creates a PromptRequest from a string argument map.
func NewPromptRequest(args map[string]string) *PromptRequest {
	return &PromptRequest{args: args}
}

// String returns a string argument by name. Returns ErrUnknownParameter if the
// argument was not provided.
func (r *PromptRequest) String(name string) (string, error) {
	if r.args == nil {
		return "", ErrUnknownParameter
	}
	val, ok := r.args[name]
	if !ok {
		return "", ErrUnknownParameter
	}
	return val, nil
}

// StringOr returns a string argument or defaultValue if not provided.
func (r *PromptRequest) StringOr(name, defaultValue string) string {
	if val, err := r.String(name); err == nil {
		return val
	}
	return defaultValue
}

// Args returns all arguments as a map. The returned map may be empty but is
// never nil when obtained from a handler invocation.
func (r *PromptRequest) Args() map[string]string {
	if r.args == nil {
		return map[string]string{}
	}
	return r.args
}
