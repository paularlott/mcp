package mcp

import "testing"

// TestCalculateSingleWordScoreNameTokenBeatsKeyword verifies that a tool whose
// name contains the query word as a token (e.g. "send" in "tools_send_email")
// scores higher than a tool that only lists the word as a keyword tag. The
// name is the strongest intent signal.
func TestCalculateSingleWordScoreNameTokenBeatsKeyword(t *testing.T) {
	const query = "report"

	// Name contains the query word as a _-separated token.
	nameMatch := calculateSingleWordScore(
		query,
		"generate_report_pdf",
		"render the report into a printable pdf document.",
		[]string{"report", "pdf", "render", "document"},
	)

	// Word appears only as a keyword tag and in the description, not in the name.
	keywordMatch := calculateSingleWordScore(
		query,
		"data_search",
		"full-text search across records and indexed content including report archives and documents.",
		[]string{"search", "find", "report", "document"},
	)

	if nameMatch <= keywordMatch {
		t.Fatalf("name-token match (%.3f) should beat keyword-tag match (%.3f) for query %q",
			nameMatch, keywordMatch, query)
	}
}

// TestCalculateSingleWordScoreLadder pins the score ladder so future regressions
// in the relative ordering of match tiers are caught.
func TestCalculateSingleWordScoreLadder(t *testing.T) {
	tests := []struct {
		label    string
		word     string
		name     string
		desc     string
		keywords []string
		minScore float64
		maxScore float64
	}{
		{
			label:    "name prefix (multi-token name)",
			word:     "memory",
			name:     "memory_recall",
			desc:     "",
			keywords: nil,
			minScore: 0.95,
			maxScore: 0.95,
		},
		{
			label:    "name token (not first segment)",
			word:     "send",
			name:     "tools_send_email",
			desc:     "",
			keywords: nil,
			minScore: 0.92,
			maxScore: 0.92,
		},
		{
			label:    "keyword exact",
			word:     "report",
			name:     "data_search",
			desc:     "",
			keywords: []string{"report"},
			minScore: 0.85,
			maxScore: 0.85,
		},
		{
			label:    "keyword phrase contains query word",
			word:     "work",
			name:     "data_search",
			desc:     "",
			keywords: []string{"work request"},
			minScore: 0.85,
			maxScore: 0.85,
		},
		{
			label:    "description word",
			word:     "report",
			name:     "unrelated",
			desc:     "talks about report briefly",
			keywords: nil,
			minScore: 0.6,
			maxScore: 0.6,
		},
		{
			label:    "description substring only",
			word:     "rep",
			name:     "unrelated",
			desc:     "renders a report",
			keywords: nil,
			minScore: 0.5,
			maxScore: 0.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			got := calculateSingleWordScore(tt.word, tt.name, tt.desc, tt.keywords)
			if got < tt.minScore || got > tt.maxScore {
				t.Errorf("score for %q in %q = %.3f, want between %.3f and %.3f",
					tt.word, tt.name, got, tt.minScore, tt.maxScore)
			}
		})
	}
}

// TestCalculateScoreMultiWordFullMatchNotPenalised verifies that a clean
// full-query match isn't artificially penalised below an equivalent
// single-word match.
func TestCalculateScoreMultiWordFullMatchNotPenalised(t *testing.T) {
	// Single-word query against a name where it appears as a token.
	single := calculateScore("send", "send_email", "", []string{"email", "mail"})

	// Two-word query where both words are token matches.
	double := calculateScore("send email", "send_email", "", []string{"email", "mail"})

	// The two-word full match should be at least as strong as the
	// single-word match; previously it was multiplied by 0.9 and ended up
	// lower, which discouraged richer queries.
	if double < single*0.95 {
		t.Errorf("multi-word full match (%.3f) should not be materially lower than single-word match (%.3f)",
			double, single)
	}
}

// TestCalculateScoreMultiWordPartialMatch verifies that a partial match
// returns a sensible non-zero score that reflects how much of the query was
// understood, without double-penalising the misses.
func TestCalculateScoreMultiWordPartialMatch(t *testing.T) {
	// Query: "send invoice" against a tool that only matches "send".
	got := calculateScore("send invoice", "send_email", "", []string{"email"})

	// "send" is a name-token match (0.92), "invoice" is a miss.
	// Expected: 0.92 / 2 = 0.46. Pre-fix: avg * matchRatio = 0.46 * 0.5 = 0.23.
	if got < 0.4 || got > 0.55 {
		t.Errorf("partial match score = %.3f, want roughly 0.46 (totalScore / queryWordCount)", got)
	}
}

// TestNameTokensSplitsOnSeparators sanity-checks the token splitter.
func TestNameTokensSplitsOnSeparators(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"send_email", []string{"send", "email"}},
		{"some-tool-name", []string{"some", "tool", "name"}},
		{"plain", []string{"plain"}},
		{"mixed_case-name", []string{"mixed", "case", "name"}},
		{"", nil},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := nameTokens(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("nameTokens(%q) = %v, want %v", tt.input, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("nameTokens(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}
