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
	if !pkg.UsesDrawShapes() {
		return nil
	}
	// A package-level var is state a body may write like any other, and
	// passReactivity already patches the widgets that read one. Leaving it out
	// here meant a canvas drawn from one had an empty dep set, so it was not
	// collected at all and nothing redrew it -- not a timer, and not a button
	// either.
	pkgVars := mutableVars(pkg.Vars)
	for _, comp := range pkg.Components {
		stateVars := mergeVarSets(pkgVars, mutableVars(comp.Vars))
		injectCanvasRedraws(comp.Body, comp.Vars, stateVars, &comp.Funcs)
	}
	for _, w := range pkg.Windows {
		stateVars := mergeVarSets(pkgVars, mutableVars(w.Vars))
		injectCanvasRedraws(w.Body, w.Vars, stateVars, &w.Funcs)
	}
	return nil
}

// mutableVars is the set of vars a body may write: every declared one that is
// not a const.
func mutableVars(vars []*ir.Var) map[*ir.Var]bool {
	out := make(map[*ir.Var]bool, len(vars))
	for _, v := range vars {
		if !v.IsConst {
			out[v] = true
		}
	}
	return out
}

// mergeVarSets returns the union of two var sets, sharing neither.
func mergeVarSets(a, b map[*ir.Var]bool) map[*ir.Var]bool {
	out := make(map[*ir.Var]bool, len(a)+len(b))
	for v := range a {
		out[v] = true
	}
	for v := range b {
		out[v] = true
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

	// The owner's own funcs, which by now include the bodies passEffect
	// synthesized. An effect's `@mount` and a timer's `@tick` are not node
	// handlers and reach none of the walks above, so a canvas driven by one
	// was redrawn by nobody -- the state advanced and the drawing held still.
	if funcs != nil {
		injectIntoFuncs(*funcs, stateVars, canvases)
	}
}

type canvasEntry struct {
	canvas *ir.NodeInst
	deps   map[*ir.Var]bool
}

// collectCanvases walks stmts recursively and appends each canvas NodeInst
// (CanvasDraw != nil) with its reactive dep set to out.
func collectCanvases(stmts []ir.Stmt, stateVars map[*ir.Var]bool, out *[]canvasEntry) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if n.CanvasDraw != nil {
				deps := drawFuncStateVars(n.CanvasDraw, stateVars)
				if len(deps) > 0 {
					*out = append(*out, canvasEntry{canvas: n, deps: deps})
				}
				continue
			}
			collectCanvases(n.Children, stateVars, out)
		// A canvas is as likely to be one arm of a target test or an entry in
		// a list as it is to be a plain child.
		case *ir.If:
			collectCanvases(n.Body, stateVars, out)
			collectCanvases(n.Else, stateVars, out)
		case *ir.For:
			collectCanvases(n.Body, stateVars, out)
			collectCanvases(n.Else, stateVars, out)
		case *ir.Window:
			collectCanvases(n.Body, stateVars, out)
		}
	}
}

// drawFuncStateVars collects the state vars read by a synthesized canvas draw
// func by walking its draw calls.
//
// Nested statements count: a canvas whose shapes come from a `for` puts every
// one of its calls inside the loop, and reading only the top level found no
// dependency at all — so the page drew the display once and never again.
func drawFuncStateVars(fn *ir.Func, stateVars map[*ir.Var]bool) map[*ir.Var]bool {
	out := make(map[*ir.Var]bool)
	var walk func(stmts []ir.Stmt)
	walk = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.CallStmt:
				if n.Call == nil {
					continue
				}
				for _, arg := range n.Call.Args {
					gatherStateVarRefs(arg.Value, stateVars, out)
				}
			case *ir.If:
				gatherStateVarRefs(n.Cond, stateVars, out)
				walk(n.Body)
				walk(n.Else)
			case *ir.For:
				gatherStateVarRefs(n.Iter, stateVars, out)
				walk(n.Body)
				walk(n.Else)
			}
		}
	}
	walk(fn.Block)
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
		case *ir.CallStmt:
			// A handler that calls an action writes whatever the action
			// writes. Handler funcs are synthesized during lowering, after the
			// checker filled in Writes, so theirs is empty and the write has
			// to be read off the callee -- `@click { press(k) }` mutates
			// everything `press` does.
			if n.Call != nil && n.Call.Func != nil {
				for _, v := range n.Call.Func.Writes {
					if stateVars[v] {
						out[v] = true
					}
				}
			}
		case *ir.If:
			gatherBlockMutations(n.Body, stateVars, out)
			gatherBlockMutations(n.Else, stateVars, out)
		case *ir.For:
			gatherBlockMutations(n.Body, stateVars, out)
			gatherBlockMutations(n.Else, stateVars, out)
		}
	}
}

