# Root, windows and the output directive

**`sngl:builtin`'s `root` is the family a package body accepts**, and that is
the whole of what makes a window top-level — no syntactic rule names the
construct. So a `node` at the root of a file is the ordinary
tree-membership error, an `if` or a `for` there still works (neither is a
node), and a component that names `root` itself renders windows, which reach
the build when something instantiates it.

It is **ambient** because it describes every package rather than widgets. It
lived in `sngl:ui`, and that forced an exemption: `output` is declared in
`sngl:builtin`, which cannot import `sngl:ui`, so `familyOptional` let the
directives — `output` and `cache.inputs` — omit a return position. Both now
name `root` like `window` does, and `familyOptional` answers only for an
extension body. Load order is not a problem: `lib/builtin/root.sngl` imports
`sngl:family` for the mark, which `sngl:builtin` already reached through
`sngl:build`, and nothing in `sngl:family` needs the ambient scope. `ir.Convert`
spells it bare, as it spells every ambient name.

What membership does *not* replace is the directives' own rule: each is
diverted in `registerRootVisualNode` and read once, before anything runs, so
one written anywhere but the root of a file is still its own error ("output may
only be written at the root of a file") rather than a family question. A
component whose family is `root` could otherwise render one, and nothing would
read it.

**The package body is checked file by file.** `checkPackageBody` gathers
every file's root statements, and enters each one's own file (`fileOf`) before
checking it: checked in whatever file scope pass1 ended on, `time.timer` at the
root of one file was undefined because the file sorted after it imported no
`time` (`cmd/sngl/testdata/package_body_reads_its_own_imports.txt`).

**A window is an ordinary component**. `lib/ui/window.sngl`
declares `ui.window` with no body and no mark, a member of `root`, and every
platform overrides it with a primitive of its own: a Toplevel on gtk4 and
fyne, a `Screen` on bubbletea and android, html's `Window` and `none`'s.
Nothing in the compiler names a window -- there is no `ir.Window`,
`pkg.Windows`, `ir.AllWindows` or `IsWindowNode`, no `#[builtin("window")]`,
and no window arm in the checker, the lowering, the optimizer, the
interpreter or a platform. What a window needed, each had a general answer
(one each): the `root` family to stand at the root of a file, the
node ops to be created and destroyed with the tree, the primitive's own
codegen for its children, a `boundary` for `@error`, a two-way prop and an
event for `visible` and `@closed`, and `nav.page` for everything html once
needed a window to be a destination for.

What used to need the separate construct is said generally instead. A window
written at the root of a file is a statement of `pkg.Body`, in the order
written, so a gtk4 or fyne window is attached to the application where it
stands rather than ahead of every root statement. A window inside a window is
an ordinary membership error, the window's rest slot taking `ui.node`.
A window's `open` and `close` are written outside the declaration's block,
`func window.open(w window) { w.visible = true }`, since a bodyless
declaration has no block to hold them; such a method reads the component only
through the receiver it is passed, and `passHandleParams` inlines a call handing
it a node's handle, so the write lands on the var `:visible` bound or on the
instance's cell (`internal/lower/handle_params.go`). A window's `@error` is a
`boundary(@error(e) { error(e) })` around the content in each override, so it
always catches: a window whose call site handles no `@error` catches a raise
and drops it (`testdata/window_error_unhandled.sngl`).

**A one-way prop read off a component instance's `#id` folds to the
expression its call site gave**, which is what the prop says for as long as the
node is rendered -- `home.title` off a window is the case that needed it, a
Select left standing being a dangling reference once the inliner has spliced
the instance away. `evalCtx.nodeForHandle` finds the node; a primitive's props
are `passNodePropReads`' and a two-way prop is a cell, so neither folds here.
It is folded again against the context the *read* sits in rather than the one
the prop was written in. That is not a detail: `window #page(title=it.title)`
inside a `for` puts the loop variable in the prop, and a read of `page.title`
from the window's body sits where the unroll has already passed -- returned as
written it stayed `it.title`, named nothing, and the page rendered an empty
span with no diagnostic. Which makes a prop that reads itself,
`window #h(title = h.title)`, a fold that re-enters on the same prop forever,
so `evalCtx.foldingProp` holds the pairs in flight and leaves the select
standing on re-entry -- the state a prop with no answer already reached codegen
in. Keyed by node *and* prop, so two nodes naming each other terminate on the
second key rather than looping on the first.

**A node may not read its own `#id` in its own arguments**, and that is now a
positioned checker error (`reportSelfReferentialProps`, `internal/checker/selfref.go`).
The id names the instance the argument list is building, so `window #h(title = h.title)` asks the title for the title. All three forms of a node
that carries an id are held to it — a window, an element-ref call
(`text #t(value = t.value)`), and the `context` declaration, whose id *is* its
own declaration, so `context #depth(depth)` is the same error rather than an
`undefined` naming the context on the line declaring it. Matched by *symbol*:
an id is a declaration in its component's scope and shadows an outer `foo`,
so `text #foo(value=foo)` beside a package's `foo` reads the node and is this
error, and beside a var of the component is a redeclaration (see
design/nodes.md). A handler is untouched — `@click` is split
off before the rule is asked, and a handler runs after mount.

What it does not cover is a cycle through *other* declarations, which is the
checker's cycle rule (`reportDeclarativeCycles`, `internal/checker/cycles.go`):
a declaration that reads itself however many hops round has no value to be
read, and it is refused at the read that leaves the declaration written first,
naming each read on the way round. The graph's vertices are consts, the
functions a const calls -- evaluated where the const is folded -- and each
node's props, an edge running from a prop to each `x.p` it reads through a
handle and to every prop of a handle it reads whole. A cycle of functions
alone is recursion and is left alone. So `const a int = a`, `const a = b`
beside `const b = a`, `window #a(title = b.title)` beside
`window #b(title = a.title)` and two canvas shapes reading each other's radius
are all positioned errors (`testdata/error_declarative_cycle.sngl`,
`cmd/sngl/testdata/const_reads_itself.txt`, `node_prop_read_cycle.txt`,
`window_prop_reads_itself.txt`). Each used to reach the build: the const fold
recursed until a guard left the initializer standing (a page rendering
nothing, silently, and before the guard a stack overflow with no position),
and the props were refused by the lowering or left standing by the fold --
an empty `<title>` on html, a name nothing declared on fyne. Those guards
(`evalCtx.foldingProp`, the const fold's, `passNodePropReads`') remain for IR
the checker did not see.

**A window owns no state**: it is a node, so what its body declares belongs
to its container — the package, or the component that renders it: the checker
leaves a window's `var` as an `*ir.LocalVar` statement in the body and
`passHoistState` moves it there, as it does a `var` in any block of a view
(below), and a `func` in a window body is registered on the container
directly.

**A `var` written in a block of a view is state of that block**, as a
component's `var` is state of the component, and nothing about a window
decides it. The checker leaves one written anywhere but a component's or the
package's own top level as an `*ir.LocalVar` where it was written, which no
target implements: bubbletea and gtk4 emitted a local of the render function
reset on every frame with the handler writing a name the Model lacks, and
android and html declared it nowhere. `passHoistState`
(`internal/lower/hoist_state.go`) answers by what the block is. Rendered once
-- a window's top level, a vbox outside any loop -- its vars join its owner
(the component, or the package) under their own names, renamed `<n>__block<k>`
only where the owner already has the name. Under a `for`, an `if` over state, a
page or a slot's population, the block becomes an instance of a component
synthesized for it, `__block_state<N>`, which the inliner builds per copy as it
does any component with state: what the block reads of the loop, a slot's
parameter or the owner's props arrives as a prop, and what it reads of the
owner's state as a two-way prop bound to it, so a write in a copy is the
owner's. A block calling a function of the component around it from such a
position is refused, since a copy cannot reach it. A provider's own body is
left to passContext, which splices it up and promotes its vars with the
provided value in scope for their initializers. The interpreter mounts a list
declaring a `var` in a scope of its own (`blockEnv`), cached by the list's path
as a component instance's is and evicted with it, and a handler's write in it
is carried back through every loop snapshot above (`Env.writeBack`).
`testdata/view_block_state.sngl` runs it on the interpreter and
`view_block_state.txtar` is the code on all six targets.

That made a two-way prop written inside a component built at run time reach
codegen, which it never had: passPropBindings names the write-back event for
the prop it reports, and passInstanceEvents skipped an event whose name a
prop already had, so the emit came out as a call to `emit` nothing declares --
on gtk4, fyne and html alike. Such an event's func prop is `__on_<prop>`
(`testdata/two_way_prop_runtime_instance.txtar`). And a struct with no fields
-- the empty `meta` a nav record carries, `struct Empty {}` -- is a Kotlin
class with value equality rather than a data class, which needs a field.
`Timers` is **gone**, and with it
`ir.Timer`: the record held an interval, a gate and a tick body, and every one
of those is readable off the timer-primitive node -- the `interval` and
`enabled` props and the `@tick` handler -- so it carried nothing the tree did
not, while costing a field on three owners and a timer arm in nineteen walks.
`codegen.CollectTimers` reads them, folding the enclosing `if` conditions onto
the gate as the pass did. #243 is untouched by that: bubbletea still cannot
lower a timer to an effect -- Elm lets nothing outside `Update` touch the model,
and `Init()` needs a period and a body, which the closure an `@mount` hands over
cannot carry -- and it still reads `CommonAnalysis.Timers`, which still carries
both.

**A root component is an ordinary component**, and its state reaches a backend
the way every other component's does: something instantiates it,
`passNoInlineComponents` splices the body into the body that wrote the
instantiation, and the splice renames what the component declared per
instantiation. `component main root { var hits = 0; window … }` with
`main()` at the root of the file emits `hits__inst0`;
`testdata/root_component_state.txtar` is that on four targets.

**So a root component nobody instantiates renders nothing**, and that is a rule
rather than an oversight — the last remnant of the `main`-by-convention harness,
now gone. A program made of nothing but one is refused by
`internal/build.Emit`, which asks `ir.Package.IsProgram`: whether the package
body renders a node, not whether any body does. Asked the other way, a file
carrying a spare root component built, and fyne and bubbletea then met a
window in the middle of a component method and panicked.

The alternative was a lowering pass that scanned `pkg.Components` for the root
family, lifted the windows out of it and hoisted the vars and funcs
somewhere they would be emitted — the package, after #215 found that leaving
them on the emptied shell reached no backend at all. It had to strip the
receiver as it went, a component-body `func` being a method and a package-level
one not; the inliner does the same, in `dropReceiver`, because a clone hoisted
into a window or into the package body is no longer a method of anything and
route mode skips anything that still carries a receiver
(`s.Keep__inst0(…)` against a file declaring nothing of the name).

**Several windows each get it, and the platform says what that means.** One
root component rendering two windows is spliced once, so both read the one
cell: a target whose windows are one process shares it — bubbletea, fyne and
gtk4 each put it in one Model, and html too, the second window being a
`<dialog>` in the first one's document -- and a target whose pages are
separate documents copies it, html writing its own `state` into each page of
a stack. That divergence is the point
rather than a gap: two pages *are* two states and one process *is* one, and
forcing either way round in the lowering would be the language overriding the
platform it compiled to. The author picks a target knowing it. Shared top-level
state is mainly a performance tool, not the default way to write an
application — a declaration belongs in the window that uses it unless there is
a reason it does not.

Two things that follow. **One Model, one cell:** the shared pointer reaches a
Model through two owners, so `CodegenCtx.ModelState` dedupes by the `*ir.Var`
— emitted per owner instead, the Go targets declared `hits int` twice and did
not compile. And **reading it needs a scope**, which is the package body's:
`CodegenCtx.ScopedExprCtx` scopes a single Model to the harness root where a
harness isolated one, and to the package body (`ExprCtx.App`) otherwise.
Without a scope every such read rendered as a bare identifier the emitted Go
never declared.

`testdata/root_component_state_two_windows.txtar` is the fixture, and it is
where the per-platform table is written down.

**A program renders something at the root of a file**, checked by
`internal/build.Emit` rather than by the checker: `component c { … }` on its own
is a perfectly good thing to type-check, and it is only as something to *run*
that it has nothing to show. The package body is the application's view and
every node in it is a member of `root`, so a window counts, and so do a root
component's instance and a generated family's host -- a status bar with no
window builds (`cmd/sngl/testdata/program_without_window.txt`). Which is why
**`component main` has lost its harness convention** —
`CodegenCtx.RootDecl()` now answers only for a harness that has cleared the
package body on purpose (the test launcher isolating a component), and a `main` in
an ordinary program is an ordinary component. Nothing else asks for the name
either, and each place that did was a behavior: `sngl run --lang none` mounted
a `main` instead of the windows (`BuildProgramEnv` mounts the program now), a
directory of files each declaring one was read as a corpus of programs rather
than a package, a library declaring one could not be imported, and route mode
gave a `window #main` the index's `/` beside the first window's
(`route_window_named_main.txtar`). Route mode and html ask
`ir.Package.RootDecl()` for a harness root. `output(entry = home)` used to
choose a window, and went with a window's `href` since
which destination a build opens at is a stack's question -- the page at `/`,
or its first.

Two consequences of a built-in being identified by its mark: a kind classifies
*one* declaration and does not
alias two — type identity is per-declaration, so two structs sharing a mark
would be two incompatible types (the checker rejects a duplicated node mark).
And `output` is a *directive* kind rather than a node one: it parses as a
visual node and is marked on a component declaration like `context` is, but the
compiler reads the tree into `ir.Output` instead of rendering it. The mark is
what recognizes it — a package declaring its own `component output` gets that
component and no build directive — and what the mark permits is the root of a
file, once per package: any file may carry it, a second one anywhere names the
first, and a `sngl:` library package may not carry one at all
(`registerOutput`).

**Its contents are an ordinary component tree**, and the second user of
`sngl:family`. `output`'s slot takes `build.language` members, a language node's
takes `build.platform` ones, and a target declares its own node in its own
package: `sngl:language/go` declares `go`, `sngl:platform/html` declares `html`.
So the nesting rule is a slot's type, a misspelled target is an unresolved name,
and a build option is a declared prop with a type and a default — written at the
level that declares it, `output(name=…)` for the ones every target shares,
`go(goVersion=…)` for a language's, `html(minify=…)` for a platform's.
`ir.Output` is the projection a build reads: one per pair, with the three levels
of props flattened into `Options`.

Three things follow. A target package declares a node named for its tier and
binds *itself* as a namespace under the package's name so its bodies can write
`html.div`, and an output block reaches the node off the package, by its
`#[gen.name]`, rather than through that scope. Resolution inside the directive is by level rather than by scope
(`checker.targetNode`, keyed on `c.outputDepth`), trying the tier that level
accepts first and the other second — `none` is both a language and a platform,
and trying the other tier second is what makes a misplaced target a
tree-membership error rather than an unresolved name. And every value in the
tree must satisfy `ir.IsConst`, the test `const(…)` uses: the directive is read
once, before the program runs, so a `var` read would compile to a snapshot of
whatever it held first, while a `const func` call is a build-time value
and passes. No walk says so: `output`, each language node and `cache.inputs`
declare `const` slots and their members `const` props, so it is the general
const-argument and const-slot rules (see *The const prefix*). `entry` is the
exception, naming a declaration rather than holding a value.

**A platform node may declare `@run`, which wraps the process start** --
`gtk4(@run(args, run) { … })`. A handler is not a value, so the const rule
does not reach it, and its body is program code: `enterFuncBody` resets
`outputDepth`, or a call in it resolved as a target at the next level down.
The event is what says a platform supports it: gtk4 and fyne declare it, and
on html or android it is an event the node does not declare. The build
(`internal/build/run.go`) makes the handler a function of the program's,
`ir.Package.Run`, so every lowering pass reads it as it reads any other --
a handler reached only through `ir.Output` is in no body. Each host's `main`
builds the tree first, its first settle included, so a write in the handler
updates widgets that exist; then calls the function with the command line and
a `run` that presents the window and runs the loop. A handler that never calls
it gets the loop with no window presented (`RunWindows(false)` in
`gtk4rt` and `fynelayout`), which is how a status bar launched by i3 starts.
The application tears down before `run` returns, and a second `run` is
refused (design/ownership.md, *The application ends before the process
does*; `golang.EmitAppStart` writes both hosts' part of `main`).
`golang.ModelCallee` spells the call, since a handler that touches no state is
emitted free. gtk4's cgo mode has no entry point to wrap and refuses it.
`testdata/platform_run_handler.txtar` is the code and
`testdata/platform_run_handler_runs.txtar` runs it.

**The package body is the application's view, and a window is a component.**
Every node at the root of the package body is `lower.AppendChild(__app, n)`
(`ir.AppParent`, written by passDeclarative), and a render slot there is
handed the application as its parent; what attaching to it means is each
platform translator's answer. On gtk4 and fyne `ui.window` is overridden by
the platform's `Toplevel` primitive (`title`, `:visible`, `@closed`, a rest
slot of `ui.node`), and attaching a Toplevel creates its window: `gtk4rt.App`
and `fynelayout.App` are what a slot is handed, and the box calls a slot makes
(`BoxAppend`/`BoxRemove`/`InsertBefore`/`SlotAnchor`, fyne's `InsertBefore`,
`Remove`, `SlotAnchor`) answer for the application when handed it. A Toplevel's
handle is the box its content is appended to, so a child and a slot inside a
window are ordinary box operations. BuildUI -- what a test and a snapshot take
-- shows the first Toplevel's content (`gtk4rt.ToplevelWindow`); `main` builds
the tree, its first settle included, and runs `RunWindows(show)`.

What makes that reach the two platforms is generic: `ui.window` is an ordinary
component, composed through its override like any other, and a builtin node
whose declaration the target overrides is composed the same way
(`composeOverriddenBuiltins` in `internal/lower/platform_extension.go`, which
repoints each node at a per-build copy of the declaration with the mark off,
since library IR is shared between builds) -- sngl:ui/nav's stack and page on
html.

**html writes its document from a Window and shows every other as a
`<dialog>`**. html overrides `ui.window` once,
with `Window(title, favicon, :visible=visible, @closed { closed() }) { boundary(@error(e) { error(e) }) { content } }`, around its
`#[gen.renders(surface)] #[intrinsic("html:window")] component Window(…) root`.
The mark is what says the primitive's nodes are surfaces (`ir.IsSurface`,
`ir.RenderedSurface`), and it is read twice. After inlining,
html's `documentsOf` writes its documents from the first Window that no `if`
over state and no `for` can take away (`documentSurface`), and writes what the
package body renders around it (`bodyAround`: nodes and render-slot calls)
into every document -- each page's, in static and route mode, and a
harness's -- so a dialog is the application's and not a page's, with a `state`
per document as each page has. Before inlining, `findSurfaces`
(`internal/lower/surfaces.go`) finds which `ui.window` will be that document,
a node being a surface when the primitive its component renders is one
(`ir.RenderedPrimitive`): a `nav.stack` in any other surface navigates in
place, so passNavigationHrefs leaves it alone, the composition leaves its nav
nodes the builtins they are, and passNavigation -- which runs on every target,
and on one holding `navigation` lowers nothing unless the package has
surfaces -- lowers exactly those. A program whose every window may be absent
has no document and is refused at its first (`window_dialog_refused.txt`).

