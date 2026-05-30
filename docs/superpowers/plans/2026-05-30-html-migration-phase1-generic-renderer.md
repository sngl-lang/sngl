# HTML Migration — Phase 1+2: generic renderer, delete renderStaticX, complete data-driven bodies

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Render every stdlib component in the html `--lang none` static path through the single generic `renderRawElementIR`, deleting the 17 `renderStaticX` helpers, and complete the six stdlib components whose `platform html { }` bodies currently drop their data props.

**Architecture:** After `passPlatformExtensionBody` + `InlinePure` (with Phase 0's param substitution), stdlib components arrive at codegen as native tags with params bound. `renderRawElementIR` already handles tag/id/style/attrs/children/void-elements/reactive-`value`-seeding/event-wiring. The remaining work is: (1) complete the data-driven bodies in `codegen/platform/html/html.sngl` for `select`/`radio`/`tabs`/`table`/`tree`/`modal` (their bodies are `{ slot }` and ignore `options`/`items`/`columns`/`rows`/`open`); (2) delete `renderStaticNode`'s switch + the 17 helpers and route stdlib-named native tags to the generic path, preserving `slot`/`window`/`timer` dispatch. Verified by rod/CDP browser tests asserting rendered DOM and interaction.

**Tech Stack:** Go, SNGL (`codegen/platform/html/html.sngl`), `codegen/platform/html` (`html.go`), rod/CDP via `codegen/platform/html/internal/webtest`.

**Predecessor:** Phase 0 (`docs/superpowers/plans/2026-05-30-html-migration-phase0-inlinepure-substitution.md`) — landed. Spec: `docs/superpowers/specs/2026-05-30-html-static-renderer-migration-design.md`.

---

## Ordering rationale

The completed bodies are masked by the helpers until the helpers are deleted, and deleting the helpers makes the six data-driven components render empty until their bodies are completed. So:

1. **Task 1–2 (tests first):** write the browser-test suite. THIN components (text/input/button/…) pass immediately (they already work via the helpers). The six data-driven components fail (empty/incomplete). This is the red baseline.
2. **Task 3 (big-bang deletion):** delete the switch + helpers, rewire dispatch. THIN components now render via the generic path — their browser tests must stay green (regression gate). The six stay red.
3. **Task 4 (complete bodies):** complete the six `.sngl` bodies — their browser tests go green.
4. **Task 5 (website):** end-to-end check that the real site renders all components (carousel reactivity is Phase 3, excluded here).

---

## File Structure

- `codegen/platform/html/component_dom_browser_test.go` — **create**. Table-driven per-component DOM browser test (all stdlib components).
- `codegen/platform/html/component_interaction_browser_test.go` — **create**. Interaction browser tests (input typing, select change).
- `codegen/platform/html/html.sngl` — **modify**. Complete `select`/`radio`/`tabs`/`table`/`tree`/`modal` bodies.
- `codegen/platform/html/html.go` — **modify**. Delete `renderStaticNode` switch + 17 `renderStaticX` helpers; rewire `renderIRNode`.

The browser tests reuse `compileAsyncHTML`-style setup and the `webtest` engine documented in the spec research; they are `//go:build !js` and skip when no browser is available.

---

### Task 1: Per-component DOM browser test (table-driven)

**Files:**
- Create: `codegen/platform/html/component_dom_browser_test.go`

- [ ] **Step 1: Write the table-driven DOM test**

Model the compile/serve/browser setup on the existing `*_browser_test.go` files (`async_browser_test.go`: parse → `checker.Check` → `lower.Lower(pkg, caps, lower.Options{Platform: gen.PlatformIdentifier()})` → `gen.Generate(&codegen.Request{Doc, Pkg, Lang: codegen.LookupLang("none")}, codegen.NewMemSink())`; then serve the html via an `http.ServeMux`, `webtest.New(mux)`, `engine.StartHeadless(1280,720)` (skip on error), `browser.NavigateRaw(engine.BaseURL()+"/")`, `browser.Page()`).

Create `codegen/platform/html/component_dom_browser_test.go`:

```go
//go:build !js

package html

import (
	"testing"
)

// componentDOMCase is one stdlib component rendered in isolation, with
// substrings that MUST appear in the live rendered DOM (outerHTML of body).
type componentDOMCase struct {
	name string
	src  string
	want []string // substrings required in document.body.outerHTML
}

var componentDOMCases = []componentDOMCase{
	{"text", `component main { text(value="HELLO") }`, []string{"<span", "HELLO"}},
	{"vbox", `component main { vbox { text(value="A") text(value="B") } }`, []string{"flex-direction:column", ">A<", ">B<"}},
	{"hbox", `component main { hbox { text(value="A") } }`, []string{"flex-direction:row"}},
	{"button", `component main { button(text="CLICK") }`, []string{"<button", "CLICK"}},
	{"input", `component main { var n = "Bob" input(:value=n) }`, []string{"<input"}},
	{"checkbox", `component main { var c = true checkbox(label="ok", checked=c) }`, []string{"type=\"checkbox\"", "ok"}},
	{"image", `component main { image(src="/x.png", alt="pic") }`, []string{"<img", "src=\"/x.png\"", "alt=\"pic\""}},
	{"link", `component main { link(text="Home", href="/") }`, []string{"<a", "href=\"/\"", "Home"}},
	{"select", `component main { var f = "b" select(options=["a","b","c"], :value=f, placeholder="pick") }`,
		[]string{"<select", "<option", ">a<", ">b<", ">c<", "pick"}},
	{"radio", `component main { var r = "y" radio(options=["x","y"], :value=r) }`,
		[]string{"<fieldset", "type=\"radio\"", "value=\"x\"", "value=\"y\""}},
	{"textarea", `component main { var t = "hi" textarea(:value=t, rows=3) }`, []string{"<textarea", "rows=\"3\""}},
	{"tabs", `component main { var sel = 0 tabs(items=["One","Two"], selected=sel) { text(value="panel") } }`,
		[]string{"role=\"tablist\"", "One", "Two", "panel"}},
	{"table", `component main { table(columns=["A","B"], rows=[["1","2"],["3","4"]]) }`,
		[]string{"<table", "<thead", "<tbody", ">A<", ">B<", ">1<", ">4<"}},
	{"tree", `component main { tree(items=["root","child"]) }`, []string{"<ul", "<li", "root", "child"}},
	{"modal", `component main { var o = true modal(open=o, title="Dialog") { text(value="body") } }`,
		[]string{"Dialog", "body"}},
	{"divider", `component main { divider() }`, []string{"<hr"}},
	{"badge", `component main { badge(value="3") }`, []string{"3"}},
}

func TestComponentDOM(t *testing.T) {
	for _, tc := range componentDOMCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			html := renderComponentHTML(t, tc.src)
			outer := bodyOuterHTMLInBrowser(t, html) // skips if no browser
			for _, want := range tc.want {
				if !containsSubstr(outer, want) {
					t.Errorf("%s: rendered DOM missing %q\n--- body.outerHTML ---\n%s", tc.name, want, outer)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Add the harness helpers in the same file**

Add `renderComponentHTML` (compile `output { none { html } }` + the source to html bytes — reuse the parse/check/lower/generate sequence from `async_browser_test.go`, with a no-import resolver), `bodyOuterHTMLInBrowser` (serve, `StartHeadless`, `t.Skipf` on browser error, navigate, `WaitStable`, return `page.MustElement("body").MustEval("() => this.outerHTML").String()`), and a `containsSubstr` (use `strings.Contains`). Copy the exact compile and `webtest` calls from `async_browser_test.go` (do not invent new APIs).

- [ ] **Step 3: Run the test — establish the red baseline**

Run: `go test ./codegen/platform/html/ -run TestComponentDOM -v`
Expected: THIN components PASS (text/vbox/hbox/button/input/checkbox/image/link/textarea/divider/badge). The six data-driven components FAIL (select/radio/tabs/table/tree/modal) — their bodies render empty/incomplete. Record which fail; that is the work for Task 4. If a THIN component fails, investigate before proceeding (its body or the generic path has a real gap).

- [ ] **Step 4: Commit**

```bash
git add codegen/platform/html/component_dom_browser_test.go
git commit -m "test(html): table-driven per-component DOM browser test

Red baseline: THIN components pass; data-driven select/radio/tabs/table/
tree/modal fail (bodies drop their data props). Phase 1 of html migration."
```

---

### Task 2: Interaction browser tests

**Files:**
- Create: `codegen/platform/html/component_interaction_browser_test.go`

- [ ] **Step 1: Write interaction tests**

Create `codegen/platform/html/component_interaction_browser_test.go`:

```go
//go:build !js

package html

import (
	"testing"

	rodproto "github.com/go-rod/rod/lib/proto"
)

// Input shows its initial bound value and updates the bound text on typing.
func TestInteraction_InputTwoWay(t *testing.T) {
	src := `component main {
    var name = "World"
    text(value="Hello, {name}!")
    input(:value=name)
}`
	b := startComponent(t, src) // compiles, serves, headless browser; skips if none
	defer b.Close()
	page := b.Page()

	input := page.MustElement("input")
	if v := input.MustEval("() => this.value").String(); v != "World" {
		t.Fatalf("initial input value: got %q, want %q", v, "World")
	}
	input.MustSelectAllText()
	input.MustInput("Sam")
	b.WaitStable(stableWait)
	if got := page.MustElement("span").MustText(); got != "Hello, Sam!" {
		t.Errorf("after typing: got %q, want %q", got, "Hello, Sam!")
	}
	if v := input.MustEval("() => this.value").String(); v != "Sam" {
		t.Errorf("input value after typing: got %q, want %q", v, "Sam")
	}
}

// Selecting a different option updates the bound value.
func TestInteraction_SelectChange(t *testing.T) {
	src := `component main {
    var fruit = "apple"
    text(value="picked: {fruit}")
    select(options=["apple","banana","cherry"], :value=fruit)
}`
	b := startComponent(t, src)
	defer b.Close()
	page := b.Page()

	sel := page.MustElement("select")
	sel.MustSelect("banana")
	b.WaitStable(stableWait)
	if got := page.MustElement("span").MustText(); got != "picked: banana" {
		t.Errorf("after select: got %q, want %q", got, "picked: banana")
	}
	_ = rodproto.InputMouseButtonLeft // keep import if unused after edits
}
```

- [ ] **Step 2: Add `startComponent`, `stableWait`, and reuse the compile helper**

Add a `stableWait` constant (e.g. `200 * time.Millisecond`) and `startComponent(t, src) *webtest.Browser` that does the same compile-serve-StartHeadless-navigate as Task 1's `bodyOuterHTMLInBrowser` but returns the `*webtest.Browser` for interaction (skip via `t.Skipf` when the browser is unavailable). Factor the shared compile-and-serve setup so Task 1 and Task 2 use it (DRY). Drop the `rodproto` import if `MustSelect`/`MustInput` make it unused.

- [ ] **Step 3: Run — input passes, select fails (red)**

Run: `go test ./codegen/platform/html/ -run TestInteraction -v`
Expected: `TestInteraction_InputTwoWay` PASS (Phase 0 + generic value-seeding + the two-way `e.target.value` fix already work). `TestInteraction_SelectChange` FAIL (select renders no options yet). The select test goes green in Task 4.

- [ ] **Step 4: Commit**

```bash
git add codegen/platform/html/component_interaction_browser_test.go
git commit -m "test(html): interaction browser tests for input two-way + select change"
```

---

### Task 3: Delete the renderStaticX switch and rewire dispatch (big-bang)

**Files:**
- Modify: `codegen/platform/html/html.go` (`renderIRNode`, `renderStaticNode`, delete 17 `renderStaticX` helpers)

- [ ] **Step 1: Rewire `renderIRNode` to route native tags to the generic path**

Replace `renderIRNode` (currently dispatches stdlib names to `renderStaticNode`) so that, after the user-component check, every node routes to the generic renderer, EXCEPT the three non-element stdlib names that need special handling. Read the current `renderIRNode` and `renderStaticNode` first. The new `renderIRNode`:

```go
func (g *htmlGen) renderIRNode(b *strings.Builder, n *ir.NodeInst, depth int) {
	if isUserIRComponent(n) {
		g.renderIRUserComponent(b, n, depth)
		return
	}
	// Non-element stdlib nodes that are not raw tags.
	switch n.Name {
	case "slot":
		g.renderSlotProjection(b, n, depth) // whatever the current "slot" case calls
		return
	case "window", "timer":
		return // handled at Generate level / no visual element
	}
	// Bodyless calls promoted from ir.CallStmt carry no Component back-ref;
	// resolve a user component by name. Everything else is a native element.
	if n.Component == nil {
		if comp := g.findIRComponent(n.Name); comp != nil && comp.AST != nil {
			n.Component = comp
			g.renderIRUserComponent(b, n, depth)
			return
		}
	}
	g.renderRawElementIR(b, n, depth)
}
```

Use the EXACT body of the current `renderStaticNode` `case "slot":` for `renderSlotProjection` (extract it, or inline it). Confirm by reading `renderStaticNode` what `slot`/`window`/`timer` currently do and preserve that behavior precisely.

- [ ] **Step 2: Delete `renderStaticNode` and the 17 helpers**

Delete the entire `renderStaticNode` function and these helpers (all only called from within that switch — verified): `renderStaticBox`, `renderStaticText`, `renderStaticButton`, `renderStaticInput`, `renderStaticCheckbox`, `renderStaticImage`, `renderStaticRadio`, `renderStaticToggle`, `renderStaticSelect`, `renderStaticTextarea`, `renderStaticTabs`, `renderStaticTable`, `renderStaticTree`, `renderStaticModal`, `renderStaticDatepicker`, `renderStaticConditionalContainer`. If a helper references a small unique sub-helper used nowhere else (e.g. `stripInterTagWhitespace` is used by the generic path — keep it), remove only the now-orphaned ones; let the compiler tell you (`go build ./...`).

- [ ] **Step 3: Build and run the Go suite**

Run: `go build ./... && go test ./codegen/platform/html/ ./internal/lower/ ./internal/optimize/ ./cmd/sngl/`
Expected: build clean. Some golden/string-asserting tests in `codegen/platform/html` may change because THIN components now emit via the generic path (e.g. whitespace `" />"` vs `"/>"`, attribute ordering). For each failing golden/string test, inspect the diff: if it is purely a cosmetic emission difference (whitespace, attr order, equivalent markup) for a THIN component, update the expectation; if it is a missing element/attribute or dropped content, STOP — that is a real regression to fix in the generic path before continuing.

- [ ] **Step 4: Run the DOM browser test — THIN green, six still red**

Run: `go test ./codegen/platform/html/ -run 'TestComponentDOM|TestInteraction' -v`
Expected: all THIN component subtests PASS via the generic path (regression gate for the deletion); `input` two-way PASS. The six data-driven components (select/radio/tabs/table/tree/modal) and `TestInteraction_SelectChange` still FAIL (bodies not yet completed). If any THIN component regressed, fix the generic renderer or that component's body now.

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/html/html.go
git commit -m "refactor(html): delete renderStaticX helpers; render via generic path

After PlatformExtensionBody + InlinePure, stdlib components arrive as
native tags; renderRawElementIR renders them. Route all native-tag nodes
through it; keep slot/window/timer dispatch. THIN components verified by
browser tests; data-driven bodies completed next."
```

---

### Task 4: Complete the six data-driven `.sngl` bodies

**Files:**
- Modify: `codegen/platform/html/html.sngl` (`sngl.select`, `sngl.radio`, `sngl.tabs`, `sngl.table`, `sngl.tree`, `sngl.modal`)

Do these one component at a time; after each, build (`go install ./cmd/sngl`) and run that component's browser subtest until green, then commit. The bodies below are the intended starting point, faithful to the deleted helpers (`renderStaticSelect`/`Radio`/`Tabs`/`Table`/`Tree`/`Modal` — read them in git history at `HEAD~1:codegen/platform/html/html.go` for exact semantics). Verify each lowers and renders correctly in the browser test; adjust SNGL syntax as the build/test directs (e.g. if a reactive `selected=(opt == value)` boolean-attribute form does not lower, fall back to the form the build accepts and re-verify).

- [ ] **Step 1 (select):** In `codegen/platform/html/html.sngl`, replace the `sngl.select` body with:

```
component sngl.select {
    platform html {
        html.select(disabled=disabled, @change { @change() }) {
            if placeholder != "" {
                html.option(value="", disabled=true, selected=true, textContent=placeholder)
            }
            for opt = options {
                html.option(value=opt, selected=(opt == value), textContent=opt)
            }
        }
    }
}
```

Run: `go install ./cmd/sngl && go test ./codegen/platform/html/ -run 'TestComponentDOM/select|TestInteraction_SelectChange' -v`
Expected: both PASS (options render; selecting updates the bound value). Commit: `feat(html): render select options in its platform body`.

- [ ] **Step 2 (radio):** Replace the `sngl.radio` body to emit, inside the existing `html.fieldset`, a per-option label+input (mirror `renderStaticRadio`):

```
component sngl.radio {
    platform html {
        html.fieldset(style={display="flex", flexDirection=direction, gap=8, border="none", padding=0}) {
            for opt = options {
                html.label(style={display="inline-flex", alignItems="center", gap=4}) {
                    html.input(type="radio", value=opt, checked=(opt == value), @change { @change() })
                    html.span(textContent=opt)
                }
            }
        }
    }
}
```

Run: `go install ./cmd/sngl && go test ./codegen/platform/html/ -run 'TestComponentDOM/radio' -v` → PASS. Commit: `feat(html): render radio options in its platform body`.

- [ ] **Step 3 (tabs):** Replace the `sngl.tabs` body to loop `items` into tab buttons inside the tablist, keeping the slot for the panel (mirror `renderStaticTabs`; consult `HEAD~1` for the active-tab styling):

```
component sngl.tabs {
    platform html {
        html.div(style={display="flex", flexDirection="column"}) {
            html.div(role="tablist", style={display="flex", gap=4}) {
                for i, label = items {
                    html.button(role="tab", textContent=label, aria-selected=(i == selected))
                }
            }
            html.div(role="tabpanel") { slot }
        }
    }
}
```

(If the `for i, label = items` indexed form or `aria-selected` reactive bool does not lower, adjust to the accepted form and re-verify.) Run the `TestComponentDOM/tabs` subtest → PASS. Commit: `feat(html): render tab items in its platform body`.

- [ ] **Step 4 (table):** Replace the `sngl.table` body to build `thead` from `columns` and `tbody` from `rows` (mirror `renderStaticTable`):

```
component sngl.table {
    platform html {
        html.table {
            html.thead {
                html.tr {
                    for col = columns { html.th(textContent=col) }
                }
            }
            html.tbody {
                for row = rows {
                    html.tr {
                        for cell = row { html.td(textContent=cell) }
                    }
                }
            }
        }
    }
}
```

Run `TestComponentDOM/table` → PASS. Commit: `feat(html): render table columns/rows in its platform body`.

- [ ] **Step 5 (tree):** Replace the `sngl.tree` body to loop `items` into `<li>`s (mirror `renderStaticTree`):

```
component sngl.tree {
    platform html {
        html.ul {
            for item = items { html.li(textContent=item) }
        }
    }
}
```

Run `TestComponentDOM/tree` → PASS. Commit: `feat(html): render tree items in its platform body`.

- [ ] **Step 6 (modal):** Replace the `sngl.modal` body to add the optional title and the open/closed display (mirror `renderStaticModal`; the conditional open uses the same reactive-`if`/display mechanism the generic path supports):

```
component sngl.modal {
    platform html {
        if open {
            html.div(class="modal-overlay", style={position="fixed", inset=0, display="flex", alignItems="center", justifyContent="center"}) {
                html.div(class="modal-content") {
                    if title != "" { html.h3(textContent=title) }
                    slot
                }
            }
        }
    }
}
```

(`if open { … }` makes the modal a reactive conditional — that path is finished in Phase 3; for Phase 1+2 verify the *initial* render with `open=true` shows title+body, and `open=false` shows nothing. If reactive toggling of `open` does not yet work, that is expected and covered by Phase 3 — the DOM test uses `open=true`.) Run `TestComponentDOM/modal` → PASS. Commit: `feat(html): render modal title/open in its platform body`.

- [ ] **Step 7: Full browser suite green**

Run: `go test ./codegen/platform/html/ -run 'TestComponentDOM|TestInteraction' -v`
Expected: all subtests PASS.

---

### Task 5: Website end-to-end verification

**Files:** none (verification + any body fixes surfaced)

- [ ] **Step 1: Regenerate the website and assert components render**

Run:
```bash
go install ./cmd/sngl
rm -rf /tmp/site_phase1 && go tool sngl generate --platform html --lang none --out /tmp/site_phase1 website.sngl
```
Expected: exit 0. Then check the playground page renders its real `<select id="examples">` with `<option>`s:
```bash
grep -c 'id="examples"' /tmp/site_phase1/playground.html   # expect 1
grep -c '<option' /tmp/site_phase1/playground.html          # expect >= 1
```
And spot-check a content page still has body + CSS:
```bash
grep -c 'component-card' /tmp/site_phase1/components/index.html   # expect > 0
grep -c 'rel="stylesheet"' /tmp/site_phase1/components/index.html # expect 1
```

- [ ] **Step 2: Confirm no `renderStaticX` symbols remain**

Run: `grep -rn "renderStatic[A-Z]" codegen/platform/html/*.go | grep -v _test`
Expected: only `renderStaticNode` is gone and no `renderStaticX` helper definitions remain. (No output, or only unrelated matches.)

- [ ] **Step 3: Full repo test sweep**

Run: `go test ./...`
Expected: all pass. Address any remaining failures (cosmetic golden updates only after confirming equivalence).

- [ ] **Step 4: Commit any fixes**

```bash
git add -A && git commit -m "fix(html): complete data-driven bodies; website renders all components"
```
(Skip if Tasks 3–4 already left the tree clean and passing.)

---

## Self-Review

- **Spec coverage:** Implements spec §Phase 1 (generic renderer absorbs helpers — confirmed already-capable, so the work is deletion + dispatch), §Phase 2 (delete switch/helpers; `select` `options`→body, plus the other five data-driven bodies the research found incomplete), and the verification strategy (browser tests per component + interaction). Phase 3 (reactive slots / carousel / `__root`) is explicitly out of scope here and noted where `modal`'s reactive `open` and the carousel depend on it.
- **Placeholder scan:** Bodies and tests are given as concrete code. The one deliberate latitude is "adjust SNGL syntax if a reactive boolean-attribute form does not lower" — this is a real verification step with a defined fallback (use the accepted form, re-run the browser test), not a placeholder; it exists because exact SNGL body lowering for reactive attributes must be confirmed against the compiler, and the browser test is the objective gate.
- **Type/name consistency:** `renderRawElementIR`, `renderIRNode`, `renderIRUserComponent`, `findIRComponent`, `isUserIRComponent`, `webtest.New/StartHeadless/Browser`, `compileAsyncHTML`-style setup — all match current symbols (verified in research). `renderSlotProjection` is a name to assign to the extracted current `case "slot":` body; the engineer must use the existing slot-projection code, not invent it.
- **Risk:** Deleting helpers may surface cosmetic golden diffs (whitespace/attr order) for THIN components — handled in Task 3 Step 3 with an explicit "cosmetic → update; missing content → stop" rule. Data-driven body lowering is gated by browser tests per component (Task 4).
