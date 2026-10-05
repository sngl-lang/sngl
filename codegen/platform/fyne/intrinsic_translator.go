package fyne

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// fyneTranslator implements codegen.IntrinsicTranslator for fyne.
// Produces ir.Stmt fragments; the platform emitter feeds them through
// gc.EvalStmt to produce Go source. One instance per __renderSlot<N>
// (or promoted handler) emission; widget-field registrations flow back
// into the enclosing compilation via fieldSink.
type fyneTranslator struct {
	gc *golang.GoIRContext
	// specs resolves a lowered node id to the widget Spec its instantiation
	// carried. Built package-wide by collectNodes, because a promoted handler
	// or a reactive splice references nodes created in a sibling Func.
	//
	// The key is the node id and not the tag: every widget in fyne.sngl lowers
	// to one of three primitives, so a tag names the children contract rather
	// than the widget.
	specs      map[string]*fyneSpec
	fieldSink  func(name, goType string)
	importSink func(path string)
	// localRefs is the set of synthesized widget ref ids that lower's
	// passNodeEscape determined do NOT escape this scope. For those ids
	// OnCreateNode/OnCreateComponent emit a function-local `__nN := ...`
	// rather than a shared Model field, and the qualify* helpers render the
	// bare local name. Escaping ids keep the Model-field behavior. nil →
	// every id is a field.
	localRefs map[string]bool
	// fieldIDs are the ids this scope registered as fields regardless of what
	// passNodeEscape concluded. A canvas is the case: its redraw reaches the
	// image and its drawing context from whatever scope mutates the state, and
	// a CanvasRedrawStmt names the draw func rather than the node, so the
	// escape analysis cannot see that use and calls the node local.
	fieldIDs map[string]bool
	// topLevel tracks widget ids created via OnCreateNode that have not
	// (yet) been consumed by an AppendChild. Window-body/component-method
	// emission uses this to discover the topmost widget(s) to return as
	// the fyne.CanvasObject result. Slot-Func emission ignores it.
	topLevel []string
	// appChildren are the nodes this scope attached to the application, in
	// order.
	appChildren []string
	// slotRoot is the container a reactive slot in this scope's body renders
	// into, when this scope owns one. A call to that slot's renderer holds a
	// place in the tree exactly as a created widget does -- the subtree is
	// built into the container rather than named by a ref -- so OnDefault
	// records the container in topLevel at that position. Empty in a scope
	// whose root is decided some other way, which is every Model scope: the
	// Model's own __root is the wrapper its BuildUI already returns.
	slotRoot string
	// slotAnchor is the anchor field of the slot this render func renders,
	// set when the func resets its slot.
	slotAnchor ir.Expr

	// Canvas2D state. canvasByID/canvasByNode map a flattened canvas element to
	// its Model widget field and draw func, shared into every translator that
	// may create a canvas or emit a redraw. The drawing itself needs nothing
	// here: it is the shape overrides' own `#[go.native]` calls by now.
	// invokerSink records one (id, event) pair per handler attached, for the
	// test-invoker methods emitted after the walk. nil in scopes with no test
	// surface -- a slot func, a canvas draw.
	invokerSink func(fyneEventInvoker)
	// failSink is where a prop this platform cannot emit is reported. See
	// withFailSink.
	failSink func(error)

	// plainHandle names the instance ids whose handle is the widget the render
	// returned rather than a record carrying it, as OnCreateComponent decided
	// from the component's own RuntimeInstance mark. Only a record has a Root
	// field, so OnComponentRoot reads this before selecting one.
	plainHandle map[string]bool

	canvasByID map[string]*canvasMeta
	// canvasByNode is the same records keyed by the canvas instantiation,
	// which is what a repaint names -- there being no draw function in the IR
	// for it to point at.
	canvasByNode map[*ir.NodeInst]*canvasMeta
	// canvasTaps are the canvases a click handler put in a fynelayout.Tap,
	// which is then what their container holds in the canvas's place.
	canvasTaps map[string]bool
}

func newFyneTranslator(gc *golang.GoIRContext, specs map[string]*fyneSpec, fieldSink func(name, goType string), importSink func(path string), failSink func(error)) *fyneTranslator {
	return &fyneTranslator{
		gc:         gc,
		specs:      specs,
		fieldSink:  fieldSink,
		importSink: importSink,
		failSink:   failSink,
		fieldIDs:   map[string]bool{},
		canvasTaps: map[string]bool{},
	}
}

// fieldRef is a `<recv>.<name>` selector against the struct this emission's
// scope dispatches through -- the Model in a Model method, the instance record
// inside a component's ctor. Every node field a translator writes goes through
// here rather than codegen.ModelFieldRef, which names the Model and only the
// Model.
func (t *fyneTranslator) fieldRef(name string) ir.Expr {
	return codegen.RecvFieldRef(t.gc.NodeRecv(name), name)
}

