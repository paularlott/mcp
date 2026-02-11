package mcp

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// SearchResult represents a tool found via search
type SearchResult struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Score       float64     `json:"score"`
	InputSchema interface{} `json:"input_schema,omitempty"`
}

// internalRegistry implements ToolRegistry and provides tool search functionality
type internalRegistry struct {
	mu    sync.RWMutex
	tools map[string]*internalRegisteredTool
}

// internalRegisteredTool holds a tool registered with the internal registry
type internalRegisteredTool struct {
	tool     *MCPTool
	keywords []string
	handler  ToolHandler
}

// newInternalRegistry creates a new internal tool registry
func newInternalRegistry() *internalRegistry {
	return &internalRegistry{
		tools: make(map[string]*internalRegisteredTool),
	}
}

// RegisterTool registers a searchable tool
func (r *internalRegistry) RegisterTool(tool *ToolBuilder, handler ToolHandler, keywords ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	schema := tool.BuildSchema()
	outputSchema := tool.BuildOutputSchema()

	mcpTool := &MCPTool{
		Name:        tool.Name(),
		Description: tool.Description(),
		InputSchema: schema,
		Keywords:    keywords,
	}
	if outputSchema != nil {
		mcpTool.OutputSchema = outputSchema
	}

	r.tools[tool.Name()] = &internalRegisteredTool{
		tool:     mcpTool,
		keywords: keywords,
		handler:  handler,
	}
}

// RegisterMCPTool registers an already-built MCPTool
func (r *internalRegistry) RegisterMCPTool(tool *MCPTool, handler ToolHandler, keywords ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Combine provided keywords with any already on the tool
	allKeywords := append([]string{}, tool.Keywords...)
	allKeywords = append(allKeywords, keywords...)

	r.tools[tool.Name] = &internalRegisteredTool{
		tool:     tool,
		keywords: allKeywords,
		handler:  handler,
	}
}

// GetRegisteredTools returns all tools in the registry
func (r *internalRegistry) GetRegisteredTools() []MCPTool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]MCPTool, 0, len(r.tools))
	for _, rt := range r.tools {
		tools = append(tools, *rt.tool)
	}
	return tools
}

// Search finds tools matching the query
func (r *internalRegistry) Search(ctx context.Context, query string, maxResults int) []SearchResult {
	return r.SearchWithAdditionalTools(ctx, query, maxResults, nil)
}

