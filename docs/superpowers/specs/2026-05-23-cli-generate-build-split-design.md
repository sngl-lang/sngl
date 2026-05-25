# CLI: split code emission from binary builds; add `test` build option

Date: 2026-05-23

## Goal

Resolve the long-standing duplication between `sngl compile`, `sngl build`,
and `sngl run` (audit `cli.md` §4, §5) and introduce a `test` build option
that, when set, makes `sngl generate` produce native test files alongside
the generated app code. The tests run under the host project's own test
runner — projects using SNGL as a pure code generator do not need to run
`sngl test` separately.

This is a precursor to a larger redesign of how `sngl test` works across
platforms (separate spec). The pieces this spec lands are required by that
later work but stand on their own.

## Command surface

| Today                                            | After                                                                          |
|--------------------------------------------------|--------------------------------------------------------------------------------|
| `sngl compile` — parse→check→lower→generate code | **removed** (no alias)                                                         |
| `sngl build` — generate code + `Builder.Build()` | `sngl generate` — generate code only, to `--out`                               |
| `sngl run` — generate + build + execute          | `sngl build` — generate to temp dir + `Builder.Build()`, produce binary in cwd |
|                                                  | `sngl run` — same as `build` then exec the resulting binary                    |

Rationale: one verb per artifact class. `generate` produces source; `build`
produces a binary; `run` produces a binary and executes it. Each is a thin
wrapper around the previous.

## Pipeline centralization

A single internal function `runPipeline` (in `cmd/sngl/pipeline.go`)
performs parse→merge→check→optimize→lower→generate. It is the
sole caller of those passes from the CLI. `generate`, `build`, `run`, and
`dump` all call it.

This collapses three divergent copies of the pipeline and fixes the
audit-noted bugs: `OptionConfigurable.Configure` not run under build/run;
`noCacheBust` ignored under build/run; lang option set under run but not
elsewhere.

## `test` build option

Add to `lib/options.sngl`:

```sngl
struct Options {
    ...
    // When true, the generator emits unit tests in the target language
    // alongside the application code. Tests use the target language's
    // native test runner (Go's testing, Kotlin's JUnit, JS's Jest/Vitest).
    // No SNGL-side test runtime is linked in.
    test bool
}
```

Surface to the CLI as `--opt test=true`, or as `test=true` inside an
`output { }` block.

### Landing order

`test: true` is *parsed* and wired into the option schema as part of this
spec. The actual emission of dispatcher + native test files lands with the
NativeTestEmitter work in the separate test-architecture spec. Until
NativeTestEmitter ships, `--opt test=true` is accepted but emits nothing
beyond the regular code — no error, no warning. (This avoids stranding the
option in CLI land while the larger work proceeds.)

### What `test: true` emits (once NativeTestEmitter lands)

- For each component with at least one `t.assert`-bearing test func, emit
  a per-component test dispatcher (`__sngl_test_dispatch`).
- For each test func, emit a native test in the target language that calls
  through the dispatcher. The emitter is the same `NativeTestEmitter`
  defined in the test-architecture redesign spec.
- Tests land in the same `--out` directory as the generated source, using
  whatever filename convention the target language requires
  (`*_test.go`, `*Test.kt`, `*.test.js`).

### What `test: true` does **not** do

- No IPC test agent. The generated artifact is *not* drivable by
  `sngl test --platform=X` after the fact.
- No language-runtime linkage beyond what the native test files themselves
  pull in.

**Trade-off.** If the user's project has non-SNGL code that requires a
custom entry point (e.g. android Application subclass with manual setup),
`sngl test` cannot drive that custom-entry-point binary. They must rely on
the emitted native tests under `--opt test=true` and their existing CI to
run them.

## Binary naming for `build`

`build` writes its binary to the current working directory.

- Default name: `Options.name` from `lib/options.sngl`, sanitized to a
  valid filename (lowercased, non-alphanumeric → `-`). Adds `.exe` on
  windows targets.
- `--out path` overrides. If `path` is a directory, the default name is
  used inside it. If `path` is a file, that exact name is used.

Multi-target build (one source, several `output()` blocks): each target
gets a binary, named `<base>-<platform>` (or `<base>-<platform>.exe`) when
more than one target is present. Single-target builds use the bare base
name.

## Migration notes

- `sngl compile` is removed in this change. No deprecation period.
  Documentation, `cmd/sngl/testdata/compile_*.txt` scripts, and any
  internal callers move to `sngl generate`.
- `cmd/sngl/testdata/` golden scripts are renamed in bulk:
  `compile_*.txt` → `generate_*.txt`.
- `--out` default for `generate` stays `.` (matches old `compile`).
- `--out` default for `build` is also `.` but means "binary placement",
  not source-code placement (no source files written).

## Out of scope (this spec)

- TestScript IR, NativeTestEmitter implementation, dispatcher emission
  details — covered in the test-architecture spec.
- `sngl test` redesign — same.
- All other CLI audit findings (`--lang` vs `--language`, persistent
  `--format` collision, `--project` dead flag, etc.). Tracked separately.

## Test plan

- `cmd/sngl/testdata/generate_*.txt` — renamed `compile_*.txt` scripts run
  green against the new pipeline.
- `cmd/sngl/testdata/build_*.txt` — new scripts asserting that
  `sngl build` produces an executable file at the expected path and that
  the file is invokable for at least the `none` platform (cheapest end-to-
  end check).
- `cmd/sngl/testdata/build_multi_target.txt` — asserts the
  `<base>-<platform>` naming for multi-target builds.
- A txtar script that asserts `--opt test=true` is *accepted* by both
  `generate` and `build` without error. Assertions on file contents land
  with the NativeTestEmitter work.
- An audit-#4 regression test: a fixture with `noCacheBust: true` and an
  asset reference. Assert that `generate` and `build` emit identical asset
  filenames.
