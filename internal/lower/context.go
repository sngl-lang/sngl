package lower

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"duckfam.us/sngl/ir"
)

// passContext lowers context declarations into hidden state. Runs when
// either Features.StructComponents or Features.StdlibContextParam is set; today
// both flags co-trigger the full lowering, but they exist independently
// so a future split can run the component-side and stdlib-side rewrites
// in isolation.
//
// For COMPONENTS in Reach(ctx) (StructComponents path): a synthesized
// *ir.Var named __ctx_<name> is appended to comp.Vars with Init =
// ctx.Default. Reads inside the component's body and vars
// are rewritten to *ir.Ident with Sym = that *ir.Var, so the value
// reads as Model state. When a parent component instantiates a child
// under a provider, the parent threads __ctx_<name>=<value> as a
// NodeInst Prop arg. An inlined body reads that value in place of the
// var (providedContextVars), which is what lets a provider reading
// state update its readers; a value unsafe to read twice seeds the var
// instead. A component built at run time takes it as a prop
// (contextProps).
//
// For FUNCTIONS in Reach(ctx) -- the program's own, a component's methods,
// and the library wrappers such as i18n.tr: a *ir.Param is appended to
// fn.Params, and every call is threaded with the value active where it is
// written, so a function reads the provider its caller runs under.
//
// It must run BEFORE passInlinePure (provider rewrite assumes
// un-inlined component boundaries) and BEFORE passReactivity
// (synthesized props must be visible as reactive deps).
var passContext = pass{
	name:    "Context",
	enabled: func(c Features) bool { return c.StructComponents || c.StdlibContextParam },
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
func applyNoContext(pkg *ir.Package, _ Features, _ Options) error {
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
	lowerProviders(pkg, reach, extra, hiddenParams)
	rewriteReads(pkg, reach, extra, hiddenParams)

	pkg.Contexts = nil
	return reportSurvivingReads(pkg)
}

// reportSurvivingReads fails the build if a *ir.ContextRead outlived the pass.
// No backend has a case for one, and every backend that prints an expression
// prints it as the bare context name — so what a leak produced was host source
// naming an identifier nothing declares, blamed on the host compiler rather
// than on this pass. The pass's own postcondition, stated where it can be
// checked.
func reportSurvivingReads(pkg *ir.Package) error {
	var err error
	_ = ir.Walk(pkg, func(n ir.Node) error {
		r, ok := n.(*ir.ContextRead)
		if !ok || err != nil {
			return nil
		}
		name := "?"
		if r.Ref != nil {
			name = r.Ref.Name
		}
		err = fmt.Errorf("context %q: read survived lowering with no provider or default in scope", name)
		return ir.SkipDir
	})
	return err
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
	}
	for _, fn := range pkg.Funcs {
		if !hasBody(fn) {
			continue
		}
		seedFromStmts(fn.Block)
	}
	seedFromStmts(pkg.Body)
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

// hasBody reports whether a func has a body that NoContext should walk. A
// native import has none, and an intrinsic keeps one only when it says the
// body computes the right answer — which the i18n entry points do, and their
// bodies read the active locale, so the hidden parameter has to reach them.
//
// A #[foreign] mark is not an import: the backend emits the marked
// declaration, so its body is walked. Skipped, it keeps an ir.ContextRead that
// no emitter has a case for.
func hasBody(f *ir.Func) bool {
	if f == nil {
		return false
	}
	if !f.Foreign.Marked && (f.Foreign.Name != "" || f.Foreign.Path != "") {
		return false
	}
	return len(f.Block) > 0
}

// contextFuncs is every function whose body passContext threads a context
// into as a hidden parameter: the package's own, each component's methods, and
// the library wrappers the program reaches. A test function is not one -- the
// harness calls it with the parameters it declares, so it is a root the way a
// window is.
func contextFuncs(pkg *ir.Package, extraFuncs []*ir.Func) []*ir.Func {
	var out []*ir.Func
	add := func(fn *ir.Func) {
		if hasBody(fn) && !fn.IsTest {
			out = append(out, fn)
		}
	}
	for _, fn := range pkg.Funcs {
		add(fn)
	}
	for _, comp := range pkg.Components {
		for _, fn := range comp.Funcs {
			add(fn)
		}
	}
	for _, fn := range extraFuncs {
		add(fn)
	}
	return out
}

// computeReachability returns, for each Context, the sets of Components and
// Funcs that transitively read that context (without an intervening provider
// shadowing it). extraFuncs are external (stdlib) funcs with bodies that are
// reachable from user code and should participate in reachability propagation.
//
// A component's surface is its body and its vars. Its methods are funcs in
// their own right: one called under a provider in the body reads that
// provider's value, not the one the component was entered with.
func computeReachability(pkg *ir.Package, extraFuncs []*ir.Func) Reachable {
	reach := Reachable{
		Components: make(map[*ir.Context]map[*ir.Component]bool, len(pkg.Contexts)),
		Funcs:      make(map[*ir.Context]map[*ir.Func]bool, len(pkg.Contexts)),
	}
	for _, ctx := range pkg.Contexts {
		reach.Components[ctx] = make(map[*ir.Component]bool)
		reach.Funcs[ctx] = make(map[*ir.Func]bool)
	}
	funcs := contextFuncs(pkg, extraFuncs)

	for _, fn := range funcs {
		for ctx := range directContextReads(fn.Block) {
			reach.Funcs[ctx][fn] = true
		}
	}
	componentCalls := func(comp *ir.Component) []callEdge {
		calls := callsInBody(comp.Body, nil)
		for _, v := range comp.Vars {
			calls = append(calls, callsInExpr(v.Init, nil)...)
			for _, h := range v.Handlers {
				if h.Func != nil {
					calls = append(calls, callsInBody(h.Func.Block, nil)...)
				}
			}
		}
		return calls
	}
	for _, comp := range pkg.Components {
		out := map[*ir.Context]bool{}
		collectContextReads(comp.Body, out)
		for _, v := range comp.Vars {
			collectContextReads(v.Init, out)
			for _, h := range v.Handlers {
				if h.Func != nil {
					collectContextReads(h.Func.Block, out)
				}
			}
		}
		for ctx := range out {
			reach.Components[ctx][comp] = true
		}
	}

	reaches := func(call callEdge, ctx *ir.Context) bool {
		if call.shadowed[ctx] {
			return false
		}
		return (call.fn != nil && reach.Funcs[ctx][call.fn]) ||
			(call.comp != nil && reach.Components[ctx][call.comp])
	}
	for changed := true; changed; {
		changed = false
		for _, caller := range pkg.Components {
			for _, call := range componentCalls(caller) {
				for _, ctx := range pkg.Contexts {
					if !reach.Components[ctx][caller] && reaches(call, ctx) {
						reach.Components[ctx][caller] = true
						changed = true
					}
				}
			}
		}
		for _, caller := range funcs {
			for _, call := range callsInBody(caller.Block, nil) {
				for _, ctx := range pkg.Contexts {
					if !reach.Funcs[ctx][caller] && reaches(call, ctx) {
						reach.Funcs[ctx][caller] = true
						changed = true
					}
				}
			}
		}
	}
	return reach
}

// directContextReads returns the set of contexts read directly in stmts: not
// counting reads reached through a call, which the fixpoint in
// computeReachability propagates, and not counting reads under a provider for
// the same context, which that provider answers rather than the scope being
// scanned.
func directContextReads(stmts []ir.Stmt) map[*ir.Context]bool {
	out := map[*ir.Context]bool{}
	collectContextReads(stmts, out)
	return out
}

// collectContextReads walks root -- a []ir.Stmt, a single statement or an
// expression -- adding every context read directly under it to out.
//
// The walk is ir.Walk rather than a switch of its own. This pass used to carry
// a full copy of the IR traversal, and what it cost was one line per node kind
// that the IR had and the copy did not: a read inside a named slot's
// population, or inside an errorBoundary's @error handler, was simply not
// found, the component holding it was never marked as a reader, and the read
// survived the pass to reach codegen as a bare *ir.ContextRead that no
// emitter has a case for. What is left below is only what this pass answers
// differently from a plain traversal, and each such answer is a scope rule.
func collectContextReads(root any, out map[*ir.Context]bool) {
	// Walk's callback never returns a real error, so neither does this.
	_ = ir.Walk(root, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.ContextRead:
			out[x.Ref] = true
		case *ir.ContextProvider:
			// A provider answers reads of its own context beneath it, so those
			// are not reads of the scope being scanned. Its value is, though:
			// that expression is evaluated outside the scope it establishes.
			collectContextReads(x.Value, out)
			return ir.SkipDir
		}
		return nil
	})
}

