# Codegen Lowering — Phase 3b (NoReactivity) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `NoReactivity` — assign synthetic `__nN` IDs to visual nodes whose props reference reactive vars, and inject `*ir.Assign` stmts of the form `#__nN.key = propExpr` after every mutation that touches a tracked var. Output uses only existing IR shapes; no new intrinsics needed.

**Architecture:** Build a `reverseDeps` map (`*ir.Var → []reactiveProp{NodeID, Key, Expr}`) by walking every visual prop and collecting Ident refs to package/component-scoped reactive vars. Assign IDs to nodes with reactive props as a side effect. Then walk every Stmt slice, finding `*ir.Assign` whose Target's Symbol is a tracked Var; after each, splice in the matching prop-update Assigns from `reverseDeps`.

**Tech Stack:** Go (Go 1.24+), existing `ir`/`ast`/`internal/checker`/`internal/parser`/`internal/lower` from prior phases.

**Reference spec:** `docs/superpowers/specs/2026-05-02-phase3-ir-shapes-design.md`
**Reference plans:** Phase 1 / 2 / 2b / 3a in `docs/superpowers/plans/`

---

## Scope (v1)

In:
- Direct `*ir.Assign{Target: *ir.Ident{Sym: *ir.Var}, ...}` mutations.
- Visual node props (`Arg.Value`) referencing one or more reactive Vars.
- Component-scoped vars and package-scoped vars.

Deferred to follow-up patches (each can land as a one-task addendum):
- Compound-target assignments (`state.foo = ...`, `list[i] = ...`).
- Bidirectional bindings (`@bind:value`).
- Lambda-passed mutations (closures executing assigns).
- Mutations inside imported components.
- Timer.Enabled handling (lives in Phase 3c per spec).

## File Structure

**Create:**
- `internal/lower/testdata/reactivity_counter.txtar`
- `internal/lower/testdata/reactivity_two_props.txtar`
- `internal/lower/testdata/reactivity_in_handler_fn.txtar`

**Modify:**
- `internal/lower/reactivity.go` — replace stub.
- `internal/lower/walk.go` — add helpers if needed (likely not — pass owns its walk).

---

### Task 1: Reactive var collection + ID assignment

**Files:**
- Modify: `internal/lower/reactivity.go`

This task lays the foundation: identify reactive Vars in the package, walk every visual NodeInst, compute the reverse-deps map, and assign synthetic IDs to nodes that have at least one prop referencing a reactive Var.

No mutation injection yet — this task just builds the analysis. The pass apply function leaves the IR otherwise untouched.

- [ ] **Step 1: Implement the helpers and pass body**

Replace `internal/lower/reactivity.go` with:

