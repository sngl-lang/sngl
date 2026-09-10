---
title: Language Specification
order: 50
description: Formal grammar and semantics of the SNGL language
---

<!-- The fenced blocks and tables delimited by BEGIN/END GENERATED markers are
     produced by internal/cmd/specgen from internal/parser/sngl.ebnf. Do not
     edit those spans by hand; run `go run ./internal/cmd/specgen` to refresh
     them. All prose outside the markers is hand-authored and may be edited
     freely. -->

## Introduction

This is the reference manual for the SNGL language. SNGL is a declarative
language for describing user interfaces. A single `.sngl` program is compiled
ahead of time to native source for a chosen *platform* (the rendering target,
such as HTML, a Bubble Tea terminal UI, or Android/Compose) expressed in a
chosen *language* (the host language of the generated code, such as Go,
JavaScript, or Kotlin).

This document specifies the **core language**: its lexical structure, grammar,
type system, declarations, expressions, statements, the component and
reactivity models, and the module system. It is intended to be precise enough
to implement a conforming compiler front end and semantic analyzer.

### Scope and conformance

The core language is deliberately small. Two bodies of functionality live
*outside* this specification and are required to build complete programs:

- **The standard library** — components (`text`, `button`, `vbox`, …), types
  (`color`, `Style`, the event payload structs, the `measurement` and
  `duration` units), and functions, all written in SNGL and distributed as
  source under `lib/<package>/*.sngl`, one directory per importable package. A conforming implementation parses and checks the
  standard library with the same front end it applies to user code; the
  library is not privileged by the grammar. This manual references standard
  library entities by example but does not define them.
