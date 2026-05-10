# Cross-Platform Test Runner — Phase 0 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land the shared `codegen/testharness/` package, the `Test.click/type/key/focus/blur/wait` UI-primitive stdlib surface, an interpreter implementation in the `none` testrunner, and two new fixtures that prove the primitives end-to-end. No platform other than `none` changes behavior.

**Architecture:** New package `codegen/testharness/` hosts `Probe`, `Group`, `Promote`, canonical key set, and shared types. `lib/types.sngl` declares the new methods on `struct Test`. `codegen/platform/none/testrunner` implements the methods over the existing component-value / element-ref machinery. Two fixtures in `testdata/` exercise click and text-input flows on the `none` interpreter.

**Tech Stack:** Go 1.24, SNGL stdlib (`lib/*.sngl`), existing testrunner interpreter.

---

## File Structure

**Create:**
- `codegen/testharness/types.go` — shared types (`Available`, `TestGroup`).
- `codegen/testharness/probe.go` — `Register` + `Probe` registry.
- `codegen/testharness/probe_test.go`
- `codegen/testharness/group.go` — `Group(testFuncs)` extracted from html.
- `codegen/testharness/group_test.go`
- `codegen/testharness/promote.go` — `Promote(doc, name)` extracted from html (and `compParams` helper moved with it).
- `codegen/testharness/promote_test.go`
- `codegen/testharness/keys.go` — canonical key-name set + `IsCanonicalKey`.
- `codegen/testharness/keys_test.go`
- `testdata/test_ui_click.sngl`
- `testdata/test_ui_input.sngl`

**Modify:**
- `lib/types.sngl` — append UI primitive method stubs on `struct Test`.
- `codegen/platform/none/testrunner/testing_t.go` — add `click`, `type`, `key`, `focus`, `blur`, `wait` to `callMethod`. Add focus state to `testingT`.
- `codegen/platform/none/testrunner/runner.go` — set `pkg`/`compName` plumbing if any new field needed (likely none).
- `codegen/platform/html/promote.go` — replace body with one-line delegation to `testharness.Promote`. Keep export for callers.
- `codegen/platform/html/testing.go` — replace inline grouping (lines ~32–50) with `testharness.Group` call. `compParams` helper moves to testharness.

---

## Task 1: scaffold `codegen/testharness/` with types + probe registry

**Files:**
- Create: `codegen/testharness/types.go`
- Create: `codegen/testharness/probe.go`
- Create: `codegen/testharness/probe_test.go`

- [ ] **Step 1: Write the failing test**

`codegen/testharness/probe_test.go`:

```go
package testharness

import "testing"

func TestProbe_unregistered(t *testing.T) {
	got := Probe("does-not-exist")
	if got.OK {
		t.Fatalf("unregistered platform must return OK=false, got %+v", got)
	}
	if got.Reason == "" {
		t.Fatalf("unregistered platform must include a reason")
	}
}

func TestProbe_registered_ok(t *testing.T) {
	Register("p0test-ok", func() Available { return Available{OK: true} })
	t.Cleanup(func() { unregisterForTest("p0test-ok") })
	got := Probe("p0test-ok")
	if !got.OK {
		t.Fatalf("expected OK=true, got %+v", got)
	}
}

func TestProbe_registered_unavailable(t *testing.T) {
	Register("p0test-bad", func() Available { return Available{OK: false, Reason: "missing dep"} })
	t.Cleanup(func() { unregisterForTest("p0test-bad") })
	got := Probe("p0test-bad")
	if got.OK || got.Reason != "missing dep" {
		t.Fatalf("expected unavailable with reason, got %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./codegen/testharness/...`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write `types.go`**

```go
// Package testharness holds shared scaffolding for per-platform SNGL test
// runners: dependency probes, test grouping, single-component promotion,
// and the canonical key-name set used by t.key / t.type primitives.
package testharness

// Available reports whether a platform's headless test runner can run on
// the current host. OK=true means the runner is callable; OK=false carries
// a human-readable Reason explaining what's missing.
type Available struct {
	OK     bool
	Reason string
}

// TestGroup pairs a target component name with the test FuncDefs whose
// second parameter selects that component. Tests with no component
// receiver land in the group keyed by the empty string.
type TestGroup struct {
	Component string
	Funcs     []TestFunc
}

// TestFunc carries enough identity to look up the IR func and report
// failures back to the runner. Concrete implementations resolve Func via
// pkg lookup at runtime.
type TestFunc struct {
	Name      string
	Component string
}
```

- [ ] **Step 4: Write `probe.go`**

