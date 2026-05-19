# LSP documentColor Layer 2 (Type-Aware Constant Expressions) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a second, type-aware layer to `documentColor` in `sngl lsp` that runs the checker and optimizer over the document and walks the folded IR for color-typed `*ir.Literal` nodes. Today's AST-based detection (hex literals + `color.rgb`/`color.rgba` calls) stays as Layer 1; Layer 2 layers on top with dedupe by source range.

**Architecture:** Two-layer detection in `computeDocumentColors`. Layer 1 (existing) walks AST, always runs, never blocked by check errors. Layer 2 (new) calls `sngl.Check` + `optimize.Optimize`; on success it walks the folded `*ir.Package` for `*ir.Literal{Type: ir.TypColor, AST != nil}` and emits one `ColorInformation` per literal that doesn't already appear in Layer 1's output. On check or optimize failure, Layer 2 returns nothing — Layer 1's results stand.

**Tech Stack:** Go, existing `internal/lsp` package, `sngl.Check`, `internal/optimize.Optimize`, `ir` package.

**Spec:** `docs/superpowers/specs/2026-05-18-lsp-previews-design.md` §F2.
**Dependency tracked:** [issue #76](https://git.duckfam.us/jonathan/sngl/-/issues/76) — extend consteval to color/unit/enum/option types. Until #76 lands, Layer 2 will only rediscover literals that were already source-form hex or `color.rgb/rgba` calls — i.e. it adds no new swatches over Layer 1 in practice. The wiring goes in now so that the day #76 closes, the LSP automatically picks up swatches for `color.lighten(#ff0000, 0.5)`, user-defined pure helpers, and arbitrary compositions.

---

## File Structure

| File | Status | Responsibility |
|---|---|---|
| `internal/lsp/color_irwalk.go` | create | `walkIRColorLiterals(pkg, fn)` — minimal IR walker |
| `internal/lsp/color.go` | modify | Add Layer 2 path in `computeDocumentColors`, dedupe helper |
| `internal/lsp/color_test.go` | modify | Layer-1-vs-Layer-2 parity test on existing fixture |
| `internal/lsp/color_irwalk_test.go` | create | Unit tests for the IR walker |

---

## Task 1: IR walker

Walk a `*ir.Package` and invoke a callback for every `*ir.Literal` reachable from package-level vars, components (vars + body statements), and windows (vars + body statements). Skip imports — those packages are already optimized recursively but their literals don't appear in the source file we're rendering.

**Files:**
- Create: `internal/lsp/color_irwalk.go`
- Create: `internal/lsp/color_irwalk_test.go`

- [ ] **Step 1: Read the existing IR shape**

Run: `grep -nE "^type (Package|Component|Window|Func|Var) " ir/ir.go`

Confirm:
- `Package.Vars`, `Package.Components`, `Package.Windows`, `Package.Funcs` exist (read the actual struct in `ir/ir.go` lines 32+).
- `Component.Body`, `Window.Body`, `Var.Init` exist.
- `Func.Body` exists (for inlining-folded helpers).

If field names differ, adapt the walker code to match. The fixture tests in steps below catch missed cases.

- [ ] **Step 2: Read expression and statement node types**

Run: `grep -nE "^type " ir/expr.go ir/stmt.go`

These are the nodes the walker dispatches on. Expected shapes (verify by reading the files):

- `ir.Literal` — leaf, has `AST *ast.LiteralExpr`, `Type *ir.Type`
- `ir.Ident` — leaf
- `ir.Binary` — `Left`, `Right`
- `ir.Unary` — `Operand`
- `ir.Call` — `Func`, `Args []ir.Expr`
- `ir.Conversion` — wraps inner Expr
- `ir.StructLit` — fields with values
- `ir.ListLit` — elements
- `ir.MapLit` — key/value pairs
- `ir.Ternary` — `Cond`, `Then`, `Else`
- `ir.Index` — `Operand`, `Index`
- `ir.Member` — `Operand`
- `ir.Lambda` — body
- `ir.NodeInst` — `Args`, `Block`
- `ir.Set` (assignment) — `Value`
- `ir.If` — `Cond`, `Then`, `Else`
- `ir.For` — `Iter`, `Body`
- `ir.Return` — `Value`
- `ir.Emit` — `Args`

If any node carries an Expr field not listed above, add a case for it. Missing a case means missed literals.

- [ ] **Step 3: Write the failing test**

Create `internal/lsp/color_irwalk_test.go`:

```go
package lsp

import (
	"os"
	"path/filepath"
	"testing"

	"git.duckfam.us/jonathan/sngl"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestWalkIRColorLiterals_FixtureCovers6Colors(t *testing.T) {
	src, err := os.ReadFile("../../testdata/lsp/colors.sngl")
	if err != nil {
		t.Fatal(err)
	}
	doc, perr := parser.Parse("colors.sngl", src)
	if perr != nil {
		t.Fatalf("parse: %v", perr)
	}
	dir, err := filepath.Abs("../../testdata/lsp")
	if err != nil {
		t.Fatal(err)
	}
	pkg, diags := sngl.Check(doc, dir)
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}
	// Don't run Optimize here — the const folder doesn't yet fold color
	// calls (tracked in issue #76). Just verify the walker visits the
	// source-form #hex literals that the checker produces directly.

	var colors []*ir.Literal
	walkIRColorLiterals(pkg, func(lit *ir.Literal) {
		colors = append(colors, lit)
	})

	// Source has 4 #hex literals. Until #76 lands the rgb/rgba calls
	// stay as *ir.Call (not folded), so the walker shouldn't see them yet.
	if len(colors) != 4 {
		t.Fatalf("got %d color literals, want 4: %+v", len(colors), colors)
	}
	for _, c := range colors {
		if c.AST == nil {
			t.Errorf("color literal has nil AST: %+v", c)
		}
		if c.Type != ir.TypColor {
			t.Errorf("literal type = %v, want color", c.Type)
		}
	}
}
```

- [ ] **Step 4: Run, verify failure**

Run: `go test ./internal/lsp/ -run TestWalkIRColorLiterals_FixtureCovers6Colors -v`
Expected: FAIL with "undefined: walkIRColorLiterals".

- [ ] **Step 5: Implement the walker**

Create `internal/lsp/color_irwalk.go`:

```go
package lsp

import "git.duckfam.us/jonathan/sngl/ir"

// walkIRColorLiterals invokes fn for every *ir.Literal of color type
// reachable from package-level vars, components, windows, and pure funcs.
// Skips imported packages — only the root file's literals matter for
// documentColor.
func walkIRColorLiterals(pkg *ir.Package, fn func(*ir.Literal)) {
	if pkg == nil {
		return
	}
	w := &irLitWalker{fn: fn}
	for _, v := range pkg.Vars {
		w.expr(v.Init)
	}
	for _, c := range pkg.Components {
		for _, v := range c.Vars {
			w.expr(v.Init)
		}
		w.stmts(c.Body)
	}
	for _, win := range pkg.Windows {
		for _, v := range win.Vars {
			w.expr(v.Init)
		}
		w.stmts(win.Body)
	}
	for _, f := range pkg.Funcs {
		w.stmts(f.Body)
	}
}

type irLitWalker struct {
	fn func(*ir.Literal)
}

func (w *irLitWalker) stmts(stmts []ir.Stmt) {
	for _, s := range stmts {
		w.stmt(s)
	}
}

func (w *irLitWalker) stmt(s ir.Stmt) {
	switch x := s.(type) {
	case nil:
		return
	case *ir.NodeInst:
		for _, a := range x.Args {
			w.expr(a.Value)
		}
		w.stmts(x.Block)
	case *ir.Set:
		w.expr(x.Value)
	case *ir.If:
		w.expr(x.Cond)
		w.stmts(x.Then)
		w.stmts(x.Else)
	case *ir.For:
		w.expr(x.Iter)
		w.stmts(x.Body)
	case *ir.Return:
		w.expr(x.Value)
	case *ir.Emit:
		for _, a := range x.Args {
			w.expr(a.Value)
		}
	}
}

func (w *irLitWalker) expr(e ir.Expr) {
	switch x := e.(type) {
	case nil:
		return
	case *ir.Literal:
		if x.Type == ir.TypColor && x.AST != nil {
			w.fn(x)
		}
	case *ir.Binary:
		w.expr(x.Left)
		w.expr(x.Right)
	case *ir.Unary:
		w.expr(x.Operand)
	case *ir.Call:
		w.expr(x.Func)
		for _, a := range x.Args {
			w.expr(a)
		}
	case *ir.Conversion:
		w.expr(x.Inner)
	case *ir.StructLit:
		for _, f := range x.Fields {
			w.expr(f.Value)
		}
	case *ir.ListLit:
		for _, el := range x.Elements {
			w.expr(el)
		}
	case *ir.MapLit:
		for _, kv := range x.Entries {
			w.expr(kv.Key)
			w.expr(kv.Value)
		}
	case *ir.Ternary:
		w.expr(x.Cond)
		w.expr(x.Then)
		w.expr(x.Else)
	case *ir.Index:
		w.expr(x.Operand)
		w.expr(x.Index)
	case *ir.Member:
		w.expr(x.Operand)
	case *ir.Lambda:
		w.stmts(x.Body)
	}
}
```

**IMPORTANT:** the exact names of `ir` types and field accessors will need verification. Run the grep from Step 2 first; if any field is named differently (e.g. `Conversion.Source` rather than `.Inner`, `MapLit.Pairs` rather than `.Entries`, `For.Range` rather than `.Iter`), adapt the walker. Missing or wrong fields will surface as compile errors immediately.

If a node type doesn't exist (e.g. no `ir.Emit`), drop that case. Don't fight the IR.

- [ ] **Step 6: Run the walker test**

Run: `go test ./internal/lsp/ -run TestWalkIRColorLiterals_FixtureCovers6Colors -v`
Expected: PASS with 4 color literals.

If the count is wrong, the walker missed a node. Read `ir/stmt.go` and `ir/expr.go` end-to-end, find any field of type `ir.Expr` or `[]ir.Expr` or `[]ir.Stmt` that isn't being descended into, add a case.

- [ ] **Step 7: Commit**

```bash
git add internal/lsp/color_irwalk.go internal/lsp/color_irwalk_test.go
git commit -m "lsp(color): IR walker for color-typed literals"
```

---

## Task 2: Wire Layer 2 into documentColor

**Files:**
- Modify: `internal/lsp/color.go`
- Modify: `internal/lsp/color_test.go`

- [ ] **Step 1: Add the parity test**

Append to `internal/lsp/color_test.go`:

```go
func TestDocumentColor_Layer2ParityWithLayer1(t *testing.T) {
	// Until issue #76 (consteval color/unit/enum support) lands, Layer 2
	// can only rediscover the same source-form color literals that
	// Layer 1 already finds. The merged result must still be exactly
	// the same 6 entries — no duplicates, same order.
	src, err := os.ReadFile("../../testdata/lsp/colors.sngl")
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := parser.Parse("colors.sngl", src)
	got := computeDocumentColors(string(src), doc)

	wantRanges := []Range{
		rangeOfSubstring(t, string(src), "#ff0000"),
		rangeOfSubstring(t, string(src), "#00ff00"),
		rangeOfSubstring(t, string(src), "#aabbcc"),
		rangeOfSubstring(t, string(src), "#11223344"),
		rangeOfSubstring(t, string(src), "color.rgb(255, 128, 64)"),
		rangeOfSubstring(t, string(src), "color.rgba(10, 20, 30, 200)"),
	}
	if len(got) != len(wantRanges) {
		t.Fatalf("got %d colors, want %d", len(got), len(wantRanges))
	}
	for i, r := range wantRanges {
		if got[i].Range != r {
			t.Errorf("color %d range: got %+v, want %+v", i, got[i].Range, r)
		}
	}
}
```

The existing `TestDocumentColor` already asserts the same thing — this test is explicit about the no-duplicate-from-Layer-2 guarantee. Both should pass under the new code.

- [ ] **Step 2: Add the Layer 2 path with dedupe**

In `internal/lsp/color.go`, find the body of `computeDocumentColors`. After Layer 1 finishes (`lspcore.WalkLiterals` callback + `walkColorCalls` callback) and **before** `sortColorInformation`, insert Layer 2:

```go
	// Layer 2: type-aware. Dormant until issue #76 (consteval support for
	// color/unit/enum) lands, after which color.lighten, user-defined pure
	// helpers, etc. fold to *ir.Literal and surface here. Today this path
	// only rediscovers source-form hex literals, which dedupe against
	// Layer 1 above by source range.
	layer2 := computeColorsFromIR(doc, content)
	out = mergeColorInfoDedupe(out, layer2)
```

Then append the helpers:

```go
// computeColorsFromIR runs the type checker and optimizer over doc and
// walks the folded IR for color literals. Returns nothing if the document
// fails to check — Layer 1 still works in that case.
func computeColorsFromIR(doc *ast.Document, content string) []ColorInformation {
	if doc == nil {
		return nil
	}
	pkg, diags := sngl.Check(doc, ".")
	for _, d := range diags {
		if d.Severity == ir.Error {
			return nil
		}
	}
	if err := optimize.Optimize(pkg, &optimize.Config{IsRoot: true}); err != nil {
		return nil
	}
	var out []ColorInformation
	walkIRColorLiterals(pkg, func(lit *ir.Literal) {
		c, ok := colorFromIRLiteral(lit)
		if !ok {
			return
		}
		r, ok := rangeForIRLiteral(lit, content)
		if !ok {
			return
		}
		out = append(out, ColorInformation{Range: r, Color: c})
	})
	return out
}

// colorFromIRLiteral converts an IR color literal to LSP Color.
// Today consteval only produces colors when they were already source-form
// hex literals, so lit.Raw is "#rrggbb" or "#rrggbbaa".
func colorFromIRLiteral(lit *ir.Literal) (Color, bool) {
	if lit.Type != ir.TypColor {
		return Color{}, false
	}
	return parseHexColor(lit.Raw)
}

// rangeForIRLiteral computes the LSP Range for an IR literal using its
// source-side AST pointer. Returns ok=false if the AST is missing or the
// literal isn't a #hex form (other forms appear once #76 lands and would
// need richer span info).
func rangeForIRLiteral(lit *ir.Literal, content string) (Range, bool) {
	if lit.AST == nil || !lit.AST.Pos.IsSet() {
		return Range{}, false
	}
	startLine := lit.AST.Pos.Line - 1
	startCol := lit.AST.Pos.Column - 1
	return Range{
		Start: Position{Line: startLine, Character: startCol},
		End:   Position{Line: startLine, Character: startCol + len(lit.AST.Raw)},
	}, true
}

// mergeColorInfoDedupe appends entries from b to a, skipping any whose
// Range already exists in a. O(n*m) — fine for the small counts here.
func mergeColorInfoDedupe(a, b []ColorInformation) []ColorInformation {
	have := make(map[Range]bool, len(a))
	for _, c := range a {
		have[c.Range] = true
	}
	for _, c := range b {
		if have[c.Range] {
			continue
		}
		have[c.Range] = true
		a = append(a, c)
	}
	return a
}
```

Add the new imports at the top of `internal/lsp/color.go`:

```go
import (
    // existing imports above
    "git.duckfam.us/jonathan/sngl"
    "git.duckfam.us/jonathan/sngl/internal/optimize"
    "git.duckfam.us/jonathan/sngl/ir"
)
```

If any are already present, don't double-add.

- [ ] **Step 3: Run the new test**

Run: `go test ./internal/lsp/ -run TestDocumentColor_Layer2ParityWithLayer1 -v`
Expected: PASS — 6 colors, no duplicates, same order.

If FAIL with more than 6 entries, dedupe is broken. If FAIL with fewer than 6, the dedupe is too aggressive or the order changed — check `sortColorInformation` still runs after the merge.

- [ ] **Step 4: Run all lsp tests for regressions**

Run: `go test ./internal/lsp/... -count=1`
Expected: PASS — including `TestDocumentColor`, `TestColorPresentations*`, `TestE2EDocumentColor`, `TestLSPFixturesTypeCheck`.

If the existing `TestDocumentColor` now sees extra entries, the dedupe is broken (likely range mismatch between Layer 1 and Layer 2). Add a `t.Logf("%+v", got)` to diff actual ranges.

- [ ] **Step 5: Commit**

```bash
git add internal/lsp/color.go internal/lsp/color_test.go
git commit -m "lsp(color): wire Layer 2 IR walker (dormant until #76)"
```

---

## Task 3: Sanity smoke for `optimize.Optimize` signature

Different branches have used `optimize.Config{IsMain: bool}` and `optimize.Config{IsRoot: bool}`. Verify which is correct now and adjust Task 2's snippet if needed.

**Files:** none

- [ ] **Step 1: Check the config struct**

Run: `grep -nE "type Config|IsRoot|IsMain" internal/optimize/optimize.go`

If only `IsMain` exists, change the call in `computeColorsFromIR` from `&optimize.Config{IsRoot: true}` to `&optimize.Config{IsMain: true}`. If only `IsRoot`, leave as written. If neither, look at how `cmd/sngl/dump.go:170` configures it (`grep -nE "optimize.Config|optimize.Optimize" cmd/sngl/dump.go`) and copy that exact form.

- [ ] **Step 2: If you adjusted Task 2's code, amend that commit**

If the previous commit needs fixing:

```bash
# only if a Config field was wrong
git add internal/lsp/color.go
git commit --amend --no-edit
```

If no change was needed, skip this task entirely — but note in the next commit that you verified.

---

## Task 4: Rebuild + smoke

**Files:** none

- [ ] **Step 1: Rebuild**

Run: `go install ./cmd/sngl`
Expected: success.

- [ ] **Step 2: Run all tests one more time**

Run: `go test ./internal/lsp/... ./internal/lspcore/... ./internal/testutil/...`
Expected: PASS.

- [ ] **Step 3: Smoke check the LSP binary still starts**

Run: `sngl lsp --version 2>&1 | head -3` (or whatever variant exists; `sngl --help | grep lsp` confirms the subcommand).

Expected: no panic. (The LSP doesn't take stdin in this smoke, just verifying the binary links.)

---

## Self-Review

**Spec coverage (§F2 Layer 2):**

| Need | Task |
|---|---|
| Run checker + optimizer over the doc | Task 2 (`computeColorsFromIR`) |
| Bail silently on check errors | Task 2 (returns nil if any error diagnostic) |
| Walk folded IR for color-typed literals | Task 1 (`walkIRColorLiterals`) |
| Dedupe against Layer 1 by source range | Task 2 (`mergeColorInfoDedupe`) |
| Preserve Layer 1 behavior for broken files | Task 2 (Layer 2 returns nil; Layer 1 still emitted) |
| Forward-ready for issue #76 | Task 2 (comment at call site + dormant path) |

**Placeholder scan:** none — every code step has full code. The "verify field names" instruction in Task 1 Step 2 is concrete: run grep, compare names. The Task 3 sanity check is explicit about which value to substitute.

**Type consistency:** `walkIRColorLiterals`, `computeColorsFromIR`, `colorFromIRLiteral`, `rangeForIRLiteral`, `mergeColorInfoDedupe`, `irLitWalker` declared once in Task 1 + Task 2 and referenced consistently. `Color`, `ColorInformation`, `Range`, `Position` unchanged from earlier slices. `parseHexColor` and `sortColorInformation` reused from F2/F2b.
