# Ownership

One MR, `ownership`. Every part below is commits on this branch; none gets
its own MR. Merge `origin/main` as it moves. Delete this file before the MR
leaves draft. The MR closes #110.

A value the program allocates and something must give back -- a C string
handed to cairo, a pixbuf, a widget a C library keeps a pointer to -- has
nowhere in the pipeline to be released today. `wrapCArgs`/`cTypeFor`
(`codegen/lang/golang/ircontext.go`) write `C.CString` into the printed Go and
the gtk4 translator invents more after lowering, so no pass that can add a
statement ever sees an allocation. The result is #110: a per-frame leak in
every canvas that draws text, and a per-write leak on every reactive string
setter in gtk4's inline-cgo mode.

The fix is to make ownership something the IR says, and have lowering release
it. It is a language feature, not a cgo one: a C target makes every `string`
owned, and a Go-native handle wants the same treatment.

## The design

`design/ownership.md` holds it, and the spec's *Owned values* what it means.
Change those, not this file.

## Rules for every part

- Fixture first (`sngl fmt` writes it), and confirm it fails with the change
  reverted. Goldens assert generated code; `deny` asserts absence.
- `go tool verify` passes at the end of each part; recorded runs are
  `-update`d after any codegen change.
- Change the spec with the meaning, and the `design/` note with the
  mechanism, in the part that makes the change.
- Subagents get the comment rule from `~/.claude/CLAUDE.md` pasted into their
  prompt.
- At the end of each part: commit, push, update the MR description, tick the
  part here, and write the prompt for the next session.

## Part 1: marks, checker, tracker

The language half and the interpreter's tracking, with no pass and no target
yet. Fixtures release by hand, so the tracker is pinned before anything relies
on it.

- [X] `design/ownership.md` with the design above, and a line for it in
  CLAUDE.md's index.
- [X] `#[owned(release)]`, `#[retains]` and `#[moves]` in `sngl:macro`
  beside `cnative`. The checker refuses a release that is not a `func(T)`, an
  `owned` on anything but a type, and `retains`/`moves` on anything but a
  parameter of a bodyless (native) function.
