package lower

import "maps"

import "git.duckfam.us/jonathan/sngl/ir"

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

	reach := computeReachability(pkg)
	addHiddenParams(pkg, reach)
	rewriteReads(pkg, reach)
	lowerProviders(pkg, reach)

	pkg.Contexts = nil
	return nil
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
// shadowing it).
func computeReachability(pkg *ir.Package) Reachable {
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
	for _, fn := range pkg.Funcs {
		if !hasBody(fn) {
			continue
		}
		for ctx := range directContextReads(fn.Block) {
			reach.Funcs[ctx][fn] = true
		}
	}

	// 2. Fixpoint: propagate through call graph, respecting shadowing.
	changed := true
	for changed {
		changed = false
		// Component callers.
		for _, caller := range pkg.Components {
			for _, call := range callsInBody(caller.Body, nil) {
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
		// Func callers. (Func bodies cannot host providers, so shadowed is
		// always empty for calls inside funcs — but callsInBody still
		// handles that uniformly.)
		for _, caller := range pkg.Funcs {
			if !hasBody(caller) {
				continue
			}
			for _, call := range callsInBody(caller.Block, nil) {
				for _, ctx := range pkg.Contexts {
					if call.shadowed[ctx] {
						continue
					}
					// A func can only call other funcs, but defensively
					// allow comp edges (always false in practice).
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
// each context. Idempotent: skips if already present.
func addHiddenParams(pkg *ir.Package, reach Reachable) {
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
		for _, fn := range pkg.Funcs {
			if !reach.Funcs[ctx][fn] {
				continue
			}
			if hasFuncParam(fn, paramName) {
				continue
			}
			fn.Params = append(fn.Params, &ir.Param{
				Name: paramName,
				Type: ctx.Typ,
			})
		}
	}
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
// that refers to the hidden context prop. This matches the shape the checker
// uses when declaring props as params in component scope.
func makeHiddenParamSym(ctx *ir.Context) *ir.Param {
	return &ir.Param{
		Name: "__ctx_" + ctx.Name,
		Type: ctx.Typ,
	}
}

// --- Read rewriting ---

// rewriteReads replaces every *ir.ContextRead{Ref: ctx} in each component
// and func in Reach(ctx) with an *ir.Ident referencing the hidden param.
func rewriteReads(pkg *ir.Package, reach Reachable) {
	for _, ctx := range pkg.Contexts {
		paramSym := makeHiddenParamSym(ctx)
		paramIdent := func() ir.Expr {
			return &ir.Ident{
				Name:        "__ctx_" + ctx.Name,
				Type:        ctx.Typ,
				Sym:         paramSym,
				Synthesized: true,
			}
		}
		transform := func(e ir.Expr) ir.Expr {
			cr, ok := e.(*ir.ContextRead)
			if !ok || cr.Ref != ctx {
				return e
			}
			return paramIdent()
		}
		for _, comp := range pkg.Components {
			if !reach.Components[ctx][comp] {
				continue
			}
			w := newExprWalker(transform)
			comp.Body = w.stmts(comp.Body)
		}
		for _, fn := range pkg.Funcs {
			if !reach.Funcs[ctx][fn] {
				continue
			}
			if !hasBody(fn) {
				continue
			}
			w := newExprWalker(transform)
			fn.Block = w.stmts(fn.Block)
		}
	}
}

// --- Provider lowering + root defaults ---

// lowerProviders rewrites ContextProvider nodes to plain children, threading
// the context value as a hidden arg onto every reachable NodeInst (component
// call) and Call (func call) inside. Window roots are seeded with ctx.Default
// for each context. Func bodies thread the value of their __ctx_<name>
// param down to any Reach-callees they themselves invoke.
func lowerProviders(pkg *ir.Package, reach Reachable) {
	// Build default map: ctx → ctx.Default expression.
	defaults := make(map[*ir.Context]ir.Expr, len(pkg.Contexts))
	for _, ctx := range pkg.Contexts {
		defaults[ctx] = ctx.Default
	}

	// Seed window roots with defaults.
	for _, w := range pkg.Windows {
		w.Body = lowerInStmts(w.Body, copyExprMap(defaults), reach)
	}

	// For component bodies: no active context value at the entry point —
	// each component receives its value via the hidden prop threaded from
	// its caller. But within the body, calls to func/component Reach
	// targets thread the prop value (an Ident reading the hidden prop).
	for _, comp := range pkg.Components {
		// Inside this component's body, each ctx for which this component
		// is in Reach is active and references the hidden prop.
		compActive := hiddenActiveFor(pkg, reach, comp, nil)
		comp.Body = lowerInStmts(comp.Body, compActive, reach)
	}

	// For func bodies: same — each ctx for which this func has a hidden
	// param is active as an Ident reading that param.
	for _, fn := range pkg.Funcs {
		if !hasBody(fn) {
			continue
		}
		fnActive := hiddenActiveFor(pkg, reach, nil, fn)
		fn.Block = lowerInStmts(fn.Block, fnActive, reach)
	}
}

// hiddenActiveFor builds the active-context map at the entry of a component
// or func body: each context for which the callee is in Reach is active and
// references the hidden param by Ident. Exactly one of comp / fn is non-nil.
func hiddenActiveFor(pkg *ir.Package, reach Reachable, comp *ir.Component, fn *ir.Func) map[*ir.Context]ir.Expr {
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
		out[ctx] = &ir.Ident{
			Name:        "__ctx_" + ctx.Name,
			Type:        ctx.Typ,
			Sym:         makeHiddenParamSym(ctx),
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
