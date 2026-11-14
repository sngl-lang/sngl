package lower

import (
	"fmt"
	"slices"

	"duckfam.us/sngl/ir"
)

// A canvas whose component declares a #[gen.protocol(redraw)] is painted
// through its protocol, as every other node of the platform is built: its
// drawing -- the statements passShapeDraw put in place of its shapes -- is
// the handler of its event whose one parameter is the context those
// statements paint through, connected by the component's `on` as any
// handler is, and each redraw passCanvasReactivity put in a body that writes
// what the drawing reads is the component's redraw of the node. What is left
// is a node like any other, so a platform asking for placement draws one
// with no generator of its own (win32's `Canvas`).
//
// Two passes. passCanvasPaint hands the drawing over once passShapeDraw has
// made it, and before anything flattens a canvas -- a render slot's body by
// passReactivity, a record's by passInstanceBodies, the rest by
// passDeclarative -- so the handler is lifted and its captures found as any
// handler's are. A canvas with no id is given one of its own, which its
// redraws name it by: `__drawingN`, since the passes that number nodes
// `__nN` between this and passDeclarative count from none of their own.
// passCanvasRedraw makes each redraw the protocol's call once
// passCanvasReactivity has put them in: a read of the node, as any `#id` a
// handler writes is.
var passCanvasPaint = pass{
	name:    "CanvasPaint",
	enabled: func(c Features) bool { return c.Canvas },
	apply:   lowerCanvasPaint,
}

var passCanvasRedraw = pass{
	name:    "CanvasRedraw",
	enabled: func(c Features) bool { return c.ReactiveCanvas },
	apply:   lowerCanvasRedraw,
}

func lowerCanvasPaint(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	p := &painter{}
	for _, o := range ir.Owners(pkg) {
		if o.Body != nil {
			p.findPainted(o.Body)
		}
	}
	return p.err
}

type painter struct {
	// n numbers the ids given canvases written with none.
	n   int
	err error
}

// paintsThrough reports whether canvas is painted through its protocol.
func paintsThrough(canvas *ir.NodeInst) bool {
	return canvas != nil && canvas.Component != nil && canvas.Component.Protocol.Op(ir.ProtocolRedraw) != nil
}

// findPainted hands the drawing of each canvas in stmts painted through its
// protocol to its paint event, reaching through what says when or how many,
// as walkCanvases does, and places what the drawing held that paints
// nothing -- an effect, a timer -- beside the canvas, after it.
func (p *painter) findPainted(stmts *[]ir.Stmt) {
	for i := 0; i < len(*stmts); i++ {
		switch v := (*stmts)[i].(type) {
		case *ir.NodeInst:
			if ir.IsShapeContainer(v) {
				if paintsThrough(v) {
					beside, err := p.paintThrough(v)
					if err != nil && p.err == nil {
						p.err = err
					}
					if len(beside) > 0 {
						*stmts = slices.Insert(*stmts, i+1, beside...)
						i += len(beside)
					}
				}
				continue
			}
			p.findPainted(&v.Children)
		case *ir.If, *ir.For, *ir.Match:
			for _, b := range ir.TransparentBlocks(v) {
				p.findPainted(b)
			}
		case *ir.ErrorBoundary:
			p.findPainted(&v.Children)
		}
	}
}

// paintThrough makes canvas's drawing the handler of its paint event, and
// gives it the id it is flattened as where it has none. What the drawing
// holds that paints nothing and is no shape -- an `effect` or a `timer`,
// which belongs to no tree -- it answers, for the canvas's parent to place:
// a lifetime is the component's, wherever it was written.
func (p *painter) paintThrough(canvas *ir.NodeInst) ([]ir.Stmt, error) {
	if canvas.ID == "" {
		canvas.ID = fmt.Sprintf("__drawing%d", p.n)
		p.n++
	}
	var draw, beside []ir.Stmt
	for _, s := range canvas.Children {
		if n, ok := s.(*ir.NodeInst); ok && (ir.IsTimerPrimitive(n.Component) || isEffectNode(n)) {
			beside = append(beside, s)
			continue
		}
		draw = append(draw, s)
	}
	canvas.Children = nil
	ctx := drawingContext(draw)
	if ctx == nil {
		// A drawing reading no context paints nothing.
		return beside, nil
	}
	for _, s := range draw {
		if n, ok := s.(*ir.NodeInst); ok {
			return beside, fmt.Errorf("%s: %s is painted through its protocol, and holds %s, which paints nothing", nodePos(n), canvas.Name, n.Name)
		}
	}
	for _, e := range canvas.Component.Events {
		if len(e.Params) == 1 && e.Params[0].Type != nil && e.Params[0].Type.Equal(ctx.Type) {
			fn := &ir.Func{Pkg: canvas.Component.Pkg, Params: []*ir.Param{ctx}, Block: draw}
			fn.Lowered.Paints = true
			canvas.Handlers = append(canvas.Handlers, ir.EventHandler{Name: e.Name, Func: fn})
			return beside, nil
		}
	}
	return beside, fmt.Errorf("%s: %s is painted through its protocol, and declares no event handed the context its shapes paint through", nodePos(canvas), canvas.Component.Name)
}

// drawingContext is the context draw paints through: the parameter
// passShapeDraw wrote every statement against, typed by the first shape
// handed it. Nil where no statement reads it.
func drawingContext(draw []ir.Stmt) *ir.Param {
	var ctx *ir.Param
	_ = ir.Walk(draw, func(n ir.Node) error {
		if id, ok := n.(*ir.Ident); ok && ctx == nil && id.Name == canvasCtxName {
			if p, ok := id.Sym.(*ir.Param); ok && p.Type != nil {
				ctx = p
			}
		}
		return nil
	})
	return ctx
}

// lowerCanvasRedraw makes each redraw of a canvas painted through its
// protocol the protocol's call of the node.
func lowerCanvasRedraw(pkg *ir.Package, f Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	var failed error
	redraw := func(stmts []ir.Stmt) []ir.Stmt {
		for i, s := range stmts {
			r, ok := s.(*ir.CanvasRedrawStmt)
			if !ok || !paintsThrough(r.Canvas) {
				continue
			}
			call, err := protocolSplice(r.Canvas.Component.Protocol.Redraw, []ir.Expr{handleRef(r.Canvas.ID, handleType(r.Canvas))}, f.language)
			if err != nil {
				if failed == nil {
					failed = fmt.Errorf("%s: %w", r.Canvas.Name, err)
				}
				continue
			}
			stmts[i] = through(call)[0]
		}
		return stmts
	}
	ir.RewriteBlocks(pkg, redraw)
	for _, fn := range closureFuncs(pkg) {
		ir.RewriteBlocks(fn, redraw)
	}
	return failed
}
