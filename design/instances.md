# Scoping, overrides and instances

**One name, one meaning — over the scope the binding has.** A declaration is
package-wide, so two files of one package declaring one name is an error
naming both positions, whichever file loaded first. An import binds into one
file, so two imports claiming one alias, two dot imports lifting one name, or
a declaration taking a name an import alias binds are errors within that file.
Both are `claimTopLevel` in `internal/checker/checker.go`, which measures a
declaration against `pkgDecls` and an import against `topLevel`. The one
exception is shadowing, where only one of the two is written in this package:
a declaration may shadow a dot-imported name, including a built-in.

**A `struct`, `enum`, `unit` or `component` written in a body is scoped to that
body**, and `registerBodyDecl` is where all four register. The declaration
still joins the package collection its kind lands in — `ir.Package.Structs`,
`.Enums`, `.Units`, `.Components` — because that is what a backend emits from,
and only the *name* is body-scoped: it binds through `c.declare` in the scope
`collectComponentDecls` pushed and reaches `claimTopLevel` not at all. So it is
keyed by nothing — the declaration is its identity, and two bodies each writing
`struct Local` declare two incompatible types, the rule that makes two
packages each declaring a `shape` family declare two; a body-local
`component card` likewise shadows a top-level one inside that body and is
undefined outside it. A component body needs the binding in both passes and a
scope cannot span them, so the symbols travel on `ir.Component.BodyDecls` and
`declareBodyDecls` rebinds them in pass2. A unit's *suffix* map stays
package-wide regardless: a suffix is matched on a literal, which hands it no
scope. An **override** may not be written in a body: it merges into a
declaration registered elsewhere in the package, so there is nothing
body-scoped for one to land on.

Two in *one* body is that scope's duplicate and `c.declare` says so. Two in
*different* bodies is correct and the language allows it, but the emitted
namespace is flat — so each kind is asked, in its own terms, what that costs,
and the two answers differ in kind because the collisions do.

- A **type** collides always: every backend emits a type declaration straight
  from `ir.Package.Structs` and none renamed. `passHoistBodyTypes`
  (`internal/lower/body_types.go`) is the answer — the first claimant keeps its
  spelling and the rest become `Local__second`, named for the body they were
  written in. It reserves every top-level name over the whole package first, so
  a top-level declaration wins whatever order registration put the two in, and
  a body-local type is measured against funcs and components too because Go
  gets `type Local struct` beside `func Local()`. Which body a declaration
  belongs to is `BodyOwner` on the three decls, stamped by `registerBodyDecl`.
  Every reference rides on the declaration pointer — a literal's `Def`, an
  annotation's `Decl`, a field type, the element of a `list[Local]`, the host
  spelling each backend derives from `Name` — so setting `Name` reaches all of
  them, a method's `ir.Func.Receiver` included: it is the type of the
  declaration whose member table holds the method, so Go's
  `Local__secondShout`, Kotlin's `fun Local__second.shout` and JS's
  `Local__second_shout` follow the rename rather than landing on a type that
  holds someone else's fields. It was a name written out as a string, which
  each renaming pass had to rewrite by hand. A checker error (`claimBodyType`) stood in
  the gap for as long as #198 was open, so that no program could reach the
  output while it was.
- A **component** collides only inside a recursion cycle, which is why that
  check is narrower and is still a **codegen limitation surfaced in the
  checker**. Every platform sets `InlineComponents=false`, so
  `passNoInlineComponents` substitutes a component that is not in a cycle into
  its caller with its state renamed per call site (`__instN`) and it never
  reaches a backend under its declared name. What survives is a cycle, and two
  surviving declarations of one name emit one host component twice.
  `reportBodyComponentCollisions` asks that after pass2 and of the cycles only
  (`error_component_nested_recursive_collision.sngl`), which is what lets the
  ordinary shadowing and two-bodies cases through. Renaming it the way a type
  is renamed is the remaining half of #198.

