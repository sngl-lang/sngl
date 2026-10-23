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
nothing allocated. The scaffold's `gtkPost` hands `g_idle_add` a Go pointer,
`&f`, which GLib keeps past the call as cgo forbids; nothing calls it, and
gtk4rt's `Post` passes an index instead.

**An owned value has one of four lifetimes**, the narrowest that fits:

| Lifetime        | When                                                                                            | Released                             |
|-----------------|-------------------------------------------------------------------------------------------------|--------------------------------------|
| **Statement**   | passed to a parameter that copies or borrows (GLib's transfer-none)                             | after the statement                  |
| **Node**        | held in component state, bound by an owned derived value, or handed to a `#[retains]` parameter | on overwrite, on key change, unmount |
| **Application** | built from constants                                                                            | once, at application teardown        |
| **Moved**       | handed to a `#[moves]` parameter (transfer-full)                                                | never; the host owns it              |

**Node lifetime lowers onto `effect`** (`internal/lower/owned_state.go`). A
branch leaving, a list row removed, a window closing are already `@unmount`,
so `OwnedState` writes ordinary `effect`s and every target's `Effect` lowering
brackets them. Three things become node lifetime:

- **An owned `var` of a component** is released when it is overwritten and
  when the node unmounts. Each assignment to it, in the component or through
  an instance (a test's `c.f = …`), binds the old value to an `__oldN`,
  assigns, then releases the old one, since the new value may be computed
  from it. One effect written ahead of everything else in the body releases
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
- **An allocation an effect's handler hands a `#[retains]` parameter** is
  stored in an `option` cell of the component (`__retN`) and passed from it;
  the next call replaces it, releasing the old one after the native has the
  new, and the effect's `@unmount` releases the last. An effect that only
  reports writes brackets nothing, and an `@unmount` would make it a bracket
  ended by every change of its key, releasing what the native still holds, so
  its cells are released by the component's unmount effect instead
  (`cmd/sngl/testdata/simulate_cnative_owned.txt`). A `#[moves]` argument is
  left where it is.

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
the write it was read for had just stored. Each is bound to a `__relN` before the statement,
an allocation's own arguments first, and released after it with an explicit
statement, never Go's `defer` -- that is function-scoped, wrong in a loop in a
draw callback, and C has none (`testdata/owned_canvas_text.txtar` denies one).
A `return` binds its result to a `__retN`, releases, then returns; an `if` or
`match` tests a bound head, so a branch that returns skips nothing.

The reach is CSE's (`imperativeBlocks`), statement-local and never hoisting out
of a lambda, plus each canvas's children (`drawBlocks`): `ShapeDraw` made those
the statements that paint it, a block a host runs per frame that no view walk
treats as one, and #110's leak is in it. On the interpreter, `ReleaseTemps`
reaches every body the program does, library ones included, the way
`CMarshal` does, since nothing there is spliced.

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

**An application-lifetime value is a package-level declaration.** An
allocation built from constants -- a literal of an owned type whose fields are
literals or package consts, or a call of a `const func` or of `c.cstring` with
such arguments -- is declared once as a package `var` (`__relN`, shared by every
writing of the same value), and its release is appended to
`ir.Package.Teardown`, which the effect lowering already makes the page's exit
handler and the pass creates where no `@unmount` did. Every Go target then sets
it in `New` and frees it in `__snglTeardown`. A const parameter is not constant
here, although `ir.IsConst` says so: it is constant at each call site, not at
package level. The interpreter's test teardown runs `Teardown` after the timers
stop and before the leak check.

**A release must be emitted.** A program's release is otherwise called by
nothing until lowering, so the shake keeps an owned struct's release with the
struct; a user package's is taken with the function that made the value
(`reachableForeignFuncs`, `testdata/owned_derived_in_imported_component.txtar`,
whose `close` was otherwise emitted against Go's builtin); and a library one
-- `c.free` -- is promoted by running `LibFuncs` again once the pass has
called it. `c.free` calls cgo's `C.free`, which `<stdlib.h>`
declares: gtk4's preamble includes it, and fyne's and bubbletea's
(`EmitCHeader`) include it beside the program's links, since every `c.str`
those targets make is freed.

**What it does not release yet**, each left held rather than released wrongly:
an allocation the statement may not evaluate -- the right of `&&` or `||`, a
ternary's arm (lowered to statements before the pass on a target without
ternaries) -- or in a `for` head, which may be evaluated per iteration; a temp
bound before a later temp of the same statement raises while being bound; and
a temp passed to a function returning the same owned type, which may return
the argument itself: the pass releases the argument and the result alike,
which the tracker reports as a double release. Of what a component holds: an
owned value in a list, a map or a struct field rather than a var of the owned
type; a package `var`, which `OwnedState` cannot release at teardown because
the effect lowering writes `Teardown` afresh after it; a var assigned
another's value, which both then release; a var initializer reading an owned
derived value, which is initialized after the vars; an allocation handed to a
`#[retains]` parameter outside an effect's handler (an event handler, a
function).

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
entry points, and where each calls it:

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
shared by every scope the way its write log is.

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
`letGo` before its `@unmount`), or the application outside one -- or until the
same call in the same lifetime hands the native another, which it holds in
its place: an `@update` keyed on a list is one lifetime however often it runs.
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
`--simulate`: a run ends with no leak check until application teardown exists
on targets. What the tracker cannot see is a call the gtk4 translator builds
after lowering; moving inline-cgo resolution into lowering is what brings
those under it (`cmd/sngl/testdata/simulate_cnative_owned.txt` pins the rest).

**Out of scope: a C language target.** It adds `#[owned]` to `string`, and
decides between per-statement release and a per-frame or per-event arena for
temps. State needs release-on-overwrite either way: an arena held for an
instance's lifetime keeps every rewrite of a label until unmount.
