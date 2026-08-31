package lower

import (
	"fmt"
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// passRefLoop lowers `for &t = list` / `for i, &t = list` element references.
// The checker types the &-bound element var as ref<T> and wraps its reads in
// Unary{Deref}. This pass replaces every reference to that element var with
// indexed list access (`list[idx]`), so reads index the live element and
// writes update it in place under value semantics. The loop is then desugared
// into an ordinary two-var loop over indices — For.Key is the index (the user's
// own binding, or a synthesized name) and For.Value is "_" — so no ref-specific
// path reaches codegen.
//
// Runs early (before reactivity) so the rewritten `list[idx].field` writes are
// seen as mutations of the list var — the reactivity peel then fires the
// list's dependents and re-renders the loop's slot.
var passRefLoop = pass{
	name:    "RefLoop",
	enabled: func(Caps) bool { return true }, // core loop semantics
	apply:   lowerRefLoop,
}

func lowerRefLoop(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := &refLoopState{}
	for _, c := range pkg.Components {
		st.stmts(c.Body)
		for _, fn := range c.Funcs {
			if fn != nil {
				st.stmts(fn.Block)
			}
		}
		st.varHandlers(c.Vars)
		for _, t := range c.Timers {
			if t != nil && t.Handler != nil {
				st.stmts(t.Handler.Block)
			}
		}
	}
	for _, w := range pkg.Windows {
		st.stmts(w.Body)
		for _, fn := range w.Funcs {
			if fn != nil {
				st.stmts(fn.Block)
			}
		}
		st.varHandlers(w.Vars)
	}
	for _, fn := range pkg.Funcs {
		if fn != nil {
			st.stmts(fn.Block)
		}
	}
	st.varHandlers(pkg.Vars)
	return nil
}

type refLoopState struct {
	idxCounter int
}

func (st *refLoopState) varHandlers(vars []*ir.Var) {
	for _, v := range vars {
		for _, h := range v.Handlers {
			if h.Func != nil {
				st.stmts(h.Func.Block)
			}
		}
	}
}

// stmts walks statements, lowering any RefElem For it finds (depth-first so a
// nested &-loop is handled with its own element var after the outer rewrite).
func (st *refLoopState) stmts(ss []ir.Stmt) {
	for _, s := range ss {
		st.stmt(s)
	}
}

func (st *refLoopState) stmt(s ir.Stmt) {
	switch n := s.(type) {
	case *ir.For:
		if n.RefElem {
			st.lowerFor(n)
		}
		st.stmts(n.Body)
		st.stmts(n.Else)
	case *ir.If:
		st.stmts(n.Body)
		st.stmts(n.Else)
	case *ir.NodeInst:
		st.stmts(n.Children)
		for _, h := range n.Handlers {
			if h.Func != nil {
				st.stmts(h.Func.Block)
			}
		}
	case *ir.SlotInst:
		st.stmts(n.Children)
	case *ir.ErrorBoundary:
		st.stmts(n.Children)
		if n.Handler != nil && n.Handler.Func != nil {
			st.stmts(n.Handler.Func.Block)
		}
	case *ir.ContextProvider:
		st.stmts(n.Children)
	case *ir.Window:
		st.stmts(n.Body)
	case *ir.Assign, *ir.Toggle, *ir.CallStmt, *ir.Return, *ir.LocalVar, *ir.Emit, *ir.CanvasRedrawStmt:
		// Leaf statements: no nested loops to descend into. The element
		// rewrite for an enclosing &-loop already visited these via lowerFor.
	default:
		panic(fmt.Sprintf("refloop.stmt: unhandled stmt %T", n))
	}
}

// lowerFor rewrites a RefElem loop's body: every reference to the &-bound
// element var becomes Index{Iter, idx}. The index is the two-var Key or a
// synthesized name.
func (st *refLoopState) lowerFor(n *ir.For) {
	elemName := n.Key
	indexName := ""
	var idxSym *ir.LoopVar
	if n.Value != "" {
		// two-var `for i, &t`: Key is the index, Value the element. The
		// index is the user's own binding, so it keeps the symbol the
		// checker gave it — the body's references to `i` point at that one,
		// and a fresh symbol here would leave them referring to nothing.
		elemName = n.Value
		indexName = n.Key
		idxSym = n.KeySym
	} else {
		// single-var `for &t`: synthesize an index, and the symbol for it.
		indexName = "__forIdx" + strconv.Itoa(st.idxCounter)
		st.idxCounter++
	}
	if idxSym == nil {
		idxSym = &ir.LoopVar{Name: indexName, Type: ir.TypInt}
	}
	r := &refLoopRewriter{elemName: elemName, iter: n.Iter, elemType: n.ElemType, idxSym: idxSym}
	for i := range n.Body {
		n.Body[i] = r.stmt(n.Body[i])
	}
	for i := range n.Else {
		n.Else[i] = r.stmt(n.Else[i])
	}
	// Desugar into an ordinary two-var loop over indices: Key=index,
	// Value="_" (the element binding is discarded — every element use was
	// rewritten to iter[index]). Downstream reactivity and codegen treat this
	// as a plain indexed loop; no ref-specific path remains.
	n.Key = indexName
	n.KeySym = idxSym
	n.Value = "_"
	n.ValueSym = nil
	n.RefElem = false
}

// refLoopRewriter replaces references to a loop's &-bound element var with indexed
// list access. elemName is the element var; iter the list expression; idxSym
// the index symbol to index by.
type refLoopRewriter struct {
	elemName string
	iter     ir.Expr
	elemType *ir.Type
	idxSym   *ir.LoopVar
}

// indexAccess builds a fresh `iter[idx]` expression (cloned so occurrences
// don't alias).
func (r *refLoopRewriter) indexAccess() ir.Expr {
	return &ir.Index{
		Operand: cloneExpr(r.iter),
		Idx:     &ir.Ident{Name: r.idxSym.Name, Type: ir.TypInt, Sym: r.idxSym},
		Type:    r.elemType,
	}
}

// matchesElem reports whether e is a reference to the &-bound element var,
// either bare (`t`) or dereffed (`*t`, as the checker emits for ref reads).
func (r *refLoopRewriter) matchesElem(e ir.Expr) bool {
	id, ok := e.(*ir.Ident)
	if !ok {
		return false
	}
	if _, isLoop := id.Sym.(*ir.LoopVar); !isLoop {
		return false
	}
	return id.Name == r.elemName
}

func (r *refLoopRewriter) expr(e ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	// `*t` (deref of the ref element) → iter[idx].
	if u, ok := e.(*ir.Unary); ok && u.Op == ast.UnaryDeref && r.matchesElem(u.Operand) {
		return r.indexAccess()
	}
	// Bare `t` → iter[idx] (defensive; reads normally arrive dereffed).
	if r.matchesElem(e) {
		return r.indexAccess()
	}
	switch x := e.(type) {
	case *ir.Literal, *ir.Ident, *ir.ContextRead:
		// leaves (an Ident here is not the element var — checked above)
	case *ir.Binary:
		x.Left = r.expr(x.Left)
		x.Right = r.expr(x.Right)
	case *ir.Unary:
		x.Operand = r.expr(x.Operand)
	case *ir.Ternary:
		x.Cond = r.expr(x.Cond)
		x.Then = r.expr(x.Then)
		x.Else = r.expr(x.Else)
	case *ir.Conversion:
		x.Operand = r.expr(x.Operand)
	case *ir.Select:
		x.Operand = r.expr(x.Operand)
	case *ir.Index:
		x.Operand = r.expr(x.Operand)
		x.Idx = r.expr(x.Idx)
	case *ir.Call:
		x.Receiver = r.expr(x.Receiver)
		for i := range x.Args {
			x.Args[i].Value = r.expr(x.Args[i].Value)
		}
	case *ir.ListLit:
		for i := range x.Elems {
			x.Elems[i] = r.expr(x.Elems[i])
		}
	case *ir.MapLitIR:
		for i := range x.Entries {
			x.Entries[i].Key = r.expr(x.Entries[i].Key)
			x.Entries[i].Value = r.expr(x.Entries[i].Value)
		}
	case *ir.StructLit:
		for i := range x.Fields {
			x.Fields[i].Value = r.expr(x.Fields[i].Value)
		}
	case *ir.Spread:
		x.Operand = r.expr(x.Operand)
	case *ir.Lambda:
		if x.Func != nil {
			r.stmtSlice(x.Func.Block)
		}
	case *ir.Closure:
		if x.Func != nil {
			r.stmtSlice(x.Func.Block)
		}
		if x.State != nil {
			r.expr(x.State) // *ir.StructLit; fields rewritten in place
		}
	default:
		panic(fmt.Sprintf("refloop.expr: unhandled ir.Expr %T", e))
	}
	return e
}

func (r *refLoopRewriter) stmtSlice(ss []ir.Stmt) {
	for i := range ss {
		ss[i] = r.stmt(ss[i])
	}
}

func (r *refLoopRewriter) stmt(s ir.Stmt) ir.Stmt {
	switch n := s.(type) {
	case *ir.Assign:
		n.Target = r.expr(n.Target)
		n.Value = r.expr(n.Value)
	case *ir.Toggle:
		n.Target = r.expr(n.Target)
	case *ir.Return:
		n.Value = r.expr(n.Value)
	case *ir.LocalVar:
		n.Init = r.expr(n.Init)
	case *ir.CallStmt:
		if n.Call != nil {
			n.Call = r.expr(n.Call).(*ir.Call)
		}
	case *ir.Emit:
		for i := range n.Args {
			n.Args[i].Value = r.expr(n.Args[i].Value)
		}
	case *ir.If:
		n.Cond = r.expr(n.Cond)
		r.stmtSlice(n.Body)
		r.stmtSlice(n.Else)
	case *ir.For:
		// A nested loop: rewrite its Iter (may reference the outer element),
		// and descend into its body. If the nested loop shadows the element
		// name it won't match (different LoopVar sym), which is correct.
		n.Iter = r.expr(n.Iter)
		r.stmtSlice(n.Body)
		r.stmtSlice(n.Else)
	case *ir.NodeInst:
		for i := range n.Props {
			n.Props[i].Value = r.expr(n.Props[i].Value)
		}
		r.stmtSlice(n.Children)
		for _, h := range n.Handlers {
			if h.Func != nil {
				r.stmtSlice(h.Func.Block)
			}
		}
	case *ir.SlotInst:
		r.stmtSlice(n.Children)
	case *ir.ErrorBoundary:
		r.stmtSlice(n.Children)
		if n.Handler != nil && n.Handler.Func != nil {
			r.stmtSlice(n.Handler.Func.Block)
		}
	case *ir.ContextProvider:
		n.Value = r.expr(n.Value)
		r.stmtSlice(n.Children)
	case *ir.Window:
		n.Href = r.expr(n.Href)
		n.Title = r.expr(n.Title)
		n.Favicon = r.expr(n.Favicon)
		r.stmtSlice(n.Body)
	case *ir.CanvasRedrawStmt:
		// No expressions to rewrite.
	default:
		panic(fmt.Sprintf("refloop.refLoopRewriter.stmt: unhandled stmt %T", n))
	}
	return s
}
