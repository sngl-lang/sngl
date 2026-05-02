# Codegen Lowering — Phase 3d (NoDeclarative) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `NoDeclarative` — flatten every `*ir.NodeInst` tree into an explicit sequence of `var __nN <type> = lower.createNode("name")` LocalVars, `#__nN.<key> = <expr>` prop Assigns, lifted `__nN_<event>_handler` Funcs, `lower.attachHandler` calls, and `lower.appendChild` calls. The original NodeInst tree is replaced; control-flow (`If`, `For`, `PlatformFilter`, `SlotInst`, `ErrorBoundary`) is preserved and recursed into.

**Architecture:** Mirrors NoTimer's Func-synthesis pattern — pass state lazily allocates four intrinsic Funcs (`lower.createNode`, `lower.appendChild`, `lower.attachHandler`, plus a placeholder `lower.removeNode` reserved for v2 If-branch lifecycle). The pass first scans existing `NodeInst.ID` values to seed its ID counter past any `__nN` ids that NoReactivity already assigned, then walks every Stmt slice on each Component and Window, replacing NodeInsts with their flat emission. Lifted handler Funcs are appended to the owning Component/Window's `Funcs` slice. No `lib/lower.sngl` checker registration needed in v1 — the synthesized Funcs are referenced directly by Call.Func, matching NoTimer's contract.

**Tech Stack:** Go (Go 1.24+), existing `ir`/`ast`/`internal/checker`/`internal/parser`/`internal/lower` from prior phases.

**Reference spec:** `docs/superpowers/specs/2026-05-02-phase3-ir-shapes-design.md`
**Reference plans:** Phase 3b (`2026-05-02-codegen-lowering-phase3b.md`), Phase 3c (`2026-05-02-codegen-lowering-phase3c.md`).

---

## Scope (v1)

In:
- `*ir.NodeInst` in component bodies, window bodies, and (recursively) nested children.
- Inline event handlers (already lowered into `NodeInst.Handlers` by the checker).
- Reuse of `__nN` IDs assigned by NoReactivity when both caps are on; counter seeded past existing IDs.
- Recursion into `*ir.If`, `*ir.For`, `*ir.PlatformFilter`, `*ir.SlotInst`, `*ir.ErrorBoundary` (bodies/children walked; the wrapping stmt itself stays as IR).
- Composition with NoReactivity output: existing `#__n0.value = expr` Assigns inside handler bodies pass through unchanged when handlers are lifted.

