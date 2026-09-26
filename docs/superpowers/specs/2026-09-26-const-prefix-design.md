# The `const` prefix

## Problem

Several constructs must be known at compile time, and the compiler knows which
by hardcoding them:

- `output` and `cache.inputs` each have a hand-written walk
  (`requireConstOutputTree`, `requireConstGenInputs`) holding every prop in
  their tree to `ir.IsConst`.
- A function is a build-time value when its body is *inferred* pure, or when
  `#[foreign(…, pure)]`, a `//sngl:pure` doc comment or a TypeScript doc tag
  says so, and every `lib/` function defaults to pure.
- A component inlines when `passInlinePure.isPure` infers it; a platform
  package's component that does not is a lowering error with no position.
- Codegen reads about twenty props as literals and falls back silently when
  one is not: fyne's `spec`, bubbletea's blueprint records, html's raw element
  `tag`/`attrs`, canvas `width`/`height`, gtk4's enum props.

None of it is writable by a program or visible in a declaration. A user
cannot say "this is a build-time value" and have the compiler hold callers to
it, and a reader cannot see which props must be constant.

## The idea

`const` as a declaration prefix says **this is known at compile time**, and
so takes no part in reactivity. What that means varies with the construct:

| Construct | `const` means |
|---|---|
| `func` | pure: its value depends only on its arguments |
| `component` | pure: its render depends only on its props |
| param / prop | the value is constant at every call site |
| slot | its population is pure |

The existing `const x = …` declaration and `const(expr)` assertion keep their
meaning, and `ir.IsConst` stays the one test of a constant expression.

## Syntax

```sngl
const func area(w float, h float) float => w * h
const func Point.len() float { … }
const component badge(const tag string, label string) ui.node { … }
const component ui.text[platform](…) { … }
component output(const name string = "", targets const ...component build.language) root {}
func mkchan(const witness T) go.chan<T>
```

The grammar stays LL(1). `ConstDecl` is left-factored on `kw_const`; the
token after it is disjoint across the four alternatives:

```ebnf
ConstDecl = kw_const ( ConstSpec                      # ident
                     | lparen { ConstSpec … } rparen  # lparen
                     | FuncDecl                       # kw_func
                     | ComponentDecl ) .              # kw_component
```

`StructBodyItem` and `EnumBodyItem` gain a `kw_const FuncDecl` alternative;
their other alternatives begin with `ident`.

`Param` gains a `kw_const ident [ Type ]` alternative beside the `colon`, `at`
and `ident` ones, after the macro attributes:
`#[macro.construct] const x int`. `Param` also serves lambda, handler and
slot-population parameters; the grammar stays permissive and the checker
refuses `const` in those three positions, the trade `ArgList` makes.

`const` is refused on an event, a `var`, and a handler. A `const` func *type*
and a `const` lambda are out of scope.

The formatter prints the prefix; the tree-sitter grammar gains it.

## AST and IR

- `Const bool` on `ast.FuncDef`, `ast.ComponentDecl`, `ast.Param`.
- `ir.Func.Const` — declared. `ir.Func.Purity` stays the *inferred* fact.
- `ir.Component.Const` — an override's is the base's OR its own.
- `ir.Param.Const`, `ir.Prop.Const`; a slot is a `Param`, and
  `ir.SlotDecl.Const` mirrors it.
- `ir.Convert` prints every one.

`ir.IsConst` changes in two places:

- **Call**: arguments const and `Func.Const`. `Purity == PurityPure` no longer
  counts.
- **Ident**: an `*ir.Param` with `Const` set is const.

## Checker

### `const func`

- **With a body**, the body is checked. It may read its params, its locals,
  consts and `const` params, and call only `const func`s. Reading or writing a
  package, component or window var, emitting, or calling a non-const func is a
  positioned error naming what was reached:
  - `const func f reads var "count"`
  - `const func f calls g, which is not const (declare it const func g)`

  Its locals may be mutated; they are the call's own.
- **Without a body** — foreign, native, intrinsic, import-synthesized — `const`
  is trusted: nothing in the program says what the host does.
- `pure` is **removed** from `#[foreign]`'s flag vocabulary. Writing it is the
  ordinary unknown-flag error; there is no migration diagnostic, since no SNGL
  source exists outside this repository. `async` stays.
- The Go importer's `//sngl:pure` and the TypeScript importer's pure doc tag
  emit `const func`.
- The intrinsic registry's default-pure assumption and the library's
  default-pure rule are dropped; `lib/` writes `const` where it means it.
- Inferred `Purity` is still computed for every func and still drives the
  optimizer (inline, CSE, fold).

### `const component`

