package toon

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type decoder struct {
	strict     bool
	indentSize int
}

func newDecoder(strict bool, indentSize int) *decoder {
	return &decoder{
		strict:     strict,
		indentSize: indentSize,
	}
}

var (
	headerRegex = regexp.MustCompile(`^(\w+)?\[(\d+)(?:([,\t|]))?\](?:\{([^}]+)\})?:(.*)$`)
	keyValueRegex = regexp.MustCompile(`^([^:]+):\s*(.*)$`)
)

func (d *decoder) decode(data string) (interface{}, error) {
	lines := strings.Split(strings.TrimRight(data, "\n"), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return map[string]interface{}{}, nil
	}
	
	// Remove empty lines
	var nonEmptyLines []string
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			nonEmptyLines = append(nonEmptyLines, line)
		}
	}
	
	if len(nonEmptyLines) == 0 {
		return map[string]interface{}{}, nil
	}
	
	// Determine root form
	firstLine := strings.TrimSpace(nonEmptyLines[0])
	
	// Check if it's a root array (starts with [N] without a key)
	if matches := headerRegex.FindStringSubmatch(firstLine); matches != nil && matches[1] == "" {
		return d.decodeRootArray(nonEmptyLines)
	}
	
	// Single primitive value
	if len(nonEmptyLines) == 1 && !strings.Contains(firstLine, ":") {
		return d.parseValue(firstLine), nil
	}
	
	return d.decodeObject(nonEmptyLines, 0)
}

func (d *decoder) isArrayHeader(line string) bool {
	return headerRegex.MatchString(strings.TrimSpace(line))
}

func (d *decoder) decodeRootArray(lines []string) (interface{}, error) {
	return d.decodeArray(lines, 0, "")
}

func (d *decoder) decodeObject(lines []string, startDepth int) (interface{}, error) {
	result := make(map[string]interface{})
	i := 0
	
	for i < len(lines) {
		line := lines[i]
		depth := d.getIndentDepth(line)
		
		if depth < startDepth {
			break
		}
		
		if depth > startDepth {
			i++
			continue
		}
		
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			i++
			continue
		}
		
		if d.isArrayHeader(trimmed) {
			key, arr, consumed, err := d.decodeArrayFromLines(lines[i:], depth)
			if err != nil {
				return nil, err
			}
			result[key] = arr
			i += consumed
		} else if match := keyValueRegex.FindStringSubmatch(trimmed); match != nil {
			key := d.parseKey(strings.TrimSpace(match[1]))
			valueStr := strings.TrimSpace(match[2])
			
			if valueStr == "" {
				// Nested object
				nestedLines := d.collectNestedLines(lines, i+1, depth+1)
				if len(nestedLines) == 0 {
					result[key] = map[string]interface{}{}
				} else {
					nested, err := d.decodeObject(nestedLines, depth+1)
					if err != nil {
						return nil, err
					}
					result[key] = nested
				}
				i += len(nestedLines) + 1
			} else {
				result[key] = d.parseValue(valueStr)
				i++
			}
		} else {
			i++
		}
	}
	
	return result, nil
}

func (d *decoder) decodeArray(lines []string, startDepth int, key string) (interface{}, error) {
	if len(lines) == 0 {
		return []interface{}{}, nil
	}
	
	firstLine := strings.TrimSpace(lines[0])
	matches := headerRegex.FindStringSubmatch(firstLine)
	if matches == nil {
		return nil, fmt.Errorf("invalid array header: %s", firstLine)
	}
	
	length, _ := strconv.Atoi(matches[2])
	delimiter := ","
	if matches[3] != "" {
		delimiter = matches[3]
	}
	
	fields := matches[4]
	inline := strings.TrimSpace(matches[5])
	
	if inline != "" {
		// Primitive array
		return d.parsePrimitiveArray(inline, delimiter, length)
	}
	
	if fields != "" {
		// Tabular array - has field list
		return d.decodeTabularArray(lines[1:], startDepth+1, fields, delimiter, length)
	}
	
	// List array - no field list
	return d.decodeListArray(lines[1:], startDepth+1, length)
}

func (d *decoder) decodeArrayFromLines(lines []string, depth int) (string, interface{}, int, error) {
	firstLine := strings.TrimSpace(lines[0])
	matches := headerRegex.FindStringSubmatch(firstLine)
	if matches == nil {
		return "", nil, 0, fmt.Errorf("invalid array header: %s", firstLine)
	}
	
	key := matches[1]
	if key == "" {
		return "", nil, 0, fmt.Errorf("missing key in array header")
	}
	
	arrayLines := d.collectArrayLines(lines, depth)
	arr, err := d.decodeArray(arrayLines, depth, key)
	return key, arr, len(arrayLines), err
}

func (d *decoder) collectArrayLines(lines []string, startDepth int) []string {
	result := []string{lines[0]} // Include header
	
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		depth := d.getIndentDepth(line)
		
		if depth <= startDepth && strings.TrimSpace(line) != "" {
			break
		}
		
		result = append(result, line)
	}
	
	return result
}

