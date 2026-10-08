package lower

import "duckfam.us/sngl/ir"

// passDirectCalls turns a call through a name that holds a declared function
// into a call of that function.
//
// The inliner is what makes one: a component taking `render func(…) string`
// and calling `render(xs)` is spliced with the argument in the prop's place,
// so a call site writing `render=encode` leaves `encode(xs)` as a call through
// an identifier -- Call.Callee naming the *ir.Func, and Call.Func nil. Every
// analysis that follows the call graph reads Call.Func, so that call read
// nothing: an effect keyed on it was settled by no write, and stayed on the
// first value its key ever had.
//
// Always on, since the shape is the inliner's and not a target's.
var passDirectCalls = pass{
	name:    "DirectCalls",
	enabled: func(Features) bool { return true },
	apply:   lowerDirectCalls,
}

func lowerDirectCalls(pkg *ir.Package, _ Features, _ Options) error {
	return ir.RewriteExprs(pkg, func(e ir.Expr) (ir.Expr, error) {
		call, ok := e.(*ir.Call)
		if !ok || call.Func != nil || call.Receiver != nil {
			return e, nil
		}
		id, ok := call.Callee.(*ir.Ident)
		if !ok {
			return e, nil
		}
		// A method named as a value carries its receiver in the value, which
		// a direct call would have to be handed as an argument.
		if fn, ok := id.Sym.(*ir.Func); ok && fn.Receiver == "" {
			call.Func = fn
			call.Callee = nil
		}
		return e, nil
	})
}
