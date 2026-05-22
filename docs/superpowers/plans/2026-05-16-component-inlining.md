# Component Inlining Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a lowering pass `NoInlineComponents` that inlines every non-recursive, non-stdlib, non-native user component into `main`, then delete the bubbletea/fyne flat-Model workarounds it replaces.

**Architecture:** New pass `internal/lower/inline_components.go` runs after `InlinePure` and before `NoReactivity`. It clones each callee's body/vars/funcs/timers, renames identifiers with per-call-site suffixes (`n` → `n__inst0`), substitutes prop refs with the call-site argument expressions, splices the substituted body into the parent at the `NodeInst`'s position, and hoists vars/funcs/timers into `main`. Recursive cycles (detected upfront) stay as real `NodeInst`-referenced components; everything else is inlined to a fixed point. The pass is gated by `Caps.NoInlineComponents` — initially flipped on for `golang` and `kotlin` only. Once green, the bubbletea/fyne `Renames`-based prop-default substitution and the cross-component `taggedVar` loops get deleted.

**Tech Stack:** Go, existing `internal/lower` pass framework, existing `ir` package walkers, `walkPackage`/`walkFuncs` from `internal/lower/walk.go`, `deepCloneStmts`/`deepCloneExpr`/`newExprWalker` already in `internal/lower/inline_pure.go` (reuse — do not duplicate).

Reference reading:
- `docs/superpowers/specs/2026-05-16-component-inlining-design.md` (spec)
- `internal/lower/inline_pure.go` (the cloning + param-substitution + event-substitution engine to reuse)
- `internal/lower/list_lambda.go` (recent example of a fresh pass; same shape applies)
- `internal/lower/lower.go` (pass list + ordering)
- `internal/lower/caps.go` (Caps struct + Merge/String)
- `internal/lower/golden_test.go` (txtar golden harness — `setCapByName` must learn the new cap)

---

## File Structure

**New files:**
- `internal/lower/inline_components.go` — the pass: cycle detection, clone+rename+substitute, splice/hoist.
- `internal/lower/inline_components_test.go` — unit tests for cycle detection, fresh-name allocation, and rename map application.
- `internal/lower/testdata/inline_components_basic.txtar` — single-instance smoke fixture.
- `internal/lower/testdata/inline_components_multi_instance.txtar` — two instances of the same component must produce distinct state.
- `internal/lower/testdata/inline_components_events.txtar` — child emits an event; parent's handler body replaces the emit site with arg substitution.
- `internal/lower/testdata/inline_components_recursion.txtar` — recursive component (TreeView-style) survives the pass.
- `internal/lower/testdata/inline_components_timer.txtar` — child timer hoists into main with renamed identifiers.
- `internal/lower/testdata/inline_components_nested.txtar` — Page contains LabeledCounter contains Counter — fixed-point iteration.

**Modified files:**
- `internal/lower/caps.go` — add `NoInlineComponents bool` field, merge clause, String clause.
- `internal/lower/golden_test.go` — add `"NoInlineComponents"` arm to `setCapByName`.
- `internal/lower/lower.go` — splice `passNoInlineComponents` into `passes`, between `passInlinePure` and `passReactivity`.
- `codegen/lang/golang/golang.go` — set `caps.NoInlineComponents = true`.
- `codegen/lang/kotlin/kotlin.go` — set `caps.NoInlineComponents = true`.
- `codegen/platform/bubbletea/compiler_ir.go` — delete the `Renames`-based prop-default substitution loop (lines ~177-188).
- `codegen/platform/fyne/compiler_ir.go` — delete the mirror `Renames` loop (lines ~167-175); delete the `if v.Synthesized` skip for non-main components (line ~124); narrow the cross-`pkg.Components` taggedVar walk to `main` + `pkg.Vars` only.

**Files to read but not modify:**
- `ir/stmt.go`, `ir/expr.go`, `ir/ir.go` — IR node shapes.
- `internal/lower/inline_pure.go` — reuse `deepCloneStmts`, `deepCloneExpr`, `newExprWalker`, `substituteParams`, `substituteEvents`, `bindEventParams`.

---

## Phase A — Foundations (Caps wiring + skeleton pass)

### Task A1: Add `NoInlineComponents` cap

**Files:**
- Modify: `internal/lower/caps.go`
- Modify: `internal/lower/golden_test.go`

- [ ] **Step 1: Add the Caps field**

In `internal/lower/caps.go`, inside `type Caps struct { ... }`, add (after `NoListLambdas`):

```go
	NoInlineComponents bool // user-defined non-recursive components → inlined into main (per-instance renamed vars/funcs/timers/body)
```

- [ ] **Step 2: Add to Merge**

In the same file, inside `(c Caps) Merge`, add inside the returned `Caps{...}` literal:

```go
		NoInlineComponents: c.NoInlineComponents || other.NoInlineComponents,
```

- [ ] **Step 3: Add to String**

In `(c Caps) String()`, add (place it after the `NoStdlibWrappers` block — order matches pass-execution position is fine; the spec puts the pass before NoReactivity but after InlinePure):

```go
	if c.NoInlineComponents {
		parts = append(parts, "NoInlineComponents")
	}
```

- [ ] **Step 4: Teach the golden harness about the cap**

In `internal/lower/golden_test.go`, inside `setCapByName`, add a case before `default`:

```go
	case "NoInlineComponents":
		c.NoInlineComponents = true
```

