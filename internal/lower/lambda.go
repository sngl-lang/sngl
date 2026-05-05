package lower

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

var passLambda = pass{
	name:    "NoLambda",
	enabled: func(c Caps) bool { return c.NoLambda },
	apply:   lowerLambda,
}

// capture is one captured outer-scope binding referenced by a lambda body.
type capture struct {
	Sym     ir.Symbol // the outer Var or Param being captured
	Mutable bool      // true if the body assigns to Sym
}

// analyzeCaptures walks body collecting every Ident.Sym that resolves to a
// *Var or *Param not declared inside body's params. Mutability is set when
// the body contains an Assign whose Target chain bottoms out at the same Sym.
// Returned slice is in first-occurrence order (stable across runs).
func analyzeCaptures(body []ir.Stmt, params []*ir.Param) []capture {
	paramSet := make(map[ir.Symbol]bool, len(params))
	for _, p := range params {
		paramSet[p] = true
	}

	seen := make(map[ir.Symbol]int) // sym → index in result
	var caps []capture

	addRead := func(sym ir.Symbol) {
		if sym == nil || paramSet[sym] {
			return
		}
		switch sym.(type) {
		case *ir.Var, *ir.Param:
			// OK
		default:
			return
		}
		if _, ok := seen[sym]; ok {
			return
		}
		seen[sym] = len(caps)
		caps = append(caps, capture{Sym: sym})
	}

	markMutable := func(sym ir.Symbol) {
		if sym == nil {
			return
		}
		addRead(sym)
		if i, ok := seen[sym]; ok {
			caps[i].Mutable = true
		}
	}

	var walkExpr func(ir.Expr)
	var walkStmt func(ir.Stmt)
	var walkStmts func([]ir.Stmt)

	walkExpr = func(e ir.Expr) {
		switch x := e.(type) {
		case nil:
			return
		case *ir.Ident:
			addRead(x.Sym)
		case *ir.Binary:
			walkExpr(x.Left)
			walkExpr(x.Right)
		case *ir.Unary:
			walkExpr(x.Operand)
		case *ir.Ternary:
			walkExpr(x.Cond)
			walkExpr(x.Then)
			walkExpr(x.Else)
		case *ir.Call:
			walkExpr(x.Receiver)
			for i := range x.Args {
				walkExpr(x.Args[i].Value)
			}
		case *ir.Conversion:
			walkExpr(x.Operand)
		case *ir.Select:
			walkExpr(x.Operand)
		case *ir.Index:
			walkExpr(x.Operand)
			walkExpr(x.Idx)
		case *ir.ListLit:
			for _, el := range x.Elems {
				walkExpr(el)
			}
		case *ir.StructLit:
			for i := range x.Fields {
				walkExpr(x.Fields[i].Value)
			}
		case *ir.Spread:
			walkExpr(x.Operand)
		case *ir.Lambda:
			// Nested lambdas: their own captures are the responsibility of
			// their own Lift call. Do not descend (the outer lambda's
			// captures are only the Idents directly visible at this level).
			return
		case *ir.Closure:
			// Already-lifted; do not descend.
			return
		}
	}

	rootSym := func(e ir.Expr) ir.Symbol {
		for {
			switch x := e.(type) {
			case *ir.Ident:
				return x.Sym
			case *ir.Select:
				e = x.Operand
			case *ir.Index:
				e = x.Operand
			case *ir.Unary:
				if x.Op == ast.UnaryDeref {
					e = x.Operand
					continue
				}
				return nil
			default:
				return nil
			}
		}
	}

	walkStmt = func(s ir.Stmt) {
		switch n := s.(type) {
		case *ir.Assign:
			markMutable(rootSym(n.Target))
			walkExpr(n.Target)
			walkExpr(n.Value)
		case *ir.LocalVar:
			walkExpr(n.Init)
		case *ir.Return:
			walkExpr(n.Value)
		case *ir.If:
			walkExpr(n.Cond)
			walkStmts(n.Body)
			walkStmts(n.Else)
		case *ir.For:
			walkExpr(n.Iter)
			walkStmts(n.Body)
			walkStmts(n.Else)
		case *ir.PlatformFilter:
			walkStmts(n.Body)
		case *ir.NodeInst:
			for i := range n.Props {
				walkExpr(n.Props[i].Value)
			}
			walkExpr(n.Key)
			walkExpr(n.Ref)
			walkStmts(n.Children)
			for i := range n.Handlers {
				if n.Handlers[i].Func != nil {
					walkStmts(n.Handlers[i].Func.Block)
				}
			}
		case *ir.SlotInst:
			walkStmts(n.Children)
		case *ir.ErrorBoundary:
			walkStmts(n.Children)
			if n.Handler != nil && n.Handler.Func != nil {
				walkStmts(n.Handler.Func.Block)
			}
		case *ir.Emit:
			for i := range n.Args {
				walkExpr(n.Args[i].Value)
			}
		case *ir.CallStmt:
			if n.Call != nil {
				walkExpr(n.Call)
			}
		case *ir.Toggle:
			markMutable(rootSym(n.Target))
			walkExpr(n.Target)
		case *ir.Window:
			walkExpr(n.Href)
			walkExpr(n.Title)
			walkExpr(n.Favicon)
			walkStmts(n.Body)
		}
	}

	walkStmts = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			walkStmt(s)
		}
	}

	walkStmts(body)
	return caps
}

// lowerLambda lifts closures to top-level functions plus captured-state
// structs. Phase B Task 2: capture analysis is in place; the pass body is
// still a placeholder until Task 3 (lifter) and Task 4 (apply wiring).
func lowerLambda(pkg *ir.Package) error { return nil }
