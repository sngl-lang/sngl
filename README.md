<p align="center">
  <img src="docs/sngl.svg" alt="SNGL" width="200">
</p>

<h1 align="center">SNGL</h1>

<p align="center">A purpose-built language for reactive, cross-platform UIs.</p>

---

Write your UI once. SNGL compiles it to Web, TUI, Desktop, and Mobile targets.

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

    ui.vbox(style={gap=12, padding=16}) {
        ui.text(value=status, style={fontWeight=bold, fontSize=24})
        ui.hbox(style={gap=8, alignItems=center}) {
            ui.input(:value=newTodo, placeholder="Buy eggs", style={flex=1})
            ui.button(text="Add", @click {
                todos.push(Todo{text=newTodo})
                newTodo = ""
            })
        }
        ui.vbox(style={gap=4}) {
            for var index, item = todos {
                ui.checkbox(checked=item.done, label=item.text, @change { todos[index].done!! })
            }
        }
    }
}
```

A program is one or more `ui.window`s at the root of a file. State declared in a
window's body, or in a component it renders, is reactive: the UI updates
wherever it is read.

## Features

- **Declarative** — describe what your interface looks like, not how to build it
- **Reactive** — state changes automatically propagate to the UI
- **Cross-platform** — one source targets the web, BubbleTea, Fyne, GTK 4, and Android
- **Type-safe** — types, bindings, and dependencies are verified at compile time
- **Minimal runtime** — the web and Fyne targets patch only what changed, with no virtual DOM

## Platforms

| Target   | Platform  | Languages                           | Needs to build the output                  |
|----------|-----------|-------------------------------------|--------------------------------------------|
| Web      | html      | `none` (static site), `go` (server) | nothing for a static site; Go for a server |
| Terminal | bubbletea | `go`                                | Go                                         |
| Desktop  | fyne      | `go`                                | Go, cgo, OpenGL and X11 headers            |
| Desktop  | gtk4      | `go`                                | Go, cgo, GTK 4 development files           |
| Android  | android   | `kotlin`, `go`                      | JDK 17–23 and the Android SDK              |

A program names its targets in an `output` block, for example
`output { none { html } go { bubbletea } }`; `--platform` and `--lang` pick one
from the command line.

## Install

Requires Go 1.26+.

```bash
git clone https://git.duckfam.us/jonathan/sngl.git
cd sngl
go install ./cmd/sngl
```

Prebuilt binaries are linked from the
[installation page](https://jonathan.git.duckfam.us/sngl/learn/installation.html).

## Usage

```bash
sngl check todo.sngl                                  # parse and type-check
sngl generate --platform html --out out/ todo.sngl    # write a static site to out/
sngl generate --platform bubbletea --lang go --out ui/ todo.sngl
sngl run --platform bubbletea todo.sngl               # compile and run
sngl preview todo.sngl                                # live preview in the browser
sngl fmt todo.sngl                                    # format in place
sngl lsp                                              # language server for editors
```

`sngl --help` lists every command.

## Architecture

```
.sngl source
    │
    ├─ Parser ──▶ AST
    ├─ Checker ──▶ typed IR
    ├─ Optimizer ──▶ constant folding, dead code elimination
    ├─ Lower ──▶ rewrites the IR to what the target can emit
    └─ Code Generator ──▶ target code (HTML/JS, Go, Kotlin)
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

See [`examples/`](examples/) for complete apps. The [todo app](examples/todo/) demonstrates structs, reactive state, derived values, event handling, and iteration, and embeds the generated BubbleTea model in a Go program that persists the list.
