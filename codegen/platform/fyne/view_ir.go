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
}

func (vc *irViewContext) line(format string, args ...any) {
	fmt.Fprintf(vc.buf, "%s"+format+"\n", append([]any{strings.Repeat("\t", vc.indent)}, args...)...)
}

func (vc *irViewContext) addField(name, goType string) {
	vc.widgetFields = append(vc.widgetFields, irWidgetField{name, goType})
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
	fieldName := fmt.Sprintf("ifBox%d", id)
	vc.addField(fieldName, "*fyne.Container")

	innerVar := resultVar + "Inner"
	vc.line("var %s fyne.CanvasObject", innerVar)
	for _, child := range s.Body {
		vc.renderStmt(child, innerVar)
	}
	vc.line("if %s == nil { %s = widget.NewLabel(\"\") }", innerVar, innerVar)
	vc.line("m.%s = container.NewStack(%s)", fieldName, innerVar)

	cond := vc.gc.EvalExpr(s.Cond)
	vc.line("if !(%s) { m.%s.Hide() }", cond, fieldName)
	vc.line("%s = m.%s", resultVar, fieldName)

	deps := vc.exprDeps(s.Cond)
	if len(deps) > 0 {
		updaterName := fmt.Sprintf("updateIf%d", id)
		body := fmt.Sprintf("if %s { m.%s.Show() } else { m.%s.Hide() }", cond, fieldName, fieldName)
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
		vc.line("m.%s = container.NewVBox(%s...)", fieldName, loopItems)
		vc.line("%s = m.%s", resultVar, fieldName)

		deps := vc.exprDeps(s.Iter)
		if len(deps) > 0 {
			updaterName := fmt.Sprintf("updateFor%d", id)
			var bodyBuf strings.Builder
			fmt.Fprintf(&bodyBuf, "var items []fyne.CanvasObject\n")
			fmt.Fprintf(&bodyBuf, "\tfor %s, %s := range %s {\n", indexVar, iterVar, iterExpr)
			if indexVar != "_" {
				fmt.Fprintf(&bodyBuf, "\t\t_ = %s\n", indexVar)
			}
			fmt.Fprintf(&bodyBuf, "\t\t_ = %s\n", iterVar)
			fmt.Fprintf(&bodyBuf, "\t\titems = append(items, widget.NewLabel(fmt.Sprint(%s)))\n", iterVar)
			fmt.Fprintf(&bodyBuf, "\t}\n")
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
	id := vc.labelCount
	vc.labelCount++
	fieldName := fmt.Sprintf("label%d", id)
	vc.addField(fieldName, "*widget.Label")

	vc.line("m.%s = widget.NewLabel(fmt.Sprint(%s))", fieldName, content)
	vc.line("%s = m.%s", resultVar, fieldName)

	// Register updater if content is reactive
	contentExpr := vc.resolveContentIRExpr(n)
	if contentExpr != nil {
		deps := vc.exprDeps(contentExpr)
		if len(deps) > 0 {
			updaterName := fmt.Sprintf("updateLabel%d", id)
			body := fmt.Sprintf("m.%s.SetText(fmt.Sprint(%s))", fieldName, content)
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
	id := vc.labelCount
	vc.labelCount++
	fieldName := fmt.Sprintf("link%d", id)
	vc.addField(fieldName, "*widget.Hyperlink")
	vc.line("{ linkURL%d, _ := url.Parse(%s); m.%s = widget.NewHyperlink(%s, linkURL%d) }", id, href, fieldName, text, id)
	vc.line("%s = m.%s", resultVar, fieldName)
}

func (vc *irViewContext) renderButton(n *ir.NodeInst, resultVar string) {
	text := `""`
	if v := codegen.NodeProp(n, "text"); v != nil {
		text = vc.gc.EvalExpr(v)
	} else if v := codegen.NodeProp(n, "label"); v != nil {
		text = vc.gc.EvalExpr(v)
	}

	id := vc.labelCount
	vc.labelCount++
	fieldName := fmt.Sprintf("btn%d", id)
	vc.addField(fieldName, "*widget.Button")

	clickHandler := codegen.NodeHandler(n, "click")
	if clickHandler != nil && clickHandler.Func != nil {
		vc.line("m.%s = widget.NewButton(fmt.Sprint(%s), func() {", fieldName, text)
		vc.indent++
		vc.emitEventHandlerBlock(clickHandler.Func.Block)
		vc.indent--
		vc.line("})")
	} else {
		vc.line("m.%s = widget.NewButton(fmt.Sprint(%s), nil)", fieldName, text)
	}
	vc.line("%s = m.%s", resultVar, fieldName)
}

func (vc *irViewContext) renderEntry(n *ir.NodeInst, resultVar string) {
	idx := vc.entryIndex
	vc.entryIndex++
	fieldName := fmt.Sprintf("entry%d", idx)
	vc.line("%s = m.%s", resultVar, fieldName)

	// `value` attribute drives an init-time SetText and a reactive updater so
	// that the entry's text stays in sync when the source expression changes
	// (e.g. textarea(value=jira.IssuesPretty(issues))). Without this, the
	// constructor only writes SetText for input/textarea via the bind-target
	// shortcut — read-only textareas and inputs whose value is a non-bind
	// expression render empty.
	contentExpr := vc.resolveContentIRExpr(n)
	if contentExpr == nil {
		return
	}
	content := vc.gc.EvalExpr(contentExpr)
	vc.line("m.%s.SetText(fmt.Sprint(%s))", fieldName, content)
	deps := vc.exprDeps(contentExpr)
	if len(deps) > 0 {
		updaterName := fmt.Sprintf("updateEntry%d", idx)
		body := fmt.Sprintf("m.%s.SetText(fmt.Sprint(%s))", fieldName, content)
		vc.addUpdater(updaterName, body, deps)
	}
}

func (vc *irViewContext) renderCheck(n *ir.NodeInst, resultVar string) {
	label := `""`
	if v := codegen.NodeProp(n, "label"); v != nil {
		label = vc.gc.EvalExpr(v)
	}

	id := vc.labelCount
	vc.labelCount++
	fieldName := fmt.Sprintf("check%d", id)
	vc.addField(fieldName, "*widget.Check")

	changeHandler := codegen.NodeHandler(n, "change")
	if changeHandler != nil && changeHandler.Func != nil {
		vc.line("m.%s = widget.NewCheck(fmt.Sprint(%s), func(b bool) {", fieldName, label)
		vc.indent++
		vc.emitEventHandlerBlock(changeHandler.Func.Block)
		vc.indent--
		vc.line("})")
	} else {
		vc.line("m.%s = widget.NewCheck(fmt.Sprint(%s), nil)", fieldName, label)
	}
	vc.line("%s = m.%s", resultVar, fieldName)
}

func (vc *irViewContext) renderSelect(n *ir.NodeInst, resultVar string) {
	id := vc.labelCount
	vc.labelCount++
	fieldName := fmt.Sprintf("sel%d", id)
	vc.addField(fieldName, "*widget.Select")

	changeHandler := codegen.NodeHandler(n, "change")
	if changeHandler != nil && changeHandler.Func != nil {
		vc.line("m.%s = widget.NewSelect(nil, func(s string) {", fieldName)
		vc.indent++
		vc.emitEventHandlerBlock(changeHandler.Func.Block)
		vc.indent--
		vc.line("})")
	} else {
		vc.line("m.%s = widget.NewSelect(nil, nil)", fieldName)
	}
	vc.line("%s = m.%s", resultVar, fieldName)
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
