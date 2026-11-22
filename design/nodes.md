# Primitives, natives, element refs and handles

**`#[intrinsic]` on a component is a platform primitive.** On a function the
mark names a signature in `ir.Intrinsics` that every language backend must
implement. On a component there is no signature to register — the declaration
*is* the contract for props and events, and one platform's codegen emits the
widget from it (android's `Screen`, `Link` and `SnglDateField` in
`lib/platform/android/android.sngl`). The id is that codegen's dispatch key, namespaced by
the emitting platform (`android:Screen`) so it can never collide with a stdlib
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

**How a platform builds the widget may be declared beside it.** A
`#[gen.protocol(op)]` function (`sngl:x/gen`) is one operation of the
protocol an `#[intrinsic]` component's widget is built with, written in SNGL
so that every call to the host is one a pass sees. `create` is the first: a
function of no parameters declaring the component as its return type, in the
component's own package, at most one per component (`bindProtocols`,
`testdata/error_gen_protocol_create.sngl`,
`TestGenProtocolPlacement`). The return type is declared rather than
inferred because the mark runs before an `=>` body is checked. The checker
hangs it on the component (`Component.Protocol`), and `passDeclarative`
splices its one returned expression into the `createNode` op as a second
operand, marshaling the splice itself (`marshalSpliced`): `CMarshal` ran
before the op existed. The predeclared `null` in the splice is folded there
too, since the optimizer never saw a library body nothing calls. A
translator taking the operand implements `codegen.NodeMaker`; one that does
not and is handed one panics, rather than dropping the call.

`append` and `remove` are the next two: a function of the parent, typed as
the component, and the child, under the same placement rules
(`testdata/error_gen_protocol_append.sngl`). `childOp` makes every
`AppendChild` and `RemoveChild` -- the flattening, a render slot's teardown,
a placed slot's close -- and splices the parent's function as a third
operand, its parameters bound to the two operands; a translator taking it
implements `codegen.ChildProtocol`. The parent is found by its handle's type,
which names its component. A component with a create and no append takes no
child, and putting one in is a lowering error where the op is made
(`TestClassWithNoAdder_RefusesAChild`); one with no remove keeps a child the
program stops rendering, as it did when the translator answered. A child of an
`#[intrinsic]` component whose package declares no protocol for it at all is
no widget -- gtk4's span, a piece of its flow's markup, read off the tree --
and takes no splice and no refusal (`isNoWidget`).

`connect` is the platform's rather than a component's: a function of the
node, the name the host knows the event by and the handler, declared once
beside the build-target node (`bindPlatformOp`,
`testdata/error_gen_protocol_connect.sngl`), since a platform connects a
handler to every widget it makes the same way. `FeaturesFrom` reads it off
the platform's node, and `passDeclarative` splices it into each
`attachHandler` op on a node whose component declares a protocol, as a
fourth operand. The event is handed over as `#[gen.connects]` names it, or by
its own name: GTK's `notify::active` is no SNGL name, so `@notifyActive`
carries the mark. The handler parameter is the one left unbound: the op
already carries the handler, and a copy in the splice would be a second body
every later pass rewrites, the two sharing whatever the body declares. The
platform puts the walked handler there, shaped as its trampoline calls it
(`codegen.HandlerConnector`).

`on` is a component's own connect for one event the host raises through no
call `connect` makes: a function of the widget, typed as the component, and
the handler, whose parameter is named as the event -- or as a two-way
prop, whose report reaches the node as an event of its name (`bindOn`,
`testdata/error_gen_protocol_on.sngl`, `TestGenProtocolPlacement`), at most
one per event. `passDeclarative` splices it in place of the platform's
connect, with the node bound and the handler left for the platform as
`connect` leaves it. gtk4's scroll is the first: GTK raises no signal for
the end of the content arriving, so `@reachEnd` is gtk4.sngl's
`_rtScrollOnReachEnd`, not a `g_signal_connect`.

`set` writes one prop: a function of the widget, typed as the component, and
the value, whose parameter is named as the prop it sets (`bindSet`,
`testdata/error_gen_protocol_set.sngl`, `TestGenProtocolPlacement`), at most
one per prop. It is spliced into every write of the prop, not only the
view's first: an updater and a handler write a node's prop as well, and
`passReactivity` made both before `passDeclarative` runs, so `spliceSets`
walks the whole package once the view is flattened and makes the write's
value the set's call, with the node and the written value bound. A write that
two blocks share is spliced once. A write is made only of a node the build renders:
an arm of a plain `if` whose condition the build reads (`foldedBool`, which
takes two enum members, or the two ints `passEnum` made of them on C) gets no
updater, since the arm is folded away; gtk4's radio, its `direction`
defaulted, wrote `sensitive` to the horizontal box it never built on each
write of `disabled`, which GTK reported as a critical
(`cmd/sngl/testdata/c_gtk4_radio_disabled_runs.txt`). `ReleaseTemps` visits an assignment's
value, so a string the call hands C is bound before the write and freed after
it, or made once for the application where it is a literal. The passes that
lower a ternary and a match ran before the op existed: a match in a set
folds where it is spliced, which takes the arm its literal subject names and
refuses a value no arm names or one that is no literal (`foldSplice`), since
a set's match maps a word the program writes to the host's own value and its
`else` is there only because a match over a string must have one; a ternary
folds on a literal head and is otherwise a call in place on a target without
ternaries. The platform puts the node where its scope holds it (gtk4's
`OnPropAssign`), and refuses a write of a prop no set answers. gtk4 declares its whole
protocol this way, declgen writing a GIR class's and `gtk4.sngl` the tags no
GIR class is behind, and what its translator still calls GTK for is where a
node lives -- the application's attach, a render slot's box, the window icon
-- and the canvas, which #294 takes (`design/architecture.md`).

`insertBefore` is a component's, beside its `append`: the parent, the child
and the node to go ahead of. `handle`, `attach` and `detach` are the
platform's, as `connect` is: the type every node is held as, and how a node at
the program's root -- a window -- is shown and taken away
(`bindPlatformOp`). They are what a platform asking for **placement**
(`#[gen.wants(placement)]`) declares, and with them the platform writes no
placement code in Go. `passPlacement` (`internal/lower/placement.go`) runs
once the tree is flat and every write explicit, and turns each node op into
the protocol call `passDeclarative` spliced into it: a create is its call, an
append, remove or insert its parent's, a handler's attach the connect or
`on`, and an op on the application the platform's attach or detach. An
insert whose reference is an `option` -- a render slot's cursor -- appends
where it is null. A handler nobody wrote, which lowering attaches in place of
an event the program left alone, is dropped rather than connected. A handler
taking the widget it fired on, `@toggled(b)`, connected through a `connect`
whose handler takes nothing, is wrapped in one calling it with the node it is
connected to (`handedTheNode`), as gtk4's trampoline hands it nothing. A
test's fire of an event whose handler reads a value off the widget -- a check
button's state, an entry's text -- is a `#[gen.fires]` the platform declares
itself, putting the value there first; declgen leaves those out of the
widgets it writes (`valuedFires`). Every node
is then retyped as the `handle`'s return type, through lists and options too,
and the package body's node locals, which handlers reach, become package vars.
A generic component whose call site binds nothing of the family it renders
into -- a timer, which takes no children, built at run time in a loop -- holds
its root as that parameter, which no language writes; every node is the
handle, so the parameter is too
(`cmd/sngl/testdata/timer_tab_loop_wait_runs.txt`).
What is left is plain functions, structs and calls, which a language writes
whole (`clang.WriteProgram`), so a platform on placement needs no translator:
win32's generator writes only the program's entry
(`testdata/win32_c_counter.txtar`, which fails with the pass turned off).

