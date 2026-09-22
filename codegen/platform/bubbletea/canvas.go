package bubbletea

import (
	"fmt"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/canvasutil"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// The 2D primitives are translated in this package rather than through an
// IntrinsicEmitter, which renders one expression and could not carry the
// statements and pending style these need. Declaring the package is how that
// implementation becomes visible to the completeness check.
func init() { codegen.DeclarePlatformImplements("bubbletea", "sngl:internal/draw") }

// Canvas2D rendering for bubbletea.
//
// bubbletea keeps the declarative visual tree (it holds Declarative), so a
// canvas arrives in the view body as an *ir.NodeInst with n.CanvasDraw set
// (the synthesized `_canvasDrawN(ctx)` func passShapeDraw produced) and
// width/height props still on the node. That func's body is the bracket
// intrinsics (CanvasSave / CanvasRestore) around each composed shape, plus
// whatever each shape override's own `@draw` handler was written as.
// bubbletea declares no shape overrides and inherits `sngl:language/go`'s,
// which paint through `#[go.native]` methods on the pkg/go/canvas runtime.
//
// bubbletea is a RenderModel: View() re-runs on every update, so the canvas is
// rasterised inline in View() each frame — no persistent widget, no reactive
// redraw. The shared pkg/go/canvas runtime (snglcanvas) rasterises the shapes
// into an image.Image; pkg/go/tui (tui) renders that image into a terminal
// string (kitty escapes when supported, else truecolor half-blocks) that is
// woven into the lipgloss View output like any other node's string fragment.
//
// The canvas intrinsics are translated via the shared canvasutil.GoContextStmts
// helper (also used by fyne) into Context method calls — never reimplemented.

const (
	snglCanvasImportPath = "git.duckfam.us/jonathan/sngl/pkg/go/canvas"
	snglCanvasAlias      = "snglcanvas"
	tuiImportPath        = "git.duckfam.us/jonathan/sngl/pkg/go/tui"
)

// canvasCtxType is the IR native type for the draw-func ctx parameter:
// *snglcanvas.Context.
func canvasCtxType() *ir.Type { return ir.NativeGoPointerOf(snglCanvasAlias + ".Context") }

// canvasDims returns the canvas pixel dimensions, defaulting to the HTML canvas
// 300x150 when unset.
func canvasDims(w, h int) (int, int) {
	if w <= 0 {
		w = 300
	}
	if h <= 0 {
		h = 150
	}
	return w, h
}

// terminalCells maps canvas pixel dimensions to a terminal cell grid. The
// half-block renderer packs two pixel rows per character cell, so rows is half
// the pixel height (rounded up). Both are clamped to a sane terminal size.
func terminalCells(w, h int) (cols, rows int) {
	cols = w
	rows = (h + 1) / 2
	if cols > 200 {
		cols = 200
	}
	if rows > 100 {
		rows = 100
	}
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	return cols, rows
}

// nodeCanvasDims reads the integer pixel width/height props off a canvas
// NodeInst, falling back to canvasDims defaults.
func nodeCanvasDims(n *ir.NodeInst) (int, int) {
	return canvasDims(intProp(n, "width"), intProp(n, "height"))
}

// intProp reads a NodeInst prop as an int pixel value, stripping any unit
// suffix (e.g. "40px"). Mirrors lower.nodeIntProp so the un-flattened canvas
// NodeInst yields the same dimensions the LocalVar path would. Returns 0 when
// absent or non-literal.
func intProp(n *ir.NodeInst, name string) int {
	for _, p := range n.Props {
		if p.Name != name {
			continue
		}
		lit, ok := p.Value.(*ir.Literal)
		if !ok {
			return 0
		}
		raw := lit.Value
		if v, err := strconv.Atoi(raw); err == nil {
			return v
		}
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			return int(f)
		}
		return 0
	}
	return 0
}

// canvasStdlibDecls returns the Go decls for the canvas stdlib structs
// (Color/CanvasStyle/PathCmd), omitting any whose name a user struct already
// declares (those emit their own type decl).
func canvasStdlibDecls(structs []*ir.StructDef) string {
	declared := map[string]struct{}{}
	for _, s := range structs {
		switch s.Name {
		case "Color", "CanvasStyle", "PathCmd":
			declared[s.Name] = struct{}{}
		}
	}
	return canvasutil.StructDeclsExcluding(declared)
}

