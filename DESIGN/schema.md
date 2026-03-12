# SNGL KDL Schema — Full Specification

## Purpose

This document defines the complete schema for SNGL KDL layout documents: the document structure, node types, attribute types, expression syntax, and the typed definitions for every standard library component. This is the canonical reference for compiler and tooling implementors.

## Type System

### Primitive Types

| Type        | Description                            | Examples                                  |
| ----------- | -------------------------------------- | ----------------------------------------- |
| `bool`      | Boolean                                | `true`, `false`                           |
| `int`       | Signed integer (platform-native width) | `0`, `-1`, `42`                           |
| `float`     | Floating-point number                  | `1.0`, `-3.14`                            |
| `string`    | UTF-8 text                             | `"hello"`                                 |
| `color`     | RGBA color (hex, named, or function)   | `"#FF0000"`, `"red"`, `"rgba(255,0,0,1)"` |
| `length`    | Dimensional value                      | `8`, `"50%"`, `"auto"`                    |
| `enum(...)` | One of a fixed set of string values    | Defined per property                      |

### Composite Types

| Type           | Syntax                   | Description                        |
| -------------- | ------------------------ | ---------------------------------- |
| `list<T>`      | `list<string>`           | Ordered collection                 |
| `map<K,V>`     | `map<string, int>`       | Key-value mapping                  |
| `message`      | protobuf message name    | Structured data (protobuf-defined) |
| `optional<T>`  | `optional<string>`       | Value or absent                    |
| `handler(...)` | `handler(Event) -> void` | Callable handler signature         |

### Expression Types

Values in KDL attributes are one of:

- **Literal** — A static value: `text value="Hello"`
- **CEL expression** — A KDL type-annotated value: `text value=(cel)"user.name"`

The `(cel)` type annotation uses KDL's native type annotation syntax. The parser distinguishes CEL expressions from plain strings at the KDL parse level — no string scanning or escape conventions are needed. All CEL expressions are full CEL; string concatenation is expressed as `(cel)"'Hello ' + user.name"` rather than interpolation.

## Document Structure

```
Document := Declaration* App
Declaration := Import | Bind | Computed | Component | Style
```

### Import

```kdl
import "<path-or-module>"
```

| Argument     | Type     | Description                                                     |
| ------------ | -------- | --------------------------------------------------------------- |
| (positional) | `string` | Module path or protobuf descriptor relative to the current file |

### Bind

```kdl
bind <name> <initial-value>
bind <name>: <type> = <cel-expr>
```

| Part            | Required | Description                                           |
| --------------- | -------- | ----------------------------------------------------- |
| name            | yes      | Identifier; becomes a CEL variable                    |
| type annotation | no       | Explicit type; inferred from initial value if omitted |
| initial value   | yes      | Literal or CEL expression                             |

### Computed

```kdl
computed <name>: <type> = <cel-expr>
```

| Part            | Required | Description                                        |
| --------------- | -------- | -------------------------------------------------- |
| name            | yes      | Identifier; becomes a read-only CEL variable       |
| type annotation | no       | Explicit type; inferred from expression if omitted |
| expression      | yes      | CEL expression; dependencies auto-tracked          |

### Component (user-defined)

```kdl
component <Name> {
    param <name>: <type> [default=<value>]
    // ... body nodes
}
```

| Part        | Required | Description                                       |
| ----------- | -------- | ------------------------------------------------- |
| Name        | yes      | PascalCase identifier                             |
| param nodes | no       | Typed parameters (immutable within the component) |
| body        | yes      | One or more visual nodes; may contain `slot`      |

### Style (named style blocks)

```kdl
style "<name>" {
    // style properties
}
```

Named style blocks can be referenced via `class` attributes on visual nodes.

## Visual Node Grammar

```
VisualNode := <component-name> [Attributes] [Children]
Attributes := (key=value)*
Children := { AttributeNode* VisualNode* }
AttributeNode := @<name> [Attributes] [Block]
```

### Universal Attributes

Every visual node accepts these attributes:

| Attribute | Type                      | Default | Description                                 |
| --------- | ------------------------- | ------- | ------------------------------------------- |
| `id`      | `string`                  | none    | Unique identifier for selection and testing |
| `key`     | `string \| (cel)`         | none    | Identity key for list diffing               |
| `class`   | `string`                  | none    | References a named style block              |
| `if`      | `(cel) -> bool`           | none    | Conditional rendering                       |
| `for`     | `"<ident> in <cel-expr>"` | none    | List rendering                              |
| `ref`     | `string`                  | none    | Named reference for programmatic access     |

