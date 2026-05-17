# Polymorphism via Structural Interfaces

**Status:** Designed

## Background

SNGL has methods (receiver functions on structs, components, enums, units,
and built-in `list<T>`/`map<K,V>`), generics on structs and methods, and an
async-color analysis that propagates `IsAsync` over the call graph and
through funcvar slots via points-to (see `internal/checker/async.go`,
`ir/color.go`). It does not have a polymorphism primitive that lets a
program write code against an abstract surface and substitute concrete
implementations.

The motivating pressure is theming. The current workaround is one reactive
context value per themed component, holding a function that describes how
to draw it. With many themed components, the context system becomes a bag
of unrelated function values. A theme should be a single value whose
shape is the set of behaviors it provides.

A secondary motivator is mocking application logic in tests. Today this is
hacked around with import-time substitution.

The async-color analysis is the key complication. Concrete implementations
of the same abstract method may be sync or async, and a sync caller cannot
silently inherit async coloring without telling the rest of the program.

## Goals

1. First-class abstract types whose values can be stored, passed, and
   returned, with method calls dispatched to the underlying concrete value.
2. Structural conformance — any concrete type whose method set matches the
   abstract surface conforms, with no opt-in keyword.
3. Color polymorphism — an interface call's color is the union of its
   reachable implementations' colors, computed by extending the existing
   points-to + color propagation pass, not by adding new IR machinery.
4. Native host-language emission per target with one mental model: at the
   boundary between concrete and abstract, materialize a method table.
5. No null in the surface. Uninitialized interface fields have a defined
   zero-method behavior, not a runtime panic.

## Non-Goals (v1)

- Explicit `implements` keyword. (Considered for later as opt-in
  declaration-site conformance check.)
- Interface embedding (`interface B { A; foo() }`).
- Type-test `is` predicate returning `(value, ok)`. The cast form is the
  only assertion mechanism.
- Per-method color annotation on the interface (`func draw() async`) to
  lock conformance. The grammar permits it; v1 ignores it. Future hybrid.
- Use as a generic constraint (`func f<T: I>(x T)`). Interfaces are values,
  not bounds, in v1.

## Surface Syntax

Declaration:

```sngl
interface ButtonTheme {
    func draw(label string, pressed bool) Visual
    func minSize() Size
}
```

Generic:

```sngl
interface Container<T> {
    func get(i int) T
    func len() int
}
```

Interface name appears wherever a type appears:

```sngl
var theme ButtonTheme = roundedButton
func render(t ButtonTheme, label string) => t.draw(label, false)
```

Type assertion via the existing cast form `TargetType(expr)`:

```sngl
var t ButtonTheme = ...
var r = RoundedButton(t)    // returns zero of RoundedButton on mismatch
```

Method calls on an interface-typed receiver use the same `recv.method(args)`
syntax as concrete-receiver calls. No keyword distinguishes them at the
call site.

## Conformance Rules

The **method set of T** is the set of funcs declared with receiver `T`,
including generic-receiver methods instantiated at T's type arguments.

Type `T` conforms to interface `I` iff for every method `m(args) R` in
`I`, T's method set has a method `m` with:

- identical param types, positionally (param names ignored)
- identical return type
- color is unconstrained in v1 (see Color Polymorphism)

Conformance is checked at the point of assignment, argument passing, or
return where a `T`-typed value flows into an `I`-typed slot. Errors are
reported at the use site (structural), in checker phase `check`.

For generic interfaces, the expected-type's type arguments substitute the
interface's type parameters before signature comparison. `list<int>`
conforms to `Container<int>` when `list<T>` has the required methods after
binding `T = int`.

Interface names share the type namespace with structs, enums, units, and
components; collisions are checker errors. Interfaces cannot appear as
method receivers (`func ButtonTheme.helper()` is a check error).

## Color Polymorphism

Each interface method is emitted into IR with `FuncSig.Color = ColorParam`.
This is the existing color used for funcvar polymorphism, where the
concrete color is resolved per call site through points-to.

