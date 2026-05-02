# Codegen Lowering — Phase 3a (NoComputed) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `NoComputed` — inline expression-bodied computed funcs at every call site. The other Phase 3 passes (NoReactivity, NoTimer, NoDeclarative) are deferred to their own specs because each needs IR-shape design discussion that doesn't belong in an implementation plan.

**Deferred (filed as gitlab issues):**
- NoReactivity — issue #41
- NoTimer — issue #42
- NoDeclarative — issue #43
- NoLambda (Phase 2 holdover) — issue #40

**Architecture:** Walk every Func/Component/Window. Identify computed funcs (zero-param, expression body, non-test — same predicate `codegen.IsComputed` uses). At every call site whose `Call.Func` resolves to a computed func, replace the Call with the body expression of the computed. After all sites are rewritten, remove the inlined funcs from their owning collection.

**Tech Stack:** Go (Go 1.24+), existing `ir`/`ast`/`internal/checker`/`internal/parser`/`internal/lower` from prior phases.

**Reference spec:** `docs/superpowers/specs/2026-05-02-codegen-lowering-design.md`

---

## File Structure

**Create:**
- `internal/lower/testdata/computed_basic.txtar`
- `internal/lower/testdata/computed_in_text.txtar`
- `internal/lower/testdata/computed_chain.txtar`

**Modify:**
- `internal/lower/computed.go` — replace stub

---

### Task 1: NoComputed implementation

**Files:**
- Modify: `internal/lower/computed.go`
- Create: 3 txtar fixtures

The pass:

1. Build a map `computedBodies: *ir.Func → ir.Expr`. For every component-scoped, window-scoped, and package-scoped Func, check `isComputed(f)` (a local copy of the `IsComputed` predicate). If yes, extract the body expression — for an expression-body computed, the checker has emitted `Func.Block = []ir.Stmt{*ir.Return{Value: bodyExpr}}`. Stash the bodyExpr.
2. Walk every expression in the package via `walkPackage`. At every `*ir.Call`, if `Call.Func` is in `computedBodies`, replace the Call with the stashed expression. Recurse — the inlined expression may itself contain computed calls.
3. After the rewrite, remove inlined funcs from their owning lists.

The pass intentionally does **not** rewrite multi-statement computeds. The checker's `IsComputed` predicate already excludes them (gates on `AST.Body != nil` — only expression-body funcs match). Multi-stmt funcs that look computed structurally remain as regular funcs in the lowered output.

- [ ] **Step 1: Write failing goldens**

Create `internal/lower/testdata/computed_basic.txtar`:

```
caps: NoComputed
-- input.sngl --
component main {
    var n int = 5

    func double() int => n * 2

    text(value=string(double()))
}
-- expected.sngl --
```

Create `internal/lower/testdata/computed_in_text.txtar`:

```
caps: NoComputed
-- input.sngl --
component main {
    var first string = "Hello"
    var last string = "World"

    func full() string => first + " " + last

    text(value=full())
}
-- expected.sngl --
```

Create `internal/lower/testdata/computed_chain.txtar`:

```
caps: NoComputed
-- input.sngl --
component main {
    var n int = 3

    func a() int => n + 1
    func b() int => a() * 2

    text(value=string(b()))
}
-- expected.sngl --
```

The chain case verifies recursive inlining: `b()` should inline to `a() * 2` then to `(n + 1) * 2`.

- [ ] **Step 2: Implement NoComputed**

Replace `internal/lower/computed.go` with:

```go
package lower

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

var passComputed = pass{
	name:    "NoComputed",
	enabled: func(c Caps) bool { return c.NoComputed },
	apply:   lowerComputed,
}

// lowerComputed inlines every expression-body computed func at its call
// sites. The inlined funcs are then removed from their owning collection.
//
// A computed func is zero-param, non-test, and has an AST expression body
// (matching codegen.IsComputed). The checker stores the body as a single
// Return statement: Func.Block = []ir.Stmt{*ir.Return{Value: expr}}.
//
// Multi-statement computeds are not inlined (they don't satisfy isComputed).
func lowerComputed(pkg *ir.Package) error {
	if pkg == nil {
		return nil
	}

	bodies := make(map[*ir.Func]ir.Expr)
	collectComputed(pkg.Funcs, bodies)
	for _, comp := range pkg.Components {
		collectComputed(comp.Funcs, bodies)
	}
	for _, w := range pkg.Windows {
		collectComputed(w.Funcs, bodies)
	}

	if len(bodies) == 0 {
		return nil
	}

	rewrite := func(e ir.Expr) ir.Expr {
		return inlineComputedExpr(e, bodies)
	}
	walkPackage(pkg, walkFuncs{
		expr:  rewrite,
		stmts: func(stmts []ir.Stmt) []ir.Stmt { return inlineComputedStmts(stmts, rewrite) },
	})

	// Remove inlined funcs from their owners.
	pkg.Funcs = filterFuncs(pkg.Funcs, bodies)
	for _, comp := range pkg.Components {
		comp.Funcs = filterFuncs(comp.Funcs, bodies)
	}
	for _, w := range pkg.Windows {
		w.Funcs = filterFuncs(w.Funcs, bodies)
	}
	return nil
}

func isComputed(f *ir.Func) bool {
	return f.AST != nil && f.AST.Body != nil && len(f.Params) == 0 && !f.IsTest
}

func collectComputed(funcs []*ir.Func, out map[*ir.Func]ir.Expr) {
	for _, f := range funcs {
		if !isComputed(f) {
			continue
		}
		if len(f.Block) != 1 {
			continue
		}
		ret, ok := f.Block[0].(*ir.Return)
		if !ok || ret.Value == nil {
			continue
		}
		out[f] = ret.Value
	}
}

func filterFuncs(funcs []*ir.Func, removed map[*ir.Func]ir.Expr) []*ir.Func {
	out := funcs[:0]
	for _, f := range funcs {
		if _, drop := removed[f]; drop {
			continue
		}
		out = append(out, f)
	}
	return out
}

// inlineComputedExpr replaces every Call to a computed func with the
// computed's body expression. Recurses so nested calls (b() inlines to
// a()*2 inlines to (n+1)*2) collapse in one pass.
func inlineComputedExpr(e ir.Expr, bodies map[*ir.Func]ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *ir.Call:
		if x.Func != nil {
			if body, ok := bodies[x.Func]; ok {
				// Recurse: the inlined body itself may reference computed
				// funcs.
				return inlineComputedExpr(body, bodies)
			}
		}
		if x.Receiver != nil {
			x.Receiver = inlineComputedExpr(x.Receiver, bodies)
		}
		for i := range x.Args {
			x.Args[i].Value = inlineComputedExpr(x.Args[i].Value, bodies)
		}
		return x
	case *ir.Binary:
		x.Left = inlineComputedExpr(x.Left, bodies)
		x.Right = inlineComputedExpr(x.Right, bodies)
		return x
	case *ir.Unary:
		x.Operand = inlineComputedExpr(x.Operand, bodies)
		return x
	case *ir.Ternary:
		x.Cond = inlineComputedExpr(x.Cond, bodies)
		x.Then = inlineComputedExpr(x.Then, bodies)
		x.Else = inlineComputedExpr(x.Else, bodies)
		return x
	case *ir.Conversion:
		x.Operand = inlineComputedExpr(x.Operand, bodies)
		return x
	case *ir.Select:
		x.Operand = inlineComputedExpr(x.Operand, bodies)
		return x
	case *ir.Index:
		x.Operand = inlineComputedExpr(x.Operand, bodies)
		x.Idx = inlineComputedExpr(x.Idx, bodies)
		return x
	case *ir.ListLit:
		for i := range x.Elems {
			x.Elems[i] = inlineComputedExpr(x.Elems[i], bodies)
		}
		return x
	case *ir.StructLit:
		for i := range x.Fields {
			if x.Fields[i].Value != nil {
				x.Fields[i].Value = inlineComputedExpr(x.Fields[i].Value, bodies)
			}
		}
		return x
	case *ir.Spread:
		x.Operand = inlineComputedExpr(x.Operand, bodies)
		return x
	}
	return e
}

func inlineComputedStmts(stmts []ir.Stmt, rewrite func(ir.Expr) ir.Expr) []ir.Stmt {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.Assign:
			n.Value = rewrite(n.Value)
		case *ir.LocalVar:
			if n.Init != nil {
				n.Init = rewrite(n.Init)
			}
		case *ir.Return:
			if n.Value != nil {
				n.Value = rewrite(n.Value)
			}
		case *ir.If:
			n.Cond = rewrite(n.Cond)
			n.Body = inlineComputedStmts(n.Body, rewrite)
			n.Else = inlineComputedStmts(n.Else, rewrite)
		case *ir.For:
			n.Iter = rewrite(n.Iter)
			n.Body = inlineComputedStmts(n.Body, rewrite)
			n.Else = inlineComputedStmts(n.Else, rewrite)
		case *ir.PlatformFilter:
			n.Body = inlineComputedStmts(n.Body, rewrite)
		case *ir.NodeInst:
			for i := range n.Props {
				if n.Props[i].Value != nil {
					n.Props[i].Value = rewrite(n.Props[i].Value)
				}
			}
			if n.Key != nil {
				n.Key = rewrite(n.Key)
			}
			if n.Ref != nil {
				n.Ref = rewrite(n.Ref)
			}
			n.Children = inlineComputedStmts(n.Children, rewrite)
			for i := range n.Handlers {
				if n.Handlers[i].Func != nil {
					n.Handlers[i].Func.Block = inlineComputedStmts(n.Handlers[i].Func.Block, rewrite)
				}
			}
		case *ir.SlotInst:
			n.Children = inlineComputedStmts(n.Children, rewrite)
		case *ir.ErrorBoundary:
			n.Children = inlineComputedStmts(n.Children, rewrite)
			if n.Handler != nil && n.Handler.Func != nil {
				n.Handler.Func.Block = inlineComputedStmts(n.Handler.Func.Block, rewrite)
			}
		case *ir.Emit:
			for i := range n.Args {
				n.Args[i].Value = rewrite(n.Args[i].Value)
			}
		case *ir.CallStmt:
			if n.Call != nil {
				if n.Call.Receiver != nil {
					n.Call.Receiver = rewrite(n.Call.Receiver)
				}
				for i := range n.Call.Args {
					n.Call.Args[i].Value = rewrite(n.Call.Args[i].Value)
				}
			}
		case *ir.Window:
			if n.Href != nil {
				n.Href = rewrite(n.Href)
			}
			if n.Title != nil {
				n.Title = rewrite(n.Title)
			}
			if n.Favicon != nil {
				n.Favicon = rewrite(n.Favicon)
			}
			n.Body = inlineComputedStmts(n.Body, rewrite)
		}
	}
	return stmts
}
```

