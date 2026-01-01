package toon

import (
	"testing"
)

// SearchResult represents a matched tool from a search (copied from discovery package)
type SearchResult struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Score       float64     `json:"score"`
	InputSchema interface{} `json:"inputSchema,omitempty"`
}

func TestSearchResultEncoding(t *testing.T) {
	// Test data similar to what discovery package produces
	results := []SearchResult{
		{
			Name:        "calculator",
			Description: "A simple calculator that performs basic arithmetic operations",
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
					"operation": map[string]interface{}{
						"type":        "string",
						"description": "The operation to perform: add, subtract, multiply, or divide",
					},
				},
				"required":             []string{"a", "b", "operation"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "greet",
			Description: "A simple tool that greets the user",
			Score:       1.0,
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "The name to greet (optional, defaults to 'World')",
					},
				},
				"additionalProperties": false,
			},
		},
	}

	// Test 1: Direct slice encoding (what was failing)
	t.Run("DirectSlice", func(t *testing.T) {
		encoded, err := Encode(results)
		if err != nil {
			t.Fatalf("Failed to encode: %v", err)
		}
		t.Logf("Direct slice encoding:\n%s", string(encoded))

		// Verify it contains all expected fields
		str := string(encoded)
		if !contains(str, "calculator") {
			t.Error("Missing 'calculator' name")
		}
		if !contains(str, "greet") {
			t.Error("Missing 'greet' name")
		}
		if !contains(str, "name:") {
			t.Error("Missing 'name:' field")
		}
		if !contains(str, "score:") {
			t.Error("Missing 'score:' field")
		}
		if !contains(str, "A simple calculator") {
			t.Error("Missing calculator description")
		}
		if !contains(str, "A simple tool that greets") {
			t.Error("Missing greet description")
		}
	})

	// Test 2: Wrapped in map (current workaround)
	t.Run("WrappedInMap", func(t *testing.T) {
		wrapped := map[string]interface{}{"tools": results}
		encoded, err := Encode(wrapped)
		if err != nil {
			t.Fatalf("Failed to encode: %v", err)
		}
		t.Logf("Wrapped in map encoding:\n%s", string(encoded))

		// Verify it contains all expected fields
		str := string(encoded)
		if !contains(str, "calculator") {
			t.Error("Missing 'calculator' name")
		}
		if !contains(str, "greet") {
			t.Error("Missing 'greet' name")
		}
	})
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsAt(s, substr)))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}