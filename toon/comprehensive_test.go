package toon

import (
	"testing"
)

func TestComplexNestedStructures(t *testing.T) {
	// Test case 1: Array of objects with mixed field types
	testData1 := []map[string]interface{}{
		{
			"name":        "tool1",
			"description": "First tool",
			"score":       1.0,
			"metadata": map[string]interface{}{
				"version": "1.0",
				"tags":    []string{"tag1", "tag2"},
			},
		},
		{
			"name":        "tool2", 
			"description": "Second tool",
			"score":       0.8,
			"metadata": map[string]interface{}{
				"version": "2.0",
				"tags":    []string{"tag3"},
			},
		},
	}

	encoded1, err := Encode(testData1)
	if err != nil {
		t.Fatalf("Failed to encode test data 1: %v", err)
	}
	t.Logf("Test 1 - Array of objects with nested data:\\n%s", encoded1)

	// Verify all fields are present
	if !contains(encoded1, "name:") {
		t.Error("Test 1: Missing 'name:' field")
	}
	if !contains(encoded1, "tool1") {
		t.Error("Test 1: Missing 'tool1' value")
	}
	if !contains(encoded1, "tool2") {
		t.Error("Test 1: Missing 'tool2' value")
	}

	// Test case 2: Deeply nested objects
	testData2 := map[string]interface{}{
		"level1": map[string]interface{}{
			"level2": map[string]interface{}{
				"level3": map[string]interface{}{
					"name":  "deep",
					"value": 42,
				},
			},
		},
	}

	encoded2, err := Encode(testData2)
	if err != nil {
		t.Fatalf("Failed to encode test data 2: %v", err)
	}
	t.Logf("Test 2 - Deeply nested objects:\\n%s", encoded2)

	// Test case 3: Mixed arrays and objects
	testData3 := map[string]interface{}{
		"users": []map[string]interface{}{
			{
				"id":   1,
				"name": "Alice",
				"roles": []string{"admin", "user"},
				"profile": map[string]interface{}{
					"email": "alice@example.com",
					"age":   30,
				},
			},
			{
				"id":   2,
				"name": "Bob",
				"roles": []string{"user"},
				"profile": map[string]interface{}{
					"email": "bob@example.com",
					"age":   25,
				},
			},
		},
	}

	encoded3, err := Encode(testData3)
	if err != nil {
		t.Fatalf("Failed to encode test data 3: %v", err)
	}
	t.Logf("Test 3 - Mixed arrays and objects:\\n%s", encoded3)

	// Verify critical fields
	if !contains(encoded3, "Alice") {
		t.Error("Test 3: Missing 'Alice' value")
	}
	if !contains(encoded3, "Bob") {
		t.Error("Test 3: Missing 'Bob' value")
	}
	if !contains(encoded3, "name:") {
		t.Error("Test 3: Missing 'name:' field")
	}
}