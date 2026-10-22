# Language rules

What each construct means is `docs/reference/specification.md`'s; each
section here is how the compiler holds it.

## The const prefix

The spec's *The const prefix*.

`ConstDecl` is left-factored on `kw_const` (a name, `(`, `func` or `component`
follows), `StructBodyDecl` and `EnumBodyItem` take `kw_const FuncDecl`, and
`Param` takes `kw_const ident [Type]`. The flag is `Const` on `ast.FuncDef`,
`ast.ComponentDecl` and `ast.Param`, and on `ir.Func`, `ir.Component`,
`ir.Param`, `ir.Prop`, `ir.SlotDecl` and `ir.Override` (an override's own).

**`ir.IsConst` asks the declaration, never the inference.** A call is a
constant because `Func.Const` says so; `Func.Purity` is still inferred for
every function and still drives the optimizer, and promises a caller nothing.
A read of a `const` `*ir.Param` is a constant. That is the whole change to
the one test, so `const(…)`, a const initializer, a context default and a
const argument all move together.

The checker rules (`constparams.go`, `constfuncs.go`, `constcomponents.go`)
run after the purity fixpoint, where `const(…)` assertions are judged:

- An argument bound to a const param or prop is recorded where it is bound
  (`deferConstArg`, including a spread and a default) and judged by
  `runConstArgChecks`. A component body is read twice, so the pre-pass
  truncates the list with the diagnostics, and the judge dedupes.
- A bodied `const func` may read its params, locals and consts and call only
  const funcs (`checkConstFuncs`). A **bodyless** one is trusted and made
  `PurityPure` in `buildFunc`: nothing says what a host does. `pure` is gone
  from `#[foreign]`; the Go importer's `//sngl:pure`, the TypeScript pure tag
  and `file:` set `Const`.
- A const render (`renderCheck`) walks props, bindings, keys, `if`/`for`
  heads, slot-insertion args and context values, and skips handlers and
  lambdas. It allows contexts — a prop an ancestor supplies — and flags any
  non-const `*ir.Var` and any non-const call. It skips an argument bound to a
  const prop, which the argument check already reports at the same place.
- `const` on a base binds every override, reported at the override
  (`checkOverrideConst`). **Every component and override a target package
  declares must be const**, written or inherited (`checkTargetComponentsConst`);
  an empty `{}` component (a build node) and a primitive are exempt. This
  replaces what `passInlinePure`'s strict mode found at lowering with no
  position, so its impure-wrapper branch is now unreachable from a checked
  program. Every override in a target package (`lib/platform/*`, `lib/language/*`) says `const` for that reason.

**The library says `const` where it means it.** There is no default-pure rule
any more: a bodied library func is seeded pure for its own fixpoint, and a
bodyless one is pure only when it is `const func`. So the builtin methods,
`sngl:seq`, `sngl:ui/draw`'s geometry and the target helpers that build a host
value are marked, and a side-effecting native (a draw call, a timer,
`error.raise`, `async.start`) is not — it used to be pure by default.

**Inferred purity counts a call it knows nothing about.** An undeclared
native's `Purity` is `PurityUnknown`, which ranks *below* pure, so
`highestCalledPurity` used to let it through: a function whose body only calls
one came out pure, and passCSE merged `roll() + roll()` into one roll
(`testdata/cse_unknown_native_not_shared.txtar`). Such a call now counts as
read-only -- the host may answer differently each time, and it has no way to
name a program's state to write it. That also stopped the optimizer inlining
an async computed into the prop that reads it, which is what had hidden a
`NoAsyncReactive` ordering bug: it hoisted the call into an async `__hoist_0`
before lowering the computed to a plain read, leaving `await` outside an
`async` function. Named computeds are lowered first now
(`testdata/generate_async_computed_lowered.txtar`).

A library base is deliberately **not** marked `const`: it would bind every
override, a program's own included, and a program override whose render reads
its state is legitimate (it is kept as an instance).

**Lowering and codegen.** `passInlinePure` substitutes a const component
(`constSubstitutable`) unless it has funcs, or has a var and sits under a
`for` — a hoisted var there would be one cell for every copy. A const prop gets
no setter and is never rebuilt (`propIsConst`). After the optimizer, an
argument bound to a const param or prop is folded owned to a literal tree
(`optimize.foldConstArg`, before the call is inlined), and one that does not
fold is a build error at the argument; one still naming a const param is left
for the call site that substitutes it. fyne's `spec` and bubbletea's
`Layout.join`, `Widget.model`, `Widget.binds` and `Styled.events` are const
descriptors, and their decoders panic on anything but a literal instead of
reading it as empty. **`const` never removes a reactive feature**: a prop is
marked only when it is a descriptor no program has a reason to change while it
runs, and a codegen that reads a value prop only as a literal today is a gap,
not a contract.

**A bodyless func's block is not a body.** `ir.Normalize` appends `return <zero>` to a signature, and a trusted-pure native then folded to that zero
(`<span>0</span>` for `add(1, 2)`). The optimizer asks `hasWrittenBody` before
it folds, interprets or inlines a body; the old `pure` + `native` pair had the
same hole. `cmd/sngl/testdata/const_arg_unfoldable.txt` is both halves.

## Component syntax

The spec's *Components*: `{}` is an empty body that renders nothing, and no
block at all is a signature whose render comes from elsewhere.

`reportBodylessComponents` requires that of a signature rather than assuming
it — the counterpart of the rule `checkFuncBody` applies to a bodyless func.
Without it a bodyless declaration renders nothing, silently, on every target
that has no override for it. An **override** may not be bodyless for the same
reason from the other side: an override *is* the body a target renders, so one
with no body would satisfy the base's rule while rendering nothing. It is
refused in `addOverrideBody` *after* the override key is reserved, so the base
is not reported a second time for a supplier that was written and rejected.

Two limits on that rule, both deliberate and both load-bearing for #213:

- **One override satisfies it for every target**, because a program is checked
  without knowing which target a build picks. This is the func rule's existing
  semantics rather than a new hole, and
  `testdata/bodyless_component_override.txtar` pins what it costs: two targets,
  where the one with no override renders nothing at all.
- The diagnostic names only the override, because a program can write neither
  `#[intrinsic]` nor `#[builtin]` — both live in `sngl:internal/marks`.

A **library** declaration is asked the same question by
`reportBodylessLibComponents`, and asked it **per target**: by then the build's
targets are resolved, so an override for one is not an answer for another.
`hasOverrideFor` mirrors `ir.pick` — the platform's override answers first and
the language's is the fallback, which is what lets one
`sngl:language/go` implementation serve fyne and bubbletea while gtk4 and
android override on the platform axis. It runs at the very end of
`CheckPackage`, because a lib package loads on import *or* when
`mergeTargetExtensions` resolves an override's base, and both can happen after
`newChecker`.

It is also asked **only of what the program renders**. A gap matters where it
is reached, and importing a package is not reaching every declaration in it: a
program that imports `sngl:ui/draw` for `Point` and draws nothing asks its
target for no shapes, and a new platform implements what its users write rather
than the whole of `lib/` before the first program builds.
`reachedLibComponents` is that set — seeded from the program's own bodies and
closed to a fixed point through the body each target will actually build, an
override's where it has one. Ungated, a stub platform in a test about output
props was asked for seven shapes.

Two consequences worth knowing before touching `lib/`:

- A bodyless `lib/` component the program renders, with no implementation for
  the target being built, is a **build failure**, which is the mechanism that
  stops an implementation gap being skipped in a switch.
- An override's **parameter list** selects what its body reads, and no list
  is `()`, as for any other parameter list. It is read by `overrideSelection` for a component and `selectFuncParams`
  for a function, both through `selectEntry`: each entry names one of the
  base's props, slots or `@events` (a function's parameters), once, and may
  restate a type only where it matches. What it does not list is not in the
  body's scope. Parens with nothing in them *and no target index* are
  android's marker for a component its own codegen reads by name.

