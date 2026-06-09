# Canvas2D Component — Design Spec

**Date:** 2026-06-09
**Status:** Approved
**Prerequisite:** GitLab #66 compiler macros (merged — `#[canvas.shape]` is in place)

## Overview

A declarative Canvas2D drawing system for SNGL. Shape components are declared as children of a `canvas` component; the `passCanvas` lower pass transforms them into a platform draw function. The initial target is HTML5 canvas. The same mechanism provides a rendering fallback for platforms that lack native UI components.

## SNGL Surface (`lib/canvas.sngl`)

```sngl
import "internal://canvas"

struct CanvasStyle {
    fill color = color{a = 0}
    stroke color = color{a = 0}
    strokeWidth float = 1.0
    lineCap string = "butt"
    lineJoin string = "miter"
    fontSize float = 16.0
    fontFamily string = "sans-serif"
}

struct PathCmd {
    op string
    x float = 0.0
    y float = 0.0
    cx1 float = 0.0
    cy1 float = 0.0
    cx2 float = 0.0
    cy2 float = 0.0
    r float = 0.0
}
// "moveTo" | "lineTo" | "bezierTo" | "arcTo" | "close"

component canvas(width Length, height Length, style CanvasStyle, @click ClickEvent) list<shape> {}

#[canvas.shape]
component rect(x Length, y Length, w Length, h Length, style CanvasStyle) {}

#[canvas.shape]
component circle(cx Length, cy Length, r Length, style CanvasStyle) {}

#[canvas.shape]
component ellipse(cx Length, cy Length, rx Length, ry Length, style CanvasStyle) {}

#[canvas.shape]
component line(x1 Length, y1 Length, x2 Length, y2 Length, style CanvasStyle) {}

#[canvas.shape]
component path(cmds list<PathCmd>, style CanvasStyle) {}

#[canvas.shape]
component text(x Length, y Length, content string, style CanvasStyle) {}

#[canvas.shape]
component image(x Length, y Length, w Length, h Length, src string) {}
```

The `canvas` component is the outer container; it is **not** a shape. It may carry event props. Built-in shape components are registered via `#[canvas.shape]`; user-defined shapes may use the same macro.

## `#[canvas.shape]` Macro — Extended Behavior

The existing `shapeHandler` in `internal/macros/canvas/canvas.go` sets `IsShape = true` and rejects event props. It is extended to also rewrite `ChildrenType`:

- If `comp.ChildrenType` is nil or `list<component>` → rewrite to `list<shape>`
- If `comp.ChildrenType` is already `list<shape>` → leave unchanged
- If `comp.ChildrenType` is anything else → `ERROR(expand) "shape components may only have list<shape> children"`

This means shape authors never need to write `list<shape>` explicitly — the macro handles it.

## Type System — `shape`

`shape` is a new built-in virtual type, declared alongside `component` in the checker's pre-declared types.

Rules:
- A component with `IsShape = true` is assignable to `shape` at call sites
- `list<shape>` is valid as a `ChildrenType`; only `IsShape = true` component instances may appear as children
- `shape` used outside `list<shape>` (e.g. as a standalone field type or parameter type) → `ERROR(check) "shape is only valid as a children type (list<shape>)"`
- A non-shape component used as a child of a `list<shape>` body → `ERROR(check) "expected shape component"`

## Lower Pass — `passCanvas`

New file `internal/lower/pass_canvas.go`. Feature flag: `lower.Canvas`. Platforms that support canvas rendering set this flag.

**Input:** IR for a component instance whose called component has `ChildrenType = list<shape>` (i.e. the `canvas` component or any shape with shape children).

**Transform:**

1. Collect the shape children from the component body in declaration order
2. Generate a draw function (`ir.Func`) that accepts a platform draw context and calls draw intrinsics for each shape
3. Style application: emit `canvas.applyStyle` before each shape's draw call; emit `canvas.save`/`canvas.restore` around nested shape groups
4. Replace the `list<shape>` children in the IR with a reference to the generated draw function
5. Recurse into nested shape children (shapes with `list<shape>` children become nested save/restore groups)

**Bounding-box metadata:** the lower pass records each shape's bounding-box expression as IR metadata. The initial HTML implementation clears the full canvas on each redraw; the bounding-box metadata is emitted for future dirty-region optimisation without a second pass.

**Draw intrinsics** (registered per platform, called by the lowered draw function):

