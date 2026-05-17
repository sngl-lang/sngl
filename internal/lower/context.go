package lower

import "maps"

import "git.duckfam.us/jonathan/sngl/ir"

// passNoContext lowers context declarations to hidden component props.
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
//  1. Compute per-context reachability over the call graph.
//  2. Add a synthesized __ctx_<name> *ir.Prop to every component in Reach(ctx).
//  3. Rewrite *ir.ContextRead{Ref: ctx} inside those components into reads of
//     the corresponding hidden prop (as *ir.Ident with *ir.Param Sym).
//  4. Rewrite *ir.ContextProvider nodes: splice out the provider, thread
//     __ctx_<name>=value as an extra Arg onto every reachable NodeInst inside.
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

// componentCall records one component call site inside a caller's body,
// together with the set of contexts that are shadowed at that call site by an
// enclosing ContextProvider.
type componentCall struct {
	callee   *ir.Component
	shadowed map[*ir.Context]bool
}

// computeReachability returns, for each Context, the set of Components that
// transitively read that context (without an intervening provider shadowing it).
func computeReachability(pkg *ir.Package) map[*ir.Context]map[*ir.Component]bool {
	reach := make(map[*ir.Context]map[*ir.Component]bool, len(pkg.Contexts))
	for _, ctx := range pkg.Contexts {
		reach[ctx] = make(map[*ir.Component]bool)
	}

	// 1. Mark direct readers.
	for _, comp := range pkg.Components {
		for ctx := range directContextReads(comp) {
			reach[ctx][comp] = true
		}
	}

	// 2. Fixpoint: propagate through call graph, respecting shadowing.
	changed := true
	for changed {
		changed = false
		for _, caller := range pkg.Components {
			for _, call := range componentCallsInBody(caller.Body, nil) {
				for _, ctx := range pkg.Contexts {
					if call.shadowed[ctx] {
						continue
					}
					if reach[ctx][call.callee] && !reach[ctx][caller] {
						reach[ctx][caller] = true
						changed = true
					}
				}
			}
		}
	}
	return reach
}

