// Package lower performs capability-driven IR-to-IR transformations between
// the optimizer and codegen. Each lowering pass is gated by a Caps flag:
// passes whose flag is true rewrite high-level constructs into simpler
// primitives that the target platform/language can natively emit.
package lower

import "strings"

// Features declares which high-level SNGL constructs a language or platform
// can natively emit. A flag set to true means no lowering is needed for that
// construct; false means the corresponding lowering pass must run.
//
// The zero value is safe: every flag defaults to false (needs lowering), so
// new platforms automatically get all lowering passes until they opt in.
//
// Languages return Features from Capabilities(). Platforms receive the
// language's Features and return the combined set, restricting any constructs
// the platform cannot consume.
//
// Call ToLowerCaps() to convert to the Caps shape required by Lower().
//
// StructComponents and StdlibContextParam are platform-opt-in passes rather
// than language limitations: setting them to true requests the corresponding
// lowering even when the language could handle the construct directly.
type Features struct {
	Toggle           bool // can emit x!! natively
	Ternary          bool // can emit a ? b : c natively
	Lambda           bool // can emit closures natively
	Ref              bool // can emit ref<T> natively
	Unit             bool // can emit unit types natively
	Enum             bool // can emit enum types natively
	AsyncReactive    bool // can handle async in reactive contexts natively
	Computed         bool // can handle computed vars natively
	Timer            bool // can handle timer decls natively
	ListLambdas      bool // can emit xs.filter(f) / xs.map(f) natively
	Reactivity       bool // can handle reactive deps natively (false → explicit updater stmts)
	Declarative      bool // can handle declarative visual tree (false → flat create/update/delete calls)
	InlineComponents bool // can handle inline component references (false → inline into main)
	ImplicitRecv     bool // can handle implicit receiver (false → explicit Args[0])
	StructSpread     bool // can handle struct-literal spreads (false → flatten)

	// StructComponents requests that components compile to structs with methods
	// rather than functions/closures. Set by platforms that use this model.
	StructComponents bool
	// StdlibContextParam requests a hidden trailing parameter threaded through
	// every stdlib func reachable from user code that reads a context.
	StdlibContextParam bool
	// FocusOrder requests focus-tracking lowering: the pass walks the visual
	// tree, assigns integer IDs to focusable nodes, and injects __focusID,
	// __focusNext, and __focusPrev into the component. Platforms that render
	// their own widgets (canvas, TUI) opt in; native-widget platforms that
	// delegate focus to the OS do not.
	FocusOrder bool
	// Canvas requests canvas-drawing lowering: passCanvas walks shape-children
	// bodies and transforms them into draw functions with intrinsic calls.
	// Set by platforms that support Canvas2D rendering (e.g. HTML5 canvas).
	Canvas bool
	// ReactiveCanvas requests passCanvasReactivity: injects CanvasRedrawStmt
	// into handler/timer bodies that mutate vars read by a canvas draw func.
	ReactiveCanvas bool
}

// AllFeatures returns a Features with every capability enabled. Use as a
// starting point for full-featured languages: disable only what you can't emit.
func AllFeatures() Features {
	return Features{
		Toggle:           true,
		Ternary:          true,
		Lambda:           true,
		Ref:              true,
		Unit:             true,
		Enum:             true,
		AsyncReactive:    true,
		Computed:         true,
		Timer:            true,
		ListLambdas:      true,
		Reactivity:       true,
		Declarative:      true,
		InlineComponents: true,
		ImplicitRecv:     true,
		StructSpread:     true,
	}
}

// ToLowerCaps converts Features to the Caps shape consumed by Lower().
func (f Features) ToLowerCaps() Caps {
	return Caps{
		NoToggle:           !f.Toggle,
		NoTernary:          !f.Ternary,
		NoLambda:           !f.Lambda,
		NoRef:              !f.Ref,
		NoUnit:             !f.Unit,
		NoEnum:             !f.Enum,
		NoAsyncReactive:    !f.AsyncReactive,
		NoComputed:         !f.Computed,
		NoTimer:            !f.Timer,
		NoListLambdas:      !f.ListLambdas,
		NoReactivity:       !f.Reactivity,
		NoDeclarative:      !f.Declarative,
		NoInlineComponents: !f.InlineComponents,
		NoImplicitRecv:     !f.ImplicitRecv,
		NoStructSpread:     !f.StructSpread,
		StructComponents:   f.StructComponents,
		StdlibContextParam: f.StdlibContextParam,
		FocusOrder:         f.FocusOrder,
		Canvas:             f.Canvas,
		ReactiveCanvas:     f.ReactiveCanvas,
	}
}

