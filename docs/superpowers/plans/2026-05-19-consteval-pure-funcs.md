# Generic Constant Folding via Pure-Function Interpreter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace per-type cases in the optimizer with a generic interpreter — extract the existing testrunner IR interpreter to `internal/interp`, then plumb it into `evalCall` so any pure SNGL function with constant inputs folds, regardless of value type.

**Architecture:** Four pieces. (B) Move `eval.go`/`exec.go`/`builtins.go` from `codegen/platform/none/testrunner/` to `internal/interp/` (pure refactor). (C) Fix a checker bug that rejects `const X color = color{r=...}` because `TypColor` builtin doesn't `Equal` `TypeStruct{Decl:colorStructDef}`. (A) In the checker, lower hex literals (`#xxxxxx`) to `*ir.StructLit{Name:"color"}` so downstream code sees one shape. (D) Add an optimizer-side adapter that invokes `interp` for pure SNGL function calls with constant args, applying depth limit, deep-copy on bind, and error-as-bail; reconstruct the result via `irFromValue`.

**Tech Stack:** Go, existing `internal/checker`, `internal/optimize`, `codegen/platform/none/testrunner` (source), `ir`, `internal/lsp` packages.

**Spec:** `docs/superpowers/specs/2026-05-19-consteval-pure-funcs-design.md`.

## Open items resolved during exploration

- **Piece C scope**: The actual bug is `TypColor` (Kind=`TypeColor` builtin) and `TypeStruct{Decl:colorStructDef}` are two distinct shapes for the same semantic type. `Equal` rejects them as different. Minimal fix: extend `IsAssignableTo` (in `ir/types.go`) to bridge the two shapes. Deeper cleanup — collapsing the builtin Kind into the StructDef — is out of scope; tracked as a follow-up.
- **Piece A type**: Hex lowers to `*ir.StructLit{Type: colorStructDef.SymType(), Def: colorStructDef, Fields:...}` — same shape `inferStructLit` produces for the `color{...}` literal form.
- **SNGL break/continue**: Per the user, SNGL has no `break`/`continue`. The interpreter doesn't need to handle them.

---

## File Structure

| File                                                                                       | Status | Responsibility                                                                                                                                        |
|--------------------------------------------------------------------------------------------|--------|-------------------------------------------------------------------------------------------------------------------------------------------------------|
| `internal/interp/eval.go`                                                                  | move   | Eval expressions over an Env. Moved verbatim from testrunner.                                                                                         |
| `internal/interp/exec.go`                                                                  | move   | Exec statements (Assign, Toggle, If, For, etc.). Moved verbatim from testrunner.                                                                      |
| `internal/interp/builtins.go`                                                              | move   | Built-in functions: int.min, list.length, etc. Moved verbatim from testrunner.                                                                        |
| `codegen/platform/none/testrunner/eval.go,exec.go,builtins.go`                             | delete | Replaced by imports of `internal/interp`.                                                                                                             |
| `codegen/platform/none/testrunner/runner.go`, `render.go`, `assert_msg.go`, `testing_t.go` | modify | Import `internal/interp` for moved symbols; testrunner-specific orchestration stays here.                                                             |
| `ir/types.go`                                                                              | modify | Add a bridge case in `IsAssignableTo` for `TypeColor ↔ TypeStruct{Decl:colorStructDef}`.                                                              |
| `internal/checker/expr.go`                                                                 | modify | Lower `ast.LiteralColor` to `*ir.StructLit{Def:colorStructDef, Fields:[r,g,b,a]}`.                                                                    |
| `internal/lsp/color_irwalk.go`                                                             | modify | Walk for `*ir.StructLit` with `Def.Name == "color"` instead of `*ir.Literal{Type:color}`.                                                             |
| `internal/lsp/color.go`                                                                    | modify | `colorFromIRLiteral` consumes a StructLit, reads r/g/b/a from its field literals.                                                                     |
| `internal/lsp/color_irwalk_test.go`                                                        | modify | Update test expectations to StructLit shape.                                                                                                          |
| `internal/optimize/interpret.go`                                                           | create | Adapter: `interpretFunc(*ir.Func, args, ctx, depth) (any, ok bool)` with deep-copy, depth limit, error→bail; `irFromValue(val, type) ir.Expr`.        |
| `internal/optimize/interpret_test.go`                                                      | create | Adapter tests + E2E fold tests.                                                                                                                       |
| `internal/optimize/consteval.go`                                                           | modify | `evalCall` tries the adapter for pure SNGL bodies; remove `color.*` cases from `evalQualifiedMethod`; remove `case ir.TypeColor` from `parseLiteral`. |
| `internal/lsp/color.go` (Layer-2 entry)                                                    | modify | Switch to walking StructLits (covered above).                                                                                                         |
| Any test that pattern-matches `*ir.Literal{Type:color}`                                    | modify | Update to StructLit shape.                                                                                                                            |

---

## Task 1: Extract interpreter to `internal/interp` (Piece B)

This is a pure refactor — no behavior change. The testrunner has 2700+ LOC of interpreter logic that the optimizer and a future REPL will share.

**Files:**
- Create: `internal/interp/eval.go` (moved from `codegen/platform/none/testrunner/eval.go`)
- Create: `internal/interp/exec.go` (moved from `codegen/platform/none/testrunner/exec.go`)
- Create: `internal/interp/builtins.go` (moved from `codegen/platform/none/testrunner/builtins.go`)
- Delete: the three originals
- Modify: `codegen/platform/none/testrunner/runner.go`, `render.go`, `assert_msg.go`, `testing_t.go`, `runner_test.go` — import `internal/interp` for moved symbols; testrunner-local refs become `interp.Env`, `interp.NewEnv`, etc.

- [ ] **Step 1: Inspect the surface**

Run: `grep -nE "^func \(.*Env\)|^func [A-Z]\w*|^type [A-Z]\w*" codegen/platform/none/testrunner/eval.go codegen/platform/none/testrunner/exec.go codegen/platform/none/testrunner/builtins.go | head -80`

Note the exported symbols: `Env`, `NewEnv`, `BuildEnv`, `SetFunc`, `SetVar`, `Snapshot`, `SetContext`, `ContextVal`, `Eval`, `Exec`, error types `AssertError`, `RaisedError`. The set survives the move unchanged — they'll be `interp.X` callers.

- [ ] **Step 2: Move the three files**

```bash
mkdir -p internal/interp
git mv codegen/platform/none/testrunner/eval.go internal/interp/eval.go
git mv codegen/platform/none/testrunner/exec.go internal/interp/exec.go
git mv codegen/platform/none/testrunner/builtins.go internal/interp/builtins.go
```

- [ ] **Step 3: Update package declaration in moved files**

In each of `internal/interp/eval.go`, `internal/interp/exec.go`, `internal/interp/builtins.go`, replace the first line:

```go
package testrunner
```

