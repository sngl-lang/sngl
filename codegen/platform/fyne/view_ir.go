package fyne

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
	"git.duckfam.us/jonathan/sngl/ir"
)

// irWidgetField tracks a persistent widget stored on Model.
type irWidgetField struct {
	name   string
	goType string
}

// irWidgetUpdater tracks a function that updates one widget property.
type irWidgetUpdater struct {
	name string
	body string
	deps map[string]bool
}

func (u irWidgetUpdater) DepFields() map[string]bool { return u.deps }

// irViewContext tracks state during IR-based BuildUI() code generation.
type irViewContext struct {
	gc             *golang.GoIRContext
	ctx            *codegen.CodegenCtx
	buf            *strings.Builder
	indent         int
	info           *irAnalysis // for dep tracking (nil in component renders)
	entryIndex     int
	widgetFields   []irWidgetField
	updaters       []irWidgetUpdater
	labelCount     int
	containerCount int
	slotVar        string
	slotChildren   []ir.Stmt
	propVals       map[string]ir.Expr

	// localMode disables Model-field registration: render* helpers emit a
	// local var declaration and return the bare name, so the same renderStmt
	// machinery can be used inside for-loop bodies (init + updater) without
	// allocating Model fields that would only retain the last iteration's
	// widget. Updater registration is also suppressed: the parent updater
	// re-renders the whole loop body each refresh.
	localMode bool
}

func (vc *irViewContext) line(format string, args ...any) {
	fmt.Fprintf(vc.buf, "%s"+format+"\n", append([]any{strings.Repeat("\t", vc.indent)}, args...)...)
}

func (vc *irViewContext) addField(name, goType string) {
	if vc.localMode {
		// Whole loop body re-renders into local vars per iteration; nothing
		// to persist on the Model.
		return
	}
	vc.widgetFields = append(vc.widgetFields, irWidgetField{name, goType})
}

// allocWidget reserves a widget slot and returns (id, ref, declaredType).
//   - normal mode: registers a Model field; ref is "m.<prefix><id>".
//   - local mode: emits "var <prefix><id> <goType>" so the caller can write
//     "<ref> = NewXxx(...)" with the same syntax used for Model fields.
func (vc *irViewContext) allocWidget(prefix, goType string) (int, string) {
	id := vc.labelCount
	vc.labelCount++
	name := fmt.Sprintf("%s%d", prefix, id)
	if vc.localMode {
		vc.line("var %s %s", name, goType)
		return id, name
	}
	vc.addField(name, goType)
	return id, "m." + name
}

// counterSnapshot captures the rolling per-kind counters so a block of code
// can be re-rendered in a separate scope (init body vs updater body of a
// for-loop) and produce identical local variable names.
type counterSnapshot struct {
	label, container, entry int
}

func (vc *irViewContext) snapshotCounters() counterSnapshot {
	return counterSnapshot{vc.labelCount, vc.containerCount, vc.entryIndex}
}

func (vc *irViewContext) restoreCounters(s counterSnapshot) {
	vc.labelCount, vc.containerCount, vc.entryIndex = s.label, s.container, s.entry
}

// withLocalMode runs fn inside a localMode scope, ensuring the flag is
// restored even if a render path returns early.
func (vc *irViewContext) withLocalMode(fn func()) {
	prev := vc.localMode
	vc.localMode = true
	fn()
	vc.localMode = prev
}

func (vc *irViewContext) addUpdater(name, body string, deps map[string]bool) {
	if len(deps) == 0 {
		return
	}
	vc.updaters = append(vc.updaters, irWidgetUpdater{name: name, body: body, deps: deps})
}

func (vc *irViewContext) exprDeps(expr ir.Expr) map[string]bool {
	if vc.info == nil {
		return nil
	}
	return vc.info.depTracker().ExprDeps(expr)
}

// --- Statement rendering ---

