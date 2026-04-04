---
title: "Language Reference"
order: 2
description: "Complete SNGL language reference"
---

## File Structure

A `.sngl` file contains top-level declarations in any order. Every file must have exactly one `component main`.

```sngl
output {
    go { bubbletea(package="main") }
    js { html }
}

struct Todo {
    text string = ""
    done bool = false
}
enum Status { active, inactive, pending }
unit duration(ms, s = 1000ms, m = 60s, h = 60m)
style heading {
    fontSize = 24
}

component Counter {
    param label = ""
    text(value=label)
}
component main {
    Counter(label="Hello")
}
```

## Lexical Structure

### Comments

```sngl
// line comment
/* block comment */
```

### Identifiers

```
IDENT = [a-zA-Z_][a-zA-Z0-9_]*
```

Identifiers use camelCase (`fontSize`, `alignItems`).

### Literals

| Literal | Examples |
| --- | --- |
| int | `0`, `42`, `-1` |
| float | `1.0`, `-3.14` |
| string | `"hello"`, `"it's a \"test\""` |
| bool | `true`, `false` |
| null | `null` |
| color | `#ff0000`, `#fff`, `#ff000080` |
| unit | `5s`, `100ms`, `12px`, `1.5em` |

### String Interpolation

Inside double-quoted strings, `{expr}` evaluates the expression and converts to string:

<!-- SNGL-component -->
```sngl
func _a() "Hello, {name}!"
func _b() "Todo List ({todos.length()} items)"
func _c() "{user.name} is {user.age} years old"
```

Escape literal braces with `\{`.

### Semicolon Insertion

A semicolon is automatically inserted after a line's final token if that token is an identifier, a literal, or `)`, `]`, `}`. Opening `{` must appear on the same line as its construct (same convention as Go).

### Keywords

`import`, `output`, `struct`, `enum`, `unit`, `const`, `var`, `style`, `styles`, `component`, `param`, `prop`, `event`, `children`, `if`, `else`, `for`, `in`, `extern`, `trigger`, `func`, `true`, `false`, `null`

## Type System

### Primitive Types

`bool`, `int`, `float`, `string`, `dyn`

### Special Types

`color`, `date`, `time`, `dateTime`, `duration`, `measurement`, `url`, `email`, `uuid`, `regex`

`duration` and `measurement` are unit types with literal syntax (`5s`, `12px`).

### Collection Types

`list<T>` — ordered collection of type T.

### Function Types

`func(ParamType, ...) -> ReturnType` — callable. Omit `-> ReturnType` for void.

### Enum Types

Named enums declared with `enum Name { ... }` can be used as types. Inline enums use `enum<value1 | value2 | value3>` for one-off constrained string types without a top-level declaration.

### Type Inference

When a `var` or `const` has a default value, the type is inferred:

<!-- SNGL-top
struct User { name string = "" }
struct Todo { text string = "" done bool = false }
component main { text(value="") }
-->
```sngl
var count = 0
var name = "World"
var bg = #ff0000
var timeout duration = "5s"
var user = User{name: "World"}
var todos list<Todo> = []
```

## Top-Level Declarations

### import

Imports load all `.sngl` files from a directory, making their component, struct, and enum definitions available.

```
import "shared"
```

### output

<!-- SNGL-top
component main { text(value="") }
-->
```sngl
output {
    go { bubbletea(package="main") }
    js { html }
}
```

### struct

```sngl
struct Todo {
    text string = ""
    done bool = false
}
```

Structs are value types — they cannot be assigned `null`. Declare without an initializer for zero-value defaults:

<!-- SNGL-top
struct Todo { text string = "" done bool = false }
component main { text(value="") }
-->
```sngl
var todo Todo
var todo2 = Todo{text: "Buy eggs", done: false}
```

Assigning `null` to a struct is a compile error — use `option<Todo>` if you need nullable.

### enum

Declares a named set of allowed string values. Enum values are identifiers:

```sngl
enum Status { active, inactive, pending }
enum Theme { light, dark }
```

Use a named enum as a type hint on `var`, `param`, or struct fields. The checker validates that literal values match one of the declared variants:

<!-- SNGL-top
enum Status { active, inactive, pending }
component main { text(value="") }
-->
```sngl
var status Status = "active"
```

Assigning an undeclared variant (e.g., `"unknown"`) is a compile error.

Enums can also be declared inline as a type annotation without a top-level declaration:

<!-- SNGL-component -->
```sngl
var mode enum<light | dark> = "light"
param size enum<small | medium | large> = "medium"
```

Inline enums use `enum<value1 | value2 | ...>` syntax. At runtime, enum values are strings.

### unit

Declares a unit type with named suffixes and optional conversion factors:

```sngl
unit duration(ms, s = 1000ms, m = 60s, h = 60m)
unit measurement(px, em, rem = 16em, vw, vh, pct)
```

