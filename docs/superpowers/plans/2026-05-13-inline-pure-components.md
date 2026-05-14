# Inline Pure Components Implementation Plan (Plan G)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `passInlinePure` to `internal/lower` — substitutes pure-component calls with their inlined bodies (params, slot, events). Always-on for user-defined pure components; strict mode via `Caps.NoStdlibWrappers` for platform-stdlib wrappers. After enabling on gtk4 and html, delete `gtk4TagToCType`/`gtk4Constructor`/`htmlTagToDOM` translator switches.

**Architecture:** Single pass between `passReactivity` and `passTimer`. One substitution engine drives both modes. Detects pure components structurally (no Vars/Funcs/Timers) for optimization mode; consults Caps + import scheme for strict mode. fyne untouched — runs under strict mode with no behavior change since its wrapper bodies are already pure blueprint-bearing NodeInsts.

**Tech Stack:** Go, the existing `internal/lower/` pass framework, `*ir.Component`, `*ir.NodeInst`, `*ir.SlotInst`, `*ir.Emit`, `*ir.Param`.

**Spec:** `docs/superpowers/specs/2026-05-13-inline-pure-components-design.md`.

**Predecessors:** Plans A, B, B.2, C, F (all on `main`). Plan D paused mid-execution; Plan G unblocks it.

**Successor:** Resume Plan D Tasks 10-17 with `htmlTagToDOM` removed.

---

## File Structure

**Create:**
- `internal/lower/inline_pure.go` — pass entry + substitution engine.
- `internal/lower/inline_pure_test.go` — unit tests.
- `internal/lower/testdata/inline_pure_basic.txtar`
- `internal/lower/testdata/inline_pure_with_slot.txtar`
- `internal/lower/testdata/inline_pure_event.txtar`
- `internal/lower/testdata/inline_pure_recursion.txtar`
- `internal/lower/testdata/inline_pure_impure_skipped.txtar`
- `internal/lower/testdata/inline_strict_impure_errors.txtar`
- `internal/lower/testdata/inline_strict_clean.txtar`

**Modify:**
- `internal/lower/caps.go` — add `NoStdlibWrappers` flag.
- `internal/lower/lower.go` — register pass in execution order.
- `codegen/platform/fyne/fyne.go` — `Capabilities()` adds `NoStdlibWrappers`.
- `codegen/platform/gtk4/gtk4.go` — `Capabilities()` adds `NoStdlibWrappers`.
- `codegen/platform/html/html.go` — `Capabilities()` adds `NoStdlibWrappers`.
- `codegen/platform/gtk4/intrinsic_translator.go` — delete `gtk4TagToCType` + `gtk4Constructor` switches once pass enabled.
- `codegen/platform/html/intrinsic_translator.go` — delete `htmlTagToDOM` once pass enabled.

---

## Phase A: Pass scaffold

### Task 1: Add `Caps.NoStdlibWrappers` flag

**Files:**
- Modify: `internal/lower/caps.go`

- [x] **Step 1: Add field**

In `internal/lower/caps.go`, find the `Caps` struct. Add:

```go
NoStdlibWrappers bool // Inline platform-stdlib wrapper components; fail if any wrapper is impure.
```

Update `Caps.Merge` to OR the new flag, and `Caps.String` to include it.

- [x] **Step 2: Build**

Run: `go build ./...`
Expected: clean.

- [x] **Step 3: Commit**

```bash
git add internal/lower/caps.go
git commit -m "lower(caps): add NoStdlibWrappers flag

Platform opt-in for the inline-pure-components strict mode that
inlines platform-stdlib wrapper-component calls. Pass is added in
the next task; this flag is dormant until then.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Pass scaffold + always-on optimization mode for user pure components

**Files:**
- Create: `internal/lower/inline_pure.go`

- [x] **Step 1: Write the file**

Create `internal/lower/inline_pure.go`:

```go
package lower

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passInlinePure substitutes pure-component calls with their inlined
// bodies. Pure = no Vars, no Funcs, no Timers.
//
// Two modes share the substitution engine:
//   - Optimization (always-on): inlines user-defined pure components.
//   - Strict (Caps.NoStdlibWrappers): inlines platform-stdlib wrappers
//     and errors if any platform-stdlib component is impure.
//
// Runs between passReactivity and passTimer. After passReactivity so
// reactive deps wire against user-level props before inlining flattens
// them; before passDeclarative so the inlined native NodeInsts get
// flattened along with everything else.
var passInlinePure = pass{
	name:    "InlinePure",
	enabled: func(c Caps) bool { return true }, // always on (strict path gated internally)
	apply:   lowerInlinePure,
}

func lowerInlinePure(pkg *ir.Package, caps Caps) error {
	if pkg == nil {
		return nil
	}
	st := &inlinePureState{
		pkg:        pkg,
		strictMode: caps.NoStdlibWrappers,
		inFlight:   map[*ir.Component]bool{},
	}
	for _, comp := range pkg.Components {
		body, err := st.inlineStmts(comp.Body)
		if err != nil {
			return err
		}
		comp.Body = body
	}
	for _, w := range pkg.Windows {
		body, err := st.inlineStmts(w.Body)
		if err != nil {
			return err
		}
		w.Body = body
	}
	return nil
}

type inlinePureState struct {
	pkg        *ir.Package
	strictMode bool
	inFlight   map[*ir.Component]bool
}

// isPure reports whether a component is structurally pure (no internal
// state). nil component → false.
func (st *inlinePureState) isPure(c *ir.Component) bool {
	if c == nil {
		return false
	}
	return len(c.Vars) == 0 && len(c.Funcs) == 0 && len(c.Timers) == 0
}

// inlineStmts walks a stmt slice, recursing into nested control-flow
// bodies and NodeInst children/handlers, and inlines eligible
// NodeInst → component calls in place.
func (st *inlinePureState) inlineStmts(stmts []ir.Stmt) ([]ir.Stmt, error) {
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		replaced, err := st.inlineStmt(s)
		if err != nil {
			return nil, err
		}
		out = append(out, replaced...)
	}
	return out, nil
}

