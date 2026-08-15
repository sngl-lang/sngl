package checker

import "git.duckfam.us/jonathan/sngl/ir"

// analyzeEffects computes a function's direct purity and its Reads/Writes sets
// by walking the checked IR body (fn.Block), using resolved identifier symbols
// rather than names. varSet is the set of *reactive* vars in scope (package +
// component vars, by pointer) — locals, params, loop vars, and consts are not
// members, so a write to a local that shadows a package var is correctly seen
// as internal, not a mutation of external state.
//
// This computes only *direct* effects. Transitive purity (a function that
// calls an impure one) is propagated separately by the call-graph fixed point
// in checkBodies, after every function has its direct purity.
func analyzeEffects(f *ir.Func, varSet map[*ir.Var]struct{}) {
	w := &effectWalker{
		vars:   varSet,
		reads:  make(map[*ir.Var]struct{}),
		writes: make(map[*ir.Var]struct{}),
	}
	w.walkStmts(f.Block)

	if w.mutates {
		f.Purity = ir.PurityMutates
	} else if len(w.reads) > 0 {
		f.Purity = ir.PurityReadonly
	} else {
		f.Purity = ir.PurityPure
	}

	f.Reads = f.Reads[:0]
	for v := range w.reads {
		f.Reads = append(f.Reads, v)
	}
	f.Writes = f.Writes[:0]
	for v := range w.writes {
		f.Writes = append(f.Writes, v)
	}
}

type effectWalker struct {
	vars    map[*ir.Var]struct{}
	reads   map[*ir.Var]struct{}
	writes  map[*ir.Var]struct{}
	mutates bool
}

// externalVar returns the reactive var an expression refers to, or nil when the
// expression is not a plain identifier bound to a reactive var (i.e. it is a
// local/param/loop-var, a const, or a non-identifier).
func (w *effectWalker) externalVar(e ir.Expr) *ir.Var {
	id, ok := e.(*ir.Ident)
	if !ok {
		return nil
	}
	v, ok := id.Sym.(*ir.Var)
	if !ok {
		return nil
	}
	if _, ok := w.vars[v]; ok {
		return v
	}
	return nil
}

func (w *effectWalker) walkStmts(stmts []ir.Stmt) {
	for _, s := range stmts {
		w.walkStmt(s)
	}
}

func (w *effectWalker) walkStmt(s ir.Stmt) {
	switch n := s.(type) {
	case *ir.Assign:
		w.recordWrite(n.Target)
		w.walkExpr(n.Value)
	case *ir.Toggle:
		w.recordWrite(n.Target)
	case *ir.Emit:
		// Emitting an event fires parent handlers — an observable side effect.
		w.mutates = true
		for _, a := range n.Args {
			w.walkExpr(a.Value)
		}
	case *ir.LocalVar:
		w.walkExpr(n.Init)
	case *ir.Return:
		w.walkExpr(n.Value)
	case *ir.CallStmt:
		if n.Call != nil {
			w.walkExpr(n.Call)
		}
	case *ir.If:
		w.walkExpr(n.Cond)
		w.walkStmts(n.Body)
		w.walkStmts(n.Else)
	case *ir.For:
		w.walkExpr(n.Iter)
		w.walkStmts(n.Body)
		w.walkStmts(n.Else)
	case *ir.NodeInst:
		for _, p := range n.Props {
			w.walkExpr(p.Value)
		}
		for _, h := range n.Handlers {
			if h.Func != nil {
				w.walkStmts(h.Func.Block)
			}
		}
		w.walkStmts(n.Children)
	case *ir.SlotInst:
		w.walkStmts(n.Children)
	case *ir.PlatformFilter:
		w.walkStmts(n.Body)
	case *ir.ErrorBoundary:
		w.walkStmts(n.Children)
	case *ir.ContextProvider:
		w.walkStmts(n.Children)
	}
}

// recordWrite classifies an assignment/toggle target. A plain identifier bound
// to a reactive var is a mutation of external state; a plain identifier bound
// to a local/param is internal (pure). Any other target shape (field, index,
// deref) may reach external state, so it is treated conservatively as a
// mutation. The target is also walked for reads (e.g. `m[k] = v` reads m, k).
func (w *effectWalker) recordWrite(target ir.Expr) {
	if v := w.externalVar(target); v != nil {
		w.mutates = true
		w.writes[v] = struct{}{}
		return
	}
	if _, ok := target.(*ir.Ident); ok {
		// Local/param/loop-var write — no external effect.
		return
	}
	// Field/index/deref target: conservatively a side effect.
	w.mutates = true
	w.walkExpr(target)
}

func (w *effectWalker) walkExpr(e ir.Expr) {
	switch x := e.(type) {
	case nil:
		return
	case *ir.Ident:
		if v := w.externalVar(x); v != nil {
			w.reads[v] = struct{}{}
		}
	case *ir.Binary:
		w.walkExpr(x.Left)
		w.walkExpr(x.Right)
	case *ir.Unary:
		w.walkExpr(x.Operand)
	case *ir.Ternary:
		w.walkExpr(x.Cond)
		w.walkExpr(x.Then)
		w.walkExpr(x.Else)
	case *ir.Call:
		w.walkExpr(x.Receiver)
		w.walkExpr(x.Callee)
		for _, a := range x.Args {
			w.walkExpr(a.Value)
		}
	case *ir.Conversion:
		w.walkExpr(x.Operand)
	case *ir.Select:
		w.walkExpr(x.Operand)
	case *ir.Index:
		w.walkExpr(x.Operand)
		w.walkExpr(x.Idx)
	case *ir.ListLit:
		for _, el := range x.Elems {
			w.walkExpr(el)
		}
	case *ir.MapLitIR:
		for _, kv := range x.Entries {
			w.walkExpr(kv.Key)
			w.walkExpr(kv.Value)
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			w.walkExpr(f.Value)
		}
	case *ir.Spread:
		w.walkExpr(x.Operand)
	case *ir.Lambda:
		// Effects inside a lambda body count toward the enclosing function,
		// matching the conservative pre-IR behaviour.
		if x.Func != nil {
			w.walkStmts(x.Func.Block)
		}
	case *ir.Closure:
		if x.Func != nil {
			w.walkStmts(x.Func.Block)
		}
	case *ir.Literal, *ir.ContextRead:
		// Leaf — no sub-expressions and no external access.
	}
}
