# Component Inlining as a Lowering Pass — Design

**Date:** 2026-05-16
**Status:** Approved direction (no implementation yet)

## Goal

Replace the flat-Model gymnastics every Go-flavored platform (bubbletea,
fyne, gtk4, future C) does today with a single language-agnostic
lowering pass that **inlines every non-recursive child component into
`main`**. After the pass, codegen only ever sees one `Component`
(plus any recursive cycles), and every per-instance prop, event, state,
and computed-func reference is already resolved to a fully-rewritten
identifier scoped to `main`.

The trigger for this design is the long tail of bubbletea/fyne bugs
surfaced by `examples/showcase/app.sngl`:

- "param refs in child-component var initializers" (`var n = start`)
- "field collisions when multiple child components contribute to
  `Model`"
- "child component's `__root` / `__slot<N>` collide across copies"
- "computed funcs on a child component don't show up on the parent's
  Model"
- "events emitted from a child don't reach the parent handler unless
  the codegen wires it manually"

Each of these is a symptom of the same architectural shortcut: the
codegens fake "one Model" by flattening, but child-component scopes
(props, events, child-private state) survive into IR, and the
flattening is incomplete. Either go fully flat at lowering time, or
go fully not-flat at codegen time. This design chooses the former.

## Non-goals

- Inlining stdlib components (`vbox`, `text`, `button`, …). They are
  already handled by `InlinePure`; the new pass picks up *user*
  components.
- Inlining recursive components. They stay as components; codegen
  retains a "real" call form for them (today only fyne and android
  exercise this — TreeView in showcase). They are a tiny surface.
- Changing checker-time scope rules. The pass operates entirely on
  type-checked IR. Source-level component composition is unchanged.
- Inlining components passed dynamically (e.g. through a `dyn` slot).
  SNGL doesn't have this feature today.

## What the pass does

Conceptually, for every `NodeInst` whose `Component` field references a
user-defined component (not stdlib, not recursive, not a window):

1. **Clone the callee's body, vars, funcs, timers, and prop refs.**
   Deep-clone so the original component stays available for the
   recursive case and for codegen's debug paths.
2. **Rewrite identifiers.** Vars/Funcs/Locals get a unique suffix
   keyed to the call site (`n` → `n__instance0`). The suffix is stable
   so reactive deps and updaters remain consistent across re-runs.
3. **Substitute prop references with the arg expression at the call
   site.** `var n = start` against `LabeledCounter(start=0)` becomes
   `var n__instance0 = 0`. Against `LabeledCounter(start=count + 1)` it
   becomes `var n__instance0 = count + 1`. The full IR expression
   gets inlined, not just literal defaults. **This is the key win
   over the current Renames-based hack.**
4. **Inline event handlers at the emit site.** `@click { @changed(n) }`
   inside the child becomes the parent's handler body with `n`
   substituted for the event arg. Multi-line handler blocks splice
   into the call-site's enclosing block.
5. **Splice the inlined body into the parent's tree** at the
   `NodeInst`'s position. Hoist vars/funcs/timers to the enclosing
   component (always `main` after a full pass).
6. **Run to fixed point.** Inlining a child may surface more
   instantiations (e.g. a `Page` component that uses `LabeledCounter`
   internally). Iterate until no user-component instantiations remain
   outside a recursive cycle.

## Recursion handling

Build a call-graph at the start of the pass. Strongly-connected
components in that graph are kept as real `ir.Component`s. Instantiation
sites *inside* a recursive cycle stay as `NodeInst` referencing the
component; instantiation sites *outside* the cycle get inlined as usual
unless the entry point is itself in the cycle.

The recursive surface today is small: TreeView-style components that
take a `dyn`-typed node and call themselves. The pass guarantees
codegens only see this exactly-one shape and not the wider "any user
component" surface.

## Pass placement

In `internal/lower/lower.go`'s pipeline:

```
PlatformExtensionBody
NoUnit
NoEnum
NoTernary
NoAsyncReactive
NoComputed
NoLambda
NoListLambdas
NoToggle
InlinePure
NoInlineComponents   ← NEW: this design
NoReactivity
NoTimer
NoDeclarative
NoRef
```

Reasoning:

- After `InlinePure` so stdlib wrappers are gone first — saves a pass
  iteration.
- Before `NoReactivity` so the synthesized `__root`/`__slot<N>` get
  attached to main (the only component the pass leaves with vars).
- Before `NoTimer` so timers from inlined components get hoisted to
  main's timer list. (Today fyne/bubbletea iterate components for
  timers; after inlining, every timer is on main.)
- `NoComputed` runs *before* this pass. Implication: a child
  component's computed funcs are first inlined into their use sites
  by `NoComputed`, then this pass clones the result into main. This
  avoids needing to re-derive computed dependencies after inlining.

## Caps gating

New `Caps.NoInlineComponents bool`. Initially set on:

- `codegen/lang/golang/golang.go` (Go targets need this)
- `codegen/lang/kotlin/kotlin.go` (android does the same flat-Model)

JS keeps it **off**. JavaScript's natural component-per-class shape
benefits from preserved boundaries — html codegen currently emits
per-component update funcs; inlining would inflate those. The pass is
opt-in so each target picks its tradeoff.

The cap is a one-line flip per target. Default off — no behavior
change for targets that don't opt in.

## What stops being needed

