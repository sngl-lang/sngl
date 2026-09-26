---
title: Effective SNGL
order: 60
description: Comprehensive guide to writing idiomatic SNGL
---

## Introduction

SNGL is a declarative UI language that compiles to native code across multiple platforms -- HTML/JS, Go TUI (Bubbletea), Android (Compose), and desktop (Fyne and GTK4). You write UI once in `.sngl` files; the compiler generates reactive, platform-specific output with no runtime overhead.

## File Structure

Every `.sngl` file is a flat list of top-level declarations. Order does not matter -- a declaration may reference any other top-level name in its package regardless of where it appears.

### A program is its windows

A program is the windows written at the root of its files. There is no entry-point component and no `main`: a `ui.window` at the root of a file is what the build opens.

```sngl
import ui "sngl:ui"

ui.window(title="Hello") {
    ui.text(value="Hello, SNGL")
}
```

A window's body is where the state that window uses lives, beside the tree that reads it:

```sngl
import ui "sngl:ui"

ui.window(title="Counter") {
    var count = 0
    ui.hbox(style={gap=8px}) {
        ui.button(text="+", @click { count += 1 })
        ui.text(value="Count: {count}")
    }
}
```

A package with no window is a library: it declares structs, enums, functions and components for other packages to import. A file of components on its own type-checks perfectly well; it just has nowhere to draw until something puts it in a window.

A program may declare several windows. Give each an `#id`, name the one the build opens at with `output(entry = ...)`, and give each an `href` -- on `html` every window is a page served at its route:

```sngl
import ui "sngl:ui"

ui.window #home(title="Home", href="/") {
    ui.text(value="Welcome")
}

ui.window #about(title="About", href="/about") {
    ui.text(value="About this app")
}

output(entry=home) {
    none { html }
}
```

### Imports

Standard library packages are imported under an alias and used qualified. The alias is the package's last path segment: `sngl:ui` is `ui`, `sngl:time` is `time`, `sngl:ui/draw` is `draw`.

```sngl
import ui "sngl:ui"
import seq "sngl:seq"

ui.window {
    for var i = seq.count(3) {
        ui.text(value="row {i}")
    }
}
```

Qualify every name. A dot import (`import . "sngl:ui"`) is legal, but it makes a reader guess where each bare name came from, and a name the file declares silently shadows the imported one. The one package you never import is `sngl:builtin`: `int`, `string`, `color`, `list`, `map`, `option`, `output`, `effect`, `context`, `boundary` and `error` are always in scope.

A directory import pulls in another package of your project by path, bound under its last segment:

<!-- SNGL-top
-- shared/lib.sngl --
struct Widget { name string = "" }
-->

```sngl
import ui "sngl:ui"
import "shared"

ui.window {
    var w shared.Widget
    ui.text(value=w.name)
}
```

Scheme imports pull in declarations from host-language packages. A `go:` import is callable on a Go target:

```sngl
import ui "sngl:ui"
import "go:strings"

output {
    go { bubbletea }
}

ui.window {
    var name = "gizmo"
    ui.text(value=strings.ToUpper(name))
}
```

### Targets

The `output` block declares which language and platform combinations the package targets. Each language node holds the platforms it drives; build options are props on the node that declares them:

<!-- SNGL-top
import ui "sngl:ui"

ui.window { ui.text(value="") }
-->

```sngl
output {
    none { html }
    go { bubbletea(package="main") }
    kotlin { android }
}
```

`none { html }` is a static site with inline JavaScript. The Go platforms are `bubbletea`, `fyne` and `gtk4`; `android` takes `kotlin` or `go`; `html` also takes `go`, which emits a Go HTTP server. A build that names targets on the command line (`--lang`, `--platform`) ignores the block.

### Folding a host function at build time

A function imported through a scheme can be marked so the compiler *runs* it
while building and freezes the result into the output as a literal. An
unmarked function is left as a call for the target to make at runtime. The
importer declares a marked function `const func`, which is also what lets a
call to it with constant arguments stand in a `const` initializer.

In Go the marker is a `//sngl:pure` line in the doc comment. In JavaScript and
TypeScript it is a tag in the declaration's doc comment, and six spellings are
accepted:

| Marker                                             | Origin           | Promises         |
|----------------------------------------------------|------------------|------------------|
| `@sngl-pure`                                       | SNGL             | deterministic    |
| `@__NO_SIDE_EFFECTS__` (or `#__NO_SIDE_EFFECTS__`) | Rollup, esbuild  | side-effect-free |
| `@nosideeffects`                                   | Closure Compiler | side-effect-free |
| `@__PURE__` (or `#__PURE__`)                       | Rollup, esbuild  | side-effect-free |

```ts
/** @sngl-pure */
export function slugify(s: string): string {
    return s.toLowerCase().split(" ").join("-");
}
```

A marker must stand alone on its own line of the comment, so a mention of one
in prose does not opt a function in. Matching is case-sensitive.

**Reach for `@sngl-pure` in code you own.** It says the thing folding actually
requires — that the function returns the same value every time — and it changes
nothing about how any bundler treats the declaration.

