package mcp

import (
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// checkArguments checks a tool call's arguments against the tool's
// inputSchema, as the tools spec requires of servers ("Validate all tool
// inputs"), and returns the arguments the handler should see.
//
// Before validating, a string sent for a parameter whose schema declares
// number, integer or boolean, and not string, is converted when the whole
// string is such a value: "10" becomes 10, "2.5" becomes 2.5 and "true"
// becomes true. Models often quote scalars, and these values mean only one
// thing. Anything else, including a string that only partly parses, is
// left for validation to refuse. Conversion follows properties and items,
// so nested objects and arrays get it too. args is never modified; a copy
// is returned when a value was converted.
//
// Everything else in the schema applies as written: required properties,
// types, constraints, enums and additionalProperties (an unknown argument
// is refused, not dropped, so a misspelt filter can't widen a call).
//
// A violation is an input validation error. Server.CallTool callers get it
// as a *ToolError (-32602); tools/call reports it as a tool execution error
// (isError: true), as the spec classes input validation errors, so the
// model reads the message and can correct the call.
func checkArguments(inputSchema any, args map[string]any) (map[string]any, error) {
	schema, ok := inputSchema.(map[string]any)
	if !ok {
		return args, nil
	}
	if converted, changed := convertScalarStrings(schema, args, 0); changed {
		args = converted.(map[string]any)
	}
	value := args
	if value == nil {
		value = map[string]any{}
	}
	if err := validateJSONSchema(schema, value); err != nil {
		msg := err.Error()
		if se, ok := err.(schemaError); ok && se.path == "" {
			msg = "arguments " + se.msg
		} else if ok {
			msg = "parameter " + se.path + " " + se.msg
		}
		return nil, invalidArguments(msg, pathParameter(err))
	}
	return args, nil
}

// maxConvertDepth bounds convertScalarStrings on deeply nested arguments;
// validation reports anything that deep on its own.
const maxConvertDepth = 64

// convertScalarStrings returns value with every string converted that
// convertScalarString converts, following the schema's properties and
// items. changed reports whether anything was converted; value itself is
// never modified.
func convertScalarStrings(schema any, value any, depth int) (any, bool) {
	s, ok := schema.(map[string]any)
	if !ok || depth > maxConvertDepth {
		return value, false
	}
	switch v := value.(type) {
	case string:
		return convertScalarString(s["type"], v)
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		var out map[string]any
		for k, item := range v {
			sub, ok := props[k]
			if !ok {
				continue
			}
			if nv, changed := convertScalarStrings(sub, item, depth+1); changed {
				if out == nil {
					out = maps.Clone(v)
				}
				out[k] = nv
			}
		}
		if out != nil {
			return out, true
		}
	case []any:
		var out []any
		for i, item := range v {
			if nv, changed := convertScalarStrings(s["items"], item, depth+1); changed {
				if out == nil {
					out = slices.Clone(v)
				}
				out[i] = nv
			}
		}
		if out != nil {
			return out, true
		}
	}
	return value, false
}

// jsonNumberText is the JSON number grammar (RFC 8259): no leading +, no
// leading zeros, no hex, Inf or NaN, which strconv.ParseFloat would take.
var jsonNumberText = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// convertScalarString converts v to the type a schema "type" keyword
// declares when that type is number, integer or boolean (and string is not
// allowed) and v is exactly a JSON value of it.
func convertScalarString(schemaType any, v string) (any, bool) {
	types := schemaTypes(schemaType)
	if len(types) == 0 || types["string"] {
		return v, false
	}
	if types["boolean"] {
		switch v {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	if (types["number"] || types["integer"]) && jsonNumberText.MatchString(v) {
		f, err := strconv.ParseFloat(v, 64)
		if err == nil && !math.IsInf(f, 0) && (types["number"] || f == math.Trunc(f)) {
			return f, true
		}
	}
	return v, false
}

// schemaTypes is the set of types a "type" keyword names.
func schemaTypes(t any) map[string]bool {
	types := map[string]bool{}
	switch v := t.(type) {
	case string:
		types[v] = true
	case []string:
		for _, s := range v {
			types[s] = true
		}
	case []any:
		for _, s := range v {
			if name, ok := s.(string); ok {
				types[name] = true
			}
		}
	}
	return types
}

// invalidArguments is the error for a tool call whose arguments the tool
// can't take: a *ToolError (-32602) to callers of Server.CallTool, which
// tools/call reports as a tool execution error (isError), as the spec asks
// of input validation errors, so the model can correct the call.
func invalidArguments(msg, parameter string) error {
	return &ToolError{Code: ErrorCodeInvalidParams, Message: msg, Data: map[string]any{"parameter": parameter}}
}

// pathParameter is the top-level argument name a schema error refers to.
func pathParameter(err error) string {
	se, ok := err.(schemaError)
	if !ok || se.path == "" {
		return ""
	}
	name := se.path
	if i := strings.IndexAny(name, ".["); i > 0 {
		name = name[:i]
	}
	return name
}
