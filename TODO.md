# TODO

## Type parameters in brackets

Type parameters and type arguments move from `<…>` to `[…]`, as Go writes
them. The motivation is the call site: `<…>` there is not LL(1) against a
comparison -- `CmpExpr` chains, so `f<a>(b)` already parses as
`(f < a) > (b)`, and `g(a < b, c > (d))` is two comparisons or one generic call
depending on a `>` arbitrarily far ahead. A bracket after a name is already a
postfix index, so `f[int](x)` parses today and the checker decides what it
means, as Go's does. That retires the TODO in `lib/builtin/context.sngl` and
the typed-const workaround it documents
(`const nobody option<User> = null` then `context #who(nobody)`):
`context[option[User]] #who(null)`.

Three phases, in this order. Phase 1 cannot start until phase 0 is finished,
because a suffix bracket on a declaration name is the override target today,
and phase 0 is what tells the two apart.

### Phase 0: every type parameter names a constraint

`component ui.vbox[platform]` and `component box[T]` would be the same tokens.
A target is only ever `ident` or `ident.ident` -- `resolveTargetIndex` refuses
every other shape -- so if a type parameter must name a constraint, a type
parameter list always opens `ident Type`, and the token after `[ ident`
decides it: `rbracket` or `dot` is a target, anything in `FIRST(Type)` is a
type parameter. Neither `dot` nor `rbracket` is in `FIRST(Type)`. The parser
decides, as Go's does for `type A[N]int` against `type A[P any]int`, and the
target stays where it is.

`any` is the constraint that says nothing. Done in `<…>` first, so the tree is
converted while the brackets still mean one thing:

```
TypeParam = ident Type [ assign Type ] .
```

```sngl
func first<T any>(xs list<T>) T { … }
component effect<T any, F any>(…) F
component page<T any = struct {}, M any = struct {}>(…) _page<M>
```

A receiver's list restates names and no constraints, as Go's does:
`func list<T>.map<U any>(f func(T) U) list<U>`. It is told apart by the `dot`
after it (see phase 1), so it keeps `TypeParam`'s old shape as its own
production, `ReceiverTypeParams`.

- [X] Fixtures first: a constraint required in a declaration, a component and a
  struct; `any` refused anywhere but a constraint. The parser stops at its
  first error, so a missing constraint is one fixture per declaration
  (`testdata/error_type_param_constraint_required_{func,method,component,struct}.sngl`)
  and `any` is `testdata/error_any_outside_constraint.sngl`.
- [X] `any` declared in `sngl:builtin`, marked like the scalars, and accepted
  by the checker only in a constraint position (`dyn` is the type that holds
  anything). A constraint other than `any` is the constraints section's.
- [X] Grammar: `TypeParam = ident Type [ assign Type ]`; `FuncName`'s
  receiver list takes names only. Builder and formatter.
- [X] `ast.TypeParam.Constraint`, `ir.TypeParamSym.Constraint`; `ir.Convert`
  prints it.
- [X] `sngl fmt` cannot add what the source does not say: a script in `tmp/`
  writes `any` into every type parameter in `lib/`, `testdata/`, examples,
  and the SNGL embedded in `.txtar`, `.txt`, `.md` files and Go test strings.
- [X] Diagnostics and `// ERROR` text that print a type parameter list.
- [X] Tree-sitter, vscode, neovim.
- [X] Spec, effective-sngl, the tour, CLAUDE.md. The tour declares no
  generic, so it needed no change.

### Phase 1: declarations and types in brackets

```
TypeParamList = lbracket TypeParam { comma TypeParam } rbracket .
Type          = ident [ dot ident [ TypeArgs ] | TypeArgs ] | … .
TypeArgs      = lbracket TypeList rbracket .
```

`TypeParamList` and `TargetIndex` both open `lbracket ident` after a
declaration's name, so they are one production, left-factored on what follows
the first ident, and the builder sorts it by shape:

```
NameBracket = lbracket ident ( [ dot ident ] rbracket
                             | Type [ assign Type ] { comma TypeParam } rbracket ) .
```

In `FuncName` a bracket after the receiver is followed by `dot` and is the
receiver's names, `func list[T].map[U any]`; `func list[T].second[html.platform]`
is an override of a generic method. So `func f[T]` -- one bare name and no
`dot` after the bracket -- is a target, and an undefined one: the factoring
reads the bracket first and decides on the token after `rbracket`.

Also `FuncTypeParam`'s two `lt TypeList gt` branches and `FuncName`'s comment
block. No production that can follow a `Type` begins with `lbracket`, and
`lbracket` is not in `FIRST(Type)`, so `list[int]`, `func(x list[int])` and
`[T = list[int]]` stay LL(1). There is no `>>` token to retire.

- [X] Grammar accepts both `<…>` and `[…]` for one commit; builder
  (`buildTypeParams`, `buildType`) builds the same AST from either.
- [X] Formatter prints brackets; `ir.Type.String()` and `ir.Convert` print
  brackets.
- [X] `sngl fmt` over the tree; script the embedded SNGL in `.txtar`, `.txt`
  and `.md` files (about 450 files mention a `<T>` form today).
- [X] The ~20 fixtures whose `// ERROR` text prints a type; goldens
  `-update` where a type is printed back as SNGL.
- [X] Tree-sitter (`grammar.js:156`, `:321`), vscode, neovim; any LSP
  completion or signature-help trigger on `<`.
