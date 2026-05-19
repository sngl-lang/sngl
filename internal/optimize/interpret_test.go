package optimize

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestIRFromValue_Primitives(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want string // Raw of the resulting Literal
	}{
		{"int", 42, "42"},
		{"float", 3.14, "3.14"},
		{"string", "hi", "hi"},
		{"bool true", true, "true"},
		{"bool false", false, "false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := irFromValue(tt.val, nil)
			lit, ok := got.(*ir.Literal)
			if !ok {
				t.Fatalf("got %T, want *ir.Literal", got)
			}
			if lit.Raw != tt.want {
				t.Errorf("Raw = %q, want %q", lit.Raw, tt.want)
			}
		})
	}
}

func TestIRFromValue_Map(t *testing.T) {
	val := map[string]any{
		"r": 255,
		"g": 128,
		"b": 64,
		"a": 255,
	}
	got := irFromValue(val, nil)
	sl, ok := got.(*ir.StructLit)
	if !ok {
		t.Fatalf("got %T, want *ir.StructLit", got)
	}
	if len(sl.Fields) != 4 {
		t.Fatalf("Fields count = %d, want 4", len(sl.Fields))
	}
	for _, f := range sl.Fields {
		if f.Name == "r" {
			lit := f.Value.(*ir.Literal)
			if lit.Raw != "255" {
				t.Errorf("r = %q", lit.Raw)
			}
		}
	}
}

func TestIRFromValue_Slice(t *testing.T) {
	val := []any{1, 2, 3}
	got := irFromValue(val, nil)
	ll, ok := got.(*ir.ListLit)
	if !ok {
		t.Fatalf("got %T, want *ir.ListLit", got)
	}
	if len(ll.Elems) != 3 {
		t.Fatalf("Elems = %d, want 3", len(ll.Elems))
	}
}

func TestIRFromValue_Nil(t *testing.T) {
	got := irFromValue(nil, nil)
	lit, ok := got.(*ir.Literal)
	if !ok || lit.Raw != "null" {
		t.Errorf("got %v, want null Literal", got)
	}
}

func TestInterpretFunc_SimplePureFunc(t *testing.T) {
	src := `
func double(x int) => x * 2
const C = double(21)
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})

	var doubleFn *ir.Func
	for _, f := range pkg.Funcs {
		if f.Name == "double" {
			doubleFn = f
			break
		}
	}
	if doubleFn == nil {
		t.Fatal("func double not found")
	}

	ctx := newEvalCtxForTest(pkg)
	val, ok := interpretFunc(doubleFn, []any{21}, ctx, 0)
	if !ok {
		t.Fatalf("interpretFunc returned ok=false")
	}
	if val != 42 {
		t.Errorf("got %v, want 42", val)
	}
}

// newEvalCtxForTest is a small helper that builds a minimal evalCtx
// for testing the interpreter adapter in isolation.
func newEvalCtxForTest(pkg *ir.Package) *evalCtx {
	return &evalCtx{
		pkg:    pkg,
		values: map[ir.Symbol]any{},
	}
}

func TestInterpretFunc_MutationIsolation(t *testing.T) {
	// withR returns a pure copy of c with r overridden. The interpreter's
	// deep-copy of args ensures the input map for A isn't aliased to the
	// callee's local c (which gets a new map via the struct literal anyway).
	src := `
func withR(c color, r int) => color{r=r, g=c.g, b=c.b, a=c.a}
const A color = color{r=10, g=20, b=30, a=255}
const B color = withR(A, 99)
component main {
    text(value=string(A.r))
    text(value=string(B.r))
}
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := Optimize(pkg, &Config{}); err != nil {
		t.Fatalf("optimize: %v", err)
	}

	var a, b *ir.Var
	for _, v := range pkg.Consts {
		switch v.Name {
		case "A":
			a = v
		case "B":
			b = v
		}
	}
	if a == nil || b == nil {
		t.Fatal("A or B not found")
	}

	rOf := func(v *ir.Var) string {
		init := v.Init
		if conv, ok := init.(*ir.Conversion); ok {
			init = conv.Operand
		}
		sl, ok := init.(*ir.StructLit)
		if !ok {
			return ""
		}
		for _, f := range sl.Fields {
			if f.Name == "r" {
				if lit, ok := f.Value.(*ir.Literal); ok {
					return lit.Raw
				}
			}
		}
		return ""
	}
	// Once T7 lands, remove the Skip at the top of this test.
	if rOf(a) != "10" {
		t.Errorf("A.r = %q after optimize, want 10 (mutation leaked)", rOf(a))
	}
	if rOf(b) != "99" {
		t.Errorf("B.r = %q after optimize, want 99", rOf(b))
	}
}

