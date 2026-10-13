---
title: Getting Started
order: 20
description: Build a SNGL app from Hello World to a todo list
---

```sngl mode=package
import "sngl:ui"

var runPlatform = "html"
var generateTarget = "html"
var embedTarget = "html"

component whenSelected(selected string, value string, content ...component ui.node) ui.node {
    if selected == value {
        ui.vbox(style={gap=12px}) {
            content
        }
    }
}
```

This tutorial walks through building a SNGL app from a one-line Hello World up
to a full todo list, then shows how to embed the generated code in a larger
project.

## Hello, World

A SNGL app is one or more windows, each the root of a tree of components. The
smallest app is a window holding a single text node:

```sngl
import "sngl:ui"

ui.window(title="Hello") {
    ui.text(value="Hello, World!")
}
```

`import "sngl:ui"` brings in the UI library under the name `ui`. Save the
file as `hello.sngl` and run it in a browser:

```bash
sngl run hello.sngl --platform html
```

## Reactive state and binding

State is declared with `var` (mutable) or a zero-argument `func` (derived). The
`:value` prefix on a prop is two-way binding: changes flow both ways. Replace
the contents of `hello.sngl` with:

```sngl
import "sngl:ui"

ui.window(title="Hello") {
    var name = "World"
    func greeting() => "Hello, {name}!"
    ui.vbox(style={gap=8px, padding=16px}) {
        ui.text(value=greeting)
        ui.input(:value=name)
    }
}
```

When the user types in the input, `name` updates, `greeting` recomputes, and
the text re-renders. No subscriptions to wire up. `value=greeting` names the
function without calling it: a zero-argument function is called wherever its
value is read, so the prop always shows the current greeting.

## Run it on any platform

Pick a target. The same source compiles to all of them. The first run of a Go
target downloads and compiles its dependencies, which can take several minutes
with no output; later runs start in seconds.

```sngl mode=island
import "sngl:ui"

ui.hbox(style={gap=8px}) {
    ui.button(text="HTML", @click { runPlatform = "html" })
    ui.button(text="BubbleTea (TUI)", @click { runPlatform = "bubbletea" })
    ui.button(text="Fyne (Desktop)", @click { runPlatform = "fyne" })
    ui.button(text="GTK 4 (Desktop)", @click { runPlatform = "gtk4" })
    ui.button(text="Android", @click { runPlatform = "android" })
}
```

```sngl mode=island
import "sngl:ui/markup"

whenSelected(selected=runPlatform, value="html") {
    markup.codeBlock {
        markup.text(value="sngl run hello.sngl --platform html")
    }
    markup.paragraph {
        markup.text(value="Builds a static page and serves it, printing the local URL to open. Pass ")
        markup.monospace {
            markup.text(value="--opt listen=:8080")
        }
        markup.text(value=" to pick the port. For live reload while you edit, use ")
        markup.monospace {
            markup.text(value="sngl preview hello.sngl")
        }
        markup.text(value=" instead.")
    }
}
```

```sngl mode=island
import "sngl:ui/markup"

whenSelected(selected=runPlatform, value="bubbletea") {
    markup.codeBlock {
        markup.text(value="sngl run hello.sngl --platform bubbletea")
    }
    markup.paragraph {
        markup.text(value="Compiles to a Bubble Tea program and runs it inline in your terminal. Tab moves between controls, Enter presses a button, Space toggles a checkbox, and typing goes to the focused input. Quit with Ctrl-C. Needs a Go toolchain.")
    }
}
```

```sngl mode=island
import "sngl:ui/markup"

whenSelected(selected=runPlatform, value="fyne") {
    markup.codeBlock {
        markup.text(value="sngl run hello.sngl --platform fyne")
    }
    markup.paragraph {
        markup.text(value="Builds and opens a native desktop window with Fyne. Needs a Go toolchain, a C compiler, and the OpenGL development headers Fyne builds against.")
    }
}
```

```sngl mode=island
import "sngl:ui/markup"

whenSelected(selected=runPlatform, value="gtk4") {
    markup.codeBlock {
        markup.text(value="sngl run hello.sngl --platform gtk4")
    }
    markup.paragraph {
        markup.text(value="Builds and opens a native GTK 4 window through cgo. Needs a Go toolchain, a C compiler, and the GTK 4 development files.")
    }
}
```

