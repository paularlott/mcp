// Package toon implements the TOON (Token-Oriented Object Notation) format.
// TOON is a line-oriented, indentation-based text format that encodes the JSON data model
// with explicit structure and minimal quoting.
package toon

// EncodeOptions configures TOON encoding behavior.
type EncodeOptions struct {
	Indent    int    // Number of spaces per indentation level (default: 2)
	Delimiter string // Delimiter for arrays and tabular data (default: ",")
}

// DecodeOptions configures TOON decoding behavior.
type DecodeOptions struct {
	Strict bool // Enable strict validation (default: true)
}

// Encode converts a Go value to TOON format.
func Encode(v interface{}) (string, error) {
	return EncodeWithOptions(v, nil)
}

// EncodeWithOptions converts a Go value to TOON format with custom options.
func EncodeWithOptions(v interface{}, opts *EncodeOptions) (string, error) {
	if opts == nil {
		opts = &EncodeOptions{Indent: 2, Delimiter: ","}
	}
	if opts.Indent <= 0 {
		opts.Indent = 2
	}
	if opts.Delimiter == "" {
		opts.Delimiter = ","
	}

	encoder := newEncoder(opts.Indent, opts.Delimiter)
	normalized, err := normalizeValue(v)
	if err != nil {
		return "", err
	}
	return encoder.encode(normalized, 0)
}

// Decode parses TOON format and returns the decoded value.
func Decode(data string) (interface{}, error) {
	return DecodeWithOptions(data, nil)
}

// DecodeWithOptions parses TOON format with custom options.
func DecodeWithOptions(data string, opts *DecodeOptions) (interface{}, error) {
	if opts == nil {
		opts = &DecodeOptions{Strict: true}
	}

	decoder := &decoder{strict: opts.Strict}
	return decoder.decode(data)
}
