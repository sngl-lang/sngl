# Reactivity Lowering Consolidation

## Background

SNGL has a lowering layer (`internal/lower`) that rewrites high-level IR into
simpler primitives gated by `Caps` flags. One of those passes, `NoReactivity`
(`internal/lower/reactivity.go`), is supposed to be the single owner of
reactive dispatch: when a tracked reactive `Var` is mutated, the pass splices
explicit updater statements at the mutation site so codegen never has to
re-derive reactivity.

The pass currently covers only one kind of reactive update: **prop
reactivity** — changes to a `NodeInst.Props` value when one of its dependent
vars mutates. It does not cover **structural reactivity** — changes to the
tree shape when an `If.Cond` or `For.Iter` dependent var mutates.

Each MutationModel platform (fyne, gtk4, html) has filled the gap with its
own parallel reactive pipeline: a `updaters` / `addUpdater` registry plus a
`codegen.FindAffected(depTracker, updaters, mutated)` dispatch that runs at
each mutation site. fyne and gtk4 also kept the prop-side machinery alongside
their newer `lateReactive` / `resolveReactiveTokens` consumers of the
lowered Assigns, so two parallel reactive systems coexist per platform. html
goes further: it infers reactive bindings from handler shape
(`@input(e) { name = e.value }` got auto-bound to two-way `value=name`),
producing surprising behavior (input boxes spontaneously populated from state
even when the source assigns nothing).

The recent fix (commit `18208e8`) removed the `@input`-shape inference in
html and routed reactive `value` props through the same lowered-Assign path
the rest of the renderer uses. This design consolidates that direction
across all three MutationModel platforms and extends `NoReactivity` to be
the single owner of both prop and structural reactivity.

## Goals

1. **Single source of truth for reactivity.** Every reactive update — prop
   or structural — is emitted by `passReactivity` as explicit IR. Platforms
   become translators of intrinsic calls and Assign statements, not dep
   inference engines.
2. **No regressions to static-site generation.** html keeps its declarative
   initial-HTML emitter. Pages with zero reactive deps emit zero JS.
3. **Preserve the post-lowering AST round-trip invariant.** `ir.Convert` on
   lowered IR must produce a `*ast.Document` that re-parses and re-checks.
4. **One coherent end state.** No phased intermediate where the cleanup
   work is half-done; all three MutationModel platforms land on the new
   contract within a single set of changes.

## Non-goals

- Two-way binding syntax (`:value=name`). Deferred to a separate spec.
- Keyed for-loop diffing. The new pass tears down and rebuilds all children
  when an iter dep changes. Existing fyne / gtk4 `updateFor` machinery does
  the same today; this is no regression.
- Converting android / bubbletea (RenderModel platforms) to use `NoReactivity`.
  Their frameworks handle reactivity via re-render-from-state; they remain
  `Caps{}`.
- General audit of *other* lowering passes (NoLambda, NoTernary, etc.) that
  android / bubbletea / none might benefit from. Tracked separately.

## End state

| Platform   | Render model    | `NoReactivity` | `NoDeclarative` |
|------------|-----------------|----------------|-----------------|
| android    | RenderModel     | off            | off             |
| bubbletea  | RenderModel     | off            | off             |
| none       | interpreter     | off            | off             |
| fyne       | MutationModel   | **on**         | **on**          |
| gtk4       | MutationModel   | **on**         | **on**          |
| html       | MutationModel   | **on**         | off             |

After this work:

- **fyne, gtk4** consume a flat lowered stream of `lower.createNode`,
  `lower.appendChild`, `lower.removeChild`, `lower.attachHandler`, plus
  property Assigns on element refs. One generic intrinsic dispatcher per
  platform translates each form to native API calls.
- **html** keeps its tree-walking initial-HTML emitter for the static-site
  path. The reactive-update layer (JS) consumes the same lowered Assigns
  and `lower.*` calls as fyne / gtk4 but emits JS DOM calls. When no
  reactive var touches the document, no JS block is emitted.
- **No platform** maintains a parallel `updaters` registry or
  `FindAffected` dispatch.

## Design

### 1. Lowering changes (`internal/lower`)

#### 1.1 New intrinsic: `lower.removeChild`

Add a fourth intrinsic alongside the existing trio defined in
`internal/lower/declarative.go`:

```go
lower.removeChild(parent dyn, child dyn) → void
// Intrinsic: "LowerRemoveChild"
```