```sngl mode=island
import "sngl:ui/markup"

whenSelected(selected=runPlatform, value="android") {
    markup.codeBlock {
        markup.text(value="sngl run hello.sngl --platform android")
    }
    markup.paragraph {
        markup.text(value="Generates a Jetpack Compose project, builds the APK, and installs it on the connected device or emulator. Needs the Android SDK.")
    }
}
```

## Generate code you can wrap

`sngl run` is for iteration. To produce source you can ship inside a larger
project, use `sngl generate`, which writes the code to a directory (`-o`) and
stops. Each target's options, passed as `--opt key=value`, are listed on its
page under [Platforms](/platforms/index.html). Pick a target:

```sngl mode=island
import "sngl:ui"

ui.hbox(style={gap=8px}) {
    ui.button(text="Static HTML", @click { generateTarget = "html" })
    ui.button(text="Go web server", @click { generateTarget = "gohtml" })
    ui.button(text="Go desktop / TUI", @click { generateTarget = "go" })
    ui.button(text="Kotlin (Android)", @click { generateTarget = "kotlin" })
}
```

```sngl mode=island
import "sngl:ui/markup"

whenSelected(selected=generateTarget, value="html") {
    markup.codeBlock {
        markup.text(value="sngl generate hello.sngl --platform html -o ./site")
    }
    markup.paragraph {
        markup.text(value="Emits an ")
        markup.monospace {
            markup.text(value="index.html")
        }
        markup.text(value=" per window with its script inline. Host it on any static server.")
    }
}
```

```sngl mode=island
import "sngl:ui/markup"

whenSelected(selected=generateTarget, value="gohtml") {
    markup.codeBlock {
        markup.text(value="sngl generate hello.sngl --lang go --platform html -o ./ui")
    }
    markup.paragraph {
        markup.text(value="Emits a Go package ")
        markup.monospace {
            markup.text(value="ui")
        }
        markup.text(value=" whose ")
        markup.monospace {
            markup.text(value="Handler()")
        }
        markup.text(value=" returns an ")
        markup.monospace {
            markup.text(value="http.Handler")
        }
        markup.text(value=" serving one route per page. Event handlers that call into Go run on the server; the rest stay in the browser.")
    }
}
```

```sngl mode=island
import "sngl:ui/markup"

whenSelected(selected=generateTarget, value="go") {
    markup.codeBlock {
        markup.text(value="sngl generate hello.sngl --lang go --platform bubbletea -o ./ui")
    }
    markup.paragraph {
        markup.text(value="Emits a Go package ")
        markup.monospace {
            markup.text(value="ui")
        }
        markup.text(value=" with a ")
        markup.monospace {
            markup.text(value="Model")
        }
        markup.text(value=" and a ")
        markup.monospace {
            markup.text(value="New()")
        }
        markup.text(value=" constructor. The same works with ")
        markup.monospace {
            markup.text(value="--platform fyne")
        }
        markup.text(value=" or ")
        markup.monospace {
            markup.text(value="gtk4")
        }
        markup.text(value=". Add ")
        markup.monospace {
            markup.text(value="--opt main=true")
        }
        markup.text(value=" to get a runnable package main instead of a library; build it from a Go module, after ")
        markup.monospace {
            markup.text(value="go mod init")
        }
        markup.text(value=" and ")
        markup.monospace {
            markup.text(value="go mod tidy")
        }
        markup.text(value=". The package is named ")
        markup.monospace {
            markup.text(value="ui")
        }
        markup.text(value=" whatever the directory; ")
        markup.monospace {
            markup.text(value="--opt package=name")
        }
        markup.text(value=" changes it.")
    }
}
```

