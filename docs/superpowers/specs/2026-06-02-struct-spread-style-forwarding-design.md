# Struct Spread Lowering + Explicit `...style` Forwarding — Design

**Date:** 2026-06-02
**Status:** Draft

## Goal

Make a caller's `style` reach the element a stdlib wrapper renders, the
**explicit** way: platform `.sngl` bodies forward `...style` onto their
root element, and the compiler flattens that spread during lowering.

Today stdlib wrappers (`vbox`, `text`, …) declare `style Style` but their
platform bodies render only structural style (`html.div(style={display= "flex", flexDirection="column"})`) and never reference the param — so the
caller's `style={gap=10, padding=16, background=#f0f2f5}` is dropped, and
HTML output comes out unstyled. The wrappers need to forward it.

The forwarding idiom is struct spread (`html.div(style={...style, display= "flex", flexDirection="column"})`), but **struct spread is currently
half-broken**: it parses and type-checks (`ir.FieldInit{Spread:true}`),
survives to codegen, then:

- the HTML/CSS builders (`htmlutil.BuildCSSStyleIR` / `BuildCSSStyle`)
  ignore spread fields entirely;
- Go struct emission (`codegen/lang/golang/translate_ir.go`) emits a
  dropped `/* ...operand */` comment;
- Kotlin emission similarly drops it;
- only JS emits a real `...operand`.

So this design does two things:

1. Add a **`flatten_struct_spread` lowering pass** that eliminates every
   struct-literal spread before codegen — compile-time for literal
   operands, a generated runtime `merge<Struct>` call for opaque ones.
2. **Forward `...style`** from every platform wrapper body onto its root
   element, across html / gtk4 / fyne / android.

This is the "B" approach: no magic `style` property special-cased in the
compiler. Forwarding is data in the `.sngl` body; the platform author
chooses precedence by spread position.

## Non-goals

- List spreads (`[...xs]`), call-argument spreads (`f(...xs)`), and map
  literals. This pass touches **struct-literal field spreads only**.
- Presence tracking (per-field "was set" bits / optional fields). We use
  the zero value as the "unset" sentinel; see Merge Semantics.
- Making every native toolkit *render* every CSS-ish style prop. Native
  forwarding must type-check and is best-effort at render; full
  per-toolkit style application is out of scope.
- Dynamic/reactive style values. Reactivity of field *values* is
  orthogonal and handled by the existing reactivity pass (which runs
  after this one).

## Background: what a spread is in IR

- A `#rrggbb` color literal lowers (in `checker.lowerHexLiteral`) to a
  `*ir.StructLit{Def: color, Fields: [r,g,b,a]}`.
- A struct literal `{a=1, ...op, b=2}` is an `*ir.StructLit` whose
  `Fields` include `FieldInit{Spread:true, Value: op}` in source order.
- The checker requires a spread source to be the **same struct type** as
  the literal target (`internal/checker/expr.go`).
- `passInlinePure` / `passNoInlineComponents` substitute component params
  before reactivity. After inlining, a wrapper's `...style` operand —
  originally the `style` param — is already the caller's concrete struct
  literal. (Verified via `dump optimized`: the inlined `html.div` carries
  a `Spread:true` field whose value is the caller's `{gap, padding, …}`.)

## Merge Semantics

The rule (decided with the user):

- An **explicit** field (`display="flex"`, or `padding=0` written on
  purpose) is set **unconditionally**; later explicit writes win.
- A **spread** (`...op`) contributes each field that is *present* in `op`.
  What "present" means depends on the operand:
  - **literal operand** (`...{…}`): present = **written in the literal**.
    A struct literal's `Fields` contains only the fields the author wrote
    (unwritten fields are simply absent), so a literal spread splices
    exactly those — `...{a=0}` splices `a=0`. There is **no** skip step
    for literals; an unwritten field isn't there to skip.
  - **opaque runtime operand** (`...someVar`): every field of the type has
    *some* value, so present = **not the unset sentinel** — `!= null` for
    `option<T>`, `!= type-zero` for plain fields (fallback). This is where
    the merge decision lives.

The explicit-vs-spread distinction is **syntactic / compile-time**, so no
runtime provenance data is needed: explicit fields become direct field
assignments, literal spreads splice written fields, opaque spreads become
`merge<Struct>` calls.

Worked examples:

| Source                          | Result                                       | Why                                                                  |
|---------------------------------|----------------------------------------------|----------------------------------------------------------------------|
| `{a=1, ...{a=0, b=2}}`          | `{a=0, b=2}`                                 | literal spread splices both written fields; `a`: 1 then 0, last wins |
| `{a=1, ...{a=5}}`               | `{a=5}`                                      | literal spread overrides `a`                                         |
| `{...{a=1}, a=0}`               | `{a=0}`                                      | explicit `a=0` is unconditional, wins (last)                         |
| `{...style, display="flex"}`    | caller style + locked `display`              | structural after spread → locked                                     |
| `{padding="2px 8px", ...style}` | caller `padding` wins if set                 | spread after default → caller wins                                   |
| `{...runtimeStyle}` (opaque)    | only `runtimeStyle`'s non-null option fields | runtime merge: `null`-skip                                           |

**The canonical "unset" is `option<T> == null`.** The most important
zero-case — and the one `Style` depends on — is the optional. A struct of
`option<T>` fields distinguishes *unset* (`null` / `None`) from
*explicitly set to the zero value* (`Some(0)`), so "non-zero = override"
becomes simply **`field != null`**. The merge generator must treat any
`option<T>` field's `null` as no-replace, and a non-null option (including
`Some(0)`) as a replace. `Style` is expected to migrate its fields to
`option<T>` precisely so a spread of a runtime `Style` merges correctly;
see Dependencies.

