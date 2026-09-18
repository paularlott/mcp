package mcp

import (
	"reflect"
	"testing"
)

// TestToolRequest_StringSliceOr covers the *Or wrapper for StringSlice, which
// has no direct test (only String()/StringOr() are exercised via handlers).
func TestToolRequest_StringSliceOr(t *testing.T) {
	req := NewToolRequest(map[string]any{
		"good": []any{"a", "b"},
		"bad":  []any{"a", 1},
		"nota": "not-an-array",
	})

	if got, want := req.StringSliceOr("good", nil), []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("StringSliceOr(good) = %v, want %v", got, want)
	}
	if got, want := req.StringSliceOr("missing", []string{"def"}), []string{"def"}; !reflect.DeepEqual(got, want) {
		t.Errorf("StringSliceOr(missing) = %v, want %v", got, want)
	}
	if got, want := req.StringSliceOr("bad", []string{"def"}), []string{"def"}; !reflect.DeepEqual(got, want) {
		t.Errorf("StringSliceOr(bad) = %v, want %v", got, want)
	}
	if got, want := req.StringSliceOr("nota", []string{"def"}), []string{"def"}; !reflect.DeepEqual(got, want) {
		t.Errorf("StringSliceOr(nota) = %v, want %v", got, want)
	}
}

// TestToolRequest_IntSlice covers IntSlice (mixed int/float64 elements, the
// non-array error, and the non-number element error) plus IntSliceOr.
func TestToolRequest_IntSlice(t *testing.T) {
	req := NewToolRequest(map[string]any{
		"ints": []any{1, 2.0, 3},
		"bad":  []any{1, "two"},
		"nota": "nope",
	})

	got, err := req.IntSlice("ints")
	if err != nil {
		t.Fatalf("IntSlice(ints) error: %v", err)
	}
	if want := []int{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Errorf("IntSlice(ints) = %v, want %v", got, want)
	}

	if _, err := req.IntSlice("bad"); err == nil {
		t.Error("IntSlice(bad) expected error for non-number element")
	}
	if _, err := req.IntSlice("nota"); err == nil {
		t.Error("IntSlice(nota) expected error for non-array")
	}
	if _, err := req.IntSlice("missing"); err != ErrUnknownParameter {
		t.Errorf("IntSlice(missing) = %v, want ErrUnknownParameter", err)
	}

	if got, want := req.IntSliceOr("ints", nil), []int{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Errorf("IntSliceOr(ints) = %v, want %v", got, want)
	}
	if got, want := req.IntSliceOr("missing", []int{9}), []int{9}; !reflect.DeepEqual(got, want) {
		t.Errorf("IntSliceOr(missing) = %v, want %v", got, want)
	}
}

// TestToolRequest_FloatSlice covers FloatSlice's success/error paths and
// FloatSliceOr.
func TestToolRequest_FloatSlice(t *testing.T) {
	req := NewToolRequest(map[string]any{
		"floats": []any{1.5, 2.5},
		"bad":    []any{1.5, "x"},
		"nota":   42,
	})

	got, err := req.FloatSlice("floats")
	if err != nil {
		t.Fatalf("FloatSlice(floats) error: %v", err)
	}
	if want := []float64{1.5, 2.5}; !reflect.DeepEqual(got, want) {
		t.Errorf("FloatSlice(floats) = %v, want %v", got, want)
	}
	if _, err := req.FloatSlice("bad"); err == nil {
		t.Error("FloatSlice(bad) expected error")
	}
	if _, err := req.FloatSlice("nota"); err == nil {
		t.Error("FloatSlice(nota) expected error for non-array")
	}
	if _, err := req.FloatSlice("missing"); err != ErrUnknownParameter {
		t.Errorf("FloatSlice(missing) = %v, want ErrUnknownParameter", err)
	}
	if got, want := req.FloatSliceOr("missing", []float64{9.9}), []float64{9.9}; !reflect.DeepEqual(got, want) {
		t.Errorf("FloatSliceOr(missing) = %v, want %v", got, want)
	}
}

