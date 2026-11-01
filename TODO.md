# Constraints: what is left

Built on `!286`: the constraints are declarations in `lib/builtin` (`any`,
`anyStruct`, `anyFields`, `fieldsOf[S]`, `methodsOf[S]`), a bare struct is no
constraint, aliases name one (`struct Formattable = methodsOf[…]`), and http,
i18n and nav take `anyStruct`.

## Later

- **`union` and `intersection`**, when the standard library needs one. Asking
  for fields and methods at once is an intersection; the field types http's
  `queryProblem`, i18n's placeholders and html's `pathParseable` accept are
  each a union, checked by hand until then.
- **Reading the fields (#309)**: a generic body reading the bound type's
  fields as `list[struct { name string; value … }]`, expanded per binding
  where `ir.BoundCall` resolves a constraint's function. What `value` is needs
  `union` or #309's payload-enum spelling, and the function needs a name other
  than `fieldsOf`, which is the constraint.
- **Aliases of other types**: `struct`, `enum` and `unit` aliases of a type,
  not a constraint, are refused for now.
- **i18n's `option` argument** prints differently on every target
  (`Hello <nil>` on Go and the interpreter, `Hello null` on JS, `Hello ` on
  Kotlin; a pointer address for a present one on Go). Read off the runtimes,
  not yet reproduced; to be filed.