### Event Attributes

Event attributes are prefixed with `on:` and take a `(cel)` type-annotated value:

```
on:<event-name> = (cel)"<expression>"
```

The CEL expression may use `$event` to access the event payload. The type of `$event` depends on the event.

### Attribute Nodes

Any child node whose name starts with `@` is an **attribute node** — structured metadata that applies to the parent, not a visual child. The `@` prefix is a general convention: the compiler strips attribute nodes from the visual child list before layout, so no reserved keyword list is needed.

- Attribute nodes carry key/value data that modifies their parent
- Convention is to place attribute nodes before visual children, but order is not enforced
- Currently defined: `@style`
- The pattern is extensible to future attribute nodes (e.g., `@accessibility`, `@animation`)

### `@style` Block

The `@style` block provides a structured alternative to `style.*` attributes for setting style properties. Property names inside the block are the same as `style.*` attributes but without the `style.` prefix.

Both forms can coexist on the same node. When they conflict, the `@style` block takes precedence.

```kdl
// Attribute form — convenient for 1-2 properties
button style.padding=8 style.background="#ff0000"

// Block form — cleaner for many properties
button {
    @style {
        padding 8
        background "#ff0000"
        font-size 16
        border-radius 4
    }
    text value="child"
}

// Both forms — block wins on conflict
button style.margin=4 {
    @style {
        padding 8
        background "#ff0000"
    }
}
```

### Style Properties

Style properties map to Yoga layout properties or visual properties. They can be set in two ways:

1. **Attribute form** — `style.<property>=<value>` as an attribute on the node
2. **Block form** — `<property> <value>` inside a `@style` child block

The property names are identical; the block form simply omits the `style.` prefix.

See the Layout Properties and Visual Properties sections below.

## Layout Properties (style.\*)

All layout properties map to Yoga. Values of type `length` accept integers (pixels), `"X%"` (percentage), or `"auto"`.

### Sizing

| Property             | Type     | Default  | Description        |
| -------------------- | -------- | -------- | ------------------ |
| `style.width`        | `length` | `"auto"` | Width              |
| `style.height`       | `length` | `"auto"` | Height             |
| `style.min-width`    | `length` | `0`      | Minimum width      |
| `style.min-height`   | `length` | `0`      | Minimum height     |
| `style.max-width`    | `length` | `"none"` | Maximum width      |
| `style.max-height`   | `length` | `"none"` | Maximum height     |
| `style.aspect-ratio` | `float`  | none     | Width/height ratio |

### Padding

| Property               | Type     | Default |
| ---------------------- | -------- | ------- |
| `style.padding`        | `length` | `0`     |
| `style.padding-top`    | `length` | `0`     |
| `style.padding-right`  | `length` | `0`     |
| `style.padding-bottom` | `length` | `0`     |
| `style.padding-left`   | `length` | `0`     |
| `style.padding-x`      | `length` | `0`     |
| `style.padding-y`      | `length` | `0`     |

### Margin

| Property              | Type     | Default |
| --------------------- | -------- | ------- |
| `style.margin`        | `length` | `0`     |
| `style.margin-top`    | `length` | `0`     |
| `style.margin-right`  | `length` | `0`     |
| `style.margin-bottom` | `length` | `0`     |
| `style.margin-left`   | `length` | `0`     |
| `style.margin-x`      | `length` | `0`     |
| `style.margin-y`      | `length` | `0`     |

### Flex

| Property            | Type                                                | Default  | Description               |
| ------------------- | --------------------------------------------------- | -------- | ------------------------- |
| `style.flex`        | `float`                                             | `0`      | Shorthand for flex-grow   |
| `style.flex-grow`   | `float`                                             | `0`      | Grow factor               |
| `style.flex-shrink` | `float`                                             | `1`      | Shrink factor             |
| `style.flex-basis`  | `length`                                            | `"auto"` | Initial main axis size    |
| `style.align-self`  | `enum(auto, start, center, end, stretch, baseline)` | `"auto"` | Cross-axis self alignment |

### Positioning