```go
package testharness

import (
	"fmt"
	"sync"
)

type ProbeFunc func() Available

var (
	probesMu sync.RWMutex
	probes   = map[string]ProbeFunc{}
)

// Register installs a probe under the given platform name. Intended for
// init() in each platform package. Re-registering replaces the previous
// probe.
func Register(name string, fn ProbeFunc) {
	probesMu.Lock()
	defer probesMu.Unlock()
	probes[name] = fn
}

// Probe runs the registered probe for name. Returns OK=false with a
// reason when no probe is registered.
func Probe(name string) Available {
	probesMu.RLock()
	fn, ok := probes[name]
	probesMu.RUnlock()
	if !ok {
		return Available{OK: false, Reason: fmt.Sprintf("no probe registered for platform %q", name)}
	}
	return fn()
}

// unregisterForTest is exported within-package only for test cleanup.
func unregisterForTest(name string) {
	probesMu.Lock()
	defer probesMu.Unlock()
	delete(probes, name)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./codegen/testharness/...`
Expected: PASS — 3 tests.

- [ ] **Step 6: Commit**

```bash
git add codegen/testharness/types.go codegen/testharness/probe.go codegen/testharness/probe_test.go
git commit -m "feat(testharness): scaffold package with Probe registry"
```

---

## Task 2: extract test grouping into `testharness.Group`

**Files:**
- Create: `codegen/testharness/group.go`
- Create: `codegen/testharness/group_test.go`

- [ ] **Step 1: Write the failing test**

`codegen/testharness/group_test.go`:

```go
package testharness

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
)

func TestGroup_byComponent(t *testing.T) {
	mk := func(name, comp string) *ast.FuncDef {
		fn := &ast.FuncDef{Name: name}
		fn.Params.Params = []*ast.Param{
			{Name: "t", Type: &ast.NamedType{Name: "Test"}},
			{Name: "c", Type: &ast.NamedType{Name: comp}},
		}
		return fn
	}
	standalone := func(name string) *ast.FuncDef {
		fn := &ast.FuncDef{Name: name}
		fn.Params.Params = []*ast.Param{{Name: "t", Type: &ast.NamedType{Name: "Test"}}}
		return fn
	}

	groups := Group([]*ast.FuncDef{
		mk("testA", "Counter"),
		mk("testB", "Counter"),
		mk("testC", "Form"),
		standalone("testD"),
	})

	got := map[string]int{}
	for _, g := range groups {
		got[g.Component] = len(g.Funcs)
	}
	if got["Counter"] != 2 || got["Form"] != 1 || got[""] != 1 {
		t.Fatalf("unexpected grouping: %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./codegen/testharness/...`
Expected: FAIL — `Group` undefined.

- [ ] **Step 3: Write `group.go`**

```go
package testharness

import "git.duckfam.us/jonathan/sngl/ast"

// Group bins test FuncDefs by the component named in their second
// parameter. Tests whose second parameter is missing or not a NamedType
// land in the group keyed by the empty string.
//
// The returned slice has stable iteration order: components appear in the
// order their first test was encountered.
func Group(testFuncs []*ast.FuncDef) []TestGroup {
	order := []string{}
	idx := map[string]int{}
	for _, fn := range testFuncs {
		comp := componentOf(fn)
		if _, ok := idx[comp]; !ok {
			idx[comp] = len(order)
			order = append(order, comp)
		}
	}
	groups := make([]TestGroup, len(order))
	for i, comp := range order {
		groups[i].Component = comp
	}
	for _, fn := range testFuncs {
		comp := componentOf(fn)
		groups[idx[comp]].Funcs = append(groups[idx[comp]].Funcs, TestFunc{Name: fn.Name, Component: comp})
	}
	return groups
}

func componentOf(fn *ast.FuncDef) string {
	if fn == nil || len(fn.Params.Params) < 2 {
		return ""
	}
	nt, ok := fn.Params.Params[1].Type.(*ast.NamedType)
	if !ok {
		return ""
	}
	return nt.Name
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./codegen/testharness/...`
Expected: PASS.

- [ ] **Step 5: Migrate html to use `testharness.Group`**

Edit `codegen/platform/html/testing.go`. Replace the local grouping block (the `type testGroup struct { ... }` declaration and the loop building `groups := map[string]*testGroup{}`) with:

```go
groups := testharness.Group(testFuncs)
```

Then change the loop body from `for compName, group := range groups` to `for _, group := range groups` and use `group.Component` for `compName` and `group.Funcs` for the inner loop. Each `fn` in the inner loop is now a `testharness.TestFunc`; replace `fn.Name` references accordingly. Update the import block to add `git.duckfam.us/jonathan/sngl/codegen/testharness`.

