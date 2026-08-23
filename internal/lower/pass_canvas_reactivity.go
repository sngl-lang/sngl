package lower

import (
	"git.duckfam.us/jonathan/sngl/ir"
)

var passCanvasReactivity = pass{
	name:    "CanvasReactivity",
	enabled: func(c Caps) bool { return c.ReactiveCanvas },
	apply:   lowerCanvasReactivity,
}

func lowerCanvasReactivity(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil || !pkg.UsesShapes {
		return nil
	}
	for _, comp := range pkg.Components {
		stateVars := componentStateVars(comp)
		injectCanvasRedraws(comp.Body, comp.Vars, stateVars, &comp.Funcs)
	}
	for _, w := range pkg.Windows {
		stateVars := windowStateVars(w)
		injectCanvasRedraws(w.Body, w.Vars, stateVars, &w.Funcs)
	}
	return nil
}

// componentStateVars returns the mutable state vars for a component.
func componentStateVars(comp *ir.Component) map[*ir.Var]bool {
	out := make(map[*ir.Var]bool, len(comp.Vars))
	for _, v := range comp.Vars {
		if !v.IsConst {
			out[v] = true
		}
	}
	return out
}

// windowStateVars returns the mutable state vars for a window.
func windowStateVars(w *ir.Window) map[*ir.Var]bool {
	out := make(map[*ir.Var]bool, len(w.Vars))
	for _, v := range w.Vars {
		if !v.IsConst {
			out[v] = true
		}
	}
	return out
}

// injectCanvasRedraws walks the visual tree rooted at stmts, finds canvas
// NodeInsts (CanvasDraw != nil), and for each one appends a CanvasRedrawStmt
// to every handler that mutates a var the draw func reads.
func injectCanvasRedraws(stmts []ir.Stmt, vars []*ir.Var, stateVars map[*ir.Var]bool, funcs *[]*ir.Func) {
	var canvases []canvasEntry
	collectCanvases(stmts, stateVars, &canvases)

	if len(canvases) == 0 {
		return
	}

	// Handlers on state vars (e.g. var x @change { ... }).
	for _, v := range vars {
		for _, h := range v.Handlers {
			if h.Func == nil {
				continue
			}
			mutated := handlerMutatedVars(h.Func, stateVars)
			for _, e := range canvases {
				if varsOverlap(mutated, e.deps) {
					h.Func.Block = append(h.Func.Block, &ir.CanvasRedrawStmt{
						Canvas:   e.canvas,
						DrawFunc: e.canvas.CanvasDraw,
					})
				}
			}
		}
	}

	// Handlers on NodeInsts in the visual tree (e.g. button @click).
	injectIntoNodeHandlers(stmts, stateVars, canvases)
}

type canvasEntry struct {
	canvas *ir.NodeInst
	deps   map[*ir.Var]bool
}

// collectCanvases walks stmts recursively and appends each canvas NodeInst
// (CanvasDraw != nil) with its reactive dep set to out.
func collectCanvases(stmts []ir.Stmt, stateVars map[*ir.Var]bool, out *[]canvasEntry) {
	for _, s := range stmts {
		ni, ok := s.(*ir.NodeInst)
		if !ok {
			continue
		}
		if ni.CanvasDraw != nil {
			deps := drawFuncStateVars(ni.CanvasDraw, stateVars)
			if len(deps) > 0 {
				*out = append(*out, canvasEntry{canvas: ni, deps: deps})
			}
		} else {
			collectCanvases(ni.Children, stateVars, out)
		}
	}
}

// drawFuncStateVars collects the state vars read by a synthesized canvas draw
// func by walking its block's CallStmt args.
func drawFuncStateVars(fn *ir.Func, stateVars map[*ir.Var]bool) map[*ir.Var]bool {
	out := make(map[*ir.Var]bool)
	for _, s := range fn.Block {
		cs, ok := s.(*ir.CallStmt)
		if !ok {
			continue
		}
		for _, arg := range cs.Call.Args {
			gatherStateVarRefs(arg.Value, stateVars, out)
		}
	}
	return out
}

