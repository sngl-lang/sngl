# TODO

## gtk4's widget protocol, declared

One MR, `gtk4-declared-protocol`, for #293. The branch starts from
`origin/main`; merge `origin/main` into it when it moves (never rebase).
Delete this section before the MR leaves draft.

gtk4's inline-cgo translator builds its C calls after lowering: a widget's
constructor, the child adder, `sngl_connect` and the `snglCallbacks` index,
the setters and `g_object_set_property`, and every tag with no GIR class
behind it. No pass sees those calls, so `CMarshal`, `ReleaseTemps` and the
interpreter's tracker cover none of them, and `emitShared.ownCStrings`
writes by hand the release `ReleaseTemps` would have. The leak is closed;
what is wrong is where the calls are made.

### The design

**The widget protocol is SNGL: functions calling `#[cnative]` GTK functions,
marked `#[gen.protocol(op)]`, which `passDeclarative` splices into the node
op it makes, so every C call is IR before `ReleaseTemps` and the translator
keeps only where a node lives.** `declgen` writes the functions for each GIR
class beside its component; `lib/platform/gtk4/gtk4.sngl` writes them for
the tags no GIR class is behind.

- **Found by mark, never by name.** `#[gen.protocol(op)]` is a mark in
  `sngl:x/gen` on a function, with `enum Protocol` growing one member per
  part: `create` (Part 1), `append` (2), `connect` (3), `set` (4). Each op
  has a placement rule the checker holds it to, and a fixture refusing the
  wrong shape. The functions are unexported (`_create_GtkLabel`): no program
  calls one, and the pass reaches them through the mark.
- **Spliced where the op is born.** `passDeclarative` makes
  `lower.CreateNode`, `AppendChild`, `AttachHandler` and the prop `Assign`,
  and `passReactivity`'s eager flattening of a render slot calls the same
  code, so that is the one place to resolve an op: the expression the
  protocol function returns, its parameters bound to the op's operands,
  becomes the op's last argument (an assignment's value), as `CMarshal`'s
  `convertArg` splices a bodied conversion. A body of more than its one
  `return` is refused today; the first op that needs one decides whether it
  is called instead, and `LibFuncs` promotes it as it promotes `c.free`.
- **Marshaled where it is spliced.** `CMarshal` runs before the ops exist,
  since `ConditionalTemps` needs every allocation in view. The splice is
  marshaled by the same code, scoped to it (`marshalSpliced`), and an
  argument whose own type is converted already is left, since the program's
  copy of a protocol function may have been rewritten in place.
- **Released where the view was flattened.** `ReleaseTemps` runs after
  `passDeclarative`, and on a target without declarative views it reaches
  the flattened view bodies as it reaches any block.
