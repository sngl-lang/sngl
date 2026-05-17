package lower

import (
	"maps"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passNoContext lowers context declarations to hidden component/func props.
// It must run BEFORE passInlinePure (provider rewrite assumes un-inlined
// component boundaries) and BEFORE passReactivity (synthesized props must
// be visible as reactive deps).
var passNoContext = pass{
	name:    "NoContext",
	enabled: func(c Caps) bool { return c.NoContext },
	apply:   applyNoContext,
}

// applyNoContext is the entry point for the NoContext lowering pass.
//
// Algorithm:
//  1. Compute per-context reachability over the call graph for both
//     components and funcs.
//  2. Add a synthesized __ctx_<name> prop/param to every component or
//     func in Reach(ctx).
//  3. Rewrite *ir.ContextRead{Ref: ctx} inside those bodies into reads of
//     the corresponding hidden prop/param (as *ir.Ident with *ir.Param Sym).
//  4. Rewrite *ir.ContextProvider nodes: splice out the provider, thread
//     __ctx_<name>=value as an extra Arg onto every reachable NodeInst /
//     Call inside.
//  5. Seed window roots with ctx.Default so consumers without an enclosing
//     provider receive the default value.
//  6. Clear pkg.Contexts (no longer needed by codegen).
func applyNoContext(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	if len(pkg.Contexts) == 0 {
		return nil
	}

	// Collect all funcs reachable from user code that have a SNGL body —
	// includes stdlib wrappers (e.g. i18n.tr, i18n.numberInt) which live
	// outside pkg.Funcs but are referenced from user code via Call.Func
	// pointers. NoContext mutates these in place so ContextRead nodes
	// inside their bodies get rewritten to hidden-param reads and the
	// optimizer's inlineCall can splice them at call sites.
	extra := collectReachableExternalFuncs(pkg)

	reach := computeReachability(pkg, extra)
	addHiddenParams(pkg, reach, extra)
	hiddenParams := buildHiddenParamIndex(pkg, reach, extra)
	rewriteReads(pkg, reach, extra, hiddenParams)
	lowerProviders(pkg, reach, extra, hiddenParams)

	pkg.Contexts = nil
	return nil
}

// collectReachableExternalFuncs walks the user package and returns all
// *ir.Func pointers that are referenced from user code AND have a SNGL
// body AND are not already in pkg.Funcs / pkg.Components funcs. These
// are predominantly stdlib expression-body wrappers (i18n.tr etc.).
// The walk is transitive: a stdlib wrapper that calls another stdlib
// wrapper is also collected.
func collectReachableExternalFuncs(pkg *ir.Package) []*ir.Func {
	known := map[*ir.Func]bool{}
	for _, fn := range pkg.Funcs {
		known[fn] = true
	}
	for _, comp := range pkg.Components {
		for _, fn := range comp.Funcs {
			known[fn] = true
		}
	}

	seen := map[*ir.Func]bool{}
	var queue []*ir.Func

	visit := func(fn *ir.Func) {
		if fn == nil || known[fn] || seen[fn] {
			return
		}
		if !hasBody(fn) {
			return
		}
		seen[fn] = true
		queue = append(queue, fn)
	}

	seedFromExpr := func(e ir.Expr) {
		for _, call := range callsInExpr(e, nil) {
			if call.fn != nil {
				visit(call.fn)
			}
		}
	}
	seedFromStmts := func(stmts []ir.Stmt) {
		for _, call := range callsInBody(stmts, nil) {
			if call.fn != nil {
				visit(call.fn)
			}
		}
	}
	seedFromVar := func(v *ir.Var) {
		seedFromExpr(v.Init)
		for _, h := range v.Handlers {
			if h.Func != nil {
				seedFromStmts(h.Func.Block)
			}
		}
	}

	// Seed from user package: walk every place an expression or stmt can
	// host a Call — component bodies, component Vars (Init + Handlers),
	// component Funcs, component Timers, window bodies, package funcs and
	// vars. callsInBody alone misses comp.Vars / comp.Funcs because Vars
	// aren't part of Body — they hang off the Component struct directly.
	for _, comp := range pkg.Components {
		seedFromStmts(comp.Body)
		for _, v := range comp.Vars {
			seedFromVar(v)
		}
		for _, fn := range comp.Funcs {
			if hasBody(fn) {
				seedFromStmts(fn.Block)
			}
		}
		for _, t := range comp.Timers {
			if t.Handler != nil {
				seedFromStmts(t.Handler.Block)
			}
		}
	}
	for _, fn := range pkg.Funcs {
		if !hasBody(fn) {
			continue
		}
		seedFromStmts(fn.Block)
	}
	for _, w := range pkg.Windows {
		seedFromStmts(w.Body)
		for _, v := range w.Vars {
			seedFromVar(v)
		}
		for _, fn := range w.Funcs {
			if hasBody(fn) {
				seedFromStmts(fn.Block)
			}
		}
	}
	for _, v := range pkg.Vars {
		seedFromVar(v)
	}

	// Transitive closure over external funcs.
	for i := 0; i < len(queue); i++ {
		fn := queue[i]
		for _, call := range callsInBody(fn.Block, nil) {
			if call.fn != nil {
				visit(call.fn)
			}
		}
	}
	return queue
}

// --- Reachability ---

// Reachable records, for each Context, the set of Components and Funcs that
// transitively read that context (without an intervening provider shadowing
// it).
type Reachable struct {
	Components map[*ir.Context]map[*ir.Component]bool
	Funcs      map[*ir.Context]map[*ir.Func]bool
}

// callEdge records one call site inside a caller's body, together with the
// set of contexts that are shadowed at that call site by an enclosing
// ContextProvider. Exactly one of comp / fn is non-nil.
type callEdge struct {
	comp     *ir.Component
	fn       *ir.Func
	shadowed map[*ir.Context]bool
}

// hasBody reports whether a func has a body that NoContext should walk.
// Intrinsics and native imports have no SNGL body to inspect or rewrite.
func hasBody(f *ir.Func) bool {
	if f == nil {
		return false
	}
	if f.Intrinsic != "" || f.NativeName != "" || f.NativePkg != "" {
		return false
	}
	return len(f.Block) > 0
}

// computeReachability returns, for each Context, the sets of Components and
// Funcs that transitively read that context (without an intervening provider
// shadowing it). extraFuncs are external (stdlib) funcs with bodies that are
// reachable from user code and should participate in reachability propagation.
func computeReachability(pkg *ir.Package, extraFuncs []*ir.Func) Reachable {
	reach := Reachable{
		Components: make(map[*ir.Context]map[*ir.Component]bool, len(pkg.Contexts)),
		Funcs:      make(map[*ir.Context]map[*ir.Func]bool, len(pkg.Contexts)),
	}
	for _, ctx := range pkg.Contexts {
		reach.Components[ctx] = make(map[*ir.Component]bool)
		reach.Funcs[ctx] = make(map[*ir.Func]bool)
	}

	// 1. Mark direct readers.
	for _, comp := range pkg.Components {
		for ctx := range directContextReads(comp.Body) {
			reach.Components[ctx][comp] = true
		}
	}
	markFuncReads := func(fn *ir.Func) {
		if !hasBody(fn) {
			return
		}
		for ctx := range directContextReads(fn.Block) {
			reach.Funcs[ctx][fn] = true
		}
	}
	for _, fn := range pkg.Funcs {
		markFuncReads(fn)
	}
	for _, fn := range extraFuncs {
		markFuncReads(fn)
	}

	// Helper: collect all call edges from a component's full surface area
	// (Body, Vars.Init+Handlers, Funcs.Block, Timers.Handler.Block). Calls
	// inside Vars/Funcs/Timers aren't bounded by component-Body
	// ContextProviders, so shadow is always empty for those edges.
	componentCalls := func(comp *ir.Component) []callEdge {
		var calls []callEdge
		calls = append(calls, callsInBody(comp.Body, nil)...)
		for _, v := range comp.Vars {
			calls = append(calls, callsInExpr(v.Init, nil)...)
			for _, h := range v.Handlers {
				if h.Func != nil {
					calls = append(calls, callsInBody(h.Func.Block, nil)...)
				}
			}
		}
		for _, fn := range comp.Funcs {
			if hasBody(fn) {
				calls = append(calls, callsInBody(fn.Block, nil)...)
			}
		}
		for _, t := range comp.Timers {
			if t.Handler != nil {
				calls = append(calls, callsInBody(t.Handler.Block, nil)...)
			}
		}
		return calls
	}
	// Direct ContextReads can also live in Vars/Funcs/Timers — mark those.
	compDirectReads := func(comp *ir.Component) map[*ir.Context]bool {
		out := map[*ir.Context]bool{}
		collectContextReadsInStmts(comp.Body, out)
		for _, v := range comp.Vars {
			collectContextReadsInExpr(v.Init, out)
			for _, h := range v.Handlers {
				if h.Func != nil {
					collectContextReadsInStmts(h.Func.Block, out)
				}
			}
		}
		for _, fn := range comp.Funcs {
			if hasBody(fn) {
				collectContextReadsInStmts(fn.Block, out)
			}
		}
		for _, t := range comp.Timers {
			if t.Handler != nil {
				collectContextReadsInStmts(t.Handler.Block, out)
			}
		}
		return out
	}
	// Re-mark direct-readers using the wider surface.
	for _, comp := range pkg.Components {
		for ctx := range compDirectReads(comp) {
			reach.Components[ctx][comp] = true
		}
	}

	// 2. Fixpoint: propagate through call graph, respecting shadowing.
	propagateFuncCaller := func(caller *ir.Func) bool {
		if !hasBody(caller) {
			return false
		}
		changed := false
		for _, call := range callsInBody(caller.Block, nil) {
			for _, ctx := range pkg.Contexts {
				if call.shadowed[ctx] {
					continue
				}
				if call.fn != nil && reach.Funcs[ctx][call.fn] && !reach.Funcs[ctx][caller] {
					reach.Funcs[ctx][caller] = true
					changed = true
				}
				if call.comp != nil && reach.Components[ctx][call.comp] && !reach.Funcs[ctx][caller] {
					reach.Funcs[ctx][caller] = true
					changed = true
				}
			}
		}
		return changed
	}
	changed := true
	for changed {
		changed = false
		// Component callers — walk the component's full surface (Body +
		// Vars + Funcs + Timers).
		for _, caller := range pkg.Components {
			for _, call := range componentCalls(caller) {
				for _, ctx := range pkg.Contexts {
					if call.shadowed[ctx] {
						continue
					}
					if call.comp != nil && reach.Components[ctx][call.comp] && !reach.Components[ctx][caller] {
						reach.Components[ctx][caller] = true
						changed = true
					}
					if call.fn != nil && reach.Funcs[ctx][call.fn] && !reach.Components[ctx][caller] {
						reach.Components[ctx][caller] = true
						changed = true
					}
				}
			}
		}
		// Func callers (user + extras).
		for _, caller := range pkg.Funcs {
			if propagateFuncCaller(caller) {
				changed = true
			}
		}
		for _, caller := range extraFuncs {
			if propagateFuncCaller(caller) {
				changed = true
			}
		}
	}
	return reach
}

// directContextReads returns the set of contexts directly read in stmts
// (not counting transitive calls).
func directContextReads(stmts []ir.Stmt) map[*ir.Context]bool {
	result := map[*ir.Context]bool{}
	collectContextReadsInStmts(stmts, result)
	return result
}

func collectContextReadsInStmts(stmts []ir.Stmt, out map[*ir.Context]bool) {
	for _, s := range stmts {
		collectContextReadsInStmt(s, out)
	}
}

func collectContextReadsInStmt(s ir.Stmt, out map[*ir.Context]bool) {
	switch n := s.(type) {
	case *ir.NodeInst:
		for _, p := range n.Props {
			collectContextReadsInExpr(p.Value, out)
		}
		collectContextReadsInStmts(n.Children, out)
		for _, h := range n.Handlers {
			if h.Func != nil {
				collectContextReadsInStmts(h.Func.Block, out)
			}
		}
	case *ir.If:
		collectContextReadsInExpr(n.Cond, out)
		collectContextReadsInStmts(n.Body, out)
		collectContextReadsInStmts(n.Else, out)
	case *ir.For:
		collectContextReadsInExpr(n.Iter, out)
		collectContextReadsInStmts(n.Body, out)
		collectContextReadsInStmts(n.Else, out)
	case *ir.ContextProvider:
		// Do NOT descend into provider's children for read collection:
		// those reads are shielded by this provider's value. Only collect
		// reads from the value expression itself (which is in the outer scope).
		collectContextReadsInExpr(n.Value, out)
	case *ir.Assign:
		collectContextReadsInExpr(n.Target, out)
		collectContextReadsInExpr(n.Value, out)
	case *ir.LocalVar:
		collectContextReadsInExpr(n.Init, out)
	case *ir.Return:
		collectContextReadsInExpr(n.Value, out)
	case *ir.CallStmt:
		if n.Call != nil {
			collectContextReadsInExpr(n.Call.Receiver, out)
			for _, a := range n.Call.Args {
				collectContextReadsInExpr(a.Value, out)
			}
		}
	case *ir.Emit:
		for _, a := range n.Args {
			collectContextReadsInExpr(a.Value, out)
		}
	case *ir.PlatformFilter:
		collectContextReadsInStmts(n.Body, out)
	case *ir.SlotInst:
		collectContextReadsInStmts(n.Children, out)
	case *ir.ErrorBoundary:
		collectContextReadsInStmts(n.Children, out)
	}
}

func collectContextReadsInExpr(e ir.Expr, out map[*ir.Context]bool) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ir.ContextRead:
		out[n.Ref] = true
	case *ir.Binary:
		collectContextReadsInExpr(n.Left, out)
		collectContextReadsInExpr(n.Right, out)
	case *ir.Unary:
		collectContextReadsInExpr(n.Operand, out)
	case *ir.Ternary:
		collectContextReadsInExpr(n.Cond, out)
		collectContextReadsInExpr(n.Then, out)
		collectContextReadsInExpr(n.Else, out)
	case *ir.Select:
		collectContextReadsInExpr(n.Operand, out)
	case *ir.Index:
		collectContextReadsInExpr(n.Operand, out)
		collectContextReadsInExpr(n.Idx, out)
	case *ir.Call:
		collectContextReadsInExpr(n.Receiver, out)
		collectContextReadsInExpr(n.Callee, out)
		for _, a := range n.Args {
			collectContextReadsInExpr(a.Value, out)
		}
	case *ir.Conversion:
		collectContextReadsInExpr(n.Operand, out)
	case *ir.StructLit:
		for _, f := range n.Fields {
			collectContextReadsInExpr(f.Value, out)
		}
	case *ir.ListLit:
		for _, el := range n.Elems {
			collectContextReadsInExpr(el, out)
		}
	case *ir.MapLitIR:
		for _, entry := range n.Entries {
			collectContextReadsInExpr(entry.Key, out)
			collectContextReadsInExpr(entry.Value, out)
		}
	case *ir.Spread:
		collectContextReadsInExpr(n.Operand, out)
	}
}

