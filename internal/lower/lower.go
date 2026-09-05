package lower

import (
	"fmt"
	"slices"

	"git.duckfam.us/jonathan/sngl/internal/imports"
	"git.duckfam.us/jonathan/sngl/ir"
)

// pass is one lowering pass: a name (matching its Caps field), a predicate
// over Caps for whether it runs, and the apply function that mutates pkg in
// place. apply receives the full Caps so cross-cap-aware passes can branch
// (e.g. NoDeclarative checks NoLambda before lifting promoted handlers).
type pass struct {
	name    string
	enabled func(Caps) bool
	apply   func(*ir.Package, Caps, Options) error
}

// passes is the fixed execution order. Earlier passes may not depend on
// transformations performed by later ones; later passes may. Order rationale:
//
//  0. PropBindings — transforms NodeInst.Bindings into @event+handler pairs.
//     Runs after PlatformExtensionBody (so stdlib component bodies are resolved)
//     and before RefLoop/NoToggle so that prop mutations inside component bodies
//     are still in their original form when we rewrite them to Emit nodes.
//     0a. RefLoop — rewrites &-bound loop element refs to indexed list access
//     (list[idx]). Runs before NoReactivity so the resulting list[idx].field
//     writes are seen as mutations of the list var, and before NoToggle so a
//     toggled element-ref target is rewritten first.
//
//  1. NoUnit, NoEnum — collapse types, no deps.
//
//  3. NoAsyncReactive — must run before NoComputed (introduces sync state vars
//     that NoComputed would otherwise inline away) and before NoReactivity
//     (synthetic vars must be visible as reactive deps).
//
//  4. NoComputed — must run before NoReactivity (plain reads vs. computed indirections).
//
//  5. NoLambda — must run before NoReactivity (helpers may inject closures otherwise).
//
//  6. NoToggle — cheap stmt rewrite; before NoReactivity so the assignment is visible.
//     6a. Canvas — must run before NoReactivity. Shape NodeInsts (rect, circle, …)
//     are removed from the visual tree and replaced by synthesized draw funcs.
//     If reactivity ran first, it would assign __n* DOM ids to canvas shapes that
//     never appear in the DOM, generating broken setAttribute calls.
//     6a. InlinePure — always on; inlines pure user components and
//     platform-stdlib wrappers. Runs BEFORE reactivity, which flattens a
//     NodeInst past the point this pass can recognise the wrapper call.
//     6b. NoInlineComponents — opt-in. Inlines every non-recursive user
//     component into main, renaming vars/funcs/timers and substituting
//     prop refs with call-site arg exprs. After this pass, codegen on
//     opted-in targets sees only main + any recursive components.
//     6c. NoImplicitRecv — opt-in. Normalizes method calls to always carry
//     the receiver in Args[0], enabling codegen to drop conditional logic.
//
//     The list is the shape of the pipeline, not its census: passes that need no
//     explanation beyond their name are in `passes` below and not repeated here.
//
//  7. NoReactivity — analyzes dataflow, injects updaters.
//     NoTernary — MUST run AFTER NoReactivity. It rewrites `cond ? a : b`
//     into a `var __ltN` decl plus a sibling value-only `if cond { __ltN = a }
//     else { __ltN = b }`. If it ran first, reactivity's collectFromIf would
//     misclassify that synthesized `if` (when cond reads a reactive var) as a
//     reactive render slot and relocate it into a __renderSlotN func, orphaning
//     the `var __ltN` decl + its uses → compile error, and gatherDeps (which
//     has a correct `case *ir.Ternary`) couldn't see through the opaque temp to
//     track the reactive deps. Running after reactivity, ternaries stay intact
//     through dep analysis, then lower in place inside the bodies and updaters
//     reactivity produced (reactivity deep-copies each prop expression into its
//     updater so the build-path prop and its updater no longer alias the same
//     Ternary node, and each lowers independently). Placed before
//     NoCanvasReactivity because that pass
//     injects CanvasRedrawStmt nodes that NoTernary's stmt walker does not
//     handle; canvas draw funcs (built by NoCanvas, earlier) are still walked
//     by NoTernary via pkg/component/window Funcs, so their ternaries lower.
//
//  8. NoTimer — depends on reactivity decisions (timer handlers may have been wrapped).
//
//  9. NoDeclarative — flattens the visual tree, destroying shape earlier passes used;
//     its lifter (when NoLambda is also active) may emit fresh ref<T> shapes for
//     handlers promoted from inline blocks.
//
//  10. NoRef — runs last so it catches every ref<T> shape any earlier pass may have
//     emitted, including those produced by NoDeclarative's lifter. Idempotent: when
//     no ref<T> survives, all rewrites are no-ops.
var passes = []pass{
	passRootWindow,
	passHoistState,
	passForeignPrimitive,
	passPlatformExtensionBody,
	passPropBindings,
	passRefLoop,
	// Early, and well before passReactivity: the `if` it leaves behind has to
	// reach that pass as an ordinary view conditional so a reactive iterable
	// makes it a render slot, and the emptiness expression it synthesizes
	// names whatever the loop head names -- so every pass that rewrites a
	// name has to see it.
	passViewForElse,
	passUnit,
	passEnum,
	passQuery,
	passAsyncReactive,
	passComputed,
	passLambda,
	passNoListLambdas,
	passToggle,
	passContext,
	passInlinePure,
	passNoInlineComponents,
	// Right after inlining: that is what can splice a component's window into
	// another window's body, and every pass below reads the tree's shape.
	passWindowNesting,
	// After the inliner, which is what leaves a recursive component standing,
	// and before the passes that read a prop: the depth arrives as one.
	passRecursionDepth,
	passFlattenStructSpread,
	passNoImplicitRecv,
	passCanvas,
	// Before passReactivity: a mount handler that writes state has to reach it
	// as an ordinary assignment, or nothing patches what reads that state.
	passEffect,
	// Before passInstanceEvents, which is what turns the events it declares
	// into the props the render re-points.
	passSlotChildInstances,
	// Before passComponentProps, which is what turns the prop an event becomes
	// into the cell the render re-points.
	passInstanceEvents,
	// After the inliner, whose RuntimeInstance mark says which declarations
	// get cells at all, and after the two passes above, which are what add
	// the props a slot child and an instance event arrive as. Before
	// passReactivity, because a prop promoted to a var *is* a reactive cell
	// and the pass that injects updaters has to see it as one -- that is how a
	// setter comes to re-fire the slots reading the prop.
	passComponentProps,
	passReactivity,
	passTernary,
	passCanvasReactivity,
	passTimer,
	passFocusOrder,
	passInstanceBodies,
	passDeclarative,
	passNodeEscape,
	passNoRef,
	passIndexedIter,
	passForElse,
	passCSE,
	passIterKind,
	passStampUsage,
}

