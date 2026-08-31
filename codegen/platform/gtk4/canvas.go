package gtk4

import (
	"context"
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/canvasutil"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4/gir"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Canvas2D rendering for gtk4 via cairo.
//
// passCanvas (internal/lower) extracts a `canvas`+shapes subtree into a
// synthesized `_canvasDrawN(ctx)` func whose body is a sequence of canvas
// intrinsic CallStmts (CanvasSave / CanvasApplyStyle / CanvasDrawRect / ...).
// passDeclarative then flattens the canvas NodeInst to a
// `lower.CreateNode("canvas")` LocalVar, threading the draw func + pixel
// dimensions onto LocalVar.CanvasDraw / CanvasWidth / CanvasHeight (recovered
// here via canvasutil.Collect).
//
// gtk4 renders with a GtkDrawingArea: the draw func is a `void (*)(cr ...)`
// cairo callback, registered via a registry-indexed trampoline that mirrors
// the existing sngl_connect/snglCallbacks signal pattern. The `ctx` param of
// the synthesized draw func is a *C.cairo_t. Reactive redraws call
// gtk_widget_queue_draw on the drawing area.
//
// The canvas intrinsics are NOT registered in the lang-keyed intrinsic
// registry (that one is JS-specific and would collide with another Go
// platform). They are translated here, inside the gtk4 translator — the same
// way gtk4 rewrites widget calls — because cairo's 2D API is gtk4-specific.

// canvasMeta aliases the shared platform-neutral canvas metadata type.
type canvasMeta = canvasutil.Meta

// canvasCairoHelpers is the gtk4-specific runtime support for the synthesized
// draw funcs: a 0..255→0..1 source setter and the alpha-gated fill/stroke
// painters (fill when fill.a>0, stroke when stroke.a>0). Emitted into model.go
// alongside the canvas stdlib struct decls whenever any canvas is present.
const canvasCairoHelpers = `// _snglCairoScale maps a drawing's own coordinate space onto the size GTK is
// drawing at. An empty mode leaves the context alone, which is what a canvas
// shown at its own size wants.
func _snglCairoScale(cr *C.cairo_t, pw, ph, w, h int, mode string) {
	if w <= 0 || h <= 0 || mode == "" {
		return
	}
	sx, sy := float64(pw)/float64(w), float64(ph)/float64(h)
	switch mode {
	case "stretch":
	case "fill":
		if sy > sx {
			sx = sy
		}
		sy = sx
	default:
		if sy < sx {
			sx = sy
		}
		sy = sx
	}
	C.cairo_scale(cr, C.double(sx), C.double(sy))
}

func _snglCairoSource(cr *C.cairo_t, c ` + canvasutil.ColorGoType + `) {
	C.cairo_set_source_rgba(cr, C.double(float64(c.R)/255.0), C.double(float64(c.G)/255.0), C.double(float64(c.B)/255.0), C.double(float64(c.A)/255.0))
}

// _snglCairoPaint fills (when fill.a>0) and strokes (when stroke.a>0) the
// current path. cairo_fill_preserve keeps the path so a following stroke
// applies to the same geometry.
func _snglCairoPaint(cr *C.cairo_t, s CanvasStyle) {
	if s.Fill.A > 0 {
		_snglCairoSource(cr, s.Fill)
		if s.Stroke.A > 0 {
			C.cairo_fill_preserve(cr)
		} else {
			C.cairo_fill(cr)
		}
	}
	if s.Stroke.A > 0 {
		C.cairo_set_line_width(cr, C.double(s.StrokeWidth))
		_snglCairoSource(cr, s.Stroke)
		C.cairo_stroke(cr)
	}
}

// _snglCairoStroke strokes the current path (line primitives have no fill).
func _snglCairoStroke(cr *C.cairo_t, s CanvasStyle) {
	if s.Stroke.A > 0 {
		C.cairo_set_line_width(cr, C.double(s.StrokeWidth))
		_snglCairoSource(cr, s.Stroke)
	}
	C.cairo_stroke(cr)
}
`

// canvasStdlibDeclsExcluding returns the canvas stdlib struct decls (from the
// shared canvasutil table), omitting any struct whose name collides with a
// user-declared struct, then appends the gtk4 cairo helpers.
func canvasStdlibDeclsExcluding(structs []structData) string {
	declared := map[string]struct{}{}
	for _, s := range structs {
		switch s.Name {
		case "Color", "CanvasStyle", "PathCmd":
			declared[s.Name] = struct{}{}
		}
	}
	return canvasutil.StructDeclsExcluding(declared) + canvasCairoHelpers
}

// cairoCall builds a `C.cairo_*(cr, args...)` CallStmt.
func cairoCall(name string, cr ir.Expr, args ...ir.Expr) ir.Stmt {
	all := append([]ir.Expr{cr}, args...)
	return &ir.CallStmt{Call: nativeCall(name, all...)}
}

// dbl wraps an expr in C.double(...) so cairo's double params type-check.
func dbl(e ir.Expr) ir.Expr { return nativeCall("double", e) }

// twoPi is the literal 2*math.Pi as a Go float, used for full-circle arcs.
// Spelled out so no "math" import is needed in the cgo file.
const twoPiLit = "6.283185307179586"

func twoPi() ir.Expr { return &ir.Literal{Type: ir.TypFloat, Value: twoPiLit} }
func zeroF() ir.Expr { return &ir.Literal{Type: ir.TypFloat, Value: "0"} }

// goHelperCall builds `<helper>(args...)` as a plain Go function call (not a
// cgo C call) — used for the _snglCairo* runtime helpers emitted in model.go.
func goHelperCall(helper string, args ...ir.Expr) *ir.Call {
	callArgs := make([]ir.CallArg, len(args))
	for i, a := range args {
		callArgs[i] = ir.CallArg{Value: a}
	}
	return &ir.Call{Type: ir.TypVoid, Func: &ir.Func{Name: helper}, Args: callArgs}
}

// styleHelperCall builds `<helper>(cr, _styleN)` where the second arg is the
// pending CanvasStyle local (or a zero CanvasStyle{} when none was applied).
func (t *gtk4Translator) styleHelperCall(helper string, cr ir.Expr) ir.Stmt {
	style := t.takeCanvasStyle()
	return &ir.CallStmt{Call: goHelperCall(helper, cr, style)}
}

// takeCanvasStyle returns the pending style (set by the preceding
// CanvasApplyStyle) and clears it. When no style was applied it yields a
// zero CanvasStyle{} literal so the painter no-ops (fill.a==0, stroke.a==0).
//
// INVARIANT: every draw-primitive case in translateCanvasIntrinsic must
// consume the pending style exactly once (here or via styleHelperCall). A
// primitive that skipped it would leak the previous shape's style onto the
// next — which is why CanvasDrawImage calls it despite emitting nothing.
func (t *gtk4Translator) takeCanvasStyle() ir.Expr {
	s := t.pendingCanvasStyle
	t.pendingCanvasStyle = nil
	if s == nil {
		return &ir.Ident{Name: "CanvasStyle{}", Type: ir.TypDyn}
	}
	return s
}

// translateCanvasIntrinsic rewrites one canvas-intrinsic CallStmt (inside a
// draw func body) into native cairo calls. passCanvas emits a fixed per-shape
// structure: Save, [ApplyStyle], DrawPrimitive, Restore. ApplyStyle binds a
// `_styleN` local that the following primitive's fill/stroke reference.
func (t *gtk4Translator) translateCanvasIntrinsic(cs *ir.CallStmt) []ir.Stmt {
	call := cs.Call
	id := call.Func.Intrinsic
	cr := call.Args[0].Value
	rest := call.Args[1:]
	arg := func(i int) ir.Expr { return rest[i].Value }

	switch id {
	case "CanvasSave":
		return []ir.Stmt{cairoCall("cairo_save", cr)}
	case "CanvasRestore":
		return []ir.Stmt{cairoCall("cairo_restore", cr)}
	case "CanvasApplyStyle":
		// Bind the style to a fresh local so the following draw's fill/stroke
		// don't re-evaluate the (possibly large) style expression repeatedly.
		t.canvasStyleCounter++
		name := fmt.Sprintf("_style%d", t.canvasStyleCounter)
		t.pendingCanvasStyle = &ir.Ident{Name: name, Type: ir.TypDyn}
		return []ir.Stmt{&ir.LocalVar{Name: name, Init: arg(0)}}
	case "CanvasDrawRect":
		return []ir.Stmt{
			cairoCall("cairo_rectangle", cr, dbl(arg(0)), dbl(arg(1)), dbl(arg(2)), dbl(arg(3))),
			t.styleHelperCall("_snglCairoPaint", cr),
		}
	case "CanvasDrawCircle":
		// new_sub_path so the arc starts a fresh subpath (avoids a stray line
		// from the current point to the arc start).
		return []ir.Stmt{
			cairoCall("cairo_new_sub_path", cr),
			cairoCall("cairo_arc", cr, dbl(arg(0)), dbl(arg(1)), dbl(arg(2)), zeroF(), twoPi()),
			t.styleHelperCall("_snglCairoPaint", cr),
		}
	case "CanvasDrawEllipse":
		// cairo has no native ellipse. The CTM-scale trick (translate+scale+arc)
		// distorts the stroke: cairo applies the CTM at paint time, so scaling by
		// (rx, ry) scales the pen too, rendering a hugely thick, anisotropic
		// outline (~2x the intended size vs other platforms). Instead build the
		// ellipse as an explicit cubic-Bézier path at real coordinates so the
		// stroke runs in screen space with a uniform width. kappa is the standard
		// circle-to-Bézier control-point ratio.
		n := t.canvasStyleCounter
		mk := func(suffix string, init ir.Expr) (string, ir.Stmt) {
			name := fmt.Sprintf("_e%s%d", suffix, n)
			return name, &ir.LocalVar{Name: name, Init: init}
		}
		const kappa = "0.5522847498307936"
		cxN, cxV := mk("cx", arg(0))
		cyN, cyV := mk("cy", arg(1))
		rxN, rxV := mk("rx", arg(2))
		ryN, ryV := mk("ry", arg(3))
		oxN, oxV := mk("ox", &ir.Binary{Op: ast.BinMul, Type: ir.TypFloat,
			Left: &ir.Ident{Name: rxN, Type: ir.TypFloat}, Right: &ir.Literal{Type: ir.TypFloat, Value: kappa}})
		oyN, oyV := mk("oy", &ir.Binary{Op: ast.BinMul, Type: ir.TypFloat,
			Left: &ir.Ident{Name: ryN, Type: ir.TypFloat}, Right: &ir.Literal{Type: ir.TypFloat, Value: kappa}})
		f := func(name string) ir.Expr { return &ir.Ident{Name: name, Type: ir.TypFloat} }
		add := func(a, b string) ir.Expr {
			return &ir.Binary{Op: ast.BinAdd, Type: ir.TypFloat, Left: f(a), Right: f(b)}
		}
		sub := func(a, b string) ir.Expr {
			return &ir.Binary{Op: ast.BinSub, Type: ir.TypFloat, Left: f(a), Right: f(b)}
		}
		return []ir.Stmt{
			cxV, cyV, rxV, ryV, oxV, oyV,
			cairoCall("cairo_new_sub_path", cr),
			cairoCall("cairo_move_to", cr, dbl(sub(cxN, rxN)), dbl(f(cyN))),
			cairoCall("cairo_curve_to", cr, dbl(sub(cxN, rxN)), dbl(sub(cyN, oyN)), dbl(sub(cxN, oxN)), dbl(sub(cyN, ryN)), dbl(f(cxN)), dbl(sub(cyN, ryN))),
			cairoCall("cairo_curve_to", cr, dbl(add(cxN, oxN)), dbl(sub(cyN, ryN)), dbl(add(cxN, rxN)), dbl(sub(cyN, oyN)), dbl(add(cxN, rxN)), dbl(f(cyN))),
			cairoCall("cairo_curve_to", cr, dbl(add(cxN, rxN)), dbl(add(cyN, oyN)), dbl(add(cxN, oxN)), dbl(add(cyN, ryN)), dbl(f(cxN)), dbl(add(cyN, ryN))),
			cairoCall("cairo_curve_to", cr, dbl(sub(cxN, oxN)), dbl(add(cyN, ryN)), dbl(sub(cxN, rxN)), dbl(add(cyN, oyN)), dbl(sub(cxN, rxN)), dbl(f(cyN))),
			cairoCall("cairo_close_path", cr),
			t.styleHelperCall("_snglCairoPaint", cr),
		}
	case "CanvasDrawLine":
		return []ir.Stmt{
			cairoCall("cairo_move_to", cr, dbl(arg(0)), dbl(arg(1))),
			cairoCall("cairo_line_to", cr, dbl(arg(2)), dbl(arg(3))),
			t.styleHelperCall("_snglCairoStroke", cr),
		}
	case "CanvasDrawText":
		style := t.takeCanvasStyle()
		var stmts []ir.Stmt
		// Apply font size + fill colour, then show the text at (x, y).
		stmts = append(stmts,
			cairoCall("cairo_set_font_size", cr, dbl(&ir.Select{Operand: style, Field: "fontSize", Type: ir.TypFloat})),
			&ir.CallStmt{Call: goHelperCall("_snglCairoSource", cr, &ir.Select{Operand: style, Field: "fill", Type: ir.TypDyn})},
			cairoCall("cairo_move_to", cr, dbl(arg(0)), dbl(arg(1))),
			cairoCall("cairo_show_text", cr, nativeCall("CString", arg(2))),
		)
		return stmts
	case "CanvasDrawPath":
		return t.translatePath(cr, arg(0))
	case "CanvasDrawImage":
		// Drawing an external image src needs async decode; unsupported in the
		// cairo path (consistent with fyne). Consume the pending style so it
		// doesn't leak onto the next shape, then emit nothing.
		t.takeCanvasStyle()
		return nil
	}
	return []ir.Stmt{cs}
}

// translatePath rewrites CanvasDrawPath(cr, cmds) into a range loop over the
// PathCmd list emitting cairo move_to/line_to/curve_to/close_path, then paint.
func (t *gtk4Translator) translatePath(cr, cmds ir.Expr) []ir.Stmt {
	loopVar := &ir.Ident{Name: "_cmd", Type: ir.TypDyn}
	opSel := &ir.Select{Operand: loopVar, Field: "op", Type: ir.TypString}
	field := func(name string) ir.Expr {
		return dbl(&ir.Select{Operand: loopVar, Field: name, Type: ir.TypFloat})
	}
	cmdIf := func(op string, then ir.Stmt) *ir.If {
		return &ir.If{
			Cond: &ir.Binary{Op: ast.BinEq, Left: opSel, Right: &ir.Literal{Type: ir.TypString, Value: op}},
			Body: []ir.Stmt{then},
		}
	}
	body := []ir.Stmt{
		cmdIf("moveTo", cairoCall("cairo_move_to", cr, field("x"), field("y"))),
		cmdIf("lineTo", cairoCall("cairo_line_to", cr, field("x"), field("y"))),
		cmdIf("bezierTo", cairoCall("cairo_curve_to", cr, field("cx1"), field("cy1"), field("cx2"), field("cy2"), field("x"), field("y"))),
		cmdIf("close", cairoCall("cairo_close_path", cr)),
	}
	loop := &ir.For{Key: "_cmd", Iter: cmds, Body: body}
	return []ir.Stmt{loop, t.styleHelperCall("_snglCairoPaint", cr)}
}

// translateCanvasRedraw rewrites a CanvasRedrawStmt into a
// gtk_widget_queue_draw on the matching canvas drawing-area Model field.
func (t *gtk4Translator) translateCanvasRedraw(rs *ir.CanvasRedrawStmt) []ir.Stmt {
	m := t.canvasMetaForDraw(rs.DrawFunc)
	if m == nil {
		return nil
	}
	widget := cgoCast("GtkWidget", codegen.ModelFieldRef(m.ID))
	return []ir.Stmt{&ir.CallStmt{Call: nativeCall("gtk_widget_queue_draw", widget)}}
}

// canvasMetaForDraw resolves the canvasMeta for a draw func via the
// translator's shared map.
func (t *gtk4Translator) canvasMetaForDraw(draw *ir.Func) *canvasMeta {
	if t.canvasByFunc == nil {
		return nil
	}
	return t.canvasByFunc[draw]
}

// emitCanvasCreate emits the OnCreateNode result for a `canvas` tag: register
// the *C.GtkDrawingArea Model field, construct it sized to the canvas, and
// register the cairo draw callback via the registry-indexed trampoline.
func (t *gtk4Translator) emitCanvasCreate(id string) []ir.Stmt {
	m := t.canvasByID[id]
	if m == nil {
		return nil
	}
	t.fieldSink(id, "GtkDrawingArea")
	t.idCTypes[id] = "GtkDrawingArea"
	t.topLevel = append(t.topLevel, id)

	w, h := m.Width, m.Height
	if w <= 0 {
		w = 300
	}
	if h <= 0 {
		h = 150
	}
	daField := codegen.ModelFieldRef(id)

	// m.<id> = (*C.GtkDrawingArea)(unsafe.Pointer(C.gtk_drawing_area_new()))
	ctor := cgoCast("GtkDrawingArea", nativeCall("gtk_drawing_area_new"))
	stmts := []ir.Stmt{
		&ir.Assign{Target: daField, Op: ast.AssignSet, Value: ctor},
		&ir.CallStmt{Call: nativeCall("gtk_drawing_area_set_content_width", daField, nativeCall("int", &ir.Literal{Type: ir.TypInt, Value: fmt.Sprint(w)}))},
		&ir.CallStmt{Call: nativeCall("gtk_drawing_area_set_content_height", daField, nativeCall("int", &ir.Literal{Type: ir.TypInt, Value: fmt.Sprint(h)}))},
	}
	if m.Scaling != "" && m.Scaling != canvasutil.ScaleCenter {
		// The content size stays as the natural one -- it is what the drawing
		// asks for -- but a canvas that scales takes more when there is more,
		// which is what expanding says in GTK.
		widget := cgoCast("GtkWidget", daField)
		stmts = append(stmts,
			&ir.CallStmt{Call: nativeCall("gtk_widget_set_hexpand", widget, &ir.Ident{Name: "1", Type: ir.TypDyn})},
			&ir.CallStmt{Call: nativeCall("gtk_widget_set_vexpand", widget, &ir.Ident{Name: "1", Type: ir.TypDyn})},
		)
	}

	// Register the draw callback through the trampoline registry:
	//   snglDrawFuncs = append(snglDrawFuncs, func(cr *C.cairo_t) { m._canvasDrawN(cr) })
	//   C.sngl_drawing_area_set_draw(unsafe.Pointer(m.<id>), C.int(len(snglDrawFuncs)-1))
	cbList := &ir.Ident{Name: "snglDrawFuncs", Type: ir.TypDyn}
	// The drawing's own coordinate space is scaled onto the size GTK is
	// drawing at, so the shapes are rasterised where they land rather than
	// drawn at one size and stretched from it. cairo is a vector context, so
	// this costs a transform and nothing else.
	closure := &ir.Ident{
		Name: fmt.Sprintf("func(cr *C.cairo_t, pw, ph int) { _snglCairoScale(cr, pw, ph, %d, %d, %q); m.%s(cr) }",
			w, h, m.Scaling, m.Draw.Name),
		Type: ir.TypDyn,
	}
	appendCall := &ir.Call{
		Type: ir.TypDyn,
		Func: &ir.Func{Name: "append"},
		Args: []ir.CallArg{{Value: cbList}, {Value: closure}},
	}
	stmts = append(stmts, &ir.Assign{Target: cbList, Op: ast.AssignSet, Value: appendCall})

	lenCall := &ir.Call{
		Type: ir.TypInt,
		Func: &ir.Func{Name: "len"},
		Args: []ir.CallArg{{Value: cbList}},
	}
	idx := nativeCall("int", &ir.Binary{Op: ast.BinSub, Left: lenCall, Right: &ir.Literal{Type: ir.TypInt, Value: "1"}})
	widgetPtr := cgoCast("", daField) // unsafe.Pointer(m.<id>)
	stmts = append(stmts, &ir.CallStmt{Call: nativeCall("sngl_drawing_area_set_draw", widgetPtr, idx)})

	return stmts
}

// emitIRCanvasDraw emits a synthesized `_canvasDrawN(ctx)` func as a Model
// method `func (m *Model) _canvasDrawN(cr *C.cairo_t)`, translating each
// canvas-intrinsic CallStmt body statement into native cairo calls.
func emitIRCanvasDraw(b *strings.Builder, fn *ir.Func, gc *golang.GoIRContext, reg *gir.TypeRegistry, shared *emitShared, byFunc map[*ir.Func]*canvasMeta) {
	// The registry and the shared sink are not optional even though a draw
	// body reaches mostly cairo intrinsics: *emitShared is nil-safe, so
	// without the sink a fail() here would be discarded and the build would
	// emit the broken call anyway, and a needBoolToInt() would silently omit
	// the helper the emitted code then references.
	tr := newGtk4Translator(gc, func(string, string) {}).withRegistry(reg).withShared(shared)
	tr.canvasByFunc = byFunc
	body := codegen.WalkLowered(context.Background(), fn.Block, tr)
	synthesized := &ir.Func{
		Name:     fn.Name,
		Receiver: "Model",
		// Param name matches the IR draw-func param ("ctx") the
		// canvas-intrinsic bodies reference; renaming would dangle them.
		Params: []*ir.Param{{Name: "ctx", Type: ir.NativePointerOf("cairo_t")}},
		Return: ir.TypVoid,
		Block:  body,
	}
	for _, line := range gc.EmitFuncDef(synthesized) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}
