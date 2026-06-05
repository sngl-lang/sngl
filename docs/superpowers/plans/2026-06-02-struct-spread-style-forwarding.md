# Struct Spread Lowering + Explicit `...style` Forwarding — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a caller's `style` reach the element a stdlib wrapper renders, the explicit way — platform `.sngl` bodies forward `...style`, and a capability-gated lowering pass flattens that spread at compile time.

**Architecture:** A new `flatten_struct_spread` lowering pass (gated by `Caps.NoStructSpread`) rewrites struct literals that contain `...literal` spreads into flat literals, with last-write-wins semantics. The html wrapper bodies in `html.sngl` forward `...style` onto their root element. This removes the temporary "magic" `forwardStyle` inliner hack. The color-struct CSS fix (already in the tree) is kept.

**Tech Stack:** Go; SNGL's `internal/lower` capability-driven IR passes; `internal/htmlutil` CSS builder; `codegen/platform/html` DOM tests (go-rod CDP).

**Structure.** Two phases, each independently shippable:

- **Phase 1 (Tasks 1–5):** capability gate, **compile-time (literal-operand)** flatten, html `...style` forwarding, remove the A stopgap, keep the color fix. Delivers the reported styling fix — the wrapper case is *always* a literal operand after inlining. Phase 1 leaves opaque (non-literal) spreads untouched in the IR (today's behavior; no regression).
- **Phase 2 (Tasks 6–12):** runtime `merge<Struct>` codegen for **opaque** spread operands (Go/JS/Kotlin, per-language idiom), removal of the per-backend spread emitters, and native (`gtk4`/`fyne`/`android`) `...style` forwarding.

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

| Component                   | Root style literal                                                                           |
|-----------------------------|----------------------------------------------------------------------------------------------|
| `sngl.vbox`                 | `style={...style, display="flex", flexDirection="column"}`                                   |
| `sngl.hbox`                 | `style={...style, display="flex", flexDirection="row"}`                                      |
| `sngl.stack`                | `style={...style, position="relative"}`                                                      |
| `sngl.spacer`               | `style={...style, flex=1}`                                                                   |
| `sngl.scroll`               | `style={...style, overflow="auto"}`                                                          |
| `sngl.text` (span)          | add `style={...style}`                                                                       |
| `sngl.button` (button)      | add `style={...style}`                                                                       |
| `sngl.image` (img)          | add `style={...style}`                                                                       |
| `sngl.input` (input)        | add `style={...style}`                                                                       |
| `sngl.checkbox` (label)     | `style={display="inline-flex", alignItems="center", gap=4, ...style}`                        |
| `sngl.radio` (fieldset)     | `style={display="flex", flexDirection=direction, gap=8, border="none", padding=0, ...style}` |
| `sngl.toggle` (label)       | `style={display="inline-flex", alignItems="center", gap=8, ...style}`                        |
| `sngl.select` (select)      | add `style={...style}`                                                                       |
| `sngl.textarea` (textarea)  | add `style={...style}`                                                                       |
| `sngl.progress` (progress)  | add `style={...style}`                                                                       |
| `sngl.spinner` (outer span) | `style={display="inline-flex", alignItems="center", gap=8, ...style}`                        |
| `sngl.badge` (span)         | `style={display="inline-block", padding="2px 8px", borderRadius=12, ...style}`               |
| `sngl.tabs` (outer div)     | `style={...style, display="flex", flexDirection="column"}`                                   |
| `sngl.link` (a)             | add `style={...style}`                                                                       |
| `sngl.divider` (hr)         | add `style={...style}`                                                                       |

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

---

# Phase 2: runtime merge for opaque spreads + native forwarding

**Goal:** handle struct spreads whose operand is *not* a literal (a runtime struct value), by lowering them to a chain of generated `__merge_<Struct>` calls, emitted per language in its native idiom; then remove the now-dead per-backend spread emitters; then forward `...style` from the native platform wrappers.

**Key approach (decided):**
- **Expression-only rewrite, no statement hoisting.** `{a=1, ...op, b=2}` lowers to `__merge_<S>(__merge_<S>(S{a:1}, op), S{b:2})`. This is correct for `option<T>` structs because an explicit literal field is `Some(x)` (non-null), so it survives the null-skip merge; consecutive explicit/literal-spread fields fold into one struct-literal operand.
- **Per-language text emitters**, not a generic IR func — Kotlin params are `val` (can't mutate like Go). Idioms: Go pointer-value copy + return; JS `{...base}` + assigns; Kotlin `.copy()`.
- **Stable name `__merge_<StructName>`** so the lowered call site matches every backend's emitted function.

**Primary risk:** struct-type identity must be recoverable at the flatten pass (in `lower`, after `optimize`). For opaque spreads the literal is not const-folded, so `StructLit.Type`/`Def` survive; the resolver also falls back to the opaque operand's type, and asserts if none is found. The opaque-`Cfg` fixture in Task 6 validates this end-to-end.

## Phase 2 File Structure

- **Modify** `ir/ir.go` — add `Package.MergeStructs []*StructDef`.
- **Modify** `internal/lower/flatten_struct_spread.go` — `flattenStructLit` opaque branch builds the merge chain + records structs; add `mergeChainFor`, `structDefOf`.
- **Create** `codegen/lang/golang/merge_emit.go` — `EmitMergeFuncs(structs) string`; called from each Go platform alongside `HelpersNeeded().Emit()`.
- **Create** `codegen/lang/javascript/merge_emit.go` — `EmitMergeFuncs(structs) string`; called from `html.go` `emitScript`.
- **Create** `codegen/lang/kotlin/merge_emit.go` — `EmitMergeFuncs(structs) string`; called from `android` assembly after data classes.
- **Modify** `codegen/lang/golang/translate_ir.go`, `codegen/lang/golang/ircontext.go`, `codegen/lang/kotlin/ircontext.go`, `codegen/lang/javascript/javascript.go` — remove the `f.Spread` arms, assert instead.
- **Modify** `codegen/platform/gtk4/gtk4.sngl`, `codegen/platform/fyne/fyne.sngl`, `codegen/platform/android/android.sngl` — forward `...style`.
- **Test:** `internal/lower/flatten_struct_spread_test.go`, `codegen/lang/*/merge_emit_test.go`, `testdata/struct_spread_runtime.sngl`.

---

## Task 6: Lower opaque spreads to a `__merge_<Struct>` call chain

**Files:**
- Modify: `ir/ir.go` (add `MergeStructs` to `Package`)
- Modify: `internal/lower/flatten_struct_spread.go`
- Test: `internal/lower/flatten_struct_spread_test.go`

- [ ] **Step 1: Add the package field**

In `ir/ir.go`, add to `type Package struct` (after `AsyncKickers`):

```go
	// MergeStructs lists struct types that need a generated __merge_<Struct>
	// runtime function, recorded by the flatten_struct_spread lowering pass
	// when it rewrites an opaque (non-literal) spread. Deduped by *StructDef.
	MergeStructs []*StructDef
```

- [ ] **Step 2: Write the failing test**

Append to `internal/lower/flatten_struct_spread_test.go`:

```go
func structType(name string) *ir.Type {
	sd := &ir.StructDef{Name: name, Fields: []ir.StructField{
		{Name: "a", Type: ir.TypInt}, {Name: "b", Type: ir.TypInt},
	}}
	return &ir.Type{Kind: ir.TypeStruct, Decl: sd}
}

func TestFlattenOpaqueSpreadBuildsMergeChain(t *testing.T) {
	st := structType("Cfg")
	pkg := &ir.Package{}
	// {a=1, ...op, b=2}  →  __merge_Cfg(__merge_Cfg(Cfg{a:1}, op), Cfg{b:2})
	in := &ir.StructLit{Type: st, Def: st.Decl.(*ir.StructDef), Fields: []ir.FieldInit{
		{Name: "a", Value: intLit("1")},
		{Spread: true, Value: &ir.Ident{Name: "op", Type: st}},
		{Name: "b", Value: intLit("2")},
	}}
	out := flattenStructLitCtx(in, pkg)
	call, ok := out.(*ir.Call)
	if !ok || call.Func == nil || call.Func.Name != "__merge_Cfg" {
		t.Fatalf("outer expr must be a __merge_Cfg call, got %T", out)
	}
	inner, ok := call.Args[0].Value.(*ir.Call)
	if !ok || inner.Func.Name != "__merge_Cfg" {
		t.Fatalf("first arg must be inner __merge_Cfg call, got %T", call.Args[0].Value)
	}
	if len(pkg.MergeStructs) != 1 || pkg.MergeStructs[0].Name != "Cfg" {
		t.Fatalf("pkg.MergeStructs must record Cfg once, got %+v", pkg.MergeStructs)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/lower/ -run TestFlattenOpaqueSpread -v`
Expected: FAIL — `undefined: flattenStructLitCtx`.

- [ ] **Step 4: Thread the package through the pass and implement the merge chain**

In `internal/lower/flatten_struct_spread.go`, change `lowerFlattenStructSpread` to capture `pkg` in the closures, and route `flattenStructLit` through a package-aware variant. Replace the `expr`/`stmts` wiring with:

```go
func lowerFlattenStructSpread(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	rewrite := func(e ir.Expr) ir.Expr { return flattenSpreadExprCtx(e, pkg) }
	walkPackage(pkg, walkFuncs{
		expr:  rewrite,
		stmts: func(stmts []ir.Stmt) []ir.Stmt { return flattenSpreadStmts(stmts, rewrite) },
	})
	return nil
}
```

Rename `flattenSpreadExpr` to `flattenSpreadExprCtx(e ir.Expr, pkg *ir.Package) ir.Expr`, recurse with `pkg` threaded, and in its `*ir.StructLit` arm call `flattenStructLitCtx(x, pkg)`. (All recursive `flattenSpreadExpr(...)` calls become `flattenSpreadExprCtx(..., pkg)`; the `Lambda`/`Closure` arms pass `pkg` to `flattenSpreadStmts` via the `rewrite` closure — capture a local `rewrite := func(e ir.Expr) ir.Expr { return flattenSpreadExprCtx(e, pkg) }` at the top of the function and use it for the nested `.Block` walks.)

Replace `flattenStructLit` with `flattenStructLitCtx`:

```go
// flattenStructLitCtx collapses a spread-bearing struct literal. When every
// spread operand is a struct literal it folds to a flat literal (Phase 1).
// When any operand is opaque it builds a left-folded __merge_<Struct> call
// chain (Phase 2) and records the struct on pkg.MergeStructs. Returns sl
// unchanged when it has no spreads.
func flattenStructLitCtx(sl *ir.StructLit, pkg *ir.Package) ir.Expr {
	hasSpread, allLiteral := false, true
	for _, f := range sl.Fields {
		if f.Spread {
			hasSpread = true
			if _, ok := f.Value.(*ir.StructLit); !ok {
				allLiteral = false
			}
		}
	}
	if !hasSpread {
		return sl
	}
	if allLiteral {
		return flattenLiteralSpread(sl) // the Phase 1 splice (renamed body of old flattenStructLit)
	}
	return buildMergeChain(sl, pkg)
}

// buildMergeChain lowers a literal with >=1 opaque spread into nested
// __merge_<Struct> calls. Explicit fields and literal spreads accumulate into
// a struct-literal operand; each opaque spread flushes that operand and merges
// the runtime value. Left-folded → source order preserved, last wins.
func buildMergeChain(sl *ir.StructLit, pkg *ir.Package) ir.Expr {
	sd := structDefOf(sl)
	if sd == nil {
		panic("flatten_struct_spread: cannot resolve struct type for spread merge")
	}
	recordMergeStruct(pkg, sd)
	fn := &ir.Func{Name: "__merge_" + sd.Name, Synthesized: true, Return: sl.Type,
		Params: []*ir.Param{{Name: "base", Type: sl.Type}, {Name: "ov", Type: sl.Type}}}
	mkLit := func(fields []ir.FieldInit) *ir.StructLit {
		cp := append([]ir.FieldInit(nil), fields...)
		return &ir.StructLit{AST: sl.AST, Type: sl.Type, Def: sl.Def, Fields: cp}
	}
	merge := func(a, b ir.Expr) ir.Expr {
		return &ir.Call{Type: sl.Type, Func: fn, Args: []ir.CallArg{{Value: a}, {Value: b}}}
	}
	var acc ir.Expr
	var pending []ir.FieldInit
	flush := func() {
		if len(pending) == 0 {
			return
		}
		if acc == nil {
			acc = mkLit(pending)
		} else {
			acc = merge(acc, mkLit(pending))
		}
		pending = nil
	}
	for _, f := range sl.Fields {
		if f.Spread {
			if inner, ok := f.Value.(*ir.StructLit); ok {
				pending = append(pending, inner.Fields...)
				continue
			}
			flush()
			if acc == nil {
				acc = mkLit(nil)
			}
			acc = merge(acc, f.Value)
			continue
		}
		pending = append(pending, f)
	}
	flush()
	return acc
}

// structDefOf resolves the *StructDef for a struct literal, falling back to the
// type of its first opaque spread operand (the checker guarantees same type).
func structDefOf(sl *ir.StructLit) *ir.StructDef {
	if sl.Def != nil {
		return sl.Def
	}
	if sl.Type != nil {
		if sd, ok := sl.Type.Decl.(*ir.StructDef); ok {
			return sd
		}
	}
	for _, f := range sl.Fields {
		if f.Spread && f.Value != nil {
			if t := exprStructType(f.Value); t != nil {
				if sd, ok := t.Decl.(*ir.StructDef); ok {
					return sd
				}
			}
		}
	}
	return nil
}

// exprStructType returns the static *Type of an expr when it is a struct type.
func exprStructType(e ir.Expr) *ir.Type {
	switch x := e.(type) {
	case *ir.Ident:
		return x.Type
	case *ir.Select:
		return x.Type
	case *ir.Call:
		return x.Type
	case *ir.Index:
		return x.Type
	}
	return nil
}

func recordMergeStruct(pkg *ir.Package, sd *ir.StructDef) {
	for _, e := range pkg.MergeStructs {
		if e == sd {
			return
		}
	}
	pkg.MergeStructs = append(pkg.MergeStructs, sd)
}
```

Rename the body of the old `flattenStructLit` (the splice loop from Task 2 Step 3) to `flattenLiteralSpread(sl *ir.StructLit) *ir.StructLit` and keep it as-is.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/lower/ -run 'TestFlatten' -v`
Expected: PASS (Phase 1 literal tests + the new merge-chain test).

- [ ] **Step 6: Commit**

```bash
git add ir/ir.go internal/lower/flatten_struct_spread.go internal/lower/flatten_struct_spread_test.go
git commit -m "feat(lower): opaque struct spreads → __merge_<Struct> call chain"
```

---

## Task 7: Go `__merge_<Struct>` emitter

**Files:**
- Create: `codegen/lang/golang/merge_emit.go`
- Modify: each Go platform's emit assembly to append `golang.EmitMergeFuncs(pkg.MergeStructs)` next to `HelpersNeeded(pkg).Emit()` — `codegen/platform/fyne/compiler_ir.go` (near line 465), `codegen/platform/gtk4/*` and `codegen/platform/bubbletea/*` equivalents, and the Go-lang html route mode.
- Test: `codegen/lang/golang/merge_emit_test.go`

- [ ] **Step 1: Write the failing test**

Create `codegen/lang/golang/merge_emit_test.go`:

```go
package golang

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestEmitMergeFuncsGo(t *testing.T) {
	sd := &ir.StructDef{Name: "Style", Fields: []ir.StructField{
		{Name: "gap", Type: ir.NewOption(ir.TypInt)},
		{Name: "color", Type: ir.NewOption(ir.TypString)},
		{Name: "flex", Type: ir.TypFloat}, // plain field → type-zero test
	}}
	out := EmitMergeFuncs([]*ir.StructDef{sd})
	for _, want := range []string{
		"func __merge_Style(base, ov Style) Style {",
		"if ov.Gap != nil { base.Gap = ov.Gap }",
		"if ov.Color != nil { base.Color = ov.Color }",
		"if ov.Flex != 0 { base.Flex = ov.Flex }",
		"return base",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}
```

(Confirm the option constructor name: `ir.NewOption` per `ir/types.go:95`. If it differs, use the actual constructor.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./codegen/lang/golang/ -run TestEmitMergeFuncsGo -v`
Expected: FAIL — `undefined: EmitMergeFuncs`.

- [ ] **Step 3: Implement the emitter**

Create `codegen/lang/golang/merge_emit.go`:

```go
package golang

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// EmitMergeFuncs returns Go source for one __merge_<Struct> helper per struct
// in structs. Each copies the (value-semantics) base, overwrites a field only
// when the override's value is "present" — non-nil for option<T> (*T), or
// non-zero for plain fields — and returns the merged copy.
func EmitMergeFuncs(structs []*ir.StructDef) string {
	var b strings.Builder
	for _, sd := range structs {
		name := ExportName(sd.Name)
		fmt.Fprintf(&b, "func __merge_%s(base, ov %s) %s {\n", name, name, name)
		for _, f := range sd.Fields {
			fn := ExportName(f.Name)
			fmt.Fprintf(&b, "\tif ov.%s != %s { base.%s = ov.%s }\n", fn, goZeroComparand(f.Type), fn, fn)
		}
		b.WriteString("\treturn base\n}\n\n")
	}
	return b.String()
}

// goZeroComparand returns the Go expression a field is compared against to
// decide "unset". Option/pointer/reference types compare to nil; scalars to
// their zero literal.
func goZeroComparand(t *ir.Type) string {
	if t == nil {
		return "nil"
	}
	switch t.Kind {
	case ir.TypeOption, ir.TypeList, ir.TypeMap, ir.TypeRef, ir.TypeFunc:
		return "nil"
	case ir.TypeString, ir.TypeURL, ir.TypeEmail, ir.TypeUUID, ir.TypeRegex,
		ir.TypeBase64, ir.TypeIPV4, ir.TypeIPV6, ir.TypeHostname, ir.TypeDecimal, ir.TypeColor:
		return `""`
	case ir.TypeBool:
		return "false"
	case ir.TypeFloat:
		return "0"
	default:
		return "0"
	}
}
```

> Note: `goZeroComparand` returns `""` for `TypeColor` to match `IRTypeToGo`/`ZeroExpr` (color renders as a string literal at the Go layer today). If a struct has a non-comparable plain field (e.g. a nested struct that isn't `!=`-comparable), the generated `!=` won't compile — Phase 2 targets `option<T>` structs (`Style`) where every field is `*T`, so this does not arise; if it ever does, the compile error names the field (acceptable loud failure per the spec).

- [ ] **Step 4: Run the emitter test**

Run: `go test ./codegen/lang/golang/ -run TestEmitMergeFuncsGo -v`
Expected: PASS.

- [ ] **Step 5: Wire emission into Go platforms**

In `codegen/platform/fyne/compiler_ir.go` near line 465 where `td.LangHelpers = helpers.Emit()` is set, change to:

```go
td.LangHelpers = helpers.Emit() + golang.EmitMergeFuncs(ctx.Pkg.MergeStructs)
```

Find the equivalent `HelpersNeeded(...).Emit()` / `LangHelpers` assignment in the bubbletea and gtk4 generators and the Go html route mode, and append `+ golang.EmitMergeFuncs(<pkg>.MergeStructs)` the same way. (Search: `grep -rn "HelpersNeeded\|LangHelpers" codegen/platform`.)

- [ ] **Step 6: Build**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add codegen/lang/golang/merge_emit.go codegen/lang/golang/merge_emit_test.go codegen/platform/fyne/compiler_ir.go codegen/platform/bubbletea/*.go codegen/platform/gtk4/*.go
git commit -m "feat(golang): emit __merge_<Struct> helpers; wire into Go platforms"
```

---

## Task 8: JS `__merge_<Struct>` emitter (html)

**Files:**
- Create: `codegen/lang/javascript/merge_emit.go`
- Modify: `codegen/platform/html/html.go` (`emitScript`, near the `emitJSFunc` loop ~line 1918)
- Test: `codegen/lang/javascript/merge_emit_test.go`

- [ ] **Step 1: Write the failing test**

Create `codegen/lang/javascript/merge_emit_test.go`:

```go
package javascript

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestEmitMergeFuncsJS(t *testing.T) {
	sd := &ir.StructDef{Name: "Style", Fields: []ir.StructField{
		{Name: "gap", Type: ir.NewOption(ir.TypInt)},
		{Name: "color", Type: ir.NewOption(ir.TypString)},
	}}
	out := EmitMergeFuncs([]*ir.StructDef{sd})
	for _, want := range []string{
		"function __merge_Style(base, ov) {",
		"const r = { ...base };",
		"if (ov.gap != null) r.gap = ov.gap;",
		"if (ov.color != null) r.color = ov.color;",
		"return r;",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./codegen/lang/javascript/ -run TestEmitMergeFuncsJS -v`
Expected: FAIL — `undefined: EmitMergeFuncs`.

- [ ] **Step 3: Implement the emitter**

Create `codegen/lang/javascript/merge_emit.go`:

```go
package javascript

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// EmitMergeFuncs returns JS source for one __merge_<Struct> helper per struct.
// JS objects carry only their set fields; "present" is `!= null` (covers null
// and undefined). Plain (non-option) fields use the same null test — a JS
// object simply omits unset fields, so null/undefined is the right sentinel.
func EmitMergeFuncs(structs []*ir.StructDef) string {
	var b strings.Builder
	for _, sd := range structs {
		fmt.Fprintf(&b, "function __merge_%s(base, ov) {\n", sd.Name)
		b.WriteString("  const r = { ...base };\n")
		for _, f := range sd.Fields {
			fmt.Fprintf(&b, "  if (ov.%s != null) r.%s = ov.%s;\n", f.Name, f.Name, f.Name)
		}
		b.WriteString("  return r;\n}\n\n")
	}
	return b.String()
}
```

(JS uses raw field names and the JS struct name unchanged — matches `javascript/ircontext.go` struct-literal emission and the `__merge_<sd.Name>` call rendered by the JS call emitter.)

- [ ] **Step 4: Run the emitter test**

Run: `go test ./codegen/lang/javascript/ -run TestEmitMergeFuncsJS -v`
Expected: PASS.

- [ ] **Step 5: Wire emission into `emitScript`**

In `codegen/platform/html/html.go`, in `emitScript`, immediately before the `for _, fn := range ...` loop that calls `g.emitJSFunc` (~line 1918), emit the merge helpers once:

```go
if mf := javascript.EmitMergeFuncs(g.pkg.MergeStructs); mf != "" {
	b.WriteString(mf)
}
```

Confirm `javascript` is imported in `html.go` (it is — the html platform delegates JS via the javascript translator); if not, add the import.

- [ ] **Step 6: Build**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add codegen/lang/javascript/merge_emit.go codegen/lang/javascript/merge_emit_test.go codegen/platform/html/html.go
git commit -m "feat(javascript): emit __merge_<Struct> helpers; wire into html emitScript"
```

---

## Task 9: Kotlin `__merge_<Struct>` emitter (android)

**Files:**
- Create: `codegen/lang/kotlin/merge_emit.go`
- Modify: `codegen/platform/android/compiler_ir.go` (after the `data class` emission loop, ~line 271–294)
- Test: `codegen/lang/kotlin/merge_emit_test.go`

- [ ] **Step 1: Write the failing test**

Create `codegen/lang/kotlin/merge_emit_test.go`:

```go
package kotlin

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestEmitMergeFuncsKotlin(t *testing.T) {
	sd := &ir.StructDef{Name: "Style", Fields: []ir.StructField{
		{Name: "gap", Type: ir.NewOption(ir.TypInt)},
		{Name: "color", Type: ir.NewOption(ir.TypString)},
	}}
	out := EmitMergeFuncs([]*ir.StructDef{sd})
	for _, want := range []string{
		"fun __merge_Style(base: Style, ov: Style): Style =",
		"base.copy(",
		"gap = ov.gap ?: base.gap,",
		"color = ov.color ?: base.color,",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./codegen/lang/kotlin/ -run TestEmitMergeFuncsKotlin -v`
Expected: FAIL — `undefined: EmitMergeFuncs`.

- [ ] **Step 3: Implement the emitter**

Create `codegen/lang/kotlin/merge_emit.go`:

```go
package kotlin

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// EmitMergeFuncs returns Kotlin source for one __merge_<Struct> helper per
// struct, using data-class .copy(). For option (T?) fields the elvis operator
// `ov.f ?: base.f` is exactly null-skip. Plain fields fall back to an
// if-expression against the Kotlin zero value.
func EmitMergeFuncs(structs []*ir.StructDef) string {
	var b strings.Builder
	for _, sd := range structs {
		name := exportName(sd.Name)
		fmt.Fprintf(&b, "fun __merge_%s(base: %s, ov: %s): %s =\n", name, name, name, name)
		b.WriteString("    base.copy(\n")
		for _, f := range sd.Fields {
			if f.Type != nil && f.Type.Kind == ir.TypeOption {
				fmt.Fprintf(&b, "        %s = ov.%s ?: base.%s,\n", f.Name, f.Name, f.Name)
			} else {
				fmt.Fprintf(&b, "        %s = if (ov.%s != %s) ov.%s else base.%s,\n",
					f.Name, f.Name, kotlinZeroComparand(f.Type), f.Name, f.Name)
			}
		}
		b.WriteString("    )\n\n")
	}
	return b.String()
}

func kotlinZeroComparand(t *ir.Type) string {
	if t == nil {
		return "null"
	}
	switch t.Kind {
	case ir.TypeString, ir.TypeURL, ir.TypeEmail, ir.TypeUUID, ir.TypeRegex,
		ir.TypeBase64, ir.TypeIPV4, ir.TypeIPV6, ir.TypeHostname, ir.TypeDecimal, ir.TypeColor:
		return `""`
	case ir.TypeBool:
		return "false"
	case ir.TypeFloat:
		return "0.0"
	default:
		return "0"
	}
}
```

(Trailing comma in `.copy(...)` is valid Kotlin. `exportName` is the Kotlin struct-name mapper at `codegen/lang/kotlin/kotlin.go:202`; field names are raw, matching the data-class properties and struct-literal constructor emission in `kotlin/ircontext.go`.)

- [ ] **Step 4: Run the emitter test**

Run: `go test ./codegen/lang/kotlin/ -run TestEmitMergeFuncsKotlin -v`
Expected: PASS.

- [ ] **Step 5: Wire emission into android assembly**

In `codegen/platform/android/compiler_ir.go`, immediately after the `for _, sd := range info.Structs { ... }` data-class loop (the block ending ~line 294, after the `ErrorEvent` data class), append:

```go
if mf := kotlin.EmitMergeFuncs(info.Structs); mf != "" {
	b.WriteString(mf)
}
```

Use the package's merge set rather than all structs if available here — if `info.Structs` is `CommonAnalysis.Structs` (all structs) and the package merge set is reachable as `req.Pkg.MergeStructs`, prefer `kotlin.EmitMergeFuncs(req.Pkg.MergeStructs)`. (Emitting a merge fn for an unused struct is harmless dead code, but the merge set is precise.) Confirm `kotlin` is imported in this file.

- [ ] **Step 6: Build**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add codegen/lang/kotlin/merge_emit.go codegen/lang/kotlin/merge_emit_test.go codegen/platform/android/compiler_ir.go
git commit -m "feat(kotlin): emit __merge_<Struct> helpers; wire into android assembly"
```

---

## Task 10: End-to-end runtime-merge fixture + remove the dead spread emitters

**Files:**
- Create: `testdata/struct_spread_runtime.sngl`
- Modify: `codegen/lang/golang/translate_ir.go` (and `ircontext.go` if it has a spread arm), `codegen/lang/kotlin/ircontext.go`, `codegen/lang/javascript/javascript.go`
- Test: existing suites

- [ ] **Step 1: Write the runtime-merge fixture**

Create `testdata/struct_spread_runtime.sngl`:

```
struct Cfg { a int = 0  b int = 0 }

component main {
    var base Cfg = {a = 1}
    var merged Cfg = {...base, b = 2}
    text(value=string(merged.a) + "/" + string(merged.b))
}
```

- [ ] **Step 2: Verify it compiles to Go via a Go platform and calls the merge fn**

Run:

```bash
go run ./cmd/sngl generate --lang go --platform bubbletea testdata/struct_spread_runtime.sngl -o /tmp/rtmerge
grep -rn "__merge_Cfg" /tmp/rtmerge/
```

Expected: the generated Go contains both a `func __merge_Cfg(base, ov Cfg) Cfg {` definition and a `__merge_Cfg(` call site. (`b` is a plain int field, so `base`/`b=2` merge via the `!= 0` test — `a=1` from `base`, `b=2` explicit. With the plain-int limitation, `a=1` survives because `base.a` is non-zero.)

- [ ] **Step 3: Run test to verify the Go spread comment is gone**

Run: `grep -rn '/\* \.\.\.' /tmp/rtmerge/ || echo "no dropped-spread comments"`
Expected: `no dropped-spread comments`.

- [ ] **Step 4: Remove the dead spread arms**

Now that the flatten pass converts every struct-literal spread to either a flat literal or a `__merge_` call, no `FieldInit{Spread:true}` reaches struct-literal emission. Replace each backend's spread arm with a panic:

- `codegen/lang/golang/translate_ir.go` — in the `*ir.StructLit` case, replace:

  ```go
  if f.Spread {
      parts = append(parts, "/* ..."+translateIRExpr(f.Value, scope)+" */")
  } else {
  ```

  with:

  ```go
  if f.Spread {
  	panic("golang: struct spread must be lowered by flatten_struct_spread")
  }
  parts = append(parts, ExportName(f.Name)+": "+translateIRExpr(f.Value, scope))
  ```

  (drop the now-redundant `else`). Apply the same removal to any sibling spread arm found by `grep -n "f.Spread" codegen/lang/golang/*.go` (e.g. `ircontext.go:183`).
- `codegen/lang/kotlin/ircontext.go:92` — replace the `if f.Spread { ... }` branch with `if f.Spread { panic("kotlin: struct spread must be lowered") }`.
- `codegen/lang/javascript/javascript.go:71` — replace the `if f.Spread { ... }` branch with `if f.Spread { panic("javascript: struct spread must be lowered") }`.

- [ ] **Step 5: Build + run the testdata + lang suites**

Run: `go build ./... && go test ./codegen/... ./internal/... 2>&1 | grep -iv '^ok\|no test files' | tail -30`
Expected: no failures (no panics — every spread is lowered before codegen).

- [ ] **Step 6: Commit**

```bash
git add testdata/struct_spread_runtime.sngl codegen/lang/golang/*.go codegen/lang/kotlin/ircontext.go codegen/lang/javascript/javascript.go
git commit -m "feat(codegen): drop per-backend struct-spread emitters; assert spreads are lowered"
```

---

## Task 11: Native `...style` forwarding (gtk4 / fyne / android)

**Files:**
- Modify: `codegen/platform/gtk4/gtk4.sngl`, `codegen/platform/fyne/fyne.sngl`, `codegen/platform/android/android.sngl`
- Test: per-platform build/snapshot suites

> **Open dependency (resolve first):** raw native elements referenced in these bodies (`gtk4.GtkBox`, fyne widgets, Compose `Column`/`Row`) must accept a `style` prop, or the checker errors `unknown prop "style"`. Step 1 probes this; Step 2 resolves it before forwarding.

- [ ] **Step 1: Probe whether native roots accept `style`**

For one wrapper per platform, temporarily add `style={...style}` to the root and build:

```bash
# gtk4
go run ./cmd/sngl generate --lang go --platform gtk4 testdata/component_simple.sngl -o /tmp/gtk4probe 2>&1 | head
# fyne
go run ./cmd/sngl generate --lang go --platform fyne testdata/component_simple.sngl -o /tmp/fyneprobe 2>&1 | head
# android
go run ./cmd/sngl generate --lang kotlin --platform android testdata/component_simple.sngl -o /tmp/androidprobe 2>&1 | head
```

Record which platforms error with `unknown prop "style"`.

- [ ] **Step 2: For platforms that reject `style`, add a `style Style` param to the native element**

For each native element decl that lacks `style` (in the platform's `.sngl` element declarations or its Go-side element registration), add a `style Style` param so the body can forward it. (The element's codegen may ignore the value — that is acceptable: forwarding must type-check; rendering coverage is per-toolkit and out of scope here.) If an element's codegen *rejects unknown props structurally* (not just via the checker), surface that as a finding and stop — do not hack around it.

- [ ] **Step 3: Forward `...style` on each native wrapper root**

Apply the same precedence rule as html (locked structural after `...style`; overridable defaults before). For the core layout wrappers:

- `gtk4.sngl` `sngl.vbox`: `gtk4.GtkBox(orientation="vertical", spacing=6, style={...style})` (gtk4 ignores unmapped style keys today).
- `gtk4.sngl` `sngl.hbox`: `gtk4.GtkBox(orientation="horizontal", spacing=6, style={...style})`.
- `fyne.sngl` `sngl.vbox` / `sngl.hbox`: add `style={...style}` to the root container.
- `android.sngl` `sngl.vbox` (`Column`) / `sngl.hbox` (`Row`): add `style={...style}` to the root composable.

Apply to the remaining wrappers in each native `.sngl` that declare a `style Style` param, mirroring the html table from Task 4.

- [ ] **Step 4: Build each native platform**

Run:

```bash
go run ./cmd/sngl generate --lang go --platform gtk4 testdata/component_simple.sngl -o /tmp/gtk4 2>&1 | head
go run ./cmd/sngl generate --lang go --platform fyne testdata/component_simple.sngl -o /tmp/fyne 2>&1 | head
go run ./cmd/sngl generate --lang kotlin --platform android testdata/component_simple.sngl -o /tmp/android 2>&1 | head
```

Expected: no `unknown prop` errors; each generates.

- [ ] **Step 5: Run native platform suites**

Run: `go test ./codegen/platform/gtk4/... ./codegen/platform/fyne/... ./codegen/platform/android/... 2>&1 | tail -20`
Expected: no new failures (snapshot tests may need regolden if style now affects native output — only regold if the change is the expected style addition).

- [ ] **Step 6: Commit**

```bash
git add codegen/platform/gtk4/gtk4.sngl codegen/platform/fyne/fyne.sngl codegen/platform/android/android.sngl
git commit -m "feat(native): gtk4/fyne/android wrappers forward ...style on root"
```

---

## Task 12: Full Phase 2 verification

**Files:** none (verification only)

- [ ] **Step 1: Build everything**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 2: Full suite, failures only**

Run: `go test ./... 2>&1 | grep -iv '^ok\|no test files' | tail -40`
Expected: only the pre-existing `TestDocSNGLFormat` (tour.md drift) fails; nothing else.

- [ ] **Step 3: Confirm no struct spread survives to any backend**

Run: `grep -rn '/\* \.\.\.' codegen/ ; echo "---"; go test ./codegen/... 2>&1 | grep -i 'panic' || echo "no spread panics"`
Expected: no dropped-spread comments; no spread-lowering panics.

- [ ] **Step 4: Commit any residual**

```bash
git add -A && git status
git commit -m "test: phase 2 verification residual" || echo "nothing to commit"
```

---

## Phase 2 self-review (author checklist — completed)

- **Spec coverage:** runtime merge for opaque spreads (Task 6) ✓; `merge<Struct>` codegen Go/JS/Kotlin per-idiom (Tasks 7–9) ✓; `option<T>==null` primary test + plain-field fallback (emitter tests) ✓; remove per-backend spread emitters (Task 10) ✓; native forwarding + `style`-prop dependency (Task 11) ✓.
- **Placeholder scan:** emitter code, lowering code, and tests are complete; the one genuinely-investigative step (Task 11 Step 1–2, native `style`-prop acceptance) is structured as probe-then-resolve with a stop-and-surface rule, not a hand-wave.
- **Type consistency:** `__merge_<Struct>` name is identical across the lowering call (`"__merge_" + sd.Name`) and all three emitters; `EmitMergeFuncs`/`goZeroComparand`/`kotlinZeroComparand`/`structDefOf`/`buildMergeChain`/`flattenStructLitCtx`/`flattenLiteralSpread`/`Package.MergeStructs` used consistently.
- **Known limitation carried from spec:** plain (non-`option`) fields use a type-zero "present" test, so a runtime spread can't set a plain field to its zero value; `option<T>` (and `Style` once migrated) has no such limit. `Style` → `option<T>` migration remains related future work, not required for these tasks.