// Options controls a single Lower invocation.
type Options struct {
	// StopAfter, when non-empty, stops the pipeline after the named pass
	// runs (matching pass.name, e.g. "NoToggle"). Special value "none"
	// runs no passes — useful for the dump command's "show pre-lowering
	// state" mode. Empty string runs all enabled passes.
	StopAfter string

	// Platform is the active build target's platform identifier (e.g.
	// "html", "gtk4", "fyne"). passPlatformExtensionBody uses it to
	// swap each *ir.Component's matching PlatformOverrides entry into
	// Component.Body. Empty string disables the swap — appropriate for
	// platform-agnostic tools (LSP, format) that should leave abstract
	// stdlib components abstract.
	Platform string

	// Language is the active target's language identifier ("go", "js",
	// "kotlin"). A declaration may be overridden on either axis, and the
	// swap prefers the platform's where both apply.
	Language string

	// RootComponent names the declaration lowering should treat as the
	// program's entry point, overriding the by-name lookup for "main".
	//
	// It exists for a test build, where the entry point is the component under
	// test rather than the program's own root. Without it the inliner flattens
	// that component into whatever the program renders and renames its state
	// per instance, so the Model has `n__inst0` where the test agent -- written
	// against the declaration -- asks for `n` (#136).
	RootComponent string

	// ClaimsIntrinsic reports whether the target implements an #[intrinsic]
	// component id from another platform's namespace. A platform primitive is
	// the emitting codegen's dispatch key, so one platform's is meaningless to
	// another -- but a platform may choose to implement someone else's, and
	// this is how it says so. Nil means it claims none.
	ClaimsIntrinsic func(id string) bool

	// localComponents is the set the package itself declares, captured before
	// Lower widens pkg.Components to everything this build renders. Inlining
	// asks a different question from the rest of the pipeline -- whose
	// declaration is this? -- and that question is still about the package.
	// Set by Lower; a caller's value is overwritten.
	localComponents map[*ir.Component]bool
}