```go
package lower

import (
	"strconv"

	"git.duckfam.us/jonathan/sngl/ir"
)

var passReactivity = pass{
	name:    "NoReactivity",
	enabled: func(c Caps) bool { return c.NoReactivity },
	apply:   lowerReactivity,
}

// reactiveProp is one (node, prop) pair affected by a reactive var.
type reactiveProp struct {
	NodeID string
	Key    string
	Expr   ir.Expr
}

// reactivityState carries the analysis built up before mutation injection.
type reactivityState struct {
	// reactiveVars is the set of reactive (non-const) Vars whose mutations
	// should trigger updaters.
	reactiveVars map[*ir.Var]bool
	// reverseDeps maps each reactive Var to the (node, prop) pairs that
	// reference it.
	reverseDeps map[*ir.Var][]reactiveProp
	// counter for synthetic node IDs.
	idCounter int
}

func (st *reactivityState) freshNodeID() string {
	id := "__n" + strconv.Itoa(st.idCounter)
	st.idCounter++
	return id
}

// lowerReactivity is the top-level pass entry. Phase 3b implements only the
// analysis + ID assignment side; mutation injection lands in Task 2.
func lowerReactivity(pkg *ir.Package) error {
	if pkg == nil {
		return nil
	}
	st := &reactivityState{
		reactiveVars: collectReactiveVars(pkg),
		reverseDeps:  make(map[*ir.Var][]reactiveProp),
	}
	walkPackage(pkg, walkFuncs{
		stmts: func(stmts []ir.Stmt) []ir.Stmt {
			st.collectFromStmts(stmts)
			return stmts
		},
	})
	// Mutation injection comes in Task 2.
	return nil
}

// collectReactiveVars returns the set of mutable Vars (non-const) declared
// at the package, component, or window level. Lambdas and Locals are not
// reactive.
func collectReactiveVars(pkg *ir.Package) map[*ir.Var]bool {
	out := make(map[*ir.Var]bool)
	for _, v := range pkg.Vars {
		if !v.IsConst {
			out[v] = true
		}
	}
	for _, comp := range pkg.Components {
		for _, v := range comp.Vars {
			if !v.IsConst {
				out[v] = true
			}
		}
	}
	for _, w := range pkg.Windows {
		for _, v := range w.Vars {
			if !v.IsConst {
				out[v] = true
			}
		}
	}
	return out
}

// collectFromStmts walks stmts (recursing into sub-blocks) and records
// reactive props on every NodeInst it encounters. Assigns synthetic IDs as a
// side effect.
func (st *reactivityState) collectFromStmts(stmts []ir.Stmt) {
	for _, s := range stmts {
		st.collectFromStmt(s)
	}
}

func (st *reactivityState) collectFromStmt(s ir.Stmt) {
	switch n := s.(type) {
	case *ir.NodeInst:
		st.collectFromNode(n)
	case *ir.If:
		st.collectFromStmts(n.Body)
		st.collectFromStmts(n.Else)
	case *ir.For:
		st.collectFromStmts(n.Body)
		st.collectFromStmts(n.Else)
	case *ir.PlatformFilter:
		st.collectFromStmts(n.Body)
	case *ir.SlotInst:
		st.collectFromStmts(n.Children)
	case *ir.ErrorBoundary:
		st.collectFromStmts(n.Children)
	case *ir.Window:
		st.collectFromStmts(n.Body)
	}
}

func (st *reactivityState) collectFromNode(n *ir.NodeInst) {
	for _, prop := range n.Props {
		deps := st.exprDeps(prop.Value)
		if len(deps) == 0 {
			continue
		}
		// Ensure this node has an ID we can reference from updaters.
		if n.ID == "" {
			n.ID = st.freshNodeID()
		}
		for v := range deps {
			st.reverseDeps[v] = append(st.reverseDeps[v], reactiveProp{
				NodeID: n.ID,
				Key:    prop.Name,
				Expr:   prop.Value,
			})
		}
	}
	// Recurse into children.
	st.collectFromStmts(n.Children)
}

// exprDeps walks e and returns the set of reactive Vars it reads.
func (st *reactivityState) exprDeps(e ir.Expr) map[*ir.Var]bool {
	out := make(map[*ir.Var]bool)
	st.gatherDeps(e, out)
	return out
}

func (st *reactivityState) gatherDeps(e ir.Expr, out map[*ir.Var]bool) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ir.Ident:
		if v, ok := x.Sym.(*ir.Var); ok && st.reactiveVars[v] {
			out[v] = true
		}
	case *ir.Binary:
		st.gatherDeps(x.Left, out)
		st.gatherDeps(x.Right, out)
	case *ir.Unary:
		st.gatherDeps(x.Operand, out)
	case *ir.Ternary:
		st.gatherDeps(x.Cond, out)
		st.gatherDeps(x.Then, out)
		st.gatherDeps(x.Else, out)
	case *ir.Call:
		if x.Receiver != nil {
			st.gatherDeps(x.Receiver, out)
		}
		for _, a := range x.Args {
			st.gatherDeps(a.Value, out)
		}
	case *ir.Conversion:
		st.gatherDeps(x.Operand, out)
	case *ir.Select:
		st.gatherDeps(x.Operand, out)
	case *ir.Index:
		st.gatherDeps(x.Operand, out)
		st.gatherDeps(x.Idx, out)
	case *ir.ListLit:
		for _, el := range x.Elems {
			st.gatherDeps(el, out)
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			st.gatherDeps(f.Value, out)
		}
	case *ir.Spread:
		st.gatherDeps(x.Operand, out)
	}
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./internal/lower/...`
Expected: success.