- **Platform and language plugins** — the code generators that lower checked
  programs to a particular platform/language pair, and the *capabilities* they
  declare (see [Platform and language plugins](#platform-and-language-plugins)).

A conforming front end is one that, given the standard library source and a set
of user `.sngl` files, accepts exactly the programs this manual describes as
well-formed and rejects the rest, producing the typed intermediate
representation that plugins consume.

Where the present implementation diverges from a rule stated here, this
specification is authoritative and the implementation is considered to have a
bug to be fixed.

### Notation

The grammar is written in Extended Backus–Naur Form (EBNF):

```
Production  = expression .
```

- `{ X }` — zero or more repetitions of `X`
- `[ X ]` — `X` is optional (zero or one)
- `X | Y` — either `X` or `Y`
- `( X )` — grouping
- `"text"` — the literal token `text`
- Names in `UPPER_CASE` denote terminal token classes produced by the lexer
  (for example `IDENT`, `INT`, `STRING`).

The complete grammar appears in the [Grammar appendix](#grammar-appendix);
individual productions are reproduced inline in the relevant sections. The
grammar is LL(1) over the token stream described in
[Lexical structure](#lexical-structure).

## Source representation

A SNGL source file is a sequence of Unicode code points encoded as UTF-8. The
lexer processes the input as a sequence of runes.

The space (`U+0020`), horizontal tab (`U+0009`), and carriage return
(`U+000D`) are *whitespace* and are insignificant except as token separators.
The newline (`U+000A`) is a *line terminator*; it separates tokens and may
trigger automatic semicolon insertion (see
[Semicolons](#semicolons-and-automatic-insertion)).

Source files carry no byte-order mark requirement and no shebang handling at
the lexical level.

## Lexical structure

### Comments

There are three forms of comment. Two are *lexical* — discarded before
parsing — and one is *structural*, removing a whole construct from the program.

- A **line comment** begins with `//` and runs to the end of the line.
- A **block comment** begins with `/*` and ends with the matching `*/`. Block
  comments **nest**: each `/*` increases nesting depth and each `*/` decreases
  it, so a block comment may enclose other block comments. An unterminated
  block comment is a lexical error.
- A **slashdash comment** is the prefix `/-` applied to a statement or
  declaration; it comments the construct *out*. See
  [The slashdash prefix](#the-slashdash-prefix).

Line and block comments are not significant to the grammar and carry no
semantic meaning; they are discarded before parsing. A comment that ends a line
is treated, for the purpose of automatic semicolon insertion, as though the
token preceding the comment were at end of line.

Slashdash differs in kind: the construct it prefixes is still lexed and parsed —
so it must be syntactically well-formed — and is then dropped from the program.
It is the way to comment out a statement, a declaration, or a visual node
(including its nested body) without deleting the text:

<!-- SNGL-component -->

```sngl
vbox {
    text(value="shown")
    /- text(value="hidden")
    /- button(text="also gone", @click {})
}
```

A slashdash may precede any statement at file scope or inside a block, and may
itself be preceded by macro attributes.

### Tokens

Tokens are the identifiers, keywords, literals, operators, and punctuation
described below. The lexer always forms the longest valid token, with the
documented exception for `.`/`...`.

### Semicolons and automatic insertion

The formal grammar uses the semicolon `;` as the terminator that separates
declarations and statements. Source code rarely contains explicit semicolons
because the lexer inserts them automatically:

> When a line terminator is encountered, a semicolon is inserted into the token
> stream if the last token before it was one of:
> - an identifier;
> - an integer, float, string, raw-string, unit, or `#`-token (a color literal
>   or element reference), or the closing segment of an interpolated or i18n
>   string;
> - the keyword `return`;
> - one of the tokens `@`, `)`, `]`, `}`, `!!`, `++`, `--`.

A semicolon is likewise inserted at end of file if the final token satisfies
the rule above. The closing `]` of a macro attribute does **not** trigger
insertion, so an attribute may precede the declaration it decorates on the next
line. Multiple statements may be placed on one line by writing explicit
semicolons between them.

### Identifiers

An identifier begins with an ASCII letter (`a`–`z`, `A`–`Z`) or underscore and
continues with ASCII letters, decimal digits, and underscores. Identifiers are
ASCII only; non-ASCII letters are not permitted in identifiers.

```
identifier = ( letter | "_" ) { letter | digit | "_" } .
```

Identifiers beginning with two underscores (`__`) are reserved for
compiler-synthesized names and should not be declared by user code.

### Keywords

The following words are reserved and may not be used as identifiers:

<!-- BEGIN GENERATED: keywords -->

`break` `component` `const` `continue` `else` `enum` `for` `func` `if` `import` `platform` `return` `struct` `unit` `var`

The following names are **predeclared identifiers**, not keywords: `true`, `false`, `null`, `output`, `timer`, `window`, `style`. They have meaning in context but may be shadowed by user declarations.

<!-- END GENERATED: keywords -->

`break` and `continue` act on the innermost enclosing loop; see
[The for statement](#the-for-statement). Predeclared
identifiers occupy the outermost scope and may be shadowed by a user
declaration of the same name (see [Declarations and scope](#declarations-and-scope)).

### Integer literals

An integer literal is a non-empty run of decimal digits, optionally containing
underscores as digit separators. There is no sign (a leading `-` is the unary
minus operator) and there are no non-decimal bases. A digit run immediately
followed by letters is a [unit literal](#unit-literals), not a based number, so
`0x`, `0o`, and `0b` are simply the quantity `0` with the unit suffixes `x`,
`o`, and `b` — there is no hexadecimal, octal, or binary integer syntax.

```
int_lit = digit { digit | "_" } .
```

### Float literals

A float literal is a decimal mantissa, a `.`, and a decimal fraction. The
decimal point must be followed by at least one digit, so `1.` is not a float
literal. Underscores may separate digits on either side. There is **no
exponent syntax**: `1e9` lexes as the integer `1` followed by the identifier
`e9`.

```
float_lit = digit { digit | "_" } "." digit { digit | "_" } .
```

### String literals

SNGL has three string forms, each of which may be plain or interpolated:

- **Quoted strings** are delimited by `"`. They support escape sequences and
  interpolation.
- **Raw strings** are delimited by backticks (```). They contain no escape
  sequences and no interpolation; every character up to the closing backtick is
  literal, including `{` and `}`.
- **Triple-quoted strings** are delimited by `"""`. They span multiple lines
  (literal newlines are part of the value), support escapes and interpolation,
  and are *dedented*: a leading line break immediately after the opening `"""`
  is dropped, the longest common leading-whitespace prefix of the non-blank
  content lines is removed from every line, and a trailing blank line before the
  closing `"""` is dropped.

The escape sequences valid in quoted and triple-quoted strings are:

| Escape | Meaning                                        |
|--------|------------------------------------------------|
| `\n`   | newline (`U+000A`)                             |
| `\t`   | tab (`U+0009`)                                 |
| `\r`   | carriage return (`U+000D`)                     |
| `\"`   | double quote                                   |
| `\\`   | backslash                                      |
| `\{`   | literal `{` (suppresses interpolation)         |
| `\}`   | literal `}`                                    |
| `\0`   | NUL (`U+0000`)                                 |
| `\xHH` | the byte with the given two hexadecimal digits |

An unterminated string and an ill-formed `\x` escape are lexical errors.

### String interpolation

Inside a quoted or triple-quoted string, an unescaped `{` begins an
*interpolation*: the text up to the matching `}` is lexed and parsed as an
expression and spliced into the string. Braces nest, so an interpolation may
contain brace-delimited subexpressions (for example a struct literal).

The lexer emits a string as a single token when it has no interpolations, and
otherwise as a sequence of segment tokens surrounding the interpolated
expressions:

```
InterpStr    = STR_START Expr { STR_RESUME Expr } STR_END
TripleInterp = TRIPLE_START Expr { STR_RESUME Expr } TRIPLE_END
```

For example `"a {x} b {y} c"` produces the segments `STR_START "a "`, the
expression `x`, `STR_RESUME " b "`, the expression `y`, and `STR_END " c"`.
Each interpolated expression is implicitly converted to `string` (see
[Conversions](#conversions)).

### Translatable (i18n) strings

A string prefixed with `$` — `$"…"` or `$"""…"""` — is a *translatable*
string. Lexically it behaves like an ordinary string with interpolation, but
its tokens are distinguished (`I18N_*`) so the checker can route it through the
internationalization machinery, where it is lowered to a translation lookup.

Two additional lexical rules apply inside translatable strings:

- **ICU apostrophe quoting**: `''` denotes a literal apostrophe; an apostrophe
  immediately before one of the ICU metacharacters `{ } # |` begins a quoted
  run of literal text that ends at the next apostrophe.
- **ICU case bodies**: within a plural/select/selectordinal placeholder, a
  selector keyword or `=`-prefixed number followed by `{` introduces a *case
  body* whose literal text is lexed as a case-body token, allowing nested
  placeholders.

An interpolation field in a translatable string may carry up to three
comma-separated parts — the value, a format keyword or function, and a style
or message body — following ICU MessageFormat conventions for plurals and
selects.

### Color literals

A color literal is `#` followed by three, four, six, or eight hexadecimal
digits: `#rgb`, `#rgba`, `#rrggbb`, or `#rrggbbaa`. The three- and four-digit
short forms expand CSS-style by doubling each digit — `#fff` is `#ffffff` and
`#f00a` is `#ff0000aa`. Alpha defaults to fully opaque when omitted (the three-
and six-digit forms).

The lexer does **not** distinguish a color from an
[element reference](#element-references); both are the single `#`-token (the
text after `#`, which may begin with a digit). Position alone decides: a
`#`-token in value position is a color literal — the checker validates the hex
shape and reports `invalid color literal` otherwise — while as a postfix it is
an element reference. Because the lexer no longer guesses by shape, no
identifier is "stolen" by the color rule: `#facade` and `#deadbeef` are valid
element-reference names, and `#0f0f0f` is a valid color.

### Unit literals

A unit literal is a numeric literal (integer or float form) immediately
followed, with no intervening whitespace, by a unit suffix consisting of one or
more ASCII letters: `5ms`, `3.5s`, `100px`, `1rem`. The suffix names a member
of a `unit` type (see [Unit types](#unit-types)); the numeric part is the
quantity in that suffix.

### Element references

An element reference is the `#`-token used as a postfix declaration tag: it
names a visual node (`button #submit(…)`) or a context declaration
(`context #locale(…)`). Its value is the text after `#`, and — since the lexer
does not split `#`-tokens by shape — that name may be any identifier, including
one made of hex digits (`#deadbeef`). An element reference is never a standalone
operand; in value position the same token is a [color literal](#color-literals)
instead. A named node or context is reached elsewhere by its bare name (or, from
a handle to its container, by ordinary field selection — `c.id.value`) — for
example to read a node's state or drive its events in a test (see
[Element references](#element-references-1)).

### Macro attributes

A macro attribute begins with `#[` and ends with the matching `]`. It decorates
what follows it: a declaration, a statement, a function parameter or a
component prop.

```
MacroAttr = "#[" IDENT [ "." IDENT ] [ "(" [ Expr { "," Expr } ] ")" ] "]"
```

Brackets nest within the attribute, and the closing `]` suppresses automatic
semicolon insertion so the decorated construct may begin on the following line.

### The slashdash prefix

The token `/-` (slashdash) prefixes a declaration or statement to disable it:
the construct is still lexed and parsed but is dropped from the program. It is
the structured-comment mechanism for temporarily removing a node without
deleting its text.

### Operators and punctuation

```
+   -   *   /   %        arithmetic
==  !=  <   <=  >   >=    comparison
&&  ||  !                 logical
=   +=  -=  *=  /=  %=    assignment
++  --                    increment / decrement
!!                        boolean toggle
?   :                     ternary
&   *                     reference / dereference (unary)
.   ,   ;                 selector, separators
(   )   [   ]   {   }     grouping, lists, blocks
@                         events and handlers
=>                        expression-body function
...                       spread / variadic
/-                        slashdash (disable)
#[ ]                      macro attribute
```

## Document structure

A source file is a sequence of statements, each optionally preceded by macro
attributes and a slashdash prefix, and optionally followed by a semicolon
(usually inserted automatically):

<!-- BEGIN GENERATED: grammar-document -->

```ebnf
Document = native_value Expr [ ";" ] | { [ "/-" ] { MacroAttr } Stmt [ ";" ] }

StmtBlock = "{" { [ "/-" ] { MacroAttr } Stmt [ ";" ] } "}"

```

<!-- END GENERATED: grammar-document -->

The top level admits the same statement forms as a block (see
[Statements](#statements)); in practice a file consists of imports, an
`output` block, type declarations (`struct`, `enum`, `unit`), `const` and `var`
declarations, `func` declarations, component declarations, and root visual
nodes such as `window` and `timer`. Declaration order is not significant (see
[Declarations and scope](#declarations-and-scope)).

## Types

A type classifies values and the operations on them. The built-in type names
listed below are predeclared; `struct`, `enum`, and `unit` introduce
user-defined types.

### Predeclared types

| Type     | Description                                                  |
|----------|--------------------------------------------------------------|
| `bool`   | `true` or `false`.                                           |
| `int`    | 64-bit signed integer. Division truncates toward zero.       |
| `float`  | 64-bit IEEE-754 floating point.                              |
| `string` | Immutable UTF-8 text.                                        |
| `dyn`    | The dynamic type; see [The dynamic type](#the-dynamic-type). |

A type may be designated *string-representable*: it has a canonical textual
form and converts implicitly to and from `string` in both directions (a literal
such as `"2026-03-12"` is therefore a valid value for one). This is a property a
type opts into rather than a fixed set of built-in names. The standard library's
`color`, `date`, `time`, and `datetime` are string-representable types; the
library also supplies the `measurement` and `duration` units used by `Style` and
`timer`. None of these are core types — they are defined in SNGL under `lib/`.

### Composite and generic types

| Type        | Description                                                           |
|-------------|-----------------------------------------------------------------------|
| `list<T>`   | An ordered sequence of `T`. Literal: `[a, b, c]`.                     |
| `option<T>` | Either a `T` or absent (`null`).                                      |
| `map<K, V>` | A mapping from comparable keys `K` to values `V`. Literal: `{k = v}`. |
| `iter<T>`   | An opaque iterator yielding `T`; the loop type for `for`.             |
| `ref<T>`    | A mutable reference to a `T` (see [References](#references)).         |

`list<T>`, `map<K, V>`, and `iter<T>` are also declared in the standard library
as generic structs carrying methods (`length`, `map`, `filter`, `keys`, …);
the type constructors themselves are built in.

A `map` literal is written `{k1 = v1, k2 = v2}` and is distinguished from a
struct literal by the expected type at its position. The key type `K` must be
*comparable*: primitives, enums, units, and named structs are comparable;
lists, maps, and functions are not.

### Function types

A function type is written `func(P1, P2, …) R`, where each `Pi` is a parameter
type and the optional `R` is the result type; its omission denotes a function
that yields no value. Function types are values: they may be stored, passed,
and returned, and a `null` is assignable to any function type (calling it
yields the result type's zero value).

### Struct, enum, and unit types

`struct`, `enum`, and `unit` declare named types. Each may also appear
*anonymously* in a type position — `var p {x int; y int}` gives `p` an
anonymous struct type — but a name may only be bound at the point of
declaration.

#### Struct types

A struct is a record of named, typed fields, each optionally with a default
value. Structs are **value types**: assignment and parameter passing copy the
struct. A struct may be generic over type parameters:

```
struct Box<T> { value T }
```

A struct may carry methods declared as `func StructName.method(…)` (see
[Functions and methods](#functions-and-methods)).

#### Enum types

An enum is a finite set of named members, each an identifier optionally bound
to a value. A member is referenced through the enum type's namespace
(`Status.active`) or, where the expected type makes the enum unambiguous, by
its bare name. Enums are comparable and may be used as map keys. An enum may
also carry methods.

#### Unit types

A unit type declares a family of measurement suffixes that share a dimension,
with conversion factors between them:

```
unit duration { ms, s = 1000ms, m = 60s, h = 60m }
```

The first suffix is the base; each subsequent suffix's value expresses it in
terms of an already-declared suffix. Unit literals (`5ms`, `2s`) have the unit
type. Within a single unit type, values of different suffixes are comparable
and combine arithmetically with the conversion factors applied. The integer
literal `0` is assignable to any unit type; other bare numbers must be scaled
by a unit literal (`5 * 1s`).

### The dynamic type

`dyn` is the dynamic type and the type assigned to a binding that has no
annotation and no inferable initializer. Any value is assignable to `dyn`, and
operations on a `dyn` operand bypass static operand checking. A `dyn` value is
**not** implicitly assignable to a concrete type; it must be converted
explicitly.

### References

`ref<T>` is a mutable reference to a value of type `T`. References are not
written directly by user programs; they arise from the address-of operator `&`
and, most importantly, from reference loop variables (`for var &x = xs`), which
make writes to the loop variable flow back to the underlying list element. See
[The for statement](#the-for-statement).

### Type identity

Two types are *identical* when:

- they are the same predeclared type;
- they are both `list`, `option`, `iter`, or `ref` and their element types are
  identical;
- they are both `map` and their key and value types are identical;
- they are struct, enum, or unit types introduced by the same declaration; for
  generic structs the type arguments must also be identical pairwise;
- they are function types with identical signatures.

### Assignability

A value of type `A` may be used where type `B` is expected when any of the
following holds:

1. `A` and `B` are identical.
2. `B` is `dyn` (any value is assignable to `dyn`).
3. `A` is `null` and `B` is an `option` type or a function type.
4. `A` is `int` and `B` is `float`.
5. one of `A` and `B` is `string` and the other is a string-representable type
   (a standard-library type such as `color`, `date`, `time`, or `datetime`).
6. `A` is `list<S>`, `B` is `list<T>`, and `S` is assignable to `T` (lists are
   covariant in their element type).
7. `A` is `list<S>` and `B` is `iter<T>` with `S` assignable to `T` (the
   conversion that underlies `for` over a list).
8. `A` is `map<K, S>` and `B` is `map<K, T>` (or `B`'s key type is `dyn`) with
   `S` assignable to `T`.
9. `A` is `option<S>` and `B` is `option<T>` with `S` assignable to `T`, or `A`
   is `T` and `B` is `option<T>` (a bare value auto-wraps into an option).

The auto-wrap stores the value, not a reference to where it was read from: a
struct is a value in SNGL, so a later write to what was wrapped does not reach
the option.

```sngl
import . "sngl:ui"

struct box {
    value int = 0
}

struct holder {
    inner option<box> = null
}

func wrapped() int {
    var b = box{value=3}
    var h = holder{inner=b} // h keeps what b was
    b.value = 99
    if h.inner != null {
        return h.inner.value // 3
    }
    return 0
}
```

### Conversions

An explicit conversion is written `T(x)`, naming a type and an operand. The
permitted conversions among primitives are:

- to `int` — from `int`, `float` (truncating), `string`, `bool`, an enum, or a
  unit;
- to `float` — from the same set as `int`;
- to `string` — from `int`, `float`, `bool`, an enum, a unit, or a
  string-representable type;
- to `bool` — from `bool` or `string`;
- to a string-representable type — from `string` or from itself.

A `dyn` operand may be converted to any primitive type (this is how `dyn` is
narrowed). Conversions whose operand is a struct, component, function, list,
option, or `null` are rejected. Unit conversions are governed by the unit type's
factors, with the literal `0` convertible to any unit.

### Narrowing an option

A comparison against `null` says what a value is in the branch where the
comparison holds. Within that branch an `option<T>` reads as a `T`:

```sngl
import . "sngl:ui"

func doubled(maybe option<int>) int {
    if maybe != null {
        return maybe * 2 // maybe is an int here
    }
    return 0
}
```

The narrowed thing is a *path*: a variable, parameter or loop variable,
extended by any number of struct field reads (`node.left`, `a.b.c`). An index
never extends one, since two spellings may name a single element.

The forms that narrow are:

- `if x != null { … }` — the then branch.
- `if x == null { … } else { … }` — the else branch.
- `x != null ? … : …` and `x == null ? … : …` — the matching arm.
- `a && b` — a null test in `a` narrows `b` and everything the whole condition
  guards. `a || b` is the same fact negated: a `== null` test in `a` narrows
  `b`.

Every other form leaves the value an `option<T>`, so reading it as a `T` is
the ordinary type error rather than a silent wrong type.

A narrowing does not survive anything that could make the value absent again.
Within the branch, assigning to the tested path or to a prefix of it, taking
its address, calling a method that mutates it, or — when the path is rooted at
package or component state a callee could reach — calling anything that
mutates state, is an error naming the write. A branch that narrows nothing
anybody read is unaffected, so `if x != null { x = null }` is legal.

A narrowing does not cross into a lambda. The body runs when the lambda is
called, which may be after the test has stopped holding.

### Generic type parameters

Type parameters are introduced by a type-parameter list (`<T>`, `<K, V>`) on a
struct, enum, unit, or method, and within a function name. They are bound to
concrete types by inference rather than written at the call site:

- A **receiver type parameter** of a method on a generic type is bound from the
  receiver's concrete type. For `func list<T>.first() T`, a call on a
  `list<int>` binds `T = int`, so the result type is `int`.
- A **method type parameter** is bound from the call's argument types,
  typically through a function argument's result type. For
  `func list<T>.map<U>(f func(T) U) list<U>`, calling `xs.map(g)` on a
  `list<int>` with `g : func(int) string` binds `T = int` and `U = string`,
  yielding a `list<string>`.

Inference walks parameter and argument types structurally, recursing through
function signatures and type arguments, and substitutes the bound types into the
signature's result.

## Declarations and scope

A *declaration* binds a name to an entity — a constant, variable, function,
component, type, import namespace, or loop variable — within a region of the
program text called a *scope*.

### Declaration order

The order of top-level declarations is not significant. Every top-level name —
a type, constant, variable, function, or component — is visible throughout its
package regardless of where it is declared, and a reference need not follow the
declaration it resolves to in source order. An imported namespace is in scope
throughout the file that imports it.

### Scopes

Scopes nest from the innermost outward:

- the **predeclared scope**, holding the built-in type names and the
  predeclared identifiers `true`, `false`, `null`;
- the **built-in scope**, holding the ambient declarations of `sngl:builtin`;
- the **package scope**, holding the user program's top-level declarations and
  the names its dot imports lift;
- a **component scope** for each component, holding its parameters, variables,
  nested functions, and nested types;
- a **function scope** for each function or method body, holding its
  parameters;
- a fresh **block scope** for each `if` branch, `else` branch, `for` body, and
  nested statement block, holding that block's local variables;
- a **loop scope** wrapping a `for` body, holding the loop variables.

Lookup proceeds outward through the enclosing scopes. A name declared in an
inner scope **shadows** the same name in an outer scope; package declarations
shadow built-in and imported declarations of the same name, which is how user
code overrides a library entity. Declaring the same name twice in one scope is an
error. Methods attached to a type (`func Type.m`) occupy that type's method set
rather than a value scope, so a user method may override a standard-library
method of the same name on the same type.

### Predeclared identifiers and the library tiers

Everything predeclared is an ordinary declaration in `sngl:builtin` —
the scalar and collection types (`int`, `float`, `string`, `list`, `map`,
`option`, `ref`, `iter`, `color`, `date`, `time`, `datetime`) with their
methods, and the constants `true`, `false`, `null`, `PLATFORM` and `LANGUAGE`.
That package is
dot-imported into every file implicitly and cannot be imported explicitly; it
is the only implicit import in the language.

`PLATFORM` and `LANGUAGE` name the target a build is producing — `"html"`,
`"go"`, and so on. Comparing one against a literal gates code on the target:
the comparison folds at build time and the branch not taken is removed. The
compiler supplies their values, so a declaration of your own by either name is
an ordinary constant and shadows the predeclared one.

Nothing here is a keyword. `true`, `false` and `null` resolve through the scope
chain like every other name, and a declaration of your own by one of those
names shadows it — the grammar reserves none of them.

Everything else the standard library provides is imported. The components,
event payload types, style enums, `Style` and the `window` they are placed on
belong to `sngl:ui`; `date`, `time`, `datetime`, `duration` and `timer` to
`sngl:time`; `Alert` and `File` to `sngl:dialog`; `Test` to `sngl:test`; and
the translation surface to `sngl:i18n`:

```sngl
import . "sngl:ui"
import sngl "sngl:ui"
```

The dot form flattens the package's declarations into the file, so they are
written unqualified (`text(...)`). The alias form binds a namespace instead,
under whatever name the importer chooses (`sngl.text(...)`).

Both packages register beneath the package scope, so a top-level declaration
named like a library entity takes precedence over it within the package.

The library is not limited to those two packages. `sngl:ui/draw` holds `canvas`
and the 2D shapes it hosts, and is imported the same way. A library package may
also carry macros next to the declarations they apply to: `import "sngl:ui/draw"`
brings both the shape components and the `#[draw.shape]` mark that declares new
ones.

Packages under `sngl:internal/` are the compiler's own tier. They declare the
intrinsics a backend implements natively — the string, list, map and formatting
primitives the packages above are written against — and the marks that identify
them. A program may name one, as it may any package, but nothing there is part
of the language a program is written in, and documentation indexes leave them
out.

### Package comments

A run of line comments at the top of a file, separated from what follows by a
blank line, documents the package rather than the declaration below it. The
text is markdown. Where several of a package's files carry one, they are
concatenated in load order, separated by blank lines; that order is
unspecified, so prose whose sequence matters belongs in one file.

### Exported and unexported names

A name is **unexported** if it begins with an underscore (`_`), and **exported**
otherwise. The distinction governs cross-package access only: an unexported
name is freely referenced anywhere within the package that declares it, but is
not reachable from another package. That covers three routes — a qualified
reference (`ns.name`), a name lifted by a dot import, and a **member reached
through an imported declaration**: importing a type does not carry its
unexported fields or methods with it.

<!-- SNGL-nocheck -->

```sngl
// package w
struct Box {
    v       int = 0
    _hidden int = 0
}

func Box._secret() => 42
```

Given `import w "w"`, a value of `w.Box` exposes `v` but neither `_hidden` nor
`_secret`. The rule holds wherever a member is named, not only on a field read:
a struct literal (`w.Box{_hidden = 2}`), a method call, an enum member reached
through its type (`w.Mode._B`) or resolved bare against an expected enum type,
and an assignment target are all rejected alike.

### One name, one meaning at file scope

A name may be bound once at file scope. Two declarations of it, two imports
claiming it as an alias, two dot imports lifting it, or a declaration taking a
name an import alias already binds are all errors — none of them has a
tiebreak, so resolving by source order would make meaning depend on ordering.

The single exception is shadowing, where exactly one of the two bindings is
written in this file: a declaration may shadow a name that a dot import lifted,
including a built-in. This is what lets a package define its own `text` or
`color` over the library's.

The rule covers every kind of declaration a file scope holds — types,
components, free functions, constants and variables alike — and the alias an
import binds. Where a name is genuinely taken, an alias resolves it: an import
chooses its own alias, so `import d "sngl:ui/draw"` reaches a package
whose default name a dot import already claimed.

Two bindings that mean the same package are a restatement, not a conflict. The
standard library exposes the intrinsic namespaces it imports, so a file may
also import one of them by name without colliding.

Inside a body the same principle applies to a narrower scope: a duplicate
local constant, a duplicate component-level variable, and a `for` loop binding
one name to both of its variables are all errors.

## Constants and variables

<!-- BEGIN GENERATED: grammar-constants-variables -->

```ebnf
ConstDecl = 
    "const" ConstSpec
    | "const" "(" { ConstSpec [ "," | ";" ] } ")"

ConstSpec = IdentList [ Type ] "=" Expr

IdentList = IDENT { "," IDENT }

VarDecl = 
    "var" VarSpec
    | "var" "(" { VarSpec [ "," | ";" ] } ")"

VarSpec = IdentList [ Type ] [ "=" Expr ] { VarHandler }

VarHandler = "@" IDENT [ "(" [ ParamList ] ")" ] StmtBlock

```

<!-- END GENERATED: grammar-constants-variables -->

A `const` declaration binds an immutable name. Its initializer must be a
*constant expression*: a literal, an enum member, a primitive conversion, a
reference to another constant, or one of the predeclared constants `true`,
`false`, `null`, `PLATFORM`, `LANGUAGE`. A constant initializer may not
reference a variable, a function, or a context, and may not forward-reference a
name not yet declared. Assigning to a constant is an error.

A `var` declaration binds a mutable name. The type may be given explicitly,
inferred from the initializer, or — if neither is present — defaults to `dyn`.
A variable initializer is evaluated **once**; it provides the initial value
only and does not establish a reactive dependency. A derived value that should
recompute when its inputs change must be a function, not a variable (see
[Reactivity](#reactivity)).

Both forms have a grouped variant — `const ( … )` and `var ( … )` — that lists
several specifications inside parentheses. A `var` declaration may carry
[handlers](#variable-handlers).

## Functions and methods

<!-- BEGIN GENERATED: grammar-functions -->

```ebnf
FuncDecl = "func" FuncName [ TargetIndex ] FuncTail

FuncTail = 
    "(" [ ParamList ] ")" FuncBodyTail
    | FuncBodyTail

FuncBodyTail = 
    "=>" Expr
    | [ Type ] [ StmtBlock ]

FuncName = IDENT [ TypeParamList [ "." IDENT [ TypeParamList ] ] | "." IDENT [ TypeParamList ] ]

TypeParamList = "<" TypeParam { "," TypeParam } ">"

TypeParam = IDENT [ "=" Type ]

ParamList = Param { "," Param } [ "," ]

Param = { MacroAttr } ( ":" IDENT | "@" IDENT | IDENT ) [ Type ] [ "=" Expr ]

```

<!-- END GENERATED: grammar-functions -->

A function has one of two bodies, and no third form exists:

- a **block body** — `func name(params) [Type] { … }` — whose return type
  annotation is optional and required only when the function returns a value;
- an **expression body** — `func name(params) => expr` — whose result type is
  always inferred and which therefore may not carry a return type annotation.

The form `func name(params) -> Type` is not valid syntax; the arrow `->` is
reserved for function *type* expressions only.

A parameter is a name with an optional type and an optional default value. The
parameter list itself is optional: `func now => …` declares a parameterless
function. A function declared with no parameters and an expression body is a
*computed* value; in an operand position where a value is expected it is called
implicitly.

A **method** is a function whose name is qualified by a receiver type,
`func Type.method(…)`. Type parameters may appear after the receiver type
(receiver-level) and after the method name (method-level); see
[Generic type parameters](#generic-type-parameters). A method is dispatched on
the type of the receiver expression.

A **function literal** (`FuncLit`) is an anonymous function written `func(params) => expr` or `func(params) [Type] { … }`; it is an ordinary expression of
function type and may capture variables from the enclosing scope.

### Purity

Every function is classified by its effect on state:

- **pure** — reads neither variables nor state, only its parameters;
- **read-only** — reads state but assigns to none;
- **mutating** — assigns to at least one state variable.

The classification follows from the body and constrains observable behavior: a
pure or read-only computed yields the same result for the same inputs and may
be re-evaluated on demand, whereas a mutating function runs exactly once per
call.

### Asynchrony

A function is *asynchronous* if it transitively calls an asynchronous primitive
(for example a host-language import that returns a promise). Asynchrony
propagates along the call graph to a fixed point. An asynchronous function that
takes parameters may not be used in a *reactive expression* (a property of a
visual node), because the reactive update mechanism keys a single cached result
per computed and cannot distinguish per-argument results; asynchronous calls
are permitted in event handlers, timer bodies, and ordinary function bodies.

## Expressions

An expression denotes a value. Operators combine sub-expressions according to
the precedence and associativity in the table below; primary expressions are
operands.

<!-- BEGIN GENERATED: grammar-expressions -->

```ebnf
Expr = TernaryExpr

TernaryExpr = OrExpr [ "?" Expr ":" Expr ]

OrExpr = AndExpr  { "||"  AndExpr  }

AndExpr = EqExpr   { "&&" EqExpr   }

EqExpr = CmpExpr  { EqOp  CmpExpr  }

CmpExpr = AddExpr  { CmpOp AddExpr  }

AddExpr = MulExpr  { AddOp MulExpr  }

MulExpr = UnaryExpr { MulOp UnaryExpr }

EqOp = "==" | "!="

CmpOp = "<" | "<=" | ">" | ">="

AddOp = "+" | "-"

MulOp = "*" | "/" | "%"

UnaryExpr = 
    PostfixExpr
    | "!"  UnaryExpr
    | "-" UnaryExpr
    | "&"   UnaryExpr
    | "*"  UnaryExpr
    | "const" UnaryExpr

PostfixExpr = PrimaryExpr { ExprPostfixOp }

PrimaryExpr = 
    INT
    | FLOAT
    | UNIT
    | STRING
    | TRIPLE_STRING
    | RAW_STRING
    | InterpStr
    | TripleInterp
    | I18N_STR_FULL
    | I18N_TRIPLE_FULL
    | I18nInterpStr
    | I18nTriple
    | HASH
    | "(" Expr ")"
    | "[" ListBody "]"
    | AnonStructLit
    | FuncLit
    | StructDecl StructLitBody
    | ImportExpr StructLitBody
    | IDENT [ StructLitBody ]

ExprPostfixOp = 
    "." IDENT [ StructLitBody ]
    | "[" Expr "]"
    | "(" [ ArgList ] ")"

StmtPostfixOp = 
    "." IDENT
    | HASH
    | "[" Expr "]"
    | "(" [ ArgList ] ")"
    | [ "(" [ ArgList ] ")" ] StmtBlock

```

<!-- END GENERATED: grammar-expressions -->

### Operator precedence

From lowest to highest binding:

<!-- BEGIN GENERATED: precedence -->

| Precedence | Operators                           | Associativity | Description    |
|------------|-------------------------------------|---------------|----------------|
| 1          | `? :`                               | right         | Ternary        |
| 2          | <code>&#124;&#124;</code>           | left          | Logical OR     |
| 3          | `&&`                                | left          | Logical AND    |
| 4          | `==`, `!=`                          | left          | Equality       |
| 5          | `<`, `>`, `<=`, `>=`                | left          | Comparison     |
| 6          | `+`, `-`                            | left          | Addition       |
| 7          | `*`, `/`, `%`                       | left          | Multiplication |
| 8          | `!`, `-`, `&`, `*`, `const` (unary) | right         | Unary          |
| 9          | `.`, `[]`, `()`                     | left          | Postfix        |

<!-- END GENERATED: precedence -->

The ternary operator `c ? a : b` evaluates `a` when `c` is true and `b`
otherwise; `c` must be `bool`. The unary operators are `!` (logical negation,
on `bool`), `-` (numeric or unit negation), `&` (reference), `*`
(dereference), and `const` (constant assertion).

### Operands

Operands are literals, identifiers, parenthesized expressions, list literals,
struct and map literals, function literals, interpolated and translatable
strings, and color and unit literals. An event name is not an operand: the
`@` sigil belongs to an event's declaration, its handler and a `var`
handler, and an event is referred to in an expression by its bare name. An
element reference (`#id`) is not an operand either; it appears only as a
postfix declaration tag (see [Element references](#element-references-1)).

<!-- BEGIN GENERATED: grammar-literals -->

```ebnf
ListBody = [ ListElem { "," ListElem } [ "," ] ]

ListElem = "..." Expr | Expr

StructLitBody = "{" [ AnonField { ( "," | ";" ) AnonField } [ "," | ";" ] ] "}"

AnonStructLit = "{" [ AnonField { ( "," | ";" ) AnonField } [ "," | ";" ] ] "}"

AnonField = 
    "..." Expr
    | Expr [ "=" Expr ]

FuncLit = "func" [ "(" [ ParamList ] ")" ] FuncBodyTail

```

<!-- END GENERATED: grammar-literals -->

A **list literal** `[a, b, …]` may contain spread elements `...xs`. A **struct
literal** is written `Name{field = value, …}` (named) or `{field = value, …}`
(anonymous), and may spread another struct's fields with `...other`; the same
brace syntax denotes a **map literal** when a map type is expected. A struct
literal whose head is a bare identifier is recognized only outside condition
position, where a following `{` would otherwise begin a statement block (see
[Condition expressions](#condition-expressions)).

### Selectors, indexing, and calls

A primary expression may be followed by postfix operators:

- `.field` or `.method` — member selection (a named node is addressed the same
  way, `c.id.value`, and an event invoked through a reference, `c.inc.click()`,
  is an ordinary call — neither carries a sigil);
- `[index]` — indexing into a list or map;
- `(args)` — a call.

The exact set of admissible postfix operators depends on context: in expression
position a `.field` may be followed by a struct-literal body, whereas in
statement position a primary may instead be followed by a statement block (a
visual node body). See the grammar for `ExprPostfixOp`, `StmtPostfixOp`, and
`CondPostfixOp`.

### Arguments

A call's argument list unifies positional arguments, named arguments
(`name = value`, the same surface as struct-literal fields), binding arguments
(`:prop = target`), spread arguments (`...xs`), and event handlers
(`@event { … }`). Named and binding arguments are meaningful chiefly at visual
node and component instantiations (see [Components](#components)).

<!-- BEGIN GENERATED: grammar-argument-lists -->

```ebnf
ArgList = Arg { ("," | ";") Arg } [ "," | ";" ]

Arg = 
    ":" IDENT [ Type ] [ "=" Expr ]
    | "..." Expr
    | EventArg
    | IDENT IdentArgCont
    | "!" UnaryExpr ArgExprCont
    | "-" UnaryExpr ArgExprCont
    | "&" UnaryExpr ArgExprCont
    | "*" UnaryExpr ArgExprCont
    | "const" UnaryExpr ArgExprCont
    | NonIdentPrimary { StmtPostfixOp } ArgExprCont

EventArg = "@" IDENT [ "(" [ ParamList ] ")" ] StmtBlock

IdentArgCont = 
    "=" Expr
    | StructLitBody { ExprPostfixOp } ArgExprCont
    | { ExprPostfixOp } ArgExprCont

ArgExprCont = 
    { MulOp UnaryExpr }
    { AddOp MulExpr }
    { CmpOp AddExpr }
    { EqOp CmpExpr }
    { "&&" EqExpr }
    { "||" AndExpr }
    [ "?" Expr ":" Expr ]

```

<!-- END GENERATED: grammar-argument-lists -->

### Operator semantics

- The logical operators `&&`, `||`, `!` require `bool` operands and yield
  `bool`.
- Equality `==` / `!=` applies when the operands have the same kind, when both
  are numeric, or when one is `null`; it yields `bool`.
- The relational operators `<`, `<=`, `>`, `>=` apply to two numbers, to two
  values of one unit type, or to two strings.
- `+` is addition on numbers, concatenation when either operand is a `string`,
  and the dimension-preserving sum of two values of one unit type.
- `-`, `*`, `/`, `%` are the arithmetic operators; `int` divided by `int`
  truncates. A unit may be scaled by a number (`5px * 2`), and dividing two
  values of one unit type yields a dimensionless `float`.

When a binary arithmetic operator mixes `int` and `float` operands, the `int`
operand is converted to `float` and the result is `float`.

### String interpolation expressions

The segments of an interpolated string surround expressions that are each
implicitly converted to `string`. A custom type participates in interpolation
only if it converts to `string` (lists and options fall back to a built-in
stringification). A parameterless computed in an interpolation is called
implicitly.

<!-- BEGIN GENERATED: grammar-string-interpolation -->

```ebnf
InterpStr = STR_START Expr { STR_RESUME Expr } STR_END

TripleInterp = TRIPLE_START Expr { STR_RESUME Expr } TRIPLE_END

I18nInterpStr = I18N_STR_START I18nPlaceholder { I18N_STR_RESUME I18nPlaceholder } I18N_STR_END

I18nTriple = I18N_TRIPLE_START I18nPlaceholder { I18N_STR_RESUME I18nPlaceholder } I18N_TRIPLE_END

I18nPlaceholder = Expr [ "," IDENT [ "," I18nThirdArg ] ]

```

<!-- END GENERATED: grammar-string-interpolation -->

### Condition expressions

In the condition of an `if` and the iterator of a `for`, a `{` begins the
statement block that follows, so the expression grammar in these positions
(`CondExpr`) excludes the bare-identifier struct literal and the trailing
statement block. Parenthesize a struct literal if one is genuinely needed in a
condition.

## Statements

<!-- BEGIN GENERATED: grammar-statements -->

```ebnf
Stmt = 
    ImportDecl
    | StructDecl
    | EnumDecl
    | UnitDecl
    | ConstDecl
    | VarDecl
    | FuncDecl
    | ComponentDecl
    | "return" [ Expr ]
    | "break"
    | "continue"
    | IfNode
    | ForNode
    | VisualOrStmt

VisualOrStmt = StatementPrimary { StmtPostfixOp } [ AssignOp Expr | "!!" | IncDecOp ]

IfNode = "if" CondExpr StmtBlock [ "else" ( IfNode | StmtBlock ) ]

ForNode = "for" ( "var" [ "&" ] IDENT [ "," [ "&" ] IDENT ] "=" CondExpr | [ CondExpr ] ) StmtBlock [ "else" StmtBlock ]

AssignOp = "=" | "+=" | "-=" | "*=" | "/=" | "%="

IncDecOp = "++" | "--"

```

<!-- END GENERATED: grammar-statements -->

### Assignment

An assignment `target = value` stores `value` into an *lvalue*: a variable, a
parameter, a loop variable, a field selection, an index expression, or an
element reference. The value must be assignable to the target's type. The
compound forms `+= -= *= /= %=` apply the corresponding arithmetic operator to
the current value; `target++` and `target--` are shorthand for adding or
subtracting `1` and require a numeric target. Constants and contexts may not be
assigned.

### The toggle statement

`target!!` negates a boolean lvalue in place; it is equivalent to
`target = !target` and requires a `bool` (or `dyn`) target.

### The return statement

`return` with no operand returns from a function that yields no value; `return expr` returns a value, which must be assignable to the function's declared or
inferred result type.

### The break and continue statements

`break` ends the innermost enclosing loop; `continue` ends the current
iteration of it and begins the next. Neither takes a label, and neither may be
written where no loop encloses it.

A loop encloses these statements only if it is one they can still be running
inside. A lambda body starts over: a `break` written in a lambda that sits in a
loop body acts on a loop in the lambda, not on the loop the lambda was written
inside, because the lambda's body runs later — or not at all — and by then that
loop may be over.

Both are restricted to imperative bodies, for the reason the loop forms below
are: a view body's loop is a template stamped once per element, not a statement
stream, so there is no iteration for an escape to cut short.

### The if statement

```
IfNode = "if" CondExpr StmtBlock [ "else" StmtBlock ]
```

The condition must be `bool` (or `dyn`); a parameterless `bool`-returning
computed is called implicitly. The `if` body and the optional `else` body are
each their own scope.

### The for statement

```
ForNode = "for" ( "var" [ "&" ] IDENT [ "," [ "&" ] IDENT ] "=" CondExpr | [ CondExpr ] ) StmtBlock [ "else" StmtBlock ]
```

`for` has one head, and what that head *is* says what the loop does. An
iterable is walked, a `bool` is a condition tested before each iteration, and
no head at all is a loop that runs until its body leaves it.

Iterating a list, an iterator, or a map:

- `for var x = xs` binds `x` to each element of a list or iterator;
- `for var i, x = xs` binds `i` to the index (an `int`) and `x` to the element;
- `for var k, v = m` binds `k` and `v` to each key and value of a map; map
  iteration requires the two-variable form;
- `for xs` binds nothing, for a loop whose body never names the element.

A loop that names its element declares a variable, and `var` says so, as it
does everywhere else a name is introduced. It is also what tells the two forms
apart: without it, the head is the iterable alone.

The loop variables are scoped to the loop body. Prefixing the element variable
with `&` (`for var &x = xs`, `for var i, &x = xs`) binds it as a `ref<T>`, so that
assigning to `x` — or to a field of `x` — writes through to the underlying list
element by index. The index variable may not be taken by reference.

Iterating on a condition, or on nothing:

- `for x < n` runs its body while the head is `true`, testing it before each
  iteration;
- `for` runs its body until a `break` or a `return` leaves the loop.

Neither walks anything, so neither declares a variable. `for var x = cond` is
an error — there is no element for it to bind — and the headless form has no
way to write `var` at all, since the grammar takes it only in the branch that
goes on to require an `=` and an expression.

Both are restricted to **imperative bodies** — a function, a handler, a timer.
A view body repeats its body once per element of something, which is what
gives the rendered tree a shape: a list gives that a length and a counted
sequence gives it a number, while a condition gives it neither. Writing either
form in a view body is an error.

A loop over a **map** is restricted the same way, for the same reason applied
to order rather than to count. A map says how many copies of the body the tree
holds and in no defined order, so two renders of one map may lay it out
differently. Walk the map in a function or a handler and render the list that
comes back.

A head expression may not begin with `{`: that brace is the body's. A map or
anonymous-struct literal in the head of a `for` — or of an `if` — is written
parenthesized.

#### The else block

A loop's `else` block runs when **the body never ran**:

<!-- SNGL-component -->

```sngl
import . "sngl:ui"
var items list<string> = []
for var item = items {
    text(value=item)
} else {
    text(value="Nothing yet")
}
```

For a loop over an iterable that is "the iterable was empty"; for a condition
loop it is "the condition was false the first time it was asked". A `break`
does not trigger the `else`, because a loop cannot break out of a body that
never ran.

`for { } else { }` is an error: a loop with no condition always runs its body,
so the block would be unreachable rather than an empty case.

### The platform statement

```
PlatformNode = "platform" IDENT StmtBlock
```

A `platform` block contains statements that apply only when compiling for the
named platform; for any other target the block is dropped. Within the block the
named platform's package is added to the scope as a fallback, so platform-
specific names (including raw target elements, such as HTML tags) resolve.

### The emit statement

An **emit** fires an event declared on the enclosing component, invoking the
handler the caller attached. It is written as an ordinary call on the event's
name — `event(args)` — and carries no sigil; the checker resolves the name to
the declared event. See [Events](#events).

## Components

A **component** is a reusable, parameterized fragment of user interface.

<!-- BEGIN GENERATED: grammar-components -->

```ebnf
ComponentDecl = "component" IDENT [ "." IDENT ] [ TypeParamList ] [ TargetIndex ] [ "(" [ ParamList ] ")" ] [ Type ] [ StmtBlock ]

```

<!-- END GENERATED: grammar-components -->

```
component Name(params) ChildrenType { body }
```

The parameter list defines the component's public interface; the optional
children type declares what the component may contain; the body is a sequence
of statements, chiefly visual node instantiations. An optional `.Variant`
suffix on the name (`component Name.Variant(…)`) is accepted by the grammar and
reserved for platform-level specialization.

The body is optional, as a function's is, and the two spellings say different
things. An empty body — `component Spacer() { }` — says the component renders
nothing. **No body at all** — `component Name(params) Tree` — is a
*signature*: the render comes from somewhere the declaration names, and the
checker requires that to be true. The three answers are an `#[intrinsic]` id a
platform emits, a `#[builtin]` node kind the compiler dispatches on, and a
per-target override. An override may not itself be bodyless, since an override
*is* the body a target renders.

### Parameters

A component parameter is one of three kinds:

- a **regular parameter** — `name Type = default` — a read-only input supplied
  by the caller, with an optional default;
- a **binding parameter** — `:name Type` — a two-way bound property: the caller
  passes an lvalue with `:name = target`, and the component writes back to that
  lvalue by emitting the corresponding change, so parent and child stay in
  sync;
- an **event parameter** — `@name Type` (the type is optional, denoting a
  payloadless event) — an outgoing event the component fires by calling its
  name (`name(args)`) and the caller handles with `@name { … }`.

### Slots and children

A **slot** is a region of UI the caller supplies. It is an ordinary parameter
whose type is a **component type**, so a component's whole API — props, events
and slots — is one parameter list:

- `header component` — any number of nodes, of whatever family the component
  itself belongs to;
- `shapes component shape` — any number of that tree's members;
- `body component tree.one<T>` — exactly one;
- `badge component option<T>` — zero or one;
- `cell component(Row)` — a *scoped* slot: the insertion passes a `Row`, and
  the population binds a name for it.

A slot's invocation parameters may be named in the type — `cell component(row Row)` — and the name is **part of the contract**, not documentation: two slot
types match by name. Renaming one is therefore a breaking change to every
component supplied to that slot.

A slot renders where its name is written in the body, as an ordinary node
(`header { … }` supplies a fallback, `cell(r)` passes an argument). When an
insertion appears inside a conditional or loop, the surrounding structure is
rendered per the reactive rules below.

A caller populates a slot by name with a `component` declaration written
directly in the instantiation's block:

<!-- SNGL-component
struct Row { title string }
component table(rows list<Row>, cell component(Row)) node { vbox { for var r = rows { cell(r) } } }
var rs list<Row> = []
-->

```sngl
table(rows=rs) {
    component cell(row) {
        text(value=row.title)
    }
}
```

The parameters a population declares are its own names for what the insertion
passes, matched by position; a type written on one is optional and must agree
with the position it names.

**The rest slot takes the children written bare.** `...` before the component
type is a count bound saying the slot collects everything the caller did not
supply by name:

```sngl
import . "sngl:ui"

component card(header component, content ...component) node {
    vbox {
        header {}
        content
    }
}
```

A component declares at most one rest slot, and a component that declares none
accepts no children at all. `...` composes with a count bound — `content ...component tree.one` is "the bare children, of which exactly one" — because
the two say different things: `...` says which children arrive here, the
wrapper how many. A rest slot names no invocation parameters — bare
children are written once, with nothing to bind them to — and populating it by
name *and* writing bare children populates it twice.

A `component` declaration in a body is read by **position**: at the root of a
component definition it is a nested declaration, and directly in a child node's
block it is a slot population. Anywhere else — inside an `if` or `for`, or in a
function body — is an error.

### Instantiation and visual nodes

A statement of the form `Name(args) { children }` instantiates a component or a
platform element as a **visual node**. Its arguments may be positional, named,
binding (`:prop = target`), and event (`@event { … }`) arguments; nested
statements form its children. A node may be labeled with an element reference,
`Name #id(args)`, naming it for later use.

### Element references

Attaching `#id` to a node (`button #id(…)`) names it within the component and
introduces `id` as a component-scoped binding: an opaque, immutable handle to
that node (it does not shadow an existing name). The node is reached either by
that bare name or, from a handle to its container, by ordinary field
selection — `c.id.value` reads the node's state and `c.id.click()` drives its
events (event invocation through a reference is an ordinary call on the event
name, `value.event(args)`). The `#` sigil itself appears only as the node- or
context-naming declaration tag; it is never a selection field nor written on
its own as an operand.

## Reactivity

SNGL is reactive: when state changes, the parts of the UI that depend on it
update, with the dependencies determined entirely at compile time. There is no
virtual DOM and no run-time diffing of the whole tree.

### State and derivation

- A **reactive variable** is any non-constant `var` at package, component, or
  window scope. Its initializer runs once; thereafter its value changes only by
  assignment.
- A **derived value** is a parameterless function (`func total => price * qty`).
  It re-evaluates whenever a reactive variable it reads is assigned.

Each reactive variable has a statically determined set of dependents — every
node property, condition, and loop iterator whose expression reads it — and an
assignment to the variable updates exactly those. A conditional or loop whose
condition or iterator is reactive is re-rendered as a unit when its
dependencies change.

This is the central reason `var` and `func` differ: `var x = expr` captures a
value once, while `func x => expr` defines a relationship that the compiler
keeps current. Use `var` for state and `func` for anything derived from it.

### Variable handlers

A `var` may carry one or more handlers that run after each assignment to it:

```
var count = 0 @change(old) { … }
```

The optional parameter binds the previous value. The handler body executes
after the new value is in place; it may itself assign to other reactive
variables.

### References in loops

A reference loop variable (`for var &x = xs`) makes assignments to the element — and
to its fields — write back to the list by index, as described under
[The for statement](#the-for-statement). This is the supported way to mutate a
list's elements in place; structs are otherwise value types and copying them
would discard the mutation.

### Timers

A `timer` node fires its `@tick` handler at a fixed interval while it is
enabled:

```
timer(interval = 100ms, enabled = running, @tick { progress += 0.1 })
```

Toggling the enabling variable cancels or restarts the timer; a timer is torn
down when its component is removed. The interval is a `duration`.

## Modules

A program is organized into **packages**. A package is the set of `.sngl` files
in one directory; all of its files share a single top-level namespace and are
checked together as one unit. A name declared in any file of a package is
visible throughout the package, and declaring the same top-level name twice
within a package is an error.

### Imports

<!-- BEGIN GENERATED: grammar-imports -->

```ebnf
ImportDecl = "import" [ IDENT | "." ] STRING [ "=>" STRING ]

```

<!-- END GENERATED: grammar-imports -->

An import binds a *namespace* through which another package's exported names are
reached:

```
import "widgets"            // namespace "widgets"; widgets.Button
import w "widgets"          // explicit alias: w.Button
import "ui/cards"           // namespace "cards" (last path segment)
```

Without an explicit alias, the namespace name is the last segment of the path.
A member is accessed as `namespace.Member`.

The `=> "target"` form redirects a local import path to another target while
keeping the local name:

```
import "cdn/ui" => "https://cdn.example.com/ui"
```

### Scheme imports and target routing

An import path may carry a URI scheme, which routes the import to a provider
rather than to a directory of `.sngl` files:

- `import "go:fmt"`, `import "ts://lodash"` — import declarations from a host
  language package, so generated code in that language can call into it;
- `import "sngl:platform/html"`, `import "sngl:language/go"` — bring a
  platform's or language's contributed package into scope;
- other schemes may be resolved by the host to fetch remote SNGL sources.

An import of a host-language package is the boundary at which the *language*
selected for compilation matters: see the next section. Members of a scheme
import are namespaced exactly like directory imports.

## Platform and language plugins

A compilation targets a **platform** (what is rendered — HTML, a Bubble Tea
TUI, Android/Compose, a Fyne desktop window, …) paired with a **language** (the
host language of the emitted code — Go, JavaScript, Kotlin, …). Platforms and
languages are plugins, not part of the core language; this specification fixes
the *contract* between the core and a plugin but not any plugin's
implementation.

The contract has these observable facts:

- **Checked IR is the interface.** A plugin consumes the typed intermediate
  representation produced by the front end this manual specifies, never the
  source text. Anything a plugin needs about a program is present in that
  representation.
- **Capabilities gate lowering.** A language declares the features it supports
  natively (ternaries, lambdas, reactivity primitives, and so on). The compiler
  runs capability-driven lowering passes between checking and code generation,
  each gated by a feature flag, so that a program is rewritten into the subset a
  target can express — for example replacing lambdas, ternaries, or reactive
  bindings where a target lacks them. These rewrites preserve the observable
  semantics defined in this manual; they do not change what a well-formed
  program means.
- **Platform packages contribute names.** A platform or language may contribute
  a package of declarations reachable through a `sngl:platform/…` or
  `sngl:language/…` import, and `platform` blocks may resolve
  otherwise-unknown identifiers against the active platform (for example raw
  HTML tag names).
- **Some targets restrict programs.** A platform may support only certain
  languages, and a language may lack a capability that a program relies on; such
  a combination is rejected at build time rather than mis-compiled.

A conforming front end need not implement any platform or language. It must
produce the intermediate representation and enforce the core rules above;
plugins are responsible for everything downstream.

## Grammar appendix

The complete grammar, assembled from `internal/parser/sngl.ebnf`:

<!-- BEGIN GENERATED: grammar-full -->

```ebnf
Document = native_value Expr [ ";" ] | { [ "/-" ] { MacroAttr } Stmt [ ";" ] }

StmtBlock = "{" { [ "/-" ] { MacroAttr } Stmt [ ";" ] } "}"

```

```ebnf
Stmt = 
    ImportDecl
    | StructDecl
    | EnumDecl
    | UnitDecl
    | ConstDecl
    | VarDecl
    | FuncDecl
    | ComponentDecl
    | "return" [ Expr ]
    | "break"
    | "continue"
    | IfNode
    | ForNode
    | VisualOrStmt

VisualOrStmt = StatementPrimary { StmtPostfixOp } [ AssignOp Expr | "!!" | IncDecOp ]

IfNode = "if" CondExpr StmtBlock [ "else" ( IfNode | StmtBlock ) ]

ForNode = "for" ( "var" [ "&" ] IDENT [ "," [ "&" ] IDENT ] "=" CondExpr | [ CondExpr ] ) StmtBlock [ "else" StmtBlock ]

AssignOp = "=" | "+=" | "-=" | "*=" | "/=" | "%="

IncDecOp = "++" | "--"

```

```ebnf
ImportDecl = "import" [ IDENT | "." ] STRING [ "=>" STRING ]

```

```ebnf
StructDecl = "struct" [ IDENT ] [ TypeParamList ] "{" { StructBodyItem [ ";" ] } "}"

StructField = IdentList Type [ "=" Expr ]

EnumDecl = "enum" [ IDENT ] "{" [ EnumBodyItem { ("," | ";") EnumBodyItem } [ "," | ";" ] ] "}"

UnitDecl = "unit" [ IDENT ] "{" [ ArgList ] "}"

```

```ebnf
ConstDecl = 
    "const" ConstSpec
    | "const" "(" { ConstSpec [ "," | ";" ] } ")"

ConstSpec = IdentList [ Type ] "=" Expr

IdentList = IDENT { "," IDENT }

VarDecl = 
    "var" VarSpec
    | "var" "(" { VarSpec [ "," | ";" ] } ")"

VarSpec = IdentList [ Type ] [ "=" Expr ] { VarHandler }

VarHandler = "@" IDENT [ "(" [ ParamList ] ")" ] StmtBlock

```

```ebnf
FuncDecl = "func" FuncName [ TargetIndex ] FuncTail

FuncTail = 
    "(" [ ParamList ] ")" FuncBodyTail
    | FuncBodyTail

FuncBodyTail = 
    "=>" Expr
    | [ Type ] [ StmtBlock ]

FuncName = IDENT [ TypeParamList [ "." IDENT [ TypeParamList ] ] | "." IDENT [ TypeParamList ] ]

TypeParamList = "<" TypeParam { "," TypeParam } ">"

TypeParam = IDENT [ "=" Type ]

ParamList = Param { "," Param } [ "," ]

Param = { MacroAttr } ( ":" IDENT | "@" IDENT | IDENT ) [ Type ] [ "=" Expr ]

```

```ebnf
ComponentDecl = "component" IDENT [ "." IDENT ] [ TypeParamList ] [ TargetIndex ] [ "(" [ ParamList ] ")" ] [ Type ] [ StmtBlock ]

```

```ebnf
Expr = TernaryExpr

TernaryExpr = OrExpr [ "?" Expr ":" Expr ]

OrExpr = AndExpr  { "||"  AndExpr  }

AndExpr = EqExpr   { "&&" EqExpr   }

EqExpr = CmpExpr  { EqOp  CmpExpr  }

CmpExpr = AddExpr  { CmpOp AddExpr  }

AddExpr = MulExpr  { AddOp MulExpr  }

MulExpr = UnaryExpr { MulOp UnaryExpr }

EqOp = "==" | "!="

CmpOp = "<" | "<=" | ">" | ">="

AddOp = "+" | "-"

MulOp = "*" | "/" | "%"

UnaryExpr = 
    PostfixExpr
    | "!"  UnaryExpr
    | "-" UnaryExpr
    | "&"   UnaryExpr
    | "*"  UnaryExpr
    | "const" UnaryExpr

PostfixExpr = PrimaryExpr { ExprPostfixOp }

PrimaryExpr = 
    INT
    | FLOAT
    | UNIT
    | STRING
    | TRIPLE_STRING
    | RAW_STRING
    | InterpStr
    | TripleInterp
    | I18N_STR_FULL
    | I18N_TRIPLE_FULL
    | I18nInterpStr
    | I18nTriple
    | HASH
    | "(" Expr ")"
    | "[" ListBody "]"
    | AnonStructLit
    | FuncLit
    | StructDecl StructLitBody
    | ImportExpr StructLitBody
    | IDENT [ StructLitBody ]

ExprPostfixOp = 
    "." IDENT [ StructLitBody ]
    | "[" Expr "]"
    | "(" [ ArgList ] ")"

StmtPostfixOp = 
    "." IDENT
    | HASH
    | "[" Expr "]"
    | "(" [ ArgList ] ")"
    | [ "(" [ ArgList ] ")" ] StmtBlock

```

```ebnf
ArgList = Arg { ("," | ";") Arg } [ "," | ";" ]

Arg = 
    ":" IDENT [ Type ] [ "=" Expr ]
    | "..." Expr
    | EventArg
    | IDENT IdentArgCont
    | "!" UnaryExpr ArgExprCont
    | "-" UnaryExpr ArgExprCont
    | "&" UnaryExpr ArgExprCont
    | "*" UnaryExpr ArgExprCont
    | "const" UnaryExpr ArgExprCont
    | NonIdentPrimary { StmtPostfixOp } ArgExprCont

EventArg = "@" IDENT [ "(" [ ParamList ] ")" ] StmtBlock

IdentArgCont = 
    "=" Expr
    | StructLitBody { ExprPostfixOp } ArgExprCont
    | { ExprPostfixOp } ArgExprCont

ArgExprCont = 
    { MulOp UnaryExpr }
    { AddOp MulExpr }
    { CmpOp AddExpr }
    { EqOp CmpExpr }
    { "&&" EqExpr }
    { "||" AndExpr }
    [ "?" Expr ":" Expr ]

```

```ebnf
ListBody = [ ListElem { "," ListElem } [ "," ] ]

ListElem = "..." Expr | Expr

StructLitBody = "{" [ AnonField { ( "," | ";" ) AnonField } [ "," | ";" ] ] "}"

AnonStructLit = "{" [ AnonField { ( "," | ";" ) AnonField } [ "," | ";" ] ] "}"

AnonField = 
    "..." Expr
    | Expr [ "=" Expr ]

FuncLit = "func" [ "(" [ ParamList ] ")" ] FuncBodyTail

```

```ebnf
InterpStr = STR_START Expr { STR_RESUME Expr } STR_END

TripleInterp = TRIPLE_START Expr { STR_RESUME Expr } TRIPLE_END

I18nInterpStr = I18N_STR_START I18nPlaceholder { I18N_STR_RESUME I18nPlaceholder } I18N_STR_END

I18nTriple = I18N_TRIPLE_START I18nPlaceholder { I18N_STR_RESUME I18nPlaceholder } I18N_TRIPLE_END

I18nPlaceholder = Expr [ "," IDENT [ "," I18nThirdArg ] ]

```

```ebnf
Type = 
    IDENT [ "." IDENT [ "<" TypeList ">" ] | "<" TypeList ">" ]
    | "func"      [ "(" [ FuncTypeParamList ] ")" ] [ Type ]
    | "component" [ "(" [ FuncTypeParamList ] ")" ] [ Type ]
    | "..." Type
    | StructDecl
    | EnumDecl
    | UnitDecl

TypeList = Type { "," Type }

FuncTypeParamList = FuncTypeParam { "," FuncTypeParam }

FuncTypeParam = 
    IDENT [ "." IDENT [ "<" TypeList ">" ] | "<" TypeList ">" | Type ]
    | "func"      [ "(" [ FuncTypeParamList ] ")" ] [ Type ]
    | "component" [ "(" [ FuncTypeParamList ] ")" ] [ Type ]
    | "..." Type
    | StructDecl
    | EnumDecl
    | UnitDecl

```

<!-- END GENERATED: grammar-full -->
