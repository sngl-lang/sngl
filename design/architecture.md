# Architecture

SNGL is a UI language that compiles to multiple platforms. The pipeline:

```
.sngl source → Parser → AST → Checker → Optimizer → Lower → Platform+Lang Codegen → Output
```

**Public API** is in `sngl.go`: `Parse`, `Format`, `FormatTo`, `FormatExpr`, `Check`, `Convert`. Keep this file intact as the stable surface.

## Codegen Plugin System

Every target is a `lib/` directory registered from the layout; its Go generator registers by key in `init()`, and both are looked up by name at runtime:

- **`codegen/codegen.go`** — defines `LangTranslator` and `PlatformGenerator` interfaces
- **`codegen/registry.go`** and **`codegen/declared.go`** — the registry: a `DeclaredPlatform`/`DeclaredLang` per lib target, resolved to the Go generator `RegisterNative` registered under the key its node names
- **`codegen/lang/`** — language translators (golang, javascript, kotlin), each registering with `RegisterNative` in `init()`
- **`codegen/platform/`** — platform generators (android, bubbletea, fyne, gtk4, html), each registering with `RegisterNative` in `init()`; the packages are `lib/platform/<name>` -- see *A target is its library package*
- **`codegen/lang/languages.go`** and **`codegen/platform/platforms.go`** — blank-import all implementations; `cmd/sngl/main.go` imports these to trigger registration

`PlatformGenerator` optionally implements `TestRunner`, `PreviewStyler`, or `Snapshotter` interfaces (checked via type assertion).

**A generated file is written through its language's `FileEmitter`, and a scaffold file straight to its writer; neither goes through `text/template`.** The emitter owns the package clause, the cgo preamble and the import block, so a platform writes only a body and requires each import (`RequireImport`); gtk4's `model.go`/`callbacks.go` and Android's `MainScreen.kt`, `MainActivity.kt` and `Theme.kt` are written that way. Gradle files and the manifest, which no translator speaks, are `codegen.WriterFile`s. `text/template` looks names up with `reflect.Value.MethodByName`, and a binary that can reach that call keeps every exported method of every type it holds, since the linker can no longer tell which go unused. With Android's scaffold written as templates that was half the playground's wasm (100 MB, over GitHub's file limit for the pages deploy, to 52 MB without), most of it typescript-go's AST. `internal/playground`'s `TestWasmLinksNoTemplatePackage` holds the wasm to it.

**The release CLI reaches no reflective method call either**, and `cmd/sngl`'s `TestBinaryReachesNoReflectiveMethodCall` holds it to that by reading the linker's dependency dump (`-ldflags=-dumpdep`), which marks each reached call `<ReflectMethod>`. Whether `text/template` is linked is the wrong question for the CLI: cobra imports it and runs it only for a custom help template, which sngl never sets. glamour ran every style's `Format` through it, so `sngl doc`'s renderer cost a third of the binary (66.6 MB stripped, 44.8 MB without). sngl uses glamour from the fork `github.com/sngl-lang/glamour` (a `replace` in `go.mod`), where a `Format` substitutes `{{.text}}` and nothing else; every built-in style says no more.

**Two more forks trim what the CLI embeds.** `github.com/sngl-lang/typescript-go` (branch `sngl-rename`) carries no translated diagnostics, since SNGL reports in English and all thirteen catalogues were linked (1.1 MB). `github.com/sngl-lang/chroma` (branch `sngl-v2`, from v2.27.0) keeps only the lexers SNGL shows -- the repository's fences, its targets and import schemes, and the config formats beside them -- for 1.9 MB. A fence in any other language, in the docs or in a program's `md:` markdown, renders as plain text; a lexer comes back by restoring its file from upstream in the fork.

## Codegen Models

Platforms choose between two intermediate representations based on their rendering approach:

- **MutationModel** (`codegen/model.go`) — emit a static tree once, then generate targeted `Updater` functions to patch when state changes. Used by HTML and Fyne. Interfaces: `MutationModelEmitter`.
- **RenderModel** (`codegen/model.go`) — re-render the full view from state on every change; framework handles diffing. Used by BubbleTea and Android/Compose. Interfaces: `RenderModelEmitter`.

Both start from `codegen.AnalyzeCommon(doc)` which extracts model fields, computed deps, functions, structs, and timers into a platform-independent `CommonAnalysis`. That struct is read-only to a generator and holds no `*ir.Package`: what a generator *writes* as it emits — helper flags, registered CSS — is `codegen.Emission`, which a platform embeds beside the analysis. Keeping the two apart is what makes `dump --stage analysis` a dump of the analysis rather than of the whole IR graph plus the emitter's scratch space.

## Platform Details