// callsInBody returns all call sites (component instantiations + func calls)
// reachable from stmts, each annotated with the set of contexts shadowed at
// that site by enclosing ContextProvider nodes. shadow is the current shadow
// set (copied on descent).
func callsInBody(stmts []ir.Stmt, shadow map[*ir.Context]bool) []callEdge {
	var calls []callEdge
	for _, s := range stmts {
		calls = append(calls, callsInStmt(s, shadow)...)
	}
	return calls
}

func callsInStmt(s ir.Stmt, shadow map[*ir.Context]bool) []callEdge {
	switch n := s.(type) {
	case *ir.NodeInst:
		var calls []callEdge
		if n.Component != nil {
			calls = append(calls, callEdge{comp: n.Component, shadowed: shadow})
		}
		for _, p := range n.Props {
			calls = append(calls, callsInExpr(p.Value, shadow)...)
		}
		calls = append(calls, callsInBody(n.Children, shadow)...)
		for _, h := range n.Handlers {
			if h.Func != nil {
				calls = append(calls, callsInBody(h.Func.Block, shadow)...)
			}
		}
		return calls
	case *ir.ContextProvider:
		inner := copyContextShadow(shadow)
		inner[n.Ref] = true
		calls := callsInExpr(n.Value, shadow)
		calls = append(calls, callsInBody(n.Children, inner)...)
		return calls
	case *ir.If:
		calls := callsInExpr(n.Cond, shadow)
		calls = append(calls, callsInBody(n.Body, shadow)...)
		calls = append(calls, callsInBody(n.Else, shadow)...)
		return calls
	case *ir.For:
		calls := callsInExpr(n.Iter, shadow)
		calls = append(calls, callsInBody(n.Body, shadow)...)
		calls = append(calls, callsInBody(n.Else, shadow)...)
		return calls
	case *ir.PlatformFilter:
		return callsInBody(n.Body, shadow)
	case *ir.SlotInst:
		return callsInBody(n.Children, shadow)
	case *ir.ErrorBoundary:
		return callsInBody(n.Children, shadow)
	case *ir.Assign:
		calls := callsInExpr(n.Target, shadow)
		calls = append(calls, callsInExpr(n.Value, shadow)...)
		return calls
	case *ir.LocalVar:
		return callsInExpr(n.Init, shadow)
	case *ir.Return:
		return callsInExpr(n.Value, shadow)
	case *ir.CallStmt:
		if n.Call != nil {
			return callsInExpr(n.Call, shadow)
		}
	case *ir.Emit:
		var calls []callEdge
		for _, a := range n.Args {
			calls = append(calls, callsInExpr(a.Value, shadow)...)
		}
		return calls
	}
	return nil
}

