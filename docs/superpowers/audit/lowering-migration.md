# Lowering migration audit

Findings of platform-codegen logic that belongs in `internal/lower` (or in
`codegen/analysis.go` running once across all platforms), ordered by
cross-platform duplication impact. Every finding is concrete; "migration"
sketches the IR / cap shape, not the implementation.

---

## Re-evaluation (2026-06-11)

Every finding re-checked against current code. Per-finding status lines added
below (`> **Status (2026-06-11):**`). Summary:

- **RESOLVED:** #10 (see bottom); #9 and #15 fixed 2026-06-11 (quick wins);
  #1 and #18 fixed 2026-06-12 (Wave 1: `passStampUsage` stamps package usage
  flags; #6 detection done, full alert lowering still deferred); #20 and #23
  fixed 2026-06-12 (Wave 2: dead conversion branches removed; `IterKind`
  stamped). #2 scaffold built (`ir.WalkStmts`); walker-migration long-tail open.
- **Infra landed but not wired through:**
  - `ir/walkexprs.go` `ir.WalkExprs` — shared *expression-only* walker. Adopted
    by the i18n detectors (#1) but not the stmt-level walkers (#2).
  - `ir.Component.Stdlib` field — exists and used by the checker, but codegen
    still string-guesses (#15).
  - `internal/lower/platform_filter.go` `passPlatformFilter` — runs in the
    pipeline, yet gtk4 still hand-rolls its own copy and ErrorBoundary/SlotInst
    are unhandled (#4, #9).
- **OPEN (no pass, no IR field):** #3, #5, #6, #7, #8, #11, #12, #13, #14, #16,
  #17, #18, #20, #21, #22, #23, #24.
- **PARTIAL:** #1, #2, #4, #9, #15, #19.

No findings removed: only #10 is complete and it was already in Resolved.

---

## 1. i18n-usage detection re-implemented in every platform

> **Status (2026-06-12): RESOLVED (package-level).** `ir.Package.UsesI18n` is
> now stamped by the always-on `passStampUsage` lowering pass
> (`internal/lower/usage.go`), which runs last so it sees post-inlining IR. The
> i18n-call predicate moved into `ir` (`ir.IsI18nCall`/`IsI18nIntrinsic`/
> `IsI18nPluralKey`); `codegen/i18n.IsCall` is now a thin alias. All three
> package-level detectors (`html`/`android` `hasI18nCalls`, `golang.PackageUsesI18n`)
> collapse to `return pkg != nil && pkg.UsesI18n` — the stamp uses the Go
> backend's superset predicate (calls + plural-key Selects) so every backend
> shares one definition. Verified: full suite green, example codegen byte-identical
> (only pre-existing android style-order noise). *Remaining:* `html/html.go`
> `exprUsesI18n` is a per-expression query (updater initOnly decision), not a
> package predicate — left in place; folds into the #2 visitor scaffold.

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

> **Status (2026-06-12): PARTIAL — scaffold built.** `ir/walkexprs.go` is now a
> unified `walker` carrying both an expr and a stmt callback; `ir.WalkExprs` and
> the new `ir.WalkStmts(pkg, fn)` are its two entry points (one traversal, one
> place to extend per new IR shape). The Wave-1/2 stamp passes use it
> (`usage.go`, `iterkind.go`), and `analysis.go` `stmtsUseErrorHandling` +
> `irStmtUsesAlert` were *deleted* (folded into `WalkStmts`-based passes).
> *Remaining (long tail):* migrate the surviving bespoke walkers —
> `collectUsedIRStmts`, html's `prewalkNodes`/`collectLoweredRefs`,
> `fyne.collectNodeTags`, `gtk4.collectTagComponents`, routes.go walkers — onto
> `WalkStmts`. Each is a separate, low-risk swap now that the scaffold exists.

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

> **Status (2026-06-11): OPEN.** `NoStdlibWrappers` (`caps.go:147`, spelled
> `StdlibWrappers`) is still html-only — only `html/html.go:61` sets it; no
> other platform opts in. `bubbletea/view_ir.go:325` `renderStdlibComponent`,
> `android/compose_ir.go:154` `renderStdlibComposable`,
> `fyne/view_ir.go:209` `renderStdlibComponent` all remain.

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

> **Status (2026-06-11): PARTIAL.** `passPlatformFilter`
> (`internal/lower/platform_filter.go:28`) lands and runs at `lower.go:75`, so
> the PlatformFilter half of this finding is addressed in principle — but the
> `case *ir.PlatformFilter` / `case *ir.ErrorBoundary` arms still exist in
> `bubbletea/view_ir.go:183,198`, `android/compose_ir.go:38,44`, and many html
> sites. No `passFlattenErrorBoundary`, no `NoErrorBoundary`/`NoSlotMarker`
> cap. Render-loop `renderStmt` switches not yet trimmed.

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

> **Status (2026-06-11): OPEN.** `Updater` is still a codegen-only struct
> (`codegen/model.go:33`, `Deps map[*ir.Var]struct{}`); no `ir.Updater` IR node.
> `codegen/deps.go` `DepTracker`/`ExprDeps` still drives platform emit.
> See the #5 addendum at the bottom — `OptimizeMutation` is now invoked **only**
> by html (`html.go:2517`); the deps engine's other consumers have thinned, so
> the addendum's "annotate in lowering, delete the engine" path is closer.

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

> **Status (2026-06-12): PARTIAL.** *Detection* is migrated: `ir.Package.UsesAlert`
> is stamped by `passStampUsage`; `analysis.go` `usesAlert`/`irStmtUsesAlert`/
> `irExprUsesAlert` deleted, `NeedsToast = pkg.UsesAlert`. *Remaining (the big
> part):* no `passLowerAlert`/`NoAlert` cap — the per-platform toast overlay
> emission (bubbletea/fyne/gtk4 render + AlertFunc) is untouched. Full toast
> *lowering* (synthesize toast-state var + root visual node so platforms see
> normal IR) is deferred.

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

> **Status (2026-06-12): PARTIAL — nondeterminism fixed, extraction unified.**
> `codegen.NodeStyleFields` now returns an ordered `[]codegen.StyleField`
> (source order) instead of a `map[string]ir.Expr`. This fixes a real bug:
> android's `buildModifierRaw`/`textStyle` ranged the map, so `TextStyle(...)`
> arg order was non-reproducible across builds — confirmed deterministic now
> (regenerate-twice diff is empty). bubbletea sorts the slice (output
> byte-identical); the caller-style merge in `renderRawTerminal` is a
> deterministic `mergeStyleFields` (caller-wins) replacing the old map merge.
> *Remaining:* the per-key *interpretation* switches (`irStyleCall` lipgloss,
> `buildModifierRaw`/`textStyle` Modifier/TextStyle, html CSS) stay
> platform-specific — that's inherent (lipgloss ≠ Compose ≠ CSS), not
> duplication. The deeper `ir.NodeInst.StyleProps` + `passApplyTerminalScale`
> form (move extraction into lowering, closed key enum) is deferred; it offers
> marginal value over the shared ordered helper since extraction is trivial and
> interpretation can't move.

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

> **Status (2026-06-11): OPEN.** No `Window.IsDynamic`/`Window.Index` fields, no
> normalization pass. `iterate.go:84` `collectWindows`, `html/html.go:170`
> `rejectDynamicHrefs` (still codegen, not a checker diagnostic) remain.

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

> **Status (2026-06-11): RESOLVED.** Confirmed every gtk4 codegen entry runs
> `lower.Lower(pkg, caps, Options{Platform: "gtk4"})` (`cmd/sngl/pipeline.go:143`,
> `dump.go`, `testdriver.go:82`), so the always-on `passPlatformFilter` strips
> all `PlatformFilter` nodes before `mainBodyStmts`. gtk4's `flattenPlatformFilters`
> was a no-op passthrough — deleted; `compiler_ir.go:325` now uses `mainBodyStmts`
> directly. Full suite green.

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

> **Status (2026-06-11): RESOLVED.** See the Resolved section at the bottom.
> `passFocusOrder` (`internal/lower/focus_order.go`) lands the fix — note the
> shipped form injects a synthesized `__focused` prop + `__focusNext/Prev`
> funcs rather than the `NodeInst.FocusIndex` field the original sketch
> proposed; net effect (single source of truth for focus order) is the same.

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

> **Status (2026-06-11): OPEN.** `prewalkNodes` still at `html/html.go:674`
> (called `:932`); no `ir.Package.Nodes` index populated by `passReactivity`.

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

> **Status (2026-06-11): OPEN.** No `ir.LocalVar.Tag`. `fyne/compiler_ir.go:905`
> `collectNodeTags` and `gtk4/intrinsic_translator.go:94` `collectTagComponents`
> both remain.

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

> **Status (2026-06-11): OPEN.** `parseSignatureParams`
> (`fyne/compiler_ir.go:1033`) still splits a literal Go signature string;
> blueprint `Bindings.Signature` is still typed `string` (parsed at `:980`).

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

> **Status (2026-06-11): OPEN.** No `ir.EventHandler.TwoWayTarget`. Helper lives
> in `bubbletea/compiler_ir.go:944` and `fyne/compiler_ir.go:846`, called from
> `bubbletea/compiler_ir.go:221`, `fyne/compiler_ir.go:1001`,
> `fyne/view_ir.go:405`.

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

> **Status (2026-06-11): RESOLVED.** `iterate.go` now reads `n.Component.Stdlib`
> / `c.Stdlib` directly; the fragile lowercase/`sngl.`-prefix heuristic is
> deleted. (Scope note: html.go's separate `isStdlibComponentName` is an
> explicit name *allowlist*, not the heuristic this finding targeted, and it
> keys on node names incl. non-components like `window`/`timer` — left as-is.)
> Full suite green.

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

> **Status (2026-06-11): OPEN.** `collectLoweredRefs` at `html/html.go:2907`
> (called `:2876`, `:3204`); no `ir.Package.LoweredRefs`.

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

> **Status (2026-06-11): OPEN.** No `NodeInst.BoundVar`. Pattern-match still at
> `android/compose_ir.go:263-266`.

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

> **Status (2026-06-12): RESOLVED.** `ir.Package.UsesErrorHandling` stamped by
> `passStampUsage` (walker moved verbatim into `internal/lower/usage.go`).
> `codegen.PackageUsesErrorHandling` + `stmtsUseErrorHandling` deleted; the two
> readers (`android`/`bubbletea compiler_ir.go`) use `ctx.Pkg.UsesErrorHandling`.
> Full suite green.

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

## 19. Test functions bypass `lower.Lower` → a full testlower walker per language driver

> **Status (2026-06-11): PARTIAL — grown, not shrunk.** All three testlower
> files remain and are *larger*: `golang/testlower.go` 393 lines,
> `kotlin/testlower.go` 534, `javascript/testlower.go` 154. No shared
> `lowerTest` pass; test funcs still skip `lower.Lower` (`CollectTestFuncs` →
> `LowerTestFile` at `gtk4.go:216`, `android.go:330`). Biggest duplicated
> semantic block in the drivers — note kotlin nearly doubled since the audit.

**Files:**
- `codegen/lang/golang/testlower.go` (whole file: `lowerTestStmt`, `lowerTestIf`,
  `lowerTestFor`, `lowerTestSetContext`, `lowerEventTrigger`)
- `codegen/lang/kotlin/testlower.go` (`lowerTestStmt`, `lowerTestExpr` — a
  *second* full expression evaluator with its own Binary/Unary/Select/Index
  handling, `composeAction`, `lowerEventTrigger`)
- `codegen/lang/javascript/testlower.go` (thinner — delegates the body to
  `jc.EvalStmt`, proving the others don't have to be this big)
- Root cause: `CollectTestFuncs(req.Pkg)` (e.g. `gtk4.go:201`, `android.go:311`)
  feeds test funcs straight to `LowerTestFile` **without** running `lower.Lower`.

**What it does:** Each driver re-walks test-body IR and re-desugars constructs
the pipeline already handles for non-test code: it decodes the `c.id.@event()`
shape by digging into raw `ast.SelectExpr{Kind: SelectEvent}` (re-parsing AST,
not translating IR), recognizes `Test.assert`/`setContext`/`snapshot` by
`Func.Receiver == "Test"`, and re-decides component-state direct-field-write
vs. setter.

**Why lower:** Because test bodies skip `lower.Lower`, every high-level form
(`@event`, `Test.*`, context writes, refs, list-lambdas, toggle) survives to
codegen and each driver hand-desugars it — three times. Largest block of
duplicated semantic logic in the drivers.

**Migration:** Run test funcs through `lower.Lower` like every other function
(or add a `lowerTest` pass that desugars `Test.assert/setContext/snapshot` and
`c.id.@event()` into ordinary IR — an assert `CallStmt`, an `Assign` to a
synthesized `__ctx_*`, an `ir.Call` to the synthesized event method). Each
driver's testlower then collapses to the shared `EvalStmt`/`EvalExpr` path the
JS one already uses.

---

## 20. Builtin type conversions matched by function *name* in all three drivers

> **Status (2026-06-12): RESOLVED.** Investigation showed the checker's
> `inferBuiltinConversion` *already* materializes `ir.Conversion` for
> string/int/float casts, and each driver's `evalConversion` already renders
> them identically — so the name-keyed `evalCall` branches were dead. Deleted
> from all three drivers; only the genuine builtins survive (`size`→`len` in Go,
> `regex`→`RegExp` in JS — neither is a type conversion). Verified: example
> codegen byte-identical, full suite green.

**Files:**
- `codegen/lang/golang/ircontext.go:406` (`evalCall`: `string`/`int`/`float`/`size`)
- `codegen/lang/javascript/ircontext.go:346`
- `codegen/lang/kotlin/ircontext.go:246`

**What it does:** Each `evalCall` special-cases `n.Func.Name == "string"|"int"| "float"|"size"` and emits a cast, keyed on a high-level name.

**Why lower:** A type conversion is a semantic concept already modelled as
`ir.Conversion` (the checker materializes implicit conversions there). These
builtin conversion *calls* slip through as plain `*ir.Call` and get re-recognized
by name in three places, overlapping each driver's own `evalConversion`.

**Migration:** Have the checker (or a small pass) rewrite `string(x)`/`int(x)`/
`float(x)`/`size(x)` into `ir.Conversion{Type, Operand}`. Delete the name-keyed
branches; route through the single `evalConversion`.

---

## 21. `null`→func/nillable "stub" desugaring duplicated across drivers

> **Status (2026-06-11): OPEN.** Per-driver stubs intact:
> `golang/ircontext.go:1037,1047`, `kotlin/ircontext.go:550,558`,
> `javascript/ircontext.go:660,668`. No shared pass.

**Files:**
- `codegen/lang/golang/ircontext.go:806,832` (`isNullToFuncConv`, `nullFuncStubGo`)
- `codegen/lang/kotlin/ircontext.go:430` (`isNullToFuncConvKt`, `nullFuncStubKt`, `ktZeroFor`)
- `codegen/lang/javascript/ircontext.go:532` (`isNullToFuncConvJS`)

**What it does:** Each driver recognizes "a `null` literal converted to a
`func`/`option`/`ref`/`map`/`list` type" and synthesizes a stub — for the func
case a zero-returning lambda (deciding what an unset callable does when invoked),
plus a per-language zero-value table.

**Why lower:** The meaning of `null` in a callable slot is platform-independent
desugaring, replicated 3×.

**Migration:** A pass rewrites `Conversion(null → func sig)` into an explicit
zero-returning `ir.Lambda` once (drivers translate a normal lambda); emit plain
`nil`/`null` for the other nillable kinds. Zero-value-of-type belongs near
`ir/defaults.go`.

---

## 22. Native `(T, error)` call wrapped in an IIFE at codegen time

> **Status (2026-06-11): OPEN.** `maybeWrapErrorReturn` still at
> `golang/ircontext.go:493`; no normalizing pass. Go-only.

**File:** `codegen/lang/golang/ircontext.go:370` (`maybeWrapErrorReturn`)

**What it does:** When a native func has `HasErrorReturn`, codegen wraps the call
in `func() T { v, _ := f(args); return v }()` — synthesizing control flow that
discards the error. The comment admits it was placed here to "keep this fix
local … avoid threading plumbing through every platform."

**Why lower:** Effect/error-handling semantics in the translator; conflicts with
the project rule "prefer compile-time analysis over runtime machinery — emit
direct invocations." Go-only, so not 3-way duplicated, but still misplaced.

**Migration:** A pass normalizes a 2-value `(T, error)` native call into explicit
IR (a temp + discard materialized as IR statements, or an explicit
error-handling node) so the driver emits a 1:1 translation.

---

## 23. `ForHead` re-derives the map-vs-indexed iteration choice in all three drivers

> **Status (2026-06-12): RESOLVED.** `ir.For.IterKind` (Element / Indexed /
> MapEntries) added with `ir.DeriveIterKind`; stamped by the new always-on
> late pass `passIterKind` (`internal/lower/iterkind.go`, using `ir.WalkStmts`)
> after RefLoop so it sees the final loop shape. All three `ForHead`s are now
> pure per-kind templates with no `n.Iter.ExprType()` map-branch. Verified:
> example codegen byte-identical (todo loops included), full suite green.

**Files:** `ForHead` in `codegen/lang/{golang,javascript,kotlin}/ircontext.go`

**What it does:** Each `ForHead` makes the same decision — `iterType.Kind == TypeMap` → key/value iteration; else two-var → indexed; else single element —
then renders it (`range` / `.entries()` / `.withIndex()`).

**Why lower:** The single/two-var index/element *ordering* was just unified
(see "Resolved this pass"). What remains is the map-vs-list *decision* computed
three times. Low impact — the surface syntax genuinely diverges — but real
duplicated semantic logic.

**Migration:** A pass (extend `refloop`, which already normalizes `for &t`)
stamps an explicit `IterKind` (MapEntries / Indexed / Element) on `ir.For`; each
`ForHead` becomes a pure template with no `TypeMap` branch.

---

## 24. html `rewriteSlotCallsToAnchors` rewrites synthesized slot-call args post-lowering

> **Status (2026-06-11): OPEN.** `rewriteSlotCallsToAnchors` still at
> `html/html.go:772`; `passReactivity` still threads a uniform `parent`
> placeholder that this rewrite retargets.

**File:** `codegen/platform/html/html.go:739` `rewriteSlotCallsToAnchors`

**What it does:** Walks every component/window/handler/timer block (and, since
the inline-handler work, into handler-closure lambda args) mutating the
`ir.Call.Args[0]` of every `__renderSlotN(parent)` call that `passReactivity`
synthesized — rebinding `parent` to a `display:contents` anchor ident.

**Why lower:** Threading the correct parent into the slot's create/teardown/
re-fire calls is work the pass that *created* those calls should do. The anchor
*strategy* is html-specific, but the tree-walk-and-rebind is lowering-shaped and
exists only because `passReactivity` emits a placeholder parent.

**Migration:** Have `passReactivity` thread the slot's own abstract anchor ref
into the `__renderSlotN` calls (html supplies the anchor element). The post-hoc
tree rewrite disappears.

---

> **#5 addendum:** the codegen-time reactive engine is two structures —
> `codegen/deps.go` (`ExprDeps`/`MutatedFields`) **and** `codegen/model.go`
> (`Updater.Deps` + `AffectedUpdaters`/`FindAffected`, pruned by
> `codegen/iropt.go:OptimizeMutation`). html/fyne/gtk4 all set `NoReactivity`
> (so `passReactivity` runs *and* injects updater IR) yet still call this engine.
> bubbletea is the one legitimate `deps.go` consumer (RenderModel, no
> `NoReactivity`); its `MutatedFields` use should become a lowering-pass
> annotation so the engine can be deleted.
>
> **Update (2026-06-11):** `codegen.OptimizeMutation` is now invoked **only**
> by html (`html.go:2517`) — fyne/gtk4 no longer call it. So the path to
> deleting `iropt.go`'s mutation optimizer narrows to: (1) move bubbletea's
> `MutatedFields` to a lowering annotation, (2) fold html's `OptimizeMutation`
> survivors into `passReactivity`. `deps.go` `ExprDeps` is still consumed at
> emit time by the mutation platforms.

---

## Resolved (2026-06-07)

### 10. Per-platform "compute focusables, focus index, button index" walks ✅ RESOLVED

`passFocusOrder` lands in `internal/lower/focus.go` (commits `f9759154`, `47a68022`, `d1407507`). `Features.FocusOrder` (positive capability) drives the pass; bubbletea opts in. The three bubbletea walks (`compiler_ir.go:206-239`, `view_ir.go:350-545`, `compiler_ir.go:706`) now read `NodeInst.FocusIndex` stamped by the pass instead of maintaining independent counter sequences.

---

## Resolved this pass (2026-06-01)

The "defer to the language driver / desugar in lowering" theme, applied to
loops, conditionals, and func definitions:

- `for &t = list` (ref<T> loop write-through) is desugared **entirely in
  lowering** (`internal/lower/refloop.go`) into an ordinary two-var indexed
  loop; `ir.For` carries no codegen-facing ref fields and no driver has a ref
  branch.
- Two-var **list** iteration was fixed/aligned across golang (was index/element
  swapped), javascript (dropped the index), and kotlin (`withIndex()`).
- bubbletea / fyne / android view emitters now defer `for`, `if`, and func-def
  emission to the language driver (`ForHead`/`IfHead`/`ElseHead`/`BlockEnd`/
  `EmitFuncDef`); gtk4 already routed through `gc.EvalStmt`. android was the
  un-migrated platform (its loop had no map branch).

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
