# Pull-Reactive Interp Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the 4 pre-existing testrunner reactive fixtures pass: `test_reactivity_two_components`, `test_reactivity_extension_method`, `test_reactivity_global_var`, `test_mutation_through_method`.

**Architecture:** Cache child component envs in `Env.childEnvs` keyed by `*ir.NodeInst` so state persists across `ResolveElementRef` calls. Add `componentValue.children` returning live wrappers in source order. Extend method-dispatch lookup chain on `componentValue` to consult `env.Funcs[<compName>.<method>]` for top-level extension methods. Bind `this` to the `componentValue` itself when invoking desugared methods so mutations flow through `SetField` (already wired at `internal/interp/exec.go:178-179`).

**Tech Stack:** Go. Touchpoints: `internal/interp/render.go`, `internal/interp/eval.go`, `codegen/platform/none/testrunner/runner.go`, `codegen/platform/none/testrunner/testing_t.go`.

**Spec:** `docs/superpowers/specs/2026-05-21-interp-pull-reactive-design.md`

---

## File Structure

| File                                              | Action | Responsibility                                                                                                                               |
|---------------------------------------------------|--------|----------------------------------------------------------------------------------------------------------------------------------------------|
| `internal/interp/eval.go`                         | Modify | `Env` struct gains `childEnvs` field; constructors init it                                                                                   |
| `internal/interp/render.go`                       | Modify | `componentEnv` memoises via `childEnvs`                                                                                                      |
| `codegen/platform/none/testrunner/runner.go`      | Modify | `componentValue` gains `body` + `children` fields; constructor captures component body                                                       |
| `codegen/platform/none/testrunner/testing_t.go`   | Modify | `GetField("children")` returns the live slice; `walkChildren` helper; type-namespace dispatch in `GetField` + `InvokeMethod`; `invokeOnSelf` |
| `codegen/platform/none/testrunner/runner_test.go` | Modify | Unit tests for `children` + isolation between siblings                                                                                       |

---

## Task 1: Cache child component envs

**Files:**
- Modify: `internal/interp/eval.go`
- Modify: `internal/interp/render.go`

- [ ] **Step 1.1: Add `childEnvs` field to `Env`**

In `internal/interp/eval.go`, find the `Env` struct definition. Add a field:

```go
type Env struct {
	// ... existing fields ...

	// childEnvs caches per-NodeInst child component envs so state
	// persists across ResolveElementRef calls. Keyed by the
	// instantiation site's *ir.NodeInst pointer.
	childEnvs map[*ir.NodeInst]*Env
}
```

- [ ] **Step 1.2: Initialise in `NewEnv`**

Find `NewEnv()` (around line 125) and add the map alongside the existing initialisers:

```go
func NewEnv() *Env {
	return &Env{
		Vars:      map[string]any{},
		Consts:    map[string]any{},
		Funcs:     map[string]*ir.Func{},
		childEnvs: map[*ir.NodeInst]*Env{},
	}
}
```

- [ ] **Step 1.3: Initialise in `Snapshot`**

`Snapshot()` returns a shallow copy. The `childEnvs` map should be SHARED across snapshots so cached envs persist through scope changes (each child env owns its own state, but the cache itself is parent-state). Update Snapshot:

```go
func (env *Env) Snapshot() *Env {
	cp := &Env{
		// ... existing fields copied ...
		childEnvs: env.childEnvs, // share the cache; do not deep-copy
	}
	if cp.childEnvs == nil {
		cp.childEnvs = map[*ir.NodeInst]*Env{}
	}
	return cp
}
```

> Read the existing Snapshot to see exact field assignments. The new line goes alongside (e.g.) `Funcs: env.Funcs`. Both are shared references, not deep copies.

- [ ] **Step 1.4: Memoise in `componentEnv`**

In `internal/interp/render.go`, the existing `componentEnv` (line 103) builds a fresh env each call. Wrap with cache:

```go
func (env *Env) componentEnv(comp *ir.Component, inst *ir.NodeInst) *Env {
	if env.childEnvs == nil {
		env.childEnvs = map[*ir.NodeInst]*Env{}
	}
	if cached, ok := env.childEnvs[inst]; ok {
		return cached
	}
	child := NewEnv()
	child.Pkg = env.Pkg
	child.Units = env.Units
	child.Comp = comp

	for _, p := range comp.Props {
		child.Vars[p.Name] = evalInit(child, p.Default)
	}
	for _, arg := range inst.Props {
		if arg.Name == "" {
			continue
		}
		v, err := env.Eval(arg.Value)
		if err == nil {
			child.Vars[arg.Name] = v
		}
	}
	for _, v := range comp.Vars {
		if v.IsConst {
			child.Consts[v.Name] = evalInit(child, v.Init)
		} else {
			child.Vars[v.Name] = evalInit(child, v.Init)
		}
	}
	for _, fn := range comp.Funcs {
		child.SetFunc(fn)
	}
	child.BodyStmts = comp.Body

	env.childEnvs[inst] = child
	return child
}
```