- **html** — two modes selected by `--lang`. Its overrides of `ui.badge`, `ui.chip` and `ui.avatar` render what sngl:ui's bodies do and carry `sngl-<name>` classes, with a modifier per choice (`sngl-badge-success`, `sngl-avatar-large`), so a page's stylesheet reaches every one: A constant loop's copies of a node carrying an `#id` are elements of their own, each with its own variable (`nodeID`, `testdata/html_loop_node_id.txtar`).
  - JavaScript's `===` is an object's identity, so `==` between structs, colours included, and payload enums is written out field by field from the type (`jsEqualExpr`); written as `===`, two colours alike were unequal (`cmd/sngl/testdata/struct_equality_runs.txt`). A test runs in a module beside the page and finds the page's funcs and consts on window, where a test build publishes both.
  - `--lang none` (default): static site — one document per page of the `nav.stack` a window holds, written at the page's href with inline JS, or one `index.html` for a window that holds none (see *Navigation*). Every window after the first one that is always there is a `<dialog>` in its document, in every page's (see *html writes its document from a Window* in design/windows.md).
    Pages are written one at a time from what `documentsOf` decides, folded with `codegen.Request.Fold`, and a page's script is written from the package, which holds every page's component factories and render slots. So those are marked as they are written and `pruneDecls` keeps the ones the rest of the script names: written whole, a site of N pages carried N pages' factories in each and built in N² time.
  - any language whose translator implements `codegen.HTTPCompiler` (today: `--lang go`): route mode. html collects the pages of the stack a window holds -- or a window that holds none -- into `HTTPRoute`s and delegates code gen (mux syntax for dynamic paths, server entry, `main()`/ListenAndServe) to the language via `CompileHTTP`. The platform carries no language- or framework-specific logic. POST actions are emitted only for handlers that transitively call functions imported from the target language (e.g. `go:` funcs under `--lang go`); other handlers stay pure client-side JS. Static mode errors the build if any page's href is a pattern. Two pages whose route patterns conflict -- some path matches both and neither is more specific -- are a build error too, since net/http panics at startup on the second registration (`routesConflict`, held to ServeMux itself by its test).
    Browser testing via CDP (go-rod) is gated behind `//go:build !js` so WASM playground builds exclude it. A `testing_js.go` stub satisfies the interface for WASM.