The distinction has to survive the checker, so it is `ir.Component.Bodyless`
rather than `AST.Body.IsDefined()` — that reports whether a block came from
*source*, so everything `ir.Convert` rebuilds looks bodyless, and reading it
as "has a body" made `sngl dump --stage checked` print every empty-bodied
component back as a signature.

**A prop with no default is required, whichever package declares it.** A
library prop used to be optional because it was a library's, which is a rule
about where a declaration lives. What is left is about what a declaration is
(`ir.Component.RendersFromDeclaration`): a primitive a platform emits, a
wildcard element, a builtin node, a build-target node and a canvas are read
straight off their node by a target, which takes an omitted prop as *absent*
-- html writes no `checked` on an element that named none, and a build option
left out is not an option set to its zero -- so their props are not required
and their defaults are not filled in. Every other component's omitted props
take their written default at the call site, except a bodyless one's, whose
override reads the declaration's defaults itself. `Prop.DefaultWritten` is what
makes a prop optional, because `Default` is only checked with its component's
body and a call checked first would see none.

## Event syntax

An event declares the parameters its handlers receive **as a function
declares its own**: `@pick(index int, label string)`, `@change(checked bool)`.
It is the same list read by the same code -- `EventParams` is `lparen [ ParamList ] rparen` in the grammar, the builder reads it with
`buildParamList`, and the checker resolves it with `buildParams` -- so each
parameter is named and typed, and `ir.EventDecl.Params` is that list. The
names are documentation rather than contract, since a handler binds by
position. As a function may, an event leaves its parens off when there are
none: `@tick` and `@done()` both pass nothing, and `ir.Convert` prints the
second. What a function's parameter may carry and an event's may not -- a
default, `const`, `...` -- is refused in `eventParams`
(`testdata/error_event_param_forms.sngl`).

There used to be a func *type's* list here, names optional, and a shorthand
for a list of one, `@change T`. The shorthand is refused naming the list
(`testdata/error_event_type_shorthand.sngl`): the grammar still admits it so
that the builder can say `@change(v T)` rather than "unexpected identifier".
Most bare events meant none -- a timer's tick, a DOM event SNGL cannot name a
payload for -- which is what took the `func(any)` and the `nil` every tick
handed it out of a timer instance's generated code. In an **override's** prop
list a bare `@click` selects one of the base's events rather than declaring
one, and `ast.EventDecl.HasParens` is what tells it from `@click()`, which the
selection refuses. An event a lowering pass synthesizes names its parameter
too -- a two-way prop's write-back is `@count(count int)` -- and `ir.Convert`
names one that arrives unnamed `v<i>`, since the list it prints must parse.

A handler binds **by position**, the way a func literal does, and its
parameters take their types from the declaration it implements, as a
lambda's take theirs from the func type it is handed, so it may write names
alone or annotate them. It may leave trailing
parameters unbound; binding more than the event passes, or annotating
one with a type other than its position's, is a positioned error
(`bindParams`). An emit supplies every declared parameter
(`checkEmitArgs`) — or **none at all, which forwards**: written in a handler,
`click()` hands on what that handler received, position for position
(`bindEventParams`), and it is how every platform override re-fires its host
widget's event. An event used as a callback (`eventAsFunc`) takes the event's
parameters exactly, or none.

