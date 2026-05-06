package checker

import "git.duckfam.us/jonathan/sngl/ir"

// analyzeAsyncWithPointsTo extends color propagation to cover funcvar call
// sites: if a slot pointed to by a Callee contains any async candidate, the
// enclosing function is colored async. Runs after analyzePointsTo populates
// pkg.PointsTo. The pass is a fixed-point loop so that chains of callers are
// handled correctly.
func (c *checker) analyzeAsyncWithPointsTo() {
	pkg := c.pkg
	if pkg == nil || pkg.PointsTo == nil {
		return
	}
	funcs := allFuncs(pkg)
	for {
		changed := false
		for _, fn := range funcs {
			if fn.IsAsync {
				continue
			}
			if ir.BlockHasFuncvarAsyncCall(fn.Block, pkg.PointsTo) {
				fn.IsAsync = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}
}

// analyzeAsync propagates IsAsync over the call graph by fixed-point.
//
// Sources: native-imported funcs whose declared signature returned a
// Promise<T> (the importer set IsAsync at decl time).
//
// Propagation: any SNGL function whose body transitively calls an
// IsAsync function becomes IsAsync itself. Mirrors the CanError pass,
// but without handler-scoping — async is purely a transitive property.
func (c *checker) analyzeAsync() {
	pkg := c.pkg
	if pkg == nil {
		return
	}
	funcs := allFuncs(pkg)
	for {
		changed := false
		for _, fn := range funcs {
			if fn.IsAsync {
				continue
			}
			if ir.BlockHasAsyncCall(fn.Block) {
				fn.IsAsync = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}
}