The emitter answers a Window that is not the document, as gtk4's codegen
answers a Toplevel (`renderDialog`): a `<dialog>` with the title in its
`aria-label` and a header holding the title and a close button, which is a
window manager's close -- the `visible` write-back with false, then `@closed`
-- and the only thing that fires `@closed`: a close the program makes runs
nothing, as on gtk4. `visible` is the dialog's `open` property
(`domPropForProp`), written by the init and by the patches reactivity put in
every handler that writes it. One built at run time -- a window under an `if`
over state -- is `_snglDialog()`, whose close button runs the `__onVisible`
and `__onClosed` the attach ops assign, in that order whichever was attached
first. The document's own `visible` and `@closed` do nothing, since a page
cannot leave the screen and is told of no close, and its writes through its
`#id` are dropped (`respellWindowWrites`); its `open` and `close` write the
var `:visible` bound or its cell as on every target. The lowering knows none of
that: the document's unbound `visible` is a cell like any window's, which the
page's `state` carries and nothing reads. A page's reset strips a
dialog's margin and padding, so a page holding one gives them back.
`testdata/window_several.txtar`, `window_visible.txtar`,
`window_under_if.txtar`, `window_dialog_pages.txtar` (static and route) and
`window_dialog_nav.txtar` are the code, and
`cmd/sngl/testdata/window_dialog_runs.txt` runs it in Chromium, on fyne, gtk4
and the interpreter.