The other five were defined to mean *side-effect-free*, which is a weaker
promise: `Date.now()`, `Math.random()`, `process.env.TZ` and
`Intl.DateTimeFormat().resolvedOptions()` all have no side effects and none of
them is deterministic. A function marked that way is taken at its word, so if
the word is weaker than the library meant, the build freezes one moment's
answer into the output and nothing downstream can tell it from a correct one.
They are accepted because a package in `node_modules` cannot be annotated by
the person compiling it, and honoring only `@sngl-pure` would mean no
dependency could ever fold.

A marked function has to be runnable during the build: `go:` folding needs
the Go toolchain and `js:` folding needs `node`. When the tool is missing,
the value falls back to a runtime call on a target that can make one, and the
build fails on a target that cannot.

Any `node` will do. The compiler compiles the module and everything it imports
itself, so TypeScript is already gone by the time node sees it — a `.ts` with
an enum folds, and node's own type stripping never comes into it.

Where the function's declared return type says what the value is, that type is
what makes it well-typed. Where it does not — a Go `[]any`, a TypeScript
`any[]` — a `go:` value still arrives typed, because the Go runtime can name
the type it is: `reflect` gives the declaring package and the name in it, which
is exactly what the `go:` importer keyed its declarations by. It writes that
as `import("go:path/to/pkg").Type{…}`, which is not source you can write —
only the reader the compiler points at a folded value accepts an import in
expression position. A `js:` value cannot. A JavaScript value carries no link to the module that declared its
type, a class name alone does not identify one, and a TypeScript interface has
no runtime constructor at all — so an untyped `js:` element stays `dyn`, with
the field names the source language used.

A folded value crosses as SNGL source, and a class can choose its own form with
a `[Symbol.for("sngl.marshal")]()` method returning that source; for a class
you did not write and so cannot add a method to, `globalThis.__SNGL_CONSTEVAL__.register(Ctor, fn)`
registers the same thing from outside.

### Top-level declarations

The declarations a file holds are `import`, `output`, `struct`, `enum`, `unit`, `const`, `var`, `func`, `component`, and the windows the program renders. There is no `style`, `timer` or `test` declaration: a reusable style is a `ui.Style` constant, a timer is a `time.timer` node placed in a body, and a test is an ordinary function taking a `test.Test` receiver.

## Types

### Primitives

`bool`, `int`, `float`, `string` work as expected. `dyn` is the escape hatch for untyped values -- use it for host interop, not as a default.

### Special types

`color` is built in and has hex literal syntax (`#ff0000`). `sngl:time` provides `time.date`, `time.time` and `time.datetime`, which are *string-representable*: each converts to and from `string`, so a validated string literal is a valid value (`var d time.date = "2024-01-15"`).

`time.duration` and `ui.measurement` are unit types, not string-representable ones: their values are written as unit literals (`5s`, `12px`), and a string is not a duration.

### Collections

`list<T>` is an ordered, typed collection, and `map<K, V>` a keyed one:

<!-- SNGL-component -->

```sngl
var names list<string> = []
var scores = [100, 95, 87]
var ages map<string, int> = {"ada"=36, "alan"=41}
```

### Optional types

`option<T>` wraps a value of type `T` that may be null. `null` is the default value for option types and is **only** valid for option types (not bare structs or primitives):

<!-- SNGL-component
struct Todo { text string = ""; done bool = false }
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
struct Todo { text string = ""; done bool = false }
-->

```sngl
var todo Todo
var todo2 = Todo{text="Buy eggs", done=false}
```

Accessing and mutating fields uses dot notation: `todo.text`, `todo.done = true`.

### Enums

Named enums declare a fixed set of members:

```sngl
enum Status { active, inactive, pending }
```

Inline enums skip the top-level declaration when you need a one-off constraint:

<!-- SNGL-component -->

```sngl
var mode enum { light, dark } = light
```

A member is written bare where the expected type makes it unambiguous, and qualified as `Status.active` otherwise. A string is not an enum value: `= "light"` is a type error.

### Unit types

Units declare named suffixes with optional conversion factors. The standard library declares the two you will use most, `time.duration` and `ui.measurement`:

<!-- SNGL-nocheck -->

```sngl
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
var count = 0
var name = "World"
var bg = #ff0000
var timeout time.duration = 5s
var todos list<Todo> = []
var status Status = active
```

The rule: if the right side is an empty list, a zero-value struct, a bare enum member, or a unit literal, annotate the type so it resolves. A suffix belongs to the unit that declares it, and the annotation is what says which unit `5s` means. Otherwise, let inference do its job.

### Function types

Function types use `func(ParamTypes) ReturnType` syntax. Omit the return type for void:

<!-- SNGL-component -->

```sngl
var handler func() = null
var transform func(string) string = null
var callback func(string) int = null
```

## Type Conversions

### Explicit conversions

`int(x)`, `float(x)`, `string(x)`, and `bool(x)` are the only cast forms. They accept **primitive operands only** — struct, func, component, list, and option values are rejected. The cast syntax never dispatches to user-defined methods; define a method and call it as `x.string()` instead.

<!-- SNGL-component -->