func callsInBody(stmts []ir.Stmt, shadow map[*ir.Context]bool) []callEdge {
	return callsIn(stmts, shadow)
}

func callsInExpr(e ir.Expr, shadow map[*ir.Context]bool) []callEdge {
	return callsIn(e, shadow)
}

// callsIn collects every call edge reachable from root -- a component
// instantiation or a function call -- each tagged with the set of contexts a
// provider already answers at that point.
//
// Shadowing is why this cannot be a plain ir.Walk: the tag is a property of
// the path taken to an edge, not of the edge. Only the provider case knows
// that, so only the provider case recurses by hand.
func callsIn(root any, shadow map[*ir.Context]bool) []callEdge {
	var calls []callEdge
	_ = ir.Walk(root, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.NodeInst:
			if x.Component != nil {
				calls = append(calls, callEdge{comp: x.Component, shadowed: shadow})
			}
		case *ir.Call:
			if x.Func != nil {
				calls = append(calls, callEdge{fn: x.Func, shadowed: shadow})
			}
		case *ir.ContextProvider:
			inner := copyContextShadow(shadow)
			inner[x.Ref] = true
			calls = append(calls, callsIn(x.Value, shadow)...)
			calls = append(calls, callsIn(x.Children, inner)...)
			return ir.SkipDir
		}
		return nil
	})
	return calls
}

func copyContextShadow(m map[*ir.Context]bool) map[*ir.Context]bool {
	out := make(map[*ir.Context]bool, len(m)+1)
	maps.Copy(out, m)
	return out
}

// --- Hidden params ---