func (vc *irViewContext) renderStmt(stmt ir.Stmt, resultVar string) {
	switch s := stmt.(type) {
	case *ir.NodeInst:
		vc.renderNode(s, resultVar)
	case *ir.If:
		vc.renderIf(s, resultVar)
	case *ir.For:
		vc.renderFor(s, resultVar)
	case *ir.PlatformFilter:
		for _, bs := range s.Body {
			vc.renderStmt(bs, resultVar)
		}
	case *ir.SlotInst:
		if len(vc.slotChildren) == 1 {
			vc.renderStmt(vc.slotChildren[0], resultVar)
		} else if len(vc.slotChildren) > 1 {
			partsVar := resultVar + "SlotParts"
			vc.line("var %s []fyne.CanvasObject", partsVar)
			for i, child := range vc.slotChildren {
				childVar := fmt.Sprintf("%sSlot%d", resultVar, i)
				vc.line("var %s fyne.CanvasObject", childVar)
				vc.renderStmt(child, childVar)
				vc.line("if %s != nil { %s = append(%s, %s) }", childVar, partsVar, partsVar, childVar)
			}
			vc.line("%s = container.NewVBox(%s...)", resultVar, partsVar)
		} else if vc.slotVar != "" {
			vc.line(`%s = %s`, resultVar, vc.slotVar)
		}
	case *ir.ErrorBoundary:
		for _, child := range s.Children {
			vc.renderStmt(child, resultVar)
		}
	}
}

func (vc *irViewContext) renderIf(s *ir.If, resultVar string) {
	if vc.info != nil {
		vc.renderConditional(s, resultVar)
		return
	}
	cond := vc.gc.EvalExpr(s.Cond)
	vc.line("if %s {", cond)
	vc.indent++
	for _, child := range s.Body {
		vc.renderStmt(child, resultVar)
	}
	vc.indent--
	if len(s.Else) > 0 {
		vc.line("} else {")
		vc.indent++
		for _, child := range s.Else {
			vc.renderStmt(child, resultVar)
		}
		vc.indent--
	}
	vc.line("}")
}

func (vc *irViewContext) renderConditional(s *ir.If, resultVar string) {
	id := vc.containerCount
	vc.containerCount++
	bodyField := fmt.Sprintf("ifBox%d", id)
	if vc.localMode {
		// Conditionals nested inside a for-loop body collapse to a runtime
		// re-render, not a Show/Hide updater — the parent loop updater
		// rebuilds everything. Emit a plain Go if/else with locals.
		innerVar := resultVar + "Inner"
		vc.line("var %s fyne.CanvasObject", innerVar)
		vc.line("if %s {", vc.gc.EvalExpr(s.Cond))
		vc.indent++
		for _, child := range s.Body {
			vc.renderStmt(child, innerVar)
		}
		vc.indent--
		if len(s.Else) > 0 {
			vc.line("} else {")
			vc.indent++
			for _, child := range s.Else {
				vc.renderStmt(child, innerVar)
			}
			vc.indent--
		}
		vc.line("}")
		vc.line("%s = %s", resultVar, innerVar)
		return
	}
	vc.addField(bodyField, "*fyne.Container")

	innerVar := resultVar + "Inner"
	vc.line("var %s fyne.CanvasObject", innerVar)
	for _, child := range s.Body {
		vc.renderStmt(child, innerVar)
	}
	vc.line("if %s == nil { %s = widget.NewLabel(\"\") }", innerVar, innerVar)
	vc.line("m.%s = container.NewStack(%s)", bodyField, innerVar)

	var elseField string
	if len(s.Else) > 0 {
		elseField = fmt.Sprintf("elseBox%d", id)
		vc.addField(elseField, "*fyne.Container")
		elseInnerVar := resultVar + "ElseInner"
		vc.line("var %s fyne.CanvasObject", elseInnerVar)
		for _, child := range s.Else {
			vc.renderStmt(child, elseInnerVar)
		}
		vc.line("if %s == nil { %s = widget.NewLabel(\"\") }", elseInnerVar, elseInnerVar)
		vc.line("m.%s = container.NewStack(%s)", elseField, elseInnerVar)
	}

	cond := vc.gc.EvalExpr(s.Cond)
	vc.line("if !(%s) { m.%s.Hide() }", cond, bodyField)
	if elseField != "" {
		vc.line("if %s { m.%s.Hide() }", cond, elseField)
	}
	if elseField != "" {
		// Group the two sub-stacks into a single result so the parent layout
		// sees one slot. Ordering matches the source: body first, else second.
		vc.line("%s = container.NewStack(m.%s, m.%s)", resultVar, bodyField, elseField)
	} else {
		vc.line("%s = m.%s", resultVar, bodyField)
	}

	deps := vc.exprDeps(s.Cond)
	if len(deps) > 0 {
		updaterName := fmt.Sprintf("updateIf%d", id)
		var body string
		if elseField != "" {
			body = fmt.Sprintf("if %s { m.%s.Show(); m.%s.Hide() } else { m.%s.Hide(); m.%s.Show() }",
				cond, bodyField, elseField, bodyField, elseField)
		} else {
			body = fmt.Sprintf("if %s { m.%s.Show() } else { m.%s.Hide() }", cond, bodyField, bodyField)
		}
		vc.addUpdater(updaterName, body, deps)
	}
}

