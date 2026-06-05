# Finish the html static renderer migration — Design

**Date:** 2026-05-30
**Status:** Draft

## Goal

Complete the unified-codegen migration for the **html static-site
renderer** (`--lang none`). After the migration, the html platform renders
through **one generic IR-driven path** over native tags, with no
per-stdlib-component special-casing, and reactive `if`/`for` work end-to-end
in static HTML.

This finishes work the existing specs already landed for fyne/gtk4 and for
html's reactive-*update* path:

- `2026-05-13-component-extensions-design.md` — stdlib components carry a
  `platform html { html.<tag>(...) }` body; translators should see only
  native tags and the per-stdlib-tag switches should be deleted.
- `2026-05-12-reactivity-lowering-consolidation-design.md` — reactive
  `if`/`for` lower to `__renderSlotN`/`__slotN`/`__root`; fyne binds `__root`
  to a real container. html's structural-slot path exists but is incomplete.
- `2026-05-16-component-inlining-design.md` — user components inline into
  `main`; stdlib components are inlined by `InlinePure`.

The html static initial-HTML emitter never finished this migration. It still
carries a 17-case switch in `renderStaticNode` (`codegen/platform/html/html.go`)
dispatching to bespoke `renderStaticX` helpers. Those helpers are vestigial:
they re-derive each element from the original stdlib props and ignore the
inlined native-tag body. This is the root of three current bugs (see
Motivation).

## Motivation (the bugs this fixes)

1. **`input` renders empty / `disabled=disabled` garbage.** When a stdlib
   component's html body (e.g. `html.input(disabled=disabled, …)`) references a
   param the call site omitted, `InlinePure.substitute`
   (`internal/lower/inline_pure.go:367-376`) leaves the param as a bare
   identifier — unlike the user-component inliner
   (`internal/lower/inline_components.go:592-596`), which falls back to the
   param default. The unsubstituted ident reaches the generic renderer and is
   emitted as `disabled="disabled"`; in `__sngl_init` an undefined `disabled`
   reference throws before the input is seeded with its value, leaving the
   field blank.

2. **`html.select { html.option }` drops its options.** `renderStaticSelect`
   renders a `<select>` from an `options=[...]` data prop and **ignores
   children**. The generic path renders children, and the stdlib `select` body
   (`html.select(disabled, @change) { slot }`) already models children via
   `slot`.

3. **Reactive carousel renders literal `<__renderSlotN>` text.** html
   synthesizes the slot render functions but never binds `__root` to a real DOM
   node and never emits the initial `__renderSlotN(parent)` calls; the
   `<__renderSlotN>` placeholder leaks as an invalid tag. Reactive content
   inside component-nested windows and inside inlined components is additionally
   not collected by the reactivity pass.

## Non-goals

- fyne, gtk4, android — already migrated. No changes.
- Non-html target languages on the html platform (`--lang go` route mode is
  unaffected; this is the `--lang none` static path).
- Removing the legacy `g.updates` text/input updater registry
  (reactivity-consolidation Plan D Tasks 10–13, explicitly deferred there) —
  unless it directly blocks reactive-slot completion.
- Changing the SNGL surface language or checker scope rules.
- Adding new stdlib components (the existing set is the migration surface).

## Approach

**Big-bang migration, verified by real browser tests.** All 17
`renderStaticX` helpers are removed in one change; every stdlib component routes
through the generic renderer simultaneously. The risk of simultaneous
regressions is controlled by a rod/CDP browser-test suite that asserts rendered
DOM and interaction behavior for every stdlib component and the reactive cases
— not by snippet/golden assertions, which can pass while runtime behavior is
broken (e.g. the `__root = null` trap).

**Source of truth:** each stdlib component's `.sngl` `platform html { … }`
body (`codegen/platform/html/html.sngl`) and its declaration
(`lib/components.sngl`) define the intended rendering. Where a helper did
something the body does not yet express (e.g. `select`'s `options=` data API vs
its `slot` body), the body/declaration is corrected to match the intended
behavior; the helper is not preserved.

## Design

### Phase 0 — `InlinePure` param substitution

`internal/lower/inline_pure.go`. Make `substitute` bind every param of the
inlined component, mirroring `expandCall` in `inline_components.go`:

1. If the call site passes the prop, bind to that argument expression.
2. Else if the param has a `Default`, bind to the default.
3. Else bind to a synthesized typed zero-value literal: `bool→false`,
   `string→""`, `int→0`, `float→0`, and the type's zero value otherwise.

`substituteParams` then replaces *every* param ident in the body, so no bare
param identifiers survive into codegen. This is foundational: with it, props
arrive at the renderer as literals or reactive expressions, which the existing
generic renderer already handles correctly.

### Phase 1 — generic renderer absorbs helper responsibilities

`renderRawElementIR` (`codegen/platform/html/html.go`) already handles: tag
name, reactive node id / `data-sngl-id`, CSS from the `style` prop, boolean vs
literal vs reactive attributes, `innerText`/`innerHTML`/`textContent`, and
children. Add the remaining helper responsibilities so it is a complete
replacement:

- **Event handlers.** Wire `n.Handlers` (`@input`, `@change`, `@click`,
  `@focus`, `@blur`, `@submit`, …) through the existing
  `addParamEventHandler` path. The helpers previously called
  `addInputHandler`/`addChangeHandler`/etc.; the generic path must do the same
  for any handler present on the node.
