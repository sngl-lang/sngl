package optimize

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// foldExpr attempts to evaluate an expression as a constant, falling back
// to recursive sub-expression folding.
func foldExpr(e ir.Expr, ctx *evalCtx) ir.Expr {
	if e == nil {
		return nil
	}

	if val, ok := evalExpr(e, ctx); ok {
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
		if lit := scaledUnitLiteral(x); lit != nil {
			return lit
		}
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
			if lit.Value == "true" {
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
				// Every prop of the #[builtin("window")] component is
				// readable off a window symbol, so all three must fold here —
				// a Select left standing reaches codegen as a dangling
				// reference. Keep in step with windowStructValue (expand.go),
				// which does the same for the unrolled-list case.
				switch x.Field {
				case "href":
					if win.Href != nil {
						return win.Href
					}
				case "title":
					if win.Title != nil {
						return win.Title
					}
				case "favicon":
					if win.Favicon != nil {
						return win.Favicon
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
	case *ir.Lambda:
		// A lambda's body is statements, folded as any other block is. It used
		// to be skipped here on the grounds that the enclosing func would fold
		// it, which holds for a lambda the program wrote and not for one a
		// lowering pass synthesized: passQuery builds the thunk after the
		// optimizer has walked every declaration, so the calls inside it are
		// calls nothing has looked at.
		if x.Func != nil {
			x.Func.Block = foldStmts(x.Func.Block, ctx)
		}
	case *ir.Closure:
		if x.Func != nil {
			x.Func.Block = foldStmts(x.Func.Block, ctx)
		}
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
				if lit.Value == "true" {
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
	case *ir.CanvasRedrawStmt:
		// Canvas redraw stmts carry only NodeInst/Func pointers; no expressions to fold.
	default:
		panic(fmt.Sprintf("foldStmt: unhandled stmt %T", n))
	}
	return s
}

func foldIfStmt(s *ir.If, ctx *evalCtx) ir.Stmt {
	s.Cond = foldExpr(s.Cond, ctx)

	if lit, ok := s.Cond.(*ir.Literal); ok && lit.Type != nil && lit.Type.Kind == ir.TypeBool {
		if lit.Value == "true" {
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

func foldNodeInst(n *ir.NodeInst, ctx *evalCtx) ir.Stmt {
	for i := range n.Props {
		// A func-typed prop's lambda is a handler written as an argument, so
		// its body folds the way a handler's does. foldExpr leaves a lambda
		// alone — a lambda elsewhere is folded when its enclosing func is —
		// and this one has no enclosing func to be reached from, so an
		// unrolled loop would leave its own variable behind in the body.
		if lam, ok := n.Props[i].Value.(*ir.Lambda); ok && lam.Func != nil {
			lam.Func.Block = foldStmts(lam.Func.Block, ctx)
			continue
		}
		n.Props[i].Value = foldExpr(n.Props[i].Value, ctx)
	}
	for i := range n.Handlers {
		n.Handlers[i].Func.Block = foldStmts(n.Handlers[i].Func.Block, ctx)
	}
	n.Children = foldStmts(n.Children, ctx)
	return n
}

// scaledUnitLiteral folds a unit literal scaled by a number -- `400 * 1px`,
// `1px * 400`, `100px / 2` -- into the single literal `400px`.
//
// It works on the expressions rather than through evalExpr because the
// folder's value model has no unit: parseLiteral answers nil for one, so a
// measurement written as arithmetic never folded at all. That is not cosmetic.
// A canvas reads its pixel size off a literal prop, and a canvas sized by a
// const expression was falling back to the 300x150 default on every platform.
//
// Scaling is the case that needs no conversion table: the suffix is the one
// the unit literal was written with. Adding two units does need one, and is
// left alone.
func scaledUnitLiteral(x *ir.Binary) *ir.Literal {
	if x.Op != ast.BinMul && x.Op != ast.BinDiv {
		return nil
	}
	unit, _ := x.Left.(*ir.Literal)
	num, _ := x.Right.(*ir.Literal)
	if unit == nil || num == nil {
		return nil
	}
	if unit.Suffix == "" {
		if x.Op == ast.BinDiv {
			// A number over a unit is not that unit.
			return nil
		}
		unit, num = num, unit
	}
	if unit.Suffix == "" || num.Suffix != "" {
		return nil
	}
	amount, err := strconv.ParseFloat(strings.TrimSuffix(unit.Value, unit.Suffix), 64)
	if err != nil {
		return nil
	}
	factor, err := strconv.ParseFloat(num.Value, 64)
	if err != nil {
		return nil
	}
	if x.Op == ast.BinDiv {
		if factor == 0 {
			return nil
		}
		amount /= factor
	} else {
		amount *= factor
	}
	text := strconv.FormatFloat(amount, 'g', -1, 64)
	if amount == math.Trunc(amount) && !math.IsInf(amount, 0) {
		text = strconv.FormatInt(int64(amount), 10)
	}
	return &ir.Literal{Type: unit.Type, Value: text + unit.Suffix, Suffix: unit.Suffix}
}