// recvIdent is that receiver as a call target.
func (t *fyneTranslator) recvIdent() ir.Expr {
	return &ir.Ident{Name: t.gc.RecvName()}
}

var _ codegen.IntrinsicTranslator = (*fyneTranslator)(nil)

// withLocalRefs sets the non-escaping ref-id set for the scope this
// translator emits. See fyneTranslator.localRefs.
// fyneEventInvoker is one (id, event) pair a test can drive. Firing it invokes
// the widget's own callback field -- the same thing fyne's test.Tap does, and
// the same thing a real tap does -- so no layout or synthetic pointer is
// involved.
type fyneEventInvoker struct {
	IDLabel   string // the #id a program wrote
	SnglEvent string // the event a test writes, e.g. "click"
	Field     string // the Go callback field, e.g. "OnTapped"
	Param     string // the declared parameter name, "" for a no-arg event
	ParamType string // its Go type, "" for a no-arg event
	// Payload is the SNGL payload's Go type a test passes, and PayloadField
	// the field of it the callback takes; both "" when the handler declares
	// no payload or none of its fields is the callback's type.
	Payload      string
	PayloadField string
	// Target is the Model field holding Field when it is not the #id's own
	// widget -- a canvas's Tap. "" means IDLabel.
	Target string
}

func (t *fyneTranslator) withInvokerSink(sink func(fyneEventInvoker)) *fyneTranslator {
	t.invokerSink = sink
	return t
}

// fail records that a prop assignment cannot be emitted.
//
// The sink is a constructor argument rather than a `withX` option, because a
// scope given none drops what it is told -- which is exactly the failure this
// exists to end. gtk4 has carried the same thing all along in emitShared.fail.
func (t *fyneTranslator) fail(err error) {
	if t.failSink != nil {
		t.failSink(err)
	}
}

func (t *fyneTranslator) withLocalRefs(local map[string]bool) *fyneTranslator {
	t.localRefs = local
	return t
}

func (t *fyneTranslator) withSlotRoot(name string) *fyneTranslator {
	t.slotRoot = name
	return t
}

// recordsSlotRoot reports whether stmt is the call that renders this scope's
// own slot container, which is what puts that container in the tree.
func (t *fyneTranslator) recordsSlotRoot(stmt ir.Stmt) bool {
	if t.slotRoot == "" {
		return false
	}
	cs, ok := stmt.(*ir.CallStmt)
	if !ok || cs.Call == nil || cs.Call.Func == nil || !cs.Call.Func.SlotRender || len(cs.Call.Args) != 1 {
		return false
	}
	id, ok := cs.Call.Args[0].Value.(*ir.Ident)
	return ok && id.Name == t.slotRoot
}

// isLocalRef reports whether id is a non-escaping ref that should be emitted
// as a function-local variable rather than a Model field.
func (t *fyneTranslator) isLocalRef(id string) bool {
	if t.fieldIDs[id] {
		return false
	}
	return t.localRefs != nil && t.localRefs[id]
}

// localElementRef renders a bare local-variable reference for a non-escaping
// widget id (used by the qualify* helpers and return trailers).
func localElementRef(name string) ir.Expr {
	return &ir.Ident{Name: name, Type: ir.TypDyn}
}

// fyneFrameworkPkgs maps the fyne package selectors this platform emits
// *itself* — the toast box, the BuildUI fallbacks, the multi-root wrapper — to
// their full Go import paths, and is what the Model's rendered field-type
// strings are scanned against.
//
// A widget's own packages do not come from here: those are the `imports` its
// Spec declares, which is what lets a Spec name a Go module this table never
// heard of. The table is the fallback for a selector no Spec resolved, so the
// fyne widgets need not repeat what every build already imports.
var fyneFrameworkPkgs = map[string]string{
	"fyne":      "fyne.io/fyne/v2",
	"widget":    "fyne.io/fyne/v2/widget",
	"container": "fyne.io/fyne/v2/container",
	"canvas":    "fyne.io/fyne/v2/canvas",
	"layout":    "fyne.io/fyne/v2/layout",
}

// fyneImportPath returns the full Go import path for a fyne package selector,
// or "" if the selector isn't a known framework package (so callers leave it
// unchanged — e.g. std "strings", or a go: native already carrying a path).
func fyneImportPath(sel string) string {
	if p, ok := fyneFrameworkPkgs[sel]; ok && p != sel {
		return p
	}
	return ""
}

// nativeCall builds a Call that gc.EvalExpr renders verbatim. A dotted name
// (e.g. "widget.NewLabel") is emitted qualified, through the import path its
// selector stands for. A bare name (e.g. "append") is emitted as written.
// nativeCallAt is nativeCall for a name whose import path is already known --
// a Spec's, which travels with the Native rather than being recovered from the
// selector. The name arrives qualified with the alias the context assigned.
func nativeCallAt(qualifiedName, path string, args []ir.Expr, retType *ir.Type) *ir.Call {
	c := nativeCall(qualifiedName, args, retType)
	if path != "" {
		c.Func = &ir.Func{Foreign: ir.Foreign{Path: path, Name: qualifiedName}}
	}
	return c
}

