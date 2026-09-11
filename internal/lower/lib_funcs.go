package lower

import "git.duckfam.us/jonathan/sngl/ir"

// passLibFuncs promotes a library package's func into this package's func list
// when a body this build emits calls it.
//
// Every backend emits from `pkg.Funcs`, which holds the program's own
// declarations. A func declared in a `sngl:` package reached codegen only if
// some pass had inlined it, and passInlinePure leaves an impure one standing --
// so a platform override calling a helper from its own package emitted the call
// and nothing that declares it. No `sngl:` package had such a helper until the
// drawing overrides needed one, which is why nothing noticed.
//
// Promoting rather than teaching each platform to look: they do not agree on
// where to look, and the one funnel that claims to answer for all of them
// (codegen.AllFuncs) is bypassed by html, which builds its own list.
//
// Runs after passCanvas, because the call may be inside a draw function that
// pass synthesized from an override's handler body.
var passLibFuncs = pass{
	name:    "LibFuncs",
	enabled: func(c Caps) bool { return true },
	apply:   lowerLibFuncs,
}

func lowerLibFuncs(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	have := map[*ir.Func]bool{}
	for _, fn := range pkg.Funcs {
		have[fn] = true
	}
	// A func with no body is a native or an intrinsic and is emitted as the
	// call itself; one with a receiver travels with whatever declares it. What
	// is left is a plain helper another package declared, which a program's own
	// func list would never mention -- a program's declarations carry an empty
	// Pkg, so a non-empty one is exactly the library case.
	wanted := func(fn *ir.Func) bool {
		return fn != nil && !have[fn] && len(fn.Block) > 0 &&
			fn.Receiver == "" && fn.Intrinsic == "" && fn.Foreign.Name == "" &&
			fn.Pkg != ""
	}
	var roots []any
	push := func(v any) { roots = append(roots, v) }
	for _, c := range pkg.Components {
		push(c.Body)
		for _, fn := range c.Funcs {
			push(fn.Block)
		}
	}
	for _, w := range pkg.Windows {
		push(w.Body)
		for _, fn := range w.Funcs {
			push(fn.Block)
		}
	}
	for _, fn := range pkg.Funcs {
		push(fn.Block)
	}
	var added []*ir.Func
	for len(roots) > 0 {
		root := roots[0]
		roots = roots[1:]
		_ = ir.WalkExprs(root, func(e ir.Expr) error {
			call, ok := e.(*ir.Call)
			if !ok || !wanted(call.Func) {
				return nil
			}
			have[call.Func] = true
			added = append(added, call.Func)
			// A promoted helper may call a second one.
			push(call.Func.Block)
			return nil
		})
	}
	pkg.Funcs = append(pkg.Funcs, added...)
	return nil
}
