# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Reviewing changes

**Start with `testdata/`.** Every language feature in SNGL has at least one fixture in `testdata/*.sngl` that exercises it. Reading those fixtures is the fastest way to understand what a change is meant to do and to spot gaps. When proposing a feature, write the fixture first and let the test framework drive the implementation. Fixtures use directives like `// ERROR(check) "msg"` and `// FOLD(...)` to assert behavior at specific compiler phases — see `internal/testutil/sample.go` for the framework. A fixture written ahead of the lowering that would let a backend emit it carries `// SKIP(codegen) "reason"` (see `### Test Infrastructure`).

Every fixture is written the way `sngl fmt` writes it, and
`TestTestdataIsFormatted` says so, so add one with `sngl fmt` rather than by
hand. A fixture whose exact layout is the thing under test opts out with
`// NOFMT "reason"`; the directive exempts it from that test only, so do not
run `sngl fmt` over the tree while one is in it. An `// ERROR(...)` directive
names the line it sits on, which is why the formatter keeps a comment on the
line it was written on.

## Function syntax

Two valid forms — no third:

- `func name(params) [Type] { ... }` — block-bodied function (return type is optional only when there's no return value)
- `func name(params) => expr` — expression-bodied function (cannot carry a return type annotation; return type is always inferred)

`func name(params) -> Type` is **not valid syntax** (despite occasional appearances in old docs/specs). The arrow `->` is reserved for func *type* expressions only, and even that usage is being phased out.

## The const prefix

`const` before a declaration says **this is known at compile time**, and so
takes no part in reactivity. Four meanings, one idea:

- `const func` — pure: the value depends only on the arguments.
- `const component` — the render depends only on the props.
- `const name T`, a parameter or prop — the argument is constant at every call
  site, and so is the default.
- `const name component(…)`, a slot — every population is a const render.

`ConstDecl` is left-factored on `kw_const` (a name, `(`, `func` or `component`
follows), `StructBodyDecl` and `EnumBodyItem` take `kw_const FuncDecl`, and
`Param` takes `kw_const ident [Type]`. The flag is `Const` on `ast.FuncDef`,
`ast.ComponentDecl` and `ast.Param`, and on `ir.Func`, `ir.Component`,
`ir.Param`, `ir.Prop`, `ir.SlotDecl` and `ir.Body` (an override's own).

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
  program. Every override in `codegen/**/*.sngl` says `const` for that reason.

**The library says `const` where it means it.** There is no default-pure rule
any more: a bodied library func is seeded pure for its own fixpoint, and a
bodyless one is pure only when it is `const func`. So the builtin methods,
`sngl:seq`, `sngl:ui/draw`'s geometry, the `remote.Value` accessors and the
target helpers that build a host value are marked, and a side-effecting native
(a draw call, a timer, `error.raise`, `async.spawn`) is not — it used to be
pure by default. The `remote.Value` accessors are const for the reason
`list.length` is: each is a function of its argument's current value. Unmarked,
the optimizer stopped treating `contents.value()` as effect-free, declined to
duplicate it into `Result.ok`'s inlined body, and emitted a Go method call on a
native type that has none (`cmd/sngl/testdata/remote_http_build.txt`).

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

A component's block is optional, as a func's is, and the two spellings say
different things:

- `component name(params) [Tree] { ... }` — a body. `{}` is an *empty* body and
  says the component renders nothing, which is a legitimate thing to say.
- `component name(params) [Tree]` — no block at all is a **signature**, and the
  render comes from somewhere the declaration names.

`reportBodylessComponents` requires the second to be true rather than assuming
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
- A platform package's override may carry a **prop selection**, read by the
  same `overrideSelection` a program's override gets. Only
  parens-with-nothing-in-them is skipped, and that form is android's marker for
  a component its own codegen reads by name.

The distinction has to survive the checker, so it is `ir.Component.Bodyless`
rather than `AST.Body.IsDefined()` — that reports whether a block came from
*source*, so everything `ir.Convert` rebuilds looks bodyless, and reading it
as "has a body" made `sngl dump --stage checked` print every empty-bodied
component back as a signature.

## Event syntax

An event declares the parameters its handlers receive, with a func type's
parameter list — `@pick(index int, label string)` — and `ir.EventDecl.Params`
is that list. Three spellings, one mechanism:

- `@pick(index int, label string)` — the list; names are optional, as in a
  func type, and are documentation rather than contract.
- `@change T` — the one-parameter case without the parens.
- `@done()` passes nothing. A bare `@tick` is **not** that: it is one unnamed
  `dyn` parameter, the loose payload a bare event has always carried, which is
  why `ir.Convert` prints an empty list as `@done()`.

A handler binds **by position**, the way a func literal does, and may leave
trailing parameters unbound; binding more than the event passes, or annotating
one with a type other than its position's, is a positioned error
(`bindParams`). An emit supplies every declared parameter
(`checkEmitArgs`) — or **none at all, which forwards**: written in a handler,
`click()` hands on what that handler received, position for position
(`bindEventParams`), and it is how every platform override re-fires its host
widget's event. An event used as a callback (`eventAsFunc`) takes the event's
parameters exactly, or none.

A host widget's event — a DOM click, a Fyne callback, a boundary's `@error` —
hands its handler one value, and `EventDecl.Payload()` is the type the code
dispatching one reads. A parameter list reaches a backend through a *user*
component's event, where passInlinePure substitutes it and
passInstanceEvents turns it into a func-typed prop with that signature;
`testdata/event_params.txtar` and `testdata/event_params_instance.txtar` are
the two routes.

## Loop forms

`for` has one head, and its *type* says what the loop does — the grammar does
not distinguish the forms:

- `for var x = xs` / `for var i, x = xs` / `for var k, v = m` — walk a list,
  iterator or map. `var` is what makes the head a declaration; without it
  (`for xs`, `for seq.count(3)`) the head is the iterable alone and the loop
  binds nothing.
- `for cond { }` — a condition, tested before each iteration.
- `for { }` — no head at all; ends by `break` or `return`.

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
of reach: `map<K, V>` is not assignable to `iter<T>`, but `dyn` holds one and
says nothing about it. `c.funcDepth == 0` is what "in a view body" means; `c.loopDepth` is
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

**`else` means the body never ran.** For an iterable that is "it was empty";
for a condition, "it was false the first time it was asked". A `break` does
not trigger it, since a loop cannot break out of a body that never ran, and
`for { } else { }` is an error because the body always runs.

In an imperative body `passForElse` states that as a flag: `__ranN := false`
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
and `offloadableFuncs` both iterate `ir.Owners(pkg)`, so a window's timers and
its `@error` reach them because the enumeration says a window owns those and
not because each remembered to — each had a *second*, thinner copy of the
window arm beside the `pkg.Windows` one, and `blocks.go`'s passed nil timers
and skipped the `@error` outright. Neither was reachable, since
`passWindowNesting` rejects the only shape that leaves a window a statement by
then; the point is that nothing had to notice.

What *was* reachable is the case neither copy had: **the handlers on a package
var.** Both files named the package's funcs and its body and stopped, while a
component's and a window's vars were walked in both — so a `for … else` in the
`@change` of a top-level `var` reached `passForElse` not at all and every
backend dropped the else in silence. Nothing moves a package's vars off the
package, so the shape survives the whole pipeline;
`testdata/for_else_package_var_handler.txtar` is it.

Both orders are load-bearing and neither is this file's any more.
`passCSE` and `passForElse` name their temps `__cseN`/`__ranN` off `blocks.go`'s
order, and `passAsyncOffload` names `__async_offN` off `offloadableFuncs`', so
each keeps the order it had: a window's `@error` after its view body, and the
declared funcs across every owner before any handler.
`codegen.CodegenCtx.Windows` — the iterator a backend takes when it wants the
windows rather than the owners — reads the same list.
`walk.go`'s `walkPackage` is the one left: it walks the package's funcs, its
components, `pkg.Windows` and `pkg.Body` by hand, where the owners would give
it all four.

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
the third, already had an `*ir.Lambda` arm in its rewriter.

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
taken as a value in a lambda passing the values active there, and a lambda's
body is lowered where it is written. The interpreter agrees by capturing the
provided values when a `LambdaValue` is made. Passing the invoker's values
instead would need every function type to carry the contexts, which a host
callback cannot supply.

The interpreter is dynamic by construction — a provider pushes onto the scope
for its subtree — so what it has to carry explicitly is what runs after the
mount: a mounted node and an effect keep the values they were mounted under
(`Node.Context`, `MountedEffect.Context`) and a handler runs under them
(`underContext`).
`testdata/context_read_in_user_func.txtar` and
`cmd/sngl/testdata/context_read_in_user_func.txt` are the two halves.

## Build & Test Commands

```bash
go install ./cmd/sngl          # build CLI (preferred over go build)
go build ./...                 # verify all packages compile
go tool verify                 # full test suite with coverage
go test ./path/to/pkg/...      # test a single package tree
go test -run TestName ./pkg/   # run a single test
go fmt .                       # format Go (run from package dir)
go tool docsgen                # build docs site to _site/
```

`githooks/pre-commit` refuses a commit over the two `verify` steps that fail for
purely mechanical reasons — `go fix` rewriting a loop, `mdox fmt` reformatting a
paragraph — because neither is visible until CI says so. It checks staged files
only and takes about a second. Install it with `git config core.hooksPath githooks`; `git commit --no-verify` skips it.

WASM build (used by docsgen for playground):

```bash
GOOS=js GOARCH=wasm go build ./internal/playground
```

## Architecture

SNGL is a UI language that compiles to multiple platforms. The pipeline:

```
.sngl source → Parser → AST → Checker → Optimizer → Lower → Platform+Lang Codegen → Output
```

**Public API** is in `sngl.go`: `Parse`, `Format`, `FormatTo`, `FormatExpr`, `Check`, `Convert`. Keep this file intact as the stable surface.

### Codegen Plugin System

Languages and platforms register via `init()` and are looked up by name at runtime:

- **`codegen/codegen.go`** — defines `LangTranslator` and `PlatformGenerator` interfaces
- **`codegen/registry.go`** — thread-safe registration (`RegisterLang`, `RegisterPlatform`)
- **`codegen/lang/`** — language translators (golang, javascript, kotlin), each registers in `init()`
- **`codegen/platform/`** — platform generators (android, bubbletea, fyne, gtk4, html, none), each registers in `init()`
- **`codegen/lang/languages.go`** and **`codegen/platform/platforms.go`** — blank-import all implementations; `cmd/sngl/main.go` imports these to trigger registration

`PlatformGenerator` optionally implements `TestRunner`, `PreviewStyler`, or `Snapshotter` interfaces (checked via type assertion).

### Codegen Models

Platforms choose between two intermediate representations based on their rendering approach:

- **MutationModel** (`codegen/model.go`) — emit a static tree once, then generate targeted `Updater` functions to patch when state changes. Used by HTML and Fyne. Interfaces: `MutationModelEmitter`.
- **RenderModel** (`codegen/model.go`) — re-render the full view from state on every change; framework handles diffing. Used by BubbleTea and Android/Compose. Interfaces: `RenderModelEmitter`.

Both start from `codegen.AnalyzeCommon(doc)` which extracts model fields, computed deps, functions, structs, and timers into a platform-independent `CommonAnalysis`. That struct is read-only to a generator and holds no `*ir.Package`: what a generator *writes* as it emits — helper flags, registered CSS — is `codegen.Emission`, which a platform embeds beside the analysis. Keeping the two apart is what makes `dump --stage analysis` a dump of the analysis rather than of the whole IR graph plus the emitter's scratch space.

### Platform Details

- **html** — two modes selected by `--lang`:
  - `--lang none` (default): static site — one `index.html` per window with inline JS.
    Pages are written from `codegen.Request.Documents` one at a time, and a page's script is written from the package, which holds every page's component factories and render slots. So those are marked as they are written and `pruneDecls` keeps the ones the rest of the script names: written whole, a site of N pages carried N pages' factories in each and built in N² time.
  - any language whose translator implements `codegen.HTTPCompiler` (today: `--lang go`): route mode. html collects windows into `HTTPRoute`s and delegates code gen (mux syntax for dynamic paths, server entry, `main()`/ListenAndServe) to the language via `CompileHTTP`. The platform carries no language- or framework-specific logic. POST actions are emitted only for handlers that transitively call functions imported from the target language (e.g. `go:` funcs under `--lang go`); other handlers stay pure client-side JS. Static mode errors the build if any window has a dynamic href. Two windows whose route patterns conflict -- some path matches both and neither is more specific -- are a build error too, since net/http panics at startup on the second registration (`routesConflict`, held to ServeMux itself by its test).
    Browser testing via CDP (go-rod) is gated behind `//go:build !js` so WASM playground builds exclude it. A `testing_js.go` stub satisfies the interface for WASM.
- **bubbletea** — generates Go TUI code (`model.go`); supports `golang` lang only.
- **fyne** — generates Go desktop code; supports `golang` lang only. Its Go emitter knows three `#[intrinsic]` primitives, which differ only in the children contract a declaration cannot express as data: `Widget` (none), `Container` (a default slot, children attach through a method) and `Wrapper` (a default slot bounded to one, the child is assigned to a field). *Which* Fyne widget one becomes is a `Spec` record passed as a prop — Go constructor, its arguments, the Go type, the import paths, the setter behind each value prop, the callback field and Go signature behind each event. `codegen/platform/fyne/spec.go` decodes it and nothing else in the platform names a Fyne type. Label, Button, VBox and the other twelve are ordinary components in `fyne.sngl` carrying a Spec, so wrapping a widget from a Go module the compiler never heard of is writing a thirteenth — `codegen/platform/fyne/third_party_widget_test.go` is that, done in SNGL alone.

  A value has to reach the emitter through a *declared* prop, since that is what lowering turns into the `node.prop = expr` assignment the translator sees. So the primitives declare a vocabulary of value props by type (`text`, `placeholder`, `number`, `flag`, `options`) and `Setter` binds one to a Go method — the vocabulary grows with the types a setter takes, not with the widget count. Same for `@click`/`@change`/`@input` and `Handler`.
- **android** — generates Android app code; supports `kotlin` and `golang`.
- **gtk4** — generates CGo GTK4 desktop code; supports `golang` only. Widget metadata is parsed at compile time from a `Gtk-4.0.gir`, resolved by `girRegistry` in one place because the code generator used to resolve its own and the two could disagree: `--opt gir=builtin` selects the bundled subset, any other value is that path and a failure to load it is an error, and an empty value probes the system locations (`/usr/share/gir-1.0/` etc.) and falls back to the bundled subset.

  The GIR reaches the platform as two producer outputs in the generated-file store (`girstore.go`, and see *Performance*): `gtk4.registry`, the parsed registry as SNGL data -- everything the code generator reads back per class, recorded against the GIR file and the probe locations before it that were absent -- and `gtk4.widgets`, the declarations derived from it, whose one input is the registry entry. The second is what `PackageFS` serves, directive included, and what `Unavailable` answers from, because the checker asks every platform on every build; the first is decoded only by a build that targets gtk4. A gob cache of the parsed registry used to do the first job on its own, keyed by the GIR's stat and a reflected schema fingerprint -- the compiler's identity in the store's key is what replaced the fingerprint.

  `codegen/platform/gtk4/gir/minimal/Gtk-4.0.gir` is that subset: the ~19 classes `codegen/platform/gtk4/gtk4.sngl` wraps, embedded so a host with no GTK 4 development files can still check, document and generate the stdlib overrides — the generated code needs GTK to *build*, which is a separate matter. It is also what the platform's tests read. Which classes and setter links a system GIR records varies by GTK version, so a test naming host vocabulary asserts GTK's catalogue rather than this platform's behaviour and fails on the wrong machine; the tests assert the parse and merge *rules* over every entry of the fixture instead, each with a guard that the rule was exercised. Adding an override that names a new widget means extending that file, which `TestBundledGIRCoversTheWrappedWidgets` reports.

  Snapshot testing uses `gtk_widget_paintable` + `cairo` (CGo); gated behind `//go:build !js`.
- **none** — no codegen; provides an interpreter-based test runner for headless test execution.

### Key Internal Packages

- **`ir/`** — typed IR produced by the checker. `ir.Package`, `ir.Component`, `ir.NodeInst`, `ir.Expr`, `ir.Stmt`. Phases after the checker work from IR rather than re-reading the source, but IR is not AST-free: an operator is still an `ast.BinaryOp`/`ast.AssignOp`, and `ir.NodeInst` and `ir.VarDecl` keep the `ast.Stmt` they came from for positions and diagnostics. That is why `internal/lower`, `internal/optimize` and every platform import `ast`.

  The AST and the IR divide a literal between them: `ast.LiteralExpr.Raw` is
  the source spelling — a backslash and an `n` — and
  `ir.Literal.Value` is what it stands for (a newline). The formatter reprints
  the spelling verbatim, so the checker decodes on the way in
  (`ast.LiteralExpr.StringValue`) and `ir.Convert` re-spells on the way out
  (`ast.EscapeString`). Decoding in the lexer instead is what let `sngl fmt`
  rewrite `"a\nb"` with a raw newline in it.
- **`internal/parser/`** — lexer, recursive-descent parser, formatter for `.sngl` syntax
- **`internal/checker/`** — two-pass type checker (pass1: register declarations, pass2: validate expressions). Both passes run over a *package*: `CheckPackage` takes its documents together, so a type annotated in one file may name a type declared in a sibling, and `Check` is that function for a single document. One set of registrars serves every tier, and `loadStdlibPackage` runs the same `pass1` — a `sngl:` package and a user package differ in which package a declaration lands in (`declPkg`) and in a few policies that follow from library source not being body-checked, not in how declarations are built or the order they are registered in. What the loader still does for itself are phases rather than second implementations: its own scope, *when* function bodies are checked (pass2 walks a program's declarations, so a library's are driven from the loader — through the same `checkFuncBody`), the purity fixpoint over them, and a target package's component bodies.
- **`internal/optimize/`** — constant folding, dead code elimination with platform/language awareness. The optimizer unrolls no loop, for any target: a language target emits the loop and its own compiler decides whether to unroll one whose bounds it can see — three copies of a Compose `RadioButton` were what the loop is. A target whose view is markup (html, which withholds `viewStatements`) has nowhere to run one, and **`optimize.Documents`** unrolls its loops after lowering, one window at a time: each is a clone of the lowered window with the loops around it bound for its iteration and its constant view loops unrolled, so a site of a thousand pages holds one page's expanded tree at a time (the docs site peaked at 9 GB holding all of them). A loop in a handler or other script stays a JS loop. A const only such a view loop reads is a build value rather than the page's, and shake keeps it on `ir.Package.BuildConsts`, where Documents evaluates it and no backend declares it. A static unroll is bounded (`maxStaticUnroll`) and reports rather than writing a page nobody asked for. A read of a root-package list or map const stays a reference to the declaration (`sharedAggregateConsts`) and folds through it where the value is needed; it is copied only where it becomes storage the program may write (`foldOwned`), so every backend must declare its package consts. html's static mode writes such a const once to `assets/consts/` when it emits more than one page.
- **`internal/lower/`** — capability-driven IR→IR transformation passes, running between optimizer and codegen. Each pass is gated by a `lower.Features` flag. Languages declare their native capabilities via `Capabilities() lower.Features`; platforms combine that with their own restrictions. Passes include: PropBindings, RefLoop, NoTernary, NoLambda, NoReactivity, etc. Ten run always and are not capability-gated because they answer for every target: `SpreadOnce` (a spread's computed operand, which every field read would otherwise evaluate again), `IndexedIter` (a two-variable loop over a pull sequence, which hands out no ordinal), `ForElse` (an imperative for-else, which no host loop expresses), `ViewForElse` (the same construct in a view body, which no platform emitter rendered), `BoundaryFailed` (a boundary's fallback slot, which no platform emitter rendered either), `CSE` (a pure call a statement makes twice), `HoistBodyTypes` (a body-local type whose name another body claims — a component body is not a function scope on any host, so Go and Kotlin need it as much as JavaScript does), `UnprovidedContext` (a context nothing provides, whose constant default is folded into every read — lowered as state instead it is a field nothing writes, and a platform override reading `markup.palette` handed each token a runtime value where a literal was there to be had), `ErrorScope` (a raise resolved against the render tree once each component is spliced where it is rendered) and `ErrorCatch` (the catch block a handler body resolved to a boundary or window becomes, so the raise ends the handler). `CSE` is statement-local and imperative-only on purpose — the temp it binds has to be a statement the target can hold, and a view body on `--lang none` cannot hold one. Entry point: `lower.Lower(pkg, caps, opts)`.
- **`internal/lsp/`** + **`internal/lspcore/`** — Language Server Protocol implementation (hover, completion, diagnostics)

### Stdlib

Stdlib source lives in `lib/<package>/*.sngl`, embedded via `//go:embed` in `lib/lib.go` (exported as `lib.FS`). **Each subdirectory is one importable package: `lib/<path>` is `sngl:<path>`.** Nothing in Go enumerates them — `lib.Packages()` reads the embedded directory, so adding a package is adding a directory.

A package is named for **what it does**, never for who ships it — `std` was a
name of the second kind, which is why everything drifted into it. Within a
package, files are organised by topic and not by declaration kind: a type and
its methods sit together (`lib/builtin/numbers.sngl`, `lib/ui/form.sngl`),
because grouping by kind splits every subject in two.

The tiers, and the split between them is the whole point of the system:

- **`lib/builtin/` → `sngl:builtin`** — the `#[builtin]` types and their methods, plus `output`, the error channel and `root`, the family a package body accepts. The build directive is here rather than in a tier of its own because a package names its own targets without importing anything; `error`, `error.raise` and the `boundary` that catches one are here because a boundary is generic over the tree it was placed in and so belongs to no family — it is the compiler's construct, not a widget. Ambient: dot-imported into every file implicitly, and importing it explicitly is an error. This is the *only* implicit import in the language.
- **`lib/ui/` → `sngl:ui`** — the portable components most applications are built from, the `node` family they belong to, the `window` that is a member of `sngl:builtin`'s `root`, and the vocabulary every one of them refers to: `Style`, the style enums, `measurement`, and the event payloads. Those sit here rather than in packages of their own precisely because every component in every package under `sngl:ui/` names them. Reaches user code only through `import <alias> "sngl:ui"` (qualifies) or `import . "sngl:ui"` (flattens).
- **`lib/ui/draw/` → `sngl:ui/draw`** — `canvas`, the `shape` tree it hosts, and the 2D shapes that are members of it. It is the first *specialised surface* under `sngl:ui/`: a program pays for a drawing canvas only by importing it.
- **`lib/ui/markup/` → `sngl:ui/markup`** — inline rich text: the `span` family, the `richText` node that shows one flow of it, and the bodied block components a document is written in. The second specialised surface; see **Markup and the `md:` scheme** below.
- **`lib/tree/` → `sngl:tree`** — the tree *vocabulary* and no families: the `none` mark that says a component joins none, and `one<T>` for a slot that takes exactly one. A family lives where its members do, which is why the widget family is `sngl:ui`'s `node` and not a `tree.default` here.
- **`lib/build/` → `sngl:build`** — families, and the build-target tree: `family`, the family of families every family is a member of, and `language` and `platform`, the two families an `output` directive's contents are members of. It is a package of its own rather than part of `sngl:ui` because `sngl:builtin` declares `output` and so has to import whatever holds its slot's type; `sngl:ui` is what `sngl:builtin` would then be importing, and it loads before `sngl:builtin` is adopted into the ambient scope, so the load fails on `unknown type "color"`. `sngl:builtin` cannot hold them either, since it already declares `struct platform` as the identity type. An application names it to declare a family (`component block build.family`); otherwise a program writes `output`, and a target package names `build.language` or `build.platform` in its own node's return position.
- **`lib/time/` → `sngl:time`** — dates and the clock: `date`, `time`, `datetime`, the `duration` between two of them, and the `timer` that fires every duration -- an ordinary component, not a builtin node, which is why it carries no `#[builtin]` mark. What it lowers to is each target's answer: html, fyne and gtk4 override it with an `effect` over a start/stop pair of host natives, since such a pair already is a lifetime with a thing to release; bubbletea, android and none still override it with an `#[intrinsic]` node, which stays where it was written -- `codegen.CollectTimers` reads the schedule off it and the platform's view emitter draws nothing for it, so the branch and the component boundary around it are answered by the tree rather than by a gate a pass folded. **An effect hands over a closure, and a closure is only a schedule where the host may run it against live state** — which is what the first two cannot offer, bubbletea because Elm lets nothing outside `Update` touch the model, android because `LaunchedEffect` already is the bracket. `none` is not that case: the interpreter honours `effect` itself, so an override would bracket correctly and schedule nothing, since it owns the clock and finds the node in the rendered tree. None of it is ambient — a program that never asks what time it is never names any of it — which is why all four types moved out of `sngl:builtin`. Loaded at startup even when nothing imports it, because its declarations carry kinds the compiler dispatches on.
- **`lib/math/` → `sngl:math`** — mathematical constants: `pi` and `tau`. A package rather than methods on `float`, because a constant has no receiver and nothing to fold — a zero-parameter static method survived only where the optimizer ran. `float`'s `sin`/`atan2`/`sqrt` belong here too and will move; they are intrinsics with per-language emitters, so that is its own change.
- **`lib/seq/` → `sngl:seq`** — integer sequences: `count`, `range` and `step`, the `iter<int>` a counting loop iterates. Nothing else can produce one, since building a range in SNGL would need a loop and a loop needs a range; a sequence in a loop head lowers to the host's counting loop (`ir.IterCounted`), and anywhere else it is the pull sequence `iter<T>` is spelled as -- `func(func(T) bool)` in Go, a generator in JS, `Iterable<T>` in Kotlin -- so no list is built to iterate one. A list reaching an iter<T> position is wrapped by the conversion the checker already inserts there (`wrapIfNeeded`); a two-variable loop over one gets its ordinal from a counter (`passIndexedIter`), since a pull sequence hands out no index.
- **`lib/async/` → `sngl:async`** — `spawn` and `post`: handing a closure somewhere else to run. They are each other's halves — `spawn` starts work that must not block the caller, `post` brings the answer back to the thread the target draws on — and **each is answered by a different half of the build**, which is why they are two declarations. Starting work is the host *language*'s (`go func(){}()`); reaching the drawing thread is the *platform*'s, because there is no such thread in general. `passAsyncOffload` has always written both for a blocking call on a language that cannot suspend; they are *declared* because a platform package needs to name one — a platform whose own `.sngl` describes a schedule (a timer handing its tick to a host scheduler) has to be back on the drawing thread before it touches a widget, and with no declaration has nothing to write but that platform's own spelling of `fyne.Do`, in its own package. A generator reaching its own thread from hand-written Go needs no name; a platform *package* does. That is the line against `ir.NodeOps`, which stay undeclared precisely because nothing can name them. A target that answers neither is refused at the call by `passAsyncCapable` rather than emitting code that does not compile: `async.post` on bubbletea used to come out as `m.post(...)`, a method on the model that does not exist.
- **`lib/dialog/` → `sngl:dialog`** — `Alert` and `File`: host-native modal surfaces. Not components — a component is placed in a tree and rendered, whereas `Alert.confirm` hands control to the host and returns what the user chose.
- **`lib/test/` → `sngl:test`** — `Test`, the receiver a test function's first parameter carries.
- **`lib/i18n/` → `sngl:i18n`** — the translation surface `$"..."` lowers to.
- **`lib/macro/` → `sngl:macro`** — the public mark vocabulary a package writes to describe its own declarations (`foreign`, `wildcard`, `construct`). Only the vocabulary: `sngl:platform/<name>` and `sngl:language/<name>` are not under `lib/` at all — a target carries its own package, described below.
- **`lib/x/gen/` → `sngl:x/gen`** — what a target package says about what it generates: `#[gen.can]` and `#[gen.cannot]` name the SNGL constructs it emits natively, `#[gen.wants]` the lowering passes it asks for. Written on the build-tree node the package already declares (`component go(…) build.language`), and read by `codegen.CapsFor` into `lower.Features` — there is no `Capabilities()` method, because a target that is a command rather than a linked-in package cannot answer one. **A capability not written is not held**: there is no base set to subtract from, so a target that says nothing gets every lowering pass. That is what lets the language grow — a new construct arrives with a lowering pass that converts it away, and every target that has not heard of it keeps working unedited, opting in only when its code generator can do better than the pass. Under the other polarity, silence would mean "I emit this" on every declaration written before the construct existed, so adding one would break every target at once. A plugin answering nothing, or answering a vocabulary older than the compiler asking, is the same argument with a version skew in place of a new construct. A platform overrules a language per capability, which is what lets html take back the `ternary` Go withdrew; naming one both ways on one declaration is an error. Not `sngl:build`, whose audience is the same: that package is the build-target *tree*, and what generating for a target involves is a different subject — `sngl:x/gen/cache`, the inputs a generated file records (see *Performance*), is the second member. Where a mark may be written is checked in `finishTreeMarks` rather than in the handler, since a mark applies while its declaration is still registering and `Component.Tree` is read after that.
- **`lib/internal/` → `sngl:internal/<name>`** — the compiler's own tier, importable only from lib source.

A package documents itself with a **package comment**: a run of line comments
at the top of its `doc.sngl`, separated from what follows by a blank line
(without the blank line it documents the declaration below it instead). The
text is markdown, and `sngl doc` renders it as the package description — so
adding a `lib/` directory with a package comment needs no code change.

**Only `doc.sngl` answers** (`checker.PackageDocFile`, enforced in
`checker.PackageDoc`), which is where Go's semantics are dropped: every file's
comment counting, in a load order nothing guarantees, published whichever file
header a directory listed first. It is also what keeps a stored file's
`// Code generated … DO NOT EDIT.` header, which gtk4 serves as part of its
package, out of that package's description. A leading comment in any other
file is an ordinary comment about that file. Target packages under `codegen/`
follow the same rule, each with a `doc.sngl` beside its source.

Packages import each other — `lib/ui/draw` is written against `lib/ui`, and `lib/ui` in turn against `sngl:time` and `sngl:tree` — so they load lazily and memoized (`libPkg`), not in directory order. A lib package qualifies its dependencies rather than dot-importing them: lib source is registered into the checker's own symbol table, so a name it lifted would be indistinguishable from one it declared and would be re-lifted by a dot import of it. User packages do not re-export a dot import; lib packages must not either.

A `#[builtin("kind")]` mark says which IR construct a declaration dispatches to, **not** which tier it lives in nor what the declaration is called — the builtin visual nodes are spread across tiers, `window` in `ui`, and `effect`, `context`, `output` and `boundary` in `builtin`. `boundary` is the case that makes the second half plain: it carries the `errorBoundary` kind, because the kind names the role and `ir.ErrorBoundary` is the construct it dispatches to, while the name a program writes is the declaration's own.

`internal/checker/stdlib.go` parses the library at startup. User declarations shadow stdlib ones. Platform-specific component implementations live in that platform's own package; its source imports the stdlib under an alias and overrides through it (`import ui "sngl:ui"` + `component ui.vbox`), and the prefix is that alias, not a fixed name. The override names the target it implements by the package's own build-target node, unqualified — `component ui.vbox[platform]`, not `[android.platform]`: inside the package that declares it, saying the package name would say it twice. A program outside the package writes the qualified form, `[html.platform]`, because that is how the node reaches it.

**A target is its build-target node.** A platform package declares `#[gen.name("html")] component platform(…) build.platform`, a language package `component language(…) build.language`: the node is named for its tier by convention, and `#[gen.name]` is the string the CLI, the Go registry and an `output` block's bare `html` match on (`ir.TargetNode`, `GenCaps.TargetName`). The same declaration is the option schema, carries the `#[gen.can]` marks and is the identity: `[platform]` and `[html.platform]` resolve to it (`resolveTargetIndex`), and read as a value it has sngl:builtin's `platform` type (`targetValueType`), folding to its name in the optimizer and the interpreter, so `PLATFORM == html.platform` is unchanged. There used to be a generated `const platform = "html"` mounted into every target package (`identityDoc`, `#[macro.identity]`), a second declaration that could disagree with the first. A build-target node without the mark is an error, as is the mark anywhere else, and so is a name another node of the same tier carries (`reportTargetNames`, run last over every loaded package, library nodes ahead of the program's so the program's is the one reported) or a target package's node naming anything but its own package: `none` may be both a language and a platform, and nothing else may be two things. Diagnostics name a node by `Component.DisplayName`, its target name, since every platform's node is called `platform`.

**A target carries its own library package.** `sngl:platform/<name>` and `sngl:language/<name>` are served by the registered plugin, not read out of `lib/`: a plugin implements `PackageFS() fs.FS` and the checker reads whatever it returns (`ProvidedDocs`, and `libDocs` which appends it to the embedded tiers). The source sits beside the plugin — `codegen/platform/html/html.sngl`, `codegen/lang/golang/golang.sngl` — and is embedded there.

The point is that the checker does not know where a package comes from. gtk4's `PackageFS` returns its embedded overrides merged with one component declaration per GTK widget class, generated from the host's introspection data; nothing outside `codegen/platform/gtk4/packagefs.go` knows half of that package did not exist a moment earlier. The same interface is what will let a plugin outside this repository answer over an RPC.

A target that cannot serve its package returns nil and contributes none, which is a whole-package decision on purpose: `mergePlatformExtensions` walks *every* registered platform's source, so a platform serving overrides whose widgets it cannot also declare would report them as undefined in a build targeting something else. gtk4 does this when no introspection data resolves.

The `#[builtin]` mark only stamps the kind on whichever IR the declaration
became. What a kind then *requires* — that a node kind names a component, that
a const kind names a const — is checked by `bindBuiltinRole`
(`internal/checker/builtins.go`), where the compiler stores the reference,
because that is where the requirement comes from. A duplicate mark is an error
there rather than a silent overwrite. Which declaration forms may carry a mark
at all is the AST's answer: a form implements `ast.Attributed`, and the parser
refuses a mark on one that does not.

**Built-ins are declared, not hardcoded.** The compiler identifies a built-in by
a `#[builtin("kind")]` mark on its `lib/` declaration, never by matching its
name — so every built-in is shadowable by a user declaration of the same name.
Type kinds (`int`, `color`, `datetime`, `list`, `option`, …) mark a struct;
node kinds (`window`, `errorBoundary`, `effect`, `context`, `output`) mark a
component, and the checker dispatches a visual node to the matching IR construct
off the mark. Four tree kinds mark a *family* — itself a component, but one
that is never rendered: `treeFamily` (the family of families), `treeRoot` (the
family a package body accepts), `treeNode` (the widget family) and `treeShape`
(the drawing family). Each is a family the compiler itself has to name — to
know what a family is, to check the body against, to tell an ordinary component
from a rendered one, to know which node is a canvas — and every other family it
compares by declaration alone. Marking them is what keeps `ir.go` from holding
a package URI and a name for each: `ir.IsUITree` and its siblings read
`Component.Builtin`, so a family may be renamed or moved and a program's own
`shape` family does not become the painted one.
The mark is declared in `lib/internal/marks` and implemented in
`internal/checker/marks_impl.go`; kinds are `ir.BuiltinKind`.

**Macros are not ambient.** A macro package is imported like any other:
`#[tree.none]` needs `import "sngl:tree"`, and `#[marks.builtin("...")]` and
`#[marks.intrinsic("...")]` need `import marks "sngl:internal/marks"` — which
is why every `lib/` file carrying a mark declares it. The alias is an
ordinary file-scope binding, so the mark follows it: `import t "sngl:tree"`
means `#[t.none]`.

**Write the qualified form.** A dot import stays legal and supported — with
`import . "sngl:internal/marks"` the mark is the bare `#[builtin("...")]` —
but the repository's own source no longer uses one, so that what a reader
learns from is the qualified form. The alias is the package's last path
segment (`marks`, `ui`, `seq`, `draw` for `sngl:ui/draw`). `sngl:builtin` is
unaffected: it is ambient rather than dot-imported, and it is how `int`,
`string` and `color` are named.

A lib package may carry macros alongside its declarations — `sngl:tree`
ships the `none` mark next to the `one` count — so the
`sngl` scheme is checked against the `lib/` layout alone: a directory is what
makes a package exist, macro-only ones included. `sngl:internal/<name>` is
the compiler's own tier: a package there may contribute macros, declarations,
or both. `internal/marks` declares only macros; `internal/ir` declares `Macro`,
the type a macro returns, and the flag enums the marks take.

**A family is a declared component, a member of `sngl:build`'s `family`.**
A segmented component tree is one whose members are not interchangeable
widgets, where a container accepts only its own family. Drawing is the first
user, rich text and menus are the next, and nothing in the mechanism knows what
a shape is.

```sngl
component shape build.family

component circle(…)                          shape {}   // is a shape
component canvas(shapes ...component shape)   ui.node {}   // hosts shapes, is a widget
component group(children ...component)        shape {}   // is one, and hosts its own family
```

The **return position says what a component is**; what it *hosts* is its rest
slot's type, which is how a member hosts a different family. A slot
naming no tree accepts the one its component belongs to, so a member hosts its
own without saying so — but declaring a slot at all is what makes it host
anything. Naming something that is not a tree in the return position is an
error: a children contract is a slot's to declare.

The family is the *declaration*, not its name, so a misspelling is an
unresolved name where it is written, and two packages each declaring a `shape`
family declare two. `ir.Component.Tree` points at it, and `Component.IsFamily`
is "a member of the family of families", which is the one declaration that is a
member of itself (`#[marks.builtin("treeFamily")]` on `build.family`, where the
regress stops). A family takes no parameters and has no body — a body is where
how a family is generated will be written, and nothing reads one yet, so it is
refused rather than dropped — and writing one in a tree is an error rather than
a membership mismatch. It is exempt from the bodyless-component rule for the
same reason.

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
bodyless host it cannot render) walks each host written at the root of a file
into data: a member with a `gen.node` override is a node, a member with a body
composes -- its body read with its props bound, its rest slot inserting what it
was written with -- and an `if` or a `for` is decided there, so every value is a
build-time one and a read of state is refused at the read. The host is then
removed from the package, and a runner turns the tree into the target's extra
file. The walk and the runner are separate on purpose: the tree
(`emittedNode`) carries no IR a process could not be handed, so an emitter that
runs another program takes the same data. Where `gen.emit`/`gen.node` may be
written is `reportEmitterPlacement`; what a build refuses is
`cmd/sngl/testdata/emit_family_refused.txt`, and `testdata/emit_family.txtar`
is the code. What is not here yet: content that reads state, which needs the
emitter to write host-language code (`p.expr`) rather than text, and a host
anywhere but the root of a file.

**`sngl:ui`'s `node` is the widget family**, and it is a family like any
other: naming it in a slot accepts widgets and nothing else, which is what
makes `vbox { circle(…) }` an error. It is named for what a member *is* — a
member of `draw.shape` is a shape, a member of this one is a node — which is
also what keeps `ui.ui` from appearing in every declaration that spells it. It used to be `tree.default`, and the default
tree was not a family at all — `checkTreeMembership` opened with `if want == nil { return }`, so membership was enforced *into* a named tree and never *out
of* the unnamed one. `sngl:tree` keeps the `kind` mark, the `none` mark and
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

`inferComponentTrees` is that, and it is a **fixed point** rather than one
ordered pass, because the evidence may be a declaration whose own family is
unsettled and two declarations may be mutually recursive. It runs in two
rounds: settle everyone whose evidence is complete, repeatedly; then settle
what is left — necessarily a cycle — from the evidence that did resolve, so
`a` renders a widget and the `b` that renders an `a` is one too.

**Every membership check waits for it.** `checkTreeMembership` opens with
`if want == nil { return }`, so a check that ran while its subject was still
unsettled would compare against no family and pass in *silence* — the same
nothing an unrestricted position reports. So the six call sites record a
closure (`deferTreeCheck`) and `runTreeChecks` drains them once every body has
been read, which is why the function takes the component the content was
written in rather than reading `c.currentComponent`.
One fixture per position holds that, each naming a declaration written *below*
it: `error_tree_inferred_late.sngl` for a node's bare children,
`error_tree_inferred_positions.sngl` for four more,
`error_tree_inferred_treeless_contains.sngl` for the tree-less rule, which
reads an inferred family too, and `error_tree_component_body.sngl` for the
body. Move the matching check back inline and the fixture passes clean rather
than failing — which is how each was confirmed.

**A component's own body is the sixth position**, and it is the one nothing
else asks about: the other five are a container asking after its children,
which leaves what a declaration itself puts in the tree unmeasured. So
`component c ui.node { window … }` type-checked, and every backend then
swallowed the window *and its siblings* without a word (#214) — five golden
archives in this repository were written that way. The check is
`checkTreeMembership` against `comp.Tree` at the end of `checkComponentBody`,
which makes `output` and a `#[tree.none]` component exempt for free: both carry
a nil `Tree`, so the first is never asked and the second is left to
`checkTreelessBody` rather than reported twice.

**Four constructs are reached through, and `treeTransparent` is the one list
of them.** An `if` and a `for` say when and how many; an `ir.ErrorBoundary`
says what happens when a raise reaches it; an `ir.ContextProvider` sets a value
for what is under it. None puts anything in the tree itself, so every tree
question asked of a block is asked of theirs.

It returns **two** lists. `all` is every block, for the callers that ask whether
content belongs to a family. `binds` is the blocks that may *supply* one, which
is every block but a boundary's fallback: that stands where the content stood
and is held to the content's answer rather than giving one. `childrenTree` is
the only caller of `binds`, and handing it `all` made a diagnostic depend on
source order — `T` bound to `shape` off the fallback of a boundary whose content
was empty, and the widget beside it was blamed
(`error_tree_boundary_failed_binds_nothing.sngl` is both orderings). A boundary
with no content still takes `T` from its fallback as a last resort, in the
boundary's own check where that rule belongs.

There were **five** copies of that walk and each was missing a different
member, which is why the list is now a function rather than a `switch` per
caller:

- `checkTreeMembership` walked `if`/`for`. A boundary is the subtle one — its
  own check binds `T` off its content and holds the rest to that, so reaching
  through it is what holds the `T` it settled on to the family the surrounding
  *position* accepts. Without it, `vbox { boundary { circle(…) } }` passed in
  silence.
- `treeEvidence` is the same question from the other side, and missed the
  provider: a component with no return position whose body was
  `theme("dark") { ui.text(…) }` rendered nothing the inference could see and
  was refused as a body that names no tree — a correct program refused.
- `checkTreelessBody` walked `if`/`for`, so a `#[tree.none]` component rendered
  a widget under either wrapper and passed.
- `childrenTree` and `insertsSlot` each had their own partial copy.

Two of those gaps were found one at a time, each as a silent acceptance; the
third is what made the list shared rather than corrected a third time. Fixtures:
`tree_inferred_context.sngl` (inference), `error_tree_component_body.sngl`'s
`scoped` (membership) and `error_tree_treeless_wrapped.sngl` (tree-less, both
wrappers). Deleting the `ContextProvider` case from `treeTransparent` fails all
three, which is the unification doing its job.

A **sixth** copy was `internal/checker/effects.go`'s `walkVisualErrors`, which
resolves a raise to the nearest boundary. It had no provider case at all, so a
handler written under one resolved past every boundary around it and the error
came out uncaught — `sngl test` reporting `raised: boom` for a program that
wrote a boundary. It keeps its own boundary case, because a boundary is the one
transparent statement that is not: it pushes its handler onto the scope, which
is the whole of what it does. Its fallback is walked under that same handler,
since `passBoundaryFailed` puts the fallback exactly where the content was.
`cmd/sngl/testdata/error_under_wrappers.txt` covers both halves on the interpreter. A `testdata/*.sngl` fixture's test functions are run as well, on the interpreter only, by `TestRunFixtures` (`codegen/platform/none/testrunner`): every fixture that declares one is executed unless it asserts a failure before the run.

Two of those deferrals are subtler than the rest. The tree-less check captures
the body it was asked about instead of re-reading `comp.Body`, because
`checkPendingExtensions` swaps an override's statements onto the declaration
and restores the base body after: read late, it checks the base body once per
registered override and the override's body never. And `CheckLibPackage` has no
pass2, so it drains the checks itself — without that, nothing checks a library
body's membership at all, silently: `sngl doc`, the LSP's lib path and
`sngl check sngl:platform/fyne` alike.

A **library** declaration names its family and is not inferred. Only the
target tiers have their bodies checked at load, so for most of `lib/` there is
nothing to read an answer off — and a package's declarations are a published
contract, which a body should not be quietly restating.

**`#[tree.none]` says a component belongs to no family**, which is what a
component that renders nothing wants — `effect`, `timer`, `context`, and each
platform's `Timer` primitive.

**A canvas keeps them and draws the rest.** `passShapeDraw` replaces a
canvas's shape children with the statements that paint them, in place, and
`emitShape` passes a tree-less node through as a child rather than painting it:
it paints nothing, and its lifetime is the canvas's, so `passEffect` finds it
there as it would under a vbox. Under `emitShapes` an `if` and a `for` are
rebuilt around what they held, so a bracket under one survives too — which is
where a `timer` lands, its override being `if enabled { effect(…) }` by then.
The pass it replaced hoisted the shapes into a function and cleared `Children`,
so the bracket went with them and a canvas that scheduled its own animation
compiled clean and never moved. `testdata/canvas_schedules_itself.txtar` holds
it.

Two rules follow from the mark, and they are each other's halves:

- it may be placed in **any** tree, so a lifetime bracket belongs in a drawing
  as readily as in a layout (`checkTreeMembership`);
- it may contain a member of **none**, because a body that rendered a widget
  would have joined that family without saying so, and would then be
  placeable in a canvas (`checkTreelessBody`). The rule reaches through
  everything `treeTransparent` lists, so a widget wrapped in a boundary or a
  context override is still a widget this component renders.

**A wrapper whose family is whatever it was handed says so with a type
parameter** — `component boundary<T>(@error error, content ...component T, failed component T) T`. Nothing at a call site names a type argument and nothing
needs to: a slot's content *is* an argument, so the children bind `T` — the
first one that belongs to a family says which, and the rest are held to it
(`slotWant`, `childrenTree`). An empty body binds nothing and leaves `T`
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
  posted back through `async.post` (`error_catch_async_offload.txtar`). The interpreter's `dispatchRaise` returns
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
answer. And a raise in a `var`'s `@change` is resolved by neither.

A call in **expression position** that carries its own `@error`
(`v = risky(7, @error(e) { … })`) is refused (`refuseExprErrorHandler`): the
handler would replace the rest of the statement, so the call has nothing to
evaluate to. And a call through a **func value** with no `@error` of its own is
never fallible, since nothing about a func type says whether it raises; a
lambda body's raise leaves the lambda.

**`sngl:builtin`'s `root` is the family a package body accepts**, and that is
the whole of what makes a window top-level — no syntactic rule names the
construct. So a `node` at the root of a file is the ordinary
tree-membership error, an `if` or a `for` there still works (neither is a
node), and a component that names `root` itself renders windows, which reach
the build when something instantiates it.

It is **ambient** because it describes every package rather than widgets. It
lived in `sngl:ui`, and that forced an exemption: `output` is declared in
`sngl:builtin`, which cannot import `sngl:ui`, so `treeOptional` let the
directives — `output` and `cache.inputs` — omit a return position. Both now
name `root` like `window` does, and `treeOptional` answers only for an
extension body. Load order is not a problem: `lib/builtin/root.sngl` imports
`sngl:tree` for the mark, which `sngl:builtin` already reached through
`sngl:build`, and nothing in `sngl:tree` needs the ambient scope. `ir.Convert`
spells it bare, as it spells every ambient name.

What membership does *not* replace is the directives' own rule: each is
diverted in `registerRootVisualNode` and read once, before anything runs, so
one written anywhere but the root of a file is still its own error ("output may
only be written at the root of a file") rather than a family question. A
component whose family is `root` could otherwise render one, and nothing would
read it.

**Which windows there are is `ir.AllWindows`**, and the field is only half the
answer. The checker registers a window written at the root of a file on
`pkg.Windows`; one under an `if` or a `for` there stays a statement in
`pkg.Body`, and one a component renders stays a statement in that component's
body. `ir.AllWindows` reads `ir.Owners`, which reports all three deduped by
pointer, and the lowering, every platform and the checker's own entry-window
lookup ask it rather than the field. A lowering pass used to lift the second
and third onto the field before anything else ran, on the argument that two
dozen passes and five platforms already walked it -- and paid for it by
clearing the bodies it emptied, which dropped every statement in them that was
not a window (`testdata/timer_at_package_root.txtar`).

**A window is an `ir.NodeInst`**, and `ir.Window` is an alias for it rather
than a type. The name is kept because "which of these nodes is a window" is a
question nineteen consumers ask and `*ir.Window` is what they have always
spelled the answer as — but it buys no type safety, a plain vbox satisfies it,
and `ir.IsWindowNode` is the actual test. That predicate reads the
`#[builtin("window")]` mark off the declaration, never the name, for the reason
every other builtin lookup gives.

What the separate struct cost was **72 `case *ir.Window:` arms** across the
lowering, the optimizer, the interpreter and four platforms, each a second
answer to a question the `*ir.NodeInst` arm beside it had already answered.
Forty-eight were a strict subset of that arm; the rest are `ir.IsWindowNode`
guards at the head of it now, next to the code they except — a window is its
own reactivity owner, is not flattened into `CreateNode`, allocates no element
var for its id, and may not appear in a view tree, which is what the `panic`s
android, bubbletea, html and the interpreter keep.

Three fields ride on `NodeInst` and are nil on every other node —
`ErrorHandler`, `Params`, `LocalRefs` — which is the price, and it is three nil
fields against 72 arms. None is a *body owner*: `Vars` and `Funcs` stay off
`NodeInst`. `Checked` did not come along at all, being the checker's
bookkeeping about its own progress and so a set there.

**And a window's props are the declaration's, not the compiler's.**
`lib/ui/window.sngl` declares `title`, `href` and `favicon` like any other
component declares a prop, so they are the `Props []Arg` any node carries and
`Component` is the declaration they were measured against. Naming the three as
Go fields cost 22 files a hardcoded triple, and two of them — `buildWindow` and
the window's own convert path — a hand-maintained list that had to agree; a
fourth prop would have needed every one of them edited before it reached a
backend. What a Go consumer still spells is `ir.WindowTitle` and its two
siblings, which name the *prop it reads* rather than redeclaring one: html asks
for the href, gtk4 for the title, and neither is a list of what a window has.

**A window's `#id` binds a node handle**, and it is `NodeInst.ID` like every
other node's. `declareNodeID` had the split written out: every node id bound an
`*ir.Var` marked `NodeHandle`, and `if isWindow` bound the window itself
— so a window was an `ir.Symbol` and "what does a node id name" had two
answers. It binds the same handle now, `Handle` points at it, and
`SymName`/`SymType` are gone, so the compiler refuses any attempt to declare a
window as a symbol. That is what found the three consumers rather than leaving
them to a grep: `output(entry = home)` matches by handle and falls back to the
name for a window a component renders, folding `home.title` reaches the window
through `ir.WindowForHandle`, and `hoistedWindow`/`bindWindow` are deleted —
the handle is the stable thing a reference resolves to, so `buildWindow` builds
a fresh window every call. `ir.Window.Typ` went with them, having only ever
answered `SymType`. The window's `Name` is the target the program wrote
(`ui.window`) and its `ID` is the `#id`, which is the one rename the type merge
could not have the compiler check: both fields existed, and reading the wrong
one compiles.

**A read off a window's id folds to the window's own prop expression**, and it
is folded again against the context the *read* sits in rather than the one the
prop was written in. That is not a detail: `window #page(title=it.title)`
inside a `for` puts the loop variable in the prop, and a read of `page.title`
from the window's body sits where the unroll has already passed -- returned as
written it stayed `it.title`, named nothing, and the page rendered an empty
span with no diagnostic. Which makes a prop that reads itself,
`window #h(title = h.title)`, a fold that re-enters on the same prop forever,
so `evalCtx.foldingProp` holds the pairs in flight and leaves the select
standing on re-entry -- the state a prop with no answer already reached codegen
in. Keyed by window *and* prop, so two windows naming each other terminate on
the second key rather than looping on the first.

**A node may not read its own `#id` in its own arguments**, and that is now a
positioned checker error (`reportSelfReferentialProps`, `internal/checker/selfref.go`).
The id names the instance the argument list is building, so `window #h(title = h.title)` asks the title for the title. All three forms of a node
that carries an id are held to it — a window, an element-ref call
(`text #t(value = t.value)`), and the `context` declaration, whose id *is* its
own declaration, so `context #depth(depth)` is the same error rather than an
`undefined` naming the context on the line declaring it. Matched by *symbol*:
`declareNodeID` declines to bind an id an outer scope already holds, so a
`#foo` written beside an existing `foo` reads that one, which is an ordinary
read of something that does exist. A handler is untouched — `@click` is split
off before the rule is asked, and a handler runs after mount.

What it does **not** cover is the two cases where no single declaration reads
itself, and both keep the guards they had. **Mutual reference** —
`window #a(title = b.title)` beside `window #b(title = a.title)` — reaches
`evalCtx.foldingProp` by a second key and is still left standing, so the page
comes out with content and no `<title>`, silently. And `const a int = a` is the
same shape one layer down in a scope of its own: `sngl check` accepts it and
always did, because the checker never folds, while `sngl generate` used to
crash — `evalIdent` and `evalExpr` calling each other until the stack went,
with no position and no message
(`cmd/sngl/testdata/const_reads_itself.txt`).

So the general rule — a *declaration* that reads itself, however many hops
round — is still not made, and both guards are still the survivable answer
rather than the right one there. `cmd/sngl/testdata/window_prop_reads_itself.txt`
holds the two halves apart: the self-reference refused with a position, the
mutual pair still emitted with nothing said.

**A window owns no state**, which is why `Vars`, `Funcs` and `Timers` did not
come along either. A window is a rendering root and not a storage level, so
what its body declares belongs to its container — the package, or the
component that renders it: the checker leaves a window's `var` as an
`*ir.LocalVar` statement in the body and `passHoistState` moves it there, and a
`func` at the root of a window body is registered on the container directly.
`ir.Owners` reports a window with neither. `Timers` is **gone**, and with it
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
`internal/build.Emit`, which asks `ir.Package.IsProgram`: reachability from the
package body through the components it instantiates, not membership in any
body. Asked the other way, a file carrying a spare root component built, and
fyne and bubbletea then met an `*ir.Window` in the middle of a component method
and panicked.

The alternative was a lowering pass that scanned `pkg.Components` for the root
family, lifted the windows onto `pkg.Windows` and hoisted the vars and funcs
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
gtk4 each put it in one Model — and a target whose windows are separate
documents copies it, html writing its own `state` into each page. That divergence is the point
rather than a gap: two pages *are* two states and one process *is* one, and
forcing either way round in the lowering would be the language overriding the
platform it compiled to. The author picks a target knowing it. Shared top-level
state is mainly a performance tool, not the default way to write an
application — a declaration belongs in the window that uses it unless there is
a reason it does not.

Two things that follow. **One Model, one cell:** the shared pointer reaches a
Model through two owners, so `CodegenCtx.ModelState` dedupes by the `*ir.Var`
— emitted per owner instead, the Go targets declared `hits int` twice and did
not compile. And **reading it needs a scope**: `EntryWindow` declines to say
which window a multi-window program's single Model is scoped to, rightly, since
two windows' `count` are two names — but a declaration owned by *all* of them
is one var reachable from each, so `sharedWindowScope` scopes to those without
choosing between windows. Without it every such read rendered as a bare
identifier the emitted Go never declared, beside the Model field it should have
projected onto.

`testdata/root_component_state_two_windows.txtar` is the fixture, and it is
where the per-platform table is written down.

**A program declares at least one window**, checked by `internal/build.Emit`
rather than by the checker: `component c { … }` on its own is a perfectly good
thing to type-check, and it is only as something to *run* that it has nowhere
to draw. Which is why **`component main` has lost its harness convention** —
`CodegenCtx.RootDecl()` now answers only for a harness that has cleared the
windows on purpose (the test launcher isolating a component), and a `main` in
an ordinary program is an ordinary component. Nothing else asks for the name
either, and each place that did was a behaviour: `sngl run --lang none` mounted
a `main` instead of the windows (`BuildProgramEnv` mounts the program now), a
directory of files each declaring one was read as a corpus of programs rather
than a package, a library declaring one could not be imported, and route mode
gave a `window #main` the index's `/` beside the first window's
(`route_window_named_main.txtar`). Route mode and html ask
`ir.Package.RootDecl()` for a harness root. `output(entry = home)` names the
window a build opens at, by element reference so a typo is a name nobody
declared; it completes the gap `codegen/codegenctx.go` already admitted to,
where one window was scoped implicitly and two or more got no scoping at all.

The facts land on `ir.Component.Tree` at registration and on
`ir.Package.TreeKinds` — keyed by declaration — for the lowering passes to gate
on. A drawing rule rides along there: a painted shape declares no events, which
`finishTreeMarks` enforces for every member of `sngl:ui/draw`'s tree, because
membership is the return position and no mark is written to opt in. Nothing
about a mark reaches the AST: the source carries the `#[...]` as written and the
checker applies it where it registers the declaration.

**A slot is an ordinary parameter whose type is a component type**, declared in
the parameter list beside the props and events, so a component's whole API is
one list. `slot` is not a keyword. The type carries the whole of a slot's
contract: `component(Row)`'s parenthesised list is what the slot is *invoked*
with at its insertion point, and the trailing type is the tree it *accepts*,
wrapped in whatever bounds the count — bare is any number, `tree.one<T>`
exactly one, `option<T>` none or one. Bare `tree.one` is a count and no
family: "exactly one of whatever this slot already accepts", which is the
tree the component declaring it belongs to. It used to say that through a
defaulted type parameter, `one<T = default>`, and there is no default family
to point one at any more.

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

`lib/ui/window.sngl` is the user: a window's route parameters arrive as one
struct value in the `params` prop, `T` is inferred from it, and the body that
reads them is the population of the window's `content` slot. That is what
makes a path a plain string rather than an interpolation — the names in
`/p/{pkg}` are the struct's fields, not identifiers in scope. Before it, the
checker read the placeholders off the href and synthesized an `*ir.Var` per
name: a placeholder and a node `#id` shared one namespace with nothing
declaring either, so which one a body's `pkg` reached fell out of scope-push
order; nothing could say a parameter was anything but a string; and a
misspelled placeholder declared a var rather than being reported.
`checkWindowPathParams` asks the last two now, holding every `{name}` to a
field of the struct and that field to a type a route can parse text into.

`NodeInst.Params` is the cell it lands in, and it is the `*ir.Param` the
population declares — an ordinary slot binding, because there is no
distinction for the checker to make. That a target *stores* it is codegen's
answer: `CodegenCtx.ModelState` is "which bindings a single-Model target puts
in its Model", and an `ir.Symbol` rather than an `*ir.Var` for exactly this
reason — not everything stored is a declaration a body made. `OwnedVar`
answers the five questions the four Model emitters ask, of which a parameter
answers `Name` and `Type` and is neither const nor synthesized, and whose
`Init` is its type's zero because nothing in the program writes one. A Go
route handler binds it from the request (`writeRouteParamBindings`); a target
with no request renders that zero.

Two things follow from the zero being codegen's. The shake roots the window
*node* rather than its children, so `Params.Type` and the `@error` are
reachable — the struct a route's parameters name is otherwise declared and
never constructed, and went. And the window that writes no population carries
no cell at all, so nothing downstream binds a route parameter for a page that
does not read one.

**A window's body reaches `checkSlotPopulations` like every other node's**,
and `w.Params` is that population's own parameter. It used to read its own
population out of the block: sixty-five lines restating "no such slot",
"already populated" and "populated by name and bare", and missing the ones it
did not think to restate — slot arity, and a population naming an override
target, which `testdata/error_window_population.sngl` pins. What is left of
that peel is `windowBodyBlock`, which answers only *which lines* the body is,
and exists because a window hoists its own node ids before the body is read.

**And a window is built in pass2**, like every other node. `windowShell` is
what pass1 reserves — the target name, the `#id`, the handle — because
`output(entry = home)` and a sibling window need something to resolve against
before any body is read; `checkWindow` reads the props, the `@error` and the
body. Building the whole thing in pass1 checked its arguments against a scope
pass1 had not finished filling, so `window #home(title = greeting())` above
`func greeting()` was `undefined: greeting` while the same window one level
into a component body checked clean
(`cmd/sngl/testdata/window_prop_reads_a_later_decl.txt`). One local
specialization also means the bound `T` needs no carrying, which is what the
params cell used to be for.

What is still the checker's alone is the **id scope**: a window pushes one and
hoists its own `#id`s into it, which `declareNodeIDsStmt` says by not
descending into a window, and which `isWindowNode`'s own doc calls the reason
it exists.

**`...` and a count wrapper compose**, and deliberately: `content ...component tree.one` is "the bare children, of which exactly one". The two say different
things — `...` says *which* children arrive here (the unnamed ones), the
wrapper says *how many* — so they are orthogonal rather than two spellings of
one bound. `scroll`, `tooltip` and fyne's `Wrapper` are all of that shape, and
`Wrapper`'s codegen assigns the single child to a field, so the combination is
load-bearing rather than tolerated. #193 briefly specified it as an error on
the grounds that both were count bounds; that would have left the contract
unspellable and is retracted there.

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
body stays where it was written, handed the host event's payload and any
population parameter it reads (`EventDecl.Params`, the one event a source
declaration cannot spell). A call reading a population parameter stays in the
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

**`#[intrinsic]` on a component is a platform primitive.** On a function the
mark names a signature in `ir.Intrinsics` that every language backend must
implement. On a component there is no signature to register — the declaration
*is* the contract for props and events, and one platform's codegen emits the
widget from it (android's `Column`/`Row`/`Spacer`/`Text` in
`codegen/platform/android/android.sngl`). The id is that codegen's dispatch key, namespaced by
the emitting platform (`android:Column`) so it can never collide with a stdlib
intrinsic or with another platform's. `lib/internal_intrinsics_test.go` holds
the two forms to opposite rules: a function id must be in the registry, a
component id must not be, and must carry its namespace.

An `#[intrinsic]` component may **not** have a body, which is the same claim
from the other side: the mark says where the render comes from, so a body
beside one is emitted by nobody and read by nobody -- `isPrimitiveComponent`
exempts the declaration from inlining precisely so the platform can render it
from the declaration itself. `{}` is refused with the rest, because it says the
component renders nothing, which is the one thing an intrinsic never does. The
rule is at registration, so gtk4's GIR-synthesized declarations are held to it
too.

The mark's other job is to stop the inliner. A platform's extension override
inlines into its caller (`passInlinePure`), and every platform-package
component must inline or the build fails — so the primitives those overrides
lower down to have to be exempt.
`isPrimitiveComponent` reads `Component.Intrinsic` for that, alongside
`Wildcard` (html's raw element) and `Builtin` (a node kind) — the marks are the
whole list, and each says in its own vocabulary that the declaration is
rendered rather than composed away. A **tree kind** is deliberately not on it:
belonging to a segmented tree says which family a declaration joins, not that a
codegen renders it, so a shape composed out of other shapes is a wrapper like
any other. `isPlatformStdlibComponent` is a different question: whether a component came
from a `sngl:platform/` package the program imports.

A **native** mark — `#[go.native(path, name, flags)]`, `#[js.native(name, module, flags)]` — is the other half: the declaration *is* that host
identifier, so a call becomes a call to it and nothing is emitted for the
declaration itself. Two things about the name that are easy to get wrong:

- **It carries its own qualifier.** `#[go.native("strings", "strings.ToUpper")]`,
  not `("strings", "ToUpper")` — a Go package's name is not a function of its
  import path (`gopkg.in/yaml.v3` is package `yaml`), so the path cannot supply
  it. The path is what `RequireImport` adds.
- **Every language has one.** `go.native` and `js.native` were joined by
  `kotlin.native`, which android needs to describe Compose, and by
  **`#[cnative]` in `sngl:macro`** — C is an ABI rather than a target, so a
  platform built on a C library (gtk4 on cairo) names C identifiers and no
  language at all. The Go backend renders those as cgo and supplies the
  `C.double`/`C.int` conversions from the declared parameter types.
- **A native is never emitted.** Every call became a call to the host
  identifier, so a declaration would be read by nobody — and it is written as
  a stub over the zero value, which reads exactly like a real implementation.
  JavaScript got that rule first; Kotlin had the bug until a `deny` caught
  `fun hyp(a: Double, b: Double): Double = 0.0` beside a working call; Go was
  the third and is `testdata/native_decl_not_emitted.txtar`. The test is
  `Foreign.Name != "" && !Foreign.Marked` and it is applied **once**, in
  `CodegenCtx.AllFuncs` — not at the emitter, because fyne and bubbletea
  rebuild a component's func into a fresh `ir.Func` to give it a Model
  receiver and the copy carries no `Foreign`. Only a native with a *return
  type* ever showed: the checker synthesizes `return <zero>` for a bodyless
  func, and a void one got an empty block that every Go emitter's
  `len(fn.Block) == 0` guard already skipped.
- **`method` says the identifier is invoked *on* its first argument** rather
  than handed it: `c.Circle(1, 2)` where the default is
  `gfx.Context.Circle(c, 1, 2)`. Both are valid Go for the same method, and
  which one a host API wants is the API's to say — cairo takes its context
  first and wants the default. JavaScript has no receiver-first spelling at
  all, so describing a DOM API needs the flag: `ctx.arc(x, y, r)` is the only
  thing that runs. Only the last dotted segment is emitted, because the
  receiver supplies the package and type.
- **`named` (Kotlin) passes the arguments by the declaration's own parameter
  names**, which is what reaching a host parameter after one with a default
  requires: `drawCircle(color, radius, center, alpha, style, …)` cannot be
  reached past `alpha` positionally.
- **A native mark names a *type* as readily as a function**, which is what
  makes a host handle spellable: `#[go.native("time", "*time.Ticker")] struct Schedule { C go.chan<time.datetime> }` with `#[go.native("time", "time.Ticker.Stop", method)] func Schedule.stop()` beside it -- and a *field* of one is how a host API hands a channel back. gtk4 has done the same for C all along —
  `#[cnative("*C.cairo_t")] struct CairoContext {}` — so the form predates the
  need for it by a platform.

  **Reach for it before working around a signature.** fyne's and gtk4's timer
  runtimes each kept a process-wide mutex-guarded `map[int]…` and handed SNGL
  an integer index into it, because the override was written as
  `var handle = 0` and `int` looked like the only thing the declaration could
  spell. It was not: the schedule itself is a name, and the registries were
  bought for nothing. Nothing new had to be built to delete them.

  **A runtime package is a list of missing language features written in Go**,
  and `pkg/go/fynert` was two of them in turn: a registry while a handle was
  unspellable, then a goroutine and a `select` around a `time.Ticker`, because
  `Ticker.Stop` does not close `C` and a bare `for range` over it leaks. The
  registry went when the handle became spellable. The second was the language
  gap itself, and it is closed rather than stepped around: fyne's timer is a
  `*time.Ticker` consumed on a goroutine, and `fyne.sngl` holds all of it.

  **`go.chan<T>` is a builtin type kind declared by `sngl:language/go`**, not by
  `lib/`, because only a language with channels can answer one. It exists
  because a host API hands channels *out*: `*time.Ticker`'s `C` is a field, and
  without a channel type there was nothing to declare it as. `IRTypeToGo`
  spells it `chan T`, `lazyIter` ranges it one variable at a time like the pull
  sequence it is, and its zero is nil in both places a zero is written --
  `ZeroValueGo`, whose `TypeHintToGo` would otherwise title-case it into
  `Chan bool`, and the struct-literal path, which resolves the declaration the
  way `structLitTypeName` does because a synthesized zero leaves `Def` nil.

  **Direction is not spelled**, and the declaration says why: a receive-only
  channel is reachable because nothing writes a Go type for an expression that
  is only selected on, while binding one to a var would emit the bidirectional
  `chan T` and not compile against it.

  **`go.select` takes a `list<Case>`** -- SNGL has no variadic func parameters,
  `...` being a slot's -- and each arm is `go.recv(ch, func(v T) { … })`. It is
  the one thing here that cannot be an `IntrinsicEmitter`: that renders a single
  *expression*, and a select's arms are statement lists. So it is answered from
  `CallStmtLines`, the statement-level seam that already existed, and the arms
  are **inlined rather than called** -- an arm may `return`, and that has to
  leave the goroutine, which a closure wrapper would not do. A case variable is
  bound only where the body reads it, Go rejecting an unused one.

  `go.makechan` takes a witness value rather than a type argument --
  `makeChan(false)` is a `chan<bool>` -- because a call site has no syntax for
  the latter and a zero-argument generic leaves the element type unrecoverable.
  `make` and `close` are Go *builtins*, carrying no import path, so a
  `#[go.native]` cannot name them at all and both are intrinsics for that
  reason.

  **A statement-level answer has to say so**, which is what
  `DeclareLangImplements` is: the language-axis counterpart of
  `DeclarePlatformImplements`, and it exists for the same reason stated there.
  `lib/internal_intrinsics_test.go` asks whether *some* target can emit each id,
  and three of these are answered outside the emitter registry, so without it
  the check reads them as ids a build would emit a call to nothing for. It names
  the ids rather than the package, the opposite of the platform side and
  deliberately: `sngl:language/go` holds ordinary emitter-answered intrinsics
  too.

  **A list literal is a fourth place a lambda hides.** `WalkLowered` reached a
  lambda that *was* an expression and one handed to a call, and not one inside a
  list -- which is what a select's arms are, two levels down. So the widget
  writes in a tick reached the emitter untranslated: a bare `__n0.Text =`, which
  is neither a field any Fyne widget has nor a name in scope. The same blind
  spot recorded above for the lowering passes, one layer out.

  **The hand-over it also held is `async.post`**, which a platform package may
  name: `#[intrinsic("async.post")] func post(f func())` dispatches through
  `LookupPlatformIntrinsic` exactly as a blocking call's posted tail does, and
  emits `fyne.Do` with the import. So a callback that must reach the drawing
  thread asks for that in one word instead of a package re-spelling it. The id
  is also the second thing `nativeCallbackFuncs` treats as scheduling, for the
  reason the `schedules` flag exists: a post runs its closure from the loop the
  platform owns, so a blocking call written inside one is on the drawing thread
  unless this pass takes the closure as an entry point. There is no *one*
  declaration to put the flag on -- the pass synthesizes calls to the id
  itself, and a platform package may declare its own -- so the pass names it.
  Only fyne and gtk4 answer the id at all, which is what stops the declaration
  being lifted somewhere portable.

  **Two shapes make Go's select behave unexpectedly, and both are written on the
  declaration.** A *closed* channel is always ready and yields the zero value
  forever -- which is the idiom here rather than the hazard, since closing
  `done` is how the goroutine is told to stop and its arm is taken on the very
  next pass. A *nil* channel is never ready, so its arm is never chosen, and nil
  is exactly what `chan<T>`'s zero value is.

  What such a type may *not* do is be constructed: a program holds one and
  calls methods on it. So the only literal of one that reaches a backend is the
  empty zero the checker synthesizes for an uninitialised `var t Ticker`, and
  where the host spelling is a pointer that zero is `nil` rather than a
  composite literal — `*time.Ticker{}` does not parse
  (`testdata/native_pointer_zero.txtar`).

  An **index** is still right where the host's own ABI is an index: a GLib
  callback carries an `int` user_data and cannot hold a Go pointer at all,
  which is what `pkg/go/cbind` is. The test is whether the host asked for it.

**An event's payload is its declaration's, and each target hands it over in
its own terms.** `sngl:ui` declares one struct per kind of event
(`lib/ui/events.sngl`): `ChangeEvent{value string}` for a committed text or
choice, `ToggleEvent{checked bool}` for a checkbox's and a toggle's flip,
`ClickEvent{}` carrying nothing. A handler's parameter is typed by the
component's `@event`, and a platform override forwards the host's value one of
two ways: `change()` with no argument binds the handler's parameter to the
override's own host event, which the emitter reads fields off
(`e.target.checked`, fyne's `OnChanged(b bool)`, gtk4's
`gtk_check_button_get_active`); `change({checked = on})` builds the payload in
place, and `bindEventParams` reads each field where it was built, holding one
computed from state in a temp so a handler that writes that state reads the
value the event carried (bubbletea's `{checked = !checked}`). A `:prop` binding
writes back from the payload's field of the prop's type (`injectBind`) rather
than toggling, since a host that reports its state also reports it when the
program set it; `sngl test`'s interpreter, which runs the checked IR, does the
same in `writeBindings`. A test's `c.box.change({checked=true})` reaches the
platform's input path -- the element's state and a dispatched event on html,
the widget's state on gtk4, the callback on fyne -- except on bubbletea and
android, where a key press and a click flip the control whatever the payload
says. The shake declares a library payload a handler or a test names, and each
Go and Kotlin emitter prunes the ones nothing it wrote reads
(`golang.PruneStructDecls`, `kotlin.PruneLibraryDataClasses`), html's
`pruneDecls` doing the same for a constructor.
`testdata/toggle_change_payload.txtar` and `canvas_click.txtar` are the code,
`cmd/sngl/testdata/toggle_change_payload_runs.txt` the answer.

**A `#id` on a visual node declares a handle, and `ir.Var.NodeHandle` is what
says so.** Every target stores one wherever it keeps the tree — a field of the
Model on the Go targets — rather than as a local, so a read of it has to be
qualified. There are two ways a reference can be recognised as one and only one
of them is a fact about the *declaration*: `ir.Ident.IsElementRef` is set on the
`__nN` references a lowering pass synthesizes, while a read of a program's own
`#id` resolves to the var the checker bound and carries nothing. Neither kind
lands in `Component.Vars`, so `ExprCtx.Resolve` answers for neither.

Tested, never name-matched, for the reason `ir.Param.Receiver` gives: a node id
is the author's word and `__`-prefixed names are not reserved. It is asked in
the Go language context rather than in a platform emitter because the question
— where does this handle live — has one answer for fyne, gtk4 and bubbletea;
each platform's own intrinsic path already wrote `m.<id>` for the references it
emits, which is why only a native call's *receiver* went bare and why it read
as a gtk4 bug. `testdata/node_handle_native_method.txtar` is the fixture:
`gtk_progress_bar_pulse` sets nothing, so GIR describes no property for it and
it is hand-declared as a `#[cnative]` method reached through the handle.

**A node's prop may not be written at all**, which `refuseNodePropAssign`
reports on both the assignment and the toggle paths. A prop is declarative:
`ui.text(value=greeting)` says what the node shows for as long as it is
rendered, and reactivity re-evaluates it when `greeting` changes. A write
beside that is a second source of truth the next render undoes, so the program
that looks like it worked is the one whose write is silently gone — change the
state the prop reads instead. Nothing in the repository depended on it: every
assignment to a `#id` handle was a fixture testing whether one could be
written.

What a *lowering* writes is untouched, and is how a prop reaches a host at all:
`passReactivity` and `passDeclarative` emit `__n0.value = expr` by the hundred.
The two are told apart by `ir.Var.Synthesized` — and, where lowered IR is
printed and checked again, by `checker.Config.Lowered`, since `text #__n0(…)`
re-parses as an ordinary node with an ordinary id and nothing in the text says
which side of the pipeline wrote it. Only a caller that lowered the IR itself
may set that flag.

**So a handle's remaining use is reading**, and a *prop* read does not compile
on the Go mutation platforms: `GoIRContext.Select` ends at
`operand + "." + ExportName(field)`, inventing a Go field by title-casing the
SNGL prop, so `box.value` is `m.box.Value` against a `widget.Entry` that spells
it `Text` — and against a gtk4 handle that is an `unsafe.Pointer` with no
fields at all. On html it reaches the emitter and renders an empty element
nothing fills. The one read that does work is a `#[cnative]` method's receiver,
above. Closing the rest needs a language↔platform read hook that does not
exist, and on gtk4 a getter is a call (`gtk4rt.EntryGetText`) whose name GIR
would have to supply per property, not a field. Until then a read is answered
at build time wherever the tree says the node is gone — see
`#[gen.renders(identity)]` under `sngl:x/gen`.

**Two nodes may share an id, but a handle that is *read* may not be rendered
twice.** The two halves are asked differently and deliberately so.
`uniqueNodeIDs` renames the later copies *by name*, because what that repairs is
the emitted namespace, where any two `#bar`s collide however unrelated. The
refusal is *by symbol*: `declareNodeIDs` runs per body, so two components each
writing `#bar` declare two vars and each read says which it meant, while two
spliced copies of one body share theirs and neither read can — all of them
resolve to the first copy's field, and the second widget is created and never
touched. `ir.NodeInst.Handle` is the link that makes the symbol reachable from
the node, since `ID` is only a name. Keyed by name instead, a `quiet()` that
reads nothing and renders one was refused for a `#bar` that a *different*
component read; `cmd/sngl/testdata/node_handle_read_duplicated.txt` holds both
halves apart.

**`#[foreign]` records what a declaration corresponds to outside SNGL.** It
lives in `sngl:macro` for the same reason `shape` lives in `sngl:ui/draw`, and
because its users are outside the compiler: a language plugin generating marked
SNGL to describe a foreign API, a platform package naming its host types.
`#[foreign("go:example.com/api", "api.Entry")]` gives the import path and the
name there; one argument is the name alone, which is all a struct field can
say. A function may add flags — `pure` and `async` — that state what a call
costs, because a foreign function's SNGL body describes it rather than
implementing it and nothing may be inferred from it. `pure` is the sharp edge:
it lets the compiler evaluate a call at build time, so a wrongly marked
function runs during a build.

**A pure function's value depends on its arguments and its source and on
nothing it reads while it runs.** Its result is stored between builds, keyed
by the call and validated against the Go closure it was built from, and
nothing records a file opened or a variable read at run time -- so such a
read is replayed stale after the thing it read changes, which for a folded
const is a wrong build. Hand the data in as an argument instead:
`file:`'s `names(pattern)` lists a directory at build time, which is how
website.sngl gives `docs.LibraryComponents` the snapshots it used to `os.Stat`.
Reading the compiler's own generated source is the one exception, and only
because the store reports it.

The mark imports nothing, resolves nothing and validates nothing, and never
confers type identity — only a scheme importer's `Foreign.Origin` unifies two
declarations. A marked declaration is still the program's own, which is what
`Foreign.Marked` says: a backend emits it, so the mark's `Name` is a name to
spell alongside that declaration and never a reference redirecting to one the
backend did not emit. Struct, struct field and function are the forms that
carry it; the others refuse it.

Not every compiler primitive is a package. The node operations a visual tree
lowers to (CreateNode, AppendChild, …) are `ir.NodeOps` constants: no program
can name them, no language registers an emitter for them, and their only
consumer is `codegen.WalkLowered` dispatching to a platform's
`IntrinsicTranslator`. A declaration would describe nobody's contract.

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
  annotation's `Decl`, a field type, the element of a `list<Local>`, the host
  spelling each backend derives from `Name` — so setting `Name` reaches all of
  them. `ir.Func.Receiver` is the exception, being the receiver type's name
  written out as a string; renaming it is what keeps Go's `Local__secondShout`,
  Kotlin's `fun Local__second.shout` and JS's `Local__second_shout` off a type
  that holds someone else's fields. A checker error (`claimBodyType`) stood in
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

**`__instN` is one sequence across two passes.** `passInlinePure` substitutes a
platform override and `passNoInlineComponents` substitutes a user component, and
they rename that component's state into one host namespace — but each held a
counter of its own, both starting at zero, so an owner holding one of each came
out declaring two `hits__inst0`. Kotlin and Go refuse that outright; html keyed
its `state` object twice and silently kept one of the two counters, with only an
esbuild warning to say so. The counter is `Options.instSeq`, a `*int` so it
survives `Options` being passed by value, set once by `Lower` and defaulted by
`seqOrOwn` for a unit test that builds a pass's state directly.
`testdata/inst_suffix_one_sequence.txtar` is the fixture.

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
are about *when* the override is installed. `checkPendingExtensions` swaps the
override's vars, props and body decls onto the declaration and restores the
base after, so the nested bodies are checked while it still holds
(`checkOverrideNestedBodies`) rather than in pass2, which runs after the
restore. And the owner link has to survive to lowering, so `ir.Body` carries
`BodyDecls` beside `Vars` and `specializeComp` swaps all three — without it
every backend emitted the captured names bare, which
`testdata/component_override_body_capture.txtar` denies on both of its targets.
`ir.BodyOwners` and `ir.CapturesEnclosingState` read the overrides too, because
the checker asks its questions with no target picked; that is what lets the
recursion-cycle report reach an override body, and a `limit` var in
`error_component_override_nested_capture_recursive.sngl` keeps the cycle from
being folded away before it is asked.

A `func` written there is the third slot of the same shape. It is registered
by `collectExtensionDecls` and desugared onto the component by the same
`registerNestedMethods` an ordinary body uses, so the override's body calls its
own helper and a nested body reaches it through `lookupBodyMethod` — the route
a method takes, since a component-body func is a method on its owner rather
than a name in scope. `ir.Body` carries `Funcs` and `Methods` for it, swapped
by `specializeComp` with the rest; without that swap bubbletea emitted
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

`ir.BodyFuncs` is what pass2's `compOwnedFuncs` set reads, because by then the
base declaration is restored and the live `Component.Funcs` no longer names the
override's helper — checked at package scope instead, it reported the
component's own vars as undefined. That set is asked of `c.pendingExtensions`
as well as `pkg.Components`: an override's base is usually *not* this package's
declaration, and every override in `lib/` and in a target package has a stdlib
one.

**A helper needs the base reachable by its bare name**, which an override
written through a qualified alias does not have: a component-body func is a
method, and the synthesised receiver type, `AttachMethod` and `lookupBodyMethod`
all resolve the receiver as a bare name. So `component draw.circle[…]` may
declare vars and types but not funcs, and `reportOverrideFuncUnreachableReceiver`
says so where the helper is written. Binding the bare name for the body's
duration is what supporting it would take, and that shadows a same-named
declaration of the program's own for as long as it lasts — carrying a qualified
`ir.Func.Receiver` instead is the real fix and touches every `fn.Receiver == comp.Name` comparison in the checker.

`declareEnclosingBody` is the scope half, and `checkBodyOnce` orders an owner's
body check ahead of the bodies it declared — pass1 registers a nested
declaration first, so read in package order an unannotated `var count = 0` was
still `Dyn` where the nested body named it. The lowering half is
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
no state renders as a plain loop -- on html, as markup `optimize.Documents`
unrolls. A window resets the context, because a loop over pages is not a
position a page's body is repeated in. A canvas under a `for` is built at run
time for the same reason -- its surface and draw routine are its owner's one
field and one method -- which `passCanvasInstances` does by synthesizing a
component around it (`testdata/canvas_under_loop.txtar`).

A loop that holds a component built at run time is a slot too, whatever it
iterates (`bodyNeedsSlot`): the slot is what keeps the list of live instances,
and outside one fyne assigned every copy to the one Model field its id named.
"Built at run time" means a component with a body of its own -- the inliner
marks a *primitive* standing in a reactive position as well, on the
declaration, and counting that made every loop of html elements a slot
(`testdata/const_loop_beside_reactive_loop.txtar`).

**A slot re-renders where it was written.** html renders each into a
`display:contents` wrapper of its own; fyne and gtk4 render into the container
the slot sits in, so each slot keeps a hidden anchor there
(`codegen.SlotAnchorField`), added by its first render -- which runs while the
container is built, at the slot's position -- and inserts its entries before
it. Appending instead moved a re-rendered slot after every sibling below it
(`testdata/render_slot_in_place.txtar`); gtk4's cgo mode does the same through
two preamble helpers (`gtk4.TestCgoSlotReRendersInPlace`). A gtk4
record also holds a reference on its root, since the slot holding it removes
it before appending it again and GTK frees a widget its parent held alone.

**A target that keeps no state of an instance's own splices it instead**, and
that is `Features.InstanceState`, a capability every platform but bubbletea
declares: a record on fyne and gtk4, a factory closure on html, `remember` on
android. bubbletea's model keeps every cell in itself, and a component built at
run time there is a render function with nowhere to put one. So
`passNoInlineComponents` splices a component that has state or holds a lifetime
where it is written (`expandPerCopy`), and under a `for` gives each of its vars
one cell per copy (`perCopyCells`): a map keyed by the loops' indices
(`lower.CopyKey`), read as `cell.get(key, init)` and written through a
temporary stored back. A timer under a loop is then a schedule per copy, which
bubbletea keeps keyed the same way and routes through Update
(`codegen.CollectLoopTimers`, `bubbletea/loop_timers.go`); it used to be
collected by nobody and never ran. A recursion is never spliced, so state
inside one -- the recursive component's own vars or lifetime, a stateful
component written in its body, or a canvas it renders -- has nowhere to live
there and is refused with a position
(`refuseStateInCycles`, `refuseCanvasInCycles`,
`cmd/sngl/testdata/bubbletea_recursive_state_refused.txt`);
it used to reach the Model as a field nothing declared, or the view as an
empty string.

**A loop's focus stops are the focusable nodes it renders**, not its
iterations. bubbletea's `passFocusOrder` makes the outermost `for` holding one
a single slot whose cursor is an ordinal over those nodes, nested loops and
taken branches included: the view counts them as it renders (`__focusPosN`),
`__focusLoopN_len` counts them for Tab, and Update's case for a key walks the
loop the same way and runs the handler of the node the cursor names
(`bubbletea/focus.go`). The cursor was the iteration index, so two buttons in
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
reads is declared at file level (`sharedPageState`). What is still missing on
the three mutation targets is the other direction: a write to page state
updates the nodes of the scope that wrote it and of the page, and not those of
*other* live instances reading it, so their views go stale until they are
rebuilt.

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
and `ir.Func.Nested` says which declarations that is.** It joins the enclosing
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
belongs to the component, and it keeps the name it was written under. Reading `Nested` as "hoisted" is
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

Two consequences of a built-in being identified by its mark: a kind classifies
*one* declaration and does not
alias two — type identity is per-declaration, so two structs sharing a mark
would be two incompatible types (the checker rejects a duplicated node mark).
And `output` is a *directive* kind rather than a node one: it parses as a
visual node and is marked on a component declaration like `window` is, but the
compiler reads the tree into `ir.Output` instead of rendering it. The mark is
what recognises it — a package declaring its own `component output` gets that
component and no build directive — and what the mark permits is the root of a
file, once per package: any file may carry it, a second one anywhere names the
first, and a `sngl:` library package may not carry one at all
(`registerOutput`).

**Its contents are an ordinary component tree**, and the second user of
`sngl:tree`. `output`'s slot takes `build.language` members, a language node's
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

Whether a name nothing declares is a *misspelling* is `Config.TargetsComplete`'s
answer, and only a caller holding the whole registry may claim it
(`internal/build.Check`, the fixture harness). A platform's own test harness
registers itself while its fixtures name five other targets: that is a build the
check does not have rather than a name nobody serves, and from a partial
registry the two are indistinguishable. Everyone else gets a stand-in node with
the right family and no schema, which is the tolerance `mergedOptions` returning
nil used to give per lookup.

Notable stdlib packages:

- **`i18n`** — translatable strings via `$"..."` syntax, lowered to `i18n.tr(template, args)`. Supports ICU MessageFormat: plurals (`{n, plural, =0{...} one{...} other{...}}`), selects (`{x, select, key{...} other{...}}`). Manifest-backed translation; runtime locale from `LC_ALL`/`LC_MESSAGES`/`LANG`. Runtimes live in `pkg/{go,js,kotlin}/i18n/`. Direct formatters: `i18n.numberInt`, `i18n.numberFloat`, `i18n.date`, `i18n.time`, `i18n.datetime`, `i18n.select`.

### Markup and the `md:` scheme

**`sngl:ui/markup` is inline and nothing else.** A heading is a `text` with a
size, a quote a `vbox` with a rule down its side, a list a `vbox` of rows —
ordinary layout every platform already renders. A bold word inside a sentence
is not: it has to live in one flow of text with the words around it, and a box
cannot hold it without breaking the line. So the family is the part of a
document there was no other way to say, and a document is a `vbox` of
`richText` flows interleaved with anything else a `ui.node` can be — which is
also what makes a document extensible without the vocabulary growing.

`span` is an ordinary family, and that is the design rather
than a shortcut. A member holds its own family and never a `node`, so
emphasis around a link and a link inside emphasis are both one flow, and a box
in the middle of a sentence is a membership error rather than a content model
written in prose. There is no `passMarkup`: each platform overrides the members
in its own package and the nesting an author wrote is the tree the host is
handed. A run list flattened in `ir` was built and reverted, being a second
representation of a tree. Where the cascade is resolved is each host's answer —
CSS on html, Pango and Compose's `SpanStyle` merge natively on gtk4 and
android, fyne flattens at render time because the tree is what a reactive
program mutates, and bubbletea flattens at compile time because lipgloss
returns a string with reset sequences in it.

**A reactive `if` or `for` among spans** is a render slot only where a span
can hold one, which is html's `display:contents` wrapper and what
`#[gen.can(inlineSlots)]` says. Withheld, `slotFlows` makes the flow holding it
the slot -- wrapped in a one-pass loop over a const, which re-renders its body
on any state it reads -- and the `if` inside is an ordinary one: fyne builds
the flow around it, gtk4 guards the runs in its markup with `gtk4rt.When` and
refuses a `for` there, a label's markup being one expression
(`testdata/markup_reactive_span.txtar`). Only the flow's own content is asked:
an `if` holding a whole paragraph is its container's ordinary slot
(`testdata/markup_flow_under_reactive_if.txtar`).

`markup.SpanStyle` is its own struct and not `ui.Style` for two reasons. A run
has no box, so most of `ui.Style` would type-check on a span and do nothing
except on html, where a `<span>` takes padding. And flattening needs a third
state per field: in `ui.Style` an enum's zero is its first member, so an inner
run would say `normal` and un-bold its parent. `Weight` and `Slant` lead with
`inherit` for that reason, and the cost is that a span cannot turn *off* a
decoration an enclosing span turned on.

**A token's color is a palette's answer first and the host's second.**
`markup.palette` is a context holding a `Palette`, one color per `Token` kind,
where alpha zero means the palette says nothing about that kind. Unset, html and
gtk4 draw `lightPalette`/`darkPalette` (GitHub's syntax colors, which the docs
site's chroma style already used) and bubbletea, fyne and android ask their
theme. html writes the pair as the `sngl-tok-<kind>` class's rules, the dark
half under `prefers-color-scheme`, so a page's own CSS still wins; gtk4 takes
the light half. A kind the program sets wins on all five. An unset or constant
palette is a literal by the time it is emitted -- `UnprovidedContext` folds the
first, and an inlined body reads a provider's value in place of the context --
so only a palette read from state reaches an emitter as a run-time value:
`gtk4rt.Foreground`, `style.color`, fyne's `SetColor`, and a fallback chain on
bubbletea and android.

**Text in the family is literal.** Every character of a `markup.text` renders
as written — two spaces are two, a `"\n"` ends the line — on every target,
because what an author wrote between the quotes is the one thing a document
cannot have a platform reinterpret. Wrapping still happens around it. That is
why there is no line-break component, a second spelling of `"\n"` being a second
answer to give, and why html's collapsing is html's to undo (`white-space: pre-wrap`, `markup_rendered.txt` in a real Chromium) rather than the
author's. The importer follows from it: a soft break in markdown is a space,
since the wrapping an editor chose must not reach the reader as one, and a hard
break is `"\n"`.

html has two more ways to lose it, both closed. Its pretty-printer strips
the newline runs between tags inside a pre-wrap element, which is also what a
code sample's line-break-and-indent token looks like as a `<span>`, so a text node
that is all whitespace spells its line breaks `&#10;` (`escapeTextContent`,
denied in `markdown_import.txtar`). And a flow is a `<p>`/`<h*>`/`<pre>`,
which the browser's stylesheet gives a margin no other target has, so the
flow zeros it: the space between blocks is the document's `vbox` gap, and a
list item's text sits on its bullet's line rather than a margin below it.

The block components (`paragraph`, the six headings, `quote`, `codeBlock`,
`caption`, `list`, `listItem`) are **bodied** — each a `richText` with its
`role` and style set, or a `vbox` — so a platform implements the two
primitives and inherits every block. `role` is what a flow is *for*, and a
prop rather than a component per role, so it reaches every emitter through the
node they already render.

**`md:` imports markdown as SNGL source.** `codegen/scheme/markdown` is an
`FSSchemeImporter`, the seam `git:` and `http:` use, rather than a native
importer: what comes back is `.sngl` source the checker checks like any other
package, so a bad import can be dumped and read and a round-trip is assertable.
The document is read through the filesystem the program is checked against
(`codegen.ProjectFSScheme`), because it is a file of the project and an
in-memory package has no other. It parses with the same goldmark configuration
the doc site uses, so two readings of one document cannot differ.

- **A file**, `import doc "md:./guide.md"`, is a package holding one component,
  **`document`**, whatever the file is called: a name derived from the path
  would make the call site depend on something the alias already stands for.
  Frontmatter scalars become consts, in the order written and keeping their
  type. An html block and a raw inline are dropped — one target's vocabulary,
  in a document that renders on six.
- **A directory**, `import docs "md:./docs/"`, is a **site**: one package in
  which every markdown file under it is a page, and every `.sngl` file
  *directly* in it is a file of the package, so a site's own declarations sit
  beside its prose. A page is a component named for its path, `_` standing for
  what an identifier cannot hold (`index`, `guide`, `guide_intro`). The root is
  `index.md` and is required; `guide/intro.md` is a child of `guide`, whose
  content is `guide.md` or `guide/index.md` — both is an error, and neither
  leaves the child with no parent. A page's href follows its file:
  `/guide/intro.html`, and `/guide/index.html` for `guide/index.md` where
  `guide.md` is `/guide.html`.

  The package declares **`Page`** (`href`, `frontmatter`, `children list<Page>`),
  **`root`**, a const holding the whole tree, and **`site<T>(layout component(page Page, content component ui.node) T) T`**, which inserts
  `layout` once per page — reading each page out of `root` so the tree is
  written once — and populates its `content` entry with that page's component.
  The program writes the layout once and decides what a page is: a `window`
  per page, a route, a pane. Nothing holds a component, so all of it is
  compile-time. Those four names are reserved, and a page taking one, or two
  paths becoming one name, is refused naming the files.

  **`Frontmatter` is the site's own vocabulary.** A `struct Frontmatter`
  declared in one of the directory's `.sngl` files is what each page's literal
  is checked against, so the importer does no checking of its own. With none
  declared the importer synthesizes one: the union of the keys the pages wrote,
  in first-seen order, each typed by its first scalar and defaulting to that
  type's zero; a key written with two types is an error naming both files.

  **`#[md.order]` on a declared field sorts each page's children by it**,
  ascending and stable over path order. The mark is `sngl:ui/markup/md`'s,
  the home of the scheme's vocabulary, and the importer reads it off the AST
  before anything is checked — which is why a page omitting the key sorts as
  the field's default and that default has to be a literal. The checker's
  handler only holds the field to an int, float or string.

**A `sngl` fence may be live**, spelled `mode=` on the info line, which leaves
the fence reporting `sngl` and highlighting like any other. `view`, the
default, shows it. `island` is a component of its own at the fence's position,
with the whole fence as its **body** — so two islands each writing `var n = 0`
are two cells, by the language's own scoping rather than a mechanism the
importer built, and an island cannot hold a `window`. `package` contributes
declarations only; `body` places statements into the page at its position. A
live fence does not also show its source. Imports are hoisted as written and
collapsed only when identical, since rewriting an alias would be checking. A
mistake inside one reports the markdown file and line, because the position the
checker has is the `import` that read the document.

### Runtime packages for generated code

SNGL ships per-target-language runtime packages under `pkg/<lang>/<name>/`. These contain Go/Kotlin/JS code that generated programs import. Examples:

- `pkg/go/i18n/` — Go runtime backing the `i18n` SNGL stdlib package (manifest loader, ICU formatter, date/number formatters wrapping `golang.org/x/text` and `github.com/goodsign/monday`).
- `pkg/js/i18n/` — JavaScript runtime: ICU template parser plus `Intl.NumberFormat`/`DateTimeFormat`/`PluralRules`. Manifest inlined as `globalThis.__SNGL_I18N_MANIFEST__` by the html platform.
- `pkg/kotlin/i18n/` — Kotlin/Android runtime: ICU parser plus `android.icu.text.*`. Manifest loaded from `assets/i18n.manifest.json` via `I18n.init(context)` in `Application.onCreate` (currently injected into `MainActivity.onCreate` since the scaffold has no custom Application).

The `lib/` directory holds **SNGL stdlib declarations** (language-agnostic `.sngl` files embedded into the compiler). The `pkg/` directory holds **runtime implementations** (per-target-language packages emitted into generated code's import graph).

When adding a new stdlib package that needs runtime support:
1. Declare the SNGL surface in `lib/<name>/`, one directory per importable package.
2. For each target language that needs runtime support, create `pkg/<lang>/<name>/`.
3. The codegen for that language emits `import "git.duckfam.us/jonathan/sngl/pkg/<lang>/<name>"` and translates stdlib calls to that package's API.

### Built-in Generic Types

- **`map<K, V>`** — generic map type. Literal syntax `{k = v}` (disambiguated from struct literals by expected-type context). Methods: `length`, `keys`, `values`, `contains`, `get`. Codegen: Go → `map[K]V`, JS → `Map`, Kotlin → `Map<K,V>`.
- **`iter<T>`** — opaque generic iterator type. `list<T>` implicitly converts to `iter<T>`; `map<K, V>` does not, so a map cannot reach an `iter` position with its map-ness erased. For-loops bind elements via `for var x = iter`; map iteration uses two variables `for var k, v = m`. No methods, no fields.

**A unit's members are its bases, and they are registered like any other
declaration's.** A unit value is a magnitude per base (`ir/units.go`): `unit measurement { px, em, rem = 16em, vw, vh, pct }` declares five bases, so a
value carries five numbers and every backend emits it as a record of them.
`ir.UnitDef.Fields` is that record's member table — `ir.UnitFields`, one
`float` per base, built at registration — and `ir.Fielded` is what a struct and
a unit answer it through, the field half of what `ir.methodTable` already does
for methods across four kinds. `checker.selectDeclaredMember` is the one lookup
both use.

A **single-base** unit has no members at all. `unit tick { tk }` is `type Tick float64` in Go, a `Double` in Kotlin and a number in JavaScript: there is
nothing to select, and `float(x)`/`int(x)` is how that magnitude is read. So
`t.tk` is an unknown member like any other, and a *reduced* suffix is one too —
`rem` is 16em and no value carries a magnitude for it.

Before the table existed, nothing looked a unit member up: `inferSelect`
reports an unknown member only for the kinds `hasNoLegitimateFields` lists, and
`ir.TypeUnit` was not one, so **every** select on a unit fell through to `dyn`.
That is one defect with two faces. The invented `.value` was accepted in
silence and spelled blindly by each backend — `Measurement.Value` and
`Tick.Value` in Go, neither of which compiles, `Double.value` in Kotlin,
`undefined` in JavaScript. And the *valid* `m.px` was `dyn` too, which
`interpPartAlreadyString` answers yes to, so the string conversion every other
numeric operand gets was skipped and Go emitted `"px is " + m.m.Px`. Typing the
select fixed the second everywhere at once; no backend grew a case.

**A cast reads the magnitude, and it reads it in the unit's base.** `int(x)`
and `float(x)` are the whole of how a single-base unit's value is got at, so
every target has to answer them the same. A **multi-base** unit is refused
there (`multiBaseUnitCast`): a value of one is a magnitude per base, so there
is no single number to produce, and the diagnostic points at the per-base read
that replaces the cast — `m.px`. That is the same premise the checker already
applied to ordering two of them, extended to the one other place it decides
anything. `string(m)` is untouched, being display rather than a magnitude.
`duration` is the one that did not agree: Go carries one as a `time.Duration`,
whose unit is nanoseconds, while the declared base is ms — so
`GoIRContext.durationToNumber` divides at the cast. Only at the cast, because
arithmetic stays inside the representation:
`d + 100ms` and `d * 2` are `time.Duration` arithmetic and are correct in ns
right up to the cast that divides them out. `float` divides as floats, since an
integer division converted afterwards truncates a sub-millisecond duration to
zero.

`ClassifyUnit` is what gates it, and it reads `UnitDef.Builtin` —
`ir.BuiltinDuration`, the kind `lib/time/time.sngl` marks — rather than the
name `duration`. It asked the name in four places, which made the one built-in
with a host representation the one built-in that was *not* shadowable: a
program's own `unit duration { blip }` got `time.Duration(3) * time.Millisecond`
for `3blip`, the `time` import, the `mustParseDuration` helper and a Kotlin
`Long`. `testdata/unit_duration_shadowed.txtar` pins that it no longer does.
`golang/helpers.go`'s `case "duration":` stays, switching on a scheme type
*hint* beside `"color"` and `"date"` rather than on a declaration.

**And the display, which is the same claim made without a cast.** A unit
interpolated bare renders as its magnitude per base on every target: `"{d}"`
of `500ms` is `500ms`, `"{m}"` of `3px + 2em` is `3px + 2em`. The rule is the
representation restated rather than a second decision on top of it, and
`ir.FormatUnitTerm`, `ir.UnitTermSep` and `ir.FormatUnitZero` are the one
place it is written down — the interpreter and the optimizer call them, and
each backend emits a runtime helper saying the same thing about values no
compile-time caller can see.

It was **five** answers, not one per target. A var-held `500ms` printed
`500ms` on Go, `500` on JavaScript, `500.0` on Kotlin and `500ms` on the
interpreter; a `const` one printed `500` on all four, because
`optimize.evalConversion` folded a unit to the bare float64 its magnitude is
and spelled that back with `%v`. Go's apparent agreement was a coincidence of
the value: `time.Duration.String` normalises across units, so 1100ms prints
`1.1s` for a unit whose declared base is ms. And a multi-base value had no
chosen spelling anywhere — `{3 2 0 0 0}` on Go, `[object Object]` on
JavaScript, `Measurement(px=3.0, …)` on Kotlin — because nothing had ever
asked the question; those are three host defaults leaking.

Two consequences, and each is the representation asserting itself. **The
written suffix is gone from display**: `2rem` shows `32em`, because the record
is the whole of what a compiled target holds and a spelling to prefer was a
memory only `interp.unitValue.Suffix` had. And an **all-zero value prints in
the first declared base**, so `0rem` and `0px` both print `0px` — they are the
same value under per-base equality, and equal values have to print equally.

Go needs a helper per unit (`HelperSet.UnitStrings`, emitted by
`EmitUnitStringFuncs`) because it has no expression form for the join;
JavaScript and Kotlin inline an arrow and a `let`, which is also what binds
the operand so it is not re-evaluated once per base. Each helper spells its
magnitude with whatever that target already spells a bare float with —
`fmt.Sprint`, `String`, `_snglFloatStr` — so unit display inherits the float
agreement instead of restating it and drifting from it.

**The CSS path is not this path**, which is what made unifying it cheap rather
than a trade against html. A style prop is spelled by `internal/htmlutil`:
`UnitLiteralToCSS` off an `ir.Literal`, with its own rename table where the
base `pct` is written `%`. It shares `ir.UnitMagnitude` with the above and
nothing else, reaches `interpolateStringify` nowhere, and `7px` in a
stylesheet is unchanged by any of this.

The interpreter is a fourth implementation of all of this and has to be checked
with the three backends: `interp.ToInt` needs its `unitValue` case (without it
every `int(<unit>)` was 0 on `--platform=none`), `evalSelect` needs one to read
a per-base member, and `unitTable.BaseOf` is what says which base a suffix
reduces to, so `2rem` counts in em. `cmd/sngl/testdata/unit_magnitude_compiles.txt`
runs one program under both `--platform=bubbletea --language=go` and
`--platform=none` for exactly that reason.

`interp.unitValue` **is** that record — `Amounts`, a magnitude per base, plus
the suffix the value was written with for display. It was one `BaseAmount` and
a suffix, the pre-`ea7b2f84` "a unit reduces to one number" model, and the
member table is what made that visible: `1px + 2em` added to 3 and then
answered 3 for `px` and 0 for `em`, so a per-base read off a value *arithmetic
built* was quietly wrong where the same read off a literal was right. Both
fixtures had only literals, which is why neither caught it.

Four things follow, and the first three are the record restated:

- **Arithmetic is per base.** `Add`/`Sub` combine base by base and `Scale`
  scales each; a base whose magnitude is zero is absent rather than stored.
- **Equality is per base**, and **ordering is refused** for a multi-base unit
  (`orderable`): `3px` and `2em` are each the larger on their own base, so
  there is no answer to invent. The checker already refuses the pair
  (`IsSingleBaseUnit` gates `<`, `<=`, `>`, `>=`), so the guard is for the
  `dyn` operand that reached a comparison untyped.
- **A cast reads `magnitude()`** — the one base of a single-base unit, which is
  every cast the checker's ordering rule leaves meaningful.
- **Display sums the bases it carries**: `3px + 2em`, and a bare `3px` for a
  value that only ever names one, which is what a program that never mixes
  bases sees. In the bases, not in the spelling — `1rem` displays `16em`.

The timer is unaffected: `durationFromMs` goes through `toFloat`, and a
duration is single-base.

**Equality is per base on all four**, and the three compiled targets get there
differently: Go compares its `Measurement` struct field-wise and Kotlin its
`data class` component-wise, both without being asked, while JavaScript has no
such operator for an object. `===` there is reference identity, so `a == b` for
two equal measurements was false on every pair — `multiBaseUnitEqualJS` emits
the conjunction over the bases instead, with `!=` its negation rather than a
second walk. A single-base unit is a plain number in JS and keeps the bare
operator; routing one through the walk is what `testdata/unit_equality.txtar`
denies. `cmd/sngl/testdata/unit_equality_runs.txt` runs the comparisons under
bubbletea, the interpreter and a real Chromium, because a golden shows the
emitted text and only executing it shows the answer.

The case that separates two plausible implementations is a base that cancelled
to zero: the interpreter's map holds no key for it where the JS and Go records
hold a zero, so a comparison written over *the bases a value carries* rather
than over the unit's declared bases disagrees with the other three.

`hasNoLegitimateFields` stays an inverted allowlist, so a kind absent from it
still accepts any member name silently: `list`, `map`, `option`, `iter`,
`string` beyond `.length`, and a struct type carrying no `Decl`. That is not
uniformly a defect — an element-ref list projects a member read over its
elements, which `testdata/test_slot_element_ref.sngl` depends on
(`c.body.value` where `c.body` is `list<text>`) — so closing the rest is a
question per kind rather than one line.

Stdlib collection types support generic methods: `func list<T>.filter(f func(T) bool) list<T>`, `func list<T>.map<U>(f func(T) U) list<U>`, `func map<K, V>.keys() list<K>`, etc. The receiver's type parameters are bound at the call site from the operand's concrete type (e.g. `xs : list<int>` binds `T=int`). Method-level type parameters (the `<U>` after the method name) are inferred from the call's actual argument types — typically from a lambda's return type.

### AST

- **`ast/ast.go`** — top-level declarations and structural types: `Document`, `ComponentDecl`, `VisualNode`, `FuncDef`, `VarDecl`, `ConstDecl`, `StructDef`, `EnumDef`, `UnitDef`, `Param`, `Import`, `IfStmt`, `ForStmt`
- **`ast/expr.go`** — expression nodes and statements: `BinaryExpr`, `UnaryExpr`, `CallExpr`, `SelectExpr`, `IndexExpr`, `TernaryExpr`, `LiteralExpr`, `IdentExpr`, `ListExpr`, `StructExpr`, `LambdaExpr`, `InterpolationExpr`, plus `AssignStmt`, `EmitStmt`, `StmtBlock`, etc.

### Test Infrastructure

- `testdata/` at project root contains `.sngl` fixture files (e.g., `test_arithmetic.sngl`, `component_simple.sngl`, `error_*.sngl`)
- `cmd/sngl/testdata/` contains CLI golden test files (`txtar` format)
- Test runners resolve testdata via relative paths from their package directory
- Error directive comments in test files (e.g., `// ERROR(check) "invalid color literal"` — phase is `parse`, `check`, etc.) drive expected-failure assertions via `internal/testutil`

**A fixture-first fixture the backends cannot emit yet carries `// SKIP(codegen) "reason"`.** Every platform harness compiles the *whole* of `testdata/` for its own target, so a fixture naming a construct no platform can lower does not fail one assertion — it takes that harness down. The directive is the fixture's own opt-out from codegen only: `TestFixtures` still parses, formats, checks and folds it, which is the point of writing the fixture before the implementation. The decision lives in one place, `testutil.CodegenSamples` (a `TestdataSamples` that drops the skipped) plus `RunComponentFixtures`; a platform harness walks testdata through those and never tests the flag itself. Remove the directive in the commit that makes the fixture emit.

**Txtar script tests** (`cmd/sngl/script_test.go`): each `.txt` file is a txtar archive with script commands at top and embedded files below `-- filename --` markers. The `sngl` command runs in-process. Use `stdout`, `stderr`, `exists`, `grep`, and `!` for assertions.

**Golden codegen fixtures** (`testdata/*.txtar`, run by `internal/goldentest`
from the root `TestGolden`): the archive's root-level files are one SNGL
package, `out/<lang>/<platform>/` is the generated code, and which targets run
is the source's own `output` block rather than a harness header. Seed or
refresh with `go test . -run TestGolden -update`. The compile is
`internal/build`'s — the same code `sngl generate` runs, which is why that
package exists.

**Assert on generated code with a golden, not a grep.** A substring assertion
cannot see the shape of what it matched or the order two statements came out
in. `passForElse` sets its flag as the loop body's first statement; moving it
to the last broke `break` and `continue` on every compiled target and passed
the whole suite, because the only assertion was `grep -count=1 '__ran0 = true'`. `testdata/for_else_imperative.txtar` is that fixture converted, and it
fails on all three targets under the same change.

**The two harnesses are split by what they assert, and must stay split.**
`cmd/sngl/testdata/*.txt` is the CLI's: flags, exit codes, `dump` stages,
error text, `--out` layout, and anything that compiles or *executes* generated
code (`sngl test --language go`, a browser run, a gradle build). Those keep
their `[!node]`/`[gtk4]`/`[chromium]` skip conditions and their real
subprocesses; a golden runs nothing, so moving them would lose the assertion,
not reformat it. `testdata/*.txtar` is the language's: given this program,
this is the code every target generates. A claim about the CLI goes in the
first; a claim about codegen goes in the second.

**A `sngl test` a script launches is recorded, as a golden's toolchain run
is.** Each launch of a generated test program -- every target but `none` --
is answered from a `testrun/<platform>/<lang>/<file>/<component>/<n>` file in
the script's own archive: a digest of the generated program and the snapshots
it was compared against, then the results, the error, and any snapshots the
run wrote, which a replay writes back. An ordinary run only compares the
digest, so CI verifies a gtk4, Chromium or Robolectric run with none of them
installed, and a script needs no `[!display]`, `[!chromium]` or `[short]`
guard around one. A digest that moved, or a launch with no record, fails
naming `-update`; `go test ./cmd/sngl -run TestScript/<name> -update` reruns
every launch for real, refuses to record one the host cannot run, and
rewrites the records. The digest does not cover `pkg/<lang>/` runtimes, which
the generated program imports from the checkout -- a change there wants an
`-update` the digest will not ask for, the same gap a golden's record has.

A claim of *absence* is the one thing a golden cannot state — it makes
absence visible, but no reader notices that something is not there. Those are
written as `deny` lines in the archive comment, naming a golden file (or `*`
for all of them), a backquoted regex, and a reason:

```
deny out/go/bubbletea/model.go `held := \[\]int` -- the held sequence is a range func, not a slice
deny * `\.toList\(\)` -- no target copies a progression into a list
```

Deliberately not `! grep`: rsc.io/script's `grep` wants a pattern and a file,
one argument is a usage error, and a usage error under `!` is a *pass* — six
such lines had accumulated, three asserting the absence of the very thing
their change was about (`TestScriptGrepsNameAFile` is the lint against the
class). A `deny` that names no reason, no file, a file no target generates, or
a pattern that does not compile is a failure.

Neither harness fixes a fixture whose input never reaches the branch it means
to exercise: generated output is identical either way. Confirm a new fixture
*fails* when the behaviour is reverted.

**Know which harness sees platforms.** `TestFixtures` (root package, `internal/fixtures`) is the one walk over `testdata/*.sngl`: each fixture is read, parsed, formatted, checked, folded and LSP-marked once, as its own directives ask. It checks against every registered language and platform via `internal/testtargets`, so a fixture *can* exercise platform element resolution and `component sngl.X` extension bodies. No fixture gets the real import resolver — directory imports resolve through the stub in `internal/fixtures/resolver.go`. For that, and for anything driven by CLI flags, use a txtar test in `cmd/sngl/testdata/`: it runs the real CLI. For generated output use a golden in `testdata/*.txtar`, described above.

**A test body is lowered by each language's own test emitter**
(`codegen/lang/{golang,javascript,kotlin}/testlower.go`), and the interpreter
is the reference every target is held to — `cmd/sngl/testdata/test_component_surface*.txt`
runs one program through every form of access a test body has on all six. Three
rules keep them agreeing:

- **Test bodies are checked last**, after every component and window body:
  an unannotated component var has no type until its body is read, and a test
  naming it saw `dyn`.
- **A test holds its instance, not `m` or `state`**: package state and the
  funcs reading it are spelled through the instance (`ExprCtx.StateReceiver`
  on Go and JS; on android the state members MainScreen spells as `state.<x>`,
  rebound to the instance). Each test starts from a fresh one — a new Model on
  the Go targets, a remounted page on html.
- **An event invoker takes the widget-level value**: `c.f.input({value=v})`
  hands the invoker `v`, which it puts in the widget before firing the one
  event, as android's `performTextReplacement` does.

`internal/testtargets` is a separate package from `internal/testutil` on purpose — the platform tests are *internal* test packages (`package html`) that import testutil, so putting the codegen/platform dependency in testutil would close an import cycle.

**A Go type crossing into SNGL is tested with `sngltest/`.** A `go:` value
crosses in two halves — `pkg/go/consteval` encodes it, `codegen/scheme/golang`
types it — and nothing in the compiler forces them to agree.
`sngltest.CheckMarshal(t, v)` runs both over one value and reports which half
is wrong. It is a test helper, not a runtime package, which is why it sits
beside `sngl.go` rather than under `pkg/go/`: `pkg/<lang>/` is what generated
code imports, and the compiler must not be in that graph.

When adding a fixture or directive, confirm it *fails* when the behaviour is reverted. Several directives in this repo assert conditions that no test actually evaluates.

### Performance

What the compiler costs is measured in total work — CPU and bytes allocated —
not only wall time: it runs beside everything else on a developer's machine,
so a change that finishes sooner by doing the same work on more cores (running
targets in parallel) is not a saving.

- **Profile.** `SNGL_CPUPROFILE=f.prof` / `SNGL_MEMPROFILE=f.prof` on any
  command; env vars rather than flags so a tool driving a build need not
  thread one through. Set `SNGL_NO_PROXY=1` when timing the installed binary,
  or the first run measures `go tool` rebuilding it. A benchmark over
  `examples/` in a gitignored `tmp/` package (`build.ParsePackageFS` →
  `build.Check` → `build.Emit`, slog discarded) gives the warm per-phase
  numbers a one-shot CLI profile is too short to show.
- **An AST is immutable once parsed.** Nothing after the parser writes to one,
  which is what lets the embedded library tiers (`parseStdlibDocs`) and a
  target's served package (`parseProvided`, keyed by file name and content)
  be parsed once per process and shared by every check.
  `TestProvidedDocsSurviveBuilds` builds every golden fixture and then compares
  each shared document with a fresh parse; a phase that needs a modified tree
  builds IR or a new node, never an edit.
- **A library package is loaded against the targets it belongs to.**
  `CheckLibPackage` selects a target package's own target (`ownTarget`);
  selecting none loads every registered target's overrides, gtk4's GIR
  included.
- **The collector runs at Go's defaults, on purpose.** GC is a large share of
  a build's CPU, and the fix is allocating less: a `GOGC`/memory-limit value
  tuned against today's allocation profile goes stale as that profile shrinks,
  and is tuned to one machine besides.
- **The docs site (`go tool docsgen`) is the stress case**: one html build
  writing ~1200 files, where per-page costs dominate. Its compile step is
  `sngl generate --platform html --lang none website.sngl` once
  `internal/playground/assets/sngl.wasm` is staged.
- **What another process produces is SNGL, and it is stored.** A step
  whose output the compiler reads but did not write -- the compile-time
  evaluator's run, gtk4's GIR -- is a *producer* registered with
  `internal/gencache`, and what it hands back is a SNGL file whose root
  `cache.inputs` directive (`sngl:x/gen/cache`) lists every input it read,
  recorded before reading it. The store is separate from the file: keyed by
  the request and the compiler's identity (the executable's hash, since a
  producer's code is an input nothing in a file can record), it re-checks the
  recorded inputs and hands back the stored file when all still hold. Under
  `os.UserCacheDir()/sngl/gen`, `SNGL_GENCACHE_DIR` to move it,
  `SNGL_GENCACHE=off` to run every producer, 512MB and 30 days LRU.
  An `entry` input names another producer's output by digest, which is how a
  consteval value depends on the `go.deps` closure of the packages its
  program imported without restating hundreds of files, and on whatever of the
  compiler's own generated source the evaluator read (`codegen.GeneratedInputs`,
  reported as `//gencache` comment records in the results file). A batch
  producer uses `Lookup`/`Put` rather than `Get`: the evaluator looks each call
  up and runs its misses as one program. The docs site's warm compile answers
  every call from the store and runs no subprocess. js: calls are not stored
  yet, nor are go: import declarations -- both are producers still to move.
- **Cloning is the largest remaining cost.** `build.Emit` clones the checked
  package for every target but the last, and `ir.ClonePackage` copies by
  reflection everything reachable — library IR included, since per-target
  override bodies are written onto the shared library declarations.

### Debugging

Structured logging via `slog` at three levels controlled by CLI flags:

- `-v, --verbose` — Info: phase timing, file discovery, external commands run
- `--debug` — Debug: const folding, dead code elimination, import resolution
- `-q, --quiet` — Error only

Dump commands inspect each compiler phase:

```bash
sngl dump --stage parsed  [file|dir]                              # after parse + merge
sngl dump --stage checked [file|dir]                              # after type check
sngl dump --stage optimized --lang js --platform html [file|dir]  # after optimization
sngl dump --stage analysis --lang js --platform html [file|dir]   # CommonAnalysis as JSON
sngl dump --stage lowered --after none [file|dir]                 # pre-lower IR
```

The stage is a **flag**, not a positional argument — `sngl dump checked f.sngl`
fails with "accepts at most 1 arg(s)". `--format` selects `sngl` (default),
`spew`, or `json`; `--omit AST,Pos` trims noise. The analysis stage defaults to
`json` instead: it dumps facts *about* a program rather than a program, and
those have no source form — `--format sngl` on it is an error naming the two
that work.

A positional argument may also be a package rather than a path:
`sngl dump --stage checked sngl:platform/gtk4`. It arrives already checked,
because a library package loads under the rules that permit its own
`sngl:internal/` imports and because a target synthesizes part of it with no
file on disk. `check` and `generate` take one too; `fmt` does not, since it
rewrites files and a package has none.