- [ ] **Step 5: Verify build**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add internal/lower/caps.go internal/lower/golden_test.go
git commit -m "lower: NoInlineComponents cap stub (no pass yet)"
```

---

### Task A2: Skeleton pass that no-ops when disabled

**Files:**
- Create: `internal/lower/inline_components.go`
- Modify: `internal/lower/lower.go`

- [ ] **Step 1: Create the pass file**

Write `internal/lower/inline_components.go`:

```go
package lower

import "git.duckfam.us/jonathan/sngl/ir"

// passNoInlineComponents inlines every non-recursive, non-native, non-main
// user-defined component into main. After the pass, codegen only sees one
// real ir.Component (main) plus any recursive cycles. See
// docs/superpowers/specs/2026-05-16-component-inlining-design.md.
var passNoInlineComponents = pass{
	name:    "NoInlineComponents",
	enabled: func(c Caps) bool { return c.NoInlineComponents },
	apply:   lowerInlineComponents,
}

func lowerInlineComponents(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	main := mainComponent(pkg)
	if main == nil {
		return nil // no main → nothing to inline into
	}
	cycles := findRecursiveCycles(pkg)
	st := &inlineCompState{pkg: pkg, main: main, cycles: cycles}
	if err := st.run(); err != nil {
		return err
	}
	pkg.Components = retainComponents(pkg.Components, st.keep)
	return nil
}

type inlineCompState struct {
	pkg    *ir.Package
	main   *ir.Component
	cycles map[*ir.Component]bool
	// keep tracks which components survive the pass: main + any in cycles.
	keep map[*ir.Component]bool
	// instCounter feeds the per-call-site suffix.
	instCounter int
}

func (st *inlineCompState) run() error {
	// Placeholder: real implementation in subsequent tasks. For now do nothing.
	st.keep = map[*ir.Component]bool{st.main: true}
	for c := range st.cycles {
		st.keep[c] = true
	}
	return nil
}

// mainComponent returns the package's main component, or nil if absent.
func mainComponent(pkg *ir.Package) *ir.Component {
	for _, c := range pkg.Components {
		if c.Name == "main" {
			return c
		}
	}
	return nil
}

// findRecursiveCycles returns the set of components participating in any
// call cycle (including self-recursion). Edges follow NodeInst.Component
// from each component's body (and nested control-flow / handlers).
func findRecursiveCycles(pkg *ir.Package) map[*ir.Component]bool {
	edges := map[*ir.Component]map[*ir.Component]bool{}
	for _, c := range pkg.Components {
		edges[c] = map[*ir.Component]bool{}
		collectCalleeEdges(c.Body, edges[c])
		for _, f := range c.Funcs {
			collectCalleeEdges(f.Block, edges[c])
		}
	}
	// Tarjan-style SCCs. Any SCC of size >1, or size 1 with self-edge, is a cycle.
	return tarjanCycles(edges)
}

func collectCalleeEdges(stmts []ir.Stmt, out map[*ir.Component]bool) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if n.Component != nil {
				out[n.Component] = true
			}
			collectCalleeEdges(n.Children, out)
			for _, h := range n.Handlers {
				if h.Func != nil {
					collectCalleeEdges(h.Func.Block, out)
				}
			}
		case *ir.If:
			collectCalleeEdges(n.Body, out)
			collectCalleeEdges(n.Else, out)
		case *ir.For:
			collectCalleeEdges(n.Body, out)
			collectCalleeEdges(n.Else, out)
		case *ir.PlatformFilter:
			collectCalleeEdges(n.Body, out)
		case *ir.SlotInst:
			collectCalleeEdges(n.Children, out)
		case *ir.ErrorBoundary:
			collectCalleeEdges(n.Children, out)
			if n.Handler != nil && n.Handler.Func != nil {
				collectCalleeEdges(n.Handler.Func.Block, out)
			}
		}
	}
}

// tarjanCycles runs Tarjan's SCC algorithm over the edge map and returns
// the set of nodes in any non-trivial SCC (size > 1, or size 1 with a
// self-edge).
func tarjanCycles(edges map[*ir.Component]map[*ir.Component]bool) map[*ir.Component]bool {
	cycles := map[*ir.Component]bool{}
	idx := 0
	indices := map[*ir.Component]int{}
	lowlinks := map[*ir.Component]int{}
	onStack := map[*ir.Component]bool{}
	var stack []*ir.Component

	var strongconnect func(v *ir.Component)
	strongconnect = func(v *ir.Component) {
		indices[v] = idx
		lowlinks[v] = idx
		idx++
		stack = append(stack, v)
		onStack[v] = true
		for w := range edges[v] {
			if _, seen := indices[w]; !seen {
				strongconnect(w)
				if lowlinks[w] < lowlinks[v] {
					lowlinks[v] = lowlinks[w]
				}
			} else if onStack[w] {
				if indices[w] < lowlinks[v] {
					lowlinks[v] = indices[w]
				}
			}
		}
		if lowlinks[v] == indices[v] {
			var scc []*ir.Component
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			if len(scc) > 1 {
				for _, n := range scc {
					cycles[n] = true
				}
			} else if edges[scc[0]][scc[0]] {
				cycles[scc[0]] = true
			}
		}
	}
	for v := range edges {
		if _, seen := indices[v]; !seen {
			strongconnect(v)
		}
	}
	return cycles
}