Note: this is mostly the current body, with the cache lookup at the top and the cache store before return.

- [ ] **Step 1.5: Verify build**

Run: `go build ./internal/interp/...`
Expected: clean.

- [ ] **Step 1.6: Run interp tests**

Run: `go test ./internal/interp/... -count=1`
Expected: all pass. Caching doesn't change behavior for fixtures that don't rely on persistence.

- [ ] **Step 1.7: Commit**

```bash
git add internal/interp/eval.go internal/interp/render.go
git commit -m "interp: cache child component envs by NodeInst

State mutations on a child component instance now persist across
ResolveElementRef calls. The cache is keyed on the instantiation
*ir.NodeInst pointer, so distinct call sites get distinct envs.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 2: `componentValue` carries the body for walking

**Files:**
- Modify: `codegen/platform/none/testrunner/runner.go`

- [ ] **Step 2.1: Add `body` and `children` fields**

Find the `componentValue` struct (around line 129):

```go
type componentValue struct {
	Env        *interp.Env
	Vars       map[string]any
	Consts     map[string]any
	Funcs      map[string]*ir.Func
	compName   string
	testParams map[string]bool

	// body is the component's lowered body, captured at construction.
	// Used by walkChildren to enumerate direct visual statements.
	body []ir.Stmt

	// children is a memoised live slice — user-component entries are
	// *componentValue wrappers, native entries are element-map dicts.
	// Computed lazily on first GetField("children").
	children []any
}
```

- [ ] **Step 2.2: Capture body at construction**

Find the cVal construction (around line 55):

```go
cVal = &componentValue{
	Env:        env,
	Vars:       make(map[string]any),
	Consts:     make(map[string]any),
	Funcs:      make(map[string]*ir.Func),
	compName:   compName,
	testParams: testParams,
}
maps.Copy(cVal.Vars, env.Vars)
maps.Copy(cVal.Consts, env.Consts)
maps.Copy(cVal.Funcs, env.Funcs)
env.Vars[fn.Params[1].Name] = cVal
```

After `maps.Copy(cVal.Funcs, env.Funcs)`, look up the component definition and capture its body:

```go
if comp := findComponentByName(pkg, compName); comp != nil {
	cVal.body = comp.Body
}
```

Add the helper at the bottom of `runner.go`:

```go
// findComponentByName scans the package for a component with the given name.
func findComponentByName(pkg *ir.Package, name string) *ir.Component {
	for _, c := range pkg.Components {
		if c.Name == name {
			return c
		}
	}
	return nil
}
```

- [ ] **Step 2.3: Verify build**

Run: `go build ./codegen/platform/none/testrunner/...`
Expected: clean.

- [ ] **Step 2.4: Do not commit yet** — Task 3 wires up the walker.

---

## Task 3: `walkChildren` helper

**Files:**
- Modify: `codegen/platform/none/testrunner/testing_t.go`

- [ ] **Step 3.1: Add walkChildren**

At the bottom of `testing_t.go`:

```go
// walkChildren returns the direct visual statements of the component's
// body as a slice in source order. User-component NodeInsts become
// *componentValue wrappers sharing the parent env's cached child envs;
// native elements become element-map dicts.
func (cv *componentValue) walkChildren(stmts []ir.Stmt) []any {
	var out []any
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if n.Component != nil && len(n.Component.Body) > 0 {
				childEnv := cv.Env.ComponentEnv(n.Component, n)
				wrapper := &componentValue{
					Env:      childEnv,
					Vars:     childEnv.Vars,
					Consts:   childEnv.Consts,
					Funcs:    childEnv.Funcs,
					compName: n.Component.Name,
					body:     n.Component.Body,
				}
				out = append(out, wrapper)
			} else {
				out = append(out, cv.Env.RenderNodeProps(n))
			}
		case *ir.CallStmt:
			if rendered := cv.Env.RenderCallStmtNode(n); rendered != nil {
				out = append(out, rendered)
			}
		case *ir.If:
			cond, err := cv.Env.Eval(n.Cond)
			if err != nil {
				continue
			}
			if b, _ := cond.(bool); b {
				out = append(out, cv.walkChildren(n.Body)...)
			} else {
				out = append(out, cv.walkChildren(n.Else)...)
			}
		case *ir.PlatformFilter:
			if n.Platform == "" || n.Platform == "none" {
				out = append(out, cv.walkChildren(n.Body)...)
			}
			// *ir.For: out of scope per spec — components inside for loops
			// need per-iteration child envs; deferred.
		}
	}
	return out
}
```

> The walker uses `cv.Env.ComponentEnv`, `cv.Env.RenderNodeProps`, `cv.Env.RenderCallStmtNode` — interp methods that today are package-private (`componentEnv`, `renderNodeProps`). Task 4 exports them.

- [ ] **Step 3.2: Do not commit yet** — Task 4 exports the helpers used here.

---

## Task 4: Export the interp helpers used by walkChildren

**Files:**
- Modify: `internal/interp/render.go`

The testrunner is in a separate package and currently can't call private interp methods. Export thin public wrappers.

- [ ] **Step 4.1: Add `ComponentEnv` public wrapper**

In `internal/interp/render.go`, after the existing `componentEnv` (around line 134):

```go
// ComponentEnv is the public entry point for componentEnv. Used by the
// testrunner to construct live child componentValue wrappers.
func (env *Env) ComponentEnv(comp *ir.Component, inst *ir.NodeInst) *Env {
	return env.componentEnv(comp, inst)
}
```

- [ ] **Step 4.2: Add `RenderNodeProps` public wrapper**

After the existing `renderNodeProps` (around line 250):

```go
// RenderNodeProps is the public entry point for renderNodeProps. Returns
// a map of prop-name → evaluated value, plus the node's ID. Used by the
// testrunner to expose native elements as dicts in c.children.
func (env *Env) RenderNodeProps(node *ir.NodeInst) map[string]any {
	return env.renderNodeProps(node)
}
```

- [ ] **Step 4.3: Add `RenderCallStmtNode` helper**

The existing `collectCallStmtByID` (around line 139) renders a CallStmt-as-element only as a side effect of its ID search. Extract the rendering into a standalone function:

```go
// RenderCallStmtNode renders a children-less element call (text #id(...))
// as an element map. Returns nil for non-element call statements.
func (env *Env) RenderCallStmtNode(cs *ir.CallStmt) map[string]any {
	elemName, elemID := elemCallInfo(cs)
	if elemName == "" {
		return nil
	}
	// Synthesise a NodeInst-shaped map by reading the call's props.
	// The current collectCallStmtByID already builds this; refactor
	// its rendering into a helper that returns the map.
	return env.renderCallStmtMap(cs, elemName, elemID)
}
```

> Where the rendering logic currently lives inside `collectCallStmtByID`, factor it out into a private `renderCallStmtMap(cs, name, id)` returning `map[string]any`. Then both `collectCallStmtByID` and `RenderCallStmtNode` call it.

If the existing logic is short and inlined deeply, the cleanest refactor is:

1. Read the body of `collectCallStmtByID`.
2. Move the per-match map construction into `renderCallStmtMap(cs *ir.CallStmt, name, id string) map[string]any`.
3. Have `collectCallStmtByID` call the helper when its ID match condition fires.
4. Have `RenderCallStmtNode` call the helper unconditionally.

- [ ] **Step 4.4: Verify build**

Run: `go build ./internal/interp/...`
Expected: clean.

- [ ] **Step 4.5: Commit Tasks 2-4**

```bash
git add internal/interp/render.go \
        codegen/platform/none/testrunner/runner.go \
        codegen/platform/none/testrunner/testing_t.go
