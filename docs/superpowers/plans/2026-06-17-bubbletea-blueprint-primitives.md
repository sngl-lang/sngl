# Bubbletea Blueprint Primitives Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace bubbletea's name-keyed stdlib-widget codegen with three inlined platform primitives (`Layout`/`Styled`/`Widget`) described by user-facing blueprint records, decoupling all six codegen subsystems from stdlib component names and enabling real Bubbles widgets.

**Architecture:** Stdlib components are rewritten in `bubbletea.sngl` to the new-form `component sngl.X { platform bubbletea { <primitive(...)> } }` so the checker installs their bodies (`PlatformBodies`) and `passInlinePure` inlines them. Codegen then sees only `Layout`/`Styled`/`Widget` primitive nodes carrying blueprint-record props, which a generic engine reads. Migration is component-by-component (converted ones inline via the pure path; unconverted ones keep the old name switch) until all are converted, then the strict `NoStdlibWrappers` cap is enabled and the name switch deleted.

**Tech Stack:** Go; SNGL compiler (`internal/checker`, `internal/lower`, `codegen/platform/bubbletea`); Charm Bubble Tea / Bubbles / Lip Gloss; spec at `docs/superpowers/specs/2026-06-14-bubbletea-blueprint-primitives-design.md`.

**Conventions:** Work on `main`. `go tool verify` runs the suite + formatters — commit any formatter changes. Behavior changes are intended; the bubbletea snapshot goldens are regenerated deliberately and reviewed, not preserved byte-for-byte. Use `tmp/gen-matrix.sh <dir>` to diff generated example codegen for inspection (not a pass/fail gate). Commit message footer: `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.

---

## File Structure

- `internal/checker/stdlib.go` — **modify** (`checkPendingExtensions`): Phase 0 param-binding fix.
- `codegen/platform/bubbletea/bubbletea.sngl` — **modify**: blueprint record type decls + `JoinDir` enum; rewrite every `component sngl.X()` to new-form blueprint bodies.
- `codegen/platform/bubbletea/blueprint.go` — **create**: Go structs mirroring the blueprint records + extractors that read them off an inlined primitive `*ir.NodeInst`'s props.
- `codegen/platform/bubbletea/view_ir.go` — **modify**: blueprint-driven rendering of `Layout`/`Styled`/`Widget`; eventually delete `renderStdlibComponent` + dead `expandStdlibComponent`/`propVals`/`slotChildren`.
- `codegen/platform/bubbletea/compiler_ir.go` — **modify**: `analyzeIR` (Widget field allocation), `emitIR`/`emitIRUpdate`/Init (blueprint-driven init/update/bind-sync/event routing).
- `internal/lower/focus_order.go` — **modify**: read `Focus` off primitives (generalize from name/`focusable`-prop coupling where needed).
- `codegen/platform/bubbletea/bubbletea.go` — **modify** (final): set `f.StdlibWrappers = false`.
- `codegen/platform/bubbletea/blueprint_test.go` — **create**: unit tests for blueprint extraction.

---

## Phase 0 — Param binding through new-form platform bodies

Goal: a new-form bubbletea component whose body references a stdlib prop (e.g. `Styled(content=value)`) renders the caller's argument, not empty. This unblocks everything; land and verify it alone first.

### Task 0.1: Reproduce the binding loss with a checker/lower test

**Files:**
- Test: `codegen/platform/bubbletea/blueprint_test.go` (Create)

- [ ] **Step 1: Write the failing test**

A test that compiles a tiny SNGL doc using a new-form `sngl.text` body and asserts the generated bubbletea source contains the caller's value. Use the existing bubbletea test harness pattern (see `component_test.go` for how it builds a package and calls `CompileIR`). The fixture: a temporary platform package override is not needed — instead drive a SNGL source that calls `text(value="HELLO")` and assert the output contains `"HELLO"`.

```go
func TestNewFormTextBindsValue(t *testing.T) {
	src := `output { go { bubbletea } }
component main {
    text(value="HELLO")
}`
	out := compileBubbletea(t, src) // helper: parse→check→optimize→lower(platform=bubbletea)→CompileIR; see component_test.go
	if !strings.Contains(out, `"HELLO"`) {
		t.Fatalf("generated source missing bound value; got:\n%s", out)
	}
}
```

If `component_test.go` lacks a reusable `compileBubbletea` helper, add one in the test file mirroring its existing setup.

- [ ] **Step 2: Temporarily convert `sngl.text` to new form to trigger the path**

In `bubbletea.sngl`, change only `sngl.text` to:

```sngl
component sngl.text {
    platform bubbletea {
        Styled(content=value) {}
    }
}
```

(Leave all other components legacy for now.)

- [ ] **Step 3: Run the test, confirm it fails**

Run: `go test ./codegen/platform/bubbletea/ -run TestNewFormTextBindsValue -v`
Expected: FAIL — output contains `Render(fmt.Sprint(""))` / `Render("")`, not `"HELLO"` (the probe-2 symptom).

### Task 0.2: Root-cause and fix the param binding

**Files:**
- Modify: `internal/checker/stdlib.go` (`checkPendingExtensions`, ~685-701) and/or `internal/lower/inline_pure.go` (`substitute`/`substituteParams`)

- [ ] **Step 1: Diagnose** — determine whether the `value` Ident in the checked platform body (a) fails to resolve to the `sngl.text` `value` prop during `checkComponentBody` (scope issue in `checkPendingExtensions`), or (b) resolves but its Sym isn't a Param so `substituteParams` skips it, or (c) the callsite arg isn't in the bindings map. Add a temporary `fmt.Fprintf(os.Stderr, ...)` in `substitute`'s bindings loop and in `substituteParams` to observe; remove after.

- [ ] **Step 2: Fix at the root cause.** Most likely: ensure `checkComponentBody` for a pending extension brings the stdlib component's `Props` into scope so body identifiers bind to params (so the inliner's name-based `substituteParams` matches). Implement the minimal fix at the identified site.

- [ ] **Step 3: Run the test, confirm it passes**

Run: `go test ./codegen/platform/bubbletea/ -run TestNewFormTextBindsValue -v`
Expected: PASS — output contains `"HELLO"`.

- [ ] **Step 4: Full suite + commit**

Run: `go tool verify` → expect 0 failures (html/fyne/gtk4 already use new-form; the fix must not regress them).
Then revert the temporary `sngl.text` change from Task 0.1 Step 2 (keep it legacy for now — it re-lands in Phase 3):

```bash
git checkout codegen/platform/bubbletea/bubbletea.sngl
git add internal/checker/stdlib.go codegen/platform/bubbletea/blueprint_test.go
git commit -m "fix(checker): bind stdlib props in new-form platform bodies (bubbletea #3 phase 0)"
```

---

## Phase 1 — Blueprint record types + extractor

Goal: the SNGL-side record vocabulary exists and a Go extractor reads it off an inlined primitive node. No rendering wired yet.

### Task 1.1: Declare blueprint records + `JoinDir` enum in bubbletea.sngl

**Files:**
- Modify: `codegen/platform/bubbletea/bubbletea.sngl` (add decls near the top, after `struct Options`)

- [ ] **Step 1: Add the decls**

```sngl
enum JoinDir { vertical, horizontal }

