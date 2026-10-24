# gtk4's widget protocol, declared

One MR, `gtk4-declared-protocol`, for #293. The branch starts from
`origin/main`; merge `origin/main` into it when it moves (never rebase).
Delete this file before the MR leaves draft.

gtk4's inline-cgo translator builds its C calls after lowering: a widget's
constructor, the child adder, `sngl_connect` and the `snglCallbacks` index,
the setters and `g_object_set_property`, and every tag with no GIR class
behind it. No pass sees those calls, so `CMarshal`, `ReleaseTemps` and the
interpreter's tracker cover none of them, and `emitShared.ownCStrings`
writes by hand the release `ReleaseTemps` would have. The leak is closed;
what is wrong is where the calls are made.

## The design

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

## Rules for every part

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

## The sites

`ownCStrings` has seven call sites in three files (the issue counted twelve
on 2026-10-23; seven is the count on this branch): the constructor
(`intrinsic_translator.go`'s `OnCreateNode`), a setter (`emitSetterCall`),
`g_object_set_property` (`emitGObjectPropSet`), `sngl_connect`
(`OnAttachHandler`), the entry-text write and the window title
(`compiler_ir.go`), and a flow's markup (`markup.go`). The parts take them in
that order, after the child adder, which makes no string but is the protocol
all the same.

## Part 1: a widget's constructor

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

## Part 2: the child adder

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

## Part 3: `sngl_connect` and the callbacks index

The first string a pass frees: the signal name.

- [ ] `Protocol` gains `connect`, declared once in `gtk4.sngl`, not per
  class: `sngl_connect` is a preamble function of the platform's.
  `snglCallbacks` becomes a package var of `gtk4.sngl` (`list[func()]`) the
  scaffold's `snglGoDispatch` reads by its emitted name, or stays Go with
  only the call declared; decide by whether the generated `callbacks.go`
  can read a SNGL var's emitted name without the translator spelling it,
  and write the reason in `design/architecture.md`.
- [ ] A `@notify<Prop>` event's `notify::<prop>` and every GIR signal reach
  the connect as a `string` argument, so `CMarshal` makes the `c.str` and
  `ReleaseTemps` gives it back (a literal at the application's lifetime).
  The activate signal in `callbacks.go` is the scaffold's own and stays a
  package var.
- [ ] `OnAttachHandler` keeps `recordInvoker` (a test's invoker needs the id
  and event, which only the op has) and the signal lambda's `self`.

Fixture: `testdata/error_gen_protocol_connect.sngl`. Golden:
`testdata/gtk4_declared_connect.txtar`, a button's `@clicked`, a switch's
`@notifyActive` and a handler in a `for` row, asserting the connect, the
index and the signal string's release. Revert: drop the resolution and the
golden shows the translator's `__cstr` back.

## Part 4: the setters and `g_object_set_property`

The largest site: every prop write.

- [ ] `Protocol` gains `set`: `(w W, v T)`, the prop named by the second
  parameter's name. `declgen` writes one per settable prop: GIR's setter, or
  `gtkSetterTable`'s where it has an entry (the table stays Go data
  `declgen` reads, since it is per C type and the `gir:` package is where
  every `set` lives), or a `sngl_set_prop_*` helper by the property's name
  where GTK ships no setter. An enumeration's string member is a `match`
  over the member names GIR lists, to the member's value through the enum's
  cast, which folds for a literal; an `int` passes through.
- [ ] A nullable string prop is `option[string]`; `CMarshal` learns that
  conversion (`null` to `NULL`, a value to `cstring`) here, since Part 1 only
  ever passes `null`.
- [ ] `setEntryText` (`compiler_ir.go`'s entry-text write) becomes a SNGL
  body in `gtk4.sngl` calling `sngl_set_entry_text_quiet` as a
  `#[cnative]`, and stops being a `widgetWrites` intrinsic.
- [ ] `OnPropAssign` keeps `style` and `flex`, `CopyProp`, instance roots,
  and the refusals (construct-only, unsettable, no such prop), which move
  to where the op finds no `set`; `coerceSetterValue` goes.

Fixture: `testdata/error_gen_protocol_set.sngl`. Golden:
`testdata/gtk4_declared_set.txtar`, a reactive label text, a check
button's `active`, a box's `orientation` from a literal, an entry's
`placeholderText` with no GIR setter in the bundled set, and a
`setEntryText` in a handler, asserting each setter and each release.
Revert: drop the resolution and the golden shows `ownCStrings`'s `__ctmp`.

## Part 5: the tags no GIR class is behind

Each is a protocol written by hand in `gtk4.sngl`, smallest first, one
commit each.

- [ ] Scroll (`scrollTag`, a `GtkScrolledWindow` with its own child rule).
- [ ] Image, URL image and drawing image (`image.go`).
- [ ] Drop-down (`dropdown.go`, a `GtkDropDown` over a string list).
- [ ] Toplevel (`toplevel.go`): the window's create, its title -- the
  `compiler_ir.go` call site -- its icon and `visible`.
- [ ] Flow and span markup (`markup.go`): the label's create and its
  `gtk_label_set_markup`, the last `ownCStrings` site; a span stays a node
  with no widget, read off the tree.
- [ ] Canvas (`canvas.go`): the `GtkDrawingArea`'s create and its draw func.

Fixture per tag: the placement error it introduces, if any, else none.
Golden per tag: `testdata/gtk4_declared_<tag>.txtar`, each asserting the
create and every C string's release. Revert: drop the tag's declaration and
its golden fails on the translator's path.

## Part 6: wrapped `gtk4rt` mode, decided

Every gtk4 build tries wrapped mode first -- `gtk4rt` calls over a `Handle`
-- and re-emits as inline cgo when a `C.` is left (`emitIRMode`), which is
why most gtk4 goldens record wrapped output and a fixture needing cgo draws
a canvas. A lowered protocol leaves a `C.` wherever a pass marshaled a
string, so from Part 3 on a program handing a string to a widget falls back
to cgo whatever it renders: read those goldens' diffs as that fallback, and
take this part before Part 3 if the churn hides behavior.

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

## Part 7: retire `ownCStrings`, widen the tracker

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

Fixture: `cmd/sngl/testdata/simulate_gtk4_widget_strings.txt`, a `sngl test --simulate gtk4` over a reactive label, a link button and a switch's notify,
passing. Revert: stop `ReleaseTemps` releasing a spliced temp and the
tracker reports the leak at the setter; stop the interpreter calling the
protocol and the run passes while the golden's release lines go, which is
why both are pinned.

## Part 8: docs

- [ ] `design/ownership.md`: the translator paragraph says the protocol is
  declared and what retired `ownCStrings`; the tracker's and the ASan
  section's "what is left to #293" say it is closed; *A C language target*'s
  last paragraph loses the gtk4 exception.
- [ ] `design/architecture.md`'s gtk4 section, `design/nodes.md` and
  `design/stdlib.md` reread end to end for the translator answering what is
  now declared.
- [ ] `sngl:x/gen`'s package doc and the protocol mark's doc say what a
  platform author writes.
- [ ] Delete this file.
