# Portable GUI KDL — Design Document

## Overview

Design an abstraction layer that uses KDL as the canonical UI template language and produces portable UI layouts and reactive bindings that can target Web / Desktop / Mobile. The first implementation target is Go (runtime + compiler), but the system should be pluggable so backends can target other languages/platforms.

## Goals

* Allow designers/engineers to author UI templates in KDL with data binding and minimal imperative code.
* Support reactive bindings (properties, events, computed values) with predictable performance.
* Compile KDL templates into platform-specific artifacts via pluggable compiler backends.
* Keep runtime small and idiomatic for each platform.
* Use Yoga for layout and support Gio as a first-class Go GUI target.

## Constraints & Assumptions

* KDL is the source-of-truth for structure, attributes, and binding annotations.
* Authors can embed expressions for initial values and computed properties. The expression language can be Go-centric at first (e.g., expr or text/template style), but should be swappable.
* First runtime will be Go; later backends may target JS/TS, Swift, Kotlin, or native widgets.
* Prefer compile-time analysis where possible to minimize runtime overhead.

## High-level Architecture

1. **Parser** — parse KDL into an AST with binding annotations and expression ASTs.
2. **Type System / Analyzer** — validate bindings, infer types, compute dependency graph for computed values.
3. **Compiler Plugins** — backends that take the analyzed AST and emit target code/artifacts or runtime instructions.
4. **Runtime Interface** — a small target-specific runtime that provides primitives (create node, set prop, event handling, layout, lifecycle).
5. **Binding Runtime** — minimal library to handle change propagation, subscriptions, and patching of the UI.

## KDL Binding Syntax (proposal)

Use attributes and special nodes for bindings and expressions.

Example:

```
button id="loginBtn" text="{user.canLogin ? 'Login' : 'Locked'}" onclick="{actions.login()}"
  style.padding=8
/>

text id="greeting" text="{`Hello ${user.name}`}"/>

bind user.age: int = {parseInt(initialInput)}
computed user.isAdult: bool = {user.age >= 18}
```

* Curly braces `{...}` denote expressions evaluated by the expression engine.
* `bind` and `computed` are top-level KDL nodes (or could be attributes on a `script` node).

## Type System

* Lightweight nominal/struct-like types for objects, primitives, arrays.
* Types are optional; inferred where possible; explicit annotations allowed (e.g. `: int`).
* Compiler verifies that bound expressions are compatible with target property types.

## Reactivity / Avoiding the Update Loop

Three strategies considered:

1. **Traditional update loop / diffing** — maintain full virtual tree and diff on changes. Simple but higher runtime overhead.
2. **Subscription-based runtime (Svelte-like)** — compile bindings into direct imperative updates: when `user.name` changes, call `setText` on specific text node. Requires dependency graph computed at compile time and generation of small update functions.
3. **Hybrid** — use subscription for scalar and frequently-updated values, fall back to structural diffing for dynamic children lists.

Recommendation: start with **Subscription-based compile-time propagation** (strategy 2). This avoids a runtime reconciliation loop and is efficient: the compiler emits, for each binding, the exact update code (or runtime operation sequence) and the dependency graph for computed values. At runtime, assignments to bound properties trigger only the affected update handlers.

Implementation notes:

* At compile time, track every binding's dependencies (e.g., `greeting` depends on `user.name` and `user.title`).
* Emit small update closures or an array of operations for each dependency to apply to the runtime.
* Provide a minimal `Signal` API: `signal.Set(value)` triggers observers.
* For arrays/lists where diffs are necessary, provide a standard list-diffing util; keep it optional.

## Expression Language

Where expression logic is required, Google's CEL can be used. CEL has access to the protobuf types defined for the data on the component.

## Pluggable Compiler

Interface for backends:

* `Analyze(ast) -> AnalyzedTemplate` (type-checked, dependency graph)
* `Emit(analyzedTemplate, target) -> Artifact` (source code, binary blob, json ops)

Backends may choose:

* Emit native source (Go, Swift, Kotlin).
* Emit a portable instruction sequence (opcodes) for a small runtime VM.
* Emit Web-compatible templates (JS/TS + DOM ops or Web Components).

## Runtime API (Go sketch)

```go
type NodeHandle interface{}

type Runtime interface {
  Create(nodeType string, props map[string]any) (NodeHandle, error)
  SetProp(h NodeHandle, prop string, value any) error
  Call(h NodeHandle, method string, args ...any) error
  AppendChild(parent, child NodeHandle) error
  Remove(node NodeHandle) error
  // layout
  YogaNode(node NodeHandle) *yoga.Node
}

// signal
type Signal[T any] struct {
  Get() T
  Set(T)
  Subscribe(func(T)) Unsubscribe
}
```

The compiler emits code that drives this runtime. For Go, the emitted artifact could be a Go package that registers a `Mount` function.

## Interop with Gio/Yoga

* Use Yoga for consistent layout semantics across platforms. Emit a `YogaNode` for each KDL element with style attributes mapped to Yoga.
* For Gio, generate Gio widgets or use a thin layer that maps NodeHandle calls to Gio APIs.

## Compiler-produced artifacts

* Option A: native source (Go package) with `Mount(root io.Writer|... , model Model)` — no runtime VM required.
* Option B: JSON op-sequence plus a runtime that interprets ops and applies them to target. Easier to support multiple backends but slower.

## Milestones

1. Prototype parser -> AST -> analyzer with minimal KDL features.
2. Implement expr evaluation and type checks; compute simple dependency graph.
3. Emit a Go runtime artifact that can create simple UI nodes (button, text, container) using Yoga + Gio or Gio only.
4. Implement subscription-based updates for scalar bindings and computed values.
5. Add list/diff support and event binding.
6. Create a Web backend that emits small JS runtime operations.

## Risks

* Expression language portability if you rely on Go-specific features.
* Complexity of cross-platform widget semantics and lifecycle differences.
* Performance edge cases for large dynamic lists.

## Example KDL (minimal)

```
app
  window title="{app.title}"
    vbox
      text id="name" text="{`Name: ${user.name}`}"/>
      button text="{user.loggedIn ? 'Logout' : 'Login'}" onclick="{actions.toggleLogin()}"/>
    /
  /
/
```

## Next steps

* Iterate on binding syntax and small prototype of parser + analyzer in Go.
* Decide on expression language portability tradeoffs.
* Choose whether backends will emit native source or an op-sequence.

---

*End of initial design doc — iterate!*

---

## 📐 SNGL KDL Schema (Proposed)

This section defines the **structural and semantic schema** for SNGL templates written in KDL. It is intentionally minimal, extensible, and platform-agnostic.

### 🧱 Top-Level Nodes

A SNGL document may contain the following top-level nodes:

* `app` — root of the UI tree (exactly one)
* `bind` — declare reactive state
* `computed` — declare derived state
* `component` — declare reusable composite components
* `import` — import component libraries or protobuf descriptors (future)

---

### `app`

Root UI container.

```kdl
app {
  // exactly one root visual node inside
}
```

---

### `bind`

Declares reactive, mutable state.

```kdl
bind <name>: <Type> = <cel-expression>
```

Examples:

```kdl
bind count: int = 0
bind user: User = initialUser
```

Rules:

* `Type` may be a primitive or protobuf message type
* Initial value must type-check via CEL
* Bound values become CEL identifiers

---

### `computed`

Declares derived, read-only state.

```kdl
computed <name>: <Type> = <cel-expression>
```

Example:

```kdl
computed isAdult: bool = user.age >= 18
```

Rules:

* Cannot be mutated directly
* Recomputed automatically when dependencies change

---

### 🧩 Visual Nodes (Components)

Any non-reserved node name is treated as a **component instance**.

```kdl
<component-name> [id="..."] <props...> {
  <children...>
}
```

Example:

```kdl
button id="save" text="Save" on:click="submit()"
```

#### Common Attributes