// TestToolRequest_BoolSlice covers BoolSlice (entirely untested) and
// BoolSliceOr.
func TestToolRequest_BoolSlice(t *testing.T) {
	req := NewToolRequest(map[string]any{
		"bools": []any{true, false, true},
		"bad":   []any{true, "x"},
		"nota":  "nope",
	})

	got, err := req.BoolSlice("bools")
	if err != nil {
		t.Fatalf("BoolSlice(bools) error: %v", err)
	}
	if want := []bool{true, false, true}; !reflect.DeepEqual(got, want) {
		t.Errorf("BoolSlice(bools) = %v, want %v", got, want)
	}
	if _, err := req.BoolSlice("bad"); err == nil {
		t.Error("BoolSlice(bad) expected error")
	}
	if _, err := req.BoolSlice("nota"); err == nil {
		t.Error("BoolSlice(nota) expected error for non-array")
	}
	if _, err := req.BoolSlice("missing"); err != ErrUnknownParameter {
		t.Errorf("BoolSlice(missing) = %v, want ErrUnknownParameter", err)
	}

	if got, want := req.BoolSliceOr("bools", nil), []bool{true, false, true}; !reflect.DeepEqual(got, want) {
		t.Errorf("BoolSliceOr(bools) = %v, want %v", got, want)
	}
	if got, want := req.BoolSliceOr("missing", []bool{true}), []bool{true}; !reflect.DeepEqual(got, want) {
		t.Errorf("BoolSliceOr(missing) = %v, want %v", got, want)
	}
	if got, want := req.BoolSliceOr("bad", []bool{false}), []bool{false}; !reflect.DeepEqual(got, want) {
		t.Errorf("BoolSliceOr(bad) = %v, want %v", got, want)
	}
}

// TestToolRequest_ObjectAndObjectOr covers Object/ObjectOr, entirely untested
// paths (existing tests only reach ObjectString/ObjectInt/ObjectBool which go
// through ObjectProperty, not Object directly for the *Or variant).
func TestToolRequest_ObjectAndObjectOr(t *testing.T) {
	req := NewToolRequest(map[string]any{
		"obj":  map[string]any{"k": "v"},
		"nota": "nope",
	})

	got, err := req.Object("obj")
	if err != nil || got["k"] != "v" {
		t.Errorf("Object(obj) = (%v, %v), want ({k:v}, nil)", got, err)
	}
	if _, err := req.Object("nota"); err == nil {
		t.Error("Object(nota) expected error for non-object")
	}
	if _, err := req.Object("missing"); err != ErrUnknownParameter {
		t.Errorf("Object(missing) = %v, want ErrUnknownParameter", err)
	}

	if got := req.ObjectOr("obj", nil); got["k"] != "v" {
		t.Errorf("ObjectOr(obj) = %v, want {k:v}", got)
	}
	def := map[string]any{"d": 1}
	if got := req.ObjectOr("missing", def); !reflect.DeepEqual(got, def) {
		t.Errorf("ObjectOr(missing) = %v, want %v", got, def)
	}
	if got := req.ObjectOr("nota", def); !reflect.DeepEqual(got, def) {
		t.Errorf("ObjectOr(nota) = %v, want %v", got, def)
	}
}

// TestToolRequest_ObjectSlice covers ObjectSlice/ObjectSliceOr, entirely
// untested (0%).
func TestToolRequest_ObjectSlice(t *testing.T) {
	req := NewToolRequest(map[string]any{
		"objs": []any{map[string]any{"a": 1}, map[string]any{"b": 2}},
		"bad":  []any{map[string]any{"a": 1}, "not-an-object"},
		"nota": "nope",
	})

	got, err := req.ObjectSlice("objs")
	if err != nil {
		t.Fatalf("ObjectSlice(objs) error: %v", err)
	}
	if len(got) != 2 || got[0]["a"] != 1 || got[1]["b"] != 2 {
		t.Errorf("ObjectSlice(objs) = %v, want [{a:1} {b:2}]", got)
	}
	if _, err := req.ObjectSlice("bad"); err == nil {
		t.Error("ObjectSlice(bad) expected error for non-object element")
	}
	if _, err := req.ObjectSlice("nota"); err == nil {
		t.Error("ObjectSlice(nota) expected error for non-array")
	}
	if _, err := req.ObjectSlice("missing"); err != ErrUnknownParameter {
		t.Errorf("ObjectSlice(missing) = %v, want ErrUnknownParameter", err)
	}

	def := []map[string]any{{"d": 1}}
	if got := req.ObjectSliceOr("missing", def); !reflect.DeepEqual(got, def) {
		t.Errorf("ObjectSliceOr(missing) = %v, want %v", got, def)
	}
	if got := req.ObjectSliceOr("objs", def); len(got) != 2 {
		t.Errorf("ObjectSliceOr(objs) = %v, want the parsed slice", got)
	}
}

