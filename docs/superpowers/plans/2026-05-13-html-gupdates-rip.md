# HTML `g.updates` Rip Implementation Plan (Plan D continuation)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rip the html platform's `g.updates` registry and route every
reactive/structural/initial-sync DOM-write through the
`htmlTranslator` + `WalkLowered` + `JsIRContext` path. End state: one
dispatch path for html, matching gtk4/fyne. `grep "g\\.updates"
codegen/platform/html/` returns empty.

**Why split from Plan D:** Plan D Tasks 10-13 underestimated the
depth. `g.updates` is load-bearing for: initial-sync seeds, structural
updaters not in `passReactivity` (for-loop list functions, if/else
updaters, class-list, attr updaters), `MutationModel` optimization
(dead-code elim + dispatch sharing), and scope/rename plumbing that
lives in `g.scope`/`g.dataRenames` (not yet in `JsIRContext`). This
needs a phased migration across multiple sessions.

**Tech Stack:** Go, `codegen/platform/html/html.go`,
`codegen/platform/html/intrinsic_translator.go`,
`codegen/lang/javascript/ircontext.go`,
`codegen/model.go` (MutationModel).

**Predecessors:** Plans A-H on `main`; Plan D Tasks 1-9 + 14-17.

**Successor:** None — closes Plan D fully.

**Ground-truth fixtures:**
- `cmd/sngl/testdata/compile_html_initial_sync.txt`
- `cmd/sngl/testdata/compile_html_setter_callback.txt`
- `cmd/sngl/testdata/compile_html_renamed_event_param.txt`
- `cmd/sngl/testdata/compile_html_reactive_for.txt`

These currently lock the LEGACY g.updates output. Each phase that
changes JS output structure rewrites the affected fixtures in the
same commit.

---

## Phase 1: Strip redundant init-only dispatch

Bound scope, no fixture changes.

### Task 1: Skip `findAffectedUpdaters` when all updaters are init-only

`findAffectedUpdaters` is invoked from `emitHandlers`, `emitTimers`,
`emitSetter` to find dependent `$u_*` calls. When NoReactivity is set
(the default in current state) all returned updaters are
`initOnly=true` — meaning they fire once at load, not from handlers.
Calling them from handlers is dead dispatch.

- [x] **Step 1: Locate call sites**

```bash
grep -n "findAffectedUpdaters\|initOnly" codegen/platform/html/html.go | head -30
```

- [x] **Step 2: Add early-return on all-init-only**

Already done by commit `4ad84957` (2026-05-09, pre-plan):
`findAffectedUpdaters` at `html.go:3091` skips `u.initOnly`. No IIFE
wrapper exists at handler/timer/setter emission sites — when the
filtered slice is empty the dispatch loop iterates zero times, so no
extra code is emitted.

- [x] **Step 3: Verify**

`go test ./codegen/platform/html/... ./cmd/sngl/` — green. No output
change.

- [x] **Step 4: Commit**

No code change needed; phase complete at plan-time. Documented here
2026-05-15.

---

## Phase 2: Collapse per-prop `$u_*` init functions

Bounded fixture churn (4 fixtures, mechanical rewrite).

### Task 2: Emit single `__sngl_init()` for initial-sync

Today each reactive prop emits a `$u__nN_text()` (or similar)
function whose body is "compute value; write to DOM". The init phase
calls them sequentially. Collapse into one `__sngl_init()` whose body
is the concatenated writes.

- [x] **Step 1: Locate init emission**

`codegen/platform/html/html.go:2659` (updater function emission) +
`:2672` (Initial sync block).

- [x] **Step 2: Replace per-prop functions with inline writes**

Updater-function loop now skips `u.initOnly` entries. Replaced
`// Initial sync` block with `function __sngl_init() { ... }` whose
body inlines each init-only updater body and calls each non-init
updater function. Preview `__sngl_updates` filter also skips
init-only entries.

- [x] **Step 3: Rewrite fixtures**

`compile_html_initial_sync.txt` + `compile_html_setter_callback.txt`
rewritten to expect `function __sngl_init` and assert no
`$u__nN_text`. The other two ground-truth fixtures
(`renamed_event_param`, `reactive_for`) did not reference `$u_*`
funcs and needed no edits.

- [x] **Step 4: Verify**

`go test ./codegen/platform/html/... ./cmd/sngl/` green.
`hello-i18n` compile inspected: single `__sngl_init()` with all
inlined `__nN.textContent = i18n.getTranslator().tr(...)` writes.
Pre-existing `TestDocSNGLFormat` failures in
`docs/superpowers/plans/2026-05-13-component-extensions.md` are
unrelated (verified by stashing).

