# LSP Rich Hover Markdown Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the current plain hover output in `internal/lspcore/hover.go` with structured markdown per identifier kind — function signatures, doc comments, const values, unit bodies, color swatches, keyword descriptions — driven by fixture files and a new `HOVER` test directive that future LSP features (completion, signature help) can reuse.

**Architecture:** Add a `HOVER(target) "substring"` test directive to `internal/testutil` modeled after the existing `ERROR(phase) "substring"` and `FOLD value` directives. Each kind of hover gets a `.sngl` fixture in `testdata/lsp_hover/` with directives describing expected output. A single Go test function iterates fixtures and asserts. Extend the existing `lspcore.HoverInfo` per-kind formatters; add a position-aware entry `HoverAt(content, doc, line, col)` to support literal hover (colors).

**Tech Stack:** Go, existing `internal/lspcore` and `internal/lsp` packages, existing `ast` package, fixture infra in `internal/testutil`.

**Spec:** `docs/superpowers/specs/2026-05-18-lsp-previews-design.md` §F4.

## Scope decisions (out of this slice)

| Spec item | Status | Reason |
|---|---|---|
| Image embed on component hover | deferred to F1 plan | depends on preview index that doesn't exist yet |
| Stdlib badge + docs-site footer link | deferred | stdlib loader is a TODO in v2 (`hover.go:58`) |
| Method type-param resolution at call site | deferred | needs call-context resolution; significant scope |
| Measurement literal hover | deferred to F3 plan | depends on F3 viewport assumptions |

What this plan covers: functions (signature + doc), vars/consts (type + literal value), structs (doc), enums (doc), units (body + doc), color literals (swatch + rgb), keywords.

## HOVER directive format

```
// HOVER(target) "expected substring"
```

`target` is one of:

- A bare word (`func`, `add`, `Counter`) — directive runs hover on the position of the **first occurrence** of that word in the file.
- A `#hex` literal (`#ff0000`) — same first-occurrence rule.
- An explicit position `@<line>:<col>` (1-based) — directive runs hover at that exact cursor.

Multiple directives may share a target; each contributes one substring assertion. A `NOT` directive (`// HOVER-NOT(target) "substring"`) asserts the substring is absent.

Directives are line-anchored in the source comment that contains them, but the **hover position is computed from the target**, not from the directive line. The directive can appear anywhere in the file.

---

## File Structure

| File | Status | Responsibility |
|---|---|---|
| `internal/testutil/hover.go` | create | `HoverDirective`, `ParseHoverDirectives`, `AssertHovers` |
| `internal/lspcore/walk.go` | create | Move `walkLiterals` here, importable by both lsp and lspcore |
| `internal/lspcore/hover.go` | modify | Per-kind formatters; add `HoverAt` entry |
| `internal/lspcore/keywords.go` | create | Static keyword → description table |
| `internal/lsp/color.go` | modify | Use `lspcore.WalkLiterals` instead of local copy |
| `internal/lsp/hover.go` | modify | Call `lspcore.HoverAt` (position-aware) |
| `internal/lspcore/hover_fixture_test.go` | create | Single Go test that iterates `testdata/lsp_hover/*.sngl` |
| `testdata/lsp_hover/func.sngl` | create | Function signature fixtures |
| `testdata/lsp_hover/var_const.sngl` | create | Var/const value fixtures |
| `testdata/lsp_hover/struct.sngl` | create | Struct fixtures |
| `testdata/lsp_hover/enum.sngl` | create | Enum fixtures |
| `testdata/lsp_hover/unit.sngl` | create | Unit fixtures |
| `testdata/lsp_hover/component.sngl` | create | Component fixtures |
| `testdata/lsp_hover/color.sngl` | create | Color literal fixtures |
| `testdata/lsp_hover/keywords.sngl` | create | Keyword fixtures |
| `internal/lsp/hover_test.go` | modify | Remove unused `parseForHover` helper |

---

## Task 1: Add HOVER directive to testutil

