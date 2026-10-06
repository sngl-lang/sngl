package lower

import "fmt"

// orderConstraint is one requirement the registry order has to satisfy:
// Earlier must run before Later, and Why says what goes wrong when it does
// not. TestPassOrderConstraints checks each against `passes`.
//
// The reason is the data, not a comment about it. A full-order list asserts
// every pair at once, says nothing about which pair matters, and is silenced
// by moving a name until it passes -- which is how ViewForElse came to sit
// where it does without anyone deciding it must precede Query. A pass added
// here conflicts only with a stated requirement, and the failure quotes the
// requirement.
//
// The list is not a total order and is not meant to become one: a pair absent
// from it is a pair the pipeline does not depend on, which is most of them --
// swapping each of the 40 adjacent pairs in `passes` and rebuilding all 113
// goldens changed the output for 8.
//
// All 8 are constraints below, and several non-adjacent pairs were confirmed
// the same way; where the failure says something the pass's own documentation
// does not, the Why quotes it. The rest are stated from that documentation and
// no current fixture reaches them -- a gap in the fixtures, not permission to
// reorder, since an untested requirement is still one.
type orderConstraint struct {
	Earlier string
	Later   string
	Why     string
}

func (c orderConstraint) String() string {
	return fmt.Sprintf("%s before %s: %s", c.Earlier, c.Later, c.Why)
}

