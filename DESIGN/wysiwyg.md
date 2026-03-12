# SNGL WYSIWYG Editor — Design Document

## Purpose

The WYSIWYG editor provides a visual, interactive design surface for SNGL KDL documents. Authors can see a live preview of their layout, manipulate components visually, and have the KDL source updated in real time. It targets the `none/none` compiler backend — no language-specific code generation is involved.

## Architecture

```
┌────────────────────────────────────┐
│         WYSIWYG Application        │
│                                    │
│  ┌──────────┐    ┌──────────────┐  │
│  │  KDL     │◄──►│  Visual      │  │
│  │  Editor  │    │  Canvas      │  │
│  └────┬─────┘    └──────┬───────┘  │
│       │                 │          │
│       ▼                 ▼          │
│  ┌──────────────────────────┐      │
│  │  SNGL Compiler Frontend  │      │
│  │  (parse + analyze)       │      │
│  └────────────┬─────────────┘      │
│               ▼                    │
│  ┌──────────────────────────┐      │
│  │  Preview Renderer        │      │
│  │  (Yoga layout + paint)   │      │
│  └──────────────────────────┘      │
└────────────────────────────────────┘
```

### Components

1. **KDL Editor** — A text editor pane showing the raw KDL source. Edits here trigger reparse and re-render on the canvas.
2. **Visual Canvas** — Renders the layout using Yoga for positioning and a platform-native 2D drawing surface. Components are drawn as styled rectangles, text, and controls.
3. **Compiler Frontend** — The same parse + analyze pipeline used by the compiler and static analysis tool, running in `none/none` mode.
4. **Preview Renderer** — Takes the analyzed AST, computes Yoga layout, and paints the result to the canvas.

## Bidirectional Editing

The core feature is bidirectional sync between KDL source and the visual canvas:

### Source → Canvas

1. Author edits KDL text
2. Parser produces AST (incrementally where possible)
3. Analyzer validates and resolves types
4. Preview renderer computes layout and paints
5. Errors display inline in both the editor and on the canvas

### Canvas → Source

1. Author drags, resizes, or edits a component on the canvas
2. The editor identifies the corresponding AST node
3. The AST node's properties are updated
4. The KDL source is regenerated from the modified AST, preserving formatting and comments where possible
5. The text editor updates to reflect the change

### Source Preservation

When modifying KDL via canvas actions, the tool should preserve:

- Comments
- Whitespace and formatting style
- Node ordering
- Attributes not related to the edit

This requires a **concrete syntax tree** (CST) rather than a pure AST, so that round-tripping is lossless.

## Canvas Interactions

### Selection

- Click a component to select it
- Selection highlights the component on canvas and the corresponding KDL node in the editor
- Property inspector panel shows the selected component's attributes

### Manipulation

- **Drag** — Reorder children within a parent container or move between containers. Updates the KDL tree structure.
- **Resize** — Adjust `style.width`, `style.height`, or flex properties. Updates the corresponding style attributes in KDL.
- **Property editing** — Edit properties in the inspector panel. Validates against the component schema. The inspector reads and writes style properties in either form (`style.*` attributes or `@style` block). Canvas edits write to the existing `@style` block if one is present; when setting multiple style properties on a node that has none, the inspector creates a new `@style` block rather than adding many `style.*` attributes.

### Tree View

A collapsible tree view mirrors the AST hierarchy, providing an alternative navigation mechanism. Drag-and-drop in the tree view reorders nodes in the KDL source.

## Data Binding Preview

Since the WYSIWYG operates without a language runtime, bound expressions cannot execute real handlers. Instead:

- **CEL expressions** (values with `(cel)` type annotations) are evaluated with mock data. The author provides sample data in a companion file or inline panel.
- **Computed values** are calculated from the sample data using the CEL evaluator.
- **Event handlers** that reference target-language handlers are shown as inert (visually indicated).
- **Conditional rendering** (`if` attributes) respects the sample data — toggling sample values shows/hides components.
- **List rendering** (`for` attributes) iterates over sample arrays.

### Sample Data

Sample data is provided as a KDL or JSON file:

```kdl
// app.sample.kdl
sample {
    user {
        name "Alice"
        age 30
        loggedIn true
    }
    items {
        item id=1 name="First"
        item id=2 name="Second"
    }
}
```

The WYSIWYG evaluates CEL expressions against this data to produce a realistic preview.

## Component Palette

A sidebar palette lists available components:

- **Standard library** — `vbox`, `hbox`, `text`, `button`, etc.
- **User-defined components** — Composite components declared in the project
- **Drag-to-add** — Drag a component from the palette onto the canvas or tree to insert it into the KDL document

## Diagnostics

The WYSIWYG integrates the static analysis tool's diagnostics:

- Errors and warnings display inline in the KDL editor (squiggly underlines)
- Components with errors are highlighted on the canvas (red outline)
- The property inspector shows type errors on individual attributes

## Platform

The WYSIWYG editor itself is an SNGL application — specifically, it targets one concrete platform (likely desktop via Gio or web via WASM). The preview canvas, however, renders platform-agnostically using Yoga layout and a generic paint layer, since the preview must not be tied to any specific target platform.

## Responsive Preview

The canvas supports resizing to preview layouts at different viewport sizes (phone, tablet, desktop). A toolbar provides preset sizes and orientation toggle. Yoga recomputes layout at each size, showing how the design responds.