**A canvas is placed through its protocol where its component declares a
`#[gen.protocol(redraw)]`**, a function of the widget asking it to paint again,
which the checker allows only on a component whose rest slot takes shapes
(`TestGenProtocolPlacement`). win32's `Canvas` is one, which its
`draw.canvas` override renders, and gtk4's, and gtk4's
`ImageDrawing`, a `draw.image`, whose paint is a texture's. `passCanvasPaint` (`internal/lower/canvas_paint.go`)
runs right after `passShapeDraw` and hands the drawing -- the statements that
pass put in place of the shapes -- to the canvas's event whose one parameter is
typed as the context those statements paint through, as a handler marked
`Lowered.Paints`. An `effect` or a `timer` written in the drawing paints
nothing and belongs to no tree, so it is placed beside the canvas, after it,
as the component's lifetime it is; it was refused
(`testdata/component_treeless_in_canvas.txtar`). One under an `if` or a
`for` -- a shape's own owned state under a `for` is an effect per copy -- is
reached through it (`splitLifetimes`): the `if` or `for` is split in two over
the same head, the drawing keeping the shapes and a copy beside the canvas
keeping the lifetimes, its loop variables its own. Both agree on each copy
because `passCopyKeys` has already keyed the copy's cells by its loop
variable, which both read; with no split the effect reached placement in the
paint handler, which has no protocol for it
(`testdata/canvas_for_shape_state.txtar`). It runs before anything flattens a canvas, so a render slot's
canvas (`passReactivity`) and a record's (`passInstanceBodies`) are flattened
with the handler, which is lifted and connected through the component's `on`
as any handler is. A canvas written with no id is given `__drawingN`, its own
numbering, since the passes between that number nodes `__nN` count from none of
theirs; a shared `__nN` named the window and the canvas both, and the canvas
replaced the window it was to go in. `passCanvasReactivity` reads what the
paint handler reads, and finds such a canvas flattened into a render slot's
function too, which no other target's redraw can reach from outside the slot
(html named a local of the slot's render). `passCanvasRedraw` then makes each
redraw the protocol's call of the node, a read by name that placement holds as
it holds any (`hoistShared`, which walks `closureFuncs` for a handler's
closure as `hoistNodes` does, and `holdRead`, which still nulls such a node as
its branch goes). `testdata/win32_c_canvas.txtar` holds it, and
`TestSnapshotCanvas` (`codegen/platform/win32`) what GDI+ paints;
`testdata/gtk4_c_canvas.txtar` holds gtk4's, whose snapshots draw a
canvas afresh and so cannot see a redraw left out.

**A component built at run time is placed as a record**
(`internal/lower/placerecords.go`): a row a keyed `for` keeps, a slot body
lifted into a component of its own. The record is a struct holding the
component's vars, the nodes its body makes and `__root`, the node it renders
as, and it is a box (`StructDef.RefBox`), a reference every holder shares,
which C holds through a pointer. Its constructor is the body building into
it, each of its functions -- a setter, a slot's render, its teardown --
becomes the package's taking it first, and inside each a read of a var or a
node is a read through it. A var whose initializer reads nothing of the
record starts in the record's literal, which would otherwise make a box
field a box of its own only to replace it. `CreateComponent` is the
constructor's call, `ComponentRoot` the read of `__root`, `UpdateComponent`
the prop's setter and `DestroyComponent` the teardown; the root leaves the
tree by the parent's remove, which the render destroying the instance makes
too. A closure the record makes captured what it reads, as it captured the
var; one reading the record itself -- calling one of its functions, an
input's effect settling, or reading a node it holds -- captures the record
too, as `__self`, and so does each closure holding such a one, which hands
it on. Whether a function is handed the record already, a method whose
receiver it is, is asked before any is given its `__self`, or a call of one
given it first would be taken for a method's
(`testdata/win32_c_loop.txtar`, `cmd/sngl/testdata/c_win32_loops_runs.txt`).
A platform granting `instanceState` makes one of every component with state
of its own built at run time, a library override's included, where without
it the inliner splices one.

**A component rendering more than one node, or a slot, at its top renders
into the platform's `contents`** (`intoContents`), a container the
platform's `#[gen.protocol(contents)]` makes, as gtk4's C record makes one
with `sngl_contents_new`: made first, so the slot renders into it, and each
top node put into it ahead of the next statement rendering into the root, so
they stand in the order written. A slot whose parent is typed only as a node
is one such, and is placed through the container's append and insertBefore
(`rootContainerOp`). The component a test takes is shown the same way
(`placeRoot`): its one node attached to the application, or the container
holding several. A node one function makes and another reads -- an effect's
mount reading the input its render slot made -- is held in a package var
beside the function's local (`hoistShared`), which the function and what it
closes over still read, so each copy of a loop's handler reads its own. The
package body is such a maker for a node under a `for` it runs once -- win32's
and gtk4's `ui.tabs` walk their tabs with one, so a select in a tab is a local
of the loop's copy -- and a record holds such a node, made by one of its
functions or its body and read by another, as a field
(`shareRecordNodes`); its effects and `@change` named a local none of them
reached (`testdata/select_under_tabs.txtar`, examples/pokedex's
`#methodPick`). A node the package body or a record's body makes under a boundary is
hoisted or made a field as one made outside it (`bodyLocals`): a boundary is
where a raise is caught, which C writes as a block, so the node was that
block's local, and a handler writing it named nothing the C declared
(`cmd/sngl/testdata/render_raise_reaches_its_boundary_runs.txt`).

**A test of a placed program reaches its instance as the program's own
state** (`internal/lower/placetests.go`). A build rooted at the component a
test takes renders it as the package body would: the root's vars, functions
and body become the package's (`liftRoot`), so the language writes the one
program it writes for any build. A test's `c.n` is then the package var, its
`.value` where NoRef boxed it, and `c.save` the node's var. A node a function
other than the package body makes -- a render slot's, made and taken away as
the slot renders -- is held in a package var of its option that the
function's every render starts by setting null (`holdRead`), so `c.big == null` once a branch took it away. A record's function renders for
each of the record's instances, so it lets go of only what its own instance
made, which a field of the record notes: each tab's panel is a record, and the
second panel's render said the first's button was gone
(`cmd/sngl/testdata/tabs_runs.txt`). The package body's own branches are held
the same way, and a node a record holds is held as the record's constructor
makes it and let go by its destroy, unless another record's took its place
-- a keyed render builds the new before destroying the old -- so it too is
null once its branch is gone. An instance of a component built at run time
is held the same way, as it is set rather than declared, since a render
declares it and then sets it from the instances kept or a new one, and let
go by its own destroy; the test's conversion of it to its component is to
the record, a var of the record's NoRef boxed is read as its `.value`, and
a function of the component the instance is a copy of -- a fire lowering
wrote -- is the record's own copy of it (`recordReads`). A trigger fires the node such a var holds
(`cmd/sngl/testdata/c_win32_test_reach_runs.txt`). One copy of a node loops render,
`c.row[1]`, or one copy of an instance, whose root is read with its key,
is held by its key (`ir.CopyProp`) in a package map that each
render stamping the keys starts afresh (`copyWrite`, `clearCopies`): a
record's builder does, since a keyed render updates every prop of each
instance it keeps, the key among them. A record's own render does not, the
map being every instance's, so its destroy takes its node out where its key
still holds it (`dropCopies`; `page_write_reaches_instance_runs.txt`, the
`#extra` of a row whose `if` went false). A trigger of the copy fires the node
the key holds, and a null test of it is whether the key holds one. A method
lowering wrote for the instance is handed null for it, its state being the
program's. A test's trigger, `c.save.click()`, is the
platform's `#[gen.fires(event)]` function for the event the program's
handler was connected to (`recordFire` notes it at each connect, under the
name the program wrote, which an override may have connected under another:
ui.input's `@change` is an edit's `@activate`), handed the node and the
trigger's values. A fire puts each value where the host keeps it and raises
the host's own notification, so the program's handler runs as it would for
the user: win32's press of a check box sets its state and sends the
`BN_CLICKED` the box would (`cmd/sngl/testdata/c_win32_test_runs.txt`). The
language writes the tests with the program (`clang.WriteTests`), and only a
test's own calls -- mount, assert, wait -- are its.

The mark's other job is to stop the inliner. A platform's extension override
inlines into its caller (`passInlinePure`), and every platform-package
component must inline or the build fails — so the primitives those overrides
lower down to have to be exempt.
`isPrimitiveComponent` reads `Component.Primitive()` for that -- `Intrinsic`,
`Wildcard` (html's raw element) and a native mark -- alongside `Builtin` (a node kind) — the marks are the
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
  it. The path is what `RequireImport` adds. The qualifier names the
  package as the mark's author knew it, and the file writes it with the name
  the import has there (`GoIRContext.Qualified`, through `site.qualify` for a
  type): two packages of one name, the platform's wrapper package and a
  program's own (`sngl tool cgogen`), both `cgo`, are imported as `cgo` and
  `cgo2`, and every reference to the second says `cgo2`. Written literally,
  the second import was aliased and unused while its calls and types named
  the first (`cmd/sngl/testdata/cgogen_beside_platform_runs.txt`). A name
  no other import collides with is written as the mark spells it, and a
  method's qualifier, never written, claims nothing. fyne and bubbletea
  hand their emitter the paths alone, and a type spelled with no file to
  ask (`IRTypeToGo`) is written as the mark spells it, so a collision there
  is untested.
- **Every language has one.** `go.native` and `js.native` were joined by
  `kotlin.native`, which android needs to describe Compose, and by
  **`#[c.native]` in `sngl:macro`** — C is an ABI rather than a target, so a
  platform built on a C library (gtk4 on cairo) names C identifiers and no
  language at all. The name is C's spelling -- a function's identifier, a
  type such as `cairo_t *` -- and the checker refuses a language's (`C.`,
  `unsafe.Pointer`), so a declaration reachable only from Go cannot be written
  (`testdata/error_c_native_spelling.sngl`); each language renders it through
  `codegen.CInterop` (`design/architecture.md`). Each argument is converted
  before any backend sees the call, by `lower.CMarshal`, through the
  `sngl:x/c` conversion its parameter's type names (`cstring`, `cint`,
  `cdouble`, `cbool`), so a C string is an owned `c.str` in the IR rather than
  a `C.CString` only the printed Go held (`testdata/c_bool_argument.txtar`).
  A parameter written with a `#[c.native]` alias -- a C typedef of a scalar,
  `#[c.native("UINT")] struct UINT = int` -- crosses cast to the typedef, which
  cgo needs, since it refuses a `C.int` for an unsigned one
  (`testdata/c_alias_argument.txtar`); a string alias crosses as UTF-16
  (`c.utf16`, `testdata/c_utf16_argument.txtar`). A C function returning a `string`
  hands back a `const char *` it keeps, which the call copies -- `C.GoString`
  in Go, the turn's `sngl_string_copy` in C -- and one returning a `bool`
  its `int` (`testdata/c_string_return.txtar`, `codegen/lang/c`'s
  `cinterop` run). One answering a string it hands over, which the program
  frees, is marked `#[c.frees("g_free")]` (`Func.CFrees`): the call copies
  the string, then hands C's pointer to the named function, but NULL, which
  is what lets gtk4 call `g_strdup_printf` or read an input method's
  preedit. An `option[string]` result is `null` for NULL, freed or not. On
  a `ref[string]` parameter (`Param.CFrees`) the mark says the same of what
  C stores: C is handed a pointer to NULL, and what it stored is copied and
  freed as the call writes back (`refCall`'s temp in C, `cStringOut` in
  Go); an unmarked `ref[string]` stays refused, since nothing says whose
  that string is (`checkCFrees`). C frees through a helper per function
  (`sngl_freed_<f>`); Go cannot hand the function the pointer at a type it
  does not know (GLib's gpointer is no `unsafe.Pointer` to cgo), so it calls
  a C function freeing all but NULL (`snglFree_<f>`), which the
  `sngl_shims.h` it writes beside a variadic call's shape defines, and a
  wrapper package's preamble likewise (`ir.CalledCShimmed`, `CShims`).
  purego refuses one, keeping no pointer to free
  (`testdata/c_frees.txtar`, `error_c_frees.sngl`, `codegen/lang/c`'s
  `frees` run, `cgogen_user_package_runs.txt`,
  `go_cgo_purego_refused.txt`). The alias is expanded where
  it is written, so `ir.Param.CAlias` remembers it. It records the alias
  rather than its C type because the alias's mark may run after the parameter
  is built, and it is shared by every clone, its package being the library's
  where it is `c.utf16`: `ir.RewriteTypes` reached the checked package through
  a copied one's `Pkg` and wrote a library override's struct literal. Which
  header declares one is the program's `c.link` (`sngl:x/c`), and every Go target writes the links the program
  reaches (`ir.ReachedCLinks`) into its cgo preamble: fyne and bubbletea as a
  preamble of their own, gtk4 inside the one inline mode already has, which a
  `C.` call puts the build in (`golang.CPreambleLines`,
  `testdata/c_link.txtar`). A call of a program's `#[c.native]` function from a package
  no c.link reaches is a checker error at the call
  (`reportUnlinkedCNativeCalls`, `testdata/error_c_native_unlinked.sngl`):
  it used to reach the Go toolchain as `undefined: C`. A library's is exempt
  (`ir.Package.Library`, set for a `sngl:` package and what a library plugin
  writes), since gtk4's preamble includes the headers its cairo and `gir:`
  declarations name. A `c.link`'s `sources` are C files beside the file
  writing it, read at the check into `ir.CLink.Sources`, which a language
  compiles into the program: Go writes them beside the generated package,
  where cgo compiles every `.c` (`codegen.CCompiler.CSources`). That is how a
  platform's runtime is C rather than one language's.
  A `#[c.native]` struct declaring fields is a C struct
  (`ir.StructDef.IsCStruct`): the program's own struct, `Foreign.Marked`
  so a backend declares it, converted field by field where a call takes one
  (`CConvertStruct`) or returns one, each field as its alias
  (`StructField.CAlias`) or scalar, and a C struct it holds by value through
  that struct's own conversion, as Win32's `NMDATETIMECHANGE` holds an
  `NMHDR` and a `SYSTEMTIME` (`codegen/lang/c`'s `nested_structs` run,
  `testdata/c_nested_structs.txtar`, `cgogen_user_package_runs.txt`): C had
  written such a field `(int)v.f`, and refused a C struct a function
  answers. Held as cgo's type instead, its fields
  would be `C.int` in every SNGL expression reading one. A field may also
  be a string, as a `char *` or its UTF-16 alias, a handle, or an option of
  a `*` handle (`cPointerField`), as Win32's `LVITEMW` holds its text. A
  string is valid for the call the struct is handed to and is not read
  back, the program's struct keeping the string it held: C hands its bytes,
  which a NUL ends, or their UTF-16 in the turn's arena, as an argument's
  is, written without const so the field fits C's `LPWSTR` or `LPCWSTR`,
  and reads back through a helper handed the struct the program handed in
  (`fromCStructKeeps`). Go copies each into Go memory pinned for the call
  (`cbind.Pins`, declared by the call, `cStructPins`), stored through
  `unsafe.Pointer` since it can spell the field's pointer type only as cgo
  names it, and builds the struct read back from the value handed in. A C
  function answering such a struct is refused, having no string of the
  program's to keep (`cStringField`); a held copy is sound, being the
  program's value and never the C struct (`codegen/lang/c`'s
  `struct_strings` run, `testdata/c_struct_strings.txtar`,
  `error_c_struct.sngl`, `cgogen_user_package_runs.txt`, and
  `cmd/sngl/testdata/c_win32_interop_runs.txt`, which inserts rows into a
  real list view through LVM_INSERTITEMW and reads their text back). A
  program's own C struct in a test build calling C directly was declared
  twice, once more as another package's (`libraryCStructs`). A C function's
  `ref` parameter is a pointer it fills: `passRefParams` makes it an out
  (`cOuts`), so the call hands back what C wrote and the caller's variable
  is assigned as a bodied function's write-back is -- an assignment every
  later pass sees -- and the translator hands C the address of a C temp
  (`CInterop.CCall`). `rewriteBody` leaves a C function's signature alone,
  since its pointers are the translator's (`testdata/c_struct_argument.txtar`,
  `error_c_struct.sngl`, `error_c_struct_mark.sngl`). The C target defines a
  function per such C function (`sngl_ccall_<name>_<func>`, `refCall`)
  taking each argument as the call renders it, handing C the address of a
  C temp for each `ref` -- a scalar's, or a C struct's, converted into it
  and back field by field (`cStruct`, `fromCStruct`; gtk4's `_place` hands
  `gtk_widget_size_allocate` a GtkAllocation) -- and answering what the
  pass made of the call. A ref to an option of a pointer handle
  (`ir.IsCPointerOption`) is a pointer to the pointer, GLib's `GError **`:
  C is handed the address of a temp holding NULL, whatever the variable held,
  as GLib asks of an error and as a `ref[string]` is, and reads back null or
  the handle; Go's temp is cgo's pointer, read back as the pointer to it
  Go's option of one is, and a wrapper takes the option without splitting
  its call on null (`codegen/lang/c/testdata/run/ref_options.txtar`,
  `testdata/c_ref_option.txtar`, `cgogen_user_package_runs.txt`). `passNoRef` leaves a C function's signature alone
  too (`cRefs`): boxed, a program's own C function was handed a box where C
  takes a pointer to its type (`codegen/lang/c/testdata/run/refs.txtar`). `passRefParams` runs before `passLibFuncs`, so a promoted
  library function's `&x` reached no translator -- gtk4's probe measures
  its child through `gtk_widget_measure` that way -- and `passCRefCalls`,
  after it, lowers the calls of C functions still handing an address, and
  each promoted library function taking a `ref` with the calls of it, as
  `passRefParams` lowers a program's (`internal/lower/ref_params.go`). Any
  other call it leaves alone: it took the `&` off every call, so gtk4's
  `_layoutKidsInto(&kids, c)` handed a list where passNoRef's box was
  declared. A promoted copy's call of itself names the copy
  (`promoteFuncs`), or the pass met the frozen original, which writes
  nothing back (`gtk4_look_runs.txtar`).
  A `func` parameter is a callback: C calls a function pointer and hands it
  back a `void *`, which the declaration marks `#[c.context]` (`sngl:x/c`) and
  no caller passes. The mark gives the parameter a placeholder default, so the
  call checker binds past it, a positional argument going to the parameter
  after it, and `CMarshal` writes it in (`insertCContext`), since
  `CallDefaults` leaves a native's arity alone. The callback argument becomes
  a `CConvertCallback` holding the closure and the context a
  `CConvertContext`, which the translator writes from that closure: the
  context is what finds it, and both arguments naming one closure would
  render it twice. The context is the callback's last argument, after those
  its type declares, the shape GLib's signals, glibc's `qsort_r` and
  `EnumWindows` share. Go exports a dispatcher per callback type, named for
  it (`snglCallback_int__int`), in `sngl_callbacks.go`
  (`codegen/lang/golang/ccallback.go`), which finds the closure through
  `cbind`'s registry by the index the context holds, offset past the first
  64K so that none is NULL and none is a pointer Go calls bad: an
  `unsafe.Pointer` below 4096 on a goroutine's stack throws as Go copies the
  stack, which `examples/pokedex`'s thousandth callback reached on gtk4. They sit in a file of their own because a Go file that exports to
  C may define nothing in its preamble that another unit links, and a
  platform's preamble may. An unmarked callback is held for the call, a
  `#[retains]` one for the rest of the program, and one whose function marks
  a `#[c.release]` parameter until C calls the function the translator
  hands it there (`snglRelease`, GLib's `GClosureNotify`). A dispatcher
  finding its closure released does nothing, since C may still call a
  callback it has let go, as GLib does after a disconnect.
  (`testdata/c_callback_argument.txtar`, `error_c_callback.sngl`,
  `error_c_context_mark.sngl`, `error_c_context_argument.sngl`). gtk4
  connects every handler through its runtime's `sngl_signal_connect` this
  way.
  A callback C calls with no context -- a GTK custom layout's measure, a
  `GDestroyNotify`, a Win32 window procedure -- is marked `#[c.static]`
  (`sngl:x/c`), and has nothing to find a closure by, so its argument is a
  function of a package by name (`ir.StaticCallee`): a lambda or a method is
  refused at the call (`checkCStaticArgs`), its captures and its receiver
  having nowhere to come from. What it works on it reads off what C hands
  it, a `held` value on the widget. It takes what any callback takes and a
  `string`, copied as C calls, and answers a scalar or a handle
  (`cStaticRefusal`); the mark goes only on a `func` parameter of a bodiless
  function (`markCallbackParam`), and `checkCCallbacks` refuses it on a
  function that is not C's. Marked `#[c.outs]` as well, it answers a struct
  of the program's whose fields are `int`, `float` and `bool`, which C reads
  through one pointer a field handed after the arguments, as GTK hands a
  measure `int *minimum, int *natural`; a NULL one is not written, and an
  `outs` without `static` is an error, since a context-finding callback
  answers as it returns. C defines a function per function handed to one
  (`sngl_static_<name>`, `staticCallback` in `codegen/lang/c/context.go`),
  which enters a turn as a dispatcher does, copies a string into it, and
  writes each out. Go exports one per function from the callbacks' file
  (`snglStatic_<Name>`, `codegen/lang/golang/cstatic.go`), calling through
  a package variable the call sets before it hands C the export's pointer:
  the file is written from the package alone, and how the function is named
  -- free, or a platform's Model's -- is the call's writer's to say
  (`testdata/error_c_static.sngl`, `c_static_callback.txtar`,
  `codegen/lang/c`'s `static` run). gtk4 lays a scroll's probe out by three
  of its functions handed to `gtk_custom_layout_new`, and lets a flow's
  span store go through one handed to `g_object_set_data_full`.
  A static callback C calls with a convention other than the platform's
  default -- Win32's `CALLBACK`, `__stdcall` on 32-bit x86, for a window
  procedure or a timer's -- names it, `#[c.static(convention="CALLBACK")]`
  (`Param.CConvention`), free text written as given and judged by C's
  compiler, an empty one refused (`markCStatic`). C writes it into the
  function's definition (`static LRESULT CALLBACK sngl_static_CALLBACK_f`)
  and hands that function at the `#[c.type]` typedef with no cast: the
  `void (*)(void)` one a conventionless static, a dispatcher or a release
  still goes through is what had hidden a cdecl function handed where 386
  calls `__stdcall`, and without it MinGW's i686 compiler refuses the
  mismatch as an incompatible pointer. cgo exports only the default
  convention, so Go hands C a `static` C function of the convention in the
  callbacks' preamble calling the export (`staticTrampoline`), found
  through a function answering it, since a static function has no symbol
  cgo could take the address of; a wrapper package's callbacks hold the
  same for each wrapper's parameter. A callback's signature records the
  aliases it is written with (`Param.CAlias` on each of its parameters,
  `FuncSig.ReturnCAlias`), and a static's C function and Go export take and
  answer those C types, so the function is of the typedef's type and a
  `WPARAM`, `LPARAM` or `LRESULT` crosses whole rather than as a C `int`
  (`codegen/lang/c`'s `static_convention` run, under a convention no
  compiler calls with by default on each of its targets;
  `testdata/c_static_convention.txtar`, `cgogen_user_package_runs.txt`,
  and `cmd/sngl/testdata/c_win32_interop_runs.txt`, where a `TIMERPROC`
  ticks and a `WNDPROC` the fixture's C registers answers under Wine on
  both architectures, in C and in Go).
  A function pointer the caller hands C NULL for -- `g_source_set_callback`'s
  `GDestroyNotify` where nothing is let go -- is no callback, since none is
  handed: it is declared an option of a pointer at the typedef `#[c.type]`
  names, `#[c.type("GDestroyNotify")] notify option[c.pointer]`, and handed
  `null`. C passes NULL; Go converts what it hands to the typedef, cgo's
  `*[0]byte`, which no data pointer is (`cFuncPointer`): a wrapper package
  hands the option's pointer on, and refused to compile handing a
  `gpointer` there (`c_static_callback.txtar`, `codegen/lang/c`'s `static`
  run). A static callback with an empty body did the job before.
  A C function taking a variable argument list -- `printf`,
  `g_object_set` -- is `#[c.native(name, variadic)]`, on a function and
  never beside `cast` (`markCNative`). The declaration lists what one call
  passes, so a function called two ways is declared twice. C calls it as
  it is; cgo calls no variadic function, so Go calls a `static inline` C
  function of the declaration's one shape (`snglV_<name>`), which the
  `sngl_shims.h` it writes after the program's links defines
  (`shimsLink`).
  A member of a C struct is read by a function flagged `field`, named
  `Struct.member` (`Foreign.Field`): its first parameter is a handle whose
  C type is a pointer to the struct (`checkCFields`), and a call is
  `first->member`, or, handed more, a call of the function pointer the
  member holds with the rest (`ir.CCallExpr`), converted as a C function's
  call is: gtk4 reads `GError.message` and calls GtkLabel's class
  `measure` that way. cgo reads a member but calls no function pointer, so
  Go goes through a C function of the declaration's shape (`snglF_<name>`)
  in `sngl_shims.h`, as a variadic call does, a ref crossing it as a
  pointer to its element's C type; a wrapper package's preamble defines it
  (cgogen names the wrapper `GErrorMessage`). purego refuses one, finding
  a function by its name alone (`testdata/c_field.txtar`,
  `error_c_field.sngl`, `codegen/lang/c`'s `fields` run,
  `cgogen_user_package_runs.txt`, `go_cgo_purego_refused.txt`).
  A pointer C takes as an integer typedef -- a Win32 message's `LPARAM`
  carrying a string, a struct or a window -- is a parameter crossing as a
  pointer (a string or its UTF-16 alias, an option of one, a `ref` to a C
  struct, a handle, or an option of a `*` handle) that `#[c.type("LPARAM")]`
  marks (`Param.CIntPointer`); on a generic pointer (`void *`, a typedef
  flagged `pointer`) the mark still names the function-pointer typedef a
  NULL is handed for, and on anything else it is refused (`cTypeNames`). C
  casts the argument, `(LPARAM)p`, a `ref`'s temp's address included
  (`intPointerArgs`). Go makes no integer of a pointer it hands C -- an
  integer would keep nothing it points to for the call, Go's UTF-16 copy
  and a ref's temp among them -- so the call goes through a C function of
  the declaration's shape taking the pointer and casting it
  (`snglP_<name>`, `CShims`), as a variadic call does; a wrapper package's
  preamble defines it alike. The pointer is good for the call alone: this
  is for a function done with it when it returns, as a sent message is,
  and handed to `PostMessageW` it dangles, which nothing polices. A cast
  takes such an integer back to a pointer handle, written with the alias it
  is (`#[c.native("NMHDR *", cast)] func header(l LPARAM) Nmhdr`,
  `Func.CCastFromInt`): C casts through the alias first, `(NMHDR *)(LPARAM)l`,
  since a SNGL int is wider than a 32-bit pointer, and Go calls a C function
  making it, which a wrapper package wraps as it wraps a function, though it
  wraps no other cast. purego refuses both
  (`codegen/lang/c`'s `int_pointers` run, `testdata/c_int_pointer.txtar`,
  `error_c_type_mark.sngl`, `cgogen_user_package_runs.txt`,
  `go_cgo_purego_refused.txt`, and `cmd/sngl/testdata/c_win32_interop_runs.txt`,
  which sets and counts a window's text with WM_SETTEXT and
  WM_GETTEXTLENGTH, hands a RECT the fixture's window procedure reads and
  writes, and reads a WM_NOTIFY's NMHDR, under Wine on both architectures).
  A value C holds -- what `g_object_set_data_full` keeps on a widget -- is
  `sngl:x/c`'s `held`, a generic alias of its one parameter marked
  `#[builtin("cHeld")]` (`ir.Alias.IsCHeld`). `cAliasOf` records it as
  `Param.CAlias` or `Func.ReturnCAlias` as it records `c.utf16`, so the
  parameter's type is the `T` itself; it is the one marked alias taking type
  arguments. `CMarshal` makes the argument a `CConvertHeld`, and the
  function's `#[c.release]` parameter, which a held value needs in place of
  a callback's context (`checkCHeld`), is handed what lets the value go:
  the C target's per-type release, Go's `snglRelease`, which the registry's
  index the value is held at shares with a callback's closure. A held
  parameter names its pointer typedef with `#[c.type]` where cgo needs it
  spelled (`gpointer`). A result is `option[c.held[T]]`, since C answers
  NULL where it holds nothing, read back as the `T` the pointer finds, and
  `null` in Go where the registry holds nothing there. An option of an
  opaque handle whose C type is written with a `*` is the pointer, NULL for
  none, and so is one of a typedef that spells no `*` where its declaration
  says it is a pointer, `#[c.native("gpointer", pointer)]`, in C (`cHandle`,
  `codegen/lang/c/collections.go`) and in Go's cgo call (`cCall`), as gtk4's
  `g_object_get_data` answering `option[_Pointer]` is. The flag is written,
  not inferred from the struct declaring no fields: NULL is a value of
  Win32's `HWND`, which a keyed loop hands `sngl_win32_insert` for "at the
  end", and reading every handle's NULL as none moved a win32 snapshot
  (`dropped.png` in `cmd/sngl/testdata/c_win32_loops_runs.txt`). A Go wrapper (`sngl tool cgogen`) cannot name the program's `T`, so it takes and answers
  `any` and the program's call asserts it back (`heldOut`). purego takes
  the value's index as a `uintptr` (`cload.Hold`) and the one release
  callback cload makes. `CMarshal` writes a compiler-passed argument under
  purego too, which it had skipped with every other conversion
  (`testdata/error_c_held.sngl`, `c_held_value.txtar`,
  `c_held_purego.txtar`, `codegen/lang/c`'s `held` run). The C files a
  language adds -- a link's sources, the dispatchers -- are written by
  `codegen.Generate`, which a build, a test's launch and a dump all call:
  `sngl test` called the platform alone and built without them.
- **A component may be one, on Kotlin.** `#[kt.native("Card", "androidx.compose.material3.Card")]` on a bodyless component names a
  composable: android calls it with the props as arguments by name and the rest
  slot as the trailing lambda (`renderIntrinsic`), and adds the import unless a
  star import of its package already reaches it. android declares its plain
  composables this way rather than as `#[intrinsic]`, so a program describing a
  widget the platform never heard of writes what the platform writes
  (`testdata/android_native_composable.txtar`). It is `Component.IsNative`, a
  `Primitive` like an intrinsic, and the native rule below applies:
  `codegen.componentsFor` skips one. Any other platform refuses it in
  `passForeignPrimitive` unless its generator claims the scheme
  (`codegen.NativeWidgets`); a body or a call flag beside the mark is a checker
  error (`error_native_component.sngl`).
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
  `len(fn.Block) == 0` guard already skipped. Nor is one overridden: no target
  emits its declaration, so `func ToUpper[bubbletea.platform]` over
  `go:strings` is refused where it is written rather than checked and never
  run.
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
  makes a host handle spellable: `#[go.native("time", "*time.Ticker")] struct Schedule { C go.chan[time.datetime] }` with `#[go.native("time", "time.Ticker.Stop", method)] func Schedule.stop()` beside it -- and a *field* of one is how a host API hands a channel back. gtk4 has done the same for C all along —
  `#[c.native("cairo_t *")] struct CairoContext {}` — so the form predates the
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

  **`go.chan[T]` is an intrinsic struct declared by `sngl:language/go`**
  (`#[marks.intrinsic("go:chan")] struct chan[T any] {}`, `StructDef.Intrinsic`),
  not a type kind: to the checker it is an ordinary generic struct, and only
  the Go emitter, which answers the id, gives it a host spelling. It is
  declared there rather than in `lib/` because only a language with channels
  can answer one. It exists
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

  **`go.select` takes a `list[Case]`**, written before SNGL had variadic
  parameters, and each arm is `go.recv(ch, func(v T) { … })`. It is
  the one thing here that cannot be an `IntrinsicEmitter`: that renders a single
  *expression*, and a select's arms are statement lists. So it is answered from
  `CallStmtLines`, the statement-level seam that already existed, and the arms
  are **inlined rather than called** -- an arm may `return`, and that has to
  leave the goroutine, which a closure wrapper would not do. A case variable is
  bound only where the body reads it, Go rejecting an unused one.

  `go.makechan` takes a witness value rather than a type argument --
  `makeChan(false)` is a `chan[bool]` -- because it was declared before a call
  site could write one, and the emitter reads the element type off the
  argument.
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
  spot design/language.md records for the lowering passes, one layer out.
  An `if`'s condition is a fifth: `menu.length() > 0` over a data slot counts
  records holding the call site's handlers, and walked only through `OnCond`
  their `UpdateComponent` reached JavaScript raw. `walkExprLambdas` also
  enters an operator's operands, a member read and an index
  (`cmd/sngl/testdata/window_menu_under_if_runs.txt`).

  **The hand-over it also held is `async.do`**, which a platform package may
  name: `#[intrinsic("async.do")] func do(f func())` dispatches through
  `LookupPlatformIntrinsic` exactly as a blocking call's posted tail does, and
  on fyne emits `__snglDo`, the model's helper calling `fyne.Do` -- or, in a
  test build, queueing the closure for `t.wait` to run on the test's own
  goroutine (`agentPosts`): fyne's test driver runs `fyne.Do` on the goroutine
  that calls it, so a request's answer rebuilt a list's widgets while the test
  clicked others, and fyne crashed laying them out
  (`examples/pokedex`'s tests, in `cmd/sngl/testdata/examples_runs.txt`). What the
  last test's Model posted and the last test never waited for is dropped as the
  next test starts (`__snglDropPosts`): run, it drew the old Model's widgets
  inside the next test's waits, seconds on fyne's test driver for a page whose
  every answer redraws a list (`cmd/sngl/testdata/test_drops_last_tests_posts_runs.txt`). So a callback that must reach the drawing
  thread asks for that in one word instead of a package re-spelling it. The id
  is also the second thing `nativeCallbackFuncs` treats as scheduling, for the
  reason the `schedules` flag exists: a post runs its closure from the loop the
  platform owns, so a blocking call written inside one is on the drawing thread
  unless this pass takes the closure as an entry point. There is no *one*
  declaration to put the flag on -- the pass synthesizes calls to the id
  itself, and a platform package may declare its own -- so the pass names it.
  fyne, gtk4 and bubbletea answer the id; inside a literal bubbletea runs off
  its loop, the Go emitter spells the post itself (`GoIRContext.evalPost`),
  since the Model the post writes is the one the literal was made from.

  **Two shapes make Go's select behave unexpectedly, and both are written on the
  declaration.** A *closed* channel is always ready and yields the zero value
  forever -- which is the idiom here rather than the hazard, since closing
  `done` is how the goroutine is told to stop and its arm is taken on the very
  next pass. A *nil* channel is never ready, so its arm is never chosen, and nil
  is exactly what `chan[T]`'s zero value is.

  What such a type may *not* do is be constructed: a program holds one and
  calls methods on it. So the only literal of one that reaches a backend is the
  empty zero the checker synthesizes for an uninitialized `var t Ticker`, and
  where the host spelling is a pointer that zero is `nil` rather than a
  composite literal — `*time.Ticker{}` does not parse
  (`testdata/native_pointer_zero.txtar`).

  An **index** is still right where the host's own ABI is an index: a GLib
  callback carries an `int` user_data and cannot hold a Go pointer at all,
  which is what `pkg/go/cbind` is. The test is whether the host asked for it.

**`c.<id>` names the nodes one instance's own body wrote** (`elementRef`,
Jonathan's call). A node in a component the instance renders is that
component's ref and is reached through the id of the instance holding it —
`badge #inbox(…)`, then `c.inbox.lbl` — so two instances never share a ref and
a recursive component's `c.step` is this level's alone. Children written bare
are the writer's wherever the callee inserts them, which the rest-slot rule in
design/slots.md makes a single rendering. A slot population is a declaration with a scope
of its own, so its ids are reached through the node it populates, wrapped by
that slot's insertion count. How many nodes the read names is where the node is
written: `T`, `option[T]` behind a conditional (an `if`, a for-else, a slot
fallback, a boundary with a fallback), `list[T]` behind a `for` -- a loop
inside a loop a list of lists, and an `if` inside a `for` an entry per
iteration -- stamped on `ir.Select.Ref` (`ir.RefCount`, the wrappers outermost
first). The interpreter agrees through `interp.Node.Owner` — the instance
whose body wrote the node, nil in a population — and `View.Refs`. A compiled
test answers `c.x == null` with a presence check per id (`codegen.RefNullTest`,
`codegen.LowerRefNullTests`): html's `data-sngl-id`, fyne's and gtk4's
`__named_<id>`, bubbletea's conditions around the node, and android's
semantics tree. Each finds the node by its id alone, so in a test build
`passTestRefOwner` first renames a node another body writes under an id a test
reaches on the tested one (`b__own0`), before anything moves a node out of the
body that wrote it (`testdata/test_ref_owner.txtar`). Another body includes a
library override the build renders -- gtk4's datepicker writes a `#cal`
(`test_ref_owner_library.txtar`) -- and in a build of the whole program each
reached id is kept by the first tested component writing it and renamed in
the rest, a select off a renamed component's instance following
(`test_ref_owner_whole_program.txtar`). A diagnostic names a renamed handle by
`ir.Var.WrittenName`, the id the program wrote. A slot's reset
clears the field of a widget a test null-tests on fyne and gtk4, which still
held a widget the branch dropped. An
instance built at run time has no widget of its own: on fyne and gtk4 it hands
its root to `__present_<id>` where the root is bound, and on android its call
site holds a token in `SnglPresent` while it is composed
(`testdata/ref_null_test_instance.txtar`). `c.x[1] == null` null-tests one
copy, an iteration of the loops around the node with or without an `if`
between, by the copy's key as a trigger names one (`<id>PresentAt`, `codegen.PresenceAtMethod`), and `ir.IndexedRefIDs`
stamps the ids a test reaches either way
(`testdata/indexed_ref_null_test.txtar`). One copy of an instance built at run
time is handed its key in the `__copy` prop (`passCopyKeys`), and the call site
lists its root under that key where it binds it -- `ComponentRoot`'s second
argument, after the setter has re-keyed an instance a slot reuses: html's
`data-sngl-copy` beside `data-sngl-instance`, fyne's and gtk4's
`__copies_<id>`, one map for every record: a re-key replaces the entry and a
record's `Destroy` takes its root out -- a record holding a nested instance
lists none of it, since the nested one takes its own root out
(`golang.InstanceRecordTypes`; listed with the widgets, it compared a widget
with a record and the program did not build) -- and android's `SnglPresent` entry
`id[key]`, each asked only for an id some such instance carries
(`codegen.KeyedInstanceIDs`); html places a static instance's root, built
once at a fixed position, with the same marks. bubbletea splices it and answers from the view (`testdata/indexed_ref_null_test_instance.txtar`).

**An instance is present wherever its call site is rendered**, whatever
its body renders now -- `name.empty` is the separate question of what it
renders. So a test's null test of a program component's instance is the
conditions of the `if`s around its call site and, for one copy, whether each
loop around it renders that copy (`answerInstancePresence`, in passTestReach,
read through the instance that wrote the call site, whose own presence is
asked first), on every target alike
(`cmd/sngl/testdata/test_reach_review_runs.txt`'s present.sngl). Where
something else decides the call site -- a boundary, a slot fallback, a
builtin node such as a page, a container inserting its children other than
once -- or the conditions read what a test cannot name -- a context, a loop's
variable, a prop bound to one -- the target's presence check answers instead,
as follows; one for a node the build folded away answers false
(`codegen.RendersNowhere`). A node reached through an instance whose body the
build folded it out of is absent in passTestReach itself: its null test is
the constant, a branch on one is decided -- through a local holding it too,
which stands for the ref -- and any other reach of it is refused where it is
written. A test firing an event its call site handles
none of is refused where it fires it.

**A spliced instance a test null-tests is found by its roots.** A splice
leaves no node of the instance's own, only the roots of its body -- the nodes
it renders first, through an `if`, a `for` or a boundary there -- which carry
the instance's id in `NodeInst.Instances` (`ir.InstancesOf`). bubbletea's
view answers from those. On every other compiled target `passCopyKeys` stamps
each root node `ir.InstanceRootProp` (`__instance_<id>`), holding the key of
the copy the instance stands in -- the loops around where it was written,
not a `for` of its own body -- or "" for one rendered once. A root that is
a component built at run time is left unstamped: it declares no such prop,
and a reactive `for`'s reuse pushed it to a setter none has, which refused
the Pokédex's home once each card asked for its species
(`testdata/null_test_instance_query_in_loop.sngl`): html writes it as
`data-sngl-root-<id>`; fyne and gtk4 list the root in the instance's
`__copies_<id>` or `__present_<id>`, take it out where a render removes it
and where a record that listed it is destroyed (`__rooted`,
`__unlistInstance`); android holds a `SnglPresent` token while the root is
composed. The two splices that move the call site's `#id` onto the body's
first node leave a program component to passNoInlineComponents where that
node is one an `if`, a `for` or a boundary's fallback decides
(`ir.NodeIDRendersWithBody`). A null test of a node nothing decides is one
too, answered present (`testdata/indexed_ref_null_test_spliced.txtar`). A
placed node's var is typed as the host holds it, a fieldless foreign struct
(gtk4's `Widget`, a `GtkWidget *`), which the fold does not take for present:
a node under an `if` a context's provider settled false is never built, and
`c.p.w == null` folded to false (`alwaysPresent`;
`cmd/sngl/testdata/test_reach_review_runs.txt`, `context.sngl`).

**An event's parameters are its declaration's, and an override reads what the
host reports and writes the prop it reports.** A platform override handles
its host widget's event, writes the two-way prop the control holds from what
the host reported, and emits the `sngl:ui` event with the same value:

```sngl
html.input(type="checkbox", checked=checked, @change(e) {
    checked = e.currentTarget.checked
    change(checked)
})
```

The write is what `passPropBindings` already makes of a body writing its own
two-way prop -- the `@checked` report a `:checked` binding's handler answers --
so nothing guesses which event reports a prop or which of its values does.
There used to be such a guess (`injectNativeEmit`): candidate event names by
the prop's type, a payload field matched by type, and a toggle where none
matched. Each target reads its host its own way, in its own `.sngl`:

- **html**'s element declares the DOM's event as an external struct,
  `#[js.native("Event")] struct Event { currentTarget Target }`, so a handler
  on an element is handed the DOM event and reads `e.currentTarget.value`.
  `@click` hands a `ClickEvent`, whose `target.closest(controls)` is how a
  clickable card hears only the clicks no control inside it handled: the DOM
  bubbles a button's click -- a mouse's, and the one Enter makes -- up to the
  card, which clicked it as well; the other targets hand a click to the
  innermost control already
  (`cmd/sngl/testdata/card_click_stays_with_its_control_runs.txt`).
- **fyne**'s primitives declare an event vocabulary by type, as their props
  are -- `@change(text string)`, `@toggle(flag bool)`, `@adjust(number int)`,
  `@pick(date option[time.date])` -- and a `Handler` names the Go callback
  whose `signature` spells the same parameters in Go. The promoted handler
  takes the SNGL handler's own parameters (`namedAsHandler`), the declaration
  and not a copy, so its body reads them by symbol; a Go lambda scopes its
  parameters as locals (`evalLambda`), or one named like a component's var
  read the var.
- **gtk4**'s GIR signals hand their callback the instance they fired on, which
  is what every generated event now declares, `@toggled(self GtkCheckButton)`; a component may name itself in its own signature for this.
  The override reads that widget with a getter `gtk4.sngl` declares --
  `checkActive`, `switchActive`, `entryText`, `calendarDate` -- SNGL calling
  GTK's own getters on the widget a node is, which each language calls as it
  calls C. The trampoline passes nothing, so
  a signal handler's `self` is the node it is connected on: placement wraps a
  handler taking the widget in one taking nothing that hands it the node
  (`handedTheNode`).
- **android**'s Compose callbacks pass the value: `func(on) { checked = on; change(on) }`; one passing nothing (radio's `onClick`) names the loop's
  option.
- **bubbletea**'s key press flips the state itself: `checked = !checked; change(checked)`.
- **none** renders a `Control[T]` primitive (`none:Control`) the trigger
  reaches, whose handler writes the prop; the snapshot prints the `sngl:ui`
  component in its place.

**A write the program makes fires none of a control's events, and each
override holds to it** (`cmd/sngl/testdata/program_write_fires_no_event_runs.txt`,
on every target). A handler reports only a value the program does not already
hold -- `if checkActive(b) != checked { … }`, fyne's `if v != value { … }` --
so a bound value written to the widget, whose host fires its change signal for
the write, reports nothing; a commit (an entry's `activate`, fyne's submit) is
not guarded, since pressing Enter commits whatever the value. A host whose
write fires its signal more than once is written quietly instead: gtk4's entry
overrides do not bind `text` but push it from
`effect(on=value, @mount(v) { if entryText(entry) != v { setEntryText(entry, v) } })`,
`setEntryText` (the runtime's `sngl_editable_set_text_quiet`) blocking
"changed", because gtk_editable_set_text deletes and reinserts and a runtime
instance's handler writing the text back re-entered itself until the stack
overflowed (`testdata/gtk4_entry_two_way_no_echo.txtar`).
`selectDay` blocks "day-selected" the same way; a test's fire
puts the value in place first and emits the signal itself (`_rtFireDay`).

A test's trigger reaches the platform's input path: html writes the
arguments, by their declared names, onto the element and the dispatched event;
gtk4 sets the widget's state (which fires the signal) or its text; fyne calls
the callback; bubbletea and android press the key and click the control,
which flips it whatever the test passed. The interpreter enters at the node
the override renders whose handler emits the event (`forwardingDescendant`),
and a write of an instance's bound two-way prop writes the var its call site
bound (`Env.writeBound`). `testdata/toggle_change_payload.txtar`,
`ui_event_params.txtar` and `canvas_click.txtar` are the code,
`cmd/sngl/testdata/toggle_change_payload_runs.txt` and
`ui_event_params_runs.txt` the answer.

**An event no host widget reports is a primitive of the platform's own.**
`ui.scroll`'s `@reachEnd` has no signal on any target, so each answers it with
something it ships: android's `SnglScroll` (`scroll.kt`), html's
`sngl-reach-end` element (`reachend.go`), gtk4's scroll (`_rtScrollNew`) and fynelayout's
`ReachScroll`. Each fires as the end arrives and again when the content
grows while it is there, so a short first page loads the next without a
scroll; content shrinking while it is there -- a row removed, a filter
narrowed -- is nothing new, as html's sentinel, still in sight, reports
nothing, where gtk4 and fyne fired on any change of size. gtk4 and fyne measure on a scroll, a resize of the viewport and the
content changing size, which neither toolkit reports of its own: each puts a
probe between the viewport and the content, a box Fyne or GTK lays out again
as the content's size changes, and gtk4's reads the content's natural height
there, since the adjustment's upper is never less than the page. Each posts
its report -- to GTK's idle, or `fynelayout.Post` -- rather than calling the
handler while the toolkit lays the tree out, and one report answers every
measure before it runs. fyne used to call it from the renderer and look again
100 ms later, so a first page fetched slower than that never loaded the next;
gtk4 never heard content shorter than its viewport grow
(`pkg/go/fynelayout/scroll_test.go`, `cmd/sngl/testdata/gtk4_test_runs.txt`'s `scroll.sngl`). A fyne
test build queues `fynelayout.Post` for `t.wait`, since fyne's test driver
runs `fyne.Do` at once. html's sentinel dispatches `reachEnd`, which `html.element` declares,
because html names a test's invoker after the DOM event a handler listens
for: an override mapping `@reachend` to `reachEnd()` would have left
`c.list.reachEnd()` naming an invoker nothing attached. Where an override
writes the control beneath the node the call site named -- bubbletea's
clickable card is a box holding a focusable `›` -- the invoker is the named
node's (`triggerOwners`), and a looped copy is keyed by that node's copy key.
bubbletea fires no `@reachEnd`, and a test firing one on a layout -- which no
key reaches -- runs the handler directly (`emitBtDirectInvokers`)
(`testdata/card_click.txtar`, `testdata/scroll_reach_end.txtar`).

**A select is one control on every target**, so its `#id` names the widget a
test's `c.pick.change(Fruit.banana)` chooses through, as a user does: html's
`<select>`, fyne's Select, gtk4's GtkDropDown, bubbletea's `tui.Select`,
android's read-only field and the interpreter's own
(`cmd/sngl/testdata/select_change_runs.txt`).

**Each control knows its choices by label**, and the value a `choice[T]`
holds is the override's to convert: it shows `ui.choiceLabel(options, value)`
and writes the value of the choice `ui.choiceLabeled(options, label)` finds.
A test hands a value, and each target's override of the event says how its
control takes it (`func ui.select[T].change[platform](value)`, see
design/testing.md): gtk4 selects the label through `selectLabel`
and fyne through `Select.SetSelected`, each of which reports the change as a
pick does; html sets the `<select>`'s value to the label and dispatches
`change`, and checks the radio button at the choice's index under the
`<fieldset>` (`group.inputAt(at)`) and dispatches `change` on it, so the
listener the page attached runs as it does for a person; bubbletea and
android call the method their control's own handler calls with a label
(`pickLabel`, `choose`), and a radio's the one its buttons call
(`pickValue`). A radio is a control per choice on every target but html: gtk4's check buttons, fyne's Checks, bubbletea's `(•)` rows, each
calling `pickValue` when chosen, so a program's write -- which checks the new
choice, and fyne's Check reports -- fires nothing
(`cmd/sngl/testdata/radio_change_runs.txt`). Handed to the generated invokers, the value gave
fyne's `OnChanged` an enum's name where it knew a label, and an int did not
compile.
bubbletea's binds write the call site's var from Go, so its override holds
the label as state the widget binds (`Widget`'s `:choice`) and converts in its
`@change` (`testdata/select_typed_choices.sngl`).

A select laid out as one widget
per choice leaves the id on the box around them, which no trigger reaches,
and a menu the trigger cannot type into is no input path a test has. Each
holds the bound value even before a choice carries it, since choices may
be state that arrives later, and neither offering choices nor writing the
value is reported as a choice:

- **gtk4** offers a GtkStringList through `offerChoices`, which blocks
  `notify::selected` meanwhile (`testdata/gtk4_choose_runs.txtar`); the
  handler reads `dropDownLabel`. The invoker selects the label, which
  notifies, and fires nothing after it. Each splice of the override has its
  own `#drop` (`freshNodeHandles`, as datepicker's `#cal`): the declaration's
  one handle, shared, made two selects in a component one handle rendered
  twice, which the build refused. A select a render slot builds is a local
  of the slot's render, held in a package var a test reads besides, each
  named apart (`hoistShared`; `cmd/sngl/testdata/controls_several_runs.txt`). The invoker holds the
  drop-down until the selection is done: a choice whose handler renders afresh
  the slot the select is in let go of its last reference while
  `gtk_drop_down_set_selected` was still running
  (`cmd/sngl/testdata/select_rebuilt_by_its_choice_runs.txt`). The offer is
  keyed on the drop-down as well as the labels, so a render that builds it
  afresh offers the new one its choices (see *Queries* in `language.md` for
  why a key reading a node mounts again;
  `cmd/sngl/testdata/select_rebuilt_slot_runs.txt`), and a drop-down not built
  yet is offered nothing.
- **bubbletea**'s `tui.Select` is handed the choices on each view and update
  (`View(options)`, `Update(msg, options)`), its value is the truth and the
  cursor follows it, and a choice is the cursor moving or Enter on the row it
  is on, so `@change` fires when an update changed the value
  (`choosesOnMove`). A table is handed its headings and rows the same way
  (`tui.Table`): built once from the rows it started with, it showed
  headings alone where the rows were a request's answer
  (`cmd/sngl/testdata/table_rows_runs.txt`).
- **android**'s select is a read-only field a tap opens a menu from, an
  override holding whether the menu is open. A user cannot type into it --
  an editable field took any text -- but its box's semantics answer
  `SetText` with a choice's label as a pick (`snglChooses`, a Kotlin modifier
  the platform ships, since Compose spells semantics as a lambda with a
  receiver). A trigger's `change` runs that
  action rather than `performTextReplacement`, which refuses a field that is
  not editable, as its `click` runs `OnClick` rather than a touch.

**A tab is chosen by pressing it.** No node of a `ui.tabs` reports `@change`:
each tab of the strip a loop renders emits `change(k)` from its click, so the
`#id` a test fires through lands on the box around them and named nothing
that answers -- the interpreter said so, html chose nothing, and the Go
targets called an invoker nobody wrote. A trigger of an event the node the
id lands on does not report, where the body emits it from a looped node's
handler or callback handing on the loop's index (`ir.EmittedIndex`), is a
press of that node's copy at the index the trigger hands: `c.strip.change(1)`
clicks the tab at 1, which writes `selected` and fires `@change` as a user's
click does. `lower.NamePresses` gives the node the id `<id>__<event>` and
turns the trigger into `c.strip__change[1].click()`, the invoker each target
already has for one copy of a node loops render; both inliners call it,
before the emits are substituted, since the optimizer splices a body no
target overrides. Which node a trigger presses is one rule, `ir.PressedNode`,
that both sides ask: none where the body's first node, the one the id lands
on, reports the event itself; otherwise the first node under exactly one loop
of the body whose handler, or callback taking nothing, hands on that loop's
index -- the first variable of a two-variable loop over a list, since one
index names one copy, and an element, a map's key or a count names a value.
The optimizer rewrites a trigger only once it keeps the splice. A trigger an
earlier splice rewrote names the node too, so two components each holding a
`#strip` are each pressed through their own. The interpreter finds that
node's copy by evaluating the index in each copy's scope
(`interp.View.Choosing`), among the nodes the instance's own body wrote and
before any node that reports the event: a select in a panel is the panel's
writer's, and the press chooses the tab all the same. It had a rule of its
own -- any loop variable, compared by value, pressed even where the first
node reported the event, and no callback -- which no program could tell from
the targets', since only a library body is pressed (a program component's
trigger runs its call site's handler) and `ui.tabs` met both;
`internal/interp`'s `TestChoosingPressesByTheRuleTheTargetsDo` pins it. Android's `Tab` takes a
`modifier` so the copy carries its testTag. `cmd/sngl/testdata/tabs_runs.txt`
runs it on all six, through an instance as well.

Where the pressed node is a primitive the override writes, rather than a
component whose own override re-raises its event, the handler's
`ComponentEvent` is the tabs' `change` it raises, and that is what gtk4 and
fyne name the invoker for (`codegen.TriggerEventName`). A button's is `click`,
the name its own override hands on, which NamePresses also wrote, and the two
agreed by that alone. gtk4's and fyne's `TabPage` therefore declares its event
`@change`: a page whose event was `@shown` was pressed as `shown` and invoked
as `change`, and the test's call named a method nobody wrote.
testdata/tabs_look.txtar.

**A menu entry is picked by pressing what the window drew for it.** A
test reaches an entry as a record through the window's data slot,
`c.main.menu[1].children[2].select()`, and the interpreter runs the record's
`@select`. A compiled target's record is the call site's list re-evaluated
in the test, whose handler writes the state and runs none of the updates the
window's own drawing carries, so passTestReach notes each pick's path
(`ir.Package.RecordPicks`) as it answers the slot with that list, and the
inliner that splices the window turns the pick into a press
(`lower.NameRecordPresses`, beside NamePresses): the node the body draws
under an indexed loop over the slot, and one over each record's slot below,
whose handler or callback fires the record's event is named
`<id>__<slot>[__<slot>]` -- `main__menu__children` -- and the pick becomes a
press of its copy at the record's indexes, from the test's own instance as
passTestReach flattens every other ref. Where the body hands the slot on --
html's window writes `ui.menubar(items=menu)` -- the pick follows it to that
node (`forwardPick`). A target that draws nothing for the records, bubbletea's
window, leaves the noted list standing, and the record's handler runs. A test
opens a submenu before picking from it, `c.main.menu[1].select()` first, as a
person does: android draws an entry only while its DropdownMenu is open. A
window's own event that takes nothing, `c.main.back()`, is a press of the node
its body draws that emits it (`windowPresses`): fyne's and gtk4's `Back`,
which is there only while the window can go back. testdata/window_menu.txtar.

A test's read of a record's field, `c.main.menu[1].children[2].checked`, is
that one entry as its call site wrote it (`lower.answerTestRecordRead`, ahead
of the slot's own answer in passTestReach). The records DataFamilies wrote are
walked by the test's indexes: a `for` among them is its map's function applied
to the list at the index, an entry after the loop is counted past a const's
length, and the field is the answering record's -- `sizes[2] == c.size`. That
is what the window drew the entry from, as a looped node's prop is read off
its loop's list at the index. It used to read the slot whole and index it, so
the test built every entry and each handler with it, and a handler writing
what an instance built at run time reads brought that instance's updates
(`UpdateComponent`, which no test emitter had) into a test that only read.
Where the list does not say which record an index names -- an `if` or a
forwarded slot before it, a non-literal index past an entry -- the slot is
still answered whole. An event is a pick and goes as above, so bubbletea's
pick still runs the rebuilt record's handler. testdata/window_menu.txtar
denies the rebuilt menu in the tests, and
cmd/sngl/testdata/window_menu_checked_runs.txt reads entries before, in and
after a loop on all six.

**Every tab's panel is mounted with the tabs, and selecting one shows it.**
`ui.tabs`' body puts each panel, in its record's iteration, in a `vbox`
whose `display` is `none` unless its tab is selected. It rendered the selected
one alone under `if k == selected`, so leaving a tab dropped its panel and
coming back started it again: a counter at zero, a query asked again. A null
test asks whether a node is mounted, so a node of a panel not shown is
present (`tabs_runs.txt`, `tabs_panel_presence_runs.txt`). The interpreter
keeps that body; every other target overrides it, and shows one panel its own
way. gtk4's `Tabs` is the runtime's GtkNotebook, each panel a `TabPage` the notebook
holds -- a GtkStackSwitcher over a GtkStack drew a row of linked buttons, no
tabs (`TestTabsAreANotebook`); fyne's is fynelayout's AppTabs, each panel a
`TabPage` whose box is an item's content; html keeps a `role=tabpanel` per tab
with all but one `hidden`; bubbletea puts each panel in a Layout whose
`fit.hidden` draws nothing while its children still render, under a bar of
tui.TabLook boxes. Tab passes over a hidden panel's controls, as over any box
displayed none (design/instances.md's *A stop that draws nothing is passed
over*; `TestTabPassesOverAHiddenTabPanel`).

A page reaches the stack, or the AppTabs, however the render slot of the
strip's loop hands it to the tabs: gtk4.sngl's box append, insert and remove
(`_rtBoxAppend` and its kin) route a child of a `Tabs` to its stack (`testdata/gtk4_tabs_runs.txtar`), and fynelayout's `Tabs`
is a widget drawing the AppTabs whose `Container` -- the box the slot inserts
into, drawn nowhere -- holds the pages, its layout making their items the
AppTabs'. A `Tabs` that embedded the box drew nothing, since Fyne walks into a
`*fyne.Container` and a widget and nothing else. GTK and Fyne report the
program's own change of the page shown as they do the user's, so each runtime
fires a page's `@change` only for a page the program did not select.
testdata/tabs_look.txtar and cmd/sngl/testdata/tabs_look_runs.txt.

On fyne and gtk4 each panel's page is lifted into an instance of its own
(`liftSlotBodies`), one per tab, which put two gaps in reach. Each one's
render cleared the Model's `__present_<id>` for an instance only another
builds, so after a switch the counter in the first panel read as gone; a
record now takes the field back only where a render removes the root it holds
(`releaseRemovedInstances`). And a widget a test fires that the record's slot
builds, a button written straight in a panel, had no invoker, since a
record's invokers were gathered from its body alone: its slot now hands the
widget to the Model as `__named_<id>` and its invoker goes through that
(`ir.TriggeredIDs`).

android pages through the panels: `SnglPager` (`codegen/platform/android/ pager.kt`) is a HorizontalPager under the TabRow, `beyondViewportPageCount`
one less than the count so no page is disposed, its page a slot taking the
index -- the first intrinsic slot with parameters, which `slotArgs` now
binds. `selected` moves it, and a swipe settling on another page hands it to
`onSelect`; one settling on a disabled page goes on to the next that is not,
or back. It fills the height under the TabRow with `fillMaxHeight` rather than
a weight: in a `ui.scroll` the Column has no height to share, a weight there
measures to nothing, and the Pokédex's detail panels collapsed, where
`fillMaxHeight` lets the pager take its page's height. The override's `change(k)` there sits under `flexShare`, and an emit
under a context provider was never answered, coming out as an `onChange`
nothing declared (`substituteEvents`). html's panels are in `sngl-swipe`
(`codegen/platform/html/swipe.go`), an element the page defines that hears a
touch or a pen move sideways and dispatches `swipe`, `detail` 1 toward what
follows; one starting in a sideways scroll is the scroll's, and a mouse
selects text. Its handler chooses the nearest tab that way not disabled.

A test swipes with `sngl:test/mobile`'s `swipe(node, toward)`: android's
Compose test injects the finger into what scrolls in the node
(`performTouchInput`), html dispatches a touch's pointer events on its
`sngl-swipe`. `testdata/tabs_panels_stay_loaded.{sngl,txtar}` and
`cmd/sngl/testdata/tabs_panels_stay_loaded_runs.txt` pin all of it, the
swipe on android and html.

**A record's content is counted by where it was written.** A data family's
content becomes a population of its container's hidden content slot,
branching on the record's number (`ir.RecordID`), and once the container is
spliced that branch sits in the container's loop over its records -- a tab's
panel in the strip's `for var k, t = items`. One iteration renders it, so a
`for` in the panel makes the only copies a test indexes, as the checker types
`c.cell` and the interpreter mounts it. `ir.RecordBranchOf` names the loop a
`t.__id == N` branch picks one record of, and `ir.BlockLoops` leaves it out
of a copy's key (`passCopyKeys`) and of the loops a copy's prop read binds
(`nodeUnderLoops`), of the loops whose index keys a stateful component's cells
where bubbletea splices one under a `for` in a panel
(`cmd/sngl/testdata/tabs_panel_cells_runs.txt`), and of the key bubbletea's
presence check matches a copy by, which put the tab's index in front of it
(`cmd/sngl/testdata/tabs_panel_presence_runs.txt`); the interpreter found no loop at all, since `refLoops` did
not look in slot populations, and walks `ir.ViewBlocks` now. A panel with
state of its own is an instance whose render slot builds the copies: fyne and
gtk4 declare the Model's map of them once that slot's method is walked. And
`sngl test` lowers each group with that group's tests alone, as it generates
it: a key stamped for another group's test named a map this group never
declared. `cmd/sngl/testdata/tabs_runs.txt` runs it on all six.

**A `#id` on a visual node declares a handle, and `ir.Var.NodeHandle` is what
says so.** Every target stores one wherever it keeps the tree — a field of the
Model on the Go targets — rather than as a local, so a read of it has to be
qualified. There are two ways a reference can be recognized as one and only one
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
`gtk_progress_bar_pulse` sets nothing, so GIR describes no property for it; it
is one of the *actions* `declgen` generates from GIR -- a method returning
nothing, no property's setter, whose values each cross as a SNGL scalar
(`gir.methodParam.crosses`) -- as a `#[c.native]` method reached through the
handle. A value crosses only where cgo takes the conversion CMarshal writes:
a `C.int` passes as a `gint`, a typedef of `int`, and not as a `guint`.

**A node's one-way prop may not be written**, which `refuseNodePropAssign`
reports on both the assignment and the toggle paths, through the node's `#id`
and through a parameter a handle was passed to alike. A one-way prop is
declarative: `ui.text(value=greeting)` says what the node shows for as long as
it is rendered, and reactivity re-evaluates it when `greeting` changes. A write
beside that is a second source of truth the next render undoes, so the program
that looks like it worked is the one whose write is silently gone — change the
state the prop reads instead.

**A two-way prop may be** (Jonathan's call): it is already a cell the host
writes -- a checkbox reports its clicks through it -- so a write from the
program is one more writer of that cell and lands where the host's report does.
`passHandleParams` makes `bound.checked = false` under `:checked=shown` a write
of `shown`, and inlines a call handing a handle to a function that writes one
through the parameter (`details.open()`); an unbound one is left to
`passImplicitState`, which names the instance's cell. The interpreter writes
through the handle the same way (`writeThroughHandle`, and a toggle through
one). `testdata/node_prop_write_two_way.sngl` runs it, `.txtar` is the code on
all six targets, and `error_node_prop_assign.sngl` is the one-way half.

What a *lowering* writes is untouched, and is how a prop reaches a host at all:
`passReactivity` and `passDeclarative` emit `__n0.value = expr` by the hundred.
The two are told apart by `ir.Var.Synthesized` — and, where lowered IR is
printed and checked again, by `checker.Config.Lowered`, since `text #__n0(…)`
re-parses as an ordinary node with an ordinary id and nothing in the text says
which side of the pipeline wrote it. Only a caller that lowered the IR itself
may set that flag.

**An html element's handle is the element**, which an override drives
through the natives `sngl:platform/html` declares on `element`: `press`,
`fire`, `setValue`, `setChecked` and `inputAt`, each a DOM method or a page
function html writes where a call reached it (`elementHelpers`). `press` is
named for the act, since `click` is the element's event a test raises by
that name. `componentNamed` finds the wildcard `html.button` resolves to, so
the handle is typed as one; it was invalid, so `b.click()` checked as nothing
and was emitted `b()`. A read is spelled as the element's `$N`
(`ExprCtx.NodeVar`), found by the handle's symbol (`handleVar`) and only for
an element written into the page's markup: by name, a second splice's method
pressed the first splice's button, and a component built at run time pressed
the page's element of its handle's name. An element whose handle is read is
written with an id to bind it by, a listener or no (`readHandles`). One built at run time is
a local of the function building it, named for the handle, which a read
beside it spells; a trigger html writes on the page reads it from outside, and
finds it by `data-sngl-id` as the invokers do
(`cmd/sngl/testdata/html_element_handle_runs.txt`).

**So a handle's remaining use is reading**, and a *prop* read does not compile
on the Go mutation platforms: `GoIRContext.Select` ends at
`operand + "." + ExportName(field)`, inventing a Go field by title-casing the
SNGL prop, so `box.value` is `m.box.Value` against a `widget.Entry` that spells
it `Text` — and against a gtk4 handle that is an `unsafe.Pointer` with no
fields at all. On html it reaches the emitter and renders an empty element
nothing fills. The one read that does work is a `#[c.native]` method's receiver,
above. Closing the rest needs a language↔platform read hook that does not
exist, and on gtk4 a getter is a call (`gtk_editable_get_text`) whose name GIR
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

**An `#id` is a declaration, hoisted to its component's scope.** It shadows
what an enclosing scope binds and is a redeclaration beside a var, a prop or a
method of that scope (`declareNodeID`), however deep in the body the node is;
inside an `if` or a `for` it is re-bound as the plain handle
(`declareOwnNodeIDs`), giving way there to the head's variables and the block's
own declarations, which are closer. It used to yield to any existing name
instead, and quietly: `card #card` beside `component card` bound nothing, the
node carried no `Handle`, the inliner recorded no splice of it, and a test's
`c.card.hits` went unrepointed on every compiled target
(`cmd/sngl/testdata/test_reach_id_named_like_component_runs.txt`,
`testdata/node_id_shadows_outer.sngl`, `error_node_id_redeclared.sngl`). A node
position looks a component up past the id (`Scope.LookupComponent`), which is
what `componentNamed` asks too.

**`#[foreign]` records what a declaration corresponds to outside SNGL.** It
lives in `sngl:macro` for the same reason `shape` lives in `sngl:ui/draw`, and
because its users are outside the compiler: a language plugin generating marked
SNGL to describe a foreign API, a platform package naming its host types.
`#[foreign("go:example.com/api", "api.Entry")]` gives the import path and the
name there; a struct field writes the name alone, `#[foreign(name="Title")]`,
having no package of its own. A mark's arguments bind as a call's do --
positionally or by name, constants only, a trailing flag list being a rest or
`flags=[…]` (`markArgsFor`). A function may add flags — `pure` and `async` — that state what a call
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
because the store reports it. A SNGL `const func` reading through
`sngl:x/gen` is the other, and for the same reason: every such read records
what it read (see *Trust*).

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