// inlineStmt processes one stmt. Returns the slice of replacement stmts
// (may be one or many).
func (st *inlinePureState) inlineStmt(s ir.Stmt) ([]ir.Stmt, error) {
	switch n := s.(type) {
	case *ir.NodeInst:
		return st.inlineNodeInst(n)
	case *ir.If:
		body, err := st.inlineStmts(n.Body)
		if err != nil {
			return nil, err
		}
		els, err := st.inlineStmts(n.Else)
		if err != nil {
			return nil, err
		}
		n.Body = body
		n.Else = els
		return []ir.Stmt{n}, nil
	case *ir.For:
		body, err := st.inlineStmts(n.Body)
		if err != nil {
			return nil, err
		}
		els, err := st.inlineStmts(n.Else)
		if err != nil {
			return nil, err
		}
		n.Body = body
		n.Else = els
		return []ir.Stmt{n}, nil
	case *ir.PlatformFilter:
		body, err := st.inlineStmts(n.Body)
		if err != nil {
			return nil, err
		}
		n.Body = body
		return []ir.Stmt{n}, nil
	case *ir.SlotInst:
		ch, err := st.inlineStmts(n.Children)
		if err != nil {
			return nil, err
		}
		n.Children = ch
		return []ir.Stmt{n}, nil
	}
	return []ir.Stmt{s}, nil
}

// inlineNodeInst decides whether to inline n. If yes, runs the
// substitution engine and returns the substituted stmts. If no,
// recurses into n's children/handlers and returns n unchanged.
func (st *inlinePureState) inlineNodeInst(n *ir.NodeInst) ([]ir.Stmt, error) {
	// Recurse first so nested calls inline bottom-up.
	children, err := st.inlineStmts(n.Children)
	if err != nil {
		return nil, err
	}
	n.Children = children
	for _, h := range n.Handlers {
		if h.Func == nil {
			continue
		}
		body, err := st.inlineStmts(h.Func.Block)
		if err != nil {
			return nil, err
		}
		h.Func.Block = body
	}

	comp := n.Component
	if comp == nil {
		return []ir.Stmt{n}, nil
	}

	// Decide eligibility.
	pure := st.isPure(comp)
	strictApplies := st.strictMode && isPlatformStdlibComponent(st.pkg, comp)
	if !pure && !strictApplies {
		return []ir.Stmt{n}, nil
	}
	if strictApplies && !pure {
		return nil, fmt.Errorf("platform stdlib wrapper %q must be pure (declares %s)", comp.Name, impurityReason(comp))
	}

	// Cycle check.
	if st.inFlight[comp] {
		return nil, fmt.Errorf("inline cycle in component %q at %v", comp.Name, n.AST)
	}
	st.inFlight[comp] = true
	defer delete(st.inFlight, comp)

	// Substitute.
	body, err := st.substitute(comp, n)
	if err != nil {
		return nil, err
	}
	// Recurse on substituted body (the wrapper's body may itself contain
	// pure-component calls that need inlining).
	return st.inlineStmts(body)
}

// impurityReason returns a short string describing why comp is impure.
// Caller has already established len(Vars|Funcs|Timers) > 0.
func impurityReason(comp *ir.Component) string {
	var parts []string
	if len(comp.Vars) > 0 {
		parts = append(parts, fmt.Sprintf("var %q", comp.Vars[0].Name))
	}
	if len(comp.Funcs) > 0 {
		parts = append(parts, fmt.Sprintf("func %q", comp.Funcs[0].Name))
	}
	if len(comp.Timers) > 0 {
		parts = append(parts, "timer")
	}
	return strings.Join(parts, ", ")
}

// isPlatformStdlibComponent reports whether comp came from one of the
// package's platform:// imports.
func isPlatformStdlibComponent(pkg *ir.Package, comp *ir.Component) bool {
	for _, imp := range pkg.Imports {
		if !strings.HasPrefix(imp.Path, "platform://") {
			continue
		}
		if imp.Pkg == nil {
			continue
		}
		for _, c := range imp.Pkg.Components {
			if c == comp {
				return true
			}
		}
	}
	return false
}

// substitute applies the three substitutions (params, slot, events) to
// the wrapper's body, returning a fresh stmt slice ready to splice into
// the caller's position.
func (st *inlinePureState) substitute(comp *ir.Component, callsite *ir.NodeInst) ([]ir.Stmt, error) {
	if len(comp.Body) == 0 {
		return nil, fmt.Errorf("component %q has no body to inline", comp.Name)
	}
	// Implementation: Task 3 fills this in.
	return nil, fmt.Errorf("substitute: not implemented")
}
```

- [x] **Step 2: Register the pass**

In `internal/lower/lower.go`, find the `passes` slice. Insert `passInlinePure` between `passReactivity` and `passTimer`:

```go
var passes = []pass{
	passUnit,
	passEnum,
	passTernary,
	passAsyncReactive,
	passComputed,
	passLambda,
	passToggle,
	passReactivity,
	passInlinePure,    // NEW
	passTimer,
	passDeclarative,
	passNoRef,
}
```

Update the comment block above `passes` to mention `InlinePure` between Reactivity and Timer.

- [x] **Step 3: Build**

Run: `go build ./...`
Expected: clean.

Run: `go test ./internal/lower/...`
Expected: PASS — pass is registered but its substitute() returns an error only when inlining is actually attempted. Existing tests don't exercise pure components inside test fixtures (yet), so no failure.

If a fixture happens to contain a pure component call, `substitute: not implemented` fires. Investigate and update the fixture to either (a) skip its inlining by ensuring components have state, or (b) wait for Task 3 to complete.

- [x] **Step 4: Commit**

```bash
git add internal/lower/inline_pure.go internal/lower/lower.go
git commit -m "lower: scaffold passInlinePure (no substitution yet)

