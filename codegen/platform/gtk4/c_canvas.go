package gtk4

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	clang "duckfam.us/sngl/codegen/lang/c"
	"duckfam.us/sngl/ir"
)

// A canvas in C is a GtkDrawingArea rt's sngl_canvas_new makes, as gtk4rt's
// is for Go: sized to the drawing, which its draw function paints with cairo
// -- the statements passShapeDraw put in the canvas's place, cairo's own
// `#[cnative]` calls -- once rt has scaled the context to the size GTK draws
// at. The draw function is the file's, or, for a canvas a record's body
// makes, the record's, handed the record as its data:
//
//	static void s___canvasDraw0(cairo_t *ctx, void *data);
//	static void s_card___canvasDraw1(cairo_t *ctx, void *data) {
//		s_card__inst *__self = data;
//		…
//	}
//
// A redraw queues one on the area.

// canvases indexes the program's drawings by the id each canvas is made as.
func (e *cEmitter) canvases(pkg *ir.Package) {
	e.canvasByID = map[string]*codegen.Canvas{}
	e.canvasByNode = map[*ir.NodeInst]*codegen.Canvas{}
	all := codegen.Canvases(pkg)
	for i := range all {
		cv := &all[i]
		e.canvasByID[cv.ID] = cv
		e.canvasByNode[cv.Node] = cv
	}
}

// canvasOwner is the record whose canvas cv is, nil where the file holds it.
func canvasOwner(cv *codegen.Canvas) *ir.Component {
	if cv.Owner != nil && isInstanceComponent(cv.Owner) {
		return cv.Owner
	}
	return nil
}

// drawName is the function that paints cv.
func drawName(cv *codegen.Canvas) string {
	if comp := canvasOwner(cv); comp != nil {
		return recordFunc(comp, cv.Name)
	}
	return clang.Global(cv.Name)
}

// canvasCreate is the canvas id names, made: rt's area, which the native
// hook spells once it knows whose drawing it is.
func (e *cEmitter) canvasCreate(id string) []ir.Stmt {
	if e.canvasByID[id] == nil {
		return e.fail("a canvas no drawing is found for")
	}
	e.hold(id, "GtkDrawingArea *")
	t := ir.NativePointerOf("GtkDrawingArea")
	v := &ir.Var{Name: id, Type: t}
	return []ir.Stmt{&ir.Assign{
		Target: &ir.Ident{Name: id, Sym: v, Type: t},
		Op:     ast.AssignSet,
		Value:  &ir.Conversion{Type: t, Operand: rtCall("CanvasNew", ir.NativePointerOf("GtkWidget"), &ir.Literal{Type: ir.TypString, Value: id})},
	}}
}

// canvasNew is the native hook's CanvasNew: the canvas the literal names,
// sized to its drawing, painted by its draw function, with the record that
// owns it where one does.
func (e *cEmitter) canvasNew(id ir.Expr) string {
	lit, _ := id.(*ir.Literal)
	cv := e.canvasByID[lit.Value]
	w, h := cv.Width, cv.Height
	if w <= 0 {
		w = 300
	}
	if h <= 0 {
		h = 150
	}
	data := "NULL"
	if canvasOwner(cv) != nil {
		data = selfName
	}
	return fmt.Sprintf("sngl_canvas_new(%d, %d, %s, %s, %s)", w, h, strconv.Quote(cv.Scaling), drawName(cv), data)
}

// canvasRedraw queues a draw of the canvas a redraw names.
func (e *cEmitter) canvasRedraw(n *ir.CanvasRedrawStmt) []ir.Stmt {
	cv := e.canvasByNode[n.Canvas]
	if cv == nil {
		return e.fail("redrawing a canvas no drawing is found for")
	}
	area := &ir.Ident{Name: cv.ID, Type: ir.NativePointerOf("GtkDrawingArea")}
	return []ir.Stmt{&ir.CallStmt{Call: rtCall("QueueDraw", ir.TypVoid, area)}}
}

// draws writes the draw function of each canvas comp's record owns, or the
// file's where comp is nil.
func (e *cEmitter) draws(comp *ir.Component) {
	for _, cv := range codegen.Canvases(e.pkg) {
		if canvasOwner(&cv) != comp {
			continue
		}
		body := codegen.WalkLowered(context.Background(), cv.Draw, e)
		name := drawName(&cv)
		var b strings.Builder
		fmt.Fprintf(&b, "\nstatic void %s(cairo_t *ctx, void *data) {\n", name)
		if comp != nil {
			fmt.Fprintf(&b, "\t%s *%s = data;\n", recordType(comp), selfName)
		}
		// A draw GTK asks for is a turn.
		b.WriteString("\tsngl_turn_enter();\n")
		for _, l := range e.ctx.WithLocal("ctx").EvalBlock(body) {
			b.WriteString("\t" + l + "\n")
		}
		b.WriteString("\tsngl_turn_leave();\n}\n")
		e.fe.Prototype(fmt.Sprintf("static void %s(cairo_t *ctx, void *data);", name))
		fmt.Fprint(e.fe, b.String())
	}
}

// canvasClick runs handler on a press of the canvas node names, which has no
// click signal of its own, as gtk4rt's attachCanvasClick does for Go.
func (e *cEmitter) canvasClick(node, handler ir.Expr) []ir.Stmt {
	if t := handler.ExprType(); t == nil || t.Sig == nil || len(t.Sig.Params) > 0 {
		return e.fail("a canvas's @click carrying a value")
	}
	if id, ok := node.(*ir.Ident); ok {
		e.invokers[id.Name+"."+codegen.TriggerEventName(handler, "click")] = cInvoker{node: node, fire: "sngl_canvas_click"}
	}
	return []ir.Stmt{&ir.CallStmt{Call: rtCall("CanvasOnClick", ir.TypVoid, node, handler)}}
}
