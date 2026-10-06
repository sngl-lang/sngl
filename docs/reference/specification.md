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

- **The standard library** — components (`ui.text`, `ui.button`, `ui.vbox`,
  …), types (`color`, `ui.Style`, the event payload structs, the
  `ui.measurement` and `time.duration` units), and functions, all written in
  SNGL and distributed as source under `lib/<package>/*.sngl`, one directory
  per importable package. A conforming implementation parses and checks the
  standard library with the same front end it applies to user code; the
  library is not privileged by the grammar. This manual references standard
  library entities by example but does not define them. A few library
  declarations stand for a construct the compiler itself implements —
  `ui.window`, `output`, `effect`, `context`, `boundary`, and the tree
  families — and are recognized by a `#[builtin]` mark rather than by name.
  This manual specifies those constructs by the role they play.
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
ui.vbox {
    ui.text(value="shown")
    /- ui.text(value="hidden")
    /- ui.button(text="also gone", @click {})
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

The compiler synthesizes names beginning with two underscores (`__`), so user
code should not declare them; the prefixes `__async_` and `__hoist_` are
rejected outright.

### Keywords

The following words are reserved and may not be used as identifiers:

<!-- BEGIN GENERATED: keywords -->

`break` `component` `const` `continue` `else` `enum` `for` `func` `if` `import` `return` `struct` `unit` `var`

`true`, `false` and `null` are **predeclared identifiers**, not keywords, and may be shadowed by user declarations. Every other name a program uses without importing it — `int`, `string`, `list`, `output`, `effect`, `boundary` — is declared in `sngl:builtin`.

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
exponent syntax**: `1e9` lexes as the unit literal `1e` followed by the
integer `9`.

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
an element reference. Because the lexer does not guess by shape, no
identifier is "stolen" by the color rule: `#facade` and `#deadbeef` are valid
element-reference names, and `#0f0f0f` is a valid color.

### Unit literals

A unit literal is a numeric literal (integer or float form) immediately
followed, with no intervening whitespace, by a unit suffix consisting of one or
more ASCII letters: `5ms`, `3.5s`, `100px`, `1rem`. The suffix names a member
of a `unit` type (see [Unit types](#unit-types)); the numeric part is the
quantity in that suffix. Which unit a suffix belongs to is read from the
expected type at the literal's position, not from the suffix alone, so a unit
literal where nothing expects a unit type (`var d = 2s`) is an error.

### Element references

An element reference is the `#`-token used as a postfix declaration tag: it
names a visual node (`ui.button #submit(…)`) or a context declaration
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
MacroAttr = "#[" IDENT [ "." IDENT ] [ "(" [ Expr { "," Expr } [ "," ] ] ")" ] "]"
```

Brackets nest within the attribute, and the closing `]` suppresses automatic
semicolon insertion so the decorated construct may begin on the following line.

A macro is an ordinary declaration of a package, and none is ambient: the
attribute names it through the file's import of that package, qualified by the
import's alias like any other member — `#[tree.none]` after
`import tree "sngl:tree"`, `#[macro.foreign(…)]` after
`import macro "sngl:macro"`. A macro name that does not resolve is an error.

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
:                         binding parameter / argument (prefix)
@                         events and handlers
=>                        expression body, import redirect
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
`output` directive, type declarations (`struct`, `enum`, `unit`), `const` and
`var` declarations, `func` declarations, component declarations, and the root
nodes of the program — its `ui.window`s. Declaration order is not significant
(see [Declarations and scope](#declarations-and-scope)). An `import` may be
written only at the root of a file. What a file's root nodes may be, and what
makes a package a program, is described under [Programs](#programs).

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
`color` (ambient) and `time.date`, `time.time` and `time.datetime` are
string-representable types; the library also supplies the `ui.measurement` and
`time.duration` units used by `ui.Style` and `time.timer`. None of these are
core types — they are defined in SNGL under `lib/`.

### Composite and generic types

| Type        | Description                                                           |
|-------------|-----------------------------------------------------------------------|
| `list<T>`   | An ordered sequence of `T`. Literal: `[a, b, c]`.                     |
| `option<T>` | Either a `T` or absent (`null`).                                      |
| `map<K, V>` | A mapping from comparable keys `K` to values `V`. Literal: `{k = v}`. |
| `iter<T>`   | An opaque pull sequence yielding `T`; no methods and no fields.       |
| `ref<T>`    | A mutable reference to a `T` (see [References](#references)).         |

All five are declarations in `sngl:builtin`, marked as the construct they
stand for; `list<T>` and `map<K, V>` carry methods there (`length`, `map`,
`filter`, `keys`, `contains`, `get`, …). A `list<T>` converts implicitly to
`iter<T>`; a `map<K, V>` does not, so a map never reaches an `iter` position
with its map-ness erased. The integer sequences a counting loop walks are
`iter<int>` values produced by `sngl:seq` — `seq.count(n)`,
`seq.range(start, end)` and `seq.step(start, end, by)`.

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
*anonymously* in a type position — `var p struct { x int; y int }` gives `p`
an anonymous struct type — but a name may only be bound at the point of
declaration.

A type declared inside a body — a component, window or function body — is
scoped to that body: its name is visible there and nowhere else, and two
bodies each declaring `struct Local` declare two distinct types.

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

An enum is a finite set of named members. A member is a name, not a value; the
grammar admits `name = value` so that a declaration describing a host
enumeration can record the host constant a member corresponds to, which does
not change what the member means in SNGL. A member is referenced through the
enum type's namespace
(`Status.active`) or, where the expected type makes the enum unambiguous, by
its bare name. Enums are comparable and may be used as map keys. An enum may
also carry methods.

#### Unit types

A unit type declares a set of suffixes. A suffix written bare is a **base**; a
suffix written `name = literal` is **reduced**, defined as a multiple of an
already-declared suffix:

```
unit duration { ms, s = 1000ms, m = 60s, h = 60m }
unit measurement { px, em, rem = 16em, vw, vh, pct }
```

A value of a unit type is a magnitude per base. `duration` has one base, so a
value is one number counted in `ms`; `measurement` has five, so
`3px + 2em` is a single value holding 3 in `px` and 2 in `em`, and `2rem` is
32 in `em`. Unit literals (`5ms`, `2s`) have the unit type the position
expects (see [Unit literals](#unit-literals)).

- **Arithmetic** is per base: `+` and `-` combine two values of one unit type
  base by base, `*` and `/` by a number scale every base, and dividing two
  values of a single-base unit yields a dimensionless `float`.
- **Equality** compares every base.
- **Ordering** (`<`, `<=`, `>`, `>=`) is defined only for a single-base unit;
  two values of a multi-base unit may each be the larger on a different base,
  so comparing them is an error.
- **Members.** A multi-base unit has one `float` member per base — `m.px`,
  `m.em` — reading that base's magnitude. A single-base unit has no members.
- **Conversion.** `int(x)` and `float(x)` read the magnitude of a single-base
  unit, counted in its base; on a multi-base unit they are an error, since
  there is no single number to produce. `string(x)` and interpolation print
  the magnitude per base, joined by plus signs (`3px + 2em`), with an all-zero
  value printed in the first declared base (`0px`).

The integer literal `0` is assignable to any unit type; other bare numbers must
be scaled by a unit literal (`5 * 1s`).

### The dynamic type

`dyn` is the dynamic type and the type assigned to a binding that has no
annotation and no inferable initializer. Any value is assignable to `dyn`, a
`dyn` value is assignable to any type, and operations on a `dyn` operand bypass
static operand checking. Where the checker knows what a `dyn` binding was
initialized with, it holds later uses to that type.

### References

`ref<T>` is a mutable reference to a value of type `T`: what holds one goes on
seeing what the referenced cell becomes. `&x` makes one and `*r` reads through
it. References arise chiefly from reference loop variables (`for var &x = xs`),
which make writes to the loop variable flow back to the underlying list element
(see [The for statement](#the-for-statement)). A parameter declared `ref<T>`
also accepts a plain `T`, which is then an ordinary value — permitted only
where the callee does nothing with the parameter but read through it.

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
2. `A` or `B` is `dyn` (see [The dynamic type](#the-dynamic-type)).
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
  single-base unit;
- to `float` — from the same set as `int`;
- to `string` — from `int`, `float`, `bool`, an enum, a unit, or a
  string-representable type;
- to `bool` — from `bool` or `string`;
- to a string-representable type — from `string` or from itself.

A `dyn` operand may be converted to any primitive type (this is how `dyn` is
narrowed). Conversions whose operand is a struct, component, function, list,
option, or `null` are rejected, and so are `int` and `float` of a multi-base
unit (see [Unit types](#unit-types)).

### Narrowing an option

A comparison against `null` says what a value is in the branch where the
comparison holds. Within that branch an `option<T>` reads as a `T`:

```sngl
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
struct, a function, a method's receiver or name, or a component. A type
parameter may carry a default (`<T = struct {}>`), used when nothing binds it.
In a type position the arguments are written (`Box<int>`); at a call site they
are bound by inference rather than written:

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

A **component's** type parameters are bound from its arguments, and a slot's
content is an argument: the children written in a slot typed `component T` bind
`T` to the family they belong to (see [Trees and families](#trees-and-families)).

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

- the **built-in scope**, holding the ambient declarations of `sngl:builtin` —
  the predeclared types and identifiers;
- the **package scope**, holding the package's top-level declarations and, per
  file, that file's import aliases and the names its dot imports lift;
- a **component scope** for each component, holding its parameters, variables,
  nested functions, nested components, and nested types; a component nested in
  another body also sees that body's declarations;
- a **function scope** for each function or method body, holding its
  parameters. A `func` declared inside a function body is hoisted rather than
  closed over: it sees its sibling declarations and the scope its enclosing
  function was entered from, but not that function's parameters or locals;
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

Apart from `bool` and `dyn`, everything predeclared is an ordinary declaration
in `sngl:builtin`: the scalar and collection types (`int`, `float`, `string`,
`color`, `list`, `map`, `option`, `ref`, `iter`) with their methods; the constants
`true`, `false`, `null`, `PLATFORM` and `LANGUAGE`; the `platform` and
`language` target-identity types; the `error` type; and the compiler's node
constructs `output`, `effect`, `context` and `boundary`. That package is in
scope in every file implicitly and cannot be imported explicitly; it is the
only implicit import in the language.

`PLATFORM` and `LANGUAGE` name the target a build is producing. Their types are
`platform` and `language`, which have no literal: a value of one is the
build-target node a target's own package declares, read as a value, so a
platform is compared as
`PLATFORM == html.platform` (after `import html "sngl:platform/html"`), and
`PLATFORM == "html"` is a type error. The comparison folds at build time and
the branch not taken is removed. See
[Target-dependent code](#target-dependent-code).

Nothing here is a keyword. `true`, `false`, `null` and the rest resolve through
the scope chain like every other name, and a declaration of your own by one of
those names shadows it — the grammar reserves none of them.

Everything else the standard library provides is imported, and each directory
under `lib/` is one package, `lib/<path>` being `sngl:<path>`:

| Package             | Provides                                                                                           |
|---------------------|----------------------------------------------------------------------------------------------------|
| `sngl:ui`           | the portable components, `window`, the `node` family, `Style`, the style enums, `measurement`      |
| `sngl:ui/draw`      | `canvas` and the 2D `shape` family it hosts                                                        |
| `sngl:ui/markup`    | inline rich text: the `span` family, `richText`, and the block components a document is written in |
| `sngl:ui/markup/md` | the vocabulary of the `md:` scheme, such as the `order` mark                                       |
| `sngl:remote`       | `Value<T>`, the three-state box a data adapter returns                                             |
| `sngl:remote/http`  | `fetch`: an HTTP request whose answer is a `remote.Value`                                          |
| `sngl:build`        | the `language` and `platform` families an `output` directive holds                                 |
| `sngl:x/gen`        | what a target package says about what it generates                                                 |
| `sngl:x/gen/cache`  | what a generated file records it was generated from                                                |
| `sngl:time`         | `date`, `time`, `datetime`, `duration`, `timer`                                                    |
| `sngl:seq`          | `count`, `range`, `step`: the `iter<int>` sequences a counting loop walks                          |
| `sngl:tree`         | the `kind` and `none` marks, and the `one<T>` slot count                                           |
| `sngl:math`         | `pi`, `tau`                                                                                        |
| `sngl:async`        | `spawn`, `post`                                                                                    |
| `sngl:dialog`       | `Alert`, `File`                                                                                    |
| `sngl:test`         | `Test`                                                                                             |
| `sngl:i18n`         | the translation surface `$"…"` lowers to                                                           |
| `sngl:macro`        | the marks a package writes to describe its own declarations (`foreign`, …)                         |

A library package is imported under an alias, conventionally its last path
segment, and its members are written qualified:

```sngl
import ui "sngl:ui"
import draw "sngl:ui/draw"

ui.window(title="Shapes") {
    draw.canvas(width=100px, height=100px) {
        draw.circle(cx=50, cy=50, r=20)
    }
}
```

The dot form, `import . "sngl:ui"`, is also legal and flattens the package's
declarations into the file so they are written unqualified; the qualified form
is the recommended one. Imported packages register beneath the package scope,
so a top-level declaration named like a library entity takes precedence over it
within the package.

A library package may carry macros next to the declarations they apply to:
`import tree "sngl:tree"` brings the `#[tree.none]` mark beside the
`tree.one` count it qualifies slots with (see
[Trees and families](#trees-and-families)).

Packages under `sngl:internal/` are the compiler's own tier. They declare the
intrinsics a backend implements natively and the marks that identify built-in
constructs. Only library source may import one; a program's import of one is an
error.

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

### One name, one meaning

A name has one meaning over the scope its binding has. A top-level declaration
is package-wide, so two files of one package declaring the same name is an
error. An import binds into one file, so two imports claiming one alias, two
dot imports lifting one name, or a declaration taking a name one of the file's
import aliases binds are errors within that file. None of these has a
tiebreak, so resolving by source order would make meaning depend on ordering.

The single exception is shadowing, where only one of the two bindings is
written in this package: a declaration may shadow a name that a dot import
lifted, including a built-in. This is what lets a package define its own `text`
or `color` over the library's.

The rule covers every kind of declaration — types, components, free functions,
constants and variables alike — and the alias an import binds. Where a name is
genuinely taken, an alias resolves it: an import chooses its own alias, so
`import d "sngl:ui/draw"` reaches a package under a name nothing else in the
file claims.

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
    "const" ( ConstSpec
    | "(" { ConstSpec [ "," | ";" ] } ")"
    | FuncDecl
    | ComponentDecl )

ConstSpec = IdentList [ Type ] [ "=" Expr ]

IdentList = IDENT { "," IDENT }

VarDecl = 
    "var" VarSpec
    | "var" "(" { VarSpec [ "," | ";" ] } ")"

VarSpec = IdentList [ Type ] [ "=" Expr ] { VarHandler }

VarHandler = "@" IDENT [ "(" [ ParamList ] ")" ] StmtBlock

```

<!-- END GENERATED: grammar-constants-variables -->

A `const` declaration binds an immutable name. Its initializer must be a
*constant expression*: a literal (including a list, map or struct literal whose
elements are constant), an enum member, an operator applied to constant
operands, a primitive conversion, a reference to another constant, one of the
predeclared constants `true`, `false`, `null`, `PLATFORM`, `LANGUAGE`, or a
call to a [const function](#const-functions) with constant arguments. A constant initializer may not reference a variable, an ordinary
function, or a context. Constants may refer to one another in any source order.
Assigning to a constant is an error.

A `var` declaration binds a mutable name. The type may be given explicitly,
inferred from the initializer, or — if neither is present — defaults to `dyn`.
A variable initializer is evaluated **once**; it provides the initial value
only and does not establish a reactive dependency. A derived value that should
recompute when its inputs change must be a function, not a variable (see
[Reactivity](#reactivity)).

Both forms have a grouped variant — `const ( … )` and `var ( … )` — that lists
several specifications inside parentheses. A `var` declaration may carry
[handlers](#variable-handlers).

### The const prefix

`const` says *this is known at compile time*, and so takes no part in
reactivity. Before a name it declares a constant. Before another declaration it
says the same thing of what that declaration produces:

| Written                              | Means                                                                  |
|--------------------------------------|------------------------------------------------------------------------|
| `const func f(…)`                    | [f is pure](#const-functions): its value depends only on its arguments |
| `const component c(…)`               | [c's render depends only on its props](#const-components)              |
| `const name T` (a parameter or prop) | the argument is constant at every call site                            |
| `const name component(…)` (a slot)   | every population of the slot is a const render                         |

A **const parameter** — of a function or a component — requires every call
site's argument, and the default, to be a constant expression; inside the body
a read of the parameter is one, so it may flow into `const(…)`, a constant
initializer, or another const parameter. The build then folds the argument to a
literal and reports one it cannot fold. `const` is refused on a lambda's, a
handler's, or a slot population's parameter, since what supplies those is not a
call site; on a binding parameter (`:name`); and beside `#[construct]`, which it
already implies.

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

Param = { MacroAttr } ( ":" IDENT [ Type ] | "@" IDENT [ EventParams | Type ] | "const" IDENT [ Type ] | IDENT [ Type ] ) [ "=" Expr ]

EventParams = "(" [ FuncTypeParamList ] ")"

```

<!-- END GENERATED: grammar-functions -->

A function has one of two bodies, and no third form exists:

- a **block body** — `func name(params) [Type] { … }` — whose return type
  annotation is optional and required only when the function returns a value;
- an **expression body** — `func name(params) => expr` — whose result type is
  always inferred and which therefore may not carry a return type annotation.

The form `func name(params) -> Type` is not valid syntax, and `->` appears
nowhere in the grammar: a function type writes its result type directly after
the parameter list, `func(int) string`.

A block body may also be omitted. A function with no body is a *signature*,
and the checker requires its body to come from somewhere the declaration names:
a `#[macro.foreign]` mark describing a host function, or a per-target override
(see [Target-dependent code](#target-dependent-code)).

A parameter is a name with an optional type and an optional default value. The
parameter list itself is optional: `func now => …` declares a parameterless
function. A parameterless function is a *computed* value (see
[Reactivity](#reactivity)); where it is read as a value in a node property, an
interpolation, or a condition, it is called implicitly. Elsewhere it is a value
of function type and is called explicitly.

A **method** is a function whose name is qualified by a receiver type,
`func Type.method(…)`. Type parameters may appear after the receiver type
(receiver-level) and after the method name (method-level); see
[Generic type parameters](#generic-type-parameters). A method is dispatched on
the type of the receiver expression. A `func` written inside a `struct` or
`enum` body is a method of that type, and its body reads the receiver's fields
by bare name. A `func` written in a component body is a method of the
component.

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
call. It is *inferred*, and promises nothing to a caller: a change to the body
changes it.

### Const functions

`const func` *declares* a function pure, and only a declared one is a
compile-time value: a call to it with constant arguments is a constant
expression, which `const(…)`, a constant initializer and a const parameter
accept. The declaration is a contract the checker holds the body to. The body
may read its parameters, its own locals and constants, and call only other
`const func`s; its locals are the call's own and may be written. Reading or
writing a variable, reading a context, emitting an event, or calling a function
that is not const is an error naming what was reached.

A const function with no body — a host identifier, an intrinsic, an import — is
trusted, since nothing in the program says what the host does. The Go importer
reads `//sngl:pure` and the TypeScript importer its pure doc tag as `const`.
The compiler may run a const function at build time, so one wrongly declared
const runs during a build.

### Asynchrony

A function is *asynchronous* if it transitively calls an asynchronous primitive
(for example a host-language import that returns a promise). Asynchrony
propagates along the call graph to a fixed point. An asynchronous function that
takes parameters may not be used in a *reactive expression* (a property of a
visual node), because the reactive update mechanism keys a single cached result
per computed and cannot distinguish per-argument results; asynchronous calls
are permitted in event handlers and ordinary function bodies. Constructing a
function literal is not calling it: a body that only builds a closure over an
asynchronous call is not itself asynchronous.

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
  values of one single-base unit type, or to two strings.
- `+` is addition on numbers, concatenation when either operand is a `string`,
  and the per-base sum of two values of one unit type.
- `-`, `*`, `/`, `%` are the arithmetic operators; `int` divided by `int`
  truncates. A unit may be scaled by a number (`5px * 2`), and dividing two
  values of one single-base unit type yields a dimensionless `float` (see
  [Unit types](#unit-types)).

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
(`CondExpr`) excludes the bare-identifier struct literal, a leading
anonymous-struct or map literal, and the trailing statement block. A head
expression therefore never begins with `{`. Parenthesize a struct or map
literal if one is genuinely needed in a condition.

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
parameter, a loop variable, a field selection, or an index expression. The
value must be assignable to the target's type. The compound forms
`+= -= *= /= %=` apply the corresponding arithmetic operator to the current
value; `target++` and `target--` are shorthand for adding or subtracting `1`
and require a numeric target. Constants and contexts may not be assigned, and
neither may a visual node's property reached through its element reference
(`box.value = …`): a property says what the node shows for as long as it is
rendered, so change the state it reads instead.

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
IfNode = "if" CondExpr StmtBlock [ "else" ( IfNode | StmtBlock ) ]
```

The condition must be `bool` (or `dyn`); a parameterless `bool`-returning
computed is called implicitly. The `if` body and the optional `else` body are
each their own scope; `else if` chains without nesting braces.

A comparison of `PLATFORM` or `LANGUAGE` against a target identity folds at
build time, so the branch not taken is removed rather than tested at run time
(see [Target-dependent code](#target-dependent-code)).

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
  over an iterator, which has no index, `i` counts the elements yielded so far;
- `for var k, v = m` binds `k` and `v` to each key and value of a map; map
  iteration requires the two-variable form;
- `for xs` binds nothing, for a loop whose body never names the element.

A counting loop iterates a `sngl:seq` sequence:

<!-- SNGL-component -->

```sngl
import seq "sngl:seq"
import ui "sngl:ui"

for var i = seq.count(3) {
    ui.text(value="row {i}")
}
```

`seq.count(n)` yields `0 … n-1`, `seq.range(start, end)` yields
`start … end-1`, and `seq.step(start, end, by)` steps by `by`, stopping short
of `end`.

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

Both are restricted to **imperative bodies** — a function or a handler. A view
body repeats its body once per element of something, which is what
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
var items list<string> = []
for var item = items {
    ui.text(value=item)
} else {
    ui.text(value="Nothing yet")
}
```

For a loop over an iterable that is "the iterable was empty"; for a condition
loop it is "the condition was false the first time it was asked". A `break`
does not trigger the `else`, because a loop cannot break out of a body that
never ran.

`for { } else { }` is an error: a loop with no condition always runs its body,
so the block would be unreachable rather than an empty case.

In a view body the emptiness is asked of the iterable on every render, so the
head of a view loop with an `else` must be **measurable** — a list, a map, or a
`sngl:seq` range, whose emptiness is known without consuming an element; a
general `iter<T>` is an error there — and must be safe to evaluate twice.

### The emit statement

An **emit** fires an event declared on the enclosing component, invoking the
handler the caller attached. It is written as an ordinary call on the event's
name — `event(args)` — and carries no sigil; the checker resolves the name to
the declared event. See [Parameters](#parameters).

## Components

A **component** is a reusable, parameterized fragment of user interface.

<!-- BEGIN GENERATED: grammar-components -->

```ebnf
ComponentDecl = "component" IDENT [ "." IDENT ] [ TypeParamList ] [ TargetIndex ] [ "(" [ ParamList ] ")" ] [ Type ] [ StmtBlock ]

```

<!-- END GENERATED: grammar-components -->

```
component Name<TypeParams>(params) Family { body }
```

The parameter list defines the component's public interface — props, events
and slots alike. The type after it, the **return position**, names the tree
family the component *is* a member of (`ui.node` for a widget, `draw.shape`
for a shape); what it *hosts* is said by its slots (see
[Trees and families](#trees-and-families)). The body is a sequence of
statements, chiefly visual node instantiations.

The body is optional, as a function's is, and the two spellings say different
things. An empty body — `component Spacer() ui.node { }` — says the component
renders nothing. **No body at all** — `component Name(params) Family` — is a
*signature*: the render comes from somewhere the declaration names, and the
checker requires that to be true. The answer a program can write is a
per-target override (see [Target-dependent code](#target-dependent-code));
`#[intrinsic]` and `#[builtin]` are the library's own, and are accepted for
it. An override may not itself be bodyless, since an override *is* the body a
target renders.

The dotted name and the bracketed index, `component ui.text[html.platform](…)`,
are the override form: the name is the declaration being overridden, reached
through an import alias when it lives in another package, and the index is
the target the override is for.

A component written in another component's body is scoped to that body, and
its body sees the enclosing body's props, variables and functions; a write it
makes to one of them is a write to the enclosing component's own state.

### Const components

`const component` says the render depends only on the props. The render is
every expression the view body evaluates — a node's props and bindings, an `if`
or `for` head, a slot insertion's arguments, a context override's value — and
not a handler's or an effect's body. It may read props, constants, const
function results, loop variables, a slot's invocation arguments and contexts; a
variable read there, or a call to a function that is not const, is an error. A
variable only handlers touch is allowed, and so is a child with state of its
own, which is its own instance.

`const` on a declaration an override implements is part of its contract, so
every override of it is held to the rule. An override of a non-const
declaration may say `const` itself. A target package's components and overrides
must all be const, written or inherited: each is inlined into every program
that renders it.

A **const slot** holds every population written for it to the same rule, while
its invocation arguments need not be constant — a slot inserted once per list
item is pure over the item. A build directive's contents are populations of
const slots, which is what requires every value in one to be constant.

### Parameters

A component parameter is one of four kinds:

- a **regular parameter** — `name Type = default` — an input supplied by the
  caller, with an optional default, and constant at every call site when
  written `const name Type` (see [the const prefix](#the-const-prefix));
- a **binding parameter** — `:name Type` — a two-way bound property: the caller
  passes an lvalue with `:name = target`, and an assignment the component makes
  to `name` is written back to that lvalue, so parent and child stay in sync.
  Left unbound, it is a cell of the instance. Unlike a regular parameter it may
  also be written from outside, through the node's `#id` or a parameter holding
  that handle (`box.checked = true`): the write lands where the component's own
  does, on the bound lvalue or the instance's cell. Writing a regular parameter
  that way is an error, since what it shows is the expression the caller wrote;
- an **event parameter** — `@name(a A, b B)` — an outgoing event the
  component fires by calling its name (`name(x, y)`) and the caller handles
  with `@name(a, b) { … }`. The list is a func type's, names optional;
  `@name T` is the one-parameter case written without the parens, `@name()`
  passes nothing, and a bare `@name` carries one untyped value;
- a **slot** — `name component …` — a region of UI the caller supplies,
  described next.

A caller supplies a handler as an event argument, `@name { … }` or
`@name(a, b) { … }`. A handler binds the parameters by position and may leave
trailing ones unbound; binding more than the event passes, or annotating a
type other than its position's, is an error. A call of the event supplies
every parameter, or none, which forwards what the handler it is written in
received. There is no value form of an event reference: `@name` alone is not
an argument.

### Slots and children

A **slot** is a region of UI the caller supplies. It is an ordinary parameter
whose type is a **component type**, so a component's whole API — props, events
and slots — is one parameter list:

- `header component` — any number of nodes, of whatever family the component
  itself belongs to;
- `shapes component draw.shape` — any number of that family's members;
- `body component tree.one<ui.node>` — exactly one (bare `tree.one` is exactly
  one of whatever the slot already accepts);
- `badge component option<ui.node>` — zero or one;
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
component table(rows list<Row>, cell component(Row)) ui.node { ui.vbox { for var r = rows { cell(r) } } }
var rs list<Row> = []
-->

```sngl
table(rows=rs) {
    component cell(row) {
        ui.text(value=row.title)
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
import ui "sngl:ui"

component card(header component, content ...component) ui.node {
    ui.vbox {
        header {}
        content
    }
}

ui.window {
    card {
        component header {
            ui.text(value="Title")
        }
        ui.text(value="Body")
    }
}
```

A component declares at most one rest slot, and a component that declares none
accepts no children at all. `...` composes with a count bound — `content ...component tree.one` is "the bare children, of which exactly one" — because
the two say different things: `...` says which children arrive here, the
wrapper how many. Populating a rest slot by name *and* writing bare children
populates it twice.

A rest slot may be scoped — `children ...component(v T) ui.node` — and then
the population written by name, `component children(v) { … }`, is the only
form that reaches its arguments. Children written bare see none of them: a
spread has nowhere to write a name.

A slot's invocation list may itself declare a component entry —
`layout component(page Page, content component ui.node) ui.node` — which the
insertion populates by name, `layout(p) { component content { … } }`, and the
population inserts under its own positional name. The bare block written at
an insertion is that slot's fallback, and `...` may not appear in an
invocation list.

A `component` declaration in a body is read by **position**: at the root of a
component definition it is a nested declaration, and directly in a child node's
block it is a slot population. Anywhere else — inside an `if` or `for`, or in a
function body — is an error.

### Instantiation and visual nodes

A statement of the form `Name(args) { children }` instantiates a component or a
platform element as a **visual node**. Its arguments may be positional, named,
binding (`:prop = target`), and event (`@event { … }`) arguments; nested
statements form its children, which fill the component's rest slot, and a
`component` declaration directly in the block populates a named slot. Every
child must belong to the family the slot it lands in accepts. A node may be
labeled with an element reference, `Name #id(args)`, naming it for later use.

### Element references

Attaching `#id` to a node (`ui.button #id(…)`) names it within the component and
introduces `id` as a component-scoped binding: an opaque, immutable handle to
that node (it does not shadow an existing name). The node is reached either by
that bare name or, from a handle to its container, by ordinary field
selection — `c.id.value` reads the node's state and `c.id.click()` drives its
events (event invocation through a reference is an ordinary call on the event
name, `value.event(args)`). The `#` sigil itself appears only as the node- or
context-naming declaration tag; it is never a selection field nor written on
its own as an operand.

A handle is for reading. A node's properties may not be assigned through it
(see [Assignment](#assignment)), and a node may not read its own `#id` in its
own arguments (`ui.text #t(value = t.value)`), since the id names the instance
that argument list is building. A handle that is read may not be rendered more
than once — two copies of one body would share it, and neither read could say
which it meant.

## Reactivity

SNGL is reactive: when state changes, the parts of the UI that depend on it
update, with the dependencies determined entirely at compile time. How a target
applies an update — patching exactly the nodes that depend on the change, or
re-rendering from state for a host framework that reconciles — is the
target's choice; the observable result is the same.

### State and derivation

- A **reactive variable** is any non-constant `var` at package, component, or
  window scope. Its initializer runs once; thereafter its value changes only by
  assignment. A window owns no state of its own: a `var` in a window body
  belongs to what holds the window — the package, or the component that renders
  it.
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

A handler runs when its event fires. What a handler reads does not subscribe
it to anything; only node properties, conditions, loop heads and computed
functions are re-evaluated because of what they read.

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

### Effects

`effect` (from `sngl:builtin`) brackets a lifetime. It is a node placed in the
tree, so it lives exactly as long as the position it occupies: `@mount` runs
when it enters the tree and `@unmount` when it leaves. One inside an `if` lives
as long as that branch.

`on` makes the bracket keyed: while `on` holds the same value it is the same
effect, and a different value ends the old lifetime (`@unmount`) and begins a
new one (`@mount`). Each handler receives the key its own lifetime was begun
with. An effect declaring neither handler brackets nothing and is an error.

```sngl
import ui "sngl:ui"

ui.window {
    var room = "lobby"
    var log = ""

    effect(
        on=room,
        @mount(r) {
            log = "joined {r}"
        },
        @unmount(r) {
            log = "left {r}"
        },
    )
    ui.text(value=log)
    ui.button(text="Next room", @click {
        room = "hall"
    })
}
```

`effect` belongs to no tree family, so it may be placed in any of them — inside
a layout, a drawing, or a menu.

### Contexts

A context is a value supplied to a subtree rather than passed down as a
parameter. `context #name(default)` at the root of a file declares one, typed by
its default, and binds `name`; reading `name` anywhere yields the value supplied
by the nearest enclosing provider, or the default. Instantiating the context as
a node, `name(value) { … }`, provides `value` to everything under it:

```sngl
import ui "sngl:ui"

context #theme("light")

component badge() ui.node {
    ui.text(value="theme: {theme}")
}

ui.window {
    badge()
    theme("dark") {
        badge()
    }
}
```

A context may not be assigned, and may not be captured into a `var`, which
would freeze the value and miss later changes; read it where it is used.

### Timers

`time.timer` (from `sngl:time`) fires its `@tick` handler every `interval`
while `enabled` is true:

<!-- SNGL-component -->

```sngl
import time "sngl:time"
import ui "sngl:ui"

var running = true
var progress = 0.0
time.timer(interval=100ms, enabled=running, @tick {
    progress += 0.1
})
ui.text(value="{progress}")
```

Toggling `enabled` stops or restarts the timer; a timer is torn down when the
position it occupies leaves the tree. The interval is a `time.duration`. Like
`effect`, a timer belongs to no tree family.

### Error boundaries

`error.raise(message, kind)` raises an `error`, a struct carrying `message` and
`kind`. The error propagates to the nearest enclosing `boundary`, which catches
errors raised in the event handlers and expressions of its content. A boundary
has two halves: `@error(e) { … }` reports the error, and a `failed` slot
replaces the content from then on. Either is enough on its own, and one is
required — a boundary with neither would catch an error and do nothing with it.

```sngl
import ui "sngl:ui"

ui.window {
    var last = ""

    boundary(@error(e) {
        last = e.message
    }) {
        ui.button(text="Fail", @click {
            error.raise("could not save", "io")
        })
        component failed {
            ui.text(value="Something went wrong: {last}")
        }
    }
}
```

`boundary` is generic over the family of what it wraps: its content binds the
family, and `failed` must belong to the same one, since it stands where the
content stood. A window's `@error` is its outermost boundary, and it catches
whether or not the program handles it: a raise under a window whose call site
wrote no `@error` is caught and dropped. An error no boundary catches goes to
the platform's default handler.

## Trees and families

A user interface is a tree, and it is segmented into **families**: a container
accepts members of its own family and nothing else. Widgets are one family
(`ui.node`), the shapes a canvas draws are another (`draw.shape`), the windows
and build directive at the root of a file a third (`root`, declared in `sngl:builtin` and so in scope everywhere).

A family is declared by a component that is a member of `build.family`, the
family of families in `sngl:build`. It takes no parameters, has no body, holds
nothing and is never placed in a tree; it *is* the family, identified by its
declaration rather than its name, so two packages each declaring
`component item build.family` declare two families. A family may be declared
after the components that name it.

```sngl
import build "sngl:build"
import ui "sngl:ui"

component item build.family

component entry(label string) item {}

component menu(items ...component item) ui.node {}

ui.window {
    menu {
        entry(label="Open")
        entry(label="Save")
    }
}
```

The rules:

- **The return position says what a component is.** `component entry(…) item`
  is a member of `item`. Naming anything there that is not a family is an
  error.
- **A slot says what a component hosts.** A slot typed with a family accepts
  that family's members; a slot naming none accepts the family its component
  belongs to. A component that declares no slot hosts nothing.
- **A component's body is held to its own family.** What the body puts in the
  tree must belong to the family the return position names, so a
  `ui.node` component whose body renders a window is an error.
- **An omitted return position is inferred from the body**: a declaration
  whose body renders widgets is a widget. The evidence is what the body itself
  places, reached through any `if`, `for`, `boundary` or context provider
  around it. A body rendering members of two families, or of none, is an
  error. A library declaration always names its family.
- **`#[tree.none]` says a component belongs to no family.** It may be placed in
  any tree and may not render a member of any: `effect`, `context`, and
  `time.timer` are such components.
- **`if`, `for`, `boundary` and context providers are transparent**: they say
  when, how many, and under what conditions nodes appear, but put nothing in
  the tree themselves, so every membership rule looks through them.

A component generic over the family it wraps writes a type parameter where a
family goes — `component boundary<T>(…, content ...component T, failed component T) T` — and the children written in the slot bind `T`.

## Programs

A package is a **program** when its body renders something; a package whose
body renders nothing is a library, which may be type-checked but has nothing to
run.

The package body — the statements at the root of its files — is a slot that
accepts the `root` family, whose members are `ui.window`, a component whose
return position is `root`, and the `output` directive. A `ui.node` written at
the root of a file is therefore a family error, while an `if` or `for` there is
not a node and may hold windows. A window is an ordinary component of
`sngl:ui`, which each target implements. There is
no entry-point function or component: a component named `main` is an ordinary
component. A component whose return position is `root` renders windows, and
they reach the program when something at the root instantiates it.

```sngl
import ui "sngl:ui"

ui.window #home(title="Home") {
    var count = 0
    ui.button(text="Clicked {count} times", @click {
        count++
    })
}

ui.window #about(title="About") {
    ui.text(value="A two-window program.")
}

output {
    none {
        html()
    }
    go {
        bubbletea()
    }
}
```

A window's props are `title` and `favicon`; a window is a surface, not a
destination. The destinations a user moves between are the pages of a
`sngl:ui/nav` stack the window holds, each with an `href`. A page whose href
has `{name}` placeholders is handed their values as one struct value in its
`params` prop; the body that reads them is the population of the page's
`content` slot, `component content(p) { … }`, and every field of that struct is
one of the href's placeholders.

A window's body is its content; state it declares belongs to what holds the
window (see [State and derivation](#state-and-derivation)). A node's `#id` at
the root of a file is the package's: every root statement, function and
component body reaches it, a window's body hoists its ids as any node's does,
and a second declaration of the name in the package is an error. Where several
windows read one package-level variable, whether they share one value or each
get a copy is the target's: windows that are one process share it, windows
that are separate documents copy it.

### The output directive

`output` says what a package compiles to. It is written at the root of a file,
at most once per package, and its contents are an ordinary component tree:
`output` hosts language nodes and a language node hosts platform nodes, each
declared by that target's own package (`go`, `js`, `kotlin`, `none`; `html`,
`bubbletea`, `fyne`, `gtk4`, `android`, `none`). A target node needs no
import: inside the directive it is resolved against the registered targets, so
a misspelled one is an unresolved name. Build options are the props of the
node they belong to — `output(name = …)` for options every target shares,
`go(goVersion = …)` for a language's, `html(minify = …)` for a platform's — and
every value in the tree must be constant, since the directive is read before
the program runs. A build that names its targets on the command line ignores
the directive.

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
import "./widgets"          // namespace "widgets"; widgets.Button
import w "./widgets"        // explicit alias: w.Button
import "./ui/cards"         // namespace "cards" (last path segment)
import . "./widgets"        // dot import: Button, unqualified
```

A directory path is resolved relative to the importing file. Without an
explicit alias, the namespace name is the last segment of the path. A member is
accessed as `namespace.Member`. A dot import lifts the package's exported names
into the file instead; it is legal, but the qualified form is recommended, and
a package does not re-export the names it dot-imports. An import may be written
only at the root of a file, and binds only in that file.

The `=> "target"` form redirects a local import path to another target while
keeping the local name:

```
import "./cdn/ui" => "https://cdn.example.com/ui"
```

The redirect applies to the whole package: every import of that local path, in
any of the package's files, resolves to the target.

### Scheme imports and target routing

An import path may carry a URI scheme, which routes the import to a provider
rather than to a directory of `.sngl` files:

- `sngl:` — a standard library package (`import ui "sngl:ui"`), or a target's
  own package (`import html "sngl:platform/html"`,
  `import go "sngl:language/go"`), which is served by that target's plugin;
- `go:`, `js:`, `c:` — declarations describing a host-language package
  (`import strings "go:strings"`, `import slug "js:slugify"`), so generated
  code in that language can call into it;
- `git:`, `http:`, `https:` — SNGL source fetched from elsewhere;
- `md:` — a markdown document (`import doc "md:./guide.md"`) or directory
  (`import docs "md:./docs/"`) imported as SNGL source: a file is a package
  holding one `document` component, and a directory is a site of page
  components with a `site` component that renders each through a layout.

An import of a host-language package is the boundary at which the *language*
selected for compilation matters: see
[Platform and language plugins](#platform-and-language-plugins). Members of a
scheme import are namespaced exactly like directory imports.

## Target-dependent code

A program is written once for every target, and says where it differs in two
ways.

**A comparison against the target identity.** `PLATFORM` and `LANGUAGE` hold
the target a build produces. Each target's package declares its build-target
node, named for its tier — `html.platform`, `go.language` — and a comparison
against one folds at build time, removing the branch not taken:

<!-- SNGL-component -->

```sngl
import html "sngl:platform/html"
import ui "sngl:ui"

if PLATFORM == html.platform {
    ui.text(value="Running in a browser")
} else {
    ui.text(value="Running natively")
}
```

**A per-target override.** A function or component declaration indexed by a
target identity is that target's body for the declaration of the same name,
used in place of the unindexed one when building for that target:

```sngl
import html "sngl:platform/html"
import ui "sngl:ui"

func greeting() => "hello"

func greeting[html.platform]() => "hello, web"

component badge(label string) ui.node

component badge[html.platform](label) {
    html.span {
        ui.text(value=label)
    }
}

ui.window {
    badge(label=greeting())
}
```

An override's parameter list selects the props of the declaration it overrides
by name and carries no types of its own. The base may be a signature with no
body, as `badge` is here; an override satisfies it for every target, so a
target with no override renders nothing for it. An override may itself not be
bodyless. A target's raw elements, such as `html.span`, are reached through an
import of its package like any other name.

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
- **Capabilities gate lowering.** A target declares, in its own package, the
  constructs it emits natively (ternaries, lambdas, reactivity primitives, and
  so on); a capability it does not name is one it does not hold. The compiler
  runs capability-driven lowering passes between checking and code generation,
  so that a program is rewritten into the subset a target can express — for
  example replacing lambdas, ternaries, or reactive bindings where a target
  lacks them. These rewrites preserve the observable semantics defined in this
  manual; they do not change what a well-formed program means.
- **Target packages contribute names.** Each platform and language serves a
  package of declarations reachable through a `sngl:platform/…` or
  `sngl:language/…` import: its node in the `output` tree, which is also its
  identity, its raw elements (for example HTML tags), and its overrides of library
  components.
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
    "const" ( ConstSpec
    | "(" { ConstSpec [ "," | ";" ] } ")"
    | FuncDecl
    | ComponentDecl )

ConstSpec = IdentList [ Type ] [ "=" Expr ]

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

Param = { MacroAttr } ( ":" IDENT [ Type ] | "@" IDENT [ EventParams | Type ] | "const" IDENT [ Type ] | IDENT [ Type ] ) [ "=" Expr ]

EventParams = "(" [ FuncTypeParamList ] ")"

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