struct Model {
    type string
    new string
    view string
    update string
    init string
    pkg string
}

struct Focus { enabled bool }

struct Bind {
    prop string
    get string
}

struct Event {
    on string
    key string
}
```

- [ ] **Step 2: Verify the package still parses**

Run: `go test ./codegen/platform/bubbletea/ -run TestPackageParses -v` (add a trivial test that `Package()` returns a doc without panic if none exists; bubbletea's `init()` already panics on parse error, so `go build ./codegen/platform/bubbletea/` failing is also a signal).
Expected: builds/parses clean.

- [ ] **Step 3: Commit**

```bash
git add codegen/platform/bubbletea/bubbletea.sngl
git commit -m "feat(bubbletea): blueprint record types + JoinDir enum (#3 phase 1)"
```

### Task 1.2: Go extractor for blueprint records

**Files:**
- Create: `codegen/platform/bubbletea/blueprint.go`
- Test: `codegen/platform/bubbletea/blueprint_test.go` (extend)

- [ ] **Step 1: Write failing tests for extraction**

Tests that, given an `*ir.NodeInst` with a `model=Model{...}` / `join=` / `content=` prop (constructed via the test harness by compiling a new-form body), the extractor returns the right Go struct. Cover: `Layout` (join dir), `Styled` (content + focus), `Widget` (model fields + binds + events).

```go
func TestExtractLayout(t *testing.T) {
	n := primitiveNode(t, `Layout(join=JoinDir.vertical) {}`)
	bp := extractBlueprint(n)
	if bp.Kind != bpLayout || bp.Join != joinVertical {
		t.Fatalf("got %+v", bp)
	}
}
```

(`primitiveNode` helper compiles a one-node new-form body and returns the inlined `*ir.NodeInst`.)

- [ ] **Step 2: Run, confirm fail** — `go test ./codegen/platform/bubbletea/ -run TestExtract -v` → FAIL (undefined `extractBlueprint`).

- [ ] **Step 3: Implement `blueprint.go`**

Define Go structs `blueprint{Kind; Join; Content; Focus; Model; Binds; Events; ...}`, kind constants (`bpLayout|bpStyled|bpWidget`), and `extractBlueprint(n *ir.NodeInst) blueprint` which reads node props via `codegen.NodeProp` + the existing struct-lit/literal helpers (`codegen.IRLiteralString`, `ir.StructLit` field reads). Kind is decided structurally: has `model` → Widget; has `join` → Layout; else Styled. Mirror the shape of `fyne/blueprint.go`'s extractors.

- [ ] **Step 4: Run, confirm pass** — `go test ./codegen/platform/bubbletea/ -run TestExtract -v` → PASS.

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/bubbletea/blueprint.go codegen/platform/bubbletea/blueprint_test.go
git commit -m "feat(bubbletea): blueprint extractor reads records off inlined nodes (#3 phase 1)"
```

