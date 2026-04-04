---
title: "Effective SNGL"
order: 1
description: "Comprehensive guide to writing idiomatic SNGL"
---

## Introduction

SNGL is a declarative UI language that compiles to native code across multiple platforms -- HTML/JS, Go TUI (Bubbletea), Android (Compose), and desktop (Fyne). You write UI once in `.sngl` files; the compiler generates reactive, platform-specific output with no runtime overhead.

## File Structure

Every `.sngl` file is a flat list of top-level declarations. Order does not matter -- the compiler resolves references in two passes.

The `output` block declares which language and platform combinations the file targets:

<!-- SNGL-top
component main { text(value="") }
-->

```sngl
output {
    js { html }
    go { bubbletea(package="main") }
}
```

Imports pull in definitions from other `.sngl` files by directory path, or from host-language packages via scheme imports:

<!-- SNGL-top
-- shared/lib.sngl --
struct Widget { name string = "" }
-->

```sngl
import "shared"

component main {
    var w shared.Widget
    text(value=w.name)
}
```

Scheme imports pull in types from host-language packages:

```sngl
import "go://go/ast"

component main {
    var file ast.File
    text(value=string(file))
}
```

The top-level declarations available are: `import`, `output`, `struct`, `enum`, `unit`, `style`, `const`, `var`, `func`, `component`, `timer`, and `test`.

A complete minimal file needs only a `component main`:

```sngl
component main {
}
```

A realistic minimal file looks like this:

```sngl
output {
    js { html }
}

component main {
    text(value="Hello, SNGL")
}
```

Every package that produces an executable must have exactly one `component main`. Packages without `component main` are libraries -- they export structs, enums, and components for other files to import.

## Types

### Primitives

`bool`, `int`, `float`, `string` work as expected. `dyn` is the escape hatch for untyped values -- use it for host interop, not as a default.

### Special types

`color`, `date`, `time`, `dateTime`, `duration`, `measurement`, `url`, `email`, `uuid`, `regex` are built-in types with validation. Color has literal syntax (`#ff0000`). Duration and measurement have unit literal syntax (`5s`, `12px`).

### Regex

The `regex` type represents a compiled regular expression. Declare one with the `regex()` constructor or as a typed variable:

<!-- SNGL-component -->

```sngl
const pattern = regex("[a-z]+")
const emailPat regex = "^[^@]+@[^@]+$"
```

Invalid patterns are caught at compile time. Use `regex.matches` to check for a match and `regex.find` to extract the first match:

<!-- SNGL-component -->

```sngl
const pattern = regex("[a-z]+")
func _a() pattern.matches("hello")
func _b() pattern.find("abc 123")
```

### Collections

`list<T>` is an ordered, typed collection:

<!-- SNGL-component -->

```sngl
var names list<string> = []
var scores = [100, 95, 87]
```

### Optional types

`option<T>` wraps a value of type `T` that may be null. `null` is the default value for option types and is **only** valid for option types (not bare structs or primitives):

<!-- SNGL-component
struct Todo { text string = "" done bool = false }
-->

```sngl
var name option<string>
var count option<int> = 5
var todo option<Todo> = null
```

Check for presence with `== null` / `!= null`. Concrete values of `T` are implicitly assignable to `option<T>`.

### Structs

Structs are value types. They cannot be null. Fields have zero-value defaults when not specified:

```sngl
struct Todo {
    text string = ""
    done bool = false
}
```

<!-- SNGL-component
struct Todo { text string = "" done bool = false }
-->

```sngl
var todo Todo
var todo2 = Todo{text: "Buy eggs", done: false}
```

Accessing and mutating fields uses dot notation: `todo.text`, `todo.done = true`.

### Enums

Named enums declare a fixed set of string values:

```sngl
enum Status { active, inactive, pending }
```

Inline enums skip the top-level declaration when you need a one-off constraint:

<!-- SNGL-component -->

```sngl
var mode enum<light | dark> = "light"
```

At runtime, enum values are strings. The compiler validates assignments against declared variants.

### Unit types

