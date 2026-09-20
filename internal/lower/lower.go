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
// transformations performed by later ones; later passes may.
//
// What this order has to satisfy is stated as data next door, in
// orderConstraints (order.go): each pair carries the reason it exists, and
// TestPassOrderConstraints checks the registry against it. The sequence below
// is not that reason and never was -- most adjacent pairs in it appear in no
// constraint, and swapping one changes nothing any target emits. Place a new
// pass wherever the stated requirements allow; if it has a requirement not yet
// stated, state it there rather than leaving it to position.
var passes = []pass{
	passHoistBodyTypes,
	passRootWindow,
	passHoistState,
	passForeignPrimitive,
	passPlatformExtensionBody,
	passPropBindings,
	passRefLoop,
	passViewForElse,
	passBoundaryFailed,
	passUnit,
	passEnum,
	passQuery,
	// Before passAsyncOffload, which synthesizes calls to the two ids this
	// reads, and before anything rewrites a body out from under a position.
	passAsyncCapable,
	passAsyncReactive,
	passComputed,
	passLambda,
	passNoListLambdas,
	passToggle,
	passContext,
	passInlinePure,
	passNoInlineComponents,
	passWindowNesting,
	passRecursionDepth,
	passFlattenStructSpread,
	passNoImplicitRecv,
	// After passCanvas: the call it promotes may be inside a draw function
	// synthesized from an override's handler body.
	passLibFuncs,
	passEffect,
	passSlotChildInstances,
	passInstanceEvents,
	passComponentProps,
	passReactivity,
	passTernary,
	passCanvasReactivity,
	passFocusOrder,
	passInstanceBodies,
	passDeclarative,
	passNodeEscape,
	passNoRef,
	// After passReactivity, and after everything that rewrites an imperative
	// body: what it splits off onto a goroutine is a finished body, updaters
	// and all. Before the three always-on passes below, which reach a lambda
	// body and so still see inside the two closures it leaves.
	passAsyncOffload,
	passIndexedIter,
	passForElse,
	// Last: it reads every body the passes above finished rewriting.
	passMutatedVars,
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

	// instSeq numbers the `__instN` suffix each substituted component's state
	// is renamed with. Shared, because two passes substitute into one emitted
	// namespace -- passInlinePure takes a platform override, passNoInlineComponents
	// takes a user component -- and a counter each had them both start at zero:
	// an override and a component in one owner were emitted as two
	// `hits__inst0`. A pointer so it survives Options being passed by value.
	// Set by Lower; a caller's value is overwritten.
	instSeq *int
}

// seqOrOwn is the shared `__instN` counter, or a private one for a caller that
// built a pass's state directly rather than through Lower -- a unit test.
func seqOrOwn(seq *int) *int {
	if seq == nil {
		return new(int)
	}
	return seq
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
	opts.instSeq = new(int)
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
	added := reachableForeignComponents(pkg, local, opts.Platform)
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
		for _, c := range reachableForeignComponents(pkg, local, opts.Platform) {
			still[c] = true
		}
		pkg.Components = slices.DeleteFunc(pkg.Components, func(c *ir.Component) bool {
			// A component the build renders as a live instance stays, whether
			// or not this second walk can still see the node that elected it:
			// by now that node is a CreateComponent call and not an
			// ir.NodeInst, so the walk finds nothing and the declaration the
			// factory is emitted from was being deleted out from under it.
			return addedSet[c] && !still[c] && !c.RuntimeInstance
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

	// A const or var that *is* a host identifier is taken off the lists a backend
	// emits from, the way `CodegenCtx.AllFuncs` drops a native func: it has no
	// value of its own to declare, and `math.Pi` came out as
	// `var pi float64 = Float64{}` -- a zero for a value that lives on the
	// host, spelled as a type name that is not a Go literal. Here rather than
	// per backend because five of them read pkg.Consts and each would need the
	// same test. Every reference already resolves through ir.Ident.Sym, which
	// holds the declaration and not its place in this list.
	pkg.Consts = slices.DeleteFunc(pkg.Consts, ir.IsHostValue)
	pkg.Vars = slices.DeleteFunc(pkg.Vars, ir.IsHostValue)

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

// statefulOverrideFor reports whether c's platform extension body for the
// platform being built declares state of its own.
//
// Asked while judging whether a component with no body yet is a primitive.
// This runs before passPlatformExtensionBody, so an overridden stdlib
// component still looks bodiless -- and one that could survive inlining needs
// its declaration on the list, because that is what gets a body lowered and
// what the factory emitter reads.
//
// State, not merely an override: a stateless override always inlines into its
// caller, so it never needs a declaration of its own, and widening every
// overridden component in lowers bodies nothing renders.
func statefulOverrideFor(c *ir.Component, platform string) bool {
	if c == nil || platform == "" || c.PlatformOverrides == nil {
		return false
	}
	ov, ok := c.PlatformOverrides[platform]
	return ok && len(ov.Vars) > 0
}

// reachableForeignComponents collects, in first-seen order, every component an
// imported package declares that this build actually renders: reached from a
// declared component's or window's body through a NodeInst, transitively.
func reachableForeignComponents(pkg *ir.Package, local map[*ir.Component]bool, platform string) []*ir.Component {
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
		//
		// "Nothing in it" has to count the override this build is about to
		// swap in: this runs before passPlatformExtensionBody, so a stdlib
		// component whose body comes from `sngl:platform/<name>` still looks
		// bodiless here. Judged so, it never joined the list, so a node of it
		// that survived inlining had no declaration for a backend to emit --
		// html called `__cf_timer(...)`, a factory nothing defined.
		if len(c.Body) == 0 && len(c.Vars) == 0 && len(c.Funcs) == 0 && !statefulOverrideFor(c, platform) {
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