func nativeCall(nativeName string, args []ir.Expr, retType *ir.Type) *ir.Call {
	callArgs := make([]ir.CallArg, len(args))
	for i, a := range args {
		callArgs[i] = ir.CallArg{Value: a}
	}
	if dot := strings.Index(nativeName, "."); dot > 0 {
		pkg := nativeName[:dot]
		// The import path must be the full Go one so platforms can collect it
		// (gc.Imports()), while the name keeps the selector it is written with
		// (e.g. "widget" in "widget.NewLabel"). Resolve the known fyne
		// selectors; std/other selectors pass through unchanged.
		nativePkg := pkg
		if full := fyneImportPath(pkg); full != "" {
			nativePkg = full
		}
		return &ir.Call{
			Type:     retType,
			Receiver: &ir.Ident{Name: pkg},
			Func:     &ir.Func{Foreign: ir.Foreign{Path: nativePkg, Name: nativeName}},
			Args:     callArgs,
		}
	}
	return &ir.Call{
		Type: retType,
		Func: &ir.Func{Name: nativeName},
		Args: callArgs,
	}
}

// methodCall builds an ir.Call shaped as `receiver.Method(args...)`.
func methodCall(receiver ir.Expr, method string, args []ir.Expr, retType *ir.Type) *ir.Call {
	callArgs := make([]ir.CallArg, len(args))
	for i, a := range args {
		callArgs[i] = ir.CallArg{Value: a}
	}
	return &ir.Call{
		Type:     retType,
		Receiver: receiver,
		Func:     &ir.Func{Name: method},
		Args:     callArgs,
	}
}

func (t *fyneTranslator) OnCreateNode(ctx context.Context, id, tag string) []ir.Stmt {
	if tag == "canvas" {
		if _, ok := t.canvasByID[id]; ok {
			return t.emitCanvasCreate(id)
		}
	}
	sp, ok := t.specs[id]
	if !ok {
		return nil
	}
	t.topLevel = append(t.topLevel, id)
	// The Go type reaches the output as a Model field's type string rather
	// than as an evaluated call, so gc never sees it as an expression --
	// qualifying it here is what registers its import and fixes its alias.
	goType := sp.GoType.qualify(t.gc)
	if !t.isLocalRef(id) {
		t.fieldSink(id, goType)
	}
	ctor := t.flexLayoutCall(sp)
	if ctor == nil {
		ctor = nativeCallAt(sp.New.qualify(t.gc), sp.New.Path, sp.ctorArgs(t.gc), ir.TypDyn)
	}
	if t.isLocalRef(id) {
		// Non-escaping: declare a function-local `__nN := <ctor>` so each
		// call frame (notably a recursive render method) keeps its own
		// widget temp rather than clobbering a shared Model field.
		return []ir.Stmt{&ir.LocalVar{Name: id, Init: ctor}}
	}
	return []ir.Stmt{&ir.Assign{
		Target: t.fieldRef(id),
		Op:     ast.AssignSet,
		Value:  ctor,
	}}
}

// OnCreateComponent binds a name to a live instance of a non-inlinable user
// component: `__nX = newCardInstance(props...)`, which is what the Go IR
// context renders the CreateComponent call as.
//
// The id names the INSTANCE, so it is not a top-level widget candidate --
// ComponentRoot is what yields something a container can hold. Listing it here
// left an instance record in the window's root set, which used to compile
// because the field was typed as a widget and no longer does.
func (t *fyneTranslator) OnCreateComponent(ctx context.Context, id string, call *ir.Call) []ir.Stmt {
	if t.instanceGoType(call) == fyneWidgetHandleType {
		if t.plainHandle == nil {
			t.plainHandle = map[string]bool{}
		}
		t.plainHandle[id] = true
	}
	if t.isLocalRef(id) {
		// Non-escaping: function-local, so each recursion frame and each row
		// of a list keeps its own instance.
		return []ir.Stmt{&ir.LocalVar{Name: id, Init: call}}
	}
	t.fieldSink(id, t.instanceGoType(call))
	return []ir.Stmt{&ir.Assign{
		Target: t.fieldRef(id),
		Op:     ast.AssignSet,
		Value:  call,
	}}
}

// instanceGoType is the Go type a held instance handle has: the record when
// this host builds one, and the widget the render returned when it does not.
func (t *fyneTranslator) instanceGoType(call *ir.Call) string {
	if t.gc.InstanceRecords {
		if comp := golang.CreateComponentTarget(call); comp != nil && comp.RuntimeInstance {
			return "*" + golang.ComponentInstanceType(comp.Name)
		}
	}
	return fyneWidgetHandleType
}

// fyneWidgetHandleType is what a component's render returns where the build
// emits it as a method rather than a record: the widget itself.
const fyneWidgetHandleType = "fyne.CanvasObject"

