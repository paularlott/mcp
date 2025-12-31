package toon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	// Auto-discover test cases from testdata directory
	files, err := filepath.Glob(filepath.Join("testdata", "*.json"))
	if err != nil {
		t.Fatalf("Failed to find test files: %v", err)
	}
	
	for _, jsonFile := range files {
		testCase := strings.TrimSuffix(filepath.Base(jsonFile), ".json")
		// Skip copy files
		if strings.Contains(testCase, " copy") {
			continue
		}
		
		t.Run(testCase, func(t *testing.T) {
			// Read JSON file
			jsonPath := jsonFile
			jsonData, err := os.ReadFile(jsonPath)
			if err != nil {
				t.Fatalf("Failed to read JSON file %s: %v", jsonPath, err)
			}
			
			// Read TOON file
			toonPath := filepath.Join("testdata", testCase+".toon")
			expectedToon, err := os.ReadFile(toonPath)
			if err != nil {
				t.Fatalf("Failed to read TOON file %s: %v", toonPath, err)
			}
			expectedToonStr := strings.TrimSpace(string(expectedToon))
			
			// Parse JSON
			var jsonValue interface{}
			if err := json.Unmarshal(jsonData, &jsonValue); err != nil {
				t.Fatalf("Failed to parse JSON: %v", err)
			}
			
			// Test JSON -> TOON encoding
			encoded, err := Encode(jsonValue)
			if err != nil {
				t.Fatalf("Failed to encode to TOON: %v", err)
			}
			
			// Test TOON -> JSON decoding
			decoded, err := Decode(expectedToonStr)
			if err != nil {
				t.Fatalf("Failed to decode TOON: %v", err)
			}
			
			// Compare decoded with original JSON (content should match)
			if !reflect.DeepEqual(decoded, jsonValue) {
				t.Errorf("Decoding mismatch for %s:\nExpected: %+v\nGot: %+v", testCase, jsonValue, decoded)
			}
			
			// Test round-trip: JSON -> TOON -> JSON (should be consistent)
			reencoded, err := Encode(decoded)
			if err != nil {
				t.Fatalf("Failed to re-encode: %v", err)
			}
			
			// Round-trip should produce the same result as direct encoding
			if reencoded != encoded {
				t.Errorf("Round-trip encoding mismatch for %s:\nFirst: %s\nSecond: %s", testCase, encoded, reencoded)
			}
		})
	}
}

func TestEncodeBasicTypes(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected string
	}{
		{"null", nil, "null"},
		{"true", true, "true"},
		{"false", false, "false"},
		{"integer", float64(42), "42"},
		{"float", 3.14, "3.14"},
		{"string", "hello", "hello"},
		{"quoted string", "hello world", "hello world"},
		{"empty object", map[string]interface{}{}, ""},
		{"empty array", []interface{}{}, "[0]:"},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Encode(tt.input)
			if err != nil {
				t.Fatalf("Encode failed: %v", err)
			}
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestDecodeBasicTypes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected interface{}
	}{
		{"null", "null", nil},
		{"true", "true", true},
		{"false", "false", false},
		{"integer", "42", float64(42)},
		{"float", "3.14", 3.14},
		{"string", "hello", "hello"},
		{"quoted string", "\"hello world\"", "hello world"},
		{"empty object", "", map[string]interface{}{}},
		{"empty array", "[0]:", []interface{}{}},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Decode(tt.input)
			if err != nil {
				t.Fatalf("Decode failed: %v", err)
			}
			if !reflect.DeepEqual(result, tt.expected) {
				// Special case for empty arrays - both should be empty slices
				if resultSlice, ok := result.([]interface{}); ok {
					if expectedSlice, ok := tt.expected.([]interface{}); ok {
						if len(resultSlice) == 0 && len(expectedSlice) == 0 {
							return // Both are empty, consider them equal
						}
					}
				}
				t.Errorf("Expected %+v, got %+v", tt.expected, result)
			}
		})
	}
}

func TestStringQuoting(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "\"\""},
		{"hello", "hello"},
		{"hello world", "hello world"},
		{"true", "\"true\""},
		{"false", "\"false\""},
		{"null", "\"null\""},
		{"42", "\"42\""},
		{"3.14", "\"3.14\""},
		{"-5", "\"-5\""},
		{"with:colon", "\"with:colon\""},
		{"with\"quote", "\"with\\\"quote\""},
		{"with\\backslash", "\"with\\\\backslash\""},
		{"with\nnewline", "\"with\\nnewline\""},
	}
	
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			encoder := &encoder{indentSize: 2}
			result := encoder.encodeString(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}