Used by the structural-reactivity machinery in `passReactivity` to tear
down children before re-creating them.

#### 1.2 `lower` intrinsic namespace

The synthetic `*ir.Func` values today named `lower.createNode`,
`lower.appendChild`, `lower.attachHandler` round-trip through `ir.Convert`
as `ast.IdentExpr{Name: "lower.createNode"}` — which the parser then sees
as `Select(Ident("lower"), "createNode")`, and the checker rejects because
no `lower` symbol is in scope. The round-trip-to-AST invariant is broken
for any lowering pass that emits these intrinsics.

Restore the invariant by following the same pattern existing intrinsic
namespaces use (`internal://alert`, `internal://file`):

1. Add a `LowerIntrinsics` list to `ir/intrinsics.go`:
   ```go
   var LowerIntrinsics = []IntrinsicDef{
       {Name: "CreateNode", Params: []*Param{{Name: "tag", Type: TypString}}, Return: TypDyn},
       {Name: "AppendChild", Params: []*Param{{Name: "parent", Type: TypDyn}, {Name: "child", Type: TypDyn}}, Return: TypVoid},
       {Name: "RemoveChild", Params: []*Param{{Name: "parent", Type: TypDyn}, {Name: "child", Type: TypDyn}}, Return: TypVoid},
       {Name: "AttachHandler", Params: []*Param{{Name: "node", Type: TypDyn}, {Name: "event", Type: TypString}, {Name: "handler", Type: TypDyn}}, Return: TypVoid},
   }
   ```
   Wire it into `LookupIntrinsic` alongside the other lists.

2. In `internal/checker/checker.go` (around the existing `internal://alert`
   / `internal://file` cases ~ line 302-306), add an `internal://lower`
   case that calls `buildIntrinsicsPkgFrom(ir.LowerIntrinsics)` with alias
   `lower`. The import is added unconditionally to every checked package
   (like alert / file are today) so any lowering-produced `lower.*` call
   resolves at re-check time.

3. Update `internal/lower/declarative.go` to use these IntrinsicDef-derived
   symbols rather than hand-constructed `*ir.Func` values with names
   containing `.`. The `*ir.Func` for each intrinsic now lives on the
   `lower` package's namespace, and `ir.Call.Receiver` is set to the
   namespace ident — which matches the AST shape `convertCallExpr`
   already handles at `convert.go:622-628` (the namespaced-call path).

4. `passReactivity` does the same when it synthesizes `lower.removeChild`
   and `lower.appendChild` / `lower.createNode` / `lower.attachHandler`
   calls inside `__renderSlot<N>` Funcs.

After this, lowered IR round-trips cleanly through parse + check.

#### 1.3 Extend `passReactivity` to cover structural deps

Today `passReactivity`:

1. Walks every `NodeInst` and records `reverseDeps[var] → [reactiveProp{NodeID, Key, Expr}]`.
2. After each `Assign{Target: <reactive Var>}`, splices a synthetic
   `Assign{Target: #__nN.<key>, Value: Expr}` for every prop in
   `reverseDeps[var]`.

Extend it with structural coverage:

1. Walk every `*ir.If` and `*ir.For` and record `reverseDeps[var] →
   [reactiveSlot{SlotID, ParentRef}]` for every reactive var in
   `If.Cond` / `For.Iter`. Assign a synthetic ID `__slot<N>` per reactive
   If/For.
2. For each `__slot<N>`, synthesize:
   - A `*ir.Var` `__slot<N> list<dyn>` added to the owning
     component / window's `Vars` slice (private, no AST source).
   - A `*ir.Func` `__renderSlot<N>(parent dyn) void` added to the owning
     component / window's `Funcs` slice. Body:
     - For each entry `c` in `__slot<N>`: `CallStmt lower.removeChild(parent, c)`.
     - `__slot<N> = []dyn{}`.
     - Re-evaluate `Cond` (for If) or iterate `Iter` (for For):
       - For each surviving child NodeInst in the body, emit the same
         create / setProp / attachHandler / appendChild sequence that
         `passDeclarative` would emit inline, with `parent` as the
         current arg.
       - Append each created node ref to `__slot<N>`.
3. Replace the original `*ir.If` / `*ir.For` in its parent stmt sequence
   with `CallStmt __renderSlot<N>(#parentRef)`, anchored at the same
   position so initial render happens in source order.
