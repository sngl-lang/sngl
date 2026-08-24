package optimize

import (
	"fmt"
	"slices"

	"git.duckfam.us/jonathan/sngl/ir"
)

// maxInlineDepth caps how deep component-call inlining may recurse before
// the optimizer falls back to the unspecialized call. Guards against
// pathological self-referential components.
const maxInlineDepth = 8

// findComponentPkg searches root and its transitive imports for the package
// that declares comp. Returns nil if not found. Used so cross-package
// inlining resolves native imports against the body's owning package.
func findComponentPkg(root *ir.Package, comp *ir.Component) *ir.Package {
	if root == nil {
		return nil
	}
	visited := map[*ir.Package]bool{}
	var walk func(p *ir.Package) *ir.Package
	walk = func(p *ir.Package) *ir.Package {
		if p == nil || visited[p] {
			return nil
		}
		visited[p] = true
		if slices.Contains(p.Components, comp) {
			return p
		}
		for _, imp := range p.Imports {
			if got := walk(imp.Pkg); got != nil {
				return got
			}
		}
		return nil
	}
	return walk(root)
}

// inlineComponentCall attempts to replace a user-component NodeInst with a
// cloned-and-folded copy of the component's body, with the call's prop
// values bound into the body's parameter symbols. Returns nil when the call
// is not a worthwhile inlining target — caller should leave the NodeInst as
// a regular runtime component instantiation.
//
// The motivating case: a `for x = entries` inside the component body, where
// `entries` is a Prop receiving a compile-time const at the call site. After
// inlining, foldStmts on the cloned body unrolls the loop using the bound
// prop value, and the call site dissolves into ordinary platform NodeInsts.
func inlineComponentCall(n *ir.NodeInst, ctx *evalCtx) []ir.Stmt {
	comp := n.Component
	if comp == nil {
		return nil
	}
	if ctx.inlining[comp] >= maxInlineDepth {
		return nil
	}
	// Stateful components (Vars/Funcs/Timers) carry per-instance state that
	// must be hoisted into the surrounding scope's state container. The
	// optimizer's body-substitution path doesn't clone state — it only
	// splices body statements. Leave stateful components to the lowering
	// pass `passNoInlineComponents`, which properly clones Vars/Funcs/Timers
	// into main with per-call-site rename suffixes.
	if len(comp.Vars) > 0 || len(comp.Funcs) > 0 || len(comp.Timers) > 0 {
		return nil
	}

	propNames := make(map[string]bool, len(comp.Props))
	for _, p := range comp.Props {
		propNames[p.Name] = true
	}

	// Pre-check: only inline when binding the prop into the body would
	// unlock further compile-time folding — either a `for x = param`
	// unroll, or a pure native call whose argument is a param. Otherwise
	// leave the call as-is so component sharing is preserved.
	if !bodyHasFoldableParamUse(comp.Body, propNames) {
		return nil
	}

	// Fold and evaluate each provided prop. Bind whatever is fold-evaluable
	// into the child context — non-const props remain runtime parameters
	// and don't unlock any inlining benefit, but they don't block it either.
	propValues := make(map[string]any, len(n.Props))
	for _, p := range n.Props {
		v := foldExpr(p.Value, ctx)
		if val, ok := evalExpr(v, ctx); ok {
			propValues[p.Name] = val
		}
	}
	// If no provided prop folded to a const, the body's for-loop won't
	// unroll either — bail.
	if len(propValues) == 0 {
		return nil
	}

	paramSyms := findParamSyms(comp.Body, propNames)
	if len(paramSyms) == 0 {
		return nil
	}

	cloned := cloneStmts(comp.Body)

	// When inlining a cross-package component, native-call resolution
	// inside the body (e.g. `docs.Highlight(...)`) needs to consult the
	// imports of the component's owning package, not the caller's. Find
	// the owning pkg via a depth-first walk; fall back to the caller's
	// when the component is local.
	bodyPkg := findComponentPkg(ctx.pkg, comp)
	if bodyPkg == nil {
		bodyPkg = ctx.pkg
	}

	childCtx := ctx.childInPkg(bodyPkg)
	childCtx.inlining[comp] = ctx.inlining[comp] + 1
	for name, val := range propValues {
		if sym, ok := paramSyms[name]; ok {
			childCtx.values[sym] = val
		}
	}

	cloned = substituteSlots(cloned, n.Children)
	// Component-body platform override at inline time: optimize splices the
	// (cloned) body into the parent tree here, before lower's passPlatformFilter
	// runs, so the override must be resolved now while the component boundary is
	// still intact. passPlatformFilter handles every other path (lower-inlined
	// and non-inlined component bodies); both apply the same override rule.
	cloned = applyPlatformOverride(cloned, ctx.platform)

	// Bind non-const props by substituting their parameter references with the
	// call-site argument expressions. Const props are bound via childCtx.values
	// during foldStmts; non-const props (e.g. a reactive `value=posts`) have no
	// const value to fold, so without this their param refs dangle in the
	// inlined body — the prop renders empty and never reacts. Substitution runs
	// before foldStmts so a substituted expr that turns out constant still folds.
	subs := make(map[*ir.Param]ir.Expr)
	for _, p := range n.Props {
		if _, isConst := propValues[p.Name]; isConst {
			continue
		}
		if param, ok := paramSyms[p.Name]; ok {
			subs[param] = p.Value
		}
	}
	if len(subs) > 0 {
		substituteParamsInStmts(cloned, subs)
	}

	folded := foldStmts(cloned, childCtx)

	ctx.fileAssets = childCtx.fileAssets
	if ctx.err == nil {
		ctx.err = childCtx.err
	}
	return folded
}

