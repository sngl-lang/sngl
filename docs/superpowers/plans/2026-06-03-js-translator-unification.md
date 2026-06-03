# JS Translator Unification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Retire the legacy `ExprScope`-based `translate_ir.go` JS translator, routing all html JavaScript emission through the single `JsIRContext` + irwalk + `WalkLowered` pipeline, with byte-identical generated output.

**Architecture:** The html platform always uses the JavaScript translator (`codegen.LookupLang("js")`). Two translators exist today: legacy `translate_ir.go` (`*codegen.ExprScope`, direct recursion, exposed via `LangTranslator.TranslateIRExpr`/`TranslateIRMutation`) and new `ircontext.go` (`JsIRContext`, `*codegen.ExprCtx`, irwalk visitor, fed via `scopedJC()`+`codegen.WalkLowered`+`htmlTranslator`). We close the new path's feature gaps (test-first), consolidate helper-flagging onto `ExprCtx.Helpers`, switch html's three legacy call sites to the unified pipeline, then delete the legacy code and trim the two methods off the `LangTranslator` interface. Go/Kotlin keep their copies as concrete (non-interface) funcs; `codegen.ExprScope`/`NeededHelpers` stay for them.

**Tech Stack:** Go; the SNGL compiler (`internal/parser`, `internal/checker`, `internal/optimize`, `internal/lower`); codegen packages under `codegen/`. Tests are `go test` assertion-style (`strings.Contains`) plus a new golden-snapshot parity harness.

**Reference spec:** `docs/superpowers/specs/2026-06-03-js-translator-unification-design.md`

**Working location:** Per project convention, work directly on `main` (no worktree). Commit frequently.

**Build/test commands:**
- Single package: `go test ./codegen/lang/javascript/...`
- html platform: `go test ./codegen/platform/html/...`
- Full suite: `go tool verify`
- Compile check: `go build ./...`

---

## File Structure

**Created:**
- `codegen/lang/javascript/jshelpers.go` — relocated free helpers that survive the deletion of `translate_ir.go` (`factoryName`, `sanitizeJSIdent`, `isIntIR`, `isPluralKeyMapType`, `isBundledImport`, `isBundledNativePkg`, and a new ctx-based `nativeBundledNamespaceAliasCtx`).
- `codegen/platform/html/parity_golden_test.go` — golden-snapshot harness compiling a fixture corpus to html and diffing committed `.golden` files. `SNGL_UPDATE_GOLDEN=1` regenerates.
- `codegen/platform/html/testdata/parity/*.sngl` — curated fixtures exercising every gap feature.
- `codegen/platform/html/testdata/parity/*.golden` — committed baseline output.

**Modified:**
- `codegen/lang/javascript/ircontext.go` — gain the ~10 missing behaviors; switch helper-flagging to `jc.Ctx.Helpers` (already does).
- `codegen/lang/javascript/ircontext_test.go` — characterization tests for each ported behavior.
- `codegen/platform/html/html.go` — wire `g.ctx.Helpers`; route `emitJSFunc`, `exprToJS`, delete `translateHandlerStmt`; stop constructing `g.scope`.
- `codegen/lang/javascript/javascript.go` — remove `TranslateIRExpr`/`TranslateIRMutation` methods.
- `codegen/codegen.go` — remove the two methods from the `LangTranslator` interface.

**Deleted (final phase):**
- `codegen/lang/javascript/translate_ir.go`
- `codegen/lang/javascript/translate_ir_test.go` (after porting its cases)

---

## Phase 0: Parity golden baseline

Establishes the byte-identical bar before any behavior moves. The current mixed output (handlers via new path, funcs/init via legacy) is captured as truth.

### Task 0.1: Curated parity fixtures

**Files:**
- Create: `codegen/platform/html/testdata/parity/intdiv.sngl`
- Create: `codegen/platform/html/testdata/parity/elemref.sngl`
- Create: `codegen/platform/html/testdata/parity/regex_bool.sngl`
- Create: `codegen/platform/html/testdata/parity/asynclambda.sngl`
- Create: `codegen/platform/html/testdata/parity/component.sngl`
- Create: `codegen/platform/html/testdata/parity/userfunc.sngl`

- [ ] **Step 1: Write fixtures that each compile cleanly to html and hit a gap feature**

Each fixture must be a valid, checkable SNGL program. Model them on existing root `testdata/*.sngl` (read 3-4 first to match idiom). Minimum coverage:

- `intdiv.sngl`: a computed func returning `a / b` where both are `int` (exercises `Math.trunc` division) and a reactive prop binding it.
- `elemref.sngl`: an element ref (`@`-bound node) whose `.value`/`.text` is read in an expression and written in a click handler (exercises `IsElementRef` read + `OnPropAssign` write).
- `regex_bool.sngl`: a func using `regex("...")` and a `bool(x)` conversion.
- `asynclambda.sngl`: a list `.map`/`.filter` or funcvar taking an `async` lambda (exercises async lambda prefix).
- `component.sngl`: a parent that instantiates a child component (exercises `CreateComponent` → factory) plus a user-defined method call (exercises namespace/type-method dispatch).
- `userfunc.sngl`: a multi-statement user function with a `let` local, an `if`, and a `return` (exercises `emitJSFunc` body path).

If i18n plural/PluralKey fixtures are needed, copy the shapes asserted in `codegen/lang/javascript/translate_ir_test.go` (`TestPluralKeyMapLitLowersToPlainObject`, `TestEmitI18nPlural`) into a `i18n.sngl` fixture.

- [ ] **Step 2: Verify each fixture checks without error**

Run for each file:
```bash
go run ./cmd/sngl dump checked codegen/platform/html/testdata/parity/intdiv.sngl
```
Expected: no diagnostics, prints the checked IR. Repeat for every fixture. Fix any that error before continuing.

- [ ] **Step 3: Commit fixtures**

```bash
git add codegen/platform/html/testdata/parity/*.sngl
git commit -m "test(html): add parity fixtures for JS translator unification"
```

### Task 0.2: Golden parity harness

**Files:**
- Create: `codegen/platform/html/parity_golden_test.go`
- Test: itself

- [ ] **Step 1: Write the harness**

