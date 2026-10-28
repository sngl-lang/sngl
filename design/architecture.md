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

**Two more forks trim what the CLI embeds.** `github.com/sngl-lang/typescript-go` (branch `sngl-rename`) carries no translated diagnostics, since SNGL reports in English and all thirteen catalogs were linked (1.1 MB). `github.com/sngl-lang/chroma` (branch `sngl-v2`, from v2.27.0) keeps only the lexers SNGL shows -- the repository's fences, its targets and import schemes, and the config formats beside them -- for 1.9 MB. A fence in any other language, in the docs or in a program's `md:` markdown, renders as plain text; a lexer comes back by restoring its file from upstream in the fork.

## Codegen Models

Platforms choose between two intermediate representations based on their rendering approach:

- **MutationModel** (`codegen/model.go`) — emit a static tree once, then generate targeted `Updater` functions to patch when state changes. Used by HTML and Fyne. Interfaces: `MutationModelEmitter`.
- **RenderModel** (`codegen/model.go`) — re-render the full view from state on every change; framework handles diffing. Used by BubbleTea and Android/Compose. Interfaces: `RenderModelEmitter`.

Both start from `codegen.AnalyzeCommon(doc)` which extracts model fields, computed deps, functions, structs, and timers into a platform-independent `CommonAnalysis`. That struct is read-only to a generator and holds no `*ir.Package`: what a generator *writes* as it emits — helper flags, registered CSS — is `codegen.Emission`, which a platform embeds beside the analysis. Keeping the two apart is what makes `dump --stage analysis` a dump of the analysis rather than of the whole IR graph plus the emitter's scratch space.

## Platform Details

**A platform translates a `ui.Style` in SNGL.** Each override passes its
`style` to a `const func` of its own package -- `lookOf` in
`lib/platform/<name>/style.sngl` on bubbletea, fyne and gtk4, `modifierOf`,
`textStyle` and the arrangements on android, `css` in `html.sngl` on html --
over the host declarations the package already has, and the Go generator
names no Style field. One function serves a literal Style and a held one: a
literal holds only the fields written (design/types.md), so a literal's
translation folds to what it sets. `flex` is read by the parent: a context
on android, the Layout on bubbletea, the Box on fyne, the box the widget lands
in on gtk4, CSS on html.

**`ui.spacer` is `flex: 1` on every platform**, its own `flex` a share in
its place: each override writes `{flex=1, ...style}` (bubbletea, reading
the one field, `style.flex == null ? 1.0 : style.flex`). It grew unasked on
html, gtk4 and fyne and not at all on android and bubbletea, so a toolbar's
spacer pushed nothing there (`cmd/sngl/testdata/spacer_grows_runs.txt`).

**A field a platform cannot express is listed, with a reason**, in its
`const unstyled list[string]`, html's empty. `lib`'s
`TestEveryPlatformAnswersEveryStyleField` holds the fields each platform's
package reads off a Style, together with that list, to every field of
`ui.Style`, so a field added there fails each platform until it reads it or
says why not, and a listed field read is a stale reason. `none` draws
nothing and is not held to it. A program's write of a listed field does not
warn: only a literal shows the write, and a multi-target build would warn per
target with no way to say a field is meant for one.

