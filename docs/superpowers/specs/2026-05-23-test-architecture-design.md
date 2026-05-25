# Test architecture

Date: 2026-05-23

## Goals

1. One source of truth for SNGL test semantics across every supported
   platform and language. Today five hand-rolled runners drift; users
   discover platform-specific quirks through whack-a-mole.
2. Cheap-to-add platform support. Adding a new target should not require
   re-implementing testing.
3. Cross-platform regression coverage: `sngl test --platform=all testdata/` runs the same fixtures against every target and reports a
   green-or-red matrix.
4. Make `--opt test=true` (per the CLI generate/build spec) actually emit
   useful native tests — Go's `testing`, Kotlin's JUnit, JS's Jest — that
   land in the user's project and run under the project's existing CI.

## Non-goals

- Replicating every host-language testing framework feature
  (parameterised tests, custom matchers, etc.). SNGL tests are written
  in SNGL; native idioms come into play only when emitting `--opt test=true` output.
- Backwards compatibility with the existing five-runner shape. The
  current `codegen/platform/*/runtests.go` files go away (or shrink to
  the per-platform transport hook).
- A separate test IR or per-op dispatcher. (Both were considered and
  rejected — see Alternatives.)

## High-level architecture

```
test func IR  ─►  NativeTestEmitter(<lang>)  ─►  target-lang test source
                                                       │
                                  ┌────────────────────┴───────────────────┐
                                  │                                        │
                          --opt test=true                            sngl test
                                  │                                        │
                                  ▼                                        ▼
                  files land in user's --out                 codegen also links the
                  (no SNGL runtime linked)                   per-language testagent runtime;
                                                             sngl test process drives
                                                             the binary via JSON-RPC
```

One emitter per host language. Two emission modes selected at codegen
time:

- **Native mode** (`--opt test=true`): emit symbols that call the host
  language's native testing API (`t.Log`, `t.Errorf`, `Assert.assertTrue`,
  `expect(…).toBe(…)`).
- **Agent mode** (`sngl test`): emit symbols that call into a small
  per-language `testagent` runtime which speaks JSON-RPC to the driver.

The user's program code is identical in both modes. Only the lowering of
test intrinsics differs.

## Intrinsic surface

### Atoms

| Intrinsic                    | Description                                                                                   |
|------------------------------|-----------------------------------------------------------------------------------------------|
| `t.log(msg)`                 | Record an output line.                                                                        |
| `t.fail()`                   | Mark the current test failed. Execution continues.                                            |
| `t.skip(reason)`             | Mark the current test skipped; abort the test body.                                           |
| `t.setContext(ctx, value)`   | Overlay the given context value for this test and its subtests. Purely local mutation.        |
| `t.snapshot(name)`           | Capture rendered state and assert it matches the named golden. See `Snapshot handling` below. |
| `t.wait(duration)`           | Sleep.                                                                                        |
| `t.waitFor(pred, timeoutMs)` | Poll `pred` until it returns true or the timeout elapses. Fails the test on timeout.          |
| `t.test(name, body)`         | Run `body` as a nested subtest.                                                               |

`abort` (the unwind that ends test body execution after `t.skip` or a
`fatal`-class composite) is not an intrinsic exposed to users. The agent
runtime implements it as `panic`/`recover` in Go, exceptions in Kotlin
and JS.

### Composites

These desugar to atoms during lowering:

| Composite          | Desugaring                    |
|--------------------|-------------------------------|
| `t.error(msg)`     | `t.log(msg); t.fail()`        |
| `t.fatal(msg)`     | `t.log(msg); t.fail(); abort` |
| `t.failNow()`      | `t.fail(); abort`             |
| `t.assert(b, msg)` | `if !b { t.fatal(msg) }`      |

### Lowering rules

- **Native mode** (`--opt test=true`): if the host language's native
  testing API has a direct match for a composite (Go's `t.Errorf` =
  `log+fail`; `t.Fatalf` = `log+fail+abort`), emit the composite
  directly. Otherwise decompose to atoms. Decisions live per language
  in `NativeTestEmitter(<lang>)`.
- **Agent mode** (`sngl test`): always decompose to atoms. Keeps the
  testagent surface and the RPC method set small — exactly one of each
  atom.

### Direct calls (no intrinsic)

State reads, state writes, and event fires inside test bodies do not
become RPCs or intrinsics — they compile to direct calls on the
component value, identical to non-test code, because the test runs
inside the same process as the program. `c.x = 5` is a setter call;
`c.btn.@click()` is the event-handler call. Reactivity runs
synchronously through these same paths in both modes.

## Per-language `testagent` runtime

Lives under `pkg/<lang>/testagent/`. Estimated ~200 LOC per language.

Responsibilities:
- Expose a `main()` (or equivalent entry point) that accepts the driver
  protocol over the platform's transport.
- Maintain a registry of test funcs that codegen populates during
  package init.
- Provide the agent-mode intrinsic implementations: `log`, `markFail`,
  `markSkip`, `setContext`, `snapshot`, `wait`, `waitFor`, `runSubtest`.