**Non-option fields (documented limitation):** for a plain-typed field
(a struct that has *not* moved to `option<T>`), "unset" can only be
approximated by the type zero (`0` / `""` / `false` / zero-struct), so a
field whose legitimate value *is* the zero value can't be set through a
spread of a *runtime* value. This does **not** affect:
- the **compile-time literal path** — a struct literal only carries the
  fields actually written, so splicing its present fields is exact
  regardless of option-ness (this is the common `...style` wrapper case);
- **`option<T>` fields** — `Some(0)` is non-null, so it always applies.

Writing the field explicitly always works. The limitation is purely the
runtime-spread-of-a-plain-typed-struct corner, which `Style`'s migration
to options removes for style.

## Component 1: `flatten_struct_spread` lowering pass

**File:** `internal/lower/flatten_struct_spread.go`
**Pipeline position:** in `internal/lower/lower.go`, after
`passNoInlineComponents` and before `passReactivity`. After inlining (so
`...style` operands are concrete literals) and before reactivity (so
reactive style fields are already flat when reactivity analyzes them).

### Capability gate

The pass is gated by a new `Caps.NoStructSpread bool` flag, following the
existing convention (`No<Feature>` = "this target cannot consume
`<Feature>` directly → run the pass that removes it"; flags OR-merge):

```go
var passFlattenStructSpread = pass{
	name:    "NoStructSpread",
	enabled: func(c Caps) bool { return c.NoStructSpread },
	apply:   lowerFlattenStructSpread,
}
```

The **absence** of the flag (default zero value `false`) is the
"codegen handles struct spreads directly" capability. A target that can
process `ir.Spread` itself — honoring the zero-skip merge semantics —
leaves it false and consumes the un-flattened literal.

**Polarity / where it's set:** the flag is set at the **platform** level
(each platform's `Capabilities()`), not the language level. Because
`Caps.Merge` ORs flags, once any layer sets `true` it can never be unset;
if a language hard-set it, a future spread-aware platform could never opt
out. Setting it only on platforms keeps that door open.

All current platforms set `NoStructSpread: true` (html, bubbletea, fyne,
gtk4, android, none) — none of them implement zero-skip spread handling
today, so all flatten. The first opt-out is anticipated future work:

> **Forward use — CSS generation for HTML.** A future HTML mode that emits
> CSS rules/classes instead of inline `style` attributes wants to *see*
> the spread structure (which fields come from the base vs the override)
> so it can map the merge onto the CSS cascade (e.g. layered classes,
> later-wins) rather than a pre-flattened struct. That mode leaves
> `NoStructSpread` false and consumes `ir.Spread` directly, taking
> responsibility for the zero-skip semantics in CSS terms. The flatten
> path and the generated `merge<Struct>` functions (Component 2) belong to
> the flattening targets; a spread-aware codegen uses neither.

Note: language-native object spread (e.g. JS `{...a, ...b}`) is **not**
an acceptable "handles spreads" implementation — it overwrites with all
fields including zeros, which violates zero-skip. "Capable" means the
codegen implements the zero-skip merge, not that the language has a spread
operator. That is why no current target opts out.

The pass walks every expression in the package (component bodies,
window bodies, var inits, func bodies, node props, timers — reuse the
existing expr-walker used by sibling passes) and rewrites each
`*ir.StructLit` that contains at least one `FieldInit{Spread:true}`.

### Rewrite algorithm (per struct literal)

Process fields in source order into an ordered accumulator. Track whether
the literal is **statically resolvable** (all spread operands are
literals) or requires a **runtime sequence** (any opaque spread).

**Fully-static case** — produce a flat `*ir.StructLit`:

- ordered map `name -> Value` (preserve first-seen position; last write
  updates value).
- explicit field `f=v`: `set(f, v)`.
- spread of literal `...{…}`: recursively flatten it first, then `set(g, w)` for **every written field** `g=w`. No skip step — a literal carries
  only the fields the author wrote, and presence is intent (`...{a=0}`
  sets `a=0`). Later writes win, so order is preserved.
- emit a `StructLit` with no spread fields.

**Runtime case** — hoist a synthesized local and build it with a sequence
of statements, then replace the original struct-literal expression with a
reference to that local. The construction sequence applies fields in
source order:

```
__spr := StructLit{<explicit fields before the first opaque spread, with
                    any preceding literal spreads already folded in>}
__spr = merge<Struct>(__spr, op1)   // first opaque spread (zero-skip)
__spr.f = v                         // an interior explicit field (unconditional)
__spr = merge<Struct>(__spr, op2)   // second opaque spread (zero-skip)
__spr.g = w                         // a trailing explicit field (unconditional)
// the literal expression is replaced by: __spr
```

The key invariant: **explicit fields are direct field assignments**
(`__spr.f = v`), never routed through `merge<Struct>` — that is exactly
what keeps them unconditional (so explicit `=0` applies), while spreads go
through the zero-skip merge. `merge<Struct>(...)` is an ordinary `*ir.Call`
to a generated function (Component 2).

Hoisting mechanics: introduce a synthesized immutable-after-construction
local in the **enclosing statement** context (precedent: `passComputed`,
`passLambda` already hoist synthesized locals). Insert the construction
statements before the current statement; replace the struct-literal
expression with an `Ident` referencing the local. When the struct literal
sits where there is no clean statement anchor (a deeply nested
sub-expression), hoist to the nearest enclosing statement. If a context
genuinely has no anchor, that surfaces as a concrete case to handle — not
a silent miscompile (see Risks).

> Implementation note: the common real case — `...style` after inlining —
> is **always the fully-static case**, so the runtime/hoisting path is
> exercised only by genuine opaque spreads (`div(style={...computeStyle(), color=red})`, `Config{...base, timeout=30}` with `base` a runtime var).
> Build the static path first; the runtime path second.
>
> Omitted optional params are safe: a caller that omits `style` binds it
> (via `ir.ZeroExpr`) to an **empty** struct literal `Style{}` (`Fields: nil`), so `...style` becomes `...{}` and splices nothing — structural
> style is untouched. "Splice every written field" never clobbers, because
> an omitted param contributes zero written fields.

### Edge cases

- Nested/chained spreads `{...a, ...b}`: flatten/merge left to right.
- Spread operand that is itself a spread-containing literal: recurse
  before splicing.
- A spread whose operand has a different shape than the target: cannot
  occur — the checker already requires same struct type.
- Color fields: a `color` value is a nested `StructLit`. On the **static
  path** a written color field is spliced as-is (presence = intent; no
  zero compare). The deep-equal-to-zero question arises only on the
  **runtime plain-fallback path** (and disappears once the field is
  `option<color>`, where the test is just `!= null`) — see Component 2.

## Component 2: generated `merge<Struct>` runtime functions

Demand-driven: emitted **only** for struct types spread by an opaque
operand somewhere in the program. The lowering pass records each such
struct type in a set on the package (e.g. `pkg.SpreadMergeStructs`); each
language codegen reads it and emits one function per struct.

**Contract:**

```
merge<Struct>(base Struct, ov Struct) Struct:
    if ov.f1 != zero(T1): base.f1 = ov.f1
    if ov.f2 != zero(T2): base.f2 = ov.f2
    ...
    return base
```

**Zero detection per field type** (the "skip when…" test):

| Field type                    | skip (no-replace) when               | notes                                                                      |
|-------------------------------|--------------------------------------|----------------------------------------------------------------------------|
| **`option<T>`**               | **`== null` / `== nil` / `== None`** | **primary case; what `Style` depends on. `Some(0)` is non-null → applies** |
| int / float / duration / unit | `== 0`                               | plain-field fallback (limited)                                             |
| string / url / email / …      | `== ""`                              | plain-field fallback (limited)                                             |
| bool                          | `== false`                           | plain-field fallback (limited)                                             |
| nested struct (e.g. color)    | deep-equal to the struct's zero      | plain-field fallback (limited)                                             |
| enum                          | `== <zero variant>`                  | plain-field fallback (limited)                                             |

`option<T>` lowers to `*T` (Go, `nil`), value-or-`null` (JS), `T?`
(Kotlin) — so the null test is a direct, cheap comparison in every
target. The plain-field rows are the fallback for structs not (yet) using
options and carry the limitation noted in Merge Semantics.

**Per-language emission** (shown for `option<T>` fields, the `Style` case):

- **Go** (`codegen/lang/golang`): `func mergeStyle(base, ov Style) Style { if ov.Gap != nil { base.Gap = ov.Gap }; …; return base }` (option fields
  are `*T`). Plain-typed fields use the type-zero test (`!= 0`, `!= ""`);
  plain nested structs are comparable in Go when all their fields are
  (`ov.Color != (color{})`).
- **JS** (`codegen/lang/javascript`): `function mergeStyle(base, ov) { const r = {...base}; if (ov.gap != null) r.gap = ov.gap; …; return r }`
  (`!= null` catches both `null` and `undefined`). Plain fields use the
  type-zero test.
- **Kotlin** (`codegen/lang/kotlin`): `fun mergeStyle(base: Style, ov: Style): Style = base.copy(gap = ov.gap ?: base.gap, …)` (the `?:`
  elvis is exactly null-skip for `T?` fields). Plain fields use an
  `if (ov.f != <zero>) ov.f else base.f` form.

**Limitation / loud failure:** if a struct needing a runtime merge has a
**non-comparable** field (slice/map) in a language where that blocks the
zero test, emit a clear codegen error naming the struct and field rather
than producing code that won't compile. (No such struct exists today;
`Style` and `color` are all comparable.)

## Component 3: remove per-backend spread emitters

After the pass, no `FieldInit{Spread:true}` reaches struct-literal
emission (explicit fields are plain literal fields; opaque spreads are
plain `merge` calls). Remove the `if f.Spread` arms:

- `codegen/lang/golang/translate_ir.go` (the `/* ...operand */` comment)
  and any sibling in `ircontext.go` / `helpers_emit.go` / `http.go`.
- `codegen/lang/kotlin/ircontext.go`.
- `codegen/lang/javascript/javascript.go`.

Replace each with a `panic`/assert ("struct spread should have been
lowered") so a future leak is loud, not a silently dropped style.

The CSS builders (`htmlutil.BuildCSSStyleIR` / `BuildCSSStyle`) likewise
no longer need to consider spread fields — by the time they run, the
style `StructLit` is flat. (They already skip `f.Name == ""` spread
fields; that path becomes dead and can be dropped.)

## Component 4: `.sngl` wrapper bodies forward `...style`

Add `...style` to each stdlib wrapper's root element, position chosen for
precedence intent. Across `codegen/platform/html/html.sngl`,
`codegen/platform/gtk4/gtk4.sngl`, `codegen/platform/fyne/fyne.sngl`,
`codegen/platform/android/android.sngl`.

Precedence rule for authors:

- **Lock** a structural prop the caller must not override → place it
  **after** `...style`: `html.div(style={...style, display="flex", flexDirection="column"})`.
- **Default** a structural prop the caller may override → place it
  **before** `...style`: `html.span(style={padding="2px 8px", borderRadius=12, ...style})`.

Every wrapper with a `style Style` param and a root element gets the
forward. Wrappers with multiple structural children (radio, tabs, …)
forward onto the outermost root only.

**Open dependency — native `style`-prop acceptance:** raw native elements
(`gtk4.GtkBox`, fyne/Compose widgets referenced in the bodies) must accept
a `style` prop or the checker errors "unknown prop." Today `style` never
reaches them. For each native platform, verify the raw element accepts
`style`; if it does not, that element needs a `style` param (or a
per-toolkit application step). Where a toolkit cannot consume a given prop
yet, forwarding type-checks and is a render no-op (not a regression).
This is surfaced per platform during implementation, not hacked around.

## Component 5: remove the A stopgap, keep the color fix

- **Remove** `forwardStyle` and its call site in
  `internal/lower/inline_pure.go`, and the `forwardStyle` helper in
  `internal/lower/inline_components.go`. B replaces it.
- **Keep** `colorStructToCSS` in `internal/htmlutil/helpers.go`. It is
  independent of A/B and still required: under B the caller's color still
  arrives as a `color{r,g,b,a}` struct (constant folding strips its
  `Type`/`Def`, so detection stays structural — fields r,g,b[,a] of static
  ints → `#rrggbb` / `rgba()`).

## Testing

- **Lowering fixtures** (`testdata/*.sngl`, driven by `internal/testutil`):
  - literal spread folds at compile time, splicing written fields (`// FOLD`
    / dump assertion): `{a=1, ...{a=0,b=2}}` → `{a=0,b=2}` (last wins);
  - explicit `=0` applies: `{...{a=1}, a=0}` → `{a=0}`;
  - nested/chained spreads;
  - opaque spread lowers to a `merge<Struct>` call (dump assertion);
  - runtime null-skip: `merge<Struct>` leaves a `null`/`nil` option field
    untouched and applies a `Some(0)` field (per-language merge-fn test).
- **Capability gate**: with `NoStructSpread` false, the pass is a no-op
  and `ir.Spread` survives lowering (a `lower_test.go` pass-list assertion
  plus an IR check that the spread field is preserved).
- **Per-language golden** (`go`/`js`/`kotlin`): a struct built with an
  opaque spread emits a `merge<Struct>` call + the generated function; no
  `...` / no `/* ... */` comment survives.
- **HTML DOM cases** (`codegen/platform/html/component_dom_browser_test.go`,
  already added): `vbox-style` (gap + background merge) and `text-color`
  (`#hex`) — now driven through `...style`, not the removed magic.
- **Cross-platform type-check**: every wrapper body type-checks on html,
  gtk4, fyne, android after adding `...style`.
- **End-to-end**: the tour "Building a Component Library" lesson seed
  compiles to HTML with all caller styles applied (background, gap,
  padding, border-radius, font-size, font-weight, color).
- Full suite green (modulo the pre-existing, unrelated `TestDocSNGLFormat`
  tour.md formatting drift).

## Dependencies

- **`Style` → `option<T>` fields (related, not blocking).** Today `Style`
  uses plain types (`measurement`, enums, `color`, …), so a *runtime*
  spread of an opaque `Style` would hit the plain-field zero limitation.
  Migrating `Style`'s fields to `option<T>` makes "unset" an explicit
  `null` and is what gives runtime style merges correct semantics. That
  migration is its own effort (it touches every codegen that reads `Style`
  fields). This design does **not** block on it: the `...style` wrapper
  forwarding goes through the compile-time literal path, which is exact
  regardless. The `merge<Struct>` generator is built option-aware now
  (`null`-skip as the primary case) so no merge changes are needed when
  `Style` migrates.

## Implementation order

1. Add `Caps.NoStructSpread` (+ `Merge` + `String`); set `true` on all
   current platforms' `Capabilities()`. Then `flatten_struct_spread` pass
   — static path only; fixtures for literal spread + zero-skip +
   explicit-zero. (Unblocks the `...style` wrapper case, which is always
   static after inlining.)
2. Wire `...style` into `html.sngl`; remove the A magic; confirm the
   lesson renders. (Color fix already in place.)
3. Runtime path: `merge<Struct>` codegen (Go, then JS, then Kotlin) +
   `pkg`-level needed-struct set + hoisting; opaque-spread fixtures.
4. Remove per-backend spread emitters, replace with asserts.
5. Native wrapper bodies (gtk4 / fyne / android) + resolve the
   `style`-prop acceptance dependency per platform.

## Risks

- **Runtime merge codegen across three languages** is the bulk of the
  work; nested-struct zero detection (color) is the fiddly part.
- **Native `style`-prop acceptance** may expand step 5 if raw elements
  reject `style`.
- **Expression-to-statement hoisting** for the runtime path must reuse
  existing lowering hoist machinery; if no clean anchor exists in some
  context, that surfaces as a concrete case to handle rather than a
  silent miscompile.