**Files:**
- Create: `internal/testutil/hover.go`
- Create: `internal/testutil/hover_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/testutil/hover_test.go`:

```go
package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseHoverDirectives_BareWord(t *testing.T) {
	src := `// HOVER(add) "func add"
func add(a int) int => a + 1
`
	path := writeTemp(t, "bareword.sngl", src)
	dirs, err := ParseHoverDirectives(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 {
		t.Fatalf("got %d dirs, want 1", len(dirs))
	}
	d := dirs[0]
	if d.Target != "add" {
		t.Errorf("target = %q, want %q", d.Target, "add")
	}
	if d.Substring != "func add" {
		t.Errorf("substring = %q, want %q", d.Substring, "func add")
	}
	if d.Negate {
		t.Error("Negate true, want false")
	}
	// Position: "add" appears first at line 2, col 6 (1-based)
	if d.Line != 2 || d.Col != 6 {
		t.Errorf("position = %d:%d, want 2:6", d.Line, d.Col)
	}
}

func TestParseHoverDirectives_HexLiteral(t *testing.T) {
	src := `// HOVER(#ff0000) "rgb(255, 0, 0)"
component App { var c = #ff0000 }
`
	path := writeTemp(t, "color.sngl", src)
	dirs, err := ParseHoverDirectives(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 {
		t.Fatalf("got %d", len(dirs))
	}
	d := dirs[0]
	if d.Target != "#ff0000" || d.Substring != "rgb(255, 0, 0)" {
		t.Errorf("got %+v", d)
	}
	if d.Line != 2 {
		t.Errorf("line = %d, want 2", d.Line)
	}
}

func TestParseHoverDirectives_ExplicitPosition(t *testing.T) {
	src := `// HOVER(@3:7) "something"
//
component Foo {}
`
	path := writeTemp(t, "explicit.sngl", src)
	dirs, err := ParseHoverDirectives(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 {
		t.Fatal("expected 1 dir")
	}
	if dirs[0].Line != 3 || dirs[0].Col != 7 {
		t.Errorf("position = %d:%d, want 3:7", dirs[0].Line, dirs[0].Col)
	}
	if dirs[0].Target != "@3:7" {
		t.Errorf("target = %q", dirs[0].Target)
	}
}

func TestParseHoverDirectives_Negate(t *testing.T) {
	src := `// HOVER-NOT(add) "broken"
func add() {}
`
	path := writeTemp(t, "neg.sngl", src)
	dirs, err := ParseHoverDirectives(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 1 || !dirs[0].Negate {
		t.Fatalf("got %+v", dirs)
	}
}

func TestParseHoverDirectives_TargetNotFound(t *testing.T) {
	src := `// HOVER(missing) "x"
func add() {}
`
	path := writeTemp(t, "missing.sngl", src)
	_, err := ParseHoverDirectives(path)
	if err == nil {
		t.Fatal("expected error for unfindable target")
	}
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/testutil/ -run TestParseHoverDirectives -v`
Expected: FAIL with "undefined: ParseHoverDirectives".

- [ ] **Step 3: Implement directive parser**

Create `internal/testutil/hover.go`:

```go
package testutil

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var hoverRE = regexp.MustCompile(`//\s*HOVER(-NOT)?\(([^)]+)\)\s+"((?:[^"\\]|\\.)*)"`)
var hoverPosRE = regexp.MustCompile(`^@(\d+):(\d+)$`)

// HoverDirective is a parsed // HOVER(target) "substring" comment.
type HoverDirective struct {
	Target    string // raw target text inside the parens
	Substring string // expected substring of hover output (or required-absent if Negate)
	Negate    bool   // true for HOVER-NOT
	Line      int    // 1-based line of the hover position
	Col       int    // 1-based column of the hover position
	DirLine   int    // 1-based line of the directive comment itself (for error messages)
}

// ParseHoverDirectives scans a file for HOVER directives and resolves each
// target to a 1-based source position. Returns an error if any target cannot
// be located in the source.
func ParseHoverDirectives(path string) ([]HoverDirective, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")

	var dirs []HoverDirective
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		m := hoverRE.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		negate := m[1] == "-NOT"
		target := strings.TrimSpace(m[2])
		substring, err := strconv.Unquote(`"` + m[3] + `"`)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: HOVER directive: invalid string: %w", path, lineNum, err)
		}
		line, col, err := resolveTarget(target, lines, lineNum)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: HOVER(%s): %w", path, lineNum, target, err)
		}
		dirs = append(dirs, HoverDirective{
			Target:    target,
			Substring: substring,
			Negate:    negate,
			Line:      line,
			Col:       col,
			DirLine:   lineNum,
		})
	}
	return dirs, scanner.Err()
}

