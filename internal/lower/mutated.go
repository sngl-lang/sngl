package lower

import "duckfam.us/sngl/ir"

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
	enabled: func(c Features) bool { return true },
	apply:   lowerMutatedVars,
}

func lowerMutatedVars(pkg *ir.Package, _ Features, _ Options) error {
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
	for v := range writtenVars(pkg) {
		pkg.MutatedVars[v] = true
	}
	return nil
}

// writtenVars is every Var something in pkg writes after it is bound: an
// assignment or a toggle through it, or a call declaring it writes its
// argument. NoLambda reads it too, since a closure holds such a var by
// reference, where it sees what a later write gives it.
func writtenVars(pkg *ir.Package) map[*ir.Var]bool {
	out := map[*ir.Var]bool{}
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
					out[v] = true
				}
				return
			default:
				return
			}
		}
	}
	// The package, not a list of roots assembled here. The list this replaces
	// named component bodies, their funcs, window
	// bodies and funcs, package funcs and the package body -- and so missed
	// every `Timers` entry and a window's error handler. A timer handler that
	// wrote through a struct binding was read as writing nothing, Kotlin then
	// skipped the copy, and the in-place write left Compose's structural
	// equality saying nothing had changed: exactly the bug the copy exists to
	// prevent, in the one place nothing tested.
	_ = ir.Walk(pkg, func(n ir.Node) error {
		switch s := n.(type) {
		case *ir.Assign:
			mark(s.Target)
		case *ir.Toggle:
			mark(s.Target)
		case *ir.Call:
			if s.Func == nil {
				return nil
			}
			// A pass's call of an intrinsic (ir.IntrinsicMethod) carries no
			// marks: the intrinsic's own says whether it writes.
			if def := ir.LookupIntrinsic(s.Func.Intrinsic); !s.Func.MutatesReceiver && (def == nil || !def.MutatesReceiver) {
				return nil
			}
			// The declaration says this call writes through its argument,
			// so every argument is treated as written: which one it means
			// is the first, but a receiver reaches here as an argument or
			// as Call.Receiver depending on the pass that built it.
			if s.Receiver != nil {
				mark(s.Receiver)
			}
			for _, a := range s.Args {
				mark(a.Value)
			}
		}
		return nil
	})
	return out
}
