# Compiler Macros — Design Spec

**Date:** 2026-06-08
**Status:** Approved
**Related:** GitLab #66

## Overview

Compiler macros let built-in packages transform declarations before or after type-checking. The initial iteration ships compiler-defined macros only — no user-defined macro authorship. This gives the language a macro invocation mechanism and a concrete use-case (`#[canvas.shape]`) without solving the harder problems of hygiene, staging, and user-facing macro type signatures.

User-defined macros are a planned follow-on: the invocation syntax and expand pipeline are designed to be forward-compatible with them.

## Syntax

`#[macroName(args)]` on its own line before a declaration. Arguments are SNGL expressions (literals, strings, idents); the handler validates them.

```sngl
import "internal://canvas"

#[canvas.shape]
component rect(x Length, y Length, w Length, h Length, style CanvasStyle) {}

#[deprecated("use newButton")]
#[canvas.shape]
component oldRect(...) {}

import "internal://storage"

#[storage.persist("theme")]
var theme = "light"
```

Multiple attributes stack top-to-bottom; each receives the output of the prior. The macro name is always qualified: `alias.name`, where `alias` is the import alias of an `internal://` package.

`#[` is unambiguous from existing `#` uses:
- Color literals: `#RRGGBB` — next token is a hex digit
- Node IDs: `#name` — next token is a letter (no `[`)

### Applicable declarations

All declaration kinds: `component`, `func`, `var`, `const`, `struct`, `enum`. The macro handler is responsible for rejecting inapplicable kinds with a clear error.

## Import Resolution Split

The `internal://` scheme handler currently lives in `checker.registerImport`. It is extracted into a new `internal/imports` package with two levels:

**Light layer** (used by pre-check expand):
```go
// ResolveAliases scans import declarations and returns alias → ImportRef
// for each import. No IR building; scheme/uri only.
func ResolveAliases(docs []*ast.Document) map[string]ImportRef

type ImportRef struct {
    Scheme string // e.g. "internal"
    URI    string // e.g. "canvas"
}
```

**Full layer** (used by checker): existing `registerImport` logic refactored to call the light layer internally. Checker behavior is unchanged.

## Expand Pass Architecture

New package `internal/expand/` with three files.

### registry.go

```go
func RegisterPre(internalURI, name string, h PreHandler)
func RegisterPost(internalURI, name string, h PostHandler)

type MacroAttr struct {
    Name string
    Args []ast.Expr
}

type PreHandler  func(attr MacroAttr, decl ast.Decl) (ast.Decl, error)
type PostHandler func(attr MacroAttr, decl ir.Decl) (ir.Decl, error)
```

Macro packages register via `init()`, same pattern as codegen platform/lang registration.

### pre.go — Pre-check expansion

Runs immediately after `Parse`, before `Check`.

1. Call `imports.ResolveAliases(docs)` to build alias→`ImportRef` map
2. Walk every declaration in each document
3. For each `#[alias.name(args)]` attribute on the declaration:
   - Look up `alias` in the alias map
   - If `alias` resolves to an `internal://` URI, look up `name` in the registered `PreHandler`s
   - If `name` is unregistered for that URI → `ERROR(expand) "unknown macro name"`
   - If `alias` resolves to a non-internal import → silently skip (user-defined macros, future)
   - If `alias` is unresolved → `ERROR(expand) "unresolved import alias"`
4. Invoke the handler; on error → `ERROR(expand)` at the attribute position
5. Apply handlers top-to-bottom; each receives the output of the prior
6. Post-handler consistency check (see below)

### post.go — Post-check expansion

Runs after `Check`, before `Optimize`. Same dispatch model but operates on `ir.Decl` instead of `ast.Decl`. Has access to resolved types.

## Consistency Checks

Applied after each handler invocation.

**Pre-check (AST):**
- Declaration kind must not change — a `ComponentDecl` handler must return a `ComponentDecl`
- Attributes on the returned node are re-queued (allows macros to inject attributes for subsequent macros)

**Post-check (IR):**
- Param types and return type must not change
- Injected IR nodes must pass lightweight well-formedness validation

## Error Phase

New `expand` phase in test fixtures, consistent with existing `parse` and `check`:

```sngl
#[canvas.shape]
var bad = 5  // ERROR(expand) "shape macro requires a component declaration"
```

## Pipeline

`sngl.go` public API gains two new steps:

```
Parse → expand.Pre → Check → expand.Post → Optimize → Lower → Codegen
```

`expand.Pre` and `expand.Post` are no-ops if no macros are registered or no `#[...]` attributes appear in the source. Existing callers passing only parsed docs through `Check` are unaffected unless they opt in to the expand steps.

## First Built-in: `internal://canvas`

The concrete driver for this iteration. Registered from a new `internal/macros/canvas/` package (or alongside the canvas stdlib implementation).

```sngl
import "internal://canvas"

#[canvas.shape]
component rect(x Length, y Length, w Length, h Length, style CanvasStyle) {}
```

The `canvas.shape` pre-check handler:

1. Rejects non-`ComponentDecl` declarations → `ERROR(expand) "shape macro requires a component declaration"`
2. Changes the component body type from `list<component>` to `list<shape>`
3. Validates no disallowed props:
   - Any `list<component>` body (shapes may not have component children; only `list<shape>` bodies are valid after transformation)
   - DOM event handlers (`@click`, `@focus`, etc.) — `ERROR(expand) "shape components do not support DOM events"`
4. Attaches metadata tag `Shape: true` to the `ComponentDecl`, readable by the `passCanvas` lowering pass

`internal://canvas` is the only `internal://` macro package in this iteration. Canvas2D shapes and the `passCanvas` lowering pass are specified separately.

## Out of Scope (This Iteration)

- User-defined macro functions (the invocation syntax is forward-compatible)
- Post-check `#[storage.persist]` or `#[animation.*]` macros
- Macro-generated declarations (macros may only transform the declaration they annotate)
- Hygiene / gensym for macro-injected identifiers