Points-to extension: assignment `iface = concrete_expr` records the static
type of `concrete_expr` into `pointsTo[iface]`. Aliased slots merge. A
method call `iface.m()` expands, for color analysis, into the union
`{ T.m | T in pointsTo[iface] }`. The enclosing function becomes async if
any `T.m` in that set is async — the same rule already used for funcvar
async calls in `ir/async.go` (`BlockHasFuncvarAsyncCall`).

Precision is per-slot, matching today's points-to granularity. Two
interface variables of the same type with disjoint points-to sets color
independently; aliasing one to the other merges their sets.

Conformance checking does not enforce color match in v1. A sync impl can
sit beside an async impl in the same interface; callers see the union
color.

Existing async-rule diagnostics (`internal/checker/async_rules.go`) fire
unchanged. A sync function that becomes async via an interface impl
flowing into a reactive context is rejected by Rule 2 just as a direct
async call would be.

## Zero Values

SNGL has no null. A function's zero value is a no-op that returns the
zero value of its declared return type. An interface's zero value extends
this: a value whose method-table entries are all zero-value functions.
Calling any method on a zero interface returns the zero value of that
method's return type. No runtime check is required at the call site.

Type assertion on a zero interface returns the zero value of the target
type, not a runtime panic. The assertion form is total.

## Runtime Representation

The unifying model is "interface value = method table built at the
boundary." Go gets this free from its native structural interfaces; JS and
Kotlin synthesize a table at each conformance site.

**Go.** Native `interface { ... }`. Generic interfaces use Go generics.
Zero value is the nil interface; a small emitted helper detects nil at
method dispatch and returns zero of the declared return type. Type
assertion uses the comma-ok form; on `!ok` the result is the zero value of
the target.

**JavaScript.** Interfaces are erased at the type level. At each
conformance site, codegen emits a method-bag literal:

```js
{ draw: (l, p) => t.draw(l, p), minSize: () => t.minSize(),
  __sngl_self: t, __sngl_type: "RoundedButton" }
```

Zero value is a singleton object whose method props are zero-value
functions. Cast `Concrete(iface)` checks `__sngl_type` against the target;
on mismatch returns zero of `Concrete`.

**Kotlin.** Interfaces emit as data classes of function references rather
than Kotlin `interface`s. Foreign types imported via `java://` cannot be
retro-annotated with `: ButtonTheme`, so nominal Kotlin interfaces would
exclude them. The data-class form is uniform:

```kotlin
class ButtonTheme(
    val draw: (String, Boolean) -> Visual,
    val minSize: () -> Size,
    val __sngl_self: Any? = null,
)
```

At each conformance site, codegen materializes the class with `::method`
references and stashes the original value in `__sngl_self`. Zero value is
a singleton with zero-value lambdas. Cast checks `__sngl_self::class`
against the target; mismatch returns zero of the target.

## AST and IR

New AST node `InterfaceDecl` at top level:

```go
type InterfaceDecl struct {
    Pos        Pos
    Name       string
    TypeParams []string
    Methods    []*InterfaceMethod
}

type InterfaceMethod struct {
    Pos    Pos
    Name   string
    Params []Param
    Return TypeExpr            // nil when no return value
}
```

Parser slot: top-level `interface` keyword. Params reuse the existing
`Param` shape from func decls; return type reuses `TypeExpr`. No body.

New IR type kind `TypeInterface`:

```go
type Interface struct {
    Name       string
    TypeParams []*TypeParam
    Methods    []*FuncSig       // each .Color = ColorParam
}
```

Conformance set is computed by the checker and stored as
`map[*Type][]*Interface`, answering "what interfaces does this type
satisfy?" Used for assignment/argument-pass checks and for error message
construction.

Points-to extension reuses the existing `pointsTo` map keyed by SSA
variable / slot; no new structure.

## Checker Passes

Additive only; no reordering.

1. **Decl registration.** Register `InterfaceDecl` into the type namespace
   alongside structs/enums/units/components. Resolve method param and
   return types, allowing forward references.
2. **Generic-param scope.** Bring interface type params into scope for
   method signature resolution.
3. **Conformance index.** New pass after type-resolve, before expr-check.
   For each `(T, I)` pair reachable in the program, compute conformance
   once; cache. Drives use-site error messages with missing-method lists.