```sngl mode=island
import "sngl:ui/markup"

whenSelected(selected=generateTarget, value="kotlin") {
    markup.codeBlock {
        markup.text(value="sngl generate hello.sngl --lang kotlin --platform android --opt package=com.example.app -o ./app/src/main/java/com/example/app")
    }
    markup.paragraph {
        markup.text(value="Emits Kotlin source with a ")
        markup.monospace {
            markup.text(value="MainScreen")
        }
        markup.text(value=" composable you can call from any Compose ")
        markup.monospace {
            markup.text(value="setContent")
        }
        markup.text(value=" block. ")
        markup.monospace {
            markup.text(value="--opt package")
        }
        markup.text(value=" sets the Kotlin package; make it match the directory you generate into.")
    }
}
```

## Pre-configure with `output`

Repeating `--lang` and `--platform` flags is tedious. An `output` block in the
source declares the targets up front; bare `sngl run` and `sngl generate` then
pick them up automatically. It goes at the top level of the file, beside the
window, and a package has at most one.

<!-- SNGL-top
import "sngl:ui"

ui.window {
    ui.text(value="")
}
-->

```sngl
output {
    none { html }
    go {
        bubbletea
        fyne
    }
    kotlin { android }
}
```

Each language–platform pair in the block becomes one build. `none` is the
language for a target that needs no host language, which is what a static page
is. Same source, four outputs.

`sngl generate` writes every build into the current directory, or into `-o`.
`sngl run` runs the builds one after another in the order listed, each starting
when the one before exits; a served page never exits, and Ctrl-C stops
`sngl run` altogether, so pass `--platform` to run just one. The flags still win over the block:
`sngl generate --platform bubbletea` builds only that pair.

## Build a todo app

Now the full thing: a list of todos with an input field, an add button, and a
checkbox per item. Save it as `todo.sngl`; every line is part of the running
app, and the paragraphs after it explain the pieces.

```sngl
import "sngl:ui"

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
            } else {
                ui.text(value="Nothing to do.", style={color=#888888})
            }
        }
    }
}
```

`struct Todo` is the data shape. `var todos list<Todo> = []` starts empty and
is what the UI iterates. `func status()` is derived — the count updates
whenever `todos` changes. The state is scoped to the window's body; nothing
outside it can touch it.

`@click` is an event handler running a statement block; `todos.push(...)`
mutates the list, which updates what is rendered. `for var &item` binds each
element by reference, so the checkbox's `:checked` binding writes straight back
into the list. The loop's `else` renders when the list is empty.

## Persist state with a Go import

The todo list lives in memory. To save it across runs, import a Go package:
its exported functions become SNGL functions with the same signature, and its
exported types become SNGL types.

A `go:` import resolves through the Go module in the current directory, so
make the directory holding `todo.sngl` a module and put the package in it:

```
go.mod           module myapp
store/store.go
todo.sngl
```

```go
package store

import (
	"encoding/json"
	"os"
)

type Todo struct {
	Text string
	Done bool
}

const file = "todos.json"

func Load() []Todo {
	var todos []Todo
	if b, err := os.ReadFile(file); err == nil {
		json.Unmarshal(b, &todos)
	}
	return todos
}

func Save(todos []Todo) {
	if b, err := json.Marshal(todos); err == nil {
		os.WriteFile(file, b, 0o644)
	}
}
```

The list now holds the Go package's `Todo` rather than one declared in SNGL,
since that is the type `Load` returns and `Save` takes. Its fields keep their Go
names with the first letter lowercased: `Text` is `text`.

<!-- SNGL-nocheck -->

```sngl
import "go:myapp/store"
import "sngl:ui"

ui.window(title="Todos") {
    var newTodo = ""
    var todos list<store.Todo> = store.Load()

    effect(on=todos, @update(t) {
        store.Save(t)
    })

    ui.vbox(style={gap=12px, padding=16px}) {
        ui.hbox(style={gap=8px, alignItems=center}) {
            ui.input(:value=newTodo, placeholder="Buy eggs", style={flex=1})
            ui.button(text="Add", @click {
                todos.push(store.Todo{text=newTodo})
                newTodo = ""
            })
        }
        for var &item = todos {
            ui.checkbox(:checked=item.done, label=item.text)
        }
    }
}
```

