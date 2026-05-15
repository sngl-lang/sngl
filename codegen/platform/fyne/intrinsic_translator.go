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
	idTags     map[string]string
	// topLevel tracks widget ids created via OnCreateNode that have not
	// (yet) been consumed by an AppendChild. Window-body/component-method
	// emission uses this to discover the topmost widget(s) to return as
	// the fyne.CanvasObject result. Slot-Func emission ignores it.
	topLevel []string
}

func newFyneTranslator(gc *golang.GoIRContext, blueprints map[string]*fyneBlueprint, fieldSink func(name, goType string)) *fyneTranslator {
	return &fyneTranslator{
		gc:         gc,
		blueprints: blueprints,
		fieldSink:  fieldSink,
		idTags:     map[string]string{},
	}
}

var _ codegen.IntrinsicTranslator = (*fyneTranslator)(nil)

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
		return &ir.Call{
			Type:     retType,
			Receiver: &ir.Ident{Name: pkg},
			Func:     &ir.Func{NativePkg: pkg, NativeName: nativeName},
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
	bp, ok := t.blueprints[tag]
	if !ok || bp.Constructor == nil || bp.Constructor.GoType == "" {
		return nil
	}
	t.fieldSink(id, bp.Constructor.GoType)
	t.idTags[id] = tag
	t.topLevel = append(t.topLevel, id)

	args := zeroArgsToExprs(bp.Constructor.ZeroArgs)
	ctor := nativeCall(bp.Constructor.GoFn, args, ir.TypDyn)
	return []ir.Stmt{&ir.Assign{
		Target: modelFieldRef(id),
		Op:     ast.AssignSet,
		Value:  ctor,
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
	parent = t.qualifyParentExpr(parent)
	child = t.qualifyChildExpr(child)
	return []ir.Stmt{&ir.CallStmt{Call: methodCall(parent, "Add", []ir.Expr{child}, ir.TypVoid)}}
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

// qualifyHandlerNode produces a Model-field ref for a node id.
func (t *fyneTranslator) qualifyHandlerNode(e ir.Expr, bareID string) ir.Expr {
	if bareID != "" {
		return modelFieldRef(bareID)
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
	nodeRef := modelFieldRef(bareID)
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
	return []ir.Stmt{stmt}
}
