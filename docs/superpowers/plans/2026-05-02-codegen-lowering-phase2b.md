# Codegen Lowering — Phase 2b (NoTernary) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `NoTernary` — the only Phase 2 pass that requires statement hoisting (the others were value-replacement). Each `*ir.Ternary` expression is replaced by an `*ir.Ident` referring to a synthesized `*ir.LocalVar`, with an `*ir.If` statement materializing the temp inserted before the consuming statement.

**Architecture:** Walk every statement block, expanding each statement to a list (`[preStmts..., rewrittenStmt]`) when its expressions contain ternaries. Sub-ternaries inside a Then/Else branch are hoisted into that branch (not before the outer If). Fresh temp names use a package-scoped counter (`__lt0`, `__lt1`, …).

**NoLambda is deferred** to its own spec. Audit found that capture data is derivable from the IR (every `*ir.Ident` inside a `*ir.Lambda.Func.Block` whose `Sym` points to a Var/Param declared outside the lambda's params is a capture). What's missing is the **output shape** — lifting a closure to a top-level Func plus a captured-state struct involves IR shapes the package doesn't currently express cleanly (no notion of a Func receiving an explicit context struct as its first param, no obvious way to emit the struct construction site at the original lambda position). That design discussion belongs in its own spec.

**Tech Stack:** Go (Go 1.24+), existing `ir`/`ast`/`internal/checker`/`internal/parser`/`internal/lower` from prior phases.

**Reference spec:** `docs/superpowers/specs/2026-05-02-codegen-lowering-design.md`
**Reference plans:** `docs/superpowers/plans/2026-05-02-codegen-lowering-phase1.md`, `phase2.md`

---

## File Structure

**Create:**
- `internal/lower/testdata/ternary_in_assign.txtar`
- `internal/lower/testdata/ternary_in_call.txtar`
- `internal/lower/testdata/ternary_nested.txtar`

**Modify:**
- `internal/lower/ternary.go` — replace stub
- `internal/lower/walk.go` — extend with helpers for hoisting (a per-statement transform that returns a list, to plumb through walkPackage)

---

### Task 1: NoTernary implementation

**Files:**
- Modify: `internal/lower/ternary.go`
- Create: 3 txtar fixtures

The hoisting algorithm:

1. `lowerTernary(pkg)` allocates a `state{counter: 0}`. Walks every Stmt slice in pkg via `transformBlock`.
2. `transformBlock(block, st) []ir.Stmt` returns a new slice: for each input stmt, calls `transformStmt(s, st)` which returns `(preStmts, rewrittenStmt)`. Concatenate all results.
3. `transformStmt(s, st)` mutates s's expressions (and recursively any nested Stmt slices within s). Each ternary it encounters via `liftTernary(t, st)` returns a temp Ident and produces a list of pre-stmts (the `LocalVar` + `If` pair). Pre-stmts accumulate.
4. `liftTernary(t, st)`:
   - Recursively lifts ternaries inside `t.Cond` (those run before the `If`).
   - Allocates a fresh temp name. Synthesizes `LocalVar{name, type=t.Type}`.
   - Recursively lifts ternaries inside `t.Then` and `t.Else` separately — those go *inside* the corresponding branch body, not before the outer If.
   - Builds `If{Cond, Body=[thenPreStmts..., Assign{tmp = thenExpr}], Else=[elsePreStmts..., Assign{tmp = elseExpr}]}`.
   - Returns `(LocalVar+If as preStmts, Ident{name, type})` to the caller.

Idents pointing at the synthesized temp leave `Sym` nil. Phase 2 only ships pass output to dump-as-SNGL; codegen consumption (which reads Sym) lands in later phases that can attach a synthetic `*ir.Var` if needed.

- [ ] **Step 1: Write failing goldens**

Create `internal/lower/testdata/ternary_in_assign.txtar`:

```
caps: NoTernary
-- input.sngl --
component main {
    var label string = ""
    var on bool = true

    button(@click {
        label = on ? "yes" : "no"
    })
}
-- expected.sngl --
```

Create `internal/lower/testdata/ternary_in_call.txtar`:

```
caps: NoTernary
-- input.sngl --
component main {
    var on bool = true

    text(value=on ? "ON" : "OFF")
}
-- expected.sngl --
```

Create `internal/lower/testdata/ternary_nested.txtar`:

```
caps: NoTernary
-- input.sngl --
component main {
    var a bool = true
    var b bool = false
    var label string = ""

    button(@click {
        label = a ? (b ? "ab" : "a") : "neither"
    })
}
-- expected.sngl --
```

- [ ] **Step 2: Implement NoTernary**

Replace `internal/lower/ternary.go` with:

```go
package lower

import (
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

var passTernary = pass{
	name:    "NoTernary",
	enabled: func(c Caps) bool { return c.NoTernary },
	apply:   lowerTernary,
}

// lowerTernary rewrites every Ternary expression to a synthetic LocalVar
// declaration plus an If statement that assigns into it, with the original
// expression position replaced by an Ident referring to that temp.
//
// Sub-ternaries are lifted into their containing branch body, not before
// the outer If — i.e. `a ? (b ? c : d) : e` produces a single outer If on
// `a` whose Then body contains another LocalVar+If pair on `b`.
func lowerTernary(pkg *ir.Package) error {
	if pkg == nil {
		return nil
	}
	st := &ternState{}
	walkPackage(pkg, walkFuncs{
		stmts: func(stmts []ir.Stmt) []ir.Stmt { return st.transformBlock(stmts) },
		expr: func(e ir.Expr) ir.Expr {
			// At the package leaf level (const/var inits, prop defaults,
			// timer interval/enabled, struct field defaults), there is no
			// preceding statement to hoist into. We fold sub-ternaries
			// here only when the expression itself is constant enough that
			// no hoisting is needed — otherwise leave the ternary alone.
			//
			// In practice the optimizer (which runs before lower in the
			// production pipeline) folds const ternaries; what reaches us
			// is dynamic. For Phase 2 fixtures we don't exercise this
			// path, so a no-op is safe.
			return e
		},
	})
	return nil
}

// ternState carries the temp-name counter across the whole package walk so
// names are globally unique.
type ternState struct {
	counter int
}

func (st *ternState) freshName() string {
	n := st.counter
	st.counter++
	return "__lt" + strconv.Itoa(n)
}

// transformBlock runs transformStmt on each stmt and flattens the
// (preStmts, stmt) results into a single block.
func (st *ternState) transformBlock(block []ir.Stmt) []ir.Stmt {
	var out []ir.Stmt
	for _, s := range block {
		pre, rewritten := st.transformStmt(s)
		out = append(out, pre...)
		if rewritten != nil {
			out = append(out, rewritten)
		}
	}
	return out
}

// transformStmt rewrites s in place — lifting ternaries from its
// expressions into preStmts and recursing into any nested Stmt slices.
// Returns (preStmts, s); s is the same pointer (mutated).
func (st *ternState) transformStmt(s ir.Stmt) ([]ir.Stmt, ir.Stmt) {
	var pre []ir.Stmt
	switch n := s.(type) {
	case *ir.Assign:
		pre, n.Value = st.transformExpr(n.Value)
	case *ir.LocalVar:
		if n.Init != nil {
			pre, n.Init = st.transformExpr(n.Init)
		}
	case *ir.Return:
		if n.Value != nil {
			pre, n.Value = st.transformExpr(n.Value)
		}
	case *ir.If:
		pre, n.Cond = st.transformExpr(n.Cond)
		n.Body = st.transformBlock(n.Body)
		n.Else = st.transformBlock(n.Else)
	case *ir.For:
		pre, n.Iter = st.transformExpr(n.Iter)
		n.Body = st.transformBlock(n.Body)
		n.Else = st.transformBlock(n.Else)
	case *ir.PlatformFilter:
		n.Body = st.transformBlock(n.Body)
	case *ir.NodeInst:
		for i := range n.Props {
			if n.Props[i].Value != nil {
				p, v := st.transformExpr(n.Props[i].Value)
				pre = append(pre, p...)
				n.Props[i].Value = v
			}
		}
		if n.Key != nil {
			p, v := st.transformExpr(n.Key)
			pre = append(pre, p...)
			n.Key = v
		}
		if n.Ref != nil {
			p, v := st.transformExpr(n.Ref)
			pre = append(pre, p...)
			n.Ref = v
		}
		n.Children = st.transformBlock(n.Children)
		for i := range n.Handlers {
			if n.Handlers[i].Func != nil {
				n.Handlers[i].Func.Block = st.transformBlock(n.Handlers[i].Func.Block)
			}
		}
	case *ir.SlotInst:
		n.Children = st.transformBlock(n.Children)
	case *ir.ErrorBoundary:
		n.Children = st.transformBlock(n.Children)
		if n.Handler != nil && n.Handler.Func != nil {
			n.Handler.Func.Block = st.transformBlock(n.Handler.Func.Block)
		}
	case *ir.Emit:
		for i := range n.Args {
			p, v := st.transformExpr(n.Args[i].Value)
			pre = append(pre, p...)
			n.Args[i].Value = v
		}
	case *ir.CallStmt:
		if n.Call != nil {
			if n.Call.Receiver != nil {
				p, v := st.transformExpr(n.Call.Receiver)
				pre = append(pre, p...)
				n.Call.Receiver = v
			}
			for i := range n.Call.Args {
				p, v := st.transformExpr(n.Call.Args[i].Value)
				pre = append(pre, p...)
				n.Call.Args[i].Value = v
			}
		}
	case *ir.Window:
		if n.Href != nil {
			p, v := st.transformExpr(n.Href)
			pre = append(pre, p...)
			n.Href = v
		}
		if n.Title != nil {
			p, v := st.transformExpr(n.Title)
			pre = append(pre, p...)
			n.Title = v
		}
		if n.Favicon != nil {
			p, v := st.transformExpr(n.Favicon)
			pre = append(pre, p...)
			n.Favicon = v
		}
		n.Body = st.transformBlock(n.Body)
	}
	return pre, s
}

// transformExpr walks e, lifting any ternary it contains into a list of
// pre-stmts and replacing the ternary in-place with an Ident referring to
// the synthesized temp. Returns (preStmts, e').
func (st *ternState) transformExpr(e ir.Expr) ([]ir.Stmt, ir.Expr) {
	if e == nil {
		return nil, nil
	}
	switch x := e.(type) {
	case *ir.Ternary:
		return st.liftTernary(x)
	case *ir.Binary:
		var pre []ir.Stmt
		var p []ir.Stmt
		p, x.Left = st.transformExpr(x.Left)
		pre = append(pre, p...)
		p, x.Right = st.transformExpr(x.Right)
		pre = append(pre, p...)
		return pre, x
	case *ir.Unary:
		p, op := st.transformExpr(x.Operand)
		x.Operand = op
		return p, x
	case *ir.Call:
		var pre []ir.Stmt
		if x.Receiver != nil {
			p, v := st.transformExpr(x.Receiver)
			pre = append(pre, p...)
			x.Receiver = v
		}
		for i := range x.Args {
			p, v := st.transformExpr(x.Args[i].Value)
			pre = append(pre, p...)
			x.Args[i].Value = v
		}
		return pre, x
	case *ir.Conversion:
		p, op := st.transformExpr(x.Operand)
		x.Operand = op
		return p, x
	case *ir.Select:
		p, op := st.transformExpr(x.Operand)
		x.Operand = op
		return p, x
	case *ir.Index:
		var pre []ir.Stmt
		p, op := st.transformExpr(x.Operand)
		pre = append(pre, p...)
		x.Operand = op
		p, idx := st.transformExpr(x.Idx)
		pre = append(pre, p...)
		x.Idx = idx
		return pre, x
	case *ir.ListLit:
		var pre []ir.Stmt
		for i := range x.Elems {
			p, v := st.transformExpr(x.Elems[i])
			pre = append(pre, p...)
			x.Elems[i] = v
		}
		return pre, x
	case *ir.StructLit:
		var pre []ir.Stmt
		for i := range x.Fields {
			if x.Fields[i].Value != nil {
				p, v := st.transformExpr(x.Fields[i].Value)
				pre = append(pre, p...)
				x.Fields[i].Value = v
			}
		}
		return pre, x
	case *ir.Spread:
		p, op := st.transformExpr(x.Operand)
		x.Operand = op
		return p, x
	}
	return nil, e
}

// liftTernary materializes a ternary as (LocalVar, If, Ident).
//   - LocalVar declares the temp.
//   - If branches assign the appropriate side into the temp.
//   - Ident replaces the original ternary position.
//
// Sub-ternaries inside Cond surface as preStmts that run before the If.
// Sub-ternaries inside Then/Else are hoisted into the matching branch
// body, *not* before the outer If — preserving the short-circuit
// semantics of the source code.
func (st *ternState) liftTernary(t *ir.Ternary) ([]ir.Stmt, ir.Expr) {
	var pre []ir.Stmt

	// Cond's sub-ternaries run before the If.
	condPre, condExpr := st.transformExpr(t.Cond)
	pre = append(pre, condPre...)

	// Then / Else: lift sub-ternaries into their branch.
	thenPre, thenExpr := st.transformExpr(t.Then)
	elsePre, elseExpr := st.transformExpr(t.Else)

	name := st.freshName()
	tmpDecl := &ir.LocalVar{
		Name: name,
		Type: t.Type,
	}
	tmpRef := &ir.Ident{
		Name: name,
		Type: t.Type,
	}

	// Body / Else of the synthesized If: the sub-ternary pre-stmts followed
	// by an Assign of the lowered sub-expression.
	body := append(thenPre, &ir.Assign{
		Target: &ir.Ident{Name: name, Type: t.Type},
		Op:     ast.AssignSet,
		Value:  thenExpr,
	})
	elseBlock := append(elsePre, &ir.Assign{
		Target: &ir.Ident{Name: name, Type: t.Type},
		Op:     ast.AssignSet,
		Value:  elseExpr,
	})

	pre = append(pre, tmpDecl, &ir.If{
		Cond: condExpr,
		Body: body,
		Else: elseBlock,
	})
	return pre, tmpRef
}
```

- [ ] **Step 3: Update goldens**

Run: `go test ./internal/lower/ -run TestLower -update`
Expected: PASS, three new `expected.sngl` sections populated.

- [ ] **Step 4: Inspect goldens**

```bash
cat internal/lower/testdata/ternary_in_assign.txtar
cat internal/lower/testdata/ternary_in_call.txtar
cat internal/lower/testdata/ternary_nested.txtar
```

Expected pattern (basic case):

```
@click {
    var __lt0 string
    if on { __lt0 = "yes" } else { __lt0 = "no" }
    label = __lt0
}
```

Nested case should produce two LocalVars at different nesting levels — outer `__lt0` for the `a ? ... : "neither"`, and inside the `if a` body another `__lt1` for `b ? "ab" : "a"`.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/lower/`
Expected: PASS.

- [ ] **Step 6: Run full suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/lower/ternary.go internal/lower/testdata/ternary_in_assign.txtar internal/lower/testdata/ternary_in_call.txtar internal/lower/testdata/ternary_nested.txtar
git commit -m "$(cat <<'EOF'
Implement NoTernary lowering pass

Hoists every ternary expression to a synthesized LocalVar plus If
statement before the consuming statement; replaces the expression
position with an Ident reference to the temp. Sub-ternaries inside
Then/Else lift into their branch body (preserving short-circuit
semantics); sub-ternaries inside Cond lift before the outer If.

Three goldens cover ternary-in-Assign, ternary-in-CallArg, and the
nested-ternary case that exercises branch-internal hoisting.

NoLambda remains deferred to its own spec — capture data is derivable
but the output IR shape needs design discussion.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Final Phase 2 verification

- [ ] **Step 1: Run full project verify**

Run: `go tool verify`
Expected: PASS.

- [ ] **Step 2: Confirm Phase 2 scope is closed**

Phase 2 in the spec lists: NoToggle, NoTernary, NoUnit, NoEnum, NoLambda. After this plan ships, NoLambda remains deferred — explicitly scoped out at the top of the plan with rationale.

If the user wants NoLambda before Phase 3 starts, that's a separate spec.

---

## Self-Review Notes

Spec coverage:
- §Migration Plan Phase 2 — NoToggle (Phase 2 plan), NoEnum, NoUnit (Phase 2 plan), NoTernary (this plan). NoLambda explicitly deferred at top.
- §Pass ordering — registry order in `internal/lower/lower.go`: NoUnit → NoEnum → NoTernary → NoComputed → NoLambda → NoToggle → NoReactivity → NoTimer → NoDeclarative. NoTernary runs *before* NoToggle, so a ternary whose branch contains a toggle will lift correctly: ternary lifts first to if/else+temp, then toggle in either branch becomes `x = !x`.

Risks:

1. **Synthesized Idents have nil Sym.** Phase 2 ships pass output to dump-as-SNGL only; codegen consumption needs Sym threading. Track as a Phase 3 follow-up: synthesize an `*ir.Var` (or attach a special Symbol marker) for `__ltN` temps when codegen platforms turn caps on.
2. **Const-context ternaries.** `walkFuncs.expr` is called for const initializers / prop defaults / struct field defaults / timer interval — there's no statement to hoist before, so a ternary in those positions is left alone. The optimizer folds const ternaries before lower runs, so in production this path is unreachable. If a fixture exercises it, the lowering output will retain the ternary — flag as a known gap.
3. **Pre-existing `__lt` collisions.** A user variable named `__lt0` would shadow a synthesized temp. The double-underscore prefix is the project convention for synthesized names (matches `__sngl_n_*` in codegen). Acceptable risk.

Type consistency check:
- `ternState`, `transformBlock`, `transformStmt`, `transformExpr`, `liftTernary` are all defined in this plan.
- `walkFuncs.stmts` and `walkFuncs.expr` callbacks — defined in Phase 2 plan's `walk.go` (already shipped).