- Run the unwind machinery for `abort` (`panic`/`recover` in Go,
  exceptions elsewhere).

Snapshot of the Go runtime's shape:

```go
package testagent

type T struct {
    name    string
    failed  bool
    skipped bool
    log     []string
    parent  *T
    rpc     *rpcClient
}

func (t *T) Log(msg string)        { t.rpc.notify("log", LogArgs{...}) }
func (t *T) Fail()                 { t.failed = true; t.rpc.notify("markFail", ...) }
func (t *T) Skip(reason string)    { t.skipped = true; t.rpc.notify("markSkip", ...); abort() }
func (t *T) Snapshot(name string)  { ... } // see Snapshot handling
func (t *T) Wait(d time.Duration)  { time.Sleep(d) }
func (t *T) Test(name string, body func(*T)) { ... }

func RegisterTest(name string, fn func(*T)) { registry[name] = fn }

func Main() { /* parse stdin RPC, dispatch list/run/cancel */ }
```

`RegisterTest` calls are emitted by codegen alongside each test func.

## Wire protocol

JSON-RPC 2.0 with `id` distinguishing requests from notifications
(notifications omit `id` and expect no response).

### Driver → Agent (requests)

| Method   | Params              | Result                                                     |
|----------|---------------------|------------------------------------------------------------|
| `list`   | `{}`                | `{tests: [name, ...]}`                                     |
| `run`    | `{filter?: string}` | `{}` (results stream as notifications until `runComplete`) |
| `cancel` | `{}`                | `{}`                                                       |

### Agent → Driver

**Notifications:**
- `testStart{test: string}`
- `testEnd{test: string, status: "pass"|"fail"|"skip", durationMs: number}`
- `log{test: string, msg: string}`
- `markFail{test: string, location?: {file, line}}`
- `markSkip{test: string, reason?: string}`
- `runComplete{passed: number, failed: number, skipped: number}`

**Requests:**
- `snapshotAssert{test: string, name: string, mime: string, bytes: base64}` →
  `{pass: bool, diff?: string}` (driver owns golden-file logic, returns
  `pass: true` when running in update-snapshots mode). `mime` selects
  the driver's storage extension and diff strategy — `text/plain`,
  `text/ansi`, `image/png`, `application/json`. Driver rejects unknown
  mimes with a JSON-RPC error.

### Error model

Standard JSON-RPC error codes. Agent-side panics that escape `recover`
emit a final `testEnd{status: "fail"}` with the stack trace in the
preceding `log`, then `runComplete`. The driver treats connection drop
mid-run as a fail-all on outstanding tests.

## Transport per platform

| Platform    | Launcher source                                         | Transport                                                                | Lifecycle                                                                                                                                                                              |
|-------------|---------------------------------------------------------|--------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `none`      | none platform itself                                    | In-process function call (no socket; agent is a library)                 | `sngl test` calls the agent's `Main` equivalent directly with a synthetic RPC connection.                                                                                              |
| `bubbletea` | Go lang (fallback)                                      | stdin/stdout JSON-RPC                                                    | Spawn compiled binary; frame stdout for protocol; user logs flow via `log` notifications.                                                                                              |
| `fyne`      | Go lang (fallback)                                      | stdin/stdout JSON-RPC                                                    | Same as bubbletea.                                                                                                                                                                     |
| `gtk4`      | gtk4 platform (window lifecycle needs special handling) | unix socket in `$XDG_RUNTIME_DIR/sngl-test-<pid>.sock`                   | Compiled binary opens its windowed app; testagent thread serves the socket. Platform override required because the GUI mainloop and the RPC loop need to coexist on different threads. |
| `html`      | html platform                                           | WebSocket from the browser back to a `sngl test`-hosted localhost server | Driver starts WebSocket server, opens page via rod, agent connects back.                                                                                                               |
| `android`   | android platform                                        | adb-forwarded TCP socket                                                 | Build APK with testagent linked; install to emulator/device; `adb forward tcp:<host> tcp:<device>`; launch via intent; agent connects back.                                            |

A **launcher** (small file, ~50 LOC) handles "build, spawn, connect,
hand back an RPC channel." One interface, `codegen.TestLauncher`,
implementable by *either* a `PlatformGenerator` *or* a `LangTranslator`
— same pattern as `codegen.Builder`. Resolution: if the platform
implements `TestLauncher`, use it; otherwise fall back to the
language's implementation; otherwise error.

```go
type TestLauncher interface {
	Launch(ctx context.Context, pkg *ir.Package, lang LangTranslator, opts *ir.StructLit) (RPCChannel, Cleanup, error)
}
```

Most Go-emitting platforms (bubbletea, fyne, gtk4) need nothing custom
— they all "compile a binary, spawn it, talk JSON-RPC over its
stdin/stdout." The Go language's `TestLauncher` does that once and the
platforms inherit it.

