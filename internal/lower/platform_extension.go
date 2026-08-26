package lower

import "git.duckfam.us/jonathan/sngl/ir"

// passPlatformExtensionBody specializes each *ir.Component whose
// PlatformBodies map has an entry for the active platform by swapping
// that entry into Component.Body, and its PlatformVars entry into
// Component.Vars. The checker is platform-agnostic and
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

	// PlatformBodies lives on stdlib *ir.Component pointers, which are in
	// scope but not in pkg.Components (that list holds only user-package
	// components). Walk the symbol table so stdlib extensions get
	// specialized too.
	if pkg.Symbols != nil {
		pkg.Symbols.EachSymbol(func(sym ir.Symbol) bool {
			comp, _ := sym.(*ir.Component)
			specializeComp(comp, platform)
			return true
		})
	}
	for _, comp := range pkg.Components {
		specializeComp(comp, platform)
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
	var walk func(stmts []ir.Stmt)
	walk = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.NodeInst:
				specializeComp(n.Component, platform)
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

// specializeComp swaps one component's entries for platform into the live
// Body and Vars slots. The vars travel with the body wherever the body does:
// the body reads them, and a var belonging to a platform that is not the
// build target must never reach codegen. Idempotent — re-swapping an
// already-specialized component writes the same values.
func specializeComp(comp *ir.Component, platform string) {
	if comp == nil || comp.PlatformBodies == nil {
		return
	}
	body, ok := comp.PlatformBodies[platform]
	if !ok {
		return
	}
	comp.Body = body
	if vars, ok := comp.PlatformVars[platform]; ok {
		comp.Vars = vars
	}
}
