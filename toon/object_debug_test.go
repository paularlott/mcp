package toon

import (
	"testing"
)

func TestObjectEncodingDebug(t *testing.T) {
	// Test the exact normalized map that's causing issues
	normalizedMap := map[string]interface{}{
		"name":        "calculator",
		"description": "A simple calculator", 
		"score":       1.0,
		"inputSchema": map[string]interface{}{
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
			"required": []interface{}{"a", "b"},
		},
	}

	t.Logf("Input map keys: %v", getKeys(normalizedMap))
	
	// Test encoding this map directly
	encoder := newEncoder(2, ",")
	encoded, err := encoder.encodeObject(normalizedMap, 0)
	if err != nil {
		t.Fatalf("encodeObject failed: %v", err)
	}
	
	t.Logf("Encoded object:\n%s", encoded)
	
	// Check which keys are present in output
	expectedKeys := []string{"name", "description", "score", "inputSchema"}
	for _, key := range expectedKeys {
		if contains(encoded, key+":") {
			t.Logf("✓ Found key: %s", key)
		} else {
			t.Errorf("✗ Missing key: %s", key)
		}
	}
	
	// Check for spurious keys
	if contains(encoded, "type: null") {
		t.Error("Found spurious 'type: null'")
	}
}

func getKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}