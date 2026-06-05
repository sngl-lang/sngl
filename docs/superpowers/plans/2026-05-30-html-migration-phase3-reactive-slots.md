# HTML Migration — Phase 3: complete reactive `if`/`for` slots (carousel)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make runtime-reactive `if`/`for` render correctly in the html `--lang none` static path so the homepage carousel rotates one image at a time (driven by its timer), instead of showing all branches at once.

**Architecture:** The reactivity pass already synthesizes `__renderSlotN` render functions + `__slotN` accumulators and replaces each reactive `If`/`For` with a `CallStmt __renderSlotN(parent)` at its source position. Three gaps stop this working on html: (1) pass 1 of the reactivity pass never descends into a `window` nested inside a component body, so the carousel's reactive `if`s are never collected; (2) component inlining renames a state var's references by `Name` but not by `Sym`, so the reactivity pass (which collects the cloned vars from `main.Vars`) misses the dependency; (3) html renders the slot position as an invalid `<__renderSlotN>` raw tag and the slot's parent is an unbound `__root` (`= null`) for top-level slots, and init `__renderSlotN(...)` calls are only scanned from `main.Body` (missing window/component-nested slots). This phase fixes those three so the slot machinery is wired end-to-end on html.

**Tech Stack:** Go; `internal/lower/reactivity.go`, `internal/lower/inline_components.go`, `codegen/platform/html/html.go`; rod/CDP browser tests.

**Predecessors:** Phase 0 + Phase 1+2 landed (spec: `docs/superpowers/specs/2026-05-30-html-static-renderer-migration-design.md`).

## Out of scope (separate follow-ups; NOT website-blocking)
- **For-unrolled reactive bodies / tabs active styling.** A reactive `if`/attr inside an *unrolled* `for` emits malformed slot JS, and indexed `for i, x = list` substitutes the value where the index belongs (`internal/optimize/expand.go` `substituteLoopVars`). Distinct optimize-pass bug; tabs active-tab styling depends on it. Not used by the website.
- **Deeply-nested reactive slots.** `reactivity.go` panics on an `If`/`For` with a `LoweredSlotID` nested inside another reactive slot body (a documented "next step"). The carousel is shallow (sibling slots, no nesting) and does not hit it. Leave the panic.
- Hyphenated SNGL attribute names (`aria-selected`); i18n `select` export.

If any of these is hit while implementing an in-scope task, STOP and report — do not work around it.

---

## File Structure
- `internal/lower/reactivity.go` — pass-1 recurse into component-nested windows (gap 1).
- `internal/lower/inline_components.go` — repoint inlined idents' `Sym` to the clone (gap 2).
- `codegen/platform/html/html.go` — render a valid per-slot anchor element; bind each slot's parent to its anchor; emit init `__renderSlotN(anchor)` calls for ALL slots (gap 3).
- `codegen/platform/html/reactive_slot_browser_test.go` — **create**; browser tests (carousel rotation, top-level toggle).

---

### Task 1: Browser tests for reactive slots (red baseline)

**Files:** Create `codegen/platform/html/reactive_slot_browser_test.go`

- [ ] **Step 1: Write the tests** (reuse the `startComponent`/`renderComponentHTML` harness from `component_interaction_browser_test.go` / `component_dom_browser_test.go` — same package, faithful pipeline):

```go
//go:build !js

package html

import (
	"testing"
)

// Carousel shape: a timer-driven reactive `if` inside a component nested in a
// window. Exactly one branch must be visible at a time. We drive it via state
// rather than waiting on the timer: assert only ZERO is present initially.
func TestReactiveSlot_CarouselShowsOneAtATime(t *testing.T) {
	src := `
component Car() {
    var a = 0
    button(text="next", @click { a = (a + 1) % 2 })
    stack {
        if a == 0 { text(value="ZERO") }
        if a == 1 { text(value="ONE") }
    }
}
component main { window(title="H", href="/index.html") { Car() } }
`
	b := startComponent(t, src) // skips if no browser
	defer b.Close()
	page := b.Page()

	bodyText := func() string { return page.MustElement("body").MustText() }

	// Initially a==0: ZERO visible, ONE not.
	txt := bodyText()
	if !contains(txt, "ZERO") || contains(txt, "ONE") {
		t.Fatalf("initial: want ZERO and not ONE; body text=%q", txt)
	}
	// Click → a==1: ONE visible, ZERO not.
	page.MustElement("button").MustClick()
	b.WaitStable(stableWait)
	txt = bodyText()
	if !contains(txt, "ONE") || contains(txt, "ZERO") {
		t.Fatalf("after click: want ONE and not ZERO; body text=%q", txt)
	}
}

// Top-level reactive `if` (direct child of the component body, no enclosing
// element) — exercises the __root/top-level slot path.
func TestReactiveSlot_TopLevelToggle(t *testing.T) {
	src := `