// renderCanvas weaves a canvas node into the View string output: allocate a
// snglcanvas.Context sized to the canvas, run the draw func to rasterise the
// shapes, then render the resulting image into a terminal string fragment via
// pkg/go/tui. The string is assigned to resultVar like any other node's
// rendered output, so it joins into the surrounding lipgloss layout normally.
func (vc *irViewContext) renderCanvas(n *ir.NodeInst, c *codegen.Canvas, resultVar string) {
	cols, rows := terminalCells(c.Width, c.Height)
	vc.requireImport(snglCanvasImportPath)
	vc.requireImport(tuiImportPath)
	vc.requireImport("image")
	// The emitted CanvasStyle struct decl references snglcolor.Color.
	vc.requireImport(canvasutil.ColorImportPath)
	// Rasterisation is deferred into a closure so the kitty path (which needs
	// only the constant, cached placeholder grid) never runs it every frame;
	// only the half-block fallback invokes it. The terminal string is assigned
	// like any other leaf node's output. Only wrap it in the node's lipgloss
	// style when one is actually set — an empty NewStyle().Render() pads the
	// multi-line half-block grid with background cells, mangling the art.
	render := fmt.Sprintf("tui.RenderTerminal(%d, %d, %d, %s)", cols, rows, canvasImageID(c), canvasRasteriser(c))
	style := buildIRStyleExpr(codegen.NodeStyleFields(n), vc.gc, vc.scaleFactor)
	if style != "lipgloss.NewStyle()" {
		vc.line("%s = %s.Render(%s)", resultVar, style, render)
	} else {
		vc.line("%s = %s", resultVar, render)
	}
}

// canvasRasteriser returns a Go `func() image.Image` literal that draws the
// node's shapes into its canvas surface and returns the rasterised image.
// Shared by the View placeholder path and the out-of-band transmit method; both
// pass it to tui, which calls it only when pixels are actually required.
//
// A terminal canvas never rescales -- the cell grid is computed from the
// declared size -- so the surface is asked for the size it already has and the
// buffer survives every frame after the first.
func canvasRasteriser(c *codegen.Canvas) string {
	return fmt.Sprintf("func() image.Image { __c := %s.Begin(%d, %d, 0, 0, \"\"); m.%s(__c); return __c.Result() }",
		canvasSurfaceVar(c), c.Width, c.Height, c.Name)
}

// canvasSurfaceVar names the package-level surface backing a draw func. It is a
// package var rather than a Model field because bubbletea's Model is a value:
// an Update returns a copy, so a buffer parked in a field would be reallocated
// on the frame after every keypress -- the allocation this exists to remove.
func canvasSurfaceVar(c *codegen.Canvas) string {
	return strings.Replace(c.Name, "canvasDraw", "canvasSurface", 1)
}

// emitCanvasSurfaceDecls declares one reusable drawing surface per canvas.
//
// tui calls the rasteriser on every frame it cannot serve from cache, and a
// context allocated per call throws the whole image away per keypress -- most
// of a megabyte for a readout across a wide terminal.
func emitCanvasSurfaceDecls(b *strings.Builder, draws *codegen.CanvasDraws) {
	all := draws.All()
	for i := range all {
		fmt.Fprintf(b, "var %s %s.Surface\n", canvasSurfaceVar(&all[i]), snglCanvasAlias)
	}
	if len(all) > 0 {
		b.WriteByte('\n')
	}
}

// canvasImageID derives a stable, nonzero kitty image ID from a canvas draw
// func. codegen names them `_canvasDraw0`, `_canvasDraw1`, … — globally
// unique per canvas in the package — so the trailing index + 1 gives each
// on-screen canvas a distinct image ID (kitty IDs must be > 0, and two images
// sharing an ID would clobber each other's transmitted data).
func canvasImageID(c *codegen.Canvas) int {
	if c == nil {
		return 1
	}
	i := len(c.Name)
	for i > 0 && c.Name[i-1] >= '0' && c.Name[i-1] <= '9' {
		i--
	}
	if n, err := strconv.Atoi(c.Name[i:]); err == nil {
		return n + 1
	}
	return 1
}

// canvasTransmitMethodName is the Model method that transmits every canvas's
// pixels out of band; View() emits only placeholder cells (which a cell-diffing
// renderer preserves), so the image data must reach the tty via tea.Raw.
const canvasTransmitMethodName = "__canvasTransmit"

