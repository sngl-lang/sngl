package ir

import (
	"fmt"
)

// Walk visits every node reachable from root in pre-order, read-only: the
// callback cannot replace nodes. It reaches exactly what Rewrite reaches, in
// the same order and under the same SkipDir/SkipAll rules -- see Rewrite for
// which slots are owned and which are references -- but writes nothing back.
//
// A slot is read after the visit of the node that holds it, so a callback may
// replace a node's own fields and the walk descends into what it put there,
// as Rewrite does. TestWalkMatchesRewrite pins the visit order against
// Rewrite's under no-op callbacks; TestWalkReadsSlotsAfterTheVisit pins the
// read-after-visit rule for Children, Props and an If's Body.
func Walk(root any, visit func(Node) error) error {
	w := walker{visit: visit}
	w.root(root)
	return w.err
}

// WalkStmts is a read-only walk whose callback fires only on statements.
func WalkStmts(root any, fn func(Stmt) error) error {
	w := walker{visitStmt: fn}
	w.root(root)
	return w.err
}

// WalkExprs is a read-only walk whose callback fires only on expressions.
func WalkExprs(root any, fn func(Expr) error) error {
	w := walker{visitExpr: fn}
	w.root(root)
	return w.err
}

// walker is rewriter without the write-back. Exactly one of the three visit
// funcs is set; a node the set one does not take is descended into.
type walker struct {
	visit     func(Node) error
	visitExpr func(Expr) error
	visitStmt func(Stmt) error
	done      bool
	err       error
}

