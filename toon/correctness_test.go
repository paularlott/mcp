package toon

import (
	"strings"
	"testing"
)

func TestCorrectnessIssues(t *testing.T) {
	t.Run("delimiter_specific_quoting", func(t *testing.T) {
		data := map[string]interface{}{
			"pipe_value": "has|pipe",
			"comma_value": "has,comma",
		}
		
		// With comma delimiter (default), pipe should not be quoted
		result1, err := EncodeWithOptions(data, &EncodeOptions{Delimiter: ","})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(result1, "pipe_value: has|pipe") {
			t.Error("Pipe should not be quoted with comma delimiter")
		}
		if !strings.Contains(result1, `comma_value: "has,comma"`) {
			t.Error("Comma should be quoted with comma delimiter")
		}
		
		// With pipe delimiter, comma should not be quoted
		result2, err := EncodeWithOptions(data, &EncodeOptions{Delimiter: "|"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(result2, "comma_value: has,comma") {
			t.Error("Comma should not be quoted with pipe delimiter")
		}
		if !strings.Contains(result2, `pipe_value: "has|pipe"`) {
			t.Error("Pipe should be quoted with pipe delimiter")
		}
	})

	t.Run("canonical_number_formatting", func(t *testing.T) {
		data := map[string]interface{}{
			"large_number": 1e15,
			"small_number": 1e-10,
		}
		
		result, err := Encode(data)
		if err != nil {
			t.Fatal(err)
		}
		
		// Should not contain scientific notation (e followed by digits)
		if strings.Contains(result, "e+") || strings.Contains(result, "e-") || strings.Contains(result, "E+") || strings.Contains(result, "E-") {
			t.Errorf("Numbers should not use scientific notation: %s", result)
		}
	})

	t.Run("identifier_validation", func(t *testing.T) {
		data := map[string]interface{}{
			"valid_key":     "value1",
			"key.with.dots": "value2",
		}
		
		result, err := Encode(data)
		if err != nil {
			t.Fatal(err)
		}
		
		// Keys with dots should be quoted
		if !strings.Contains(result, `"key.with.dots"`) {
			t.Error("Keys with dots should be quoted")
		}
		if !strings.Contains(result, "valid_key: value1") {
			t.Error("Valid identifiers should not be quoted")
		}
	})
}