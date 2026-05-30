package lower

import "git.duckfam.us/jonathan/sngl/ir"

// passPlatformExtensionBody specializes each *ir.Component whose
// PlatformBodies map has an entry for the active platform by swapping
// that entry into Component.Body. The checker is platform-agnostic and
// collects every registered platform's `component sngl.X { platform <p>
// { ... } }` body into the map; the active platform is chosen here.
//
// Runs first so every subsequent pass (computed/reactivity/inline/etc.)
// sees the specialized body rather than an empty stdlib stub.
//
// When opts.Platform is empty (LSP, format, multi-platform discovery),
// the pass is a no-op — components keep whatever Body the checker left
// them with (empty for stdlib abstract components).
var passPlatformExtensionBody = pass{
	name:    "PlatformExtensionBody",
	enabled: func(c Caps) bool { return true },
	apply:   lowerPlatformExtensionBody,
}

func lowerPlatformExtensionBody(pkg *ir.Package, _ Caps, opts Options) error {
	if pkg == nil || opts.Platform == "" {
		return nil
	}
	specializePkgBodies(pkg, opts.Platform, map[*ir.Package]struct{}{})
	return nil
}

// specializePkgBodies swaps each component's PlatformBodies[platform] entry
// into its Body, for pkg and every package it imports (transitively).
// Imported packages carry their own stdlib *ir.Component instances — distinct
// pointers from the main package's — so a component referenced inside an
// imported component's body (e.g. `text` used by a cross-package `Row`) only
// gets specialized when we walk the imported package's symbol table too.
// The seen set guards against import cycles.
func specializePkgBodies(pkg *ir.Package, platform string, seen map[*ir.Package]struct{}) {
	if pkg == nil {
		return
	}
	if _, done := seen[pkg]; done {
		return
	}
	seen[pkg] = struct{}{}

	// PlatformBodies lives on stdlib *ir.Component pointers which are
	// shared with pkg.Symbols.Comps but not stored in pkg.Components
	// (that list holds only user-package components). Walk the symbol
	// table so stdlib extensions get specialized too.
	if pkg.Symbols != nil {
		for _, sym := range pkg.Symbols.Comps {
			comp, ok := sym.(*ir.Component)
			if !ok || comp == nil || comp.PlatformBodies == nil {
				continue
			}
			if body, ok := comp.PlatformBodies[platform]; ok {
				comp.Body = body
			}
		}
	}
	for _, comp := range pkg.Components {
		if comp == nil || comp.PlatformBodies == nil {
			continue
		}
		if body, ok := comp.PlatformBodies[platform]; ok {
			comp.Body = body
		}
	}
	for _, imp := range pkg.Imports {
		if imp != nil {
			specializePkgBodies(imp.Pkg, platform, seen)
		}
	}

	// Cross-package inlining (run in the optimize pass that precedes lower)
	// copies imported component bodies — including NodeInsts whose .Component
	// points at the *imported* package's stdlib instance — directly into this
	// package's visual tree. Those instances are no longer reachable via any
	// symbol table or import edge above, so specialize each Component actually
	// referenced in the tree. Idempotent: re-swapping an already-specialized
	// Body is a no-op.
	specializeComp := func(comp *ir.Component) {
		if comp == nil || comp.PlatformBodies == nil {
			return
		}
		if body, ok := comp.PlatformBodies[platform]; ok {
			comp.Body = body
		}
	}
	var walk func(stmts []ir.Stmt)
	walk = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.NodeInst:
				specializeComp(n.Component)
				walk(n.Children)
			case *ir.For:
				walk(n.Body)
			case *ir.If:
				walk(n.Body)
				walk(n.Else)
			}
		}
	}
	for _, comp := range pkg.Components {
		if comp != nil {
			walk(comp.Body)
		}
	}
	for _, win := range pkg.Windows {
		if win != nil {
			walk(win.Body)
		}
	}
}
