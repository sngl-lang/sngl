package lower

import "duckfam.us/sngl/ir"

// passIterKind stamps ir.For.IterKind on every loop so each language's ForHead
// emits a pure per-kind template instead of independently re-deriving the
// map-vs-list / indexed-vs-element choice from the iterable's type (the same
// decision was duplicated across the golang, javascript, and kotlin drivers).
//
// Always-on and late: it runs after RefLoop (which desugars `for var &t` into an
// ordinary two-var indexed loop) and after any pass that synthesizes For nodes
// (e.g. NoListLambdas), so it observes each loop's final shape — the same Iter
// type and variable arity codegen would see. The derivation lives in
// ir.DeriveIterKind so the checker/interpreter can share the classification.
//
// It also records the sequence an integer-range loop walks (ir.CountedSeq).
// That is the same reading of the loop's final shape: whether a sngl:seq call
// is still in the iterable to recognize depends on which pass touched it last.
var passIterKind = pass{
	name:    "IterKind",
	enabled: func(Features) bool { return true },
	apply:   stampIterKind,
}

func stampIterKind(pkg *ir.Package, _ Features, _ Options) error {
	stamp := func(s ir.Stmt) error {
		if f, ok := s.(*ir.For); ok {
			f.Counted = ir.CountedSeq(f)
			f.IterKind = ir.DeriveIterKind(f)
		}
		return nil
	}
	ir.WalkStmts(pkg, stamp)
	// A closure's function is a reference the walk does not follow, and one
	// lifted out of a component the inliner spliced away is in no list of
	// the package's: a keyed loop's update, written in the handler a
	// canvas's copy relays its @click through, reached C unstamped.
	for _, fn := range closureFuncs(pkg) {
		ir.WalkStmts(fn, stamp)
	}
	return nil
}
