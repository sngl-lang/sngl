package checker

import (
	"maps"

	"git.duckfam.us/jonathan/sngl/ir"
)

// checkEffectSelfRekey refuses a bracket whose own handler writes what its own
// `on` reads.
//
// An effect's handlers run because its lifetime began or ended. A handler that
// writes the key that lifetime is identified by describes a different tree than
// the one that ran it, so the settle owes itself another pass -- and what the
// program then means is a question the language has not answered. The
// interpreter runs handlers to a fixpoint; the compiled settle runs a fixpoint
// only when the write goes through an ordinary function, because a write
// inlined in the handler body lands in a function passEffect synthesized and no
// settle is injected there. Two shapes of one program, three answers.
//
// Only the effect's OWN handlers, and only its OWN key. A click handler, a
// timer or any other code writing the key is the ordinary way to rekey a
// bracket and stays legal -- that write is not inside the lifetime it ends.
//
// Here rather than in a lowering pass because it is a rule about the language:
// `sngl test` never lowers, and passEffect does not run for a platform that
// brackets a lifetime natively, so a rule stated there would be one two of the
// three ways of running a program never applied.
func (c *checker) checkEffectSelfRekey() {
	if c.pkg == nil {
		return
	}
	pkgVars := make(map[*ir.Var]struct{}, len(c.pkg.Vars))
	for _, v := range c.pkg.Vars {
		pkgVars[v] = struct{}{}
	}
	analyzed := map[*ir.Func]bool{}
	for _, fn := range c.pkg.Funcs {
		analyzed[fn] = true
	}
	for _, comp := range c.pkg.Components {
		for _, fn := range comp.Funcs {
			analyzed[fn] = true
		}
	}
	for _, o := range ir.Owners(c.pkg) {
		scope := maps.Clone(pkgVars)
		for _, v := range o.Vars {
			scope[v] = struct{}{}
		}
		_ = ir.Walk(o.Stmts, func(n ir.Node) error {
			inst, isNode := n.(*ir.NodeInst)
			if !isNode || inst.Component == nil || inst.Component.Builtin != ir.BuiltinEffect {
				return nil
			}
			c.reportSelfRekey(inst, scope, analyzed)
			return nil
		})
	}
}

// reportSelfRekey names the first key one bracket's handler writes.
//
// First rather than all: the two handlers of one bracket are one mistake, and a
// program with three of them has one thing to change per bracket.
func (c *checker) reportSelfRekey(inst *ir.NodeInst, scope map[*ir.Var]struct{}, analyzed map[*ir.Func]bool) {
	var key ir.Expr
	for _, p := range inst.Props {
		if p.Name == "on" {
			key = p.Value
		}
	}
	if key == nil {
		// No key, so nothing a handler could rekey. This is the common form,
		// and a mount handler writing state is what it is for.
		return
	}
	reads := map[*ir.Var]bool{}
	c.varsRead(key, scope, analyzed, reads, map[*ir.Func]bool{})
	if len(reads) == 0 {
		return
	}
	for _, h := range inst.Handlers {
		if h.Func == nil || (h.Name != "mount" && h.Name != "unmount") {
			continue
		}
		writes := map[*ir.Var]bool{}
		c.varsWritten(h.Func, scope, analyzed, writes, map[*ir.Func]bool{})
		for _, v := range c.pkg.Vars {
			if reads[v] && writes[v] {
				c.reportRekey(inst, h, v.Name)
				return
			}
		}
		// Component vars are not in pkg.Vars, so the stable order for those is
		// the intersection walked over the key's own reads.
		for v := range reads {
			if writes[v] {
				c.reportRekey(inst, h, v.Name)
				return
			}
		}
	}
}

func (c *checker) reportRekey(inst *ir.NodeInst, h ir.EventHandler, name string) {
	pos := ir.StmtPos(inst)
	if h.AST != nil {
		pos = h.AST.Pos
	}
	c.error(pos, "an effect's @%s writes %q, which the same effect's `on` reads, so the bracket rekeys itself; write the key from a click handler, a timer, or any other code outside the lifetime it ends", h.Name, name)
}

// varsWritten is the state a body mutates, directly and through what it calls.
//
// fn.Writes for a function the effect analysis has already been over, and the
// same walker for one it has not -- an event handler's body is not one of
// pkg.Funcs, so nothing computed its set. Following the calls is what makes the
// answer transitive, which is the half a per-statement test of the handler body
// would miss: the write that started this was `bump()`.
func (c *checker) varsWritten(fn *ir.Func, scope map[*ir.Var]struct{}, analyzed map[*ir.Func]bool, out map[*ir.Var]bool, seen map[*ir.Func]bool) {
	if fn == nil || seen[fn] {
		return
	}
	seen[fn] = true
	if analyzed[fn] {
		for _, v := range fn.Writes {
			out[v] = true
		}
	} else {
		w := &effectWalker{vars: scope, reads: map[*ir.Var]struct{}{}, writes: map[*ir.Var]struct{}{}}
		ir.Walk(fn.Block, w.visit)
		for v := range w.writes {
			out[v] = true
		}
	}
	_ = ir.Walk(fn.Block, func(n ir.Node) error {
		if call, isCall := n.(*ir.Call); isCall {
			c.varsWritten(call.Func, scope, analyzed, out, seen)
		}
		return nil
	})
}

// varsRead is the state an expression reads, directly and through what it
// calls. `on=scaled(2)` reads whatever scaled reads.
func (c *checker) varsRead(e ir.Expr, scope map[*ir.Var]struct{}, analyzed map[*ir.Func]bool, out map[*ir.Var]bool, seen map[*ir.Func]bool) {
	_ = ir.Walk(e, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.Ident:
			if v, isVar := x.Sym.(*ir.Var); isVar {
				if _, tracked := scope[v]; tracked {
					out[v] = true
				}
			}
		case *ir.Call:
			c.readsOf(x.Func, scope, analyzed, out, seen)
		}
		return nil
	})
}

func (c *checker) readsOf(fn *ir.Func, scope map[*ir.Var]struct{}, analyzed map[*ir.Func]bool, out map[*ir.Var]bool, seen map[*ir.Func]bool) {
	if fn == nil || seen[fn] {
		return
	}
	seen[fn] = true
	if analyzed[fn] {
		for _, v := range fn.Reads {
			out[v] = true
		}
	} else {
		w := &effectWalker{vars: scope, reads: map[*ir.Var]struct{}{}, writes: map[*ir.Var]struct{}{}}
		ir.Walk(fn.Block, w.visit)
		for v := range w.reads {
			out[v] = true
		}
	}
	_ = ir.Walk(fn.Block, func(n ir.Node) error {
		if call, isCall := n.(*ir.Call); isCall {
			c.readsOf(call.Func, scope, analyzed, out, seen)
		}
		return nil
	})
}
