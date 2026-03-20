# SNGL Language Specification

## Overview

SNGL is a purpose-built language for describing reactive UIs. Expressions, types, and control flow are first-class syntactic constructs.

File extension: `.sngl`.

## Design Goals

- **LL(1) grammar** — parseable with a single token of lookahead via recursive descent
- **First-class expressions** — Go-like expressions appear directly in the syntax, not wrapped in strings
- **Go-style semicolons** — newline-based statement termination with automatic semicolon insertion
- **Minimal syntax** — `node(params) { children }` is the core pattern
- **1:1 AST mapping** — every syntactic construct maps directly to an existing AST node
- **Imperative event handlers** — statement blocks with assignment and method calls

## Lexical Structure

### Comments

```
// line comment
/* block comment */
```

### Identifiers

```
IDENT = [a-zA-Z_][a-zA-Z0-9_]*
```

Hyphenated identifiers (`font-size`, `align-items`) are allowed in style property and prop name positions.

### Literals

| Literal | Examples                       |
| ------- | ------------------------------ |
| int     | `0`, `42`, `-1`                |
| float   | `1.0`, `-3.14`                 |
| string  | `"hello"`, `"it's a \"test\""` |
| bool    | `true`, `false`                |
| null    | `null`                         |
| color   | `#ff0000`, `#fff`, `#ff000080` |
| unit    | `5s`, `100ms`, `12px`, `1.5em` |

Number literals are untyped constants (like Go): `0` defaults to `int`, `1.0` defaults to `float`. All literals carry an intrinsic type — strings are `string`, booleans are `bool`, colors are `color`, etc. Unit literals (a number immediately followed by a suffix) carry a `unit:<suffix>` type hint that is resolved against `unit` declarations. This enables type inference: when a `var` has a default value, the type can be omitted and will be inferred from the expression.

### String Interpolation

Inside double-quoted strings, `{expr}` evaluates the expression and converts to string:

```
"Hello, {name}!"
"Todo List ({todos.length()} items)"
"{user.name} is {user.age} years old"
```

Escape literal braces with `\{`. Interpolated expressions follow the same syntax as ordinary SNGL expressions.

### Semicolon Insertion

A semicolon is automatically inserted after a line's final token if that token is:

- An identifier
- A literal (int, float, string, bool, null, color, unit)
- One of: `)`, `]`, `}`

This means `{` does NOT trigger insertion, enabling multi-line constructs:

```
var user = User{
    name: "World",
    age: 25,
}
```

Multi-line expressions work when lines end with operators or commas:

```
computed label = count > 0 ?
    "active" : "inactive"
```

Opening `{` must appear on the same line as its construct (same convention as Go).

### Keywords

Reserved words: `import`, `output`, `struct`, `enum`, `unit`, `const`, `var`, `computed`, `style`, `styles`, `component`, `param`, `prop`, `event`, `children`, `if`, `for`, `in`, `extern`, `trigger`, `func`, `true`, `false`, `null`.

## Expressions

Expressions use Go-like syntax parsed by the SNGL parser. The parser implements a full precedence-climbing expression parser. Expressions are stored as native SNGL AST nodes and type-checked by walking the AST — no external expression engine is used.

### Operators (by precedence, lowest to highest)

| Precedence | Operators            | Associativity | Description    |
| ---------- | -------------------- | ------------- | -------------- |
| 1          | `? :`                | right         | Ternary        |
| 2          | `\|\|`               | left          | Logical OR     |
| 3          | `&&`                 | left          | Logical AND    |
| 4          | `==`, `!=`           | left          | Equality       |
| 5          | `<`, `>`, `<=`, `>=` | left          | Comparison     |
| 6          | `+`, `-`             | left          | Addition       |
| 7          | `*`, `/`, `%`        | left          | Multiplication |
| 8          | `!`, `-` (unary)     | right         | Unary          |
| 9          | `.`, `[]`, `()`      | left          | Postfix        |

### Expression Forms

