package fyne

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/ir"
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
	specs     map[string]*fyneSpec
	fieldSink func(name, goType string)
	// fieldGoTypes is what sinkField declared each field this translator
	// writes as, which is the type a reference to it carries.
	fieldGoTypes map[string]string
	importSink   func(path string)
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
	// rootRenders are the calls written directly in this scope's body that
	// render a slot into the scope's own container; renderedRoot is that
	// container once one has run. Every slot in a body renders into the one
	// container, so the body's own widgets go into it too, in written order,
	// or a widget between two slots lands after both. Empty in a scope whose
	// root is decided some other way, which is every Model scope: the Model's
	// own __root is the wrapper its BuildUI already returns.
	rootRenders  map[ir.Stmt]bool
	renderedRoot ir.Expr
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
	// indexed are the ids a test triggers one copy of (`c.row[1]`), whose
	// widgets this Model scope keeps by copy (copiesField): put there by
	// each write of a copy's key (listCopy), and emptied where a slot render
	// starts over. nil in a scope that keeps no such map.
	indexed map[string]bool
	// slotNamed are the indexed ids this slot render func creates, which its
	// reset empties.
	slotNamed []string
	// instanceCopies are the ids a test reaches one copy of whose instances,
	// built at run time, this scope lists by root in a Model map, declared
	// through modelSink: a record's own fields are not the Model's. No slot
	// reset empties the map, since one map holds every record's instances:
	// a re-key replaces an instance's entry, and a destroyed record unlists
	// itself.
	instanceCopies map[string]bool
	modelSink      func(name, goType string)
	// record is set in a component built at run time, which lists the roots
	// of the spliced instances it renders (listRoot) so its Destroy can take
	// them out again.
	record bool
	// present are the ids a test null-tests. slotPresent are the fields of
	// those this slot render func creates -- a widget's own, an instance's
	// presentField -- which its reset clears: what the render no longer builds
	// is not rendered, though its field still holds it.
	present     map[string]bool
	slotPresent []string
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
	return codegen.RecvFieldRef(t.gc.NodeRecvRef(name), name, t.fieldType(name))
}

// sinkField declares a field of the Go type goType, and remembers it as the
// type a reference to the field carries.
func (t *fyneTranslator) sinkField(name, goType string) {
	if t.fieldGoTypes == nil {
		t.fieldGoTypes = map[string]string{}
	}
	t.fieldGoTypes[name] = goType
	t.fieldSink(name, goType)
}

// fieldType is the host type of the field name: what it was declared as, or
// for a node another scope created, its spec's widget type.
func (t *fyneTranslator) fieldType(name string) *ir.Type {
	if ft, ok := t.declaredType(name); ok {
		return ft
	}
	panic(fmt.Sprintf("fyne: the field %s was declared by no translator", name))
}

func (t *fyneTranslator) declaredType(name string) (*ir.Type, bool) {
	if goType, ok := t.fieldGoTypes[name]; ok {
		return ir.NativeGoNamed(goType), true
	}
	if sp := t.specs[name]; sp != nil && sp.GoType.Name != "" {
		return ir.NativeGoNamed(sp.GoType.qualify(t.gc)), true
	}
	return nil, false
}

// nodeFieldRef is fieldRef for a reference the lowered IR made, which
// another scope may have declared: typed as the field was declared where this
// translator knows it, and as the reference is otherwise.
func (t *fyneTranslator) nodeFieldRef(id *ir.Ident) ir.Expr {
	ft, ok := t.declaredType(id.Name)
	if !ok {
		ft = id.Type
	}
	return codegen.RecvFieldRef(t.gc.NodeRecvRef(id.Name), id.Name, ft)
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
	// Params are the callback's, from its Spec signature: what a test hands
	// the invoker, which are the event's own arguments.
	Params []*ir.Param
	// Target is the Model field holding Field when it is not the #id's own
	// widget -- a canvas's Tap. "" means IDLabel.
	Target string
	// List says a test indexes the id, so its copies are kept by key
	// (copiesField) and an `At` invoker drives the one it is handed the key of.
	List bool
}

func (t *fyneTranslator) withInvokerSink(sink func(fyneEventInvoker)) *fyneTranslator {
	t.invokerSink = sink
	return t
}