func callsInExpr(e ir.Expr, shadow map[*ir.Context]bool) []callEdge {
	if e == nil {
		return nil
	}
	var calls []callEdge
	switch n := e.(type) {
	case *ir.Call:
		if n.Func != nil {
			calls = append(calls, callEdge{fn: n.Func, shadowed: shadow})
		}
		calls = append(calls, callsInExpr(n.Receiver, shadow)...)
		calls = append(calls, callsInExpr(n.Callee, shadow)...)
		for _, a := range n.Args {
			calls = append(calls, callsInExpr(a.Value, shadow)...)
		}
	case *ir.Binary:
		calls = append(calls, callsInExpr(n.Left, shadow)...)
		calls = append(calls, callsInExpr(n.Right, shadow)...)
	case *ir.Unary:
		calls = append(calls, callsInExpr(n.Operand, shadow)...)
	case *ir.Ternary:
		calls = append(calls, callsInExpr(n.Cond, shadow)...)
		calls = append(calls, callsInExpr(n.Then, shadow)...)
		calls = append(calls, callsInExpr(n.Else, shadow)...)
	case *ir.Select:
		calls = append(calls, callsInExpr(n.Operand, shadow)...)
	case *ir.Index:
		calls = append(calls, callsInExpr(n.Operand, shadow)...)
		calls = append(calls, callsInExpr(n.Idx, shadow)...)
	case *ir.Conversion:
		calls = append(calls, callsInExpr(n.Operand, shadow)...)
	case *ir.StructLit:
		for _, f := range n.Fields {
			calls = append(calls, callsInExpr(f.Value, shadow)...)
		}
	case *ir.ListLit:
		for _, el := range n.Elems {
			calls = append(calls, callsInExpr(el, shadow)...)
		}
	case *ir.MapLitIR:
		for _, entry := range n.Entries {
			calls = append(calls, callsInExpr(entry.Key, shadow)...)
			calls = append(calls, callsInExpr(entry.Value, shadow)...)
		}
	case *ir.Spread:
		calls = append(calls, callsInExpr(n.Operand, shadow)...)
	}
	return calls
}

