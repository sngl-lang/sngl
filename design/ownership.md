# Ownership

A value the program allocates and something must give back -- a C string
handed to cairo, a pixbuf, a widget a C library keeps a pointer to -- is
owned, and the IR says so. The spec's *Owned values* is what it means; this is
how the compiler holds it. #110 is the bug: the Go printer wrote `C.CString`
into each C call's arguments (`wrapCArgs`, since gone) and the gtk4 translator
invents more after lowering, so no pass that can add a statement ever saw an
allocation, and every canvas that draws text leaks per frame.

**A C call's arguments are converted in the IR.** `lower.CMarshal` wraps each
argument of a call whose `Foreign.Path` is `"C"` in the `sngl:x/c` conversion
its parameter's type names: `cstring` for a `string`, `cint`, `cdouble`, and
`cbool`, whose `=>` body is written at the call, since a library function is
emitted only when the program reaches it. Each is a `#[builtin]` function,
found by kind among what the program and the C function's own package import
-- which is why gtk4.sngl imports `sngl:x/c` it never names. The pass runs
after `LibFuncs`, so a promoted library helper's calls are reached, and before
`NoTernary`, which lowers `cbool`'s. `cstring` answers a `c.str`, which is
`#[owned(free)]`, so a C string is an owned value like any other and
`ReleaseTemps` will see it. The printer keeps only the other direction, a C
number coming back as `int(...)`. A `bool` argument used to panic the printer,
cgo having no conversion for one (`testdata/c_bool_argument.txtar`); `free`
reaches cgo's `C.free` through `unsafe.Pointer`, a qualified native name the
printer spells as written (`testdata/c_free.txtar`).

**A type declares its release.** `#[owned(release)]` (`lib/macro/owned.sngl`)
marks a struct and names the function that gives one back, stored as
`ir.StructDef.Release`; `ir.ReleaseOf(t)` and `ir.IsRelease(fn)` ask it.
Nothing is recognized by name. The release is resolved when the mark runs, at
the end of registration, but its signature is judged in `checkOwnedReleases`
after every body is checked, since a `=>` release has no return type until
then (`testdata/error_mark_owned.sngl`). `owned` is the one generic macro, so
`resolveMacroSig` pushes a macro's type parameters.

**A type the host counts names a retain** (`StructDef.Retain`, `ir.RetainOf`,
`ir.IsRetain`): `#[owned(unref, retain=ref)]` names a `func(T) T`, judged in
`checkOwnedReleases` beside the release (`testdata/error_mark_owned.sngl`).
The macro's parameter defaults to a lambda, as `gen.sngl`'s function-typed
parameters do, since a mark's optional parameter is one with a default. The
retain is written by `OwnedState` (`stored`), once the derived values are
cells and the holder vars are collected: every initializer of a holder var --
a component's, a view block's, a package's -- and, through `bindNew`, the
value of every assignment `overwrite` rewrites, and what `push` and `insert`
hand a list (`putIn`), is passed through the retain where it reads what some
other holder holds (`holderRead`): a var other than a local, a parameter, a
loop variable, a field, an element, what an intrinsic reads out of a
collection, through the `copy` the checker wraps a struct read in; a literal
or a conversion in that position is entered, and a ternary's or a match's
arms are each stored. A local names a value rather than holding it: one
initialized by an allocation moves it into the holder it is stored in, and
one initialized from a holder's value is an alias (`findAliases`, a walk
that meets each declaration before its reads), stored as the holder's own
would be. The retain call is an allocation like any other to every later
pass, so a `__new` bound to one is the var's, and `ReleaseTemps` holds one
handed to `push` with the list. The shake keeps a struct's retain with its
release, and `LibFuncs`, which runs after `OwnedState`, promotes a library's
-- gtk4's `refPicture` -- as it promotes any library call the program's
lowered body makes (`testdata/owned_retain.sngl`,
`testdata/gtk4_picture_retained.txtar`,
`cmd/sngl/testdata/simulate_gtk4_picture_retained.txt`). What the retain
does not reach: a composed type -- `list[T]` for a `T` with a retain --
copied var to var is still released by each, as before; and a local's
reassignment, which releases nothing today, retains nothing either.

gtk4's `Picture` is the first use: `gtk4rt.Picture.refs` counts its holders,
`RefPicture` adds one and `UnrefPicture` gives the decode back at the last,
so a program keeping the picture a source derived -- the last one shown while
the URL moves on -- holds a reference, not a second decode. A `gir:` class
whose chain reaches `GObject.Object` would be declared
`#[owned(g_object_unref, retain=g_object_ref)]` by `declgen` if it declared a
struct for one; it declares none today (every GIR class is a widget
component, and a GObject-typed property or action parameter is left out as
having no SNGL spelling), so the rule waits on the first struct a `gir:`
import declares.

**A type that holds an owned value is owned by what it holds** (`ir.Owned`):
a struct with an owned field, a `list`, a `map` or an `option` of an owned
type, through any depth. Its release is synthesized by `Package.ReleaseFor`
on the program's package, once per type by `releaseKey` (the declaration
of the owned struct, not its name, so two packages' `file` are two), named
`__release_<type>` and registered in `ir.Package.Releases`; `ir.ReleaseOf`
stays the declared release, and `Func.Releases` is what marks the synthesized
one for `ir.IsRelease`. The body is ordinary IR every target already emits:
a list walks `list.reverse()` of itself, so the last pushed is released
first; a map walks its values; an option tests `!= null` and converts; a
struct releases its owned fields last declared first, calling `ReleaseFor`
of each field's type, so `list[option[file]]` reaches
`__release_option_file` through `__release_list_option_file`. On the
interpreter the body runs as any function does, and the element's release
marks each handle, which every copy of a list shares. A synthesized release
whose body reaches a library's -- gtk4's `unrefPicture` -- is promoted the
way a library release is (`callsLibrary`). Every pass that asked
`ReleaseOf` of a var's or a temp's type asks `Owned` and `ReleaseFor` now
(`testdata/owned_containers.sngl`, `testdata/owned_containers.txtar`).

**A collection write moves the value in** (`OwnedState.overwrites`). An
assignment to a list element, a map entry or a field of a struct is
rewritten the way one to an owned var is: the new value bound to a
`__newN`, the old to an `__oldN`, the write, then the release. The index or
key is bound to an `__idxN` first, since the old value's read and the write
both name it. A map entry that may be absent is read into an
`option` cell under `map.contains`, and released where it was held, which
is `releaseIfHeld`'s shape; `remove` reads the element the same way under a
bounds test, since an index outside the list removes nothing, and releases
it after the call; `clear` calls the list's own release before it. A `__retN`
cell is an option of an owned type and so owned by what it holds, but
`retain` writes its own releases, so `collect` leaves the cells out
(`cells`). The rewrite walks every root whether or not a var is owned, and
a `remove` or `clear` once rewritten is remembered (`taken`), since
`RewriteBlocks` reaches a lambda's block twice.