Deferred (each can land as a one-task addendum):
- Per-iteration node keying for `*ir.For` bodies (spec §Risks #3). v1 emits createNode inside the loop body each iteration; downstream platforms handle reconciliation. No `key` arg threaded through `lower.createNode`.
- Conditional subtree removal on `*ir.If` flip false (spec §Risks #4). v1 emits createNode inside the branch body; removal is platform-side.
- `*ir.NodeInst.Ref` binding emission.
- `*ir.NodeInst.Key` for explicit list diff keys.
- Distinct treatment of user-component instantiations vs platform elements (`NodeInst.Component != nil`); v1 emits `lower.createNode("name")` uniformly and the platform decides.
- ErrorBoundary handler lifting (the `@error` handler stays attached to the IR ErrorBoundary stmt; not converted to a `lower.attachHandler` call).
- SlotInst transformation (recursed into but not flattened).

## File Structure

**Create:**
- `internal/lower/testdata/declarative_basic.txtar`
- `internal/lower/testdata/declarative_nested.txtar`
- `internal/lower/testdata/declarative_with_reactivity.txtar`

**Modify:**
- `internal/lower/declarative.go` — replace stub.

---

### Task 1: NoDeclarative implementation

**Files:**
- Modify: `internal/lower/declarative.go`

- [ ] **Step 1: Implement the pass**

Replace `internal/lower/declarative.go` with:

```go
package lower

import (
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

var passDeclarative = pass{
	name:    "NoDeclarative",
	enabled: func(c Caps) bool { return c.NoDeclarative },
	apply:   lowerDeclarative,
}

// lowerDeclarative flattens every visual node tree into an explicit
// sequence of LocalVar (createNode) + Assign (props) + CallStmt (handlers,
// appendChild). Last pass because it destroys the tree shape earlier
// passes rely on.
func lowerDeclarative(pkg *ir.Package) error {
	if pkg == nil {
		return nil
	}
	st := newDeclarativeState()
	st.seedCounter(pkg)
	for _, comp := range pkg.Components {
		comp.Body = st.processStmts(comp.Body, &comp.Funcs)
	}
	for _, w := range pkg.Windows {
		w.Body = st.processStmts(w.Body, &w.Funcs)
	}
	return nil
}

type declarativeState struct {
	create        *ir.Func
	appendChild   *ir.Func
	attachHandler *ir.Func
	nextID        int
}

func newDeclarativeState() *declarativeState { return &declarativeState{} }

// seedCounter scans every NodeInst.ID matching __n<digits> and starts the
// counter past the max. Lets NoDeclarative coexist with NoReactivity's
// pre-assigned IDs without clashing.
func (st *declarativeState) seedCounter(pkg *ir.Package) {
	walkPackage(pkg, walkFuncs{
		stmts: func(stmts []ir.Stmt) []ir.Stmt {
			st.scanStmts(stmts)
			return stmts
		},
	})
}

func (st *declarativeState) scanStmts(stmts []ir.Stmt) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			st.observeID(n.ID)
			st.scanStmts(n.Children)
		case *ir.If:
			st.scanStmts(n.Body)
			st.scanStmts(n.Else)
		case *ir.For:
			st.scanStmts(n.Body)
			st.scanStmts(n.Else)
		case *ir.PlatformFilter:
			st.scanStmts(n.Body)
		case *ir.SlotInst:
			st.scanStmts(n.Children)
		case *ir.ErrorBoundary:
			st.scanStmts(n.Children)
		case *ir.Window:
			st.scanStmts(n.Body)
		}
	}
}

func (st *declarativeState) observeID(id string) {
	if !strings.HasPrefix(id, "__n") {
		return
	}
	n, err := strconv.Atoi(strings.TrimPrefix(id, "__n"))
	if err != nil {
		return
	}
	if n >= st.nextID {
		st.nextID = n + 1
	}
}

func (st *declarativeState) freshID() string {
	id := "__n" + strconv.Itoa(st.nextID)
	st.nextID++
	return id
}

func (st *declarativeState) createFunc() *ir.Func {
	if st.create == nil {
		st.create = &ir.Func{
			Name:      "lower.createNode",
			Intrinsic: "LowerCreateNode",
			Params:    []*ir.Param{{Name: "tag", Type: ir.TypString}},
			Return:    ir.TypDyn,
		}
	}
	return st.create
}

func (st *declarativeState) appendChildFunc() *ir.Func {
	if st.appendChild == nil {
		st.appendChild = &ir.Func{
			Name:      "lower.appendChild",
			Intrinsic: "LowerAppendChild",
			Params: []*ir.Param{
				{Name: "parent", Type: ir.TypDyn},
				{Name: "child", Type: ir.TypDyn},
			},
			Return: ir.TypVoid,
		}
	}
	return st.appendChild
}

func (st *declarativeState) attachHandlerFunc() *ir.Func {
	if st.attachHandler == nil {
		st.attachHandler = &ir.Func{
			Name:      "lower.attachHandler",
			Intrinsic: "LowerAttachHandler",
			Params: []*ir.Param{
				{Name: "node", Type: ir.TypDyn},
				{Name: "event", Type: ir.TypString},
				{Name: "handler", Type: ir.TypDyn},
			},
			Return: ir.TypVoid,
		}
	}
	return st.attachHandler
}

// processStmts walks stmts, replacing each *ir.NodeInst with its flat
// emission and recursing into nested control-flow / handler bodies.
// funcs is the owning Funcs slice for handler lifting.
func (st *declarativeState) processStmts(stmts []ir.Stmt, funcs *[]*ir.Func) []ir.Stmt {
	var out []ir.Stmt
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			out = append(out, st.lowerNode(n, funcs)...)
		case *ir.If:
			n.Body = st.processStmts(n.Body, funcs)
			n.Else = st.processStmts(n.Else, funcs)
			out = append(out, n)
		case *ir.For:
			n.Body = st.processStmts(n.Body, funcs)
			n.Else = st.processStmts(n.Else, funcs)
			out = append(out, n)
		case *ir.PlatformFilter:
			n.Body = st.processStmts(n.Body, funcs)
			out = append(out, n)
		case *ir.SlotInst:
			n.Children = st.processStmts(n.Children, funcs)
			out = append(out, n)
		case *ir.ErrorBoundary:
			n.Children = st.processStmts(n.Children, funcs)
			out = append(out, n)
		default:
			out = append(out, s)
		}
	}
	return out
}

// lowerNode emits the flat sequence for a single NodeInst:
//
//  1. var __nM dyn = lower.createNode("name")
//  2. #__nM.<key> = <propExpr>            (per prop)
//  3. lower.attachHandler(#__nM, "<evt>", __nM_<evt>_handler)  (per handler)
//  4. for each child: emit child's full subtree, then
//     lower.appendChild(#__nM, #__nC)
func (st *declarativeState) lowerNode(n *ir.NodeInst, funcs *[]*ir.Func) []ir.Stmt {
	id := n.ID
	if id == "" {
		id = st.freshID()
		n.ID = id
	}

	var stmts []ir.Stmt

	// 1. createNode
	stmts = append(stmts, &ir.LocalVar{
		Name: id,
		Type: ir.TypDyn,
		Init: &ir.Call{
			Type: ir.TypDyn,
			Func: st.createFunc(),
			Args: []ir.CallArg{
				{Value: &ir.Literal{Type: ir.TypString, Raw: strconv.Quote(n.Name)}},
			},
		},
	})

	// 2. props
	for _, p := range n.Props {
		stmts = append(stmts, &ir.Assign{
			Target: &ir.Select{
				Type:    ir.TypDyn,
				Operand: &ir.Ident{Name: id, Type: ir.TypDyn, IsElementRef: true},
				Field:   p.Name,
			},
			Op:    ast.AssignSet,
			Value: p.Value,
		})
	}

	// 3. handlers — lift each into a named Func owned by the surrounding
	// component/window, then emit attachHandler.
	for i := range n.Handlers {
		h := &n.Handlers[i]
		if h.Func == nil {
			continue
		}
		handlerName := id + "_" + h.Name + "_handler"
		h.Func.Name = handlerName
		*funcs = append(*funcs, h.Func)
		stmts = append(stmts, &ir.CallStmt{
			Call: &ir.Call{
				Type: ir.TypVoid,
				Func: st.attachHandlerFunc(),
				Args: []ir.CallArg{
					{Value: &ir.Ident{Name: id, Type: ir.TypDyn, IsElementRef: true}},
					{Value: &ir.Literal{Type: ir.TypString, Raw: strconv.Quote(h.Name)}},
					{Value: &ir.Ident{Name: handlerName, Type: ir.TypDyn, Sym: h.Func}},
				},
			},
		})
	}

	// 4. children — recurse, then appendChild parent → child.
	for _, c := range n.Children {
		switch cn := c.(type) {
		case *ir.NodeInst:
			stmts = append(stmts, st.lowerNode(cn, funcs)...)
			stmts = append(stmts, &ir.CallStmt{
				Call: &ir.Call{
					Type: ir.TypVoid,
					Func: st.appendChildFunc(),
					Args: []ir.CallArg{
						{Value: &ir.Ident{Name: id, Type: ir.TypDyn, IsElementRef: true}},
						{Value: &ir.Ident{Name: cn.ID, Type: ir.TypDyn, IsElementRef: true}},
					},
				},
			})
		default:
			// Non-NodeInst child (If/For/etc.): recurse via processStmts on
			// a one-element slice. Result inherits parent's child position.
			stmts = append(stmts, st.processStmts([]ir.Stmt{c}, funcs)...)
		}
	}

	return stmts
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./internal/lower/...`
Expected: success.

- [ ] **Step 3: Verify prior goldens still pass**

Run: `go test ./internal/lower/`
Expected: PASS — no existing fixture turns NoDeclarative on.

- [ ] **Step 4: Commit**

```bash
git add internal/lower/declarative.go
git commit -m "$(cat <<'EOF'
NoDeclarative Task 1: pass implementation

Replaces every *ir.NodeInst with var __nM dyn = lower.createNode("name")
plus per-prop Assigns (#__nM.key = expr), lifted handler Funcs, and
lower.attachHandler / lower.appendChild CallStmts. Counter seeds past
any __nN IDs already assigned by NoReactivity so the two passes compose
without clashing. If/For/PlatformFilter/SlotInst/ErrorBoundary are
preserved in IR; the pass recurses into their bodies.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Goldens

**Files:**
- Create: `internal/lower/testdata/declarative_basic.txtar`
- Create: `internal/lower/testdata/declarative_nested.txtar`
- Create: `internal/lower/testdata/declarative_with_reactivity.txtar`

- [ ] **Step 1: Write the basic fixture**

Create `internal/lower/testdata/declarative_basic.txtar`:

```
caps: NoDeclarative
-- input.sngl --
component main {
    text(value="hello")
    button(text="+")
}
-- expected.sngl --
```

- [ ] **Step 2: Write the nested fixture**

Create `internal/lower/testdata/declarative_nested.txtar`:

```
caps: NoDeclarative
-- input.sngl --
component main {
    vbox {
        text(value="a")
        text(value="b")
    }
}
-- expected.sngl --
```

- [ ] **Step 3: Write the reactivity-composition fixture**

Create `internal/lower/testdata/declarative_with_reactivity.txtar`:

```
caps: NoDeclarative, NoReactivity
-- input.sngl --
component main {
    var n int = 0
    text(value=string(n))
    button(text="+", @click { n = n + 1 })
}
-- expected.sngl --
```

This fixture verifies that:
- NoReactivity's pre-assigned `__n0` ID on the `text` node is reused by NoDeclarative (button gets `__n1`, not `__n0`).
- NoReactivity's injected `#__n0.value = string(n)` Assign inside the click handler block survives intact when the handler is lifted to a named Func.

- [ ] **Step 4: Update goldens**

Run: `go test ./internal/lower/ -run TestLower -update`
Expected: PASS.

- [ ] **Step 5: Inspect goldens**

```bash
cat internal/lower/testdata/declarative_basic.txtar
cat internal/lower/testdata/declarative_nested.txtar
cat internal/lower/testdata/declarative_with_reactivity.txtar
```

Expected for `declarative_basic`:

```
component main {
    var __n0 dyn = lower.createNode("text")
    #__n0.value = "hello"
    var __n1 dyn = lower.createNode("button")
    #__n1.text = "+"
}
```

Expected for `declarative_nested`:

```
component main {
    var __n0 dyn = lower.createNode("vbox")
    var __n1 dyn = lower.createNode("text")
    #__n1.value = "a"
    lower.appendChild(#__n0, #__n1)
    var __n2 dyn = lower.createNode("text")
    #__n2.value = "b"
    lower.appendChild(#__n0, #__n2)
}
```

Expected for `declarative_with_reactivity` (handler Funcs render before body stmts per `convertComponent`):

```
component main {
    var n int = 0
    func __n1_click_handler() {
        n = n + 1
        #__n0.value = string(n)
    }
    var __n0 dyn = lower.createNode("text")
    #__n0.value = string(n)
    var __n1 dyn = lower.createNode("button")
    #__n1.text = "+"
    lower.attachHandler(#__n1, "click", __n1_click_handler)
}
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/lower/`
Expected: PASS.

- [ ] **Step 7: Run full suite**

Run: `go test ./...`
Expected: PASS — no other tests turn NoDeclarative on.

- [ ] **Step 8: Commit**

```bash
git add internal/lower/testdata/declarative_basic.txtar internal/lower/testdata/declarative_nested.txtar internal/lower/testdata/declarative_with_reactivity.txtar
git commit -m "$(cat <<'EOF'
NoDeclarative goldens

Three fixtures: bare flat-list (createNode + props per top-level node),
nested vbox (createNode + props per node, appendChild parent→child), and
NoReactivity composition (text gets __n0 from NoReactivity, button gets
__n1 from NoDeclarative; click handler lifts intact with the
NoReactivity-injected #__n0.value updater preserved).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Final verification

- [ ] **Step 1: Run full project verify**

Run: `go tool verify`
Expected: PASS. internal/lower coverage rises from current baseline as the new pass exercises many arms.

- [ ] **Step 2: Confirm Phase 3d shippable**

Phase 3d adds NoDeclarative as the final phase 3 pass. All 9 lowering passes now have working implementations:

```
NoUnit, NoEnum, NoTernary, NoComputed, NoLambda, NoToggle,
NoReactivity, NoTimer, NoDeclarative
```

Phases 4–6 of the master spec (`docs/superpowers/specs/2026-05-02-codegen-lowering-design.md`) port HTML / Fyne / etc. onto lowered IR and delete the legacy MutationModel.

---

## Self-Review Notes

Spec coverage (`docs/superpowers/specs/2026-05-02-phase3-ir-shapes-design.md`):
- §Per-pass behavior / NoDeclarative steps 1–4 → Task 1.
- §Architecture / Node handles — LocalVar with `lower.createNode` Init → Task 1's `lowerNode` step 1. (LocalVar Type is `TypDyn` rather than the original component type as the spec proposed. Reason: keeps platform-element vs user-component handling uniform; NodeInst.Component is often nil for platform elements like `text`/`button`. Refining to TypeComponent for user components is a one-task addendum if downstream codegen needs the prop-set on `__nN.<key>` Selects.)
- §Architecture / Reactive update output — plain Assign with Select target → reuses NoReactivity's output unchanged.
- §Architecture / Codegen contract (intrinsic IDs) → Task 1 emits `LowerCreateNode`, `LowerAppendChild`, `LowerAttachHandler`. `LowerRemoveNode` not emitted in v1; spec §Risks #4 reserves it for a follow-up if a platform needs it.
- §Migration order / Phase 3d row → satisfied by this plan.

Out of scope (deferred per §Scope, tracked separately):
- Per-iteration For-loop node keying (spec §Risks #3) — own design discussion before lowering picks it up.
- Conditional subtree removal on If-flip (spec §Risks #4) — platform-side responsibility for now.
- Ref binding / Key for list diffing.
- ErrorBoundary handler lifting via `lower.attachHandler`.
- SlotInst flattening.
- `lib/lower.sngl` checker registration — v1 synthesizes Funcs directly, matching NoTimer's contract.

Risks (carried forward from spec):

1. **LocalVar Type is `TypDyn`, not the original component type.** Spec proposed `*ir.Type{Kind: TypeComponent, Decl: <componentDecl>}` so downstream Selects against `__nN` resolve to the prop set. v1 picks `TypDyn` because (a) `NodeInst.Component` is nil for platform elements and (b) phase 4 codegen will read prop info via the intrinsic-Func dispatch, not via the LocalVar Type. If this hurts a downstream phase, refine to `TypeComponent` when `NodeInst.Component != nil`.

2. **Counter-seeding correctness.** `seedCounter` scans every NodeInst.ID across the whole package; if any non-`__n` ID happens to start with `__n` followed by digits the heuristic still skips past it, just to be safe. False positives only inflate IDs — never collide.

3. **Handler lift mutates the existing `*ir.Func`.** `h.Func.Name = handlerName` overwrites in place. Originally these Funcs had Name=="" (anonymous lambda/handler form). Naming them is the lift. If any other pass later re-walks `NodeInst.Handlers`, the handler.Func still points at the same Func value — names just stick. Phase 4 codegen reads the lifted Funcs from `Component.Funcs` directly.

4. **Order of lifted handler Funcs vs body stmts in rendered goldens.** `convertComponent` emits Vars → Funcs → Timers → Body. Lifted handlers therefore render *before* the createNode LocalVars in the body. Goldens reflect this; readers should not infer execution order from textual order.

5. **Nested-child flat-emission ordering.** A parent's `appendChild` for child N is emitted after child N's full subtree (including child N's grandchildren and their appendChilds). This guarantees every Ident in an `appendChild(parent, child)` call refers to a LocalVar already declared earlier in the same Stmt slice.

Type consistency:
- `declarativeState`, `freshID`, `seedCounter`, `processStmts`, `lowerNode`, `createFunc`, `appendChildFunc`, `attachHandlerFunc` all defined consistently across the single Task 1 implementation.
- The injected `*ir.Ident{Name: id, IsElementRef: true}` matches the convention from Phase 3b NoReactivity (`internal/lower/reactivity.go:545`) and renders via `convert.go:573` to `#name`.
- Synthesized Func name pattern `__nN_<event>_handler` is one canonical form; `lower.attachHandler` is always called with that name in the third arg.
