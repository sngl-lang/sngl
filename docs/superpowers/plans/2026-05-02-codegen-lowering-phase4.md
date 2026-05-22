# Codegen Lowering — Phase 4 (HTML opts into NoReactivity, prop subset) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move HTML's `NodeInst.Props` reactivity off `codegen/deps.go`'s `DepTracker` and onto the lowered IR's injected `#__nN.<key> = expr` Assigns. After this phase, prop-update wiring inside event handlers / inputs / timers is driven by IR statements `lower.Lower(pkg, Caps{NoReactivity:true})` already produced — no `addTextUpdater` / `addAttrUpdater` / `addDisabledUpdater` calls fire for nodes carrying a `__n*` ID.

**Architecture:** HTML's `Generator.Capabilities()` flips from `lower.Caps{}` to `lower.Caps{NoReactivity: true}`. The static-render walk (`renderStaticNode`, `renderRawElementIR`, etc.) prefers `NodeInst.ID` (a pre-assigned `__nN` from NoReactivity) over `g.allocID()`'s `$N`. When a prop is reactive but its NodeInst already has a `__n*` id, HTML skips the `addTextUpdater` / `addAttrUpdater` / `addDisabledUpdater` registration — the equivalent already lives inside handler/timer/init-block bodies as `*ir.Assign{Target: *ir.Select{IsElementRef:true}}`. A new `htmlGen.translateHandlerStmt` helper wraps `lang.TranslateIRMutation` and intercepts those reactive-update Assigns, looking up the originating NodeInst from a side table to remap the SNGL prop name (e.g. `value` on a `text` component) to the DOM-correct write (e.g. `.textContent`).

**Tech Stack:** Go (Go 1.24+), existing `internal/lower` (already covers NoReactivity for NodeInst.Props as of phase 3b), `codegen/lang/javascript` (already translates `IsElementRef` Idents to `document.querySelector`), `codegen/platform/html`.

**Reference spec:** `docs/superpowers/specs/2026-05-02-codegen-lowering-design.md` §"Phase 4 — Port HTML".
**Reference plans:** Phase 1 (`2026-05-02-codegen-lowering-phase1.md`) for capability plumbing; Phase 3b (`2026-05-02-codegen-lowering-phase3b.md`) for NoReactivity contract.

---

## Scope (this slice)

In:
- HTML opts into `NoReactivity` cap.
- HTML's static renderer reuses pre-assigned `__nN` IDs from NoReactivity rather than allocating a fresh `$N`.
- HTML emits `data-sngl-id="__nN"` so JS lang's `IsElementRef` ident translation (`document.querySelector('[data-sngl-id=…]')`, `codegen/lang/javascript/translate_ir.go:103-104`) finds the element.
- HTML suppresses prop-update registration (`addTextUpdater`, `addAttrUpdater`, `addDisabledUpdater`, `addTextContentUpdater`) for any NodeInst whose ID starts with `__n`.
- HTML's handler / timer / change-setter emission paths translate any reactive-update Assign (`*ir.Assign{Target: *ir.Select{IsElementRef:true}}`) through a new prop→DOM remap so `value`/`text`/`disabled` props land on the right DOM property.

