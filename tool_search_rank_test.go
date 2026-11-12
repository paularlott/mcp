package mcp

import (
	"context"
	"testing"
)

func TestStemWordMakesVariantsAgree(t *testing.T) {
	groups := [][]string{
		{"opportunity", "opportunities"},
		{"visualise", "visualize", "visualising", "visualizing", "visualised"},
		{"summarise", "summarize", "summarised", "summarising", "summarizes"},
		{"create", "created", "creates", "creating"},
		{"invoice", "invoices", "invoiced"},
		{"ticket", "tickets"},
		{"run", "running"},
	}
	for _, group := range groups {
		want := stemWord(group[0])
		for _, word := range group[1:] {
			if got := stemWord(word); got != want {
				t.Errorf("stemWord(%q) = %q, want %q (as for %q)", word, got, want, group[0])
			}
		}
	}
	// Words that only look inflected are left alone.
	for _, word := range []string{"status", "class", "analysis", "bus", "list", "get", "id", "chart_maker", "v2"} {
		if got := stemWord(word); got != word {
			t.Errorf("stemWord(%q) = %q, want it unchanged", word, got)
		}
	}
	if stemWord("string") != "string" || stemWord("thing") != "thing" {
		t.Errorf("short stems are not stripped: string -> %q, thing -> %q", stemWord("string"), stemWord("thing"))
	}
}

func TestSearchTermsDropStopwords(t *testing.T) {
	got := searchTerms("who created the most opportunities by value, created")
	want := []string{"created", "most", "opportunities", "value"}
	if len(got) != len(want) {
		t.Fatalf("searchTerms = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("searchTerms = %v, want %v", got, want)
		}
	}
	// A query made only of stopwords keeps its words rather than matching nothing.
	if got := searchTerms("what is it"); len(got) != 3 {
		t.Errorf("searchTerms(stopwords only) = %v, want the words kept", got)
	}
	// A tool name stays one word.
	if got := searchTerms("send_email now"); got[0] != "send_email" {
		t.Errorf("searchTerms = %v, want send_email kept whole", got)
	}
}

func rankTools(query string, maxResults int, tools ...MCPTool) []SearchResult {
	return newInternalRegistry().SearchWithAdditionalTools(context.Background(), query, maxResults, nil, tools)
}

// A word many tools match counts for less than a word few tools match: the tool
// named for the rare word wins over one that matches the common words.
func TestSearchWeightsRareWordsAboveCommonOnes(t *testing.T) {
	tools := []MCPTool{
		{Name: "count_orders", Description: "Count orders created in a period.", Keywords: []string{"count", "created", "orders"}},
		{Name: "list_orders", Description: "List orders created recently.", Keywords: []string{"created"}},
		{Name: "list_items", Description: "List items created recently.", Keywords: []string{"created"}},
		{Name: "list_notes", Description: "List notes created recently.", Keywords: []string{"created"}},
		{Name: "count_invoices", Description: "Count invoices.", Keywords: []string{"invoice"}},
	}
	results := rankTools("invoices created count", 5, tools...)
	if len(results) == 0 || results[0].Name != "count_invoices" {
		t.Fatalf("top result = %v, want count_invoices (the only tool for the rare word)", results)
	}
}

// Detailed descriptions that mention several query words do not outrank a
// tool that matches the subject in its name or keywords.
func TestSearchStrongMatchBeatsDescriptionMentions(t *testing.T) {
	tools := []MCPTool{
		{Name: "send_message", Description: "Send a message.", Keywords: []string{"message", "send"}},
		{Name: "list_threads", Description: "List threads. A thread can hold a message you send, a reply you send, and every message sent later."},
		{Name: "filler_one", Description: "Unrelated."},
		{Name: "filler_two", Description: "Unrelated."},
	}
	results := rankTools("send message", 5, tools...)
	if results[0].Name != "send_message" {
		t.Fatalf("top result = %s, want send_message", results[0].Name)
	}
}

// Each strongly matched word keeps its best tool in the results, so a query
// with two intents returns a tool for each even when one intent has many
// higher-scoring tools.
func TestSearchKeepsTheBestToolForEachIntent(t *testing.T) {
	tools := []MCPTool{
		{Name: "draw_chart", Description: "Draw a chart from numbers.", Keywords: []string{"chart", "graph", "plot"}},
	}
	for _, name := range []string{"list_orders", "summarise_orders", "count_orders", "export_orders", "archive_orders"} {
		tools = append(tools, MCPTool{Name: name, Description: "Orders, with their value and status.", Keywords: []string{"orders", "order", "value"}})
	}
	results := rankTools("plot orders value status", 3, tools...)
	found := false
	for _, r := range results {
		if r.Name == "draw_chart" {
			found = true
		}
	}
	if !found {
		t.Fatalf("results = %v, want draw_chart kept for the word 'plot'", results)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
}

func TestSearchExactNameScoresOne(t *testing.T) {
	tools := []MCPTool{
		{Name: "send_email", Description: "Send an email."},
		{Name: "send_email_later", Description: "Schedule an email."},
	}
	for _, query := range []string{"send_email", "send email", "SEND_EMAIL"} {
		results := rankTools(query, 5, tools...)
		if results[0].Name != "send_email" || results[0].Score != 1.0 {
			t.Errorf("query %q: top = %s %.2f, want send_email 1.00", query, results[0].Name, results[0].Score)
		}
	}
}

func TestSearchEmptyQueryListsEverything(t *testing.T) {
	tools := []MCPTool{{Name: "b_tool"}, {Name: "a_tool"}, {Name: "c_tool"}}
	results := rankTools("  ", 2, tools...)
	if len(results) != 2 || results[0].Name != "a_tool" || results[0].Score != 1.0 {
		t.Fatalf("results = %v, want the first two by name, each scored 1", results)
	}
}

func TestSearchNoMatchReturnsNothing(t *testing.T) {
	tools := []MCPTool{{Name: "send_email", Description: "Send an email."}}
	if results := rankTools("zzzz qqqq", 5, tools...); len(results) != 0 {
		t.Fatalf("results = %v, want none", results)
	}
}

// A single-word query scores each tool by that word's match alone, as before.
func TestSearchSingleWordScoreIsTheMatch(t *testing.T) {
	tools := []MCPTool{
		{Name: "memory_recall", Description: "Recall memories."},
		{Name: "notes", Description: "Notes you can recall later."},
	}
	results := rankTools("memory", 5, tools...)
	if results[0].Name != "memory_recall" || results[0].Score != 0.95 {
		t.Fatalf("results = %v, want memory_recall at 0.95 (a name prefix)", results)
	}
}