Pass is registered between passReactivity and passTimer. Detects
eligible NodeInst → component sites; the substitution engine in
Task 3 fills in the body. Returns a placeholder error if any
fixture trips the substitution path before Task 3 lands.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase B: Substitution engine

### Task 3: Implement param-substitution + slot-substitution

**Files:**
- Modify: `internal/lower/inline_pure.go`
- Test: `internal/lower/testdata/inline_pure_basic.txtar` (create)

- [x] **Step 1: Create golden fixture**

Create `internal/lower/testdata/inline_pure_basic.txtar`:

```
caps: ""
-- input.sngl --
component greet(name string) {
    text(value=$"Hello, {name}!")
}

component main {
    greet(name="Alice")
}
-- expected.sngl --
component main {
    text(value=$"Hello, {name}!")
}
```

Hmm, actually the `name` inside the interpolation should substitute to "Alice". So expected:

```
component main {
    text(value=$"Hello, Alice!")
}
```

But this depends on whether the inline pass also collapses literal interpolation. It probably DOESN'T (that's an optimizer's job). So expected:

```
component main {
    text(value=$"Hello, {name}!")
}
```

where `name` resolves at runtime to "Alice" via param substitution producing a string literal `"Alice"` directly in the value expression. Either form is acceptable depending on how the substitution renders.

Use the form the implementation actually produces — run the golden in `-update` mode to populate after Step 3-4 complete.

- [x] **Step 2: Implement substitute()**

In `internal/lower/inline_pure.go`, replace the `substitute` stub with:

```go
func (st *inlinePureState) substitute(comp *ir.Component, callsite *ir.NodeInst) ([]ir.Stmt, error) {
	if len(comp.Body) == 0 {
		return nil, fmt.Errorf("component %q has no body to inline", comp.Name)
	}

	// Build param-binding map: paramName → user's bound arg expression.
	bindings := map[string]ir.Expr{}
	for _, p := range comp.AST.Props.Props {
		// Find the user's matching CallArg by name.
		for _, prop := range callsite.Props {
			if prop.Name == p.Name {
				bindings[p.Name] = prop.Value
				break
			}
		}
		// Missing args → param's Default (if AST carries one) or zero
		// expression. For now, leave unbound (substitute leaves the
		// ident as-is); the checker should have caught missing
		// required props.
	}

	// Deep-clone the wrapper body so substitution mutations don't
	// leak across call sites.
	body := cloneStmts(comp.Body)

	// Apply param substitution (walk body, replace Ident-with-Param-Sym
	// with the user's bound expression).
	body = substituteParams(body, bindings, comp)

	// Apply slot substitution: replace each *ir.SlotInst with the
	// user's children.
	body = substituteSlots(body, callsite.Children)

	// Apply event-invocation substitution: replace each *ir.Emit (or
	// equivalent shape) whose Name matches an event param with the
	// user's handler body.
	body = substituteEvents(body, callsite.Handlers)

	// ID preservation: transfer callsite.ID to the first top-level
	// NodeInst of the substituted body.
	if callsite.ID != "" {
		for _, s := range body {
			if ni, ok := s.(*ir.NodeInst); ok {
				ni.ID = callsite.ID
				break
			}
		}
	}

	return body, nil
}
```

Add `substituteParams`, `substituteSlots`, `substituteEvents`, `cloneStmts` helpers in the same file:

