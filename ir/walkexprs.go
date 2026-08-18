package ir

import (
	"errors"
	"fmt"
)

// SkipDir and SkipAll are sentinel errors an Inspector callback returns to
// steer the traversal, mirroring fs.WalkDir:
//
//   - return nil        — descend into the visited node's children (default).
//   - return SkipDir    — do not descend into this node's children, but continue
//     with its siblings (a per-branch prune).
//   - return SkipAll    — stop the whole walk; the Inspect* call returns nil.
//   - return other err  — stop the whole walk; the Inspect* call returns err.
var (
	SkipDir = errors.New("ir: skip this node's children")
	SkipAll = errors.New("ir: skip everything and stop the walk")
)

// Inspector holds the optional per-node callbacks for the Inspect* family.
// Either field may be nil; the corresponding node kind is still descended into.
// Each callback returns an error per the SkipDir/SkipAll contract above.
type Inspector struct {
	Stmt func(Stmt) error
	Expr func(Expr) error
}

// InspectPackage walks every statement and expression reachable from pkg in a
// single pre-order traversal, invoking in.Stmt / in.Expr on each. This is the
// general read-only visitor; the roots covered are package consts, vars (init +
// handlers), funcs, components (vars + funcs + timers + body), package timers,
// and windows. Container statements — If/For/PlatformFilter/SlotInst/
// ErrorBoundary/Window/ContextProvider — and lambda/closure bodies are
// descended into. Returns the first non-sentinel error a callback produced, or
// nil (SkipAll is swallowed). Panics on an unknown node kind, so every new IR
// shape extends this one scaffold and all consumers stay in lockstep.
func InspectPackage(pkg *Package, in Inspector) error {
	if pkg == nil {
		return nil
	}
	w := walker{exprFn: in.Expr, stmtFn: in.Stmt}
	w.run(pkg)
	return w.err
}

// InspectFunc walks the statements/expressions of a single function subtree
// (its param defaults and body), not a whole package.
func InspectFunc(fn *Func, in Inspector) error {
	if fn == nil {
		return nil
	}
	w := walker{exprFn: in.Expr, stmtFn: in.Stmt}
	w.visitFunc(fn)
	return w.err
}

// InspectStmts walks a statement slice (a subtree), not a whole package.
func InspectStmts(stmts []Stmt, in Inspector) error {
	w := walker{exprFn: in.Expr, stmtFn: in.Stmt}
	w.visitStmts(stmts)
	return w.err
}

// InspectExpr walks a single expression subtree (the expression and its
// descendants), not a whole package.
func InspectExpr(e Expr, in Inspector) error {
	w := walker{exprFn: in.Expr, stmtFn: in.Stmt}
	w.visitExpr(e)
	return w.err
}

// walker is the single IR traversal scaffold backing every Inspect* entry
// point. exprFn and/or stmtFn may be nil; the corresponding nodes are still
// descended into (so e.g. a stmt-only walk still reaches statements buried
// inside lambda bodies). done halts the walk (set by SkipAll or a real error);
// err holds the real error to surface (nil for SkipAll).
type walker struct {
	exprFn func(Expr) error
	stmtFn func(Stmt) error
	done   bool
	err    error
}

// gate applies a callback's returned error to the walk state and reports
// whether to descend into the current node's children.
func (w *walker) gate(err error) (descend bool) {
	switch {
	case err == nil:
		return true
	case err == SkipDir:
		return false
	case err == SkipAll:
		w.done = true
		return false
	default:
		w.err = err
		w.done = true
		return false
	}
}

func (w *walker) visitExpr(e Expr) {
	if w.done || e == nil {
		return
	}
	if w.exprFn != nil {
		if !w.gate(w.exprFn(e)) {
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
		if !w.gate(w.stmtFn(s)) {
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
				if !w.gate(w.exprFn(n.Call)) {
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