When this pass lands, the following codegen workarounds become dead
code and get deleted:

- `bubbletea/compiler_ir.go`: the Renames-based prop-default
  substitution loop (`for _, p := range tv.comp.Props { ... }`).
- `fyne/compiler_ir.go`: the mirror of that substitution loop.
- `fyne/compiler_ir.go`: the "skip Synthesized vars from non-main
  components" guard added in commit a3602e5 — there *are* no non-main
  components left.
- bubbletea/fyne `taggedVar` collection across multiple `pkg.Components`
  — the loop walks only `main` and `pkg.Vars` (+ const decls).
- The flat-Model "fields from every component" merging logic — the
  Model only carries main's fields.

The remaining codegen complexity (Model struct emit, New(), Update(),
getters/setters) stays but operates on a clean, single-component IR.

## Implementation sketch

`internal/lower/inline_components.go`:

```go
var passNoInlineComponents = pass{
    name:    "NoInlineComponents",
    enabled: func(c Caps) bool { return c.NoInlineComponents },
    apply:   lowerInlineComponents,
}

func lowerInlineComponents(pkg *ir.Package, _ Caps, _ Options) error {
    cycles := findRecursiveCycles(pkg)
    inlinable := func(c *ir.Component) bool {
        return c != mainOf(pkg) && !cycles[c] && c.Native == nil
    }
    changed := true
    for changed {
        changed = false
        // For each NodeInst{Component: inlinable(c)} in main.Body
        // (and recursively in children), clone+rewrite+splice.
    }
    // Drop pkg.Components entries that were inlined (cycles + main remain).
    return nil
}
```

Key helpers:

- `cloneAndRename(body []ir.Stmt, subst map[string]string) []ir.Stmt`
  — deep clone with ident renaming.
- `substituteProps(body []ir.Stmt, propValues map[string]ir.Expr) []ir.Stmt`
  — replace `*ir.Ident{Name: <propName>}` with the call-site arg
  expression.
- `inlineEventHandlers(body []ir.Stmt, handlers map[string][]ir.Stmt) []ir.Stmt`
  — replace `Emit{Name: "@changed"}` with the parent's handler block,
  substituting event args.

The IR's existing walk helpers (`walkPackage`, the
`transformBlock`/`transformExpr` pattern used by `passLambda`,
`passTernary`, and the new `passNoListLambdas`) provide the
infrastructure.

## Open questions

1. **Reactivity over inlined deps.** When `LabeledCounter`'s `var n`
   reads `start` (a prop), and `start` is bound to `count` (a parent
   state var) at the call site, the inlined `var n__0 = count` makes
   `n__0` reactive on `count`. The reactivity pass should pick this up
   automatically because it runs *after* inlining and sees only the
   final expression — confirm with a fixture before claiming this
   works.

2. **Event arg shape.** `@changed(n)` emits a single value; the parent
   handler `@changed(c) { clicks = c }` binds `c`. Inlining means
   substituting `c → n__0` in the handler body. Multi-arg events and
   spread-style emits need explicit casework.

3. **Native components.** GTK widgets and similar carry
   `Component.Native`. The pass must treat them as opaque (don't
   clone, don't inline). Already gated by `inlinable()` check.

4. **Source positions for diagnostics.** When a checker error or
   runtime panic surfaces from inlined code, the source position
   should point at the original component — not at the call site.
   Preserve `ast.Pos` through cloning; that's already the convention
   for `passLambda`'s lifted closures.

5. **Code size.** N call sites × M lines of body → N×M lines of
   emitted code. For typical SNGL usage (a handful of child component
   instances) this is fine. If a project hits a hot spot, the
   inlining cap can be turned off per-target.

## Test surface

The existing `testdata/component_*.sngl` fixtures are the primary
validation surface. After the pass lands, every fixture that exercises
a child component must still:

1. Pass `sngl check` (unchanged — checker runs first).
2. Pass `sngl test` on every TestRunner platform that has the cap on
   (golang-flavored ones).
3. Produce identical visible output to the pre-inlining version
   (verified via the snapshot fixtures under
   `codegen/platform/<name>/testdata/`).

A new golden-test fixture in `internal/lower/testdata/`:

```
testdata/inline_components/labeled_counter.sngl
testdata/inline_components/labeled_counter.NoInlineComponents.golden
```

should pin the exact post-pass IR shape so future refactors don't
silently break it.

## Acceptance

- `go test ./...` clean.
- `go tool verify` green on golang and kotlin targets.
- HTML snapshot fixtures unchanged (cap is off for JS).
- The Renames-hack in `bubbletea/compiler_ir.go` and
  `fyne/compiler_ir.go` is deleted, not just neutered.
- A new lower-pass test fixture demonstrates per-instance state
  separation (two `LabeledCounter`s with different `start` values
  have distinct `n` state).

## Scope estimate

Multi-session:

1. **Session 1** — implement clone+rename+prop-sub, single-instance
   only, recursion-safe via cycle detection. Get one fixture passing.
2. **Session 2** — handle multi-instance per-call-site renaming,
   event-handler inlining, timer hoisting. Get all `component_*.sngl`
   fixtures green.
3. **Session 3** — delete the codegen workarounds, simplify
   bubbletea/fyne to assume single-component IR. Verify all platforms.
4. **Session 4 (optional)** — turn the cap on for fyne/gtk4/android
   and chase any remaining platform-specific assumptions.
