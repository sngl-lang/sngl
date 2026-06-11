package fyne

import (
	"context"
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
	gc         *golang.GoIRContext
	blueprints map[string]*fyneBlueprint
	fieldSink  func(name, goType string)
	importSink func(path string)
	// localRefs is the set of synthesized widget ref ids that lower's
	// passNodeEscape determined do NOT escape this scope. For those ids
	// OnCreateNode/OnCreateComponent emit a function-local `__nN := ...`
	// rather than a shared Model field, and the qualify* helpers render the
	// bare local name. Escaping ids keep the Model-field behavior. nil →
	// every id is a field.
	localRefs map[string]bool
	idTags    map[string]string
	// topLevel tracks widget ids created via OnCreateNode that have not
	// (yet) been consumed by an AppendChild. Window-body/component-method
	// emission uses this to discover the topmost widget(s) to return as
	// the fyne.CanvasObject result. Slot-Func emission ignores it.
	topLevel []string

	// Canvas2D state. canvasByID/canvasByFunc map flattened canvas elements
	// (LocalVar.CanvasDraw) to their Model widget field + draw func, shared
	// into every translator that may create a canvas or emit a redraw. The
	// drawing itself is stateless here — canvasutil.GoContextStmts translates
	// each intrinsic into Context method calls.
	canvasByID   map[string]*canvasMeta
	canvasByFunc map[*ir.Func]*canvasMeta
}

func newFyneTranslator(gc *golang.GoIRContext, blueprints map[string]*fyneBlueprint, fieldSink func(name, goType string), importSink func(path string)) *fyneTranslator {
	return &fyneTranslator{
		gc:         gc,
		blueprints: blueprints,
		fieldSink:  fieldSink,
		importSink: importSink,
		idTags:     map[string]string{},
	}
}

var _ codegen.IntrinsicTranslator = (*fyneTranslator)(nil)

// withLocalRefs sets the non-escaping ref-id set for the scope this
// translator emits. See fyneTranslator.localRefs.
func (t *fyneTranslator) withLocalRefs(local map[string]bool) *fyneTranslator {
	t.localRefs = local
	return t
}

// isLocalRef reports whether id is a non-escaping ref that should be emitted
// as a function-local variable rather than a Model field.
func (t *fyneTranslator) isLocalRef(id string) bool {
	return t.localRefs != nil && t.localRefs[id]
}

// localElementRef renders a bare local-variable reference for a non-escaping
// widget id (used by the qualify* helpers and return trailers).
func localElementRef(name string) ir.Expr {
	return &ir.Ident{Name: name, Type: ir.TypDyn}
}

// platformBlueprints returns the blueprint table loaded at init().
func platformBlueprints() map[string]*fyneBlueprint {
	return loadBlueprints()
}

// modelFieldRef returns an *ir.Select that gc.EvalExpr renders as
// `m.<name>` — a reference to a Model struct field. Widget fields
// aren't registered in the type-checker scope, so we synthesize the
// `m.` qualifier here rather than relying on Resolve.
func modelFieldRef(name string) ir.Expr {
	return &ir.Select{
		Operand: &ir.Ident{Name: "m"},
		Field:   name,
		Type:    ir.TypDyn,
	}
}

// fyneFrameworkPkgs maps every fyne/std package selector the codegen emits
// (in blueprint goFn/goType and the view's literal fallbacks) to its full Go
// import path. It is the single source of truth for resolving a selector to a
// path: nativeCall uses it for NativePkg, and the import block uses it to scan
// the generated body for framework usage — replacing the old always-on set.
var fyneFrameworkPkgs = map[string]string{
	"fmt":       "fmt",
	"fyne":      "fyne.io/fyne/v2",
	"widget":    "fyne.io/fyne/v2/widget",
	"container": "fyne.io/fyne/v2/container",
	"canvas":    "fyne.io/fyne/v2/canvas",
	"layout":    "fyne.io/fyne/v2/layout",
}

