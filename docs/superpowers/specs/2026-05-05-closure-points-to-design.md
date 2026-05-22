# Closure / function-variable points-to — design

**Status:** Shipped (ir/pointsto.go)

Sub-task B of [#39 Concurrency model](https://git.duckfam.us/jonathan/sngl/-/issues/39).
Tracking issue: [#47](https://git.duckfam.us/jonathan/sngl/-/issues/47).

Builds on [JS async completion (sub-task A)](2026-05-05-js-async-completion-design.md).

Companion sub-tasks (out of scope here): #48 monomorphization, #49 parallelization, #50 C# target, #51 kicker stale-write race.

## Goal

After this work, SNGL's color analysis flows through funcvars: a function that
calls a funcvar gets colored from the candidates that may flow into that
funcvar, not just from direct callees. Users never declare or distinguish
async vs sync funcvar types. Where mixing is unavoidable (e.g. a stored
funcvar receives both sync and async assignments), the storage slot is
auto-promoted to async and reads are awaited unconditionally — preserving
async-transparency at the cost of one no-op `await` for the sync candidates.

## Non-goals

- Color monomorphization for higher-order callees (#48 / sub-task C). This
  pass produces the points-to data and a `Color = Param(k)` marker; C
  consumes it.
- Auto-parallelization of pure async (#49 / sub-task D).
- C# target (#50 / sub-task E).
- Field-sensitive points-to per struct instance — we use one bucket per
  (struct type, field name) pair (Andersen-style field-insensitive).
- Open-world / cross-module callbacks. SNGL is closed-world; all callers
  and stored funcvar values are visible at compile time.

## Current state

- `internal/checker/async.go` propagates `IsAsync` over the *direct* call
  graph by fixpoint (sub-task A). Funcvar calls are invisible to it: a
  call expression whose callee is not a fixed `*ir.Func` is treated as a
  no-op for color propagation, so a function that invokes an async
  candidate via a funcvar stays sync.
- `*ir.FuncSig` has no color field. The reviewer of A flagged this when
  Rule 3 ("async fn into sync function slot") had to be deferred — there
  is no place to record "this function-typed slot is sync." This pass
  adds the field.
- The IR represents lambdas as `*ir.Lambda` (resolved literal) and
  `*ir.Closure` (post-NoLambda lifted form). The checker sees `*ir.Lambda`
  only. Lowering's `NoLambda` pass converts to `*ir.Closure` later; that
  is downstream of this analysis.

## Design

### 1. Color lattice

```
Color ∈ {Sync, Async, Param(k)}
```

`Param(k)` is reserved for funcs whose effective color depends on the
color of their `k`-th funcvar parameter. C (#48) consumes `Param(k)` to
specialize. Within B, any *concrete* function ends in either `Sync` or
`Async` after the analysis settles. `Param(k)` is set during the call-graph
walk and stays for higher-order funcs.

Lattice join: `Sync ⊔ Async = Async`. `Param(k)` does not join with concrete
colors at the function level — it propagates only through param-flow.

### 2. New data structures

#### 2.1 `FuncSig.Color`

```go
// ir/types.go
type FuncSig struct {
	Params     []*Param
	Return     *Type
	TypeParams []string
	Purity     Purity
	Color      Color // new
}

type Color int

const (
	ColorSync Color = iota
	ColorAsync
	// ColorParam encoded with the param index in a sibling field if needed.
)
```

For the `Param(k)` case, add a sibling `PolyParam int` (or `[]int` if a sig
can be poly on more than one funcvar param). Default `-1` meaning concrete.

#### 2.2 `Package.PointsTo`

```go
// ir/pointsto.go (new file)
type PointsToKey struct {
	Kind  SlotKind // SlotVar | SlotParam | SlotField | SlotListElem | SlotReturn
	Var   *Var     // for Var/Param/Return slots; nil otherwise
	Type  *Type    // for Field/ListElem (the struct or list type)
	Field string   // for Field
}

type SlotKind int

const (
	SlotVar SlotKind = iota
	SlotParam
	SlotField
	SlotListElem
	SlotReturn
)

type PointsToInfo struct {
	Sites     map[PointsToKey][]*Func
	SlotColor map[PointsToKey]Color
}

// Package adds:
//   PointsTo *PointsToInfo
```

`PointsTo` is nil before the pass runs and populated afterward. Codegen
and downstream passes consult it; an absent entry means "no funcvar info"
and falls back to the conservative async-everywhere treatment (g3's "any
async candidate ⇒ Async").

#### 2.3 `SlotKey` construction

Each slot kind has a deterministic key:

- `SlotVar(*Var)` — top-level or local variable holding a funcvar.
- `SlotParam(*Var)` — function parameter that is a funcvar (the `*Var` here
  is the param's symbol; `*Param.Var` or equivalent).
- `SlotField(StructType, FieldName)` — struct field of funcvar type.
  Field-insensitive across instances.
- `SlotListElem(ListType)` — element of a list of funcvar type.
- `SlotReturn(*Func)` — the return slot of a function whose result is a
  funcvar.

### 3. Pass: `analyzePointsTo`

Runs in `internal/checker/`, in a new file `pointsto.go`. The checker's
top-level pipeline calls it after `analyzeAsync()` (which sets the initial
direct-call colors) and before `checkAsyncRules()` (Task 10 from A).

Sequence becomes:

1. `analyzeAsync()` — direct-call fixpoint, leaves on natives marked `IsAsync`.
2. **`analyzePointsTo()` — new.**
3. `analyzeAsyncWithPointsTo()` — re-runs color propagation now that
   funcvar calls have known target sets. Fixpoint.
4. `checkAsyncRules()` — narrowed Rule 1 + Rule 2; Rule 3 stays superseded
   by g3 (see §6).

Algorithm:

```
constraints = []   # list of pts ⊇ pts pairs

walk every Stmt and Expr in the package:
  on `v = e`              if e is a func value: emit pts(slot(v)) ⊇ funcsOf(e)
                          if e is a funcvar:    emit pts(slot(v)) ⊇ pts(slot(e))
  on `g(args...)`         for each funcvar arg: emit pts(param-slot of g[i]) ⊇ pts(arg)
                          for each func-literal arg: emit pts(param-slot) ⊇ {literal}
  on `return e`           emit pts(SlotReturn(enclosing)) ⊇ pts(e) | funcsOf(e)
  on `s.field = e`        emit pts(SlotField(StructOf(s), name)) ⊇ pts(e) | funcsOf(e)
  on `[a, b, ...]`        for each elem: emit pts(SlotListElem(ListType)) ⊇ pts(a) | funcsOf(a)
  on `xs[i] = e`          (mutation) same as struct-field, on the list elem slot

iterate constraints to fixpoint
```

`funcsOf(e)` returns the singleton concrete func when `e` is an `*ir.Ident`
referring to a `*ir.Func`, or a `*ir.Lambda` (treat the lambda's lifted
representation as a candidate — for points-to we only care that it is
some concrete callable; identity doesn't have to survive lowering).

After fixpoint, for each storage slot (`SlotVar`, `SlotField`, `SlotListElem`),
compute the slot color:

```
color(S) = Async  if any f ∈ pts(S) has color Async
color(S) = Sync   otherwise
```

`SlotParam` and `SlotReturn` are *not* coerced — they stay as candidate
sets for C to consume during monomorphization.

### 4. Color re-propagation

`analyzeAsyncWithPointsTo` extends the original fixpoint:

- Old rule: a call `f(direct_callee, ...)` colors caller async iff
  `direct_callee.IsAsync`.
- New rule: a call `f(receiver_expr, ...)` where the call is via funcvar
  (`receiver_expr` evaluates to a slot) colors caller async iff that slot
  has `Async` color (storage) OR any candidate in pts(slot) is async (for
  param/return slots, until C specializes).

Param-slot specifics: a function whose body calls one of its funcvar
params transitively async-bearing flips to `Color = Param(k)` — its
concrete color is decided per call site by C. For B alone, treat
`Param(k)` as Async at the conservative caller-side (await everything
that flows into a Param(k) callee). C will tighten this.

### 5. Codegen wiring (JS / html)

In `codegen/lang/javascript/translate_ir.go`:

- The plain-call path already awaits on `n.Func.IsAsync` (A's Task 3).
- Add a new branch for funcvar calls: when `n.Func == nil` and the call's
  receiver expression resolves to a slot known in `pkg.PointsTo`, look up
  `SlotColor`. If `Async`, prepend `await`.
- Receiver-resolution: for `*ir.Ident` referencing a `*ir.Var`, the slot
  is `SlotVar` or `SlotParam` for that var. For a `*ir.Select` reading
  `s.field`, the slot is `SlotField(StructTypeOf(s), name)`. For `xs[i]`,
  `SlotListElem(ListTypeOf(xs))`.
- Reactive contexts in visual nodes: A's Task 9 hoister already runs;
  a funcvar call inside a visual prop that resolves to an async slot will
  be hoisted into a `__hoist_N` computed and lowered the same way as a
  direct async call.

### 6. Checker rule 3 — superseded, not revived

Rule 3 in A's spec ("async fn into sync function slot") was deferred
because `FuncSig` had no color. With B, every funcvar slot has an
inferred color, but g3 *auto-promotes* slots to async when any async
candidate flows in. There is no "sync-only" slot to violate — the user
never declares colors. So Rule 3 stays a no-op.

If we later add an explicit user-facing sync-only annotation
(`func<sync>(x) -> y` or similar), Rule 3 reactivates as: assignment to
that slot of any candidate whose `Color` is `Async` is a checker error.
Out of scope for B.

### 7. Diagnostics

The pass is a pure analysis: it must not error by itself. Anything that
would be an error becomes a quiet promotion to async (g3). The only
checker errors that can fire because of B are:

- `__pts_*` reserved-prefix collisions if we end up synthesizing names
  (we do not — points-to is a sidecar registry, no synthetic decls).
- A's Task 10 rules continue to fire on parameterized async-reactive
  computeds; B does not change them.

### 8. Test plan

Unit tests in `internal/checker/pointsto_test.go`:

- Direct assignment: `var v = syncFn; v()` → `slot(v)` is Sync; caller stays sync.
- Pure-async store: `var v = asyncFn; v()` → `slot(v)` is Async; caller becomes async.
- Mixed store: `if cond { v = syncFn } else { v = asyncFn }; v()` → `slot(v)` Async; caller async.
- Param flow: `fn run(f: () -> string) { f() }`; called once with sync, once with async; `run` becomes `Color = Param(0)`. (C will specialize; in B the caller side is async-fallback at any call site whose argument might be async.)
- Struct field: `Handler { onClick: () -> void }; h.onClick = asyncFn; h.onClick()` → field slot Async, call awaited.
- List elem: `var hs: list<() -> void> = [syncFn, asyncFn]; hs[0]()` → elem slot Async.
- Return-of-funcvar: `fn pick(): () -> void => asyncFn; pick()()` → return slot Async, both calls awaited correctly.

End-to-end txtar in `cmd/sngl/testdata/`:

- `funcvar_stored_async.txt` — stored funcvar called from event handler;
  generated JS contains `await state.handler()`.
- `funcvar_mixed_promotes.txt` — sync + async assigned to same funcvar at
  different times; build succeeds, runtime calls awaited.
- `funcvar_struct_field_async.txt` — async candidate flows into struct
  field; reads of that field await.
- `funcvar_passthrough_no_promote.txt` — funcvar passed as call-arg only,
  not stored; B alone treats target as Async-fallback (Param(k)). After C
  this becomes monomorphized; for B, just verify build succeeds and
  `await` appears at the call site inside the higher-order callee.

CDP browser test (`codegen/platform/html/funcvar_browser_test.go`):
build a SNGL program that stores `() -> string` async candidate in a
state struct field, click a button to invoke it, assert DOM updates
after promise settles. Mirrors the Task 12 pattern.

### 9. Rollout / risk

- Pure-additive: programs without funcvars or whose funcvar slots resolve
  to all-sync candidates change nothing.
- Programs that previously had a funcvar call to an async candidate would
  have either misbehaved (no `await`) or been forced into a non-funcvar
  shape by the user. With this pass they compile to correctly-awaited JS.
- The g3 promotion is conservative: a stored funcvar that *could* see an
  async candidate awaits unconditionally, even if at runtime only sync
  values were ever assigned. The cost is a one-microtask scheduling per
  call; immaterial in practice. Note this in the user-visible docs once
  the docs system covers funcvars.
- Andersen's worst case is O(n³), but SNGL programs are small (single-app
  scope, closed-world). No bound issues expected. If a pathological case
  emerges, switch to lazy/on-demand resolution — out of scope for now.

## Open items deferred to implementation

- The exact representation of `Color = Param(k)` — single int field vs
  bitmask of param indices — settled during implementation when we know
  whether a single sig needs to be poly on multiple funcvar params at
  once. Default: single int, `-1` = concrete.
- Lambda candidate identity: a lambda literal that flows into a funcvar
  must survive lowering as a callable with a stable identity. NoLambda
  lifts lambdas to top-level funcs; the points-to candidate set in that
  case is the lifted func. Decide at impl time whether to populate the
  set with `*ir.Lambda` (pre-lift) and rewrite during NoLambda, or to
  re-run analysis post-lift. Pre-lift + rewrite is cheaper.
- Receiver-resolution helper for codegen: where to put it. Probably
  `codegen.SlotKeyFor(expr ir.Expr) ir.PointsToKey`. Out of B's scope to
  finalize; design it when wiring step 5.
