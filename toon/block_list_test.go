package toon

import (
	"reflect"
	"testing"
)

// Block lists ("key:" followed by "- item" lines) are accepted on decode,
// alongside the canonical inline "key[N]: a,b,c" form.

func TestDecodeBlockListUnderPlainKey(t *testing.T) {
	input := "name: Alice\nage: 30\nactive: true\ntags:\n  - python\n  - golang\n"
	decoded, err := Decode(input)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	expected := map[string]any{
		"name":   "Alice",
		"age":    30.0,
		"active": true,
		"tags":   []any{"python", "golang"},
	}
	if !reflect.DeepEqual(decoded, expected) {
		t.Errorf("decoded = %#v, want %#v", decoded, expected)
	}
}

func TestDecodeBlockListScalarTypes(t *testing.T) {
	input := "values:\n  - 1\n  - 2.5\n  - true\n  - false\n  - null\n  - \"quoted string\"\n  - bare string\n"
	decoded, err := Decode(input)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	expected := map[string]any{
		"values": []any{1.0, 2.5, true, false, nil, "quoted string", "bare string"},
	}
	if !reflect.DeepEqual(decoded, expected) {
		t.Errorf("decoded = %#v, want %#v", decoded, expected)
	}
}

func TestDecodeBlockListOfObjects(t *testing.T) {
	input := "users:\n  - name: alice\n    role: admin\n  - name: bob\n    role: user\n"
	decoded, err := Decode(input)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	expected := map[string]any{
		"users": []any{
			map[string]any{"name": "alice", "role": "admin"},
			map[string]any{"name": "bob", "role": "user"},
		},
	}
	if !reflect.DeepEqual(decoded, expected) {
		t.Errorf("decoded = %#v, want %#v", decoded, expected)
	}
}

func TestDecodeRootBlockList(t *testing.T) {
	decoded, err := Decode("- alpha\n- beta\n- gamma\n")
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	expected := []any{"alpha", "beta", "gamma"}
	if !reflect.DeepEqual(decoded, expected) {
		t.Errorf("decoded = %#v, want %#v", decoded, expected)
	}
}

func TestDecodeBlockListNestedInObject(t *testing.T) {
	input := "outer:\n  inner:\n    - one\n    - two\n  other: yes\n"
	decoded, err := Decode(input)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	expected := map[string]any{
		"outer": map[string]any{
			"inner": []any{"one", "two"},
			"other": "yes",
		},
	}
	if !reflect.DeepEqual(decoded, expected) {
		t.Errorf("decoded = %#v, want %#v", decoded, expected)
	}
}

func TestDecodeBlockListLenientMode(t *testing.T) {
	input := "tags:\n  - a\n  - b\n"
	decoded, err := DecodeWithOptions(input, &DecodeOptions{Strict: false})
	if err != nil {
		t.Fatalf("DecodeWithOptions error: %v", err)
	}
	expected := map[string]any{"tags": []any{"a", "b"}}
	if !reflect.DeepEqual(decoded, expected) {
		t.Errorf("decoded = %#v, want %#v", decoded, expected)
	}
}

// Unhappy paths: strict mode (the default) rejects malformed input instead of
// silently dropping lines; lenient mode keeps skipping.

func TestDecodeStrictRejectsInvalidLine(t *testing.T) {
	input := "name: Alice\nthis line is not toon\nage: 30\n"
	_, err := Decode(input)
	if err == nil {
		t.Fatal("expected error for invalid line in strict mode")
	}
}

func TestDecodeLenientSkipsInvalidLine(t *testing.T) {
	input := "name: Alice\nthis line is not toon\nage: 30\n"
	decoded, err := DecodeWithOptions(input, &DecodeOptions{Strict: false})
	if err != nil {
		t.Fatalf("DecodeWithOptions error: %v", err)
	}
	expected := map[string]any{"name": "Alice", "age": 30.0}
	if !reflect.DeepEqual(decoded, expected) {
		t.Errorf("decoded = %#v, want %#v", decoded, expected)
	}
}

func TestDecodeStrictRejectsHeaderLengthMismatch(t *testing.T) {
	input := "tags[2]: a,b,c\n"
	_, err := Decode(input)
	if err == nil {
		t.Fatal("expected error for array length mismatch in strict mode")
	}
}

// A bare key with no nested lines still decodes to an empty object (unchanged
// from before block-list support).
func TestDecodeEmptyKeyStillEmptyObject(t *testing.T) {
	decoded, err := Decode("empty:\nname: Alice\n")
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	expected := map[string]any{"empty": map[string]any{}, "name": "Alice"}
	if !reflect.DeepEqual(decoded, expected) {
		t.Errorf("decoded = %#v, want %#v", decoded, expected)
	}
}
