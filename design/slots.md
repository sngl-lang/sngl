# Slots, spreads and populations

What a slot is, and its two counts, are the spec's (*Slots and children* in
`docs/reference/specification.md`). This is how the compiler holds them.

**A named slot's count is how often the body inserts it**, written around the
whole component type (Jonathan's call): bare is exactly once, wherever the body
goes; `option[component() T]` at most once, possibly under an `if`;
`list[component() T]` any number of times. It is a count of *insertions*, not
of the nodes a caller writes, because it is what a ref read through the slot is
wrapped in -- `c.t.row` is a list for a list slot, `c.guard.gone` an option for
boundary's `failed` -- and so it has to be true of the body.
`checkSlotInsertions` holds it, counting every path: each branch of an
`if`/`else` inserting once is once, a `for` is many, a boundary's fallback or a
slot fallback may be none, and a population of another node's slot renders as
often as *that* slot is inserted. A caller may still leave a named slot
unpopulated (its insertion's block is the fallback), so nothing is required at
the call site.

The component type itself counts the nodes a fill renders, in a named slot
and the rest slot alike: `component T` any number, `component family.one`
exactly one, `component option[T]` none or one, so
`src option[component option[imageSource]]` is inserted at most once and holds
at most one source. The two counts are orthogonal, and a population is held
to the inner one where it is written. The rest slot is inserted exactly once, so
bare children stay the caller's refs at the position the caller wrote them.
A count written around the `...` is refused (Jonathan's call): an `#id` in bare
children is typed by where the caller wrote it, which only holds if the body
renders them exactly once. Content the body may leave out or repeat is a named
`option[component …]` or `list[component …]` slot, whose population is a
namespace of its own -- avatar's `src`, shown only when there is a picture to
show. Bare
`family.one` is a count and no family: "exactly one of whatever this slot
already accepts", which is the tree the component declaring it belongs to.

A builtin's kind can say more than its declaration: a nav page's content is
mounted only while its stack shows the page, so `ir.Component.InsertsOf`
reads it as an option, bare children and population alike.

The parenthesised list reuses `FuncTypeParamList`, so a parameter there may be
named (`cell component(row Row)`) — and **the name is contract, not
documentation**, because a slot's structural match is by name (Jonathan's call:
a call already assigns by name, `add(b = 2, a = 1)`). So `ir.SlotDecl.Params`
is `[]*Param` and not `[]*Type`, the shape `ir.FuncSig.Params` already has;
dropping the name would leave #206 nothing to match on. Renaming one is a
breaking change to every caller. Note that `FuncTypeParamList` has no `@` form,
so a slot's contract cannot mention events — true today as a consequence of the
reuse rather than as a decision anyone made.

**An insertion binds its arguments the way a call does**, through `bindArgs`
against the slot's `Params`: positionally, by name where the parameter has
one, and a spread by its fields.

**`...x` in any argument list is `x`'s fields written as named arguments**
(Jonathan's call), so one rule covers a call, a component's props, a slot
insertion and an emit. `expandSpread` (`internal/checker/spread.go`) is that
rule, and each list binds its fields by name: one naming no parameter is left
out, a spread none of whose fields names one is an error, and a field
naming one already given is "already provided". Two consequences:

- **A list whose parameters carry no name takes no spread.**
  `cell component(Row)` is one unnamed parameter and an event binds its
  parameters by position, so `cell(...r)` and `fired(...ev)` name nothing; `cell(r)` and
  `fired(ev)` are the spellings. Before, both kept the spread whole, and an
  `ir.Spread` reached `SlotInst.Args` and `Emit.Args`, where Go emitted the
  bare struct and Kotlin `*r.toTypedArray()`.
- **An `ir.Spread` is only ever a list-literal element.** A spread reaching
  `checkExpr` stands where one value is taken (a context's value, a
  conversion) and is refused there; `checkListSpread` is the one constructor.

**A function's last parameter may be `...T`**, and it collects the positional
arguments past the others: `func sum(first int, rest ...int)` reads `rest` as a
`list[int]` (`ir.Param.Variadic`, whose `Type` is the list). The checker packs
them into a list literal at each call (`bindArgs`), so nothing after it sees
anything but a list argument and no backend grew a case. A spread there is the
other meaning `...` has in an argument list, told apart by the operand's type:
a list contributes its elements, `sum(1, 2, ...xs)` being `sum(1, [2, ...xs])`,
and a struct still names parameters by its fields. Given nothing, the parameter
is the empty list, so it takes no default; naming it, `rest=[2, 3]`, passes the
list whole. Only a declared function has one: a func type has no `...`, so a
call through a value of one passes the list. `int.min`, `int.max` and the float
pair are the library's, and an emitter whose host takes the arguments
separately writes a literal rest out (`codegen.VariadicArgs`) and folds over any
other (`testdata/func_variadic.sngl`, `func_variadic.txtar`).

**The operand is evaluated once**, however many fields it fills. Each field is
an `ir.Select` of the operand, so a computed one (`add(...next())`) carries a
site id in `Select.Spread` and `ir.StatementSpreads` names the sites a
statement evaluates unconditionally. `passSpreadOnce` binds each to a
`__spreadN` ahead of the statement and the interpreter caches it at the same
point, so both evaluate the operand before the rest of the statement. A site
under `&&`, `||`, a ternary branch or a lambda, or in a condition loop's head,
is not bound and still reads per field. A view body cannot hold the temp, so a
view-body spread whose operand writes state is refused
(`reportImpureViewSpreads`, after the purity fixpoint); a pure one is read per
field there (`spread_operand_once.txtar`, `error_spread_view_impure.sngl`).

`...component` is the **rest slot**: the one a caller fills with the children
written bare. Its name is the author's (`content`, `shapes`, `panes`,
`children` where nothing better is true), and it is inserted by that name like
any other slot. A component declares at most one; one that declares none
accepts no children, which is where `component X does not accept children`
comes from. Supplying both a population by name and bare children populates it
twice. `ir.SlotDecl.Rest` is the flag, `Component.RestSlot()` the lookup; the
old answer was the name `_`, which is why nothing keys on a slot's name any
more.

**A body inserts its rest slot exactly once, where it always renders**
(`checkRestInsertion`, `internal/checker/rest_slot.go`). Not under an `if` or a
`for`, not in a boundary that has a fallback, a slot fallback, a slot
population or a window, not twice, and not never — each a positioned error
(`error_rest_slot_insertion.sngl`). The reason is the element-ref rule below:
children written bare are the *caller's*, their `#id`s are the caller's refs,
and the rule is what makes one written node one rendered node without the
caller having to read the callee's body. Through a callee's own rest slot it
composes, since that callee is held to the same rule. A signature, a library
declaration's `{}` (its render is a target's, and the target's override is what
is asked), and a declaration carrying `#[builtin]`, `#[intrinsic]` or
`#[wildcard]` have no insertion to hold and are exempt.

**A rest slot may be scoped, and the population written by name is the only
thing that reaches its arguments.** `children ...component(v T) node` is
declared like a named slot's contract, and a caller that wants the arguments
writes `component children(v) { … }` — the same form and the same
positional binding an ordinary population gets. Children written bare see
nothing: a spread has nowhere to write a name, so there is nothing there for
the arguments to be collected into, and a caller switches forms to obtain
them. Reading the names off the *declaration* instead was tried and rejected
(Jonathan's call): it would put a binding in a body that never named one, and
put it there for every caller in the language. A second, unscoped rest slot
for the bare case is not the way out either, since a component declares at
most one and bare children would then have nowhere unambiguous to land.

The form was refused outright before — "bare children are written once, with
nothing to bind them to" — and allowing it needed no lowering change at all:
a population already arrives through `NodeInst.Slots`, which is the first
thing `ir.SlotBody` looks at.

`sngl:ui/nav`'s `page` is the user: a page's route parameters arrive as one
struct value in the `params` prop, `T` is inferred from it, and the body that
reads them is the population of the page's `content` slot. (A window was the
user until `href` and `params` moved to the page.) That is what
makes a path a plain string rather than an interpolation — the names in
`/p/{pkg}` are the struct's fields, not identifiers in scope. Before it, the
checker read the placeholders off the href and synthesized an `*ir.Var` per
name: a placeholder and a node `#id` shared one namespace with nothing
declaring either, so which one a body's `pkg` reached fell out of scope-push
order; nothing could say a parameter was anything but a string; and a
misspelled placeholder declared a var rather than being reported.
`passNavigationHrefs` (`internal/lower/navigation_hrefs.go`) asks the last two
now, holding every `{name}` to a field of the struct, that field to a type a
path can carry, and every field to a placeholder.

`NodeInst.Params` is the cell it lands in -- on the page, and from there on the
window of the document html's `documentsOf` writes for it -- and it is the `*ir.Param` the
population declares — an ordinary slot binding, because there is no
distinction for the checker to make. That a target *stores* it is codegen's
answer: `CodegenCtx.ModelState` is "which bindings a single-Model target puts
in its Model", and an `ir.Symbol` rather than an `*ir.Var` for exactly this
reason — not everything stored is a declaration a body made. `OwnedVar`
answers the five questions the four Model emitters ask, of which a parameter
answers `Name` and `Type` and is neither const nor synthesized, and whose
`Init` is its type's zero because nothing in the program writes one. A Go
route handler binds it from the request (`writeRouteParamBindings`), a
client-only route's script reads it from the path the browser asked for
(`routeParamsJS`, since one baked document answers every request), and a
target with no request renders that zero.

Two things follow from the zero being codegen's. The shake roots the window
*node* rather than its children, so `Params.Type` and the `@error` are
reachable — the struct a route's parameters name is otherwise declared and
never constructed, and went. And a page that writes no population carries no
cell at all, so nothing downstream binds a route parameter for a page that does
not read one.

**A window's body is a node's block**, read by `checkSlotPopulations` and
checked in pass2 like every other node's (`testdata/error_window_population.sngl`
for the population rules, `cmd/sngl/testdata/window_prop_reads_a_later_decl.txt`
for a prop reading a declaration further down the file). A window used to be
registered in pass1 and checked by a construct of its own, which restated the
population rules and missed two.

