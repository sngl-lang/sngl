# SNGL Canvas2D Example

Demonstrates the Canvas2D drawing API: shapes, styles, and reactive redraws.

The example has two canvases:

- **Interactive canvas** — a circle whose radius is controlled by Grow/Shrink buttons. Because `radius` is a state variable referenced in the `circle` shape prop, the canvas automatically redraws whenever it changes.
- **Shapes showcase** — a static canvas showing `rect`, `ellipse`, and `line` shapes side by side.

## Run

```
sngl generate --platform html --lang none --out out/ examples/canvas/
open out/index.html
```

## Check

```
sngl check examples/canvas/app.sngl
```

## Canvas API used

Everything below comes from `sngl://draw`, which the app dot-imports alongside
`sngl://std`. Shapes live in their own package because `rect`, `line` and
`path` are names an application usually wants for itself.

- `canvas(width, height)` — the container; accepts `list<shape>` children
- `rect(x, y, w, h, style)` — filled/stroked rectangle
- `circle(cx, cy, r, style)` — filled/stroked circle
- `ellipse(cx, cy, rx, ry, style)` — filled/stroked ellipse
- `line(x1, y1, x2, y2, style)` — stroked line
- `path(cmds, style)` — a path built from a `list<PathCmd>`
- `canvasText(x, y, content, style)` — drawn text
- `canvasImage(x, y, w, h, src)` — a drawn image
- `CanvasStyle` — struct with `fill`, `stroke`, `strokeWidth`, `fontSize`, `fontFamily`