// directContextReads returns the set of contexts directly read in comp's body
// (not counting transitive calls).
func directContextReads(comp *ir.Component) map[*ir.Context]bool {
	result := map[*ir.Context]bool{}
	collectContextReadsInStmts(comp.Body, result)
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
	case *ir.Call:
		// A call to a function with non-empty ReadsContexts is treated as a
		// direct read of each listed context — the same way a bare ContextRead
		// node is. This is how i18n.* calls (which need the active locale)
		// pull components that call them into Reach(locale).
		if n.Func != nil {
			for _, ctx := range n.Func.ReadsContexts {
				out[ctx] = true
			}
		}
		collectContextReadsInExpr(n.Receiver, out)
		collectContextReadsInExpr(n.Callee, out)
		for _, a := range n.Args {
			collectContextReadsInExpr(a.Value, out)
		}
		return
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

// componentCallsInBody returns all component call sites reachable from stmts,
// each annotated with the set of contexts shadowed at that site by enclosing
// ContextProvider nodes. shadow is the current shadow set (copied on descent).
func componentCallsInBody(stmts []ir.Stmt, shadow map[*ir.Context]bool) []componentCall {
	var calls []componentCall
	for _, s := range stmts {
		calls = append(calls, componentCallsInStmt(s, shadow)...)
	}
	return calls
}

func componentCallsInStmt(s ir.Stmt, shadow map[*ir.Context]bool) []componentCall {
	switch n := s.(type) {
	case *ir.NodeInst:
		var calls []componentCall
		if n.Component != nil {
			calls = append(calls, componentCall{callee: n.Component, shadowed: shadow})
		}
		// Recurse into children (may contain nested user-component calls).
		calls = append(calls, componentCallsInBody(n.Children, shadow)...)
		for _, h := range n.Handlers {
			if h.Func != nil {
				calls = append(calls, componentCallsInBody(h.Func.Block, shadow)...)
			}
		}
		return calls
	case *ir.ContextProvider:
		// Descend into children with this context shadowed.
		inner := copyContextShadow(shadow)
		inner[n.Ref] = true
		return componentCallsInBody(n.Children, inner)
	case *ir.If:
		calls := componentCallsInBody(n.Body, shadow)
		calls = append(calls, componentCallsInBody(n.Else, shadow)...)
		return calls
	case *ir.For:
		calls := componentCallsInBody(n.Body, shadow)
		calls = append(calls, componentCallsInBody(n.Else, shadow)...)
		return calls
	case *ir.PlatformFilter:
		return componentCallsInBody(n.Body, shadow)
	case *ir.SlotInst:
		return componentCallsInBody(n.Children, shadow)
	case *ir.ErrorBoundary:
		return componentCallsInBody(n.Children, shadow)
	}
	return nil
}

func copyContextShadow(m map[*ir.Context]bool) map[*ir.Context]bool {
	out := make(map[*ir.Context]bool, len(m)+1)
	maps.Copy(out, m)
	return out
}

// --- Hidden params ---

// addHiddenParams adds a synthesized __ctx_<name> *ir.Prop to every component
// in Reach(ctx) for each context. Idempotent: skips if prop already present.
func addHiddenParams(pkg *ir.Package, reach map[*ir.Context]map[*ir.Component]bool) {
	for _, ctx := range pkg.Contexts {
		paramName := "__ctx_" + ctx.Name
		for _, comp := range pkg.Components {
			if !reach[ctx][comp] {
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

// rewriteReads replaces every *ir.ContextRead{Ref: ctx} in each component in
// Reach(ctx) with an *ir.Ident referencing the hidden prop param.
func rewriteReads(pkg *ir.Package, reach map[*ir.Context]map[*ir.Component]bool) {
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
		for _, comp := range pkg.Components {
			if !reach[ctx][comp] {
				continue
			}
			w := newExprWalker(func(e ir.Expr) ir.Expr {
				cr, ok := e.(*ir.ContextRead)
				if !ok || cr.Ref != ctx {
					return e
				}
				return paramIdent()
			})
			comp.Body = w.stmts(comp.Body)
		}
	}
}

// --- Provider lowering + root defaults ---

// lowerProviders rewrites ContextProvider nodes to plain children, threading
// the context value as a hidden arg onto every reachable NodeInst component
// call inside. Window roots are seeded with ctx.Default for each context.
func lowerProviders(pkg *ir.Package, reach map[*ir.Context]map[*ir.Component]bool) {
	// Build default map: ctx → ctx.Default expression.
	defaults := make(map[*ir.Context]ir.Expr, len(pkg.Contexts))
	for _, ctx := range pkg.Contexts {
		defaults[ctx] = ctx.Default
	}

	// Seed window roots with defaults (consumers without an enclosing provider
	// receive the context's declared default value).
	for _, w := range pkg.Windows {
		w.Body = lowerInStmts(w.Body, copyExprMap(defaults), reach)
	}

	// When there are no explicit window declarations, the codegen treats the
	// "main" component body as the window root directly. In that case, seed
	// its body with defaults (not __ctx_<name> idents) so that i18n.* calls
	// inside the body receive a concrete value (e.g. i18n.defaultLocale())
	// rather than an undefined variable reference.
	isWindowRootComp := make(map[*ir.Component]bool)
	if len(pkg.Windows) == 0 {
		for _, comp := range pkg.Components {
			if comp.Name == "main" {
				isWindowRootComp[comp] = true
			}
		}
	}

	// For component bodies: seed the active map with the hidden prop ident for
	// each context the component is in Reach of. This ensures that i18n.*
	// calls inside a component body receive the component-level __ctx_locale
	// ident as their locale arg, rather than getting no arg at all.
	//
	// Exception: window-root components are seeded with the context defaults
	// instead, since no parent component passes them the hidden prop.
	//
	// Note: ContextRead nodes are already rewritten to __ctx_<name> Idents by
	// rewriteReads (which runs before lowerProviders). The active map here only
	// affects ReadsContexts-based threading (i.e., implicit i18n args).
	for _, comp := range pkg.Components {
		var compActive map[*ir.Context]ir.Expr
		if isWindowRootComp[comp] {
			compActive = copyExprMap(defaults)
		} else {
			compActive = map[*ir.Context]ir.Expr{}
			for _, ctx := range pkg.Contexts {
				if reach[ctx][comp] {
					compActive[ctx] = &ir.Ident{
						Name:        "__ctx_" + ctx.Name,
						Type:        ctx.Typ,
						Sym:         makeHiddenParamSym(ctx),
						Synthesized: true,
					}
				}
			}
		}
		comp.Body = lowerInStmts(comp.Body, compActive, reach)
	}
}

// lowerInStmts recursively lowers ContextProvider nodes and threads context
// args onto component-call NodeInsts. active maps each context to its current
// value expression at this point in the tree.
func lowerInStmts(stmts []ir.Stmt, active map[*ir.Context]ir.Expr, reach map[*ir.Context]map[*ir.Component]bool) []ir.Stmt {
	exprRewriter := makeI18nCallRewriter(active)
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
					if !reach[ctx][n.Component] {
						continue
					}
					paramName := "__ctx_" + ctx.Name
					// Only append if not already present (avoid duplicates on
					// repeated lowering, though the pass should only run once).
					if !hasArgNamed(n.Props, paramName) {
						n.Props = append(n.Props, ir.Arg{Name: paramName, Value: val})
					}
				}
			}
			// Rewrite i18n.* calls inside prop values at this node.
			for i := range n.Props {
				n.Props[i].Value = exprRewriter.expr(n.Props[i].Value)
			}
			// Rewrite i18n.* calls in handler blocks.
			for _, h := range n.Handlers {
				if h.Func != nil {
					h.Func.Block = exprRewriter.stmts(h.Func.Block)
				}
			}
			// Recurse into children (primitive containers may hold component calls).
			n.Children = lowerInStmts(n.Children, active, reach)
			out = append(out, n)

		case *ir.If:
			n.Cond = exprRewriter.expr(n.Cond)
			n.Body = lowerInStmts(n.Body, active, reach)
			n.Else = lowerInStmts(n.Else, active, reach)
			out = append(out, n)

		case *ir.For:
			n.Iter = exprRewriter.expr(n.Iter)
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

		default:
			// For all other statement types (Assign, LocalVar, Return, CallStmt,
			// Emit, etc.), rewrite any i18n.* calls in their expressions.
			lowerI18nCallsInStmt(s, exprRewriter)
			out = append(out, n)
		}
	}
	return out
}

