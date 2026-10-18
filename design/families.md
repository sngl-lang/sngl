# Families, membership and error boundaries

What a family is, and the rules of membership, are the spec's (*Trees and families* in
`docs/reference/specification.md`). This is how the compiler holds them.

**A family is a declared component, a member of `sngl:build`'s `family`.**
Drawing is the first user, rich text and menus are the next, and nothing in
the mechanism knows what a shape is. The family is the *declaration*, not its name, so a misspelling is an
unresolved name where it is written, and two packages each declaring a `shape`
family declare two. `ir.Component.Family` points at it, and `Component.IsFamily`
is "a member of the family of families", which is the one declaration that is a
member of itself (`#[marks.builtin("familyOfFamilies")]` on `build.family`, where the
regress stops). A family has no body — a body is where how a family is
generated will be written, and nothing reads one yet, so it is refused rather
than dropped — and writing one in a tree is an error rather than a membership
mismatch. It is exempt from the bodyless-component rule for the same reason.

**A family declares the props its members share**, and is a type with values.
`component suit(name string, rank int) build.family`: every member
declares each prop by name and type (`checkFamilyMember`, run once the
package's components are registered and again for a member whose family was
inferred), and a handle to a member is assignable to the family's type, one way
only (`Type.IsAssignableTo`). A value of a family is a record of those props
and the identity of the member it holds, so a select through one reaches only
the family's props and `==` against a member compares identity
(`comparableEq`); two *different* members still do not compare. A family prop
may carry a default, which must be constant: a member that omits the prop then
holds it with that default, and a call site may not set it
(`refuseInheritedProps`), since the member never said it takes one. Anything
else on a family -- a two-way or const prop, an event, a slot -- would be
read by nobody and is refused (`checkFamilyDecl`); a type parameter is the
members' to bind (see *Navigation*). Every
family has values, the propless ones included (identity alone), but naming a
family as an expression is still an error: it is a type, not one of its
values. `testdata/family_props.sngl`, `family_generic.sngl`,
`error_family_member_props.sngl`, `error_nav_surface.sngl` and
`error_family_decl.sngl`.

