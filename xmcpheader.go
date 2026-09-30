package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// The x-mcp-header mechanism (2026-07-28, Streamable HTTP §Custom Headers
// from Tool Parameters): a tool's inputSchema may mark primitive parameters
// with "x-mcp-header": "<Name>", and a client calling that tool over HTTP
// MUST mirror the argument's value into an "Mcp-Param-<Name>" header so
// intermediaries can route on it without parsing the body. A server that
// processes the body MUST check every such header against the body and
// reject a mismatch — a missing header for a present value, a header whose
// (decoded) value differs, or one with invalid characters — with 400
// HeaderMismatch (-32020).
//
// Constraints on an annotation (a tool definition violating any of them is
// invalid, and an HTTP client MUST exclude that tool from tools/list):
//   - a non-empty HTTP token (RFC 9110 tchar), case-insensitively unique
//     within the schema;
//   - only on string, integer or boolean parameters (never number);
//   - only on properties statically reachable from the root through a chain
//     of "properties" keys — never under items, oneOf/anyOf/allOf/not,
//     if/then/else, $ref or anything else.

const (
	xMcpHeaderKey        = "x-mcp-header"
	headerMcpParamPrefix = "Mcp-Param-"

	// maxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER; header-bound
	// integers must lie within ±(2^53-1).
	maxSafeInteger = 1<<53 - 1
)

// headerBinding is one validated x-mcp-header annotation.
type headerBinding struct {
	name string   // header name suffix: Mcp-Param-<name>
	path []string // property path from the schema root
	typ  string   // "string", "integer" or "boolean"
}

func (b headerBinding) headerName() string { return headerMcpParamPrefix + b.name }

// schemaHeaderBindings extracts and validates the x-mcp-header annotations in
// a tool inputSchema. A schema without annotations yields (nil, nil).
func schemaHeaderBindings(schema any) ([]headerBinding, error) {
	root, ok := schema.(map[string]any)
	if !ok {
		// Normalise typed schemas (structs, json.RawMessage) to generic JSON.
		raw, err := json.Marshal(schema)
		if err != nil || json.Unmarshal(raw, &root) != nil {
			return nil, nil
		}
	}
	if !schemaMentionsHeader(root) {
		return nil, nil
	}

	var bindings []headerBinding
	seen := map[string]bool{}
	var walk func(node any, path []string, static bool) error
	walk = func(node any, path []string, static bool) error {
		switch n := node.(type) {
		case []any:
			for _, item := range n {
				if err := walk(item, path, false); err != nil {
					return err
				}
			}
		case map[string]any:
			if raw, has := n[xMcpHeaderKey]; has {
				name, _ := raw.(string)
				if !static || len(path) == 0 {
					return fmt.Errorf("x-mcp-header %q is not on a property reachable through properties keys only", name)
				}
				if !isHTTPToken(name) {
					return fmt.Errorf("x-mcp-header %q on %q is not a valid HTTP header token", name, strings.Join(path, "."))
				}
				key := strings.ToLower(name)
				if seen[key] {
					return fmt.Errorf("x-mcp-header %q is not unique", name)
				}
				seen[key] = true
				typ, _ := n["type"].(string)
				if typ != "string" && typ != "integer" && typ != "boolean" {
					return fmt.Errorf("x-mcp-header %q on %q has type %v; only string, integer and boolean are allowed", name, strings.Join(path, "."), n["type"])
				}
				bindings = append(bindings, headerBinding{name: name, path: append([]string(nil), path...), typ: typ})
			}
			for key, child := range n {
				if key == "properties" {
					props, ok := child.(map[string]any)
					if !ok {
						continue
					}
					for propName, propSchema := range props {
						if err := walk(propSchema, append(append([]string(nil), path...), propName), static); err != nil {
							return err
						}
					}
					continue
				}
				if key == xMcpHeaderKey {
					continue
				}
				if err := walk(child, path, false); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(root, nil, true); err != nil {
		return nil, err
	}
	return bindings, nil
}

// schemaMentionsHeader is a cheap pre-check so schemas without any
// annotation (the common case) skip the full walk.
func schemaMentionsHeader(node any) bool {
	switch n := node.(type) {
	case map[string]any:
		if _, ok := n[xMcpHeaderKey]; ok {
			return true
		}
		for _, v := range n {
			if schemaMentionsHeader(v) {
				return true
			}
		}
	case []any:
		for _, v := range n {
			if schemaMentionsHeader(v) {
				return true
			}
		}
	}
	return false
}

// isHTTPToken reports whether s is a non-empty RFC 9110 token (1*tchar).
func isHTTPToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// bindingArgument returns the argument at b's property path; present is
// false when the path is missing or the value is null (header omitted).
func bindingArgument(args map[string]any, b headerBinding) (value any, present bool) {
	var cur any = args
	for _, key := range b.path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[key]; !ok {
			return nil, false
		}
	}
	return cur, cur != nil
}

// headerValueString converts a bound argument to its header representation:
// strings as-is, integers in decimal, booleans as "true"/"false".
func headerValueString(v any, typ string) (string, error) {
	switch typ {
	case "string":
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("expected a string, got %T", v)
		}
		return s, nil
	case "boolean":
		b, ok := v.(bool)
		if !ok {
			return "", fmt.Errorf("expected a boolean, got %T", v)
		}
		return strconv.FormatBool(b), nil
	case "integer":
		n, ok := integerValue(v)
		if !ok {
			return "", fmt.Errorf("expected an integer within the JavaScript safe range, got %v", v)
		}
		return strconv.FormatInt(n, 10), nil
	}
	return "", fmt.Errorf("unsupported type %q", typ)
}

// integerValue accepts the integer forms an argument can take after JSON
// decoding (float64, json.Number) or from Go callers (int types), within the
// JavaScript safe range.
func integerValue(v any) (int64, bool) {
	var f float64
	switch n := v.(type) {
	case int:
		f = float64(n)
	case int32:
		f = float64(n)
	case int64:
		if n > maxSafeInteger || n < -maxSafeInteger {
			return 0, false
		}
		return n, true
	case float32:
		f = float64(n)
	case float64:
		f = n
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return integerValue(i)
		}
		parsed, err := n.Float64()
		if err != nil {
			return 0, false
		}
		f = parsed
	default:
		return 0, false
	}
	if f != math.Trunc(f) || math.Abs(f) > maxSafeInteger {
		return 0, false
	}
	return int64(f), true
}

