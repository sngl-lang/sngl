package lower

import "git.duckfam.us/jonathan/sngl/ir"

// passIterKind stamps ir.For.IterKind on every loop so each language's ForHead
// emits a pure per-kind template instead of independently re-deriving the
// map-vs-list / indexed-vs-element choice from the iterable's type (the same
// decision was duplicated across the golang, javascript, and kotlin drivers).
//
// Always-on and late: it runs after RefLoop (which desugars `for &t` into an
// ordinary two-var indexed loop) and after any pass that synthesizes For nodes
// (e.g. NoListLambdas), so it observes each loop's final shape — the same Iter
// type and variable arity codegen would see. The derivation lives in
// ir.DeriveIterKind so the checker/interpreter can share the classification.
var passIterKind = pass{
	name:    "IterKind",
	enabled: func(Caps) bool { return true },
	apply:   stampIterKind,
}

func stampIterKind(pkg *ir.Package, _ Caps, _ Options) error {
	ir.WalkStmts(pkg, func(s ir.Stmt) error {
		if f, ok := s.(*ir.For); ok {
			f.IterKind = ir.DeriveIterKind(f)
		}
		return nil
	})
	return nil
}
