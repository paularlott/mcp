package toon

import (
	"testing"
)

func TestComplexObjectEncoding(t *testing.T) {
	// Test with complex nested objects like SearchResult
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
			"required":             []string{"a", "b"},
			"additionalProperties": false,
		},
	}

	// Test single object
	encoded, err := Encode(result)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}
	t.Logf("Single object:\n%s", encoded)

	// Check for all expected fields
	str := encoded
	if !contains(str, "name:") {
		t.Error("Missing 'name:' field in single object")
	}
	if !contains(str, "description:") {
		t.Error("Missing 'description:' field in single object")
	}
	if !contains(str, "score:") {
		t.Error("Missing 'score:' field in single object")
	}
	if !contains(str, "inputSchema:") {
		t.Error("Missing 'inputSchema:' field in single object")
	}

	// Test array of objects
	results := []SearchResult{result}
	encodedArray, err := Encode(results)
	if err != nil {
		t.Fatalf("Failed to encode array: %v", err)
	}
	t.Logf("Array:\n%s", encodedArray)

	// Check for all expected fields in array
	str = encodedArray
	if !contains(str, "name:") {
		t.Error("Missing 'name:' field in array")
	}
	if !contains(str, "description:") {
		t.Error("Missing 'description:' field in array")
	}
	if !contains(str, "score:") {
		t.Error("Missing 'score:' field in array")
	}
	if !contains(str, "inputSchema:") {
		t.Error("Missing 'inputSchema:' field in array")
	}
}