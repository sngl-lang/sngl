# LSP Rich Hover Markdown Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the current plain hover output in `internal/lspcore/hover.go` with structured markdown per identifier kind — function signatures, doc comments, const values, unit bodies, color swatches, keyword descriptions — so editors render informative hover floats.

**Architecture:** Extend the existing `lspcore.HoverInfo` per-kind formatters; add a position-aware entry `HoverAt(content, doc, line, col)` to support literal hover (colors). Reuse `walkLiterals` from the F2 implementation in `internal/lsp/color.go` (move to lspcore so both can use it, or duplicate — see Task 1). No new dependencies.

**Tech Stack:** Go, existing `internal/lspcore` and `internal/lsp` packages, existing `ast` package.

**Spec:** `docs/superpowers/specs/2026-05-18-lsp-previews-design.md` §F4.

## Scope decisions (out of this slice)

| Spec item | Status | Reason |
|---|---|---|
| Image embed on component hover | deferred to F1 plan | depends on preview index that doesn't exist yet |
| Stdlib badge + docs-site footer link | deferred | stdlib loader is a TODO in v2 (`hover.go:58`) |
| Method type-param resolution at call site | deferred | needs call-context resolution; significant scope |
| Measurement literal hover | deferred to F3 plan | depends on F3 viewport assumptions |

What this plan covers: functions (signature + doc), vars/consts (type + literal value), structs (doc), enums (doc), units (body + doc), color literals (swatch + rgb), keywords.

---

## File Structure

| File | Status | Responsibility |
|---|---|---|
| `internal/lspcore/hover.go` | modify | Per-kind formatters; add `HoverAt` entry |
| `internal/lspcore/keywords.go` | create | Static keyword → description table |
| `internal/lspcore/walk.go` | create | Move `walkLiterals` here, importable by both lsp and lspcore |
| `internal/lsp/color.go` | modify | Use `lspcore.WalkLiterals` instead of local copy |
| `internal/lsp/hover.go` | modify | Call `lspcore.HoverAt` (position-aware) instead of `Hover` |
| `internal/lspcore/hover_test.go` | create | Table tests per kind |
| `internal/lsp/hover_test.go` | modify | Update existing tests; remove unused `parseForHover` helper noticed in diagnostics |

---

## Task 1: Move `walkLiterals` to `lspcore` so both packages can use it

**Files:**
- Create: `internal/lspcore/walk.go`
- Modify: `internal/lsp/color.go`

- [ ] **Step 1: Read the current walker**

Run: `sed -n '/^func walkLiterals/,/^}/p' internal/lsp/color.go | head -150`

Note the exact function signature and AST type/field names. The walker was hand-adapted in F2 to match the real AST — do not change semantics, only relocate.

- [ ] **Step 2: Create `internal/lspcore/walk.go`**

Copy the walker into `internal/lspcore/walk.go`, exporting it as `WalkLiterals`:

```go
package lspcore

import "git.duckfam.us/jonathan/sngl/ast"

// WalkLiterals invokes fn for every LiteralExpr in the document.
// Minimal walker shared by hover and color features.
func WalkLiterals(doc *ast.Document, fn func(*ast.LiteralExpr)) {
	// [paste the full body of walkLiterals from internal/lsp/color.go verbatim]
}
```

