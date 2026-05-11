# Fyne Test Runner — Phase 2a Vertical Slice Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** End-to-end pipeline that runs one trivial SNGL test on `fyne`: compile a single-component module via `fyne.Generate`, append a Go `*_testing.go` file with the SNGL test body lowered to Go, shell `go test -json`, parse the result back into `*codegen.TestResult`.

**Architecture:** New `fyne.RunTests` shells a temp Go module per component. SNGL test bodies are lowered to Go via a new `codegen/lang/golang/testlower` package that translates `Test.assert/must/wait` method calls and ordinary statements into Go using the existing `translateIRExpr` and `translateIRMutation`. Probe checks that a minimal fyne program builds.

**Tech Stack:** Go 1.23+, `fyne.io/fyne/v2/test`, existing `codegen/platform/fyne` codegen, `go/format` for output prettifying, `encoding/json` for `go test -json` parse.

**Scope (deliberately narrow):** This plan delivers the *pipeline* via a single fixture that only uses `t.assert` on a counter component. Out-of-scope (future plans):
- UI event invocation (`c.btn.@click()`)
- `t.wait` lowering (the SNGL stub exists; fyne lowering deferred)
- `t.must`, `t.tick`, `t.test` sub-tests, `t.setLocale`
- Multi-component test files
- Non-counter test patterns (lists, computed, timers)

---

## File Structure

**Create:**
- `codegen/lang/golang/testlower/testlower.go` — `LowerTestFunc(fn *ir.Func, scope *codegen.ExprScope) string` returns a `func TestXxx(t *testing.T) { ... }` Go source string.
- `codegen/lang/golang/testlower/testlower_test.go` — golden test of one lowered body.
- `codegen/platform/fyne/runtests.go` — `(*Generator).RunTests` and helpers. Build-tag `//go:build !js` to mirror html.
- `codegen/platform/fyne/runtests_test.go` — integration test that exercises the full pipeline against a fixture.
- `codegen/platform/fyne/runtests_js.go` — `//go:build js` stub that satisfies the interface in WASM builds.
- `testdata/test_fyne_counter.sngl` — minimal fixture (counter with `t.assert(c.count == 0)`).

**Modify:**
- `codegen/platform/fyne/fyne.go` — register `ProbeTest`.
- `cmd/sngl/test.go` — no changes; existing matrix runner picks fyne up via `RunTests` interface.

**No new packages elsewhere.**

---

## Task 1: probe + skeleton RunTests

**Files:**
- Create: `codegen/platform/fyne/runtests.go`
- Create: `codegen/platform/fyne/runtests_js.go`
- Modify: `codegen/platform/fyne/fyne.go`

- [ ] **Step 1: Read current `fyne.go` to find the right insertion spot for the probe.**

Run: `grep -n "func (g \\*Generator)" codegen/platform/fyne/fyne.go`

- [ ] **Step 2: Create `runtests.go` with skeleton + probe**

```go
//go:build !js

package fyne

import (
	"os/exec"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// RunTests is the entry point for `sngl test --platform=fyne`. It groups
// the package's test functions by their target component, generates a
// temp Go module per component, shells `go test -json`, and parses
// results back. See runtests.go inside helpers for the per-component
// pipeline.
func (g *Generator) RunTests(pkg *ir.Package, lang codegen.LangTranslator) ([]*codegen.TestResult, error) {
	if pkg == nil {
		return nil, nil
	}
	// Pipeline lives in helpers below; in this skeleton the function
	// returns nil for now so the matrix runner can call us without
	// erroring while later tasks fill in the body.
	return nil, nil
}

// ProbeTest reports whether a Go toolchain is available. We don't probe
// for fyne build deps directly — that would require running a real
// `go build`, which is too expensive for a cheap probe. The actual
// dependencies surface at RunTests time as a build failure that gets
// reported through the test results.
func (g *Generator) ProbeTest() (bool, string) {
	if _, err := exec.LookPath("go"); err != nil {
		return false, "go toolchain not on PATH"
	}
	return true, ""
}
```

- [ ] **Step 3: Create `runtests_js.go` stub for WASM**

```go
//go:build js

package fyne

import (
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func (g *Generator) RunTests(*ir.Package, codegen.LangTranslator) ([]*codegen.TestResult, error) {
	return nil, nil
}

func (g *Generator) ProbeTest() (bool, string) {
	return false, "fyne test runner unavailable in WASM build"
}
```

