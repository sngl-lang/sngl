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
because a suffix bracket on a declaration name is the override target today.

### Phase 0: the override target moves in front of the name

`TargetIndex` sits where `TypeParamList` would go:

```
ComponentDecl = kw_component ident [ dot ident ] [ TypeParamList ] [ TargetIndex ] …
FuncDecl      = kw_func FuncName [ TargetIndex ] FuncTail .
```

`component ui.vbox[platform]` and `component box[T]` are the same tokens. An
override never restates type parameters and a base never names a target, so a
declaration has at most one list, but telling them apart would take the
checker (does the entry resolve to a build-target node?), and a type parameter
named `platform` would then be ambiguous. Move the target instead:

```sngl
component[platform] ui.vbox(gap) { … }
component[html.platform] gap(w) { … }
func[bubbletea.platform] ToUpper(s string) string { … }
```

`lbracket` directly after `kw_component`/`kw_func` is unambiguous, so the
grammar stays LL(1). `kw_const FuncDecl` and `kw_const ComponentDecl` follow
for free.

- [ ] Grammar: `ComponentDecl = kw_component [ TargetIndex ] ident …`,
  `FuncDecl = kw_func [ TargetIndex ] FuncName FuncTail`. Accept both
  positions for one commit.
- [ ] Builder (`buildTargetIndex` callers in `internal/parser/build.go`) and
  formatter: the formatter prints the prefix form.
- [ ] `ir.Convert` prints the prefix form.
- [ ] `sngl fmt` over `lib/` (~300 `[platform]`/`[language]` overrides),
  `testdata/`, `examples/`; hand-convert SNGL embedded in `.txtar`,
  `cmd/sngl/testdata/*.txt` and `docs/**/*.md` (a script in `tmp/`).
- [ ] Diagnostics that spell an override (`[html.platform]` in messages and in
  `// ERROR` text).
- [ ] Tree-sitter grammar, vscode and neovim syntax.
- [ ] `docs/reference/specification.md`, effective-sngl, CLAUDE.md (the
  override sections write `component ui.vbox[platform]` throughout).
- [ ] Drop the suffix position from the grammar. `TestTestdataIsFormatted`
  and the goldens are green with no `-update` beyond printed SNGL.

### Phase 1: declarations and types in brackets

```
TypeParamList = lbracket TypeParam { comma TypeParam } rbracket .
Type          = ident [ dot ident [ TypeArgs ] | TypeArgs ] | … .
TypeArgs      = lbracket TypeList rbracket .
```

Also `FuncTypeParam`'s two `lt TypeList gt` branches and `FuncName`'s comment
block. No production that can follow a `Type` begins with `lbracket`, and
`lbracket` is not in `FIRST(Type)`, so `list[int]`, `func(x list[int])` and
`[T = list[int]]` stay LL(1). There is no `>>` token to retire.

- [ ] Grammar accepts both `<…>` and `[…]` for one commit; builder
  (`buildTypeParams`, `buildType`) builds the same AST from either.
- [ ] Formatter prints brackets; `ir.Type.String()` and `ir.Convert` print
  brackets.
- [ ] `sngl fmt` over the tree; script the embedded SNGL in `.txtar`, `.txt`
  and `.md` files (about 450 files mention a `<T>` form today).
- [ ] The ~20 fixtures whose `// ERROR` text prints a type; goldens
  `-update` where a type is printed back as SNGL.
- [ ] Tree-sitter (`grammar.js:156`, `:321`), vscode, neovim; any LSP
  completion or signature-help trigger on `<`.
- [ ] Spec, effective-sngl, the tour, CLAUDE.md, and Go comments quoting
  SNGL (`list<T>`, `page<T, M>`, `func list<T>.map<U>` …).
- [ ] Drop `<…>` from the grammar.

### Phase 2: type arguments at a call site

Write the fixtures first: `testdata/generic_call_site_args.sngl` (a func, a
method, a component node, a struct literal, a partial list with the rest
inferred) and `testdata/error_generic_call_site_args.sngl` (too many, an index
on a non-generic name reaching the type rule, a type that is not an
expression).

- [ ] Index takes a list: `lbracket Expr { comma Expr } rbracket` in
  `ExprPostfixOp`, `CondPostfixOp` and `StmtPostfixOp`.
  `ast.IndexExpr.Index` becomes a list (or an `Indices` beside it).
- [ ] Checker: an index whose operand resolves to a generic declaration --
  func, method, component, struct -- is an instantiation. Each index
  expression is read as a type (ident, qualified ident, a nested index for
  `option[User]`). Explicit arguments bind a prefix of the parameters and
  inference fills the rest, as Go does; a default fills what neither
  reaches. Anything else is an ordinary index.
- [ ] A type that is not an expression -- `func(int) bool`, `struct {}`,
  `component …` -- is refused with a position in this phase. A
  type-or-expression production inside the brackets is the follow-up if
  one is needed.
- [ ] A node: `context[option[User]] #who(null)`. `StmtPostfixOp`'s index then
  `hash` must reach the builder as a node with type arguments.
- [ ] A struct literal: `Box[int]{v=1}` needs `[ StructLitBody ]` after the
  index in `ExprPostfixOp` (not the condition context, whose `{` is the
  body's).
- [ ] The instantiation reaches IR as the bindings inference already
  produces, so nothing after the checker changes. Confirm on the
  interpreter and one monomorphizing target (`passMonomorphize`).
- [ ] LSP: hover and completion inside the brackets.
- [ ] Remove the TODO in `lib/builtin/context.sngl` and rewrite the
  "A context's type is its default's" paragraph in CLAUDE.md and the spec.

## Type parameter constraints

A type parameter may name a constraint between its name and its default:

```
TypeParam = ident [ Type ] [ assign Type ] .
```

`assign`, `comma` and `rbracket` are not in `FIRST(Type)`, so this stays LL(1)
and needs no production of its own. The grammar admits any type; the checker
accepts only an **anonymous struct** for now, which leaves the position open
for `comparable`, a family or an interface later.

**An anonymous struct constraint is a struct with at least these fields.**
`[T struct {}]` is any struct; `[T struct { name string }]` is any struct
with a `name` field of type `string`. Fields are matched by name at equal
types -- the same `Type.Equal` per field that makes two anonymous structs one
type -- not at assignable ones. A named struct satisfies it as readily as an
anonymous one: the constraint is about fields, not identity. A field default
on the struct being checked is ignored; only the constraint's own fields may
not carry one.

There is no `any`. Go needs one because its syntax requires every type
parameter to name a constraint; here the constraint is optional, and leaving
it off already means any type.

Write the fixtures first: `testdata/type_param_constraint.sngl` (a named struct
with field defaults and an anonymous one satisfying it, a field read and a spread in the generic
body, a default satisfying its own constraint, the interpreter running each)
and `testdata/error_type_param_constraint.sngl` (a non-struct argument, a
missing field, a field at another type, a constraint that is not an anonymous
struct, a constraint field with a default, a default that does not satisfy the
constraint). Then a golden on Go and Kotlin, which monomorphize, and
JavaScript, which keeps the generic.

- [ ] AST: `ast.TypeParam.Constraint`; builder and formatter.
- [ ] `ir.TypeParamSym.Constraint`; `ir.Convert` prints it.
- [ ] Refuse anything but an anonymous struct as a constraint, and a field
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
a constrained parameter with no default has none.
