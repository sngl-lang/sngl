package optimize

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/interp"
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
			if lit.Value != tt.want {
				t.Errorf("Raw = %q, want %q", lit.Value, tt.want)
			}
		})
	}
}

// A struct value comes back as a struct literal carrying the declaration it
// was built from, and its fields in the order they were checked in — neither
// of which the expected type has to supply, and neither of which a bare map
// could have held.
func TestIRFromValue_Struct(t *testing.T) {
	def := &ir.StructDef{Name: "color", Fields: []*ir.StructField{
		{Name: "r"}, {Name: "g"}, {Name: "b"}, {Name: "a"},
	}}
	val := interp.NewStruct(def, nil)
	for _, f := range []struct {
		name string
		v    int
	}{{"r", 255}, {"g", 128}, {"b", 64}, {"a", 255}} {
		val.Set(f.name, f.v)
	}
	got := irFromValue(val, nil)
	sl, ok := got.(*ir.StructLit)
	if !ok {
		t.Fatalf("got %T, want *ir.StructLit", got)
	}
	if sl.Def != def {
		t.Errorf("Def = %v, want the declaration the value carried", sl.Def)
	}
	var names []string
	for _, f := range sl.Fields {
		names = append(names, f.Name)
	}
	if len(names) != 4 || names[0] != "r" || names[1] != "g" || names[2] != "b" || names[3] != "a" {
		t.Errorf("fields = %v, want them in the order they were set", names)
	}
	if lit, ok := sl.Fields[0].Value.(*ir.Literal); !ok || lit.Value != "255" {
		t.Errorf("r = %v", sl.Fields[0].Value)
	}
}

