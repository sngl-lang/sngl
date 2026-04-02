---
title: "Getting Started"
order: 1
description: "Learn the basics of SNGL"
---

## Overview

SNGL projects consist of `.sngl` source files that describe reactive UIs. The `sngl` CLI compiles these files to platform-specific code.

A typical SNGL file contains:

- **Output declarations** — which platforms to target
- **Type definitions** — structs, enums, and units
- **Components** — the UI tree with state, layout, and event handlers

## Project Structure

```
myapp/
  myapp.sngl          # main UI definition
  output/             # generated code (per target)
```

Every SNGL app must have exactly one `component main` — this is the root of the UI tree.

## Core Concepts

### Components

Components are the building blocks of SNGL UIs. They encapsulate state, layout, and behavior:

```sngl
component Counter {
    param label = ""
    var count = 0

    hbox {
        text(value="{label}: {count}")
        button(text="+", @click={ count += 1 })
        button(text="-", @click={ count -= 1 })
    }
}
```

### Reactive State

State is declared with `var` (mutable) and `computed` (derived):

```sngl
component main {
    var name = "World"
    computed greeting = "Hello, {name}!"

    vbox {
        text(value=greeting)
        input(@input={ name = event.value })
    }
}
```

Changes to `name` automatically update `greeting` and any UI that references either.

### Visual Nodes

SNGL provides a standard set of primitive components:

| Component  | Purpose                   |
| ---------- | ------------------------- |
| `vbox`     | Vertical flex container   |
| `hbox`     | Horizontal flex container |
| `stack`    | Overlapping container     |
| `text`     | Text display              |
| `button`   | Clickable button          |
| `input`    | Text input                |
| `checkbox` | Toggle checkbox           |
| `image`    | Image display             |
| `scroll`   | Scrollable container      |
| `spacer`   | Flexible space            |
