package bubbletea

import (
	"fmt"
	"strconv"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/ir"
)

// A scroll is a Layout given a `scroll` axis: the view cuts its content to
// the extent the box is given at the offset the Model's tui.Scrolls keeps
// for it, known by the node and the copy of each loop around it. The wheel
// over it scrolls it, PgUp and PgDn page the one focus is in, and a key that
// moves focus brings the stop it lands on into view. A drag is left to the
// terminal, which selects text with it.
//
// `@reachEnd` is asked of the view, which alone knows how big the content
// came out, and only Update may run a handler: the view signals `__wake`, as
// one drawing a sleeping widget does, and Update's answer runs the handler of
// each scroll whose end has come into view at a size not yet reported. A
// scroll a loop renders has no handler there, since Update has no copy's
// loop variables to run it with.

// scrollsField is the modelState field holding the tui.Scrolls.
const scrollsField = "__scrolls"

// scrollInfo is one scroll the view draws.
type scrollInfo struct {
	idx   int
	loops []*ir.For
	// reach is the handler of its `@reachEnd`, which Update runs; nil where
	// none is written, or the scroll is in a loop.
	reach *ir.EventHandler
}

// scrollAxisOf is the axis a Layout scrolls along, false for one that does
// not.
func scrollAxisOf(n *ir.NodeInst) (int, bool) {
	if n == nil || btIntrinsic(n) != "Layout" {
		return 0, false
	}
	v, ok := codegen.IRLiteralNumber(codegen.NodeProp(n, "scroll"))
	if !ok || v < 0 {
		return 0, false
	}
	return int(v), true
}

// collectScrolls numbers the scrolls the view and the components draw, and
// fixes the copy key of each loop around one before any head is written, as
// a widget's is (newLoopWidget).
func collectScrolls(view []ir.Stmt, pkg *ir.Package, info *irAnalysis) {
	info.scrolls = map[*ir.NodeInst]*scrollInfo{}
	loops := widgetLoops(view)
	add := func(n *ir.NodeInst) {
		if _, ok := scrollAxisOf(n); !ok || info.scrolls[n] != nil {
			return
		}
		si := &scrollInfo{idx: len(info.scrolls), loops: loops[n]}
		if len(si.loops) > 0 {
			lower.CopyKey(si.loops)
			for _, l := range si.loops {
				l.KeyRead = true
			}
		} else if h := codegen.NodeHandler(n, "reachEnd"); h != nil && h.Func != nil && len(h.Func.Block) > 0 {
			si.reach = h
			info.reaches = true
		}
		info.scrolls[n] = si
	}
	visit := func(x ir.Node) error {
		if n, ok := x.(*ir.NodeInst); ok {
			add(n)
		}
		return nil
	}
	_ = ir.Walk(view, visit)
	for _, c := range pkg.Components {
		_ = ir.Walk(c.Body.Stmts, visit)
	}
}

// scrollKey is the Go of the key the Scrolls knows n by.
func (vc *irViewContext) scrollKey(si *scrollInfo) string {
	key := strconv.Quote(strconv.Itoa(si.idx))
	if len(si.loops) > 0 {
		key += ` + "/" + ` + vc.gc.EvalExpr(lower.CopyIdentity(si.loops))
	}
	return key
}

// clipScroll cuts resultVar, a scroll's whole content, to the viewport its
// look leaves of the extent w by h it is given.
func (vc *irViewContext) clipScroll(n *ir.NodeInst, resultVar, style, w, h string) {
	axis, ok := scrollAxisOf(n)
	si := vc.scrolls[n]
	if !ok || si == nil {
		return
	}
	vc.requireImport(tuiImportPath)
	vw, vh := resultVar+"VW", resultVar+"VH"
	vc.line("%s, %s := tui.Inner(%s, %s, %s)", vw, vh, style, extent(w), extent(h))
	focus := "tui.Box{}, false"
	if vc.hasFocus {
		focus = fmt.Sprintf("m.%s.Find(%s, m.__isFocused)", clicksField, resultVar)
	}
	vc.line("%sFocus, %sFocused := %s", resultVar, resultVar, focus)
	vc.line("%s = m.%s.View(%s, %s, %s, %s, %d, %sFocus, %sFocused, %t)", resultVar, scrollsField, vc.scrollKey(si), resultVar, vw, vh, axis, resultVar, resultVar, si.reach != nil)
}

// emitScrollFields writes the Scrolls field where the view draws a scroll.
func emitScrollFields(b *strings.Builder, info *irAnalysis) {
	if len(info.scrolls) > 0 {
		fmt.Fprintf(b, "\t%s *tui.Scrolls\n", scrollsField)
	}
}