- [ ] **Step 4: Run the build and a smoke test**

Run: `go build ./...`
Expected: clean.

Run: `go install ./cmd/sngl && sngl test --platform=all ./...`
Expected: header `=== platform=fyne` appears alongside `html` and `none`. (Will print 0 tests since the testdata/ glob is excluded by walk.)

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/fyne/runtests.go codegen/platform/fyne/runtests_js.go
git commit -m "feat(fyne): scaffold RunTests + ProbeTest"
```

---

## Task 2: lower a trivial test func to Go

**Files:**
- Create: `codegen/lang/golang/testlower/testlower.go`
- Create: `codegen/lang/golang/testlower/testlower_test.go`

- [ ] **Step 1: Read the existing golang translator interface**

```bash
grep -n "^func translate" codegen/lang/golang/translate_ir.go
grep -n "type ExprScope" codegen/codegen.go ir/*.go
```

Confirm `translateIRExpr(e ir.Expr, scope *codegen.ExprScope) string` and `translateIRMutation(s ir.Stmt, scope *codegen.ExprScope) []string` exist and are package-level functions. They are not exported. The testlower package must either live inside `codegen/lang/golang` (so it can call them) or duplicate enough logic to lower a small subset. The cleanest path is to keep `testlower` in a sub-package and add narrow exported entry points in `codegen/lang/golang`. To keep this task small, put `testlower.go` directly inside `codegen/lang/golang` package (no sub-package).

**Decision:** put `testlower.go` in package `golang` itself. Update file paths accordingly:
- `codegen/lang/golang/testlower.go`
- `codegen/lang/golang/testlower_test.go`

- [ ] **Step 2: Write the failing test**

`codegen/lang/golang/testlower_test.go`:

```go
package golang

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

func TestLowerTestFunc_trivialAssert(t *testing.T) {
	src := `
component box {
    var count = 0
    text(value="x")
}

func testCountStartsZero(t Test, c box) {
    t.assert(c.count == 0)
}
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
	for _, d := range diags {
		if d.Severity.String() == "error" {
			t.Fatalf("check: %s", d.Message)
		}
	}
	if pkg == nil {
		t.Fatal("nil pkg")
	}
	var fn *ir.Func
	for _, f := range pkg.Funcs {
		if f.Name == "testCountStartsZero" {
			fn = f
			break
		}
	}
	if fn == nil {
		t.Fatal("test func not found in pkg.Funcs")
	}

	out := LowerTestFunc(fn, "counterTest")
	if !strings.Contains(out, "func TestcounterTest(t *testing.T)") {
		t.Errorf("missing Go test func header in:\n%s", out)
	}
	if !strings.Contains(out, "t.Errorf") {
		t.Errorf("assert should lower to t.Errorf:\n%s", out)
	}
	if !strings.Contains(out, "c.Count == 0") && !strings.Contains(out, "c.count == 0") {
		t.Errorf("assert expression body missing or mistranslated:\n%s", out)
	}
}
```

Add the missing import for `ir` at the top: `"git.duckfam.us/jonathan/sngl/ir"`.

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./codegen/lang/golang/ -run TestLowerTestFunc -v`
Expected: FAIL — `LowerTestFunc` undefined.

- [ ] **Step 4: Implement `LowerTestFunc`**

Create `codegen/lang/golang/testlower.go`:

```go
package golang

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// LowerTestFunc renders a SNGL test function as a Go *testing.T test
// function. The output is a single self-contained Go source block
// suitable for inclusion in a `_test.go` file inside the temp module
// emitted by a platform RunTests.
//
// componentVar is the Go identifier of the freshly-constructed
// component value passed to the SNGL test body's `c` parameter. For a
// counter component, the caller typically emits `c := NewCounter()`
// just before each call and passes "c".
func LowerTestFunc(fn *ir.Func, suffix string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "func Test%s(t *testing.T) {\n", suffix)
	b.WriteString("\tc := newTestComponent()\n")
	scope := &codegen.ExprScope{}
	for _, s := range fn.Body {
		for _, line := range lowerTestStmt(s, scope) {
			fmt.Fprintf(&b, "\t%s\n", line)
		}
	}
	b.WriteString("}\n")
	return b.String()
}

// lowerTestStmt translates a single IR statement from a test body into
// one or more Go source lines. Statements not yet supported produce a
// // TODO comment so the lowered file still compiles.
func lowerTestStmt(s ir.Stmt, scope *codegen.ExprScope) []string {
	if call, ok := s.(*ir.CallStmt); ok && call.Call != nil {
		if msg, ok := lowerTestAssert(call, scope); ok {
			return []string{msg}
		}
	}
	// Fallback: emit a comment so other statement kinds don't break the
	// build during the phase-2a vertical slice. Later tasks lower more
	// statement kinds (assign, event-trigger, t.wait).
	return []string{fmt.Sprintf("// unsupported test stmt: %T", s)}
}

// lowerTestAssert recognizes `t.assert(expr)` and lowers it to
// `if !(expr) { t.Errorf("assert failed: ...") }`. Returns (line, true)
// on match. Returns ("", false) for any other call shape.
func lowerTestAssert(call *ir.CallStmt, scope *codegen.ExprScope) (string, bool) {
	c := call.Call
	if c.Receiver == nil {
		return "", false
	}
	recvIdent, ok := c.Receiver.(*ir.Ident)
	if !ok || recvIdent.Name != "t" {
		return "", false
	}
	// The method name lives on the Call.Func or on a Select; reach for
	// whichever is non-nil. Reading codegen/lang/golang/translate_ir.go
	// translateIRCall shows the canonical pattern.
	method := ""
	if c.Func != nil {
		method = c.Func.Name
		// Strip "Test." namespace if present.
		method = strings.TrimPrefix(method, "Test.")
	}
	if method != "assert" {
		return "", false
	}
	if len(c.Args) != 1 {
		return "", false
	}
	exprGo := translateIRExpr(c.Args[0].Value, scope)
	return fmt.Sprintf("if !(%s) { t.Errorf(\"assert failed: %%s\", %q) }", exprGo, exprGo), true
}
```

Note: `*ir.CallStmt` may or may not be the canonical IR shape for a method-call statement in this codebase. Read `ir/stmt.go` to confirm the type name; the canonical shape is likely `*ir.CallStmt` wrapping `*ir.Call`. Adapt names if they differ.

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./codegen/lang/golang/ -run TestLowerTestFunc -v`
Expected: PASS — three substring checks satisfied.

- [ ] **Step 6: Commit**

```bash
git add codegen/lang/golang/testlower.go codegen/lang/golang/testlower_test.go
git commit -m "feat(golang): LowerTestFunc for trivial t.assert bodies"
```

---

## Task 3: write the fyne fixture and confirm `none` passes it

**Files:**
- Create: `testdata/test_fyne_counter.sngl`

- [ ] **Step 1: Write fixture**

```sngl
component counter {
    var count = 0
    text(value=string(count))
}

func testCountStartsZero(t Test, c counter) {
    t.assert(c.count == 0)
}
```

- [ ] **Step 2: Verify on `none`**

```bash
sngl test --platform=none testdata/test_fyne_counter.sngl
```

Expected: PASS — `none` interpreter already handles `t.assert`.

- [ ] **Step 3: Commit**

```bash
git add testdata/test_fyne_counter.sngl
git commit -m "test: minimal counter fixture for fyne RunTests"
```

---

## Task 4: fyne.RunTests — temp module write-out

**Files:**
- Modify: `codegen/platform/fyne/runtests.go`

- [ ] **Step 1: Understand the existing fyne Generate output**

```bash
go install ./cmd/sngl
mkdir -p /tmp/fyne-out
sngl build --platform fyne --lang go --out /tmp/fyne-out testdata/test_fyne_counter.sngl 2>&1 | tail -10
ls /tmp/fyne-out
head -40 /tmp/fyne-out/model.go 2>/dev/null
```

Confirm fyne emits `model.go` in a Go package. Note the package name (default `ui`) and the constructor name. (The implementer may discover the constructor is called `NewCounter`, `Newcounter`, or generated differently; adapt the lowering below to match.)

Clean up: `rm -rf /tmp/fyne-out`.

- [ ] **Step 2: Replace the skeleton `RunTests` body**

In `codegen/platform/fyne/runtests.go`, replace the stub body with this implementation. Helper functions go below `RunTests`.

```go
//go:build !js

package fyne

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/codegen/testharness"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/ir"
)

func (g *Generator) RunTests(pkg *ir.Package, lang codegen.LangTranslator) ([]*codegen.TestResult, error) {
	if pkg == nil {
		return nil, nil
	}
	doc := ir.Convert(pkg)
	testFuncs := doc.TestFuncs()
	if len(testFuncs) == 0 {
		return nil, nil
	}
	groups := testharness.Group(testFuncs)

	var results []*codegen.TestResult
	for _, group := range groups {
		if group.Component == "" {
			// Standalone tests with no component receiver aren't covered
			// by the vertical slice.
			continue
		}
		compDoc := testharness.Promote(doc, group.Component)
		if compDoc == nil {
			continue
		}
		compPkg, diags := checker.Check(compDoc, &checker.Config{IsMain: true})
		hasErr := false
		for _, d := range diags {
			if d.Severity == ir.Error {
				hasErr = true
				break
			}
		}
		if hasErr || compPkg == nil {
			continue
		}
		resp, err := g.Generate(&codegen.Request{
			Pkg:     compPkg,
			Lang:    lang,
			Options: codegen.OptionsFromMap(map[string]any{"package": "ui"}),
		})
		if err != nil {
			return nil, fmt.Errorf("fyne generate %q: %w", group.Component, err)
		}
		if resp.Error != "" {
			return nil, fmt.Errorf("fyne generate %q: %s", group.Component, resp.Error)
		}

		grpResults, err := runFyneTestGroup(compPkg, group, resp.Files)
		if err != nil {
			return nil, err
		}
		results = append(results, grpResults...)
	}
	return results, nil
}

// runFyneTestGroup writes a temp Go module with the generated component
// code plus a `_test.go` file containing one Go test per SNGL test in
// the group. It shells `go test -json` and parses results.
func runFyneTestGroup(pkg *ir.Package, group testharness.TestGroup, files []*codegen.OutputFile) ([]*codegen.TestResult, error) {
	dir, err := os.MkdirTemp("", "sngl-fyne-test-")
	if err != nil {
		return nil, fmt.Errorf("mktemp: %w", err)
	}
	defer os.RemoveAll(dir)

	if err := writeGoMod(dir); err != nil {
		return nil, err
	}
	for _, f := range files {
		var buf bytes.Buffer
		if _, err := f.WriteTo(&buf); err != nil {
			return nil, fmt.Errorf("buffer file %s: %w", f.Path, err)
		}
		out := filepath.Join(dir, filepath.Base(f.Path))
		if err := os.WriteFile(out, buf.Bytes(), 0644); err != nil {
			return nil, fmt.Errorf("write %s: %w", out, err)
		}
	}

	testFile, err := writeTestFile(dir, pkg, group)
	if err != nil {
		return nil, err
	}
	_ = testFile

	cmd := exec.Command("go", "test", "-json", "-count=1", "./...")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run() // we read the JSON regardless of exit code

	return parseGoTestJSON(stdout.Bytes(), group), nil
}

// writeGoMod writes a minimal go.mod that pulls in fyne v2 and points
// the module path at a stable in-tree name. The Go toolchain resolves
// fyne from the module cache.
func writeGoMod(dir string) error {
	mod := `module sngltest

go 1.23

require fyne.io/fyne/v2 v2.5.0
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0644); err != nil {
		return fmt.Errorf("go.mod: %w", err)
	}
	// `go test` will run `go mod tidy` implicitly when missing sums; we
	// don't pre-populate go.sum. If the toolchain refuses to fetch
	// modules in offline mode, surface that as a test failure.
	return nil
}

// writeTestFile lowers each SNGL test in the group to a Go *testing.T
// function and writes them all into a single `_test.go` file alongside
// the generated component.
func writeTestFile(dir string, pkg *ir.Package, group testharness.TestGroup) (string, error) {
	var b bytes.Buffer
	b.WriteString("package ui\n\n")
	b.WriteString("import \"testing\"\n\n")

	// Helper to build a fresh component value used by every test func.
	// Until the fyne codegen exposes a per-component constructor, build
	// a zero value and let later tasks improve this.
	b.WriteString("func newTestComponent() any { return nil }\n\n")

	for _, tf := range group.Funcs {
		var fn *ir.Func
		for _, f := range pkg.Funcs {
			if f.Name == tf.Name {
				fn = f
				break
			}
		}
		if fn == nil {
			continue
		}
		// Strip the leading "test" so the Go test name is `TestFooBar`
		// rather than `TesttestFooBar`.
		suffix := strings.TrimPrefix(fn.Name, "test")
		b.WriteString(golang.LowerTestFunc(fn, suffix))
		b.WriteString("\n")
	}

	out := filepath.Join(dir, "component_test.go")
	if err := os.WriteFile(out, b.Bytes(), 0644); err != nil {
		return "", fmt.Errorf("write test file: %w", err)
	}
	return out, nil
}

// parseGoTestJSON reads the `go test -json` event stream and converts
// each PASS/FAIL test event into a codegen.TestResult.
func parseGoTestJSON(raw []byte, group testharness.TestGroup) []*codegen.TestResult {
	var out []*codegen.TestResult
	type event struct {
		Action  string  `json:"Action"`
		Test    string  `json:"Test"`
		Output  string  `json:"Output"`
		Elapsed float64 `json:"Elapsed"`
	}
	logs := map[string][]string{}
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e event
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		switch e.Action {
		case "output":
			if e.Test != "" {
				logs[e.Test] = append(logs[e.Test], e.Output)
			}
		case "pass", "fail":
			r := &codegen.TestResult{
				Component: group.Component,
				Desc:      e.Test,
				Passed:    e.Action == "pass",
				Log:       logs[e.Test],
			}
			if !r.Passed {
				r.Error = strings.Join(logs[e.Test], "")
			}
			out = append(out, r)
		}
	}
	return out
}
```

The implementer should adjust the package name (`ui`) and constructor (`newTestComponent`) to match what `fyne.Generate` actually emits.

- [ ] **Step 3: Run the e2e fixture**

```bash
go install ./cmd/sngl
sngl test --platform=fyne testdata/test_fyne_counter.sngl
```

Expected on first run: a failure of some kind — either the generated module fails to build, or the lowered Go test fails to compile (the `newTestComponent` placeholder + the assert referencing `c.count` is unlikely to type-check against a `nil` component).

This is expected. Task 5 makes the generated module compile and the assert pass.

- [ ] **Step 4: Commit**

```bash
git add codegen/platform/fyne/runtests.go
git commit -m "feat(fyne): RunTests temp-module + parse go test -json"
```

---

## Task 5: make the lowered test actually pass

**Files:**
- Modify: `codegen/platform/fyne/runtests.go`
- Modify: `codegen/lang/golang/testlower.go`

This task bridges the gap between the placeholder `newTestComponent() any { return nil }` and a real component value the lowered test can read.

- [ ] **Step 1: Discover the real fyne constructor name**

```bash
mkdir -p /tmp/fyne-inspect
go install ./cmd/sngl
sngl build --platform fyne --lang go --out /tmp/fyne-inspect testdata/test_fyne_counter.sngl 2>&1 | tail
grep -n "^func New\\|^type.*struct" /tmp/fyne-inspect/model.go | head -10
```

Read the generated `model.go`. Note:
- The Go struct name for the component (likely `Counter`).
- Whether there's a `NewCounter()` constructor or whether the struct must be zero-valued.
- The exported names of model fields (`Count` if `count` is promoted; whatever capitalization the lang translator uses).

Clean up: `rm -rf /tmp/fyne-inspect`.

- [ ] **Step 2: Update `writeTestFile` to emit a real constructor**

In `codegen/platform/fyne/runtests.go`, replace the `newTestComponent() any { return nil }` line with a call that produces a usable instance. The exact form depends on what step 1 reveals. If fyne emits a `NewCounter()` constructor:

```go
b.WriteString(fmt.Sprintf("func newTestComponent() *%s { return New%s() }\n\n", group.Component, group.Component))
```

If fyne emits a zero-valued struct only:

```go
b.WriteString(fmt.Sprintf("func newTestComponent() *%s { return &%s{} }\n\n", group.Component, group.Component))
```

Capitalize `group.Component` for Go-export correctness if needed (the fyne codegen will already export it, but the SNGL name is lowercase). Use the `golang.ExportName` helper if it exists:

```bash
grep -n "ExportName" codegen/lang/golang/*.go
```

- [ ] **Step 3: Update `LowerTestFunc` to use `*ComponentType` rather than `any`**

Signature shift:

```go
func LowerTestFunc(fn *ir.Func, suffix, componentType string) string {
```

Body change:

```go
fmt.Fprintf(&b, "\tc := newTestComponent()\n")
```

stays. The caller passes `componentType` so the function declaration can be `func newTestComponent() *Counter`. Actually since `newTestComponent` is generated by `writeTestFile` (not by `LowerTestFunc`), the only thing `LowerTestFunc` needs to do is reference `c` correctly — no signature change needed. Leave `LowerTestFunc` as-is. The constructor lives in `writeTestFile`.

- [ ] **Step 4: Re-run the e2e fixture**

```bash
sngl test --platform=fyne testdata/test_fyne_counter.sngl
```

Expected: PASS — one Go test runs in the temp module, the assert evaluates `c.Count == 0` (or whatever the exported field is), the test passes.

Common failure modes & remediation:

| Failure | Likely cause | Fix |
|---|---|---|
| `go test` complains about `fyne.io/fyne/v2` not in module cache | network-isolated host or fyne not pre-fetched | run `go mod download fyne.io/fyne/v2@v2.5.0` once in your dev environment; mark as a probe constraint to add later |
| `c.count` is undefined in lowered Go | field exported as `Count` | the SNGL→Go translator already handles this in `translateIRExpr`; if not, use `ExportName` |
| Lowered test references `string()` | conversion lowering | not relevant for `t.assert(c.count == 0)`; only relevant when the test reads the rendered text |

- [ ] **Step 5: Run the full matrix**

```bash
sngl test --platform=all testdata/test_fyne_counter.sngl
```

Expected: PASS on both `none` and `fyne`. (`html` will skip if Chrome isn't installed; that's fine.)

- [ ] **Step 6: Commit**

```bash
git add codegen/platform/fyne/runtests.go codegen/lang/golang/testlower.go
git commit -m "feat(fyne): wire constructor into lowered test module"
```

---

## Task 6: full verify

- [ ] **Step 1: Run `go tool verify`**

```bash
go tool verify
```

Expected: all Go tests pass, sngl-test step picks up the fyne platform via `--platform=all`. The matrix output includes `=== platform=fyne` with the new counter fixture passing.

If verify fails at the sngl-test step because `testdata/` is excluded from the recursive walk, that's expected — the new fixture lives in `testdata/`. Either move the smoke fixture out of testdata (e.g., into `examples/test-fyne-counter/`), or trust that verify's check is for non-testdata tests in the broader repo.

- [ ] **Step 2: If anything is left untracked or dirty, commit**

```bash
git status
```

If clean, done. Otherwise, fold any remaining cleanup into a final commit.

---

## Self-Review

**Spec coverage:**
- `fyne.RunTests` exists and is wired via the matrix runner — Task 1 + 4. ✓
- `ProbeTest` reports go-toolchain availability — Task 1. ✓
- SNGL `t.assert` lowers to Go `t.Errorf` — Task 2. ✓
- Temp-module + `go test -json` shell + JSON parse — Task 4. ✓
- End-to-end smoke test on a real fixture — Task 5. ✓
- Verify wiring through `--platform=all` — Task 6. ✓

**Out-of-scope explicitly:**
- Event invocation (`@click`) — needs a follow-up plan.
- `t.wait` lowering — needs a follow-up plan.
- `t.must`, `t.tick`, `t.test`, `t.setLocale` — follow-up.
- Multi-component test files — works in theory via the groups loop, but only `t.assert`-using tests will lower successfully until follow-ups land.

**Type consistency check:**
- `LowerTestFunc(fn, suffix)` — used in Task 2 and Task 4 with the same signature.
- `runFyneTestGroup(pkg, group, files)` — used in Task 4.
- `writeTestFile`, `writeGoMod`, `parseGoTestJSON` — internal to Task 4, consistent.

**Known fragility points to flag during execution:**
1. The fyne `Generate` output's package name and constructor are platform-codegen details that may not match the placeholders in Task 2. Task 5 step 1 reads the actual output before wiring.
2. `*ir.CallStmt` is the assumed IR shape for a method-call statement. If the canonical shape is `*ir.ExpressionStmt` wrapping a `*ir.Call`, Task 2's `lowerTestStmt` will need to be adjusted.
3. `go test -json` requires network access to fetch fyne if not cached. Document this in Phase 2b probe work; not blocking for Phase 2a.