**A node's `#id` at the root of a file is the package's** (`hoistPackageBodyIDs`):
hoisted into the package scope at the start of pass2, at the count an `if` or
a `for` around it confers, so every root statement, package func and component
body reaches it, as a window's handle always was. What a window's body
declares hoists like any node's block, plainly, so a sibling window reads
another's nodes as one node -- every target holds every window in one process
or document (`testdata/window_reads_sibling_window.sngl`). A plain id is a
declaration of the package, held to its one-name rule: one naming what the
package declares is "already declared in this scope", and two nodes writing one
are a duplicate (`error_window_id_takes_a_declared_name.sngl`,
`error_window_id_collision.sngl`). A component body already refused both.

**`...` and a count wrapper compose**, and deliberately: `content ...component family.one` is "the bare children, of which exactly one". The two say different
things — `...` says *which* children arrive here (the unnamed ones), the
wrapper says *how many* — so they are orthogonal rather than two spellings of
one bound. `tooltip`'s trigger is of that shape, and `scroll` and fyne's
`Wrapper` are its `option[…]` half: `Wrapper`'s codegen assigns the single
child to a field, so the combination is load-bearing rather than tolerated.
Issue #193 briefly specified it as an error on the grounds that both were count
bounds; that would have left the contract unspellable and is retracted there.