func (vc *irViewContext) renderFor(s *ir.For, resultVar string) {
	iterVar := s.Key
	iterExpr := vc.gc.EvalExpr(s.Iter)
	indexVar := "_"
	if s.Value != "" {
		indexVar = s.Key
		iterVar = s.Value
	}

	loopGC := vc.gc.WithLocal(iterVar)
	if indexVar != "_" {
		loopGC = loopGC.WithLocal(indexVar)
	}
	savedGC := vc.gc
	vc.gc = loopGC

	if vc.info != nil {
		id := vc.containerCount
		vc.containerCount++
		fieldName := fmt.Sprintf("forBox%d", id)
		vc.addField(fieldName, "*fyne.Container")

		// Render the loop body once into a reusable Go-source snippet that
		// emits one CanvasObject per iteration into a target slice. Used for
		// both the init path and the updater body so a state change re-runs
		// the same widget construction logic instead of a placeholder.
		emitBody := func(target string) string {
			var subBuf strings.Builder
			savedBuf := vc.buf
			savedIndent := vc.indent
			savedCounters := vc.snapshotCounters()

			vc.buf = &subBuf
			vc.indent = 0
			vc.withLocalMode(func() {
				vc.line("for %s, %s := range %s {", indexVar, iterVar, iterExpr)
				vc.indent++
				if indexVar != "_" {
					vc.line("_ = %s", indexVar)
				}
				vc.line("_ = %s", iterVar)
				innerVar := "item"
				vc.line("var %s fyne.CanvasObject", innerVar)
				for _, child := range s.Body {
					vc.renderStmt(child, innerVar)
				}
				vc.line("if %s != nil { %s = append(%s, %s) }", innerVar, target, target, innerVar)
				vc.indent--
				vc.line("}")
			})

			vc.buf = savedBuf
			vc.indent = savedIndent
			// Both renders must produce identical local var names. Restoring
			// counters here means the second emission (updater) sees the same
			// starting state the first one (init) did.
			vc.restoreCounters(savedCounters)
			return subBuf.String()
		}

		loopItems := resultVar + "Items"
		vc.line("var %s []fyne.CanvasObject", loopItems)
		// Init: emit the loop directly into the current buffer at the current
		// indent so it slots into BuildUI naturally.
		initBody := emitBody(loopItems)
		for ln := range strings.SplitSeq(strings.TrimRight(initBody, "\n"), "\n") {
			vc.line("%s", ln)
		}
		vc.line("m.%s = container.NewVBox(%s...)", fieldName, loopItems)
		vc.line("%s = m.%s", resultVar, fieldName)

		deps := vc.exprDeps(s.Iter)
		if len(deps) > 0 {
			updaterName := fmt.Sprintf("updateFor%d", id)
			updateBody := emitBody("items")
			var bodyBuf strings.Builder
			fmt.Fprintf(&bodyBuf, "var items []fyne.CanvasObject\n")
			// Reindent the body one tab for inclusion inside the updater's
			// function block.
			for ln := range strings.SplitSeq(strings.TrimRight(updateBody, "\n"), "\n") {
				bodyBuf.WriteByte('\t')
				bodyBuf.WriteString(ln)
				bodyBuf.WriteByte('\n')
			}
			fmt.Fprintf(&bodyBuf, "\tm.%s.Objects = items\n", fieldName)
			fmt.Fprintf(&bodyBuf, "\tm.%s.Refresh()", fieldName)
			vc.addUpdater(updaterName, bodyBuf.String(), deps)
		}
		vc.gc = savedGC
		return
	}

	loopItems := resultVar + "Items"
	vc.line("var %s []fyne.CanvasObject", loopItems)
	vc.line("for %s, %s := range %s {", indexVar, iterVar, iterExpr)
	vc.indent++
	if indexVar != "_" {
		vc.line("_ = %s", indexVar)
	}
	innerVar := resultVar + "Item"
	vc.line("var %s fyne.CanvasObject", innerVar)
	for _, child := range s.Body {
		vc.renderStmt(child, innerVar)
	}
	vc.line("if %s != nil { %s = append(%s, %s) }", innerVar, loopItems, loopItems, innerVar)
	vc.indent--
	vc.line("}")
	vc.line("%s = container.NewVBox(%s...)", resultVar, loopItems)
	vc.gc = savedGC
}

