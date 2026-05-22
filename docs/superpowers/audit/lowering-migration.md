# Lowering migration audit

Findings of platform-codegen logic that belongs in `internal/lower` (or in
`codegen/analysis.go` running once across all platforms), ordered by
cross-platform duplication impact. Every finding is concrete; "migration"
sketches the IR / cap shape, not the implementation.

---

## 1. i18n-usage detection re-implemented in every platform

**Files:**
- `codegen/platform/html/i18n.go:23` `hasI18nCalls`
- `codegen/platform/html/i18n.go:47-278` `walkPkgExprs` (a full 230-line IR walker)
- `codegen/platform/android/i18n.go:18` `hasI18nCalls`
- `codegen/platform/android/i18n.go:80-308` `walkPkgExprs` (near-identical copy)
- `codegen/lang/golang/golang.go:447` `PackageUsesI18n` + helpers (`funcUsesI18n`, `stmtUsesI18n`, `exprUsesI18n` 100+ lines)
- `codegen/platform/html/html.go:3144` `exprUsesI18n` — yet a *third* IR-expr walker
  asking the same question.

**What it does:** Walks the entire IR after lowering looking for calls whose
qualified name matches a hardcoded i18n receiver list, plus the `Intrinsic`
field (the post-`InlinePure` form).

**Why lower:** "Does this package transitively use i18n?" is a one-shot
predicate that does not depend on the target. The fact that we have three
nearly-identical 200-line stmt/expr visitors with `// Mirrors html.walkPkgExprs.` comments is the giveaway. Lower already runs `passInlinePure`
which is what splits the receiver-call vs. intrinsic-call cases; it can stamp
a single `Pkg.UsesI18n bool` (or `Pkg.RequiredRuntimes`) on the package at the
end of that pass.

**Migration:** Add an `ir.Package.Requires` bitmask (or simpler: bool flags
`UsesI18n`, `UsesAlert`, etc.). Populate during/after `passInlinePure`, since
that's the pass that determines whether the i18n receiver survived inlining or
collapsed to a direct intrinsic. Platform code becomes
`if pkg.UsesI18n { ... }`. Delete both `walkPkgExprs` copies and
`exprUsesI18n`.

---

## 2. `walkPkgExprs` / generic IR visitors duplicated across platforms

**Files:**
- `codegen/platform/html/i18n.go:47-278` — html's `walkPkgExprs`
- `codegen/platform/android/i18n.go:80-308` — android's `walkPkgExprs`
  (comment literally: "Mirrors html.walkPkgExprs.")
- `codegen/platform/html/html.go:580` `visit`/`visitStmts` (collectNodeIDs)
- `codegen/platform/html/html.go:3145` `walk` (exprUsesI18n)
- `codegen/platform/html/html.go:3219` `walkStmtExprs`
- `codegen/platform/html/html.go:3548` `collectLoweredRefs` (full stmt+expr walker)
- `codegen/platform/html/routes.go:254` `walkInstances`, `:301` `stmtCallsTarget`, `:357` `exprCallsTarget`
- `codegen/platform/fyne/compiler_ir.go:838` `collectNodeTags`
- `codegen/platform/gtk4/intrinsic_translator.go:59` `collectFromStmt`
- `codegen/platform/bubbletea/compiler_ir.go:706` `emitIRButtonHandlersWalk`
- `codegen/analysis.go:177` `stmtsUseErrorHandling`, `:232` `collectUsedIRStmts`, `:379` `irStmtUsesAlert`
- `codegen/iterate.go:84` `collectWindows`, `:162` `collectReachableComponents`
- `codegen/treewalk.go:35` `TreeWalker.WalkStmts`, `codegen/iterate.go:290` `walkVisual`

**What it does:** Each one is a recursive stmt+expr visitor that case-switches
over the same set of `ir.Stmt`/`ir.Expr` constructors, and each one panics
when it sees an unknown node. They differ only in the leaf action.