// orderConstraints is every ordering the pipeline actually depends on.
var orderConstraints = []orderConstraint{
	{"HoistBodyTypes", "NoRef",
		"NoRef synthesizes a box struct per element type and deduplicates them by the element's type *name*, so two body-local types still sharing one name are handed one __ref_ box between them"},
	{"PlatformExtensionBody", "InlinePure",
		"until the active platform's override is swapped into Component.Body, a stdlib component is an empty stub, and inlining an empty stub inlines nothing"},
	{"PlatformExtensionBody", "UnprovidedContext",
		"a read an override body makes is in no body until the override is swapped in, so it is not folded and reaches Context as state"},
	{"UnprovidedContext", "Context",
		"once Context has lowered a read to a hidden field, nothing sees through it to the default, and the literal a platform override wanted is a runtime read"},
	{"PlatformExtensionBody", "PropBindings",
		"the same stub, for a binding written on a stdlib component: with no body swapped in there is nothing for the write-back handler to be attached to, and the android radio goldens come out with `onClick = {}`"},
	{"InlinePure", "NoInlineComponents",
		"the user-component inliner rewrites the very calls InlinePure matches on, so a stdlib wrapper standing behind one is never folded away -- swapped, 96 of the 113 goldens grow an un-inlined __cf_ wrapper and the __merge_Style helper its struct spread needs"},
	{"NavigationValues", "PlatformExtensionBody",
		"composeOverriddenBuiltins repoints a node whose kind a target overrides at a copy with the kind cleared, and html overrides nav.stack and nav.page with its own primitives: found by kind afterwards, a page would have no record"},
	{"NavigationValues", "NavigationHrefs",
		"a link's `to` and a go's page are found by the record this puts in the handle's place"},
	{"NavigationHrefs", "PlatformExtensionBody",
		"the page a link or a go names, and the params its call site wrote, are found on a nav.page node by its kind, which composition clears"},
	{"NavigationHrefs", "InlinePure",
		"the ui.link a nav.link becomes is sngl:ui's, whose override the inliner substitutes only if it meets the node"},
	{"NavigationValues", "Navigation",
		"the structure half finds a page a `go` or a link names by the record the value half put in the handle's place, and the stack's current page by the one call `pages.current` became"},
	{"NavigationValues", "NodePropReads",
		"`pages.current` is a computed selected off a handle, which NodePropReads would answer as a prop the stack declares none of"},
	{"Navigation", "ImplicitState",
		"the button a nav.link becomes has no two-way prop, and the stack's pages are an if-chain only once this has run: a page left to ImplicitState is a node with content"},
	{"Navigation", "NoReactivity",
		"the if-chain over the pages has to reach reactivity as an ordinary view conditional, so a write to the current page re-renders it"},
	{"Navigation", "InlinePure",
		"the button a nav.link becomes is sngl:ui's, whose override the inliner substitutes only if it meets the node"},
	{"PlatformExtensionBody", "HandleParams",
		"the node a handle names has to be the one this target renders, its bindings the override's"},
	{"HandleParams", "ImplicitState",
		"a write of an unbound two-way prop through a handle is a write of the instance's cell only once ImplicitState renames it, and the write exists only once the call handing the handle is inlined"},
	{"PlatformExtensionBody", "ImplicitState",
		"an override body the program wrote is in no body until it is swapped in, so an unbound two-way prop written there would get no cell"},
	{"ImplicitState", "PropBindings",
		"the cell reaches the node as a `:prop` binding, which PropBindings is what lowers"},
	{"ImplicitState", "NodePropReads",
		"a read of an unbound two-way prop off its `#id` names the cell; NodePropReads would answer it with the value the call site wrote, which is only where the cell starts"},
	{"ImplicitState", "NoInlineComponents",
		"the cell is a var of the component this pass wraps the node in, and the inliner is what gives that var a copy per instance"},
	{"PropBindings", "RefLoop",
		"a bound prop is still an ir.Bindings entry until this pass makes it an @event handler, and RefLoop rewrites what the handler assigns to"},
	{"PropBindings", "NoToggle",
		"the same, for a binding whose handler toggles its target: NoToggle rewrites the Toggle stmt PropBindings emits"},

	{"NoInlineComponents", "DirectCalls",
		"the call through a name that this pass makes direct is one the inliner leaves, by substituting a func-typed prop with the function it was given"},
	{"DirectCalls", "Effect",
		"an effect's settle is placed by the state its key reads, followed through Call.Func; a key calling through a name reads nothing, and no write settles it"},
	{"DirectCalls", "NoReactivity",
		"the same, for a render slot whose condition calls through a name"},

	{"SpreadOnce", "NoAsyncCalls",
		"the temp a spread's operand is bound to is a statement of its own, and a blocking call in it has to be in a statement by the time the offload splits a body around one"},

	{"RefLoop", "NoReactivity",
		"the list[idx].field write this leaves has to reach reactivity as a mutation of the list var, or the loop's slot never re-renders"},
	{"RefLoop", "NoToggle",
		"a toggled element ref is `&t!!`; the ref has to become list[idx] before the toggle becomes an assignment to it"},
	{"RefLoop", "IterKind",
		"IterKind reads a loop's final head, and this pass desugars `for var &t = xs` into an ordinary two-variable indexed loop"},
	{"NoListLambdas", "IterKind",
		"IterKind reads a loop's final head, and this pass synthesizes fresh loops for xs.filter/xs.map"},
	{"IndexedIter", "IterKind",
		"IterKind reads a loop's final variable arity, and this pass turns a two-variable loop over a pull sequence into a one-variable loop plus a counter"},

	{"ViewForElse", "ComponentProps",
		"the emptiness expression it synthesizes names whatever the loop head names, so every pass that rewrites a name has to see it -- a promoted component prop is the one a fixture catches, and a computed indirection or a context read is the same requirement untested"},
	{"ViewForElse", "NoReactivity",
		"the `if` this leaves has to reach reactivity as an ordinary view conditional, so a reactive iterable makes it a render slot"},

	{"NoAsyncReactive", "NoComputed",
		"it introduces sync state vars that NoComputed would otherwise inline away"},
	{"NoReactivity", "NoAsyncCalls",
		"the updater statements a state write is followed by are what has to run on the UI thread, and they land in the posted closure only by sitting after the assignment when this pass splits the body"},
	{"NoAsyncCalls", "ErrorCatch",
		"a blocking call splits a handler body across a goroutine and the closure it posts back, and a catch placed around the body before the split is on the wrong goroutine to recover the tail's panic"},
	{"NoInlineComponents", "ErrorScope",
		"a component's body is under the boundaries of the tree that renders it only once it is spliced there, and before that the handler is the declaration's, shared by every instance"},
	{"ErrorScope", "ErrorCatch",
		"the catch block a handler body gets is the one its raises resolved to, and a raise the render tree resolves is native until this pass has run"},
	{"NoAsyncReactive", "NoReactivity",
		"the settle vars it synthesizes have to be visible to the dep analysis as reactive state"},
	{"NoComputed", "NoReactivity",
		"reactivity tracks reads of state; a computed call is an indirection it cannot see through"},
	{"NoLambda", "NoReactivity",
		"reactivity's helpers inject closures, and lifting those would be lifting shapes NoLambda never saw"},
	{"NoToggle", "NoReactivity",
		"`x!!` is not an assignment, so reactivity would not credit it as a write to x"},

	{"NoToggle", "InlinePure",
		"inlining copies a wrapper body into its caller, so the high-level shapes in it have to be lowered first"},
	{"NoLambda", "InlinePure",
		"the same: a lambda in a wrapper body is lifted once, before the body is copied"},

	{"Context", "InlinePure",
		"the provider rewrite threads a hidden arg across component boundaries, which only exist while the components are still separate"},
	{"Context", "NoReactivity",
		"the __ctx_<name> var it synthesizes is state, and reactivity has to see it as a dep"},

	{"InlinePure", "NoReactivity",
		"reactivity flattens a NodeInst into CreateNode + Assigns, past the point a wrapper call can be recognised and substituted"},
	{"InlinePure", "StampUsage",
		"inlining can collapse an i18n wrapper into a direct intrinsic, and the usage flags have to count the call as it finally reads"},

	{"BoundaryPassthrough", "StampUsage",
		"a boundary declares no payload of its own: what names ErrorEvent is a raise, a catch block or a fallible call, and what a boundary was handed must not decide it"},
	{"IterKind", "BoundaryPassthrough",
		"a boundary is how a pass reaches its handler -- a catch block holds it only as an alias -- so a loop in one is stamped only while the boundary stands"},
	{"ErrorCatch", "BoundaryPassthrough",
		"the catch block a handler body becomes resolves to the boundary's handler, which the boundary has to be there to have"},
	{"NoInlineComponents", "RecursionDepth",
		"a recursive component is the one the inliner leaves standing, and it is the only one that needs a depth bound"},
	{"NoInlineComponents", "ComponentProps",
		"RuntimeInstance is the mark the inliner leaves on a declaration it could not flatten, and that mark is what decides which components get prop cells"},
	{"NoInlineComponents", "InstanceSlots",
		"it copies the declarations the inliner marked RuntimeInstance, and only the inliner knows which instantiations survive"},
	{"InstanceSlots", "InstanceEvents",
		"a handler written in the children becomes an event the copy declares, and InstanceEvents is what turns a declared event into a prop"},
	{"InstanceSlots", "ComponentProps",
		"a value the children read from the caller becomes a prop of the copy, and ComponentProps is what gives it the cell the render writes"},
	{"RecursionDepth", "ComponentProps",
		"__depth is an ordinary prop, and ComponentProps is what promotes a prop to a settable cell"},

	{"NoInlineComponents", "Canvas",
		"a canvas written inside a user component is not in the tree this pass walks until the inliner has spliced that component into its caller -- moved ahead of it, canvas_shapes_from_data and generate_canvas_through_import extract no draw function at all, and ahead of InlinePure as well the early extraction freezes the host component into a runtime instance whose reactive `lit` prop NoReactivity then refuses"},
	{"Canvas", "NoReactivity",
		"a shape has to leave the visual tree first, or reactivity gives it a node id and emits setAttribute against a node that was never in the DOM"},
	{"Canvas", "CanvasReactivity",
		"the redraws are injected into the draw funcs this pass extracts"},
	{"NoReactivity", "CanvasReactivity",
		"a redraw fires on a state write, which is the dependency reactivity settles"},
	{"NoTernary", "CanvasReactivity",
		"CanvasRedrawStmt is a statement NoTernary's walker does not handle, so it must not be in the tree while NoTernary runs"},

	{"Effect", "NoReactivity",
		"a mount handler that writes state has to reach reactivity as an ordinary assignment, or nothing patches what reads that state"},

	{"SlotChildInstances", "InstanceEvents",
		"it turns a slot child's handler into a declared event, and InstanceEvents is what turns a declared event into a prop"},
	{"InstanceEvents", "ComponentProps",
		"it turns an event into a func-typed prop, and ComponentProps is what gives that prop its cell and setter"},
	{"ComponentProps", "NoReactivity",
		"a prop promoted to a var is a reactive cell, and the updater injection has to see it as one -- that is how a setter comes to re-fire the slots reading the prop"},

	{"NoReactivity", "NoTernary",
		"the value-only `if` NoTernary synthesizes would be misread as a reactive render slot and relocated into a __renderSlotN func, orphaning the `var __ltN` it assigns into"},

	{"InstanceBodies", "NoDeclarative",
		"both flatten a body, and they disagree about handlers: an instance's are kept inline because a factory's handler closes over the call that built it, and the declarative pass would lift them -- swapped, fyne and gtk4 emit `OnTapped = c.__n0_click_handler` for testdata/factory_reactive_slot"},
	{"NoDeclarative", "NodeEscape",
		"the escape analysis runs over the flat `var __nN = CreateNode(...)` + `#__nN` sequence NoDeclarative emits, and there is nothing to analyse before it"},
	{"NoDeclarative", "NoRef",
		"NoDeclarative's handler lifter emits fresh ref<T> shapes, and NoRef is what boxes them"},
	{"NoLambda", "NoRef",
		"NoLambda's lifter is the other producer of ref<T>, and it seeds pkg.AddressedVars for NoRef to read"},
}
