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

- [ ] `--simulate <target>` on `sngl test` (and `sngl run` if it fits):
  `Prepare` specialized for the target, `#[cnative]` calls stubbed as the
  design says.
- [ ] A script test leak checking a small program with its own `#[cnative]`
  declarations and an owned type, run with no C toolchain.
- [ ] A script test running gtk4's `canvasText` override under it, expected
  to report today's leak; Part 4 flips it.

## Part 3: `CMarshal`

Move the C conversions out of the printer into IR, with no change in what
leaks yet.

- [ ] `sngl:x/c`: `str` (`#[owned(free)]`), `cstring`, `free`, and a `cbool`
  in place of `cTypeFor`'s bool panic.
- [ ] A `CMarshal` pass converting each argument of a `Foreign.Path == "C"`
  call explicitly; `wrapCArgs` and `cTypeFor` go. It runs under `--simulate`
  too, so the stubbed calls see the same `c.cstring`s the target does.
- [ ] Every golden byte-identical except a `bool` argument, which now
  compiles (fixture for that).

## Part 4: `ReleaseTemps`

The statement half; closes the unbounded part of #110.

- [ ] `ir.Package.Teardown`, empty until something appends to it.
- [ ] The pass: an application-lifetime value is a package-level declaration
  released in `Teardown`; the rest bind to `__relN` and release after the
  statement, before a `return`, and in the catch block of a raise. It joins
  `interp.Prepare`.
- [ ] Part 1's fixtures stop releasing by hand: statement temps (in a loop, a
  `return`, a raise into a boundary, inside a lambda) and constants.
- [ ] Goldens: gtk4 canvas text (`"serif"` application-lifetime, `"count: {n}"` released each frame), a loop, a return. `deny` a `defer` in a draw
  function.
- [ ] Part 2's `canvasText` script test reports no leak.
- [ ] Recorded gtk4 runs `-update`d.

## Part 5: `OwnedState`

The node half.

- [ ] The pass: an owned derived value becomes an instance cell plus an
  `effect(on=inputs, @mount, @unmount)`; an owned `var` gets release on
  overwrite (assign, then release the old value) and an `@unmount`; a
  `#[retains]` argument in an updater takes its node's lifetime. It joins
  `interp.Prepare`.
- [ ] Fixtures under the tracker: a `var` overwritten, an owned derived value re-keyed
  and unmounted (branch, list row, window under `if`), a retained argument
  replaced and unmounted, a moved argument never released.
- [ ] Goldens on Go, JS and Kotlin.

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
  `(src, w, h)`.
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
- [ ] MR description final; this file deleted.