The `runSingleTestFunc` callee currently takes `*ast.FuncDef` for the original AST node — change its signature to take the AST `*ast.FuncDef` separately if still needed, or look up by name from the doc's TestFuncs slice keyed by `group.Funcs[i].Name`. (The current callee already takes `fn *ast.FuncDef` plus `irFn`; pass the AST node retrieved by name lookup over `testFuncs`.)

- [ ] **Step 6: Run html tests**

Run: `go test ./codegen/platform/html/...`
Expected: PASS — no behavior change.

- [ ] **Step 7: Commit**

```bash
git add codegen/testharness/group.go codegen/testharness/group_test.go codegen/platform/html/testing.go
git commit -m "refactor(testharness): extract Group from html testing"
```

---

## Task 3: extract `Promote` into testharness

**Files:**
- Create: `codegen/testharness/promote.go`
- Create: `codegen/testharness/promote_test.go`
- Modify: `codegen/platform/html/promote.go`

- [ ] **Step 1: Write the failing test**

`codegen/testharness/promote_test.go`:

```go
package testharness

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/parser"
)

func TestPromote_extractsBody(t *testing.T) {
	src := `
component Counter(start int = 0) {
    var count = start
    text #lbl(value=string(count))
}

component Other { text(value="x") }
`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out := Promote(doc, "Counter")
	if out == nil {
		t.Fatal("Promote returned nil for known component")
	}
	// Promoted doc must contain the original ComponentDecls (so
	// references still resolve) plus the body of Counter at top level.
	var sawText, sawVar, sawParam, sawCounterDecl, sawOtherDecl bool
	for _, s := range out.Stmts {
		switch v := s.(type) {
		case *anyComponentDecl:
		default:
			_ = v
		}
	}
	for _, s := range out.Stmts {
		switch v := s.(type) {
		case *componentDecl:
			if v.name() == "Counter" { sawCounterDecl = true }
			if v.name() == "Other" { sawOtherDecl = true }
		}
	}
	_ = sawText; _ = sawVar; _ = sawParam; _ = sawCounterDecl; _ = sawOtherDecl
}
```

The above is intentionally a smoke test wrapper; the body uses local helper types we don't have. **Replace it with this simpler test** — keep this only:

```go
package testharness

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

func TestPromote_extractsBody(t *testing.T) {
	src := `component Counter(start int = 0) {
    var count = start
    text #lbl(value=string(count))
}

component Other { text(value="x") }
`
	doc, err := parser.Parse("test.sngl", strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out := Promote(doc, "Counter")
	if out == nil {
		t.Fatal("Promote returned nil for known component")
	}

	var components, varDecls, visualNodes int
	for _, s := range out.Stmts {
		switch s.(type) {
		case *ast.ComponentDecl:
			components++
		case *ast.VarDecl:
			varDecls++
		case *ast.VisualNode:
			visualNodes++
		}
	}
	if components < 2 {
		t.Errorf("expected both component decls preserved, got %d", components)
	}
	if varDecls == 0 {
		t.Errorf("expected promoted var/param decls, got 0")
	}
	if visualNodes == 0 {
		t.Errorf("expected promoted visual nodes, got 0")
	}

	if Promote(doc, "Nope") != nil {
		t.Errorf("unknown component must return nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./codegen/testharness/...`
Expected: FAIL — `Promote` undefined.

- [ ] **Step 3: Write `promote.go` (move logic + helper from html)**

```go
package testharness

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// Promote returns a Document with the named component's body promoted to
// top level, plus all component/struct/enum decls from the original.
// Returns nil when the component does not exist or has an empty body.
//
// Used by per-platform test runners to render a single component in
// isolation: the promoted document is re-checked to produce an
// ir.Package whose top-level surface is just that component.
func Promote(doc *ast.Document, name string) *ast.Document {
	comp := codegen.FindComponent(doc, name)
	if comp == nil || len(comp.Body.Stmts) == 0 {
		return nil
	}

	var stmts []ast.Stmt
	for _, s := range doc.Stmts {
		switch s.(type) {
		case *ast.StructDef, *ast.EnumDef, *ast.ComponentDecl:
			stmts = append(stmts, s)
		}
	}
	stmts = append(stmts, comp.Body.Stmts...)

	for _, p := range compParams(comp) {
		stmts = append(stmts, &ast.VarDecl{
			Specs: []ast.VarSpec{{
				Names:   []string{p.Name},
				Default: p.Default,
			}},
		})
	}
	return &ast.Document{Stmts: stmts}
}

// compParams returns the component's declared parameters. Mirrors the
// helper previously inlined in the html package; lifted here so other
// platform test runners can reuse it.
func compParams(c *ast.ComponentDecl) []*ast.Param {
	if c == nil || c.Params == nil {
		return nil
	}
	return c.Params.Params
}
```

- [ ] **Step 4: Replace html `PromoteComponent` body**