When pasting, leave the inner closures named `walkE`, `walkS`, `walkBlock`, `walkArgs` (or whatever F2's actual implementation uses) — exact preservation is the goal of this task.

- [ ] **Step 3: Replace the local copy in `internal/lsp/color.go`**

Find `func walkLiterals(doc *ast.Document, fn func(*ast.LiteralExpr)) {` and delete the whole function body (including the closing `}`).

In `computeDocumentColors` change the call from `walkLiterals(doc, ...)` to `lspcore.WalkLiterals(doc, ...)`.

Add `"git.duckfam.us/jonathan/sngl/internal/lspcore"` to the imports if not already present.

- [ ] **Step 4: Build and run all tests**

Run: `go build ./... && go test ./internal/lsp/... ./internal/lspcore/...`
Expected: PASS. F2 tests still green.

- [ ] **Step 5: Commit**

```bash
git add internal/lspcore/walk.go internal/lsp/color.go
git commit -m "lspcore: move walkLiterals to lspcore for reuse"
```

---

## Task 2: Function hover — signature, params, return type, doc comment

**Files:**
- Modify: `internal/lspcore/hover.go`
- Create: `internal/lspcore/hover_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/lspcore/hover_test.go`:

```go
package lspcore

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/parser"
)

func hoverOf(t *testing.T, src, word string) string {
	t.Helper()
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return HoverInfo(doc, word)
}

func TestHover_Func_SignatureAndDoc(t *testing.T) {
	src := `// Adds two integers.
// Returns their sum.
func add(a int, b int) int { return a + b }
`
	got := hoverOf(t, src, "add")
	mustContain(t, got, "func add(a int, b int) int")
	mustContain(t, got, "Adds two integers.")
	mustContain(t, got, "Returns their sum.")
}

func TestHover_Func_NoParamsNoReturn(t *testing.T) {
	src := `func tick() {}
`
	got := hoverOf(t, src, "tick")
	mustContain(t, got, "func tick()")
}

func TestHover_Func_ExpressionBody(t *testing.T) {
	src := `func double(x int) int => x * 2
`
	got := hoverOf(t, src, "double")
	mustContain(t, got, "func double(x int) int")
}

func mustContain(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("hover output missing %q\n--- got ---\n%s\n", want, got)
	}
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/lspcore/ -run TestHover_Func -v`
Expected: FAIL — current output for `add` is `func add()` (incorrect, drops params/return).

- [ ] **Step 3: Replace the `*ast.FuncDef` case in `hoverInStmts`**

Edit `internal/lspcore/hover.go`. Find:

```go
case *ast.FuncDef:
    if s.Name == word && !s.Block.IsDefined() && len(s.Params.Params) == 0 {
        return fmt.Sprintf("```sngl\nfunc %s()\n```", s.Name)
    }
```

Replace with:

```go
case *ast.FuncDef:
    if s.Name == word {
        return formatFuncHover(s, doc)
    }
```

Then append `formatFuncHover` to the same file:

```go
func formatFuncHover(f *ast.FuncDef, doc *ast.Document) string {
	var sb strings.Builder
	sb.WriteString("```sngl\nfunc ")
	sb.WriteString(f.Name)
	sb.WriteByte('(')
	for i, p := range f.Params.Params {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(p.Name)
		if t := typeExprString(p.Type); t != "" {
			sb.WriteByte(' ')
			sb.WriteString(t)
		}
	}
	sb.WriteByte(')')
	if rt := typeExprString(f.ReturnType); rt != "" {
		sb.WriteByte(' ')
		sb.WriteString(rt)
	}
	sb.WriteString("\n```\n")
	if d := docForPos(doc, f.Pos); d != "" {
		sb.WriteByte('\n')
		sb.WriteString(d)
		sb.WriteByte('\n')
	}
	return sb.String()
}
```

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/lspcore/ -run TestHover_Func -v`
Expected: all three subtests PASS.

- [ ] **Step 5: Run full lspcore + lsp tests for regressions**

Run: `go test ./internal/lspcore/... ./internal/lsp/...`
Expected: PASS. The old `TestHoverInfo_Computed` in `internal/lsp/hover_test.go` still passes (its assertion only checks the word is present).

- [ ] **Step 6: Commit**

```bash
git add internal/lspcore/hover.go internal/lspcore/hover_test.go
git commit -m "lspcore(hover): rich function signature with params, return, doc"
```

---

## Task 3: Const & Var hover — type + literal value

**Files:**
- Modify: `internal/lspcore/hover.go`
- Modify: `internal/lspcore/hover_test.go`

- [ ] **Step 1: Write failing tests**

Append to `internal/lspcore/hover_test.go`:

```go
func TestHover_Const_WithLiteralValue(t *testing.T) {
	src := `const PI = 3.14
