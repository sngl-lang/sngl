// Package lower performs capability-driven IR-to-IR transformations between
// the optimizer and codegen. Each lowering pass is gated by a Features field:
// a target that claims a construct keeps it, and a target that says nothing
// gets the pass that rewrites it into something simpler.
package lower

import (
	"strings"
)

// Features is what a target says it can do, declared in its own package by
// `sngl:x/gen`'s marks and read by codegen.CapsFor.
//
// **A capability not written is not held.** The zero value claims nothing, so
// the pass that rewrites each withheld construct runs for it. That is what lets
// the language grow: a new construct arrives with a pass that converts it away,
// and every target that has not heard of it keeps working unedited, opting in
// when its own code generator can do better than the pass. Under the other
// polarity, silence would mean "I emit this" on every declaration written
// before the construct existed, and adding one would break every target at
// once.
//
// Not *every* gated pass, and the exceptions are the fields that read the other
// way round. The five `#[gen.wants]` requests are asked for by being set, so
// silence correctly leaves them off; and InsertBefore is a grant rather than a
// withdrawal, so passSlotChildInstances -- which wants it beside a withheld
// Reactivity -- is off for the zero value too. order_test.go's soleGate records
// all six as gating nothing on their own.
//
// There used to be a second record, Caps, saying the same thing inverted --
// NoTernary against Ternary -- with ToLowerCaps, Merge and String each naming
// every field. One record is one list.
type Features struct {
	Toggle        bool // can emit x!! natively
	Ternary       bool // can emit a ? b : c natively
	Lambda        bool // can emit closures natively
	Ref           bool // can emit ref[T] natively
	Unit          bool // can emit unit types natively
	Enum          bool // can emit enum types natively
	AsyncReactive bool // can handle async in reactive contexts natively
	// AsyncCalls says a call to a function that does not complete now can be
	// emitted where it is written. JavaScript awaits it and Kotlin suspends;
	// Go has neither, so on Go the call runs on the goroutine that made it --
	// which on a UI target is the goroutine drawing the screen.
	//
	// Withheld, passAsyncOffload moves the call to a goroutine and posts the
	// answer back through the target's own scheduler. A platform for which no
	// goroutine is the wrong one -- an html route handler already runs on its
	// own -- claims it back, the way html reclaims Ternary.
	AsyncCalls       bool
	Computed         bool // can handle computed vars natively
	ListLambdas      bool // can emit xs.filter(f) / xs.map(f) natively
	Reactivity       bool // can handle reactive deps natively (else explicit updater stmts)
	Declarative      bool // can handle a declarative visual tree (else flat create/update/delete calls)
	InlineComponents bool // can handle inline component references (else inlined into main)
	ImplicitRecv     bool // can handle an implicit receiver (else explicit Args[0])
	StructSpread     bool // can emit struct-literal spreads (else flattened)

	// ViewStatements declares that a view body reaches the output as
	// host-language statements the target runs. Withheld says it is written
	// out as markup -- html, in both its modes -- and then a `for` over a
	// compile-time-constant iterable has nowhere to run: nothing iterates it,
	// and it renders its body once with its variable bound to nothing.
	// Withholding it is what makes the build unroll such loops after lowering
	// (html's documentsOf), and what shake reads to keep a const only they walk
	// as a build value.
	//
	// Unlike every other capability here it gates no pass: what a target
	// declares about itself is a wider question than which passes run for it.
	ViewStatements bool

	// InstanceState says the platform keeps a component instance's own state --
	// in a record, a closure, a composable's `remember` -- so a component with
	// state of its own that is built at run time holds it per instance.
	// Withheld, passNoInlineComponents splices such a component where it is
	// written, and under a `for` gives each of its vars one cell per copy, keyed
	// by the copy's position. bubbletea withholds it: an Elm model keeps every
	// cell in itself, and a runtime component there is a render function.
	InstanceState bool

	// InsertBefore says the platform's container can put a child at a
	// position, not only at the end -- so a keyed reconciliation may move one
	// child instead of rebuilding the run. A toolkit whose container appends
	// is not wrong for lacking it, and the rebuild it keeps is correct, just
	// less direct. A platform claiming this must implement
	// codegen.ChildInserter.
	InsertBefore bool
	// InlineSlots says a reactive `if` or `for` may render into a span.
	// Withheld, the flow holding one is the render slot instead.
	InlineSlots bool
	// AsyncPost says the platform can run a closure back on the thread it
	// draws on, and answers lower.AsyncDoIntrinsic with the call that does
	// it. Without it there is nowhere for a blocking call's answer to land,
	// and passAsyncOffload refuses the program rather than writing a state
	// update onto a goroutine that does not own the widgets.
	//
	// A platform claiming it must register an emitter for that id; codegen's
	// tests check the two against each other.
	AsyncPost bool
	// AsyncSpawn says the language can run a closure without waiting for it,
	// and answers lower.AsyncStartIntrinsic with the call that does -- Go's
	// `go func(){}()`. It sits on the language axis where AsyncPost sits on
	// the platform's, because starting work is a property of the host language
	// and getting back to the drawing thread is a property of the surface.
	AsyncSpawn bool
	// Effects says the platform brackets an `effect`'s lifetime itself and
	// wants the node left standing. A framework whose own model already
	// brackets a lifetime keyed on a value -- Compose's DisposableEffect is one
	// -- expresses @mount and @unmount better than the calls passEffect lowers
	// them to, and gets the node with those two handlers and its key. It knows
	// nothing of writes, so passEffect still lowers `@update` and a list's
	// change events there.
	Effects bool
	// Navigation says the platform answers sngl:ui/nav's stack, page and link
	// in its own codegen -- a document or a route per page, a Compose NavHost
	// -- so passNavigation leaves them standing. Withheld, they are lowered to
	// a var holding the current page, a history list and an if-chain over the
	// pages, which every target can render.
	Navigation bool
	// GenericFuncs says a function with type parameters can be emitted once
	// and called at every type. Withheld, passMonomorphize gives each
	// instantiation a copy of its own with the types written in: Go has no
	// generic method to put a state-reading one on the Model, and no
	// constraint to state what a body does with T.
	GenericFuncs bool
	// GenericComponents says a component with type parameters can be emitted
	// once and built at every type, which is how a component built at run
	// time reaches its host. Withheld, passNoInlineComponents copies such a
	// component per instantiation where it elects it (instantiateRuntime), as
	// passMonomorphize copies a function: one declaration with T erased holds
	// a `list[T]` as `[]any`, which `[]int` is not, and cannot spell
	// `struct { v T }`. A platform holds it apart from its language: html
	// builds an instance in the page's JavaScript under any --lang.
	GenericComponents bool
	// Match says the language emits a match statement as its own switch --
	// Go's `switch`, Kotlin's `when`, JavaScript's `switch` -- in a function,
	// a handler or a lambda. Withheld, passMatch writes it as the if chain it
	// means.
	Match bool
	// MatchValue is Match for a match expression: Kotlin's `when` is one, and
	// Go's switch assigning a temp (passTernary) stands for one. JavaScript
	// has no switch expression, so it withholds this, and passMatch writes the
	// ternaries.
	MatchValue bool
	// ViewMatch says the platform renders a match in a view body as one, and
	// a match expression in a view or an initializer, which is what a
	// platform whose view is host statements can do: Compose's `when` in a
	// composable. Withheld, passMatch writes those as the chain.
	ViewMatch bool
	// OptionMatch says the language's switch compares an option subject as
	// `==` compares one, so a match over an option of a bool, a number, a
	// string or an enum is held where Match, MatchValue or ViewMatch holds
	// it: Kotlin's `when` takes `null ->` beside the element's values. Go's
	// switch would compare the option's pointer, so Go withholds it, and so
	// does JavaScript; passMatch writes such a match as the chain.
	OptionMatch bool

	// The six below are requests rather than capabilities, which is the split
	// `sngl:x/gen` spells as `#[gen.wants]`: each asks for a pass the target
	// wants run, so a platform asking for Canvas is not confessing to
	// anything. They read the other way round from every field above -- true
	// runs the pass.

	// StructComponents requests that components compile to structs with
	// methods rather than functions or closures.
	StructComponents bool
	// StdlibContextParam requests a hidden trailing parameter threaded through
	// every stdlib func reachable from user code that reads a context.
	StdlibContextParam bool
	// FocusOrder requests focus-tracking lowering: the pass walks the visual
	// tree, assigns integer IDs to focusable nodes, and injects __focusID,
	// __focusNext and __focusPrev into the component. Platforms that render
	// their own widgets (canvas, TUI) ask for it; native-widget platforms that
	// delegate focus to the OS do not.
	FocusOrder bool
	// Canvas requests canvas-drawing lowering: passShapeDraw walks
	// shape-children bodies and turns them into draw functions of intrinsic
	// calls.
	Canvas bool
	// ReactiveCanvas requests passCanvasReactivity: it injects CanvasRedrawStmt
	// into handler and timer bodies that mutate vars a canvas draw func reads.
	ReactiveCanvas bool
	// NavigationHrefs requests passNavigationHrefs, for a target that answers
	// sngl:ui/nav with an address per page: a link becomes a ui.link to the
	// page's href, and a go passing no params passes the page's own.
	NavigationHrefs bool

	// Reserved is every name the build's two build-tree nodes reserve by
	// default -- the language's reservedWords and the platform's
	// reservedNames props together -- for a build with no output block to
	// read them from: passReservedNames renames a
	// declaration the program gave one of them. Not a capability, so it is
	// the one field String does not list and no pass is gated on.
	Reserved []string
}