// OnComponentRoot binds a name to the widget an instance renders as, so
// AppendChild has something to attach -- and that widget, not the instance, is
// what a top-level position holds.
//
// Local or field by the same escape analysis every other node id answers to. A
// local unconditionally is what left `__nN__el := …` declared in the body and
// `m.__nN__el` read two lines later.
func (t *fyneTranslator) OnComponentRoot(ctx context.Context, id string, inst ir.Expr) []ir.Stmt {
	t.topLevel = append(t.topLevel, id)
	var root ir.Expr = t.instanceRef(inst)
	if !t.plainHandle[codegen.IdentBareName(inst)] {
		// Only a record has a Root field. Where the build renders the
		// component as a method of the enclosing scope instead, the handle IS
		// the widget it returned -- `__n0.Root` on a fyne.CanvasObject names
		// no field the emitted file has.
		root = &ir.Select{Type: ir.TypDyn, Operand: root, Field: golang.ComponentRootField}
	}
	if t.isLocalRef(id) {
		return []ir.Stmt{&ir.LocalVar{Name: id, Init: root}}
	}
	t.fieldSink(id, fyneWidgetHandleType)
	return []ir.Stmt{&ir.Assign{Target: t.fieldRef(id), Op: ast.AssignSet, Value: root}}
}

// instanceRef qualifies the handle an instance op names. The reconcile a
// reactive slot performs declares that handle as a local of the render func,
// so a bare synthesized ident renders as `m.__nN` -- a field of the Model,
// against a local the same function just declared -- unless it is asked here
// whether the id escapes.
func (t *fyneTranslator) instanceRef(inst ir.Expr) ir.Expr { return t.qualifyChildExpr(inst) }

// OnUpdateComponent patches a prop on a live instance through the setter the
// instance carries for it.
func (t *fyneTranslator) OnUpdateComponent(ctx context.Context, inst ir.Expr, prop string, value ir.Expr) []ir.Stmt {
	return []ir.Stmt{&ir.CallStmt{Call: methodCall(t.instanceRef(inst), golang.ComponentSetterMethod(prop), []ir.Expr{value}, ir.TypVoid)}}
}

// OnDestroyComponent ends the instance's lifetime.
func (t *fyneTranslator) OnDestroyComponent(ctx context.Context, inst ir.Expr) []ir.Stmt {
	return []ir.Stmt{&ir.CallStmt{Call: methodCall(t.instanceRef(inst), golang.ComponentDestroyMethod, nil, ir.TypVoid)}}
}

func (t *fyneTranslator) OnAppendChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	// A child that's been appended to a parent is no longer a top-level
	// candidate. Window/component emitters consult topLevel to decide
	// what to return.
	if id, ok := child.(*ir.Ident); ok && id.Synthesized {
		for i, name := range t.topLevel {
			if name == id.Name || (t.canvasTaps[id.Name] && name == canvasTapField(id.Name)) {
				t.topLevel = append(t.topLevel[:i], t.topLevel[i+1:]...)
				break
			}
		}
	}
	if ir.IsAppParent(parent) {
		return t.appAttach(child)
	}
	// Single-child containers (e.g. *container.Scroll) have no Add method;
	// assign to the field the Spec names instead.
	sp := t.specs[codegen.IdentBareName(parent)]
	childSpec := t.specs[codegen.IdentBareName(child)]
	if id, ok := parent.(*ir.Ident); ok && id.Name == "parent" && t.slotAnchor != nil {
		return []ir.Stmt{&ir.CallStmt{Call: nativeCallAt("fynelayout.InsertBefore", fyneLayoutImportPath,
			[]ir.Expr{t.qualifyParentExpr(parent), t.slotAnchor, t.themed(t.qualifyChildExpr(child), childSpec)}, ir.TypVoid)}}
	}
	parent = t.qualifyParentExpr(parent)
	child = t.themed(t.qualifyChildExpr(child), childSpec)
	if sp != nil && sp.isSingleChild() {
		return []ir.Stmt{&ir.Assign{
			Target: &ir.Select{Operand: parent, Field: sp.Content, Type: ir.TypDyn},
			Op:     ast.AssignSet,
			Value:  child,
		}}
	}
	// A parent with no Spec is the slot-function `parent` param or the
	// synthesized __root container, both of which are *fyne.Container.
	method := "Add"
	if sp != nil {
		method = sp.addMethod()
	}
	return []ir.Stmt{&ir.CallStmt{Call: methodCall(parent, method, []ir.Expr{child}, ir.TypVoid)}}
}

