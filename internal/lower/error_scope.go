package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passErrorScope resolves a raise against the render tree rather than against
// the declaration it was written in. The checker resolves each handler's
// raises inside its own component body, so one that reaches no boundary there
// is left native -- and it is only once the inliner has spliced a component's
// body into the tree that renders it that the boundaries and windows around
// that instance are there to be found. Every splice is its own clone, so two
// instances of one component under different boundaries each answer to their
// own.
//
// A handler is found wherever the node carries it: in Handlers, or as a lambda
// in Props, which is where android's override substitution has put it by now.
// Upgrading only what the checker left native is what keeps that safe: a
// lambda the program wrote resolves its raises as leaving the lambda, never as
// native.
//
// A component built at run time is not spliced: its body is emitted once, for
// every instance, and none of them knows statically which boundary it sits
// under. An instance of one under a boundary or window, whose body would let a
// raise out, is refused rather than emitted with the raise going nowhere.
//
// Always on: where a raise lands is not a target's choice.
var passErrorScope = pass{
	name:    "ErrorScope",
	enabled: func(Features) bool { return true },
	apply:   lowerErrorScope,
}

func lowerErrorScope(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := &errorScopeState{escapes: map[*ir.Component]bool{}}
	for _, w := range pkg.Windows {
		st.stmts([]ir.Stmt{w}, nil)
	}
	st.stmts(pkg.Body, nil)
	for _, c := range pkg.Components {
		st.stmts(c.Body, nil)
	}
	return st.err
}

type errorScopeState struct {
	escapes map[*ir.Component]bool
	err     error
}

func (st *errorScopeState) stmts(stmts []ir.Stmt, scope []*ir.EventHandler) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.ErrorBoundary:
			inner := scope
			if n.Handler != nil {
				resolveHandlerEscapes(n.Handler, scope)
				inner = append([]*ir.EventHandler{n.Handler}, scope...)
			}
			st.stmts(n.Children, inner)
			st.stmts(n.Failed, inner)
		case *ir.NodeInst:
			for i := range n.Handlers {
				resolveHandlerEscapes(&n.Handlers[i], scope)
			}
			for _, p := range n.Props {
				resolveEscapes(p.Value, scope)
			}
			inner := scope
			if ir.IsWindowNode(n) && n.ErrorHandler != nil {
				resolveHandlerEscapes(n.ErrorHandler, scope)
				inner = append([]*ir.EventHandler{n.ErrorHandler}, scope...)
			}
			if len(inner) > 0 && n.Component != nil && n.Component.RuntimeInstance && st.componentEscapes(n.Component) && st.err == nil {
				st.err = fmt.Errorf("%s: a raise in %s cannot reach the @error around this instance: %s is built at run time here, so its body is shared by every instance and answers only to the boundaries it writes itself", ir.StmtPos(n), n.Component.Name, n.Component.Name)
			}
			st.stmts(n.Children, inner)
			for _, name := range ir.SlotNames(n.Slots) {
				if sc := n.Slots[name]; sc != nil {
					st.stmts(sc.Body, inner)
				}
			}
		case *ir.If:
			st.stmts(n.Body, scope)
			st.stmts(n.Else, scope)
		case *ir.For:
			st.stmts(n.Body, scope)
			st.stmts(n.Else, scope)
		case *ir.ContextProvider:
			st.stmts(n.Children, scope)
		case *ir.SlotInst:
			st.stmts(n.Children, scope)
		case *ir.CallStmt:
			if n.Call != nil {
				resolveEscapes(n.Call, scope)
			}
		}
	}
}

func resolveHandlerEscapes(h *ir.EventHandler, scope []*ir.EventHandler) {
	if h != nil && h.Func != nil {
		resolveEscapes(h.Func, scope)
	}
}

// resolveEscapes points every raise under root that the checker left native
// at the nearest handler in scope.
func resolveEscapes(root any, scope []*ir.EventHandler) {
	if len(scope) == 0 || root == nil {
		return
	}
	_ = ir.Walk(root, func(n ir.Node) error {
		if x, ok := n.(*ir.Call); ok && x.ErrorMode == ir.ErrorPropagateNative {
			x.ErrorMode = ir.ErrorInvokeAndTerminate
			x.ResolvedHandler = scope[0]
		}
		return nil
	})
}

// componentEscapes reports whether a raise in a handler c renders, directly or
// through the components it instantiates, reaches no handler inside c.
func (st *errorScopeState) componentEscapes(c *ir.Component) bool {
	if v, ok := st.escapes[c]; ok {
		return v
	}
	st.escapes[c] = false
	found := false
	_ = ir.Walk(c.Body, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.NodeInst:
			if x.Component != nil && x.Component != c && st.componentEscapes(x.Component) {
				found = true
				return ir.SkipAll
			}
		case *ir.Call:
			if x.ErrorMode == ir.ErrorPropagateNative {
				found = true
				return ir.SkipAll
			}
		}
		return nil
	})
	st.escapes[c] = found
	return found
}