git commit -m "interp+testrunner: scaffold for c.children traversal

componentValue carries the lowered component body; walkChildren
enumerates direct visual statements. Interp exports ComponentEnv,
RenderNodeProps, and RenderCallStmtNode for the testrunner to call.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 5: `GetField('children')` returns the live slice

**Files:**
- Modify: `codegen/platform/none/testrunner/testing_t.go`

- [ ] **Step 5.1: Add the GetField branch**

Find `GetField` (around line 262). Add a `field == "children"` branch BEFORE the var/const lookups:

```go
func (cv *componentValue) GetField(field string) (any, error) {
	if field == "children" {
		if cv.children == nil {
			cv.children = cv.walkChildren(cv.body)
		}
		return cv.children, nil
	}
	if v, ok := cv.Vars[field]; ok {
		return v, nil
	}
	// ... existing chain ...
}
```

- [ ] **Step 5.2: Build + test**

Run: `go build ./codegen/platform/none/testrunner/...`
Run: `go test ./codegen/platform/none/testrunner/... -count=1 -short -run TestRunFixtures/test_reactivity_two_components`
Expected: PASS (or at minimum, the test progresses past `c.children[0]` and fails on a later assertion — which is fine; subsequent tasks fix the deeper issues).

- [ ] **Step 5.3: Commit**

