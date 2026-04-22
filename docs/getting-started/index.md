---
title: "Getting Started"
order: 1
description: "Learn the basics of SNGL"
---

## Overview

SNGL projects consist of `.sngl` source files that describe reactive UIs. The `sngl` CLI compiles these files to platform-specific code.

Every SNGL app must have exactly one `component main` — this is the root of the UI tree. Component main acts as the entrypoint into your GUI. Your app may also contain one or more windows which can serve as alternate entrypoints on some platforms (eg: web URLs)

## Core Concepts

### Components

Components are the building blocks of SNGL UIs. They encapsulate state, layout, and behavior:

```sngl
component Counter(label = "") {
    var count = 0
    hbox {
        text(value="{label}: {count}")
        button(text="+", @click { count++ })
        button(text="-", @click { count-- })
    }
}
```

### Reactive State

State is declared with `var` (mutable) and zero-arg `func` (derived):

```sngl
component main {
    var name = "World"
    func greeting() => "Hello, {name}!"
    vbox {
        text(value=greeting)
        input(:value=name)
    }
}
```

The `:value` syntax defines a two-way binding, so input may use and update the value name. Changes to `name` automatically update `greeting` and any UI that references either.

### Visual Nodes

SNGL provides a standard set of common primitive components:

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

These are the essentials. The standard library ships many more (accordion, avatar, badge, card, drawer, menu, modal, popover, progress, radio, select, spinner, tabs, textarea, toggle, toolbar, tooltip, tree, and others) — see `internal/checker/stdlib/components.sngl` for the full list.

Additionally users may implement their own components, often with a few lines of code, to use platform components not available through the standard library.