| Property         | Type                       | Default      | Description                     |
| ---------------- | -------------------------- | ------------ | ------------------------------- |
| `style.position` | `enum(relative, absolute)` | `"relative"` | Positioning mode                |
| `style.top`      | `length`                   | none         | Offset from top (absolute only) |
| `style.right`    | `length`                   | none         | Offset from right               |
| `style.bottom`   | `length`                   | none         | Offset from bottom              |
| `style.left`     | `length`                   | none         | Offset from left                |
| `style.z-index`  | `int`                      | `0`          | Stacking order                  |

### Container Layout

These apply to container components (`vbox`, `hbox`, `stack`, etc.):

| Property                | Type                                                                  | Default     | Description                |
| ----------------------- | --------------------------------------------------------------------- | ----------- | -------------------------- |
| `style.gap`             | `length`                                                              | `0`         | Space between children     |
| `style.row-gap`         | `length`                                                              | `0`         | Vertical gap               |
| `style.column-gap`      | `length`                                                              | `0`         | Horizontal gap             |
| `style.align-items`     | `enum(start, center, end, stretch, baseline)`                         | `"stretch"` | Cross-axis child alignment |
| `style.justify-content` | `enum(start, center, end, space-between, space-around, space-evenly)` | `"start"`   | Main-axis distribution     |
| `style.flex-wrap`       | `enum(nowrap, wrap, wrap-reverse)`                                    | `"nowrap"`  | Wrapping behavior          |

## Visual Properties (style.\*)

These control appearance and are interpreted by platform backends. Backends that cannot support a property ignore it gracefully.

| Property              | Type                            | Default         | Description       |
| --------------------- | ------------------------------- | --------------- | ----------------- |
| `style.background`    | `color`                         | `"transparent"` | Background color  |
| `style.border-color`  | `color`                         | `"transparent"` | Border color      |
| `style.border-width`  | `length`                        | `0`             | Border thickness  |
| `style.border-radius` | `length`                        | `0`             | Corner radius     |
| `style.opacity`       | `float`                         | `1.0`           | Opacity (0.0–1.0) |
| `style.overflow`      | `enum(visible, hidden, scroll)` | `"visible"`     | Overflow behavior |

---

## Standard Library Components

Every platform backend must implement these components. Each definition below specifies the component's properties, events, and children policy.

---

### `vbox`

Vertical flex container. Children are laid out top to bottom.

**Inherent layout:** `flex-direction: column`

| Property                        | Type | Default | Description |
| ------------------------------- | ---- | ------- | ----------- |
| (none beyond universal + style) |      |         |             |

| Event  | Payload | Description |
| ------ | ------- | ----------- |
| (none) |         |             |

**Children:** zero or more visual nodes.

---

### `hbox`

Horizontal flex container. Children are laid out left to right.

**Inherent layout:** `flex-direction: row`

| Property                        | Type | Default | Description |
| ------------------------------- | ---- | ------- | ----------- |
| (none beyond universal + style) |      |         |             |

| Event  | Payload | Description |
| ------ | ------- | ----------- |
| (none) |         |             |

**Children:** zero or more visual nodes.

---

### `stack`

Overlay container. Children are stacked on top of each other (z-order determined by document order or `style.z-index`).

**Inherent layout:** All children are positioned absolutely within the stack's bounds.

| Property                        | Type | Default | Description |
| ------------------------------- | ---- | ------- | ----------- |
| (none beyond universal + style) |      |         |             |

| Event  | Payload | Description |
| ------ | ------- | ----------- |
| (none) |         |             |

**Children:** zero or more visual nodes.

---

### `text`

Displays text content. Leaf node (no children).

| Property              | Type                                 | Default          | Description                      |
| --------------------- | ------------------------------------ | ---------------- | -------------------------------- |
| `value`               | `string \| (cel) -> string`          | `""`             | Text content to display          |
| `style.color`         | `color`                              | platform default | Text color                       |
| `style.font-size`     | `float`                              | platform default | Font size in scaled points       |
| `style.font-weight`   | `enum(normal, bold, 100..900)`       | `"normal"`       | Font weight                      |
| `style.font-style`    | `enum(normal, italic)`               | `"normal"`       | Font style                       |
| `style.font-family`   | `string`                             | platform default | Font family name                 |
| `style.text-align`    | `enum(left, center, right, justify)` | `"left"`         | Horizontal text alignment        |
| `style.line-height`   | `float`                              | `1.2`            | Line height multiplier           |
| `style.text-overflow` | `enum(clip, ellipsis)`               | `"clip"`         | Overflow behavior                |
| `style.max-lines`     | `int`                                | none             | Maximum number of visible lines  |
| `selectable`          | `bool`                               | `false`          | Whether the text can be selected |

