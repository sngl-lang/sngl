# Compiler-phase audit

Findings on phase boundary violations, ordering bugs, and correctness gaps
across `internal/parser/`, `internal/checker/`, `internal/optimize/`,
`internal/lower/`, and the public surface in `sngl.go`. file:line references
throughout. Ordered by severity within each section.

---

## 1. Phase-boundary violations

### 1.1 Purity analysis ignores transitive purity (UNSOUND)

**File:** `internal/checker/purity.go:9-26`, `internal/checker/purity.go:138-139`

`analyzePurity` walks the AST and only flags `w.mutates=true` on direct
`AssignStmt`/`ToggleStmt`/`IncDecStmt`/`EmitStmt`/`IncDec`. The `CallStmt`
case at line 138 walks `x.Call` (the operand/args) but never inspects the
callee's purity. So `func f() { g() }` is `PurityPure` even if `g` is
`PurityMutates`. Optimizer then folds `f()` via `interpretFunc`
(`internal/optimize/interpret.go:65-67`) — which gates only on
`fn.Purity == ir.PurityPure`. A user-declared impure function transitively
called from a "pure" wrapper will be silently evaluated at compile time and
its side effects discarded.

Stdlib has a separate fixed-point propagation in
`internal/checker/stdlib.go:190-199` (`highestCalledPurity` iteration),
which is correct for stdlib but never runs over user funcs.

**Severity:** soundness bug. Fix: propagate purity over the call graph in
pass2 after `analyzePurity` completes the per-func walk, identical to the
stdlib loop.

### 1.2 Purity walker is name-based, no scope awareness

**File:** `internal/checker/purity.go:64-67`, `:181-183`, `:238-242`

`purityWalker.walkExpr` checks `if _, ok := w.vars[x.Name]; ok` against a
single `map[string]*ir.Var` built from `c.pkg.Vars` only
(`internal/checker/checker.go:1656-1662`). Component-local vars
(`comp.Vars`), window vars, and lambda-local `LocalVar`s aren't in the map.
Worse, a local var that shadows a package var gets the package-var purity
flag wrongly attributed: `func f(x int) { x = 1 }` reads `x` as the
parameter but `w.vars["x"]` may match an unrelated package var of the same
name, and `w.mutates = true` fires for a write to a *parameter* — flagging
a pure function as mutating.

**Severity:** correctness. Walk over IR (which already has resolved
`*ir.Var` pointers via `ir.Ident.Sym`) instead of the AST, or thread the
real scope through.

### 1.3 Purity / access analysis walks AST, not IR

**File:** `internal/checker/purity.go:9-49`

After pass2 builds the IR, `analyzePurity` and `trackAccess` re-walk
`f.AST.Body` / `f.AST.Block` from the AST. Means they ignore desugaring
that the checker performed (interpolation lowered to concat calls, method
desugaring, implicit conversions, etc.). For any future pre-lower
desugaring done in the checker, purity will silently diverge from what the
optimizer/lower see. Should walk `fn.Block` IR.

### 1.4 Type-namespace method recognition duplicated in `nonConstCallRef`

**File:** `internal/checker/checker.go:717-735`

`nonConstCallRef` hardcodes `int, float, string, bool, list, color, ref`
as type namespaces to accept method calls on. This is checker doing the
parser/resolver's job of "is this a type-method dispatch" — and it will
silently fall out of sync with the actual type-namespace dispatch in
`expr.go`. Already missing `map`, `iter`. Should ask `c.scope` /
`c.symtab.Methods` rather than string-list.

### 1.5 Checker re-checks stdlib documents during every package check

**File:** `internal/checker/checker.go:490-503` `buildPkgFromDocs` and
`internal/checker/checker.go:1268-1297` `lookupOptions`/
`lookupStdlibOptions`.

Platform/language `Package()` docs go through a fresh `Check(...)` call
inside the current Check. Stdlib parsing is `sync.Once`-cached but stdlib
*checking* is not — every user `Check` re-checks every platform's docs.
Imported package re-check is also unmemoized: same imported directory
processed N times for N callers walks N type checks. Memoize per-import.

### 1.6 Checker emits raw `*ir.Literal` for unit lowering decisions

**File:** `internal/checker/checker.go:1656` and surrounding
`exprType`/`adaptLiteralZero` calls.