```bash
git add codegen/platform/none/testrunner/testing_t.go
git commit -m "testrunner: expose c.children as a live slice

GetField('children') walks the component body once and returns each
direct visual statement wrapped as a *componentValue (user components)
or rendered as an element-map dict (native elements).

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 6: Type-namespace method dispatch on `componentValue`

**Files:**
- Modify: `codegen/platform/none/testrunner/testing_t.go`

The existing `GetField` and `InvokeMethod` look up `cv.Funcs[bare]` and `cv.Funcs[cv.compName+"."+bare]`. After both miss, they should fall through to `cv.Env.Funcs[cv.compName+"."+bare]` to pick up top-level extension methods registered at package scope.

- [ ] **Step 6.1: Extend `GetField` chain**

In `GetField`, after the existing two `cv.Funcs[...]` checks, before the ElementRef fallback, add:

```go
// Top-level extension method on this component type (e.g.
// `func main.double(this main) => ...`). Registered in env.Funcs
// under "<compName>.<method>"; not in cv.Funcs.
if cv.compName != "" {
	if fn, ok := cv.Env.Funcs[cv.compName+"."+field]; ok {
		effective := len(fn.Params)
		if effective > 0 && fn.Receiver != "" && fn.Params[0].Name == "this" {
			effective--
		}
		if effective == 0 {
			return cv.invokeOnSelf(fn, nil)
		}
	}
}
```

- [ ] **Step 6.2: Extend `InvokeMethod` chain**

`InvokeMethod` (around line 318) currently looks up `cv.Funcs[method]`. After the existing lookup branch fails, add:

```go
if !ok && cv.compName != "" {
	if extFn, extOK := cv.Env.Funcs[cv.compName+"."+method]; extOK {
		fn = extFn
		ok = true
	}
}
```

Position: immediately after the line `fn, ok := cv.Funcs[method]` (and any @event handling).

- [ ] **Step 6.3: Implement `invokeOnSelf`**

Add near the bottom of `testing_t.go`:

```go
// invokeOnSelf evaluates a desugared method bound to this componentValue
// as the synthetic `this` receiver. Mutations inside the body that target
// `this.<field>` route through cv.SetField via the existing *ir.Select
// branch in eval.go's evalMutTarget (line 178).
func (cv *componentValue) invokeOnSelf(fn *ir.Func, argExprs []ir.Expr) (any, error) {
	if fn == nil {
		return nil, fmt.Errorf("invokeOnSelf: nil func")
	}
	// The desugared method's first param is `this`. Bind it to cv.
	if len(fn.Params) == 0 {
		return cv.compEnv().EvalUserFunc(fn, nil)
	}
	// Build the arg expression slice: prepend a synthetic ident bound
	// to cv (the test func's env has cv stored under fn.Params[0].Name
	// already if it's `this`; we use a fresh synthesised expr below).
	synth := make([]ir.Expr, 0, len(argExprs)+1)
	synth = append(synth, &ir.Ident{Name: fn.Params[0].Name})
	synth = append(synth, argExprs...)

	// Set the synthetic `this` ident's binding in the env BEFORE eval.
	saved, hadSaved := cv.Env.Vars[fn.Params[0].Name]
	cv.Env.Vars[fn.Params[0].Name] = cv
	defer func() {
		if hadSaved {
			cv.Env.Vars[fn.Params[0].Name] = saved
		} else {
			delete(cv.Env.Vars, fn.Params[0].Name)
		}
	}()

	return cv.Env.EvalUserFunc(fn, synth)
}
```

> Implementation note: `EvalUserFunc` in `internal/interp/eval.go` takes (fn, argExprs). The pre-binding via `cv.Env.Vars[...]` is the trick that makes the synthetic ident resolve to `cv` when EvalUserFunc binds the param.
>
> Verify the signature of `EvalUserFunc`:
>
> ```bash
> grep -n "func.*EvalUserFunc" internal/interp/eval.go
> ```
>
> Adjust if the signature differs.

- [ ] **Step 6.4: Build + test**

Run: `go build ./...`
Run: `go test ./codegen/platform/none/testrunner/... -count=1 -short -run 'TestRunFixtures/test_reactivity|TestRunFixtures/test_mutation' -v`
Expected: progress on all 4 fixtures. Two pass outright (those that only needed children + type-namespace dispatch); two still partial-fail on assertions that need Section 7's wiring.

- [ ] **Step 6.5: Commit**

```bash
git add codegen/platform/none/testrunner/testing_t.go
git commit -m "testrunner: type-namespace method dispatch + invokeOnSelf

