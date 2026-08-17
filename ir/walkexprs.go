package ir

import "fmt"

// WalkAction is returned by an Inspector callback to steer the traversal.
type WalkAction int

const (
	// Continue descends into the visited node's children (the default).
	Continue WalkAction = iota
	// SkipChildren does not descend into this node's children but continues
	// with its siblings — a per-branch prune. Use this when a node has been
	// handled "as a whole" and its interior should not be visited.
	SkipChildren
	// Stop ends the entire traversal immediately — a short-circuit for
	// "does this contain X?" queries.
	Stop
)

// Inspector holds the optional per-node callbacks for the Inspect* family.
// Either field may be nil; the corresponding node kind is still descended
// into. Each callback returns a WalkAction (Continue / SkipChildren / Stop).
type Inspector struct {
	Stmt func(Stmt) WalkAction
	Expr func(Expr) WalkAction
}

// InspectPackage walks every statement and expression reachable from pkg in a
// single pre-order traversal, invoking in.Stmt / in.Expr on each. This is the
// general read-only visitor; the roots covered are package consts, vars (init +
// handlers), funcs, components (vars + funcs + timers + body), package timers,
// and windows. Container statements — If/For/PlatformFilter/SlotInst/
// ErrorBoundary/Window/ContextProvider — and lambda/closure bodies are
// descended into. Panics on an unknown node kind, so every new IR shape extends
// this one scaffold and all consumers stay in lockstep.
func InspectPackage(pkg *Package, in Inspector) {
	if pkg == nil {
		return
	}
	w := walker{exprFn: in.Expr, stmtFn: in.Stmt}
	w.run(pkg)
}

// InspectFunc walks the statements/expressions of a single function subtree
// (its param defaults and body), not a whole package.
func InspectFunc(fn *Func, in Inspector) {
	if fn == nil {
		return
	}
	w := walker{exprFn: in.Expr, stmtFn: in.Stmt}
	w.visitFunc(fn)
}

// InspectStmts walks a statement slice (a subtree), not a whole package.
func InspectStmts(stmts []Stmt, in Inspector) {
	w := walker{exprFn: in.Expr, stmtFn: in.Stmt}
	w.visitStmts(stmts)
}

// InspectExpr walks a single expression subtree (the expression and its
// descendants), not a whole package.
func InspectExpr(e Expr, in Inspector) {
	w := walker{exprFn: in.Expr, stmtFn: in.Stmt}
	w.visitExpr(e)
}

// WalkExprs visits every expression reachable from pkg, calling fn for each.
// fn returns true to stop the walk early (short-circuit). Thin bool adapter
// over InspectPackage; new code that needs subtree roots or per-branch pruning
// should use the Inspect* family with WalkAction directly.
func WalkExprs(pkg *Package, fn func(Expr) bool) {
	InspectPackage(pkg, Inspector{Expr: boolExpr(fn)})
}

// WalkStmts visits every statement reachable from pkg in pre-order, calling fn
// for each; fn returns true to stop early. Thin bool adapter over
// InspectPackage.
func WalkStmts(pkg *Package, fn func(Stmt) bool) {
	InspectPackage(pkg, Inspector{Stmt: boolStmt(fn)})
}

// VisitorFuncs is the bool-returning form of Inspector (true == stop the whole
// walk). Retained for existing Walk callers; prefer Inspector/WalkAction for
// new code.
type VisitorFuncs struct {
	Stmt func(Stmt) bool
	Expr func(Expr) bool
}

// Walk visits every statement and expression reachable from pkg, stopping the
// whole walk when either callback returns true. Thin bool adapter over
// InspectPackage.
func Walk(pkg *Package, v VisitorFuncs) {
	InspectPackage(pkg, Inspector{Stmt: boolStmt(v.Stmt), Expr: boolExpr(v.Expr)})
}

// boolStmt/boolExpr adapt a bool "true == stop" callback to a WalkAction one.
func boolStmt(fn func(Stmt) bool) func(Stmt) WalkAction {
	if fn == nil {
		return nil
	}
	return func(s Stmt) WalkAction {
		if fn(s) {
			return Stop
		}
		return Continue
	}
}

func boolExpr(fn func(Expr) bool) func(Expr) WalkAction {
	if fn == nil {
		return nil
	}
	return func(e Expr) WalkAction {
		if fn(e) {
			return Stop
		}
		return Continue
	}
}

// walker is the single IR traversal scaffold backing every Inspect*/Walk*
// entry point. exprFn and/or stmtFn may be nil; the corresponding nodes are
// still descended into (so e.g. a stmt-only walk still reaches statements
// buried inside lambda bodies).
type walker struct {
	exprFn func(Expr) WalkAction
	stmtFn func(Stmt) WalkAction
	done   bool
}