// substituteParamsInStmts replaces parameter references in a (cloned) component
// body with their call-site argument expressions, recursing through nested
// statement lists. Mirrors substituteParams (which handles expression trees)
// at the statement level. Mutates in place; operates on cloned IR.
func substituteParamsInStmts(stmts []ir.Stmt, subs map[*ir.Param]ir.Expr) {
	for _, s := range stmts {
		substituteParamsInStmt(s, subs)
	}
}

func substituteParamsInStmt(s ir.Stmt, subs map[*ir.Param]ir.Expr) {
	switch n := s.(type) {
	case *ir.NodeInst:
		for i := range n.Props {
			n.Props[i].Value = substituteParams(n.Props[i].Value, subs)
		}
		n.Key = substituteParams(n.Key, subs)
		n.Ref = substituteParams(n.Ref, subs)
		substituteParamsInStmts(n.Children, subs)
		for _, h := range n.Handlers {
			if h.Func != nil {
				substituteParamsInStmts(h.Func.Block, subs)
			}
		}
	case *ir.If:
		n.Cond = substituteParams(n.Cond, subs)
		substituteParamsInStmts(n.Body, subs)
		substituteParamsInStmts(n.Else, subs)
	case *ir.For:
		n.Iter = substituteParams(n.Iter, subs)
		substituteParamsInStmts(n.Body, subs)
		substituteParamsInStmts(n.Else, subs)
	case *ir.Assign:
		n.Target = substituteParams(n.Target, subs)
		n.Value = substituteParams(n.Value, subs)
	case *ir.Return:
		n.Value = substituteParams(n.Value, subs)
	case *ir.LocalVar:
		n.Init = substituteParams(n.Init, subs)
	case *ir.CallStmt:
		if n.Call != nil {
			if c, ok := substituteParams(n.Call, subs).(*ir.Call); ok {
				n.Call = c
			}
		}
	case *ir.Emit:
		for i := range n.Args {
			n.Args[i].Value = substituteParams(n.Args[i].Value, subs)
		}
	case *ir.Toggle:
		n.Target = substituteParams(n.Target, subs)
	case *ir.PlatformFilter:
		substituteParamsInStmts(n.Body, subs)
	case *ir.SlotInst:
		substituteParamsInStmts(n.Children, subs)
	case *ir.ContextProvider:
		n.Value = substituteParams(n.Value, subs)
		substituteParamsInStmts(n.Children, subs)
	case *ir.ErrorBoundary:
		substituteParamsInStmts(n.Children, subs)
	case *ir.Window:
		n.Href = substituteParams(n.Href, subs)
		n.Title = substituteParams(n.Title, subs)
		n.Favicon = substituteParams(n.Favicon, subs)
		substituteParamsInStmts(n.Body, subs)
	case *ir.CanvasRedrawStmt:
		// No params to substitute.
	default:
		panic(fmt.Sprintf("substituteParamsInStmt: unhandled stmt %T", n))
	}
}

