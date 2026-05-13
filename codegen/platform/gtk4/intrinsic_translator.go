package gtk4

import (
	"context"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// gtk4Translator implements codegen.IntrinsicTranslator for gtk4.
// Emits IR fragments whose Call.Func values carry NativePkg="C" /
// NativeName="C.gtk_*" so gc.EvalExpr (via the namespace-call path)
// renders the cgo source verbatim.
//
// NativeName-cast shorthand: where the Plan C IR needs a Go type cast
// like (*C.GtkLabel)(p), we synthesize an ir.Call whose Func.NativeName
// is literally "(*C.GtkLabel)". The Go renderer's namespace-call branch
// just emits NativeName + "(" + args + ")" — which is exactly the cast
// syntax we need. This is a documented abuse of NativeName; a follow-up
// plan should introduce a dedicated ir.Cast node.
type gtk4Translator struct {
	gc        *golang.GoIRContext
	fieldSink func(name, cType string)
	idCTypes  map[string]string // id ("__n0") → GTK C type ("GtkLabel")
	topLevel  []string
}

func newGtk4Translator(gc *golang.GoIRContext, fieldSink func(name, cType string)) *gtk4Translator {
	return &gtk4Translator{gc: gc, fieldSink: fieldSink, idCTypes: map[string]string{}}
}

var _ codegen.IntrinsicTranslator = (*gtk4Translator)(nil)

// modelFieldRef returns an ir.Expr that gc.EvalExpr/evalMutTarget
// render as `m.<name>` — a reference to a Model struct field. Widget
// fields aren't registered in the type-checker scope, so we synthesize
// the `m.` qualifier rather than relying on Resolve.
func modelFieldRef(name string) ir.Expr {
	return &ir.Select{
		Operand: &ir.Ident{Name: "m"},
		Field:   name,
		Type:    ir.TypDyn,
	}
}

// nativeFunc constructs an *ir.Func with NativePkg/NativeName set so
// gc.EvalExpr renders it as a fully-qualified Go expression via the
// namespace-call branch (evalNamespaceCall).
func nativeFunc(nativeName string) *ir.Func {
	pkg := nativeName
	if i := strings.Index(nativeName, "."); i > 0 {
		pkg = nativeName[:i]
	}
	return &ir.Func{NativePkg: pkg, NativeName: nativeName, Name: nativeName}
}

// nativeCall builds a single-level native call. Sets Receiver so the
// Go renderer enters evalNamespaceCall and emits NativeName(args).
func nativeCall(nativeName string, args ...ir.Expr) *ir.Call {
	callArgs := make([]ir.CallArg, len(args))
	for i, a := range args {
		callArgs[i] = ir.CallArg{Value: a}
	}
	pkg := nativeName
	if i := strings.Index(nativeName, "."); i > 0 {
		pkg = nativeName[:i]
	}
	return &ir.Call{
		Type:     ir.TypDyn,
		Receiver: &ir.Ident{Name: pkg},
		Func:     nativeFunc(nativeName),
		Args:     callArgs,
	}
}

// cgoCast wraps an expression in the cgo cast pattern:
//
//	(*C.<typeName>)(unsafe.Pointer(expr))
func cgoCast(typeName string, expr ir.Expr) ir.Expr {
	unsafePtr := nativeCall("unsafe.Pointer", expr)
	return nativeCall("(*C."+typeName+")", unsafePtr)
}

// gtk4TagToCType maps a SNGL stdlib tag to its GTK C type.
func gtk4TagToCType(tag string) string {
	switch tag {
	case "text", "label":
		return "GtkLabel"
	case "button":
		return "GtkButton"
	case "input", "entry":
		return "GtkEntry"
	case "vbox":
		return "GtkBox"
	case "hbox":
		return "GtkBox"
	case "checkbox":
		return "GtkCheckButton"
	case "scroll":
		return "GtkScrolledWindow"
	}
	return ""
}

// gtk4Constructor returns the cgo Call ir.Expr for a tag's
// constructor. Returns nil for unsupported tags.
func gtk4Constructor(tag string) *ir.Call {
	nullLit := &ir.Literal{Type: ir.TypNull}
	switch tag {
	case "text", "label":
		return nativeCall("C.gtk_label_new", nullLit)
	case "button":
		emptyCStr := nativeCall("C.CString", &ir.Literal{Type: ir.TypString, Raw: ""})
		return nativeCall("C.gtk_button_new_with_label", emptyCStr)
	case "input", "entry":
		return nativeCall("C.gtk_entry_new")
	case "vbox":
		orient := &ir.Ident{Name: "C.GTK_ORIENTATION_VERTICAL", Type: ir.TypDyn}
		spacing := &ir.Literal{Type: ir.TypInt, Raw: "6"}
		return nativeCall("C.gtk_box_new", orient, spacing)
	case "hbox":
		orient := &ir.Ident{Name: "C.GTK_ORIENTATION_HORIZONTAL", Type: ir.TypDyn}
		spacing := &ir.Literal{Type: ir.TypInt, Raw: "6"}
		return nativeCall("C.gtk_box_new", orient, spacing)
	case "checkbox":
		return nativeCall("C.gtk_check_button_new")
	case "scroll":
		return nativeCall("C.gtk_scrolled_window_new")
	}
	return nil
}

func (t *gtk4Translator) OnCreateNode(ctx context.Context, id, tag string) []ir.Stmt {
	cType := gtk4TagToCType(tag)
	if cType == "" {
		return nil
	}
	ctor := gtk4Constructor(tag)
	if ctor == nil {
		return nil
	}
	t.fieldSink(id, cType)
	t.idCTypes[id] = cType
	t.topLevel = append(t.topLevel, id)

	// m.<id> = (*C.<cType>)(unsafe.Pointer(ctor))
	return []ir.Stmt{&ir.Assign{
		Target: modelFieldRef(id),
		Op:     ast.AssignSet,
		Value:  cgoCast(cType, ctor),
	}}
}

// gtk4ChildAppendFn returns the C function name (with C. prefix) for
// adding a child to a container of the given C type.
func gtk4ChildAppendFn(parentCType string) string {
	switch parentCType {
	case "GtkBox":
		return "C.gtk_box_append"
	case "GtkScrolledWindow":
		return "C.gtk_scrolled_window_set_child"
	case "GtkWindow", "GtkApplicationWindow":
		return "C.gtk_window_set_child"
	}
	return ""
}

func gtk4ChildRemoveFn(parentCType string) string {
	switch parentCType {
	case "GtkBox":
		return "C.gtk_box_remove"
	}
	return ""
}

// parentCType extracts the C type from a node Ident.
func (t *gtk4Translator) parentCType(e ir.Expr) string {
	if id, ok := e.(*ir.Ident); ok {
		if id.Name == "container" || id.Name == "parent" {
			return "GtkBox"
		}
		return t.idCTypes[identBareName(e)]
	}
	return ""
}

func (t *gtk4Translator) OnAppendChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	cType := t.parentCType(parent)
	if cType == "" {
		cType = "GtkBox"
	}
	fn := gtk4ChildAppendFn(cType)
	if fn == "" {
		return nil
	}
	// Remove appended child from topLevel.
	if id, ok := child.(*ir.Ident); ok && id.Synthesized {
		for i, name := range t.topLevel {
			if name == id.Name {
				t.topLevel = append(t.topLevel[:i], t.topLevel[i+1:]...)
				break
			}
		}
	}
	parentArg := cgoCast(cType, t.qualifyNodeExpr(parent))
	childArg := cgoCast("GtkWidget", t.qualifyNodeExpr(child))
	return []ir.Stmt{&ir.CallStmt{Call: nativeCall(fn, parentArg, childArg)}}
}