// retainComponents returns a new slice containing only components in keep,
// preserving relative order.
func retainComponents(in []*ir.Component, keep map[*ir.Component]bool) []*ir.Component {
	out := make([]*ir.Component, 0, len(in))
	for _, c := range in {
		if keep[c] {
			out = append(out, c)
		}
	}
	return out
}
```

- [ ] **Step 2: Add the pass to the pipeline**

In `internal/lower/lower.go`, splice `passNoInlineComponents` into `var passes` between `passInlinePure` and `passReactivity`:

```go
var passes = []pass{
	passPlatformExtensionBody,
	passUnit,
	passEnum,
	passTernary,
	passAsyncReactive,
	passComputed,
	passLambda,
	passNoListLambdas,
	passToggle,
	passInlinePure,
	passNoInlineComponents,
	passReactivity,
	passTimer,
	passDeclarative,
	passNoRef,
}
```

Also update the comment block above `var passes` to mention the new pass. Add this line after the `7a. InlinePure` paragraph:

```
//     7b. NoInlineComponents — opt-in. Inlines every non-recursive user
//     component into main, renaming vars/funcs/timers and substituting
//     prop refs with call-site arg exprs. After this pass, codegen on
//     opted-in targets sees only main + any recursive components.
```

- [ ] **Step 3: Verify build and that existing tests still pass**

Run: `go test ./internal/lower/...`
Expected: PASS — pass exists, is disabled by default, no fixtures opt in yet.

- [ ] **Step 4: Commit**

```bash
git add internal/lower/inline_components.go internal/lower/lower.go
git commit -m "lower: NoInlineComponents skeleton pass + cycle detection"
```

---

### Task A3: Unit test for cycle detection

**Files:**
- Create: `internal/lower/inline_components_test.go`

- [ ] **Step 1: Write the test**

Write `internal/lower/inline_components_test.go`:

```go
package lower

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestFindRecursiveCycles(t *testing.T) {
	// A → B → A (mutual), C → C (self), D → E (acyclic).
	a := &ir.Component{Name: "A"}
	b := &ir.Component{Name: "B"}
	c := &ir.Component{Name: "C"}
	d := &ir.Component{Name: "D"}
	e := &ir.Component{Name: "E"}

	a.Body = []ir.Stmt{&ir.NodeInst{Component: b}}
	b.Body = []ir.Stmt{&ir.NodeInst{Component: a}}
	c.Body = []ir.Stmt{&ir.NodeInst{Component: c}}
	d.Body = []ir.Stmt{&ir.NodeInst{Component: e}}
	e.Body = []ir.Stmt{} // leaf

	pkg := &ir.Package{Components: []*ir.Component{a, b, c, d, e}}
	got := findRecursiveCycles(pkg)
	want := map[*ir.Component]bool{a: true, b: true, c: true}
	for k := range want {
		if !got[k] {
			t.Errorf("missing %s from cycles", k.Name)
		}
	}
	for k := range got {
		if !want[k] {
			t.Errorf("unexpected cycle node %s", k.Name)
		}
	}
}

func TestFindRecursiveCyclesThroughIfBranch(t *testing.T) {
	// Self-recursion hidden under an if — cycle detection must see it.
	tv := &ir.Component{Name: "TreeView"}
	tv.Body = []ir.Stmt{
		&ir.If{Body: []ir.Stmt{&ir.NodeInst{Component: tv}}},
	}
	pkg := &ir.Package{Components: []*ir.Component{tv}}
	got := findRecursiveCycles(pkg)
	if !got[tv] {
		t.Errorf("TreeView self-recursion should be detected")
	}
}
```

- [ ] **Step 2: Run the test**

Run: `go test -run TestFindRecursiveCycles ./internal/lower/...`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/lower/inline_components_test.go
git commit -m "lower: unit-test NoInlineComponents cycle detection"
```

---

## Phase B — Core inliner: clone + rename + prop substitution (single instance)

### Task B1: Per-call-site fresh-name allocator

**Files:**
- Modify: `internal/lower/inline_components.go`

- [ ] **Step 1: Add the allocator on the state struct**

Append to `internal/lower/inline_components.go` (after `inlineCompState` methods, before `mainComponent`):

```go
// freshSuffix returns a unique suffix like "__inst0", "__inst1", ... for
// the next call site. Stable across re-runs because state is per-Lower call.
func (st *inlineCompState) freshSuffix() string {
	n := st.instCounter
	st.instCounter++
	return "__inst" + itoa(n)
}

// itoa avoids importing strconv just for this — keeps the file's imports
// matched to ir alone. Replace with strconv.Itoa if other helpers grow.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
```

(Or import `strconv` and use `strconv.Itoa(n)` — your call. The plan keeps imports tight.)

- [ ] **Step 2: Verify build**

