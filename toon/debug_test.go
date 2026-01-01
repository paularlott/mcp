package toon

import (
	"encoding/json"
	"testing"
)

func TestNameFieldDebug(t *testing.T) {
	// Simple struct with name field
	result := SearchResult{
		Name:        "test_tool",
		Description: "A test tool",
		Score:       1.0,
	}

	// Test JSON marshaling/unmarshaling (what normalizeValue does)
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}
	t.Logf("JSON: %s", string(jsonBytes))

	var normalized interface{}
	err = json.Unmarshal(jsonBytes, &normalized)
	if err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}
	t.Logf("Normalized: %+v", normalized)

	// Test TOON encoding
	encoded, err := Encode(result)
	if err != nil {
		t.Fatalf("TOON encode failed: %v", err)
	}
	t.Logf("TOON: %s", encoded)

	// Test array of structs
	results := []SearchResult{result, {
		Name:        "another_tool",
		Description: "Another test tool",
		Score:       0.8,
	}}

	// Test TOON encoding of array
	encodedArray, err := Encode(results)
	if err != nil {
		t.Fatalf("TOON encode array failed: %v", err)
	}
	t.Logf("TOON Array: %s", encodedArray)

	// Test wrapped array
	wrapped := map[string]interface{}{"tools": results}
	encodedWrapped, err := Encode(wrapped)
	if err != nil {
		t.Fatalf("TOON encode wrapped failed: %v", err)
	}
	t.Logf("TOON Wrapped: %s", encodedWrapped)
}