// A map value is a map literal. It used to come back as a struct literal,
// which is a different type with different codegen; nothing told the two apart
// while both were a Go map.
func TestIRFromValue_Map(t *testing.T) {
	got := irFromValue(map[string]any{"b": 2, "a": 1}, nil)
	ml, ok := got.(*ir.MapLitIR)
	if !ok {
		t.Fatalf("got %T, want *ir.MapLitIR", got)
	}
	if len(ml.Entries) != 2 {
		t.Fatalf("Entries count = %d, want 2", len(ml.Entries))
	}
	if k, ok := ml.Entries[0].Key.(*ir.Literal); !ok || k.Value != "a" {
		t.Errorf("first key = %v, want the smallest — a map has no order of its own", ml.Entries[0].Key)
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
	if !ok || lit.Value != "null" {
		t.Errorf("got %v, want null Literal", got)
	}
}

func TestInterpretFunc_SimplePureFunc(t *testing.T) {
	src := `
func double(x int) => x * 2
const C = double(21)
`
	doc, err := parser.Parse("t.sngl", []byte(withStdSrc(src)))
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
	// Post-fold A and B are inlined into main and shaken from pkg.Consts,
	// so verify the folded r-channel values directly in the component body.
	src := `
func withR(c color, r int) => color{r=r, g=c.g, b=c.b, a=c.a}
const A color = color{r=10, g=20, b=30, a=255}
const B color = withR(A, 99)
component main node {
    text(value=string(A.r))
    text(value=string(B.r))
}
`
	doc, err := parser.Parse("t.sngl", []byte(withStdSrc(src)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := Optimize(pkg, &Config{}); err != nil {
		t.Fatalf("optimize: %v", err)
	}

	vals := textValuesInMain(t, pkg)
	want := []string{"10", "99"}
	if len(vals) != len(want) {
		t.Fatalf("got %d text values, want %d: %v", len(vals), len(want), vals)
	}
	for i, w := range want {
		if vals[i] != w {
			t.Errorf("text[%d] = %q, want %q (mutation leaked or fold failed)", i, vals[i], w)
		}
	}
}

func TestOptimize_FoldsColorLighten(t *testing.T) {
	// color.lighten(red, 0.5) per the SNGL formula in lib/functions.sngl:
	// r,g,b each → min(255, c + int(float(255-c) * pct))
	// red = {255, 0, 0, 255}: r stays 255; g/b → 0 + int(127.5) = 127.
	src := `
const C color = color.lighten(#ff0000, 0.5)
component main node {
	text(value=string(C.r))
	text(value=string(C.g))
	text(value=string(C.b))
	text(value=string(C.a))
}
`
	doc, _ := parser.Parse("t.sngl", []byte(withStdSrc(src)))
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}
	if err := Optimize(pkg, &Config{}); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	vals := textValuesInMain(t, pkg)
	want := []string{"255", "127", "127", "255"}
	if len(vals) != len(want) {
		t.Fatalf("got %d text values, want %d: %v", len(vals), len(want), vals)
	}
	for i, w := range want {
		if vals[i] != w {
			t.Errorf("text[%d] = %q, want %q (fold failed)", i, vals[i], w)
		}
	}
}

func TestOptimize_FoldsComposedColorExpression(t *testing.T) {
	// Composes the block-body interpreted color.lighten with the
	// expression-body inlined color.opacity. Verifies the alpha override
	// flows through after lighten's r/g/b computation.
	src := `
const C color = color.opacity(color.lighten(#ff0000, 0.5), 128)
component main node {
	text(value=string(C.r))
	text(value=string(C.g))
	text(value=string(C.b))
	text(value=string(C.a))
}
`
	doc, _ := parser.Parse("t.sngl", []byte(withStdSrc(src)))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := Optimize(pkg, &Config{}); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	vals := textValuesInMain(t, pkg)
	want := []string{"255", "127", "127", "128"}
	if len(vals) != len(want) {
		t.Fatalf("got %d text values, want %d: %v", len(vals), len(want), vals)
	}
	for i, w := range want {
		if vals[i] != w {
			t.Errorf("text[%d] = %q, want %q (compose fold failed)", i, vals[i], w)
		}
	}
}

func TestOptimize_FoldsUserDefinedColorHelper(t *testing.T) {
	// A user-defined expression-body helper that delegates to the
	// block-body stdlib function. Inlining + interpretation must compose.
	src := `
func tint(c color, n float) => color.lighten(c, n)
const C color = tint(#ff0000, 0.5)
component main node {
	text(value=string(C.r))
	text(value=string(C.g))
	text(value=string(C.b))
	text(value=string(C.a))
}
`
	doc, _ := parser.Parse("t.sngl", []byte(withStdSrc(src)))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := Optimize(pkg, &Config{}); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	vals := textValuesInMain(t, pkg)
	want := []string{"255", "127", "127", "255"}
	if len(vals) != len(want) {
		t.Fatalf("got %d text values, want %d: %v", len(vals), len(want), vals)
	}
	for i, w := range want {
		if vals[i] != w {
			t.Errorf("text[%d] = %q, want %q (user-helper fold failed)", i, vals[i], w)
		}
	}
}

// textValuesInMain returns the folded `value` prop of each text() node in
// the package's `main` component, in declaration order. Fails the test if
// `main` is missing or any value is not a folded *ir.Literal.
func textValuesInMain(t *testing.T, pkg *ir.Package) []string {
	t.Helper()
	var main *ir.Component
	for _, c := range pkg.Components {
		if c.Name == "main" {
			main = c
			break
		}
	}
	if main == nil {
		t.Fatal("component main not found")
	}
	var vals []string
	for _, s := range main.Body {
		ni, ok := s.(*ir.NodeInst)
		if !ok {
			continue
		}
		for _, p := range ni.Props {
			if p.Name != "value" {
				continue
			}
			lit, ok := p.Value.(*ir.Literal)
			if !ok {
				t.Fatalf("text.value not folded to *ir.Literal, got %T", p.Value)
			}
			vals = append(vals, lit.Value)
		}
	}
	return vals
}
