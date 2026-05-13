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
// NativeName="<bare C ident>" so gc.EvalExpr (via the namespace-call
// path) renders the cgo source verbatim — the renderer prepends the
// "C." prefix.
//
// Cgo type casts of the shape (*C.X)(unsafe.Pointer(y)) are produced
// via ir.Conversion to an ir.NativePointerOf("X") type (see cgoCast);
// the Go renderer's evalConversion path emits the cgo cast pattern.
type gtk4Translator struct {
	gc           *golang.GoIRContext
	pkg          *ir.Package // optional; used to consult GIR-resolved native metadata
	fieldSink    func(name, cType string)
	idCTypes     map[string]string // id ("__n0") → GTK C type ("GtkLabel")
	topLevel     []string
	tagComponent map[string]*ir.Component // tag ("GtkButton") → resolved Component (from pre-walk)
}

func newGtk4Translator(gc *golang.GoIRContext, fieldSink func(name, cType string)) *gtk4Translator {
	return &gtk4Translator{
		gc:           gc,
		fieldSink:    fieldSink,
		idCTypes:     map[string]string{},
		tagComponent: map[string]*ir.Component{},
	}
}

// withPkg attaches a package whose imports may carry GIR-resolved
// components; the translator consults their Native metadata when a
// tag isn't matched by the static SNGL-stdlib switch.
func (t *gtk4Translator) withPkg(pkg *ir.Package) *gtk4Translator {
	t.pkg = pkg
	return t
}

// collectTagComponents pre-walks lowered body stmts and records each
// CreateNode tag's resolved ir.Component (carried on LocalVar.Type for
// component-typed nodes). Lets OnCreateNode reach the Native metadata
// without re-querying GIR.
func (t *gtk4Translator) collectTagComponents(stmts []ir.Stmt) {
	for _, s := range stmts {
		t.collectFromStmt(s)
	}
}

func (t *gtk4Translator) collectFromStmt(s ir.Stmt) {
	switch n := s.(type) {
	case *ir.LocalVar:
		if n.Type != nil && n.Type.Kind == ir.TypeComponent {
			if comp, ok := n.Type.Decl.(*ir.Component); ok && comp != nil {
				if call, ok := n.Init.(*ir.Call); ok && len(call.Args) >= 1 {
					if lit, ok := call.Args[0].Value.(*ir.Literal); ok && lit.Type == ir.TypString {
						t.tagComponent[lit.Raw] = comp
					}
				}
			}
		}
	case *ir.For:
		for _, c := range n.Body {
			t.collectFromStmt(c)
		}
		for _, c := range n.Else {
			t.collectFromStmt(c)
		}
	case *ir.If:
		for _, c := range n.Body {
			t.collectFromStmt(c)
		}
		for _, c := range n.Else {
			t.collectFromStmt(c)
		}
	}
}

// lookupNativeByTag finds an ir.Component by name (tag) using the
// pre-collected tag→Component map. Returns the gtk4 metadata when
// present.
func (t *gtk4Translator) lookupNativeByTag(tag string) (*ir.Component, *gtk4NativeComponent) {
	c, ok := t.tagComponent[tag]
	if !ok || c == nil {
		return nil, nil
	}
	if nm, ok := c.Native.(*gtk4NativeComponent); ok {
		return c, nm
	}
	return c, nil
}

// lookupNativeByCType finds an ir.Component whose native CType matches.
func (t *gtk4Translator) lookupNativeByCType(cType string) (*ir.Component, *gtk4NativeComponent) {
	for _, c := range t.tagComponent {
		if nm, ok := c.Native.(*gtk4NativeComponent); ok && nm.CType == cType {
			return c, nm
		}
	}
	return nil, nil
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

// nativeFunc constructs an *ir.Func with NativePkg="C" /
// NativeName=<bare-C-identifier> so gc.EvalExpr's namespace-call branch
// emits the cgo source `C.<NativeName>` (the renderer adds the "C."
// prefix). Callers pass bare names like "gtk_label_new" — never
// pre-prefix with "C." (the renderer would still strip-and-add it via
// the backwards-compat HasPrefix check, but new code should be clean).
func nativeFunc(nativeName string) *ir.Func {
	return &ir.Func{NativePkg: "C", NativeName: nativeName, Name: nativeName}
}

// nativeCall builds a single-level cgo C-API call. The receiver
// (`C`) drives the renderer into evalNamespaceCall, which emits
// `C.<NativeName>(args)`.
func nativeCall(nativeName string, args ...ir.Expr) *ir.Call {
	callArgs := make([]ir.CallArg, len(args))
	for i, a := range args {
		callArgs[i] = ir.CallArg{Value: a}
	}
	return &ir.Call{
		Type:     ir.TypDyn,
		Receiver: &ir.Ident{Name: "C"},
		Func:     nativeFunc(nativeName),
		Args:     callArgs,
	}
}

// cgoCast wraps an expression in a cgo pointer cast:
//
//	(*C.<typeName>)(unsafe.Pointer(expr))
//
// Implemented as an ir.Conversion to a NativePointer type; the Go
// renderer (lang/golang) recognises the shape and emits the cgo
// cast pattern via evalConversion.
func cgoCast(typeName string, expr ir.Expr) ir.Expr {
	return &ir.Conversion{
		Type:    ir.NativePointerOf(typeName),
		Operand: expr,
	}
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
		return nativeCall("gtk_label_new", nullLit)
	case "button":
		emptyCStr := nativeCall("CString", &ir.Literal{Type: ir.TypString, Raw: ""})
		return nativeCall("gtk_button_new_with_label", emptyCStr)
	case "input", "entry":
		return nativeCall("gtk_entry_new")
	case "vbox":
		orient := &ir.Ident{Name: "C.GTK_ORIENTATION_VERTICAL", Type: ir.TypDyn}
		spacing := &ir.Literal{Type: ir.TypInt, Raw: "6"}
		return nativeCall("gtk_box_new", orient, spacing)
	case "hbox":
		orient := &ir.Ident{Name: "C.GTK_ORIENTATION_HORIZONTAL", Type: ir.TypDyn}
		spacing := &ir.Literal{Type: ir.TypInt, Raw: "6"}
		return nativeCall("gtk_box_new", orient, spacing)
	case "checkbox":
		return nativeCall("gtk_check_button_new")
	case "scroll":
		return nativeCall("gtk_scrolled_window_new")
	}
	return nil
}