4. After each `Assign{Target: <tracked Var>}`, splice
   `CallStmt __renderSlot<N>(#parentRef)` for every slot in
   `reverseDeps[var]`, in addition to the prop Assigns already spliced.

For non-reactive `If` / `For` (no reactive deps in Cond/Iter), the shape is
left intact. `passDeclarative` (when enabled) flattens the body inline as
it does today.

Both prop and structural reactivity share `reverseDeps[var]`, so a single
mutation triggers all relevant updaters in one walk. No new `Caps` flag —
`Caps.NoReactivity` gates both behaviors.

#### 1.4 Pass ordering

Unchanged from `internal/lower/lower.go:37`:

```
passUnit, passEnum, passTernary, passAsyncReactive, passComputed, passLambda,
passToggle, passReactivity, passTimer, passDeclarative, passNoRef
```

`passReactivity` must run before `passDeclarative` so the synthetic
`__renderSlot<N>` Funcs and the If/For removal happen before flattening;
the slot's generator-Func body is then flattened by `passDeclarative` along
with the rest.

#### 1.5 Round-trip preservation

Once the `lower` namespace exists, every intrinsic call in lowered IR
round-trips cleanly. Add a fuzz target `FuzzLoweredDocument` next to
`FuzzDocument` in `fuzz_test.go` that:

1. Parses + checks a seed input.
2. Runs `lower.Lower(pkg, allMutationModelCaps, lower.Options{})` where
   `allMutationModelCaps = {NoReactivity: true, NoDeclarative: true,
   NoLambda: true, ...}` covering every pass that emits new IR.
3. Calls `ir.Convert(pkg)` and reformats the result.
4. Re-parses + re-checks the formatted source and asserts no diags.

Failures here prove that some pass emits an IR shape `ir.Convert` cannot
represent in valid AST.

### 2. Codegen changes

#### 2.1 Shared intrinsic walker

New file `codegen/intrinsic_walker.go`. Defines:

```go
type IntrinsicTranslator interface {
    OnCreateNode(id, tag string) string
    OnAppendChild(parent, child string) string
    OnRemoveChild(parent, child string) string
    OnAttachHandler(node, event, handlerRef string) string
    OnPropAssign(nodeID, prop string, valueExpr ir.Expr) string
}

func WalkLowered(stmts []ir.Stmt, t IntrinsicTranslator) string
```

`WalkLowered` recurses over the lowered stmt sequence (LocalVar, CallStmt
to `lower.*` intrinsics, Assign on element-ref selects, plain Assign,
If/For/PlatformFilter, etc.) and dispatches each form to the translator,
threading the platform-specific code into a single output buffer.

fyne, gtk4, and html each implement `IntrinsicTranslator` once. Their
existing prop-translation logic (e.g. fyne `nodeBindingInfo` setter
synthesis, html `exprToJS` for value/textContent) feeds into `OnPropAssign`.

#### 2.2 fyne conversion

**Remove:**

- `codegen/platform/fyne/view_ir.go` — `renderStmt`, `renderNode`,
  `renderText`, `renderIf`, `renderFor`, `renderInput`, `renderCheckbox`,
  `addUpdater`, `irWidgetUpdater`, `lateReactive`,
  `resolveReactiveTokens`, `nodeBindings`, `recordNodeBinding`,
  `localMode` / `withLocalMode` (slot Funcs scope locals naturally).
- `codegen/platform/fyne/compiler_ir.go:166-216, 280-289, 340, 358, 378,
  482-509` — `updaters []irWidgetUpdater` accumulation,
  `codegen.FindAffected` call sites, `emitIRUpdaters`.
- `codegen/platform/fyne/scaffold.go:67, 91, 115` — `UpdaterNames`,
  `AffectedUpdaters`, `doRefresh()` template emission.

**Keep:**

- `irWidgetField` registry + Model struct emission. The intrinsic walker
  registers a field on each `OnCreateNode`.
- Blueprint / `entrySync` bindParam handling. Two-way input sync between a
  blueprint component's bindParam and the underlying widget is a separate
  concern from reactivity; left as-is for now.
- Runtime helpers, scaffold harness, runtests.

**Add:**

- `fyneIntrinsicTranslator` implementing the interface. Per-tag widget
  table (vbox → `container.NewVBox()`, text → `widget.NewLabel("")`, etc.).
  `OnPropAssign` dispatches via a per-tag setter table
  (text.value → `.SetText(...)`).

Enable `NoReactivity + NoDeclarative` in fyne's `Capabilities()`.