// fyneImportPath returns the full Go import path for a fyne package selector,
// or "" if the selector isn't a known framework package (so callers leave it
// unchanged — e.g. std "strings", or a go:// native already carrying a path).
func fyneImportPath(sel string) string {
	if p, ok := fyneFrameworkPkgs[sel]; ok && p != sel {
		return p
	}
	return ""
}

// nativeCall builds a Call that gc.EvalExpr renders verbatim. For a
// dotted name (e.g. "widget.NewLabel"), uses the namespace path with
// NativePkg/NativeName so the Go renderer emits the qualified name
// directly. For a bare name (e.g. "append"), sets only Func.Name.
func nativeCall(nativeName string, args []ir.Expr, retType *ir.Type) *ir.Call {
	callArgs := make([]ir.CallArg, len(args))
	for i, a := range args {
		callArgs[i] = ir.CallArg{Value: a}
	}
	if dot := strings.Index(nativeName, "."); dot > 0 {
		pkg := nativeName[:dot]
		// NativePkg must be the full Go import path so platforms can collect
		// it (gc.Imports()); the selector in NativeName (e.g. "widget" in
		// "widget.NewLabel") stays the package name. Resolve the known fyne
		// selectors; std/other selectors pass through unchanged.
		nativePkg := pkg
		if full := fyneImportPath(pkg); full != "" {
			nativePkg = full
		}
		return &ir.Call{
			Type:     retType,
			Receiver: &ir.Ident{Name: pkg},
			Func:     &ir.Func{NativePkg: nativePkg, NativeName: nativeName},
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
	bp, ok := t.blueprints[tag]
	if !ok || bp.Constructor == nil || bp.Constructor.GoType == "" {
		return nil
	}
	t.idTags[id] = tag
	t.topLevel = append(t.topLevel, id)
	if !t.isLocalRef(id) {
		t.fieldSink(id, bp.Constructor.GoType)
	}
	// Only flow imports whose package name is referenced by the ctor
	// goFn/goType — blueprints may list extra imports (e.g. "net/url")
	// used by the unused prelude/Raw-arg path, which would otherwise
	// leak as unused imports.
	if t.importSink != nil {
		ref := bp.Constructor.GoFn + " " + bp.Constructor.GoType
		for _, imp := range bp.Constructor.Imports {
			if imp == "" {
				continue
			}
			pkg := imp
			if i := strings.LastIndex(imp, "/"); i >= 0 {
				pkg = imp[i+1:]
			}
			if strings.Contains(ref, pkg+".") {
				t.importSink(imp)
			}
		}
	}

	args := zeroArgsToExprs(bp.Constructor.ZeroArgs)
	ctor := nativeCall(bp.Constructor.GoFn, args, ir.TypDyn)
	if t.isLocalRef(id) {
		// Non-escaping: declare a function-local `__nN := <ctor>` so each
		// call frame (notably a recursive render method) keeps its own
		// widget temp rather than clobbering a shared Model field.
		return []ir.Stmt{&ir.LocalVar{Name: id, Init: ctor}}
	}
	return []ir.Stmt{&ir.Assign{
		Target: modelFieldRef(id),
		Op:     ast.AssignSet,
		Value:  ctor,
	}}
}

// OnCreateComponent promotes a recursive/non-inlinable user component
// instance (`__nX = lower.CreateComponent(...)`) to a Model field, so the
// `m.__nX` references emitted for it elsewhere (parent Add, etc.) resolve.
// The translated CreateComponent call becomes `m.render<Comp>(props...)`
// via the Go IR context when the returned Assign is later evaluated.
func (t *fyneTranslator) OnCreateComponent(ctx context.Context, id string, call *ir.Call) []ir.Stmt {
	t.topLevel = append(t.topLevel, id)
	if t.isLocalRef(id) {
		// Non-escaping: function-local `__nN := m.render<Comp>(...)` so each
		// recursion frame keeps its own child widget.
		return []ir.Stmt{&ir.LocalVar{Name: id, Init: call}}
	}
	t.fieldSink(id, "fyne.CanvasObject")
	return []ir.Stmt{&ir.Assign{
		Target: modelFieldRef(id),
		Op:     ast.AssignSet,
		Value:  call,
	}}
}

// zeroArgsToExprs parses a blueprint's ZeroArgs string into IR exprs.
// Recognises the small handful of forms blueprints actually use.
func zeroArgsToExprs(zeroArgs string) []ir.Expr {
	switch zeroArgs {
	case "":
		return nil
	case `""`:
		return []ir.Expr{&ir.Literal{Type: ir.TypString, Raw: ""}}
	case `"", nil`:
		return []ir.Expr{
			&ir.Literal{Type: ir.TypString, Raw: ""},
			&ir.Literal{Type: ir.TypNull},
		}
	case `nil`:
		return []ir.Expr{&ir.Literal{Type: ir.TypNull}}
	case `nil, nil`:
		return []ir.Expr{
			&ir.Literal{Type: ir.TypNull},
			&ir.Literal{Type: ir.TypNull},
		}
	}
	return nil
}

func (t *fyneTranslator) OnAppendChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	// A child that's been appended to a parent is no longer a top-level
	// candidate. Window/component emitters consult topLevel to decide
	// what to return.
	if id, ok := child.(*ir.Ident); ok && id.Synthesized {
		for i, name := range t.topLevel {
			if name == id.Name {
				t.topLevel = append(t.topLevel[:i], t.topLevel[i+1:]...)
				break
			}
		}
	}
	// Single-child containers (e.g. *container.Scroll) have no Add method;
	// assign to .Content instead. Detected via parent's tag → blueprint.
	parentTag := t.idTags[identBareName(parent)]
	parent = t.qualifyParentExpr(parent)
	child = t.qualifyChildExpr(child)
	if bp, ok := t.blueprints[parentTag]; ok && bp.Constructor != nil && isSingleChildContainerGoFn(bp.Constructor.GoFn) {
		return []ir.Stmt{&ir.Assign{
			Target: &ir.Select{Operand: parent, Field: "Content", Type: ir.TypDyn},
			Op:     ast.AssignSet,
			Value:  child,
		}}
	}
	return []ir.Stmt{&ir.CallStmt{Call: methodCall(parent, "Add", []ir.Expr{child}, ir.TypVoid)}}
}

// isSingleChildContainerGoFn reports whether the given fyne constructor
// produces a widget that stores its child via a `Content` field rather
// than an Add method. Used by OnAppendChild to switch from
// `parent.Add(child)` to `parent.Content = child` for those types.
func isSingleChildContainerGoFn(goFn string) bool {
	switch goFn {
	case "container.NewVScroll", "container.NewHScroll", "container.NewScroll":
		return true
	}
	return false
}

func (t *fyneTranslator) OnRemoveChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	parent = t.qualifyParentExpr(parent)
	child = t.qualifyChildExpr(child)
	return []ir.Stmt{&ir.CallStmt{Call: methodCall(parent, "Remove", []ir.Expr{child}, ir.TypVoid)}}
}