---

## Phase 2 — Blueprint-driven subsystems (behind the name switch)

Goal: implement the generic engine for each subsystem so a primitive node renders correctly, while the legacy name switch still handles unconverted components. Each task converts ONE representative component and proves its subsystem end-to-end.

> Each Phase-2/3 task follows the same loop: (1) write a generated-output assertion test for the target component; (2) convert that component in `bubbletea.sngl` to new form; (3) route the inlined primitive through the blueprint engine in the relevant subsystem(s); (4) `go test` the bubbletea suite incl. snapshots — regenerate goldens deliberately if behavior changed intentionally; (5) commit. Full per-step code is given per task.

### Task 2.1: `Layout` rendering (vbox/hbox)

**Files:**
- Modify: `view_ir.go` (`renderNode`/`renderRawTerminal` → blueprint-aware), `bubbletea.sngl` (vbox, hbox, stack, scroll + horizontal: tabs, splitview, menubar, toolbar)

- [ ] **Step 1: Failing test** — assert generated `vbox{text...}` produces `lipgloss.JoinVertical` and `hbox` produces `lipgloss.JoinHorizontal`, via the new-form path (component converted in this task).
- [ ] **Step 2: Convert the layout components** in `bubbletea.sngl` to new form with `Layout(join=JoinDir.vertical|horizontal){ slot }`.
- [ ] **Step 3: Route** — in `renderNode`, when `extractBlueprint(n).Kind == bpLayout`, emit the join (reuse the existing join logic from `renderRawTerminal`). Set `vc.vertical` per join dir.
- [ ] **Step 4: Test + snapshots** — `go test ./codegen/platform/bubbletea/ -v`; regenerate goldens if changed; verify `examples/showcase` bubbletea still compiles (`tmp/gen-matrix.sh` diff is layout-only).
- [ ] **Step 5: Commit** `feat(bubbletea): Layout primitive via blueprint (#3)`.