- [X] `ir.Keyable` and the checker error for an owned derived value (a
  component's `func label => …`; SNGL has no `let`) whose body reads
  something that is not (`testdata/error_owned_let_unkeyable.sngl`). A `var`
  is exempt, since its initializer runs once and keys nothing.
- [X] Handles in the interpreter: leak, double release and use after release
  are errors naming the positions. Fixtures that commit each on purpose and
  expect the error, and one that releases correctly by hand and passes.
- [X] Application teardown in the interpreter: at the end of a test the root
  tree unmounts, `@unmount` fires (a fixture whose `@unmount` releases what
  would otherwise leak), timers stop, then the leak check runs. Nothing was
  torn down at test end before, which the design note says.
- [X] Spec: owned types, the four lifetimes, and application teardown.

## Part 2: simulated natives

- [X] `--simulate <target>` on `sngl test`: `Prepare` specialized for the
  target, `#[cnative]` calls stubbed as the design says. Not `sngl run`: a
  run has no end to leak check at until Part 6.
- [X] A script test leak checking a small program with its own `#[cnative]`
  declarations and an owned type, run with no C toolchain.
- [X] A script test running gtk4's `canvasText` override under it, expected
  to report today's leak; Part 4 flips it.

## Part 3: `CMarshal`

Move the C conversions out of the printer into IR, with no change in what
leaks yet.

- [X] `sngl:x/c`: `str` (`#[owned(free)]`), `cstring`, `free`, and a `cbool`
  in place of `cTypeFor`'s bool panic.
- [X] A `CMarshal` pass converting each argument of a `Foreign.Path == "C"`
  call explicitly; `wrapCArgs` and `cTypeFor` go. It runs under `--simulate`
  too, so the stubbed calls see the same `c.cstring`s the target does.
- [X] Every golden byte-identical except a `bool` argument, which now
  compiles (fixture for that).

## Part 4: `ReleaseTemps`

The statement half; closes the unbounded part of #110.

- [X] `ir.Package.Teardown`: it already existed as the effect lowering's exit
  handler, so the pass appends to it, and makes one where no `@unmount` did.
- [X] The pass: an application-lifetime value is a package-level declaration
  released in `Teardown`; the rest bind to `__relN` and release after the
  statement, before a `return`, and in a catch block of their own that raises
  again. It joins `interp.Prepare`; ErrorCatch does not, which the design note
  says why.
- [X] Statement temps without releasing by hand
  (`testdata/owned_release_temps.sngl`): in a loop, a `return`, a raise into a
  boundary, inside a lambda, a discarded result, and a constant.
  `owned_release_by_hand.sngl` keeps releasing its `var`s by hand: a local
  `var` is no temp.
- [X] Goldens: `testdata/owned_canvas_text.txtar` (`"serif"`
  application-lifetime, `"count: {n}"` freed each frame, `deny` a `defer` in
  the draw routine) and `testdata/owned_c_string_temps.txtar` (a loop, a
  return, a constant, a raise).
- [X] Part 2's `canvasText` script test reports no leak
  (`simulate_gtk4_canvas_text.txt`, renamed from `…_leaks.txt`).
- [X] Recorded runs `-update`d.
- [X] `<stdlib.h>`: fyne's and bubbletea's preambles (`EmitCHeader`) include
  it beside the program's links, as gtk4's does, so `c.free` compiles wherever
  a `c.str` is made. A `c.link` in sngl:x/c was the other way, but a library
  file's links are not collected, and one there would have silenced the
  checker's unlinked-native error for every program importing sngl:x/c.

## Part 5: `OwnedState`

The node half.

- [X] The pass: an owned derived value becomes an instance var plus an
  `effect(on=inputs, @update)` (not `@mount`/`@unmount`: the design note says
  why); an owned `var` gets release on overwrite (assign, then release the old
  value) and an `@unmount`; a `#[retains]` argument in an effect's handler
  takes its node's lifetime. It joins `interp.Prepare`. A component var
  released by hand is now a double release.
- [X] Fixtures under the tracker: a `var` overwritten, an owned derived value
  re-keyed and unmounted (branch, list row, window under `if`), a view-block
  var, a retained argument replaced and unmounted, a moved argument never
  released (`testdata/owned_state.sngl`,
  `cmd/sngl/testdata/simulate_cnative_owned.txt`).
- [X] Goldens on Go and JS (`testdata/owned_state.txtar`).
- [ ] The Kotlin golden: this host has no Android SDK, so its run cannot be
  recorded. Before it can be, android has to compile an owned value in a
  component under `if`: such a Composable cannot call a package function that
  writes package state (the release, in the fixture), which predates Part 5.
- [X] bubbletea keeps an instance's state when the `if` it sits under leaves
  and comes back, where every other target and the interpreter start it
  again; with this part that is a second release on the next leave. A `match`
  arm and a `for` copy too: an Enter effect sets the vars again where the
  instance stands, ahead of every other bracket
  (`testdata/bubbletea_state_leaves_with_branch.txtar`).
- [X] Found on the way: the interpreter kept a component's state across a
  branch leaving when its parent rendered nothing else
  (`testdata/component_state_leaves_with_branch.sngl`), and an effect's
  `@update` keyed on a prop of a component built at run time never ran on a
  target (`testdata/effect_update_on_prop.txtar`).
- [X] Found on the way: a component that renders nothing is spliced under a
  reactive `if` on every target and kept its state across the branch on all
  of them (`testdata/renders_nothing_state_leaves_with_branch.txtar`, which
  changes `async.query`'s on html); an owned var in a `for` copy did not
  generate when its initializer read the loop's variable; and `ReleaseTemps`
  took a cell's `map.get` for an allocation, releasing the value a write to an
  owned cell had just stored.
- [ ] android, which brackets lifetimes itself, gets no restart for a
  component that renders nothing spliced under a reactive `if`. Whether it
  keeps that component's state across the branch is unverified: the run
  cannot be recorded on this host.
- [X] The interpreter lost a package write a var initializer made while a
  `for` copy or a scoped population mounted: `var f = open()` with `open`
  counting in a package var left the count at 0, and each copy's `f` was the
  first one opened. The mount carries the copy's writes back as a handler does
  (`testdata/for_copy_initializer_writes_package.sngl`).
- [X] A write to an owned derived value's key while its branch is hidden left
  the `@update`'s dirty flag set, so on bubbletea the settle that took the
  branch back built the value in the enter function and again in the report.
  An `@update` under an `if` taken in the same pass as a write to its key
  ran on every target, where the interpreter reports only writes made while
  the bracket ran: a report now skips a bracket its position did not describe
  at the last pass
  (`testdata/owned_derived_built_once_on_entry.txtar`).
- [ ] An `@update` under a `for` whose key does not step into the loop still
  runs for a copy that entered in the pass a write reached its key: only a
  position of `if`s remembers what it described. It needs the copies keyed, as
  a cell under a loop has them (`dirtyAt`). android keeps nothing either:
  whether it reports to a bracket its `if` took back in the same pass as the
  write is unverified, since this host has no Android SDK to record the run
  (`testdata/owned_derived_built_once_on_entry.txtar` with kotlin/android in
  its `output` block).

## Part 6: application teardown on targets

- [ ] List every entry point a host calls to start the application, per
  target: `main`, the `run` an `@run` handler is handed, `BuildUI`, and
  anything else that starts one.
- [ ] Each gets its teardown: when the loop exits (gtk4 and fyne
  `RunWindows`, bubbletea's program returning), before `run` returns to the
  handler, the root tree unmounts, timers stop, and `Teardown` runs. Where the
  application ends only with its process or its host (html's page, android's
  activity), the design note says what ends it and that nothing more is owed.
- [ ] Settle `run()` called twice: a second application after a full
  teardown, or a refusal. Fixture either way.
- [ ] Script tests extending `platform_run_handler_runs.txtar`: an
  `@unmount` and an application-lifetime release both happen before "after"
  prints, on gtk4 and fyne.

## Part 7: GIR ownership

- [ ] girresolve reads `transfer-ownership` on `gir:` parameters: `full`
  is `#[moves]`. A string parameter with `full` is a test asserting no free
  is emitted. GIR has no annotation for a borrowed pointer the callee keeps,
  so `#[retains]` stays hand-written; list any gtk4 reaches.
- [ ] Audit the hand-written `#[cnative]` declarations in
  `lib/platform/gtk4/gtk4.sngl` against cairo's docs; none should retain, and
  the "cairo copies" comment on `selectFontFace` becomes redundant.

## Part 8: gtk4 images

The adjacent defect in #110: a drawn image is decoded and scaled from disk on
every frame.

- [ ] Declare gtk4's pixbuf as `#[owned(unrefPixbuf)]` and its loading in
  `gtk4.sngl`, so the image override is an owned derived value that Part 5 keys on
  `(src, w, h)`. `OwnedState` reaches a library component's body only on the
  interpreter; on a target the override has to be reached as well.
- [ ] Leak checked under `--simulate gtk4`; golden showing one load per key;
  recorded run.

## Part 9: gtk4 translator sites

What lowering cannot see: calls the inline-cgo translator builds after it.

- [ ] Constants application-lifetime, the rest freed after the call:
  `cString` in `codegen/platform/gtk4/intrinsic_translator.go`
  (`coerceSetterValue`, ctor zero values), the window title and the
  `sngl_connect` signal names in `compiler_ir.go`, and the `setEntryText`
  setter if `fix/program-writes-fire-no-events` has landed.
- [ ] Goldens: `ui.button(text=n)` beside a `draw.canvas` (#110's repro)
  frees per update; `deny` a bare `C.CString(` outside an
  application-lifetime declaration.

## Part 10: inline-cgo resolution in lowering

Part 9 is the short-term answer; this removes the reason for it. Moving native
resolution for inline-cgo widget build and updaters out of the translator into
lowering makes those calls IR, so `CMarshal`, the release passes and the
tracker cover them and Part 9's hand-written frees go. A C target needs the
same move.

- [ ] Scope it first. If it is larger than Parts 1 to 9 together, stop and
  raise splitting it out before starting.

## Part 11: docs

- [ ] `docs/reference/effective-sngl.md`: an advanced section on ownership --
  when to declare `#[owned]`, which lifetime a value gets and why, application
  teardown for a host that outlives the GUI, the keyable limitation and the
  shape that avoids it, and `--simulate` for finding a leak.
- [ ] Spec and `design/ownership.md` reread against what landed.
- [ ] `design/ownership.md`'s *What it does not release yet* (Part 4): fix
  each or say in the spec that it is held.
- [ ] MR description final; this file deleted.
