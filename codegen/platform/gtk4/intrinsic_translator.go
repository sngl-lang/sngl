package gtk4

import (
	"context"
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4/gir"
	"git.duckfam.us/jonathan/sngl/ir"
)

// emitShared is the state every translator emitting into one generated file
// accumulates: which preamble helpers that file has to carry, and which
// property assignments could not be emitted. A nil *emitShared answers every
// method, so a scope that emits no widgets need not carry one.
type emitShared struct {
	boolToInt  bool
	gObjectSet bool
	errs       []error
	seen       map[string]bool
}

func (s *emitShared) needBoolToInt() {
	if s != nil {
		s.boolToInt = true
	}
}

func (s *emitShared) needGObjectSet() {
	if s != nil {
		s.gObjectSet = true
	}
}

// fail records that a property cannot be emitted. The same property assigned
// from several places is one error. A nil sink drops it, which is why every
// scope that resolves widget props is given one.
func (s *emitShared) fail(err error) {
	if s == nil {
		return
	}
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	if s.seen[err.Error()] {
		return
	}
	s.seen[err.Error()] = true
	s.errs = append(s.errs, err)
}

// gtk4Translator implements codegen.IntrinsicTranslator for gtk4.
// Emits IR fragments whose Call.Func values carry Foreign.Path="C" /
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
	wrapped      bool
	tagComponent map[string]*ir.Component // tag ("GtkButton") → resolved Component (from pre-walk)
	// registry is the GIR data the widget declarations were generated from.
	// A declaration says which props and events a widget has; the C setter
	// behind each one is read back out of here, keyed by the C type the
	// declaration's #[intrinsic] names. nil in scopes that emit no widgets.
	registry *gir.TypeRegistry
	// shared accumulates what the file being emitted needs beyond the
	// statements themselves: preamble helpers, and the properties that
	// could not be emitted at all. nil in scopes that emit no widgets.
	shared *emitShared

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

// withPkg attaches the package being emitted.
func (t *gtk4Translator) withPkg(pkg *ir.Package) *gtk4Translator {
	t.pkg = pkg
	return t
}

// withRegistry attaches the GIR data behind the widget declarations.
func (t *gtk4Translator) withRegistry(reg *gir.TypeRegistry) *gtk4Translator {
	t.registry = reg
	return t
}