### Task 2.2: `Styled` rendering (text + styled-content family)

**Files:** `view_ir.go`, `bubbletea.sngl` (text, badge, link, image, divider, avatar, spacer, datepicker)

- [ ] **Step 1: Failing test** — `text(value="HI")` → `lipgloss...Render(fmt.Sprint("HI"))` via new form.
- [ ] **Step 2: Convert** those components to `Styled(content=<prop>)`.
- [ ] **Step 3: Route** `bpStyled` (no focus) → styled content render (reuse existing styled path).
- [ ] **Step 4: Test + snapshots.**
- [ ] **Step 5: Commit** `feat(bubbletea): Styled primitive via blueprint (#3)`.

### Task 2.3: `Styled` with focus + events (button, checkbox, toggle, radio, chip, select)

**Files:** `view_ir.go`, `compiler_ir.go` (`emitIRButtonHandlers` → event-record driven), `internal/lower/focus_order.go` (read `Focus`), `bubbletea.sngl`

- [ ] **Step 1: Failing test** — `button(text="OK", @click { count = count + 1 })` generates a focusable styled node with an `enter`-key handler arm running the click body; assert the generated Update contains the focus-gated key case and the handler body.
- [ ] **Step 2: Convert** button/checkbox/etc. to `Styled(content=<prop>, focus=Focus{enabled=true}, events=[Event{on="click", key="enter"}])` (checkbox: `on="change", key="space"`).
- [ ] **Step 3: Generalize focus + events** — make `passFocusOrder`'s focusable detection and the Update event-routing read `Focus.enabled` and `Event{on,key}` off the primitive's blueprint instead of stdlib names. Preserve `__focusID`/loop-cursor behavior.
- [ ] **Step 4: Test + snapshots** — focus/activation behavior changes are intended; review regenerated goldens.
- [ ] **Step 5: Commit** `feat(bubbletea): Styled focus+events via blueprint (#3)`.

### Task 2.4: `Widget` — textinput (input)

**Files:** `compiler_ir.go` (`analyzeIR` field alloc, Init, Update message-forward + bind-sync — all blueprint-driven), `view_ir.go`, `bubbletea.sngl` (input)

- [ ] **Step 1: Failing test** — `input(placeholder="name")` with a two-way bind generates: a `widget0 textinput.Model` field, `textinput.New()` init, `textinput.Blink` in Init(), focus-gated `.Update(msg)` forwarding, and bind-target sync from `.Value()`.
- [ ] **Step 2: Convert** `sngl.input` to the `Widget(model=Model{...}, focus=Focus{enabled=true}, binds=[Bind{prop=value, get=".Value()"}], placeholder=placeholder)` body.
- [ ] **Step 3: Implement blueprint-driven Widget subsystems** — `analyzeIR` scans inlined `Widget` nodes (kind via `extractBlueprint`) to allocate `widgetN <model.type>` fields, collect init cmds, binds, and the import (`model.pkg`); `emitIR` Init emits `model.init`; `emitIRUpdate` forwards `model.update` gated on focus and syncs `binds`; `view` emits `widgetN<model.view>`. Remove the `n.Name=="input"` special-casing for converted components.
- [ ] **Step 4: Test + snapshots** — `go test ./codegen/platform/bubbletea/ -v`; verify `examples/hello` (uses input) compiles and the textinput works.
- [ ] **Step 5: Commit** `feat(bubbletea): Widget primitive (textinput) via blueprint (#3)`.

### Task 2.5: `Widget` — textarea + remaining layout-ish (modal, drawer, tooltip, popover, card, menu, tree, table, radio container)

**Files:** `bubbletea.sngl`, `view_ir.go`, `compiler_ir.go`

