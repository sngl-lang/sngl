# Constraints as a package

A plan for review. It replaces the open questions on `!285` about what a
constraint can say: a struct constraint grew functions there, and the next
asks -- a struct with any fields, the types of fields it does not name, a
body reading those fields (#309) -- each wanted another rule in the
language. Instead, constraints become declarations of a library package,
`sngl:constraints`, named through aliases.

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
import c "sngl:constraints"

struct Formattable = c.methodsOf[struct {
    const func format() string
}]

struct Keyed[K c.any] = c.fieldsOf[struct {
    key K
}]

func show[T Formattable](v T) => "<{v}>"
```

An alias is written with the keyword of what it aliases, `struct Name = Type`, and is that type under another name: the same type everywhere, not a
new one. There is no `type` keyword. An alias may take type parameters,
which its right-hand side names.

For now an alias is of a constraint only: the `sngl:constraints` builtins
are declared as structs, so `struct` is the keyword that aliases them, and
an alias of any other type is refused. `enum Name = …` and `unit Name = …`
follow the same grammar when an alias of one is wanted.

It is exported by case and documented like any declaration, and the LSP's
hover shows the alias and what it stands for.

## 2. A constraint names a declaration

A constraint is a type expression whose type is a constraint: a
`sngl:constraints` builtin, or an alias of one. `resolveConstraint` resolves
it as a type and asks the declaration's builtin kind what it requires,
rather than matching two syntactic forms. Each builtin is declared with
`#[builtin("…")]` and found by its mark, never by name, so a program may
shadow one.

A bare struct is no constraint: `[T struct { name string }]` and
`[T Named]` are errors naming `c.fieldsOf`, and `[T struct {}]` one naming
`c.anyStruct`.

## 3. `sngl:constraints`

```sngl
import c "sngl:constraints"
```

| Builtin          | A type bound to the parameter must                                               |
|------------------|----------------------------------------------------------------------------------|
| `c.any`          | be anything: what `any` is today, which it replaces                              |
| `c.anyStruct`    | be a struct, whatever its fields                                                 |
| `c.anyFields`    | have fields: a struct or a component                                             |
| `c.fieldsOf[S]`  | have S's fields, each at an equal type (what `struct { … }` is today)            |
| `c.methodsOf[S]` | have a method fitting each function S declares (`!285`'s rule, defaults and all) |

S is an anonymous struct, of fields for `fieldsOf` and of bodyless functions
for `methodsOf`. `c.union` and `c.intersection` are added when the standard
library needs one: asking for fields and methods at once is an intersection.

`any` is gone: `[T any]` is an error naming `c.any`, and every declaration
in the repository moves to it.

Decide: `[T any]` is the most common constraint, in `lib/builtin` too
(`struct list[T any]`). Does every file declaring a generic import
`sngl:constraints`, or is `c.any` reachable without one -- the builtin
package re-exporting it, or `sngl:constraints` imported by every file the
way `sngl:builtin` is?

## 4. The standard library moves to it

From the survey on `!285`:

| Declaration                           | Today           | Becomes           |
|---------------------------------------|-----------------|-------------------|
| `http.send`, `get` (`Q`)              | `[Q struct {}]` | `[Q c.anyStruct]` |
| `i18n.tr`, `trInline`, `format` (`A`) | `[A struct {}]` | `[A c.anyStruct]` |
| `nav` params `T`, meta `M`            | `[T struct {}]` | `[T c.anyStruct]` |
| every `[T any]`                       | `[T any]`       | `[T c.any]`       |

Each is a struct the target reads field by field -- http's query encoder,
i18n's `RecordArgs`, android's serialized route, html's path -- so
`c.anyStruct`, not `c.anyFields`: a component's fields are no record. Nothing
in the standard library asks for `c.anyFields` yet.

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

`c.fieldsOf(v)` in a generic body: the bound type's fields as
`list[struct { name string; value … }]`, expanded at each binding where a
constraint's function is resolved now (`ir.BoundCall`, the per-binding
copies in `passBuiltinMethods`). What `value` is needs `c.union` or the
payload-enum spelling from #309, so this waits with them.

## Order

1. Aliases of constraints, with fixtures and the spec.
2. Constraints name declarations; `sngl:constraints` with `c.any`,
   `c.anyStruct`, `c.anyFields`, `c.fieldsOf` and `c.methodsOf`, declared and
   marked.
3. `!285`'s struct-constraint functions move to `c.methodsOf`, and a bare
   struct constraint becomes an error.
4. `any` becomes `c.any` everywhere; http, i18n and nav move to
   `c.anyStruct`.
5. Later: `c.union`, `c.intersection`, then #309.