```go
// substituteParams walks stmts replacing every *ir.Ident whose Sym is
// one of comp's *ir.Param entries with the bound argument expression.
func substituteParams(stmts []ir.Stmt, bindings map[string]ir.Expr, comp *ir.Component) []ir.Stmt {
	// Build a set of param symbols for fast lookup.
	paramByName := map[string]*ir.Param{}
	if comp.AST != nil {
		for _, p := range comp.AST.Props.Props {
			paramByName[p.Name] = nil // placeholder; will set from comp's resolved params if/when available
		}
	}
	// In the resolved IR, params are typically Sym targets of *ir.Ident
	// references inside the body. We walk and check by name match
	// against `bindings`. If the Sym is a *ir.Param with matching name,
	// substitute.
	walkExprs := newExprWalker(func(e ir.Expr) ir.Expr {
		if id, ok := e.(*ir.Ident); ok {
			if _, paramRef := id.Sym.(*ir.Param); paramRef {
				if bound, ok := bindings[id.Name]; ok {
					return cloneExpr(bound)
				}
			}
		}
		return e
	})
	return walkExprs.stmts(stmts)
}

// substituteSlots replaces every *ir.SlotInst with the children slice.
func substituteSlots(stmts []ir.Stmt, children []ir.Stmt) []ir.Stmt {
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		if _, isSlot := s.(*ir.SlotInst); isSlot {
			out = append(out, children...)
			continue
		}
		// Recurse into nested control-flow / NodeInst children.
		switch n := s.(type) {
		case *ir.If:
			n.Body = substituteSlots(n.Body, children)
			n.Else = substituteSlots(n.Else, children)
		case *ir.For:
			n.Body = substituteSlots(n.Body, children)
			n.Else = substituteSlots(n.Else, children)
		case *ir.NodeInst:
			n.Children = substituteSlots(n.Children, children)
		}
		out = append(out, s)
	}
	return out
}

// substituteEvents replaces every *ir.Emit whose Name matches a
// user-provided event handler with the handler's body.
func substituteEvents(stmts []ir.Stmt, handlers []ir.EventHandler) []ir.Stmt {
	byName := map[string]*ir.EventHandler{}
	for i := range handlers {
		h := &handlers[i]
		byName[h.Name] = h
	}
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		if emit, isEmit := s.(*ir.Emit); isEmit {
			if h, ok := byName[emit.Name]; ok && h != nil && h.Func != nil {
				out = append(out, h.Func.Block...)
				continue
			}
			// No matching handler — drop the emit (checker will
			// eventually reject this case).
			continue
		}
		// Recurse.
		switch n := s.(type) {
		case *ir.If:
			n.Body = substituteEvents(n.Body, handlers)
			n.Else = substituteEvents(n.Else, handlers)
		case *ir.For:
			n.Body = substituteEvents(n.Body, handlers)
			n.Else = substituteEvents(n.Else, handlers)
		case *ir.NodeInst:
			n.Children = substituteEvents(n.Children, handlers)
			for _, h := range n.Handlers {
				if h.Func != nil {
					h.Func.Block = substituteEvents(h.Func.Block, handlers)
				}
			}
		}
		out = append(out, s)
	}
	return out
}

// cloneStmts produces a deep copy of stmts so substitution mutations
// don't leak across multiple call sites of the same component.
func cloneStmts(stmts []ir.Stmt) []ir.Stmt {
	// Implementation: pure structural clone. For each stmt kind,
	// recursively rebuild the IR shape. Reuses pointers to immutable
	// shared symbols (e.g. *ir.Func, *ir.Var) without deep-copying them.
	out := make([]ir.Stmt, len(stmts))
	for i, s := range stmts {
		out[i] = cloneStmt(s)
	}
	return out
}

func cloneStmt(s ir.Stmt) ir.Stmt {
	// One case per stmt kind. For Plan G's MVP, handle the kinds that
	// appear in wrapper component bodies: NodeInst, If, For, Assign,
	// CallStmt, SlotInst, Emit. Other kinds rare in pure components.
	switch n := s.(type) {
	case *ir.NodeInst:
		clone := *n
		clone.Children = cloneStmts(n.Children)
		clone.Handlers = make([]ir.EventHandler, len(n.Handlers))
		for i, h := range n.Handlers {
			hc := h
			if h.Func != nil {
				fc := *h.Func
				fc.Block = cloneStmts(h.Func.Block)
				hc.Func = &fc
			}
			clone.Handlers[i] = hc
		}
		clone.Props = make([]ir.NodeInstProp, len(n.Props))
		for i, p := range n.Props {
			pc := p
			pc.Value = cloneExpr(p.Value)
			clone.Props[i] = pc
		}
		return &clone
	case *ir.If:
		clone := *n
		clone.Cond = cloneExpr(n.Cond)
		clone.Body = cloneStmts(n.Body)
		clone.Else = cloneStmts(n.Else)
		return &clone
	case *ir.For:
		clone := *n
		clone.Iter = cloneExpr(n.Iter)
		clone.Body = cloneStmts(n.Body)
		clone.Else = cloneStmts(n.Else)
		return &clone
	case *ir.SlotInst:
		clone := *n
		clone.Children = cloneStmts(n.Children)
		return &clone
	case *ir.Emit:
		clone := *n
		clone.Args = append([]ir.CallArg{}, n.Args...)
		for i := range clone.Args {
			clone.Args[i].Value = cloneExpr(clone.Args[i].Value)
		}
		return &clone
	case *ir.Assign:
		clone := *n
		clone.Target = cloneExpr(n.Target)
		clone.Value = cloneExpr(n.Value)
		return &clone
	case *ir.CallStmt:
		clone := *n
		if n.Call != nil {
			cc := *n.Call
			cc.Args = append([]ir.CallArg{}, n.Call.Args...)
			for i := range cc.Args {
				cc.Args[i].Value = cloneExpr(cc.Args[i].Value)
			}
			clone.Call = &cc
		}
		return &clone
	}
	return s
}

// cloneExpr is the expression analog. Implementations per kind.
func cloneExpr(e ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	// MVP: most common shapes. Other kinds fall through unchanged.
	switch n := e.(type) {
	case *ir.Ident:
		clone := *n
		return &clone
	case *ir.Literal:
		clone := *n
		return &clone
	case *ir.Binary:
		clone := *n
		clone.Left = cloneExpr(n.Left)
		clone.Right = cloneExpr(n.Right)
		return &clone
	case *ir.Unary:
		clone := *n
		clone.Operand = cloneExpr(n.Operand)
		return &clone
	case *ir.Select:
		clone := *n
		clone.Operand = cloneExpr(n.Operand)
		return &clone
	case *ir.Call:
		clone := *n
		clone.Receiver = cloneExpr(n.Receiver)
		clone.Args = append([]ir.CallArg{}, n.Args...)
		for i := range clone.Args {
			clone.Args[i].Value = cloneExpr(clone.Args[i].Value)
		}
		return &clone
	}
	return e
}

// newExprWalker builds a stmt-walking helper that applies an expr
// transform to every reachable expression. Used for param substitution.
type exprWalker struct {
	transform func(ir.Expr) ir.Expr
}

func newExprWalker(transform func(ir.Expr) ir.Expr) *exprWalker {
	return &exprWalker{transform: transform}
}

func (w *exprWalker) expr(e ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	e = w.transform(e)
	switch n := e.(type) {
	case *ir.Binary:
		n.Left = w.expr(n.Left)
		n.Right = w.expr(n.Right)
	case *ir.Unary:
		n.Operand = w.expr(n.Operand)
	case *ir.Select:
		n.Operand = w.expr(n.Operand)
	case *ir.Call:
		n.Receiver = w.expr(n.Receiver)
		for i := range n.Args {
			n.Args[i].Value = w.expr(n.Args[i].Value)
		}
	}
	return e
}

func (w *exprWalker) stmts(stmts []ir.Stmt) []ir.Stmt {
	for _, s := range stmts {
		w.stmt(s)
	}
	return stmts
}

func (w *exprWalker) stmt(s ir.Stmt) {
	switch n := s.(type) {
	case *ir.NodeInst:
		for i := range n.Props {
			n.Props[i].Value = w.expr(n.Props[i].Value)
		}
		w.stmts(n.Children)
		for _, h := range n.Handlers {
			if h.Func != nil {
				w.stmts(h.Func.Block)
			}
		}
	case *ir.If:
		n.Cond = w.expr(n.Cond)
		w.stmts(n.Body)
		w.stmts(n.Else)
	case *ir.For:
		n.Iter = w.expr(n.Iter)
		w.stmts(n.Body)
		w.stmts(n.Else)
	case *ir.SlotInst:
		w.stmts(n.Children)
	case *ir.Assign:
		n.Target = w.expr(n.Target)
		n.Value = w.expr(n.Value)
	case *ir.CallStmt:
		if n.Call != nil {
			n.Call.Receiver = w.expr(n.Call.Receiver)
			for i := range n.Call.Args {
				n.Call.Args[i].Value = w.expr(n.Call.Args[i].Value)
			}
		}
	}
}
```

