package fyne

import (
	"context"
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Canvas2D rendering for fyne.
//
// passCanvas (internal/lower) extracts a `canvas`+shapes subtree into a
// synthesized `_canvasDrawN(ctx)` func whose body is a sequence of canvas
// intrinsic CallStmts (CanvasSave / CanvasApplyStyle / CanvasDrawRect / ...).
// passDeclarative then flattens the canvas NodeInst to a
// `lower.CreateNode("canvas")` LocalVar, threading the draw func + pixel
// dimensions onto LocalVar.CanvasDraw / CanvasWidth / CanvasHeight.
//
// fyne renders via github.com/fogleman/gg software raster: ctx is a
// *gg.Context, the canvas widget is a *canvas.Image built from the
// rasterised image. Reactive redraws re-rasterise into the same image and
// call Refresh().
//
// The canvas intrinsics are NOT registered in the lang-keyed intrinsic
// registry (that one is shared and JS-specific). They are translated here,
// inside the fyne translator — the same way gtk4 rewrites widget calls —
// because each native 2D API differs.

const ggImportPath = "github.com/fogleman/gg"

// canvasStdlibDecls emits the Go decls for the canvas stdlib structs
// (lib/canvas.sngl + lib/types.sngl color) and the SNGL-color→color.Color
// helper. These stdlib structs are referenced by the synthesized draw funcs
// but are not carried in pkg.Structs (html, the only other canvas consumer,
// emits JS object literals and needs no type decls), so the fyne Go path
// declares them here when any canvas is present. Field names/types mirror the
// stdlib definitions.
const canvasStdlibDecls = "type Color struct {\n" +
	"\tR int\n\tG int\n\tB int\n\tA int\n" +
	"}\n\n" +
	"type CanvasStyle struct {\n" +
	"\tFill        Color\n" +
	"\tStroke      Color\n" +
	"\tStrokeWidth float64\n" +
	"\tLineCap     string\n" +
	"\tLineJoin    string\n" +
	"\tFontSize    float64\n" +
	"\tFontFamily  string\n" +
	"}\n\n" +
	"type PathCmd struct {\n" +
	"\tOp  string\n" +
	"\tX   float64\n\tY   float64\n" +
	"\tCx1 float64\n\tCy1 float64\n\tCx2 float64\n\tCy2 float64\n\tR float64\n" +
	"}\n\n" +
	"func _snglColor(c Color) color.Color {\n" +
	"\treturn color.RGBA{R: uint8(c.R), G: uint8(c.G), B: uint8(c.B), A: uint8(c.A)}\n" +
	"}\n"

// canvasStdlibDeclsExcluding returns canvasStdlibDecls, but only when none of
// the canvas stdlib struct names collide with a user-declared struct already
// in td.Structs. The canvas structs are stdlib-only today; if a program ever
// declares one itself, fall back to emitting just the _snglColor helper to
// avoid a duplicate type decl.
func canvasStdlibDeclsExcluding(structs []structData) string {
	for _, s := range structs {
		switch s.Name {
		case "Color", "CanvasStyle", "PathCmd":
			// Collision: emit only the helper (the user's structs stand in).
			return "func _snglColor(c Color) color.Color {\n" +
				"\treturn color.RGBA{R: uint8(c.R), G: uint8(c.G), B: uint8(c.B), A: uint8(c.A)}\n" +
				"}\n"
		}
	}
	return canvasStdlibDecls
}

// canvasMeta records a flattened canvas element discovered via a
// `LocalVar id = lower.CreateNode("canvas")` carrying a draw func.
type canvasMeta struct {
	id     string // synthesized node id (e.g. "__n0") → Model *canvas.Image field
	draw   *ir.Func
	width  int
	height int
}

// collectCanvases walks every Func block + component/window body for
// `LocalVar.CanvasDraw != nil` entries (canvas CreateNode locals threaded
// through declarative flattening) and returns them keyed by node id and by
// draw func.
func collectCanvases(pkg *ir.Package, funcs []*ir.Func) (map[string]*canvasMeta, map[*ir.Func]*canvasMeta) {
	byID := map[string]*canvasMeta{}
	byFunc := map[*ir.Func]*canvasMeta{}
	var walk func([]ir.Stmt)
	walk = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.LocalVar:
				if n.CanvasDraw != nil {
					m := &canvasMeta{id: n.Name, draw: n.CanvasDraw, width: n.CanvasWidth, height: n.CanvasHeight}
					byID[n.Name] = m
					byFunc[n.CanvasDraw] = m
				}
			case *ir.If:
				walk(n.Body)
				walk(n.Else)
			case *ir.For:
				walk(n.Body)
				walk(n.Else)
			case *ir.PlatformFilter:
				walk(n.Body)
			case *ir.NodeInst:
				walk(n.Children)
			case *ir.Window:
				walk(n.Body)
			}
		}
	}
	for _, fn := range funcs {
		if fn != nil {
			walk(fn.Block)
		}
	}
	if pkg != nil {
		for _, comp := range pkg.Components {
			walk(comp.Body)
		}
		for _, w := range pkg.Windows {
			walk(w.Body)
		}
	}
	return byID, byFunc
}