```sngl
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
| `int`    | `int`, `float`, `string`, `bool`, `enum`, single-base `unit`            |
| `float`  | `int`, `float`, `string`, `bool`, `enum`, single-base `unit`            |
| `string` | any primitive / string-representable (`color`, `date`, …) / enum / unit |
| `bool`   | `bool`, `string`                                                        |

A cast of a unit reads its magnitude in the unit's base: `int(1500ms)` is `1500`. A multi-base unit such as `measurement` has no single number, so `int(m)` is refused; read one base with `m.px` instead.

### Implicit conversions

Narrow list — most type changes are rejected and require an explicit cast.

| From              | To            | When                                                        |
|-------------------|---------------|-------------------------------------------------------------|
| `int`             | `float`       | Anywhere a `float` is expected (auto-promotion)             |
| `int` literal `0` | any unit type | Typed zero: `var t time.duration = 0`                       |
| `string`          | string-repr.  | Assignment: `var d time.date = "2024-01-15"` (validated)    |
| string-repr.      | `string`      | Assignment: `var s string = myDate`                         |
| `T`               | `option<T>`   | Assignment: `var x option<int> = 5`                         |
| `null`            | `option<T>`   | Assignment: `var x option<int> = null`                      |
| `null`            | `func(...)`   | Assignment; calling it yields the return type's zero value  |
| `func() T`        | `T`           | A zero-arg function passed as an argument, prop or `{expr}` |
| any               | `dyn`         | `dyn` accepts any type                                      |
| `dyn`             | any           | Escape hatch; no runtime check                              |

`float` → `int` is **not** implicit — write `int(x)` to discard the fractional part. Non-zero `int` → unit is **not** implicit — use unit literals (`5s`) or multiply (`n * 1s`).

### Disallowed conversions

These are compile errors:

| Conversion                                           | Error                                            |
|------------------------------------------------------|--------------------------------------------------|
| `null` → struct                                      | `cannot initialize T with null`                  |
| struct → `int()` / `float()` / `string()` / `bool()` | `cannot convert` — define a method instead       |
| list/option/func/component → cast                    | `cannot convert` — define a method instead       |
| non-zero `int` → unit                                | `cannot initialize` / `cannot pass`              |
| string → enum                                        | `cannot initialize` — write the member           |
| wrong type → component param                         | `does not match`                                 |
| bad string literal → string-repr. type               | format-specific error (e.g. invalid date, color) |

### String interpolation

Inside `"{expr}"`, a primitive, string-representable type, enum, unit, `null`, list, option, or zero-arg function flows through automatically. For any other type — struct, component, etc. — the compiler looks up a `.string()` method on the value's type and calls it. When no such method exists the interpolation is a compile error:

<!-- SNGL-component -->

```sngl
struct Point {
    x int = 0
    y int = 0
}
func Point.string(p Point) => "({p.x},{p.y})"
var count = 42
var active = true
var origin = Point{x=0, y=0}
func label() => "count={count}, active={active}, origin={origin}"
```

Removing the `Point.string` method makes the `{origin}` interpolation a compile error, not a silent `{...}` render.

Interpolation is `{expr}`. A `$` before the brace is a literal dollar sign, not part of the syntax.

## State

### var -- mutable reactive state

`var` declares mutable state that triggers UI updates when changed. Group related vars:

<!-- SNGL-component -->

```sngl
var (
    count = 0
    name = "World"
    active = true
)
```

### Where state lives

Declare state in the window or component that uses it. A `var` in a component body belongs to each instance of that component; a `var` in a window body is scoped to that window's body and stored by what holds the window -- the package, or the component that renders it. That keeps each piece of state next to the only code that reads and writes it, and a component that owns its state is one you can test on its own.

A top-level `var` is package state, and its meaning depends on the target: on a target whose windows share one process (Bubbletea, Fyne, GTK4) every window reads the same cell, while on `html` each page is a separate document with its own copy. Reach for it when that sharing is really what you want, not as the default place to put things.

When a parent and child both need a value, keep it in the parent and hand it down -- as a prop to read it, or through a bidirectional binding (see below) to let the child change it.

### const -- compile-time values

`const` declares immutable values evaluated at compile time. Use them for configuration and magic numbers:

<!-- SNGL-component -->

```sngl
const (
    MAX_ITEMS = 100
    DEFAULT_NAME = "unnamed"
    RATIO float = 1.5
)
const SINGLE = 42
```

Constants cannot reference `var` values. For `pi` and `tau`, import `sngl:math` rather than declaring your own.

### Derived state with zero-arg functions

Zero-arg functions serve as derived state -- they re-evaluate when what they read changes, and they are read-only:

<!-- SNGL-component -->

```sngl
var count = 0
func doubled() => count * 2
func label() => "Count: {count}"
```

Zero-arg functions are auto-invoked when referenced without `()`: `ui.text(value=label)` calls `label()` implicitly.

### Reacting to a change

`@change` on a `var` runs a statement block whenever the variable is assigned. Name a parameter to receive the new value:

<!-- SNGL-component
struct Todo { text string = ""; done bool = false }
func saveTodos(items list<Todo>) {}
-->

```sngl
var todos list<Todo> @change {
    saveTodos(todos)
}
var query = "" @change(q) {
    todos = []
}
```

Prefer derived state to `@change` wherever the answer is a function of other state: a zero-arg function cannot fall out of date, while a handler copying one var into another can. Keep `@change` for side effects -- saving, logging, starting a request. For work that belongs to a lifetime rather than to an assignment, use an `effect` (see Lifetimes).

### Gotchas

Structs cannot be null (`var todo Todo = null` is a compile error; use `var todo Todo` or `var todo option<Todo> = null` for nullable). Empty lists need an explicit type (`var items list<string> = []`) because the compiler cannot infer `T` from `[]`.

A view body describes a tree; nothing runs it top to bottom. An assignment written directly in one is a compile error -- put it in a handler, a function, or an effect's `@mount`.

## Functions

### Expression form

For single-expression functions, the body follows the parameter list directly:

```sngl
func add(a int, b int) => a + b
func greet(name string) => "Hello, {name}!"
```

An expression-bodied function cannot carry a return type annotation; its type is always inferred.

### Block form

For multi-step logic, use a body with `return`, and annotate the return type:

```sngl
func clamp(val int, lo int, hi int) int {
    var clamped = val < lo ? lo : val
    var result = clamped > hi ? hi : clamped
    return result
}
```

There is no `->` form: `func f() -> int` does not parse.

### Action functions

A function with no return value is an action. It can mutate the state in scope where it is declared, and is typically called from event handlers:

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

Declare an action where its state lives: inside the component or window whose vars it writes, or at the top level when it writes package state.

Keep the two kinds apart. A function you read from a view -- a zero-arg derived value, a formatter -- should only read; a function that writes should be called from a handler. The compiler infers which is which from the body, and a read-only function is what it can re-evaluate freely.

### Type methods

Attach a function to a type with a dotted name. The first parameter is the receiver:

```sngl
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

