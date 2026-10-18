package bubbletea

import (
	"fmt"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/codegen/lang/golang"
	"duckfam.us/sngl/internal/lower"
	"duckfam.us/sngl/ir"
)

// A bubbles model a loop renders -- a text input per row -- is one model per
// copy. The Model holds them in a map keyed as a cell of the copy is
// (lower.CopyKey) and builds each the first time View renders it; the copy's
// value lives in the per-copy cell its `:value` writes back to, which the copy
// is set from whenever the two differ. Update hands a message to the copy the
// loop slot's cursor names, found by walking the loop as View counts it, and
// runs that copy's write-back and handlers there, the loop's variables bound.
//
// It used to be one model field for every copy: every row showed and edited
// the same text, the write-back wrote whichever var the first handler
// assigned, and a handler reading the loop variable did not compile.

// loopWidget is what a widgetInfo under a loop carries.
type loopWidget struct {
	// loops are the loops around the node, outermost first: the copy key's
	// indices, and the first is the slot the walks run over.
	loops []*ir.For
	lf    loopFocus
	// cell reads the copy's current value, off the write-back handler's read
	// of its per-copy cell; nil for a widget nothing binds.
	cell ir.Expr
	// bind is the write-back handler, named after the bound prop.
	bind *ir.EventHandler
}

// widgetLoops is the loops around each node of stmts, outermost first.
func widgetLoops(stmts []ir.Stmt) map[*ir.NodeInst][]*ir.For {
	out := map[*ir.NodeInst][]*ir.For{}
	var walk func(stmts []ir.Stmt, loops []*ir.For)
	walk = func(stmts []ir.Stmt, loops []*ir.For) {
		for _, st := range stmts {
			switch n := st.(type) {
			case *ir.NodeInst:
				if len(loops) > 0 {
					out[n] = loops
				}
				for _, s := range ir.SuppliedContent(n) {
					walk(s.Body, loops)
				}
			case *ir.For:
				inner := append(append([]*ir.For{}, loops...), n)
				walk(n.Body, inner)
				walk(n.Else, loops)
			case *ir.If, *ir.Match, *ir.ErrorBoundary:
				for _, b := range ir.TransparentBlocks(n) {
					walk(*b, loops)
				}
			}
		}
	}
	walk(stmts, nil)
	return out
}

// newLoopWidget is what a widget under loops carries, or nil for one a loop
// slot's focus does not reach, which there is no copy under the cursor of.
func newLoopWidget(n *ir.NodeInst, loops []*ir.For, bindProp string) *loopWidget {
	lf, ok := nodeLoopFocus(n)
	if !ok {
		return nil
	}
	lw := &loopWidget{loops: loops, lf: lf}
	if h := codegen.NodeHandler(n, bindProp); h != nil && h.Func != nil {
		lw.bind = h
		_ = ir.Walk(h.Func.Block, func(x ir.Node) error {
			if lv, ok := x.(*ir.LocalVar); ok && lw.cell == nil && lv.Init != nil {
				lw.cell = lv.Init
				return ir.SkipAll
			}
			return nil
		})
	}
	return lw
}

// readOf reads a widget's value out of x, as bindReadBack does out of a field.
func readOf(x, get string) string {
	if strings.HasPrefix(get, "tui.") {
		return get + "(" + x + ")"
	}
	return x + get
}

func loopCopyMethod(w widgetInfo) string  { return w.fieldName + "Copy" }
func loopCountMethod(w widgetInfo) string { return w.fieldName + "Count" }
func loopFocusMethod(w widgetInfo) string { return w.fieldName + "FocusSync" }

// copyCall is the copy of w the loop variables in gc name.
func copyCall(w widgetInfo, gc *golang.GoIRContext) string {
	key := gc.EvalExpr(lower.CopyKey(w.loop.loops))
	if w.loop.cell == nil {
		return fmt.Sprintf("m.%s(%s)", loopCopyMethod(w), key)
	}
	return fmt.Sprintf("m.%s(%s, %s)", loopCopyMethod(w), key, gc.EvalExpr(w.loop.cell))
}