**An event's parameters are what it carries; there are no payload structs.**
`sngl:ui` declares each event as a function declares its parameters:
`@change(checked bool)` on a checkbox, `@input(value string)` on an input,
`@change(value option[time.date])` on a datepicker, `@click()` carrying
nothing. A struct handed to a handler whole stays where the value *is* one:
a boundary's `@error(e error)`, a user event whose author chose one. A host
widget's own event is declared the host's way and read by the override that
wraps it, which hands on the parameters the `sngl:ui` event declares (see
*An event's parameters are its declaration's*). A parameter list reaches a
backend through a *user* component's event, where passInlinePure substitutes
it and passInstanceEvents turns it into a func-typed prop with that
signature; `testdata/event_params.txtar` and
`testdata/event_params_instance.txtar` are the two routes, and
`testdata/ui_event_params.txtar` is the library's events on every target.

**A test fires an event as the program would.** `c.box.change(true)`,
`c.f.input(value="x")`, `c.b.click()`: a trigger's arguments bind to the
event's parameters as an emit's do, positionally or by name, all of them.
`c.row[1].change(true)` names one of the nodes a loop rendered under one
id, and `c.cell[1][0].click()` one nested loops rendered, by the index into
each loop, outermost first (`codegen.EventTrigger.Path`). No target could turn
a path into a position among all the copies without knowing how many each
earlier iteration rendered, so each copy carries its **key** instead:
`passCopyKeys` writes it into `ir.CopyProp` (`__copy`) on every copy of an id
a test indexes (`ir.IndexedRefIDs`) -- the loop indices joined by `/`,
`"1/0"`, a single loop's being the bare index (`ir.CopyKey`) -- and hands a
component built at run time the key of the copy it stands in through a prop of
the same name, which its own copies' keys extend. An ordinary prop, so an
instance a slot keeps and moves is handed its new key by its setter. Each
language's test lowerer calls the invoker's `At` variant
(`codegen.InvokerAt`) with the key first, and each target finds the copy that
carries it: html writes it as `data-sngl-copy`, in markup and at run time, and
queries for it; fyne and gtk4 keep a map per id (`__copies_<id>`), put there
by each write of the key and left in `Destroy`; bubbletea walks the loop as
View counts it to the copy's focus ordinal, beside the key the walk computes;
android sets it as a `SnglCopy` semantics property beside the test tag, read
where the modifier is built. The key is an addition: the plain invoker stays,
since an id one test indexes may be written once elsewhere, and a node
written once may sit under a loop after lowering (a tab's panel on
bubbletea). A component built at run time is handed a key only where one of
its instances is rendered under a loop. Adding an index to a loop over a list the checker wrapped as a
sequence walks the list (`loopIndex`), and a loop over a pull sequence keeps
the counter `passIndexedIter` puts beside it in every view walk.
`testdata/indexed_trigger.txtar` and `indexed_trigger_nested.txtar` are the
code, and `cmd/sngl/testdata/indexed_trigger_runs.txt` and
`indexed_trigger_nested_runs.txt` run them on all six.

## Loop forms

The spec's *The for statement*: one head, whose type says whether the loop
walks an iterable, tests a condition, or has no head at all. The grammar does
not distinguish the forms.

`ir.For.IterKind` is that classification, stamped late by `passIterKind` from
`ir.DeriveIterKind`; each language's `ForHead` emits a per-kind template. A
condition or headless loop carries no iterable, so `ir.For.Iter` is the
condition or nil.

The last two are **imperative-only** — function, handler, timer — and so are
`break` and `continue`. A view body's loop says how many copies of its body
the rendered tree holds: a list gives that a length and a counted sequence a
number, and a condition gives neither, so there is nothing for a mutation
model to diff and nothing for a static renderer to write down. **A map over
the first head is imperative-only for the same reason applied to order**: it
says how many copies, and in no defined order, so two renders of one map may
lay the body out differently and neither a diff nor a static page has anything
to hold. Imposing an order instead buys a per-platform contingency at every
backend — a key type the host cannot sort, a host map that happens to be
insertion-ordered so the bug appears only on the other targets. The checker
refuses all five in a view body with a positioned error (`checkHeadlessFor`,
`checkViewMapFor`, `requireLoop`), which keeps codegen to the imperative paths
that route through `ForHead`. A map whose map-ness the head has erased is out
of reach: `map[K, V]` is not assignable to `iter[T]`. `c.funcDepth == 0` is what "in a view body" means; `c.loopDepth` is
what an escape requires one of, and it resets at every imperative-body
boundary (`enterFuncBody`) so a lambda cannot break a loop it was written
inside.

A head expression may not begin with `{`: that brace is the body's. `CondPrimary`
in `internal/parser/sngl.ebnf` is `StatementPrimary` minus `AnonStructLit` for
exactly that reason — with the head optional, `lbrace` in `FIRST(CondExpr)` is
a predict conflict against the `StmtBlock` that follows. So a map or
anonymous-struct literal in an `if`/`for` head is written parenthesized. The
tree-sitter grammar says the same thing by preferring the headless `for`
alternative at a higher dynamic precedence.