`
	got := hoverOf(t, src, "PI")
	mustContain(t, got, "const PI")
	mustContain(t, got, "3.14")
}

func TestHover_Const_NoValue(t *testing.T) {
	// const without literal default still works (returns just the name+type)
	src := `const Greeting string = "hi"
`
	got := hoverOf(t, src, "Greeting")
	mustContain(t, got, "const Greeting string")
	mustContain(t, got, "\"hi\"")
}

func TestHover_Var_WithTypeAndDefault(t *testing.T) {
	src := `component App { var counter int = 7 }
`
	got := hoverOf(t, src, "counter")
	mustContain(t, got, "var counter int")
	mustContain(t, got, "7")
}

func TestHover_Var_NoType(t *testing.T) {
	src := `component App { var x = 42 }
`
	got := hoverOf(t, src, "x")
	mustContain(t, got, "var x")
	mustContain(t, got, "42")
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/lspcore/ -run "TestHover_Const|TestHover_Var" -v`
Expected: FAIL — current output for consts is `const PI` with no value or type.

- [ ] **Step 3: Replace `VarDecl` and `ConstDecl` cases**

Edit `internal/lspcore/hover.go`. Find the `case *ast.VarDecl:` block and replace it (and the `case *ast.ConstDecl:` block) with:

```go
case *ast.VarDecl:
    if info := formatVarLike(s.Specs, "var", word, doc); info != "" {
        return info
    }
case *ast.ConstDecl:
    if info := formatVarLike(s.Specs, "const", word, doc); info != "" {
        return info
    }
```

Append `formatVarLike` to the file:

```go
func formatVarLike(specs []ast.VarSpec, kw, word string, doc *ast.Document) string {
	for _, spec := range specs {
		for _, name := range spec.Names {
			if name != word {
				continue
			}
			var sb strings.Builder
			sb.WriteString("```sngl\n")
			sb.WriteString(kw)
			sb.WriteByte(' ')
			sb.WriteString(name)
			if t := typeExprString(spec.Type); t != "" {
				sb.WriteByte(' ')
				sb.WriteString(t)
			}
			if v := literalValueString(spec.Default); v != "" {
				sb.WriteString(" = ")
				sb.WriteString(v)
			}
			sb.WriteString("\n```\n")
			// Doc comments only attach to top-level decls; nested vars/consts
			// inside components rarely have them, but try anyway.
			// (No Pos on VarSpec; skip doc lookup here — caller handles it
			// only where we have a Pos.)
			_ = doc
			return sb.String()
		}
	}
	return ""
}

// literalValueString returns a compact rendering for compile-time-constant
// expressions. Currently handles only LiteralExpr; arithmetic/struct values
// fall through to "" (no value shown).
func literalValueString(e ast.Expr) string {
	if e == nil {
		return ""
	}
	if lit, ok := e.(*ast.LiteralExpr); ok {
		return lit.Raw
	}
	return ""
}
```

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/lspcore/ -run "TestHover_Const|TestHover_Var" -v`
Expected: PASS.

- [ ] **Step 5: Full regression run**

Run: `go test ./internal/lspcore/... ./internal/lsp/...`
Expected: PASS. Existing `TestHoverInfo_Var` checks substring `"count"` — still satisfied.

- [ ] **Step 6: Commit**

```bash
git add internal/lspcore/hover.go internal/lspcore/hover_test.go
git commit -m "lspcore(hover): var and const show type and literal value"
```

---

## Task 4: Struct and Enum hover — add doc comments and improve enum body

**Files:**
- Modify: `internal/lspcore/hover.go`
- Modify: `internal/lspcore/hover_test.go`

- [ ] **Step 1: Write failing tests**

Append to `internal/lspcore/hover_test.go`:

```go
func TestHover_Struct_WithDoc(t *testing.T) {
	src := `// A user account.
struct User {
    name string
    age int
}
`
	got := hoverOf(t, src, "User")
	mustContain(t, got, "struct User")
	mustContain(t, got, "A user account.")
	mustContain(t, got, "name string")
	mustContain(t, got, "age int")
}

func TestHover_Enum_WithDoc(t *testing.T) {
	src := `// Account states.
