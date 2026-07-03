package mcp

import "context"

// ResourceProvider is the interface that providers implement to expose
// resources scoped to a request — for example per-user or per-session data.
//
// It is the resource analogue of [ToolProvider]: attach instances to the
// request context with [WithResourceProviders] and the server merges them with
// any statically-registered resources when serving resources/list,
// resources/templates/list and resources/read.
type ResourceProvider interface {
	// GetResources returns the resources and resource templates this provider
	// exposes for the request. The context carries tenant/user/session
	// information for filtering. Return a non-nil, empty ProvidedResources if
	// the provider has nothing to expose.
	GetResources(ctx context.Context) (*ProvidedResources, error)

	// ReadResource returns the content of the resource identified by uri.
	//
	// Miss contract: if this provider does not handle the uri, return
	// (nil, ErrUnknownResource). Any other non-nil error aborts dispatch and is
	// returned to the caller, so only use it for genuine failures, not misses.
	ReadResource(ctx context.Context, uri string) (*ResourceResponse, error)
}

// ProvidedResources is the set of descriptors a [ResourceProvider] exposes for a
// request. Either slice may be empty.
type ProvidedResources struct {
	Resources []MCPResource         // Static resources surfaced via resources/list
	Templates []MCPResourceTemplate // Parameterized templates surfaced via resources/templates/list
}

// resourceProvidersKey is the context key for resource providers.
type resourceProvidersKey struct{}

// WithResourceProviders returns a context with the given resource providers
// attached. Multiple providers can be attached; all are queried. Providers are
// consulted in order and the first one that handles a URI wins.
//
// This is the resource equivalent of [WithToolProviders]. Use it in request
// middleware to inject per-user or per-session resources:
//
//	func handler(w http.ResponseWriter, r *http.Request) {
//	    user := currentUser(r)
//	    ctx := mcp.WithResourceProviders(r.Context(), &UserResourceProvider{user: user})
//	    server.HandleRequest(w, r.WithContext(ctx))
//	}
func WithResourceProviders(ctx context.Context, providers ...ResourceProvider) context.Context {
	existing := GetResourceProviders(ctx)
	return context.WithValue(ctx, resourceProvidersKey{}, append(existing, providers...))
}

// GetResourceProviders returns the resource providers from the context, or nil
// if none are attached.
func GetResourceProviders(ctx context.Context) []ResourceProvider {
	if ctx == nil {
		return nil
	}
	providers, _ := ctx.Value(resourceProvidersKey{}).([]ResourceProvider)
	return providers
}

// listResourcesFromProviders returns static-resource descriptors from all
// providers, skipping any URI already in seen (and recording new ones).
func listResourcesFromProviders(ctx context.Context, seen map[string]bool) []MCPResource {
	providers := GetResourceProviders(ctx)
	if len(providers) == 0 {
		return nil
	}
	var all []MCPResource
	for _, provider := range providers {
		provided, err := provider.GetResources(ctx)
		if err != nil || provided == nil {
			continue
		}
		for _, res := range provided.Resources {
			if !seen[res.URI] {
				all = append(all, res)
				seen[res.URI] = true
			}
		}
	}
	return all
}

// listResourceTemplatesFromProviders returns template descriptors from all
// providers, skipping any template already in seen (and recording new ones).
func listResourceTemplatesFromProviders(ctx context.Context, seen map[string]bool) []MCPResourceTemplate {
	providers := GetResourceProviders(ctx)
	if len(providers) == 0 {
		return nil
	}
	var all []MCPResourceTemplate
	for _, provider := range providers {
		provided, err := provider.GetResources(ctx)
		if err != nil || provided == nil {
			continue
		}
		for _, tmpl := range provided.Templates {
			if !seen[tmpl.URITemplate] {
				all = append(all, tmpl)
				seen[tmpl.URITemplate] = true
			}
		}
	}
	return all
}

// readResourceFromProviders tries each provider in order. The first that
// returns a non-nil response (or a non-miss error) terminates the search.
// Returns ErrUnknownResource if no provider handles the uri.
func readResourceFromProviders(ctx context.Context, uri string) (*ResourceResponse, error) {
	for _, provider := range GetResourceProviders(ctx) {
		resp, err := provider.ReadResource(ctx, uri)
		if err == ErrUnknownResource {
			continue
		}
		if err != nil {
			return nil, err
		}
		if resp != nil {
			return resp, nil
		}
	}
	return nil, ErrUnknownResource
}