GetField and InvokeMethod fall through to env.Funcs[compName.method]
for top-level extension methods. invokeOnSelf binds the desugared
this param to the componentValue itself so writes flow through
SetField via the existing *ir.Select assign path.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 7: Verify mutation propagation through event handlers

**Files:**
- (Diagnose; modify if needed) `internal/interp/exec.go`, `internal/interp/eval.go`, `codegen/platform/none/testrunner/testing_t.go`

The remaining failing fixture is `test_mutation_through_method`:

```sngl
component main {
    var x = 0
    func reset() { x = 0 }
    func bump()  { x += 1 }
    button #bump(text="+", @click { bump() })
    button #reset(text="0", @click { reset() })
    text #out(value=string(x))
}
func testMutationThroughMethod(t Test, c main) {
    t.assert(c.out.value == "0")
    c.bump.@click()  // ← triggers handler that calls bump()
    t.assert(c.out.value == "2") // ← after 2 clicks
    ...
}
```

`c.bump.@click()` triggers the click handler. Inside the handler, `bump()` is a bare-name call to a component method.

- [ ] **Step 7.1: Diagnose the call shape**

Run the fixture in isolation:

```bash
go test ./codegen/platform/none/testrunner/... -count=1 -short -run TestRunFixtures/test_mutation_through_method -v
```

Inspect the output. The failure likely manifests as `c.out.value` stuck at the initial value, meaning the click handler ran but `bump()` didn't mutate `x`.

Two possibilities:
- (A) The handler runs but `bump()` resolves to a fresh env clone where the mutation doesn't propagate.
- (B) The handler doesn't trigger at all from `c.bump.@click()`.

`grep -n "@click\|button.*Click\|cv.btn" internal/interp/*.go codegen/platform/none/testrunner/*.go` to find the click-trigger path.

- [ ] **Step 7.2: Fix per diagnosis**

If (A): the call to `bump()` from inside the handler body needs to use the same `cv` binding the test holds. Inside `cv.Env`, `bump()` should resolve via `env.Funcs["main.bump"]` → `invokeOnSelf` with `cv = the test's componentValue`. The handler's execution env IS `cv.Env`, so this should happen naturally once `invokeOnSelf` from Task 6 binds `this` to the same cv.

The likely fix: when the interp resolves a bare-name call inside a handler running on `cv.Env`, and the name matches `<compName>.<bare>` in env.Funcs, invoke via `invokeOnSelf` instead of `EvalUserFunc`. Look in `internal/interp/eval.go` for the bare-call path:

```bash
grep -n "evalCall\|evalPlainFunc" internal/interp/eval.go | head
```

