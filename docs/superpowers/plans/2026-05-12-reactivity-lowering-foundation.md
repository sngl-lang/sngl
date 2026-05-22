# Reactivity Lowering Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land the lowering-side prerequisites for consolidating reactivity in SNGL: a real `lower` intrinsic namespace (resolvable through `ir.Convert` round-trip), a new `lower.removeChild` intrinsic, an extended `passReactivity` that owns both prop and structural reactivity, and a shared `intrinsic_walker.go` scaffold platforms will later consume.

**Architecture:** Three phases. Phase 1 adds the `internal://lower` namespace and intrinsics + refactors `passDeclarative` to use them. Phase 2 extends `passReactivity` so reactive `if`/`for` get rewritten into per-slot generator Funcs (`__renderSlot<N>`) that tear down and rebuild via the lower intrinsics. Phase 3 introduces a shared `IntrinsicTranslator` interface and walker that fyne/gtk4/html will plug into in follow-up plans (B/C/D). No platform behavior changes in this plan; every existing test continues to pass.

**Tech Stack:** Go, `internal/lower` IR-to-IR passes, `internal/checker` for type-checking lowered output, txtar golden tests, fuzz tests.

**Spec:** `docs/superpowers/specs/2026-05-12-reactivity-lowering-consolidation-design.md`

---

## File Structure

**Create:**
- `lib/lower.sngl` — stdlib hook that imports `internal://lower` so the namespace is merged into every checked package
- `codegen/intrinsic_walker.go` — shared walker + `IntrinsicTranslator` interface
- `codegen/intrinsic_walker_test.go` — unit tests for the walker
- `internal/lower/testdata/structural_if.txtar` — golden test for reactive `if`
- `internal/lower/testdata/structural_for.txtar` — golden test for reactive `for`
- `internal/lower/testdata/structural_mixed.txtar` — golden test for mixed prop + structural reactivity

**Modify:**
- `ir/intrinsics.go` — add `LowerIntrinsics` list; extend `LookupIntrinsic`
- `internal/checker/checker.go:298-309` — add `internal://lower` case
- `internal/lower/declarative.go` — replace hand-built `*ir.Func` values with `LowerIntrinsics`-derived ones; set `Call.Receiver` to namespace ident
- `internal/lower/reactivity.go` — extend `reactivityState` with slot tracking; add `collectFromIf`/`collectFromFor`; synthesize `__slot<N>` Vars and `__renderSlot<N>` Funcs; replace reactive `If`/`For` with `CallStmt`s; extend `updatersFor` to splice slot calls
- `internal/lower/testdata/declarative_with_*.txtar` — update expected outputs to namespaced-call shape
- `fuzz_test.go` — add `FuzzLoweredDocument`

---

## Phase 1: `lower` intrinsic namespace

### Task 1: Declare `LowerIntrinsics`

**Files:**
- Modify: `ir/intrinsics.go`
- Test: `ir/intrinsics_test.go` (create if missing)

- [ ] **Step 1: Write the failing test**

Append to `ir/intrinsics_test.go`:

```go
package ir

import "testing"

func TestLookupLowerIntrinsic(t *testing.T) {
	for _, name := range []string{"CreateNode", "AppendChild", "RemoveChild", "AttachHandler"} {
		if def := LookupIntrinsic(name); def == nil {
			t.Errorf("LookupIntrinsic(%q) returned nil; expected definition", name)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./ir/ -run TestLookupLowerIntrinsic -v`
Expected: FAIL with `LookupIntrinsic("CreateNode") returned nil`.

- [ ] **Step 3: Add `LowerIntrinsics` list**

Append to `ir/intrinsics.go` before the `LookupIntrinsic` definition:

```go
// LowerIntrinsics are intrinsics emitted by lowering passes. They live in
// the `lower` namespace (imported by the synthetic internal://lower
// package). Every codegen backend that consumes lowered output must
// provide native translations.
var LowerIntrinsics = []IntrinsicDef{
	{Name: "CreateNode", Params: []*Param{{Name: "tag", Type: TypString}}, Return: TypDyn},
	{Name: "AppendChild", Params: []*Param{{Name: "parent", Type: TypDyn}, {Name: "child", Type: TypDyn}}, Return: TypVoid},
	{Name: "RemoveChild", Params: []*Param{{Name: "parent", Type: TypDyn}, {Name: "child", Type: TypDyn}}, Return: TypVoid},
	{Name: "AttachHandler", Params: []*Param{{Name: "node", Type: TypDyn}, {Name: "event", Type: TypString}, {Name: "handler", Type: TypDyn}}, Return: TypVoid},
}
```

Then update `LookupIntrinsic` to include `LowerIntrinsics` in its search list:

```go
func LookupIntrinsic(name string) *IntrinsicDef {
	for _, list := range [][]IntrinsicDef{Intrinsics, AlertIntrinsics, FileIntrinsics, LowerIntrinsics} {
		for i := range list {
			if list[i].Name == name {
				return &list[i]
			}
		}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./ir/ -run TestLookupLowerIntrinsic -v`
Expected: PASS.

- [ ] **Step 5: Run full ir package tests**

Run: `go test ./ir/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add ir/intrinsics.go ir/intrinsics_test.go
git commit -m "ir: declare LowerIntrinsics for lowering-emitted intrinsics

CreateNode, AppendChild, RemoveChild, AttachHandler — the four ops that
lowering passes (NoDeclarative, NoReactivity) emit. Lookup-able through
LookupIntrinsic alongside the existing language/alert/file lists.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Wire `internal://lower` in the checker

**Files:**
- Modify: `internal/checker/checker.go:298-309`
- Test: `internal/checker/checker_lower_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `internal/checker/checker_lower_test.go`:

```go
package checker

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/parser"
)