func (t *fyneTranslator) OnRemoveChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	if ir.IsAppParent(parent) {
		return []ir.Stmt{&ir.CallStmt{Call: nativeCallAt("fynelayout.AppDetach", fyneLayoutImportPath,
			[]ir.Expr{t.qualifyChildExpr(child)}, ir.TypVoid)}}
	}
	// A render slot's container may be the application, which only the
	// runtime can tell from the value it is handed.
	if id, ok := parent.(*ir.Ident); ok && id.Name == "parent" {
		return []ir.Stmt{&ir.CallStmt{Call: nativeCallAt("fynelayout.Remove", fyneLayoutImportPath,
			[]ir.Expr{t.qualifyParentExpr(parent), t.qualifyChildExpr(child)}, ir.TypVoid)}}
	}
	parent = t.qualifyParentExpr(parent)
	child = t.qualifyChildExpr(child)
	return []ir.Stmt{&ir.CallStmt{Call: methodCall(parent, "Remove", []ir.Expr{child}, ir.TypVoid)}}
}

// slotParentParam is what a render slot's func calls the container it renders
// into; `container` would shadow the fyne package.
const slotParentParam = "__parent"

// qualifyParentExpr renames the slot-function parameter `parent` to
// slotParentParam, the typed parameter emitIRSlotFunc declares.
func (t *fyneTranslator) qualifyParentExpr(e ir.Expr) ir.Expr {
	if id, ok := e.(*ir.Ident); ok {
		if id.Name == "parent" {
			return &ir.Ident{Name: slotParentParam, Type: ir.TypDyn}
		}
		if t.isLocalRef(id.Name) {
			return localElementRef(id.Name)
		}
		// Synthesized __nN parents from inline AppendChild calls in
		// window/component bodies need an `m.` qualifier; slot Funcs use
		// the typed slotParentParam instead.
		if id.Synthesized && strings.HasPrefix(id.Name, "__n") {
			return t.fieldRef(id.Name)
		}
	}
	return e
}

// qualifyChildExpr re-qualifies a synthesized widget ident (e.g.
// "__n0") to a Model field reference. Non-synth idents (loop-key
// "__entry", etc.) and already-qualified expressions pass through.
func (t *fyneTranslator) qualifyChildExpr(e ir.Expr) ir.Expr {
	if id, ok := e.(*ir.Ident); ok {
		if t.canvasTaps[id.Name] {
			return t.fieldRef(canvasTapField(id.Name))
		}
		if t.isLocalRef(id.Name) {
			return localElementRef(id.Name)
		}
		if id.Synthesized && strings.HasPrefix(id.Name, "__n") {
			return t.fieldRef(id.Name)
		}
	}
	return e
}

func (t *fyneTranslator) OnAttachHandler(ctx context.Context, node ir.Expr, event string, handler ir.Expr) []ir.Stmt {
	bareID := codegen.IdentBareName(node)
	if _, isCanvas := t.canvasByID[bareID]; isCanvas && event == "click" {
		return t.attachCanvasClick(bareID, event, handler)
	}
	sp, ok := t.specs[bareID]
	if !ok {
		return nil
	}
	h, ok := sp.Handlers[event]
	if !ok {
		return nil
	}
	fieldName := h.Field
	t.recordInvoker(bareID, codegen.TriggerEventName(handler, event), h, handler)
	// Qualify node + handler to Model references when synthesized/promoted.
	nodeRef := t.qualifyHandlerNode(node, bareID)
	handlerRef := t.qualifyHandlerFunc(handler)
	if lam, ok := handlerRef.(*ir.Lambda); ok {
		handlerRef = fyneCallbackLambda(lam, h, event)
	}
	return []ir.Stmt{&ir.Assign{
		Target: &ir.Select{
			Operand: nodeRef,
			Field:   fieldName,
			Type:    ir.TypDyn,
		},
		Op:    ast.AssignSet,
		Value: handlerRef,
	}}
}

// fyneCallbackLambda gives a handler written in place -- in an instance
// record, where nothing promotes it to a method -- the Go signature of the
// callback field it is assigned to, the way emitIRPromotedHandler does for a
// promoted one: the SNGL event parameter goes, and the two-way bind that opens
// the body reads the callback's own parameter instead.
func fyneCallbackLambda(lam *ir.Lambda, h fyneHandler, event string) ir.Expr {
	if lam.Func == nil || h.Signature == "" {
		return lam
	}
	params, err := signatureParams("", event, h.Signature)
	if err != nil {
		panic("fyne: " + err.Error())
	}
	stmts := lam.Func.Block
	if h.Param != "" {
		if bindVar := extractEventBindTarget(stmts, lam.Func.Params); bindVar != "" {
			bind := stmts[0].(*ir.Assign)
			stmts = append([]ir.Stmt{&ir.Assign{
				Target: bind.Target,
				Op:     ast.AssignSet,
				Value:  &ir.Ident{Name: h.Param},
			}}, stmts[1:]...)
		}
	}
	stmts = substitutePayload(stmts, lam.Func.Params, h.Param, params)
	fn := *lam.Func
	fn.Params = params
	fn.Block = stmts
	out := *lam
	out.Func = &fn
	return &out
}