Units declare named suffixes with optional conversion factors:

```sngl
unit duration(ms, s = 1000ms, m = 60s, h = 60m)
unit measurement(px, em, rem = 16em, vw, vh, pct)
```

Same-base additions normalize automatically (`1s + 500ms` becomes `1500ms`). Different-base additions produce compound values (`16px + 2em`). Scalar multiplication and division work as expected (`3px * 2` is `6px`).

### Type inference

Types are inferred from initializers. Explicit types are needed when the initializer is ambiguous or absent:

<!-- SNGL-component
struct Todo { text string = "" done bool = false }
enum Status { active, inactive, pending }
-->

```sngl
var count = 0
var name = "World"
var bg = #ff0000
var timeout duration = "5s"
var todos list<Todo> = []
var status Status = "active"
```

The rule: if the right side is an empty list, a zero-value struct, an enum string, or a unit literal (which could match multiple unit types), annotate the type. Otherwise, let inference do its job.

### Function types

Function types use `func(ParamTypes) -> ReturnType` syntax. Omit `-> ReturnType` for void:

<!-- SNGL-component -->

```sngl
var handler func() = null
var transform func(string) -> string = null
var callback func(string) -> int = null
```

## Type Conversions

### Explicit conversions

`string()`, `int()`, and `float()` convert between primitive types:

<!-- SNGL-component -->

```sngl
func _a() string(42)
func _b() string(3.14)
func _c() string(true)
func _d() int("42")
func _e() int(3.14)
func _f() float(42)
func _g() float("3.14")
```

`string()` accepts any type, including structs. `int()` and `float()` accept strings, numbers, and bools, but **not** structs -- `int(myStruct)` is a compile error.

### Implicit conversions

The following conversions happen automatically without an explicit call:

| From             | To           | When                                                  |
| ---------------- | ------------ | ----------------------------------------------------- |
| `int` constant   | `float`      | Constant expressions only: `var x float = 5`          |
| `float` constant | `int`        | Constant expressions only: `var x int = 3.0`          |
| `string`         | special type | Assignment: `var d date = "2024-01-15"`               |
| special type     | `string`     | Assignment: `var s string = myDate`                   |
| `string` literal | `enum`       | Assignment with validation: `var s Status = "active"` |
| `T`              | `option<T>`  | Assignment: `var x option<int> = 5`                   |
| `null`           | `option<T>`  | Default value: `var x option<int>`                    |
| `bool`           | `int`        | Arithmetic: `true + 0 == 1`, `false * 2 == 0`         |
| any              | `dyn`        | Always: `dyn` accepts any type                        |
| `dyn`            | any          | Always: `dyn` is assignable to any type               |

**Constant numeric coercion** only works for compile-time constants -- literals, `const` values, and pure expressions on constants. A `var` of type `int` is NOT assignable to `float` without an explicit `float()` call.

**Special type coercion** means string values flow freely to and from types like `color`, `date`, `url`, `email`, `uuid`, `regex`, etc. The compiler validates the format at compile time when the value is a literal.

### Disallowed conversions

These are compile errors:

| Conversion                | Error                                                     |
| ------------------------- | --------------------------------------------------------- |
| `null` → struct           | `null is not assignable to struct type`                   |
| struct → `int()`          | `cannot convert struct to int`                            |
| struct → `float()`        | `cannot convert struct to float`                          |
| wrong type → component param | `does not match`                                          |
| bad string → special type | format-specific error (e.g., invalid date, invalid email) |
| wrong variant → enum      | `is not a valid variant`                                  |

### String interpolation

String interpolation (`"{expr}"`) implicitly calls `string()` on the embedded expression, so any type can appear inside `{}`:

<!-- SNGL-component -->

```sngl
var count = 42
var active = true
func label() "Count: {count}, active: {active}"
```

## State

### var -- mutable reactive state

`var` declares mutable state that triggers UI updates when changed. Group related vars:

<!-- SNGL-component -->

```sngl
var (
    count = 0,
    name = "World",
    active = true,
)
```

### const -- compile-time values

