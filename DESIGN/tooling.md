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
| `compile`  | Compile KDL to target artifacts     | See [compiler.md](compiler.md)               |
| `check`    | Type-check and validate (no output) | See [static-analysis.md](static-analysis.md) |
| `lint`     | Run lint rules                      | See [static-analysis.md](static-analysis.md) |
| `test`     | Run test suites                     | See [testing.md](testing.md)                 |
| `lsp`      | Start the language server           | See below                                    |
| `fmt`      | Format KDL source files             | See below                                    |
| `preview`  | Launch the WYSIWYG preview          | See [wysiwyg.md](wysiwyg.md)                 |
| `init`     | Initialize a new SNGL project       | See below                                    |
| `version`  | Print version info                  |                                              |

### Common Flags

These apply across subcommands where relevant:

| Flag               | Description                                              |
| ------------------ | -------------------------------------------------------- |
| `--project=<path>` | Project root (default: cwd, walks up to find `sngl.kdl`) |
| `--format=<fmt>`   | Output format: `text`, `json`, `sarif`                   |
| `--quiet`          | Suppress non-error output                                |
| `--verbose`        | Verbose/debug output                                     |

### Project Configuration

A project root is identified by a `sngl.kdl` configuration file:

```kdl
project "myapp"

targets {
    go-gio lang="go" platform="gio" out="gen/gio"
    web lang="ts" platform="web" out="gen/web"
}

lint {
    no-unused-bind "warn"
    no-missing-key "error"
    max-depth 10
}
```

`sngl init` creates this file along with a minimal directory structure.

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
- Source-mapped to KDL line/column
- Includes parse errors, type errors, resolution errors, and lint violations
- CEL expression errors reported at the `(cel)` annotation site

#### Hover

- Over a `bind`/`computed` identifier: show its type and current expression
- Over a component name: show its schema (properties, events, children policy)
- Over a `(cel)` expression: show the inferred return type
- Over a `style.*` property: show accepted values and Yoga mapping
- Over an `on:` event: show the event payload type

#### Go-to-Definition

- Component name → component declaration (user-defined) or standard library schema
- `bind`/`computed` reference in a `(cel)` expression → declaration site
- Handler reference → handler interface definition (if compiled output exists)
- `import` path → imported file

#### Find References

- Find all uses of a `bind`/`computed` variable across `(cel)` expressions
- Find all instantiations of a user-defined component
- Find all references to a handler

#### Autocomplete

- Component names (builtins + user-defined + imported)
- Property names for the current component (filtered by schema)
- `style.*` property names
- `on:` event names for the current component
- Inside `(cel)` expressions: identifiers from `bind`, `computed`, component params, `event` fields, and built-in CEL functions

#### Rename

- Rename a `bind`/`computed` variable across all `(cel)` references
- Rename a user-defined component across all instantiations
- Rename component parameters

#### Code Actions

- Extract inline `(cel)` expression to a `computed` declaration
- Add missing `key` attribute on `for` nodes
- Add missing required properties
- Wrap node in a container (`vbox`/`hbox`)

#### Formatting

Delegates to the same engine as `sngl fmt` (see below).

#### Workspace

- Watches for file changes and re-analyzes affected files incrementally
- Respects `sngl.kdl` project configuration for lint rules and target settings
- Multi-root workspace support

---

## Tree-Sitter Grammar

A tree-sitter grammar for SNGL KDL provides syntax highlighting, code folding, and structural queries for editors that support tree-sitter (Neovim, Helix, Zed, Emacs, VS Code via extensions).

### Scope

The grammar extends standard KDL parsing with SNGL-specific awareness:

- **KDL structure** — nodes, attributes, values, comments, type annotations
- **SNGL keywords** — `app`, `bind`, `computed`, `component`, `slot`, `import`, `style`
- **Attribute nodes** — `@`-prefixed child nodes (`@style`, `@accessibility`, etc.) recognized as structured metadata
- **Type annotations** — `(cel)` recognized as a distinct annotation type
- **Namespaced attributes** — `style.*` and `on:*` parsed as structured attribute names
- **CEL inner highlighting** — The string content of `(cel)"..."` values is injected with a CEL sub-grammar for expression-level highlighting