- [ ] **Step 3: Verify Phase 1+2 goldens still pass**

Run: `go test ./internal/lower/`
Expected: PASS — Task 1 leaves IR untouched, so all existing fixtures still produce the same output.

- [ ] **Step 4: Commit**

```bash
git add internal/lower/reactivity.go
git commit -m "$(cat <<'EOF'
NoReactivity Task 1: reactive var collection + node ID assignment

Walks every visual NodeInst in the package, records (Var → [reactiveProp])
reverse-deps for any prop whose expression references a reactive Var,
and assigns synthetic __nN IDs to nodes that need them. No mutation
injection yet — that lands in Task 2. Existing IR shapes preserved;
all prior goldens continue to pass.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Mutation injection

**Files:**
- Modify: `internal/lower/reactivity.go`

This task plugs the analysis from Task 1 into a second walk that finds `*ir.Assign` mutation sites and splices in the matching prop-update Assigns.

- [ ] **Step 1: Extend lowerReactivity to inject mutations**

Replace the body of `lowerReactivity` and add the injection logic. Final form of `internal/lower/reactivity.go`:

```go
package lower

import (
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

var passReactivity = pass{
	name:    "NoReactivity",
	enabled: func(c Caps) bool { return c.NoReactivity },
	apply:   lowerReactivity,
}

type reactiveProp struct {
	NodeID string
	Key    string
	Expr   ir.Expr
}

type reactivityState struct {
	reactiveVars map[*ir.Var]bool
	reverseDeps  map[*ir.Var][]reactiveProp
	idCounter    int
}

func (st *reactivityState) freshNodeID() string {
	id := "__n" + strconv.Itoa(st.idCounter)
	st.idCounter++
	return id
}

func lowerReactivity(pkg *ir.Package) error {
	if pkg == nil {
		return nil
	}
	st := &reactivityState{
		reactiveVars: collectReactiveVars(pkg),
		reverseDeps:  make(map[*ir.Var][]reactiveProp),
	}
	// Pass 1: collect reverse deps + assign IDs.
	walkPackage(pkg, walkFuncs{
		stmts: func(stmts []ir.Stmt) []ir.Stmt {
			st.collectFromStmts(stmts)
			return stmts
		},
	})
	// Pass 2: inject mutation updaters.
	walkPackage(pkg, walkFuncs{
		stmts: func(stmts []ir.Stmt) []ir.Stmt {
			return st.injectIntoStmts(stmts)
		},
	})
	return nil
}

func collectReactiveVars(pkg *ir.Package) map[*ir.Var]bool {
	out := make(map[*ir.Var]bool)
	for _, v := range pkg.Vars {
		if !v.IsConst {
			out[v] = true
		}
	}
	for _, comp := range pkg.Components {
		for _, v := range comp.Vars {
			if !v.IsConst {
				out[v] = true
			}
		}
	}
	for _, w := range pkg.Windows {
		for _, v := range w.Vars {
			if !v.IsConst {
				out[v] = true
			}
		}
	}
	return out
}

func (st *reactivityState) collectFromStmts(stmts []ir.Stmt) {
	for _, s := range stmts {
		st.collectFromStmt(s)
	}
}

func (st *reactivityState) collectFromStmt(s ir.Stmt) {
	switch n := s.(type) {
	case *ir.NodeInst:
		st.collectFromNode(n)
	case *ir.If:
		st.collectFromStmts(n.Body)
		st.collectFromStmts(n.Else)
	case *ir.For:
		st.collectFromStmts(n.Body)
		st.collectFromStmts(n.Else)
	case *ir.PlatformFilter:
		st.collectFromStmts(n.Body)
	case *ir.SlotInst:
		st.collectFromStmts(n.Children)
	case *ir.ErrorBoundary:
		st.collectFromStmts(n.Children)
	case *ir.Window:
		st.collectFromStmts(n.Body)
	}
}

func (st *reactivityState) collectFromNode(n *ir.NodeInst) {
	for _, prop := range n.Props {
		deps := st.exprDeps(prop.Value)
		if len(deps) == 0 {
			continue
		}
		if n.ID == "" {
			n.ID = st.freshNodeID()
		}
		for v := range deps {
			st.reverseDeps[v] = append(st.reverseDeps[v], reactiveProp{
				NodeID: n.ID,
				Key:    prop.Name,
				Expr:   prop.Value,
			})
		}
	}
	st.collectFromStmts(n.Children)
}

func (st *reactivityState) exprDeps(e ir.Expr) map[*ir.Var]bool {
	out := make(map[*ir.Var]bool)
	st.gatherDeps(e, out)
	return out
}

func (st *reactivityState) gatherDeps(e ir.Expr, out map[*ir.Var]bool) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ir.Ident:
		if v, ok := x.Sym.(*ir.Var); ok && st.reactiveVars[v] {
			out[v] = true
		}
	case *ir.Binary:
		st.gatherDeps(x.Left, out)
		st.gatherDeps(x.Right, out)
	case *ir.Unary:
		st.gatherDeps(x.Operand, out)
	case *ir.Ternary:
		st.gatherDeps(x.Cond, out)
		st.gatherDeps(x.Then, out)
		st.gatherDeps(x.Else, out)
	case *ir.Call:
		if x.Receiver != nil {
			st.gatherDeps(x.Receiver, out)
		}
		for _, a := range x.Args {
			st.gatherDeps(a.Value, out)
		}
	case *ir.Conversion:
		st.gatherDeps(x.Operand, out)
	case *ir.Select:
		st.gatherDeps(x.Operand, out)
	case *ir.Index:
		st.gatherDeps(x.Operand, out)
		st.gatherDeps(x.Idx, out)
	case *ir.ListLit:
		for _, el := range x.Elems {
			st.gatherDeps(el, out)
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			st.gatherDeps(f.Value, out)
		}
	case *ir.Spread:
		st.gatherDeps(x.Operand, out)
	}
}

