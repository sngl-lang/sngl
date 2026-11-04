package ir

import (
	"slices"
	"strings"
)

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
	InstallOverlay(pkg)
	t := target{platform, language}
	if !specializeOverridden(pkg, t, false, nil) {
		specializePkgBodies(pkg, t, map[*Package]struct{}{}, false)
	}
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
//
// Given a reach, it moves only what that holds; reach nil holds everything.
func SpecializeOverriddenBodies(pkg *Package, platform, language string, reach *Reached) {
	if pkg == nil || (platform == "" && language == "") {
		return
	}
	InstallOverlay(pkg)
	t := target{platform, language}
	if !specializeOverridden(pkg, t, true, reach) {
		specializePkgBodies(pkg, t, map[*Package]struct{}{}, true)
	}
}

// specializeOverridden swaps the declarations a build's copy recorded as
// overridden for t (Package.Overridden), those reach holds, and reports
// whether pkg is such a copy.
func specializeOverridden(pkg *Package, t target, bodiedOnly bool, reach *Reached) bool {
	if !covers(pkg.OverriddenFor, t) {
		return false
	}
	for _, sym := range pkg.Overridden {
		if !reach.Has(sym) {
			continue
		}
		switch decl := sym.(type) {
		case *Component:
			specializeComp(decl, t, bodiedOnly)
		case *Func:
			specializeFunc(decl, t, bodiedOnly)
		}
	}
	return true
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

// ComponentOverride is the override body comp carries for the target, by the
// rule SpecializeForTarget swaps one in with: the platform's entry, else the
// language's. A reader that needs a target's body before or without the swap
// asks here rather than ranging over the maps, which hold every target the
// build names -- and, in a per-target clone (ClonePackageFor), only this one.
func ComponentOverride(comp *Component, platform, language string) (Override, bool) {
	if comp == nil {
		return Override{}, false
	}
	return pick(target{platform, language}, comp.PlatformOverrides, comp.LanguageOverrides)
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
			case *For, *If, *Match:
				for _, b := range TransparentBlocks(n) {
					walk(*b)
				}
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
			walk(comp.Stmts)
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
		fn.mustWrite("specializing " + fn.Name)
		fn.SpecializedFor = t.key()
		fn.Block = body.Stmts
		if body.RecvParam != nil {
			fn.RecvParam = body.RecvParam
		}
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
	if bodiedOnly && len(comp.Stmts) == 0 {
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
	comp.mustWrite("specializing " + comp.Name)
	if comp.SpecializedFor == "" && comp.DeclaredBody == nil {
		comp.DeclaredBody = comp.Stmts
	}
	comp.SpecializedFor = t.key()
	// The body is swapped whole: the vars, funcs and body-scoped
	// declarations travel with the statements that read them, and a var
	// belonging to a target that is not this one must never reach codegen.
	// An override that recorded no method set of its own keeps the
	// declaration's.
	funcs, methods := comp.Funcs, comp.Methods
	comp.Body = body.Body
	if body.Funcs == nil && body.Methods == nil {
		comp.Funcs, comp.Methods = funcs, methods
	}
	// Const is the declaration's or the override's. Read off the declaration's
	// AST rather than the field, which a previous target's swap has written.
	comp.Const = body.Const || (comp.AST != nil && comp.AST.Const)
}

// FuncsFor is what comp's funcs are on the target: its override's for the
// target, where SpecializeForTarget has yet to swap that in, and its own
// otherwise. A pass that runs ahead of the swap -- one reading what a test
// calls, which the library body for the target declares -- asks this.
func (comp *Component) FuncsFor(platform, language string) []*Func {
	t := target{platform, language}
	if comp == nil || covers(comp.SpecializedFor, t) {
		return comp.Funcs
	}
	if body, ok := pick(t, comp.PlatformOverrides, comp.LanguageOverrides); ok && (body.Funcs != nil || body.Methods != nil) {
		return body.Funcs
	}
	return comp.Funcs
}

// DropUncalledTriggers removes from each component pkg reaches the
// overrides of its events (Func.Trigger) no call in pkg makes. A test's
// trigger is a call of one by now, and one no test calls is not the
// program's: left among the component's funcs it is a method of every
// instance, and a method keeps a const override from splicing, so an app
// build rendered differently for having a test half. Only a component
// SpecializeForTarget swapped a body into is written: that is where a
// target's trigger is, and the build's own copy.
func DropUncalledTriggers(pkg *Package) {
	if pkg == nil {
		return
	}
	called := map[*Func]bool{}
	_ = Walk(pkg, func(n Node) error {
		if c, ok := n.(*Call); ok && c.Func != nil && c.Func.Trigger != "" {
			called[c.Func] = true
		}
		return nil
	})
	uncalled := func(fn *Func) bool { return fn.Trigger != "" && !called[fn] }
	drop := func(comp *Component) {
		if comp == nil || comp.SpecializedFor == "" || !slices.ContainsFunc(comp.Funcs, uncalled) {
			return
		}
		var dropped []*Func
		for _, fn := range comp.Funcs {
			if uncalled(fn) {
				dropped = append(dropped, fn)
			}
		}
		comp.Funcs = slices.DeleteFunc(slices.Clone(comp.Funcs), uncalled)
		// A node the override named for its triggers alone -- fyne's
		// `Select #sel` -- is named for nothing once they are gone, and an
		// id costs a build a field of its own.
		reads := func(roots ...any) map[*Var]bool {
			out := map[*Var]bool{}
			for _, r := range roots {
				_ = Walk(r, func(n Node) error {
					if id, ok := n.(*Ident); ok {
						if v, ok := id.Sym.(*Var); ok && v.NodeHandle {
							out[v] = true
						}
					}
					return nil
				})
			}
			return out
		}
		var droppedRoots, keptRoots []any
		for _, fn := range dropped {
			droppedRoots = append(droppedRoots, fn)
		}
		keptRoots = append(keptRoots, comp.Stmts)
		for _, fn := range comp.Funcs {
			keptRoots = append(keptRoots, fn)
		}
		for _, v := range comp.Vars {
			if v.Init != nil {
				keptRoots = append(keptRoots, v.Init)
			}
		}
		byTriggers := reads(droppedRoots...)
		kept := reads(keptRoots...)
		_ = Walk(comp.Stmts, func(n Node) error {
			if ni, ok := n.(*NodeInst); ok && ni.Handle != nil && byTriggers[ni.Handle] && !kept[ni.Handle] {
				ni.ID, ni.Handle = "", nil
			}
			return nil
		})
	}
	seen := map[*Package]bool{}
	var visit func(p *Package)
	visit = func(p *Package) {
		if p == nil || seen[p] {
			return
		}
		seen[p] = true
		for _, sym := range p.Overridden {
			if comp, ok := sym.(*Component); ok {
				drop(comp)
			}
		}
		if p.Symbols != nil {
			p.Symbols.EachSymbol(func(sym Symbol) bool {
				if comp, ok := sym.(*Component); ok {
					drop(comp)
				}
				return true
			})
		}
		for _, comp := range p.Components {
			drop(comp)
		}
		for _, imp := range p.Imports {
			if imp != nil {
				visit(imp.Pkg)
			}
		}
	}
	visit(pkg)
	// A component only a node in the tree names, as specializePkgBodies
	// finds one.
	_ = Walk(pkg, func(n Node) error {
		if ni, ok := n.(*NodeInst); ok {
			drop(ni.Component)
		}
		return nil
	})
}
