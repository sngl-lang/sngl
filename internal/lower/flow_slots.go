package lower

import "git.duckfam.us/jonathan/sngl/ir"

// slotFlows makes a flow that holds a reactive `if` or `for` among its spans
// the render slot, for a target withholding Features.InlineSlots: the slot's
// entries would otherwise be spans rendered into a span, which such a target
// has no container for. The flow is wrapped in a one-pass loop over a const,
// which collectFromFor makes a slot re-rendering its whole body on any state
// the body reads -- the construct the language already has for it -- and the
// `if` inside is then an ordinary one, evaluated each time the flow is built.
func (st *reactivityState) slotFlows(stmts []ir.Stmt) []ir.Stmt {
	for i, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if ir.IsWindowNode(n) {
				continue
			}
			if st.hostsReactiveSpans(n) {
				stmts[i] = flowSlot(n)
				continue
			}
			n.Children = st.slotFlows(n.Children)
		case *ir.If:
			n.Body = st.slotFlows(n.Body)
			n.Else = st.slotFlows(n.Else)
		case *ir.For:
			n.Body = st.slotFlows(n.Body)
			n.Else = st.slotFlows(n.Else)
		case *ir.ErrorBoundary:
			n.Children = st.slotFlows(n.Children)
		case *ir.ContextProvider:
			n.Children = st.slotFlows(n.Children)
		case *ir.SlotInst:
			n.Children = st.slotFlows(n.Children)
		}
	}
	return stmts
}

func flowSlot(n *ir.NodeInst) ir.Stmt {
	return &ir.For{
		Iter:     &ir.ListLit{Type: ir.ListOf(ir.TypInt), Elems: []ir.Expr{&ir.Literal{Type: ir.TypInt, Value: "0"}}},
		ElemType: ir.TypInt,
		Body:     []ir.Stmt{n},
	}
}

func (st *reactivityState) hostsReactiveSpans(n *ir.NodeInst) bool {
	return n.Component != nil && ir.IsUITree(n.Component.Tree) && !ir.IsShapeContainer(n) &&
		st.reactiveAmongSpans(n.Children, false)
}

func isSpanNode(n *ir.NodeInst) bool {
	return n.Component != nil && ir.IsSegmentedTree(n.Component.Tree) && !ir.IsDrawShapeTree(n.Component.Tree)
}

// reactiveAmongSpans reports whether stmts hold a reactive `if` or `for` whose
// content is spans, or a span holding one.
func (st *reactivityState) reactiveAmongSpans(stmts []ir.Stmt, inSpan bool) bool {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if isSpanNode(n) && st.reactiveAmongSpans(n.Children, true) {
				return true
			}
		case *ir.If:
			if len(st.exprDeps(n.Cond)) > 0 && (inSpan || holdsSpan(n.Body) || holdsSpan(n.Else)) {
				return true
			}
			if st.reactiveAmongSpans(n.Body, inSpan) || st.reactiveAmongSpans(n.Else, inSpan) {
				return true
			}
		case *ir.For:
			reactive := len(st.exprDeps(n.Iter)) > 0 || st.bodyNeedsSlot(n.Body)
			if reactive && (inSpan || holdsSpan(n.Body)) {
				return true
			}
			if st.reactiveAmongSpans(n.Body, inSpan) {
				return true
			}
		}
	}
	return false
}

func holdsSpan(stmts []ir.Stmt) bool {
	found := false
	_ = ir.WalkStmts(stmts, func(s ir.Stmt) error {
		if n, ok := s.(*ir.NodeInst); ok && isSpanNode(n) {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}
