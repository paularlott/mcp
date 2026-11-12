package mcp

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/paularlott/cli/fuzzy"
)

// SearchResult represents a tool found via search
type SearchResult struct {
	Name        string           `json:"name"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description"`
	Annotations *ToolAnnotations `json:"annotations,omitempty"`
	Score       float64          `json:"score"`
	InputSchema any              `json:"inputSchema,omitempty"`
	Keywords    []string         `json:"keywords,omitempty"`
	Meta        map[string]any   `json:"_meta,omitempty"` // Extension metadata, e.g. MCP Apps' ui.resourceUri
	Icons       []Icon           `json:"icons,omitempty"`
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
		Title:       tool.title,
		Description: tool.Description(),
		Annotations: tool.toolAnnotations(),
		InputSchema: schema,
		Meta:        tool.meta,
		Icons:       tool.icons,
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

// UnregisterTool removes a tool by name from the registry.
func (r *internalRegistry) UnregisterTool(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
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

// SearchWithAdditionalTools finds tools matching the query, including additional tools passed in.
// The additional tools are typically discoverable tools from providers.
// listedTools are additional searchable tools that may already appear in tools/list.
func (r *internalRegistry) SearchWithAdditionalTools(ctx context.Context, query string, maxResults int, additionalTools []MCPTool, listedTools []MCPTool) []SearchResult {
	r.mu.RLock()
	toolsCopy := make(map[string]*internalRegisteredTool, len(r.tools))
	for k, v := range r.tools {
		toolsCopy[k] = v
	}
	r.mu.RUnlock()

	// Every tool is a candidate once: registered tools first, then the
	// providers' discoverable tools, then the listed tools.
	var candidates []SearchResult
	seen := make(map[string]bool)
	add := func(tool *MCPTool, keywords []string) {
		if seen[tool.Name] {
			return
		}
		seen[tool.Name] = true
		candidates = append(candidates, SearchResult{
			Name:        tool.Name,
			Title:       tool.Title,
			Description: tool.Description,
			Annotations: tool.Annotations,
			InputSchema: tool.InputSchema,
			Keywords:    keywords,
			Meta:        tool.Meta,
			Icons:       tool.Icons,
		})
	}
	registeredNames := make([]string, 0, len(toolsCopy))
	for name := range toolsCopy {
		registeredNames = append(registeredNames, name)
	}
	sort.Strings(registeredNames)
	for _, name := range registeredNames {
		dt := toolsCopy[name]
		add(dt.tool, dt.keywords)
	}
	for i := range additionalTools {
		add(&additionalTools[i], additionalTools[i].Keywords)
	}
	for i := range listedTools {
		add(&listedTools[i], listedTools[i].Keywords)
	}

	queryLower := strings.ToLower(strings.TrimSpace(query))
	var results []SearchResult
	if queryLower == "" {
		for _, c := range candidates {
			c.Score = 1.0
			results = append(results, c)
		}
		sortSearchResults(results)
		if maxResults > 0 && len(results) > maxResults {
			results = results[:maxResults]
		}
		return results
	}

	return rankSearchCandidates(queryLower, candidates, maxResults)
}

func sortSearchResults(results []SearchResult) {
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Name < results[j].Name
	})
}

// GetTool retrieves a tool by name

// CallTool executes a tool by name

// Search scoring
//
// A query is reduced to its meaningful words (searchTerms): stopwords such as
// "the", "by" or "who" are dropped and each word is stemmed, so "visualising"
// matches "visualize" and "opportunities" matches "opportunity". Each word is
// scored against each tool with the match ladder in calculateSingleWordScore
// (name, then keywords, then description).
//
// The word scores are combined as a root mean square, weighted by how rare
// each word is among the candidate tools (inverse document frequency):
//
//   - A word most tools match, such as "created" or "list", says little about
//     which tool is wanted; a word few tools match, such as "chart" or
//     "invoice", says a lot. Without this weighting, a tool whose long
//     description contains several generic query words outranks the one tool
//     named after the subject of the query.
//   - The root mean square favours strong matches: a name or keyword match on
//     one word counts for more than description mentions of two, so detailed
//     descriptions are not rewarded for their length.
//   - A word no candidate matches at all is left out, rather than counted as a
//     miss against every tool.
//
// Finally, the best tool for each strongly matched word (a name or keyword
// match) is kept in the results even when the combined score would cut it, so
// a query with two intents - "chart opportunities by value" - returns a tool
// for each.

