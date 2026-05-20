# Funcs Inside Type Definitions

Issue: [#75](https://git.duckfam.us/jonathan/sngl/-/issues/75)
Date: 2026-05-20

## Summary

Allow `func` declarations inside `struct`, `enum`, and (already permitted) `component` bodies. Nested funcs are **methods** on the enclosing type with an implicit `this` receiver. The existing top-level `func T.foo(...)` form remains a first-class peer — useful for extension methods declared outside the type's defining file and for cases where static-call ergonomics (`int.add(1, 2)`) read better than method-call (`1.add(2)`).

## Motivation

Today, attaching behavior to a type requires the top-level `func T.foo(v T, ...)` form, which separates the method's declaration from the type's body. Components already allow nested `func` declarations; extending the same affordance to `struct` and `enum` (and aligning the top-level form across all type-decl shapes) gives a uniform model and lets type authors keep related declarations together.

## Design

### Three declaration forms

For any user-defined type `T` (struct, enum, or component):

| Form                                                         | Kind   | Receiver                    | Where allowed                     |
|--------------------------------------------------------------|--------|-----------------------------|-----------------------------------|
| `func T.foo(v T, ...)` (top-level, first param has type `T`) | method | explicit param `v`          | anywhere (same file or extension) |
| `func T.foo(...)` (top-level, no `T`-typed first param)      | static | none — call as `T.foo(...)` | anywhere                          |
| `T { func foo(...) }` (nested)                               | method | implicit `this`             | inside the type's defining body   |

Primitives (`int`, `float`, `string`, `bool`) have no body to nest in, so they only support the top-level forms (unchanged from today).

### Examples

```sngl
struct Point {
    x, y int

    func magnitude() float => math.sqrt(this.x*this.x + this.y*this.y)
    func translate(dx, dy int) {
        this.x += dx
        this.y += dy
    }
    // bare field + bare sibling call (this.-elision)
    func describe() string => "({x},{y}) mag={magnitude()}"
}

// Extension method declared in another file or after a using-site:
func Point.dot(p Point, q Point) float => p.x*q.x + p.y*q.y

// Static utility on the type namespace (no method receiver):
func Point.origin() Point => Point{x=0, y=0}
```

```sngl
enum Status { ok, err
    func isOk() bool => this == Status.ok
}
```

```sngl
component counter {
    var n = 0
    func increment() { n += 1 }      // bare `n`; equivalent to `this.n += 1`
    func reset()     { this.n = 0 }  // explicit `this` allowed
}

// Component static (callable as counter.label()):
func counter.label() string => "counter widget"
```

### `this` semantics inside nested methods

1. **Binding.** The checker prepends a synthetic parameter named `this` of type `T` (or `T<typeparams>` for generic types) to the method's `Params`. The IR-level method is indistinguishable from a top-level explicit-recv method whose first parameter happens to be named `this`.
2. **Mutability.** `this.x = ...` is permitted whenever direct field mutation through any other ref would be — i.e. struct/component field mutation works as it does today.
3. **`this.`-elision.** Inside the body, identifier resolution proceeds innermost-out:
   - local vars / params (including `this` itself);
   - field/member of `T` → resolves as `this.<name>`;
   - sibling method on `T` → call rewrites to `this.<name>(args)`;
   - outer scope (enclosing package, stdlib).
4. **Shadowing.** `this` is a normal identifier in the method's scope and may be shadowed by an inner declaration of the same name. This matches the existing convention for magic identifiers in component bodies.
5. **Outside a nested method body**, `this` is an unbound identifier and the checker reports the normal "undeclared identifier" error.

### Grammar deltas

- `StructDecl` body: allow `FuncDecl` interleaved with `StructField`, separated by semicolons (requires multiline form). Single-line struct literals remain field-only.
- `EnumDecl` body: the existing `ArgList` rule already accepts `,` or `;` as separator. Extend the production so each element may be `EnumMember | FuncDecl`. Multiline form remains optional but is idiomatic when funcs are present.
- `ComponentDecl`: no change. `FuncDecl` is already accepted in component bodies.
- Top-level `FuncDecl`: no grammar change. `func myComp.foo()` already parses today — only checker support is added for components.

EBNF sketch:

```ebnf
StructDecl = kw_struct [ ident ] [ TypeParamList ] lbrace
             [ StructBodyItem { semi StructBodyItem } [ semi ] ] rbrace .
StructBodyItem = StructField | FuncDecl .

EnumBodyItem = EnumMember | FuncDecl .
EnumDecl     = kw_enum [ ident ] lbrace [ EnumBodyItem { (comma | semi) EnumBodyItem } [ comma | semi ] ] rbrace .
```

### AST changes

Replace the separate field/member slice on each type-decl with a single ordered slice of body items, accessed through a sealed interface. This preserves source order intrinsically — the formatter just walks the slice and dispatches on type, no position tracking needed.

```go
// ast/ast.go

// StructBodyItem is one declaration inside a struct body: a field or a func.
type StructBodyItem interface {
	structBodyItem()
}

func (*StructField) structBodyItem() {}
func (*FuncDef) structBodyItem()     {}

type StructDef struct {
	Pos         Pos
	Name        string
	TypeParams  []string
	Body        []StructBodyItem // fields and funcs in source order
	IsMultiline bool
}

// EnumBodyItem is one declaration inside an enum body: a member or a func.
type EnumBodyItem interface {
	enumBodyItem()
}

func (*EnumMember) enumBodyItem() {}
func (*FuncDef) enumBodyItem()    {}

type EnumDef struct {
	Pos         Pos
	Name        string
	Body        []EnumBodyItem // members and funcs in source order
	IsMultiline bool
}
```

(Sealed-interface pattern matches existing precedent in this codebase — `ast.ParamOrEventDecl` and `ast.Stmt`.)

Note `EnumMember` becomes pointer-receiver — its current value-receiver `enumBodyItem` would prevent embedding it in a slice of pointers without copies. The accessor uses pointer receiver to match `FuncDef`.

Helper accessors keep call-site ergonomics where the old slices were heavily used (parser builders, formatter, checker pass1):

```go
func (s *StructDef) Fields() []*StructField {
	out := make([]*StructField, 0, len(s.Body))
	for _, it := range s.Body {
		if f, ok := it.(*StructField); ok {
			out = append(out, f)
		}
	}
	return out
}
func (s *StructDef) Funcs() []*FuncDef { /* analogous */ }

// Same shape for EnumDef.
```

These accessors are *iteration helpers* only; mutation goes through `Body`. Used sparingly — most call sites switch to ranging over `Body` directly.

`ComponentDecl` already carries nested funcs in `Body.Stmts`; no AST change.

### Parser changes

- `buildStructDecl` (`internal/parser/build.go:255`): in the body loop, append a `*StructField` or `*FuncDef` to `s.Body` based on the non-terminal symbol. Reject non-multiline structs that contain funcs (semicolons are required for the multiline form).
- `buildEnumDecl` (`internal/parser/build.go:314`): switch from the `ArgList`-only path to a body iterator that appends `*EnumMember` or `*FuncDef` to `e.Body` per element.
- Formatter (`internal/parser/format.go`): `writeStructDef` / `writeEnumDef` walk `Body` in order and type-switch to the existing per-item writer (`writeStructField`, `writeEnumMember`, `writeFuncDef`). No position tracking, no merge-sort.

Existing callers across the codebase that read `StructDef.Fields` / `EnumDef.Members` migrate to the helper accessors (`s.Fields()`, `e.Members()`) or, where natural, to ranging over `Body` with a type-switch. The mechanical pass is in scope for this work.

### Checker changes

The checker desugars nested methods to the equivalent top-level explicit-recv form before symbol registration. No IR changes.

In pass1 type-decl registration (`internal/checker/checker.go:879` and surrounding):

1. For each `*FuncDef` element of `StructDef.Body` (and `EnumDef.Body`):
   - Synthesise a `ast.FuncDef` clone with:
     - `Name = T + "." + nested.Name` (sets the receiver via existing `SplitMethodName`).
     - A new first `Param{Name: "this", Type: <T or T<TypeParams>>}` prepended to `Params`.
     - `RecvTypeParams = T.TypeParams` when `T` is generic.
   - Register through the existing `c.symtab.RegisterMethod(T, fn)` path.
2. Collision check: nested func name must not match any field/member of `T`, nor any other nested func on `T`, nor any top-level method already registered on `T`. Error: `duplicate declaration of "foo" on struct/enum/component S`.

Nested funcs whose name starts with `test` are not rejected — test discovery is document-level (`Document.TestFuncs` walks `Document.Stmts`), so methods named `testFoo` simply aren't picked up as tests. No special-case enforcement.

For top-level `func T.foo(...)` where `T` is a user-defined type and no parameter has type `T` (matching by exact name; for generics, exact-name with any type-param instantiation), register as a **static** in `T`'s namespace. Call resolution: `T.foo(args)` looks the static up via the existing type-namespace lookup; instance calls `v.foo()` do not resolve to it.

For component receivers in top-level form: today `c.symtab.RegisterMethod` already accepts any string receiver; component name resolves through the type system as `ir.TypeComponent`. The new piece is recognising that `myComp.foo()` at a call site (with `myComp` referring to a component declaration, not an instance) resolves to a static. Implementation: in `checkSelectExpr` / call resolution, when the operand is an ident bound to a `*ir.Component`, look up `foo` in the component's method set; if it has no receiver parameter, treat it as a static call.

Within the synthetic-body scope for a desugared nested method:

- The synthetic `this` param is declared first.
- Identifier-resolution for bare `<name>` falls through to `this.<name>` when:
  - `<name>` is a field/member of `T` and not shadowed locally; or
  - `<name>` is a sibling method on `T` and is being called (the parser produces a call; the checker rewrites the call's `Func` from `IdentExpr{name}` to `SelectExpr{Operand: this, Field: name}` before normal call type-checking).

The rewrite happens at expression-check time, not as an AST pass, so source positions stay accurate for diagnostics and LSP.

### IR & codegen

No changes. The desugared form is structurally identical to a hand-written `func T.foo(this T, ...)` and emits through the existing method codepaths in every translator and platform generator.

### LSP

Hover and completion already understand top-level methods and struct fields. After desugaring, nested methods register through the same machinery and pick up hover automatically. Two follow-ups (small):

- The hover provider for a nested-method declaration should display its **source-form** signature (without the synthetic `this`), not the desugared one.
- Completion on `this.` inside a nested method should suggest fields and sibling methods.

These are tracked as part of the implementation plan, not separate spec items.

## Constraints & error cases

| Source                                                  | Phase | Message                                                |
|---------------------------------------------------------|-------|--------------------------------------------------------|
| `struct S { x int; func x() }`                          | check | `duplicate declaration of "x" on struct S`             |
| `struct S { func foo(); func foo() }`                   | check | `duplicate declaration of "foo" on struct S`           |
| `struct S { func foo() }` + `func S.foo() {}` top-level | check | `duplicate declaration of "foo" on struct S`           |
| `func main() => this` at doc scope                      | check | `undeclared identifier "this"`                         |
| Nested `func` in a single-line struct (no semicolons)   | parse | grammar requires multiline form when funcs are present |

## Test fixtures (driver, written before implementation)

- `testdata/test_struct_nested_methods.sngl` — field access, mutation, sibling call, `this`-elision.
- `testdata/test_enum_nested_methods.sngl` — enum with method using `this == E.member`.
- `testdata/test_component_nested_this.sngl` — verifies bare-field and `this.field` produce identical behavior in a component body.
- `testdata/test_top_level_static.sngl` — `func S.helper(...)` (no `S`-typed first param) callable as `S.helper(...)`.
- `testdata/test_extension_method.sngl` — explicit-recv top-level method declared in a separate file from its struct.
- `testdata/test_nested_generic_struct_method.sngl` — `struct box<T> { v T; func get() T => this.v }`.
- `testdata/error_nested_func_field_collision.sngl`
- `testdata/error_this_outside_method.sngl`
- `testdata/error_duplicate_nested_and_toplevel.sngl`

Per CLAUDE.md (`testdata/` is the source of truth for language features), these fixtures are added first and drive the parser / checker work.

## Out of scope

- Visibility modifiers (`pub`/`priv`) on nested funcs.
- Static funcs on primitive types (`func int.add(a, b int)` — already representable as a regular method with a non-int first param, or a free function in a package).
- Component instances as fully first-class values passable across packages — components are already `TypeComponent` internally, but expanding their value-semantics surface area is unrelated to this work.
- Reordering `this`-elision to allow shadowing fields by locals without warning — current behavior (locals win silently) is preserved; a future lint may flag the shadow.

## Risk & mitigation

- **Parser ambiguity in struct bodies.** `FuncDecl` and `StructField` share `ident` first-tokens (field name vs `func` keyword). The `func` keyword is a distinct lex token (`kw_func`), so dispatch is LL(1) — no ambiguity.
- **Formatter round-trip.** Single ordered `Body` slice (sealed interface) preserves source order intrinsically; the formatter walks and type-switches — no position tracking, no merge-sort.
- **Checker call-site churn.** Switching `Fields []*StructField` and `Members []EnumMember` to a single `Body` slice touches every consumer of those fields across the codebase. Mitigation: add `Fields()` / `Members()` / `Funcs()` accessor methods for read-only use so most call sites move with a one-line edit; only sites that mutate the slice need real refactoring.
- **Diagnostic positions after desugaring.** The synthetic `this` param has no source position. Errors that mention the receiver should use the enclosing type-decl's position, not a zero `Pos`. Implementation must thread the type-decl position into the synthetic param.

## Plan (high level — detailed plan to follow via writing-plans)

1. Write test fixtures (driver).
2. AST: replace `Fields`/`Members` with a single `Body []StructBodyItem` / `[]EnumBodyItem` sealed-interface slice; add read-only accessor helpers. Migrate existing readers across the codebase.
3. Grammar + parser: accept nested `FuncDecl` in struct/enum bodies; build into `Body`.
4. Formatter: walk `Body` and type-switch to existing per-item writers.
5. Checker pass1: desugar nested funcs to top-level explicit-recv method registration with synthetic `this`.
6. Checker expr: implement `this`-elision (bare field and bare sibling-call rewrite).
7. Checker: recognise no-receiver top-level `func T.foo()` as static in `T`'s namespace; support component `T`.
8. Collision checking against fields/members and existing top-level methods.
9. LSP follow-ups: hover signature without synthetic `this`; `this.` completion.
10. Verify with `go tool verify`; run docs site if any stdlib type benefits from migration.