- [ ] **Step 1: Failing test** — `textarea` allocates a `textarea.Model` widget; `modal(open=flag){...}` renders inside `if flag`.
- [ ] **Step 2: Convert** textarea → `Widget(model=Model{type="textarea.Model", new="textarea.New()", view=".View()", update=".Update(msg)", pkg="charm.land/bubbles/v2/textarea"}, focus=Focus{enabled=true})`. modal/drawer/etc → `Layout` (vertical) wrapped in the existing `if open { … }` body form. table/tree/menu → `Layout(vertical)` for now (Bubbles list/table wrapping is a follow-on blueprint record, noted below).
- [ ] **Step 3: Route** — no new engine code beyond what 2.1–2.4 built; these are blueprint records over the existing primitives.
- [ ] **Step 4: Test + snapshots.**
- [ ] **Step 5: Commit** `feat(bubbletea): convert remaining components to blueprints (#3)`.

---

## Phase 3 — Enable strict mode, delete the name switch

### Task 3.1: Convert any stragglers + enable `NoStdlibWrappers`

**Files:** `bubbletea.go`, `bubbletea.sngl`

- [ ] **Step 1:** Verify every `component sngl.X` in `bubbletea.sngl` is new-form (grep for `component sngl.\w\+(` parens form → expect none).
- [ ] **Step 2:** Set `f.StdlibWrappers = false` in `bubbletea.go` `Capabilities()`.
- [ ] **Step 3:** Run `go tool verify`. Strict mode errors on any impure/uninlined wrapper — fix by converting/making pure. Expected end state: 0 failures.
- [ ] **Step 4: Commit** `feat(bubbletea): enable NoStdlibWrappers — all widgets inline (#3)`.

### Task 3.2: Delete dead code

**Files:** `view_ir.go`

- [ ] **Step 1:** Confirm `renderStdlibComponent` is unreachable (instrument with a panic, run the suite, see it never fires; then remove the instrumentation).
- [ ] **Step 2:** Delete `renderStdlibComponent`, `expandStdlibComponent`, the `propVals`/`slotChildren` fields and their now-dead branches (per the audit's noted half-finished prior attempt). Simplify `resolveProp` if it becomes a thin wrapper.
- [ ] **Step 3:** `go build ./...` clean; `go tool verify` → 0 failures.
- [ ] **Step 4: Commit** `refactor(bubbletea): delete name-keyed renderStdlibComponent + dead inline machinery (#3)`.

### Task 3.3: Update the audit + spec status

**Files:** `docs/superpowers/audit/lowering-migration.md` (#3 Status), `docs/superpowers/specs/2026-06-14-bubbletea-blueprint-primitives-design.md` (status)

- [ ] **Step 1:** Mark #3 RESOLVED for bubbletea (android remains, per spec); note the blueprint-record public API and the param-binding fix.
- [ ] **Step 2: Commit** `docs(audit): #3 resolved for bubbletea (blueprint primitives)`.

---

## Self-review notes

- **Spec coverage:** primitives (Layout/Styled/Widget) → Phase 2; blueprint records + JoinDir enum + user-extensibility (public types in bubbletea.sngl) → Task 1.1; six subsystems → Tasks 2.1–2.4; Phase 0 param binding → Phase 0; migration component-by-component → Phase 2 ordering; cap + delete → Phase 3; verification via verify + deliberate snapshot regeneration → stated in header and each task. Bubbles wrapping (input/textarea now; list/table/spinner/progress as follow-on blueprint records) → Tasks 2.4–2.5 + noted as follow-on.
- **Known investigation task:** Task 0.2 (root-cause the binding) is inherently diagnostic; its success criterion is the concrete Task 0.1 test passing.
- **Follow-on (out of scope, by design):** richer Bubbles widgets for list/table/menu/tree/spinner/progress (add a blueprint record each — no new engine code); android `renderStdlibComposable` (same approach, separate plan).
