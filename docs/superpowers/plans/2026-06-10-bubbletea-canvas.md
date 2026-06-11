# Bubbletea Canvas2D Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Canvas2D rendering to the bubbletea TUI platform, backed by a shared `pkg/go/canvas` draw runtime (also adopted by fyne) and a framework-agnostic `pkg/go/tui` terminal-present runtime (kitty detection + Unicode half-block fallback).

**Architecture:** A `pkg/go/canvas.Context` wraps `github.com/fogleman/gg` and exposes stateful draw methods (`ApplyStyle` then a primitive). `pkg/go/tui.RenderTerminal` turns the rendered `image.Image` into a terminal string. The fyne and bubbletea intrinsic translators share one helper that emits canvas intrinsics as `Context` method calls. bubbletea is RenderModel, so `View()` re-rasterizes each update (no reactive redraw).

**Tech Stack:** Go, `github.com/fogleman/gg` v1.3.0, charm.land/bubbletea + lipgloss v2, the SNGL `ir`/`codegen`/`lower` packages, the existing canvas lower infra (`passCanvas`, `ir.CanvasIntrinsics`).

**Reference implementation:** `codegen/platform/fyne/canvas.go` is the current inline-gg translation. Tasks 1–3 move its gg logic into `pkg/go/canvas`; Task 6 makes the translation shared; Task 7 refactors fyne onto it. Read it before starting.

---

## File Map

| File                                        | Status | Responsibility                                                                     |
|---------------------------------------------|--------|------------------------------------------------------------------------------------|
| `pkg/go/canvas/canvas.go`                   | Create | `Context` (gg wrapper), `Style`, `PathCmd`, draw methods, `Result()`               |
| `pkg/go/canvas/canvas_test.go`              | Create | Pixel-level tests of each primitive                                                |
| `pkg/go/tui/terminal.go`                    | Create | `RenderTerminal(img, cols, rows)`, kitty detection, half-block + kitty encoders    |
| `pkg/go/tui/terminal_test.go`               | Create | Half-block + kitty render tests                                                    |
| `codegen/canvasutil/gocontext.go`           | Create | Shared: canvas-intrinsic `CallStmt` → `Context` method-call IR (Go)                |
| `codegen/canvasutil/gocontext_test.go`      | Create | Asserts intrinsic → `ctx.Rect(...)` etc.                                           |
| `codegen/platform/fyne/canvas.go`           | Modify | Use the shared helper + `pkg/go/canvas`; drop inline gg/`_snglColor`/`paintAround` |
| `codegen/platform/bubbletea/bubbletea.go`   | Modify | `Capabilities()`: `Canvas=true`, `ReactiveCanvas=false`                            |
| `codegen/platform/bubbletea/canvas.go`      | Create | Emit `_canvasDrawN` (shared helper) + View integration via `tui.RenderTerminal`    |
| `codegen/platform/bubbletea/view_ir.go`     | Modify | Render a canvas node into the View string                                          |
| `codegen/platform/bubbletea/canvas_test.go` | Create | bubbletea canvas golden/codegen test                                               |

---

## Task 1: `pkg/go/canvas` — Context skeleton, Style, PathCmd

**Files:**
- Create: `pkg/go/canvas/canvas.go`
- Create: `pkg/go/canvas/canvas_test.go`

- [ ] **Step 1: Write the failing test**

`pkg/go/canvas/canvas_test.go`:

```go
package canvas

import "testing"

func TestNewResultSize(t *testing.T) {
	c := New(40, 20)
	img := c.Result()
	b := img.Bounds()
	if b.Dx() != 40 || b.Dy() != 20 {
		t.Fatalf("Result size = %dx%d, want 40x20", b.Dx(), b.Dy())
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/go/canvas/ -run TestNewResultSize -v`
Expected: compile error — package/`New` undefined.

- [ ] **Step 3: Write `canvas.go`**

```go
// Package canvas is a small immediate-mode 2D drawing runtime backing SNGL's
// Canvas2D on Go platforms (fyne, bubbletea). It wraps github.com/fogleman/gg
// and is stateful: ApplyStyle sets the pending style, then a primitive call
// (Rect, Circle, ...) fills and/or strokes it. Generated code drives this; it
// is not meant as a public API.
package canvas

import (
	"image"
	"image/color"

	"github.com/fogleman/gg"
)

// Style is the resolved per-shape paint state. A zero-alpha Fill or Stroke
// disables that paint (matches the SNGL CanvasStyle transparent default).
type Style struct {
	Fill        color.NRGBA
	Stroke      color.NRGBA
	StrokeWidth float64
	LineCap     string
	LineJoin    string
	FontSize    float64
	FontFamily  string
}

// PathCmd mirrors the SNGL stdlib PathCmd (op + coordinates).
type PathCmd struct {
	Op                 string // "moveTo" | "lineTo" | "bezierTo" | "arcTo" | "close"
	X, Y               float64
	Cx1, Cy1, Cx2, Cy2 float64
	R                  float64
}

// Context is a stateful 2D drawing surface.
type Context struct {
	dc      *gg.Context
	pending Style
}

// New creates a Context backed by a width x height pixel image.
func New(width, height int) *Context {
	return &Context{dc: gg.NewContext(width, height)}
}

// Result returns the rendered image.
func (c *Context) Result() image.Image { return c.dc.Image() }

// Save / Restore bracket transform/clip state.
func (c *Context) Save()    { c.dc.Push() }
func (c *Context) Restore() { c.dc.Pop() }

// ApplyStyle sets the style used by the next primitive.
func (c *Context) ApplyStyle(s Style) { c.pending = s }
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./pkg/go/canvas/ -run TestNewResultSize -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/go/canvas/canvas.go pkg/go/canvas/canvas_test.go
git commit -m "feat(pkg/canvas): Context skeleton, Style, PathCmd"
```