**Why lower:** This isn't strictly "lowering" — it's the absence of a single
visitor scaffold. But it's worth flagging here because every new lowering pass
or platform needs to be updated in N places when an IR constructor is added.

**Migration:** Build one IR visitor in `ir/walk.go` with overrideable hooks
(à la go/ast). Every grep-result above collapses to a `Visitor` impl with one
or two methods set. Concretely: `ir.Walk(stmts, ir.VisitorFuncs{Stmt: ..., Expr: ...})`.
This deletes ~1500 lines of duplicated walker code across the platforms.

---

## 3. Stdlib-component → native-widget translation done per platform

**Files:**
- `codegen/platform/bubbletea/view_ir.go:287` `renderStdlibComponent` (switch over
  ~20 stdlib names: vbox, hbox, scroll, text, badge, button, checkbox, modal,
  input, datepicker, etc.)
- `codegen/platform/android/compose_ir.go:147` `renderStdlibComposable` (same 20-ish
  names, mapped to Compose widgets)
- `codegen/platform/fyne/view_ir.go:206` `renderStdlibComponent` →
  `renderFromBlueprint` (blueprint-driven, but the blueprint table is still
  per-platform and the dispatch path is custom)
- `codegen/platform/gtk4/intrinsic_translator.go` (similar, blueprint-style)
- `codegen/platform/html/html.go` `domWriteForIR:193` + props table at `:54`
  hard-codes the SNGL-component → DOM-property mapping.

**What it does:** Each platform has a giant switch keyed on the stdlib
component name (`"vbox"`, `"button"`, `"input"`, …) that decides which native
widget to build and how to map SNGL props to native props/events.

**Why lower:** html *already* dodges this via `NoStdlibWrappers` —
`passInlinePure` substitutes wrapper components with platform-defined
native-element bodies before codegen. The other platforms still hand-roll the
dispatch because `NoStdlibWrappers` only fires for html in
`Capabilities()`. They have blueprint tables (`fyne/blueprint.go`) or hardcoded
switches doing the same job, but only after the visual tree has reached
codegen.

**Migration:** Generalize `NoStdlibWrappers` so every platform can opt in.
Move the per-platform "what widget for `button`" definitions into each
platform's `.sngl` file (`bubbletea.sngl`, `android.sngl`, `fyne.sngl`,
`gtk4.sngl` already exist via `pkgSource`). Lower the visual tree through
`passInlinePure` with the platform's overrides applied so codegen only sees
native nodes. Drop `renderStdlibComponent`/`renderStdlibComposable` entirely.

---

## 4. Visual-tree walker reimplemented in every platform

**Files:**
- `codegen/platform/bubbletea/view_ir.go:150` `renderStmt`
- `codegen/platform/android/compose_ir.go:26` `renderStmt`
- `codegen/platform/fyne/view_ir.go` `renderStmt`
- `codegen/platform/html/html.go:812` `renderIRStmt`
- `codegen/platform/html/html.go:3604` `collectLoweredRefs`
- `codegen/platform/html/routes.go:254` `walkInstances`

Each has a `case *ir.If: …recurse… case *ir.For: …recurse… case *ir.PlatformFilter: …recurse… case *ir.SlotInst: …recurse… case *ir.ErrorBoundary: …recurse…`. The comment in html.go:806–811 admits
"`*ir.If` and `*ir.For` never reach this switch in production" — yet the cases
are still required because lowering doesn't strip them in every code path.

**Why lower:** `PlatformFilter`, `ErrorBoundary`, and `SlotInst` are
structural sugar that lowering can eliminate before codegen runs. Today
`NoDeclarative` is too coarse (it flattens the *entire* tree into create/append
calls) — a finer pass that *only* drops PlatformFilter/ErrorBoundary wrappers
when the body still needs to be visible to codegen would let the render-loop
platforms (bubbletea, android) stop having those cases.