- **Arithmetic:** `count + 1`, `price * quantity`
- **Comparison:** `age >= 18`, `name != ""`
- **Logical:** `isAdult && isActive`, `!done`
- **Ternary:** `loggedIn ? "Logout" : "Login"`
- **Field access:** `user.name`, `todos[0].text`
- **Index:** `todos[0]`, `items[index]`
- **Method call:** `todos.push(item)` (in statement context)
- **Function call:** `string(count)`
- **Struct construction:** `Todo{text: "hello", done: false}`
- **List literal:** `[1, 2, 3]`
- **String interpolation:** `"Hello, {name}!"`

### Expression Boundaries

Where an expression ends depends on context:

| Context                               | Boundary                                     |
| ------------------------------------- | -------------------------------------------- |
| Inside `()` prop list                 | `,` or `)` at nesting depth 0                |
| After `if`                            | `{` at depth 0 (tracking `()` and `[]` only) |
| After `in` in `for`                   | `{` at depth 0 (tracking `()` and `[]` only) |
| After `=` in `const`/`var`/`computed` | Semicolon (inserted or explicit)             |
| After `=` in struct field             | Semicolon                                    |

In all contexts, `()`, `[]`, and string literals are tracked for nesting. In `if`/`for` contexts, `{}` is NOT tracked — this means struct construction cannot appear directly in `if`/`for` conditions (same restriction as Go).

### Built-in Functions

| Function | Signature                 | Description                               |
| -------- | ------------------------- | ----------------------------------------- |
| `string` | `string(value) -> string` | Convert to string                         |
| `int`    | `int(value) -> int`       | Convert to int                            |
| `float`  | `float(value) -> float`   | Convert to float                          |
| `embed`  | `embed(path) -> string`   | Compile-time file contents (const string) |

### List Methods

| Method    | Signature              | Description               |
| --------- | ---------------------- | ------------------------- |
| `.length` | `list.length() -> int` | Length of a list          |
| `.push`   | `list.push(value)`     | Append to a list          |
| `.remove` | `list.remove(index)`   | Remove from list by index |

## Statements

Statements appear only in event handler blocks (`@event={ ... }`). They are imperative — they mutate state directly. Functions and methods that return a value are pure (no side effects) and cannot appear as statements. Only mutating operations are valid statements: assignment, toggle, method calls on state, and emit.

### Statement Forms

| Operation       | Syntax                | Example             |
| --------------- | --------------------- | ------------------- |
| Assignment      | `target = value`      | `count = count + 1` |
| Compound assign | `target op= value`    | `count += 1`        |
| Toggle          | `target!!`            | `active!!`          |
| Method call     | `lvalue.method(args)` | `todos.push(item)`  |
| Emit            | `@name(payload?)`     | `@save(data)`       |

Compound assignment operators: `+=`, `-=`, `*=`, `/=`, `%=`.

### Statement Blocks

Multiple statements are separated by `;` or newline (semicolons are auto-inserted). Single statements can appear on one line:

```
button(@click={ count += 1 })
button(@click={
    todos.push(Todo{text: newTodo, done: false})
    newTodo = ""
})
```

## Type System

### Type Syntax

```
Type = IDENT                              // int, string, bool, User, etc.
     | IDENT "<" Type ("," Type)* ">"     // list<Todo>, map<string, int>
     | "func" "(" TypeList? ")" ("->" Type)?  // func(string) -> bool
     | "enum" "<" IDENT ("|" IDENT)* ">"  // enum<light | dark>
```

### Primitive Types

`bool`, `int`, `float`, `string`, `dyn`

### Special Types

`color`, `date`, `time`, `dateTime`, `duration`, `measurement`, `url`, `email`, `uuid`, `regex`, `base64`, `ipv4`, `ipv6`, `hostname`, `currency`, `country2`, `country3`, `countrySubdivision`, `decimal`, `idnEmail`, `idnHostname`, `irl`, `irlReference`, `urlReference`, `urlTemplate`

`duration` and `measurement` are unit types defined in the stdlib (see `unit` declarations). They have their own literal syntax (`5s`, `12px`) rather than string-wrapped values.