// withIndexed has this Model scope keep the widgets of the ids a test indexes;
// stmts is the scope's body, whose creates of them a slot's reset empties.
func (t *fyneTranslator) withIndexed(indexed, present map[string]bool, stmts []ir.Stmt) *fyneTranslator {
	t.indexed = indexed
	t.slotNamed = nil
	t.slotPresent = nil
	t.present = present
	_ = ir.Walk(stmts, func(n ir.Node) error {
		lv, ok := n.(*ir.LocalVar)
		if !ok {
			return nil
		}
		c, ok := lv.Init.(*ir.Call)
		if !ok || c.Func == nil {
			return nil
		}
		switch c.Func.Intrinsic {
		case ir.NodeOpCreateNode:
			if indexed[lv.Name] {
				t.slotNamed = append(t.slotNamed, lv.Name)
			}
			if t.present[lv.Name] {
				t.slotPresent = append(t.slotPresent, lv.Name)
			}
		case ir.NodeOpComponentRoot:
			inst := codegen.IdentBareName(c.Args[0].Value)
			if t.present[inst] {
				t.slotPresent = append(t.slotPresent, presentField(inst))
			}
		}
		return nil
	})
	return t.withInstanceCopies(indexed, t.sinkField)
}

// withRecordPresent has this scope of a component built at run time hand the
// widgets of the ids a test null-tests to the Model (namedWidgetField), which
// a test reaches no record through; stmts is the scope's body, whose creates
// of them a slot's reset takes back.
func (t *fyneTranslator) withRecordPresent(present map[string]bool, stmts []ir.Stmt) *fyneTranslator {
	t.present = present
	t.slotPresent = nil
	_ = ir.Walk(stmts, func(n ir.Node) error {
		lv, ok := n.(*ir.LocalVar)
		if !ok {
			return nil
		}
		c, ok := lv.Init.(*ir.Call)
		if !ok || c.Func == nil {
			return nil
		}
		switch c.Func.Intrinsic {
		case ir.NodeOpCreateNode:
			if present[lv.Name] {
				t.slotPresent = append(t.slotPresent, lv.Name)
			}
		case ir.NodeOpComponentRoot:
			if inst := codegen.IdentBareName(c.Args[0].Value); present[inst] {
				t.slotPresent = append(t.slotPresent, presentField(inst))
			}
		}
		return nil
	})
	return t
}

// modelPresentRef is the Model field holding the widget a test null-tests by
// id, for a record's scope: the field a Model scope's own widget is.
func (t *fyneTranslator) modelPresentRef(field string) ir.Expr {
	return codegen.RecvFieldRef(&ir.Ident{Name: t.gc.ModelRef()}, field, ir.NativeGoNamed(fyneWidgetHandleType))
}

// withInstanceCopies has this scope list the instances of the ids a test
// reaches one copy of, declaring each map through modelSink.
func (t *fyneTranslator) withInstanceCopies(ids map[string]bool, modelSink func(name, goType string)) *fyneTranslator {
	t.instanceCopies, t.modelSink = ids, modelSink
	return t
}

