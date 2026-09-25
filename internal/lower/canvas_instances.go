package lower

import (
	"strconv"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passCanvasInstances makes each canvas written under a `for` a component
// built at run time. A canvas is lowered to a surface and a draw routine the
// owner holds -- one field and one method -- so a loop's copies shared both:
// every copy painted into the last surface, and the routine read the loop
// variable in a scope that never bound it. As an instance each copy holds its
// own, and what the drawing read from the loop arrives as a prop, the way a
// slot child's does in passSlotChildInstances.
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
	for i := range n.Handlers {
		h := &n.Handlers[i]
		if h.Func == nil || len(h.Func.Block) == 0 {
			continue
		}
		name := "__on" + strconv.Itoa(len(comp.Events))
		comp.Events = append(comp.Events, &ir.EventDecl{Name: name})
		inst.Handlers = append(inst.Handlers, ir.EventHandler{Name: name, Func: h.Func})
		h.Func = &ir.Func{Block: []ir.Stmt{&ir.Emit{Name: name}}}
	}
	local := declaredWithin([]ir.Stmt{n})
	_ = ir.Walk(n, func(node ir.Node) error {
		if host, ok := node.(*ir.NodeInst); ok {
			for _, h := range host.Handlers {
				if h.Func != nil {
					for _, p := range h.Func.Params {
						local[p] = true
					}
				}
			}
		}
		return nil
	})
	st.liftValues(n, comp, inst, local)
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
