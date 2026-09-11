package lower

import "git.duckfam.us/jonathan/sngl/ir"

// passMutatedVars records which Vars are written after they are bound.
//
// A backend with value semantics copies a struct on binding so a later
// mutation cannot reach whoever else holds it -- Kotlin does, because Compose
// decides whether to recompose by structural equality, and a handler that
// mutated a struct in place left the screen unchanged. A binding nothing ever
// writes needs no copy, and until this there was no way to tell the two apart.
//
// Last, because a pass that rewrites a body can introduce an assignment and an
// answer computed before that would be stale.
//
// Conservative by construction: anything that might write marks the var. A
// missing copy aliases two names, which is the bug the copying exists to
// prevent; a spurious one costs a shallow clone.
var passMutatedVars = pass{
	name:    "MutatedVars",
	enabled: func(c Caps) bool { return true },
	apply:   lowerMutatedVars,
}

func lowerMutatedVars(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	if pkg.MutatedVars == nil {
		pkg.MutatedVars = map[*ir.Var]bool{}
	}
	// Taking an address is a write nobody has made yet.
	for v := range pkg.AddressedVars {
		pkg.MutatedVars[v] = true
	}
	mark := func(e ir.Expr) {
		// The root of an assignment path: `s`, `s.field`, `s.a.b`, `s[i]`.
		// Writing through any of them writes the binding.
		for {
			switch x := e.(type) {
			case *ir.Select:
				e = x.Operand
			case *ir.Index:
				e = x.Operand
			case *ir.Ident:
				if v, ok := x.Sym.(*ir.Var); ok {
					pkg.MutatedVars[v] = true
				}
				return
			default:
				return
			}
		}
	}
	visit := func(root any) {
		_ = ir.WalkStmts(root, func(st ir.Stmt) error {
			switch s := st.(type) {
			case *ir.Assign:
				mark(s.Target)
			case *ir.Toggle:
				mark(s.Target)
			}
			return nil
		})
		_ = ir.WalkExprs(root, func(e ir.Expr) error {
			call, ok := e.(*ir.Call)
			if !ok || call.Func == nil || !call.Func.MutatesReceiver {
				return nil
			}
			// The declaration says this call writes through its argument, so
			// every argument is treated as written: which one it means is the
			// first, but a receiver reaches here as an argument or as
			// Call.Receiver depending on the pass that built it.
			if call.Receiver != nil {
				mark(call.Receiver)
			}
			for _, a := range call.Args {
				mark(a.Value)
			}
			return nil
		})
	}
	for _, c := range pkg.Components {
		visit(c.Body)
		for _, fn := range c.Funcs {
			visit(fn.Block)
		}
		for _, v := range c.Vars {
			for _, h := range v.Handlers {
				if h.Func != nil {
					visit(h.Func.Block)
				}
			}
		}
	}
	for _, w := range pkg.Windows {
		visit(w.Body)
		for _, fn := range w.Funcs {
			visit(fn.Block)
		}
	}
	for _, fn := range pkg.Funcs {
		visit(fn.Block)
	}
	visit(pkg.Body)
	return nil
}
