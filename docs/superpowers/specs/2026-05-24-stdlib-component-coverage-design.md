# Stdlib component coverage — fixture backfill

Date: 2026-05-24

## Goal

Close the 22 stdlib component coverage gaps identified in the
test-deficiencies audit (`docs/superpowers/audit/test-deficiencies.md`
items 1-22), adding one `testdata/component_<name>.sngl` fixture per
gap. Fixtures use the now-working test infrastructure from Plans 1-4 —
no new runtime work; pure test authoring.

## Non-goals

- Other audit gaps. Items 23-32 (platform-asymmetric coverage), 33-46
  (untested checker error paths), 47-50 (edge cases), and the
  remaining audit findings are out of scope. Each can be its own
  follow-up backfill plan.
- Cross-platform verification as a Plan 5 gate. Fixtures pass via
  `sngl test --platform=none` (interp). Failures on GUI platforms
  surface real component-binding bugs and get filed separately, not
  treated as fixture-design issues.
- Snapshot or event-firing tests. State-binding assertions are the
  Plan 5 floor; richer test depth is a future-task per fixture when
  a specific bug demands it.

## High-level architecture

```
testdata/
    component_image.sngl       ← state-binding assertion via prop ↔ var
    component_scroll.sngl
    component_checkbox.sngl
    ...
    component_divider.sngl     ← declaration-only (no testable state)
```

Each fixture:
1. Declares a `component main` that uses the target stdlib component.
2. Binds at least one prop to a state var on `main`.
3. Has at least one `func test...(t Test, c main)` body that mutates
   the var and asserts the new value.

The interp testrunner from `codegen/platform/none/testrunner/` walks
the test body via the SNGL interpreter — it executes prop bindings,
reactivity, and assertions independent of any platform-specific
rendering. State assertions catch the SNGL-level binding contract;
they don't catch widget-rendering bugs (out of scope per non-goals).

## Per-component classification

Each fixture is one of:

- **B-tier (state-bindable)**: at least one prop binds to a SNGL var
  on `main`. The fixture mutates the var, asserts the new value.
- **A-tier (declaration-only)**: no straightforwardly testable state
  (e.g. `divider` has only `style`; `slot` is structural). The fixture
  validates parse + check + lower by existing in `testdata/`. No
  `func test...` body required.

| # | Component | Tier | Prop ↔ var | Test body sketch |
|---|---|---|---|---|
| 1 | image | B | `var src string` ↔ `src` | mutate src, assert |
| 2 | scroll | B | `var x float` ↔ `scrollX` | mutate, assert |
| 3 | spacer | A | — | declaration only |
| 4 | checkbox | B | `var on bool` ↔ `checked` | toggle, assert |
| 5 | radio | B | `var sel string` shared | reassign, assert |
| 6 | textarea | B | `var text string` ↔ `value` | mutate, assert |
| 7 | spinner | B | `var loading bool` ↔ `visible` | toggle, assert |
| 8 | tabs | B | `var idx int` ↔ `selected` | mutate, assert |
| 9 | link | A | — | declaration + href literal |
| 10 | divider | A | — | declaration only |
| 11 | modal | B | `var open bool` ↔ `open` | toggle, assert |
| 12 | drawer | B | `var open bool` ↔ `open` | toggle, assert |
| 13 | tooltip | B | `var msg string` ↔ `message` | mutate, assert |
| 14 | popover | B | `var visible bool` ↔ `visible` | toggle, assert |
| 15 | splitview | A | — | declaration only |
| 16 | table | B | `var rows list<T>` ↔ `rows` | push, assert length |
| 17 | tree | B | `var root dyn` ↔ `node` | reassign, assert |
| 18a | menu | B | `var sel string` ↔ `selected` | mutate, assert |
| 18b | menubar | A | — | declaration only |
| 18c | toolbar | A | — | declaration only |
| 19 | datepicker | B | `var d date` ↔ `value` | mutate, assert |
| 20 | chip | A | — | declaration + label binding |
| 21 | avatar | A | — | declaration + src/fallback |
| 22 | slot | A | — | declaration: parent uses slot |