### Node Types

Key tree-sitter node types the grammar produces:

```
(document
  (declaration
    (bind_decl name: (identifier) type: (type_annotation)? value: (value))
    (computed_decl name: (identifier) type: (type_annotation)? expr: (cel_expr))
    (component_decl name: (identifier) params: (param_list)? body: (node_block))
    (import_decl path: (string))
    (style_decl name: (string) body: (node_block)))
  (app_node body: (node_block)))

(visual_node
  name: (identifier)
  (attribute name: (identifier) value: (value))
  (style_attribute name: (style_prop_name) value: (value))
  (event_attribute name: (event_name) handler: (cel_expr))
  (node_block ...))

(attribute_node
  name: (identifier)  // starts with @
  (attribute name: (identifier) value: (value))*
  (node_block ...)?)

(style_block  // inside @style attribute_node
  (style_property name: (identifier) value: (value))*)

(cel_expr (string))  // injected with CEL sub-grammar
(type_annotation)    // e.g., (cel), : int, : string
```

### CEL Injection

The `(cel)"..."` pattern is handled via tree-sitter's language injection mechanism. The outer grammar captures the string content; editors inject a CEL grammar for syntax highlighting within it:

```scheme
;; injections.scm
((cel_expr (string_content) @injection.content)
 (#set! injection.language "cel"))
```

This gives users full CEL highlighting (operators, string literals, function calls, field access) inside `(cel)` values.

### Highlight Queries

```scheme
;; highlights.scm
(bind_decl "bind" @keyword)
(computed_decl "computed" @keyword)
(component_decl "component" @keyword)
(import_decl "import" @keyword.import)
(app_node "app" @keyword)
"slot" @keyword

(bind_decl name: (identifier) @variable)
(computed_decl name: (identifier) @variable)
(component_decl name: (identifier) @type)
(visual_node name: (identifier) @tag)

(style_attribute name: (style_prop_name) @property)
(event_attribute name: (event_name) @attribute)

(attribute_node name: (identifier) @attribute)
(style_block (style_property name: (identifier) @property))

(type_annotation) @type
(cel_expr) @embedded
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

The `sngl` binary does not need to include the tree-sitter grammar — it is consumed by editors directly. However, `sngl init` can scaffold editor configuration files that reference it.

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
- Sorted attributes: `id`, `key`, `class`, then alphabetical, with `style.*` grouped and `on:*` last
- Normalized whitespace in `(cel)` string contents is not touched (CEL formatting is the author's responsibility)
- Preserves comments

### Configuration

Formatting preferences are set in `sngl.kdl`:

```kdl
fmt {
    indent 4
    max-line-width 100
    sort-attributes true
    align-equals false
}
```

---

## `sngl init`

```
sngl init [directory]
```

Creates:

```
<directory>/
    sngl.kdl            # project configuration
    app.kdl             # minimal starter template
    app.sample.kdl      # sample data for WYSIWYG preview
    app.test.kdl        # starter test file
```

With flags:

| Flag                | Description                              |
| ------------------- | ---------------------------------------- |
| `--lang=<lang>`     | Pre-configure a language target          |
| `--platform=<plat>` | Pre-configure a platform target          |
| `--bare`            | Only create `sngl.kdl`, no starter files |

---

## Editor Integration

### Neovim / Helix / Zed

These editors consume tree-sitter grammars natively. Integration requires:

1. Install `tree-sitter-sngl` grammar
2. Configure the LSP client to run `sngl lsp`
3. Highlighting, folding, and indentation work via tree-sitter queries

`sngl init --editor=neovim` can generate a `.nvim.lua` snippet for the project.

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
  "filetypes": ["kdl"],
  "root_markers": ["sngl.kdl"]
}
```

The LSP activates only for KDL files within a project that has a `sngl.kdl` root marker.