func TestInternalLowerNamespace(t *testing.T) {
	src := `
import "internal://lower"

func test() => lower.CreateNode("text")
`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg, diags := Check(doc, &Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == DiagError {
			t.Errorf("unexpected diag: %s", d.Message)
		}
	}
	if pkg == nil {
		t.Fatal("Check returned nil pkg")
	}
	var found bool
	for _, imp := range pkg.Imports {
		if strings.HasSuffix(imp.Path, "lower") && imp.Pkg != nil {
			found = true
			if imp.Pkg.LookupFunc("CreateNode") == nil {
				t.Errorf("lower.CreateNode not registered on imported pkg")
			}
		}
	}
	if !found {
		t.Errorf("internal://lower import not present in checked pkg")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/checker/ -run TestInternalLowerNamespace -v`
Expected: FAIL — checker will emit `unknown internal package: "lower"`.

- [ ] **Step 3: Add the `internal://lower` case**

In `internal/checker/checker.go`, locate the switch at line ~300 and add:

```go
		case "stdlib":
			irImport.Pkg = c.buildIntrinsicsPkgFrom(ir.Intrinsics)
		case "alert":
			irImport.Pkg = c.buildIntrinsicsPkgFrom(ir.AlertIntrinsics)
		case "file":
			irImport.Pkg = c.buildIntrinsicsPkgFrom(ir.FileIntrinsics)
		case "lower":
			irImport.Pkg = c.buildIntrinsicsPkgFrom(ir.LowerIntrinsics)
		default:
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/checker/ -run TestInternalLowerNamespace -v`
Expected: PASS.

- [ ] **Step 5: Run full checker tests**

Run: `go test ./internal/checker/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/checker/checker.go internal/checker/checker_lower_test.go
git commit -m "checker: register internal://lower intrinsic namespace

Source that imports \"internal://lower\" can now reference
lower.CreateNode etc. as resolvable symbols. Used by ir.Convert
round-trip after lowering: lowered IR formats back to SNGL source that
re-parses and re-checks cleanly.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Add `lib/lower.sngl` so the import propagates via stdlib merge

**Files:**
- Create: `lib/lower.sngl`
- Test: `internal/checker/checker_lower_test.go` (extend)

- [ ] **Step 1: Write the failing test**

Append to `internal/checker/checker_lower_test.go`:

```go
func TestLowerNamespaceAutoImported(t *testing.T) {
	// User source that DOES NOT explicitly import internal://lower
	// should still resolve `lower.CreateNode` because the stdlib
	// brings the import in transitively via lib/lower.sngl.
	src := `func test() => lower.CreateNode("text")`
	doc, err := parser.Parse("test.sngl", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, diags := Check(doc, &Config{IsMain: true})
	for _, d := range diags {
		if d.Severity == DiagError {
			t.Errorf("unexpected diag: %s", d.Message)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/checker/ -run TestLowerNamespaceAutoImported -v`
Expected: FAIL — `unknown identifier: lower`.

- [ ] **Step 3: Create `lib/lower.sngl`**

```sngl
// Lowering intrinsics. The `lower` namespace is populated by the
// checker from ir.LowerIntrinsics. Lowering passes emit calls into
// this namespace; importing it here makes the namespace available
// at recheck time via the standard stdlib merge.
import "internal://lower"
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/checker/ -run TestLowerNamespaceAutoImported -v`
Expected: PASS.

- [ ] **Step 5: Run full checker tests + verify (the embed pulls lib/* automatically)**

Run: `go test ./internal/checker/... ./lib/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add lib/lower.sngl internal/checker/checker_lower_test.go
git commit -m "lib: import internal://lower from stdlib

Makes the lower namespace available in every user-checked package via
the standard stdlib import merge. Required for ir.Convert round-trip
after lowering: lowered IR references lower.* without the user source
explicitly importing internal://lower.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Refactor `declarative.go` to use the `lower` namespace

**Files:**
- Modify: `internal/lower/declarative.go:115-156`
- Modify: `internal/lower/testdata/declarative_basic.txtar`
- Modify: `internal/lower/testdata/declarative_nested.txtar`
- Modify: `internal/lower/testdata/declarative_with_if.txtar`
- Modify: `internal/lower/testdata/declarative_with_reactivity.txtar`

Background: today `declarative.go` builds the intrinsic funcs ad-hoc with names like `"lower.createNode"`. That makes the `*ir.Func` non-resolvable. We're swapping them for the `LowerIntrinsics`-backed funcs which carry PascalCase names (`CreateNode`) and live on the synthetic `internal://lower` package — so the `Call.Receiver` is the `lower` namespace ident.

- [ ] **Step 1: Read the current implementation**

Run: `sed -n '110,160p' internal/lower/declarative.go`
Verify the three helper functions `createFunc`, `appendChildFunc`, `attachHandlerFunc` exist.

- [ ] **Step 2: Add lookup helper**

In `internal/lower/declarative.go`, replace the three lazy-init helpers with a single eager init that pulls from `LowerIntrinsics` once when the state is constructed. Add a field `intrinsics map[string]*ir.Func` on `declarativeState`, and in `newDeclarativeState`:

```go
func newDeclarativeState(pkg *ir.Package, caps Caps) *declarativeState {
	st := &declarativeState{liftHandlers: caps.NoLambda, intrinsics: map[string]*ir.Func{}}
	for _, def := range ir.LowerIntrinsics {
		st.intrinsics[def.Name] = intrinsicFunc(def)
	}
	if st.liftHandlers {
		st.lifter = &lifter{pkg: pkg}
	}
	return st
}

// intrinsicFunc materializes an IntrinsicDef into the *ir.Func form
// lowering uses when emitting calls.
func intrinsicFunc(def ir.IntrinsicDef) *ir.Func {
	return &ir.Func{
		Name:      def.Name,
		Intrinsic: def.Name,
		Params:    def.Params,
		Return:    def.Return,
	}
}
```

- [ ] **Step 3: Add a `lower` namespace-ident helper**

Append to `internal/lower/declarative.go`:

```go
// lowerNSIdent returns a fresh Ident referring to the `lower` namespace.
// Used as Call.Receiver so ir.Convert emits SelectExpr{Operand: Ident("lower"), Field: name}.
func lowerNSIdent() *ir.Ident {
	return &ir.Ident{Name: "lower", Type: ir.TypDyn}
}
```

- [ ] **Step 4: Update each callsite that built a `lower.*` Call**

Replace each of the three callsites in `lowerNode`:

```go
// 1. createNode — was: Func: st.createFunc(), ... Args: ...
stmts = append(stmts, &ir.LocalVar{
	Name: id,
	Type: varType,
	Init: &ir.Call{
		Type:     ir.TypDyn,
		Receiver: lowerNSIdent(),
		Func:     st.intrinsics["CreateNode"],
		Args: []ir.CallArg{
			{Value: &ir.Literal{Type: ir.TypString, Raw: n.Name}},
		},
	},
})
```

```go
// 3. attachHandler — replace Func: st.attachHandlerFunc()
stmts = append(stmts, &ir.CallStmt{
	Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: lowerNSIdent(),
		Func:     st.intrinsics["AttachHandler"],
		Args: []ir.CallArg{
			{Value: &ir.Ident{Name: id, Type: ir.TypDyn, IsElementRef: true}},
			{Value: &ir.Literal{Type: ir.TypString, Raw: h.Name}},
			{Value: handlerArg},
		},
	},
})
```

```go
// 4. appendChild — replace Func: st.appendChildFunc()
stmts = append(stmts, &ir.CallStmt{
	Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: lowerNSIdent(),
		Func:     st.intrinsics["AppendChild"],
		Args: []ir.CallArg{
			{Value: &ir.Ident{Name: id, Type: ir.TypDyn, IsElementRef: true}},
			{Value: &ir.Ident{Name: cn.ID, Type: ir.TypDyn, IsElementRef: true}},
		},
	},
})
```

Delete the now-unused `createFunc`, `appendChildFunc`, `attachHandlerFunc` methods and their `create`, `appendChild`, `attachHandler` fields on `declarativeState`.

- [ ] **Step 5: Run the golden tests; expect failures so we can update expected outputs**

Run: `go test ./internal/lower/ -run TestLower/declarative -v`
Expected: failures on `declarative_basic`, `declarative_nested`, `declarative_with_if`, `declarative_with_reactivity` — the formatted output now prints the call as `lower.CreateNode(...)` (capitalized).

- [ ] **Step 6: Regenerate goldens**

Run: `go test ./internal/lower/ -run TestLower/declarative -update`
Inspect the diff with `git diff internal/lower/testdata/declarative_*.txtar`. The only changes should be `lower.createNode` → `lower.CreateNode` (and the other three intrinsic names) inside the `expected.sngl` sections. Verify no semantic shifts.

- [ ] **Step 7: Re-run goldens**

Run: `go test ./internal/lower/ -run TestLower/declarative -v`
Expected: PASS.

- [ ] **Step 8: Run full lower test suite**

Run: `go test ./internal/lower/...`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/lower/declarative.go internal/lower/testdata/declarative_*.txtar
git commit -m "lower: route NoDeclarative intrinsics through ir.LowerIntrinsics

Replaces ad-hoc *ir.Func values with PascalCase intrinsic-backed Funcs
plus a Receiver ident for the lower namespace. Result: lowered IR
Convert→Format→Parse→Check round-trips cleanly because lower.* now
resolves through the synthetic internal://lower import.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Add the post-lowering round-trip fuzz target

**Files:**
- Modify: `fuzz_test.go`

- [ ] **Step 1: Write the new fuzz target**

Append to `fuzz_test.go`:

```go
// FuzzLoweredDocument extends FuzzDocument with a lowering step. It
// asserts that for every input which parses+checks cleanly:
//
//	Convert(Lower(checked)) → format → reparse → recheck → strip-compare
//
// produces an IR equivalent to the lowered IR. This pins the round-trip
// invariant for lowered output: every IR shape any pass produces must be
// expressible in valid AST.
func FuzzLoweredDocument(f *testing.F) {
	for _, src := range loadTestdataSeeds() {
		f.Add(src)
	}
	caps := lower.Caps{
		NoReactivity:  true,
		NoDeclarative: true,
	}
	f.Fuzz(func(t *testing.T, src string) {
		doc1, ok := safeParseDoc(src)
		if !ok {
			t.Skip("parse failed")
		}
		pkg1, diags := checker.Check(doc1, &checker.Config{IsMain: true})
		if hasError(diags) {
			t.Skip("check failed")
		}
		if len(pkg1.Imports) > 0 {
			// Imports require a resolver; skip per the FuzzDocument convention.
			t.Skip("imports require resolver")
		}
		if err := lower.Lower(pkg1, caps, lower.Options{}); err != nil {
			t.Fatalf("lower: %v", err)
		}
		convDoc := ir.Convert(pkg1)
		convSrc := parser.Format(convDoc)
		convReparsed, err := parser.Parse("fuzz.lower.sngl", []byte(convSrc))
		if err != nil {
			t.Fatalf("lowered ir.Convert output failed to parse: %v\n--- generated ---\n%s", err, convSrc)
		}
		_, convDiags := checker.Check(convReparsed, &checker.Config{IsMain: true})
		if hasError(convDiags) {
			t.Fatalf("lowered ir.Convert output failed to type-check:\n--- generated ---\n%s\n--- diags ---\n%s",
				convSrc, joinDiags(convDiags))
		}
	})
}
```

- [ ] **Step 2: Run the fuzz target's seed corpus**

Run: `go test -run=FuzzLoweredDocument ./...`
Expected: PASS (seed corpus only; no actual fuzzing).

If failures occur, they identify lowering output that doesn't round-trip. Investigate each: most likely culprit is `passLambda`'s lifted closures or `passAsyncReactive`'s kicker emission. Document each finding in a follow-up issue per spec §4 of the Next steps; do not fix them in this plan (they're explicit non-goals).

If failures look like a regression from Task 4, debug now: a failure here means the intrinsic refactor produced AST that doesn't round-trip after all. Confirm by `git stash` + re-running; if the failure goes away, the refactor missed a callsite.

- [ ] **Step 3: Run a short fuzz session to sanity-check**

Run: `go test -run=FuzzLoweredDocument -fuzz=FuzzLoweredDocument -fuzztime=30s ./...`
Expected: no new failures.

- [ ] **Step 4: Commit**

```bash
git add fuzz_test.go
git commit -m "test: add FuzzLoweredDocument for post-lowering round-trip

Asserts Convert(Lower(checked)) re-parses + re-checks. Currently
exercised with {NoReactivity, NoDeclarative} caps; future cap
additions extend this set.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase 2: Structural reactivity in `passReactivity`

### Task 6: Add the `reactiveSlot` record and slot ID counter

**Files:**
- Modify: `internal/lower/reactivity.go`

- [ ] **Step 1: Extend `reactivityState`**

In `internal/lower/reactivity.go`, replace the `reactivityState` struct definition near line 24 with:

```go
// reactivityState carries the analysis built up before mutation injection.
type reactivityState struct {
	pkg          *ir.Package
	reactiveVars map[*ir.Var]bool
	reverseDeps  map[*ir.Var][]reactiveProp
	reverseSlots map[*ir.Var][]reactiveSlot
	intrinsics   map[string]*ir.Func // CreateNode, AppendChild, RemoveChild
	idCounter    int
	slotCounter  int
	// slot synthesis owner: the *ir.Component or *ir.Window whose stmt body we're
	// currently walking, so synthesized slot Vars/Funcs get attached to the
	// right scope.
	owner reactivityOwner
}

// reactiveSlot records a per-If/per-For reactive dep. SlotID names the
// synthetic __slot<N>; ParentRefName is the IR-level identifier referring
// to the parent container at the slot's original source position.
type reactiveSlot struct {
	SlotID        string
	ParentRefName string   // "" when the slot sits at the component/window body's top level
	GenFunc       *ir.Func // the __renderSlot<N> Func — populated in Phase 2.4
}

// reactivityOwner is the closest enclosing scope that owns synthesized
// Vars/Funcs. Either a *ir.Component or *ir.Window.
type reactivityOwner interface {
	addVar(v *ir.Var)
	addFunc(f *ir.Func)
}

type compOwner struct{ c *ir.Component }

func (o compOwner) addVar(v *ir.Var)   { o.c.Vars = append(o.c.Vars, v) }
func (o compOwner) addFunc(f *ir.Func) { o.c.Funcs = append(o.c.Funcs, f) }

type windowOwner struct{ w *ir.Window }

func (o windowOwner) addVar(v *ir.Var)   { o.w.Vars = append(o.w.Vars, v) }
func (o windowOwner) addFunc(f *ir.Func) { o.w.Funcs = append(o.w.Funcs, f) }
```

Update `lowerReactivity`:

```go
func lowerReactivity(pkg *ir.Package, _ Caps) error {
	if pkg == nil {
		return nil
	}
	st := &reactivityState{
		pkg:          pkg,
		reactiveVars: collectReactiveVars(pkg),
		reverseDeps:  make(map[*ir.Var][]reactiveProp),
		reverseSlots: make(map[*ir.Var][]reactiveSlot),
		intrinsics:   make(map[string]*ir.Func),
	}
	for _, def := range ir.LowerIntrinsics {
		st.intrinsics[def.Name] = &ir.Func{
			Name:      def.Name,
			Intrinsic: def.Name,
			Params:    def.Params,
			Return:    def.Return,
		}
	}
	// Pass 1: collect reverse deps per owner scope.
	for _, comp := range pkg.Components {
		st.owner = compOwner{comp}
		st.collectFromStmts(comp.Body)
	}
	for _, w := range pkg.Windows {
		st.owner = windowOwner{w}
		st.collectFromStmts(w.Body)
	}
	// Pass 2: synthesize __renderSlot<N> Funcs and rewrite If/For sites.
	for _, comp := range pkg.Components {
		st.owner = compOwner{comp}
		comp.Body = st.rewriteAndInject(comp.Body)
	}
	for _, w := range pkg.Windows {
		st.owner = windowOwner{w}
		w.Body = st.rewriteAndInject(w.Body)
	}
	return nil
}
```

(The old single-pass `walkPackage` approach gives way to per-owner traversal so synthesized Vars/Funcs land on the right scope. `rewriteAndInject` will be implemented in Tasks 7-11.)

- [ ] **Step 2: Stub `rewriteAndInject` and the new collector to keep the package compiling**

Append minimal stubs at the bottom of `internal/lower/reactivity.go` (we'll fill them out in subsequent tasks):

```go
// rewriteAndInject is the unified pass-2 walk. Tasks 7-11 build it out.
// For now it delegates to the existing injectIntoStmts so behavior is
// unchanged for prop-only reactivity.
func (st *reactivityState) rewriteAndInject(stmts []ir.Stmt) []ir.Stmt {
	return st.injectIntoStmts(stmts)
}
```

Update `collectFromStmt` to no-op for `*ir.Window` (windows are now seeded by the top-level loop in `lowerReactivity`, so we don't want the recursive walker to re-enter them):

```go
case *ir.Window:
	// handled by top-level loop in lowerReactivity
```

- [ ] **Step 3: Run lower tests to confirm parity**

Run: `go test ./internal/lower/...`
Expected: PASS (no behavior change yet).

- [ ] **Step 4: Commit**

```bash
git add internal/lower/reactivity.go
git commit -m "lower(reactivity): add slot tracking scaffold

Extends reactivityState with reverseSlots, intrinsics, and an owner
abstraction so synthesized slot Vars/Funcs land on the right
component/window scope. No behavior change — collection and injection
still go through the prop-only path.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: Collect structural deps for reactive `If` / `For`

**Files:**
- Modify: `internal/lower/reactivity.go`

- [ ] **Step 1: Add `collectFromIf` and `collectFromFor`**

In `internal/lower/reactivity.go`, extend `collectFromStmt` to record slot deps:

```go
func (st *reactivityState) collectFromStmt(s ir.Stmt) {
	switch n := s.(type) {
	case *ir.NodeInst:
		st.collectFromNode(n)
	case *ir.If:
		st.collectFromIf(n)
		st.collectFromStmts(n.Body)
		st.collectFromStmts(n.Else)
	case *ir.For:
		st.collectFromFor(n)
		st.collectFromStmts(n.Body)
		st.collectFromStmts(n.Else)
	case *ir.PlatformFilter:
		st.collectFromStmts(n.Body)
	case *ir.SlotInst:
		st.collectFromStmts(n.Children)
	case *ir.ErrorBoundary:
		st.collectFromStmts(n.Children)
	}
}

func (st *reactivityState) collectFromIf(n *ir.If) {
	deps := st.exprDeps(n.Cond)
	if len(deps) == 0 {
		return
	}
	slot := reactiveSlot{SlotID: st.freshSlotID()}
	for v := range deps {
		st.reverseSlots[v] = append(st.reverseSlots[v], slot)
	}
	// Pin the slot on the If so pass-2 can find it.
	n.LoweredSlotID = slot.SlotID
}

func (st *reactivityState) collectFromFor(n *ir.For) {
	deps := st.exprDeps(n.Iter)
	if len(deps) == 0 {
		return
	}
	slot := reactiveSlot{SlotID: st.freshSlotID()}
	for v := range deps {
		st.reverseSlots[v] = append(st.reverseSlots[v], slot)
	}
	n.LoweredSlotID = slot.SlotID
}

func (st *reactivityState) freshSlotID() string {
	id := "__slot" + strconv.Itoa(st.slotCounter)
	st.slotCounter++
	return id
}

// renderFuncName maps a slot ID ("__slot0") to its generator-Func name
// ("__renderSlot0"). Centralized so collection and rewrite agree.
func renderFuncName(slotID string) string {
	return "__renderS" + strings.TrimPrefix(slotID, "__s")
}
```

- [ ] **Step 2: Add the `LoweredSlotID` field on `ir.If` and `ir.For`**

In `ir/ir.go` (find the `If` and `For` struct definitions), add:

```go
// LoweredSlotID is set by passReactivity to the slot ID assigned when
// the If/For's Cond/Iter depends on a reactive Var. "" when the
// construct is not reactive. Pass-2 of passReactivity rewrites these
// into CallStmt __renderSlot<N>() invocations.
LoweredSlotID string `json:"-"`
```

- [ ] **Step 3: Write a unit test**

Create `internal/lower/reactivity_structural_test.go`:

```go
package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

func TestCollectSlotForReactiveIf(t *testing.T) {
	src := `
component main {
    var visible bool = true
    if visible {
        text(value="hi")
    }
}
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}); err != nil {
		t.Fatal(err)
	}
	// Find the If in the lowered body.
	comp := pkg.Components[0]
	for _, s := range comp.Body {
		if ifNode, ok := s.(*ir.If); ok {
			if ifNode.LoweredSlotID == "" {
				t.Errorf("expected reactive If to have LoweredSlotID set")
			}
			return
		}
	}
	t.Errorf("no If found in lowered body")
}

func TestCollectSlotForReactiveFor(t *testing.T) {
	src := `
component main {
    var items list<int> = [1, 2, 3]
    for item = items {
        text(value=string(item))
    }
}
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}); err != nil {
		t.Fatal(err)
	}
	comp := pkg.Components[0]
	for _, s := range comp.Body {
		if forNode, ok := s.(*ir.For); ok {
			if forNode.LoweredSlotID == "" {
				t.Errorf("expected reactive For to have LoweredSlotID set")
			}
			return
		}
	}
	t.Errorf("no For found in lowered body")
}
```

(Add `"git.duckfam.us/jonathan/sngl/ir"` to the imports.)

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/lower/ -run TestCollectSlot -v`
Expected: PASS. (Tests should pass even though `rewriteAndInject` still defers to `injectIntoStmts`; `LoweredSlotID` gets set during the pass-1 collection step.)

- [ ] **Step 5: Run full lower tests to confirm no regressions**

Run: `go test ./internal/lower/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/lower/reactivity.go internal/lower/reactivity_structural_test.go ir/ir.go
git commit -m "lower(reactivity): collect structural deps for reactive If/For

Pass-1 of passReactivity now records reverseSlots[var] entries for
every If.Cond and For.Iter that references a reactive Var, and pins a
LoweredSlotID onto the IR node. Pass-2 will use these to synthesize
__renderSlot<N> Funcs in upcoming tasks.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: Synthesize `__slot<N>` Vars

**Files:**
- Modify: `internal/lower/reactivity.go`

- [ ] **Step 1: Add `synthesizeSlotVar`**

Append to `internal/lower/reactivity.go`:

```go
// synthesizeSlotVar creates the per-slot `__slotN list<dyn>` Var and
// attaches it to the current owner. Idempotent — returns the existing
// Var if one was already created.
func (st *reactivityState) synthesizeSlotVar(slotID string) *ir.Var {
	if existing := st.findSlotVar(slotID); existing != nil {
		return existing
	}
	v := &ir.Var{
		Name: slotID,
		Type: ir.ListOf(ir.TypDyn),
		Init: &ir.ListLit{Type: ir.ListOf(ir.TypDyn), Elems: nil},
	}
	st.owner.addVar(v)
	return v
}

func (st *reactivityState) findSlotVar(name string) *ir.Var {
	switch o := st.owner.(type) {
	case compOwner:
		for _, v := range o.c.Vars {
			if v.Name == name {
				return v
			}
		}
	case windowOwner:
		for _, v := range o.w.Vars {
			if v.Name == name {
				return v
			}
		}
	}
	return nil
}
```

- [ ] **Step 2: Wire into `rewriteAndInject`**

Replace the stub:

```go
func (st *reactivityState) rewriteAndInject(stmts []ir.Stmt) []ir.Stmt {
	for _, slots := range st.reverseSlots {
		for _, slot := range slots {
			st.synthesizeSlotVar(slot.SlotID)
		}
	}
	return st.injectIntoStmts(stmts)
}
```

- [ ] **Step 3: Test slot var allocation**

Append to `internal/lower/reactivity_structural_test.go`:

```go
func TestSynthesizeSlotVar(t *testing.T) {
	src := `
component main {
    var visible bool = true
    if visible {
        text(value="hi")
    }
}
`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}); err != nil {
		t.Fatal(err)
	}
	comp := pkg.Components[0]
	var found bool
	for _, v := range comp.Vars {
		if v.Name == "__slot0" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected synthesized __slot0 var on component; got vars: %v", varNames(comp.Vars))
	}
}

func varNames(vs []*ir.Var) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.Name
	}
	return out
}
```

- [ ] **Step 4: Run the test**

Run: `go test ./internal/lower/ -run TestSynthesizeSlotVar -v`
Expected: PASS.

- [ ] **Step 5: Run full lower tests**

Run: `go test ./internal/lower/...`
Expected: PASS (existing tests untouched).

- [ ] **Step 6: Commit**

```bash
git add internal/lower/reactivity.go internal/lower/reactivity_structural_test.go
git commit -m "lower(reactivity): synthesize __slotN list<dyn> vars

For each reactive If/For collected in pass-1, allocate a private
list<dyn> on the owning component/window. The list will hold the
currently-mounted child refs once __renderSlotN Funcs are synthesized
in the next task.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: Synthesize `__renderSlot<N>` Funcs

**Files:**
- Modify: `internal/lower/reactivity.go`

**Background.** Each `__renderSlot<N>(parent dyn)` Func must:
1. Walk current `__slot<N>` entries and emit `lower.RemoveChild(parent, entry)` per entry.
2. Reset `__slot<N>` to empty.
3. Re-evaluate Cond (for If) or iterate Iter (for For).
4. For each child NodeInst in the body, emit the same create/setProp/attachHandler/appendChild sequence `passDeclarative.lowerNode` produces. Append the new child ref into `__slot<N>`.

To avoid duplicating `passDeclarative.lowerNode`, factor it into a shared helper.

- [ ] **Step 1: Extract `lowerNodeShared` from `passDeclarative`**

In `internal/lower/declarative.go`, rename the `lowerNode` method to `lowerNodeIntoStmts`, then add a package-level helper:

```go
// LowerNodeForSlot emits the same create/setProp/attachHandler/appendChild
// sequence passDeclarative produces for one NodeInst, but with `parentRef`
// supplied externally (so the slot generator can append to its passed-in
// parent rather than the original source-position parent). funcs is the
// owning Funcs slice for handler promotion. Returns (id, stmts) where id
// is the LocalVar name bound to the new node ref.
func LowerNodeForSlot(st *declarativeState, n *ir.NodeInst, parentID string, funcs *[]*ir.Func) (string, []ir.Stmt) {
	stmts := st.lowerNodeIntoStmts(n, funcs)
	if parentID != "" {
		stmts = append(stmts, &ir.CallStmt{
			Call: &ir.Call{
				Type:     ir.TypVoid,
				Receiver: lowerNSIdent(),
				Func:     st.intrinsics["AppendChild"],
				Args: []ir.CallArg{
					{Value: &ir.Ident{Name: parentID, Type: ir.TypDyn, IsElementRef: true}},
					{Value: &ir.Ident{Name: n.ID, Type: ir.TypDyn, IsElementRef: true}},
				},
			},
		})
	}
	return n.ID, stmts
}
```

(The existing `lowerNodeIntoStmts` already emits create+props+handlers+children for the subtree; the slot generator wants to attach the *top* of that subtree to a runtime-supplied parent rather than the source-position parent.)

- [ ] **Step 2: Allow `passReactivity` to construct a `declarativeState`**

Today `declarativeState` is private to `declarative.go`. Expose a constructor visible inside the `lower` package:

```go
// newDeclarativeStateForSlot constructs a declarativeState configured to
// share a slot generator's emission concerns. liftHandlers=false because
// slot bodies don't need closure capture — handlers attached during slot
// re-render are re-bound to the same Closure each time.
func newDeclarativeStateForSlot(pkg *ir.Package) *declarativeState {
	return newDeclarativeState(pkg, Caps{NoLambda: false})
}
```

- [ ] **Step 3: Add `synthesizeRenderSlotFunc` in `reactivity.go`**

```go
// synthesizeRenderSlotFunc generates the __renderSlotN(parent dyn) Func.
// Body:
//
//	for _, entry := __slotN { lower.RemoveChild(parent, entry) }
//	__slotN = []
//	<re-evaluate cond/iter and emit child create+append, pushing into __slotN>
//
// origStmts is the original If.Body / For.Body to render. cond is the If
// gate (nil for For). iter is the For iter (nil for If). key/value are
// the For loop binding names (zero for If).
func (st *reactivityState) synthesizeRenderSlotFunc(slotID string, cond ir.Expr, iter ir.Expr, key, value string, origBody, origElse []ir.Stmt) *ir.Func {
	parentParam := &ir.Param{Name: "parent", Type: ir.TypDyn}
	fn := &ir.Func{
		Name:   renderFuncName(slotID),
		Params: []*ir.Param{parentParam},
		Return: ir.TypVoid,
	}

	// 1. Teardown loop.
	entryVar := "__entry"
	teardown := &ir.For{
		Value: entryVar,
		Iter:  &ir.Ident{Name: slotID, Type: ir.ListOf(ir.TypDyn)},
		Body: []ir.Stmt{
			&ir.CallStmt{Call: &ir.Call{
				Type:     ir.TypVoid,
				Receiver: lowerNSIdent(),
				Func:     st.intrinsics["RemoveChild"],
				Args: []ir.CallArg{
					{Value: &ir.Ident{Name: parentParam.Name, Type: ir.TypDyn, Sym: parentParam}},
					{Value: &ir.Ident{Name: entryVar, Type: ir.TypDyn}},
				},
			}},
		},
	}

	// 2. Reset slot.
	reset := &ir.Assign{
		Target: &ir.Ident{Name: slotID, Type: ir.ListOf(ir.TypDyn)},
		Op:     ast.AssignSet,
		Value:  &ir.ListLit{Type: ir.ListOf(ir.TypDyn), Elems: nil},
	}

	// 3. Re-evaluate.
	declSt := newDeclarativeStateForSlot(st.pkg)
	body := st.renderSlotBody(declSt, parentParam.Name, slotID, cond, iter, key, value, origBody, origElse, &fn.Block)

	fn.Block = []ir.Stmt{teardown, reset}
	fn.Block = append(fn.Block, body...)
	return fn
}

// renderSlotBody emits the cond/iter-gated create+append sequence for the
// slot's children, with each created top-level NodeInst's ref pushed onto
// __slotN via ListPush.
func (st *reactivityState) renderSlotBody(declSt *declarativeState, parentName, slotID string, cond, iter ir.Expr, key, value string, origBody, origElse []ir.Stmt, funcs *[]ir.Stmt) []ir.Stmt {
	listPushFn := &ir.Func{
		Name:      "ListPush",
		Intrinsic: "ListPush",
		Params:    ir.LookupIntrinsic("ListPush").Params,
		Return:    ir.LookupIntrinsic("ListPush").Return,
	}
	pushToSlot := func(nodeID string) ir.Stmt {
		return &ir.Assign{
			Target: &ir.Ident{Name: slotID, Type: ir.ListOf(ir.TypDyn)},
			Op:     ast.AssignSet,
			Value: &ir.Call{
				Type:     ir.ListOf(ir.TypDyn),
				Receiver: &ir.Ident{Name: "stdlib"},
				Func:     listPushFn,
				Args: []ir.CallArg{
					{Value: &ir.Ident{Name: slotID, Type: ir.ListOf(ir.TypDyn)}},
					{Value: &ir.Ident{Name: nodeID, Type: ir.TypDyn, IsElementRef: true}},
				},
			},
		}
	}
	emitNodeAt := func(n *ir.NodeInst) []ir.Stmt {
		_, sub := LowerNodeForSlot(declSt, n, parentName, &st.pkg.Components[0].Funcs /* placeholder; see step 4 */)
		sub = append(sub, pushToSlot(n.ID))
		return sub
	}
	emitStmts := func(stmts []ir.Stmt) []ir.Stmt {
		var out []ir.Stmt
		for _, s := range stmts {
			if nodeInst, ok := s.(*ir.NodeInst); ok {
				out = append(out, emitNodeAt(nodeInst)...)
			} else {
				// Non-NodeInst stmts inside the slot body (e.g. nested If) — copy
				// through. They'll be handled by a recursive renderSlot if reactive,
				// or by passDeclarative otherwise.
				out = append(out, s)
			}
		}
		return out
	}

	if iter != nil {
		// For-loop slot.
		return []ir.Stmt{&ir.For{
			Key:   key,
			Value: value,
			Iter:  iter,
			Body:  emitStmts(origBody),
		}}
	}
	// If-slot.
	ifStmt := &ir.If{
		Cond: cond,
		Body: emitStmts(origBody),
	}
	if len(origElse) > 0 {
		ifStmt.Else = emitStmts(origElse)
	}
	return []ir.Stmt{ifStmt}
}
```

- [ ] **Step 4: Fix the `funcs` parameter**

The placeholder `&st.pkg.Components[0].Funcs` is wrong — it always targets the first component. Replace with the owner's funcs slice:

```go
// in renderSlotBody, replace the placeholder line:
emitNodeAt := func(n *ir.NodeInst) []ir.Stmt {
	var ownerFuncs *[]*ir.Func
	switch o := st.owner.(type) {
	case compOwner:
		ownerFuncs = &o.c.Funcs
	case windowOwner:
		ownerFuncs = &o.w.Funcs
	}
	_, sub := LowerNodeForSlot(declSt, n, parentName, ownerFuncs)
	sub = append(sub, pushToSlot(n.ID))
	return sub
}
```

- [ ] **Step 5: Attach generated Funcs to the owner**

In `rewriteAndInject`, after `synthesizeSlotVar`, synthesize the matching Func and remember it on the `reactiveSlot`:

```go
func (st *reactivityState) rewriteAndInject(stmts []ir.Stmt) []ir.Stmt {
	for v, slots := range st.reverseSlots {
		for i, slot := range slots {
			st.synthesizeSlotVar(slot.SlotID)
			fn := st.buildRenderSlotFor(slot.SlotID, stmts)
			if fn != nil {
				st.owner.addFunc(fn)
				slots[i].GenFunc = fn
			}
		}
		st.reverseSlots[v] = slots
	}
	return st.injectIntoStmts(stmts)
}

// buildRenderSlotFor walks stmts to find the If/For carrying slotID, then
// constructs the corresponding __renderSlotN Func. Returns nil if the
// matching node has already been rewritten (idempotent).
func (st *reactivityState) buildRenderSlotFor(slotID string, stmts []ir.Stmt) *ir.Func {
	var fn *ir.Func
	var walk func([]ir.Stmt)
	walk = func(ss []ir.Stmt) {
		for _, s := range ss {
			switch n := s.(type) {
			case *ir.If:
				if n.LoweredSlotID == slotID && fn == nil {
					fn = st.synthesizeRenderSlotFunc(slotID, n.Cond, nil, "", "", n.Body, n.Else)
					return
				}
				walk(n.Body)
				walk(n.Else)
			case *ir.For:
				if n.LoweredSlotID == slotID && fn == nil {
					fn = st.synthesizeRenderSlotFunc(slotID, nil, n.Iter, n.Key, n.Value, n.Body, n.Else)
					return
				}
				walk(n.Body)
				walk(n.Else)
			case *ir.NodeInst:
				walk(n.Children)
				for _, h := range n.Handlers {
					if h.Func != nil {
						walk(h.Func.Block)
					}
				}
			}
		}
	}
	walk(stmts)
	return fn
}
```

- [ ] **Step 6: Test that the Func appears on the owner**

Extend `internal/lower/reactivity_structural_test.go`:

```go
func TestSynthesizeRenderSlotFunc(t *testing.T) {
	src := `
component main {
    var visible bool = true
    if visible {
        text(value="hi")
    }
}
`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}); err != nil {
		t.Fatal(err)
	}
	comp := pkg.Components[0]
	var found *ir.Func
	for _, f := range comp.Funcs {
		if f.Name == "__renderSlot0" {
			found = f
			break
		}
	}
	if found == nil {
		t.Fatalf("expected __renderSlot0 Func on component; got: %v", funcNames(comp.Funcs))
	}
	if len(found.Params) != 1 || found.Params[0].Name != "parent" {
		t.Errorf("expected __renderSlot0(parent dyn); got params %v", found.Params)
	}
}

func funcNames(fs []*ir.Func) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Name
	}
	return out
}
```

- [ ] **Step 7: Run the test**

Run: `go test ./internal/lower/ -run TestSynthesizeRenderSlotFunc -v`
Expected: PASS.

- [ ] **Step 8: Run full lower tests**

Run: `go test ./internal/lower/...`
Expected: PASS. (The slot Func now exists on the component, but the body of the original `If` still hasn't been replaced with a `CallStmt` — that's Task 10. Existing platform code never reads `__renderSlot*` so the new Func is harmless.)

- [ ] **Step 9: Commit**

```bash
git add internal/lower/declarative.go internal/lower/reactivity.go internal/lower/reactivity_structural_test.go
git commit -m "lower(reactivity): synthesize __renderSlotN generator Funcs