func TestOptimize_FoldsColorLighten(t *testing.T) {
	t.Skip("TODO: interpret block-body stdlib functions; See #T8-BLOCKED")
	src := `
const C color = color.lighten(#ff0000, 0.5)
component main {
	text(value=string(C.r))
}
`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}
	if err := Optimize(pkg, &Config{}); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	var c *ir.Var
	for _, v := range pkg.Consts {
		if v.Name == "C" {
			c = v
			break
		}
	}
	if c == nil {
		t.Fatalf("C not found; pkg has %d consts", len(pkg.Consts))
	}
	init := unwrapConversion(c.Init)
	sl, ok := init.(*ir.StructLit)
	if !ok {
		t.Fatalf("Init = %T (was %T), want *ir.StructLit", init, c.Init)
	}
	// color.lighten(red, 0.5) per the SNGL formula in lib/functions.sngl:
	// r,g,b each → min(255, c + int(float(255-c) * pct))
	// red = {255, 0, 0, 255}: r stays 255; g/b → 0 + int(127.5) = 127.
	want := map[string]string{"r": "255", "g": "127", "b": "127", "a": "255"}
	for _, f := range sl.Fields {
		lit, ok := f.Value.(*ir.Literal)
		if !ok {
			t.Errorf("field %s value = %T", f.Name, f.Value)
			continue
		}
		if lit.Raw != want[f.Name] {
			t.Errorf("field %s = %q, want %q", f.Name, lit.Raw, want[f.Name])
		}
	}
}

func TestOptimize_FoldsComposedColorExpression(t *testing.T) {
	// This test demonstrates inlining (not interpretation) of color.opacity.
	// color.opacity is an expression body, so it inlines to a StructLit with
	// Select expressions. To properly test composition, use functions with
	// non-trivial block bodies that exercise the interpreter.
	t.Skip("TODO: use non-inlinable composed functions; See #T8-BLOCKED")
	src := `
const C color = color.opacity(color.lighten(#ff0000, 0.5), 128)
component main {
	text(value=string(C.r))
}
`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := Optimize(pkg, &Config{}); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	var c *ir.Var
	for _, v := range pkg.Consts {
		if v.Name == "C" {
			c = v
			break
		}
	}
	if c == nil {
		t.Fatal("C not found")
	}
	init := unwrapConversion(c.Init)
	sl, ok := init.(*ir.StructLit)
	if !ok {
		t.Fatalf("Init = %T", init)
	}
	want := map[string]string{"r": "255", "g": "127", "b": "127", "a": "128"}
	for _, f := range sl.Fields {
		lit, ok := f.Value.(*ir.Literal)
		if !ok {
			t.Errorf("field %s value = %T", f.Name, f.Value)
			continue
		}
		if lit.Raw != want[f.Name] {
			t.Errorf("field %s = %q, want %q", f.Name, lit.Raw, want[f.Name])
		}
	}
}

func TestOptimize_FoldsUserDefinedColorHelper(t *testing.T) {
	t.Skip("TODO: interpret user-defined functions that call block-body stdlib; See #T8-BLOCKED")
	src := `
func tint(c color, n float) color => color.lighten(c, n)
const C color = tint(#ff0000, 0.5)
component main {
	text(value=string(C.r))
}
`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := Optimize(pkg, &Config{}); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	var c *ir.Var
	for _, v := range pkg.Consts {
		if v.Name == "C" {
			c = v
			break
		}
	}
	if c == nil {
		t.Fatalf("C not found; pkg has %d consts", len(pkg.Consts))
	}
	init := unwrapConversion(c.Init)
	sl, ok := init.(*ir.StructLit)
	if !ok {
		t.Fatalf("Init = %T (was %T), want *ir.StructLit", init, c.Init)
	}
	want := map[string]string{"r": "255", "g": "127", "b": "127", "a": "255"}
	for _, f := range sl.Fields {
		lit, ok := f.Value.(*ir.Literal)
		if !ok {
			t.Errorf("field %s value = %T", f.Name, f.Value)
			continue
		}
		if lit.Raw != want[f.Name] {
			t.Errorf("field %s = %q, want %q", f.Name, lit.Raw, want[f.Name])
		}
	}
}

// unwrapConversion strips a top-level *ir.Conversion wrapper if present.
// The optimizer may wrap a struct lit in a no-op conversion when the
// declared type differs in form from the produced shape.
func unwrapConversion(e ir.Expr) ir.Expr {
	if conv, ok := e.(*ir.Conversion); ok {
		return conv.Operand
	}
	return e
}
