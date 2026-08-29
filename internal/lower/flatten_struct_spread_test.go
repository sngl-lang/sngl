package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func intLit(n string) *ir.Literal { return &ir.Literal{Type: ir.TypInt, Value: n} }

// names returns the field names of a flat struct literal in order.
func names(sl *ir.StructLit) []string {
	var out []string
	for _, f := range sl.Fields {
		out = append(out, f.Name)
	}
	return out
}

func TestFlattenStructLitLiteralSpread(t *testing.T) {
	// {a=1, ...{a=0, b=2}}  →  {a=0, b=2}  (literal spread splices written fields; last wins)
	in := &ir.StructLit{Fields: []ir.FieldInit{
		{Name: "a", Value: intLit("1")},
		{Spread: true, Value: &ir.StructLit{Fields: []ir.FieldInit{
			{Name: "a", Value: intLit("0")},
			{Name: "b", Value: intLit("2")},
		}}},
	}}
	out := flattenSpreadExprCtx(in, &ir.Package{}).(*ir.StructLit)
	got := names(out)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("field names: got %v want [a b]", got)
	}
	if a := out.Fields[0]; a.Spread || a.Value.(*ir.Literal).Value != "0" {
		t.Fatalf("a should be 0 (last write wins), got %+v", a)
	}
}

func TestFlattenStructLitExplicitZeroWins(t *testing.T) {
	// {...{a=1}, a=0}  →  {a=0}  (explicit, last)
	in := &ir.StructLit{Fields: []ir.FieldInit{
		{Spread: true, Value: &ir.StructLit{Fields: []ir.FieldInit{{Name: "a", Value: intLit("1")}}}},
		{Name: "a", Value: intLit("0")},
	}}
	out := flattenSpreadExprCtx(in, &ir.Package{}).(*ir.StructLit)
	if len(out.Fields) != 1 || out.Fields[0].Value.(*ir.Literal).Value != "0" {
		t.Fatalf("want {a=0}, got %+v", out.Fields)
	}
}

func structType(name string) *ir.Type {
	sd := &ir.StructDef{Name: name, Fields: []*ir.StructField{
		{Name: "a", Type: ir.TypInt}, {Name: "b", Type: ir.TypInt},
	}}
	return &ir.Type{Kind: ir.TypeStruct, Decl: sd}
}

func TestFlattenOpaqueSpreadBuildsMergeChain(t *testing.T) {
	st := structType("Cfg")
	pkg := &ir.Package{}
	// {a=1, ...op, b=2}  →  __merge_Cfg(__merge_Cfg(Cfg{a:1}, op), Cfg{b:2})
	in := &ir.StructLit{Type: st, Def: st.Decl.(*ir.StructDef), Fields: []ir.FieldInit{
		{Name: "a", Value: intLit("1")},
		{Spread: true, Value: &ir.Ident{Name: "op", Type: st}},
		{Name: "b", Value: intLit("2")},
	}}
	out := flattenStructLitCtx(in, pkg)
	call, ok := out.(*ir.Call)
	if !ok || call.Func == nil || call.Func.Name != "__merge_Cfg" {
		t.Fatalf("outer expr must be a __merge_Cfg call, got %T", out)
	}
	inner, ok := call.Args[0].Value.(*ir.Call)
	if !ok || inner.Func.Name != "__merge_Cfg" {
		t.Fatalf("first arg must be inner __merge_Cfg call, got %T", call.Args[0].Value)
	}
	if len(pkg.MergeStructs) != 1 || pkg.MergeStructs[0].Name != "Cfg" {
		t.Fatalf("pkg.MergeStructs must record Cfg once, got %+v", pkg.MergeStructs)
	}
}
