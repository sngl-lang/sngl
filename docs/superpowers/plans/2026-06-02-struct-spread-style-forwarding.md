# Struct Spread Lowering + Explicit `...style` Forwarding — Implementation Plan (Phase 1)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a caller's `style` reach the element a stdlib wrapper renders, the explicit way — platform `.sngl` bodies forward `...style`, and a capability-gated lowering pass flattens that spread at compile time.

**Architecture:** A new `flatten_struct_spread` lowering pass (gated by `Caps.NoStructSpread`) rewrites struct literals that contain `...literal` spreads into flat literals, with last-write-wins semantics. The html wrapper bodies in `html.sngl` forward `...style` onto their root element. This removes the temporary "magic" `forwardStyle` inliner hack. The color-struct CSS fix (already in the tree) is kept.

**Tech Stack:** Go; SNGL's `internal/lower` capability-driven IR passes; `internal/htmlutil` CSS builder; `codegen/platform/html` DOM tests (go-rod CDP).

**Scope note — Phase 1 vs Phase 2.** This plan covers the part that fixes the reported bug and is fully, concretely specifiable: the capability, the **compile-time (literal-operand)** flatten path, the html `...style` forwarding, removal of the A stopgap, and keeping the color fix. The wrapper case is *always* a literal operand after inlining, so this delivers the working styled output. Deferred to a **follow-up plan** (see "Phase 2 — deferred" at the end), because they need emitter-wiring exploration and don't block the fix:

- runtime `merge<Struct>` codegen for opaque spread operands (across Go/JS/Kotlin),
- removing the per-backend spread emitters,
- native (`gtk4`/`fyne`/`android`) `...style` forwarding (blocked on the native `style`-prop dependency).

Phase 1 leaves opaque (non-literal) struct spreads untouched in the IR — exactly today's behavior (no regression; Go already drops them) — until Phase 2 adds the runtime merge.

Reference spec: `docs/superpowers/specs/2026-06-02-struct-spread-style-forwarding-design.md`.

---

## File Structure

- **Modify** `internal/lower/caps.go` — add `NoStructSpread` field, `Merge` clause, `String` clause.
- **Create** `internal/lower/flatten_struct_spread.go` — the pass (`passFlattenStructSpread`, `lowerFlattenStructSpread`, `flattenSpreadExpr`, `flattenSpreadStmts`, `flattenStructLit`).
- **Modify** `internal/lower/lower.go` — register the pass after `passNoInlineComponents`.
- **Modify** each platform's `Capabilities()` to set `NoStructSpread: true`:
  `codegen/platform/html/html.go`, `codegen/platform/bubbletea/bubbletea.go`, `codegen/platform/fyne/fyne.go`, `codegen/platform/gtk4/gtk4.go`, `codegen/platform/android/android.go`, `codegen/platform/none/none.go`.
- **Modify** `codegen/platform/html/html.sngl` — add `...style` to wrapper roots.
- **Modify** `internal/lower/inline_pure.go` and `internal/lower/inline_components.go` — remove the A `forwardStyle` hack.
- **Create** `testdata/struct_spread_flatten.sngl` — lowering fixtures.
- **Keep** `internal/htmlutil/helpers.go` `colorStructToCSS` and the two `codegen/platform/html/component_dom_browser_test.go` cases already added (they now validate the B path).

---

## Task 1: Add the `NoStructSpread` capability

**Files:**
- Modify: `internal/lower/caps.go`
- Modify: `codegen/platform/html/html.go:56`, `codegen/platform/bubbletea/bubbletea.go:47`, `codegen/platform/fyne/fyne.go:57`, `codegen/platform/gtk4/gtk4.go:143`, `codegen/platform/android/android.go:101`, `codegen/platform/none/none.go:24`
- Test: `internal/lower/caps_test.go` (create if absent)

- [ ] **Step 1: Write the failing test**

Create or append to `internal/lower/caps_test.go`:

```go
package lower

import (
	"strings"
	"testing"
)

func TestNoStructSpreadMergeAndString(t *testing.T) {
	merged := Caps{}.Merge(Caps{NoStructSpread: true})
	if !merged.NoStructSpread {
		t.Fatal("Merge should OR NoStructSpread to true")
	}
	if !strings.Contains(Caps{NoStructSpread: true}.String(), "NoStructSpread") {
		t.Fatalf("String() must list NoStructSpread, got %q", Caps{NoStructSpread: true}.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/lower/ -run TestNoStructSpreadMergeAndString -v`
Expected: FAIL — compile error `unknown field 'NoStructSpread'`.

- [ ] **Step 3: Add the field, Merge clause, and String clause**

In `internal/lower/caps.go`, add the field at the end of the `Caps` struct (after `NoImplicitRecv`):

```go
	NoImplicitRecv     bool // method calls with implicit receiver → explicit Args[0]
	NoStructSpread     bool // struct-literal spreads (`{...x}`) → flattened literal / merge<Struct> call
```

Add the clause to `Merge` (after the `NoImplicitRecv` line):

```go
		NoImplicitRecv:     c.NoImplicitRecv || other.NoImplicitRecv,
		NoStructSpread:     c.NoStructSpread || other.NoStructSpread,
```

Add to `String()` (after the `NoImplicitRecv` block, before `NoTimer`):

```go
	if c.NoStructSpread {
		parts = append(parts, "NoStructSpread")
	}
```

- [ ] **Step 4: Set `NoStructSpread: true` on every current platform**

Each edit adds `NoStructSpread: true` to the returned `lower.Caps{...}` literal:

- `codegen/platform/html/html.go:56` — append `, NoStructSpread: true` inside the existing `lower.Caps{...}`.
- `codegen/platform/bubbletea/bubbletea.go:47` — same.
- `codegen/platform/fyne/fyne.go:57` — same.
- `codegen/platform/gtk4/gtk4.go:143` — same.
- `codegen/platform/android/android.go:101` — same.
- `codegen/platform/none/none.go:24` — change `return lower.Caps{}` to `return lower.Caps{NoStructSpread: true}`.

- [ ] **Step 5: Run test + build**

Run: `go test ./internal/lower/ -run TestNoStructSpreadMergeAndString -v && go build ./...`
Expected: PASS; build clean.

- [ ] **Step 6: Commit**

```bash
git add internal/lower/caps.go internal/lower/caps_test.go codegen/platform/*/*.go
git commit -m "feat(lower): add NoStructSpread capability, enable on all platforms"
```

---

## Task 2: The `flatten_struct_spread` pass (compile-time path)

**Files:**
- Create: `internal/lower/flatten_struct_spread.go`
- Modify: `internal/lower/lower.go` (register pass)
- Test: `internal/lower/flatten_struct_spread_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/lower/flatten_struct_spread_test.go`:

```go
package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func intLit(n string) *ir.Literal { return &ir.Literal{Type: ir.TypInt, Raw: n} }

// names returns the field names of a flat struct literal in order.
func names(sl *ir.StructLit) []string {
	var out []string
	for _, f := range sl.Fields {
		out = append(out, f.Name)
	}
	return out
}

func TestFlattenStructLitLiteralSpread(t *testing.T) {
	// {a=1, ...{a=0, b=2}}  →  {a=0, b=2}  (literal spread splices written fields; last wins)
	in := &ir.StructLit{Fields: []ir.FieldInit{
		{Name: "a", Value: intLit("1")},
		{Spread: true, Value: &ir.StructLit{Fields: []ir.FieldInit{
			{Name: "a", Value: intLit("0")},
			{Name: "b", Value: intLit("2")},
		}}},
	}}
	out := flattenSpreadExpr(in).(*ir.StructLit)
	got := names(out)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("field names: got %v want [a b]", got)
	}
	if a := out.Fields[0]; a.Spread || a.Value.(*ir.Literal).Raw != "0" {
		t.Fatalf("a should be 0 (last write wins), got %+v", a)
	}
}

func TestFlattenStructLitExplicitZeroWins(t *testing.T) {
	// {...{a=1}, a=0}  →  {a=0}  (explicit, last)
	in := &ir.StructLit{Fields: []ir.FieldInit{
		{Spread: true, Value: &ir.StructLit{Fields: []ir.FieldInit{{Name: "a", Value: intLit("1")}}}},
		{Name: "a", Value: intLit("0")},
	}}
	out := flattenSpreadExpr(in).(*ir.StructLit)
	if len(out.Fields) != 1 || out.Fields[0].Value.(*ir.Literal).Raw != "0" {
		t.Fatalf("want {a=0}, got %+v", out.Fields)
	}
}

func TestFlattenStructLitOpaqueLeftUntouched(t *testing.T) {
	// {...someVar} with an opaque operand stays a spread (Phase 2 territory).
	in := &ir.StructLit{Fields: []ir.FieldInit{
		{Spread: true, Value: &ir.Ident{Name: "someVar"}},
	}}
	out := flattenSpreadExpr(in).(*ir.StructLit)
	if len(out.Fields) != 1 || !out.Fields[0].Spread {
		t.Fatalf("opaque spread must be left intact, got %+v", out.Fields)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/lower/ -run TestFlattenStructLit -v`