**A rest slot's count is what the children render**, not the statements written
(`renderedRange`, `internal/checker/family.go`): a node is one, an `if` the
least and most of its branches, a `for` any number from its body's least, a
boundary its content or its fallback, an insertion of another slot that slot's
count. So `one { for … }` is refused and `one { if c { a } else { b } }` is not;
counting statements, the first passed and a wrapper assigning its one child to
a field was handed several. A bare `if` is none or one, which is why `scroll`
takes `option[node]`: the two goldens putting a reactive `if` in a scroll are
what the backends render into a box (`testdata/error_children_count_rendered.sngl`).
A named slot counts its insertions instead (above).

**A slot's invocation list may declare a component entry**, and it is the same
slot mechanism one level down: `layout component(page Page, content component ui.node) T` says an insertion of `layout` hands its population a
`content` to insert. The insertion populates it by name, the way a call site
populates a component's slot — `layout(p) { component content { … } }` — and
the population binds it by position under a name of its own, like every other
invocation argument. It exists because a component is not a value
(`struct Page { content component() ui.node }` is refused), and handing a
layout the body it wraps is what a directory import needs.

Two rules keep it from meaning something else. **The bare block written at an
insertion is that slot's fallback** (`ir.SlotInst.Children`), which is what it
already meant before entries existed, so content reaches a population only
through an entry. And **`...` is refused in an invocation list**
(`error_slot_invocation_rest.sngl`), with a message naming the fallback: a rest
entry would be filled by exactly the block that is already the fallback.