`const` declares immutable values evaluated at compile time. Use them for configuration and magic numbers:

<!-- SNGL-component -->

```sngl
const (
    MAX_ITEMS = 100,
    DEFAULT_NAME = "unnamed",
    PI float = 3.14159,
)
const SINGLE = 42
```

Constants cannot reference `var` values.

### Derived state with zero-arg functions

Zero-arg functions serve as derived state -- they auto-update reactively and are read-only:

<!-- SNGL-component -->

```sngl
var count = 0
func doubled() count * 2
func label() "Count: {count}"
```

Zero-arg functions are auto-invoked when referenced without `()`: `text(value=label)` calls `label()` implicitly.

### extern -- host-provided values

`extern` marks a var as injected by the host platform at runtime:

<!-- SNGL-component -->

```sngl
var apiClient dyn extern
var formatDate func(string) -> string extern
```

Extern vars must have an explicit type since there is no initializer to infer from.

### trigger -- onChange callbacks

`trigger` attaches an onChange hook to a var. The platform generates the callback plumbing:

<!-- SNGL-component -->

```sngl
var todos list<Todo> trigger
var items list<Item> trigger("SaveItems")
```

Without an argument, the trigger name is generated from the var name. With a string argument, you control the callback name.

### Gotchas

Structs cannot be null (`var todo Todo = null` is a compile error; use `var todo Todo` or `var todo option<Todo> = null` for nullable). Empty lists need an explicit type (`var items list<string> = []`) because the compiler cannot infer `T` from `[]`.

## Functions

### Expression form

For single-expression pure functions, the body follows the parameter list directly:

```sngl
func add(a int, b int) a + b
func greet(name string) "Hello, {name}!"
```

### Block form

For multi-step logic, use a body with `return`:

```sngl
func clamp(val int, lo int, hi int) int {
    var clamped = val < lo ? lo : val
    var result = clamped > hi ? hi : clamped
    return result
}
```

### Void/action functions

Functions with no return type are void. They can mutate component state and are called from event handlers:

<!-- SNGL-component -->

```sngl
var count = 0
func reset() {
    count = 0
}
func increment(n int) {
    count += n
}
```

Void functions can only appear inside a `component` block. Pure functions (with a return type) can appear at the top level or inside components.

### Type methods

Attach a function to a type with a dotted name. The first parameter is the receiver:

```sngl
func int.double(x int) x * 2
func string.shout(s string) "{s}!"
```

Call with either syntax:

<!-- SNGL-component -->

```sngl
func _a() int.double(5)
func _b() 5.double()
func _c() "hello".shout()
```

Type methods work on primitives (`int`, `float`, `string`, `bool`, `color`, `list`) and user-defined structs.

### Generic functions

Type parameters go after the function name inside angle brackets. All stdlib list functions use generics:

<!-- SNGL-top
-- ... --
return l
-- ... --
return l
-- ... --
return -1
-->

```sngl
func list.push<T>(l list<T>, item T) list<T> { ... }
func list.reverse<T>(l list<T>) list<T> { ... }
func list.indexOf<T>(l list<T>, item T) { ... }
```

Type parameters are inferred at call sites -- you never write `<T>` explicitly when calling a generic function:

<!-- SNGL-component
struct Todo { text string = "" done bool = false }
-->

```sngl
var todos = [Todo{text: "a", done: true}, Todo{text: "b", done: false}]
func active() list.filter(todos, func(t) !t.done)
func labels() list.map(todos, func(t) t.text)
```

### Lambdas

Inline functions for filtering and mapping:

<!-- SNGL-component -->

```sngl
var todos = [Todo{text: "a", done: true}, Todo{text: "b", done: false}]
func active() list.filter(todos, func(t) !t.done)
func labels() list.map(todos, func(t) t.text)
```

Lambda parameter types are inferred from context.

### Gotchas

Pure functions cannot mutate state. Void functions cannot return values. Attempting either is a compile error.

## Components

### Declaration

A component groups params, state, functions, and visual nodes:

```sngl
component Counter(label = "", start = 0) {
    var count = start

    hbox {
        text(value="{label}: {count}")
        button(text="+", @click={ count += 1 })
    }
}
```

### component main

`component main` is the app entry point. Its body becomes the root of the rendered UI. Every executable file needs exactly one.

### Params

Params are the component's public API. They are declared in parentheses after the component name and accept values from parent components:

```sngl
component MyWidget(label = "default", count int, size enum<small | medium | large> = "medium") {
    text(value=label)
}
```

Without a default, params use the type's zero value. Parents pass params as named arguments:

<!-- SNGL-component
component Counter(label = "", start = 0) { text(value=label) }
-->

```sngl
Counter(label="Clicks", start=10)
Counter()
```

### Composition

Components can use other components:

<!-- SNGL-component
component Counter(label = "", start = 0) { text(value=label) }
-->

```sngl
Counter(label="Score")
```

Imported components are namespaced by their import path (e.g., `widgets.Counter(label="Imported")`).

### Stdlib components

The standard library provides layout (`vbox`, `hbox`, `stack`, `spacer`), input (`input`, `checkbox`, `toggle`, `select`, `textarea`, `radio`), display (`text`, `image`, `badge`, `progress`, `spinner`), navigation (`tabs`, `link`), and overlay (`modal`, `drawer`, `tooltip`, `popover`) components.

## Visual Nodes

### Syntax

Visual nodes are component instances rendered as UI elements:

<!-- SNGL-component -->

```sngl
text(value="hello", style={fontSize=24})
```

Both `()` and `{}` are optional:

<!-- SNGL-component -->

```sngl
spacer
text(value="Hello")
vbox { text(value="Hi") }
```

### Props

Props are typed values defined by the component's schema. The compiler validates prop names and types:

<!-- SNGL-component -->

```sngl
button(text="Submit", disabled=count <= 0)
input(value=name, placeholder="Enter name", type="email")
image(src="photo.png", alt="Profile", fit="cover")
```

### Events

Events use the `@` prefix and contain mutation statements:

<!-- SNGL-component -->

```sngl
var count = 0
button(text="+", @click={ count += 1 })
input(value=name, @input={ name = event.value })
```

Multi-statement events separate with semicolons:

<!-- SNGL-component -->

```sngl
var (newTodo = "", todos list<Todo> = [])
button(text="Add", @click={
    todos.push(Todo{text: newTodo, done: false})
    newTodo = ""
})
```

Available events depend on the component (check the stdlib definition). Use `event.value` to access the event payload in input handlers.

### Inline styles

Apply styles directly on any node:

<!-- SNGL-component -->

```sngl
vbox(style={padding=16, gap=12})
text(value="hello", style={color=#007700, fontSize=24, fontWeight="bold"})
```

### Conditional rendering

Use `if` blocks. `if` does not support `else` -- use two `if` blocks with opposite conditions (note: `else` is only available on `for` loops):

<!-- SNGL-component -->

```sngl
var active = true
if active {
    text(value="Active", style={color=#007700})
}
if !active {
    text(value="Inactive", style={color=#CC0000})
}
```

### Iteration

<!-- SNGL-component -->

```sngl
var items = ["a", "b", "c"]
for item in items {
    text(value=item)
}
for item, idx in items {
    text(value="{idx}: {item}", key=idx)
}
```

Use `key` for stable identity across re-renders when the list changes.

### for...else

The `else` block renders when the list is empty. It works with both `for item in list` and `for item, index in list`:

<!-- SNGL-component -->

```sngl
var items list<string> = []
for item in items {
    text(value=item)
} else {
    text(value="No items yet")
}
```

### Attribute nodes

Inside a children block, `@name(props)` attaches metadata:

<!-- SNGL-component -->

```sngl
text(value="hello") {
    @tooltip(text="A helpful tip")
}
```

### Element refs

Tag a node with `#id` to reference it in tests:

<!-- SNGL-component -->

```sngl
var count = 0
button #inc (text="+", @click={ count += 1 })
text #display (value="Count: {count}")
```

## Events and Mutations

### Assignment operators

