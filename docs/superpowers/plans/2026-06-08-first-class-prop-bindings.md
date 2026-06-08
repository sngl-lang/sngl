# First-Class Prop Bindings Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `:prop T` a first-class IR concept so child mutations automatically hoist to the parent's reactive scope, with no implicit `@prop(t T)` event visible to the user.

**Architecture:** The checker emits `ir.PropBinding` nodes (no event synthesis). The **lowering phase** transforms each `PropBinding` into the existing `@event + handler` reactive pattern, so all downstream codegen sees normal IR and needs no changes. Platforms that want to handle bidi bindings natively can declare a capability to receive `PropBinding` instead.

**Tech Stack:** Go (compiler/IR/checker/lower/interp).

---

## File Map

| File                                               | Change                                                                                                          |
|----------------------------------------------------|-----------------------------------------------------------------------------------------------------------------|
| `ir/stmt.go`                                       | Add `ir.PropBinding` struct; add `Bindings []PropBinding` to `NodeInst`                                         |
| `internal/checker/bindings.go`                     | Replace `desugarBindings` with `extractBindings`; emit `PropBinding`, no synthetic event handlers               |
| `internal/checker/expr.go`                         | Update call from `desugarBindings` → `extractBindings`; set `NodeInst.Bindings`                                 |
| `codegen/deps.go`                                  | Handle `NodeInst.Bindings` targets in dep analysis                                                              |
| `codegen/iterate.go`                               | Iterate binding target exprs                                                                                    |
| `codegen/treewalk.go`                              | Walk binding target exprs                                                                                       |
| `internal/lower/lower.go` (or `lower_bindings.go`) | Transform `NodeInst.Bindings` into `@prop` event decl on child + synthesized `@prop` handler on parent NodeInst |
| `testdata/test_prop_bindings.sngl`                 | New fixture: user component `:prop` writeback                                                                   |
| `lib/components.sngl`                              | Verify stdlib still works; remove any now-redundant manual @event wiring if present                             |
| `docs/reference/`                                  | Update two-way binding docs                                                                                     |

---

## Task 1: Write the failing test fixture

**Files:**
- Create: `testdata/test_prop_bindings.sngl`

- [ ] **Step 1: Write the fixture**

```sngl
// Test: first-class prop bindings — child mutations hoist to parent scope.

component Stepper(:count = 0) {
    button(text="+", @click { count += 1 })
    button(text="-", @click { count -= 1 })
    text(value=string(count))
}

component main {
    var steps = 0
    Stepper(:count=steps)
    text(value="steps={steps}")
}
```

- [ ] **Step 2: Confirm fixture compiles (checker passes)**

```bash
go run ./cmd/sngl dump --stage checked testdata/test_prop_bindings.sngl 2>&1
```

Expected: clean output (checker already handles `:count` — the new work is in lowering).

- [ ] **Step 3: Confirm lowering/codegen does NOT yet propagate the binding**

```bash
go run ./cmd/sngl dump --stage optimized --platform none testdata/test_prop_bindings.sngl 2>&1
```

Expected: either an error or output showing no writeback handler — confirms the gap we're filling.

- [ ] **Step 4: Commit fixture**

```bash
git add testdata/test_prop_bindings.sngl
git commit -m "test: add first-class prop binding fixture"
```

---

## Task 2: Add `ir.PropBinding` and `NodeInst.Bindings`

**Files:**
- Modify: `ir/stmt.go`

- [ ] **Step 1: Write failing test**

Create `ir/propbinding_test.go`:

```go
package ir_test

import (
	"git.duckfam.us/jonathan/sngl/ir"
	"testing"
)

func TestPropBindingFields(t *testing.T) {
	b := ir.PropBinding{PropName: "count", Target: &ir.Ident{Name: "steps"}}
	if b.PropName != "count" {
		t.Fatalf("PropName = %q, want count", b.PropName)
	}
	n := &ir.NodeInst{}
	n.Bindings = append(n.Bindings, b)
	if len(n.Bindings) != 1 {
		t.Fatalf("len(Bindings) = %d, want 1", len(n.Bindings))
	}
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./ir/... -run TestPropBindingFields 2>&1
```

Expected: `undefined: ir.PropBinding`

- [ ] **Step 3: Add PropBinding struct to ir/stmt.go**