Expected: FAIL — `undefined: flattenSpreadExpr`.

- [ ] **Step 3: Create the pass file**

Create `internal/lower/flatten_struct_spread.go`:

```go
package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

var passFlattenStructSpread = pass{
	name:    "NoStructSpread",
	enabled: func(c Caps) bool { return c.NoStructSpread },
	apply:   lowerFlattenStructSpread,
}

// lowerFlattenStructSpread rewrites struct literals that contain `...literal`
// spread fields into flat literals (last-write-wins). Spreads whose operand is
// not itself a struct literal (opaque runtime values) are left intact for a
// later runtime-merge pass; today's backends handle those as they do now.
func lowerFlattenStructSpread(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	walkPackage(pkg, walkFuncs{
		expr:  flattenSpreadExpr,
		stmts: func(stmts []ir.Stmt) []ir.Stmt { return flattenSpreadStmts(stmts, flattenSpreadExpr) },
	})
	return nil
}

// flattenSpreadExpr recurses into every sub-expression, then collapses any
// spread-bearing struct literal it produced. Mirrors rewriteUnitExpr's
// traversal so every expr position is covered.
func flattenSpreadExpr(e ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *ir.Binary:
		x.Left = flattenSpreadExpr(x.Left)
		x.Right = flattenSpreadExpr(x.Right)
	case *ir.Unary:
		x.Operand = flattenSpreadExpr(x.Operand)
	case *ir.Ternary:
		x.Cond = flattenSpreadExpr(x.Cond)
		x.Then = flattenSpreadExpr(x.Then)
		x.Else = flattenSpreadExpr(x.Else)
	case *ir.Call:
		if x.Receiver != nil {
			x.Receiver = flattenSpreadExpr(x.Receiver)
		}
		for i := range x.Args {
			x.Args[i].Value = flattenSpreadExpr(x.Args[i].Value)
		}
	case *ir.Conversion:
		x.Operand = flattenSpreadExpr(x.Operand)
	case *ir.Select:
		x.Operand = flattenSpreadExpr(x.Operand)
	case *ir.Index:
		x.Operand = flattenSpreadExpr(x.Operand)
		x.Idx = flattenSpreadExpr(x.Idx)
	case *ir.ListLit:
		for i := range x.Elems {
			x.Elems[i] = flattenSpreadExpr(x.Elems[i])
		}
	case *ir.StructLit:
		for i := range x.Fields {
			if x.Fields[i].Value != nil {
				x.Fields[i].Value = flattenSpreadExpr(x.Fields[i].Value)
			}
		}
		return flattenStructLit(x)
	case *ir.Spread:
		x.Operand = flattenSpreadExpr(x.Operand)
	case *ir.MapLitIR:
		for i := range x.Entries {
			x.Entries[i].Key = flattenSpreadExpr(x.Entries[i].Key)
			x.Entries[i].Value = flattenSpreadExpr(x.Entries[i].Value)
		}
	case *ir.Lambda:
		if x.Func != nil {
			x.Func.Block = flattenSpreadStmts(x.Func.Block, flattenSpreadExpr)
		}
	case *ir.Closure:
		if x.State != nil {
			for i := range x.State.Fields {
				if x.State.Fields[i].Value != nil {
					x.State.Fields[i].Value = flattenSpreadExpr(x.State.Fields[i].Value)
				}
			}
		}
		if x.Func != nil {
			x.Func.Block = flattenSpreadStmts(x.Func.Block, flattenSpreadExpr)
		}
	case *ir.Literal, *ir.Ident, *ir.ContextRead:
		// Terminal — no struct literals beneath.
	default:
		panic(fmt.Sprintf("flattenSpreadExpr: unhandled %T", x))
	}
	return e
}

// flattenStructLit collapses a spread-bearing struct literal into a flat one
// when every spread operand is itself a struct literal. Returns sl unchanged
// when it has no spreads or any spread operand is opaque. Field values are
// assumed already flattened (flattenSpreadExpr recurses first). Last write by
// name wins; first-seen position is preserved.
func flattenStructLit(sl *ir.StructLit) *ir.StructLit {
	hasSpread, allLiteral := false, true
	for _, f := range sl.Fields {
		if f.Spread {
			hasSpread = true
			if _, ok := f.Value.(*ir.StructLit); !ok {
				allLiteral = false
			}
		}
	}
	if !hasSpread || !allLiteral {
		return sl
	}
	var order []string
	vals := map[string]ir.FieldInit{}
	set := func(fi ir.FieldInit) {
		if _, seen := vals[fi.Name]; !seen {
			order = append(order, fi.Name)
		}
		vals[fi.Name] = fi
	}
	for _, f := range sl.Fields {
		if f.Spread {
			for _, g := range f.Value.(*ir.StructLit).Fields {
				set(g) // literal spread splices every written field
			}
			continue
		}
		set(f)
	}
	flat := make([]ir.FieldInit, 0, len(order))
	for _, name := range order {
		flat = append(flat, vals[name])
	}
	out := *sl
	out.Fields = flat
	return &out
}
```