// LowersNothing reports whether f holds exactly NoLowering's capabilities,
// whatever names it reserves. String lists every capability held, so two
// records listing the same are the same set of passes.
func (f Features) LowersNothing() bool {
	return f.String() == NoLowering().String()
}

// NoLowering is the Features under which no capability-gated pass runs: every
// construct claimed, nothing granted, nothing requested.
//
// It is a test's answer and not a target's. A real target says what it can do,
// and the one place a build wants this shape is the interpreted path below.
func NoLowering() Features {
	return Features{
		Toggle:            true,
		Ternary:           true,
		Lambda:            true,
		Ref:               true,
		Unit:              true,
		Enum:              true,
		AsyncReactive:     true,
		AsyncCalls:        true,
		Computed:          true,
		ListLambdas:       true,
		Reactivity:        true,
		Declarative:       true,
		InlineComponents:  true,
		ImplicitRecv:      true,
		StructSpread:      true,
		ViewStatements:    true,
		InstanceState:     true,
		Effects:           true,
		Navigation:        true,
		GenericFuncs:      true,
		GenericComponents: true,
		Match:             true,
		MatchValue:        true,
		ViewMatch:         true,
		OptionMatch:       true,
	}
}

// InterpreterFeatures is what a target with no host language holds: every
// gated pass is compensation for something a backend cannot emit, and there is
// no backend -- in particular Declarative, whose absence dissolves the visual
// tree the interpreter mounts.
//
// Effects is the exception, and it is NoLowering's one difference: nothing in
// internal/interp answers an `ir.Effect` node, so passEffect has to turn one
// into the calls that run its bracket before the interpreter sees it.
func InterpreterFeatures() Features {
	f := NoLowering()
	f.Effects = false
	return f
}

