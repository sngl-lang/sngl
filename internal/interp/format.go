package interp

import "duckfam.us/sngl/ir"

// formatCall is call, of `_format` or `_appendFormat`, as the call of the
// format or appendFormat of the type its value has here (ir.FormatCall): a
// generic body runs once for every binding, so which type that is is known only
// while it runs.
func (env *Env) formatCall(call *ir.Call) (ir.Expr, bool) {
	if call.Func == nil || call.Func.Builtin != ir.BuiltinFormatWith && call.Func.Builtin != ir.BuiltinAppendFormatWith {
		return nil, false
	}
	bound := call
	if len(env.types) > 0 {
		bound, _ = ir.InstantiateExpr(call, env.types).(*ir.Call)
	}
	for e := env; e != nil; e = e.parent {
		if e.Pkg != nil && e.Pkg.Symbols != nil {
			return ir.FormatCall(e.Pkg.Symbols.Root, bound)
		}
	}
	return nil, false
}
