# Cross-Platform Headless Test Runner

**Date:** 2026-05-09
**Status:** Design

## Goal

Run SNGL test functions on every code-generating platform headlessly as part
of `go tool verify`. Tests written once should compile and execute on each
target platform that supports test generation, drive the resulting components
without a display or device, and report structured `TestResult` data back to
the runner.

Today only the `html` platform implements `codegen.TestRunner`, via Chrome
DevTools Protocol. `bubbletea` has a `testgen_ir.go` scaffold but no runner.
`fyne`, `gtk4`, and `android` have neither.

## Non-Goals

- Visual / pixel snapshot assertions (separate `Snapshotter` interface
  already covers screenshots).
- Cross-target differential testing (running the same test against two
  platforms and comparing results).
- A uniform binary protocol where every platform emits a self-contained
  test executable. Each platform keeps its own driver shape.

## Architecture

### Per-platform `RunTests` (status quo extended)

The existing `codegen.TestRunner` interface is the only entry point:

```go
type TestRunner interface {
    RunTests(pkg *ir.Package, lang LangTranslator) ([]*TestResult, error)
}
```

Each gen-capable platform implements it with whatever driver shape fits the
target — out-of-process (CDP for html), shelled `go test` over a temp module
(fyne, gtk4), or in-process (bubbletea via teatest). No new uniform binary
protocol.

### Shared harness: `codegen/testharness/`

New package factored out of today's `html/testing.go`:

- `Probe(name string) Available` — system-dep checks per platform (chrome,
  weston/Xvfb, fyne build OK, ANDROID_HOME). Returns reason string if
  unavailable. Result cached per verify run.
- `Group(testFuncs []*ast.FuncDef) []TestGroup` — groups tests by component
  from second-param type (lifted from `html/testing.go`).
- `Promote(doc *ast.Document, compName string) *ast.Document` — promotes a
  single component to a top-level surface for isolated rendering. Lifted
  from `html.PromoteComponent`.
- `Settle(platform string) error` — post-action hook each platform overrides
  to drain main loop / microtasks before the next `t.assert` runs.
- Canonical key-name set (`Enter`, `Tab`, `Escape`, `ArrowUp/Down/Left/Right`,
  `Backspace`, `Delete`, `Home`, `End`, `PageUp`, `PageDown`, letters,
  digits, `Space`). Each platform maps to native key codes.

### UI-primitives test API

New stdlib methods on `Test` declared in `lib/testing.sngl`:

| Method | Semantics |
|---|---|
| `t.click(node)` | Synthesize a click on a `#id`-bound node. |
| `t.type(node, s string)` | Focus node, type string char-by-char. |
| `t.key(name string)` | Send a named key event to the focused element. |
| `t.focus(node)` / `t.blur(node)` | Move keyboard focus. |
| `t.wait(predicate, timeout_ms int)` | Pump platform main loop until predicate true or timeout. |

`node` type is the existing component-child handle (`c.lbl`, `c.foot[i]`).
The checker resolves these to platform-specific element references; each
platform's runner translates the lowered call into native event injection.

Existing model-state surface (`t.assert`, `t.must`, property reads/writes)
stays unchanged. UI primitives are additive.

## Per-platform driver design

### html (Phase 1)

Migrate today's `html/testing.go` onto the shared harness. UI primitives lower
to CDP `Input.dispatchKeyEvent`, `Input.dispatchMouseEvent`, and
`Runtime.evaluate` against `#id`. `Settle` = `awaitMicrotasks` JS bridge
already present.

Probe: `chrome` or `chromium` binary on PATH.

### fyne (Phase 2)

`fyne.RunTests` mirrors html's pattern: scaffold a temp Go module, write the
generated fyne code plus a `*_test.go` harness, shell to `go test -json`,
parse results back into `TestResult`. The harness uses
`fyne.io/fyne/v2/test`:

- `t.click` → `test.Tap(widget)` after id-lookup walks the canvas tree.
- `t.type` → `test.Type(input, s)`.
- `t.key` → `test.TypeKey(input, fyne.KeyName)`.
- `t.focus` / `t.blur` → `canvas.Focus(...)` / `canvas.Unfocus()`.
- `Settle` → `widget.Refresh()` then drain `app.Driver()` events.

Probe: a smoke `go build` of a minimal fyne stub (catches missing
`libgl1-mesa-dev` / X11 headers on bare CI).

### bubbletea (Phase 3)

`bubbletea.RunTests` uses `github.com/charmbracelet/x/exp/teatest`:

- Create `tm := teatest.NewTestModel(t, model)`.
- `t.click(node)` → resolve `#id` to a grid rectangle by parsing the current
  `View()` ANSI string and tracking a per-render id-to-bounds map (planned
  via `testgen_ir.go`); send `tea.MouseMsg{Type: tea.MouseLeft, X, Y}`.
- `t.type(node, s)` → focus then `tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{...}})` per char.
- `t.key("Enter")` → `tm.Send(tea.KeyMsg{Type: tea.KeyEnter})`.
- `Settle` → `teatest.WaitFor(tm.Output(), …, teatest.WithDuration(...))`.

Probe: always available (pure Go).