| Event      | Payload Type | Description             |
| ---------- | ------------ | ----------------------- |
| `on:click` | `ClickEvent` | Text was clicked/tapped |

**Children:** none.

---

### `button`

Interactive push button.

| Property           | Type                        | Default          | Description                    |
| ------------------ | --------------------------- | ---------------- | ------------------------------ |
| `text`             | `string \| (cel) -> string` | `""`             | Button label                   |
| `disabled`         | `bool \| (cel) -> bool`     | `false`          | Whether the button is disabled |
| `style.color`      | `color`                     | platform default | Label text color               |
| `style.background` | `color`                     | platform default | Button background              |

| Event           | Payload Type     | Description             |
| --------------- | ---------------- | ----------------------- |
| `on:click`      | `ClickEvent`     | Button was pressed      |
| `on:long-press` | `LongPressEvent` | Button was long-pressed |

**Children:** optional. If children are provided, they replace the `text` label as the button's content.

---

### `input`

Text input field.

| Property                  | Type                                                    | Default          | Description                           |
| ------------------------- | ------------------------------------------------------- | ---------------- | ------------------------------------- |
| `value`                   | `string \| (cel) -> string`                             | `""`             | Current text value (two-way bindable) |
| `placeholder`             | `string`                                                | `""`             | Placeholder text when empty           |
| `disabled`                | `bool \| (cel) -> bool`                                 | `false`          | Whether input is disabled             |
| `readonly`                | `bool`                                                  | `false`          | Whether input is read-only            |
| `type`                    | `enum(text, password, number, email, url, tel, search)` | `"text"`         | Input type hint                       |
| `max-length`              | `int`                                                   | none             | Maximum character count               |
| `style.color`             | `color`                                                 | platform default | Text color                            |
| `style.placeholder-color` | `color`                                                 | platform default | Placeholder text color                |

| Event       | Payload Type                    | Description                             |
| ----------- | ------------------------------- | --------------------------------------- |
| `on:input`  | `InputEvent { value: string }`  | Value changed (fires on each keystroke) |
| `on:change` | `ChangeEvent { value: string }` | Value committed (blur or enter)         |
| `on:focus`  | `FocusEvent`                    | Input gained focus                      |
| `on:blur`   | `FocusEvent`                    | Input lost focus                        |
| `on:submit` | `SubmitEvent { value: string }` | Enter/return pressed                    |

**Children:** none.

---

### `image`

Displays an image. Leaf node.

| Property | Type                                           | Default     | Description                      |
| -------- | ---------------------------------------------- | ----------- | -------------------------------- |
| `src`    | `string \| (cel) -> string`                    | required    | Image source (URL or asset path) |
| `alt`    | `string`                                       | `""`        | Accessibility description        |
| `fit`    | `enum(contain, cover, fill, none, scale-down)` | `"contain"` | How the image fits its bounds    |

| Event      | Payload Type                     | Description              |
| ---------- | -------------------------------- | ------------------------ |
| `on:click` | `ClickEvent`                     | Image was clicked/tapped |
| `on:load`  | `LoadEvent`                      | Image finished loading   |
| `on:error` | `ErrorEvent { message: string }` | Image failed to load     |

**Children:** none.

---

### `scroll`

Scrollable container. Wraps content that may exceed the container's bounds.

| Property         | Type                               | Default      | Description                                   |
| ---------------- | ---------------------------------- | ------------ | --------------------------------------------- |
| `direction`      | `enum(vertical, horizontal, both)` | `"vertical"` | Scroll axes                                   |
| `scroll-x`       | `float \| (cel) -> float`          | `0`          | Horizontal scroll position (two-way bindable) |
| `scroll-y`       | `float \| (cel) -> float`          | `0`          | Vertical scroll position (two-way bindable)   |
| `show-scrollbar` | `enum(auto, always, never)`        | `"auto"`     | Scrollbar visibility                          |

| Event           | Payload Type                         | Description             |
| --------------- | ------------------------------------ | ----------------------- |
| `on:scroll`     | `ScrollEvent { x: float, y: float }` | Scroll position changed |
| `on:scroll-end` | `ScrollEvent { x: float, y: float }` | Scroll momentum ended   |

