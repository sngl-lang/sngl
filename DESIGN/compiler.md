# SNGL Compiler — Design Document

## Purpose

The SNGL compiler transforms KDL template documents into target-specific artifacts. A single KDL source file describes a language- and platform-agnostic GUI layout; the compiler pairs a **language backend** with a **platform backend** to produce runnable output for any supported combination.

## Compilation Model

```
KDL Source
    │
    ▼
┌──────────┐
│  Parser  │  KDL → SNGL AST (layout tree + bindings + expressions)
└────┬─────┘
     ▼
┌──────────────┐
│   Analyzer   │  Type-check, dependency graph, CEL validation
└────┬─────────┘
     ▼
┌──────────────┐
│ IR Lowering  │  SNGL AST → Compiler IR (platform/language agnostic)
└────┬─────────┘
     ▼
┌────────────────────────────────┐
│  Language Backend + Platform   │  IR → target artifacts
│  Backend (selected by target)  │
└────────────────────────────────┘
```

### Phases

1. **Parse** — Read KDL, produce an untyped SNGL AST. Nodes, attributes, bindings, and `(cel)` type-annotated values are all captured verbatim. Child nodes whose names start with `@` are separated from visual children and stored as attribute nodes on the parent (e.g., `@style` properties are merged into the parent's style map).
2. **Analyze** — Resolve component references (builtins + user-defined), type-check properties and bindings against component schemas, parse and type-check CEL expressions, build the reactive dependency graph.
3. **Lower to IR** — Produce a normalized intermediate representation that captures the full semantic intent: node creation order, binding subscriptions, event handler wiring, layout constraints. The IR is independent of any target.
4. **Emit** — A language backend and platform backend collaborate to produce final artifacts.

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

## Language Backends

A language backend is responsible for:

- **CEL translation** — Convert CEL expression ASTs into equivalent expressions in the target language. Each language backend implements a CEL-to-native transpiler.
- **Binding codegen** — Emit reactive subscription/update code using the target language's idioms (closures, lambdas, observer patterns).
- **Handler stubs** — Generate typed interfaces or function signatures for handlers that the application author implements in the target language. These are the escape hatch from pure-KDL logic into full imperative code.
- **Type mapping** — Map SNGL/protobuf types to native types.

### CEL Translation

CEL is the expression language embedded in KDL templates. It handles simple logic: conditional text, computed properties, validation predicates. The language backend translates each CEL expression into a native expression at compile time.

For expressions that cannot be statically translated (rare), the backend may emit a small CEL runtime evaluation call, but this is discouraged.

### Handler Delegation

Complex logic (network calls, state machines, business rules) is not written in CEL. Instead, the KDL template references named handlers:

```kdl
button on:click=(cel)"handlers.submitForm(formData)"
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
- **Event system** — Map SNGL event names (`on:click`, `on:input`) to platform event mechanisms.
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
sngl compile --lang=go --platform=gio --out=./gen/ app.kdl
sngl compile --lang=ts --platform=web --out=./gen/ app.kdl
sngl check app.kdl                          # analyze only (none/none)
```

## Multi-Target Compilation

A single KDL source can be compiled to multiple targets. The parse and analyze phases run once; only IR lowering and emission are repeated per target. A project configuration file can declare multiple targets:

```kdl
targets {
    go-gio lang="go" platform="gio" out="gen/gio"
    web lang="ts" platform="web" out="gen/web"
}
```

## Error Reporting

Errors reference KDL source locations (file, line, column). Categories:

- **Parse errors** — malformed KDL
- **Resolution errors** — unknown component or property names
- **Type errors** — CEL expression type mismatches, incompatible bindings
- **Dependency errors** — circular computed values, missing handler definitions
- **Platform errors** — use of unsupported component/property for a given platform

## Extension Points

- **Custom components** — Users register component schemas; platform backends provide implementations.
- **Custom language backends** — Implement the language backend interface to add a new target language.
- **Custom platform backends** — Implement the platform backend interface to add a new target platform.
- **Compiler plugins** — Transform the IR before emission (optimization, instrumentation, accessibility annotations).
