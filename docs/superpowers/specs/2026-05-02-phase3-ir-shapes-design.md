# Phase 3 Lowering Passes — IR Shape Design

**Date:** 2026-05-02
**Status:** Shipped (passReactivity, passTimer, passDeclarative)
**Tracks:** gitlab issues #41 (NoReactivity), #42 (NoTimer), #43 (NoDeclarative)

## Summary

The three Phase 3 lowering passes (NoReactivity, NoTimer, NoDeclarative) all need a way to express "do this side effect at this point in the program" in lowered IR. This spec picks the shape: **a stdlib `lower.*` namespace of native intrinsic functions, plus reuse of existing `*ir.Assign` for reactive updates**. Lowering passes emit ordinary `*ir.Call` to the resolved intrinsic funcs; codegen platforms translate the intrinsics through the same path used today for `int.min`, `string.upper`, and similar builtins.

## Motivation

Phase 1+2 of the lowering layer (`docs/superpowers/specs/2026-05-02-codegen-lowering-design.md`) shipped value-replacement passes (NoToggle, NoEnum, NoUnit, NoTernary) plus NoComputed. The remaining passes can't follow the same pattern — they need to express side effects that current SNGL surface doesn't have a syntax for: scheduling a timer, creating a node imperatively, attaching a handler. Several IR-shape options were considered (synthesized magic-name CallStmts, dedicated new IR stmts, a single `LowerOp` envelope). The chosen approach reuses the existing native-intrinsic mechanism (`*ir.Func.Intrinsic`), keeping the IR surface unchanged.

## Architecture

### Primitive set (`lib/lower.sngl`, all marked Intrinsic)

```
// Tree construction (used by NoDeclarative).
func lower.createNode(tag string) <ElementRef>            // returns the node handle
func lower.appendChild(parent <ElementRef>, child <ElementRef>)
func lower.removeNode(node <ElementRef>)
func lower.attachHandler(node <ElementRef>, event string, handler func())

// Timer scheduling (used by NoTimer).
func lower.scheduleTimer(id int, intervalMs int, handler func())
func lower.cancelTimer(id int)
```

**Reactive updates (NoReactivity output) use no intrinsic.** A reactive update is a plain `*ir.Assign` whose Target is a `*ir.Select` against a synthesized node handle: `__n0.value = string(n)`. This reuses existing AssignStmt + Select IR.

### Node handles

Each visual node lowered by NoDeclarative becomes a synthesized `*ir.LocalVar` named `__nN` (sequential, package-scoped counter). The Var's Type is the original component type — `*ir.Type{Kind: TypeComponent, Decl: <originalComponentDecl>}` — so a downstream `Select` against the handle resolves to the original component's prop set, matching how today's `#id` references work.

The Var's `Init` is a `*ir.Call` to `lower.createNode("<tagName>")`. Subsequent prop reads/writes against `__nN` use ordinary `Select` and `Assign`.

### Timer handles

`*ir.Timer` decls are replaced by:
- A synthesized `*ir.Func` named `__timerN_handler` containing the original `Timer.Handler.Block`.
- A `lower.scheduleTimer(N, intervalMs, __timerN_handler)` call in the owning component/window body.
- An `*ir.If` wrapping the schedule call when `Timer.Enabled` was set; NoReactivity's mutation handling for the Enabled var emits the matching `lower.cancelTimer(N)` when the gate flips.

Timer IDs are ints, matching the existing `codegen.TimerInfo.Index` convention.

### Conditionals and loops

`*ir.If` and `*ir.For` inside a visual tree stay as IR stmts after NoDeclarative. The pass recurses into their bodies, emitting createNode/appendChild calls inside each branch / iteration. Codegen wraps the if/for with whatever runtime bookkeeping the target platform needs (DOM diffing, list reconciliation, etc.).

NoDeclarative does not invent runtime `lower.if` / `lower.for` primitives.

### Codegen contract

