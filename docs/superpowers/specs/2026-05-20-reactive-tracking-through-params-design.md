# Reactive Tracking Through Parameters

Date: 2026-05-20

## Summary

Extend the reactive dep tracker so it follows reads and writes through function parameters. Today's tracker only sees bare ident references to component/package vars; method bodies that access state via a parameter (`func main.bigger(this main) => this.x > this.y`) produce zero deps and zero mutated fields, silently breaking reactive subscriptions and setter callbacks.

Replace the standalone read/write walkers with a single substituting walker that carries a `bindings` frame (param name → caller-side expression) and resolves every dep down to a `*ir.Var` pointer. Migrate `DepTracker`'s set fields from `map[string]bool` keyed by name to `map[*ir.Var]struct{}` keyed by pointer.

## Motivation

The existing `walkExprDeps` in `codegen/deps.go` is a flat AST walk. At each `*ir.Ident` it checks the bare name against `modelFields`; at each `*ir.Select` it uses `FindRootIdent` to extract the leftmost ident name and checks that. When the leftmost ident is a function parameter, the name never matches a model field — the walker returns empty deps.

Funcs that read state through params include:

- Top-level method declarations: `func MyComp.bigger(c MyComp) => c.x > c.y`. The walker on the body sees `c.x` and `c.y`; `c` is a param, no dep recorded.
- Desugared nested methods on struct/enum/component (issue #75): the synthetic `this T` receiver param means every method body access becomes `this.field`. Same blind spot.
- Extension methods that pass component state through intermediate helpers: `func helper(c MyComp) => c.totalCost > 100`.

The same gap exists on the write side: `MutatedFields` uses `FindRootIdent` and misses writes through params (`func reset(this main) { this.x = 0 }`). Event handlers that delegate to helper methods don't trigger re-renders for the state those methods mutate.

This spec extends the analysis to recurse through call edges, binding each parameter to its call-site argument expression, and resolves dep keys to `*ir.Var` pointers so name collisions (package var vs component var, two components with the same field name) cannot confuse the tracker.

## Design

### Scope

The walker tracks **reads** of component vars and package-level vars within **tracked contexts**, and **writes** within **untracked contexts**.

- **Tracked contexts** (read-tracking active): visual-node prop expressions, conditional and loop expressions in component bodies, computed-func bodies invoked transitively from those.
- **Untracked contexts** (read-tracking inactive): event handlers (`@click`, `@input`, etc.), timer tick handlers, test functions. Reads here happen once at firing time and don't subscribe; writes still propagate.
- Writes propagate from any context — the same substitution walker visits assignment LHS, toggle targets, and inc-dec targets, including those nested through called helpers.

### Walker shape

A new internal type, `depExtractor`, replaces the standalone walk-* functions:

```go
type depExtractor struct {
    modelVars     map[*ir.Var]struct{}    // tracked vars (Pkg.Vars ∪ all comp.Vars)
    bindings      map[string]ir.Expr      // param name → caller-side expr
    visited       map[*ir.Func]struct{}   // recursion guard, per top-level walk
    deps          map[*ir.Var]struct{}    // accumulated reads (when tracking)
    mutated       map[*ir.Var]struct{}    // accumulated writes
    implicitThis  *ir.Component           // component owning the current tracked walk
    tracking      bool                    // read-tracking on/off
}
```

`bindings` is a single frame; entering a Call clones the extractor with a fresh `bindings` populated from the callee's params and the call-site arg expressions. The fresh frame inherits `modelVars`, `visited`, `deps`, `mutated`, `implicitThis`, and `tracking`.

### Root resolution

A single helper resolves any expression to the `*ir.Var` it ultimately accesses (whole-var granularity):

```go
// peelRoot walks left through Select/Index to the leftmost ident,
// remembering the field name immediately adjacent to that ident.
// For `this.p.x` returns (Ident{this}, "p") — the field rooted on the
// component is what matters; deeper field accesses are subsumed by
// whole-var deps on the root var.
func peelRoot(e ir.Expr) (root *ir.Ident, field string) {
    for {
        switch n := e.(type) {
        case *ir.Ident:
            return n, field
        case *ir.Select:
            field = n.Field
            e = n.Operand
        case *ir.Index:
            e = n.Operand
        default:
            return nil, ""
        }
    }
}

// resolveVar returns the *ir.Var an expression ultimately accesses,
// or nil if it doesn't bottom out in a var.
func (w *depExtractor) resolveVar(e ir.Expr) *ir.Var {
    root, field := peelRoot(e)
    if root == nil {
        return nil
    }
    if expr, bound := w.bindings[root.Name]; bound {
        if field == "" {
            return w.resolveVar(expr)
        }
        return w.resolveVar(&ir.Select{Operand: expr, Field: field})
    }
    if root.Name == "this" && w.implicitThis != nil && field != "" {
        return lookupCompVar(w.implicitThis, field)
    }
    if v, ok := root.Sym.(*ir.Var); ok {
        return v
    }
    if comp, ok := root.Sym.(*ir.Component); ok && field != "" {
        return lookupCompVar(comp, field)
    }
    return nil
}
```

`lookupCompVar(c *ir.Component, name string) *ir.Var` is a tiny helper that scans `c.Vars` for a match. Returns nil if not found.

The substitution rule is straightforward: when the leftmost ident is a bound param, replace the root with the bound expression while preserving the adjacent field. Recursive resolution unwinds arbitrarily deep call chains because each call frame's bindings reference expressions in the caller's scope, and resolution chases through them.

### Read tracking

When `tracking == true`, the walker visits every expression and records `deps[v] = struct{}{}` for each resolved var. The walks for `*ir.Binary`, `*ir.Ternary`, `*ir.Conversion`, `*ir.ListLit`, `*ir.StructLit`, etc., recurse into subexpressions; the only change from the existing walkers is that `*ir.Ident` and `*ir.Select` route through `resolveVar` and call sites recurse with bindings.

Reads inside expressions assigned to `Reads`/`Writes` on the `*ir.Func` are no longer the source of truth for tracking — the substituting walker becomes authoritative. `Func.Reads`/`Writes` remain for purity classification (used by `analyzePurity` to assign `PurityPure`/`PurityReadonly`/`PurityMutates`); the dep tracker no longer consults them.

### Write tracking

When the walker visits an `*ir.Assign`, `*ir.Toggle`, or `*ir.IncDec`, it resolves the LHS via `resolveVar` and records into `mutated`. Call dispatch is the same as for reads: enter the callee body with a fresh bindings frame, accumulating writes from every transitively reachable assignment.

Write propagation respects SNGL's value-vs-reference parameter semantics:

| Param declared type | Calling convention | Writes via this param propagate? |
|---|---|---|
| Component (`MyComp`) | by reference | yes |
| `ref<T>` (any T) | by reference | yes |
| Struct (`Point`) | by value | no — callee writes to a local copy |
| Primitive (`int`, `string`, …) | by value | no |
| List, map (`list<T>`, `map<K,V>`) | by reference (existing language semantics) | yes |

A write through a param is recorded in `mutated` only when the param's declared type uses reference semantics. The walker checks the param's `ir.Type.Kind` at substitution time: `TypeComponent`, `TypeRef`, `TypeList`, `TypeMap` propagate; other kinds don't. Reads always propagate regardless of calling convention — the dependency on the caller-side expression is what matters for re-computation, not whether the body mutates its own copy.

A function called from a tracked-read context can also contain writes; those still go into `mutated` if the user is asking for both (most call sites ask for one or the other, but the walker supports interleaved).

### Call dispatch

```go
func (w *depExtractor) walkCall(c *ir.Call) {
    // Caller-side: receiver and arg expressions are themselves possible reads.
    if c.Receiver != nil { w.walkExpr(c.Receiver) }
    for _, a := range c.Args { w.walkExpr(a.Value) }

    fn := c.Func
    if fn == nil || w.opaque(fn) {
        return
    }
    if _, seen := w.visited[fn]; seen {
        return
    }

    sub := w.cloneFrame()
    sub.bindings = make(map[string]ir.Expr, len(fn.Params))
    for i, p := range fn.Params {
        if i < len(c.Args) {
            sub.bindings[p.Name] = c.Args[i].Value
        }
    }
    sub.visited[fn] = struct{}{}
    sub.walkFuncBody(fn)
}
```

`opaque(fn)` returns true when:
- `fn.AST == nil` (synthetic / native),
- `fn.Intrinsic != ""` (stdlib intrinsic),
- `fn.NativePkg != ""` (scheme-imported),
- `fn.Receiver == ""` and the func is a stdlib-merged function (heuristic: AST source comes from `lib/*.sngl`).

Opaque funcs contribute nothing to deps/mutated on tracked vars. The walker still pre-walks their arg expressions for transitive reads.

### Public API migration

`DepTracker` fields change shape and key type:

```go
// codegen/deps.go
type DepTracker struct {
    ModelVars     map[*ir.Var]struct{}
    ComputedFuncs map[*ir.Func]struct{}
    ComputedDeps  map[*ir.Func]map[*ir.Var]struct{}
    Components    []*ir.Component  // for implicitThis lookup at walk entry
}
```

`Dependent` interface:

```go
type Dependent interface {
    DepVars() map[*ir.Var]struct{}
}
```

Top-level entry points keep their existing names but switch return types:

```go
func (dt *DepTracker) ExprDeps(currentComp *ir.Component, expr ir.Expr) map[*ir.Var]struct{}
func MutatedFields(currentComp *ir.Component, s ir.Stmt) map[*ir.Var]struct{}
func MutatedFieldsExpr(currentComp *ir.Component, e ir.Expr) map[*ir.Var]struct{}
func (dt *DepTracker) ExpandDeps(deps map[*ir.Var]struct{}) map[*ir.Var]struct{}
func (dt *DepTracker) ExpandMutated(mutated map[*ir.Var]struct{}) map[*ir.Var]struct{}
func FindAffected[T Dependent](dt *DepTracker, items []T, mutated map[*ir.Var]struct{}) []T
```

`currentComp` is the component whose tracked context is being walked (for `implicitThis` resolution). Passed by every caller of `ExprDeps` / `MutatedFields`.

`CommonAnalysis` (`codegen/analysis.go`) populates the same fields by pointer:

```go
ModelFields    map[string]bool       // (existing — keep for name-keyed consumers)
ModelVars      map[*ir.Var]struct{}  // NEW — primary key for DepTracker
ComputedFields map[string]bool       // (existing)
ComputedFuncs  map[*ir.Func]struct{} // NEW
```

The name-keyed maps stay for codegen output that emits field names directly (HTML setter names, Go method names). The pointer-keyed maps are what the dep tracker consults.

### Migration of consumers

Every consumer of `DepTracker` switches over its set type. Inventory from grep:

- `codegen/model.go` — `FindAffected` callers in update paths.
- `codegen/platform/html/*.go` — reactivity wiring, setter callback emission.
- `codegen/platform/bubbletea/*.go` — Update() function generation.
- `codegen/platform/fyne/*.go` — mutation model setters.
- `codegen/platform/android/*.go` — Compose recomposition triggers.
- `codegen/lang/golang/*.go` — getter/setter emission, computed field references.
- `codegen/lang/javascript/*.go` — same in JS.
- `codegen/lang/kotlin/*.go` — same in Kotlin.
- `internal/lower/*.go` — passes that consult dep info (computed inlining, reactivity).

Each site swaps `map[string]bool` for `map[*ir.Var]struct{}` and dereferences `*ir.Var` to `.Name` at name-emission boundaries.

## Constraints

- The walker is a static, compile-time analyzer. No runtime tracking, no proxies, no version IDs.
- Whole-var granularity. Reading `c.p.x` and `c.p.y` both subscribe to `c.p`. Mutating `c.p.x` re-renders any reader of `c.p`. Field-level deps are a possible future refinement; out of scope here.
- Recursion is bounded by a `visited` set keyed on `*ir.Func`. Mutual recursion stops at the first revisit; pathological depth can't blow the stack.
- Lambdas (`*ir.Lambda`, `*ir.Closure`) — the walker recurses into the lambda body but doesn't have call-site bindings (the lambda is invoked later, often by stdlib higher-order methods). Any reads in the lambda body resolve against the bindings frame in scope at the lambda's definition. Free idents matching `modelVars` count as deps. This matches today's behavior for `xs.filter(func(x) => x > threshold)`.
- Indirect/funcvar calls (`call.Callee != nil`, `call.Func == nil`) — opaque to call dispatch. Pre-walk the callee expression and args; don't enter a body. Same as today.

## Errors

None. This is internal analysis; no new diagnostics. A pathological case (e.g. recursion depth exceeded by visited-set saturation) is silently bounded.

## Testing

Driver fixtures in `testdata/*.sngl` exercise the new behavior end-to-end:

- `testdata/test_reactivity_through_method.sngl` — component with nested method that reads `this.x`; visual node uses the method via `text(value=method())`. Mutating `x` re-renders.
- `testdata/test_reactivity_extension_method.sngl` — same shape but via top-level extension `func MyComp.method(c MyComp) => c.x > 0`.
- `testdata/test_mutation_through_method.sngl` — event handler calls `update(this)`; method body sets `this.x = 5`; subscription re-fires.
- `testdata/test_reactivity_two_components.sngl` — two components with same-named var (`var x = 0`); proves pointer-keyed deps don't cross-subscribe.
- `testdata/test_reactivity_global_var.sngl` — package-level `var theme = "light"` mutated by an event handler in one component; another component re-renders.

Unit tests in `codegen/deps_test.go` cover the walker directly:

- Substitution through one call level.
- Substitution through two call levels (helper calls helper).
- Recursion (function calls itself indirectly).
- Opaque call edges (stdlib, native imports).
- Implicit `this` resolution.
- Component-namespace access (`childComp.x` from outside).

## Out of scope

- Field-level dep granularity. Tracked as a future refinement.
- Component method desugaring re-enable (issue #75 follow-up). This spec creates the analyzer that re-enabling depends on; the actual re-enable lives in a separate change so it can be evaluated against working analysis.
- Cross-component subscription semantics beyond what platform codegens already do. The pointer-keyed dep set is sufficient to express the dependency; routing of "var V on component C changed → re-render component C" is the platform codegen's existing concern.
- Lambdas with call-site param binding. Lambdas remain opaque from the caller's perspective.

## Risk & mitigation

- **Migration breadth.** Many codegen sites consume `DepTracker`. Mitigation: the migration is mechanical (set type change + dereference at emission). One commit per platform target keeps blast radius scoped.
- **Performance.** Substituting walker re-enters function bodies at every call site, potentially doing redundant work for the same function in different bindings frames. Mitigation: `visited` cuts cycles; in practice SNGL programs are small. If this becomes hot, add a per-frame memoization keyed on `(fn, bindings-hash)`.
- **Semantic change for opaque funcs.** Today `Func.Reads` may include vars read by stdlib funcs via their body (when the stdlib func has a body). The new walker treats stdlib/native funcs as opaque. Mitigation: stdlib `Reads` lists are empty in practice — stdlib funcs read their params, not component vars. Verified by grepping `lib/*.sngl` bodies.
- **API breakage for external consumers.** `DepTracker`'s exported fields change shape. SNGL has no external consumers today; in-tree callers all migrate in this change.

## Plan summary (detailed plan to follow via writing-plans)

1. Driver fixtures + unit tests for the walker (red baseline).
2. Migrate `DepTracker` field types and `Dependent` interface to pointer-keyed sets.
3. Migrate `CommonAnalysis` to populate pointer-keyed maps alongside the existing name-keyed ones.
4. Replace `walkExprDeps`/`walkStmtDeps`/`ExtractDeps` with the `depExtractor` walker.
5. Update `MutatedFields` / `MutatedFieldsExpr` to use the walker.
6. Migrate consumers: lang codegens, platform codegens, lower passes.
7. Re-enable issue #75 component method desugaring in a separate change once the new analyzer lands and tests pass.
