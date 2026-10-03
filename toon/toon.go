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
	Strict     bool // Enable strict validation (default: true)
	IndentSize int  // Expected indentation size (0 = auto-detect, default: 0)
	// Integers decodes integer literals (no fraction or exponent) as int64
	// instead of float64, as a Python or JavaScript BigInt-aware reader
	// would. Out-of-range integers still decode as float64. Default: false,
	// matching encoding/json.
	Integers bool
}

// Encode converts a Go value to TOON format.
func Encode(v any) (string, error) {
	return EncodeWithOptions(v, nil)
}

// EncodeWithOptions converts a Go value to TOON format with custom options.
func EncodeWithOptions(v any, opts *EncodeOptions) (string, error) {
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
func Decode(data string) (any, error) {
	return DecodeWithOptions(data, nil)
}

// DecodeWithOptions parses TOON format with custom options.
func DecodeWithOptions(data string, opts *DecodeOptions) (any, error) {
	if opts == nil {
		opts = &DecodeOptions{Strict: true, IndentSize: 0}
	}

	decoder := newDecoder(opts.Strict, opts.IndentSize)
	decoder.integers = opts.Integers
	return decoder.decode(data)
}