```go
package html

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	_ "git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	_ "git.duckfam.us/jonathan/sngl/codegen/lang/none"
)

// TestParityGolden compiles every codegen/platform/html/testdata/parity/*.sngl
// to html and compares against the committed .golden file. This is the
// byte-identical parity bar for the JS translator unification: the output must
// not change as emission moves from the legacy path to JsIRContext.
//
// Regenerate goldens with: SNGL_UPDATE_GOLDEN=1 go test ./codegen/platform/html/ -run TestParityGolden
func TestParityGolden(t *testing.T) {
	update := os.Getenv("SNGL_UPDATE_GOLDEN") == "1"
	srcs, err := filepath.Glob("testdata/parity/*.sngl")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(srcs)
	if len(srcs) == 0 {
		t.Fatal("no parity fixtures found")
	}
	for _, src := range srcs {
		src := src
		t.Run(filepath.Base(src), func(t *testing.T) {
			got := generateHTML(t, src)
			golden := strings.TrimSuffix(src, ".sngl") + ".golden"
			if update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden (run with SNGL_UPDATE_GOLDEN=1 to create): %v", err)
			}
			if got != string(want) {
				t.Errorf("output differs from golden %s\n--- got ---\n%s", golden, got)
			}
		})
	}
}
```

Note: reuse the existing `generateHTML(t, path)` helper in `codegen/platform/html/html_test.go:63` (it parses, checks, optimizes, lowers, generates, returns the `.html` string).

- [ ] **Step 2: Generate the baseline goldens**

```bash
SNGL_UPDATE_GOLDEN=1 go test ./codegen/platform/html/ -run TestParityGolden
```
Expected: PASS, writes `*.golden` files alongside fixtures.

- [ ] **Step 3: Verify the harness now passes without update**

```bash
go test ./codegen/platform/html/ -run TestParityGolden -v
```
Expected: PASS for every fixture.

- [ ] **Step 4: Commit baseline**

```bash
git add codegen/platform/html/parity_golden_test.go codegen/platform/html/testdata/parity/*.golden
git commit -m "test(html): golden parity harness + baseline for JS unification"
```

**After every later task, run `go test ./codegen/platform/html/ -run TestParityGolden` — it MUST stay green. The only task permitted to regenerate goldens is one with an explicitly justified output change (none are expected; if one occurs, stop and report the diff before regenerating).**

---

## Phase 1: Close feature gaps in JsIRContext (test-first)

Each task: write a characterization test asserting the exact JS the legacy path produces, watch it fail on the new path, implement, watch it pass, confirm golden + full suite still green, commit.

### Task 1.1: Relocate surviving helpers out of translate_ir.go

This unblocks all later gap tasks (they reference these helpers) and shrinks `translate_ir.go` to only-deletable content by Phase 4.

**Files:**
- Create: `codegen/lang/javascript/jshelpers.go`
- Modify: `codegen/lang/javascript/translate_ir.go` (remove the moved funcs)

- [ ] **Step 1: Move the helpers**

Cut these definitions from `translate_ir.go` and paste into a new `jshelpers.go` (package `javascript`), unchanged except as noted: `factoryName`, `sanitizeJSIdent`, `isIntIR`, `isPluralKeyMapType`, `isBundledImport`, `isBundledNativePkg`. Keep `nativeBundledNamespaceAlias` and the free `registerNativeImport(scope ...)` in `translate_ir.go` for now (still used by legacy code; a ctx variant is added in Task 1.4).

`jshelpers.go` imports needed: `fmt`(no), `strings`(no for these), `git.duckfam.us/jonathan/sngl/codegen`, `git.duckfam.us/jonathan/sngl/ir`. Add only the imports the moved funcs actually use (`codegen` for `SplitScheme`/`NativeAlias` references inside `isBundledImport`/`isBundledNativePkg`; `ir` for `*ir.Type`/`*ir.Package`/`*ir.Component`/`*ir.Import`).

- [ ] **Step 2: Verify it compiles**

```bash
go build ./codegen/lang/javascript/...
```
Expected: success (pure relocation, no behavior change).

- [ ] **Step 3: Run JS + html tests**

```bash
go test ./codegen/lang/javascript/... ./codegen/platform/html/...
```
Expected: PASS (including TestParityGolden).

- [ ] **Step 4: Commit**

```bash
git add codegen/lang/javascript/jshelpers.go codegen/lang/javascript/translate_ir.go
git commit -m "refactor(javascript): relocate surviving helpers to jshelpers.go"
```

### Task 1.2: Integer division → Math.trunc in JsIRContext.Binary

**Files:**
- Modify: `codegen/lang/javascript/ircontext.go:46-48` (`Binary`)
- Test: `codegen/lang/javascript/ircontext_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestJsBinary_IntDivisionTruncates(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	expr := &ir.Binary{
		Op:    ast.BinDiv,
		Left:  &ir.Literal{Type: ir.TypInt, Raw: "7"},
		Right: &ir.Literal{Type: ir.TypInt, Raw: "2"},
	}
	got := jc.EvalExpr(expr)
	if got != "Math.trunc(7 / 2)" {
		t.Errorf("got %q, want Math.trunc(7 / 2)", got)
	}
}
```
Add `"git.duckfam.us/jonathan/sngl/ast"` to the test imports if not present. Confirm the int-typed literal constructor matches the codebase (`ir.TypInt` per existing tests; adjust if the canonical name differs — check `ir` package).

- [ ] **Step 2: Run to verify it fails**

```bash
go test ./codegen/lang/javascript/ -run TestJsBinary_IntDivisionTruncates -v
```
Expected: FAIL — got `(7 / 2)`.

- [ ] **Step 3: Implement**

Replace `JsIRContext.Binary`:
```go
func (jc *JsIRContext) Binary(n *ir.Binary, left, right string) string {
	if n.Op == ast.BinDiv && isIntIR(n.Left) && isIntIR(n.Right) {
		return "Math.trunc(" + left + " / " + right + ")"
	}
	return "(" + left + " " + binaryOpStr(n.Op) + " " + right + ")"
}
```
Ensure `ast` is imported in `ircontext.go` (it already imports `ast`).

- [ ] **Step 4: Verify pass + golden + suite**

```bash
go test ./codegen/lang/javascript/ -run TestJsBinary_IntDivisionTruncates -v
go test ./codegen/platform/html/ -run TestParityGolden
```
Expected: both PASS.

- [ ] **Step 5: Commit**

```bash
git add codegen/lang/javascript/ircontext.go codegen/lang/javascript/ircontext_test.go
git commit -m "feat(javascript): int/int division truncates in JsIRContext.Binary"
```

