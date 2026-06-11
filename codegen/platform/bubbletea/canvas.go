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
// bubbletea keeps the declarative visual tree (it does NOT set NoDeclarative),
// so a canvas arrives in the view body as an *ir.NodeInst with n.CanvasDraw
// set (the synthesized `_canvasDrawN(ctx)` func produced by passCanvas) and
// width/height props still on the node. passCanvas's draw func body is a
// sequence of canvas-intrinsic CallStmts (CanvasApplyStyle / CanvasDrawRect /
// ...).
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
		raw := strings.TrimSuffix(lit.Raw, lit.Suffix)
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

// canvasDrawFuncSet returns the set of canvas draw funcs referenced by canvas
// NodeInsts in the visual tree, so the generic user-func loop can skip them.
func canvasDrawFuncSet(pkg *ir.Package) map[*ir.Func]bool {
	set := map[*ir.Func]bool{}
	collect := func(body []ir.Stmt) {
		codegen.WalkVisualTree(body, func(n *ir.NodeInst, _ int) bool {
			if n.CanvasDraw != nil {
				set[n.CanvasDraw] = true
			}
			return false
		})
	}
	for _, c := range pkg.Components {
		collect(c.Body)
	}
	for _, w := range pkg.Windows {
		collect(w.Body)
	}
	return set
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

// hasCanvasNodes reports whether any canvas NodeInst (CanvasDraw != nil) appears
// in the package's component/window bodies.
func hasCanvasNodes(pkg *ir.Package) bool {
	found := false
	walk := func(body []ir.Stmt) {
		codegen.WalkVisualTree(body, func(n *ir.NodeInst, _ int) bool {
			if n.CanvasDraw != nil {
				found = true
			}
			return false
		})
	}
	for _, c := range pkg.Components {
		walk(c.Body)
	}
	for _, w := range pkg.Windows {
		walk(w.Body)
	}
	return found
}

// renderCanvas weaves a canvas node into the View string output: allocate a
// snglcanvas.Context sized to the canvas, run the draw func to rasterise the
// shapes, then render the resulting image into a terminal string fragment via
// pkg/go/tui. The string is assigned to resultVar like any other node's
// rendered output, so it joins into the surrounding lipgloss layout normally.
func (vc *irViewContext) renderCanvas(n *ir.NodeInst, resultVar string) {
	w, h := nodeCanvasDims(n)
	cols, rows := terminalCells(w, h)
	vc.requireImport(snglCanvasImportPath)
	vc.requireImport(tuiImportPath)
	vc.requireImport("image")
	// Rasterisation is deferred into a closure so the kitty path (which needs
	// only the constant, cached placeholder grid) never runs it every frame;
	// only the half-block fallback invokes it. The terminal string is assigned
	// like any other leaf node's output. Only wrap it in the node's lipgloss
	// style when one is actually set — an empty NewStyle().Render() pads the
	// multi-line half-block grid with background cells, mangling the art.
	render := fmt.Sprintf("tui.RenderTerminal(%d, %d, %d, %s)", cols, rows, canvasImageID(n.CanvasDraw), canvasRasteriser(n, w, h))
	style := buildIRStyleExpr(codegen.NodeStyleFields(n), vc.gc, vc.scaleFactor)
	if style != "lipgloss.NewStyle()" {
		vc.line("%s = %s.Render(%s)", resultVar, style, render)
	} else {
		vc.line("%s = %s", resultVar, render)
	}
}

// canvasRasteriser returns a Go `func() image.Image` literal that allocates a
// canvas Context, runs the node's draw func, and returns the rasterised image.
// Shared by the View placeholder path and the out-of-band transmit method; both
// pass it to tui, which calls it only when pixels are actually required.
func canvasRasteriser(n *ir.NodeInst, w, h int) string {
	return fmt.Sprintf("func() image.Image { __c := %s.New(%d, %d); m.%s(__c); return __c.Result() }",
		snglCanvasAlias, w, h, n.CanvasDraw.Name)
}

// canvasImageID derives a stable, nonzero kitty image ID from a canvas draw
// func. passCanvas names them `_canvasDraw0`, `_canvasDraw1`, … — globally
// unique per canvas in the package — so the trailing index + 1 gives each
// on-screen canvas a distinct image ID (kitty IDs must be > 0, and two images
// sharing an ID would clobber each other's transmitted data).
func canvasImageID(fn *ir.Func) int {
	if fn == nil {
		return 1
	}
	i := len(fn.Name)
	for i > 0 && fn.Name[i-1] >= '0' && fn.Name[i-1] <= '9' {
		i--
	}
	if n, err := strconv.Atoi(fn.Name[i:]); err == nil {
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
func emitCanvasTransmitMethod(b *strings.Builder, pkg *ir.Package, gc *golang.GoIRContext) {
	gc.RequireImport("strings")
	gc.RequireImport("image")
	fmt.Fprintf(b, "func (m Model) %s() tea.Cmd {\n", canvasTransmitMethodName)
	b.WriteString("\tvar __ctb strings.Builder\n")
	seen := map[*ir.Func]bool{}
	emit := func(body []ir.Stmt) {
		codegen.WalkVisualTree(body, func(n *ir.NodeInst, _ int) bool {
			if n.CanvasDraw == nil || seen[n.CanvasDraw] {
				return false
			}
			seen[n.CanvasDraw] = true
			w, h := nodeCanvasDims(n)
			cols, rows := terminalCells(w, h)
			id := canvasImageID(n.CanvasDraw)
			// KittyTransmit calls the rasteriser only when it actually needs to
			// re-encode (kitty + pixels changed), so a static or off-screen canvas
			// costs nothing here.
			fmt.Fprintf(b, "\t__ctb.WriteString(tui.KittyTransmit(%d, %d, %d, %s))\n", cols, rows, id, canvasRasteriser(n, w, h))
			return false
		})
	}
	for _, c := range pkg.Components {
		emit(c.Body)
	}
	for _, w := range pkg.Windows {
		emit(w.Body)
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
func emitCanvasDrawFuncs(b *strings.Builder, pkg *ir.Package, gc *golang.GoIRContext) {
	seen := map[*ir.Func]bool{}
	emit := func(body []ir.Stmt) {
		codegen.WalkVisualTree(body, func(n *ir.NodeInst, _ int) bool {
			if n.CanvasDraw == nil || seen[n.CanvasDraw] {
				return false
			}
			seen[n.CanvasDraw] = true
			emitCanvasDrawFunc(b, n.CanvasDraw, gc)
			return false
		})
	}
	for _, c := range pkg.Components {
		emit(c.Body)
	}
	for _, w := range pkg.Windows {
		emit(w.Body)
	}
}

// emitCanvasDrawFunc emits a single draw func as a Model method, translating
// each canvas-intrinsic body CallStmt into Context method calls.
func emitCanvasDrawFunc(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext) {
	st := &canvasutil.GoCanvasState{}
	var body []ir.Stmt
	for _, stmt := range fn.Block {
		if cs, ok := stmt.(*ir.CallStmt); ok && cs.Call != nil && cs.Call.Func != nil && cs.Call.Func.Intrinsic != "" {
			body = append(body, canvasutil.GoContextStmts(cs, st)...)
			continue
		}
		body = append(body, stmt)
	}
	synthesized := &ir.Func{
		Name:     fn.Name,
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
