package ir

import (
	"errors"
	"fmt"
	"maps"
	"slices"
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
// root may be a *Package, *Component, *Func, []Stmt, Stmt, or Expr (panics
// otherwise); a *Window arrives as a Stmt and needs no case of its own. For a
// container root (*Package/*Component/*Func/[]Stmt) replacements land in the
// container; for a bare Stmt/Expr root, replacing the root node itself is not
// observable (the root is passed by value) — rewrite its children, or use a
// container root.
//
// Panics on an unknown node kind, so every new IR shape extends this one
// scaffold and all consumers stay in lockstep. Kind coverage is enforced by
// that panic; *slot* coverage — a new Expr-typed field on an existing node —
// would otherwise be silent, so TestRewriteVisitsEveryExprSlot enumerates the
// Expr-typed fields of the IR by reflection and fails naming any this walk
// does not reach.
//
// The walk covers what a node owns. These slots hold references to something
// owned elsewhere and are deliberately skipped, because visiting them would
// walk another node's body — once per reference, rewriting it more than once:
//
//   - Ident.Sym, Type.Decl, Prop.Sym, LocalVar.Sym — the declaration a name
//     resolves to.
//   - Call.Func — the callee, owned by pkg.Funcs; Closure.Func — the lifted
//     body, likewise appended to pkg.Funcs by passLambda. Lambda.Func,
//     Timer.Handler, EventHandler.Func and ErrorBoundary.Handler are owned and
//     are walked.
//   - Call.ResolvedHandler — aliases Call.ErrorHandler or a handler owned by
//     an enclosing boundary or window.
//   - NodeInst.Component — the component being instantiated, owned by
//     pkg.Components; the instance owns only its Props, Handlers and Children.
//   - StructLit.Def — the struct being constructed, owned by pkg.Structs.
//   - Component.Methods, StructDef/EnumDef/UnitDef.Methods — the same *Funcs
//     already reached through their owning slice.
//   - Func.Reads, Func.Writes — analysis results naming Vars owned by a scope.
//   - Package.Symbols, Import.Pkg, Context/ContextRead.Ref — tables and
//     cross-package or cross-declaration links.
//
// Type is not descended into at all: it is reached from every typed node, and
// Type.Decl would lead back out into whole declarations.
//
// That list is not a matter of taste, and the reason is worth keeping: the IR
// is cyclic, and every cycle in it closes through one of those edges. A
// recursive func reaches itself through Call.Func, a recursive component
// through NodeInst.Component, a struct method constructing its own type
// through StructLit.Def, and a scope its parent through Scope.Parent. Drop the
// reference edges and what is left — what this walk descends into — is a tree.
// So a cycle reachable by this walk is not something to guard against with a
// visited set; it is the signal that a reference has been mistaken for
// ownership.
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
		// An inline @error(e){...} belongs to this call site. ResolvedHandler
		// is deliberately not walked: it aliases either this handler or one
		// owned by an enclosing boundary or window, and walking it would visit
		// that body a second time.
		if x.ErrorHandler != nil {
			w.fn(x.ErrorHandler.Func)
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
		// Closure.Func is deliberately not walked: passLambda appends the
		// lifted func to pkg.Funcs and stores the pointer here, so the body is
		// already reached where it is declared and walking it again would
		// apply every rewrite to it twice.
		if x.State != nil {
			// The captured-state literal is built at this site and owned by it,
			// so its field values are ordinary expressions of the enclosing
			// scope — a pass that rewrites reads of a captured Var has to reach
			// them.
			if st, ok := w.expr(x.State).(*StructLit); ok {
				x.State = st
			}
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
		n.Key = w.expr(n.Key)
		n.Ref = w.expr(n.Ref)
		n.Children = w.stmts(n.Children)
		// By name, because Slots is a map and a pass that numbers what it
		// finds -- a synthesized component, a temp, an event -- would name it
		// differently on each run. Source order is not recoverable here, so
		// the order is at least the same one twice.
		for _, name := range slices.Sorted(maps.Keys(n.Slots)) {
			sc := n.Slots[name]
			if sc != nil {
				sc.Body = w.stmts(sc.Body)
			}
		}
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
		// See LocalVar.CanvasNode: a flattened canvas keeps the statements that
		// paint it on the node the flattening replaced, and they are IR like
		// any other.
		if n.CanvasNode != nil {
			n.CanvasNode.Children = w.stmts(n.CanvasNode.Children)
		}
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
		for i := range n.Args {
			n.Args[i] = w.expr(n.Args[i])
		}
		n.Children = w.stmts(n.Children)
	case *ErrorBoundary:
		// The @error handler is the boundary's own, the way a window's is:
		// Call.ResolvedHandler only aliases it, so walking it here is the one
		// visit it gets. Left out, a read in the handler was invisible to
		// every pass built on this walk.
		if n.Handler != nil {
			w.fn(n.Handler.Func)
		}
		n.Children = w.stmts(n.Children)
		n.Failed = w.stmts(n.Failed)
	case *Window:
		w.window(n)
	case *ContextProvider:
		n.Value = w.expr(n.Value)
		n.Children = w.stmts(n.Children)
	case *CanvasRedrawStmt, *Break, *Continue:
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

// component walks everything a Component owns. Split out so a caller with one
// component in hand -- a pass asking what this declaration alone instantiates
// -- reaches the same set the package walk does.
func (w *rewriter) component(c *Component) {
	if w.done || c == nil {
		return
	}
	for _, p := range c.Props {
		p.Default = w.expr(p.Default)
	}
	for _, v := range c.Vars {
		w.varDecl(v)
	}
	for _, f := range c.Funcs {
		w.fn(f)
	}
	c.Body = w.stmts(c.Body)
}

// window walks everything a Window owns. A window is reachable two ways — as a
// package-level declaration and as a statement inside a for-loop body — and
// having one body of code for both is what stops the two from drifting apart.
func (w *rewriter) window(win *Window) {
	if w.done || win == nil {
		return
	}
	for i := range win.Props {
		win.Props[i].Value = w.expr(win.Props[i].Value)
	}
	if win.ErrorHandler != nil {
		w.fn(win.ErrorHandler.Func)
	}
	win.Body = w.stmts(win.Body)
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

func (w *rewriter) pkg(pkg *Package) {
	if pkg == nil {
		return
	}
	// A const is a Var like any other: it can carry handlers, and walking only
	// its initializer was the reason a handler on one was invisible here.
	for _, v := range pkg.Consts {
		w.varDecl(v)
	}
	for _, v := range pkg.Vars {
		w.varDecl(v)
	}
	for _, f := range pkg.Funcs {
		w.fn(f)
	}
	// Declaration-level defaults are expressions of the declaring scope and are
	// folded and lowered like any other. Reaching them here is what lets the
	// consumers below drop their own copies of this walk.
	for _, s := range pkg.Structs {
		for _, f := range s.Fields {
			f.Default = w.expr(f.Default)
		}
	}
	for _, e := range pkg.Enums {
		for _, m := range e.Members {
			m.Value = w.expr(m.Value)
		}
	}
	for _, ctx := range pkg.Contexts {
		ctx.Default = w.expr(ctx.Default)
	}
	for _, o := range pkg.Outputs {
		if o.Options != nil {
			if st, ok := w.expr(o.Options).(*StructLit); ok {
				o.Options = st
			}
		}
	}
	for _, c := range pkg.Components {
		w.component(c)
	}
	for _, win := range pkg.Windows {
		w.window(win)
	}
	// The package's own body, last, so a walk sees declarations before what
	// renders them -- the same order this walk visits a component in.
	pkg.Body = w.stmts(pkg.Body)
}

func (w *rewriter) root(root any) {
	switch r := root.(type) {
	case nil:
		// no-op
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
		panic(fmt.Sprintf("ir.Rewrite: unsupported root %T", root))
	}
}
