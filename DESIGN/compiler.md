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

| Language | Platform  | Output                                           | Status      |
| -------- | --------- | ------------------------------------------------ | ----------- |
| JS       | html      | Single `index.html` with inline JS               | Implemented |
| Go       | bubbletea | Go TUI package using Charm's BubbleTea           | Implemented |
| Go       | fyne      | Desktop GUI using the Fyne toolkit               | Implemented |
| Kotlin   | android   | Android app using Jetpack Compose                | Implemented |
| —        | none      | Interpreter-based headless test runner            | Implemented |

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

### User-Defined Functions

User-defined functions (`fn` declarations) are registered during checker pass 1 alongside `var`, `computed`, and component declarations. Each function's name, parameter types, and return type are recorded in the component scope (or document scope for global functions). During pass 2, the checker validates function bodies: parameter types are pushed into a nested scope, the body is type-checked, and the return type is verified against the declaration. Pure functions (the default) are subject to purity enforcement — the checker rejects mutations to non-local variables and `emit` statements inside pure function bodies.

During codegen, the language translator is responsible for emitting function definitions. The JS translator emits standalone functions at the module level. The Go translator emits functions as methods on the component's `Model` struct, giving them access to model fields via the receiver.

### Handler Delegation

Complex logic (network calls, state machines, business rules) lives in `extern` functions declared in SNGL and implemented by the application author in the target language.

## Platform Generators

A platform generator (`codegen.PlatformGenerator`) is responsible for:

- **Component mapping** — Map SNGL standard library components (`vbox`, `button`, `text`, etc.) to platform-native widgets or drawing calls.
- **Layout engine integration** — Wire up layout (CSS flexbox for HTML, lipgloss for bubbletea).
- **Event system** — Map SNGL event names (`@click`, `@input`) to platform event mechanisms.
- **Lifecycle** — Handle mount, update, and teardown in a platform-idiomatic way.

Generators optionally implement `TestRunner` (for platform-specific test execution), `PreviewStyler` (CSS for HTML preview), or `Snapshotter` (visual screenshots). These are checked via type assertion.

## Standard Library

The standard library defines a set of components that every platform generator must implement:

- Layout: `vbox`, `hbox`, `stack`, `scroll`, `spacer`
- Content: `text`, `image`
- Input: `button`, `input`, `checkbox`

Each component has a schema: typed properties, supported events, and layout behavior. Schemas are defined as `.sngl` files embedded in the checker package (`internal/checker/stdlib/*.sngl`). Platform generators map these to native equivalents.

## Plugin Registration

Languages and platforms register via `init()` in their packages and are looked up by name at runtime:

- `codegen/registry.go` — thread-safe `RegisterLang`, `RegisterPlatform`
- `codegen/lang/languages.go` — blank-imports all language translator packages
- `codegen/platform/platforms.go` — blank-imports all platform generator packages
- `cmd/sngl/main.go` imports these to trigger registration

## Compiler CLI

```
sngl compile --lang=go --platform=gio --out=./gen/ app.sngl
sngl compile --lang=ts --platform=web --out=./gen/ app.sngl
sngl check app.sngl                          # analyze only (none/none)
```

## Multi-Target Compilation

A single SNGL source can be compiled to multiple targets. The parse and check phases run once; the document is cloned per target, then optimized and generated independently. Targets are declared in `output` blocks:

```sngl
output {
    go gio(out="gen/gio")
    ts web(out="gen/web")
}
```

## Error Reporting

Errors reference SNGL source locations (file, line, column). Categories:

- **Parse errors** — malformed SNGL syntax, integer overflow, unterminated strings, invalid interpolation
- **Resolution errors** — unknown component or property names
- **Type errors** — expression type mismatches, param default type mismatches, incompatible state bindings
- **Platform errors** — use of unsupported component/property for a given platform

## Extension Points

- **Custom components** — Users define component schemas in `.sngl`; platform generators provide implementations.
- **Custom language translators** — Implement the `codegen.LangTranslator` interface to add a new target language.
- **Custom platform generators** — Implement the `codegen.PlatformGenerator` interface to add a new target platform.