Lots of `adaptLiteralZero`/`wrapIfNeeded` calls perform implicit
conversions in-place rather than materializing an `*ir.Conversion` node
(per `feedback_explicit_conversions.md`). Downstream phases see an
ir.Literal of one type with `Type:` retagged. The audit memory mandates
"every implicit conversion as `ir.Conversion`" — this is being violated.
Check `internal/checker/coerce.go`:

```
grep -n adaptLiteralZero internal/checker
```

returns 12 call sites; only a few wrap into `ir.Conversion`. Codegen has
to re-derive that a conversion happened.

### 1.7 Stale `CLAUDE.md`: CEL is not actually used anywhere

**File:** `CLAUDE.md:54`, codebase grep.

CLAUDE.md claims the checker uses CEL. There is no CEL import in
`internal/checker/`, no `cel-go` in `go.mod`. The two-pass description is
otherwise accurate. Update CLAUDE.md to remove the CEL claim.

### 1.8 Format → Parse round-trip drops in-stmt comments

**File:** `internal/parser/build.go:139-164` (`injectComments`),
`internal/parser/format.go:1199-1201`.

`injectComments` only inserts comments between top-level `doc.Stmts`
based on line number — comments inside a function body, between visual
node children, on the same line as a stmt, etc., never make it onto the
AST. Format then has no way to emit them. Parse→Format→Parse is *not*
fixpoint-stable: any nested comment is lost on the first round trip. Spec
the round-trip property (no nested comments) or fix.

---

## 2. Ordering / correctness bugs

### 2.1 Same `*ir.Package` mutated across multiple build targets

**File:** `cmd/sngl/compile.go:122-188`, `cmd/sngl/build.go:122-160`,
`cmd/sngl/run.go:134-149`.

```
for _, target := range targets {
    optimize.Optimize(pkg, optCfg)       // mutates pkg
    lower.Lower(pkg, caps, opts{...})    // mutates pkg further
    optimize.Optimize(pkg, optCfg)       // optimize2
    generateTarget(..., pkg, target, ...)
}
```

`pkg` is the same pointer across iterations. After target #1 runs through
lower, `pkg.Components[*].Body` has been rewritten into the create/append
intrinsic stream, `pkg.Vars` extended with synthesized reactive vars,
ternaries lowered, etc. Target #2 sees that already-lowered state instead
of the original IR, then runs its own optimize+lower on top — producing
either compile failures, double-lowering corruption, or just wrong code
that happens to look OK on toy fixtures. There is no `ir.ClonePackage` in
the codebase. Either deep-copy the package per target or have lower be
non-destructive. The "fold once, lower once per target" topology is broken
under multi-target builds.

**Severity:** correctness, latent. Triggers any time `output { ... }`
declares more than one platform.

### 2.2 Optimizer runs before lower — sees high-level constructs lower will rewrite

**File:** `cmd/sngl/compile.go:139-173`, lower order in
`internal/lower/lower.go:46-64`.

Pre-lower optimize folds ternaries, ifs, for-loops, computed vars, enum
literals, etc., based on the high-level shape. Post-lower optimize2 sees
the *lowered* shape (intrinsic calls, synthesized vars, etc.). The two
runs share the same `Optimize` function which has only one switch over IR
nodes — so fold-time decisions about "is this a const" depend on which
shape the IR is in. Concretely: `foldIfStmt` (`fold.go:223-239`) handles
`*ir.If`, but after `passReactivity` and `passDeclarative`, conditional
trees may live inside synthesized renderer closures whose body is opaque
to the optimizer. Conversely, optimize1 inlines pure functions across
component bodies, but `passInlinePure` (lower) does similar work for
stdlib wrappers. Overlap of responsibility between optimize and lower's
`passInlinePure` is not documented and produces correctness ambiguity.

### 2.3 `foldIfStmt` produces ill-formed IR (`Cond: nil`)

**File:** `internal/optimize/fold.go:226-234`.