**`else` means the body never ran** (the spec's *The else block*). In an
imperative body `passForElse` states that as a flag: `__ranN := false`
before the loop, set as the body's first statement, tested by an `if` after
it. Every backend already emits those three statements, so no language grows a
case — before the pass, `codegen/irwalk` read a loop's head and body and
nothing else, and an imperative for-else compiled with the else silently
dropped.

A view body's for-else is `passViewForElse`, and the two cannot share a
desugaring: a flag is a statement, and a view body on a target with no host
language has nowhere to put one. So the emptiness question is asked of the
iterable instead — `if <iterable is empty> { ELSE } else { for … { BODY } }`,
built by `ir.EmptyTest` — and asked again on every render rather than stored.
That leaves the checker two requirements the loop itself does not have
(`checkViewForElse`): the head must be *measurable* (a list or map reports a
length, a `sngl:seq` range is compared against its bounds; a pull sequence
answers only by consuming an element) and it must survive a *second*
evaluation, since it is now written once and read twice. Both are positioned
errors. The pass runs early, well before `passReactivity`, so the `if` reaches
that pass as an ordinary view conditional and a reactive iterable makes it a
render slot — which is what re-asks the question when the list changes.
Leaving the else to the platform emitters was the previous answer and only
bubbletea implemented it; fyne, gtk4, android and html on `--lang none` each
emitted the loop and nothing at all, silently, since the interpreter honours
`Else` itself and `sngl test` passed everywhere.

Which blocks each pass rewrites is `blocks.go`'s answer: `imperativeBlocks`
for `passForElse`, shared with `passCSE`, and `viewBlocks` — its complement —
for `passViewForElse`. The first is the declared imperative
roots *and every lambda body in the package*, the latter from `ir.Walk`. The
lambdas are what reach a handler on android, where a handler body is a lambda
in `NodeInst.Props` by then rather than an `ir.EventHandler` — a hand-written
descent through the view finds nothing there, which is why a pure call made
twice in an android click handler went unshared until both passes were put on
the one walk.

**A walk over a view asks `ir.ViewBlocks`; a walk over everything is
`ir.Walk`.** A pass that descends a view -- where a node, a page or a surface
stands -- reads a statement's blocks from `ir.ViewBlocks` (a node's children
and each population of its slots, a slot insertion's fallback and its entries'
populations, and `ir.TransparentBlocks`: an `if`'s and a `for`'s body and else,
a match's arms, a boundary's content and fallback, a provider's content), or `ir.WalkView` for
a plain visit, and handles a statement's own handlers itself. A pass that
wants every expression -- props, handlers, lambdas, slot arguments -- is
`ir.Walk`/`ir.Rewrite`/`ir.RewriteExprs`. A hand-written switch over the
statement kinds is how each of these walks went wrong, one member at a time,
and the members missed were always the same few: a slot entry's population, a
boundary's fallback and own handler, a provider, a for's else. Each was a
silent loss reachable from a program -- a for-else's else, a `:value=`
binding's write-back, a `var`'s hoist, a function the fallback calls, a prop
the fallback reads (`view_for_else_slot_entry`, `prop_binding_in_slot_population`,
`view_block_state_slot_entry`, `boundary_fallback_keeps_what_it_names`,
`boundary_fallback_reads_prop`). A walk that numbers what it synthesizes
iterates slots by `ir.SlotNames`, never the map.

**A call site's `#id` replaces the one an override gave its root node, and
takes its reads with it.** `ir.AttachNodeID` moves the call site's id and
handle onto the node a spliced body renders, and repoints every read of the
handle the body had named that node by: gtk4's datepicker override names its
calendar `#cal` for the effect that selects the bound date, and with a call
site's `#d` the effect read a `m.cal` nothing built
(`cmd/sngl/testdata/datepicker_change_runs.txt`).

**A clone owns what its subtree aliases.** A boundary's handler is the
boundary's code, so both inliners' clones copy it with the content, and
`ir.RepointHandler` carries the catch blocks and resolved raises in the copy to
it -- shared, every window's `@error` was the first window's
(`window_error_handler_per_window`). A `CanvasRedrawStmt` in a copy that names
a canvas the copy holds names that canvas's copy (the optimizer's
`cloneStmts`), catch blocks' handlers included: an html document is a clone,
and a redraw naming the original repainted nothing
(`canvas_redraw_from_handler`).

`blocks.go` is not the only such enumeration, and a pass picks one of three
depending on what it needs a handle to. A pass rewriting a *statement list in
place* takes `blocks.go`'s pointers; a pass rewriting statements and leaf
expressions together takes `walk.go`'s `walkPackage` (`passTernary`,
`passIndexedIter`, `passNoRef`, `NoDeclarative`'s id scan); a pass wanting the
*functions* a target may enter takes `async_offload.go`'s `offloadableFuncs`.
The three enumerate the same owners and each used to say so in its own code,
which is what let one of them forget a case the other two had. **A window used
to own timers** — a lowering pass lifted each schedule onto whichever owner held
the node, and the inliner has by then put a top-level component's timer in the
window — and `walkWindow` and `offloadableFuncs` both walked a window's vars,
funcs and body and not its timers. Nothing in source puts a timer there, so
both gaps opened only after that pass ran and were invisible to every fixture
written before it: a ternary in a `@tick` panicked the Go emitter, a
two-variable `sngl:seq` loop there emitted `for i, x := range` over a pull
sequence, and a `#[go.async]` call there ran on fyne's drawing thread.
`testdata/timer_tick_lowered.txtar` and `testdata/timer_tick_async_offload.txtar`
pin the three. Neither list exists any more: a timer primitive stays in the
tree it was written in, so a tick is the `@tick` handler of an ordinary node and
is reached wherever a node's handlers are.

Two of the three now *ask* `ir.Owner` rather than restating it. `blocks.go`
and `offloadableFuncs` both iterate `ir.Owners(pkg)` -- the package and each
component, since a window is an ordinary node of whatever renders it -- so
what an owner holds reaches them because the enumeration says so and not
because each remembered to. Each used to have a *second*, thinner copy of a
window arm beside the owners loop, and `blocks.go`'s passed nil timers and
skipped the window's `@error` outright.

What *was* reachable is the case neither copy had: **the handlers on a package
var**, which a component's vars had in both and the package's in neither, so a
`for … else` in the `@change` of a top-level `var` reached `passForElse` not at
all. A var takes no handler any more (`testdata/error_var_handler.sngl`); an
effect's `@update` is a node's handler and is reached where every node's is.

Both orders are load-bearing and neither is this file's any more.
`passCSE` and `passForElse` name their temps `__cseN`/`__ranN` off `blocks.go`'s
order, and `passAsyncOffload` names `__async_offN` off `offloadableFuncs`', so
each keeps the order it had: the declared funcs across every owner before any
handler. `walk.go`'s `walkPackage` is the one left: it walks the package's
funcs, its components and `pkg.Body` by hand, where the owners would give it
all three.

