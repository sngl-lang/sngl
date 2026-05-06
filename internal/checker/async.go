package checker

import "git.duckfam.us/jonathan/sngl/ir"

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