- [ ] **Step 4: Add the statement walker**

Append to `internal/lower/flatten_struct_spread.go` (this mirrors `rewriteUnitStmts` exactly so every expr position in every statement shape is visited):

```go
func flattenSpreadStmts(stmts []ir.Stmt, rewrite func(ir.Expr) ir.Expr) []ir.Stmt {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.Assign:
			n.Value = rewrite(n.Value)
		case *ir.LocalVar:
			if n.Init != nil {
				n.Init = rewrite(n.Init)
			}
		case *ir.Return:
			if n.Value != nil {
				n.Value = rewrite(n.Value)
			}
		case *ir.If:
			n.Cond = rewrite(n.Cond)
			n.Body = flattenSpreadStmts(n.Body, rewrite)
			n.Else = flattenSpreadStmts(n.Else, rewrite)
		case *ir.For:
			n.Iter = rewrite(n.Iter)
			n.Body = flattenSpreadStmts(n.Body, rewrite)
			n.Else = flattenSpreadStmts(n.Else, rewrite)
		case *ir.PlatformFilter:
			n.Body = flattenSpreadStmts(n.Body, rewrite)
		case *ir.NodeInst:
			for i := range n.Props {
				if n.Props[i].Value != nil {
					n.Props[i].Value = rewrite(n.Props[i].Value)
				}
			}
			if n.Key != nil {
				n.Key = rewrite(n.Key)
			}
			if n.Ref != nil {
				n.Ref = rewrite(n.Ref)
			}
			n.Children = flattenSpreadStmts(n.Children, rewrite)
			for i := range n.Handlers {
				if n.Handlers[i].Func != nil {
					n.Handlers[i].Func.Block = flattenSpreadStmts(n.Handlers[i].Func.Block, rewrite)
				}
			}
		case *ir.SlotInst:
			n.Children = flattenSpreadStmts(n.Children, rewrite)
		case *ir.ErrorBoundary:
			n.Children = flattenSpreadStmts(n.Children, rewrite)
			if n.Handler != nil && n.Handler.Func != nil {
				n.Handler.Func.Block = flattenSpreadStmts(n.Handler.Func.Block, rewrite)
			}
		case *ir.Emit:
			for i := range n.Args {
				n.Args[i].Value = rewrite(n.Args[i].Value)
			}
		case *ir.CallStmt:
			if n.Call != nil {
				if n.Call.Receiver != nil {
					n.Call.Receiver = rewrite(n.Call.Receiver)
				}
				for i := range n.Call.Args {
					n.Call.Args[i].Value = rewrite(n.Call.Args[i].Value)
				}
			}
		case *ir.Window:
			if n.Href != nil {
				n.Href = rewrite(n.Href)
			}
			if n.Title != nil {
				n.Title = rewrite(n.Title)
			}
			if n.Favicon != nil {
				n.Favicon = rewrite(n.Favicon)
			}
			n.Body = flattenSpreadStmts(n.Body, rewrite)
		case *ir.Toggle:
			n.Target = rewrite(n.Target)
		case *ir.ContextProvider:
			n.Value = rewrite(n.Value)
			n.Children = flattenSpreadStmts(n.Children, rewrite)
		default:
			panic(fmt.Sprintf("flattenSpreadStmts: unhandled %T", n))
		}
	}
	return stmts
}
```

