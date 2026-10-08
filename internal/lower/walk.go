package lower

import (
	"fmt"

	"duckfam.us/sngl/ir"
)

// rewriteStmtExprs applies rewrite to every leaf expression reachable from the
// given statement slice (assignments, initializers, conditions, prop values,
// handler blocks, nested bodies, …), recursing into nested statement lists. It
// mutates in place and returns the same slice. Several lowering passes (enum,
// computed, unit, struct-spread) need exactly this traversal with a
// pass-specific leaf rewriter; they share this one implementation instead of
// each carrying an identical copy.
func rewriteStmtExprs(stmts []ir.Stmt, rewrite func(ir.Expr) ir.Expr) []ir.Stmt {
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
			n.Body = rewriteStmtExprs(n.Body, rewrite)
			n.Else = rewriteStmtExprs(n.Else, rewrite)
		case *ir.For:
			n.Iter = rewrite(n.Iter)
			n.Body = rewriteStmtExprs(n.Body, rewrite)
			n.Else = rewriteStmtExprs(n.Else, rewrite)
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
			n.Children = rewriteStmtExprs(n.Children, rewrite)
			rewriteSlotExprs(n.Slots, rewrite)
			for i := range n.Handlers {
				if n.Handlers[i].Func != nil {
					n.Handlers[i].Func.Block = rewriteStmtExprs(n.Handlers[i].Func.Block, rewrite)
				}
			}
		case *ir.SlotInst:
			for i := range n.Args {
				n.Args[i] = rewrite(n.Args[i])
			}
			n.Children = rewriteStmtExprs(n.Children, rewrite)
			rewriteSlotExprs(n.Slots, rewrite)
		case *ir.ErrorBoundary:
			n.Children = rewriteStmtExprs(n.Children, rewrite)
			n.Failed = rewriteStmtExprs(n.Failed, rewrite)
			if n.Handler != nil && n.Handler.Func != nil {
				n.Handler.Func.Block = rewriteStmtExprs(n.Handler.Func.Block, rewrite)
			}
		case *ir.Emit:
			for i := range n.Args {
				n.Args[i].Value = rewrite(n.Args[i].Value)
			}
		case *ir.CallStmt:
			if n.Call != nil {
				if n.Call.Callee != nil {
					n.Call.Callee = rewrite(n.Call.Callee)
				}
				if n.Call.Receiver != nil {
					n.Call.Receiver = rewrite(n.Call.Receiver)
				}
				for i := range n.Call.Args {
					n.Call.Args[i].Value = rewrite(n.Call.Args[i].Value)
				}
			}
		case *ir.Toggle:
			n.Target = rewrite(n.Target)
		case *ir.ContextProvider:
			n.Value = rewrite(n.Value)
			n.Children = rewriteStmtExprs(n.Children, rewrite)
		case *ir.Break, *ir.Continue:
			// A loop escape holds no expression.
		default:
			panic(fmt.Sprintf("rewriteStmtExprs: unhandled %T", n))
		}
	}
	return stmts
}

// rewriteSlotExprs is rewriteStmtExprs over each population, in name order.
func rewriteSlotExprs(slots map[string]*ir.SlotContent, rewrite func(ir.Expr) ir.Expr) {
	for _, name := range ir.SlotNames(slots) {
		if sc := slots[name]; sc != nil {
			sc.Body = rewriteStmtExprs(sc.Body, rewrite)
		}
	}
}

// walkFuncs collects optional callbacks invoked while walking a package's
// declarations. A nil callback skips that traversal.
type walkFuncs struct {
	// stmts is invoked for every Stmt slice in the package (component
	// bodies, function blocks, window bodies, handler blocks, timer
	// handler blocks, var handler blocks, etc.). The callback receives the
	// slice and returns a possibly-rewritten slice.
	stmts func([]ir.Stmt) []ir.Stmt

	// expr is invoked for every Expr appearing as a leaf of a declaration
	// (var initializers, prop defaults, function return values, etc.).
	expr func(ir.Expr) ir.Expr
}

// walkPackage applies the callbacks in fns to every relevant location in
// pkg. Mutates in place.
func walkPackage(pkg *ir.Package, fns walkFuncs) {
	if pkg == nil {
		return
	}
	for _, c := range pkg.Consts {
		if fns.expr != nil && c.Init != nil {
			c.Init = fns.expr(c.Init)
		}
	}
	for _, s := range pkg.Structs {
		for _, f := range s.Fields {
			if fns.expr != nil && f.Default != nil {
				f.Default = fns.expr(f.Default)
			}
		}
	}
	for _, v := range pkg.Vars {
		walkVar(v, fns)
	}
	for _, f := range pkg.Funcs {
		if fns.stmts != nil {
			f.Block = fns.stmts(f.Block)
		}
	}
	for _, comp := range pkg.Components {
		walkComponent(comp, fns)
	}
	// The package's own body is a view body like a component's: a window under
	// a top-level `for` is a statement in it and reaches these passes nowhere
	// else.
	if fns.stmts != nil {
		pkg.Body = fns.stmts(pkg.Body)
	}
}

func walkVar(v *ir.Var, fns walkFuncs) {
	if fns.expr != nil && v.Init != nil {
		v.Init = fns.expr(v.Init)
	}
	for _, h := range v.Handlers {
		if h.Func != nil && fns.stmts != nil {
			h.Func.Block = fns.stmts(h.Func.Block)
		}
	}
}

func walkComponent(c *ir.Component, fns walkFuncs) {
	for _, p := range c.Props {
		if fns.expr != nil && p.Default != nil {
			p.Default = fns.expr(p.Default)
		}
	}
	for _, v := range c.Vars {
		walkVar(v, fns)
	}
	for _, f := range c.Funcs {
		if fns.stmts != nil {
			f.Block = fns.stmts(f.Block)
		}
	}
	if fns.stmts != nil {
		c.Body = fns.stmts(c.Body)
	}
}
