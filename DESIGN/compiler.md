# SNGL Compiler — Design Document

## Purpose

The SNGL compiler transforms `.sngl` source documents into target-specific artifacts. A single SNGL source file describes a language- and platform-agnostic GUI layout; the compiler pairs a **language backend** with a **platform backend** to produce runnable output for any supported combination.

## Compilation Model

```
SNGL Source
    │
    ▼
┌──────────┐
│  Parser  │  SNGL → AST (layout tree + state + expressions)
└────┬─────┘
     ▼
┌──────────────┐
│   Checker    │  Type-check expressions, validate component schemas
└────┬─────────┘
     ▼
┌──────────────┐
│  Optimizer   │  Constant folding, dead code elimination (PLATFORM/LANGUAGE)
└────┬─────────┘
     ▼
┌────────────────────────────────┐
│  Language Translator +         │  AST → target artifacts
│  Platform Generator            │
└────────────────────────────────┘
```

### Phases

1. **Parse** — Read SNGL source, produce a typed AST via recursive-descent parser. Nodes, props, state declarations, and expressions (including string interpolation) are all captured as SNGL AST nodes. Event handlers (`@event={ stmts }`) are parsed as statement blocks. Numeric overflows and unterminated strings are rejected at parse time.
2. **Check** — Resolve component references (builtins + user-defined + imported), type-check properties and state against component schemas, validate SNGL expressions against a custom type system with scoped variable tracking. Broken string interpolation is rejected.
3. **Optimize** — Evaluate compile-time constants (`PLATFORM`, `LANGUAGE`), fold constant expressions (binary, ternary, interpolation), eliminate dead `if` branches and empty `for` loops. The optimizer walks the SNGL AST directly — no external expression engine.
4. **Generate** — A language translator and platform generator collaborate to produce final artifacts. Language translators convert SNGL expressions to target syntax; platform generators produce complete output files.

## Targets

A **target** is a (language, platform) pair. Examples:

| Language   | Platform  | Output                                           |
| ---------- | --------- | ------------------------------------------------ |
| Go         | Gio       | Go package using Gio widgets + Yoga layout       |
| Go         | GTK       | Go package using gotk4 bindings                  |
| Go         | bubbletea | Go package using charmbracelet bubbletea         |
| TypeScript | Web       | ES module with DOM operations                    |
| Kotlin     | Android   | Kotlin source using Jetpack Compose              |
| Swift      | iOS       | Swift source using SwiftUI                       |
| none       | none      | Analysis-only (static analysis, WYSIWYG preview) |

The `none/none` target performs all phases except emission and is used by the static analysis and WYSIWYG tools.

## Language Translators

A language translator (`codegen.LangTranslator`) is responsible for:

- **Expression translation** — Convert SNGL expression AST nodes into equivalent expressions in the target language (e.g., `int()` → `Math.trunc()` in JS, `int()` in Go; `float()` → `parseFloat()` in JS, `float64()` in Go).
- **Mutation translation** — Convert assignment, toggle, push/remove, and emit statements to target syntax.
- **Literal translation** — Format SNGL literal values in target syntax.
- **Type mapping** — Map SNGL type hints to native types (`TypeToNative`).
- **Name export** — Convert SNGL identifiers to target conventions (`ExportName` — capitalize for Go, identity for JS).

Translators register via `init()` and are looked up by name at runtime. Current implementations: `js` (JavaScript) and `go` (Go).

### Expression Translation

SNGL expressions are Go-like and handle simple logic: conditional text, computed properties, validation predicates. The language translator walks SNGL AST nodes (`*ast.BinaryExpr`, `*ast.CallExpr`, `*ast.InterpolationExpr`, etc.) and produces target-language source strings. Scope context (`codegen.ExprScope`) tracks which identifiers are model fields, computed fields, local variables, or renames.

### Handler Delegation

Complex logic (network calls, state machines, business rules) lives in `extern` functions. The SNGL source references named handlers:

```sngl
button(text="Submit", @click={ save(formData) })
```

The compiler generates a typed handler interface in the target language:

```go
// Generated
type Handlers interface {
    SubmitForm(formData *FormData) error
}
```

The application author provides the implementation.

## Platform Backends

A platform backend is responsible for:

- **Component mapping** — Map SNGL standard library components (`vbox`, `button`, `text`, etc.) to platform-native widgets or drawing calls.
- **Layout engine integration** — Wire up Yoga layout nodes to the platform's rendering pipeline.
- **Event system** — Map SNGL event names (`@click`, `@input`) to platform event mechanisms.
- **Lifecycle** — Handle mount, update, and teardown in a platform-idiomatic way.

## Standard Library

The standard library defines a set of components that every platform backend must implement:

- Layout: `vbox`, `hbox`, `stack`, `scroll`, `spacer`
- Content: `text`, `image`
- Input: `button`, `input`

Each component has a schema: typed properties, supported events, and layout behavior. Platform backends map these to native equivalents. Additional platform-specific components can be registered but are not portable.

## Compiler IR

The IR is a flat list of operations that fully describe the UI construction and reactive wiring:

- `CreateNode(id, type, props)` — instantiate a component
- `SetProp(id, prop, value | expr)` — set a static or bound property
- `AppendChild(parent, child)` — build the tree
- `Subscribe(signal, handler)` — wire a reactive update
- `BindEvent(id, event, handler)` — attach an event handler
- `SetLayout(id, yogaProps)` — configure layout constraints

This IR is what language/platform backends consume.

## Compiler CLI

```
sngl compile --lang=go --platform=gio --out=./gen/ app.sngl
sngl compile --lang=ts --platform=web --out=./gen/ app.sngl
sngl check app.sngl                          # analyze only (none/none)
```

## Multi-Target Compilation

A single SNGL source can be compiled to multiple targets. The parse and analyze phases run once; only IR lowering and emission are repeated per target. Targets are declared in `output` blocks:

```sngl
output {
    go gio(out="gen/gio")
    ts web(out="gen/web")
}
```

## Error Reporting

Errors reference SNGL source locations (file, line, column). Categories:

- **Parse errors** — malformed SNGL syntax
- **Resolution errors** — unknown component or property names
- **Type errors** — expression type mismatches, incompatible state bindings
- **Dependency errors** — circular computed values, missing handler definitions
- **Platform errors** — use of unsupported component/property for a given platform

## Extension Points

- **Custom components** — Users register component schemas; platform backends provide implementations.
- **Custom language backends** — Implement the language backend interface to add a new target language.
- **Custom platform backends** — Implement the platform backend interface to add a new target platform.
- **Compiler plugins** — Transform the IR before emission (optimization, instrumentation, accessibility annotations).