When the condition folds to `true`, `s.Cond = nil` and the `*ir.If` is
returned with body. Every downstream consumer must handle the nil-cond
sentinel. `internal/optimize/shake.go:217` calls `walkExpr(n.Cond,...)`
which short-circuits on nil — safe but accidental. Codegen platforms each
check `if If.Cond == nil { … }` — but it's a footgun for new platforms.
Either splice the body inline (drop the `*ir.If` shell entirely, which is
what `foldStmts:142` already does) or document the sentinel on `ir.If`.
The duplication between `foldStmts:138-149` (which *does* inline) and
`foldIfStmt:226-234` (which *doesn't*) is the bug — `foldIfStmt` is
reached via `foldStmt` for non-block contexts (within `*ir.Window`,
`*ir.NodeInst.Children`, etc.) and produces the malformed shape.

### 2.4 `passTernary` synthesizes Ident without `Sym` pointer back to LocalVar

**File:** `internal/lower/ternary.go:283-308`.

`liftTernary` emits

```go
tmpDecl := &ir.LocalVar{Name: name, Type: t.Type}
tmpRef := &ir.Ident{Name: name, Type: t.Type}
```

with no `tmpRef.Sym` pointing at `tmpDecl`. Later passes that walk
`*ir.Ident.Sym` (DCE in `internal/optimize/shake.go:255-258`, reactivity
in `internal/lower/reactivity.go`, codegen) lose the binding. Symbol-table
hygiene rule says: every `*ir.Ident` carries a resolved Sym. The
synthesized assign-Idents in `:293, :298` also lack Sym. Set
`tmpRef.Sym = tmpDecl` and similar.

Also: `tmpDecl.Init` is left nil — the LocalVar is declared
uninitialized, with the value coming via subsequent `Assign` inside If.
Languages that disallow uninitialised locals (Go without zero-value
support for some types, Kotlin with strict null) need a zero-init —
codegen will have to detect and synthesize one.

### 2.5 Optimize FP arithmetic produces Inf/NaN; floatToStr stringifies them

**File:** `internal/optimize/consteval.go:702-779`, `irLiteral:451`.

`math.Sqrt(-1.0)`, `0.0/0.0`-style folds, `math.Pow` overflow → `+Inf`,
`-Inf`, `NaN`. `numericOp` does check `rf == 0` for div, but `1.0/0.0`
arrives as float and... actually it's caught (`rf == 0` triggers, returns
not-ok). But Pow/Sqrt of negative don't have guards. The resulting Go
`float64` rounds back through `floatToStr` (in helpers); strconv likely
emits "+Inf" / "NaN" which is not a valid SNGL literal. Any language
codegen then emits an unparseable literal. Add an `if math.IsNaN(f) || math.IsInf(f, 0) { return nil, false }` guard before returning fold
results.

### 2.6 Integer arithmetic has no overflow detection

**File:** `internal/optimize/consteval.go:512-534`.

`li + ri`, `li * ri` on Go `int` silently wraps on overflow. Folding
`const MAX = 9223372036854775807 * 2` produces a negative literal, which
then becomes a `*ir.Literal{Raw: "-..."}` — but the language target may
be `js` (no int64), `kotlin` (`Long`), etc. Different targets have
different overflow semantics; the optimizer's silent wrap is its own
arbitrary choice. Either define IR int as bounded with explicit overflow
diagnostics, or detect overflow with `math/bits` and refuse to fold.

### 2.7 Lower pass-ordering comment contradicts the actual `passes` slice

**File:** `internal/lower/lower.go:30-46`.

Doc comment says "7a. InlinePure ... Runs after reactivity wires
user-level deps." Actual `passes` slice:

```
passInlinePure        (index 10)
passNoInlineComponents (11)
passNoImplicitRecv    (12)
passReactivity        (13)
```

`passInlinePure` runs **before** `passReactivity`, opposite to the
comment. Either the order is wrong or the comment is wrong; given that
reactivity needs to see post-inline node identity, the order is probably
intentional and the comment is stale. Update.

### 2.8 `ir.For` in `passNoListLambdas` lacks loop-var symbol

**File:** `internal/lower/list_lambda.go:410-415`.

Synthesized `*ir.For{Key: itemName, ...}` carries the loop var as a
string. No `*ir.Var`/`*ir.LoopVar` declaration is created, no symbol
binding is set on `itemIdent` references inside the body. Same class as
2.4: post-pass walkers that match on `*ir.Ident.Sym` cannot find the loop
variable. Codegen platforms paper over by inferring from `Key` string —
brittle.

### 2.9 Second optimize pass after lower can re-fold lowered intrinsic calls

**File:** `cmd/sngl/compile.go:168-174`, `internal/optimize/consteval.go:64-72`.

