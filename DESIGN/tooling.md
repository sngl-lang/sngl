# SNGL Tooling — Design Document

## Purpose

All SNGL tools ship as subcommands of the single `sngl` binary. This document covers the unified CLI, the language server (LSP), the tree-sitter grammar, and editor integration.

## The `sngl` Binary

Every tool is a subcommand. No separate binaries.

```
sngl <subcommand> [flags] [args]
```

### Subcommands

| Subcommand | Description                         | Details                                      |
| ---------- | ----------------------------------- | -------------------------------------------- |
| `compile`  | Compile SNGL to target artifacts    | See [compiler.md](compiler.md)               |
| `check`    | Type-check and validate (no output) | See [static-analysis.md](static-analysis.md) |
| `lint`     | Run lint rules                      | See [static-analysis.md](static-analysis.md) |
| `test`     | Run test suites                     | See [testing.md](testing.md)                 |
| `lsp`      | Start the language server           | See below                                    |
| `fmt`      | Format SNGL source files            | See below                                    |
| `preview`  | Launch the WYSIWYG preview          | See [wysiwyg.md](wysiwyg.md)                 |
| `version`  | Print version info                  |                                              |

### Common Flags

These apply across subcommands where relevant:

| Flag             | Description                            |
| ---------------- | -------------------------------------- |
| `--format=<fmt>` | Output format: `text`, `json`, `sarif` |
| `--quiet`        | Suppress non-error output              |
| `--verbose`      | Verbose/debug output                   |

---

## Language Server (LSP)

### Starting

```
sngl lsp
```

The LSP communicates over stdio (default) or TCP (`--tcp=:port`). Editors launch it automatically via their SNGL plugin configuration.

### Capabilities

The LSP builds on the compiler's parse and analyze pipeline, keeping the AST warm in memory for incremental updates.

#### Diagnostics

- Real-time errors and warnings as the user types
- Source-mapped to SNGL line/column
- Includes parse errors, type errors, resolution errors, and lint violations

#### Hover

- Over a `var`/`computed` identifier: show its type and current expression
- Over a component name: show its schema (properties, events, children policy)
- Over an expression: show the inferred return type
- Over a `style` property: show accepted values and Yoga mapping
- Over an `@event`: show the event payload type

#### Go-to-Definition

- Component name → component declaration (user-defined) or standard library schema
- `var`/`computed` reference in an expression → declaration site
- Handler reference → handler interface definition (if compiled output exists)
- `import` path → imported file

#### Find References

- Find all uses of a `var`/`computed` variable across expressions
- Find all instantiations of a user-defined component
- Find all references to a handler

#### Autocomplete

- Component names (builtins + user-defined + imported)
- Property names for the current component (filtered by schema)
- `style` property names
- `@event` names for the current component
- Inside expressions: identifiers from `var`, `computed`, component params, `event` fields, and built-in functions

#### Rename

- Rename a `var`/`computed` variable across all references
- Rename a user-defined component across all instantiations
- Rename component parameters

#### Code Actions

- Extract inline expression to a `computed` declaration
- Add missing `key` attribute on `for` nodes
- Add missing required properties
- Wrap node in a container (`vbox`/`hbox`)

#### Formatting

Delegates to the same engine as `sngl fmt` (see below).

#### Workspace

- Watches for file changes and re-analyzes affected files incrementally
- Multi-root workspace support

---

## Tree-Sitter Grammar

A tree-sitter grammar for SNGL provides syntax highlighting, code folding, and structural queries for editors that support tree-sitter (Neovim, Helix, Zed, Emacs, VS Code via extensions).

### Scope

The grammar covers the full SNGL language:

- **Document structure** — top-level declarations, components, visual nodes
- **SNGL keywords** — `component`, `var`, `computed`, `const`, `import`, `style`, `if`, `for`, `test`
- **Expressions** — arithmetic, comparison, logical, ternary, field access, method calls, literals
- **Statement blocks** — event handlers (`@click={ stmts }`), assignment, toggle, emit
- **Attribute nodes** — `@name(props)` inside children blocks
- **String interpolation** — `{expr}` inside string literals

### Node Types

Key tree-sitter node types the grammar produces:

```
(source_file
  (import_declaration path: (string_literal))
  (component_declaration name: (identifier)
    (var_declaration name: (identifier) value: (expression))
    (computed_declaration name: (identifier) value: (expression))
    (visual_node name: (identifier)
      (prop name: (identifier) value: (expression))
      (event_handler name: (identifier) body: (statement_block))
      (visual_node ...)))
  (test_declaration component: (identifier) description: (string_literal)
    (statement)*))

(expression
  (binary_expression) (unary_expression) (ternary_expression)
  (method_expression) (field_expression) (index_expression)
  (call_expression) (identifier) (string_literal) (integer_literal) ...)
```

### Highlight Queries

```scheme
;; highlights.scm
"component" @keyword
"var" @keyword
"computed" @keyword
"const" @keyword
"import" @keyword.import
"if" @keyword
"for" @keyword
"in" @keyword
"test" @keyword

(component_declaration name: (identifier) @type)
(var_declaration name: (identifier) @variable)
(computed_declaration name: (identifier) @variable)
(visual_node name: (identifier) @tag)
(event_handler name: (identifier) @attribute)
```

### Folds and Indents

```scheme
;; folds.scm
(node_block) @fold

;; indents.scm
(node_block) @indent
```

### Distribution

The tree-sitter grammar ships as:

- A `tree-sitter-sngl` package (npm + crates.io + Go module)
- Pre-built WASM binaries for web-based editors
- Bundled queries (highlights, folds, indents, injections) in the package

The `sngl` binary does not need to include the tree-sitter grammar — it is consumed by editors directly.

---

## Formatter (`sngl fmt`)

```
sngl fmt [files...]                 # format in place
sngl fmt --check [files...]         # check only, exit 1 if unformatted
sngl fmt --stdin                    # read from stdin, write to stdout
```

### Rules

- Consistent indentation (spaces, configurable width, default 4)
- One attribute per line when a node exceeds a line width threshold
- Aligned `=` signs within attribute groups (optional, off by default)
- Sorted props: `id`, `key`, `class`, then alphabetical, with `style` grouped and `@events` last
- Preserves comments

---

## Editor Integration

### Neovim / Helix / Zed

These editors consume tree-sitter grammars natively. Integration requires:

1. Install `tree-sitter-sngl` grammar
2. Configure the LSP client to run `sngl lsp`
3. Highlighting, folding, and indentation work via tree-sitter queries

### VS Code

A VS Code extension wraps:

- The tree-sitter grammar (via WASM) for syntax highlighting
- The LSP client pointing to `sngl lsp`
- A preview pane that launches `sngl preview` in an embedded webview (if the web platform is available)

### General LSP Clients

Any editor with LSP support can use `sngl lsp`. The minimum configuration is:

```json
{
  "command": ["sngl", "lsp"],
  "filetypes": ["sngl"]
}
```