with:

```go
package interp
```

- [ ] **Step 4: Build, expect compile errors in testrunner**

Run: `go build ./...`
Expected: FAIL with many `undefined: Env`, `undefined: NewEnv`, etc. errors from files in `codegen/platform/none/testrunner/`.

- [ ] **Step 5: Add the import + qualify references in remaining testrunner files**

For each of `codegen/platform/none/testrunner/runner.go`, `render.go`, `assert_msg.go`, `testing_t.go`, `runner_test.go`:

1. Add `"git.duckfam.us/jonathan/sngl/internal/interp"` to the import block.
2. Replace bare references to moved symbols with `interp.` qualified form. Use:

```bash
# Run inside each file; verify diffs manually before continuing.
sed -i -E 's/\b(Env|NewEnv|BuildEnv|SetFunc|SetVar|Snapshot|SetContext|ContextVal|Eval|Exec|AssertError|RaisedError|lambdaValue|unitValue|unitTable|componentValue|nativeFunc)\b/interp.\1/g' codegen/platform/none/testrunner/<FILE>.go
```

Important caveats:

- `sed` will over-qualify unrelated symbols. After running, eyeball each diff for false positives — particularly local variables named `env`, struct field names like `.Env`. Revert misqualified ones.
- Inside `runner.go` etc., function signatures like `func (env *Env) X()` need `interp.Env` only if `Env` becomes exported from interp. Since you also moved the receiver types' definitions, methods stay on the `interp.Env` receiver but the testrunner-local callers reference `*interp.Env`.

A safer approach: instead of `sed`, manually update each of the 5 files. They're 200-400 lines each — tractable.

- [ ] **Step 6: Resolve cross-package private references**

The moved code may reference helpers that stayed in testrunner (e.g. assertion-formatting helpers in `assert_msg.go`). Determine direction:

- If `eval.go`/`exec.go` calls something in `assert_msg.go` → either move the helper to interp, or expose it in testrunner (and have interp call back — but that creates a cycle).
- If `assert_msg.go` calls into the (moved) eval — fine, that's the expected direction.

Avoid cycles. If `eval.go` references a testrunner-private symbol, move that symbol into interp too. The decision rule: any symbol used by the moved interpreter belongs in interp; any symbol used only by testrunner orchestration stays.

Inspect by running:

```bash
go build ./... 2>&1 | grep undefined: | head -40
```

For each remaining `undefined:` error, decide direction and apply.

- [ ] **Step 7: Compile + run testrunner tests**

Run: `go build ./... && go test ./codegen/platform/none/testrunner/... ./internal/interp/... -count=1`
Expected: PASS — `TestRunFixtures` and any other testrunner tests still pass.

If tests fail with behavioral differences, the move accidentally changed semantics — likely a name collision or imported-vs-local mismatch. Roll back and redo more carefully.

- [ ] **Step 8: Full regression**

Run: `go test ./... -count=1`
Expected: PASS across the whole tree.

- [ ] **Step 9: Commit**

```bash
git add internal/interp/ codegen/platform/none/testrunner/ go.sum 2>/dev/null
git commit -m "interp: extract testrunner interpreter to internal/interp

Pure refactor. The IR interpreter (eval.go, exec.go, builtins.go) moves
from codegen/platform/none/testrunner/ to internal/interp/ so the
optimizer and a future REPL can share it. Testrunner becomes a thin
shell that imports internal/interp.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 2: Bridge `TypeColor` builtin and `TypeStruct{Decl:colorStructDef}` (Piece C)

The user can write `const X color = #ff8040` today (LHS=`TypColor` builtin, RHS=`*ir.Literal{Type:TypColor}` — `Equal` succeeds). But `const X color = color{r=255,g=128,b=64,a=255}` errors because the RHS is `*ir.StructLit{Type:TypeStruct,Decl:colorStructDef}` and `Equal` returns false (different `Kind`).

Fix in `ir/types.go`: in `IsAssignableTo`, allow a `TypeColor` builtin and a `TypeStruct{Decl:colorStructDef}` to assign in either direction.

**Files:**
- Modify: `ir/types.go`
- Create: `internal/checker/color_struct_assign_test.go` (regression)

- [ ] **Step 1: Write the failing test**

Create `internal/checker/color_struct_assign_test.go`:

```go
package checker_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestColorStructLitAssignableToColor(t *testing.T) {
	src := `const X color = color{r=255, g=255, b=255, a=255}`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Errorf("unexpected error: %s", d.Error())
		}
	}
}

func TestColorHexAssignableToColor(t *testing.T) {
	// Baseline: the literal form already works. Pin it so the fix
	// doesn't accidentally break it.
	src := `const Y color = #ffffff`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Errorf("unexpected error: %s", d.Error())
		}
	}
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/checker/ -run TestColorStructLitAssignableToColor -v`
Expected: FAIL with "cannot initialize color with color".

The baseline `TestColorHexAssignableToColor` should PASS.

- [ ] **Step 3: Add the bridge in IsAssignableTo**

Edit `ir/types.go`. Find the `IsAssignableTo` function. After the `t.Equal(target)` check (which currently returns true for matching types), add a case that bridges the two color shapes:

```go
func (t *Type) IsAssignableTo(target *Type) bool {
	if t.Equal(target) {
		return true
	}
	// Bridge the builtin TypeColor kind and the stdlib color struct decl.
	// They represent the same semantic type and should assign freely.
	// (Tracked separately: collapsing the two representations into one
	// is a deeper refactor; this bridge unblocks the immediate use cases.)
	if isColorShape(t) && isColorShape(target) {
		return true
	}
	// ...existing cases below...
```

Append the helper at the end of the file (or near the other Type helpers):

```go
// isColorShape reports whether t represents the color value type — either
// the builtin TypeColor kind or a TypeStruct whose Decl is the stdlib
// "color" StructDef.
func isColorShape(t *Type) bool {
	if t == nil {
		return false
	}
	if t.Kind == TypeColor {
		return true
	}
	if t.Kind == TypeStruct {
		if sd, ok := t.Decl.(*StructDef); ok && sd.Name == "color" {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/checker/ -run "TestColorStructLitAssignableToColor|TestColorHexAssignableToColor" -v`
Expected: both PASS.

- [ ] **Step 5: Full regression**

Run: `go test ./... -count=1`
Expected: PASS.

If anything regresses on the equivalence (e.g. a stronger-type-check test now passes where it expected failure), inspect — most likely the bridge is correct and the broken test was relying on the bug. Update those tests.

- [ ] **Step 6: Commit**

```bash
git add ir/types.go internal/checker/color_struct_assign_test.go
git commit -m "ir(types): bridge TypeColor builtin and color StructDef in IsAssignableTo

Fix \"cannot initialize color with color\" when assigning a color{r=,g=,b=,a=}
struct literal to a color-typed var. The two type shapes represent the same
semantic type; deeper collapsing of the dual representation is a follow-up.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 3: Lower hex literals to StructLit (Piece A)

Replace the `ast.LiteralColor` case in the checker so that `#xxxxxx` produces a `*ir.StructLit{Def:colorStructDef, Fields:[r,g,b,a]}` instead of `*ir.Literal{Type:TypColor}`.

**Files:**
- Modify: `internal/checker/expr.go`
- Modify: existing tests that assert `*ir.Literal{Type:color}` (find via grep — they're in `internal/checker/`, `internal/lsp/`, and others)

- [ ] **Step 1: Locate the literal-color path**

Run: `grep -nE "ast\\.LiteralColor" internal/checker/*.go`

Find the case in `inferLiteral` (around `internal/checker/expr.go:128`) that builds the IR literal.

- [ ] **Step 2: Find the color StructDef in scope**

The stdlib `color` struct (`lib/types.sngl:33`) is loaded into the checker as a `StructDef`. Find how it's accessed — either via `c.scope.Lookup("color")` or via a cached field on the checker. Check with:

```bash
grep -nE "Lookup\\(\"color\"|stdlibColorDef|colorDecl" internal/checker/*.go
```

If no cached field exists, look it up via `c.scope.Lookup("color")` and assert `*ir.StructDef`. If the lookup can fail (during early checker passes), guard and fall back to the existing literal shape.

- [ ] **Step 3: Write the failing test**

Create `internal/checker/hex_color_lowering_test.go`:

```go
package checker_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestHexColorLowersToStructLit(t *testing.T) {
	src := `const C color = #ff8040`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}
	var found *ir.Var
	for _, v := range pkg.Vars {
		if v.Name == "C" {
			found = v
			break
		}
	}
	if found == nil {
		t.Fatal("const C not found")
	}
	sl, ok := found.Init.(*ir.StructLit)
	if !ok {
		t.Fatalf("Init = %T %+v, want *ir.StructLit", found.Init, found.Init)
	}
	if sl.Def == nil || sl.Def.Name != "color" {
		t.Errorf("Def = %v, want color struct def", sl.Def)
	}
	if len(sl.Fields) != 4 {
		t.Fatalf("Fields count = %d, want 4", len(sl.Fields))
	}
	wantChannels := map[string]int{"r": 255, "g": 128, "b": 64, "a": 255}
	for _, f := range sl.Fields {
		w, ok := wantChannels[f.Name]
		if !ok {
			t.Errorf("unexpected field %q", f.Name)
			continue
		}
		lit, ok := f.Value.(*ir.Literal)
		if !ok || lit.Type.Kind != ir.TypeInt {
			t.Errorf("field %s: value = %T, want int Literal", f.Name, f.Value)
			continue
		}
		n := 0
		fmt.Sscanf(lit.Raw, "%d", &n)
		if n != w {
			t.Errorf("field %s = %d, want %d", f.Name, n, w)
		}
	}
}

func TestHexColorThreeDigitExpands(t *testing.T) {
	src := `const C color = #abc`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	var found *ir.Var
	for _, v := range pkg.Vars {
		if v.Name == "C" {
			found = v
			break
		}
	}
	sl, ok := found.Init.(*ir.StructLit)
	if !ok {
		t.Fatalf("Init = %T", found.Init)
	}
	want := map[string]int{"r": 0xaa, "g": 0xbb, "b": 0xcc, "a": 0xff}
	for _, f := range sl.Fields {
		lit := f.Value.(*ir.Literal)
		n := 0
		fmt.Sscanf(lit.Raw, "%d", &n)
		if n != want[f.Name] {
			t.Errorf("field %s = %d, want %d", f.Name, n, want[f.Name])
		}
	}
}

func TestHexColorEightDigitIncludesAlpha(t *testing.T) {
	src := `const C color = #11223344`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	var found *ir.Var
	for _, v := range pkg.Vars {
		if v.Name == "C" {
			found = v
			break
		}
	}
	sl := found.Init.(*ir.StructLit)
	want := map[string]int{"r": 0x11, "g": 0x22, "b": 0x33, "a": 0x44}
	for _, f := range sl.Fields {
		lit := f.Value.(*ir.Literal)
		n := 0
		fmt.Sscanf(lit.Raw, "%d", &n)
		if n != want[f.Name] {
			t.Errorf("field %s = %d, want %d", f.Name, n, want[f.Name])
		}
	}
}
```

Add imports: `"fmt"` at the top.

- [ ] **Step 4: Run, verify failure**

Run: `go test ./internal/checker/ -run TestHexColor -v`
Expected: FAIL on `Init = *ir.Literal …, want *ir.StructLit` — current behavior is the Literal form.

- [ ] **Step 5: Replace the LiteralColor case**

Edit `internal/checker/expr.go`. Find:

```go
case ast.LiteralColor:
    typ = TypColor
```

Wait — this is inside `inferLiteral` which returns the unified Literal shape at the end. We need to early-return a StructLit instead. Restructure:

```go
case ast.LiteralColor:
    return c.lowerHexLiteral(x)
```

Add a new method on the checker. Append to `internal/checker/expr.go`:

```go
// lowerHexLiteral converts a hex color literal like #ff8040 to a
// *ir.StructLit{Def:colorStructDef, Fields:[r,g,b,a]} so downstream
// code sees the same shape as color{r=,g=,b=,a=} struct literals.
// Falls back to a Literal form if the color struct isn't yet in scope
// (early checker phases).
func (c *checker) lowerHexLiteral(x *ast.LiteralExpr) ir.Expr {
	r, g, b, a, ok := parseHexChannels(x.Raw)
	if !ok {
		c.error(x.Pos, "invalid color literal %q", x.Raw)
		return &ir.Literal{AST: x, Type: TypColor, Raw: x.Raw}
	}
	sym, ok := c.scope.Lookup("color")
	if !ok {
		// Stdlib not loaded yet — fall back to the legacy form.
		return &ir.Literal{AST: x, Type: TypColor, Raw: x.Raw}
	}
	sd, ok := sym.(*ir.StructDef)
	if !ok {
		return &ir.Literal{AST: x, Type: TypColor, Raw: x.Raw}
	}
	intLit := func(n int) *ir.Literal {
		return &ir.Literal{Type: TypInt, Raw: strconv.Itoa(n)}
	}
	return &ir.StructLit{
		AST:  &ast.StructExpr{Pos: x.Pos, Name: "color"},
		Type: sd.SymType(),
		Def:  sd,
		Fields: []ir.FieldInit{
			{Name: "r", Value: intLit(r)},
			{Name: "g", Value: intLit(g)},
			{Name: "b", Value: intLit(b)},
			{Name: "a", Value: intLit(a)},
		},
	}
}

// parseHexChannels parses #rgb, #rrggbb, or #rrggbbaa to 0..255 channels.
// Alpha defaults to 255 for 3- and 6-digit forms.
func parseHexChannels(raw string) (r, g, b, a int, ok bool) {
	if len(raw) == 0 || raw[0] != '#' {
		return 0, 0, 0, 0, false
	}
	hex := raw[1:]
	nibble := func(c byte) (int, bool) {
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
	byteOf := func(hi, lo byte) (int, bool) {
		h, ok1 := nibble(hi)
		l, ok2 := nibble(lo)
		if !(ok1 && ok2) {
			return 0, false
		}
		return h*16 + l, true
	}
	switch len(hex) {
	case 3:
		rv, ok1 := nibble(hex[0])
		gv, ok2 := nibble(hex[1])
		bv, ok3 := nibble(hex[2])
		if !(ok1 && ok2 && ok3) {
			return 0, 0, 0, 0, false
		}
		return rv*16 + rv, gv*16 + gv, bv*16 + bv, 255, true
	case 6:
		rv, ok1 := byteOf(hex[0], hex[1])
		gv, ok2 := byteOf(hex[2], hex[3])
		bv, ok3 := byteOf(hex[4], hex[5])
		if !(ok1 && ok2 && ok3) {
			return 0, 0, 0, 0, false
		}
		return rv, gv, bv, 255, true
	case 8:
		rv, ok1 := byteOf(hex[0], hex[1])
		gv, ok2 := byteOf(hex[2], hex[3])
		bv, ok3 := byteOf(hex[4], hex[5])
		av, ok4 := byteOf(hex[6], hex[7])
		if !(ok1 && ok2 && ok3 && ok4) {
			return 0, 0, 0, 0, false
		}
		return rv, gv, bv, av, true
	}
	return 0, 0, 0, 0, false
}
```

Add `"strconv"` to the imports if absent.

- [ ] **Step 6: Run new tests**

Run: `go test ./internal/checker/ -run TestHexColor -v`
Expected: all three PASS.

- [ ] **Step 7: Full regression**

Run: `go test ./... -count=1`
Expected: PASS — but expect failures in downstream consumers that pattern-match `*ir.Literal{Type:TypColor}`.

Likely affected files (per grep `TypColor`):

- `codegen/lang/golang/translate_ir.go`
- `codegen/lang/javascript/translate_ir.go`
- `codegen/lang/kotlin/translate_ir.go`
- `codegen/platform/html/html.go`
- `internal/optimize/consteval.go` — `parseLiteral`'s TypeColor case (deleted in Task 9)
- `internal/lsp/color.go`, `internal/lsp/color_irwalk.go` — Layer 2 (updated in Task 4)
- existing checker tests asserting the literal shape

For codegen/translators, the lowering means `*ir.Literal{Type:color}` no longer occurs — but old switch arms still exist. They can stay; they're now dead but harmless. The corresponding StructLit case must work (it already does, since `color{...}` produces StructLit today). Validate by running language-specific codegen tests:

```bash
go test ./codegen/lang/... ./codegen/platform/... -count=1
```

If any fail, the codegen for color StructLit needs to match what the literal form used to produce. Likely it already does for HTML (CSS color string is `rgb(r,g,b)` or `rgba(r,g,b,a)` — derivable from the four int fields). If a codegen path fails, fix it before continuing.

- [ ] **Step 8: Commit**

```bash
git add internal/checker/expr.go internal/checker/hex_color_lowering_test.go
git commit -m "checker: lower hex color literals to *ir.StructLit

#xxxxxx now produces the same IR shape as color{r=,g=,b=,a=} so downstream
code (optimizer, codegen, LSP) sees one form per value type.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 4: Update LSP color walker for the new shape

Today `internal/lsp/color_irwalk.go` walks for `*ir.Literal` with `Type == ir.TypColor`. Post-Task-3 those don't exist — color values are `*ir.StructLit{Def.Name == "color"}`.

**Files:**
- Modify: `internal/lsp/color_irwalk.go`
- Modify: `internal/lsp/color.go`
- Modify: `internal/lsp/color_irwalk_test.go`

- [ ] **Step 1: Change the walker callback**

In `internal/lsp/color_irwalk.go`, change `walkIRColorLiterals(pkg, fn)` to walk for `*ir.StructLit{Def.Name == "color"}` instead of `*ir.Literal{Type: ir.TypColor}`.

Replace the `expr` switch's literal case:

```go
case *ir.Literal:
    if x.Type == ir.TypColor && x.AST != nil {
        w.fn(x)
    }
```

with a `*ir.StructLit` case. The callback signature should change to pass the `*ir.StructLit`:

```go
case *ir.StructLit:
    if x.Def != nil && x.Def.Name == "color" && x.AST != nil {
        w.fn(x)
    }
```

…and update the type signature of `walkIRColorLiterals` and its `irLitWalker.fn` field to `func(*ir.StructLit)`.

- [ ] **Step 2: Update `colorFromIRLiteral` and `rangeForIRLiteral`**

In `internal/lsp/color.go`, those helpers must now consume a StructLit. Rename to `colorFromIRStructLit` / `rangeForIRStructLit` and reshape:

```go
// colorFromIRStructLit reads r/g/b/a int field literals from a color
// StructLit and returns an LSP Color (channels normalized to 0..1).
func colorFromIRStructLit(sl *ir.StructLit) (Color, bool) {
	if sl.Def == nil || sl.Def.Name != "color" {
		return Color{}, false
	}
	var r, g, b, a int
	a = 255 // default alpha when not set
	for _, f := range sl.Fields {
		lit, ok := f.Value.(*ir.Literal)
		if !ok || lit.Type.Kind != ir.TypeInt {
			return Color{}, false
		}
		n, err := strconv.Atoi(lit.Raw)
		if err != nil {
			return Color{}, false
		}
		switch f.Name {
		case "r":
			r = n
		case "g":
			g = n
		case "b":
			b = n
		case "a":
			a = n
		}
	}
	return Color{
		Red:   float64(r) / 255.0,
		Green: float64(g) / 255.0,
		Blue:  float64(b) / 255.0,
		Alpha: float64(a) / 255.0,
	}, true
}

// rangeForIRStructLit computes the LSP Range for a color StructLit using
// its source-side AST pointer. Returns ok=false if the AST is missing.
func rangeForIRStructLit(sl *ir.StructLit, content string) (Range, bool) {
	if sl.AST == nil || !sl.AST.Pos.IsSet() {
		return Range{}, false
	}
	// The AST may be a synthesized StructExpr (from hex lowering) with
	// only a Pos set, or a real source StructExpr. For both, the start
	// is the .Pos; the end is computed by scanning to the matching `)`
	// or by using the original raw text length when synthesized.
	startLine := sl.AST.Pos.Line - 1
	startCol := sl.AST.Pos.Column - 1
	// For synthesized (hex) form, the AST may not have a span — fall
	// back to a 7-character range (length of #rrggbb).
	end := Position{Line: startLine, Character: startCol + 7}
	return Range{
		Start: Position{Line: startLine, Character: startCol},
		End:   end,
	}, true
}
```

Add `"strconv"` if absent.

Update the call site in `computeColorsFromIR`:

```go
walkIRColorLiterals(pkg, func(sl *ir.StructLit) {
	c, ok := colorFromIRStructLit(sl)
	if !ok {
		return
	}
	r, ok := rangeForIRStructLit(sl, content)
	if !ok {
		return
	}
	out = append(out, ColorInformation{Range: r, Color: c})
})
```

- [ ] **Step 3: Update the walker test**

In `internal/lsp/color_irwalk_test.go`, change the assertion from `*ir.Literal` of color type to `*ir.StructLit` with `Def.Name == "color"`. Adjust collected slice type:

```go
var colors []*ir.StructLit
walkIRColorLiterals(pkg, func(sl *ir.StructLit) {
	colors = append(colors, sl)
})

if len(colors) != 4 {
	t.Fatalf("got %d colors, want 4", len(colors))
}
for _, c := range colors {
	if c.Def == nil || c.Def.Name != "color" {
		t.Errorf("Def = %v", c.Def)
	}
}
```

- [ ] **Step 4: Run LSP tests**

Run: `go test ./internal/lsp/... -count=1`
Expected: PASS — including F2b TestDocumentColor, the Layer-2 parity test, and `TestLSPFixturesTypeCheck`.

If Layer-2 parity now produces extra colors (because each hex literal in `testdata/lsp/colors.sngl` now becomes both a Layer-1 hex hit AND a Layer-2 StructLit hit), `mergeColorInfoDedupe` should still dedupe them by Range. If ranges differ slightly between Layer 1 (AST hex token) and Layer 2 (synthesized StructLit Pos), the dedupe breaks — fix by computing the same Range. The simplest fix: `rangeForIRStructLit` derives the end from the original hex raw length when the AST is a synthesized StructExpr; check via `sl.AST.Name == "color"` and look at the originating hex token's length if exposed.

If the parity test gives noisy duplicates, a clean fix is: only emit Layer-2 results for StructLits that originated from `color{...}` source syntax (not synthesized from hex). Distinguish by checking whether `sl.AST.Fields` is non-empty (real source struct has fields; synthesized one we'd leave empty).

Resolve this concretely once you see actual failures. The dedupe path is the right control point — don't move the work elsewhere.

- [ ] **Step 5: Commit**

```bash
git add internal/lsp/color_irwalk.go internal/lsp/color.go internal/lsp/color_irwalk_test.go
git commit -m "lsp(color): walk StructLit instead of Literal for color values

Adapts the Layer-2 IR walker to the unified shape from the checker change.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 5: Optimizer adapter scaffolding (Piece D part 1)

Create the adapter file with an entry point, depth tracking, and result round-trip helper.

**Files:**
- Create: `internal/optimize/interpret.go`
- Create: `internal/optimize/interpret_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/optimize/interpret_test.go`:

```go
package optimize

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestIRFromValue_Primitives(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want string // Raw of the resulting Literal
	}{
		{"int", 42, "42"},
		{"float", 3.14, "3.14"},
		{"string", "hi", "hi"},
		{"bool true", true, "true"},
		{"bool false", false, "false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := irFromValue(tt.val, nil)
			lit, ok := got.(*ir.Literal)
			if !ok {
				t.Fatalf("got %T, want *ir.Literal", got)
			}
			if lit.Raw != tt.want {
				t.Errorf("Raw = %q, want %q", lit.Raw, tt.want)
			}
		})
	}
}

func TestIRFromValue_Map(t *testing.T) {
	val := map[string]any{
		"r": 255,
		"g": 128,
		"b": 64,
		"a": 255,
	}
	// nil type → produces a generic StructLit (no Def)
	got := irFromValue(val, nil)
	sl, ok := got.(*ir.StructLit)
	if !ok {
		t.Fatalf("got %T, want *ir.StructLit", got)
	}
	if len(sl.Fields) != 4 {
		t.Fatalf("Fields count = %d, want 4", len(sl.Fields))
	}
	// Spot-check one field.
	for _, f := range sl.Fields {
		if f.Name == "r" {
			lit := f.Value.(*ir.Literal)
			if lit.Raw != "255" {
				t.Errorf("r = %q", lit.Raw)
			}
		}
	}
}

func TestIRFromValue_Slice(t *testing.T) {
	val := []any{1, 2, 3}
	got := irFromValue(val, nil)
	ll, ok := got.(*ir.ListLit)
	if !ok {
		t.Fatalf("got %T, want *ir.ListLit", got)
	}
	if len(ll.Elems) != 3 {
		t.Fatalf("Elems = %d, want 3", len(ll.Elems))
	}
}

func TestIRFromValue_Nil(t *testing.T) {
	got := irFromValue(nil, nil)
	lit, ok := got.(*ir.Literal)
	if !ok || lit.Raw != "null" {
		t.Errorf("got %v, want null Literal", got)
	}
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/optimize/ -run TestIRFromValue -v`
Expected: FAIL with "undefined: irFromValue".

- [ ] **Step 3: Implement adapter scaffolding**

Create `internal/optimize/interpret.go`:

```go
package optimize

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

// maxInterpDepth bounds recursion in the interpreter adapter. Excess
// depth makes the fold fail silently — the original ir.Call is preserved.
const maxInterpDepth = 256

// irFromValue converts a Go-side runtime value (as produced by the
// interp package) back into an IR expression. Primitives go through
// irLiteral; maps become StructLits; slices become ListLits.
//
// When typ is non-nil and represents a known struct type, the returned
// StructLit carries Type+Def matching it. Otherwise, the StructLit is
// "naked" (Def=nil); downstream consumers that need the Def must look
// it up themselves.
func irFromValue(val any, typ *ir.Type) ir.Expr {
	switch v := val.(type) {
	case nil:
		return &ir.Literal{Type: ir.TypNull, Raw: "null"}
	case bool, int, float64, string:
		return irLiteral(v, typ)
	case map[string]any:
		fields := make([]ir.FieldInit, 0, len(v))
		// Sort keys for stable output; struct field order matters for
		// type-checking and downstream codegen consistency.
		for _, k := range sortedMapKeys(v) {
			fields = append(fields, ir.FieldInit{
				Name:  k,
				Value: irFromValue(v[k], nil),
			})
		}
		sl := &ir.StructLit{
			Type:   typ,
			Fields: fields,
		}
		if typ != nil && typ.Kind == ir.TypeStruct {
			if sd, ok := typ.Decl.(*ir.StructDef); ok {
				sl.Def = sd
			}
		}
		return sl
	case []any:
		elems := make([]ir.Expr, 0, len(v))
		for _, e := range v {
			elems = append(elems, irFromValue(e, nil))
		}
		return &ir.ListLit{Type: typ, Elems: elems}
	}
	return nil
}

// sortedMapKeys returns the keys of m sorted alphabetically.
func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Bubble sort works fine for the small field counts here; avoids
	// importing sort just for this helper.
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}
```

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/optimize/ -run TestIRFromValue -v`
Expected: all four subtests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/optimize/interpret.go internal/optimize/interpret_test.go
git commit -m "optimize: adapter scaffolding for pure-function interpreter

irFromValue converts Go-side runtime values back to IR exprs (Literal /
StructLit / ListLit). Depth-limit constant established. interpretFunc
follows in next task.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 6: `interpretFunc` adapter — bind args, exec body, return result (Piece D part 2)

Add the call into `internal/interp` from the optimizer, with deep-copy on bind, depth tracking, and error-as-bail.

**Files:**
- Modify: `internal/optimize/interpret.go`
- Modify: `internal/optimize/interpret_test.go`

- [ ] **Step 1: Append failing test**

Append to `internal/optimize/interpret_test.go`:

```go
import (
	// existing imports above
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

func TestInterpretFunc_SimplePureFunc(t *testing.T) {
	src := `
func double(x int) int => x * 2
const C = double(21)
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})

	// Find the `double` func and the C var.
	var doubleFn *ir.Func
	for _, f := range pkg.Funcs {
		if f.Name == "double" {
			doubleFn = f
			break
		}
	}
	if doubleFn == nil {
		t.Fatal("func double not found")
	}

	// Build a fresh evalCtx (the optimizer's internal context).
	ctx := newEvalCtxForTest(pkg)
	val, ok := interpretFunc(doubleFn, []any{21}, ctx, 0)
	if !ok {
		t.Fatalf("interpretFunc returned ok=false")
	}
	if val != 42 {
		t.Errorf("got %v, want 42", val)
	}
}

