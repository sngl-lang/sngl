package lower

import (
	"strconv"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passCanvasInstances makes each canvas under a `for` a component built at
// run time, since a canvas's surface and draw routine are otherwise one field
// and one method of its owner, shared by every copy.
var passCanvasInstances = pass{
	name:    "CanvasInstances",
	enabled: hasInstanceRuntime,
	apply:   lowerCanvasInstances,
}

func lowerCanvasInstances(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := &slotChildSynth{pkg: pkg, reactive: collectReactiveVars(pkg)}
	for _, c := range append([]*ir.Component(nil), pkg.Components...) {
		if c != nil {
			st.canvasesUnderLoops(c.Body, false)
		}
	}
	st.canvasesUnderLoops(pkg.Body, false)
	for _, w := range ir.AllWindows(pkg) {
		st.canvasesUnderLoops(w.Children, false)
	}
	return nil
}

// synthesizeCanvas is synthesize for a canvas, whose shapes' own `@draw`
// handlers are the drawing: they stay in the component, and only the canvas's
// handlers -- what the program wrote on it -- cross as events.
func (st *slotChildSynth) synthesizeCanvas(n *ir.NodeInst) *ir.NodeInst {
	comp := &ir.Component{Name: "__canvas" + strconv.Itoa(st.n), RuntimeInstance: true}
	st.n++
	inst := &ir.NodeInst{AST: n.AST, Component: comp}
	l := newLift(comp, inst)
	liftNodeHandlers(n, l, nil)
	st.liftValues(n, l, handlerParams(n, declaredWithin([]ir.Stmt{n})))
	comp.Body = []ir.Stmt{n}
	st.pkg.Components = append(st.pkg.Components, comp)
	return inst
}

func (st *slotChildSynth) canvasesUnderLoops(stmts []ir.Stmt, inLoop bool) {
	for i, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if ir.IsWindowNode(n) {
				continue
			}
			if inLoop && ir.IsShapeContainer(n) {
				stmts[i] = st.synthesizeCanvas(n)
				continue
			}
			st.canvasesUnderLoops(n.Children, inLoop)
		case *ir.For:
			st.canvasesUnderLoops(n.Body, true)
			st.canvasesUnderLoops(n.Else, inLoop)
		case *ir.If:
			st.canvasesUnderLoops(n.Body, inLoop)
			st.canvasesUnderLoops(n.Else, inLoop)
		case *ir.ErrorBoundary:
			st.canvasesUnderLoops(n.Children, inLoop)
		case *ir.ContextProvider:
			st.canvasesUnderLoops(n.Children, inLoop)
		case *ir.SlotInst:
			st.canvasesUnderLoops(n.Children, inLoop)
		}
	}
}