In `evalPlainFunc` (or `evalCall`'s plain-func branch), before defaulting to `EvalUserFunc`, check if the call's Func has a Receiver matching the env's current Comp and a method body with synthetic `this`. If so, bind cv:

```go
// internal/interp/eval.go, in the plain-call resolver:
if call.Func != nil && call.Func.Receiver != "" && env.Comp != nil && env.Comp.Name == call.Func.Receiver {
	// Desugared method call from within the component scope.
	// The cv associated with this env should be the receiver.
	// We don't have cv at the interp level; the testrunner's
	// testing_t.go is responsible for installing it via
	// env.Vars["this"] before invoking handler bodies.
	if recv, ok := env.Vars["this"]; ok {
		if cv, ok := recv.(ComponentValue); ok {
			_ = cv // bind via env.Vars[fn.Params[0].Name]
		}
	}
}
```

> The cleanest fix may be at the testrunner level: when `cv.dispatchEvent(elem, handler)` runs the handler body, install `env.Vars["this"] = cv` for the duration. Then any bare method call inside resolves with `this` already bound to cv. Verify by looking at the existing click-dispatch path.

If (B): the click trigger path doesn't call the handler. This is a deeper testrunner bug — out of scope; flag and revisit.

- [ ] **Step 7.3: Re-run the fixture**

Run: `go test ./codegen/platform/none/testrunner/... -count=1 -short -run TestRunFixtures/test_mutation_through_method -v`
Expected: PASS, OR a clearly diagnosable next blocker.

- [ ] **Step 7.4: Commit**

```bash
git add codegen/platform/none/testrunner/ internal/interp/
git commit -m "testrunner+interp: bind this to componentValue in handler scope

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 8: Unit tests for children + isolation

**Files:**
- Modify: `codegen/platform/none/testrunner/runner_test.go` (or create if absent)

- [ ] **Step 8.1: Add a small in-process test**

```go
package testrunner_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen/platform/none/testrunner"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

func TestChildrenLiveAcrossMutations(t *testing.T) {
	src := `
component counter {
    var n = 0
    button #b(text="+", @click { n += 1 })
    text #o(value=string(n))
}
component main {
    counter()
    counter()
}
func testIsolation(t Test, c main) {
    var l = c.children[0]
    var r = c.children[1]
    t.assert(l.o.value == "0")
    l.b.@click()
    t.assert(l.o.value == "1")
    t.assert(r.o.value == "0")
}
`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	pkg, _ := checker.Check(doc, &checker.Config{IsMain: true})
	results, err := testrunner.Run(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("no test results")
	}
	for _, r := range results {
		if !r.Passed {
			t.Errorf("test failed: %s\nfailures: %v", r.Desc, r.Failures)
		}
	}
}
```

> Verify the import path of `testrunner.Run` and the exact result-struct field names. Adjust accordingly.

- [ ] **Step 8.2: Run the test**

Run: `go test ./codegen/platform/none/testrunner/ -count=1 -run TestChildrenLiveAcrossMutations -v`
Expected: PASS.

- [ ] **Step 8.3: Commit**

```bash
git add codegen/platform/none/testrunner/runner_test.go
git commit -m "testrunner: unit test for cross-mutation child isolation

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 9: Full verification

- [ ] **Step 9.1: Run all 4 target fixtures**

Run: `go test ./codegen/platform/none/testrunner/... -count=1 -short -run 'TestRunFixtures/test_reactivity_two_components|TestRunFixtures/test_reactivity_extension_method|TestRunFixtures/test_reactivity_global_var|TestRunFixtures/test_mutation_through_method' -v`
Expected: all 4 PASS.

- [ ] **Step 9.2: Run full suite**

Run: `go tool verify`
Expected: clean, OR only pre-existing failures (test_components, test_stmt_refs — both relate to element-ref dispatch inside inlined components, separate concern).

- [ ] **Step 9.3: Build CLI**

Run: `go install ./cmd/sngl`
Expected: success.

- [ ] **Step 9.4: Final cleanup commit if any stragglers**

```bash
git add -A
git commit -m "interp-pull-reactive: final cleanup"
```

---

## Self-Review

**Spec coverage:**
- §Shared state model → Task 1 (cache childEnvs).
- §`componentValue.children` → Tasks 2 + 3 + 5 (body capture, walkChildren, GetField branch).
- §Type-namespace method dispatch → Task 6.
- §Mutation through methods → Task 6 (invokeOnSelf) + Task 7 (handler-scope binding).
- §Tree-shake → dropped per user feedback (lazy eval makes shake redundant for interp).
- §Testing → Task 8 (unit) + Task 9 (4 fixtures).
- §Constraints (for-loop instances out of scope) → respected; Task 3's walkChildren explicitly skips `*ir.For`.

**Placeholder scan:** Task 7 has a diagnose-then-fix step rather than concrete code. That's intentional — the precise fix depends on which path the click trigger takes, and writing both branches without diagnosis would bloat the plan. The decision tree is spelled out.

**Type consistency:**
- `*componentValue` used uniformly across tasks.
- `cv.Env`, `cv.Funcs`, `cv.compName`, `cv.body`, `cv.children` referenced consistently.
- `ComponentEnv`, `RenderNodeProps`, `RenderCallStmtNode` exported with matching signatures across the file boundary.
- `invokeOnSelf(fn, argExprs)` signature consistent between Task 6's implementation and call sites.

**Flagged-for-verification notes:**
- Task 4.3: refactor `collectCallStmtByID` to extract `renderCallStmtMap`. The exact shape of the existing inlined logic determines how much factoring is needed.
- Task 6.3: `EvalUserFunc` signature — verify before writing call sites.
- Task 8.1: `testrunner.Run` signature — verify exact field names in the result struct.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-21-interp-pull-reactive.md`. Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
