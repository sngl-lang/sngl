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

## Editor Support

SNGL includes a Tree-sitter grammar for syntax highlighting in editors that support it. The grammar is located in `editors/tree-sitter-sngl/`.

An LSP server is also available:

```bash
sngl lsp
```

Configure your editor to use `sngl lsp` as the language server for `.sngl` files.