// substitutePayload reads the event's value off the callback's own parameter
// named goParam, where its Go type is the field's.
func substitutePayload(stmts []ir.Stmt, sngl []*ir.Param, goParam string, goParams []*ir.Param) []ir.Stmt {
	if goParam == "" {
		return stmts
	}
	var goType string
	for _, p := range goParams {
		if ref, ok := p.Type.Meta.(ir.NativeTypeRef); p.Name == goParam && ok {
			goType = ref.Name
		}
	}
	return codegen.SubstituteEventPayload(stmts, sngl, func(f *ir.StructField) ir.Expr {
		if f.Type == nil || golang.IRTypeToGo(f.Type) != goType {
			return nil
		}
		return &ir.Ident{Name: goParam, Type: f.Type}
	})
}

// OnDetachHandler clears the callback field. Fyne holds one callback per
// event rather than a list of listeners, so detaching is assigning nothing and
// the handler expression is not needed to identify what to remove.
func (t *fyneTranslator) OnDetachHandler(ctx context.Context, node ir.Expr, event string, _ ir.Expr) []ir.Stmt {
	bareID := codegen.IdentBareName(node)
	sp, ok := t.specs[bareID]
	if !ok {
		return nil
	}
	h, ok := sp.Handlers[event]
	if !ok {
		return nil
	}
	return []ir.Stmt{&ir.Assign{
		Target: &ir.Select{
			Operand: t.qualifyHandlerNode(node, bareID),
			Field:   h.Field,
			Type:    ir.TypDyn,
		},
		Op:    ast.AssignSet,
		Value: &ir.Literal{Type: ir.TypNull},
	}}
}

// nodeRefFor returns the reference expression for a synthesized widget id:
// a bare local for non-escaping ids, else a Model-field selector.
func (t *fyneTranslator) nodeRefFor(bareID string) ir.Expr {
	if t.isLocalRef(bareID) {
		return localElementRef(bareID)
	}
	return t.fieldRef(bareID)
}

// qualifyHandlerNode produces a ref for a node id (local or Model-field).
func (t *fyneTranslator) qualifyHandlerNode(e ir.Expr, bareID string) ir.Expr {
	if bareID != "" {
		return t.nodeRefFor(bareID)
	}
	return e
}

// qualifyHandlerFunc qualifies a promoted-handler ident (e.g.
// "handleClick" with "__" prefix) to a Model method ref.
func (t *fyneTranslator) qualifyHandlerFunc(e ir.Expr) ir.Expr {
	if id, ok := e.(*ir.Ident); ok {
		name := id.Name
		if strings.HasPrefix(name, "m.") {
			return e
		}
		if strings.HasPrefix(name, "__") {
			return t.fieldRef(name)
		}
	}
	return e
}

func (t *fyneTranslator) OnPropAssign(ctx context.Context, node ir.Expr, prop string, value ir.Expr) []ir.Stmt {
	bareID := codegen.IdentBareName(node)
	sp, ok := t.specs[bareID]
	if !ok {
		// Not a widget this scope built -- a canvas, or a node another
		// translator owns. Nothing to say about it here.
		return nil
	}
	if structuralProp(prop) {
		// This platform's own props, not the widget's: `spec` is the record
		// that says which Fyne widget to build, and `style` is forwarded onto
		// the widget root by the override bodies. Neither is a value with a
		// setter behind it, which is the same exemption gtk4 makes for its
		// `style`.
		return nil
	}
	if sp.CtorOnly[prop] && value == sp.CtorProps[prop] {
		return nil
	}
	// A window that names no title is built with none.
	if lit, ok := value.(*ir.Literal); ok && sp.toplevel && prop == "title" && lit.Value == "" {
		return nil
	}
	methodName, ok := sp.Setters[prop]
	if !ok {
		// The write is what a prop *is* by the time it reaches a platform:
		// passReactivity and passDeclarative turn `text(value=greeting)` into
		// `__n0.value = greeting`, and this is where that becomes
		// `SetText`. A prop the Spec names no setter for therefore does not
		// render as a stale value -- it does not render at all, and returning
		// no statements said so to nobody.
		//
		// Reported rather than dropped, which is the rule gtk4 has followed
		// all along for its own unsettable props. A prop declared on a widget
		// in fyne.sngl with no `Setter` beside it is the declaration being
		// incomplete, and that is a thing to fix rather than to discover by
		// looking at the screen.
		named := "none at all"
		if len(sp.Setters) > 0 {
			named = strings.Join(slices.Sorted(maps.Keys(sp.Setters)), ", ")
		}
		t.fail(fmt.Errorf("fyne: %s has no setter for %q, so the value reaches the screen not at all; its Spec names %s",
			sp.GoType.Name, prop, named))
		return nil
	}
	nodeRef := t.nodeRefFor(bareID)
	set := &ir.CallStmt{Call: methodCall(nodeRef, methodName, []ir.Expr{value}, ir.TypVoid)}
	if cur := sp.Currents[prop]; cur != "" {
		return []ir.Stmt{&ir.If{
			Cond: &ir.Binary{Op: ast.BinNeq, Type: ir.TypBool,
				Left:  &ir.Select{Operand: t.nodeRefFor(bareID), Field: cur, Type: value.ExprType()},
				Right: value},
			Body: []ir.Stmt{set},
		}}
	}
	return []ir.Stmt{set}
}