Edit `codegen/platform/html/promote.go` to:

```go
package html

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen/testharness"
)

// PromoteComponent is preserved as the html-package entry point; it
// delegates to testharness.Promote. New code should call testharness
// directly.
func PromoteComponent(doc *ast.Document, name string) *ast.Document {
	return testharness.Promote(doc, name)
}
```

If a `compParams` helper exists elsewhere in the html package and is used by other html-side code, leave that copy in place — only the body of `PromoteComponent` changes.

- [ ] **Step 5: Run tests**

Run: `go test ./codegen/testharness/... ./codegen/platform/html/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add codegen/testharness/promote.go codegen/testharness/promote_test.go codegen/platform/html/promote.go
git commit -m "refactor(testharness): extract Promote from html"
```

---

## Task 4: canonical key-name set

**Files:**
- Create: `codegen/testharness/keys.go`
- Create: `codegen/testharness/keys_test.go`

- [ ] **Step 1: Write the failing test**

`codegen/testharness/keys_test.go`:

```go
package testharness

import "testing"

func TestIsCanonicalKey(t *testing.T) {
	for _, k := range []string{"Enter", "Tab", "Escape", "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight", "Backspace", "Delete", "Home", "End", "PageUp", "PageDown", "Space", "A", "Z", "0", "9"} {
		if !IsCanonicalKey(k) {
			t.Errorf("expected %q to be canonical", k)
		}
	}
	for _, k := range []string{"enter", "RETURN", "Esc", "Foo", ""} {
		if IsCanonicalKey(k) {
			t.Errorf("expected %q to be rejected", k)
		}
	}
}

func TestCanonicalKeys_complete(t *testing.T) {
	if len(CanonicalKeys()) < 20 {
		t.Errorf("canonical key set too small: %d", len(CanonicalKeys()))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./codegen/testharness/...`
Expected: FAIL — `IsCanonicalKey` undefined.

- [ ] **Step 3: Write `keys.go`**

```go
package testharness

// CanonicalKeys returns the closed set of key names accepted by t.key()
// across all platforms. Each platform's runner translates these names to
// its native key code or message type. Names use PascalCase and match the
// W3C UI Events "key" attribute where possible.
func CanonicalKeys() []string {
	out := make([]string, 0, len(canonicalKeySet))
	for k := range canonicalKeySet {
		out = append(out, k)
	}
	return out
}

// IsCanonicalKey reports whether name is a member of the canonical key set.
func IsCanonicalKey(name string) bool {
	_, ok := canonicalKeySet[name]
	return ok
}

var canonicalKeySet = func() map[string]struct{} {
	keys := []string{
		"Enter", "Tab", "Escape", "Space", "Backspace", "Delete",
		"ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight",
		"Home", "End", "PageUp", "PageDown",
	}
	for c := 'A'; c <= 'Z'; c++ {
		keys = append(keys, string(c))
	}
	for c := '0'; c <= '9'; c++ {
		keys = append(keys, string(c))
	}
	m := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		m[k] = struct{}{}
	}
	return m
}()
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./codegen/testharness/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add codegen/testharness/keys.go codegen/testharness/keys_test.go
git commit -m "feat(testharness): canonical key-name set"
```

---

## Task 5: declare UI primitives in `lib/types.sngl`

**Files:**
- Modify: `lib/types.sngl`

- [ ] **Step 1: Append the new method stubs**

Add after `func Test.setLocale(loc string) {}` (around line 60):

```sngl
// Synthesize a click on the given element. The argument is normally an
// element-ref expression (e.g. `c.btn`); each platform's test runner
// dispatches the click through its native event path, firing any
// `@click` handler on the node and settling reactivity before the next
// statement runs.
func Test.click(node dyn) {}

// Type the given string into the focused element. Pass an explicit node
// to focus it first (equivalent to `t.focus(node)` followed by typing).
// Each character is delivered via the platform's native key-input path
// so component handlers and bindings observe per-character updates.
func Test.type(node dyn, s string) {}

// Send a single key press to the currently-focused element. Name must
// be a member of the canonical key set: `Enter`, `Tab`, `Escape`,
// `Space`, `Backspace`, `Delete`, `ArrowUp`/`Down`/`Left`/`Right`,
// `Home`, `End`, `PageUp`, `PageDown`, single uppercase letters
// `A`..`Z`, or single digits `0`..`9`.
func Test.key(name string) {}

// Move keyboard focus to the given element. The platform runner ensures
// any focus/blur side effects fire before returning.
func Test.focus(node dyn) {}

// Remove keyboard focus from the given element.
func Test.blur(node dyn) {}

// Pump the platform main loop until either the predicate returns true
// or the timeout (in milliseconds) elapses. Used to gate assertions on
// asynchronous reactivity (timers, debounced handlers). The predicate
// is evaluated repeatedly between settle steps.
func Test.wait(predicate func() bool, timeout_ms int) {}
```