// addHiddenParams adds a synthesized __ctx_<name> Var to every component in
// Reach(ctx), initialized to ctx.Default, and a __ctx_<name> Param to every
// func in it. A func takes the value as a parameter rather than reading its
// owner's var because its caller is what says which provider it runs under:
// the same func called inside and outside `depth(5) { … }` reads two values.
func addHiddenParams(pkg *ir.Package, reach Reachable, extraFuncs []*ir.Func) {
	funcs := contextFuncs(pkg, extraFuncs)
	for _, ctx := range pkg.Contexts {
		paramName := "__ctx_" + ctx.Name
		for _, comp := range pkg.Components {
			if !reach.Components[ctx][comp] || hasComponentVar(comp, paramName) {
				continue
			}
			// Prepended so it initializes before a var whose Init reads it
			// (`var greeting = $"Login"` reads __ctx_locale).
			hidden := &ir.Var{
				Name:        paramName,
				Type:        ctx.Typ,
				Init:        ctx.Default,
				Synthesized: true,
			}
			comp.Vars = append([]*ir.Var{hidden}, comp.Vars...)
		}
		for _, fn := range funcs {
			if !reach.Funcs[ctx][fn] || hasFuncParam(fn, paramName) {
				continue
			}
			fn.Params = append(fn.Params, &ir.Param{Name: paramName, Type: ctx.Typ})
		}
	}
}

// hiddenParamIndex maps each hidden context symbol back to the actual
// declaration node that owns it. For funcs in Reach(ctx) the symbol is the
// *ir.Param appended to fn.Params; for components in Reach(ctx) it's the
// *ir.Var appended to comp.Vars. Storing the actual pointers is
// load-bearing: the optimizer's substituteParams keys on *ir.Param pointer
// identity for funcs, and reactivity/inlining pass-throughs key on the
// *ir.Var pointer for components.
type hiddenParamIndex struct {
	funcs map[*ir.Func]map[*ir.Context]*ir.Param
	comps map[*ir.Component]map[*ir.Context]*ir.Var
}

func buildHiddenParamIndex(pkg *ir.Package, reach Reachable, extraFuncs []*ir.Func) hiddenParamIndex {
	idx := hiddenParamIndex{
		funcs: map[*ir.Func]map[*ir.Context]*ir.Param{},
		comps: map[*ir.Component]map[*ir.Context]*ir.Var{},
	}
	for _, fn := range contextFuncs(pkg, extraFuncs) {
		for _, ctx := range pkg.Contexts {
			if !reach.Funcs[ctx][fn] {
				continue
			}
			for _, p := range fn.Params {
				if p.Name == "__ctx_"+ctx.Name {
					if idx.funcs[fn] == nil {
						idx.funcs[fn] = map[*ir.Context]*ir.Param{}
					}
					idx.funcs[fn][ctx] = p
					break
				}
			}
		}
	}
	for _, comp := range pkg.Components {
		for _, ctx := range pkg.Contexts {
			if !reach.Components[ctx][comp] {
				continue
			}
			for _, v := range comp.Vars {
				if v.Name == "__ctx_"+ctx.Name {
					if idx.comps[comp] == nil {
						idx.comps[comp] = map[*ir.Context]*ir.Var{}
					}
					idx.comps[comp][ctx] = v
					break
				}
			}
		}
	}
	return idx
}

func (idx hiddenParamIndex) get(fn *ir.Func, ctx *ir.Context) *ir.Param {
	if m := idx.funcs[fn]; m != nil {
		return m[ctx]
	}
	return nil
}

func (idx hiddenParamIndex) getComp(comp *ir.Component, ctx *ir.Context) *ir.Var {
	if m := idx.comps[comp]; m != nil {
		return m[ctx]
	}
	return nil
}