What a nested *component* body sees is the body it was written in: its sibling
declarations, and that body's props, vars and funcs. **Capture is lowered as
shared state** (#202) — the nested body reads and writes the owner's own var,
the way a nested func's method does, which is what makes a write from inside it
a write the owner sees. A synthesized prop was the alternative and could not
express the write, since a prop is not a binding.

The **accepted cost** is that two instantiations of one nested component share
the captured var rather than each getting an `__instN` copy of it, exactly as
two calls of a nested func share the model. Its own state is unaffected and
stays per instantiation. `testdata/component_nested_capture_shared.sngl` pins
that, because it is the surprising half.

**`__instN` is counted per name, across two passes.** `passInlinePure`
substitutes a platform override and `passNoInlineComponents` substitutes a user
component, and they rename that component's state into one host namespace —
but each held a counter of its own, both starting at zero, so an owner holding
one of each came out declaring two `hits__inst0`. Kotlin and Go refuse that
outright; html keyed its `state` object twice and silently kept one of the two
counters, with only an esbuild warning to say so. So the two share
`Options.names`, a `renamer` set once by `Lower` and defaulted by `namesOrOwn`
for a unit test that builds a pass's state directly. It counts per name:
`hits__inst0` and `hits__inst1` are the first two copies of a `hits` whatever
was renamed between them. One sequence for every name made each emitted name
depend on how many unrelated instances came before it, so one more anywhere
renumbered everything after it, in the program and in every golden. It also
names the `__pN` a spliced body's parameter takes where it would shadow a name
the call site's code reads (`freshenShadowingParams`): an override's
`func(on) { checked = on; change(on) }` with the call site's `@change { on = !on }` spliced in for the emit was one `on` in every target spelling a local
bare. A local of the spliced body is renamed the same way, since a call site's
`:value=got` beside `async.query`'s `match var got = r` wrote `got = got`
once spliced into a component built at run time
(`cmd/sngl/testdata/spliced_local_shadows_runs.txt`), and a call site's prop
values count among the names it reads. `testdata/inst_suffix_one_sequence.txtar` is the fixture.

A *double* suffix is not the symptom and is correct wherever it appears: html's
`timer` override is substituted by one pass and its owner's clone hoisted by the
other, so `handle__inst0__inst1` is one var renamed twice as it travels through
two owners.

**Which target shows it turns on something unrelated**, and that is worth
knowing before reading `viewReadVars`. It exempts a var only a *handler* touches
from making a component impure — such a var needs neither an updater nor a
setter, so the body may be substituted and the var hoisted. But android declares
its `Button` primitive with `onClick func()` as an ordinary prop, and
`passInlinePure` substitutes `@click` into it *while walking the override's own
body*: by the time the call site asks, the handler is a lambda sitting in `Props`
where a rendered read goes. So one source file gets two verdicts — impure on
android, pure on html — and the purity question is answered by how a target
spells a handler rather than by what the component renders. Skipping a
lambda-valued prop makes the two agree and changes no output in this repository,
which is why it is not done here: it is unpinnable as a change on its own, and
the position where it *would* matter (a stateful override under a reactive `if`)
is one where android's accidental answer is the better of the two.

**A platform override body is a body like any other** (#230): an override *is*
the body its target renders, so "the body it was written in" is well defined
there and every word above applies unchanged. Two things make it work, and both
are about *where* the override is installed. `checkPendingExtensions` checks it
on a copy of the declaration carrying the override's vars, props, body decls,
funcs and method table -- the declaration is usually another package's, which
every check sharing it reads, so it is never written -- and checks the nested
bodies while that copy is installed (`checkOverrideNestedBodies`) rather than
in pass2, which never sees it. And the owner link has to survive to lowering, so `ir.Body` carries
`BodyDecls` beside `Vars` and `Stmts`. A component embeds its `ir.Body`, an
override is an `ir.Override` holding one, and `specializeComp` swaps the body
whole — without the body decls in it
every backend emitted the captured names bare, which
`testdata/component_override_body_capture.txtar` denies on both of its targets.
`ir.BodyOwners` and `ir.CapturesEnclosingState` read the overrides too, because
the checker asks its questions with no target picked; that is what lets the
recursion-cycle report reach an override body, and a `limit` var in
`error_component_override_nested_capture_recursive.sngl` keeps the cycle from
being folded away before it is asked.

**A program's override of a declaration the program wrote is checked after that
declaration's body** (`lateOverrides`, drained by `checkBodyOnce`): an
unannotated `var count = 0` has no type until the body declaring it is read, and
checked first, from `checkPendingExtensions`, a nested body reading `count`
would see none (`component_override_body_capture.txtar`). A library's base is
checked when its package loads, so its overrides stay where they were.

A `func` written there is the third slot of the same shape. It is registered
by `collectExtensionDecls` and desugared onto the component by the same
`registerNestedMethods` an ordinary body uses, so the override's body calls its
own helper and a nested body reaches it through `lookupBodyMethod` — the route
a method takes, since a component-body func is a method on its owner rather
than a name in scope. `ir.Body` carries `Funcs` and `Methods` for it, with the
rest; without them bubbletea emitted
`func (m *outer) Bump()` against a type it never declares, which
`component_override_body_func.txtar` denies.

A method is attached by **receiver**, so the base declaration's table is where
an override's helper would otherwise land and stay — visible to the base body
and to every other target's. Each override body therefore gets its own
`maps.Clone` of that table. It starts from the base's, so a helper the
declaration wrote stays callable from an override that did not rewrite it, and
two *overrides* may each write a `func bump` without one being a redeclaration
of the other: two platform packages implementing one component must not have to
agree on their helpers' names.

What is refused is an override helper **shadowing** one the base body wrote
(`reportOverrideFuncShadows`). That is a codegen limitation surfaced in the
checker, on `reportBodyComponentCollisions`' terms rather than as a language
rule: nothing renames a component method per body, so both would be emitted
under one host identifier. The hoist-and-rename `passHoistBodyTypes` does for
types is where it lifts, and the diagnostic says so
(`error_component_override_body_func_shadows.sngl`).

`ir.BodyFuncs` is what pass2's `compOwnedFuncs` set reads, because the live
`Component.Funcs` is the base declaration's and never names the override's
helper — checked at package scope instead, it reported the
component's own vars as undefined. That set is asked of `c.pendingExtensions`
as well as `pkg.Components`: the base of a program's override of a library
component is not the program's declaration. A target package's overrides are
checked in its load and never reach pass2.

**A helper needs the base reachable by its bare name**, which an override
written through a qualified alias does not have: a component-body func is a
method, and the synthesised receiver type, `AttachMethod` and `lookupBodyMethod`
all resolve the receiver as a bare name. So `component draw.circle[…]` may
declare vars and types but not funcs, and `reportOverrideFuncUnreachableReceiver`
says so where the helper is written. Binding the bare name for the body's
duration is what supporting it would take, and that shadows a same-named
declaration of the program's own for as long as it lasts. `ir.Func.Receiver`
is the declaration now, so the IR can carry such a receiver; what is left is the
checker's attaching and lookup, which still go by the bare name
(`fn.RecvName() == comp.Name`, `AttachMethod`).

`declareEnclosingBody` is the scope half, and `checkBodyOnce` orders an owner's
body check ahead of the bodies it declared — pass1 registers a nested
declaration first, so read in package order an unannotated `var count = 0`
would have no type yet where the nested body names it. The lowering half is
`spliceNestedCaptures` (`internal/lower/nested_capture.go`): a capturing nested
body is substituted into the body that declared it *before* that body is
inlined anywhere, because `expandCall` renames the body it splices and not the
separate declaration a `NodeInst` inside it points at. Left to the main walk,
the nested body arrived after its owner was already spliced away and emitted
the captured names with nothing declaring them. Only the capturing ones move
early; a nested component that reads nothing of its owner is placed at its call
site as before.

Two shapes cannot be spliced, and both are reported rather than emitted:

- A **recursion cycle** — nothing substitutes it, so its surviving render would
  name a var belonging to an instance of its owner.
  `reportBodyComponentCapture` in the checker, beside the collision report
  above and for the same reason
  (`error_component_nested_capture_recursive.sngl`).
- A **reactive `if` or `for`**, or any `for` once the nested component declares
  state of its own — each copy there needs state of its own, which is what the
  main walk's `RuntimeInstance` election gives a *non*-capturing nested
  component, and a capturing one cannot have. Reported by
  `spliceNestedCaptures`, since reactivity is not a fact the checker holds
  (`cmd/sngl/testdata/nested_capture_in_reactive_position.txt`).

**A loop is a loop whatever it iterates.** Under any `for`, a component with
state of its own is a runtime instance (`reactiveCtx.perCopy`), and a `for`
whose body renders from state is a render slot (`collectFromFor`) -- the two
things a loop over state already got. A `const` iterable says how many copies
there are, not that they may share state. Treated otherwise, a component's
vars were hoisted once and every copy wrote the same cell; a lifetime's handle
was overwritten by the second mount, so the first schedule could never be
stopped, which a refusal stood in for (#245); and a reactive `if` inside a
const loop became a render func reading a loop variable only the host loop
bound (`undefined: p`). What still differs is only what has nothing to
reconcile: a stateless component is spliced, and a const loop whose body reads
no state renders as a plain loop -- on html, as markup html's `documentsOf`
unrolls. A window resets the context, because a loop over pages is not a
position a page's body is repeated in. A canvas under a `for` is built at run
time for the same reason -- its surface and draw routine are its owner's one
field and one method -- which `passCanvasInstances` does by synthesizing a
component around it (`testdata/canvas_under_loop.txtar`). A lifetime is state
in the same sense: a component holding an `effect` or a timer is built per copy
under a `for` (`reactiveCtx.perCopy`) and not substituted there (`isPure`),
since spliced, the copies shared one key and one handle; an override with no
var of its own that holds one -- gtk4's entry, pushing its value from an effect
-- is wrapped per call site by `passImplicitState` so that it is. A shape
with state of its own under a `for` or a reactive `if` in a canvas is
spliced per copy on every target, its cells keyed by the copy as bubbletea
keys every component's (`reactiveCtx.drawn`, `expandPerCopy`): a drawing is
spliced into its canvas's draw routine, so there is nowhere a copy could be
built at run time, and a target keeping instance state left the shape to that
and read the loop variable from a method that never bound it
(`testdata/canvas_for_shape_state.txtar`).

A loop that holds a component built at run time is a slot too, whatever it
iterates (`bodyNeedsSlot`): the slot is what keeps the list of live instances,
and outside one fyne assigned every copy to the one Model field its id named.
"Built at run time" means a component with a body of its own -- the inliner
marks a *primitive* standing in a reactive position as well, on the
declaration, and counting that made every loop of html elements a slot
(`testdata/const_loop_beside_reactive_loop.txtar`). The slot keeps every
instance its body builds, one under a node of the body as well as one at its
top (`declarativeState.nestedInstance`): a tab's panel under the tabs of an
`if` was built afresh by each render and never destroyed
(`cmd/sngl/testdata/slot_nested_instance_runs.txt`). One under a node is
kept across renders as one at the top is, by position, and appended to the
node built afresh around it. Built afresh itself, a query in it asked again
on every render, so a page whose answers wrote what its slot renders asked
for every name again with each answer and never finished
(`cmd/sngl/testdata/slot_nested_query_kept_runs.txt`).

**A render asked for while it runs runs after it** (`renderOnce`): a render
builds its instances, a query's mount runs in the constructor, and a fetch
that answers at once runs the call site's `@load` there. One writing what the
slot renders asked for the render it was inside; re-entered, that render built
the instance afresh, since the list it reuses from is set at its end, until
the stack overflowed (`cmd/sngl/testdata/slot_render_reentered_runs.txt`).
A reused instance is handed its function props first: each setter settles
it, and a key written first mounted with the handler the last render handed
over, so an `@load` reading its loop variable wrote under the last key
(`cmd/sngl/testdata/instance_reuse_reads_loop_var_runs.txt`).

**A slot re-renders where it was written.** html renders each into a
`display:contents` wrapper of its own; fyne and gtk4 render into the container
the slot sits in, so each slot keeps a hidden anchor there
(`codegen.SlotAnchorField`), added by its first render -- which runs while the
container is built, at the slot's position -- and inserts its entries before
it. Appending instead moved a re-rendered slot after every sibling below it
(`testdata/render_slot_in_place.txtar`); gtk4's cgo mode does the same through
two preamble helpers (`gtk4.TestCgoSlotReRendersInPlace`). A gtk4
record also holds a reference on its root, since the slot holding it removes
it before appending it again and GTK frees a widget its parent held alone. In
a table part -- a `tbody`, a `tr` -- html writes the anchor as a `template`,
which the parser leaves where it is written where it moves a span out before
the table, and the boot puts the span in its place
(`cmd/sngl/testdata/table_rows_runs.txt`).

**A slot re-renders on what its structure reads**, the `if`'s condition and
the `for`'s iterable, and what its body reads beyond that is patched where it
stands. A call there reads what its callee's `Func.Reads` says and what every
call in the callee's body reads, a lambda's included; a splice adds to a
method's `Reads` the vars it substitutes for the props the method reads
(`withSubstitutedReads`), or a `for var n = rest()` over a prop the caller
hands its var re-rendered on nothing
(`cmd/sngl/testdata/slot_method_reads_prop_runs.txt`). A re-render builds the body afresh, so a slot that re-fired on
everything its body read rebuilt a whole panel under `if details` every time
one label in it changed, and lost what its widgets held that no state
described -- focus, a selection, typed text. So on the three targets that
build instances at run time, `passSlotChildInstances` lifts each top-level
node of a slot body that reads state into a synthesized component
(`liftSlotBodies`, `internal/lower/slot_body_instances.go`): what the subtree
reads becomes a prop, read at the site as the subtree wrote it (`liftValues`) -- a
synthesized var such as the context lowering's `__ctx_locale` stays one, which
html binds on the page rather than on `state` -- and the slot keeps the instances across renders as it
keeps any other. passReactivity's `registerSlotBodyDeps` then subscribes such
an instance's props to the vars they read rather than the slot to them, and a
write hands every live instance at the site the new value through its setter
(`liveUpdaters`, a loop over the site's registry). A prop reading something
the slot binds -- a loop variable, a local -- has no one value for every copy
and still re-fires the slot, as does a `#[construct]` prop and anything else
the body renders from; a node reading nothing is left to the rebuild, which
only a structural change triggers. What a function literal in the subtree
binds -- its parameters, its own loops and locals -- goes with it and is no
prop (`handlerParams`): `options.map(func(o) => o.label)` in a looped component
and `rows.map(func(r) => …)` in a tab's panel each made the parameter one,
handed a name the site never declared
(`testdata/slot_child_lambda_param.txtar`,
`cmd/sngl/testdata/slot_child_literal_runs.txt`). A node is not lifted when its subtree holds
a slot insertion, a boundary, a canvas or a window, or declares a handle
something reads. bubbletea and android re-render the view anyway and are
untouched (`testdata/slot_body_updates_in_place.txtar`,
`cmd/sngl/testdata/slot_body_updates_in_place_runs.txt`).

**A prop's setter reports a write to an effect keyed on the prop.** passEffect
marks an effect's `@update` written after each write to a var its key reads,
and a prop of a component built at run time is a parameter then, which
nothing writes; `passComponentProps` promotes it to a cell whose setter is its
one write. The effect lowering records each report whose key reads a prop
(`ir.Package.PropReports`), and `settleOnSet` has the setter mark it and
settle. Where the prop's type compares, only a new value marks it, which is
what the interpreter does with a key reading something no write is logged
against. Before this, fyne's setter settled with nothing marked and html's did
not settle, so such an `@update` never ran on a target. An owned derived value
reading a prop is one (`testdata/effect_update_on_prop.txtar`). android has
no setter: a composable is called again with the new value. So it keeps the
value it last saw beside a `DisposableEffect` keyed on the prop, which marks
and settles when the two differ (`emitPropReports`); the first value is the
one the brackets began with and reports nothing. Without it the `@update`
never ran there either, and an owned derived value kept the first value it
made (`testdata/owned_derived_built_once_on_entry.txtar`'s
`testRebuiltOnAWriteWhileShown`).

**An `@update` is owed only the writes made while its bracket ran**, as the
interpreter has it: a bracket mounted after a write starts reading the log
past it. A target's dirty flag outlived the position, so a write while an
`if` was hidden and the branch taken back in the same settle ran the
`@update` of a bracket just begun, and on bubbletea, where the enter function
had just built an owned derived value, built it a second time. A report under
an `if` remembers whether its position described the bracket at the last pass
(`__effectN_absent`) and runs nothing for one it describes only now; the dirty
flag is cleared either way. It starts false: an instance whose only brackets are `@update`s is not settled when it is built, so starting true missed a write made straight after (`effect_update_runs.txt`'s `testGated`). A bracket under a loop stepping into its key keeps its per-copy
queue. One under a loop that does not keeps each copy's key at the last pass
by the copy (`__effectN_keys`, `keysReportBlock`): a copy the last pass
described runs its `@update` on a write to what the key reads, and a copy
entered since was made with its key and is owed nothing. A key naming the
loop's variable reads the list the loop walks, so a write replacing the list
sets a second flag (`__effectN_rekey`, `rekeyBy`) on which only a copy whose
key differs runs: a prop reports only a new value, and after the per-copy
splice the loop variable is the prop
(`testdata/canvas_for_shape_state.txtar`, where replacing the list rekeys
one copy and releases one value). Before this a write to the list reached no
copy at all, and a dirty flag ran every copy.

**A two-way prop the caller leaves unbound is state of the instance**
(`ir.UnboundProps`): a cell that starts from what the call site gave the prop,
or its default, or its type's zero, and that the host's reports write, as if
the component had declared a `var` for it. A one-way value on a two-way prop
is *only* where the cell starts (Jonathan's call): after
`checkbox(checked=done)`, a later write to `done` does not reach the box, and
`:checked=done` is the spelling for one that should. So a checkbox nobody binds
keeps what it is clicked to, which before this reported its clicks into nothing.

`passImplicitState` (`internal/lower/implicit_state.go`, always on) says it in
the inliner's vocabulary: it wraps each such node in a component written for
that call site, `__checkbox_state0`, whose `var` is the cell and whose body is
the node bound to it with `:checked=`. The inliner then does what it does with
any component with a var -- splices it once where it stands, builds it at run
time in a reactive position, keeps a cell per copy under a `for` on bubbletea
-- and no backend is told. One wrapper per call site rather than per
declaration, because the node's `#id` moves inside with it: the handle is what
a test drives the widget through. A read of the prop off the `#id`
(`box.checked`) names the cell, and since it sits outside the wrapper the
inliner repoints it at the one copy it spliced (`repointCellReads`); a read of
one built at run time, or rendered more than once, cannot say which copy it
means and is refused with a position, on the terms `uniqueNodeIDs` holds a
handle to. That is also why `box.checked` beside an input under a reactive `if`
is refused: the wrapper there is an instance of its own. Bare children stay at
the call site and reach the node through the wrapper's own rest slot. A
two-way prop the call site did bind is the wrapper's own two-way prop, bound to
what the call site bound and through to the node, as an override binds its
base's prop through to a primitive -- `async.query(:value=v)` keeps `pending`
and `failure` and hands `value` on. A generic node's wrapper declares
everything at the types its call site bound (`ir.ComponentBindings`,
`ir.InstantiateExpr` for a default), and is a member of the family it was
bound, or, for a node whose family is a type parameter the call site leaves
to the position -- `async.query`'s `F` -- of the family that position takes
(`wrapperFamily`): with none, a wrapper built at run time holding the query's
effect was a root no pass could type
(`cmd/sngl/testdata/async_query_under_if_in_built_instance_runs.txt`). One shape is refused, which no `sngl:ui`
component reaches: a call site populating a named slot the node does not
declare.

A `const` component keeps no state, so a two-way prop of one left unbound is a
checker error at the call site (`refuseUnboundConstProps`); a one-way value is
no answer, since all it says is where the cell starts. A two-way prop cannot
itself be `const` (`checkConstProp`), so the callee is the only question. The
interpreter keeps the cell natively, on the prop's symbol in the instance's
cached env: `bindProps` binds it on the first mount only, a node's prop shows
the cell, and the override's handler writes it as it would write a bound one.
The interpreter also answers a `#id` read in a view now (`bindHandle`), which it
never did for any prop. `testdata/unbound_prop_state.txtar`,
`cmd/sngl/testdata/unbound_prop_state_runs.txt` and
`testdata/error_unbound_prop_const.sngl` are the three halves.

**A test reaches a `#id` inside a component built at run time**, which the
typed-text half of `slot_body_updates_in_place_runs.txt` needs: on html an
element a program named and built by code carries `data-sngl-id` and the
invoker looks it up when called (`runtimeHandlers`); on fyne and gtk4 the
instance's ctor hands the widget to a Model field `__named_<id>` and the
invoker goes through that, so it reaches whichever instance built it last. A
handler emitted as a closure records the component event it serves as well as
a promoted one (`FuncLowering.ComponentEvent`), or a gtk4 invoker was named for
the GTK signal. That is the first event the override's handler emits that is
not a binding's write-back, which passPropBindings marks on the handler it
synthesizes (`ir.EventHandler.WriteBack`) -- `Synthesized` is no answer, since
the implicit-state wrapper's forwarding `@change` is synthesized too. html's snapshot writes each control's text and checkedness
onto a copy of the document, since `outerHTML` carries neither.

Giving every unbound input a write-back put a binding where none had been,
and five gaps it had hidden came with it. fyne's `Select.SetSelected` fires
`OnChanged` for the value it already holds, so a re-sync from inside the
callback recursed; a `Setter` names the Go field holding the current value
(`current`) and the write is skipped when it would change nothing. A handler
`passDeclarative` promoted from a branch the post-lowering fold then decided
away -- `listItem`'s checkbox under `if task == Task.none`, whose operands meet
as constants only after the splice -- named a widget nobody built; the shake
drops a promoted handler nothing names (`dropDetachedHandlers`), from every
component's funcs as well as the package's, since a root component keeps its
own. A handler lifted out of a slot body relays its event whole, which on
gtk4 is the widget its signal fired on (see *An event's parameters are its
declaration's*). The shake roots the types an instance's events and a
closure's parameters name. And the test invokers:
fyne's commit sets the entry's text quietly first, as gtk4's does, and
bubbletea's writes the binding back before the handler, as Update does.

**A target that keeps no state of an instance's own splices it instead**, and
that is `Features.InstanceState`, a capability every platform but bubbletea
declares: a record on fyne and gtk4, a factory closure on html, `remember` on
android. bubbletea's model keeps every cell in itself, and a component built at
run time there is a render function with nowhere to put one. So
`passNoInlineComponents` splices a component that has state, holds a lifetime
or declares a function where it is written (`expandPerCopy`) -- a render
function's methods would be the Model's, which cannot read the props it is
handed -- and under a `for` gives each of its vars one cell per copy
(`perCopyCells`): a map keyed by the loops' indices (`lower.CopyKey`), read as
`cell.get(key, init)` and written through a temporary stored back. On every
target a function a splice hoists out of loops takes the loop variables it
reads as parameters (`passLoopVars`), since a prop bound to one is substituted
into it as that variable, and one taken as a value is a lambda passing them
(`testdata/component_func_reads_loop_prop.txtar`). An effect reading a
copy's cells is keyed on the copy (`keyEffectsByCopy`), and its handlers are
handed the key; one keyed `on` something already -- `async.query` in a card
per row -- is keyed on the pair `{copy, on}`, and each handler reads both
halves into locals where it starts, since a mount that waits writes its cells
after the wait, when the settle may have handed a later copy's mount another
key (`testdata/async_query_loop.sngl`,
`cmd/sngl/testdata/async_query_bubbletea_runs.txt`). A loop variable a handler
reads -- a call site's `@load(got) { seen[w] = got }` under `for var w` -- is
bound where it starts, by walking the loop's iterable to the index the key
holds (`loopVarsFrom`); the handler runs from the settle, outside the loop, and
the read was refused
(`cmd/sngl/testdata/instance_reuse_reads_loop_var_runs.txt`). A timer under a loop is then a schedule per copy, which
bubbletea keeps keyed the same way and routes through Update
(`codegen.CollectLoopTimers`, `bubbletea/timers.go`); it used to be
collected by nobody and never ran. A timer under no loop is the same
schedule with one copy, keyed `""`: sync runs after every Update and asks the
gate -- the timer's `enabled` ANDed with every `if` around it -- as an
expression. It used to be armed from Init and gated by a Model field only when
the gate was a bare state read, so a timer under an `if` ran while the branch
was off, and one a gate did stop was never armed again
(`bubbletea/timer_gate_run_test.go`). A recursion is never spliced, so state
inside one -- the recursive component's own vars or lifetime, a stateful
component written in its body, or a canvas it renders -- has nowhere to live
there and is refused with a position, and so is a function of the recursive
component that reads its props, which would be the Model's
(`refuseStateInCycles`, `refuseCanvasInCycles`,
`cmd/sngl/testdata/bubbletea_recursive_state_refused.txt`,
`bubbletea_recursive_func_refused.txt`);
it used to reach the Model as a field nothing declared, or the view as an
empty string.

**A spliced instance starts again each time its position is taken**, as one a
target builds there does and as the interpreter's `evictUnreached` has it.
Spliced, its vars are its owner's and were set once, in `New()`, so on
bubbletea a counter at 1 when its `if` left was 1 when the branch came back,
and an owned var released at the leave was released again at the next one.
`restartOnEntry` writes the instance an `effect` marked `ir.NodeInst.Enter`
whose `@mount` assigns each var its initializer, and declares the var from
its type's zero instead, since an initializer run twice makes an owned value
twice. Under loops it is keyed on the copy, and the copy's cells are written
through. passEffect lowers it to one function (`lowerEnter`) that rebuilds the
position from its frames and runs the assignments inside it, for each key it
did not describe at the last pass, where a loop's variables are in scope: a
prop bound to one, `child(n=x)`, is read where the copy is. It runs ahead of
every other bracket of the scope, so the owned-state effect, a derived
value's key and a user's `@mount` all read the fresh values. Its writes are
neither marked nor settled after: the settle that ran it is already running,
and a mark would report a write to a lifetime just beginning. A `for` copy
needed this as much as a branch: its cells are a map nothing removes from, so
a row that left and came back kept its state. A component that renders
nothing is spliced under a reactive `if` on every target, not only bubbletea,
and kept its state on all of them; it gets the same effect. A target that
brackets lifetimes itself (android) never runs the settle `lowerEnter` builds,
so there the effect stays a native bracket, a `DisposableEffect` whose
`@mount` assigns the initializers; spliced ahead of the instance's own
brackets, it runs before them, as Compose runs a composition's effects in
order. Without it android counted the mounts, as bubbletea had
(`testdata/bubbletea_state_leaves_with_branch.txtar`,
`testdata/renders_nothing_state_leaves_with_branch.txtar`).

**The interpreter mounts a `for` copy or a scoped population in a snapshot
of the scope around it** (`forStmt`, `slotInst`, `entryInst`), where the loop's
variables and the population's parameters are bound. What the mount writes to
a binding that scope holds is carried back once the copy has mounted
(`writeBackMountCopies`), as a handler's writes are when it returns
(`runHandler`), and the next copy's snapshot is taken after that. Nothing
carried it before: a var initializer counting in a package var left the count
at 0, and every copy got the first value. A copy's own state is its
component's scope, cached by the copy's path, which no snapshot holds, so it
stays per copy (`testdata/for_copy_initializer_writes_package.sngl`).

**A bubbles model a loop renders is one per copy** (`bubbletea/loopwidget.go`):
a map keyed as a cell of the copy is, each model built the first time View
renders it and set from the copy's cell -- read off the write-back handler's
`cell.get` -- whenever the two differ. Update walks the loop as View counts it
to the copy the slot's cursor names, hands it the message, and runs its
write-back, `@input` and `@change` there with the loop's variables bound; the
focus sync focuses that copy and blurs the rest. It used to be one model field
for every copy: each row showed and edited one text, the write-back wrote
whichever var the first handler assigned, and a handler reading the loop
variable did not compile (`bubbletea/loop_widget_run_test.go`).

**A loop's focus stops are the focusable nodes it renders**, not its
iterations. bubbletea's `passFocusOrder` makes the outermost `for` holding one
a single slot whose cursor is an ordinal over those nodes, nested loops and
taken branches included: the view counts them as it renders (`__focusPosN`),
`__focusLoopN_len` counts them for Tab, and Update's case for a key walks the
loop the same way and runs the handler of the node the cursor names
(`bubbletea/focus.go`). So a component that renders one, in a reactive
position or under a `for`, is spliced rather than built at run time, as one with
state is (`rendersFocusStop`, `testdata/bubbletea_focus_component_under_for.txtar`).
The cursor was the iteration index, so two buttons in
one iteration -- a spliced card and the child handed to it -- were one stop
with two `case` arms, and a nested loop's buttons were unreachable
(`bubbletea/focus_run_test.go`). Content handed to a recursion is rendered
once per level but is **one stop**, counted where the content is written: the
slot func numbers its stops from where it was declared, so every copy is
marked together and Enter runs the handler as written. That handler runs where
no slot argument is bound, so a focusable node there whose handler, or a
branch around it, reads one is refused (`refuseFocusReadingSlotArgs`,
`bubbletea/focus_recursion_run_test.go`,
`cmd/sngl/testdata/bubbletea_focus_reads_slot_argument.txt`). A canvas under a loop is the same shape: its
drawing reads the iteration's variables, so it is written inline as the
rasteriser View hands tui rather than as a `_canvasDrawN` method, with a
surface and a kitty image ID per copy, and the transmit reaches each copy
through the same walk (`bubbletea/viewwalk.go`,
`testdata/bubbletea_canvas_in_loop.txtar`).

**An instance reaches the page through what holds it.** A fyne or gtk4 record
holds its Model (`__model`), and a name its component does not declare -- the
page's state, widgets and funcs -- is spelled through it
(`ExprCtx.OuterReceiver`, `golang.PageNodes`); spelled through the record, it
named a field no record has. On android a composable other than MainScreen
cannot see MainScreen's `remember`ed locals, so page state such a composable
reads is declared at file level (`sharedPageState`). So is a package function
it calls, with every function that one calls and the state they reach
(`composedCallees`): a call is no read by name, and `var fd = open()` called
a local fun of MainScreen (`testdata/android_instance_calls_page_func_app.txtar`),
or in a test build a member through a `state` the file declared only for a
composable naming page state itself
(`testdata/android_instance_calls_page_func.txtar`). In a test build the
page's state is a class, and a function reading it is a member of the class
(`stateScopedFuncs`); what the function binds itself is no read of the
page's, or `display(name string)` beside a page's `name` became a member a
composable built on its own called through a `state` it does not have
(`cmd/sngl/testdata/pure_func_param_named_like_state_runs.txt`), and a write
is respelled `state.<name>` only for a var the package or a component
declares, or a function's local named like the page's var was written as the
page's (`kotlin_local_named_like_state_runs.txt`). The class
is written through a context requiring its imports of the file
(`KtIRContext.Unrewritten`,
`cmd/sngl/testdata/i18n_test_state_class_runs.txt`). A composable built on
its own that reads the page's state reads the class the test made, which the
file holds (`lateinit var state`) and each new one replaces
(`cmd/sngl/testdata/instance_reads_page_state_runs.txt`). What is still
missing on the three mutation targets is the other direction: a write to page
state updates the nodes of the scope that wrote it and of the page, and not
those of *other* live instances reading it, so their views go stale until they
are rebuilt. A write in a component's body is given no update of another
component's nodes at all (`reachable` in passReactivity): it named them, and
the other's panel instances, through its own record, and fyne and gtk4 did not
build (`cmd/sngl/testdata/page_state_other_instance_runs.txt`).

**An html instance is a closure, and its body is laid out the way the page's
markup is.** Its component's own `func`s are closures beside its vars, called
bare with no receiver (`ExprCtx.ClosureMethods`); spelled the page's way they
were `function bump(this)`, which esbuild refuses. And each render slot whose
first render the body writes gets a `display:contents` anchor at that
position (`factorySlotAnchors`), as the page's markup gives one: rendered
into the bare parent, a re-render had nothing to insert before and moved the
slot's nodes past every sibling written after it
(`cmd/sngl/testdata/runtime_instance_html_runs.txt`).

An owner's `func` is reached too, and by a different route: a component-body
`func` is a method with `Receiver == owner.Name` rather than a name in scope,
so `lookupBodyMethod` walks the owner chain where `inferIdent` used to ask
`currentComponent` alone. The lowering needed nothing — `renameIdents` already
repoints `Call.Func`, so the owner's per-instance clone is what the spliced
body calls. `CapturesEnclosingState` counts every func for that reason, while
`declareEnclosingBody` still declares only the receiverless ones: declaring a
method by bare name would shadow it.

**A `func` written inside another function body is hoisted, not closed over,
and the checker's `nested` set says which declarations that is.** Nothing after the check asks: a hoisted func is an ordinary member of what it joined. It joins the enclosing
component, window or package — what a backend emits from — while the name it
was written under is the body's, which leaves two things to reconcile
(`internal/checker/nested_funcs.go`).

- **One declaration, whatever a body is read.** `preCheckComponentMethods`
  reads a component method's signature and `checkComponentBody` then checks it
  authoritatively, so `checkStmt` sees the same `*ast.FuncDef` twice and used
  to hoist an `ir.Func` each time: `method Model.innerF already declared` on
  bubbletea, two `fun innerF` in one android file, and on html a second
  `function innerF` that silently won. `checker.nestedFuncs` keys the
  declaration by its AST node. Every loop that checks or declares an owner's
  funcs a second time in that owner's scope skips one, because that is not the
  scope its source sits in.
- **An emitted name of its own.** Two bodies may each write `func helper` and
  mean two functions, into a flat namespace. `renameNestedFuncs` gives each
  `<body>__<name>` at the end of the check, once no scope holds a written
  name; three levels compose because it runs in declaration order. Renaming is
  available to a func because a call site holds the declaration,
  `ir.Call.Func`, and not the name. The names it
  avoids are every kind that shares the emitted namespace — structs, enums,
  units, components and funcs — because `struct step__mark` beside a `mark`
  nested in `step` is Go's `Step__mark redeclared in this block`, and a
  redeclaration parses, so `format.Source` passed it through.
  `orderNestedFuncs` then puts each ahead of the body that declared it — the
  hoist appends, and android emits an owner's funcs as local `fun`s inside one
  composable, where a local function may not be referenced above its
  declaration.

What one may **reach** follows from the hoist rather than from where it is
written. Its siblings and itself are hoisted into the same namespace, so a
sibling call and recursion are ordinary calls (`checker.nestedScope`, one per
body, chained on that body's outer scope). The enclosing function's params and
locals are not: the call that held them has returned, and nothing about a hoist
captures them. So the body is checked against the scope its enclosing function
was *entered* from, and naming one is a positioned error carrying `captureHint`
rather than a bare `undefined` — closing over them is a closure conversion and
is not what this is. Two of one name in one body is that body's duplicate, from
`c.declare` like any other binding.

A func at the root of a **window** body reaches the same code and is none of
this: it belongs to the window's container, the way a component-body func
belongs to the component, and it keeps the name it was written under. Counting it in `nested` as "hoisted" is
what renamed `examples/todo`'s `status`.

Bare component resolution is `checker.lookupComponentInScope` — the lexical
chain, like every other identifier. `ir.SymbolTable.LookupRootComponent` is
the other question: a name qualified by a package, and an override's target.

An `import` outside the root of a file is an error at the import. That is a
**policy** and not a structural impossibility: the parser still produces the
node and `checkStmt` refuses it at one site, so relaxing it JS-style — a
body-level import binding in the body's scope — is deleting that check and
wiring a scope. Which is why the diagnostic says where an import may be
written rather than that a nested one means nothing. Before the check it
parsed and was discarded, which told a program only that the *use* of its
alias was undefined.
