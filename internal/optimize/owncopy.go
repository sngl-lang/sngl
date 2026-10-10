package optimize

import (
	"maps"

	"duckfam.us/sngl/ir"
)

// ownCopy is stmts copied for a fold to rewrite, every loop body in it still
// shared: an unroll copies a body per iteration, so copying it here as well
// was spent on loops about to be replaced and on branches about to be
// dropped, which in an html document was nearly all of it. A loop the fold
// keeps copies its body before folding it.
//
// What the copy holds that a shared body may name -- a boundary's handler, a
// canvas -- is recorded on ctx, so a body copied later in the same region
// names the copies rather than the originals.
func (ctx *evalCtx) ownCopy(stmts []ir.Stmt) []ir.Stmt {
	if stmts == nil {
		return nil
	}
	c := &stmtCloner{shareLoops: true, handlers: map[*ir.EventHandler]*ir.EventHandler{}}
	out := c.stmts(stmts)
	handlers := ctx.copiedHandlers
	if len(c.handlers) > 0 {
		handlers = maps.Clone(handlers)
		if handlers == nil {
			handlers = map[*ir.EventHandler]*ir.EventHandler{}
		}
		maps.Copy(handlers, c.handlers)
	}
	canvases := ctx.copiedCanvases
	if ctx.redraws {
		if own := pairCanvases(stmts, out); len(own) > 0 {
			canvases = maps.Clone(canvases)
			if canvases == nil {
				canvases = map[*ir.NodeInst]*ir.NodeInst{}
			}
			maps.Copy(canvases, own)
		}
	}
	if len(handlers)+len(canvases) > 0 {
		repointCopied(out, handlers, canvases)
	}
	ctx.copiedHandlers, ctx.copiedCanvases = handlers, canvases
	return out
}

// pairCanvases maps each node of orig outside a loop body to its copy in cp,
// which a sharing clone copied structurally, so the two walks agree.
func pairCanvases(orig, cp []ir.Stmt) map[*ir.NodeInst]*ir.NodeInst {
	nodes := func(stmts []ir.Stmt) []*ir.NodeInst {
		var out []*ir.NodeInst
		_ = ir.Walk(stmts, func(n ir.Node) error {
			switch x := n.(type) {
			case *ir.NodeInst:
				out = append(out, x)
			case *ir.For:
				return ir.SkipDir
			}
			return nil
		})
		return out
	}
	from, to := nodes(orig), nodes(cp)
	if len(from) != len(to) {
		return nil
	}
	out := make(map[*ir.NodeInst]*ir.NodeInst, len(from))
	for i, n := range from {
		out[n] = to[i]
	}
	return out
}

// repointCopied points the aliases in root at the copies of what they name:
// a catch block and a resolved raise at a copied handler, a redraw at a copied
// canvas. It stops at a loop body, which a sharing clone left shared.
func repointCopied(root []ir.Stmt, handlers map[*ir.EventHandler]*ir.EventHandler, canvases map[*ir.NodeInst]*ir.NodeInst) {
	seen := map[*ir.EventHandler]bool{}
	var visit func(n ir.Node) error
	visit = func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.For:
			_ = ir.Walk(x.Iter, visit)
			return ir.SkipDir
		case *ir.Call:
			if h, ok := handlers[x.ResolvedHandler]; ok {
				x.ResolvedHandler = h
			}
		case *ir.CanvasRedrawStmt:
			if c, ok := canvases[x.Canvas]; ok {
				x.Canvas = c
			}
		case *ir.If:
			if h, ok := handlers[x.Catch]; ok {
				x.Catch = h
			}
			// The walk does not follow a catch block's handler, and once a
			// boundary with no fallback is spliced away it is the handler's
			// only route; see repointRedraws.
			if h := x.Catch; h != nil && h.Func != nil && !seen[h] && len(canvases) > 0 {
				seen[h] = true
				_ = ir.Walk(h.Func.Block, visit)
			}
		}
		return nil
	}
	_ = ir.Walk(root, visit)
}

func holdsRedraw(pkg *ir.Package) bool {
	found := false
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if _, ok := n.(*ir.CanvasRedrawStmt); ok {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// ownHandler is the copy of a catch block's handler this region folds. The
// handler is a boundary's or a window's, reached by alias from every catch
// block that resolved to it, so it is copied once and the copy folded once.
func (ctx *evalCtx) ownHandler(h *ir.EventHandler) *ir.EventHandler {
	if c, ok := ctx.copiedHandlers[h]; ok {
		return c
	}
	for _, c := range ctx.copiedHandlers {
		if c == h {
			return h
		}
	}
	hc := *h
	fn := *h.Func
	hc.Func = &fn
	handlers := maps.Clone(ctx.copiedHandlers)
	if handlers == nil {
		handlers = map[*ir.EventHandler]*ir.EventHandler{}
	}
	handlers[h] = &hc
	ctx.copiedHandlers = handlers
	fn.Block = foldStmts(ctx.ownCopy(h.Func.Block), ctx)
	return &hc
}