A test may now take a root component and reach its windows. The interpreter
mounts a window as a node of its component, so its `#id` holds the cell
behind `visible`, and a method called through a node's handle runs in the
instance's scope. A test's `c.box.checked` -- a select of the handle through
the instance -- is repointed at the cell as `box.checked` is
(`handleThrough`), and the JS and Go test emitters spell the root
component's vars as the instance's state.

**bubbletea and android show one window, and a `Screen` is it.** Each
overrides `ui.window` with `Screen(visible=visible, …) { content(params) }`: a
hidden screen draws nothing, while what it holds stays mounted -- its timers
tick and its effects stay -- since hiding is not destroying. Both re-render
the whole view from state, so a window under `if details` is an ordinary
conditional and needs no slot. The view is the package body (`viewStmts`), and `codegen.SoleScreen`
finds the Screen in it and the conditions it is drawn under, following a
component built at run time into its body -- which is where android's window
under a reactive `if` lands, the implicit-state wrapper being a `remember`ing
composable there. A second window is refused at the second, one in the other
branch of an `if` and one under a `for` included; both targets used to take
`wins[0]` and drop the rest without a word
(`cmd/sngl/testdata/window_second_refused.txt`). The close is the host's:
ctrl+c on bubbletea, the system back on android (a `BackHandler` inside the
Screen). It runs the Screen's `@closed`, which the override answers with
`visible = false` and then the window's own handler, and the program quits
(`tea.Quit`, `Activity.finish`) when the conditions say the window is off
screen after it (`Screen.Shown`: the conditions and the Screen's own
`visible`) -- on android the innermost body's, which is the scope a runtime
instance's composable can evaluate. `testdata/window_screen.txtar` and
`window_under_if_screen.txtar` are the code, and
`codegen/platform/bubbletea/window_close_run_test.go` drives ctrl+c through
Update and ticks a hidden window's timer. The fixture harnesses compile every
multi-window fixture in `testdata/` for these two as well, and skip one on
`codegen.OneWindowError` rather than fail it.

