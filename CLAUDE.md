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
The three enumerate the same owners and each says so in its own code, which is
what let one of them forget a case the other two had. **A window owns timers**
— `passTimerPrimitive` records a schedule on whichever owner held the node, and
the inliner has by then put a top-level component's timer in the window — and
`walkWindow` and `offloadableFuncs` both walked a window's vars, funcs and body
and not its timers. Nothing in source puts a timer there, so both gaps opened
only after that pass ran and were invisible to every fixture written before it:
a ternary in a `@tick` panicked the Go emitter, a two-variable `sngl:seq` loop
there emitted `for i, x := range` over a pull sequence, and a `#[go.async]` call
there ran on fyne's drawing thread. `testdata/timer_tick_lowered.txtar` and
`testdata/timer_tick_async_offload.txtar` pin the three.

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
  - any language whose translator implements `codegen.HTTPCompiler` (today: `--lang go`): route mode. html collects windows into `HTTPRoute`s and delegates code gen (mux syntax for dynamic paths, server entry, `main()`/ListenAndServe) to the language via `CompileHTTP`. The platform carries no language- or framework-specific logic. POST actions are emitted only for handlers that transitively call functions imported from the target language (e.g. `go:` funcs under `--lang go`); other handlers stay pure client-side JS. Static mode errors the build if any window has a dynamic href.
    Browser testing via CDP (go-rod) is gated behind `//go:build !js` so WASM playground builds exclude it. A `testing_js.go` stub satisfies the interface for WASM.
- **bubbletea** — generates Go TUI code (`model.go`); supports `golang` lang only.
- **fyne** — generates Go desktop code; supports `golang` lang only. Its Go emitter knows three `#[intrinsic]` primitives, which differ only in the children contract a declaration cannot express as data: `Widget` (none), `Container` (a default slot, children attach through a method) and `Wrapper` (a default slot bounded to one, the child is assigned to a field). *Which* Fyne widget one becomes is a `Spec` record passed as a prop — Go constructor, its arguments, the Go type, the import paths, the setter behind each value prop, the callback field and Go signature behind each event. `codegen/platform/fyne/spec.go` decodes it and nothing else in the platform names a Fyne type. Label, Button, VBox and the other twelve are ordinary components in `fyne.sngl` carrying a Spec, so wrapping a widget from a Go module the compiler never heard of is writing a thirteenth — `codegen/platform/fyne/third_party_widget_test.go` is that, done in SNGL alone.

  A value has to reach the emitter through a *declared* prop, since that is what lowering turns into the `node.prop = expr` assignment the translator sees. So the primitives declare a vocabulary of value props by type (`text`, `placeholder`, `number`, `flag`, `options`) and `Setter` binds one to a Go method — the vocabulary grows with the types a setter takes, not with the widget count. Same for `@click`/`@change`/`@input` and `Handler`.