**A lambda body is the fourth kind of block**, and it is the one none of these
reaches by walking declarations: it hangs off an *expression*. `blocks.go`
collects them with an `ir.Walk` for exactly that reason, and `offloadableFuncs`
leaves them out on purpose — a lambda is a value, and what calls it is the code
it was handed to. `walkPackage` is where it bit, twice, and in the same
position both times. `passTernary` and `passIndexedIter` each make the `expr`
hook a no-op because a const-context expression — an initializer, a prop
default — has no statement list to hoist into. True of the expression; false of
a lambda body inside it, which is an ordinary statement list. So
`var ys = xs.map(func(x int) => c ? a : b)` panicked every Go emitter with
"ir.Ternary reached Go codegen", and a two-variable `sngl:seq` loop written
there came out as `for i, v := range slices.Values(...)`, which no Go compiler
accepts. Both hooks now descend to lambda bodies and hoist nothing into the
initializer, so the reason they are no-ops survives.
`testdata/ternary_in_lambda_initializer.txtar` and
`testdata/indexed_iter_in_lambda_initializer.txtar` pin the pair; `passNoRef`,
the third, already had an `*ir.Lambda` arm in its rewriter. A third position
came from `passNoListLambdas`, which makes `xs.map(func(x) => …)` a loop calling
the literal in place: the literal is then a call's `Callee`, which
`transformExpr` never visited, so a ternary or a match in it reached the Go
emitters from a test body or a prop
(`cmd/sngl/testdata/list_lambda_branch_runs.txt`). The loop it makes of a call
in a ternary's branch, a match's arm or the right side of `&&` or `||` is
written in that branch, its value a temp each branch sets: ahead of the
expression, `b != null ? b.items.map(f) : []` read `b.items` with `b` null
(`cmd/sngl/testdata/list_lambda_conditional_runs.txt`).

That was latent in a second place until a timer became an effect: the tick is
then a closure handed to a host scheduler, so the same defects came back on
fyne through an `ir.Lambda` instead of through the window list a timer then had.
`offloadableFuncs` leaves a lambda out for the reason above, with one
exception -- the lambdas handed to a call that **says it schedules**. What
calls an ordinary lambda is code the pass can read, and `xs.map(f)` wants the
blocking f rather than a goroutine per element; but a host scheduler calls its
callback from the loop it owns, which is the thread the target draws on.

**Which a declaration says with `schedules`**, the third `NativeFlag` beside
`fails` and `method`. It is a flag rather than something read off the call, for
the reason nothing about a `#[foreign]` declaration is ever inferred: its SNGL
body describes the identifier and does not implement it, so nothing in the
program says when the host runs an argument -- and "the callee is a native"
would be wrong in the other direction too, giving a goroutine to a native that
runs its callback inline. A scheduler whose declaration forgets the flag gets
the *old* bug rather than a new one: the callback is not an entry point and a
blocking call in it stays on the drawing thread, which
`testdata/timer_tick_async_offload.txtar` denies on both Go platforms.

**Constructing a closure is not calling it**, which is the colouring half of
the same change. `ir.ExprHasAsyncCall` read through an `ir.Lambda` into its
body, so `handle = every(d, func(){ …blocking… })` was an async statement and
the function holding it was coloured async -- for a body that hands work over
and blocks on nothing. On Go that reached `passAsyncOffload`, which refuses a
blocking call inside the `if` an effect's `_up` wraps its mount in; on
JavaScript it spread `async` from the arrow up through the settle. The closure's
own `ir.Func` is coloured in its own right, which is what makes the descent
redundant as well as wrong.

## Match

`match [subject] { arms }` is a statement and an expression, and an arm takes
the form of its position, as a function body does: `pattern { … }` where a
statement goes, `pattern => value` where a value goes, and the checker refuses
the other (the grammar takes both everywhere). A pattern is a constant -- an
enum member written bare, a literal, a `const`, `null` -- compared with `==`;
commas join patterns, `else` is the last arm, and nothing falls through. A
headless `match { cond => … }` tests `bool` conditions in order.

**`match var v = subject` names the subject for the arms** (`ast.Match.Bind`,
`matchBind` in `internal/checker/match.go`), as Go's type switch does: in an
arm whose one pattern is a member carrying a payload, `v` is that payload; in
every other arm it is the subject, narrowed to its element where the subject
is an option and the arm cannot be taken on `null` (`subjectBind`). In a block
run imperatively `v` is a local ahead of the block, declared only where the
arm names it, since Go refuses one unused; anywhere else -- a view, a match
expression -- each read of `v` is the read off the subject in place, which
may then not be written (`matchBinds`). Nothing after the checker sees the
name (`testdata/match_var.sngl`, `cmd/sngl/testdata/match_var_runs.txt`).

**Every match is exhaustive**: each enum member, `true` and `false`, and
`null` for an option, or an `else`. An `else` beside full coverage is allowed
and is what effective SNGL recommends for a match that should survive the enum
growing a member.