// canvasByIDFor rebuilds the id→meta map from the func→meta map (both share
// the same canvasMeta pointers).
func canvasByIDFor(byFunc map[*ir.Func]*canvasMeta) map[string]*canvasMeta {
	out := make(map[string]*canvasMeta, len(byFunc))
	for _, m := range byFunc {
		out[m.id] = m
	}
	return out
}

// translateCanvasIntrinsic rewrites one canvas-intrinsic CallStmt (inside a
// draw func body) into native gg calls. passCanvas emits a fixed per-shape
// structure: Save, [ApplyStyle], DrawPrimitive, Restore. ApplyStyle binds a
// `_styleN` local that the following primitive's fill/stroke reference.
func (t *fyneTranslator) translateCanvasIntrinsic(cs *ir.CallStmt) []ir.Stmt {
	call := cs.Call
	id := call.Func.Intrinsic
	ctxArg := call.Args[0].Value
	rest := call.Args[1:]
	arg := func(i int) ir.Expr { return rest[i].Value }

	switch id {
	case "CanvasSave":
		return []ir.Stmt{methodStmt(ctxArg, "Push")}
	case "CanvasRestore":
		return []ir.Stmt{methodStmt(ctxArg, "Pop")}
	case "CanvasApplyStyle":
		// Bind the style to a fresh local so the following draw's fill/stroke
		// don't re-evaluate the (possibly large) style expression repeatedly.
		t.styleCounter++
		name := fmt.Sprintf("_style%d", t.styleCounter)
		t.pendingStyle = &ir.Ident{Name: name, Type: ir.TypDyn}
		return []ir.Stmt{&ir.LocalVar{Name: name, Init: arg(0)}}
	case "CanvasDrawRect":
		return t.paintAround(ctxArg, methodStmt(ctxArg, "DrawRectangle", arg(0), arg(1), arg(2), arg(3)))
	case "CanvasDrawCircle":
		return t.paintAround(ctxArg, methodStmt(ctxArg, "DrawCircle", arg(0), arg(1), arg(2)))
	case "CanvasDrawEllipse":
		return t.paintAround(ctxArg, methodStmt(ctxArg, "DrawEllipse", arg(0), arg(1), arg(2), arg(3)))
	case "CanvasDrawLine":
		return t.strokeAround(ctxArg, methodStmt(ctxArg, "DrawLine", arg(0), arg(1), arg(2), arg(3)))
	case "CanvasDrawText":
		style := t.takeStyle()
		var stmts []ir.Stmt
		if style != nil {
			stmts = append(stmts, fillColorStmt(ctxArg, style))
		}
		// gg DrawString(s, x, y); content=arg(2), x=arg(0), y=arg(1).
		stmts = append(stmts, methodStmt(ctxArg, "DrawString", arg(2), arg(0), arg(1)))
		return stmts
	case "CanvasDrawPath":
		return t.translatePath(ctxArg, arg(0))
	case "CanvasDrawImage":
		// Drawing an external image src needs async decode; unsupported in
		// the gg raster path. Emit nothing rather than fail the build.
		t.takeStyle()
		return nil
	}
	return []ir.Stmt{cs}
}

// takeStyle returns the pending style (set by the preceding CanvasApplyStyle)
// and clears it.
func (t *fyneTranslator) takeStyle() ir.Expr {
	s := t.pendingStyle
	t.pendingStyle = nil
	return s
}

// paintAround wraps a path-defining draw call with fill + stroke using the
// pending style. FillPreserve keeps the path so the following Stroke applies
// to the same geometry.
func (t *fyneTranslator) paintAround(ctxArg ir.Expr, draw ir.Stmt) []ir.Stmt {
	style := t.takeStyle()
	stmts := []ir.Stmt{draw}
	if style == nil {
		return append(stmts, methodStmt(ctxArg, "Fill"))
	}
	return append(stmts,
		fillColorStmt(ctxArg, style),
		methodStmt(ctxArg, "FillPreserve"),
		methodStmt(ctxArg, "SetLineWidth", styleField(style, "strokeWidth")),
		strokeColorStmt(ctxArg, style),
		methodStmt(ctxArg, "Stroke"),
	)
}