func (t *gtk4Translator) OnRemoveChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	cType := t.parentCType(parent)
	if cType == "" {
		cType = "GtkBox"
	}
	fn := gtk4ChildRemoveFn(cType)
	if fn == "" {
		return nil
	}
	parentArg := cgoCast(cType, t.qualifyNodeExpr(parent))
	childArg := cgoCast("GtkWidget", t.qualifyNodeExpr(child))
	return []ir.Stmt{&ir.CallStmt{Call: nativeCall(fn, parentArg, childArg)}}
}

// qualifyNodeExpr re-qualifies a synthesized widget ident ("__n0") as a
// Model field reference. Non-synth idents (e.g. slot-function "container"
// or "parent") pass through.
func (t *gtk4Translator) qualifyNodeExpr(e ir.Expr) ir.Expr {
	if id, ok := e.(*ir.Ident); ok {
		if id.Synthesized && strings.HasPrefix(id.Name, "__n") {
			return modelFieldRef(id.Name)
		}
		if id.IsElementRef && id.Synthesized {
			return modelFieldRef(id.Name)
		}
	}
	return e
}

func (t *gtk4Translator) OnPropAssign(ctx context.Context, node ir.Expr, prop string, value ir.Expr) []ir.Stmt {
	bare := identBareName(node)
	cType, ok := t.idCTypes[bare]
	if !ok {
		return nil
	}
	setter := gtkSetter(cType, prop)
	if setter == "" {
		// Try the canonical stdlib-mapped name: most "value"/"text" props
		// flow into the underlying GTK "label" setter on labels & buttons.
		switch prop {
		case "value", "text":
			setter = gtkSetter(cType, "label")
		}
	}
	if setter == "" {
		return nil
	}
	// Most GTK setters take char*; wrap value in C.CString. Boolean-only
	// setters (e.g. gtk_check_button_set_active) need C.gboolean — handle
	// those via a small switch on setter name.
	var valArg ir.Expr
	switch setter {
	case "gtk_check_button_set_active":
		valArg = nativeCall("C.gboolean", value)
	default:
		valArg = nativeCall("C.CString", value)
	}
	cast := cgoCast(cType, t.qualifyNodeExpr(node))
	return []ir.Stmt{&ir.CallStmt{Call: nativeCall("C."+setter, cast, valArg)}}
}

