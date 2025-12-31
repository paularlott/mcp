// Package toon implements the TOON (Token-Oriented Object Notation) format.
// TOON is a line-oriented, indentation-based text format that encodes the JSON data model
// with explicit structure and minimal quoting.
package toon

// Encode converts a Go value to TOON format.
func Encode(v interface{}) (string, error) {
	encoder := &encoder{indentSize: 2}
	normalized, err := normalizeValue(v)
	if err != nil {
		return "", err
	}
	return encoder.encode(normalized, 0)
}

// Decode parses TOON format and returns the decoded value.
func Decode(data string) (interface{}, error) {
	decoder := &decoder{strict: true}
	return decoder.decode(data)
}