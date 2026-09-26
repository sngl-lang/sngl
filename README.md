<p align="center">
  <img src="docs/sngl.svg" alt="SNGL" width="200">
</p>

<h1 align="center">SNGL</h1>

<p align="center">A purpose-built language for reactive, cross-platform UIs.</p>

---

Write your UI once. SNGL compiles it to Web, TUI, Desktop, and Mobile targets.

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
    vbox(style={gap = 12, padding = 16}) {
        text(value=status, style={fontWeight = bold, fontSize = 24})
        hbox(style={gap = 8, alignItems = center}) {
            input(:value=newTodo, placeholder="Buy eggs", style={flex = 1})
            button(text="Add", @click {
                todos.push(Todo{text = newTodo, done = false})
                newTodo = ""
            })
        }
        vbox(style={gap = 4}) {
            for var index, item = todos {
                checkbox(checked=item.done, key=index, label=item.text, @change { todos[index].done!! })
            }
        }
    }
}
```

## Features

- **Declarative** — describe what your interface looks like, not how to build it
- **Reactive** — state changes automatically propagate to the UI
- **Cross-platform** — one source targets HTML/JS, BubbleTea, Fyne, and Android
- **Type-safe** — types, bindings, and dependencies are verified at compile time
- **Minimal runtime** — subscription-based updates, no virtual DOM

## Platforms

| Target   | Language   | Platform  | Status            |
|----------|------------|-----------|-------------------|
| Web      | JavaScript | html      | Stable            |
| Terminal | Go         | bubbletea | Stable            |
| Desktop  | Go         | fyne      | Stable            |
| Desktop  | Go         | gtk4      | Needs system GTK4 |
| Android  | Kotlin     | android   | Stable            |

## Install

Requires Go 1.26+.

```bash
go install git.duckfam.us/jonathan/sngl/cmd/sngl@latest
```

## Usage

```bash
# Compile a .sngl file to all declared targets
sngl generate todo.sngl

# Start the LSP server (for editor integration)
sngl lsp
```

## Architecture

```
.sngl source
    │
    ├─ Parser ──▶ AST
    ├─ Checker ──▶ type validation
    ├─ Optimizer ──▶ platform-specific transforms
    └─ Code Generator ──▶ target code (HTML/JS, Go, ...)
```

Code generation is pluggable: platform backends and language translators register themselves via `codegen.RegisterPlatform` and `codegen.RegisterLang`.

## Documentation

Full docs are at the [SNGL documentation site](https://jonathan.git.duckfam.us/sngl), including a browser-based [Playground](https://jonathan.git.duckfam.us/sngl/playground.html) that compiles SNGL to HTML+JS via WebAssembly.

To build the docs locally:

```bash
go tool docsgen
# Open _site/index.html
```

## Examples

See [`examples/`](examples/) for complete apps. The [todo app](examples/todo/) demonstrates structs, reactive state, computed values, event handling, and iteration.

## License

See [LICENSE](LICENSE).
