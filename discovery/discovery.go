// Package discovery provides tool discovery functionality for MCP servers.
// This package allows you to register tools that are hidden from the main tools/list response
// but can be discovered via search and executed through a wrapper tool.
//
// This is useful when you have many tools and want to reduce context window usage.
// Instead of sending all tool definitions to the LLM upfront, you can:
// 1. Register essential tools normally with the MCP server
// 2. Register specialized tools with a ToolRegistry
// 3. Attach the registry to the server - it registers tool_search and execute_tool
//
// The workflow for LLMs becomes:
//  1. tool_search(query="email") -> finds tools with full schemas
//  2. execute_tool(name="send_email", arguments={...}) -> executes the tool
package discovery

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/paularlott/mcp"
)

// ToolMetadata contains searchable information about a tool
type ToolMetadata struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Keywords    []string `json:"keywords,omitempty"`
}

// SearchResult represents a matched tool from a search
type SearchResult struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Score       float64     `json:"score"`
	InputSchema interface{} `json:"inputSchema,omitempty"`
}

// ToolProvider allows external tool sources (scripts, plugins, databases, etc.)
type ToolProvider interface {
	// ListToolMetadata returns metadata for all searchable tools from this provider
	ListToolMetadata(ctx context.Context) ([]ToolMetadata, error)

	// GetTool returns the full tool definition for a specific tool by name
	// Returns nil, nil if the tool doesn't exist in this provider
	GetTool(ctx context.Context, name string) (*mcp.MCPTool, error)

	// CallTool executes a tool by name with the given arguments
	// Returns ErrToolNotFound if the tool doesn't exist in this provider
	CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.ToolResponse, error)
}

// ErrToolNotFound is returned when a tool is not found
var ErrToolNotFound = mcp.ErrUnknownTool

// contextKey is a type for context keys to avoid collisions
type contextKey string

// requestProvidersKey is the context key for request-scoped providers
const requestProvidersKey contextKey = "discovery.request_providers"

// WithRequestProviders adds request-scoped tool providers to the context.
// These providers are only available for the duration of the request.
// Use this for per-user or per-tenant tool providers.
func WithRequestProviders(ctx context.Context, providers ...ToolProvider) context.Context {
	return context.WithValue(ctx, requestProvidersKey, providers)
}

// getRequestProviders retrieves request-scoped providers from context
func getRequestProviders(ctx context.Context) []ToolProvider {
	if providers, ok := ctx.Value(requestProvidersKey).([]ToolProvider); ok {
		return providers
	}
	return nil
}

// registeredTool represents a tool that won't appear in ListTools but can be searched and called
type registeredTool struct {
	tool     *mcp.MCPTool
	metadata ToolMetadata
	handler  mcp.ToolHandler
}

// ToolRegistry manages searchable tools and provides discovery functionality.
// Tools registered here are hidden from tools/list but can be discovered via search.
// Create one instance and attach it to your MCP server.
type ToolRegistry struct {
	mu        sync.RWMutex
	tools     map[string]*registeredTool
	providers []ToolProvider
}

// NewToolRegistry creates a new tool registry for searchable tools
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools:     make(map[string]*registeredTool),
		providers: make([]ToolProvider, 0),
	}
}

// RegisterTool registers a searchable tool that won't appear in ListTools but can be discovered and called.
// Keywords are used for fuzzy search matching.
func (r *ToolRegistry) RegisterTool(tool *mcp.ToolBuilder, handler mcp.ToolHandler, keywords ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Build the MCP tool using the builder's exported method
	schema := tool.BuildSchema()
	outputSchema := tool.BuildOutputSchema()

	mcpTool := &mcp.MCPTool{
		Name:        tool.Name(),
		Description: tool.Description(),
		InputSchema: schema,
	}
	if outputSchema != nil {
		mcpTool.OutputSchema = outputSchema
	}

	r.tools[tool.Name()] = &registeredTool{
		tool: mcpTool,
		metadata: ToolMetadata{
			Name:        tool.Name(),
			Description: tool.Description(),
			Keywords:    keywords,
		},
		handler: handler,
	}
}

// AddProvider adds a dynamic tool provider
func (r *ToolRegistry) AddProvider(provider ToolProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers = append(r.providers, provider)
}