// resolveTarget finds the 1-based source position for a directive target.
// @L:C → explicit. Otherwise: first occurrence of target text in source,
// skipping comment lines (to avoid the directive line matching itself).
func resolveTarget(target string, lines []string, dirLine int) (int, int, error) {
	if m := hoverPosRE.FindStringSubmatch(target); m != nil {
		line, _ := strconv.Atoi(m[1])
		col, _ := strconv.Atoi(m[2])
		return line, col, nil
	}
	for i, l := range lines {
		lineNum := i + 1
		if lineNum == dirLine {
			continue
		}
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		idx := strings.Index(l, target)
		if idx < 0 {
			continue
		}
		return lineNum, idx + 1, nil
	}
	return 0, 0, fmt.Errorf("target %q not found in source", target)
}
```

- [ ] **Step 4: Run tests, verify pass**

Run: `go test ./internal/testutil/ -run TestParseHoverDirectives -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/testutil/hover.go internal/testutil/hover_test.go
git commit -m "testutil: HOVER directive parser for fixture-driven hover tests"
```

---

## Task 2: Move `walkLiterals` to `lspcore`

**Files:**
- Create: `internal/lspcore/walk.go`
- Modify: `internal/lsp/color.go`

- [ ] **Step 1: Read the current walker**

Run: `sed -n '/^func walkLiterals/,/^}/p' internal/lsp/color.go`

Note exact signature and AST field names. F2 adapted this hand-tuned for the real AST — preserve verbatim, only relocate and export.

- [ ] **Step 2: Create `internal/lspcore/walk.go`**

Copy the function body unchanged into a new file:

```go
package lspcore

import "git.duckfam.us/jonathan/sngl/ast"

// WalkLiterals invokes fn for every LiteralExpr in the document.
// Minimal walker shared by hover and color features.
func WalkLiterals(doc *ast.Document, fn func(*ast.LiteralExpr)) {
	// [paste the function body verbatim from internal/lsp/color.go;
	// inner closures keep their original names]
}
```

- [ ] **Step 3: Replace local copy with reference**

In `internal/lsp/color.go`:

- Delete the entire `walkLiterals` function definition.
- In `computeDocumentColors`, change `walkLiterals(doc, ...)` to `lspcore.WalkLiterals(doc, ...)`.
- Add `"git.duckfam.us/jonathan/sngl/internal/lspcore"` to the imports if absent.

- [ ] **Step 4: Build and run all tests**

Run: `go build ./... && go test ./internal/lsp/... ./internal/lspcore/...`
Expected: PASS. F2 tests still green.

- [ ] **Step 5: Commit**

```bash
git add internal/lspcore/walk.go internal/lsp/color.go
git commit -m "lspcore: move walkLiterals to lspcore for reuse"
```

---

## Task 3: Fixture runner harness

This is the meta-task. One Go test discovers every fixture under `testdata/lsp_hover/`, parses directives, parses the source, runs hover at each directive position, asserts substring.

**Files:**
- Create: `internal/lspcore/hover_fixture_test.go`
- Create: `testdata/lsp_hover/_smoke.sngl` (single trivial fixture to validate the harness)

- [ ] **Step 1: Create the smoke fixture**

Create `testdata/lsp_hover/_smoke.sngl`:

```sngl
// HOVER(Counter) "component Counter"
component Counter(label string) { vbox {} }
```

- [ ] **Step 2: Create the harness**

Create `internal/lspcore/hover_fixture_test.go`:

```go
package lspcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/testutil"
)