### Task 1.3: IsElementRef ident → querySelector

**Files:**
- Modify: `codegen/lang/javascript/ircontext.go` `evalIdent` (after the `Member` check, before `Synthesized`)
- Test: `codegen/lang/javascript/ircontext_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestJsEvalIdent_ElementRef(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	got := jc.EvalExpr(&ir.Ident{Name: "myInput", IsElementRef: true})
	want := `document.querySelector('[data-sngl-id="myInput"]')`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
```
Verify the exact legacy format string at `translate_ir.go:192` (`fmt.Sprintf("document.querySelector('[data-sngl-id=%q]')", name)` — `%q` wraps in double quotes, so the expected string is as written above). Match it exactly.

- [ ] **Step 2: Run to verify it fails**

```bash
go test ./codegen/lang/javascript/ -run TestJsEvalIdent_ElementRef -v
```
Expected: FAIL.

- [ ] **Step 3: Implement**

In `evalIdent`, immediately after the `if n.Member != ""` block and before the `if n.Synthesized` block, add:
```go
	if n.IsElementRef {
		return fmt.Sprintf("document.querySelector('[data-sngl-id=%q]')", n.Name)
	}
```

- [ ] **Step 4: Verify pass + golden**

```bash
go test ./codegen/lang/javascript/ -run TestJsEvalIdent_ElementRef -v
go test ./codegen/platform/html/ -run TestParityGolden
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add codegen/lang/javascript/ircontext.go codegen/lang/javascript/ircontext_test.go
git commit -m "feat(javascript): element-ref idents resolve to querySelector in JsIRContext"
```

### Task 1.4: Native bundled-namespace alias + i18n PluralKey const in Select

**Files:**
- Modify: `codegen/lang/javascript/ircontext.go` `Select` (and add `nativeBundledNamespaceAliasCtx` to `jshelpers.go`)
- Test: `codegen/lang/javascript/ircontext_test.go`

- [ ] **Step 1: Add a ctx-based namespace-alias helper**

In `jshelpers.go`:
```go
// nativeBundledNamespaceAliasCtx resolves a namespace alias name to the
// esbuild-compatible JS identifier when it maps to a bundled js:// native
// import. ExprCtx analogue of nativeBundledNamespaceAlias. Returns ("","")
// when not applicable.
func nativeBundledNamespaceAliasCtx(ctx *codegen.ExprCtx, nsName string) (jsAlias, importPath string) {
	if ctx == nil || ctx.Pkg == nil {
		return "", ""
	}
	for _, imp := range ctx.Pkg.Imports {
		if imp == nil || imp.Alias != nsName || imp.Native == nil {
			continue
		}
		if !isBundledImport(imp) {
			continue
		}
		path := imp.Native.ImportPath
		return codegen.NativeAlias(path), path
	}
	return "", ""
}
```

- [ ] **Step 2: Write the failing test (PluralKey const — the simplest deterministic case)**

```go
func TestJsSelect_I18nPluralKeyConst(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	expr := &ir.Select{
		Operand: &ir.Ident{Name: "i18n"},
		Field:   "other",
	}
	got := jc.EvalExpr(expr)
	if got != `"other"` {
		t.Errorf("got %q, want \"other\"", got)
	}
}
```

- [ ] **Step 3: Run to verify it fails**

```bash
go test ./codegen/lang/javascript/ -run TestJsSelect_I18nPluralKeyConst -v
```
Expected: FAIL — got `i18n.other`.

- [ ] **Step 4: Implement Select**

The current `Select` receives the already-translated `operand` string, but the namespace-alias and PluralKey logic needs the raw operand node. Change `Select` to inspect `n.Operand`:
```go
func (jc *JsIRContext) Select(n *ir.Select, operand string) string {
	// Predeclared i18n.PluralKey constants lower to JS string literals.
	if ident, ok := n.Operand.(*ir.Ident); ok && ident.Name == "i18n" {
		if s := jsI18nConstString("i18n." + n.Field); s != "" {
			return s
		}
	}
	// Native bundled namespace (js://): emit the esbuild alias and register
	// the module so the platform emits the `import * as` prelude.
	if ident, ok := n.Operand.(*ir.Ident); ok {
		if _, ok := ident.Sym.(*ir.Namespace); ok {
			if jsAlias, importPath := nativeBundledNamespaceAliasCtx(jc.Ctx, ident.Name); jsAlias != "" {
				jc.registerNativeImport(importPath, n.Field)
				return jsAlias + "." + n.Field
			}
		}
	}
	return operand + "." + n.Field
}
```
Note: `jsI18nConstString` is already defined in `ircontext.go`. `jc.registerNativeImport` is the existing method (ircontext.go:426). `irwalk` still evaluates `operand` eagerly; that is harmless (the namespace ident path returns before using it, and for a namespace ident `operand` is unused).

- [ ] **Step 5: Verify pass + golden + full suite (native namespace exercised by suite)**

```bash
go test ./codegen/lang/javascript/ -run TestJsSelect_I18nPluralKeyConst -v
go test ./codegen/platform/html/... ./codegen/lang/javascript/...
```
Expected: PASS. The native-namespace branch is covered by existing js:// import tests; confirm they stay green.

- [ ] **Step 6: Commit**

```bash
git add codegen/lang/javascript/ircontext.go codegen/lang/javascript/jshelpers.go codegen/lang/javascript/ircontext_test.go
git commit -m "feat(javascript): PluralKey const + bundled-namespace alias in JsIRContext.Select"
```

### Task 1.5: regex builtin → new RegExp

**Files:**
- Modify: `codegen/lang/javascript/ircontext.go` `evalCall` (the `fname` switch around line 344)
- Test: `codegen/lang/javascript/ircontext_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestJsCall_RegexBuiltin(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	call := &ir.Call{
		Func: &ir.Func{Name: "regex"},
		Args: []ir.CallArg{{Value: &ir.Literal{Type: ir.TypString, Raw: "ab+c"}}},
	}
	got := jc.EvalExpr(call)
	if got != `new RegExp("ab+c")` {
		t.Errorf("got %q", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
go test ./codegen/lang/javascript/ -run TestJsCall_RegexBuiltin -v
```
Expected: FAIL — got `regex("ab+c")`.

- [ ] **Step 3: Implement**

