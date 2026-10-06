package lower

import (
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// passBoundaryFailed gives a boundary's `failed` slot its meaning: once the
// boundary has caught, it renders the fallback in place of its content.
//
//	boundary(@error(e) { … }) { CONTENT  component failed { FALLBACK } }
//	    =>  var __failed0 = false            (on the owner)
//	        boundary(@error(e) { __failed0 = true; … }) {
//	            if __failed0 { FALLBACK } else { CONTENT }
//	        }
//
// The flag and the conditional are both things every backend already emits, so
// no platform emitter grows a case -- the same trade passForElse makes, and the
// reason a boundary reaches codegen as the passthrough it has always been.
//
// Nothing about the fallback can be answered statically. "Has this boundary
// caught?" is a fact about the run, and the boundary's own @error handler is
// the one place that learns it: the checker resolves each fallible call site to
// a handler (analyzeErrors), and a backend inlines that handler's body at the
// raise. So the flag is set from the handler and read from the view, which is
// exactly the shape passReactivity already turns into a render slot.
//
// It runs early -- before inlining, before reactivity -- so the `if` it leaves
// is an ordinary view conditional by the time either sees it. A component
// holding a boundary is inlined per call site afterwards, which renames the
// flag with the rest of that component's state and so gives each instance its
// own.
//
// A boundary that declared no fallback is untouched. One that declared a
// fallback always has a handler to prepend to, empty or not: the checker
// supplies one, because error resolution runs there and a raise resolves past
// a boundary that has none.
var passBoundaryFailed = pass{
	name:    "BoundaryFailed",
	enabled: func(Features) bool { return true },
	apply:   lowerBoundaryFailed,
}

func lowerBoundaryFailed(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := &boundaryFailedState{}
	for _, o := range ir.Owners(pkg) {
		st.owner = o
		st.added = nil
		st.stmts(o.Stmts())
		st.attach()
	}
	return nil
}

type boundaryFailedState struct {
	counter int
	owner   ir.Owner
	added   []*ir.Var
}

// stmts finds every boundary carrying a fallback in one view body, descending
// through the nodes, conditionals and loops that hold others.
func (st *boundaryFailedState) stmts(stmts []ir.Stmt) {
	for _, s := range stmts {
		// What a statement holds first, then the boundary itself: slots in
		// name order, since this order numbers __failedN.
		for _, b := range ir.ViewBlocks(s) {
			st.stmts(*b)
		}
		if n, ok := s.(*ir.ErrorBoundary); ok {
			st.rewrite(n)
		}
	}
}

// rewrite swaps one boundary's content for the conditional, and returns with
// the flag recorded for the owner.
func (st *boundaryFailedState) rewrite(n *ir.ErrorBoundary) {
	if len(n.Failed) == 0 {
		return
	}
	name := "__failed" + strconv.Itoa(st.counter)
	st.counter++
	// Synthesized on both halves, and they have to agree. A backend reads it
	// off the Var to decide where the declaration goes and off each Ident to
	// decide how a reference spells it -- html writes a top-level `let` for
	// the first and a bare name for the second, where an unsynthesized var is
	// a field of `state` reached as `state.x`. Marked on one and not the
	// other, the page declared `var __failed0` and then read
	// `state.__failed0`, which is a different binding and merely happened to
	// be falsy.
	v := &ir.Var{Name: name, Type: ir.TypBool, Synthesized: true,
		Init: &ir.Literal{Value: "false", Type: ir.TypBool}}
	st.added = append(st.added, v)
	read := func() ir.Expr {
		return &ir.Ident{Name: name, Sym: v, Type: ir.TypBool, Synthesized: true}
	}

	pos := ast.Pos{}
	if n.AST != nil {
		pos = n.AST.Pos
	}
	n.Children = []ir.Stmt{&ir.If{
		AST:  &ast.IfStmt{Pos: pos},
		Cond: read(),
		Body: n.Failed,
		Else: n.Children,
	}}
	n.Failed = nil

	// First, so a handler that raises again leaves the fallback showing.
	n.Handler.Func.Block = append([]ir.Stmt{&ir.Assign{
		Target: read(),
		Op:     ast.AssignSet,
		Value:  &ir.Literal{Value: "true", Type: ir.TypBool},
	}}, n.Handler.Func.Block...)
}

// attach hangs this owner's flags on the declaration that owns them. ir.Owner
// carries Vars by value, so the write has to go back to the declaration
// itself, which AddVars is.
func (st *boundaryFailedState) attach() {
	st.owner.AddVars(st.added...)
}