func (w *walker) visitExpr(e Expr) {
	if w.done || e == nil {
		return
	}
	if w.exprFn != nil {
		switch w.exprFn(e) {
		case Stop:
			w.done = true
			return
		case SkipChildren:
			return
		}
	}
	switch x := e.(type) {
	case *Binary:
		w.visitExpr(x.Left)
		w.visitExpr(x.Right)
	case *Unary:
		w.visitExpr(x.Operand)
	case *Ternary:
		w.visitExpr(x.Cond)
		w.visitExpr(x.Then)
		w.visitExpr(x.Else)
	case *Call:
		w.visitExpr(x.Receiver)
		w.visitExpr(x.Callee)
		for _, a := range x.Args {
			w.visitExpr(a.Value)
		}
	case *Conversion:
		w.visitExpr(x.Operand)
	case *Select:
		w.visitExpr(x.Operand)
	case *Index:
		w.visitExpr(x.Operand)
		w.visitExpr(x.Idx)
	case *ListLit:
		for _, el := range x.Elems {
			w.visitExpr(el)
		}
	case *MapLitIR:
		for _, kv := range x.Entries {
			w.visitExpr(kv.Key)
			w.visitExpr(kv.Value)
		}
	case *StructLit:
		for _, f := range x.Fields {
			w.visitExpr(f.Value)
		}
	case *Lambda:
		if x.Func != nil {
			w.visitFunc(x.Func)
		}
	case *Spread:
		w.visitExpr(x.Operand)
	case *Literal, *Ident:
		// Leaf — no sub-expressions.
	case *Closure:
		if x.Func != nil {
			w.visitFunc(x.Func)
		}
	case *ContextRead:
		// Leaf reference — no sub-expressions.
	default:
		panic(fmt.Sprintf("ir.WalkExprs: unhandled ir.Expr %T", x))
	}
}

func (w *walker) visitStmt(s Stmt) {
	if w.done || s == nil {
		return
	}
	if w.stmtFn != nil {
		switch w.stmtFn(s) {
		case Stop:
			w.done = true
			return
		case SkipChildren:
			return
		}
	}
	switch n := s.(type) {
	case *NodeInst:
		for _, p := range n.Props {
			w.visitExpr(p.Value)
		}
		for _, h := range n.Handlers {
			w.visitFunc(h.Func)
		}
		w.visitStmts(n.Children)
	case *CallStmt:
		if n.Call != nil {
			// The Call itself is an expression — feed it through exprFn so
			// callers that key off "any *Call" detect it at the stmt
			// boundary as well as via expression descent.
			if w.exprFn != nil {
				switch w.exprFn(n.Call) {
				case Stop:
					w.done = true
					return
				case SkipChildren:
					return
				}
			}
			w.visitExpr(n.Call.Receiver)
			w.visitExpr(n.Call.Callee)
			for _, a := range n.Call.Args {
				w.visitExpr(a.Value)
			}
		}
	case *Assign:
		w.visitExpr(n.Target)
		w.visitExpr(n.Value)
	case *Toggle:
		w.visitExpr(n.Target)
	case *Emit:
		for _, a := range n.Args {
			w.visitExpr(a.Value)
		}
	case *LocalVar:
		w.visitExpr(n.Init)
	case *Return:
		w.visitExpr(n.Value)
	case *If:
		w.visitExpr(n.Cond)
		w.visitStmts(n.Body)
		w.visitStmts(n.Else)
	case *For:
		w.visitExpr(n.Iter)
		w.visitStmts(n.Body)
		w.visitStmts(n.Else)
	case *SlotInst:
		w.visitStmts(n.Children)
	case *PlatformFilter:
		w.visitStmts(n.Body)
	case *ErrorBoundary:
		w.visitStmts(n.Children)
	case *Window:
		w.visitExpr(n.Href)
		w.visitExpr(n.Title)
		w.visitExpr(n.Favicon)
		for _, v := range n.Vars {
			w.visitVar(v)
		}
		for _, f := range n.Funcs {
			w.visitFunc(f)
		}
		w.visitStmts(n.Body)
	case *ContextProvider:
		w.visitStmts(n.Children)
	case *CanvasRedrawStmt:
		// No expressions to walk.
	default:
		panic(fmt.Sprintf("ir.WalkExprs: unhandled ir.Stmt %T", n))
	}
}

func (w *walker) visitStmts(stmts []Stmt) {
	for _, s := range stmts {
		if w.done {
			return
		}
		w.visitStmt(s)
	}
}

func (w *walker) visitFunc(f *Func) {
	if w.done || f == nil {
		return
	}
	for _, p := range f.Params {
		if p.Default != nil {
			w.visitExpr(p.Default)
		}
	}
	w.visitStmts(f.Block)
}

func (w *walker) visitVar(v *Var) {
	if w.done || v == nil {
		return
	}
	w.visitExpr(v.Init)
	for _, h := range v.Handlers {
		w.visitFunc(h.Func)
	}
}

func (w *walker) run(pkg *Package) {
	for _, v := range pkg.Consts {
		w.visitExpr(v.Init)
	}
	for _, v := range pkg.Vars {
		w.visitVar(v)
	}
	for _, f := range pkg.Funcs {
		w.visitFunc(f)
	}
	for _, c := range pkg.Components {
		for _, v := range c.Vars {
			w.visitVar(v)
		}
		for _, f := range c.Funcs {
			w.visitFunc(f)
		}
		for _, t := range c.Timers {
			if t.Interval != nil {
				w.visitExpr(t.Interval)
			}
			if t.Enabled != nil {
				w.visitExpr(t.Enabled)
			}
			w.visitFunc(t.Handler)
		}
		w.visitStmts(c.Body)
	}
	for _, t := range pkg.Timers {
		if t.Interval != nil {
			w.visitExpr(t.Interval)
		}
		if t.Enabled != nil {
			w.visitExpr(t.Enabled)
		}
		w.visitFunc(t.Handler)
	}
	for _, win := range pkg.Windows {
		w.visitExpr(win.Href)
		w.visitExpr(win.Title)
		w.visitExpr(win.Favicon)
		for _, v := range win.Vars {
			w.visitVar(v)
		}
		for _, f := range win.Funcs {
			w.visitFunc(f)
		}
		if win.ErrorHandler != nil {
			w.visitFunc(win.ErrorHandler.Func)
		}
		w.visitStmts(win.Body)
	}
}
