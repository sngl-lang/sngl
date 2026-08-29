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
// passCanvas (internal/lower) extracts a `canvas`+shapes subtree into a
// synthesized `_canvasDrawN(ctx)` func whose body is a sequence of canvas
// intrinsic CallStmts (CanvasSave / CanvasApplyStyle / CanvasDrawRect / ...).
// passDeclarative then flattens the canvas NodeInst to a
// `lower.CreateNode("canvas")` LocalVar, threading the draw func + pixel
// dimensions onto LocalVar.CanvasDraw / CanvasWidth / CanvasHeight.
//
// fyne renders via the shared pkg/go/canvas runtime (software raster over
// github.com/fogleman/gg): ctx is a *snglcanvas.Context, the canvas widget is a
// *canvas.Image built from ctx.Result(). Reactive redraws re-rasterise and call
// Refresh().
//
// The canvas intrinsics are NOT registered in the lang-keyed intrinsic
// registry (that one is shared and JS-specific). They are translated via the
// shared canvasutil.GoContextStmts helper (also used by bubbletea) into
// Context method calls.

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
func collectCanvases(pkg *ir.Package, funcs []*ir.Func) (map[string]*canvasMeta, map[*ir.Func]*canvasMeta) {
	return canvasutil.Collect(pkg, funcs)
}

// canvasByIDFor delegates to the shared canvasutil rebuild.
func canvasByIDFor(byFunc map[*ir.Func]*canvasMeta) map[string]*canvasMeta {
	return canvasutil.ByIDFor(byFunc)
}

// translateCanvasIntrinsic rewrites one canvas-intrinsic CallStmt (inside a
// draw func body) into pkg/go/canvas Context method calls via the shared
// canvasutil helper. canvasState (per-translator, i.e. per draw func) gives
// each ApplyStyle a uniquely-named style local.
func (t *fyneTranslator) translateCanvasIntrinsic(cs *ir.CallStmt) []ir.Stmt {
	if t.canvasState == nil {
		t.canvasState = &canvasutil.GoCanvasState{}
	}
	return canvasutil.GoContextStmts(cs, t.canvasState)
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
	m := t.canvasMetaForDraw(rs.DrawFunc)
	if m == nil {
		return nil
	}
	dcField := codegen.ModelFieldRef(canvasCtxField(m.ID))
	imgField := codegen.ModelFieldRef(m.ID)
	w, h := canvasDims(m)
	drawCall := &ir.Call{
		Type:     ir.TypVoid,
		Receiver: &ir.Ident{Name: "m"},
		Func:     &ir.Func{Name: m.Draw.Name},
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

// canvasMetaForDraw resolves the canvasMeta for a draw func via the
// translator's shared map.
func (t *fyneTranslator) canvasMetaForDraw(draw *ir.Func) *canvasMeta {
	if t.canvasByFunc == nil {
		return nil
	}
	return t.canvasByFunc[draw]
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
	t.fieldSink(id, "*canvas.Image")
	t.fieldSink(canvasCtxField(id), "*"+snglCanvasAlias+".Context")
	t.topLevel = append(t.topLevel, id)
	if t.importSink != nil {
		t.importSink(snglCanvasImportPath)
		t.importSink("fyne.io/fyne/v2/canvas")
		// The emitted CanvasStyle struct decl references snglcolor.Color.
		t.importSink(canvasutil.ColorImportPath)
	}

	w, h := canvasDims(m)
	dcField := codegen.ModelFieldRef(canvasCtxField(id))
	imgField := codegen.ModelFieldRef(id)

	drawCall := &ir.Call{
		Type:     ir.TypVoid,
		Receiver: &ir.Ident{Name: "m"},
		Func:     &ir.Func{Name: m.Draw.Name},
		Args:     []ir.CallArg{{Value: dcField}},
	}
	newImg := &ir.Call{
		Type:     ir.NativeGoPointerOf("canvas.Image"),
		Receiver: &ir.Ident{Name: "canvas"},
		Func:     &ir.Func{Foreign: ir.Foreign{Path: "fyne.io/fyne/v2/canvas", Name: "canvas.NewImageFromImage"}},
		Args:     []ir.CallArg{{Value: methodCall(dcField, "Result", nil, ir.TypDyn)}},
	}
	return []ir.Stmt{
		&ir.Assign{Target: dcField, Op: ast.AssignSet, Value: newCanvasContextCall(w, h)},
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
// method `func (m *Model) _canvasDrawN(ctx *snglcanvas.Context)`, translating
// each canvas-intrinsic CallStmt body statement into Context method calls.
func emitIRCanvasDraw(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext, byFunc map[*ir.Func]*canvasMeta, specs map[string]*fyneSpec, importSink func(string)) {
	tr := newFyneTranslator(gc, specs, func(string, string) {}, importSink)
	tr.canvasByFunc = byFunc
	body := codegen.WalkLowered(context.Background(), fn.Block, tr)
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