**Migration:**
- Add `passFlattenPlatformFilters` (cap: `NoPlatformFilter`) that splices
  matching `PlatformFilter` bodies inline and drops non-matching ones. Today
  gtk4 already does this by hand at `compiler_ir.go:131` `flattenPlatformFilters`.
  Lift that into a lower pass; gate by `Caps.NoPlatformFilter`. Enable for
  every platform.
- Add `passFlattenErrorBoundary` similarly (today every platform's switch
  has `case *ir.ErrorBoundary: walk children`).
- `SlotInst` lowering already exists for declarative platforms; gate it for
  render-loop platforms too via a `NoSlotMarker` cap.

After these, render-loop platforms' `renderStmt` switches drop from ~10
cases to 3 (`NodeInst`, `If`, `For`).

---

## 5. Computed-dep tracking done in CommonAnalysis from raw IR.Reads

**Files:**
- `codegen/analysis.go:65-99` populates `ComputedDeps` by walking
  `f.Reads` (which the *checker* fills in).
- `codegen/deps.go` `DepTracker` re-derives roughly the same information at
  *codegen* time.
- Platforms (`codegen/platform/html/html.go:3324` `exprDeps`,
  `codegen/platform/fyne/view_ir.go:77` `exprDeps`, etc.) each call
  `DepTracker.ExprDeps` and then post-process the result by remapping
  promoted variable names through `dataRenames`.

**Why lower:** Reactive dependency analysis is a transformation, not a
codegen concern. The `passReactivity` pass already runs after deps must be
known. A lowered IR where each watched expression carries its dependency set
inline (e.g. `ir.Updater { Watch ir.Expr, Deps []*ir.Var, ... }`) would let
every platform stop reaching back into `DepTracker` mid-emit.

**Migration:** Have `passReactivity` materialize `*ir.Updater` IR nodes
carrying their `Deps` directly (today they live in `codegen.MutationModel.Updaters`
populated mid-codegen). The MutationModel becomes a thin index over the IR,
not a parallel data structure. Render-loop platforms keep ignoring updaters;
mutation platforms iterate them.

---

## 6. Toast / Alert lowering done per-platform

**Files:**
- `codegen/analysis.go:355` `usesAlert` (scans IR for `Alert.*` receiver calls)
- `codegen/platform/bubbletea/view_ir.go:71-84` emits 14 lines of hand-written
  toast overlay code (variant→bg color switch, lipgloss styling, JoinVertical
  splice) directly into the `View()` body.
- `codegen/platform/fyne/compiler_ir.go:603-607` emits `m.toastLabel`,
  `m.toastBox`, container splice for fyne.
- `codegen/platform/fyne/compiler_ir.go:797` `fyneIRAlertFunc` translates
  `Alert.toast(...)` into `m.showToast(msg, method)` Go strings.
- `codegen/platform/gtk4/compiler_ir.go:190` `gc.AlertFunc = gtk4IRAlertFunc`
  → emits `fmt.Fprintf(os.Stderr, ...)`.
- Each platform has its own NeedsToast handling + toast container
  initialization.

**Why lower:** `Alert.toast/info/warn/error` is a stdlib API — its semantics
("show a transient banner with a variant color") are platform-independent. A
lower pass can rewrite `Alert.toast(msg)` into a synthesized
`__sngl_toast_queue.push({msg, variant})` mutation against a synthesized
toast-state Var, plus a synthesized visual node at the root of the tree that
binds to that state. Then every platform just sees normal IR.

**Migration:** Add `passLowerAlert` (cap: `NoAlert`). It declares
`__sngl_toasts list<sngl.Toast>` as a synthesized package var, rewrites every
`Alert.toast(...)` call into a list mutation, and injects an `if __sngl_toasts.length > 0 { ... }` visual node at the bottom of `main`'s body.
Drop the per-platform NeedsToast/AlertFunc/toast-render code paths.

---

## 7. Style props translated per platform with overlapping shape

**Files:**
- `codegen/platform/bubbletea/view_ir.go:578` `buildIRStyleExpr` (lipgloss chain)
  + `:608` `irStyleCall` (per-prop switch: padding/margin/width/color/...)
