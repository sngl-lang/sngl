# NoLambda Lowering Pass — Design

**Date:** 2026-05-03
**Status:** Shipped (passLambda)
**Tracks:** gitlab issue #40

## Summary

`NoLambda` is the lowering pass that lifts every closure to a top-level `*ir.Func` plus a synthesized "captured-state" struct, so target languages without first-class closure support (C, winforms / .NET pre-anonymous-method, embedded targets) can consume lowered IR directly. The pass introduces one new IR expression node — `*ir.Closure{Func, State}` — and one new IR/SNGL primitive type — `ref<T>` with surface operators `&` and `*` — to honestly express by-reference captures.

The pass runs at step 5 of the lowering pipeline (before `NoReactivity`). Its lift routine is exported so `NoDeclarative` (step 9) can call it when promoting `EventHandler.Func` blocks to top-level — same machinery, two invocation sites.

## Motivation

Phase 1+2 of the master lowering spec (`docs/superpowers/specs/2026-05-02-codegen-lowering-design.md`) deferred `NoLambda` because the output IR shape was unresolved. The Phase 2b plan put it succinctly: capture analysis is derivable, but lifting requires IR shapes the package didn't express cleanly.

Today's shipping language targets (Go, JS, Kotlin) all support closures natively, so they keep `NoLambda` off. The forcing function is the imminent C target (`imperative` platform, master-spec Phase 7). C has no closures; every lambda-bearing program must be lifted before codegen.

## Three locked design decisions

After brainstorm:

1. **Scope:** ship the full pass and goldens now (parallels Phases 2 / 3 — lowered output validated via `dump lowered --format sngl` long before any consumer turns the cap on). C-target consumer follows.
2. **Call-site IR shape:** new `*ir.Closure{Func, State}` expression node. Replaces every `*ir.Lambda` in lowered output. Strong post-pass invariant (`assert: no *ir.Lambda survives a NoLambda run`).
3. **Mutable captures:** add a real `TypeRef` Kind plus `&` / `*` surface syntax to SNGL. The state-struct field type carries ref-ness directly; `dump lowered --format sngl` roundtrips faithfully. (The alternative — a `StructField.IsRef bool` flag with the field type lying about its storage — was rejected because round-tripped SNGL would parse back to value semantics, silently changing program behavior.)

## Architecture

### New IR shape — `*ir.Closure`

```go
// ir/expr.go
type Closure struct {
	AST   *ast.LambdaExpr // original lambda's source position (nil for synthesized handler lifts)
	Type  *Type           // user-visible TypeFunc — without the synthesized leading state param
	Func  *Func           // lifted top-level Func; first Param is the state struct
	State *StructLit      // construction of the captured-state struct at this site
}

func (*Closure) exprNode()         {}
func (x *Closure) ExprType() *Type { return x.Type }
```

Notes:

- `Func` is the same `*ir.Func` value appended to the package's top-level `Funcs` slice — pointer identity matters for downstream passes that index by Func.
- `State.Type` is the synthesized struct's resolved type; `State.Def` points to the synthesized `*ir.StructDef` that's been appended to `pkg.Structs`.
- `Type.Sig` is the *user-visible* signature: original lambda params and return only. The lifted Func's actual signature has one extra leading state Param. Codegen reads `Func.Params` for the full signature and prepends the state field at every call site.

### New IR shape — `TypeRef`

```go
// ir/types.go
const (
	// ...existing kinds...
	TypeRef // Elem set
)
```

Only `Elem` is set. `Decl`, `Sig`, `Meta` unused. Equality: `ref<T> == ref<U>` iff `T == U`.

### New IR / surface ops — `UnaryAddr`, `UnaryDeref`

```go
// ast/expr.go (UnaryOp constants)
UnaryAddr  // &x
UnaryDeref // *x
```

Encoded in IR as `*ir.Unary{Op: UnaryAddr | UnaryDeref, Operand, Type}`.

- `&` requires its operand to be an addressable lvalue. Permitted: `Ident` resolving to a `*Var` or `*Param`; `Select` whose chain bottoms out at one of those; `Index` against an addressable list operand. (List-index targets are already lvalues in SNGL — `list[i]!!` and `list[i]++` are valid — so `&list[i]` falls out naturally and produces `ref<ElemType>`.) Forbidden: literals, call results. Checker enforces.
- `*` requires its operand to have type `ref<T>`. Result type is `T`.