component main {
    var on = true
    button(text="t", @click { on = !on })
    if on { text(value="SHOWN") }
}
`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	if !contains(page.MustElement("body").MustText(), "SHOWN") {
		t.Fatalf("initial: want SHOWN visible")
	}
	page.MustElement("button").MustClick()
	b.WaitStable(stableWait)
	if contains(page.MustElement("body").MustText(), "SHOWN") {
		t.Fatalf("after toggle: SHOWN should be hidden")
	}
}
```

Add a small `contains(s, sub string) bool` helper (or use `strings.Contains` directly). `MustText()` returns only *visible* text, so a hidden/removed branch won't appear — that is the behavioral assertion.

- [ ] **Step 2: Run — red baseline**

Run: `go test ./codegen/platform/html/ -run TestReactiveSlot -v`
Expected: BOTH FAIL. Carousel shows both ZERO and ONE (no slots generated for the component-nested window). Top-level toggle likely shows nothing or throws (`__renderSlot0(null)`). If the browser is unavailable, both SKIP — report that.

- [ ] **Step 3: Commit**

```bash
git add codegen/platform/html/reactive_slot_browser_test.go
git commit -m "test(html): reactive-slot browser tests (carousel + top-level toggle) — red baseline"
```

---

### Task 2: Reactivity pass recurses into component-nested windows (gap 1)

**Files:** Modify `internal/lower/reactivity.go` (`collectFromStmt`, the `*ir.Window` case)

- [ ] **Step 1: Apply the fix.** Replace the no-op `*ir.Window` case in `collectFromStmt`:

```go
	case *ir.Window:
		// handled by top-level loop in lowerReactivity
```

with:

```go
	case *ir.Window:
		// Top-level windows live in pkg.Windows and are walked by the loop in
		// lowerReactivity. Windows declared inside a component body
		// (`component main { window { ... } }`) are *ir.Window statements here
		// instead, and pass 2 already recurses into them — so pass 1 must too,
		// or reactive If/For inside such a window never get a slot collected.
		st.collectFromStmts(n.Body)
```

(Top-level windows are in `pkg.Windows`, not in any component body, so this only fires for component-nested windows — no double-processing.)

- [ ] **Step 2: Verify collection happens.** Slots still won't render (gap 2 blocks the dep match, gap 3 blocks rendering), but build must be clean and existing tests must pass:

Run: `go build ./... && go test ./internal/lower/ ./internal/optimize/`
Expected: clean + pass.

- [ ] **Step 3: Commit**

```bash
git add internal/lower/reactivity.go
git commit -m "fix(lower): reactivity pass 1 recurses into component-nested windows

Windows declared inside a component are *ir.Window stmts in the body, not
in pkg.Windows; pass 1 must descend into them so reactive if/for inside
(e.g. the carousel) get slots collected. Pass 2 already recursed."
```

---

### Task 3: Inlined identifiers repoint `Sym` to the clone (gap 2)

**Files:** Modify `internal/lower/inline_components.go` (`renameIdents`, `renameInExpr`, `expandCall`)

- [ ] **Step 1: Thread a symbol-rename map.** `renameIdents` currently rewrites `id.Name` but leaves `id.Sym` at the original decl, while `expandCall` adds a *clone* var to `main.Vars`. The reactivity pass collects the clones and matches by `Sym`, so the dependency is missed. Change `renameIdents`/`renameInExpr` to also repoint `Sym`:

Replace `renameIdents`:

```go
func renameIdents(stmts []ir.Stmt, renames map[ir.Symbol]string) []ir.Stmt {
	if len(renames) == 0 {
		return stmts
	}
	w := newExprWalker(func(e ir.Expr) ir.Expr {
		id, ok := e.(*ir.Ident)
		if !ok || id.Sym == nil {
			return e
		}
		if newName, ok2 := renames[id.Sym]; ok2 {
			id.Name = newName
		}
		return e
	})
	return w.stmts(stmts)
}
```

with (add a `symRenames map[ir.Symbol]ir.Symbol` parameter and repoint Sym):