enum Status {
    active
    inactive
}
`
	got := hoverOf(t, src, "Status")
	mustContain(t, got, "enum Status")
	mustContain(t, got, "Account states.")
	mustContain(t, got, "active")
	mustContain(t, got, "inactive")
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/lspcore/ -run "TestHover_Struct_WithDoc|TestHover_Enum_WithDoc" -v`
Expected: FAIL — doc comments not present in output.

- [ ] **Step 3: Update formatters**

Edit `internal/lspcore/hover.go`. Replace `formatStructHover` with a doc-aware version:

```go
func formatStructHover(s *ast.StructDef, doc *ast.Document) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "```sngl\nstruct %s {\n", s.Name)
	for _, f := range s.Fields {
		fmt.Fprintf(&sb, "    %s %s\n", strings.Join(f.Names, ", "), typeExprString(f.Type))
	}
	sb.WriteString("}\n```\n")
	if d := docForPos(doc, s.Pos); d != "" {
		sb.WriteByte('\n')
		sb.WriteString(d)
		sb.WriteByte('\n')
	}
	return sb.String()
}
```

Update the call site to pass `doc`:

```go
case *ast.StructDef:
    if s.Name == word {
        return formatStructHover(s, doc)
    }
```

Replace the inline `EnumDef` case with a doc-aware formatter:

```go
case *ast.EnumDef:
    if s.Name == word {
        return formatEnumHover(s, doc)
    }
```

Append:

```go
func formatEnumHover(e *ast.EnumDef, doc *ast.Document) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "```sngl\nenum %s {\n", e.Name)
	for _, m := range e.Members {
		fmt.Fprintf(&sb, "    %s\n", m.Name)
	}
	sb.WriteString("}\n```\n")
	if d := docForPos(doc, e.Pos); d != "" {
		sb.WriteByte('\n')
		sb.WriteString(d)
		sb.WriteByte('\n')
	}
	return sb.String()
}
```

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/lspcore/ -run "TestHover_Struct_WithDoc|TestHover_Enum_WithDoc" -v`
Expected: PASS.

- [ ] **Step 5: Full regression run**

Run: `go test ./internal/lspcore/... ./internal/lsp/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/lspcore/hover.go internal/lspcore/hover_test.go
git commit -m "lspcore(hover): struct/enum doc comments and multiline body"
```

---

## Task 5: Unit hover (currently missing)

**Files:**
- Modify: `internal/lspcore/hover.go`
- Modify: `internal/lspcore/hover_test.go`

- [ ] **Step 1: Write failing test**

Append to `internal/lspcore/hover_test.go`:

```go
func TestHover_Unit(t *testing.T) {
	src := `// Time measurement.
unit time { ms, s = 1000ms, m = 60s }
`
	got := hoverOf(t, src, "time")
	mustContain(t, got, "unit time")
	mustContain(t, got, "ms")
	mustContain(t, got, "s")
	mustContain(t, got, "Time measurement.")
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/lspcore/ -run TestHover_Unit -v`
Expected: FAIL — no UnitDef case in `hoverInStmts`.

- [ ] **Step 3: Add unit case + formatter**

Edit `internal/lspcore/hover.go`. Inside `hoverInStmts`'s switch, add a case before the closing `}` of the switch:

```go
case *ast.UnitDef:
    if s.Name == word {
        return formatUnitHover(s, doc)
    }
```

Append `formatUnitHover`:

```go
func formatUnitHover(u *ast.UnitDef, doc *ast.Document) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "```sngl\nunit %s {\n", u.Name)
	for _, sfx := range u.Suffixes {
		sb.WriteString("    ")
		sb.WriteString(sfx.Name)
		if sfx.Factor != nil {
			if lit, ok := sfx.Factor.(*ast.LiteralExpr); ok {
				sb.WriteString(" = ")
				sb.WriteString(lit.Raw)
			}
		}
		sb.WriteByte('\n')
	}
	sb.WriteString("}\n```\n")
	if d := docForPos(doc, u.Pos); d != "" {
		sb.WriteByte('\n')
		sb.WriteString(d)
		sb.WriteByte('\n')
	}
	return sb.String()
}
```

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/lspcore/ -run TestHover_Unit -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/lspcore/hover.go internal/lspcore/hover_test.go
git commit -m "lspcore(hover): add unit declarations"
```