// emitCanvasTransmitMethod emits `func (m Model) __canvasTransmit() tea.Cmd`,
// which rasterises every canvas in the package, builds each one's kitty
// transmit escape (via tui.KittyTransmit — empty on non-kitty terminals), and
// returns them as a single tea.Raw command. Init/Update return this so the
// image data is written raw to the tty, out of the cell compositor that would
// otherwise drop the APC graphics sequence. Returns nil when nothing was
// transmitted (e.g. half-block terminals), so the half-block path is unaffected.
func emitCanvasTransmitMethod(b *strings.Builder, draws *codegen.CanvasDraws, gc *golang.GoIRContext) {
	gc.RequireImport("strings")
	gc.RequireImport("image")
	fmt.Fprintf(b, "func (m Model) %s() tea.Cmd {\n", canvasTransmitMethodName)
	b.WriteString("\tvar __ctb strings.Builder\n")
	all := draws.All()
	for i := range all {
		c := &all[i]
		cols, rows := terminalCells(c.Width, c.Height)
		// KittyTransmit calls the rasteriser only when it actually needs to
		// re-encode (kitty + pixels changed), so a static or off-screen canvas
		// costs nothing here.
		fmt.Fprintf(b, "\t__ctb.WriteString(tui.KittyTransmit(%d, %d, %d, %s))\n", cols, rows, canvasImageID(c), canvasRasteriser(c))
	}
	b.WriteString("\tif __ctb.Len() == 0 {\n\t\treturn nil\n\t}\n")
	b.WriteString("\treturn tea.Raw(__ctb.String())\n")
	b.WriteString("}\n\n")
}

// emitCanvasDrawFuncs emits one `func (m *Model) _canvasDrawN(ctx
// *snglcanvas.Context)` per canvas NodeInst found in the visual tree. The body
// is the draw func's canvas-intrinsic CallStmts, each translated to ctx method
// calls via the shared canvasutil.GoContextStmts helper and rendered through
// the Go IR context.
func emitCanvasDrawFuncs(b *strings.Builder, draws *codegen.CanvasDraws, gc *golang.GoIRContext) {
	all := draws.All()
	for i := range all {
		emitCanvasDrawFunc(b, &all[i], gc)
	}
}

// translateCanvasBody rewrites the canvas intrinsics in stmts into Context
// method calls, descending into the conditionals and loops the splice keeps in
// a draw body -- reading only the top level left a shape written inside an
// `if` as a call to a Go function nobody emits, which panicked the build.
//
// Matched on the "Canvas" prefix rather than on carrying any intrinsic at all:
// GoContextStmts answers for this package's ids and returns nil for anything
// else, so handing it another intrinsic deleted the statement.
func translateCanvasBody(stmts []ir.Stmt) []ir.Stmt {
	var out []ir.Stmt
	for _, stmt := range stmts {
		switch n := stmt.(type) {
		case *ir.CallStmt:
			if n.Call != nil && n.Call.Func != nil && strings.HasPrefix(n.Call.Func.Intrinsic, "Canvas") {
				out = append(out, canvasutil.GoContextStmts(n)...)
				continue
			}
		case *ir.If:
			out = append(out, &ir.If{
				AST:  n.AST,
				Cond: n.Cond,
				Body: translateCanvasBody(n.Body),
				Else: translateCanvasBody(n.Else),
			})
			continue
		case *ir.For:
			cp := *n
			cp.Body = translateCanvasBody(n.Body)
			cp.Else = translateCanvasBody(n.Else)
			out = append(out, &cp)
			continue
		}
		out = append(out, stmt)
	}
	return out
}

// emitCanvasDrawFunc emits a single draw func as a Model method, translating
// each canvas-intrinsic body CallStmt into Context method calls.
func emitCanvasDrawFunc(b *strings.Builder, cv *codegen.Canvas, gc *golang.GoIRContext) {
	body := translateCanvasBody(cv.Draw)
	synthesized := &ir.Func{
		Name:     cv.Name,
		Receiver: "Model",
		Params:   []*ir.Param{{Name: "ctx", Type: canvasCtxType()}},
		Return:   ir.TypVoid,
		Block:    body,
	}
	for _, line := range gc.EmitFuncDef(synthesized) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}