In `evalCall`, add a `case "regex":` to the `switch fname` block (alongside `string`/`int`/`float`):
```go
		case "regex":
			if len(args) == 1 {
				return "new RegExp(" + args[0] + ")"
			}
```

- [ ] **Step 4: Verify pass + golden**

```bash
go test ./codegen/lang/javascript/ -run TestJsCall_RegexBuiltin -v
go test ./codegen/platform/html/ -run TestParityGolden
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add codegen/lang/javascript/ircontext.go codegen/lang/javascript/ircontext_test.go
git commit -m "feat(javascript): regex builtin -> new RegExp in JsIRContext"
```

### Task 1.6: bool conversion → Boolean(...)

**Files:**
- Modify: `codegen/lang/javascript/ircontext.go` `evalConversion` (switch around line 519)
- Test: `codegen/lang/javascript/ircontext_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestJsConversion_Bool(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	conv := &ir.Conversion{
		Type:    &ir.Type{Kind: ir.TypeBool},
		Operand: &ir.Ident{Name: "x", Synthesized: true},
	}
	got := jc.EvalExpr(conv)
	if got != "Boolean(x)" {
		t.Errorf("got %q, want Boolean(x)", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
go test ./codegen/lang/javascript/ -run TestJsConversion_Bool -v
```
Expected: FAIL — got `x` (bool falls through to the default `return operand`).

- [ ] **Step 3: Implement**

In `evalConversion`'s `switch n.Type.Kind`, add:
```go
		case ir.TypeBool:
			return "Boolean(" + operand + ")"
```

- [ ] **Step 4: Verify pass + golden**

```bash
go test ./codegen/lang/javascript/ -run TestJsConversion_Bool -v
go test ./codegen/platform/html/ -run TestParityGolden
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add codegen/lang/javascript/ircontext.go codegen/lang/javascript/ircontext_test.go
git commit -m "feat(javascript): bool conversion -> Boolean() in JsIRContext"
```

### Task 1.7: PluralKey map literal → plain object

**Files:**
- Modify: `codegen/lang/javascript/ircontext.go` `MapLit` (line 70)
- Test: `codegen/lang/javascript/ircontext_test.go`

- [ ] **Step 1: Write the failing test**

Port the assertion shape from `translate_ir_test.go:163 TestPluralKeyMapLitLowersToPlainObject`. Read that test for the exact `*ir.MapLitIR` construction (type with PluralKey key, string-literal keys/values), then assert the new path produces the same `{["one"]: ..., ["other"]: ...}` plain-object output rather than `new Map([...])`.

```go
func TestJsMapLit_PluralKeyPlainObject(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	m := /* construct *ir.MapLitIR with isPluralKeyMapType(m.Type)==true,
	        copied from translate_ir_test.go's fixture */
	got := jc.EvalExpr(m)
	if strings.Contains(got, "new Map") {
		t.Errorf("PluralKey map must lower to plain object, got %q", got)
	}
	if !strings.HasPrefix(got, "{[") {
		t.Errorf("expected plain-object form, got %q", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
go test ./codegen/lang/javascript/ -run TestJsMapLit_PluralKeyPlainObject -v
```
Expected: FAIL — got `new Map([...])`.

- [ ] **Step 3: Implement**

The irwalk `MapLit(n, keys, vals)` receives pre-translated keys/vals. Add the PluralKey branch at the top:
```go
func (jc *JsIRContext) MapLit(n *ir.MapLitIR, keys, vals []string) string {
	if isPluralKeyMapType(n.Type) {
		var b strings.Builder
		b.WriteString("{")
		for i := range keys {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString("[")
			b.WriteString(keys[i])
			b.WriteString("]: ")
			b.WriteString(vals[i])
		}
		b.WriteString("}")
		return b.String()
	}
	// ... existing new Map([...]) body unchanged ...
}
```
Confirm the irwalk `Renderer.MapLit` signature passes `n *ir.MapLitIR` (it does — ircontext.go:70). `isPluralKeyMapType` now lives in `jshelpers.go`.

- [ ] **Step 4: Verify pass + golden**

```bash
go test ./codegen/lang/javascript/ -run TestJsMapLit_PluralKeyPlainObject -v
go test ./codegen/platform/html/ -run TestParityGolden
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add codegen/lang/javascript/ircontext.go codegen/lang/javascript/ircontext_test.go
git commit -m "feat(javascript): PluralKey map literal -> plain object in JsIRContext"
```

### Task 1.8: async lambda prefix

**Files:**
- Modify: `codegen/lang/javascript/ircontext.go` `evalLambda` (line 569)
- Test: `codegen/lang/javascript/ircontext_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestJsLambda_AsyncPrefix(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	lam := &ir.Lambda{Func: &ir.Func{
		IsAsync: true,
		Params:  []*ir.Param{{Name: "x"}},
		Block:   []ir.Stmt{&ir.Return{Value: &ir.Ident{Name: "x", Synthesized: true}}},
	}}
	got := jc.EvalExpr(lam)
	if !strings.HasPrefix(got, "async ") {
		t.Errorf("expected async prefix, got %q", got)
	}
}
```
Note: the legacy single-param sync fast path is `x => body` (no parens); the async path is `async (x) => body`. Match legacy exactly (`translate_ir.go:548-551`): async always uses parens.

- [ ] **Step 2: Run to verify it fails**

```bash
go test ./codegen/lang/javascript/ -run TestJsLambda_AsyncPrefix -v
```
Expected: FAIL.

- [ ] **Step 3: Implement**

Update `evalLambda` to mirror legacy:
```go
func (jc *JsIRContext) evalLambda(n *ir.Lambda) string {
	if n.Func == nil {
		return "() => null"
	}
	params := make([]string, len(n.Func.Params))
	for i, p := range n.Func.Params {
		params[i] = p.Name
	}
	asyncPrefix := ""
	if n.Func.IsAsync {
		asyncPrefix = "async "
	}
	bodyJC := jc
	for _, p := range n.Func.Params {
		bodyJC = bodyJC.WithLocal(p.Name)
	}
	if len(n.Func.Block) == 1 {
		if ret, ok := n.Func.Block[0].(*ir.Return); ok && ret.Value != nil {
			body := bodyJC.EvalExpr(ret.Value)
			if asyncPrefix == "" && len(params) == 1 {
				return params[0] + " => " + body
			}
			return asyncPrefix + "(" + strings.Join(params, ", ") + ") => " + body
		}
	}
	var b strings.Builder
	b.WriteString(asyncPrefix + "(" + strings.Join(params, ", ") + ") => {\n")
	for _, stmt := range n.Func.Block {
		for _, line := range bodyJC.EvalStmt(stmt) {
			b.WriteString("  " + line + ";\n")
		}
	}
	b.WriteString("}")
	return b.String()
}
```
This also fixes a latent bug: the current `evalLambda` doesn't bind params as locals (`bodyJC`). Adding `WithLocal` per param matches the legacy `subScope` behavior. Verify the golden harness still passes (no change expected for sync single-expr lambdas, which dominate the corpus).

