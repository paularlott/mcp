package mcp

import "context"

// PromptProvider is the interface that providers implement to expose prompts
// scoped to a request — for example per-user or per-session prompts.
//
// It is the prompt analogue of [ToolProvider] and [ResourceProvider]: attach
// instances to the request context with [WithPromptProviders] and the server
// merges them with any statically-registered prompts when serving prompts/list
// and prompts/get.
type PromptProvider interface {
	// GetPrompts returns the prompt descriptors this provider exposes for the
	// request. The context carries tenant/user/session information for filtering.
	GetPrompts(ctx context.Context) ([]MCPPrompt, error)

	// GetPrompt renders a prompt by name with the given arguments.
	//
	// Miss contract: if this provider does not handle the named prompt, return
	// (nil, ErrUnknownPrompt). Any other non-nil error aborts dispatch and is
	// returned to the caller, so only use it for genuine failures, not misses.
	GetPrompt(ctx context.Context, name string, args map[string]string) (*PromptResponse, error)
}

// promptProvidersKey is the context key for prompt providers.
type promptProvidersKey struct{}

// WithPromptProviders returns a context with the given prompt providers
// attached. Multiple providers can be attached; all are queried. Providers are
// consulted in order and the first one that handles a prompt wins.
//
// This is the prompt equivalent of [WithToolProviders] / [WithResourceProviders].
func WithPromptProviders(ctx context.Context, providers ...PromptProvider) context.Context {
	existing := GetPromptProviders(ctx)
	return context.WithValue(ctx, promptProvidersKey{}, append(existing, providers...))
}

// GetPromptProviders returns the prompt providers from the context, or nil if
// none are attached.
func GetPromptProviders(ctx context.Context) []PromptProvider {
	if ctx == nil {
		return nil
	}
	providers, _ := ctx.Value(promptProvidersKey{}).([]PromptProvider)
	return providers
}

// listPromptsFromProviders returns prompt descriptors from all providers,
// skipping any name already in seen (and recording new ones).
func listPromptsFromProviders(ctx context.Context, seen map[string]bool) []MCPPrompt {
	providers := GetPromptProviders(ctx)
	if len(providers) == 0 {
		return nil
	}
	var all []MCPPrompt
	for _, provider := range providers {
		prompts, err := provider.GetPrompts(ctx)
		if err != nil {
			continue
		}
		for _, p := range prompts {
			if !seen[p.Name] {
				all = append(all, p)
				seen[p.Name] = true
			}
		}
	}
	return all
}

// getPromptFromProviders tries each provider in order. The first that returns a
// non-nil response (or a non-miss error) terminates the search. Returns
// ErrUnknownPrompt if no provider handles the name.
func getPromptFromProviders(ctx context.Context, name string, args map[string]string) (*PromptResponse, error) {
	for _, provider := range GetPromptProviders(ctx) {
		resp, err := provider.GetPrompt(ctx, name, args)
		if err == ErrUnknownPrompt {
			continue
		}
		if err != nil {
			return nil, err
		}
		if resp != nil {
			return resp, nil
		}
	}
	return nil, ErrUnknownPrompt
}