func hasComponentVar(comp *ir.Component, name string) bool {
	for _, v := range comp.Vars {
		if v.Name == name {
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

func hiddenIdent(sym ir.Symbol, name string, typ *ir.Type) *ir.Ident {
	return &ir.Ident{Name: name, Type: typ, Sym: sym, Synthesized: true}
}

// --- Read rewriting ---

// rewriteReads replaces every *ir.ContextRead{Ref: ctx} left in a component or
// func in Reach(ctx) with a read of its hidden var or param. The reads under a
// provider were answered by lowerProviders already; these are the ones the
// scope's entry answers.
func rewriteReads(pkg *ir.Package, reach Reachable, extraFuncs []*ir.Func, hidden hiddenParamIndex) {
	for _, ctx := range pkg.Contexts {
		readsOf := func(ident func() ir.Expr) *exprWalker {
			return newExprWalker(func(e ir.Expr) ir.Expr {
				if cr, ok := e.(*ir.ContextRead); ok && cr.Ref == ctx {
					return ident()
				}
				return e
			})
		}
		for _, comp := range pkg.Components {
			varSym := hidden.getComp(comp, ctx)
			if !reach.Components[ctx][comp] || varSym == nil {
				continue
			}
			w := readsOf(func() ir.Expr { return hiddenIdent(varSym, varSym.Name, varSym.Type) })
			comp.Body = w.stmts(comp.Body)
			for _, v := range comp.Vars {
				if v == varSym {
					continue
				}
				v.Init = w.expr(v.Init)
				for _, h := range v.Handlers {
					if h.Func != nil {
						h.Func.Block = w.stmts(h.Func.Block)
					}
				}
			}
		}
		for _, fn := range contextFuncs(pkg, extraFuncs) {
			paramSym := hidden.get(fn, ctx)
			if !reach.Funcs[ctx][fn] || paramSym == nil {
				continue
			}
			w := readsOf(func() ir.Expr { return hiddenIdent(paramSym, paramSym.Name, paramSym.Type) })
			fn.Block = w.stmts(fn.Block)
		}
	}
}

// --- Provider lowering + root defaults ---

// provLower is what the three walkers below need to answer a call site, held
// together rather than passed as four parameters: the reachability sets, the
// hidden bindings, each context's default, and the slot environments.
type provLower struct {
	reach    Reachable
	hidden   hiddenParamIndex
	defaults map[*ir.Context]ir.Expr
	slots    slotEnvs
	order    []*ir.Context
}

// slotEnvs records, per component and per slot, the context values a provider
// in that component's body establishes over that slot's insertion point.
//
// A provider covers what is *under* it in the rendered tree, and a slot
// insertion is a hole in that tree the caller fills -- so `list { listItem() }`
// puts the item under whatever `list`'s body wrapped its `items` around, even
// though the item is written at the call site. Nothing else in this pass can
// see that: everywhere else a context is threaded by *lexical* position, and
// lexically the population is the caller's, outside the callee's provider.
//
// The rest slot is keyed by "", which is where a caller's bare children go
// (ir.NodeInst.Children); a named slot is keyed by its name, matching
// ir.NodeInst.Slots.
type slotEnvs map[*ir.Component]map[string]map[*ir.Context]ir.Expr

// computeSlotEnvs builds that table. It runs before any body is lowered,
// because lowerProviders splices a provider out of the body it was written in
// and the values recorded here are read long after that.
//
// A recorded value is written in the *callee's* terms: its ContextReads are
// the values the callee was entered with, which is what a call site
// substitutes. Composition down a chain of providers happens here rather than
// at the call site, so `depth(depth + 1)` inside `depth(depth + 1)` is one
// closed expression by the time any caller reads it.
func computeSlotEnvs(pkg *ir.Package) slotEnvs {
	out := slotEnvs{}
	for _, comp := range pkg.Components {
		per := map[string]map[*ir.Context]ir.Expr{}
		collectSlotEnv(comp.Body, map[*ir.Context]ir.Expr{}, per)
		if len(per) > 0 {
			out[comp] = per
		}
	}
	return out
}

// collectSlotEnv walks root recording the active provider values at every
// SlotInst under it. Like callsIn, it cannot be a plain ir.Walk: what it
// records is a property of the path taken to a slot, not of the slot.
func collectSlotEnv(root any, cur map[*ir.Context]ir.Expr, out map[string]map[*ir.Context]ir.Expr) {
	_ = ir.Walk(root, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.ContextProvider:
			inner := copyExprMap(cur)
			inner[x.Ref] = substituteReads(x.Value, cur)
			collectSlotEnv(x.Children, inner, out)
			return ir.SkipDir
		case *ir.SlotInst:
			if len(cur) > 0 {
				key := x.Name
				if x.Rest {
					key = ""
				}
				out[key] = copyExprMap(cur)
			}
		}
		return nil
	})
}

// substituteReads returns a copy of e with every ContextRead that vals answers
// replaced by its value. The copy is the point: the result is spliced into
// however many call sites the component has, and this pass and the ones after
// it rewrite expressions in place.
func substituteReads(e ir.Expr, vals map[*ir.Context]ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	w := newExprWalker(func(x ir.Expr) ir.Expr {
		cr, ok := x.(*ir.ContextRead)
		if !ok {
			return x
		}
		if v, found := vals[cr.Ref]; found {
			return ir.CloneExprSharingDecls(v)
		}
		return x
	})
	return w.expr(ir.CloneExprSharingDecls(e))
}

// slotActive is the active map for content a caller writes into one of inst's
// slots: the caller's own, overridden by whatever the callee's body wraps that
// slot in. entry is what the callee reads at its own entry, which is what the
// recorded values are written against.
func slotActive(active, entry map[*ir.Context]ir.Expr, env map[*ir.Context]ir.Expr, pc *provLower) map[*ir.Context]ir.Expr {
	if len(env) == 0 {
		return active
	}
	out := copyExprMap(active)
	for ctx, ex := range env {
		// Lowered against entry rather than against active: the expression
		// came from the callee's body, so a call inside it is threaded with
		// what the callee holds.
		out[ctx] = lowerInExpr(substituteReads(ex, entry), entry, pc)
	}
	return out
}

// calleeEntry is what inst's callee reads for each context on entry: the value
// threaded to it where the caller has one, and the context's default where it
// does not -- which is exactly what the hidden var's Init would have been.
func calleeEntry(active map[*ir.Context]ir.Expr, pc *provLower) map[*ir.Context]ir.Expr {
	out := copyExprMap(active)
	for ctx, def := range pc.defaults {
		if _, ok := out[ctx]; !ok {
			out[ctx] = def
		}
	}
	return out
}

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
	pc := &provLower{
		reach:    reach,
		hidden:   hidden,
		defaults: defaults,
		order:    pkg.Contexts,
		// Recorded before the first body is lowered: this walk splices every
		// provider out of the body it was written in.
		slots: computeSlotEnvs(pkg),
	}

	// The package body is a root the same way a window is.
	pkgBodyActive := copyExprMap(defaults)
	pkg.Body = lowerInStmts(pkg.Body, pkgBodyActive, pc)
	pkg.Body, pkg.Vars = promoteLocalVarsToVars(pkg.Body, pkg.Vars)
	// Top-level pkg.Vars: also rooted, seed with defaults.
	for _, v := range pkg.Vars {
		pkgActive := copyExprMap(defaults)
		v.Init = lowerInExpr(v.Init, pkgActive, pc)
		for _, h := range v.Handlers {
			if h.Func != nil {
				h.Func.Block = lowerInStmts(h.Func.Block, pkgActive, pc)
			}
		}
	}

	// For component bodies: the active value for each ctx is a read of
	// the component's hidden Var. The Var's Init is ctx.Default; when a
	// parent instantiates this component under a provider, the inliner /
	// codegen overrides Init with the parent-supplied arg, so reads from
	// the field see the threaded value.
	for _, comp := range pkg.Components {
		compActive := hiddenActiveFor(pkg, hidden, comp, nil)
		comp.Body = lowerInStmts(comp.Body, compActive, pc)
		// Any LocalVar (`var x = ...` originating from inside a provider
		// block, now spliced up to component-body level by the provider
		// unwrap above) is promoted to a component-level *ir.Var so
		// codegen treats it as Model state instead of leaving it as a
		// dangling block-local in a context where there is no enclosing
		// function body.
		comp.Body, comp.Vars = promoteLocalVarsToVars(comp.Body, comp.Vars)
		for _, v := range comp.Vars {
			v.Init = lowerInExpr(v.Init, compActive, pc)
			for _, h := range v.Handlers {
				if h.Func != nil {
					h.Func.Block = lowerInStmts(h.Func.Block, compActive, pc)
				}
			}
		}
	}

	for _, fn := range contextFuncs(pkg, extraFuncs) {
		fn.Block = lowerInStmts(fn.Block, hiddenActiveFor(pkg, hidden, nil, fn), pc)
	}
	for _, fn := range pkg.Funcs {
		if fn.IsTest && hasBody(fn) {
			fn.Block = lowerInStmts(fn.Block, copyExprMap(defaults), pc)
		}
	}
}

