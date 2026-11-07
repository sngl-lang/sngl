package bubbletea

import (
	"fmt"
	"slices"
	"strings"

	"duckfam.us/sngl/ir"
)

// A click presses the focus stop under it. The view marks each stop's block
// as it draws it (tui.Clicks), carrying the stop's slot, its ordinal in a loop
// slot, and the key that presses it -- an Event record's, enter for a button
// and space for a checkbox; a text field has none, and a click only focuses
// it. Update answers a left click by moving focus to the stop and sending that
// key through itself, which is the route a key pressed on the stop takes, so a
// handler a loop's copy runs reads that copy, and an overlay's freeze holds a
// click as it holds a key. A stop Tab passes over (`focusable=false`) gives
// focus back once it is pressed.

// stopType is the Go type of what a view marks.
const stopType = "snglStop"

// clicksField is the modelState field holding what the last view drew.
const clicksField = "__clicks"

// pressingField says a click is pressing a stop Tab passes over, whose key
// Update takes though focus is not shown on it.
const pressingField = "__pressing"

// stopKey is the message that presses n, "" for a stop a click only focuses.
func stopKey(n *ir.NodeInst) string {
	for _, ev := range extractBlueprint(n).Events {
		if k := teaKeyMsg(ev.Key); k != "" {
			return k
		}
	}
	return ""
}

// markStop wraps resultVar, n's rendered block, in the mark a click finds it
// by, where n is a focus stop.
func (vc *irViewContext) markStop(n *ir.NodeInst, resultVar string) {
	vc.markStopAt(n, resultVar, "")
}

// markStopAt is markStop with the copy of a loop's stop read from at, where
// at is not "": a block marked after its stop was drawn reads the copy the
// stop was, which the loop's position has moved past by then.
func (vc *irViewContext) markStopAt(n *ir.NodeInst, resultVar, at string) {
	slot, pos := nodeFocusSlotIdx(n), "0"
	if lf, ok := nodeLoopFocus(n); ok {
		slot, pos = lf.slot, lf.pos
		if at != "" {
			pos = at
		}
	}
	if slot < 0 {
		return
	}
	key := stopKey(n)
	press := "false"
	if key == "" {
		key = "tea.KeyPressMsg{}"
	} else {
		press = "true"
	}
	vc.line("%s = m.%s.Mark(%s{slot: %d, pos: %s, press: %s, key: %s}, %s)", resultVar, clicksField, stopType, slot, pos, press, key, resultVar)
}

// emitClickDecls writes the stop a view marks.
func emitClickDecls(b *strings.Builder) {
	fmt.Fprintf(b, "// %s is a focus stop a click presses: the slot and copy focus moves to,\n", stopType)
	b.WriteString("// and the key that presses it there, where press says it has one.\n")
	fmt.Fprintf(b, "type %s struct {\n\tslot, pos int\n\tpress    bool\n\tkey      tea.KeyPressMsg\n}\n\n", stopType)
}

// emitClickFields writes the modelState fields a click reads.
func emitClickFields(b *strings.Builder, info *irAnalysis) {
	fmt.Fprintf(b, "\t%s tui.Clicks[%s]\n", clicksField, stopType)
	if info.settles {
		fmt.Fprintf(b, "\t%s bool\n", pressingField)
	}
}

// emitClickCase writes Update's case for a mouse click. One on an open
// float is the widget's it floats over, which forwarding hands it, rather
// than a press of what the float covers.
func emitClickCase(b *strings.Builder, info *irAnalysis) {
	b.WriteString("\tcase tea.MouseClickMsg:\n")
	if info.floats {
		fmt.Fprintf(b, "\t\tif msg.Button == tea.MouseLeft && !m.%s.At(msg.X, msg.Y) {\n", floatsField)
	} else {
		b.WriteString("\t\tif msg.Button == tea.MouseLeft {\n")
	}
	fmt.Fprintf(b, "\t\t\tif s, ok := m.%s.At(msg.X, msg.Y); ok {\n", clicksField)
	b.WriteString("\t\t\t\tcmds = append(cmds, m.__press(s))\n")
	b.WriteString("\t\t\t}\n")
	b.WriteString("\t\t}\n")
}