// --- Node rendering ---

func (vc *irViewContext) renderNode(n *ir.NodeInst, resultVar string) {
	if n.Component != nil && vc.isUserComponent(n.Component) {
		vc.renderUserComponent(n, resultVar)
		return
	}
	if n.Component != nil {
		vc.renderStdlibComponent(n, resultVar)
		return
	}
	vc.renderRawWidget(n, resultVar)
}

func (vc *irViewContext) isUserComponent(comp *ir.Component) bool {
	return slices.Contains(vc.ctx.Pkg.Components, comp)
}

func (vc *irViewContext) renderStdlibComponent(n *ir.NodeInst, resultVar string) {
	switch n.Name {
	case "vbox", "stack", "scroll", "card", "radio",
		"drawer", "tooltip", "popover", "table", "tree", "menu":
		vc.renderContainerVBox(n, resultVar)
	case "hbox", "tabs", "splitview", "menubar", "toolbar":
		vc.renderContainerHBox(n, resultVar)
	case "text", "badge", "divider", "avatar", "progress", "spinner":
		vc.renderLabel(n, resultVar)
	case "link":
		vc.renderLink(n, resultVar)
	case "button":
		vc.renderButton(n, resultVar)
	case "input", "textarea":
		vc.renderEntry(n, resultVar)
	case "checkbox":
		vc.renderCheck(n, resultVar)
	case "toggle":
		vc.renderCheck(n, resultVar)
	case "select":
		vc.renderSelect(n, resultVar)
	case "image":
		vc.renderImage(n, resultVar)
	case "spacer":
		vc.line("%s = layout.NewSpacer()", resultVar)
	case "modal":
		vc.renderModal(n, resultVar)
	default:
		if len(n.Children) > 0 {
			vc.renderContainerVBox(n, resultVar)
		} else {
			vc.line("%s = widget.NewLabel(\"\")", resultVar)
		}
	}
}

func (vc *irViewContext) renderContainerVBox(n *ir.NodeInst, resultVar string) {
	childrenVar := resultVar + "Children"
	vc.line("var %s []fyne.CanvasObject", childrenVar)
	for i, child := range n.Children {
		childVar := fmt.Sprintf("%sC%d", resultVar, i)
		vc.line("var %s fyne.CanvasObject", childVar)
		vc.renderStmt(child, childVar)
		vc.line("if %s != nil { %s = append(%s, %s) }", childVar, childrenVar, childrenVar, childVar)
	}
	if n.Name == "scroll" {
		vc.line("%s = container.NewVScroll(container.NewVBox(%s...))", resultVar, childrenVar)
	} else {
		vc.line("%s = container.NewVBox(%s...)", resultVar, childrenVar)
	}
}

func (vc *irViewContext) renderContainerHBox(n *ir.NodeInst, resultVar string) {
	childrenVar := resultVar + "Children"
	vc.line("var %s []fyne.CanvasObject", childrenVar)
	for i, child := range n.Children {
		childVar := fmt.Sprintf("%sC%d", resultVar, i)
		vc.line("var %s fyne.CanvasObject", childVar)
		vc.renderStmt(child, childVar)
		vc.line("if %s != nil { %s = append(%s, %s) }", childVar, childrenVar, childrenVar, childVar)
	}
	vc.line("%s = container.NewHBox(%s...)", resultVar, childrenVar)
}

