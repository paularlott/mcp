package mcp

import (
	"strings"
	"testing"
)

// A namespace containing the separator is rejected at validation time and
// panics in the constructors: every namespaced tool name it produced would
// be ambiguous ("a__b__tool" could belong to "a" or "a__b").
func TestValidateNamespace(t *testing.T) {
	for _, ns := range []string{"", "scriptling", "knot2", "a.b"} {
		if err := ValidateNamespace(ns); err != nil {
			t.Errorf("ValidateNamespace(%q) = %v, want nil", ns, err)
		}
	}
	for _, ns := range []string{"a__b", "__x", "x__"} {
		if err := ValidateNamespace(ns); err == nil {
			t.Errorf("ValidateNamespace(%q) = nil, want error", ns)
		}
	}
}

func TestNormalizeNamespace(t *testing.T) {
	cases := map[string]string{
		"":      "",
		"  ":    "",
		"fed":   "fed" + DefaultNamespaceSeparator,
		" fed ": "fed" + DefaultNamespaceSeparator,
	}
	for in, want := range cases {
		if got := normalizeNamespace(in); got != want {
			t.Errorf("normalizeNamespace(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewClientRejectsSeparatorInNamespace(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on a namespace containing the separator")
		}
		if !strings.Contains(fmtSprintf(r), DefaultNamespaceSeparator) {
			t.Fatalf("panic should name the separator, got: %v", r)
		}
	}()
	NewClient("http://127.0.0.1:1", nil, "a"+DefaultNamespaceSeparator+"b")
}

func fmtSprintf(v any) string {
	s, _ := v.(error)
	if s != nil {
		return s.Error()
	}
	return ""
}