// inRecord marks this scope as the body or a method of a component built at
// run time.
func (t *fyneTranslator) inRecord() *fyneTranslator {
	t.record = true
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

func (t *fyneTranslator) withSlotRoot(name string, body []ir.Stmt) *fyneTranslator {
	t.rootRenders = codegen.RootSlotRenders(body, name)
	return t
}

// addTopsTo places the top-level widgets written so far into root, ahead of
// the slot render that follows: a slot's first render appends at the end of
// its container, and lowering finishes a node's subtree before anything later.
func (t *fyneTranslator) addTopsTo(root ir.Expr) []ir.Stmt {
	stmts := make([]ir.Stmt, 0, len(t.topLevel))
	for _, ref := range t.topLevel {
		stmts = append(stmts, &ir.CallStmt{Call: methodCall(root, "Add", []ir.Expr{topRef(t, ref)}, ir.TypVoid)})
	}
	t.topLevel = nil
	return stmts
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
// widget id (used by the qualify* helpers and return trailers), of the host
// type the field would have had.
func (t *fyneTranslator) localElementRef(name string) ir.Expr {
	return &ir.Ident{Name: name, Type: t.fieldType(name)}
}

// localRef is localElementRef for a reference the lowered IR made, typed as
// the reference is where this translator did not declare it.
func (t *fyneTranslator) localRef(id *ir.Ident) ir.Expr {
	ft, ok := t.declaredType(id.Name)
	if !ok {
		ft = id.Type
	}
	return &ir.Ident{Name: id.Name, Type: ft}
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
	if m := t.canvasByID[id]; m != nil && isImageDrawing(m.Node) {
		return t.emitImageDrawingCreate(id)
	}
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
		t.sinkField(id, goType)
	}
	ctor := nativeCallAt(sp.New.qualify(t.gc), sp.New.Path, sp.ctorArgs(t.gc), ir.NativeGoNamed(sp.GoType.qualify(t.gc)))
	if t.isLocalRef(id) {
		// Non-escaping: declare a function-local `__nN := <ctor>` so each
		// call frame (notably a recursive render method) keeps its own
		// widget temp rather than clobbering a shared Model field.
		return t.withBox(id, sp, &ir.LocalVar{Name: id, Init: ctor})
	}
	create := t.withBox(id, sp, &ir.Assign{
		Target: t.fieldRef(id),
		Op:     ast.AssignSet,
		Value:  ctor,
	})
	if t.indexed[id] {
		t.sinkField(copiesField(id), copyMapType(goType))
	}
	if t.record && t.present[id] {
		named := namedWidgetField(id)
		t.modelSink(named, fyneWidgetHandleType)
		create = append(create, &ir.Assign{Target: t.modelPresentRef(named), Op: ast.AssignSet, Value: t.fieldRef(id)})
	}
	return create
}

func (t *fyneTranslator) withBox(id string, sp *fyneSpec, create ir.Stmt) []ir.Stmt {
	if !sp.Boxed {
		return []ir.Stmt{create}
	}
	return []ir.Stmt{create, t.createBox(id, t.nodeRefFor(id))}
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
	t.sinkField(id, t.instanceGoType(call))
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
func (t *fyneTranslator) OnComponentRoot(ctx context.Context, id string, inst, key ir.Expr) []ir.Stmt {
	t.topLevel = append(t.topLevel, id)
	var root ir.Expr = t.instanceRef(inst)
	if !t.plainHandle[codegen.IdentBareName(inst)] {
		// Only a record has a Root field. Where the build renders the
		// component as a method of the enclosing scope instead, the handle IS
		// the widget it returned -- `__n0.Root` on a fyne.CanvasObject names
		// no field the emitted file has.
		root = &ir.Select{Type: fyneObject(), Operand: root, Field: golang.ComponentRootField}
	}
	// An instance a test null-tests hands its root to the Model while it is
	// rendered, as a widget is its own field.
	var present []ir.Stmt
	if name := codegen.IdentBareName(inst); t.present[name] {
		field := presentField(name)
		var target ir.Expr
		if t.record {
			t.modelSink(field, fyneWidgetHandleType)
			target = t.modelPresentRef(field)
		} else {
			t.sinkField(field, fyneWidgetHandleType)
			target = t.fieldRef(field)
		}
		present = append(present, &ir.Assign{Target: target, Op: ast.AssignSet, Value: root})
	}
	// One copy of it is found by the key it stands in now, which a reused
	// instance was handed before its root is bound again.
	if name := codegen.IdentBareName(inst); key != nil && t.instanceCopies[name] {
		t.modelSink(copiesField(name), copyMapType(fyneWidgetHandleType))
		present = append(present, t.listInstanceCopy(name, key, root))
	}
	if hold := t.holdInstance(inst, key); hold != nil {
		present = append(present, hold)
	}
	if t.isLocalRef(id) {
		return append([]ir.Stmt{&ir.LocalVar{Name: id, Init: root}}, present...)
	}
	t.sinkField(id, fyneWidgetHandleType)
	return append([]ir.Stmt{&ir.Assign{Target: t.fieldRef(id), Op: ast.AssignSet, Value: root}}, present...)
}

// holdInstance is the Model's hold on the record inst names, where a test
// reads a member through it (golang.HoldInstance), or nil.
func (t *fyneTranslator) holdInstance(inst, key ir.Expr) ir.Stmt {
	name := codegen.IdentBareName(inst)
	r, ok := t.gc.InstanceRead(name)
	if !ok || t.plainHandle[name] {
		return nil
	}
	if !r.Keyed {
		key = nil
	} else if key == nil {
		return nil
	}
	return golang.HoldInstance(&ir.Ident{Name: t.gc.ModelRef()}, name, r.Comp, t.instanceRef(inst), key)
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
			[]ir.Expr{t.qualifyParentExpr(parent), t.slotAnchor, t.shown(t.qualifyChildExpr(child), childSpec, codegen.IdentBareName(child))}, ir.TypVoid)}}
	}
	unqualified := parent
	parent = t.qualifyParentExpr(parent)
	child = t.shown(t.qualifyChildExpr(child), childSpec, codegen.IdentBareName(child))
	if sp != nil && sp.isSingleChild() {
		// A select's operand is written as it stands, so a parent the
		// program named -- `ui.scroll #list` -- came out a bare `list` no
		// scope declares. It is named as a handler names its node.
		if parent == unqualified {
			parent = t.qualifyHandlerNode(parent, codegen.IdentBareName(parent))
		}
		return []ir.Stmt{&ir.Assign{
			Target: &ir.Select{Operand: parent, Field: sp.Content, Type: fyneObject()},
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
	return append(t.removeChild(parent, child), t.unlistRemoved(child)...)
}

func (t *fyneTranslator) removeChild(parent, child ir.Expr) []ir.Stmt {
	child = t.shown(t.qualifyChildExpr(child), t.specs[codegen.IdentBareName(child)], codegen.IdentBareName(child))
	if ir.IsAppParent(parent) {
		return []ir.Stmt{&ir.CallStmt{Call: nativeCallAt("fynelayout.AppDetach", fyneLayoutImportPath,
			[]ir.Expr{child}, ir.TypVoid)}}
	}
	// A render slot's container may be the application, which only the
	// runtime can tell from the value it is handed.
	if id, ok := parent.(*ir.Ident); ok && id.Name == "parent" {
		return []ir.Stmt{&ir.CallStmt{Call: nativeCallAt("fynelayout.Remove", fyneLayoutImportPath,
			[]ir.Expr{t.qualifyParentExpr(parent), child}, ir.TypVoid)}}
	}
	parent = t.qualifyParentExpr(parent)
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
			return &ir.Ident{Name: slotParentParam, Type: ir.NativeGoPointerOf("fyne.Container")}
		}
		if t.isLocalRef(id.Name) {
			return t.localRef(id)
		}
		// Synthesized __nN parents from inline AppendChild calls in
		// window/component bodies need an `m.` qualifier; slot Funcs use
		// the typed slotParentParam instead.
		if id.Synthesized && strings.HasPrefix(id.Name, "__n") {
			return t.nodeFieldRef(id)
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
			return t.localRef(id)
		}
		if id.Synthesized && strings.HasPrefix(id.Name, "__n") {
			return t.nodeFieldRef(id)
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
	t.recordInvoker(bareID, codegen.TriggerEventName(handler, event), h)
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
			Type:    ir.NativeGoNamed(h.Signature),
		},
		Op:    ast.AssignSet,
		Value: handlerRef,
	}}
}