| Intrinsic            | Signature                     |
|----------------------|-------------------------------|
| `canvas.applyStyle`  | `(ctx, style CanvasStyle)`    |
| `canvas.save`        | `(ctx)`                       |
| `canvas.restore`     | `(ctx)`                       |
| `canvas.drawRect`    | `(ctx, x, y, w, h)`           |
| `canvas.drawCircle`  | `(ctx, cx, cy, r)`            |
| `canvas.drawEllipse` | `(ctx, cx, cy, rx, ry)`       |
| `canvas.drawLine`    | `(ctx, x1, y1, x2, y2)`       |
| `canvas.drawPath`    | `(ctx, cmds list<PathCmd>)`   |
| `canvas.drawText`    | `(ctx, x, y, content, style)` |
| `canvas.drawImage`   | `(ctx, x, y, w, h, src)`      |

## HTML Platform

New file `codegen/platform/html/canvas.go`.

**Element:** `canvas` component emits `<canvas width="..." height="...">`. The draw function becomes a JS function `function _draw(ctx) { ... }`.

**Fill/stroke rule:** for all shape draw calls, fill is applied if `style.fill.a > 0`; stroke is applied if `style.stroke.a > 0`. Both may fire on the same shape. Neither fires if alpha is zero (transparent default).

**Intrinsic mappings:**

| SNGL intrinsic                   | Canvas 2D API                                                                                                   |
|----------------------------------|-----------------------------------------------------------------------------------------------------------------|
| `canvas.applyStyle`              | set `fillStyle`, `strokeStyle`, `lineWidth`, `lineCap`, `lineJoin`, `font`                                      |
| `canvas.save` / `canvas.restore` | `ctx.save()` / `ctx.restore()`                                                                                  |
| `canvas.drawRect`                | `ctx.fillRect(x,y,w,h)` if `style.fill.a > 0`; `ctx.strokeRect(x,y,w,h)` if `style.stroke.a > 0`; both may fire |
| `canvas.drawCircle`              | `ctx.beginPath(); ctx.arc(cx,cy,r,0,Math.PI*2); ctx.fill()/stroke()`                                            |
| `canvas.drawEllipse`             | `ctx.beginPath(); ctx.ellipse(cx,cy,rx,ry,0,0,Math.PI*2); ctx.fill()/stroke()`                                  |
| `canvas.drawLine`                | `ctx.beginPath(); ctx.moveTo(x1,y1); ctx.lineTo(x2,y2); ctx.stroke()`                                           |
| `canvas.drawPath`                | `ctx.beginPath()` + command loop                                                                                |
| `canvas.drawText`                | `ctx.fillText(content,x,y)` and/or `ctx.strokeText`                                                             |
| `canvas.drawImage`               | `ctx.drawImage(img,x,y,w,h)` via `new Image()` load                                                             |

**Reactivity:** the generated JS wraps `_draw` in a redraw scheduler:

```js
function _scheduleRedraw(el) {
    requestAnimationFrame(() => {
        const ctx = el.getContext("2d");
        ctx.clearRect(0, 0, el.width, el.height);
        _draw(ctx);
    });
}
```

Reactive state changes that affect the canvas subtree call `_scheduleRedraw`. This integrates with the existing HTML mutation model — the canvas element is treated as a single mutation target.

**Events:** `@click`, `@mousemove`, etc. on the `canvas` component map to DOM event listeners on the `<canvas>` element. No per-shape hit testing in this iteration.

## Out of Scope (This Iteration)

- Dirty-region optimisation (bounding-box metadata is emitted but not used for partial clears)
- Per-shape hit testing / pointer events on individual shapes
- Animation (will be addressed as a general state-change animation system)
- Non-HTML platforms (Android, BubbleTea, etc.) — intrinsics are registered per-platform; adding them later is additive
- User-defined macro authorship (user shapes use `#[canvas.shape]` but cannot define new macros)

## Testing

**Testdata fixtures:**
- `testdata/canvas_basic.sngl` — happy-path: `canvas` with `rect`, `circle`, `text` children; no errors
- `testdata/canvas_shape_errors.sngl` — `ERROR(check)` for non-shape child in canvas body, standalone `shape` type usage
- `testdata/macro_canvas_errors.sngl` (existing) — `ERROR(expand)` for `#[canvas.shape]` on var and event-bearing component

**Lower pass tests (`internal/lower/`):**
- Fixture-driven: `passCanvas` on a canvas+shapes IR produces draw-function IR with correct intrinsic call sequence

**HTML codegen snapshot tests (`codegen/platform/html/`):**
- `canvas` component with a `rect` child produces expected `<canvas>` element + JS draw function
- Snapshot covers `applyStyle` + `fillRect` call order
