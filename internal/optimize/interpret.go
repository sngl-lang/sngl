package optimize

import (
	"git.duckfam.us/jonathan/sngl/internal/interp"
	"git.duckfam.us/jonathan/sngl/ir"
)

// maxInterpDepth bounds recursion in the interpreter adapter. Excess
// depth makes the fold fail silently — the original ir.Call is preserved.
const maxInterpDepth = 256

// irFromValue converts a Go-side runtime value (as produced by the
// interp package) back into an IR expression. Primitives go through
// irLiteral; maps become StructLits; slices become ListLits.
//
// When typ is non-nil and represents a known struct type, the returned
// StructLit carries Type+Def matching it. Otherwise, the StructLit is
// "naked" (Def=nil); downstream consumers that need the Def must look
// it up themselves.
func irFromValue(val any, typ *ir.Type) ir.Expr {
	switch v := val.(type) {
	case nil:
		return &ir.Literal{Type: ir.TypNull, Raw: "null"}
	case bool, int, float64, string:
		return irLiteral(v, typ)
	case map[string]any:
		fields := make([]ir.FieldInit, 0, len(v))
		for _, k := range sortedMapKeys(v) {
			fields = append(fields, ir.FieldInit{
				Name:  k,
				Value: irFromValue(v[k], nil),
			})
		}
		sl := &ir.StructLit{
			Type:   typ,
			Fields: fields,
		}
		if typ != nil && typ.Kind == ir.TypeStruct {
			if sd, ok := typ.Decl.(*ir.StructDef); ok {
				sl.Def = sd
			}
		}
		return sl
	case []any:
		elems := make([]ir.Expr, 0, len(v))
		for _, e := range v {
			elems = append(elems, irFromValue(e, nil))
		}
		return &ir.ListLit{Type: typ, Elems: elems}
	}
	return nil
}

// interpretFunc runs fn's body via internal/interp with args bound to its
// params. Returns (value, true) on success; (nil, false) when the
// interpretation can't proceed cleanly (non-pure func, depth exceeded,
// arity mismatch, or any error from interp). Composite arg values are
// deep-copied at param binding so callee mutations cannot corrupt cached
// const values in ctx.values.
func interpretFunc(fn *ir.Func, args []any, ctx *evalCtx, depth int) (any, bool) {
	if fn == nil || len(fn.Block) == 0 || fn.Purity != ir.PurityPure {
		return nil, false
	}
	if depth >= maxInterpDepth {
		return nil, false
	}
	if len(args) != len(fn.Params) {
		return nil, false
	}

	var (
		env *interp.Env
		err error
	)
	if ctx != nil && ctx.pkg != nil {
		env, err = interp.BuildEnv(ctx.pkg, "")
		if err != nil {
			return nil, false
		}
	} else {
		env = interp.NewEnv()
	}

	copiedArgs := make([]any, len(args))
	for i, a := range args {
		copiedArgs[i] = deepCopyValue(a)
	}

	result, err := env.CallUserFuncValues(fn, copiedArgs)
	if err != nil {
		return nil, false
	}
	return result, true
}

// deepCopyValue clones composite values (map[string]any, []any) so callee
// mutations during interp evaluation can't corrupt cached const-eval
// results held by the optimizer in ctx.values.
func deepCopyValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		c := make(map[string]any, len(x))
		for k, val := range x {
			c[k] = deepCopyValue(val)
		}
		return c
	case []any:
		c := make([]any, len(x))
		for i, val := range x {
			c[i] = deepCopyValue(val)
		}
		return c
	}
	return v
}

// sortedMapKeys returns the keys of m sorted alphabetically.
func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}