// makeI18nCallRewriter returns an exprWalker whose transform appends the
// active locale as a trailing CallArg to every *ir.Call whose Func has a
// non-empty ReadsContexts list. The arg is named "__ctx_<name>" (matching
// the hidden prop convention) so codegen can locate it positionally.
//
// The walker mutates the call in place and returns the same node so that the
// walker continues to recurse into nested subexpressions (e.g. a call whose
// argument is itself an i18n call).
func makeI18nCallRewriter(active map[*ir.Context]ir.Expr) *exprWalker {
	return newExprWalker(func(e ir.Expr) ir.Expr {
		call, ok := e.(*ir.Call)
		if !ok || call.Func == nil || len(call.Func.ReadsContexts) == 0 {
			return e
		}
		for _, ctx := range call.Func.ReadsContexts {
			val, inActive := active[ctx]
			if !inActive {
				// Context has no active value at this site — no threading needed.
				continue
			}
			argName := "__ctx_" + ctx.Name
			// Avoid duplicate injection on repeated passes.
			if !hasCallArgNamed(call.Args, argName) {
				call.Args = append(call.Args, ir.CallArg{Name: argName, Value: val})
			}
		}
		// Return same node so walker recurses into (now-extended) args.
		return e
	})
}

// hasCallArgNamed reports whether args already contains a named CallArg with
// the given name.
func hasCallArgNamed(args []ir.CallArg, name string) bool {
	for _, a := range args {
		if a.Name == name {
			return true
		}
	}
	return false
}

// lowerI18nCallsInStmt applies exprRewriter to all expressions reachable from
// the given statement. Used for statement types not otherwise handled by the
// lowerInStmts switch (Assign, LocalVar, Return, CallStmt, Emit, etc.).
func lowerI18nCallsInStmt(s ir.Stmt, w *exprWalker) {
	switch n := s.(type) {
	case *ir.Assign:
		n.Target = w.expr(n.Target)
		n.Value = w.expr(n.Value)
	case *ir.LocalVar:
		n.Init = w.expr(n.Init)
	case *ir.Return:
		n.Value = w.expr(n.Value)
	case *ir.CallStmt:
		// w.expr visits the *ir.Call node, applies the transform (which appends
		// the locale arg if ReadsContexts is set), then recurses into children.
		if n.Call != nil {
			w.expr(n.Call)
		}
	case *ir.Emit:
		for i := range n.Args {
			n.Args[i].Value = w.expr(n.Args[i].Value)
		}
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