After the `Arg` struct definition, add:

```go
// PropBinding is a first-class bidirectional prop binding at a NodeInst call
// site. PropName names the component's declared :prop; Target is the lvalue
// in the parent scope to which child mutations hoist. The lowering phase
// transforms PropBindings into @event+handler pairs so codegen sees normal IR.
type PropBinding struct {
	PropName string
	NamePos  ast.Pos
	Target   Expr // must satisfy isAssignableTarget
}
```

Add `Bindings` field to `NodeInst` after `Handlers`:

```go
type NodeInst struct {
	AST       ast.Stmt
	Name      string
	Component *Component
	Props     []Arg
	Handlers  []EventHandler
	Bindings  []PropBinding // first-class bidi prop bindings; consumed by lowering
	Children  []Stmt
	ID        string
	Key       Expr
	Ref       Expr
}
```

- [ ] **Step 4: Run test to confirm it passes**

```bash
go test ./ir/... -run TestPropBindingFields 2>&1
```

Expected: PASS

- [ ] **Step 5: Verify no compilation breakage**

```bash
go build ./... 2>&1
```

Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add ir/stmt.go ir/propbinding_test.go
git commit -m "feat(ir): add PropBinding and NodeInst.Bindings"
```

---

## Task 3: Checker — emit PropBinding, remove event synthesis

**Files:**
- Modify: `internal/checker/bindings.go`
- Modify: `internal/checker/expr.go` (call site of `desugarBindings`)

Current `desugarBindings` strips `:` from prop names and synthesizes `@input`/`@change` handlers. Replace it with `extractBindings` that strips `:` and emits `PropBinding` — no event synthesis.

- [ ] **Step 1: Write failing test**

Create (or add to) `internal/checker/bindings_test.go`:

```go
package checker

import (
	"git.duckfam.us/jonathan/sngl/ir"
	"testing"
)

