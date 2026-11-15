package lower

import (
	"strconv"

	"duckfam.us/sngl/ir"
)

// passCanvasInstances makes each canvas under a `for` a component built at
// run time, since a canvas's surface and draw routine are otherwise one field
// and one method of its owner, shared by every copy. So too one under a
// branch that is not painted through its protocol: its render slot made the
// element and nothing drew into it, or on fyne named a widget nothing
// declared. One painted through its protocol is held where its slot made it
// (passPlacement), and redrawn from there.
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
			st.canvasesUnderSlots(c.Stmts, slotNone)
		}
	}
	st.canvasesUnderSlots(pkg.Body, slotNone)
	return nil
}

// synthesizeCanvas is synthesize for a canvas, whose shapes' own `@draw`
// handlers are the drawing: they stay in the component, and only the canvas's
// handlers -- what the program wrote on it -- cross as events.
func (st *slotChildSynth) synthesizeCanvas(n *ir.NodeInst) *ir.NodeInst {
	comp := &ir.Component{Name: "__canvas" + strconv.Itoa(st.n), RuntimeInstance: true, Family: nodeFamily(n)}
	st.n++
	inst := &ir.NodeInst{AST: n.AST, Component: comp}
	l := newLift(comp, inst)
	liftNodeHandlers(n, l, nil, nil)
	st.liftValues(n, l, handlerParams(n, declaredWithin([]ir.Stmt{n})))
	comp.Stmts = []ir.Stmt{n}
	st.pkg.Components = append(st.pkg.Components, comp)
	return inst
}

// slotKind is what a canvas is rendered under: nothing that renders it again,
// a branch, or a loop, the last of which wins.
type slotKind int

const (
	slotNone slotKind = iota
	slotBranch
	slotLoop
)

func (st *slotChildSynth) canvasesUnderSlots(stmts []ir.Stmt, under slotKind) {
	for i, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if ir.IsShapeContainer(n) && (under == slotLoop || under == slotBranch && !paintsThrough(n)) {
				stmts[i] = st.synthesizeCanvas(n)
				continue
			}
			st.canvasesUnderSlots(n.Children, under)
		case *ir.For:
			st.canvasesUnderSlots(n.Body, slotLoop)
			st.canvasesUnderSlots(n.Else, max(under, slotBranch))
		case *ir.If, *ir.Match:
			for _, b := range ir.TransparentBlocks(n) {
				st.canvasesUnderSlots(*b, max(under, slotBranch))
			}
		case *ir.ErrorBoundary:
			st.canvasesUnderSlots(n.Children, under)
		case *ir.ContextProvider:
			st.canvasesUnderSlots(n.Children, under)
		case *ir.SlotInst:
			st.canvasesUnderSlots(n.Children, under)
		}
	}
}