**Children:** exactly one child (the scrollable content, typically a `vbox` or `hbox`).

---

### `spacer`

Flexible empty space. Expands to fill available space along the parent's main axis.

**Inherent layout:** `flex-grow: 1`

| Property | Type     | Default | Description                          |
| -------- | -------- | ------- | ------------------------------------ |
| `size`   | `length` | none    | Fixed size (overrides flex behavior) |

| Event  | Payload | Description |
| ------ | ------- | ----------- |
| (none) |         |             |

**Children:** none.

---

## Event Payload Types

These are the typed payloads available via `$event` in event handler CEL expressions.

### `ClickEvent`

| Field      | Type    | Description                        |
| ---------- | ------- | ---------------------------------- |
| `x`        | `float` | X coordinate relative to component |
| `y`        | `float` | Y coordinate relative to component |
| `global_x` | `float` | X coordinate relative to window    |
| `global_y` | `float` | Y coordinate relative to window    |

### `LongPressEvent`

| Field      | Type    | Description                        |
| ---------- | ------- | ---------------------------------- |
| `x`        | `float` | X coordinate relative to component |
| `y`        | `float` | Y coordinate relative to component |
| `duration` | `float` | Press duration in milliseconds     |

### `InputEvent`

| Field   | Type     | Description         |
| ------- | -------- | ------------------- |
| `value` | `string` | Current input value |

### `ChangeEvent`

| Field   | Type     | Description     |
| ------- | -------- | --------------- |
| `value` | `string` | Committed value |

### `FocusEvent`

| Field   | Type | Description |
| ------- | ---- | ----------- |
| (empty) |      |             |

### `SubmitEvent`

| Field   | Type     | Description                   |
| ------- | -------- | ----------------------------- |
| `value` | `string` | Input value at time of submit |

### `ScrollEvent`

| Field | Type    | Description              |
| ----- | ------- | ------------------------ |
| `x`   | `float` | Horizontal scroll offset |
| `y`   | `float` | Vertical scroll offset   |

### `LoadEvent`

| Field   | Type | Description |
| ------- | ---- | ----------- |
| (empty) |      |             |

### `ErrorEvent`

| Field     | Type     | Description       |
| --------- | -------- | ----------------- |
| `message` | `string` | Error description |

---

## Two-Way Binding

Properties marked as "two-way bindable" support automatic synchronization between the UI and the reactive state. When a CEL expression references a `bind` variable, the binding is two-way:

```kdl
bind name: string = ""
input value=(cel)"name" on:input=(cel)"set(name, $event.value)"
```

The `set()` built-in function is the canonical way to mutate bound state from event handlers.

### Built-in CEL Functions

| Function | Signature                             | Description                                       |
| -------- | ------------------------------------- | ------------------------------------------------- |
| `set`    | `set(binding, value) -> void`         | Mutate a bound variable                           |
| `toggle` | `toggle(binding) -> void`             | Flip a boolean binding                            |
| `push`   | `push(list-binding, value) -> void`   | Append to a list binding                          |
| `remove` | `remove(list-binding, index) -> void` | Remove from a list by index                       |
| `emit`   | `emit(event-name, payload) -> void`   | Emit a custom event (for component communication) |

---

## Complete Example

```kdl
import "app.proto"

bind user: User = (cel)"User{ name: 'World', age: 25, loggedIn: false }"
bind count: int = 0

computed greeting: string = (cel)"'Hello, ' + user.name + '!'"
computed isAdult: bool = (cel)"user.age >= 18"

component Counter(label: string) {
    hbox {
        text value=(cel)"label + ': ' + string(count)"
        button text="+" on:click=(cel)"set(count, count + 1)"
        button text="-" on:click=(cel)"set(count, count - 1)" disabled=(cel)"count <= 0"
    }
}

app {
    vbox style.padding=16 style.gap=12 {
        text value=(cel)"greeting" {
            @style {
                font-size 24
                font-weight "bold"
            }
        }

        text if=(cel)"isAdult" value="(Adult)" style.color="#007700"
        text if=(cel)"!isAdult" value="(Minor)" style.color="#CC0000"

        input value=(cel)"user.name" placeholder="Enter name" on:input=(cel)"set(user.name, $event.value)"

        Counter label="Clicks"

        button text=(cel)"user.loggedIn ? 'Logout' : 'Login'" on:click=(cel)"toggle(user.loggedIn)"
    }
}
```