func (t *gtk4Translator) OnCreateNode(ctx context.Context, id, tag string) []ir.Stmt {
	// 1. SNGL stdlib tag fast path.
	if cType := gtk4TagToCType(tag); cType != "" {
		ctor := gtk4Constructor(tag)
		if ctor == nil {
			return nil
		}
		return t.emitConstructorAssign(id, cType, ctor)
	}
	// 2. GIR-resolved native component (e.g. "GtkButton" used directly).
	if _, nm := t.lookupNativeByTag(tag); nm != nil {
		// Constructors with required non-null pointer args (e.g.
		// gtk_button_new_with_label takes a const gchar*) accept NULL
		// for nullable params; pass nil for everything to keep this
		// generic. Constructors that strictly require non-null args
		// will need a follow-up to pick a no-arg variant.
		ctor := nativeCall(nm.Constructor)
		// gtk_application_window_new requires the GtkApplication;
		// special-case so it gets the `app` parameter passed into
		// BuildUI rather than nil.
		if nm.Constructor == "gtk_application_window_new" {
			ctor = nativeCall("gtk_application_window_new", &ir.Ident{Name: "app", Type: ir.TypDyn})
		}
		return t.emitConstructorAssign(id, nm.CType, ctor)
	}
	return nil
}

// emitConstructorAssign records the new widget's id↔cType mapping and
// emits `m.<id> = (*C.<cType>)(unsafe.Pointer(ctor))`.
func (t *gtk4Translator) emitConstructorAssign(id, cType string, ctor ir.Expr) []ir.Stmt {
	t.fieldSink(id, cType)
	t.idCTypes[id] = cType
	t.topLevel = append(t.topLevel, id)
	return []ir.Stmt{&ir.Assign{
		Target: modelFieldRef(id),
		Op:     ast.AssignSet,
		Value:  cgoCast(cType, ctor),
	}}
}

// gtk4ChildAppendFn returns the bare C function name for adding a
// child to a container of the given C type. Callers pass the result
// to nativeCall, which prepends the "C." prefix at render time.
func gtk4ChildAppendFn(parentCType string) string {
	switch parentCType {
	case "GtkBox":
		return "gtk_box_append"
	case "GtkScrolledWindow":
		return "gtk_scrolled_window_set_child"
	case "GtkWindow", "GtkApplicationWindow":
		return "gtk_window_set_child"
	}
	return ""
}

func gtk4ChildRemoveFn(parentCType string) string {
	switch parentCType {
	case "GtkBox":
		return "gtk_box_remove"
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
		if strings.HasPrefix(id.Name, "__n") {
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
		// GIR-resolved native metadata fallback.
		if comp, _ := t.lookupNativeByCType(cType); comp != nil {
			for _, p := range comp.Props {
				if p.Name == prop && p.NativeSetter != "" {
					setter = p.NativeSetter
					break
				}
			}
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
		valArg = nativeCall("gboolean", value)
	default:
		valArg = nativeCall("CString", value)
	}
	cast := cgoCast(cType, t.qualifyNodeExpr(node))
	return []ir.Stmt{&ir.CallStmt{Call: nativeCall(setter, cast, valArg)}}
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
		// GIR-resolved native metadata fallback.
		if comp, _ := t.lookupNativeByCType(cType); comp != nil {
			for _, e := range comp.Events {
				if e.Name == event && e.NativeSignal != "" {
					signal = e.NativeSignal
					break
				}
			}
		}
	}
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
	// C signature is `void *widget`, so cgo expects unsafe.Pointer.
	widget := cgoCast("", t.qualifyNodeExpr(node))
	signalCStr := nativeCall("CString", &ir.Literal{Type: ir.TypString, Raw: signal})
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
	idxArg := nativeCall("int", idxExpr)
	connectStmt := &ir.CallStmt{
		Call: nativeCall("sngl_connect", widget, signalCStr, idxArg),
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