func (d *decoder) collectNestedLines(lines []string, start, minDepth int) []string {
	var result []string
	
	for i := start; i < len(lines); i++ {
		line := lines[i]
		depth := d.getIndentDepth(line)
		
		if depth < minDepth && strings.TrimSpace(line) != "" {
			break
		}
		
		result = append(result, line)
	}
	
	return result
}

func (d *decoder) parsePrimitiveArray(inline, delimiter string, expectedLength int) (interface{}, error) {
	if inline == "" {
		return []interface{}{}, nil
	}
	
	values := d.splitByDelimiter(inline, delimiter)
	result := make([]interface{}, len(values))
	
	for i, val := range values {
		result[i] = d.parseValue(strings.TrimSpace(val))
	}
	
	if d.strict && len(result) != expectedLength {
		return nil, fmt.Errorf("array length mismatch: expected %d, got %d", expectedLength, len(result))
	}
	
	return result, nil
}

func (d *decoder) decodeTabularArray(lines []string, startDepth int, fieldsStr, delimiter string, expectedLength int) (interface{}, error) {
	fields := d.splitByDelimiter(fieldsStr, delimiter)
	for i, field := range fields {
		fields[i] = d.parseKey(strings.TrimSpace(field))
	}
	
	var result []interface{}
	
	for _, line := range lines {
		depth := d.getIndentDepth(line)
		trimmed := strings.TrimSpace(line)
		
		if depth < startDepth || trimmed == "" {
			continue
		}
		
		if depth > startDepth {
			break
		}
		
		// Check if this is actually a tabular row
		if !d.isTabularRow(trimmed, delimiter) {
			break
		}
		
		values := d.splitByDelimiter(trimmed, delimiter)
		if d.strict && len(values) != len(fields) {
			return nil, fmt.Errorf("row value count mismatch: expected %d, got %d", len(fields), len(values))
		}
		
		obj := make(map[string]interface{})
		for i, field := range fields {
			if i < len(values) {
				obj[field] = d.parseValue(strings.TrimSpace(values[i]))
			}
		}
		
		result = append(result, obj)
	}
	
	if d.strict && len(result) != expectedLength {
		return nil, fmt.Errorf("array length mismatch: expected %d, got %d", expectedLength, len(result))
	}
	
	return result, nil
}

func (d *decoder) decodeListArray(lines []string, startDepth int, expectedLength int) (interface{}, error) {
	var result []interface{}
	
	i := 0
	for i < len(lines) {
		line := lines[i]
		depth := d.getIndentDepth(line)
		trimmed := strings.TrimSpace(line)
		
		if trimmed == "" {
			i++
			continue
		}
		
		if depth < startDepth {
			break
		}
		
		if depth > startDepth {
			i++
			continue
		}
		
		if !strings.HasPrefix(trimmed, "- ") {
			break
		}
		
		itemContent := strings.TrimSpace(trimmed[2:])
		
		if itemContent == "" {
			result = append(result, map[string]interface{}{})
			i++
		} else if d.isArrayHeader(itemContent) {
			// Nested array
			arrayLines := []string{itemContent}
			j := i + 1
			for j < len(lines) {
				nextLine := lines[j]
				nextDepth := d.getIndentDepth(nextLine)
				if nextDepth <= depth && strings.TrimSpace(nextLine) != "" {
					break
				}
				arrayLines = append(arrayLines, nextLine)
				j++
			}
			
			arr, err := d.decodeArray(arrayLines, depth, "")
			if err != nil {
				return nil, err
			}
			result = append(result, arr)
			i = j
		} else if strings.Contains(itemContent, ":") {
			// Object - collect all lines for this object
			objLines := []string{line}
			j := i + 1
			
			// Collect all lines that belong to this object
			for j < len(lines) {
				nextLine := lines[j]
				nextDepth := d.getIndentDepth(nextLine)
				nextTrimmed := strings.TrimSpace(nextLine)
				
				// Stop if we hit another list item or go back to parent level
				if nextDepth < startDepth || (nextDepth == startDepth && strings.HasPrefix(nextTrimmed, "- ")) {
					break
				}
				
				objLines = append(objLines, nextLine)
				j++
			}
			
			obj, err := d.decodeListItemObject(objLines, startDepth)
			if err != nil {
				return nil, err
			}
			result = append(result, obj)
			i = j
		} else {
			// Primitive
			result = append(result, d.parseValue(itemContent))
			i++
		}
	}
	
	if d.strict && len(result) != expectedLength {
		return nil, fmt.Errorf("array length mismatch: expected %d, got %d", expectedLength, len(result))
	}
	
	return result, nil
}