Builds a __renderSlotN(parent dyn) Func per reactive If/For. Body
tears down current __slotN entries via lower.RemoveChild,
re-evaluates the cond/iter, and emits the same create/append
sequence passDeclarative would emit — with each created child ref
pushed back into __slotN.

Shared via newDeclarativeStateForSlot + LowerNodeForSlot so the slot
generator and passDeclarative agree on the emission shape.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 10: Replace reactive `If`/`For` with `CallStmt __renderSlot<N>(parent)`

**Files:**
- Modify: `internal/lower/reactivity.go`

- [ ] **Step 1: Add a rewriting walker**

Add a new walker that runs **before** `injectIntoStmts` (because the spliced updaters reference the new CallStmts):

```go
// rewriteReactiveStructures replaces every *ir.If/*ir.For carrying a
// LoweredSlotID with a CallStmt to its slot generator. The parent
// reference is the synthetic ident for the enclosing NodeInst — passed
// in via the recursion stack.
func (st *reactivityState) rewriteReactiveStructures(stmts []ir.Stmt, parentRef ir.Expr) []ir.Stmt {
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.If:
			if n.LoweredSlotID != "" {
				out = append(out, st.slotCall(n.LoweredSlotID, parentRef))
				continue
			}
			n.Body = st.rewriteReactiveStructures(n.Body, parentRef)
			n.Else = st.rewriteReactiveStructures(n.Else, parentRef)
		case *ir.For:
			if n.LoweredSlotID != "" {
				out = append(out, st.slotCall(n.LoweredSlotID, parentRef))
				continue
			}
			n.Body = st.rewriteReactiveStructures(n.Body, parentRef)
			n.Else = st.rewriteReactiveStructures(n.Else, parentRef)
		case *ir.NodeInst:
			pref := &ir.Ident{Name: n.ID, Type: ir.TypDyn, IsElementRef: true}
			n.Children = st.rewriteReactiveStructures(n.Children, pref)
			for _, h := range n.Handlers {
				if h.Func != nil {
					h.Func.Block = st.rewriteReactiveStructures(h.Func.Block, parentRef)
				}
			}
		}
		out = append(out, s)
	}
	return out
}

// slotCall emits CallStmt __renderSlotN(parentRef).
func (st *reactivityState) slotCall(slotID string, parentRef ir.Expr) *ir.CallStmt {
	if parentRef == nil {
		// Top-level reactive If/For: use the "root" sentinel. Platforms
		// translate this to their root container reference.
		parentRef = &ir.Ident{Name: "__root", Type: ir.TypDyn, IsElementRef: true}
	}
	fn := st.lookupSlotFunc(slotID)
	return &ir.CallStmt{Call: &ir.Call{
		Type: ir.TypVoid,
		Func: fn,
		Args: []ir.CallArg{{Value: parentRef}},
	}}
}

func (st *reactivityState) lookupSlotFunc(slotID string) *ir.Func {
	want := renderFuncName(slotID)
	switch o := st.owner.(type) {
	case compOwner:
		for _, f := range o.c.Funcs {
			if f.Name == want {
				return f
			}
		}
	case windowOwner:
		for _, f := range o.w.Funcs {
			if f.Name == want {
				return f
			}
		}
	}
	return nil
}
```

