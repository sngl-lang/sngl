package optimize

import (
	"testing"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// A literal beside an operand that did not fold: one that cannot decide the
// answer leaves the operand, one that decides it is the answer where what it
// replaces is never read or reads nothing that does anything.
func TestSettledLogic(t *testing.T) {
	x := &ir.Ident{Name: "x", Type: ir.TypBool}
	tru := &ir.Literal{Type: ir.TypBool, Value: "true"}
	fls := &ir.Literal{Type: ir.TypBool, Value: "false"}
	effect := &ir.Call{Type: ir.TypBool, Func: &ir.Func{Name: "roll", Purity: ir.PurityReadonly}}

	for _, tc := range []struct {
		name        string
		op          ast.BinaryOp
		left, right ir.Expr
		want        ir.Expr
	}{
		{"x && true", ast.BinAnd, x, tru, x},
		{"true && x", ast.BinAnd, tru, x, x},
		{"false && x", ast.BinAnd, fls, x, fls},
		{"x && false", ast.BinAnd, x, fls, fls},
		{"x || false", ast.BinOr, x, fls, x},
		{"false || x", ast.BinOr, fls, x, x},
		{"true || x", ast.BinOr, tru, x, tru},
		{"x || true", ast.BinOr, x, tru, tru},
		{"false && roll()", ast.BinAnd, fls, effect, fls},
		{"roll() && false", ast.BinAnd, effect, fls, nil},
		{"roll() || true", ast.BinOr, effect, tru, nil},
		{"x == true", ast.BinEq, x, tru, nil},
	} {
		got := settledLogic(&ir.Binary{Type: ir.TypBool, Op: tc.op, Left: tc.left, Right: tc.right})
		if tc.want == nil {
			if got != nil {
				t.Errorf("%s: settled to %v, want left standing", tc.name, got)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("%s: settled to %v, want %v", tc.name, got, tc.want)
		}
	}
}