Both ops appear *only* in lowered output. The parser accepts them anywhere a unary expression is valid; pre-lower SNGL programs that use them are legal but rare in practice.

### Auto-deref through `.`

When `Select{Operand, Field}` has `Operand.Type.Kind == TypeRef`, the checker treats the access as if the operand were dereferenced first: the resolved field is looked up against `Operand.Type.Elem`. This matches Go's convention and keeps lowered SNGL readable (`c.__n1.value = ...` reads naturally even when `c.__n1` is `ref<text>`).

The IR materializes this as an explicit `*ir.Unary{Op: UnaryDeref}` wrapping the operand inside the `Select`, so downstream passes never need a special case.

Auto-deref applies to `Select` only. Standalone reads (`*c.count`) and `Index` operations require explicit `*`.

### SNGL surface — minimal additions

```
type ::= ... | "ref" "<" type ">"
unary ::= ... | "&" unary | "*" unary
```

Precedence: prefix `*` and `&` bind at the same level as `!` and unary `-`. The formatter emits `&x` and `*x` with no space, matching the C convention common readers expect.

Hand-written user code never needs these; they're a primitive used exclusively by lowered output. Documenting them as "low-level / lowering-only" in the language reference keeps the surface conceptually minimal.

### `pkg.Structs` and `pkg.Funcs` membership

Synthesized state structs are appended to `pkg.Structs` with names of the form `__lambda<N>_caps` where `N` is a package-scoped counter. Synthesized lifted funcs are appended to `pkg.Funcs` with names `__lambda<N>` — paired by index with the corresponding caps struct.

Both carry full IR metadata (resolved Symbols, Types) so optimizer / later passes / `ir.Convert` treat them indistinguishably from user-written decls. The fresh-name allocator probes existing `pkg.Structs` / `pkg.Funcs` names for collisions and bumps the counter past any clash — no reserved-prefix restriction on user code is needed.

### The lift routine