// strokeAround emits a stroke-only paint for line primitives.
func (t *fyneTranslator) strokeAround(ctxArg ir.Expr, draw ir.Stmt) []ir.Stmt {
	style := t.takeStyle()
	stmts := []ir.Stmt{draw}
	if style != nil {
		stmts = append(stmts,
			methodStmt(ctxArg, "SetLineWidth", styleField(style, "strokeWidth")),
			strokeColorStmt(ctxArg, style),
		)
	}
	return append(stmts, methodStmt(ctxArg, "Stroke"))
}

// translatePath rewrites CanvasDrawPath(ctx, cmds) into a range loop over the
// PathCmd list emitting gg MoveTo/LineTo/CubicTo/ClosePath, then fill+stroke.
func (t *fyneTranslator) translatePath(ctxArg, cmds ir.Expr) []ir.Stmt {
	style := t.takeStyle()
	loopVar := &ir.Ident{Name: "_cmd", Type: ir.TypDyn}
	opSel := &ir.Select{Operand: loopVar, Field: "op", Type: ir.TypString}
	field := func(name string) ir.Expr { return &ir.Select{Operand: loopVar, Field: name, Type: ir.TypFloat} }
	cmdIf := func(op string, then ir.Stmt) *ir.If {
		return &ir.If{
			Cond: &ir.Binary{Op: ast.BinEq, Left: opSel, Right: &ir.Literal{Type: ir.TypString, Raw: op}},
			Body: []ir.Stmt{then},
		}
	}
	body := []ir.Stmt{
		cmdIf("moveTo", methodStmt(ctxArg, "MoveTo", field("x"), field("y"))),
		cmdIf("lineTo", methodStmt(ctxArg, "LineTo", field("x"), field("y"))),
		cmdIf("bezierTo", methodStmt(ctxArg, "CubicTo", field("cx1"), field("cy1"), field("cx2"), field("cy2"), field("x"), field("y"))),
		cmdIf("close", methodStmt(ctxArg, "ClosePath")),
	}
	loop := &ir.For{Key: "_cmd", Iter: cmds, Body: body}
	stmts := []ir.Stmt{loop}
	if style != nil {
		return append(stmts,
			fillColorStmt(ctxArg, style),
			methodStmt(ctxArg, "FillPreserve"),
			methodStmt(ctxArg, "SetLineWidth", styleField(style, "strokeWidth")),
			strokeColorStmt(ctxArg, style),
			methodStmt(ctxArg, "Stroke"),
		)
	}
	return append(stmts, methodStmt(ctxArg, "Fill"))
}

// methodStmt builds `receiver.Method(args...)` as a CallStmt.
func methodStmt(receiver ir.Expr, method string, args ...ir.Expr) ir.Stmt {
	return &ir.CallStmt{Call: methodCall(receiver, method, args, ir.TypVoid)}
}

// styleField builds `<style>.<field>` as an ir.Select with float type.
func styleField(style ir.Expr, field string) ir.Expr {
	return &ir.Select{Operand: style, Field: field, Type: ir.TypFloat}
}

// fillColorStmt emits `ctx.SetColor(_snglColor(style.fill))`.
func fillColorStmt(ctxArg, style ir.Expr) ir.Stmt {
	return methodStmt(ctxArg, "SetColor", snglColorCall(&ir.Select{Operand: style, Field: "fill", Type: ir.TypDyn}))
}

// strokeColorStmt emits `ctx.SetColor(_snglColor(style.stroke))`.
func strokeColorStmt(ctxArg, style ir.Expr) ir.Stmt {
	return methodStmt(ctxArg, "SetColor", snglColorCall(&ir.Select{Operand: style, Field: "stroke", Type: ir.TypDyn}))
}

// snglColorCall builds `_snglColor(<colorExpr>)`.
func snglColorCall(colorExpr ir.Expr) ir.Expr {
	return &ir.Call{
		Type: ir.TypDyn,
		Func: &ir.Func{Name: "_snglColor"},
		Args: []ir.CallArg{{Value: colorExpr}},
	}
}

// translateCanvasRedraw rewrites a CanvasRedrawStmt into the fyne re-raster
// sequence for the matching canvas widget field:
//
//	m.<id>Ctx.Clear()
//	m._canvasDrawN(m.<id>Ctx)
//	m.<id>.Image = m.<id>Ctx.Image()
//	m.<id>.Refresh()
func (t *fyneTranslator) translateCanvasRedraw(rs *ir.CanvasRedrawStmt) []ir.Stmt {
	m := t.canvasMetaForDraw(rs.DrawFunc)
	if m == nil {
		return nil
	}
	dcField := modelFieldRef(canvasCtxField(m.id))
	imgField := modelFieldRef(m.id)
	drawCall := &ir.Call{
		Type:     ir.TypVoid,
		Receiver: &ir.Ident{Name: "m"},
		Func:     &ir.Func{Name: m.draw.Name},
		Args:     []ir.CallArg{{Value: dcField}},
	}
	return []ir.Stmt{
		methodStmt(dcField, "Clear"),
		&ir.CallStmt{Call: drawCall},
		&ir.Assign{
			Target: &ir.Select{Operand: imgField, Field: "Image", Type: ir.TypDyn},
			Op:     ast.AssignSet,
			Value:  methodCall(dcField, "Image", nil, ir.TypDyn),
		},
		methodStmt(imgField, "Refresh"),
	}
}

