---
title: Getting Started
order: 20
description: Build a SNGL app from Hello World to a todo list
---

```sngl mode=package
import ui "sngl:ui"

var runPlatform = "html"
var generateLang = "go"
var embedLang = "go"

component whenSelected(selected string, value string, content ...component ui.node) ui.node {
    if selected == value {
        ui.vbox(style={gap=12}) {
            content
        }
    }
}
```

This tutorial walks through building a SNGL app from a one-line Hello World up
to a full todo list with persistence and code embedding. Every snippet is real
SNGL and type-checks; copy one into a file and run it. The only exceptions are
marked: a `// ...` elides code shown earlier, and the persistence example
imports a Go package you supply.

## Hello, World

A SNGL app is its windows, each the root of a tree of components. The smallest
app is one window holding a single text node:

```sngl
import . "sngl:ui"

window {
    text(value="Hello, World!")
}
```

Save it as `hello.sngl` and run:

```bash
sngl run hello.sngl
```

## Reactive state and binding

State is declared with `var` (mutable) or zero-arg `func` (derived). The
`:value` prefix on a prop is two-way binding: changes flow both ways.

```sngl
import . "sngl:ui"

window {
    var name = "World"
    func greeting() => "Hello, {name}!"
    vbox(style={gap=8, padding=16}) {
        text(value=greeting)
        input(:value=name)
    }
}
```

When the user types in the input, `name` updates, `greeting` recomputes, and
the text re-renders. No subscriptions to wire up.

## Run it on any platform

Pick a target. The same source compiles to all of them:

```sngl mode=island
import ui "sngl:ui"

ui.hbox(style={gap=8}) {
    ui.button(text="HTML", @click { runPlatform = "html" })
    ui.button(text="BubbleTea (TUI)", @click { runPlatform = "bubbletea" })
    ui.button(text="Fyne (Desktop)", @click { runPlatform = "fyne" })
    ui.button(text="Android", @click { runPlatform = "android" })
}
```

```sngl mode=island
import markup "sngl:ui/markup"

whenSelected(selected=runPlatform, value="html") {
    markup.codeBlock {
        markup.text(value="sngl run hello.sngl --platform html")
    }
    markup.paragraph {
        markup.text(value="Starts a local HTTP server (default :8080) serving a static page. Edit-and-refresh — no rebuild step.")
    }
}
```

```sngl mode=island
import markup "sngl:ui/markup"

whenSelected(selected=runPlatform, value="bubbletea") {
    markup.codeBlock {
        markup.text(value="sngl run hello.sngl --platform bubbletea")
    }
    markup.paragraph {
        markup.text(value="Compiles to a Bubbletea program and runs it inline in your terminal. Quit with Ctrl-C.")
    }
}
```

```sngl mode=island
import markup "sngl:ui/markup"

whenSelected(selected=runPlatform, value="fyne") {
    markup.codeBlock {
        markup.text(value="sngl run hello.sngl --platform fyne")
    }
    markup.paragraph {
        markup.text(value="Builds a native desktop window via Fyne. Requires the OS GUI toolchain (XQuartz on macOS, etc.).")
    }
}
```

```sngl mode=island
import markup "sngl:ui/markup"

whenSelected(selected=runPlatform, value="android") {
    markup.codeBlock {
        markup.text(value="sngl run hello.sngl --platform android")
    }
    markup.paragraph {
        markup.text(value="Generates an Android Compose project, builds the APK, and installs it to the connected device or emulator.")
    }
}
```

## Generate code you can wrap

`sngl run` is for iteration. To produce code you can ship inside a larger
project, use `sngl generate`. Pick a host language:

```sngl mode=island
import ui "sngl:ui"

ui.hbox(style={gap=8}) {
    ui.button(text="Go", @click { generateLang = "go" })
    ui.button(text="JavaScript", @click { generateLang = "js" })
    ui.button(text="Kotlin", @click { generateLang = "kotlin" })
}
```

```sngl mode=island
import markup "sngl:ui/markup"

whenSelected(selected=generateLang, value="go") {
    markup.codeBlock {
        markup.text(value="sngl generate hello.sngl --lang go --platform html")
    }
    markup.paragraph {
        markup.text(value="Emits a Go package containing the rendered HTML, an ")
        markup.monospace {
            markup.text(value="http.Handler")
        }
        markup.text(value=", and any data structs. Drop it into your existing Go project and call ")
        markup.monospace {
            markup.text(value="NewHandler()")
        }
        markup.text(value=".")
    }
}
```

```sngl mode=island
import markup "sngl:ui/markup"

whenSelected(selected=generateLang, value="js") {
    markup.codeBlock {
        markup.text(value="sngl generate hello.sngl --lang js --platform html")
    }
    markup.paragraph {
        markup.text(value="Emits a static ")
        markup.monospace {
            markup.text(value="index.html")
        }
        markup.text(value=" plus inline JS for the reactive runtime. Host on any static server.")
    }
}
```