func TestHoverFixtures(t *testing.T) {
	matches, err := filepath.Glob("../../testdata/lsp_hover/*.sngl")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no hover fixtures found")
	}
	for _, path := range matches {
		path := path
		name := strings.TrimSuffix(filepath.Base(path), ".sngl")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			doc, perr := parser.Parse(filepath.Base(path), src)
			if perr != nil {
				t.Fatalf("parse %s: %v", path, perr)
			}
			dirs, err := testutil.ParseHoverDirectives(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(dirs) == 0 {
				t.Fatal("no HOVER directives in fixture")
			}
			for _, d := range dirs {
				got := HoverAt(string(src), doc, d.Line, d.Col)
				if d.Negate {
					if strings.Contains(got, d.Substring) {
						t.Errorf("HOVER-NOT(%s) %q matched at %d:%d (directive line %d)\n--- got ---\n%s",
							d.Target, d.Substring, d.Line, d.Col, d.DirLine, got)
					}
					continue
				}
				if !strings.Contains(got, d.Substring) {
					t.Errorf("HOVER(%s) missing %q at %d:%d (directive line %d)\n--- got ---\n%s",
						d.Target, d.Substring, d.Line, d.Col, d.DirLine, got)
				}
			}
		})
	}
}
```

- [ ] **Step 3: Verify the harness fails because `HoverAt` doesn't exist yet**

Run: `go test ./internal/lspcore/ -run TestHoverFixtures -v`
Expected: FAIL with "undefined: HoverAt".

- [ ] **Step 4: Add a minimal `HoverAt` that delegates to the existing `Hover`**

Append to `internal/lspcore/hover.go`:

```go
// HoverAt returns hover markdown for the cursor position. Tries literal
// hover first (for color/measurement literals where the cursor isn't on
// an identifier word), then falls back to word-based identifier hover.
func HoverAt(content string, doc *ast.Document, line, col int) string {
	// Literal lookup is added in a later task; for now, just delegate.
	return Hover(content, doc, line, col)
}
```

- [ ] **Step 5: Run, verify smoke fixture passes**

Run: `go test ./internal/lspcore/ -run TestHoverFixtures -v`
Expected: PASS. `Counter` already hovers as a component via existing code.

- [ ] **Step 6: Commit**

```bash
git add internal/lspcore/hover_fixture_test.go internal/lspcore/hover.go testdata/lsp_hover/_smoke.sngl
git commit -m "lspcore(hover): fixture harness + HoverAt entry point"
```

---

## Task 4: Function hover — fixture + implementation

**Files:**
- Create: `testdata/lsp_hover/func.sngl`
- Modify: `internal/lspcore/hover.go`

- [ ] **Step 1: Write the fixture**

Create `testdata/lsp_hover/func.sngl`:

```sngl
// HOVER(add) "func add(a int, b int) int"
// HOVER(add) "Adds two integers."
// HOVER(add) "Returns their sum."
// Adds two integers.
// Returns their sum.
func add(a int, b int) int => a + b

// HOVER(tick) "func tick()"
func tick() {}