// promoteLocalVarsToVars converts every top-level *ir.LocalVar in stmts to a
// *ir.Var, appends it to vars, and returns stmts without those LocalVars. Used
// after provider-unwrap to lift `var` declarations that were originally
// block-scoped inside a `name(value) { ... }` provider up to the enclosing
// component or window's Vars slice, where codegen treats them as ordinary
// state declarations. Only top-level LocalVars are promoted; LocalVars nested
// inside If/For/etc. remain block-scoped.
//
// The Var promoted is the one the local already bound, not a copy of it.
// Everything downstream that asks whether a name is state keys on the *ir.Var
// pointer -- the dependency tracker, the reactive bindings -- and an Ident in
// the body still resolves to lv.Sym. Minting a second Var here left those
// Idents pointing at a symbol no longer in any state set, so the value was a
// model field that nothing appeared to read.
func promoteLocalVarsToVars(stmts []ir.Stmt, vars []*ir.Var) ([]ir.Stmt, []*ir.Var) {
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		lv, ok := s.(*ir.LocalVar)
		if !ok {
			out = append(out, s)
			continue
		}
		v := lv.Sym
		if v == nil {
			v = &ir.Var{AST: lv.AST, Name: lv.Name}
		}
		// The statement carries the initializer and the resolved type; the
		// symbol carries only the binding.
		if v.Init == nil {
			v.Init = lv.Init
		}
		if v.Type == nil {
			v.Type = lv.Type
		}
		vars = append(vars, v)
	}
	return out, vars
}

// hiddenActiveFor builds the active-context map at the entry of a component
// or func body: each context the scope is in Reach for reads its hidden var or
// param. Exactly one of comp / fn is non-nil. For funcs the Ident's Sym is the
// *ir.Param on fn.Params, which is what substituteParams keys on.
func hiddenActiveFor(pkg *ir.Package, hidden hiddenParamIndex, comp *ir.Component, fn *ir.Func) map[*ir.Context]ir.Expr {
	out := map[*ir.Context]ir.Expr{}
	for _, ctx := range pkg.Contexts {
		if comp != nil {
			if v := hidden.getComp(comp, ctx); v != nil {
				out[ctx] = hiddenIdent(v, v.Name, v.Type)
			}
		}
		if fn != nil {
			if p := hidden.get(fn, ctx); p != nil {
				out[ctx] = hiddenIdent(p, p.Name, p.Type)
			}
		}
	}
	return out
}

