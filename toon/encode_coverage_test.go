package toon

import (
	"reflect"
	"strings"
	"testing"
)

// Tests targeting specific uncovered branches in encode.go and toon.go,
// identified via `go tool cover -func` / line-level coverage profiling.

func TestKeyToStringDirect(t *testing.T) {
	type customKey rune

	cases := []struct {
		name string
		in   any
		want string
	}{
		{"string", "abc", "abc"},
		{"int", int(42), "42"},
		{"int64", int64(-7), "-7"},
		{"uint", uint(9), "9"},
		{"uint64", uint64(18446744073709551615), "18446744073709551615"},
		{"float64", 3.5, "3.5"},
		{"bool", true, "true"},
		{"default (unmatched type)", customKey('x'), "120"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := keyToString(c.in)
			if got != c.want {
				t.Errorf("keyToString(%#v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestNormalizeValueMapWithNonStringKeys(t *testing.T) {
	// End-to-end sanity check that non-string map keys round-trip through
	// the public Encode() entry point, exercising keyToString via
	// normalizeValue's Map case.
	encoded, err := Encode(map[int]string{1: "one", 2: "two"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Numeric-looking keys aren't valid bare identifiers, so they come out
	// quoted; the point of this test is that keyToString ran at all (int
	// keys stringified) rather than the quoting style.
	if !strings.Contains(encoded, `"1": one`) || !strings.Contains(encoded, `"2": two`) {
		t.Errorf("encoded = %q, want keys 1 and 2 present", encoded)
	}
}

func TestNormalizeValuePointers(t *testing.T) {
	var nilPtr *int
	got, err := normalizeValue(nilPtr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("nil pointer should normalize to nil, got %#v", got)
	}

	x := 5
	got, err = normalizeValue(&x)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 5.0 {
		t.Errorf("normalized *int = %#v, want 5.0", got)
	}
}

func TestNormalizeValueUnsignedInt(t *testing.T) {
	encoded, err := Encode(uint(42))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if encoded != "42" {
		t.Errorf("encoded = %q, want %q", encoded, "42")
	}
}

// unmarshalableStruct has a field type json.Marshal cannot handle, used to
// exercise normalizeValue's struct-marshal error path and its propagation
// through the map/slice cases.
type unmarshalableStruct struct {
	Ch chan int
}

func TestNormalizeValueStructMarshalErrorPropagatesThroughMap(t *testing.T) {
	_, err := normalizeValue(map[string]any{"bad": unmarshalableStruct{Ch: make(chan int)}})
	if err == nil {
		t.Fatal("expected error for unmarshalable struct nested in map")
	}
}

func TestNormalizeValueStructMarshalErrorPropagatesThroughSlice(t *testing.T) {
	_, err := normalizeValue([]any{unmarshalableStruct{Ch: make(chan int)}})
	if err == nil {
		t.Fatal("expected error for unmarshalable struct nested in slice")
	}
}

func TestEncodeWithOptionsPropagatesNormalizeError(t *testing.T) {
	_, err := Encode(unmarshalableStruct{Ch: make(chan int)})
	if err == nil {
		t.Fatal("expected error from EncodeWithOptions when normalizeValue fails")
	}
}

func TestEncodeDirectUnsupportedType(t *testing.T) {
	// encode() only handles the canonical normalized types; anything else
	// (reachable only by calling the encoder directly, since
	// EncodeWithOptions always normalizes first) hits the default branch.
	enc := newEncoder(2, ",")
	_, err := enc.encode(42, 0) // plain int, not float64
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
	if !strings.Contains(err.Error(), "unsupported type") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetIndentGrowsBeyondInitialCache(t *testing.T) {
	// newEncoder pre-allocates indent strings for depths 0-7; nest deep
	// enough to force getIndent's cache-growth loop.
	var v any = "leaf"
	for i := 0; i < 12; i++ {
		v = map[string]any{"k": v}
	}
	encoded, err := Encode(v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(encoded, "leaf") {
		t.Errorf("encoded output missing leaf value: %q", encoded)
	}
	// Sanity: depth 11 should be indented by 22 spaces (2 per level).
	if !strings.Contains(encoded, strings.Repeat(" ", 22)+"k: leaf") {
		t.Errorf("encoded output missing deeply indented leaf: %q", encoded)
	}
}

func TestNeedsQuotingLeadingTrailingSpace(t *testing.T) {
	encoded, err := Encode(map[string]any{"k": " leading"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(encoded, `k: " leading"`) {
		t.Errorf("leading-space string should be quoted: %q", encoded)
	}

	encoded, err = Encode(map[string]any{"k": "trailing "})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(encoded, `k: "trailing "`) {
		t.Errorf("trailing-space string should be quoted: %q", encoded)
	}
}

func TestNeedsQuotingLeadingZeroNumberLikeString(t *testing.T) {
	encoded, err := Encode(map[string]any{"k": "007"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(encoded, `k: "007"`) {
		t.Errorf(`"007" should be quoted to avoid ambiguity with a number: %q`, encoded)
	}
}

func TestNeedsQuotingScientificNotationLikeString(t *testing.T) {
	encoded, err := Encode(map[string]any{"k": "1e+10"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(encoded, `k: "1e+10"`) {
		t.Errorf(`"1e+10" should be quoted to avoid ambiguity with a number: %q`, encoded)
	}
}

func TestNeedsQuotingVersionStringNotQuoted(t *testing.T) {
	// A string with two dots (like a version number) fails the "looks like
	// a number" sniff (only one dot allowed) and should be left bare.
	encoded, err := Encode(map[string]any{"version": "1.2.3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(encoded, "version: 1.2.3") {
		t.Errorf("version string should not be quoted: %q", encoded)
	}
}

func TestNeedsQuotingRepeatedExponentNotQuoted(t *testing.T) {
	encoded, err := Encode(map[string]any{"k": "1e2e3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(encoded, "k: 1e2e3") {
		t.Errorf("string with repeated 'e' should not be quoted: %q", encoded)
	}
}

func TestLooksLikeNumberDirect(t *testing.T) {
	enc := newEncoder(2, ",")

	// Only reachable directly: needsQuoting intercepts the empty string and
	// leading '-' cases before ever calling looksLikeNumber.
	if enc.looksLikeNumber("") {
		t.Error(`looksLikeNumber("") should be false`)
	}
	if !enc.looksLikeNumber("-5") {
		t.Error(`looksLikeNumber("-5") should be true`)
	}
	if enc.looksLikeNumber("-abc") {
		t.Error(`looksLikeNumber("-abc") should be false`)
	}
}

func TestQuoteStringEscapesCarriageReturnAndTab(t *testing.T) {
	encoded, err := Encode(map[string]any{"k": "a\rb\tc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(encoded, `k: "a\rb\tc"`) {
		t.Errorf("expected \\r and \\t to be escaped, got %q", encoded)
	}
}

func TestIsValidIdentifierEmptyKey(t *testing.T) {
	encoded, err := Encode(map[string]any{"": "value"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(encoded, `"": value`) {
		t.Errorf("empty key should be quoted: %q", encoded)
	}
}

func TestEncodeObjectDirectErrorPropagation(t *testing.T) {
	enc := newEncoder(2, ",")

	if _, err := enc.encodeObject(map[string]any{"bad": complex128(1)}, 0); err == nil {
		t.Error("expected error for unsupported scalar value")
	}
	if _, err := enc.encodeObject(map[string]any{"arr": []any{complex128(1)}}, 0); err == nil {
		t.Error("expected error for unsupported value inside array")
	}
	if _, err := enc.encodeObject(map[string]any{"outer": map[string]any{"bad": complex128(1)}}, 0); err == nil {
		t.Error("expected error for unsupported value inside nested object")
	}
}

func TestIsTabularDirectEmptyArray(t *testing.T) {
	enc := newEncoder(2, ",")
	if enc.isTabular([]any{}) {
		t.Error("isTabular([]) should be false")
	}
}

func TestIsTabularLaterObjectHasNonPrimitiveValue(t *testing.T) {
	enc := newEncoder(2, ",")
	arr := []any{
		map[string]any{"a": 1.0, "b": 2.0},
		map[string]any{"a": 3.0, "b": map[string]any{"x": 1.0}},
	}
	if enc.isTabular(arr) {
		t.Error("isTabular should be false when a later object has a non-primitive value")
	}
}

func TestIsTabularLaterObjectHasUnknownKey(t *testing.T) {
	enc := newEncoder(2, ",")
	arr := []any{
		map[string]any{"a": 1.0, "b": 2.0},
		map[string]any{"a": 3.0, "c": 4.0},
	}
	if enc.isTabular(arr) {
		t.Error("isTabular should be false when a later object has a key not in the first object")
	}
}

func TestEncodeListArrayEmptyMapItem(t *testing.T) {
	encoded, err := Encode([]any{map[string]any{}, "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(encoded, "\n  -\n") && !strings.HasSuffix(encoded, "\n  -") {
		t.Errorf("expected a bare '-' line for the empty map item, got %q", encoded)
	}

	// Must also round-trip back through Decode.
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("round-trip decode failed: %v", err)
	}
	arr, ok := decoded.([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("unexpected decoded shape: %#v", decoded)
	}
	if m, ok := arr[0].(map[string]any); !ok || len(m) != 0 {
		t.Errorf("expected first item to be an empty map, got %#v", arr[0])
	}
}

func TestEncodeListArraySecondFieldIsArray(t *testing.T) {
	// Within a list item object, a field after the first ("tags", sorted
	// after "name") that holds a nested array exercises the i>0 branch of
	// the []any case.
	data := []any{
		map[string]any{"name": "x", "tags": []any{"a", "b"}},
	}
	encoded, err := Encode(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("round-trip decode failed: %v", err)
	}
	arr, ok := decoded.([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("unexpected decoded shape: %#v", decoded)
	}
	item, ok := arr[0].(map[string]any)
	if !ok {
		t.Fatalf("item is not a map: %#v", arr[0])
	}
	if item["name"] != "x" {
		t.Errorf("name = %#v, want x", item["name"])
	}
	tags, ok := item["tags"].([]any)
	if !ok || len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Errorf("tags = %#v, want [a b]", item["tags"])
	}
}

func TestEncodeTabularDirectErrorPropagation(t *testing.T) {
	enc := newEncoder(2, ",")
	_, err := enc.encodeTabular([]any{map[string]any{"a": complex128(1)}}, 0, "")
	if err == nil {
		t.Fatal("expected error for unsupported value inside a tabular cell")
	}
}

func TestEncodeListArrayFirstFieldIsArray(t *testing.T) {
	// "a" sorts before "b", so the array-valued field lands at i==0,
	// exercising the dash-prefix branch of the []any case.
	data := []any{
		map[string]any{"a": []any{"x", "y"}, "b": "z"},
	}
	encoded, err := Encode(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("round-trip decode failed: %v", err)
	}
	arr, ok := decoded.([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("unexpected decoded shape: %#v", decoded)
	}
	item, ok := arr[0].(map[string]any)
	if !ok {
		t.Fatalf("item is not a map: %#v", arr[0])
	}
	a, ok := item["a"].([]any)
	if !ok || len(a) != 2 || a[0] != "x" || a[1] != "y" {
		t.Errorf("a = %#v, want [x y]", item["a"])
	}
	if item["b"] != "z" {
		t.Errorf("b = %#v, want z", item["b"])
	}
}

func TestRoundTripListItemFirstFieldArray(t *testing.T) {
	// Regression test for a real round-trip bug: encoding a list of
	// objects whose first (alphabetically sorted) field is an array used
	// to produce TOON that decoded back into a bare array, discarding the
	// key and any sibling fields entirely.
	data := []any{
		map[string]any{"a": []any{"x", "y"}, "b": "z"},
	}
	encoded, err := Encode(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	decoded, err := Decode(encoded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []any{map[string]any{"a": []any{"x", "y"}, "b": "z"}}
	if !reflect.DeepEqual(decoded, expected) {
		t.Errorf("round trip = %#v, want %#v (encoded: %q)", decoded, expected, encoded)
	}
}

func TestEncodePrimitiveArrayDirectErrorPropagation(t *testing.T) {
	enc := newEncoder(2, ",")
	_, err := enc.encodePrimitiveArray([]any{complex128(1)}, "arr")
	if err == nil {
		t.Fatal("expected error for unsupported primitive-array element type")
	}
}

func TestEncodeListArrayDirectNestedErrorPropagation(t *testing.T) {
	enc := newEncoder(2, ",")

	// Nested object value (second field, "z" sorted after "a") fails to encode.
	_, err := enc.encodeListArray([]any{
		map[string]any{"a": 1.0, "z": map[string]any{"bad": complex128(1)}},
	}, 0, "")
	if err == nil {
		t.Error("expected error for unsupported value inside nested object field")
	}

	// Nested array value (second field) fails to encode.
	_, err = enc.encodeListArray([]any{
		map[string]any{"a": 1.0, "z": []any{complex128(1)}},
	}, 0, "")
	if err == nil {
		t.Error("expected error for unsupported value inside nested array field")
	}

	// Default scalar branch (second field) fails to encode.
	_, err = enc.encodeListArray([]any{
		map[string]any{"a": 1.0, "z": complex128(1)},
	}, 0, "")
	if err == nil {
		t.Error("expected error for unsupported scalar field value")
	}

	// Top-level item (not a map at all) fails to encode.
	_, err = enc.encodeListArray([]any{complex128(1)}, 0, "")
	if err == nil {
		t.Error("expected error for unsupported top-level item value")
	}
}
