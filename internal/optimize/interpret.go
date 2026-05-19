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
	// Refuse to fold any function whose body transitively calls a native
	// (unresolved) function. Natives like intl.DefaultLocale() depend on
	// the runtime environment (host locale, file system state) and must
	// not be evaluated at compile time even though interp can execute them
	// against the build host.
	if bodyUsesNativeCall(fn) {
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

// bodyUsesNativeCall reports whether fn's body contains any unresolved
// call expression. The checker leaves Call.Func == nil for native/import
// invocations (their resolution lives in NativeImport, not pkg.Funcs);
// such calls escape the compile-time interpreter so we refuse to fold any
// function that transitively contains one.
//
// Env-impure intrinsics (i18n.DefaultLocale, file.Pick, file.PickFolder)
// used to be blocklisted here; that workaround is no longer needed because
// the checker now propagates their PurityReadonly mark up through stdlib
// wrappers, which the evalCall purity gate naturally rejects.
func bodyUsesNativeCall(fn *ir.Func) bool {
	var hasNative bool
	var walkExpr func(e ir.Expr)
	var walkStmt func(s ir.Stmt)
	walkExpr = func(e ir.Expr) {
		if hasNative || e == nil {
			return
		}
		switch x := e.(type) {
		case *ir.Call:
			if x.Func == nil || x.Func.NativePkg != "" {
				hasNative = true
				return
			}
			if x.Receiver != nil {
				walkExpr(x.Receiver)
			}
			for _, a := range x.Args {
				walkExpr(a.Value)
			}
		case *ir.Binary:
			walkExpr(x.Left)
			walkExpr(x.Right)
		case *ir.Unary:
			walkExpr(x.Operand)
		case *ir.Ternary:
			walkExpr(x.Cond)
			walkExpr(x.Then)
			walkExpr(x.Else)
		case *ir.Conversion:
			walkExpr(x.Operand)
		case *ir.Select:
			walkExpr(x.Operand)
		case *ir.Index:
			walkExpr(x.Operand)
			walkExpr(x.Idx)
		case *ir.ListLit:
			for _, el := range x.Elems {
				walkExpr(el)
			}
		case *ir.StructLit:
			for _, f := range x.Fields {
				walkExpr(f.Value)
			}
		case *ir.MapLitIR:
			for _, e := range x.Entries {
				walkExpr(e.Key)
				walkExpr(e.Value)
			}
		case *ir.Spread:
			walkExpr(x.Operand)
		case *ir.Lambda:
			if x.Func != nil {
				for _, s := range x.Func.Block {
					walkStmt(s)
				}
			}
		}
	}
	walkStmt = func(s ir.Stmt) {
		if hasNative || s == nil {
			return
		}
		switch x := s.(type) {
		case *ir.Return:
			if x.Value != nil {
				walkExpr(x.Value)
			}
		case *ir.Assign:
			walkExpr(x.Target)
			walkExpr(x.Value)
		case *ir.If:
			walkExpr(x.Cond)
			for _, s := range x.Body {
				walkStmt(s)
			}
			for _, s := range x.Else {
				walkStmt(s)
			}
		case *ir.For:
			walkExpr(x.Iter)
			for _, s := range x.Body {
				walkStmt(s)
			}
		case *ir.LocalVar:
			if x.Init != nil {
				walkExpr(x.Init)
			}
		case *ir.CallStmt:
			walkExpr(x.Call)
		}
	}
	for _, s := range fn.Block {
		walkStmt(s)
		if hasNative {
			return true
		}
	}
	return hasNative
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