Run: `go build ./internal/lower/...`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add internal/lower/inline_components.go
git commit -m "lower: per-call-site fresh-suffix allocator"
```

---

### Task B2: Identifier rename walker

**Files:**
- Modify: `internal/lower/inline_components.go`

- [ ] **Step 1: Add `renameIdents`**

Append to `internal/lower/inline_components.go`:

```go
// renameIdents walks stmts and renames every Ident whose Sym is in
// renames to the mapped name. Sym is preserved so downstream passes can
// still resolve to the underlying decl. Mutates in place (caller passes a
// deep clone).
func renameIdents(stmts []ir.Stmt, renames map[ir.Symbol]string) []ir.Stmt {
	if len(renames) == 0 {
		return stmts
	}
	w := newExprWalker(func(e ir.Expr) ir.Expr {
		id, ok := e.(*ir.Ident)
		if !ok || id.Sym == nil {
			return e
		}
		if newName, ok := renames[id.Sym]; ok {
			id.Name = newName
		}
		return e
	})
	return w.stmts(stmts)
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./internal/lower/...`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add internal/lower/inline_components.go
git commit -m "lower: ident-rename walker for inlined components"
```

---

### Task B3: Single-instance inliner — hoist + body splice

**Files:**
- Modify: `internal/lower/inline_components.go`

- [ ] **Step 1: Implement `run` + helpers**

Replace the stub `run` and add the inlining helpers. Final shape of the relevant section:

```go
func (st *inlineCompState) run() error {
	st.keep = map[*ir.Component]bool{st.main: true}
	for c := range st.cycles {
		st.keep[c] = true
	}
	// Iterate to a fixed point: each pass over main.Body replaces every
	// inlinable NodeInst. New inlinable NodeInsts may surface inside the
	// substituted body (e.g. Page inlines LabeledCounter inlines Counter),
	// so loop until a pass makes no changes.
	for {
		changed := false
		body, ch, err := st.inlineStmts(st.main.Body)
		if err != nil {
			return err
		}
		st.main.Body = body
		// Also inline inside any user funcs that were already hoisted into
		// main (subsequent rounds pick up nested instantiations).
		for _, f := range st.main.Funcs {
			fbody, fch, err := st.inlineStmts(f.Block)
			if err != nil {
				return err
			}
			f.Block = fbody
			ch = ch || fch
		}
		changed = changed || ch
		if !changed {
			break
		}
	}
	return nil
}

// inlinable reports whether comp is a candidate for inlining.
func (st *inlineCompState) inlinable(comp *ir.Component) bool {
	if comp == nil || comp == st.main {
		return false
	}
	if st.cycles[comp] {
		return false
	}
	if comp.Native != nil {
		return false // platform-native (GTK widget, etc.) — opaque
	}
	return true
}

// inlineStmts walks a stmt slice and inlines eligible NodeInsts. Returns
// (rewritten, changed, err). Recurses into nested control-flow bodies and
// NodeInst children/handlers so deep instantiations get picked up.
func (st *inlineCompState) inlineStmts(stmts []ir.Stmt) ([]ir.Stmt, bool, error) {
	changed := false
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		repl, ch, err := st.inlineStmt(s)
		if err != nil {
			return nil, false, err
		}
		changed = changed || ch
		out = append(out, repl...)
	}
	return out, changed, nil
}

func (st *inlineCompState) inlineStmt(s ir.Stmt) ([]ir.Stmt, bool, error) {
	switch n := s.(type) {
	case *ir.NodeInst:
		// Recurse into children/handlers first so inner inlinings happen.
		ch, chCh, err := st.inlineStmts(n.Children)
		if err != nil {
			return nil, false, err
		}
		n.Children = ch
		anyHandlerCh := false
		for _, h := range n.Handlers {
			if h.Func == nil {
				continue
			}
			hbody, hCh, err := st.inlineStmts(h.Func.Block)
			if err != nil {
				return nil, false, err
			}
			h.Func.Block = hbody
			anyHandlerCh = anyHandlerCh || hCh
		}
		if !st.inlinable(n.Component) {
			return []ir.Stmt{n}, chCh || anyHandlerCh, nil
		}
		spliced, err := st.expandCall(n)
		if err != nil {
			return nil, false, err
		}
		return spliced, true, nil
	case *ir.If:
		body, ch1, err := st.inlineStmts(n.Body)
		if err != nil {
			return nil, false, err
		}
		els, ch2, err := st.inlineStmts(n.Else)
		if err != nil {
			return nil, false, err
		}
		n.Body = body
		n.Else = els
		return []ir.Stmt{n}, ch1 || ch2, nil
	case *ir.For:
		body, ch1, err := st.inlineStmts(n.Body)
		if err != nil {
			return nil, false, err
		}
		els, ch2, err := st.inlineStmts(n.Else)
		if err != nil {
			return nil, false, err
		}
		n.Body = body
		n.Else = els
		return []ir.Stmt{n}, ch1 || ch2, nil
	case *ir.PlatformFilter:
		body, ch, err := st.inlineStmts(n.Body)
		if err != nil {
			return nil, false, err
		}
		n.Body = body
		return []ir.Stmt{n}, ch, nil
	case *ir.SlotInst:
		ch, chCh, err := st.inlineStmts(n.Children)
		if err != nil {
			return nil, false, err
		}
		n.Children = ch
		return []ir.Stmt{n}, chCh, nil
	case *ir.ErrorBoundary:
		ch, chCh, err := st.inlineStmts(n.Children)
		if err != nil {
			return nil, false, err
		}
		n.Children = ch
		hCh := false
		if n.Handler != nil && n.Handler.Func != nil {
			body, b, err := st.inlineStmts(n.Handler.Func.Block)
			if err != nil {
				return nil, false, err
			}
			n.Handler.Func.Block = body
			hCh = b
		}
		return []ir.Stmt{n}, chCh || hCh, nil
	}
	return []ir.Stmt{s}, false, nil
}

// expandCall inlines a single NodeInst whose Component is inlinable.
//  1. Allocate a per-call suffix.
//  2. Deep-clone the callee's body.
//  3. For each Var/Func/Timer in the callee: deep-clone, rename to <name><suffix>,
//     hoist into main, and record a rename in the symbol map.
//  4. For each Prop: bind the call-site arg expression (or default) to the
//     prop's name.
//  5. Apply renameIdents and substituteParams over the cloned body.
//  6. Apply substituteSlots (handles <slot> inside the callee).
//  7. Apply substituteEvents (handler-body substitution at emit sites).
//  8. Return the prepared body for splicing into the caller.
func (st *inlineCompState) expandCall(n *ir.NodeInst) ([]ir.Stmt, error) {
	comp := n.Component
	suffix := st.freshSuffix()

	// Build rename map: symbol → new name.
	renames := map[ir.Symbol]string{}

	// Hoist vars.
	for _, v := range comp.Vars {
		clone := cloneVarShallow(v)
		clone.Name = v.Name + suffix
		clone.Init = deepCloneExpr(v.Init)
		renames[v] = clone.Name
		st.main.Vars = append(st.main.Vars, clone)
	}
	// Hoist funcs.
	for _, f := range comp.Funcs {
		clone := cloneFuncShallow(f)
		clone.Name = f.Name + suffix
		clone.Block = deepCloneStmts(f.Block)
		renames[f] = clone.Name
		st.main.Funcs = append(st.main.Funcs, clone)
	}
	// Hoist timers (no name to rename, but their handler block needs the
	// same identifier rewrites).
	for _, t := range comp.Timers {
		clone := *t
		clone.Interval = deepCloneExpr(t.Interval)
		clone.Enabled = deepCloneExpr(t.Enabled)
		if t.Handler != nil {
			h := *t.Handler
			h.Block = deepCloneStmts(t.Handler.Block)
			clone.Handler = &h
		}
		st.main.Timers = append(st.main.Timers, &clone)
	}

	// Apply renames to every hoisted block AND to the cloned body.
	for _, v := range st.main.Vars[len(st.main.Vars)-len(comp.Vars):] {
		if v.Init != nil {
			v.Init = renameInExpr(v.Init, renames)
		}
	}
	for _, f := range st.main.Funcs[len(st.main.Funcs)-len(comp.Funcs):] {
		f.Block = renameIdents(f.Block, renames)
	}
	for _, t := range st.main.Timers[len(st.main.Timers)-len(comp.Timers):] {
		if t.Handler != nil {
			t.Handler.Block = renameIdents(t.Handler.Block, renames)
		}
	}

	body := deepCloneStmts(comp.Body)
	body = renameIdents(body, renames)

	// Bind props: callsite arg wins; otherwise fall back to default.
	bindings := map[string]ir.Expr{}
	for _, p := range comp.Props {
		var val ir.Expr
		for _, arg := range n.Props {
			if arg.Name == p.Name {
				val = arg.Value
				break
			}
		}
		if val == nil {
			val = p.Default
		}
		if val != nil {
			bindings[p.Name] = val
		}
	}
	body = substituteParams(body, bindings) // reused from inline_pure.go
	// Also substitute prop refs in hoisted var initializers (the key win
	// over the current Renames hack: `var n = start` against start=count+1
	// becomes `var n__inst0 = count+1`).
	hoistStart := len(st.main.Vars) - len(comp.Vars)
	for i := hoistStart; i < len(st.main.Vars); i++ {
		if st.main.Vars[i].Init != nil {
			st.main.Vars[i].Init = substituteParamsExpr(st.main.Vars[i].Init, bindings)
		}
	}

	body = substituteSlots(body, n.Children)
	body = substituteEvents(body, n.Handlers)

	if n.ID != "" {
		for _, s := range body {
			if ni, ok := s.(*ir.NodeInst); ok {
				ni.ID = n.ID
				break
			}
		}
	}

	return body, nil
}

// cloneVarShallow returns a copy of v with Init left as the original (caller
// replaces with a deep clone).
func cloneVarShallow(v *ir.Var) *ir.Var {
	c := *v
	c.Handlers = nil // hoisted handlers handled per-var via deep clone below
	for _, h := range v.Handlers {
		hc := *h
		if h.Func != nil {
			fc := *h.Func
			fc.Block = deepCloneStmts(h.Func.Block)
			hc.Func = &fc
		}
		c.Handlers = append(c.Handlers, &hc)
	}
	return &c
}

// cloneFuncShallow returns a copy of f with Block left as nil (caller
// supplies the cloned block).
func cloneFuncShallow(f *ir.Func) *ir.Func {
	c := *f
	c.Block = nil
	return &c
}

// renameInExpr applies renameIdents to a single expression by wrapping it
// in a one-stmt slice. Mostly a convenience over newExprWalker for callers
// that only have an Expr.
func renameInExpr(e ir.Expr, renames map[ir.Symbol]string) ir.Expr {
	tmp := []ir.Stmt{&ir.LocalVar{Init: e}}
	tmp = renameIdents(tmp, renames)
	return tmp[0].(*ir.LocalVar).Init
}

// substituteParamsExpr applies substituteParams to a single expression.
func substituteParamsExpr(e ir.Expr, bindings map[string]ir.Expr) ir.Expr {
	tmp := []ir.Stmt{&ir.LocalVar{Init: e}}
	tmp = substituteParams(tmp, bindings)
	return tmp[0].(*ir.LocalVar).Init
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./internal/lower/...`
Expected: clean. Fix any import drift (`strconv` if you used it).

- [ ] **Step 3: Commit**

```bash
git add internal/lower/inline_components.go
git commit -m "lower: NoInlineComponents — clone+rename+prop-sub for one call site"
```

---

### Task B4: First golden fixture — single instance

**Files:**
- Create: `internal/lower/testdata/inline_components_basic.txtar`

- [ ] **Step 1: Write the fixture**

Write `internal/lower/testdata/inline_components_basic.txtar`:

```
caps: NoInlineComponents
-- input.sngl --
component Counter(start = 0) {
    var n = start

    text(value=string(n))
}

component main {
    Counter(start=5)
}
-- expected.sngl --
component Counter(start int = 0) {
    var n int = start
    text(value=string(n))
}
component main {
    var n__inst0 int = 5
    text(value=string(n__inst0))
}
```

(If the expected pretty-print differs in trivia, run with `-update` and review the diff before committing — the spec calls out that the post-pass IR is exactly what should be pinned.)

- [ ] **Step 2: Run with -update to seed if needed**

Run: `go test -run TestLower/inline_components_basic ./internal/lower/... -update`
Then inspect: `git diff internal/lower/testdata/inline_components_basic.txtar`
Expected: the inlined shape matches the design's hand-written expected (Counter declaration unchanged; main has the var and text).

- [ ] **Step 3: Run without -update**

Run: `go test -run TestLower/inline_components_basic ./internal/lower/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/lower/testdata/inline_components_basic.txtar
git commit -m "lower: golden — NoInlineComponents single-instance basic"
```

---

### Task B5: Drop inlined components from `pkg.Components`

**Files:**
- Modify: `internal/lower/inline_components.go`

- [ ] **Step 1: Decide which components survive**

The skeleton already calls `retainComponents(pkg.Components, st.keep)`. Confirm in `run()` that anything inlined never gets added back to `st.keep`. Inlinable components must NOT survive.

The current logic only adds main + cycles to `keep`. That's correct — but `Counter` from the fixture will still appear in the expected output because `expected.sngl` shows its declaration. Decide policy:

- Option A: keep the user's source-level Counter declaration in `pkg.Components` even after inlining, so dumps stay readable and downstream tooling (LSP, docs) doesn't lose the symbol.
- Option B (spec): drop it — codegens for opted-in targets must not see it.

The spec is explicit: "drop pkg.Components entries that were inlined." Go with B. Update the basic.txtar fixture's `expected.sngl` to remove the `component Counter` block.

- [ ] **Step 2: Update fixture**

Edit `internal/lower/testdata/inline_components_basic.txtar` — remove the `component Counter(...) { ... }` declaration from `expected.sngl`. Final form:

```
caps: NoInlineComponents
-- input.sngl --
component Counter(start = 0) {
    var n = start
    text(value=string(n))
}
component main {
    Counter(start=5)
}
-- expected.sngl --
component main {
    var n__inst0 int = 5
    text(value=string(n__inst0))
}
```

- [ ] **Step 3: Run the fixture**

Run: `go test -run TestLower/inline_components_basic ./internal/lower/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/lower/testdata/inline_components_basic.txtar
git commit -m "lower: drop inlined components from pkg.Components"
```

---

## Phase C — Multi-instance, events, timers, nesting

### Task C1: Multi-instance fixture

**Files:**
- Create: `internal/lower/testdata/inline_components_multi_instance.txtar`

- [ ] **Step 1: Write the fixture**

```
caps: NoInlineComponents
-- input.sngl --
component Counter(start = 0) {
    var n = start
    text(value=string(n))
}
component main {
    Counter(start=5)
    Counter(start=10)
}
-- expected.sngl --
component main {
    var n__inst0 int = 5
    var n__inst1 int = 10
    text(value=string(n__inst0))
    text(value=string(n__inst1))
}
```

- [ ] **Step 2: Run**

Run: `go test -run TestLower/inline_components_multi_instance ./internal/lower/...`
Expected: PASS (the per-call `freshSuffix` allocator already produces `__inst0`, `__inst1`).

- [ ] **Step 3: Commit**

```bash
git add internal/lower/testdata/inline_components_multi_instance.txtar
git commit -m "lower: golden — NoInlineComponents per-instance state separation"
```

---

### Task C2: Event-handler inlining fixture

**Files:**
- Create: `internal/lower/testdata/inline_components_events.txtar`

- [ ] **Step 1: Write the fixture**

```
caps: NoInlineComponents
-- input.sngl --
component LabeledCounter(start = 0) {
    var n = start
    event @changed(c int)
    button(label=string(n)) {
        @click {
            n = n + 1
            @changed(n)
        }
    }
}
component main {
    var clicks = 0
    LabeledCounter(start=0) {
        @changed(c) { clicks = c }
    }
}
-- expected.sngl --
component main {
    var clicks int = 0
    var n__inst0 int = 0
    button(label=string(n__inst0)) {
        @click {
            n__inst0 = n__inst0 + 1
            clicks = n__inst0
        }
    }
}
```

- [ ] **Step 2: Run**

Run: `go test -run TestLower/inline_components_events ./internal/lower/...`
Expected: PASS. `substituteEvents` (reused from inline_pure.go) already replaces `@changed(n__inst0)` with the parent's handler block, binding `c → n__inst0`.

If output diverges, inspect with `-update` and diagnose: most likely the handler-emit substitution isn't recognising emits inside nested `@click` blocks. Fix by ensuring `substituteEvents` is applied recursively over the inlined body before any `@click` handler is collapsed (it is — already recursive into `NodeInst.Handlers`).

- [ ] **Step 3: Commit**

```bash
git add internal/lower/testdata/inline_components_events.txtar
git commit -m "lower: golden — NoInlineComponents event-handler inlining"
```

---

### Task C3: Timer hoisting fixture

**Files:**
- Create: `internal/lower/testdata/inline_components_timer.txtar`

- [ ] **Step 1: Write the fixture**

```
caps: NoInlineComponents
-- input.sngl --
component Ticker(rate = 1s) {
    var tick = 0
    timer(interval=rate) { tick = tick + 1 }
    text(value=string(tick))
}
component main {
    Ticker(rate=2s)
}
-- expected.sngl --
component main {
    var tick__inst0 int = 0
    timer(interval=2s) { tick__inst0 = tick__inst0 + 1 }
    text(value=string(tick__inst0))
}
```

- [ ] **Step 2: Run**

Run: `go test -run TestLower/inline_components_timer ./internal/lower/...`
Expected: PASS. The timer is hoisted into `main.Timers`, its interval expression has the prop-substituted value (2s, not `rate`), and its handler body uses `tick__inst0`.

If timer hoisting isn't producing the substituted interval, that's because `expandCall` deep-clones `t.Interval` but doesn't run prop substitution on it. Fix: in `expandCall`, after building `bindings`, also walk the newly-appended timer's Interval/Enabled/Handler block with both `substituteParams` and `renameIdents`:

```go
tStart := len(st.main.Timers) - len(comp.Timers)
for i := tStart; i < len(st.main.Timers); i++ {
	t := st.main.Timers[i]
	if t.Interval != nil {
		t.Interval = substituteParamsExpr(t.Interval, bindings)
	}
	if t.Enabled != nil {
		t.Enabled = substituteParamsExpr(t.Enabled, bindings)
	}
	if t.Handler != nil {
		t.Handler.Block = substituteParams(t.Handler.Block, bindings)
	}
}
```

Add that block, rerun, then commit both the fixture and the fix.

- [ ] **Step 3: Commit**

```bash
git add internal/lower/inline_components.go internal/lower/testdata/inline_components_timer.txtar
git commit -m "lower: NoInlineComponents — prop-substitute and rename hoisted timers"
```

---

### Task C4: Nested-component (Page → LabeledCounter) fixture

**Files:**
- Create: `internal/lower/testdata/inline_components_nested.txtar`

- [ ] **Step 1: Write the fixture**

```
caps: NoInlineComponents
-- input.sngl --
component Counter(start = 0) {
    var n = start
    text(value=string(n))
}
component Page(initial = 0) {
    Counter(start=initial)
    Counter(start=initial + 10)
}
component main {
    Page(initial=1)
}
-- expected.sngl --
component main {
    var n__inst1 int = 1
    text(value=string(n__inst1))
    var n__inst2 int = 1 + 10
    text(value=string(n__inst2))
}
```

(Suffix numbering depends on iteration order: Page is expanded first → bumps counter by 1 (no vars, but the call site increments). Then the two Counter call sites get __inst1 and __inst2. If your numbering produces a different shape, that's acceptable — use `-update` and verify the substituted values are correct, then pin.)

- [ ] **Step 2: Run**

Run: `go test -run TestLower/inline_components_nested ./internal/lower/...`
Expected: PASS. Fixed-point iteration drives the second round; Page's body (two Counter calls) gets substituted into main, then the next round inlines both Counters.

- [ ] **Step 3: Commit**

```bash
git add internal/lower/testdata/inline_components_nested.txtar
git commit -m "lower: golden — NoInlineComponents fixed-point nested inlining"
```

---

### Task C5: Recursion fixture (cycle preservation)

**Files:**
- Create: `internal/lower/testdata/inline_components_recursion.txtar`

- [ ] **Step 1: Write the fixture**

```
caps: NoInlineComponents
-- input.sngl --
component Tree(node dyn) {
    text(value="node")
    for child = node.children {
        Tree(node=child)
    }
}
component main {
    Tree(node=root)
}
-- expected.sngl --
component Tree(node dyn) {
    text(value="node")
    for child = node.children {
        Tree(node=child)
    }
}
component main {
    Tree(node=root)
}
```

(Tree is in a self-cycle → `cycles[Tree]=true` → not inlinable → unchanged. main's body still has a `Tree(...)` NodeInst because the cycle gate blocked the call-site expansion too.)

- [ ] **Step 2: Run**

Run: `go test -run TestLower/inline_components_recursion ./internal/lower/...`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/lower/testdata/inline_components_recursion.txtar
git commit -m "lower: golden — NoInlineComponents preserves recursive cycles"
```

---

## Phase D — Integration: opt fyne in, delete the workarounds

### Task D1: Turn on the cap for fyne (initial target)

**Files:**
- Modify: `codegen/platform/fyne/fyne.go` (or wherever its `Capabilities()` lives — verify before editing)

- [ ] **Step 1: Locate the Caps assembly site for fyne**

Run: `grep -rn 'NoLambda\|NoReactivity\|Caps{' codegen/platform/fyne/ | head`
Expected: finds the `Capabilities()` or equivalent method that returns `lower.Caps{...}`.

- [ ] **Step 2: Flip on the cap**

Add `NoInlineComponents: true,` to fyne's Caps literal.

- [ ] **Step 3: Run full test suite**

Run: `go tool verify`
Expected: green. If fyne golden snapshots have drifted because main now carries the inlined state, regenerate them with the project's snapshot-update flow (consult `cmd/sngl/testdata/` and any platform-snapshot harness — do not blindly overwrite; inspect first).

- [ ] **Step 4: Commit**

```bash
git add codegen/platform/fyne/...
git commit -m "fyne: opt into NoInlineComponents"
```

---

### Task D2: Delete fyne `Renames` workaround

**Files:**
- Modify: `codegen/platform/fyne/compiler_ir.go`

- [ ] **Step 1: Locate the loop**

Open `codegen/platform/fyne/compiler_ir.go` around line 167. The block reads:

```go
// same fix — Renames maps prop name → rendered Go expression.
for _, p := range tv.comp.Props {
    if p.Default == nil {
        ...
    }
    varGC.Ctx.Renames[p.Name] = varGC.EvalExpr(p.Default)
}
```

- [ ] **Step 2: Delete it**

Remove the entire block (and the comment explaining "same fix"). Also remove the `if v.Synthesized` skip at line ~124 if it exists solely to avoid emitting non-main component vars. Inspect first — if the Synthesized check is doing other work, leave it.

- [ ] **Step 3: Narrow taggedVar walk**

Locate the loop that iterates `pkg.Components` to collect tagged vars. After this pass, only `main` should have user vars. Change it to walk `main.Vars` + `pkg.Vars` (+ const decls) only. Reference: spec §"What stops being needed".

- [ ] **Step 4: Verify**

Run: `go tool verify`
Expected: green. fyne examples in `examples/showcase/app.sngl` should produce identical visible output to the pre-cap baseline. Spot-check by running the showcase under fyne and comparing the rendered window.

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/fyne/compiler_ir.go
git commit -m "fyne: drop Renames prop-default hack — handled by NoInlineComponents"
```

---

### Task D3: Turn on the cap for bubbletea and delete its workaround

**Files:**
- Modify: `codegen/platform/bubbletea/bubbletea.go` (Caps site)
- Modify: `codegen/platform/bubbletea/compiler_ir.go` (delete loop)

- [ ] **Step 1: Flip cap**

Same shape as Task D1 — locate Caps assembly site for bubbletea, add `NoInlineComponents: true,`.

- [ ] **Step 2: Delete Renames loop**

In `codegen/platform/bubbletea/compiler_ir.go` around line 177-188, delete the `for _, p := range tv.comp.Props` loop that populates `varGC.Ctx.Renames` from prop defaults.

- [ ] **Step 3: Narrow taggedVar walk to main only**

Same as Task D2 step 3.

- [ ] **Step 4: Verify**

Run: `go tool verify`
Expected: green. Run the showcase under bubbletea (`sngl run --platform bubbletea examples/showcase`) and compare interactive behavior to the baseline if possible.

- [ ] **Step 5: Commit**

```bash
git add codegen/platform/bubbletea/...
git commit -m "bubbletea: opt into NoInlineComponents + drop Renames hack"
```

---

### Task D4: Opt golang language and kotlin language (caps merge)

**Files:**
- Modify: `codegen/lang/golang/golang.go`
- Modify: `codegen/lang/kotlin/kotlin.go`

The spec says the cap belongs on the language side (golang + kotlin) so any platform paired with those languages gets it. If the platform-side flip in D1/D3 was sufficient, this task may be redundant — but enabling at the language level is closer to the spec.

- [ ] **Step 1: Locate Capabilities() for golang**

Run: `grep -n 'Capabilities\|Caps{' codegen/lang/golang/golang.go`

- [ ] **Step 2: Add the cap**

Add `NoInlineComponents: true,` to golang's returned Caps.

- [ ] **Step 3: Same for kotlin**

Repeat for `codegen/lang/kotlin/kotlin.go`.

- [ ] **Step 4: Verify**

Run: `go tool verify`
Expected: green. Android golden snapshots (`codegen/platform/android/testdata/`) may need regen — inspect each diff.

- [ ] **Step 5: Commit**

```bash
git add codegen/lang/golang/golang.go codegen/lang/kotlin/kotlin.go
git commit -m "lang: golang + kotlin opt into NoInlineComponents"
```

---

### Task D5: Acceptance sweep

- [ ] **Step 1: Verify the design's acceptance bullets**

Confirm each:
- `go test ./...` clean.
- `go tool verify` green on golang and kotlin targets.
- HTML snapshot fixtures unchanged (cap is off for JS).
- The `Renames`-hack in bubbletea/fyne compiler_ir.go is deleted (not neutered).
- The per-instance fixture (`inline_components_multi_instance.txtar`) demonstrates distinct state for two LabeledCounters with different `start` values.

- [ ] **Step 2: Smoke-test the showcase**

Run the showcase end-to-end under fyne and bubbletea:

```bash
go install ./cmd/sngl
sngl run --platform fyne examples/showcase
sngl run --platform bubbletea examples/showcase
```

Expected: both render and behave like the pre-cap baseline. Two LabeledCounters in the showcase keep independent state when clicked.

- [ ] **Step 3: No commit — this is a verification gate**

If acceptance fails for a specific platform, file a follow-up plan rather than partially reverting.

---

## Open questions to resolve during execution

These are flagged in the spec — each needs a concrete answer before D-phase merges:

1. **Reactivity over inlined deps.** The reactivity pass runs after this one and should pick up `var n__inst0 = count` as reactive on `count`. Verify with `inline_components_events.txtar` — when `clicks` is mutated through the substituted handler, the rendered text should refresh.

2. **Multi-arg events.** `substituteEvents` (in inline_pure.go) handles single-arg events. If the showcase or any fixture exercises multi-arg `@event(a, b)`, extend `bindEventParams` (already accepts `[]CallArg`) and add a fixture. If not exercised, leave as-is and document.

3. **Native components.** Already gated by `comp.Native != nil` in `inlinable()`. Add a fixture under Phase C if a GTK widget composition exists in test surface; otherwise skip.

4. **Source positions.** Verify checker error messages from inlined code still point at the original component. `deepCloneStmts` should preserve `ast.Pos` (it copies the AST pointer fields unchanged — confirm by reading `deepCloneStmt` in `inline_pure.go`).

5. **Code size.** Not blocking. If a project hits a hot spot, the cap is per-target and can be turned off without code changes.

---

## Self-review checklist (for the planner only)

- [X] Every spec section maps to a phase or task above.
- [X] No placeholders, no "TBD", no "implement appropriate X".
- [X] Method names are consistent across tasks (`inlinable`, `expandCall`, `freshSuffix`, `renameIdents`, `substituteParams`, `substituteEvents`, `substituteSlots`).
- [X] File paths are exact.
- [X] Each fixture includes complete `input.sngl` and `expected.sngl` blocks, not just shapes.
- [X] Reuse of existing helpers (`deepCloneStmts`, `deepCloneExpr`, `newExprWalker`, `substituteParams`, `substituteEvents`) is called out — no duplicated cloning machinery.