Deferred (NOT in this plan; tracked for phase 4b):
- Removing `MutationModelEmitter` interface from HTML (still needed for `If`/`For`/list reactivity which NoReactivity doesn't cover yet).
- Removing `g.dt` (`DepTracker`), `findAffectedUpdaters`, `optimizeIR`, `MutationModel` construction in HTML.
- Extending NoReactivity to cover `*ir.If` cond visibility and `*ir.For` collection refresh (today only `NodeInst.Props` is covered — see `internal/lower/reactivity.go:113-130`).
- Fyne port (phase 5).
- Deletion of `codegen/analysis.go` + `codegen/deps.go` (phase 6).

**Net behavior at end of this phase:** prop-update flows go through lowered IR; control-flow updates still go through HTML's existing dataflow. Snapshot tests must still pass (output churn is acceptable as long as the asserted substrings still appear).

## File Structure

**Create:**
- `internal/lower/testdata/compose_html_caps_counter.txtar` — anchor golden showing HTML's resolved caps applied to a counter component.

**Modify:**
- `codegen/platform/html/html.go` — `Capabilities()` flip; `allocID` → `nodeID` helper; static renderers (`renderStaticBadge`, `renderStaticText`, `renderStaticButton`, `renderStaticInput`, `renderStaticProgress`, `renderRawElementIR`); reactive-prop registration guards; new `translateHandlerStmt` + `domWriteFor` helpers; handler / input / timer / change-setter call sites.
- `codegen/platform/html/html_test.go` — additional assertion that the JS body contains `document.querySelector('[data-sngl-id="__n0"]')` for a known reactive component.

**Possibly modify (only if a fixture genuinely diverges):**
- `cmd/sngl/testdata/compile_html_cache_bust.txt` — if asset hashing flips because state init order changes.

---

### Task 0: Anchor golden showing HTML's lowered IR

**Files:**
- Create: `internal/lower/testdata/compose_html_caps_counter.txtar`

This task lands BEFORE any HTML code change. It documents the IR shape HTML will consume. No HTML code changes; the golden lives next to existing `reactivity_*.txtar` files and exercises the same caps HTML will set in Task 4.

- [ ] **Step 1: Create the fixture**

Create `internal/lower/testdata/compose_html_caps_counter.txtar`:

```
caps: NoReactivity
-- input.sngl --
component main {
    var n int = 0
    text(value=string(n))
    badge(value=string(n))
    button(text="+", @click { n = n + 1 })
}
-- expected.sngl --
```

(The `caps:` line matches HTML's about-to-flip `Capabilities()`. The `expected.sngl` block is intentionally empty; Step 2 fills it via `-update`. Two reactive nodes — `text` and `badge` — share the same expr so the fixture exercises NoReactivity assigning multiple `__n*` IDs and injecting multiple updater Assigns per mutation, distinguishing this fixture from the single-node `reactivity_counter.txtar`.)

- [ ] **Step 2: Generate the golden**

Run: `go test ./internal/lower/ -run TestLower -update`
Expected: PASS.

- [ ] **Step 3: Inspect**

Run: `cat internal/lower/testdata/compose_html_caps_counter.txtar`

Expected output's `expected.sngl` block (must match exactly — copy-paste this as your reference for Tasks 2-4):

```
import stdlib "internal://stdlib"
import alert "internal://alert"
import file "internal://file"
component main {
    var n int = 0
    text #__n0(value=string(n))
    badge #__n1(value=string(n))
    button(text="+", @click {
        n = n + 1
        #__n0.value = string(n)
        #__n1.value = string(n)
    })
}
```

This is the IR HTML will receive once `Capabilities()` flips. Note:
- `text` NodeInst has `ID = "__n0"`; `badge` has `ID = "__n1"`; `button` has no ID (its `text` prop is a literal, not reactive).
- `@click` handler's block now contains two extra Assigns after `n = n + 1` — one per reactive prop dependency.

- [ ] **Step 4: Run all lower tests**

Run: `go test ./internal/lower/`
Expected: PASS — including the new golden.

- [ ] **Step 5: Commit**

```bash
git add internal/lower/testdata/compose_html_caps_counter.txtar
git commit -m "$(cat <<'EOF'
Phase 4 Task 0: anchor golden for HTML's lowered IR

Adds compose_html_caps_counter.txtar exercising NoReactivity alone (the
caps HTML's Generator.Capabilities() will return after Task 4). Documents
the prop-update Assign shape HTML's handler-emission path must consume:
text NodeInst gets __n0, click handler gains an injected
#__n0.value = string(n) Assign right after the user-written n = n + 1.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 1: HTML renderer reuses NodeInst.ID and emits data-sngl-id

**Files:**
- Modify: `codegen/platform/html/html.go`

**Background:** NoReactivity assigns `NodeInst.ID = "__nN"` to every NodeInst with a reactive prop (`internal/lower/reactivity.go:118-119`). HTML today ignores `NodeInst.ID` entirely and allocates its own `$N` via `g.allocID()` (html.go:493-497). To consume the lowered IR, HTML must (a) prefer the pre-assigned ID over `$N`, and (b) make the chosen ID discoverable from JS via `data-sngl-id` (so the JS lang's existing `IsElementRef` Ident translation `document.querySelector('[data-sngl-id="__n0"]')` lines up — see `codegen/lang/javascript/translate_ir.go:103-104`).

**Side table:** This task also seeds `g.idToNode map[string]*ir.NodeInst`. Task 4 needs it to translate `#<id>.<key> = expr` to the DOM-correct write.

- [ ] **Step 1: Add the side table to htmlGen**

Edit `codegen/platform/html/html.go` around line 420 (end of `htmlGen` struct):

```go
	// irBodyStmts is the IR body rendered for the current window.
	irBodyStmts []ir.Stmt

	// idToNode maps each emitted element id (either an alloc'd "$N" or a
	// pre-assigned "__nN" from internal/lower NoReactivity) back to its
	// originating NodeInst. Task 4 reads this when translating
	// reactive-update Assigns inside handler / timer / change-setter
	// bodies, where the only context is the id string.
	idToNode map[string]*ir.NodeInst
}
```

- [ ] **Step 2: Initialize idToNode in newHTMLGen**

Edit `codegen/platform/html/html.go` around line 459 (inside `newHTMLGen`, just before the `g.scope = ...` block):

```go
	g := &htmlGen{
		pkg:            pkg,
		lang:           lang,
		CommonAnalysis: common,
		preview:        opts.Preview,
		testMode:       opts.Test,
		minify:         opts.Minify,
		idToNode:       make(map[string]*ir.NodeInst),
	}
```

- [ ] **Step 3: Add a nodeID helper**

Append after `allocID` (html.go:497):

```go
// nodeID returns n.ID when NoReactivity has pre-assigned one (`__n*`),
// otherwise allocates a fresh `$N`. Records the chosen id in g.idToNode
// so reactive-update Assigns inside handler bodies can be translated
// against the originating NodeInst (see translateHandlerStmt).
func (g *htmlGen) nodeID(n *ir.NodeInst) string {
	var id string
	if n != nil && strings.HasPrefix(n.ID, "__n") {
		id = n.ID
	} else {
		id = g.allocID()
	}
	if n != nil {
		g.idToNode[id] = n
	}
	return id
}
```

- [ ] **Step 4: Add a writeReactiveIDAttrs helper**

Append directly after `nodeID` (html.go ≈ line 520):

```go
// writeReactiveIDAttrs writes id="..." plus data-sngl-id="..." for ids
// originating from NoReactivity (the `__n*` form). For an alloc'd `$N`
// id only the legacy `id="..."` attr is emitted — JS lang's IsElementRef
// path uses `data-sngl-id`, and `$N` ids are never targets of
// reactive-update Assigns (NoReactivity doesn't assign `$`-prefixed
// ids).
func (g *htmlGen) writeReactiveIDAttrs(b *strings.Builder, id string) {
	if id == "" {
		return
	}
	fmt.Fprintf(b, " id=%q", id)
	if strings.HasPrefix(id, "__n") {
		fmt.Fprintf(b, " data-sngl-id=%q", id)
	}
}
```

- [ ] **Step 5: Build**

Run: `go build ./codegen/platform/html/...`
Expected: success. (The new helpers aren't called yet.)

- [ ] **Step 6: Wire nodeID into renderStaticBadge**

Edit `renderStaticBadge` at `codegen/platform/html/html.go:1024-1032`. Replace:

```go
		id := ""
		if g.nodeIsReactive(n) || g.preview {
			id = g.allocID()
		}
		g.writeOpenTag(b, "span", id, style, n, depth)
		fmt.Fprintf(b, "%s</span>\n", html.EscapeString(value))
		if codegen.IRIsReactive(codegen.NodeProp(n, "value")) {
			g.addTextUpdater(id, codegen.NodeProp(n, "value"))
		}
```

with:

```go
		id := ""
		if g.nodeIsReactive(n) || g.preview {
			id = g.nodeID(n)
		}
		g.writeOpenTag(b, "span", id, style, n, depth)
		fmt.Fprintf(b, "%s</span>\n", html.EscapeString(value))
		if codegen.IRIsReactive(codegen.NodeProp(n, "value")) && !strings.HasPrefix(id, "__n") {
			g.addTextUpdater(id, codegen.NodeProp(n, "value"))
		}
```

(The guarded `addTextUpdater` is the Task 2 pattern, applied early here so badge stays consistent. Other elements get the same treatment in Task 2.)

- [ ] **Step 7: Run html tests with the partial change**

Run: `go test ./codegen/platform/html/`
Expected: PASS — `Capabilities()` still returns `lower.Caps{}` so NoReactivity hasn't run; nothing has `__n*` ids; both branches go through the legacy path.

- [ ] **Step 8: Run lower tests**

Run: `go test ./internal/lower/`
Expected: PASS (Task 0's golden still passes; nothing new touches it).

- [ ] **Step 9: Commit**

```bash
git add codegen/platform/html/html.go
git commit -m "$(cat <<'EOF'
Phase 4 Task 1: htmlGen nodeID + writeReactiveIDAttrs helpers

Adds htmlGen.idToNode side table mapping emitted element ids to their
originating NodeInsts. New nodeID(n) helper prefers a pre-assigned
NodeInst.ID (the __nN form internal/lower NoReactivity sets) over
g.allocID()'s $N — and records the choice in idToNode for Task 4's
prop-update translation. New writeReactiveIDAttrs(b, id) writes both
id="..." and data-sngl-id="..." for __n-prefixed ids so JS lang's
IsElementRef document.querySelector path resolves the element. badge
renderer wired through nodeID as the canary; remaining elements move
in Task 2.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Wire remaining static renderers through nodeID + skip duplicate registration

**Files:**
- Modify: `codegen/platform/html/html.go`

**Goal:** Every static renderer that allocates an id for a reactive prop now (a) routes through `g.nodeID(n)` so a NoReactivity-assigned `__nN` is preferred, and (b) skips its `addTextUpdater` / `addAttrUpdater` / `addDisabledUpdater` / `addTextContentUpdater` call when the id starts with `__n` (because the lowered IR already injected the equivalent inside handler/timer/setter bodies — task 4 will translate it).

The five callsites of these registrations live at:
- `html.go:1003` — `renderStaticProgress`'s `addAttrUpdater(id, "value", valExpr)`.
- `html.go:1031` — `renderStaticBadge`'s `addTextUpdater(id, …)` (already partially handled in Task 1; revisit to confirm).
- `html.go:1304` — `renderStaticText`'s `addTextUpdater(id, …)`.
- `html.go:1352` + `html.go:1355` — `renderStaticButton`'s `addTextContentUpdater(id, textExpr)` and `addDisabledUpdater(id, disabledExpr)`.
- `html.go:1949` — generic raw-element / user-attr updater inside `addUserAttrUpdaters`.

Plus three id-allocation sites in elements that don't register prop updaters but still need `nodeID` so `idToNode` is seeded (so Task 4's lookup succeeds for handlers attached to them):
- `html.go:1311-1320` — `renderStaticButton` (allocates id when reactive).
- `html.go:1366` — `renderStaticInput` (always allocates).
- `html.go:1696` + `html.go:1724` — `details`/`open` element renderers (already use addIfUpdater, not in scope here, but their `g.allocID()` calls can move to `g.nodeID(n)` now to keep the pattern consistent).

The `addUserAttrUpdaters` site (html.go:1949 area) is for test-mode `data-key`/`id`/`class` attrs and is independent of NoReactivity — leave it as-is, but route its callsite through `nodeID` if it allocs an id.

- [ ] **Step 1: Wire renderStaticText through nodeID**

Edit `codegen/platform/html/html.go:1290-1305`. Replace the block:

```go
	if reactive {
		id = g.allocID()
	}

	// In test mode, id/class need an element ID for updaters
	if g.testMode && id == "" && g.nodeHasUserAttrs(n) {
		id = g.allocID()
	}

	g.writeOpenTag(b, "span", id, style, n, depth)
	b.WriteString(html.EscapeString(val))
	b.WriteString("</span>\n")

	if codegen.IRIsReactive(codegen.NodeProp(n, "value")) {
		g.addTextUpdater(id, codegen.NodeProp(n, "value"))
	}
```

with:

```go
	if reactive {
		id = g.nodeID(n)
	}

	// In test mode, id/class need an element ID for updaters
	if g.testMode && id == "" && g.nodeHasUserAttrs(n) {
		id = g.nodeID(n)
	}

	g.writeOpenTag(b, "span", id, style, n, depth)
	b.WriteString(html.EscapeString(val))
	b.WriteString("</span>\n")

	if codegen.IRIsReactive(codegen.NodeProp(n, "value")) && !strings.HasPrefix(id, "__n") {
		g.addTextUpdater(id, codegen.NodeProp(n, "value"))
	}
```

- [ ] **Step 2: Wire renderStaticButton through nodeID**

Edit `codegen/platform/html/html.go:1316-1356`. Replace the block (full diff to keep it unambiguous):

```go
	reactive := codegen.IRIsReactive(textExpr) || len(n.Handlers) > 0 || codegen.IRIsReactive(disabledExpr)
	id := ""
	if reactive {
		id = g.allocID()
	}
```

with:

```go
	reactive := codegen.IRIsReactive(textExpr) || len(n.Handlers) > 0 || codegen.IRIsReactive(disabledExpr)
	id := ""
	if reactive {
		id = g.nodeID(n)
	}
```

Then at `html.go:1334-1336`:

```go
	if g.preview && id == "" {
		id = g.allocID()
	}
```

becomes:

```go
	if g.preview && id == "" {
		id = g.nodeID(n)
	}
```

Then guard the prop-update registrations at `html.go:1351-1356`:

```go
	if codegen.IRIsReactive(textExpr) {
		g.addTextContentUpdater(id, textExpr)
	}
	if codegen.IRIsReactive(disabledExpr) {
		g.addDisabledUpdater(id, disabledExpr)
	}
```

becomes:

```go
	if codegen.IRIsReactive(textExpr) && !strings.HasPrefix(id, "__n") {
		g.addTextContentUpdater(id, textExpr)
	}
	if codegen.IRIsReactive(disabledExpr) && !strings.HasPrefix(id, "__n") {
		g.addDisabledUpdater(id, disabledExpr)
	}
```

- [ ] **Step 3: Wire renderStaticProgress through nodeID + guard**

Edit `codegen/platform/html/html.go` near line 1001 — first locate the `id := ""` allocation a few lines above the addAttrUpdater (it's part of the progress branch starting around html.go:982). Then change:

```go
			if id != "" {
				if valExpr := codegen.NodeProp(n, "value"); valExpr != nil {
					g.addAttrUpdater(id, "value", valExpr)
				}
			}
```

to:

```go
			if id != "" && !strings.HasPrefix(id, "__n") {
				if valExpr := codegen.NodeProp(n, "value"); valExpr != nil {
					g.addAttrUpdater(id, "value", valExpr)
				}
			}
```

And update the `id = g.allocID()` line directly above the writeOpenTag call inside the progress case to `id = g.nodeID(n)`.

- [ ] **Step 4: Wire renderStaticInput through nodeID**

Edit `codegen/platform/html/html.go:1364-1368`:

```go
func (g *htmlGen) renderStaticInput(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyle(n)
	id := g.allocID() // inputs are always reactive
```

becomes:

```go
func (g *htmlGen) renderStaticInput(b *strings.Builder, n *ir.NodeInst, depth int) {
	style := g.buildCSSStyle(n)
	id := g.nodeID(n) // inputs are always reactive
```

(Inputs use `addAttrUpdater` and `addInputHandler`. Today `addAttrUpdater(id, "value", expr)` is what html.go:1003 already showed for progress; check input's emission for similar; if found, guard with `!strings.HasPrefix(id, "__n")` the same way.)

- [ ] **Step 5: Audit remaining `g.allocID()` callsites**

Run: `grep -n "g.allocID()" codegen/platform/html/html.go`
Expected: a list. For each, decide:
- If the allocated id is *only* a placeholder that the static renderer will register an `addIfUpdater` / `addElseUpdater` / `addForStmtUpdater` against (the If/For/list family), leave as `g.allocID()` — NoReactivity doesn't claim those nodes.
- If the id is for a NodeInst rendered as a real element (text/button/input/badge/raw element), switch to `g.nodeID(n)`.

The `renderRawElementIR` path at `html.go:1818` is the catch-all for arbitrary HTML tags (`html.div`, `html.img`, …). Find its id allocation (likely a `g.allocID()` near the open-tag emission) and switch to `g.nodeID(n)`. Its prop-update registrations should be guarded with the same `!strings.HasPrefix(id, "__n")` pattern; locate them via `grep -n "addAttrUpdater\|addTextUpdater\|addDisabledUpdater" codegen/platform/html/html.go`.

The `addUserAttrUpdaters` site at html.go:1949 is test-mode only and orthogonal; leave it on `g.allocID()`.

- [ ] **Step 6: Build**

Run: `go build ./codegen/platform/html/...`
Expected: success.

- [ ] **Step 7: Run all html tests**

Run: `go test ./codegen/platform/html/`
Expected: PASS. NoReactivity isn't enabled yet (Capabilities still returns `lower.Caps{}`); all `id`s are `$N`; the `!strings.HasPrefix(id, "__n")` guards are no-ops; legacy registration runs as before.

- [ ] **Step 8: Run script tests touching html**

Run: `go test ./cmd/sngl/ -run TestScript`
Expected: PASS. Same reasoning as Step 7.

- [ ] **Step 9: Run full project verify**

Run: `go tool verify`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add codegen/platform/html/html.go
git commit -m "$(cat <<'EOF'
Phase 4 Task 2: route HTML id allocation through nodeID + guard registration

Every static renderer that allocates an element id for a reactive node
now goes through htmlGen.nodeID(n), seeding the idToNode map so Task 4
can recover the originating NodeInst from a __n* id. addTextUpdater,
addAttrUpdater, addDisabledUpdater, and addTextContentUpdater calls are
guarded with !strings.HasPrefix(id, "__n") so they no-op when
NoReactivity has pre-assigned a __nN id (the lowered IR already
contains the equivalent #__nN.<key> = expr Assign inside handler /
timer / setter bodies). Capabilities() still returns lower.Caps{}, so
no __n* id is in flight yet — guards are dormant. Tests pass under
legacy behavior.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Emit data-sngl-id attribute alongside id

**Files:**
- Modify: `codegen/platform/html/html.go`

**Goal:** When HTML emits an element with a `__n*` id, also emit `data-sngl-id="__nN"`. This is the attribute JS lang's `translateIRIdent` (`codegen/lang/javascript/translate_ir.go:103-104`) targets when rendering an `IsElementRef: true` Ident — the form of every reactive-update Assign target NoReactivity injects.

The attribute write happens in two places:
1. `g.writeOpenTag(b, tag, id, style, n, depth)` — the standard open-tag helper. Threading `data-sngl-id` through this covers most callers.
2. Inline `fmt.Fprintf(b, "%s<button id=%q", ...)` style writes inside `renderStaticButton`, `renderStaticProgress`, `renderRawElementIR`, etc. Each of these is a special-case open-tag write that bypasses `writeOpenTag`.

Approach: introduce `g.writeReactiveIDAttrs` (already added in Task 1) and call it after the `id="…"` write at every open-tag site.

- [ ] **Step 1: Locate writeOpenTag**

Run: `grep -n "func (g \*htmlGen) writeOpenTag" codegen/platform/html/html.go`
Expected: one match. Read that function (~30 lines).

- [ ] **Step 2: Patch writeOpenTag to add data-sngl-id**

Inside `writeOpenTag`, find the `id != ""` branch that writes `id=%q`. Add a `data-sngl-id` write directly after when the id starts with `__n`:

Locate this pattern (or the equivalent — exact existing source):

```go
	if id != "" {
		fmt.Fprintf(b, " id=%q", id)
	}
```

Replace with:

```go
	if id != "" {
		fmt.Fprintf(b, " id=%q", id)
		if strings.HasPrefix(id, "__n") {
			fmt.Fprintf(b, " data-sngl-id=%q", id)
		}
	}
```

(If `writeOpenTag` uses `fmt.Sprintf` plus a single `b.WriteString` instead, adapt accordingly; the goal is to insert the data-sngl-id write between the `id="…"` and the next attr.)

- [ ] **Step 3: Patch the inline open-tag writes**

For each of these sites, find the `fmt.Fprintf(b, ... id=%q ...)` call and add a `g.writeReactiveIDAttrs` call OR inline the same `data-sngl-id` conditional:

- `renderStaticProgress`: `html.go:990-997` — change:

  ```go
  fmt.Fprintf(b, "%s<progress", indent)
  if id != "" {
  	fmt.Fprintf(b, " id=\"%s\"", id)
  }
  ```

  to:

  ```go
  fmt.Fprintf(b, "%s<progress", indent)
  if id != "" {
  	fmt.Fprintf(b, " id=%q", id)
  	if strings.HasPrefix(id, "__n") {
  		fmt.Fprintf(b, " data-sngl-id=%q", id)
  	}
  }
  ```

- `renderStaticButton`: `html.go:1338-1342`:

  ```go
  if id != "" {
  	fmt.Fprintf(b, "%s<button id=\"%s\"", indent, id)
  } else {
  	fmt.Fprintf(b, "%s<button", indent)
  }
  ```

  becomes:

  ```go
  if id != "" {
  	fmt.Fprintf(b, "%s<button id=%q", indent, id)
  	if strings.HasPrefix(id, "__n") {
  		fmt.Fprintf(b, " data-sngl-id=%q", id)
  	}
  } else {
  	fmt.Fprintf(b, "%s<button", indent)
  }
  ```

- `renderRawElementIR` (`html.go:1818` area): find the `id=%q` write and apply the same conditional. (Inspect the function first; structure may differ.)

- `renderIRIf` (`html.go:728-746`): the wrapper `<div id="…">`s for if/else branches use `g.allocID()` → `$N` form, never `__n*` (NoReactivity doesn't touch If wrappers — see deferred scope). Leave unchanged.

- `renderStaticInput` (`html.go:1364`): inputs always have an id; locate the open-tag write and apply the conditional.

- [ ] **Step 4: Build**

Run: `go build ./codegen/platform/html/...`
Expected: success.

- [ ] **Step 5: Run html tests**

Run: `go test ./codegen/platform/html/`
Expected: PASS. No `__n*` id is in flight yet, so no `data-sngl-id` attr appears in any output, so all tests behave identically.

- [ ] **Step 6: Run full project verify**

Run: `go tool verify`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add codegen/platform/html/html.go
git commit -m "$(cat <<'EOF'
Phase 4 Task 3: emit data-sngl-id alongside id for __n* element ids

writeOpenTag and the inline open-tag writes inside renderStaticProgress,
renderStaticButton, renderStaticInput, and renderRawElementIR now emit
data-sngl-id="__nN" right after id="__nN" when the id starts with __n.
This is the attribute JS lang's translateIRIdent (IsElementRef branch)
already targets via document.querySelector — wires the lowered IR's
reactive-update Assign idents to the right DOM element. No __n* id is
in flight yet (Capabilities() still returns lower.Caps{}), so no
data-sngl-id attr appears in test output yet.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Translate reactive-update Assigns through prop→DOM remap

**Files:**
- Modify: `codegen/platform/html/html.go`

**Goal:** When the lowered IR injects `*ir.Assign{Target: *ir.Select{IsElementRef:true, Operand: *ir.Ident{Name: "__n0"}, Field: "value"}, Value: <expr>}` into a handler block, JS lang's default translation produces:

```js
document.querySelector('[data-sngl-id="__n0"]').value = String(state.n);
```

That's correct for an `<input>` (`.value` is its DOM property), but wrong for a `<span>` rendering a SNGL `text`/`badge` component (DOM uses `.textContent`), and wrong for a `<button>` rendering a SNGL `button` component's `text` prop (also `.textContent`).

This task adds a per-handler-stmt translator that intercepts these Assigns and remaps the field name to the DOM-correct write. Non-reactive-update statements pass through to `lang.TranslateIRMutation` unchanged.

The mapping is component-aware: `g.idToNode[id]` (seeded in Task 1) gives the originating NodeInst → its component name (`text`/`button`/`input`/`progress`/...) drives the choice.

- [ ] **Step 1: Add domWriteFor — the prop→DOM mapping**

Append to `codegen/platform/html/html.go` (near the existing addTextUpdater family, around line 2700):

```go
// domWriteFor maps a (SNGL component name, prop key) pair to the JS
// expression that writes that prop on the rendered DOM element.
// `el` is the JS expression yielding the element (e.g.
// `document.querySelector('[data-sngl-id="__n0"]')` or a cached const);
// `value` is the JS expression to write. Returns the full statement
// without trailing semicolon.
//
// Only props that NoReactivity may inject as #__nN.<key> Assigns are
// listed. Unknown (componentName, key) pairs fall through to a generic
// property assignment `el.<key> = value`, matching JS lang's default —
// surface a TODO if a real test produces a wrong write so the table
// can be extended.
func domWriteFor(componentName, key, el, value string) string {
	switch componentName {
	case "text", "badge":
		if key == "value" {
			return fmt.Sprintf("%s.textContent = %s", el, value)
		}
	case "button":
		if key == "text" {
			return fmt.Sprintf("%s.textContent = %s", el, value)
		}
		if key == "disabled" {
			return fmt.Sprintf("%s.disabled = %s", el, value)
		}
	case "input":
		if key == "value" {
			return fmt.Sprintf("%s.value = %s", el, value)
		}
		if key == "disabled" {
			return fmt.Sprintf("%s.disabled = %s", el, value)
		}
	case "progress":
		if key == "value" {
			return fmt.Sprintf("%s.setAttribute(\"value\", %s)", el, value)
		}
	}
	return fmt.Sprintf("%s.%s = %s", el, key, value)
}
```

- [ ] **Step 2: Add translateHandlerStmt — the per-stmt dispatcher**

Append directly after `domWriteFor`:

```go
// translateHandlerStmt translates one handler-block IR stmt to a JS
// snippet (no trailing semicolon). Reactive-update Assigns of the form
// `*ir.Assign{Target: *ir.Select{IsElementRef:true, Field:F},
// Operand:*ir.Ident{Name:ID}}` are routed through domWriteFor using
// g.idToNode[ID] to choose the DOM-correct write. Everything else
// falls through to lang.TranslateIRMutation.
func (g *htmlGen) translateHandlerStmt(s ir.Stmt) []string {
	a, ok := s.(*ir.Assign)
	if !ok {
		return g.lang.TranslateIRMutation(s, g.scope)
	}
	sel, ok := a.Target.(*ir.Select)
	if !ok {
		return g.lang.TranslateIRMutation(s, g.scope)
	}
	idn, ok := sel.Operand.(*ir.Ident)
	if !ok || !idn.IsElementRef {
		return g.lang.TranslateIRMutation(s, g.scope)
	}
	node, ok := g.idToNode[idn.Name]
	if !ok || node == nil {
		// Pre-assigned __n* id but renderer never seeded the map — fall
		// through to JS default (querySelector + .field = …) and rely
		// on the generic write to be correct enough.
		return g.lang.TranslateIRMutation(s, g.scope)
	}
	el := fmt.Sprintf("document.querySelector('[data-sngl-id=%q]')", idn.Name)
	value := g.lang.TranslateIRExpr(a.Value, g.scope)
	return []string{domWriteFor(node.Name, sel.Field, el, value)}
}
```

- [ ] **Step 3: Wire translateHandlerStmt into addClickHandler**

Edit `codegen/platform/html/html.go:2830-2855` (`addClickHandler`). Replace:

```go
	for _, s := range body {
		stmts = append(stmts, g.lang.TranslateIRMutation(s, g.scope)...)
		for k, v := range codegen.MutatedFields(s) {
			if mutated == nil {
				mutated = make(map[string]bool)
			}
			mutated[k] = v
		}
	}
```

with:

```go
	for _, s := range body {
		stmts = append(stmts, g.translateHandlerStmt(s)...)
		for k, v := range codegen.MutatedFields(s) {
			if mutated == nil {
				mutated = make(map[string]bool)
			}
			mutated[k] = v
		}
	}
```

- [ ] **Step 4: Wire translateHandlerStmt into addInputHandler**

Edit `codegen/platform/html/html.go:2889-2895` (`addInputHandler`):

```go
	var stmts []string
	mutated := make(map[string]bool)
	for _, s := range fn.Block {
		stmts = append(stmts, g.lang.TranslateIRMutation(s, g.scope)...)
		maps.Copy(mutated, codegen.MutatedFields(s))
	}
```

becomes:

```go
	var stmts []string
	mutated := make(map[string]bool)
	for _, s := range fn.Block {
		stmts = append(stmts, g.translateHandlerStmt(s)...)
		maps.Copy(mutated, codegen.MutatedFields(s))
	}
```

- [ ] **Step 5: Wire translateHandlerStmt into the remaining TranslateIRMutation callsites in handler-shaped contexts**

The full list (from `grep -n "TranslateIRMutation" codegen/platform/html/html.go`):

- `html.go:2259` — inside `emitScript`'s `$set_*` setter for `@change` handlers. Switch to `g.translateHandlerStmt(s)`.
- `html.go:2755` — context unclear from the grep alone; `Read` lines 2745-2770 first to verify it's in a handler context. If yes, switch.
- `html.go:2926` — likely a timer or change-handler emission; `Read` 2915-2940 to verify, then switch.
- `html.go:2952` — same, `Read` 2940-2970, switch if applicable.
- `html.go:3036` — same, `Read` 3025-3050, switch if applicable.

Leave any callsite that's NOT translating handler/timer/setter mutation bodies (e.g., a function-body translation for user-defined funcs) on `g.lang.TranslateIRMutation`. The deciding rule: if the IR stmt could contain a NoReactivity-injected `#__nN.<key> = expr` Assign, route through `translateHandlerStmt`. NoReactivity only injects inside Func.Block, comp.Funcs, var.Handlers (`internal/lower/walk.go:60-64`, `walkComponent` lines 76-80), so any path that translates such blocks needs the new dispatcher.

- [ ] **Step 6: Build**

Run: `go build ./codegen/platform/html/...`
Expected: success.

- [ ] **Step 7: Run html tests**

Run: `go test ./codegen/platform/html/`
Expected: PASS. NoReactivity still off; no Assign has `IsElementRef:true`; `translateHandlerStmt` always falls through to `lang.TranslateIRMutation`.

- [ ] **Step 8: Run full verify**

Run: `go tool verify`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add codegen/platform/html/html.go
git commit -m "$(cat <<'EOF'
Phase 4 Task 4: prop->DOM remap for reactive-update Assigns in handlers

Adds domWriteFor(componentName, key, el, value) mapping SNGL prop
names (text/badge.value, button.text/disabled, input.value/disabled,
progress.value) to the DOM-correct write expression. New
htmlGen.translateHandlerStmt(s) intercepts *ir.Assign with an
IsElementRef Select target, looks up the originating NodeInst via
g.idToNode[id], and routes through domWriteFor. addClickHandler,
addInputHandler, and the @change-setter / timer-handler /
related TranslateIRMutation callsites now go through
translateHandlerStmt so that — once NoReactivity is on — the injected
#__nN.value = expr lines hit the right DOM property. With NoReactivity
still off, every Assign falls through to lang.TranslateIRMutation;
behavior is byte-identical.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: Flip Capabilities to NoReactivity and stabilize tests

**Files:**
- Modify: `codegen/platform/html/html.go`
- Modify: `codegen/platform/html/html_test.go`
- Possibly modify: `cmd/sngl/testdata/*.txt` if a script test diverges.

- [ ] **Step 1: Flip Capabilities**

Edit `codegen/platform/html/html.go:51`:

```go
func (g *Generator) Capabilities() lower.Caps { return lower.Caps{} }
```

becomes:

```go
func (g *Generator) Capabilities() lower.Caps { return lower.Caps{NoReactivity: true} }
```

- [ ] **Step 2: Run html tests — expect deltas, not breakage**

Run: `go test ./codegen/platform/html/ -v`
Expected: PASS for `TestFixtures` (only checks for `<!DOCTYPE html>`, `<script>`, `let state = {`). For `TestTodoApp`, `TestFullExample`, `TestJSStateAndUpdaters`, `TestFullFixture`: PASS — none of the asserted substrings (`document.getElementById`, `addEventListener`, `state.<field>`, `function $<computed>()`) depend on whether prop updates come from `addTextUpdater`-registered functions or from inline handler-Assigns.

If any test FAILS, capture the actual generated HTML (stderr in `t.Errorf`) and diagnose:
- A missing `state.<field>` reference: handler block's `lang.TranslateIRMutation` for a non-reactive Assign should still emit `state.<field>`. If the injected `#__n*.value = …` translation displaced it, that's a bug — translateHandlerStmt should append both the user mutation AND the injected lines, not replace.
- A missing `addEventListener`: the click handler emission lost its body. Diagnose via `dump lowered --platform html` on the failing fixture.
- A missing `document.getElementById`: HTML's element-ref cache (`g.collectReferencedIDs` at html.go:2397) only sees `$N`-prefixed ids. NoReactivity uses `__n*` ids that are addressed via `document.querySelector` from JS lang, NOT cached by `getElementById`. That's expected — but if a test asserts `document.getElementById` was called for what is now a `__n*` id, it's an incidental assertion that needs updating to `document.querySelector` OR a new cache must be plumbed (defer that — the assertion is over-specific). Update the test assertion to be less brittle, e.g., assert the substring `data-sngl-id="__n` instead of `getElementById`.

- [ ] **Step 3: Add a positive assertion for the new path**

Edit `codegen/platform/html/html_test.go`. Append after `TestFullFixture` (line 189):

```go
func TestLoweredReactivityWiring(t *testing.T) {
	const src = `component main {
    var n int = 0
    text(value=string(n))
    button(text="+", @click { n = n + 1 })
}`
	doc, err := parser.Parse("counter.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	if len(diags) > 0 {
		t.Fatalf("check: %v", diags[0])
	}

	gen := &Generator{}
	resp, err := gen.Generate(&codegen.Request{
		Doc:  doc,
		Pkg:  pkg,
		Lang: codegen.LookupLang("none"),
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	var buf bytes.Buffer
	resp.Files[0].WriteTo(&buf)
	out := buf.String()

	// Static render must carry the data-sngl-id attribute for the
	// reactive text node so JS lang's IsElementRef path resolves it.
	if !strings.Contains(out, `data-sngl-id="__n0"`) {
		t.Errorf("missing data-sngl-id=\"__n0\" attr; output:\n%s", out)
	}
	// The click handler body must contain the prop-remapped DOM write
	// for the text node (textContent, not value).
	if !strings.Contains(out, `.textContent = String(state.n)`) &&
		!strings.Contains(out, `.textContent = String((state.n))`) {
		t.Errorf("missing .textContent = String(state.n) DOM write; output:\n%s", out)
	}
	// addTextUpdater must NOT have fired for __n0 — no $u_*_text
	// updater function should target it. (Match the legacy naming
	// pattern $u_<idsuffix>_text and rule it out.)
	if strings.Contains(out, "function $u___n0_text(") {
		t.Errorf("legacy $u___n0_text updater registered despite NoReactivity; output:\n%s", out)
	}
}
```

- [ ] **Step 4: Run the new test**

Run: `go test ./codegen/platform/html/ -run TestLoweredReactivityWiring -v`
Expected: PASS. If it fails, diagnose via the printed output — likely indicators:
- `data-sngl-id` missing → Task 3 didn't reach the `text` element renderer (probably `renderStaticBadge` vs `renderStaticText` mix-up; reading the actual rendered HTML names the element).
- `.textContent = String(state.n)` missing → `translateHandlerStmt` either fell through (idToNode lookup failed) or `domWriteFor` mapped wrong. Add a debug print of `idn.Name` and `node.Name` to confirm.
- Stale `$u___n0_text` updater present → Task 2's `!strings.HasPrefix(id, "__n")` guard didn't reach the relevant renderer.

- [ ] **Step 5: Run full project verify**

Run: `go tool verify`
Expected: PASS.

- [ ] **Step 6: Run script tests**

Run: `go test ./cmd/sngl/...`
Expected: PASS. The HTML compile script tests (`compile_html_cache_bust.txt`, `compile_html_minify.txt`) check for asset hashing and minification, not specific updater names — should be stable.

If `compile_html_cache_bust.txt` fails because asset hashing computed off the JS body changed: regenerate the expected hashes (the txtar fixture compares output bytes). Read the test, regenerate the hash by hand or via `go test -run … -update` if the test supports it.

- [ ] **Step 7: Commit**

```bash
git add codegen/platform/html/html.go codegen/platform/html/html_test.go
git commit -m "$(cat <<'EOF'
Phase 4 Task 5: HTML opts into NoReactivity for prop reactivity

Generator.Capabilities() returns lower.Caps{NoReactivity: true}, so
internal/lower runs the NoReactivity pass over every package HTML
compiles. Prop-update wiring for text/badge/button/input/progress
NodeInsts now flows through the lowered IR's injected #__nN.<key> = expr
Assigns rather than addTextUpdater/addAttrUpdater/addDisabledUpdater
registrations. New TestLoweredReactivityWiring asserts the data-sngl-id
attr renders, the prop->DOM remap kicks in (.textContent for text), and
the legacy updater-function name is gone. Existing fixture coverage
(TestTodoApp, TestFullExample, TestFullFixture, TestFixtures) keeps
asserting the stable JS surface (state.<field>, addEventListener, etc.)
and continues to pass — control-flow updates (If/For/disabled-on-non-
reactive paths) still go through HTML's existing dataflow.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Final verification + scope-out documentation

- [ ] **Step 1: Run the full project verify one more time**

Run: `go tool verify`
Expected: PASS.

- [ ] **Step 2: Run dump lowered --list to inspect HTML's resolved caps**

Run: `sngl dump lowered --platform html --lang none --list`
Expected output (or close):

```
caps: NoReactivity
passes: NoReactivity
```

If the binary isn't built: `go install ./cmd/sngl && sngl dump lowered --platform html --lang none --list`.

- [ ] **Step 3: Manually inspect a real fixture's lowered IR**

Run: `sngl dump lowered --platform html --lang none --format sngl examples/todo/todo.sngl | head -80`
Expected: every reactive prop should render as `text #__nN(value=...)` etc., and click/input handlers should contain extra `#__nN.<key> = …` Assigns.

- [ ] **Step 4: Confirm phase 4 first-slice shippable**

Phase 4's first slice is complete. Still TODO for phase 4 to fully match spec text:
- NoReactivity extension to cover `*ir.If` (reactive cond visibility) and `*ir.For` (reactive collection refresh). When that lands, HTML's `addIfUpdater` / `addElseUpdater` / `addForStmtUpdater` callsites can be removed under the same `__n*`-id guard pattern.
- Once If/For are covered, `g.dt`, `findAffectedUpdaters`, and the `optimizeIR`-built `MutationModel` become dead. At that point HTML can drop the `MutationModelEmitter` interface (per spec Phase 4 text). Track as Phase 4b.
- Phase 5 (Fyne port) is independent of Phase 4b.

This task closes phase 4's first slice. No commit unless `go tool verify` revealed issues that needed fixing.

---

## Self-Review Notes

Spec coverage (`docs/superpowers/specs/2026-05-02-codegen-lowering-design.md` §"Phase 4 — Port HTML"):

- "HTML platform opts into NoReactivity cap" → Task 5 Step 1.
- "Generator drops MutationModelEmitter" → **explicitly deferred to Phase 4b.** Reason in scope section: NoReactivity today only covers `NodeInst.Props` (`internal/lower/reactivity.go:113-130` — `collectFromNode` walks `n.Props` only, never `If.Cond` or `For.Iter`); HTML still needs `g.dt` to drive `addIfUpdater`/`addElseUpdater`/`addForStmtUpdater` until NoReactivity is extended.
- "consumes lowered IR directly" → Tasks 1+4. Static render reads pre-assigned `__nN`; handlers consume injected Assigns.
- "static visual tree from initial render plus injected updater statements bound to each mutation" → covered for the prop subset.
- "Existing snapshot tests serve as e2e regression coverage" → Task 5 Step 2.
- "codegen/analysis.go + codegen/deps.go originals stay in place" → confirmed; Task 5 doesn't touch them.

Out of scope (deferred to Phase 4b):
- NoReactivity extension to `*ir.If` and `*ir.For`.
- `MutationModelEmitter` interface removal from HTML.
- `g.dt` field, `findAffectedUpdaters`, `optimizeIR` in HTML.
- Fyne port (phase 5).
- `codegen/analysis.go` + `codegen/deps.go` deletion (phase 6).

Risks:

1. **Hybrid path complexity.** Two reactivity systems coexist in HTML for one phase: lowered-IR-driven for prop updates, dataflow-driven for control flow. Mitigation: tasks 2 and 4 keep both paths cleanly orthogonal — `__n*` ids only ever flow through the lowered path, `$N` ids only ever flow through the legacy path, and `g.idToNode` makes the boundary explicit.

2. **JS lang's querySelector form is not cached.** `document.querySelector('[data-sngl-id="__n0"]')` runs once per reactive update (potentially many times per frame). Today's HTML caches `getElementById` results in top-level `const`s. The new path is slower per write. For now: accept it (correctness over micro-optimization); a follow-up task can add a `const __n0 = document.querySelector(...)` cache emission analogous to `g.collectReferencedIDs`.

3. **Test brittleness on `getElementById` substring assertions.** Some tests in `html_test.go` (e.g., `TestTodoApp`) assert `document.getElementById`. If a fixture's only reactive ids become `__n*` (and therefore reach DOM via `querySelector`), the assertion would fail. Mitigation: Task 5 Step 2 surfaces this; if it happens, soften the assertion to `data-sngl-id` or `document.` substring.

4. **prop→DOM mapping table is incomplete.** `domWriteFor` lists only the props NoReactivity is known to inject for stdlib components (text, badge, button, input, progress). Non-stdlib raw HTML elements (`html.div(...)` user-attr `value`) fall through to the generic `el.<key> = value` write — which is JS lang's default and matches today's behavior for raw elements. If a stdlib component grows a new reactive prop, extend the table. Each unhandled (componentName, key) pair only causes a wrong DOM write under NoReactivity's lowered path — still observable in tests, not silent.

5. **renderRawElementIR may have id-allocation sites the plan missed.** Task 2 Step 5 explicitly audits `g.allocID()` callsites; Task 3 Step 3 explicitly audits open-tag writes. Both rely on grep + manual reading. If a raw-element render path skips the audit, NoReactivity-assigned ids could land on an element without `data-sngl-id` and Task 4's lookup would fall through to the generic write — observable as a wrong DOM write in tests, not silent.

6. **Handler block stmts that come from imported / library funcs.** NoReactivity walks `pkg.Funcs` and `comp.Funcs` (`walk.go:40-44, 76-80`), so any function called from a click handler that mutates a reactive var also gets injected `#__n*.<key> = …` Assigns. Task 4's `translateHandlerStmt` is wired into handler / input / setter / timer paths but NOT into the path that translates user-defined function bodies (the `emitJSFunc` / function-emission code path, around html.go:2200-2228). Verify by reading `emitJSFunc`: if it translates the func body via `lang.TranslateIRMutation`, that path also needs `translateHandlerStmt`. **Add to Task 4 Step 5 as part of the audit if it's missed.**

Type consistency check:

- `htmlGen.idToNode map[string]*ir.NodeInst` — declared Task 1 Step 1, initialized Task 1 Step 2, populated Task 1 Step 3 via `nodeID`, read Task 4 Step 2 via `translateHandlerStmt`.
- `nodeID(n *ir.NodeInst) string` — Task 1 Step 3, called in Tasks 1 Step 6 (badge), 2 Steps 1-5.
- `writeReactiveIDAttrs(b *strings.Builder, id string)` — Task 1 Step 4. Not actually called in subsequent tasks; Task 3 inlines the `data-sngl-id` write at each open-tag site for clarity. Either keep `writeReactiveIDAttrs` as dead code (the alternative inline-everywhere form is what's actually deployed) or delete it before Task 3 commit. **Decision: delete `writeReactiveIDAttrs` at end of Task 3 — the inline form is what every site uses, and dead code is worse than longer diffs.** Add a step to Task 3 to delete it.
- `domWriteFor(componentName, key, el, value string) string` — Task 4 Step 1, called only from `translateHandlerStmt`.
- `translateHandlerStmt(s ir.Stmt) []string` — Task 4 Step 2, called from `addClickHandler` (Task 4 Step 3), `addInputHandler` (Task 4 Step 4), and the auditable callsites in Task 4 Step 5.

(Self-review revision applied above: added explicit step in Task 3 to delete `writeReactiveIDAttrs` at commit time.)

---

### Task 3 addendum (per self-review): delete writeReactiveIDAttrs

After Task 3 Step 3 (the inline `data-sngl-id` writes are in place):

- [ ] **Task 3 Step 3.5: Delete writeReactiveIDAttrs**

Edit `codegen/platform/html/html.go`. Delete the `writeReactiveIDAttrs` function added in Task 1 Step 4 — the inline form at each open-tag site is what's actually used; the helper is dead code.

- [ ] **Task 3 Step 4-revised: Build**

Run: `go build ./codegen/platform/html/...`
Expected: success. (If the build fails because some reader expected the helper, restore it; the inline form should be sufficient otherwise.)

The Step 4 / Step 5 / Step 6 / Step 7 numbering in Task 3 above stays the same; this addendum simply slots a half-step between original Step 3 and Step 4.
