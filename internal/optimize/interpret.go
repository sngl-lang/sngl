package optimize

import (
	"duckfam.us/sngl/internal/interp"
	"duckfam.us/sngl/ir"
)

// maxInterpDepth bounds recursion in the interpreter adapter. Excess
// depth makes the fold fail silently — the original ir.Call is preserved.
const maxInterpDepth = 256

// irFromValue converts a Go-side runtime value (as produced by the interp
// package) back into an IR expression. Primitives go through irLiteral; struct
// values become StructLits, maps MapLitIRs, slices ListLits.
//
// typ is the type of the position the expression is going into. A value that
// says what it is outranks it — a struct held in a `dyn` field is still that
// struct, and Go names it by its own type — while a value that does not (a
// bare slice or map) takes the position's, so a list of Item read through a
// name declared list<dyn> is still emitted as []any.
//
// What the value carries beyond its type — the declaration, and the order its
// fields were checked in — typ cannot supply at all, and is kept.
func irFromValue(val any, typ *ir.Type) ir.Expr {
	if m := enumMemberIdent(val, typ); m != nil {
		return m
	}
	switch v := val.(type) {
	case nil:
		return &ir.Literal{Type: ir.TypNull, Value: "null"}
	case bool, int, uint64, float64, string:
		return irLiteral(v, typ)
	case *interp.Struct:
		sd, t := v.Def, v.Type
		if sd == nil {
			sd = expectedStructDef(typ)
		}
		if t == nil {
			t = typ
		}
		fields := make([]ir.FieldInit, 0, len(v.Fields))
		for _, f := range v.Fields {
			fields = append(fields, ir.FieldInit{
				Name:  f.Name,
				Value: irFromValue(f.Value, structFieldType(sd, f.Name)),
			})
		}
		return &ir.StructLit{Type: t, Def: sd, Fields: fields}
	case map[string]any:
		var entries []ir.MapEntry
		for _, k := range sortedMapKeys(v) {
			var valType *ir.Type
			if typ != nil && typ.Kind == ir.TypeMap && len(typ.Elems) == 2 {
				valType = typ.Elems[1]
			}
			entries = append(entries, ir.MapEntry{
				Key:   &ir.Literal{Type: ir.TypString, Value: k},
				Value: irFromValue(v[k], valType),
			})
		}
		return &ir.MapLitIR{Type: typ, Entries: entries}
	case []any:
		var elemType *ir.Type
		if typ != nil && (typ.Kind == ir.TypeList || typ.Kind == ir.TypeIter) && len(typ.Elems) > 0 {
			elemType = typ.Elems[0]
		}
		elems := make([]ir.Expr, 0, len(v))
		for _, e := range v {
			elems = append(elems, irFromValue(e, elemType))
		}
		// A folded sequence lands in an iter<T> position, and a list literal
		// is not one: an iter<T> is a pull sequence in every backend that
		// spells it. So the literal keeps its own type and takes the same
		// conversion the checker inserts for a list written there by hand.
		if typ != nil && typ.Kind == ir.TypeIter && len(typ.Elems) == 1 {
			lit := &ir.ListLit{Type: ir.ListOf(typ.Elems[0]), Elems: elems}
			return &ir.Conversion{Type: typ, Operand: lit}
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
	if fn == nil || len(fn.Block) == 0 || fn.Purity != ir.PurityPure || !hasWrittenBody(fn) {
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
	// A context read answers whatever provider the call runs under, which the
	// interpreter here cannot see -- it would answer the default. Once
	// passContext has made the value a parameter, the call folds from its
	// argument.
	var memo map[*ir.Func]bool
	if ctx != nil {
		memo = ctx.readsCtx
	}
	if readsContext(fn, memo) {
		return nil, false
	}
	if depth >= maxInterpDepth {
		return nil, false
	}
	if len(args) != len(fn.Params) {
		return nil, false
	}
	// A body that reads the host is a producer: stored, gated, recorded.
	if ctx != nil && ctx.host != nil && reachesHost(fn, ctx.hostMemo) {
		return ctx.produce(fn, args)
	}

	var (
		env *interp.Env
		err error
	)
	if ctx != nil && ctx.pkg != nil {
		env, err = ctx.interpEnv()
		if err != nil {
			return nil, false
		}
	} else {
		env = interp.NewEnv()
	}

	copiedArgs := make([]any, len(args))
	for i, a := range args {
		copiedArgs[i] = interp.CloneValue(a)
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
// Env-impure intrinsics (i18n.DefaultLocale, file.Pick, file.PickFolder) need
// no blocklist: the checker propagates their PurityReadonly mark up through
// stdlib wrappers, and the evalCall purity gate rejects them on that.
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
			// Whatever declared it: an unresolved scheme import (no Func), a
			// foreign path, or a native mark. The path test alone missed the
			// last of those whenever the mark carries no module --
			// `#[kt.native("androidx.compose.ui.graphics.Color", "")]` names a
			// class the emitter already imports -- so this folder ran the body
			// anyway, got nothing back from the native, and folded the call to
			// its return type's zero. That is how every constant-coloured
			// Compose shape became `drawRect(color = null, ...)`, which is no
			// Kotlin that compiles.
			if x.Func == nil || x.Func.Foreign.Path != "" || isUnfoldableNative(x.Func) {
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
			for _, s := range x.Else {
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

// readsContext reports whether fn's body, or any function it reaches, reads a
// context: through a call, a function named as a value (`xs.map(show)`), or
// the boundary or window handler a call's raise is caught by.
//
// memo may be nil. A false answer is recorded for every function the walk
// visited, since a walk that found nothing explored each of them whole; a true
// one only for fn, since the walk stopped partway.
func readsContext(fn *ir.Func, memo map[*ir.Func]bool) bool {
	if v, ok := memo[fn]; ok {
		return v
	}
	seen := map[*ir.Func]bool{}
	found := readsContextWalk(fn, seen, memo)
	if memo != nil {
		if found {
			memo[fn] = true
		} else {
			for f := range seen {
				memo[f] = false
			}
		}
	}
	return found
}

func readsContextWalk(fn *ir.Func, seen, memo map[*ir.Func]bool) bool {
	if fn == nil || seen[fn] {
		return false
	}
	if v, ok := memo[fn]; ok {
		return v
	}
	seen[fn] = true
	found := false
	var visit func(ir.Node) error
	visit = func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.ContextRead:
			found = true
		case *ir.Ident:
			if f, ok := x.Sym.(*ir.Func); ok {
				found = readsContextWalk(f, seen, memo)
			}
		case *ir.Call:
			found = readsContextWalk(x.Func, seen, memo)
			if h := ir.CatchingHandler(x); !found && h != nil && h != x.ErrorHandler && h.Func != nil {
				_ = ir.Walk(h.Func.Block, visit)
			}
		}
		if found {
			return ir.SkipAll
		}
		return nil
	}
	_ = ir.Walk(fn.Block, visit)
	return found
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

// enumMemberIdent rebuilds the member reference an enum-typed value stands
// for. A member is its name in the value model, so the declaration the
// position names is what says which member that is. A name that matches none
// is not a member of this enum and stays the string it is.
func enumMemberIdent(val any, typ *ir.Type) ir.Expr {
	name, ok := val.(string)
	if !ok || typ == nil || typ.Kind != ir.TypeEnum {
		return nil
	}
	ed, _ := typ.Decl.(*ir.EnumDef)
	if ed == nil {
		return nil
	}
	for _, m := range ed.Members {
		if m.Name == name {
			return &ir.Ident{Type: typ, Name: m.Name, Member: m.Name}
		}
	}
	return nil
}

// expectedStructDef returns the struct declaration t names, or nil.
func expectedStructDef(t *ir.Type) *ir.StructDef {
	if t == nil || t.Kind != ir.TypeStruct {
		return nil
	}
	sd, _ := t.Decl.(*ir.StructDef)
	return sd
}

// structFieldType returns the declared type of field name on sd, or nil when
// sd is nil or the field isn't found. Used by irFromValue to keep per-field
// type info when reconstructing folded composites.
func structFieldType(sd *ir.StructDef, name string) *ir.Type {
	if sd == nil {
		return nil
	}
	for _, f := range sd.Fields {
		if f.Name == name {
			return f.Type
		}
	}
	return nil
}

// interpEnv is the environment a pure func is interpreted in: ctx.pkg's consts,
// evaluated. Built once per package per run and shared, because building it
// evaluates every const's initializer and a highlighted code block interprets
// a token's colour once per token -- 160000 builds of a site's search index.
// A call runs in a snapshot of it (CallUserFuncValues), so none writes it.
func (ctx *evalCtx) interpEnv() (*interp.Env, error) {
	if ctx.interpEnvs == nil {
		return interp.BuildEnv(ctx.pkg, "")
	}
	if env, ok := ctx.interpEnvs[ctx.pkg]; ok {
		return env, nil
	}
	env, err := interp.BuildEnv(ctx.pkg, "")
	if err != nil {
		return nil, err
	}
	if ctx.host != nil {
		env.SetBuildHost(ctx.host)
	}
	ctx.interpEnvs[ctx.pkg] = env
	return env, nil
}