// Caps declares which high-level SNGL constructs the target cannot consume
// directly. A flag set to true requests the corresponding lowering pass.
// Derived from Features.ToLowerCaps(); prefer Features in public APIs.
type Caps struct {
	NoToggle        bool // x!! → x = !x
	NoTernary       bool // a ? b : c → if/else stmt with temp var
	NoLambda        bool // closures → top-level funcs + captured-state struct
	NoRef           bool // ref<T> → synthesized one-field reference-semantic struct
	NoUnit          bool // unit values → underlying int
	NoEnum          bool // enum members → int constants
	NoAsyncReactive bool // async in reactive contexts → settled state-field + kicker
	NoComputed      bool // computed vars → inlined exprs or memoized funcs
	NoTimer         bool // timer decls → explicit scheduler.At()/cancel() calls
	// StructComponents declares that components compile to structs with
	// methods rather than functions/closures. User-declared `context #foo`
	// blocks must be lowered into hidden Vars on each component in
	// Reach(ctx) and hidden Params on each user func in Reach(ctx) — the
	// component receiver carries the context value rather than a
	// closure-captured variable. Target languages with function-shaped
	// components (today: JS via html) can in principle keep user contexts
	// as closure captures; today they still set this for parity with
	// StdlibContextParam, but the two flags exist so a future migration
	// can flip just one off.
	StructComponents bool

	// StdlibContextParam threads a hidden trailing parameter through every
	// stdlib func reachable from user code that reads a context. Today this
	// covers the i18n stdlib wrappers (i18n.tr et al.) reading the active
	// locale. Even closure-based component targets need this because stdlib
	// funcs live outside the user closure scope.
	StdlibContextParam bool
	NoReactivity       bool // reactive deps → explicit updater stmts after each mutation
	NoDeclarative      bool // visual node tree → flat stream of create/update/delete IR calls
	NoListLambdas      bool // xs.filter(f) / xs.map(f) → explicit accumulator + for-loop.
	NoInlineComponents bool // user-defined non-recursive components → inlined into main (per-instance renamed vars/funcs/timers/body)
	NoImplicitRecv     bool // method calls with implicit receiver → explicit Args[0]
	NoStructSpread     bool // struct-literal spreads (`{...x}`) → flattened literal / merge<Struct> call
	// FocusOrder requests focus-tracking lowering. See Features.FocusOrder.
	FocusOrder bool
	// Canvas requests passCanvas lowering. See Features.Canvas.
	Canvas bool
	// ReactiveCanvas requests passCanvasReactivity: after passCanvas extracts
	// draw funcs and passReactivity wires state deps, this pass injects
	// CanvasRedrawStmt into any handler/timer body that mutates a var read by
	// a canvas draw func. Platforms translate CanvasRedrawStmt to their native
	// "clear and redraw" operation.
	ReactiveCanvas bool
}

// Merge returns the field-wise OR of c and other. Either side disabling a
// feature requests the corresponding lowering pass.
func (c Caps) Merge(other Caps) Caps {
	return Caps{
		NoToggle:           c.NoToggle || other.NoToggle,
		NoTernary:          c.NoTernary || other.NoTernary,
		NoLambda:           c.NoLambda || other.NoLambda,
		NoRef:              c.NoRef || other.NoRef,
		NoUnit:             c.NoUnit || other.NoUnit,
		NoEnum:             c.NoEnum || other.NoEnum,
		NoAsyncReactive:    c.NoAsyncReactive || other.NoAsyncReactive,
		NoComputed:         c.NoComputed || other.NoComputed,
		NoTimer:            c.NoTimer || other.NoTimer,
		StructComponents:   c.StructComponents || other.StructComponents,
		StdlibContextParam: c.StdlibContextParam || other.StdlibContextParam,
		FocusOrder:         c.FocusOrder || other.FocusOrder,
		Canvas:             c.Canvas || other.Canvas,
		ReactiveCanvas:     c.ReactiveCanvas || other.ReactiveCanvas,
		NoReactivity:       c.NoReactivity || other.NoReactivity,
		NoDeclarative:      c.NoDeclarative || other.NoDeclarative,
		NoListLambdas:      c.NoListLambdas || other.NoListLambdas,
		NoInlineComponents: c.NoInlineComponents || other.NoInlineComponents,
		NoImplicitRecv:     c.NoImplicitRecv || other.NoImplicitRecv,
		NoStructSpread:     c.NoStructSpread || other.NoStructSpread,
	}
}

// String returns a comma-separated list of enabled flags in pass-execution
// order (NoUnit first, NoDeclarative last). Empty string when no flags set.
func (c Caps) String() string {
	var parts []string
	if c.NoUnit {
		parts = append(parts, "NoUnit")
	}
	if c.NoEnum {
		parts = append(parts, "NoEnum")
	}
	if c.NoTernary {
		parts = append(parts, "NoTernary")
	}
	if c.NoAsyncReactive {
		parts = append(parts, "NoAsyncReactive")
	}
	if c.NoComputed {
		parts = append(parts, "NoComputed")
	}
	if c.NoLambda {
		parts = append(parts, "NoLambda")
	}
	if c.NoListLambdas {
		parts = append(parts, "NoListLambdas")
	}
	if c.NoRef {
		parts = append(parts, "NoRef")
	}
	if c.NoToggle {
		parts = append(parts, "NoToggle")
	}
	if c.StructComponents {
		parts = append(parts, "StructComponents")
	}
	if c.StdlibContextParam {
		parts = append(parts, "StdlibContextParam")
	}
	if c.NoReactivity {
		parts = append(parts, "NoReactivity")
	}
	if c.NoInlineComponents {
		parts = append(parts, "NoInlineComponents")
	}
	if c.NoStructSpread {
		parts = append(parts, "NoStructSpread")
	}
	if c.NoImplicitRecv {
		parts = append(parts, "NoImplicitRecv")
	}
	if c.NoTimer {
		parts = append(parts, "NoTimer")
	}
	if c.NoDeclarative {
		parts = append(parts, "NoDeclarative")
	}
	if c.FocusOrder {
		parts = append(parts, "FocusOrder")
	}
	if c.Canvas {
		parts = append(parts, "Canvas")
	}
	if c.ReactiveCanvas {
		parts = append(parts, "ReactiveCanvas")
	}
	return strings.Join(parts, ",")
}
