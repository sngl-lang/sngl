package optimize

import (
	"maps"
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

	childCtx := &evalCtx{
		platform:   ctx.platform,
		language:   ctx.language,
		dir:        ctx.dir,
		pkg:        bodyPkg,
		fileAssets: ctx.fileAssets,
		values:     make(map[ir.Symbol]any, len(ctx.values)+len(propValues)),
		inlining:   make(map[*ir.Component]int, len(ctx.inlining)+1),
	}
	maps.Copy(childCtx.values, ctx.values)
	maps.Copy(childCtx.inlining, ctx.inlining)
	childCtx.inlining[comp] = ctx.inlining[comp] + 1
	for name, val := range propValues {
		if sym, ok := paramSyms[name]; ok {
			childCtx.values[sym] = val
		}
	}

	cloned = substituteSlots(cloned, n.Children)
	cloned = applyPlatformOverride(cloned, ctx.platform)
	folded := foldStmts(cloned, childCtx)

	ctx.fileAssets = childCtx.fileAssets
	return folded
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
	}
	return s
}