### gtk4 (Phase 4)

`gtk4.RunTests` shells a temp Go module + `go test`. The harness uses
`gotk4`'s `Test*` bindings:

- `t.click` → `gtk_test_widget_click(widget, button, modifiers)`.
- `t.key` → `gtk_test_widget_send_key(widget, keyval, modifiers)`.
- `t.type` → loop over runes calling `send_key`.
- `Settle` → spin `g_main_context_iteration(NULL, FALSE)` until idle.

Display: spawn `weston --backend=headless-backend.so` per suite (preferred)
or `Xvfb :99 -screen 0 1024x768x24` as fallback. Set `WAYLAND_DISPLAY` /
`DISPLAY` for the child process. Tear down after the suite finishes.

Probe: (`weston` on PATH **or** `Xvfb` on PATH) AND `pkg-config --exists gtk4`.

### android (Phase 5)

`android.RunTests` writes a Compose source tree into a Gradle module, then
runs `./gradlew testDebugUnitTest`. Tests use Robolectric +
`androidx.compose.ui.test`:

- `t.click(node)` → `composeTestRule.onNodeWithTag("id").performClick()`.
- `t.type(node, s)` → `performTextInput(s)`.
- `t.key("Enter")` → `performKeyInput { keyDown(Key.Enter); keyUp(Key.Enter) }`.
- `Settle` → `composeTestRule.waitForIdle()`.

Probe: `ANDROID_HOME` set, `gradle` (or wrapper) reachable, JDK 17+ present.
Default path is Robolectric on the JVM — no emulator. Emulator-driven
instrumentation may come later behind an opt-in flag.

## Verify integration

`internal/cmd/verify` gains a final step after `go test`:

```
>>> sngl test --platform html     --language js
>>> sngl test --platform fyne     --language go
>>> sngl test --platform bubbletea --language go
>>> sngl test --platform gtk4     --language go
>>> sngl test --platform android  --language kotlin
```

For each invocation:

1. CLI calls `testharness.Probe(platform)`.
2. If unavailable: print `SKIP <platform>: <reason>` and exit 0.
3. Otherwise discover and run all `testdata/test_*.sngl` fixtures whose
   referenced components are buildable for the platform. Per-fixture skips
   surface as `SKIP <fixture>: <reason>` lines and do not fail the suite.
4. Per-test failures fail the platform run; verify aggregates a per-platform
   summary at the end.

A platform skip is not a verify failure. A test failure within a platform
that did run is.

No frontmatter directive for fixture targeting — keep `testdata/` flat. If a
fixture truly is platform-specific, gate it via existing capability checks
or an explicit `// PLATFORMS: html` directive (deferred until a real need
appears).

## Test fixtures

Two new fixtures land in Phase 0 to exercise the new API:

- `testdata/test_ui_click.sngl` — counter component, `t.click(c.btn)` then
  `t.assert(c.count == 1)`.
- `testdata/test_ui_input.sngl` — text input component, `t.type(c.field, "hi")`,
  `t.key("Enter")`, assert resulting model state.

Both run on every platform that reaches at least Phase 1.

## Phasing

| Phase | Scope |
|---|---|
| 0 | `codegen/testharness/`; UI primitive stdlib decls + checker lowering; canonical key set; two new fixtures. |
| 1 | Migrate html onto shared harness; CDP impls of UI primitives. |
| 2 | `fyne.RunTests` via temp-module + `go test` shelling, `fyne/v2/test`. |
| 3 | `bubbletea.RunTests` via teatest; finish `testgen_ir.go` and id-bounds map. |
| 4 | `gtk4.RunTests` via gotk4 test bindings + weston/Xvfb spawn. |
| 5 | `android.RunTests` via Robolectric + `androidx.compose.ui.test`. |
| 6 | Wire all five into `internal/cmd/verify` matrix step with probe-and-skip. |

Each phase is its own implementation plan. Phase 0 is a hard prerequisite;
Phases 1–5 are independent and may interleave.

## Risks

- **Key-name divergence.** Each platform's native key-name space is
  different. Mitigation: canonical map in the harness; deviations are bugs
  in the per-platform translator.
- **Reactivity timing.** State-write tests rely on platform reactivity to
  re-render before the next assert. Each `RunTests` must `Settle()` after
  every action.
- **fyne/gtk4 build deps on bare CI.** A clean Linux box without GL/X11
  headers fails to compile fyne. The probe must be cheap and accurate.
- **Android Robolectric class-load latency.** First test launch adds
  multi-second JVM warmup. Acceptable for verify; consider keeping a hot
  Gradle daemon between fixtures.
- **gtk4 weston headless backend availability.** Some distro packages omit
  `headless-backend.so`. Probe checks for the `.so`, not just the binary;
  fall back to Xvfb if absent.

## Open questions

- Should `t.wait` accept a CEL predicate or only well-known forms (e.g.
  "node X exists", "value X equals Y")? Defer until a real fixture needs it.
- Robolectric vs emulator for android long-term: Robolectric is the right
  default, but emulator gives real-device fidelity for animation/layout
  bugs. Add an opt-in path (Phase 5+) only if needed.
