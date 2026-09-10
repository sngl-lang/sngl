---
title: SNGL
order: 0
description: A purpose-built language for reactive, cross-platform UIs
---

## What is SNGL?

SNGL (pronounced "snuggle") is a purpose-built language for describing reactive user interfaces that compile to multiple languages and platforms. Write your UI once, and SNGL compiles it to Web, Desktop, Mobile, and TUI targets.

It combines the reactivity of Svelte, the ergonomics of Vue, with a language and platform agnostic code generator. Tooling inspired by and built in Go.

## Design Philosophy

- **Declarative UI** — describe what your interface looks like, not how to build it
- **Reactive by default** — state changes automatically propagate to the UI
- **Cross-platform** — one source file targets Web, Desktop, Mobile, and Terminal
- **First-class expressions** — Go-like expressions appear directly in templates and compile to native logic in the target language
- **Compile-time analysis** — types, dependencies, and bindings are verified before code generation
- **Minimal runtime** — subscription-based updates with no virtual DOM diffing

## Quick Example

```sngl
import . "sngl:ui"

struct Todo {
    text string = ""
    done bool = false
}

component main node {
    var (
        newTodo = ""
        todos list<Todo> = []
    )
    func status() => "Todo List ({todos.length()} items)"
    vbox(style={gap=12, padding=16}) {
        text(value=status, style={fontWeight="bold", fontSize=24})
        hbox(style={gap=8, alignItems="center"}) {
            input(:value=newTodo, placeholder="Buy eggs", style={flex=1})
            button(text="Add", @click {
                todos.push(Todo{text=newTodo, done=false})
                newTodo = ""
            })
        }
        vbox(style={gap=4}) {
            for var index, item = todos {
                checkbox(checked=item.done, key=index, label=item.text, @change { todos[index].done!! })
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

| Target   | Language   | Platform  | Status            |
|----------|------------|-----------|-------------------|
| Web      | JavaScript | html      | Stable            |
| Terminal | Go         | bubbletea | Stable            |
| Desktop  | Go         | fyne      | Stable            |
| Desktop  | Go         | gtk4      | Needs system GTK4 |
| Android  | Kotlin     | android   | Stable            |

## Next Steps

- [Getting Started](learn/getting-started.html) — learn the basics
- [Tour](tutorial.html) — interactive, lesson-by-lesson walkthrough
- [Language Specification](reference/specification.html) — formal grammar
