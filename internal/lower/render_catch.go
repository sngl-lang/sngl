package lower

import (
	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// passRenderCatch catches a raise from a view's own expression where it is
// evaluated -- at the first draw, in a render slot, in the update a write
// made -- and hands it to the boundary around what rendered (ir.ErrorRender):
// that handler runs, the call is its type's zero value, and the render goes
// on. Each such call becomes a closure called in place,
//
//	func() T { var v T; v = zero; if true { v = call } catch H; return v }()
//
// since a raise there belongs to no event handler: the write that asked for
// the render may sit under another boundary, or under none, as a timer's
// does. Every target renders the closure as the expression it already is,
// so a raise lands the same on html, fyne, gtk4, bubbletea and android,
// whichever of them renders inside the handler and whichever after it.
//
// After passErrorCatch would be too late and before passReactivity too
// early: a closure is opaque to what reads a render's dependencies.
//
// Always on: a raise is a raise on every target.
var passRenderCatch = pass{
	name:    "RenderCatch",
	enabled: func(Features) bool { return true },
	apply:   lowerRenderCatch,
}

func lowerRenderCatch(pkg *ir.Package, _ Features, _ Options) error {
	return ir.RewriteExprs(pkg, func(e ir.Expr) (ir.Expr, error) {
		c, ok := e.(*ir.Call)
		if !ok || c.ErrorMode != ir.ErrorRender || c.ResolvedHandler == nil {
			return e, nil
		}
		return caughtRender(c), nil
	})
}

// caughtRender is c called inside a closure catching its raise.
func caughtRender(c *ir.Call) ir.Expr {
	h := c.ResolvedHandler
	inner := *c
	inner.ErrorMode, inner.ResolvedHandler = ir.ErrorBubble, nil
	typ := c.ExprType()
	// Assigned rather than returned from inside the catch: a catch block's
	// body is a function of its own on Go, where a return leaves only that.
	v := &ir.Var{Name: "__caught", Type: typ, Synthesized: true}
	ref := func() ir.Expr { return &ir.Ident{Name: v.Name, Sym: v, Type: typ} }
	fn := &ir.Func{
		Return:      typ,
		Synthesized: true,
		Block: []ir.Stmt{
			// Declared by its type and then given its zero: Kotlin types a
			// list local by its first value, and an empty one is a mutable
			// list the call's list does not fit.
			&ir.LocalVar{Name: v.Name, Type: typ, Sym: v},
			&ir.Assign{Target: ref(), Op: ast.AssignSet, Value: ir.ZeroExpr(typ)},
			&ir.If{
				Cond:  &ir.Literal{Type: ir.TypBool, Value: "true"},
				Body:  []ir.Stmt{&ir.Assign{Target: ref(), Op: ast.AssignSet, Value: &inner}},
				Catch: h,
			},
			&ir.Return{Value: ref()},
		},
	}
	return &ir.Call{
		Callee: &ir.Lambda{Type: &ir.Type{Kind: ir.TypeFunc, Sig: fn.FuncSig()}, Func: fn},
		Type:   typ,
	}
}
