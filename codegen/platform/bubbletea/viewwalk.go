package bubbletea

import (
	"fmt"
	"strings"

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
			w.stmts(n.Children, gc)
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