func (vc *irViewContext) renderLabel(n *ir.NodeInst, resultVar string) {
	content := vc.resolveContentExpr(n)
	id, ref := vc.allocWidget("label", "*widget.Label")

	vc.line("%s = widget.NewLabel(fmt.Sprint(%s))", ref, content)
	vc.line("%s = %s", resultVar, ref)

	if vc.localMode {
		// Updater would target a Model field that doesn't exist for locals.
		// The enclosing for-loop's updater rebuilds the whole body anyway.
		return
	}

	contentExpr := vc.resolveContentIRExpr(n)
	if contentExpr != nil {
		deps := vc.exprDeps(contentExpr)
		if len(deps) > 0 {
			updaterName := fmt.Sprintf("updateLabel%d", id)
			body := fmt.Sprintf("%s.SetText(fmt.Sprint(%s))", ref, content)
			vc.addUpdater(updaterName, body, deps)
		}
	}
}

func (vc *irViewContext) renderLink(n *ir.NodeInst, resultVar string) {
	text := `""`
	if v := codegen.NodeProp(n, "text"); v != nil {
		text = vc.gc.EvalExpr(v)
	} else if v := codegen.NodeProp(n, "value"); v != nil {
		text = vc.gc.EvalExpr(v)
	}
	href := `""`
	if v := codegen.NodeProp(n, "href"); v != nil {
		href = vc.gc.EvalExpr(v)
	}
	id, ref := vc.allocWidget("link", "*widget.Hyperlink")
	vc.line("{ linkURL%d, _ := url.Parse(%s); %s = widget.NewHyperlink(%s, linkURL%d) }", id, href, ref, text, id)
	vc.line("%s = %s", resultVar, ref)
}

func (vc *irViewContext) renderButton(n *ir.NodeInst, resultVar string) {
	text := `""`
	if v := codegen.NodeProp(n, "text"); v != nil {
		text = vc.gc.EvalExpr(v)
	} else if v := codegen.NodeProp(n, "label"); v != nil {
		text = vc.gc.EvalExpr(v)
	}

	_, ref := vc.allocWidget("btn", "*widget.Button")

	clickHandler := codegen.NodeHandler(n, "click")
	if clickHandler != nil && clickHandler.Func != nil {
		vc.line("%s = widget.NewButton(fmt.Sprint(%s), func() {", ref, text)
		vc.indent++
		vc.emitEventHandlerBlock(clickHandler.Func.Block)
		vc.indent--
		vc.line("})")
	} else {
		vc.line("%s = widget.NewButton(fmt.Sprint(%s), nil)", ref, text)
	}
	vc.line("%s = %s", resultVar, ref)
}

func (vc *irViewContext) renderEntry(n *ir.NodeInst, resultVar string) {
	idx := vc.entryIndex
	vc.entryIndex++
	vc.line("%s = m.entry%d", resultVar, idx)
}

func (vc *irViewContext) renderCheck(n *ir.NodeInst, resultVar string) {
	label := `""`
	if v := codegen.NodeProp(n, "label"); v != nil {
		label = vc.gc.EvalExpr(v)
	}

	_, ref := vc.allocWidget("check", "*widget.Check")

	changeHandler := codegen.NodeHandler(n, "change")
	if changeHandler != nil && changeHandler.Func != nil {
		vc.line("%s = widget.NewCheck(fmt.Sprint(%s), func(b bool) {", ref, label)
		vc.indent++
		vc.emitEventHandlerBlock(changeHandler.Func.Block)
		vc.indent--
		vc.line("})")
	} else {
		vc.line("%s = widget.NewCheck(fmt.Sprint(%s), nil)", ref, label)
	}
	vc.line("%s = %s", resultVar, ref)
}

func (vc *irViewContext) renderSelect(n *ir.NodeInst, resultVar string) {
	_, ref := vc.allocWidget("sel", "*widget.Select")

	changeHandler := codegen.NodeHandler(n, "change")
	if changeHandler != nil && changeHandler.Func != nil {
		vc.line("%s = widget.NewSelect(nil, func(s string) {", ref)
		vc.indent++
		vc.emitEventHandlerBlock(changeHandler.Func.Block)
		vc.indent--
		vc.line("})")
	} else {
		vc.line("%s = widget.NewSelect(nil, nil)", ref)
	}
	vc.line("%s = %s", resultVar, ref)
}