func copyContextShadow(m map[*ir.Context]bool) map[*ir.Context]bool {
	out := make(map[*ir.Context]bool, len(m)+1)
	maps.Copy(out, m)
	return out
}

// --- Hidden params ---

// addHiddenParams adds a synthesized __ctx_<name> prop to every component
// in Reach(ctx) and a __ctx_<name> param to every func in Reach(ctx) for
// each context. Idempotent: skips if already present. extraFuncs are
// external (stdlib) funcs that should also be augmented.
func addHiddenParams(pkg *ir.Package, reach Reachable, extraFuncs []*ir.Func) {
	for _, ctx := range pkg.Contexts {
		paramName := "__ctx_" + ctx.Name
		for _, comp := range pkg.Components {
			if !reach.Components[ctx][comp] {
				continue
			}
			if hasHiddenProp(comp, paramName) {
				continue
			}
			comp.Props = append(comp.Props, &ir.Prop{
				Name: paramName,
				Type: ctx.Typ,
			})
		}
		addToFunc := func(fn *ir.Func) {
			if !reach.Funcs[ctx][fn] {
				return
			}
			if hasFuncParam(fn, paramName) {
				return
			}
			fn.Params = append(fn.Params, &ir.Param{
				Name: paramName,
				Type: ctx.Typ,
			})
		}
		for _, fn := range pkg.Funcs {
			addToFunc(fn)
		}
		for _, fn := range extraFuncs {
			addToFunc(fn)
		}
	}
}