// lowerInStmts recursively lowers ContextProvider nodes and threads context
// args onto component-call NodeInsts and func-call Calls. active maps each
// context to its current value expression at this point in the tree.
func lowerInStmts(stmts []ir.Stmt, active map[*ir.Context]ir.Expr, pc *provLower) []ir.Stmt {
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.ContextProvider:
			// Replace provider with its children, updating the active value.
			// The value is lowered against the *enclosing* active map, not the
			// one it establishes: `depth(depth + 1)` reads the value it is
			// overriding, which is a strictly outer scope, so the substitution
			// terminates and nesting accumulates. Installed unlowered, that
			// read stayed an *ir.ContextRead and reached codegen as a bare
			// identifier nothing declares.
			inner := copyExprMap(active)
			inner[n.Ref] = lowerInExpr(n.Value, active, pc)
			lowered := lowerInStmts(n.Children, inner, pc)
			out = append(out, lowered...)

		case *ir.NodeInst:
			// Thread hidden args onto user-component calls that are reachable.
			if n.Component != nil {
				// By name: the order is the props' order, which is output.
				for _, ctx := range sortedContexts(active) {
					val := active[ctx]
					if !pc.reach.Components[ctx][n.Component] {
						continue
					}
					paramName := "__ctx_" + ctx.Name
					if !hasArgNamed(n.Props, paramName) {
						n.Props = append(n.Props, ir.Arg{Name: paramName, Value: val})
					}
				}
			}
			// Walk prop values and handler bodies to thread args onto
			// nested func calls. Those are the caller's own and stay on the
			// caller's active map -- only what the callee *renders* stands
			// under the callee's providers.
			for i := range n.Props {
				n.Props[i].Value = lowerInExpr(n.Props[i].Value, active, pc)
			}
			for i := range n.Handlers {
				lowerInHandler(&n.Handlers[i], active, pc)
			}
			// A slot population is rendered where the callee's body inserts
			// it, so a provider the callee wrapped that insertion in covers
			// it -- even though the population is written here.
			slots := pc.slots[n.Component]
			entry := active
			if len(slots) > 0 {
				entry = calleeEntry(active, pc)
			}
			n.Children = lowerInStmts(n.Children, slotActive(active, entry, slots[""], pc), pc)
			for _, name := range ir.SlotNames(n.Slots) {
				sc := n.Slots[name]
				sc.Body = lowerInStmts(sc.Body, slotActive(active, entry, slots[name], pc), pc)
			}
			out = append(out, n)

		case *ir.If:
			n.Cond = lowerInExpr(n.Cond, active, pc)
			n.Body = lowerInStmts(n.Body, active, pc)
			n.Else = lowerInStmts(n.Else, active, pc)
			out = append(out, n)

		case *ir.For:
			n.Iter = lowerInExpr(n.Iter, active, pc)
			n.Body = lowerInStmts(n.Body, active, pc)
			n.Else = lowerInStmts(n.Else, active, pc)
			out = append(out, n)

		case *ir.SlotInst:
			// What the insertion hands its population is evaluated here.
			for i := range n.Args {
				n.Args[i] = lowerInExpr(n.Args[i], active, pc)
			}
			n.Children = lowerInStmts(n.Children, active, pc)
			for _, name := range ir.SlotNames(n.Slots) {
				sc := n.Slots[name]
				sc.Body = lowerInStmts(sc.Body, active, pc)
			}
			out = append(out, n)

		case *ir.ErrorBoundary:
			lowerInHandler(n.Handler, active, pc)
			n.Children = lowerInStmts(n.Children, active, pc)
			n.Failed = lowerInStmts(n.Failed, active, pc)
			out = append(out, n)

		case *ir.Assign:
			n.Target = lowerInExpr(n.Target, active, pc)
			n.Value = lowerInExpr(n.Value, active, pc)
			out = append(out, n)

		case *ir.LocalVar:
			n.Init = lowerInExpr(n.Init, active, pc)
			out = append(out, n)

		case *ir.Return:
			n.Value = lowerInExpr(n.Value, active, pc)
			out = append(out, n)

		case *ir.CallStmt:
			if ctx, val := testSetContext(n.Call); ctx != nil {
				// A test body's override is a provider over the statements after it.
				lowered := lowerInExpr(val.Value, active, pc)
				val.Value = lowered
				active = copyExprMap(active)
				active[ctx] = lowered
			} else if n.Call != nil {
				lowerInExpr(n.Call, active, pc)
			}
			out = append(out, n)

		case *ir.Emit:
			for i := range n.Args {
				n.Args[i].Value = lowerInExpr(n.Args[i].Value, active, pc)
			}
			out = append(out, n)

		case *ir.Toggle:
			// Toggle may survive into NoContext when NoToggle cap is off.
			n.Target = lowerInExpr(n.Target, active, pc)
			out = append(out, n)

		case *ir.Break, *ir.Continue:
			// A loop escape has no expression and no nested block to lower --
			// but it is still a statement, and this walk rebuilds the list it
			// was in. Left out it was deleted: a `continue` in any program
			// that also lowers a context vanished on every target, silently,
			// and which programs those were depended only on whether some
			// package in the build declared one.
			out = append(out, n)
		default:
			panic(fmt.Sprintf("lowerInStmts: unhandled %T", n))
		}
	}
	return out
}