// withShared points the translator at the per-file accumulator described on
// gtk4Translator.shared.
func (t *gtk4Translator) withShared(s *emitShared) *gtk4Translator {
	t.shared = s
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

// widgetClass returns the GTK class behind a tag: the C type its declaration's
// #[intrinsic] names, and the GIR entry the emitter reads the class's C API
// from. Zero values for a tag that is not one of this platform's widgets.
func (t *gtk4Translator) widgetClass(tag string) (string, *gir.ClassInfo) {
	cType := widgetCType(t.tagComponent[tag])
	if cType == "" {
		return "", nil
	}
	info := t.classFor(cType)
	if info == nil {
		return "", nil
	}
	return cType, info
}

// unresolvedTagError explains why a node tag resolved to no GTK widget, or
// returns nil when emitting nothing for it is correct.
//
// Every case is read off the declaration rather than matched against a list of
// names. A library component is abstract — it renders only through the
// `platform gtk4 { ... }` body in codegen/platform/gtk4/gtk4.sngl that
// passPlatformExtensionBody swaps in and passInlinePure then inlines away — so
// one arriving here still bearing its own name is one this platform never
// implemented. A component of the user's own with an empty body is the
// program's own statement that it draws nothing, and is left alone.
func (t *gtk4Translator) unresolvedTagError(tag string) error {
	comp := t.tagComponent[tag]
	if comp == nil {
		return fmt.Errorf("gtk4: no widget for node %q", tag)
	}
	if cType := widgetCType(comp); cType != "" {
		return fmt.Errorf("gtk4: widget %q names C type %s, which the installed Gtk-4.0.gir does not declare", tag, cType)
	}
	if !comp.Stdlib {
		// A user component that renders nothing. Lowering drops such a node
		// before codegen, so this is unreachable through a compile — but this
		// function is the translator's entry point, and the alternative to
		// answering here is telling a user their own component has no gtk4
		// implementation, which is not what is wrong.
		return nil
	}
	// PlatformBodies survives passPlatformExtensionBody — the pass swaps the
	// active platform's entry into Body and leaves the map — so it is still the
	// record of which platforms declared an override, and a gtk4 entry here
	// means the override existed but did not reach a widget.
	if _, ok := comp.PlatformBodies[platformName]; ok {
		return fmt.Errorf("gtk4: component %q has a gtk4 implementation that did not lower to a widget", tag)
	}
	return &codegen.UnimplementedComponent{Component: tag, Platform: platformName}
}

// classFor returns the GIR entry for a C type the walk already resolved.
func (t *gtk4Translator) classFor(cType string) *gir.ClassInfo {
	if t.registry == nil {
		return nil
	}
	return t.registry.ByCType[cType]
}

var _ codegen.IntrinsicTranslator = (*gtk4Translator)(nil)

// nativeFunc constructs an *ir.Func that gc.EvalExpr's namespace-call branch
// emits as cgo source, `C.<identifier>`. Callers pass a bare name like
// "gtk_label_new": the renderer adds the "C." prefix, and one written in
// survives only as a compatibility case.
func nativeFunc(nativeName string) *ir.Func {
	return &ir.Func{Foreign: ir.Foreign{Path: "C", Name: nativeName}, Name: nativeName}
}

// nativeCall builds a single-level cgo C-API call. The receiver
// (`C`) drives the renderer into evalNamespaceCall, which emits
// `C.<identifier>(args)`.
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
	t.shared.needBoolToInt()
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
	cType, info := t.widgetClass(tag)
	if info == nil {
		// Nothing here can be emitted. When the tag names a declaration this
		// platform was supposed to implement, the build has to fail: dropping
		// the node emits a window missing the widgets its source asked for and
		// says so nowhere.
		if err := t.unresolvedTagError(tag); err != nil {
			t.shared.fail(err)
		}
		// Record the id as skipped so later AppendChild/PropAssign/
		// AttachHandler references to it are dropped rather than emitting
		// `m.<id>` for a field that was never declared on Model. A diagnostic
		// only reaches the caller if the rest of the walk does not panic.
		t.skipped[id] = struct{}{}
		return nil
	}
	ctorInfo := pickPrimaryConstructorInfo(info)
	// gtk_application_window_new requires the GtkApplication;
	// special-case so it gets the `app` parameter passed into
	// BuildUI rather than nil.
	if ctorInfo.Name == "gtk_application_window_new" {
		appRef := &ir.Ident{Name: "app", Type: ir.TypDyn}
		if t.wrapped {
			return t.emitConstructorAssign(id, cType, rtCall("ApplicationWindowNew", appRef))
		}
		return t.emitConstructorAssign(id, cType, nativeCall("gtk_application_window_new", appRef))
	}
	// Wrapped mode: emit a gtk4rt constructor when the widget is in the
	// bounded surface. Unmapped widgets fall through to the cgo ctor below,
	// leaving a `C.` that triggers the whole-program fallback.
	if t.wrapped {
		if ctor, ok := rtCtorForCType(cType); ok {
			return t.emitConstructorAssign(id, cType, ctor)
		}
	}
	// GIR describes no constructor for this class at all. The name derived
	// from the class is not one GTK ships for any of them — 46 of the 47
	// such classes are abstract bases and interior helpers (GtkWidget,
	// GtkRange, GtkListItem) — so emitting the call would leave a link
	// error in the user's build instead of a compiler diagnostic here.
	if len(info.Constructors) == 0 {
		t.shared.fail(fmt.Errorf("gtk4: %s cannot be constructed: GTK declares no constructor for it", cType))
		return nil
	}
	// Pass a typed-zero value for each required constructor parameter
	// so the cgo call type-checks. OnPropAssign immediately rewrites
	// any user-supplied prop values via the dedicated setter.
	args := []ir.Expr{}
	for _, p := range ctorInfo.Params {
		arg, err := ctorZeroArg(t.registry, p)
		if err != nil {
			t.shared.fail(fmt.Errorf("gtk4: %s cannot be constructed: %s takes %s %s, %w", cType, ctorInfo.Name, p.GIRType, p.Name, err))
			return nil
		}
		args = append(args, arg)
	}
	ctor := nativeCall(ctorInfo.Name, args...)
	return t.emitConstructorAssign(id, cType, ctor)
}