// hiddenParamIndex maps (ctx, fn) → the *ir.Param appended to fn.Params for
// that context. Stored so rewriteReads and lowerProviders use the SAME
// *ir.Param pointer in Ident.Sym slots as the one on fn.Params. This is
// load-bearing for the optimizer's substituteParams, which keys on
// *ir.Param pointer identity.
type hiddenParamIndex struct {
	funcs map[*ir.Func]map[*ir.Context]*ir.Param
}

func buildHiddenParamIndex(pkg *ir.Package, reach Reachable, extraFuncs []*ir.Func) hiddenParamIndex {
	idx := hiddenParamIndex{funcs: map[*ir.Func]map[*ir.Context]*ir.Param{}}
	indexFn := func(fn *ir.Func) {
		if !hasBody(fn) {
			return
		}
		for _, ctx := range pkg.Contexts {
			if !reach.Funcs[ctx][fn] {
				continue
			}
			paramName := "__ctx_" + ctx.Name
			for _, p := range fn.Params {
				if p.Name == paramName {
					if idx.funcs[fn] == nil {
						idx.funcs[fn] = map[*ir.Context]*ir.Param{}
					}
					idx.funcs[fn][ctx] = p
					break
				}
			}
		}
	}
	for _, fn := range pkg.Funcs {
		indexFn(fn)
	}
	for _, fn := range extraFuncs {
		indexFn(fn)
	}
	return idx
}

func (idx hiddenParamIndex) get(fn *ir.Func, ctx *ir.Context) *ir.Param {
	if m := idx.funcs[fn]; m != nil {
		return m[ctx]
	}
	return nil
}

func hasHiddenProp(comp *ir.Component, name string) bool {
	for _, p := range comp.Props {
		if p.Name == name {
			return true
		}
	}
	return false
}

func hasFuncParam(fn *ir.Func, name string) bool {
	for _, p := range fn.Params {
		if p.Name == name {
			return true
		}
	}
	return false
}

// makeHiddenParamSym returns a fresh *ir.Param used as the Sym of an Ident
// that refers to the hidden context prop in a component body. This matches
// the shape the checker uses when declaring props as params in component
// scope. For func bodies, prefer the *ir.Param actually attached to the
// func via hiddenParamIndex — pointer identity matters for the optimizer's
// substituteParams.
func makeHiddenParamSym(ctx *ir.Context) *ir.Param {
	return &ir.Param{
		Name: "__ctx_" + ctx.Name,
		Type: ctx.Typ,
	}
}

// --- Read rewriting ---