Exported as a helper on a `lifter` struct so callers (the main NoLambda pass and `NoDeclarative`'s handler-promote step) share state:

```go
// internal/lower/lambda.go
type lifter struct {
	pkg             *ir.Package
	counter         int
	enclosingScopes []*scopeFrame // tracks captured-Var → state-field for nested lifts
}

// Lift converts a closure-shaped Func+body into a top-level Func + caps StructDef
// and returns the *ir.Closure that replaces the original lambda expression.
//
// captures must be the result of analyzeCaptures(body, lambdaParams).
// The lifted Func is appended to pkg.Funcs; the caps struct to pkg.Structs.
func (l *lifter) Lift(body []ir.Stmt, params []*ir.Param, ret *ir.Type, src *ast.LambdaExpr) *ir.Closure
```

NoLambda's pass entry point walks every Expr position via the standard `walk.go` machinery, replacing each `*ir.Lambda` it finds:

```go
walkPackage(pkg, walkFuncs{
    expr: func(e ir.Expr) ir.Expr {
        if lam, ok := e.(*ir.Lambda); ok {
            return l.Lift(lam.Func.Block, lam.Func.Params, lam.Func.Return, lam.AST)
        }
        return e
    },
    stmts: ..., // walk into stmt blocks for nested lambdas
})
```

NoDeclarative invokes the same `Lift` when promoting an `EventHandler.Func` whose body contains free Idents resolving to component-scoped Vars.

### Capture analysis (already proven derivable)

Per the issue: every `*ir.Ident` inside `body` whose `Sym` is a `*Var` or `*Param` *not* declared inside `body`'s lexical scope is a capture. Implementation:

```go
func analyzeCaptures(body []ir.Stmt, params []*ir.Param) []capture
```

Returns a deterministic-ordered slice of `capture{Sym ir.Symbol, Mutable bool}`. `Mutable` is set when the same body contains an `*ir.Assign` whose `Target` chain bottoms out at `Sym`. Order: first-occurrence in the body's left-to-right traversal — keeps lifts stable across runs (and across re-runs in `dump lowered`).

### Lift output shape

For each capture `(sym, mutable)`:

- Field name: `sym.SymName()` (collisions inside one lift are impossible — Sym values are unique within a lexical scope).
- Field type: `mutable ? ref<sym.SymType()> : sym.SymType()`.
- Field init at call site: `mutable ? Unary{Addr, Ident{sym}} : Ident{sym}`.

Inside the lifted body, every `*ir.Ident` resolving to a captured Sym is rewritten:

- Mutable capture, read of the value → `Unary{Deref, Select{Ident{state}, fieldName}}` with type `T` (the underlying captured type).
- Mutable capture, write target → `Unary{Deref, Select{Ident{state}, fieldName}}` as the `Assign.Target`.
- Read-only capture → `Select{Ident{state}, fieldName}`.

When the rewritten access is itself the operand of an outer `Select` (e.g. `state.__n1.value`), the auto-deref-through-Select rule means the lowered IR can omit an explicit `Unary{Deref}`: `Select{Select{Ident{state}, "__n1"}, "value"}` checks correctly because the inner Select's type is `ref<text>` and the outer Select auto-derefs. The lift routine only inserts an explicit `Unary{Deref}` when the captured-field access is the bottom of the expression tree — never inside an enclosing Select.

### Nested closures

When an inner lambda captures a Var that's already a captured field of an enclosing lift, the inner lift's caps field type is `ref<T>` (re-using the outer indirection) and its caller-site init is `Ident{state}.fieldName` — *not* `&state.fieldName`. The `lifter.enclosingScopes` stack tracks (Sym → enclosing-state-field expr) so the inner Lift sees the right indirection.

Example: outer lambda captures `n` (mutable) and contains an inner lambda that also reads `n`. Outer caps field `n` is `ref<int>`. Inner caps field `n` is `ref<int>`, initialized at outer's body via `state.n` (already `ref<int>`). Both lambdas read through the same indirection.

## Worked example

Source (single component, mutable capture, reactive update, handler lift):

```sngl
component counter {
    var count: int = 0
    button(@click {
        count = count + 1
    })
    text(value="Count: " + string(count))
}
```

Caps: `NoLambda + NoReactivity + NoDeclarative` enabled.

After `NoReactivity` (step 7): the `@click` handler block has the reactive updater appended. Component body is unchanged shape — still a `NodeInst` tree.

```sngl
component counter {
    var count: int = 0
    button(@click {
        count = count + 1
        __n1.value = "Count: " + string(count)   // injected by NoReactivity
    })
    text(value="Count: " + string(count))
}
```

(`__n1` is NoReactivity's synthesized id; its `LocalVar` decl with nil Init is prepended to the parent block.)

After `NoDeclarative` (step 9), which itself invokes the NoLambda lift routine on the handler block:

```sngl
struct __lambda0_caps {
    count: ref<int>
    __n1:  ref<text>
}

func __lambda0(state: __lambda0_caps) {
    *state.count = *state.count + 1
    state.__n1.value = "Count: " + string(*state.count)
}

component counter {
    var count: int = 0
    var __n0: button = lower.createNode("button")
    var __n1: text   = lower.createNode("text")
    __n1.value = "Count: 0"
    var __c0 = __lambda0_caps{count: &count, __n1: &__n1}
    lower.attachHandler(__n0, "click", __closure(__lambda0, __c0))
}
```

Notes:

- `state.__n1.value = ...` uses auto-deref through `Select`. The IR for that statement is `Assign{Target: Select{Unary{Deref, Select{Ident{state}, "__n1"}}, "value"}, Value: ...}`.
- `*state.count` is explicit (no Select to trigger auto-deref).
- The body's `__closure(__lambda0, __c0)` is the formatter's surface rendering of `*ir.Closure`. It's not a callable function — the parser would reject `__closure(...)` in user code (reserved name).

C codegen sketch (closure-free target, illustrative — actual emission belongs to the C-target spec):

```c
typedef struct { int *count; text *__n1; } __lambda0_caps;
void __lambda0(__lambda0_caps state) {
    *state.count = *state.count + 1;
    state.__n1->value = strconcat("Count: ", int_to_str(*state.count));
}
// in counter():
int count = 0;
button *__n0 = lower_createNode("button");
text   *__n1 = lower_createNode("text");
__n1->value = "Count: 0";
__lambda0_caps __c0 = { .count = &count, .__n1 = &__n1 };
lower_attachHandler(__n0, "click", (__closure_t){ .fn = __lambda0, .state = &__c0 });
```

## Pass interaction

### Order

Position 5 in the master pipeline (unchanged from the master spec):

```
1 NoUnit  →  2 NoEnum  →  3 NoTernary  →  4 NoComputed  →
5 NoLambda  →  6 NoToggle  →  7 NoReactivity  →  8 NoTimer  →  9 NoDeclarative
```

### NoReactivity sees lowered closures

At step 7, all source-level lambdas are gone. `NoReactivity`'s analysis walks ordinary `*ir.Func` bodies — including the lifted `__lambda<N>` Funcs. Mutations inside lifted bodies route through `state.fieldName` reads/writes; the dataflow analysis tracks the *underlying captured Var*, not the field. `analyzeCaptures` records the Sym→field mapping and exposes it via `pkg.LiftedCaptures map[*ir.Func]map[ir.Symbol]string` so `NoReactivity` can resolve `*state.count` back to "this mutates Var `count`" for updater injection.

### NoDeclarative re-uses the lift routine

Step 9 promotes `EventHandler.Func` blocks (sourced from inline `@click { ... }` style handlers attached to NodeInsts) to top-level. Bodies frequently reference component-scoped Vars — captures, in the technical sense — so promotion alone produces orphan Funcs whose Idents would dangle for closure-free targets. NoDeclarative's promote routine therefore calls `lifter.Lift` on every handler body that has free Idents, threading the lifted Closure into the resulting `lower.attachHandler(...)` call's third argument.

### Post-pass invariant

After `NoLambda`'s apply (when its cap is on): walking `pkg` finds zero `*ir.Lambda` nodes. After `NoDeclarative`'s apply with caps `NoDeclarative+NoLambda` both on: same invariant plus every `lower.attachHandler` third-arg is a `*ir.Closure`. Both invariants are checked at the end of each pass's apply via a cheap `walkPackage` traversal (no debug-build conditional — the cost is one extra walk and the failure mode is debugging-grade nasty without it).

## Testing

### Per-pass goldens (Layer 1+2)

```
internal/lower/testdata/
  lambda_basic.txtar              # immutable capture, single lambda
  lambda_mutable_capture.txtar    # mutable capture, exercises ref<T> + &
  lambda_nested.txtar             # inner lambda captures outer's capture
  lambda_no_captures.txtar        # closure with empty caps struct
  lambda_in_for.txtar             # lambda inside a for-loop (per-iteration captures)
  lambda_handler_lift.txtar       # caps: NoLambda + NoDeclarative, exercises shared lift routine
  lambda_with_reactivity.txtar    # caps: NoLambda + NoReactivity, mutation inside lifted body
```

Each fixture's `expected.sngl` is the formatted lowered IR. Goldens regenerable via `go test -run TestLower -update`.

### Surface-syntax goldens

Parser/formatter round-trip tests for the new `ref<T>`, `&x`, `*x` syntax. Place under `internal/parser/testdata/ref_*.sngl` and run through the existing parse → format → re-parse cycle.

### IR-level invariants

Unit tests in `internal/lower/lambda_test.go` exercising:
- `analyzeCaptures` order stability (multiple runs over same body produce identical capture order)
- Mutable-vs-readonly classification on toy bodies
- Nested-lift sharing (outer state field reused, not re-addressed)
- Synthesized name collision check (parser rejects `__lambda` prefix in user code)

### Composition / regression

Add to the existing `dump lowered --list` regression script: every `examples/*.sngl` runs cleanly under every registered platform's caps. Once the C platform lands in master-spec Phase 7, its end-to-end snapshot tests cover the consumer side.

## Risks

1. **`ir.Convert` fanout.** Adds three ir-Convert renderers (`*ir.Closure`, `TypeRef`, the two new `Unary` ops) plus auto-deref handling. Mitigated by Phase 2's NoTernary precedent — synthesized IR additions have routed through `ir.Convert` cleanly before. Each new shape gets a dedicated test in `ir/convert_fuzz_test.go`.

2. **Optimizer ref-awareness.** Constant folding must not propagate through `Unary{Deref}` (the underlying storage may be mutated elsewhere). Concretely: `*p` is never const, even when `p` was just initialized from `&x` of a const x. Add an early-return in the folder's Unary case for `UnaryDeref` and `UnaryAddr`.

3. **Type system ripple.** Every type-equality / type-printing site needs a `TypeRef` arm. Audit all `t.Kind == ...` switches in `ir/types.go`, `internal/checker/`, `codegen/lang/*`, and `codegen/platform/*`. Most can simply error out for unexpected `TypeRef` (closure-supporting langs never see it).

4. **Auto-deref + `*` interplay.** `*c.count` parses as `*(c.count)` (prefix `*` outermost). When `c` is the value-typed state struct and `count` is `ref<int>`, this is exactly the explicit-deref read of a mutable capture. When `c` is itself `ref<S>`, the inner `c.count` auto-derefs to the field's type — and a further outer `*` only type-checks if the field type is itself a `ref<...>`. Document the rule in the language reference; the lift routine never produces ambiguous nesting because it only emits `Unary{Deref}` directly around a `Select{Ident{state}, ...}` (state is value-typed, never a ref).

5. **Nested-capture bookkeeping.** The `enclosingScopes` stack is small but easy to get subtly wrong. Fixture `lambda_nested.txtar` is the explicit guard. A bug here produces silently wrong runtime output (inner closure mutating its own copy of the captured Var, not the outer's storage cell).

## Companion pass: `NoRef` (ref-elimination via boxing)

Some target languages have neither first-class closures (so `NoLambda` is on) nor first-class pointers / references (so the `ref<T>` shape produced by `NoLambda` is itself unrenderable). For those targets a follow-on `NoRef` cap eliminates `ref<T>` by **boxing** every addressed binding into a one-field reference-semantic struct.

### Cap

```go
NoRef bool // ref<T> → __ref_T struct boxing; & → identity; * → .value
```

Pass position: immediately after `NoLambda`, before `NoToggle` (slot 5.5 in the master pipeline). Reactivity, timer, and declarative passes see only boxed access patterns — no `TypeRef` survives this pass.

### Rewrite

For every `*ir.Var` (top-level, component-scoped, window-scoped, or `LocalVar`) whose address is taken anywhere in the package:

1. Its declared type `T` becomes `__ref_T` — a synthesized one-field reference-semantic struct with field `value: T`.
2. Its initializer `init` becomes `__ref_T{value: init}` (`null` if it had no initializer).
3. Every read of the var (`Ident{v}`) becomes `Ident{v}.value` (a `Select`).
4. Every write to the var (`Assign{Target: Ident{v}, Value: x}`) becomes `Assign{Target: Select{Ident{v}, "value"}, Value: x}`.
5. Every `&v` becomes plain `Ident{v}` — the box itself is the reference.
6. Every `*p` becomes `Select{p, "value"}`.
7. Every `ref<T>` type appearance becomes the synthesized `__ref_T` struct type.

The synthesized box structs are one per *unique element type encountered*, named `__ref_int`, `__ref_string`, `__ref_<componentName>`, etc. Allocation by the consuming codegen target relies on the language's natural reference semantics for structs / objects (JS object, Python class, Lua table, Smalltalk-style object). This pass therefore only makes sense as a cap for targets whose struct semantics are reference-equal — value-struct-only languages without pointers genuinely cannot model shared mutation, and `NoRef` will not save them.

### What gets boxed

Only Vars that are *actually addressed* in the package. `NoLambda`'s lifter records every `&v` it emits in `pkg.AddressedVars map[*ir.Var]bool`; `NoRef` reads that set rather than re-scanning the IR. (Vars whose address is taken by hand-written user code outside lowering also get boxed via the same set — `NoRef`'s pre-walk seeds it from `*ir.Unary{UnaryAddr}` operands.)

### Example

`NoLambda` + `NoRef` applied to the worked counter example above:

```sngl
struct __ref_int { value: int }

struct __lambda0_caps {
    count: __ref_int
    __n1: text                      // not addressed, stays unboxed
}

func __lambda0(state: __lambda0_caps) {
    state.count.value = state.count.value + 1
    state.__n1.value = "Count: " + string(state.count.value)
}

component counter {
    var count: __ref_int = __ref_int{value: 0}
    var __n0 = lower.createNode("button")
    var __n1 = lower.createNode("text")
    __n1.value = "Count: 0"
    var __c0 = __lambda0_caps{count: count, __n1: __n1}
    lower.attachHandler(__n0, "click", __closure(__lambda0, __c0))
}
```

The `state.count.value` accesses replaced what was `*state.count` in the post-NoLambda IR. `count: count` (rather than `count: &count`) at the StructLit reflects the `&` → identity rewrite.

### Limits

`NoRef` only works for targets where struct values themselves carry reference semantics (an object passed to a function shares storage with the caller). Value-struct languages without explicit pointers are out of scope; that combination has no general lowering and any such target would need to ship its own escape analysis or single-call-site promotion, separate from this pass.

## Open items resolved during brainstorm

- **Scope** — full pass + impl, ship goldens now, C-target consumer follows.
- **Call-site IR shape** — new `*ir.Closure{Func, State}` expression node (rather than reusing/extending `*ir.Lambda`).
- **Mutable captures** — `TypeRef` Kind plus `&` / `*` SNGL surface syntax (rather than a `StructField.IsRef` flag whose round-tripped SNGL would lose semantics).
- **Lifted func signature** — ordinary leading `*Param` of state-struct type (rather than a new `Func.Context` field).
- **Auto-deref** — applies to `Select` only; standalone reads use explicit `*`.
- **Pass ordering** — NoLambda stays at step 5; NoDeclarative invokes the same lift routine when promoting handler blocks (single source of truth).
- **Ref-elimination** — added as companion `NoRef` cap (slot 5.5), boxing every addressed Var into a one-field reference-semantic struct. Targets without closures *and* without pointers (e.g. JS-flavored interpreters that have neither first-class closures nor explicit references) opt into `NoLambda + NoRef`.

## File-level changes (preview)

**New:**
- `internal/lower/testdata/lambda_*.txtar` (7 fixtures listed above).
- `internal/parser/testdata/ref_*.sngl` (surface-syntax round-trip fixtures).

**Modified:**
- `ir/expr.go` — add `*ir.Closure` type, `exprNode`, `ExprType`, `IsConst` arm (always false for Closure).
- `ir/types.go` — add `TypeRef` Kind constant; extend equality and printing to handle it.
- `ast/expr.go` — add `UnaryAddr`, `UnaryDeref` constants to `UnaryOp`; regenerate stringer.
- `ast/expr.go` — add `UnaryAddr`, `UnaryDeref` constants. `ref<T>` reuses the existing `ast.NamedType{Name: "ref", TypeArgs: [T]}` shape, parallel to `list<T>` / `option<T>` — no new type expression node needed.
- `ir/convert.go` — `Closure` → renders as `__closure(funcRef, structLit)`; `TypeRef` → renders as `ref<T>`; `UnaryAddr`/`UnaryDeref` → `&x`/`*x`.
- `internal/parser/parse.go` — accept `ref<T>` in type position; accept `&` / `*` in unary position (precedence as documented).
- `internal/parser/format.go` — print the new shapes.
- `internal/checker/expr.go` — type-check `&` (lvalue requirement), `*` (operand must be `ref<T>`); auto-deref through `Select`.
- `internal/checker/types.go` — `TypeRef` equality / compatibility rules.
- `internal/optimize/fold.go` — early-return for `UnaryDeref` / `UnaryAddr` in const folder.
- `internal/lower/lambda.go` — replace stub with full pass + exported `lifter.Lift` helper; add `analyzeCaptures`.
- `internal/lower/walk.go` — extend `walkFuncs` if needed to cover Closure descent.
- `internal/lower/declarative.go` — call `lifter.Lift` from handler-promote step (Phase 3d follow-up; this spec specifies the contract).
- `ir/ir.go` — add `Package.LiftedCaptures map[*ir.Func]map[ir.Symbol]string` field for NoReactivity's Sym-resolution lookup.

**No changes** to `codegen/codegen.go`, `internal/lower/lower.go`, `internal/lower/caps.go` — existing plumbing carries this pass unchanged, just like Phase 3.
