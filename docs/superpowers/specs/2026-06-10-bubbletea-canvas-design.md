# Bubbletea Canvas2D — Design Spec

**Date:** 2026-06-10
**Status:** Approved
**Prerequisite:** Canvas2D shared infra + html/fyne/gtk4/android platforms are DONE and green.

## Overview

Bring Canvas2D rendering to the **bubbletea** TUI platform — the last deferred
canvas target. bubbletea is a RenderModel platform: `View()` produces an ANSI
string (via lipgloss) re-rendered on every update. Canvas shapes are rasterized
to an offscreen image and presented into that string.

The work also extracts the gg-based drawing that fyne currently does inline into
a shared runtime package, and adds a framework-agnostic terminal-present
package, so the rendering logic lives in runtime packages (the `pkg/<lang>/<name>`
pattern) rather than being emitted inline.

## Runtime packages

### `pkg/go/canvas` — shared draw runtime

A `Context` wrapping `*gg.Context`. Stateful, mirroring the per-shape sequence
`passCanvas` emits (`ApplyStyle` then a draw call):

```go
type Style struct {
    Fill, Stroke color.NRGBA // alpha 0 => that paint is skipped
    StrokeWidth  float64
    LineCap, LineJoin string
    FontSize     float64
    FontFamily   string
}

type Context struct { /* wraps *gg.Context + pending Style */ }

func New(width, height int) *Context
func (c *Context) Save()
func (c *Context) Restore()
func (c *Context) ApplyStyle(s Style)               // sets the pending style
func (c *Context) Rect(x, y, w, h float64)
func (c *Context) Circle(cx, cy, r float64)
func (c *Context) Ellipse(cx, cy, rx, ry float64)
func (c *Context) Line(x1, y1, x2, y2 float64)
func (c *Context) Path(cmds []PathCmd)
func (c *Context) Text(x, y float64, content string)
func (c *Context) Image(x, y, w, h float64, src string) // gg image decode/draw
func (c *Context) Result() image.Image                  // the rendered image
```

Fill fires when `Style.Fill.A > 0`, stroke when `Style.Stroke.A > 0` — the
existing cross-platform semantics, now centralized. `PathCmd` is a runtime
mirror of the stdlib `PathCmd` (op + coordinates). Used by **both** fyne and
bubbletea.

### `pkg/go/tui` — terminal present runtime (framework-agnostic)

Renders an image to terminal output. Reusable by any Go TUI framework
(bubbletea today; others later) — not bubbletea-specific.

```go
// RenderTerminal renders img into a terminal string sized to cols x rows.
// On first call it detects kitty-graphics support (cached); when available it
// emits kitty graphics escape sequences, otherwise it downsamples to a
// truecolor Unicode half-block grid.
func RenderTerminal(img image.Image, cols, rows int) string
```

- **Half-block fallback:** each character cell is `▀` with foreground = top
  pixel color and background = bottom pixel color → two independently-colored
  pixels per cell, via ANSI truecolor (`\x1b[38;2;r;g;bm` / `48;2`). Works in
  any truecolor terminal, no gating.
- **Kitty path:** encode the image as base64 PNG inside the kitty graphics
  protocol escape (`\x1b_G...\x1b\`). Used only when detection succeeds.
- **Detection:** once, cached. Heuristic via environment (`$KITTY_WINDOW_ID`,
  `$TERM`/`$TERM_PROGRAM` for kitty/wezterm/ghostty). A terminal-query handshake
  is a possible later refinement; env heuristics are the initial approach.

## Codegen

### Shared intrinsic translation (fyne + bubbletea)

The canvas intrinsics translate to `pkg/go/canvas` `Context` calls. The
translator builds a `canvas.Style` from the SNGL `CanvasStyle` fields and emits:

- `CanvasApplyStyle(ctx, s)` → `ctx.ApplyStyle(canvas.Style{Fill: ..., ...})`
- `CanvasDrawRect(ctx,x,y,w,h)` → `ctx.Rect(x, y, w, h)`
- …circle/ellipse/line/path/text/image analogously
- `CanvasSave`/`CanvasRestore` → `ctx.Save()`/`ctx.Restore()`

This replaces fyne's bespoke inline-gg translation and its `_snglColor` /
`paintAround` helpers. The two Go canvas platforms now share one translation
path.

### fyne (refactor)

The draw func emits `Context` calls; the widget is
`canvas.NewImageFromImage(ctx.Result())` (unchanged shape). fyne's inline gg code
is removed. fyne tests must stay green through the refactor.

### bubbletea (new)

- `Capabilities()`: `f.Canvas = true`, `f.ReactiveCanvas = false`. bubbletea is
  RenderModel — `View()` re-runs every update, so the canvas re-rasterizes
  automatically and no `CanvasRedrawStmt` is injected (same rationale as
  android).
- `_canvasDrawN(ctx *canvas.Context)` is emitted as a sequence of `Context`
  calls (shared translation above).
- In the View, the canvas node becomes: construct `canvas.New(w, h)`, run
  `_canvasDrawN(ctx)`, and embed `tui.RenderTerminal(ctx.Result(), cols, rows)`
  into the lipgloss string output.

## Sizing

Canvas `width`/`height` are pixels (the gg image size). For terminal output the
image is downsampled to a cell grid: half-block gives 1 px wide × 2 px tall per
cell, so `cols ≈ width`, `rows ≈ ceil(height/2)`, clamped to a sane maximum.
Kitty uses the full image. TUI canvas is inherently low-res: small `canvasText`
is blocky under half-block and crisp under kitty — acceptable.

## Scope

All shapes: rect, circle, ellipse, line, path, canvasText, canvasImage. Brings
bubbletea to parity with the other platforms. No per-shape hit testing,
animation, or partial redraw (out of scope, consistent with other platforms).

## Testing

- `pkg/go/canvas`: unit tests drawing each primitive and asserting pixels in the
  resulting image (e.g. a filled rect colors the expected region).
- `pkg/go/tui`: half-block render test (image → assert the `▀` + ANSI truecolor
  string for a known small image); a kitty-path test exercised behind forced
  detection.
- bubbletea: a snapshot/golden test that a canvas program renders shapes into
  the View (half-block path, the universal default).
- fyne: existing canvas tests stay green after the refactor onto `pkg/go/canvas`.
- `go tool verify` stays green.

## Out of scope

- Terminal-query handshake for kitty detection (env heuristic first).
- Refactoring gtk4 (cairo) or html (JS) onto the shared Context — they target
  different languages / native APIs; only the two Go platforms (fyne, bubbletea)
  share `pkg/go/canvas`.
- Animation / reactive partial redraw / per-shape pointer events.