```go
// renameIdents rewrites Ident.Name via renames and repoints Ident.Sym via
// symRenames. Repointing Sym is essential: after inlining, the cloned
// vars/funcs live in main, and downstream passes (notably reactivity) collect
// those clones and match dependencies by Sym pointer. Leaving Sym at the
// original decl makes a reactive `if` referencing an inlined var resolve to a
// var no longer in scope, so the dependency is missed and the branch loses
// reactivity. Mutates exprs in place; caller passes a deep clone.
func renameIdents(stmts []ir.Stmt, renames map[ir.Symbol]string, symRenames map[ir.Symbol]ir.Symbol) []ir.Stmt {
	if len(renames) == 0 && len(symRenames) == 0 {
		return stmts
	}
	w := newExprWalker(func(e ir.Expr) ir.Expr {
		id, ok := e.(*ir.Ident)
		if !ok || id.Sym == nil {
			return e
		}
		old := id.Sym
		if newName, ok2 := renames[old]; ok2 {
			id.Name = newName
		}
		if newSym, ok2 := symRenames[old]; ok2 {
			id.Sym = newSym
		}
		return e
	})
	return w.stmts(stmts)
}
```

And update `renameInExpr` to take + forward `symRenames`:

```go
func renameInExpr(e ir.Expr, renames map[ir.Symbol]string, symRenames map[ir.Symbol]ir.Symbol) ir.Expr {
	if e == nil {
		return nil
	}
	tmp := []ir.Stmt{&ir.LocalVar{Init: e}}
	tmp = renameIdents(tmp, renames, symRenames)
	return tmp[0].(*ir.LocalVar).Init
}
```

- [ ] **Step 2: Build `symRenames` in `expandCall` and pass it to every call site.** In `expandCall`, add `symRenames := map[ir.Symbol]ir.Symbol{}` next to `renames`; for each cloned var set `symRenames[v] = clone`; for each cloned func set `symRenames[f] = clone`. Then update the four `renameIdents(...)`/`renameInExpr(...)` calls in `expandCall` (the hoisted var inits, hoisted func blocks, hoisted timer handler blocks, and the body) to pass `symRenames` as the third/second argument. (Read `expandCall` to place these exactly: the var clone loop, the func clone loop, and the four rename calls.)

- [ ] **Step 3: Verify the lowering-level dependency tracking.** Add/confirm a lower test that a reactive `if` inside an inlined component produces a `__renderSlot` func on main:

Run: `go build ./... && go test ./internal/lower/`
Expected: clean + pass. (If a lower test for inlined reactivity doesn't exist, add one: build a pkg with `component Car{var a; if a==0{...}}` called from `main`, run `lowerInlineComponents` then `lowerReactivity`, assert `main.Funcs` contains a `__renderSlot`-prefixed func.)

- [ ] **Step 4: Commit**

```bash
git add internal/lower/inline_components.go
git commit -m "fix(lower): inlined idents repoint Sym to the clone

renameIdents rewrote Name but left Sym at the original decl, while expandCall
adds clone vars to main. The reactivity pass collects the clones and matches
by Sym, so reactive deps in inlined components were missed. Thread a
Symbol->Symbol map and repoint Sym."
```

---

### Task 4: html renders a real slot anchor, binds parent, emits init calls (gap 3)

**Files:** Modify `codegen/platform/html/html.go`

After Tasks 2-3 the carousel's slots are generated, but html still: renders the slot position as an invalid `<__renderSlotN>` raw tag; leaves `__root = null` for top-level slots; and only emits init `__renderSlotN(...)` calls scanned from `main.Body` (missing window/component-nested slots). Fix all three with a per-slot anchor.

- [ ] **Step 1: Read the current emission.** Read in `html.go`: `nodeFromIRCallStmt`/`irCallName` (how the `__renderSlotN` CallStmt becomes a `NodeInst{Name:"__renderSlotN"}` rendered as a raw tag), `emitSynthesizedSlots` (declares `let __slotN`/`let __root = null`, routes `__renderSlotN` bodies, scans `main.Body` for init calls), and how a reactive slot's `parent` arg is currently emitted (the `parentRef` — an enclosing element id for nested slots, `__root` for top-level).

- [ ] **Step 2: Emit a valid, position-preserving anchor for each slot CallStmt.** Where the `__renderSlotN` CallStmt is rendered (the raw `<__renderSlotN>` today), instead emit a real anchor element that occupies the slot's DOM position without affecting layout:

```html
<span data-sngl-slot="N" style="display:contents"></span>
```

(`display:contents` makes the wrapper transparent to layout; the slot's children render as if direct children of the real parent.) Render the `__renderSlotN` content INTO this anchor.

- [ ] **Step 3: Bind each slot's `parent` to its anchor + emit init calls for ALL slots.** In the JS:
  - For each slot N, resolve its anchor: `var __slotAnchor_N = document.querySelector('[data-sngl-slot="N"]');`
  - Change the slot render call to render into the anchor: `__renderSlotN(__slotAnchor_N)` — both at init and wherever a dep mutation re-fires it.
  - Make `__renderSlotN(parent)` operate on the anchor uniformly (it already does `parent.appendChild`/`parent.removeChild`; with the anchor as parent and `display:contents`, position is correct for both top-level and element-nested slots).
  - Emit the initial `__renderSlotN(__slotAnchor_N)` for EVERY synthesized slot, not only those found as top-level `CallStmt`s in `main.Body`. Walk all rendered bodies (or drive off the synthesized `__renderSlotN` funcs list) so window/component-nested slots are initialized too.
  - This removes the dependency on the unbound `__root` sentinel. If `__root`/`parentRef` is still threaded through the IR CallStmt arg, ignore it in favor of the per-slot anchor (or map `__root` → the slot's own anchor). Do NOT leave any `__renderSlotN(null)`/`__renderSlotN(__root)` with `__root == null`.

- [ ] **Step 4: Verify with the browser tests.**

Run: `go test ./codegen/platform/html/ -run TestReactiveSlot -v`
Expected: BOTH PASS — carousel shows one branch at a time and toggles on click; top-level toggle shows/hides.

If the carousel slots fire but render into the wrong position (e.g. both into body end), the anchor binding (Step 2-3) is off — fix it so each slot renders at its own anchor. If a dep mutation doesn't re-fire the slot, the re-fire splice (already emitted by the reactivity pass after each dep write) must also target the anchor — align it with the init call.

- [ ] **Step 5: Regression + commit.**

Run: `go test ./codegen/platform/html/ ./internal/lower/ ./internal/optimize/ ./cmd/sngl/`
Expected: all pass (the 17 component DOM tests + interaction + the new reactive-slot tests). Then:

```bash
git add codegen/platform/html/html.go
git commit -m "feat(html): render reactive slots into real per-slot anchors

Replace the invalid <__renderSlotN> placeholder with a display:contents
anchor span; bind each slot's parent to its anchor and emit init
__renderSlotN calls for all slots (incl. window/component-nested), removing
the unbound __root=null path. Reactive if/for now render and update."
```

---

### Task 5: Website carousel end-to-end

**Files:** none (verification)

- [ ] **Step 1: Regenerate + inspect the carousel.**

```bash
go install ./cmd/sngl
rm -rf /tmp/site_p3 && go tool sngl generate --platform html --lang none --out /tmp/site_p3 website.sngl
grep -c '<__renderSlot' /tmp/site_p3/index.html          # expect 0 (no invalid tags)
grep -c 'data-sngl-slot' /tmp/site_p3/index.html          # expect > 0 (anchors present)
grep -c '__renderSlot' /tmp/site_p3/index.html            # expect > 0 (render fns + init calls)
```

Confirm the carousel images render through slots (only the active image's `<img>` should be appended at init; the others live in the slot render functions).

- [ ] **Step 2: Browser-verify the live website carousel (optional but recommended).** If a browser test over the full `website.sngl` is feasible with the existing harness, assert exactly one carousel image is visible initially and it changes after a timer tick or carousel-button click. Otherwise rely on the unit carousel test (Task 1) + the static inspection above.

- [ ] **Step 3: Full sweep.**

Run: `go test ./codegen/platform/html/ ./internal/lower/ ./internal/optimize/ ./cmd/sngl/ ./internal/checker/`
Expected: all pass. (`codegen/platform/android` may time out in this environment — unrelated.)

---

## Self-Review
- **Spec coverage:** Implements spec §Phase 3 (reactive-slot completion: window-nested + inlined reactivity, `__root`/anchor binding, init calls). The spec's "nested reactive slots (panic)" item is explicitly deferred here as out-of-scope (the carousel is shallow); flagged in the Out-of-scope section.
- **Placeholder scan:** Tasks 2-3 give exact code (the two reverted fixes). Task 4 specifies the anchor mechanism concretely; the one judgement area (how `parentRef`/`__root` threads through the CallStmt arg vs the anchor) is a read-then-decide step with a defined outcome (no `__renderSlotN(null)` may remain) gated by the browser test.
- **Type/name consistency:** `renameIdents(stmts, renames, symRenames)`, `renameInExpr(e, renames, symRenames)`, `expandCall`, `collectFromStmt`, `emitSynthesizedSlots`, `startComponent`/`stableWait` (from component_interaction_browser_test.go), `renderComponentHTML` — all match current symbols.
- **Risk:** Task 4's anchor/parent binding is the design-heavy step; the carousel + top-level browser tests are the objective gate. If the per-slot-anchor approach conflicts with how the reactivity pass threads `parentRef` for element-nested slots, reconcile by always rendering into the anchor (uniform) — and STOP/ask if that reveals a deeper reactivity-pass assumption.
