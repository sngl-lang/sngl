# SNGL Schema — Full Specification

## Purpose

This document defines the complete schema for SNGL layout documents: the document structure, node types, attribute types, expression syntax, and the typed definitions for every standard library component. This is the canonical reference for compiler and tooling implementors.

## Type System

### Primitive Types

| Type        | Description                            | Examples                    |
| ----------- | -------------------------------------- | --------------------------- |
| `bool`      | Boolean                                | `true`, `false`             |
| `int`       | Signed integer (platform-native width) | `0`, `-1`, `42`             |
| `float`     | Floating-point number                  | `1.0`, `-3.14`              |
| `string`    | UTF-8 text                             | `"hello"`                   |
| `color`     | RGBA color (hex literal)               | `#FF0000`, `#fff`, `#ff000080` |
| `enum(...)` | One of a fixed set of string values    | Defined per property        |

### Composite Types

| Type           | Syntax             | Description                        |
| -------------- | ------------------ | ---------------------------------- |
| `list<T>`      | `list<string>`     | Ordered collection                 |
| `map<K,V>`     | `map<string, int>` | Key-value mapping                  |
| `optional<T>`  | `optional<string>` | Value or absent                    |

### Expression Types

All values in SNGL are either:

- **Literal** — A static value: `text(value="Hello")`
- **Expression** — A Go-like expression: `text(value="Hello, {name}!")`
- **Interpolation** — Inline expression in strings: `"Count: {count}"`

Expressions are first-class syntactic constructs stored as native SNGL AST nodes. The type checker infers types by walking the AST — literals carry intrinsic types, identifiers are resolved via scoped variable tracking, and operators produce types based on their operands.

## Document Structure

```
Document := Declaration*
Declaration := Import | Output | Struct | Enum | Unit | Style | Styles | Component | Test
```

### Import

```sngl
import "app.proto"
```

| Argument     | Type     | Description                                                     |
| ------------ | -------- | --------------------------------------------------------------- |
| (positional) | `string` | Module path or protobuf descriptor relative to the current file |

### var

Declares mutable reactive state inside a component.

```sngl
var count = 0
var name = "World"
var todos list<Todo> = []
```

Grouped form:

```sngl
var (
    count = 0
    name = "World"
    active = true
)
```

Types are inferred from default values when possible.

### computed

Declares derived, read-only state inside a component.

```sngl
computed greeting = "Hello, {name}!"
computed isAdult = user.age >= 18
```

Grouped form:

```sngl
computed (
    greeting = "Hello, {name}!"
    isAdult = user.age >= 18
)
```

### const

Declares immutable values inside a component.

```sngl
const maxItems = 100
const label string = "Hello"
```

### Component (user-defined)

```sngl
component Counter {
    param label = ""
    param start = 0
    var count = start

    hbox {
        text(value="{label}: {count}")
        button(text="+", @click={ count += 1 })
    }
}
```

Components own their reactive state. `var` and `computed` declarations inside a component are scoped to that component instance.

#### `param`

```sngl
param name type = default   // explicit type
param name = default        // type inferred from default
param name type             // no default — zero value of type
```

### Style (named style blocks)

```sngl
style heading {
    font-size = 24
    font-weight = "bold"
    color = #007700
}
```

Named style blocks can be referenced via `class` attributes on visual nodes.

## Visual Node Grammar

```
VisualNode := <component-name> ("(" PropList ")")? ("{" Children "}")?
PropList := Prop ("," Prop)*
Prop := "@" IDENT "=" "{" StmtList "}" | "style" "=" StyleLiteral | IDENT "=" Expr
Children := (NodeOrControl | "@" IDENT "(" KVList ")")*
```

### Universal Attributes

Every visual node accepts these attributes:

| Attribute | Type         | Default | Description                                 |
| --------- | ------------ | ------- | ------------------------------------------- |
| `id`      | `string`     | none    | Unique identifier for selection and testing |
| `key`     | `string`     | none    | Identity key for list diffing               |
| `class`   | `string`     | none    | References a named style block              |
| `ref`     | `string`     | none    | Named reference for programmatic access     |

### Control Flow

Conditional and iterative rendering use block syntax:

```sngl
if isAdult {
    text(value="(Adult)")
}

for item in todos {
    text(key=item.id, value=item.text)
}
```

### Event Attributes

Event attributes use the `@` prefix and contain statement blocks:

```sngl
button(@click={ count += 1 })
input(@input={ name = event.value })
button(@click={
    todos.push(Todo{text: newTodo, done: false})
    newTodo = ""
})
```

### Style Prop (Inline)

The `style` prop accepts a style literal — `{key=value, ...}` pairs:

```sngl
vbox(style={gap=12, padding=16})
text(value="hello", style={color=#007700, font-size=24})
```

### Attribute Nodes

Inside a children block, `@name(props)` defines attribute metadata:

```sngl
text(value="hello") {
    @tooltip(text="A helpful tip")
}
```

## Layout Properties (style)

All layout properties map to Yoga. Values of type `length` accept integers (pixels), `"X%"` (percentage), or `"auto"`.

### Sizing

