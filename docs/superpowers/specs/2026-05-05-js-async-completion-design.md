# JS async completion — design

**Status:** Shipped (passAsyncReactive)

Sub-task A of [#39 Concurrency model](https://git.duckfam.us/jonathan/sngl/-/issues/39).

Companion sub-tasks (out of scope here): #47 closure points-to, #48 color
monomorphization, #49 purity-driven parallelization, #50 C# target.

## Goal

Make async-transparent compilation work end-to-end for the JavaScript
target (under the `html` platform). Today the compiler propagates
`IsAsync` over the call graph but the JS codegen only awaits *native*
async calls — SNGL→SNGL async calls are silently emitted as plain
calls, no SNGL function declaration ever gets the `async` keyword, and
event handlers / timers / setters never become async wrappers. After
this work, a SNGL program whose call graph transitively reaches an
`async` native leaf compiles to JS that runs correctly: `async`
functions where needed, `await` at every async call site, and a
sane lowering for the one structurally-broken case (an `async` value
read from a reactive computed expression).

## Non-goals

- Closure / function-variable points-to (#47).
- Monomorphization of mixed-color callees (#48).
- Auto-parallelization of consecutive pure async calls (#49).
- C# target (#50).
- Parameterized reactive computed funcs (would require keying the
  synthetic settle-state per call instance — out of scope).
- Any platform other than `html` with `--lang js`.

## Current state

- `internal/checker/async.go` runs a fixed-point pass that flips
  `ir.Func.IsAsync = true` for any SNGL function whose body
  transitively calls an `IsAsync` callee. Sources are native imports
  whose declared signature returned `Promise<T>`.
- `codegen/lang/javascript/translate_ir.go:translateIRNativeCall`
  prefixes `await ` when the native callee is async. The plain and
  namespace call paths do not.
- `codegen/platform/html/html.go` emits every JS function with the
  bare `function` keyword: user funcs (`emitJSFunc`), computed
  zero-arg expression funcs (`function $X()`), setters (`$set_X`),
  updaters, event handler wrappers, timer ticks, struct constructors.
- The async checker pass walks statement and expression shapes but it
  is unclear whether lambda bodies are visited. To be confirmed during
  implementation; if they are not, propagation must descend into
  `*ir.Lambda` (or whatever the IR node is named).

## Design

### 1. Async analysis

Keep `analyzeAsync` as the single source of truth. Add lambda-body
walking if missing so a lambda containing an async call colors its
enclosing function async. No closure points-to — that is #47.

Extract the block/statement/expression walker into a reusable helper
in `ir/async.go`, exported as `BlockHasAsyncCall(stmts []ir.Stmt) bool`
and friends. The checker pass and the html codegen both consume it,
so there is one definition of "this body contains an async call."

### 2. Lowering: async-in-reactive

New pass `internal/lower/async_reactive.go`, registered in
`internal/lower/lower.go` after existing computed/reactivity passes.

Scope: every reactive context where an async call appears. A reactive
context is anything the existing reactivity pass already classifies as
"re-runs when its dependencies change" — zero-arg expression-body
computed funcs, plus reactive subexpressions inside visual nodes
(text interpolation, attribute bindings, conditional/loop guards).

Two sub-cases, handled uniformly via a single hoist-then-lower trick:

**(a) Named computed:** the reactive context is already a named
zero-arg computed func `fn greeting() => …`. Lower in place.

**(b) Inline reactive expr:** the reactive context is an anonymous
subexpression in a visual tree, e.g.
`Text("Hello, " + await fetchHello())`. Hoist the maximal async
subexpression into a synthetic anonymous zero-arg computed
`__hoist_<n>`, replace the original site with a call to it, then fall
into case (a). "Maximal" = the largest containing subtree whose free
variables are all reactive dependencies (no enclosing-scope locals,
no non-reactive params); the existing reactivity dependency-set
machinery already computes this.

Out of scope (still a checker error): parameterized reactive computed
funcs containing async. Settling state would need to be keyed per
parameter tuple, which is a different lowering and lives outside this
spec.

Transformation:

```
// before
fn greeting() => await fetchHello()      // computed, reactive

// after (conceptually — emitted at IR level)
var __async_greeting: string = ""        // synthetic state field, zero of T
fn $compute_greeting() {                 // synthetic, fire-and-forget
  __async_greeting = await fetchHello()
  // codegen: re-run any updaters that depend on `greeting`
}
fn greeting() => __async_greeting        // synchronous read after lowering
```

- The synthetic state field's name is `__async_<orig>`; collision is
  rejected by the checker.
- Type is the original computed return type. Zero value: `""` for
  string, `0` for numerics, `false` for bool, `null` for struct/list,
  unit zero-value where applicable. If the type has no sensible zero
  (e.g. enum without a zero variant), the lowering errors and the user
  must give an explicit fallback. (We keep this conservative — no
  silent `undefined`.)
- The synthetic kicker `$compute_greeting` runs once at startup
  (before updaters wire up) and additionally on every change of any
  reactive dependency of the original expression. The dependency set
  is whatever the existing reactivity pass already computed.
- Reads of `greeting()` everywhere else are rewritten to read
  `__async_greeting`. The original `greeting` symbol disappears from
  the IR after lowering, replaced by the synchronous wrapper above.

### 3. Codegen wiring

In `codegen/platform/html/html.go`, prefix `async ` based on
`ir.BlockHasAsyncCall` of the body being emitted at:

- `emitJSFunc(fn)` — `async function …` iff `fn.IsAsync`.
- `function $set_X(v)` — `async function $set_X(v)` iff any of the
  handler bodies, transitively-called updaters, or timer-sync hooks
  it inlines contains an async call.
- Event handler `addEventListener(…, function() { … })` — `async
  function` iff body async.
- Timer tick `$timer_N_tick` and timer sync `$timer_N_sync` — `async
  function` iff body async.
- Updaters (`function NAME() { … }`) and computed expression-body
  funcs (`function $X()`) and struct constructors stay synchronous.
  After the lowering pass, none of them can transitively reach an
  async call. If one ever does, codegen panics (programmer error).

`html.go` already knows enough to compute "is this body async" because
the bodies are still IR statements at emit time. Use the new
`ir.BlockHasAsyncCall` rather than re-walking ad hoc.

### 4. SNGL→SNGL await emission

In `codegen/lang/javascript/translate_ir.go`, mirror the native-call
treatment in every call-site translator:

- `translateIRPlainCall`: if `n.Func != nil && n.Func.IsAsync`, wrap
  result `"await " + call`.
- `translateIRNamespaceCall` (method-call form): same predicate, same
  wrap.
- `translateIRNativeCall`: unchanged.

Effect: every async call — native or SNGL — is awaited at the call
site. This is approach **b1** in the brainstorm: sequential await,
predictable mutation order. Pure-async fan-out is #49.

### 5. Checker rules

- Async inside a parameterized reactive computed is a checker error:
  "async expression not allowed in parameterized reactive context."
  Other reactive contexts are handled by lowering (§2).
- Async function passed where a sync function type is expected is a
  checker error. (Closure points-to that would otherwise color the
  callee site is #47.)
- Reserved synthetic prefix `__async_` on a user-declared name is a
  checker error.

### 6. Test plan

Txtar fixtures under `cmd/sngl/testdata/`:

- `async_native_call.txt` — SNGL function calls one async native;
  generated JS contains `async function` and `await`.
- `async_handler.txt` — event handler triggers an async chain;
  generated JS wraps the handler with `async function`.
- `async_setter.txt` — `@change` handler is async; `$set_X` becomes
  `async function`.
- `async_timer.txt` — timer tick is async.
- `async_computed_lowered.txt` — named computed expr with async;
  check presence of synthetic state field, kicker, sync wrapper, and
  read-site rewrite.
- `async_inline_reactive_lowered.txt` — async inside a Text/attribute
  binding inside a visual node; check that an anonymous `__hoist_N`
  computed is synthesized and lowered.
- `async_in_computed_with_param.txt` — checker error (parameterized
  reactive computed).

Lowering golden tests in `internal/lower/testdata/` for the
async_reactive pass.

End-to-end: at least one CDP browser test in `codegen/platform/html`
that exercises an async chain settling and verifies a DOM update
fires after the promise resolves.

### 7. Rollout / risk

- The lowering pass is purely additive; programs without async are
  unaffected.
- Adding `async` to a JS `function` keyword changes the return type
  to a Promise. Anywhere user code reads the function as a value
  (e.g. assigning a SNGL func to a JS callback prop) the consumer
  must handle a Promise. We do not currently expose SNGL funcs as
  JS values; if we did, this would become the first place #47 is
  forced. Not in scope here — flag in code if encountered.
- `await` in a JS function whose enclosing wrapper is not `async` is
  a syntax error. The codegen rules above guarantee that any JS
  function emitted with an `await` inside has been marked `async`.
  Verify in tests with an intentionally tricky case (async call
  nested in a ternary, etc.).

## Open items deferred to implementation

- Confirm the IR node name and walker entry point for lambdas in
  `analyzeAsync` and add visiting if missing.
- Confirm that `translateIRMethodCall` (or equivalent for method-form
  calls on receivers) reuses the same `IsAsync` predicate.
- Decide whether the synthetic kicker for a computed lowering runs
  at module init or at first read. Rec: at init, to keep semantics
  simple. Revisit if it causes ordering issues with constants.