- **html** — two modes selected by `--lang`. Every element's `style` is the `list[Declaration]` `css` answers for a Style, so an override writes `style=css({…, ...style})` and a program writing a raw element writes `style=html.css({…})`; the fold reduces each declaration the build can read to text in the markup, and one only the page knows is a `setProperty`, grouped so a shorthand written empty clears no longhand set beside it (`_snglSetStyles`). A side is a longhand of its own, the first of its own field, its axis's and the whole's that is set, so a whole the page writes leaves a side the build wrote where it was (`cmd/sngl/testdata/style_side_zero_runs.txt`'s `kept`); written as the `padding` shorthand after the side's static attribute, each write reset it. A node's updaters run after a handler only where the lowering wrote none into it: NoReactivity writes every reader after each write, a node carrying the program's own `#id` included, which html once took for a node it had not lowered and updated twice (`inlineUpdated`, `testdata/html_handler_updates_named_node_once.txtar`). Its overrides of `ui.badge`, `ui.chip` and `ui.avatar` render what sngl:ui's bodies do and carry `sngl-<name>` classes, with a modifier per choice (`sngl-badge-success`, `sngl-avatar-large`), so a page's stylesheet reaches every one: A constant loop's copies of a node carrying an `#id` are elements of their own, each with its own variable (`nodeID`, `testdata/html_loop_node_id.txtar`).
  - A style's `flex` is written with `min-width:0;min-height:0`: a flex item's minimum is its content's in CSS, so a box given a share grew with what it held, a scroll in it never scrolled, and a grid loading a page at its end loaded them all (`testdata/html_flex_child_bounded.txtar`).
  - JavaScript's `===` is an object's identity, so `==` between structs, colors included, payload enums and lists is written out field by field and element by element from the type (`jsEqualExpr`); written as `===`, two colors alike were unequal (`cmd/sngl/testdata/struct_equality_runs.txt`, `struct_list_equality_runs.txt`). A test runs in a module beside the page and finds the page's funcs and consts on window, where a test build publishes both.
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

  A Layout joins its parts with `tui.Spaced` and `tui.Join`, which leave out
  a part that draws nothing and put the Layout's `spacing` between two that
  draw; a loop's copies and a branch's nodes are joined the same way, being
  parts of the Layout they are in (`irViewContext.join`), so a loop of badges
  is spaced as badges written out are, and the panels of the tabs not shown
  were each a blank line. A cell is one pixel wide and two tall, so a length
  across is `cellsOf` and a height or a column's gap is `rowsOf`, twice the
  pixels to the cell (`lib/platform/bubbletea/style.sngl`): a 192px image
  was 24 rows around a 12-row picture, and a picture fits the size its box's
  look gives (`tui.PictureBox`). A component whose body grows into a
  Layout's free space is handed the width it is placed in (`__availW`), and
  the view's own is the terminal's, 80 where it has said none (`tui.Avail`),
  so a table's columns after a flexing one line up row to row. A Layout's
  `alignItems` also places the joined block in a box wider or taller than it
  (`AlignHorizontal`), so a card's names sit in its middle
  (`cmd/sngl/testdata/layout_bubbletea_rendered_runs.txt`,
  `cmd/sngl/testdata/layout_bubbletea_aligned_runs.txt`). A growing child
  under an `if` or a `for` is a part of the Layout as one written directly
  is: the statement is written around the parts, each node it renders one of
  its own (`renderIfParts`, `renderForParts`), where joined as one part the
  grower took only its own size
  (`cmd/sngl/testdata/style_bubbletea_flex_under_if_runs.txt`). One holding a
  loop's focus stop or canvas is still joined at its own size, since the
  closure it is rendered in would count its copies after its later
  siblings'.

  What a lipgloss style cannot say is a `tui.Fit` beside the look, which
  `fitOf` builds from a Style's `whiteSpace`, `overflow` and `display` and
  each primitive renders through (`tui.Render`): `nowrap` and `pre` break no
  line and cut one wider than the box, since a terminal has nothing beyond the
  box to draw it on, where lipgloss's own Inline would also have dropped the
  box's padding, border and margin; an overflow that is not visible cuts what
  is taller than the box inside its frame, where lipgloss grows the box past
  its height; and `display=none` draws nothing and takes no line, nor a focus
  stop: each override's `Focus` record is enabled where its Style does not
  display it none, as CSS focuses no element displayed none. A component
  rendered by a method of its own may draw nothing as a node may, so its part
  is left out of its parent's where it is empty (`appendPart`): joined, the
  empty string was a blank line
  (`cmd/sngl/testdata/style_bubbletea_display_none_runs.txt`). A literal
  Style's Fit folds to `tui.Fit{}` and the primitive renders as it did. A
  Layout's `justifyContent` places its parts in what they leave of its extent
  (`tui.Join`), which it learns as it does for a growing child, and where a
  child grows the free space is the child's
  (`cmd/sngl/testdata/style_bubbletea_fit_runs.txt`).