`isConstExpr` for `*ir.Call` returns true when `call.Func.Purity == PurityPure`. Lower synthesizes intrinsic calls (`lower.CreateNode`,
`lower.AppendChild`) marked pure by their intrinsic definitions in
`ir.Intrinsics`. If args (which are themselves synthesized
`*ir.Literal`s) happen to all be const, `evalCall` will try to fold the
intrinsic via `evalCallFunc(call.Func.Name, args)`. If that succeeds
(e.g. `parseInt`-style intrinsic), the synthesized DOM-builder call gets
collapsed to a literal — destroying the lowered output. Confirm no
intrinsic name overlaps with `evalCallFunc`'s builtin table, or gate the
second optimize pass to skip intrinsics.

---

## 3. Checker pass1 / pass2 issues

### 3.1 pass1 vs pass2 ordering: imports → types → comp/var/func/visual; ConstDecl init pre-checked in pass1

**File:** `internal/checker/checker.go:232-301`.

pass1 calls `c.registerConsts(s)` which type-checks the initializer
(`c.checkExprExpecting`) and runs `c.nonConstRef` against the scope. At
this point ConstDecls iterated in source order: a const that references a
later-declared const will hit the "forward-references" branch
(`checker.go:583`). But mutual top-level const references are otherwise
fine in most languages (Go orders them topologically). SNGL forbids
forward refs in consts. Document the limitation or sort.

### 3.2 `preCheckComponentMethods` runs `checkFuncBody` then discards diagnostics

**File:** `internal/checker/checker.go:1701-1748`.

The pre-pass runs `c.checkFuncBody(fn)` for inference; the comment notes
"discards diagnostics — they may be spurious." But `checkFuncBody`
*mutates* the IR (`fn.Block = c.checkBlockIR(...)`, sets `fn.Return`).
The "real" check at `:1825-1836` runs again, *appending* to the same
`fn.Block` semantics? Let me re-check — actually `fn.Block = ...` overwrites,
not appends, so it's idempotent. But any side effect into `c.symtab`,
`c.pkg.Vars`, etc., from the first run is permanent. If the first run
synthesizes a method (e.g., `RegisterMethod` for a nested foreign-type
method), it's now duplicated.

Discarding diagnostics works only if the second run produces a *superset*
of issues. If the first run, with provisional Dyn vars, emits a spurious
error and the second run with refined types emits a *different* spurious
error, the second one survives — and was caused by the partial state from
the first run.

### 3.3 Async/error analysis depends on points-to and runs after pass2 in fixed order

**File:** `internal/checker/checker.go:62-66`.

```go
c.analyzeErrors()
c.analyzeAsync()
analyzePointsTo(c.pkg)
c.analyzeAsyncWithPointsTo()
c.checkAsyncRules()
```

This is "pass3-pass7" — five sequential analysis passes that each depend
on the previous. None of them are gated on whether earlier passes
produced errors. If pass2 left `*ir.Func.Block` partially populated (a
type error caused early-return from `checkFuncBody`), these analyses
walk half-built IR and may panic or produce nonsense diagnostics that
mask the original error.

### 3.4 `c.symtab.RegisterMethod` called both in `registerFunc` (pass1) and `checkComponentBody` (pass2)

**File:** `internal/checker/checker.go:908`, `:1803`.

A component-level method `func compName.foo(...)` registered via
`registerFunc` is registered *again* during `checkComponentBody`. Method
lookup may return the same fn twice, or `RegisterMethod` may dedup
(check). If dedup is by pointer, OK; if by name, the second registration
overwrites the first. Race condition smell.

### 3.5 pass1 silently swallows AST stmts it doesn't recognize

**File:** `internal/checker/checker.go:273-300`.

The default branch comments "IfStmt, ForStmt at top level are checked in
pass2" but does nothing. If pass2 also doesn't reach them, they're
dropped silently. Top-level `*ast.IfStmt` is meaningful (platform-gated
top-level decls), but the registration step doesn't preregister any
binders inside the If body — so any decl nested inside top-level
`if PLATFORM == "html" { ... }` is invisible to forward references.

### 3.6 `nonConstRef` returns "<function call>" sentinel — fragile interface

**File:** `internal/checker/checker.go:584-590`, `:743`.

The function returns either a real identifier name or the string
`"<function call>"` to encode "non-const call". Callers must check
`strings.HasPrefix(name, "<")` to distinguish. Replace with `(name string, isCall bool)` or a typed enum.

