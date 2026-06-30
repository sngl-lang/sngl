package checker_test

import (
	"fmt"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// unwrapConv strips an outer *ir.Conversion (the checker materializes implicit
// type conversions, e.g. wrapping a color StructLit when assigning to a
// declared `color` typed const).
func unwrapConv(e ir.Expr) ir.Expr {
	if cv, ok := e.(*ir.Conversion); ok {
		return cv.Operand
	}
	return e
}

func TestHexColorLowersToStructLit(t *testing.T) {
	src := `const C color = #ff8040`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}
	var found *ir.Var
	for _, v := range pkg.Consts {
		if v.Name == "C" {
			found = v
			break
		}
	}
	if found == nil {
		t.Fatal("const C not found")
	}
	sl, ok := unwrapConv(found.Init).(*ir.StructLit)
	if !ok {
		t.Fatalf("Init = %T %+v, want *ir.StructLit", found.Init, found.Init)
	}
	if sl.Def == nil || sl.Def.Name != "color" {
		t.Errorf("Def = %v, want color struct def", sl.Def)
	}
	if len(sl.Fields) != 4 {
		t.Fatalf("Fields count = %d, want 4", len(sl.Fields))
	}
	wantChannels := map[string]int{"r": 255, "g": 128, "b": 64, "a": 255}
	for _, f := range sl.Fields {
		w, ok := wantChannels[f.Name]
		if !ok {
			t.Errorf("unexpected field %q", f.Name)
			continue
		}
		lit, ok := f.Value.(*ir.Literal)
		if !ok || lit.Type.Kind != ir.TypeInt {
			t.Errorf("field %s: value = %T, want int Literal", f.Name, f.Value)
			continue
		}
		n := 0
		fmt.Sscanf(lit.Raw, "%d", &n)
		if n != w {
			t.Errorf("field %s = %d, want %d", f.Name, n, w)
		}
	}
}

// colorChannels checks a `const C color = <literal>` and returns its r/g/b/a.
func colorChannels(t *testing.T, literal string) map[string]int {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte("const C color = "+literal))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check %s: %s", literal, d.Error())
		}
	}
	sl := unwrapConv(pkg.Consts[0].Init).(*ir.StructLit)
	got := map[string]int{}
	for _, f := range sl.Fields {
		n := 0
		fmt.Sscanf(f.Value.(*ir.Literal).Raw, "%d", &n)
		got[f.Name] = n
	}
	return got
}

// TestHexColorShortForms pins the CSS-style 3- and 4-digit short forms, which
// double each nibble (#fff -> #ffffff, #f00a -> #ff0000aa). These reach the
// color path now that the lexer emits a single #-token regardless of length.
func TestHexColorShortForms(t *testing.T) {
	cases := map[string]map[string]int{
		"#fff":  {"r": 255, "g": 255, "b": 255, "a": 255},
		"#abc":  {"r": 0xaa, "g": 0xbb, "b": 0xcc, "a": 255},
		"#f00a": {"r": 255, "g": 0, "b": 0, "a": 0xaa},
	}
	for lit, want := range cases {
		got := colorChannels(t, lit)
		for ch, w := range want {
			if got[ch] != w {
				t.Errorf("%s channel %s = %d, want %d", lit, ch, got[ch], w)
			}
		}
	}
}

func TestHexColorEightDigitIncludesAlpha(t *testing.T) {
	src := `const C color = #11223344`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	var found *ir.Var
	for _, v := range pkg.Consts {
		if v.Name == "C" {
			found = v
			break
		}
	}
	sl := unwrapConv(found.Init).(*ir.StructLit)
	want := map[string]int{"r": 0x11, "g": 0x22, "b": 0x33, "a": 0x44}
	for _, f := range sl.Fields {
		lit := f.Value.(*ir.Literal)
		n := 0
		fmt.Sscanf(lit.Raw, "%d", &n)
		if n != want[f.Name] {
			t.Errorf("field %s = %d, want %d", f.Name, n, want[f.Name])
		}
	}
}
