---
title: Installation
order: 1
description: How to install the SNGL CLI
---

## Prerequisites

- Go 1.26 or later

## Install from Source

```bash
go install git.duckfam.us/jonathan/sngl/cmd/sngl@latest
```

## Verify Installation

```bash
sngl version
```

## Download Prebuilt Binaries

Prebuilt binaries are available for common platforms. Download the `.gz` for
your system, decompress it (`gunzip sngl-*.gz`), make it executable
(`chmod +x sngl-*`), rename it to `sngl`, and place it on your `PATH`.

<!-- sngl:downloads -->

## Editor Support

SNGL includes a Tree-sitter grammar for syntax highlighting in editors that support it. The grammar is located in `editors/tree-sitter-sngl/`.

An LSP server is also available:

```bash
sngl lsp
```

Configure your editor to use `sngl lsp` as the language server for `.sngl` files.