- **The translator keeps what is codegen's.** Each `On*` takes the resolved
  call and no longer builds one: it casts the result to the class's C type,
  records the id's C type, and puts the handle where the scope needs it.
  `passNodeEscape` and `fieldSink` stay: whether a node is a Model field, an
  instance record's field or a local of one recursion frame is the shape of
  the generated Go file -- which scopes read the handle later (a handler, an
  updater, a test's `#id`), and whether a render method runs once or per row
  -- and the IR has no Model to put a field on. Neither allocates or calls
  C, so neither is anything `CMarshal` or `ReleaseTemps` would see. A C
  target asks the same question of a struct field and a stack local.
- **The interpreter calls the same functions.** Under `--simulate gtk4` a
  node with a declared protocol is made, filled and connected through it, so
  the stubbed natives are handed the `c.str` the target allocates and the
  tracker follows it (Part 7).

### Rules for every part

- Fixture first (`sngl fmt` writes it), and confirm it fails with the change
  reverted: each part says how. Goldens assert generated code; `deny`
  asserts absence, never a grep.
- `go tool verify` passes at the end of each part, in a clean worktree
  (`git worktree add --detach`; the main checkout's `tmp/sharecost` breaks
  it; `export ANDROID_HOME=$HOME/Android/Sdk`).
- After every codegen change, `go test . -run TestGolden -update` rerecords
  the goldens and `go test ./codegen/platform/gtk4/ -update` gtk4's recorded
  runs (`verify` itself takes no `-update`). Read each golden diff for
  behavior, not only hashes: a constructor, a setter or a connection that
  changed shape must still make the same call with the same value, and a
  string handed to C must still be freed or kept exactly once.
- Change the spec with the meaning, and the `design/` note with the
  mechanism, in the part that makes the change. No part changes what a
  program means; each changes `design/ownership.md`'s *A call the gtk4
  translator builds releases its own strings* by the site it retires, and
  `design/architecture.md`'s gtk4 section by what the translator still does.
- Subagents get the comment rule from `~/.claude/CLAUDE.md` pasted into their
  prompt.
- At the end of each part: commit, push, update the MR description, tick the
  part here.
- When a task is done -- a part, or any item asked for on its own -- end the
  final message with the prompt for the next session: the next open part,
  what to read first, and how to confirm it, ready to paste.

### The sites

`ownCStrings` has seven call sites in three files (the issue counted twelve
on 2026-10-23; seven is the count on this branch): the constructor
(`intrinsic_translator.go`'s `OnCreateNode`), a setter (`emitSetterCall`),
`g_object_set_property` (`emitGObjectPropSet`), `sngl_connect`
(`OnAttachHandler`), the entry-text write and the window title
(`compiler_ir.go`), and a flow's markup (`markup.go`). The parts take them in
that order, after the child adder, which makes no string but is the protocol
all the same. Parts 1, 3 and 4 retired the constructor, `sngl_connect`, the
setter, `g_object_set_property` and the entry-text write, and Part 5's
toplevel the window title; a flow's markup is left.

**Wrapped mode is kept by what a declared op calls.** Part 3 found that a
string marshaled in IR leaves a `C.` that sends the program down the cgo
path. Where gtk4rt already wraps the call with a Go string, the op calls it
as a `#[go.native]` in both modes, as the connect does, and the string's C
copy is gtk4rt's; where it does not, the string is a pass's and the program
is cgo, as a link button's create already is. Part 6 decides what is left.

### Part 1: a widget's constructor

The smallest site and the whole mechanism: a mark, a splice, a marshal, and
one op the translator stops building.

- [X] `sngl:x/gen` declares `enum Protocol { create }` and
  `func protocol(op Protocol) ir.Macro`; the checker stamps the op on the
  function (`ir.Func.Protocol`) and, once every mark has run, hangs it on
  the component (`Component.Protocol`, `bindProtocols`). `create` takes no
  parameters and declares the `#[intrinsic]` component it returns -- an
  `=>` body's type is not known when marks run -- of its own package, one
  per component.
- [X] `declgen` writes, for each class whose primary constructor it can
  spell, the constructor as a `#[macro.cnative]` function (`_gtk_label_new`)
  returning the class's component, and `#[gen.protocol(create)] func _create_<CType>() <CType> { return … }` with a zero per parameter: `""` for
  a string GTK refuses NULL for, `null` for a nullable string
  (`option[string]`) or any other pointer (`option[c.pointer]`), `0` for a
  number cgo's `int` or `double` carries, and a cast through a struct naming
  the C type otherwise (`_to_GtkOrientation`, `_c_GtkOrientation`; prefixed,
  since GTK has a `float` parameter). `ctorZeroArg` and `ctorScalarCast`
  moved there; `unconstructible` words the build error.
- [X] `passDeclarative` splices the create's returned expression into
  `CreateNode` as its second operand, folds the predeclared `null` in it and
  marshals it (`marshalSpliced`). `CMarshal` leaves an argument whose own
  type is converted already: the program's copy of the `gir:` package had
  its create bodies rewritten in place, and the splice was converted twice.
- [X] `ReleaseTemps` reaches a flattened view body on a target without
  declarative views: the link button's `""` is now one application-lifetime
  `c.str` freed at teardown, where the translator's `__cstr` was never freed.
- [X] The translator takes the operand through `codegen.NodeMaker`
  (`OnMakeNode`) and builds no GIR constructor; wrapped mode keeps
  `rtCtorForCType` until Part 6. Its `ownCStrings` call went.
- [X] `design/nodes.md`, `design/architecture.md`'s gtk4 section and
  `design/ownership.md` (the translator paragraph, `CMarshal`, the reach of
  `ReleaseTemps`).

Fixture: `testdata/error_gen_protocol_create.sngl` (a parameter, an inferred
or non-component return, a struct) and `TestGenProtocolPlacement`
(not intrinsic, another package's, twice). Golden:
`testdata/gtk4_declared_create.txtar`. `declgen_test.go`'s
`TestWidgetSource_DeclaresConstructors` and
`TestCtorParam_CastShadowsNoScalar`. Reverted, each fails: no splice and the
golden's build stops at "GtkLabel cannot be constructed"; no view-block
release and the golden differs; no parameter check and the fixture's
`ERROR(check)` goes unmatched; no idempotent conversion and
`TestWidgetSurface_EitherCompilesOrRefuses` does not build.

### Part 2: the child adder

No string, but a C call all the same, and the shape every container op
after it takes.

- [X] `Protocol` gains `append` and `remove`: `(parent P, child T)` with `P`
  an `#[intrinsic]` component of the function's package, one each per
  component (`bindProtocols`). `declgen` writes, per class with a
  `gir.ChildAdder`, the call as a `#[macro.cnative]` taking C pointers and
  `#[gen.protocol(append)] func _append_<CType>(parent <CType>, child ui.node)` handing it the nodes through declared casts (`_as_GtkButton(_ptr_GtkLinkButton(parent))`, `adderReceiver`'s rule); `_ptr_<CType>` takes
  the class, since a family value reaches Go boxed in an `any`. `remove` is
  GIR's `remove` method taking one widget (`gir.ClassInfo.ChildRemove`,
  inherited as the adder is; the bundled subset gained `gtk_box_remove`),
  written where the class accumulates children.
- [X] `childOp` makes every `AppendChild` and `RemoveChild` (the flattening, a
  render slot's teardown, a placed slot's close, a recreated instance) and
  splices the parent's function as a third operand (`protocolSplice`, shared
  with the create); the translator takes it through `codegen.ChildProtocol`.
  A create with no append is refused in lowering
  (`TestClassWithNoAdder_RefusesAChild`). The translator keeps `appAttach`,
  the window icon, wrapped mode's `rtChildAppendWrappers`, the render slot's
  parent -- a GtkBox whatever its container is declared as, so its insert is
  `sngl_insert_before` and a remove with no call is `gtk_box_remove` -- and
  the GIR adder for the tags no GIR class is behind, until Part 5. An
  unresolved op on a declared-protocol parent panics.

Fixture: `testdata/error_gen_protocol_append.sngl`. Golden:
`testdata/gtk4_declared_append.txtar`, a box, a frame and a scrolled window
each holding a child, and an `if` removing one, asserting each adder and
`gtk_box_remove`. Revert: drop the resolution and the golden's build fails
on the missing adder. Done: also a link button, whose adder is GtkButton's;
every other golden and recorded run is unchanged byte for byte.

### Part 3: `sngl_connect` and the callbacks index

Done, but not as planned: the signal name makes no C string in the program.
Marshaled in IR, it left a `C.` in every program with a handler, which would
have sent 145 wrapped goldens down the cgo path and wrapped mode's cached
build with them, so the connect calls a cached runtime instead.

- [X] `Protocol` gains `connect`, declared once in `gtk4.sngl` beside the
  build-target node (`bindConnect`, `NodeProtocol.Connect` on the node) and
  read off it by `FeaturesFrom` (`Features.connect`, unexported). It is
  `gtk4rt.Connect` as a `#[go.native]`: gtk4rt registers the handler through
  `cbind` and copies the signal into C itself, in both modes. `snglCallbacks`
  stays Go, `callbacks.go` being fixed text that cannot name a SNGL var's
  emitted spelling; it holds only the canvas click until Part 5.
- [X] `#[gen.connects(name)]` (`EventDecl.Connects`) names an event as its
  host knows it; declgen writes `notify::<prop>` on each `@notify<Prop>` and
  the GIR name on a signal SNGL spells otherwise. `passDeclarative` splices
  the connect into `AttachHandler` as a fourth operand, every parameter bound
  but the handler, which the op already carries.
- [X] `OnConnectThrough` (`codegen.HandlerConnector`) puts the node as a
  `Handle` and the walked handler in the unbound parameter (`connectCall`),
  and keeps `recordInvoker` and the signal lambda's `self`. A node with no
  declared protocol is connected through `gtk4rt.Connect` too; one with a
  protocol and no splice panics. `OnAttachHandler`'s `ownCStrings` call went.

Fixture: `testdata/error_gen_protocol_connect.sngl`, and
`TestGenProtocolPlacement` (beside the target, twice, `connects` on an event
and on a prop). Golden: `testdata/gtk4_declared_connect.txtar`. Reverted,
each fails: no splice and the build panics at the translator; no
`#[gen.connects]` and the golden connects `"notifyActive"`.

### Part 4: the setters and `g_object_set_property`

The largest site: every prop write. Done, with three departures from the
plan: there is no `gtkSetterTable` (the setter is GIR's, `girSetter`), the
bundled GIR does carry `gtk_entry_set_placeholder_text`, so the
`sngl_set_prop_*` path is pinned over `rules.gir` in
`TestGObjectPropSet_EmitsGenericPath` rather than in the golden, and
`setEntryText` is gtk4rt's, not a `#[cnative]`.

- [X] `Protocol` gains `set`: `(w W, v T)`, the prop named by the second
  parameter's name (`bindSet`, `NodeProtocol.Set`). `declgen` writes one
  per settable prop (`writeSets`): gtk4rt's wrapper as a `#[go.native]`
  where `rtSetterTable` has the setter, GIR's setter as a `#[cnative]`
  otherwise, or `sngl_set_prop_*` by the property's name where GTK ships
  none. An enumeration's member is a `match` over GIR's member names, to
  the value (`gir.EnumInfo.Values`) through the enum's cast; `foldSplice`
  folds it for a literal and refuses any other value. A bool is
  `v ? 1 : 0`, folded for a literal and a call in place otherwise.
  `spliceSets` makes every prop write's value the set's call -- updaters'
  and handlers' too, which `passReactivity` made first -- once per shared
  `Assign`. No wrapped golden flipped to cgo.
- [X] A nullable string parameter is `option[string]`; `CMarshal` hands a
  string as `cstring` and `null` as NULL, and refuses an option value
  (`testdata/c_option_string.txtar`).
- [X] `setEntryText` is `gtk4rt.EditableSetTextQuiet` as a `#[go.native]`,
  not a `widgetWrites` intrinsic: a `#[cnative]` would have sent every
  wrapped program with a `ui.input` to cgo. `readWidgets` hands any gtk4rt
  native taking a widget the widget as a `Handle`, and no longer calls
  `ownCStrings`.
- [X] `OnPropAssign` keeps `look`, `CopyProp`, instance roots, the non-GIR
  tags and the refusals; `emitSetterCall`, `emitGObjectPropSet`,
  `coerceSetterValue`, `setterValueArg`, `enumValueArg`, `rtSetterValue`
  and `rtEventSetterExpr` went. A handler closure in a `for` row now
  writes the label the translator used to drop
  (`testdata/indexed_trigger_nested.txtar`).

Fixture: `testdata/error_gen_protocol_set.sngl`, and
`TestGenProtocolPlacement` (a set of no prop, twice, another package's).
Golden: `testdata/gtk4_declared_set.txtar`. Reverted, each fails: no splice
and the translator panics on a write that is no call; no `option[string]`
conversion and `c_option_string` differs; no `bindSet` and the placement
cases go unrefused.

### Part 5: the tags no GIR class is behind

Each is a protocol written by hand in `gtk4.sngl`, smallest first, one
commit each.

- [X] Scroll (`scrollTag`, a `GtkScrolledWindow` with its own child rule):
  `gtk4rt.ScrollNew`, `ScrolledWindowSetChild` and `ScrollOnReachEnd` as
  `#[go.native]`s; `@reachEnd` is no GTK signal, so `Protocol` gains `on`,
  a component's own connect for one event (`bindOn`,
  `testdata/error_gen_protocol_on.sngl`). No golden flipped to cgo.
- [X] Image, URL image and drawing image (`image.go`): each create, the
  image's append, remove, `fit` and three events, and a URL source's
  `picture`, as `#[go.native]`s over gtk4rt; a drawing's paint stays the
  translator's until the canvas is declared. No golden flipped to cgo.
- [X] Drop-down (`dropdown.go`, a `GtkDropDown` over a string list): the
  create was already declgen's; `offerChoices` is `gtk4rt.DropDownOffer` as
  a `#[go.native]`, which copies its strings, and `dropdown.go` and the
  `snglDropDownOffer` helper, whose cgo copy made two C strings, went.
- [X] Toplevel (`toplevel.go`): the window's create, its title -- the
  `compiler_ir.go` call site -- its icon and `visible`. gtk4rt's toplevel in
  both modes; the cgo scaffold's window takes the title through
  `gtk4rt.ToplevelTitleWindow`. `on` binds a two-way prop's report too
  (`:visible`). The icon stays the translator's (`windowIcon`): appending a
  source to a window is where it lives, not a call to make.
- [ ] Flow and span markup (`markup.go`): the label's create and its
  `gtk_label_set_markup`, the last `ownCStrings` site; a span stays a node
  with no widget, read off the tree.
- [ ] Canvas (`canvas.go`): the `GtkDrawingArea`'s create and its draw func.

Fixture per tag: the placement error it introduces, if any, else none.
Golden per tag: `testdata/gtk4_declared_<tag>.txtar`, each asserting the
create and every C string's release. Revert: drop the tag's declaration and
its golden fails on the translator's path.

### Part 6: wrapped `gtk4rt` mode, decided

Every gtk4 build tries wrapped mode first -- `gtk4rt` calls over a `Handle`
-- and re-emits as inline cgo when a `C.` is left (`emitIRMode`), which is
why most gtk4 goldens record wrapped output and a fixture needing cgo draws
a canvas. A lowered protocol leaves a `C.` wherever a pass marshaled a
string, so a program handing one to a widget falls back to cgo whatever it
renders; Part 3 kept the connect from doing so by calling gtk4rt, and the
parts after it do the same where gtk4rt wraps the call.

- [ ] Measure a fresh `go build` of `examples/pokedex` and a two-widget
  program in each mode with a warm and a cold cache. If wrapped mode still
  saves what it was written to save, it becomes a second declared protocol
  over `#[go.native]` `gtk4rt` functions, selected by an option at check
  time as `gir` is; otherwise it is retired with `wrapped.go` and the
  `rt*` tables. Write the numbers and the decision in
  `design/architecture.md`.

Golden: a kept mode gets `testdata/gtk4_wrapped_protocol.txtar`, a label,
a button and a reactive string setter under the option, asserting
`gtk4rt` calls and a `deny` on `C\.`; a retired mode's goldens rerecord as
cgo. Revert: for a kept mode, drop the option and the golden shows cgo.

### Part 7: retire `ownCStrings`, widen the tracker

- [ ] `ownCStrings`, `constCString`, `cTempName`, `scaffoldData.CStrings`
  and their state on `emitShared` go; nothing is left calling them.
- [ ] `testdata/gtk4_translator_strings.txtar` asserts the lowered calls
  instead of denying `C.CString`: each string handed to C is a
  `ReleaseTemps` temp or an application-lifetime literal, and no
  `__cstr`/`__ctmp` name is left.
- [ ] The interpreter under `--simulate gtk4` makes, fills and connects a
  node with a declared protocol through it (`interp.PrepareFor` already
  runs `CMarshal`; it now also runs the protocol functions where it mounts
  an intrinsic, writes its prop and attaches its handler), so the tracker
  sees each `c.str`.

Fixture: `cmd/sngl/testdata/simulate_gtk4_widget_strings.txt`, a `sngl test --simulate gtk4` over a reactive label, a link button and a switch's notify
(whose connect makes no `c.str`), passing. Revert: stop `ReleaseTemps` releasing a spliced temp and the
tracker reports the leak at the setter; stop the interpreter calling the
protocol and the run passes while the golden's release lines go, which is
why both are pinned.

### Part 8: docs

- [ ] `design/ownership.md`: the translator paragraph says the protocol is
  declared and what retired `ownCStrings`; the tracker's and the ASan
  section's "what is left to #293" say it is closed; *A C language target*'s
  last paragraph loses the gtk4 exception.
- [ ] `design/architecture.md`'s gtk4 section, `design/nodes.md` and
  `design/stdlib.md` reread end to end for the translator answering what is
  now declared.
- [ ] `sngl:x/gen`'s package doc and the protocol mark's doc say what a
  platform author writes.
- [ ] Delete this section of `TODO.md`.

## Style fields, translated in SNGL

P-1's third part, on !257's branch (`refactor/android-select-state`), which
already lets a target package's override hold state, writes android's select
as an override, takes every gtk4 setter from GIR, and has `match` merged in.

### Where it stands

Each platform's Go switches on `ui.Style` field names, and drops every field
it does not name with no diagnostic. On android, bubbletea and fyne the work
is done twice: a Style written as a literal is mapped field by field in Go,
and a Style held in a value goes through a runtime helper the Go writes out
(Kotlin `Modifier.snglStyle`, Go `__styleLipgloss`, `__styleTheme`). The two
copies disagree:

- android's helper drops flex, fontFamily, borderRadius and button colours,
  and maps textAlign center/right only.
- bubbletea's helper renders `opacity=1` faint.
- All three helpers read `.px` and ignore em and rem.

What each platform answers today:

| Platform  | Fields it maps (of 52)                               |
|-----------|------------------------------------------------------|
| html      | all, in Go (htmlutil); stays there, see step 5       |
| android   | done in SNGL (style.sngl); `unstyled` lists the rest |
| bubbletea | done in SNGL (style.sngl); `unstyled` lists the rest |
| fyne      | done in SNGL (style.sngl); `unstyled` lists the rest |
| gtk4      | done in SNGL (style.sngl); `unstyled` lists the rest |

The places to read first: `codegen/iterate.go` (`NodeStyleFieldsOf`, which
decides literal vs held), `codegen/measurement.go`, android's finished
`lib/platform/android/style.sngl` and bubbletea's as the pattern, fyne's
`spec.go`, `layout.go`, `theme.go` and `pkg/go/fynetheme`, gtk4's
`lib/platform/gtk4/style.sngl` and `pkg/go/gtk4rt/look.go`,
and `internal/htmlutil/style.go`.

### The design

No new language feature. Each platform package translates a Style with a
`const func` written in SNGL over the host declarations it already has --
`#[kt.native]` Modifier and TextStyle calls on android, `#[go.native]`
lipgloss methods on bubbletea, fynetheme on fyne -- and every override calls
it on its `style`. `match` reads an enum field (fontWeight, textAlign) into
the host's constant. One function serves a literal and a held Style alike, so
the two copies above become one. html is the exception (step 5).

Fields a parent consumes -- `flex`, fyne's `margin`, Compose's
`Modifier.weight`, which exists only in a Row or Column scope -- are answered
by a context: vbox and hbox provide their axis, and the child's function reads
it. On Compose the call is then emitted inside `Row { }`, where `weight`
compiles. On fyne the layout reads each child's weight and margin off the
child's widget rather than taking them as arguments, a fynelayout change.

gtk4 gets a CSS setter in gtk4rt, exposed as a prop of the GIR widget
declarations, so reactivity re-sets it when the style changes; an effect is
for what the host adds and removes with the node, such as a CssProvider.

**Silence is an error, as a repo test.** Each platform declares the fields it
cannot express, with a reason, in a `const` list. A test in `lib/` holds the
fields its style functions read, together with that list, to every field of
`ui.Style`, so adding a field fails each platform until it answers. A
program's write of a field its target lists does not warn (Jonathan's call):
only a literal shows the write, a multi-target build would warn per target
with no way to say it is meant for one, and a field not answered yet is the
compiler's gap rather than the program's. A lint the build opts into may come
later.

When a platform's translation is SNGL, the Go it replaces goes: the field
switches, the held-Style helpers, and the per-node routing that the overrides
now say themselves (android's `modifier=` / `colors=` / `shape=` / Text
`style=`; bubbletea's flow split).

### Order

Each is a commit series on the branch; one MR, so CI runs once.

1. **android.** Fixture first: the audit's repro, `margin` and `borderRadius`
   written on android, both dropped today. Then `modifier`, `textStyle`,
   `buttonColors` and `shape` functions in `lib/platform/android/style.sngl`,
   the axis context on vbox/hbox, the overrides calling them, and the Go
   above deleted. Done. The axis is a closure rather than an enum: vbox
   and hbox provide `flexShare`, a `func(float32) Modifier` written in the
   Column's or Row's content, so a component built at run time -- a
   composable of its own, outside that scope -- still calls `weight` through
   it. Nothing in style.sngl reads a composable: under `--lang go` a ternary
   splits a function into statements no splice takes, so the theme's text
   style and a button's default colors are read in the overrides.
2. **bubbletea**, the lipgloss chain; a `Widget`'s style answered or listed.
   Done: `lib/platform/bubbletea/style.sngl` over `#[go.native]` lipgloss
   declarations, and a Widget renders inside its look. `flex` is read by the
   Layout off its child: the override hands each primitive `grow`, and a
   Layout with a growing child renders that child in a closure once the fixed
   ones have said what they leave of its extent -- its look's width, else
   its parent's, else the terminal's (`tui.Join`). A context was no use here:
   a share depends on the siblings' rendered sizes, which only the Layout
   sees.
3. **fyne**, with the fynelayout change for flex and margin. Done:
   `lib/platform/fyne/style.sngl`'s `lookOf` builds a `Look`, a
   `#[go.native]` record that is `fynelayout.Look`, in plain SNGL, so a
   literal Style folds to a Go literal and the interpreted host is handed
   the same record. A node whose look asks for anything is shown in a
   `fynelayout.Box`, which draws the margin, border, fill and padding and
   puts the widget under a theme of its own only where the look sets one.
   `flex` is read by the parent off the child: every vbox and hbox is
   fynelayout's `Weighted` layout, which takes each child's share from its
   Box, and a margin is the child's own Box's.
4. **gtk4**, with the gtk4rt CSS setter. Done: `lib/platform/gtk4/style.sngl`'s
   `lookOf` builds a `Look`, a `#[go.native]` record that is `gtk4rt.Look`,
   declared in the `gir:` package beside the widgets and carried by each as
   a `look` prop; the CSS text is plain SNGL, so a literal Style folds to a
   constant. `gtk4rt.SetLook` answers it in both modes: a CssProvider per
   styled widget, owned by the widget, so no effect; the size request, a
   label's alignment and lines, overflow, visibility and cursor; and what an
   earlier Look took, given back when a later one leaves it unset. `flex` is
   expand along the axis of the box the widget sits in, read off the parent
   when the widget lands there, and GTK splits the free space evenly: a
   weighted share is a layout of gtk4rt's own. A box's `alignItems` is put
   on each child it holds, later ones included.
5. **html** stays in Go until a Style can say "unset". Tried and dropped: a
   `css` function in SNGL answered every field, but a function of a Style
   cannot tell a written `0px` from an omitted one, and on html, where the
   browser has defaults of its own, that is a loss rather than a no-op --
   `padding=0px` on a card, `alignItems=start`, `display=flex` on a text,
   `listStyleType=none`, `fontWeight=normal` on a header cell and black
   would each write nothing. A style reading state would also ship with none
   of its static CSS in the markup, since the whole call runs where the page
   does, and a held literal carries all 52 fields. Folding the function per
   node per page cost the docs site 35% more allocation (12.6 GB against
   9.3), most of it the interpreter rebinding an environment per call. The
   way back is step 7, then a fold cache keyed on a const call's arguments.
6. The coverage test across all six, and CLAUDE.md's platform sections.
7. **A Style that can say "unset".** Every style function reads a field at
   its type's zero as unset, so a written zero and an enum's first member are
   lost: `alignItems=start` stretches on fyne and gtk4, `textAlign=left` leaves
   a fyne button centered, an explicit `#000000` is unset everywhere, a side
   written `0px` beside a non-zero whole takes the whole, and html cannot move
   at all (step 5). An `unset` member leading each Style enum, as
   `markup.SpanStyle`'s `inherit` does, answers the enums; a measurement and
   a color need option fields or an unset value of their own. With it, a
   JavaScript struct literal can write every field
   (`js_struct_omitted_fields`), since nothing would read a field's presence
   any more.

### Found on the way, not yet fixed

- A literal Style's Modifier is built at run time on android,
  `styleModifier(Style(…))`: the optimizer splices single-expression
  functions only, and the Modifier builder is a block.
- android's `unstyled` still lists fields Compose can express: `gap`,
  `alignItems` and `justifyContent` (Column and Row arguments), `maxLines` and
  `whiteSpace` (Text arguments). Each would put an argument on every node.
- On android a Column the Go writes rather than an override -- a window's
  content arriving through a slot, a nav page of several nodes -- provides no
  `flexShare`, so a `flex` child there fills the screen; a page under an hbox
  inherits the Row's closure. Both took a Column weight before.
- A side written `0px` beside a non-zero whole (`{padding=16px, paddingTop=0px}`) reads as unset, so it is 16 on android and 0 on html: zero
  is unset in every Style.
- A Go struct literal leaves an omitted enum field `""`, not the first
  member: `S{n=1}` then fails `s.t == Tone.calm` on every Go target, where
  the interpreter passes. bubbletea's style.sngl never compares against a
  first member for that reason. Writing the member in every literal would
  put twelve fields in each Go Style; spelling the first member `""` would
  change what `"{e}"` prints.
- The interpreter, and the fold, print a color as `{r = 51, g = 102, b = 153, a = 255}`; every compiled target prints `#336699`.
- A struct spread in a library function is not lowered on a target withholding
  `structSpread`: LibFuncs brings the function into the package after
  NoStructSpread has run.
- On bubbletea a `flex` child is joined at its own size under an `if` or a
  `for`, being part of what that statement renders, and where it holds a
  loop's focus stop or canvas, whose count would come after its later
  siblings'. `justifyContent`, `overflow`, `whiteSpace` and `display` are
  listed as not answered yet.
- On clean main, `ui.select` does not compile on bubbletea under a `for`
  (`too many arguments in call to m.widget0Copy`), nor on fyne or gtk4 under
  a reactive `if` (`undefined: o`; a missing `c.__n1`).
- passImplicitState refuses a call site that binds one two-way prop and
  leaves another unbound.
- android's select honors none of `disabled`, `multiple`, `@focus`,
  `@blur`.
- On fyne a widget added to a container already under a Box's theme keeps
  the theme it had until that Box refreshes, Fyne's ThemeOverride rule; a
  render slot appending into a styled vbox meets it.
- On fyne an explicit `#000000` is unset, as the color zero is, and so are
  `alignItems=start` (stretches) and `textAlign=left` (a button stays
  centered): each is its type's zero.
- On gtk4 `flex` shares the free space evenly whatever the weights, and
  `justifyContent` is listed as not answered yet. A `fontSize` is written in
  points, as Pango reads a span's, so 1em is GTK's default text, while every
  other length is pixels. A Look's CSS is at application priority on the
  widget's own node, so a button given a `background` loses the theme's hover
  and press feedback, and a card's border is drawn beside the one Adwaita draws
  on the frame's border node; a popover's `background` paints its body and
  not its arrow.
- The shake keeps a library body reachable through a node handle's component
  type, so a program holding `#d` on a datepicker keeps `LookOf` and its
  helpers on gtk4 though nothing calls them.
- A JavaScript struct literal writes only the fields it names, so an omitted
  one reads undefined where Go and Kotlin read its zero
  (`testdata/js_struct_omitted_fields.txtar`). Writing every field is a
  small change in the JS emitter, but html's held-Style helper reads a
  field's presence as having been set, so it waits on step 5.
- html writes a Style field the build cannot fold with `setProperty` after the
  static attribute, in the literal's field order: `{padding=p, paddingTop=4px}`
  with `p` state resets the side on every write.
- The optimizer interprets a const function per call, and a lambda call
  snapshots the environment: a `.filter(func…)` in a function folded per node
  was most of a 7 GB increase on the docs site.

## `match`, later

`match` is implemented: the rules are in CLAUDE.md's *Match* section and the
specification's *The match statement*. What is left is the longer road.

### Generated code a person would write

The checker turns a match into an if/else-if chain or nested ternaries, which
every target emits unchanged, so the first implementation needed no backend
work. The cost is the shape of the output: Go gets `if … else { if … }`,
Kotlin nested `if` expressions, JavaScript chained `?:`, where each has a
construct that says it -- Go's `switch`, Kotlin's `when`, JavaScript's
`switch` -- and an exhaustive match over an enum is exactly what a compiler
checks a `when` for.

1. Keep the match in the IR: an `ir.Match` statement and expression the
   checker produces instead of the chain, with the subject, the arms'
   patterns and whether the last arm is taken unasked.
2. Lower it to the chain in a pass every target gets unless its language
   declares `#[gen.can(match)]` -- the capability polarity `sngl:x/gen`
   already uses, so a target that has not heard of it keeps working.
3. Teach the walks that ask an `if` what it holds to ask a match the same:
   `ir.TransparentBlocks`, the family, slot, element-ref and `.empty` rules,
   the interpreter, the optimizer's folds, the reactivity pass's render slots.
   Each is a case beside the `if` it already has, and the reason the first
   implementation did not keep one.
4. Emit it natively in Go, Kotlin and JavaScript, view bodies included where
   the platform renders statements (Compose's `when` in a composable).

### Language

- A binding pattern (`var name`), once there is a use beyond unwrapping an
  option: payload enums, or guards (`var n if n > 10`).
- Option-unwrapping syntax of its own.