// emitLoopWidgetMethods writes the copy accessor, the copy count, and the
// focus sync of a widget under a loop.
func emitLoopWidgetMethods(b *strings.Builder, w widgetInfo, gc *golang.GoIRContext) {
	val, _, _ := widgetPayloadEvents(w)
	params := "key string"
	if w.loop.cell != nil && val.goType != "" {
		params += ", value " + val.goType
	}
	fmt.Fprintf(b, "// %s is the copy of %s the key names, built the first time it renders\n// and set from its cell whenever the two differ.\n", loopCopyMethod(w), w.fieldName)
	fmt.Fprintf(b, "func (m *Model) %s(%s) %s {\n", loopCopyMethod(w), params, w.model.Type)
	fmt.Fprintf(b, "\tw, ok := m.%s[key]\n", w.fieldName)
	b.WriteString("\tif !ok {\n")
	fmt.Fprintf(b, "\t\tw = %s\n", w.model.New)
	if w.placeholder != "" {
		fmt.Fprintf(b, "\t\tw.Placeholder = %q\n", w.placeholder)
	}
	if w.model.Resize != "" {
		fmt.Fprintf(b, "\t\tw%s\n", w.model.Resize)
	}
	b.WriteString("\t}\n")
	if w.loop.cell != nil && val.goType != "" {
		fmt.Fprintf(b, "\tif %s != value {\n\t\tw%s(value)\n\t}\n", readOf("w", val.get), val.set)
	}
	fmt.Fprintf(b, "\tm.%s[key] = w\n\treturn w\n}\n\n", w.fieldName)

	fmt.Fprintf(b, "// %s is how many copies of %s the loop renders.\n", loopCountMethod(w), w.fieldName)
	fmt.Fprintf(b, "func (m *Model) %s() int {\n\t__n := 0\n", loopCountMethod(w))
	walkCopies(b, w, gc, 1, func(vw *viewWalk, _ *golang.GoIRContext) { vw.line("__n++") })
	b.WriteString("\treturn __n\n}\n\n")

	if modelHasFocusMethods(w.model.Type) {
		fmt.Fprintf(b, "// %s focuses the copy of %s the cursor is on and blurs the rest.\n", loopFocusMethod(w), w.fieldName)
		fmt.Fprintf(b, "func (m *Model) %s() {\n\tfocused := \"\"\n", loopFocusMethod(w))
		fmt.Fprintf(b, "\tif m.__focusID == %d {\n\t\tfunc() {\n", w.loop.lf.slot)
		walkSlot(b, w, gc, 3, func(vw *viewWalk, gc *golang.GoIRContext) {
			vw.line("if m.%s == %s {", w.loop.lf.cursor, w.loop.lf.pos)
			vw.line("\tfocused = %s", gc.EvalExpr(lower.CopyKey(w.loop.loops)))
			vw.line("\treturn")
			vw.line("}")
		})
		b.WriteString("\t\t}()\n\t}\n")
		fmt.Fprintf(b, "\tfor key, w := range m.%s {\n\t\tif key == focused && m.__focusID == %d {\n\t\t\tw.Focus()\n\t\t} else {\n\t\t\tw.Blur()\n\t\t}\n\t\tm.%s[key] = w\n\t}\n}\n\n",
			w.fieldName, w.loop.lf.slot, w.fieldName)
	}
}

// walkSlot writes a walk of w's loop slot as View renders it, counting its
// focusable nodes in lf.pos and calling at on w's node with the loop
// variables bound and lf.pos its ordinal.
func walkSlot(b *strings.Builder, w widgetInfo, gc *golang.GoIRContext, indent int, at func(vw *viewWalk, gc *golang.GoIRContext)) {
	isLoopFocus := func(n *ir.NodeInst) bool {
		lf, ok := nodeLoopFocus(n)
		return ok && lf.slot == w.loop.lf.slot
	}
	vw := &viewWalk{b: b, indent: indent, wants: isLoopFocus}
	vw.visit = func(vw *viewWalk, n *ir.NodeInst, gc *golang.GoIRContext) {
		if n == w.node {
			at(vw, gc)
		}
		vw.line("%s++", w.loop.lf.pos)
	}
	vw.line("%s := 0", w.loop.lf.pos)
	vw.line("_ = %s", w.loop.lf.pos)
	vw.stmts([]ir.Stmt{w.loop.loops[0]}, gc)
}

// walkCopies writes a walk of w's loop calling at on each copy of w's node,
// in render order, the loop variables bound.
func walkCopies(b *strings.Builder, w widgetInfo, gc *golang.GoIRContext, indent int, at func(vw *viewWalk, gc *golang.GoIRContext)) {
	vw := &viewWalk{b: b, indent: indent, wants: func(n *ir.NodeInst) bool { return n == w.node }}
	vw.visit = func(vw *viewWalk, _ *ir.NodeInst, gc *golang.GoIRContext) { at(vw, gc) }
	vw.stmts([]ir.Stmt{w.loop.loops[0]}, gc)
}

// emitCopyHandler writes the handler h of a copy, its parameter bound to the
// Go expression value.
func emitCopyHandler(vw *viewWalk, h *ir.EventHandler, value string, info *irAnalysis, gc *golang.GoIRContext) {
	if h == nil || h.Func == nil {
		return
	}
	hgc := gc
	if len(h.Func.Params) > 0 {
		name := h.Func.Params[0].Name
		vw.line("%s := %s", name, value)
		vw.line("_ = %s", name)
		hgc = gc.WithLocal(name)
	}
	for _, stmt := range h.Func.Block {
		for _, l := range hgc.EvalStmt(stmt) {
			vw.line("%s", l)
		}
	}
	var sync strings.Builder
	syncMutatedInputs(&sync, h.Func.Block, info.widgets, info.binds, hgc)
	for l := range strings.SplitSeq(strings.TrimRight(sync.String(), "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			vw.line("%s", l)
		}
	}
}