func emitScrollInit(b *strings.Builder, info *irAnalysis) {
	if len(info.scrolls) > 0 {
		fmt.Fprintf(b, "\tm.%s = tui.NewScrolls()\n", scrollsField)
	}
}

// emitScrollWheel writes Update's case for the wheel, which scrolls what is
// under it.
func emitScrollWheel(b *strings.Builder, info *irAnalysis) {
	if len(info.scrolls) == 0 {
		return
	}
	b.WriteString("\tcase tea.MouseWheelMsg:\n")
	fmt.Fprintf(b, "\t\tm.%s.Wheel(msg)\n", scrollsField)
}

// emitScrollKeys writes the key cases that page the scroll focus is in,
// which a key no case before them took reaches.
func emitScrollKeys(b *strings.Builder, info *irAnalysis) {
	if len(info.scrolls) == 0 {
		return
	}
	fmt.Fprintf(b, "\t\tcase (msg.Code == tea.KeyPgUp || msg.Code == tea.KeyPgDown) && m.%s.Key(msg):\n", scrollsField)
}

// emitScrollFollow writes, in a case that moves focus by a key, the ask that
// the next view bring the stop it moves to into view.
func emitScrollFollow(b *strings.Builder, info *irAnalysis, indent string) {
	if len(info.scrolls) > 0 {
		fmt.Fprintf(b, "%sm.%s.Follow()\n", indent, scrollsField)
	}
}

// emitScrollViewStart and emitScrollViewEnd bracket the view: every scroll
// is marked again, and once the view is whole the follow it did is done and
// a scroll whose end came into view wakes Update to report it.
func emitScrollViewStart(vc *irViewContext, info *irAnalysis) {
	if len(info.scrolls) > 0 {
		vc.line("m.%s.Reset()", scrollsField)
	}
}

func emitScrollViewEnd(b *strings.Builder, info *irAnalysis) {
	if len(info.scrolls) == 0 {
		return
	}
	fmt.Fprintf(b, "\tm.%s.Settle()\n", scrollsField)
	if info.reaches {
		fmt.Fprintf(b, "\tif m.%s.Waiting() {\n\t\tselect {\n\t\tcase m.%s <- struct{}{}:\n\t\tdefault:\n\t\t}\n\t}\n", scrollsField, wakeField)
	}
}

// emitScrollReach writes, in Update's answer to a wake, each `@reachEnd`
// handler whose scroll is due.
func emitScrollReach(b *strings.Builder, info *irAnalysis, gc goEvaluator) {
	for _, si := range sortedScrolls(info) {
		if si.reach == nil {
			continue
		}
		fmt.Fprintf(b, "\t\tif m.%s.Due(%q) {\n", scrollsField, strconv.Itoa(si.idx))
		for _, stmt := range si.reach.Func.Block {
			for _, line := range gc.EvalStmt(stmt) {
				fmt.Fprintf(b, "\t\t\t%s\n", line)
			}
		}
		b.WriteString("\t\t}\n")
	}
}

// goEvaluator is what emitScrollReach writes a handler's statements with.
type goEvaluator interface {
	EvalStmt(ir.Stmt) []string
}

func sortedScrolls(info *irAnalysis) []*scrollInfo {
	out := make([]*scrollInfo, len(info.scrolls))
	for _, si := range info.scrolls {
		out[si.idx] = si
	}
	return out
}

// emitIsFocused writes __isFocused, whether a stop a view marked is the one
// focus is on, which a scroll asks of its content (tui.Clicks.Find).
func emitIsFocused(b *strings.Builder, info *irAnalysis) {
	if len(info.scrolls) == 0 || !info.hasFocus {
		return
	}
	fmt.Fprintf(b, "// __isFocused reports whether s is the stop focus is on.\nfunc (m Model) __isFocused(s %s) bool {\n", stopType)
	b.WriteString("\tif s.slot != m.__focusID {\n\t\treturn false\n\t}\n")
	if cursors := loopCursors(info); len(cursors) > 0 {
		b.WriteString("\tswitch s.slot {\n")
		for _, c := range cursors {
			slot := strings.TrimSuffix(strings.TrimPrefix(c, "__focusLoop"), "_cursor")
			fmt.Fprintf(b, "\tcase %s:\n\t\treturn s.pos == m.%s\n", slot, c)
		}
		b.WriteString("\t}\n")
	}
	b.WriteString("\treturn true\n}\n\n")
}