// qualifyParentExpr renames the slot-function parameter `parent` to the
// type-asserted `container` local — mirrors the typed-slot signature
// emitted by emitIRSlotFunc.
func (t *fyneTranslator) qualifyParentExpr(e ir.Expr) ir.Expr {
	if id, ok := e.(*ir.Ident); ok {
		if id.Name == "parent" {
			return &ir.Ident{Name: "container", Type: ir.TypDyn}
		}
		if t.isLocalRef(id.Name) {
			return localElementRef(id.Name)
		}
		// Synthesized __nN parents from inline AppendChild calls in
		// window/component bodies need an `m.` qualifier; slot Funcs use
		// the typed `container` param instead.
		if id.Synthesized && strings.HasPrefix(id.Name, "__n") {
			return modelFieldRef(id.Name)
		}
	}
	return e
}

// qualifyChildExpr re-qualifies a synthesized widget ident (e.g.
// "__n0") to a Model field reference. Non-synth idents (loop-key
// "__entry", etc.) and already-qualified expressions pass through.
func (t *fyneTranslator) qualifyChildExpr(e ir.Expr) ir.Expr {
	if id, ok := e.(*ir.Ident); ok {
		if t.isLocalRef(id.Name) {
			return localElementRef(id.Name)
		}
		if id.Synthesized && strings.HasPrefix(id.Name, "__n") {
			return modelFieldRef(id.Name)
		}
	}
	return e
}