### 3.7 `windowType` lookup races stdlib loading

**File:** `internal/checker/checker.go:174-176`.

`c.windowType` is initialized from `c.symtab.Types["Window"]` right after
`loadStdlib`. Any stdlib that registers Window later (e.g., via a
deferred body check at `stdlib.go:179-181`) would arrive after this
lookup. Today it works because Window is a direct struct decl, but the
ordering invariant isn't expressed.

---

## 4. Optimize

### 4.1 DCE doesn't recurse into `pkg.Outputs.Options` references

**File:** `internal/optimize/shake.go:127-155`.

Roots = components, windows, timers, outputs (referenced by struct lits),
test functions. But the loop never walks `pkg.Outputs[*].Options` —
constants used as option values (e.g., `output { lang { html(entry = appConst) } }` where `appConst` is `const appConst = "foo"`) won't be
marked alive and will get shaken. Verify with a fixture that uses a const
as an output option.

### 4.2 DCE walks timers twice for component timers

**File:** `internal/optimize/shake.go:120-122` walks `walkTimer` for
`comp.Timers`; `:140-142` walks `pkg.Timers`. If a component's timers
*and* the package's timer list overlap (mergePkgInto / extension
mechanics), double-visit is harmless (memoized via `used` map) — but
indicates the IR ownership story is murky.

### 4.3 DCE silently drops methods registered only via `symtab.Methods`

`internal/optimize/shake.go:71-156` enumerates `pkg.Components`, walks
their `Funcs`, but a foreign-type method registered into `c.symtab.Methods["int"]`
nested inside a component (`func int.double(...)`) is appended to
`c.pkg.Funcs` per `registerFunc:904`. Walked only if some live symbol
calls it via `walkCallExpr → walk(call.Func)`. Methods called via dynamic
dispatch / receiver-only `x.double()` where the resolver hasn't bound
`call.Func` at check time will see `call.Func == nil` → DCE skips the
method → it's shaken even though it's the only definition for that
method name → codegen later can't find it.

### 4.4 Pure-function caching: no compile-time call-result cache

`evalCall` re-evaluates the same pure call with the same args every time
it appears. With `interpretFunc` running a full interpreter env build via
`interp.BuildEnv` (`internal/optimize/interpret.go:88-95`), this is
non-trivial cost. Cache by `(fn, hash(args))`.

### 4.5 Pure native Go func execution shells out to `go run`

**File:** `internal/optimize/goexec.go:88` and surrounding.

`go run` at fold time is a per-call subprocess. No memoization. The
purity guard is "the user marked it pure"; if the underlying Go function
actually has side effects (filesystem, RNG, time.Now), folding produces
nondeterministic output. There's no sandboxing.

### 4.6 `expandForWindows` runs on root only — imported windows never expand

**File:** `internal/optimize/optimize.go:72-75`.

Comment says "Phases 3+4 run only on the root package." For imported
windows inside a library, `for-loop`-generated windows are not unrolled.
Codegen then sees an `*ir.For` inside `window` context, which most
platforms don't handle. Either deny library-level dynamic windows in the
checker or expand recursively.

### 4.7 file-asset accumulation across calls subtly relies on caller-managed `Config.FileAssets`

**File:** `internal/optimize/optimize.go:82-100`.

The dedup loop assumes the caller passes the *same* `*Config` across
optimize1 and optimize2; if a new Config is allocated, the first-pass
assets are lost. `cmd/sngl/compile.go:133-138` does reuse, but
`cmd/sngl/dump.go` may not. Anything that calls Optimize without
threading the same Config across both invocations silently drops file
assets.

---

## 5. Lower

### 5.1 No idempotence guarantee for lower passes

There is no test fixture (per grep `idempot` returns nothing in
`internal/lower/`) verifying that `lower.Lower(pkg, caps); lower.Lower(pkg, caps)`
is a no-op on the second call. The dump tool's `--after pass` flag
implies stopping is meaningful, so re-running might re-rewrite. For
example, `passTernary` doesn't check whether an `*ir.Ternary` was already
lowered — a second run on a no-ternary IR is presumably a no-op, but
`passNoRef` *does* match on `ref<T>` shapes that earlier passes
synthesize, and there's no idempotence assertion.