// fyneCallbackLambda gives a handler written in place -- in an instance
// record, where nothing promotes it to a method -- the Go signature of the
// callback field it is assigned to, the way emitIRPromotedHandler does for a
// promoted one.
func fyneCallbackLambda(lam *ir.Lambda, h fyneHandler, event string) ir.Expr {
	if lam.Func == nil || h.Signature == "" {
		return lam
	}
	params, err := signatureParams("", event, h.Signature)
	if err != nil {
		panic("fyne: " + err.Error())
	}
	fn := *lam.Func
	fn.Params = namedAsHandler(params, lam.Func.Params)
	out := *lam
	out.Func = &fn
	return &out
}

// namedAsHandler is a callback's parameters as the SNGL handler declared them.
// The two lists are one event's parameters, position for position --
// `@toggle(flag bool)` is `func(b bool)` -- so the handler's own parameter is
// the callback's, the declaration itself and not a copy: its body's reads name
// it by symbol, and a copy the Go context did not know as a local resolved by
// name instead, to a component field that happened to share it. Its type is
// the event's, which the Spec's signature spells in Go. A parameter the
// handler left unbound is the Spec's.
func namedAsHandler(goParams, sngl []*ir.Param) []*ir.Param {
	out := make([]*ir.Param, len(goParams))
	for i, p := range goParams {
		if i < len(sngl) && sngl[i] != nil && sngl[i].Name != "" {
			out[i] = sngl[i]
			continue
		}
		out[i] = p
	}
	return out
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
			Type:    ir.NativeGoNamed(h.Signature),
		},
		Op:    ast.AssignSet,
		Value: &ir.Literal{Type: ir.TypNull},
	}}
}

