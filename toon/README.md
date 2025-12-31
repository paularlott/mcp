# TOON Package

A Go implementation of the TOON (Token-Oriented Object Notation) format specification.

## Overview

TOON is a line-oriented, indentation-based text format that encodes the JSON data model with explicit structure and minimal quoting. It's particularly efficient for arrays of uniform objects and provides a compact, deterministic representation of structured data.

## Features

- **Full TOON Specification Compliance**: Implements TOON spec v3.0
- **Bidirectional Conversion**: JSON ↔ TOON with perfect round-trip fidelity
- **Tabular Arrays**: Efficient encoding of uniform object arrays
- **Minimal Quoting**: Strings are quoted only when necessary
- **Type Safety**: Preserves JSON data types (string, number, boolean, null, object, array)

## Installation

```bash
go get github.com/paularlott/mcp/toon
```

## Usage

### Basic Example

```go
package main

import (
    "fmt"
    "log"
    
    "github.com/paularlott/mcp/toon"
)

func main() {
    // JSON data
    data := map[string]interface{}{
        "name": "Alice",
        "age":  30,
        "active": true,
    }
    
    // Encode to TOON
    toonStr, err := toon.Encode(data)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println("TOON:")
    fmt.Println(toonStr)
    
    // Decode back to Go value
    decoded, err := toon.Decode(toonStr)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Decoded: %+v\n", decoded)
}
```

Output:
```
TOON:
active: true
age: 30
name: Alice

Decoded: map[active:true age:30 name:Alice]
```

### Array Examples

#### Primitive Arrays
```go
data := map[string]interface{}{
    "numbers": []interface{}{1, 2, 3, 4, 5},
    "names":   []interface{}{"Alice", "Bob", "Charlie"},
}

// Encodes to:
// names[3]: Alice,Bob,Charlie
// numbers[5]: 1,2,3,4,5
```

#### Tabular Arrays (Arrays of Objects)
```go
data := map[string]interface{}{
    "users": []interface{}{
        map[string]interface{}{"id": 1, "name": "Alice", "role": "admin"},
        map[string]interface{}{"id": 2, "name": "Bob", "role": "user"},
    },
}

// Encodes to:
// users[2]{id,name,role}:
//   1,Alice,admin
//   2,Bob,user
```

## API Reference

### Functions

#### `Encode(v interface{}) (string, error)`

Converts a Go value to TOON format using default options.

#### `EncodeWithOptions(v interface{}, opts *EncodeOptions) (string, error)`

Converts a Go value to TOON format with custom options.

**EncodeOptions:**
```go
type EncodeOptions struct {
    Indent    int    // Number of spaces per indentation level (default: 2)
    Delimiter string // Delimiter for arrays and tabular data (default: ",")
}
```

**Example:**
```go
// Custom 4-space indentation and pipe delimiter
opts := &toon.EncodeOptions{
    Indent:    4,
    Delimiter: "|",
}
result, err := toon.EncodeWithOptions(data, opts)
// Output: users[2]{id|name|role}:
//             1|Alice|admin
//             2|Bob|user
```

#### `Decode(data string) (interface{}, error)`

Parses TOON format using default options (strict mode).

#### `DecodeWithOptions(data string, opts *DecodeOptions) (interface{}, error)`

Parses TOON format with custom options.

**DecodeOptions:**
```go
type DecodeOptions struct {
    Strict bool // Enable strict validation (default: true)
}
```

**Example:**
```go
// Non-strict mode - allows array length mismatches
opts := &toon.DecodeOptions{Strict: false}
result, err := toon.DecodeWithOptions(data, opts)
```

**Parameters:**
- `v`: Any Go value (will be normalized to JSON-compatible types)
- `opts`: Configuration options (nil uses defaults)

**Returns:**
- `string`: TOON-formatted string
- `error`: Error if encoding fails

**Supported Input Types:**
- Primitives: `nil`, `bool`, `int`, `float64`, `string`
- Collections: `map[string]interface{}`, `[]interface{}`
- Structs (converted via JSON marshaling)
- Pointers and interfaces (dereferenced)

#### `Decode(data string) (interface{}, error)`

Parses TOON format and returns the decoded value.

**Decode Parameters:**
- `data`: TOON-formatted string
- `opts`: Configuration options (nil uses defaults)

**Returns:**
- `interface{}`: Decoded Go value
- `error`: Error if parsing fails

**Output Types:**
- `nil` for null values
- `bool` for true/false
- `float64` for numbers
- `string` for text
- `map[string]interface{}` for objects
- `[]interface{}` for arrays

## TOON Format Overview

### Objects
```
key1: value1
key2: value2
nested:
  subkey: subvalue
```

### Arrays

**Primitive Arrays:**
```
numbers[3]: 1,2,3
```

**Tabular Arrays (uniform objects):**
```
users[2]{id,name,role}:
  1,Alice,admin
  2,Bob,user
```

**Mixed Arrays:**
```
items[3]:
- primitive_value
- key: object_value
- [2]: nested,array
```

### String Quoting

Strings are quoted only when necessary:
- Empty strings: `""`
- Strings with whitespace: `"hello world"`
- Reserved words: `"true"`, `"false"`, `"null"`
- Numeric-looking strings: `"42"`, `"3.14"`
- Strings with special characters: `"with:colon"`, `"with\"quote"`

## Testing

Run the test suite:

```bash
go test ./...
```

The package includes comprehensive tests with test data files in `testdata/`:
- Round-trip conversion tests (JSON → TOON → JSON)
- Basic type encoding/decoding tests
- String quoting behavior tests

## Specification Compliance

This implementation follows the [TOON Specification v3.0](https://github.com/toon-format/spec/blob/main/SPEC.md) and includes:

- ✅ Complete JSON data model support
- ✅ Canonical number formatting
- ✅ Minimal string quoting rules
- ✅ Tabular array detection and encoding
- ✅ Proper indentation and whitespace handling
- ✅ Strict mode validation (default)
- ✅ UTF-8 encoding with LF line endings

## License

MIT License - see the main project license for details.