// strongWordMatch is the lowest per-word score from a name or keyword match.
const strongWordMatch = 0.85

func rankSearchCandidates(queryLower string, candidates []SearchResult, maxResults int) []SearchResult {
	terms := searchTerms(queryLower)
	texts := make([]searchText, len(candidates))
	for i, c := range candidates {
		texts[i] = newSearchText(c.Name, c.Description, c.Keywords)
	}

	// matches[i][j]: how well candidate i matches term j.
	matches := make([][]float64, len(candidates))
	docFreq := make([]int, len(terms))
	for i := range candidates {
		matches[i] = make([]float64, len(terms))
		for j, term := range terms {
			matches[i][j] = texts[i].wordScore(term)
			if matches[i][j] > 0 {
				docFreq[j]++
			}
		}
	}

	weights := make([]float64, len(terms))
	var totalWeight float64
	for j := range terms {
		if docFreq[j] == 0 {
			continue
		}
		weights[j] = math.Log(1 + float64(len(candidates))/float64(docFreq[j]))
		totalWeight += weights[j]
	}

	queryAsName := strings.Join(strings.Fields(queryLower), "_")
	var results []SearchResult
	for i, c := range candidates {
		var score float64
		if texts[i].nameLower == queryLower || texts[i].nameLower == queryAsName {
			score = 1.0
		} else if totalWeight > 0 {
			// Root mean square of the weighted word scores: a strong match on a
			// word counts for more than two weak ones, so a description that
			// merely mentions several query words does not outrank a name or
			// keyword match. A one-word query scores its match as it stands.
			for j := range terms {
				score += weights[j] * matches[i][j] * matches[i][j]
			}
			score = math.Sqrt(score / totalWeight)
		}
		if score > 0 {
			c.Score = score
			results = append(results, c)
		}
	}
	sortSearchResults(results)

	if maxResults <= 0 || len(results) <= maxResults {
		return results
	}

	// The best tool for each strongly matched word stays in the results.
	position := make(map[string]int, len(results))
	for i, r := range results {
		position[r.Name] = i
	}
	required := make(map[string]bool)
	for j := range terms {
		if weights[j] == 0 {
			continue
		}
		best, bestMatch, bestScore := "", 0.0, 0.0
		for i, c := range candidates {
			m := matches[i][j]
			if m < strongWordMatch {
				continue
			}
			if _, ok := position[c.Name]; !ok {
				continue
			}
			score := results[position[c.Name]].Score
			if m > bestMatch || (m == bestMatch && score > bestScore) {
				best, bestMatch, bestScore = c.Name, m, score
			}
		}
		if best != "" {
			required[best] = true
		}
	}

	top := append([]SearchResult(nil), results[:maxResults]...)
	inTop := make(map[string]bool, len(top))
	for _, r := range top {
		inTop[r.Name] = true
	}
	for _, r := range results[maxResults:] {
		if !required[r.Name] {
			continue
		}
		// Replace the lowest-scoring result that is not itself required.
		for k := len(top) - 1; k >= 0; k-- {
			if !required[top[k].Name] {
				inTop[top[k].Name] = false
				top[k] = r
				inTop[r.Name] = true
				break
			}
		}
	}
	sortSearchResults(top)
	return top
}

