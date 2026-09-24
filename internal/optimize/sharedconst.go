package optimize

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

// sharedAggregateConsts is the set of pkg's consts that a reference keeps
// naming rather than copying: a list or a map, whose size is the program's
// data rather than its declaration. A struct is left out because backends read
// record-typed props as literals -- a Style, a Spec, a color -- and a record
// is bounded by the fields it declares.
func sharedAggregateConsts(pkg *ir.Package) map[*ir.Var]bool {
	var out map[*ir.Var]bool
	for _, c := range pkg.Consts {
		if c.Init == nil || c.Builtin != ir.BuiltinNone || c.Type == nil {
			continue
		}
		if c.Type.Kind != ir.TypeList && c.Type.Kind != ir.TypeMap {
			continue
		}
		if out == nil {
			out = map[*ir.Var]bool{}
		}
		out[c] = true
	}
	return out
}

// sharedConstRef returns the shared const e names, looking through the
// conversions a checker inserts around a reference.
func (ctx *evalCtx) sharedConstRef(e ir.Expr) *ir.Var {
	if ctx == nil || len(ctx.sharedConsts) == 0 {
		return nil
	}
	for {
		conv, ok := e.(*ir.Conversion)
		if !ok {
			break
		}
		e = conv.Operand
	}
	id, ok := e.(*ir.Ident)
	if !ok {
		return nil
	}
	v, ok := id.Sym.(*ir.Var)
	if !ok || !ctx.sharedConsts[v] {
		return nil
	}
	return v
}

// keepsReference reports whether e, which evaluated to val, stays as written
// instead of becoming a literal of val.
func (ctx *evalCtx) keepsReference(e ir.Expr, val any) bool {
	if ctx == nil || ctx.copyShared || ctx.sharedConstRef(e) == nil {
		return false
	}
	switch val.(type) {
	case []any, map[string]any:
		return true
	}
	return false
}

// foldOwned folds an expression whose value becomes storage the program may
// write: a var's initializer, an assignment, an argument to a parameter the
// callee writes. A reference to a shared const there would alias the one
// declaration every reader sees, so the value is copied as it always was.
func foldOwned(e ir.Expr, ctx *evalCtx) ir.Expr {
	if ctx == nil || ctx.copyShared {
		return foldExpr(e, ctx)
	}
	ctx.copyShared = true
	defer func() { ctx.copyShared = false }()
	return foldExpr(e, ctx)
}

// isScalar reports whether a value of t is copied wherever it is bound, so
// nothing read out of a shared const through it can alias the const.
func isScalar(t *ir.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case ir.TypeBool, ir.TypeInt, ir.TypeFloat, ir.TypeString, ir.TypeEnum:
		return true
	}
	return false
}

// foldCallArg folds argument i of call.
func foldCallArg(call *ir.Call, i int, ctx *evalCtx) ir.Expr {
	arg := call.Args[i]
	if ctx == nil || ctx.sharedConstRef(arg.Value) == nil || !ctx.writes.callWrites(call, i) {
		return foldExpr(arg.Value, ctx)
	}
	return foldOwned(arg.Value, ctx)
}

// foldPropArg folds the value n passes for prop name.
func foldPropArg(n *ir.NodeInst, name string, value ir.Expr, ctx *evalCtx) ir.Expr {
	if ctx == nil || ctx.sharedConstRef(value) == nil || !ctx.writes.propWrites(n.Component, name) {
		return foldExpr(value, ctx)
	}
	return foldOwned(value, ctx)
}

// writesAnalysis answers whether a callee may write, or hand on to something
// that may write, the value bound to one of its parameters. Memoized per
// declaration and parameter; a question asked again while it is being answered
// -- recursion -- is answered yes.
type writesAnalysis struct {
	funcs map[funcParam]bool
	props map[compProp]bool
	// platform and language are the build's target: a component's body is
	// its own and the override this target picks, not every target's.
	platform, language string
}

type funcParam struct {
	fn *ir.Func
	i  int
}

type compProp struct {
	comp *ir.Component
	name string
}

func newWritesAnalysis(platform, language string) *writesAnalysis {
	return &writesAnalysis{funcs: map[funcParam]bool{}, props: map[compProp]bool{}, platform: platform, language: language}
}

func (w *writesAnalysis) callWrites(call *ir.Call, i int) bool {
	fn := call.Func
	if fn == nil || w == nil {
		return true
	}
	if fn.Intrinsic != "" {
		def, ok := ir.IntrinsicByName(fn.Intrinsic)
		return !ok || def.MutatesReceiver && i == 0
	}
	if fn.Foreign.Name != "" && !fn.Foreign.Marked {
		return true
	}
	p := callParam(call, i)
	if p < 0 {
		return true
	}
	key := funcParam{fn, p}
	if got, ok := w.funcs[key]; ok {
		return got
	}
	w.funcs[key] = true
	param := fn.Params[p]
	got := w.escapes(ir.Body{Stmts: fn.Block}, func(s ir.Symbol) bool { return s == param })
	w.funcs[key] = got
	return got
}

