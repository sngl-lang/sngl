package ir

import (
	"errors"
	"fmt"
)

// SkipDir and SkipAll are sentinel errors a visit callback returns to steer the
// traversal, mirroring fs.WalkDir:
//
//   - return nil        — descend into the visited node's children (default).
//   - return SkipDir    — do not descend into this node's children, but continue
//     with its siblings (a per-branch prune).
//   - return SkipAll    — stop the whole walk; the entry point returns nil.
//   - return other err  — stop the whole walk; the entry point returns err.
var (
	SkipDir = errors.New("ir: skip this node's children")
	SkipAll = errors.New("ir: skip everything and stop the walk")
)

// Rewrite is the base traversal. It visits every node reachable from root in a
// single pre-order pass; visit returns the (possibly replaced) node plus a
// control error. The returned node is written back into its parent slot, so a
// callback that returns its input unchanged is a read-only visit (that is what
// Walk is). A replacement MUST be the same kind as the input — an Expr for an
// expression slot, a Stmt for a statement slot — or the walk panics writing it
// back.
//
// root may be a *Package, *Func, []Stmt, Stmt, or Expr (panics otherwise). For a
// container root (*Package/*Func/[]Stmt) replacements land in the container; for
// a bare Stmt/Expr root, replacing the root node itself is not observable (the
// root is passed by value) — rewrite its children, or use a container root.
//
// Panics on an unknown node kind, so every new IR shape extends this one
// scaffold and all consumers stay in lockstep.
func Rewrite(root any, visit func(Node) (Node, error)) error {
	w := rewriter{visit: visit}
	w.root(root)
	return w.err
}

// Walk visits every node reachable from root in pre-order (read-only): the
// callback cannot replace nodes. A convenience over Rewrite with an identity
// replacement.
func Walk(root any, visit func(Node) error) error {
	return Rewrite(root, func(n Node) (Node, error) { return n, visit(n) })
}

// WalkStmts is a read-only walk whose callback fires only on statements.
func WalkStmts(root any, fn func(Stmt) error) error {
	return Walk(root, func(n Node) error {
		if s, ok := n.(Stmt); ok {
			return fn(s)
		}
		return nil
	})
}

// WalkExprs is a read-only walk whose callback fires only on expressions.
func WalkExprs(root any, fn func(Expr) error) error {
	return Walk(root, func(n Node) error {
		if e, ok := n.(Expr); ok {
			return fn(e)
		}
		return nil
	})
}

// RewriteStmts rewrites only statements; expressions pass through unchanged.
func RewriteStmts(root any, fn func(Stmt) (Stmt, error)) error {
	return Rewrite(root, func(n Node) (Node, error) {
		if s, ok := n.(Stmt); ok {
			return fn(s)
		}
		return n, nil
	})
}

// RewriteExprs rewrites only expressions; statements pass through unchanged.
func RewriteExprs(root any, fn func(Expr) (Expr, error)) error {
	return Rewrite(root, func(n Node) (Node, error) {
		if e, ok := n.(Expr); ok {
			return fn(e)
		}
		return n, nil
	})
}

// rewriter is the single IR traversal scaffold. visit is invoked on every node;
// done halts the walk (set by SkipAll or a real error) and err holds the real
// error to surface (nil for SkipAll).
type rewriter struct {
	visit func(Node) (Node, error)
	done  bool
	err   error
}

// step invokes visit on n and reports the replacement plus whether to descend
// into its children.
func (w *rewriter) step(n Node) (repl Node, descend bool) {
	nn, err := w.visit(n)
	switch {
	case err == nil:
		return nn, true
	case err == SkipDir:
		return nn, false
	case err == SkipAll:
		w.done = true
		return nn, false
	default:
		w.err = err
		w.done = true
		return nn, false
	}
}

func (w *rewriter) expr(e Expr) Expr {
	if w.done || e == nil {
		return e
	}
	nn, descend := w.step(e)
	e = nn.(Expr)
	if !descend {
		return e
	}
	switch x := e.(type) {
	case *Binary:
		x.Left = w.expr(x.Left)
		x.Right = w.expr(x.Right)
	case *Unary:
		x.Operand = w.expr(x.Operand)
	case *Ternary:
		x.Cond = w.expr(x.Cond)
		x.Then = w.expr(x.Then)
		x.Else = w.expr(x.Else)
	case *Call:
		x.Receiver = w.expr(x.Receiver)
		x.Callee = w.expr(x.Callee)
		for i := range x.Args {
			x.Args[i].Value = w.expr(x.Args[i].Value)
		}
	case *Conversion:
		x.Operand = w.expr(x.Operand)
	case *Select:
		x.Operand = w.expr(x.Operand)
	case *Index:
		x.Operand = w.expr(x.Operand)
		x.Idx = w.expr(x.Idx)
	case *ListLit:
		for i := range x.Elems {
			x.Elems[i] = w.expr(x.Elems[i])
		}
	case *MapLitIR:
		for i := range x.Entries {
			x.Entries[i].Key = w.expr(x.Entries[i].Key)
			x.Entries[i].Value = w.expr(x.Entries[i].Value)
		}
	case *StructLit:
		for i := range x.Fields {
			x.Fields[i].Value = w.expr(x.Fields[i].Value)
		}
	case *Lambda:
		if x.Func != nil {
			w.fn(x.Func)
		}
	case *Spread:
		x.Operand = w.expr(x.Operand)
	case *Closure:
		if x.Func != nil {
			w.fn(x.Func)
		}
	case *Literal, *Ident, *ContextRead:
		// Leaf — no sub-expressions.
	default:
		panic(fmt.Sprintf("ir.Rewrite: unhandled ir.Expr %T", x))
	}
	return e
}