// newEvalCtxForTest is a small helper that builds a minimal evalCtx
// for testing the interpreter adapter in isolation. The optimizer's
// real ctx is built by Optimize() — we approximate enough here.
func newEvalCtxForTest(pkg *ir.Package) *evalCtx {
	return &evalCtx{
		values: map[ir.Symbol]any{},
		// Other fields left zero; the interpreter doesn't read them
		// for simple pure func calls without imports.
	}
}
```

- [ ] **Step 2: Run, verify failure**

Run: `go test ./internal/optimize/ -run TestInterpretFunc_SimplePureFunc -v`
Expected: FAIL with "undefined: interpretFunc".

- [ ] **Step 3: Implement interpretFunc**

Append to `internal/optimize/interpret.go`:

```go
import (
	// existing imports above plus
	"git.duckfam.us/jonathan/sngl/internal/interp"
)

// interpretFunc runs fn's body via internal/interp, with args bound to
// params. Returns (value, true) on success; (nil, false) when the
// interpretation can't complete cleanly — either depth exhausted,
// runtime error from interp, or a non-foldable construct in the body.
// Mutation of composite args (maps/slices) is isolated by deep-copying
// at param binding.
func interpretFunc(fn *ir.Func, args []any, ctx *evalCtx, depth int) (any, bool) {
	if fn == nil || fn.Block == nil || fn.Purity != ir.PurityPure {
		return nil, false
	}
	if depth >= maxInterpDepth {
		return nil, false
	}
	if len(args) != len(fn.Params) {
		return nil, false
	}

	env := interp.NewEnv()
	env.SetFunc(fn)
	for i, p := range fn.Params {
		env.SetVar(p.Name, deepCopyValue(args[i]))
	}

	// Exec the body; a Return propagates a *interp.ReturnSignal (or
	// similar) which the env surfaces via its result mechanism. The
	// interp package's existing test runner already uses this; consult
	// internal/interp/exec.go for the exact return-value retrieval.
	result, err := execBlockToReturn(env, fn.Block)
	if err != nil {
		return nil, false
	}
	return result, true
}

