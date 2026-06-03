package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func intLit(n string) *ir.Literal { return &ir.Literal{Type: ir.TypInt, Raw: n} }

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
	out := flattenSpreadExpr(in).(*ir.StructLit)
	got := names(out)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("field names: got %v want [a b]", got)
	}
	if a := out.Fields[0]; a.Spread || a.Value.(*ir.Literal).Raw != "0" {
		t.Fatalf("a should be 0 (last write wins), got %+v", a)
	}
}

func TestFlattenStructLitExplicitZeroWins(t *testing.T) {
	// {...{a=1}, a=0}  →  {a=0}  (explicit, last)
	in := &ir.StructLit{Fields: []ir.FieldInit{
		{Spread: true, Value: &ir.StructLit{Fields: []ir.FieldInit{{Name: "a", Value: intLit("1")}}}},
		{Name: "a", Value: intLit("0")},
	}}
	out := flattenSpreadExpr(in).(*ir.StructLit)
	if len(out.Fields) != 1 || out.Fields[0].Value.(*ir.Literal).Raw != "0" {
		t.Fatalf("want {a=0}, got %+v", out.Fields)
	}
}

func TestFlattenStructLitOpaqueLeftUntouched(t *testing.T) {
	// {...someVar} with an opaque operand stays a spread (Phase 2 territory).
	in := &ir.StructLit{Fields: []ir.FieldInit{
		{Spread: true, Value: &ir.Ident{Name: "someVar"}},
	}}
	out := flattenSpreadExpr(in).(*ir.StructLit)
	if len(out.Fields) != 1 || !out.Fields[0].Spread {
		t.Fatalf("opaque spread must be left intact, got %+v", out.Fields)
	}
}