- [ ] **Step 2: Verify the stdlib still parses and checks**

Run: `go install ./cmd/sngl && sngl check lib/types.sngl`
Expected: no errors. (If `sngl check` does not accept stdlib files directly, run `go test ./internal/checker/...` — the checker tests parse and load the embedded stdlib at startup.)

Run: `go test ./internal/checker/...`
Expected: PASS — stdlib loads cleanly.

- [ ] **Step 3: Commit**

```bash
git add lib/types.sngl
git commit -m "feat(stdlib): declare Test.click/type/key/focus/blur/wait"
```

---

## Task 6: implement `t.focus` / `t.blur` in the `none` interpreter

**Files:**
- Modify: `codegen/platform/none/testrunner/testing_t.go`
- Create: `testdata/test_focus.sngl` (kept short — used only here)

- [ ] **Step 1: Write the failing fixture**

`testdata/test_focus.sngl`:

```sngl
component focusbox {
    var focused = ""
    text_input #a(value="", @focus { focused = "a" }, @blur { focused = "" })
    text_input #b(value="", @focus { focused = "b" })
}

func testFocusFires(t Test, c focusbox) {
    t.focus(c.a)
    t.assert(c.focused == "a")
    t.focus(c.b)
    t.assert(c.focused == "b")
    t.blur(c.b)
    t.assert(c.focused == "")
}
```

If `text_input` is not the canonical input element name in this codebase, replace with whatever the stdlib uses (`input`, `textfield`, etc.). Confirm with `grep -n "component text_input\|^component input" lib/components.sngl`.

- [ ] **Step 2: Run the fixture to verify it fails**

Run: `go install ./cmd/sngl && sngl test testdata/test_focus.sngl`
Expected: FAIL with `Test has no method "focus"` (or similar — current interpreter rejects the call).

- [ ] **Step 3: Add focus state and method dispatch**

Edit `codegen/platform/none/testrunner/testing_t.go`. Add a `focused` field to `testingT` (find its struct decl, likely in `runner.go`; add `focused any` if not present — Step 3a below).

Add these cases inside the `switch method` block in `callMethod`:

```go
case "focus":
	if len(args) != 1 {
		return nil, fmt.Errorf("t.focus() requires 1 argument")
	}
	target, err := env.Eval(args[0])
	if err != nil {
		return nil, err
	}
	prev := tv.focused
	tv.focused = target
	if prev != nil {
		if err := fireElementHandler(env, prev, "@blur", nil); err != nil {
			return nil, err
		}
	}
	if target != nil {
		if err := fireElementHandler(env, target, "@focus", nil); err != nil {
			return nil, err
		}
	}
	syncMutationsBack(env)
	return nil, nil

case "blur":
	if len(args) != 1 {
		return nil, fmt.Errorf("t.blur() requires 1 argument")
	}
	target, err := env.Eval(args[0])
	if err != nil {
		return nil, err
	}
	if tv.focused != nil {
		if err := fireElementHandler(env, tv.focused, "@blur", nil); err != nil {
			return nil, err
		}
	}
	tv.focused = nil
	_ = target
	syncMutationsBack(env)
	return nil, nil
```

- [ ] **Step 3a: Add the `focused` field on `testingT`**

Find the `testingT` struct (search: `grep -n "type testingT struct" codegen/platform/none/testrunner/`). Add `focused any` to the struct.

- [ ] **Step 3b: Add helpers `fireElementHandler` and `syncMutationsBack`**

Append to the bottom of `testing_t.go`:

```go
// fireElementHandler invokes the named "@event" handler on a resolved
// element-ref value, if present. Element refs resolve to a
// map[string]any (single match) or a []any of those maps (for-loop
// match). For the list case, the handler fires on the first element to
// match the platform behavior of focus targeting a single widget.
func fireElementHandler(env *Env, target any, event string, _ map[string]any) error {
	m := elementMapOf(target)
	if m == nil {
		return nil
	}
	h, ok := m[event].(*ir.Func)
	if !ok || h == nil {
		return nil
	}
	_, err := env.runEventHandler(h, nil)
	return err
}

func elementMapOf(v any) map[string]any {
	switch x := v.(type) {
	case map[string]any:
		return x
	case []any:
		if len(x) == 0 {
			return nil
		}
		if m, ok := x[0].(map[string]any); ok {
			return m
		}
	}
	return nil
}

// syncMutationsBack copies env.vars writes performed by event handlers
// back to the active componentValue so subsequent property reads observe
// the new state.
func syncMutationsBack(env *Env) {
	for _, v := range env.vars {
		if cv, ok := v.(*componentValue); ok {
			for k := range cv.vars {
				if nv, ok := env.vars[k]; ok {
					cv.vars[k] = nv
				}
			}
		}
	}
}
```

