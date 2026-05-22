# Closure / Function-Variable Points-To Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Track per-funcvar candidate sets via Andersen-style points-to so SNGL color analysis flows through funcvars; auto-promote storage slots that may receive an async candidate to async (g3) so `await` is emitted at every funcvar call that may resolve to async, without the user ever seeing colors.

**Architecture:** New IR types (`Color`, `PointsToKey`, `PointsToInfo`) plus a new field `Color` on `FuncSig`. New checker pass `analyzePointsTo` runs after `analyzeAsync`, walks the IR collecting subset constraints (`pts ⊇ pts`), iterates to fixpoint, then computes slot colors. Color propagation re-runs to account for funcvar calls. JS codegen consults the points-to registry at funcvar call sites and prepends `await` for async slots.

**Tech Stack:** Go (compiler), the existing `internal/checker/` and `codegen/lang/javascript/` packages, txtar fixtures.

**Spec:** [`docs/superpowers/specs/2026-05-05-closure-points-to-design.md`](../specs/2026-05-05-closure-points-to-design.md)

**Sub-task of:** [#39 Concurrency model](https://git.duckfam.us/jonathan/sngl/-/issues/39); tracked as [#47](https://git.duckfam.us/jonathan/sngl/-/issues/47). Builds on sub-task A (commit range `main..feature/js-async-completion`).

---

## File Structure

**Create:**
- `ir/color.go` — `Color` enum + helpers (`Sync`, `Async`, with poly representation).
- `ir/pointsto.go` — `PointsToKey`, `SlotKind`, `PointsToInfo` types and helpers.
- `internal/checker/pointsto.go` — `analyzePointsTo` pass.
- `internal/checker/pointsto_test.go` — unit tests for the pass.
- `cmd/sngl/testdata/funcvar_stored_async.txt`
- `cmd/sngl/testdata/funcvar_mixed_promotes.txt`
- `cmd/sngl/testdata/funcvar_struct_field_async.txt`
- `cmd/sngl/testdata/funcvar_passthrough_no_promote.txt`
- `codegen/platform/html/funcvar_browser_test.go`

**Modify:**
- `ir/types.go` — add `Color` and `PolyParam` fields to `FuncSig`.
- `ir/ir.go` — add `PointsTo *PointsToInfo` field to `Package`.
- `internal/checker/checker.go` (or wherever the pass pipeline lives) — call `analyzePointsTo` between `analyzeAsync` and `checkAsyncRules`. Re-run async propagation in a new `analyzeAsyncWithPointsTo`.
- `internal/checker/async.go` — add `analyzeAsyncWithPointsTo` that consumes points-to, OR factor `analyzeAsync` to take a "funcvar-aware" hook.
- `codegen/lang/javascript/translate_ir.go` — extend the call translator to await on funcvar slots whose color is `Async`.

---

## Conventions used in this plan

- Working directory: `/home/jonathan/src/git.duckfam.us/jonathan/sngl/.worktrees/closure-points-to`
- Single test: `go test -run TestName ./pkg/`
- Full suite: `go tool verify`
- Build CLI: `go install ./cmd/sngl`
- Commit after each task; never amend.

---

### Task 1: Add `Color` type and `FuncSig` fields

**Files:**
- Create: `ir/color.go`
- Modify: `ir/types.go`
- Test: `ir/color_test.go`

- [ ] **Step 1: Write the failing test**

```go
// ir/color_test.go
package ir_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestColor_String(t *testing.T) {
	cases := []struct {
		c    ir.Color
		want string
	}{
		{ir.ColorSync, "Sync"},
		{ir.ColorAsync, "Async"},
		{ir.ColorParam, "Param"},
	}
	for _, tc := range cases {
		if tc.c.String() != tc.want {
			t.Fatalf("Color(%d).String() = %q want %q", tc.c, tc.c.String(), tc.want)
		}
	}
}

func TestFuncSig_DefaultsToSync(t *testing.T) {
	s := &ir.FuncSig{}
	if s.Color != ir.ColorSync {
		t.Fatalf("zero-value FuncSig.Color = %v, want ColorSync", s.Color)
	}
	if s.PolyParam != -1 {
		t.Fatalf("zero-value FuncSig.PolyParam = %d, want -1", s.PolyParam)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestColor_String -run TestFuncSig_DefaultsToSync ./ir/`
Expected: FAIL — `Color`, `ColorSync`, `ColorAsync`, `ColorParam` undefined; `FuncSig.Color`, `FuncSig.PolyParam` undefined.

- [ ] **Step 3: Add `Color` enum**

Create `ir/color.go`:

```go
package ir

// Color tracks the synchronous-vs-asynchronous flavor of a function or
// function-typed slot. Concrete values are Sync or Async. Param indicates
// the color depends on the color of a funcvar parameter; the parameter
// index is held alongside (e.g. on FuncSig.PolyParam) and resolved per
// call site by color monomorphization (sub-task C / #48).
type Color int

const (
	ColorSync Color = iota
	ColorAsync
	ColorParam
)

func (c Color) String() string {
	switch c {
	case ColorSync:
		return "Sync"
	case ColorAsync:
		return "Async"
	case ColorParam:
		return "Param"
	default:
		return "ColorUnknown"
	}
}
```

- [ ] **Step 4: Add fields to `FuncSig`**

Edit `ir/types.go`. Find the `FuncSig` struct (~line 365) and extend:

```go
type FuncSig struct {
	Params     []*Param
	Return     *Type
	TypeParams []string
	Purity     Purity
	Color      Color // Sync (default), Async, or Param.
	PolyParam  int   // when Color == ColorParam: index of the funcvar param the color depends on; -1 otherwise.
}
```

Update every existing `&FuncSig{...}` literal in `ir/`, `internal/checker/`, and `internal/lower/` to set `PolyParam: -1` so the zero-value test passes. Use grep:

```
grep -rln '\&FuncSig{' ir/ internal/checker/ internal/lower/ codegen/
```

For each match, append `PolyParam: -1` (and explicitly `Color: ColorSync` if you prefer, though the zero value matches).

A simpler alternative: keep PolyParam as `int` with zero default `0`, but reserve `-1` as "no poly index" in the helper. Adjust the test to expect `0` and add a `IsPoly()` predicate that requires `Color == ColorParam`. Pick whichever is cleaner — but be consistent. The plan below assumes the explicit `-1` convention.

If existing struct literals set fields in declaration order, ensure the trailing fields (`Color`, `PolyParam`) are kept named-only to avoid silent drift. Run `go vet ./...` after the edit and fix any "unkeyed" warnings.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test -run TestColor_String ./ir/ && go test -run TestFuncSig_DefaultsToSync ./ir/`
Expected: PASS.

- [ ] **Step 6: Verify nothing else broke**

Run: `go build ./...`
Expected: clean.

Run: `go tool verify`
Expected: green. Existing tests should not depend on `FuncSig.Color`/`PolyParam`; the new fields default to `ColorSync` / `-1`.

- [ ] **Step 7: Commit**

```bash
git add ir/color.go ir/color_test.go ir/types.go
# plus any files where FuncSig literals were updated
git commit -m "ir: add Color enum and FuncSig.Color/PolyParam fields"
```

---

### Task 2: Add `PointsToInfo` types

**Files:**
- Create: `ir/pointsto.go`
- Create: `ir/pointsto_test.go`
- Modify: `ir/ir.go` (add `PointsTo *PointsToInfo` to `Package`).

- [ ] **Step 1: Write the failing test**

```go
// ir/pointsto_test.go
package ir_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

func TestPointsToKey_VarAndField(t *testing.T) {
	v := &ir.Var{Name: "h"}
	k1 := ir.SlotVarKey(v)
	if k1.Kind != ir.SlotVar || k1.Var != v {
		t.Fatalf("SlotVarKey(v) malformed: %+v", k1)
	}
	sd := &ir.StructDef{Name: "Handler"}
	sty := &ir.Type{Kind: ir.TypeStruct, Decl: sd}
	k2 := ir.SlotFieldKey(sty, "onClick")
	if k2.Kind != ir.SlotField || k2.Type != sty || k2.Field != "onClick" {
		t.Fatalf("SlotFieldKey malformed: %+v", k2)
	}
}

func TestPointsToInfo_AddCandidate(t *testing.T) {
	info := ir.NewPointsToInfo()
	v := &ir.Var{Name: "h"}
	fn := &ir.Func{Name: "syncFn"}
	key := ir.SlotVarKey(v)
	info.AddCandidate(key, fn)
	if got := info.Candidates(key); len(got) != 1 || got[0] != fn {
		t.Fatalf("expected 1 candidate, got %v", got)
	}
	// dedup
	info.AddCandidate(key, fn)
	if got := info.Candidates(key); len(got) != 1 {
		t.Fatalf("expected dedup; got %d", len(got))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestPointsToKey_VarAndField -run TestPointsToInfo_AddCandidate ./ir/`
Expected: FAIL — types undefined.

- [ ] **Step 3: Implement `ir/pointsto.go`**

```go
package ir

// SlotKind identifies the kind of points-to slot a key refers to.
type SlotKind int

const (
	SlotVar      SlotKind = iota // top-level var or local holding a funcvar
	SlotParam                    // function parameter of funcvar type
	SlotField                    // struct field of funcvar type, field-insensitive across instances
	SlotListElem                 // element of a list of funcvar type
	SlotReturn                   // function return slot when result is a funcvar
)

// PointsToKey identifies a single slot in the points-to graph.
//
// The struct is value-comparable: equal keys mean the same slot. For Var,
// Param, and Return slots, identity comes from a *Var/*Func pointer (heap-
// stable). For Field and ListElem, identity is the (Type, fieldName) pair —
// keyed on the *Type pointer for the declaring struct/list type.
type PointsToKey struct {
	Kind  SlotKind
	Var   *Var   // SlotVar
	Param *Param // SlotParam (canonical *Param pointer)
	Func  *Func  // SlotReturn
	Type  *Type  // SlotField, SlotListElem
	Field string // SlotField
}

func SlotVarKey(v *Var) PointsToKey     { return PointsToKey{Kind: SlotVar, Var: v} }
func SlotParamKey(p *Param) PointsToKey { return PointsToKey{Kind: SlotParam, Param: p} }
func SlotReturnKey(f *Func) PointsToKey { return PointsToKey{Kind: SlotReturn, Func: f} }
func SlotFieldKey(t *Type, name string) PointsToKey {
	return PointsToKey{Kind: SlotField, Type: t, Field: name}
}
func SlotListElemKey(t *Type) PointsToKey { return PointsToKey{Kind: SlotListElem, Type: t} }

// PointsToInfo is the analysis result attached to a Package.
type PointsToInfo struct {
	Sites     map[PointsToKey][]*Func
	SlotColor map[PointsToKey]Color
}

func NewPointsToInfo() *PointsToInfo {
	return &PointsToInfo{
		Sites:     map[PointsToKey][]*Func{},
		SlotColor: map[PointsToKey]Color{},
	}
}

// AddCandidate inserts fn into the candidate set for key, deduping.
// Returns true if a new candidate was added.
func (p *PointsToInfo) AddCandidate(key PointsToKey, fn *Func) bool {
	for _, existing := range p.Sites[key] {
		if existing == fn {
			return false
		}
	}
	p.Sites[key] = append(p.Sites[key], fn)
	return true
}

// Candidates returns the candidate set for key (nil if none).
func (p *PointsToInfo) Candidates(key PointsToKey) []*Func {
	return p.Sites[key]
}
```

- [ ] **Step 4: Add `PointsTo` field to `Package`**

Edit `ir/ir.go`. Find the `Package` struct (~line 95 area). Add:

```go
type Package struct {
	// ... existing fields ...

	// PointsTo is set by the checker's analyzePointsTo pass when funcvar
	// analysis runs; nil before. Codegen and downstream passes consult
	// this to decide await placement at funcvar call sites.
	PointsTo *PointsToInfo
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./ir/...`
Expected: PASS.

- [ ] **Step 6: Build everything**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add ir/pointsto.go ir/pointsto_test.go ir/ir.go
git commit -m "ir: add PointsToInfo, PointsToKey, SlotKind"
```

---

### Task 3: Constraint walker

**Files:**
- Create: `internal/checker/pointsto.go`
- Create: `internal/checker/pointsto_test.go`

This task implements the constraint *collection* walk. The fixpoint and color computation come in Tasks 4 and 5.

The walker visits every statement and expression in the package's funcs (top-level, component-scoped, window-scoped) and emits subset constraints into a list. A constraint is a pair `(dstKey, src)` where `src` is either a `PointsToKey` (subset of another slot's pts) or a `*Func` (single concrete candidate added directly).

- [ ] **Step 1: Write the failing test for `collectConstraints`**

```go
// internal/checker/pointsto_test.go
package checker

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

// directAssignment: var v = syncFn → constraint (SlotVarKey(v), {syncFn})
func TestCollectConstraints_DirectAssign(t *testing.T) {
	syncFn := &ir.Func{Name: "syncFn"}
	v := &ir.Var{
		Name: "v",
		Type: funcType(), // helper: returns &Type{Kind: TypeFunc, Sig: ...}
		Init: &ir.Ident{Name: "syncFn", Sym: syncFn},
	}
	pkg := &ir.Package{Vars: []*ir.Var{v}, Funcs: []*ir.Func{syncFn}}

	cs := collectConstraints(pkg)
	want := constraint{dst: ir.SlotVarKey(v), funcs: []*ir.Func{syncFn}}
	if len(cs) != 1 || !equalConstraint(cs[0], want) {
		t.Fatalf("got %+v, want [%+v]", cs, want)
	}
}
```

The test references three helpers (`funcType`, `equalConstraint`, plus the `constraint` struct) that you will define in `pointsto.go`. Provide them. Do not gold-plate — minimal shapes only.

Add additional cases (separate test functions) for:
- `TestCollectConstraints_FuncvarParamPassthrough`: `g(syncFn)` where `g`'s param is funcvar → constraint `(SlotParamKey(g.Params[0]), {syncFn})`.
- `TestCollectConstraints_AssignFuncvar`: `var v: () -> int = syncFn; var w = v` → constraints for both.
- `TestCollectConstraints_StructField`: assignment to `s.onClick` → `SlotFieldKey(StructType, "onClick")`.
- `TestCollectConstraints_ListElem`: `var hs = [syncFn, asyncFn]` → `SlotListElemKey(ListType)` accumulates both candidates.
- `TestCollectConstraints_ReturnFuncvar`: `fn pick(): () -> int => asyncFn` → `SlotReturnKey(pick)` candidate `asyncFn`.

The exact IR shapes for these are:
- `*ir.Var` for vars; `*ir.LocalVar` for locals; `*ir.Assign` for stores.
- `*ir.Call` with funcvar args.
- `*ir.Select` for `s.field` reads; `*ir.Assign{Target: *ir.Select}` for stores.
- `*ir.Index` for list element reads; `*ir.Assign{Target: *ir.Index}` for stores.
- `*ir.ListLit` for list literals.
- `*ir.Return` for return statements.
- `*ir.Lambda` for inline lambdas (treat each as a fresh `*ir.Func` candidate — read `ir/expr.go:Lambda` to see the embedded `Func`).

If any IR shape diverges from this, read the actual definitions in `ir/ir.go` and `ir/expr.go` and update the tests + walker accordingly. Don't invent.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run TestCollectConstraints ./internal/checker/`
Expected: FAIL — `collectConstraints` undefined.

- [ ] **Step 3: Implement the walker**

Create `internal/checker/pointsto.go`:

```go
package checker

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

// constraint is a single subset rule: pts(dst) ⊇ {funcs...} ∪ pts(srcs...).
type constraint struct {
	dst   ir.PointsToKey
	funcs []*ir.Func       // direct candidates
	srcs  []ir.PointsToKey // pts(src) ⊆ pts(dst) for each
}

// collectConstraints walks the IR and emits subset constraints capturing
// every funcvar flow site. The resulting list is consumed by the
// fixpoint solver in Task 4.
func collectConstraints(pkg *ir.Package) []constraint {
	var out []constraint
	walker := &pointsToWalker{out: &out}
	walker.walkPackage(pkg)
	return out
}

type pointsToWalker struct {
	out *[]constraint
	// enclosing function context for SlotReturn keys
	fn *ir.Func
}

func (w *pointsToWalker) walkPackage(pkg *ir.Package) {
	for _, v := range pkg.Vars {
		w.walkVarInit(v)
	}
	for _, fn := range pkg.Funcs {
		w.walkFunc(fn)
	}
	// Components and Windows: walk their Vars + Funcs the same way.
	for _, c := range pkg.Components {
		for _, v := range c.Vars {
			w.walkVarInit(v)
		}
		for _, fn := range c.Funcs {
			w.walkFunc(fn)
		}
	}
	// ... and Windows ...
}

func (w *pointsToWalker) walkVarInit(v *ir.Var) {
	if !isFuncType(v.Type) || v.Init == nil {
		return
	}
	dst := ir.SlotVarKey(v)
	w.bindRHS(dst, v.Init)
}

func (w *pointsToWalker) walkFunc(fn *ir.Func) {
	prev := w.fn
	w.fn = fn
	defer func() { w.fn = prev }()
	w.walkStmts(fn.Block)
}

func (w *pointsToWalker) walkStmts(stmts []ir.Stmt) {
	for _, s := range stmts {
		w.walkStmt(s)
	}
}

func (w *pointsToWalker) walkStmt(s ir.Stmt) {
	switch x := s.(type) {
	case *ir.LocalVar:
		if isFuncType(x.Type) && x.Init != nil {
			w.bindRHS(localVarSlotKey(x), x.Init)
		}
		w.walkExpr(x.Init)
	case *ir.Assign:
		w.walkAssign(x)
	case *ir.Return:
		if w.fn != nil && w.fn.Sig != nil && isFuncType(w.fn.Sig.Return) && x.Value != nil {
			w.bindRHS(ir.SlotReturnKey(w.fn), x.Value)
		}
		w.walkExpr(x.Value)
	case *ir.If:
		w.walkExpr(x.Cond)
		w.walkStmts(x.Body)
		w.walkStmts(x.Else)
	case *ir.For:
		w.walkExpr(x.Iter)
		w.walkStmts(x.Body)
		w.walkStmts(x.Else)
	case *ir.PlatformFilter:
		w.walkStmts(x.Body)
	case *ir.CallStmt:
		w.walkExpr(x.Call)
	}
}

// walkAssign handles every assignment target shape that may bind a funcvar.
func (w *pointsToWalker) walkAssign(a *ir.Assign) {
	if a.Value == nil {
		return
	}
	if !exprIsFuncTyped(a.Value) {
		w.walkExpr(a.Value)
		return
	}
	dst, ok := slotKeyForAssignTarget(a.Target)
	if !ok {
		// Unknown target shape — conservative: skip rather than guess.
		w.walkExpr(a.Value)
		return
	}
	w.bindRHS(dst, a.Value)
	w.walkExpr(a.Value)
}

func (w *pointsToWalker) walkExpr(e ir.Expr) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ir.Call:
		w.walkCall(x)
	case *ir.Binary:
		w.walkExpr(x.Left)
		w.walkExpr(x.Right)
	case *ir.Unary:
		w.walkExpr(x.Operand)
	case *ir.Ternary:
		w.walkExpr(x.Cond)
		w.walkExpr(x.Then)
		w.walkExpr(x.Else)
	case *ir.Conversion:
		w.walkExpr(x.Operand)
	case *ir.Select:
		w.walkExpr(x.Operand)
	case *ir.Index:
		w.walkExpr(x.Operand)
		w.walkExpr(x.Idx)
	case *ir.ListLit:
		elemKey, ok := slotListElemKeyForListType(x.Type)
		for _, el := range x.Elems {
			if ok && exprIsFuncTyped(el) {
				w.bindRHS(elemKey, el)
			}
			w.walkExpr(el)
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			// f.Field is the field name; f.Value is the assigned expr.
			// Find the StructDef for x.Type and key SlotFieldKey accordingly.
			if exprIsFuncTyped(f.Value) {
				if k, ok := structFieldKey(x.Type, f.Field); ok {
					w.bindRHS(k, f.Value)
				}
			}
			w.walkExpr(f.Value)
		}
	case *ir.Spread:
		w.walkExpr(x.Operand)
	case *ir.Lambda:
		// A lambda escaping into a funcvar slot is a candidate; the binding
		// is recorded by the enclosing assignment / arg site, not here.
	case *ir.Closure:
		// Same as Lambda.
	}
}

// walkCall: for each funcvar arg, emit pts(param-slot) ⊇ {arg-funcs} | pts(arg-slot).
func (w *pointsToWalker) walkCall(c *ir.Call) {
	if c.Func != nil {
		for i, a := range c.Args {
			if i >= len(c.Func.Params) {
				break
			}
			p := c.Func.Params[i]
			if !isFuncType(p.Type) {
				continue
			}
			w.bindRHS(ir.SlotParamKey(p), a.Value)
		}
	}
	// Walk receiver and arg subexpressions for nested calls.
	w.walkExpr(c.Receiver)
	for _, a := range c.Args {
		w.walkExpr(a.Value)
	}
}

// bindRHS emits a constraint binding pts(dst) ⊇ candidates(rhs).
//
// rhs may be:
//   - *ir.Ident referring to a *ir.Func: direct concrete candidate.
//   - *ir.Ident referring to a *ir.Var: pts subset of that var's slot.
//   - *ir.Ident referring to a *ir.Param: pts subset of that param's slot.
//   - *ir.Lambda: candidate is the lambda's lifted Func (see lambda.Func).
//   - *ir.Select / *ir.Index resolving to a funcvar slot: pts subset.
//   - *ir.Call returning funcvar: pts subset of the callee's return slot.
//   - other shapes: skip (conservative).
func (w *pointsToWalker) bindRHS(dst ir.PointsToKey, rhs ir.Expr) {
	switch x := rhs.(type) {
	case *ir.Ident:
		if fn, ok := x.Sym.(*ir.Func); ok {
			*w.out = append(*w.out, constraint{dst: dst, funcs: []*ir.Func{fn}})
			return
		}
		if v, ok := x.Sym.(*ir.Var); ok {
			*w.out = append(*w.out, constraint{dst: dst, srcs: []ir.PointsToKey{ir.SlotVarKey(v)}})
			return
		}
		if p, ok := x.Sym.(*ir.Param); ok {
			*w.out = append(*w.out, constraint{dst: dst, srcs: []ir.PointsToKey{ir.SlotParamKey(p)}})
			return
		}
	case *ir.Lambda:
		if x.Func != nil {
			*w.out = append(*w.out, constraint{dst: dst, funcs: []*ir.Func{x.Func}})
		}
	case *ir.Closure:
		if x.Func != nil {
			*w.out = append(*w.out, constraint{dst: dst, funcs: []*ir.Func{x.Func}})
		}
	case *ir.Call:
		if x.Func != nil {
			*w.out = append(*w.out, constraint{dst: dst, srcs: []ir.PointsToKey{ir.SlotReturnKey(x.Func)}})
		}
	case *ir.Select:
		// Reading a struct field: subset of the field slot.
		if x.Operand != nil {
			t := exprType(x.Operand)
			if k, ok := structFieldKey(t, x.Name); ok {
				*w.out = append(*w.out, constraint{dst: dst, srcs: []ir.PointsToKey{k}})
			}
		}
	case *ir.Index:
		// Reading a list element: subset of the elem slot.
		if x.Operand != nil {
			if k, ok := slotListElemKeyForListType(exprType(x.Operand)); ok {
				*w.out = append(*w.out, constraint{dst: dst, srcs: []ir.PointsToKey{k}})
			}
		}
	}
	// Other shapes ignored.
}
```

Helpers:

```go
func isFuncType(t *ir.Type) bool {
	return t != nil && t.Kind == ir.TypeFunc
}

func exprType(e ir.Expr) *ir.Type {
	if e == nil {
		return nil
	}
	return e.ExprType()
}

func exprIsFuncTyped(e ir.Expr) bool {
	return isFuncType(exprType(e))
}

func slotKeyForAssignTarget(t ir.Expr) (ir.PointsToKey, bool) {
	switch x := t.(type) {
	case *ir.Ident:
		if v, ok := x.Sym.(*ir.Var); ok {
			return ir.SlotVarKey(v), true
		}
		if p, ok := x.Sym.(*ir.Param); ok {
			return ir.SlotParamKey(p), true
		}
	case *ir.Select:
		if k, ok := structFieldKey(exprType(x.Operand), x.Name); ok {
			return k, true
		}
	case *ir.Index:
		if k, ok := slotListElemKeyForListType(exprType(x.Operand)); ok {
			return k, true
		}
	}
	return ir.PointsToKey{}, false
}

func structFieldKey(t *ir.Type, name string) (ir.PointsToKey, bool) {
	if t == nil || t.Kind != ir.TypeStruct {
		return ir.PointsToKey{}, false
	}
	return ir.SlotFieldKey(t, name), true
}

func slotListElemKeyForListType(t *ir.Type) (ir.PointsToKey, bool) {
	if t == nil || t.Kind != ir.TypeList {
		return ir.PointsToKey{}, false
	}
	return ir.SlotListElemKey(t), true
}

// localVarSlotKey: a LocalVar maps to a Var-equivalent slot. If LocalVar
// already wraps a *ir.Var (read ir/stmt.go to confirm), use that pointer
// directly. Otherwise treat the LocalVar pointer itself as the identity.
func localVarSlotKey(lv *ir.LocalVar) ir.PointsToKey {
	// If LocalVar has a Var field: return SlotVarKey(lv.Var).
	// Otherwise wrap the LocalVar in a synthetic Var-like key. Read the
	// source first; do not invent.
	panic("inspect ir/stmt.go and complete this helper")
}
```

The `localVarSlotKey` helper has a deliberate panic; resolve it by reading `ir/stmt.go` to find the actual `LocalVar` shape. If `LocalVar` does not embed a `*Var`, either (a) extend `LocalVar` to do so, or (b) introduce a new `SlotLocal` kind keyed on `*ir.LocalVar`. Pick the minimal change consistent with how the rest of the codebase handles local vars.

- [ ] **Step 4: Run tests**

Run: `go test -run TestCollectConstraints ./internal/checker/`
Expected: PASS for all six sub-tests.

- [ ] **Step 5: `go tool verify`**

Run: `go tool verify`
Expected: green.

- [ ] **Step 6: Commit**

```bash
git add internal/checker/pointsto.go internal/checker/pointsto_test.go
# plus ir/stmt.go if you extended LocalVar
git commit -m "checker: collect funcvar points-to constraints"
```

---

### Task 4: Fixpoint solver and slot color

**Files:**
- Modify: `internal/checker/pointsto.go`
- Modify: `internal/checker/pointsto_test.go`

- [ ] **Step 1: Write failing tests for the solver**

```go
func TestPointsTo_DirectStore_SyncSlot(t *testing.T) {
	syncFn := &ir.Func{Name: "syncFn", Sig: &ir.FuncSig{Color: ir.ColorSync, PolyParam: -1}}
	v := &ir.Var{Name: "v", Type: funcType(), Init: identTo(syncFn)}
	pkg := &ir.Package{Vars: []*ir.Var{v}, Funcs: []*ir.Func{syncFn}}

	info := analyzePointsTo(pkg)
	key := ir.SlotVarKey(v)
	if info.SlotColor[key] != ir.ColorSync {
		t.Fatalf("want SlotColor Sync, got %v", info.SlotColor[key])
	}
	if got := info.Candidates(key); len(got) != 1 || got[0] != syncFn {
		t.Fatalf("want [syncFn], got %+v", got)
	}
}

func TestPointsTo_DirectStore_AsyncSlot(t *testing.T) {
	asyncFn := &ir.Func{Name: "asyncFn", IsAsync: true, Sig: &ir.FuncSig{Color: ir.ColorAsync, PolyParam: -1}}
	v := &ir.Var{Name: "v", Type: funcType(), Init: identTo(asyncFn)}
	pkg := &ir.Package{Vars: []*ir.Var{v}, Funcs: []*ir.Func{asyncFn}}

	info := analyzePointsTo(pkg)
	if info.SlotColor[ir.SlotVarKey(v)] != ir.ColorAsync {
		t.Fatalf("want SlotColor Async")
	}
}

func TestPointsTo_MixedStore_PromotesAsync(t *testing.T) {
	syncFn := &ir.Func{Name: "syncFn"}
	asyncFn := &ir.Func{Name: "asyncFn", IsAsync: true}
	v := &ir.Var{Name: "v", Type: funcType()}
	// Two assignments inside a func body: v = syncFn ; v = asyncFn
	fn := &ir.Func{
		Name: "main",
		Block: []ir.Stmt{
			&ir.Assign{Target: identTo(v), Value: identTo(syncFn)},
			&ir.Assign{Target: identTo(v), Value: identTo(asyncFn)},
		},
		Sig: &ir.FuncSig{PolyParam: -1},
	}
	pkg := &ir.Package{Vars: []*ir.Var{v}, Funcs: []*ir.Func{fn, syncFn, asyncFn}}

	info := analyzePointsTo(pkg)
	if info.SlotColor[ir.SlotVarKey(v)] != ir.ColorAsync {
		t.Fatalf("mixed candidates should promote to Async; got %v", info.SlotColor[ir.SlotVarKey(v)])
	}
	if len(info.Candidates(ir.SlotVarKey(v))) != 2 {
		t.Fatalf("expected 2 candidates")
	}
}

func TestPointsTo_TransitiveSubset(t *testing.T) {
	asyncFn := &ir.Func{Name: "asyncFn", IsAsync: true}
	v := &ir.Var{Name: "v", Type: funcType(), Init: identTo(asyncFn)}
	w := &ir.Var{Name: "w", Type: funcType(), Init: identTo(v)} // w = v
	pkg := &ir.Package{Vars: []*ir.Var{v, w}, Funcs: []*ir.Func{asyncFn}}

	info := analyzePointsTo(pkg)
	if info.SlotColor[ir.SlotVarKey(w)] != ir.ColorAsync {
		t.Fatalf("transitive flow should color w Async")
	}
}
```

`identTo` is a tiny helper that builds an `*ir.Ident` referring to a func/var symbol. Add it to the test file.

- [ ] **Step 2: Run — FAIL**

Run: `go test -run TestPointsTo_ ./internal/checker/`
Expected: FAIL — `analyzePointsTo` undefined.

- [ ] **Step 3: Implement the solver**

Append to `internal/checker/pointsto.go`:

```go
// analyzePointsTo runs the constraint walker, solves to fixpoint, and
// computes per-slot colors. Mutates pkg.PointsTo in place and returns it.
func analyzePointsTo(pkg *ir.Package) *ir.PointsToInfo {
	info := ir.NewPointsToInfo()
	pkg.PointsTo = info

	cs := collectConstraints(pkg)

	for {
		changed := false
		for _, c := range cs {
			for _, fn := range c.funcs {
				if info.AddCandidate(c.dst, fn) {
					changed = true
				}
			}
			for _, src := range c.srcs {
				for _, fn := range info.Candidates(src) {
					if info.AddCandidate(c.dst, fn) {
						changed = true
					}
				}
			}
		}
		if !changed {
			break
		}
	}

	// Compute slot color for storage slots only.
	for key, fns := range info.Sites {
		switch key.Kind {
		case ir.SlotVar, ir.SlotField, ir.SlotListElem:
			color := ir.ColorSync
			for _, fn := range fns {
				if fn.IsAsync || (fn.Sig != nil && fn.Sig.Color == ir.ColorAsync) {
					color = ir.ColorAsync
					break
				}
			}
			info.SlotColor[key] = color
		}
		// SlotParam, SlotReturn — leave SlotColor unset; consumers treat
		// missing entries as "must consult Candidates" (g3 fallback).
	}

	return info
}
```

- [ ] **Step 4: Run — PASS all four tests**

Run: `go test -run TestPointsTo_ ./internal/checker/`
Expected: PASS.

- [ ] **Step 5: `go tool verify`**

Run: `go tool verify`
Expected: green.

- [ ] **Step 6: Commit**

```bash
git add internal/checker/pointsto.go internal/checker/pointsto_test.go
git commit -m "checker: solve points-to constraints and compute slot color"
```

---

### Task 5: Wire pass into the checker pipeline

**Files:**
- Modify: `internal/checker/checker.go` (or wherever the pipeline lives — search for where `analyzeAsync` is called).
- Modify: `internal/checker/async.go` — new function `analyzeAsyncWithPointsTo`.
- Test: `internal/checker/pointsto_test.go`

- [ ] **Step 1: Find the pipeline invocation**

Search:

```
grep -n "analyzeAsync\|checkAsyncRules" internal/checker/*.go
```

Identify the function that calls `c.analyzeAsync()` (likely `Check()` or a helper). Confirm the order is:
1. `c.analyzeAsync()`
2. `c.checkAsyncRules()` — added in sub-task A.

- [ ] **Step 2: Write the failing test**

```go
// internal/checker/pointsto_test.go (extend)
func TestAnalyzeAsyncWithPointsTo_FuncvarCallColorsCaller(t *testing.T) {
	asyncFn := &ir.Func{Name: "asyncFn", IsAsync: true}
	v := &ir.Var{Name: "v", Type: funcType(), Init: identTo(asyncFn)}

	// fn caller() { v() }
	caller := &ir.Func{
		Name:  "caller",
		Block: []ir.Stmt{&ir.CallStmt{Call: &ir.Call{Receiver: identTo(v)}}},
		Sig:   &ir.FuncSig{PolyParam: -1},
	}
	pkg := &ir.Package{Vars: []*ir.Var{v}, Funcs: []*ir.Func{asyncFn, caller}}

	// Run the full async pipeline (analyzeAsync, then analyzePointsTo,
	// then analyzeAsyncWithPointsTo).
	runAsyncPipeline(t, pkg)

	if !caller.IsAsync {
		t.Fatalf("caller should be Async after points-to refinement; got IsAsync=false")
	}
}
```

`runAsyncPipeline` is a small helper that builds a `*checker` and invokes the methods directly without going through `Check()`. Define in the test file.

- [ ] **Step 3: Run — FAIL**

Run: `go test -run TestAnalyzeAsyncWithPointsTo ./internal/checker/`
Expected: FAIL — `analyzeAsyncWithPointsTo` undefined or caller not colored.

- [ ] **Step 4: Implement `analyzeAsyncWithPointsTo`**

Add to `internal/checker/async.go`:

```go
// analyzeAsyncWithPointsTo extends analyzeAsync to color funcs that call
// funcvars whose slot color is Async. Runs after analyzePointsTo has
// populated pkg.PointsTo.
func (c *checker) analyzeAsyncWithPointsTo() {
	pkg := c.pkg
	if pkg == nil || pkg.PointsTo == nil {
		return
	}
	funcs := allFuncs(pkg)
	for {
		changed := false
		for _, fn := range funcs {
			if fn.IsAsync {
				continue
			}
			if blockHasFuncvarAsyncCall(fn.Block, pkg.PointsTo) {
				fn.IsAsync = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}
}

// blockHasFuncvarAsyncCall walks stmts looking for a Call whose Func is
// nil but whose Receiver resolves to a points-to slot of color Async, OR
// whose Func is async via the existing predicate.
func blockHasFuncvarAsyncCall(stmts []ir.Stmt, pts *ir.PointsToInfo) bool {
	// Reuse ir.BlockHasAsyncCall for the direct case, and add a
	// funcvar-aware visit. Implement as a parallel walker mirroring
	// ir/async.go but with the funcvar branch.
	// ... inline a small walker similar to ir.BlockHasAsyncCall ...
}
```

Implement the walker so it returns true for either:
- Direct async call (mirrors `ir.BlockHasAsyncCall`).
- A `*ir.Call` whose `Func == nil` (or whose Func is a funcvar reference) where the receiver expression resolves to a slot in `pts.SlotColor` with value `ColorAsync`. Resolve the receiver via the same logic as Task 3's `slotKeyForAssignTarget`, but for read positions.

If `pts.SlotColor` is missing for the slot (e.g. `SlotParam` / `SlotReturn`), fall back to: any candidate in `pts.Candidates(key)` is async ⇒ async. That captures the g3 conservative case for non-storage slots.

- [ ] **Step 5: Wire into the pipeline**

In the function that calls `c.analyzeAsync()`, change the order to:

```go
c.analyzeAsync()
analyzePointsTo(c.pkg) // populates pkg.PointsTo
c.analyzeAsyncWithPointsTo()
c.checkAsyncRules()
```

If the calls live in different methods, wire them coherently. Don't reorder anything else.

- [ ] **Step 6: Run — PASS**

Run: `go test -run TestAnalyzeAsyncWithPointsTo ./internal/checker/`
Expected: PASS.

- [ ] **Step 7: `go tool verify`**

Run: `go tool verify`
Expected: green. Existing async-related tests should keep working — the new pass is additive.

If any test now fails because previously-sync code is now correctly colored async (and the test was wrong before), update the test with a brief comment explaining the change. Don't paper over real regressions.

- [ ] **Step 8: Commit**

```bash
git add internal/checker/
git commit -m "checker: re-run color propagation with points-to (analyzeAsyncWithPointsTo)"
```

---

### Task 6: JS codegen — `await` for async funcvar calls

**Files:**
- Modify: `codegen/lang/javascript/translate_ir.go`
- Test: `codegen/lang/javascript/translate_ir_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestTranslateIRCall_FuncvarAsyncSlotAwaits(t *testing.T) {
	asyncFn := &ir.Func{Name: "asyncFn", IsAsync: true}
	v := &ir.Var{Name: "handler", Type: funcTypeNoArgs()}
	pkg := &ir.Package{
		Vars:  []*ir.Var{v},
		Funcs: []*ir.Func{asyncFn},
		PointsTo: &ir.PointsToInfo{
			Sites:     map[ir.PointsToKey][]*ir.Func{ir.SlotVarKey(v): {asyncFn}},
			SlotColor: map[ir.PointsToKey]ir.Color{ir.SlotVarKey(v): ir.ColorAsync},
		},
	}
	call := &ir.Call{Func: nil, Receiver: identTo(v)} // funcvar invocation: handler()
	scope := &codegen.ExprScope{Pkg: pkg /* set whatever else is required */}
	got := translateIRCall(call, scope)
	want := "await handler()"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestTranslateIRCall_FuncvarSyncSlot_NoAwait(t *testing.T) {
	syncFn := &ir.Func{Name: "syncFn"}
	v := &ir.Var{Name: "handler", Type: funcTypeNoArgs()}
	pkg := &ir.Package{
		Vars:  []*ir.Var{v},
		Funcs: []*ir.Func{syncFn},
		PointsTo: &ir.PointsToInfo{
			Sites:     map[ir.PointsToKey][]*ir.Func{ir.SlotVarKey(v): {syncFn}},
			SlotColor: map[ir.PointsToKey]ir.Color{ir.SlotVarKey(v): ir.ColorSync},
		},
	}
	call := &ir.Call{Func: nil, Receiver: identTo(v)}
	scope := &codegen.ExprScope{Pkg: pkg}
	got := translateIRCall(call, scope)
	want := "handler()"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
```

If `codegen.ExprScope` does not currently carry the `*ir.Package`, add a `Pkg *ir.Package` field to it. Search where `ExprScope` is constructed to populate consistently. If adding a field is too invasive, an alternative is to thread a `pts *ir.PointsToInfo` directly. Pick the minimal change.

- [ ] **Step 2: Run — FAIL**

Run: `go test -run TestTranslateIRCall_Funcvar ./codegen/lang/javascript/`
Expected: FAIL — funcvar branch not aware of points-to.

- [ ] **Step 3: Implement the funcvar-await branch**

In `translate_ir.go:translateIRCall`, after the existing native-call and namespace-call branches, before falling through to the receiver-call fallback, add:

```go
// Funcvar invocation: callee is not a fixed *ir.Func.
if n.Func == nil && n.Receiver != nil {
	receiverJS := translateIRExpr(n.Receiver, scope)
	argStrs := make([]string, len(n.Args))
	for i, a := range n.Args {
		argStrs[i] = translateIRExpr(a.Value, scope)
	}
	call := receiverJS + "(" + strings.Join(argStrs, ", ") + ")"
	if scope.Pkg != nil && scope.Pkg.PointsTo != nil {
		if k, ok := receiverSlotKey(n.Receiver); ok {
			if scope.Pkg.PointsTo.SlotColor[k] == ir.ColorAsync {
				call = "await " + call
			} else if _, hasColor := scope.Pkg.PointsTo.SlotColor[k]; !hasColor {
				// SlotParam / SlotReturn fallback: g3 conservative.
				if anyAsyncCandidate(scope.Pkg.PointsTo.Candidates(k)) {
					call = "await " + call
				}
			}
		}
	}
	return call
}
```

`receiverSlotKey` mirrors `slotKeyForAssignTarget` from Task 3 but for read positions. Define it in `codegen/lang/javascript/` (or a shared `codegen` helper if cleaner). `anyAsyncCandidate` checks `fn.IsAsync || fn.Sig.Color == ColorAsync`.

- [ ] **Step 4: Run — PASS**

Run: `go test -run TestTranslateIRCall_Funcvar ./codegen/lang/javascript/`
Expected: PASS.

- [ ] **Step 5: `go tool verify`**

Run: `go tool verify`
Expected: green. If goldens shift because a funcvar call now correctly awaits, update the golden after confirming the diff is justified.

- [ ] **Step 6: Commit**

```bash
git add codegen/lang/javascript/
git commit -m "js: await funcvar calls when slot color is Async"
```

---

### Task 7: End-to-end txtar fixtures

**Files (Create):**
- `cmd/sngl/testdata/funcvar_stored_async.txt`
- `cmd/sngl/testdata/funcvar_mixed_promotes.txt`
- `cmd/sngl/testdata/funcvar_struct_field_async.txt`
- `cmd/sngl/testdata/funcvar_passthrough_no_promote.txt`

The txtar conventions are documented in `CLAUDE.md` and exemplified by sub-task A's `cmd/sngl/testdata/async_*.txt` (commit `caeb9f5` on `feature/js-async-completion`). Copy those for shape.

- [ ] **Step 1: `funcvar_stored_async.txt`**

```
sngl build --lang js --platform html .
stdout 'await state\.handler\('

-- main.sngl --
import { fetchHello } from "js://app/api"

state handler: () -> string = fetchHello

window MainWindow {
    button "Run" {
        @click { handler() }
    }
}

-- app/api.ts --
export async function fetchHello(): Promise<string> { return "hi"; }
```

(Adjust syntax — `state X: T = init` may be `var X: T = init` in real SNGL.) Inspect existing fixtures to confirm.

- [ ] **Step 2: `funcvar_mixed_promotes.txt`**

A program that assigns both a sync func and an async func to the same `var handler` (e.g. inside a click handler that toggles between them). Assert build succeeds and the call site has `await`.

- [ ] **Step 3: `funcvar_struct_field_async.txt`**

A struct with a funcvar field assigned an async candidate. Read of `s.field()` from a handler awaits.

- [ ] **Step 4: `funcvar_passthrough_no_promote.txt`**

A higher-order func that takes a funcvar param and calls it. Two call sites: one passes a sync func, one passes an async. Both build cleanly. The higher-order callee's body emits `await` (g3 fallback for SlotParam without explicit color). When monomorphization (#48) lands, this fixture's output will tighten.

- [ ] **Step 5: Run all fixtures**

Run: `go test ./cmd/sngl/`
Expected: PASS.

- [ ] **Step 6: `go tool verify`**

Run: `go tool verify`
Expected: green.

- [ ] **Step 7: Commit**

```bash
git add cmd/sngl/testdata/funcvar_*.txt
git commit -m "test: txtar fixtures for closure points-to"
```

---

### Task 8: CDP browser end-to-end

**Files:**
- Create: `codegen/platform/html/funcvar_browser_test.go`

Mirror the pattern in `async_browser_test.go` (sub-task A's commit `da3cfa0`).

- [ ] **Step 1: Write the test**

```go
//go:build !js

package html_test

import (
	"testing"
)

func TestBrowser_FuncvarStoredAsyncUpdatesDOM(t *testing.T) {
	src := `
import { fetchHello } from "js://./api"

state handler: () -> string = fetchHello
state greeting: string = "before"

window MainWindow {
    text(value=greeting)
    button "Go" { @click { greeting = handler() } }
}
`
	runBrowserCase(t, src, browserExpect{
		clickSelector: "button",
		finalText:     "after",
	})
}
```

`runBrowserCase` and `browserExpect` are the helpers introduced in `async_browser_test.go`. Reuse them; if they live in that file, factor them out to a shared `browser_testutil_test.go` so this new test can call them. If sharing helpers between `_test.go` files in the same package is not possible cleanly, copy the minimal harness and accept the duplication.

The `api/index.ts` mock returns `"after"` from `fetchHello` — same shape as the Task 12 mock in sub-task A.

- [ ] **Step 2: Run — PASS**

Run: `go test -run TestBrowser_FuncvarStoredAsyncUpdatesDOM ./codegen/platform/html/`
Expected: PASS (Chrome required; the harness should `t.Skip` when Chrome is unavailable).

- [ ] **Step 3: `go tool verify`**

Run: `go tool verify`
Expected: green.

- [ ] **Step 4: Commit**

```bash
git add codegen/platform/html/funcvar_browser_test.go
# plus any helper-extraction file
git commit -m "test: CDP coverage for stored async funcvar dispatch"
```

---

### Task 9: Final verification

- [ ] **Step 1: Full suite**

Run: `go tool verify`
Expected: all green.

- [ ] **Step 2: CLI build**

Run: `go install ./cmd/sngl`
Expected: clean.

- [ ] **Step 3: WASM build**

Run: `GOOS=js GOARCH=wasm go build ./internal/playground`
Expected: clean.

- [ ] **Step 4: Docs site**

Run: `go tool docsgen`
Expected: clean.

- [ ] **Step 5: Surface unmerged status**

Don't push. Branch lives in worktree at `/home/jonathan/src/git.duckfam.us/jonathan/sngl/.worktrees/closure-points-to`, branch `feature/closure-points-to`. Leave for user.

---

## Self-review notes

**Spec coverage:**
- §2.1 FuncSig.Color → Task 1.
- §2.2 PointsToInfo / PointsToKey → Task 2.
- §3 analyzePointsTo (constraints + fixpoint + slot color) → Tasks 3, 4.
- §4 color re-propagation (analyzeAsyncWithPointsTo) → Task 5.
- §5 codegen wiring (await on async funcvar calls) → Task 6.
- §6 Rule 3 supersession → no task; behavior emerges naturally because g3 promotes slots.
- §7 diagnostics → no task; pass is analysis-only.
- §8 test plan → Tasks 4, 7, 8.
- §9 rollout/risk → Task 9 verifies.

**Open items deferred:**
- `localVarSlotKey` shape (Task 3 step 3) — resolved during impl by reading `ir/stmt.go`.
- ExprScope.Pkg threading (Task 6 step 1) — resolved by adding the field.
- Lambda candidate lifting (spec §"Open items") — still deferred; tests in Task 7 use named funcs to avoid the issue. C (#48) will revisit.

**Type / name consistency:**
- `Color` enum members: `ColorSync`, `ColorAsync`, `ColorParam` — used consistently across Tasks 1, 4, 5, 6.
- Slot kinds: `SlotVar`, `SlotParam`, `SlotField`, `SlotListElem`, `SlotReturn` — consistent.
- Helper functions: `analyzePointsTo` (Task 4), `analyzeAsyncWithPointsTo` (Task 5), `collectConstraints` (Task 3) — distinct names, no collisions.

**Placeholder scan:**
- One deliberate `panic("inspect ir/stmt.go and complete this helper")` in Task 3 step 3, with explicit instructions to resolve. Not a placeholder — it's a guarded extension point.
- "implement as parallel walker mirroring ir/async.go" in Task 5 step 4 — has clear reference; implementer reads the source it points at.

No "TBD" or "fill in details" left.