// loopCursors is the cursor field of each loop slot, which a press moves and
// a stop Tab passes over puts back.
func loopCursors(info *irAnalysis) []string {
	var out []string
	for _, bd := range info.binds {
		if strings.HasPrefix(bd.name, "__focusLoop") && strings.HasSuffix(bd.name, "_cursor") {
			out = append(out, bd.name)
		}
	}
	return out
}

// emitPress writes __press, a click on a stop.
func emitPress(b *strings.Builder, info *irAnalysis) {
	cursors := loopCursors(info)
	b.WriteString("// __press is a click on s: focus moves to it and s's key is pressed there,\n")
	b.WriteString("// as a key pressed on the stop Tab reached is. A stop Tab passes over gives\n")
	b.WriteString("// focus back once it is pressed.\n")
	fmt.Fprintf(b, "func (m Model) __press(s %s) tea.Cmd {\n", stopType)
	saved := append([]string{"__focusID"}, cursors...)
	if info.settles {
		fmt.Fprintf(b, "\tprev := [...]int{%s}\n", strings.Join(prefixed("m.", saved), ", "))
	}
	b.WriteString("\tm.__focusID = s.slot\n")
	if len(cursors) > 0 {
		b.WriteString("\tswitch s.slot {\n")
		for _, c := range cursors {
			slot := strings.TrimSuffix(strings.TrimPrefix(c, "__focusLoop"), "_cursor")
			fmt.Fprintf(b, "\tcase %s:\n\t\tm.%s = s.pos\n", slot, c)
		}
		b.WriteString("\t}\n")
	}
	if info.settles {
		b.WriteString("\ttabbed := m.__focusShown()\n")
	}
	var sync strings.Builder
	emitIRFocusSync(&sync, info.widgets)
	b.WriteString(dedent(sync.String(), 2))
	b.WriteString("\tvar cmd tea.Cmd\n")
	// The key is the press's and no key the window hears.
	flags := []string{}
	if info.settles {
		flags = append(flags, pressingField)
	}
	if screenKeyHandler(info) != nil {
		flags = append(flags, unheardField)
	}
	b.WriteString("\tif s.press {\n")
	for _, f := range flags {
		fmt.Fprintf(b, "\t\tm.%s = true\n", f)
	}
	b.WriteString("\t\t_, cmd = m.Update(s.key)\n")
	for _, f := range flags {
		fmt.Fprintf(b, "\t\tm.%s = false\n", f)
	}
	b.WriteString("\t}\n")
	if info.settles {
		b.WriteString("\tif !tabbed {\n")
		for i, f := range saved {
			fmt.Fprintf(b, "\t\tm.%s = prev[%d]\n", f, i)
		}
		b.WriteString(dedent(sync.String(), 1))
		b.WriteString("\t}\n")
	}
	b.WriteString("\treturn cmd\n")
	b.WriteString("}\n\n")
}

// pressGuard is what each key's activation case adds where some stop may not
// be shown: the stop focus is on takes its key while it is, or while a click
// is pressing it.
func pressGuard(info *irAnalysis) string {
	if !info.settles {
		return ""
	}
	return fmt.Sprintf(" && (m.%s || m.__focusShown())", pressingField)
}

func prefixed(p string, names []string) []string {
	out := slices.Clone(names)
	for i := range out {
		out[i] = p + out[i]
	}
	return out
}

// dedent takes n tabs off the front of each of s's lines: emitIRFocusSync
// writes its lines for a case three deep.
func dedent(s string, n int) string {
	cut := strings.Repeat("\t", n)
	lines := strings.SplitAfter(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimPrefix(l, cut)
	}
	return strings.Join(lines, "")
}

