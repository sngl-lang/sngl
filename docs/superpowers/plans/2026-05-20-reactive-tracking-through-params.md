# Reactive Tracking Through Parameters Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace SNGL's flat dep walker with a substituting walker that follows reads and writes through function parameters, keyed on `*ir.Var` pointers. Unblocks issue #75's component-method desugaring re-enable.

**Architecture:** New `depExtractor` type in `codegen/deps.go` walks an expression with a `bindings` map (param name → caller-side expr). At each `*ir.Ident` / `*ir.Select`, a `resolveVar` helper chases through bindings, implicit `this`, and component-namespace refs to return the `*ir.Var` ultimately accessed. Migrate `DepTracker`'s set fields from name-keyed `map[string]bool` to pointer-keyed `map[*ir.Var]struct{}`. Migrate every consumer (analysis, model, platforms, langs).

**Tech Stack:** Go. `codegen/deps.go` is the core target. Test driver: fixtures in `testdata/*.sngl` exercising reactivity through methods; unit tests in `codegen/deps_test.go`.

**Spec:** `docs/superpowers/specs/2026-05-20-reactive-tracking-through-params-design.md`

---

## File Structure

Files created or modified, by responsibility:

| File | Purpose | Action |
|---|---|---|
| `codegen/deps.go` | New `depExtractor` walker + `resolveVar`; migrated dep tracker types | Modify (large rewrite) |
| `codegen/deps_test.go` | Unit tests for the walker | Create |
| `codegen/analysis.go` | Populate pointer-keyed `ModelVars` alongside existing name-keyed maps | Modify |
| `codegen/model.go` | `Updater.Deps` and `Handler.Mutated` become `map[*ir.Var]struct{}`; `Dependent.DepVars` | Modify |
| `codegen/codegen.go` | `ExprScope`: keep name-keyed `ModelFields`/`ComputedFields` (used at emission), no new fields needed | No change |
| `codegen/lang/golang/translate_ir.go` | No change — uses name-keyed `scope.ModelFields` for emission | No change |
| `codegen/lang/javascript/translate_ir.go` | No change for the same reason | No change |
| `codegen/platform/html/html.go` | Updater/handler construction now consumes pointer-keyed deps from the walker, extracts names for emission via `.Name` | Modify |
| `codegen/platform/html/cdprunner.go` | Reads `Updater.Deps`; migrate set type | Modify |
| `codegen/platform/bubbletea/compiler_ir.go` | Re-render trigger sets; migrate set type | Modify |
| `codegen/platform/fyne/compiler_ir.go` | Mutation model setters; migrate set type | Modify |
| `codegen/platform/android/compiler_ir.go` | Recomposition triggers; migrate set type | Modify |
| `internal/lower/computed.go` | Already uses `*ir.Func` pointers; verify no name-keyed dep references | Audit |
| `testdata/test_reactivity_through_method.sngl` | Driver fixture | Create |
| `testdata/test_reactivity_extension_method.sngl` | Driver fixture | Create |
| `testdata/test_mutation_through_method.sngl` | Driver fixture | Create |
| `testdata/test_reactivity_two_components.sngl` | Driver fixture | Create |
| `testdata/test_reactivity_global_var.sngl` | Driver fixture | Create |

---

## Task 1: Driver fixtures (red baseline)

Per CLAUDE.md, fixtures come first. They will fail today: the existing tracker doesn't see reads through params, so reactive UIs don't update. Once the walker lands, these become pass.

**Files:**
- Create: `testdata/test_reactivity_through_method.sngl`
- Create: `testdata/test_reactivity_extension_method.sngl`
- Create: `testdata/test_mutation_through_method.sngl`
- Create: `testdata/test_reactivity_two_components.sngl`
- Create: `testdata/test_reactivity_global_var.sngl`

- [ ] **Step 1.1: Write `testdata/test_reactivity_through_method.sngl`**

```sngl
component main {
    var x = 5
    func double() => x * 2
    button #bump(text="bump", @click { x += 1 })
    text #out(value=string(double()))
}

func testReactiveMethodRead(t Test, c main) {
    t.assert(c.out.value == "10")
    c.bump.@click()
    t.assert(c.out.value == "12")
}
```

- [ ] **Step 1.2: Write `testdata/test_reactivity_extension_method.sngl`**

```sngl
component main {
    var x = 5
}

func main.double(this main) => this.x * 2

component root {
    main()
}

func testReactiveExtensionRead(t Test, c root) {
    var cm = c.children[0]
    t.assert(cm.double() == 10)
}
```

- [ ] **Step 1.3: Write `testdata/test_mutation_through_method.sngl`**

```sngl
component main {
    var x = 0
    func reset() { x = 0 }
    func bump() { x += 1 }
    button #bump(text="+", @click { bump() })
    button #reset(text="0", @click { reset() })
    text #out(value=string(x))
}

func testMutationThroughMethod(t Test, c main) {
    t.assert(c.out.value == "0")
    c.bump.@click()
    c.bump.@click()
    t.assert(c.out.value == "2")
    c.reset.@click()
    t.assert(c.out.value == "0")
}
```

- [ ] **Step 1.4: Write `testdata/test_reactivity_two_components.sngl`**

```sngl
// Two components with the same-named var prove pointer-keyed deps
// don't cross-subscribe.
component left {
    var x = 0
    button #bump(text="L", @click { x += 1 })
    text #out(value=string(x))
}

component right {
    var x = 100
    button #bump(text="R", @click { x += 1 })
    text #out(value=string(x))
}

component main {
    left()
    right()
}

func testTwoComponentsIsolated(t Test, c main) {
    var l = c.children[0]
    var r = c.children[1]
    t.assert(l.out.value == "0")
    t.assert(r.out.value == "100")
    l.bump.@click()
    t.assert(l.out.value == "1")
    t.assert(r.out.value == "100")
}
```

- [ ] **Step 1.5: Write `testdata/test_reactivity_global_var.sngl`**