### 5.2 `passInlinePure` ordering claim contradicts source (cross-ref #2.7)

See #2.7. Documentation drift.

### 5.3 Lower passes have no enable/disable telemetry — only optimize does

**File:** grep `slog\.` in `internal/lower/` returns zero hits.

CLAUDE.md says `-v` emits "phase timing". Lower has none. Optimize logs
per-sub-phase. Asymmetric. Add a per-pass `slog.Debug("lower:pass", "name", p.name, "duration", ...)` wrap.

### 5.4 Caps-merge is field-wise OR — no way to express conflict

**File:** `internal/lower/caps.go:35-54`.

If platform wants `NoTernary=true` and language wants `NoTernary=false`,
OR forces lowering. Conceptually a language is a strict subset (it can
consume everything a platform can or more); but the API doesn't enforce
"language ≤ platform". A code-emitting language that *requires*
ternaries (won't accept lowered If form) cannot opt out.

### 5.5 Lower has no diagnostic mechanism — only `error`

**File:** `internal/lower/lower.go:89-117`.

A pass returns `error` on failure. There's no `ir.Diagnostic` slice for
"this construct can't be lowered because X". A pass that hits an
unsupported pattern can only fail the whole build with a wrapped go
error. Add a diags out-param so lower can emit position-bearing
diagnostics like the checker.

### 5.6 `passReactivity` rebuilds `__nN` counters; `idCounter` is per-package, not per-component

**File:** `internal/lower/reactivity.go:32`, propagation through
`reactivityState`.

A single shared counter across components produces `__n0`, `__n1`, …
spanning the whole package. After `passNoInlineComponents`, two
instances of the same component get distinct ids — by design — but
codegen platforms ingesting the rendered IR have to trust the assignment
order matches what they later re-walk. See `lowering-migration.md:264-292`
finding #10 (three bubbletea walks expecting matching id sequences).

---

## 6. Public API & error recovery

### 6.1 `sngl.Check` doesn't gate on errors; caller uses `Convert(pkg)` regardless

**File:** `sngl.go:63-69`, `:55-59`.

`Check` returns `(*ir.Package, []Diagnostic)`. `Convert` accepts any
package. There's no `pkg.HasErrors` predicate; the caller must scan diags.
The CLI `checkDoc` (`cmd/sngl/discover.go:227-231`) returns the **first**
error and stops, **discarding the rest** — the user sees one error per
build instead of all of them. Should aggregate. Compare `internal/checker`
returns the full slice; the CLI throws away N-1.

### 6.2 `sngl.Lower` re-exports but `sngl.Optimize` doesn't exist

**File:** `sngl.go`.

Public surface exposes `Parse, Format, FormatTo, FormatExpr, FormatType, Check, Convert, Lower`. No `Optimize`. External callers (LSP, playground)
have to import `internal/optimize` directly — defeats the "stable
surface" claim of the file. Either add `sngl.Optimize` or remove
`sngl.Lower` from the public surface for symmetry.

### 6.3 `sngl.Parse` swallows lex errors and parse errors via `errors.Join`

**File:** `internal/parser/parse.go:15-53`, `sngl.go:21-33`.

Parser returns `(doc, errors.Join(lexErrs, parseErr, panicErr...))`.
Caller has to type-assert into `errors.Unwrap` chain to recover position
info. There's no `ParseError` type. Compare `Check` which returns a
structured `[]ir.Diagnostic` slice. Inconsistent.

### 6.4 Parser-panic-as-error masks unhandled grammar paths

**File:** `internal/parser/parse.go:42-48`.

A `recover()` converts panics in the AST builder into a generic
`"parser panic"` error. Means a missing grammar rule never surfaces as a
crash — it survives as an unhelpful error. Useful for production
robustness; counterproductive for development. Gate on a build tag /
env var to keep panics visible during dev.

### 6.5 Stdlib parse errors are silently swallowed

**File:** `internal/checker/stdlib.go:44-47`.

```go
doc, err := parser.Parse(e.Name(), data)
if err != nil {
	continue
}
```

A malformed `lib/*.sngl` causes the stdlib file to vanish from the
language — every user program then fails to find e.g. `text` or
`Color`. No diagnostic, no warning. Log at slog.Error or panic during
init.

### 6.6 `Check` resolves imports synchronously, recursively, no cycle telemetry

**File:** `internal/checker/checker.go:431-434`.

```go
if c.visited[imp.Path] {
	c.error("import cycle")
}
c.visited[imp.Path] = true
docs, err := c.cfg.Resolver.Resolve(c.cfg.FS, uri)
```

`visited` is set before the recursive `Check` runs and never cleared on
return. Means two siblings importing the same library — diamond import —
trigger the cycle diagnostic on the second sibling. Should be a set of
"currently being checked" packages, popped on return.

### 6.7 Error recovery between phases: phases don't gate on prior errors

`internal/checker/checker.go:60-66` runs pendingExtensions, pass2, async,
points-to, etc., regardless of pass1 diags. `cmd/sngl/compile.go:104-107`
gates compile on checkDoc errors. But `sngl.Check` returns the pkg even
on errors — and callers using the IR can crash. Document the partial-IR
contract or have downstream phases short-circuit when `len(diags) > 0`.

---

## 7. Other

### 7.1 Stdlib loading uses `sync.Once` — re-checks racy under per-config customization

**File:** `internal/checker/stdlib.go:14-52`.

`stdlibOnce` parses once. The *parse* is shared globally. But
`loadStdlib` is per-checker, so checking happens N times. If two
concurrent `Check` calls (LSP serves files in parallel) race on
`stdlibOnce.Do`, the second waits — but each then re-checks the same
docs into its own scope. Acceptable for correctness; wasteful for perf.

### 7.2 Test runner runs both pre-lower and post-lower with shared `pkg` — same multi-target bug

**File:** `cmd/sngl/run.go:127-149`, similar shape to `compile.go`.

If `sngl test` ever supports multi-target, hits the same multi-mutate
issue from #2.1.

### 7.3 `irLiteral` returns `nil` for unknown types — silently drops folded values

**File:** `internal/optimize/consteval.go:444-462`.

If a fold produces a Go value whose Go-type isn't in the switch (e.g., a
typed `int64` from an arithmetic op that exceeded `int`, or any
non-`(string,int,float64,bool,nil)` value from interpreter), `irLiteral`
returns nil. Caller in `foldExpr` checks `if lit := irLiteral(...); lit != nil` and falls back to `irFromValue` — which has its own coverage
gaps (no `map[string]any` vs `[]any` for `iter` types). Folded values
silently disappear back to the original expression. Diagnostic at least.

