package codegen

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestApplyOptions_Primitives(t *testing.T) {
	type cfg struct {
		Package string
		Main    bool
		Count   int
		Ratio   float64
		Color   string
	}
	opts := &ir.StructLit{
		Fields: []ir.FieldInit{
			{Name: "package", Value: &ir.Literal{Type: ir.TypString, Value: `"com.example"`}},
			{Name: "main", Value: &ir.Literal{Type: ir.TypBool, Value: "true"}},
			{Name: "count", Value: &ir.Literal{Type: ir.TypInt, Value: "42"}},
			{Name: "ratio", Value: &ir.Literal{Type: ir.TypFloat, Value: "0.5"}},
			{Name: "color", Value: &ir.Literal{Type: ir.TypString, Value: "#abc123"}},
		},
	}
	var c cfg
	if err := ApplyOptions(&c, opts); err != nil {
		t.Fatal(err)
	}
	want := cfg{Package: "com.example", Main: true, Count: 42, Ratio: 0.5, Color: "#abc123"}
	if c != want {
		t.Errorf("got %+v want %+v", c, want)
	}
}

func TestApplyOptions_NestedStruct(t *testing.T) {
	type theme struct {
		Primary   string
		Secondary string
	}
	type cfg struct {
		Theme theme
	}
	opts := &ir.StructLit{
		Fields: []ir.FieldInit{
			{Name: "theme", Value: &ir.StructLit{Fields: []ir.FieldInit{
				{Name: "primary", Value: &ir.Literal{Type: ir.TypString, Value: `"#fff"`}},
				{Name: "secondary", Value: &ir.Literal{Type: ir.TypString, Value: `"#000"`}},
			}}},
		},
	}
	var c cfg
	if err := ApplyOptions(&c, opts); err != nil {
		t.Fatal(err)
	}
	if c.Theme.Primary != "#fff" || c.Theme.Secondary != "#000" {
		t.Errorf("got %+v", c)
	}
}

func TestApplyOptions_List(t *testing.T) {
	type cfg struct {
		Tags []string
	}
	opts := &ir.StructLit{
		Fields: []ir.FieldInit{
			{Name: "tags", Value: &ir.ListLit{Elems: []ir.Expr{
				&ir.Literal{Type: ir.TypString, Value: `"a"`},
				&ir.Literal{Type: ir.TypString, Value: `"b"`},
			}}},
		},
	}
	var c cfg
	if err := ApplyOptions(&c, opts); err != nil {
		t.Fatal(err)
	}
	if len(c.Tags) != 2 || c.Tags[0] != "a" || c.Tags[1] != "b" {
		t.Errorf("got %v", c.Tags)
	}
}

func TestApplyOptions_UnknownField(t *testing.T) {
	// Unknown fields are silently skipped: the merged options envelope is a
	// union of stdlib + lang + platform fields, and each Go Config only
	// declares the subset it cares about.
	type cfg struct {
		Known string
	}
	opts := &ir.StructLit{
		Fields: []ir.FieldInit{
			{Name: "known", Value: &ir.Literal{Type: ir.TypString, Value: `"yes"`}},
			{Name: "unknown", Value: &ir.Literal{Type: ir.TypString, Value: `"x"`}},
		},
	}
	var c cfg
	if err := ApplyOptions(&c, opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Known != "yes" {
		t.Errorf("known: got %q want yes", c.Known)
	}
}

func TestApplyOptions_Nil(t *testing.T) {
	type cfg struct{ X string }
	var c cfg
	if err := ApplyOptions(&c, nil); err != nil {
		t.Fatal(err)
	}
	if c.X != "" {
		t.Errorf("expected zero value, got %q", c.X)
	}
}

func TestApplyOptions_TypeMismatch(t *testing.T) {
	type cfg struct {
		N int
	}
	opts := &ir.StructLit{Fields: []ir.FieldInit{
		{Name: "n", Value: &ir.Literal{Type: ir.TypString, Value: `"abc"`}},
	}}
	var c cfg
	err := ApplyOptions(&c, opts)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOptionsFromMap(t *testing.T) {
	lit := OptionsFromMap(map[string]any{
		"name":    "MyApp",
		"main":    true,
		"version": 3,
	})
	if len(lit.Fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(lit.Fields))
	}
	type cfg struct {
		Name    string
		Main    bool
		Version int
	}
	var c cfg
	if err := ApplyOptions(&c, lit); err != nil {
		t.Fatal(err)
	}
	if c.Name != "MyApp" || !c.Main || c.Version != 3 {
		t.Errorf("got %+v", c)
	}
}

func TestOptionField(t *testing.T) {
	lit := OptionsFromMap(map[string]any{"x": "hello"})
	v, ok := OptionField(lit, "x")
	if !ok {
		t.Fatal("expected x to be present")
	}
	if l, ok := v.(*ir.Literal); !ok || l.Value != "hello" {
		t.Errorf("unexpected value %#v", v)
	}
	if _, ok := OptionField(lit, "missing"); ok {
		t.Error("expected missing to be absent")
	}
}

func TestSetOptionField(t *testing.T) {
	lit := &ir.StructLit{}
	SetOptionField(lit, "a", "x")
	SetOptionField(lit, "b", true)
	SetOptionField(lit, "a", "y") // overwrite
	if len(lit.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d: %+v", len(lit.Fields), lit.Fields)
	}
	v, _ := OptionField(lit, "a")
	if v.(*ir.Literal).Value != "y" {
		t.Errorf("a should be y, got %v", v)
	}
}
