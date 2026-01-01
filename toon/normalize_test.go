package toon

import (
	"encoding/json"
	"testing"
)

func TestNormalizationDebug(t *testing.T) {
	result := SearchResult{
		Name:        "calculator",
		Description: "A simple calculator",
		Score:       1.0,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"a": map[string]interface{}{
					"type":        "number",
					"description": "The first number",
				},
			},
		},
	}

	// Step 1: JSON marshal
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}
	t.Logf("JSON: %s", string(jsonBytes))

	// Step 2: JSON unmarshal (what normalizeValue does for structs)
	var normalized interface{}
	err = json.Unmarshal(jsonBytes, &normalized)
	if err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}
	t.Logf("Normalized: %+v", normalized)

	// Step 3: Call normalizeValue directly
	normalizedDirect, err := normalizeValue(result)
	if err != nil {
		t.Fatalf("normalizeValue failed: %v", err)
	}
	t.Logf("Direct normalization: %+v", normalizedDirect)

	// Step 4: Encode the normalized value
	encoded, err := Encode(normalizedDirect)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	t.Logf("TOON: %s", encoded)
}