### 7.4 `iterate.go` `isStdlibComponentName` (codegen) heuristic

Cross-ref `lowering-migration.md` finding #15: codegen guesses
"is stdlib component" by checking first letter case / `sngl.` prefix.
Should be a field on `*ir.Component`. Cited here because it's a
phase-boundary issue: checker knows for sure (loaded from `lib/`),
codegen guesses.

### 7.5 No "phase: complete IR" tests

There is no test fixture asserting that after `Check + Optimize + Lower`,
the IR contains no `*ir.Ternary`, no `*ir.InterpolationExpr`-equivalent,
no `*ir.Toggle`, no `*ir.ContextRead`, etc. — for caps that should remove
them. `internal/lower/golden_test.go` likely covers individual passes;
add a "complete pipeline" assertion that scans the post-lower IR for
residuals matching the cap set.

### 7.6 Inter-phase data flow: globals & shared state

- `stdlibOnce`/`stdlibDocs` package globals in
  `internal/checker/stdlib.go:14-18`. Idempotent but global.
- `internal/optimize/optimize.go:34-53` introduces `optimizerRun` to
  carry cross-package state — good.
- `internal/lower/` passes share state only through `*ir.Package`. Good,
  no globals.
- Checker's `c.userMethods`, `c.constAsserts`, `c.pendingExtensions`,
  `c.optionsCache`, `c.mergedOptionsCache`, `c.platformScopeCache`,
  `c.stdlibOptions`, `c.windowType`, `c.replaces` — eight shared maps
  on the checker struct. Most are caches; `pendingExtensions` is
  cross-phase deferred work. Consider grouping into a `phaseState`
  sub-struct.

### 7.7 `ir.Normalize(c.pkg)` at end of `Check`

**File:** `internal/checker/checker.go:68`.

A blackbox normalization runs at the very end of checking. What does it
do? Worth documenting in CLAUDE.md as part of the public Check contract.
If it rewrites IR shape, downstream phases assume normalized input —
violations of that invariant aren't caught.
