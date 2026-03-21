---
title: "Language Reference"
order: 2
description: "Complete SNGL language reference"
---

## File Structure

A `.sngl` file contains top-level declarations in any order. Every file must have exactly one `component main`.

```sngl
import "shared"

output {
    go bubbletea(package="main")
    js html
}

struct Todo { ... }
enum Status { active, inactive, pending }
unit duration(ms, s = 1000ms, m = 60s, h = 60m)
style heading { ... }

component Counter { ... }
component main { ... }
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

```sngl
"Hello, {name}!"
"Todo List ({todos.length()} items)"
"{user.name} is {user.age} years old"
```

Escape literal braces with `\{`.

### Semicolon Insertion

A semicolon is automatically inserted after a line's final token if that token is an identifier, a literal, or `)`, `]`, `}`. Opening `{` must appear on the same line as its construct (same convention as Go).

### Keywords

`import`, `output`, `struct`, `enum`, `unit`, `const`, `var`, `computed`, `style`, `styles`, `component`, `param`, `prop`, `event`, `children`, `if`, `for`, `in`, `extern`, `trigger`, `func`, `true`, `false`, `null`

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

When a `var`, `const`, or `computed` has a default value, the type is inferred:

```sngl
var count = 0                    // int
var name = "World"               // string
var bg = #ff0000                 // color
var timeout = 5s                 // duration
var user = User{name: "World"}   // User
var todos list<Todo> = []        // type required
```

## Top-Level Declarations

### import

Imports load all `.sngl` files from a directory, making their component, struct, and enum definitions available.

```sngl
import "shared"
```

### output

```sngl
output go bubbletea(package="main")

output {
    go bubbletea(package="main")
    js { html; node(ssr=true) }
}
```

### struct

```sngl
struct Todo {
    text string = ""
    done bool = false
}
```

### enum

Declares a named set of allowed string values. Enum values are identifiers:

```sngl
enum Status { active, inactive, pending }
enum Theme { light, dark }
```

Use a named enum as a type hint on `var`, `param`, or struct fields. The checker validates that literal values match one of the declared variants:

```sngl
var status Status = "active"     // OK
var status Status = "unknown"    // error: invalid enum value
```

Enums can also be declared inline as a type annotation without a top-level declaration:

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

```sngl
// Same-base addition normalizes automatically
1s + 500ms        // 1500ms
1h + 30m          // 5400000ms
1rem + 2em        // 18em
3px + 2px         // 5px

// Different bases produce compound values
16px + 2em        // {px: 16, em: 2}

// Scalar multiplication and division
2 * 3px           // 6px
6px / 2           // 3px

// Subtraction
5px - 2px         // 3px
2s - 500ms        // 1500ms
```

Adding values from the same base group normalizes to the base suffix. Adding values from different base groups produces a compound value with multiple components. Arithmetic between different unit types (e.g., `5px + 3s`) is an error.

#### Equality

Unit values compare by their normalized components. Values that normalize to the same base amount are equal:

```sngl
1s == 1000ms       // true
1rem == 16em       // true
16px + 2em == 2em + 16px  // true (order-independent)
```

#### Usage

Unit literals like `5s` and `12px` are resolved against unit declarations. Unit types can be used in type positions:

```sngl
var timeout duration = 5s
var spacing measurement = 12px
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

```sngl
param name type = default   // explicit type
param name = default        // type inferred
param name type             // no default, zero value
```

### prop (stdlib)

```sngl
prop value string
prop type string enum(text, password, number, email)
```

### event (stdlib)

```sngl
event click ClickEvent
event input InputEvent
```

### children (stdlib)

```sngl
children none
children one
children many
```

### Usage

```sngl
Counter(label="Clicks")
Counter(label="Score", start=10)
```

## Visual Nodes

### Syntax

```sngl
componentName(key=expr, key=expr) {
    children...
}
```

Both `()` and `{}` are optional:

```sngl
spacer
text(value="Hello")
vbox { text(value="Hi") }
```

### Events

Events use the `@` prefix and contain statement blocks:

```sngl
button(@click={ count += 1 })
input(@input={ name = event.value })
button(@click={
    todos.push(Todo{text: newTodo, done: false})
    newTodo = ""
})
```

### Inline Style

```sngl
vbox(style={gap=12, padding=16})
text(value="hello", style={color=#007700, fontSize=24})
```

### Attribute Nodes

Inside a children block, `@name(props)` defines attribute metadata:

```sngl
text(value="hello") {
    @tooltip(text="A helpful tip")
}
```

## Control Flow

### if

```sngl
if isAdult {
    text(value="(Adult)", style={color=#007700})
}
if !isAdult {
    text(value="(Minor)", style={color=#CC0000})
}
```

### for

```sngl
for item in todos {
    text(value=item.text)
}

for item, index in todos {
    checkbox(checked=item.done, key=index, label=item.text,
             @change={ todos[index].done!! })
}
```

## Built-in Functions

| Function | Signature | Description |
| --- | --- | --- |
| `string` | `string(value) -> string` | Convert to string |
| `int` | `int(value) -> int` | Convert to int |
| `float` | `float(value) -> float` | Convert to float |
| `embed` | `embed(path) -> string` | Compile-time file contents |

## List Methods

| Method | Signature | Description |
| --- | --- | --- |
| `.length` | `list.length() -> int` | Length of a list |
| `.push` | `list.push(value)` | Append to a list |
| `.remove` | `list.remove(index)` | Remove by index |