---

## Task 2: `pkg/go/canvas` — Rect/Circle/Ellipse/Line with alpha-gated paint

**Files:**
- Modify: `pkg/go/canvas/canvas.go`
- Modify: `pkg/go/canvas/canvas_test.go`

- [ ] **Step 1: Write the failing test**

Append to `canvas_test.go`:

```go
import (
	"image/color"
	"testing"
)

func nrgbaAt(t *testing.T, c *Context, x, y int) color.NRGBA {
	t.Helper()
	r, g, b, a := c.Result().At(x, y).RGBA()
	return color.NRGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}

func TestRectFills(t *testing.T) {
	c := New(20, 20)
	c.ApplyStyle(Style{Fill: color.NRGBA{255, 0, 0, 255}})
	c.Rect(5, 5, 10, 10)
	if got := nrgbaAt(t, c, 10, 10); got.R != 255 || got.G != 0 || got.B != 0 {
		t.Errorf("center pixel = %+v, want red", got)
	}
	if got := nrgbaAt(t, c, 0, 0); got.A != 0 {
		t.Errorf("corner pixel = %+v, want transparent (no fill)", got)
	}
}

func TestLineStrokesOnly(t *testing.T) {
	c := New(20, 20)
	c.ApplyStyle(Style{Stroke: color.NRGBA{0, 0, 255, 255}, StrokeWidth: 2})
	c.Line(0, 10, 20, 10)
	if got := nrgbaAt(t, c, 10, 10); got.B != 255 {
		t.Errorf("on-line pixel = %+v, want blue", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/go/canvas/ -run 'TestRectFills|TestLineStrokesOnly' -v`
Expected: FAIL — `Rect`/`Line` undefined.

- [ ] **Step 3: Implement the primitives + paint helpers**

Append to `canvas.go`:

```go
func nrgbaToColor(c color.NRGBA) color.Color { return c }

// paint fills (when Fill.A>0) then strokes (when Stroke.A>0) the current path.
// FillPreserve keeps the path for the subsequent stroke.
func (c *Context) paint() {
	s := c.pending
	if s.Fill.A > 0 {
		c.dc.SetColor(nrgbaToColor(s.Fill))
		if s.Stroke.A > 0 {
			c.dc.FillPreserve()
		} else {
			c.dc.Fill()
		}
	}
	if s.Stroke.A > 0 {
		if s.StrokeWidth > 0 {
			c.dc.SetLineWidth(s.StrokeWidth)
		}
		c.dc.SetColor(nrgbaToColor(s.Stroke))
		c.dc.Stroke()
	}
	// Clear any path left if neither paint fired.
	c.dc.ClearPath()
}

// strokeOnly strokes the current path with the stroke color (used for lines).
func (c *Context) strokeOnly() {
	s := c.pending
	if s.Stroke.A > 0 {
		if s.StrokeWidth > 0 {
			c.dc.SetLineWidth(s.StrokeWidth)
		}
		c.dc.SetColor(nrgbaToColor(s.Stroke))
	}
	c.dc.Stroke()
}

func (c *Context) Rect(x, y, w, h float64)        { c.dc.DrawRectangle(x, y, w, h); c.paint() }
func (c *Context) Circle(cx, cy, r float64)       { c.dc.DrawCircle(cx, cy, r); c.paint() }
func (c *Context) Ellipse(cx, cy, rx, ry float64) { c.dc.DrawEllipse(cx, cy, rx, ry); c.paint() }
func (c *Context) Line(x1, y1, x2, y2 float64)    { c.dc.DrawLine(x1, y1, x2, y2); c.strokeOnly() }
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./pkg/go/canvas/ -run 'TestRectFills|TestLineStrokesOnly' -v`
Expected: PASS. (If `ClearPath` is not in gg v1.3.0, drop that line — `Fill`/`Stroke` already clear the path; verify with `go doc github.com/fogleman/gg.Context`.)

- [ ] **Step 5: Commit**

```bash
git add pkg/go/canvas/
git commit -m "feat(pkg/canvas): rect/circle/ellipse/line with alpha-gated paint"
```

---

## Task 3: `pkg/go/canvas` — Path, Text, Image