```sngl mode=island
import markup "sngl:ui/markup"

whenSelected(selected=generateLang, value="kotlin") {
    markup.codeBlock {
        markup.text(value="sngl generate hello.sngl --lang kotlin --platform android")
    }
    markup.paragraph {
        markup.text(value="Emits a Compose package — components, view models, and a ")
        markup.monospace {
            markup.text(value="Root")
        }
        markup.text(value=" composable you can mount inside an existing Android Activity.")
    }
}
```

## Pre-configure with `output`

Repeating `--lang` and `--platform` flags is tedious. An `output` block in the
source declares the targets up front; bare `sngl run` and `sngl generate` then
pick them up automatically.

<!-- SNGL-top
import . "sngl:ui"

window { text(value="") }
-->

```sngl
output {
    js { html }
    go { bubbletea(package="main") }
    kotlin { android }
}
```

Each language–platform pair in the block becomes one build artifact. Same
source, three outputs.

## Build a todo app

Now the full thing: a list of todos with an input field, an add button, and a
checkbox per item. Walk through it section by section — every line is part of
the running app.

```sngl
import . "sngl:ui"

struct Todo {
    text string = ""
    done bool = false
}

window {
    var (
        newTodo = ""
        todos list<Todo> = []
    )
    func status() => "Todo List ({todos.length()} items)"

    vbox(style={gap=12, padding=16}) {
        text(value=status, style={fontWeight=bold, fontSize=24})
        hbox(style={gap=8, alignItems=center}) {
            input(:value=newTodo, placeholder="Buy eggs", style={flex=1})
            button(text="Add", @click {
                todos.push(Todo{text=newTodo, done=false})
                newTodo = ""
            })
        }
        vbox(style={gap=4}) {
            for var index, item = todos {
                checkbox(
                    checked=item.done,
                    label=item.text,
                    @change { todos[index].done!! },
                )
            }
        }
    }
}
```

`struct Todo` is the data shape. `var todos list<Todo> = []` starts empty and
is what the UI iterates. `func status()` is derived — the count updates
whenever `todos` changes.

`@click` is an event handler running a statement block; `todos.push(...)`
mutates the bound list, which triggers a rerender. The trailing `done!!` in the
checkbox is the toggle operator — flips a bool in place.

## Persist state with a Go import

The Todo list lives in memory. To save it across runs, import a Go package —
any type-safe Go function becomes a SNGL function with the same signature.

<!-- SNGL-nocheck -->

```sngl
import . "sngl:ui"
import "go:myapp/store"

window {
    var todos list<Todo> = store.Load() @change {
        store.Save(todos)
    }
    // ... rest of UI as before
}
```

`@change` is a data event on the variable: it re-runs whenever `todos` changes.
Any exported Go function becomes a SNGL function with the same signature, so
`store.Load` and `store.Save` are ordinary calls. The import only compiles when
the host language is Go; on other platforms, swap in a platform-appropriate
persistence package.

## Embed generated code in a host app

The generator can omit a `main()` so the output is a library, not an app. Pick
a host language:

```sngl mode=island
import ui "sngl:ui"

ui.hbox(style={gap=8}) {
    ui.button(text="Go", @click { embedLang = "go" })
    ui.button(text="Kotlin (Android)", @click { embedLang = "kotlin" })
}
```

```sngl mode=island
import markup "sngl:ui/markup"

whenSelected(selected=embedLang, value="go") {
    markup.codeBlock {
        markup.text(value="sngl generate todo.sngl --lang go --platform html --opt main=false -o ./generated")
    }
    markup.paragraph {
        markup.text(value="Then write your own ")
        markup.monospace {
            markup.text(value="main")
        }
        markup.text(value=" that mounts the handler:")
    }
    markup.codeBlock {
        markup.text(value=`package main

import (
    "net/http"

    sngl "./generated"
)

func main() {
    http.Handle("/", sngl.NewHandler())
    http.ListenAndServe(":8080", nil)
}`)
    }
}
```

```sngl mode=island
import markup "sngl:ui/markup"

whenSelected(selected=embedLang, value="kotlin") {
    markup.codeBlock {
        markup.text(value="sngl generate todo.sngl --lang kotlin --platform android --opt main=false -o ./app/src/main/sngl")
    }
    markup.paragraph {
        markup.text(value="Then attach the generated ")
        markup.monospace {
            markup.text(value="Root")
        }
        markup.text(value=" composable in your existing Android ")
        markup.monospace {
            markup.text(value="Application")
        }
        markup.text(value=":")
    }
    markup.codeBlock {
        markup.text(value=`package com.example.todo

import android.app.Application
import sngl.generated.Root

class TodoApp : Application() {
    override fun onCreate() {
        super.onCreate()
        Root.attach(this)
    }
}`)
    }
}
```

## Where next

That's a complete app touching every part of the language. Take the
interactive [Tour](/tutorial.html) for a guided walkthrough of more features,
or jump to the [Reference](/reference/index.html) for the full standard
library, language spec, and platform/language matrices.