- [ ] **Step 2: Wire `rewriteReactiveStructures` into the pass-2 entry point**

Replace the `rewriteAndInject` body:

```go
func (st *reactivityState) rewriteAndInject(stmts []ir.Stmt) []ir.Stmt {
	for v, slots := range st.reverseSlots {
		for i, slot := range slots {
			st.synthesizeSlotVar(slot.SlotID)
			fn := st.buildRenderSlotFor(slot.SlotID, stmts)
			if fn != nil {
				st.owner.addFunc(fn)
				slots[i].GenFunc = fn
			}
		}
		st.reverseSlots[v] = slots
	}
	stmts = st.rewriteReactiveStructures(stmts, nil)
	return st.injectIntoStmts(stmts)
}
```

- [ ] **Step 3: Test that the original `If` is replaced**

Append to `internal/lower/reactivity_structural_test.go`:

```go
func TestReactiveIfReplacedByCallStmt(t *testing.T) {
	src := `
component main {
    var visible bool = true
    if visible {
        text(value="hi")
    }
}
`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}); err != nil {
		t.Fatal(err)
	}
	comp := pkg.Components[0]
	for _, s := range comp.Body {
		if _, ok := s.(*ir.If); ok {
			t.Errorf("reactive If was not rewritten out of component body")
			return
		}
	}
	// Look for the synthesized CallStmt __renderSlot0.
	var found bool
	for _, s := range comp.Body {
		if cs, ok := s.(*ir.CallStmt); ok && cs.Call != nil && cs.Call.Func != nil && cs.Call.Func.Name == "__renderSlot0" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected CallStmt __renderSlot0 in component body; got %d stmts", len(comp.Body))
	}
}
```

