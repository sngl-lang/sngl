<p align="center">
  <img src="docs/sngl.svg" alt="SNGL" width="200">
</p>

<h1 align="center">SNGL</h1>

<p align="center">A purpose-built language for reactive, cross-platform UIs.</p>

---

Write your UI once. SNGL compiles it to Web, TUI, Desktop, and Mobile targets.

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

## Features

- **Declarative** — describe what your interface looks like, not how to build it
- **Reactive** — state changes automatically propagate to the UI
- **Cross-platform** — one source targets HTML/JS, BubbleTea, Gio, and native mobile
- **Type-safe** — types, bindings, and dependencies are verified at compile time
- **Minimal runtime** — subscription-based updates, no virtual DOM

## Platforms

| Target | Language | Platform | Status |
| --- | --- | --- | --- |
| Web | JavaScript | HTML | In progress |
| TUI | Go | BubbleTea | In progress |
| Desktop | Go | Gio | Planned |
| Mobile | Swift/Kotlin | Native | Planned |

## Install

Requires Go 1.26+.

```bash
go install git.duckfam.us/jonathan/sngl/cmd/sngl@latest
```

## Usage

```bash
# Compile a .sngl file to all declared targets
sngl compile todo.sngl

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
