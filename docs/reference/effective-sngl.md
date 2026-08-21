---
title: Effective SNGL
order: 60
description: Comprehensive guide to writing idiomatic SNGL
---

## Introduction

SNGL is a declarative UI language that compiles to native code across multiple platforms -- HTML/JS, Go TUI (Bubbletea), Android (Compose), and desktop (Fyne). You write UI once in `.sngl` files; the compiler generates reactive, platform-specific output with no runtime overhead.

## File Structure

Every `.sngl` file is a flat list of top-level declarations. Order does not matter -- a declaration may reference any other top-level name in its package regardless of where it appears.

The `output` block declares which language and platform combinations the file targets:

<!-- SNGL-top
import . "sngl://std"

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
import . "sngl://std"
import "shared"

component main {
    var w shared.Widget
    text(value=w.name)
}
```

Scheme imports pull in types from host-language packages:

```sngl
import . "sngl://std"
import "go://go/ast"

component main {
    var file ast.File
    text(value=string(file))
}
```

The top-level declarations available are: `import`, `output`, `struct`, `enum`, `unit`, `style`, `const`, `var`, `func`, components (PascalCase names), `timer`, and `test`.

A complete minimal file needs only a `component main`:

```sngl
import . "sngl://std"

component main {
}
```

A realistic minimal file looks like this:

```sngl
import . "sngl://std"

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

The standard library provides `color`, `date`, `time`, `datetime`, `duration`, and `measurement`. Each is *string-representable*: it converts to and from `string`, so a validated string literal is a valid value (`var d date = "2024-01-15"`). `color` has hex literal syntax (`#ff0000`); `duration` and `measurement` have unit-literal syntax (`5s`, `12px`).

### Collections

`list<T>` is an ordered, typed collection:

<!-- SNGL-component -->

```sngl
import . "sngl://std"
var names list<string> = []
var scores = [100, 95, 87]
```

### Optional types

`option<T>` wraps a value of type `T` that may be null. `null` is the default value for option types and is **only** valid for option types (not bare structs or primitives):

<!-- SNGL-component
struct Todo { text string = ""; done bool = false }
-->

```sngl
import . "sngl://std"
var name option<string>
var count option<int> = 5
var todo option<Todo> = null
```

Check for presence with `== null` / `!= null`. Concrete values of `T` are implicitly assignable to `option<T>`.

### Structs

Structs are value types. They cannot be null. Fields have zero-value defaults when not specified:

```sngl
import . "sngl://std"

struct Todo {
    text string = ""
    done bool = false
}
```

<!-- SNGL-component
struct Todo { text string = ""; done bool = false }
-->

```sngl
import . "sngl://std"
var todo Todo
var todo2 = Todo{text = "Buy eggs", done = false}
```

Accessing and mutating fields uses dot notation: `todo.text`, `todo.done = true`.

### Enums

Named enums declare a fixed set of string values:

```sngl
import . "sngl://std"

enum Status { active, inactive, pending }
```

Inline enums skip the top-level declaration when you need a one-off constraint:

<!-- SNGL-component -->

```sngl
import . "sngl://std"
var mode enum { light, dark } = light
```

A member is written bare where the expected type makes it unambiguous, and qualified as `Status.active` otherwise. A string is not an enum value: `= "light"` is a type error.

### Unit types

Units declare named suffixes with optional conversion factors:

```sngl
import . "sngl://std"

unit duration { ms, s = 1000ms, m = 60s, h = 60m }
unit measurement { px, em, rem = 16em, vw, vh, pct }
```

Same-base additions normalize automatically (`1s + 500ms` becomes `1500ms`). Different-base additions produce compound values (`16px + 2em`). Scalar multiplication and division work as expected (`3px * 2` is `6px`).

### Type inference

Types are inferred from initializers. Explicit types are needed when the initializer is ambiguous or absent:

<!-- SNGL-component
struct Todo { text string = ""; done bool = false }
enum Status { active, inactive, pending }
-->

```sngl
import . "sngl://std"
var count = 0
var name = "World"
var bg = #ff0000
var timeout = 5s
var todos list<Todo> = []
var status Status = active
```

