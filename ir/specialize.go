package ir

// SpecializeForTarget swaps every declaration's body for platform into the
// slot the rest of the compiler reads -- a component's Body and Vars, a
// function's Block. The checker is platform-agnostic and collects every
// registered target's override; the active one is chosen here.
//
// It runs at the top of both the optimizer and lowering. The optimizer folds
// and inlines against the body a call actually runs, which is the override
// where there is one: specializing only in lowering left the optimizer folding
// the body the override replaces. Idempotent, so running it twice is running
// it once.
//
// An empty platform (LSP, format, multi-platform discovery) specializes
// nothing: declarations keep whatever body the checker left them with.
func SpecializeForTarget(pkg *Package, platform string) {
	if pkg == nil || platform == "" {
		return
	}
	specializePkgBodies(pkg, platform, map[*Package]struct{}{}, false)
}

// SpecializeOverriddenBodies is SpecializeForTarget restricted to declarations
// that have a body of their own -- the ones where *not* specializing yet would
// leave the optimizer folding and inlining a body this target replaced.
//
// A library component has no body until lowering swaps one in, and several
// passes are written against that: they see an empty stdlib stub during
// optimization and the platform's implementation after it. Specializing those
// early changes what the optimizer sees for every stdlib component in every
// build, which is a different change than this one. So the early pass moves
// only what a program overrode.
func SpecializeOverriddenBodies(pkg *Package, platform string) {
	if pkg == nil || platform == "" {
		return
	}
	specializePkgBodies(pkg, platform, map[*Package]struct{}{}, true)
}

// specializePkgBodies swaps each component's PlatformBodies[platform] entry
// into its Body, for pkg and every package it imports (transitively).
// Imported packages carry their own stdlib *Component instances — distinct
// pointers from the main package's — so a component referenced inside an
// imported component's body (e.g. `text` used by a cross-package `Row`) only
// gets specialized when we walk the imported package's symbol table too.
// The seen set guards against import cycles.
func specializePkgBodies(pkg *Package, platform string, seen map[*Package]struct{}, bodiedOnly bool) {
	if pkg == nil {
		return
	}
	if _, done := seen[pkg]; done {
		return
	}
	seen[pkg] = struct{}{}

	// PlatformBodies lives on stdlib *Component pointers, which are in
	// scope but not in pkg.Components (that list holds only user-package
	// components). Walk the symbol table so stdlib extensions get
	// specialized too.
	if pkg.Symbols != nil {
		pkg.Symbols.EachSymbol(func(sym Symbol) bool {
			switch decl := sym.(type) {
			case *Component:
				specializeComp(decl, platform, bodiedOnly)
			case *Func:
				specializeFunc(decl, platform, bodiedOnly)
			}
			return true
		})
	}
	for _, comp := range pkg.Components {
		specializeComp(comp, platform, bodiedOnly)
	}
	for _, fn := range pkg.Funcs {
		specializeFunc(fn, platform, bodiedOnly)
	}
	for _, imp := range pkg.Imports {
		if imp != nil {
			specializePkgBodies(imp.Pkg, platform, seen, bodiedOnly)
		}
	}

	// Cross-package inlining (run in the optimize pass that precedes lower)
	// copies imported component bodies — including NodeInsts whose .Component
	// points at the *imported* package's stdlib instance — directly into this
	// package's visual tree. Those instances are no longer reachable via any
	// symbol table or import edge above, so specialize each Component actually
	// referenced in the tree. Idempotent: re-swapping an already-specialized
	// Body is a no-op.
	var walk func(stmts []Stmt)
	walk = func(stmts []Stmt) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *NodeInst:
				specializeComp(n.Component, platform, bodiedOnly)
				walk(n.Children)
			case *For:
				walk(n.Body)
			case *If:
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

// specializeFunc swaps one function's body for platform into its live Block.
// Idempotent, like specializeComp: re-swapping writes the same value.
func specializeFunc(fn *Func, platform string, bodiedOnly bool) {
	if fn == nil || fn.PlatformBodies == nil {
		return
	}
	if bodiedOnly && len(fn.Block) == 0 {
		return
	}
	if body, ok := fn.PlatformBodies[platform]; ok {
		fn.Block = body
	}
}

// specializeComp swaps one component's entries for platform into the live
// Body and Vars slots. The vars travel with the body wherever the body does:
// the body reads them, and a var belonging to a platform that is not the
// build target must never reach codegen. Idempotent — re-swapping an
// already-specialized component writes the same values.
func specializeComp(comp *Component, platform string, bodiedOnly bool) {
	if comp == nil {
		return
	}
	// A component's own functions are overridable too, and are reachable only
	// through the component that holds them.
	for _, fn := range comp.Funcs {
		specializeFunc(fn, platform, bodiedOnly)
	}
	if comp.PlatformBodies == nil {
		return
	}
	if bodiedOnly && len(comp.Body) == 0 {
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
