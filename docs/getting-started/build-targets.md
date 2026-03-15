---
title: "Build Targets"
order: 3
description: "Output declarations and platform targets"
---

## Output Declaration

The `output` block declares which platforms to compile for:

```sngl
output go bubbletea(package="main")
```

Or group multiple targets:

```sngl
output {
    go bubbletea(package="main")
    js html
}
```

A language with multiple platforms uses braces:

```sngl
output {
    js { html; node(ssr=true) }
}
```

### Available Targets

| Language | Platform | Description |
| --- | --- | --- |
| `go` | `bubbletea` | Terminal UI via BubbleTea |
| `js` | `html` | Web app with DOM rendering |

### CLI Flags

You can override the output target from the command line:

```bash
sngl compile --lang=go --platform=bubbletea todo.sngl
sngl compile --lang=js --platform=html todo.sngl
```

Options can be passed with `--opt`:

```bash
sngl compile --lang=go --platform=bubbletea --opt=package=main todo.sngl
```

### Output Directory

By default, generated files are written to the current directory. Use `--out` to specify a different location:

```bash
sngl compile --out=./output todo.sngl
```