**Files:**
- Modify: `pkg/go/canvas/canvas.go`
- Modify: `pkg/go/canvas/canvas_test.go`

- [ ] **Step 1: Write the failing test**

Append to `canvas_test.go`:

```go
func TestPathFills(t *testing.T) {
	c := New(20, 20)
	c.ApplyStyle(Style{Fill: color.NRGBA{0, 255, 0, 255}})
	c.Path([]PathCmd{
		{Op: "moveTo", X: 2, Y: 2},
		{Op: "lineTo", X: 18, Y: 2},
		{Op: "lineTo", X: 18, Y: 18},
		{Op: "close"},
	})
	if got := nrgbaAt(t, c, 15, 10); got.G != 255 {
		t.Errorf("inside-triangle pixel = %+v, want green", got)
	}
}

func TestTextDoesNotPanic(t *testing.T) {
	c := New(60, 20)
	c.ApplyStyle(Style{Fill: color.NRGBA{0, 0, 0, 255}, FontSize: 12})
	c.Text(2, 14, "hi") // gg uses a built-in basic font when none is set.
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/go/canvas/ -run 'TestPathFills|TestTextDoesNotPanic' -v`
Expected: FAIL — `Path`/`Text` undefined.

- [ ] **Step 3: Implement Path, Text, Image**

Append to `canvas.go` (add `"github.com/fogleman/gg"` is already imported):

```go
func (c *Context) Path(cmds []PathCmd) {
	for _, cmd := range cmds {
		switch cmd.Op {
		case "moveTo":
			c.dc.MoveTo(cmd.X, cmd.Y)
		case "lineTo":
			c.dc.LineTo(cmd.X, cmd.Y)
		case "bezierTo":
			c.dc.CubicTo(cmd.Cx1, cmd.Cy1, cmd.Cx2, cmd.Cy2, cmd.X, cmd.Y)
		case "arcTo":
			// gg has no arcTo; approximate with a line to the endpoint.
			c.dc.LineTo(cmd.X, cmd.Y)
		case "close":
			c.dc.ClosePath()
		}
	}
	c.paint()
}

func (c *Context) Text(x, y float64, content string) {
	s := c.pending
	if s.Fill.A > 0 {
		c.dc.SetColor(nrgbaToColor(s.Fill))
	}
	c.dc.DrawString(content, x, y)
}

// Image decodes src (a local file path) and draws it scaled into (x,y,w,h).
// A missing/undecodable path draws nothing.
func (c *Context) Image(x, y, w, h float64, src string) {
	img, err := gg.LoadImage(src)
	if err != nil {
		return
	}
	c.Save()
	ib := img.Bounds()
	if ib.Dx() > 0 && ib.Dy() > 0 {
		c.dc.Scale(w/float64(ib.Dx()), h/float64(ib.Dy()))
		c.dc.DrawImage(img, int(x/(w/float64(ib.Dx()))), int(y/(h/float64(ib.Dy()))))
	}
	c.Restore()
}
```

Note: confirm `gg.LoadImage`, `Context.DrawString`, `Context.Scale`, `Context.DrawImage`, `Context.ClosePath`, `Context.CubicTo` exist in gg v1.3.0 via `go doc github.com/fogleman/gg`. If `DrawImage` scaling is awkward, simplify `Image` to draw unscaled at (int(x),int(y)) and leave scaling as a follow-up — keep it non-panicking.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./pkg/go/canvas/ -run 'TestPathFills|TestTextDoesNotPanic' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/go/canvas/
git commit -m "feat(pkg/canvas): path, text, image primitives"
```

---

## Task 4: `pkg/go/tui` — half-block terminal render

**Files:**
- Create: `pkg/go/tui/terminal.go`
- Create: `pkg/go/tui/terminal_test.go`

- [ ] **Step 1: Write the failing test**

`pkg/go/tui/terminal_test.go`:

```go
package tui

import (
	"image"
	"image/color"
	"strings"
	"testing"
)