### Collection Types

`list<T>` — ordered collection of type T

### Function Types

`func(ParamType, ...) -> ReturnType` — callable. Omit `-> ReturnType` for void functions.

Examples: `func(string)`, `func(string, int) -> bool`

### Inline Enum Types

`enum<value1 | value2 | value3>` — one of a fixed set of values.

## Top-Level Declarations

### import

```
import "app.proto"
```

→ `ast.Import{Path: "app.proto"}`

### output

```
output go bubbletea(package="main")
output js html
```

→ `ast.Output{Lang: "go", Platform: "bubbletea", Options: {"package": "main"}}`

Options are optional. When present, they use `(key=value, ...)` syntax.

#### Grouped outputs

Multiple outputs can be grouped in a single `output` block:

```
output {
    go bubbletea(package="main")
    js { html; node }
}
```

A language with multiple platforms uses `{ platform; platform }` syntax. Each platform can have its own options: `js { html; node(ssr=true) }`.

### struct

```
struct Todo {
    text string = ""
    done bool = false
}
```

→ `ast.StructDef` with fields. Each field is `name type = default`.

### enum

```
enum Status { active, inactive, pending }
```

→ `ast.EnumDef{Name: "Status", Values: ["active", "inactive", "pending"]}`

Values are comma-separated identifiers.

### unit

Declares a unit type with named suffixes and optional conversion factors. A bare suffix (no `= expr`) is an independent base. A suffix with a unit literal factor like `rem = 16em` expresses a relationship to another suffix.

```
unit duration(ms, s = 1000ms, m = 60s, h = 60m)
unit measurement(px, em, rem = 16em, vw, vh, pct)
```

→ `ast.UnitDef{Name, Suffixes: []*UnitSuffix}`. Each suffix has an optional `Factor` expression (nil for bare suffixes).

Unit literals (e.g., `5s`, `12px`, `1.5em`) are number-suffix tokens recognized by the lexer. The suffix is resolved against unit declarations to determine the unit type. Compound literals like `2h30m` are not supported — use `2h + 30m` instead.

Unit types can be used in type positions just like any other type name:

```
var timeout duration = 5s
var spacing measurement = 12px
```

### style (named)

```
style heading {
    font-size = 24
    font-weight = "bold"
    color = #007700
}
```

→ `ast.StyleDecl{Name, Props}`. Properties are `name = value` per line.

### styles (schema definition)

Defines the available style properties and their types (used in stdlib).

```
styles {
    width dyn
    height dyn
    gap dyn
    align-items string enum(flex-start, flex-end, center, baseline, stretch)
    color color
    font-size float
}
```

→ `ast.StylePropDef` entries. The `enum(...)` suffix constrains allowed values.

## Reactive State