Two gaps composing the window there exposed are general. A library component
with no state of its own in a reactive position is spliced rather than
elected a runtime instance, since nothing emits a library body as one:
`ui.window` has methods, so passInlinePure leaves it to passNoInlineComponents,
and on bubbletea the implicit-state wrapper around it is spliced per copy,
leaving the window under the `if` with no declaration any backend writes. And
`ir.NodeInst.Site` is the node a program wrote where an override's body was
substituted for it (`ir.AttachNodeSite`, in both inliners, and in expandCall
for a library body), since the node's own AST is then the platform
package's; `ir.NodePos` reads it, and the second-window refusal is the first
diagnostic that needed it.

One divergence stays: bubbletea keeps no state of an instance's own, so the
`visible` cell of a window under `if details` is spliced into the Model and
survives the condition turning false, where every other target starts it
again.

**A window under an `if details` is a node in a render slot**: created with
the slot, destroyed with it, content and effects included. The slot's body is
lifted into a component built at run time (liftSlotBodies), so what the window
renders is an instance, and an instance's teardown destroys the instances its
own build made (`destroyBuiltInstances`) -- without that an effect in a
component the window rendered stayed mounted after the window was gone.
Nothing survives the condition turning false; hiding without destroying is
`visible`. `testdata/window_under_if.txtar` is the code and
`testdata/window_under_if_runs.txtar` runs it, including a counter
that starts again at zero the second time.

