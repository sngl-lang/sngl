---
title: "Data Binding"
order: 4
description: "Reactive state, computed values, and expressions"
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
var mode enum<light | dark> = "light"
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

### computed

Declares derived, read-only state that updates automatically when dependencies change:

```sngl
computed greeting = "Hello, {name}!"
computed isAdult = user.age >= 18
computed status = "Todo List ({todos.length()} items)"
```

## Modifiers

### extern

Marks a variable as externally provided (not auto-initialized):

```sngl
var apiClient dyn = null extern
var save func(string) = null extern
```

### trigger

Generates an onChange callback:

```sngl
var todos list<Todo> = null trigger
var items list<Item> = null trigger("saveItems")
```

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
| Struct literal | `Todo{text: "hello", done: false}` |
| List literal | `[1, 2, 3]` |
| String interpolation | `"Hello, {name}!"` |

## Statements

Statements appear in event handler blocks and mutate state directly:

```sngl
button(@click={ count += 1 })

button(@click={
    todos.push(Todo{text: newTodo, done: false})
    newTodo = ""
})

checkbox(@change={ todos[index].done!! })
```

| Operation | Syntax | Example |
| --- | --- | --- |
| Assignment | `target = value` | `count = count + 1` |
| Compound assign | `target op= value` | `count += 1` |
| Toggle | `target!!` | `active!!` |
| Method call | `lvalue.method(args)` | `todos.push(item)` |
| Emit | `@name(payload?)` | `@save(data)` |