- **Reactive `value`/`checked` seeding.** For a reactive `value` (or `checked`)
  prop, register the init-only updater (`.value = state.X` /
  `.checked = state.X`) so the DOM is seeded from initial state — the behavior
  currently in `renderStaticInput`/`renderStaticCheckbox`. Subsequent mutations
  are already spliced by the reactivity pass.
- **Void elements.** `input`, `img`, `br`, `hr`, `source`, `meta`, `link`
  self-close and render no children/slot.
- **Children / slot.** Render `n.Children` for non-void elements (covers
  `select { option }`, `radio { … }`, `tabs`, `modal`, `table`, etc.).

### Phase 2 — delete the switch and helpers

Remove `renderStaticNode`'s `switch name { … }` and all 17 `renderStaticX`
helpers (`renderStaticBox/Text/Button/Input/Checkbox/Image/Radio/Toggle/Select/ Textarea/Tabs/Table/Tree/Modal/Datepicker/ConditionalContainer`). `renderIRNode`
dispatches non-user, non-recursive nodes straight to the generic renderer.
After `InlinePure`, those nodes are native tags. Recursive user components retain
their existing call form (unchanged).

Correct any `.sngl` bodies/declarations whose helper did more than the body
expressed. **`select`:** migrate the helper's `options=[...]` data handling into
the `.sngl` body to match native HTML `<select>` behavior — the body renders an
`<option>` per entry in `options` (a `for` loop over the data prop) and also
admits explicit `slot` children. The deleted `renderStaticSelect` logic
(placeholder option, `selected` matching the bound `value`) is expressed in the
body, not retained as a helper.

### Phase 3 — html reactive slots

`codegen/platform/html/html.go` (slot emission) + `internal/lower/reactivity.go`
+ `internal/lower/inline_components.go`.

- **Anchor + binding.** Replace the literal `<__renderSlotN>` placeholder with a
  real, stable DOM anchor — an empty marker element (`<span data-sngl-slot="N">`) emitted at the slot's source position. Bind the slot's
  `parent`/`__root` reference to that element (the html analog of fyne's
  container Model field). `__renderSlotN(anchor)` appends/removes children of
  the anchor.
- **Init calls.** Emit `__renderSlotN(anchor)` for every slot in
  `__sngl_init`, so initial state renders. (Post-mutation splices already
  exist.)
- **Window-nested + inlined reactivity.** Re-apply the two reverted fixes:
  - `reactivity.go` `collectFromStmt` must recurse into `*ir.Window` statements
    found in a component body (windows declared inside `component main` are not
    in `pkg.Windows`); pass 2 already recurses, so pass 1 must too.
  - `inline_components.go` must repoint inlined identifiers' `Sym` to the cloned
    var (via a `Symbol→Symbol` rename map), so the reactivity pass — which
    collects the clones from `main.Vars` — matches the dependency.
- **Nested reactive slots.** `renderSlotBody` currently `panic`s on a nested
  `If`/`For` carrying its own `LoweredSlotID`. Support recursion so reactive
  conditionals/loops can nest (the carousel's `stack { if … }` is one level;
  deeper nesting must not panic).

## Testing

Primary: **rod/CDP browser tests** extending the existing
`codegen/platform/html/*_browser_test.go` harness.

- **Per-component DOM test** for every stdlib component: build a minimal app
  using the component, render in headless Chrome, assert the resulting DOM
  (tag, attributes, children) matches intent.
- **Interaction tests** for behavior that snippet tests cannot prove:
  - `input(:value=name)` shows the initial value and updates text on typing
    (`e.target.value`).
  - `checkbox`/`toggle` flip bound state.
  - `select { option }` shows its options and reports `change`.
  - reactive `if` shows/hides on state change.
  - timer-driven carousel rotates (one child visible at a time).

Secondary: golden HTML for static structure where a browser assertion is
unnecessary. A component is "migrated" only when its browser test passes.

A pre-migration step inventories each component's current helper output (golden
capture) so the browser tests encode the intended DOM before helpers are
deleted.

## Risks

- **Body/helper behavior gap.** A helper may encode behavior absent from the
  `.sngl` body. Resolved for `select` (migrate `options=` rendering into the
  body, above). For any other component found to have this gap, the rule is the
  same: express the behavior in the body, capture the intended DOM in a browser
  test, then delete the helper.
- **Big-bang regressions.** Mitigated by the per-component browser suite; a
  failing component points to a single body/renderer gap.
- **Reactive-slot anchor placement.** The marker element must not perturb
  layout (e.g. an empty inline `<span>` inside a flex container). Mitigation:
  prefer a zero-impact marker (comment node or `display:contents` span) and
  verify layout in browser tests.

## Affected code

- `internal/lower/inline_pure.go` — Phase 0 substitution.
- `codegen/platform/html/html.go` — generic renderer additions; delete switch +
  helpers; slot anchor/init emission.
- `codegen/platform/html/html.sngl`, `lib/components.sngl` — body/declaration
  corrections (source of truth).
- `internal/lower/reactivity.go`, `internal/lower/inline_components.go` —
  window-nested + inlined reactivity; nested slots.
- `codegen/platform/html/*_browser_test.go` — verification suite.