// callParam is the index of the parameter argument i binds, or -1.
func callParam(call *ir.Call, i int) int {
	params := call.Func.Params
	if name := call.Args[i].Name; name != "" {
		for j, p := range params {
			if p.Name == name {
				return j
			}
		}
		return -1
	}
	if i < len(params) {
		return i
	}
	return -1
}

func (w *writesAnalysis) propWrites(comp *ir.Component, name string) bool {
	if comp == nil || w == nil || w.renderedByPlatform(comp) {
		return true
	}
	key := compProp{comp, name}
	if got, ok := w.props[key]; ok {
		return got
	}
	w.props[key] = true
	var sym *ir.Param
	for _, p := range comp.Props {
		if p.Name == name {
			sym = p.Sym
		}
	}
	// An override body binds the prop to a parameter of its own, which is
	// what the name is matched against there.
	isProp := func(s ir.Symbol) bool {
		if sym != nil && s == sym {
			return true
		}
		p, ok := s.(*ir.Param)
		return ok && p.Name == name
	}
	got := w.escapes(ir.Body{Stmts: comp.Body, Vars: comp.Vars, Funcs: comp.Funcs}, isProp)
	if b, ok := ir.ComponentOverride(comp, w.platform, w.language); ok {
		got = got || w.escapes(b, isProp)
	}
	w.props[key] = got
	return got
}

// renderedByPlatform reports whether a node's props are read by a platform
// emitter rather than by a body, which is to say read as literals. A body
// another target overrides the component with is not one this build renders.
func (w *writesAnalysis) renderedByPlatform(comp *ir.Component) bool {
	if comp.Intrinsic != "" || comp.Wildcard != "" || comp.Builtin != ir.BuiltinNone {
		return true
	}
	if len(comp.Body) > 0 {
		return false
	}
	_, ok := ir.ComponentOverride(comp, w.platform, w.language)
	return !ok
}

// escapes reports whether root writes the value a matching symbol holds, or
// puts it anywhere a write could reach it: a binding, a returned value, an
// aggregate, or a parameter that itself escapes.
func (w *writesAnalysis) escapes(body ir.Body, match func(ir.Symbol) bool) bool {
	bare := func(e ir.Expr) bool {
		for {
			conv, ok := e.(*ir.Conversion)
			if !ok {
				break
			}
			e = conv.Operand
		}
		id, ok := e.(*ir.Ident)
		return ok && match(id.Sym)
	}
	rooted := func(e ir.Expr) bool {
		for {
			switch x := e.(type) {
			case *ir.Select:
				e = x.Operand
				continue
			case *ir.Index:
				e = x.Operand
				continue
			case *ir.Unary:
				e = x.Operand
				continue
			case *ir.Conversion:
				e = x.Operand
				continue
			case *ir.Ident:
				return match(x.Sym)
			}
			return false
		}
	}
	found := false
	visit := func(node ir.Node) error {
		if found {
			return nil
		}
		switch n := node.(type) {
		case *ir.Assign:
			found = rooted(n.Target) || bare(n.Value)
		case *ir.Toggle:
			found = rooted(n.Target)
		case *ir.LocalVar:
			found = n.Init != nil && bare(n.Init)
		case *ir.Return:
			found = n.Value != nil && bare(n.Value)
		case *ir.For:
			found = n.RefElem && n.Iter != nil && rooted(n.Iter)
		case *ir.Emit:
			for _, a := range n.Args {
				found = found || bare(a.Value)
			}
		case *ir.ContextProvider:
			found = n.Value != nil && bare(n.Value)
		case *ir.ListLit:
			for _, el := range n.Elems {
				found = found || bare(el)
			}
		case *ir.StructLit:
			for _, f := range n.Fields {
				found = found || f.Value != nil && bare(f.Value)
			}
		case *ir.MapLitIR:
			for _, e := range n.Entries {
				found = found || bare(e.Value)
			}
		case *ir.Spread:
			found = bare(n.Operand)
		case *ir.Call:
			if def, ok := intrinsicDef(n); ok && def.MutatesReceiver && len(n.Args) > 0 && rooted(n.Args[0].Value) {
				found = true
				break
			}
			for i, a := range n.Args {
				if bare(a.Value) && w.callWrites(n, i) {
					found = true
					break
				}
			}
		case *ir.NodeInst:
			for _, p := range n.Props {
				if p.Value != nil && bare(p.Value) && w.propWrites(n.Component, p.Name) {
					found = true
					break
				}
			}
		}
		return nil
	}
	_ = ir.Walk(body.Stmts, visit)
	for _, v := range body.Vars {
		if v.Init != nil {
			found = found || bare(v.Init)
			_ = ir.Walk(v.Init, visit)
		}
		for _, h := range v.Handlers {
			_ = ir.Walk(h.Func, visit)
		}
	}
	for _, f := range body.Funcs {
		_ = ir.Walk(f, visit)
	}
	return found
}

func intrinsicDef(call *ir.Call) (ir.IntrinsicDef, bool) {
	if call.Func == nil || call.Func.Intrinsic == "" {
		return ir.IntrinsicDef{}, false
	}
	return ir.IntrinsicByName(call.Func.Intrinsic)
}