// nodeRefFor returns the reference expression for a synthesized widget id:
// a bare local for non-escaping ids, else a Model-field selector.
func (t *fyneTranslator) nodeRefFor(bareID string) ir.Expr {
	if t.isLocalRef(bareID) {
		return t.localElementRef(bareID)
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
			return t.nodeFieldRef(id)
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
	if prop == lookPropName {
		if !sp.Boxed {
			return codegen.KeepLocalsRead(value)
		}
		return []ir.Stmt{&ir.CallStmt{Call: methodCall(t.boxRef(bareID), "SetLook", []ir.Expr{codegen.IRWrittenFields(value)}, ir.TypVoid)}}
	}
	if prop == ir.CopyProp {
		return []ir.Stmt{t.listCopy(bareID, value)}
	}
	if id, ok := codegen.InstanceRootID(prop); ok {
		return t.listRoot(id, t.nodeRefFor(bareID), value)
	}
	if prop == specPropName {
		return codegen.KeepLocalsRead(value)
	}
	// The constructor answers the write it was built from, and a copy of
	// that write a pass made: the expression, not the pointer, is the value.
	if sp.CtorOnly[prop] && reflect.DeepEqual(value, sp.CtorProps[prop]) {
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
	args := []ir.Expr{value}
	if split := sp.SetterArgs[prop]; split != nil {
		args = split(value)
	}
	set := &ir.CallStmt{Call: methodCall(nodeRef, methodName, args, ir.TypVoid)}
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
	// A slot's fields are the objects it holds and the anchor it inserts them
	// before, as the compiler declares them.
	object := ir.NativeGoNamed("fyne.CanvasObject")
	t.slotAnchor = codegen.RecvFieldRef(t.gc.NodeRecvRef(slot.Name), codegen.SlotAnchorField(slot.Name), object)
	anchor := nativeCallAt("fynelayout.SlotAnchor", fyneLayoutImportPath,
		[]ir.Expr{t.qualifyParentExpr(&ir.Ident{Name: "parent"}), t.slotAnchor}, object)
	reset := []ir.Stmt{
		&ir.Assign{
			Target: t.slotFieldRef(slot),
			Op:     ast.AssignSet,
			Value:  &ir.Literal{Type: ir.TypNull},
		},
		&ir.Assign{Target: t.slotAnchor, Op: ast.AssignSet, Value: anchor},
	}
	// The render builds every copy again, so the map of them starts over with
	// it.
	for _, id := range t.slotNamed {
		list := copiesField(id)
		reset = append(reset, &ir.Assign{Target: codegen.RecvFieldRef(t.gc.NodeRecvRef(list), list, t.copiesType(id)), Op: ast.AssignSet, Value: &ir.Literal{Type: ir.TypNull}})
	}
	for _, id := range t.slotPresent {
		if t.isLocalRef(id) {
			continue
		}
		if t.record {
			reset = append(reset, t.releasePresent(id))
			continue
		}
		reset = append(reset, &ir.Assign{Target: codegen.RecvFieldRef(t.gc.NodeRecvRef(id), id, ir.NativeGoNamed("fyne.CanvasObject")), Op: ast.AssignSet, Value: &ir.Literal{Type: ir.TypNull}})
	}
	return reset
}

// releasePresent takes back what a record handed the Model for a test's null
// test of field -- a node's id, or an instance's presentField. A node is taken
// back only where the Model still holds this record's widget: another record
// may have handed its own since.
func (t *fyneTranslator) releasePresent(field string) ir.Stmt {
	if strings.HasPrefix(field, presentField("")) {
		return &ir.Assign{Target: t.modelPresentRef(field), Op: ast.AssignSet, Value: &ir.Literal{Type: ir.TypNull}}
	}
	held := t.modelPresentRef(namedWidgetField(field))
	return &ir.If{
		Cond: &ir.Binary{Type: ir.TypBool, Op: ast.BinEq, Left: held, Right: t.fieldRef(field)},
		Body: []ir.Stmt{&ir.Assign{Target: t.modelPresentRef(namedWidgetField(field)), Op: ast.AssignSet, Value: &ir.Literal{Type: ir.TypNull}}},
	}
}

func (t *fyneTranslator) OnSlotAppend(ctx context.Context, slot *ir.Var, child ir.Expr) []ir.Stmt {
	slotRef := t.slotFieldRef(slot)
	if sp := t.specs[codegen.IdentBareName(child)]; sp != nil && sp.Boxed {
		child = t.boxRef(codegen.IdentBareName(child))
	} else {
		child = t.qualifyChildExpr(child)
	}
	appendExpr := nativeCall("append", []ir.Expr{slotRef, child}, slot.Type)
	return []ir.Stmt{&ir.Assign{
		Target: t.slotFieldRef(slot),
		Op:     ast.AssignSet,
		Value:  appendExpr,
	}}
}

// OnIter reads a slot's list, a field the scope declares, through the
// receiver. Any other name a pass made is a local of the code it is in: the
// accumulator a `map` or `filter` builds, which a second one ranges over.
func (t *fyneTranslator) OnIter(ctx context.Context, iter ir.Expr) ir.Expr {
	if id, ok := iter.(*ir.Ident); ok && id.Synthesized && (id.IsElementRef || ir.IsSlotVarName(id.Name)) {
		return t.nodeFieldRef(id)
	}
	return iter
}

func (t *fyneTranslator) OnCond(ctx context.Context, cond ir.Expr) ir.Expr {
	return cond
}

func (t *fyneTranslator) OnDefault(ctx context.Context, stmt ir.Stmt) []ir.Stmt {
	if t.rootRenders[stmt] {
		t.renderedRoot = stmt.(*ir.CallStmt).Call.Args[0].Value
		return append(t.addTopsTo(t.renderedRoot), t.translateDefault(stmt)...)
	}
	return t.translateDefault(stmt)
}

func (t *fyneTranslator) translateDefault(stmt ir.Stmt) []ir.Stmt {
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
	if cs.Call == nil || cs.Call.Func == nil || !cs.Call.Func.Lowered.SlotRender || len(cs.Call.Args) != 1 {
		return nil
	}
	bare := codegen.IdentBareName(cs.Call.Args[0].Value)
	sp := t.specs[bare]
	if sp == nil || !sp.isSingleChild() {
		return nil
	}
	boxName := bare + "_box"
	t.sinkField(boxName, "*fyne.Container")
	box := t.fieldRef(boxName)
	call := *cs.Call
	call.Args = []ir.CallArg{{Value: box}}
	return []ir.Stmt{
		&ir.Assign{Target: box, Op: ast.AssignSet,
			Value: nativeCallAt("fynelayout.SlotBox", fyneLayoutImportPath, []ir.Expr{box}, ir.NativeGoPointerOf("fyne.Container"))},
		&ir.Assign{Target: &ir.Select{Operand: t.nodeRefFor(bare), Field: sp.Content, Type: fyneObject()}, Op: ast.AssignSet, Value: box},
		&ir.CallStmt{Call: &call},
	}
}

// localSlotRenderCall rewrites a render slot call whose container is a local
// of this scope -- a slot nothing but the ctor renders -- to pass the local,
// which the language context would otherwise spell as a field.
func localSlotRenderCall(cs *ir.CallStmt, isLocal func(string) bool) *ir.CallStmt {
	if cs.Call == nil || cs.Call.Func == nil || !cs.Call.Func.Lowered.SlotRender || len(cs.Call.Args) != 1 {
		return nil
	}
	id, ok := cs.Call.Args[0].Value.(*ir.Ident)
	if !ok || !isLocal(id.Name) {
		return nil
	}
	call := *cs.Call
	call.Args = []ir.CallArg{{Name: cs.Call.Args[0].Name, Value: &ir.Ident{Name: id.Name, Type: id.Type}}}
	return &ir.CallStmt{Call: &call}
}

// recordInvoker notes one (id, event) pair for the test-invoker methods emitted
// after the walk. A synthesized id is skipped: `c.__n0.click()` is not
// something a test can write.
func (t *fyneTranslator) recordInvoker(id, event string, h fyneHandler) {
	if t.invokerSink == nil || id == "" || strings.HasPrefix(id, "__n") {
		return
	}
	params, err := signatureParams("", event, h.Signature)
	if err != nil {
		panic("fyne: " + err.Error())
	}
	inv := fyneEventInvoker{
		IDLabel:   id,
		SnglEvent: event,
		Field:     h.Field,
		Params:    params,
	}
	inv.List = t.indexed[id]
	t.invokerSink(inv)
}

// emitFyneEventInvokers writes one Model method per (id, event) pair, invoking
// the widget's declared callback field, and for a widget whose copies are
// kept by key an `At` method driving the copy with a key.
func emitFyneEventInvokers(b *strings.Builder, invokers []fyneEventInvoker) {
	seen := map[string]bool{}
	keyed := map[string]bool{}
	for _, inv := range invokers {
		keyed[inv.IDLabel] = keyed[inv.IDLabel] || inv.List
	}
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
		emitFyneInvoker(b, inv, name, nil, "m."+target)
		if !keyed[inv.IDLabel] {
			continue
		}
		fmt.Fprintf(b, "// %s invokes it on the copy of #%s with the key; for tests.\n",
			codegen.InvokerAt(name), inv.IDLabel)
		emitFyneInvoker(b, inv, codegen.InvokerAt(name), []string{"key string"}, fmt.Sprintf("m.%s[key]", copiesField(inv.IDLabel)))
	}
}

// emitFyneInvoker writes one invoker, driving the widget w spells.
func emitFyneInvoker(b *strings.Builder, inv fyneEventInvoker, name string, lead []string, w string) {
	decl := append([]string{}, lead...)
	var call []string
	for _, p := range inv.Params {
		decl = append(decl, p.Name+" "+golang.IRTypeToGo(p.Type))
		call = append(call, p.Name)
	}
	fmt.Fprintf(b, "func (m *Model) %s(%s) {\n", name, strings.Join(decl, ", "))
	if len(lead) > 0 {
		fmt.Fprintf(b, "\tif %s == nil {\n\t\treturn\n\t}\n", w)
	}
	if len(inv.Params) == 0 {
		fmt.Fprintf(b, "\tif %s.%s != nil {\n\t\t%s.%s()\n\t}\n}\n\n", w, inv.Field, w, inv.Field)
		return
	}
	// A commit hands on text the entry already holds, since a user cannot
	// submit what they have not typed. Put it there first without firing
	// OnChanged, as gtk4's invoker does, or a binding's write-back setting it
	// would fire @input.
	if inv.Field == "OnSubmitted" && len(inv.Params) == 1 && golang.IRTypeToGo(inv.Params[0].Type) == "string" {
		v := inv.Params[0].Name
		fmt.Fprintf(b, "\tif oc := %s.OnChanged; %s.Text != %s {\n\t\t%s.OnChanged = nil\n\t\t%s.SetText(%s)\n\t\t%s.OnChanged = oc\n\t}\n",
			w, w, v, w, w, v, w)
	}
	// Detached while it runs: a widget calls it after taking the value, so the
	// handler's write-back of the bound var is a no-op there, while here it
	// would set a new value and fire the callback again.
	fmt.Fprintf(b, "\tif cb := %s.%s; cb != nil {\n\t\t%s.%s = nil\n\t\tcb(%s)\n\t\t%s.%s = cb\n\t}\n}\n\n",
		w, inv.Field, w, inv.Field, strings.Join(call, ", "), w, inv.Field)
}

// fyneObject is fyne.CanvasObject, what a container holds and a record's Root
// is.
func fyneObject() *ir.Type { return ir.NativeGoNamed("fyne.CanvasObject") }

// slotFieldRef is the field a render slot's var is held in.
func (t *fyneTranslator) slotFieldRef(slot *ir.Var) ir.Expr {
	return codegen.RecvFieldRef(t.gc.NodeRecvRef(slot.Name), slot.Name, slot.Type)
}

// copiesType is the type of copiesField(id).
func (t *fyneTranslator) copiesType(id string) *ir.Type {
	if ft, ok := t.declaredType(copiesField(id)); ok {
		return ft
	}
	return ir.NativeGoNamed(copyMapType(t.fieldType(id).Meta.(ir.NativeTypeRef).Name))
}