The rule: if the right side is an empty list, a zero-value struct, or a bare enum member, annotate the type so the member resolves. Otherwise, let inference do its job.

### Function types

Function types use `func(ParamTypes) ReturnType` syntax. Omit the return type for void:

<!-- SNGL-component -->

```sngl
import . "sngl://std"
var handler func() = null
var transform func(string) string = null
var callback func(string) int = null
```

## Type Conversions

### Explicit conversions

`int(x)`, `float(x)`, `string(x)`, and `bool(x)` are the only cast forms. They accept **primitive operands only** — struct, func, component, list, and option values are rejected. The cast syntax never dispatches to user-defined methods; define a method and call it as `x.string()` instead.

<!-- SNGL-component -->

```sngl
import . "sngl://std"
func _a() => string(42)
func _b() => string(3.14)
func _c() => string(true)
func _d() => int("42")
func _e() => int(3.14)
func _f() => float(42)
func _g() => float("3.14")
```

Allow-lists per target:

| Target   | Accepts                                                                 |
|----------|-------------------------------------------------------------------------|
| `int`    | `int`, `float`, `string`, `bool`, `enum`, `unit`                        |
| `float`  | `int`, `float`, `string`, `bool`, `enum`, `unit`                        |
| `string` | any primitive / string-representable (`color`, `date`, …) / enum / unit |
| `bool`   | `bool`, `string`                                                        |

### Implicit conversions

Narrow list — most type changes are rejected and require an explicit cast.

| From              | To            | When                                                       |
|-------------------|---------------|------------------------------------------------------------|
| `int`             | `float`       | Anywhere a `float` is expected (auto-promotion)            |
| `int` literal `0` | any unit type | Typed zero: `var t duration = 0`, `delay(0)`               |
| `string`          | string-repr.  | Assignment: `var d date = "2024-01-15"` (validated)        |
| string-repr.      | `string`      | Assignment: `var s string = myDate`                        |
| `string` literal  | `enum`        | Assignment: `var s Status = "active"` (validated)          |
| `T`               | `option<T>`   | Assignment: `var x option<int> = 5`                        |
| `null`            | `option<T>`   | Assignment: `var x option<int> = null`                     |
| `null`            | `func(...)`   | Assignment; calling it yields the return type's zero value |
| `func() T`        | `T`           | Zero-arg function auto-called where `T` is expected        |
| any               | `dyn`         | `dyn` accepts any type                                     |
| `dyn`             | any           | Escape hatch; no runtime check                             |

`float` → `int` is **not** implicit — write `int(x)` to discard the fractional part. Non-zero `int` → unit is **not** implicit — use unit literals (`5s`) or multiply (`n * 1s`).

### Disallowed conversions

These are compile errors:

| Conversion                                           | Error                                            |
|------------------------------------------------------|--------------------------------------------------|
| `null` → struct                                      | `null is not assignable to struct type`          |
| struct → `int()` / `float()` / `string()` / `bool()` | `cannot convert` — define a method instead       |
| list/option/func/component → cast                    | `cannot convert` — define a method instead       |
| non-zero `int` → unit                                | `cannot initialize` / `cannot pass`              |
| wrong type → component param                         | `does not match`                                 |
| bad string literal → string-repr. type               | format-specific error (e.g. invalid date, color) |
| wrong variant → enum                                 | `is not a valid variant`                         |

### String interpolation

Inside `"{expr}"`, a primitive, string-representable type, enum, unit, `null`, list, option, or zero-arg function flows through automatically. For any other type — struct, component, etc. — the compiler looks up a `.string()` method on the value's type and calls it. When no such method exists the interpolation is a compile error:

<!-- SNGL-component -->

```sngl
import . "sngl://std"
struct Point {
    x int = 0
    y int = 0
}
func Point.string(p Point) => "({p.x},{p.y})"
var count = 42
var active = true
var origin = Point{x = 0, y = 0}
func label() => "count={count}, active={active}, origin={origin}"
```

Removing the `Point.string` method makes the `{origin}` interpolation a compile error, not a silent `{...}` render.