// identBareName returns the unqualified name of an Ident, stripping any
// "m." prefix that came pre-qualified.
func identBareName(e ir.Expr) string {
	if id, ok := e.(*ir.Ident); ok {
		return strings.TrimPrefix(id.Name, "m.")
	}
	return ""
}

// gtk4SignalFor maps a SNGL event name on a given C type to the
// corresponding GTK signal name (no "g_signal_connect_" prefix).
func gtk4SignalFor(cType, event string) string {
	switch cType {
	case "GtkButton":
		if event == "click" {
			return "clicked"
		}
	case "GtkEntry":
		if event == "input" || event == "change" {
			return "changed"
		}
	case "GtkCheckButton":
		if event == "change" {
			return "toggled"
		}
	}
	return ""
}

func (t *gtk4Translator) OnAttachHandler(ctx context.Context, node ir.Expr, event string, handler ir.Expr) []ir.Stmt {
	bare := identBareName(node)
	cType := t.idCTypes[bare]
	signal := gtk4SignalFor(cType, event)
	if signal == "" {
		return nil
	}
	// snglCallbacks = append(snglCallbacks, handler)
	cbList := &ir.Ident{Name: "snglCallbacks", Type: ir.TypDyn}
	appendCall := &ir.Call{
		Type: ir.TypDyn,
		Func: &ir.Func{Name: "append"},
		Args: []ir.CallArg{
			{Value: cbList},
			{Value: handler},
		},
	}
	registerStmt := &ir.Assign{
		Target: cbList,
		Op:     ast.AssignSet,
		Value:  appendCall,
	}
	// C.sngl_connect(widget, "<signal>", C.int(len(snglCallbacks)-1))
	widget := cgoCast("GtkWidget", t.qualifyNodeExpr(node))
	signalCStr := nativeCall("C.CString", &ir.Literal{Type: ir.TypString, Raw: signal})
	lenCall := &ir.Call{
		Type: ir.TypInt,
		Func: &ir.Func{Name: "len"},
		Args: []ir.CallArg{{Value: cbList}},
	}
	idxExpr := &ir.Binary{
		Op:    ast.BinSub,
		Left:  lenCall,
		Right: &ir.Literal{Type: ir.TypInt, Raw: "1"},
	}
	idxArg := nativeCall("C.int", idxExpr)
	connectStmt := &ir.CallStmt{
		Call: nativeCall("C.sngl_connect", widget, signalCStr, idxArg),
	}
	return []ir.Stmt{registerStmt, connectStmt}
}

func (t *gtk4Translator) OnSlotReset(ctx context.Context, slot *ir.Var) []ir.Stmt {
	return []ir.Stmt{&ir.Assign{
		Target: modelFieldRef(slot.Name),
		Op:     ast.AssignSet,
		Value:  &ir.Literal{Type: ir.TypNull},
	}}
}

func (t *gtk4Translator) OnSlotAppend(ctx context.Context, slot *ir.Var, child ir.Expr) []ir.Stmt {
	slotRef := modelFieldRef(slot.Name)
	castChild := cgoCast("GtkWidget", t.qualifyNodeExpr(child))
	appendCall := &ir.Call{
		Type: slot.Type,
		Func: &ir.Func{Name: "append"},
		Args: []ir.CallArg{
			{Value: slotRef},
			{Value: castChild},
		},
	}
	return []ir.Stmt{&ir.Assign{
		Target: modelFieldRef(slot.Name),
		Op:     ast.AssignSet,
		Value:  appendCall,
	}}
}

func (t *gtk4Translator) OnIter(ctx context.Context, iter ir.Expr) ir.Expr {
	if id, ok := iter.(*ir.Ident); ok && id.Synthesized {
		return modelFieldRef(id.Name)
	}
	return iter
}

func (t *gtk4Translator) OnCond(ctx context.Context, cond ir.Expr) ir.Expr {
	return cond
}

func (t *gtk4Translator) OnDefault(ctx context.Context, stmt ir.Stmt) []ir.Stmt {
	return []ir.Stmt{stmt}
}
