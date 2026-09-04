package lower

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// rebuildDiffers is the expression that is true when a and b are not the same
// key: what a keyed lifetime asks of two values of its `on`, and a built
// instance of two values of a #[construct] prop.
//
// Emitted from the same walk ir.RebuildComparable answers with, which is the
// point of it being a walk. A single `a != b` cannot be that expression,
// because it does not mean the same thing on every target: Go compares two
// structs field by field, JS compares two objects by identity, so a struct key
// held one lifetime on the Go build and tore down and remounted on every settle
// of the JS one. Comparing the fields makes the emitted comparison structural
// on all three languages without any of them being told about structs.
//
// Deliberately not a language-level change to `==`: a user-written `a == b` on
// two structs is still whatever the target says, which is a separate hole. This
// is the comparison the compiler writes for itself, and it is written where
// every backend already agrees.
//
// The walk terminates because ir.RebuildComparable gates every call site, and a
// struct that contains itself is not comparable there. Reaching one here would
// be a caller that skipped the gate.
func rebuildDiffers(a, b ir.Expr, t *ir.Type) ir.Expr {
	return rebuildCompare(a, b, t, false)
}

// rebuildSame is rebuildDiffers' opposite, written as its own walk rather than
// a `!` around it: the two are read in opposite branches -- a settle asks what
// changed, an instantiation site asks what it may reuse -- and a negated
// disjunction is a worse thing to find in generated code than the conjunction
// it stands for.
func rebuildSame(a, b ir.Expr, t *ir.Type) ir.Expr {
	return rebuildCompare(a, b, t, true)
}

func rebuildCompare(a, b ir.Expr, t *ir.Type, same bool) ir.Expr {
	op, join := ast.BinNeq, ast.BinOr
	empty := "false"
	if same {
		op, join = ast.BinEq, ast.BinAnd
		empty = "true"
	}
	if t != nil && t.Kind == ir.TypeStruct {
		if sd, _ := t.Decl.(*ir.StructDef); sd != nil && !(sd.Builtin != ir.BuiltinNone && len(sd.Fields) == 0) {
			var out ir.Expr
			for _, f := range sd.Fields {
				if f == nil {
					continue
				}
				sel := func(e ir.Expr) ir.Expr {
					return &ir.Select{Type: f.Type, Operand: deepCloneExpr(e), Field: f.Name}
				}
				cmp := rebuildCompare(sel(a), sel(b), f.Type, same)
				if out == nil {
					out = cmp
					continue
				}
				out = &ir.Binary{Type: ir.TypBool, Op: join, Left: out, Right: cmp}
			}
			if out == nil {
				// No fields, so there is nothing two values can differ in and
				// the bracket never restarts. `effect<T = struct {}>`'s
				// default is such a struct, though a bracket written with no
				// `on` at all does not arrive here -- it has no key to compare
				// and lowers over a bool instead.
				return &ir.Literal{Type: ir.TypBool, Value: empty}
			}
			return out
		}
	}
	return &ir.Binary{Type: ir.TypBool, Op: op, Left: deepCloneExpr(a), Right: deepCloneExpr(b)}
}