// canvasMetaForDraw resolves the canvasMeta for a draw func via the
// translator's shared map.
func (t *fyneTranslator) canvasMetaForDraw(draw *ir.Func) *canvasMeta {
	if t.canvasByFunc == nil {
		return nil
	}
	return t.canvasByFunc[draw]
}

// canvasCtxField names the *gg.Context Model field backing a canvas widget.
func canvasCtxField(id string) string { return id + "Ctx" }

// emitCanvasCreate emits the OnCreateNode result for a `canvas` tag: register
// the *canvas.Image and *gg.Context Model fields, build the context sized to
// the canvas, rasterise once, and create the image widget.
func (t *fyneTranslator) emitCanvasCreate(id string) []ir.Stmt {
	m := t.canvasByID[id]
	if m == nil {
		return nil
	}
	t.fieldSink(id, "*canvas.Image")
	t.fieldSink(canvasCtxField(id), "*gg.Context")
	t.idTags[id] = "canvas"
	t.topLevel = append(t.topLevel, id)
	if t.importSink != nil {
		t.importSink(ggImportPath)
		t.importSink("fyne.io/fyne/v2/canvas")
	}

	w, h := m.width, m.height
	if w <= 0 {
		w = 300
	}
	if h <= 0 {
		h = 150
	}
	dcField := modelFieldRef(canvasCtxField(id))
	imgField := modelFieldRef(id)

	newCtx := &ir.Call{
		Type:     ir.NativeGoPointerOf("gg.Context"),
		Receiver: &ir.Ident{Name: "gg"},
		Func:     &ir.Func{NativePkg: ggImportPath, NativeName: "gg.NewContext"},
		Args: []ir.CallArg{
			{Value: &ir.Literal{Type: ir.TypInt, Raw: fmt.Sprint(w)}},
			{Value: &ir.Literal{Type: ir.TypInt, Raw: fmt.Sprint(h)}},
		},
	}
	drawCall := &ir.Call{
		Type:     ir.TypVoid,
		Receiver: &ir.Ident{Name: "m"},
		Func:     &ir.Func{Name: m.draw.Name},
		Args:     []ir.CallArg{{Value: dcField}},
	}
	newImg := &ir.Call{
		Type:     ir.NativeGoPointerOf("canvas.Image"),
		Receiver: &ir.Ident{Name: "canvas"},
		Func:     &ir.Func{NativePkg: "fyne.io/fyne/v2/canvas", NativeName: "canvas.NewImageFromImage"},
		Args:     []ir.CallArg{{Value: methodCall(dcField, "Image", nil, ir.TypDyn)}},
	}
	return []ir.Stmt{
		&ir.Assign{Target: dcField, Op: ast.AssignSet, Value: newCtx},
		&ir.CallStmt{Call: drawCall},
		&ir.Assign{Target: imgField, Op: ast.AssignSet, Value: newImg},
		&ir.Assign{
			Target: &ir.Select{Operand: imgField, Field: "FillMode", Type: ir.TypDyn},
			Op:     ast.AssignSet,
			Value:  &ir.Ident{Name: "canvas.ImageFillOriginal", Type: ir.TypDyn},
		},
	}
}

// emitIRCanvasDraw emits a synthesized `_canvasDrawN(ctx)` func as a Model
// method `func (m *Model) _canvasDrawN(ctx *gg.Context)`, translating each
// canvas-intrinsic CallStmt body statement into native gg calls.
func emitIRCanvasDraw(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext, byFunc map[*ir.Func]*canvasMeta, importSink func(string)) {
	tr := newFyneTranslator(gc, platformBlueprints(), func(string, string) {}, importSink)
	tr.canvasByFunc = byFunc
	body := codegen.WalkLowered(context.Background(), fn.Block, tr)
	synthesized := &ir.Func{
		Name:     fn.Name,
		Receiver: "Model",
		Params:   []*ir.Param{{Name: "ctx", Type: ir.NativeGoPointerOf("gg.Context")}},
		Return:   ir.TypVoid,
		Block:    body,
	}
	for _, line := range gc.EmitFuncDef(synthesized) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}
