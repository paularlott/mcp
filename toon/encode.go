package toon

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type encoder struct {
	indentSize   int
	delimiter    string
	indentCache  []string
	keyBuffer    []string
	escapeBuffer strings.Builder
}

var (
	numericRegex     = regexp.MustCompile(`^-?\d+(?:\.\d+)?(?:e[+-]?\d+)?$`)
	leadingZeroRegex = regexp.MustCompile(`^0\d+$`)
	identifierRegex  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)
)

func normalizeValue(v interface{}) (interface{}, error) {
	if v == nil {
		return nil, nil
	}

	val := reflect.ValueOf(v)
	switch val.Kind() {
	case reflect.Ptr:
		if val.IsNil() {
			return nil, nil
		}
		return normalizeValue(val.Elem().Interface())
	case reflect.Interface:
		return normalizeValue(val.Elem().Interface())
	case reflect.Map:
		result := make(map[string]interface{})
		for _, key := range val.MapKeys() {
			keyStr := fmt.Sprintf("%v", key.Interface())
			normVal, err := normalizeValue(val.MapIndex(key).Interface())
			if err != nil {
				return nil, err
			}
			result[keyStr] = normVal
		}
		return result, nil
	case reflect.Slice, reflect.Array:
		result := make([]interface{}, val.Len())
		for i := 0; i < val.Len(); i++ {
			normVal, err := normalizeValue(val.Index(i).Interface())
			if err != nil {
				return nil, err
			}
			result[i] = normVal
		}
		return result, nil
	case reflect.Struct:
		jsonBytes, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		var result interface{}
		err = json.Unmarshal(jsonBytes, &result)
		return result, err
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(val.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(val.Uint()), nil
	case reflect.Float32, reflect.Float64:
		return val.Float(), nil
	default:
		return v, nil
	}
}

func (e *encoder) encode(v interface{}, depth int) (string, error) {
	switch val := v.(type) {
	case nil:
		return "null", nil
	case bool:
		return strconv.FormatBool(val), nil
	case float64:
		return e.formatNumber(val), nil
	case string:
		return e.encodeString(val), nil
	case map[string]interface{}:
		return e.encodeObject(val, depth)
	case []interface{}:
		return e.encodeArray(val, depth, "")
	default:
		return "", fmt.Errorf("unsupported type: %T", v)
	}
}

func (e *encoder) formatNumber(f float64) string {
	if f != f { // NaN
		return "null"
	}
	if math.IsInf(f, 0) {
		return "null"
	}
	if f == 0 {
		return "0"
	}

	s := strconv.FormatFloat(f, 'f', -1, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	return s
}

func (e *encoder) encodeString(s string) string {
	if e.needsQuoting(s) {
		return e.quoteString(s)
	}
	return s
}

func (e *encoder) getIndent(depth int) string {
	needed := depth + 1
	for len(e.indentCache) < needed {
		level := len(e.indentCache)
		e.indentCache = append(e.indentCache, strings.Repeat(" ", level*e.indentSize))
	}
	return e.indentCache[depth]
}

func (e *encoder) needsQuoting(s string) bool {
	if len(s) == 0 {
		return true
	}

	// Fast path for common cases
	switch s {
	case "true", "false", "null":
		return true
	}

	// Check first/last character for spaces
	if s[0] == ' ' || s[len(s)-1] == ' ' {
		return true
	}

	// Check for special characters
	for _, c := range s {
		switch c {
		case ':', '"', '\\', '\n', '\r', '\t', '[', ']', '{', '}', ',', '|':
			return true
		}
	}

	// Check for leading minus
	if s[0] == '-' {
		return true
	}

	// Use regex for numeric patterns (less frequent)
	if numericRegex.MatchString(strings.ToLower(s)) || leadingZeroRegex.MatchString(s) {
		return true
	}
	return false
}

func (e *encoder) quoteString(s string) string {
	e.escapeBuffer.Reset()
	e.escapeBuffer.WriteByte('"')

	for _, c := range s {
		switch c {
		case '\\':
			e.escapeBuffer.WriteString("\\\\")
		case '"':
			e.escapeBuffer.WriteString("\\\"")
		case '\n':
			e.escapeBuffer.WriteString("\\n")
		case '\r':
			e.escapeBuffer.WriteString("\\r")
		case '\t':
			e.escapeBuffer.WriteString("\\t")
		default:
			e.escapeBuffer.WriteRune(c)
		}
	}

	e.escapeBuffer.WriteByte('"')
	return e.escapeBuffer.String()
}

func (e *encoder) encodeKey(key string) string {
	if identifierRegex.MatchString(key) {
		return key
	}
	return e.quoteString(key)
}

func (e *encoder) encodeObject(obj map[string]interface{}, depth int) (string, error) {
	if len(obj) == 0 {
		return "", nil
	}

	// Reuse key buffer
	e.keyBuffer = e.keyBuffer[:0]
	for k := range obj {
		e.keyBuffer = append(e.keyBuffer, k)
	}
	sort.Strings(e.keyBuffer)

	var b strings.Builder
	indent := e.getIndent(depth)
	first := true

	for _, key := range e.keyBuffer {
		if !first {
			b.WriteByte('\n')
		}
		first = false

		value := obj[key]
		encodedKey := e.encodeKey(key)

		switch v := value.(type) {
		case map[string]interface{}:
			b.WriteString(indent)
			b.WriteString(encodedKey)
			b.WriteByte(':')
			if len(v) > 0 {
				nested, err := e.encodeObject(v, depth+1)
				if err != nil {
					return "", err
				}
				if nested != "" {
					b.WriteByte('\n')
					b.WriteString(nested)
				}
			}
		case []interface{}:
			arrayStr, err := e.encodeArray(v, depth, key)
			if err != nil {
				return "", err
			}
			b.WriteString(indent)
			b.WriteString(arrayStr)
		default:
			encoded, err := e.encode(value, depth)
			if err != nil {
				return "", err
			}
			b.WriteString(indent)
			b.WriteString(encodedKey)
			b.WriteString(": ")
			b.WriteString(encoded)
		}
	}

	return b.String(), nil
}

func (e *encoder) encodeArray(arr []interface{}, depth int, key string) (string, error) {
	length := len(arr)

	if length == 0 {
		if key == "" {
			return "[0]:", nil
		}
		return key + "[0]:", nil
	}

	if e.isTabular(arr) {
		return e.encodeTabular(arr, depth, key)
	}

	if e.isPrimitiveArray(arr) {
		return e.encodePrimitiveArray(arr, key)
	}

	return e.encodeListArray(arr, depth, key)
}

func (e *encoder) isTabular(arr []interface{}) bool {
	if len(arr) == 0 {
		return false
	}

	firstObj, ok := arr[0].(map[string]interface{})
	if !ok {
		return false
	}

	// Check if all values in first object are primitive
	for _, v := range firstObj {
		if !e.isPrimitive(v) {
			return false
		}
	}

	// Reuse key buffer for first object keys
	e.keyBuffer = e.keyBuffer[:0]
	for k := range firstObj {
		e.keyBuffer = append(e.keyBuffer, k)
	}
	sort.Strings(e.keyBuffer)
	firstKeys := e.keyBuffer

	// Check remaining objects
	for i := 1; i < len(arr); i++ {
		obj, ok := arr[i].(map[string]interface{})
		if !ok || len(obj) != len(firstKeys) {
			return false
		}

		// Check all values are primitive and keys match
		for k, v := range obj {
			if !e.isPrimitive(v) {
				return false
			}
			// Quick check if key exists in first keys
			found := false
			for _, fk := range firstKeys {
				if k == fk {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}

	return true
}

func (e *encoder) isPrimitive(v interface{}) bool {
	switch v.(type) {
	case nil, bool, float64, string:
		return true
	default:
		return false
	}
}

func (e *encoder) isPrimitiveArray(arr []interface{}) bool {
	for _, item := range arr {
		if !e.isPrimitive(item) {
			return false
		}
	}
	return true
}

func (e *encoder) encodeTabular(arr []interface{}, depth int, key string) (string, error) {
	firstObj := arr[0].(map[string]interface{})

	// Reuse key buffer
	e.keyBuffer = e.keyBuffer[:0]
	for k := range firstObj {
		e.keyBuffer = append(e.keyBuffer, k)
	}
	sort.Strings(e.keyBuffer)

	var b strings.Builder
	if key != "" {
		b.WriteString(key)
	}
	b.WriteByte('[')
	b.WriteString(strconv.Itoa(len(arr)))
	b.WriteString("]{")
	for i, field := range e.keyBuffer {
		if i > 0 {
			b.WriteString(e.delimiter)
		}
		b.WriteString(field)
	}
	b.WriteString("}:")

	indent := e.getIndent(depth + 1)
	for _, item := range arr {
		b.WriteByte('\n')
		b.WriteString(indent)
		obj := item.(map[string]interface{})
		for i, field := range e.keyBuffer {
			if i > 0 {
				b.WriteString(e.delimiter)
			}
			encoded, err := e.encode(obj[field], depth+1)
			if err != nil {
				return "", err
			}
			b.WriteString(encoded)
		}
	}

	return b.String(), nil
}

func (e *encoder) encodePrimitiveArray(arr []interface{}, key string) (string, error) {
	var b strings.Builder
	if key != "" {
		b.WriteString(key)
	}
	b.WriteByte('[')
	b.WriteString(strconv.Itoa(len(arr)))
	b.WriteString("]: ")

	for i, item := range arr {
		if i > 0 {
			b.WriteString(e.delimiter)
		}
		encoded, err := e.encode(item, 0)
		if err != nil {
			return "", err
		}
		b.WriteString(encoded)
	}

	return b.String(), nil
}

func (e *encoder) encodeListArray(arr []interface{}, depth int, key string) (string, error) {
	var b strings.Builder
	if key != "" {
		b.WriteString(key)
	}
	b.WriteByte('[')
	b.WriteString(strconv.Itoa(len(arr)))
	b.WriteString("]:")

	indent := e.getIndent(depth + 1)
	for _, item := range arr {
		b.WriteByte('\n')
		switch v := item.(type) {
		case map[string]interface{}:
			if len(v) == 0 {
				b.WriteString(indent)
				b.WriteByte('-')
			} else {
				// Reuse key buffer
				e.keyBuffer = e.keyBuffer[:0]
				for k := range v {
					e.keyBuffer = append(e.keyBuffer, k)
				}
				sort.Strings(e.keyBuffer)

				firstKey := e.keyBuffer[0]
				firstValue := v[firstKey]
				encodedKey := e.encodeKey(firstKey)

				switch fv := firstValue.(type) {
				case map[string]interface{}:
					b.WriteString(indent)
					b.WriteString("- ")
					b.WriteString(encodedKey)
					b.WriteByte(':')
					if len(fv) > 0 {
						nested, err := e.encodeObject(fv, depth+2)
						if err != nil {
							return "", err
						}
						if nested != "" {
							b.WriteByte('\n')
							b.WriteString(nested)
						}
					}
				case []interface{}:
					arrayStr, err := e.encodeArray(fv, depth+1, encodedKey)
					if err != nil {
						return "", err
					}
					b.WriteString(indent)
					b.WriteString("- ")
					b.WriteString(arrayStr)
				default:
					encoded, err := e.encode(firstValue, depth+1)
					if err != nil {
						return "", err
					}
					b.WriteString(indent)
					b.WriteString("- ")
					b.WriteString(encodedKey)
					b.WriteString(": ")
					b.WriteString(encoded)
				}

				for _, k := range e.keyBuffer[1:] {
					value := v[k]
					encodedKey := e.encodeKey(k)

					b.WriteByte('\n')
					switch val := value.(type) {
					case map[string]interface{}:
						b.WriteString(indent)
						b.WriteString(encodedKey)
						b.WriteByte(':')
						if len(val) > 0 {
							nested, err := e.encodeObject(val, depth+2)
							if err != nil {
								return "", err
							}
							if nested != "" {
								b.WriteByte('\n')
								b.WriteString(nested)
							}
						}
					case []interface{}:
						arrayStr, err := e.encodeArray(val, depth+1, encodedKey)
						if err != nil {
							return "", err
						}
						b.WriteString(indent)
						b.WriteString(arrayStr)
					default:
						encoded, err := e.encode(value, depth+1)
						if err != nil {
							return "", err
						}
						b.WriteString(indent)
						b.WriteString(encodedKey)
						b.WriteString(": ")
						b.WriteString(encoded)
					}
				}
			}
		default:
			encoded, err := e.encode(item, depth+1)
			if err != nil {
				return "", err
			}
			b.WriteString(indent)
			b.WriteString("- ")
			b.WriteString(encoded)
		}
	}

	return b.String(), nil
}