```sngl
var theme = "light"

component card {
    text #out(value=theme)
}

component main {
    button #toggle(text="toggle", @click { theme = "dark" })
    card()
}

func testGlobalVarReactivity(t Test, c main) {
    var cd = c.children[1]
    t.assert(cd.out.value == "light")
    c.toggle.@click()
    t.assert(cd.out.value == "dark")
}
```

- [ ] **Step 1.6: Verify fixtures fail today**

Run: `go test ./codegen/platform/none/testrunner/... -count=1 -run 'TestRunFixtures/test_reactivity_through_method|TestRunFixtures/test_mutation_through_method|TestRunFixtures/test_reactivity_extension_method|TestRunFixtures/test_reactivity_two_components|TestRunFixtures/test_reactivity_global_var' -v`

Expected:
- `test_reactivity_through_method`: passes today (interp evaluates `double()` directly; doesn't go through the static dep tracker).
- `test_mutation_through_method`: depends on whether `reset`/`bump` desugar — currently component funcs DON'T desugar so they close over `x`; this may pass.
- Others: probably pass in interp.

Run also: `go test ./codegen/platform/html/... -count=1` — HTML codegen relies on the static dep tracker; some of the new fixtures may produce HTML that doesn't update properly, but these aren't in the HTML test set yet so the build still passes.

The point of this step is just to confirm the fixtures parse and check successfully. The reactivity-coverage gap they expose is visible in HTML output, not in the interp runner.

- [ ] **Step 1.7: Commit fixtures**

```bash
git add testdata/test_reactivity_through_method.sngl \
        testdata/test_reactivity_extension_method.sngl \
        testdata/test_mutation_through_method.sngl \
        testdata/test_reactivity_two_components.sngl \
        testdata/test_reactivity_global_var.sngl
git commit -m "testdata: fixtures for reactive tracking through parameters"
```

---

## Task 2: Unit-test scaffold for the walker

Drive the implementation via unit tests in `codegen/deps_test.go`. The tests build small `ir.Package` values by hand and assert which `*ir.Var`s end up in the dep set.

**Files:**
- Create: `codegen/deps_test.go`

- [ ] **Step 2.1: Create the test file skeleton**

Write `codegen/deps_test.go`:

```go
package codegen

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// makeVar constructs a minimal *ir.Var for testing.
func makeVar(name string) *ir.Var {
	return &ir.Var{Name: name, Type: &ir.Type{Kind: ir.TypeInt}}
}

// makeComp constructs a minimal *ir.Component for testing.
func makeComp(name string, vars ...*ir.Var) *ir.Component {
	return &ir.Component{Name: name, Vars: vars}
}

// makeFunc constructs a minimal *ir.Func for testing.
func makeFunc(name string, params []*ir.Param, body ir.Expr) *ir.Func {
	return &ir.Func{
		Name:   name,
		Params: params,
		Block:  []ir.Stmt{&ir.Return{Value: body}},
	}
}

// asSet returns a set with the given vars.
func asSet(vars ...*ir.Var) map[*ir.Var]struct{} {
	s := make(map[*ir.Var]struct{}, len(vars))
	for _, v := range vars {
		s[v] = struct{}{}
	}
	return s
}
```

- [ ] **Step 2.2: Add the first failing test (bare ident)**

Append:

```go
func TestExtractor_BareIdent(t *testing.T) {
	x := makeVar("x")
	comp := makeComp("main", x)
	pkg := &ir.Package{Components: []*ir.Component{comp}}
	dt := NewDepTrackerFromPkg(pkg)

	// Expression: `x`
	expr := &ir.Ident{Name: "x", Sym: x}

	got := dt.ExprDeps(comp, expr)
	want := asSet(x)
	if !equalVarSet(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func equalVarSet(a, b map[*ir.Var]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}
```

- [ ] **Step 2.3: Run; expect compile failures**

Run: `go test ./codegen/... -count=1 -run TestExtractor_BareIdent`

Expected: build error because `ExprDeps` currently returns `map[string]bool` and takes only `(expr)` not `(comp, expr)`. This drives the API migration.

- [ ] **Step 2.4: Commit the scaffold**

```bash
git add codegen/deps_test.go
git commit -m "codegen/deps: scaffold unit tests for substituting walker"
```

---

## Task 3: Migrate `DepTracker` type to pointer-keyed sets

**Files:**
- Modify: `codegen/deps.go` (top of file — type definition + constructors)

- [ ] **Step 3.1: Replace `DepTracker` type**

In `codegen/deps.go`, replace the existing `DepTracker` struct and `NewDepTracker`:

```go
// DepTracker tracks reactive dependencies between vars and computed funcs.
// All set fields are keyed on *ir.Var pointers (not names) so that
// same-named vars in different scopes don't collide.
type DepTracker struct {
	ModelVars     map[*ir.Var]struct{}
	ComputedFuncs map[*ir.Func]struct{}
	ComputedDeps  map[*ir.Func]map[*ir.Var]struct{}

	// Components is the list of all components in the package, used to
	// resolve `this.<field>` when walking inside a tracked context whose
	// owning component is known.
	Components []*ir.Component
}

// NewDepTracker creates a DepTracker. Sets are copied; the tracker doesn't
// hold references to caller-owned maps.
func NewDepTracker(modelVars map[*ir.Var]struct{}, computedFuncs map[*ir.Func]struct{}, computedDeps map[*ir.Func]map[*ir.Var]struct{}) *DepTracker {
	return &DepTracker{
		ModelVars:     modelVars,
		ComputedFuncs: computedFuncs,
		ComputedDeps:  computedDeps,
	}
}
```

- [ ] **Step 3.2: Migrate `NewDepTrackerFromPkg`**

Replace:

```go
func NewDepTrackerFromPkg(pkg *ir.Package) *DepTracker {
	model := make(map[*ir.Var]struct{})
	computed := make(map[*ir.Func]struct{})
	computedDeps := make(map[*ir.Func]map[*ir.Var]struct{})

	for _, v := range pkg.Vars {
		model[v] = struct{}{}
	}
	for _, f := range pkg.Funcs {
		if IsComputed(f) {
			computed[f] = struct{}{}
			deps := make(map[*ir.Var]struct{})
			for _, r := range f.Reads {
				deps[r] = struct{}{}
			}
			computedDeps[f] = deps
		}
	}
	for _, comp := range pkg.Components {
		for _, v := range comp.Vars {
			model[v] = struct{}{}
		}
		for _, f := range comp.Funcs {
			if IsComputed(f) {
				computed[f] = struct{}{}
				deps := make(map[*ir.Var]struct{})
				for _, r := range f.Reads {
					deps[r] = struct{}{}
				}
				computedDeps[f] = deps
			}
		}
	}
	return &DepTracker{
		ModelVars:     model,
		ComputedFuncs: computed,
		ComputedDeps:  computedDeps,
		Components:    pkg.Components,
	}
}
```

- [ ] **Step 3.3: Run go build; expect cascading failures**

Run: `go build ./...`
Expected: many errors at callers that reference `ModelFields`, `ComputedFields`, `ComputedDeps`. Subsequent tasks address each.

DO NOT commit yet. Build is broken until later tasks.

---

## Task 4: Implement the substituting walker

**Files:**
- Modify: `codegen/deps.go` (replace walk-* with `depExtractor`)

- [ ] **Step 4.1: Add `depExtractor` type and helpers**

Below the migrated `DepTracker` in `codegen/deps.go`, add:

```go
// depExtractor is the substituting walker. One per top-level entry into
// ExprDeps/MutatedFields; recurses into Call bodies with cloned frames.
type depExtractor struct {
	tracker       *DepTracker
	bindings      map[string]ir.Expr
	visited       map[*ir.Func]struct{}
	deps          map[*ir.Var]struct{}
	mutated       map[*ir.Var]struct{}
	implicitThis  *ir.Component
	tracking      bool

	// paramTypes captures the declared types of the params currently in
	// scope (matched up with bindings). Used to decide whether writes
	// through a param propagate (component/ref/list/map: yes; value
	// types: no).
	paramTypes    map[string]*ir.Type
}

// newExtractor creates a fresh extractor for a top-level entry.
func newExtractor(dt *DepTracker, currentComp *ir.Component, tracking bool) *depExtractor {
	return &depExtractor{
		tracker:      dt,
		bindings:     nil,
		visited:      make(map[*ir.Func]struct{}),
		deps:         make(map[*ir.Var]struct{}),
		mutated:      make(map[*ir.Var]struct{}),
		implicitThis: currentComp,
		tracking:     tracking,
	}
}

// cloneFrame returns a sibling extractor for a recursive call. Shares
// deps/mutated/visited; gets a fresh bindings/paramTypes frame.
func (w *depExtractor) cloneFrame() *depExtractor {
	return &depExtractor{
		tracker:      w.tracker,
		bindings:     nil,
		visited:      w.visited,
		deps:         w.deps,
		mutated:      w.mutated,
		implicitThis: w.implicitThis,
		tracking:     w.tracking,
	}
}
```

- [ ] **Step 4.2: Add `peelRoot` and `resolveVar`**

Append:

```go
// peelRoot walks left through Select/Index, returning the leftmost ident
// and the field name adjacent to it. For `this.p.x` returns (Ident{this}, "p").
func peelRoot(e ir.Expr) (root *ir.Ident, field string) {
	for {
		switch n := e.(type) {
		case *ir.Ident:
			return n, field
		case *ir.Select:
			field = n.Field
			e = n.Operand
		case *ir.Index:
			e = n.Operand
		default:
			return nil, ""
		}
	}
}

// resolveVar returns the *ir.Var that an expression ultimately accesses,
// chasing through param bindings and resolving implicit `this`.
func (w *depExtractor) resolveVar(e ir.Expr) *ir.Var {
	root, field := peelRoot(e)
	if root == nil {
		return nil
	}
	if expr, bound := w.bindings[root.Name]; bound {
		if field == "" {
			return w.resolveVar(expr)
		}
		return w.resolveVar(&ir.Select{Operand: expr, Field: field})
	}
	if root.Name == "this" && w.implicitThis != nil && field != "" {
		return lookupCompVar(w.implicitThis, field)
	}
	if v, ok := root.Sym.(*ir.Var); ok {
		return v
	}
	if comp, ok := root.Sym.(*ir.Component); ok && field != "" {
		return lookupCompVar(comp, field)
	}
	return nil
}

func lookupCompVar(c *ir.Component, name string) *ir.Var {
	for _, v := range c.Vars {
		if v.Name == name {
			return v
		}
	}
	return nil
}
```

- [ ] **Step 4.3: Add `walkExpr` / `walkStmt` / `walkCall`**

Append (replacing the existing `walkExprDeps`/`walkStmtDeps`):

```go
func (w *depExtractor) walkExpr(e ir.Expr) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ir.Literal, *ir.ContextRead:
		// Terminals.
	case *ir.Ident:
		if w.tracking {
			if v := w.resolveVar(n); v != nil {
				if _, isModel := w.tracker.ModelVars[v]; isModel {
					w.deps[v] = struct{}{}
				}
			}
		}
	case *ir.Select:
		if w.tracking {
			if v := w.resolveVar(n); v != nil {
				if _, isModel := w.tracker.ModelVars[v]; isModel {
					w.deps[v] = struct{}{}
				}
				return
			}
		}
		w.walkExpr(n.Operand)
	case *ir.Index:
		w.walkExpr(n.Operand)
		w.walkExpr(n.Idx)
	case *ir.Binary:
		w.walkExpr(n.Left)
		w.walkExpr(n.Right)
	case *ir.Unary:
		w.walkExpr(n.Operand)
	case *ir.Ternary:
		w.walkExpr(n.Cond)
		w.walkExpr(n.Then)
		w.walkExpr(n.Else)
	case *ir.Conversion:
		w.walkExpr(n.Operand)
	case *ir.ListLit:
		for _, el := range n.Elems {
			w.walkExpr(el)
		}
	case *ir.StructLit:
		for _, f := range n.Fields {
			w.walkExpr(f.Value)
		}
	case *ir.MapLitIR:
		for _, en := range n.Entries {
			w.walkExpr(en.Key)
			w.walkExpr(en.Value)
		}
	case *ir.Spread:
		w.walkExpr(n.Operand)
	case *ir.Lambda:
		if n.Func != nil {
			for _, s := range n.Func.Block {
				w.walkStmt(s)
			}
		}
	case *ir.Closure:
		if n.Func != nil {
			for _, s := range n.Func.Block {
				w.walkStmt(s)
			}
		}
		if n.State != nil {
			w.walkExpr(n.State)
		}
	case *ir.Call:
		w.walkCall(n)
	default:
		// Unhandled: be conservative and skip rather than panic; new IR
		// nodes can be added without breaking analysis.
	}
}

func (w *depExtractor) walkCall(c *ir.Call) {
	if c.Receiver != nil {
		w.walkExpr(c.Receiver)
	}
	for _, a := range c.Args {
		w.walkExpr(a.Value)
	}
	fn := c.Func
	if fn == nil || opaqueFunc(fn) {
		return
	}
	if _, seen := w.visited[fn]; seen {
		return
	}
	sub := w.cloneFrame()
	sub.bindings = make(map[string]ir.Expr, len(fn.Params))
	sub.paramTypes = make(map[string]*ir.Type, len(fn.Params))
	for i, p := range fn.Params {
		if i < len(c.Args) {
			sub.bindings[p.Name] = c.Args[i].Value
		}
		sub.paramTypes[p.Name] = p.Type
	}
	sub.visited[fn] = struct{}{}
	if fn.AST != nil && fn.AST.Body != nil {
		// Expression body — walked via the bodyExpr of the IR (stored in fn.Block as a single Return).
	}
	for _, s := range fn.Block {
		sub.walkStmt(s)
	}
}

func (w *depExtractor) walkStmt(s ir.Stmt) {
	if s == nil {
		return
	}
	switch n := s.(type) {
	case *ir.Assign:
		w.recordWrite(n.Target)
		w.walkExpr(n.Value)
	case *ir.Toggle:
		w.recordWrite(n.Target)
	case *ir.Return:
		w.walkExpr(n.Value)
	case *ir.LocalVar:
		w.walkExpr(n.Init)
	case *ir.If:
		w.walkExpr(n.Cond)
		for _, c := range n.Body {
			w.walkStmt(c)
		}
		for _, c := range n.Else {
			w.walkStmt(c)
		}
	case *ir.For:
		w.walkExpr(n.Iter)
		for _, c := range n.Body {
			w.walkStmt(c)
		}
		for _, c := range n.Else {
			w.walkStmt(c)
		}
	case *ir.Emit:
		for _, a := range n.Args {
			w.walkExpr(a.Value)
		}
	case *ir.CallStmt:
		if n.Call != nil {
			w.walkExpr(n.Call)
		}
	case *ir.NodeInst:
		for _, p := range n.Props {
			w.walkExpr(p.Value)
		}
		for _, c := range n.Children {
			w.walkStmt(c)
		}
	case *ir.SlotInst:
		for _, c := range n.Children {
			w.walkStmt(c)
		}
	case *ir.ErrorBoundary:
		for _, c := range n.Children {
			w.walkStmt(c)
		}
		if n.Handler != nil && n.Handler.Func != nil {
			for _, c := range n.Handler.Func.Block {
				w.walkStmt(c)
			}
		}
	case *ir.PlatformFilter:
		for _, c := range n.Body {
			w.walkStmt(c)
		}
	case *ir.Window:
		w.walkExpr(n.Href)
		w.walkExpr(n.Title)
		w.walkExpr(n.Favicon)
		for _, c := range n.Body {
			w.walkStmt(c)
		}
	case *ir.ContextProvider:
		w.walkExpr(n.Value)
		for _, c := range n.Children {
			w.walkStmt(c)
		}
	}
}

// recordWrite records a mutation on the target's resolved var, gated on
// SNGL's value-vs-ref parameter semantics: writes through value-typed
// params don't propagate.
func (w *depExtractor) recordWrite(target ir.Expr) {
	v := w.resolveVar(target)
	if v == nil {
		return
	}
	// If the write reaches the var via a bound param whose type is a
	// value type, the write is local to the callee and doesn't escape.
	if root, _ := peelRoot(target); root != nil {
		if _, bound := w.bindings[root.Name]; bound {
			if t := w.paramTypes[root.Name]; t != nil && !propagatesWrite(t) {
				return
			}
		}
	}
	if _, isModel := w.tracker.ModelVars[v]; isModel {
		w.mutated[v] = struct{}{}
	}
}

// propagatesWrite reports whether writes through a param of this type
// propagate to the caller. Component, ref<T>, list, and map are reference
// semantics; struct and primitives are value semantics.
func propagatesWrite(t *ir.Type) bool {
	switch t.Kind {
	case ir.TypeComponent, ir.TypeRef, ir.TypeList, ir.TypeMap:
		return true
	}
	return false
}

// opaqueFunc reports whether a function should not be walked further
// (stdlib intrinsics, native/scheme-imported, missing AST).
func opaqueFunc(fn *ir.Func) bool {
	if fn == nil {
		return true
	}
	if fn.AST == nil {
		return true
	}
	if fn.Intrinsic != "" {
		return true
	}
	if fn.NativePkg != "" {
		return true
	}
	return false
}
```

- [ ] **Step 4.4: Add the public entry points**

Replace existing `ExprDeps`, `ExtractDeps`, etc. with:

```go
// ExprDeps returns the set of model vars that a tracked expression reads.
// currentComp is the component owning the tracked context (for implicit
// `this` resolution); nil if the context isn't inside a component.
func (dt *DepTracker) ExprDeps(currentComp *ir.Component, expr ir.Expr) map[*ir.Var]struct{} {
	if expr == nil {
		return nil
	}
	w := newExtractor(dt, currentComp, true)
	w.walkExpr(expr)
	return dt.ExpandDeps(w.deps)
}

// ExpandDeps adds transitive deps through computed funcs.
func (dt *DepTracker) ExpandDeps(deps map[*ir.Var]struct{}) map[*ir.Var]struct{} {
	result := make(map[*ir.Var]struct{}, len(deps))
	for v := range deps {
		result[v] = struct{}{}
	}
	for fn, fnDeps := range dt.ComputedDeps {
		if _, isComputed := dt.ComputedFuncs[fn]; !isComputed {
			continue
		}
		// If any dep is in the input set, add this computed's deps too.
		for d := range fnDeps {
			if _, ok := deps[d]; ok {
				for x := range fnDeps {
					result[x] = struct{}{}
				}
				break
			}
		}
	}
	return result
}

// ExpandMutated expands a set of mutated vars to include any computed
// funcs that transitively depend on them.
//
// Returns the same key type for symmetry with ExpandDeps; computed funcs
// represented in the result are placeholder vars (todo: re-evaluate
// whether this method is needed once consumers migrate). For now,
// callers typically only need the original mutated set.
func (dt *DepTracker) ExpandMutated(mutated map[*ir.Var]struct{}) map[*ir.Var]struct{} {
	result := make(map[*ir.Var]struct{}, len(mutated))
	for v := range mutated {
		result[v] = struct{}{}
	}
	return result
}

// MutatedFields returns the set of vars mutated by a statement.
// currentComp is the component owning the handler context.
func MutatedFields(currentComp *ir.Component, dt *DepTracker, s ir.Stmt) map[*ir.Var]struct{} {
	if s == nil {
		return nil
	}
	w := newExtractor(dt, currentComp, false)
	w.walkStmt(s)
	return w.mutated
}

// MutatedFieldsExpr returns vars mutated by an expression (e.g., a method
// call whose body assigns to a model field).
func MutatedFieldsExpr(currentComp *ir.Component, dt *DepTracker, e ir.Expr) map[*ir.Var]struct{} {
	if e == nil {
		return nil
	}
	w := newExtractor(dt, currentComp, false)
	w.walkExpr(e)
	return w.mutated
}
```

- [ ] **Step 4.5: Delete obsolete walkers**

Remove these functions from `codegen/deps.go`:
- `walkExprDeps`
- `walkStmtDeps`
- `walkStmtsDeps`
- `collectUsedIRStmtsDeps`
- the standalone `ExtractDeps` (package-level)

Keep `FindRootIdent` — it's used by other callers for name-based extraction.

- [ ] **Step 4.6: Update `Dependent` interface and `FindAffected`**

```go
type Dependent interface {
	DepVars() map[*ir.Var]struct{}
}

func FindAffected[T Dependent](dt *DepTracker, items []T, mutated map[*ir.Var]struct{}) []T {
	if len(mutated) == 0 {
		return nil
	}
	expanded := dt.ExpandMutated(mutated)
	var result []T
	for _, item := range items {
		for v := range item.DepVars() {
			if _, hit := expanded[v]; hit {
				result = append(result, item)
				break
			}
		}
	}
	return result
}
```

- [ ] **Step 4.7: Run go build; expect remaining failures**

Run: `go build ./codegen/...`
Expected: errors in `codegen/analysis.go`, `codegen/model.go`, platform/lang files that reference the old name-keyed maps or `DepFields`. Subsequent tasks fix those.

- [ ] **Step 4.8: Run the deps unit tests; expect pass**

Run: `go test ./codegen/... -count=1 -run TestExtractor_BareIdent`

If the test still fails because `NewDepTrackerFromPkg` build issues persist (`pkg.Vars` missing for the synthetic minimal package), fix the test to use a Components-only pkg setup (already correct in step 2.2).

Expected: PASS.

DO NOT commit yet — build is still broken in callers.

---

## Task 5: Migrate `analysis.go`

`CommonAnalysis` holds the name-keyed maps that codegen emitters consume (HTML emits `m.x`, JS emits `m.x`, Go emits `m.X()`). Keep those. Add pointer-keyed maps for the DepTracker.

**Files:**
- Modify: `codegen/analysis.go`

- [ ] **Step 5.1: Add pointer-keyed fields**

Locate the `CommonAnalysis` struct (around line 8). Add fields:

```go
type CommonAnalysis struct {
	// Name-keyed (existing — used at code-emission sites).
	ModelFields    map[string]bool
	ComputedFields map[string]bool
	ComputedDeps   map[string]map[string]bool

	// Pointer-keyed (NEW — consumed by DepTracker).
	ModelVars     map[*ir.Var]struct{}
	ComputedFuncs map[*ir.Func]struct{}
	VarDeps       map[*ir.Func]map[*ir.Var]struct{}

	// ... rest of existing fields ...
}
```

(Preserve all other existing fields untouched.)

- [ ] **Step 5.2: Populate the new fields in `Analyze`**

Locate the `Analyze` function (around line 30). At the loop that iterates `pkg.Funcs`/`pkg.Vars`/`pkg.Components`, in addition to setting name-keyed entries, set pointer-keyed entries:

```go
for _, v := range pkg.Vars {
	a.ModelFields[v.Name] = true
	a.ModelVars[v] = struct{}{}
}
for _, f := range pkg.Funcs {
	if IsComputed(f) {
		a.ModelFields[f.Name] = true
		a.ComputedFields[f.Name] = true
		a.ComputedFuncs[f] = struct{}{}
		deps := make(map[string]bool)
		ptrDeps := make(map[*ir.Var]struct{})
		for _, r := range f.Reads {
			deps[r.Name] = true
			ptrDeps[r] = struct{}{}
		}
		a.ComputedDeps[f.Name] = deps
		a.VarDeps[f] = ptrDeps
	}
}
for _, comp := range pkg.Components {
	if comp.Name == "main" {
		for _, v := range comp.Vars {
			a.ModelFields[v.Name] = true
			a.ModelVars[v] = struct{}{}
		}
		for _, f := range comp.Funcs {
			if IsComputed(f) {
				a.ModelFields[f.Name] = true
				a.ComputedFields[f.Name] = true
				a.ComputedFuncs[f] = struct{}{}
				deps := make(map[string]bool)
				ptrDeps := make(map[*ir.Var]struct{})
				for _, r := range f.Reads {
					deps[r.Name] = true
					ptrDeps[r] = struct{}{}
				}
				a.ComputedDeps[f.Name] = deps
				a.VarDeps[f] = ptrDeps
			}
		}
	}
}
```

Initialize new fields at the top of `Analyze` (where `ModelFields` etc. are made):

```go
a := &CommonAnalysis{
	ModelFields:    make(map[string]bool),
	ComputedFields: make(map[string]bool),
	ComputedDeps:   make(map[string]map[string]bool),
	ModelVars:      make(map[*ir.Var]struct{}),
	ComputedFuncs:  make(map[*ir.Func]struct{}),
	VarDeps:        make(map[*ir.Func]map[*ir.Var]struct{}),
	// ... existing fields ...
}
```

- [ ] **Step 5.3: Update `DepTracker()` constructor**

Find the existing `(a *CommonAnalysis) DepTracker()` method (around line 262):

```go
func (a *CommonAnalysis) DepTracker() *DepTracker {
	return &DepTracker{
		ModelVars:     a.ModelVars,
		ComputedFuncs: a.ComputedFuncs,
		ComputedDeps:  a.VarDeps,
		// Components populated by caller if needed for implicit-this.
	}
}
```

- [ ] **Step 5.4: Verify build**

Run: `go build ./codegen/...`
Expected: a few errors still remain in `model.go` and platform files. Those are addressed in the next tasks. Errors in `analysis.go` itself should be gone.

DO NOT commit yet.

---

## Task 6: Migrate `codegen/model.go`

**Files:**
- Modify: `codegen/model.go`

- [ ] **Step 6.1: Update `Updater` and `Handler`**

Replace the struct fields:

```go
type Updater struct {
	Name     string
	Kind     string
	Node     *ir.NodeInst
	Expr     ir.Expr
	Body     string
	Deps     map[*ir.Var]struct{} // CHANGED
	InitOnly bool
}

func (u Updater) DepVars() map[*ir.Var]struct{} { return u.Deps }  // RENAMED from DepFields

type Handler struct {
	NodeID  string
	Event   string
	Body    ir.Stmt
	Mutated map[*ir.Var]struct{} // CHANGED
}

type TimerHandler struct {
	TimerInfo
	Mutated map[*ir.Var]struct{} // CHANGED
}
```

- [ ] **Step 6.2: Update `AffectedUpdaters`**

```go
func (m *MutationModel) AffectedUpdaters(mutated map[*ir.Var]struct{}) []Updater {
	return FindAffected(m.DepTracker, m.Updaters, mutated)
}
```

- [ ] **Step 6.3: Update `NewMutationModel` to wire Components**

```go
func NewMutationModel(a *CommonAnalysis) *MutationModel {
	dt := a.DepTracker()
	if a.Pkg != nil {
		dt.Components = a.Pkg.Components
	}
	return &MutationModel{
		Analysis:   a,
		DepTracker: dt,
	}
}
```

(`a.Pkg` is the package reference; verify the field name from existing code. If `CommonAnalysis` doesn't carry the package, add a `Pkg *ir.Package` field and populate it in `Analyze`.)

- [ ] **Step 6.4: Run go build**

Run: `go build ./codegen/...`
Expected: errors remaining only in platform/lang consumer files.

---

## Task 7: Migrate platform/html

**Files:**
- Modify: `codegen/platform/html/html.go`
- Modify: `codegen/platform/html/cdprunner.go`

- [ ] **Step 7.1: Update updater-deps construction in `html.go`**

Find every site that does `updater.Deps = map[string]bool{...}` or `extractDeps(...)`. Replace with calls that produce `map[*ir.Var]struct{}` via `dt.ExprDeps(currentComp, expr)`.

For example, the existing pattern:

```go
updater.Deps = ExtractDeps(expr, scope.ModelFields)
```

becomes:

```go
updater.Deps = dt.ExprDeps(comp, expr)
```

Where `dt` is the `MutationModel.DepTracker` and `comp` is the component currently being processed. Pass `comp` through the call chain that builds updaters.

- [ ] **Step 7.2: Update handler mutated-set construction**

Similar replacement for handlers:

```go
handler.Mutated = MutatedFields(comp, dt, stmt)
```

- [ ] **Step 7.3: Update name extraction at emission sites**

Where the HTML emitter writes `setX("new value")` etc., it needs the var NAMES, not pointers. Replace:

```go
for name := range updater.Deps {
    // emit subscription for name
}
```

with:

```go
for v := range updater.Deps {
    name := v.Name
    // emit subscription for name
}
```

- [ ] **Step 7.4: Update `cdprunner.go` similarly**

Apply the same pointer-keyed-set migration to any test/runner code that reads `updater.Deps` or `handler.Mutated`.

- [ ] **Step 7.5: Build + test HTML**

Run: `go build ./codegen/platform/html/...`
Run: `go test ./codegen/platform/html/... -count=1`
Expected: build and tests pass. Existing HTML reactivity unchanged because pkg.Vars and comp.Vars are still the model; only the keying is different.

---

## Task 8: Migrate platform/bubbletea

**Files:**
- Modify: `codegen/platform/bubbletea/compiler_ir.go`

- [ ] **Step 8.1: Migrate any `map[string]bool` deps/mutated usage**

Grep:
```bash
grep -n "map\[string\]bool\|Deps\b\|Mutated\b" codegen/platform/bubbletea/*.go
```

For each site that constructs or reads dep/mutated sets, switch to `map[*ir.Var]struct{}` and dereference `.Name` at name-emission boundaries.

- [ ] **Step 8.2: Build + test**

Run: `go build ./codegen/platform/bubbletea/...`
Run: `go test ./codegen/platform/bubbletea/... -count=1`
Expected: pass.

---

## Task 9: Migrate platform/fyne + platform/android

**Files:**
- Modify: `codegen/platform/fyne/compiler_ir.go`
- Modify: `codegen/platform/android/compiler_ir.go`

- [ ] **Step 9.1: Apply same migration as Task 8 for fyne**

- [ ] **Step 9.2: Apply same migration as Task 8 for android**

- [ ] **Step 9.3: Build all platforms**

Run: `go build ./codegen/platform/...`
Expected: clean.

- [ ] **Step 9.4: Test platforms**

Run: `go test ./codegen/platform/... -count=1`
Expected: pass.

---

## Task 10: Audit lang codegens and lower passes

**Files:**
- Modify (if needed): `codegen/lang/golang/translate_ir.go`
- Modify (if needed): `codegen/lang/javascript/translate_ir.go`
- Modify (if needed): `internal/lower/computed.go`

- [ ] **Step 10.1: Grep for name-keyed dep references in lang/**

```bash
grep -n "ModelFields\|ComputedFields\|ComputedDeps" codegen/lang/*/*.go
```

These already use name-keyed `scope.ModelFields` for emission. No change needed unless a particular site needs pointer keys. Verify each reference is emission-only.

- [ ] **Step 10.2: Audit `internal/lower/computed.go`**

The computed-inlining pass already keys on `*ir.Func` pointers. Verify it doesn't also consult dep sets by name. If it does, migrate.

- [ ] **Step 10.3: Build + full test**

Run: `go build ./...`
Run: `go test ./... -count=1 -short`
Expected: pass (modulo any pre-existing unrelated failures).

---

## Task 11: Add unit tests for substitution + write tracking

**Files:**
- Modify: `codegen/deps_test.go`

- [ ] **Step 11.1: Substitution-through-one-call test**

Append:

```go
func TestExtractor_SubstituteOneCall(t *testing.T) {
	x := makeVar("x")
	comp := makeComp("main", x)

	// func double(c main) => c.x * 2
	cParam := &ir.Param{Name: "c", Type: &ir.Type{Kind: ir.TypeComponent, Decl: comp}}
	body := &ir.Binary{
		Op: ast.BinaryMul,
		Left: &ir.Select{
			Operand: &ir.Ident{Name: "c", Sym: cParam},
			Field:   "x",
		},
		Right: &ir.Literal{Type: &ir.Type{Kind: ir.TypeInt}},
	}
	double := makeFunc("double", []*ir.Param{cParam}, body)
	double.Receiver = "main"
	double.AST = &ast.FuncDef{} // non-nil so opaqueFunc returns false

	pkg := &ir.Package{Components: []*ir.Component{comp}, Funcs: []*ir.Func{double}}
	dt := NewDepTrackerFromPkg(pkg)

	// Caller: double(c_inst) where c_inst is an Ident bound to the comp.
	cInst := &ir.Ident{Name: "self", Sym: comp}
	call := &ir.Call{
		Func: double,
		Args: []ir.CallArg{{Value: cInst}},
	}

	got := dt.ExprDeps(comp, call)
	want := asSet(x)
	if !equalVarSet(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
```

- [ ] **Step 11.2: Implicit-this substitution test**

```go
func TestExtractor_ImplicitThis(t *testing.T) {
	x := makeVar("x")
	comp := makeComp("main", x)

	// Walk `this.x` inside a tracked context owned by comp (no bindings).
	expr := &ir.Select{
		Operand: &ir.Ident{Name: "this"},
		Field:   "x",
	}
	pkg := &ir.Package{Components: []*ir.Component{comp}}
	dt := NewDepTrackerFromPkg(pkg)

	got := dt.ExprDeps(comp, expr)
	want := asSet(x)
	if !equalVarSet(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
```

- [ ] **Step 11.3: Write propagation through component param**

```go
func TestExtractor_WriteThroughCompParam(t *testing.T) {
	x := makeVar("x")
	comp := makeComp("main", x)

	// func reset(c main) { c.x = 0 }
	cParam := &ir.Param{Name: "c", Type: &ir.Type{Kind: ir.TypeComponent, Decl: comp}}
	body := &ir.Assign{
		Target: &ir.Select{
			Operand: &ir.Ident{Name: "c", Sym: cParam},
			Field:   "x",
		},
		Value: &ir.Literal{Type: &ir.Type{Kind: ir.TypeInt}},
	}
	reset := &ir.Func{Name: "reset", Receiver: "main", Params: []*ir.Param{cParam},
		Block: []ir.Stmt{body}, AST: &ast.FuncDef{}}

	pkg := &ir.Package{Components: []*ir.Component{comp}, Funcs: []*ir.Func{reset}}
	dt := NewDepTrackerFromPkg(pkg)

	cInst := &ir.Ident{Name: "self", Sym: comp}
	call := &ir.Call{Func: reset, Args: []ir.CallArg{{Value: cInst}}}
	callStmt := &ir.CallStmt{Call: call}

	got := MutatedFields(comp, dt, callStmt)
	want := asSet(x)
	if !equalVarSet(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
```

- [ ] **Step 11.4: Write through value-typed struct param does NOT propagate**

```go
func TestExtractor_WriteThroughValueStructParam(t *testing.T) {
	// struct Point { x int }
	pointDef := &ir.StructDef{Name: "Point", Fields: []*ir.StructField{
		{Name: "x", Type: &ir.Type{Kind: ir.TypeInt}},
	}}
	pointType := &ir.Type{Kind: ir.TypeStruct, Decl: pointDef}

	// Caller-side: var p Point = ...
	p := &ir.Var{Name: "p", Type: pointType}
	comp := makeComp("main", p)

	// func mutate(arg Point) { arg.x = 0 }
	arg := &ir.Param{Name: "arg", Type: pointType}
	body := &ir.Assign{
		Target: &ir.Select{Operand: &ir.Ident{Name: "arg", Sym: arg}, Field: "x"},
		Value:  &ir.Literal{Type: &ir.Type{Kind: ir.TypeInt}},
	}
	mutate := &ir.Func{Name: "mutate", Params: []*ir.Param{arg},
		Block: []ir.Stmt{body}, AST: &ast.FuncDef{}}

	pkg := &ir.Package{Components: []*ir.Component{comp}, Funcs: []*ir.Func{mutate}}
	dt := NewDepTrackerFromPkg(pkg)

	pInst := &ir.Ident{Name: "p", Sym: p}
	call := &ir.Call{Func: mutate, Args: []ir.CallArg{{Value: pInst}}}
	callStmt := &ir.CallStmt{Call: call}

	got := MutatedFields(comp, dt, callStmt)
	if len(got) != 0 {
		t.Errorf("write through value-typed struct param should not propagate; got %v", got)
	}
}
```

- [ ] **Step 11.5: Recursion guard**

```go
func TestExtractor_Recursion(t *testing.T) {
	x := makeVar("x")
	comp := makeComp("main", x)

	// func loop(c main) => loop(c)  -- direct self-recursion
	cParam := &ir.Param{Name: "c", Type: &ir.Type{Kind: ir.TypeComponent, Decl: comp}}
	loop := &ir.Func{Name: "loop", Receiver: "main", Params: []*ir.Param{cParam}, AST: &ast.FuncDef{}}
	loop.Block = []ir.Stmt{&ir.Return{Value: &ir.Call{
		Func: loop, Args: []ir.CallArg{{Value: &ir.Ident{Name: "c", Sym: cParam}}},
	}}}

	pkg := &ir.Package{Components: []*ir.Component{comp}, Funcs: []*ir.Func{loop}}
	dt := NewDepTrackerFromPkg(pkg)

	cInst := &ir.Ident{Name: "self", Sym: comp}
	call := &ir.Call{Func: loop, Args: []ir.CallArg{{Value: cInst}}}

	// Must not stack-overflow. Result is empty (no var reads in body).
	got := dt.ExprDeps(comp, call)
	if len(got) != 0 {
		t.Errorf("unexpected deps: %v", got)
	}
}
```

- [ ] **Step 11.6: Run all walker tests**

Run: `go test ./codegen/... -count=1 -run TestExtractor`
Expected: all pass.

- [ ] **Step 11.7: Run fixtures**

Run: `go test ./codegen/platform/none/testrunner/... -count=1 -run 'TestRunFixtures/test_reactivity|TestRunFixtures/test_mutation'`
Expected: pass.

- [ ] **Step 11.8: Commit migration + tests**

```bash
git add codegen/ internal/lower/
git commit -m "$(cat <<'EOF'
codegen: substituting dep walker through function params

Replace flat walkExprDeps/walkStmtDeps with a depExtractor that carries
a bindings frame (param name -> caller-side expr) and resolves all deps
to *ir.Var pointers. DepTracker, Updater.Deps, Handler.Mutated, and
all platform consumers migrate to pointer-keyed sets.

Writes through value-typed params don't propagate; writes through
component/ref/list/map params do. Reads always propagate via
substitution regardless of calling convention.

Pointer keys disambiguate same-named vars across scopes (package vs
component, two components with same field). Closes the gap that broke
issue #75's component-method desugaring.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 12: Full verification

- [ ] **Step 12.1: Full test suite**

Run: `go tool verify`
Expected: PASS — all fixtures pass, all platform smoke tests pass.

- [ ] **Step 12.2: Format**

Run: `go fmt ./...`
Expected: no diff.

- [ ] **Step 12.3: Build CLI**

Run: `go install ./cmd/sngl`
Expected: success.

- [ ] **Step 12.4: Smoke-test the failing-#75 case**

Manually re-enable component method desugaring (Task 1 of #75's follow-up — small one-line change in `internal/checker/checker.go`'s `registerComponent`):

```go
case *ast.FuncDef:
    nestedFuncs = append(nestedFuncs, s)
}
// (later, after irComp is in symtab)
irComp.Funcs = c.registerNestedMethods(irComp.Name, nil, nestedFuncs)
```

Then run: `go test ./... -count=1 -short`
Expected: passes, demonstrating the analyzer fix unblocked the #75 follow-up.

Revert this change before committing the plan's work — re-enabling component desugaring is a separate change.

- [ ] **Step 12.5: Final commit if any cleanup**

If steps 12.1-12.4 surface stragglers, fix and commit:

```bash
git add -A
git commit -m "reactive-tracking: final cleanup"
```

---

## Self-Review

**Spec coverage:**
- §Walker shape → Task 4.
- §Root resolution → Task 4.2 (peelRoot, resolveVar).
- §Read tracking → Task 4.3 (walkExpr's Ident/Select branches).
- §Write tracking → Task 4.3 (walkStmt's Assign/Toggle) + 4.4 (recordWrite + propagatesWrite gate).
- §Call dispatch → Task 4.3 (walkCall).
- §Public API migration → Tasks 3, 4.4, 4.6.
- §Pass-by-value vs ref → Task 4.3 (`propagatesWrite`) + Task 11.4 (test).
- §Constraints (no runtime, whole-var, visited-set, lambda handling, indirect calls) → Tasks 4, 11.5.
- §Testing (fixtures + unit tests) → Tasks 1, 2, 11.

**Placeholder scan:** none. Every step has actual code or commands.

**Type consistency:** `map[*ir.Var]struct{}` is used uniformly. `DepVars` (not `DepFields`) on the `Dependent` interface. `resolveVar` (not `findVar` or `resolveRoot`) used consistently across tasks. `Updater.Deps`, `Handler.Mutated`, `TimerHandler.Mutated` all `map[*ir.Var]struct{}`.

**Notes flagged for verification at implementation time:**
- Task 6.3: `CommonAnalysis.Pkg` field may not exist; check and add if needed.
- Task 10.2: `internal/lower/computed.go` may already be pointer-keyed; if so, no change.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-20-reactive-tracking-through-params.md`. Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