func (vc *irViewContext) renderImage(n *ir.NodeInst, resultVar string) {
	src := codegen.NodeProp(n, "src")
	if src != nil {
		vc.line("%s = canvas.NewImageFromURI(nil) // TODO: resolve %s", resultVar, vc.gc.EvalExpr(src))
	} else {
		vc.line("%s = widget.NewLabel(\"[image]\")", resultVar)
	}
}

func (vc *irViewContext) renderModal(n *ir.NodeInst, resultVar string) {
	openExpr := codegen.NodeProp(n, "open")
	if openExpr != nil {
		cond := vc.gc.EvalExpr(openExpr)
		vc.line("if %s {", cond)
		vc.indent++
		vc.renderContainerVBox(n, resultVar)
		vc.indent--
		vc.line("}")
	}
}

func (vc *irViewContext) renderUserComponent(n *ir.NodeInst, resultVar string) {
	var args []string
	if n.Component != nil {
		for _, p := range n.Component.Props {
			propVal := codegen.NodeProp(n, p.Name)
			if propVal != nil {
				args = append(args, vc.gc.EvalExpr(propVal))
			} else if p.Default != nil {
				args = append(args, golang.IRLiteralToGo(p.Default))
			} else {
				args = append(args, "nil")
			}
		}
	}
	if n.Component != nil && n.Component.ChildrenType != nil && len(n.Children) > 0 {
		slotVar := resultVar + "Slot"
		vc.renderChildrenToObject(n.Children, slotVar)
		args = append(args, slotVar)
	}
	vc.line("%s = m.render%s(%s)", resultVar, golang.ExportName(n.Name), strings.Join(args, ", "))
}

func (vc *irViewContext) renderChildrenToObject(children []ir.Stmt, resultVar string) {
	if len(children) == 1 {
		if n, ok := children[0].(*ir.NodeInst); ok {
			vc.line("var %s fyne.CanvasObject", resultVar)
			vc.renderNode(n, resultVar)
			return
		}
	}
	partsVar := resultVar + "Parts"
	vc.line("var %s []fyne.CanvasObject", partsVar)
	for i, child := range children {
		childVar := fmt.Sprintf("%sPart%d", resultVar, i)
		vc.line("var %s fyne.CanvasObject", childVar)
		vc.renderStmt(child, childVar)
		vc.line("if %s != nil { %s = append(%s, %s) }", childVar, partsVar, partsVar, childVar)
	}
	vc.line("%s := container.NewVBox(%s...)", resultVar, partsVar)
}

func (vc *irViewContext) renderRawWidget(n *ir.NodeInst, resultVar string) {
	content := `""`
	if v := codegen.NodeProp(n, "content"); v != nil {
		content = vc.gc.EvalExpr(v)
	}
	vc.line("%s = widget.NewLabel(fmt.Sprint(%s))", resultVar, content)
}

// --- Event handler emission ---

func (vc *irViewContext) emitEventHandlerBlock(stmts []ir.Stmt) {
	mutated := make(map[string]bool)
	for _, stmt := range stmts {
		for _, line := range vc.gc.EvalStmt(stmt) {
			vc.line("%s", line)
		}
		maps.Copy(mutated, codegen.MutatedFields(stmt))
	}
	if vc.info != nil {
		affected := codegen.FindAffected(vc.info.depTracker(), vc.updaters, mutated)
		if len(affected) > 0 {
			for _, u := range affected {
				vc.line("m.%s()", u.name)
			}
		} else {
			vc.line("m.doRefresh()")
		}
	} else {
		vc.line("m.doRefresh()")
	}
}

// --- Content resolution helpers ---

func (vc *irViewContext) resolveContentExpr(n *ir.NodeInst) string {
	if v := codegen.NodeProp(n, "value"); v != nil {
		return vc.gc.EvalExpr(v)
	}
	if v := codegen.NodeProp(n, "text"); v != nil {
		return vc.gc.EvalExpr(v)
	}
	if v := codegen.NodeProp(n, "label"); v != nil {
		return vc.gc.EvalExpr(v)
	}
	if v := codegen.NodeProp(n, "content"); v != nil {
		return vc.gc.EvalExpr(v)
	}
	return `""`
}

func (vc *irViewContext) resolveContentIRExpr(n *ir.NodeInst) ir.Expr {
	for _, name := range []string{"value", "text", "label", "content"} {
		if v := codegen.NodeProp(n, name); v != nil {
			return v
		}
	}
	return nil
}