- [ ] **Step 5: Run unit tests to verify they pass**

Run: `go test ./internal/lower/ -run TestFlattenStructLit -v`
Expected: PASS (all three).

- [ ] **Step 6: Register the pass after `passNoInlineComponents`**

In `internal/lower/lower.go`, in the `passes` slice, insert `passFlattenStructSpread` immediately after `passNoInlineComponents`:

```go
	passInlinePure,
	passNoInlineComponents,
	passFlattenStructSpread,
	passNoImplicitRecv,
```

- [ ] **Step 7: Update the pass-order test if present**

Run: `go test ./internal/lower/ -run TestPass -v`
If `lower_test.go` asserts an ordered pass list (e.g. `TestPassOrder`/`PassExecutionOrder`), add `"NoStructSpread"` to the expected list in the same position (after `NoInlineComponents`). Re-run until PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/lower/flatten_struct_spread.go internal/lower/flatten_struct_spread_test.go internal/lower/lower.go internal/lower/lower_test.go
git commit -m "feat(lower): flatten_struct_spread pass — compile-time literal-spread flattening"
```

---

## Task 3: Add a lowering fixture for the end-to-end flatten

**Files:**
- Create: `testdata/struct_spread_flatten.sngl`
- Test: existing `internal/testutil` fixture runner (no new test code)

- [ ] **Step 1: Write the fixture**

Create `testdata/struct_spread_flatten.sngl`. This exercises a literal spread through the real parse→check→lower pipeline and asserts the flattened CSS in HTML output is not broken. (The `// FOLD`/`// ERROR` directives are documented in `internal/testutil/sample.go`; this fixture just needs to compile and type-check cleanly.)

```
import "platform://html"

component main {
    html.div(style={display="flex", ...{gap = 6, padding = 12}}) {
        html.span(textContent="x")
    }
}
```

- [ ] **Step 2: Verify it type-checks and the spread flattens in HTML output**

