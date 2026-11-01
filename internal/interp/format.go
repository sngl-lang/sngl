package interp

import "duckfam.us/sngl/ir"

// boundCall is call, of `_format`, `_appendFormat` or a constraint's
// function, as the call of the method of the type its value has here
// (ir.BoundCall): a generic body runs once for every binding, so which type
// that is is known only while it runs.
func (env *Env) boundCall(call *ir.Call) (ir.Expr, bool) {
	if call.Func == nil || call.Func.Builtin != ir.BuiltinFormatWith && call.Func.Builtin != ir.BuiltinAppendFormatWith &&
		!ir.IsConstraintMethod(call.Func) {
		return nil, false
	}
	bound := call
	if len(env.types) > 0 {
		bound, _ = ir.InstantiateExpr(call, env.types).(*ir.Call)
	}
	if scope := env.rootScope(); scope != nil {
		return ir.BoundCall(scope, bound)
	}
	return nil, false
}

// rootScope is the scope of the package env runs, nil where it runs none.
func (env *Env) rootScope() *ir.Scope {
	for e := env; e != nil; e = e.parent {
		if e.Pkg != nil && e.Pkg.Symbols != nil {
			return e.Pkg.Symbols.Root
		}
	}
	return nil
}

// formatArgsLit is lit, a `_formatArgs[X]` written where X was a type
// parameter, holding every argument the format of the type X is here takes:
// the constraint's format, which the literal was written for, may take fewer.
func (env *Env) formatArgsLit(lit *ir.StructLit) *ir.StructLit {
	if len(env.types) == 0 || !ir.IsFormatArgs(lit.Type) || !lit.Type.HasTypeParam() {
		return lit
	}
	scope := env.rootScope()
	if scope == nil {
		return lit
	}
	return ir.FormatArgsLit(scope, lit.Type.Substitute(env.types), lit)
}