// gatherStateVarRefs walks an expression and adds any Ident that resolves to
// a known state var to out.
func gatherStateVarRefs(e ir.Expr, stateVars map[*ir.Var]bool, out map[*ir.Var]bool) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *ir.Ident:
		if v, ok := x.Sym.(*ir.Var); ok && stateVars[v] {
			out[v] = true
		}
	case *ir.Binary:
		gatherStateVarRefs(x.Left, stateVars, out)
		gatherStateVarRefs(x.Right, stateVars, out)
	case *ir.Unary:
		gatherStateVarRefs(x.Operand, stateVars, out)
	case *ir.Ternary:
		gatherStateVarRefs(x.Cond, stateVars, out)
		gatherStateVarRefs(x.Then, stateVars, out)
		gatherStateVarRefs(x.Else, stateVars, out)
	case *ir.Conversion:
		gatherStateVarRefs(x.Operand, stateVars, out)
	case *ir.Select:
		gatherStateVarRefs(x.Operand, stateVars, out)
	case *ir.Index:
		gatherStateVarRefs(x.Operand, stateVars, out)
		gatherStateVarRefs(x.Idx, stateVars, out)
	case *ir.Call:
		if x.Receiver != nil {
			gatherStateVarRefs(x.Receiver, stateVars, out)
		}
		for _, a := range x.Args {
			gatherStateVarRefs(a.Value, stateVars, out)
		}
		// Follow computed func reads transitively (e.g. circleStyle() that
		// reads a state var indirectly).
		if x.Func != nil {
			for _, v := range x.Func.Reads {
				if stateVars[v] {
					out[v] = true
				}
			}
		}
	case *ir.StructLit:
		for _, f := range x.Fields {
			gatherStateVarRefs(f.Value, stateVars, out)
		}
	case *ir.ListLit:
		for _, el := range x.Elems {
			gatherStateVarRefs(el, stateVars, out)
		}
	case *ir.Spread:
		gatherStateVarRefs(x.Operand, stateVars, out)
	}
}

// handlerMutatedVars returns the state vars written by a handler func.
// Uses Func.Writes when set (checker-populated); otherwise walks the block.
func handlerMutatedVars(fn *ir.Func, stateVars map[*ir.Var]bool) map[*ir.Var]bool {
	out := make(map[*ir.Var]bool)
	if len(fn.Writes) > 0 {
		for _, v := range fn.Writes {
			if stateVars[v] {
				out[v] = true
			}
		}
		return out
	}
	gatherBlockMutations(fn.Block, stateVars, out)
	return out
}

// gatherBlockMutations walks a statement list for direct Assign/Toggle writes
// to state vars.
func gatherBlockMutations(stmts []ir.Stmt, stateVars map[*ir.Var]bool, out map[*ir.Var]bool) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.Assign:
			if id, ok := n.Target.(*ir.Ident); ok {
				if v, ok2 := id.Sym.(*ir.Var); ok2 && stateVars[v] {
					out[v] = true
				}
			}
		case *ir.Toggle:
			if id, ok := n.Target.(*ir.Ident); ok {
				if v, ok2 := id.Sym.(*ir.Var); ok2 && stateVars[v] {
					out[v] = true
				}
			}
		case *ir.If:
			gatherBlockMutations(n.Body, stateVars, out)
			gatherBlockMutations(n.Else, stateVars, out)
		case *ir.For:
			gatherBlockMutations(n.Body, stateVars, out)
		}
	}
}

// injectIntoNodeHandlers walks the visual tree and injects CanvasRedrawStmt
// into event handlers on non-canvas NodeInsts (e.g. button @click).
func injectIntoNodeHandlers(stmts []ir.Stmt, stateVars map[*ir.Var]bool, canvases []canvasEntry) {
	for _, s := range stmts {
		ni, ok := s.(*ir.NodeInst)
		if !ok {
			continue
		}
		if ni.CanvasDraw != nil {
			continue // canvas nodes themselves don't have user handlers
		}
		for i := range ni.Handlers {
			if ni.Handlers[i].Func == nil {
				continue
			}
			mutated := handlerMutatedVars(ni.Handlers[i].Func, stateVars)
			for _, e := range canvases {
				if varsOverlap(mutated, e.deps) {
					ni.Handlers[i].Func.Block = append(ni.Handlers[i].Func.Block, &ir.CanvasRedrawStmt{
						Canvas:   e.canvas,
						DrawFunc: e.canvas.CanvasDraw,
					})
				}
			}
		}
		injectIntoNodeHandlers(ni.Children, stateVars, canvases)
	}
}

func varsOverlap(a, b map[*ir.Var]bool) bool {
	for v := range a {
		if b[v] {
			return true
		}
	}
	return false
}