// lowerInExpr walks an expression, threading hidden ctx args onto any
// reachable *ir.Call inside.
func lowerInExpr(e ir.Expr, active map[*ir.Context]ir.Expr, pc *provLower) ir.Expr {
	if e == nil {
		return nil
	}
	switch n := e.(type) {
	case *ir.Call:
		lowerCallInPlace(n, active, pc)
		n.Receiver = lowerInExpr(n.Receiver, active, pc)
		if calledFunc(n) == n.Func {
			n.Callee = lowerInExpr(n.Callee, active, pc)
		}
		for i := range n.Args {
			n.Args[i].Value = lowerInExpr(n.Args[i].Value, active, pc)
		}
		lowerInHandler(n.ErrorHandler, active, pc)
	case *ir.Binary:
		n.Left = lowerInExpr(n.Left, active, pc)
		n.Right = lowerInExpr(n.Right, active, pc)
	case *ir.Unary:
		n.Operand = lowerInExpr(n.Operand, active, pc)
	case *ir.Ternary:
		n.Cond = lowerInExpr(n.Cond, active, pc)
		n.Then = lowerInExpr(n.Then, active, pc)
		n.Else = lowerInExpr(n.Else, active, pc)
	case *ir.Select:
		n.Operand = lowerInExpr(n.Operand, active, pc)
	case *ir.Index:
		n.Operand = lowerInExpr(n.Operand, active, pc)
		n.Idx = lowerInExpr(n.Idx, active, pc)
	case *ir.Conversion:
		n.Operand = lowerInExpr(n.Operand, active, pc)
	case *ir.StructLit:
		for i := range n.Fields {
			n.Fields[i].Value = lowerInExpr(n.Fields[i].Value, active, pc)
		}
	case *ir.ListLit:
		for i := range n.Elems {
			n.Elems[i] = lowerInExpr(n.Elems[i], active, pc)
		}
	case *ir.MapLitIR:
		for i := range n.Entries {
			n.Entries[i].Key = lowerInExpr(n.Entries[i].Key, active, pc)
			n.Entries[i].Value = lowerInExpr(n.Entries[i].Value, active, pc)
		}
	case *ir.Spread:
		n.Operand = lowerInExpr(n.Operand, active, pc)
	case *ir.Lambda:
		// Lambda survives NoContext when NoLambda cap is off; thread ctx
		// args into its body.
		if n.Func != nil {
			n.Func.Block = lowerInStmts(n.Func.Block, active, pc)
		}
	case *ir.Closure:
		// Closure (post-NoLambda) — captured-state field exprs may need
		// threading; the lifted Func is walked separately via pkg.Funcs.
		if n.State != nil {
			for i := range n.State.Fields {
				n.State.Fields[i].Value = lowerInExpr(n.State.Fields[i].Value, active, pc)
			}
		}
	case *ir.ContextRead:
		// Inside an active provider scope, replace the ContextRead with the
		// provider's current value expression. rewriteReads handles direct
		// readers outside any provider (replacing with the hidden-param
		// Ident); this branch handles inside-provider reads, which the
		// reachability collector deliberately skips because they are
		// "shielded" by the provider — but the provider's value is what
		// they should actually resolve to.
		if val, ok := active[n.Ref]; ok {
			return val
		}
		// No active provider for this ctx in scope (the read sits outside
		// any provider in this lowering scope but reachability didn't mark
		// the surrounding scope as a reader). Leave as-is; downstream
		// codegen will surface this via its unhandled-IR panic if it
		// matters.
	case *ir.Ident:
		if fn, ok := n.Sym.(*ir.Func); ok && len(pc.hidden.funcs[fn]) > 0 && fn.Receiver == "" {
			return funcValueUnder(n, fn, active, pc)
		}
	case *ir.Literal:
	default:
		panic(fmt.Sprintf("lowerInExpr: unhandled %T", n))
	}
	return e
}

func lowerInHandler(h *ir.EventHandler, active map[*ir.Context]ir.Expr, pc *provLower) {
	if h != nil && h.Func != nil {
		h.Func.Block = lowerInStmts(h.Func.Block, active, pc)
	}
}

// lowerCallInPlace threads hidden __ctx_<name> args onto a func call site
// for every context in Reach(callee). active provides the current value
// expression for each context. Only stdlib-wrapper callees (those that
// actually carry the hidden Param) receive threading — user pkg.Funcs
// pick up the hidden state from the pkg-level synth Var directly and
// expose no Param to thread into.
func lowerCallInPlace(c *ir.Call, active map[*ir.Context]ir.Expr, pc *provLower) {
	fn := calledFunc(c)
	if fn == nil {
		return
	}
	hidden := pc.hidden.funcs[fn]
	for _, ctx := range pc.order {
		p := hidden[ctx]
		if p == nil || hasCallArgNamed(c.Args, p.Name) {
			continue
		}
		val, ok := active[ctx]
		if !ok {
			val = pc.defaults[ctx]
		}
		c.Args = append(c.Args, ir.CallArg{Name: p.Name, Value: ir.CloneExprSharingDecls(val)})
	}
}

// calledFunc is the declaration a call invokes directly: its Func, or the one
// a bare callee names, which is how the checker leaves a component method.
func calledFunc(c *ir.Call) *ir.Func {
	if c == nil {
		return nil
	}
	if c.Func != nil {
		return c.Func
	}
	if id, ok := c.Callee.(*ir.Ident); ok {
		fn, _ := id.Sym.(*ir.Func)
		return fn
	}
	return nil
}

