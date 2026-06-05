package html

import (
	"context"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/javascript"
	"git.duckfam.us/jonathan/sngl/ir"
)

// htmlTranslator implements codegen.IntrinsicTranslator for html.
// Produces ir.Stmt fragments that the JS renderer (jc.EvalStmt)
// emits as JavaScript inside the page's bootstrap <script> block.
//
// Unlike fyne/gtk4: no Model receiver. Node refs are JS top-level
// consts initialized via document.createElement(...). State vars
// live on a `state` object: `state.<name>` (handled by the JS
// renderer's evalIdent state-var path).
type htmlTranslator struct {
	jc       *javascript.JsIRContext
	idTags   map[string]string // id ("__n0") → SNGL tag ("text")
	topLevel []string          // ids not yet AppendChild'd
	// idToNode maps an element id to the originating NodeInst so
	// OnPropAssign can translate SNGL component props (e.g. text.value)
	// to the correct DOM property (textContent). Caller-supplied; nil
	// is fine — OnPropAssign falls back to the DOM-name fast path.
	idToNode map[string]*ir.NodeInst
}

func newHTMLTranslator(jc *javascript.JsIRContext) *htmlTranslator {
	return &htmlTranslator{jc: jc, idTags: map[string]string{}}
}

// newHTMLTranslatorWithNodes is like newHTMLTranslator but also threads an
// id → NodeInst map so OnPropAssign can map SNGL component props to DOM
// props via domWriteForIR. Required when translating handler/timer/setter
// bodies that NoReactivity injects with the original SNGL prop names.
func newHTMLTranslatorWithNodes(jc *javascript.JsIRContext, idToNode map[string]*ir.NodeInst) *htmlTranslator {
	t := newHTMLTranslator(jc)
	t.idToNode = idToNode
	return t
}

var _ codegen.IntrinsicTranslator = (*htmlTranslator)(nil)

// htmlNativeDOMProps is the set of DOM property names that html.sngl's
// platform-extension bodies write to directly. Any prop not in this
// set falls back to element.setAttribute(name, value).
//
// passInlinePure substitutes stdlib wrapper components with their
// html.sngl-defined native element bodies at lowering time, so every
// prop name landing here is a native DOM attribute/property name.
var htmlNativeDOMProps = map[string]bool{
	"textContent": true,
	"innerHTML":   true,
	"value":       true,
	"placeholder": true,
	"disabled":    true,
	"readonly":    true,
	"checked":     true,
	"type":        true,
	"className":   true,
	"src":         true,
	"alt":         true,
	"href":        true,
	"title":       true,
	"role":        true,
	"rows":        true,
	"max":         true,
}

// htmlEventName maps a SNGL event name to its DOM counterpart.
func htmlEventName(event string) string {
	switch event {
	case "click", "input", "change", "submit":
		return event
	}
	return ""
}

// identBareName returns the bare name of an Ident expression, or "".
func identBareName(e ir.Expr) string {
	if id, ok := e.(*ir.Ident); ok {
		return id.Name
	}
	return ""
}

func (t *htmlTranslator) OnCreateNode(ctx context.Context, id, tag string) []ir.Stmt {
	// passInlinePure substitutes stdlib wrapper components (vbox, text,
	// button, ...) with html.sngl's native element bodies before this
	// translator runs, so every tag landing here is a native HTML
	// element name (span, button, input, div, ...).
	t.idTags[id] = tag
	t.topLevel = append(t.topLevel, id)

	// const <id> = document.createElement("<tag>")
	createCall := &ir.Call{
		Type:     ir.TypDyn,
		Receiver: &ir.Ident{Name: "document"},
		Func:     &ir.Func{Name: "createElement"},
		Args:     []ir.CallArg{{Value: &ir.Literal{Type: ir.TypString, Raw: tag}}},
	}
	return []ir.Stmt{&ir.LocalVar{
		Name: id,
		Type: ir.TypDyn,
		Init: createCall,
	}}
}

// OnCreateComponent preserves the LocalVar as-is. html inlines user
// components via renderIRUserComponent on the static-tree path; on the
// WalkLowered (JS) path a non-inlinable component instance keeps its
// original `const id = ...CreateComponent(...)` binding.
func (t *htmlTranslator) OnCreateComponent(ctx context.Context, id string, call *ir.Call) []ir.Stmt {
	return []ir.Stmt{&ir.LocalVar{Name: id, Type: ir.TypDyn, Init: call}}
}

func (t *htmlTranslator) OnAppendChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	// Child appended somewhere → no longer top-level.
	if id, ok := child.(*ir.Ident); ok && id.Synthesized {
		for i, name := range t.topLevel {
			if name == id.Name {
				t.topLevel = append(t.topLevel[:i], t.topLevel[i+1:]...)
				break
			}
		}
	}
	return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: parent,
		Func:     &ir.Func{Name: "appendChild"},
		Args:     []ir.CallArg{{Value: child}},
	}}}
}

func (t *htmlTranslator) OnRemoveChild(ctx context.Context, parent, child ir.Expr) []ir.Stmt {
	return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: parent,
		Func:     &ir.Func{Name: "removeChild"},
		Args:     []ir.CallArg{{Value: child}},
	}}}
}

func (t *htmlTranslator) OnAttachHandler(ctx context.Context, node ir.Expr, event string, handler ir.Expr) []ir.Stmt {
	domEvent := htmlEventName(event)
	if domEvent == "" {
		return nil
	}
	return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: node,
		Func:     &ir.Func{Name: "addEventListener"},
		Args: []ir.CallArg{
			{Value: &ir.Literal{Type: ir.TypString, Raw: domEvent}},
			{Value: handler},
		},
	}}}
}

