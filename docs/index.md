---
title: "SNGL"
order: 0
description: "A purpose-built language for reactive, cross-platform UIs"
---

## What is SNGL?

![SNGL Logo](./assets/sngl.png)

SNGL is a purpose-built language for describing reactive user interfaces that compile to multiple platforms. Write your UI once, and SNGL compiles it to Web, Desktop, Mobile, and TUI targets.

## Design Philosophy

- **Declarative UI** — describe what your interface looks like, not how to build it
- **Reactive by default** — state changes automatically propagate to the UI
- **Cross-platform** — one source file targets Web (HTML/JS), Desktop (Go/Gio), and TUI (BubbleTea)
- **First-class expressions** — Go-like expressions appear directly in the syntax, not wrapped in strings
- **Compile-time analysis** — types, dependencies, and bindings are verified before code generation
- **Minimal runtime** — subscription-based updates with no virtual DOM diffing

## Quick Example

```sngl
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
    }
}
```

## Architecture

1. **Parser** — parses `.sngl` files into an AST
2. **Checker** — validates types, bindings, and dependency graphs
3. **Optimizer** — platform-specific AST transformations
4. **Code Generator** — pluggable backends emit target code

## Platforms

| Target  | Language     | Platform  | Status      |
| ------- | ------------ | --------- | ----------- |
| Web     | JavaScript   | HTML      | In progress |
| TUI     | Go           | BubbleTea | In progress |
| Desktop | Go           | Gio       | Planned     |
| Mobile  | Swift/Kotlin | Native    | Planned     |

## Next Steps

- [Getting Started](getting-started/index.html) — learn the basics
- [Language Reference](language/reference.html) — complete language guide
- [Language Specification](language/specification.html) — formal grammar
