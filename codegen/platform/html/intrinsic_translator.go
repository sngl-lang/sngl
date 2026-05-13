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
}

func newHTMLTranslator(jc *javascript.JsIRContext) *htmlTranslator {
	return &htmlTranslator{jc: jc, idTags: map[string]string{}}
}

var _ codegen.IntrinsicTranslator = (*htmlTranslator)(nil)

// htmlTagToDOM maps a SNGL tag to its equivalent HTML element name.
// Returns "" for unknown tags (translator returns nil emission).
func htmlTagToDOM(tag string) string {
	switch tag {
	case "text", "label":
		return "span"
	case "button":
		return "button"
	case "input":
		return "input"
	case "checkbox":
		return "input" // type="checkbox" assigned via OnPropAssign
	case "vbox", "hbox":
		return "div" // flex direction via style props
	case "scroll":
		return "div"
	case "link":
		return "a"
	case "image":
		return "img"
	}
	return ""
}

// htmlPropSetter returns the DOM property name for a SNGL prop on a
// tag. Empty string means "no known property — fall back to
// setAttribute()."
func htmlPropSetter(tag, prop string) string {
	switch tag {
	case "text", "label":
		if prop == "value" {
			return "textContent"
		}
	case "button":
		switch prop {
		case "text":
			return "textContent"
		case "disabled":
			return "disabled"
		}
	case "input":
		switch prop {
		case "value":
			return "value"
		case "placeholder":
			return "placeholder"
		case "disabled":
			return "disabled"
		case "type":
			return "type"
		}
	case "checkbox":
		if prop == "checked" {
			return "checked"
		}
	}
	return ""
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
	domTag := htmlTagToDOM(tag)
	if domTag == "" {
		return nil
	}
	t.idTags[id] = tag
	t.topLevel = append(t.topLevel, id)

	// const <id> = document.createElement("<domTag>")
	createCall := &ir.Call{
		Type:     ir.TypDyn,
		Receiver: &ir.Ident{Name: "document"},
		Func:     &ir.Func{Name: "createElement"},
		Args:     []ir.CallArg{{Value: &ir.Literal{Type: ir.TypString, Raw: domTag}}},
	}
	return []ir.Stmt{&ir.LocalVar{
		Name: id,
		Type: ir.TypDyn,
		Init: createCall,
	}}
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
	bareID := identBareName(node)
	tag := t.idTags[bareID]
	setter := htmlPropSetter(tag, prop)
	if setter == "" {
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
	// node.<setter> = value
	return []ir.Stmt{&ir.Assign{
		Target: &ir.Select{Operand: node, Field: setter, Type: ir.TypDyn},
		Op:     ast.AssignSet,
		Value:  value,
	}}
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
