package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
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
			for i := range n.Handlers {
				if n.Handlers[i].Func != nil {
					n.Handlers[i].Func.Block = rewriteStmtExprs(n.Handlers[i].Func.Block, rewrite)
				}
			}
		case *ir.SlotInst:
			n.Children = rewriteStmtExprs(n.Children, rewrite)
		case *ir.ErrorBoundary:
			n.Children = rewriteStmtExprs(n.Children, rewrite)
			if n.Handler != nil && n.Handler.Func != nil {
				n.Handler.Func.Block = rewriteStmtExprs(n.Handler.Func.Block, rewrite)
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
			n.Body = rewriteStmtExprs(n.Body, rewrite)
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
	for _, w := range pkg.Windows {
		walkWindow(w, fns)
	}
	for _, t := range pkg.Timers {
		walkTimer(t, fns)
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
	for _, t := range c.Timers {
		walkTimer(t, fns)
	}
	if fns.stmts != nil {
		c.Body = fns.stmts(c.Body)
	}
}

func walkWindow(w *ir.Window, fns walkFuncs) {
	for _, v := range w.Vars {
		walkVar(v, fns)
	}
	for _, f := range w.Funcs {
		if fns.stmts != nil {
			f.Block = fns.stmts(f.Block)
		}
	}
	// Only passTimerPrimitive puts a timer on a window; nothing in source does.
	for _, t := range w.Timers {
		walkTimer(t, fns)
	}
	if fns.stmts != nil {
		w.Body = fns.stmts(w.Body)
	}
	if w.ErrorHandler != nil && w.ErrorHandler.Func != nil && fns.stmts != nil {
		w.ErrorHandler.Func.Block = fns.stmts(w.ErrorHandler.Func.Block)
	}
}

func walkTimer(t *ir.Timer, fns walkFuncs) {
	if fns.expr != nil {
		if t.Interval != nil {
			t.Interval = fns.expr(t.Interval)
		}
		if t.Enabled != nil {
			t.Enabled = fns.expr(t.Enabled)
		}
	}
	if t.Handler != nil && fns.stmts != nil {
		t.Handler.Block = fns.stmts(t.Handler.Block)
	}
}