func TestHalfBlockKnownImage(t *testing.T) {
	// 2x2: top row red, bottom row blue. One cell wide, one cell tall.
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.NRGBA{255, 0, 0, 255})
	img.Set(1, 0, color.NRGBA{255, 0, 0, 255})
	img.Set(0, 1, color.NRGBA{0, 0, 255, 255})
	img.Set(1, 1, color.NRGBA{0, 0, 255, 255})

	out := halfBlock(img, 2, 1)
	if !strings.Contains(out, "▀") {
		t.Errorf("expected upper-half-block ▀ in output, got %q", out)
	}
	if !strings.Contains(out, "38;2;255;0;0") { // fg = top = red
		t.Errorf("expected red foreground truecolor, got %q", out)
	}
	if !strings.Contains(out, "48;2;0;0;255") { // bg = bottom = blue
		t.Errorf("expected blue background truecolor, got %q", out)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/go/tui/ -run TestHalfBlockKnownImage -v`
Expected: FAIL — `halfBlock` undefined.

- [ ] **Step 3: Implement `terminal.go` (half-block only for now)**

```go
// Package tui renders a 2D image into terminal output. It is framework-
// agnostic (any Go TUI can use it). RenderTerminal emits kitty graphics
// escapes when the terminal supports them, otherwise a truecolor Unicode
// half-block grid.
package tui

import (
	"fmt"
	"image"
	"strings"
)

// RenderTerminal renders img to a terminal string sized to cols x rows cells.
func RenderTerminal(img image.Image, cols, rows int) string {
	if kittySupported() {
		return kitty(img)
	}
	return halfBlock(img, cols, rows)
}

// sampleAt nearest-neighbor samples img at the given fraction (0..1).
func sampleAt(img image.Image, fx, fy float64) (r, g, b, a uint8) {
	b2 := img.Bounds()
	x := b2.Min.X + int(fx*float64(b2.Dx()))
	y := b2.Min.Y + int(fy*float64(b2.Dy()))
	if x >= b2.Max.X {
		x = b2.Max.X - 1
	}
	if y >= b2.Max.Y {
		y = b2.Max.Y - 1
	}
	cr, cg, cb, ca := img.At(x, y).RGBA()
	return uint8(cr >> 8), uint8(cg >> 8), uint8(cb >> 8), uint8(ca >> 8)
}

// halfBlock downsamples img to cols x rows cells. Each cell is ▀ with
// foreground = the top sub-pixel and background = the bottom sub-pixel, so one
// character cell shows two vertically-stacked colored pixels.
func halfBlock(img image.Image, cols, rows int) string {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	var b strings.Builder
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			fx := (float64(col) + 0.5) / float64(cols)
			topFy := (float64(row*2) + 0.5) / float64(rows*2)
			botFy := (float64(row*2+1) + 0.5) / float64(rows*2)
			tr, tg, tb, _ := sampleAt(img, fx, topFy)
			br, bg, bb, _ := sampleAt(img, fx, botFy)
			fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", tr, tg, tb, br, bg, bb)
		}
		b.WriteString("\x1b[0m")
		if row < rows-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
```

Add a temporary stub so it compiles (Task 5 fills these in):

```go
func kittySupported() bool         { return false }
func kitty(img image.Image) string { return "" }
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./pkg/go/tui/ -run TestHalfBlockKnownImage -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/go/tui/
git commit -m "feat(pkg/tui): truecolor half-block terminal render"
```

---

## Task 5: `pkg/go/tui` — kitty detection + encode

**Files:**
- Modify: `pkg/go/tui/terminal.go`
- Modify: `pkg/go/tui/terminal_test.go`

- [ ] **Step 1: Write the failing test**

Append to `terminal_test.go`:

```go
func TestKittyEncodeHasGraphicsEscape(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	out := kitty(img)
	if !strings.HasPrefix(out, "\x1b_G") {
		t.Errorf("kitty output must start with the graphics escape, got %q", out[:min(8, len(out))])
	}
	if !strings.HasSuffix(out, "\x1b\\") {
		t.Errorf("kitty output must end with ST, got %q", out)
	}
}

func TestKittyDetectionFromEnv(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	resetKittyDetection()
	if !kittySupported() {
		t.Error("KITTY_WINDOW_ID set should report kitty supported")
	}
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "")
	resetKittyDetection()
	if kittySupported() {
		t.Error("plain xterm should not report kitty supported")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/go/tui/ -run 'Kitty' -v`
Expected: FAIL — `resetKittyDetection` undefined; `kitty`/`kittySupported` are stubs.

- [ ] **Step 3: Replace the stubs**

Replace the two stub functions in `terminal.go` with:

```go
import (
	"bytes"
	"encoding/base64"
	"image/png"
	"os"
	"strings"
	"sync"
)

var (
	kittyOnce sync.Once
	kittyOK   bool
)

// resetKittyDetection clears the cached detection (test-only seam).
func resetKittyDetection() { kittyOnce = sync.Once{} }

// kittySupported reports whether the terminal supports the kitty graphics
// protocol, via environment heuristics, cached after the first call.
func kittySupported() bool {
	kittyOnce.Do(func() {
		if os.Getenv("KITTY_WINDOW_ID") != "" {
			kittyOK = true
			return
		}
		term := os.Getenv("TERM")
		prog := os.Getenv("TERM_PROGRAM")
		for _, hay := range []string{term, prog} {
			h := strings.ToLower(hay)
			if strings.Contains(h, "kitty") || strings.Contains(h, "wezterm") || strings.Contains(h, "ghostty") {
				kittyOK = true
				return
			}
		}
	})
	return kittyOK
}

// kitty encodes img as a single base64 PNG kitty graphics escape sequence
// (a=T: transmit + display). Chunking for very large payloads is a follow-up.
func kitty(img image.Image) string {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return ""
	}
	payload := base64.StdEncoding.EncodeToString(buf.Bytes())
	return "\x1b_Gf=100,a=T;" + payload + "\x1b\\"
}
```

Add the `bytes`/`encoding/base64`/`image/png`/`os`/`sync` imports to the file's import block (merge with the existing `fmt`/`image`/`strings`).

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./pkg/go/tui/ -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/go/tui/
git commit -m "feat(pkg/tui): kitty graphics detection + encode"
```

---

## Task 6: Shared codegen — canvas intrinsic → `Context` method-call IR

**Files:**
- Create: `codegen/canvasutil/gocontext.go`
- Create: `codegen/canvasutil/gocontext_test.go`

This centralizes the intrinsic→Go translation both fyne and bubbletea use. It returns `[]ir.Stmt` that a `GoIRContext` renders as `ctx.<Method>(...)` calls. Read `codegen/platform/fyne/canvas.go` (`translateCanvasIntrinsic`, `methodStmt`, `styleField`) for the existing IR-construction pattern; this generalizes it to emit `pkg/go/canvas` calls.

- [ ] **Step 1: Read the fyne pattern**

```bash
sed -n '90,235p' codegen/platform/fyne/canvas.go
```

Note: `methodStmt(recv, name, args...)` builds `ir.CallStmt{Call: ir.Call{Receiver: recv, Func: {Name: name}, Args: ...}}`; `styleField(style, "fill")` builds `ir.Select{Operand: style, Field: "fill"}`. The shared helper reuses these shapes.

- [ ] **Step 2: Write the failing test**

`codegen/canvasutil/gocontext_test.go`:

```go
package canvasutil

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// renderStmts renders IR statements to Go via a bare GoIRContext.
func renderStmts(stmts []ir.Stmt) string {
	gc := golang.NewIRContext(nil)
	var b strings.Builder
	for _, s := range stmts {
		for _, line := range gc.EvalStmt(s) {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

func TestRectIntrinsicEmitsContextCall(t *testing.T) {
	ctx := &ir.Ident{Name: "ctx", Type: ir.TypDyn}
	call := &ir.Call{
		Type: ir.TypVoid,
		Func: &ir.Func{Intrinsic: "CanvasDrawRect"},
		Args: []ir.CallArg{
			{Value: ctx},
			{Value: &ir.Literal{Type: ir.TypFloat, Raw: "1"}},
			{Value: &ir.Literal{Type: ir.TypFloat, Raw: "2"}},
			{Value: &ir.Literal{Type: ir.TypFloat, Raw: "3"}},
			{Value: &ir.Literal{Type: ir.TypFloat, Raw: "4"}},
		},
	}
	got := renderStmts(GoContextStmts(&ir.CallStmt{Call: call}, nil))
	if !strings.Contains(got, "ctx.Rect(") {
		t.Errorf("want ctx.Rect(...) call, got:\n%s", got)
	}
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./codegen/canvasutil/ -run TestRectIntrinsicEmitsContextCall -v`
Expected: FAIL — `GoContextStmts` undefined. (If `golang.NewIRContext(nil)` panics on a nil ctx, construct a minimal `*codegen.ExprCtx` — check `golang.NewIRContext`'s signature and mirror how `codegen/lang/golang` tests build a context.)

- [ ] **Step 4: Implement `gocontext.go`**

```go
package canvasutil

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

// canvasPkgStyle is the qualified Go type for the runtime style struct.
const canvasPkgStyle = "canvas.Style"

// pendingStyle tracks the style bound by the most recent CanvasApplyStyle so
// the following primitive can pass it. Callers thread one *GoCanvasState per
// draw-func translation.
type GoCanvasState struct {
	styleVar string // name of the bound _styleN local, or "" for none
	seq      int
}

// GoContextStmts translates one canvas-intrinsic CallStmt into pkg/go/canvas
// Context method-call statements. ctx (arg 0) is the receiver. st carries the
// pending-style binding across the ApplyStyle→primitive pair; pass the same
// *GoCanvasState for every stmt of one draw func (or nil for a throwaway).
func GoContextStmts(cs *ir.CallStmt, st *GoCanvasState) []ir.Stmt {
	if st == nil {
		st = &GoCanvasState{}
	}
	call := cs.Call
	ctx := call.Args[0].Value
	rest := call.Args[1:]
	arg := func(i int) ir.Expr { return rest[i].Value }
	method := func(name string, args ...ir.Expr) ir.Stmt {
		return &ir.CallStmt{Call: &ir.Call{
			Type:     ir.TypVoid,
			Receiver: ctx,
			Func:     &ir.Func{Name: name},
			Args:     callArgs(args),
		}}
	}
	styleRef := func() ir.Expr {
		if st.styleVar == "" {
			return &ir.Ident{Name: canvasPkgStyle + "{}", Type: ir.TypDyn}
		}
		return &ir.Ident{Name: st.styleVar, Type: ir.TypDyn}
	}
	switch call.Func.Intrinsic {
	case "CanvasSave":
		return []ir.Stmt{method("Save")}
	case "CanvasRestore":
		st.styleVar = ""
		return []ir.Stmt{method("Restore")}
	case "CanvasApplyStyle":
		st.seq++
		name := "_style" + itoa(st.seq)
		st.styleVar = name
		// var _styleN = canvas.Style{Fill: canvas.nrgba(style.fill), ...}
		return []ir.Stmt{&ir.LocalVar{Name: name, Init: styleLit(arg(0))}}
	case "CanvasDrawRect":
		return []ir.Stmt{method("ApplyStyle", styleRef()), method("Rect", arg(0), arg(1), arg(2), arg(3))}
	case "CanvasDrawCircle":
		return []ir.Stmt{method("ApplyStyle", styleRef()), method("Circle", arg(0), arg(1), arg(2))}
	case "CanvasDrawEllipse":
		return []ir.Stmt{method("ApplyStyle", styleRef()), method("Ellipse", arg(0), arg(1), arg(2), arg(3))}
	case "CanvasDrawLine":
		return []ir.Stmt{method("ApplyStyle", styleRef()), method("Line", arg(0), arg(1), arg(2), arg(3))}
	case "CanvasDrawPath":
		return []ir.Stmt{method("ApplyStyle", styleRef()), method("Path", arg(0))}
	case "CanvasDrawText":
		return []ir.Stmt{method("ApplyStyle", styleRef()), method("Text", arg(0), arg(1), arg(2))}
	case "CanvasDrawImage":
		return []ir.Stmt{method("Image", arg(0), arg(1), arg(2), arg(3), arg(4))}
	}
	return nil
}
```

You must implement the helpers used above, in the same file:
- `callArgs([]ir.Expr) []ir.CallArg` — wrap each expr as a positional `ir.CallArg{Value: e}`.
- `itoa(int) string` — `strconv.Itoa`.
- `styleLit(styleExpr ir.Expr) ir.Expr` — build an `ir.StructLit` (or a raw `ir.Ident` with a composite-literal name, mirroring the `CanvasStyle{}` pattern fyne uses) for `canvas.Style{Fill: <nrgba(style.fill)>, Stroke: <nrgba(style.stroke)>, StrokeWidth: style.strokeWidth, LineCap: style.lineCap, LineJoin: style.lineJoin, FontSize: style.fontSize, FontFamily: style.fontFamily}`. The SNGL `color` struct has `r,g,b,a` (0..255); `canvas.Style.Fill` is `color.NRGBA{R,G,B,A uint8}`. Emit `color.NRGBA{R: uint8(style.fill.r), G: uint8(style.fill.g), B: uint8(style.fill.b), A: uint8(style.fill.a)}` — i.e. helper `nrgbaLit(styleExpr, "fill")` builds that `ir.StructLit`. (Look at how `codegen/lang/golang` renders `ir.StructLit` and `ir.Select` to confirm field-access + composite-literal rendering. The generated code needs `import "image/color"`.)

Keep `GoContextStmts` the single source of truth — fyne (Task 7) and bubbletea (Task 8) both call it.

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./codegen/canvasutil/ -run TestRectIntrinsicEmitsContextCall -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add codegen/canvasutil/gocontext.go codegen/canvasutil/gocontext_test.go
git commit -m "feat(canvasutil): shared canvas-intrinsic → pkg/go/canvas Context IR"
```

---

## Task 7: Refactor fyne onto `pkg/go/canvas` + the shared helper

**Files:**
- Modify: `codegen/platform/fyne/canvas.go`

- [ ] **Step 1: Read the current fyne translation + emission**

```bash
sed -n '1,330p' codegen/platform/fyne/canvas.go
```

Identify: `translateCanvasIntrinsic`, `paintAround`/`strokeAround`, `methodStmt`, `_snglColor`/`snglColorHelper`, the gg-context construction (`gg.NewContext`), and where the widget is built (`canvas.NewImageFromImage(ctx...)`).

- [ ] **Step 2: Run the existing fyne canvas tests (baseline, must stay green)**

Run: `go test ./codegen/platform/fyne/ -run Canvas -v`
Expected: PASS (records current behavior before refactor).

- [ ] **Step 3: Replace the intrinsic translation with the shared helper**

In `fyneTranslator`, change `translateCanvasIntrinsic` to delegate:

```go
func (t *fyneTranslator) translateCanvasIntrinsic(cs *ir.CallStmt) []ir.Stmt {
	return canvasutil.GoContextStmts(cs, t.canvasState) // t.canvasState *canvasutil.GoCanvasState, created per draw func
}
```

Add `canvasState *canvasutil.GoCanvasState` to `fyneTranslator` and initialize it where the translator is built for a draw func. Remove `paintAround`, `strokeAround`, the per-intrinsic gg cases, `_snglColor`/`snglColorHelper`, and the `styleField`/`fillColorStmt`/`strokeColorStmt` helpers that are now unused. Change the draw-func ctx type and the gg-context construction so the draw func receives `*canvas.Context` (from `pkg/go/canvas`), built via `canvas.New(w, h)`, and the widget is `canvas.NewImageFromImage(ctx.Result())` (note: `canvas` is now ambiguous — `fyne.io/fyne/v2/canvas` vs `pkg/go/canvas`. Alias the SNGL runtime import, e.g. `import snglcanvas "git.duckfam.us/jonathan/sngl/pkg/go/canvas"` and emit `snglcanvas.New(...)`/`snglcanvas.Context`; keep fyne's `canvas.NewImageFromImage`). Update the import-injection accordingly.

Also: the canvas stdlib struct decls (`Color`/`CanvasStyle`/`PathCmd`) emitted by `canvasStdlibDeclsExcluding` are still needed for the *SNGL* side (the draw func reads `style.fill.r` etc.), so keep those. Only the gg drawing moves to the runtime.

- [ ] **Step 4: Run fyne tests + build a real app**

Run: `go test ./codegen/platform/fyne/...`
Then generate + build the example to confirm the emitted Go compiles against `pkg/go/canvas`:

```bash
go install ./cmd/sngl
sngl generate --platform fyne --lang go -o /tmp/fcv examples/canvas/app.sngl
# add go.mod replace + a func main, then: (cd /tmp/fcv && go build ./...)
```

Expected: fyne canvas tests PASS; emitted Go references `snglcanvas.New`/`ctx.Rect`/`ctx.Result()` and compiles.

- [ ] **Step 5: Update fyne's canvas tests for the new emission**

The fyne canvas test (`codegen/platform/fyne/canvas_test.go`) asserts gg snippets (`gg.NewContext`, `DrawRectangle`, `_snglColor`). Update those assertions to the new form (`snglcanvas.New`, `ctx.Rect(`, `ctx.Result()`, no `_snglColor`). Keep `TestCanvas_RendersRealPixels` (it renders through the real pipeline — it should still produce non-uniform pixels via the runtime).

- [ ] **Step 6: Commit**

```bash
git add codegen/platform/fyne/canvas.go codegen/platform/fyne/canvas_test.go
git commit -m "refactor(fyne): draw canvas via shared pkg/go/canvas Context"
```

---

## Task 8: bubbletea — enable Canvas + emit draw func + View integration

**Files:**
- Modify: `codegen/platform/bubbletea/bubbletea.go`
- Create: `codegen/platform/bubbletea/canvas.go`
- Modify: `codegen/platform/bubbletea/view_ir.go`
- Create: `codegen/platform/bubbletea/canvas_test.go`

- [ ] **Step 1: Read how bubbletea emits the View + synthesized funcs**

```bash
sed -n '1,120p' codegen/platform/bubbletea/view_ir.go
grep -n "Synthesized\|CanvasDraw\|func.*View\|renderNode\|RenderModel" codegen/platform/bubbletea/view_ir.go codegen/platform/bubbletea/compiler_ir.go
```

Identify where a `NodeInst` becomes View output, and how Synthesized funcs (`_canvasDrawN`) would be emitted (mirror the html/android canvas-draw-func emission — the draw func body is a sequence of canvas intrinsics that `canvasutil.GoContextStmts` now translates).

- [ ] **Step 2: Enable the capability**

In `bubbletea.go` `Capabilities()` add:

```go
f.Canvas = true
f.ReactiveCanvas = false // RenderModel re-runs View() each update; no explicit redraw
```

- [ ] **Step 3: Write the failing test**

`codegen/platform/bubbletea/canvas_test.go` — generate a canvas program for bubbletea and assert the emitted Go:

```go
package bubbletea

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/lower"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

func TestBubbleteaCanvasEmission(t *testing.T) {
	src := `component main {
    canvas(width=40px, height=20px) {
        rect(x=0.0, y=0.0, w=40.0, h=20.0, style=CanvasStyle{fill=color{r=10, g=20, b=30, a=255}}) {}
    }
}`
	doc, err := parser.Parse("t.sngl", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	pkg, diags := checker.Check(doc, &checker.Config{IsMain: true, Platforms: codegen.CollectPlatforms()})
	for _, d := range diags {
		if d.Severity == ir.Error {
			t.Fatalf("check: %s", d.Msg)
		}
	}
	g := &Generator{}
	lang := codegen.LookupLang("go")
	if err := lower.Lower(pkg, g.Capabilities(lang).ToLowerCaps(), lower.Options{Platform: "bubbletea"}); err != nil {
		t.Fatal(err)
	}
	mem := codegen.NewMemSink()
	if err := g.Generate(&codegen.Request{Pkg: pkg, Lang: lang, Source: "t.sngl"}, mem); err != nil {
		t.Fatalf("generate: %v", err)
	}
	out := ""
	for _, f := range mem.Files() {
		out += string(f)
	}
	for _, want := range []string{"snglcanvas.New(40, 20)", "ctx.Rect(", "tui.RenderTerminal("} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in emitted bubbletea output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Canvas") && strings.Contains(out, "TODO") {
		t.Errorf("canvas left as TODO:\n%s", out)
	}
}
```

- [ ] **Step 4: Run to verify it fails**

Run: `go test ./codegen/platform/bubbletea/ -run TestBubbleteaCanvasEmission -v`
Expected: FAIL — no canvas emission yet.

- [ ] **Step 5: Implement `bubbletea/canvas.go` + view integration**

Create `codegen/platform/bubbletea/canvas.go` with:
- `emitCanvasDrawFunc(...)`: emit `func _canvasDrawN(ctx *snglcanvas.Context) { <GoContextStmts for each intrinsic> }`, translating the draw func body via `canvasutil.GoContextStmts` with one `*canvasutil.GoCanvasState`. Mirror how fyne emits its draw func (Task 7) — both render Go via `GoIRContext`.
- A helper that, given the canvas `NodeInst` (with `CanvasDraw` + width/height), produces the View fragment: construct `snglcanvas.New(w, h)`, call `_canvasDrawN(ctx)`, and embed `tui.RenderTerminal(ctx.Result(), cols, rows)` (with `cols = w`, `rows = (h+1)/2`, clamped — define a small `canvasCellGrid(w, h) (cols, rows int)` helper) into the lipgloss string output.

In `view_ir.go`, where a `NodeInst` is rendered to the View, add a case: when `n.CanvasDraw != nil` (or the flattened `LocalVar.CanvasDraw`, depending on bubbletea's flattening — check whether bubbletea sets `NoDeclarative`; it does not in `Capabilities`, so the canvas arrives as a `NodeInst` like html/android), emit the View fragment above instead of treating it as a normal node.

Required imports for generated code: `snglcanvas "git.duckfam.us/jonathan/sngl/pkg/go/canvas"` and `tui "git.duckfam.us/jonathan/sngl/pkg/go/tui"` — register them via bubbletea's import mechanism (see `compiler_ir.go` `RequireImport` usage). Also emit the SNGL `Color`/`CanvasStyle`/`PathCmd` Go decls (reuse `canvasutil.StructDeclsExcluding`, as fyne/gtk4 do) since the draw func reads `style.fill.r` etc.

- [ ] **Step 6: Run the test**

Run: `go test ./codegen/platform/bubbletea/ -run TestBubbleteaCanvasEmission -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add codegen/platform/bubbletea/
git commit -m "feat(bubbletea): canvas2d via pkg/go/canvas + pkg/go/tui half-block/kitty"
```

---

## Task 9: Integration — build the example, full verify

**Files:**
- (No new source; this is the end-to-end gate.)

- [ ] **Step 1: Generate + build the canvas example for bubbletea**

```bash
go install ./cmd/sngl
root=$(pwd)
sngl generate --platform bubbletea --lang go -o /tmp/btcv examples/canvas/app.sngl
# add a go.mod with `replace git.duckfam.us/jonathan/sngl => <root>` + go mod tidy, then:
(cd /tmp/btcv && go build ./...)
```

Expected: compiles, references `snglcanvas`/`tui`. (`examples/canvas` uses rect/ellipse/line/canvasText — all must translate.)

- [ ] **Step 2: Run the canvas example through sngl test on bubbletea**

```bash
go tool sngl test --platform=bubbletea --opt goModExtra="replace git.duckfam.us/jonathan/sngl => $root" examples/canvas/app.sngl
```

Expected: `ok` (builds + runs).

- [ ] **Step 3: Run the affected package suites**

```bash
go test ./pkg/go/canvas/ ./pkg/go/tui/ ./codegen/canvasutil/ ./codegen/platform/fyne/ ./codegen/platform/bubbletea/
```

Expected: all PASS (fyne refactor + new packages + bubbletea).

- [ ] **Step 4: Full verify (capture exit code explicitly — do NOT pipe through `tail`)**

```bash
go tool verify; echo "exit=$?"
```

Expected: `exit=0`.

- [ ] **Step 5: Commit any test-fixture updates**

If the bubbletea canvas now changes a golden (e.g. `cmd/sngl/testdata/dump_lowered_list.txt` gains a bubbletea Canvas pass, or a stdlib-coverage golden), regenerate and commit:

```bash
git add -A
git commit -m "test: update goldens for bubbletea canvas"
```

---

## Notes for the implementer

- **gg API:** verify exact method names against `go doc github.com/fogleman/gg` (v1.3.0) — `NewContext`, `DrawRectangle`, `DrawCircle`, `DrawEllipse`, `DrawLine`, `MoveTo`, `LineTo`, `CubicTo`, `ClosePath`, `Fill`, `FillPreserve`, `Stroke`, `SetColor`, `SetLineWidth`, `DrawString`, `Push`, `Pop`, `Scale`, `DrawImage`, `LoadImage`, `Image`. Adjust if a name differs.
- **The `canvas` import name clash** (fyne's `fyne.io/fyne/v2/canvas` vs SNGL `pkg/go/canvas`) is real — always alias the SNGL runtime as `snglcanvas` in generated Go.
- **Do not** route gtk4 (cairo) or html (JS) through `GoContextStmts` — they target different backends. Only fyne + bubbletea share it.
- **`StructDeclsExcluding`** (in `canvasutil`) already emits the Go `Color`/`CanvasStyle`/`PathCmd` decls + has a drift-guard test; bubbletea reuses it exactly as fyne/gtk4 do.
