package fyne

import (
	"context"
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/canvasutil"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// canvasMeta aliases the shared platform-neutral canvas metadata type. The
// collection + Go stdlib struct decls live in codegen/canvasutil (shared with
// gtk4); the gg-specific translation stays in this package.
type canvasMeta = canvasutil.Meta

// Canvas2D rendering for fyne.
//
// passShapeDraw (internal/lower) replaces a canvas's shape children in place
// with the statements that paint them -- nothing is synthesized, and the
// `_canvasDrawN` name is codegen's own. Every one of those statements is what a
// shape override's own `@draw` handler was written as. fyne declares no shape
// overrides and inherits `sngl:language/go`'s, which paint through
// `#[go.native]` methods on the pkg/go/canvas runtime -- so what arrives here
// is ordinary method calls the Go emitter already handles, and this package
// translates no canvas intrinsic at all.
//
// passDeclarative then flattens the canvas NodeInst to a
// `lower.CreateNode("canvas")` LocalVar, threading the draw func + pixel
// dimensions onto the record canvasutil.Collect reads.
//
// fyne renders via the shared pkg/go/canvas runtime (software raster over
// github.com/fogleman/gg): ctx is a *snglcanvas.Context, the canvas widget is a
// *canvas.Image built from ctx.Result(). Reactive redraws re-rasterise and call
// Refresh().
//
// There are no canvas intrinsics left to translate. The shared
// canvasutil.GoContextStmts helper that turned the last two into Context
// method calls is gone with them.

// snglCanvasImportPath is the SNGL Go canvas runtime; snglCanvasAlias is the
// forced import alias (the path's default "canvas" collides with fyne's own
// canvas package).
const (
	snglCanvasImportPath = "git.duckfam.us/jonathan/sngl/pkg/go/canvas"
	snglCanvasAlias      = "snglcanvas"
)

// canvasCtxType is the IR native type for the draw-func ctx parameter and the
// Model field backing a canvas: *snglcanvas.Context.
func canvasCtxType() *ir.Type { return ir.NativeGoPointerOf(snglCanvasAlias + ".Context") }

// The canvas stdlib structs (lib/canvas.sngl: CanvasStyle, PathCmd;
// lib/types.sngl: color → Color) are referenced by the synthesized draw funcs
// (e.g. style.Fill.R read by the ApplyStyle setters, _cmd.op in the path loop)
// but are NOT carried in pkg.Structs (html emits JS object literals), so the
// fyne Go path declares them here when any canvas is present. Field names/types
// mirror the stdlib definitions; canvas_sync_test.go guards against drift.

// canvasStdlibDeclsExcluding returns the canvas stdlib struct decls (from the
// shared canvasutil table), omitting any struct whose name collides with a
// user-declared struct already in td.Structs. With the Context setter API,
// generated code no longer needs an image/color helper.
func canvasStdlibDeclsExcluding(structs []structData) string {
	declared := map[string]struct{}{}
	for _, s := range structs {
		switch s.Name {
		case "Color", "CanvasStyle", "PathCmd":
			declared[s.Name] = struct{}{}
		}
	}
	return canvasutil.StructDeclsExcluding(declared)
}

// collectCanvases delegates to the shared canvasutil collector.
func collectCanvases(draws *codegen.CanvasDraws) (map[string]*canvasMeta, map[*ir.NodeInst]*canvasMeta) {
	return canvasutil.Collect(draws)
}

// methodStmt builds `receiver.Method(args...)` as a CallStmt.
func methodStmt(receiver ir.Expr, method string, args ...ir.Expr) ir.Stmt {
	return &ir.CallStmt{Call: methodCall(receiver, method, args, ir.TypVoid)}
}

// translateCanvasRedraw rewrites a CanvasRedrawStmt into the fyne re-raster
// sequence for the matching canvas widget field. The runtime Context has no
// clear, so a fresh one is allocated each redraw:
//
//	m.<id>Ctx = snglcanvas.New(w, h)
//	m._canvasDrawN(m.<id>Ctx)
//	m.<id>.Image = m.<id>Ctx.Result()
//	m.<id>.Refresh()
func (t *fyneTranslator) translateCanvasRedraw(rs *ir.CanvasRedrawStmt) []ir.Stmt {
	m := t.canvasMetaForNode(rs.Canvas)
	if m == nil {
		return nil
	}
	// A Raster redraws by asking: Refresh re-runs the generator, at whatever
	// size the widget is now.
	if m.Scaling != "" && m.Scaling != canvasutil.ScaleCenter {
		return []ir.Stmt{methodStmt(t.fieldRef(m.ID), "Refresh")}
	}
	dcField := t.fieldRef(canvasCtxField(m.ID))
	imgField := t.fieldRef(m.ID)
	w, h := canvasDims(m)
	drawCall := &ir.Call{
		Type:     ir.TypVoid,
		Receiver: t.recvIdent(),
		Func:     &ir.Func{Name: m.DrawName},
		Args:     []ir.CallArg{{Value: dcField}},
	}
	return []ir.Stmt{
		&ir.Assign{Target: dcField, Op: ast.AssignSet, Value: newCanvasContextCall(w, h)},
		&ir.CallStmt{Call: drawCall},
		&ir.Assign{
			Target: &ir.Select{Operand: imgField, Field: "Image", Type: ir.TypDyn},
			Op:     ast.AssignSet,
			Value:  methodCall(dcField, "Result", nil, ir.TypDyn),
		},
		methodStmt(imgField, "Refresh"),
	}
}

// canvasMetaForNode resolves the canvasMeta for a canvas node via the
// translator's shared map.
func (t *fyneTranslator) canvasMetaForNode(n *ir.NodeInst) *canvasMeta {
	if t.canvasByNode == nil {
		return nil
	}
	return t.canvasByNode[n]
}

// canvasCtxField names the *snglcanvas.Context Model field backing a canvas
// widget.
func canvasCtxField(id string) string { return id + "Ctx" }

// canvasDims returns the canvas pixel dimensions, defaulting to the HTML canvas
// 300x150 when unset.
func canvasDims(m *canvasMeta) (int, int) {
	w, h := m.Width, m.Height
	if w <= 0 {
		w = 300
	}
	if h <= 0 {
		h = 150
	}
	return w, h
}

// newCanvasContextCall builds `snglcanvas.New(w, h)` returning a
// *snglcanvas.Context.
func newCanvasContextCall(w, h int) *ir.Call {
	return &ir.Call{
		Type:     canvasCtxType(),
		Receiver: &ir.Ident{Name: snglCanvasAlias},
		Func:     &ir.Func{Foreign: ir.Foreign{Path: snglCanvasImportPath, Name: snglCanvasAlias + ".New"}},
		Args: []ir.CallArg{
			{Value: &ir.Literal{Type: ir.TypInt, Value: fmt.Sprint(w)}},
			{Value: &ir.Literal{Type: ir.TypInt, Value: fmt.Sprint(h)}},
		},
	}
}

// emitCanvasCreate emits the OnCreateNode result for a `canvas` tag: register
// the *canvas.Image and *snglcanvas.Context Model fields, build the context
// sized to the canvas, rasterise once, and create the image widget.
func (t *fyneTranslator) emitCanvasCreate(id string) []ir.Stmt {
	m := t.canvasByID[id]
	if m == nil {
		return nil
	}
	t.topLevel = append(t.topLevel, id)
	t.fieldIDs[id] = true
	if t.importSink != nil {
		t.importSink(snglCanvasImportPath)
		t.importSink("fyne.io/fyne/v2/canvas")
		// The emitted CanvasStyle struct decl references snglcolor.Color.
		t.importSink(canvasutil.ColorImportPath)
	}

	w, h := canvasDims(m)
	imgField := t.fieldRef(id)

	// A scaled canvas is a Raster, because Fyne hands a Raster's generator the
	// pixel size it is about to be drawn at -- which is the one thing an Image
	// cannot know. The shapes are then rasterised at the size they are shown
	// instead of drawn small and resampled up, which is the difference between
	// a seven-segment display with edges and one without.
	if m.Scaling != "" && m.Scaling != canvasutil.ScaleCenter {
		t.fieldSink(id, "*canvas.Raster")
		// The surface outlives the drawing: Fyne asks the generator for a
		// picture on every redraw, and a buffer allocated per call is the
		// whole image thrown away per keypress.
		t.fieldSink(canvasSurfaceField(id), snglCanvasAlias+".Surface")
		if t.importSink != nil {
			t.importSink("image")
		}
		return []ir.Stmt{
			&ir.Assign{Target: imgField, Op: ast.AssignSet, Value: rawGoExpr(t.rasterExpr(m, w, h))},
			methodStmt(imgField, "SetMinSize", newFyneSizeCall(w, h)),
		}
	}

	t.fieldSink(id, "*canvas.Image")
	t.fieldSink(canvasCtxField(id), "*"+snglCanvasAlias+".Context")
	dcField := t.fieldRef(canvasCtxField(id))

	drawCall := &ir.Call{
		Type:     ir.TypVoid,
		Receiver: t.recvIdent(),
		Func:     &ir.Func{Name: m.DrawName},
		Args:     []ir.CallArg{{Value: dcField}},
	}
	newImg := &ir.Call{
		Type:     ir.NativeGoPointerOf("canvas.Image"),
		Receiver: &ir.Ident{Name: "canvas"},
		Func:     &ir.Func{Foreign: ir.Foreign{Path: "fyne.io/fyne/v2/canvas", Name: "canvas.NewImageFromImage"}},
		Args:     []ir.CallArg{{Value: methodCall(dcField, "Result", nil, ir.TypDyn)}},
	}
	out := []ir.Stmt{
		&ir.Assign{Target: dcField, Op: ast.AssignSet, Value: newCanvasContextCall(w, h)},
		&ir.CallStmt{Call: drawCall},
		&ir.Assign{Target: imgField, Op: ast.AssignSet, Value: newImg},
		&ir.Assign{
			Target: &ir.Select{Operand: imgField, Field: "FillMode", Type: ir.TypDyn},
			Op:     ast.AssignSet,
			Value:  &ir.Ident{Name: "canvas.ImageFillOriginal", Type: ir.TypDyn},
		},
	}
	// ImageFillOriginal only grows the image once its renderer has run, and a
	// container lays out before that -- so a canvas placed in a box came out
	// one pixel tall. The canvas declared its pixel size; say so.
	out = append(out, methodStmt(imgField, "SetMinSize", newFyneSizeCall(w, h)))
	return out
}

// rasterExpr is the `canvas.NewRaster` call for a scaled canvas: a generator
// that allocates a context at the pixel size Fyne asks for, scales the
// drawing's own coordinate space into it, and hands back the image.
//
// Written as Go source rather than built as IR because it is a closure over
// the Model, and the point of it is the two parameters Fyne passes in.
func (t *fyneTranslator) rasterExpr(m *canvasMeta, w, h int) string {
	// The surface and the draw func belong to whatever scope owns the canvas --
	// the Model, or a component instance's record -- so the receiver is read
	// off the context rather than spelled `m`.
	recv := t.gc.RecvName()
	return fmt.Sprintf(
		"canvas.NewRaster(func(pw, ph int) image.Image {\n"+
			"\t\tctx := %s.%s.Begin(pw, ph, %d, %d, %q)\n"+
			"\t\t%s.%s(ctx)\n"+
			"\t\treturn ctx.Result()\n"+
			"\t})",
		recv, canvasSurfaceField(m.ID), w, h, m.Scaling, recv, m.DrawName)
}

// canvasTapField names the fynelayout.Tap a clickable canvas is shown in.
func canvasTapField(id string) string { return id + "Tap" }

// attachCanvasClick puts the canvas in a fynelayout.Tap and hands its
// OnTapped the handler. No Fyne callback signature replaces the handler's
// SNGL parameter, so it is handed the declared payload.
func (t *fyneTranslator) attachCanvasClick(id, event string, handler ir.Expr) []ir.Stmt {
	tap := canvasTapField(id)
	t.fieldSink(tap, "*fynelayout.Tap")
	t.fieldIDs[tap] = true
	t.canvasTaps[id] = true
	if t.importSink != nil {
		t.importSink(fyneLayoutImportPath)
	}
	for i, name := range t.topLevel {
		if name == id {
			t.topLevel[i] = tap
		}
	}
	if t.invokerSink != nil && !strings.HasPrefix(id, "__n") {
		t.invokerSink(fyneEventInvoker{IDLabel: id, SnglEvent: codegen.TriggerEventName(handler, event), Field: "OnTapped", Target: tap})
	}
	arg := ""
	if pt, _ := codegen.HandlerPayload(handler); pt != nil {
		arg = golang.IRTypeToGo(pt) + "{}"
	}
	tapRef := t.fieldRef(tap)
	newTap := nativeCallAt("fynelayout.NewTap", fyneLayoutImportPath, []ir.Expr{t.fieldRef(id)}, ir.TypDyn)
	return []ir.Stmt{
		&ir.Assign{Target: tapRef, Op: ast.AssignSet, Value: newTap},
		&ir.Assign{
			Target: &ir.Select{Operand: tapRef, Field: "OnTapped", Type: ir.TypDyn},
			Op:     ast.AssignSet,
			Value:  rawGoExpr(fmt.Sprintf("func() { %s(%s) }", t.gc.EvalExpr(t.qualifyHandlerFunc(handler)), arg)),
		},
	}
}

// canvasSurfaceField names the reusable drawing target behind a scaled canvas.
func canvasSurfaceField(id string) string { return id + "Surface" }

// newFyneSizeCall builds `fyne.NewSize(w, h)`.
func newFyneSizeCall(w, h int) *ir.Call {
	return &ir.Call{
		Type:     ir.TypDyn,
		Receiver: &ir.Ident{Name: "fyne"},
		Func:     &ir.Func{Foreign: ir.Foreign{Path: "fyne.io/fyne/v2", Name: "fyne.NewSize"}},
		Args: []ir.CallArg{
			{Value: &ir.Literal{Type: ir.TypInt, Value: fmt.Sprint(w)}},
			{Value: &ir.Literal{Type: ir.TypInt, Value: fmt.Sprint(h)}},
		},
	}
}

// emitIRCanvasDraw wraps one drawing's statements in a Model method
// `func (m *Model) _canvasDrawN(ctx *snglcanvas.Context)`, translating each
// canvas-intrinsic CallStmt into Context method calls.
//
// The method is this platform's, not a declaration the IR carries: three
// places invoke a drawing, so the statements are wrapped once here and called
// by name.
func emitIRCanvasDraw(b *strings.Builder, cv *codegen.Canvas, gc *golang.GoIRContext, byNode map[*ir.NodeInst]*canvasMeta, specs map[string]*fyneSpec, importSink func(string), failSink func(error)) {
	tr := newFyneTranslator(gc, specs, func(string, string) {}, importSink, failSink)
	tr.canvasByNode = byNode
	body := codegen.WalkLowered(context.Background(), cv.Draw, tr)
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