// Lower applies all enabled lowering passes to pkg in execution order,
// mutating pkg in place. caps determines which passes run; opts.StopAfter
// optionally short-circuits the pipeline after a named pass.
//
// Returns an error wrapping the failing pass's name when any pass fails or
// when opts.StopAfter names a pass that does not exist.
func Lower(pkg *ir.Package, caps Caps, opts Options) error {
	if opts.StopAfter == "none" {
		return nil
	}
	if opts.StopAfter != "" {
		known := false
		for _, p := range passes {
			if p.name == opts.StopAfter {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("lower: unknown StopAfter %q (valid: %v)", opts.StopAfter, PassNames())
		}
	}
	// Every component body a backend will emit has to be lowered, and an
	// imported one the inliner leaves standing is such a body: the backend
	// walks the declaration. Passes iterate pkg.Components, so the reachable
	// imported components join that list for the duration and are taken off
	// again afterwards -- what a package *declares* is not what this build
	// *renders*, and codegen asks the first question.
	declared := pkg.Components
	local := make(map[*ir.Component]bool, len(declared))
	for _, c := range declared {
		local[c] = true
	}
	//
	// One that survives inlining stays on the list afterwards, because a
	// backend emitting its body needs its funcs -- a canvas draw function
	// among them. One that is inlined away is taken back off below: what the
	// list means at the end is what this build renders.
	added := reachableForeignComponents(pkg, local)
	pkg.Components = append(slices.Clone(declared), added...)
	opts.localComponents = local
	addedSet := make(map[*ir.Component]bool, len(added))
	for _, c := range added {
		addedSet[c] = true
	}
	defer func() {
		if len(addedSet) == 0 {
			return
		}
		still := map[*ir.Component]bool{}
		for _, c := range reachableForeignComponents(pkg, local) {
			still[c] = true
		}
		pkg.Components = slices.DeleteFunc(pkg.Components, func(c *ir.Component) bool {
			return addedSet[c] && !still[c]
		})
	}()

	// The same for functions. A method on a struct an imported package
	// declares is called by name in the emitted code, so the definition has to
	// be emitted too -- `state.calc.digit(…)` on a plain object was what came
	// out instead, a call on a method JavaScript never got.
	pkg.Funcs = append(pkg.Funcs, reachableForeignFuncs(pkg)...)

	// And the types those values have. A language that declares its types --
	// Go, Kotlin -- has to be given the declaration of every struct and enum
	// the emitted code names, whichever package wrote it.
	addForeignTypes(pkg)

	for _, p := range passes {
		if !p.enabled(caps) {
			continue
		}
		if err := p.apply(pkg, caps, opts); err != nil {
			return fmt.Errorf("lower: pass %s: %w", p.name, err)
		}
		if opts.StopAfter != "" && p.name == opts.StopAfter {
			break
		}
	}
	if opts.StopAfter != "" {
		// A dump stopped mid-pipeline is deliberately half-lowered; the
		// invariant is about the IR a backend receives.
		return nil
	}
	return verifyMutationsAreStatements(pkg)
}

// addForeignTypes appends the structs and enums declared by the program's own
// packages -- the directories it imports -- so a language that declares its
// types has seen every one the emitted code names.
//
// A library's declarations stay out. `Style`, `int` and `color` belong to the
// target, which knows what they are; emitting SNGL's idea of them would put a
// second `Style` in the output beside the one the platform means.
func addForeignTypes(pkg *ir.Package) {
	haveStruct := make(map[*ir.StructDef]bool, len(pkg.Structs))
	for _, s := range pkg.Structs {
		haveStruct[s] = true
	}
	haveEnum := make(map[*ir.EnumDef]bool, len(pkg.Enums))
	for _, e := range pkg.Enums {
		haveEnum[e] = true
	}
	for _, p := range userPackages(pkg) {
		for _, s := range p.Structs {
			if s.Builtin == ir.BuiltinNone && !haveStruct[s] {
				haveStruct[s] = true
				pkg.Structs = append(pkg.Structs, s)
			}
		}
		for _, e := range p.Enums {
			if !haveEnum[e] {
				haveEnum[e] = true
				pkg.Enums = append(pkg.Enums, e)
			}
		}
	}
}

// userPackages is every package the program imports by directory, transitively:
// its own source, split across directories. A `sngl:` import is a library and
// is not one of these -- what it declares is the target's to provide.
func userPackages(pkg *ir.Package) []*ir.Package {
	var out []*ir.Package
	seen := map[*ir.Package]bool{pkg: true}
	var walk func(p *ir.Package)
	walk = func(p *ir.Package) {
		for _, imp := range p.Imports {
			if imp == nil || imp.Pkg == nil || seen[imp.Pkg] {
				continue
			}
			if scheme, _ := imports.ParseScheme(imp.Path); scheme != "" {
				continue
			}
			seen[imp.Pkg] = true
			out = append(out, imp.Pkg)
			walk(imp.Pkg)
		}
	}
	walk(pkg)
	return out
}

// reachableForeignFuncs collects, in first-seen order, every function this
// build calls that the package does not already list: a method on an imported
// struct, a helper it calls in turn. An intrinsic or a foreign declaration is
// the target's to provide and is left alone, as is a signature with no body.
func reachableForeignFuncs(pkg *ir.Package) []*ir.Func {
	have := make(map[*ir.Func]bool, len(pkg.Funcs))
	for _, f := range pkg.Funcs {
		have[f] = true
	}
	for _, c := range pkg.Components {
		for _, f := range c.Funcs {
			have[f] = true
		}
	}
	// Only the program's own packages. A library's function is the target's to
	// implement or the inliner's to substitute, never this package's to emit.
	mine := map[*ir.Func]bool{}
	for _, p := range userPackages(pkg) {
		for _, f := range p.Funcs {
			mine[f] = true
		}
		for _, c := range p.Components {
			for _, f := range c.Funcs {
				mine[f] = true
			}
		}
	}
	var out []*ir.Func
	var visit func(fn *ir.Func)
	walk := func(stmts []ir.Stmt) {
		newExprWalker(func(e ir.Expr) ir.Expr {
			if call, ok := e.(*ir.Call); ok {
				visit(call.Func)
			}
			return e
		}).stmts(stmts)
	}
	visit = func(fn *ir.Func) {
		if fn == nil || have[fn] || !mine[fn] || fn.Intrinsic != "" || fn.Foreign.Name != "" || len(fn.Block) == 0 {
			return
		}
		have[fn] = true
		out = append(out, fn)
		walk(fn.Block)
	}
	for _, c := range pkg.Components {
		walk(c.Body)
		for _, f := range c.Funcs {
			walk(f.Block)
		}
	}
	for _, f := range pkg.Funcs {
		walk(f.Block)
	}
	for _, w := range pkg.Windows {
		walk(w.Body)
		for _, f := range w.Funcs {
			walk(f.Block)
		}
	}
	return out
}

// reachableForeignComponents collects, in first-seen order, every component an
// imported package declares that this build actually renders: reached from a
// declared component's or window's body through a NodeInst, transitively.
func reachableForeignComponents(pkg *ir.Package, local map[*ir.Component]bool) []*ir.Component {
	seen := map[*ir.Component]bool{}
	var out []*ir.Component
	var walk func(stmts []ir.Stmt)
	visit := func(c *ir.Component) {
		if c == nil || local[c] || seen[c] {
			return
		}
		// A declaration with nothing in it is a primitive -- a stdlib widget,
		// a platform element -- and the backend that draws it needs no
		// lowered body. Only a component someone wrote is widened in.
		if len(c.Body) == 0 && len(c.Vars) == 0 && len(c.Funcs) == 0 && len(c.Timers) == 0 {
			return
		}
		seen[c] = true
		out = append(out, c)
		walk(c.Body)
		for _, f := range c.Funcs {
			walk(f.Block)
		}
	}
	walk = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.NodeInst:
				visit(n.Component)
				walk(n.Children)
				for _, h := range n.Handlers {
					if h.Func != nil {
						walk(h.Func.Block)
					}
				}
			case *ir.If:
				walk(n.Body)
				walk(n.Else)
			case *ir.For:
				walk(n.Body)
				walk(n.Else)
			case *ir.SlotInst:
				walk(n.Children)
			case *ir.ErrorBoundary:
				walk(n.Children)
			case *ir.ContextProvider:
				walk(n.Children)
			case *ir.Window:
				walk(n.Body)
			}
		}
	}
	for _, c := range pkg.Components {
		walk(c.Body)
		for _, f := range c.Funcs {
			walk(f.Block)
		}
	}
	for _, w := range pkg.Windows {
		walk(w.Body)
	}
	return out
}

// PassNames returns the ordered list of pass names. Used for help text and
// error messages.
func PassNames() []string {
	names := make([]string, len(passes))
	for i, p := range passes {
		names[i] = p.name
	}
	return names
}

// EnabledPasses returns the ordered list of pass names that would run for
// the given caps. Used by `dump lowered --list`.
func EnabledPasses(caps Caps) []string {
	var out []string
	for _, p := range passes {
		if p.enabled(caps) {
			out = append(out, p.name)
		}
	}
	return out
}