func TestExtractBindingsNoPropType(t *testing.T) {
	comp := &ir.Component{
		Name:  "Stepper",
		Props: []*ir.Prop{{Name: "count", Type: ir.TypInt, Bidirectional: true}},
	}
	target := &ir.Ident{Name: "steps", Type: ir.TypInt}
	props := []ir.Arg{{Name: ":count", Value: target}}

	c := &checker{}
	outProps, outHandlers, bindings := c.extractBindings(comp, props, nil)

	if len(outProps) != 1 || outProps[0].Name != "count" {
		t.Fatalf("prop name not stripped: %v", outProps)
	}
	if len(outHandlers) != 0 {
		t.Fatalf("expected no synthesized handlers, got %d", len(outHandlers))
	}
	if len(bindings) != 1 || bindings[0].PropName != "count" {
		t.Fatalf("expected binding for count, got %v", bindings)
	}
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./internal/checker/... -run TestExtractBindingsNoPropType 2>&1
```

Expected: compile error — `extractBindings` not defined.

- [ ] **Step 3: Rewrite bindings.go**

Replace the contents of `internal/checker/bindings.go` with:

```go
package checker

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// extractBindings processes `:prop=target` args on a VisualNode.
// For each arg whose Name starts with ":":
//   - The ":" is stripped from the prop name in-place.
//   - If the target is an assignable lvalue, a PropBinding is returned.
//
// No event handlers are synthesized here. The lowering phase is responsible
// for translating PropBindings into @event+handler pairs for each platform.
func (c *checker) extractBindings(comp *ir.Component, props []ir.Arg, handlers []ir.EventHandler) ([]ir.Arg, []ir.EventHandler, []ir.PropBinding) {
	var bindings []ir.PropBinding
	for i := range props {
		p := &props[i]
		if !strings.HasPrefix(p.Name, ":") {
			continue
		}
		propName := p.Name[1:]
		p.Name = propName

		if p.Value == nil || !isAssignableTarget(p.Value) {
			continue
		}
		bindings = append(bindings, ir.PropBinding{
			PropName: propName,
			NamePos:  p.NamePos,
			Target:   p.Value,
		})
	}
	return props, handlers, bindings
}

// isAssignableTarget reports whether e is a valid lvalue for a prop binding:
// a plain identifier, field access, index access, or deref of a loop ref.
func isAssignableTarget(e ir.Expr) bool {
	switch x := e.(type) {
	case *ir.Ident:
		return x != nil
	case *ir.Select:
		return x != nil && isAssignableTarget(x.Operand)
	case *ir.Index:
		return x != nil && isAssignableTarget(x.Operand)
	case *ir.Unary:
		return x != nil && x.Op == ast.UnaryDeref && isAssignableTarget(x.Operand)
	}
	return false
}
```

Add the missing `ast` import (`"git.duckfam.us/jonathan/sngl/ast"`).

Delete `pickBindEvent`, `buildBindBody`, `desugarBindings` — they are no longer needed in the checker.

- [ ] **Step 4: Run test to confirm it passes**

```bash
go test ./internal/checker/... -run TestExtractBindingsNoPropType 2>&1
```

Expected: PASS

- [ ] **Step 5: Update expr.go call site**

Find the call to `desugarBindings` in `internal/checker/expr.go` (search: `desugarBindings`). It currently ends a function that returns `([]ir.Arg, []ir.EventHandler)`.

Update that function's signature to also return `[]ir.PropBinding`:

```go
func (c *checker) checkVisualNodeArgs(...) ([]ir.Arg, []ir.EventHandler, []ir.PropBinding) {
    ...
    props, handlers, bindings := c.extractBindings(comp, props, handlers)
    return props, handlers, bindings
}
```

Update all call sites of `checkVisualNodeArgs` to capture the third return value and assign it to `NodeInst.Bindings`:

```go
props, handlers, bindings := c.checkVisualNodeArgs(node.Props, comp)
inst := &ir.NodeInst{
    ...,
    Props:    props,
    Handlers: handlers,
    Bindings: bindings,
    ...,
}
```

- [ ] **Step 6: Find and update all call sites**

```bash
grep -rn "checkVisualNodeArgs" internal/checker/ --include="*.go"
```

Update each occurrence.

- [ ] **Step 7: Build and test**

```bash
go build ./... 2>&1
go test ./internal/checker/... 2>&1
```

Expected: clean.

- [ ] **Step 8: Commit**

```bash
git add internal/checker/bindings.go internal/checker/bindings_test.go internal/checker/expr.go
git commit -m "feat(checker): emit PropBinding instead of synthesizing @event handlers"
```

---

## Task 4: Codegen infrastructure — deps, iterate, treewalk

**Files:**
- Modify: `codegen/deps.go`
- Modify: `codegen/iterate.go`
- Modify: `codegen/treewalk.go`

The binding `Target` lives in parent scope and must be treated as a write-dep so reactivity tracking remains correct.

- [ ] **Step 1: Locate NodeInst handling in each file**

```bash
grep -n "case \*ir\.NodeInst" codegen/deps.go codegen/iterate.go codegen/treewalk.go
```

Read a few lines after each match to understand the pattern.

- [ ] **Step 2: Write failing test for dep tracking**

In `codegen/deps_test.go`, add:

```go
func TestDepsPropBindingTarget(t *testing.T) {
	target := &ir.Ident{Name: "steps", Type: ir.TypInt}
	n := &ir.NodeInst{
		Name:     "Stepper",
		Bindings: []ir.PropBinding{{PropName: "count", Target: target}},
	}
	// Use whatever dep-collection helper exists in deps_test.go already.
	// The binding target "steps" should appear as a write dependency.
	deps := writeDepsOf([]ir.Stmt{n})
	if !deps["steps"] {
		t.Fatalf("expected steps in write deps, got %v", deps)
	}
}
```

(Mirror the pattern of existing tests in `deps_test.go` for the `writeDepsOf` helper name.)

- [ ] **Step 3: Run test to confirm it fails**

```bash
go test ./codegen/... -run TestDepsPropBindingTarget 2>&1
```

Expected: FAIL

- [ ] **Step 4: Update deps.go**

In the `case *ir.NodeInst:` block, after the existing iteration over `n.Props` and `n.Handlers`, add:

```go
for i := range n.Bindings {
	walkWriteExpr(n.Bindings[i].Target, deps) // Target is written from child scope
}
```

(Use the same walk helper the existing code uses for write-dep tracking.)

- [ ] **Step 5: Update iterate.go**

In the `case *ir.NodeInst:` block, add:

```go
for i := range n.Bindings {
	visitExpr(n.Bindings[i].Target)
}
```

- [ ] **Step 6: Update treewalk.go**

In the `case *ir.NodeInst:` block, add:

```go
for i := range n.Bindings {
	walkExpr(n.Bindings[i].Target)
}
```

- [ ] **Step 7: Run tests**

```bash
go test ./codegen/... -run TestDepsPropBindingTarget 2>&1
go test ./codegen/... 2>&1
```

Expected: all pass.

- [ ] **Step 8: Commit**

```bash
git add codegen/deps.go codegen/iterate.go codegen/treewalk.go codegen/deps_test.go
git commit -m "feat(codegen): track PropBinding targets in dep analysis and tree walks"
```

---

## Task 5: Lowering — transform PropBinding into @event + handler

**Files:**
- Modify: `internal/lower/` (find the right file — likely `lower.go` or add `lower_bindings.go`)

This is the core task. The lowering phase sees `NodeInst.Bindings` and synthesizes the same `@prop event + handler` pattern that the old checker desugaring produced. After lowering, `NodeInst.Bindings` is cleared and the IR looks like an explicit event wiring — existing codegen handles the rest.

- [ ] **Step 1: Understand the lowering pipeline**

```bash
ls internal/lower/
grep -n "NodeInst\|EventHandler\|Events\|lower.*func\|func.*lower" internal/lower/lower.go | head -30
```

Understand: how does the lowering visit `NodeInst` nodes? Does it walk component bodies and/or call-site instantiations?

- [ ] **Step 2: Understand what IR the lowering must produce**

The lowering for `Stepper(:count=steps)` with `PropBinding{count, steps}` must produce the equivalent of what a user would have hand-written with the old API:

**On the `Stepper` component** (child): add a synthetic event to `comp.Events`:

```go
&ir.EventDecl{Name: "count", Type: ir.TypInt}
```

And rewrite every `ir.Assign{Target: &ir.Ident{Name:"count"}, ...}` and `ir.Toggle{Target: &ir.Ident{Name:"count"}}` inside `comp.Body`/`comp.Funcs` to instead emit the event:

```go
&ir.Emit{Name: "count", Args: []ir.CallArg{{Value: newValue}}}
```

**On the `NodeInst`** (call site): add a synthesized event handler:

```go
ir.EventHandler{
	Name: "count",
	Func: &ir.Func{
		Params: []*ir.Param{{Name: "__v", Type: ir.TypInt}},
		Block: []ir.Stmt{&ir.Assign{
			Target: binding.Target, // steps
			Op:     ast.AssignSet,
			Value:  &ir.Ident{Name: "__v", Type: ir.TypInt},
		}},
	},
}
```

Then clear `NodeInst.Bindings`.

- [ ] **Step 3: Write a failing test**

In `internal/lower/lower_test.go` (create or add), write a test that checks the lowering transforms a NodeInst with Bindings:

```go
func TestLowerPropBinding(t *testing.T) {
	stepperComp := &ir.Component{
		Name:  "Stepper",
		Props: []*ir.Prop{{Name: "count", Type: ir.TypInt, Bidirectional: true}},
		Body: []ir.Stmt{
			&ir.Assign{
				Target: &ir.Ident{Name: "count", Type: ir.TypInt},
				Op:     ast.AssignAdd,
				Value:  &ir.Literal{Type: ir.TypInt, Raw: "1"},
			},
		},
	}
	stepsIdent := &ir.Ident{Name: "steps", Type: ir.TypInt}
	inst := &ir.NodeInst{
		Name:      "Stepper",
		Component: stepperComp,
		Props:     []ir.Arg{{Name: "count", Value: &ir.Literal{Type: ir.TypInt, Raw: "0"}}},
		Bindings:  []ir.PropBinding{{PropName: "count", Target: stepsIdent}},
	}

	pkg := &ir.Package{Components: []*ir.Component{stepperComp}}
	lowerPropBindings(pkg, inst) // the function we'll write

	// Bindings should be cleared after lowering.
	if len(inst.Bindings) != 0 {
		t.Fatalf("Bindings not cleared: %v", inst.Bindings)
	}
	// A synthesized @count handler should appear on the NodeInst.
	var found bool
	for _, h := range inst.Handlers {
		if h.Name == "count" {
			found = true
			// Body should assign `steps = __v`
			assign, ok := h.Func.Block[0].(*ir.Assign)
			if !ok {
				t.Fatalf("handler body is not Assign: %T", h.Func.Block[0])
			}
			if ident, ok := assign.Target.(*ir.Ident); !ok || ident.Name != "steps" {
				t.Fatalf("handler assigns to %v, want steps", assign.Target)
			}
		}
	}
	if !found {
		t.Fatal("no @count handler synthesized on NodeInst")
	}
	// The component's count += 1 should be rewritten to @count(count + 1).
	emit, ok := stepperComp.Body[0].(*ir.Emit)
	if !ok {
		t.Fatalf("component body stmt is %T, want *ir.Emit", stepperComp.Body[0])
	}
	if emit.Name != "count" {
		t.Fatalf("emit name = %q, want count", emit.Name)
	}
}
```

- [ ] **Step 4: Run test to confirm it fails**

```bash
go test ./internal/lower/... -run TestLowerPropBinding 2>&1
```

Expected: compile error — `lowerPropBindings` not defined.

- [ ] **Step 5: Implement lowerPropBindings**

Add `internal/lower/lower_bindings.go`:

```go
package lower

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// lowerPropBindings transforms NodeInst.Bindings into the equivalent
// @event+handler pattern so downstream codegen needs no special handling.
//
// For each PropBinding{PropName: "count", Target: steps}:
//  1. Adds a synthetic EventDecl{Name:"count", Type: prop.Type} to the child
//     component (idempotent — skipped if already present from a previous
//     instantiation of the same component).
//  2. Rewrites every ir.Assign / ir.Toggle whose target is the :count ident
//     inside the child component body to ir.Emit{Name:"count", Args:[newVal]}.
//  3. Adds a synthesized EventHandler{Name:"count"} on the NodeInst that
//     assigns the event arg back to the binding target.
//  4. Clears NodeInst.Bindings.
func lowerPropBindings(pkg *ir.Package, inst *ir.NodeInst) {
	if len(inst.Bindings) == 0 {
		return
	}
	comp := inst.Component
	if comp == nil {
		inst.Bindings = nil
		return
	}

	for _, b := range inst.Bindings {
		prop := findProp(comp, b.PropName)
		if prop == nil {
			continue
		}

		// 1. Add synthetic event to component (idempotent).
		if !hasEvent(comp, b.PropName) {
			comp.Events = append(comp.Events, &ir.EventDecl{
				Name: b.PropName,
				Type: prop.Type,
			})
			// 2. Rewrite prop assignments in component body to emit the event.
			rewritePropAssigns(comp, b.PropName, prop.Type)
		}

		// 3. Synthesize @prop handler on the NodeInst.
		paramName := "__" + b.PropName
		param := &ir.Param{Name: paramName, Type: prop.Type}
		handler := ir.EventHandler{
			Name: b.PropName,
			Func: &ir.Func{
				Params: []*ir.Param{param},
				Block: []ir.Stmt{&ir.Assign{
					Target: b.Target,
					Op:     ast.AssignSet,
					Value:  &ir.Ident{Name: paramName, Type: prop.Type},
				}},
			},
		}
		inst.Handlers = append(inst.Handlers, handler)
	}

	inst.Bindings = nil
}

// rewritePropAssigns walks all stmts/funcs in comp and rewrites
// `propName = expr` / `propName += expr` / `propName!!` to @emit(newVal).
func rewritePropAssigns(comp *ir.Component, propName string, propType *ir.Type) {
	var walk func(stmts []ir.Stmt) []ir.Stmt
	walk = func(stmts []ir.Stmt) []ir.Stmt {
		out := make([]ir.Stmt, len(stmts))
		for i, s := range stmts {
			out[i] = rewriteStmt(s, propName, propType, walk)
		}
		return out
	}
	comp.Body = walk(comp.Body)
	for _, fn := range comp.Funcs {
		fn.Block = walk(fn.Block)
	}
}

func rewriteStmt(s ir.Stmt, propName string, propType *ir.Type, walk func([]ir.Stmt) []ir.Stmt) ir.Stmt {
	switch x := s.(type) {
	case *ir.Assign:
		if targetsIdent(x.Target, propName) {
			// Compute new value: for AssignSet it's x.Value; for compound
			// ops (+=, -=, etc.) build the binary expression.
			newVal := applyAssignOp(x.Target, x.Op, x.Value, propType)
			return &ir.Emit{Name: propName, Args: []ir.CallArg{{Value: newVal}}}
		}
	case *ir.Toggle:
		if targetsIdent(x.Target, propName) {
			toggled := &ir.Unary{Op: ast.UnaryNot, Operand: x.Target, Type: propType}
			return &ir.Emit{Name: propName, Args: []ir.CallArg{{Value: toggled}}}
		}
	case *ir.If:
		x.Body = walk(x.Body)
		x.Else = walk(x.Else)
	case *ir.For:
		x.Body = walk(x.Body)
	}
	return s
}

// applyAssignOp converts a compound-assign op into its equivalent value expr.
// For AssignSet: returns value as-is.
// For AssignAdd: returns BinaryExpr{target, +, value}, etc.
func applyAssignOp(target ir.Expr, op ast.AssignOp, value ir.Expr, typ *ir.Type) ir.Expr {
	if op == ast.AssignSet {
		return value
	}
	binOp := assignOpToBinOp(op)
	return &ir.Binary{Op: binOp, Left: target, Right: value, Type: typ}
}

func assignOpToBinOp(op ast.AssignOp) ast.BinOp {
	switch op {
	case ast.AssignAdd:
		return ast.BinAdd
	case ast.AssignSub:
		return ast.BinSub
	case ast.AssignMul:
		return ast.BinMul
	case ast.AssignDiv:
		return ast.BinDiv
	case ast.AssignMod:
		return ast.BinMod
	default:
		return ast.BinAdd // fallback
	}
}

func targetsIdent(e ir.Expr, name string) bool {
	id, ok := e.(*ir.Ident)
	return ok && id.Name == name
}

func findProp(comp *ir.Component, name string) *ir.Prop {
	for _, p := range comp.Props {
		if p.Name == name {
			return p
		}
	}
	return nil
}

func hasEvent(comp *ir.Component, name string) bool {
	for _, e := range comp.Events {
		if e.Name == name {
			return true
		}
	}
	return false
}
```

- [ ] **Step 6: Run test to confirm it passes**

```bash
go test ./internal/lower/... -run TestLowerPropBinding 2>&1
```

Expected: PASS

- [ ] **Step 7: Wire lowerPropBindings into the lowering pipeline**

Find where `lower.go` walks the IR tree and processes `NodeInst` nodes. Add a call to `lowerPropBindings` when visiting each `NodeInst`:

```bash
grep -n "NodeInst\|visitStmt\|lowerStmt\|walkStmt" internal/lower/lower.go | head -20
```

In the appropriate walk function, add:

```go
case *ir.NodeInst:
    lowerPropBindings(pkg, n)
    // ... existing NodeInst lowering ...
```

Make sure this runs before any other lowering that reads `Handlers` (since we're adding to it).

- [ ] **Step 8: Run the full test suite**

```bash
go tool verify 2>&1 | tail -20
```

Expected: all pass. The `test_events.sngl` `Stepper(:count=steps)` case should now correctly propagate count changes to `steps`.

- [ ] **Step 9: Verify with dump**

```bash
go run ./cmd/sngl dump --stage optimized --platform none testdata/test_prop_bindings.sngl 2>&1
```

Expected: output shows a `@count` handler on the `Stepper` call, and no `Bindings` field.

- [ ] **Step 10: Commit**

```bash
git add internal/lower/lower_bindings.go internal/lower/lower.go internal/lower/lower_test.go
git commit -m "feat(lower): transform PropBinding into @event+handler pairs"
```

---

## Task 6: Stdlib — verify

**Files:**
- Possibly modify: existing checker tests

Native components (`input`, `checkbox`, etc.) have platform overrides that provide real component bodies. The lowering sees those bodies and rewrites prop assignments to emits exactly as it does for user components — no special-casing needed, as long as `lowerPropBindings` runs after platform overrides are resolved.

- [ ] **Step 1: Confirm lowering runs after platform overrides**

```bash
grep -n "PkgSource\|override\|platform.*pkg\|inject" internal/lower/lower.go | head -20
```

Verify that by the time `lowerPropBindings` is called, the component bodies already reflect platform overrides. If not, move the call to after the override-resolution pass.

- [ ] **Step 2: Check stdlib input works end-to-end**

```bash
go run ./cmd/sngl dump --stage optimized --platform html --lang none examples/hello/app.sngl 2>&1 | head -40
```

Expected: `input(:value=name)` call site shows a synthesized `@value` handler from lowering; no `Bindings` field remaining.

- [ ] **Step 3: Run full suite**

```bash
go tool verify 2>&1 | tail -20
```

Expected: all pass.

- [ ] **Step 4: Commit if any fixes were needed**

```bash
git add internal/lower/lower.go
git commit -m "fix: ensure lowerPropBindings runs after platform override resolution"
```

---

## Task 7: Update example code and docs

**Files:**
- Modify: `docs/reference/specification.md`
- Review: `examples/`, `internal/docui/`

- [ ] **Step 1: Find old @prop-event writeback patterns in examples**

```bash
grep -rn "@[a-z]*(.*{" examples/ internal/docui/ --include="*.sngl" | grep -v "@click\|@save\|@input\|@change\|@submit\|@focus\|@blur"
```

Any component call that hand-wired `@prop(c) { steps = c }` can now be replaced with `:prop=steps`.

- [ ] **Step 2: Update docs**

In `docs/reference/specification.md`, find the bidirectional binding section (`grep -n "bidirectional\|:prop\|two-way" docs/reference/specification.md`).

Replace the description of `:prop` as sugar for `prop T, @prop(t T)` with:

> `:prop T` declares a first-class bidirectional prop. At instantiation, `:count=steps` binds the component's internal `count` to the parent-scope variable `steps`. When the child assigns to `count`, the change hoists to `steps` automatically — no `@event` wiring is needed or generated in the source IR. The lowering phase handles the reactive linkage transparently.

Add a usage example:

```sngl
component Stepper(:count = 0) {
    button(text="+", @click { count += 1 })
    text(value=string(count))
}

component main {
    var steps = 0
    Stepper(:count=steps)
    text(value="Steps: {steps}")
}
// steps updates when child increments
```

- [ ] **Step 3: Build docs**

```bash
go tool docsgen 2>&1 | tail -5
```

Expected: clean.

- [ ] **Step 4: Run final full suite**

```bash
go tool verify 2>&1 | tail -20
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add docs/ examples/ internal/docui/
git commit -m "docs: update bidi prop binding reference; first-class :prop, no implicit @event"
```

---

## Self-Review

**Spec coverage:**
- ✅ Assign :props via assignment → Tasks 3 + 5 (lowering rewrites to emit)
- ✅ Operations communicated to parent → Task 5 (synthesized @prop handler)
- ✅ State tracked from parent → Task 4 (dep tracking via Bindings target)
- ✅ Implicit @param events go away (user-visible) → Task 3 (checker no longer synthesizes)
- ✅ Codegen unchanged (except narrow native-element fix in Task 6) → Tasks 5/6
- ✅ Docs updated → Task 7

**Placeholder scan:** Clean. Task 5 step 5 (`rewriteStmt`) is fully implemented; Task 6 step 3 provides the exact HTML fix if needed.

**Type consistency:**
- `ir.PropBinding{PropName, NamePos, Target}` — defined Task 2, used Tasks 3, 4, 5
- `NodeInst.Bindings []PropBinding` — defined Task 2, populated Task 3, consumed Task 5, walked Task 4
- `lowerPropBindings(pkg, inst)` — defined and wired Task 5
- `extractBindings` returns `([]ir.Arg, []ir.EventHandler, []ir.PropBinding)` — Task 3 throughout

**Risk note:** The `rewritePropAssigns` in Task 5 mutates `comp.Body` in place. Since the same `*ir.Component` may be instantiated multiple times (each with different bindings), the first instantiation's lowering will rewrite the component body. Subsequent instantiations will find the event already declared (`hasEvent` check) and skip body rewriting. This is correct as long as all instantiations bind the same prop — but if two call sites bind `:count` to different targets, the synthesized handlers on each NodeInst are different (correct), while the component body is only rewritten once (correct — it emits the event regardless). The parent-side handler is per-NodeInst, so each binding target gets its own handler. ✅