// SearchWithAdditionalTools finds tools matching the query, including additional tools passed in.
// The additional tools are typically discoverable tools from providers.
func (r *internalRegistry) SearchWithAdditionalTools(ctx context.Context, query string, maxResults int, additionalTools []MCPTool) []SearchResult {
	r.mu.RLock()
	toolsCopy := make(map[string]*internalRegisteredTool, len(r.tools))
	for k, v := range r.tools {
		toolsCopy[k] = v
	}
	r.mu.RUnlock()

	var results []SearchResult
	queryLower := strings.ToLower(strings.TrimSpace(query))
	listAll := queryLower == ""
	seen := make(map[string]bool)

	// Search registered tools (statically registered discoverable tools)
	for _, dt := range toolsCopy {
		var score float64
		if listAll {
			score = 1.0
		} else {
			score = calculateScore(queryLower, dt.tool.Name, dt.tool.Description, dt.keywords)
		}
		if score > 0 {
			results = append(results, SearchResult{
				Name:        dt.tool.Name,
				Description: dt.tool.Description,
				Score:       score,
				InputSchema: dt.tool.InputSchema,
			})
			seen[dt.tool.Name] = true
		}
	}

	// Search additional tools (discoverable tools from providers)
	for _, tool := range additionalTools {
		if seen[tool.Name] {
			continue
		}
		var score float64
		if listAll {
			score = 1.0
		} else {
			score = calculateScore(queryLower, tool.Name, tool.Description, tool.Keywords)
		}
		if score > 0 {
			results = append(results, SearchResult{
				Name:        tool.Name,
				Description: tool.Description,
				Score:       score,
				InputSchema: tool.InputSchema,
			})
			seen[tool.Name] = true
		}
	}

	// Sort by score descending, then by name
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

// GetTool retrieves a tool by name
func (r *internalRegistry) GetTool(ctx context.Context, name string) (*MCPTool, error) {
	r.mu.RLock()
	if dt, exists := r.tools[name]; exists {
		r.mu.RUnlock()
		return dt.tool, nil
	}
	r.mu.RUnlock()

	// Check context providers
	for _, provider := range GetToolProviders(ctx) {
		tools, err := provider.GetTools(ctx)
		if err != nil {
			continue
		}
		for _, tool := range tools {
			if tool.Name == name {
				return &tool, nil
			}
		}
	}

	return nil, ErrUnknownTool
}

// CallTool executes a tool by name
func (r *internalRegistry) CallTool(ctx context.Context, name string, args map[string]interface{}) (*ToolResponse, error) {
	// Check registered tools first
	r.mu.RLock()
	if dt, exists := r.tools[name]; exists {
		handler := dt.handler
		tool := dt.tool
		r.mu.RUnlock()

		// Validate required parameters
		if err := validateRequiredParameters(tool.InputSchema, args); err != nil {
			return nil, err
		}

		return handler(ctx, NewToolRequest(args))
	}
	r.mu.RUnlock()

	// Try context providers
	for _, provider := range GetToolProviders(ctx) {
		result, err := provider.ExecuteTool(ctx, name, args)
		if err == ErrUnknownTool {
			continue
		}
		if err != nil {
			return nil, err
		}
		return convertToToolResponse(result), nil
	}

	return nil, ErrUnknownTool
}

// Search scoring functions

func calculateScore(queryLower, name, description string, keywords []string) float64 {
	nameLower := strings.ToLower(name)
	descLower := strings.ToLower(description)

	if nameLower == queryLower {
		return 1.0
	}

	queryWords := strings.Fields(queryLower)
	if len(queryWords) <= 1 {
		return calculateSingleWordScore(queryLower, nameLower, descLower, keywords)
	}

	var totalScore float64
	matchedWords := 0

	for _, word := range queryWords {
		wordScore := calculateSingleWordScore(word, nameLower, descLower, keywords)
		if wordScore > 0 {
			matchedWords++
			totalScore += wordScore
		}
	}

	if matchedWords == 0 {
		return 0
	}

	avgScore := totalScore / float64(len(queryWords))
	matchRatio := float64(matchedWords) / float64(len(queryWords))

	if matchedWords == len(queryWords) {
		return avgScore * 0.9
	}

	return avgScore * matchRatio
}

func calculateSingleWordScore(word, nameLower, descLower string, keywords []string) float64 {
	var score float64

	if strings.HasPrefix(nameLower, word) {
		score = max(score, 0.9)
	}

	if strings.Contains(nameLower, word) {
		score = max(score, 0.8)
	}

	for _, kw := range keywords {
		kwLower := strings.ToLower(kw)
		if kwLower == word {
			score = max(score, 0.85)
		} else if strings.Contains(kwLower, word) {
			score = max(score, 0.7)
		}
	}

	if containsWord(descLower, word) {
		score = max(score, 0.6)
	} else if strings.Contains(descLower, word) {
		score = max(score, 0.5)
	}

	if score == 0 {
		if fuzzyScore := fuzzyMatch(word, nameLower); fuzzyScore > 0.6 {
			score = max(score, fuzzyScore*0.7)
		}

		for _, kw := range keywords {
			if fuzzyScore := fuzzyMatch(word, strings.ToLower(kw)); fuzzyScore > 0.6 {
				score = max(score, fuzzyScore*0.6)
			}
		}
	}

	return score
}

func containsWord(text, query string) bool {
	words := strings.Fields(text)
	for _, word := range words {
		word = strings.Trim(word, ".,;:!?()[]{}\"'")
		if strings.ToLower(word) == query {
			return true
		}
	}
	return false
}

func fuzzyMatch(query, target string) float64 {
	if len(query) == 0 || len(target) == 0 {
		return 0
	}

	distance := levenshteinDistance(query, target)
	maxLen := max(len(query), len(target))

	return 1.0 - float64(distance)/float64(maxLen)
}

func levenshteinDistance(s1, s2 string) int {
	if len(s1) == 0 {
		return len(s2)
	}
	if len(s2) == 0 {
		return len(s1)
	}

	r1 := []rune(s1)
	r2 := []rune(s2)
	m := len(r1)
	n := len(r2)

	prev := make([]int, n+1)
	curr := make([]int, n+1)

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
				prev[j]+1,
				curr[j-1]+1,
				prev[j-1]+cost,
			)
		}
		prev, curr = curr, prev
	}

	return prev[n]
}

// validateRequiredParameters checks if all required parameters are present and non-empty
func validateRequiredParameters(inputSchema interface{}, args map[string]interface{}) error {
	schema, ok := inputSchema.(map[string]interface{})
	if !ok {
		return nil
	}

	required, ok := schema["required"].([]interface{})
	if !ok {
		return nil
	}

	for _, req := range required {
		paramName, ok := req.(string)
		if !ok {
			continue
		}

		val, exists := args[paramName]
		if !exists {
			return NewToolError(ErrorCodeInvalidParams, "missing required parameter: "+paramName, nil)
		}

		// Check for empty string
		if strVal, ok := val.(string); ok && strVal == "" {
			return NewToolError(ErrorCodeInvalidParams, "required parameter cannot be empty: "+paramName, nil)
		}

		// Check for nil
		if val == nil {
			return NewToolError(ErrorCodeInvalidParams, "required parameter cannot be null: "+paramName, nil)
		}
	}

	return nil
}