**A fallback renders wherever the supplied content renders nothing**
(Jonathan's call), not only where none was supplied: the branch of an `if`
that holds none, a forward of a slot its own caller left empty, two `if`s
both false, a loop over nothing, content holding only an effect. It is the
answer `.empty` gives (below), so the two never disagree: the splicer puts
the fallback where the answer is decided -- in place of no content, in an
`if`'s empty branch, as a forward's own fallback -- and anywhere else after
the content under an `if` on `ir.RendersNothing`
(`SlotSplicer.withFallback`). The interpreter decides at mount (`slotInst`,
which mounts the fallback when the content mounted no node). An effect or a
timer -- a node of no family handed nothing -- renders nothing in both
(`testdata/slot_empty_agrees_with_fallback.sngl`).

**A fallback is held to the family of the slot it falls back for**, not to the
position's (`checkFamilyMembership`'s `SlotInst` arm): it stands where the
content would have, so one insertion renders one family whichever of the two
it shows, and `#[family.crosses]` lifts the position's check and never this
one (`error_slot_fallback_family.sngl`).

**`name.empty` asks whether what the call site supplied for a slot renders
nothing** (`ir.SlotEmpty`, `internal/checker/slotempty.go`), so a body puts
something of its own in the content's place rather than inside it -- an
avatar's initials, which are no image source. It is answered where the body
is spliced, from the content (`ir.RendersNothing`, written in by
`SlotSplicer.Substitute`, and in the initializer of a var the splice hoists),
matched to its slot by declaration rather than name, since a caller's own
`.empty` may arrive in a prop argument: none at all is true, a node false, an `if` its
condition, a counted or measurable `for` its emptiness test (or, where its body
renders nothing, the else's answer when it runs no copy), and a forward
the forwarded slot's own `.empty`, answered at the next splice. A forward
of a component entry is answered by what the insertion whose population
forwarded it populated the entry with (`answerEntryEmpty`, where the
population is spliced), and a slot that content forwards by the call site
of the component that wrote it. So it is as reactive as the content -- under
`if shown` it is `!shown`. A component built at run time is handed it as a
prop (`passSlotEmptyProps`, keyed by declaration); a forwarded entry whose
populating component is built at run time has no splice to answer it and is
refused with a position. The interpreter asks the content it mounted
(`Env.slotEmpty`), finding the slot's owner by declaration and an entry
through the entry frames current where the asking instance mounted. A test
reading a prop that asks (`c.card.label.value`) is answered the same way
from the call site it reaches the instance through (`answerTestEmpty`).
It is asked only in the view, where there is content to ask about. A scoped
slot is refused, since what its content renders may turn on the arguments an
insertion hands it, and so is content whose answer turns on what one copy of
a loop renders (`checkSlotEmptyAnswers`, at the call site). That holds
however the question reaches the content: from the declaration's body or
any override's, through a recursion (`slotsAskedEmpty`, a fixed point over
what a component reaches), and through an entry a population forwards
(`slot_empty.sngl`, `slot_empty_entry.sngl`, `error_slot_empty.sngl`,
`error_slot_empty_reach.sngl`, `cmd/sngl/testdata/slot_empty_runs.txt`).
The fallback is spliced against the callee's call site, which it always
should have been: a slot insertion inside a fallback used to be left
unsubstituted. `testdata/slot_fallback_where_content_renders_nothing.sngl`.

`ir.SlotDecl.Slots` holds the entries, with `Index` their position in the
list; an insertion's populations are `ir.SlotInst.Slots`, and an insertion *of*
an entry is a `SlotInst` whose `Entry` names it. `ir.SlotSplicer` substitutes
entries as it splices a population, so the optimizer and the inliner get them
at once, and the interpreter mounts the content in the insertion's scope
(`entryInst`). **Both match an entry by `*SlotDecl` pointer**, which is why a
generic component's call-site specialization copies its entries for checking
and records each copy's origin (`checker.entryOrigin`): `SlotInst.Entry`
carries the declared one, or the splice matches nothing and renders nothing
(`slot_entry_generic.txtar`).