func (t *fyneTranslator) OnSlotReset(ctx context.Context, slot *ir.Var) []ir.Stmt {
	t.slotAnchor = codegen.RecvFieldRef(t.gc.NodeRecv(slot.Name), codegen.SlotAnchorField(slot.Name))
	anchor := nativeCallAt("fynelayout.SlotAnchor", fyneLayoutImportPath,
		[]ir.Expr{t.qualifyParentExpr(&ir.Ident{Name: "parent"}), t.slotAnchor}, ir.TypDyn)
	return []ir.Stmt{
		&ir.Assign{
			Target: t.fieldRef(slot.Name),
			Op:     ast.AssignSet,
			Value:  &ir.Literal{Type: ir.TypNull},
		},
		&ir.Assign{Target: t.slotAnchor, Op: ast.AssignSet, Value: anchor},
	}
}

func (t *fyneTranslator) OnSlotAppend(ctx context.Context, slot *ir.Var, child ir.Expr) []ir.Stmt {
	slotRef := t.fieldRef(slot.Name)
	child = t.qualifyChildExpr(child)
	appendExpr := nativeCall("append", []ir.Expr{slotRef, child}, slot.Type)
	return []ir.Stmt{&ir.Assign{
		Target: t.fieldRef(slot.Name),
		Op:     ast.AssignSet,
		Value:  appendExpr,
	}}
}

func (t *fyneTranslator) OnIter(ctx context.Context, iter ir.Expr) ir.Expr {
	if id, ok := iter.(*ir.Ident); ok && id.Synthesized {
		return t.fieldRef(id.Name)
	}
	return iter
}

func (t *fyneTranslator) OnCond(ctx context.Context, cond ir.Expr) ir.Expr {
	return cond
}

func (t *fyneTranslator) OnDefault(ctx context.Context, stmt ir.Stmt) []ir.Stmt {
	if t.recordsSlotRoot(stmt) && !slices.Contains(t.topLevel, t.slotRoot) {
		t.topLevel = append(t.topLevel, t.slotRoot)
	}
	switch n := stmt.(type) {
	case *ir.CanvasRedrawStmt:
		return t.translateCanvasRedraw(n)
	case *ir.CallStmt:
		if app := t.appSlotArg(n); app != nil {
			return []ir.Stmt{app}
		}
		if boxed := t.boxedSlotRenderCall(n); boxed != nil {
			return boxed
		}
		if local := localSlotRenderCall(n, t.isLocalRef); local != nil {
			return []ir.Stmt{local}
		}
	}
	return []ir.Stmt{stmt}
}

// boxedSlotRenderCall renders a slot whose container holds one child -- a
// scroll -- into a box that is that child, since the slot adds and removes
// any number of entries.
func (t *fyneTranslator) boxedSlotRenderCall(cs *ir.CallStmt) []ir.Stmt {
	if cs.Call == nil || cs.Call.Func == nil || !cs.Call.Func.SlotRender || len(cs.Call.Args) != 1 {
		return nil
	}
	bare := codegen.IdentBareName(cs.Call.Args[0].Value)
	sp := t.specs[bare]
	if sp == nil || !sp.isSingleChild() {
		return nil
	}
	boxName := bare + "_box"
	t.fieldSink(boxName, "*fyne.Container")
	box := t.fieldRef(boxName)
	call := *cs.Call
	call.Args = []ir.CallArg{{Value: box}}
	return []ir.Stmt{
		&ir.Assign{Target: box, Op: ast.AssignSet,
			Value: nativeCallAt("fynelayout.SlotBox", fyneLayoutImportPath, []ir.Expr{box}, ir.TypDyn)},
		&ir.Assign{Target: &ir.Select{Operand: t.nodeRefFor(bare), Field: sp.Content, Type: ir.TypDyn}, Op: ast.AssignSet, Value: box},
		&ir.CallStmt{Call: &call},
	}
}

// localSlotRenderCall rewrites a render slot call whose container is a local
// of this scope -- a slot nothing but the ctor renders -- to pass the local,
// which the language context would otherwise spell as a field.
func localSlotRenderCall(cs *ir.CallStmt, isLocal func(string) bool) *ir.CallStmt {
	if cs.Call == nil || cs.Call.Func == nil || !cs.Call.Func.SlotRender || len(cs.Call.Args) != 1 {
		return nil
	}
	id, ok := cs.Call.Args[0].Value.(*ir.Ident)
	if !ok || !isLocal(id.Name) {
		return nil
	}
	call := *cs.Call
	call.Args = []ir.CallArg{{Name: cs.Call.Args[0].Name, Value: localElementRef(id.Name)}}
	return &ir.CallStmt{Call: &call}
}