| Property       | Type     | Default  | Description        |
| -------------- | -------- | -------- | ------------------ |
| `width`        | `length` | `"auto"` | Width              |
| `height`       | `length` | `"auto"` | Height             |
| `min-width`    | `length` | `0`      | Minimum width      |
| `min-height`   | `length` | `0`      | Minimum height     |
| `max-width`    | `length` | `"none"` | Maximum width      |
| `max-height`   | `length` | `"none"` | Maximum height     |
| `aspect-ratio` | `float`  | none     | Width/height ratio |

### Padding

| Property         | Type     | Default |
| ---------------- | -------- | ------- |
| `padding`        | `length` | `0`     |
| `padding-top`    | `length` | `0`     |
| `padding-right`  | `length` | `0`     |
| `padding-bottom` | `length` | `0`     |
| `padding-left`   | `length` | `0`     |
| `padding-x`      | `length` | `0`     |
| `padding-y`      | `length` | `0`     |

### Margin

| Property        | Type     | Default |
| --------------- | -------- | ------- |
| `margin`        | `length` | `0`     |
| `margin-top`    | `length` | `0`     |
| `margin-right`  | `length` | `0`     |
| `margin-bottom` | `length` | `0`     |
| `margin-left`   | `length` | `0`     |
| `margin-x`      | `length` | `0`     |
| `margin-y`      | `length` | `0`     |

### Flex

| Property      | Type                                                | Default  | Description               |
| ------------- | --------------------------------------------------- | -------- | ------------------------- |
| `flex`        | `float`                                             | `0`      | Shorthand for flex-grow   |
| `flex-grow`   | `float`                                             | `0`      | Grow factor               |
| `flex-shrink` | `float`                                             | `1`      | Shrink factor             |
| `flex-basis`  | `length`                                            | `"auto"` | Initial main axis size    |
| `align-self`  | `enum(auto, start, center, end, stretch, baseline)` | `"auto"` | Cross-axis self alignment |

### Positioning

| Property   | Type                       | Default      | Description                     |
| ---------- | -------------------------- | ------------ | ------------------------------- |
| `position` | `enum(relative, absolute)` | `"relative"` | Positioning mode                |
| `top`      | `length`                   | none         | Offset from top (absolute only) |
| `right`    | `length`                   | none         | Offset from right               |
| `bottom`   | `length`                   | none         | Offset from bottom              |
| `left`     | `length`                   | none         | Offset from left                |
| `z-index`  | `int`                      | `0`          | Stacking order                  |

### Container Layout

These apply to container components (`vbox`, `hbox`, `stack`, etc.):

| Property          | Type                                                                  | Default     | Description                |
| ----------------- | --------------------------------------------------------------------- | ----------- | -------------------------- |
| `gap`             | `length`                                                              | `0`         | Space between children     |
| `row-gap`         | `length`                                                              | `0`         | Vertical gap               |
| `column-gap`      | `length`                                                              | `0`         | Horizontal gap             |
| `align-items`     | `enum(start, center, end, stretch, baseline)`                         | `"stretch"` | Cross-axis child alignment |
| `justify-content` | `enum(start, center, end, space-between, space-around, space-evenly)` | `"start"`   | Main-axis distribution     |
| `flex-wrap`       | `enum(nowrap, wrap, wrap-reverse)`                                    | `"nowrap"`  | Wrapping behavior          |

## Visual Properties (style)

These control appearance and are interpreted by platform backends. Backends that cannot support a property ignore it gracefully.

| Property        | Type                            | Default         | Description       |
| --------------- | ------------------------------- | --------------- | ----------------- |
| `background`    | `color`                         | `"transparent"` | Background color  |
| `border-color`  | `color`                         | `"transparent"` | Border color      |
| `border-width`  | `length`                        | `0`             | Border thickness  |
| `border-radius` | `length`                        | `0`             | Corner radius     |
| `opacity`       | `float`                         | `1.0`           | Opacity (0.0-1.0) |
| `overflow`      | `enum(visible, hidden, scroll)` | `"visible"`     | Overflow behavior |

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

Overlay container. Children are stacked on top of each other (z-order determined by document order or `z-index`).

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

| Property        | Type                                 | Default          | Description                      |
| --------------- | ------------------------------------ | ---------------- | -------------------------------- |
| `value`         | `string`                             | `""`             | Text content to display          |
| `color`         | `color`                              | platform default | Text color                       |
| `font-size`     | `float`                              | platform default | Font size in scaled points       |
| `font-weight`   | `enum(normal, bold, 100..900)`       | `"normal"`       | Font weight                      |
| `font-style`    | `enum(normal, italic)`               | `"normal"`       | Font style                       |
| `font-family`   | `string`                             | platform default | Font family name                 |
| `text-align`    | `enum(left, center, right, justify)` | `"left"`         | Horizontal text alignment        |
| `line-height`   | `float`                              | `1.2`            | Line height multiplier           |
| `text-overflow` | `enum(clip, ellipsis)`               | `"clip"`         | Overflow behavior                |
| `max-lines`     | `int`                                | none             | Maximum number of visible lines  |
| `selectable`    | `bool`                               | `false`          | Whether the text can be selected |

