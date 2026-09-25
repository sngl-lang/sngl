package bubbletea

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// viewWalk writes Go that walks a view body the way View renders it -- every
// loop and branch around the nodes it wants, with the loops' variables bound --
// and hands each of those nodes to visit. It is how code outside View reaches
// a node the view renders once per iteration: Update finding the focused one,
// the transmit reaching each copy of a canvas.
type viewWalk struct {
	b      *strings.Builder
	indent int
	wants  func(*ir.NodeInst) bool
	visit  func(w *viewWalk, n *ir.NodeInst, gc *golang.GoIRContext)
}

func (w *viewWalk) line(format string, args ...any) {
	fmt.Fprintf(w.b, "%s"+format+"\n", append([]any{strings.Repeat("\t", w.indent)}, args...)...)
}

func (w *viewWalk) holds(stmts []ir.Stmt) bool {
	found := false
	eachRenderedNode(stmts, func(n *ir.NodeInst) {
		if w.wants(n) {
			found = true
		}
	})
	return found
}

func (w *viewWalk) stmts(stmts []ir.Stmt, gc *golang.GoIRContext) {
	for _, st := range stmts {
		if !w.holds([]ir.Stmt{st}) {
			continue
		}
		switch n := st.(type) {
		case *ir.NodeInst:
			if w.wants(n) {
				w.visit(w, n, gc)
			}
			for _, s := range ir.SuppliedContent(n) {
				w.stmts(s.Body, gc)
			}
		case *ir.For:
			w.loop(n, gc)
		case *ir.If:
			w.line("%s", gc.IfHead(n, gc.EvalExpr(n.Cond)))
			w.indent++
			w.stmts(n.Body, gc)
			w.indent--
			if w.holds(n.Else) {
				w.line("%s", gc.ElseHead())
				w.indent++
				w.stmts(n.Else, gc)
				w.indent--
			}
			w.line("%s", gc.BlockEnd())
		case *ir.ErrorBoundary:
			w.stmts(n.Children, gc)
		}
	}
}

func (w *viewWalk) loop(f *ir.For, gc *golang.GoIRContext) {
	iter := gc.EvalExpr(f.Iter)
	loopGC := gc
	if f.Key != "" && f.Key != "_" {
		loopGC = loopGC.WithLocal(f.Key)
	}
	if f.Value != "" && f.Value != "_" {
		loopGC = loopGC.WithLocal(f.Value)
	}
	if w.holds(f.Body) {
		w.line("%s", loopGC.ForHead(f, iter))
		w.indent++
		kept := golang.WithUnreadVarsDropped(f)
		if kept.Key != "" && kept.Key != "_" {
			w.line("_ = %s", kept.Key)
		}
		if kept.Value != "" && kept.Value != "_" {
			w.line("_ = %s", kept.Value)
		}
		w.stmts(f.Body, loopGC)
		w.indent--
		w.line("}")
	}
	if w.holds(f.Else) {
		w.line("if len(%s) == 0 {", iter)
		w.indent++
		w.stmts(f.Else, gc)
		w.indent--
		w.line("}")
	}
}

// collectLoopOverlays records each Overlay a loop renders. One is open when any
// copy of it is rendered, which only a walk of the loop can say: the gate that
// opens it reads the copy's own state. Escape closes the first open copy by
// clearing the var or cell its gate reads.
func collectLoopOverlays(f *ir.For, gate *ir.If, gc *golang.GoIRContext, out *[]overlayInfo) {
	gates := overlayGates([]ir.Stmt{f}, nil, map[*ir.NodeInst]*ir.If{})
	eachRenderedNode([]ir.Stmt{f}, func(n *ir.NodeInst) {
		if btIntrinsic(n) != "Overlay" {
			return
		}
		walk := func(visit func(w *viewWalk, gc *golang.GoIRContext)) string {
			var b strings.Builder
			w := &viewWalk{b: &b, indent: 1, wants: func(x *ir.NodeInst) bool { return x == n }}
			w.visit = func(w *viewWalk, _ *ir.NodeInst, gc *golang.GoIRContext) { visit(w, gc) }
			w.stmts([]ir.Stmt{f}, gc)
			return b.String()
		}
		open := "func() bool {\n" + walk(func(w *viewWalk, _ *golang.GoIRContext) { w.line("return true") }) + "\treturn false\n}()"
		if gate != nil && gate.Cond != nil {
			open = "(" + gc.EvalExpr(gate.Cond) + ") && " + open
		}
		oi := overlayInfo{openExpr: open}
		if clear := overlayClear(gates[n]); clear != nil {
			oi.closeCode = "func() {\n" + walk(func(w *viewWalk, gc *golang.GoIRContext) {
				for _, l := range gc.EvalStmt(clear) {
					w.line("%s", l)
				}
				w.line("return")
			}) + "}()"
		}
		*out = append(*out, oi)
	})
}

// overlayGates is the `if` each Overlay under stmts is written directly in.
func overlayGates(stmts []ir.Stmt, gate *ir.If, out map[*ir.NodeInst]*ir.If) map[*ir.NodeInst]*ir.If {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if btIntrinsic(n) == "Overlay" {
				out[n] = gate
			}
			overlayGates(n.Children, gate, out)
		case *ir.If:
			overlayGates(n.Body, n, out)
			overlayGates(n.Else, gate, out)
		case *ir.For:
			overlayGates(n.Body, gate, out)
			overlayGates(n.Else, gate, out)
		case *ir.ErrorBoundary:
			overlayGates(n.Children, gate, out)
		}
	}
	return out
}

// overlayClear is the write that closes an overlay whose gate reads one bool
// -- a var, or a per-copy cell read as `cell.get(key, init)` -- or nil.
func overlayClear(gate *ir.If) ir.Stmt {
	if gate == nil {
		return nil
	}
	off := &ir.Literal{Type: ir.TypBool, Value: "false"}
	switch c := gate.Cond.(type) {
	case *ir.Ident:
		if _, ok := c.Sym.(*ir.Var); ok {
			return &ir.Assign{Target: c, Op: ast.AssignSet, Value: off}
		}
	case *ir.Call:
		if c.Func != nil && c.Func.Intrinsic == "map.get" && len(c.Args) == 3 {
			if cell, ok := c.Args[0].Value.(*ir.Ident); ok {
				return &ir.Assign{
					Target: &ir.Index{Type: ir.TypBool, Operand: cell, Idx: c.Args[1].Value},
					Op:     ast.AssignSet,
					Value:  off,
				}
			}
		}
	}
	return nil
}
