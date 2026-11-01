# Constraints as a package

A plan for review. It replaces the open questions on `!285` about what a
constraint can say: a struct constraint grew functions there, and the next
asks -- a struct with any fields, the types of fields it does not name, a
body reading those fields (#309) -- each wanted another rule in the
language. Instead, constraints become declarations of a library package,
`sngl:constraints`, named through type aliases.

Nothing here is built. Each section ends with what it needs decided.

## Where things stand

- A type parameter's constraint is `any` or an anonymous struct, and
  nothing else: `[T Named]` with `struct Named { name string }` is refused,
  "a type parameter's constraint is `any` or an anonymous struct"
  (`checker.resolveConstraint`). A constraint cannot name a declaration.
- `struct { … }` as a constraint means a type with fields, at least those
  ones, at equal types. `struct {}` is any struct.
- `!285` lets one declare bodyless functions, which a type satisfies by a
  method taking a call of that shape, constant defaults matched
  (`methodUnsatisfied`, `matchDefaults`). A constraint naming only functions
  is satisfied by any type with those methods, `int` included -- the
  asymmetry with `struct {}` this plan removes.
- SNGL has no `type` keyword. Declarations are `struct X {}`, `enum X {}`,
  `unit X`, so a name for a type that is not one of those has no spelling.
  This is the third design that has wanted one.

## 1. Type aliases

```sngl
type Formattable = c.methodsOf[struct {
    const func format() string
}]

type Query = c.fieldsOf[struct {
    page option[int]
}]

func show[T Formattable](v T) => "<{v}>"
```

`type Name = Type` declares Name as another spelling of Type: the same type
everywhere, not a new one (Go's alias, not Go's defined type). It is what
lets a constraint be written once and named, and a long instantiation
shortened anywhere else a type is written.

Decide:

- Generic aliases, `type Pair[T any] = struct { a T; b T }`: in, or later?
- Exported like any declaration (by case), documented like one, shown by the
  LSP's hover as the alias and what it stands for?
- Is `type Name struct { … }` (Go's defined type) wanted too, replacing or
  beside `struct Name { … }`? This plan only needs the alias.

## 2. A constraint names a declaration

A constraint is any type expression whose type is a constraint: `any`'s
replacement, a `sngl:constraints` builtin, an alias of either, or an
anonymous struct. `resolveConstraint` resolves it as a type and asks the
declaration's builtin kind what it requires, rather than matching two
syntactic forms. Each builtin is declared with `#[builtin("…")]` and found
by its mark, never by name, so a program may shadow one.

Decide: does a named struct (`[T Named]`) mean "has Named's fields", as an
anonymous struct does, or is it refused as not a constraint?

## 3. `sngl:constraints`

```sngl
import c "sngl:constraints"
```

| Builtin          | A type bound to the parameter must                                    |
|------------------|-----------------------------------------------------------------------|
| `c.any`          | be anything: what `any` is today                                      |
| `c.anyFields`    | have fields: a struct or a component, whatever they are               |
| `c.fieldsOf[S]`  | have S's fields, each at an equal type (what `struct { … }` is today) |
| `c.methodsOf[S]` | have a method fitting each function S declares (`!285`'s rule)        |

S is an anonymous struct. One constraint asking both is an alias of both,
which needs a way to combine them:

- `c.fieldsOf[S]` and `c.methodsOf[S]` of one S, `c.fieldsOf[c.methodsOf[S]]`,
  or one builtin taking both, `c.has[S]`?

Unions -- "a scalar or a plain enum", the types an http query field may be
-- are left out until the standard library needs one.

Decide:

- Is `any` kept as a keyword, an alias of `c.any`, or removed in favor of
  it? `any` is found in scope today, so a program's own `any` already
  shadows it.
- Does an anonymous struct remain a constraint by itself, meaning
  `c.fieldsOf` of it, or must it be wrapped? Kept, `struct {}` still means
  any struct, and `c.anyFields` is the same thing; wrapped, `[T struct {}]`
  is an error naming `c.anyFields`.
- `!285`'s functions in a struct constraint: move to `c.methodsOf` only, or
  keep both spellings?

## 4. The standard library moves to it

From the survey on `!285`:

| Declaration                           | Today           | Becomes           |
|---------------------------------------|-----------------|-------------------|
| `http.send`, `get` (`Q`)              | `[Q struct {}]` | `[Q c.anyFields]` |
| `i18n.tr`, `trInline`, `format` (`A`) | `[A struct {}]` | `[A c.anyFields]` |
| `nav` params `T`, meta `M`            | `[T struct {}]` | `[T c.anyFields]` |
| `test.Test.test` (`C`)                | `[C any]`       | `[C c.any]`       |

The field types each accepts stay where they are checked today until unions
exist: http's `queryProblem` (bool, int, float, string, plain enum, date,
datetime, each optionally one `option` or `list`), i18n's placeholder
checks, html's `pathParseable` for nav params. i18n's arguments are not
checked by type at all, and an `option` field prints differently on every
target; that is a bug to file separately.

## 5. Reading the fields (#309)

`c.fieldsOf(v)` in a generic body: the bound type's fields as
`list[struct { name string; value … }]`, expanded at each binding where a
constraint's function is resolved now (`ir.BoundCall`, the per-binding
copies in `passBuiltinMethods`). What `value` is needs unions or the
payload-enum spelling from #309, so this waits with them.

## Order

1. Type aliases, with fixtures and the spec.
2. Constraints name declarations; `sngl:constraints` with `c.any`,
   `c.anyFields`, `c.fieldsOf`, `c.methodsOf`, declared and marked.
3. Move `!285`'s constraint functions to `c.methodsOf` (or keep both).
4. Move http, i18n, nav and test to the package.
5. Later: unions, then #309.
