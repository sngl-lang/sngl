# Inline-Pure-Components Lowering Pass

**Status:** Shipped (passInlinePure)

## Background

SNGL platforms ship stdlib wrapper components in their `.sngl` files:

- `codegen/platform/html/html.sngl`: `component sngl.text() { span(textContent=value) {} }`
- `codegen/platform/gtk4/gtk4.sngl`: `component button() { GtkButton(label=text, @clicked { @click() }) {} }`
- `codegen/platform/fyne/fyne.sngl`: `component text() { Label(constructor=Constructor{...}, bindings=[Reactive{...}]) {} }`

Each platform additionally maintains Go-side tag tables that re-encode the
SNGL-stdlib-tag → native-target mapping:

- `htmlTranslator.htmlTagToDOM("text") → "span"`
- `gtk4Translator.gtk4TagToCType("text") → "GtkLabel"` plus `gtk4Constructor(...)` switch.
- fyne uses runtime blueprint lookup (not a Go switch) — different storage.

The Go-side tables duplicate knowledge the `.sngl` file already declares. They
also tie platforms to SNGL stdlib semantics: the translator knows that a SNGL
`text` is "actually a span/GtkLabel". The architectural cleanup is to inline
the wrapper components into the IR before codegen, leaving each platform's
translator with native shapes only.

## Goals

1. New lowering pass `passInlinePure` that substitutes pure-component calls
   with their inlined bodies via three substitutions (params, slot, event
   invocations).
2. Two operating modes from one substitution engine:
   - **Optimization mode** (always-on): inline user-defined pure components
     opportunistically. Pure = no Vars, no Funcs, no Timers.
   - **Strict mode** (`Caps.NoStdlibWrappers`): platforms opt in. Inline
     wrapper-component calls that reference platform-stdlib components.
     Failure to inline (recursion, impurity, missing body) = pass error.
3. After pass enabled, remove `htmlTagToDOM` from html and `gtk4TagToCType` +
   `gtk4Constructor` from gtk4. fyne stays as-is.

## Non-goals

- fyne's blueprint pattern is untouched. The pass runs on fyne with strict
  mode enabled, but fyne wrapper bodies already contain pure structure
  (blueprint-bearing `Label` NodeInsts with no internal state); behavior
  unchanged.