// injectIntoNodeHandlers walks the visual tree and injects CanvasRedrawStmt
// into event handlers on non-canvas NodeInsts (e.g. button @click).
func injectIntoNodeHandlers(stmts []ir.Stmt, stateVars map[*ir.Var]bool, canvases []canvasEntry) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if n.CanvasDraw != nil {
				continue // canvas nodes themselves don't have user handlers
			}
			for i := range n.Handlers {
				if n.Handlers[i].Func == nil {
					continue
				}
				mutated := handlerMutatedVars(n.Handlers[i].Func, stateVars)
				for _, e := range canvases {
					if varsOverlap(mutated, e.deps) {
						n.Handlers[i].Func.Block = append(n.Handlers[i].Func.Block, &ir.CanvasRedrawStmt{
							Canvas:   e.canvas,
							DrawFunc: e.canvas.CanvasDraw,
						})
					}
				}
			}
			injectIntoNodeHandlers(n.Children, stateVars, canvases)
		case *ir.If:
			injectIntoNodeHandlers(n.Body, stateVars, canvases)
			injectIntoNodeHandlers(n.Else, stateVars, canvases)
		case *ir.For:
			injectIntoNodeHandlers(n.Body, stateVars, canvases)
			injectIntoNodeHandlers(n.Else, stateVars, canvases)
		case *ir.Window:
			injectIntoNodeHandlers(n.Body, stateVars, canvases)
		}
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

// injectIntoFuncs appends a redraw to each of an owner's funcs that writes a
// var a canvas draws from, and to each lambda body inside one.
//
// The lambda half is where a timer lands: a tick reaches the emitter as the
// closure handed to the host scheduler, so the write is a statement of the
// lambda and not of the func holding it. gatherBlockMutations does not descend
// into a lambda either, which is what keeps the two from both firing -- a func
// holding a writing lambda is not itself seen to write.
//
// Only the funcs lowering synthesized get one of their own. gatherBlockMutations
// credits a call to its callee's Writes, so a handler calling `bump()` already
// redraws for what bump writes -- injecting into bump as well rasterizes the
// surface twice per click, the first time against half-applied state. Writes is
// what tells the two apart: the checker fills it in, so a body that did not
// exist then has none.
//
// A lambda is asked separately and always, because nothing credits one to a
// caller: what runs it is a host scheduler rather than code this pass can read.
func injectIntoFuncs(funcs []*ir.Func, stateVars map[*ir.Var]bool, canvases []canvasEntry) {
	for _, fn := range funcs {
		if fn == nil {
			continue
		}
		if len(fn.Writes) == 0 {
			redrawIfWrites(&fn.Block, stateVars, canvases)
		}
		for _, l := range lambdaFuncsIn(fn.Block) {
			redrawIfWrites(&l.Block, stateVars, canvases)
		}
	}
}

// redrawIfWrites appends one redraw per canvas whose draw reads a var block
// writes.
func redrawIfWrites(block *[]ir.Stmt, stateVars map[*ir.Var]bool, canvases []canvasEntry) {
	mutated := map[*ir.Var]bool{}
	gatherBlockMutations(*block, stateVars, mutated)
	for _, e := range canvases {
		if varsOverlap(mutated, e.deps) {
			*block = append(*block, &ir.CanvasRedrawStmt{
				Canvas:   e.canvas,
				DrawFunc: e.canvas.CanvasDraw,
			})
		}
	}
}

// lambdaFuncsIn returns every lambda body inside block, at any depth. Flat
// rather than recursive on purpose: a nested pair is then visited once each by
// the caller instead of once per level of nesting.
func lambdaFuncsIn(block []ir.Stmt) []*ir.Func {
	var out []*ir.Func
	_ = ir.Walk(block, func(n ir.Node) error {
		if l, ok := n.(*ir.Lambda); ok && l.Func != nil {
			out = append(out, l.Func)
		}
		return nil
	})
	return out
}