A struct's methods may also be written inside its body, where the receiver is `this`:

```sngl
struct Point {
    x int = 0
    y int = 0
    func sum() => this.x + this.y
}
```

Type methods work on primitives (`int`, `float`, `string`, `bool`, `color`, `list`) and user-defined structs.

### Generic functions

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
is inferred from the lambda's return type, and the lambda's own parameter type
is inferred from the receiver's element type:

<!-- SNGL-component
struct Todo { text string = ""; done bool = false }
-->

```sngl
var todos = [Todo{text="a", done=true}, Todo{text="b", done=false}]
func active() => todos.filter(func(t) => !t.done)
func labels() => todos.map(func(t) => t.text)
```

### Lambdas

Inline functions for filtering and mapping. A lambda is written `func(x) => expr`. The `func` keyword is required -- a bare `(x) => expr` does not parse --
but the parameter type is inferred from the function type the position
expects, so `filter` binds `t` to the list's element type, as in the example
above.

A declared function type is a position like any other, so the same inference
applies when a lambda is assigned to one. Write the parameter type explicitly
where there is nothing to infer from, or where it reads better:

<!-- SNGL-component
struct Todo { text string = ""; done bool = false }
-->

```sngl
var byDone func(t Todo) bool = func(t) => t.done
var isEmpty = func(s string) => s == ""
```

## Components

### Declaration

A component groups params, state, functions, and visual nodes. Its return position names the family it belongs to -- `ui.node` for a widget:

```sngl
import ui "sngl:ui"

component Counter(label = "", start = 0) ui.node {
    var count = start
    ui.hbox {
        ui.text(value="{label}: {count}")
        ui.button(text="+", @click { count += 1 })
    }
}

ui.window {
    Counter(label="Clicks", start=10)
}
```

A component renders only where something instantiates it: in a window, or in another component that is.

### Params

Params are the component's public API. They are declared in parentheses after the component name and accept values from parent components:

```sngl
import ui "sngl:ui"

component MyWidget(label = "default", count int, size enum { small, medium, large } = medium) ui.node {
    ui.text(value=label)
}
```

A param without a default is required: every call site has to pass it. Parents pass params as named arguments:

<!-- SNGL-component
component Counter(label = "", start = 0) ui.node { ui.text(value=label) }
-->

```sngl
Counter(label="Clicks", start=10)
Counter()
```

Imported components are qualified by their package's alias (e.g., `widgets.Counter(label="Imported")`).

### Slots

A component that wraps content declares a slot: a parameter whose type is a component type. `...component ui.node` is the rest slot, which receives the children written bare at the call site; name it for what it holds:

```sngl
import ui "sngl:ui"

component Card(title string, content ...component ui.node) ui.node {
    ui.vbox(style={gap=8px, padding=12px}) {
        ui.text(value=title, style={fontWeight=bold})
        content
    }
}

ui.window {
    Card(title="Settings") {
        ui.text(value="Dark mode")
        ui.text(value="Notifications")
    }
}
```

A named slot is populated by name with a nested `component`. The slot's type bounds how many: bare is any number, `option<T>` none or one. A slot may also hand its population arguments, declared in its parameter list and bound by position:

```sngl
import ui "sngl:ui"

component Panel(title string, header component option<ui.node>, content ...component ui.node) ui.node {
    ui.vbox {
        header
        ui.text(value=title)
        content
    }
}

component Rows(items list<string>, row component(item string, index int) ui.node) ui.node {
    ui.vbox {
        for var i, item = items {
            row(item, i)
        }
    }
}

ui.window {
    Panel(title="Account") {
        component header {
            ui.text(value="Signed in")
        }
        ui.text(value="Body")
    }
    Rows(items=["a", "b"]) {
        component row(item, i) {
            ui.text(value="{i}: {item}")
        }
    }
}
```