- **fyne** — generates Go desktop code; supports `golang` lang only. Its Go emitter knows three `#[intrinsic]` primitives, which differ only in the children contract a declaration cannot express as data: `Widget` (none), `Container` (a default slot, children attach through a method) and `Wrapper` (a default slot bounded to one, the child is assigned to a field). *Which* Fyne widget one becomes is a `Spec` record passed as a prop — Go constructor, its arguments, the Go type, the import paths, the setter behind each value prop, the callback field and Go signature behind each event. `codegen/platform/fyne/spec.go` decodes it and nothing else in the platform names a Fyne type. Label, Button, VBox and the rest are ordinary components in `fyne.sngl` carrying a Spec, so wrapping a widget from a Go module the compiler never heard of is writing one more — `codegen/platform/fyne/third_party_widget_test.go` is that, done in SNGL alone. A prop an `Arg` hands the constructor and no `Setter` writes is the constructor's: its write, or a pass's copy of it, is answered there and any other is refused -- a `Grid`'s column count, which `ui.table` is built on (`cmd/sngl/testdata/table_rows_runs.txt`). A top-level const is a file-scope `var` on fyne, gtk4 and bubbletea alike, which a free function reads by name (`golang.PackageConstDecls`, `cmd/sngl/testdata/const_in_free_func_runs.txt`). A loop ranges over a pass's own list as the local it is; only a slot's list is the receiver's field (`OnIter`, `cmd/sngl/testdata/list_lambda_in_record_runs.txt`). A style's `width` and `height` are the node's Box's, which lays it out at that size in place of its own minimum, since Fyne asks no widget to be a size; a bold or italic label also gets that face as its TextStyle, which it measures itself in, beside the theme that draws it; and an instance whose body renders no node is `fynelayout.Nothing()`, hidden, where it was a blank label taking a row (`testdata/fyne_style_size_face_nothing.txtar`, which pins gtk4's hidden root too). A label's background is not painted: Fyne's theme colors no label's box, so a `ui.badge` reads as plain text there.

  A value has to reach the emitter through a *declared* prop, since that is what lowering turns into the `node.prop = expr` assignment the translator sees. So the primitives declare a vocabulary of value props by type (`text`, `placeholder`, `number`, `flag`, `options`) and `Setter` binds one to a Go method — the vocabulary grows with the types a setter takes, not with the widget count. Same for `@click`/`@change`/`@input` and `Handler`. A node's Style is `lib/platform/fyne/style.sngl`'s to translate: `lookOf` builds the `look` a primitive takes, a `fynelayout.Look`, and a node whose look folds to nothing is not wrapped, while any other is shown in a `fynelayout.Box` that draws its margin, border, fill and padding and puts it under a theme of its own. A vbox or hbox is a `fynelayout.Weighted` layout, which reads each child's `flex` share off its Box (`testdata/fyne_style_fields.txtar`). A Style's `cursor` is shown over the node's Box by a pointer area drawing nothing, which a desktop driver asks, as it asks any `desktop.Cursorable`, walking down the tree under the pointer and taking the last it meets: the area sits over a widget the Box shows, so an entry gives its text cursor up to its Style's, and under a container's children, so a box inside one says over it. A widget a styled container holds keeps its own cursor, where CSS's would inherit the container's (`pkg/go/fynelayout/cursor_test.go`, `testdata/fyne_style_cursor.txtar`).

  **A child's `flex` is the child's own**, read off its Box, not a position in its box: a render slot's anchor and content land among the children the box was built with, and a weight read by position went to a hidden anchor while the scroll that asked for it packed at its minimum (`testdata/flex_after_slot.txtar`). A box built as Fyne's `VBox`/`HBox` -- a window's, a page a navigation stack shows -- is made a `Weighted` one by `fynelayout.Adopt` when a growing child is inserted (`InsertBefore`, `Toplevel.Add`). A render slot's `InsertBefore` lays its box out again and refreshes nothing it already held: a refresh reaches every child, and a theme override refreshed is a new theme scope, which Fyne's text-metrics cache is keyed by, so each row a slot inserted measured every themed label in the box again -- a table of fifteen moves, drawn again for each of its fifteen answers, took seconds (`TestInsertingRefreshesNoOtherChild`).
