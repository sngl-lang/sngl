package lower

import (
	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// passEnumReads writes the two things a program does with a payload enum's
// selector read, `r.ok`, as what they ask (ir/enums.go): a null test is
// whether r holds the member, and the read a null test narrowed is the
// payload. The checker keeps the option read so that narrowing reads it as it
// reads any option; what is left of it for a target is only a read whose
// option is used as one.
var passEnumReads = pass{
	name:    "EnumReads",
	enabled: func(Features) bool { return true },
	apply:   lowerEnumReads,
}

func lowerEnumReads(pkg *ir.Package, _ Features, _ Options) error {
	return ir.RewriteExprs(pkg, func(e ir.Expr) (ir.Expr, error) {
		switch x := e.(type) {
		case *ir.Binary:
			if x.Op != ast.BinEq && x.Op != ast.BinNeq {
				return e, nil
			}
			read, other := x.Left, x.Right
			if !isNullLit(other) {
				read, other = x.Right, x.Left
			}
			sel, ok := enumMemberSelect(read)
			if !ok || !isNullLit(other) {
				return e, nil
			}
			var test ir.Expr = ir.EnumIs(sel.Operand, sel.Field)
			if x.Op == ast.BinEq {
				test = &ir.Unary{Type: ir.TypBool, Op: ast.UnaryNot, Operand: test}
			}
			return test, nil
		case *ir.Conversion:
			if !ir.IsOptionUnwrap(x) {
				return e, nil
			}
			if sel, ok := enumMemberSelect(x.Operand); ok {
				return ir.EnumPayloadRead(sel.Operand, sel.Field), nil
			}
		}
		return e, nil
	})
}

// enumMemberSelect is e as a payload enum's selector read.
func enumMemberSelect(e ir.Expr) (*ir.Select, bool) {
	sel, ok := e.(*ir.Select)
	if !ok || ir.PayloadEnum(sel.Operand.ExprType()) == nil {
		return nil, false
	}
	return sel, true
}
