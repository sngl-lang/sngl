---
title: Installation
order: 1
description: How to install the SNGL CLI
---

## Download Prebuilt Binaries

Prebuilt binaries are available for common platforms. Download the `.gz` for
your system, decompress it (`gunzip sngl-*.gz`), make it executable
(`chmod +x sngl-*`), rename it to `sngl`, and place it on your `PATH`.

<!-- sngl:downloads -->

## Install from Source

Building from source requires Go 1.26 or later.

```bash
git clone https://git.duckfam.us/jonathan/sngl.git
cd sngl
go install ./cmd/sngl
```

This puts `sngl` in `$(go env GOPATH)/bin`, which should be on your `PATH`.

## Verify Installation

```bash
sngl version
```

## What Each Target Needs

The `sngl` binary type-checks and generates code for every target on its own.
Building or running what it generates needs that target's toolchain:

| Platform    | Language             | Needs                                                                  |
|-------------|----------------------|------------------------------------------------------------------------|
| `html`      | `none` (the default) | nothing: the output is static HTML and JavaScript                      |
| `html`      | `go`                 | Go, to build the generated HTTP server                                 |
| `bubbletea` | `go`                 | Go                                                                     |
| `fyne`      | `go`                 | Go with cgo, a C compiler, and the OpenGL and X11 development headers  |
| `gtk4`      | `go`                 | Go with cgo, a C compiler, and the GTK 4 development files             |
| `android`   | `kotlin` or `go`     | JDK 17–23 and the Android SDK (compileSdk 35); `go` also uses gomobile |

On Debian or Ubuntu, the desktop targets need:

```bash
# fyne
sudo apt install gcc libgl-dev libx11-dev libxcursor-dev libxrandr-dev \
    libxinerama-dev libxi-dev libxxf86vm-dev
# gtk4
sudo apt install gcc libgtk-4-dev gir1.2-gtk-4.0
```

The gtk4 platform reads its widget set from the system's `Gtk-4.0.gir`. Without
one it falls back to a bundled subset of the widgets SNGL wraps, which is enough
to check and generate a program; building it still needs GTK 4 installed.

## Editor Support

An LSP server provides diagnostics, hover, and completion:

```bash
sngl lsp
```

The repository carries editor integrations that start it for `.sngl` files:

- `editors/vscode/` — a VS Code extension with syntax highlighting and LSP setup.
- `editors/neovim/` — a Neovim plugin with Tree-sitter highlighting and LSP setup.
- `editors/tree-sitter-sngl/` — the Tree-sitter grammar, for any editor that
  supports one.

For any other editor, configure `sngl lsp` as the language server for `.sngl`
files.