// rewriteReads replaces every *ir.ContextRead{Ref: ctx} in each component
// and func in Reach(ctx) with an *ir.Ident referencing the hidden param.
// For funcs, the Ident.Sym is the actual *ir.Param attached to fn.Params
// (looked up in hidden) so the optimizer's substituteParams can match by
// pointer identity. Component bodies have no func — they use a fresh
// synthetic Param (matches the checker's component-prop scope shape).
func rewriteReads(pkg *ir.Package, reach Reachable, extraFuncs []*ir.Func, hidden hiddenParamIndex) {
	for _, ctx := range pkg.Contexts {
		compSym := makeHiddenParamSym(ctx)
		compIdent := func() ir.Expr {
			return &ir.Ident{
				Name:        "__ctx_" + ctx.Name,
				Type:        ctx.Typ,
				Sym:         compSym,
				Synthesized: true,
			}
		}
		compTransform := func(e ir.Expr) ir.Expr {
			cr, ok := e.(*ir.ContextRead)
			if !ok || cr.Ref != ctx {
				return e
			}
			return compIdent()
		}
		for _, comp := range pkg.Components {
			if !reach.Components[ctx][comp] {
				continue
			}
			w := newExprWalker(compTransform)
			comp.Body = w.stmts(comp.Body)
		}
		rewriteFn := func(fn *ir.Func) {
			if !reach.Funcs[ctx][fn] {
				return
			}
			if !hasBody(fn) {
				return
			}
			paramSym := hidden.get(fn, ctx)
			if paramSym == nil {
				return // defensive: should always exist after addHiddenParams
			}
			ident := func() ir.Expr {
				return &ir.Ident{
					Name:        paramSym.Name,
					Type:        paramSym.Type,
					Sym:         paramSym,
					Synthesized: true,
				}
			}
			transform := func(e ir.Expr) ir.Expr {
				cr, ok := e.(*ir.ContextRead)
				if !ok || cr.Ref != ctx {
					return e
				}
				return ident()
			}
			w := newExprWalker(transform)
			fn.Block = w.stmts(fn.Block)
		}
		for _, fn := range pkg.Funcs {
			rewriteFn(fn)
		}
		for _, fn := range extraFuncs {
			rewriteFn(fn)
		}
	}
}

// --- Provider lowering + root defaults ---

// lowerProviders rewrites ContextProvider nodes to plain children, threading
// the context value as a hidden arg onto every reachable NodeInst (component
// call) and Call (func call) inside. Window roots are seeded with ctx.Default
// for each context. Func bodies thread the value of their __ctx_<name>
// param down to any Reach-callees they themselves invoke.
func lowerProviders(pkg *ir.Package, reach Reachable, extraFuncs []*ir.Func, hidden hiddenParamIndex) {
	// Build default map: ctx → ctx.Default expression.
	defaults := make(map[*ir.Context]ir.Expr, len(pkg.Contexts))
	for _, ctx := range pkg.Contexts {
		defaults[ctx] = ctx.Default
	}

	// Seed window roots with defaults.
	for _, w := range pkg.Windows {
		windowActive := copyExprMap(defaults)
		w.Body = lowerInStmts(w.Body, windowActive, reach)
		for _, v := range w.Vars {
			v.Init = lowerInExpr(v.Init, windowActive, reach)
			for _, h := range v.Handlers {
				if h.Func != nil {
					h.Func.Block = lowerInStmts(h.Func.Block, windowActive, reach)
				}
			}
		}
		for _, fn := range w.Funcs {
			if hasBody(fn) {
				fn.Block = lowerInStmts(fn.Block, windowActive, reach)
			}
		}
	}
	// Top-level pkg.Vars: also rooted, seed with defaults.
	for _, v := range pkg.Vars {
		pkgActive := copyExprMap(defaults)
		v.Init = lowerInExpr(v.Init, pkgActive, reach)
		for _, h := range v.Handlers {
			if h.Func != nil {
				h.Func.Block = lowerInStmts(h.Func.Block, pkgActive, reach)
			}
		}
	}

	// Identify root components — those never instantiated as a NodeInst.
	// Their hidden props never receive a threaded value from a caller, so
	// references inside their body must resolve to the context default.
	called := map[*ir.Component]bool{}
	collectInstantiations := func(stmts []ir.Stmt) {
		var visit func([]ir.Stmt)
		visit = func(ss []ir.Stmt) {
			for _, s := range ss {
				switch n := s.(type) {
				case *ir.NodeInst:
					if n.Component != nil {
						called[n.Component] = true
					}
					visit(n.Children)
					for _, h := range n.Handlers {
						if h.Func != nil {
							visit(h.Func.Block)
						}
					}
				case *ir.If:
					visit(n.Body)
					visit(n.Else)
				case *ir.For:
					visit(n.Body)
					visit(n.Else)
				case *ir.PlatformFilter:
					visit(n.Body)
				case *ir.SlotInst:
					visit(n.Children)
				case *ir.ErrorBoundary:
					visit(n.Children)
				}
			}
		}
		visit(stmts)
	}
	for _, c := range pkg.Components {
		collectInstantiations(c.Body)
	}
	for _, w := range pkg.Windows {
		collectInstantiations(w.Body)
	}

	// For component bodies: each component receives its hidden context
	// value either as a synthesized prop threaded from its caller (when
	// instantiated), or as the context default expression (when it's a
	// root component, e.g. `main` or a top-level entry).
	for _, comp := range pkg.Components {
		var compActive map[*ir.Context]ir.Expr
		if called[comp] {
			compActive = hiddenActiveFor(pkg, reach, hidden, comp, nil)
		} else {
			// Root component — seed defaults for every reachable context.
			compActive = map[*ir.Context]ir.Expr{}
			for _, ctx := range pkg.Contexts {
				if reach.Components[ctx][comp] {
					compActive[ctx] = ctx.Default
				}
			}
		}
		comp.Body = lowerInStmts(comp.Body, compActive, reach)
		// Component-level Vars (var x = ...) and Funcs (func foo() {}) host
		// expressions that can call ctx-reading wrappers too — walk them so
		// hidden args get threaded uniformly.
		for _, v := range comp.Vars {
			v.Init = lowerInExpr(v.Init, compActive, reach)
			for _, h := range v.Handlers {
				if h.Func != nil {
					h.Func.Block = lowerInStmts(h.Func.Block, compActive, reach)
				}
			}
		}
		for _, fn := range comp.Funcs {
			if hasBody(fn) {
				fn.Block = lowerInStmts(fn.Block, compActive, reach)
			}
		}
		for _, t := range comp.Timers {
			if t.Handler != nil {
				t.Handler.Block = lowerInStmts(t.Handler.Block, compActive, reach)
			}
		}
	}

	// For func bodies (user + extras): same — each ctx for which this func
	// has a hidden param is active as an Ident reading that param.
	lowerFn := func(fn *ir.Func) {
		if !hasBody(fn) {
			return
		}
		fnActive := hiddenActiveFor(pkg, reach, hidden, nil, fn)
		fn.Block = lowerInStmts(fn.Block, fnActive, reach)
	}
	for _, fn := range pkg.Funcs {
		lowerFn(fn)
	}
	for _, fn := range extraFuncs {
		lowerFn(fn)
	}
}