**A component built at run time renders what it is handed** — bare children,
a population by name, a scoped one binding the insertion's arguments, and an
entry's — on every target, each of them reading the caller's scope and the
callee's arguments per copy (`testdata/runtime_instance_named_slot.txtar` and
its `_scoped_rest`, `_slot_entry` and `_recursive` siblings,
`cmd/sngl/testdata/runtime_instance_named_slots_runs.txt`). It used to be
refused for everything but bare children.

On html, fyne and gtk4 that is `passInstanceSlots`: a factory or record is
emitted from the declaration alone, so each instantiation handing one content
gets a copy of the component (`Card__slot0`) with it spliced in by
`substituteSlots`, and every other runtime instance has its insertions
replaced by their fallbacks. A population's parameters are bound where the
copy inserts it, so they are the callee's; what the content reads from the
caller crosses the way a slot child does in `passSlotChildInstances` — a
value becomes a prop the render rewrites per copy, a handler an event whose
body stays where it was written, handed the host event's payload, any
population parameter it reads, and any variable the content binds itself -- a
loop's, a local -- which nothing binds at the site (`EventDecl.Params`, the
one event a source declaration cannot spell;
`testdata/slot_content_handler_reads_its_loop.txtar`). A call reading a population parameter stays in the
copy, since its arguments name nothing at the site.
android passes each slot as a nullable composable parameter taking the slot's
invocation list, an entry being a composable of its own; bubbletea's render
method takes a func the same way, and splices a component with state. Null is
"supplied nothing", which renders the insertion's fallback.
A copy of a recursive body holds the recursive site again, so a copy is keyed
by the template site it was made for *and* by the statements that site's
content was cloned from, and a copy meeting its own site with the content it
was made for reuses itself
(`testdata/runtime_instance_bare_children_recursive.txtar`). The site alone
is not a key: every copy of a declaration holds a clone of each of its sites,
so two callers forwarding different children would share the first one's copy
(`runtime_instance_bare_children_forwarded.txtar` and its `_recursive_`
sibling). Nor are the origins alone: a recursion forwarding `label(d * 10)`
hands each level content cloned from the same statements and bound
differently, so the key also carries the content's shape, and a site that
has needed `maxSlotVariants` of them is refused — composing a slot at every
level is what a function does, and the three targets build copies at compile
time. bubbletea and android compose it (`slot_population_runtime_instance.txt`).

**A population is a `ComponentDecl` read by position.** At the root of a
component definition's body it is a nested declaration (pass1's
`collectComponentDecls`); directly in a child node's block it populates one of
that node's slots (`checkSlotPopulations`, which peels it off before the
children are checked). Anywhere else — inside an `if`/`for`, or in a function
body — is an error, which is what a `ComponentDecl` reaching `checkStmt` now
means. Its parameter list is a `ParamList` read in *bind* mode: the compiler
holds the slot's signature, an entry names a position, and a written type is
optional and measured against it by `parambind.go`'s `bindParamType`.

A component type parses wherever a type is written, and `...` wherever a type
prefix could go; `resolveType` refuses both outside a slot. That is deliberate
— a permissive production plus a specific diagnostic, the same trade `ArgList`
makes.

**A slot population is a scope**, as a component declaration's body is --
it reads like one and is scoped like one. Its node ids are hoisted there
through every node's children, so a sibling reads a `#id` written inside a
`vbox` or a `canvas` in the population as it would in a body, and they are not
the enclosing component's: not in its scope, and not claims in its namespace
(`collectElementRefIDs` does not descend into a population), so the same `#id`
inside and beside one is two nodes. The population's parameters are its head,
which an id gives way to (`slot_population_node_ids`).

From outside, a population's node is reached **through the node it
populates**: `c.card.title` for `card #card { component title… }`
(`populationHost`), as an instance's own ids are reached through its handle.
It resolves to the node `c` renders, `c.title` in the IR, which is the reach
every target answers; the test emitters read an event trigger's target off
that IR receiver (`codegen.EventTriggerTarget`) and never off the source's
spelling. Every target reaches a node from outside by the id it renders, so a
read where the component renders two nodes of that id is refused at the read
(`refuseAmbiguousReach`, `error_slot_population_ambiguous_reach.sngl`).