// applyPlatformOverride mirrors codegen's irPlatformBody: if the cloned
// component body contains any platform-filter override, drop the
// cross-platform default statements so only the override survives. Without
// this, splicing the inlined body straight into the parent statement slice
// causes the default and the override to both render — unlike codegen's
// component path, which routes through irPlatformBody at every call.
func applyPlatformOverride(stmts []ir.Stmt, platform string) []ir.Stmt {
	if platform == "" {
		return stmts
	}
	hasFilter := false
	for _, s := range stmts {
		if _, ok := s.(*ir.PlatformFilter); ok {
			hasFilter = true
			break
		}
	}
	if !hasFilter {
		return stmts
	}
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		pf, ok := s.(*ir.PlatformFilter)
		if !ok {
			continue // drop cross-platform default; the filter wins
		}
		if pf.Platform == platform {
			out = append(out, pf.Body...)
		}
	}
	return out
}

// bodyHasFoldableParamUse reports whether stmts contain a use of one of the
// named params that could fold to a constant if the param is bound. Patterns
// that count:
//
//  1. a for-loop whose Iter is the param ident (unrolls to static stmts);
//  2. an if-cond that mentions the param (folds to a static branch);
//  3. a call argument that is or contains the param ident (the call may
//     then evaluate at compile time, e.g. a pure Go helper);
//  4. a binary/select/index/conversion where the param appears (operand
//     becomes a literal once bound).
//
// Anything covered above turns into structurally different output once the
// param is bound; everything else keeps the unspecialized component shared.
func bodyHasFoldableParamUse(stmts []ir.Stmt, propNames map[string]bool) bool {
	found := false
	var visitStmts func(stmts []ir.Stmt)
	var visitExpr func(e ir.Expr)
	isParamIdent := func(e ir.Expr) bool {
		id, ok := e.(*ir.Ident)
		if !ok || id.Sym == nil {
			return false
		}
		p, ok := id.Sym.(*ir.Param)
		return ok && propNames[p.Name]
	}
	visitExpr = func(e ir.Expr) {
		if found || e == nil {
			return
		}
		switch x := e.(type) {
		case *ir.Call:
			for _, a := range x.Args {
				if isParamIdent(a.Value) {
					found = true
					return
				}
				visitExpr(a.Value)
			}
			visitExpr(x.Receiver)
		case *ir.Binary:
			if isParamIdent(x.Left) || isParamIdent(x.Right) {
				found = true
				return
			}
			visitExpr(x.Left)
			visitExpr(x.Right)
		case *ir.Unary:
			if isParamIdent(x.Operand) {
				found = true
				return
			}
			visitExpr(x.Operand)
		case *ir.Ternary:
			if isParamIdent(x.Cond) {
				found = true
				return
			}
			visitExpr(x.Cond)
			visitExpr(x.Then)
			visitExpr(x.Else)
		case *ir.Conversion:
			if isParamIdent(x.Operand) {
				found = true
				return
			}
			visitExpr(x.Operand)
		case *ir.Select:
			if isParamIdent(x.Operand) {
				found = true
				return
			}
			visitExpr(x.Operand)
		case *ir.Index:
			if isParamIdent(x.Operand) || isParamIdent(x.Idx) {
				found = true
				return
			}
			visitExpr(x.Operand)
			visitExpr(x.Idx)
		case *ir.ListLit:
			for _, el := range x.Elems {
				visitExpr(el)
			}
		case *ir.StructLit:
			for _, f := range x.Fields {
				visitExpr(f.Value)
			}
		case *ir.MapLitIR:
			for _, kv := range x.Entries {
				visitExpr(kv.Key)
				visitExpr(kv.Value)
			}
		case *ir.Spread:
			visitExpr(x.Operand)
		case *ir.Literal, *ir.Ident, *ir.ContextRead, *ir.Lambda, *ir.Closure:
			// Literals/Idents have no param-call shape; lambda bodies
			// are opaque to this scan (they have their own scope).
		default:
			panic(fmt.Sprintf("bodyHasFoldableParamUse.visitExpr: unhandled expr %T", x))
		}
	}
	visitStmts = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			if found {
				return
			}
			switch n := s.(type) {
			case *ir.For:
				if isParamIdent(n.Iter) {
					found = true
					return
				}
				visitExpr(n.Iter)
				visitStmts(n.Body)
				visitStmts(n.Else)
			case *ir.NodeInst:
				for _, p := range n.Props {
					visitExpr(p.Value)
					if found {
						return
					}
				}
				visitStmts(n.Children)
			case *ir.If:
				if isParamIdent(n.Cond) {
					found = true
					return
				}
				visitExpr(n.Cond)
				visitStmts(n.Body)
				visitStmts(n.Else)
			case *ir.PlatformFilter:
				visitStmts(n.Body)
			case *ir.SlotInst:
				visitStmts(n.Children)
			case *ir.Window:
				visitExpr(n.Href)
				visitExpr(n.Title)
				visitExpr(n.Favicon)
				visitStmts(n.Body)
			case *ir.Assign:
				visitExpr(n.Value)
			case *ir.LocalVar:
				visitExpr(n.Init)
			case *ir.Return:
				visitExpr(n.Value)
			case *ir.CallStmt:
				if n.Call != nil {
					visitExpr(n.Call)
				}
			case *ir.ContextProvider:
				if isParamIdent(n.Value) {
					found = true
					return
				}
				visitExpr(n.Value)
				visitStmts(n.Children)
			case *ir.ErrorBoundary:
				visitStmts(n.Children)
			case *ir.Emit:
				for _, a := range n.Args {
					if isParamIdent(a.Value) {
						found = true
						return
					}
					visitExpr(a.Value)
				}
			case *ir.Toggle:
				if isParamIdent(n.Target) {
					found = true
					return
				}
				visitExpr(n.Target)
			case *ir.CanvasRedrawStmt:
				// No param exprs.
			default:
				panic(fmt.Sprintf("bodyHasFoldableParamUse.visitStmts: unhandled stmt %T", n))
			}
		}
	}
	visitStmts(stmts)
	return found
}

