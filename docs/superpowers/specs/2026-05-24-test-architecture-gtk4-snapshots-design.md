# Test architecture — gtk4 cutover + snapshot backfill

Date: 2026-05-24

## Goal

Complete the test architecture's GUI-platform coverage:

1. Cut over gtk4 from its legacy `runtests.go` to the IR-native
   `TestLauncher` path established in Plan 1.
2. Make `t.snapshot(name)` produce real rendered bytes on every Go-
   emitting GUI platform — bubbletea, fyne, gtk4 — backfilling Plan 1's
   bubbletea/fyne cutovers which emitted no snapshot capture.

Cross-platform test fixtures with `t.snapshot` are then meaningful:
visual regressions in the platform's view-builder show up as golden-diff
failures.

## Non-goals

- Full mainloop-driven testing (continuous `tea.NewProgram` / `app.Run`
  / `gtk_application_run` for the entire test session). Rejected as
  scope creep; today's white-box model (direct field writes + event
  handlers invoked directly) stays.
- Black-box message-pipeline coverage (every state mutation going
  through `Update`/Msg). Same reason.
- Snapshot pixel-tolerance / perceptual-diff. Today the golden-diff is
  bytes-equal for PNGs (already supported by `codegen/testharness/ snapshot`). Richer diff lands when a fixture demands it.

## High-level architecture

Each test runs to completion on the main goroutine of the spawned
testagent binary — no continuous mainloop, no background RPC. The new
piece is **per-snapshot mainloop entry** on platforms whose render
pipeline requires a live app/widget tree.

For each `t.snapshot(name)` call:

```
                 ┌─────────────────────────────────────────┐
testagent.T ───► │ platform's snapshotBytes(m Model)       │ ───► (mime, []byte)
  .Snapshot      │                                         │
                 │ bubbletea: pure View() call             │
                 │ fyne:      headless app.NewWithID +     │
                 │            canvas capture + teardown    │
                 │ gtk4:      gtk_application_run on bg    │
                 │            goroutine, capture in        │
                 │            activate, quit               │
                 └─────────────────────────────────────────┘
                                    │
                                    ▼
                       testrpc.snapshotAssert request
                                    │
                                    ▼
                       sngl test driver diffs against
                       <fixture>.snapshots/<name>.<ext>
```

Whether a platform reuses one mainloop across all snapshots in a
session or spins up fresh per snapshot is a tactical choice. Default:
**fresh per snapshot** for isolation; optimise if measurement shows it
matters.

## Per-platform snapshot capture

Codegen emits a `snapshotBytes` function alongside the existing
`newTestComponent` helper in each platform's `testagent_main.go`. The
function takes the current `Model` value and returns the rendered
bytes, the mime type, and any error.

### Bubbletea

```go
func snapshotBytes(m Model) (string, []byte, error) {
	return "text/ansi", []byte(m.View()), nil
}
```

`View()` is a pure function on bubbletea Model. No mainloop, no
goroutine. Submit as `text/ansi` since lipgloss output contains escape
sequences.

### Fyne

```go
func snapshotBytes(m Model) (string, []byte, error) {
	a := app.NewWithID("sngl-test-snapshot")
	defer a.Quit()
	win := a.NewWindow("test")
	win.SetContent(m.buildView()) // codegen-emitted view-builder
	win.Resize(fyne.NewSize(800, 600))
	img := test.WindowToImage(win)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", nil, err
	}
	return "image/png", buf.Bytes(), nil
}
```

`fyne.io/fyne/v2/test`'s `WindowToImage` runs the layout pass and
returns an `image.Image`. The `app.NewWithID` instance is offscreen by
default in test mode.

If the test pkg-import of `fyne.io/fyne/v2/test` pulls in a heavier
dependency tree, the codegen-emitted `agent_main.go` gates that import
behind a `// +build sngl_test_agent` tag or similar so non-test
generations don't pay the cost. (Verify during implementation.)

### Gtk4

Strict main-thread rule. Each snapshot:

```go
func snapshotBytes(m Model) (string, []byte, error) {
    if os.Getenv("GDK_BACKEND") == "" {
        os.Setenv("GDK_BACKEND", "offscreen")
    }
    type result struct {
        bytes []byte
        err   error
    }
    done := make(chan result, 1)

    runtime.LockOSThread()
    defer runtime.UnlockOSThread()

    app := C.gtk_application_new(C.CString("us.duckfam.sngl.test"), 0)
    // activate handler: build widgets from m, render to PNG, push to done, quit
    C.g_signal_connect(... "activate" ..., onActivate)
    C.g_application_run(...)
    // returns when quit fires

    res := <-done
    return "image/png", res.bytes, res.err
}
```

Activate handler does:
1. Build the widget tree from `m` (codegen emits a `buildWidgets(m)`
   helper that mirrors what `BuildUI` does in production).
2. Trigger initial layout (gtk auto-runs this).
3. Render the toplevel window to a `cairo_surface_t` via
   `gtk_widget_snapshot_to_paintable` (gtk4 API) or
   `gtk_native_get_surface` + cairo recording surface.
4. Encode the surface as PNG bytes.
5. Send result via `done`, call `g_application_quit`.

`runtime.LockOSThread()` is mandatory because gtk4 widget creation
must happen on the same OS thread as `g_application_run`. The
`snapshotBytes` function pins the caller goroutine for its duration.

A second snapshot in the same test starts a fresh `GtkApplication` (we
discarded the first). This is slow (~50-200ms per snapshot) but
isolated. Optimisation deferred.

## Gtk4 cutover (mechanical)

Mirrors Plan 1's bubbletea and fyne cutovers:

1. Delete `codegen/platform/gtk4/runtests.go` and
   `codegen/platform/gtk4/runtests_js.go`.
2. Add the agent-mode emission block in gtk4's `Generate`:
   - When `Options.test=true && Options.testMode=="agent"`: emit
     `testagent_main.go` (via `golang.LowerTestFile(... TestEmitAgent)`)
     + `agent_main.go` (calls `testagent.Main()`) + the `snapshotBytes`
       function specific to gtk4 + a `buildWidgets(m)` helper for the
       activate handler.
   - When `Options.test=true && testMode != "agent"`: emit
     `*_test.go` files for the user's project (native mode), same
     `snapshotBytes` so `--opt test=true` produces native tests with
     working snapshot support.
3. Gtk4 inherits Go's `TestLauncher` from Plan 1 — `go build` already
   picks up the CGo + `pkg-config: gtk4` deps via the same go.mod
   synthesis.
4. Add a probe: `TestLauncher` returns a skip-style error if
   `pkg-config --exists gtk4` fails on the host. Mirrors how Plan 1
   handles missing toolchains.

## Bubbletea + fyne snapshot backfill

Plan 1's bubbletea/fyne `Generate` produces a `testagent_main.go` that
defines `newTestComponent()` but **not** `snapshotBytes`. The testagent
runtime's `Snapshot` intrinsic today is unimplemented (or panics on
call). Plan 2 closes that gap:

1. Each Go-emitting platform's `Generate` adds `snapshotBytes` to the
   emitted file. Convention: same signature
   `func snapshotBytes(m Model) (string, []byte, error)`.
2. `pkg/go/testagent.T.Snapshot(name string)` calls the emitted
   `snapshotBytes` (via a package-level function pointer the emitted
   `init()` sets), encodes the bytes as base64, submits
   `snapshotAssert{test, name, mime, bytes}` via the RPC channel,
   awaits the response, marks the test failed if the diff comes back
   non-empty.

The function-pointer indirection avoids forcing `pkg/go/testagent`
itself to know about per-platform Model types. The emitted package
populates the pointer during init.

```go
// pkg/go/testagent/snapshot.go
var snapshotFn func() (string, []byte, error)

func RegisterSnapshot(fn func() (string, []byte, error)) {
	snapshotFn = fn
}

func (t *T) Snapshot(name string) {
	if snapshotFn == nil {
		t.Errorf("snapshot %q: no capture registered for this platform", name)
		return
	}
	mime, raw, err := snapshotFn()
	if err != nil {
		t.Errorf("snapshot %q: capture: %v", name, err)
		return
	}
	res, err := t.rpc.snapshotAssert(t.name, name, mime, raw)
	if err != nil {
		t.Errorf("snapshot %q: rpc: %v", name, err)
		return
	}
	if !res.Pass {
		t.Errorf("snapshot %q mismatch:\n%s", name, res.Diff)
	}
}
```