// TestToolRequest_ObjectPropertyFamily covers ObjectProperty/ObjectString/
// ObjectInt/ObjectBool plus their *Or variants and the deprecated
// Get*Property aliases, across success, missing-object, missing-property, and
// wrong-type cases.
func TestToolRequest_ObjectPropertyFamily(t *testing.T) {
	req := NewToolRequest(map[string]any{
		"obj": map[string]any{
			"str":   "hello",
			"intF":  float64(42),
			"intI":  7,
			"bool":  true,
			"wrong": []any{1},
		},
		"nota": "nope",
	})

	// ObjectProperty
	if v, err := req.ObjectProperty("obj", "str"); err != nil || v != "hello" {
		t.Errorf("ObjectProperty(obj,str) = (%v,%v)", v, err)
	}
	if _, err := req.ObjectProperty("obj", "missing"); err == nil {
		t.Error("ObjectProperty(obj,missing) expected error")
	}
	if _, err := req.ObjectProperty("missing", "str"); err != ErrUnknownParameter {
		t.Errorf("ObjectProperty(missing,str) = %v, want ErrUnknownParameter", err)
	}
	if _, err := req.ObjectProperty("nota", "str"); err == nil {
		t.Error("ObjectProperty(nota,str) expected error (not an object)")
	}
	// Deprecated alias
	if v, err := req.GetObjectProperty("obj", "str"); err != nil || v != "hello" {
		t.Errorf("GetObjectProperty(obj,str) = (%v,%v)", v, err)
	}

	// ObjectString
	if v, err := req.ObjectString("obj", "str"); err != nil || v != "hello" {
		t.Errorf("ObjectString(obj,str) = (%v,%v)", v, err)
	}
	if _, err := req.ObjectString("obj", "intF"); err == nil {
		t.Error("ObjectString(obj,intF) expected type error")
	}
	if got := req.ObjectStringOr("obj", "str", "def"); got != "hello" {
		t.Errorf("ObjectStringOr(obj,str) = %q, want hello", got)
	}
	if got := req.ObjectStringOr("obj", "missing", "def"); got != "def" {
		t.Errorf("ObjectStringOr(obj,missing) = %q, want def", got)
	}
	if v, err := req.GetObjectStringProperty("obj", "str"); err != nil || v != "hello" {
		t.Errorf("GetObjectStringProperty(obj,str) = (%v,%v)", v, err)
	}

	// ObjectInt (float64 and int forms)
	if v, err := req.ObjectInt("obj", "intF"); err != nil || v != 42 {
		t.Errorf("ObjectInt(obj,intF) = (%v,%v), want (42,nil)", v, err)
	}
	if v, err := req.ObjectInt("obj", "intI"); err != nil || v != 7 {
		t.Errorf("ObjectInt(obj,intI) = (%v,%v), want (7,nil)", v, err)
	}
	if _, err := req.ObjectInt("obj", "str"); err == nil {
		t.Error("ObjectInt(obj,str) expected type error")
	}
	if got := req.ObjectIntOr("obj", "intF", -1); got != 42 {
		t.Errorf("ObjectIntOr(obj,intF) = %d, want 42", got)
	}
	if got := req.ObjectIntOr("obj", "missing", -1); got != -1 {
		t.Errorf("ObjectIntOr(obj,missing) = %d, want -1", got)
	}
	if v, err := req.GetObjectIntProperty("obj", "intI"); err != nil || v != 7 {
		t.Errorf("GetObjectIntProperty(obj,intI) = (%v,%v)", v, err)
	}

	// ObjectBool
	if v, err := req.ObjectBool("obj", "bool"); err != nil || v != true {
		t.Errorf("ObjectBool(obj,bool) = (%v,%v), want (true,nil)", v, err)
	}
	if _, err := req.ObjectBool("obj", "str"); err == nil {
		t.Error("ObjectBool(obj,str) expected type error")
	}
	if got := req.ObjectBoolOr("obj", "bool", false); got != true {
		t.Errorf("ObjectBoolOr(obj,bool) = %v, want true", got)
	}
	if got := req.ObjectBoolOr("obj", "missing", false); got != false {
		t.Errorf("ObjectBoolOr(obj,missing) = %v, want false", got)
	}
	if v, err := req.GetObjectBoolProperty("obj", "bool"); err != nil || v != true {
		t.Errorf("GetObjectBoolProperty(obj,bool) = (%v,%v)", v, err)
	}
}

// TestToolRequest_BoolOrAndArgs covers BoolOr's error/default path and Args'
// pass-through of the underlying map.
func TestToolRequest_BoolOrAndArgs(t *testing.T) {
	req := NewToolRequest(map[string]any{"flag": true, "nota": "x"})
	if got := req.BoolOr("flag", false); got != true {
		t.Errorf("BoolOr(flag) = %v, want true", got)
	}
	if got := req.BoolOr("missing", true); got != true {
		t.Errorf("BoolOr(missing) = %v, want true (default)", got)
	}
	if got := req.BoolOr("nota", false); got != false {
		t.Errorf("BoolOr(nota) = %v, want false (default, type mismatch)", got)
	}

	args := req.Args()
	if len(args) != 2 || args["flag"] != true {
		t.Errorf("Args() = %v, want passthrough of backing map", args)
	}
}