There is no `slot` keyword and no magic `children` name: a slot is an ordinary parameter, and the whole of a component's API is its parameter list.

### Stdlib components

`sngl:ui` provides layout (`vbox`, `hbox`, `stack`, `scroll`, `spacer`, `divider`, `splitview`, `card`), input (`button`, `input`, `textarea`, `checkbox`, `radio`, `toggle`, `select`, `datepicker`), display (`text`, `image`, `badge`, `chip`, `avatar`, `progress`, `spinner`, `table`, `tree`), navigation (`link`, `tabs`, `menu`, `menubar`, `toolbar`), and overlay (`modal`, `drawer`, `tooltip`, `popover`) components. Drawing lives in `sngl:ui/draw` and rich text in `sngl:ui/markup`. `sngl doc sngl:ui` lists every component with its props and events.

## Visual Nodes

### Syntax

Visual nodes are component instances rendered as UI elements:

<!-- SNGL-component -->

```sngl
ui.text(value="hello", style={fontSize=24px})
```

Both `()` and `{}` are optional:

<!-- SNGL-component -->

```sngl
ui.spacer
ui.text(value="Hello")
ui.vbox { ui.text(value="Hi") }
```

### Params

Params are typed values defined by the component's declaration. The compiler validates param names and types:

<!-- SNGL-component
var count = 0
var name = ""
-->

```sngl
ui.button(text="Submit", disabled=count <= 0)
ui.input(value=name, placeholder="Enter name", type="email")
ui.image(src="photo.png", alt="Profile", fit=cover)
```

### Events

Events use the `@` prefix and contain mutation statements:

<!-- SNGL-component -->

```sngl
var count = 0
var name = ""
ui.button(text="+", @click { count += 1 })
ui.input(value=name, @input(e) { name = e.value })
```

A handler body may hold several statements, one per line:

<!-- SNGL-component
struct Todo { text string = ""; done bool = false }
-->

```sngl
var (
    newTodo = ""
    todos list<Todo> = []
)
ui.button(text="Add", @click {
    todos.push(Todo{text=newTodo, done=false})
    newTodo = ""
})
```

Available events depend on the component (check its declaration with `sngl doc`). A handler names its payload parameter to read it: `@input(e) { ... e.value }`. There is no ambient `event` identifier.

### Bidirectional Bindings

Input components like `input` and `checkbox` support **bidirectional bindings** using the `:` prefix. A bidirectional binding automatically propagates mutations from the child component to a parent variable without requiring a manually written event handler.

Instead of manually wiring an event:

<!-- SNGL-component -->

```sngl
var name = ""
ui.input(value=name, @input(e) { name = e.value })
```

Use a bidirectional binding:

<!-- SNGL-component -->

```sngl
var name = ""
ui.input(:value=name)
```

Bidirectional bindings work with custom components too. Declare a parameter with the `:` prefix:

```sngl
import ui "sngl:ui"

component Stepper(:count = 0) ui.node {
    ui.button(text="+", @click { count += 1 })
    ui.text(value=string(count))
}

ui.window {
    var steps = 0
    Stepper(:count=steps)
    ui.text(value="Steps: {steps}")
}
```

When the child assigns `count += 1`, the parent variable `steps` automatically updates. The binding works transparently across component boundaries.

### Inline styles

Apply styles directly on any node:

<!-- SNGL-component -->

```sngl
ui.vbox(style={padding=16px, gap=12px})
ui.text(value="hello", style={color=#007700, fontSize=24px, fontWeight=bold})
```

### Conditional rendering

Use `if`, with `else` and `else if` as you would expect:

<!-- SNGL-component -->

```sngl
var count = 0
if count == 0 {
    ui.text(value="Empty", style={color=#777777})
} else if count > 10 {
    ui.text(value="Full", style={color=#CC0000})
} else {
    ui.text(value="{count} items")
}
```

A map or anonymous-struct literal in an `if` or `for` head has to be parenthesized, because the brace after the head opens the body.

### Iteration

`for var x = xs` walks a list; `for var i, x = xs` also binds the index, which always comes first:

<!-- SNGL-component
struct Todo { id int = 0; text string = "" }
-->

```sngl
var items = ["a", "b", "c"]
var todos = [Todo{id=1, text="Write docs"}, Todo{id=2, text="Fix bug"}]
for var item = items {
    ui.text(value=item)
}
for var idx, todo = todos {
    ui.text(value="{idx}: {todo.text}", key=todo.id)
}
```

Use `key` for stable identity across re-renders when the list changes. Key by something that belongs to the element, like an id; the index changes whenever an element is inserted or removed ahead of it, which is exactly when a key has to hold still.

`var` is what makes the head a declaration. Without it the loop binds nothing
and what follows `for` is the iterable itself, which is the form to reach for
when the body never names the element:

<!-- SNGL-component -->

```sngl
import ui "sngl:ui"
import seq "sngl:seq"
for seq.count(3) {
    ui.text(value="•")
}
```

A map is walked with `for var k, v = m`, but only in imperative code -- a function, handler or timer. A map has no defined order, so in a view body two renders could lay the same map out differently; build a list from it in a handler and loop over that.

### Counting loops

There is nothing to iterate when the numbers are the point, so `sngl:seq`
produces them: `count(n)` runs a loop n times, `range(start, end)` counts
between two bounds, and `step(start, end, by)` counts by something other than
one — negative to count down. The end bound is exclusive in all three.