---

## Task 6: Component hover — attach doc comment to func body and add doc-only path

The existing `formatComponentHoverWithDoc` already shows component name + params + doc. This task only adds two things: (a) include the component's return type or marker as `component` (already present in current output, leave as-is), and (b) verify required-vs-default param distinction renders.

**Files:**
- Modify: `internal/lspcore/hover_test.go` (add coverage for existing behavior; no code change expected)

- [ ] **Step 1: Add tests verifying current component hover output**

Append:

```go
func TestHover_Component_RequiredVsOptional(t *testing.T) {
	src := `// A counter button.
component Counter(label string, step int = 1) { vbox {} }
`
	got := hoverOf(t, src, "Counter")
	mustContain(t, got, "component Counter")
	mustContain(t, got, "A counter button.")
	mustContain(t, got, "label")
	mustContain(t, got, "step")
	mustContain(t, got, "(required)")
}
```

- [ ] **Step 2: Run test**

Run: `go test ./internal/lspcore/ -run TestHover_Component_RequiredVsOptional -v`
Expected: PASS using current `formatComponentHoverWithDoc` code.

If it FAILS, inspect output and patch `formatComponentHoverWithDoc` only enough to satisfy the test (doc lookup + required marker should already work — if `mustContain "label"` fails, the param-list extraction is the problem). Do not refactor.

- [ ] **Step 3: Commit**

```bash
git add internal/lspcore/hover_test.go
git commit -m "lspcore(hover): regression test for component param formatting"
```

---

## Task 7: Color literal hover — swatch + rgb tuple

This task introduces position-aware hover. The current entry `HoverInfo(doc, word)` only takes a word — insufficient for a literal like `#ff0000` since the word-at-position scanner returns an empty string for non-ident characters. Add `HoverAt(content, doc, line, col)` that tries literal-position lookup first, then falls back to `HoverInfo`.

**Files:**
- Modify: `internal/lspcore/hover.go`
- Modify: `internal/lspcore/hover_test.go`
- Modify: `internal/lsp/hover.go`

- [ ] **Step 1: Write failing test**

Append to `internal/lspcore/hover_test.go`:

```go
func TestHover_ColorLiteral(t *testing.T) {
	src := "component App { var c = #ff0000\n}\n"
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	// Hover at the first '#' in "#ff0000" on line 1 (1-based), the literal
	// begins at column 25 in the source above.
	pos := strings.Index(src, "#ff0000")
	if pos < 0 {
		t.Fatal("literal not found in source")
	}
	col := pos - strings.LastIndex(src[:pos], "\n") // 1-based column of '#'
	got := HoverAt(src, doc, 1, col+1)
	mustContain(t, got, "#ff0000")
	mustContain(t, got, "rgb(255, 0, 0)")
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/lspcore/ -run TestHover_ColorLiteral -v`
Expected: FAIL — `HoverAt` undefined.

- [ ] **Step 3: Implement `HoverAt`**

Append to `internal/lspcore/hover.go`:

```go
// HoverAt returns hover markdown for the cursor position. Tries literal
// hover first (for color/measurement literals where the cursor isn't on
// an identifier word), then falls back to word-based identifier hover.
func HoverAt(content string, doc *ast.Document, line, col int) string {
	if doc != nil {
		if info := hoverLiteralAt(doc, line, col); info != "" {
			return info
		}
	}
	return Hover(content, doc, line, col)
}

func hoverLiteralAt(doc *ast.Document, line, col int) string {
	var found string
	WalkLiterals(doc, func(lit *ast.LiteralExpr) {
		if found != "" {
			return
		}
		if lit.Pos.Line != line {
			return
		}
		startCol := lit.Pos.Column
		endCol := startCol + len(lit.Raw)
		if col < startCol || col >= endCol {
			return
		}
		switch lit.Kind {
		case ast.LiteralColor:
			found = formatColorHover(lit.Raw)
		}
	})
	return found
}

func formatColorHover(raw string) string {
	r, g, b, ok := parseHexRGB(raw)
	if !ok {
		return ""
	}
	return fmt.Sprintf(
		"`%s` &nbsp; <span style=\"display:inline-block;width:1em;height:1em;background:%s;border:1px solid #888\"></span>\n\nrgb(%d, %d, %d)\n",
		raw, raw, r, g, b,
	)
}

// parseHexRGB extracts 0-255 RGB channels from a #rrggbb or #rrggbbaa literal.
// 3-digit forms aren't valid SNGL color tokens (lexer rejects them).
func parseHexRGB(raw string) (r, g, b int, ok bool) {
	if len(raw) < 7 || raw[0] != '#' {
		return 0, 0, 0, false
	}
	hi, ok1 := hexByteHover(raw[1], raw[2])
	mi, ok2 := hexByteHover(raw[3], raw[4])
	lo, ok3 := hexByteHover(raw[5], raw[6])
	if !(ok1 && ok2 && ok3) {
		return 0, 0, 0, false
	}
	return hi, mi, lo, true
}

func hexByteHover(hi, lo byte) (int, bool) {
	h, ok1 := hexNibbleHover(hi)
	l, ok2 := hexNibbleHover(lo)
	if !(ok1 && ok2) {
		return 0, false
	}
	return h*16 + l, true
}

func hexNibbleHover(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}
```