State is scoped to the `component` block that contains it (like Vue's component-scoped state). There is no top-level reactive state.

### Type Inference

When a `var`, `const`, or `computed` has a default value, the type annotation can be omitted — it is inferred from the expression. Types are required only when there is no default or when the type cannot be inferred (e.g., `null` alone does not determine a collection type).

```
var count = 0                    // inferred int
var name = "World"               // inferred string
var active = true                // inferred bool
var bg = #ff0000                 // inferred color
var timeout = 5s                 // inferred duration (unit type)
var spacing = 12px               // inferred measurement (unit type)
var user = User{name: "World"}   // inferred User (from struct literal)
var todos list<Todo> = []        // type required — [] doesn't determine element type
var mode enum<light | dark> = "light"  // type required — string doesn't determine enum
```

### const

Declares an immutable value. Appears inside `component` blocks. Must have a default value. The type is optional — inferred from the expression.

```
const maxItems = 100
const label string = "Hello"
```

→ `ast.Const{Name, Init: Expr, ...}`

#### Grouped const

```
const (
    maxItems = 100
    apiUrl = "https://api.example.com"
    template = embed("templates/main.html")
)
```

#### embed

`embed(path)` is a compile-time macro that reads a file relative to the source file and returns its contents as a `string`. The argument must be a constant string literal. It can appear anywhere a string expression is valid, but is most useful in `const` declarations.

```
const license = embed("LICENSE")
const styles = embed("base.css")
```

→ The compiler resolves `embed` at build time, replacing it with the file contents as a string literal.

### var

Declares mutable reactive state. The type is optional when a default value is present.

```
var count = 0
var name = "World"
```

→ `ast.Data{Name, Init: Expr, ...}`. The type sets `Init.TypeHint`.

#### Grouped var

Multiple var declarations can be grouped with parentheses (preferred):

```
var (
    count = 0
    name = "World"
    active = true
    user = User{name: "World", age: 25, loggedIn: false}
    todos list<Todo> = null
    mode enum<light | dark> = "light"
    bg = #ff0000
    timeout = 5s
)
```

Comma-separated on one line also works:

```
var (newTodo string, todos list<Todo> = [])
```

The default value (`= expr`) is optional when a type is present — fields without defaults get their zero value.

#### Modifiers

Modifiers follow the expression (or the type, if no default) on the same line:

```
var (
    apiClient dyn = null extern
    save func(string) = null extern
    format func(string) -> string = null extern
    todos list<Todo> = null trigger
    items list<Item> = null trigger("saveItems")
)
```

- `extern` — external field, not auto-initialized → `Data.Extern = true`
- `trigger` — generates onChange callback with auto-name → `Data.Trigger = "OnXxxChanged"`
- `trigger("name")` — explicit callback name → `Data.Trigger = "name"`

### computed

Declares derived read-only state. Always an expression. Appears inside `component` blocks alongside `var` and `const`. Type is always inferred.

```
computed greeting = "Hello, {name}!"
```

→ `ast.Computed{Name, Expr}`. No type annotation — inferred from expression.

#### Grouped computed

Multiple computed declarations can be grouped with parentheses (preferred):

```
computed (
    greeting = "Hello, {name}!"
    isAdult = user.age >= 18
    status = "Todo List ({todos.length()} items)"
)
```

## Component Declaration

### User-Defined Components

```
component Counter {
    param label = ""
    param start = 0
    var count = start

    hbox {
        text(value="{label}: {count}")
        button(text="+", @click={ count += 1 })
        button(text="-", @click={ count -= 1 }, disabled=count <= 0)
    }
}
```

→ `ast.Component{Name, Params, Body}`

Components own their reactive state. `var` and `computed` declarations inside a component are scoped to that component instance.

Every document must have exactly one `component main` — the root of the UI tree. It is a regular component recognized by name; no special syntax is needed.

#### param

```
param name type = default   // explicit type
param name = default        // type inferred from default
param name type             // no default — zero value of type
```

→ `ast.Param{Name, Default}`. Every type has a zero value (`0`, `""`, `false`, `null`, etc.), so params without defaults simply use the zero value when not provided.

### Stdlib Component Definitions

Used internally to define the standard component library:

```
component text {
    prop value string
    prop selectable bool
    event click ClickEvent
    children none
}

component input {
    prop value string
    prop placeholder string
    prop type string enum(text, password, number, email, url, tel, search)
    event input InputEvent
    event change ChangeEvent
    children none
}
```

#### prop

```
prop name type
prop name type enum(val1, val2, val3)
```

→ `ast.PropDecl{Name, TypeHint, Enum}`

#### event

```
event name PayloadType
```

→ `ast.EventDecl{Name, PayloadType}`

#### children

```
children none
children one
children many
```

→ `Component.ChildPolicy`

### Component Usage

```
Counter(label="Clicks")
Counter(label="Score", start=10)
```

Used like any other visual node.

## Visual Nodes

### Basic Syntax

```
component-name(key=expr, key=expr) {
    children...
}
```

Both `()` and `{}` are optional:

```
spacer                              // no props, no children
text(value="Hello")                 // props, no children
vbox { text(value="Hi") }          // no props, children
vbox(style={gap=12}) { ... }       // props and children
```

### Regular Properties

```
text(value="static string")
text(value=greeting)
text(value="Hello, {name}!")
button(disabled=count <= 0)
input(placeholder="Enter name", value=user.name)
```

Stored in `VisualNode.Props`.

### Special Properties

These identifiers are recognized by name and stored in dedicated AST fields:

| Property | AST Field          | Example             |
| -------- | ------------------ | ------------------- |
| `id`     | `VisualNode.ID`    | `id="main-title"`   |
| `key`    | `VisualNode.Key`   | `key=index`         |
| `class`  | `VisualNode.Class` | `class="container"` |
| `ref`    | `VisualNode.Ref`   | `ref="nameInput"`   |

### Events

Events use the `@` prefix inside `()` and contain statement blocks in `{ }`:

```
button(@click={ count += 1 })
input(@input={ name = event.value })
button(@click={
    todos.push(Todo{text: newTodo, done: false})
    newTodo = ""
})
checkbox(@change={ todos[index].done!! })
```

→ `VisualNode.Events["click"]`, etc. The `@` is stripped; the name after `@` is the event name.

Event values are statement blocks, not expressions.

### Style Prop (Inline)

The `style` prop accepts a style literal — `{key=value, ...}` pairs:

```
vbox(style={gap=12, padding=16})
text(value="hello", style={color=#007700, font-size=24})
button(text="Go", style={margin=4, font-weight="bold"})
```

→ `VisualNode.StyleAttrs`. The `{...}` is NOT an expression — it is a style literal parsed as key-value pairs.

### Attribute Nodes

Inside a children block, `@name(props)` defines attribute metadata:

```
text(value="hello") {
    @tooltip(text="A helpful tip")
}
```

→ `VisualNode.AttrNodes["tooltip"]`

Note the distinction: `@name` inside `()` is an event handler; `@name(...)` inside `{}` is an attribute node. The context (prop list vs children block) disambiguates.

### Children

Visual nodes nested inside `{}` are children:

```
vbox {
    text(value="First")
    text(value="Second")
    hbox {
        button(text="A")
        button(text="B")
    }
}
```

→ `VisualNode.Children`

## Control Flow

### if

Conditionally includes a visual node.

```
if isAdult {
    text(value="(Adult)", style={color=#007700})
}
if !isAdult {
    text(value="(Minor)", style={color=#CC0000})
}
```

The expression between `if` and `{` is parsed as a boolean expression. The block contains exactly **one** visual node (which may itself have children). The `if` attaches to that node.

→ Sets `VisualNode.If` on the contained node.

For multiple conditional nodes, use separate `if` blocks or wrap in a container:

```
if showDetails {
    vbox {
        text(value=name)
        text(value=email)
    }
}
```

### for

Repeats a visual node over a collection.

```
for item in todos {
    text(value=item.text)
}

for item, index in todos {
    checkbox(checked=item.done, key=index, label=item.text,
             @change={ todos[index].done!! })
}
```

Syntax: `for` variable (`,` indexvar)? `in` expression `{` visual-node `}`.

The expression after `in` is the iterable (must evaluate to a list). The block contains exactly **one** visual node.

→ Sets `VisualNode.For` on the contained node.

## LL(1) Grammar

```
Document       = Declaration*

Declaration    = "import" STRING
               | "output" IDENT IDENT ("(" KVList ")")?
               | "output" "{" OutputSpec* "}"
               | "struct" IDENT "{" StructField* "}"
               | "enum" IDENT "{" IDENT ("," IDENT)* "}"
               | "unit" IDENT "(" UnitSuffixDef ("," UnitSuffixDef)* ")"
               | "style" IDENT "{" StyleProp* "}"
               | "styles" "{" StyleDef* "}"
               | "component" IDENT "{" ComponentMember* "}"

UnitSuffixDef  = IDENT ("=" Expr)?

OutputSpec     = IDENT IDENT ("(" KVList ")")?
               | IDENT "{" PlatformList "}"

PlatformList   = IDENT ("(" KVList ")")? ("," IDENT ("(" KVList ")")?)*

ConstDecl      = "const" IDENT Type? "=" Expr
               | "const" "(" ConstField ("," ConstField)* ","? ")"

ConstField     = IDENT Type? "=" Expr

VarDecl        = "var" IDENT Type? "=" Expr VarMod*
               | "var" IDENT Type VarMod*
               | "var" "(" VarField ("," VarField)* ","? ")"

ComputedDecl   = "computed" IDENT "=" Expr
               | "computed" "(" ComputedField ("," ComputedField)* ","? ")"

VarField       = IDENT Type? "=" Expr VarMod*
               | IDENT Type VarMod*

ComputedField  = IDENT "=" Expr

StructField    = IDENT Type "=" Expr

VarMod         = "extern"
               | "trigger" ("(" STRING ")")?

StyleProp      = IDENT "=" Expr

StyleDef       = IDENT Type ("enum" "(" IDENT ("," IDENT)* ")")?

ComponentMember = "param" IDENT Type? "=" Expr
               | "param" IDENT Type "required"?
               | "prop" IDENT Type ("enum" "(" IDENT ("," IDENT)* ")")?
               | "event" IDENT IDENT
               | "children" ("none" | "one" | "many")
               | ConstDecl
               | VarDecl
               | ComputedDecl
               | NodeOrControl

NodeOrControl  = "if" Expr "{" VisualNode "}"
               | "for" IDENT ("," IDENT)? "in" Expr "{" VisualNode "}"
               | VisualNode

VisualNode     = IDENT ("(" PropList ")")? ("{" NodeBody* "}")?

NodeBody       = "@" IDENT "(" KVList ")"
               | NodeOrControl

PropList       = Prop ("," Prop)*

Prop           = "@" IDENT "=" "{" StmtList "}"
               | "style" "=" StyleLiteral
               | IDENT "=" Expr

StyleLiteral   = "{" (IDENT "=" Expr ("," IDENT "=" Expr)*)? "}"

KVList         = IDENT "=" Expr ("," IDENT "=" Expr)*

Type           = IDENT ("<" Type ("," Type)* ">")?
               | "func" "(" TypeList? ")" ("->" Type)?
               | "enum" "<" IDENT ("|" IDENT)* ">"

TypeList       = Type ("," Type)*
```

### Statement Grammar

```
StmtList       = Stmt (";" Stmt)* ";"?

Stmt           = AssignStmt | ToggleStmt | MethodCallStmt | EmitStmt

AssignStmt     = LValue "=" Expr
               | LValue "+=" Expr
               | LValue "-=" Expr
               | LValue "*=" Expr
               | LValue "/=" Expr
               | LValue "%=" Expr

ToggleStmt     = LValue "!!"

LValue         = IDENT ("." IDENT | "[" Expr "]")*

MethodCallStmt = LValue "." IDENT "(" ArgList? ")"

EmitStmt       = "@" IDENT "(" ArgList? ")"

ArgList        = Expr ("," Expr)*
```

### Expression Grammar

```
Expr           = Ternary
Ternary        = LogicalOr ("?" Expr ":" Expr)?
LogicalOr      = LogicalAnd ("||" LogicalAnd)*
LogicalAnd     = Equality ("&&" Equality)*
Equality       = Comparison (("==" | "!=") Comparison)*
Comparison     = Addition (("<" | ">" | "<=" | ">=") Addition)*
Addition       = Multiplication (("+" | "-") Multiplication)*
Multiplication = Unary (("*" | "/" | "%") Unary)*
Unary          = ("!" | "-") Unary | Postfix
Postfix        = Primary (Selector | Index | Call)*
Selector       = "." IDENT
Index          = "[" Expr "]"
Call           = "(" ArgList? ")"
Primary        = IDENT | Literal | "(" Expr ")" | StructLiteral | ListLiteral
StructLiteral  = IDENT "{" (IDENT ":" Expr ("," IDENT ":" Expr)* ","?)? "}"
ListLiteral    = "[" (Expr ("," Expr)* ","?)? "]"
```

### LL(1) Decision Points

| Position             | Lookahead token                        | Decision                            |
| -------------------- | -------------------------------------- | ----------------------------------- |
| Top-level            | keyword                                | Which declaration to parse          |
| After `output`       | IDENT vs `{`                           | Single output vs grouped block      |
| After `var`          | IDENT vs `(`                           | Single var vs grouped declaration   |
| Inside `{}` children | `if`/`for`/`@`/IDENT                   | Control, attr, or node              |
| Inside component     | `const`/`var`/`computed`/`param`/IDENT | Const, state, param, or visual node |
| After `var` IDENT    | `=` vs Type token                      | Inferred type vs explicit type      |
| Inside `()` props    | `@`/`style`/IDENT                      | Event, style literal, or prop       |
| After `@` in props   | `=` then `{`                           | Event → expect statement block      |
| After IDENT in type  | `<` or not                             | Generic type or plain type          |
| After `for` IDENT    | `,` or `in`                            | Index variable or iterable          |
| In statement         | IDENT then `!!`/`=`/`.`                | Toggle, assign, or method call      |
| In statement         | `@`                                    | Emit statement                      |

## Complete Examples

### Todo App

```
output {
    go bubbletea(package="main")
    js html
}

struct Todo {
    text string = ""
    done bool = false
}

component main {
    var (newTodo = "", todos list<Todo> = [])
    computed status = "Todo List ({todos.length()} items)"

    vbox(style={gap=12, padding=16}) {
        text(value=status, style={font-weight="bold", font-size=24})
        hbox(style={gap=8, align-items="center"}) {
            input(@input={ newTodo = event.value }, placeholder="Buy eggs",
                  style={flex-grow=1})
            button(@click={
                todos.push(Todo{text: newTodo, done: false})
                newTodo = ""
            }, text="Add")
        }
        vbox(style={gap=4}) {
            for item, index in todos {
                checkbox(checked=item.done, key=index, label=item.text,
                         @change={ todos[index].done!! })
            }
        }
        button(@click={ todos.remove(todos.length() - 1) }, text="Remove")
    }
}
```

### Reactive Bindings with Components

```
import "app.proto"

component Counter {
    param label = ""

    var count = 0

    hbox {
        text(value="{label}: {count}")
        button(text="+", @click={ count += 1 })
        button(text="-", @click={ count -= 1 }, disabled=count <= 0)
    }
}

component main {
    var user = User{name: "World", age: 25, loggedIn: false}

    computed (
        greeting = "Hello, {user.name}!"
        isAdult = user.age >= 18
    )

    vbox(style={padding=16, gap=12}) {
        text(value=greeting, style={font-size=24, font-weight="bold"})

        if isAdult {
            text(value="(Adult)", style={color=#007700})
        }
        if !isAdult {
            text(value="(Minor)", style={color=#CC0000})
        }

        input(value=user.name, placeholder="Enter name",
              @input={ user.name = event.value })

        Counter(label="Clicks")

        button(text=user.loggedIn ? "Logout" : "Login",
               @click={ user.loggedIn!! })
    }
}
```

### Special Types

```
component main {
    var (
        bg = #ff0000
        timeout = 5s
        birthday date = "2026-03-12"
        noon time = "12:00"
        event_time dateTime = "2026-03-12T10:00:00Z"
        site url = "https://example.com"
        contact email = "test@example.com"
        id uuid = "550e8400-e29b-41d4-a716-446655440000"
        pattern = /^[a-z]+$/
        addr4 ipv4 = "192.168.1.1"
        addr6 ipv6 = "::1"
        host hostname = "example.com"
        money currency = "USD"
        cc2 country2 = "US"
        cc3 country3 = "USA"
    )

    text(value=bg)
}
```

### External Vars and Triggers

```
component main {
    var (
        count = 0
        todos list<Todo> = null trigger
        items list<Item> = null trigger("saveItems")
        apiClient dyn = null extern
        saveTodo func(string) = null extern
        formatDate func(string) -> string = null extern
    )
    computed doubled = count * 2
}
```