// RemoveProvider removes a dynamic tool provider
func (r *ToolRegistry) RemoveProvider(provider ToolProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()

	filtered := make([]ToolProvider, 0, len(r.providers))
	for _, p := range r.providers {
		if p != provider {
			filtered = append(filtered, p)
		}
	}
	r.providers = filtered
}

// ListToolMetadata returns metadata for all tools registered in this registry.
// This implements the ToolProvider interface, allowing a ToolRegistry to be used
// as a request-scoped provider via WithRequestProviders.
func (r *ToolRegistry) ListToolMetadata(ctx context.Context) ([]ToolMetadata, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	metadata := make([]ToolMetadata, 0, len(r.tools))
	for _, rt := range r.tools {
		metadata = append(metadata, rt.metadata)
	}
	return metadata, nil
}

// Ensure ToolRegistry implements ToolProvider
var _ ToolProvider = (*ToolRegistry)(nil)

// Search performs fuzzy search across all registered and dynamic tools.
// If query is empty, returns all tools.
func (r *ToolRegistry) Search(ctx context.Context, query string, maxResults int) []SearchResult {
	r.mu.RLock()
	toolsCopy := make(map[string]*registeredTool, len(r.tools))
	for k, v := range r.tools {
		toolsCopy[k] = v
	}
	providersCopy := make([]ToolProvider, len(r.providers))
	copy(providersCopy, r.providers)
	r.mu.RUnlock()

	// Also get request-scoped providers from context
	requestProviders := getRequestProviders(ctx)

	var results []SearchResult
	queryLower := strings.ToLower(strings.TrimSpace(query))
	listAll := queryLower == ""
	seen := make(map[string]bool)

	// Search registered tools (we have the schema directly)
	for _, dt := range toolsCopy {
		var score float64
		if listAll {
			score = 1.0 // All tools get same score when listing all
		} else {
			score = calculateScore(queryLower, dt.metadata)
		}
		if score > 0 {
			results = append(results, SearchResult{
				Name:        dt.metadata.Name,
				Description: dt.metadata.Description,
				Score:       score,
				InputSchema: dt.tool.InputSchema,
			})
			seen[dt.metadata.Name] = true
		}
	}

	// Search global providers
	for _, provider := range providersCopy {
		metadata, err := provider.ListToolMetadata(ctx)
		if err != nil {
			continue // Log error but don't fail
		}
		for _, meta := range metadata {
			if seen[meta.Name] {
				continue // Skip duplicates (registered tools take priority)
			}
			var score float64
			if listAll {
				score = 1.0
			} else {
				score = calculateScore(queryLower, meta)
			}
			if score > 0 {
				result := SearchResult{
					Name:        meta.Name,
					Description: meta.Description,
					Score:       score,
				}
				// Try to get the full schema
				if tool, err := provider.GetTool(ctx, meta.Name); err == nil && tool != nil {
					result.InputSchema = tool.InputSchema
				}
				results = append(results, result)
				seen[meta.Name] = true
			}
		}
	}

	// Search request-scoped providers
	for _, provider := range requestProviders {
		metadata, err := provider.ListToolMetadata(ctx)
		if err != nil {
			continue
		}
		for _, meta := range metadata {
			if seen[meta.Name] {
				continue
			}
			var score float64
			if listAll {
				score = 1.0
			} else {
				score = calculateScore(queryLower, meta)
			}
			if score > 0 {
				result := SearchResult{
					Name:        meta.Name,
					Description: meta.Description,
					Score:       score,
				}
				// Try to get the full schema
				if tool, err := provider.GetTool(ctx, meta.Name); err == nil && tool != nil {
					result.InputSchema = tool.InputSchema
				}
				results = append(results, result)
				seen[meta.Name] = true
			}
		}
	}

	// Sort by score descending, then by name for stable ordering when scores are equal
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Name < results[j].Name
	})

	if maxResults > 0 && len(results) > maxResults {
		results = results[:maxResults]
	}

	return results
}

