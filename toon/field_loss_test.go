package toon

import (
	"encoding/json"
	"testing"
)

func TestFieldLossInvestigation(t *testing.T) {
	// Create the exact problematic structure
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
				"b": map[string]interface{}{
					"type":        "number", 
					"description": "The second number",
				},
			},
			"required": []string{"a", "b"},
		},
	}

	t.Logf("Original struct: %+v", result)

	// Step 1: Test JSON marshaling
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}
	t.Logf("JSON: %s", string(jsonBytes))

	// Step 2: Test JSON unmarshaling
	var unmarshaled interface{}
	err = json.Unmarshal(jsonBytes, &unmarshaled)
	if err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}
	t.Logf("Unmarshaled: %+v", unmarshaled)

	// Step 3: Test normalizeValue
	normalized, err := normalizeValue(result)
	if err != nil {
		t.Fatalf("normalizeValue failed: %v", err)
	}
	t.Logf("Normalized: %+v", normalized)

	// Step 4: Check if normalized has all expected keys
	normalizedMap, ok := normalized.(map[string]interface{})
	if !ok {
		t.Fatalf("Normalized is not a map: %T", normalized)
	}

	expectedKeys := []string{"name", "description", "score", "inputSchema"}
	for _, key := range expectedKeys {
		if _, exists := normalizedMap[key]; !exists {
			t.Errorf("Missing key '%s' in normalized map", key)
		} else {
			t.Logf("Found key '%s': %+v", key, normalizedMap[key])
		}
	}

	// Step 5: Test encoding the normalized value
	encoded, err := Encode(normalized)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	t.Logf("TOON encoded:\n%s", encoded)

	// Step 6: Check if encoded contains all fields
	for _, key := range expectedKeys {
		if !contains(encoded, key+":") {
			t.Errorf("Missing '%s:' in encoded output", key)
		}
	}
}