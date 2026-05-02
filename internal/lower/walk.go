package lower

import "git.duckfam.us/jonathan/sngl/ir"

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
