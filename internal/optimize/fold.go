package optimize

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// foldExpr attempts to evaluate an expression as a constant, falling back
// to recursive sub-expression folding.
func foldExpr(e ir.Expr, ctx *evalCtx) ir.Expr {
	if e == nil {
		return nil
	}

	// Try full constant evaluation.
	if val, ok := evalExpr(e, ctx); ok {
		if lit := irLiteral(val, e.ExprType()); lit != nil {
			return lit
		}
		if expr := irFromValue(val, e.ExprType()); expr != nil {
			return expr
		}
	}

	// Try function inlining.
	if call, ok := e.(*ir.Call); ok {
		if inlined := inlineCall(call, ctx); inlined != nil {
			if ctx != nil && call.Func != nil {
				ctx.inliningFuncs[call.Func] = true
				result := foldExpr(inlined, ctx)
				delete(ctx.inliningFuncs, call.Func)
				return result
			}
			return foldExpr(inlined, ctx)
		}
	}

	// Recursive sub-expression folding.
	switch x := e.(type) {
	case *ir.Binary:
		x.Left = foldExpr(x.Left, ctx)
		x.Right = foldExpr(x.Right, ctx)
	case *ir.Unary:
		// Reference operations (&x, *p) are never const — the underlying
		// storage may be mutated through aliases, and `&literal` is not a
		// valid emission. Leave the operand untouched so the codegen still
		// sees the original lvalue identifier.
		if x.Op == ast.UnaryAddr || x.Op == ast.UnaryDeref {
			return e
		}
		x.Operand = foldExpr(x.Operand, ctx)
	case *ir.Ternary:
		x.Cond = foldExpr(x.Cond, ctx)
		// Short-circuit if cond folded to literal bool.
		if lit, ok := x.Cond.(*ir.Literal); ok && lit.Type != nil && lit.Type.Kind == ir.TypeBool {
			if lit.Raw == "true" {
				return foldExpr(x.Then, ctx)
			}
			return foldExpr(x.Else, ctx)
		}
		x.Then = foldExpr(x.Then, ctx)
		x.Else = foldExpr(x.Else, ctx)
	case *ir.Call:
		if x.Receiver != nil {
			x.Receiver = foldExpr(x.Receiver, ctx)
		}
		for i := range x.Args {
			x.Args[i].Value = foldExpr(x.Args[i].Value, ctx)
		}
	case *ir.Conversion:
		x.Operand = foldExpr(x.Operand, ctx)
	case *ir.Select:
		x.Operand = foldExpr(x.Operand, ctx)
		if id, ok := x.Operand.(*ir.Ident); ok {
			if win, ok := id.Sym.(*ir.Window); ok {
				switch x.Field {
				case "href":
					if win.Href != nil {
						return win.Href
					}
				case "title":
					if win.Title != nil {
						return win.Title
					}
				}
			}
		}
	case *ir.Index:
		x.Operand = foldExpr(x.Operand, ctx)
		x.Idx = foldExpr(x.Idx, ctx)
	case *ir.ListLit:
		for i := range x.Elems {
			x.Elems[i] = foldExpr(x.Elems[i], ctx)
		}
	case *ir.StructLit:
		for i := range x.Fields {
			if x.Fields[i].Value != nil {
				x.Fields[i].Value = foldExpr(x.Fields[i].Value, ctx)
			}
		}
	case *ir.Spread:
		x.Operand = foldExpr(x.Operand, ctx)
	case *ir.MapLitIR:
		for i := range x.Entries {
			x.Entries[i].Key = foldExpr(x.Entries[i].Key, ctx)
			x.Entries[i].Value = foldExpr(x.Entries[i].Value, ctx)
		}
	case *ir.Literal, *ir.Ident:
		// No subexpressions to fold.
	case *ir.Lambda, *ir.Closure:
		// Lambdas/closures are opaque to constant folding; their bodies
		// are folded when their enclosing func is processed.
	case *ir.ContextRead:
		// No subexpressions.
	default:
		panic(fmt.Sprintf("foldExpr: unhandled expr %T", x))
	}
	return e
}