// funcValueUnder is fn taken as a value where active is in scope: a lambda
// calling fn with that scope's values. A func value's caller cannot know which
// function it holds, so it has nothing to thread; the value is therefore
// bound where it is taken, the same answer a lambda written there gets.
func funcValueUnder(id *ir.Ident, fn *ir.Func, active map[*ir.Context]ir.Expr, pc *provLower) ir.Expr {
	hidden := map[*ir.Param]bool{}
	for _, p := range pc.hidden.funcs[fn] {
		hidden[p] = true
	}
	wrapper := &ir.Func{Return: fn.Return, Purity: fn.Purity, CanError: fn.CanError, IsAsync: fn.IsAsync}
	call := &ir.Call{Type: fn.Return, Func: fn}
	for _, p := range fn.Params {
		if hidden[p] {
			continue
		}
		np := &ir.Param{Name: p.Name, Type: p.Type}
		wrapper.Params = append(wrapper.Params, np)
		call.Args = append(call.Args, ir.CallArg{Name: p.Name, Value: &ir.Ident{Name: np.Name, Type: np.Type, Sym: np}})
	}
	lowerCallInPlace(call, active, pc)
	if fn.Return == nil || fn.Return.Kind == ir.TypeVoid {
		wrapper.Block = []ir.Stmt{&ir.CallStmt{Call: call}}
	} else {
		wrapper.Block = []ir.Stmt{&ir.Return{Value: call}}
	}
	return &ir.Lambda{Type: id.Type, Func: wrapper}
}

// testSetContext recognises `t.setContext(ctx, value)`, returning the context
// and the argument holding the value. The context argument names the context
// rather than reading it, so it becomes the name: a read would outlive the
// pass, and a test lowering needs only which context it was.
func testSetContext(c *ir.Call) (*ir.Context, *ir.CallArg) {
	i, ctx := setContextArg(c)
	if ctx == nil {
		return nil, nil
	}
	c.Args[i].Value = &ir.Literal{Type: ir.TypString, Value: ctx.Name}
	return ctx, &c.Args[i+1]
}

// setContextArg is the index of the argument naming the context
// `t.setContext` sets, and that context.
func setContextArg(c *ir.Call) (int, *ir.Context) {
	if c == nil || c.Func == nil || c.Func.Receiver != "Test" || c.Func.Name != "setContext" {
		return 0, nil
	}
	for i := range c.Args {
		if cr, ok := c.Args[i].Value.(*ir.ContextRead); ok && i+1 < len(c.Args) {
			return i, cr.Ref
		}
	}
	return 0, nil
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

// providedContextVars is the hidden context vars of comp that a call site
// supplies, each with the value it supplies.
//
// A spliced body reads the supplied expression in place of the var. Hoisted
// as a var initialized from it instead, the value was copied once and never
// again, so a provider whose value reads state showed its first value
// forever. A context cannot be assigned, so nothing a substitution would
// miss writes one.
//
// Only a value that is safe to evaluate at every read: the default window
// roots are seeded with is `#locale`'s `defaultLocale()`, which asked the
// environment once per translated string when substituted.
func providedContextVars(comp *ir.Component, args []ir.Arg) map[*ir.Var]ir.Expr {
	var out map[*ir.Var]ir.Expr
	for _, v := range comp.Vars {
		if !v.Synthesized || !strings.HasPrefix(v.Name, "__ctx_") {
			continue
		}
		for _, arg := range args {
			if arg.Name == v.Name && rereadable(arg.Value) {
				if out == nil {
					out = map[*ir.Var]ir.Expr{}
				}
				out[v] = arg.Value
				break
			}
		}
	}
	return out
}

func substituteVars(stmts []ir.Stmt, vals map[*ir.Var]ir.Expr) []ir.Stmt {
	if len(vals) == 0 {
		return stmts
	}
	w := newExprWalker(func(e ir.Expr) ir.Expr {
		if id, ok := e.(*ir.Ident); ok {
			if v, ok := id.Sym.(*ir.Var); ok {
				if val, ok := vals[v]; ok {
					return deepCloneExpr(val)
				}
			}
		}
		return e
	})
	return w.stmts(stmts)
}

func substituteVarsExpr(e ir.Expr, vals map[*ir.Var]ir.Expr) ir.Expr {
	if e == nil || len(vals) == 0 {
		return e
	}
	tmp := substituteVars([]ir.Stmt{&ir.LocalVar{Init: e}}, vals)
	return tmp[0].(*ir.LocalVar).Init
}

// rereadable reports whether e calls nothing but pure functions, so reading
// it twice is reading one value twice.
func rereadable(e ir.Expr) bool {
	ok := true
	_ = ir.WalkExprs(e, func(x ir.Expr) error {
		if c, isCall := x.(*ir.Call); isCall && (c.Func == nil || c.Func.Purity != ir.PurityPure) {
			ok = false
			return ir.SkipAll
		}
		return nil
	})
	return ok
}

// sortedContexts is active's contexts by name, then by declaration where two
// share one.
func sortedContexts(active map[*ir.Context]ir.Expr) []*ir.Context {
	out := make([]*ir.Context, 0, len(active))
	for ctx := range active {
		out = append(out, ctx)
	}
	slices.SortStableFunc(out, func(a, b *ir.Context) int { return strings.Compare(a.Name, b.Name) })
	return out
}