// hiddenActiveFor builds the active-context map at the entry of a component
// or func body: each context for which the callee is in Reach is active and
// references the hidden param by Ident. Exactly one of comp / fn is non-nil.
// For funcs, the Ident.Sym is the actual *ir.Param on fn.Params (from the
// hiddenParamIndex) so threaded reads match what substituteParams keys on.
func hiddenActiveFor(pkg *ir.Package, reach Reachable, hidden hiddenParamIndex, comp *ir.Component, fn *ir.Func) map[*ir.Context]ir.Expr {
	out := map[*ir.Context]ir.Expr{}
	for _, ctx := range pkg.Contexts {
		inReach := false
		if comp != nil && reach.Components[ctx][comp] {
			inReach = true
		}
		if fn != nil && reach.Funcs[ctx][fn] {
			inReach = true
		}
		if !inReach {
			continue
		}
		var sym *ir.Param
		if fn != nil {
			sym = hidden.get(fn, ctx)
		}
		if sym == nil {
			sym = makeHiddenParamSym(ctx)
		}
		out[ctx] = &ir.Ident{
			Name:        sym.Name,
			Type:        sym.Type,
			Sym:         sym,
			Synthesized: true,
		}
	}
	return out
}

// lowerInStmts recursively lowers ContextProvider nodes and threads context
// args onto component-call NodeInsts and func-call Calls. active maps each
// context to its current value expression at this point in the tree.
func lowerInStmts(stmts []ir.Stmt, active map[*ir.Context]ir.Expr, reach Reachable) []ir.Stmt {
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.ContextProvider:
			// Replace provider with its children, updating the active value.
			inner := copyExprMap(active)
			inner[n.Ref] = n.Value
			lowered := lowerInStmts(n.Children, inner, reach)
			out = append(out, lowered...)

		case *ir.NodeInst:
			// Thread hidden args onto user-component calls that are reachable.
			if n.Component != nil {
				for ctx, val := range active {
					if !reach.Components[ctx][n.Component] {
						continue
					}
					paramName := "__ctx_" + ctx.Name
					if !hasArgNamed(n.Props, paramName) {
						n.Props = append(n.Props, ir.Arg{Name: paramName, Value: val})
					}
				}
			}
			// Walk prop values and handler bodies to thread args onto
			// nested func calls.
			for i := range n.Props {
				n.Props[i].Value = lowerInExpr(n.Props[i].Value, active, reach)
			}
			for _, h := range n.Handlers {
				if h.Func != nil {
					h.Func.Block = lowerInStmts(h.Func.Block, active, reach)
				}
			}
			n.Children = lowerInStmts(n.Children, active, reach)
			out = append(out, n)

		case *ir.If:
			n.Cond = lowerInExpr(n.Cond, active, reach)
			n.Body = lowerInStmts(n.Body, active, reach)
			n.Else = lowerInStmts(n.Else, active, reach)
			out = append(out, n)

		case *ir.For:
			n.Iter = lowerInExpr(n.Iter, active, reach)
			n.Body = lowerInStmts(n.Body, active, reach)
			n.Else = lowerInStmts(n.Else, active, reach)
			out = append(out, n)

		case *ir.PlatformFilter:
			n.Body = lowerInStmts(n.Body, active, reach)
			out = append(out, n)

		case *ir.SlotInst:
			n.Children = lowerInStmts(n.Children, active, reach)
			out = append(out, n)

		case *ir.ErrorBoundary:
			n.Children = lowerInStmts(n.Children, active, reach)
			out = append(out, n)

		case *ir.Assign:
			n.Target = lowerInExpr(n.Target, active, reach)
			n.Value = lowerInExpr(n.Value, active, reach)
			out = append(out, n)

		case *ir.LocalVar:
			n.Init = lowerInExpr(n.Init, active, reach)
			out = append(out, n)

		case *ir.Return:
			n.Value = lowerInExpr(n.Value, active, reach)
			out = append(out, n)

		case *ir.CallStmt:
			if n.Call != nil {
				lowerCallInPlace(n.Call, active, reach)
				n.Call.Receiver = lowerInExpr(n.Call.Receiver, active, reach)
				for i := range n.Call.Args {
					n.Call.Args[i].Value = lowerInExpr(n.Call.Args[i].Value, active, reach)
				}
			}
			out = append(out, n)

		case *ir.Emit:
			for i := range n.Args {
				n.Args[i].Value = lowerInExpr(n.Args[i].Value, active, reach)
			}
			out = append(out, n)

		default:
			out = append(out, n)
		}
	}
	return out
}

