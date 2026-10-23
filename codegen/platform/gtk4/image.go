package gtk4

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
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

// scrollTag is gtk4.sngl's `Scroll`, gtk4rt's GtkScrolledWindow that reports
// its content's end coming into view: GTK has no signal for that, and the
// adjustment's that answer it hand their handler nothing an event could
// carry.
const scrollTag = "Scroll"

// imagePictureProp is `ImageUrl`'s picture, the owned value its source derives
// from the URL (gtk4.sngl's `Picture`).
const imagePictureProp = "picture"

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
	case scrollTag:
		return "GtkScrolledWindow"
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
		return t.emitConstructorAssign(id, primitiveCType(tag), rtCall("ImageFrameNew", gtk4rtHandleIRType()))
	}
	if tag == imageDrawingTag {
		// A repaint reaches the source from whatever scope writes what it
		// reads, which the escape analysis cannot see.
		t.fieldIDs[id] = true
	}
	create := t.emitConstructorAssign(id, primitiveCType(tag), rtCall("ImageSourceNew", gtk4rtHandleIRType()))
	if tag == imageDrawingTag {
		if m := t.canvasMetaForID(id); m != nil {
			create = append(create, t.paintImageDrawing(m))
		}
	}
	return create
}

// paintImageDrawing paints a drawn source's picture afresh and hands it over.
func (t *gtk4Translator) paintImageDrawing(m *canvasMeta) ir.Stmt {
	cr := &ir.Param{Name: "cr", Type: ir.NativeGoNamed("unsafe.Pointer")}
	crRef := &ir.Ident{Name: cr.Name, Type: cr.Type, Sym: cr}
	draw := goClosure([]*ir.Param{cr}, &ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: t.gc.RecvRef(),
		Func:     &ir.Func{Name: m.DrawName},
		Args:     []ir.CallArg{{Value: cgoCast("cairo_t", crRef)}},
	}})
	return &ir.CallStmt{Call: rtCall("ImagePaint", ir.TypVoid, t.rtHandle(t.canvasFieldRef(m)), intLit(fmt.Sprint(m.Width)), intLit(fmt.Sprint(m.Height)), draw)}
}

func (t *gtk4Translator) imagePropAssign(node ir.Expr, tag, prop string, value ir.Expr) []ir.Stmt {
	ref := t.rtHandle(t.qualifyNodeExpr(node))
	switch {
	case tag == imageTag && prop == "fit":
		return []ir.Stmt{&ir.CallStmt{Call: rtCall("ImageFrameSetFit", ir.TypVoid, ref, value)}}
	case tag == imageURLTag && prop == ir.ImageSourceURL:
		// The URL is what the source says it shows; the picture is what it
		// shows.
		return nil
	case tag == imageURLTag && prop == imagePictureProp:
		return []ir.Stmt{&ir.CallStmt{Call: rtCall("ImageSourceSetPicture", ir.TypVoid, ref, value)}}
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
	return []ir.Stmt{&ir.CallStmt{Call: rtCall(fn, ir.TypVoid, t.rtHandle(t.qualifyNodeExpr(node)), handler)}}
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

// isScroll reports whether bare names a Scroll primitive.
func (t *gtk4Translator) isScroll(bare string) bool {
	return t.shared != nil && t.shared.scrolls[bare]
}

func (t *gtk4Translator) emitScrollCreate(id string) []ir.Stmt {
	return t.emitConstructorAssign(id, primitiveCType(scrollTag), rtCall("ScrollNew", gtk4rtHandleIRType()))
}

func (t *gtk4Translator) scrollAttachHandler(node ir.Expr, event string, handler ir.Expr) []ir.Stmt {
	if event != "reachEnd" {
		return nil
	}
	if id := codegen.IdentBareName(node); t.invokerSink != nil && !strings.HasPrefix(id, "__n") {
		t.invokerSink(gtkEventInvoker{IDLabel: id, SnglEvent: codegen.TriggerEventName(handler, event), FieldName: id, WidgetType: "GtkScrolledWindow", RtFire: "ScrollReachEnd"})
	}
	return []ir.Stmt{&ir.CallStmt{Call: rtCall("ScrollOnReachEnd", ir.TypVoid, t.rtHandle(t.qualifyNodeExpr(node)), handler)}}
}