**A component that names itself as its family is data**
(`component choice(label string) choice`, `ir.Component.Data`): its own
family and the only member of it. Written in a slot of its type it is a record
of its props, never rendered, and the slot is never inserted: in the
container's body its name is the list of those records, `options list[choice]` for `options ...component choice`. It is how a widget that takes
its choices as a list is written with them as children --
`ui.select { ui.choice(label="Apple") … }`, `ui.radio` likewise, a
`ui.table`'s `ui.tableRow`s, a `ui.tree`'s `ui.treeItem`s and a `ui.tabs`'s
`ui.tab`s -- and the
caller's population may
hold `if`s, `for`s and components that compose records. Its name in a value
position is its record (`Component.Record`, a struct `<name>__record` declared
beside it, which `promoteForeignStructs` declares in a program that builds
one), and in a family position (a return position, a slot's content) it is the
component: `checker.dataRecord` asks `familyPosition`. `bindFamily` recognises
the self-reference, and refuses another *bodyless* component naming a data
component as its family; `checkDataComponent` refuses a block and a slot of anything but
records or content (below), since a record holds its props and nothing else. A slot
of records is a field of the record, so a record may be a tree --
`component treeItem(label string, children ...component treeItem) treeItem`
-- and each level's population is read as a container's is
(`testdata/family_data_nested.sngl`). The self-reference is read off the source
before the signature resolves, where the name finds nothing else: a
`component node(…) node` in a body that sees `ui.node` returns that.

**A component with a body that names a data family composes its records**,
and is no record itself: `component moveTo(x float, y float) _step { _step(op=PathOp.moveTo, x=x, y=y) }`. A library declaration has to say so,
since it is never inferred; a program's may leave it to the body, which
answers the same. A data family's props are its record's fields rather than
props its members share, so `checkFamilyMember` asks nothing of a composer,
which may declare some of them under its own meaning and omit the rest. That
is how a record stays private while a program writes it: `sngl:ui/draw`'s
`path` takes `steps ...component _step`, and `moveTo`, `lineTo`, `bezierTo`,
`arcTo` and `close` each compose one `_step` with only its own fields set, so
an override reads `steps` as `list[_step__record]` and switches on `op`
(`testdata/draw_path_steps.sngl`, `draw_path_steps.txtar`).

**A data component may declare content**: one rest slot of anything but
records, `component tab(label string, content ...component node) tab`, which
is what its call site writes in it and no field of the record. A container
taking such records shows it with `t.content` in its own body -- `for var k, t = items { if k == selected { t.content } }` -- and nowhere else, which
the checker reads as an insertion of a slot the container is given for the
family at registration (`ir.SlotDecl.Shows`, named by `ir.ContentSlotName`,
invoked with the record; `recordContent`). The record carries a number,
`ir.RecordID`, and `lower.DataFamilies` numbers each record written with
content and writes the container's content slot as a population of an
`if`-chain on it, a branch per record holding what that record was written
with -- the shape navigation gives a page's content under
`if pages.current.id == N`. So every target renders it as the scoped slot
population it is, built where the container inserts it and reading and
writing the caller's scope. Numbers are unique across a run, so a container
forwarding another's records hands what it cannot show on to the content slot
of the component its population was written in. A record under a `for` among
records is one record mapped over the list, so its content is written once and
may not read the loop's variable (`family_data_content_refused.txt`); and the
content slot binds nothing. `ui.tabs` is the user: a `ui.tab` per panel, the
strip drawn from the records (`testdata/family_data_content.sngl`,
`family_data_content.txtar`, `error_family_data_content.sngl`,
`component_tabs.txtar`, `cmd/sngl/testdata/tabs_runs.txt`).

**A data component's events are functions of its record**
(`ir.EventFuncType`): `component action(label string, @run()) action` is a
record with a `run func()` field, which the container calls where it means the
record was acted on -- `a.run()` in its own handler. The call site's handler
is that function, a lambda closing over the call site's scope, and one nobody
wrote does nothing (`dataFamilies.eventFunc`); a record never emits, having no
body. A write in one patches what reads it on a mutation target, since
passReactivity reaches every lambda, and a raise in one resolves against the
caller's boundaries, past any the container wraps it in
(`testdata/family_data_events.sngl`, `family_data_events.txtar`,
`cmd/sngl/testdata/family_data_events_runs.txt`). It needed four general
fixes: `codegen.WalkLowered` reaches a lambda in a `for`'s iterable, a
spread, a ternary and a conversion, or html's patch in one kept the node's
source name; Kotlin spells a function type as one (`(Int) -> Unit`, zero `{ _: Int -> }`) rather than `Any`; JavaScript parenthesizes a function literal it
calls where it stands; and the interpreter's lambda closing over another
call's ended snapshot reads and writes through to where that snapshot was
taken (`LambdaValue.Call`), or a closure made inside a `map` wrote nothing.

On html a loop over records a page writes into its markup unrolls with each
record's handler a constant: in a document's fold a function literal is a
value, a copy reading the values the fold bound for it (`heldLambda`), and
`list.map`/`list.filter` with one evaluate per element (`evalListLambda`), so
each copy's `it.select()` folds to a call of its own handler -- the fold
folds a call's callee too. A view loop the fold still cannot evaluate on a
static site -- a served page renders one on the server -- is refused at the loop, or at the node the program wrote where an override's
body holds it (`refuseUnrolledViewLoops`); it used to be written once with its
variable bound to nothing (`component_menubar.txtar`,
`cmd/sngl/testdata/html_static_loop_unevaluable_refused.txt`). `ui.menu` and
`ui.menubar` take `ui.menuItem` records, each with its own `@select` and a
submenu of its children; `ui.toolbar` takes widgets and nothing else. A prop's default is the record's
field's where the call site omits it. A data slot inserted anywhere but in
another data slot's population is refused (`refuseDataSlotInsertion`); there
it forwards. A population is read in every package the import closure
reaches -- an `md:` document's tables are written in the document's package --
and the rewrite is idempotent, since the library IR it touches is shared.

The slot's prop (`SlotDecl.Values`) is synthesized at registration, and the
checker writes no default for it at a call site: `lower.DataFamilies` builds
the list from the population and binds it, at the start of `optimize.Optimize`
-- which every pipeline reaches before anything could fold the prop to its
empty default -- and in `interp.Prepare`. It splices each component composing
records first (one with state is refused), then reads a record, an
`if` as a ternary of two lists spread in place, a `for` over a list as a `map`
(a `filter` first where the body is a member under an `if`), and an insertion
of the container's own data slot -- forwarding -- as that list spread. A `for`
holding more than one record would map to a list of lists, which nothing
flattens yet, and is refused. From there the list is an ordinary prop, so
reactivity, a component built at run time, the interpreter and every backend
read it unchanged (`testdata/family_data.sngl`,
`testdata/error_family_data.sngl`, `testdata/select_choices.txtar`). The
general shape is nav's -- pages read as data -- and nav is to be
reimplemented on it.

Two general fixes it needed. A lambda handed to a generic method takes an
unannotated parameter's type from what the call has bound
(`inferArgExpr`, `testdata/lambda_param_from_receiver.sngl`). And `passNoListLambdas` no longer
hoists a `map` or `filter` out of a node's prop: the Go emitter writes the call
as an expression of its own, and hoisted, fyne and gtk4 made the accumulator a
render slot appending to a list it could not see
(`testdata/list_map_in_view_body.txtar`).

**A family registers before what names it.** A component is bound only once its
own signature resolves, so a member declared above its family would name
nothing. `familiesFirst` orders pass1's component registration by the
same-package names each return position and slot type mentions — which is also
what puts `family` ahead of `language` and `platform` in `sngl:build`. Structs
get the same freedom from their shells. `resolveQualifiedType` lets a family
through where it looks for a type, since `ir.IsTypeDecl` names only structs,
enums and units.

**A family may be generated rather than rendered.** A target overrides the
family itself with a `gen.emit` as the override's whole body --
`component block[go.language] { gen.emit(file="blocks.txt", open=…, close=…) }`
-- and each bodyless member it reaches overrides itself with a `gen.node`
(`open`/`close` templates, evaluated with the member's props bound). A bodyless
*host*, a component whose rest slot takes the family (`ir.EmittedFamily`), is
then answered by the family's override in both bodyless rules. The build
(`internal/build/emit.go`, ahead of the first optimize, which would drop a
bodyless host it cannot render) walks each host where a root member stands --
at the root of a file, under an `if` there, and in a component whose family is
`root` -- into data: a member with a `gen.node` override is a node, a member with a body
composes -- its body read with its props bound, its rest slot inserting what it
was written with -- and an `if` or a `for` is decided there, so every value is a
build-time one and a read of state is refused at the read. The host is then
removed from the package, and a runner turns the tree into the target's extra
file. The walk and the runner are separate on purpose: the tree
(`emittedNode`) carries no IR a process could not be handed, so an emitter that
runs another program takes the same data. Where `gen.emit`/`gen.node` may be
written is `reportEmitterPlacement`; what a build refuses is
`cmd/sngl/testdata/emit_family_refused.txt`, and `testdata/emit_family.txtar`
is the code.

**Content that reads state is generated as SNGL the target compiles** --
code mode, chosen by a `render` on the family's `gen.emit`
(`internal/build/emitcode.go`). The walk keeps each `if` and `for` rather than
deciding it, and hands its tree to a runner as data plus handles into a table
of the expressions and handlers it was written with (`codeStmt`,
`codeTable`), so a runner that writes host code directly, or hands the tree to
another process, reads the same tree and resolves the handles its own way. The
runner here, `runSNGL`, writes one function returning the members' values --
each member's `gen.node(value=…)` with its props bound, and each of its events,
read as a value, a call of the handler the call site wrote -- and puts a clone
of the family's `gen.emit` where the host stood, handed that function as
`members`. `gen.emit` is an ordinary bodied component of `sngl:x/gen`: an
effect mounted at start that fires `@start(members)`, and one keyed on
`render(members())` that fires `@change(text)`. So the re-render is an effect
rekeyed by a write to what the members read, and nothing on either host had to
learn anything: a runner that writes SNGL is one no backend needs telling
about. The synthesized function is given its Reads and Purity by
`checker.AnalyzeSynthesizedFunc`, since the effect's settle is placed by what
its key reads. `testdata/emit_family_state.txtar` is the code and
`testdata/emit_family_state_runs.txtar` runs it on gtk4 and fyne under
the headless compositor (an `exec` directive). What is not here
yet: a host under a `for`, a composed member's handlers, and a member holding
children in code mode -- each refused with a position.

**A family may be generated by a handler** -- process mode, chosen by
handling `@generate(out, members)` on the family's `gen.emit`. The walk is
template mode's, so every value is a build-time one, and what the handler is
handed is each member's `gen.node(value=…)` with its props bound and what it
holds, as `list[gen.Member[T]]`; the annotation on the handler's parameter is
what binds `T`, by the general rule that a handler's written type binds a type
parameter its event names (`bindFromHandlers`). It runs in the interpreter
under the build host (`internal/plugin`'s `Runner.Emit`) as the package the
family's override is written in -- `ir.Override.Pkg`, nil for a target package's,
which is library source -- so `gen.exec`, which takes a `stdin`, is that
package's grant and not the program's. `out.write` takes any path inside the
output directory, and what it wrote is stored as `sngl.emit` keyed by the
members' encoded values behind the package's closure digest
(`cmd/sngl/testdata/emit_family_generate.txt`). A `render` beside a
`@generate` is refused.

**A host goes where a root member goes** (`emitFamilies`). Under an `if` a
code-mode host is rewritten where it stands, so the family's `gen.emit` -- two
effects -- sits under the same `if`, and passEffect mounts and unmounts it with
the branch: `@start` runs again each time the host comes back, and a change
while it is gone prints nothing (`testdata/emit_family_under_if_runs.txtar`).
In a root component the members function joins that component's `Funcs`, so
the inliner renames what it reads with the rest of the component
(`testdata/emit_family_under_if.txtar`). A template-mode host writes its file
at build time, so the `if` above it must be decidable then, and one in a root
component nobody renders writes nothing. A `for` is refused: each copy would
be an emitter of its own, and the members function cannot see the loop's
variables.

**`sngl:ui`'s `node` is the widget family**, and it is a family like any
other: naming it in a slot accepts widgets and nothing else, which is what
makes `vbox { circle(…) }` an error. It is named for what a member *is* — a
member of `draw.shape` is a shape, a member of this one is a node — which is
also what keeps `ui.ui` from appearing in every declaration that spells it. It used to be `tree.default`, and the default
tree was not a family at all — `checkFamilyMembership` opened with `if want == nil { return }`, so membership was enforced *into* a named tree and never *out
of* the unnamed one. `sngl:family` keeps the `kind` mark, the `none` mark and
the count wrappers; the families live where their members do.

**Naming nothing asks the compiler which family it joins**, and the body is
what answers: a declaration that renders a widget is one. The evidence is the
*root* of the body — what the component puts in the tree, not what those nodes
host — and an `if`, a `for`, a boundary and a context override are how the
nodes under them got there rather than nodes, so the walk reaches through all
four. A slot
insertion is not evidence: what a slot naming no family accepts is the family
of the component declaring it, so reading one reads the answer off the
question. Two bodies have no answer, and both are positioned errors:
one that renders members of two families, and one that renders members of
none — a cycle of declarations taking their evidence from each other being the
second case spread over several declarations.

`inferComponentFamilies` is that, and it is a **fixed point** rather than one
ordered pass, because the evidence may be a declaration whose own family is
unsettled and two declarations may be mutually recursive. It runs in two
rounds: settle everyone whose evidence is complete, repeatedly; then settle
what is left — necessarily a cycle — from the evidence that did resolve, so
`a` renders a widget and the `b` that renders an `a` is one too.

**Every membership check waits for it.** `checkFamilyMembership` opens with
`if want == nil { return }`, so a check that ran while its subject was still
unsettled would compare against no family and pass in *silence* — the same
nothing an unrestricted position reports. So the six call sites record a
closure (`deferFamilyCheck`) and `runFamilyChecks` drains them once every body has
been read, which is why the function takes the component the content was
written in rather than reading `c.currentComponent`.
One fixture per position holds that, each naming a declaration written *below*
it: `error_tree_inferred_late.sngl` for a node's bare children,
`error_tree_inferred_positions.sngl` for four more,
`error_tree_inferred_treeless_contains.sngl` for the family-parameter rule, which
reads an inferred family too, and `error_tree_component_body.sngl` for the
body. Move the matching check back inline and the fixture passes clean rather
than failing — which is how each was confirmed.

**A component's own body is the sixth position**, and it is the one nothing
else asks about: the other five are a container asking after its children,
which leaves what a declaration itself puts in the tree unmeasured. So
`component c ui.node { window … }` type-checked, and every backend then
swallowed the window *and its siblings* without a word (#214) — five golden
archives in this repository were written that way. The check is
`checkFamilyMembership` against `comp.Family` at the end of `checkComponentBody`,
which makes `output` and a component returning a type parameter exempt for
free: both carry a nil `Family`, so the first is never asked and the second is
left to `checkFamilyParamBody` rather than reported twice.

**Four constructs are reached through, and `familyTransparent` is the one list
of them.** An `if` and a `for` say when and how many; an `ir.ErrorBoundary`
says what happens when a raise reaches it; an `ir.ContextProvider` sets a value
for what is under it. None puts anything in the tree itself, so every tree
question asked of a block is asked of theirs. After the checker the same list is `ir.TransparentBlocks`, and
`ir.ViewBlocks` adds a node's and a slot insertion's own blocks: a walk over a
view tree in a later phase asks those rather than writing its switch.

It returns **two** lists. `all` is every block, for the callers that ask whether
content belongs to a family. `binds` is the blocks that may *supply* one, which
is every block but a boundary's fallback: that stands where the content stood
and is held to the content's answer rather than giving one. `childrenFamily` is
the only caller of `binds`, and handing it `all` made a diagnostic depend on
source order — `T` bound to `shape` off the fallback of a boundary whose content
was empty, and the widget beside it was blamed
(`error_tree_boundary_failed_binds_nothing.sngl` is both orderings). A boundary
with no content still takes `T` from its fallback as a last resort, in the
boundary's own check where that rule belongs.

There were **five** copies of that walk and each was missing a different
member, which is why the list is now a function rather than a `switch` per
caller:

- `checkFamilyMembership` walked `if`/`for`. A boundary is the subtle one — its
  own check binds `T` off its content and holds the rest to that, so reaching
  through it is what holds the `T` it settled on to the family the surrounding
  *position* accepts. Without it, `vbox { boundary { circle(…) } }` passed in
  silence.
- `familyEvidence` is the same question from the other side, and missed the
  provider: a component with no return position whose body was
  `theme("dark") { ui.text(…) }` rendered nothing the inference could see and
  was refused as a body that names no tree — a correct program refused.
- `checkFamilyParamBody` walked `if`/`for`, so a component of no family rendered
  a widget under either wrapper and passed.
- `childrenFamily` and `insertsSlot` each had their own partial copy.

Two of those gaps were found one at a time, each as a silent acceptance; the
third is what made the list shared rather than corrected a third time. Fixtures:
`tree_inferred_context.sngl` (inference), `error_tree_component_body.sngl`'s
`scoped` (membership) and `error_tree_treeless_wrapped.sngl` (family parameter, both
wrappers). Deleting the `ContextProvider` case from `familyTransparent` fails all
three, which is the unification doing its job.

A **sixth** copy was `internal/checker/effects.go`'s `walkVisualErrors`, which
resolves a raise to the nearest boundary. It had no provider case at all, so a
handler written under one resolved past every boundary around it and the error
came out uncaught — `sngl test` reporting `raised: boom` for a program that
wrote a boundary. It keeps its own boundary case, because a boundary is the one
transparent statement that is not: it pushes its handler onto the scope, which
is the whole of what it does. Its fallback is walked under that same handler,
since `passBoundaryFailed` puts the fallback exactly where the content was.
`cmd/sngl/testdata/error_under_wrappers.txt` covers both halves on the interpreter. A `testdata/*.sngl` fixture's test functions are run as well, on the interpreter only, by `TestRunFixtures` (`internal/interp/testrunner`): every fixture that declares one is executed unless it asserts a failure before the run.

Two of those deferrals are subtler than the rest. The family-parameter check is
handed the component it was asked about, which for an override is the copy of
its declaration the override was checked on (`checkPendingExtensions`): the
declaration itself never holds an override's body. And a library package
drains its own, in the `finishDeclarations` its load runs -- left for the
program's pass2, nothing checked a library body's membership when no program
loaded it: `sngl doc`, the LSP's lib path and `sngl check sngl:platform/fyne`
alike.

A **library** declaration names its family and is not inferred. Most of
`lib/` is bodyless, so there is nothing to read an answer off -- and a
package's declarations are a published contract, which a body should not be
quietly restating.

**A type parameter in the return position takes the family a component is
placed in** -- `component effect[T any, F any](…) F` -- which is what a component that
renders nothing wants: `effect`, `timer`, `context`, `gen.emit`, `gen.node`,
and each platform's `Timer` primitive. Nothing binds `F` at a call site with
no children, so such a node has a nil `Family` and `ir.IsFamilyGenericNode` is
the question a pass asks of it. `#[tree.none]` said this until the generic form
could, and the generic form could only once its body was held to `F`: before,
`component sneak[F any](…) F { ui.text(…) }` placed in a canvas checked clean.

**A canvas keeps them and draws the rest.** `passShapeDraw` replaces a
canvas's shape children with the statements that paint them, in place, and
`emitShape` passes a family-generic node through as a child rather than painting it:
it paints nothing, and its lifetime is the canvas's, so `passEffect` finds it
there as it would under a vbox. Under `emitShapes` an `if` and a `for` are
rebuilt around what they held, so a bracket under one survives too — which is
where a `timer` lands, its override being `if enabled { effect(…) }` by then.
The pass it replaced hoisted the shapes into a function and cleared `Children`,
so the bracket went with them and a canvas that scheduled its own animation
compiled clean and never moved. `testdata/canvas_schedules_itself.txtar` holds
it.

Two rules follow from the type parameter, and they are each other's halves:

- it may be placed in **any** tree, so a lifetime bracket belongs in a drawing
  as readily as in a layout (`checkFamilyMembership`);
- its body may render only what is generic in `F` the same way -- another
  such component, or a slot typed `F` -- because a body that rendered a widget
  would have joined that family without saying so, and would then be
  placeable in a canvas (`checkFamilyParamBody`). The rule reaches through
  everything `familyTransparent` lists, so a widget wrapped in a boundary or a
  context override is still a widget this component renders -- and it counts
  a slot insertion, which puts what the slot takes where it stands. Passed
  over, `component sneak[F any](content ...component ui.node) F { content }`
  carried a button into a canvas, where html painted nothing for it
  (`error_tree_treeless_slot.sngl`).

**Crossing a family is said at the
placement, with `#[family.crosses]`** (`sngl:family`, for any package): written on
a node or a slot insertion in a view, it lifts the one check the position
makes of it -- that it belongs to the family accepted there -- and nothing
else. The node's own body, slots and children are checked as ever, it is no
evidence for a family the body is inferring (`familyEvidence`), and on anything
else the mark is refused (`markFamilyCrosses`). On the placement rather than the
declaration, so a platform permits exactly the use it means: html's window
override marks the `html.dialog` it renders in a `root` member's body, and
each `nav.page` override marks the `Page` primitive it stands in, which is a
`ui.node` and says so. It is `NodeInst.Crosses` and `SlotInst.Crosses`, and a
node written as a call (`ui.text(value=v)`, an `ast.CallStmt`) carries marks
as a `VisualNode` does. `testdata/tree_crosses.sngl` and
`error_tree_crosses_target.sngl`.

**A wrapper whose family is whatever it was handed says so with a type
parameter** — `component boundary[T any](@error error, content ...component T, failed component T) T`. Nothing at a call site names a type argument and nothing
needs to: a slot's content *is* an argument, so the children bind `T` — the
first one that belongs to a family says which, and the rest are held to it
(`slotWant`, `childrenFamily`). An empty body binds nothing and leaves `T`
unbound: there is no content for a binding to have checked, and a default
would name a family the wrapper has no reason to prefer. `failed` is held to
the *content's* answer rather than to its own, because it stands where the
content stood: a boundary around widgets cannot fall back to a shape.

**A boundary has two halves and one of them is required.** `@error` reports,
`failed` replaces; a boundary declaring neither catches an error and drops it,
which reads as handling something. Which slot the fallback is comes from the
declaration's *shape* — the marked component declares one rest slot for the
content and one named slot for the fallback — so nothing in Go spells
`failed`, and `ir.ErrorBoundary.FailedSlot` carries the name back for
`ir.Convert` to print. A fallback with no handler gets an empty one **in the
checker**, not in the lowering: `analyzeErrors` resolves every raise to the
nearest boundary that has one and runs at the end of the check, so a handler
synthesized later left the raise resolved past the boundary.

`passBoundaryFailed` is the meaning: a flag on the owner, set as the handler's
first statement, and `if __failedN { FALLBACK } else { CONTENT }` in place of
the content. Both are shapes every backend already emits, so no platform
emitter grew a case — the trade `passForElse` makes. It runs early, so the `if`
reaches `passReactivity` as an ordinary view conditional and the flag makes it
a render slot. The flag is marked `Synthesized` **on the Var and on every
reference**, and the two must agree: html reads the first to write a top-level
binding and the second to spell a bare name, where an unsynthesized var is a
field of `state`; marked on one half only, the page declared one binding and
read another, falsy by accident. bubbletea says it from the other side —
`__failed0` title-cases to itself, so the accessor it skips for a synthesized
field would have collided with the field.

Where the `if` lands is the boundary's parent node, so reactivity reaches
through a boundary when it asks whether a node needs an id to render a slot
into (`childrenContainReactiveSlot`). And once a view is flattened into
statements the boundary around them holds nothing — a raise reaches its
handler through `Call.ResolvedHandler` — so `codegen.WalkLowered` returns its
children (`cmd/sngl/testdata/boundary_in_render_slot_runs.txt`).

**The interpreter answers it itself**, in `Env.caught`. `sngl test` on the
`none` platform runs the *checked* IR with no lowering at all — the same reason
the interpreter honours `For.Else` itself — so the pass is not in that path.
The map is keyed by the boundary's `@error` handler, which is what a raise
reaches through `Call.ResolvedHandler`, and held per scope so two
instantiations of one component catch separately.

**A raise unwinds to the handler that resolved it, and what that handler
answers for decides what runs after it.** Inside a callee a raise is the host's
native throw — a panic of `ErrorEvent` on Go, `SnglRaise` on Kotlin, an `Error`
with a `kind` on JavaScript — and it passes through every fallible function
between with no signature change. Where it stops has two shapes:

- **A call's own `@error`** (`ErrorPerCall`) answers for that call alone: the
  handler runs and the statement after the call runs next. Each language's
  `catchAtCall` runs the call under a recover or a try and inlines the handler
  there, rethrowing anything that is not a raise; a `fails` native reports
  through its error result on Go, which gets an `if err` instead, and through
  any exception on JavaScript and Kotlin, which is caught whole
  (`call_error_handler_catches_raises_only.txtar`). The handler does not
  answer for the call's arguments, which are evaluated first, so
  `passErrorCatch` binds an argument that may raise to a temp ahead of the
  statement.
- **A boundary's or window's `@error`** (`ErrorInvokeAndTerminate`) ends the
  event handler that made the call, as an exception would: `passErrorCatch`
  makes the whole handler body one catch block (`ir.If` with `Catch` set and
  the literal `true` for a condition, so every analysis that reads an `if`'s
  body still reads it), and nothing after the raise runs — in the function
  that raised, in a caller between, or in the handler. Each language renders
  the block through `irwalk`'s `Catch` hook. A `fails` native the block covers
  is made to raise (`raiseFailure`: its error result panics on Go, its exception
  is rethrown as a raise elsewhere); caught at its own call instead, the handler
  ran and the click went on, into whatever raised next
  (`error_catch_fails_native.txtar`). A catch block in the half
  `passAsyncOffload` spawned recovers off the drawing thread, so its handler is
  posted back through `async.do` (`error_catch_async_offload.txtar`). The interpreter's `dispatchRaise` returns
  a marked `returnSignal`, which `invokeHandler` and an emitted event's
  handler pass on, so a raise caught from inside either ends the handler it
  was run from too.

A `return` in a handler ends the handler, so a body holding one runs as a
function of its own rather than a block of the function it was inlined into
(`error_handler_return.txtar`), the catch clause of a block included
(`error_catch_handler_return.txtar`). A handler rendered somewhere other than where
it is declared — inlined by `catchAtCall`, or the `Catch` of a block — is not
reached by `ir.Walk` there (`ResolvedHandler` and `If.Catch` are aliases), so
whatever asks what a block contains has to ask of it too: `codegen.WalkLowered`,
or a widget write in a window's `@error` reaches fyne and gtk4 untranslated
(`window_error_handler_updates_view.txtar`), and `codegen.PackageStateFuncs`,
or a mount the handler is inlined into is emitted as a free func writing the
Model (`effect_mount_caught_by_window.txtar`).

**Which handler is the render tree's answer, not the declaration's.** The
checker resolves a raise inside the component it was written in, and one that
reaches no boundary there is left native. A compiled target settles it in
`passErrorScope`, right after `passNoInlineComponents`: every instance has been
spliced into the tree that renders it by then, each splice its own clone, so
the boundaries and windows around each instance are there to walk and one
declaration rendered under two boundaries answers to both. A handler is read
wherever the node carries it — in `Handlers`, or as a lambda in `Props`, which
is where android's override substitution has put it. A component built at run
time is not spliced, so its body is shared by every instance; one of those
under a boundary, whose body lets a raise out, is refused with a position
rather than emitted with the raise going nowhere. The interpreter answers at
run time instead: the mounter pushes each boundary's and window's handler onto
a frame list it keeps among the context values a node is mounted under
(`raiseScope`), and `underHandler` -- the outermost event handler's entry, not a
lambda or an emitted event's -- offers a raise that left it to those frames,
starting outside the handler that let it out
(`error_raise_render_tree_runs.txt`, `error_raise_render_tree.txtar`,
`internal/interp/raise_scope_test.go`).

**Slot content is the exception.** A handler written in the caller and
rendered in a callee's slot is resolved by the checker against the *caller's*
boundaries, so where the caller has one it wins over a nearer boundary the
callee wraps the slot in; only where the caller has none does the render tree
answer.

A call in **expression position** that carries its own `@error`
(`v = risky(7, @error(e) { … })`) is refused (`refuseExprErrorHandler`): the
handler would replace the rest of the statement, so the call has nothing to
evaluate to. And a call through a **func value** with no `@error` of its own is
never fallible, since nothing about a func type says whether it raises; a
lambda body's raise leaves the lambda.