| Attribute | Meaning                |
| --------- | ---------------------- |
| `id`      | stable node identifier |
| `key`     | list diffing identity  |
| `class`   | semantic styling hook  |

---

### 🎛 Properties

Properties are key/value pairs.

```kdl
text value="Hello"
text value="{user.name}" // CEL binding
```

Rules:

* String literals are static
* `{...}` denotes CEL expressions
* Types are validated against component schema

---

### ⚡ Event Handlers

Event handlers are attributes prefixed with `on:`.

```kdl
button on:click="inc(count)"
input on:input="set(name, $event)"
```

Rules:

* Expressions must evaluate to Mutation IR
* `$event` is implicitly typed per event

---

### 📦 `component` (Composite Components)

Defines reusable components in SNGL itself.

```kdl
component MyCard(title: string) {
  vbox {
    text value="{title}"/>
    slot
  }
}
```

Usage:

```kdl
MyCard title="Welcome" {
  text value="Content"/>
}
```

Rules:

* Parameters are typed
* Components expand at compile time
* Slots allow child projection

---

### 🪟 `slot`

Placeholder for children in composite components.

```kdl
slot
```

---

### 📐 Layout & Style

Layout and style are expressed via namespaced properties.

```kdl
vbox {
  style.padding = 8
  style.gap = 4
}
```

Rules:

* `style.*` maps to Yoga
* Platforms may ignore unsupported styles

---

### 📚 Built-in Primitive Components (Universal)

These form the **standard SNGL component library**.

* `vbox`
* `hbox`
* `stack`
* `text`
* `button`
* `input`
* `image`
* `scroll`
* `spacer`

Each component has a **platform-independent semantic contract**.
Platform plugins decide how to realize them.

---

### 🧪 Lists & Conditionals (Minimal)

Handled via attributes rather than statements.

```kdl
text if="user.loggedIn" value="Welcome"/>

item for="item in items" key="item.id" {
  text value="{item.name}"/>
}
```

Compiler expands these into static dependency-aware structures.

---

### 🔐 Reserved Keywords

* `app`
* `bind`
* `computed`
* `component`
* `slot`
* `import`

---

### ✅ Design Guarantees

* Fully static structure
* Strong typing via protobuf + CEL
* No runtime AST walking
* Dependency graph known at compile time
* Portable across GUI, Web, Mobile, and TUI

---

This schema defines **what SNGL is allowed to express**.
Platform plugins define **how it is rendered**.

---

## 📜 SNGL KDL Schema — Formal Specification

This section defines a **formal, implementation-oriented schema** for SNGL KDL documents. It is intended for compiler authors and tooling.

### 1. Document Structure

```
Document := { Import | Bind | Computed | Component } App
```

Constraints:

* Exactly one `app` node MUST exist
* Order of declarations before `app` is insignificant

---

### 2. Imports

```
import <string>
```

Semantics:

* Imports component libraries or protobuf descriptors
* Resolution is compiler-defined

---

### 3. State Declarations

#### 3.1 bind

```
bind <ident> : <type> = <cel-expr>
```

Rules:

* Declares mutable reactive state
* `<type>` may be primitive or protobuf message
* Initial value must CEL-typecheck

#### 3.2 computed

```
computed <ident> : <type> = <cel-expr>
```

Rules:

* Declares derived read-only state
* Dependencies inferred from CEL AST
* Cannot be mutation targets

---

### 4. Components (Composite)

```
component <Name>(<param>: <type>, ...) {
  <VisualNode>*
}
```

Rules:

* Parameters are immutable and typed
* Component bodies are expanded at compile time
* Recursive components are forbidden

---

### 5. Visual Nodes

```
<node-name> [Attributes] [Properties] [Block]
```

Where:

* `<node-name>` is either a primitive component or composite component
* `Block` contains zero or more VisualNodes

---

### 6. Attributes

#### 6.1 Identity

```
id = <string>
key = <cel-expr>
class = <string>
```

#### 6.2 Events

```
on:<event
```
