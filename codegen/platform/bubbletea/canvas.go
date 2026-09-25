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

// Canvas2D rendering for bubbletea.
//
// bubbletea keeps the declarative visual tree (it holds Declarative), so a
// canvas arrives in the view body as an *ir.NodeInst, whose drawing
// ctx.Canvases.ForNode finds and whose width and height are still props on the
// node. Every statement in that drawing is what a shape override's own `@draw` handler was written as: bubbletea declares no
// shape overrides and inherits `sngl:language/go`'s, which paint through
// `#[go.native]` methods on the pkg/go/canvas runtime. No canvas intrinsic
// reaches here, so nothing in this package translates one.
//
// bubbletea is a RenderModel: View() re-runs on every update, so the canvas is
// rasterised inline in View() each frame — no persistent widget, no reactive
// redraw. The shared pkg/go/canvas runtime (snglcanvas) rasterises the shapes
// into an image.Image; pkg/go/tui (tui) renders that image into a terminal
// string (kitty escapes when supported, else truecolor half-blocks) that is
// woven into the lipgloss View output like any other node's string fragment.
//
// There are no canvas intrinsics left to translate. The shared
// canvasutil.GoContextStmts helper that turned the last two into Context
// method calls is gone with them.

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
	if vc.canvasSeq[c.Name] {
		id, raster := emitLoopCanvasRaster(vc.line, c, vc.gc)
		render := fmt.Sprintf("tui.RenderTerminal(%d, %d, %s, %s)", cols, rows, id, raster)
		style := buildIRStyleExpr(codegen.NodeStyleFields(n), vc.gc, vc.scaleFactor)
		if style != "lipgloss.NewStyle()" {
			render = fmt.Sprintf("%s.Render(%s)", style, render)
		}
		vc.line("%s = %s", resultVar, render)
		return
	}
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

// emitCanvasSurfaceDecls declares one reusable drawing surface per canvas, and
// one per copy of a canvas a loop renders.
//
// tui calls the rasteriser on every frame it cannot serve from cache, and a
// context allocated per call throws the whole image away per keypress -- most
// of a megabyte for a readout across a wide terminal.
func emitCanvasSurfaceDecls(b *strings.Builder, draws *codegen.CanvasDraws, inLoop map[*ir.NodeInst]bool) {
	all := draws.All()
	for i := range all {
		c := &all[i]
		if !inLoop[c.Node] {
			fmt.Fprintf(b, "var %s %s.Surface\n", canvasSurfaceVar(c), snglCanvasAlias)
			continue
		}
		v := canvasSurfaceVar(c)
		fmt.Fprintf(b, "var %s = map[int]*%s.Surface{}\n\n", v, snglCanvasAlias)
		fmt.Fprintf(b, "func %sAt(copy int) *%s.Surface {\n", v, snglCanvasAlias)
		fmt.Fprintf(b, "\ts := %s[copy]\n", v)
		b.WriteString("\tif s == nil {\n")
		fmt.Fprintf(b, "\t\ts = &%s.Surface{}\n", snglCanvasAlias)
		fmt.Fprintf(b, "\t\t%s[copy] = s\n", v)
		b.WriteString("\t}\n\treturn s\n}\n")
	}
	if len(all) > 0 {
		b.WriteByte('\n')
	}
}

// loopCanvases is the canvases the view renders once per iteration of a loop.
// Their drawing reads the loop's variables, so it is written inline where the
// iteration binds them, rather than as a Model method that could not see them.
func loopCanvases(ctx *codegen.CodegenCtx) map[*ir.NodeInst]bool {
	out := map[*ir.NodeInst]bool{}
	var walk func(stmts []ir.Stmt, inLoop bool)
	walk = func(stmts []ir.Stmt, inLoop bool) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.NodeInst:
				if inLoop && ctx.Canvases.ForNode(n) != nil {
					out[n] = true
				}
				walk(n.Children, inLoop)
			case *ir.For:
				walk(n.Body, true)
				walk(n.Else, true)
			case *ir.If:
				walk(n.Body, inLoop)
				walk(n.Else, inLoop)
			case *ir.ErrorBoundary:
				walk(n.Children, inLoop)
			}
		}
	}
	for _, w := range ctx.Windows() {
		walk(w.Body, false)
	}
	for _, comp := range ctx.Pkg.Components {
		walk(comp.Body, false)
	}
	return out
}

func canvasSeqVar(c *codegen.Canvas) string {
	return strings.Replace(c.Name, "_canvasDraw", "__canvasSeq", 1)
}