- [ ] **Step 4: Verify pass + golden + suite**

```bash
go test ./codegen/lang/javascript/ -run TestJsLambda_AsyncPrefix -v
go test ./codegen/platform/html/... ./codegen/lang/javascript/...
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add codegen/lang/javascript/ircontext.go codegen/lang/javascript/ircontext_test.go
git commit -m "feat(javascript): async lambda prefix + param-local binding in JsIRContext"
```

### Task 1.9: richer literal type coverage

**Files:**
- Modify: `codegen/lang/javascript/ircontext.go` `evalLiteral` (line 261)
- Test: `codegen/lang/javascript/ircontext_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestJsLiteral_QuotedScalarTypes(t *testing.T) {
	ctx := codegen.NewExprCtx(&ir.Package{})
	jc := NewIRContext(ctx)
	for _, k := range []ir.TypeKind{ir.TypeURL, ir.TypeEmail, ir.TypeUUID, ir.TypeRegex, ir.TypeBase64, ir.TypeIPV4, ir.TypeIPV6, ir.TypeHostname, ir.TypeDecimal} {
		lit := &ir.Literal{Type: &ir.Type{Kind: k}, Raw: "val"}
		got := jc.evalLiteral(lit)
		if got != `"val"` {
			t.Errorf("kind %v: got %q, want \"val\"", k, got)
		}
	}
}
```
Confirm these `ir.TypeKind` constant names exist (they appear in `translate_ir.go:168-174`). Use the same set.

- [ ] **Step 2: Run to verify it fails**

```bash
go test ./codegen/lang/javascript/ -run TestJsLiteral_QuotedScalarTypes -v
```
Expected: FAIL — these kinds hit the `default` branch and return `n.Raw` unquoted.

- [ ] **Step 3: Implement**

Extend the `evalLiteral` switch's quoted-string case to include the extra kinds (match `translate_ir.go:168-174`):
```go
	case ir.TypeString, ir.TypeColor,
		ir.TypeDate, ir.TypeTime, ir.TypeDateTime, ir.TypeDuration,
		ir.TypeURL, ir.TypeEmail, ir.TypeUUID, ir.TypeRegex, ir.TypeBase64,
		ir.TypeIPV4, ir.TypeIPV6, ir.TypeHostname, ir.TypeDecimal:
		return fmt.Sprintf("%q", n.Raw)
```
Preserve the existing unit-suffix handling. Cross-check ordering against `translateIRLiteral` (suffix first, then type switch) so output matches byte-for-byte.

- [ ] **Step 4: Verify pass + golden**

```bash
go test ./codegen/lang/javascript/ -run TestJsLiteral_QuotedScalarTypes -v
go test ./codegen/platform/html/ -run TestParityGolden
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add codegen/lang/javascript/ircontext.go codegen/lang/javascript/ircontext_test.go
git commit -m "feat(javascript): full quoted-scalar literal coverage in JsIRContext"
```

### Task 1.10: namespace/type-method user-function dispatch (CreateComponent + dispatch reconciliation)

This is the highest-risk task. The legacy path resolves user funcs in namespace/type-method calls via `scope.FuncNames` and handles `lower.CreateComponent`. The new path scans `Ctx.Pkg.Funcs` and lacks `CreateComponent`. Target: `Pkg.Funcs` scan + `CreateComponent`, byte-identical output.

**Files:**
- Modify: `codegen/lang/javascript/ircontext.go` `evalNamespaceCall` (line 437)
- Test: `codegen/lang/javascript/ircontext_test.go`

- [ ] **Step 1: Write the failing test for CreateComponent**

```go
func TestJsNamespaceCall_CreateComponent(t *testing.T) {
	comp := &ir.Component{Name: "Card"}
	pkg := &ir.Package{Components: []*ir.Component{comp}}
	ctx := codegen.NewExprCtx(pkg)
	jc := NewIRContext(ctx)
	call := &ir.Call{
		Receiver: &ir.Ident{Name: "lower"}, // matches how CreateComponent calls are shaped
		Func:     &ir.Func{Name: "CreateComponent", Receiver: "lower"},
		Args: []ir.CallArg{
			{Value: &ir.Ident{Name: "Card", Sym: comp}},
			{Value: &ir.StructLit{}},
		},
	}
	got := jc.EvalExpr(call)
	want := factoryName(comp) + "({})"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
```
Before writing, read `translate_ir.go:404-424` to confirm the exact `*ir.Call` shape the checker/lowering produces for `CreateComponent` (receiver presence, `Func.Receiver` value, arg order). Adjust the test construction to match real IR. If `CreateComponent` arrives with `n.Receiver == nil`, place the dispatch in `evalCall` before the `n.Receiver != nil` branch instead.

- [ ] **Step 2: Run to verify it fails**

```bash
go test ./codegen/lang/javascript/ -run TestJsNamespaceCall_CreateComponent -v
```
Expected: FAIL.

- [ ] **Step 3: Implement CreateComponent + reconcile dispatch**

In `evalNamespaceCall`, after computing `receiver`/`args` and before the builtin dispatch, add the `CreateComponent` special case (mirror `translate_ir.go:409-424`):
```go
	if n.Func != nil && n.Func.Name == "CreateComponent" {
		if len(n.Args) != 2 {
			return "/* CreateComponent: wrong arity */"
		}
		compIdent, ok := n.Args[0].Value.(*ir.Ident)
		if !ok {
			return "/* CreateComponent: arg[0] not an Ident */"
		}
		comp, ok := compIdent.Sym.(*ir.Component)
		if !ok {
			return "/* CreateComponent: arg[0].Sym not a Component */"
		}
		return factoryName(comp) + "(" + jc.EvalExpr(n.Args[1].Value) + ")"
	}
```
The user-function dispatch already scans `jc.Ctx.Pkg.Funcs` (see `evalTypeMethodCall` ircontext.go:497-503). Confirm `evalNamespaceCall` performs the equivalent for namespace-qualified user funcs; if it currently only falls through to `receiver + "." + fname`, add a `Pkg.Funcs` scan producing `strings.ReplaceAll(qualName, ".", "_") + "(" + allArgs + ")"` to match legacy `translate_ir.go:451-453`. Write a second characterization test asserting a user `ns.fn(x)` call emits `ns_fn(...)` exactly as legacy does, and make it pass.