The `runEventHandler` method on `*Env` is already defined in `eval.go`; it accepts `(handler *ir.Func, args []ir.CallArg)` and returns `(any, error)`. Pass `nil` for args since focus/blur receive no event payload.

- [ ] **Step 4: Run the fixture to verify it passes**

Run: `sngl test testdata/test_focus.sngl`
Expected: PASS.

- [ ] **Step 5: Run the full none testrunner test suite**

Run: `go test ./codegen/platform/none/...`
Expected: PASS — no regression.

- [ ] **Step 6: Commit**

```bash
git add codegen/platform/none/testrunner/testing_t.go testdata/test_focus.sngl
git commit -m "feat(testrunner/none): implement t.focus and t.blur"
```

---

## Task 7: implement `t.click` in the `none` interpreter

**Files:**
- Modify: `codegen/platform/none/testrunner/testing_t.go`

- [ ] **Step 1: Write the failing fixture**

`testdata/test_ui_click.sngl`:

```sngl
component clickcounter {
    var count = 0
    button #btn(text="+", @click { count = count + 1 })
}

func testClickIncrements(t Test, c clickcounter) {
    t.assert(c.count == 0)
    t.click(c.btn)
    t.assert(c.count == 1)
    t.click(c.btn)
    t.click(c.btn)
    t.assert(c.count == 3)
}
```

- [ ] **Step 2: Run the fixture to verify it fails**

Run: `sngl test testdata/test_ui_click.sngl`
Expected: FAIL with `Test has no method "click"`.

- [ ] **Step 3: Add the `click` case in `callMethod`**

Insert in `testing_t.go` `callMethod` switch:

```go
case "click":
	if len(args) != 1 {
		return nil, fmt.Errorf("t.click() requires 1 argument")
	}
	target, err := env.Eval(args[0])
	if err != nil {
		return nil, err
	}
	if err := fireElementHandler(env, target, "@click", nil); err != nil {
		return nil, err
	}
	syncMutationsBack(env)
	return nil, nil
```

- [ ] **Step 4: Run the fixture to verify it passes**

Run: `sngl test testdata/test_ui_click.sngl`
Expected: PASS — three asserts, count goes 0 → 1 → 3.

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/none/testrunner/testing_t.go testdata/test_ui_click.sngl
git commit -m "feat(testrunner/none): implement t.click"
```

---

## Task 8: implement `t.type` and `t.key` in the `none` interpreter

**Files:**
- Modify: `codegen/platform/none/testrunner/testing_t.go`

- [ ] **Step 1: Write the failing fixture**

`testdata/test_ui_input.sngl`:

```sngl
component greeter {
    var name = ""
    var submitted = ""
    text_input #field(value=name, @input { name = event.value }, @key { if event.key == "Enter" { submitted = name } })
}

func testTypeUpdatesValue(t Test, c greeter) {
    t.focus(c.field)
    t.type(c.field, "Ada")
    t.assert(c.name == "Ada")
    t.assert(c.submitted == "")
}

func testEnterSubmits(t Test, c greeter) {
    t.focus(c.field)
    t.type(c.field, "Hopper")
    t.key("Enter")
    t.assert(c.submitted == "Hopper")
}
```

If the actual stdlib input component uses different handler names (`@change` instead of `@input`, etc.), adjust accordingly — confirm via `grep -n "^component text_input\|^component input" lib/components.sngl`. Confirm whether the event payload object is `event.value` or `e.value` (search `lib/components.sngl` for "@input" or analogous) and adjust.

- [ ] **Step 2: Run the fixture to verify it fails**

Run: `sngl test testdata/test_ui_input.sngl`
Expected: FAIL — `Test has no method "type"`.

- [ ] **Step 3: Add `type` and `key` cases**

Insert in `callMethod`:

```go
case "type":
	if len(args) != 2 {
		return nil, fmt.Errorf("t.type() requires 2 arguments")
	}
	target, err := env.Eval(args[0])
	if err != nil {
		return nil, err
	}
	sVal, err := env.Eval(args[1])
	if err != nil {
		return nil, err
	}
	s, ok := sVal.(string)
	if !ok {
		return nil, fmt.Errorf("t.type() second argument must be a string, got %T", sVal)
	}
	tv.focused = target
	cur := stringValueOfElement(target)
	for _, r := range s {
		cur += string(r)
		setElementValue(target, cur)
		event := map[string]any{"value": cur, "key": string(r)}
		if err := fireElementHandler(env, target, "@input", event); err != nil {
			return nil, err
		}
		if err := fireElementHandler(env, target, "@key", event); err != nil {
			return nil, err
		}
		syncMutationsBack(env)
	}
	return nil, nil