## State

### var -- mutable reactive state

`var` declares mutable state that triggers UI updates when changed. Group related vars:

<!-- SNGL-component -->

```sngl
import . "sngl://std"
var (
    count = 0
    name = "World"
    active = true
)
```

### const -- compile-time values

`const` declares immutable values evaluated at compile time. Use them for configuration and magic numbers:

<!-- SNGL-component -->

```sngl
import . "sngl://std"
const (
    MAX_ITEMS = 100
    DEFAULT_NAME = "unnamed"
    PI float = 3.14159
)
const SINGLE = 42
```

Constants cannot reference `var` values.

### Derived state with zero-arg functions

Zero-arg functions serve as derived state -- they auto-update reactively and are read-only:

<!-- SNGL-component -->

```sngl
import . "sngl://std"
var count = 0
func doubled() => count * 2
func label() => "Count: {count}"
```

Zero-arg functions are auto-invoked when referenced without `()`: `text(value=label)` calls `label()` implicitly.

### Data events

Data events let you react to variable changes with inline statement blocks:

<!-- SNGL-component
struct Todo { text string = ""; done bool = false }
func saveTodos(items list<Todo>) {}
func loadCount() {}
-->

```sngl
var todos list<Todo> @change {
    saveTodos(todos)
}
var count = 0 @init {
    loadCount()
}
```

Available events: `@change` (value changed), `@init` (component initialized), `@insert(item)` and `@delete(item)` (list-specific).

### Gotchas

Structs cannot be null (`var todo Todo = null` is a compile error; use `var todo Todo` or `var todo option<Todo> = null` for nullable). Empty lists need an explicit type (`var items list<string> = []`) because the compiler cannot infer `T` from `[]`.

## Functions

### Expression form

For single-expression pure functions, the body follows the parameter list directly:

```sngl
import . "sngl://std"

func add(a int, b int) => a + b
func greet(name string) => "Hello, {name}!"
```

### Block form

For multi-step logic, use a body with `return`:

```sngl
import . "sngl://std"

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
import . "sngl://std"
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
import . "sngl://std"

func int.double(x int) => x * 2
func string.shout(s string) => "{s}!"
```

Call with either syntax:

<!-- SNGL-component
func int.double(x int) => x * 2
func string.shout(s string) => "{s}!"
-->

```sngl
func _a() => int.double(5)
func _b() => 5.double()
func _c() => "hello".shout()
```

Type methods work on primitives (`int`, `float`, `string`, `bool`, `color`, `list`) and user-defined structs.

### Generic functions

Type parameters go after the function name inside angle brackets. All stdlib list functions use generics:

Generic collection functions are declared as methods on the receiver, so the
element type binds from the value they are called on:

<!-- SNGL-nocheck -->

```sngl
func list<T>.push(item T)
func list<T>.reverse() list<T>
func list<T>.indexOf(item T) int
func list<T>.filter(pred func(T) bool) list<T>
func list<T>.map<U>(fn func(T) U) list<U>
```

Type parameters are inferred at call sites -- you never write `<T>` explicitly
when calling a generic method. A method-level parameter such as `map`'s `<U>`
is inferred from the lambda's return type:

<!-- SNGL-component
struct Todo { text string = ""; done bool = false }
-->

```sngl
var todos = [Todo{text = "a", done = true}, Todo{text = "b", done = false}]
func active() => todos.filter(func(t Todo) => !t.done)
func labels() => todos.map(func(t Todo) => t.text)
```

### Lambdas

Inline functions for filtering and mapping. A lambda is written `func(x T) =>
expr`; the parameter type is explicit:

<!-- SNGL-component
struct Todo { text string = ""; done bool = false }
-->

```sngl
var todos = [Todo{text = "a", done = true}, Todo{text = "b", done = false}]
func active() => todos.filter(func(t Todo) => !t.done)
func labels() => todos.map(func(t Todo) => t.text)
```

### Gotchas

Pure functions cannot mutate state. Void functions cannot return values. Attempting either is a compile error.

## Components

### Declaration

A component groups params, state, functions, and visual nodes:

```sngl
import . "sngl://std"

component Counter(label = "", start = 0) {
    var count = start
    hbox {
        text(value="{label}: {count}")
        button(text="+", @click { count += 1 })
    }
}
```

### component main

`component main` is the app entry point. Its body becomes the root of the rendered UI. Every executable file needs exactly one.

### Params

Params are the component's public API. They are declared in parentheses after the component name and accept values from parent components:

```sngl
import . "sngl://std"

component MyWidget(label = "default", count int, size enum { small, medium, large } = medium) {
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
text(value="hello", style={fontSize = 24})
```

Both `()` and `{}` are optional:

<!-- SNGL-component -->

```sngl
spacer
text(value="Hello")
vbox { text(value="Hi") }
```

### Params

Params are typed values defined by the component's declaration. The compiler validates param names and types:

<!-- SNGL-component
var count = 0
var name = ""
-->

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
var name = ""
button(text="+", @click { count += 1 })
input(value=name, @input(e) { name = e.value })
```

Multi-statement events separate with semicolons:

<!-- SNGL-component
struct Todo { text string = ""; done bool = false }
-->

```sngl
var (
    newTodo = ""
    todos list<Todo> = []
)
button(text="Add", @click {
    todos.push(Todo{text = newTodo, done = false})
    newTodo = ""
})
```

Available events depend on the component (check the stdlib definition). A handler names its payload parameter to read it: `@input(e) { ... e.value }`. There is no ambient `event` identifier.

### Bidirectional Bindings

Input components like `input` and `checkbox` support **bidirectional bindings** using the `:` prefix. A bidirectional binding automatically propagates mutations from the child component to a parent variable without requiring a manually written event handler.

Instead of manually wiring an event:

<!-- SNGL-component -->

```sngl
var name = ""
input(value=name, @input(e) { name = e.value })
```

Use a bidirectional binding:

<!-- SNGL-component -->

```sngl
var name = ""
input(:value=name)
```

Bidirectional bindings work with custom components too. Declare a parameter with the `:` prefix:

```sngl
import . "sngl://std"

component Stepper(:count = 0) {
    button(text="+", @click { count += 1 })
    text(value=string(count))
}