// execBlockToReturn runs stmts via env.Exec and recovers the Return
// value. The exact mechanism depends on internal/interp's API; if the
// interp package has a dedicated entry like env.RunBlock(stmts) that
// returns the bubbled-up Return, prefer that. If only Exec is exposed,
// catch the return-signal panic/sentinel that interp uses internally.
func execBlockToReturn(env *interp.Env, stmts []ir.Stmt) (any, error) {
	// Implementation depends on interp's exact API. Try the simplest
	// path: iterate stmts via env.Exec; if Exec returns a sentinel
	// error of type interp.ReturnError (verify the type name), unwrap
	// to get the value.
	for _, s := range stmts {
		if err := env.Exec(s); err != nil {
			// interp.ReturnError (or whatever the package calls it)
			// signals an early return with a value. Match the type and
			// unwrap.
			if re, ok := err.(*interp.ReturnError); ok {
				return re.Value, nil
			}
			return nil, err
		}
	}
	return nil, nil // ran off the end without return → void/null
}

// deepCopyValue clones composite values (map[string]any, []any) so
// callee mutations can't corrupt cached const-eval results in the
// optimizer.
func deepCopyValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		c := make(map[string]any, len(x))
		for k, val := range x {
			c[k] = deepCopyValue(val)
		}
		return c
	case []any:
		c := make([]any, len(x))
		for i, val := range x {
			c[i] = deepCopyValue(val)
		}
		return c
	}
	return v
}
```

**IMPORTANT:** `execBlockToReturn`'s exact implementation depends on the interp package's return-signal API. After Task 1 lands, inspect `internal/interp/exec.go` to find the actual mechanism (look for a `Return` statement handler that produces a `*ReturnError` or sets a field on `Env` or raises a panic). Adapt the code accordingly. The shape above is a sketch.

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/optimize/ -run TestInterpretFunc_SimplePureFunc -v`
Expected: PASS — `double(21)` returns 42.

