---
title: "Your First App"
order: 2
description: "Build a todo app with SNGL"
---

## Todo App Walkthrough

This walks through a complete SNGL app — a todo list that compiles to both web and terminal targets.

### Define Your Types

Start by declaring the data structures your app needs:

```sngl
struct Todo {
    text string = ""
    done bool = false
}
```

Structs define the shape of your data. Each field has a name, type, and default value.

### Declare Output Targets

Tell the compiler which platforms to generate code for:

<!-- SNGL-top
component main { text(value="") }
-->
```sngl
output {
    go { bubbletea(package="main") }
    js { html }
}
```

This generates both a Go BubbleTea TUI app and a web app from the same source.

### Build the Component

Every SNGL app has a `component main` as its entry point:

```sngl
component main {
    var (
        newTodo = ""
        todos list<Todo> = []
    )

    func status() => "Todo List ({todos.length()} items)"

    vbox(style={gap=12, padding=16}) {
        text(value=status, style={fontWeight="bold", fontSize=24})
        hbox(style={gap=8, alignItems="center"}) {
            input(@input { newTodo = event.value }, placeholder="Buy eggs", style={flexGrow=1})
            button(@click {
                todos.push(Todo{text=newTodo, done=false})
                newTodo = ""
            }, text="Add")
        }
        vbox(style={gap=4}) {
            for item, index = todos {
                checkbox(checked=item.done, key=index, label=item.text, @change { todos[index].done!! })
            }
        }
    }
}
```

### Key Concepts in This Example

**Reactive state** — `var` declares mutable state. When `todos` changes, the `for` loop re-renders. When `newTodo` changes, the input stays in sync.

**Derived state** — `func status()` derives from `todos` automatically. Zero-arg functions are auto-invoked when referenced without `()`. No manual subscription needed.

**Event handlers** — `@click` and `@input` contain statement blocks that mutate state directly. `todos[index].done!!` is the toggle operator.

**String interpolation** — `"Todo List ({todos.length()} items)"` embeds expressions in strings.

**Inline styles** — `style={gap=12, padding=16}` applies layout properties. These map to Yoga/CSS flexbox.

### Compile

```bash
sngl compile todo.sngl
```

This generates platform-specific code in the output directory based on the `output` declaration.
