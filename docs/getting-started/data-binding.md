---
title: "Data Binding"
order: 4
description: "Reactive state, derived values, and expressions"
---

## Reactive State

### var

Declares mutable reactive state inside a component:

```sngl
var count = 0
var name = "World"
var active = true
```

Types are inferred from default values. Explicit types are needed when the type can't be inferred:

```sngl
var todos list<Todo> = []
var mode enum { light, dark } = "light"
```

Group multiple declarations with parentheses:

```sngl
var (
    count = 0
    name = "World"
    todos list<Todo> = []
)
```

### const

Declares immutable values:

```sngl
const maxItems = 100
const apiUrl = "https://api.example.com"
```

### Derived state

Zero-arg functions declare derived, read-only state that updates automatically when dependencies change. They are auto-invoked when referenced without `()`:

```sngl
func greeting() => "Hello, {name}!"
func isAdult() => user.age >= 18
func status() => "Todo List ({todos.length()} items)"
```

## Functions

Use `func` to declare reusable logic alongside your state. Functions can read component state and be called in expressions or event handlers:

```sngl
component main {
    var todos list<Todo> = []
    func done() => todos.filter((t) => t.done)
    func remaining() => size(todos) - size(done)

    func addTodo(text string) {
        todos.push(Todo{text=text, done=false})
    }

    func reset() {
        todos = []
    }

    text(value="{size(done)}/{size(todos)} done, {remaining} remaining")
    button(text="Add", @click { addTodo("New item") })
    button(text="Reset", @click { reset() })
}
```

Inline functions (`(params) => expr`) can be passed to list methods like `filter` and `map`. Parameter types are inferred from context when omitted. Functions with a return type are pure and work in any expression context. Void functions (no return type) can mutate state and are called from event handlers. See the [Language Reference](/language/reference/#functions) for the full syntax.

## Modifiers

### Data events

React to variable changes with inline statement blocks:

```sngl
var todos list<Todo> @change {
    saveTodos(todos)
}

var count = 0 @init {
    loadCount()
}
```

Events: `@change`, `@init`, `@insert(item)`, `@delete(item)`.

## Expressions

SNGL uses Go-like expression syntax:

| Form | Example |
| --- | --- |
| Arithmetic | `count + 1`, `price * quantity` |
| Comparison | `age >= 18`, `name != ""` |
| Logical | `isAdult && isActive`, `!done` |
| Ternary | `loggedIn ? "Logout" : "Login"` |
| Field access | `user.name`, `todos[0].text` |
| Method call | `todos.length()` |
| Function call | `string(count)` |
| Struct literal | `Todo{text="hello", done=false}` |
| List literal | `[1, 2, 3]` |
| String interpolation | `"Hello, {name}!"` |

## Statements

Statements appear in event handler blocks and mutate state directly:

<!-- SNGL-component -->
```sngl
button(@click { count += 1 })

button(@click {
    todos.push(Todo{text=newTodo, done=false})
    newTodo = ""
})

checkbox(@change { todos[index].done!! })
```

| Operation | Syntax | Example |
| --- | --- | --- |
| Assignment | `target = value` | `count = count + 1` |
| Compound assign | `target op= value` | `count += 1` |
| Toggle | `target!!` | `active!!` |
| Method call | `lvalue.method(args)` | `todos.push(item)` |
| Emit | `@name(payload?)` | `@save(data)` |