// injectIntoStmts walks stmts, splicing updater Assigns after every Assign
// that mutates a tracked Var. Recurses into nested blocks (handler funcs,
// if/for bodies, etc.).
func (st *reactivityState) injectIntoStmts(stmts []ir.Stmt) []ir.Stmt {
	var out []ir.Stmt
	for _, s := range stmts {
		out = append(out, s)
		// Recurse into nested Stmt slices.
		switch n := s.(type) {
		case *ir.If:
			n.Body = st.injectIntoStmts(n.Body)
			n.Else = st.injectIntoStmts(n.Else)
		case *ir.For:
			n.Body = st.injectIntoStmts(n.Body)
			n.Else = st.injectIntoStmts(n.Else)
		case *ir.PlatformFilter:
			n.Body = st.injectIntoStmts(n.Body)
		case *ir.NodeInst:
			n.Children = st.injectIntoStmts(n.Children)
			for i := range n.Handlers {
				if n.Handlers[i].Func != nil {
					n.Handlers[i].Func.Block = st.injectIntoStmts(n.Handlers[i].Func.Block)
				}
			}
		case *ir.SlotInst:
			n.Children = st.injectIntoStmts(n.Children)
		case *ir.ErrorBoundary:
			n.Children = st.injectIntoStmts(n.Children)
			if n.Handler != nil && n.Handler.Func != nil {
				n.Handler.Func.Block = st.injectIntoStmts(n.Handler.Func.Block)
			}
		case *ir.Window:
			n.Body = st.injectIntoStmts(n.Body)
			for _, fn := range n.Funcs {
				fn.Block = st.injectIntoStmts(fn.Block)
			}
			for _, v := range n.Vars {
				for _, h := range v.Handlers {
					if h.Func != nil {
						h.Func.Block = st.injectIntoStmts(h.Func.Block)
					}
				}
			}
		}
		// After the stmt, splice updaters for mutated vars.
		if updaters := st.updatersFor(s); len(updaters) > 0 {
			out = append(out, updaters...)
		}
	}
	return out
}