4. **Expr-check additions.**
   - Assignment / argument pass into an interface slot consults the
     conformance index.
   - Method calls on interface-typed receivers resolve through the
     interface's method set; emit IR call with `Color = ColorParam`.
   - Cast `Concrete(iface_expr)` is statically valid when `Concrete`
     conforms to the operand's interface; otherwise it is a check error
     (statically impossible). At runtime, a cast whose dynamic value is
     not of the target type returns the zero value of the target.
5. **Points-to (existing pass, extended).** Flow concrete static types
   into interface slots; merge on aliasing.
6. **Async analysis (existing).** Already handles `ColorParam` through
   points-to. Interface calls inherit the analysis once points-to is
   extended.
7. **Async-rule diagnostics (existing).** Unchanged.

Fixture directives continue to use `// ERROR(check) "..."`.

## Codegen

`codegen/codegen.go` interfaces are unchanged. `AnalyzeCommon` extends to
surface declared interfaces, per-type conformance sets, and per-slot
materialization sites; both `MutationModel` and `RenderModel` consume the
same analysis.

Per-language translator work:

- **Go (`codegen/lang/golang`).** Emit native `interface`s; rely on Go's
  structural conformance. Zero-nil helper. Comma-ok casts returning zero
  on `!ok`.
- **JavaScript (`codegen/lang/javascript`).** Erase interface types.
  Emit method-bag materialization at conformance sites. Zero-method
  singleton. Tag-check casts returning zero on mismatch.
- **Kotlin (`codegen/lang/kotlin`).** Emit data class of fun refs;
  materialize at conformance sites with `::method` references. Singleton
  zero value. Class-check casts on `__sngl_self`.

Platforms (html, bubbletea, fyne, android, none) are not impacted —
interfaces flow through IR like any other type and are emitted by the
language translator.

Runtime support packages: `pkg/<lang>/iface/` per target where applicable,
containing zero-of-T helpers and tag/class-check utilities.

## Testing

Fixtures under `testdata/`:

- `check_interface_basic.sngl` — declare, conform, call through var.
- `check_interface_structural.sngl` — implicit conformance.
- `check_interface_generic.sngl` — `Container<int>` via `list<T>`.
- `check_interface_cast.sngl` — cast success + zero-on-mismatch.
- `check_interface_zero.sngl` — zero-value method calls.
- `error_interface_missing_method.sngl` — `// ERROR(check) "type Foo does not satisfy Bar: missing method baz"`.
- `error_interface_sig_mismatch.sngl` — arity / return-type errors.
- `error_interface_self_recv.sngl` — interface as receiver rejected.

Async fixtures (load-bearing):

- `check_interface_async_propagation.sngl` — sync caller becomes async
  when an async impl flows into the slot.
- `check_interface_async_isolated.sngl` — two same-typed interface vars,
  only the one with an async impl colors its caller.
- `check_interface_async_alias.sngl` — `b = a` merges points-to sets;
  both color.
- `error_interface_async_reactive.sngl` — async-via-interface call in a
  parameterized reactive context (existing Rule 2).

Codegen txtar fixtures under `cmd/sngl/testdata/` exercise round-trip
emission for Go, JS, and Kotlin.

Runtime tests under `pkg/js/iface/` and `pkg/kotlin/iface/` cover
zero-value behavior and cast tag-checks.

## Rollout

1. Parser, AST, formatter (no semantics).
2. Decl registration and name resolution.
3. Conformance index and structural check.
4. Interface as type in slots; method-call resolution.
5. Points-to extension for interface slots.
6. Async propagation validated end-to-end.
7. Cast / type-assertion.
8. Codegen, in order: Go, JavaScript, Kotlin.
9. Stdlib audit — defer reifying existing stdlib method sets as
   interfaces unless a concrete consumer needs it.
10. Theming example as integration fixture.

## Open Future Work

- Explicit `implements` keyword for declaration-site conformance check.
- Interface embedding.
- Per-method color annotation on interfaces to lock conformance.
- Interfaces as generic constraints (`func f<T: I>(x T)`).
- `is`-style predicate for non-fatal type tests if the zero-on-mismatch
  cast proves insufficient in practice.