func (w *rewriter) stmt(s Stmt) Stmt {
	if w.done || s == nil {
		return s
	}
	nn, descend := w.step(s)
	s = nn.(Stmt)
	if !descend {
		return s
	}
	switch n := s.(type) {
	case *NodeInst:
		for i := range n.Props {
			n.Props[i].Value = w.expr(n.Props[i].Value)
		}
		for _, h := range n.Handlers {
			if h.Func != nil {
				w.fn(h.Func)
			}
		}
		n.Children = w.stmts(n.Children)
	case *CallStmt:
		if n.Call != nil {
			// The Call is an expression slot — visiting it descends into its
			// receiver/callee/args and keeps it detectable at the stmt boundary.
			if c, ok := w.expr(n.Call).(*Call); ok {
				n.Call = c
			}
		}
	case *Assign:
		n.Target = w.expr(n.Target)
		n.Value = w.expr(n.Value)
	case *Toggle:
		n.Target = w.expr(n.Target)
	case *Emit:
		for i := range n.Args {
			n.Args[i].Value = w.expr(n.Args[i].Value)
		}
	case *LocalVar:
		n.Init = w.expr(n.Init)
	case *Return:
		n.Value = w.expr(n.Value)
	case *If:
		n.Cond = w.expr(n.Cond)
		n.Body = w.stmts(n.Body)
		n.Else = w.stmts(n.Else)
	case *For:
		n.Iter = w.expr(n.Iter)
		n.Body = w.stmts(n.Body)
		n.Else = w.stmts(n.Else)
	case *SlotInst:
		n.Children = w.stmts(n.Children)
	case *PlatformFilter:
		n.Body = w.stmts(n.Body)
	case *ErrorBoundary:
		n.Children = w.stmts(n.Children)
	case *Window:
		n.Href = w.expr(n.Href)
		n.Title = w.expr(n.Title)
		n.Favicon = w.expr(n.Favicon)
		for _, v := range n.Vars {
			w.varDecl(v)
		}
		for _, f := range n.Funcs {
			w.fn(f)
		}
		n.Body = w.stmts(n.Body)
	case *ContextProvider:
		n.Value = w.expr(n.Value)
		n.Children = w.stmts(n.Children)
	case *CanvasRedrawStmt:
		// No expressions to walk.
	default:
		panic(fmt.Sprintf("ir.Rewrite: unhandled ir.Stmt %T", n))
	}
	return s
}

func (w *rewriter) stmts(stmts []Stmt) []Stmt {
	for i := range stmts {
		if w.done {
			break
		}
		stmts[i] = w.stmt(stmts[i])
	}
	return stmts
}

func (w *rewriter) fn(f *Func) {
	if w.done || f == nil {
		return
	}
	for _, p := range f.Params {
		if p.Default != nil {
			p.Default = w.expr(p.Default)
		}
	}
	f.Block = w.stmts(f.Block)
}

func (w *rewriter) varDecl(v *Var) {
	if w.done || v == nil {
		return
	}
	v.Init = w.expr(v.Init)
	for _, h := range v.Handlers {
		if h.Func != nil {
			w.fn(h.Func)
		}
	}
}

func (w *rewriter) timer(t *Timer) {
	if w.done || t == nil {
		return
	}
	if t.Interval != nil {
		t.Interval = w.expr(t.Interval)
	}
	if t.Enabled != nil {
		t.Enabled = w.expr(t.Enabled)
	}
	w.fn(t.Handler)
}

func (w *rewriter) pkg(pkg *Package) {
	if pkg == nil {
		return
	}
	for _, v := range pkg.Consts {
		if v.Init != nil {
			v.Init = w.expr(v.Init)
		}
	}
	for _, v := range pkg.Vars {
		w.varDecl(v)
	}
	for _, f := range pkg.Funcs {
		w.fn(f)
	}
	for _, c := range pkg.Components {
		for _, v := range c.Vars {
			w.varDecl(v)
		}
		for _, f := range c.Funcs {
			w.fn(f)
		}
		for _, t := range c.Timers {
			w.timer(t)
		}
		c.Body = w.stmts(c.Body)
	}
	for _, t := range pkg.Timers {
		w.timer(t)
	}
	for _, win := range pkg.Windows {
		win.Href = w.expr(win.Href)
		win.Title = w.expr(win.Title)
		win.Favicon = w.expr(win.Favicon)
		for _, v := range win.Vars {
			w.varDecl(v)
		}
		for _, f := range win.Funcs {
			w.fn(f)
		}
		if win.ErrorHandler != nil {
			w.fn(win.ErrorHandler.Func)
		}
		win.Body = w.stmts(win.Body)
	}
}

func (w *rewriter) root(root any) {
	switch r := root.(type) {
	case nil:
		// no-op
	case *Package:
		w.pkg(r)
	case *Func:
		w.fn(r)
	case []Stmt:
		w.stmts(r)
	case Stmt:
		w.stmt(r)
	case Expr:
		w.expr(r)
	default:
		panic(fmt.Sprintf("ir.Rewrite: unsupported root %T", root))
	}
}