// emitClickScan writes the view's scan of its marks: the content's, before an
// overlay is composited over it, since compositing draws cells and keeps no
// escape a terminal is not sent; then each open overlay's box where it lands,
// which covers what was found beneath it.
//
// A float's marks are taken out the same way, for Draw to lay each float
// over everything once the overlays are down (emitFloatsDraw).
func emitClickScan(b *strings.Builder, overlays []pendingOverlay, floats bool) {
	fmt.Fprintf(b, "\tcontent = m.%s.Scan(content, 0, 0)\n", clicksField)
	if floats {
		fmt.Fprintf(b, "\tcontent = m.%s.Scan(content, 0, 0)\n", floatsField)
	}
	for _, ov := range overlays {
		fmt.Fprintf(b, "\tif %s != \"\" {\n", ov.boxVar)
		fmt.Fprintf(b, "\t\tm.%s.Cover()\n", clicksField)
		fmt.Fprintf(b, "\t\tx, y := tui.OverlayAt(content, %s, %s, m.width, m.height)\n", ov.boxVar, ov.placementExpr)
		fmt.Fprintf(b, "\t\t%s = m.%s.Scan(%s, x, y)\n", ov.boxVar, clicksField, ov.boxVar)
		if floats {
			fmt.Fprintf(b, "\t\t%s = m.%s.Scan(%s, x, y)\n", ov.boxVar, floatsField, ov.boxVar)
		}
		b.WriteString("\t}\n")
	}
}

// floatsField is the modelState field holding the tui.Floats a widget's
// templates name, which an open select draws its popover through.
const floatsField = "__floats"

// usesFloats reports whether w's templates hand it the Model's Floats.
func usesFloats(w widgetInfo) bool {
	return strings.Contains(w.model.View+w.model.Update, "m."+floatsField)
}

// emitFloatsDraw writes the view's last step where a float may be open:
// each laid over the finished view, below the block it floats from.
func emitFloatsDraw(b *strings.Builder, info *irAnalysis) {
	if info.floats {
		fmt.Fprintf(b, "\tcontent = m.%s.Draw(content, m.width, m.height)\n", floatsField)
	}
}

// emitFloatsGuard writes what Update does first while a float is open: Esc
// and a click outside it close it and go no further, so neither goes back a
// page nor presses what the float covers, and Tab closes it on its way to
// the next stop.
func emitFloatsGuard(b *strings.Builder, info *irAnalysis) {
	if !info.floats {
		return
	}
	fmt.Fprintf(b, "\tif m.%s.Open() {\n", floatsField)
	b.WriteString("\t\tswitch msg := msg.(type) {\n")
	b.WriteString("\t\tcase tea.KeyPressMsg:\n")
	b.WriteString("\t\t\tif msg.Code == tea.KeyEsc {\n")
	fmt.Fprintf(b, "\t\t\t\tm.%s.Close()\n", floatsField)
	b.WriteString("\t\t\t\treturn m, nil\n")
	b.WriteString("\t\t\t}\n")
	b.WriteString("\t\t\tif msg.Code == tea.KeyTab {\n")
	fmt.Fprintf(b, "\t\t\t\tm.%s.Close()\n", floatsField)
	b.WriteString("\t\t\t}\n")
	b.WriteString("\t\tcase tea.MouseClickMsg:\n")
	fmt.Fprintf(b, "\t\t\tif !m.%s.At(msg.X, msg.Y) {\n", floatsField)
	fmt.Fprintf(b, "\t\t\t\tm.%s.Close()\n", floatsField)
	b.WriteString("\t\t\t\treturn m, nil\n")
	b.WriteString("\t\t\t}\n")
	b.WriteString("\t\t}\n")
	b.WriteString("\t}\n")
}

// focusedExpr is the Go of a stop's __focused: focus is on it, and, where a
// stop may not be shown, it is.
func (vc *irViewContext) focusedExpr(fp ir.Expr) string {
	e := vc.gc.EvalExpr(fp)
	if vc.shows {
		e = "(" + e + ") && m.__focusShown()"
	}
	return e
}