func (t *fyneTranslator) OnAttachHandler(ctx context.Context, node ir.Expr, event string, handler ir.Expr) []ir.Stmt {
	bareID := identBareName(node)
	tag, ok := t.idTags[bareID]
	if !ok {
		return nil
	}
	bp, ok := t.blueprints[tag]
	if !ok {
		return nil
	}
	var target string
	for _, b := range bp.Bindings {
		if b.Kind == bindEvent && b.Prop == event {
			target = b.Target
			break
		}
	}
	if target == "" {
		return nil
	}
	fieldName := strings.TrimPrefix(target, ".")
	// Qualify node + handler to Model references when synthesized/promoted.
	nodeRef := t.qualifyHandlerNode(node, bareID)
	handlerRef := t.qualifyHandlerFunc(handler)
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

// nodeRefFor returns the reference expression for a synthesized widget id:
// a bare local for non-escaping ids, else a Model-field selector.
func (t *fyneTranslator) nodeRefFor(bareID string) ir.Expr {
	if t.isLocalRef(bareID) {
		return localElementRef(bareID)
	}
	return modelFieldRef(bareID)
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
			return modelFieldRef(name)
		}
	}
	return e
}

func (t *fyneTranslator) OnPropAssign(ctx context.Context, node ir.Expr, prop string, value ir.Expr) []ir.Stmt {
	bareID := identBareName(node)
	tag, ok := t.idTags[bareID]
	if !ok {
		return nil
	}
	bp, ok := t.blueprints[tag]
	if !ok {
		return nil
	}
	var target, transform string
	for _, b := range bp.Bindings {
		if (b.Kind == bindReactive || b.Kind == bindInit) && b.Prop == prop {
			target = b.Target
			transform = b.Transform
			break
		}
	}
	if target == "" {
		return nil
	}
	methodName := strings.TrimPrefix(target, ".")
	if transform != "" {
		value = nativeCall(transform, []ir.Expr{value}, ir.TypString)
	}
	nodeRef := t.nodeRefFor(bareID)
	return []ir.Stmt{&ir.CallStmt{Call: methodCall(nodeRef, methodName, []ir.Expr{value}, ir.TypVoid)}}
}

// identBareName returns the unqualified name of an Ident, stripping
// any "m." prefix that came pre-qualified.
func identBareName(e ir.Expr) string {
	if id, ok := e.(*ir.Ident); ok {
		return strings.TrimPrefix(id.Name, "m.")
	}
	return ""
}

func (t *fyneTranslator) OnSlotReset(ctx context.Context, slot *ir.Var) []ir.Stmt {
	return []ir.Stmt{&ir.Assign{
		Target: modelFieldRef(slot.Name),
		Op:     ast.AssignSet,
		Value:  &ir.Literal{Type: ir.TypNull},
	}}
}

func (t *fyneTranslator) OnSlotAppend(ctx context.Context, slot *ir.Var, child ir.Expr) []ir.Stmt {
	slotRef := modelFieldRef(slot.Name)
	child = t.qualifyChildExpr(child)
	appendExpr := nativeCall("append", []ir.Expr{slotRef, child}, slot.Type)
	return []ir.Stmt{&ir.Assign{
		Target: modelFieldRef(slot.Name),
		Op:     ast.AssignSet,
		Value:  appendExpr,
	}}
}

func (t *fyneTranslator) OnIter(ctx context.Context, iter ir.Expr) ir.Expr {
	if id, ok := iter.(*ir.Ident); ok && id.Synthesized {
		return modelFieldRef(id.Name)
	}
	return iter
}

func (t *fyneTranslator) OnCond(ctx context.Context, cond ir.Expr) ir.Expr {
	return cond
}

func (t *fyneTranslator) OnDefault(ctx context.Context, stmt ir.Stmt) []ir.Stmt {
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