- [ ] **Step 4: Verify pass + golden + full suite**

```bash
go test ./codegen/lang/javascript/ -run TestJsNamespaceCall -v
go tool verify
```
Expected: PASS. `go tool verify` is the broad net for any dispatch divergence; if any test changes output, this is where it surfaces — investigate before proceeding.

- [ ] **Step 5: Commit**

```bash
git add codegen/lang/javascript/ircontext.go codegen/lang/javascript/ircontext_test.go
git commit -m "feat(javascript): CreateComponent + user-func dispatch in JsIRContext namespace calls"
```

### Task 1.11: Port remaining translate_ir_test.go cases to the new path

Ensures no legacy-asserted behavior is lost when the file is deleted in Phase 4.

**Files:**
- Modify: `codegen/lang/javascript/ircontext_test.go`
- Reference: `codegen/lang/javascript/translate_ir_test.go`

- [ ] **Step 1: Inventory legacy tests not yet covered**

Legacy test funcs: `TestTranslateIRPlainCall_AwaitsAsyncSNGLCallee`, `..._NoAwaitForSyncCallee`, `TestTranslateIRNamespaceCall_AwaitsAsyncSNGLCallee`, `..._NoAwaitForSyncCallee`, `TestTranslateIRCall_FuncvarAsyncSlot_Awaits`, `..._FuncvarSyncSlot_NoAwait`, `..._FuncvarParamSlot_AnyAsync_Awaits`, `TestEmitI18nTr`, `TestEmitI18nPlural`, `TestEmitI18nExactly`, `TestPluralKeyMapLitLowersToPlainObject` (done 1.7), `TestI18nPluralKeyConstantsLowerToStringLiterals` (done 1.4). For each not already covered by an `ircontext_test.go` case, write the equivalent against `NewIRContext` + `EvalExpr`/`EvalStmt`.

- [ ] **Step 2: Write the ported tests**

For each, construct the same IR the legacy test uses, call the `JsIRContext` equivalent, assert the same expected string. (await-on-async-callee, await-on-async-funcvar-slot, i18n tr/plural/exactly emission.)

- [ ] **Step 3: Run to verify they pass**

