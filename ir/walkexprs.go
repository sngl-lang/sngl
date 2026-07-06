package ir

import "fmt"

// WalkExprs visits every expression reachable from pkg, calling fn for
// each. fn returns true to stop the walk early (e.g. for "does this
// package contain X?" queries that can short-circuit on the first hit).
//
// Roots covered: package consts, vars (init + handlers), funcs,
// components (vars + funcs + timers + body), package timers, and
// windows (href / title / favicon / vars / funcs / body). Statement
// containers — If/For/PlatformFilter/SlotInst/ErrorBoundary/Window/
// ContextProvider — are descended into; pure leaves (Literal, Ident,
// ContextRead) are visited but have no sub-expressions.
//
// Panics on an unknown node kind. The convention is that each new IR
// shape must extend this one walker in lockstep, but only this site
// needs the update — every platform/lang "does pkg use X" or "stamp X
// on every node" query routes through WalkExprs / WalkStmts instead of
// hand-rolling its own recursive switch.
func WalkExprs(pkg *Package, fn func(Expr) bool) {
	if pkg == nil {
		return
	}
	w := walker{exprFn: fn}
	w.run(pkg)
}

// WalkStmts visits every statement reachable from pkg (the same root set as
// WalkExprs), calling fn for each in pre-order. Container statements are
// visited before their children; statements nested inside handler/timer Func
// bodies and inside lambda/closure expressions are reached too. fn returns
// true to stop the walk early; a stamping pass that must visit everything
// returns false unconditionally.
func WalkStmts(pkg *Package, fn func(Stmt) bool) {
	if pkg == nil {
		return
	}
	w := walker{stmtFn: fn}
	w.run(pkg)
}

// VisitorFuncs holds the optional per-node callbacks for Walk. Either
// field may be nil; the corresponding node kind is still descended into.
// A callback returns true to stop the walk early.
type VisitorFuncs struct {
	Stmt func(Stmt) bool
	Expr func(Expr) bool
}

// Walk visits every statement and expression reachable from pkg in a
// single traversal (the same root set as WalkStmts/WalkExprs), invoking
// v.Stmt on each statement and v.Expr on each expression. Statements are
// visited in pre-order before their children. Returning true from either
// callback stops the whole walk.
//
// Walk is the general entry point; WalkStmts and WalkExprs are the
// single-callback conveniences. New IR shapes are handled by extending
// the one walker below, so every consumer stays in lockstep.
func Walk(pkg *Package, v VisitorFuncs) {
	if pkg == nil {
		return
	}
	w := walker{exprFn: v.Expr, stmtFn: v.Stmt}
	w.run(pkg)
}

// walker is the single IR traversal scaffold backing Walk, WalkExprs, and
// WalkStmts.
// exprFn and/or stmtFn may be nil; the corresponding nodes are still descended
// into (so e.g. WalkStmts reaches statements buried inside lambda bodies even
// though it sets no exprFn).
type walker struct {
	exprFn func(Expr) bool
	stmtFn func(Stmt) bool
	done   bool
}

func (w *walker) visitExpr(e Expr) {
	if w.done || e == nil {
		return
	}
	if w.exprFn != nil && w.exprFn(e) {
		w.done = true
		return
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
	if w.stmtFn != nil && w.stmtFn(s) {
		w.done = true
		return
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
			if w.exprFn != nil && w.exprFn(n.Call) {
				w.done = true
				return
			}
			w.visitExpr(n.Call.Receiver)
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