// calculateScore scores one tool against a query on its own, with every query
// word weighted equally: there are no other tools to weigh the words against.
func calculateScore(queryLower, name, description string, keywords []string) float64 {
	nameLower := strings.ToLower(name)
	if nameLower == queryLower {
		return 1.0
	}

	terms := searchTerms(queryLower)
	if len(terms) == 0 {
		return 0
	}
	text := newSearchText(name, description, keywords)
	var total float64
	for _, term := range terms {
		total += text.wordScore(term)
	}
	// Unmatched words count as 0, so a partial match scores in proportion to
	// how much of the query it covers, without a further penalty.
	return total / float64(len(terms))
}

// searchText is a tool's searchable text, as written and stemmed.
type searchText struct {
	nameLower    string
	descLower    string
	keywords     []string
	stemName     string
	stemDesc     string
	stemKeywords []string
}

func newSearchText(name, description string, keywords []string) searchText {
	t := searchText{
		nameLower: strings.ToLower(name),
		descLower: strings.ToLower(description),
		keywords:  keywords,
	}
	nameParts := nameTokens(t.nameLower)
	for i, part := range nameParts {
		nameParts[i] = stemWord(part)
	}
	t.stemName = strings.Join(nameParts, "_")
	t.stemDesc = strings.Join(stemWords(splitWords(t.descLower)), " ")
	t.stemKeywords = make([]string, len(keywords))
	for i, kw := range keywords {
		t.stemKeywords[i] = strings.Join(stemWords(strings.Fields(strings.ToLower(kw))), " ")
	}
	return t
}

// wordScore is the better of the word's match as written and its stem's match
// against the stemmed text.
func (t searchText) wordScore(word string) float64 {
	score := calculateSingleWordScore(word, t.nameLower, t.descLower, t.keywords)
	if stem := stemWord(word); stem != "" {
		score = max(score, calculateSingleWordScore(stem, t.stemName, t.stemDesc, t.stemKeywords))
	}
	return score
}

// searchStopwords are words that say nothing about which tool is wanted.
var searchStopwords = map[string]bool{
	"a": true, "an": true, "the": true, "of": true, "for": true, "to": true, "in": true, "on": true,
	"at": true, "by": true, "with": true, "from": true, "and": true, "or": true, "is": true, "are": true,
	"was": true, "were": true, "be": true, "been": true, "am": true, "do": true, "does": true, "did": true,
	"i": true, "me": true, "my": true, "we": true, "our": true, "us": true, "you": true, "your": true,
	"it": true, "its": true, "this": true, "that": true, "these": true, "those": true, "there": true,
	"what": true, "which": true, "who": true, "whom": true, "whose": true, "how": true, "when": true,
	"where": true, "why": true, "can": true, "could": true, "would": true, "should": true, "will": true,
	"please": true, "into": true, "about": true, "any": true, "some": true, "all": true, "just": true,
}

// searchTerms is the query's meaningful words: lowercased, split on spaces and
// punctuation, without stopwords or repeats. A query made only of stopwords
// keeps its words.
func searchTerms(queryLower string) []string {
	words := splitWords(queryLower)
	var terms []string
	seen := make(map[string]bool)
	for _, w := range words {
		if searchStopwords[w] || seen[w] {
			continue
		}
		seen[w] = true
		terms = append(terms, w)
	}
	if len(terms) == 0 {
		for _, w := range words {
			if !seen[w] {
				seen[w] = true
				terms = append(terms, w)
			}
		}
	}
	return terms
}

// splitWords splits lowercased text into words on anything but letters and
// digits, keeping underscores so a tool name in a query stays one word.
func splitWords(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
}