Platforms whose lifecycle differs implement the interface themselves
and override the language fallback:
- `android`: APK build via gradle, install, `adb forward`, launch
  intent, connect.
- `html`: spin up a localhost WebSocket server, generate the HTML +
  testagent JS, drive a browser via rod to load the page, accept the
  agent's inbound connection.

## Snapshot handling

`t.snapshot(name)` is an atom at the SNGL surface. Its lowering depends
on what the platform's runtime can render in-process:

| Platform      | Capture                                                                                                                                                 | Mime         |
|---------------|---------------------------------------------------------------------------------------------------------------------------------------------------------|--------------|
| Bubbletea     | lipgloss `View()` → string                                                                                                                              | `text/ansi`  |
| Fyne          | `canvas.NewCapture` → PNG                                                                                                                               | `image/png`  |
| Android       | screenshot bitmap → PNG                                                                                                                                 | `image/png`  |
| Gtk4          | GdkPixbuf → PNG                                                                                                                                         | `image/png`  |
| Html          | driver-side `Page.captureScreenshot` via CDP (agent's `snapshot` intrinsic asks driver to capture; in-page JS can't cheaply serialise the rendered DOM) | `image/png`  |
| None / interp | visual-node dump → string                                                                                                                               | `text/plain` |

Golden files live alongside fixtures:
`testdata/<fixture>.snapshots/<name>.<ext>` where `<ext>` is derived
from `mime` (`.txt`, `.ansi`, `.png`, `.json`). `SNGL_UPDATE_SNAPSHOTS=1`
puts the driver in update mode (always returns `pass: true` and
overwrites the golden).

Diff strategy is mime-driven: text mimes use unified diff;
`image/png` uses `internal/imgdiff` for pixel-tolerance comparison;
`application/json` uses structural diff.

## Sequencing

1. **Land `NativeTestEmitter(go)` and the Go `testagent` runtime.** Run
   the existing testdata against it via a new `sngl test --platform=bubbletea` path that uses the agent (not the
   `runtests.go` shell-out to `go test`).
2. **Wire `--opt test=true`** in `sngl generate` to invoke the same
   emitter in native mode; write the resulting `*_test.go` files into
   `--out`. This is the user-visible CLI feature the previous spec
   deferred.
3. **Migrate `fyne` and `gtk4`** off their bespoke `runtests.go` files
   onto the Go testagent + their respective transports. Delete the old
   per-platform test runners. Net code reduction.
4. **Adapt `none` / interp.** The existing
   `codegen/platform/none/testrunner` keeps walking IR but wraps its
   intrinsic calls in the same agent-mode shape as compiled platforms.
   Uniformity for downstream reporters.
5. **Kotlin path.** Implement `NativeTestEmitter(kotlin)` and the
   Kotlin testagent. Wire `sngl test --platform=android` to the adb
   transport. This is the first IPC platform; gets us to a cross-host
   test matrix.
6. **JS path.** Implement `NativeTestEmitter(js)` and the JS testagent.
   Wire `sngl test --platform=html` to the WebSocket transport;
   rebuild the html platform's test runner around it. (Rod stays for
   page launch and screenshot capture.)
7. **Backfill the audit's fixture gaps.** With the matrix actually
   producing red lights, write the missing fixtures for the 22
   untested stdlib components. Tracked separately.

## Alternatives considered

- **TestScript IR + per-op dispatcher.** Lower each test func to a flat
  sequence of `Set` / `Read` / `Fire` / `Assert` ops; agent applies
  them. Rejected as over-engineered: one RTT per op over IPC, requires
  predicate-closure machinery for arbitrary assertion expressions,
  duplicates work the host language's evaluator already does.
- **Piggyback on native runners' wire formats** (`go test -json`,
  JUnit XML, Jest reporter). Considered cheap because each adapter is
  small. Rejected because we own the binary anyway under `sngl test`,
  so we may as well speak one protocol — saves three parsers and
  centralises diagnostic formatting.
- **External UI automation drivers (AT-SPI, WayDriver, Appium).**
  Considered for gtk4 and android. Rejected because the embedded
  testagent model is uniform across all platforms and gives us direct
  access to component state rather than only what the accessibility
  surface exposes.

## Out of scope (tracked separately)

- Backfilling missing fixtures for 22 stdlib components without test
  coverage.
- Test-only LSP features (test-codelens, run-this-test).
- CI matrix wiring for `sngl test --platform=all` (Dockerfile update;
  separate task once the per-platform agents land).
- A `sngl test` JUnit-XML output mode for CI consumers that want
  upstream test-result UIs. Easy add later; not blocking.

## Test plan

- Each platform's `testagent` package has its own unit tests covering
  RPC framing, intrinsic dispatch, and abort/unwind paths. These run
  without any SNGL source.
- A new top-level test (`cmd/sngl/test_integration_test.go`) runs a
  representative fixture against every available platform via the
  `sngl test --platform=all` path and asserts the same pass/fail count
  on each.
- Snapshot diffing has unit tests over the golden-file machinery
  (`codegen/testharness/snapshot/`).