// updatersFor returns the list of *ir.Assign updaters to splice after s.
// Empty for stmts that don't mutate a tracked Var.
func (st *reactivityState) updatersFor(s ir.Stmt) []ir.Stmt {
	a, ok := s.(*ir.Assign)
	if !ok {
		return nil
	}
	id, ok := a.Target.(*ir.Ident)
	if !ok {
		return nil
	}
	v, ok := id.Sym.(*ir.Var)
	if !ok {
		return nil
	}
	props, ok := st.reverseDeps[v]
	if !ok {
		return nil
	}
	var out []ir.Stmt
	for _, p := range props {
		out = append(out, &ir.Assign{
			Target: &ir.Select{
				Type:    ir.TypDyn,
				Operand: &ir.Ident{Name: p.NodeID, Type: ir.TypDyn, IsElementRef: true},
				Field:   p.Key,
			},
			Op:    ast.AssignSet,
			Value: p.Expr,
		})
	}
	return out
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./internal/lower/...`
Expected: success.

- [ ] **Step 3: Verify prior goldens still pass**

Run: `go test ./internal/lower/`
Expected: PASS — no caps test yet exercises NoReactivity, so prior fixtures unchanged.

- [ ] **Step 4: Commit**

```bash
git add internal/lower/reactivity.go
git commit -m "$(cat <<'EOF'
NoReactivity Task 2: inject prop-update Assigns at mutation sites

Two-pass apply: first pass builds reverseDeps + assigns IDs, second
pass walks every Stmt slice splicing #__nN.key = propExpr Assigns
after each *ir.Assign whose Target Symbol is a tracked Var. Output
uses only existing IR shapes (Assign + Select + ElementRef Ident).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Goldens

**Files:**
- Create: `internal/lower/testdata/reactivity_counter.txtar`
- Create: `internal/lower/testdata/reactivity_two_props.txtar`
- Create: `internal/lower/testdata/reactivity_in_handler_fn.txtar`

- [ ] **Step 1: Write the three fixtures**

Create `internal/lower/testdata/reactivity_counter.txtar`:

```
caps: NoReactivity
-- input.sngl --
component main {
    var n int = 0
    text(value=string(n))
    button(text="+", @click { n = n + 1 })
}
-- expected.sngl --
```

Create `internal/lower/testdata/reactivity_two_props.txtar`:

```
caps: NoReactivity
-- input.sngl --
component main {
    var n int = 0
    text(value=string(n))
    text(value=string(n + 1))
    button(text="+", @click { n = n + 1 })
}
-- expected.sngl --
```

Create `internal/lower/testdata/reactivity_in_handler_fn.txtar`:

```
caps: NoReactivity
-- input.sngl --
component main {
    var n int = 0

    func bump() {
        n = n + 1
    }

    text(value=string(n))
    button(text="bump", @click { bump() })
}
-- expected.sngl --
```

The third fixture verifies that mutation injection finds Assigns inside any function block, not only inline `@click` handlers.

- [ ] **Step 2: Update goldens**

Run: `go test ./internal/lower/ -run TestLower -update`
Expected: PASS.

- [ ] **Step 3: Inspect goldens**

```bash
cat internal/lower/testdata/reactivity_counter.txtar
```

Expected pattern:

```
component main {
    var n int = 0
    #__n0: text(value=string(n))
    button(text="+", @click {
        n = n + 1
        #__n0.value = string(n)
    })
}
```

For `reactivity_two_props`: both text nodes get IDs (`#__n0`, `#__n1`), and the click handler emits two trailing Assigns — one per affected node. Order matches the order in which nodes were visited.

For `reactivity_in_handler_fn`: the trailing Assign appears inside `func bump() { ... }`, after `n = n + 1`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/lower/`
Expected: PASS.

- [ ] **Step 5: Run full suite**

Run: `go test ./...`
Expected: PASS — no other tests turn NoReactivity on.

- [ ] **Step 6: Commit**

```bash
git add internal/lower/testdata/reactivity_counter.txtar internal/lower/testdata/reactivity_two_props.txtar internal/lower/testdata/reactivity_in_handler_fn.txtar
git commit -m "$(cat <<'EOF'
NoReactivity goldens

Three fixtures: bare counter, two-prop binding (both nodes get IDs +
trailing Assigns), and mutation inside a named function (bump() called
from @click). Goldens render the lowered form using ElementRef syntax
(#__nN.key = expr) so the output is valid SNGL roundtripping cleanly
through ir.Convert + parser.Format.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Final verification

- [ ] **Step 1: Run the full project verify**

Run: `go tool verify`
Expected: PASS. internal/lower coverage rises from 51.9% as the new pass adds many exercised arms.

- [ ] **Step 2: Confirm Phase 3b is shippable**

Phase 3b adds NoReactivity in a self-contained way; output uses only existing IR shapes, no `lib/lower.sngl` required. Phase 3c (NoTimer) and Phase 3d (NoDeclarative) build on top, each with their own implementation plan.

---

## Self-Review Notes

Spec coverage (`docs/superpowers/specs/2026-05-02-phase3-ir-shapes-design.md`):
- §Per-pass behavior / NoReactivity step 1 (ID synthesis) → Task 1.
- §Per-pass behavior / NoReactivity step 2 (Dataflow) → Task 1's reverseDeps + exprDeps.
- §Per-pass behavior / NoReactivity step 3 (Mutation injection) → Task 2.
- §Reactive update output — plain Assign with Select target → Task 2's `updatersFor`.

Out of scope (in the plan's own §Scope and tracked separately):
- Compound-target assignments (`state.foo = ...`) — common case; track as Phase 3b.1 if a real fixture surfaces a need.
- Bidirectional bindings — own design discussion before lowering picks them up.
- Lambda-passed mutations — depends on NoLambda landing first.
- Timer.Enabled handling — spec assigns it to NoTimer (Phase 3c).

Risks (carried forward from spec):
1. **Synthesized Sym on injected Idents.** The injected `*ir.Ident{Name: "__n0", IsElementRef: true, Type: TypDyn}` has nil Sym. `ir.Convert` reads only `IsElementRef` and `Name` — no Sym needed for SNGL roundtrip. Codegen consumption (Phase 4+) needs Sym threading; track on the Phase 4 plan.
2. **Order of injected updaters.** Iterating a `map[*ir.Var][]reactiveProp` is order-stable per insertion sequence, but Go map iteration is randomized. This could cause golden flakiness. Mitigation: tracking is by slice (not map) per Var, so per-Var ordering is stable; cross-Var ordering depends on collection traversal which is deterministic (component declaration order).
3. **Same expr referenced from multiple injection sites.** The injected `*ir.Assign{Value: p.Expr}` shares the same `ir.Expr` pointer with the original NodeInst's prop. Subsequent passes (and future codegen) must treat these expressions as immutable. Today the same convention holds for Toggle's Target sharing; flag if a future pass mutates expressions in place.

Type consistency:
- `reactivityState`, `reactiveProp`, `collectReactiveVars`, `freshNodeID`, `injectIntoStmts`, `updatersFor` all defined consistently across Tasks 1 and 2.
- The injected Ident uses `IsElementRef: true` matching the checker's existing convention for `#id` references (`internal/checker/expr.go:89`).