// HOVER(double) "func double(x int) int"
func double(x int) int => x * 2
```

- [ ] **Step 2: Run fixture test, verify failure**

Run: `go test ./internal/lspcore/ -run TestHoverFixtures/func -v`
Expected: FAIL — current output for `add` is `func add()`, missing params and return.

- [ ] **Step 3: Replace the `*ast.FuncDef` case in `hoverInStmts`**

In `internal/lspcore/hover.go`, find:

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

Append `formatFuncHover` to the same file:

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

Run: `go test ./internal/lspcore/ -run TestHoverFixtures/func -v`
Expected: PASS.

- [ ] **Step 5: Full regression**

Run: `go test ./internal/lspcore/... ./internal/lsp/...`
Expected: PASS. Existing `TestHoverInfo_Computed` still passes.

- [ ] **Step 6: Commit**

```bash
git add internal/lspcore/hover.go testdata/lsp_hover/func.sngl
git commit -m "lspcore(hover): rich function signature with params, return, doc"
```

---

## Task 5: Var and const hover

**Files:**
- Create: `testdata/lsp_hover/var_const.sngl`
- Modify: `internal/lspcore/hover.go`

- [ ] **Step 1: Write the fixture**

Create `testdata/lsp_hover/var_const.sngl`:

```sngl
// HOVER(PI) "const PI"
// HOVER(PI) "3.14"
const PI = 3.14

// HOVER(Greeting) "const Greeting string"
// HOVER(Greeting) "\"hi\""
const Greeting string = "hi"