- [ ] **Step 4: Run the test**

Run: `go test ./internal/lower/ -run TestReactiveIfReplacedByCallStmt -v`
Expected: PASS.

- [ ] **Step 5: Run full lower tests**

Run: `go test ./internal/lower/...`
Expected: existing prop-only tests still pass. If any platform-coupled test fails, isolate it and verify the failure is due to a slot rewrite the platform doesn't yet know how to consume — that's expected and will be handled in Plans B/C/D. Note these in the commit message.

- [ ] **Step 6: Commit**

```bash
git add internal/lower/reactivity.go internal/lower/reactivity_structural_test.go
git commit -m "lower(reactivity): rewrite reactive If/For as __renderSlotN calls

Pass-2 of passReactivity now replaces every *ir.If/*ir.For carrying a
LoweredSlotID with a CallStmt to the slot's generator Func. Initial
render still happens in source position because the CallStmt sits where
the If/For did.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 11: Splice `CallStmt __renderSlot<N>(parent)` after each tracked-dep mutation

**Files:**
- Modify: `internal/lower/reactivity.go`

- [ ] **Step 1: Extend `updatersFor`**

In `internal/lower/reactivity.go`, replace `updatersFor`:

```go
func (st *reactivityState) updatersFor(s ir.Stmt) []ir.Stmt {
	a, ok := s.(*ir.Assign)
	if !ok {
		return nil
	}
	v, fieldRewrite := st.assignTargetVar(a.Target)
	if v == nil {
		return nil
	}
	var out []ir.Stmt
	// Prop updaters (existing behavior).
	for _, p := range st.reverseDeps[v] {
		value := p.Expr
		if fieldRewrite != nil {
			value = rewriteIdentsToCaptures(value, fieldRewrite)
		}
		out = append(out, &ir.Assign{
			Target: &ir.Select{
				Type:    ir.TypDyn,
				Operand: &ir.Ident{Name: p.NodeID, Type: ir.TypDyn, IsElementRef: true},
				Field:   p.Key,
			},
			Op:    ast.AssignSet,
			Value: value,
		})
	}
	// Structural updaters (new).
	for _, slot := range st.reverseSlots[v] {
		if slot.GenFunc == nil {
			continue
		}
		out = append(out, &ir.CallStmt{Call: &ir.Call{
			Type: ir.TypVoid,
			Func: slot.GenFunc,
			Args: []ir.CallArg{{Value: &ir.Ident{Name: "__root", Type: ir.TypDyn, IsElementRef: true}}},
		}})
	}
	return out
}
```

(For now slot updaters always pass `__root`. Capturing the original parent-ref per-slot is a refinement; reserve a follow-up.)

- [ ] **Step 2: Test slot updaters get spliced after a mutation**

Append to `internal/lower/reactivity_structural_test.go`:

```go
func TestSlotUpdaterSplicedAfterMutation(t *testing.T) {
	src := `
component main {
    var visible bool = true
    button(@click { visible = !visible })
    if visible {
        text(value="hi")
    }
}
`
	doc, _ := parser.Parse("t.sngl", []byte(src))
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	if err := lowerReactivity(pkg, Caps{NoReactivity: true}); err != nil {
		t.Fatal(err)
	}
	comp := pkg.Components[0]
	// Walk the button's @click handler body and find a CallStmt __renderSlot0
	// spliced after the visible = !visible assignment.
	var found bool
	for _, s := range comp.Body {
		nodeInst, ok := s.(*ir.NodeInst)
		if !ok {
			continue
		}
		for _, h := range nodeInst.Handlers {
			if h.Name != "click" || h.Func == nil {
				continue
			}
			for _, b := range h.Func.Block {
				if cs, ok := b.(*ir.CallStmt); ok && cs.Call.Func != nil && cs.Call.Func.Name == "__renderSlot0" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Errorf("expected CallStmt __renderSlot0 spliced after visible mutation")
	}
}
```

- [ ] **Step 3: Run the test**

Run: `go test ./internal/lower/ -run TestSlotUpdaterSplicedAfterMutation -v`
Expected: PASS.

- [ ] **Step 4: Run full lower test suite + verify existing tests still pass**

Run: `go test ./internal/lower/...`
Expected: PASS. (Same caveat as Task 10: if a platform-coupled test fails, isolate and note.)

- [ ] **Step 5: Commit**

```bash
git add internal/lower/reactivity.go internal/lower/reactivity_structural_test.go
git commit -m "lower(reactivity): splice __renderSlotN calls after mutations

updatersFor now emits CallStmt __renderSlotN(__root) for every
structural dep tracked in reverseSlots, alongside the existing prop
Assign splicing. After this, any mutation to a tracked Var
re-renders both its dependent props AND any if/for whose
cond/iter referenced it.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 12: End-to-end golden: reactive `if`

**Files:**
- Create: `internal/lower/testdata/structural_if.txtar`

- [ ] **Step 1: Author the input**

Create `internal/lower/testdata/structural_if.txtar`:

```
caps: NoReactivity
-- input.sngl --
component main {
    var visible bool = true
    button(text="toggle", @click { visible = !visible })
    if visible {
        text(value="hi")
    }
}
-- expected.sngl --
```

- [ ] **Step 2: Run the golden test in update mode**

Run: `go test ./internal/lower/ -run TestLower/structural_if -update`
Expected: PASS, with `expected.sngl` populated.

- [ ] **Step 3: Manually inspect the expected output**

Run: `cat internal/lower/testdata/structural_if.txtar`

Verify the expected output shows:
- `var __slot0 list<dyn> = []` on the component
- `func __renderSlot0(parent dyn) void { ... }` containing teardown loop + `if visible { ... }`
- The original `if visible { ... }` block at the top level replaced with `__renderSlot0(__root)`
- Inside the button's `@click` handler, both `visible = !visible` and a spliced `__renderSlot0(__root)` call

If anything looks wrong, fix the implementation (most likely in Tasks 9 or 10) before continuing.

- [ ] **Step 4: Run the golden test in verify mode**

Run: `go test ./internal/lower/ -run TestLower/structural_if -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/lower/testdata/structural_if.txtar
git commit -m "lower(test): golden for reactive-if structural lowering

Asserts the full lowered shape: __slot0 var, __renderSlot0 generator
Func, top-level CallStmt at source position, and the spliced
re-render call after the visible mutation in @click.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 13: End-to-end golden: reactive `for`

**Files:**
- Create: `internal/lower/testdata/structural_for.txtar`

- [ ] **Step 1: Author the input**

Create `internal/lower/testdata/structural_for.txtar`:

```
caps: NoReactivity
-- input.sngl --
component main {
    var items list<int> = [1, 2, 3]
    button(text="add", @click { items = ListPush(items, 4) })
    for item = items {
        text(value=string(item))
    }
}
-- expected.sngl --
```

- [ ] **Step 2: Run in update mode**

Run: `go test ./internal/lower/ -run TestLower/structural_for -update`
Expected: PASS.

- [ ] **Step 3: Inspect**

Run: `cat internal/lower/testdata/structural_for.txtar`

Verify:
- `var __slot0 list<dyn> = []`
- `func __renderSlot0(parent dyn) void` body iterates `items` and pushes to `__slot0`
- Original for-loop replaced with `__renderSlot0(__root)`
- Splice in `@click` handler after `items = ...`

Fix if needed.

- [ ] **Step 4: Verify**

Run: `go test ./internal/lower/ -run TestLower/structural_for -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/lower/testdata/structural_for.txtar
git commit -m "lower(test): golden for reactive-for structural lowering

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 14: End-to-end golden: mixed prop + structural reactivity

**Files:**
- Create: `internal/lower/testdata/structural_mixed.txtar`

- [ ] **Step 1: Author the input**

Create `internal/lower/testdata/structural_mixed.txtar`:

```
caps: NoReactivity
-- input.sngl --
component main {
    var visible bool = true
    var name string = "World"
    button(text="toggle", @click { visible = !visible })
    text(value="Hello, {name}!")
    if visible {
        text(value="bye")
    }
}
-- expected.sngl --
```

- [ ] **Step 2: Update + inspect + verify (same pattern as previous tasks)**

Run: `go test ./internal/lower/ -run TestLower/structural_mixed -update`
Run: `cat internal/lower/testdata/structural_mixed.txtar`
Verify:
- Top-level text node `Hello, {name}!` got a prop-updater splice from `name` mutations (no name mutation here, so just the original prop assign).
- Reactive `if visible` got the slot rewrite.
- The button's `@click` handler splices `__renderSlot0(__root)` after `visible = !visible`, but NOT after any name-mutation (there is none) — confirms prop and slot deps are routed separately.

Run: `go test ./internal/lower/ -run TestLower/structural_mixed -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/lower/testdata/structural_mixed.txtar
git commit -m "lower(test): golden for mixed prop+structural reactivity

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 15: Verify full test suite + round-trip fuzz

- [ ] **Step 1: Run full test suite via the project's verify tool**

Run: `go tool verify`
Expected: PASS — or, if pre-existing failures from prior unrelated work persist, the count must not increase relative to the pre-plan baseline. Document baseline before starting if needed.

If new failures appear:
- A failure inside `internal/lower` is a regression in this plan — debug and fix before continuing.
- A failure inside `codegen/platform/*` is *expected* if the platform's tests exercise reactive if/for: the platform doesn't yet know to call `__renderSlot<N>`. Note each in the commit message and treat as scheduled work in Plans B/C/D.

- [ ] **Step 2: Run the lowered fuzz target seed corpus once more**

Run: `go test -run=FuzzLoweredDocument ./...`
Expected: PASS.

- [ ] **Step 3: Run a 60s fuzz pass**

Run: `go test -run=FuzzLoweredDocument -fuzz=FuzzLoweredDocument -fuzztime=60s ./...`
Expected: no failures.

If failures appear, examine the failing input. If it surfaces a pre-existing latent round-trip break (per spec §4 Next steps), record it as a finding and continue. If it surfaces a new break introduced by this plan, fix before committing.

- [ ] **Step 4: Commit (optional, only if you fixed anything from steps 1-3)**

---

## Phase 3: Shared `intrinsic_walker.go` scaffold

### Task 16: Define the `IntrinsicTranslator` interface

**Files:**
- Create: `codegen/intrinsic_walker.go`

- [ ] **Step 1: Write the file**

```go
package codegen

import "git.duckfam.us/jonathan/sngl/ir"

// IntrinsicTranslator is implemented by codegen platforms that consume
// lowered IR (post-passReactivity, optionally post-passDeclarative).
// Each method emits target-language source for one lowered IR shape.
//
// All methods are pure functions of their arguments — translators must
// not retain state across calls except through caller-managed
// per-component context.
type IntrinsicTranslator interface {
	// OnCreateNode emits a statement binding `id` to a fresh widget of
	// the given tag. e.g. fyne: "m.label0 := widget.NewLabel(\"\")".
	OnCreateNode(id, tag string) string

	// OnAppendChild emits a statement that adds `child` to `parent`.
	OnAppendChild(parent, child string) string

	// OnRemoveChild emits a statement that removes `child` from `parent`.
	OnRemoveChild(parent, child string) string

	// OnAttachHandler emits a statement that wires `event` on `node` to
	// `handlerRef` (a target-language expression evaluating to a callable).
	OnAttachHandler(node, event, handlerRef string) string

	// OnPropAssign emits a statement setting `nodeID.<prop>` to
	// `valueExpr`'s translation. The translator knows how to render
	// expressions in its target language.
	OnPropAssign(nodeID, prop string, valueExpr ir.Expr) string
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./codegen/...`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add codegen/intrinsic_walker.go
git commit -m "codegen: define IntrinsicTranslator interface

Per-platform contract for consuming lowered IR. Five methods, one per
distinct lowered shape: createNode, appendChild, removeChild,
attachHandler, and prop Assign on element refs.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 17: Implement `WalkLowered`

**Files:**
- Modify: `codegen/intrinsic_walker.go`
- Create: `codegen/intrinsic_walker_test.go`

- [ ] **Step 1: Write the failing test**

Create `codegen/intrinsic_walker_test.go`:

```go
package codegen

import (
	"fmt"
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// trace records each translator call as a string so tests can assert
// dispatch order without comparing source-language output.
type trace struct{ lines []string }

func (t *trace) OnCreateNode(id, tag string) string { t.add("create %s %s", id, tag); return "" }
func (t *trace) OnAppendChild(p, c string) string   { t.add("append %s %s", p, c); return "" }
func (t *trace) OnRemoveChild(p, c string) string   { t.add("remove %s %s", p, c); return "" }
func (t *trace) OnAttachHandler(n, e, h string) string {
	t.add("attach %s %s %s", n, e, h)
	return ""
}
func (t *trace) OnPropAssign(n, p string, _ ir.Expr) string {
	t.add("prop %s %s", n, p)
	return ""
}
func (t *trace) add(f string, args ...any) { t.lines = append(t.lines, fmt.Sprintf(f, args...)) }

func TestWalkLoweredDispatchesCreate(t *testing.T) {
	// Hand-build the IR for: var __n0 dyn = lower.CreateNode("vbox")
	createFunc := &ir.Func{Name: "CreateNode", Intrinsic: "CreateNode"}
	stmts := []ir.Stmt{
		&ir.LocalVar{
			Name: "__n0",
			Type: ir.TypDyn,
			Init: &ir.Call{
				Receiver: &ir.Ident{Name: "lower"},
				Func:     createFunc,
				Args:     []ir.CallArg{{Value: &ir.Literal{Type: ir.TypString, Raw: "vbox"}}},
			},
		},
	}
	tr := &trace{}
	WalkLowered(stmts, tr)
	got := strings.Join(tr.lines, "\n")
	if got != "create __n0 vbox" {
		t.Errorf("dispatch mismatch:\ngot:  %q\nwant: %q", got, "create __n0 vbox")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./codegen/ -run TestWalkLoweredDispatchesCreate -v`
Expected: FAIL with `WalkLowered: undefined`.

- [ ] **Step 3: Implement `WalkLowered`**

Append to `codegen/intrinsic_walker.go`:

```go
import "strings"

// WalkLowered iterates the lowered IR statement sequence and dispatches
// each known shape to t. Returns the concatenated emissions in source
// order; unrecognized statements are passed through as empty strings
// (a future pass should error on unknowns, but during the build-out
// phase platforms may not implement every dispatch yet).
func WalkLowered(stmts []ir.Stmt, t IntrinsicTranslator) string {
	var b strings.Builder
	for _, s := range stmts {
		b.WriteString(walkOne(s, t))
	}
	return b.String()
}

func walkOne(s ir.Stmt, t IntrinsicTranslator) string {
	switch n := s.(type) {
	case *ir.LocalVar:
		if call, ok := n.Init.(*ir.Call); ok && isLowerIntrinsic(call, "CreateNode") {
			tag, _ := extractStringLit(call.Args[0].Value)
			return t.OnCreateNode(n.Name, tag)
		}
	case *ir.CallStmt:
		if n.Call == nil {
			return ""
		}
		switch {
		case isLowerIntrinsic(n.Call, "AppendChild"):
			p, c := identName(n.Call.Args[0].Value), identName(n.Call.Args[1].Value)
			return t.OnAppendChild(p, c)
		case isLowerIntrinsic(n.Call, "RemoveChild"):
			p, c := identName(n.Call.Args[0].Value), identName(n.Call.Args[1].Value)
			return t.OnRemoveChild(p, c)
		case isLowerIntrinsic(n.Call, "AttachHandler"):
			node := identName(n.Call.Args[0].Value)
			evt, _ := extractStringLit(n.Call.Args[1].Value)
			hRef := identName(n.Call.Args[2].Value)
			return t.OnAttachHandler(node, evt, hRef)
		}
	case *ir.Assign:
		if sel, ok := n.Target.(*ir.Select); ok {
			if id, ok := sel.Operand.(*ir.Ident); ok && id.IsElementRef {
				return t.OnPropAssign(id.Name, sel.Field, n.Value)
			}
		}
	}
	return ""
}

func isLowerIntrinsic(call *ir.Call, name string) bool {
	return call != nil && call.Func != nil && call.Func.Intrinsic == name
}

func extractStringLit(e ir.Expr) (string, bool) {
	if l, ok := e.(*ir.Literal); ok && l.Type == ir.TypString {
		return l.Raw, true
	}
	return "", false
}

func identName(e ir.Expr) string {
	if id, ok := e.(*ir.Ident); ok {
		return id.Name
	}
	return ""
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./codegen/ -run TestWalkLoweredDispatchesCreate -v`
Expected: PASS.

- [ ] **Step 5: Add coverage for the remaining dispatches**

Append to `codegen/intrinsic_walker_test.go`:

```go
func TestWalkLoweredDispatchesAll(t *testing.T) {
	stmts := []ir.Stmt{
		// var __n0 dyn = lower.CreateNode("vbox")
		&ir.LocalVar{Name: "__n0", Type: ir.TypDyn, Init: &ir.Call{
			Func: &ir.Func{Name: "CreateNode", Intrinsic: "CreateNode"},
			Args: []ir.CallArg{{Value: &ir.Literal{Type: ir.TypString, Raw: "vbox"}}},
		}},
		// #__n0.text = "hi"
		&ir.Assign{
			Target: &ir.Select{
				Operand: &ir.Ident{Name: "__n0", IsElementRef: true},
				Field:   "text",
			},
			Value: &ir.Literal{Type: ir.TypString, Raw: "hi"},
		},
		// lower.AttachHandler(#__n0, "click", h)
		&ir.CallStmt{Call: &ir.Call{
			Func: &ir.Func{Name: "AttachHandler", Intrinsic: "AttachHandler"},
			Args: []ir.CallArg{
				{Value: &ir.Ident{Name: "__n0", IsElementRef: true}},
				{Value: &ir.Literal{Type: ir.TypString, Raw: "click"}},
				{Value: &ir.Ident{Name: "h"}},
			},
		}},
		// lower.AppendChild(#__parent, #__n0)
		&ir.CallStmt{Call: &ir.Call{
			Func: &ir.Func{Name: "AppendChild", Intrinsic: "AppendChild"},
			Args: []ir.CallArg{
				{Value: &ir.Ident{Name: "__parent", IsElementRef: true}},
				{Value: &ir.Ident{Name: "__n0", IsElementRef: true}},
			},
		}},
		// lower.RemoveChild(#__parent, #__n0)
		&ir.CallStmt{Call: &ir.Call{
			Func: &ir.Func{Name: "RemoveChild", Intrinsic: "RemoveChild"},
			Args: []ir.CallArg{
				{Value: &ir.Ident{Name: "__parent", IsElementRef: true}},
				{Value: &ir.Ident{Name: "__n0", IsElementRef: true}},
			},
		}},
	}
	tr := &trace{}
	WalkLowered(stmts, tr)
	want := []string{
		"create __n0 vbox",
		"prop __n0 text",
		"attach __n0 click h",
		"append __parent __n0",
		"remove __parent __n0",
	}
	if strings.Join(tr.lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("dispatch order mismatch:\ngot:\n%s\nwant:\n%s", strings.Join(tr.lines, "\n"), strings.Join(want, "\n"))
	}
}
```

- [ ] **Step 6: Run all walker tests**

Run: `go test ./codegen/ -run TestWalkLowered -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add codegen/intrinsic_walker.go codegen/intrinsic_walker_test.go
git commit -m "codegen: implement WalkLowered intrinsic dispatcher

Walks a lowered IR stmt sequence and dispatches each recognized shape
to an IntrinsicTranslator. Unrecognized stmts pass through silently
during build-out; later plans add stricter checks once every
platform implements the full set.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 18: Plan handoff

- [ ] **Step 1: Update spec migration-order notes**

Open `docs/superpowers/specs/2026-05-12-reactivity-lowering-consolidation-design.md` and append a single line at the end of the "Migration order" section:

```
> Plan A (this foundation) lands tasks 1-17 of these steps. Plans B/C/D
> cover per-platform conversion; Plan E covers final audit.
```

- [ ] **Step 2: Commit**

```bash
git add docs/superpowers/specs/2026-05-12-reactivity-lowering-consolidation-design.md
git commit -m "docs(spec): annotate plan-A scope on reactivity lowering design

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

- [ ] **Step 3: Push and signal handoff**

Run: `git push`
Expected: clean push.

State of the world after Plan A:
- The `lower` intrinsic namespace exists and round-trips.
- `passReactivity` owns both prop and structural reactivity. Reactive `if`/`for` produce `__slot<N>` Vars + `__renderSlot<N>` Funcs + spliced `CallStmt`s.
- Shared `codegen/intrinsic_walker.go` exists with a verified dispatch contract.
- No platform yet consumes the new shapes. Existing platform tests pass unchanged (modulo any newly-noted regressions in step 2 of Task 15, which Plan B/C/D will resolve as each platform converts).

Plan B (fyne conversion) is the next plan to write.