// ctorZeroArg returns the IR expression for a typed-zero value matching one
// constructor parameter, suitable as a placeholder a later prop assignment
// overwrites. Primitives become typed zero casts, a Gtk enumeration a bare 0
// (cgo coerces an untyped int constant into the named type), and a pointer
// nil — which the parameter's c:type is the only thing in GIR that identifies,
// since a GIR name alone does not say whether GApplicationFlags is an integer
// or GFile is a pointer.
//
// It errors for a parameter whose zero cannot be spelled: a varargs or array
// form with no c:type at all, and a named type from a namespace other than
// Gtk, which the platform does not parse and so cannot tell a foreign enum
// (a bare 0) from a foreign callback (nil). Refusing here is a build
// diagnostic naming the class; guessing emits Go that does not compile.
func ctorZeroArg(reg *gir.TypeRegistry, p gir.ConstructorParam) (ir.Expr, error) {
	switch p.GIRType {
	case "utf8", "filename", "gchararray":
		return &ir.Literal{Type: ir.TypNull}, nil
	case "gboolean", "gint", "gint32", "gint64", "guint", "guint32", "guint64", "gsize":
		return nativeCall(ctorScalarCast(p), &ir.Literal{Type: ir.TypInt, Raw: "0"}), nil
	case "gdouble", "gfloat":
		return nativeCall(ctorScalarCast(p), &ir.Literal{Type: ir.TypFloat, Raw: "0"}), nil
	}
	// A Gtk enumeration or bitfield: an integer, and a bare 0 coerces into
	// the named cgo type.
	if reg != nil && reg.Enums[p.GIRType] != nil {
		return &ir.Literal{Type: ir.TypInt, Raw: "0"}, nil
	}
	// gpointer is void*, which cgo renders as unsafe.Pointer — nil, not 0,
	// even though the c:type carries no star.
	if strings.HasSuffix(p.CType, "*") || p.CType == "gpointer" {
		return &ir.Literal{Type: ir.TypNull}, nil
	}
	if p.CType == "" {
		return nil, fmt.Errorf("which GIR describes with no C type of its own")
	}
	return nil, fmt.Errorf("whose C type %s is from a namespace this platform does not parse, so its zero value cannot be spelled", p.CType)
}

// ctorScalarCast is the cgo type a numeric zero takes to satisfy one
// constructor parameter. It is the parameter's own c:type, not the GIR type's
// canonical cgo spelling — GTK constructors take guint and float parameters of
// gint- and gdouble-named GIR types, and cgo does not convert between named C
// numeric types on its own. A c:type that is not a plain cgo identifier
// ("unsigned int") falls back to the glib typedef, which cgo does accept.
// Mirrors girScalarCast, which does the same for a property's setter.
func ctorScalarCast(p gir.ConstructorParam) string {
	if c := p.CType; c != "" && !strings.ContainsAny(c, "* ") {
		return c
	}
	return p.GIRType
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
	// The single-child setters below back the stdlib overrides that host a
	// slot in something other than a box. Each takes one child, so a second
	// append replaces the first — which is what the GTK API does and what the
	// stdlib component says (card and popover each wrap one region).
	case "GtkFrame":
		return "gtk_frame_set_child"
	case "GtkPopover":
		return "gtk_popover_set_child"
	// gtk_stack_add_child returns the GtkStackPage it created; the emitted
	// call is a statement and discards it, which is what a stack whose pages
	// are never named by the program wants.
	case "GtkStack":
		return "gtk_stack_add_child"
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
	// style is the platform's own prop, forwarded onto the widget root by the
	// override bodies, and no GTK property is behind it. Two GTK classes
	// (GtkTextTag, GtkCellRendererText) do have a property of that name; the
	// declaration reserves the name for style, so it is not theirs.
	if prop == stylePropName {
		return nil
	}
	bare := codegen.IdentBareName(node)
	cType, ok := t.idCTypes[bare]
	if !ok {
		return nil
	}
	info := t.classFor(cType)
	p, hasProp := girProp(info, prop)

	// gtkSetterTable is the platform's deliberate choice, so it wins: it
	// picks gtk_label_set_text over the gtk_label_set_label GIR names, and
	// it answers for props GIR names no setter for (GtkImage.file). GIR's
	// `setter=` answers the rest.
	entry := gtkSetterFor(cType, prop)
	if entry.Setter == "" {
		// Stdlib-mapped name: most "value"/"text" props flow into the
		// underlying GTK "label" setter on labels & buttons.
		switch prop {
		case "value", "text":
			entry = gtkSetterFor(cType, "label")
		}
	}
	// A construct-only property has no reachable write: the widget already
	// exists by the time any prop is assigned, and GObject answers a later
	// g_object_set_property with a g_critical and no change. declgen
	// withholds these, so reaching here means the prop arrived some other
	// way — refuse rather than emit a call that silently does nothing.
	// A static-table entry naming a real setter still wins, above.
	if entry.Setter == "" && hasProp && p.ConstructOnly {
		t.shared.fail(fmt.Errorf("gtk4: %s.%s is construct-only: GTK accepts it only when the widget is created, and nothing can set it afterwards", cType, p.Name))
		return nil
	}
	if entry.Setter == "" && hasProp {
		entry = girSetter(info, p)
	}
	// Wrapped mode: emit a gtk4rt setter over the Handle with the raw
	// Go-native value, before anything cgo-shaped is built — a coercion
	// records that its helper is needed, and here nothing would use it.
	// Setters that surface does not have fall through to cgo, leaving a `C.`
	// that triggers the whole-program fallback.
	if t.wrapped && entry.Setter != "" {
		if rt, ok := rtSetterTable[entry.Setter]; ok {
			if arg, ok := rtSetterValue(rt.kind, value); ok {
				return []ir.Stmt{&ir.CallStmt{Call: rtCall(rt.fn, t.qualifyNodeExpr(node), arg)}}
			}
		}
	}
	recvType := cType
	if entry.RecvType != "" {
		recvType = entry.RecvType
	}
	if !hasProp {
		if entry.Setter == "" {
			// Not a GTK property at all — nothing to set.
			return nil
		}
		// A static-table prop GIR does not describe on this class, because
		// the parse merges interface properties but not inherited ones
		// (GtkApplicationWindow's title). The SNGL value type is all there
		// is to coerce by.
		return t.emitSetterCall(node, recvType, entry.Setter, t.coerceSetterValue(entry.Setter, value))
	}
	kind := propKind(t.registry, p)
	if kind == propUnsettable {
		t.shared.fail(fmt.Errorf("gtk4: %s.%s: no SNGL value can produce %s, which is what this GTK property takes", cType, p.Name, girTypeLabel(p)))
		return nil
	}
	if entry.Setter == "" {
		return t.emitGObjectPropSet(node, p, kind, value)
	}
	valArg, err := t.setterValueArg(kind, p, entry.ValType, value)
	if err != nil {
		t.shared.fail(fmt.Errorf("gtk4: %s.%s: %w", cType, p.Name, err))
		return nil
	}
	return t.emitSetterCall(node, recvType, entry.Setter, valArg)
}

