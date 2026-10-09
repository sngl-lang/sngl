package gtk4

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// gtk4.sngl's image primitives, gtk4rt's rather than GIR classes, written here
// as Toplevel's are. An Image is a box holding what it shows: the source, a
// GtkPicture showing the texture its bytes or its drawing make, or the
// placeholder. gtk4rt fits a source to the box it is appended to and reports
// to it whether the picture arrived; appended to a Toplevel, a source is the
// window's icon.
const (
	imageTag        = "Image"
	imageURLTag     = "ImageUrl"
	imageDrawingTag = "ImageDrawing"
)

func imagePrimitive(tag string) bool {
	return tag == imageTag || tag == imageURLTag || tag == imageDrawingTag
}

// primitiveCType is the C type a hand-written primitive's handle is, which
// says how a child is appended to it.
func primitiveCType(tag string) string {
	switch tag {
	case toplevelTag, imageTag:
		return "GtkBox"
	case imageURLTag, imageDrawingTag:
		return "GtkPicture"
	}
	return ""
}

func (t *gtk4Translator) imageTagOf(bare string) string {
	if t.shared == nil {
		return ""
	}
	return t.shared.images[bare]
}

func (t *gtk4Translator) emitImageCreate(id, tag string) []ir.Stmt {
	if tag == imageTag {
		return t.emitConstructorAssign(id, primitiveCType(tag), rtCall("ImageFrameNew"))
	}
	if tag == imageDrawingTag {
		// A repaint reaches the source from whatever scope writes what it
		// reads, which the escape analysis cannot see.
		t.fieldIDs[id] = true
	}
	create := t.emitConstructorAssign(id, primitiveCType(tag), rtCall("ImageSourceNew"))
	if tag == imageDrawingTag {
		if m := t.canvasMetaForID(id); m != nil {
			create = append(create, t.paintImageDrawing(m))
		}
	}
	return create
}

// paintImageDrawing paints a drawn source's picture afresh and hands it over.
func (t *gtk4Translator) paintImageDrawing(m *canvasMeta) ir.Stmt {
	draw := &ir.Ident{
		Name: fmt.Sprintf("func(cr unsafe.Pointer) { %s.%s((*C.cairo_t)(cr)) }", t.gc.RecvName(), m.DrawName),
		Type: ir.TypDyn,
	}
	return &ir.CallStmt{Call: rtCall("ImagePaint", t.rtHandle(t.fieldRef(m.ID)), intLit(fmt.Sprint(m.Width)), intLit(fmt.Sprint(m.Height)), draw)}
}

func (t *gtk4Translator) imagePropAssign(node ir.Expr, tag, prop string, value ir.Expr) []ir.Stmt {
	ref := t.rtHandle(t.qualifyNodeExpr(node))
	switch {
	case tag == imageTag && prop == "fit":
		return []ir.Stmt{&ir.CallStmt{Call: rtCall("ImageFrameSetFit", ref, value)}}
	case tag == imageURLTag && prop == ir.ImageSourceURL:
		return []ir.Stmt{&ir.CallStmt{Call: rtCall("ImageSourceSetURL", ref, value)}}
	}
	return nil
}

func (t *gtk4Translator) imageAttachHandler(node ir.Expr, tag, event string, handler ir.Expr) []ir.Stmt {
	if tag != imageTag {
		return nil
	}
	fn := ""
	switch event {
	case "click":
		fn = "ImageFrameOnClick"
	case "load":
		fn = "ImageFrameOnLoad"
	case "failed":
		fn = "ImageFrameOnError"
	default:
		return nil
	}
	return []ir.Stmt{&ir.CallStmt{Call: rtCall(fn, t.rtHandle(t.qualifyNodeExpr(node)), handler)}}
}

// rtHandle is a widget as gtk4rt takes it, which in an inline-cgo build is a
// pointer of this file's own C type.
func (t *gtk4Translator) rtHandle(e ir.Expr) ir.Expr {
	if t.wrapped {
		return e
	}
	return &ir.Conversion{Type: gtk4rtHandleIRType(), Operand: cgoCast("", e)}
}

// windowIcon records a source appended to a window in an inline-cgo build,
// which has no gtk4rt window to take it: BuildUI hands it to the window it
// makes.
func (t *gtk4Translator) windowIcon(parent, child ir.Expr) bool {
	if t.wrapped || !t.isToplevel(codegen.IdentBareName(parent)) {
		return false
	}
	switch t.imageTagOf(codegen.IdentBareName(child)) {
	case imageURLTag, imageDrawingTag:
		t.shared.toplevelIcons[codegen.IdentBareName(parent)] = codegen.IdentBareName(child)
		return true
	}
	return false
}