- [X] Spec, effective-sngl, the tour, CLAUDE.md, and Go comments quoting
  SNGL (`list<T>`, `page<T, M>`, `func list<T>.map<U>` …).
- [X] Drop `<…>` from the grammar.

### Phase 2: type arguments at a call site

Write the fixtures first: `testdata/generic_call_site_args.sngl` (a func, a
method, a component node, a struct literal, a partial list with the rest
inferred) and `testdata/error_generic_call_site_args.sngl` (too many, an index
on a non-generic name reaching the type rule, a type that is not an
expression).

- [X] Index takes a list: `lbracket Expr { comma Expr } rbracket` in
  `ExprPostfixOp`, `CondPostfixOp` and `StmtPostfixOp`.
  `ast.IndexExpr.Index` becomes a list (or an `Indices` beside it).
- [X] Checker: an index whose operand resolves to a generic declaration --
  func, method, component, struct -- is an instantiation. Each index
  expression is read as a type (ident, qualified ident, a nested index for
  `option[User]`). Explicit arguments bind a prefix of the parameters and
  inference fills the rest, as Go does; a default fills what neither
  reaches. Anything else is an ordinary index.
- [X] A type that is not an expression -- `func(int) bool`, `struct {}`,
  `component …` -- is refused with a position in this phase. A
  type-or-expression production inside the brackets is the follow-up if
  one is needed.
- [X] A node: `context[option[User]] #who(null)`. `StmtPostfixOp`'s index then
  `hash` must reach the builder as a node with type arguments.
- [X] A struct literal: `Box[int]{v=1}` needs `[ StructLitBody ]` after the
  index in `ExprPostfixOp` (not the condition context, whose `{` is the
  body's).
- [X] The instantiation reaches IR as the bindings inference already
  produces, so nothing after the checker changes. Confirm on the
  interpreter and one monomorphizing target (`passMonomorphize`).
- [X] LSP: hover and completion inside the brackets.
- [X] Remove the TODO in `lib/builtin/context.sngl` and rewrite the
  "A context's type is its default's" paragraph in CLAUDE.md and the spec.

## Type parameter constraints

Every type parameter names a constraint between its name and its default
(phase 0 above):

```
TypeParam = ident Type [ assign Type ] .
```

The grammar admits any type; the checker accepts `any` and an **anonymous
struct** for now, which leaves the position open for `comparable`, a family or
an interface later.

**An anonymous struct constraint is a struct with at least these fields.**
`[T struct {}]` is any struct; `[T struct { name string }]` is any struct
with a `name` field of type `string`. Fields are matched by name at equal
types -- the same `Type.Equal` per field that makes two anonymous structs one
type -- not at assignable ones. A named struct satisfies it as readily as an
anonymous one: the constraint is about fields, not identity. A field default
on the struct being checked is ignored; only the constraint's own fields may
not carry one.

`any` says nothing about the type, and is required where nothing else is
said: the constraint is what tells a type parameter list from an override's
target (phase 0), as Go requires one to tell a generic from an array.

Write the fixtures first: `testdata/type_param_constraint.sngl` (a named struct
with field defaults and an anonymous one satisfying it, a field read and a spread in the generic
body, a default satisfying its own constraint, the interpreter running each)
and `testdata/error_type_param_constraint.sngl` (a non-struct argument, a
missing field, a field at another type, a constraint that is not an anonymous
struct, a constraint field with a default, a default that does not satisfy the
constraint). Then a golden on Go and Kotlin, which monomorphize, and
JavaScript, which keeps the generic.

- [ ] AST and IR carry the constraint from phase 0; this section adds only
  the anonymous struct.
- [ ] Refuse anything but `any` or an anonymous struct as a constraint, and a field
  of that anonymous struct carrying a default: a constraint says what a type
  has, not what it starts with. A satisfying struct's own defaults, named or
  anonymous, play no part in the match.
- [ ] Check every binding against the constraint, wherever a type parameter
  is bound: call inference (`bindArgs`), explicit type arguments (phase 2
  above), a type's own arguments in `resolveType`, `bindFromChildren`, family
  arguments, and a default (`M struct {} = struct {}` must satisfy its own
  constraint).
- [ ] In a generic body, a select on a `T` reads the constraint's fields, typed
  from the constraint; anything else is an unknown member, as it is on an
  unconstrained `T` today.
- [ ] A spread of a `T` binds the constraint's fields by name, under
  `expandSpread`'s existing rule; fields beyond them are not visible to it.
- [ ] Codegen: Go and Kotlin need nothing (`passMonomorphize` copies per
  instantiation, so the select is concrete); JavaScript reads a property;
  the interpreter reads the field off the value. Confirm each with the golden.
- [ ] Stdlib: `i18n.tr`, `trInline`, `format` and the private `_translate` and
  `_format` take `[A struct {}]`, and the struct check in `checkI18nCall`
  (`internal/checker/i18n.go`) goes -- the interpolated-template check and
  `ir.RecordArg` stay. `nav.page`, `stack.go`, `href`, `_follow` and `link`
  take `[T struct {} = struct {}]`; `_page` and `stack` probably constrain
  `M` the same way. `effect` and `gen.node` stay unconstrained.
- [ ] Spec, effective-sngl and CLAUDE.md.

A constraint is never a default: `[T struct {} = struct {}]` writes both, and
a parameter with no default has none, `[T any]` included.