#### 2.3 gtk4 conversion

Mirror of fyne. Same removals (`vc.updaters`, `FindAffected`,
`buildReactiveRefresh`, `/*REACTIVE_REFRESH*/` template slot, `UpdaterNames`,
`localWidgets`/`localCount`/`localMode`). Intrinsic translator emits
GTK4 C-bridge calls (`gtk_label_new`, `gtk_label_set_text`, `gtk_box_append`,
`gtk_box_remove`, `g_signal_connect`).

Enable `NoReactivity + NoDeclarative` in gtk4's `Capabilities()`.

#### 2.4 html conversion

**Keep (the static-site path):**

- The tree-walking initial-HTML emitter: `renderIR*`, `renderStaticInput`,
  `renderStaticCheckbox`, style emission, child recursion. This is what
  produces the declarative HTML body.
- HTTP route mode, preview / LSP / snapshot infrastructure.
- `data-sngl-id="__nN"` markers (lowering already assigns `__nN`; the
  renderer just propagates).
- Pages with zero reactive vars produce zero JS, end of file.

**Remove:**

- `codegen/platform/html/html.go` — `g.updates` registry, `updateFunc`,
  `findAffectedUpdaters` (`~:2931`), all `g.updates = append(...)` sites,
  `g.dt` depTracker.
- `loweredID` (the two-systems-in-parallel distinction goes away).
- The "Reactive props: set via JS updaters" loop at `html.go:2156-2186` that
  re-derives `IRIsReactive` from prop exprs.
- `codegen/platform/html/ir_helpers.go:nodeIsReactive`.

**Add:**

- `htmlIntrinsicTranslator` running after initial-HTML emission. It walks
  the same component body and emits JS for:
  - `Assign{Target: Select{IsElementRef Ident, Field}}` →
    `${nodeID}.${field} = ${jsExpr};` placed in the surrounding handler /
    setter / kicker body.
  - `CallStmt lower.removeChild / createNode / appendChild / attachHandler`
    → DOM JS equivalents. These appear inside `__renderSlot<N>` Funcs.
  - Initial-position `CallStmt __renderSlot<N>(parent)` → JS emitted at the
    document bootstrap. Static HTML covers the initial-true branch; JS only
    re-renders on subsequent transitions.

For the original-bug example, the resulting HTML for `input(@input(e) { name = e.value }, placeholder=...)` carries no `value` attribute and (assuming no other reactivity) no JS at all. For `input(value=name, @input(e) { name = e.value }, ...)`, the static HTML carries `value="World"` (initial-state baked in by the renderer) and JS contains exactly the spliced Assign that lowering produced.

Enable `NoReactivity` only in html's `Capabilities()` (no `NoDeclarative` —
the static-site path requires the declarative initial tree).

### 3. Migration order

Within one development branch, in this order, each step keeping
`go tool verify` green:

1. **Land the `lower` stdlib namespace + `lower.removeChild` intrinsic.**
   No behavior change yet; existing intrinsics start round-tripping cleanly
   through `ir.Convert`. Add `FuzzLoweredDocument` and exercise it under
   the current caps mix.
2. **Extend `passReactivity` to cover structural deps.** Gated by the
   existing `Caps.NoReactivity` flag. fyne / gtk4 / html start receiving
   `__renderSlot<N>` Funcs in their lowered IR, but their existing
   structural-update machinery (`updateIf`, `updateFor`, etc.) keeps
   running and produces the same observable result. (The new slot calls
   are no-ops to today's renderers because the synthetic Funcs aren't
   wired in yet.)
3. **Add the shared `codegen/intrinsic_walker.go`** with no consumers
   yet.
4. **Convert fyne** to the intrinsic-walker pipeline. Enable
   `NoDeclarative`. Remove fyne's `updaters` / `FindAffected` / `updateIf` /
   `updateFor`. Verify all fyne tests pass.
5. **Convert gtk4** the same way.
6. **Convert html.** Keep the initial-HTML emitter; replace the JS-update
   layer with intrinsic-walker output. Remove `g.updates` and friends.
7. **Final audit:** confirm no parallel reactive-dispatch machinery
   remains in any of the three platforms. Update `FuzzLoweredDocument` to
   cover the full caps mix.

Each step is independently verifiable. If a regression appears in step N,
it localizes to that step.

