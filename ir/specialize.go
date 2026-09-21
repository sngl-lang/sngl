package ir

import "strings"

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
func SpecializeForTarget(pkg *Package, platform, language string) {
	if pkg == nil || (platform == "" && language == "") {
		return
	}
	specializePkgBodies(pkg, target{platform, language}, map[*Package]struct{}{}, false)
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
func SpecializeOverriddenBodies(pkg *Package, platform, language string) {
	if pkg == nil || (platform == "" && language == "") {
		return
	}
	specializePkgBodies(pkg, target{platform, language}, map[*Package]struct{}{}, true)
}

// target is the build's two axes. A declaration may be overridden on either,
// and the platform's override wins where both apply -- the platform has the
// last word on the rest of the build too.
type target struct{ platform, language string }

// key names the target for the once-per-target guards below. Both halves,
// because a declaration may be overridden on either axis.
func (t target) key() string { return t.platform + "/" + t.language }

// covers reports whether a recorded specialization already answers for t.
//
// An absent half matches anything. The pipeline's callers disagree about
// whether to name the language -- the optimizer is given both axes and lowering
// is routinely given only the platform -- and read strictly, those are two
// different targets, so the guard never fires and the second swap resets
// everything the first pass built on top of the body.
//
// Two *named* languages under one platform are still different targets, because
// a function may be overridden on the language axis alone.
func covers(recorded string, t target) bool {
	if recorded == "" {
		return false
	}
	rp, rl, ok := strings.Cut(recorded, "/")
	if !ok {
		return false
	}
	if rp != "" && t.platform != "" && rp != t.platform {
		return false
	}
	if rl != "" && t.language != "" && rl != t.language {
		return false
	}
	return true
}

// pick returns the body a target selects from the two override maps, and
// whether there is one at all.
func pick[T any](t target, byPlatform, byLanguage map[string]T) (T, bool) {
	if t.platform != "" && byPlatform != nil {
		if v, ok := byPlatform[t.platform]; ok {
			return v, true
		}
	}
	if t.language != "" && byLanguage != nil {
		if v, ok := byLanguage[t.language]; ok {
			return v, true
		}
	}
	var zero T
	return zero, false
}

// specializePkgBodies swaps each component's PlatformOverrides[platform] entry
// into its Body, for pkg and every package it imports (transitively).
// Imported packages carry their own stdlib *Component instances — distinct
// pointers from the main package's — so a component referenced inside an
// imported component's body (e.g. `text` used by a cross-package `Row`) only
// gets specialized when we walk the imported package's symbol table too.
// The seen set guards against import cycles.
func specializePkgBodies(pkg *Package, t target, seen map[*Package]struct{}, bodiedOnly bool) {
	if pkg == nil {
		return
	}
	if _, done := seen[pkg]; done {
		return
	}
	seen[pkg] = struct{}{}

	// PlatformOverrides lives on stdlib *Component pointers, which are in
	// scope but not in pkg.Components (that list holds only user-package
	// components). Walk the symbol table so stdlib extensions get
	// specialized too.
	if pkg.Symbols != nil {
		pkg.Symbols.EachSymbol(func(sym Symbol) bool {
			switch decl := sym.(type) {
			case *Component:
				specializeComp(decl, t, bodiedOnly)
			case *Func:
				specializeFunc(decl, t, bodiedOnly)
			}
			return true
		})
	}
	for _, comp := range pkg.Components {
		specializeComp(comp, t, bodiedOnly)
	}
	for _, fn := range pkg.Funcs {
		specializeFunc(fn, t, bodiedOnly)
	}
	for _, imp := range pkg.Imports {
		if imp != nil {
			specializePkgBodies(imp.Pkg, t, seen, bodiedOnly)
		}
	}

	// Cross-package inlining (run in the optimize pass that precedes lower)
	// copies imported component bodies — including NodeInsts whose .Component
	// points at the *imported* package's stdlib instance — directly into this
	// package's visual tree. Those instances are no longer reachable via any
	// symbol table or import edge above, so specialize each Component actually
	// referenced in the tree. A component already specialized for this target
	// is skipped rather than re-swapped -- see specializeComp.
	var walk func(stmts []Stmt)
	walk = func(stmts []Stmt) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *NodeInst:
				specializeComp(n.Component, t, bodiedOnly)
				walk(n.Children)
			case *For:
				walk(n.Body)
				walk(n.Else)
			case *If:
				walk(n.Body)
				walk(n.Else)
			case *Window:
				// A `window #w { … }` written inside a component is a
				// statement here, not an entry in pkg.Windows. Stopping at it
				// left every widget under it holding the abstract stdlib body,
				// so the platform saw nothing it could build.
				walk(n.Body)
			case *SlotInst:
				walk(n.Children)
			case *ErrorBoundary:
				walk(n.Children)
				walk(n.Failed)
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
	walk(pkg.Body)
}

// specializeFunc swaps one function's body for platform into its live Block.
// Once per target, for the reason specializeComp gives: the assignment is a
// reset, and by the second call the block may be the lowered one.
func specializeFunc(fn *Func, t target, bodiedOnly bool) {
	if fn == nil {
		return
	}
	if bodiedOnly && len(fn.Block) == 0 {
		return
	}
	if covers(fn.SpecializedFor, t) {
		return
	}
	if body, ok := pick(t, fn.PlatformOverrides, fn.LanguageOverrides); ok {
		fn.SpecializedFor = t.key()
		fn.Block = body.Stmts
	}
}

// specializeComp swaps one component's entries for platform into the live
// Body and Vars slots. The vars travel with the body wherever the body does:
// the body reads them, and a var belonging to a platform that is not the
// build target must never reach codegen.
//
// Once per target, and the comment here used to say "idempotent -- re-swapping
// writes the same values", which was true of the values it writes and false of
// everything else in those slots.
func specializeComp(comp *Component, t target, bodiedOnly bool) {
	if comp == nil {
		return
	}
	// A component's own functions are overridable too, and are reachable only
	// through the component that holds them.
	for _, fn := range comp.Funcs {
		specializeFunc(fn, t, bodiedOnly)
	}
	if bodiedOnly && len(comp.Body) == 0 {
		return
	}
	body, ok := pick(t, comp.PlatformOverrides, comp.LanguageOverrides)
	if !ok {
		return
	}
	// Once, per target. The swap is a reset -- it assigns over whatever Body
	// and Vars currently hold -- so a second one for the same target discards
	// every declaration lowering added: a build runs the optimizer again after
	// lowering, and an override that survives as a runtime instance lost the
	// promoted prop cell and the whole effect settle chain that way. The
	// factory came out reading names nothing declared.
	//
	// Recorded as the target rather than a flag, because one process compiles
	// a program for several of them and a stdlib component is shared between
	// those builds: a flag would let the first target's body stand for the
	// second's.
	if covers(comp.SpecializedFor, t) {
		return
	}
	comp.SpecializedFor = t.key()
	// The vars travel with the statements: the body reads them, and a var
	// belonging to a target that is not this one must never reach codegen.
	// So do the body-scoped declarations, which is what links a component
	// nested in this override body back to the body that declared it.
	comp.Body, comp.Vars, comp.BodyDecls = body.Stmts, body.Vars, body.BodyDecls
	// And the methods, whose list this target's body is the only one that can
	// call: a helper written in another target's override is not this one's to
	// emit. Guarded because a func override records no method set of its own.
	if body.Funcs != nil || body.Methods != nil {
		comp.Funcs, comp.Methods = body.Funcs, body.Methods
	}
}