- [ ] **Step 3: Update goldens**

Run: `go test ./internal/lower/ -run TestLower -update`
Expected: PASS, three new `expected.sngl` sections populated.

- [ ] **Step 4: Inspect goldens**

```bash
cat internal/lower/testdata/computed_basic.txtar
cat internal/lower/testdata/computed_in_text.txtar
cat internal/lower/testdata/computed_chain.txtar
```

Expected for `computed_basic`:
```
component main {
    var n int = 5
    text(value=string(n * 2))
}
```

Expected for `computed_chain`:
```
component main {
    var n int = 3
    text(value=string((n + 1) * 2))
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/lower/`
Expected: PASS.

- [ ] **Step 6: Run full suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/lower/computed.go internal/lower/testdata/computed_basic.txtar internal/lower/testdata/computed_in_text.txtar internal/lower/testdata/computed_chain.txtar
git commit -m "$(cat <<'EOF'
Implement NoComputed lowering pass

Inlines every expression-body computed func at its call sites and
removes the inlined funcs from their owners. Recursive inlining
collapses chains (b() → a()*2 → (n+1)*2) in a single pass.

Three goldens cover bare inlining, inlining inside a text() prop, and
the chain case. Multi-statement computeds (those not satisfying
codegen.IsComputed) are left as regular functions.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Final verification

- [ ] **Step 1: Run full project verify**

Run: `go tool verify`
Expected: PASS.

- [ ] **Step 2: Confirm Phase 3 scope is closed (partially)**

Phase 3 in the spec lists: NoComputed, NoReactivity, NoTimer, NoDeclarative. After this plan ships, only NoComputed lands; the other three are tracked as gitlab issues #41, #42, #43 awaiting design specs.

---

## Self-Review Notes

Spec coverage:
- §Migration Plan Phase 3 — NoComputed (this plan). NoReactivity/NoTimer/NoDeclarative deferred with rationale.
- §Pass ordering — registry order: NoUnit → NoEnum → NoTernary → **NoComputed** → NoLambda → NoToggle → NoReactivity → NoTimer → NoDeclarative. NoComputed runs before NoReactivity (already enforced in the registry); when NoReactivity lands, dataflow analysis sees plain reads instead of computed indirections.

Risks:

1. **Free idents in inlined body.** A computed func body references the surrounding scope's vars by `*ir.Ident{Sym: *Var}`. Inlining at a call site keeps the same Ident, which still resolves correctly because computed funcs are scoped to the same component/window/package as their use sites. Multi-component scenarios where a top-level computed is called from inside a component would carry over scope correctly — the Ident's Sym is unchanged.
2. **Multi-call inlining of side-effect-bearing computeds.** A computed func that calls a non-pure function would have its body expression evaluated multiple times if the call appears multiple times. Today `codegen.IsComputed` doesn't gate on purity, so this is a possible footgun. For Phase 3a, accept the risk — the optimizer's inliner has the same property.
3. **Effects metadata loss.** `Func.Reads` / `Func.Writes` populated by purity analysis disappear when the func is removed. Future passes (NoReactivity in particular) will rederive deps from the inlined expression directly; no information is lost as long as the rederivation is consistent.

Type consistency check:
- `pass`, `walkFuncs`, `walkPackage` are defined in Phase 1/2.
- Helper functions `isComputed`, `collectComputed`, `filterFuncs`, `inlineComputedExpr`, `inlineComputedStmts` are all defined in this task.