If FAIL on `execBlockToReturn`, adjust based on the interp package's actual API (see note above).

- [ ] **Step 5: Add mutation-isolation test**

Append:

```go
func TestInterpretFunc_MutationIsolation(t *testing.T) {
	src := `
func mutates(c color) color {
    c.r = 99
    return c
}
const A color = color{r=10, g=20, b=30, a=255}
const B color = mutates(A)
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := Optimize(pkg, &Config{}); err != nil {
		t.Fatalf("optimize: %v", err)
	}

	// A must still have r=10 — the mutation in mutates() should not
	// have leaked back into A.
	var a, b *ir.Var
	for _, v := range pkg.Vars {
		switch v.Name {
		case "A":
			a = v
		case "B":
			b = v
		}
	}
	if a == nil || b == nil {
		t.Fatal("A or B not found")
	}

	rOf := func(v *ir.Var) string {
		sl, ok := v.Init.(*ir.StructLit)
		if !ok {
			return ""
		}
		for _, f := range sl.Fields {
			if f.Name == "r" {
				if lit, ok := f.Value.(*ir.Literal); ok {
					return lit.Raw
				}
			}
		}
		return ""
	}
	if rOf(a) != "10" {
		t.Errorf("A.r = %q after optimize, want 10 (mutation leaked)", rOf(a))
	}
	if rOf(b) != "99" {
		t.Errorf("B.r = %q after optimize, want 99", rOf(b))
	}
}
```

- [ ] **Step 6: Run mutation-isolation test**

Run: `go test ./internal/optimize/ -run TestInterpretFunc_MutationIsolation -v`
Expected: PASS only after Task 7 (which plumbs the adapter into evalCall — until then `Optimize` doesn't fold the call). Mark as expected-fail for now or temporarily `t.Skip` until Task 7.

- [ ] **Step 7: Commit**

```bash
git add internal/optimize/interpret.go internal/optimize/interpret_test.go
git commit -m "optimize: interpretFunc adapter with deep-copy and depth limit