// findParamSyms walks stmts and collects one representative *ir.Param symbol
// per name that appears as an Ident.Sym. Mirrors findLoopVar.
func findParamSyms(stmts []ir.Stmt, propNames map[string]bool) map[string]*ir.Param {
	out := make(map[string]*ir.Param)
	walkForBody(stmts, func(e ir.Expr) {
		if id, ok := e.(*ir.Ident); ok {
			if p, ok := id.Sym.(*ir.Param); ok && propNames[p.Name] {
				if _, dup := out[p.Name]; !dup {
					out[p.Name] = p
				}
			}
		}
	})
	return out
}

// substituteSlots replaces *ir.SlotInst nodes in stmts with the call site's
// children. Operates on cloned IR, so mutation is safe.
func substituteSlots(stmts []ir.Stmt, slotChildren []ir.Stmt) []ir.Stmt {
	var out []ir.Stmt
	for _, s := range stmts {
		if _, ok := s.(*ir.SlotInst); ok {
			out = append(out, cloneStmts(slotChildren)...)
			continue
		}
		out = append(out, substituteSlotsInStmt(s, slotChildren))
	}
	return out
}

func substituteSlotsInStmt(s ir.Stmt, slotChildren []ir.Stmt) ir.Stmt {
	switch n := s.(type) {
	case *ir.NodeInst:
		n.Children = substituteSlots(n.Children, slotChildren)
	case *ir.If:
		n.Body = substituteSlots(n.Body, slotChildren)
		n.Else = substituteSlots(n.Else, slotChildren)
	case *ir.For:
		n.Body = substituteSlots(n.Body, slotChildren)
		n.Else = substituteSlots(n.Else, slotChildren)
	case *ir.PlatformFilter:
		n.Body = substituteSlots(n.Body, slotChildren)
	case *ir.Window:
		n.Body = substituteSlots(n.Body, slotChildren)
	case *ir.ContextProvider:
		n.Children = substituteSlots(n.Children, slotChildren)
	case *ir.ErrorBoundary:
		n.Children = substituteSlots(n.Children, slotChildren)
	case *ir.SlotInst:
		// Handled in substituteSlots above; if we land here it's a
		// nested slot we don't substitute through.
	case *ir.Assign, *ir.CallStmt, *ir.LocalVar, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt:
		// No child statements with slots.
	default:
		panic(fmt.Sprintf("substituteSlotsInStmt: unhandled stmt %T", n))
	}
	return s
}