func (w *walker) control(err error) (descend bool) {
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

func (w *walker) expr(e Expr) {
	if w.done || e == nil {
		return
	}
	switch {
	case w.visit != nil:
		if !w.control(w.visit(e)) {
			return
		}
	case w.visitExpr != nil:
		if !w.control(w.visitExpr(e)) {
			return
		}
	}
	switch x := e.(type) {
	case *Binary:
		w.expr(x.Left)
		w.expr(x.Right)
	case *Unary:
		w.expr(x.Operand)
	case *Ternary:
		w.expr(x.Cond)
		w.expr(x.Then)
		w.expr(x.Else)
	case *Call:
		w.expr(x.Receiver)
		w.expr(x.Callee)
		for i := range x.Args {
			w.expr(x.Args[i].Value)
		}
		if x.ErrorHandler != nil {
			w.fn(x.ErrorHandler.Func)
		}
	case *Conversion:
		w.expr(x.Operand)
	case *Select:
		w.expr(x.Operand)
	case *Index:
		w.expr(x.Operand)
		w.expr(x.Idx)
	case *ListLit:
		for i := range x.Elems {
			w.expr(x.Elems[i])
		}
	case *MapLitIR:
		for i := range x.Entries {
			w.expr(x.Entries[i].Key)
			w.expr(x.Entries[i].Value)
		}
	case *StructLit:
		for i := range x.Fields {
			w.expr(x.Fields[i].Value)
		}
	case *Lambda:
		if x.Func != nil {
			w.fn(x.Func)
		}
	case *Spread:
		w.expr(x.Operand)
	case *Closure:
		if x.State != nil {
			w.expr(x.State)
		}
	case *Literal, *Ident, *ContextRead:
	default:
		panic(fmt.Sprintf("ir.Walk: unhandled ir.Expr %T", x))
	}
}

func (w *walker) stmt(s Stmt) {
	if w.done || s == nil {
		return
	}
	switch {
	case w.visit != nil:
		if !w.control(w.visit(s)) {
			return
		}
	case w.visitStmt != nil:
		if !w.control(w.visitStmt(s)) {
			return
		}
	}
	switch n := s.(type) {
	case *NodeInst:
		for i := range n.Props {
			w.expr(n.Props[i].Value)
		}
		for _, h := range n.Handlers {
			if h.Func != nil {
				w.fn(h.Func)
			}
		}
		if n.ErrorHandler != nil {
			w.fn(n.ErrorHandler.Func)
		}
		w.expr(n.Key)
		w.expr(n.Ref)
		w.expr(n.Record)
		w.stmts(n.Children)
		w.slots(n.Slots)
	case *CallStmt:
		if n.Call != nil {
			w.expr(n.Call)
		}
	case *Assign:
		w.expr(n.Target)
		w.expr(n.Value)
	case *Toggle:
		w.expr(n.Target)
	case *Emit:
		for i := range n.Args {
			w.expr(n.Args[i].Value)
		}
	case *LocalVar:
		w.expr(n.Init)
		if n.CanvasNode != nil {
			w.stmts(n.CanvasNode.Children)
		}
	case *Return:
		w.expr(n.Value)
	case *If:
		w.expr(n.Cond)
		w.stmts(n.Body)
		w.stmts(n.Else)
	case *For:
		w.expr(n.Iter)
		w.stmts(n.Body)
		w.stmts(n.Else)
	case *SlotInst:
		for i := range n.Args {
			w.expr(n.Args[i])
		}
		w.stmts(n.Children)
		w.slots(n.Slots)
	case *ErrorBoundary:
		if n.Handler != nil {
			w.fn(n.Handler.Func)
		}
		w.stmts(n.Children)
		w.stmts(n.Failed)
	case *ContextProvider:
		w.expr(n.Value)
		w.stmts(n.Children)
	case *CanvasRedrawStmt, *Break, *Continue:
	default:
		panic(fmt.Sprintf("ir.Walk: unhandled ir.Stmt %T", n))
	}
}

// slots visits a node's slot populations by name, the order Rewrite uses.
// One population is the common case and needs no sorted copy of the keys.
func (w *walker) slots(slots map[string]*SlotContent) {
	switch len(slots) {
	case 0:
		return
	case 1:
		for _, sc := range slots {
			if sc != nil {
				w.stmts(sc.Body)
			}
		}
		return
	}
	for _, name := range SlotNames(slots) {
		if sc := slots[name]; sc != nil {
			w.stmts(sc.Body)
		}
	}
}

func (w *walker) stmts(stmts []Stmt) {
	for i := range stmts {
		if w.done {
			break
		}
		w.stmt(stmts[i])
	}
}

func (w *walker) fn(f *Func) {
	if w.done || f == nil {
		return
	}
	for _, p := range f.Params {
		if p.Default != nil {
			w.expr(p.Default)
		}
	}
	w.stmts(f.Block)
}

func (w *walker) component(c *Component) {
	if w.done || c == nil {
		return
	}
	for _, p := range c.Props {
		w.expr(p.Default)
	}
	for _, v := range c.Vars {
		w.varDecl(v)
	}
	for _, f := range c.Funcs {
		w.fn(f)
	}
	w.stmts(c.Body)
}

func (w *walker) varDecl(v *Var) {
	if w.done || v == nil {
		return
	}
	w.expr(v.Init)
	for _, h := range v.Handlers {
		if h.Func != nil {
			w.fn(h.Func)
		}
	}
}

func (w *walker) pkg(pkg *Package) {
	if pkg == nil {
		return
	}
	for _, v := range pkg.Consts {
		w.varDecl(v)
	}
	for _, v := range pkg.Vars {
		w.varDecl(v)
	}
	for _, f := range pkg.Funcs {
		w.fn(f)
	}
	for _, s := range pkg.Structs {
		for _, f := range s.Fields {
			w.expr(f.Default)
		}
	}
	for _, e := range pkg.Enums {
		for _, m := range e.Members {
			w.expr(m.Value)
		}
	}
	for _, ctx := range pkg.Contexts {
		w.expr(ctx.Default)
	}
	for _, o := range pkg.Outputs {
		if o.Options != nil {
			w.expr(o.Options)
		}
	}
	for _, c := range pkg.Components {
		w.component(c)
	}
	for _, win := range pkg.Windows {
		w.stmt(win)
	}
	w.stmts(pkg.Body)
}

func (w *walker) root(root any) {
	switch r := root.(type) {
	case nil:
	case *Package:
		w.pkg(r)
	case *Component:
		w.component(r)
	case *Func:
		w.fn(r)
	case []Stmt:
		w.stmts(r)
	case Stmt:
		w.stmt(r)
	case Expr:
		w.expr(r)
	default:
		panic(fmt.Sprintf("ir.Walk: unsupported root %T", root))
	}
}
