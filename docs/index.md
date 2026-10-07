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
- **First-class expressions** — Go-like expressions appear directly in the view and compile to native logic in the target language
- **Compile-time analysis** — types, dependencies, and bindings are verified before code generation
- **No runtime of its own** — the generated code uses each host's own toolkit; on the web it patches exactly the nodes a change touches, with no virtual DOM

## Quick Example

```sngl
import ui "sngl:ui"

struct Todo {
    text string = ""
    done bool = false
}

ui.window(title="Todos") {
    var (
        newTodo = ""
        todos list<Todo> = []
    )
    func status() => "Todo List ({todos.length} items)"
    ui.vbox(style={gap=12px, padding=16px}) {
        ui.text(value=status, style={fontWeight=bold, fontSize=24px})
        ui.hbox(style={gap=8px, alignItems=center}) {
            ui.input(:value=newTodo, placeholder="Buy eggs", style={flex=1})
            ui.button(text="Add", @click {
                todos.push(Todo{text=newTodo})
                newTodo = ""
            })
        }
        ui.vbox(style={gap=4px}) {
            for var &item = todos {
                ui.checkbox(:checked=item.done, label=item.text)
            }
        }
    }
}
```

## Architecture

1. **Parser** — parses `.sngl` files into an AST
2. **Checker** — resolves names and types and produces a typed IR
3. **Optimizer** — constant folding and dead-code elimination for the chosen target
4. **Lowering** — rewrites constructs a target cannot express natively into ones it can
5. **Code Generator** — pluggable language and platform backends emit the target code

## Platforms

| Target   | Platform  | Languages                           | Needs to build the output                  |
|----------|-----------|-------------------------------------|--------------------------------------------|
| Web      | html      | `none` (static site), `go` (server) | nothing for a static site; Go for a server |
| Terminal | bubbletea | `go`                                | Go                                         |
| Desktop  | fyne      | `go`                                | Go, cgo, OpenGL and X11 headers            |
| Desktop  | gtk4      | `go`                                | Go, cgo, GTK 4 development files           |
| Android  | android   | `kotlin`, `go`                      | JDK and the Android SDK                    |

## Next Steps

- [Getting Started](learn/getting-started.html) — build your first app
- [Tour](tutorial.html) — interactive, lesson-by-lesson walkthrough
- [Effective SNGL](reference/effective-sngl.html) — idioms and conventions
- [Language Specification](reference/specification.html) — formal grammar and semantics