A bare suffix (e.g., `ms`, `px`) is an independent base. A suffix with a factor (e.g., `s = 1000ms`) converts to another suffix — meaning 1 `s` equals 1000 `ms`. Conversion factors chain: `m = 60s` means 1 `m` = 60 × 1000 `ms` = 60000 `ms`.

#### Base groups

Suffixes that share a conversion chain form a base group. Within a group, values normalize to the base suffix automatically. Suffixes without any conversion relationship are independent bases.

For `measurement(px, em, rem = 16em, vw, vh, pct)`:
- `px`, `em`, `vw`, `vh`, `pct` are each independent bases
- `rem` converts to `em` (1 `rem` = 16 `em`)

For `duration(ms, s = 1000ms, m = 60s, h = 60m)`:
- All suffixes chain to `ms`, so the entire unit is single-base

#### Unit arithmetic

Unit values support `+`, `-`, `*`, and `/`:

<!-- SNGL-component -->
```sngl
// Same-base addition normalizes automatically
func _a() 1s + 500ms        // 1500ms
func _b() 1h + 30m          // 5400000ms
func _c() 1rem + 2em        // 18em
func _d() 3px + 2px         // 5px

// Different bases produce compound values
func _e() 16px + 2em        // {px: 16, em: 2}

// Scalar multiplication and division
func _f() 2 * 3px           // 6px
func _g() 6px / 2           // 3px

// Subtraction
func _h() 5px - 2px         // 3px
func _i() 2s - 500ms        // 1500ms
```

Adding values from the same base group normalizes to the base suffix. Adding values from different base groups produces a compound value with multiple components. Arithmetic between different unit types (e.g., `5px + 3s`) is an error.

#### Equality

Unit values compare by their normalized components. Values that normalize to the same base amount are equal:

<!-- SNGL-component -->
```sngl
func _a() 1s == 1000ms       // true
func _b() 1rem == 16em       // true
func _c() 16px + 2em == 2em + 16px  // true (order-independent)
```

#### Usage

Unit literals like `5s` and `12px` are resolved against unit declarations. Unit types can be used in type positions:

<!-- SNGL-top
component main { text(value="") }
-->
```sngl
var timeout duration = "5s"
var spacing measurement = "12px"
```

The stdlib provides `duration` and `measurement` unit types. Custom unit types can be declared in any `.sngl` file.

### style

```sngl
style heading {
    fontSize = 24
    fontWeight = "bold"
    color = #007700
}
```

## Components

### Declaration

