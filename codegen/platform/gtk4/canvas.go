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

// The 2D primitives are translated in this package rather than through an
// IntrinsicEmitter, which renders one expression and could not carry the
// statements and pending style these need. Declaring the package is how that
// implementation becomes visible to the completeness check.
func init() { codegen.DeclarePlatformImplements("gtk4", "sngl:internal/draw") }

// Canvas2D rendering for gtk4 via cairo.
//
// passShapeDraw (internal/lower) turns a `canvas`+shapes subtree into a
// synthesized `_canvasDrawN(ctx)` func. Two kinds of statement reach it: the
// bracket around a composed shape (CanvasSave / CanvasRestore, the only canvas
// intrinsics anything still emits), and whatever a shape override's own
// `@draw` handler was written as -- for gtk4 that is cairo natives, since
// `component shapes.circle[platform]` here paints with them directly.
//
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

// translateCanvasIntrinsic rewrites one canvas-intrinsic CallStmt (inside a
// draw func body) into native cairo calls. What reaches here is the bracket
// passCanvas puts around a composed shape -- Save and Restore -- since the
// drawing itself is the shape's own override by the time this runs.
func (t *gtk4Translator) translateCanvasIntrinsic(cs *ir.CallStmt) []ir.Stmt {
	call := cs.Call
	id := call.Func.Intrinsic
	cr := call.Args[0].Value

	switch id {
	case "CanvasSave":
		return []ir.Stmt{cairoCall("cairo_save", cr)}
	case "CanvasRestore":
		return []ir.Stmt{cairoCall("cairo_restore", cr)}
	}
	return []ir.Stmt{cs}
}

// translateCanvasRedraw rewrites a CanvasRedrawStmt into a
// gtk_widget_queue_draw on the matching canvas drawing-area Model field.
func (t *gtk4Translator) translateCanvasRedraw(rs *ir.CanvasRedrawStmt) []ir.Stmt {
	m := t.canvasMetaForNode(rs.Canvas)
	if m == nil {
		return nil
	}
	widget := cgoCast("GtkWidget", t.fieldRef(m.ID))
	return []ir.Stmt{&ir.CallStmt{Call: nativeCall("gtk_widget_queue_draw", widget)}}
}

// canvasMetaForNode resolves the canvasMeta for a canvas node via the
// translator's shared map.
func (t *gtk4Translator) canvasMetaForNode(n *ir.NodeInst) *canvasMeta {
	if t.shared == nil {
		return nil
	}
	return t.shared.canvasByNode[n]
}

// canvasMetaForID resolves the canvasMeta for a flattened canvas node id.
func (t *gtk4Translator) canvasMetaForID(id string) *canvasMeta {
	if t.shared == nil {
		return nil
	}
	return t.shared.canvasByID[id]
}

// emitCanvasCreate emits the OnCreateNode result for a `canvas` tag: register
// the *C.GtkDrawingArea Model field, construct it sized to the canvas, and
// register the cairo draw callback via the registry-indexed trampoline.
func (t *gtk4Translator) emitCanvasCreate(id string) []ir.Stmt {
	m := t.canvasMetaForID(id)
	if m == nil {
		return nil
	}
	t.fieldSink(id, "GtkDrawingArea")
	t.fieldIDs[id] = true
	t.idCTypes[id] = "GtkDrawingArea"
	t.topLevel = append(t.topLevel, id)

	w, h := m.Width, m.Height
	if w <= 0 {
		w = 300
	}
	if h <= 0 {
		h = 150
	}
	daField := t.fieldRef(id)

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
	// The draw func is a method of whatever scope owns the canvas -- the Model,
	// or a component instance's record -- so the receiver is read off the
	// context rather than spelled `m`.
	closure := &ir.Ident{
		Name: fmt.Sprintf("func(cr *C.cairo_t, pw, ph int) { _snglCairoScale(cr, pw, ph, %d, %d, %q); %s.%s(cr) }",
			w, h, m.Scaling, t.gc.RecvName(), m.DrawName),
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

// emitIRCanvasDraw wraps one drawing's statements in a Model method
// `func (m *Model) _canvasDrawN(ctx *C.cairo_t)`, translating each
// canvas-intrinsic CallStmt into native cairo calls.
//
// The method is this platform's: three places invoke a drawing, so the
// statements the tree carries are wrapped once here and called by name.
func emitIRCanvasDraw(b *strings.Builder, cv *codegen.Canvas, gc *golang.GoIRContext, reg *gir.TypeRegistry, shared *emitShared) {
	// The registry and the shared sink are not optional even though a draw
	// body reaches mostly cairo intrinsics: *emitShared is nil-safe, so
	// without the sink a fail() here would be discarded and the build would
	// emit the broken call anyway, and a needBoolToInt() would silently omit
	// the helper the emitted code then references.
	tr := newGtk4Translator(gc, func(string, string) {}).withRegistry(reg).withShared(shared)
	body := codegen.WalkLowered(context.Background(), cv.Draw, tr)
	synthesized := &ir.Func{
		Name:     cv.Name,
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
