package codegen

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// IntrinsicTranslator is implemented by codegen platforms that consume
// lowered IR (post-passReactivity, optionally post-passDeclarative).
// Each method emits target-language source for one lowered IR shape.
//
// All methods are pure functions of their arguments — translators must
// not retain state across calls except through caller-managed
// per-component context.
type IntrinsicTranslator interface {
	// OnCreateNode emits a statement binding `id` to a fresh widget of
	// the given tag. e.g. fyne: "m.label0 := widget.NewLabel(\"\")".
	OnCreateNode(id, tag string) string

	// OnAppendChild emits a statement that adds `child` to `parent`.
	OnAppendChild(parent, child string) string

	// OnRemoveChild emits a statement that removes `child` from `parent`.
	OnRemoveChild(parent, child string) string

	// OnAttachHandler emits a statement that wires `event` on `node` to
	// `handlerRef` (a target-language expression evaluating to a callable).
	OnAttachHandler(node, event, handlerRef string) string

	// OnPropAssign emits a statement setting `nodeID.<prop>` to
	// `valueExpr`'s translation. The translator knows how to render
	// expressions in its target language.
	OnPropAssign(nodeID, prop string, valueExpr ir.Expr) string
}

// WalkLowered iterates the lowered IR statement sequence and dispatches
// each known shape to t. Returns the concatenated emissions in source
// order. Unrecognized statements are passed through as empty strings
// (a future pass should error on unknowns, but during the build-out
// phase platforms may not implement every dispatch yet).
func WalkLowered(stmts []ir.Stmt, t IntrinsicTranslator) string {
	var b strings.Builder
	for _, s := range stmts {
		b.WriteString(walkOne(s, t))
	}
	return b.String()
}

func walkOne(s ir.Stmt, t IntrinsicTranslator) string {
	switch n := s.(type) {
	case *ir.LocalVar:
		if call, ok := n.Init.(*ir.Call); ok && isLowerIntrinsic(call, "CreateNode") {
			tag, _ := extractStringLit(call.Args[0].Value)
			return t.OnCreateNode(n.Name, tag)
		}
	case *ir.CallStmt:
		if n.Call == nil {
			return ""
		}
		switch {
		case isLowerIntrinsic(n.Call, "AppendChild"):
			p, c := identName(n.Call.Args[0].Value), identName(n.Call.Args[1].Value)
			return t.OnAppendChild(p, c)
		case isLowerIntrinsic(n.Call, "RemoveChild"):
			p, c := identName(n.Call.Args[0].Value), identName(n.Call.Args[1].Value)
			return t.OnRemoveChild(p, c)
		case isLowerIntrinsic(n.Call, "AttachHandler"):
			node := identName(n.Call.Args[0].Value)
			evt, _ := extractStringLit(n.Call.Args[1].Value)
			hRef := identName(n.Call.Args[2].Value)
			return t.OnAttachHandler(node, evt, hRef)
		}
	case *ir.Assign:
		if sel, ok := n.Target.(*ir.Select); ok {
			if id, ok := sel.Operand.(*ir.Ident); ok && id.IsElementRef {
				return t.OnPropAssign(id.Name, sel.Field, n.Value)
			}
		}
	}
	return ""
}

func isLowerIntrinsic(call *ir.Call, name string) bool {
	return call != nil && call.Func != nil && call.Func.Intrinsic == name
}

func extractStringLit(e ir.Expr) (string, bool) {
	if l, ok := e.(*ir.Literal); ok && l.Type == ir.TypString {
		return l.Raw, true
	}
	return "", false
}

func identName(e ir.Expr) string {
	if id, ok := e.(*ir.Ident); ok {
		return id.Name
	}
	return ""
}