```sngl
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

### param

<!-- SNGL-component -->
```sngl
param name string = "default"   // explicit type
param label = "default"         // type inferred
param count int                 // no default, zero value
```

### prop (stdlib)

<!-- SNGL-component -->
```sngl
prop value string
```

### event (stdlib)

<!-- SNGL-component -->
```sngl
event click ClickEvent
event input InputEvent
```

### children (stdlib)

<!-- SNGL-component -->
```sngl
children none
```

### Usage

<!-- SNGL-component
component Counter { param label = "" param start = 0 text(value=label) }
-->
```sngl
Counter(label="Clicks")
Counter(label="Score", start=10)
```

## Visual Nodes

### Syntax

<!-- SNGL-component -->
```sngl
text(value="hello", style={fontSize=24}) {
    @tooltip(text="A tip")
}
```

Both `()` and `{}` are optional:

<!-- SNGL-component -->
```sngl
spacer
text(value="Hello")
vbox { text(value="Hi") }
```

### Events

Events use the `@` prefix and contain statement blocks:

<!-- SNGL-component -->
```sngl
button(@click={ count += 1 })
input(@input={ name = event.value })
button(@click={
    todos.push(Todo{text: newTodo, done: false})
    newTodo = ""
})
```

### Inline Style

<!-- SNGL-component -->
```sngl
vbox(style={gap=12, padding=16})
text(value="hello", style={color=#007700, fontSize=24})
```

### Attribute Nodes

Inside a children block, `@name(props)` defines attribute metadata:

<!-- SNGL-component -->
```sngl
text(value="hello") {
    @tooltip(text="A helpful tip")
}
```

## Control Flow

### if

<!-- SNGL-component -->
```sngl
if isAdult {
    text(value="(Adult)", style={color=#007700})
}
if !isAdult {
    text(value="(Minor)", style={color=#CC0000})
}
```

### for

<!-- SNGL-component -->
```sngl
for item in todos {
    text(value=item.text)
}

for item, index in todos {
    checkbox(checked=item.done, key=index, label=item.text,
             @change={ todos[index].done!! })
}
```

### for...else

The `else` block renders when the iterable list is empty:

<!-- SNGL-component -->
```sngl
for item in todos {
    text(value=item.text)
} else {
    text(value="No items yet")
}
```

## Functions

The `func` keyword declares a user-defined function. Functions may appear at the top level (global scope) or inside a `component` block.

### Pure Functions

Pure functions return a value and have no side effects. They can be declared at the top level or inside a component.

**Expression form** — a single expression after the parameter list:

```sngl
func add(a int, b int) a + b
func greet(name string) "Hello, {name}!"
```

**Block form** — a body with local variables and `return`:

```sngl
func clamp(val int, lo int, hi int) int {
    var clamped = val < lo ? lo : val
    var result = clamped > hi ? hi : clamped
    return result
}
```

### Component Methods

Functions inside a component can read component state. Zero-arg functions serve as derived state and are auto-invoked when referenced without `()`:

```sngl
component Counter {
    var count = 10

    func square(n int) n * n
    func label() "Count: {count}"

    text(value=label())
}
```

### Void/Action Functions

Functions with no return type are void. They may mutate component state and can only appear inside a `component` block:

```sngl
component Counter {
    var count = 0

    func reset() {
        count = 0
    }

    func increment(n int) {
        count += n
    }

    button(text="Reset", @click={ reset() })
    button(text="+5", @click={ increment(5) })
}
```

### Function Body Statements

Inside a block-form function body, the following are allowed:

| Statement | Example |
| --- | --- |
| Local variable | `var x = expr` |
| Assignment | `x = expr`, `x += expr` |
| Toggle | `active!!` |
| Method call | `todos.push(item)` |
| Function call | `reset()` |
| Emit | `@save(data)` |
| Return | `return expr` |

### Generic Functions

Functions can declare type parameters in angle brackets after the name:

```
func list.push<T>(l list<T>, item T) list<T>
func list.filter<T>(l list<T>, pred func(T) -> bool) list<T>
func list.map<T, U>(l list<T>, fn func(T) -> U) list<U>
```

Type parameters are inferred at call sites. Users never write `<T>` when calling a generic function -- the compiler deduces the type arguments from the values passed:

<!-- SNGL-component -->
```sngl
var names = ["alice", "bob"]
func upper() list.map(names, func(n) string.upper(n))
```

### Type Methods

Functions can be attached to a type using a dotted name. The first parameter is the receiver. This works for primitives (`int`, `string`, `float`, `bool`, `color`, `list`) and user-defined structs.

```sngl
func int.double(x int) x * 2
func string.shout(s string) s + "!"
func Todo.label(t Todo) t.done ? "[x] {t.text}" : "[ ] {t.text}"
```

Type methods support two call styles:

<!-- SNGL-component -->
```sngl
// Type-qualified — explicit receiver as first argument
func _a() int.double(5)            // 10
func _b() string.shout("hello")    // "hello!"

// Method syntax — receiver is implicit
var n = 5
func _c() n.double()               // 10
func _d() "hello".shout()          // "hello!"
```

## Built-in Functions

| Function | Signature | Description |
| --- | --- | --- |
| `string` | `string(value) -> string` | Convert any value to string |
| `int` | `int(value) -> int` | Convert to int (not valid on structs) |
| `float` | `float(value) -> float` | Convert to float (not valid on structs) |
| `embed` | `embed(path) -> string` | Compile-time file contents |
| `regex` | `regex(pattern) -> regex` | Compile-time validated regex constructor |
| `regex.test` | `regex.test(r, s) -> bool` | Test if regex matches string |
| `regex.match` | `regex.match(r, s) -> string` | First match of regex in string |

Struct values cannot be directly converted to numeric or boolean types. Use `string()` for a string representation, or access individual fields for typed conversions.

The `regex()` constructor validates the pattern at compile time -- invalid patterns produce a compile error:

<!-- SNGL-component -->
```sngl
var pattern = regex("[a-z]+")
var emailPat regex = "^[^@]+@[^@]+$"
```

## List Methods

All list functions use generics. Type parameters are inferred at call sites.

| Method | Signature | Description |
| --- | --- | --- |
| `.length` | `list.length<T>(l list<T>) -> int` | Length of a list |
| `.push` | `list.push<T>(l list<T>, item T) -> list<T>` | Append to a list |
| `.remove` | `list.remove<T>(l list<T>, index int) -> list<T>` | Remove by index |
| `.indexOf` | `list.indexOf<T>(l list<T>, item T) -> int` | Index of item, or -1 |
| `.join` | `list.join<T>(l list<T>, sep string) -> string` | Join elements with separator |
| `.reverse` | `list.reverse<T>(l list<T>) -> list<T>` | Reverse a list |
| `.slice` | `list.slice<T>(l list<T>, start int, end int) -> list<T>` | Subsequence by index range |
| `.contains` | `list.contains<T>(l list<T>, item T) -> bool` | Check if item is in list |
| `.filter` | `list.filter<T>(l list<T>, pred func(T) -> bool) -> list<T>` | Keep elements matching predicate |
| `.map` | `list.map<T, U>(l list<T>, fn func(T) -> U) -> list<U>` | Transform each element |