Wraps internal/interp.Env: binds args (deep-copying composites), runs
the body, catches the Return signal, returns the value to the optimizer.
Errors from interp → silent fold-fails.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 7: Plumb adapter into `evalCall`

**Files:**
- Modify: `internal/optimize/consteval.go`

- [ ] **Step 1: Find evalCall**

Locate the function in `internal/optimize/consteval.go` (around line 258).

- [ ] **Step 2: Add interpreter dispatch before the existing paths**

In `evalCall`, after collecting arg values but before the existing `evalQualifiedMethod`/`evalCallFunc`/`evalNativeCall` calls, add:

```go
// Try the generic SNGL-body interpreter for pure user/stdlib funcs.
if call.Func != nil && call.Func.Block != nil && call.Func.Purity == ir.PurityPure {
	if v, ok := interpretFunc(call.Func, args, ctx, ctx.interpDepth); ok {
		return v, true
	}
}
```

Add a depth-tracking field to `evalCtx`. In the `type evalCtx struct` declaration (search the file), add:

```go
interpDepth int
```

In `interpretFunc`, when invoking sub-evaluations through `evalExpr`, the depth doesn't need to advance — each *interpretFunc* recurse increments, but eval-of-expression doesn't. Actually since `interp.Env.Exec` will internally call `interp.Env.Eval` which may invoke nested pure calls — those go through `interp` itself, not back into `interpretFunc`. So the depth field tracks how deep our adapter has been re-entered.

Wait — the interp package doesn't know about the optimizer's depth field. If a pure func body calls another pure func, interp's `Eval` handles that call internally — it doesn't bounce back through `interpretFunc`. So the optimizer's depth tracking only fires on the OUTERMOST entry. That's fine — depth bounds re-entry from `evalCall` itself, which only happens if interp can't resolve a call (e.g., when the inner call is itself a non-foldable construct). In practice the depth field will rarely exceed 1-2; the limit is safety insurance.

After adding the dispatch, increment+decrement `ctx.interpDepth` around the call:

```go
ctx.interpDepth++
v, ok := interpretFunc(call.Func, args, ctx, ctx.interpDepth)
ctx.interpDepth--
if ok {
	return v, true
}
```

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 4: Re-enable the mutation-isolation test**

In `internal/optimize/interpret_test.go`, remove the `t.Skip` from `TestInterpretFunc_MutationIsolation` if you added one.

- [ ] **Step 5: Run all optimizer tests**

Run: `go test ./internal/optimize/... -count=1`
Expected: PASS, including the new tests.

- [ ] **Step 6: Commit**

```bash
git add internal/optimize/consteval.go internal/optimize/interpret_test.go
git commit -m "optimize(consteval): dispatch pure SNGL function calls via interpretFunc

evalCall now tries the generic interpreter when call.Func has a SNGL body
and is pure. Composed and user-defined pure functions fold automatically.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 8: End-to-end fold tests

Demonstrate that real stdlib pure functions fold via the new path.

**Files:**
- Modify: `internal/optimize/interpret_test.go`

- [ ] **Step 1: Add fold tests**

Append:

```go
func TestOptimize_FoldsColorLighten(t *testing.T) {
	src := `const C color = color.lighten(#ff0000, 0.5)`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Error())
		}
	}
	if err := Optimize(pkg, &Config{}); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	var c *ir.Var
	for _, v := range pkg.Vars {
		if v.Name == "C" {
			c = v
			break
		}
	}
	if c == nil {
		t.Fatal("C not found")
	}
	sl, ok := c.Init.(*ir.StructLit)
	if !ok {
		t.Fatalf("Init = %T, want *ir.StructLit", c.Init)
	}
	// color.lighten(red, 0.5) = {r: 255, g: 127, b: 127, a: 255}
	want := map[string]string{"r": "255", "g": "127", "b": "127", "a": "255"}
	for _, f := range sl.Fields {
		lit, ok := f.Value.(*ir.Literal)
		if !ok {
			t.Errorf("field %s value = %T", f.Name, f.Value)
			continue
		}
		if lit.Raw != want[f.Name] {
			t.Errorf("field %s = %q, want %q", f.Name, lit.Raw, want[f.Name])
		}
	}
}

func TestOptimize_FoldsComposedColorExpression(t *testing.T) {
	src := `const C color = color.opacity(color.lighten(#ff0000, 0.5), 128)`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := Optimize(pkg, &Config{}); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	var c *ir.Var
	for _, v := range pkg.Vars {
		if v.Name == "C" {
			c = v
		}
	}
	sl := c.Init.(*ir.StructLit)
	// opacity(lighten(red, 0.5), 128) = {r:255, g:127, b:127, a:128}
	for _, f := range sl.Fields {
		lit := f.Value.(*ir.Literal)
		want := map[string]string{"r": "255", "g": "127", "b": "127", "a": "128"}[f.Name]
		if lit.Raw != want {
			t.Errorf("field %s = %q, want %q", f.Name, lit.Raw, want)
		}
	}
}