// lowerInExpr walks an expression, threading hidden ctx args onto any
// reachable *ir.Call inside.
func lowerInExpr(e ir.Expr, active map[*ir.Context]ir.Expr, reach Reachable) ir.Expr {
	if e == nil {
		return nil
	}
	switch n := e.(type) {
	case *ir.Call:
		lowerCallInPlace(n, active, reach)
		n.Receiver = lowerInExpr(n.Receiver, active, reach)
		n.Callee = lowerInExpr(n.Callee, active, reach)
		for i := range n.Args {
			n.Args[i].Value = lowerInExpr(n.Args[i].Value, active, reach)
		}
	case *ir.Binary:
		n.Left = lowerInExpr(n.Left, active, reach)
		n.Right = lowerInExpr(n.Right, active, reach)
	case *ir.Unary:
		n.Operand = lowerInExpr(n.Operand, active, reach)
	case *ir.Ternary:
		n.Cond = lowerInExpr(n.Cond, active, reach)
		n.Then = lowerInExpr(n.Then, active, reach)
		n.Else = lowerInExpr(n.Else, active, reach)
	case *ir.Select:
		n.Operand = lowerInExpr(n.Operand, active, reach)
	case *ir.Index:
		n.Operand = lowerInExpr(n.Operand, active, reach)
		n.Idx = lowerInExpr(n.Idx, active, reach)
	case *ir.Conversion:
		n.Operand = lowerInExpr(n.Operand, active, reach)
	case *ir.StructLit:
		for i := range n.Fields {
			n.Fields[i].Value = lowerInExpr(n.Fields[i].Value, active, reach)
		}
	case *ir.ListLit:
		for i := range n.Elems {
			n.Elems[i] = lowerInExpr(n.Elems[i], active, reach)
		}
	case *ir.MapLitIR:
		for i := range n.Entries {
			n.Entries[i].Key = lowerInExpr(n.Entries[i].Key, active, reach)
			n.Entries[i].Value = lowerInExpr(n.Entries[i].Value, active, reach)
		}
	case *ir.Spread:
		n.Operand = lowerInExpr(n.Operand, active, reach)
	}
	return e
}

// lowerCallInPlace threads hidden __ctx_<name> args onto a func call site
// for every context in Reach(callee). active provides the current value
// expression for each context.
func lowerCallInPlace(c *ir.Call, active map[*ir.Context]ir.Expr, reach Reachable) {
	if c == nil || c.Func == nil {
		return
	}
	for ctx, val := range active {
		if !reach.Funcs[ctx][c.Func] {
			continue
		}
		paramName := "__ctx_" + ctx.Name
		if hasCallArgNamed(c.Args, paramName) {
			continue
		}
		c.Args = append(c.Args, ir.CallArg{Name: paramName, Value: val})
	}
}

// copyExprMap returns a shallow copy of m.
func copyExprMap(m map[*ir.Context]ir.Expr) map[*ir.Context]ir.Expr {
	out := make(map[*ir.Context]ir.Expr, len(m))
	maps.Copy(out, m)
	return out
}

// hasArgNamed reports whether props already contains a named arg with the
// given name.
func hasArgNamed(props []ir.Arg, name string) bool {
	for _, a := range props {
		if a.Name == name {
			return true
		}
	}
	return false
}

// hasCallArgNamed reports whether args already contains a named arg with
// the given name.
func hasCallArgNamed(args []ir.CallArg, name string) bool {
	for _, a := range args {
		if a.Name == name {
			return true
		}
	}
	return false
}