**`visible` is a two-way prop of `ui.window`, and `open`/`close` assign it.**
They are ordinary methods in the window's body (`func open() { visible = true }`), so `details.open()` writes the caller's var where `:visible=shown`
bound one and the window's own cell (passImplicitState) where nothing did; a
read `details.visible` names the same. A method called through a component's
`#id` is repointed at the clone the inliner made where it spliced the node
(`repointHandleCalls`, beside repointCellReads) -- before, `p.open()` came out
as `m.p.open()` and the method was shaken away. One no splice answered is
refused (`refuseUnsplicedHandleCalls`) once the components nothing renders are
dropped, since a test build rooted at one component leaves its siblings'
calls emitted nowhere
(`cmd/sngl/testdata/test_root_sibling_handle_call_runs.txt`) -- and the inliner now
substitutes a component's events into its hoisted methods too, which is how a
method's write of a bound prop reaches the caller. The override binds the prop
through (`Toplevel(:visible=visible)`), which works because passPropBindings
also lowers the bindings of every library body the program renders.

The host reports a window manager's close as `visible = false` and then fires
`@closed` -- renamed from `@close`, which clashed with the `close` method --
and the tree decides what exists: a handler writing `details = false`
destroys the window, one calling `open()` keeps it on screen. A close that
leaves no window on screen ends the program, unless it started with none; a
start with none on screen reports every window hidden, which is what makes a
later `open()` a change (`pkg/go/gtk4rt/toplevel_test.go` drives the rules
through `ToplevelRequestClose`). Each setter ignores the value the window
already has, since the binding's write-back sets it again. gtk4 holds the
application (`g_application_hold`) and fyne keeps a window it never shows,
because both would otherwise end when the last window goes. gtk4's inline-cgo
mode builds one window with no `@closed` and no state-driven `visible`, and
refuses the rest. On gtk4 `WAYLAND_DEBUG` is how a fixture tells a window
reached the screen (`testdata/window_open_runs.txtar`,
`window_several_runs.txtar`, `window_visible_runs.txtar`).

