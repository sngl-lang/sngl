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
// Panics on an unknown node kind. The convention matches the
// platform-specific walkers it replaces (html/android i18n.go): each
// new IR shape must extend WalkExprs in lockstep, but only this one
// site needs the update.
func WalkExprs(pkg *Package, fn func(Expr) bool) {
	if pkg == nil {
		return
	}
	w := exprWalker{fn: fn}
	w.run(pkg)
}

type exprWalker struct {
	fn   func(Expr) bool
	done bool
}

func (w *exprWalker) visitExpr(e Expr) {
	if w.done || e == nil {
		return
	}
	if w.fn(e) {
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

func (w *exprWalker) visitStmt(s Stmt) {
	if w.done || s == nil {
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
			// The Call itself is an expression — feed it through fn so
			// callers that key off "any *Call" detect it at the stmt
			// boundary as well as via expression descent.
			if w.fn(n.Call) {
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
	default:
		panic(fmt.Sprintf("ir.WalkExprs: unhandled ir.Stmt %T", n))
	}
}

func (w *exprWalker) visitStmts(stmts []Stmt) {
	for _, s := range stmts {
		if w.done {
			return
		}
		w.visitStmt(s)
	}
}

func (w *exprWalker) visitFunc(f *Func) {
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

func (w *exprWalker) visitVar(v *Var) {
	if w.done || v == nil {
		return
	}
	w.visitExpr(v.Init)
	for _, h := range v.Handlers {
		w.visitFunc(h.Func)
	}
}

func (w *exprWalker) run(pkg *Package) {
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
