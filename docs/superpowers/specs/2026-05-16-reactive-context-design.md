# Reactive Context

**Status:** Designed
**Issue:** [#60](https://git.duckfam.us/jonathan/sngl/-/issues/60)

## Background

Several cross-cutting concerns in SNGL programs want the same shape: a value
provided somewhere above in the visual tree and read by deeply-nested
consumers, with automatic re-render on change.

- **Locale.** i18n templates and date/number formatters need a locale.
  Today the Go runtime uses a process-wide singleton; tests can't switch
  it cleanly mid-tree.
- **Theme.** Light/dark, custom palettes, density.
- **Current user / auth.** Identity and permissions.
- **Routing / nav state.** Active route, breadcrumb stack.

This spec adds a reactive *context* primitive: state values that implicitly
propagate down the component tree without being threaded through every
prop, with reads triggering re-renders when the value changes — analogous
to React `createContext` / `Provider` / `useContext`.

## Goals

1. Subtree-scoped value propagation with nesting and shadowing.
2. Reactive: when a provider's value expression changes, all consumers in
   its subtree re-render through existing reactivity machinery.
3. Uniform across all platforms — no per-platform context runtime.
4. Test harness can set context values for the component under test from
   all four runners (none, fyne, bubbletea, android).
5. Replace the process-global locale in `pkg/{go,js,kotlin}/i18n` with a
   stdlib `locale` context.

## Non-goals

- Consumer-side write-back (mutable context). One-way for v1; revisit
  when two-way prop binding lands.
- Native `CompositionLocal` lowering on Android. Hidden-prop threading
  covers it; native lowering can be a later opt-out from `NoContext`.
- Context as a struct field, list element, function argument, or local
  variable. Top-level decls only.
- Cross-window context propagation. Each window is its own context root.
- Eager-only / non-reactive context — every provider is reactive.
- Theme-system stdlib (`#theme`). Locale is the only stdlib-shipped
  context in this spec; theme is the canonical user-code example.

## Surface syntax

### Declaration

A context is declared at file top level as a visual-node-style invocation,
following the same syntactic pattern as `window` and `timer` (no new
reserved keyword):

```sngl
context #theme(Theme.light)
context #locale("en-US")
context #user(User{name = "", id = 0})
```

The `#identifier` form defines the name. The single positional argument is
the default value expression. The context's value type is inferred from the
default; no explicit type annotation.

The default expression must be a constant expression — no reference to
reactive vars or component-scoped state. Pure stdlib function calls (e.g.
`i18n.defaultLocale()`) are permitted.

Context decls live at file top level only. They are not permitted inside
windows, components, or any other scope.

### Provider

Inside a window or component body, a context name appears in visual-node
position as a provider:

```sngl
context #theme(Theme.light)

window #home(title="Home", href="/") {
    var dark = false
    button(text="toggle", @click { dark = !dark })
    theme(dark ? Theme.dark : Theme.light) {
        Toolbar()
        Body()
    }
}
```

The provider's value expression is reactive: dep-tracked like any other
prop expression. When `dark` flips, the lowered prop assigns drive consumer
updates through the existing `passReactivity` machinery.

Providers nest. The inner block shadows the outer for that subtree only;
siblings of the inner provider continue to see the outer value:

```sngl
theme(Theme.dark) {
    theme(Theme.light) { Inner() }
    Sibling()
}
// sees light
// sees dark
```

### Consumer

A bare reference to a context name in expression position auto-derefs to
the current value (type `T`):

```sngl
component Toolbar {
    text(value=theme.primary)
    text(value=locale)
}
// theme reads as Theme; .primary on it
// locale reads as string
```

A context name cannot be assigned, used as a function argument, used as a
struct field, list element, or local var initializer. The only legal
positions are: top-level decl, provider visual-node position, and bare
expression read.

## Semantics

### Reachability

For each context `name`, a component is in `Reach(name)` iff its body
transitively reads `name`. Transitive: a component `A` that calls component
`B`, where `B ∈ Reach(name)`, is itself in `Reach(name)` if the call site
is not enclosed by a `name(...)` provider that shadows the outer value.

Reachability is computed during the `NoContext` lowering pass.

### Root default

At each window's root, the pass synthesizes a top-level binding
`__ctx_name = defaultExpr` for every context, and threads it into every
top-level component call in that window. This guarantees that every
component in `Reach(name)` receives a value, with no special-case
"no provider above" runtime handling.

### Nested provider lowering

A provider `name(value) { children }` is dropped from the tree. For each
component call reachable in its block — at any nesting depth, including
inside primitive containers like `vbox`/`hbox`, `if` arms, or `for`
bodies — the synthesized `__ctx_name` parameter is assigned `value`.
Primitive widgets (`text`, `button`, `hbox`, …) consume context only via
the i18n implicit-reader mechanism (see Function lowering); they do not
themselves carry hidden params. Nested providers compose: the inner
block's assignments override the outer, while outer-block siblings keep
the outer value.

### Reactivity

Because the lowered form is a plain reactive prop assignment, no new
reactivity machinery is required. The existing prop-update path drives
consumer re-renders when a provider's value expression changes.

## Pipeline

### Parser

- `internal/parser`: parse top-level `context #name(expr)` as a new
  `ContextDecl` AST node. Inside windows/components, parse `name(args) { children }` as the existing visual-node-call form; the checker
  disambiguates context provider from a component call.

### AST (`ast/ast.go`)

- `ContextDecl{Name string; Default Expr}`.
- `ContextProvider{Ref string; Value Expr; Children []VisualNode}` —
  emitted by the checker after resolving the visual-node call as a
  provider; reads at this layer remain `IdentExpr`.

### Checker

- Recognize `ContextDecl` in pass 1 (declaration registration). Record
  inferred type from `Default`.
- Validate that `Default` is a constant expression.
- Resolve visual-node calls whose callee is a context name as
  `ContextProvider`. Reject context name usage in disallowed positions
  (assign target, func arg, struct field, list element, local var init).
- A bare `IdentExpr` resolving to a context decl in expression position
  type-checks as the context's `T`.
- Mark stdlib i18n functions (`i18n.tr`, `i18n.format`, `i18n.numberInt`,
  `i18n.numberFloat`, `i18n.date`, `i18n.time`, `i18n.datetime`,
  `i18n.select`, `i18n.plural`, `i18n.selectordinal`) as readers of the
  stdlib `locale` context; the lowering pass treats them as implicit
  consumers.

### IR & `ir.Convert`

- IR nodes mirroring `ContextDecl` and `ContextProvider`. Post-`NoContext`
  IR contains zero context-specific nodes, so the reverse direction is
  trivial.
- Forward direction (AST → IR): straightforward translation of the new
  nodes before lowering.

### Lowering — `NoContext` capability pass

Runs after type-check, before component inlining. Steps:

1. **Reachability.** For each context, compute `Reach(name)` over the
   call graph.
2. **Hidden param synthesis.** Add `__ctx_name T` parameter to every
   component in `Reach(name)`. Rewrite reads of `name` inside those
   bodies to reads of `__ctx_name`. Computed-prop dep tracking
   re-derives normally.
3. **Provider lowering.** Replace each `ContextProvider` with its
   children, threading `value` as `__ctx_name=value` to every top-level
   component call in the block. Nested providers compose by the inner
   block overriding the outer's assignments.
4. **Root default.** At each window's root, synthesize a constant binding
   for every context's default and thread it into every top-level
   component call.
5. **For/if/list bodies.** A provider whose children include a `for`
   or `if` block lowers identically — the synthesized prop assignment
   is emitted for each instance.

After this pass, no context-specific IR remains. Existing
`passReactivity` and codegen treat the synthesized parameters as ordinary
reactive props.

### Capability flag

All platforms set `NoContext: true` for v1. Future: Android/Compose could
opt out and lower to `CompositionLocal` natively.

### Formatter (`internal/parser/formatter.go`)

Round-trip the new forms exactly:

- `context #name(default)` at top level.
- `name(value) { children }` provider in visual-node position.
- Bare-name reads need no formatter change (already an `IdentExpr`).

### LSP (`internal/lspcore`)

- Hover on a context ref: show `context name T`, default value, and
  declaring file.
- Go-to-definition: jump to the `context #name(...)` decl.
- Completion: propose context names in expression and visual-node
  positions where a context value would be type-valid.
- Diagnostics: error positions for disallowed usages.

### Dump commands

`sngl dump parsed/checked/optimized` must emit the new forms and
round-trip through the formatter. `sngl dump analysis` should expose
`Reach(name)` per context for debugging.

## Per-platform lowering

After `NoContext` runs, every platform sees only ordinary reactive props
on component-call sites. No new platform code is required beyond what
already handles reactive props.

| Platform                              | Notes                                                                                                                                                                                |
|---------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **html** (static, `--lang none`)      | Defaults baked in at emit; constant providers fold to literal child-prop substitution. Zero JS overhead for static pages.                                                            |
| **html** (route mode, `--lang go`)    | Each route's root threads the default; reactive providers emit standard prop-update JS through the existing reactivity layer.                                                        |
| **fyne / gtk4** (MutationModel)       | Lowered provider = ordinary prop reassignment + `passReactivity` updater dispatch. NoInlineComponents path: the synthesized `__ctx_name` param rides existing hidden-prop machinery. |
| **bubbletea / android** (RenderModel) | Hidden param appears as a Model field on the receiving component's struct/data-class; re-render-from-state propagates value changes for free.                                        |
| **none** (interpreter)                | Interpreter resolves provider-scoped values during tree walk; consumer reads return the active scope's value. Interpreter shortcuts the lowering for clarity.                        |

## Test harness

A single generic primitive:

```sngl
t.setContext(name, value)
```

Lowering:

- The test harness wraps each `t.mount(...)` call in a synthesized
  provider for every context the test has called `setContext` on so far.
- Subsequent `setContext` calls trigger a re-render with the new value,
  using the same reactive path as a normal provider value change. The
  values are stored in test-harness vars; the synthesized provider's
  value expression reads them.
- Implementation is identical across all test runners (none, fyne,
  bubbletea, android). Replaces the current `// TODO` stubs in
  `codegen/lang/golang/testlower.go` and `codegen/lang/kotlin/testlower.go`
  for the `Test.*` namespace.

`t.setLocale` is dropped from the test surface. Existing test fixtures
calling `t.setLocale("…")` are migrated to `t.setContext(locale, "…")`.

## i18n migration

### Stdlib

Add to `lib/i18n.sngl`:

```sngl
context #locale(i18n.defaultLocale())
```

`i18n.defaultLocale()` is a pure stdlib function returning the
process-startup locale string (env-driven: `LC_ALL` → `LC_MESSAGES` →
`LANG` → `"en-US"`). It is constant for the lifetime of a process, so the
context-decl constant-expression rule is satisfied.

### Function lowering

The i18n functions listed above gain an implicit final `locale string`
parameter sourced from the `locale` context. Source-level call sites do
not change. The checker marks these functions as readers of `locale`
and the `NoContext` pass threads it like any other consumer.

### Runtime packages

- **`pkg/go/i18n`** — drop any process-global locale state. Each i18n.*
  call constructs (or pulls from a `sync.Map` cache keyed by locale
  string) a `Translator` for the passed-in locale and dispatches.
- **`pkg/js/i18n`** — same shape. ICU template formatter takes `locale`
  as an arg instead of reading a module-level current locale.
- **`pkg/kotlin/i18n`** — drop `I18n.init(context)` injection in
  `MainActivity.onCreate`. Manifest is loaded lazily on first use (or
  via Application class if/when one is scaffolded). Per-call locale →
  `android.icu.text.*` formatter.

### Migration order

Handled in the implementation plan, not here. Sketch:

1. Land context primitive + `NoContext` lowering with non-stdlib test
   fixtures.
2. Add `context #locale(...)` to `lib/i18n.sngl`; mark i18n.* functions
   as context-readers in the checker.
3. Update each runtime package to accept per-call locale.
4. Migrate existing fixtures referencing `t.setLocale(...)` to
   `t.setContext(locale, ...)`.

## Testdata fixtures

Per CLAUDE.md, fixtures drive the implementation. Planned coverage:

### Parse / format round-trip

- `context_decl_basic.sngl` — single context decl at top level.
- `context_decl_inferred_type.sngl` — defaults of string, int, struct,
  enum types; each correctly inferred.

### Checker

- `context_basic.sngl` — decl + provider + consumer; type-check passes.
- `error_context_not_top_level.sngl` — decl inside a component.
- `error_context_non_const_default.sngl` — default references a window
  state var.
- `error_context_as_func_arg.sngl` — pass context name to a func.
- `error_context_assign.sngl` — assign to context name outside provider
  position.
- `context_nested_shadowing.sngl` — provider inside provider; inner
  subtree sees inner, outer sibling sees outer.

### Lowering / codegen

- `context_lower_hidden_prop.sngl` with `// FOLD(...)` assertions on
  post-`NoContext` IR: verifies reachability set, hidden-param
  synthesis, root-default injection.
- `context_reactive_value.sngl` — provider value depends on a toggle
  var; consumer updates on toggle.
- `context_for_loop_consumer.sngl` — provider wraps a `for` of
  components reading the context.
- `context_if_branch_consumer.sngl` — consumer inside an `if` under a
  provider.

### i18n migration

- `context_i18n_locale_override.sngl` — outer locale en-US, inner
  subtree locale es-MX; date/number/tr formatters reflect each scope.
- Update existing i18n fixtures that asserted on a fixed process locale.

### Test harness

- `context_test_setContext.sngl` — uses `t.setContext(locale, "es-MX")`
  inside a test block; passes on all four runners.
- Replace `t.setLocale(...)` calls in existing fixtures with
  `t.setContext(locale, ...)`.