Run:
```bash
go run ./cmd/sngl generate --lang none --platform html testdata/struct_spread_flatten.sngl -o /tmp/spreadfix
grep -o 'style="[^"]*"' /tmp/spreadfix/index.html
```
Expected: the `div` style contains `display:flex`, `gap:6px`, and `padding:12px` (the spread's fields merged in) — not just `display:flex`.

- [ ] **Step 3: Run the testdata suite**

Run: `go test ./internal/testutil/... ./... -run 'Sample|Testdata|Fixture' 2>&1 | tail -20`
Expected: no new failures referencing `struct_spread_flatten.sngl`.

- [ ] **Step 4: Commit**

```bash
git add testdata/struct_spread_flatten.sngl
git commit -m "test(lower): fixture for literal struct-spread flattening"
```

---

## Task 4: Forward `...style` from html wrappers; remove the A stopgap

**Files:**
- Modify: `codegen/platform/html/html.sngl`
- Modify: `internal/lower/inline_pure.go` (remove `forwardStyle` call), `internal/lower/inline_components.go` (remove `forwardStyle` helper)
- Test: `codegen/platform/html/component_dom_browser_test.go` (cases already present)

- [ ] **Step 1: Remove the A `forwardStyle` hack**

In `internal/lower/inline_pure.go`, delete the entire block added previously (the comment beginning "Forward the wrapper instance's `style` prop onto the body's root" through its `for _, prop := range callsite.Props { ... }` loop), leaving the function ending at the existing `return body, nil`.

In `internal/lower/inline_components.go`, delete the `forwardStyle` helper function (the `func forwardStyle(ni *ir.NodeInst, style ir.Expr) { ... }` block and its doc comment).

- [ ] **Step 2: Verify the hack is gone and the build is clean**

Run: `grep -rn forwardStyle internal/lower/ ; go build ./...`
Expected: no matches; build clean.

- [ ] **Step 3: Add `...style` to each html wrapper root**

In `codegen/platform/html/html.sngl`, edit each wrapper's root element. **Precedence rule:** structural props that must be locked go *after* `...style`; structural defaults the caller may override go *before* `...style`. Apply these exact root-style literals (leave the rest of each body unchanged):

| Component | Root style literal |
|-----------|--------------------|
| `sngl.vbox` | `style={...style, display="flex", flexDirection="column"}` |
| `sngl.hbox` | `style={...style, display="flex", flexDirection="row"}` |
| `sngl.stack` | `style={...style, position="relative"}` |
| `sngl.spacer` | `style={...style, flex=1}` |
| `sngl.scroll` | `style={...style, overflow="auto"}` |
| `sngl.text` (span) | add `style={...style}` |
| `sngl.button` (button) | add `style={...style}` |
| `sngl.image` (img) | add `style={...style}` |
| `sngl.input` (input) | add `style={...style}` |
| `sngl.checkbox` (label) | `style={display="inline-flex", alignItems="center", gap=4, ...style}` |
| `sngl.radio` (fieldset) | `style={display="flex", flexDirection=direction, gap=8, border="none", padding=0, ...style}` |
| `sngl.toggle` (label) | `style={display="inline-flex", alignItems="center", gap=8, ...style}` |
| `sngl.select` (select) | add `style={...style}` |
| `sngl.textarea` (textarea) | add `style={...style}` |
| `sngl.progress` (progress) | add `style={...style}` |
| `sngl.spinner` (outer span) | `style={display="inline-flex", alignItems="center", gap=8, ...style}` |
| `sngl.badge` (span) | `style={display="inline-block", padding="2px 8px", borderRadius=12, ...style}` |
| `sngl.tabs` (outer div) | `style={...style, display="flex", flexDirection="column"}` |
| `sngl.link` (a) | add `style={...style}` |
| `sngl.divider` (hr) | add `style={...style}` |

- [ ] **Step 4: Build and confirm every wrapper type-checks**

Run: `go build ./... && go run ./cmd/sngl generate --lang none --platform html testdata/component_simple.sngl -o /tmp/wrapcheck 2>&1 | head`
Expected: no error. If a wrapper errors with `unknown prop "style"` or `unknown identifier "style"`, that stdlib component lacks a `style Style` param — remove the `...style` from that one wrapper and note it (no param to forward).

- [ ] **Step 5: Verify the reported lesson renders fully styled**

Create `/tmp/lesson.sngl` with the "Building a Component Library" seed (from `docs/learn/tour.md`, the `Card`/`Stat`/`main` block), then:
```bash
go run ./cmd/sngl generate --lang none --platform html /tmp/lesson.sngl -o /tmp/lesson
grep -o 'style="[^"]*"' /tmp/lesson/index.html
```
Expected: outer vbox style includes `background-color:#f0f2f5`, `gap:10px`, `padding:16px`; the card includes `background-color:#ffffff`, `border-radius:8px`; stat labels include `color:#555555`.

- [ ] **Step 6: Run the html DOM tests (style-merge + color via the B path)**

Run: `go test ./codegen/platform/html/ -run 'TestComponentDOM|DOM' -v 2>&1 | tail -15`
Expected: PASS, including the `vbox-style` and `text-color` cases.

- [ ] **Step 7: Commit**

```bash
git add codegen/platform/html/html.sngl internal/lower/inline_pure.go internal/lower/inline_components.go
git commit -m "feat(html): wrappers forward ...style on root; drop the forwardStyle stopgap"
```

---

## Task 5: Full verification

**Files:** none (verification only)

- [ ] **Step 1: Build everything**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 2: Run the full suite, failures only**

Run: `go test ./... 2>&1 | grep -iv '^ok\|no test files' | tail -40`
Expected: the only failure is the pre-existing, unrelated `TestDocSNGLFormat` (a `docs/learn/tour.md` formatting drift — `func remainingCount()` parens + a trailing comma — that fails on a clean tree too). No other failures.

- [ ] **Step 3: Confirm the color fix is intact**

Run: `grep -n 'colorStructToCSS' internal/htmlutil/helpers.go`
Expected: present (kept from the earlier fix; renders `#hex` → `color{}` structs).

- [ ] **Step 4: Commit any residual (e.g. the kept color fix + DOM cases) if not yet committed**

```bash
git add -A
git status   # review
git commit -m "test(html): keep colorStructToCSS + style-forwarding DOM cases" || echo "nothing to commit"
```

---

## Self-review (author checklist — completed)

- **Spec coverage (Phase 1 slice):** capability gate (Task 1) ✓; compile-time flatten pass + pass position (Task 2) ✓; literal-spread + explicit-last-wins + opaque-left-intact semantics (Task 2 tests) ✓; html `...style` forwarding with precedence-by-position (Task 4) ✓; remove A magic (Task 4) ✓; keep color fix (Task 5) ✓; fixtures + DOM + e2e (Tasks 3–5) ✓.
- **Deferred, by design:** runtime `merge<Struct>` codegen, removing per-backend spread emitters, native `...style` — see below.
- **Placeholder scan:** none — every code step has complete code; the html edits are an exact per-wrapper table.
- **Type consistency:** `flattenSpreadExpr`/`flattenSpreadStmts`/`flattenStructLit`/`passFlattenStructSpread`/`lowerFlattenStructSpread`/`Caps.NoStructSpread` used consistently across tasks; `ir.Conversion.Operand` (not `.Value`) matches `rewriteUnitExpr`.

---

## Phase 2 — deferred (needs its own plan)

These are real, in-scope-for-the-spec, but deferred from this plan because they require emitter-wiring exploration and are independently shippable. Recommended approach to carry into the Phase 2 plan:

1. **Runtime merge for opaque spreads.** When a spread operand is not a struct literal, lower the enclosing literal to a hoisted construction sequence: explicit fields → direct field assignments (unconditional), each opaque spread → a `merge<Struct>(acc, op)` call. **Generate `merge<Struct>` as a synthesized `ir.Func`** (IR-level, `Synthesized: true`) rather than hand-written text per backend — every backend's existing func codegen then emits it, and the existing `option<T>` → `*T`/`T?`/nullable null-handling renders the `if ov.f != null { base.f = ov.f }` body correctly. Primary "unset" test is `option<T> == null`; plain fields fall back to type-zero (documented limitation). **Open wiring question to resolve first:** confirm each backend (esp. JS/Kotlin) emits synthesized funcs registered on the package; the Explore notes Go uses `golang.HelpersNeeded`/`Emit`, JS/Kotlin emit ad-hoc — pick or add a uniform registration (`pkg`-level set of structs needing merge).
2. **Remove per-backend spread emitters** (`codegen/lang/golang/translate_ir.go` `/* ...x */`, Kotlin, JS) once no `ir.Spread` survives lowering — replace with an assert. Depends on (1).
3. **Native `...style` forwarding** for `gtk4.sngl`/`fyne.sngl`/`android.sngl`. **Open dependency:** raw native elements (`gtk4.GtkBox`, fyne/Compose widgets) must accept a `style` prop to type-check; resolve per platform (add a `style` param or per-toolkit application) — surface, don't hack.
4. **`Style` → `option<T>` migration** (related; makes runtime style merges exact). Out of scope for both plans unless prioritized; the Phase 1 wrapper path works regardless.