// recordInvoker notes one (id, event) pair for the test-invoker methods emitted
// after the walk. A synthesized id is skipped: `c.__n0.click()` is not
// something a test can write.
func (t *fyneTranslator) recordInvoker(id, event string, h fyneHandler, handler ir.Expr) {
	if t.invokerSink == nil || id == "" || strings.HasPrefix(id, "__n") {
		return
	}
	inv := fyneEventInvoker{
		IDLabel:   id,
		SnglEvent: event,
		Field:     h.Field,
		Param:     h.Param,
		ParamType: fyneHandlerParamType(h),
	}
	if pt, sd := codegen.HandlerPayload(handler); sd != nil && inv.ParamType != "" {
		for _, f := range sd.Fields {
			if f != nil && f.Type != nil && f.Name != "value" && golang.IRTypeToGo(f.Type) == inv.ParamType {
				inv.Payload = golang.IRTypeToGo(pt)
				inv.PayloadField = golang.ExportName(f.Name)
				break
			}
		}
	}
	t.invokerSink(inv)
}

// fyneHandlerParamType reads the parameter's Go type out of the declared
// signature -- `func(s string)` gives "string". The declaration is the only
// place it is written down, and only the single-parameter shape occurs.
func fyneHandlerParamType(h fyneHandler) string {
	if h.Param == "" {
		return ""
	}
	open := strings.Index(h.Signature, "(")
	close := strings.LastIndex(h.Signature, ")")
	if open < 0 || close < open {
		return ""
	}
	parts := strings.Fields(h.Signature[open+1 : close])
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-1]
}

// emitFyneEventInvokers writes one Model method per (id, event) pair, invoking
// the widget's declared callback field.
func emitFyneEventInvokers(b *strings.Builder, invokers []fyneEventInvoker) {
	seen := map[string]bool{}
	for _, inv := range invokers {
		name := inv.IDLabel + golang.ExportName(inv.SnglEvent)
		if seen[name] {
			continue // duplicate id+event -- keep the first
		}
		seen[name] = true
		target := inv.IDLabel
		if inv.Target != "" {
			target = inv.Target
		}
		fmt.Fprintf(b, "// %s invokes the %s callback on the #%s widget; for tests.\n",
			name, inv.Field, inv.IDLabel)
		if inv.Payload != "" {
			fmt.Fprintf(b, "func (m *Model) %s(e %s) {\n", name, inv.Payload)
			fmt.Fprintf(b, "\tif m.%s.%s != nil {\n\t\tm.%s.%s(e.%s)\n\t}\n}\n\n",
				target, inv.Field, target, inv.Field, inv.PayloadField)
			continue
		}
		if inv.Param != "" && inv.ParamType != "" {
			// Detached while it runs: a widget calls it after taking the value,
			// so the handler's write-back of the bound var is a no-op there,
			// while here it would set a new value and fire the callback again.
			fmt.Fprintf(b, "func (m *Model) %s(%s %s) {\n", name, inv.Param, inv.ParamType)
			// A commit hands on text the entry already holds, since a user
			// cannot submit what they have not typed. Put it there first
			// without firing OnChanged, as gtk4's invoker does, or a
			// binding's write-back setting it would fire @input.
			if inv.Field == "OnSubmitted" && inv.ParamType == "string" {
				fmt.Fprintf(b, "\tif oc := m.%s.OnChanged; m.%s.Text != %s {\n\t\tm.%s.OnChanged = nil\n\t\tm.%s.SetText(%s)\n\t\tm.%s.OnChanged = oc\n\t}\n",
					target, target, inv.Param, target, target, inv.Param, target)
			}
			fmt.Fprintf(b, "\tif cb := m.%s.%s; cb != nil {\n\t\tm.%s.%s = nil\n\t\tcb(%s)\n\t\tm.%s.%s = cb\n\t}\n}\n\n",
				target, inv.Field, target, inv.Field, inv.Param, target, inv.Field)
			continue
		}
		fmt.Fprintf(b, "func (m *Model) %s() {\n", name)
		fmt.Fprintf(b, "\tif m.%s.%s != nil {\n\t\tm.%s.%s()\n\t}\n}\n\n",
			target, inv.Field, target, inv.Field)
	}
}

// structuralProp reports whether prop is one of this platform's own props
// rather than a value the widget shows.
//
// `spec` is the record naming the Fyne widget to construct, read by
// OnCreateNode; `style` is forwarded onto the widget root by the override
// bodies. Neither has a setter and neither should: they are how a declaration
// reaches the emitter, not something the emitter writes.
//
// Named here rather than left to fall through the setter lookup, because that
// lookup now reports what it cannot emit -- and these are the two it was
// always right to say nothing about.
func structuralProp(prop string) bool {
	return prop == "spec" || prop == "style"
}