**The IR keeps it**: the checker (`internal/checker/match.go`) makes an
`ir.Match` statement or an `ir.MatchExpr` value, each an `ir.MatchArms` -- the
subject (nil when headless), the arms' patterns as checked, an arm's block or
value, and `Exhaustive` when the last arm is taken unasked. Three helpers say
what it means as the constructs it replaces: `Chain` (the if/else-if chain),
`TernaryChain` and `Taken(i)`, the condition under which arm i is the one
taken, which a walk carrying an `if`'s condition down its branches carries
down an arm. `ir.TransparentBlocks` lists the arms, so every walk asking what
a view's blocks hold asks a match's; a hand-written switch over the statement
kinds has a `*ir.Match` case beside its `*ir.If`. The interpreter runs it
itself (`interp/match.go`, the subject evaluated once), and the optimizer
folds one whose subject the build knows to the arm it takes (`foldMatchArms`,
consteval's `evalExpr`).

**`passMatch` writes the chain wherever the target cannot emit a match**
(`internal/lower/match.go`), first in the pipeline and again before NoTernary
(`MatchLate`), for one a pass between carried out of an imperative body. Four
capabilities decide it, and a target that has heard of none gets the chain:
`matches` (a statement, in a function, a handler or a lambda), `matchValues`
(an expression there), `viewMatches` (both in a view body or an initializer)
and `optionMatches` (a subject that is an option). Go, Kotlin and JavaScript
declare `matches`; Go and Kotlin `matchValues`, Go's being a switch assigning a
temp that passTernary writes (`liftMatch`, marked `Match.FromValue` as the if a
ternary becomes is `If.FromTernary`; `ir.ComputesValue` asks either), and
JavaScript, which has no switch expression, withholds it; android and
bubbletea declare `viewMatches`, and html, whose script is JavaScript under
`--lang none`, takes back `matches` and withdraws the other three. Only Kotlin
declares `optionMatches`: its `when` takes `null ->` beside the element's
values, where Go's switch would compare an option's pointer. Whatever is
declared, two shapes are the chain on every target: a subject a host's switch
compares differently from `==` (`MatchArms.Switchable`: anything but a bool, a
number, a string or an enum, or an option of one where the language declares
`optionMatches`), and a test function's, which each language's test lowering
writes out. An arm that breaks out of a loop around the match
(`Match.BreaksOut`) names the loop: the late run labels it (`For.Label`,
`Break.Label`, `labelLoops`, after every inliner has copied what it copies),
which Go and JavaScript write as `__loopN:` and `break __loopN`, since their
switch takes a bare break as its own, and Kotlin as a bare break, since its
`when` takes none. Where the subject is a `__matchN` the checker bound just
ahead and the match is kept, the binding is undone (`unbindSubjects`): a
switch evaluates its subject once. Each language renders one through
`irwalk.Renderer`'s `Match` and `MatchExpr` -- a `switch` with the arm taken
unasked as its `default`, a `when` whose exhaustive last arm keeps its
patterns so Kotlin checks it too, a JavaScript headless match written as the
chain -- android's composable writes a `when` (`renderWhen`), and bubbletea's
View a `switch` (`renderMatch`, through `GoIRContext.SwitchHead` and
`SwitchCase`), as does every walk Update and the test surface make of the
view: focus order, loop widgets, presence, overlays. A walk that only descends
reads `ir.TransparentBlocks`; one carrying a condition down carries
`MatchArms.Taken(i)`, and one reading what a match evaluates reads
`MatchArms.Heads`.

**The subject is evaluated once wherever a statement can hold it.** A subject
that calls anything is bound to `__matchN` ahead of the statement, in an
imperative block (`preludeFor`, set per statement by `checkBlockScoped`) and in
an expression body, which is the one statement `return …` (`bodyPrelude`). A
position evaluated only some of the time -- the right of `&&`/`||`, a ternary's
branch, a match's own arms, a condition loop's head -- has no such place
(`noPrelude`), and neither has a view body: there the subject is read again per
arm tested, and `reportImpureMatchSubjects` refuses one that writes state,
after the purity fixed point. Binding ahead of the statement evaluates the
subject before the rest of the statement, as a spread's operand is.
`testdata/match.sngl`, `match_fold.sngl`, `error_match.sngl`,
`error_match_impure_subject.sngl`, `match.txtar`, `match_native.txtar`,
`cmd/sngl/testdata/match_runs.txt`, `match_native_runs.txt` and
`match_roundtrip.txt`.

## Payload enums

**An enum member may carry a type, its payload**, and a value of such an
enum holds exactly one member and that member's payload:
`enum Result[T any] { ok T, failed Failure }`. An enum takes type parameters
as a struct does (`EnumDecl = kw_enum [ ident ] [ TypeParamList ] …`,
`EnumMember = ident [ Type ] [ assign Expr ]`), and a member with a payload
takes no `= value`. An enum whose members carry nothing is every enum there
was before and every target emits it as it always has; `ir.PayloadEnum` is
the question "does this one carry anything", and `ir.MemberPayload` the
payload's type at a value's type arguments. A top-level enum's payloads
resolve after every type's shell, as a struct's fields do
(`resolveEnumBody`).

The IR spells what a program does with one in constructs every walk already
reaches (`ir/enums.go`), so no pass learned a node:

- **Construction is a struct literal naming one member**, `Result[int]{ok=2}`
  or `{ok=2}` where the type is expected: an `ir.StructLit` of the enum's
  type with one field and no `Def` (`inferEnumLit`, `ir.EnumLitMember`). A
  member with no payload is its `Ident`, `Shape.empty`, as it always was.
  Naming two, none, an unknown member or one with no payload is a positioned
  error.
- **`r.ok` is an `ir.Select` typed `option[payload]`**, present only where r
  holds the member, so the null test that narrows any option narrows it. A
  member with no payload reads as a `bool`, `ir.EnumIs`. A payload that is
  itself an option is refused a selector (Kotlin and JavaScript have no
  option of an option) and is matched instead; the read is refused as an
  assignment target.
- **Whether r holds a member is `enum.is(r, "ok")` and the payload
  `enum.payload(r, "ok")`**, two compiler-synthesized intrinsics
  (`EnumIsIntrinsic`, `EnumPayloadIntrinsic`) each language and the
  interpreter answer. The always-on `passEnumReads` writes a selector's null
  test as the first and a narrowed read as the second, so what a target
  meets of the selector is only a read whose option is used as one.
- **A match over one is headless**: `match var v = r { ok { … } failed { … } }`
  is checked (`checkMemberMatch`) into arms each taken on `enum.is` of the
  subject, so `Chain`, `TernaryChain`, `Taken` and every target's rendering
  of a headless match serve it unchanged. `v` is the payload in an arm whose
  one pattern names a member carrying one (see *Match*). Exhaustive as every
  match is.
- **The zero is the first member's, with its payload's zero** (`ZeroExpr`).
- **`==` compares member and payload**, and is refused where a payload does
  not compare alike on every target (`ir.RebuildIncomparable`, which an
  effect's key asks too). An option compares there by presence and then by
  element (`optionCompare` in `internal/lower/rebuildcompare.go`), never with
  its own `==`, which on Go compares two pointers
  (`testdata/effect_key_option.txtar`). A payload enum has no string form: `string(r)` and
  `"{r}"` are refused unless it declares `string()`.

Each target: **Go** a struct of an unexported `tag int` (the member's
position, so the struct's zero is the first member's) and an exported field
per payload member, compared with `==`; a selector is an inline function
returning the payload's address. **JavaScript** `{tag, value}`, the tag the
member's name, compared by an expression written out from the type
(`jsEqualExpr`). **Kotlin** a sealed class of a `data class` per payload
member holding `value` and a `data object` per atom, a `mutableStateOf` and a
local of one given the sealed type. **The interpreter** an `interp.EnumValue`.
Go and Kotlin copy a generic enum per instantiation, a library's into the
program, on each side of the struct pass (`monomorphizeEnums`). A payload is
a use of its type for the shake, and a library struct one carries is promoted
as a field's is (`promoteForeignStructs`): `async.Result[http.Response]` held
or handed where nothing held a `Response` left Go's `Result__Response` naming
an undeclared `Response` and `Failure`
(`cmd/sngl/testdata/async_result_payload_struct_runs.txt`).
`testdata/enum_payload.sngl`, `error_enum_payload.sngl`,
`error_enum_payload_value.sngl`, `enum_payload.txtar` and
`cmd/sngl/testdata/enum_payload_runs.txt`.

**Async is inferred, never written.** A function is async when its body
waits for an async call (`ir.Func.IsAsync`), and a func-typed slot -- a var,
a parameter, a component's prop -- is coloured async when points-to says an
async function may flow into it (`colorAsyncSlots`, stamping
`FuncSig.Color`). The colour travels with the type, so a call through the
slot waits (`ir.CallsAsyncValue`) after a lowering has moved it somewhere
points-to never saw, as an instance built at run time does with its props
(`testdata/funcvar_prop_async.txtar`). The one refusal is rule 3: a function
that waits, handed to a callback a native or an intrinsic calls and uses the
result of, `xs.map(fetch)`, since the host would read the wait rather than
the answer (`refuseAsyncIntoHostCallbacks`,
`testdata/error_async_into_host_callback.sngl`).

## Queries

**`async.query` is a request bracketed by the tree** (`lib/async/query.sngl`):
asked when its node mounts and whenever `on` changes, abandoned when the node
unmounts. It is ordinary SNGL over `effect` -- no mark, no intrinsic, no pass
-- holding three two-way props a call site binds or leaves to the instance
(`value`, `pending`, `failure`), firing `@load` and `@fail`, with `refresh()`
re-keying the effect on a counter beside `on`. `fetch` answers
`async.Result[T]`, the payload enum `{ ok T, failed Failure }`; a failure is a
value, never a raise, and `Failure` is `kind` (`async.FailureKind`), `message`
and the adapter's own `code`. **The latest attempt wins**: each mount numbers
its attempt, an unmount bumps the number, and an answer whose number is stale
writes nothing.

The general things it needed, each of its own:

- **An effect's handler is started, not awaited** (`ir.Call.Detached`, written
  by `startOf` in the effect lowering). A call marked so colours nothing async
  (`ExprHasAsyncCall`, `exprHasFuncvarAsyncCall`, passAsyncOffload's
  `callWaits`) and JavaScript writes it without `await`, so a mount runs to
  its first wait and the settle goes on. Awaited, the settle held every other
  bracket and the next key's mount until a request answered, so a stale answer
  landed first and an unmounted query's answer landed at all
  (`testdata/component_async_query.txtar`, run in Chromium, fails all three of
  its tests without it).
- **A blocking call's arguments are read on the drawing thread**
  (`offloadState.bindArgs`): passAsyncOffload binds each argument of the first
  waiting statement's waiting calls to an `__async_argN` ahead of the
  goroutine, which read `m.__effect0_key` -- what the next settle overwrites --
  off the thread that owns it (`testdata/async_query_go.txtar`).
- **A function literal called where it stands waits where its function does**
  (`ir.CallsAsyncValue`): an adapter's `fetch=func(k K) => call(k)` is
  substituted into the mount as `(lambda)(key)`, which was neither awaited nor
  offloaded. And rule 2 of `checkAsyncRules` does not read a lambda handed to a
  prop as a call made by the render. Points-to binds no library component's
  prop, so `fetch`'s signature is never coloured: it is every check's
  declaration, and the call through it is a direct or a literal call by the
  time it is emitted.
- **A query built at run time waits as its fetch does** (`passAsyncProps`): a
  query written straight under a `for` is the instance itself, so `fetch`
  stays its prop, which nothing coloured. A func-typed prop of a component
  built at run time is typed as a function that waits wherever a call site
  hands it one, before passEffect colours the mount; html read the Promise
  its fetch returned as the answer, fyne and gtk4 fetched on the drawing
  thread, and android's composable took a suspend function as a plain one;
  Kotlin spells a function type that waits `suspend`. The value the call goes
  through is bound ahead of the goroutine with the arguments, since a setter
  writes it (`testdata/async_query_fetch_prop.txtar`,
  `cmd/sngl/testdata/async_query_fetch_prop_runs.txt`).
- **An effect whose key reads a node mounts again wherever the node is
  built** (`remountOnBuild`): a call after the node lets go of the keys the
  bracket holds, unmounting them, and the next settle mounts it. A render
  that builds the node afresh frees the old one, and the new one may be handed
  its address, so a key holding the node compared equal: gtk4's select offered
  a rebuilt drop-down nothing. Such a bracket settles on any write to its
  owner's state, which is what may build the node again
  (`cmd/sngl/testdata/select_rebuilt_slot_runs.txt`).
- **A torn-down scope settles nothing** (`effectGroup.torn`): the teardown
  sets the flag before it ends any bracket, and a settle after it returns. An
  answer reaching a destroyed instance is stale, but a stale answer still
  settles when another bracket keys on what it writes, and that settle found
  the brackets the teardown had let go of wanted and mounted them again: a
  second request, and a render into the box the destroy had freed, which
  crashed gtk4 (`cmd/sngl/testdata/async_query_destroyed_instance_runs.txt`).

On bubbletea the mount's tail is posted back through Update like any other
`async.do` (see the bubbletea platform), so a fetch that waits runs there too
(`testdata/async_query_bubbletea.txtar`,
`cmd/sngl/testdata/async_query_bubbletea_runs.txt`), and so does a query in a
component built per copy of a loop, its effect keyed on the copy and its own
key together (see *Runtime instances* in `design/instances.md`). Kotlin declares no async
native, so on android a fetch answers at once. The interpreter runs `fetch` at
mount, so a test reads the answer as there (`testdata/async_query.sngl`,
and `async_query_loop.sngl` for a query per copy of a loop).

## Contexts

A context is **dynamically scoped**: a read answers the nearest provider the
*running* code is under, and where none is, the default. That includes a read
inside a function — `func show() => "{depth}"` called under `depth(5) { … }`
reads 5 and called outside it reads the default — whether the call is in a
view body, a handler, an effect, another function or the function itself, and
a component's method is a function like any other. A handler runs under the
providers it was written beneath, though it fires after they have unwound.

`passContext` (`internal/lower/context.go`) states it. Every function that
transitively reads a context (`contextFuncs`, reachability to a fixed point)
takes `__ctx_<name>` as a hidden parameter, and every call passes the value
active where it is written — a provider's, the caller's own parameter, the
component's entry value, or the default at a root. A test function is a root
and is not threaded: the harness calls it with the parameters it declares, and
`t.setContext` acts as a provider over the statements after it. The optimizer
does not interpret such a function before the pass (`readsContext`), since its
interpreter would answer the default; after, the call folds from its argument.

**A function value is bound where it is taken.** `var f = show` and a lambda
are what the program holds, and a call through one cannot know which function
it holds, so it has nothing to pass: `funcValueUnder` wraps a named function
taken as a value in a lambda passing the values active there -- its
parameters named afresh (`__ctxArgN`), since spliced into a component with a
prop named like one the lambda read the prop for its argument
(`cmd/sngl/testdata/context_func_value_param_runs.txt`) -- and a lambda's
body is lowered where it is written. The interpreter agrees by capturing the
provided values when a `LambdaValue` is made. Passing the invoker's values
instead would need every function type to carry the contexts, which a host
callback cannot supply. So **taking a function is reading what it reads**:
`callsIn` counts an `ir.Ident` naming a function as an edge beside a call,
or a component that takes one (an adapter's `fetch=lookup`) under a provider
its caller wrote was no reader of the context, was handed no value, and bound
the default (`testdata/context_func_value_under_provider.txtar`).

**Slot content stands under the providers its component wraps the slot in.**
The content is the caller's, so the lowering hands it the provider's value as
the callee wrote it (`collectSlotEnv`) -- a read of the callee's var or prop
in the caller's content. The splice binds those reads as it binds the callee's
own body, by symbol, after putting the content in the slot
(`calleeReadsInContent`): it had renamed the var and bound the prop in the
body only, so `tone(picked) { content }` left the content reading a `picked`
the caller never declared, and the Go targets did not build
(`cmd/sngl/testdata/context_slot_provider_reads_callee_state_runs.txt`).

**A context a directory or scheme package declares is part of what it
exports** (`mergePkgInto`), as a library's is: `p.server(v) { … }` provides
it and `t.setContext(p.server, v)` sets it
(`cmd/sngl/testdata/context_from_directory_package_runs.txt`).

The interpreter is dynamic by construction — a provider pushes onto the scope
for its subtree — so what it has to carry explicitly is what runs after the
mount: a mounted node and an effect keep the values they were mounted under
(`Node.Context`, `MountedEffect.Context`) and a handler runs under them
(`underContext`).
`testdata/context_read_in_user_func.txtar` and
`cmd/sngl/testdata/context_read_in_user_func.txt` are the two halves.

**A context's type is its default's**, and `sngl:builtin` says so:
`component context[V any, F any](default V) F`. The checker reads the default itself
(`registerRootContextDecl`) rather than binding the declaration's parameters,
and holds every provider's value to it. A default whose type says too little
-- `null`, `[]` -- states it in brackets, `context[option[User]] #who(null)`:
the written argument is the context's type and the default is held to it
(`ir.Context.TypeArgs`, which `ir.Convert` prints back).
`t.setContext[V any](ctx V, value V)` takes any context and a value of its type,
and a first argument that is not a context is a positioned error
(`refuseSetContextName`), where it used to fail only when the test ran
(`error_test_set_context_not_a_context.sngl`,
`cmd/sngl/testdata/set_context_typed_runs.txt`). That needed a general fix:
a `sngl:` method whose first parameter is a bare type parameter took its
operand as that parameter, since every operand fits one, so such a parameter
receives the operand only where it is marked the receiver (`receivesOperand`).
