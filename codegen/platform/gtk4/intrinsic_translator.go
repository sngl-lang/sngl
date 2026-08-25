package gtk4

import (
	"context"
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// gtk4Translator implements codegen.IntrinsicTranslator for gtk4.
// Emits IR fragments whose Call.Func values carry Foreign.Pkg="C" /
// Foreign.Name="<bare C ident>" so gc.EvalExpr (via the namespace-call
// path) renders the cgo source verbatim — the renderer prepends the
// "C." prefix.
//
// Cgo type casts of the shape (*C.X)(unsafe.Pointer(y)) are produced
// via ir.Conversion to an ir.NativePointerOf("X") type (see cgoCast);
// the Go renderer's evalConversion path emits the cgo cast pattern.
type gtk4Translator struct {
	gc        *golang.GoIRContext
	pkg       *ir.Package // optional; used to consult GIR-resolved native metadata
	fieldSink func(name, cType string)
	// localRefs is the set of synthesized widget ref ids that the lower
	// pass passNodeEscape determined do NOT escape this scope. For these
	// ids OnCreateNode/OnCreateComponent emit a function-local `__nN := ...`
	// declaration rather than a shared Model field, and qualifyNodeExpr
	// renders the bare local name rather than `m.__nN`. Escaping ids (not
	// in this set) keep the Model-field behavior. nil → every id is a field.
	localRefs map[string]bool
	idCTypes  map[string]string   // id ("__n0") → GTK C type ("GtkLabel")
	skipped   map[string]struct{} // ids whose OnCreateNode emitted nothing (unresolved tag) — later refs to them must be skipped too
	topLevel  []string
	// wrapped selects wrapped-mode emission: widget ops become pkg/go/gtk4rt
	// calls over gtk4rt.Handle instead of inline cgo. Unmapped ops fall through
	// to the cgo emission (leaving a `C.` that triggers the whole-program
	// fallback in emitIR). See wrapped.go.
	wrapped       bool
	tagComponent  map[string]*ir.Component // tag ("GtkButton") → resolved Component (from pre-walk)
	boolToIntUsed *bool                    // points to compilation.needsBoolToInt; set when boolToGoInt is called

	// Canvas2D state. canvasByID/canvasByFunc map flattened canvas elements
	// (LocalVar.CanvasDraw) to their GtkDrawingArea Model field + draw func,
	// shared into every translator that may create a canvas or emit a redraw.
	// pendingCanvasStyle holds the CanvasStyle local bound by a
	// CanvasApplyStyle while translating the following draw primitive;
	// canvasStyleCounter names the per-shape `_styleN` temporaries.
	canvasByID         map[string]*canvasMeta
	canvasByFunc       map[*ir.Func]*canvasMeta
	pendingCanvasStyle ir.Expr
	canvasStyleCounter int
}

func newGtk4Translator(gc *golang.GoIRContext, fieldSink func(name, cType string)) *gtk4Translator {
	return &gtk4Translator{
		gc:           gc,
		fieldSink:    fieldSink,
		idCTypes:     map[string]string{},
		skipped:      map[string]struct{}{},
		tagComponent: map[string]*ir.Component{},
	}
}

// withLocalRefs sets the non-escaping ref-id set for the scope this
// translator emits. See gtk4Translator.localRefs.
func (t *gtk4Translator) withLocalRefs(local map[string]bool) *gtk4Translator {
	t.localRefs = local
	return t
}

// isLocalRef reports whether id is a non-escaping ref that should be emitted
// as a function-local variable rather than a Model field.
func (t *gtk4Translator) isLocalRef(id string) bool {
	return t.localRefs != nil && t.localRefs[id]
}

// withPkg attaches a package whose imports may carry GIR-resolved
// components; the translator consults their Native metadata when a
// tag isn't matched by the static SNGL-stdlib switch.
func (t *gtk4Translator) withPkg(pkg *ir.Package) *gtk4Translator {
	t.pkg = pkg
	return t
}

// withBoolToIntFlag points the translator at a flag that gets set when
// boolToGoInt is called, so the caller knows to emit the boolToInt helper.
func (t *gtk4Translator) withBoolToIntFlag(flag *bool) *gtk4Translator {
	t.boolToIntUsed = flag
	return t
}

// withWrapped selects wrapped-mode emission (pkg/go/gtk4rt calls) when on.
func (t *gtk4Translator) withWrapped(on bool) *gtk4Translator {
	t.wrapped = on
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
	case *ir.PlatformFilter:
		for _, c := range n.Body {
			t.collectFromStmt(c)
		}
	case *ir.ErrorBoundary:
		for _, c := range n.Children {
			t.collectFromStmt(c)
		}
	case *ir.NodeInst:
		for _, c := range n.Children {
			t.collectFromStmt(c)
		}
	case *ir.Window:
		for _, c := range n.Body {
			t.collectFromStmt(c)
		}
	case *ir.SlotInst, *ir.Assign, *ir.CallStmt, *ir.Return, *ir.Emit, *ir.Toggle, *ir.CanvasRedrawStmt:
		// No component-typed LocalVar to harvest.
	case *ir.ContextProvider:
		panic(fmt.Sprintf("gtk4.collectFromStmt: ContextProvider should be lowered: %#v", n))
	default:
		panic(fmt.Sprintf("gtk4.collectFromStmt: unhandled ir.Stmt %T", n))
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

// nativeFunc constructs an *ir.Func with Foreign.Pkg="C" /
// Foreign.Name=<bare-C-identifier> so gc.EvalExpr's namespace-call branch
// emits the cgo source `C.<Foreign.Name>` (the renderer adds the "C."
// prefix). Callers pass bare names like "gtk_label_new" — never
// pre-prefix with "C." (the renderer would still strip-and-add it via
// the backwards-compat HasPrefix check, but new code should be clean).
func nativeFunc(nativeName string) *ir.Func {
	return &ir.Func{Foreign: ir.Foreign{Pkg: "C", Name: nativeName}, Name: nativeName}
}

// nativeCall builds a single-level cgo C-API call. The receiver
// (`C`) drives the renderer into evalNamespaceCall, which emits
// `C.<Foreign.Name>(args)`.
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

// boolToGoInt wraps a Go bool expression in `boolToInt(expr)` so the result
// is a Go int. cgo cannot convert bool directly to a named C integer typedef
// like gboolean — `C.gboolean(boolVar)` fails to compile — so callers wrap
// the bool first, then pass the int through C.gboolean.
// boolToInt is emitted into model.go only when this is called.
func (t *gtk4Translator) boolToGoInt(expr ir.Expr) ir.Expr {
	if t.boolToIntUsed != nil {
		*t.boolToIntUsed = true
	}
	return &ir.Call{
		Type: ir.TypInt,
		Func: &ir.Func{Name: "boolToInt"},
		Args: []ir.CallArg{{Value: expr}},
	}
}

func (t *gtk4Translator) OnCreateNode(ctx context.Context, id, tag string) []ir.Stmt {
	// Canvas: a `canvas` CreateNode carries a draw func threaded through
	// declarative flattening (LocalVar.CanvasDraw). It has no GIR-native
	// widget, so intercept it before the native-tag lookup and build a
	// GtkDrawingArea with a cairo draw callback.
	if tag == "canvas" {
		if _, ok := t.canvasByID[id]; ok {
			return t.emitCanvasCreate(id)
		}
	}
	// passInlinePure substitutes stdlib wrapper components (vbox, text,
	// button, ...) with their gtk4.sngl-defined native widget bodies
	// before this translator runs, so every tag landing here is a
	// GIR-resolved native widget name (GtkButton, GtkLabel, GtkBox, ...).
	comp, nm := t.lookupNativeByTag(tag)
	if nm == nil {
		// No native widget mapping for this tag (e.g. stdlib component
		// like `avatar`/`chip`/`divider` with no gtk4 override). Record
		// the id as skipped so later AppendChild/PropAssign/AttachHandler
		// references to it are dropped — otherwise we'd emit `m.<id>`
		// for a field that was never declared on Model.
		t.skipped[id] = struct{}{}
		return nil
	}
	// gtk_application_window_new requires the GtkApplication;
	// special-case so it gets the `app` parameter passed into
	// BuildUI rather than nil.
	if nm.Constructor == "gtk_application_window_new" {
		appRef := &ir.Ident{Name: "app", Type: ir.TypDyn}
		if t.wrapped {
			return t.emitConstructorAssign(id, nm.CType, rtCall("ApplicationWindowNew", appRef))
		}
		return t.emitConstructorAssign(id, nm.CType, nativeCall("gtk_application_window_new", appRef))
	}
	_ = comp
	// Wrapped mode: emit a gtk4rt constructor when the widget is in the
	// bounded surface. Unmapped widgets fall through to the cgo ctor below,
	// leaving a `C.` that triggers the whole-program fallback.
	if t.wrapped {
		if ctor, ok := rtCtorForCType(nm.CType); ok {
			return t.emitConstructorAssign(id, nm.CType, ctor)
		}
	}
	// Pass a typed-zero value for each required constructor parameter
	// so the cgo call type-checks. OnPropAssign immediately rewrites
	// any user-supplied prop values via the dedicated setter.
	args := []ir.Expr{}
	for _, p := range nm.CtorParams {
		args = append(args, ctorZeroArg(p.GIRType, p.IRType))
	}
	ctor := nativeCall(nm.Constructor, args...)
	return t.emitConstructorAssign(id, nm.CType, ctor)
}

// ctorZeroArg returns the IR expression for a typed-zero value matching
// the given GIR/IR type, suitable as a placeholder argument to a
// constructor call. Pointer types become nil; primitives become typed
// zero casts; enums/structs become a bare 0 (cgo coerces untyped int
// constants into the enum type).
func ctorZeroArg(girType string, t *ir.Type) ir.Expr {
	switch girType {
	case "utf8", "filename", "gchararray":
		return &ir.Literal{Type: ir.TypNull}
	case "gboolean":
		return nativeCall("gboolean", &ir.Literal{Type: ir.TypInt, Raw: "0"})
	case "gint", "gint32", "gint64", "guint", "guint32", "guint64", "gsize":
		return nativeCall("int", &ir.Literal{Type: ir.TypInt, Raw: "0"})
	case "gdouble", "gfloat":
		return nativeCall("double", &ir.Literal{Type: ir.TypFloat, Raw: "0"})
	}
	if t != nil {
		switch t.Kind {
		case ir.TypeString:
			return &ir.Literal{Type: ir.TypNull}
		case ir.TypeInt:
			return nativeCall("int", &ir.Literal{Type: ir.TypInt, Raw: "0"})
		case ir.TypeBool:
			return nativeCall("gboolean", &ir.Literal{Type: ir.TypInt, Raw: "0"})
		case ir.TypeFloat:
			return nativeCall("double", &ir.Literal{Type: ir.TypFloat, Raw: "0"})
		}
	}
	// Enum or unknown — bare 0 coerces into named cgo integer types.
	return &ir.Literal{Type: ir.TypInt, Raw: "0"}
}

// OnCreateComponent promotes a recursive/non-inlinable user component
// instance to a Model field typed as the opaque GtkWidget pointer, then
// assigns the translated `m.render<Comp>(props...)` call. Keeps `m.<id>`
// references (parent append, etc.) resolvable, mirroring OnCreateNode.
func (t *gtk4Translator) OnCreateComponent(ctx context.Context, id string, call *ir.Call) []ir.Stmt {
	t.idCTypes[id] = "GtkWidget"
	t.topLevel = append(t.topLevel, id)
	if t.isLocalRef(id) {
		// Non-escaping: declare as a function-local `__nN := m.render<Comp>(...)`
		// so each recursion frame keeps its own widget rather than clobbering
		// a shared Model field.
		return []ir.Stmt{&ir.LocalVar{
			Name: id,
			Type: ir.NativePointerOf("GtkWidget"),
			Init: call,
		}}
	}
	t.fieldSink(id, "GtkWidget")
	return []ir.Stmt{&ir.Assign{
		Target: codegen.ModelFieldRef(id),
		Op:     ast.AssignSet,
		Value:  call,
	}}
}

// emitConstructorAssign records the new widget's id↔cType mapping and
// emits `m.<id> = (*C.<cType>)(unsafe.Pointer(ctor))`.
func (t *gtk4Translator) emitConstructorAssign(id, cType string, ctor ir.Expr) []ir.Stmt {
	t.idCTypes[id] = cType
	t.topLevel = append(t.topLevel, id)
	// Wrapped mode: ctor already yields a gtk4rt.Handle — no cgo cast, and the
	// field/local is Handle-typed (LocalVar renders `id := ctor`, ignoring the
	// type; the field goType is set to gtk4rt.Handle by the fieldSink closure).
	initVal := ctor
	if !t.wrapped {
		initVal = cgoCast(cType, ctor)
	}
	if t.isLocalRef(id) {
		// Non-escaping: declare a function-local `__nN := ...` rather than a
		// shared Model field. Each call frame gets its own widget temp —
		// required for recursive component render methods.
		return []ir.Stmt{&ir.LocalVar{
			Name: id,
			Type: ir.NativePointerOf(cType),
			Init: initVal,
		}}
	}
	t.fieldSink(id, cType)
	return []ir.Stmt{&ir.Assign{
		Target: codegen.ModelFieldRef(id),
		Op:     ast.AssignSet,
		Value:  initVal,
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
		return t.idCTypes[codegen.IdentBareName(e)]
	}
	return ""
}

// isSkipped reports whether the expression refers to a synthesized
// widget id whose OnCreateNode emitted nothing (no Model field exists
// for it). Later refs to such ids must be dropped to keep the emitted
// model.go consistent.
func (t *gtk4Translator) isSkipped(e ir.Expr) bool {
	if id, ok := e.(*ir.Ident); ok {
		if _, ok := t.skipped[id.Name]; ok {
			return true
		}
	}
	return false
}

func (t *gtk4Translator) OnAppendChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	if t.isSkipped(parent) || t.isSkipped(child) {
		return nil
	}
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
	if t.wrapped {
		if stmt, ok := rtChildAppendCall(cType, t.qualifyNodeExpr(parent), t.qualifyNodeExpr(child)); ok {
			return []ir.Stmt{stmt}
		}
	}
	parentArg := cgoCast(cType, t.qualifyNodeExpr(parent))
	childArg := cgoCast("GtkWidget", t.qualifyNodeExpr(child))
	return []ir.Stmt{&ir.CallStmt{Call: nativeCall(fn, parentArg, childArg)}}
}

func (t *gtk4Translator) OnRemoveChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	if t.isSkipped(parent) || t.isSkipped(child) {
		return nil
	}
	cType := t.parentCType(parent)
	if cType == "" {
		cType = "GtkBox"
	}
	fn := gtk4ChildRemoveFn(cType)
	if fn == "" {
		return nil
	}
	if t.wrapped {
		if stmt, ok := rtChildRemoveCall(cType, t.qualifyNodeExpr(parent), t.qualifyNodeExpr(child)); ok {
			return []ir.Stmt{stmt}
		}
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
		// The __renderSlotN parent container is a function parameter, not a
		// Model widget field — pass it through even though lowerNodeForSlot
		// marks the AppendChild target ident IsElementRef+Synthesized (which
		// would otherwise rewrite it to a bogus `m.Parent`).
		if id.Name == "parent" || id.Name == "container" {
			return e
		}
		// Non-escaping refs are function-local variables; render the bare
		// name rather than a Model-field selector.
		if t.isLocalRef(id.Name) {
			return &ir.Ident{Name: id.Name, Type: id.Type}
		}
		if strings.HasPrefix(id.Name, "__n") {
			return codegen.ModelFieldRef(id.Name)
		}
		if id.IsElementRef && id.Synthesized {
			return codegen.ModelFieldRef(id.Name)
		}
	}
	return e
}

func (t *gtk4Translator) OnPropAssign(ctx context.Context, node ir.Expr, prop string, value ir.Expr) []ir.Stmt {
	if t.isSkipped(node) {
		return nil
	}
	bare := codegen.IdentBareName(node)
	cType, ok := t.idCTypes[bare]
	if !ok {
		return nil
	}
	// Resolve the prop via the GIR-derived metadata so we get the
	// setter, the receiver-cast type (e.g. *C.GtkEditable for the
	// text setter on a GtkEntry), and any value-type override
	// (e.g. GtkOrientation enum) in a single lookup.
	entry := gtkSetterFor(cType, prop)
	setter := entry.Setter
	recvType := cType
	if entry.RecvType != "" {
		recvType = entry.RecvType
	}
	var valType string
	if setter == "" {
		// Stdlib-mapped name: most "value"/"text" props flow into the
		// underlying GTK "label" setter on labels & buttons.
		switch prop {
		case "value", "text":
			entry = gtkSetterFor(cType, "label")
			setter = entry.Setter
			if entry.RecvType != "" {
				recvType = entry.RecvType
			}
		}
	}
	if comp, _ := t.lookupNativeByCType(cType); comp != nil {
		for _, p := range comp.Props {
			if p.Name == prop {
				if setter == "" && p.NativeSetter != "" {
					setter = p.NativeSetter
				}
				if p.NativeReceiverType != "" {
					recvType = p.NativeReceiverType
				}
				valType = p.NativeValueType
				break
			}
		}
	}
	if setter == "" {
		return nil
	}
	// Wrapped mode: emit a gtk4rt setter over the Handle with the raw
	// Go-native value. Unmapped setters/values fall through to cgo (→ fallback).
	if t.wrapped {
		if rt, ok := rtSetterTable[setter]; ok {
			if arg, ok := rtSetterValue(rt.kind, value); ok {
				return []ir.Stmt{&ir.CallStmt{Call: rtCall(rt.fn, t.qualifyNodeExpr(node), arg)}}
			}
		}
	}
	valArg := t.coerceSetterValue(setter, value, valType)
	cast := cgoCast(recvType, t.qualifyNodeExpr(node))
	return []ir.Stmt{&ir.CallStmt{Call: nativeCall(setter, cast, valArg)}}
}

// coerceSetterValue wraps the SNGL value expression so its Go-side
// representation matches the cgo type the C setter expects.
//   - Booleans → C.gboolean(v)
//   - Integers / Floats → C.int(v) / C.double(v)
//   - Strings → C.CString(v)
//   - Enum-typed setters (NativeValueType set, e.g. GtkOrientation):
//     map known SNGL string literals to their C constants, otherwise
//     cast through the named cgo type.
func (t *gtk4Translator) coerceSetterValue(setter string, value ir.Expr, valType string) ir.Expr {
	// Boolean-only setter shortcut — kept for setters whose GIR metadata
	// we may not have resolved.
	if setter == "gtk_check_button_set_active" {
		return nativeCall("gboolean", t.boolToGoInt(value))
	}
	if valType != "" {
		// Enum / named-type setter. Map literal strings to C constants.
		if lit, ok := value.(*ir.Literal); ok && lit.Type != nil && lit.Type.Kind == ir.TypeString {
			if cst := girEnumConstant(valType, lit.Raw); cst != "" {
				return &ir.Ident{Name: cst, Type: ir.TypDyn}
			}
		}
		// Generic cast: C.<TypeName>(v) — cgo coerces untyped int
		// constants into the named integer type.
		return nativeCall(valType, value)
	}
	if vt := exprIRType(value); vt != nil {
		switch vt.Kind {
		case ir.TypeBool:
			return nativeCall("gboolean", t.boolToGoInt(value))
		case ir.TypeInt:
			return nativeCall("int", value)
		case ir.TypeFloat:
			return nativeCall("double", value)
		}
	}
	return nativeCall("CString", value)
}

// exprIRType returns the IR type carried by an Expr when available.
func exprIRType(e ir.Expr) *ir.Type {
	switch n := e.(type) {
	case *ir.Literal:
		return n.Type
	case *ir.Ident:
		return n.Type
	case *ir.Select:
		return n.Type
	case *ir.Call:
		return n.Type
	case *ir.Binary:
		return n.Type
	case *ir.Conversion:
		return n.Type
	}
	return nil
}

// girEnumConstant maps a (GTK enum type, SNGL string value) pair to
// the corresponding cgo constant identifier. Returns "" when the type
// or value isn't recognised; callers fall back to a generic cast.
func girEnumConstant(enumType, value string) string {
	switch enumType {
	case "GtkOrientation":
		switch value {
		case "horizontal":
			return "C.GTK_ORIENTATION_HORIZONTAL"
		case "vertical":
			return "C.GTK_ORIENTATION_VERTICAL"
		}
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
	if t.isSkipped(node) {
		return nil
	}
	bare := codegen.IdentBareName(node)
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
	// Wrapped mode: gtk4rt.Connect registers the handler in cbind and wires the
	// GTK signal in one call — no per-program snglCallbacks slice or cgo.
	if t.wrapped {
		sigLit := &ir.Literal{Type: ir.TypString, Raw: signal}
		return []ir.Stmt{&ir.CallStmt{Call: rtCall("Connect", t.qualifyNodeExpr(node), sigLit, handler)}}
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
		Target: codegen.ModelFieldRef(slot.Name),
		Op:     ast.AssignSet,
		Value:  &ir.Literal{Type: ir.TypNull},
	}}
}

func (t *gtk4Translator) OnSlotAppend(ctx context.Context, slot *ir.Var, child ir.Expr) []ir.Stmt {
	slotRef := codegen.ModelFieldRef(slot.Name)
	// Wrapped mode: the slot holds []gtk4rt.Handle, so append the handle
	// directly with no cgo cast.
	childArg := ir.Expr(cgoCast("GtkWidget", t.qualifyNodeExpr(child)))
	if t.wrapped {
		childArg = t.qualifyNodeExpr(child)
	}
	appendCall := &ir.Call{
		Type: slot.Type,
		Func: &ir.Func{Name: "append"},
		Args: []ir.CallArg{
			{Value: slotRef},
			{Value: childArg},
		},
	}
	return []ir.Stmt{&ir.Assign{
		Target: codegen.ModelFieldRef(slot.Name),
		Op:     ast.AssignSet,
		Value:  appendCall,
	}}
}

func (t *gtk4Translator) OnIter(ctx context.Context, iter ir.Expr) ir.Expr {
	if id, ok := iter.(*ir.Ident); ok && id.Synthesized {
		return codegen.ModelFieldRef(id.Name)
	}
	return iter
}

func (t *gtk4Translator) OnCond(ctx context.Context, cond ir.Expr) ir.Expr {
	return cond
}

func (t *gtk4Translator) OnDefault(ctx context.Context, stmt ir.Stmt) []ir.Stmt {
	switch n := stmt.(type) {
	case *ir.CallStmt:
		if n.Call != nil && n.Call.Func != nil && strings.HasPrefix(n.Call.Func.Intrinsic, "Canvas") {
			return t.translateCanvasIntrinsic(n)
		}
	case *ir.CanvasRedrawStmt:
		return t.translateCanvasRedraw(n)
	}
	return []ir.Stmt{stmt}
}
