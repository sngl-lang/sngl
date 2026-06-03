package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passPlatformFilter resolves every ir.PlatformFilter for the active build
// platform so that no platform-conditional node survives into reactivity
// analysis, inlining, or codegen. It runs immediately after
// passPlatformExtensionBody (which swaps stdlib `component sngl.X { platform
// <p> {...} }` bodies into place) and before every visual-tree pass.
//
// Two resolution rules, matching the historical split between codegen's
// irPlatformBody (component-body override) and renderIRStmt's nested handling:
//
//   - At the TOP LEVEL of a component body, a present filter OVERRIDES the
//     cross-platform default: the non-filter sibling statements are dropped and
//     only the matching platform's body survives. This is the
//     `default body … platform html { … }` idiom.
//   - Everywhere else (nested in the visual tree, window bodies, handler/func
//     blocks) a filter is ADDITIVE: the matching platform's body is spliced in
//     place while sibling statements are kept; non-matching filters are dropped.
//
// Centralizing the collapse here means both inlining paths (optimize's
// const-fold inliner and lower's NoInlineComponents) copy already-resolved
// bodies, so a component's cross-platform default can never leak in alongside
// its platform override regardless of which path runs — the bug that arises
// when the collapse is left to a downstream pass that only some call sites hit.
var passPlatformFilter = pass{
	name:    "PlatformFilter",
	enabled: func(Caps) bool { return true },
	apply:   lowerPlatformFilter,
}

func lowerPlatformFilter(pkg *ir.Package, _ Caps, opts Options) error {
	// Platform-agnostic tools (LSP, format, multi-platform discovery) pass an
	// empty platform; leave filters intact for them, exactly as
	// passPlatformExtensionBody does.
	if pkg == nil || opts.Platform == "" {
		return nil
	}
	resolveFiltersPkg(pkg, opts.Platform, map[*ir.Package]struct{}{})
	return nil
}

// resolveFiltersPkg resolves filters in pkg and every package it imports
// (transitively); cross-package components carry their own *ir.Component
// instances, so each owning package must be walked. The seen set guards import
// cycles. Mirrors specializePkgBodies' traversal.
func resolveFiltersPkg(pkg *ir.Package, platform string, seen map[*ir.Package]struct{}) {
	if pkg == nil {
		return
	}
	if _, done := seen[pkg]; done {
		return
	}
	seen[pkg] = struct{}{}

	resolveComp := func(comp *ir.Component) {
		if comp == nil {
			return
		}
		comp.Body = resolveFilterStmts(comp.Body, platform, true)
		for _, fn := range comp.Funcs {
			if fn != nil {
				fn.Block = resolveFilterStmts(fn.Block, platform, false)
			}
		}
	}
	// Stdlib component pointers live in Symbols.Comps (shared, not in
	// pkg.Components); walk both so extensions get resolved too.
	if pkg.Symbols != nil {
		for _, sym := range pkg.Symbols.Comps {
			if comp, ok := sym.(*ir.Component); ok {
				resolveComp(comp)
			}
		}
	}
	for _, comp := range pkg.Components {
		resolveComp(comp)
	}
	for _, w := range pkg.Windows {
		if w == nil {
			continue
		}
		w.Body = resolveFilterStmts(w.Body, platform, false)
		for _, fn := range w.Funcs {
			if fn != nil {
				fn.Block = resolveFilterStmts(fn.Block, platform, false)
			}
		}
	}
	for _, fn := range pkg.Funcs {
		if fn != nil {
			fn.Block = resolveFilterStmts(fn.Block, platform, false)
		}
	}
	for _, imp := range pkg.Imports {
		if imp != nil {
			resolveFiltersPkg(imp.Pkg, platform, seen)
		}
	}
}

// resolveFilterStmts resolves platform filters in one statement list. topLevel
// marks a component-body level, where a present filter drops the default
// siblings (override semantics); elsewhere filters are additive.
func resolveFilterStmts(stmts []ir.Stmt, platform string, topLevel bool) []ir.Stmt {
	hasFilter := false
	for _, s := range stmts {
		if _, ok := s.(*ir.PlatformFilter); ok {
			hasFilter = true
			break
		}
	}
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		if pf, ok := s.(*ir.PlatformFilter); ok {
			if pf.Platform == platform {
				out = append(out, resolveFilterStmts(pf.Body, platform, false)...)
			}
			continue // non-matching filter: drop
		}
		if topLevel && hasFilter {
			continue // override: drop cross-platform default
		}
		out = append(out, resolveFilterChildren(s, platform))
	}
	return out
}

// resolveFilterChildren recurses into a statement's nested statement lists,
// resolving filters additively. Returns the statement (mutated in place).
func resolveFilterChildren(s ir.Stmt, platform string) ir.Stmt {
	switch n := s.(type) {
	case *ir.NodeInst:
		n.Children = resolveFilterStmts(n.Children, platform, false)
		for i := range n.Handlers {
			if n.Handlers[i].Func != nil {
				n.Handlers[i].Func.Block = resolveFilterStmts(n.Handlers[i].Func.Block, platform, false)
			}
		}
	case *ir.If:
		n.Body = resolveFilterStmts(n.Body, platform, false)
		n.Else = resolveFilterStmts(n.Else, platform, false)
	case *ir.For:
		n.Body = resolveFilterStmts(n.Body, platform, false)
		n.Else = resolveFilterStmts(n.Else, platform, false)
	case *ir.ErrorBoundary:
		n.Children = resolveFilterStmts(n.Children, platform, false)
	case *ir.SlotInst:
		n.Children = resolveFilterStmts(n.Children, platform, false)
	case *ir.ContextProvider:
		n.Children = resolveFilterStmts(n.Children, platform, false)
	case *ir.Window:
		n.Body = resolveFilterStmts(n.Body, platform, false)
	case *ir.Assign, *ir.LocalVar, *ir.Return, *ir.CallStmt, *ir.Emit, *ir.Toggle:
		// Leaf statements — no nested statement lists to recurse into.
	default:
		panic(fmt.Sprintf("resolveFilterChildren: unhandled stmt %T", s))
	}
	return s
}
