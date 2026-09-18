package toon

import (
	"reflect"
	"strings"
	"testing"
)

// Tests targeting specific uncovered branches in decode.go, identified via
// `go tool cover -func` / line-level coverage profiling. Each test documents
// which branch it exercises so the intent stays clear even as the
// implementation evolves.

func TestDecodeWhitespaceOnlyLines(t *testing.T) {
	// decode(): after filtering, only whitespace-only lines remain (not
	// truly empty), so nonEmptyLines ends up empty -> empty object.
	decoded, err := Decode("   \n\t  \n")
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if !reflect.DeepEqual(decoded, map[string]any{}) {
		t.Errorf("decoded = %#v, want empty map", decoded)
	}
}

func TestDecodeArrayHeaderMissingKey(t *testing.T) {
	// A bare "[N]:" header appearing as an object member line (not the
	// document root) has no key -> decodeArrayFromLines must reject it.
	_, err := Decode("foo: bar\n[2]: 1,2\n")
	if err == nil {
		t.Fatal("expected error for array header with missing key")
	}
	if !strings.Contains(err.Error(), "missing key") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDecodeObjectDirectBranches(t *testing.T) {
	d := newDecoder(true, 0)

	// depth < startDepth causes an immediate break (empty result).
	result, err := d.decodeObject([]string{"key: value"}, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(result, map[string]any{}) {
		t.Errorf("result = %#v, want empty map", result)
	}

	// A blank line embedded directly in the lines slice must be skipped
	// (decodeObject is normally fed pre-filtered lines by decode(), but the
	// function itself tolerates blanks when called directly).
	result, err = d.decodeObject([]string{"a: 1", "", "b: 2"}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := map[string]any{"a": 1.0, "b": 2.0}
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("result = %#v, want %#v", result, expected)
	}
}

func TestDecodeObjectSkipsOverIndentedJunkLine(t *testing.T) {
	// A stray line indented deeper than the enclosing object's own depth is
	// skipped rather than treated as a field of that object.
	decoded, err := Decode("a: 1\n    stray\nb: 2\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := map[string]any{"a": 1.0, "b": 2.0}
	if !reflect.DeepEqual(decoded, expected) {
		t.Errorf("decoded = %#v, want %#v", decoded, expected)
	}
}

func TestDecodeObjectNestedObjectErrorPropagates(t *testing.T) {
	_, err := Decode("outer:\n  bad line without colon\n")
	if err == nil {
		t.Fatal("expected error for invalid line inside nested object")
	}
	if !strings.Contains(err.Error(), "invalid line") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDecodeObjectBlockListErrorPropagates(t *testing.T) {
	// A block list nested under a plain key that itself contains a
	// malformed inline array must propagate the decodeListArray error.
	_, err := Decode("tags:\n  - arr[2]: 1,2,3\n")
	if err == nil {
		t.Fatal("expected error for malformed nested array inside block list item")
	}
	if !strings.Contains(err.Error(), "array length mismatch") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDecodeArrayDirectBranches(t *testing.T) {
	d := newDecoder(true, 0)

	arr, err := d.decodeArray([]string{}, 0, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(arr, []any{}) {
		t.Errorf("arr = %#v, want empty slice", arr)
	}

	_, err = d.decodeArray([]string{"not a header"}, 0, "")
	if err == nil {
		t.Fatal("expected error for invalid array header")
	}
	if !strings.Contains(err.Error(), "invalid array header") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDecodeArrayFromLinesInvalidHeader(t *testing.T) {
	d := newDecoder(true, 0)
	_, _, _, err := d.decodeArrayFromLines([]string{"not a header"}, 0)
	if err == nil {
		t.Fatal("expected error for invalid array header")
	}
}

func TestParsePrimitiveArrayEmptyInline(t *testing.T) {
	// parsePrimitiveArray's own empty-inline guard: decodeArray never calls
	// it with an empty inline string (it checks first), so exercise it
	// directly.
	d := newDecoder(true, 0)
	result, err := d.parsePrimitiveArray("", ",", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(result, []any{}) {
		t.Errorf("result = %#v, want empty slice", result)
	}
}

func TestDecodeTabularArrayMalformedIndentation(t *testing.T) {
	// A row that dips to a shallower indent than the tabular block is
	// skipped (not treated as ending the block), and the resulting short
	// count then trips the strict length check.
	_, err := Decode("[3]{a,b}:\n  1,2\n 1,2\n  3,4\n")
	if err == nil {
		t.Fatal("expected array length mismatch error")
	}
	if !strings.Contains(err.Error(), "array length mismatch") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDecodeTabularArrayOverIndentedRowBreaks(t *testing.T) {
	// A row indented deeper than the tabular block ends the block early.
	_, err := Decode("[2]{a,b}:\n  1,2\n    3,4\n")
	if err == nil {
		t.Fatal("expected array length mismatch error (block ended early)")
	}
}

func TestDecodeTabularArrayNonTabularRowBreaks(t *testing.T) {
	// A row that looks like a key-value line (colon before delimiter, or no
	// delimiter at all) is not a tabular row and ends the block.
	_, err := Decode("[2]{a,b}:\n  1,2\n  foo: bar\n")
	if err == nil {
		t.Fatal("expected array length mismatch error (non-tabular row)")
	}
}

func TestDecodeTabularArrayDashRowBreaks(t *testing.T) {
	// A row starting with "- " is a list item, never a tabular row.
	_, err := Decode("[2]{a,b}:\n  1,2\n  - 1,2\n")
	if err == nil {
		t.Fatal("expected array length mismatch error (dash row)")
	}
}

func TestDecodeTabularArrayRowValueCountMismatch(t *testing.T) {
	// A single row with a different value count than the field list, under
	// strict mode, errors immediately.
	_, err := Decode("[2]{a,b}:\n  1,2,3\n  4,5\n")
	if err == nil {
		t.Fatal("expected row value count mismatch error")
	}
	if !strings.Contains(err.Error(), "row value count mismatch") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDecodeTabularArrayValueContainingDelimiterColon(t *testing.T) {
	// A tabular value can legitimately contain a colon (e.g. a timestamp)
	// as long as the delimiter appears before it on the line.
	decoded, err := Decode("[1]{a,b}:\n  1,10:30\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	arr, ok := decoded.([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("unexpected decoded shape: %#v", decoded)
	}
	row, ok := arr[0].(map[string]any)
	if !ok || row["b"] != "10:30" {
		t.Errorf("row = %#v, want b == \"10:30\"", row)
	}
}

func TestDecodeListArrayDirectBranches(t *testing.T) {
	d := newDecoder(true, 0)

	// Blank line embedded directly in the lines slice is skipped.
	result, err := d.decodeListArray([]string{"- a", "", "- b"}, 0, -1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(result, []any{"a", "b"}) {
		t.Errorf("result = %#v, want [a b]", result)
	}

	// depth < startDepth breaks immediately.
	result, err = d.decodeListArray([]string{"- a"}, 1, -1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(result, []any(nil)) {
		t.Errorf("result = %#v, want nil slice", result)
	}
}

func TestDecodeListArrayOverIndentedLineSkipped(t *testing.T) {
	// A root-level list whose very first raw line carries leading
	// whitespace is deeper than the fixed startDepth(0) used for the root
	// list, so it is skipped as "too deep" rather than parsed as an item.
	decoded, err := Decode("  - a\n- b\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(decoded, []any{"b"}) {
		t.Errorf("decoded = %#v, want [b]", decoded)
	}
}

func TestDecodeListArrayTrailingJunkStopsParsing(t *testing.T) {
	// A line that is neither blank nor prefixed with "- " at the list's own
	// depth ends the list rather than erroring (expectedLength is -1 here
	// so no length check applies).
	decoded, err := Decode("- a\nnot a list item\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(decoded, []any{"a"}) {
		t.Errorf("decoded = %#v, want [a]", decoded)
	}
}

func TestDecodeListArrayEmptyDashItem(t *testing.T) {
	decoded, err := Decode("- foo\n-\n- bar\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []any{"foo", map[string]any{}, "bar"}
	if !reflect.DeepEqual(decoded, expected) {
		t.Errorf("decoded = %#v, want %#v", decoded, expected)
	}
}

func TestDecodeListArrayNestedArraySpanningLines(t *testing.T) {
	// A list item whose content is itself an anonymous (keyless) array
	// header spans multiple following lines.
	decoded, err := Decode("- [2]:\n  - x\n  - y\n- foo\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []any{[]any{"x", "y"}, "foo"}
	if !reflect.DeepEqual(decoded, expected) {
		t.Errorf("decoded = %#v, want %#v", decoded, expected)
	}
}

func TestDecodeListArrayKeyedArrayFirstFieldNotAnonymous(t *testing.T) {
	// Regression test for a real bug: a list item whose first field is a
	// *keyed* array header (e.g. "arr[2]: x,y") must decode as an object
	// with that field, not as an anonymous array that discards the key and
	// any sibling fields. The hyphen line and its sibling field sit at the
	// same indentation depth, matching what the encoder actually produces
	// (see TestRoundTripListItemFirstFieldArray in encode_coverage_test.go
	// for the end-to-end version of this regression).
	decoded, err := Decode("- arr[2]: x,y\nother: z\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []any{map[string]any{"arr": []any{"x", "y"}, "other": "z"}}
	if !reflect.DeepEqual(decoded, expected) {
		t.Errorf("decoded = %#v, want %#v", decoded, expected)
	}
}

func TestDecodeTabularArraySingleColumn(t *testing.T) {
	// Regression test for a real bug: a tabular array with exactly one
	// field has no delimiter in its rows (nothing to separate), which used
	// to make isTabularRow misclassify every row as a key-value line.
	decoded, err := Decode("[2]{n}:\n  1\n  2\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []any{
		map[string]any{"n": 1.0},
		map[string]any{"n": 2.0},
	}
	if !reflect.DeepEqual(decoded, expected) {
		t.Errorf("decoded = %#v, want %#v", decoded, expected)
	}
}

func TestDecodeListArrayAnonymousArrayItemErrorPropagates(t *testing.T) {
	// A list item that is itself an anonymous (keyless) array header with
	// a malformed inline body must propagate the decodeArray error.
	_, err := Decode("- [2]: 1,2,3\n")
	if err == nil {
		t.Fatal("expected array length mismatch error")
	}
	if !strings.Contains(err.Error(), "array length mismatch") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDecodeListArrayLengthMismatch(t *testing.T) {
	_, err := Decode("arr[3]:\n  - a\n  - b\n")
	if err == nil {
		t.Fatal("expected array length mismatch error")
	}
	if !strings.Contains(err.Error(), "array length mismatch") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDecodeListItemObjectInvalidFirstLine(t *testing.T) {
	d := newDecoder(true, 0)
	_, err := d.decodeListItemObject([]string{"not a list item"}, 0)
	if err == nil {
		t.Fatal("expected error for list item not starting with \"- \"")
	}
	if !strings.Contains(err.Error(), "invalid list item") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDecodeListItemObjectNestedArrayErrorPropagates(t *testing.T) {
	// The first field decodes fine; a later field in the same list item is
	// a malformed inline array, which must propagate as an error rather
	// than being silently dropped.
	_, err := Decode("- name: foo\n  arr[2]: 1,2,3\n")
	if err == nil {
		t.Fatal("expected array length mismatch error")
	}
	if !strings.Contains(err.Error(), "array length mismatch") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDecodeListItemObjectFirstFieldNestedObject(t *testing.T) {
	// Regression test for a real bug: when a list item's *first* field is
	// itself a nested object, the lines making up that nested object used
	// to be reprocessed a second time by the loop that looks for sibling
	// fields, duplicating the nested keys onto the list item itself.
	input := "- person:\n  name: Alice\n  age: 30\n- other\n"
	decoded, err := Decode(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []any{
		map[string]any{"person": map[string]any{"name": "Alice", "age": 30.0}},
		"other",
	}
	if !reflect.DeepEqual(decoded, expected) {
		t.Errorf("decoded = %#v, want %#v", decoded, expected)
	}
}

func TestDecodeListItemObjectFirstFieldNestedObjectErrorPropagates(t *testing.T) {
	_, err := Decode("- outer:\n  bad line without colon\n")
	if err == nil {
		t.Fatal("expected error for invalid line inside the first field's nested object")
	}
}

func TestDecodeListItemObjectBlankLineInSecondLoopSkipped(t *testing.T) {
	d := newDecoder(true, 0)
	result, err := d.decodeListItemObject([]string{"- a: 1", "", "b: 2"}, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := map[string]any{"a": 1.0, "b": 2.0}
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("result = %#v, want %#v", result, expected)
	}
}

func TestDecodeListItemObjectLaterFieldEmptyNestedObject(t *testing.T) {
	decoded, err := Decode("- a: 1\n  b:\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []any{map[string]any{"a": 1.0, "b": map[string]any{}}}
	if !reflect.DeepEqual(decoded, expected) {
		t.Errorf("decoded = %#v, want %#v", decoded, expected)
	}
}

func TestDecodeListItemObjectLaterFieldNestedObjectErrorPropagates(t *testing.T) {
	_, err := Decode("- a: 1\n  b:\n    bad line without colon\n")
	if err == nil {
		t.Fatal("expected error for invalid line inside a later field's nested object")
	}
}

func TestDecodeListItemObjectFirstFieldNestedEmptyObject(t *testing.T) {
	decoded, err := Decode("- meta:\n- other\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []any{
		map[string]any{"meta": map[string]any{}},
		"other",
	}
	if !reflect.DeepEqual(decoded, expected) {
		t.Errorf("decoded = %#v, want %#v", decoded, expected)
	}
}

func TestIsTabularRowDirect(t *testing.T) {
	d := newDecoder(true, 0)

	if d.isTabularRow("- 1,2", ",") {
		t.Error(`"- 1,2" should not be a tabular row`)
	}
	if d.isTabularRow("foo: bar", ",") {
		t.Error(`"foo: bar" (colon, no delimiter) should not be a tabular row`)
	}
	if !d.isTabularRow("1,foo:bar", ",") {
		t.Error(`"1,foo:bar" (delimiter before colon) should be a tabular row`)
	}
	if d.isTabularRow("1:30,2", ",") {
		t.Error(`"1:30,2" (colon before delimiter) should not be a tabular row`)
	}
	if !d.isTabularRow("1,2", ",") {
		t.Error(`"1,2" should be a tabular row`)
	}
}

func TestDecodeCustomIndentSize(t *testing.T) {
	input := "outer:\n    inner: value\n"
	decoded, err := DecodeWithOptions(input, &DecodeOptions{Strict: true, IndentSize: 4})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := map[string]any{"outer": map[string]any{"inner": "value"}}
	if !reflect.DeepEqual(decoded, expected) {
		t.Errorf("decoded = %#v, want %#v", decoded, expected)
	}
}

func TestParseValueLeadingZero(t *testing.T) {
	decoded, err := Decode("a: 007\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := map[string]any{"a": "007"}
	if !reflect.DeepEqual(decoded, expected) {
		t.Errorf("decoded = %#v, want %#v", decoded, expected)
	}
}

func TestUnescapeStringDirect(t *testing.T) {
	d := newDecoder(true, 0)

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no escapes", "no escapes here", "no escapes here"},
		{"backslash", `a\\b`, `a\b`},
		{"quote", `a\"b`, `a"b`},
		{"newline", `a\nb`, "a\nb"},
		{"carriage return", `a\rb`, "a\rb"},
		{"tab", `a\tb`, "a\tb"},
		{"unknown escape passed through", `a\xb`, `a\xb`},
		{"trailing lone backslash", `trailing\`, `trailing\`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := d.unescapeString(c.in)
			if got != c.want {
				t.Errorf("unescapeString(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestUnescapeStringViaDecode(t *testing.T) {
	// End-to-end sanity check that quoted-string escapes round-trip through
	// the public Decode() entry point too, not just the direct unit test.
	decoded, err := Decode(`a: "line1\nline2\ttabbed\r\"quoted\""` + "\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := decoded.(map[string]any)
	if !ok {
		t.Fatalf("decoded is not a map: %#v", decoded)
	}
	want := "line1\nline2\ttabbed\r\"quoted\""
	if m["a"] != want {
		t.Errorf("a = %q, want %q", m["a"], want)
	}
}
