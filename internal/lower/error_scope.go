package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passErrorScope resolves a raise the checker left native -- one that reached
// no boundary in the component it was written in -- against the tree the
// instance is spliced into. Each splice is its own clone, so one declaration
// under two boundaries answers to both. A handler may be a lambda in Props by
// now (android's override substitution); only native calls are touched, and a
// lambda the program wrote never has one.
//
// A component built at run time is not spliced and its body is shared by every
// instance, so an instance of one under a handler, whose body lets a raise out,
// is refused rather than emitted with the raise going nowhere.
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