- [x] **Step 5: Commit**

---

## Phase 3: Route init+handler bodies through htmlTranslator

Substantial. JsIRContext needs scope/rename plumbing.

### Pre-migration gap (discovered 2026-05-15)

Attempted Task 4 (migrate `addClickHandler` to `WalkLowered + jc.EvalStmt`)
and reverted. `translateHandlerStmt` + the legacy JS path carry
html-specific features that `JsIRContext` doesn't replicate today.
Migrating handlers naively breaks the following ground-truth tests:

- **`compile_js_import`** — bundled native-pkg refs. The legacy path
  routes `js://` import names through `BundledNativePkgs` to emit
  `__sngl_n_lib.fn(...)` references that the post-bundle pipeline
  rewrites. `jc.evalCall` emits the bare name.
- **`funcvar_mixed_promotes`, `funcvar_stored_async`,
  `funcvar_struct_field_async`** — auto-await on funcvar slots whose
  points-to color is Async. The legacy path inserts `await` based on
  `g.pts()`; `JsIRContext` lacks the slot-color hook.

Before Task 4 can land, these features must move into the new path:

- [x] **Step 0a: Bundled-native-pkg refs in `JsIRContext`**
  `ExprCtx` gained `BundledNativePkgs` + `NativeImports`; `JsIRContext`'s
  `evalCall` emits `__sngl_n_<alias>.<name>(...)` and registers the
  binding for native-scheme calls.
- [x] **Step 0b: Funcvar-await in `JsIRContext`**
  `evalCall` uses `Pkg.PointsTo.SlotColor` to prefix `await` on Async
  funcvars (with conservative any-async-candidate fallback). Plain
  calls flagged `IsAsync` also get `await`.

Both gaps closed by commit `0f14607`.

### Status (2026-05-15)

Phase 3 Tasks 3-5 shipped at `0f14607`. All
handler/timer/setter body emission flows through
`g.translateBlockJC` → `WalkLowered` + `htmlTranslator` +
`JsIRContext`. `translateHandlerStmt` and `domWriteFor` remain only
because `emitJSFunc` (general user-func emission) still uses them —
that lives outside the g.updates scope and is handled in Phase 5
cleanup.

### Task 3: Migrate `g.scope`/`g.dataRenames` into reusable shape

`g.scope.Renames` and `g.dataRenames` currently live on the html
`compilation` struct. `JsIRContext` (the JS language renderer) doesn't
see them. To route handler bodies through `jc.EvalStmt`, the renames
must be accessible from the JS context.

- [ ] **Step 1: Decide ownership**

Two options:
- Add `Renames`, `DataRenames` fields to `codegen.CodegenCtx` (the
  shared codegen-time context) and have `JsIRContext` read them via
  the context.
- Add a setter on `JsIRContext` (e.g. `jc.SetRenames(map)`) called
  from the html platform before each `EvalStmt`.

Pick whichever fits existing patterns. Read
`codegen/lang/javascript/ircontext.go` for the current shape.

- [ ] **Step 2: Wire renames through**

Apply renames when `JsIRContext` emits an `*ir.Ident` whose
`Sym.Name` matches a rename key.

- [ ] **Step 3: Audit other scope-dependent emission paths**

`g.exprToJS` and `g.translateHandlerStmt` apply additional rewrites:
EventVar → `e.target`, async funcvar wrapping, etc. Decide which of
these live in JsIRContext vs htmlTranslator.

### Task 4: Replace handler-body emission with WalkLowered

- [ ] **Step 1: Find emission sites**

```bash
grep -n "addClickHandler\|addInputHandler\|addChangeHandler\|emitHandlers" codegen/platform/html/html.go
```

- [ ] **Step 2: Swap body emission**

Each handler body emission currently iterates `h.Func.Block` and
applies `g.translateHandlerStmt`. Replace with:

```go
fragments := codegen.WalkLowered(ctx, h.Func.Block, g.translator)
for _, stmt := range fragments {
    jc.EvalStmt(buf, stmt)
}
```

- [ ] **Step 3: Verify behavior parity**

Run the 4 ground-truth fixtures. Output should be structurally
equivalent. Rewrite any text differences into the fixtures, but
verify in a browser (via go-rod CDP) that hello-i18n still behaves
identically before committing.

- [ ] **Step 4: Commit**

`html: route handler bodies through htmlTranslator + WalkLowered`

### Task 5: Replace timer/setter emission similarly

- [ ] **Step 1: Migrate `emitTimers`**

Same pattern — replace body iteration with `WalkLowered` + `jc.EvalStmt`.