- **The render** — every expression in the view body, including `if`/`for`
  heads and slot-insertion arguments, but not handler or effect bodies — may
  read props, consts, `const func` results and invocation arguments. A var or
  captured state read there, or a non-const call, is a positioned error.
- A var only handlers touch is allowed. html's `timer` override, whose
  `handle` only the effect's mount and unmount touch, qualifies.
- A child component with state of its own is allowed; the child is its own
  instance.
- **Overrides.** `const` on a base declaration is part of its contract: every
  override is held to the render rule, with the error at the override. An
  override of a non-const base may write `const` itself.
- **Target packages.** Every component a `sngl:platform/` or
  `sngl:language/` package declares or overrides must end up `const` —
  inherited or written. Primitives (`#[intrinsic]`, wildcard, `#[builtin]`)
  are exempt: they have no body. This replaces `passInlinePure`'s strict-mode
  error; what remains there is an internal assertion.

### `const` param / prop

- The argument at every call site, and the default, must satisfy
  `ir.IsConst`: `prop "tag" of badge is const: "label" is a var`. A non-const
  param forwarded into a const one is that error.
- Inside the body the param is const, so it may flow into `const(…)`, a const
  initializer, or another const param.
- `const` implies `#[construct]`; writing both is refused as redundant.

### `const` slot

- A population is held to the const-component render rule, positioned in the
  population. Its invocation arguments need not be const: a slot inserted per
  list item stays pure over the item.
- `requireConstOutputTree` and `requireConstGenInputs` are deleted. `output`,
  each language node and `cache.inputs` declare `const` slots, and their
  members declare `const` props. A directive tree reading a var becomes the
  general rule's error. `output`'s `entry` stays special-cased: it names a
  declaration rather than holding a value.

### Ordering

The rules run after the purity fixpoint, where `constAssertion` is resolved
today, and after `runTreeChecks`.

## Optimizer, lowering, codegen

### Folding

A `const` prop is folded by contract. After the optimizer,
`optimize.foldConstArgs` holds every argument bound to a `const` param or prop
to a literal — an `ir.Literal`, or a list, struct or map literal whose leaves
are literals. An argument that does not fold (a `js:` const call with no
evaluator, a const recursion that does not settle) is a build error at the
argument. A const component inlines before lowering, so the pass bites mainly
on primitives and runtime instances.

### Reactivity

- A `const` prop gets no setter, updater or rebuild; `rebuildsFor` and
  `propIsConstruct` read `Const`.
- A `const` slot insertion is never a render slot (`collectFromFor`,
  `bodyNeedsSlot`).
- A `const component` always inlines. `passInlinePure` reads `Const` first;
  `isPure` stays the opportunistic path for an unmarked user component.

### Codegen

The props codegen reads as literals become `const` declarations, and each
silent fallback becomes `panic("internal: … not folded")`:

- fyne: `Widget`/`Container`/`Wrapper`'s `spec`
- bubbletea: blueprint records (`dim`, `prop`/`get`/`set`, `on`/`key`)
- html: raw element `tag`/`attrs`; window `href` (a plain string since route
  parameters became a struct; static mode still refuses `{param}`)
- canvas `width`/`height`
- gtk4: GIR-synthesized enum and bitfield props, emitted `const` by the
  generator
- android: the equivalents found while doing the above

Window `title` and `favicon` stay non-const: a reactive title is legitimate on
gtk4, fyne and android. html silently dropping a non-literal title is a
separate gap, reported rather than papered over.

### Library marking

- `const func` on every `lib/` and target-package func whose body the purity
  fixpoint finds pure, and on the pure intrinsics.
- `const component` on every `lib/` base whose overrides all pass the render
  rule.

## Phases

One spec, implemented in order; each phase leaves the suite green.

1. Parser, AST, formatter, tree-sitter, IR flags, `ir.Convert`. `const`
   param/prop checking; `IsConst` Ident case. `output` and `cache.inputs`
   declare const props, and the two `requireConst*` walks are deleted.
2. `const func`: body check, `IsConst` Call case, `pure` removed from
   `#[foreign]`, importers, intrinsic and library defaults, `lib/` marking.
3. `const component` and `const` slot: render check, override inheritance,
   target-package rule, `passInlinePure`, reactivity, `lib/` marking.
4. `foldConstArgs`; codegen literal-only props marked `const`; silent
   fallbacks become panics.

## Testing

- A fixture per rule, `testdata/error_const_*.sngl`, and positive
  `testdata/const_*.sngl`.
- A golden for a const prop's folded literal reaching a primitive.
- A CLI txtar for the unfoldable-argument build error.
- Each fixture confirmed to fail with its rule reverted.

## Documentation

A CLAUDE.md section on the prefix — four meanings, one idea — and the
language reference updated for each construct.