An `effect` keyed on `todos` reports every write to it: `@update` runs after
the handler that wrote it -- the push, a checkbox toggling an item, a
replacement -- and is handed the list as it now is. So `store.Load` and
`store.Save` are ordinary calls at the two ends of its life. A `go:` import
compiles only for a Go target; on another target, use a persistence package for
that platform.

## Embed generated code in a host app

`sngl generate` writes a library — a package with no `main` — so the generated
code drops into a program you already have. These use the in-memory `todo.sngl`
from [Build a todo app](#build-a-todo-app). Pick a host:

```sngl mode=island
import "sngl:ui"

ui.hbox(style={gap=8px}) {
    ui.button(text="Go web server", @click { embedTarget = "html" })
    ui.button(text="Fyne", @click { embedTarget = "fyne" })
    ui.button(text="Android", @click { embedTarget = "kotlin" })
}
```

```sngl mode=island
import "sngl:ui/markup"

whenSelected(selected=embedTarget, value="html") {
    markup.codeBlock {
        markup.text(value="sngl generate todo.sngl --lang go --platform html -o ./ui")
    }
    markup.paragraph {
        markup.text(value="Your program is a Go module; the import path below assumes ")
        markup.monospace {
            markup.text(value="go mod init example.com/myapp")
        }
        markup.text(value=". Mount the handler in your own server, then run ")
        markup.monospace {
            markup.text(value="go mod tidy")
        }
        markup.text(value=" and ")
        markup.monospace {
            markup.text(value="go build")
        }
        markup.text(value=":")
    }
    markup.codeBlock {
        markup.text(value=`package main

import (
    "net/http"

    "example.com/myapp/ui"
)

func main() {
    http.Handle("/", ui.Handler())
    http.ListenAndServe(":8080", nil)
}`)
    }
}
```

```sngl mode=island
import "sngl:ui/markup"

whenSelected(selected=embedTarget, value="fyne") {
    markup.codeBlock {
        markup.text(value="sngl generate todo.sngl --lang go --platform fyne -o ./ui")
    }
    markup.paragraph {
        markup.text(value="Your program is a Go module; the import path below assumes ")
        markup.monospace {
            markup.text(value="go mod init example.com/myapp")
        }
        markup.text(value=". Put the generated UI in a window of your own Fyne app, then run ")
        markup.monospace {
            markup.text(value="go mod tidy")
        }
        markup.text(value=", which adds Fyne and the SNGL runtime package the generated code imports, and ")
        markup.monospace {
            markup.text(value="go build")
        }
        markup.text(value=":")
    }
    markup.codeBlock {
        markup.text(value=`package main

import (
    "fyne.io/fyne/v2/app"

    "example.com/myapp/ui"
)

func main() {
    a := app.New()
    w := a.NewWindow("Todos")
    w.SetContent(ui.New().BuildUI())
    w.ShowAndRun()
}`)
    }
}
```

```sngl mode=island
import "sngl:ui/markup"

whenSelected(selected=embedTarget, value="kotlin") {
    markup.codeBlock {
        markup.text(value="sngl generate todo.sngl --lang kotlin --platform android --opt package=com.example.app -o ./app/src/main/java/com/example/app")
    }
    markup.paragraph {
        markup.text(value="Then call the generated ")
        markup.monospace {
            markup.text(value="MainScreen")
        }
        markup.text(value=" composable from your Activity. The generated code uses Jetpack Compose with Material 3 and ")
        markup.monospace {
            markup.text(value="androidx.activity:activity-compose")
        }
        markup.text(value=", so the app module needs those dependencies and the Compose compiler plugin:")
    }
    markup.codeBlock {
        markup.text(value=`package com.example.app

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent { MainScreen() }
    }
}`)
    }
}
```

## Where next

That's a complete app touching every part of the language. To test it, move
the window's state into a component, since a component is what a test
instantiates: [Testing](/reference/effective-sngl.html#testing) shows how, and
`sngl test` runs the tests. Take the
interactive [Tour](/tutorial.html) for a guided walkthrough of more features,
read [Effective SNGL](/reference/effective-sngl.html) for the idioms, or jump to
the [Reference](/reference/index.html) for the full library, the language
specification, and the platform/language matrix.