- [ ] **Step 2: Migrate `emitSetter`**

Setters are emitted per bidirectional binding. Route through
translator.

- [ ] **Step 3: Verify + commit**

---

## Phase 4: Replace structural updaters with IR fragments

The biggest phase. Each `add*Updater` becomes IR emission through the
translator.

### Task 6: Replace `addForUpdater` with translator path

Currently `addForUpdater` emits `$u__N_list` functions that diff the
old list against the new and reconcile children. Replace with the
slot/renderSlot pattern that `passReactivity` already uses for
in-scope reactive `for` loops. Extend the pass coverage to include
for-loops outside the existing synthesis range — e.g. for_map, the
checkbox list path.

- [ ] **Step 1: Identify the gap**

Find which `for` loops today get `addForUpdater` (legacy) vs
`__renderSlotN` (Plan A). Likely: the existing path triggers when the
for is at the top level of a reactive context; the legacy path
triggers for nested or non-slot-eligible cases.

- [ ] **Step 2: Extend `passReactivity` to cover all cases**

Or, if extending the pass is too invasive, have `addForUpdater` emit
the equivalent IR (LocalVar __slot, FuncDef __renderSlot, etc.) and
route through translator instead of inlining its own JS.

- [ ] **Step 3: Delete `addForUpdater`**

### Task 7: Replace `addIfUpdater` + `addElseUpdater`

Same pattern. Reactive `if` is already covered by Plan A for some
cases; legacy `addIfUpdater` covers the rest. Migrate.

### Task 8: Replace `addCheckboxListFunc`, `addClassListUpdater`, attr updaters

Each remaining `add*Updater` migrates to either an existing
passReactivity-handled shape or to a translator-emitted IR fragment.

### Task 9: Re-derive MutationModel optimization at IR level

The legacy registry feeds `codegen.OptimizeMutation` which does
dead-code elim across the updater set. With updaters gone, the same
optimization needs an IR-level equivalent, OR we accept a small
regression in output verbosity (measure before deciding).

- [ ] **Step 1: Measure**

Build hello-i18n on html with current code. Note JS bundle size.
After migration, rebuild — measure delta. If <5% growth, accept and
skip the IR-level optimizer. If larger, implement an IR-level
equivalent.

- [ ] **Step 2: Document the decision**

Update this plan with the measurement and decision.

---

## Phase 5: Delete the registry

Cosmetic + cleanup.

### Task 10: Remove `g.updates` field and all helpers

- [ ] **Step 1: Delete code**

```
codegen/platform/html/html.go:
  - g.updates field
  - updateFunc struct
  - loweredID, nodeIsReactive
  - translateHandlerStmt, domWriteFor
  - all add*Updater helpers (addClickHandler stays; only data-emission helpers)
  - findAffectedUpdaters
  - the updater half of MutationModel (in codegen/model.go) if unused elsewhere
```

- [ ] **Step 2: Verify grep clean**

```bash
grep -rn "g\.updates\|updateFunc\|translateHandlerStmt\|domWriteFor\|findAffectedUpdaters" codegen/platform/html/
```

Must return no results in non-test files. Empty grep = rip done.

- [ ] **Step 3: Final test sweep**

`go test ./...` + `go tool verify` + `sngl run examples/hello-i18n
--platform html --lang js` browser-verified via CDP if available.

- [ ] **Step 4: Plan D + this plan handoff**

Mark Plan D fully complete in its doc. Update this plan with the
final state.

---

## Sessions estimate

- Phase 1: half session (mechanical, no fixture changes).
- Phase 2: 1 session (rewrite 4 fixtures, verify hello-i18n still
  works).
- Phase 3: 1-2 sessions (scope plumbing is the hard part).
- Phase 4: 2-3 sessions (each structural updater is a small
  migration; MutationModel optimization decision lives here).
- Phase 5: half session (delete + verify).

Total: 5-7 sessions across the plan. Each phase is independently
shippable; halt between phases is safe.

## Risks

- **Fixture drift during Phase 4** — each structural updater
  replacement may shift the 4 ground-truth fixtures + adjacent
  tests (`compile_html_setter_callback`, etc.). Re-verify hello-i18n
  in a browser after every fixture rewrite to catch behavior
  regressions early.
- **MutationModel optimization loss** — if we skip the IR-level
  rebuild, the resulting JS may be larger. Measure Phase 4 Task 9
  before committing to the deletion.
- **JsIRContext bloat** — adding renames + EventVar logic risks
  making the JS context html-specific. Keep the additions generic
  (other JS-emitting platforms might reuse them).