// String lists the capabilities held, comma-separated, in the order the fields
// are declared. Empty when the target claims none, which is what a plugin that
// answered nothing looks like.
//
// It says what the target can do, not which passes will run for it --
// EnabledPasses answers the second, and the two are no longer the same list
// read two ways.
func (f Features) String() string {
	var parts []string
	for _, c := range []struct {
		name string
		held bool
	}{
		{"Toggle", f.Toggle},
		{"Ternary", f.Ternary},
		{"Lambda", f.Lambda},
		{"Ref", f.Ref},
		{"Unit", f.Unit},
		{"Enum", f.Enum},
		{"AsyncReactive", f.AsyncReactive},
		{"AsyncCalls", f.AsyncCalls},
		{"Computed", f.Computed},
		{"ListLambdas", f.ListLambdas},
		{"Reactivity", f.Reactivity},
		{"Declarative", f.Declarative},
		{"InlineComponents", f.InlineComponents},
		{"ImplicitRecv", f.ImplicitRecv},
		{"StructSpread", f.StructSpread},
		{"ViewStatements", f.ViewStatements},
		{"InstanceState", f.InstanceState},
		{"InsertBefore", f.InsertBefore},
		{"InlineSlots", f.InlineSlots},
		{"AsyncPost", f.AsyncPost},
		{"AsyncSpawn", f.AsyncSpawn},
		{"Effects", f.Effects},
		{"Navigation", f.Navigation},
		{"GenericFuncs", f.GenericFuncs},
		{"GenericComponents", f.GenericComponents},
		{"Match", f.Match},
		{"MatchValue", f.MatchValue},
		{"ViewMatch", f.ViewMatch},
		{"OptionMatch", f.OptionMatch},
		{"StructComponents", f.StructComponents},
		{"StdlibContextParam", f.StdlibContextParam},
		{"FocusOrder", f.FocusOrder},
		{"Canvas", f.Canvas},
		{"ReactiveCanvas", f.ReactiveCanvas},
		{"NavigationHrefs", f.NavigationHrefs},
	} {
		if c.held {
			parts = append(parts, c.name)
		}
	}
	return strings.Join(parts, ",")
}
