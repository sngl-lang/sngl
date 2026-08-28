package fyne

import (
	"context"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/canvasutil"
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
	// canvasState threads per-draw-func style-local naming for
	// canvasutil.GoContextStmts; lazily created on first canvas intrinsic.
	canvasState *canvasutil.GoCanvasState
}

func newFyneTranslator(gc *golang.GoIRContext, specs map[string]*fyneSpec, fieldSink func(name, goType string), importSink func(path string)) *fyneTranslator {
	return &fyneTranslator{
		gc:         gc,
		specs:      specs,
		fieldSink:  fieldSink,
		importSink: importSink,
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
// unchanged — e.g. std "strings", or a go:// native already carrying a path).
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
	ctor := nativeCallAt(sp.New.qualify(t.gc), sp.New.Path, sp.ctorArgs(), ir.TypDyn)
	if t.isLocalRef(id) {
		// Non-escaping: declare a function-local `__nN := <ctor>` so each
		// call frame (notably a recursive render method) keeps its own
		// widget temp rather than clobbering a shared Model field.
		return []ir.Stmt{&ir.LocalVar{Name: id, Init: ctor}}
	}
	return []ir.Stmt{&ir.Assign{
		Target: codegen.ModelFieldRef(id),
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
		Target: codegen.ModelFieldRef(id),
		Op:     ast.AssignSet,
		Value:  call,
	}}
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
	// assign to the field the Spec names instead.
	sp := t.specs[codegen.IdentBareName(parent)]
	parent = t.qualifyParentExpr(parent)
	child = t.qualifyChildExpr(child)
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
			return codegen.ModelFieldRef(id.Name)
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
			return codegen.ModelFieldRef(id.Name)
		}
	}
	return e
}

func (t *fyneTranslator) OnAttachHandler(ctx context.Context, node ir.Expr, event string, handler ir.Expr) []ir.Stmt {
	bareID := codegen.IdentBareName(node)
	sp, ok := t.specs[bareID]
	if !ok {
		return nil
	}
	h, ok := sp.Handlers[event]
	if !ok {
		return nil
	}
	fieldName := h.Field
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
	return codegen.ModelFieldRef(bareID)
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
			return codegen.ModelFieldRef(name)
		}
	}
	return e
}

func (t *fyneTranslator) OnPropAssign(ctx context.Context, node ir.Expr, prop string, value ir.Expr) []ir.Stmt {
	bareID := codegen.IdentBareName(node)
	sp, ok := t.specs[bareID]
	if !ok {
		return nil
	}
	methodName, ok := sp.Setters[prop]
	if !ok {
		return nil
	}
	nodeRef := t.nodeRefFor(bareID)
	return []ir.Stmt{&ir.CallStmt{Call: methodCall(nodeRef, methodName, []ir.Expr{value}, ir.TypVoid)}}
}

func (t *fyneTranslator) OnSlotReset(ctx context.Context, slot *ir.Var) []ir.Stmt {
	return []ir.Stmt{&ir.Assign{
		Target: codegen.ModelFieldRef(slot.Name),
		Op:     ast.AssignSet,
		Value:  &ir.Literal{Type: ir.TypNull},
	}}
}

func (t *fyneTranslator) OnSlotAppend(ctx context.Context, slot *ir.Var, child ir.Expr) []ir.Stmt {
	slotRef := codegen.ModelFieldRef(slot.Name)
	child = t.qualifyChildExpr(child)
	appendExpr := nativeCall("append", []ir.Expr{slotRef, child}, slot.Type)
	return []ir.Stmt{&ir.Assign{
		Target: codegen.ModelFieldRef(slot.Name),
		Op:     ast.AssignSet,
		Value:  appendExpr,
	}}
}

func (t *fyneTranslator) OnIter(ctx context.Context, iter ir.Expr) ir.Expr {
	if id, ok := iter.(*ir.Ident); ok && id.Synthesized {
		return codegen.ModelFieldRef(id.Name)
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