The emitted `agent_main.go` per platform passes a closure capturing the
current Model so each Snapshot call reads fresh state:

```go
// agent_main.go (bubbletea)
func main() {
	testagent.RegisterSnapshot(func() (string, []byte, error) {
		return snapshotBytes(currentModel())
	})
	testagent.Main()
}
```

Where `currentModel()` is a small generator-provided accessor; for
bubbletea that's the same `newTestComponent` result the active test
holds, exposed via package-level state the agent updates per-test.
**Verify during implementation** that this side-channel doesn't fight
the per-test isolation Plan 1 establishes.

## Test plan

Three new txtar fixtures plus per-platform unit tests:

- `cmd/sngl/testdata/test_bubbletea_snapshot.txt` — a fixture with
  `t.snapshot("initial")`; asserts first run creates the golden under
  `SNGL_UPDATE_SNAPSHOTS=1`, second run passes against it, modifying
  the model and snapshotting fails.
- `cmd/sngl/testdata/test_fyne_snapshot.txt` — same shape against
  fyne. `[!exec:go]` skip + a separate exec-fyne-libs probe.
- `cmd/sngl/testdata/test_gtk4_snapshot.txt` — same shape against
  gtk4. Skip when `pkg-config --exists gtk4` fails.

Per-platform unit test (in each platform package):
`TestSnapshotBytesNonEmpty` — generate code for a one-component
fixture, link the resulting `snapshotBytes` against a hand-built Model,
assert non-empty output and a sensible mime type.

## Sequencing

1. **`pkg/go/testagent`: snapshot intrinsic + RegisterSnapshot** —
   independent of any platform; lays the contract.
2. **Bubbletea**: emit `snapshotBytes` + `RegisterSnapshot` call.
3. **Fyne**: same. Verify `fyne.io/fyne/v2/test` import boundaries.
4. **Gtk4 cutover**: delete `runtests.go`/`runtests_js.go`, emit
   `testagent_main.go` + `agent_main.go` + `snapshotBytes` +
   `buildWidgets` helper. Wire up `TestLauncher` probe (skip when
   gtk4 libs missing).
5. **Fixtures**: three `test_*_snapshot.txt` scripts + the per-platform
   unit tests.

## Out of scope (tracked elsewhere)

- Plan 3: Kotlin testagent + android (adb-forwarded TCP).
- Plan 4: JS testagent + html (WebSocket from browser).
- Plan 5: Fixture backfill for 22 untested stdlib components.
- Snapshot pixel-tolerance / SSIM perceptual diff (today's bytes-equal
  diff suffices; richer diff when a fixture demands).
- Continuous mainloop integration for full event-pipeline coverage
  (rejected per brainstorm: tests stay white-box).

## Risks

- **Gtk4 thread-locking quirks.** `runtime.LockOSThread` + cgo + signal
  handlers historically tricky. The per-snapshot teardown should be
  fully clean (release all GTK handles, unref the GtkApplication)
  before the next snapshot to avoid leaked state. Mitigation: per-test
  process isolation is an escape hatch (one binary launch per test
  group) — Plan 1's launchOneGroup already does this for fyne/bubbletea
  too.
- **Fyne offscreen rendering availability.** `fyne.io/fyne/v2/test`'s
  WindowToImage works on every backend, but the broader app machinery
  may pull in OpenGL bindings on some platforms. Verify on Linux + CI
  before committing the plan; if it fails, fall back to a "test mode
  detector" that emits `snapshotBytes` returning an error so the
  fixture skips cleanly.
- **Generated `snapshotBytes` complexity for non-trivial Models.**
  The codegen needs a `buildView`/`buildWidgets` helper that constructs
  the visual tree from current Model state. For bubbletea that's `View()`
  (already exists). For fyne/gtk4 the production code path already
  emits widget construction; reuse the same emitter under `--test`
  with the entry point exposed as a callable.