// emitLoopWidgetForward writes Update's forwarding of a message to the copy
// of w the loop slot's cursor names: the write-back of a value the message
// changed, then @input, and @change on Enter.
func emitLoopWidgetForward(b *strings.Builder, w widgetInfo, info *irAnalysis, gc *golang.GoIRContext, indent string) {
	val, hasInput, hasChange := widgetPayloadEvents(w)
	depth := strings.Count(indent, "\t")
	fmt.Fprintf(b, "%sif m.__focusID == %d {\n%s\tfunc() {\n", indent, w.loop.lf.slot, indent)
	walkSlot(b, w, gc, depth+2, func(vw *viewWalk, gc *golang.GoIRContext) {
		vw.line("if m.%s == %s {", w.loop.lf.cursor, w.loop.lf.pos)
		vw.indent++
		key := gc.EvalExpr(lower.CopyKey(w.loop.loops))
		vw.line("__key := %s", key)
		vw.line("w := %s", copyCall(w, gc))
		if modelHasFocusMethods(w.model.Type) {
			vw.line("w.Focus()")
		}
		if val.get != "" {
			vw.line("__prev := %s", readOf("w", val.get))
		}
		vw.line("w, cmd = w%s", w.model.Update)
		vw.line("cmds = append(cmds, cmd)")
		vw.line("m.%s[__key] = w", w.fieldName)
		if val.get != "" {
			vw.line("if __v := %s; __v != __prev {", readOf("w", val.get))
			vw.indent++
			emitCopyHandler(vw, w.loop.bind, "__v", info, gc)
			if hasInput {
				emitCopyHandler(vw, codegen.NodeHandler(w.node, "input"), "__v", info, gc)
			}
			vw.indent--
			vw.line("}")
			if hasChange {
				vw.line("if __k, ok := msg.(tea.KeyPressMsg); ok && __k.Code == tea.KeyEnter {")
				vw.indent++
				emitCopyHandler(vw, codegen.NodeHandler(w.node, "change"), readOf("w", val.get), info, gc)
				vw.indent--
				vw.line("}")
			}
		}
		vw.line("return")
		vw.indent--
		vw.line("}")
	})
	fmt.Fprintf(b, "%s\t}()\n%s}\n", indent, indent)
}

// emitLoopWidgetInvokers writes a test's invokers for a widget under a loop
// with an `#id`: each puts a value in a copy and runs its write-back and
// handler, as Update does when the user types. The plain one drives the last
// copy, and the `At` one, for an id a test indexes, the copy with a key.
func emitLoopWidgetInvokers(b *strings.Builder, w widgetInfo, info *irAnalysis, gc *golang.GoIRContext) {
	if w.node.ID == "" || strings.HasPrefix(w.node.ID, "__n") {
		return
	}
	val, _, _ := widgetPayloadEvents(w)
	if val.get == "" {
		return
	}
	copyKey := w.node.Prop(ir.CopyProp)
	singleLine := w.model.Type == "textinput.Model" || w.model.Type == "tui.DateInput"
	for _, ev := range []struct {
		name string
		want bool
	}{{"input", val.goType == "string"}, {"change", singleLine}} {
		if !ev.want {
			continue
		}
		drive := func(method, lead, pre, match string, counted bool) {
			fmt.Fprintf(b, "func (m *Model) %s(%sv %s) {\n%s\tfunc() {\n", method, lead, val.goType, pre)
			walkCopies(b, w, gc, 2, func(vw *viewWalk, gc *golang.GoIRContext) {
				cond := match
				if !counted {
					cond = gc.EvalExpr(copyKey) + " == key"
				}
				vw.line("if %s {", cond)
				vw.indent++
				vw.line("__key := %s", gc.EvalExpr(lower.CopyKey(w.loop.loops)))
				vw.line("w := %s", copyCall(w, gc))
				vw.line("w%s(v)", val.set)
				vw.line("m.%s[__key] = w", w.fieldName)
				emitCopyHandler(vw, w.loop.bind, "v", info, gc)
				emitCopyHandler(vw, codegen.NodeHandler(w.node, ev.name), "v", info, gc)
				vw.line("return")
				vw.indent--
				vw.line("}")
				if counted {
					vw.line("__n++")
				}
			})
			b.WriteString("\t}()\n}\n\n")
		}
		name := w.node.ID + golang.ExportName(ev.name)
		fmt.Fprintf(b, "// %s types v into the last copy of #%s and runs its @%s; for tests.\n", name, w.node.ID, ev.name)
		drive(name, "", fmt.Sprintf("\tlast, __n := m.%s()-1, 0\n", loopCountMethod(w)), "__n == last", true)
		if copyKey == nil {
			continue
		}
		at := codegen.InvokerAt(name)
		fmt.Fprintf(b, "// %s types v into the copy of #%s with the key and runs its @%s; for\n// tests.\n", at, w.node.ID, ev.name)
		drive(at, "key string, ", "", "", false)
	}
}