// foldStmts folds a slice of statements, removing nil results.
// For loops whose iterator evaluates to a const list are unrolled in place
// so downstream codegen sees static statements instead of runtime iteration.
// If statements whose condition evaluates to a const bool are inlined
// (true branch) or dropped (false branch) so downstream codegen sees static
// structure instead of runtime conditionals.
func foldStmts(stmts []ir.Stmt, ctx *evalCtx) []ir.Stmt {
	var out []ir.Stmt
	for _, s := range stmts {
		if ni, ok := s.(*ir.NodeInst); ok && ni.Component != nil {
			if inlined := inlineComponentCall(ni, ctx); inlined != nil {
				out = append(out, inlined...)
				continue
			}
		}
		if fs, ok := s.(*ir.For); ok {
			if expanded := expandForStmt(fs, ctx); expanded != nil {
				out = append(out, expanded...)
				continue
			}
		}
		if ifs, ok := s.(*ir.If); ok {
			cond := foldExpr(ifs.Cond, ctx)
			if lit, ok := cond.(*ir.Literal); ok && lit.Type != nil && lit.Type.Kind == ir.TypeBool {
				if lit.Raw == "true" {
					out = append(out, foldStmts(ifs.Body, ctx)...)
				} else {
					out = append(out, foldStmts(ifs.Else, ctx)...)
				}
				continue
			}
			ifs.Cond = cond
		}
		result := foldStmt(s, ctx)
		if result != nil {
			out = append(out, result)
		}
	}
	return out
}

// foldStmt folds constants and eliminates dead branches in a statement.
// Returns nil to remove the statement.
func foldStmt(s ir.Stmt, ctx *evalCtx) ir.Stmt {
	switch n := s.(type) {
	case *ir.If:
		return foldIfStmt(n, ctx)
	case *ir.PlatformFilter:
		return foldPlatformFilter(n, ctx)
	case *ir.NodeInst:
		return foldNodeInst(n, ctx)
	case *ir.For:
		n.Iter = foldExpr(n.Iter, ctx)
		n.Body = foldStmts(n.Body, ctx)
		n.Else = foldStmts(n.Else, ctx)
	case *ir.Assign:
		n.Value = foldExpr(n.Value, ctx)
	case *ir.CallStmt:
		if n.Call != nil {
			if n.Call.Receiver != nil {
				n.Call.Receiver = foldExpr(n.Call.Receiver, ctx)
			}
			for i := range n.Call.Args {
				n.Call.Args[i].Value = foldExpr(n.Call.Args[i].Value, ctx)
			}
		}
	case *ir.LocalVar:
		if n.Init != nil {
			n.Init = foldExpr(n.Init, ctx)
		}
	case *ir.Return:
		if n.Value != nil {
			n.Value = foldExpr(n.Value, ctx)
		}
	case *ir.Emit:
		for i := range n.Args {
			n.Args[i].Value = foldExpr(n.Args[i].Value, ctx)
		}
	case *ir.SlotInst:
		n.Children = foldStmts(n.Children, ctx)
	case *ir.ContextProvider:
		if n.Value != nil {
			n.Value = foldExpr(n.Value, ctx)
		}
		n.Children = foldStmts(n.Children, ctx)
	case *ir.Window:
		if n.Href != nil {
			n.Href = foldExpr(n.Href, ctx)
		}
		if n.Title != nil {
			n.Title = foldExpr(n.Title, ctx)
		}
		if n.Favicon != nil {
			n.Favicon = foldExpr(n.Favicon, ctx)
		}
		n.Body = foldStmts(n.Body, ctx)
	case *ir.Toggle:
		n.Target = foldExpr(n.Target, ctx)
	case *ir.ErrorBoundary:
		n.Children = foldStmts(n.Children, ctx)
	default:
		panic(fmt.Sprintf("foldStmt: unhandled stmt %T", n))
	}
	return s
}

func foldIfStmt(s *ir.If, ctx *evalCtx) ir.Stmt {
	s.Cond = foldExpr(s.Cond, ctx)

	if lit, ok := s.Cond.(*ir.Literal); ok && lit.Type != nil && lit.Type.Kind == ir.TypeBool {
		if lit.Raw == "true" {
			// Always true — inline body.
			s.Cond = nil
		} else {
			// Always false — dead branch.
			return nil
		}
	}

	s.Body = foldStmts(s.Body, ctx)
	s.Else = foldStmts(s.Else, ctx)
	return s
}

func foldPlatformFilter(s *ir.PlatformFilter, ctx *evalCtx) ir.Stmt {
	if ctx.platform != "" && ctx.platform != s.Platform {
		return nil // non-matching platform: eliminate
	}
	s.Body = foldStmts(s.Body, ctx)
	return s
}

func foldNodeInst(n *ir.NodeInst, ctx *evalCtx) ir.Stmt {
	for i := range n.Props {
		n.Props[i].Value = foldExpr(n.Props[i].Value, ctx)
	}
	for i := range n.Handlers {
		n.Handlers[i].Func.Block = foldStmts(n.Handlers[i].Func.Block, ctx)
	}
	n.Children = foldStmts(n.Children, ctx)
	return n
}
