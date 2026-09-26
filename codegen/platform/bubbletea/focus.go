package bubbletea

import (
	"fmt"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// loopFocus is what passFocusOrder put on a focusable node inside a loop slot:
// `__focusID == slot && cursor == pos`, where pos is the ordinal the view
// counts as it renders the slot's focusable nodes.
type loopFocus struct {
	slot   int
	cursor string
	pos    string
}

func nodeLoopFocus(n *ir.NodeInst) (loopFocus, bool) {
	and, ok := codegen.NodeProp(n, "__focused").(*ir.Binary)
	if !ok || and.Op != ast.BinAnd {
		return loopFocus{}, false
	}
	id, ok := and.Left.(*ir.Binary)
	if !ok || id.Op != ast.BinEq {
		return loopFocus{}, false
	}
	lit, ok := id.Right.(*ir.Literal)
	if !ok {
		return loopFocus{}, false
	}
	slot, err := strconv.Atoi(lit.Value)
	if err != nil {
		return loopFocus{}, false
	}
	at, ok := and.Right.(*ir.Binary)
	if !ok || at.Op != ast.BinEq {
		return loopFocus{}, false
	}
	cursor, ok := at.Left.(*ir.Ident)
	if !ok {
		return loopFocus{}, false
	}
	pos, ok := at.Right.(*ir.Ident)
	if !ok {
		return loopFocus{}, false
	}
	return loopFocus{slot: slot, cursor: cursor.Name, pos: pos.Name}, true
}

// eachRenderedNode visits the nodes under stmts that the view renders, in the
// order it renders them. A slot insertion's own children are not among them.
func eachRenderedNode(stmts []ir.Stmt, fn func(*ir.NodeInst)) {
	for _, st := range stmts {
		switch n := st.(type) {
		case *ir.NodeInst:
			fn(n)
			for _, s := range ir.SuppliedContent(n) {
				eachRenderedNode(s.Body, fn)
			}
		case *ir.For:
			eachRenderedNode(n.Body, fn)
			eachRenderedNode(n.Else, fn)
		case *ir.If:
			if !n.FromTernary {
				eachRenderedNode(n.Body, fn)
				eachRenderedNode(n.Else, fn)
			}
		case *ir.ErrorBoundary:
			eachRenderedNode(n.Children, fn)
		}
	}
}

func firstLoopFocus(stmts []ir.Stmt) (loopFocus, bool) {
	var found loopFocus
	ok := false
	eachRenderedNode(stmts, func(n *ir.NodeInst) {
		if lf, has := nodeLoopFocus(n); has && !ok {
			found, ok = lf, true
		}
	})
	return found, ok
}

func nodeKeyHandler(n *ir.NodeInst, guard string) []ir.Stmt {
	for _, ev := range extractBlueprint(n).Events {
		if teaKeyGuard(ev.Key) != guard {
			continue
		}
		if h := codegen.NodeHandler(n, ev.On); h != nil && h.Func != nil {
			return h.Func.Block
		}
	}
	return nil
}

// A nested loop of one slot finds its ordinal already declared.
func (vc *irViewContext) declareFocusPos(f *ir.For) {
	lf, ok := firstLoopFocus([]ir.Stmt{f})
	if !ok || vc.focusPos[lf.pos] {
		return
	}
	if vc.focusPos == nil {
		vc.focusPos = map[string]bool{}
	}
	vc.focusPos[lf.pos] = true
	vc.line("%s := 0", lf.pos)
	vc.line("_ = %s", lf.pos)
}

func (vc *irViewContext) declareLoopCounters(f *ir.For) {
	vc.declareFocusPos(f)
	vc.declareCanvasSeqs(f)
}

func (vc *irViewContext) countFocusPos(n *ir.NodeInst) {
	if lf, ok := nodeLoopFocus(n); ok {
		vc.line("%s++", lf.pos)
	}
}

// emitLoopSlotHandlers' cases are closures so that finding the node the cursor
// names ends the walk however deep the loops are.
func emitLoopSlotHandlers(b *strings.Builder, f *ir.For, info *irAnalysis, gc *golang.GoIRContext, guard string) {
	lf, ok := firstLoopFocus([]ir.Stmt{f})
	if !ok {
		return
	}
	isLoopFocus := func(n *ir.NodeInst) bool {
		_, ok := nodeLoopFocus(n)
		return ok
	}
	var keys []string
	seen := map[string]bool{}
	eachRenderedNode([]ir.Stmt{f}, func(n *ir.NodeInst) {
		if !isLoopFocus(n) {
			return
		}
		for _, ev := range extractBlueprint(n).Events {
			if k := teaKeyGuard(ev.Key); k != "" && !seen[k] && nodeKeyHandler(n, k) != nil {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	})
	for _, key := range keys {
		fmt.Fprintf(b, "\t\tcase %s && m.__focusID == %d%s:\n", key, lf.slot, guard)
		b.WriteString("\t\t\tfunc() {\n")
		w := &viewWalk{b: b, indent: 4, wants: isLoopFocus}
		w.visit = func(w *viewWalk, n *ir.NodeInst, gc *golang.GoIRContext) {
			if block := nodeKeyHandler(n, key); block != nil {
				w.line("if m.%s == %s {", lf.cursor, lf.pos)
				var body strings.Builder
				for _, stmt := range block {
					for _, l := range gc.EvalStmt(stmt) {
						fmt.Fprintf(&body, "%s%s\n", strings.Repeat("\t", w.indent+1), l)
					}
				}
				syncMutatedInputs(&body, block, info.widgets, info.binds, gc)
				w.b.WriteString(body.String())
				w.line("\treturn")
				w.line("}")
			}
			w.line("%s++", lf.pos)
		}
		w.line("%s := 0", lf.pos)
		w.stmts([]ir.Stmt{f}, gc)
		b.WriteString("\t\t\t}()\n")
	}
}