- **android** — generates Android app code; supports `kotlin` and `golang`.
- **gtk4** — generates CGo GTK4 desktop code; supports `golang` only. Widget metadata is parsed at compile time from a `Gtk-4.0.gir`, resolved by `girRegistry` in one place because the code generator used to resolve its own and the two could disagree: `--opt gir=builtin` selects the bundled subset, any other value is that path and a failure to load it is an error, and an empty value probes the system locations (`/usr/share/gir-1.0/` etc.) and falls back to the bundled subset.

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
- **`internal/optimize/`** — constant folding, dead code elimination with platform/language awareness. A loop over a constant iterable is unrolled only for a target with no host language (`evalCtx.unrollsLoops`): a static artifact holds the iterations themselves, whereas a language target emits the loop and its own compiler decides whether to unroll one whose bounds it can see — three copies of a Compose `RadioButton` were what the loop is. `expandForWindows` is the exception and unrolls everywhere, because each iteration there is a separate window rather than a repeated body. A static unroll is bounded (`maxStaticUnroll`) and reports rather than writing a page nobody asked for.
- **`internal/lower/`** — capability-driven IR→IR transformation passes, running between optimizer and codegen. Each pass is gated by a `lower.Features` flag. Languages declare their native capabilities via `Capabilities() lower.Features`; platforms combine that with their own restrictions. Passes include: PropBindings, RefLoop, NoTernary, NoLambda, NoReactivity, etc. Five run always and are not capability-gated because they answer for every target: `IndexedIter` (a two-variable loop over a pull sequence, which hands out no ordinal), `ForElse` (an imperative for-else, which no host loop expresses), `ViewForElse` (the same construct in a view body, which no platform emitter rendered), `BoundaryFailed` (a boundary's fallback slot, which no platform emitter rendered either) and `CSE` (a pure call a statement makes twice). `CSE` is statement-local and imperative-only on purpose — the temp it binds has to be a statement the target can hold, and a view body on `--lang none` cannot hold one. Entry point: `lower.Lower(pkg, caps, opts)`.
- **`internal/lsp/`** + **`internal/lspcore/`** — Language Server Protocol implementation (hover, completion, diagnostics)

### Stdlib

Stdlib source lives in `lib/<package>/*.sngl`, embedded via `//go:embed` in `lib/lib.go` (exported as `lib.FS`). **Each subdirectory is one importable package: `lib/<path>` is `sngl:<path>`.** Nothing in Go enumerates them — `lib.Packages()` reads the embedded directory, so adding a package is adding a directory.

A package is named for **what it does**, never for who ships it — `std` was a
name of the second kind, which is why everything drifted into it. Within a
package, files are organised by topic and not by declaration kind: a type and
its methods sit together (`lib/builtin/numbers.sngl`, `lib/ui/form.sngl`),
because grouping by kind splits every subject in two.

The tiers, and the split between them is the whole point of the system:

- **`lib/builtin/` → `sngl:builtin`** — the `#[builtin]` types and their methods, plus `output` and the error channel. The build directive is here rather than in a tier of its own because a package names its own targets without importing anything; `error`, `error.raise` and the `boundary` that catches one are here because a boundary is generic over the tree it was placed in and so belongs to no family — it is the compiler's construct, not a widget. Ambient: dot-imported into every file implicitly, and importing it explicitly is an error. This is the *only* implicit import in the language.
- **`lib/ui/` → `sngl:ui`** — the portable components most applications are built from, the `node` family they belong to, the `root` family and the `window` that is its one member, and the vocabulary every one of them refers to: `Style`, the style enums, `measurement`, and the event payloads. Those sit here rather than in packages of their own precisely because every component in every package under `sngl:ui/` names them. Reaches user code only through `import <alias> "sngl:ui"` (qualifies) or `import . "sngl:ui"` (flattens).
- **`lib/ui/draw/` → `sngl:ui/draw`** — `canvas`, the `shape` tree it hosts, and the 2D shapes that are members of it. It is the first *specialised surface* under `sngl:ui/`: a program pays for a drawing canvas only by importing it.
- **`lib/tree/` → `sngl:tree`** — the tree *vocabulary* and no families: the `kind` mark that declares one, the `none` mark that says a component joins none, and `one<T>` for a slot that takes exactly one. A family lives where its members do, which is why the widget family is `sngl:ui`'s `node` and not a `tree.default` here.
- **`lib/build/` → `sngl:build`** — the build-target tree: `language` and `platform`, the two `#[tree.kind]` structs an `output` directive's contents are members of. It is a package of its own rather than part of `sngl:ui` because `sngl:builtin` declares `output` and so has to import whatever holds its slot's type; `sngl:ui` is what `sngl:builtin` would then be importing, and it loads before `sngl:builtin` is adopted into the ambient scope, so the load fails on `unknown type "color"`. `sngl:builtin` cannot hold them either, since it already declares `struct platform` as the identity type. Nothing an application writes names it — a program writes `output`, and a target package names `build.language` or `build.platform` in its own node's return position.
- **`lib/time/` → `sngl:time`** — dates and the clock: `date`, `time`, `datetime`, the `duration` between two of them, and the `timer` that fires every duration -- an ordinary component that lowers to an effect, not a builtin node, which is why it carries no `#[builtin]` mark. None of it is ambient — a program that never asks what time it is never names any of it — which is why all four types moved out of `sngl:builtin`. Loaded at startup even when nothing imports it, because its declarations carry kinds the compiler dispatches on.
- **`lib/math/` → `sngl:math`** — mathematical constants: `pi` and `tau`. A package rather than methods on `float`, because a constant has no receiver and nothing to fold — a zero-parameter static method survived only where the optimizer ran. `float`'s `sin`/`atan2`/`sqrt` belong here too and will move; they are intrinsics with per-language emitters, so that is its own change.
- **`lib/seq/` → `sngl:seq`** — integer sequences: `count`, `range` and `step`, the `iter<int>` a counting loop iterates. Nothing else can produce one, since building a range in SNGL would need a loop and a loop needs a range; a sequence in a loop head lowers to the host's counting loop (`ir.IterCounted`), and anywhere else it is the pull sequence `iter<T>` is spelled as -- `func(func(T) bool)` in Go, a generator in JS, `Iterable<T>` in Kotlin -- so no list is built to iterate one. A list reaching an iter<T> position is wrapped by the conversion the checker already inserts there (`wrapIfNeeded`); a two-variable loop over one gets its ordinal from a counter (`passIndexedIter`), since a pull sequence hands out no index.
- **`lib/dialog/` → `sngl:dialog`** — `Alert` and `File`: host-native modal surfaces. Not components — a component is placed in a tree and rendered, whereas `Alert.confirm` hands control to the host and returns what the user chose.
- **`lib/test/` → `sngl:test`** — `Test`, the receiver a test function's first parameter carries.
- **`lib/i18n/` → `sngl:i18n`** — the translation surface `$"..."` lowers to.
- **`lib/macro/` → `sngl:macro`** — the public mark vocabulary a package writes to describe its own declarations (`foreign`, `wildcard`, `construct`). Only the vocabulary: `sngl:platform/<name>` and `sngl:language/<name>` are not under `lib/` at all — a target carries its own package, described below.
- **`lib/internal/` → `sngl:internal/<name>`** — the compiler's own tier, importable only from lib source.

A library package documents itself with a **package comment**: a run of line
comments at the top of a file, separated from what follows by a blank line
(without the blank line it documents the declaration below it instead). The
text is markdown, and `sngl doc` renders it as the package description — so
adding a `lib/` directory with a package comment needs no code change.

Go's semantics apply when several files carry one: they are concatenated,
blank-line separated, in load order. That order is not guaranteed, so prose
that has to read in sequence belongs in a single file — `lib/<pkg>/doc.sngl`
by convention, as `lib/ui/doc.sngl` does.

Packages import each other — `lib/ui/draw` is written against `lib/ui`, and `lib/ui` in turn against `sngl:time` and `sngl:tree` — so they load lazily and memoized (`libPkg`), not in directory order. A lib package qualifies its dependencies rather than dot-importing them: lib source is registered into the checker's own symbol table, so a name it lifted would be indistinguishable from one it declared and would be re-lifted by a dot import of it. User packages do not re-export a dot import; lib packages must not either.

A `#[builtin("kind")]` mark says which IR construct a declaration dispatches to, **not** which tier it lives in nor what the declaration is called — the builtin visual nodes are spread across tiers, `window` in `ui`, and `effect`, `context`, `output` and `boundary` in `builtin`. `boundary` is the case that makes the second half plain: it carries the `errorBoundary` kind, because the kind names the role and `ir.ErrorBoundary` is the construct it dispatches to, while the name a program writes is the declaration's own.

`internal/checker/stdlib.go` parses the library at startup. User declarations shadow stdlib ones. Platform-specific component implementations live in that platform's own package; its source imports the stdlib under an alias and overrides through it (`import ui "sngl:ui"` + `component ui.vbox`), and the prefix is that alias, not a fixed name. The override names the target it implements as the package's own identity const, unqualified — `component ui.vbox[platform]`, not `[android.platform]`: inside the package that declares it, saying the package name would say it twice. A program outside the package writes the qualified form, `[html.platform]`, because that is how the const reaches it.

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
off the mark. Three tree kinds are the odd ones out and mark a *tree* rather than a
component: `treeRoot` (the family a package body accepts), `treeNode` (the
widget family) and `treeShape` (the drawing tree). Each is a family the
compiler itself has to name — to check the body against, to tell an ordinary
component from a rendered one, to know which canvas passCanvas paints — and
every other family it compares by declaration alone. Marking them is what
keeps `ir.go` from holding a package URI and a name for each: `ir.IsUITree`
and its two siblings read `StructDef.Builtin`, so a family may be renamed or
moved and a program's own `struct shape` does not become the painted one.
The mark is declared in `lib/internal/marks` and implemented in
`internal/checker/marks_impl.go`; kinds are `ir.BuiltinKind`.

**Macros are not ambient.** A macro package is imported like any other:
`#[tree.kind]` needs `import "sngl:tree"`, and `#[marks.builtin("...")]` and
`#[marks.intrinsic("...")]` need `import marks "sngl:internal/marks"` — which
is why every `lib/` file carrying a mark declares it. The alias is an
ordinary file-scope binding, so the mark follows it: `import t "sngl:tree"`
means `#[t.kind]`.

**Write the qualified form.** A dot import stays legal and supported — with
`import . "sngl:internal/marks"` the mark is the bare `#[builtin("...")]` —
but the repository's own source no longer uses one, so that what a reader
learns from is the qualified form. The alias is the package's last path
segment (`marks`, `ui`, `seq`, `draw` for `sngl:ui/draw`). `sngl:builtin` is
unaffected: it is ambient rather than dot-imported, and it is how `int`,
`string` and `color` are named.

A lib package may carry macros alongside its declarations — `sngl:tree`
ships the `kind` mark next to the default tree it applies to — so the
`sngl` scheme is checked against the `lib/` layout alone: a directory is what
makes a package exist, macro-only ones included. `sngl:internal/<name>` is
the compiler's own tier: a package there may contribute macros, declarations,
or both. `internal/marks` declares only macros; `internal/draw` declares the
drawing primitives passCanvas emits, the intrinsic half of `sngl:ui/draw`.

**A tree is a declared type, and `#[tree.kind]` marks the struct that names
one.** `sngl:tree` describes a segmented component tree: a family whose
members are not interchangeable widgets, where a container accepts only its
own family. Drawing is the first user, rich text and menus are the next, and
nothing in the mechanism knows what a shape is.

```sngl
#[tree.kind]
struct shape {}

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

The tree is the *declaration*, not its name, so a misspelling is an unresolved
name where it is written, and two packages each declaring `struct shape`
declare two trees. A tree struct holds nothing and no value of it exists.

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
host — and an `if`, a `for` and a boundary are how the nodes under them got
there rather than nodes, so the walk reaches through all three. A slot
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
nothing an unrestricted position reports. So the five call sites record a
closure (`deferTreeCheck`) and `runTreeChecks` drains them once every body has
been read, which is why the function takes the component the content was
written in rather than reading `c.currentComponent`.
One fixture per position holds that, each naming a declaration written *below*
it: `error_tree_inferred_late.sngl` for a node's bare children,
`error_tree_inferred_positions.sngl` for the other four, and
`error_tree_inferred_treeless_contains.sngl` for the tree-less rule, which
reads an inferred family too. Move the matching check back inline and the
fixture passes clean rather than failing — which is how each was confirmed.

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
platform's `Timer` primitive. Two rules follow, and they are each other's
halves:

- it may be placed in **any** tree, so a lifetime bracket belongs in a drawing
  as readily as in a layout (`checkTreeMembership`);
- it may contain a member of **none**, because a body that rendered a widget
  would have joined that family without saying so, and would then be
  placeable in a canvas (`checkTreelessBody`). An `if` or a `for` is how the
  nodes under it got there rather than a node, so the rule reaches through
  both.

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

**The interpreter answers it itself**, in `Env.caught`. `sngl test` on the
`none` platform runs the *checked* IR with no lowering at all — the same reason
the interpreter honours `For.Else` itself — so the pass is not in that path.
The map is keyed by the boundary's `@error` handler, which is what a raise
reaches through `Call.ResolvedHandler`, and held per scope so two
instantiations of one component catch separately.

**`sngl:ui`'s `root` is the family a package body accepts**, and that is the
whole of what makes a window top-level — no syntactic rule names the
construct. So a `node` at the root of a file is the ordinary
tree-membership error, an `if` or a `for` there still works (neither is a
node), and a component that names `root` itself renders windows, which
`passRootWindow` lifts onto `pkg.Windows`. `output` is exempt: it is read as a
build directive before any tree question is asked, and `sngl:builtin` cannot
import `sngl:ui`, where the root tree lives.

**A program declares at least one window**, checked by `internal/build.Emit`
rather than by the checker: `component c { … }` on its own is a perfectly good
thing to type-check, and it is only as something to *run* that it has nowhere
to draw. Which is why **`component main` has lost its harness convention** —
`CodegenCtx.RootDecl()` now answers only for a harness that has cleared the
windows on purpose (the test launcher isolating a component), and a `main` in
an ordinary program is an ordinary component. `output(entry = home)` names the
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

`...component` is the **rest slot**: the one a caller fills with the children
written bare. Its name is the author's (`content`, `shapes`, `panes`,
`children` where nothing better is true), and it is inserted by that name like
any other slot. A component declares at most one; one that declares none
accepts no children, which is where `component X does not accept children`
comes from. It names no invocation parameters — bare children are written once,
with nothing to bind them to — and supplying both a population by name and bare
children populates it twice. `ir.SlotDecl.Rest` is the flag, `Component.RestSlot()`
the lookup; the old answer was the name `_`, which is why nothing keys on a
slot's name any more.

**`...` and a count wrapper compose**, and deliberately: `content ...component tree.one` is "the bare children, of which exactly one". The two say different
things — `...` says *which* children arrive here (the unnamed ones), the
wrapper says *how many* — so they are orthogonal rather than two spellings of
one bound. `scroll`, `tooltip` and fyne's `Wrapper` are all of that shape, and
`Wrapper`'s codegen assigns the single child to a field, so the combination is
load-bearing rather than tolerated. #193 briefly specified it as an error on
the grounds that both were count bounds; that would have left the contract
unspellable and is retracted there.

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
`Wildcard` (html's raw element), `Builtin` (a node kind) and a **segmented**
tree kind (a shape, or the canvas that hosts them) — the marks are the whole
list, and each says in its own vocabulary that the declaration is rendered
rather than composed away. Segmented is `ir.IsSegmentedTree`: any tree but the
widget family, whose members are precisely the ordinary components this pass
exists to compose away. `Tree != nil` was the test while the widget family was
not a tree, and reading it after the change exempted every wrapper in the
language.
`isPlatformStdlibComponent` is a different question: whether a component came
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
packages each declaring `struct shape` declare two trees; a body-local
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
namespace is flat — so each kind is asked, in its own terms, whether the two
would collide there, and both answers are a **codegen limitation surfaced in
the checker** rather than a language rule. #198 renames per body and deletes
both, along with the fixtures that pin them.

- A **type** collides always: every backend emits a type declaration straight
  from `ir.Package.Structs` and none renames. `claimBodyType` reports the pair
  at registration, and reports a body-local type against a *top-level* one of
  the same name for the same reason
  (`error_body_local_type_two_bodies.sngl`).
- A **component** collides only inside a recursion cycle, which is why the two
  checks differ in breadth. Every platform sets `InlineComponents=false`, so
  `passNoInlineComponents` substitutes a component that is not in a cycle into
  its caller with its state renamed per call site (`__instN`) and it never
  reaches a backend under its declared name. What survives is a cycle, and two
  surviving declarations of one name emit one host component twice.
  `reportBodyComponentCollisions` asks that after pass2 and of the cycles only
  (`error_component_nested_recursive_collision.sngl`), which is what lets the
  ordinary shadowing and two-bodies cases through.

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
checker, on `claimBodyType`'s terms rather than as a language rule: nothing
renames a component method per body, so both would be emitted under one host
identifier. #198's hoist-and-rename is where it lifts, and the diagnostic says
so (`error_component_override_body_func_shadows.sngl`).

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
- A **reactive `if` or `for`** — each copy there needs state of its own, which
  is what the main walk's `RuntimeInstance` election gives a *non*-capturing
  nested component, and a capturing one cannot have. Reported by
  `spliceNestedCaptures`, since reactivity is not a fact the checker holds
  (`cmd/sngl/testdata/nested_capture_in_reactive_position.txt`). A
  **non-reactive** loop is deliberately allowed: a nested component shares one
  cell there with or without capture, which is the pre-existing `rc.repeated`
  limitation.

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
this: it is the window's own, the way a component-body func is the component's,
and it keeps the name it was written under. Reading `Nested` as "hoisted" is
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

Three things follow. A target package now declares a component sharing the
package's own name, and the package binds *itself* as a namespace under that
name so its bodies can write `html.div`; the namespace wins inside the package,
and an output block reaches the node off the package rather than through that
scope. Resolution inside the directive is by level rather than by scope
(`checker.targetNode`, keyed on `c.outputDepth`), trying the tier that level
accepts first and the other second — `none` is both a language and a platform,
and trying the other tier second is what makes a misplaced target a
tree-membership error rather than an unresolved name. And every value in the
tree must satisfy `ir.IsConst`, the test `const(…)` uses: the directive is read
once, before the program runs, so a `var` read would compile to a snapshot of
whatever it held first, while a `#[foreign(pure)]` call is a build-time value
and passes.

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

Stdlib collection types support generic methods: `func list<T>.filter(f func(T) bool) list<T>`, `func list<T>.map<U>(f func(T) U) list<U>`, `func map<K, V>.keys() list<K>`, etc. The receiver's type parameters are bound at the call site from the operand's concrete type (e.g. `xs : list<int>` binds `T=int`). Method-level type parameters (the `<U>` after the method name) are inferred from the call's actual argument types — typically from a lambda's return type.

### AST

- **`ast/ast.go`** — top-level declarations and structural types: `Document`, `ComponentDecl`, `VisualNode`, `FuncDef`, `VarDecl`, `ConstDecl`, `StructDef`, `EnumDef`, `UnitDef`, `Param`, `Import`, `IfStmt`, `ForStmt`
- **`ast/expr.go`** — expression nodes and statements: `BinaryExpr`, `UnaryExpr`, `CallExpr`, `SelectExpr`, `IndexExpr`, `TernaryExpr`, `LiteralExpr`, `IdentExpr`, `ListExpr`, `StructExpr`, `LambdaExpr`, `InterpolationExpr`, plus `AssignStmt`, `EmitStmt`, `StmtBlock`, etc.

### Test Infrastructure

- `testdata/` at project root contains `.sngl` fixture files (e.g., `test_arithmetic.sngl`, `component_simple.sngl`, `error_*.sngl`)
- `cmd/sngl/testdata/` contains CLI golden test files (`txtar` format)
- Test runners resolve testdata via relative paths from their package directory
- Error directive comments in test files (e.g., `// ERROR(check) "invalid color literal"` — phase is `parse`, `check`, etc.) drive expected-failure assertions via `internal/testutil`

**A fixture-first fixture the backends cannot emit yet carries `// SKIP(codegen) "reason"`.** Every platform harness compiles the *whole* of `testdata/` for its own target, so a fixture naming a construct no platform can lower does not fail one assertion — it takes that harness down. The directive is the fixture's own opt-out from codegen only: `internal/checker`, `internal/parser`, `internal/optimize` and `internal/lspcore` keep running it, which is the point of writing the fixture before the implementation. The decision lives in one place, `testutil.CodegenSamples` (a `TestdataSamples` that drops the skipped) plus `RunComponentFixtures`; a platform harness walks testdata through those and never tests the flag itself. Remove the directive in the commit that makes the fixture emit.

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

**Know which harness sees platforms.** `internal/checker`'s two testdata-driven tests (`TestCheckTestdata`, `TestCheckProjectTestdata`) check against every registered language and platform via `internal/testtargets`, so a fixture *can* exercise platform element resolution and `component sngl.X` extension bodies. The other `TestdataSamples` consumers — `internal/optimize`, `internal/parser`, `internal/lspcore` — still check with none registered, and no fixture gets the real import resolver (directory imports resolve through a test stub). For those, and for anything driven by CLI flags, use a txtar test in `cmd/sngl/testdata/`: it runs the real CLI. For generated output use a golden in `testdata/*.txtar`, described above.

`internal/testtargets` is a separate package from `internal/testutil` on purpose — the platform tests are *internal* test packages (`package html`) that import testutil, so putting the codegen/platform dependency in testutil would close an import cycle.

**A Go type crossing into SNGL is tested with `sngltest/`.** A `go:` value
crosses in two halves — `pkg/go/consteval` encodes it, `codegen/scheme/golang`
types it — and nothing in the compiler forces them to agree.
`sngltest.CheckMarshal(t, v)` runs both over one value and reports which half
is wrong. It is a test helper, not a runtime package, which is why it sits
beside `sngl.go` rather than under `pkg/go/`: `pkg/<lang>/` is what generated
code imports, and the compiler must not be in that graph.

When adding a fixture or directive, confirm it *fails* when the behaviour is reverted. Several directives in this repo assert conditions that no test actually evaluates.

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