case "key":
	if len(args) != 1 {
		return nil, fmt.Errorf("t.key() requires 1 argument")
	}
	nameVal, err := env.Eval(args[0])
	if err != nil {
		return nil, err
	}
	name, ok := nameVal.(string)
	if !ok {
		return nil, fmt.Errorf("t.key() argument must be a string, got %T", nameVal)
	}
	if !testharness.IsCanonicalKey(name) {
		return nil, fmt.Errorf("t.key(%q): not a canonical key name", name)
	}
	if tv.focused == nil {
		return nil, nil
	}
	event := map[string]any{"key": name}
	if err := fireElementHandler(env, tv.focused, "@key", event); err != nil {
		return nil, err
	}
	syncMutationsBack(env)
	return nil, nil
```

Add the testharness import to the file:

```go
import (
	// ... existing imports ...
	"git.duckfam.us/jonathan/sngl/codegen/testharness"
)
```

- [ ] **Step 3a: Helpers for element value read/write**

Append to `testing_t.go`:

```go
// stringValueOfElement reads the "value" key from a resolved element ref.
// Returns "" when missing or non-string.
func stringValueOfElement(target any) string {
	m := elementMapOf(target)
	if m == nil {
		return ""
	}
	if s, ok := m["value"].(string); ok {
		return s
	}
	return ""
}

// setElementValue writes "value" on the resolved element ref. Used so
// per-character typing surfaces the new value to subsequent handler
// invocations within the same t.type() call.
func setElementValue(target any, v string) {
	m := elementMapOf(target)
	if m == nil {
		return
	}
	m["value"] = v
}
```

- [ ] **Step 3b: Update `fireElementHandler` to pass the event**

Replace the body of `fireElementHandler` from Task 6 with:

```go
func fireElementHandler(env *Env, target any, event string, payload map[string]any) error {
	m := elementMapOf(target)
	if m == nil {
		return nil
	}
	h, ok := m[event].(*ir.Func)
	if !ok || h == nil {
		return nil
	}
	saved := env.vars["event"]
	if payload != nil {
		env.vars["event"] = payload
	}
	_, err := env.runEventHandler(h, nil)
	if payload != nil {
		if saved == nil {
			delete(env.vars, "event")
		} else {
			env.vars["event"] = saved
		}
	}
	return err
}
```

If `runEventHandler` already takes a separate event-payload argument (verify by re-reading `eval.go` near line 1071), prefer threading it through that argument instead of binding `env.vars["event"]`. Use whichever path the existing implementation supports — the goal is that handler bodies referencing `event.value` / `event.key` see the payload.

- [ ] **Step 4: Run the fixture to verify it passes**

Run: `sngl test testdata/test_ui_input.sngl`
Expected: PASS — both tests.

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/none/testrunner/testing_t.go testdata/test_ui_input.sngl
git commit -m "feat(testrunner/none): implement t.type and t.key"
```

---

## Task 9: implement `t.wait` in the `none` interpreter

**Files:**
- Modify: `codegen/platform/none/testrunner/testing_t.go`

- [ ] **Step 1: Write the failing fixture**

`testdata/test_ui_wait.sngl`:

```sngl
component ticker {
    var count = 0
    timer(every=10ms) { count = count + 1 }
    text(value=string(count))
}

func testWaitSettlesTimer(t Test, c ticker) {
    t.assert(c.count == 0)
    t.wait(() => c.count >= 3, 1000)
    t.assert(c.count >= 3)
}
```

If the timer literal syntax differs, confirm via `grep -n "^timer\b\|component timer" lib/components.sngl` and a fixture that uses one (search `testdata/` for `timer`). Adjust the unit (`ms`/`s`) to match.

- [ ] **Step 2: Run the fixture to verify it fails**

Run: `sngl test testdata/test_ui_wait.sngl`
Expected: FAIL — `Test has no method "wait"`.

- [ ] **Step 3: Add the `wait` case**

Insert in `callMethod`:

```go
case "wait":
	if len(args) != 2 {
		return nil, fmt.Errorf("t.wait() requires 2 arguments")
	}
	pred, err := env.Eval(args[0])
	if err != nil {
		return nil, err
	}
	lv, ok := pred.(*lambdaValue)
	if !ok {
		return nil, fmt.Errorf("t.wait() first argument must be a function, got %T", pred)
	}
	tov, err := env.Eval(args[1])
	if err != nil {
		return nil, err
	}
	timeoutMs := toInt(tov)
	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	for {
		out, err := lv.callWithEnv(env, nil)
		if err != nil {
			return nil, err
		}
		if b, ok := out.(bool); ok && b {
			return nil, nil
		}
		if time.Now().After(deadline) {
			return nil, nil
		}
		// Fire a tick of any active component timers and re-sync.
		for _, v := range env.vars {
			if cv, ok := v.(*componentValue); ok {
				compEnv := cv.compEnv()
				if err := fireTimers(tv.pkg, compEnv); err != nil {
					return nil, err
				}
				for k := range cv.vars {
					if nv, ok := compEnv.vars[k]; ok {
						cv.vars[k] = nv
						if !cv.testParams[k] {
							cv.env.vars[k] = nv
						}
					}
				}
			}
		}
	}
```

The `time` import already exists in `testing_t.go`; verify with `grep -n '"time"' codegen/platform/none/testrunner/testing_t.go`.

- [ ] **Step 4: Run the fixture to verify it passes**

Run: `sngl test testdata/test_ui_wait.sngl`
Expected: PASS — count reaches 3 well before the 1s timeout.

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/none/testrunner/testing_t.go testdata/test_ui_wait.sngl
git commit -m "feat(testrunner/none): implement t.wait"
```

---

## Task 10: register a probe for `none`

**Files:**
- Modify: `codegen/platform/none/none.go`

- [ ] **Step 1: Add the probe registration**

Edit `codegen/platform/none/none.go`. Add to imports:

```go
"git.duckfam.us/jonathan/sngl/codegen/testharness"
```

Replace the existing `init()` with:

```go
func init() {
	codegen.RegisterPlatform(&Generator{})
	testharness.Register("none", func() testharness.Available {
		return testharness.Available{OK: true}
	})
}
```

- [ ] **Step 2: Add a smoke test**

Append to `codegen/platform/none/none.go`'s adjacent `_test.go` file (create `codegen/platform/none/none_test.go` if absent):

```go
package none

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen/testharness"
)

func TestProbeRegistered(t *testing.T) {
	got := testharness.Probe("none")
	if !got.OK {
		t.Fatalf("none probe must be OK, got %+v", got)
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./codegen/platform/none/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add codegen/platform/none/none.go codegen/platform/none/none_test.go
git commit -m "feat(none): register testharness probe"
```

---

## Task 11: full verify

- [ ] **Step 1: Run the full project verification**

Run: `go tool verify`
Expected: PASS — no new failures, all existing tests green, two new fixtures running on `none`.

- [ ] **Step 2: Run the SNGL test suite explicitly on `none`**

Run: `sngl test testdata/...`
Expected: PASS — all `test_*.sngl` fixtures including the four new ones (`test_focus`, `test_ui_click`, `test_ui_input`, `test_ui_wait`).

- [ ] **Step 3: If any fixture fails because of stdlib component-name mismatches identified in Tasks 6/8/9**

Update the fixture's component / handler names to match the canonical stdlib (`text_input` → whatever, `@input` → `@change`, etc.) and re-run. Commit the fixture-only fixes:

```bash
git add testdata/test_focus.sngl testdata/test_ui_click.sngl testdata/test_ui_input.sngl testdata/test_ui_wait.sngl
git commit -m "fix(testdata): align UI primitive fixtures with stdlib component names"
```

- [ ] **Step 4: Final commit if any cleanup left**

```bash
git status
# expected: working tree clean
```

---

## Self-Review Notes

**Spec coverage:**
- Shared harness (`Probe`, `Group`, `Promote`, key set) — Tasks 1–4. ✓
- UI primitive stdlib decls — Task 5. ✓
- UI primitive impl on at least one platform (the `none` interpreter) — Tasks 6–9. ✓
- Two fixtures exercising the API — Tasks 7, 8 (plus 6, 9 add focus/wait fixtures). ✓
- Probe registration story (per-platform `init()`) — Task 10 (none only; html/fyne/etc. land in their phase). ✓
- `Settle()` interface — **deferred to Phase 1** (no shared interface needed until two platforms implement it). Acceptable scope reduction; flag in Phase 1 plan.
- Fixture-targeting frontmatter — explicitly deferred per spec ("until a real need appears").

**Type/name consistency:**
- `Available`, `TestGroup`, `TestFunc`, `Probe`, `Register`, `Group`, `Promote`, `IsCanonicalKey`, `CanonicalKeys`, `fireElementHandler`, `elementMapOf`, `syncMutationsBack`, `stringValueOfElement`, `setElementValue` — all consistently named across tasks.
- `testingT.focused` field used in Tasks 6, 8.
- Fixture component / handler names depend on the stdlib; Task 11 Step 3 covers the cleanup if they diverge from `text_input` / `@input`.