func (d *decoder) decodeListItemObject(lines []string, itemDepth int) (interface{}, error) {
	result := make(map[string]interface{})
	
	// Parse first line (the one with the hyphen)
	firstLine := strings.TrimSpace(lines[0])
	if !strings.HasPrefix(firstLine, "- ") {
		return nil, fmt.Errorf("invalid list item: %s", firstLine)
	}
	
	firstContent := strings.TrimSpace(firstLine[2:])
	if match := keyValueRegex.FindStringSubmatch(firstContent); match != nil {
		key := d.parseKey(strings.TrimSpace(match[1]))
		valueStr := strings.TrimSpace(match[2])
		
		if valueStr == "" {
			// First field is nested object - find its content
			nestedLines := d.collectNestedLines(lines, 1, itemDepth+1)
			if len(nestedLines) == 0 {
				result[key] = map[string]interface{}{}
			} else {
				nested, err := d.decodeObject(nestedLines, itemDepth+1)
				if err != nil {
					return nil, err
				}
				result[key] = nested
			}
		} else {
			result[key] = d.parseValue(valueStr)
		}
	}
	
	// Parse remaining lines - look for fields at itemDepth (same as hyphen)
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		depth := d.getIndentDepth(line)
		trimmed := strings.TrimSpace(line)
		
		if trimmed == "" {
			continue
		}
		
		// Process lines at itemDepth (same as hyphen) OR itemDepth+1 (indented) for remaining fields
		if depth == itemDepth || depth == itemDepth + 1 {
			if d.isArrayHeader(trimmed) {
				// Array at same level as hyphen
				arrayLines := d.collectArrayLines(lines[i:], depth)
				key, arr, _, err := d.decodeArrayFromLines(arrayLines, depth)
				if err != nil {
					return nil, err
				}
				result[key] = arr
				// Skip the lines we just processed
				i += len(arrayLines) - 1
			} else if match := keyValueRegex.FindStringSubmatch(trimmed); match != nil {
				key := d.parseKey(strings.TrimSpace(match[1]))
				valueStr := strings.TrimSpace(match[2])
				
				if valueStr == "" {
					// Nested object
					nestedLines := d.collectNestedLines(lines, i+1, depth+1)
					if len(nestedLines) == 0 {
						result[key] = map[string]interface{}{}
					} else {
						nested, err := d.decodeObject(nestedLines, depth+1)
						if err != nil {
							return nil, err
						}
						result[key] = nested
					}
					// Skip the nested lines
					i += len(nestedLines)
				} else {
					result[key] = d.parseValue(valueStr)
				}
			}
		}
	}
	
	return result, nil
}

func (d *decoder) isTabularRow(line, delimiter string) bool {
	// If line starts with "- ", it's definitely a list item, not a tabular row
	if strings.HasPrefix(strings.TrimSpace(line), "- ") {
		return false
	}
	
	// A line is a tabular row if it has no colon OR if it has a delimiter before the first colon
	colonPos := strings.Index(line, ":")
	delimPos := strings.Index(line, delimiter)
	
	if colonPos == -1 {
		return delimPos != -1 // Only tabular if it has the delimiter
	}
	
	if delimPos == -1 {
		return false // No delimiter, so it's a key-value line
	}
	
	return delimPos < colonPos
}

func (d *decoder) splitByDelimiter(s, delimiter string) []string {
	if delimiter == "\t" {
		return strings.Split(s, "\t")
	}
	return strings.Split(s, delimiter)
}

func (d *decoder) getIndentDepth(line string) int {
	count := 0
	for _, char := range line {
		if char == ' ' {
			count++
		} else {
			break
		}
	}
	
	// Auto-detect indentation if not configured
	if d.indentSize == 0 {
		return count / 2 // Default to 2-space
	}
	return count / d.indentSize
}

func (d *decoder) parseKey(s string) string {
	if strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") {
		return d.unescapeString(s[1 : len(s)-1])
	}
	return s
}

func (d *decoder) parseValue(s string) interface{} {
	if s == "null" {
		return nil
	}
	if s == "true" {
		return true
	}
	if s == "false" {
		return false
	}
	
	if strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") {
		return d.unescapeString(s[1 : len(s)-1])
	}
	
	// Try parsing as number
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		// Check for leading zeros (invalid numbers)
		if strings.HasPrefix(s, "0") && len(s) > 1 && s[1] != '.' && s[1] != 'e' && s[1] != 'E' {
			return s // Treat as string
		}
		return f
	}
	
	return s
}

func (d *decoder) unescapeString(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s // Fast path: no escapes
	}
	
	var result strings.Builder
	result.Grow(len(s))
	
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '\\':
				result.WriteByte('\\')
			case '"':
				result.WriteByte('"')
			case 'n':
				result.WriteByte('\n')
			case 'r':
				result.WriteByte('\r')
			case 't':
				result.WriteByte('\t')
			default:
				result.WriteByte(s[i])
				result.WriteByte(s[i+1])
			}
			i++ // Skip next character
		} else {
			result.WriteByte(s[i])
		}
	}
	
	return result.String()
}