```bash
go test ./codegen/lang/javascript/ -run 'TestJs' -v
```
Expected: PASS (these behaviors already exist on the new path; this is coverage parity, so they should pass on first run — if any fails, that's a real gap; fix it).

- [ ] **Step 4: Commit**

```bash
git add codegen/lang/javascript/ircontext_test.go
git commit -m "test(javascript): port legacy translate_ir behaviors to JsIRContext tests"
```

---

## Phase 2: Helper consolidation

### Task 2.1: Wire ExprCtx.Helpers to common.Helpers; switch the read site

**Files:**
- Modify: `codegen/platform/html/html.go:609-614` (construction) and `:1992` (read)
- Test: existing String-helper behavior via the suite + a targeted html test

- [ ] **Step 1: Write a failing test asserting the String helper emits via the new path**

In a new `codegen/platform/html/string_helper_test.go`, compile a fixture that forces a `string(x)` conversion in a handler body (which goes through the new `JsIRContext` path today) and assert the generated html contains `function String(v)`. If an existing test already covers this, extend it instead.

```go
func TestStringHelperEmittedFromNewPath(t *testing.T) {
	// fixture source with a click handler doing e.g. label.text = string(count)
	out := generateHTMLFromSample(t, /* sample with string() in a handler */)
	if !strings.Contains(out, "function String(v)") {
		t.Errorf("String helper not emitted; new path flagged Ctx.Helpers but read site checks scope.NeededHelpers")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
go test ./codegen/platform/html/ -run TestStringHelperEmittedFromNewPath -v
```
Expected: FAIL — the new path writes `g.ctx.Helpers["String"]` but html reads `g.scope.NeededHelpers["String"]` (the maps are not the same).

- [ ] **Step 3: Implement**

In `newHTMLGen` (around html.go:609), wire the ctx helper map to the shared `common.Helpers`:
```go
	g.ctx = codegen.NewExprCtx(pkg)
	g.ctx.Helpers = common.Helpers
```
And change the read at html.go:1992:
```go
	if g.ctx.Helpers["String"] {
```
Confirm `common.Helpers` is the same map assigned to `g.scope.NeededHelpers` at line 598 (it is) — so both paths now flag the same map and the read sees both.

- [ ] **Step 4: Verify pass + golden + suite**

```bash
go test ./codegen/platform/html/ -run 'TestStringHelper|TestParityGolden' -v
go test ./codegen/platform/html/...
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/html/html.go codegen/platform/html/string_helper_test.go
git commit -m "fix(html): read String helper from ctx.Helpers so new-path flagging counts"
```

---

## Phase 3: Route html's three legacy sites onto the unified pipeline

### Task 3.1: Route exprToJS reactive branch through scopedJC

**Files:**
- Modify: `codegen/platform/html/html.go:3128-3136` (`exprToJS`)

- [ ] **Step 1: Implement (covered by TestParityGolden + suite, no new unit test needed — but confirm a golden fixture exercises a reactive init expression)**

Change the reactive branch:
```go
func (g *htmlGen) exprToJS(expr ir.Expr) string {
	if codegen.IRIsReactive(expr) {
		return g.scopedJC().EvalExpr(expr)
	}
	if codegen.IRIsLiteral(expr) {
		return g.lang.TranslateIRLiteral(expr)
	}
	return `""`
}
```
(`TranslateIRLiteral` stays on the interface — not part of this migration.)

- [ ] **Step 2: Run golden + full suite**

```bash
go test ./codegen/platform/html/ -run TestParityGolden -v
go tool verify
```
Expected: PASS, zero golden diff. If a diff appears, the new path is missing a behavior the legacy path had for that expression shape — fix the gap in `JsIRContext` (Phase 1 style) before continuing; do NOT regenerate goldens.

- [ ] **Step 3: Commit**

```bash
git add codegen/platform/html/html.go
git commit -m "refactor(html): route exprToJS reactive branch through JsIRContext"
```

### Task 3.2: Route emitJSFunc bodies through the unified pipeline

**Files:**
- Modify: `codegen/platform/html/html.go:3046-3126` (`emitJSFunc`)

- [ ] **Step 1: Implement, preserving signature emission and the single-return one-liner**

Keep the signature logic (params, `this`→`state`, `Receiver_Name` mangling, `async function` keyword). Replace the body translation:
```go
	// Single-return expression body — preserve the one-line form for output parity.
	if len(fn.Block) == 1 {
		if ret, ok := fn.Block[0].(*ir.Return); ok && ret.Value != nil {
			jc := g.scopedJC()
			for _, p := range fn.Params {
				jc = jc.WithLocal(p.Name)
			}
			body := jc.EvalExpr(ret.Value)
			fmt.Fprintf(b, "%s %s(%s) { return %s; }\n", keyword, jsName, paramStr, body)
			return
		}
	}
	if len(fn.Block) == 0 {
		return
	}
	fmt.Fprintf(b, "%s %s(%s) {\n", keyword, jsName, paramStr)
	// Bind params as locals, then route the whole body through the unified
	// pipeline (WalkLowered + htmlTranslator + JsIRContext) so element-ref
	// writes and intrinsic statements lower identically to handler bodies.
	jc := g.scopedJC()
	for _, p := range fn.Params {
		jc = jc.WithLocal(p.Name)
	}
	tr := newHTMLTranslatorWithNodes(jc, g.idToNode)
	lowered := codegen.WalkLowered(context.Background(), fn.Block, tr)
	for _, s := range lowered {
		g.collectLoweredRefs(s)
		for _, line := range jc.EvalStmt(s) {
			fmt.Fprintf(b, "  %s;\n", line)
		}
	}
	b.WriteString("}\n")
```
Delete the old `funcScope := &codegen.ExprScope{...}` block and the per-statement `switch`. Note `translateBlockJC` is the same shape but uses a fixed indent/`;` join; `emitJSFunc` needs `  ` indent and its own brace handling — inline the loop as above rather than calling `translateBlockJC`, OR extend `translateBlockJC` to accept an indent. Prefer inlining to keep `emitJSFunc`'s exact whitespace. Verify against goldens.

Watch the param-local subtlety: `scopedJC()` copies `g.scope.LocalVars` into ctx locals; once `g.scope` is removed (Phase 4) it must copy from the consts seed instead. For now `scopedJC` still reads `g.scope`; the param `WithLocal` calls above handle function params.

- [ ] **Step 2: Run golden + full suite**

```bash
go test ./codegen/platform/html/ -run TestParityGolden -v
go tool verify
```
Expected: PASS, zero golden diff. Investigate any diff (likely whitespace/one-liner formatting) and fix the emission to match, not the golden.

- [ ] **Step 3: Commit**

```bash
git add codegen/platform/html/html.go
git commit -m "refactor(html): route emitJSFunc bodies through unified JsIRContext pipeline"
```

### Task 3.3: Delete translateHandlerStmt; route its callers through translateBlockJC

**Files:**
- Modify: `codegen/platform/html/html.go` (`translateHandlerStmt` ~2725-2749 and any caller not already removed)

- [ ] **Step 1: Find remaining callers**

```bash
grep -n "translateHandlerStmt" codegen/platform/html/*.go
```
After Task 3.2, `emitJSFunc` no longer calls it. Identify any other caller (the `domWriteFor` Assign case was its reason for existing; that path is now handled by `htmlTranslator.OnPropAssign` via `WalkLowered`).

- [ ] **Step 2: Remove translateHandlerStmt and its now-dead helpers**

Delete `translateHandlerStmt`. If `domWriteFor` is now only referenced by `htmlTranslator`, leave it. Confirm `loweredRefs`/`collectLoweredRefs` bookkeeping is still performed by the pipeline (`translateBlockJC` and the inlined `emitJSFunc` loop both call `g.collectLoweredRefs`).

- [ ] **Step 3: Verify compile + golden + suite**

```bash
go build ./...
go test ./codegen/platform/html/ -run TestParityGolden -v
go tool verify
```
Expected: PASS, zero golden diff.

- [ ] **Step 4: Commit**

```bash
git add codegen/platform/html/html.go
git commit -m "refactor(html): drop translateHandlerStmt; DOM writes lower via htmlTranslator"
```

---

## Phase 4: Delete legacy & trim interface

### Task 4.1: Remove the legacy ExprScope construction from html

**Files:**
- Modify: `codegen/platform/html/html.go:591-605` (`g.scope` construction) and `scopedJC` (2778-2788)

- [ ] **Step 1: Confirm g.scope is otherwise unused**

```bash
grep -n "g.scope\|\.scope\b" codegen/platform/html/*.go | grep -v "_test.go"
```
Expect remaining uses only in `scopedJC` (LocalVars/Renames/EventVar) and the construction. `scopedJC` must source those from `g.ctx` instead.

- [ ] **Step 2: Make scopedJC self-contained on g.ctx**

`g.ctx` already carries `Locals`, `Renames`, `EventVar` (via `ForComponent`/`WithEvent` usage). Seed consts into `g.ctx.Locals` at construction (the old code seeded them into `g.scope.LocalVars`):
```go
	g.ctx = codegen.NewExprCtx(pkg)
	g.ctx.Helpers = common.Helpers
	if g.scope.NativeImports == nil { ... }   // keep native-imports plumbing on g.ctx
	if pkg != nil {
		for _, c := range pkg.Consts {
			g.ctx.Locals[c.Name] = true
		}
	}
```
Then simplify `scopedJC`:
```go
func (g *htmlGen) scopedJC() *javascript.JsIRContext {
	jc := javascript.NewIRContext(g.ctx.Clone())
	jc.EventVar = g.ctx.EventVar
	return jc
}
```
Audit any code that wrote `g.scope.Renames` / `g.scope.LocalVars` / `g.scope.EventVar` mid-emission (e.g. component-inlining renames, loop vars, event binding) and redirect those writes to `g.ctx`. Search:
```bash
grep -n "scope.Renames\|scope.LocalVars\|scope.EventVar\|dataRenames" codegen/platform/html/*.go | grep -v "_test.go"
```
Redirect each to the `g.ctx` equivalent. This is the most delicate edit — do it incrementally, running `TestParityGolden` after each redirect.

- [ ] **Step 3: Remove the g.scope field and construction**

Delete the `g.scope = &codegen.ExprScope{...}` block and the `scope` field from the `htmlGen` struct. Update `newMinimalHTMLGen` in `emit_jsfunc_test.go` to drop the `scope:` field.

- [ ] **Step 4: Verify compile + golden + suite**

```bash
go build ./...
go test ./codegen/platform/html/ -run TestParityGolden -v
go tool verify
```
Expected: PASS, zero golden diff.

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/html/
git commit -m "refactor(html): drop ExprScope; JsIRContext sources state from ExprCtx"
```

### Task 4.2: Delete translate_ir.go and its test

**Files:**
- Delete: `codegen/lang/javascript/translate_ir.go`
- Delete: `codegen/lang/javascript/translate_ir_test.go`
- Modify: `codegen/lang/javascript/javascript.go` (remove `TranslateIRExpr`/`TranslateIRMutation` methods)

- [ ] **Step 1: Confirm no remaining references**

```bash
grep -rn "translateIRExpr\|translateIRMutation\|translateIRCall\|translateIRForJS\|translateErrorAwareCall\|translateIRMutTarget\|nativeBundledNamespaceAlias\b\|registerNativeImport(scope" codegen/lang/javascript/ codegen/platform/ | grep -v "_test.go"
```
Expect: no non-test references except inside `translate_ir.go` itself. The free `registerNativeImport(scope, ...)` and `nativeBundledNamespaceAlias` (scope-based) are legacy-only now (the ctx variants live in `jshelpers.go`/`ircontext.go`).

- [ ] **Step 2: Delete the files and the interface-implementing methods**

```bash
git rm codegen/lang/javascript/translate_ir.go codegen/lang/javascript/translate_ir_test.go
```
In `javascript.go`, remove the `TranslateIRExpr` and `TranslateIRMutation` methods (lines ~31-39). Keep `TranslateIRLiteral`.

- [ ] **Step 3: Verify compile**

```bash
go build ./codegen/lang/javascript/...
```
Expected: success. If the compiler reports an undefined helper that was still in `translate_ir.go`, move it to `jshelpers.go` (it should have been moved in Task 1.1; this catches stragglers).

- [ ] **Step 4: Run JS + html tests**

```bash
go test ./codegen/lang/javascript/... ./codegen/platform/html/...
```
Expected: PASS. (`javascript.Translator` no longer satisfies the full old interface — that is fixed in Task 4.3; if `go build ./...` fails here because the interface still lists the methods, proceed to 4.3 and treat 4.2+4.3 as one commit.)

- [ ] **Step 5: Commit**

```bash
git add -A codegen/lang/javascript/
git commit -m "refactor(javascript): delete legacy translate_ir.go path"
```

### Task 4.3: Remove the two methods from the LangTranslator interface

**Files:**
- Modify: `codegen/codegen.go:103-104` (interface)

- [ ] **Step 1: Remove the interface methods**

Delete these two lines from the `LangTranslator` interface:
```go
	TranslateIRExpr(e ir.Expr, scope *ExprScope) string
	TranslateIRMutation(s ir.Stmt, scope *ExprScope) []string
```
Keep `TranslateIRLiteral`. Leave `ExprScope` and `NeededHelpers` defined in `codegen.go` (Go/Kotlin still use them via their own concrete methods).

- [ ] **Step 2: Verify the whole tree compiles**

```bash
go build ./...
```
Expected: success. Go (`golang.Translator`) and Kotlin (`kotlin.Translator`) keep their `TranslateIRExpr`/`TranslateIRMutation` as concrete methods — `golang/http.go` calls `t.TranslateIRMutation` directly, which still resolves. `none.Translator` keeps or drops its stubs (they become harmless dead methods; remove them for tidiness if desired).

- [ ] **Step 3: Optionally remove now-dead none stubs**

If `none.Translator.TranslateIRExpr`/`TranslateIRMutation` (none.go:29-30) are no longer required by any interface, delete them.

```bash
go build ./... && go test ./codegen/lang/none/...
```
Expected: success.

- [ ] **Step 4: Full suite**

```bash
go tool verify
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add codegen/codegen.go codegen/lang/none/
git commit -m "refactor(codegen): drop TranslateIRExpr/Mutation from LangTranslator interface"
```

### Task 4.4: Final verification

- [ ] **Step 1: Full clean build + suite + golden**

```bash
go build ./...
go tool verify
go test ./codegen/platform/html/ -run TestParityGolden -v
```
Expected: all PASS, zero golden diff across the whole project.

- [ ] **Step 2: Confirm dead-code is gone**

```bash
grep -rn "ExprScope" codegen/platform/html/ | grep -v "_test.go"
```
Expected: no matches (html is fully off `ExprScope`).

- [ ] **Step 3: Update the spec status**

In `docs/superpowers/specs/2026-06-03-js-translator-unification-design.md`, change `**Status:** Design approved, plan pending` to `**Status:** Implemented`.

```bash
git add docs/superpowers/specs/2026-06-03-js-translator-unification-design.md
git commit -m "docs(spec): mark JS translator unification implemented"
```

---

## Self-review notes (for the executor)

- **Parity bar:** `TestParityGolden` must stay green after every task. A diff means a real behavioral change — fix the emission to match the baseline; never regenerate goldens to paper over a diff unless the change is intended and justified (none are expected in this plan).
- **IR constructor names:** several tests reference `ir.TypInt`/`ir.TypString`/`ir.TypeBool`/`ir.TypeKind` constants and `*ir.Type{Kind:...}`. Confirm the exact names against the `ir` package before writing each test (existing `ircontext_test.go` and `translate_ir_test.go` are the source of truth for the canonical forms).
- **Highest risk:** Task 1.10 (dispatch reconciliation) and Task 4.1 (redirecting mid-emission `scope.Renames`/`LocalVars`/`EventVar` writes to `g.ctx`). Run `go tool verify` — not just the html package — after both.
- **Go/Kotlin untouched:** if any Go/Kotlin test breaks, something removed shared state it shouldn't have. The only shared surface intentionally changed is the `LangTranslator` interface (methods removed, not the `ExprScope` type).
