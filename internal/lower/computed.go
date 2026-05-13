package lower

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

var passComputed = pass{
	name:    "NoComputed",
	enabled: func(c Caps) bool { return c.NoComputed },
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
func lowerComputed(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}

	bodies := make(map[*ir.Func]ir.Expr)
	collectComputed(pkg.Funcs, bodies)
	for _, comp := range pkg.Components {
		collectComputed(comp.Funcs, bodies)
	}
	for _, w := range pkg.Windows {
		collectComputed(w.Funcs, bodies)
	}

	if len(bodies) == 0 {
		return nil
	}

	rewrite := func(e ir.Expr) ir.Expr {
		return inlineComputedExpr(e, bodies)
	}
	walkPackage(pkg, walkFuncs{
		expr:  rewrite,
		stmts: func(stmts []ir.Stmt) []ir.Stmt { return inlineComputedStmts(stmts, rewrite) },
	})

	pkg.Funcs = filterFuncs(pkg.Funcs, bodies)
	for _, comp := range pkg.Components {
		comp.Funcs = filterFuncs(comp.Funcs, bodies)
	}
	for _, w := range pkg.Windows {
		w.Funcs = filterFuncs(w.Funcs, bodies)
	}
	return nil
}

func isComputed(f *ir.Func) bool {
	return f.AST != nil && f.AST.Body != nil && len(f.Params) == 0 && !f.IsTest
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
			x.Func.Block = inlineComputedStmts(x.Func.Block, rewrite)
		}
		return x
	}
	return e
}

func inlineComputedStmts(stmts []ir.Stmt, rewrite func(ir.Expr) ir.Expr) []ir.Stmt {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.Assign:
			n.Value = rewrite(n.Value)
		case *ir.LocalVar:
			if n.Init != nil {
				n.Init = rewrite(n.Init)
			}
		case *ir.Return:
			if n.Value != nil {
				n.Value = rewrite(n.Value)
			}
		case *ir.If:
			n.Cond = rewrite(n.Cond)
			n.Body = inlineComputedStmts(n.Body, rewrite)
			n.Else = inlineComputedStmts(n.Else, rewrite)
		case *ir.For:
			n.Iter = rewrite(n.Iter)
			n.Body = inlineComputedStmts(n.Body, rewrite)
			n.Else = inlineComputedStmts(n.Else, rewrite)
		case *ir.PlatformFilter:
			n.Body = inlineComputedStmts(n.Body, rewrite)
		case *ir.NodeInst:
			for i := range n.Props {
				if n.Props[i].Value != nil {
					n.Props[i].Value = rewrite(n.Props[i].Value)
				}
			}
			if n.Key != nil {
				n.Key = rewrite(n.Key)
			}
			if n.Ref != nil {
				n.Ref = rewrite(n.Ref)
			}
			n.Children = inlineComputedStmts(n.Children, rewrite)
			for i := range n.Handlers {
				if n.Handlers[i].Func != nil {
					n.Handlers[i].Func.Block = inlineComputedStmts(n.Handlers[i].Func.Block, rewrite)
				}
			}
		case *ir.SlotInst:
			n.Children = inlineComputedStmts(n.Children, rewrite)
		case *ir.ErrorBoundary:
			n.Children = inlineComputedStmts(n.Children, rewrite)
			if n.Handler != nil && n.Handler.Func != nil {
				n.Handler.Func.Block = inlineComputedStmts(n.Handler.Func.Block, rewrite)
			}
		case *ir.Emit:
			for i := range n.Args {
				n.Args[i].Value = rewrite(n.Args[i].Value)
			}
		case *ir.CallStmt:
			if n.Call != nil {
				if n.Call.Receiver != nil {
					n.Call.Receiver = rewrite(n.Call.Receiver)
				}
				for i := range n.Call.Args {
					n.Call.Args[i].Value = rewrite(n.Call.Args[i].Value)
				}
			}
		case *ir.Window:
			if n.Href != nil {
				n.Href = rewrite(n.Href)
			}
			if n.Title != nil {
				n.Title = rewrite(n.Title)
			}
			if n.Favicon != nil {
				n.Favicon = rewrite(n.Favicon)
			}
			n.Body = inlineComputedStmts(n.Body, rewrite)
		}
	}
	return stmts
}
