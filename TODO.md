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

**A type declares its release.** `#[owned(release)]` on a type names the
function that gives one back. `sngl:x/c` declares `str` (`*C.char`, released
by `free`) and the conversions; gtk4 declares its pixbuf the same way. Nothing
is recognized by name.

**An owned value has one of four lifetimes**, the narrowest that fits:

| Lifetime      | When                                                                                  | Released                             |
|---------------|---------------------------------------------------------------------------------------|--------------------------------------|
| **Statement** | passed to a parameter that copies or borrows (GLib's transfer-none)                   | after the statement                  |
| **Node**      | held in component state, bound by a body `let`, or handed to a `#[retains]` parameter | on overwrite, on key change, unmount |
| **Program**   | built from constants                                                                  | never; interned once                 |
| **Moved**     | handed to a `#[moves]` parameter (transfer-full)                                      | never; the host owns it              |

**Node lifetime lowers onto `effect`.** A branch leaving, a list row removed,
a window closing are already `@unmount`, and `on=` already ends the old
lifetime before the new one starts (`testdata/effect_on_recreates.sngl`). So
the node half emits ordinary `effect`s and every target's existing `Effect`
lowering releases them. A body `let` of an owned type becomes an instance cell
filled in `@mount`, keyed `on=` its inputs, released in `@unmount`.

**What can be a key is one predicate, `ir.Keyable`**, asked by both the
checker and the pass. An owned `let` whose inputs are not keyable (a function
value, an async result) is a checker error, not a silent per-frame recompute.
The limitation is meant to be lifted later -- by function identity, by keying
on the settled async value, or by a write-counter key -- and widening the
predicate is how, so the pass must never grow its own idea of keyable.

**Two passes.**

```
… → OwnedState   (node half; before Effect so its effects lower like any other)
  → Effect
  → … → ErrorCatch → …
  → ReleaseTemps (statement half; after ErrorCatch so a raise path is a catch block)
  → ReservedNames
```

`ReleaseTemps` reuses CSE's reach (`imperativeBlocks`): statement-local, never
hoisting out of a lambda. It releases with an explicit statement after the use,
never Go's `defer` -- that is function-scoped, wrong in a loop in a draw
callback, and C has none. A `return` binds its result, releases, then returns.

**The interpreter is the reference.** It implements the four lifetimes
directly (it runs neither pass), so a fixture can declare an owned type whose
release is SNGL -- a function that appends to a log -- and assert exactly when
each release happens with no C in sight. The passes are held to it by goldens
and by script tests running the same program on a target.

**Out of scope, recorded in the design note:** a C language target. It adds
`#[owned]` to `string`, and decides between per-statement release and a
per-frame/per-event arena for temps. State needs release-on-overwrite either
way: an arena held for an instance's lifetime keeps every rewrite of a label
until unmount.

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

## Part 1: marks, checker, interpreter

The language half, with no target involved.

- [ ] `design/ownership.md` with the design above, and a line for it in
  CLAUDE.md's index.
- [ ] `#[owned(release)]`, `#[retains]` and `#[moves]` in `sngl:macro`
  beside `cnative`. The checker refuses a release that is not a `func(T)`, an
  `owned` on anything but a type, and `retains`/`moves` on anything but a
  parameter of a bodyless (native) function.
- [ ] `ir.Keyable` and the checker error for an owned `let` or `var` in a
  component body whose initializer reads something that is not
  (`testdata/error_owned_let_unkeyable.sngl`).
- [ ] The interpreter releases on all four lifetimes. Fixtures with a SNGL
  release that logs: statement temps (incl. in a loop, a `return`, a raise
  into a boundary, inside a lambda), interned constants released never, a
  `var` overwritten, a body `let` re-keyed and unmounted (branch, list row),
  a retained argument replaced and unmounted, a moved argument never
  released.
- [ ] Spec: a section on owned types and the four lifetimes.

## Part 2: `CMarshal`

Move the C conversions out of the printer into IR, with no change in what
leaks yet.

- [ ] `sngl:x/c`: `str` (`#[owned(free)]`), `cstring`, `free`, and a `cbool`
  in place of `cTypeFor`'s bool panic.
- [ ] A `CMarshal` pass converting each argument of a `Foreign.Path == "C"`
  call explicitly; `wrapCArgs` and `cTypeFor` go.
- [ ] Every golden byte-identical except a `bool` argument, which now
  compiles (fixture for that).

## Part 3: `ReleaseTemps`

The statement half; closes the unbounded part of #110.

- [ ] The pass: intern constant operands at package level, bind the rest to
  `__relN` and release after the statement, release before a `return` and in
  the catch block of a raise.
- [ ] Goldens: gtk4 canvas text (`"serif"` interned, `"count: {n}"` released
  each frame), a loop, a return. `deny` a `defer` in a draw function.
- [ ] Script test running Part 1's fixtures on Go and JS to show the targets
  agree with the interpreter.
- [ ] Recorded gtk4 runs `-update`d.

## Part 4: GIR ownership

- [ ] girresolve reads `transfer-ownership` on `gir:` parameters: `full`
  is `#[moves]`. A string parameter with `full` is a test asserting no free
  is emitted. GIR has no annotation for a borrowed pointer the callee keeps,
  so `#[retains]` stays hand-written; list any gtk4 reaches.
- [ ] Audit the hand-written `#[cnative]` declarations in
  `lib/platform/gtk4/gtk4.sngl` against cairo's docs; none should retain, and
  the "cairo copies" comment on `selectFontFace` becomes redundant.

## Part 5: `OwnedState`

The node half.

- [ ] The pass: an owned body `let` becomes an instance cell plus an
  `effect(on=inputs, @mount, @unmount)`; an owned `var` gets release on
  overwrite (assign, then release the old value) and an `@unmount`; a
  `#[retains]` argument in an updater takes its node's lifetime.
- [ ] Goldens on Go, JS and Kotlin; the Part 1 fixtures' script test extended
  to these cases.

## Part 6: gtk4 images

The adjacent defect in #110: a drawn image is decoded and scaled from disk on
every frame.

- [ ] Declare gtk4's pixbuf as `#[owned(unrefPixbuf)]` and its loading in
  `gtk4.sngl`, so the image override is a body `let` that Part 5 keys on
  `(src, w, h)`.
- [ ] Golden showing one load per key; recorded run.

## Part 7: gtk4 translator sites

What lowering cannot see: calls the inline-cgo translator builds after it.

- [ ] Literals interned, the rest freed after the call: `cString` in
  `codegen/platform/gtk4/intrinsic_translator.go` (`coerceSetterValue`, ctor
  zero values), the window title and the `sngl_connect` signal names in
  `compiler_ir.go`, and the `setEntryText` setter if
  `fix/program-writes-fire-no-events` has landed.
- [ ] Goldens: `ui.button(text=n)` beside a `draw.canvas` (#110's repro)
  frees per update; `deny` a bare `C.CString(` outside an interned var.

## Part 8: inline-cgo resolution in lowering

Part 7 is the short-term answer; this removes the reason for it. Moving native
resolution for inline-cgo widget build and updaters out of the translator into
lowering makes those calls IR, so `CMarshal` and the release passes cover them
and Part 7's hand-written frees go. A C target needs the same move.

- [ ] Scope it first. If it is larger than Parts 1 to 7 together, stop and
  raise splitting it out before starting.

## Part 9: docs

- [ ] `docs/reference/effective-sngl.md`: an advanced section on ownership --
  when to declare `#[owned]`, which lifetime a value gets and why, the
  keyable limitation and the shape that avoids it.
- [ ] Spec and `design/ownership.md` reread against what landed.
- [ ] MR description final; this file deleted.