// mcpParamHeaders builds the Mcp-Param-* headers for a tools/call to a tool
// with the given bindings. Arguments that are absent or null are omitted.
func mcpParamHeaders(bindings []headerBinding, args map[string]any) (http.Header, error) {
	h := http.Header{}
	for _, b := range bindings {
		v, present := bindingArgument(args, b)
		if !present {
			continue
		}
		s, err := headerValueString(v, b.typ)
		if err != nil {
			return nil, fmt.Errorf("argument %q (x-mcp-header %q): %w", strings.Join(b.path, "."), b.name, err)
		}
		h.Set(b.headerName(), encodeModernHeaderValue(s))
	}
	return h, nil
}

// validateMcpParamHeaders checks a request's Mcp-Param-* headers against its
// tools/call arguments. It returns a non-empty description of the first
// mismatch, for the HeaderMismatch error.
func validateMcpParamHeaders(r *http.Request, bindings []headerBinding, args map[string]any) string {
	for _, b := range bindings {
		raw, sent := r.Header[http.CanonicalHeaderKey(b.headerName())]
		v, present := bindingArgument(args, b)
		if !present {
			// No value: the client must omit the header and the server must
			// not expect it. A header with no value to match cannot be
			// validated, so it is rejected too.
			if sent {
				return fmt.Sprintf("%s header sent but argument %q has no value", b.headerName(), strings.Join(b.path, "."))
			}
			continue
		}
		if !sent || len(raw) != 1 {
			return fmt.Sprintf("%s header is required for argument %q", b.headerName(), strings.Join(b.path, "."))
		}
		if !isValidHeaderValue(raw[0]) {
			return fmt.Sprintf("%s header contains invalid characters", b.headerName())
		}
		got, err := decodeModernHeaderValue(raw[0])
		if err != nil {
			return fmt.Sprintf("%s header has malformed Base64 encoding", b.headerName())
		}
		want, err := headerValueString(v, b.typ)
		if err != nil {
			return fmt.Sprintf("argument %q: %v", strings.Join(b.path, "."), err)
		}
		if b.typ == "integer" {
			// Compared numerically: "42.0" and 42 are equal.
			if hf, err := strconv.ParseFloat(strings.TrimSpace(got), 64); err == nil {
				if wf, _ := strconv.ParseFloat(want, 64); hf == wf {
					continue
				}
			}
		} else if got == want {
			continue
		}
		return fmt.Sprintf("%s header value does not match argument %q", b.headerName(), strings.Join(b.path, "."))
	}
	return ""
}

// isValidHeaderValue reports whether v is a well-formed RFC 9110 field value
// as sent: visible ASCII, space and tab only.
func isValidHeaderValue(v string) bool {
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c != ' ' && c != '\t' && (c < 0x21 || c > 0x7E) {
			return false
		}
	}
	return true
}

// toolInputSchema resolves the inputSchema this server advertises for a tool
// name: a registered tool, a federated remote's tool, or a context
// provider's. nil when the tool is unknown (the call fails later anyway).
func (s *Server) toolInputSchema(ctx context.Context, name string) any {
	s.mu.RLock()
	if tool, ok := s.tools[name]; ok {
		schema := tool.Schema
		s.mu.RUnlock()
		return schema
	}
	remote, isRemote := s.toolToServer[name]
	s.mu.RUnlock()

	if isRemote {
		if tools, err := remote.client.ListTools(ctx); err == nil {
			for _, tool := range tools {
				if tool.Name == name {
					return tool.InputSchema
				}
			}
		}
		return nil
	}
	if entry, ok := providerToolIndex(ctx)[name]; ok {
		return entry.inputSchema
	}
	return nil
}

// checkToolCallHeaders validates a Modern tools/call's Mcp-Param-* headers
// against the tool's x-mcp-header annotations. It returns "" when the request
// is consistent, or the HeaderMismatch message.
func (s *Server) checkToolCallHeaders(r *http.Request, params map[string]any) string {
	name, _ := params["name"].(string)
	bindings, err := schemaHeaderBindings(s.toolInputSchema(r.Context(), name))
	if err != nil || len(bindings) == 0 {
		// No annotations, or none this server could honour: nothing to check.
		return ""
	}
	args, _ := params["arguments"].(map[string]any)
	if msg := validateMcpParamHeaders(r, bindings, args); msg != "" {
		return "Header mismatch: " + msg
	}
	return ""
}