// HOVER(counter) "var counter int"
// HOVER(counter) "7"
// HOVER(x) "var x"
// HOVER(x) "42"
component App {
    var counter int = 7
    var x = 42
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/lspcore/ -run TestHoverFixtures/var_const -v`
Expected: FAIL — current output for consts is `const PI` with no value or type.

- [ ] **Step 3: Replace `VarDecl` and `ConstDecl` cases**

In `internal/lspcore/hover.go`, replace those two cases with:

```go
case *ast.VarDecl:
    if info := formatVarLike(s.Specs, "var", word); info != "" {
        return info
    }
case *ast.ConstDecl:
    if info := formatVarLike(s.Specs, "const", word); info != "" {
        return info
    }
```

Append:

```go
func formatVarLike(specs []ast.VarSpec, kw, word string) string {
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

Run: `go test ./internal/lspcore/ -run TestHoverFixtures/var_const -v`
Expected: PASS.

- [ ] **Step 5: Regression**

Run: `go test ./internal/lspcore/... ./internal/lsp/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/lspcore/hover.go testdata/lsp_hover/var_const.sngl
git commit -m "lspcore(hover): var and const show type and literal value"
```

---

## Task 6: Struct and enum hover with doc comments

**Files:**
- Create: `testdata/lsp_hover/struct.sngl`
- Create: `testdata/lsp_hover/enum.sngl`
- Modify: `internal/lspcore/hover.go`

- [ ] **Step 1: Write fixtures**

Create `testdata/lsp_hover/struct.sngl`:

```sngl
// HOVER(User) "struct User"
// HOVER(User) "name string"
// HOVER(User) "age int"
// HOVER(User) "A user account."
// A user account.
struct User {
    name string
    age int
}
```

Create `testdata/lsp_hover/enum.sngl`:

```sngl
// HOVER(Status) "enum Status"
// HOVER(Status) "active"
// HOVER(Status) "inactive"
// HOVER(Status) "Account states."
// Account states.
enum Status {
    active
    inactive
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/lspcore/ -run "TestHoverFixtures/struct|TestHoverFixtures/enum" -v`
Expected: FAIL — doc comments missing; enum body is single-line.

- [ ] **Step 3: Update formatters**

In `internal/lspcore/hover.go`, replace `formatStructHover`:

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

Update call site:

```go
case *ast.StructDef:
    if s.Name == word {
        return formatStructHover(s, doc)
    }
```

Replace the inline `EnumDef` case:

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

Run: `go test ./internal/lspcore/ -run "TestHoverFixtures/struct|TestHoverFixtures/enum" -v`
Expected: PASS.

- [ ] **Step 5: Regression**

Run: `go test ./internal/lspcore/... ./internal/lsp/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/lspcore/hover.go testdata/lsp_hover/struct.sngl testdata/lsp_hover/enum.sngl
git commit -m "lspcore(hover): struct/enum doc comments and multiline body"
```

---

## Task 7: Unit hover (currently missing entirely)

**Files:**
- Create: `testdata/lsp_hover/unit.sngl`
- Modify: `internal/lspcore/hover.go`

- [ ] **Step 1: Write fixture**

Create `testdata/lsp_hover/unit.sngl`:

```sngl
// HOVER(time) "unit time"
// HOVER(time) "ms"
// HOVER(time) "s"
// HOVER(time) "Time measurement."
// Time measurement.
unit time { ms, s = 1000ms, m = 60s }
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/lspcore/ -run TestHoverFixtures/unit -v`
Expected: FAIL — no `UnitDef` case in `hoverInStmts`.

- [ ] **Step 3: Add unit case + formatter**

In `internal/lspcore/hover.go`, inside `hoverInStmts`'s switch, add a case:

```go
case *ast.UnitDef:
    if s.Name == word {
        return formatUnitHover(s, doc)
    }
```

Append:

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

Run: `go test ./internal/lspcore/ -run TestHoverFixtures/unit -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/lspcore/hover.go testdata/lsp_hover/unit.sngl
git commit -m "lspcore(hover): add unit declarations"
```

---

## Task 8: Component hover regression coverage

The existing `formatComponentHoverWithDoc` already covers component name + params + doc. This task adds a fixture pinning that behavior.

**Files:**
- Create: `testdata/lsp_hover/component.sngl`

- [ ] **Step 1: Write fixture**

Create `testdata/lsp_hover/component.sngl`:

```sngl
// HOVER(Counter) "component Counter"
// HOVER(Counter) "A counter button."
// HOVER(Counter) "label"
// HOVER(Counter) "step"
// HOVER(Counter) "(required)"
// A counter button.
component Counter(label string, step int = 1) { vbox {} }
```

- [ ] **Step 2: Run the fixture**

Run: `go test ./internal/lspcore/ -run TestHoverFixtures/component -v`
Expected: PASS using existing code.

If FAIL, inspect output and patch `formatComponentHoverWithDoc` only as needed. Do not refactor.

- [ ] **Step 3: Commit**

```bash
git add testdata/lsp_hover/component.sngl
git commit -m "lspcore(hover): pin component param formatting via fixture"
```

---

## Task 9: Color literal hover

Add literal-position dispatch to `HoverAt` so the cursor on `#ff0000` produces a swatch + rgb tuple.

**Files:**
- Create: `testdata/lsp_hover/color.sngl`
- Modify: `internal/lspcore/hover.go`
- Modify: `internal/lsp/hover.go`

- [ ] **Step 1: Write fixture**

Create `testdata/lsp_hover/color.sngl`:

```sngl
// HOVER(#ff0000) "rgb(255, 0, 0)"
// HOVER(#ff0000) "#ff0000"
// HOVER(#00ff00) "rgb(0, 255, 0)"
// HOVER(#11223344) "rgb(17, 34, 51)"
component App {
    var red = #ff0000
    var green = #00ff00
    var rgba = #11223344
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/lspcore/ -run TestHoverFixtures/color -v`
Expected: FAIL — current `HoverAt` returns empty for cursor on `#`.

- [ ] **Step 3: Extend `HoverAt` with literal-position lookup**

In `internal/lspcore/hover.go`, replace the placeholder `HoverAt` with:

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
	return fmt.Sprintf("```sngl\n%s\n```\n\nrgb(%d, %d, %d)\n", raw, r, g, b)
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

Note: hex helpers are duplicated with `internal/lsp/color.go`'s versions. Not worth a shared package — both copies are short.

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/lspcore/ -run TestHoverFixtures/color -v`
Expected: PASS.

If the position resolution in the directive (column of `#ff0000`) lands on the `#` character but the walker uses 1-based column for the `#` start, both are consistent — should work without adjustment. If failures show column mismatch, the parser's `lit.Pos.Column` is authoritative; investigate by adding a debug `t.Log("pos:", d.Line, d.Col, "lit:", lit.Pos.Line, lit.Pos.Column)` to the harness temporarily.

- [ ] **Step 5: Wire `HoverAt` into the LSP handler**

Edit `internal/lsp/hover.go`. Replace:

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
git add internal/lspcore/hover.go internal/lsp/hover.go testdata/lsp_hover/color.sngl
git commit -m "lspcore(hover): color literal swatch and rgb tuple at cursor"
```

---

## Task 10: Keyword hover

**Files:**
- Create: `internal/lspcore/keywords.go`
- Create: `testdata/lsp_hover/keywords.sngl`
- Modify: `internal/lspcore/hover.go`

- [ ] **Step 1: Write fixture**

Create `testdata/lsp_hover/keywords.sngl`:

```sngl
// HOVER(func) "Declares a function"
// HOVER(component) "Declares a UI component"
// HOVER(struct) "Declares a record type"
func ping() {}
component Foo {}
struct Bar {}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/lspcore/ -run TestHoverFixtures/keywords -v`
Expected: FAIL — keyword path doesn't exist.

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

In `internal/lspcore/hover.go`, modify `HoverInfo`:

```go
func HoverInfo(doc *ast.Document, word string) string {
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

Run: `go test ./internal/lspcore/ -run TestHoverFixtures/keywords -v`
Expected: PASS.

- [ ] **Step 6: Regression**

Run: `go test ./internal/lspcore/... ./internal/lsp/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/lspcore/keywords.go internal/lspcore/hover.go testdata/lsp_hover/keywords.sngl
git commit -m "lspcore(hover): keyword descriptions"
```

---

## Task 11: Cleanup unused test helper

Pre-existing diagnostic from earlier in the session: `internal/lsp/hover_test.go:36: function "parseForHover" is unused`. Clean it up.

**Files:**
- Modify: `internal/lsp/hover_test.go`

- [ ] **Step 1: Delete the unused helper and its sidecar type**

In `internal/lsp/hover_test.go`, delete `parseForHover` (function) and `lspcoreDocArg` (type).

- [ ] **Step 2: Build and run tests**

Run: `go test ./internal/lsp/...`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/lsp/hover_test.go
git commit -m "lsp(hover): remove unused test helper"
```

---

## Task 12: Rebuild binary

**Files:** none

- [ ] **Step 1: Rebuild**

Run: `go install ./cmd/sngl`
Expected: success.

- [ ] **Step 2: Manual smoke**

`:LspRestart` in nvim, open any `.sngl` file with a function declaration, hover the function name — expect signature with params and return type.

Hover a `#ff0000` literal — expect hex + rgb tuple.

Hover `func` keyword — expect "Declares a function."

---

## Self-Review

**Spec coverage (F4):**

| Spec row | Task |
|---|---|
| Component: signature, doc, image | Task 8 (image deferred to F1) |
| Function: signature, doc, type params resolved | Task 4 (call-site resolution deferred) |
| Variable / const: type, value | Task 5 |
| Struct: declaration body, doc | Task 6 |
| Enum: declaration body, doc | Task 6 |
| Unit: declaration body | Task 7 |
| Stdlib symbol: badge + link | deferred (stdlib loader is TODO) |
| Color literal: swatch + rgb | Task 9 |
| Measurement literal: resolved px | deferred to F3 |
| Keyword: one-line description | Task 10 |

Deferrals are listed in the scope decisions table at the top.

**Placeholder scan:** none. Every code step has full code shown.

**Type consistency:** `HoverDirective`, `ParseHoverDirectives`, `WalkLiterals`, `HoverAt`, `hoverLiteralAt`, `formatFuncHover`, `formatVarLike`, `literalValueString`, `formatStructHover` (sig changed to take `doc`), `formatEnumHover`, `formatUnitHover`, `formatColorHover`, `parseHexRGB`, `hexByteHover`, `hexNibbleHover`, `keywordDocs` — names used consistently across tasks. `formatStructHover` signature changes in Task 6; the only existing call site is updated in the same task.