- **android** — generates Android app code; supports `kotlin` and `golang`. Declares `navigation`: `sngl:ui/nav` is a Compose `NavHost` with typed routes (see *Navigation*), Kotlin only. A window's content that reaches its Screen through a slot -- a window under a reactive `if`, an instance of its own -- is wrapped in a `Column` carrying `safeDrawingPadding`, the inset the direct route puts on each root node (`testdata/android_window_slot_inset.txtar`). Each Column the generated code writes rather than a vbox -- that one, a window's several nodes, a nav page's -- provides the share a flexing child takes there through Compose (`LocalFlexShare`, in the `flexshare.kt` the platform ships), and flexShare's default, `snglFlexShare`, reads it, else the screen: a slot's content is composed where the slot is invoked, which no closure its caller wrote could name, and a child there read the screen's share and filled it (`testdata/android_flex_in_written_column.txtar`, `cmd/sngl/testdata/android_flex_written_column_runs.txt`). A method on a type that reads the page's state is a function of the page taking its receiver first (`pageTypeMethods`, `Pt_sum(p)`), since that state is MainScreen's locals or its test state class's members; one that reads none is an extension (`testdata/type_method_receiver.txtar`). A table is a Column of Rows whose cells each take an equal `weight` of their row, declared as a `Modifier` native in `android.sngl`; a Style's `flex` in a Row is `weight` with `fillMaxHeight`, which stretched each row down the whole column (`cmd/sngl/testdata/table_rows_runs.txt`). A Row or Column's `gap`, `justifyContent` and `alignItems` are its Arrangement and Alignment arguments (`columnArrangement` and the rest in `lib/platform/android/style.sngl`), and a flex child fills the cross axis only where `alignItems` leaves it stretched: `fillMaxHeight` fills the most a row may be, which made a stats bar the whole screen (`testdata/android_box_layout.txtar`, `testdata/android_flex_aligned.txtar`). An `option` argument a call hands null is left out, so Compose's default stands. A Style's `maxLines` and `whiteSpace` are Text's `maxLines`, `overflow` (an ellipsis) and `softWrap`: each answers the prop's declared default where the field is unset -- a host const, `wrapsLines` rather than `true`, since an argument is left to Compose only where it names the host value the default names -- so a text that sets neither passes neither (`testdata/android_text_lines.txtar`). The select is an override holding whether its menu is open; the semantics a test chooses through is `snglChooses`, a Kotlin modifier the platform ships (`chooses.kt`), since Compose spells it as a lambda with a receiver. A `color` the program sets is the theme's primary, and with none the device's dynamic colors stand; the theme is always light, so the system bars' icons are always dark (`testdata/android_theme_accent.txtar`).

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
- **gtk4** — generates GTK4 desktop code over `pkg/go/gtk4rt`, or inline cgo where a program needs it (*Which file a program gets*, below); supports `golang` only. Widget metadata is parsed at compile time from a `Gtk-4.0.gir`, resolved by `girRegistry` in one place because the code generator used to resolve its own and the two could disagree: `--opt gir=builtin` selects the bundled subset, any other value is that path and a failure to load it is an error, and an empty value probes the system locations (`/usr/share/gir-1.0/` etc.) and falls back to the bundled subset.

  The GIR reaches the platform as two producer outputs in the generated-file store (`girstore.go`, and see *Performance*): `gtk4.registry`, the parsed registry as SNGL data -- everything the code generator reads back per class, recorded against the GIR file and the probe locations before it that were absent -- and `gtk4.widgets`, the declarations derived from it, whose one input is the registry entry. The second is what the `gir:` scheme's package is -- `lib/x/scheme/gir`'s handler calls the build intrinsic `gir.widgets`, which gtk4 registers (`girwidgets.go`) and which hands it the entry, directive included, recording it and the `gtk4.gir` setting -- and what `Unavailable` answers from, because the checker asks every platform on every build; the first is decoded only by a build that targets gtk4. `lib/platform/gtk4/gtk4.sngl` imports `gtk "gir:Gtk-4.0"` and writes `gtk.GtkLabel`, as a program naming a widget does. A gob cache of the parsed registry used to do the first job on its own, keyed by the GIR's stat and a reflected schema fingerprint -- the compiler's identity in the store's key is what replaced the fingerprint.

  `codegen/platform/gtk4/gir/minimal/Gtk-4.0.gir` is that subset: the ~19 classes `lib/platform/gtk4/gtk4.sngl` wraps, embedded so a host with no GTK 4 development files can still check, document and generate the stdlib overrides — the generated code needs GTK to *build*, which is a separate matter. It is also what the platform's tests read, and **no test reads the host's GIR** (the package's tests empty `girAutoPaths`). Which classes and setter links a system GIR records varies by GTK version, so a test naming host vocabulary asserts GTK's catalog rather than this platform's behavior and fails on the wrong machine; the tests assert the parse and merge *rules* over every entry of the fixture instead, each with a guard that the rule was exercised. A rule about a class the platform does not wrap -- a setterless property, a construct-only one, a signal the trampoline cannot connect -- is asserted over `codegen/platform/gtk4/testdata/rules.gir`, GTK 4.8's own entries for those classes, which `rulesGIR` splices in after the bundled ones. Adding an override that names a new widget means extending that file, which `TestBundledGIRCoversTheWrappedWidgets` reports. **The C gtk4rt and the scaffold write is held to GTK 4.8**, Debian bookworm's, which CI builds on: a call a later GTK added sits behind `GTK_CHECK_VERSION` with the 4.8 call beside it (`gtk_css_provider_load_from_string`, 4.12's, is `load_from_data` before it), since a host's newer headers compile it and CI's link fails. The headers' `GDK_AVAILABLE_IN_4_*` marks say which.

  A GIR class is made by what `declgen` declares beside it: a `#[gen.protocol(create)]` function (`_create_GtkLabel`), which `passDeclarative` splices into the `createNode` op (`design/nodes.md`). By default it calls `gtk4rt.New` with the class's name, as a `#[go.native]` (`_rtNew_GtkLabel`): `g_object_new` on the type GTK registers under that name, which is what a GTK 4 constructor given no arguments does, the props being written once it returns. Under the `cgo` option it calls the class's primary constructor as a `#[cnative]` (`_gtk_label_new`) with a zero per parameter, so the constructor's arguments are IR `CMarshal` and `ReleaseTemps` saw. A string GTK refuses NULL for is `""` -- `gtk_link_button_new` makes no widget for NULL -- and lives for the application, made once and freed at teardown; a nullable string is an `option[string]` handed `null`, any other pointer an `option[c.pointer]`; a number crosses as cgo's `int` or `double` where its C type is that and through a cast to its own C type otherwise (`_to_GtkOrientation`, struct `_c_GtkOrientation`, prefixed because GTK has a `float` parameter whose struct would shadow SNGL's), since cgo converts no named C number to another. A class whose constructor has a parameter none of those spells gets no create, and rendering it is the build error `unconstructible` words (`TestUnconstructibleClass_FailsTheBuild`), whichever protocol is written; the translator builds no constructor of its own, and keeps where the handle lives (`testdata/gtk4_declared_create.txtar`).

  A child is added through the call GIR names for the container: a subclass inherits its parent's adder (`gir.ChildAdder.Owner`). By default the `#[gen.protocol(append)]` and `#[gen.protocol(remove)]` `declgen` writes call gtk4rt's wrapper of that call where it has one (`rtChildWrappers`: `BoxAppend`, `BoxRemove`, the window's, a scrolled window's and a frame's `SetChild`) and otherwise `gtk4rt.ChildCall`, which is handed the call's name and finds it in the GTK the program links: every adder and remover GIR names takes the container and the child and nothing else. Under the `cgo` option, its receiver is cast to the class that declares the call, and `declgen` writes the call as a `#[cnative]` taking the C pointers GIR declares (`_gtk_box_append`) and a `#[gen.protocol(append)]` function (`_append_GtkBox`) handing it the two nodes through casts it declares too -- `_as_GtkButton(_ptr_GtkLinkButton(parent))` is `(*C.GtkButton)(unsafe.Pointer(…))`, since cgo converts no C pointer to another, and `_ptr_` takes the class itself because a family value would reach Go boxed in an `any` -- and a `#[gen.protocol(remove)]` beside it where GIR names a `remove` taking one widget and the class holds more than one child (`gir.ClassInfo.ChildRemove`; the bundled subset carries `gtk_box_remove` for it). Lowering splices both (`design/nodes.md`, `testdata/gtk4_declared_append.txtar`), and the translator emits the call with each node put where its scope holds it. It keeps the application's attach, the window icon, and a render slot's parent: a slot renders into a GtkBox whatever its container is declared as, since a single-child container holds the slot's box, so the slot's insert is the preamble's `sngl_insert_before` and a remove with no call spliced is `gtk_box_remove`. The tags no GIR class is behind declare theirs in `gtk4.sngl`: a scroll's child goes in through `gtk4rt.ScrolledWindowSetChild`, an image's source in and out through `gtk4rt.BoxAppend` and `BoxRemove`, and a window's content the same, each a `#[go.native]` so that both modes make the same calls, while a flow's spans are no widgets and the canvas takes no child (`testdata/gtk4_declared_scroll.txtar`, `testdata/gtk4_declared_image.txtar`). A GIR *window* class written as a node is refused: GTK places a window in no container, and `ui.window` is how a program makes one (`cmd/sngl/testdata/gtk4_window_class_refused.txt`).

  A handler is connected by the `#[gen.protocol(connect)]` `gtk4.sngl` declares once, which lowering splices into every `attachHandler` on a widget a declared create made (`design/nodes.md`): `gtk4rt.Connect` as a `#[go.native]`, handed the node, the signal under the name GTK knows it -- declgen writes `#[gen.connects("notify::active")]` on `@notifyActive`, and on any signal SNGL spells otherwise -- and the handler. gtk4rt registers the handler through `cbind` and copies the signal into C itself, so neither mode's generated file calls C to connect one; the translator puts the node where its scope holds it, as a `Handle`, and the handler in the parameter lowering left for it (`connectCall`), and keeps `recordInvoker` and the signal lambda's `self`, which only the op and the scope know (`testdata/gtk4_declared_connect.txtar`). A connect marshaled in IR -- a `c.str` a pass made and freed -- was the plan, and would have left a `C.` in every program with a handler, sending all of them down the cgo path and wrapped mode's cached build with them: 145 goldens recorded wrapped output with a `gtk4rt.Connect`. `snglCallbacks` stays Go rather than becoming a `gtk4.sngl` package var: `callbacks.go` is fixed text, and could read such a var only under a name the translator spelled for it, while the registry wrapped mode already used serves both modes. It now holds only the canvas's click (`sngl_connect_click`), which goes with the canvas's create and draw function when #294 retires `passShapeDraw`: `draw.canvas` is `sngl:ui/draw`'s own component, not a gtk4 `#[intrinsic]`, so it has no protocol to declare. An event GTK raises no signal for is connected by its component's own `#[gen.protocol(on)]` instead: a scroll's `@reachEnd` is gtk4rt's `ScrollOnReachEnd`, which watches the vertical adjustment, an image's `@click`, `@load` and `@failed` are gtk4rt's `ImageFrameOn*`, and a window's `@closed` and the host's report of `visible` are gtk4rt's `ToplevelOn*`. An image's `fit` and a URL source's `picture` are written by sets `gtk4.sngl` declares over gtk4rt; a source's `url` is what it says it shows, which a test reads and no widget takes, and a drawing's size is read off the drawing where it is painted, so neither has a set and the translator answers both with nothing. A window is gtk4rt's toplevel in both modes, its title and `visible` written by sets over gtk4rt; the inline-cgo scaffold, which has no runtime loop and makes its one window in BuildUI, gives that window the title the set stored through `gtk4rt.ToplevelTitleWindow`, so the title's C copy is gtk4rt's (`testdata/gtk4_declared_toplevel.txtar`). What the scaffold still refuses -- a second window, `@closed`, `visible` written while it runs -- it refuses for want of the loop, not of the calls. A flow of rich text is one label made by `gtk4rt.FlowNew` and written by its `markup` prop's set, `gtk4rt.LabelSetMarkup`; the translator still assembles that markup from the span tree, and calls the set with it wherever the label is written. A span is no widget and declares no protocol, so lowering puts nothing into the flow for it (`isNoWidget`, `testdata/gtk4_declared_flow.txtar`).

  A prop is written by the `#[gen.protocol(set)]` declgen writes for it, which lowering splices into every write of it (`design/nodes.md`). By default it is gtk4rt's: where `gtk4rt` wraps the setter GIR names (`rtSetterTable` -- a label's or a button's text, a check button's state, a box's spacing and orientation, an entry's text, an image's file) the set calls the wrapper as a `#[go.native]`, as the connect does, and otherwise `gtk4rt.SetString`, `SetBool`, `SetInt` or `SetFloat` with the property's GIR name, which GObject converts to the type its `GParamSpec` declares -- an enumeration's member reaches it as its value, through `SetInt`. gtk4rt copies a string into C itself, and on the cgo path the node is handed over as a `Handle` (`setThrough`). Under the `cgo` option it is GIR's setter as a `#[cnative]`, its receiver cast to the class or interface declaring it (`girSetter`), and where GTK ships no setter the preamble's `sngl_set_prop_*` by the property's GIR name, which GObject converts to the type the property's `GParamSpec` declares. A string is a `string` to the native, which `CMarshal` converts and `ReleaseTemps` frees -- an `option[string]` where GIR marks the setter's parameter nullable -- a number crosses as the create's do, a bool as `v ? 1 : 0` through `gboolean`, and an enumeration's member, which a program writes as its name, is a `match` over the names GIR lists to the member's value through the enum's cast, folded where the set is spliced; a name no arm holds is refused there (`TestEnumProp_RejectsUnknownMember`). The translator keeps `look`, a copy's and an instance root's writes, the props no widget takes (an image source's `url`, a drawing's and a canvas's size), and the refusals of a write no set answers: a prop GIR describes none of, a construct-only one, and one whose type no SNGL value produces (`testdata/gtk4_declared_set.txtar`). An entry's quiet write is `gtk4rt.EditableSetTextQuiet` declared as `setEntryText`, and `readWidgets` hands any gtk4rt native taking a widget the widget as a `Handle`. No golden flipped to cgo with the sets declared, and one handler closure in a `for` row (`testdata/indexed_trigger_nested.txtar`) now writes the label it used to drop: the translator answered that write only in a scope that knew the node's C type.

  **Which file a program gets.** Every build emits wrapped first -- gtk4rt calls over a `Handle`, no `import "C"` -- and re-emits as inline cgo when a `C.` is left (`emitIR`). With the declared protocol calling gtk4rt, what leaves one is the program's own: a canvas or a drawing image, whose paint is cairo `#[cnative]`s, and a `#[cnative]` it calls, a GIR action (`bar.pulse()`) among them. Of 254 gtk4 goldens, 223 record wrapped output and the 31 that do not do one of those. `gtk4(cgo=true)` is the opt-out: `declgen` writes the per-class `#[cnative]` protocol instead and the file is cgo whatever it renders, so every string handed to GTK is a `CMarshal` temp `ReleaseTemps` frees, which the interpreter's tracker follows under `--simulate gtk4`, where it runs the same protocol functions (`design/ownership.md`); by default the strings the generic calls take are gtk4rt's to copy and free. The option is the widget package's as `gir` is: it is in the `gtk4.widgets` entry's key, the `gtk4.cgo` setting the `gir:` scheme records and `LibraryState`.

  Wrapped mode was kept against retiring it with `wrapped.go` and the `rt*` tables on what it was written to save, measured on 2026-10-24 as a fresh `go build` of the generated program (go1.27.2, 12 cores, median of three; cold is an empty `GOCACHE`, warm the cache a build of the same program left, with one line of `model.go` changed):

  | Program                             | Path                   | Cold  | Warm  |
  |-------------------------------------|------------------------|-------|-------|
  | A label and a button that writes it | wrapped                | 13.9s | 0.85s |
  | The same                            | cgo                    | 16.1s | 3.1s  |
  | `examples/pokedex`                  | cgo, before the change | 19.4s | 6.0s  |
  | `examples/pokedex`                  | wrapped                | 13.7s | 1.6s  |

  The difference is cgo running over the gtk.h preamble, which wrapped mode pays once, in gtk4rt: 3.4s of pokedex's warm 6.2s was its `cgo run`. Pokedex was on the cgo path for 46 lines -- a progress bar's, a spinner's and a separator's create and sets, a tooltip, `sensitive`, a button's child -- which gtk4rt wrapped none of; the generic calls cover every class GIR describes, so a wrapped build no longer depends on what someone wrapped by hand. Retiring the mode would also have taken what only its scaffold has: `@run`, a second window, `@closed`, `visible` written while the program runs. It is the default protocol rather than a second one beside it, so there is one switch, the option, and not a check of each op; `rtCtorForCType`, which put gtk4rt's constructors over the declared create in wrapped mode, went with the change.

  A `style` is `lib/platform/gtk4/style.sngl`'s to translate: `lookOf` builds the `gtk4rt.Look` a primitive takes, and `gtk4rt`'s `look.go` applies it -- a width and a height a size request, a box's `alignItems` where it puts its children, the rest CSS on a provider of the widget's own -- and gives back what a later Look leaves unset. A size request is only the least a widget is, so a label is exactly its look's size by a custom layout that asks GtkLabel's own measure at that width and wraps its words, as html's do: a size request let a long name widen its card (`sngl_look_size_label`). A GtkLabel centers its words in its room, so `ui.text` sets `xalign` 0, starting a line at the left as html does, and a style's `textAlign` moves it (`cmd/sngl/testdata/style_gtk4_aligned_runs.txt`, `cmd/sngl/testdata/style_gtk4_rendered_runs.txt`). The look's rule is at application priority, so it outranks the theme's in every state: a button given a `background` was the same color hovered and pressed, and its rule now shades the background itself under `:hover` and `:active` with a translucent `currentColor` image over it, as libadwaita's buttons do (`pkg/go/gtk4rt/paint_test.go`, which paints the widget it asks about; gtk4rt's tests draw with GSK's cairo renderer, since GL under cage aborted in epoxy once a second window was shown).

  Of `flex`: a widget whose literal style flexes calls `gtk4rt.Flex`, which expands it along the axis of the box it lands in, read on `notify::parent` since a render slot builds its content before inserting it. Without it a `ui.scroll` was the few pixels its scrollbar needs. An expand on both axes would have been simpler, but GTK carries an expand up to the parent, so a flexing child of an hbox would have stretched the hbox down its column (`testdata/flex_after_slot.txtar`). A GtkBox splits its spare room evenly between the children that expand and packs them from the start, so a box whose look justifies, or whose growing children ask for unequal shares, is laid out by gtk4rt's own layout manager (`sngl_flex_measure`, `sngl_flex_allocate` in `look.go`): each child its minimum, then up to its natural size as GTK gives it, then the spare room by weight, and what no child takes placed as `justifyContent` says. A child's weight is its look's `flex`, else one share where it expands itself, a GIR widget given `hexpand`; an expand GTK carries up from something the child holds is none, since a CSS flex item grows by its own flex and never by what is inside it -- a scroll holding an expanding widget took a share beside the weighted children. The box is gtk4rt's to lay out where such a child sits beside a weighted one, as GTK's would give it a share; a box with no weight at all is left to GTK's, carried expands and all. GtkBox keeps its orientation in its layout manager, so a box gtk4rt lays out is asked its axis through the GtkBoxLayout it kept aside (`sngl_box_axis`): asked directly, it answered horizontal with a critical on stderr, and a column was laid out as a row. It gives the box GTK's `GtkBoxLayout` back when the box asks for neither. A box's spacing is then the layout's, and GtkBox's own setter would reach a layout that is not a `GtkBoxLayout`, so the overrides hand a box's spacing to its look (`boxLookOf`) and gtk4rt sets it (`pkg/go/gtk4rt/flex_test.go`, `cmd/sngl/testdata/style_gtk4_justified_runs.txt`).

  A scroll lays its content out at its natural length along each axis its `direction` moves (`gtk4rt.ScrollSetDirection` sets the scrolled window's policies and the viewport's scroll policies), as CSS does, and at the viewport's size along one it does not; @reachEnd is read along x in a scroll moving sideways alone (`pkg/go/gtk4rt/reach_across_test.go`). GTK's default is the content's minimum, and a GtkPicture's minimum is nothing, so once the text in a scroll alone was taller than it every picture there was drawn at no height, a grid of cards none of whose pictures showed (`pkg/go/gtk4rt/scroll_test.go`). gtk4rt keys a picture by its widget's address, and a fetch arriving asks whether its own source still holds that address rather than whether any does: a page drawn again while its pictures were on their way frees the old ones, GTK gives the new ones the same memory, and a gallery's pictures landed under one another's labels (`pkg/go/gtk4rt/image_test.go`).

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
assignable to what is wanted, which is then called (`var x int = five`; `var y = five` is the function). A color or date literal is validated wherever it
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
- **`internal/lsp/`** + **`internal/lspcore/`** — Language Server Protocol implementation (hover, completion, diagnostics, definition, references, semantic tokens). A file is checked beside the other `.sngl` files of its directory, as a build reads them, an open buffer standing in for its file; a sibling's diagnostics are its own, and every position-matching feature keeps to the file's (`lspcore.FileOf`), since the package holds all of theirs. **Definition, references and semantic tokens read one index** (`lspcore.BuildIndex`): every name the package writes and the declaration it names, keyed by the declaration's pointer, which one check shares with the library. It indexes the checked package only -- a lowered or cloned one holds copies that answer to nothing written. Most declarations record their keyword or a mark rather than their name, a select its dot, a loop variable or `#id` nothing, so the indexer finds the name in the source after them and keeps a position only where the text there is the name, which also drops what the checker synthesized (an elided `this`). A library declaration's definition is its file from `lib.FS`, written read-only under the user cache dir, which the LSP then declines to check (`navigation_fixture_test.go`, `testdata/lsp_definition/`). Semantic tokens carry what only a declaration knows -- a call that is a function, not a component -- and leave keywords, strings and comments to the editor's grammar, whose finer groups a token would cover.

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
