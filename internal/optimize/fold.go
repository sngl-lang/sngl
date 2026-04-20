package optimize

import (
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
	}

	// Try function inlining.
	if call, ok := e.(*ir.Call); ok {
		if inlined := inlineCall(call, ctx); inlined != nil {
			return foldExpr(inlined, ctx)
		}
	}

	// Recursive sub-expression folding.
	switch x := e.(type) {
	case *ir.Binary:
		x.Left = foldExpr(x.Left, ctx)
		x.Right = foldExpr(x.Right, ctx)
	case *ir.Unary:
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
	}
	return e
}

// foldStmts folds a slice of statements, removing nil results.
func foldStmts(stmts []ir.Stmt, ctx *evalCtx) []ir.Stmt {
	var out []ir.Stmt
	for _, s := range stmts {
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
	case *ir.Window:
		if n.Href != nil {
			n.Href = foldExpr(n.Href, ctx)
			// Resolve name from folded href literal.
			if n.Name == "" {
				if lit, ok := n.Href.(*ir.Literal); ok && lit.Type != nil && lit.Type.Kind == ir.TypeString {
					if len(lit.Raw) >= 2 && lit.Raw[0] == '"' {
						n.Name = lit.Raw[1 : len(lit.Raw)-1]
					} else {
						n.Name = lit.Raw
					}
				}
			}
		}
		if n.Title != nil {
			n.Title = foldExpr(n.Title, ctx)
		}
		if n.Favicon != nil {
			n.Favicon = foldExpr(n.Favicon, ctx)
		}
		n.Body = foldStmts(n.Body, ctx)
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