Total: **24 fixtures** (18 B-tier + 6 A-tier; menu/menubar/toolbar
count as three separate per the audit's path naming).

The actual prop names depend on each component's signature in
`lib/components.sngl`. Each task's fixture writer reads the
component's declaration before writing the fixture and uses the real
prop names (`open`, `selected`, `value`, etc.) — the table above is
the design intent, not the exact prop wiring.

## Fixture conventions

Every fixture:

1. Lives at `testdata/component_<name>.sngl`.
2. Declares a `component main` that uses the target component.
3. Has zero or more `func test...(t Test, c main)` test bodies
   (B-tier: at least one; A-tier: zero is acceptable).
4. Asserts via `t.assert(...)`.
5. **No `output { ... }` block**. Fixtures use the default target so
   `sngl test --platform=none` runs them without per-fixture
   platform plumbing.
6. **No external imports**. Stdlib components only.

Header comment per fixture documents the component and the test
intent.

## Verification

After each fixture lands:

```bash
sngl test --platform=none testdata/component_<name>.sngl
```

Expected for B-tier: `PASS  ok  <N> tests, 0 failures`.
Expected for A-tier: `PASS  ok  0 tests, 0 failures` (no test bodies,
the existence of the file confirms parse+check).

Final sweep before closing the plan:

```bash
sngl test --platform=none testdata/
```

Should run every fixture in `testdata/` including the 24 new ones.
Total fixture count grows from N to N+24; pass count grows by however
many B-tier test bodies are added (typically 1-2 per fixture, so
~30-40 new test rows).

## Sequencing

24 tasks. Each task is one fixture:

1. Read the component's prop signature in `lib/components.sngl`.
2. Write `testdata/component_<name>.sngl` per the convention.
3. Run `sngl test --platform=none testdata/component_<name>.sngl`.
4. PASS → commit. FAIL → investigate (real component bug? fixture
   wrong?) → resolve and re-run.
5. Commit with a clear message identifying the component and tier.

Order: 1-22 from the audit (image first, slot last; menu/menubar/
toolbar as 18a/18b/18c in sequence). Order doesn't materially
matter; sequencing just keeps the task list readable.

Subagent-friendly: each task is 5-15 min including verification.

## Bug-handling protocol

A fixture run may surface a real component bug — e.g. binding doesn't
propagate, lowering panics, interp misbehaves. When this happens:

1. **File the bug** as a `TODO(component-<name>): <one-line>` comment
   in the fixture itself, and note it in the task's report.
2. **Trim the fixture** to the working subset (state assertion that
   passes; commented-out broken assertion).
3. **Commit the trimmed fixture**. The TODO survives in source as a
   tracked-debt marker.
4. **Continue to the next task**. The bug becomes a follow-up
   investigation, not a Plan 5 blocker.

Goal: Plan 5 lands 24 fixtures with at least declaration-level
coverage even when state assertions can't yet pass. Real component
bugs surface but don't block coverage.

## Risks

- **Interp testrunner gaps for unusual prop shapes.** Some components
  may use type aliases or generic types (`dyn`, `list<T>`) the interp
  has historically mishandled. Each fixture verifies through the
  interp; a gap shows up as fixture failure and gets handled per the
  bug-handling protocol.
- **Component signature drift since the audit.** The audit was written
  ~weeks ago; component signatures may have changed. Each task reads
  `lib/components.sngl` first.
- **Volume / monotony risk.** 24 nearly-identical fixtures may
  encourage shortcut-taking by implementers. Mitigate by reviewing
  each commit's diff briefly between tasks — pattern-matching catches
  any task that copied the wrong template.

## Out of scope (tracked separately)

- Audit items 23-32: platform-asymmetric CLI golden gaps
  (compile_bubbletea_for_map, compile_gtk4_*, etc.).
- Audit items 33-46: untested checker error paths
  (`error_async_in_parameterized_reactive.sngl`, etc.).
- Audit items 47-50: edge-case fixtures (empty file, only comments,
  unicode identifiers, etc.).
- Audit items 64-69: fixtures that don't meaningfully assert (existing
  tests that need upgrading vs new ones to write).
- Audit items 70-74: stale golden file audit.
- Audit items 75-88: missing test areas across the broader codebase.
- Audit items 89-94: generic methods matrix.
- Audit items 95-99: i18n parity gaps.
- Audit items 100-104: fuzz / race tests.
- Audit items 105-108: TestRunner (none) coverage gaps.

Each of these is a candidate for its own follow-up plan once Plan 5
demonstrates the fixture-authoring pattern works.
