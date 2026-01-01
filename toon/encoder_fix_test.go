package toon

import (
	"testing"
)

func TestTOONEncoderFix(t *testing.T) {
	// This test demonstrates that the TOON encoder now correctly handles
	// complex nested structures without losing fields due to key buffer corruption
	
	// Test case 1: SearchResult-like structure (the original failing case)
	searchResults := []map[string]interface{}{
		{
			"name":        "ai_completion",
			"description": "AI completion with complex schema",
			"score":       1.0,
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"prompt": map[string]interface{}{
						"type":        "string",
						"description": "The prompt to complete",
					},
					"model": map[string]interface{}{
						"type":        "string",
						"description": "Model to use",
						"enum":        []interface{}{"gpt-4", "gpt-3.5-turbo"},
					},
					"parameters": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"temperature": map[string]interface{}{
								"type":    "number",
								"minimum": 0,
								"maximum": 2,
							},
							"max_tokens": map[string]interface{}{
								"type":    "integer",
								"minimum": 1,
							},
						},
					},
				},
				"required":             []interface{}{"prompt"},
				"additionalProperties": false,
			},
		},
		{
			"name":        "database_query",
			"description": "Execute database queries",
			"score":       0.9,
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "SQL query to execute",
					},
					"parameters": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"oneOf": []interface{}{
								map[string]interface{}{"type": "string"},
								map[string]interface{}{"type": "number"},
								map[string]interface{}{"type": "boolean"},
							},
						},
					},
				},
				"required": []interface{}{"query"},
			},
		},
	}

	// Test direct array encoding
	encoded, err := Encode(searchResults)
	if err != nil {
		t.Fatalf("Failed to encode search results: %v", err)
	}

	t.Logf("Encoded search results:\n%s", encoded)

	// Verify all critical fields are present
	criticalFields := []string{
		"name:", "description:", "score:", "inputSchema:",
		"ai_completion", "database_query",
		"AI completion with complex schema", "Execute database queries",
	}

	for _, field := range criticalFields {
		if !contains(encoded, field) {
			t.Errorf("Missing critical field: %s", field)
		}
	}

	// Test wrapped in map (tools format)
	wrapped := map[string]interface{}{"tools": searchResults}
	encodedWrapped, err := Encode(wrapped)
	if err != nil {
		t.Fatalf("Failed to encode wrapped search results: %v", err)
	}

	t.Logf("Encoded wrapped search results:\n%s", encodedWrapped)

	// Verify wrapped format also has all fields
	for _, field := range criticalFields {
		if !contains(encodedWrapped, field) {
			t.Errorf("Missing critical field in wrapped format: %s", field)
		}
	}

	// Test case 2: Deeply nested objects with multiple levels
	deeplyNested := map[string]interface{}{
		"level1": map[string]interface{}{
			"name": "first_level",
			"data": map[string]interface{}{
				"level2": map[string]interface{}{
					"name": "second_level",
					"items": []interface{}{
						map[string]interface{}{
							"id":   1,
							"name": "item1",
							"metadata": map[string]interface{}{
								"type":    "test",
								"version": "1.0",
								"config": map[string]interface{}{
									"enabled": true,
									"timeout": 30,
								},
							},
						},
						map[string]interface{}{
							"id":   2,
							"name": "item2",
							"metadata": map[string]interface{}{
								"type":    "prod",
								"version": "2.0",
								"config": map[string]interface{}{
									"enabled": false,
									"timeout": 60,
								},
							},
						},
					},
				},
			},
		},
	}

	encodedDeep, err := Encode(deeplyNested)
	if err != nil {
		t.Fatalf("Failed to encode deeply nested structure: %v", err)
	}

	t.Logf("Encoded deeply nested structure:\n%s", encodedDeep)

	// Verify deep nesting preserves all fields
	deepFields := []string{
		"level1:", "level2:", "name:", "items[2]:", "metadata:", "config:",
		"first_level", "second_level", "item1", "item2",
		"enabled:", "timeout:", "version:",
	}

	for _, field := range deepFields {
		if !contains(encodedDeep, field) {
			t.Errorf("Missing field in deeply nested structure: %s", field)
		}
	}

	t.Log("✅ TOON encoder successfully handles complex nested structures without field loss")
}