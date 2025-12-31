package toon

import (
	"strings"
	"testing"
)

func TestEncodeOptions(t *testing.T) {
	data := map[string]interface{}{
		"items": []interface{}{"a", "b", "c"},
		"users": []interface{}{
			map[string]interface{}{"id": 1, "name": "Alice"},
			map[string]interface{}{"id": 2, "name": "Bob"},
		},
	}

	t.Run("custom_indent", func(t *testing.T) {
		result, err := EncodeWithOptions(data, &EncodeOptions{Indent: 4})
		if err != nil {
			t.Fatal(err)
		}
		
		lines := strings.Split(result, "\n")
		// Check that nested content uses 4-space indentation
		found := false
		for _, line := range lines {
			if strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "        ") {
				found = true
				break
			}
		}
		if !found {
			t.Error("Expected 4-space indentation not found")
		}
	})

	t.Run("custom_delimiter", func(t *testing.T) {
		result, err := EncodeWithOptions(data, &EncodeOptions{Delimiter: "|"})
		if err != nil {
			t.Fatal(err)
		}
		
		if !strings.Contains(result, "a|b|c") {
			t.Error("Expected pipe delimiter in primitive array")
		}
		if !strings.Contains(result, "{id|name}") {
			t.Error("Expected pipe delimiter in tabular header")
		}
	})
}

func TestDecodeOptions(t *testing.T) {
	// Test with array length mismatch
	invalidData := `items[3]: a,b` // Claims 3 items but only has 2

	t.Run("strict_mode", func(t *testing.T) {
		_, err := DecodeWithOptions(invalidData, &DecodeOptions{Strict: true})
		if err == nil {
			t.Error("Expected error in strict mode")
		}
	})

	t.Run("non_strict_mode", func(t *testing.T) {
		result, err := DecodeWithOptions(invalidData, &DecodeOptions{Strict: false})
		if err != nil {
			t.Fatal(err)
		}
		
		// Should decode successfully in non-strict mode
		obj := result.(map[string]interface{})
		items := obj["items"].([]interface{})
		if len(items) != 2 {
			t.Errorf("Expected 2 items, got %d", len(items))
		}
	})
}