<!-- SNGL-component -->

```sngl
import ui "sngl:ui"
import seq "sngl:seq"
var n = 4
for var i = seq.range(1, n) {
    ui.text(value="row {i}")
}
for var d = seq.step(10, 0, -2) {
    ui.text(value="{d}")
}
```

A sequence written directly in a loop head becomes the host's own counting
loop. Assigned to a variable or passed to a function it is an ordinary
`iter<int>` — a sequence the loop pulls from, not a list of numbers — so
neither spelling allocates one. Reach for `sngl:seq` rather than building a
list of numbers to loop over.

### Loops that walk nothing

The loops above work in a view body, where a loop says how many copies of
its body the tree holds. In a function, a handler or a timer there is no tree,
and two more forms are available: a condition to test before each iteration,
and no head at all.

<!-- SNGL-component -->

```sngl
func firstMultiple(of int, atLeast int) int {
    var i = of
    for {
        if i >= atLeast {
            break
        }
        i = i + of
    }
    return i
}

func countDigits(n int) int {
    var left = n
    var digits = 0
    for left > 0 {
        left = left / 10
        digits = digits + 1
    }
    return digits
}
ui.text(value="{firstMultiple(3, 10)} in {countDigits(120)} digits")
```

`break` ends the innermost loop and `continue` ends the current iteration of
it. A `for { }` with *no* break never falls out of the bottom, so a function
can end inside one — `for { … return … }` is a complete body on its own.
`firstMultiple` breaks, so the `return i` after its loop is reached, and
required.

Neither form declares a variable — there is no element to bind — and neither
may be written in a view body, where a loop says how many copies of its body
the tree holds and a condition cannot say that. Nor may `break` or `continue`:
a view body's loop is a template stamped once per element, not a statement
stream.

### for...else

The `else` block renders when the list is empty. It works with both `for var item = list` and `for var index, item = list`:

<!-- SNGL-component -->

```sngl
var items list<string> = []
for var item = items {
    ui.text(value=item)
} else {
    ui.text(value="No items yet")
}
```

Generally the `else` runs when **the body never ran**, which is what "the list
was empty" is a case of. A condition loop reads the same way — its `else` runs
when the condition was false the first time it was asked — and a `break` never
triggers it, since a loop cannot break out of a body that never ran.
`for { } else { }` is an error: the body always runs, so the block would be
unreachable.

In a view body the loop's head has to be something whose emptiness can be
asked without consuming it -- a list or a `sngl:seq` range -- and that can be
evaluated twice, since the view asks whether it is empty before walking it.

### Element refs

Tag a node with `#id` to name it. A test reads its props and fires its events through that name (see Testing):

<!-- SNGL-component -->

```sngl
var count = 0
ui.button #inc(text="+", @click { count += 1 })
ui.text #display(value="Count: {count}")
```

A ref names the node, not a cell. A prop is what the tree says it is for as long as the node is rendered, so assigning to one (`display.value = "…"`) is a compile error: change the state the prop reads instead. A node also may not read its own `#id` in its own arguments.

## Events and Mutations

### Assignment operators

`=`, `+=`, `-=`, `*=`, `/=`, `%=` work on vars and struct fields:

<!-- SNGL-component -->

```sngl
var count = 0
var label = ""
ui.button(text="+5", @click { count += 5 })
ui.button(text="tag", @click { label += " tagged" })
```

### Toggle

`!!` flips a boolean in place:

<!-- SNGL-component -->

```sngl
var active = true
ui.button(text="Toggle", @click { active!! })
```

### List mutations

`push` and `remove` change a list in place, and so does assigning through an index; observers are notified as they would be for any assignment. `filter`, `map`, `reverse` and `slice` return a new list and leave the receiver alone:

<!-- SNGL-component -->

```sngl
var items = [1, 2, 3]
ui.button(text="Add", @click { items.push(4) })
ui.button(text="Remove first", @click { items.remove(0) })
ui.button(text="Evens", @click { items = items.filter(func(x) => x % 2 == 0) })
```

`push` returns nothing, so `items = items.push(4)` is an error rather than a second way to say the same thing.

### Component events

A component notifies its parent through an event. The event is declared on the
component with `@name` and the parameters it passes -- `@save()` passes none,
`@moved(x int, y int)` two, and `@changed int` is the one-parameter case --
and firing it is an ordinary call on that name. The handler binds what the
event passes by position, `@moved(x, y) { … }`:

```sngl
import ui "sngl:ui"

component SaveButton(label = "Save", @save()) ui.node {
    ui.button(text=label, @click { save() })
}

ui.window {
    var status = ""
    SaveButton(@save { status = "saved" })
    ui.text(value=status)
}
```

### Gotchas

Mutations belong in event handlers, action functions, timer ticks and effect handlers. A view body, a prop expression and a derived-state function describe what is rendered and should only read.

## Lifetimes

### Timers

`time.timer` fires `@tick` every `interval` while `enabled` is true. It is a node, placed in the body whose state it drives, and it renders nothing:

```sngl
import ui "sngl:ui"
import time "sngl:time"

ui.window {
    var (
        progress float = 0
        running = true
    )
    time.timer(interval=100ms, enabled=running, @tick {
        progress += 0.1
    })
    ui.text(value=string(progress))
    ui.button(text="Stop", @click { running = false })
}
```

Toggling `enabled` is the idiomatic way to start and stop a timer; `enabled` defaults to `true`. The timer is cleaned up when the body it sits in leaves the tree, so a timer inside an `if` runs only while that branch is shown.

### Effects

`effect` brackets a lifetime: `@mount` runs when the node enters the tree and `@unmount` when it leaves. Use it for anything the program has to set up and later give back -- a subscription, a request, a host resource. `on` keys the effect: when its value changes, the old lifetime ends and a new one begins, and each handler receives the value its own lifetime was keyed on:

```sngl
import ui "sngl:ui"

component Room(name string) ui.node {
    var log list<string> = []
    effect(on=name, @mount(r) { log.push("join {r}") }, @unmount(r) { log.push("leave {r}") })
    ui.text(value=log.join(", "))
}

ui.window {
    var room = "lobby"
    ui.input(:value=room)
    Room(name=room)
}
```

What a handler reads does not subscribe it: an effect runs again only when `on` changes. If it should restart when some value changes, put that value in `on`.

### Error boundaries

`boundary` catches an error raised beneath it. `@error` reports it, and a `failed` slot replaces the content from then on; a boundary needs at least one of the two:

```sngl
import ui "sngl:ui"

ui.window {
    var message = ""
    boundary(@error(e) { message = e.message }) {
        ui.button(text="Fail", @click { error.raise("boom", "demo") })
    }
    ui.text(value=message)
}
```

### Contexts

A `context` supplies a value to everything beneath a point in the tree without threading it through every prop. `context #name(default)` declares one, and `name(value) { ... }` overrides it for a subtree:

```sngl
import ui "sngl:ui"

context #theme("light")

component Badge ui.node {
    ui.text(value="theme: {theme}")
}

ui.window {
    Badge()
    theme("dark") {
        Badge()
    }
}
```

## Style

### Inline styles

All styles are applied via the `style` param, whose type is `ui.Style`:

<!-- SNGL-component -->

```sngl
ui.vbox(style={gap=12px, padding=16px})
ui.text(value="bold", style={fontWeight=bold, color=#007700, fontSize=24px})
ui.button(text="go", style={margin=4px, background=#ff0000, padding=8px})
```

Enum-typed properties take a member, written bare: `fontWeight=bold`, `fontStyle=italic`, `alignItems=center`. A string is not a member.

### Named styles

There is no `style` declaration and no `class` prop. A reusable style is a
`ui.Style` constant, applied through the same `style=` prop as an inline one:

```sngl
import ui "sngl:ui"

const primary ui.Style = ui.Style{color=#0000ff, fontWeight=bold, fontSize=16px}
const secondary ui.Style = ui.Style{color=#777777, fontStyle=italic}

ui.window {
    ui.vbox {
        ui.text(value="hello", style=primary)
        ui.text(value="world", style=secondary)
    }
}
```

### Colors

Hex literals accept three, four, six, or eight digits — `#fff`, `#fff8`, `#ff0000`, `#00000080` — where the short forms expand CSS-style by doubling each digit. Use `color.rgb()` and `color.rgba()` for dynamic construction. Access channels with `.r`, `.g`, `.b`, `.a`.

### Units in styles

Lengths are `ui.measurement` values: `12px`, `1.5em`, `16rem`, `50pct`, `100vw`. Write the unit; it is what makes a length mean the same thing on every target.

### Gotchas

Style property names are camelCase: `fontSize`, `fontWeight`, `borderRadius`. Not `font-size`, not `font_size`.

## Testing

### Test functions

A test is a top-level function whose name starts with `test` and whose first parameter is a `test.Test`. A second parameter of a component type gets a fresh instance of that component, with its state reachable through the parameter:

```sngl
import ui "sngl:ui"
import test "sngl:test"

component Counter ui.node {
    var count = 0
    ui.button(text="+", @click { count += 1 })
    ui.text(value="Count: {count}")
}

ui.window {
    Counter()
}

func testStartsAtZero(t test.Test, c Counter) {
    t.assert(c.count == 0)
}

func testCountsUp(t test.Test, c Counter) {
    c.count += 1
    t.assert(c.count == 1)
}
```

Run them with `sngl test`; `--platform` picks the target the tests run on, and `--run` filters by name. This is one more reason to keep state in components: a component is the unit a test instantiates.

### assert and must

`t.assert(condition)` records a failure and lets the test continue, so one run can report several. `t.must(condition)` stops the test on the spot -- use it when the statements after it would be meaningless if the condition failed.

### Element refs

Tag nodes with `#id`, then read props and fire events in tests:

<!-- SNGL-nocheck -->

```sngl
import ui "sngl:ui"
import test "sngl:test"

component Counter ui.node {
    var count = 0
    ui.button #inc(text="+", @click { count += 1 })
    ui.text #display(value="Count: {count}")
}

ui.window {
    Counter()
}

func testIncrements(t test.Test, c Counter) {
    c.inc.click()
    t.assert(c.count == 1)
    t.assert(c.display.value == "Count: 1")
}
```