**A literal of a composed type allocates only where something in it does**
(`isAllocation`): `[open(1)]` makes a value nothing else holds, and
`[f]` or the `SourceEvent{picture=…}` the draw lowering builds from a
source's prop holds what a var holds, and released as a temp would release
the var's value under it. A literal of constants of a composed type --
`[fixed(1), fixed(2)]` -- is an application-lifetime value like the struct
literal it holds (`constantValue`). An owned value converted to an option, or
read out of one, passes through the conversion rather than being read by it
(`tempCollector`).

**A native says what it does with an argument.** `#[retains]` and `#[moves]`
set `ir.Param.Transfer` on a parameter of a bodyless function, the one place
nothing else could say it. A declared function's parameter list now takes
marks the way a component's does (`funcParamMarks` beside `paramMarks`), so
an unknown name there reports as an unknown macro (`testdata/param_mark.sngl`);
a lambda's or handler's still refuses every mark.

**GIR says `#[moves]`; nothing says `#[retains]`.** A `gir:` action's
parameter GIR marks `transfer-ownership="full"` is declared `#[macro.moves]`
(`gir.ActionParam.Moves`), so the C string CMarshal makes for it is handed
over rather than freed (`testdata/gir_transfer_full.txtar`, whose GIR says
so of a method that in GTK copies: GTK 4's one transfer-full string,
`gtk_string_list_take`, is on a model no view renders). GIR has no
annotation for a borrowed pointer the callee keeps, so `#[retains]` is
written by hand, and gtk4 has none to write: what it declares --
`sngl_calendar_select`, the cairo functions in gtk4.sngl, the `gir:` actions
-- takes scalars, cairo's enums, the `cairo_t*` GTK lends a draw callback,
and strings cairo and GTK copy (`cairo_select_font_face` builds a toy face
that `strdup`s the family; `cairo_show_text` keeps nothing). The calls that
keep a pointer -- a signal's, an idle's, a timeout's, a draw func's or a weak
ref's user data, `g_object_set_data` -- are in gtk4rt and the inline-cgo
scaffold, outside the IR, and are handed `GINT_TO_POINTER(idx)` or `NULL`,
nothing allocated. `lib/retains_test.go` pins it: no function a library
package or what it imports declares has a `#[retains]` parameter, so the
first one written is noticed as the decision it is. The scaffold's `gtkPost`
hands `g_idle_add` a Go pointer, `&f`, which GLib keeps past the call as cgo
forbids; nothing calls it, and gtk4rt's `Post` passes an index instead.

**An owned value has one of four lifetimes**, the narrowest that fits:

| Lifetime        | When                                                                                                               | Released                             |
|-----------------|--------------------------------------------------------------------------------------------------------------------|--------------------------------------|
| **Statement**   | passed to a parameter that copies or borrows (GLib's transfer-none)                                                | after the statement                  |
| **Node**        | held in component state, bound by an owned derived value, or handed to a `#[retains]` parameter inside a component | on overwrite, on key change, unmount |
| **Application** | built from constants                                                                                               | once, at application teardown        |
| **Moved**       | handed to a `#[moves]` parameter (transfer-full)                                                                   | never; the host owns it              |

**Node lifetime lowers onto `effect`** (`internal/lower/owned_state.go`). A
branch leaving, a list row removed, a window closing are already `@unmount`,
so `OwnedState` writes ordinary `effect`s and every target's `Effect` lowering
brackets them. Four things become node lifetime:

- **An owned `var` of a component** is released when it is overwritten and
  when the node unmounts. Each assignment to it, in the component or through
  an instance (a test's `c.f = …`), binds the new value to a `__newN`, then
  the old to an `__oldN`, assigns, and releases the old one: the new value may
  be computed from the old, so the old is read after it is made; and it is
  read beside the write, because AsyncOffload splits an assignment whose
  value waits at the wait, and an old value bound ahead of the goroutine was
  the one before the wait, which two clicks in flight released twice
  (`testdata/owned_async_overwrite.txtar`). One effect written ahead of everything else in the body releases
  every owned var at `@unmount`, newest first: the first lifetime to begin is
  the last to end, so every other `@unmount` may still read them. The
  compiler owns the release, so a program releasing one by hand as well
  releases it twice (`testdata/error_owned_tracker.sngl`). A view block's
  owned `var` is the block's state on the interpreter, released by an effect
  written after it; a target has already hoisted it into a component
  (passHoistState).
- **An owned derived value** -- a component's parameterless
  `func label => …` -- becomes a var of the same name, initialized as the
  value is computed, and an effect keyed on what it reads whose `@update`
  computes it again. The var is then an owned var, so the overwrite releases
  the old value and the unmount the last. Every call of the function becomes
  a read of the var; one with a block body stays as an `__own_` helper the
  var calls. What it reads is its props and vars, the package's vars, and
  what the component's own functions it calls read, keyed as the one read or
  an anonymous struct of them. A bracket's `@mount` was the design and is
  wrong twice: a mount runs after the render that first reads the value, and
  a rekey's `@unmount` before the render that precedes the new `@mount`, so a
  bracket shows a render a value not yet made or already released. `@update`
  runs after the render that made the write, which reads the old value, still
  live. It reports a write where a bracket compares, so writing a var the
  value it holds remakes the value; a prop reports only a new value. A
  write before the instance's lifetime began is not reported, so the value
  is made once when its branch comes back (design/instances.md). Cells
  follow the component's vars, ordered so one derived value reading another
  follows it (`testdata/owned_state.sngl`, `testdata/owned_state.txtar`).
  The var goes before the first var whose initializer reads it and after
  every var it reads itself (`cellIndex`); appended last, as it first was, a
  var reading it read the cell's zero value, which the interpreter allocates
  a handle for and nothing released (`reads` in `owned_state.sngl`). A
  reader declared before one of the cell's own reads is a cycle, and the cell
  goes last.
- **An allocation a component hands a `#[retains]` parameter** is stored in
  an `option` cell of the component (`__retN`) and passed from it; the next
  call at the same site replaces it, releasing the old one after the native
  has the new, and the lifetime that made the call releases the last. For a
  bracketing effect's handler that is the effect's `@unmount`. An effect that
  only reports writes brackets nothing, and an `@unmount` would make it a
  bracket ended by every change of its key, releasing what the native still
  holds, so its cells are released by the component's unmount effect instead
  (`cmd/sngl/testdata/simulate_cnative_owned.txt`); so are the cells of every
  other call the component encloses -- an event handler's, a timer's
  `@tick`'s, a component function's, which `retain` reaches by rewriting
  every block of the component after the effects
  (`testdata/owned_retains_node.sngl`). `retainedIn` rewrites one statement
  and skips the statements it encloses, which are their own block's: found
  from the enclosing `if`, a handler's call had its cell written around the
  `if`. A program should not have to know where a native may be called from,
  so a call no component encloses -- a free function, a test, a package
  var's initializer, the package body -- is refused by the checker
  (`checkRetainsOutsideNode`, `testdata/error_owned_retains_outside_node.sngl`)
  where the argument allocates; one handing over a value another holder owns
  is left to that holder's lifetime. A `#[moves]` argument is left where it
  is.
- **What a var's initializer borrows** -- `open(fd)` in
  `var n = size(open(fd))` -- becomes a var of its own declared ahead of the
  var (`__initN`, `initTemps`), and so an owned var of the component,
  released when the node unmounts; a package var's the same, released at
  teardown by `ReleaseTemps`' `packageVars`. An initializer has no statement
  around it to release a temp after, which is why no pass reached one and
  the temp was held (`testdata/owned_init_temps.sngl`,
  `testdata/owned_init_temps.txtar`). The statement-exact shape -- a method
  of the component computing the initializer, whose `return` ReleaseTemps
  would bind and release around, as it does for `derive`'s `__own_` helper
  -- is the one to move to once two backends emit it: Go drops a synthesized
  method's result type, and Kotlin declares a component's functions after
  its vars, where no initializer can reach one, which a hand-written
  `var n = helper()` on android trips today. A view block's var keeps its
  temp held: on a target passHoistState has made it a component's var by
  the time the pass runs, and the interpreter's block state is reached by no
  pass.

The pass runs before `NoComputed`, which would inline a derived value at
every read and so allocate it per read; before the inliners, which give each
instance its own copy of the var; and before `AnonStructs`, which declares
the key's struct. The optimizer, which runs first, inlines a pure function
wherever it is called, and a library override's derived value over a native
-- gtk4's `picture`, `loadPicture` having no body to say it allocates -- is
pure by inference, so `optimize.inline` leaves an owned derived value
standing (`ir.IsOwnedDerived`). The pass then reaches it: on a target it
rewrote only the program's own components, where the interpreter's
`OwnedState` walked the imports for every unfrozen component, and a build's
copy of a reached library component is as much its own, so both walk now
(`testdata/gtk4_image_pictures.txtar`). The checker lets a const render read
an owned derived value, as it reads a prop: declaring it `const func` instead
had the optimizer inline it. Its `effect` is a copy of sngl:builtin's, since a program
that writes none does not reach the declaration and the build shares the
library's own, which `InlineComponents` would otherwise flag a runtime
instance.

**What can be a key is `ir.Keyable`**, which is `ir.RebuildIncomparable`'s
answer: the rule an effect's `on` is already held to, since the pass makes
those reads an `on`. `checkOwnedDerived` refuses an owned derived value that
reads a variable or parameter whose type is not keyable -- a function value, a
list -- where the read is written
(`testdata/error_owned_let_unkeyable.sngl`), rather than letting it recompute
and release on every render. One that waits for an async call is refused
already, owned or not ("async expression not allowed in parameterized reactive
context"). A `var` is exempt: its initializer runs once and keys nothing. The
limitation is meant to be lifted -- by function identity, by keying on the
settled async value, or by a write-counter key -- and widening the predicate is
how, so the pass must never grow its own idea of keyable.

**Two passes.**

```
… → OwnedState   (node half; before NoComputed, while a derived value is a function)
  → NoComputed → … → Effect
  → … → MatchLate
  → ConditionalTemps (an arm that allocates becomes a statement) → NoTernary
  → … → ErrorCatch
  → ReleaseTemps (statement half; after ErrorCatch so a raise path is a catch block)
  → MutatedVars → CSE → … → ReservedNames
```

**`ReleaseTemps` releases what a statement borrows**
(`internal/lower/release_temps.go`). A temp is an allocation -- a call
answering an owned type, or a literal of one -- written where its value is only
read: an argument to a parameter that borrows, a receiver, the operand of a
field read or an index, a call whose result is discarded. One stored, returned,
handed to its release or to a `#[moves]` or `#[retains]` parameter has another
lifetime and is left alone. An intrinsic's call allocates nothing: a cell's
`map.get` reads what a copy holds, and taken for a temp it released the value
the write it was read for had just stored. Nor does an intrinsic's parameter
borrow: `push` stores what it is handed, and released after the statement the
list held a freed value (`pushes` in `testdata/error_owned_tracker.sngl`), so
an allocation handed to one is held with the list, as the spec says of a
value in a collection. The zero literal the effect lowering hands a copy's
`getOr` as its default was an application-lifetime `__relN` by the same
misreading, and stands inline now (`testdata/canvas_for_shape_state.txtar`). Each is bound to a `__relN` before the statement,
an allocation's own arguments first, and released after it with an explicit
statement, never Go's `defer` -- that is function-scoped, wrong in a loop in a
draw callback, and C has none (`testdata/owned_canvas_text.txtar` denies one).
A `return` binds its result to a `__retN`, releases, then returns; an `if` or
`match` tests a bound head, so a branch that returns skips nothing.

The reach is CSE's (`imperativeBlocks`), statement-local and never hoisting out
of a lambda, plus each canvas's children (`drawBlocks`): `ShapeDraw` made those
the statements that paint it, a block a host runs per frame that no view walk
treats as one, and #110's leak is in it. A per-call `@error` body is among
them now: hanging off a call rather than a node, no view descent found it, so
no pass lowered it at all -- the optimizer folds one with the call it belongs
to (`foldExpr`), and the Go printer, handed a call of `size` the inliner never
saw, spelled it as the `len` builtin by name, a hack now gone
(`testdata/owned_error_handler_temps.txtar`). Each block is walked with
`ir.RewriteBlocks`, which hands the pass every list it holds innermost first,
so a lambda written in a block is reached twice, once as its own block: a
temp already bound is an identifier, and `released` remembers an argument
already given a release. On the interpreter, `ReleaseTemps` reaches every
body the program does, library ones included, the way `CMarshal` does, since
nothing there is spliced.

**What a statement owns is enumerated by expression kind** (`tempCollector`),
every kind `ir.Rewrite` walks, and an unknown one panics as that walk does:
each kind says what its operands are to it -- read, so a temp; stored in a
literal, so held; or not evaluated here, a ternary's arms. A kind left out
was a temp nothing released: a conversion, which is what an interpolated
string is made of, and a map literal's values
(`testReleasedUnderAConversion`, `testReleasedInAnInterpolation` in
`testdata/owned_release_temps.sngl`). A call through a function value names
no declaration, and `borrows` takes its parameters for plain borrows: only a
bodiless native can say otherwise, and a mark is a declaration's
(`testReleasedWhenALambdaBorrows`). The `__argN` temps ErrorCatch bound
ahead of a call with a per-call handler are known by that shape, a run of
synthesized locals the call after them reads (`boundArgs`), not by being
synthesized locals holding an allocation: HandleParams binds one the inlined
body's statements read, and taken for ErrorCatch's it was released after the
first of them (`testdata/owned_handle_param.txtar`). HandleParams releases
its own, after the body, where the call it inlined would have ended.

**A raise releases too.** A statement that may raise while a temp is live runs
in a catch block of its own (`ir.If.Catch`, nested inside ErrorCatch's), whose
handler releases and raises the event again. A raise resolved to a boundary's
or window's handler is turned into one that bubbles, so the nested catch sees
it, and the raise again is resolved as it was: on Go the outer catch then takes
the panic, and on the interpreter, which runs a resolved handler at the call,
the handler runs from there and ends the event handler as before. Running
ErrorCatch itself on the interpreter was tried and is wrong:
`cmd/sngl/testdata/error_raise_render_tree_runs.txt` has an emitted handler a
compiled target inlines into its emitter, and a catch block of its own there
ended the emitted handler instead of both. `__argN` temps ErrorCatch bound for
a per-call `@error` are released after the call they were bound for.

A temp bound before a later temp of the same statement raises while being
bound is released the same way: the later binding is declared ahead and
assigned inside a `catchReleasing` of its own over the temps bound so far
(`releaseTemps.bind`), so each binding that may raise has its own catch and
the body's catch releases them all. A single catch over the bindings and the
body would have released a temp not yet bound
(`testReleasedWhenALaterTempRaises` in `testdata/owned_conditional_temps.sngl`).

**A value the statement may not make is made by a statement of its own**
(`passConditionalTemps`, `internal/lower/conditional_temps.go`), before
`ReleaseTemps` and on every target, so the pass sees it: a ternary's or a
`match` expression's arm that allocates becomes an assignment to a local
(`__armN`) under an `if` or a `match` statement ahead of the statement, the
right of `&&` or `||` likewise under a test of the left, and a condition
`for`'s head, evaluated per iteration, a function literal called in place
whose body returns it, so that what it makes is a `return`'s temp. The shape
is `NoTernary`'s (`FromTernary`, `FromValue`), written here only for the arms
that allocate (`allocates`, any `isAllocation` under the arm outside a
lambda), so a target with ternaries keeps the rest: the lifted arm is a
statement in the branch's block, which `ReleaseTemps` reaches as any nested
list. The kinds of statement it rewrites are `ReleaseTemps`'s, and its reach
is the same -- `imperativeBlocks` and `drawBlocks` on a target,
`reachedBlocks` on the interpreter, which runs it from `PrepareFor`. An
iterable `for` head is evaluated once and read by every iteration, so it is
`ReleaseTemps`'s own case: its temps are bound before the loop and released
after it, and ahead of each `return` in the body (`releaseBeforeReturns`,
through `ir.TransparentBlocks`, so a lambda's `return` is not one). A raise
out of the loop body wraps the loop in a `catchReleasing`, which nests one
the body's own statements wrote: `raisingCalls` enters a nested catch's
handler, which the walk does not, so the inner raise again is turned to
bubble and the outer catch sees it. Kotlin calls a lambda literal where it
stands in parentheses, `(fn@{ … })()`, since `fn@{ … }()` labels the call and
refuses the `return@fn` inside (`testdata/owned_conditional_temps.txtar`).

**An application-lifetime value is a package-level declaration.** An
allocation built from constants -- a literal of an owned type whose fields are
literals or package consts, or a call of a `const func` or of `c.cstring` with
such arguments -- is declared once as a package `var` (`__relN`, shared by every
writing of the same value: `applicationKey`, which is CSE's `exprKey` plus a
struct literal, which that key leaves out since two writings of one are two
values, `testdata/owned_application_shared.txtar`), and its release is appended to
`ir.Package.Teardown`, which the effect lowering already makes the page's exit
handler and the pass creates where no `@unmount` did. Every Go target then sets
it in `New` and frees it in `__snglTeardown`. A const parameter is not constant
here, although `ir.IsConst` says so: it is constant at each call site, not at
package level. The interpreter's test teardown runs `Teardown` after the timers
stop and before the leak check.

A package `var` of an owned type the program declares is released the same
way, last of all (`packageVars`, `testdata/owned_package_var.sngl`,
`testdata/application_teardown.txtar`): its initializer ran before any
`__relN` was made, so its release follows theirs, and a package it imports
declares one the same way, released after the program's own
(`testdata/owned_imported_var.txtar`). `OwnedState` collects both as it
collects a component's vars, so an assignment to one anywhere releases the
old value, but it cannot release the last one, since the effect lowering
writes `Teardown` afresh between the two passes. The interpreter binds an
imported package's vars in the root scope before the program's own
(`seedPackageState`): unbound, a read re-evaluated the initializer, allocating
a value nothing released, and an assignment was refused ("cannot assign to
undefined variable"), which kept the golden without a run
(`cmd/sngl/testdata/owned_imported_var_test.txt`). By the time `ReleaseTemps`
runs, an inlined instance's var is a package var too, released by its node's
`@unmount`; a var the package already releases somewhere is left to that
(`testdata/owned_state.txtar`'s bubbletea output released `f__inst0` twice
before the check).

**A release must be emitted.** A program's release is otherwise called by
nothing until lowering, so the shake keeps an owned struct's release with the
struct; a user package's is taken with the function that made the value
(`reachableForeignFuncs`, `testdata/owned_derived_in_imported_component.txtar`,
whose `close` was otherwise emitted against Go's builtin); and a library one
-- `c.free` -- is promoted by running `LibFuncs` again once the pass has
called it. A release written `{}`, the spec's own example, is emitted too:
every emitter took an empty block for a native with nothing to emit, since an
empty function the program wrote is inlined away with its calls, and a
release is called after the inliner ran. `codegen.HasBody` is the test, on
Go, Kotlin and JavaScript alike (`testdata/owned_release_empty_body.txtar`).
`c.free` calls cgo's `C.free`, which `<stdlib.h>`
declares: gtk4's preamble includes it, and fyne's and bubbletea's
(`EmitCHeader`) include it beside the program's links, since every `c.str`
those targets make is freed.

**A call the gtk4 translator builds releases its own strings.** The
inline-cgo translator writes `C.CString` into a setter, a `sngl_connect`, a
`g_object_set_property` helper, a constructor's non-nullable zero, a flow's
markup, the window title and the entry-text write after lowering, so no pass
sees the allocation. `emitShared.ownCStrings` (`intrinsic_translator.go`) is
run over the statements each of those sites returns and writes the release
`ReleaseTemps` would have: a literal's copy becomes a package-level `var`
(`__cstrN`, one per distinct text, `scaffoldData.CStrings`) kept for the
application's lifetime and never freed, and any other is bound to a `__ctmpN`
before the statement that hands it to C and freed with `C.free` after it.
Every C function the translator calls copies what it is handed, so no string
outlives its statement. The activate signal name in `callbacks.go` is a package
`var` for the same reason, and the event invokers already freed theirs
(`testdata/gtk4_translator_strings.txtar`, whose `deny` lines refuse a
`C.CString` inside another call or on a line of its own). Resolving these
calls in lowering would retire this, since `CMarshal` and `ReleaseTemps` would
see them; that is a re-platforming of gtk4's inline-cgo mode -- the widget
protocol declared as native-calling bodies rather than answered by the
translator -- and is #293, split out of the ownership work with the count of
what it touches.

**Nothing it reaches is held.** The review of the ownership work left a list
of values the compiler held, and the spec's *Owned values* carried it so a
program knew to release by hand. A value in a collection or a struct was on
it until `ReleaseOf` composed; a conditional arm and a temp bound before a
later one raises until `ConditionalTemps` and `releaseTemps.bind`; an
allocation handed to a `#[retains]` parameter outside an effect's handler
until `retain` reached every block a component encloses, all above. What is
left is what no lifetime can hold, and the checker refuses it: a `#[retains]`
call no component encloses. Two shapes release twice rather than hold, and
the spec rules them out: a temp passed to a function returning the
same owned type that returns its argument, since the pass releases the
argument and the result alike; and a component's or package's var assigned
another var's value, which both then release. A type with a retain is the
exception to the second: the store is a reference, and each holder releases
its own.

**The application ends before the process does.** `@run` hands the program a
`run` that presents the windows and runs the loop, and code after `run()`
keeps going (`testdata/platform_run_handler.txtar` prints "after"), so a CLI
can wrap the GUI and outlive it. When the loop exits, the application tears
down before `run` returns: the root tree unmounts (every `@unmount` fires,
which releases every node-lifetime value), timers stop, and the
application-lifetime values are released. Every entry point a host calls to
start the application has a teardown paired with it.

The teardown is `__snglTeardown` (`lower.TeardownFunc`, `ir.Package.Teardown`):
the effect lowering writes it to end every lifetime newest first -- which is
every `@unmount` and every timer -- and `ReleaseTemps` appends the
application-lifetime releases, so one call is the test teardown's order. The
reactivity lowering then puts the page's own registries ahead of all of it
(`destroyPageHeld`, `slot_instances.go`): every instance the page holds --
under a window's `if` or `for`, or a spliced component's -- is destroyed
newest first, which runs its own teardown, as `destroyHeld` already did for
the registries an instance holds. Before that the handler existed only where
a page effect had a teardown, and emptied no registry: a page whose every
lifetime sat inside an instance it holds had no handler at all, so the
pokedex's pages, and the pictures they held, were held until the process
ended (`testdata/teardown_page_registries.txtar`, found by the ASan check
below). The entry points, and where each calls it:

- **gtk4**, `main` of a program whose root is the application
  (`emitGTK4AppMain`): after `gtk4rt.RunWindows` returns, inside `run` where
  the program wrote `@run` and after the hidden loop where the handler never
  called it.
- **gtk4**, `main` of a build rooted at a component (the `rootComponent`
  option): `gtk4rt.Run`/`RunHidden` with `@run` (`emitGTK4RunMainWrapped`),
  the same way; `gtk4rt.Run` without (`emitGTK4MainWrapped`), whose Model is
  made on activate, so the teardown asks whether one was. Neither tore down
  before.
- **gtk4**, inline-cgo `main`: after `g_application_run`, through the package
  `snglModel`. It refuses `@run`, and has no `gtk4rt` to quit on a signal.
- **fyne**, `main` rooted at the application (`emitIRAppMain`) or a component
  (`emitIRRunMain` with `@run`, `emitIRMain` without): after
  `fynelayout.RunWindows`, `ShowAndRun` or `a.Run` returns, inside `run` as on
  gtk4.
- **bubbletea**, `main`: after `p.Run()` returns, on the final model, since
  bubbletea passes the model by value. It has no `@run`; ctrl+c is
  `tea.Quit`.
- **`BuildUI`** (gtk4's and fyne's), and bubbletea's `New()`: what a library
  build (`main` false), a snapshot and a compiled test start. The host owns
  the loop there, and pairing a teardown with it is the host calling
  `__snglTeardown` when its loop exits, which a host in the generated
  package can. A compiled test build calls it nowhere; the interpreter's
  test teardown, below, is the one that leak checks.
- **html**: the page. `pagehide` runs the page's `__snglTeardown`; a crashed
  or discarded tab runs nothing, and nothing more is owed, since the page is
  the application and its host ends both.
- **android**: the activity. MainScreen's composition is disposed when the
  activity is destroyed, which disposes every `DisposableEffect` -- each
  `@unmount` -- and cancels each timer's `LaunchedEffect`. `Teardown` holds
  only the application-lifetime releases there, since android lowers effects
  itself, and a `DisposableEffect` remembered ahead of the rest of MainScreen
  runs it on dispose, after the rest, as Compose forgets in reverse. Before,
  nothing called it, and an application-lifetime value was never released
  (`testdata/application_teardown.txtar`). A recreated activity is a new
  application; nothing more is owed, since a process outliving its activity
  holds nothing the activity made.

A loop exits when the last window closes or when the process is asked to
stop: `gtk4rt` and `fynelayout` end it on SIGINT and SIGTERM, as bubbletea's
ctrl+c does, so a terminal's ctrl+c or i3 restarting a bar tears down rather
than killing the process mid-loop. fyne's driver quits on both already, but
registers from a goroutine `Run` starts and missed a signal sent during
startup; `RunWindows` registers its own first. That is also how a fixture
ends a loop the headless compositor cannot close a window of
(`testdata/platform_run_handler_runs.txtar`: the tick sends SIGTERM, and on
both targets the probe's `@unmount` and the token's release print before
"after").

**A second `run` is refused.** The tree `run` presents is built once, before
the handler, so that the handler's writes update widgets that exist; the first
`run` tears it down. A second application would need a second tree whose state
none of the handler's writes reached, fyne's `App.Run` does not run twice, and
the handler is the process start, so there is one application per process.
The `run` each host hands over refuses a call after the first, and one after
the hidden loop started, with `RefuseRunAgain` naming the handler's position
(`ir.Package.RunPos`) on stderr and exiting 2
(`testdata/platform_run_handler_twice_runs.txtar`). A run-time refusal,
because `run` is a value the handler can pass anywhere.

## The interpreter tracks every owned value

The interpreter runs the passes rather than a second implementation of them,
and checks what they did: `interp.Prepare` runs `OwnedState` and
`ReleaseTemps` as it already runs `DataFamilies` and `RefParams`. It needs no
`Effect`, since it runs `effect` natively.

**An owned value is a `*Struct` carrying a `*Handle`** (`internal/interp/owned.go`):
an id, its type and the position that allocated it. `evalStructLit` allocates
one for a literal of an owned type, and every copy -- `CopyValue`, `copyOf`,
`DeepCopy`, `Clone` -- carries the same pointer, so releasing a copy releases
the original, as it would a pointer on a target. The program's handles are
shared by every scope the way its write log is. A handle counts its holders:
one from the allocation, one more per call of the type's retain
(`retainCall`, and the stub answers the argument itself), and the declared
release gives the value back only when the last holder's runs. Only the
declared release counts: a synthesized one -- `__release_option_file` --
calls the held value's own, and on the interpreter an option of an owned
value is that value, so counting it too released the value twice. A handle
some holders released and the rest did not is a leak of its own wording at
teardown (`has 1 of its 2 holders left`).

**Releasing twice, and reading or passing after a release, are errors where
they happen**, naming where the value was allocated, where it was released and
where it was used. `evalUserFuncCore` is where every bodied call binds its
arguments: a release (`ir.IsRelease`) runs its body and then marks the handle,
so the body may read what it is giving back; any other call refuses a released
argument, as `evalCallArgs` does for a lambda or an intrinsic. `evalCall` keeps
the call being made on a stack, which is where a release or a use is reported,
at the callee rather than the call's parenthesis. `evalSelect` refuses a field
read of a released value. Only owned values carry a handle, so the tracker is
always on.

**A test is an application.** When a test function returns, `testingT.teardown`
ends every running lifetime (`Effects.Retarget(nil, …)`, newest first, so
every `@unmount` runs), drops the timers, runs `ir.Package.Teardown` (the
application-lifetime releases), and reports each handle still live as a leak
naming where it was allocated. Before this a test ended with nothing
torn down: no `@unmount` ran at the end of any test. A test that already failed
is not leak checked, since it stopped wherever it failed. There is no exemption
list: application-lifetime values are released at teardown too.

A fixture declares an owned type whose release is SNGL -- a function that
appends to a log -- and asserts behavior with the log, while the tracker
asserts nothing leaked (`testdata/owned_release_by_hand.sngl`,
`testdata/error_owned_tracker.sngl`, and `testdata/owned_release_temps.sngl`,
where nothing releases by hand). Confirming a pass's fixture fails with
the change reverted is removing the pass and watching the leak error, which is
why `TestRunFixtures` now fails an `ERROR(test)` directive no failure matched:
it used to check only that each failure had a directive, so a tracker that
stopped reporting passed, and `test_error_runtime.sngl` had carried a directive
for a message the interpreter no longer gave.

**Simulated natives are the debugging mode.** `sngl test --simulate <target>`
runs a program on the interpreter specialized for a target rather than `none`
(`interp.PrepareFor`, platform axis only: the interpreter is the language), so
that target's overrides run. A `#[cnative]` call is stubbed always
(`callCNative`), since the interpreter has nothing else to run: its `Block` is
the checker's `return <zero>`, so the stub keys on `Foreign.Path == "C"`, not
on an empty body. A `#[go.native]` is stubbed the same way, which is how
gtk4's `loadPicture` allocates a handle the tracker follows
(`cmd/sngl/testdata/simulate_gtk4_image_pictures.txt`). An owned result is a fresh handle allocated at the call,
anything else the zero value; every argument is checked for use after release;
a `#[moves]` argument is the native's from there (a later use or release says
"moved"); a `#[retains]` one may not be released until the lifetime that made
the call ends -- the effect whose handler ran it (`handles.within`, ended by
`letGo` before its `@unmount`); outside one the component instance the call
ran in (`Env.instance`, the scope a handler's or a function's snapshot was
taken from, ended by `letGoInstance` as `evictUnreached` drops the instance,
which is before the unmount effect releasing its cells runs); or the
application outside every component -- or until the same call in the same
lifetime hands the native another, which it holds in its place: an `@update`
keyed on a list is one lifetime however often it runs.
A native's return steering control flow takes one path.

Simulating is what adds the target's half (`BuildSimulatedEnv`, set before the
initializers run), and `PrepareFor` runs `CMarshal`, so a stubbed native is
handed the `c.str` the target allocates: an owned result of the stubbed
`cstring`, which `ReleaseTemps` frees as it does on the target. Before that
pass this was #110, reported with no toolchain at the C call it was converted
for (`handles.site` skips a call the pass wrote, having no position of its
own); `cmd/sngl/testdata/simulate_gtk4_canvas_text.txt` now holds it to none.
The interpreter's own run converts nothing: it has no C. And the host
paints: after every settle, each handler of a mounted primitive in the shape
family -- the target calling in, which is why such a primitive may carry
events in an `#[family.eventless]` family -- runs with a zero payload, all of
them, since a host may call any (`Env.Paint`). A primitive painting an image
source is the exception: the host calls `@url` for a URL and `@enter` and
`@leave` around a drawing, never both, and a payload field named as a prop of
the source's node holds that prop's value as the target binds it
(`lower.drawPayloadWith`) -- the URL, and gtk4's picture, which is the
source's own value and no allocation of the host's (`hostPayload`). With a
zero payload, `canvasImage`'s `@enter` divided by the drawing's size. Leaks from one site are one
line with a count, because a frame repeats them. `sngl run` takes no
`--simulate`: a run on the interpreter (`interprun`) ends with its process
and has no teardown to leak check at. What the tracker cannot see is a call
the gtk4 translator builds after lowering, which releases its own strings
(`ownCStrings`); resolving those calls in lowering (#293) is what brings them
under it (`cmd/sngl/testdata/simulate_cnative_owned.txt` pins the rest).

## What ASan saw on gtk4

A one-off check of the generated code after the gaps closed, run by hand on
2026-10-23 and written up here; not a test, since GTK keeps caches alive on
purpose, so no report is empty and a threshold would be a number nobody can
defend. The tracker above is the check that runs on every commit; this one
asked a different question of a real toolkit: what C memory a gtk4 program
has allocated and dropped when its process ends.

**What ran.** Three programs, generated with `--opt main=true` for gtk4 and
built with `go build -asan` and `CGO_CFLAGS=-fsanitize=address`, so the Go
runtime and the cgo half are both instrumented: `examples/pokedex`, with its
window wrapped in `poke.server` and `artwork` pointed at the recorded answers
`internal/testhttp` serves (the same server `sngl test` runs against), and
the programs of `owned_canvas_text.txtar` and `gtk4_image_pictures.txtar`,
the latter with a 16x16 PNG at each file name it shows. Each ran inside
`cage` with the headless wlroots backend, as the test suite runs gtk4, under
`ASAN_OPTIONS=detect_leaks=1` and `LSAN_OPTIONS=suppressions=tmp/asan/lsan.supp`.
A driver file added to each generated package clicked every GtkButton under
each window's content in turn, four a second, cycled every GtkDropDown every
fifth tick, and after 40 ticks (120 for the pokedex, which navigates between
its pages) raised SIGTERM against the process. Its SIGTERM handler closed
every window the way a user does, collected the Go heap and gave it back to
the OS, and quit the application, so `main` ran `__snglTeardown` before the
leak check: with the windows gone, a widget still allocated is one the
program holds, and with the heap collected, a C pointer only a dead Go object
still held is a leak rather than reachable. The same was run at
`ownership`'s last commit (6ca05a1ef) for the baseline, from a worktree with
that commit's compiler and runtime.

**What LSan can and cannot see here.** Go's `-asan` registers the Go heap
with LeakSanitizer as a root region, and the binary's data is a root as any
program's is, so a C allocation a live Go value points at is reachable and
never reported: a pixbuf in a Model field, a `__cstrN` package var, a
`__relN` the Model holds until teardown. The check therefore sees only what
the program dropped -- a C string never freed, a widget nothing points at --
and cannot see a value held too long by a Go reference, which is exactly what
the held list named and the tracker checks. That is why the baseline and the
branch gave the same report for the two fixtures: what Parts 1 to 4 closed
was Go-held, and the tracker, not ASan, is where their fixtures assert it.

**What each report showed.** Numbers are from the final runs on this branch
with the suppression file off, then on; the baseline column is the same
program at `ownership`'s last commit.

| Program               | Baseline                                        | Branch, before the fixes       | Branch, after                  | Suppressed |
|-----------------------|-------------------------------------------------|--------------------------------|--------------------------------|------------|
| `owned_canvas_text`   | 192 blocks, 39 KB, none the program's           | the same                       | the same                       | 0 blocks   |
| `gtk4_image_pictures` | 189 blocks, 39 KB, none the program's           | the same                       | the same                       | 0 blocks   |
| `examples/pokedex`    | 368 blocks, 585 KB; 790 GtkBox the program made | 368 blocks, 610 KB; 827 GtkBox | 364 blocks, 80 KB; 952 B GTK's | 0 blocks   |

Every block the two fixtures report is fontconfig's: the font sets, patterns
and charsets pango builds the first time a label or a `canvasText` is laid
out, with cairo's toy font face cache behind `cairo_select_font_face` in some
runs. Nothing in either reaches a `C.CString`, a pixbuf or a cairo surface the
generated code made: the `__ctmpN` the translator frees after the statement,
the `__relN` `ReleaseTemps` frees per frame and the picture `UnrefPicture`
gives back are all gone at exit. The pokedex was different, and the same at
both commits: a GtkBox leaked per instance built, several hundred over a run
that navigates, plus the layouts GTK keeps for the dropdown popovers it
mapped.

**Found, and fixed on this branch.** Two defects, neither a Part 1 to 4 hold
and neither the translator's strings:

- *A GtkBox per instance.* Every instance record's constructor made its
  `__root`, the box a slot at the top of the body renders into, whether or
  not the body had such a slot; a body whose top is one node binds that node
  as the Root, so the box was never parented, never sunk and never
  unreferenced -- a leak LSan sees once nothing in Go points at the record.
  The constructor makes it only where a render names it
  (`instance.go`, `testdata/gtk4_instance_root_box.txtar`). Bisected with
  small programs under the same harness: a toggled instance, a `for` of
  cards, a dropdown, a `nav.stack`, tabs and remote pictures were each clean
  until the Go heap was collected before the check: a dead record still held
  the pointer. Collected, a toggled page holding two remote pictures leaked
  three boxes per toggle, one per instance built, and the pokedex its 827.
- *No teardown for a page whose lifetimes are all in its instances.*
  `__snglTeardown` was the effect lowering's, written only where a page
  effect had a teardown, so the pokedex -- every effect of which is inside a
  page instance under its `nav.stack` -- had none, and nothing destroyed its
  instances at exit; `destroyPageHeld` above gives the page one and puts the
  registries first. LSan could not see this one (the pictures those instances
  held are Go-held); the generated `main` showed it, calling no teardown.

**The suppression file** is `tmp/asan/lsan.supp` in the checkout, which git
ignores; its content, so a rerun needs no archaeology:

```
leak:libfontconfig
leak:gdk_popup_layout_new
leak:cairo_toy_font_face_create
```

Each is the toolkit's for the life of the process: fontconfig's caches, the
layout GTK keeps for a popover it has mapped (a GtkDropDown's list), cairo's
toy font faces. `leak:` matches any frame of a block's stack, so a
suppression names a library or a function the generated code never calls
itself. With the file on, all three programs report nothing, and a positive
control -- the driver dropping one `g_strdup` and one `gtk_box_new` on its
first tick -- still reports both (5 blocks, 656 bytes), so the file hides
nothing the check is for.

**What is left to #293** is unchanged: the strings `ownCStrings` writes after
lowering are freed by the translator's own statements and never appeared
here, so the check neither found them nor could it tell whether a future
site forgets one. Resolving those calls in lowering so `CMarshal` and
`ReleaseTemps` see them remains the re-platforming of gtk4's inline-cgo mode,
and nothing in this check changes its size.

**To rerun it**, which a toolkit upgrade or a change to the instance emitter
or to teardown is reason to: generate a program for gtk4 with
`--opt main=true`, add a driver that drives it and ends it through the
application's quit so `main` tears down, build with the two flags above, and
run it under `cage` with the environment `internal/headless.Env` gives. Read
the report for blocks whose stack reaches the program's binary; the rest is
the toolkit's. The driver and the summarizer are not kept, since neither is
a test and both are a page of code the note above describes.

## A C language target

Decided ahead of the target, with no code until one exists to need it. On
every target today a `string` is the host's: Go, Kotlin and JavaScript
collect it, and the only string the program gives back is the `c.str` a C
call makes of one. In C a `string` is an allocation, every concatenation,
interpolation and `fmt` call makes one, and the question is what releases
it. The holder side -- a var's overwrite, a derived value's key change, a
node's unmount, a collection's write, teardown -- is what Parts 1 to 4 built
for every owned type and the C target reuses unchanged. The decision below is
about the statement side only.

**A `string` is owned on the C target, and the program never sees it.** The
C language package declares it: `string` is `#[owned]` there, and the mark's
rule that only a struct may be owned opens to a target's own scalar when the
target arrives, as a package rule and not a spec one. The spec's *Owned
values* does not change: a program names no release for a string, stores
one, passes one and returns one as it does anywhere, and the compiler's four
lifetimes hold every string the program makes. A C string stored twice is
two copies, each released once -- stricter than the spec's "stored twice is
released twice", and what makes `string` a value on C as it is everywhere
else. The same declaration covers a `list` and a `map`, which are allocations
on C too: the rule below is for a type with no destructor, and `string` is
the first of them.

**A type with no destructor names no release, and its bytes are all it is.**
A cairo surface, a pixbuf, a widget has a destructor: a function the host
must run, which frees memory the host allocated and undoes what the
allocation did. A string has none: giving it back is giving its bytes back,
and `free` is the allocator's, not the type's. The C target's `string` is
`#[owned]` with no release function, `ReleaseOf` answers nothing for it, and
a type composed only of such types (`list[string]`, a struct of strings) is
owned by composition as today and has no destructor either. The distinction
is what decides the allocator.

**Statement-lifetime values of a type with no destructor live in a per-turn
arena.** A turn is one pass through the event loop: start-up before the loop
runs, an event handler, a frame's draw, a timer's `@tick`, a network reply's
continuation, the teardown. The target's loop resets the arena when the turn
ends, with one call it writes itself; `ReleaseTemps` writes nothing. A
string a statement makes and only reads -- an interpolation handed to a
label's setter, a concatenation compared, a `fmt` result returned up to a
caller in the same turn -- costs a bump and no free, and a statement of
string arithmetic reads as one line of C rather than a `__relN` binding, a
catch block and a release per subexpression. The alternatives, and why not:

- *Per-statement free*, which is what `ReleaseTemps` does for every owned
  type today, costs a `malloc`/`free` pair per intermediate and makes every
  statement that may raise a catch block, for a type whose release undoes
  nothing. It is right for a destructor and wrong for bytes.
- *Per-frame* alone leaves a turn that is not a frame -- a timer, a reply --
  accumulating until the next draw, and a program that draws nothing for a
  while leaks for a while. A turn is the unit of work the UI thread runs,
  and the arena is empty between turns, so per-turn is the bound.
- *Per-instance*, an arena held until the node unmounts, keeps every rewrite
  of a label until then -- the out-of-scope note's own objection -- and is
  the same mistake as holding state in the arena, below.

A loop inside one turn grows the arena until the turn ends, where today's
lowering releases a condition head's temps before each test. That is
accepted: a turn is short by construction, a loop building a million strings
in a handler is wrong on every target, and an arena mark at a loop body's
end, which is safe once nothing a loop stores stays in the arena, is an
optimization a profile asks for and not part of the design.

**A value with a destructor is released as today.** A statement-lifetime
`c.str` is already gone on C -- there is no conversion to make one -- but a
surface a draw makes and only hands to cairo, a pixbuf decoded and only
measured, keeps `ReleaseTemps`'s `__relN` binding, release after the
statement, catch around a later temp's raise, and release ahead of every
`return`. An arena cannot hold one: its memory is the host's, allocated by
cairo and not by the program, and a reset would run no destructor. The two
rules compose: a struct holding a surface and a string is released by
releasing the surface, since the string's bytes are the arena's or the
holder's, never the struct's own.

**State leaves the arena at the store, and is freed on overwrite either
way.** A value stored into a holder -- a component's or a view block's var,
a package var, a field, an element, a `#[retains]` native's argument, an
owned derived value's cell -- outlives the turn that made it, and the arena
does not. The store copies the value to the heap, deep for a composed type,
and the heap copy is the holder's. `OwnedState`'s effects then do what they
do for every owned type: the overwrite frees the old copy once the new one
is in place, the unmount frees the last, a collection write frees what it
replaces, teardown frees the package vars. Release-on-overwrite is needed
whatever the arena is scoped to, and is the reason a longer-lived arena was
never an answer: a per-turn arena frees a var's string while the var still
holds it, so state cannot be in it at all, and an arena that lives as long
as the state keeps every value the state ever held, so a label rewritten per
keystroke holds every text it showed. The heap copy and the free at
overwrite are the only way a var holds exactly one string. A value handed to
a `#[moves]` parameter is a heap copy the same way, since the callee will
free it: GLib's transfer-full takes a `g_free`-able pointer, and the arena's
original is left to the reset.

**What `ReleaseTemps` emits on C, per lifetime.** The pass runs, since the
destructor types need it, and classifies as it does today; the type decides
the rest.

| Lifetime        | No destructor (`string`, `list`, `map`, composed)                            | Destructor (a native)                    |
|-----------------|------------------------------------------------------------------------------|------------------------------------------|
| **Statement**   | nothing; the arena, reset by the loop at the turn's end                      | `__relN`, release after, catch on raise  |
| **Node**        | heap copy at the store; `OwnedState`'s overwrite and unmount effects free it | as today: overwrite, key change, unmount |
| **Application** | heap copy once at start-up; freed at teardown                                | as today                                 |
| **Moved**       | heap copy handed to the callee; the arena's original is the reset's          | as today: handed over, never released    |

The copy at the store is the one new mechanism: a lowering step on the C
target that wraps a stored value of a no-destructor type in the target's
`__snglHeap(v)`, placed where `OwnedState` binds the `__newN` and where a
collection write or a `#[moves]` argument takes its value. It is a
`#[gen.can]` capability of the C target alone, since no other target has
two allocators.

**`CMarshal` makes no copy where the string is already a C string.** On
today's targets the pass wraps a `string` argument of a C call in `cstring`,
which allocates a NUL-terminated copy the program frees. On the C target the
program's string already is one: the representation keeps its length beside
the bytes and a NUL after them, one byte per string, so handing it to a
`char *` parameter is a pointer to the bytes and no allocation. The target
writes `CMarshal` in `#[gen.can]`, so the pass does not run: `cstring`,
`cint`, `cdouble` and `cbool` are all identities or casts there, and the
reason the conversions live in the IR -- that an allocation must be where a
pass can see it -- holds for none of them. A string holding a NUL byte is
truncated at the C boundary, as `C.CString` truncates it today. A `#[moves]`
`char *` parameter gets the heap copy above; a `#[retains]` one gets the
node's, released when the node hands the native its next value or unmounts.

**What Parts 1 to 4 leave for the target: nothing held.** The held list is
empty: a value in a collection or a struct is released by composition, a
conditional arm's or a later-raising temp's by its own statement, a
`#[retains]` argument by the node around it, a value stored from another
holder by each holder or through the host's retain. A C program therefore
leaks only what no lifetime can hold, which is a `#[retains]` call no
component encloses, and the checker refuses that before any code is written.
What the tracker cannot see on gtk4, the strings its translator writes after
lowering (`ownCStrings`), has no counterpart on a C target, whose widget
protocol would be declared as native-calling bodies from the start; it is
#293's on gtk4 either way.
