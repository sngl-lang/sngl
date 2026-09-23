package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

var passComputed = pass{
	name:    "NoComputed",
	enabled: func(c Features) bool { return !c.Computed },
	apply:   lowerComputed,
}

// lowerComputed inlines every expression-body computed func at its call
// sites. The inlined funcs are then removed from their owning collection.
//
// A computed func is zero-param, non-test, and has an AST expression body
// (matching codegen.IsComputed). The checker stores the body as a single
// Return statement: Func.Block = []ir.Stmt{*ir.Return{Value: expr}}.
//
// Multi-statement computeds are not inlined.
func lowerComputed(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}

	bodies := make(map[*ir.Func]ir.Expr)
	collectComputed(pkg.Funcs, bodies)
	for _, comp := range pkg.Components {
		collectComputed(comp.Funcs, bodies)
	}

	if len(bodies) == 0 {
		return nil
	}

	rewrite := func(e ir.Expr) ir.Expr {
		return inlineComputedExpr(e, bodies)
	}
	walkPackage(pkg, walkFuncs{
		expr:  rewrite,
		stmts: func(stmts []ir.Stmt) []ir.Stmt { return rewriteStmtExprs(stmts, rewrite) },
	})

	pkg.Funcs = filterFuncs(pkg.Funcs, bodies)
	for _, comp := range pkg.Components {
		comp.Funcs = filterFuncs(comp.Funcs, bodies)
	}
	return nil
}

func isComputed(f *ir.Func) bool {
	if f.AST == nil || f.AST.Body == nil || f.IsTest {
		return false
	}
	n := len(f.Params)
	if n > 0 && f.Receiver != "" && f.Params[0].Receiver {
		n--
	}
	return n == 0
}

func collectComputed(funcs []*ir.Func, out map[*ir.Func]ir.Expr) {
	for _, f := range funcs {
		if !isComputed(f) {
			continue
		}
		if len(f.Block) != 1 {
			continue
		}
		ret, ok := f.Block[0].(*ir.Return)
		if !ok || ret.Value == nil {
			continue
		}
		out[f] = ret.Value
	}
}

func filterFuncs(funcs []*ir.Func, removed map[*ir.Func]ir.Expr) []*ir.Func {
	out := funcs[:0]
	for _, f := range funcs {
		if _, drop := removed[f]; drop {
			continue
		}
		out = append(out, f)
	}
	return out
}

// inlineComputedExpr replaces every Call to a computed func with the
// computed's body expression. Recurses so nested calls collapse in one pass.
func inlineComputedExpr(e ir.Expr, bodies map[*ir.Func]ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *ir.Call:
		if x.Func != nil {
			if body, ok := bodies[x.Func]; ok {
				return inlineComputedExpr(body, bodies)
			}
		}
		if x.Receiver != nil {
			x.Receiver = inlineComputedExpr(x.Receiver, bodies)
		}
		for i := range x.Args {
			x.Args[i].Value = inlineComputedExpr(x.Args[i].Value, bodies)
		}
		return x
	case *ir.Binary:
		x.Left = inlineComputedExpr(x.Left, bodies)
		x.Right = inlineComputedExpr(x.Right, bodies)
		return x
	case *ir.Unary:
		x.Operand = inlineComputedExpr(x.Operand, bodies)
		return x
	case *ir.Ternary:
		x.Cond = inlineComputedExpr(x.Cond, bodies)
		x.Then = inlineComputedExpr(x.Then, bodies)
		x.Else = inlineComputedExpr(x.Else, bodies)
		return x
	case *ir.Conversion:
		x.Operand = inlineComputedExpr(x.Operand, bodies)
		return x
	case *ir.Select:
		x.Operand = inlineComputedExpr(x.Operand, bodies)
		return x
	case *ir.Index:
		x.Operand = inlineComputedExpr(x.Operand, bodies)
		x.Idx = inlineComputedExpr(x.Idx, bodies)
		return x
	case *ir.ListLit:
		for i := range x.Elems {
			x.Elems[i] = inlineComputedExpr(x.Elems[i], bodies)
		}
		return x
	case *ir.StructLit:
		for i := range x.Fields {
			if x.Fields[i].Value != nil {
				x.Fields[i].Value = inlineComputedExpr(x.Fields[i].Value, bodies)
			}
		}
		return x
	case *ir.Spread:
		x.Operand = inlineComputedExpr(x.Operand, bodies)
		return x
	case *ir.Closure:
		if x.State != nil {
			for i := range x.State.Fields {
				if x.State.Fields[i].Value != nil {
					x.State.Fields[i].Value = inlineComputedExpr(x.State.Fields[i].Value, bodies)
				}
			}
		}
		if x.Func != nil {
			rewrite := func(e ir.Expr) ir.Expr { return inlineComputedExpr(e, bodies) }
			x.Func.Block = rewriteStmtExprs(x.Func.Block, rewrite)
		}
		return x
	case *ir.Lambda:
		if x.Func != nil {
			rewrite := func(e ir.Expr) ir.Expr { return inlineComputedExpr(e, bodies) }
			x.Func.Block = rewriteStmtExprs(x.Func.Block, rewrite)
		}
		return x
	case *ir.MapLitIR:
		for i := range x.Entries {
			x.Entries[i].Key = inlineComputedExpr(x.Entries[i].Key, bodies)
			x.Entries[i].Value = inlineComputedExpr(x.Entries[i].Value, bodies)
		}
		return x
	case *ir.Literal, *ir.Ident, *ir.ContextRead:
		// Terminal — no nested Calls to inline.
		return x
	default:
		panic(fmt.Sprintf("inlineComputedExpr: unhandled %T", x))
	}
}
