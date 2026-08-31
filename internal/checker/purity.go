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
	ir.Walk(f.Block, w.visit)

	switch {
	case f.Foreign.Name != "":
		// A #[foreign] function's purity is asserted by its mark, not read off
		// a body that only describes the foreign declaration (see buildFunc).
	case f.Query:
		// Same reasoning for #[query]: the body here is the fetch, but the body
		// the function ends up with is a RemoteQuery lookup, so reading effects
		// off what is written describes the wrong function. Left at the readonly
		// the mark asserted — which is also what stops the optimizer inlining a
		// query into its caller and dissolving the box it exists to return.
	case w.mutates:
		f.Purity = ir.PurityMutates
	case len(w.reads) > 0:
		f.Purity = ir.PurityReadonly
	default:
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

// visit is the shared-visitor callback for the effect analysis. An Assign/Toggle
// target is a write (via recordWrite), not a read, so those nodes are pruned
// (SkipDir) and their read-bearing operands walked explicitly; an Emit is an
// observable side effect; every other expression records a read of an external
// reactive var. Every other statement is descended into by the walker, which
// visits its expressions (recording reads) and reaches statements nested inside
// handler, timer, and lambda/closure bodies.
func (w *effectWalker) visit(n ir.Node) error {
	switch x := n.(type) {
	case *ir.Assign:
		w.recordWrite(x.Target)
		ir.Walk(x.Value, w.visit)
		return ir.SkipDir
	case *ir.Toggle:
		w.recordWrite(x.Target)
		return ir.SkipDir
	case *ir.Emit:
		// Emitting an event fires parent handlers — an observable side
		// effect. Args are read; the walker descends into them.
		w.mutates = true
	case ir.Expr:
		if v := w.externalVar(x); v != nil {
			w.reads[v] = struct{}{}
		}
	}
	return nil
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
	// Field/index/deref target: conservatively a side effect. The target is
	// still walked for the reads it performs (e.g. `m[k] = v` reads m and k).
	w.mutates = true
	ir.Walk(target, w.visit)
}