func stemWords(words []string) []string {
	out := make([]string, 0, len(words))
	for _, w := range words {
		if s := stemWord(w); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// stemWord reduces an English word to a stem shared by its common forms, so
// "opportunities"/"opportunity", "visualise"/"visualizing" and
// "created"/"creates"/"create" compare equal. It is deliberately light: it
// only has to make a word and its variants agree, not produce a real word.
func stemWord(w string) string {
	if len(w) <= 3 || strings.ContainsAny(w, "0123456789_") {
		return w
	}
	// British -ise spellings to -ize.
	for _, pair := range [][2]string{{"isation", "ization"}, {"ising", "izing"}, {"ised", "ized"}, {"ises", "izes"}, {"ise", "ize"}} {
		if strings.HasSuffix(w, pair[0]) && len(w) > len(pair[0])+2 {
			w = strings.TrimSuffix(w, pair[0]) + pair[1]
			break
		}
	}
	switch {
	case strings.HasSuffix(w, "ies") && len(w) > 4:
		w = strings.TrimSuffix(w, "ies") + "y"
	case strings.HasSuffix(w, "sses"):
		w = strings.TrimSuffix(w, "es")
	case strings.HasSuffix(w, "ss"), strings.HasSuffix(w, "us"), strings.HasSuffix(w, "is"):
		// class, status, analysis
	case strings.HasSuffix(w, "s"):
		w = strings.TrimSuffix(w, "s")
	}
	for _, suffix := range []string{"ing", "ed"} {
		if strings.HasSuffix(w, suffix) && len(w)-len(suffix) >= 4 {
			w = strings.TrimSuffix(w, suffix)
			// running -> run, planned -> plan
			if n := len(w); n >= 2 && w[n-1] == w[n-2] && !strings.ContainsRune("lsz", rune(w[n-1])) {
				w = w[:n-1]
			}
			break
		}
	}
	// create/created/creating all end as "creat".
	if strings.HasSuffix(w, "e") && len(w) > 4 {
		w = strings.TrimSuffix(w, "e")
	}
	return w
}

func calculateSingleWordScore(word, nameLower, descLower string, keywords []string) float64 {
	var score float64

	if strings.HasPrefix(nameLower, word) {
		score = max(score, 0.95)
	}

	// Tokenise the tool name on common separators so a query word matches a
	// segment of a multi-word tool name as a real word, not just a substring.
	// e.g. query 'send' against name 'tools_send_email' should score as a
	// token match. The tool's own name is the strongest signal of intent, so
	// these tiers sit above keyword-tag matches.
	for _, token := range nameTokens(nameLower) {
		if token == word {
			score = max(score, 0.92)
		} else if strings.HasPrefix(token, word) {
			score = max(score, 0.9)
		}
	}

	if strings.Contains(nameLower, word) {
		score = max(score, 0.8)
	}

	for _, kw := range keywords {
		kwLower := strings.ToLower(kw)
		if kwLower == word {
			score = max(score, 0.85)
			continue
		}

		// Tokenise multi-word keyword phrases (e.g. 'work request' becomes
		// ['work', 'request']) so an individual word in a phrase is still a
		// strong match. A developer tagging 'work request' is also tagging
		// the topic 'work' and the topic 'request'.
		matchedToken := false
		for _, kwToken := range strings.Fields(kwLower) {
			if kwToken == word {
				score = max(score, 0.85)
				matchedToken = true
				break
			}
		}
		if matchedToken {
			continue
		}

		if strings.Contains(kwLower, word) {
			score = max(score, 0.7)
		}
	}

	if containsWord(descLower, word) {
		score = max(score, 0.6)
	} else if strings.Contains(descLower, word) {
		score = max(score, 0.5)
	}

	if score == 0 {
		if fuzzyScore := fuzzy.Score(word, nameLower); fuzzyScore > 0.6 {
			score = max(score, fuzzyScore*0.7)
		}

		for _, kw := range keywords {
			if fuzzyScore := fuzzy.Score(word, strings.ToLower(kw)); fuzzyScore > 0.6 {
				score = max(score, fuzzyScore*0.6)
			}
		}
	}

	return score
}

// nameTokens splits a tool name on common separators (_ and -) into its
// constituent words. Used for token-aware matching in tool_search so that a
// query word matches a segment of a multi-word tool name as a real word
// rather than a raw substring.
func nameTokens(nameLower string) []string {
	return strings.FieldsFunc(nameLower, func(r rune) bool {
		return r == '_' || r == '-'
	})
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
