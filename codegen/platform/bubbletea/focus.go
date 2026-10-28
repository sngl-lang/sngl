package bubbletea

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/ir"
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
		case *ir.If, *ir.Match:
			if !ir.ComputesValue(n) {
				for _, b := range ir.TransparentBlocks(n) {
					eachRenderedNode(*b, fn)
				}
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

// nodeKeyHandler is the body of n's handler for the key guard, and whether n
// handles it: an empty handler handles the key with nothing.
func nodeKeyHandler(n *ir.NodeInst, guard string) ([]ir.Stmt, bool) {
	for _, ev := range extractBlueprint(n).Events {
		if teaKeyGuard(ev.Key) != guard {
			continue
		}
		if h := codegen.NodeHandler(n, ev.On); h != nil && h.Func != nil {
			return h.Func.Block, true
		}
	}
	return nil, false
}

func handlesKey(n *ir.NodeInst, guard string) bool {
	_, ok := nodeKeyHandler(n, guard)
	return ok
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
	lf, ok := nodeLoopFocus(n)
	if !ok {
		return
	}
	if c := focusCounted(n); c != nil {
		vc.line("if %s {", vc.gc.EvalExpr(c))
		vc.line("\t%s++", lf.pos)
		vc.line("}")
		return
	}
	vc.line("%s++", lf.pos)
}

// focusCounted is the condition passFocusOrder put on a stop a loop renders
// under which it is one, or nil where it always is: the view numbers it, and
// Update and a test reach it, only while it holds.
func focusCounted(n *ir.NodeInst) ir.Expr {
	return codegen.NodeProp(n, "__focusCounted")
}

// counted writes body where n is a stop of its loop: under its condition, if
// it has one.
func (w *viewWalk) counted(n *ir.NodeInst, gc *golang.GoIRContext, body func()) {
	c := focusCounted(n)
	if c == nil {
		body()
		return
	}
	w.line("if %s {", gc.EvalExpr(c))
	w.indent++
	body()
	w.indent--
	w.line("}")
}

// emitLoopSlotHandlers' cases are closures so that finding the node the cursor
// names ends the walk however deep the loops are.
func emitLoopSlotHandlers(b *strings.Builder, f *ir.For, temps []ir.Stmt, info *irAnalysis, gc *golang.GoIRContext, guard string, invokerSink btInvokerSink) {
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
			if k := teaKeyGuard(ev.Key); k != "" && !seen[k] && handlesKey(n, k) {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	})
	// A test drives a copy of a node the loop renders through the ordinal
	// that copy has, which only a walk of the loop can say (loopCopies).
	if invokerSink != nil {
		eachRenderedNode([]ir.Stmt{f}, func(n *ir.NodeInst) {
			id := info.triggerID(n)
			if !isLoopFocus(n) || id == nil {
				return
			}
			for _, ev := range extractBlueprint(n).Events {
				if teaKeyGuard(ev.Key) == "" || !handlesKey(n, teaKeyGuard(ev.Key)) {
					continue
				}
				invokerSink(btEventInvoker{
					IDLabel: id.ID, SnglEvent: ev.On, SlotIdx: lf.slot, KeyExpr: teaKeyMsg(ev.Key),
					Cursor: lf.cursor, Copies: loopCopies(f, temps, lf, n, id, isLoopFocus, gc), Keyed: id.Prop(ir.CopyProp) != nil,
				})
			}
		})
	}
	for _, key := range keys {
		fmt.Fprintf(b, "\t\tcase %s && m.__focusID == %d%s:\n", key, lf.slot, guard)
		b.WriteString("\t\t\tfunc() {\n")
		w := &viewWalk{b: b, indent: 4, wants: isLoopFocus}
		w.visit = func(w *viewWalk, n *ir.NodeInst, gc *golang.GoIRContext) {
			w.counted(n, gc, func() {
				if block, ok := nodeKeyHandler(n, key); ok {
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
			})
		}
		w.line("%s := 0", lf.pos)
		w.stmts(append(slices.Clip(temps), f), gc)
		b.WriteString("\t\t\t}()\n")
	}
}

// copyOrdinal is the Go type of one copy of a looped node in loopCopies.
const copyOrdinal = "struct{ key string; pos int }"

// loopCopies is a Go expression of the ordinal each copy of the node n has in
// loop slot lf, in render order, beside the key the node a test names it by
// carries (named's ir.CopyProp; "" where no test indexes it): the loop walked
// as View walks it, counting the slot's focusable nodes, noting where n's
// copies fall. named is n itself, or the node triggerOwners found above it.
func loopCopies(f *ir.For, temps []ir.Stmt, lf loopFocus, n, named *ir.NodeInst, isLoopFocus func(*ir.NodeInst) bool, gc *golang.GoIRContext) string {
	var b strings.Builder
	b.WriteString("func() []" + copyOrdinal + " {\n")
	w := &viewWalk{b: &b, indent: 2, wants: isLoopFocus}
	w.visit = func(w *viewWalk, c *ir.NodeInst, gc *golang.GoIRContext) {
		w.counted(c, gc, func() {
			if c == n || (programID(n) && c.ID == n.ID) {
				key := `""`
				if k := named.Prop(ir.CopyProp); k != nil {
					key = gc.EvalExpr(k)
				}
				w.line("__at = append(__at, %s{%s, %s})", copyOrdinal, key, lf.pos)
			}
			w.line("%s++", lf.pos)
		})
	}
	w.line("var __at []%s", copyOrdinal)
	w.line("%s := 0", lf.pos)
	w.stmts(append(slices.Clip(temps), f), gc)
	w.line("return __at")
	b.WriteString("\t}()")
	return b.String()
}

// triggerOwners maps a node that handles a key, and carries no #id of its
// own, to the nearest node above it that does, where a test fires that event
// through that node: an override writes the control beneath the node a call
// site named, as a clickable card's focus handle is. Only a pairing a test in
// the program fires is kept, so nothing else gains an invoker.
func triggerOwners(stmts []ir.Stmt, triggered map[string]bool) map[*ir.NodeInst]*ir.NodeInst {
	out := map[*ir.NodeInst]*ir.NodeInst{}
	var walk func(stmts []ir.Stmt, owner *ir.NodeInst)
	walk = func(stmts []ir.Stmt, owner *ir.NodeInst) {
		for _, s := range stmts {
			inner := owner
			if n, ok := s.(*ir.NodeInst); ok {
				if programID(n) {
					inner = n
				} else if owner != nil {
					for _, ev := range extractBlueprint(n).Events {
						if triggered[owner.ID+"."+ev.On] {
							out[n] = owner
						}
					}
				}
			}
			for _, b := range ir.ViewBlocks(s) {
				walk(*b, inner)
			}
		}
	}
	walk(stmts, nil)
	return out
}

// programID reports whether n carries an #id the program wrote.
func programID(n *ir.NodeInst) bool {
	return n.ID != "" && !strings.HasPrefix(n.ID, "__n")
}

// triggerID is the node whose #id a test fires n's events through: n itself,
// or the node triggerOwners found for it. nil where there is none.
func (info *irAnalysis) triggerID(n *ir.NodeInst) *ir.NodeInst {
	if programID(n) {
		return n
	}
	return info.owners[n]
}