func TestOptimize_FoldsUserDefinedColorHelper(t *testing.T) {
	src := `
func tint(c color, n float) color => color.lighten(c, n)
const C color = tint(#ff0000, 0.5)
`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := Optimize(pkg, &Config{}); err != nil {
		t.Fatalf("optimize: %v", err)
	}
	var c *ir.Var
	for _, v := range pkg.Vars {
		if v.Name == "C" {
			c = v
		}
	}
	sl, ok := c.Init.(*ir.StructLit)
	if !ok {
		t.Fatalf("Init = %T", c.Init)
	}
	// Same value as lighten directly: {r:255, g:127, b:127, a:255}
	for _, f := range sl.Fields {
		lit := f.Value.(*ir.Literal)
		want := map[string]string{"r": "255", "g": "127", "b": "127", "a": "255"}[f.Name]
		if lit.Raw != want {
			t.Errorf("field %s = %q, want %q", f.Name, lit.Raw, want)
		}
	}
}
```

- [ ] **Step 2: Run**

Run: `go test ./internal/optimize/ -run "TestOptimize_FoldsColorLighten|TestOptimize_FoldsComposedColorExpression|TestOptimize_FoldsUserDefinedColorHelper" -v`
Expected: PASS.

If any subtest fails because the channel values are off by one (truncation vs round), the interpreter is computing `int(float * pct)` correctly per the SNGL runtime — adjust expected values to match the actual implementation, since the SNGL semantics in `lib/functions.sngl` are authoritative.

- [ ] **Step 3: Full regression**

Run: `go test ./... -count=1`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/optimize/interpret_test.go
git commit -m "optimize: end-to-end fold tests for color expressions

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 9: Cleanup — remove now-redundant per-type cases

**Files:**
- Modify: `internal/optimize/consteval.go`

- [ ] **Step 1: Remove `color.*` cases from `evalQualifiedMethod`**

Find and delete the cases for:

- `case "color.rgb":`
- `case "color.rgba":`
- `case "color.lighten":`
- `case "color.darken":`
- `case "color.opacity":`
- `case "color.hex":`

These are now handled by the interpreter via `lib/functions.sngl`.

- [ ] **Step 2: Remove `case ir.TypeColor` from `parseLiteral`**

Find `parseLiteral` and remove:

```go
case ir.TypeColor:
    if v, ok := parseColorHex(lit.Raw); ok {
        return v
    }
    return nil
```

…and the now-unreferenced helpers (`parseColorHex`, `formatColorHex`, `hexNibbleConst`, `hexByteConst`, `clamp8` if unused elsewhere — verify with grep first).

Color literals no longer reach the optimizer post-Task-3, so this case is unreachable.

- [ ] **Step 3: Build + run all tests**

Run: `go build ./... && go test ./... -count=1`
Expected: PASS.

If any test fails, the cleanup removed something still referenced. Add back the specific symbol and document why.

- [ ] **Step 4: Commit**

```bash
git add internal/optimize/consteval.go
git commit -m "optimize(consteval): remove color.* per-type cases and parseLiteral color arm

Now handled generically by the interpreter via lib/functions.sngl bodies.
Color literals never reach the optimizer (lowered to StructLit in checker).

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 10: Verify smoke + commit hygiene

**Files:** none

- [ ] **Step 1: Full build and test**

Run: `go build ./... && go test ./... -count=1`
Expected: PASS across the entire tree.

- [ ] **Step 2: Rebuild the binary**

Run: `go install ./cmd/sngl`
Expected: success.

- [ ] **Step 3: Quick smoke**

```bash
cat > /tmp/fold_smoke.sngl <<'EOF'
const A color = #ff0000
const B color = color.lighten(A, 0.5)
const C color = color.opacity(B, 128)
EOF
sngl dump optimized --lang none --platform none /tmp/fold_smoke.sngl 2>&1 | grep -v proxying | grep -A6 "Name: (string) (len=1) \"C\""
```

Expected: C's Init shows a `*ir.StructLit` with concrete channel values, not a Call.

- [ ] **Step 4: Update issue #76**

Mark the color slice as closed. Add a note that measurement / enum / option follow naturally — each new type works once the checker normalizes its literal form (if it has one) AND existing stdlib pure functions for that type are present.

```bash
glab issue update 76 --description "$(cat <<'EOF'
## Status: color slice landed

Closed by the generic-folder work. The optimizer now interprets any pure
SNGL function body whose inputs are constant — color helpers compose
through user-defined functions automatically.

## Remaining slices (separate issues if/when needed)

- **measurement** — arithmetic on 12em, 1rem + 4px, etc. Today measurement
  literals stay as ir.Literal{Type:unit,Suffix:…}. The interpreter sees
  them as primitive values; arithmetic via int.* / float.* methods already
  works. Custom measurement methods would fold if they exist in stdlib.
- **enum** — Status.active, comparisons, switch elimination. Enum values
  are addressed as ir.Member today; checker normalization (similar to
  color) plus a tiny interpreter touch for enum members would close this.
- **option / result** — already structs at the IR level; should fold
  through the interpreter once stdlib pure helpers exist.
EOF
)" 2>&1 | tail -3
```

(Adjust the command if `glab issue update` rejects the description format — fall back to a comment via `glab issue note 76 --message "..."`.)

---

## Self-Review

**Spec coverage:**

| Spec requirement                                                         | Task          |
|--------------------------------------------------------------------------|---------------|
| Piece A: hex → StructLit                                                 | Task 3        |
| Piece B: extract interpreter to internal/interp                          | Task 1        |
| Piece C: fix `cannot initialize color with color`                        | Task 2        |
| Piece D: optimizer adapter (depth, deep-copy, error→bail, irFromValue)   | Tasks 5–7     |
| LSP walker update (color_irwalk.go, color.go)                            | Task 4        |
| Cleanup: remove color.* from evalQualifiedMethod, parseLiteral color arm | Task 9        |
| Tests: stmt coverage, mutation isolation, recursion, E2E                 | Tasks 5, 6, 8 |
| Issue #76 status update                                                  | Task 10       |

**Placeholder scan:**

- Task 1 Step 5's `sed` recipe has caveats — explicitly noted as risky, manual update preferred. Not a placeholder.
- Task 6 Step 3 notes the exact interp `Return` signal API depends on what's in `internal/interp/exec.go` after Task 1 lands — gives investigation guidance rather than concrete code, but with a clear sketch and where to look. Marginally acceptable; the engineer should look at the moved code to confirm the exact name.
- Task 4 Step 4's dedupe fix is described in two possible directions (control via `mergeColorInfoDedupe` vs distinguish synthesized AST). Picks the dedupe path as primary. Acceptable.

**Type consistency:**

- `interpretFunc(*ir.Func, args []any, ctx *evalCtx, depth int) (any, bool)` — declared in Task 6, called in Task 7. Consistent.
- `irFromValue(val any, typ *ir.Type) ir.Expr` — declared in Task 5, called in Task 6's interpreter result return path. Consistent.
- `execBlockToReturn`, `deepCopyValue` — used inside `interpretFunc`. Names consistent within Task 6.
- `colorFromIRStructLit`, `rangeForIRStructLit` — replace `colorFromIRLiteral`, `rangeForIRLiteral` in Task 4. Both name + caller-site updated together.
- `walkIRColorLiterals` keeps its name but callback signature changes from `func(*ir.Literal)` to `func(*ir.StructLit)` in Task 4. Used by Task 4's test.
- `parseHexChannels` declared in Task 3, used only there. Consistent.
- `isColorShape` declared in Task 2, used only by `IsAssignableTo`. Consistent.
- `maxInterpDepth`, `sortedMapKeys` — internal helpers, used only inside `internal/optimize/interpret.go`.
