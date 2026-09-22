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
// every capability-gated pass runs for it. That is what lets the language
// grow: a new construct arrives with a pass that converts it away, and every
// target that has not heard of it keeps working unedited, opting in when its
// own code generator can do better than the pass. Under the other polarity,
// silence would mean "I emit this" on every declaration written before the
// construct existed, and adding one would break every target at once.
//
// There used to be a second record, Caps, saying the same thing inverted --
// NoTernary against Ternary -- with ToLowerCaps, Merge and String each naming
// every field. One record is one list.
type Features struct {
	Toggle        bool // can emit x!! natively
	Ternary       bool // can emit a ? b : c natively
	Lambda        bool // can emit closures natively
	Ref           bool // can emit ref<T> natively
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
	// The optimizer reads it to decide whether to unroll such a loop.
	//
	// Unlike every other capability here it gates no pass: what a target
	// declares about itself is a wider question than which passes run for it.
	ViewStatements bool

	// InsertBefore says the platform's container can put a child at a
	// position, not only at the end -- so a keyed reconciliation may move one
	// child instead of rebuilding the run. A toolkit whose container appends
	// is not wrong for lacking it, and the rebuild it keeps is correct, just
	// less direct. A platform claiming this must implement
	// codegen.ChildInserter.
	InsertBefore bool
	// AsyncPost says the platform can run a closure back on the thread it
	// draws on, and answers lower.AsyncPostIntrinsic with the call that does
	// it. Without it there is nowhere for a blocking call's answer to land,
	// and passAsyncOffload refuses the program rather than writing a state
	// update onto a goroutine that does not own the widgets.
	//
	// A platform claiming it must register an emitter for that id; codegen's
	// tests check the two against each other.
	AsyncPost bool
	// AsyncSpawn says the language can run a closure without waiting for it,
	// and answers lower.AsyncSpawnIntrinsic with the call that does -- Go's
	// `go func(){}()`. It sits on the language axis where AsyncPost sits on
	// the platform's, because starting work is a property of the host language
	// and getting back to the drawing thread is a property of the surface.
	AsyncSpawn bool
	// Effects says the platform emits an `effect` node itself and wants it
	// left standing. A framework whose own model already brackets a lifetime
	// keyed on a value -- Compose's DisposableEffect is one -- expresses the
	// construct better than the calls passEffect lowers it to, and gets the
	// node instead: its two handlers and its key are all the declaration says.
	Effects bool

	// The five below are requests rather than capabilities, which is the split
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
}

// NoLowering is the Features under which no capability-gated pass runs: every
// construct claimed, nothing granted, nothing requested.
//
// It is a test's answer and not a target's. A real target says what it can do,
// and the one place a build wants this shape is the interpreted path below.
func NoLowering() Features {
	return Features{
		Toggle:           true,
		Ternary:          true,
		Lambda:           true,
		Ref:              true,
		Unit:             true,
		Enum:             true,
		AsyncReactive:    true,
		AsyncCalls:       true,
		Computed:         true,
		ListLambdas:      true,
		Reactivity:       true,
		Declarative:      true,
		InlineComponents: true,
		ImplicitRecv:     true,
		StructSpread:     true,
		ViewStatements:   true,
		Effects:          true,
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
		{"InsertBefore", f.InsertBefore},
		{"AsyncPost", f.AsyncPost},
		{"AsyncSpawn", f.AsyncSpawn},
		{"Effects", f.Effects},
		{"StructComponents", f.StructComponents},
		{"StdlibContextParam", f.StdlibContextParam},
		{"FocusOrder", f.FocusOrder},
		{"Canvas", f.Canvas},
		{"ReactiveCanvas", f.ReactiveCanvas},
	} {
		if c.held {
			parts = append(parts, c.name)
		}
	}
	return strings.Join(parts, ",")
}
