package mcp

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// registeredResource holds a static resource and its read handler.
type registeredResource struct {
	descriptor MCPResource
	handler    ResourceHandler
}

// registeredResourceTemplate holds a resource template, its read handler, and a
// precompiled pattern used to match concrete URIs against the template.
type registeredResourceTemplate struct {
	descriptor MCPResourceTemplate
	handler    ResourceHandler
	pattern    *regexp.Regexp
}

// RegisterResource registers a static resource. The resource appears in
// resources/list and is served by resources/read. Registering a resource with a
// URI that already exists replaces the previous one.
//
// Thread-safe. Configure before serving in simple setups; concurrent
// registration while requests are in flight is permitted (readers observe
// either the old or new set) but discouraged for per-request data — use
// [WithResourceProviders] instead.
func (s *Server) RegisterResource(rb *ResourceBuilder, handler ResourceHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resources[rb.uri] = &registeredResource{
		descriptor: rb.ToMCPResource(),
		handler:    handler,
	}
}

// UnregisterResource removes a static resource by URI. Returns true if a
// resource was removed.
func (s *Server) UnregisterResource(uri string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, existed := s.resources[uri]
	delete(s.resources, uri)
	return existed
}

// RegisterResourceTemplate registers a parameterized resource template. The
// template appears in resources/templates/list; a client expands it into a
// concrete URI and reads it via resources/read. The handler receives the
// expanded URI and is responsible for parsing any variables out of it.
//
// A template's URITemplate may contain one or more {var} placeholders. Each
// placeholder matches one or more characters in the requested URI. Templates are
// matched in registration order; the first match wins.
//
// Thread-safe.
func (s *Server) RegisterResourceTemplate(tb *ResourceTemplateBuilder, handler ResourceHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resourceTemplates = append(s.resourceTemplates, &registeredResourceTemplate{
		descriptor: tb.ToMCPResourceTemplate(),
		handler:    handler,
		pattern:    compileResourceTemplate(tb.uriTemplate),
	})
}

// ListResources returns all registered static resources plus any contributed by
// [ResourceProvider]s on ctx, sorted by URI. Duplicates (by URI) are removed,
// with static registrations taking precedence.
func (s *Server) ListResources(ctx context.Context) []MCPResource {
	s.mu.RLock()
	result := make([]MCPResource, 0, len(s.resources))
	seen := make(map[string]bool, len(s.resources))
	for _, rr := range s.resources {
		result = append(result, rr.descriptor)
		seen[rr.descriptor.URI] = true
	}
	s.mu.RUnlock()

	sort.Slice(result, func(i, j int) bool { return result[i].URI < result[j].URI })

	if ctx != nil {
		result = append(result, listResourcesFromProviders(ctx, seen)...)
	}
	return result
}

// ListResourceTemplates returns all registered resource templates plus any
// contributed by [ResourceProvider]s on ctx, sorted by URITemplate. Duplicates
// (by URITemplate) are removed, with static registrations taking precedence.
func (s *Server) ListResourceTemplates(ctx context.Context) []MCPResourceTemplate {
	s.mu.RLock()
	result := make([]MCPResourceTemplate, 0, len(s.resourceTemplates))
	seen := make(map[string]bool, len(s.resourceTemplates))
	for _, rt := range s.resourceTemplates {
		result = append(result, rt.descriptor)
		seen[rt.descriptor.URITemplate] = true
	}
	s.mu.RUnlock()

	sort.Slice(result, func(i, j int) bool { return result[i].URITemplate < result[j].URITemplate })

	if ctx != nil {
		result = append(result, listResourceTemplatesFromProviders(ctx, seen)...)
	}
	return result
}

// ReadResource resolves a URI to its content. Resolution order:
//  1. Static resources by exact URI match.
//  2. Static resource templates by pattern match (first match wins).
//  3. [ResourceProvider]s on ctx, in attachment order (first hit wins).
//
// Returns ErrUnknownResource if nothing handles the uri.
func (s *Server) ReadResource(ctx context.Context, uri string) (*ResourceResponse, error) {
	s.mu.RLock()

	// 1. Static exact match.
	if rr, ok := s.resources[uri]; ok {
		handler := rr.handler
		s.mu.RUnlock()
		return handler(ctx, uri)
	}

	// 2. Static template match. Copy the slice so we can release the lock before
	// invoking the handler.
	templates := make([]*registeredResourceTemplate, len(s.resourceTemplates))
	copy(templates, s.resourceTemplates)
	s.mu.RUnlock()

	for _, rt := range templates {
		if rt.pattern != nil && rt.pattern.MatchString(uri) {
			return rt.handler(ctx, uri)
		}
	}

	// 3. Providers.
	if ctx != nil {
		return readResourceFromProviders(ctx, uri)
	}
	return nil, ErrUnknownResource
}

// handleResourcesList handles resources/list over HTTP.
func (s *Server) handleResourcesList(w http.ResponseWriter, r *http.Request, req *MCPRequest) {
	resources := s.ListResources(r.Context())
	s.sendMCPResponse(w, req.ID, map[string]any{"resources": resources})
}

// handleResourcesTemplatesList handles resources/templates/list over HTTP.
func (s *Server) handleResourcesTemplatesList(w http.ResponseWriter, r *http.Request, req *MCPRequest) {
	templates := s.ListResourceTemplates(r.Context())
	s.sendMCPResponse(w, req.ID, map[string]any{"resourceTemplates": templates})
}

// handleResourcesRead handles resources/read over HTTP.
func (s *Server) handleResourcesRead(w http.ResponseWriter, r *http.Request, req *MCPRequest) {
	var params resourceReadParams
	if err := s.parseParams(req, &params); err != nil {
		s.sendMCPError(w, req.ID, ErrorCodeInvalidParams, "Invalid params", nil)
		return
	}
	if params.URI == "" {
		s.sendMCPError(w, req.ID, ErrorCodeInvalidParams, "uri parameter is required", nil)
		return
	}

	resp, err := s.ReadResource(r.Context(), params.URI)
	if err != nil {
		if err == ErrUnknownResource {
			s.sendMCPError(w, req.ID, ErrorCodeInvalidParams, "Resource not found", map[string]any{"uri": params.URI})
			return
		}
		if toolErr, ok := err.(*ToolError); ok {
			s.sendMCPError(w, req.ID, toolErr.Code, toolErr.Message, toolErr.Data)
			return
		}
		s.sendMCPError(w, req.ID, ErrorCodeInternalError, fmt.Sprintf("Resource read failed: %v", err), nil)
		return
	}

	s.sendMCPResponse(w, req.ID, resp)
}

// compileResourceTemplate compiles an RFC 6570 level-1 URI template (with {var}
// placeholders) into an anchored regexp that matches concrete URIs. Each
// placeholder becomes a capturing group matching one or more characters. A
// malformed template returns nil, in which case the template never matches.
func compileResourceTemplate(template string) *regexp.Regexp {
	var b strings.Builder
	b.WriteByte('^')
	i := 0
	for i < len(template) {
		if template[i] == '{' {
			end := strings.IndexByte(template[i:], '}')
			if end == -1 {
				// Unterminated placeholder; treat the rest as a literal.
				b.WriteString(regexp.QuoteMeta(template[i:]))
				break
			}
			b.WriteString("(.+)")
			i += end + 1
			continue
		}
		next := strings.IndexByte(template[i:], '{')
		if next == -1 {
			b.WriteString(regexp.QuoteMeta(template[i:]))
			break
		}
		b.WriteString(regexp.QuoteMeta(template[i : i+next]))
		i += next
	}
	b.WriteByte('$')
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil
	}
	return re
}