> **Plan A** (`docs/superpowers/plans/2026-05-12-reactivity-lowering-foundation.md`)
> lands steps 1–3. **Plan B**
> (`docs/superpowers/plans/2026-05-12-fyne-intrinsic-translator.md`) lands
> the fyne intrinsic translator + slot-Func consumption (validates the
> pipeline on fyne; old tree walker still in place). **Plan B.2** enables
> NoDeclarative on fyne, switches BuildUI to walk lowered IR, and rips
> the parallel updater pipeline (also resolves the `__root` sentinel
> binding that Plan B left as a Plan B.2 dependency). Plans C/D cover
> gtk4 / html. Plan E is the final audit.

## Testing

- `internal/lower/reactivity_test.go` gains golden tests for the new slot
  emission: an `if reactiveVar { ... }` and a `for x = reactiveList { ... }`
  fixture, asserting the lowered IR contains the expected `__slot<N>` var,
  `__renderSlot<N>` Func, and spliced `CallStmt`s after mutations.
- `FuzzLoweredDocument` (new) asserts the round-trip invariant across the
  full caps mix.
- Existing platform test suites (`codegen/platform/{fyne,gtk4,html}/...`)
  continue to assert observable behavior; conversion steps must keep them
  passing without modification, since the IR contract the platforms now
  consume is upstream of what those tests exercise.
- Cross-platform interpreter tests in `codegen/platform/none/...` confirm
  that the reactivity-bearing fixtures evaluate to the same final state
  regardless of which platform emits them.

## Next steps

Follow-up work to take on after this spec lands, each its own design /
plan cycle:

1. **Two-way binding syntax (`:value=name`).** Prefixed-prop sugar that
   expands during parsing or checking into an `Assign #node.value = name`
   plus an `@input` (or platform-equivalent) handler that writes back into
   `name`. Both halves then ride the standard `passReactivity` pipeline
   landed here. Replaces the implicit `@input → value` inference removed in
   commit `18208e8`.

2. **Keyed `for`-loop diffing.** The slot abstraction in this spec tears
   down and rebuilds all children when an iter dep changes — same as
   today's `updateFor<N>` machinery. Add a key-extractor expression (e.g.
   `for item = items key item.id { ... }`) and extend `__renderSlot<N>`
   to diff the prior `__slot<N>` entries against the new iter by key,
   emitting `lower.removeChild` / `lower.appendChild` only for the
   actually-changed positions. Property updates on surviving children
   continue to flow through `passReactivity`'s prop path.

3. **Audit other lowering passes for platform under-use.** android and
   bubbletea currently return `Caps{}` and handle ternaries, toggles,
   refs, enums, units, computed vars, etc. directly in codegen. Some of
   that is correct (RenderModel platforms genuinely don't need
   `NoReactivity` / `NoDeclarative`) but several other passes likely
   ought to be enabled on those platforms. Build a per-cap-per-platform
   matrix, find duplication analogous to what this spec removes, and
   migrate platforms onto the lowering pipeline.

4. **Fix any remaining `Convert(Lower(checked))` round-trip breaks.**
   `FuzzLoweredDocument` landed in step 1 may surface other passes whose
   output `ir.Convert` cannot express in valid AST (lifted closures from
   `NoLambda`, kicker patterns from `NoAsyncReactive`, etc.). Each
   finding gets its own surgical fix; ideally every enabled pass
   round-trips.

5. **Nested reactivity in slot bodies.** The Plan A `passReactivity`
   extension panics if a reactive `If`/`For` body contains a nested
   `If`/`For`. Single-level reactivity is supported; nesting requires
   a separate design that addresses shared-parent teardown semantics,
   prop reactivity on rebuilt-node IDs, and DOM ordering between
   peers and slot-rendered children.

## Risks and rollback

- **html JS-update regression.** Highest blast-radius step (6) because
  html's update path is the messiest current state. If the conversion
  reveals an under-specified corner of the lowered IR, rollback is per-step:
  revert step 6, keep the rest. The `passReactivity` extension is dormant
  for any platform not consuming `__renderSlot<N>` calls.
- **fyne / gtk4 blueprint regressions** around `entrySync`. The blueprint
  path is left intact in step 4 / 5; if conversion breaks it, the change
  is localized to those steps.
- **Round-trip fuzz finds existing latent breaks.** `FuzzLoweredDocument`
  may surface other intrinsic-emitting passes (e.g. NoLambda's lifted
  closures) that don't round-trip today. Treat as findings to fix
  incrementally; not blockers for this work.