When a platform's `Capabilities()` enables a Cap that emits intrinsics, that platform's `LangTranslator` must handle the corresponding intrinsic IDs. The dispatch follows the existing pattern (`codegen/lang/golang/golang.go`'s `goBuiltinMethodFromArgs`):

```go
switch intrinsicID {
case "LowerCreateNode":
	return emitCreateNode(args)
case "LowerAppendChild":
	return emitAppendChild(args)
	// ...
}
```

When a Cap is on but its required intrinsic returns empty (unhandled), codegen panics with `unimplemented intrinsic: Lower<Name> (required by Cap=No<Cap>)`. This forces platforms to fail loudly when they enable a Cap they haven't fully wired.

A contract test in `internal/lower/` registers, for each Cap, the set of intrinsics it can emit. End-to-end fixtures with caps turned on against a fake `LangTranslator` verify every listed intrinsic is visited.

## Per-pass behavior

### NoReactivity

Runs before NoTimer and NoDeclarative.

1. **ID synthesis.** Walk every `*ir.NodeInst`. For every node whose props reference a reactive var (or that has any inline event handler that reads/writes reactive state), assign a synthesized id `__nN` and prepend a `LocalVar{Name: "__nN", Type: <component type>, Init: nil}` declaration in the node's parent block. (NoDeclarative will later replace the Init with a `lower.createNode` call; in NoReactivity's intermediate state the Init is nil.)

2. **Dataflow analysis.** Build `mutated_var → set of (node_id, prop_key, propExpr)`. Reuse `codegen/analysis.go` + `codegen/deps.go` logic: copy the relevant functions into `internal/lower/reactivity.go` per the master spec's "copy + delete" migration.