- **bubbletea** — generates Go TUI code (`model.go`); supports `golang` lang only.
  `Update` takes the Model by value and hands back the copy, so what the
  program holds lives in a `modelState` the Model embeds by pointer, which
  every copy shares: a closure captures the Model where it is made and writes
  the state a later `Update` hands back. The event loop owns that state, so a
  function literal written as the argument of a call that runs it off the
  loop (`lower.LeavesTheLoop`: `async.start`, a `schedules` native) closes
  over a snapshot taken where it is made instead -- a deep
  copy, through the language's value copy, that it reads and writes alone
  (`golang.OffLoopSnapshots`, `emitSnapshotMethods`). Only a literal written
  there: a named function or a func value handed instead runs on the shared
  state (#273), and so does a func value kept in state that such work calls
  (#274). What such work writes reaches nothing the program shows; it
  reaches the program by **posting back**: `async.do` is a closure sent on
  `__snglPosts`, which Init and every Update after one listen on with a
  `tea.Cmd` and Update runs as a `__snglPostMsg` (`bubbletea/post.go`). The
  closure carries the Model it writes: inside an off-loop literal that is the
  Model the literal was made from, kept beside the snapshot as `__live`
  (`GoIRContext.LiveModel`, `evalPost`), so a post writes the state the loop
  shows and never the copy the work read. A test's `t.wait` runs the posts as
  they arrive, on the goroutine that owns the Model as Update does, and a post
  a finished test left behind writes only that test's Model. Posting touches
  neither #273 nor #274: a post from such work carries the shared Model it
  already wrote. `codegen/platform/bubbletea/async_run_test.go` runs the
  snapshot halves under `-race`.
- **fyne** — generates Go desktop code; supports `golang` lang only. Its Go emitter knows three `#[intrinsic]` primitives, which differ only in the children contract a declaration cannot express as data: `Widget` (none), `Container` (a default slot, children attach through a method) and `Wrapper` (a default slot bounded to one, the child is assigned to a field). *Which* Fyne widget one becomes is a `Spec` record passed as a prop — Go constructor, its arguments, the Go type, the import paths, the setter behind each value prop, the callback field and Go signature behind each event. `codegen/platform/fyne/spec.go` decodes it and nothing else in the platform names a Fyne type. Label, Button, VBox and the rest are ordinary components in `fyne.sngl` carrying a Spec, so wrapping a widget from a Go module the compiler never heard of is writing one more — `codegen/platform/fyne/third_party_widget_test.go` is that, done in SNGL alone. A prop an `Arg` hands the constructor and no `Setter` writes is the constructor's: its write, or a pass's copy of it, is answered there and any other is refused -- a `Grid`'s column count, which `ui.table` is built on (`cmd/sngl/testdata/table_rows_runs.txt`). A top-level const is a file-scope `var` on fyne, gtk4 and bubbletea alike, which a free function reads by name (`golang.PackageConstDecls`, `cmd/sngl/testdata/const_in_free_func_runs.txt`). A loop ranges over a pass's own list as the local it is; only a slot's list is the receiver's field (`OnIter`, `cmd/sngl/testdata/list_lambda_in_record_runs.txt`).

  A value has to reach the emitter through a *declared* prop, since that is what lowering turns into the `node.prop = expr` assignment the translator sees. `style` is the platform's own and renders nothing, as on gtk4 but for its `flex`; a temp its value reads -- a ternary passTernary hoisted -- is read into `_` (`codegen.KeepLocalsRead`, `testdata/style_ternary_unrendered.txtar`). So the primitives declare a vocabulary of value props by type (`text`, `placeholder`, `number`, `flag`, `options`) and `Setter` binds one to a Go method — the vocabulary grows with the types a setter takes, not with the widget count. Same for `@click`/`@change`/`@input` and `Handler`.

  **A child's `flex` and `margin` are the child's own** (`fynelayout.Share`, said where the node is built), not a position in its box: a render slot's anchor and content land among the children the box was built with, and a weight read by position went to a hidden anchor while the scroll that asked for it packed at its minimum (`testdata/flex_after_slot.txtar`). A box with no flexing child of its own is Fyne's `VBox`/`HBox`, and `fynelayout.Adopt` makes it a `Weighted` one when a flexing child is inserted (`InsertBefore`, `Toplevel.Add`) -- a page a navigation stack shows is one.
- **android** — generates Android app code; supports `kotlin` and `golang`. Declares `navigation`: `sngl:ui/nav` is a Compose `NavHost` with typed routes (see *Navigation*), Kotlin only. A window's content that reaches its Screen through a slot -- a window under a reactive `if`, an instance of its own -- is wrapped in a `Column` carrying `safeDrawingPadding`, the inset the direct route puts on each root node (`testdata/android_window_slot_inset.txtar`). A method on a type that reads the page's state is a function of the page taking its receiver first (`pageTypeMethods`, `Pt_sum(p)`), since that state is MainScreen's locals or its test state class's members; one that reads none is an extension (`testdata/type_method_receiver.txtar`). A table is a Column of Rows whose cells each take an equal `weight` of their row, declared as a `Modifier` native in `android.sngl`; a Style's `flex` in a Row is `weight` with `fillMaxHeight`, which stretched each row down the whole column (`cmd/sngl/testdata/table_rows_runs.txt`). A Row or Column's `gap`, `justifyContent` and `alignItems` are its Arrangement and Alignment arguments, and a flex child fills the cross axis only where `alignItems` leaves it stretched: `fillMaxHeight` fills the most a row may be, which made a stats bar the whole screen (`testdata/android_box_layout.txtar`, `testdata/android_flex_aligned.txtar`). A `color` the program sets is the theme's primary, and with none the device's dynamic colours stand; the theme is always light, so the system bars' icons are always dark (`testdata/android_theme_accent.txtar`).

  **An android app asks for what it reaches.** `#[android.permission("…")]`
  (`lib/platform/android/http.sngl`) is written on the native an override
  calls -- `SnglRemoteImage` for `ui.remote` -- and lands on
  `ir.DeclInfo.Permissions`; the manifest asks for each one on a function
  the program calls or a component it renders (`permissions`), so a program
  that reaches no network asks for no `INTERNET`
  (`cmd/sngl/testdata/android_internet_permission.txt`). A call the language
  answers by intrinsic id has no android declaration to mark, so android
  **restates the id** -- `#[permission(…)] #[marks.intrinsic("http.send")] func send(…)` -- which `ir.Builtins.RegisterIntrinsic` allows a target
  package alone, where the parameters agree by name: the declaring package's
  signature stays, and the restatement adds its permissions to the
  `IntrinsicDef` a call's id finds.

  **The screen imports every name explicitly, and never a star**
  (`kotlin.KtIRContext.Name`, the counterpart of Go's `AliasFor`). In Kotlin
  an explicit import outranks a declaration of the file's own package, which
  outranks a star import. So under `material3.*` a program component that
  became `fun Text` captured the emitter's own `Text(…)` calls. The obvious fix,
  an explicit import, would instead capture the program's calls. `Name` takes
  the fully-qualified name (`codegen/platform/android/ktnames.go`), imports it,
  and answers the spelling: the simple name, or `Text_2` imported `as` that
  where another import or a declaration the file makes (`Reserve`,
  `reserveDeclared`) already holds it (`testdata/android_import_alias.txtar`).
  `RequireImport` still spells a name bare and panics where the name is
  taken, as a forced Go alias does. `getValue`/`setValue` are imported as they
  are, a `by` delegate finding them by name, and so is `Toast`, which the
  language's Alert intrinsic writes with no import set to ask -- the one
  Compose-side name `reservedNames` still holds for that reason.

  **A string is quoted as Kotlin quotes one** (`kotlin.Quote`), never with
  Go's `%q`: that writes `\f` and `\x0c`, which Kotlin refuses, and leaves
  `$`, which begins a Kotlin template, so `"$cost"` named a variable
  (`cmd/sngl/testdata/string_escapes_runs.txt`).

  **Nothing the screen writes is Kotlin as text.** The emitter's own
  spellings go through `Name` -- `NameAs` where the file prefers another
  spelling, Compose's `Color` being `ComposeColor` beside the `Color` data
  class SNGL's `color` becomes -- and a fixed text block (the DrawScope
  shims, the NavType) has each name replaced with the file's spelling. A
  `#[kt.native(name, path)]` declaration is spelled by `KtIRContext.NativeName`,
  through `Name` where the path names the declaration or the name is reached
  through it (`Alignment.CenterVertically` beside `androidx.compose.ui.Alignment`).
  The composables' raw `chain` and `args` props are gone for the same reason:
  `then list[Modifier]` takes Modifier values built from the calls
  `android.sngl` declares (`modifier.verticalScroll(rememberScrollState())`),
  and a value SNGL had no way to write is a typed prop and a host const
  (`verticalAlignment=alignCenterVertically`, `color=primaryColor`,
  `shape=mediumShape`). `reserveDeclared` runs before any import, so a
  program's `component modifier` keeps its name and Compose's is
  `Modifier_2` (`testdata/android_compose_names_aliased.txtar`). A host const
  a program reads through its package (`android.modifier`) is the identifier
  it is unqualified (`inferSelect`), on every language.
- **gtk4** — generates CGo GTK4 desktop code; supports `golang` only. Widget metadata is parsed at compile time from a `Gtk-4.0.gir`, resolved by `girRegistry` in one place because the code generator used to resolve its own and the two could disagree: `--opt gir=builtin` selects the bundled subset, any other value is that path and a failure to load it is an error, and an empty value probes the system locations (`/usr/share/gir-1.0/` etc.) and falls back to the bundled subset.

  The GIR reaches the platform as two producer outputs in the generated-file store (`girstore.go`, and see *Performance*): `gtk4.registry`, the parsed registry as SNGL data -- everything the code generator reads back per class, recorded against the GIR file and the probe locations before it that were absent -- and `gtk4.widgets`, the declarations derived from it, whose one input is the registry entry. The second is what the `gir:` scheme's package is -- `lib/x/scheme/gir`'s handler calls the build intrinsic `gir.widgets`, which gtk4 registers (`girwidgets.go`) and which hands it the entry, directive included, recording it and the `gtk4.gir` setting -- and what `Unavailable` answers from, because the checker asks every platform on every build; the first is decoded only by a build that targets gtk4. `lib/platform/gtk4/gtk4.sngl` imports `gtk "gir:Gtk-4.0"` and writes `gtk.GtkLabel`, as a program naming a widget does. A gob cache of the parsed registry used to do the first job on its own, keyed by the GIR's stat and a reflected schema fingerprint -- the compiler's identity in the store's key is what replaced the fingerprint.

  `codegen/platform/gtk4/gir/minimal/Gtk-4.0.gir` is that subset: the ~19 classes `lib/platform/gtk4/gtk4.sngl` wraps, embedded so a host with no GTK 4 development files can still check, document and generate the stdlib overrides — the generated code needs GTK to *build*, which is a separate matter. It is also what the platform's tests read, and **no test reads the host's GIR** (the package's tests empty `girAutoPaths`). Which classes and setter links a system GIR records varies by GTK version, so a test naming host vocabulary asserts GTK's catalogue rather than this platform's behavior and fails on the wrong machine; the tests assert the parse and merge *rules* over every entry of the fixture instead, each with a guard that the rule was exercised. A rule about a class the platform does not wrap -- a setterless property, a construct-only one, a signal the trampoline cannot connect -- is asserted over `codegen/platform/gtk4/testdata/rules.gir`, GTK 4.8's own entries for those classes, which `rulesGIR` splices in after the bundled ones. Adding an override that names a new widget means extending that file, which `TestBundledGIRCoversTheWrappedWidgets` reports.

  A child is added through the call GIR names for the container, its receiver cast to the class that declares the call (`gir.ChildAdder.Owner`): a subclass inherits its parent's adder. A GIR *window* class written as a node is refused: GTK places a window in no container, and `ui.window` is how a program makes one (`cmd/sngl/testdata/gtk4_window_class_refused.txt`).

  Of a `style`, only `flex` is answered: a widget whose literal style flexes calls `gtk4rt.Flex`, which expands it along the axis of the box it lands in, read on `notify::parent` since a render slot builds its content before inserting it. Without it a `ui.scroll` was the few pixels its scrollbar needs. An expand on both axes would have been simpler, but GTK carries an expand up to the parent, so a flexing child of an hbox would have stretched the hbox down its column (`testdata/flex_after_slot.txtar`).

  Snapshot testing uses `gtk_widget_paintable` + `cairo` (CGo); gated behind `//go:build !js`.
- **none** — no codegen and no Go package: `lib/platform/none` and `lib/language/none` are the whole target, registered by `codegen/declared.go` from the layout as a `DeclaredPlatform` and a `DeclaredLang`. The interpreter is the none language's and the none platform's: `sngl test` runs a program through `internal/interp/testrunner` on `none` alone (`testRunnerFor` in `cmd/sngl/test.go`, `codegen.GeneratesNothing`), and a platform with a generator given `--lang none` has no tests to run (`test_interpreter_none_only.txt`). `sngl run` drives an interpreted pair through `interprun` (`build.IsInterpreted`), which is also how fyne's worker host runs `--lang none` -- and the interpreter's claim on `sngl:ui/nav` is declared beside the registration.

## Key Internal Packages

- **`ir/`** — typed IR produced by the checker. `ir.Package`, `ir.Component`, `ir.NodeInst`, `ir.Expr`, `ir.Stmt`. Phases after the checker work from IR rather than re-reading the source, but IR is not AST-free: an operator is still an `ast.BinaryOp`/`ast.AssignOp`, and `ir.NodeInst` and `ir.VarDecl` keep the `ast.Stmt` they came from for positions and diagnostics. That is why `internal/lower`, `internal/optimize` and every platform import `ast`.

  The AST and the IR divide a literal between them: `ast.LiteralExpr.Raw` is
  the source spelling — a backslash and an `n` — and
  `ir.Literal.Value` is what it stands for (a newline). The formatter reprints
  the spelling verbatim, so the checker decodes on the way in
  (`ast.LiteralExpr.StringValue`) and `ir.Convert` re-spells on the way out
  (`ast.EscapeString`). Decoding in the lexer instead is what let `sngl fmt`
  rewrite `"a\nb"` with a raw newline in it.
- **`internal/parser/`** — lexer, recursive-descent parser, formatter for `.sngl` syntax
- **`internal/checker/`** — two-pass type checker (pass1: register declarations, pass2: validate expressions). Both passes run over a *package*: `CheckPackage` takes its documents together, so a type annotated in one file may name a type declared in a sibling, and `Check` is that function for a single document. One set of registrars serves every tier, and one checker checks every package: `loadStdlibPackage` runs the same `pass1`, then the same two declaration halves `pass2` runs -- `checkDeclarationBodies` (every func, component and var-handler body) and `finishDeclarations` (nested-func names, membership, `analyzeEffects` and the purity fixpoint, and the rules judged against it) -- with nothing between them. What `pass2` puts between them is what only a program has: tests, the package body, `output`, schemes, c links, and family inference (a library names its family). A `sngl:` package and a user package otherwise differ in which package a declaration lands in (`declPkg`) and in a few `inLibSource()` policies (internal imports, overrides resolved by the target machinery, a bodyless func answered per target), not in how a declaration is checked. A library loads part-way through a program's check on the same checker, so `enterDeclSet` puts the program's deferred work aside while the library's is done.

**A value written into a slot is held by one function.** An initializer, an
argument, a prop, a struct field, a return, an assignment, a default, a
context value and a spread field all go through `coerce` (`assign.go`), or
`checkExprAs`, which is `checkExprExpecting` followed by it; the expected type
may be nil. A value goes in when it is assignable, when it is the literal `0`
and a unit is wanted, or when it is a zero-argument function whose result is
assignable to what is wanted, which is then called (`var x int = five`; `var y = five` is the function). A colour or date literal is validated wherever it
is written. A `slot` says only what is particular to a position -- the wording
and place of a mismatch, a struct field's kept shape, ref coercion for an
argument. Each position used to restate the rule, and each restatement had
drifted (`testdata/value_slots.sngl` is one line per position).

**A type taken from a value is taken by one function too**: `inferredType`
(`inferred.go`), which every declaration naming no type of its own goes
through -- a var or const (`typeFromInit`, the rule seven sites each restated:
the annotation, or the initializer's type unless it yields nothing), a
context's default, a prop's default, an expression-bodied function's or
lambda's result. It refuses a type that is or holds `null` -- `var x = null`,
`[null]`, `{a=null}`, `context #who(null)` -- as an empty list is refused:
`null` says nothing about what it is the absence of, so the declaration would
hold a type only `null` is assignable to, a context no provider could set
(`error_null_inferred.sngl`). The literal keeps its type, which `x == null`,
a ternary and the option coercion read; only a declaration may not take it. A type parameter nothing in scope declares is refused the same way: a call
that bound none of its callee's parameters left one in its result, and
`var xs = empty()` held a `list[T]` naming nothing
(`error_type_param_unbound.sngl`). An enclosing generic's own parameter is in
scope and is a type like any other, and it is told from a callee's by
reference rather than by name: a type parameter's type carries the
`ir.TypeParamSym` that declared it as its `Decl`, and `pushTypeParams` binds
one symbol per declared parameter however often a declaration's parameters
are pushed (`typeParamSym`), so `empty()` inside `func shadow[T any]` is still
refused.

**A parameter's default is evaluated at the call**, as a component prop's
is: `passCallDefaults` writes it into every call that omits it, since no
emitted function declares one. So a default may not name a parameter of its
function (`refuseDefaultReadingParams`), a call through a handle must pass
any argument whose default reads the component's state, props or methods
(`reportHandleCallDefaults`), and a lambda's parameter takes none
(`error_func_default_reads_param.sngl`, `error_default_through_handle.sngl`).

**Every type parameter names a constraint**, `any` when it says nothing, and
type parameters and arguments are written in brackets, as Go writes them:
`func first[T any](xs list[T]) T`, `component page[T any = struct {}, M any = struct {}]`, `map[string, int]`. The grammar requires the constraint
(`TypeParam = ident Type [ assign Type ]`), because the bracket after a
declaration's name is also an override's target, and the constraint is what
tells `component box[T any]` from `component ui.vbox[platform]`. A target is
a name or a qualified one and nothing else (`TargetIndex`), so after
`[ ident` the next token decides: `.` or `]` is a target, a type is a type
parameter's constraint (`NameBracket`). So `func f[T]` and `component box[T]`
are overrides of an undefined target, and that error suggests `T any`
(`resolveTargetIndex`); a list the parser can tell is no target -- several
names, a default -- is a parse error naming `T any`
(`typeParamWithoutConstraint` in `internal/parser/parse.go`). Both are
`testdata/error_type_param_constraint_required_*.sngl`. A **receiver's** list
restates names only, as Go's does -- `func list[T].map[U any]` -- and in
`FuncName` it is the `.` after the bracket that says so (`FuncNameBracket`);
the target index lives inside `FuncName` too, which keeps `lbracket` out of its
FOLLOW set.

**A call site may write type arguments**, in the same brackets:
`empty[int]()`, `xs.map[string](f)`, `tagged[int](v=1)`, `Box[int]{v=1}`,
`context[option[User]] #who(null)`. A bracket after a name is a postfix index
to the parser (`IndexList`, so `ast.IndexExpr.Indices` is a list), and the
checker says which it is (`internal/checker/typeargs.go`): on a name that
reads as a declaration -- a func, a component, a struct named as a type, a
method -- it holds type arguments, and on a value one index. A func that
takes nothing and binds nothing reads as its result (`counts[0]` for
`counts()`), which `namesItself` holds to. A bracket on a bare name on a line
of its own is split onto the node by the builder too, since `marker[string]`
and `xs[0]` are the same tokens, and the checker refuses the second as a value
that is no statement (`error_expression_statement_index.sngl`). `instantiatedCall` checks the call
without the bracket and leaves the written types for whichever path resolves
the callee (`takeCallTypeArgs`), which seeds `inferTypeParams` with them: they
bind a prefix of the callee's own parameters, inference the rest, a default
what neither reaches. A component's go to `bindComponentTypeParams`, a node
written with them is split by the builder (`ast.VisualNode.TypeArgs`), and a
struct literal after the bracket is `ast.StructExpr.TypeArgs`. **The bracket
holds a type or an expression** (`IndexElem`): a name-led type is parsed as the
expression it is spelled as and read back by `ast.TypeFromExpr`; a func literal
with no body is the `ast.FuncType` it spells, a struct declaration with no
literal after it the `ast.StructDef`, wherever an expression goes, and the
keyword-led types no expression begins with (`component …`, `...T`, `enum`,
`unit`) are the bracket's own alternatives -- each an `ast.TypeExpr` standing
where an `ast.Expr` does, which the formatter prints as a type and the checker
refuses as a value. So a func literal's list admits a parameter that is a type
and no name (`KeywordType`, a qualified or bracketed name), which a func type
lists and the builder refuses everywhere else. A written argument settles the
type of the argument it binds, which is then checked against it rather than
inferred from: `count[func(int) bool]([])`. Nothing after the checker sees them: they reach the IR as the
bindings inference produces, and are kept only so `ir.Convert` prints them
back (`ir.Call.TypeArgs`, `NodeInst.WrittenTypeArgs`, `ir.Context.TypeArgs`; a
generic struct literal always prints its arguments). A method whose receiver
its parameters do not list -- a `sngl:` method's, one on a generic type --
prints as it was written, `xs.map(f)`, which `ir.Call.ReceiverArg` says;
printed `list.map(xs, f)` the receiver went to the first parameter, and no
dump holding a `t.assert` checked again
(`cmd/sngl/testdata/generic_call_roundtrip.txt`).
`testdata/generic_call_site_args.sngl`, `error_generic_call_site_args.sngl`
and `generic_call_site_args.txtar` are the three halves. `any` is `#[_builtin(any)] struct any {}` in sngl:builtin, read like a scalar (`ir.TypAny`, in
`scalarUniverse`, since sngl:family's `one[T any]` is checked before
sngl:builtin is in scope), and it is a constraint only: written where a
value's type goes it is refused (`error_any_outside_constraint.sngl`).
The constraint lands on `ir.TypeParam.Constraint` and
`ir.TypeParamSym.Constraint`, and `ir.Convert` prints one for every parameter
of a declaration's own list, and a method's receiver list as names alone
(`receiverTypeParams`), without which `func Box.get()` named no type
(`cmd/sngl/testdata/type_param_constraint_roundtrip.txt`).

**A constraint is `any` or an anonymous struct** (`resolveConstraint`,
`internal/checker/constraints.go`): `[T struct { name string }]` is any
struct with at least a `name` that is a `string`, matched by name at equal
types (`Type.Equal`), a named struct as readily as an anonymous one. A
constraint's field takes no default, and a satisfying struct's own defaults
play no part. **Every binding is held to it by one function**,
`checkConstraint`: call inference (`inferTypeParams`, which written type
arguments flow into), a struct's or a component type's own arguments
(`applyStructTypeArgs`, `applyComponentTypeArgs` -- the latter is also a
family's arguments in a member's return position), a component's props
(`bindComponentTypeParams`), what the children bind (`bindFromChildren`, for
what the props left), and a default against its own constraint
(`resolveTypeParams`). A T bound to another type parameter is measured by that
parameter's constraint. **A constraint's field may name a parameter of its
list**, `[T any, U struct { value T }]`, or the one it constrains: such a
constraint is never interned, its fields meaning that list's parameters and
no other. It is measured against the bindings substituted in
(`substituteConstraint`), and a parameter nothing else bound is bound from
the field of the type bound to the one it constrains (`inferFromConstraints`,
which `checkConstraints` runs first and `inferTypeParams` runs before the
expected type binds); a default binds its list that way where it is declared,
and a receiver's renamed list renames what the constraint names. In a generic
body a select of a `T` reads the
constraint's fields (`paramConstraint`) and a spread binds them
(`spreadFieldsOf`); a receiver's restated names take their declaration's
constraints (`inheritReceiverConstraints`), before the signature is resolved,
since `func stack[M].current() _page[M]` hands M on. Nothing after the checker
learns of a constraint: Go and Kotlin read the field off each copy
`passMonomorphize` made, and JavaScript and the interpreter off the value. So
i18n's `tr`, `trInline` and `format` take `[A struct {}]`, which is what
refuses a map of arguments, and nav's page, link, `go`, `href` and `_follow`
constrain their params and meta the same way
(`testdata/type_param_constraint.sngl`, `error_type_param_constraint.sngl`,
`type_param_constraint.txtar`).

**A generic function is copied per instantiation** where the language does not
claim `genericFuncs` (`passMonomorphize`), so Go and Kotlin see concrete types
only: Go puts a function reading state on the Model, where no method may take
a type parameter, and states no constraint for a body comparing two T's. Each
copy is named for its type arguments (`at__string`), read off the call's
arguments and its own type, so a parameter only the result names binds too
(`testdata/generic_func_instances.txtar`). **A generic struct is copied the
same way** (`monomorphizeStructs`): `Box[int]` is a declaration `Box__int` with
its fields at the bound types, a method's copy is reattached to the copy its
receiver parameter was bound to, and every `*ir.Type` naming the instantiation
is repointed by `ir.RewriteTypes`, a reflective walk that reaches whatever
holds a type and leaves a frozen library declaration alone. Before it Go
erased the field to `any`, so `box.value + 1` did not compile, and Kotlin
declared it `Any = null` (`testdata/generic_struct_instances.txtar`).

**A bodyless function's purity is its declaration's.** `analyzeEffects` and the fixpoint skip a func with no body (`hasDeclaredBody`): an empty block calls nothing, and read as a body it lifted an undeclared native from unknown to pure -- `error.raise` became foldable the moment library funcs joined the program's fixpoint. And **a write into a value the function built itself is no effect**: one field or element of a local initialized from a literal and never reassigned whole (`freshLocals`), which is how `color.lighten` fills its result. Deeper, or through a local that may share another value (a Go slice, a JavaScript object), stays a mutation.
- **`internal/optimize/`** — constant folding, dead code elimination with platform/language awareness. The optimizer unrolls no loop, for any target: a language target emits the loop and its own compiler decides whether to unroll one whose bounds it can see — three copies of a Compose `RadioButton` were what the loop is. A target whose view is markup (html, which withholds `viewStatements`) has nowhere to run one, and a **`Fold`** (`optimize.NewFold`, handed to a platform as `codegen.Fold` on `Request.Fold`) unrolls its loops after lowering, one document at a time as the platform decides them -- html's `documentsOf`: each is a clone of the lowered window with the loops around it bound for its iteration and its constant view loops unrolled, so a site of a thousand pages holds one page's expanded tree at a time (the docs site peaked at 9 GB holding all of them). A loop in a handler or other script stays a JS loop. A const only such a view loop reads is a build value rather than the page's, and shake keeps it on `ir.Package.BuildConsts`, where the Fold evaluates it and no backend declares it. A static unroll is bounded (`maxStaticUnroll`) and reports rather than writing a page nobody asked for. A read of a root-package list or map const stays a reference to the declaration (`sharedAggregateConsts`) and folds through it where the value is needed; it is copied only where it becomes storage the program may write (`foldOwned`), so every backend must declare its package consts. html's static mode writes such a const once to `assets/consts/` when it emits more than one page. A body the optimizer splices into its caller (`inlineComponentCall`) answers its emits with the call site's handlers, as the lowering's inliners do (`lower.SubstituteEvents`): left standing, a handler the call site wrote never ran, and an event it did not handle became one of the caller's own -- `emit("change", k)` in a build rooted at a component declaring none (`cmd/sngl/testdata/optimizer_splice_events_runs.txt`).
- **`internal/lower/`** — capability-driven IR→IR transformation passes, running between optimizer and codegen. Each pass is gated by a `lower.Features` flag. Languages declare their native capabilities via `Capabilities() lower.Features`; platforms combine that with their own restrictions. Passes include: PropBindings, RefLoop, NoTernary, NoLambda, NoReactivity, etc. Several run always and are not capability-gated because they answer for every target: `ValueCopy` (a store of a struct, list or map -- an assignment, an initializer, a push or insert, a literal's element -- copies what it stores, and a function that modifies such a named parameter binds its own copy at entry while one that only reads it copies nothing, written out by each language from the value's type, since Go shares a slice's storage and JavaScript and Kotlin every object; `ir/valuecopy.go`, `testdata/value_copy.txtar`, `cmd/sngl/testdata/value_copy_runs.txt`), `RefParams` (a method's ambient `this` and a `ref[T]` parameter are passed by reference, copy-in and copy-out: a function writing one hands back what it wrote, in a record beside its own result where it has one, and each call assigns it to the place it passed, so `q.add(1)` is `q = add(q, 1)` -- a pointer would not reach Compose, which recomposes on a reassignment to a value that differs; the interpreter runs it in `Prepare`; `testdata/ref_params.txtar`, `cmd/sngl/testdata/ref_params_runs.txt`), `Effect` (an effect's `@update` and list change events, which every target reports the same way, android's `DisposableEffect` keeping only `@mount`/`@unmount`), `HandleParams` (a program's write of a node's two-way prop lands where the host's report does: through the `#id`, `bound.checked = false` writes the var `:checked` bound, and a call handing the `#id` to a function that writes one through the parameter, `details.open()`, is inlined so its writes are such writes -- a test's `c.b.reset()` too, the bound var, or the unbound prop's cell, read through the test's instance), `ImplicitState` (a two-way prop the call site left unbound, which is a cell of the instance), `DirectCalls` (a call through a name the inliner bound to a declared function -- a func-typed prop given `render=encode` -- becomes a call of that function, since every analysis following the call graph reads `Call.Func`, and an effect keyed on one was settled by no write), `RecordArgs` (an i18n call's struct of arguments, which every runtime takes as a string-keyed map; see *i18n*), `SpreadOnce` (a spread's computed operand, which every field read would otherwise evaluate again), `IndexedIter` (a two-variable loop over a pull sequence, which hands out no ordinal), `ReceiverParam` (the implicit `this` of a method on a generic built-in, which the checker keeps off the parameters for the inliner and which every call already passes first), `CallDefaults` (an omitted argument, whose default no emitted function declares), `StringBuilders` (gated, early: a builder on a target not claiming `stringBuilders` is the string it builds, each add a `+=`; see *A builder* in `design/stdlib.md`), `ForElse` (an imperative for-else, which no host loop expresses), `ViewForElse` (the same construct in a view body, which no platform emitter rendered), `BoundaryFailed` (a boundary's fallback slot, which no platform emitter rendered either), `CSE` (a pure call a statement makes twice), `HoistBodyTypes` (a body-local type whose name another body claims — a component body is not a function scope on any host, so Go and Kotlin need it as much as JavaScript does), `UnprovidedContext` (a context nothing provides, whose constant default is folded into every read — lowered as state instead it is a field nothing writes, and a platform override reading `markup.palette` handed each token a runtime value where a literal was there to be had), `ErrorScope` (a raise resolved against the render tree once each component is spliced where it is rendered) `ErrorCatch` (the catch block a handler body resolved to a boundary becomes, so the raise ends the handler), `NavigationValues` (a `nav.page` read as a value is its record whichever target shows it; see *Navigation*) and `Navigation` (the structure half, which on a target holding `navigation` lowers only the stacks in a surface other than the document). A nineteenth runs always and late: `BoundaryPassthrough`, which splices a boundary with no fallback into what it holds once every pass that reaches a handler through it has run -- a boundary's meaning does not turn on its `@error`, but one with no fallback has nothing left to say once each raise is a catch block, and spliced earlier a handler survived only as a catch block's alias, which no walk follows. `StampUsage` runs after it, so a boundary that only forwarded a raise declares no `ErrorEvent`; `ReservedNames` runs after that, last of all, renaming what every pass above declared (see *A target reserves names*), and its node-ID half `ReservedNodeIDs` right after `NoInlineComponents`. After it a catch block is the only route to the boundary's handler, so an edit codegen makes in every handler walks one (html's `walkThroughCatch`): its slot retargets missed it, and a fallback a runtime instance's mount raised into rendered at the wrong parent. `CSE` is statement-local and imperative-only on purpose — the temp it binds has to be a statement the target can hold, and a view body on `--lang none` cannot hold one. Entry point: `lower.Lower(pkg, caps, opts)`.
- **`internal/lsp/`** + **`internal/lspcore/`** — Language Server Protocol implementation (hover, completion, diagnostics)

## Runtime packages for generated code

SNGL ships per-target-language runtime packages under `pkg/<lang>/<name>/`. These contain Go/Kotlin/JS code that generated programs import. Examples:

- `pkg/go/i18n/` — Go runtime backing the `i18n` SNGL stdlib package (manifest loader, ICU formatter, date/number formatters wrapping `golang.org/x/text` and `github.com/goodsign/monday`).
- `pkg/js/i18n/` — JavaScript runtime: ICU template parser plus `Intl.NumberFormat`/`DateTimeFormat`/`PluralRules`. Manifest inlined as `globalThis.__SNGL_I18N_MANIFEST__` by the html platform.
- `pkg/kotlin/i18n/` — Kotlin/Android runtime: ICU parser plus `android.icu.text.*`. Manifest loaded from `assets/i18n.manifest.json` via `I18n.init(context)` in `Application.onCreate` (currently injected into `MainActivity.onCreate` since the scaffold has no custom Application).

The `lib/` directory holds **SNGL stdlib declarations** (language-agnostic `.sngl` files embedded into the compiler). The `pkg/` directory holds **runtime implementations** (per-target-language packages emitted into generated code's import graph).

When adding a new stdlib package that needs runtime support:
1. Declare the SNGL surface in `lib/<name>/`, one directory per importable package.
2. For each target language that needs runtime support, create `pkg/<lang>/<name>/`.
3. The codegen for that language emits `import "duckfam.us/sngl/pkg/<lang>/<name>"` and translates stdlib calls to that package's API.

## AST

- **`ast/ast.go`** — top-level declarations and structural types: `Document`, `ComponentDecl`, `VisualNode`, `FuncDef`, `VarDecl`, `ConstDecl`, `StructDef`, `EnumDef`, `UnitDef`, `Param`, `Import`, `IfStmt`, `ForStmt`
- **`ast/expr.go`** — expression nodes and statements: `BinaryExpr`, `UnaryExpr`, `CallExpr`, `SelectExpr`, `IndexExpr`, `TernaryExpr`, `LiteralExpr`, `IdentExpr`, `ListExpr`, `StructExpr`, `LambdaExpr`, `InterpolationExpr`, plus `AssignStmt`, `EmitStmt`, `StmtBlock`, etc.