// emitSetterCall is the call to a C setter: the widget cast to the type that
// setter takes as its receiver, then the cgo-coerced value.
func (t *gtk4Translator) emitSetterCall(node ir.Expr, recvType, setter string, valArg ir.Expr) []ir.Stmt {
	cast := cgoCast(recvType, t.qualifyNodeExpr(node))
	return []ir.Stmt{&ir.CallStmt{Call: nativeCall(setter, cast, valArg)}}
}

// gObjectSetFn is the preamble helper that carries each kind of value through
// GObject's generic property path. Every kind but propUnsettable has one, so
// a property GTK ships no setter for is still settable.
var gObjectSetFn = map[propValueKind]string{
	propString: "sngl_set_prop_string",
	propBool:   "sngl_set_prop_bool",
	propInt:    "sngl_set_prop_int",
	propFloat:  "sngl_set_prop_double",
	propEnum:   "sngl_set_prop_int",
}

// emitGObjectPropSet sets a property GTK ships no setter for by name, through
// g_object_set_property. The property's GIR name is the GObject property name;
// the helper transforms the primitive value into whatever GType the class's
// GParamSpec declares, which is how an enum-, float- or unsigned-typed
// property is reached from one of four helpers.
func (t *gtk4Translator) emitGObjectPropSet(node ir.Expr, p gir.Prop, kind propValueKind, value ir.Expr) []ir.Stmt {
	fn, ok := gObjectSetFn[kind]
	if !ok {
		return nil
	}
	valArg, err := t.gObjectValueArg(kind, p, value)
	if err != nil {
		t.shared.fail(fmt.Errorf("gtk4: %s: %w", p.Name, err))
		return nil
	}
	t.shared.needGObjectSet()
	obj := cgoCast("", t.qualifyNodeExpr(node))
	name := nativeCall("CString", &ir.Literal{Type: ir.TypString, Raw: p.Name})
	return []ir.Stmt{&ir.CallStmt{Call: nativeCall(fn, obj, name, valArg)}}
}

