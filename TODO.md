# Constraints as builtin declarations

A plan for review. It replaces the open questions on `!285` about what a
constraint can say: a struct constraint grew functions there, and the next
asks -- a struct with any fields, the types of fields it does not name, a
body reading those fields (#309) -- each wanted another rule in the
language. Instead, constraints become declarations in `lib/builtin`, named
through aliases.

Nothing here is built.

## Where things stand

- A type parameter's constraint is `any` or an anonymous struct, and
  nothing else: `[T Named]` with `struct Named { name string }` is refused,
  "a type parameter's constraint is `any` or an anonymous struct"
  (`checker.resolveConstraint`). A constraint cannot name a declaration.
- `struct { … }` as a constraint means a type with fields, at least those
  ones, at equal types, and `struct {}` any type with fields -- a component
  included, which http's `queryProblem` then waves through as "refused by
  Q's own constraint".
- `!285` lets a struct constraint declare bodyless functions, which a type
  satisfies by a method taking a call of that shape, constant defaults
  matched (`methodUnsatisfied`, `matchDefaults`). A constraint naming only
  functions is satisfied by any type with those methods, `int` included --
  the asymmetry with `struct {}` this plan removes.

## 1. Aliases

```sngl
struct Formattable = methodsOf[struct {
    const func format() string
}]

struct Keyed[K any] = fieldsOf[struct {
    key K
}]

func show[T Formattable](v T) => "<{v}>"
```

An alias is written with the keyword of what it aliases, `struct Name = Type`, and is that type under another name: the same type everywhere, not a
new one. There is no `type` keyword. An alias may take type parameters,
which its right-hand side names.

For now an alias is of a constraint only: the constraint builtins are
declared as structs, so `struct` is the keyword that aliases them, and
an alias of any other type is refused. `enum Name = …` and `unit Name = …`
follow the same grammar when an alias of one is wanted.

It is exported by case and documented like any declaration, and the LSP's
hover shows the alias and what it stands for.

## 2. A constraint names a declaration

A constraint is a type expression whose type is a constraint: a constraint
builtin, or an alias of one. `resolveConstraint` resolves
it as a type and asks the declaration's builtin kind what it requires,
rather than matching two syntactic forms. Each builtin is declared with
`#[builtin("…")]` and found by its mark, never by name, so a program may
shadow one.

A bare struct is no constraint: `[T struct { name string }]` and
`[T Named]` are errors naming `fieldsOf`, and `[T struct {}]` one naming
`anyStruct`.

## 3. The constraint builtins

Declared in `lib/builtin`, so every file has them without an import: `any`
alone is written in about 330 constraints across 160 files (44 in `lib`,
the rest fixtures, goldens and examples), and a first generic should not
fail for want of an import.

| Builtin        | A type bound to the parameter must                                               |
|----------------|----------------------------------------------------------------------------------|
| `any`          | be anything: the spelling stays, now a declaration rather than a scalar          |
| `anyStruct`    | be a struct, whatever its fields                                                 |
| `anyFields`    | have fields: a struct or a component                                             |
| `fieldsOf[S]`  | have S's fields, each at an equal type (what `struct { … }` is today)            |
| `methodsOf[S]` | have a method fitting each function S declares (`!285`'s rule, defaults and all) |

S is an anonymous struct, of fields for `fieldsOf` and of bodyless functions
for `methodsOf`. `union` and `intersection` are added when the standard
library needs one: asking for fields and methods at once is an intersection.

Used so far: `any` everywhere; `anyStruct` by the 16 `struct {}`
constraints in `lib`; `fieldsOf` by 30 field constraints, all in fixtures;
`methodsOf` by `!285`'s fixtures, and by `list[T].format` once it says what
T needs. `anyFields` has no user yet.

## 4. The standard library moves to it

From the survey on `!285`:

| Declaration                           | Today           | Becomes         |
|---------------------------------------|-----------------|-----------------|
| `http.send`, `get` (`Q`)              | `[Q struct {}]` | `[Q anyStruct]` |
| `i18n.tr`, `trInline`, `format` (`A`) | `[A struct {}]` | `[A anyStruct]` |
| `nav` params `T`, meta `M`            | `[T struct {}]` | `[T anyStruct]` |

Each is a struct the target reads field by field -- http's query encoder,
i18n's `RecordArgs`, android's serialized route, html's path -- so
`anyStruct`, not `anyFields`: a component's fields are no record.

The field types each accepts stay where they are checked today until unions
exist: http's `queryProblem` (bool, int, float, string, plain enum, date,
datetime, each optionally one `option` or `list`), i18n's placeholder
checks, html's `pathParseable` for nav params.

i18n checks no argument's type, and an `option` argument prints differently
on every target: `i18n.tr("greet", {name=n})` with `n` a null
`option[string]` and the template `Hello {name}` gives `Hello <nil>` on Go
and the interpreter, `Hello null` on JS and `Hello ` on Kotlin, and a
present one prints a pointer address on Go, where an option is a pointer
(`pkg/go/i18n` formats with `%v`). That is from reading each runtime, not
yet reproduced; a bug to file separately.

## 5. Reading the fields (#309)

A generic body reading the bound type's fields as
`list[struct { name string; value … }]`, expanded at each binding where a
constraint's function is resolved now (`ir.BoundCall`, the per-binding
copies in `passBuiltinMethods`). What `value` is needs `union` or the
payload-enum spelling from #309, so this waits with them, and so does its
name: `fieldsOf` is the constraint.

## Order

1. Aliases of constraints, with fixtures and the spec.
2. Constraints name declarations; `any`, `anyStruct`, `anyFields`,
   `fieldsOf` and `methodsOf` declared in `lib/builtin` and marked, `any`
   moving from a scalar to the constraint declaration.
3. `!285`'s struct-constraint functions move to `methodsOf`, and a bare
   struct constraint becomes an error, its uses moved to `fieldsOf`.
4. http, i18n and nav move to `anyStruct`.
5. Later: `union`, `intersection`, then #309.