- `codegen/platform/android/compose_ir.go:431` `buildModifierRaw` (Modifier chain)
- `codegen/platform/fyne` style integration via blueprint
- `codegen/platform/html` style → CSS via the `Styles` slice on
  CommonAnalysis (`codegen/analysis.go:281`)
- `codegen/codegen.go` `NodeStyleFields` (codegen/iterate.go:368) is shared,
  but the *interpretation* of each style key is replicated.

**Why lower:** The set of supported style props (padding, margin, color,
background, fontWeight, etc.) is fixed at the language level (`lib/`). A
lower pass can normalize each style-bearing `NodeInst` into a canonical IR
representation: a deterministic ordered list of `(propName, ir.Expr)` already
range-checked, with `scaleFactor` semantics applied for terminals separately
(or as a separate post-pass).

**Migration:** Lower the `style=` prop's `StructLit` into a flat
`Node.StyleProps []ir.StyleAssign` where each entry's key is from a closed
enumeration. Each platform's style emitter becomes a per-key switch with
*no* lookup into the StructLit shape. This deletes `buildIRStyleExpr`,
`irStyleCall`, `buildModifierRaw` overlap. Scale-factor lowering is a
separate `passApplyTerminalScale` enabled only by bubbletea.

---

## 8. Window collection / dynamic-href detection

**Files:**
- `codegen/iterate.go:84` `collectWindows`
- `codegen/platform/html/html.go:102` `rejectDynamicHrefs`
- `codegen/platform/html/routes.go` (router emission)
- `codegen/platform/fyne/compiler_ir.go:1014` `emitIRMultiWindowCode`
- `codegen/platform/gtk4/compiler_ir.go:147` `mainBodyStmts` picks
  "wins[0].Body OR main.Body" — different platforms make different choices
  about which window is "main".

**Why lower:** Window discovery + classification (static-href vs.
dynamic-href, single vs. multi) is platform-independent. The "synthesize a
window from main when none exist" rule in `iterate.go:68` is the same
across all platforms.

**Migration:** Make a lower pass that always normalizes the visual tree to
have explicit top-level `ir.Window` nodes (synthesizing one when none exist).
Tag each window with `Window.IsDynamic` (any non-literal in href) and
`Window.Index`. Platforms iterate `pkg.Windows` directly; the synthesis +
classification path disappears. `rejectDynamicHrefs` becomes a checker
diagnostic, not codegen logic.

---

## 9. `flattenPlatformFilters` hand-rolled in gtk4

**File:** `codegen/platform/gtk4/compiler_ir.go:131-143`

Hand-written 13-line function that does exactly what a lowering pass should
do: splice matching `PlatformFilter` bodies, drop non-matching ones. Used in
gtk4 codegen on `mainBodyStmts(c.ctx)`. The other platforms achieve the same
effect either by (a) catching `*ir.PlatformFilter` in their renderStmt
switch (bubbletea, android, fyne, html), or (b) doing it in
`codegen/iterate.go:179` `collectReachableComponents` selectively. Five
different implementations of the same operation.

**Migration:** Same as finding #4 — `passFlattenPlatformFilters`. Delete the
gtk4 helper outright.

---

## 10. Per-platform "compute focusables, focus index, button index" walks

**File:** `codegen/platform/bubbletea/compiler_ir.go:206-239` walks the visual
tree to build `info.focusables []string` (one entry per focusable
input/button/checkbox), plus `view_ir.go:350-545` separately re-walks the
same tree and *expects the indexes to come out the same*, plus
`compiler_ir.go:706` `emitIRButtonHandlersWalk` *re-walks again* assigning the
indexes a third time. Three walks, three independent counter sequences,
implicitly assumed to align.

**Why lower:** Numbering focusable widgets is a one-shot analysis pass. A
lower pass can stamp `NodeInst.FocusIndex` once and the three walks then
agree by construction.