Read props: `c.display.value`. Fire events: `c.inc.click()`. A ref inside a `for` becomes a list: `c.item[0].value`, `c.item[2].value`. A ref inside an `if` is `null` while the branch is hidden, so `t.assert(c.banner != null)` checks that it is shown.

### State isolation

Each test starts with fresh state. Mutations in one test do not leak to another.

### Timers in tests

Time does not pass on its own in a test. `t.tick()` advances to the next timer deadline and fires what is due; a timer whose `enabled` is false does not fire:

```sngl
import ui "sngl:ui"
import time "sngl:time"
import test "sngl:test"

component Ticker ui.node {
    var (
        progress = 0
        running = true
    )
    time.timer(interval=100ms, enabled=running, @tick { progress += 1 })
    ui.text(value="{progress}")
}

ui.window {
    Ticker()
}

func testTicks(t test.Test, c Ticker) {
    t.tick()
    t.assert(c.progress == 1)
    c.running = false
    t.tick()
    t.assert(c.progress == 1)
}
```

`t.wait(predicate, timeoutMs)` advances simulated time until the predicate holds or the timeout passes.

### Subtests

`t.test(name, func(t, c) { ... })` runs a subtest on its own snapshot of the state. Changes in the subtest do not affect the enclosing test:

```sngl
import ui "sngl:ui"
import test "sngl:test"

component Box ui.node {
    var x = 0
    ui.text(value="{x}")
}

ui.window {
    Box()
}

func testNesting(t test.Test, c Box) {
    c.x = 1
    t.test("inner", func(t, c) {
        c.x = 5
        t.assert(c.x == 5)
    })
    t.assert(c.x == 1)
}
```

### Disabling tests

Prefix a declaration with `/-` to comment it out whole: `/- func testFlaky(t test.Test, c Box) { ... }`.

## Platform Targets

The output block (see File Structure) determines compilation targets. Override from the CLI: `sngl generate --lang go --platform bubbletea`. Other commands: `sngl run` (compile and execute), `sngl build` (distributable artifact), `sngl test` (run all tests), `sngl preview` (live preview).

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
| Import aliases   | last path segment           | `ui`, `time`, `draw`       |

The standard library's own components are lower camelCase (`ui.vbox`, `ui.datepicker`); the package qualifier keeps them apart from yours.

## Common Patterns

### Todo list

```sngl
import ui "sngl:ui"

struct Todo {
    id int = 0
    text string = ""
    done bool = false
}

ui.window(title="Todos") {
    var (
        newTodo = ""
        nextId = 1
        todos list<Todo> = []
    )
    ui.vbox(style={padding=16px, gap=8px}) {
        ui.hbox(style={gap=8px}) {
            ui.input(:value=newTodo, placeholder="New todo")
            ui.button(text="Add", disabled=newTodo == "", @click {
                todos.push(Todo{id=nextId, text=newTodo})
                nextId += 1
                newTodo = ""
            })
        }
        for var idx, todo = todos {
            ui.hbox(key=todo.id, style={gap=8px}) {
                ui.checkbox(checked=todo.done, @change { todos[idx].done!! })
                ui.text(value=todo.text)
                ui.button(text="x", @click { todos.remove(idx) })
            }
        } else {
            ui.text(value="Nothing to do")
        }
    }
}
```

### Form with validation

```sngl
import ui "sngl:ui"

ui.window {
    var email = ""
    func valid() => email.contains("@") && email.length() > 3
    ui.vbox(style={padding=16px, gap=8px}) {
        ui.input(:value=email, placeholder="Email", type="email")
        if !valid {
            ui.text(value="Enter a valid email", style={color=#CC0000, fontSize=12px})
        }
        ui.button(text="Submit", disabled=!valid)
    }
}
```

### Modal open/close

```sngl
import ui "sngl:ui"

ui.window {
    var showModal = false
    ui.vbox(style={padding=16px}) {
        ui.button(text="Open", @click { showModal = true })
        ui.modal(open=showModal, title="Settings", @close { showModal = false }) {
            ui.text(value="Content goes here")
            ui.button(text="Close", @click { showModal = false })
        }
    }
}
```

### Timer animation

```sngl
import ui "sngl:ui"
import time "sngl:time"

component Loader ui.node {
    var (
        progress float = 0.0
        running = false
    )
    time.timer(interval=50ms, enabled=running, @tick {
        progress += 0.01
    })
    ui.vbox(style={padding=16px, gap=8px}) {
        ui.progress(value=progress, max=1.0, showValue=true)
        ui.button(text=running ? "Pause" : "Start", @click { running!! })
        ui.button(text="Reset", @click {
            progress = 0.0
            running = false
        })
    }
}

ui.window {
    Loader()
}
```

### Filtered list

```sngl
import ui "sngl:ui"

struct Todo {
    id int = 0
    text string = ""
    done bool = false
}

ui.window {
    var todos = [Todo{id=1, text="Write docs", done=true}, Todo{id=2, text="Fix bug", done=false}]
    func active() => todos.filter(func(t) => !t.done)
    func activeCount() => active().length()
    ui.vbox(style={padding=16px, gap=8px}) {
        ui.text(value="{activeCount} remaining")
        for var idx, todo = todos {
            ui.checkbox(key=todo.id, checked=todo.done, label=todo.text, @change { todos[idx].done!! })
        }
    }
}
```
