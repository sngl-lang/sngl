# SNGL Canvas2D Example

Demonstrates the Canvas2D drawing API: shapes, styles, and reactive redraws.

The example has two canvases:

- **Interactive canvas** — a circle whose radius is controlled by Grow/Shrink buttons. Because `radius` is a state variable referenced in the `circle` shape prop, the canvas automatically redraws whenever it changes.
- **Shapes showcase** — a static canvas showing `rect`, `ellipse`, and `line` shapes side by side.

## Run

```
sngl generate --platform html --out out/ examples/canvas/
open out/index.html
```

## Check

```
sngl check examples/canvas/app.sngl
```

## Canvas API used

Everything below comes from `sngl:ui/draw`, which the app imports as
`import draw "sngl:ui/draw"` alongside `import ui "sngl:ui"`.

- `draw.canvas(width, height)` — the container; hosts the shapes below as children
- `draw.rect(x, y, w, h, style)` — filled/stroked rectangle
- `draw.circle(cx, cy, r, style)` — filled/stroked circle
- `draw.ellipse(cx, cy, rx, ry, style)` — filled/stroked ellipse
- `draw.line(x1, y1, x2, y2, style)` — stroked line
- `draw.path(cmds, style)` — a path built from a `list<draw.PathCmd>`
- `draw.canvasText(x, y, content, style)` — drawn text
- `draw.canvasImage(x, y, w, h, src)` — a drawn image
- `draw.CanvasStyle` — struct with `fill`, `stroke`, `strokeWidth`, `lineCap`, `lineJoin`, `fontSize`, `fontFamily`
