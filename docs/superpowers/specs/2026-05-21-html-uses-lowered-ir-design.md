# HTML Codegen Uses the Lowered IR

Date: 2026-05-21

## Summary

Make HTML codegen consume the fully lowered IR — including the existing `passNoInlineComponents` pass — instead of running its own in-house component inlining with a parallel name-based dep tracker. Extend the lower passes so that NodeInsts targeting components inside reactive contexts or recursive cycles are flattened to a new `CreateComponent` intrinsic, mirroring how `passDeclarative` handles DOM nodes. After this work, HTML's `varRegistry`, `varSetToNames`, `remapMutated`, `dataRenames`, and `renderComponentInline` machinery all disappear; `g.exprDeps()` calls `dt.ExprDeps()` directly with pointer-keyed `*ir.Var` deps.

The principal motivation is unblocking issue #75's component-method desugaring re-enable — the HTML reactive paths break because the walker's pointer-keyed deps don't address HTML's promoted-name vars. Moving HTML to consume the lowered IR eliminates the impedance.

## Motivation

HTML's current architecture runs two layers in parallel:

1. **Walker layer** (post-#75-followup): `codegen/deps.go`'s `depExtractor` walks IR with `*ir.Var` pointer keys, including through method parameter bindings.
2. **In-house inlining layer** (`html.go`): `renderComponentInline` flattens user components into main inline, creating promoted-name string keys (`count$_0`, `name$_1`, …) that don't correspond to any `*ir.Var` pointer.

A bridge (`varRegistry` synthesizing fake `*ir.Var`s, `varSetToNames` flattening pointer deps to names, `remapMutated` applying `dataRenames` translation) reconciles the two. The walker can't see HTML's promoted names; HTML's renamer can't read the walker's pointers. Component methods desugared per issue #75 ride through this bridge, and reactive subscriptions for `this.x` reads don't reach the promoted-name slot — re-renders silently break.

`passNoInlineComponents` (`internal/lower/inline_components.go`) already does most of what HTML's in-house inliner does, with one critical difference: it produces **real `*ir.Var` pointers** for promoted vars (via `cloneVarShallow`). The walker walking the post-pass IR naturally produces pointer-keyed deps that address those real promoted vars. No bridge needed.

What `passNoInlineComponents` doesn't handle today: NodeInsts inside reactive For/If bodies, and NodeInsts targeting recursive (SCC-member) components. Static inlining fails for these because each runtime iteration / each recursion level needs its own component instance — one shared promoted-var slot won't do.

This spec closes that gap by extending the lower passes to emit a flat `CreateComponent` intrinsic for those cases, mirroring how `passDeclarative` already emits `CreateNode`/`AppendChild`/`AttachHandler` for DOM nodes. After the pass changes land, HTML opts in and deletes its parallel layer.

## Design

### Lower-pass extensions

**New intrinsic: `lower.CreateComponent`.**

```go
// ir/intrinsics.go — appended to LowerIntrinsics
{Name: "CreateComponent", Params: []*Param{
    {Name: "comp",  Type: TypDyn}, // *ir.Ident whose Sym is *ir.Component
    {Name: "props", Type: TypDyn}, // *ir.StructLit, anonymous, keyed by prop name
}, Return: TypDyn},
```

The intrinsic mirrors `CreateNode` in lifecycle: it produces a handle. Existing `AppendChild`/`RemoveChild` accept it. Event handlers are owned by the component internally (defined inside its body, wired by the factory) — no extra `AttachHandler` call at the instantiation site.

Slot children are explicitly out of scope; the intrinsic stays 2-arg. Slot-bearing components in reactive or recursive contexts continue to error out or fail to render — flagged as a follow-up.

**`passNoInlineComponents` change.**

The pass walks `*ir.For` bodies recursively today and would happily inline NodeInsts found inside; this corrupts state because the inlined `var n__inst0 = 0` declares a single slot that every iteration shares. Detect this and skip:

- Thread an `inReactiveContext` flag through `inlineCompState.inlineStmts`. Set to true when entering an `*ir.If` whose Cond depends on reactive vars, or an `*ir.For` whose Iter does. (Existing reactivity analysis already identifies "reactive" If/For — reuse it. If not yet available before this pass runs, treat any If/For as reactive; conservative.)
- When `inReactiveContext` is true AND the NodeInst targets a user component, do NOT inline. Leave the NodeInst in place. Add the target component to `keep`.
- Same treatment for NodeInsts whose `Component` is in the recursive-cycle set: leave in place, retain the component.

The pass's existing run-to-fixed-point loop still terminates: skipped NodeInsts don't trigger re-iteration.

**`passReactivity` change.**

When `passReactivity` synthesizes a `__renderSlotN` Func from a reactive For/If body, the body's statements are walked and lowered to imperative form (via `passDeclarative`-style lowering applied per-slot). NodeInsts inside the slot body that target a component (which `passNoInlineComponents` left in place) get lowered to:

```
__h = lower.CreateComponent(<componentRef>, <propsStruct>)
lower.AppendChild(<parentRef>, __h)
```

where `<componentRef>` is an `*ir.Ident{Sym: <comp>}` and `<propsStruct>` is an `*ir.StructLit` built from the NodeInst's `Props` field. `<parentRef>` is the slot's parent expression (existing slot-lowering already tracks this).

**`passDeclarative` change.**

For static-position component NodeInsts left in place (recursive cycle targets called from the top-level tree), `passDeclarative` emits the same `CreateComponent` + `AppendChild` pair. Today the pass handles `n.Component != nil` by error or by panic in some paths; switch that branch to emit the intrinsic.

### HTML codegen migration

**Required caps** (`codegen/platform/html/html.go:55`):

```go
return lower.Caps{
	NoContext:          true,
	NoReactivity:       true,
	NoAsyncReactive:    true,
	NoStdlibWrappers:   true,
	NoInlineComponents: true, // NEW
}
```

The lower pipeline now produces a flat `main` whose `Vars` contains promoted-name `*ir.Var` pointers per inlined instance. Recursive and reactive-loop components remain as separate `*ir.Component`s in `pkg.Components`.

**Deletions from `html.go`:**

- `renderComponentInline` and all call sites.
- `dataRenames` field + threading + the `inlinedStateInits` accumulator.
- The "promote" branch in `nodeFromIRCallStmt` that synthesized suffixed names.
- `varRegistry`, `newVarRegistry`, `varSetToNames`, `remapMutated`.
- All "promoted name" / suffix counter logic in the inliner.

Approximately 300 lines.

**Rewritten `exprDeps`:**

```go
func (g *htmlGen) exprDeps(expr ir.Expr) map[*ir.Var]struct{} {
	return g.dt.ExprDeps(g.currentComp, expr)
}
```

`g.currentComp` is set to `mainIRComponent(g.pkg)` during top-level rendering and to the appropriate enclosing component during slot/window emission. Updater / handler structs (`updateFunc.deps`, `eventHandler.mutated`, `timerDef.mutated`) become `map[*ir.Var]struct{}` end-to-end. No name conversion at the HTML layer.

**JS scope emission:**

```go
for _, v := range main.Vars {
	g.scope.ModelFields[v.Name] = true
	renames[v.Name] = "state." + v.Name
}
```

One iteration, one map. The translator (`codegen/lang/javascript/translate_ir.go`) emits `state.<name>` for any ident matching `ModelFields`; the lookup is name-keyed because JS output is name-keyed, but the dep tracking that drives WHICH updaters fire is pointer-keyed.

**Component factory emission.**

For each `*ir.Component` remaining in `pkg.Components` after lowering (recursive cycles + reactive-loop targets), emit a JS factory:

```js
function __cf_<sanitizedName>(props) {
    const state = {
        <varA>: <initA>,
        <varB>: <initB>,
        ...
    };

    // Lowered component body as imperative DOM ops.
    // setters / subscriptions / event handlers all close over `state`.
    const __root = document.createElement(...);
    // ...
    return __root;
}
```

The factory body is generated by running the existing HTML render path against the component's own lowered IR, with `state.<name>` substituted for component-var references. Props are read from the `props` argument.

**`CreateComponent` intrinsic translation** in `codegen/lang/javascript/translate_ir.go`:

```go
case "lower.CreateComponent":
    compIdent, _ := call.Args[0].Value.(*ir.Ident)
    comp, _ := compIdent.Sym.(*ir.Component)
    propsJS := translateIRExpr(call.Args[1].Value, scope)
    return factoryName(comp) + "(" + propsJS + ")"
```

`factoryName(comp)` returns `"__cf_" + sanitize(comp.Name)`. The matching factory emission step uses the same function so the names line up.

### Testing

**Driver fixtures** (`testdata/`):

- `test_html_loop_component.sngl` — user `card` component used inside `for x = items`. Items list mutates: instances created/destroyed; per-instance state preserved across re-renders that don't touch that index.
- `test_html_recursive_component.sngl` — tree-shaped component instantiating itself. Compiles, renders, expands on click.
- `test_html_method_reactivity.sngl` — exercises issue #75: `func main.bigger() => this.x > this.y` consumed by `text(value=bigger())`. Mutating `x` re-renders. Validates walker + lowered IR end-to-end.
- `test_html_two_instances_isolated.sngl` — two `counter()` instances; bumping one doesn't bump the other. Confirms per-instance state isolation without the in-house suffix mechanism.

**HTML browser tests** (`codegen/platform/html/*_browser_test.go`): existing TodoApp, FullExample, FullFixture must keep passing. Regression backstop.

**Lower-pass unit tests:**

- `internal/lower/inline_components_test.go` — cases for the new detection rules: NodeInst inside For, NodeInst targeting recursive component. Verify the NodeInst is left in place and the target component is retained in `pkg.Components`.
- `internal/lower/reactivity_test.go` — case: reactive For body containing a NodeInst → emits `CreateComponent` + `AppendChild` in `__renderSlotN`.
- `internal/lower/declarative_test.go` — case: static-position NodeInst targeting a recursive component → emits `CreateComponent` + `AppendChild`.

**Smoke test #75 re-enable.** Final task: re-enable `registerNestedMethods` for components in `internal/checker/checker.go`. Run full suite. Component methods now ride through the lowered IR cleanly.

## Constraints

- Slot-bearing components used in reactive or recursive contexts are out of scope. Inlined-static usage continues to work via `passNoInlineComponents`.
- Per-instance prop updates use re-instantiation (existing SNGL model), not factory setter methods.
- The pass-ordering invariant must hold: `NoInlineComponents` runs before `NoReactivity`, which runs before `NoDeclarative`. Verify `internal/lower/lower.go` already does this.
- Bubbletea / Fyne / Android / GTK4 do not opt into `NoInlineComponents` here; their migration is a separate spec each.

## Errors

- A slot-bearing component used inside a `for` or in a recursive cycle: compile-time error from the lower pass, message pointing at the follow-up issue.
- An attempt to use `CreateComponent` against a non-component target (shouldn't happen via the lower pass; defensive): error in JS translator.

## Risks & mitigations

- **Slot-bearing components in reactive contexts.** Pre-scan fixtures to identify any that exist today; if so, expand scope or skip those tests with a tracked TODO.
- **Recursive components without existing test coverage.** Add one driver fixture (`test_html_recursive_component.sngl`); validate browser-side that recursion terminates and per-level state isolates.
- **JS translator + factory-name coupling.** Single `factoryName(comp)` helper exported from a shared file so emit and translate use the same convention.
- **Pass-ordering regressions on other platforms.** Other platforms don't opt into `NoInlineComponents`, so the new emission paths in `passNoInlineComponents` only fire when HTML compiles. Existing platforms' compiles produce identical pre-pass IR to today.
- **`pkg.Components` iteration for factory emission.** New emission step; missing it silently produces `__cf_<name>` JS references that are undefined at runtime. Browser tests will surface this.

## Out of scope

- Slot-bearing components in reactive or recursive contexts.
- Bubbletea / Fyne / Android / GTK4 migration to the same model.
- Component lifecycle hooks (`onMount`/`onDestroy`).
- Per-instance prop updates via factory setter methods.
- Tree-shaking unused components from `pkg.Components`.

## Plan summary (detailed plan to follow via writing-plans)

1. Add `lower.CreateComponent` intrinsic to `ir/intrinsics.go`.
2. Extend `passNoInlineComponents` to detect reactive-context / recursive-cycle NodeInsts and leave them in place.
3. Extend `passReactivity` to emit `CreateComponent` + `AppendChild` for NodeInsts in `__renderSlotN` bodies.
4. Extend `passDeclarative` to emit `CreateComponent` + `AppendChild` for static-position recursive NodeInsts.
5. Add lower-pass unit tests for all three pass changes.
6. Add HTML driver fixtures.
7. Switch HTML to opt into `NoInlineComponents`.
8. Delete HTML's in-house inlining + bridge layer.
9. Rewrite `exprDeps` to call `dt.ExprDeps` directly with pointer-keyed deps.
10. Emit JS factory functions for remaining `*ir.Component`s.
11. Add `CreateComponent` translation to `codegen/lang/javascript/translate_ir.go`.
12. Verify with browser tests; re-enable issue #75's component-method desugaring as the final smoke test.