**Migration:** Add `passAssignFocusOrder` enabled when platform sets
`Caps.NoImplicitFocus` (or as a CommonAnalysis post-step) that assigns each
focusable NodeInst a stable index. Three bubbletea walks read that field
instead of incrementing counters.

---

## 11. `idToNode` map built post-lowering by html.go's prewalk

**File:** `codegen/platform/html/html.go:574-645` `prewalkNodes` walks the
already-lowered IR to build `g.idToNode` so that handler-translation
(`OnPropAssign`) can map SNGL-prop names to DOM properties via
`domWriteForIR`.

**Why lower:** `NoReactivity` already assigns each NodeInst's `__n*` id
during lowering. The map from id → NodeInst should be a byproduct of that
pass — emitted onto `ir.Package.Nodes map[string]*ir.NodeInst` — not
recomputed at codegen time. Without it, the html codegen has yet another
~70-line stmt walker (see finding #2).

**Migration:** Have `passReactivity` build the id→node index on the Package
as it assigns ids. Delete `prewalkNodes`.

---

## 12. `collectNodeTags` (fyne) parallels gtk4's `collectTagComponents`

**Files:**
- `codegen/platform/fyne/compiler_ir.go:836` `collectNodeTags` — walks all
  Funcs looking for `LocalVar __nX = lower.CreateNode("tag")` pairs to
  recover a `nodeID → tag` map.
- `codegen/platform/gtk4/intrinsic_translator.go:53` `collectTagComponents` —
  exactly the same idea, but recovers `tag → Component`.

**Why lower:** Both platforms need to know "what tag was each `__n*`
created with" to wire up blueprint/binding lookups. The information is
already known at the moment `passDeclarative` emits the `lower.CreateNode`
intrinsic. Stamping `LocalVar.Tag string` directly on the synthesized
LocalVar removes both recovery walks.

**Migration:** Have `passDeclarative` set a new `*ir.LocalVar.Tag` field
(or attach the originating `*ir.Component` pointer to the LocalVar). Drop
both helpers.

---

## 13. fyne / gtk4 parse blueprint `Signature` strings at codegen time

**File:** `codegen/platform/fyne/compiler_ir.go:963` `parseSignatureParams`
splits a literal Go signature string like `"func(s string)"` into IR params,
then re-encodes them as `&ir.Param{Type: &ir.Type{Kind: ir.TypeDyn, Meta: "string"}}`. This is a tiny parser that exists because the blueprint table is
plain text. gtk4 has analogous string-munging in its callbacks emission.

**Why lower:** The blueprint metadata is parsed once when fyne registers; it
could materialise as proper `*ir.Type` values up front. Lowering of
`bindEvent` to a synthesized Func wrapper could attach the right params
without rebuilding them from a string.

**Migration:** Type the blueprint `Bindings.Signature` field as
`[]*ir.Param` directly (loaded once at `init()`). Have `passDeclarative`
build the promoted-handler Func with those params set. Delete
`parseSignatureParams`.

---

## 14. `extractIRAssignTarget` re-extracts a two-way bind in 3 places

**Files:**
- `codegen/platform/bubbletea/compiler_ir.go:844` `extractIRAssignTarget`
- `codegen/platform/fyne/compiler_ir.go:931` calls same helper
- `codegen/platform/fyne/view_ir.go:402` calls same helper

Helper inspects the first stmt of a handler block to recover the SNGL var
being two-way bound (i.e. handler is `var = e.value`).

**Why lower:** Two-way binding is a lower-time concept. The relationship
"this handler is the two-way bind for var X attached to event Y" should be
an IR fact (`EventHandler.TwoWayTarget *ir.Var`) emitted by the pass that
synthesizes the bind, not re-derived by pattern-matching the block shape at
codegen.

**Migration:** Whatever pass synthesizes the two-way bind handler tags
`EventHandler.TwoWayTarget`. Three callsites read it directly. Drop the
helper.

---

## 15. `isStdlibComponentName` heuristic in codegen package

**File:** `codegen/iterate.go:211` decides "is this name a stdlib component"
purely by string shape (lowercase first letter, or `sngl.` prefix). Used by
`collectReachableComponents` to skip component-method emission for stdlib
nodes.

**Why lower:** Stdlib-ness is a property of the `*ir.Component` (defined in
`lib/` and embedded via the stdlib loader). The pointer identity should
flow through the IR; codegen shouldn't be guessing from a name pattern.

**Migration:** Add `Component.IsStdlib bool` (set when the checker loads
`lib/`). Replace the heuristic with a pointer/flag check. Move to lowering or
to checker — either is fine.

---

## 16. html.go `collectLoweredRefs` re-walks for element refs

**File:** `codegen/platform/html/html.go:3548-3663` — full stmt+expr walker
whose sole purpose is to find every `*ir.Ident{IsElementRef:true, Name:"__n*"}` and add it to `g.loweredRefs`. ~115 lines including the
unhandled-stmt panic.

**Why lower:** Whichever pass emits an `IsElementRef` ident knows it's
emitting one. Lowering can maintain a set on the Package
(`ir.Package.LoweredRefs map[string]bool`) populated incrementally.

**Migration:** `passReactivity` and `passDeclarative` populate
`Package.LoweredRefs` on emission. Delete `collectLoweredRefs` entirely.

---

## 17. android compose's `Checkbox`-shape pattern matching

**File:** `codegen/platform/android/compose_ir.go:256-269` peeks at the
*first statement* of a checkbox's `@change` handler to find an `*ir.Toggle`
and read the target var out of it — using that as the checked-state. The
fallback emits `checked = false`.

**Why lower:** This is "pull the bound state out of a checkbox's onchange
handler" pattern-matching, which is the same conceptual operation as
finding #14 but with `Toggle` instead of `Assign`. It belongs in a lower
pass that tags the NodeInst with its bound state ref.

**Migration:** Generalize the two-way-bind annotation from finding #14 to
also cover `Toggle`. `Checkbox` and `Toggle` widgets pick up a stable
`NodeInst.BoundVar *ir.Var` field. Compose's renderer reads it directly.

---

## 18. NeedsErrorHandling computed at codegen entry by every platform that supports it

**File:** `codegen/analysis.go:150` `PackageUsesErrorHandling` — yet another
~80-line recursive stmt walker.

**Why lower:** "Does this package use error handling?" is the *same kind* of
question as i18n usage (finding #1). Set `Package.UsesErrorHandling` during
`passPlatformExtensionBody` or `passLambda`, or any earlier pass that
already touches the relevant nodes.

**Migration:** Compute once on package construction (probably during the
last lowering pass) and stash on `ir.Package`. Delete
`PackageUsesErrorHandling`.

---

## Cross-cutting recommendations

1. **Build a single `ir.Walk(visitor)`.** Every grep in finding #2 disappears.
   Eight reusable visitor recipes (`CollectCalls`, `ScanFor(predicate)`,
   `Rewrite`) live next to it. This is by far the highest leverage change.

2. **Promote "stamp-on-IR" features over "scan-at-codegen" features.**
   Findings #1, #5, #10, #11, #12, #14, #15, #16, #17, #18 all share the
   shape "lower knows; codegen re-derives by walking." A simple rule —
   `passes may only set IR fields, codegen may only read them` — pushes this
   in the right direction.

3. **Make `NoStdlibWrappers` the default.** Only html uses it today;
   bubbletea/android/fyne/gtk4 reimplement the same dispatch by hand
   (finding #3). Standardizing on `passInlinePure` + per-platform native
   bodies in the platform's `.sngl` file would delete the largest cluster
   of platform-specific switch statements in the repo (`renderStdlibComponent`,
   `renderStdlibComposable`, blueprint dispatchers).

4. **Extend `Caps` to cover the small structural sugars.** PlatformFilter,
   ErrorBoundary, SlotInst, Window-synthesis, two-way bind, focus order —
   each one is one new cap and one short pass. Today each platform's
   renderStmt switch has 5-7 cases that exist only because no pass strips
   the construct.