| Event    | Payload Type | Description             |
| -------- | ------------ | ----------------------- |
| `@click` | `ClickEvent` | Text was clicked/tapped |

**Children:** none.

---

### `button`

Interactive push button.

| Property     | Type     | Default          | Description                    |
| ------------ | -------- | ---------------- | ------------------------------ |
| `text`       | `string` | `""`             | Button label                   |
| `disabled`   | `bool`   | `false`          | Whether the button is disabled |
| `color`      | `color`  | platform default | Label text color               |
| `background` | `color`  | platform default | Button background              |

| Event         | Payload Type     | Description             |
| ------------- | ---------------- | ----------------------- |
| `@click`      | `ClickEvent`     | Button was pressed      |
| `@long-press` | `LongPressEvent` | Button was long-pressed |

**Children:** optional. If children are provided, they replace the `text` label as the button's content.

---

### `input`

Text input field.

| Property            | Type                                                    | Default          | Description                           |
| ------------------- | ------------------------------------------------------- | ---------------- | ------------------------------------- |
| `value`             | `string`                                                | `""`             | Current text value (two-way bindable) |
| `placeholder`       | `string`                                                | `""`             | Placeholder text when empty           |
| `disabled`          | `bool`                                                  | `false`          | Whether input is disabled             |
| `readonly`          | `bool`                                                  | `false`          | Whether input is read-only            |
| `type`              | `enum(text, password, number, email, url, tel, search)` | `"text"`         | Input type hint                       |
| `max-length`        | `int`                                                   | none             | Maximum character count               |
| `color`             | `color`                                                 | platform default | Text color                            |
| `placeholder-color` | `color`                                                 | platform default | Placeholder text color                |

| Event     | Payload Type                    | Description                             |
| --------- | ------------------------------- | --------------------------------------- |
| `@input`  | `InputEvent { value: string }`  | Value changed (fires on each keystroke) |
| `@change` | `ChangeEvent { value: string }` | Value committed (blur or enter)         |
| `@focus`  | `FocusEvent`                    | Input gained focus                      |
| `@blur`   | `FocusEvent`                    | Input lost focus                        |
| `@submit` | `SubmitEvent { value: string }` | Enter/return pressed                    |

**Children:** none.

---

### `image`

Displays an image. Leaf node.

| Property | Type                                           | Default     | Description                      |
| -------- | ---------------------------------------------- | ----------- | -------------------------------- |
| `src`    | `string`                                       | required    | Image source (URL or asset path) |
| `alt`    | `string`                                       | `""`        | Accessibility description        |
| `fit`    | `enum(contain, cover, fill, none, scale-down)` | `"contain"` | How the image fits its bounds    |

| Event    | Payload Type                     | Description              |
| -------- | -------------------------------- | ------------------------ |
| `@click` | `ClickEvent`                     | Image was clicked/tapped |
| `@load`  | `LoadEvent`                      | Image finished loading   |
| `@error` | `ErrorEvent { message: string }` | Image failed to load     |

**Children:** none.

---

### `scroll`

Scrollable container. Wraps content that may exceed the container's bounds.

| Property         | Type                               | Default      | Description                                   |
| ---------------- | ---------------------------------- | ------------ | --------------------------------------------- |
| `direction`      | `enum(vertical, horizontal, both)` | `"vertical"` | Scroll axes                                   |
| `scroll-x`       | `float`                            | `0`          | Horizontal scroll position (two-way bindable) |
| `scroll-y`       | `float`                            | `0`          | Vertical scroll position (two-way bindable)   |
| `show-scrollbar` | `enum(auto, always, never)`        | `"auto"`     | Scrollbar visibility                          |

| Event         | Payload Type                          | Description             |
| ------------- | ------------------------------------- | ----------------------- |
| `@scroll`     | `ScrollEvent { x: float, y: float }` | Scroll position changed |
| `@scroll-end` | `ScrollEvent { x: float, y: float }` | Scroll momentum ended   |

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

These are the typed payloads available via `event` in event handler statement blocks.

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

## Complete Example

```sngl
import "app.proto"

struct Todo {
    text string = ""
    done bool = false
}

component Counter {
    param label = ""
    var count = 0

    hbox {
        text(value="{label}: {count}")
        button(text="+", @click={ count += 1 })
        button(text="-", @click={ count -= 1 }, disabled=count <= 0)
    }
}

component main {
    var (
        user = User{name: "World", age: 25, loggedIn: false}
        count = 0
    )

    computed (
        greeting = "Hello, {user.name}!"
        isAdult = user.age >= 18
    )

    vbox(style={padding=16, gap=12}) {
        text(value=greeting, style={font-size=24, font-weight="bold"})

        if isAdult {
            text(value="(Adult)", style={color=#007700})
        }
        if !isAdult {
            text(value="(Minor)", style={color=#CC0000})
        }

        input(value=user.name, placeholder="Enter name",
              @input={ user.name = event.value })

        Counter(label="Clicks")

        button(text=user.loggedIn ? "Logout" : "Login",
               @click={ user.loggedIn!! })
    }
}
```
