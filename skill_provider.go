package mcp

import "context"

// SkillProvider is the interface that providers implement to expose skills
// scoped to a request — for example a per-user catalog served from a database
// rather than the server's static registry.
//
// It is the skills analogue of [ToolProvider] and [ResourceProvider]: attach
// instances to the request context with [WithSkillProviders] and the server
// merges them with any statically-registered skills (see [Server.RegisterSkill])
// when serving skills/list and skills/get. A provider whose skills carry files
// should also implement [ResourceProvider] so those files answer
// resources/read.
type SkillProvider interface {
	// ListSkills returns the skills this provider exposes for the request.
	// The context carries tenant/user/session information for filtering.
	// Return a nil or empty slice when the provider has nothing to expose.
	ListSkills(ctx context.Context) ([]Skill, error)
}

// skillProvidersKey is the context key for skill providers.
type skillProvidersKey struct{}

// WithSkillProviders returns a context with the given skill providers
// attached. Multiple providers can be attached; all are queried, and the
// static registry wins URI collisions (provider entries are appended only
// for URIs it does not already serve).
//
// This is the skills equivalent of [WithToolProviders] /
// [WithResourceProviders]. Use it in request middleware to inject per-user
// or per-session skills:
//
//	func handler(w http.ResponseWriter, r *http.Request) {
//	    user := currentUser(r)
//	    ctx := mcp.WithSkillProviders(r.Context(), &UserSkillProvider{user: user})
//	    server.HandleRequest(w, r.WithContext(ctx))
//	}
func WithSkillProviders(ctx context.Context, providers ...SkillProvider) context.Context {
	existing := GetSkillProviders(ctx)
	return context.WithValue(ctx, skillProvidersKey{}, append(existing, providers...))
}

// GetSkillProviders returns the skill providers from the context, or nil if
// none are attached.
func GetSkillProviders(ctx context.Context) []SkillProvider {
	if ctx == nil {
		return nil
	}
	providers, _ := ctx.Value(skillProvidersKey{}).([]SkillProvider)
	return providers
}