3. **Mutation injection.** Walk every mutation site (`*ir.Assign`, after Phase 2's NoToggle has converted any toggles). After each, insert `*ir.Assign` stmts of the form `__nN.<key> = <propExpr>` for every (node, prop) pair affected by the mutation. Computed deps (resolved by NoComputed before NoReactivity runs) flow through naturally because NoComputed inlined them.

NoReactivity is timer-agnostic. Timer-Enabled mutation handling is NoTimer's job (next section).

### NoTimer

Runs after NoReactivity.

1. Walk `pkg.Timers` and `Component.Timers` (and `Window.Timers` if present).
2. For each `*ir.Timer{Interval, Enabled, Handler}`:
   - Allocate `id := <sequential int>`.
   - Synthesize `*ir.Func{Name: "__timerN_handler", Block: Timer.Handler.Block}` and append to the owning Funcs list.
   - Emit a schedule call as a `*ir.CallStmt` wrapping `lower.scheduleTimer(id, intervalMsLiteral, __timerN_handler)` at the top of the owning body. When `Enabled` is set, wrap in `*ir.If{Cond: Enabled, Body: [scheduleCall]}`.
3. Clear the `Timers` slice from each owner.
4. **Enabled-gating mutation handling.** When `Enabled` was set, walk every `*ir.Assign` whose Target identifier resolves to the Enabled Var. Inject after each:
   - `if newValue { lower.scheduleTimer(id, intervalMs, __timerN_handler) } else { lower.cancelTimer(id) }`
     This runs after NoReactivity has already injected its node-update Assigns, so the timer fires/stops at the correct point in the mutation sequence.

The intervalMs literal is computed at this pass: NoUnit (which ran in Phase 2) already collapsed `500ms` to `500`. No further conversion needed.

### NoDeclarative

Runs last.

1. Walk every `*ir.NodeInst` in the visual tree, recursing into children.
2. For each node:
   - Allocate id `__nM` (or reuse the id NoReactivity assigned earlier).
   - Synthesize `LocalVar{Name: "__nM", Type: <component type>, Init: <Call to lower.createNode>}` (overwrites any existing nil Init from NoReactivity).
   - Emit `*ir.Assign` stmts for each prop: `__nM.<propKey> = <propExpr>`.
   - Emit `*ir.CallStmt` to `lower.attachHandler(__nM, "<event>", __nM_<event>_handler)` for each event handler — first lifting each handler block to a synthesized package-level `*ir.Func` (handler funcs do not need NoLambda treatment because they are not closures over outer scope after NoReactivity has rewritten captures into explicit reads).
   - Recurse into children. After each child's id `__nC` is bound, emit `lower.appendChild(__nM, __nC)`.
3. The original `*ir.NodeInst` tree is replaced with this flat sequence of LocalVar + Assign + Call stmts in the component body.
4. `*ir.If` and `*ir.For` inside a visual tree are preserved; NoDeclarative recurses into their bodies.

For-loops driving repeated nodes need keying so NoReactivity's updaters target the right iteration's node. The synthesized `__nN` is the *base name*; per-iteration nodes are `__nN_${iterIdx}`. Phase 3b's implementation plan must specify the exact keying scheme before NoDeclarative ships.

## Migration order

Within Phase 3:

1. **Phase 3b — NoReactivity.** Standalone-testable: output uses only existing IR shapes. No `lib/lower.sngl` required yet.
2. **Phase 3c — NoTimer.** Adds the first two intrinsics (`scheduleTimer`, `cancelTimer`) to `lib/lower.sngl`. Walks `Timer.Enabled` mutation sites to inject schedule/cancel pairs. Goldens verify rewrite; no codegen platform turns Cap on yet.
3. **Phase 3d — NoDeclarative.** Adds four more intrinsics. Largest scope. Coordinates `__nN` ID allocation with what NoReactivity assigned.

After Phase 3d, all 9 lowering passes have working implementations. Phases 4–6 of the master spec then port HTML / Fyne / etc. onto lowered IR and delete the legacy MutationModel.

NoLambda (issue #40) remains separately scoped; its output shape is independent of these three.

## Risks

1. **`codegen/analysis.go` + `deps.go` extraction.** ~700 lines of analysis tightly coupled to MutationModel. Phase 3b copies the dataflow logic, leaving MutationModel-specific shapes behind. Cleanup of the originals happens in master-spec Phase 6 after HTML/Fyne port.

2. **Synthesized LocalVar Sym.** Phase 2's NoTernary already left synthesized Idents with nil Sym. Phase 3 makes the gap blocking — every `__nN` Ident needs a Sym to resolve `__nN.value` correctly. Plan: synthesize a proper `*ir.Var` Symbol per LocalVar and link via Sym at synthesis time.

3. **For-loop node identity.** Per-iteration `__nN_<iterIdx>` keying needs a runtime way to compute `<iterIdx>`. Plan: NoDeclarative emits a Binary string-concat expression `"__nN_" + string(iterVar)` as the third arg of `lower.createNode` (extending the createNode signature to `lower.createNode(tag string, key string)` if needed). Phase 3d's plan finalizes the keying API.

4. **Conditional node lifecycle.** When an `if` flips false at runtime, the previously-created subtree should be removed. NoDeclarative emits createNode/appendChild inside each branch; the *removal* on branch flip is platform-specific. Document as platform contract: the platform's intrinsic implementations track conditional-branch lifecycle, possibly via an additional `lower.removeSubtree` primitive added in Phase 3d if no platform can manage without it.

5. **Handler closures.** Lifted `__nM_<event>_handler` funcs reference component-scoped vars by ordinary Ident — same scope rules as before lifting. Codegen for non-closure target languages needs NoLambda to land first; for closure-supporting langs (Go, JS, Kotlin) the lift is a no-op rename.

## Open items resolved during brainstorm

- **IR shape** — native intrinsics in stdlib `lower.*` namespace (existing Intrinsic mechanism).
- **Node handle type** — original component type via ElementRef (matches today's `#id` ref behavior).
- **Reactive update output** — plain `Assign` with `Select` target, no intrinsic call.
- **Pass ordering** — NoReactivity → NoTimer → NoDeclarative within Phase 3.
- **Conditionals/loops in tree** — kept as `*ir.If` / `*ir.For` IR; NoDeclarative recurses without inventing loop primitives.

## File-level changes (preview)

- **New**: `lib/lower.sngl` declaring six intrinsic primitives.
- **New**: `internal/checker/stdlib.go` (extension) — recognize `lib/lower.sngl` and register its funcs as native intrinsics with the `Lower<Name>` IDs.
- **Modified**: `internal/lower/reactivity.go` — replace stub with copy-port of `codegen/analysis.go` + `codegen/deps.go` analysis + injection logic.
- **Modified**: `internal/lower/timer.go` — replace stub.
- **Modified**: `internal/lower/declarative.go` — replace stub.
- **New**: per-pass txtar fixtures in `internal/lower/testdata/` (reactivity_*, timer_*, declarative_*, plus composition fixtures).
- **No changes** to `codegen/codegen.go`, `internal/lower/lower.go`, `internal/lower/caps.go` — the existing plumbing carries Phase 3 unchanged.