func (t *htmlTranslator) OnPropAssign(ctx context.Context, node ir.Expr, prop string, value ir.Expr) []ir.Stmt {
	// SNGL component prop → DOM prop mapping. NoReactivity-lowered
	// handler/timer/setter bodies arrive here with the original SNGL
	// prop name (e.g. text.value), so consult idToNode when set to
	// produce the correct DOM-side write.
	if t.idToNode != nil {
		if id, ok := node.(*ir.Ident); ok && id.IsElementRef {
			if n := t.idToNode[id.Name]; n != nil {
				if stmts, ok := domWriteForIR(n.Name, prop, node, value); ok {
					return stmts
				}
			}
		}
	}
	if !htmlNativeDOMProps[prop] {
		// Unknown prop: emit node.setAttribute("prop", value).
		return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
			Type:     ir.TypVoid,
			Receiver: node,
			Func:     &ir.Func{Name: "setAttribute"},
			Args: []ir.CallArg{
				{Value: &ir.Literal{Type: ir.TypString, Raw: prop}},
				{Value: value},
			},
		}}}
	}
	// node.<prop> = value
	return []ir.Stmt{&ir.Assign{
		Target: &ir.Select{Operand: node, Field: prop, Type: ir.TypDyn},
		Op:     ast.AssignSet,
		Value:  value,
	}}
}

// domWriteForIR is the IR-level mirror of domWriteFor in html.go. Returns
// (stmts, true) when the (componentName, prop) pair maps to a known DOM
// write, or (nil, false) when the caller should fall back to the generic
// DOM-name dispatch.
func domWriteForIR(componentName, prop string, node, value ir.Expr) ([]ir.Stmt, bool) {
	mkAssign := func(field string) []ir.Stmt {
		return []ir.Stmt{&ir.Assign{
			Target: &ir.Select{Operand: node, Field: field, Type: ir.TypDyn},
			Op:     ast.AssignSet,
			Value:  value,
		}}
	}
	switch componentName {
	case "text", "badge":
		if prop == "value" {
			return mkAssign("textContent"), true
		}
	case "button":
		if prop == "text" {
			return mkAssign("textContent"), true
		}
		if prop == "disabled" {
			return mkAssign("disabled"), true
		}
	case "input":
		if prop == "value" {
			return mkAssign("value"), true
		}
		if prop == "disabled" {
			return mkAssign("disabled"), true
		}
	case "progress":
		if prop == "value" {
			return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
				Type:     ir.TypVoid,
				Receiver: node,
				Func:     &ir.Func{Name: "setAttribute"},
				Args: []ir.CallArg{
					{Value: &ir.Literal{Type: ir.TypString, Raw: "value"}},
					{Value: value},
				},
			}}}, true
		}
	case "checkbox", "toggle":
		if prop == "checked" {
			// The __n* id is on the wrapping <label>; descend to the
			// inner <input> to actually flip the checked state.
			inner := &ir.Call{
				Type:     ir.TypDyn,
				Receiver: node,
				Func:     &ir.Func{Name: "querySelector"},
				Args:     []ir.CallArg{{Value: &ir.Literal{Type: ir.TypString, Raw: "input"}}},
			}
			return []ir.Stmt{&ir.Assign{
				Target: &ir.Select{Operand: inner, Field: "checked", Type: ir.TypDyn},
				Op:     ast.AssignSet,
				Value:  value,
			}}, true
		}
	case "modal", "drawer", "popover", "menu":
		if prop == "open" {
			// el.style.display = (cond) ? "" : "none"
			styleSel := &ir.Select{Operand: node, Field: "style", Type: ir.TypDyn}
			displaySel := &ir.Select{Operand: styleSel, Field: "display", Type: ir.TypDyn}
			tern := &ir.Ternary{
				Cond: value,
				Then: &ir.Literal{Type: ir.TypString, Raw: ""},
				Else: &ir.Literal{Type: ir.TypString, Raw: "none"},
			}
			return []ir.Stmt{&ir.Assign{
				Target: displaySel,
				Op:     ast.AssignSet,
				Value:  tern,
			}}, true
		}
	}
	return nil, false
}

func (t *htmlTranslator) OnSlotReset(ctx context.Context, slot *ir.Var) []ir.Stmt {
	return []ir.Stmt{&ir.Assign{
		Target: &ir.Ident{Name: slot.Name, Synthesized: true, Type: slot.Type},
		Op:     ast.AssignSet,
		Value:  &ir.ListLit{Type: slot.Type, Elems: nil},
	}}
}

func (t *htmlTranslator) OnSlotAppend(ctx context.Context, slot *ir.Var, child ir.Expr) []ir.Stmt {
	slotRef := &ir.Ident{Name: slot.Name, Synthesized: true, Type: slot.Type}
	return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
		Type:     ir.TypVoid,
		Receiver: slotRef,
		Func:     &ir.Func{Name: "push"},
		Args:     []ir.CallArg{{Value: child}},
	}}}
}

func (t *htmlTranslator) OnIter(ctx context.Context, iter ir.Expr) ir.Expr {
	// JS for-of iterates iterable directly. Synthesized refs render as
	// bare identifiers via JsIRContext.evalIdent's Synthesized hook.
	return iter
}

func (t *htmlTranslator) OnCond(ctx context.Context, cond ir.Expr) ir.Expr {
	return cond
}

func (t *htmlTranslator) OnDefault(ctx context.Context, stmt ir.Stmt) []ir.Stmt {
	return []ir.Stmt{stmt}
}