// emitLoopCanvasRaster writes, where a loop has bound the copy's variables,
// the rasteriser for the next copy of c, and returns the kitty image ID and the
// rasteriser to hand tui. A copy's ID keeps its canvas's own in the low bits,
// so it collides with no other canvas's.
func emitLoopCanvasRaster(line func(string, ...any), c *codegen.Canvas, gc *golang.GoIRContext) (id, raster string) {
	seq := canvasSeqVar(c)
	at := strings.Replace(seq, "Seq", "At", 1)
	raster = strings.Replace(seq, "Seq", "Raster", 1)
	line("%s := %s", at, seq)
	line("%s++", seq)
	line("%s := func() image.Image {", raster)
	line("\tctx := %sAt(%s).Begin(%d, %d, 0, 0, \"\")", canvasSurfaceVar(c), at, c.Width, c.Height)
	drawGC := gc.WithLocal("ctx")
	for _, st := range translateCanvasBody(c.Draw) {
		for _, l := range drawGC.EvalStmt(st) {
			line("\t%s", l)
		}
	}
	line("\treturn ctx.Result()")
	line("}")
	return fmt.Sprintf("%d|(%s+1)<<16", canvasImageID(c), at), raster
}

func (vc *irViewContext) declareCanvasSeqs(f *ir.For) {
	eachRenderedNode([]ir.Stmt{f}, func(n *ir.NodeInst) {
		c := vc.ctx.Canvases.ForNode(n)
		if c == nil || vc.canvasSeq[c.Name] {
			return
		}
		if vc.canvasSeq == nil {
			vc.canvasSeq = map[string]bool{}
		}
		vc.canvasSeq[c.Name] = true
		vc.line("%s := 0", canvasSeqVar(c))
		vc.line("_ = %s", canvasSeqVar(c))
	})
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
//
// A canvas a loop renders is transmitted once per copy, by walking the view
// body the way View does and counting the copies as View counts them.
func emitCanvasTransmitMethod(b *strings.Builder, ctx *codegen.CodegenCtx, inLoop map[*ir.NodeInst]bool, gc *golang.GoIRContext) {
	gc.RequireImport("strings")
	gc.RequireImport("image")
	fmt.Fprintf(b, "func (m Model) %s() tea.Cmd {\n", canvasTransmitMethodName)
	b.WriteString("\tvar __ctb strings.Builder\n")
	all := ctx.Canvases.All()
	for i := range all {
		c := &all[i]
		if inLoop[c.Node] {
			continue
		}
		cols, rows := terminalCells(c.Width, c.Height)
		// KittyTransmit calls the rasteriser only when it actually needs to
		// re-encode (kitty + pixels changed), so a static or off-screen canvas
		// costs nothing here.
		fmt.Fprintf(b, "\t__ctb.WriteString(tui.KittyTransmit(%d, %d, %d, %s))\n", cols, rows, canvasImageID(c), canvasRasteriser(c))
	}
	if wins := ctx.Windows(); len(wins) > 0 {
		w := &viewWalk{b: b, indent: 1, wants: func(n *ir.NodeInst) bool { return inLoop[n] }}
		w.visit = func(w *viewWalk, n *ir.NodeInst, gc *golang.GoIRContext) {
			c := ctx.Canvases.ForNode(n)
			id, raster := emitLoopCanvasRaster(w.line, c, gc)
			cols, rows := terminalCells(c.Width, c.Height)
			w.line("__ctb.WriteString(tui.KittyTransmit(%d, %d, %s, %s))", cols, rows, id, raster)
		}
		declared := map[string]bool{}
		eachRenderedNode(wins[0].Body, func(n *ir.NodeInst) {
			if c := ctx.Canvases.ForNode(n); c != nil && inLoop[n] && !declared[c.Name] {
				declared[c.Name] = true
				w.line("%s := 0", canvasSeqVar(c))
			}
		})
		w.stmts(wins[0].Body, gc)
	}
	b.WriteString("\tif __ctb.Len() == 0 {\n\t\treturn nil\n\t}\n")
	b.WriteString("\treturn tea.Raw(__ctb.String())\n")
	b.WriteString("}\n\n")
}

// emitCanvasDrawFuncs emits one `func (m *Model) _canvasDrawN(ctx
// *snglcanvas.Context)` per canvas NodeInst found in the visual tree. The body
// is the drawing as passShapeDraw left it -- `#[go.native]` method calls on the
// canvas runtime, which the Go IR context renders like any other call. A loop
// canvas's drawing is written inline instead (emitLoopCanvasRaster).
func emitCanvasDrawFuncs(b *strings.Builder, draws *codegen.CanvasDraws, inLoop map[*ir.NodeInst]bool, gc *golang.GoIRContext) {
	all := draws.All()
	for i := range all {
		if !inLoop[all[i].Node] {
			emitCanvasDrawFunc(b, &all[i], gc)
		}
	}
}

// translateCanvasBody rebuilds a draw body, descending into the conditionals
// and loops the splice keeps in one -- reading only the top level left a shape
// written inside an `if` as a call to a Go function nobody emits, which
// panicked the build.
//
// It translates nothing any more. Every statement in a draw body is what a
// shape override's `@draw` handler was written as, which for this target is
// `#[go.native]` method calls on the pkg/go/canvas runtime that the ordinary
// Go emitter already handles. The walk stays because the descent is still
// needed: a nested body has to be rebuilt for the copy this returns.
func translateCanvasBody(stmts []ir.Stmt) []ir.Stmt {
	var out []ir.Stmt
	for _, stmt := range stmts {
		switch n := stmt.(type) {
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