- User-declared `pure` keyword: not added. Purity detected structurally.
- Cross-package inlining: components imported from other packages stay
  opaque. Only same-package inlining (including platform-stdlib package
  which arrives via the platform's `Package()` shipping path).
- Recursive components: out of scope. Pass detects cycles and errors.
- Children-type enforcement: SNGL's "children type slot between params and
  body" syntax exists but is unenforced today. Plan G does not enforce it
  either. Deferred.

## Design

### 1. Pass placement (`internal/lower/lower.go`)

`passInlinePure` slots between `passReactivity` and `passTimer`:

```
passUnit, passEnum, passTernary, passAsyncReactive, passComputed,
passLambda, passToggle, passReactivity,
passInlinePure,    // NEW
passTimer, passDeclarative, passNoRef
```

- After `passReactivity`: reactive deps wire against user-level component
  props (the props the user calls with) before inlining flattens them.
- Before `passDeclarative`: inlined native NodeInsts get flattened by
  `passDeclarative` along with everything else.

### 2. Pass operation

For each `*ir.NodeInst` in every component / window body / nested control-
flow body / handler body:

1. **Resolve target component**: if `NodeInst.Component == nil`, skip.
   Otherwise the resolved `*ir.Component` is the target.
2. **Decide eligibility**:
   - If target is **pure** (no Vars, no Funcs, no Timers) AND has a body:
     inline (optimization mode).
   - Else if `Caps.NoStdlibWrappers` is set AND target's owning package
     is platform-stdlib: inline (strict mode). If target is impure, raise
     a pass error at the wrapper-component definition site.
   - Else: leave NodeInst as-is.
3. **Substitute** (see Section 3 of brainstorming, formalized below).
4. **Replace** the NodeInst with the substituted body in its parent stmt
   slice.

### 3. Substitution engine

Walk a copy of the wrapper component's body. Three substitution kinds
applied in one pass:

#### 3a. Param-ref substitution

Wrapper params (props + events, both declared in the component's param
list with types) are `*ir.Param` symbols on `Component.AST.Params`.
Every `*ir.Ident` in the wrapper body whose `Sym` is one of those params
gets replaced with the user's bound arg expression for that prop.

The user's arg comes from the user's `NodeInst.Props`. Match by param
name. Deep-copy the arg per substitution site (don't share Expr pointers
across substituted positions).

If the user omits a prop the component declares, the substitution uses
the param's default value if present, else the zero value of the
declared type.

#### 3b. Slot substitution

The wrapper body may contain `*ir.SlotInst` nodes (SNGL's existing slot
placeholder). Each `SlotInst` gets replaced with the user's
`NodeInst.Children` (in order, as a sibling-stmt expansion).

If the wrapper has no `SlotInst` but the user passed children: children
are dropped (matches existing SNGL semantics for user components).

#### 3c. Event-invocation substitution

The wrapper body invokes a declared event via the `@<event>()` shape.
At IR level this is an `*ir.Emit` stmt (existing SNGL IR shape — confirm
during implementation; if it's `*ir.CallStmt` whose `Call.Func.Name`
matches the event name, the engine detects that form instead). The
substitution engine resolves these by inspecting the event-param Sym
chain to identify which user `@event` they target.

Each `@<event>()` call inside the wrapper is replaced with the user's
`EventHandler.Func.Block` stmts spliced at that position. The user's
event-handler body's `Params` (e.g. `(e InputEvent)`) bind to the
wrapper's `@event(eventParam)` invocation args.

If the user didn't bind an `@event` handler, the invocation becomes an
empty stmt list. (Checker will eventually reject this case; pass is
permissive for now.)

### 4. ID preservation

The user's NodeInst may carry a `__nN` ID assigned by `passReactivity`
for reactive splices targeted at it. After inlining, that ID transfers
to the substituted root (the wrapper body's outermost NodeInst). Any
inlined-internal nodes get fresh `__nN` IDs from the existing counter.

If the wrapper has multiple top-level NodeInsts, the user's ID
transfers to the first; subsequent roots become id-less peers (their
parent walks the stmts as siblings).

### 5. Failure modes

Errors carry `*ast.Pos` from the offending NodeInst or component def.

1. **Recursion**: component A inlines into B inlines into A. Pass
   maintains an in-progress set per Component; revisiting marks a
   cycle. Error: `inline cycle: Foo → Bar → Foo at <pos>`.
2. **Strict-mode impurity**: `Caps.NoStdlibWrappers` set, platform-stdlib
   component is impure. Error: `platform stdlib wrapper "<name>" must
   be pure (declares <var | func | timer> "<symbol>")`. Pinpoints the
   wrapper, not the call site.
3. **No body**: wrapper has zero VisualNodes. Error: `component "<name>"
   has no body to inline`.

### 6. Test fixtures

In `internal/lower/testdata/`:

- `inline_pure_basic.txtar` — `component text(value) { span(textContent=value) }` inlined at a call site `text(value="hi")`.
- `inline_pure_with_slot.txtar` — wrapper body contains `slot`; user passes children.
- `inline_pure_event.txtar` — wrapper invokes `@event()` inside its body; user provides handler.
- `inline_pure_recursion.txtar` — recursive call → pass error.
- `inline_pure_impure.txtar` — optimization mode leaves impure user components alone.
- `inline_strict_impure.txtar` — strict mode + impure platform-stdlib wrapper → pass error.
- `inline_strict_clean.txtar` — strict mode + pure platform-stdlib wrapper → inlines.

### 7. Platform impact

#### html

- `html.sngl` wrapper components already shaped correctly. Audit for
  any state-bearing wrapper; fix if found.
- After enabling `NoStdlibWrappers` and running pass: `htmlTagToDOM` is
  unreachable. Delete. `htmlPropSetter` simplifies — props/events live
  on native HTML tags (`span`, `button`, `input`, `div`) with their
  literal property/attribute names.

#### gtk4

- `gtk4.sngl` wrappers: `component button() { GtkButton(...) }`,
  `component label() { GtkLabel(value=value) }`, etc. Audit purity.
- After pass: `gtk4TagToCType` and `gtk4Constructor` switches in
  `intrinsic_translator.go` deleted. Translator's `OnCreateNode` only
  sees GIR-resolved names; reads constructor + setter metadata from
  `Component.Native` (Plan C added this hook).

#### fyne

- `fyne.sngl` wrappers contain `Label(constructor=Constructor{...},
  bindings=[Reactive{...}]) {}` — pure structure (NodeInst props are
  declarative records, no Vars/Funcs/Timers in the wrapper itself).
  Should already pass strict mode.
- Translator behavior unchanged: fyne reads `NodeInst.Props` for
  blueprint records exactly as today.

### 8. Sequencing

1. Build `passInlinePure` + the substitution engine + the six test
   fixtures. Caps default off; nothing inlines yet.
2. Audit each platform `.sngl` for pure-component conformance. Fix any
   wrapper that declares state.
3. Enable `Caps.NoStdlibWrappers` on **fyne** first (lowest risk;
   wrapper bodies already pure; translator unchanged). Run golden
   tests; verify no behavior change.
4. Enable on **gtk4**. Delete `gtk4TagToCType` and `gtk4Constructor`
   from `intrinsic_translator.go`. Verify integration test +
   hello-i18n.
5. Enable on **html**. Delete `htmlTagToDOM`. Verify.
6. Resume **Plan D** (html intrinsic conversion) with the simplified
   `htmlTranslator` baseline.

## Risks

- **Reactive-dep tracking on inlined props**: `passReactivity` records
  deps on user-level component props. After inlining (which runs AFTER
  passReactivity), the spliced `Assign #__nN.<prop> = <expr>` stmts
  reference the user's NodeInst ID. The inlining transfers that ID to
  the substituted root. As long as ID transfer is consistent, reactive
  splices continue to target the right node. Test fixture: a reactive
  prop on a wrapper component.

- **Param-substitution into spliced reactive Assigns**: a wrapper body's
  reactive prop expression (e.g. `Reactive{prop="value", target=".SetText"}`)
  references the wrapper's param. The substituted body must reference
  the user's bound arg, not the wrapper's param ident. Walk-and-replace
  handles this if `*ir.Ident.Sym` correctly points to the wrapper's
  `*ir.Param`. Verify in implementation.

- **fyne blueprint round-trip**: fyne's wrappers carry blueprint props
  (`constructor=Constructor{...}`) that are themselves `*ir.StructLit`
  expressions. Substitution into struct-literal fields must work.
  Probably already does — the walk-and-replace is generic — but
  unit-test it.

## Next steps

After this spec lands:

1. `writing-plans` skill produces an implementation plan for `passInlinePure`.
2. Plan G implementation lands.
3. Plan D resumes with `htmlTagToDOM` gone.
