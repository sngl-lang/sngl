package codegen

import (
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
	OnCreateNode(id, tag string) []string

	// OnAppendChild emits a statement that adds `child` to `parent`.
	OnAppendChild(parent, child string) []string

	// OnRemoveChild emits a statement that removes `child` from `parent`.
	OnRemoveChild(parent, child string) []string

	// OnAttachHandler emits a statement that wires `event` on `node` to
	// `handlerRef` (a target-language expression evaluating to a callable).
	OnAttachHandler(node, event, handlerRef string) []string

	// OnPropAssign emits a statement setting `nodeID.<prop>` to
	// `valueExpr`'s translation. The translator knows how to render
	// expressions in its target language.
	OnPropAssign(nodeID, prop string, valueExpr ir.Expr) []string

	// OnDefault handles statements the walker doesn't recognize as lower
	// intrinsics. The translator delegates to its target language's stmt
	// emitter (e.g. fyne uses golang.GoIRContext.EvalStmt). Returning nil
	// means "no emission" — preserves the current silent-skip semantics
	// for unrecognized shapes during build-out.
	OnDefault(stmt ir.Stmt) []string

	// OnSlotReset emits the "clear slot accumulator" code for a Plan A
	// __slot<N> = [] assignment. slotID is the slot's symbol name; the
	// translator decides whether to prefix with the Model receiver.
	OnSlotReset(slotID string) []string

	// OnSlotAppend emits the "push child ref into accumulator" code for
	// the Plan A __slot<N> = stdlib.ListPush(__slot<N>, child) pattern.
	OnSlotAppend(slotID, childID string) []string

	// OnIter returns the platform-side expression for a For loop's
	// Iter. For synthesized slot accumulators, this typically
	// qualifies through the Model receiver. Otherwise the translator
	// delegates to its language expression emitter.
	OnIter(iter ir.Expr) string

	// OnCond returns the platform-side expression for an If condition.
	// Almost always delegates to the language expression emitter.
	OnCond(cond ir.Expr) string
}

// WalkLowered iterates the lowered IR statement sequence and dispatches
// each known shape to t. Returns the emitted lines in source order.
// Unrecognized statements are passed through OnDefault (a future pass
// should error on unknowns, but during the build-out phase platforms
// may not implement every dispatch yet).
func WalkLowered(stmts []ir.Stmt, t IntrinsicTranslator) []string {
	var out []string
	for _, s := range stmts {
		out = append(out, walkOne(s, t)...)
	}
	return out
}

func walkOne(s ir.Stmt, t IntrinsicTranslator) []string {
	switch n := s.(type) {
	case *ir.For:
		iter := t.OnIter(n.Iter)
		body := WalkLowered(n.Body, t)
		out := []string{"for _, " + n.Key + " := range " + iter + " {"}
		for _, l := range body {
			out = append(out, "\t"+l)
		}
		out = append(out, "}")
		return out
	case *ir.If:
		cond := t.OnCond(n.Cond)
		body := WalkLowered(n.Body, t)
		out := []string{"if " + cond + " {"}
		for _, l := range body {
			out = append(out, "\t"+l)
		}
		if len(n.Else) > 0 {
			out = append(out, "} else {")
			for _, l := range WalkLowered(n.Else, t) {
				out = append(out, "\t"+l)
			}
		}
		out = append(out, "}")
		return out
	case *ir.LocalVar:
		if call, ok := n.Init.(*ir.Call); ok && isLowerIntrinsic(call, "CreateNode") {
			tag, _ := extractStringLit(call.Args[0].Value)
			return t.OnCreateNode(n.Name, tag)
		}
	case *ir.CallStmt:
		if n.Call == nil {
			return nil
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
		if id, ok := n.Target.(*ir.Ident); ok && id.Synthesized {
			// Slot reset: __slotN = []
			if ll, ok := n.Value.(*ir.ListLit); ok && len(ll.Elems) == 0 {
				return t.OnSlotReset(id.Name)
			}
			// Slot append: __slotN = stdlib.ListPush(__slotN, #childID)
			if call, ok := n.Value.(*ir.Call); ok && call.Func != nil && call.Func.Intrinsic == "ListPush" && len(call.Args) == 2 {
				if childArg, ok := call.Args[1].Value.(*ir.Ident); ok {
					return t.OnSlotAppend(id.Name, childArg.Name)
				}
			}
		}
		if sel, ok := n.Target.(*ir.Select); ok {
			if id, ok := sel.Operand.(*ir.Ident); ok && id.IsElementRef {
				return t.OnPropAssign(id.Name, sel.Field, n.Value)
			}
		}
	}
	return t.OnDefault(s)
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
