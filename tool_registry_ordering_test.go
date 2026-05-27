package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

// runToolSearch is a small helper that registers a slice of tools, invokes
// tool_search end-to-end, and returns the parsed result list in score order.
//
// Each tool is described by a name, description, and keywords list. All tools
// are registered as native so they participate in tool_search results.
func runToolSearch(t *testing.T, query string, tools []toolFixture) []SearchResult {
	t.Helper()

	server := NewServer("test", "1.0.0")

	for _, tool := range tools {
		// Register a discoverable tool alongside so tool_search is available.
		// We capture loop var.
		fixture := tool
		server.RegisterTool(
			NewTool(fixture.name, fixture.description, String("query", "")),
			func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
				return NewToolResponseText("ok"), nil
			},
			fixture.keywords...,
		)
	}

	// Ensure tool_search is registered (it requires at least one discoverable
	// tool in the registry to be auto-installed).
	server.RegisterTool(
		NewTool("__placeholder", "Discoverable placeholder",
			String("q", "")).Discoverable("placeholder"),
		func(ctx context.Context, req *ToolRequest) (*ToolResponse, error) {
			return NewToolResponseText("ok"), nil
		},
	)

	resp, err := server.CallTool(context.Background(), "tool_search", map[string]any{
		"query":       query,
		"max_results": 50,
	})
	if err != nil {
		t.Fatalf("tool_search failed: %v", err)
	}
	if len(resp.Content) == 0 {
		t.Fatalf("tool_search returned no content")
	}

	var results []SearchResult
	if err := json.Unmarshal([]byte(resp.Content[0].Text), &results); err != nil {
		t.Fatalf("failed to parse results: %v\nraw: %s", err, resp.Content[0].Text)
	}

	// Strip the placeholder so callers don't need to think about it.
	filtered := results[:0]
	for _, r := range results {
		if r.Name != "__placeholder" {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

type toolFixture struct {
	name        string
	description string
	keywords    []string
}

// rankOf returns the 0-indexed position of a tool in a result list, or -1 if
// not present.
func rankOf(results []SearchResult, name string) int {
	for i, r := range results {
		if r.Name == name {
			return i
		}
	}
	return -1
}

// TestOrdering_NameTokenBeatsKeywordTag is the canonical case that motivated
// the scoring overhaul: a tool whose name contains the query word as a token
// should rank above a search/aggregate tool that merely tags the word as a
// keyword.
func TestOrdering_NameTokenBeatsKeywordTag(t *testing.T) {
	tools := []toolFixture{
		{
			name:        "get_report_pdf",
			description: "Generate a printable PDF for a single report by ID.",
			keywords:    []string{"report", "pdf", "render"},
		},
		{
			name:        "data_search",
			description: "Full-text search across records including reports and other documents.",
			keywords:    []string{"search", "find", "report", "document"},
		},
	}

	results := runToolSearch(t, "report", tools)

	specificRank := rankOf(results, "get_report_pdf")
	searchRank := rankOf(results, "data_search")

	if specificRank == -1 || searchRank == -1 {
		t.Fatalf("both tools should be returned, got %+v", results)
	}
	if specificRank >= searchRank {
		t.Errorf("get_report_pdf (rank %d) should rank before data_search (rank %d) for query %q",
			specificRank, searchRank, "report")
		for _, r := range results {
			t.Logf("  %s score=%.3f", r.Name, r.Score)
		}
	}
}

// TestOrdering_DirectIDLookupBeatsGenericList ensures that when a user searches
// for a singular entity (e.g. "customer"), a get_<entity> tool ranks above a
// list_<entity>s tool. Both contain the word as a name token, so we rely on
// description/keyword tie-breaking via the singular form being more specific.
func TestOrdering_DirectIDLookupBeatsGenericList(t *testing.T) {
	tools := []toolFixture{
		{
			name:        "get_customer",
			description: "Get full details for a single customer by ID.",
			keywords:    []string{"customer", "details", "lookup"},
		},
		{
			name:        "list_customers",
			description: "Paginated list of customer records, filterable by type.",
			keywords:    []string{"customers", "list", "directory"},
		},
	}

	// Query: "customer" (singular) — both tools have "customer" as a name
	// token (list_customers tokenises to ["list", "customers"], not
	// ["list", "customer"]). The get_ variant should win because its name
	// token matches the query exactly while list_customers only matches via
	// "customer" being a substring of "customers".
	results := runToolSearch(t, "customer", tools)

	getRank := rankOf(results, "get_customer")
	listRank := rankOf(results, "list_customers")

	if getRank == -1 || listRank == -1 {
		t.Fatalf("both tools should be returned, got %+v", results)
	}
	if getRank >= listRank {
		t.Errorf("get_customer (rank %d) should rank before list_customers (rank %d) for query %q",
			getRank, listRank, "customer")
		for _, r := range results {
			t.Logf("  %s score=%.3f", r.Name, r.Score)
		}
	}
}

// TestOrdering_NamePrefixBeatsNameToken verifies the relative ordering of the
// name-prefix tier (full name starts with query) versus the name-token tier
// (a non-first segment matches).
func TestOrdering_NamePrefixBeatsNameToken(t *testing.T) {
	tools := []toolFixture{
		{
			name:        "memory_recall",
			description: "Recall stored memory entries.",
			keywords:    []string{"recall", "search"},
		},
		{
			name:        "ai_memory_dump",
			description: "Dump all AI memory entries for debugging.",
			keywords:    []string{"debug", "dump"},
		},
	}

	results := runToolSearch(t, "memory", tools)

	prefixRank := rankOf(results, "memory_recall")
	tokenRank := rankOf(results, "ai_memory_dump")

	if prefixRank == -1 || tokenRank == -1 {
		t.Fatalf("both tools should be returned, got %+v", results)
	}
	if prefixRank >= tokenRank {
		t.Errorf("memory_recall (prefix, rank %d) should rank before ai_memory_dump (token, rank %d)",
			prefixRank, tokenRank)
		for _, r := range results {
			t.Logf("  %s score=%.3f", r.Name, r.Score)
		}
	}
}

// TestOrdering_MultiWordKeywordPhraseWordMatch verifies that a developer
// tagging a multi-word phrase like "work request" still gets credit for an
// individual word query like "work" — equivalent to having tagged "work" on
// its own.
func TestOrdering_MultiWordKeywordPhraseWordMatch(t *testing.T) {
	tools := []toolFixture{
		{
			name:        "data_lookup",
			description: "Generic lookup tool.",
			keywords:    []string{"work request", "ticket", "task"},
		},
		{
			name:        "unrelated_tool",
			description: "Does something else entirely.",
			keywords:    []string{"misc"},
		},
	}

	results := runToolSearch(t, "work", tools)

	if rank := rankOf(results, "data_lookup"); rank == -1 {
		t.Fatalf("data_lookup should match query %q via 'work request' keyword phrase, got %+v",
			"work", results)
	}

	// Sanity: the unrelated tool should not be returned at all.
	if rank := rankOf(results, "unrelated_tool"); rank != -1 {
		t.Errorf("unrelated_tool should not match query %q, but ranked %d", "work", rank)
	}
}

// TestOrdering_FullMultiWordMatchBeatsPartial verifies that a tool which
// matches every word of a multi-word query ranks above a tool that only
// matches one word.
func TestOrdering_FullMultiWordMatchBeatsPartial(t *testing.T) {
	tools := []toolFixture{
		{
			name:        "send_email",
			description: "Send an email message.",
			keywords:    []string{"email", "mail", "send"},
		},
		{
			name:        "send_sms",
			description: "Send an SMS message.",
			keywords:    []string{"sms", "text", "send"},
		},
	}

	// Query "send email" — first tool matches both words, second matches only "send".
	results := runToolSearch(t, "send email", tools)

	emailRank := rankOf(results, "send_email")
	smsRank := rankOf(results, "send_sms")

	if emailRank == -1 || smsRank == -1 {
		t.Fatalf("both tools should be returned, got %+v", results)
	}
	if emailRank >= smsRank {
		t.Errorf("send_email (full match, rank %d) should rank before send_sms (partial match, rank %d) for query %q",
			emailRank, smsRank, "send email")
		for _, r := range results {
			t.Logf("  %s score=%.3f", r.Name, r.Score)
		}
	}
}

// TestOrdering_ExactNameMatchTopsAll verifies that an exact name match always
// wins, even against a tool whose name token also matches and which has rich
// keywords.
func TestOrdering_ExactNameMatchTopsAll(t *testing.T) {
	tools := []toolFixture{
		{
			name:        "report",
			description: "The report tool.",
			keywords:    nil,
		},
		{
			name:        "report_generator_pro",
			description: "Advanced report generator with templates.",
			keywords:    []string{"report", "pdf", "template", "render"},
		},
	}

	results := runToolSearch(t, "report", tools)

	if len(results) == 0 {
		t.Fatalf("expected results, got none")
	}
	if results[0].Name != "report" {
		t.Errorf("exact name match should rank first; got %q (score %.3f) at rank 0",
			results[0].Name, results[0].Score)
		for _, r := range results {
			t.Logf("  %s score=%.3f", r.Name, r.Score)
		}
	}
}