component main {
    var steps = 0
    Stepper(:count=steps)
    text(value="Steps: {steps}")
}
// steps updates when child increments
```

When the child assigns `count += 1`, the parent variable `steps` automatically updates. The binding works transparently across component boundaries.

### Inline styles

Apply styles directly on any node:

<!-- SNGL-component -->

```sngl
vbox(style={padding = 16, gap = 12})
text(value="hello", style={color = #007700, fontSize = 24, fontWeight = "bold"})
```

### Conditional rendering

Use `if` blocks. `if` does not support `else` -- use two `if` blocks with opposite conditions (note: `else` is only available on `for` loops):

<!-- SNGL-component -->

```sngl
import . "sngl://std"
var active = true
if active {
    text(value="Active", style={color = #007700})
}
if !active {
    text(value="Inactive", style={color = #CC0000})
}
```

### Iteration

<!-- SNGL-component -->

```sngl
import . "sngl://std"
var items = ["a", "b", "c"]
for item = items {
    text(value=item)
}
for item, idx = items {
    text(value="{idx}: {item}", key=idx)
}
```

Use `key` for stable identity across re-renders when the list changes.

### for...else

The `else` block renders when the list is empty. It works with both `for item = list` and `for item, index = list`:

<!-- SNGL-component -->

```sngl
import . "sngl://std"
var items list<string> = []
for item = items {
    text(value=item)
} else {
    text(value="No items yet")
}
```

### Element refs

Tag a node with `#id` to reference it in tests:

<!-- SNGL-component -->

```sngl
import . "sngl://std"
var count = 0
button #inc(text="+", @click { count += 1 })
text #display(value="Count: {count}")
```

## Events and Mutations

### Assignment operators

`=`, `+=`, `-=`, `*=`, `/=`, `%=` work on vars and struct fields:

<!-- SNGL-component -->

```sngl
import . "sngl://std"
var count = 0
var label = ""
button(text="+5", @click { count += 5 })
button(text="tag", @click { label += " tagged" })
```

### Toggle

`!!` flips a boolean in place:

<!-- SNGL-component -->

```sngl
import . "sngl://std"
var active = true
button(text="Toggle", @click { active!! })
```

### List mutations

Method syntax mutates in place. Function syntax returns a new list:

<!-- SNGL-component -->

```sngl
import . "sngl://std"
var items = [1, 2, 3]
button(text="Add", @click { items.push(4) })
button(text="Remove first", @click { items.remove(0) })
```

### Multiple statements

Separate statements with semicolons inside event handlers:

<!-- SNGL-component -->

```sngl
import . "sngl://std"
var (
    a = 0
    b = 0
)
button(text="go", @click {
    a += 1
    b += 2
})
```

### Emit

Fire a component event to notify the parent. The event is declared on the
component with `@name`, and firing it is an ordinary call on that name:

```sngl
import . "sngl://std"

component SaveButton(label = "Save", @save) {
    button(text=label, @click { save() })
}

component main {
    var status = ""
    SaveButton(@save { status = "saved" })
    text(value=status)
}
```

### Gotchas

Mutations are only allowed in event handlers and void functions. You cannot mutate state inside a pure function.

## Timers

```sngl
import . "sngl://std"

component main {
    var (
        progress float = 0
        running = true
    )
    timer(interval=100ms, enabled=running, @tick {
        progress += 0.1
    })
    text(value=string(progress))
    button(text="Stop", @click { running = false })
}

func testTimerIncrements(t Test, c main) {
    t.assert(c.progress == 0)
    t.tick()
    t.assert(c.progress == 0.1)
}
```

The first argument is a duration literal. The second is a bool var controlling start/stop. The body executes on each tick while the bool is true. In tests, `tick()` advances one interval.

### Gotchas

Timer bodies can mutate state, like event handlers. The timer does not fire if the controlling bool is false, even if you call `tick()` in a test.

## Style

### Inline styles

All styles are applied via the `style` param:

<!-- SNGL-component -->

```sngl
vbox(style={gap = 12, padding = 16})
text(value="bold", style={fontWeight = "bold", color = #007700, fontSize = 24})
button(text="go", style={margin = 4, background = #ff0000, padding = 8})
```

### Named styles

There is no `style` declaration and no `class` prop. A reusable style is a
`Style` constant, applied through the same `style=` prop as an inline one:

```sngl
import . "sngl://std"

const primary Style = Style{color = #0000ff, fontWeight = "bold", fontSize = 16}
const secondary Style = Style{color = #777777, fontStyle = "italic"}

component main {
    vbox {
        text(value="hello", style=primary)
        text(value="world", style=secondary)
    }
}
```

### Colors

Hex literals accept three, four, six, or eight digits — `#fff`, `#fff8`, `#ff0000`, `#00000080` — where the short forms expand CSS-style by doubling each digit. Use `color.rgb()` and `color.rgba()` for dynamic construction. Access channels with `.r`, `.g`, `.b`, `.a`.

### Units in styles

`12px`, `1.5em`, `16rem`. A bare number uses the platform default unit.

### Gotchas

Style property names are camelCase: `fontSize`, `fontWeight`, `borderRadius`. Not `font-size`, not `font_size`.

## Testing

### Test blocks

Tests target a specific component and get a fresh copy of its state:

```sngl
import . "sngl://std"

component counter {
    var count = 0
    button #inc(text="+", @click { count += 1 })
    text #display(value="Count: {count}")
}

func testStartsAtZero(t Test, c counter) {
    t.assert(c.count == 0)
}

func testIncrements(t Test, c counter) {
    c.inc.click()
    t.assert(c.count == 1)
    t.assert(c.display.value == "Count: 1")
}
```

### assert

`assert(condition)` fails the test with a diagnostic if the condition is false.

### Element refs

Tag nodes with `#id`, then access props and fire events in tests:

<!-- SNGL-component -->

```sngl
import . "sngl://std"
var count = 0
button #inc(text="+", @click { count += 1 })
text #display(value="Count: {count}")
```

In a for loop, refs become indexed: `item[0].value`, `item[2].value`.

Access props: `display.value`, `display.class`. Fire events: `inc.click()`. Check existence: `conditional != null`.

### State isolation

Each test block starts with fresh state. Mutations in one test do not leak to another.

### tick

`tick()` advances all active timers by one interval. See the Timers section for a complete example.

### Nested tests

Subtests inherit parent state but get their own snapshot. Changes in the subtest do not affect the outer scope:

```sngl
import . "sngl://std"

component app {
    var x = 0
    text(value="{x}")
}

func testNesting(t Test, c app) {
    c.x = 1
    t.test("inner")
    t.assert(c.x == 1)
}
```

### Disabling tests

Prefix with `/-` to comment out a test block: `/-test app "skipped" { assert(false) }`.

### Tests without descriptions

A test block can omit the description string: `test app { assert(count == 0) }`.

## Platform Targets

The output block (see File Structure) determines compilation targets. Override from the CLI: `sngl generate --lang go --platform bubbletea`. Other commands: `sngl run` (compile and execute), `sngl build` (distributable artifact), `sngl test` (run all test blocks).

Each platform maps stdlib components to native widgets. Core components (`vbox`, `hbox`, `text`, `button`, `input`, `checkbox`) work everywhere. Overlay components (`modal`, `drawer`, `popover`) have varying support.

## Naming Conventions

| Category         | Convention                  | Examples                   |
|------------------|-----------------------------|----------------------------|
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
import . "sngl://std"

struct Todo {
    text string = ""
    done bool = false
}

component main {
    var (
        newTodo = ""
        todos list<Todo> = []
    )
    vbox(style={padding = 16, gap = 8}) {
        hbox(style={gap = 8}) {
            input(value=newTodo, placeholder="New todo", @input(e) { newTodo = e.value })
            button(text="Add", disabled=newTodo == "", @click {
                todos.push(Todo{text = newTodo, done = false})
                newTodo = ""
            })
        }
        for idx, todo = todos {
            hbox(key=idx, style={gap = 8}) {
                checkbox(checked=todo.done, @change { todos[idx].done!! })
                text(value=todo.text)
                button(text="x", @click { todos.remove(idx) })
            }
        }
    }
}
```

### Form with validation

```sngl
import . "sngl://std"

component main {
    var email = ""
    func valid() => string.contains(email, "@") && string.length(email) > 3
    vbox(style={padding = 16, gap = 8}) {
        input(value=email, placeholder="Email", type="email", @input(e) { email = e.value })
        if !valid {
            text(value="Enter a valid email", style={color = #CC0000, fontSize = 12})
        }
        button(text="Submit", disabled=!valid)
    }
}
```

### Modal open/close

```sngl
import . "sngl://std"

component main {
    var showModal = false
    vbox(style={padding = 16}) {
        button(text="Open", @click { showModal = true })
        modal(open=showModal, title="Settings", @close { showModal = false }) {
            text(value="Content goes here")
            button(text="Close", @click { showModal = false })
        }
    }
}
```

### Timer animation

```sngl
import . "sngl://std"

component main {
    var (
        progress float = 0.0
        running = false
    )
    timer(interval=50ms, enabled=running, @tick {
        progress += 0.01
    })
    vbox(style={padding = 16, gap = 8}) {
        progress(value=progress, max=1.0, showValue=true)
        button(text=running ? "Pause" : "Start", @click { running!! })
        button(text="Reset", @click {
            progress = 0.0
            running = false
        })
    }
}
```

### Filtered list

```sngl
import . "sngl://std"

struct Todo {
    text string = ""
    done bool = false
}

component main {
    var todos = [Todo{text = "Write docs", done = true}, Todo{text = "Fix bug", done = false}]
    func active() => todos.filter(func(t Todo) => !t.done)
    func activeCount() => active().length()
    vbox(style={padding = 16, gap = 8}) {
        text(value="{activeCount} remaining")
        for idx, todo = todos {
            checkbox(key=idx, checked=todo.done, label=todo.text, @change { todos[idx].done!! })
        }
    }
}
```
