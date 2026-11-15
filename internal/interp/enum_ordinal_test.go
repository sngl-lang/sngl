package interp

import (
	"testing"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// TestEnumZeroComparesWithItsOrdinal: a fold after a lowering that makes ints
// of an enum's members reads a struct's unset enum field as its first
// member's name and compares it with the int the lowering wrote, so on C a
// default Style's `borderStyle != 0` was true and a clickable card's frame
// lost the border every other target draws.
func TestEnumZeroComparesWithItsOrdinal(t *testing.T) {
	ed := &ir.EnumDef{Name: "BorderStyle", Members: []*ir.EnumMember{{Name: "unset"}, {Name: "none"}}}
	typ := &ir.Type{Kind: ir.TypeEnum, Decl: ed}
	sd := &ir.StructDef{Name: "Style", Fields: []*ir.StructField{{Name: "borderStyle", Type: typ}}}
	st := sd.SymType()
	field := &ir.Select{Operand: &ir.StructLit{Type: st, Def: sd}, Field: "borderStyle", Type: typ}
	for _, c := range []struct {
		op   ast.BinaryOp
		ord  string
		want bool
	}{
		{ast.BinNeq, "0", false},
		{ast.BinEq, "0", true},
		{ast.BinEq, "1", false},
	} {
		e := &ir.Binary{Op: c.op, Left: field, Right: &ir.Literal{Type: ir.TypInt, Value: c.ord}, Type: ir.TypBool}
		got, err := NewEnv().Eval(e)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("Style{}.borderStyle %v %s = %v, want %v", c.op, c.ord, got, c.want)
		}
	}
}