**A window draws a bar where it has a way back or a menu, and only then.**
`ui.window`'s `canGoBack` and `@back` are an up arrow and the system's back,
and its `menu` slot is the window's menu, each top-level entry holding its
submenu. A window whose call site binds no `canGoBack` -- the literal
`false`, its default -- and populates no menu draws as it did, so no program
written before moved (Jonathan's call): an empty slot is the empty list,
whose loop the inliners drop and whose length they read as its count
(`dropEmptyLoops`), settling an `if` its literals decide; and
passImplicitState hands an empty list straight to the window rather than
making it its wrapper's prop. Each target:

- android's Screen wraps the content in appbar.kt's `SnglAppBar`, a Material3
  Scaffold whose TopAppBar holds the favicon or the launcher icon, the title,
  the up arrow and one `SnglMenuAction` per top-level entry, opening a
  DropdownMenu of `SnglMenuEntry`s; a second BackHandler, added after the
  content and so asked first, runs `@back` while `canGoBack` holds, ahead of a
  NavHost's pop and of the close. Decided in the Screen's codegen, which sees
  the literal, as a node's `if` around the content could not: content is
  inserted once.
- gtk4 and fyne keep the entries as hidden nodes among the window's content
  (gtk4rt's and fynelayout's `MenuEntry`), which a render slot inserts and
  removes like any node; `BoxAppend`, `InsertBefore` and `Remove` tell the
  window, which builds a GMenu behind a header bar's menu button, or fyne's
  main menu. A `Back` node under `if canGoBack` is the way back: gtk4 puts its
  button in the header bar where the window has one -- made as the window is,
  since GTK warns setting a title bar on a realized window -- and over the
  content where it has none, as fyne does.
- html draws a `<header>` with the title, the menu as `ui.menubar` (a
  `<details>` per top-level entry) and an up arrow in a `sngl-back`, which
  keeps a history entry while it is there: the browser's back pops it and the
  element dispatches `back`; the arrow goes back through the history too.
- bubbletea draws a bold title line, `‹` before it while it can go back, and
  esc runs `@back`; a terminal has no place for a menu, so it draws none.

The menu is drawn as nodes and never handed a primitive as a list of
records: a prop holding a record whose handler writes what the prop reads
put the updater inside the handler it updated, and the lowering looped
(`MatchLate` overflowed). testdata/window_menu.txtar,
testdata/nav_can_go_back.txtar, cmd/sngl/testdata/window_app_bar_runs.txt.

**A selected platform that cannot be used here is refused before the check**
(`internal/build.Check`), by the platform's own `Unavailable()` -- gtk4 with
no introspection data serves no package, so every component it would
implement would otherwise be reported as one it has no implementation for
(`cmd/sngl/testdata/gtk4_unavailable.txt`).

Whether a name nothing declares is a *misspelling* is `Config.TargetsComplete`'s
answer, and only a caller holding the whole registry may claim it
(`internal/build.Check`, the fixture harness). A platform's own test harness
registers itself while its fixtures name five other targets: that is a build the
check does not have rather than a name nobody serves, and from a partial
registry the two are indistinguishable. Everyone else gets a stand-in node with
the right family and no schema, which is the tolerance `mergedOptions` returning
nil used to give per lookup.
