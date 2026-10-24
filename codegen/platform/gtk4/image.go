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

func (t *gtk4Translator) emitImageCreate(id, tag string, made ir.Expr) []ir.Stmt {
	if made == nil {
		panic(fmt.Sprintf("gtk4: %s reached the translator without the create gtk4.sngl declares", tag))
	}
	if tag == imageDrawingTag {
		// A repaint reaches the source from whatever scope writes what it
		// reads, which the escape analysis cannot see.
		t.fieldIDs[id] = true
	}
	create := t.emitConstructorAssign(id, primitiveCType(tag), made)
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

// imagePropAssign is a write to an image primitive that no declared set
// answers: a source's URL, which is what it says it shows rather than what it
// shows, and a drawing's size, which the paint read off the drawing.
func (t *gtk4Translator) imagePropAssign(tag, prop string) []ir.Stmt {
	switch {
	case tag == imageURLTag && prop == ir.ImageSourceURL:
	case tag == imageDrawingTag:
	default:
		t.shared.fail(fmt.Errorf("gtk4: %s.%s is written, and gtk4.sngl declares no #[gen.protocol(set)] for it", tag, prop))
	}
	return nil
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

func (t *gtk4Translator) emitScrollCreate(id string, made ir.Expr) []ir.Stmt {
	if made == nil {
		panic("gtk4: a Scroll reached the translator without the create gtk4.sngl declares")
	}
	return t.emitConstructorAssign(id, primitiveCType(scrollTag), made)
}

// recordScrollInvoker records the test invoker that fires a Scroll's
// handler, which no key reaches.
func (t *gtk4Translator) recordScrollInvoker(node ir.Expr, event string, handler ir.Expr) {
	if id := codegen.IdentBareName(node); t.invokerSink != nil && !strings.HasPrefix(id, "__n") {
		t.invokerSink(gtkEventInvoker{IDLabel: id, SnglEvent: codegen.TriggerEventName(handler, event), FieldName: id, WidgetType: "GtkScrolledWindow", RtFire: "ScrollReachEnd"})
	}
}

// connectThrough is a handler on a tag no GIR class is behind, connected by
// the #[gen.protocol(on)] its component declares for the event.
func (t *gtk4Translator) connectThrough(bare, event string, handler, call ir.Expr) []ir.Stmt {
	if call == nil {
		panic(fmt.Sprintf("gtk4: @%s on %s reached the translator without the connect gtk4.sngl declares", event, bare))
	}
	return []ir.Stmt{&ir.CallStmt{Call: t.connectCall(call, handler)}}
}