// GetTool retrieves a registered or dynamic tool by name
func (r *ToolRegistry) GetTool(ctx context.Context, name string) (*mcp.MCPTool, error) {
	r.mu.RLock()
	if dt, exists := r.tools[name]; exists {
		r.mu.RUnlock()
		return dt.tool, nil
	}
	providersCopy := make([]ToolProvider, len(r.providers))
	copy(providersCopy, r.providers)
	r.mu.RUnlock()

	// Check global providers
	for _, provider := range providersCopy {
		tool, err := provider.GetTool(ctx, name)
		if err != nil {
			continue
		}
		if tool != nil {
			return tool, nil
		}
	}

	// Check request-scoped providers
	for _, provider := range getRequestProviders(ctx) {
		tool, err := provider.GetTool(ctx, name)
		if err != nil {
			continue
		}
		if tool != nil {
			return tool, nil
		}
	}

	return nil, ErrToolNotFound
}

// CallTool attempts to call a registered or dynamic tool
func (r *ToolRegistry) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.ToolResponse, error) {
	r.mu.RLock()
	if dt, exists := r.tools[name]; exists {
		handler := dt.handler
		r.mu.RUnlock()
		return handler(ctx, mcp.NewToolRequest(args))
	}
	providersCopy := make([]ToolProvider, len(r.providers))
	copy(providersCopy, r.providers)
	r.mu.RUnlock()

	// Check global providers
	for _, provider := range providersCopy {
		result, err := provider.CallTool(ctx, name, args)
		if err == ErrToolNotFound {
			continue // Try next provider
		}
		if err != nil {
			return nil, err
		}
		return result, nil
	}

	// Check request-scoped providers
	for _, provider := range getRequestProviders(ctx) {
		result, err := provider.CallTool(ctx, name, args)
		if err == ErrToolNotFound {
			continue
		}
		if err != nil {
			return nil, err
		}
		return result, nil
	}

	return nil, ErrToolNotFound
}

// Attach registers the discovery tools (tool_search, execute_tool) with the MCP server
func (r *ToolRegistry) Attach(server *mcp.Server) {
	// Register tool_search
	server.RegisterTool(
		mcp.NewTool("tool_search", "Search for available tools by name, description, or keywords. Returns matching tools with their names, descriptions, input schemas, and relevance scores. IMPORTANT: After finding tools with this search, you MUST use execute_tool to call them - discovered tools cannot be called directly. Omit query to list all available tools.",
			mcp.String("query", "Search query to find relevant tools (searches name, description, and keywords). Omit to list all tools."),
			mcp.Number("max_results", "Maximum number of results to return (default: 10, max: 50)"),
		),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			query := req.StringOr("query", "")

			maxResults := req.IntOr("max_results", 10)
			if maxResults <= 0 {
				maxResults = 10
			}
			if maxResults > 50 {
				maxResults = 50
			}

			results := r.Search(ctx, query, maxResults)

			// Format results for LLM consumption
			if len(results) == 0 {
				return mcp.NewToolResponseText("No tools found. Try different keywords or a broader search term."), nil
			}

			return mcp.NewToolResponseJSON(results), nil
		},
	)

	// Register execute_tool
	server.RegisterTool(
		mcp.NewTool("execute_tool", "Execute a tool by name with the given arguments. This is the ONLY way to call tools discovered via tool_search. Discovered tools cannot be called directly - you must use this execute_tool function.",
			mcp.String("name", "The exact name of the tool to execute (must be a tool found via tool_search)", mcp.Required()),
			mcp.Object("arguments", "The arguments to pass to the tool (matching the schema from tool_search results)"),
		),
		func(ctx context.Context, req *mcp.ToolRequest) (*mcp.ToolResponse, error) {
			name, err := req.String("name")
			if err != nil || name == "" {
				return mcp.NewToolResponseText("Tool name is required"), nil
			}

			args, _ := req.Object("arguments")
			if args == nil {
				args = make(map[string]interface{})
			}

			response, err := r.CallTool(ctx, name, args)
			if err == ErrToolNotFound {
				return mcp.NewToolResponseText("Tool not found: " + name + ". Use tool_search to discover available tools."), nil
			}
			if err != nil {
				return nil, err
			}
			return response, nil
		},
	)
}

