package mcp

import (
	"encoding/base64"
	"testing"
)

func TestToolRequestHelpers(t *testing.T) {
	req := &ToolRequest{args: map[string]any{
		"s":    "x",
		"i":    5.0,
		"b":    true,
		"ss":   []any{"a", "b"},
		"ii":   []any{1.0, 2.0},
		"ff":   []any{1.5, 2.5},
		"obj":  map[string]any{"k": "v", "n": 3.0, "t": true},
		"objs": []any{map[string]any{"a": 1.0}},
	}}

	if v, _ := req.String("s"); v != "x" {
		t.Fatal("string")
	}
	if v := req.StringOr("sx", "d"); v != "d" {
		t.Fatal("string or")
	}
	if v, _ := req.Int("i"); v != 5 {
		t.Fatal("int")
	}
	if v := req.IntOr("ix", 7); v != 7 {
		t.Fatal("int or")
	}
	if v, _ := req.Float("i"); v != 5.0 {
		t.Fatal("float")
	}
	if v := req.FloatOr("fx", 1.2); v != 1.2 {
		t.Fatal("float or")
	}
	if v, _ := req.Bool("b"); !v {
		t.Fatal("bool")
	}
	if v := req.BoolOr("bx", true); !v {
		t.Fatal("bool or")
	}
	if v, _ := req.StringSlice("ss"); len(v) != 2 || v[1] != "b" {
		t.Fatal("str slice")
	}
	if v, _ := req.IntSlice("ii"); len(v) != 2 || v[0] != 1 {
		t.Fatal("int slice")
	}
	if v, _ := req.FloatSlice("ff"); len(v) != 2 || v[1] != 2.5 {
		t.Fatal("float slice")
	}
	if v, _ := req.Object("obj"); v["k"].(string) != "v" {
		t.Fatal("obj")
	}
	if v, _ := req.ObjectSlice("objs"); len(v) != 1 {
		t.Fatal("obj slice")
	}
	if v, _ := req.GetObjectStringProperty("obj", "k"); v != "v" {
		t.Fatal("obj str prop")
	}
	if v, _ := req.GetObjectIntProperty("obj", "n"); v != 3 {
		t.Fatal("obj int prop")
	}
	if v, _ := req.GetObjectBoolProperty("obj", "t"); !v {
		t.Fatal("obj bool prop")
	}
}

func TestToolResponseHelpers(t *testing.T) {
	r := NewToolResponseText("hi")
	if len(r.Content) != 1 || r.Content[0].Type != "text" {
		t.Fatal("text")
	}

	r = NewToolResponseJSON(map[string]any{"a": 1})
	if r.Content[0].Type != "text" || r.Content[0].Text == "" {
		t.Fatal("json")
	}

	r = NewToolResponseTOON(map[string]any{"a": 1})
	if r.Content[0].Type != "text" || r.Content[0].Text == "" {
		t.Fatal("toon")
	}

	img := NewToolResponseImage([]byte{0x01, 0x02}, "image/png")
	if img.Content[0].Type != "image" || img.Content[0].MimeType != "image/png" {
		t.Fatal("image type")
	}
	if img.Content[0].Data != base64.StdEncoding.EncodeToString([]byte{0x01, 0x02}) {
		t.Fatal("image data")
	}

	aud := NewToolResponseAudio([]byte{0x03}, "audio/wav")
	if aud.Content[0].Type != "audio" || aud.Content[0].MimeType != "audio/wav" {
		t.Fatal("audio")
	}

	res := NewToolResponseResource("file://x", "hello", "text/plain")
	if res.Content[0].Type != "resource" || res.Content[0].Resource.URI != "file://x" {
		t.Fatal("res")
	}

	link := NewToolResponseResourceLink("https://x", "open")
	if link.Content[0].Type != "resource_link" || link.Content[0].Resource.URI != "https://x" {
		t.Fatal("link")
	}

	st := NewToolResponseStructured(map[string]any{"k": "v"})
	if st.StructuredContent == nil || st.Content != nil {
		t.Fatal("structured")
	}

	combined := NewToolResponseMulti(r, img)
	if len(combined.Content) != len(r.Content)+len(img.Content) {
		t.Fatal("multi")
	}
}

func TestToolRequestErrors(t *testing.T) {
	req := &ToolRequest{args: map[string]any{"x": 1.0, "arr": []any{"a", 2}}}
	if _, err := req.String("missing"); err == nil {
		t.Fatal("expected missing param error")
	}
	if _, err := req.String("x"); err == nil {
		t.Fatal("expected type error for string")
	}
	if _, err := req.StringSlice("arr"); err == nil {
		t.Fatal("expected mixed-type array to error")
	}
	if _, err := req.Object("x"); err == nil {
		t.Fatal("expected not object error")
	}
}

func TestStructuredContentPrecedenceInMulti(t *testing.T) {
	a := NewToolResponseStructured(map[string]any{"a": 1})
	b := NewToolResponseStructured(map[string]any{"b": 2})
	m := NewToolResponseMulti(a, b)
	if sc, ok := m.StructuredContent.(map[string]any); !ok || sc["b"].(int) != 2 {
		t.Fatal("expected last structured content to win")
	}
}