Note the substituteParams approach: it matches by NAME (id.Name == param.Name AND id.Sym is *ir.Param) rather than pointer identity. This is robust against cloning (each clone has its own Ident pointers but the Names match).

- [x] **Step 3: Generate golden fixtures**

Run: `go test ./internal/lower/ -run TestLower/inline_pure_basic -update`

Inspect the produced `expected.sngl`. The body of `main` should show the substituted text NodeInst with the user's prop "Alice" replacing the wrapper's `name` ident.

If the format isn't what you'd expect (e.g. interpolation didn't collapse), accept the current output. The pass's job is structural substitution, not constant folding.

- [x] **Step 4: Run all lower tests**

Run: `go test ./internal/lower/...`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/lower/inline_pure.go internal/lower/testdata/inline_pure_basic.txtar
git commit -m "lower(inline_pure): substitution engine — params + slots + events

Three substitutions applied in one walk:
- *ir.Ident whose Sym is a *ir.Param of the wrapper → user's bound arg.
- *ir.SlotInst → user's NodeInst.Children (in order).
- *ir.Emit whose Name matches a user @event handler → handler body.

ID-preservation transfers the call site's __nN to the substituted
root. Deep-clone of wrapper body avoids cross-callsite leakage.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Fixtures for slot, event, and impure-skipped cases

**Files:**
- Create: `internal/lower/testdata/inline_pure_with_slot.txtar`
- Create: `internal/lower/testdata/inline_pure_event.txtar`
- Create: `internal/lower/testdata/inline_pure_impure_skipped.txtar`

Three fixtures exercising the substitution engine's edge cases.

- [x] **Step 1: Slot fixture**

Create `internal/lower/testdata/inline_pure_with_slot.txtar`:

```
caps: ""
-- input.sngl --
component card() {
    vbox(style={padding=16}) {
        slot
    }
}

component main {
    card() {
        text(value="hello")
    }
}
-- expected.sngl --
```

Run: `go test ./internal/lower/ -run TestLower/inline_pure_with_slot -update`. Verify the expected output shows the user's `text` inlined into the vbox where `slot` was.

- [x] **Step 2: Event fixture**

Create `internal/lower/testdata/inline_pure_event.txtar`:

```
caps: ""
-- input.sngl --
component clicker(@click func()) {
    button(text="click", @click { @click() })
}

component main {
    var count int = 0
    clicker(@click { count = count + 1 })
}
-- expected.sngl --
```

Run -update. Verify the inner `@click()` invocation got replaced with `count = count + 1` (the user's handler body).

Note: the user's `clicker` declares an event param via `@click func()` syntax. If SNGL doesn't currently support that exact syntax for declaring event params, adapt to whatever shape is correct. The point is: the wrapper's `@click()` body invocation gets substituted with the user's handler body.

If SNGL's event-declaration syntax is unclear in the source, check existing user-defined-component fixtures in `testdata/` for the right shape.

- [x] **Step 3: Impure-skipped fixture**

Create `internal/lower/testdata/inline_pure_impure_skipped.txtar`:

```
caps: ""
-- input.sngl --
component counter() {
    var count int = 0
    button(text=string(count), @click { count = count + 1 })
}

component main {
    counter()
}
-- expected.sngl --
component main {
    counter()
}
```

(Impure `counter` has a Var; should NOT inline.) The expected.sngl should show `main`'s body containing the un-inlined `counter()` call.

Run -update; inspect.

- [x] **Step 4: Run all three fixtures**

Run: `go test ./internal/lower/ -run TestLower/inline_pure -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/lower/testdata/inline_pure_with_slot.txtar internal/lower/testdata/inline_pure_event.txtar internal/lower/testdata/inline_pure_impure_skipped.txtar
git commit -m "lower(test): fixtures for slot/event/impure substitution paths

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Recursion detection + error fixture

**Files:**
- Create: `internal/lower/testdata/inline_pure_recursion.txtar`

- [x] **Step 1: Fixture**

```
caps: ""
expected_error: inline cycle in component "foo"
-- input.sngl --
component foo() {
    foo()
}

component main {
    foo()
}
```

The `expected_error:` header is a new fixture convention — if the test framework doesn't support it, adapt: the test runner asserts that `lower.Lower(...)` returns an error containing the substring.

- [x] **Step 2: Test runner support**

Check `internal/lower/golden_test.go` for how it asserts errors. If it doesn't have an `expected_error:` shape, add one:

```go
// In runGolden:
errPattern := parseExpectedError(arc.Comment)
if errPattern != "" {
    if err := lower.Lower(pkg, caps, lower.Options{}); err == nil {
        t.Fatalf("expected lower error matching %q; got nil", errPattern)
    } else if !strings.Contains(err.Error(), errPattern) {
        t.Fatalf("expected lower error matching %q; got: %v", errPattern, err)
    }
    return
}
```

- [x] **Step 3: Run**

Run: `go test ./internal/lower/ -run TestLower/inline_pure_recursion -v`
Expected: PASS — the test asserts the cycle error.

- [x] **Step 4: Commit**

```bash
git add internal/lower/testdata/inline_pure_recursion.txtar internal/lower/golden_test.go
git commit -m "lower(test): recursion-cycle fixture + expected_error support

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase C: Strict mode

### Task 6: Strict-mode + impurity error fixture

**Files:**
- Create: `internal/lower/testdata/inline_strict_impure_errors.txtar`
- Create: `internal/lower/testdata/inline_strict_clean.txtar`

Strict mode requires platform-stdlib components to be pure. Test both cases.

- [x] **Step 1: Impure-error fixture**

Create `internal/lower/testdata/inline_strict_impure_errors.txtar`:

```
caps: "NoStdlibWrappers"
expected_error: platform stdlib wrapper "stateful" must be pure
-- input.sngl --
import "platform://teststub"

component main {
    teststub.stateful()
}
```

The `platform://teststub` import implies a synthetic test-only platform that registers a stateful component named `stateful`. If the test framework doesn't have a stub platform, this fixture may need to mock differently.

Alternative: build the fixture WITHOUT real platform imports — directly construct a pkg.Imports entry with a Component that has Vars. Adapt depending on what the test framework allows.

Simplest workaround: a unit test in `inline_pure_test.go` that constructs the IR directly:

```go
func TestStrictModeImpureError(t *testing.T) {
    impureComp := &ir.Component{
        Name: "stateful",
        Vars: []*ir.Var{{Name: "v"}},
        Body: []ir.Stmt{},
    }
    callsite := &ir.NodeInst{Name: "stateful", Component: impureComp}
    mainComp := &ir.Component{
        Name: "main",
        Body: []ir.Stmt{callsite},
    }
    pkg := &ir.Package{
        Components: []*ir.Component{mainComp},
        Imports: []*ir.Import{
            {
                Path: "platform://teststub",
                Pkg:  &ir.Package{Components: []*ir.Component{impureComp}},
            },
        },
    }
    err := lower.Lower(pkg, lower.Caps{NoStdlibWrappers: true}, lower.Options{})
    if err == nil || !strings.Contains(err.Error(), "must be pure") {
        t.Errorf("expected impurity error; got: %v", err)
    }
}
```

Pick whichever path works.

- [x] **Step 2: Clean fixture**

Create `internal/lower/testdata/inline_strict_clean.txtar`:

```
caps: "NoStdlibWrappers"
-- input.sngl --
import "platform://teststub"

component main {
    teststub.cleanwrap(value="hi")
}
-- expected.sngl --
component main {
    span(textContent="hi")
}
```

Adapt if teststub doesn't exist. The unit-test alternative:

```go
func TestStrictModeCleanInlines(t *testing.T) {
    cleanComp := &ir.Component{
        Name: "cleanwrap",
        AST:  cleanwrapASTwithProps,
        Body: []ir.Stmt{
            &ir.NodeInst{Name: "span", Props: []ir.NodeInstProp{{Name: "textContent", Value: identForParam("value")}}},
        },
    }
    callsite := &ir.NodeInst{
        Name:      "cleanwrap",
        Component: cleanComp,
        Props:     []ir.NodeInstProp{{Name: "value", Value: &ir.Literal{Type: ir.TypString, Raw: "hi"}}},
    }
    mainComp := &ir.Component{Name: "main", Body: []ir.Stmt{callsite}}
    pkg := &ir.Package{
        Components: []*ir.Component{mainComp},
        Imports: []*ir.Import{
            {Path: "platform://teststub", Pkg: &ir.Package{Components: []*ir.Component{cleanComp}}},
        },
    }
    if err := lower.Lower(pkg, lower.Caps{NoStdlibWrappers: true}, lower.Options{}); err != nil {
        t.Fatal(err)
    }
    // Walk main.Body — expect a single NodeInst named "span".
    if len(mainComp.Body) != 1 {
        t.Fatalf("expected 1 stmt in main.Body; got %d", len(mainComp.Body))
    }
    spanNode, ok := mainComp.Body[0].(*ir.NodeInst)
    if !ok || spanNode.Name != "span" {
        t.Errorf("expected inlined span; got: %T %v", mainComp.Body[0], mainComp.Body[0])
    }
}
```

- [x] **Step 3: Run + commit**

```bash
go test ./internal/lower/...
git add internal/lower/
git commit -m "lower(test): strict-mode impure-error + clean-inline fixtures

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase D: Enable on fyne (zero-behavior-change validation)

### Task 7: Audit fyne.sngl for purity

- [x] **Step 1: Read every component definition in fyne.sngl**

```bash
grep -n "^component " codegen/platform/fyne/fyne.sngl
```

For each, verify the body contains only NodeInsts + nested NodeInsts. No `var` declarations, no `func` declarations, no `timer` declarations. If anything is found, document the offending component and either fix in place or punt with a known-issue note.

Expected: all clean. fyne's wrapper components are blueprint declarations with no internal state.

- [x] **Step 2: No commit if no changes needed**

If fyne.sngl is already clean, skip to Task 8.

---

### Task 8: Enable NoStdlibWrappers on fyne

**Files:**
- Modify: `codegen/platform/fyne/fyne.go`

- [x] **Step 1: Update Capabilities**

```go
func (g *Generator) Capabilities() lower.Caps {
	return lower.Caps{
		NoReactivity:     true,
		NoDeclarative:    true,
		NoStdlibWrappers: true,
	}
}
```

- [x] **Step 2: Run full fyne tests + integration test + compile-check**

Run: `go test ./codegen/platform/fyne/...`
Expected: PASS. fyne's wrapper bodies (containing blueprint-bearing `Label`/`Button` NodeInsts) inline cleanly; fyne's translator already reads NodeInst.Props for blueprint records, so post-inline behavior is identical to pre-inline.

If any test fails, capture the failure pattern. Most likely: a wrapper body has an unexpected shape that the substitution engine doesn't handle (e.g. a `Constructor{...}` struct-literal value referencing a wrapper param). Adapt the engine to handle the case.

Run: `go tool sngl run examples/hello-i18n --platform fyne --opt goModExtra="replace git.duckfam.us/jonathan/sngl => $(pwd)"`

Expected: runs.

- [x] **Step 3: Commit**

```bash
git add codegen/platform/fyne/fyne.go
git commit -m "fyne: enable NoStdlibWrappers in Capabilities

fyne.sngl wrapper components inline at lowering time. Behavior
unchanged — wrapper bodies still produce the same blueprint-bearing
NodeInsts fyne's translator reads at codegen. Validates the inline
pass on a real platform with zero behavior change.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase E: Enable on gtk4

### Task 9: Audit gtk4.sngl + enable NoStdlibWrappers

- [x] **Step 1: Audit purity**

```bash
grep -n "^component " codegen/platform/gtk4/gtk4.sngl
grep -A 10 "^component " codegen/platform/gtk4/gtk4.sngl | grep -E "    var |    func |    timer "
```

The second grep should return nothing. If it returns something, that wrapper needs fixing.

- [x] **Step 2: Enable capability**

In `codegen/platform/gtk4/gtk4.go`:

```go
func (g *Generator) Capabilities() lower.Caps {
	return lower.Caps{
		NoReactivity:     true,
		NoDeclarative:    true,
		NoStdlibWrappers: true,
	}
}
```

- [x] **Step 3: Run gtk4 tests**

Run: `go test ./codegen/platform/gtk4/...`
Expected: PASS — wrapper bodies inline to native GIR-resolved widget calls (`GtkLabel`, `GtkBox`, etc.); translator already handles those (Plan C added GIR metadata flow).

If anything fails, capture pattern. Most likely a fixture using SNGL stdlib tag (`button`) that the test expects to land at the translator — but now the translator only sees `GtkButton`. Adapt the test or the integration.

- [x] **Step 4: Run integration**

```bash
go install ./cmd/sngl
go tool sngl run examples/hello-i18n --platform gtk4 --opt goModExtra="replace git.duckfam.us/jonathan/sngl => $(pwd)"
```

Expected: runs.

- [x] **Step 5: Commit**

```bash
git add codegen/platform/gtk4/gtk4.go
git commit -m "gtk4: enable NoStdlibWrappers in Capabilities

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 10: Delete gtk4TagToCType + gtk4Constructor

**Files:**
- Modify: `codegen/platform/gtk4/intrinsic_translator.go`

After Task 9 inlines wrapper components, gtk4Translator's OnCreateNode never receives SNGL stdlib tags like "button" or "text" — only GIR-resolved names like "GtkButton" or "GtkLabel". The `gtk4TagToCType` and `gtk4Constructor` switches are dead.

- [x] **Step 1: Audit usage**

```bash
grep -n "gtk4TagToCType\|gtk4Constructor" codegen/platform/gtk4/
```

Note every reference. They should all be in OnCreateNode (the entry path) plus tests.

- [x] **Step 2: Refactor OnCreateNode to use only GIR metadata**

The new OnCreateNode:

```go
func (t *gtk4Translator) OnCreateNode(ctx context.Context, id, tag string) []ir.Stmt {
    // Look up via Component.Native metadata (populated at Resolve time
    // for GIR-loaded widgets, Plan C added this).
    comp, nm := t.lookupNativeComponentByName(tag)
    if comp == nil || nm == nil {
        return nil
    }
    cType := nm.CType
    if cType == "" {
        cType = tag
    }
    ctor := nativeCall(nm.Constructor)
    t.fieldSink(id, cType)
    t.idCTypes[id] = cType
    t.topLevel = append(t.topLevel, id)
    return []ir.Stmt{&ir.Assign{
        Target: modelFieldRef(id),
        Op:     ast.AssignSet,
        Value:  cgoCast(cType, ctor),
    }}
}

// lookupNativeComponentByName finds an ir.Component by name in the
// package's platform-stdlib imports. Returns nil for unknown.
func (t *gtk4Translator) lookupNativeComponentByName(name string) (*ir.Component, *gtk4NativeComponent) {
    if t.pkg == nil {
        return nil, nil
    }
    for _, imp := range t.pkg.Imports {
        if imp.Pkg == nil {
            continue
        }
        for _, c := range imp.Pkg.Components {
            if c.Name == name {
                if nm, ok := c.Native.(*gtk4NativeComponent); ok {
                    return c, nm
                }
                return c, nil
            }
        }
    }
    return nil, nil
}
```

Delete `gtk4TagToCType` and `gtk4Constructor` functions entirely.

The `gtk_application_window_new(app)` special case stays (it takes an arg the default GIR metadata doesn't carry). Keep it inline:

```go
// If the constructor is gtk_application_window_new, pass the app
// reference as the only arg.
if nm.Constructor == "gtk_application_window_new" {
    ctor = nativeCall(nm.Constructor, &ir.Ident{Name: "app", Type: ir.TypDyn})
}
```

Adapt to actual code shape.

- [x] **Step 3: Update translator tests**

Some `intrinsic_translator_test.go` tests use SNGL stdlib tags like "text"/"button". These need to either:
- Switch to using GIR-resolved tags like "GtkLabel"/"GtkButton".
- Pre-populate the test package with platform-stdlib import to make the wrapper-inline path work.

Simplest: switch test args to GIR names directly. The unit tests are testing the translator's behavior on what it actually receives post-Plan-G.

- [x] **Step 4: Run + verify**

```bash
go build ./...
go test ./codegen/platform/gtk4/...
```

- [x] **Step 5: Commit**

```bash
git add codegen/platform/gtk4/intrinsic_translator.go codegen/platform/gtk4/intrinsic_translator_test.go
git commit -m "gtk4: delete gtk4TagToCType + gtk4Constructor switches

passInlinePure inlines gtk4.sngl wrapper components at lowering
time, so the translator only sees GIR-resolved widget names.
Constructor + setter metadata flows through Component.Native
(Plan C). The SNGL-stdlib-tag → C-type switch is dead code.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase F: Enable on html

### Task 11: Audit html.sngl + enable NoStdlibWrappers

- [x] **Step 1: Audit purity**

```bash
grep -n "^component " codegen/platform/html/html.sngl
grep -A 10 "^component " codegen/platform/html/html.sngl | grep -E "    var |    func |    timer "
```

Expected: nothing. html.sngl wrappers are pure DOM-element wrappers.

- [x] **Step 2: Enable capability**

In `codegen/platform/html/html.go`:

```go
func (g *Generator) Capabilities() lower.Caps {
	return lower.Caps{
		NoReactivity:     true,
		NoAsyncReactive:  true,
		NoStdlibWrappers: true,
	}
}
```

(Note: html does NOT enable NoDeclarative — static-site path.)

- [x] **Step 3: Run html tests + browser smoke**

```bash
go test ./codegen/platform/html/...
```

Expected: PASS. If snippets in tests assert SNGL-stdlib tag names being emitted, update them to expect native DOM element names (e.g. `<span>` instead of `<text>`).

```bash
go install ./cmd/sngl
cd /tmp && rm -rf hello-html && mkdir hello-html && cp -r /home/jonathan/src/git.duckfam.us/jonathan/sngl/examples/hello-i18n/* hello-html/
cd hello-html && sngl compile --platform=html --out=out app.sngl
grep -c "<span\|<button\|<input\|<div" out/index.html
```

Expected: >0 native HTML elements emitted.

- [x] **Step 4: Commit**

```bash
git add codegen/platform/html/html.go
git commit -m "html: enable NoStdlibWrappers in Capabilities

Wrapper components from html.sngl inline at lowering time.
Translator's OnCreateNode now only sees native HTML tags.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 12: Delete htmlTagToDOM

**Files:**
- Modify: `codegen/platform/html/intrinsic_translator.go`

- [x] **Step 1: Refactor OnCreateNode**

After Task 11, OnCreateNode receives only native HTML tags. The current mapping reduces to passthrough:

```go
func (t *htmlTranslator) OnCreateNode(ctx context.Context, id, tag string) []ir.Stmt {
    t.idTags[id] = tag
    t.topLevel = append(t.topLevel, id)
    // const <id> = document.createElement("<tag>");
    createCall := &ir.Call{
        Type:     ir.TypDyn,
        Receiver: &ir.Ident{Name: "document"},
        Func:     &ir.Func{Name: "createElement"},
        Args:     []ir.CallArg{{Value: &ir.Literal{Type: ir.TypString, Raw: tag}}},
    }
    return []ir.Stmt{&ir.LocalVar{Name: id, Type: ir.TypDyn, Init: createCall}}
}
```

Delete `htmlTagToDOM` function entirely.

- [x] **Step 2: Simplify htmlPropSetter**

`htmlPropSetter(tag, prop)` was a mapping table for SNGL-stdlib-tag-prop → DOM-property. With native tags now reaching the translator, the map becomes simpler — most native DOM property names match SNGL prop names directly (`value`, `placeholder`, `disabled`, `checked`, `type`). Audit:

```go
func htmlPropSetter(tag, prop string) string {
    // Native DOM property mappings. For props whose SNGL name matches
    // the DOM property directly, return prop verbatim. For special
    // cases like text content, use the right DOM property.
    switch prop {
    case "textContent", "innerHTML", "value", "placeholder", "disabled", "checked", "type", "className":
        return prop
    }
    return "" // unknown — caller falls back to setAttribute
}
```

(Adapt as needed based on what props actually appear in html.sngl wrapper bodies.)

- [x] **Step 3: Update tests**

Translator tests using "text"/"button" SNGL tags should now use "span"/"button" DOM tags. Update assertions.

- [x] **Step 4: Run + commit**

```bash
go test ./codegen/platform/html/...
git add codegen/platform/html/intrinsic_translator.go codegen/platform/html/intrinsic_translator_test.go
git commit -m "html: delete htmlTagToDOM (dead after NoStdlibWrappers)

OnCreateNode now passes the native HTML tag through to document.
createElement directly. htmlPropSetter simplified to native DOM
property names.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase G: Verify + handoff

### Task 13: Final verify

```bash
go test ./...
go tool verify 2>&1 | grep -E "^---|not ok" | head -10
```

Compare to pre-Plan-G baseline. Expected: no new failures. Plan G should preserve behavior (fyne) while enabling cleanup on gtk4/html.

Reproduce hello-i18n:

```bash
go install ./cmd/sngl
go tool sngl run examples/hello-i18n --platform fyne --opt goModExtra="replace git.duckfam.us/jonathan/sngl => $(pwd)"
go tool sngl run examples/hello-i18n --platform gtk4 --opt goModExtra="replace git.duckfam.us/jonathan/sngl => $(pwd)"
```

(html doesn't run via `sngl run`; compile + inspect output instead.)

### Task 14: Spec handoff annotation

Update `docs/superpowers/specs/2026-05-12-reactivity-lowering-consolidation-design.md`. Find the existing Plan A/B/B.2/C/F annotation block. Add Plan G:

```
> **Plan G** (`docs/superpowers/plans/2026-05-13-inline-pure-components.md`)
> added passInlinePure to inline pure-component calls at lowering time.
> All three platforms enabled NoStdlibWrappers; gtk4TagToCType +
> gtk4Constructor and htmlTagToDOM switches deleted. Translators now
> only see native widget shapes (GIR-resolved for gtk4, DOM tags for
> html, blueprint-bearing for fyne). Unblocks Plan D resume.
```

Commit. Push (or note SSH still blocked).

---

## What's NOT in Plan G

- Resuming Plan D (html Tasks 10-13). That's the next plan to execute.
- Cross-package inlining (inlining components imported from other user packages).
- User-declared `pure` keyword.
- Children-type enforcement.
- Recursive expansion of nested wrappers in fyne's `Constructor{...}` struct literals — assumes struct literals carrying param refs work via cloneExpr's struct-literal handling. Verify during fyne enable; extend cloneExpr if needed.