// calculateScore computes a fuzzy match score for a query against tool metadata
func calculateScore(queryLower string, meta ToolMetadata) float64 {
	nameLower := strings.ToLower(meta.Name)
	descLower := strings.ToLower(meta.Description)

	// Exact name match - highest score
	if nameLower == queryLower {
		return 1.0
	}

	// Split query into words for multi-word matching
	queryWords := strings.Fields(queryLower)

	// If single word, use original logic
	if len(queryWords) <= 1 {
		return calculateSingleWordScore(queryLower, nameLower, descLower, meta.Keywords)
	}

	// Multi-word query: score based on how many words match
	var totalScore float64
	matchedWords := 0

	for _, word := range queryWords {
		wordScore := calculateSingleWordScore(word, nameLower, descLower, meta.Keywords)
		if wordScore > 0 {
			matchedWords++
			totalScore += wordScore
		}
	}

	if matchedWords == 0 {
		return 0
	}

	// Average score weighted by percentage of words matched
	avgScore := totalScore / float64(len(queryWords))
	matchRatio := float64(matchedWords) / float64(len(queryWords))

	// Boost score if all words matched
	if matchedWords == len(queryWords) {
		return avgScore * 0.9 // Slightly below exact match
	}

	return avgScore * matchRatio
}

// calculateSingleWordScore scores a single word against tool metadata
func calculateSingleWordScore(word, nameLower, descLower string, keywords []string) float64 {
	var score float64

	// Name starts with word
	if strings.HasPrefix(nameLower, word) {
		score = max(score, 0.9)
	}

	// Name contains word
	if strings.Contains(nameLower, word) {
		score = max(score, 0.8)
	}

	// Exact keyword match
	for _, kw := range keywords {
		kwLower := strings.ToLower(kw)
		if kwLower == word {
			score = max(score, 0.85)
		} else if strings.Contains(kwLower, word) {
			score = max(score, 0.7)
		}
	}

	// Description contains word (word boundary aware)
	if containsWord(descLower, word) {
		score = max(score, 0.6)
	} else if strings.Contains(descLower, word) {
		score = max(score, 0.5)
	}

	// Fuzzy matching using Levenshtein-like approach for typo tolerance
	if score == 0 {
		// Try fuzzy match on name
		if fuzzyScore := fuzzyMatch(word, nameLower); fuzzyScore > 0.6 {
			score = max(score, fuzzyScore*0.7) // Scale down fuzzy matches
		}

		// Try fuzzy match on keywords
		for _, kw := range keywords {
			if fuzzyScore := fuzzyMatch(word, strings.ToLower(kw)); fuzzyScore > 0.6 {
				score = max(score, fuzzyScore*0.6)
			}
		}
	}

	return score
}

// containsWord checks if text contains the query as a whole word
func containsWord(text, query string) bool {
	words := strings.Fields(text)
	for _, word := range words {
		// Strip common punctuation
		word = strings.Trim(word, ".,;:!?()[]{}\"'")
		if strings.ToLower(word) == query {
			return true
		}
	}
	return false
}

// fuzzyMatch returns a similarity score between 0 and 1 using a simplified approach
func fuzzyMatch(query, target string) float64 {
	if len(query) == 0 || len(target) == 0 {
		return 0
	}

	// Use Levenshtein distance for fuzzy matching
	distance := levenshteinDistance(query, target)
	maxLen := max(len(query), len(target))

	// Convert distance to similarity score
	similarity := 1.0 - float64(distance)/float64(maxLen)
	return similarity
}

// levenshteinDistance calculates the edit distance between two strings
func levenshteinDistance(s1, s2 string) int {
	if len(s1) == 0 {
		return len(s2)
	}
	if len(s2) == 0 {
		return len(s1)
	}

	// Create matrix
	r1 := []rune(s1)
	r2 := []rune(s2)
	m := len(r1)
	n := len(r2)

	// Use two rows instead of full matrix for space efficiency
	prev := make([]int, n+1)
	curr := make([]int, n+1)

	// Initialize first row
	for j := 0; j <= n; j++ {
		prev[j] = j
	}

	for i := 1; i <= m; i++ {
		curr[0] = i
		for j := 1; j <= n; j++ {
			cost := 0
			if r1[i-1] != r2[j-1] {
				cost = 1
			}
			curr[j] = min(
				prev[j]+1,      // deletion
				curr[j-1]+1,    // insertion
				prev[j-1]+cost, // substitution
			)
		}
		prev, curr = curr, prev
	}

	return prev[n]
}