The hex helpers are duplicated (`internal/lsp/color.go` has its own copy). Cross-package code reuse for these helpers is not worth the import cost — both copies are 15 lines each. Leave it.

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/lspcore/ -run TestHover_ColorLiteral -v`
Expected: PASS. If column math is off, the test computes the column from the actual source — there shouldn't be a manual adjustment.

- [ ] **Step 5: Wire `HoverAt` into the LSP handler**

Edit `internal/lsp/hover.go`. Replace the line:

```go
info := lspcore.Hover(fs.Content, fs.Doc, line, col)
```

with:

```go
info := lspcore.HoverAt(fs.Content, fs.Doc, line, col)
```

- [ ] **Step 6: Full regression**

Run: `go test ./internal/lsp/... ./internal/lspcore/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/lspcore/hover.go internal/lspcore/hover_test.go internal/lsp/hover.go
git commit -m "lspcore(hover): color literal swatch and rgb tuple at cursor"
```

---

## Task 8: Keyword hover

**Files:**
- Create: `internal/lspcore/keywords.go`
- Modify: `internal/lspcore/hover.go`
- Modify: `internal/lspcore/hover_test.go`

- [ ] **Step 1: Write failing test**

Append to `internal/lspcore/hover_test.go`:

```go
func TestHover_Keywords(t *testing.T) {
	src := `func add() {}
`
	got := hoverOf(t, src, "func")
	mustContain(t, got, "func")
	mustContain(t, got, "Declares a function")

	got = hoverOf(t, src, "component")
	mustContain(t, got, "component")
	mustContain(t, got, "Declares a UI component")
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/lspcore/ -run TestHover_Keywords -v`
Expected: FAIL — no keyword path.

- [ ] **Step 3: Create keyword table**

Create `internal/lspcore/keywords.go`:

```go
package lspcore

// keywordDocs maps SNGL keywords to a one-line description shown on hover.
// Keep entries brief (one sentence).
var keywordDocs = map[string]string{
	"var":       "Declares a mutable binding.",
	"const":     "Declares an immutable binding.",
	"func":      "Declares a function.",
	"component": "Declares a UI component.",
	"struct":    "Declares a record type.",
	"enum":      "Declares an enumeration type.",
	"unit":      "Declares a unit type with named suffixes (e.g. px, em).",
	"window":    "Declares a top-level window — the entry point of a UI.",
	"if":        "Conditional statement.",
	"else":      "Alternative branch of an if statement.",
	"for":       "Iteration over a list, map, or iterator.",
	"return":    "Returns a value from a function.",
	"import":    "Imports a module.",
	"platform":  "Conditional block for platform-specific code.",
	"true":      "Boolean literal.",
	"false":     "Boolean literal.",
	"nil":       "Null literal.",
}
```

- [ ] **Step 4: Add keyword lookup to `HoverInfo`**

Edit `internal/lspcore/hover.go`. Modify `HoverInfo`:

```go
func HoverInfo(doc *ast.Document, word string) string {
	// Search top-level and component-body statements.
	if info := hoverInStmts(doc.Stmts, doc, word); info != "" {
		return info
	}

	if desc, ok := keywordDocs[word]; ok {
		return fmt.Sprintf("```sngl\n%s\n```\n\n%s\n", word, desc)
	}

	// TODO: stdlib hover lookup (LoadStdlib removed in v2)

	return ""
}
```

- [ ] **Step 5: Run, verify pass**

Run: `go test ./internal/lspcore/ -run TestHover_Keywords -v`
Expected: PASS.

- [ ] **Step 6: Full regression**

Run: `go test ./internal/lspcore/... ./internal/lsp/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/lspcore/keywords.go internal/lspcore/hover.go internal/lspcore/hover_test.go
git commit -m "lspcore(hover): keyword descriptions"
```

---

## Task 9: Cleanup — remove unused `parseForHover` helper

Diagnostics flagged this on Day 1: `internal/lsp/hover_test.go:36: function "parseForHover" is unused`. Clean it up while we're in the file.

**Files:**
- Modify: `internal/lsp/hover_test.go`

- [ ] **Step 1: Delete the unused helper and its sidecar type**

In `internal/lsp/hover_test.go`, delete `parseForHover` (function) and `lspcoreDocArg` (type). They're unused dead code.

- [ ] **Step 2: Build and run tests**

Run: `go test ./internal/lsp/...`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/lsp/hover_test.go
git commit -m "lsp(hover): remove unused test helper"
```

---

## Task 10: Rebuild binary

**Files:** none

- [ ] **Step 1: Rebuild**

Run: `go install ./cmd/sngl`
Expected: success.

- [ ] **Step 2: Manual smoke**

In nvim: `:LspRestart`, open any `.sngl` file with a function declaration, hover over the function name. Expect to see the signature with params and return type instead of bare `func name()`.

Hover over a `#ff0000` color literal — expect to see the hex + an rgb tuple (swatch span may not render in some terminals; that's known).

Hover over `func` keyword — expect to see "Declares a function."

---

## Self-Review

**Spec coverage (F4):**

| Spec row | Task |
|---|---|
| Component: signature, doc, image | Task 6 (image deferred to F1) |
| Function: signature, doc, type params resolved | Task 2 (call-site resolution deferred) |
| Variable / const: type, value | Task 3 |
| Struct: declaration body, doc | Task 4 |
| Enum: declaration body, doc | Task 4 |
| Unit: declaration body | Task 5 |
| Stdlib symbol: badge + link | deferred (stdlib loader is TODO) |
| Color literal: swatch + rgb | Task 7 |
| Measurement literal: resolved px | deferred to F3 |
| Keyword: one-line description | Task 8 |

Deferrals are listed in the scope decisions table at the top.

**Placeholder scan:** none. Every code step has full code shown.

**Type consistency:** `formatFuncHover`, `formatVarLike`, `literalValueString`, `formatStructHover` (signature changed to take `doc`), `formatEnumHover`, `formatUnitHover`, `formatColorHover`, `parseHexRGB`, `hexByteHover`, `hexNibbleHover`, `HoverAt`, `hoverLiteralAt`, `keywordDocs`, `WalkLiterals` — names used consistently across tasks. `formatStructHover` signature changes in Task 4 (adds `doc` param) — the only existing call site in the switch is also updated in the same task.
