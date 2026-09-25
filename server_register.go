package mcp

import (
	"sort"
)

// Local tool registration: single and batch registration, unregistration, and the native-tool cache rebuild.

// registeredTool represents a registered tool
type registeredTool struct {
	Name         string
	Description  string
	Schema       map[string]any
	OutputSchema map[string]any
	Meta         map[string]any
	Icons        []Icon
	Handler      ToolHandler
	Visibility   ToolVisibility
}

// RegisterTool registers a tool with the server.
// The tool's visibility is determined by whether Discoverable() was called on the ToolBuilder:
//   - Native tools (default): appear in tools/list and are directly callable
//   - Discoverable tools (via .Discoverable(keywords...)): only available via tool_search and execute_tool
//
// Optional keywords parameter is merged with keywords set via Discoverable() for search relevance.
// Keywords are used in show-all mode and for discoverable tool search.
func (s *Server) RegisterTool(tool *ToolBuilder, handler ToolHandler, keywords ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if tool.IsDiscoverable() {
		s.registerDiscoverableToolLocked(tool, handler, keywords...)
	} else {
		s.registerNativeToolLocked(tool, handler, keywords...)
	}
	s.NotifyToolsChanged()
}

// registerNativeToolLocked registers a native tool while the lock is already held.
// Native tools appear in tools/list and are directly callable.
// Keywords are stored and used in show-all mode when native tools become searchable.
func (s *Server) registerNativeToolLocked(tool *ToolBuilder, handler ToolHandler, keywords ...string) {
	prev, existed := s.tools[tool.name]

	regTool := &registeredTool{
		Name:         tool.name,
		Description:  tool.Description(),
		Schema:       tool.buildSchema(),
		OutputSchema: tool.buildOutputSchema(),
		Meta:         tool.meta,
		Icons:        tool.icons,
		Handler:      handler,
		Visibility:   ToolVisibilityNative,
	}
	s.tools[tool.name] = regTool

	s.internalRegistry.UnregisterTool(tool.name)

	if existed && prev.Visibility == ToolVisibilityDiscoverable {
		s.recalcHasDiscoverableToolsLocked()
	}

	newTool := MCPTool{
		Name:        tool.name,
		Description: tool.Description(),
		InputSchema: regTool.Schema,
		Meta:        regTool.Meta,
		Icons:       regTool.Icons,
		Keywords:    keywords,
	}
	if regTool.OutputSchema != nil {
		newTool.OutputSchema = regTool.OutputSchema
	}

	idx := sort.Search(len(s.nativeToolCache), func(i int) bool {
		return s.nativeToolCache[i].Name >= tool.name
	})

	if idx < len(s.nativeToolCache) && s.nativeToolCache[idx].Name == tool.name {
		s.nativeToolCache[idx] = newTool
	} else {
		s.nativeToolCache = append(s.nativeToolCache, MCPTool{})
		copy(s.nativeToolCache[idx+1:], s.nativeToolCache[idx:])
		s.nativeToolCache[idx] = newTool
	}
}

// registerDiscoverableToolLocked registers a discoverable tool while the lock is already held.
// Discoverable tools do NOT appear in tools/list but can be discovered through tool_search.
// Keywords from the ToolBuilder are merged with the additional keywords parameter.
func (s *Server) registerDiscoverableToolLocked(tool *ToolBuilder, handler ToolHandler, keywords ...string) {
	s.hasDiscoverableTools = true

	regTool := &registeredTool{
		Name:         tool.name,
		Description:  tool.Description(),
		Schema:       tool.buildSchema(),
		OutputSchema: tool.buildOutputSchema(),
		Meta:         tool.meta,
		Icons:        tool.icons,
		Handler:      handler,
		Visibility:   ToolVisibilityDiscoverable,
	}
	s.tools[tool.name] = regTool

	idx := sort.Search(len(s.nativeToolCache), func(i int) bool {
		return s.nativeToolCache[i].Name >= tool.name
	})
	if idx < len(s.nativeToolCache) && s.nativeToolCache[idx].Name == tool.name {
		s.nativeToolCache = append(s.nativeToolCache[:idx], s.nativeToolCache[idx+1:]...)
	}

	allKeywords := append(tool.Keywords(), keywords...)

	s.internalRegistry.RegisterTool(tool, handler, allKeywords...)
}

// RegisterTools registers multiple tools with the server in a single batch,
// notifying subscribers once at the end.
// Each tool's visibility is determined by whether Discoverable() was called on its ToolBuilder.
func (s *Server) RegisterTools(tools ...*ToolRegistration) {
	if len(tools) == 0 {
		return
	}

	s.mu.Lock()
	// Delegate to the single-tool paths so both APIs behave identically —
	// a re-registered tool must leave the search registry (discoverable →
	// native) and merge extra keywords the same way in either API.
	for _, tr := range tools {
		if tr.Tool.IsDiscoverable() {
			s.registerDiscoverableToolLocked(tr.Tool, tr.Handler)
		} else {
			s.registerNativeToolLocked(tr.Tool, tr.Handler)
		}
	}
	s.mu.Unlock()

	s.NotifyToolsChanged()
}

// UnregisterTool removes a tool by name from the server.
// Returns true if the tool was found and removed, false otherwise.
// This is safe to call concurrently.
func (s *Server) UnregisterTool(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	tool, exists := s.tools[name]
	if !exists {
		return false
	}

	delete(s.tools, name)

	if tool.Visibility == ToolVisibilityNative {
		idx := sort.Search(len(s.nativeToolCache), func(i int) bool {
			return s.nativeToolCache[i].Name >= name
		})
		if idx < len(s.nativeToolCache) && s.nativeToolCache[idx].Name == name {
			s.nativeToolCache = append(s.nativeToolCache[:idx], s.nativeToolCache[idx+1:]...)
		}
	} else {
		s.internalRegistry.UnregisterTool(name)
	}

	s.hasDiscoverableTools = false
	s.recalcHasDiscoverableToolsLocked()

	s.NotifyToolsChanged()
	return true
}

// rebuildNativeToolCacheLocked rebuilds the native tool cache from all native tools.
// Must be called with s.mu held.
func (s *Server) rebuildNativeToolCacheLocked() {
	s.nativeToolCache = make([]MCPTool, 0)
	for _, tool := range s.tools {
		if tool.Visibility == ToolVisibilityNative {
			toolItem := MCPTool{
				Name:        tool.Name,
				Description: tool.Description,
				InputSchema: tool.Schema,
				Meta:        tool.Meta,
				Icons:       tool.Icons,
			}
			if tool.OutputSchema != nil {
				toolItem.OutputSchema = tool.OutputSchema
			}
			s.nativeToolCache = append(s.nativeToolCache, toolItem)
		}
	}

	// Sort for consistent ordering
	sort.Slice(s.nativeToolCache, func(i, j int) bool {
		return s.nativeToolCache[i].Name < s.nativeToolCache[j].Name
	})
}

// ToolRegistration pairs a tool builder with its handler for batch registration.
type ToolRegistration struct {
	Tool    *ToolBuilder
	Handler ToolHandler
}

// NewToolRegistration creates a tool registration for use with RegisterTools.
func NewToolRegistration(tool *ToolBuilder, handler ToolHandler) *ToolRegistration {
	return &ToolRegistration{Tool: tool, Handler: handler}
}