// setterValueArg is the value argument for the C setter of a property GIR
// describes: the cgo cast that matches the C parameter type.
func (t *gtk4Translator) setterValueArg(kind propValueKind, p gir.Prop, valType string, value ir.Expr) (ir.Expr, error) {
	switch kind {
	case propString:
		return nativeCall("CString", value), nil
	case propBool:
		return nativeCall("gboolean", t.boolToGoInt(value)), nil
	case propInt, propFloat:
		return nativeCall(girScalarCast(p), value), nil
	case propEnum:
		member, isMember, err := t.enumValueArg(p, value)
		if err != nil {
			return nil, err
		}
		if isMember {
			return member, nil
		}
		// An int flows through the named cgo type, which the setter's
		// parameter is declared as.
		return nativeCall(valType, member), nil
	}
	return nil, fmt.Errorf("no value of this type can be set")
}

// gObjectValueArg is the value argument for one of the gObjectSetFn helpers,
// whose parameters are plain C scalars.
func (t *gtk4Translator) gObjectValueArg(kind propValueKind, p gir.Prop, value ir.Expr) (ir.Expr, error) {
	switch kind {
	case propString:
		return nativeCall("CString", value), nil
	case propBool:
		return nativeCall("int", t.boolToGoInt(value)), nil
	case propInt:
		return nativeCall("int", value), nil
	case propFloat:
		return nativeCall("double", value), nil
	case propEnum:
		member, _, err := t.enumValueArg(p, value)
		if err != nil {
			return nil, err
		}
		return nativeCall("int", member), nil
	}
	return nil, fmt.Errorf("no value of this type can be set")
}

// enumValueArg resolves the value bound to an enum- or bitfield-typed
// property: a string literal names one of its members, and an int is the
// member's own number. isMember reports the former, whose cgo spelling is
// already typed and needs no cast.
func (t *gtk4Translator) enumValueArg(p gir.Prop, value ir.Expr) (arg ir.Expr, isMember bool, err error) {
	if lit, ok := value.(*ir.Literal); ok && lit.Type != nil && lit.Type.Kind == ir.TypeString {
		cst := girEnumMember(t.registry, p.GIRType, lit.Raw)
		if cst == "" {
			return nil, false, fmt.Errorf("%q names no member of the %s enumeration", lit.Raw, p.GIRType)
		}
		return &ir.Ident{Name: cst, Type: ir.TypDyn}, true, nil
	}
	if vt := exprIRType(value); vt != nil && vt.Kind == ir.TypeInt {
		return value, false, nil
	}
	return nil, false, fmt.Errorf("a %s value is a string literal naming one of its members, or an int", p.GIRType)
}

// girTypeLabel names the type a property takes, for an error a reader can act
// on. An array property carries no GIR type of its own — its inner <type>
// names the element.
func girTypeLabel(p gir.Prop) string {
	if p.GIRType == "" {
		return "an array"
	}
	return "a " + p.GIRType
}

// coerceSetterValue wraps the SNGL value expression so its Go-side
// representation matches the cgo type the C setter expects. This is the path
// for a prop the loaded GIR does not describe, where the SNGL value's own type
// is all there is to go on:
//   - Booleans → C.gboolean(v)
//   - Integers / Floats → C.int(v) / C.double(v)
//   - anything else → C.CString(v)
func (t *gtk4Translator) coerceSetterValue(setter string, value ir.Expr) ir.Expr {
	// A boolean setter whose property the GIR did not carry: the value may
	// well be typed dyn, and the cast below would make a string of it.
	if setter == "gtk_check_button_set_active" {
		return nativeCall("gboolean", t.boolToGoInt(value))
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
		// The declared event is the SNGL spelling of a GLib signal name.
		sig, ok := girSignal(t.classFor(cType), event)
		if !ok {
			return nil
		}
		// The trampoline this connects is (instance, user_data) returning
		// void. A signal of any other shape would hand its first argument to
		// the dispatcher in place of the callback index, so refuse the build
		// naming it rather than emit a connection that misfires. declgen
		// withholds these from the declaration, so reaching here means the
		// event arrived some other way — a static table entry, or a stdlib
		// override written against a signal that is not connectable.
		if !sig.Connectable() {
			t.shared.fail(fmt.Errorf("gtk4: %s.%s: the %q signal passes %d argument(s) and returns %s; only a void signal with none can be connected",
				cType, event, sig.Name, sig.Params, girSignalReturnLabel(sig)))
			return nil
		}
		signal = sig.Name
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
