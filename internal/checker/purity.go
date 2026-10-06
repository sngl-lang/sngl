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
		locals: freshLocals(f.Block),
	}
	ir.Walk(f.Block, w.visit)

	switch {
	case f.Foreign.Name != "":
		// A #[foreign] function's purity is asserted by its mark, not read off
		// a body that only describes the foreign declaration (see buildFunc).
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
	vars   map[*ir.Var]struct{}
	reads  map[*ir.Var]struct{}
	writes map[*ir.Var]struct{}
	// locals are the vars the function declares itself and starts from a value
	// nothing else holds (freshValue), so writing into one is no effect.
	locals  map[*ir.Var]struct{}
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
	case *ir.Call:
		// `l.push(x)` is a call, and push is the only form push has. The
		// receiver is named by the intrinsic's own MutatesReceiver, which is
		// what passReactivity reads for the same question -- so the two agree
		// on what a write is without either of them listing method names.
		if x.Func != nil && x.Func.Intrinsic != "" && len(x.Args) > 0 {
			if def, ok := ir.IntrinsicByName(x.Func.Intrinsic); ok && def.MutatesReceiver {
				if v := w.rootVar(x.Args[0].Value); v != nil {
					w.mutates = true
					w.writes[v] = struct{}{}
				}
			}
		}
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
	// A field or element of a value the function built itself: filling in a
	// struct field by field (`ret.r = …` in color.lighten) writes nothing
	// outside the call.
	if w.rootLocal(target) {
		ir.Walk(target, w.visit)
		return
	}
	// Field/index target: conservatively a side effect, and a write of the var
	// at the root of the chain. `u.score += 10` and `items[i] = x` are writes
	// of `u` and `items`, which is how passReactivity has always read them --
	// recording only `mutates` here left Writes naming a strictly narrower set
	// than the one the lowering acts on. The target is still walked for the
	// reads it performs (`m[k] = v` reads m and k).
	w.mutates = true
	if v := w.rootVar(target); v != nil {
		w.writes[v] = struct{}{}
	}
	ir.Walk(target, w.visit)
}

// freshLocals is the vars a block declares from a fresh value and never
// assigns anything else to whole -- `ret = c` would make the local c's on a
// host that shares it.
func freshLocals(block []ir.Stmt) map[*ir.Var]struct{} {
	out := map[*ir.Var]struct{}{}
	shared := map[*ir.Var]bool{}
	ir.Walk(block, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.LocalVar:
			if x.Sym != nil && freshValue(x.Init) {
				out[x.Sym] = struct{}{}
			}
		case *ir.Assign:
			if id, ok := x.Target.(*ir.Ident); ok && !freshValue(x.Value) {
				if v, ok := id.Sym.(*ir.Var); ok {
					shared[v] = true
				}
			}
		}
		return nil
	})
	for v := range shared {
		delete(out, v)
	}
	return out
}

// freshValue reports whether a local's initializer is a value nothing else
// holds: none, or a literal built where it is written. A local initialized
// from another name may share it -- a Go slice, a JavaScript object -- so a
// write into it is not provably its own.
func freshValue(init ir.Expr) bool {
	switch init.(type) {
	case nil, *ir.Literal, *ir.StructLit, *ir.ListLit, *ir.MapLitIR:
		return true
	}
	return false
}

// rootLocal reports whether target is one field or element of a var the
// function declared fresh. One level only: the value replaced is the local's
// own, while a deeper one may be a value its literal took from elsewhere
// (`r.inner.x` where inner came from a parameter is that parameter's on
// JavaScript).
func (w *effectWalker) rootLocal(target ir.Expr) bool {
	var operand ir.Expr
	switch n := target.(type) {
	case *ir.Select:
		operand = n.Operand
	case *ir.Index:
		operand = n.Operand
	default:
		return false
	}
	id, ok := operand.(*ir.Ident)
	if !ok {
		return false
	}
	v, ok := id.Sym.(*ir.Var)
	if !ok {
		return false
	}
	_, local := w.locals[v]
	return local
}

// rootVar peels a Select/Index chain to the reactive var it is rooted at, or
// nil when it is rooted at anything else. Reactivity tracks a whole var, so a
// write to any part of one is a write to it.
func (w *effectWalker) rootVar(e ir.Expr) *ir.Var {
	for {
		switch n := e.(type) {
		case *ir.Select:
			e = n.Operand
		case *ir.Index:
			e = n.Operand
		default:
			return w.externalVar(e)
		}
	}
}

// AnalyzeSynthesizedFunc gives fn, a function the build wrote after the
// check, the Purity, Reads and Writes the checker's own analysis gives one
// written in source: its direct effects over the state it can reach, then the
// highest purity of what it calls. owner is the component fn joins, or nil for
// the package. The lowering reads both -- what a key reads is which writes
// settle it -- so a function without them would be a key nothing rekeys.
func AnalyzeSynthesizedFunc(pkg *ir.Package, owner *ir.Component, fn *ir.Func) {
	analyzeEffects(fn, stateVars(pkg, owner))
	if p := highestCalledPurity(fn); p > fn.Purity {
		fn.Purity = p
	}
}

// stateVars is the state a function owner declares can read and write: the
// package's vars, and the owner's when it is a component. A var written in a
// block of a view counts as its owner's -- the checker leaves it an
// ir.LocalVar where it stands, and passHoistState later moves it there -- and
// so does one an override body declares, which is the body the target
// renders. Left out, a write to one is recorded nowhere, and the function
// writing it reads as pure, which is const-foldable.
//
// The set is by pointer, so a local shadowing one of these is not in it.
func stateVars(pkg *ir.Package, owner *ir.Component) map[*ir.Var]struct{} {
	set := map[*ir.Var]struct{}{}
	add := func(vs []*ir.Var) {
		for _, v := range vs {
			set[v] = struct{}{}
		}
	}
	add(pkg.Vars)
	add(viewStateVars(pkg.Body))
	if owner != nil {
		add(owner.Vars)
		add(viewStateVars(owner.Body))
		for _, overrides := range []map[string]ir.Body{owner.PlatformOverrides, owner.LanguageOverrides} {
			for _, b := range overrides {
				add(b.Vars)
				add(viewStateVars(b.Stmts))
			}
		}
	}
	return set
}

// viewStateVars is the state the blocks of a view declare: a `var` written
// anywhere below the top level of a body -- a node's block, an `if`'s -- which
// the checker leaves an ir.LocalVar where it stands. Only the view's own
// blocks: a handler's local is a handler's.
func viewStateVars(stmts []ir.Stmt) []*ir.Var {
	var out []*ir.Var
	for _, s := range stmts {
		for _, b := range ir.ViewBlocks(s) {
			ir.WalkView(*b, func(s ir.Stmt) bool {
				if lv, ok := s.(*ir.LocalVar); ok && lv.Sym != nil {
					out = append(out, lv.Sym)
				}
				return true
			})
		}
	}
	return out
}