`=`, `+=`, `-=`, `*=`, `/=`, `%=` work on vars and struct fields:

<!-- SNGL-component -->

```sngl
var count = 0
var label = ""
button(text="+5", @click={ count += 5 })
button(text="tag", @click={ label += " tagged" })
```

### Toggle

`!!` flips a boolean in place:

<!-- SNGL-component -->

```sngl
var active = true
button(text="Toggle", @click={ active!! })
```

### List mutations

Method syntax mutates in place. Function syntax returns a new list:

<!-- SNGL-component -->

```sngl
var items = [1, 2, 3]
button(text="Add", @click={ items.push(4) })
button(text="Remove first", @click={ items.remove(0) })
```

### Multiple statements

Separate statements with semicolons inside event handlers:

<!-- SNGL-component -->

```sngl
var (a = 0, b = 0)
button(text="go", @click={ a += 1; b += 2 })
```

### Emit

Fire a component event to notify the parent:

<!-- SNGL-component -->

```sngl
var data = "saved"
button(text="Save", @click={ @save(data) })
```

### Gotchas

Mutations are only allowed in event handlers and void functions. You cannot mutate state inside a pure function.

## Timers

```sngl
component main {
    var (progress float = 0, running = true)

    timer 100ms running {
        progress += 0.1
    }

    text(value=string(progress))
    button(text="Stop", @click={ running = false })
}

test main "timer increments" {
    assert(progress == 0)
    tick()
    assert(progress == 0.1)
}
```

The first argument is a duration literal. The second is a bool var controlling start/stop. The body executes on each tick while the bool is true. In tests, `tick()` advances one interval.

### Gotchas

Timer bodies can mutate state, like event handlers. The timer does not fire if the controlling bool is false, even if you call `tick()` in a test.

## Style

### Inline styles

All styles are applied via the `style` prop:

<!-- SNGL-component -->

```sngl
vbox(style={gap=12, padding=16})
text(value="bold", style={fontWeight="bold", color=#007700, fontSize=24})
button(text="go", style={margin=4, background=#ff0000, padding=8})
```

### Named styles

Declare reusable styles at the top level and apply them with `class`:

```sngl
style primary {
    color = #0000ff
    fontWeight = "bold"
    fontSize = 16
}

style secondary {
    color = #777777
    fontStyle = "italic"
}

component main {
    vbox {
        text(value="hello", class="primary")
        text(value="world", class="secondary")
    }
}
```

### Colors

Hex literals with optional alpha: `#ff0000`, `#fff`, `#00000080`. Use `color.rgb()` and `color.rgba()` for dynamic construction. Access channels with `.r`, `.g`, `.b`, `.a`.

### Units in styles

`12px`, `1.5em`, `16rem`. A bare number uses the platform default unit.

### Gotchas

Style property names are camelCase: `fontSize`, `fontWeight`, `borderRadius`. Not `font-size`, not `font_size`.

## Testing

### Test blocks

Tests target a specific component and get a fresh copy of its state:

```sngl
component counter {
    var count = 0

    button #inc (text="+", @click={ count += 1 })
    text #display (value="Count: {count}")
}

test counter "starts at zero" {
    assert(count == 0)
}

test counter "increments" {
    #inc.@click()
    assert(count == 1)
    assert(#display.value == "Count: 1")
}
```

### assert

`assert(condition)` fails the test with a diagnostic if the condition is false.

### Element refs

Tag nodes with `#id`, then access props and fire events in tests:

<!-- SNGL-component -->

```sngl
var count = 0
button #inc (text="+", @click={ count += 1 })
text #display (value="Count: {count}")
```

In a for loop, refs become indexed: `#item[0].value`, `#item[2].value`.

Access props: `#display.value`, `#display.class`. Fire events: `#inc.@click()`. Check existence: `#conditional != null`.

### State isolation

Each test block starts with fresh state. Mutations in one test do not leak to another.

### tick

`tick()` advances all active timers by one interval. See the Timers section for a complete example.

### Nested tests

Subtests inherit parent state but get their own snapshot. Changes in the subtest do not affect the outer scope:

```sngl
component app {
    var x = 0
    text(value="{x}")
}

test app "nesting" {
    x = 1
    test "inner" {
        x = 2
        assert(x == 2)
    }
    assert(x == 1)
}
```

### Disabling tests

Prefix with `/-` to comment out a test block: `/-test app "skipped" { assert(false) }`.

### Tests without descriptions

A test block can omit the description string: `test app { assert(count == 0) }`.

## Platform Targets

The output block (see File Structure) determines compilation targets. Override from the CLI: `sngl compile --lang go --platform bubbletea`. Other commands: `sngl run` (compile and execute), `sngl build` (distributable artifact), `sngl test` (run all test blocks).

Each platform maps stdlib components to native widgets. Core components (`vbox`, `hbox`, `text`, `button`, `input`, `checkbox`) work everywhere. Overlay components (`modal`, `drawer`, `popover`) have varying support.

## Naming Conventions

| Category         | Convention                  | Examples                   |
| ---------------- | --------------------------- | -------------------------- |
| Files            | `kebab-case.sngl`           | `todo-item.sngl`           |
| Components       | `PascalCase`                | `TodoItem`, `Counter`      |
| Variables/params | `camelCase`                 | `newTodo`, `isActive`      |
| Structs/enums    | `PascalCase`                | `Todo`, `Status`           |
| Style properties | `camelCase`                 | `fontSize`, `fontWeight`   |
| Events           | `@camelCase`                | `@click`, `@longPress`     |
| Constants        | `camelCase` or `UPPER_CASE` | `defaultName`, `MAX_ITEMS` |

## Common Patterns

### Todo list

```sngl
struct Todo {
    text string = ""
    done bool = false
}

component main {
    var (
        newTodo = "",
        todos list<Todo> = []
    )

    vbox(style={padding=16, gap=8}) {
        hbox(style={gap=8}) {
            input(value=newTodo, placeholder="New todo", @input={ newTodo = event.value })
            button(text="Add", disabled=newTodo == "", @click={
                todos.push(Todo{text: newTodo, done: false})
                newTodo = ""
            })
        }
        for todo, idx in todos {
            hbox(key=idx, style={gap=8}) {
                checkbox(checked=todo.done, @change={ todos[idx].done!! })
                text(value=todo.text)
                button(text="x", @click={ todos.remove(idx) })
            }
        }
    }
}
```

### Form with validation

```sngl
component main {
    var email = ""
    func valid() string.contains(email, "@") && string.length(email) > 3

    vbox(style={padding=16, gap=8}) {
        input(value=email, placeholder="Email", type="email", @input={ email = event.value })
        if !valid {
            text(value="Enter a valid email", style={color=#CC0000, fontSize=12})
        }
        button(text="Submit", disabled=!valid)
    }
}
```

### Modal open/close

```sngl
component main {
    var showModal = false

    vbox(style={padding=16}) {
        button(text="Open", @click={ showModal = true })
        modal(open=showModal, title="Settings", @close={ showModal = false }) {
            text(value="Content goes here")
            button(text="Close", @click={ showModal = false })
        }
    }
}
```

### Timer animation

```sngl
component main {
    var (progress float = 0.0, running = false)

    timer 50ms running {
        progress += 0.01
    }

    vbox(style={padding=16, gap=8}) {
        progress(value=progress, max=1.0, showValue=true)
        button(text=running ? "Pause" : "Start", @click={ running!! })
        button(text="Reset", @click={ progress = 0.0; running = false })
    }
}
```

### Filtered list

```sngl
struct Todo {
    text string = ""
    done bool = false
}

component main {
    var todos = [Todo{text: "Write docs", done: true}, Todo{text: "Fix bug", done: false}]
    func active() list.filter(todos, func(t) !t.done)
    func activeCount() list.length(active)

    vbox(style={padding=16, gap=8}) {
        text(value="{activeCount} remaining")
        for todo, idx in todos {
            checkbox(key=idx, checked=todo.done, label=todo.text, @change={ todos[idx].done!! })
        }
    }
}
```
