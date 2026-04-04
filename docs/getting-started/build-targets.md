---
title: "Build Targets"
order: 3
description: "Output declarations and platform targets"
---

## Output Declaration

The `output` block declares which language/platform combinations to compile for. Each language gets a block containing its target platforms:

<!-- SNGL-top
component main { text(value="") }
-->
```sngl
output {
    js { html }
}
```

Multiple languages and platforms:

<!-- SNGL-top
component main { text(value="") }
-->
```sngl
output {
    go {
        bubbletea
        fyne
    }
    js { html }
    kotlin { android }
}
```

Platform options are passed in parentheses:

<!-- SNGL-top
component main { text(value="") }
-->
```sngl
output {
    go { bubbletea(package="main") }
}
```

### Available Targets

| Language | Platform | Description |
| --- | --- | --- |
| `js` | `html` | Web app with inline JS and DOM rendering |
| `go` | `bubbletea` | Terminal UI via Charm's BubbleTea |
| `go` | `fyne` | Desktop GUI via the Fyne toolkit |
| `kotlin` | `android` | Android app via Jetpack Compose |

### CLI Flags

You can specify the target from the command line instead of using an `output` block:

```bash
sngl compile --lang go --platform fyne todo.sngl
sngl compile --lang js --platform html todo.sngl
sngl compile --lang kotlin --platform android todo.sngl
```

Options can be passed with `--opt`:

```bash
sngl compile --lang go --platform bubbletea --opt package=main todo.sngl
```

Run an app directly (implies `main=true`):

```bash
sngl run --lang go --platform fyne todo.sngl
sngl run --lang js --platform html todo.sngl
```

### Output Directory

By default, generated files are written to the current directory. Use `--out` to specify a different location:

```bash
sngl compile --out ./output todo.sngl
```

### Building for Distribution

The `build` command compiles to a runnable artifact:

```bash
sngl build todo.sngl
sngl build --lang kotlin --platform android --opt name="My App" todo.sngl
```
