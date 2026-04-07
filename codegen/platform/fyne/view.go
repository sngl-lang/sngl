package fyne

import (
	"fmt"
	"maps"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// viewContext tracks state during BuildUI() code generation.
type viewContext struct {
	ec         *exprContext
	buf        *strings.Builder
	indent     int
	components []*ast.Component
	entryIndex int             // next entry index for persistent entry fields
	info       *analysisResult // for dependency tracking (nil for component renders)

	// Selective update tracking — collected during rendering
	widgetFields   []widgetField
	updaters       []widgetUpdater
	labelCount     int
	btnCount       int
	checkCount     int
	containerCount int
	progressCount  int
	radioCount     int
	selectCount    int
	scrollCount    int
	badgeCount     int
	spinnerCount   int
	slotVar        string // variable holding pre-rendered slot content

	// Component expansion support
	doc            *ast.Document
	slotChildren   []*ast.VisualNode
	callerEvents   map[string]ast.EventHandler
	componentDepth int
}

func (vc *viewContext) line(format string, args ...any) {
	fmt.Fprintf(vc.buf, "%s"+format+"\n", append([]any{strings.Repeat("\t", vc.indent)}, args...)...)
}

func (vc *viewContext) addField(name, goType string) {
	vc.widgetFields = append(vc.widgetFields, widgetField{name, goType})
}

func (vc *viewContext) addUpdater(name, body string, deps map[string]bool) {
	if len(deps) == 0 {
		return
	}
	vc.updaters = append(vc.updaters, widgetUpdater{name: name, body: body, deps: deps})
}

func (vc *viewContext) exprDeps(expr ast.Expr) map[string]bool {
	if vc.info == nil {
		return nil
	}
	return vc.info.depTracker().ExprDeps(expr)
}

func (vc *viewContext) emitEventHandler(evtNode ast.Node) {
	stmts := vc.ec.TranslateMutation(evtNode)
	for _, s := range stmts {
		vc.line("%s", s)
	}
	if vc.info != nil {
		mutated := codegen.MutatedFields(evtNode)
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

func (vc *viewContext) renderNode(vn *ast.VisualNode, resultVar string) {
	hasIf := vn.If != nil
	if hasIf {
		if vc.info != nil && vn.For == nil {
			vc.renderConditionalNode(vn, resultVar)
			return
		}
		cond := exprToGoCond(*vn.If, vc.ec)
		vc.line("if %s {", cond)
		vc.indent++
	}

	if vn.For != nil {
		vc.renderForLoop(vn, resultVar)
	} else {
		vc.renderNodeInner(vn, resultVar)
	}

	if hasIf {
		vc.indent--
		vc.line("}")
	}
}

func (vc *viewContext) renderConditionalNode(vn *ast.VisualNode, resultVar string) {
	id := vc.containerCount
	vc.containerCount++
	fieldName := fmt.Sprintf("ifBox%d", id)
	vc.addField(fieldName, "*fyne.Container")

	innerVar := resultVar + "Inner"
	vc.line("var %s fyne.CanvasObject", innerVar)

	savedIf := vn.If
	vn.If = nil
	if vn.For != nil {
		vc.renderForLoop(vn, innerVar)
	} else {
		vc.renderNodeInner(vn, innerVar)
	}
	vn.If = savedIf

	vc.line("if %s == nil { %s = widget.NewLabel(\"\") }", innerVar, innerVar)
	vc.line("m.%s = container.NewStack(%s)", fieldName, innerVar)

	cond := exprToGoCond(*vn.If, vc.ec)
	vc.line("if !(%s) { m.%s.Hide() }", cond, fieldName)
	vc.line("%s = m.%s", resultVar, fieldName)

	deps := vc.exprDeps(*vn.If)
	if len(deps) > 0 {
		updaterName := fmt.Sprintf("updateIf%d", id)
		body := fmt.Sprintf("if %s { m.%s.Show() } else { m.%s.Hide() }", cond, fieldName, fieldName)
		vc.addUpdater(updaterName, body, deps)
	}
}

func (vc *viewContext) renderForLoop(vn *ast.VisualNode, resultVar string) {
	iterVar := vn.For.Variable
	iterExpr := exprToGoValue(vn.For.Iterable, vc.ec)
	indexVar := "_"
	if vn.For.IndexVar != "" {
		indexVar = vn.For.IndexVar
		vc.ec.LocalVars[indexVar] = true
		defer func() { delete(vc.ec.LocalVars, indexVar) }()
	}
	vc.ec.LocalVars[iterVar] = true
	defer func() { delete(vc.ec.LocalVars, iterVar) }()

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
		vc.renderNodeInner(vn, innerVar)
		vc.line("if %s != nil { %s = append(%s, %s) }", innerVar, loopItems, loopItems, innerVar)
		vc.indent--
		vc.line("}")
		if len(vn.For.Else) > 0 {
			vc.line("if len(%s) == 0 {", iterExpr)
			vc.indent++
			for _, elseNode := range vn.For.Else {
				vc.renderNodeInner(elseNode, loopItems+"Else")
				vc.line("%s = append(%s, %sElse)", loopItems, loopItems, loopItems)
			}
			vc.indent--
			vc.line("}")
		}
		vc.line("m.%s = container.NewVBox(%s...)", fieldName, loopItems)
		vc.line("%s = m.%s", resultVar, fieldName)

		deps := vc.exprDeps(vn.For.Iterable)
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
	vc.renderNodeInner(vn, innerVar)
	vc.line("if %s != nil { %s = append(%s, %s) }", innerVar, loopItems, loopItems, innerVar)
	vc.indent--
	vc.line("}")
	if len(vn.For.Else) > 0 {
		vc.line("if len(%s) == 0 {", iterExpr)
		vc.indent++
		for _, elseNode := range vn.For.Else {
			elseVar := loopItems + "Else"
			vc.line("var %s fyne.CanvasObject", elseVar)
			vc.renderNodeInner(elseNode, elseVar)
			vc.line("if %s != nil { %s = append(%s, %s) }", elseVar, loopItems, loopItems, elseVar)
		}
		vc.indent--
		vc.line("}")
	}
	vc.line("%s = container.NewVBox(%s...)", resultVar, loopItems)
}

func (vc *viewContext) renderNodeInner(vn *ast.VisualNode, resultVar string) {
	if vn.Component == "slot" {
		if len(vc.slotChildren) > 0 {
			for i, child := range vc.slotChildren {
				childVar := fmt.Sprintf("%sSlot%d", resultVar, i)
				vc.line("var %s fyne.CanvasObject", childVar)
				vc.renderNode(child, childVar)
				vc.line("if %s != nil { %s = %s }", childVar, resultVar, childVar)
			}
		} else if vc.slotVar != "" {
			vc.line(`%s = %s`, resultVar, vc.slotVar)
		}
		return
	}

	comp := vc.findComponent(vn.Component)
	if comp != nil {
		body := codegen.ResolveComponentBody(comp, "fyne")
		if len(body) > 0 {
			vc.expandComponent(comp, vn, body, resultVar)
		} else {
			vc.renderUserComponent(vn, resultVar)
		}
		return
	}

	vc.renderRawWidget(vn, resultVar)
}

func (vc *viewContext) findComponent(name string) *ast.Component {
	if vc.doc != nil {
		return vc.doc.FindComponent(name)
	}
	for _, c := range vc.components {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func (vc *viewContext) expandComponent(comp *ast.Component, vn *ast.VisualNode, body []*ast.VisualNode, resultVar string) {
	vc.componentDepth++
	if vc.componentDepth > 10 {
		vc.componentDepth--
		return
	}
	defer func() { vc.componentDepth-- }()

	savedLocals := maps.Clone(vc.ec.LocalVars)
	savedSlot := vc.slotChildren
	savedEvents := vc.callerEvents

	savedOverrides := vc.ec.PropOverrides
	overrides := make(map[string]string)
	if savedOverrides != nil {
		maps.Copy(overrides, savedOverrides)
	}
	for _, p := range comp.Params {
		vc.ec.LocalVars[p.Name] = true
		if expr, ok := vn.Props[p.Name]; ok {
			overrides[p.Name] = exprToGoValue(expr, vc.ec)
		} else if p.Default.Literal != nil {
			overrides[p.Name] = literalToGo(p.Default)
		}
	}
	vc.ec.PropOverrides = overrides

	vc.slotChildren = vn.Children
	vc.callerEvents = vn.Events

	for _, child := range body {
		vc.renderNode(child, resultVar)
	}

	vc.ec.LocalVars = savedLocals
	vc.ec.PropOverrides = savedOverrides
	vc.slotChildren = savedSlot
	vc.callerEvents = savedEvents
}

// renderRawWidget renders an implicit Fyne widget by interpreting metadata props.
// Patterns:
//   - container="NewVBox" → container.NewVBox(children...)
//   - widgetType + widgetNew + widgetArg → persistent widget with updater
//   - fallback → widget.NewLabel(content)
func (vc *viewContext) renderRawWidget(vn *ast.VisualNode, resultVar string) {
	// Container pattern
	if ctr, ok := vn.Props["container"]; ok {
		if s, ok := ctr.Literal.(string); ok {
			childrenVar := resultVar + "Children"
			vc.line("var %s []fyne.CanvasObject", childrenVar)
			for i, child := range vn.Children {
				itemVar := fmt.Sprintf("%sC%d", resultVar, i)
				vc.line("var %s fyne.CanvasObject", itemVar)
				vc.renderNode(child, itemVar)
				vc.line("if %s != nil { %s = append(%s, %s) }", itemVar, childrenVar, childrenVar, itemVar)
			}
			vc.line("%s = container.%s(%s...)", resultVar, s, childrenVar)
			return
		}
	}

	// Persistent widget pattern
	if wType, ok := vn.Props["widgetType"]; ok {
		if goType, ok := wType.Literal.(string); ok {
			wNew := ""
			if v, ok := vn.Props["widgetNew"]; ok {
				if s, ok := v.Literal.(string); ok {
					wNew = s
				}
			}
			wArg := ""
			wArgProp := ""
			if v, ok := vn.Props["widgetArg"]; ok {
				if s, ok := v.Literal.(string); ok {
					wArgProp = s
				}
			}
			if wArgProp != "" {
				if expr, ok := vn.Props[wArgProp]; ok {
					wArg = exprToGoValue(expr, vc.ec)
				} else {
					wArg = `""`
				}
			}

			// Allocate persistent field
			id := vc.labelCount
			vc.labelCount++
			fieldName := fmt.Sprintf("widget%d", id)
			vc.addField(fieldName, goType)

			// Extra constructor args (e.g., nil callback for widget.NewButton)
			extraArgs := ""
			if v, ok := vn.Props["widgetExtraArgs"]; ok {
				if s, ok := v.Literal.(string); ok {
					extraArgs = ", " + s
				}
			}

			if wNew != "" {
				if wArg != "" {
					vc.line("m.%s = %s(fmt.Sprint(%s)%s)", fieldName, wNew, wArg, extraArgs)
				} else {
					vc.line("m.%s = %s(%s)", fieldName, wNew, strings.TrimPrefix(extraArgs, ", "))
				}
			}
			vc.line("%s = m.%s", resultVar, fieldName)

			// Register updater if arg has deps
			wUpdate := ""
			if v, ok := vn.Props["widgetUpdate"]; ok {
				if s, ok := v.Literal.(string); ok {
					wUpdate = s
				}
			}
			if wUpdate != "" && wArgProp != "" {
				if expr, ok := vn.Props[wArgProp]; ok {
					deps := vc.exprDeps(expr)
					if len(deps) > 0 {
						updaterName := fmt.Sprintf("updateWidget%d", id)
						body := fmt.Sprintf("m.%s%s(fmt.Sprint(%s))", fieldName, wUpdate, wArg)
						vc.addUpdater(updaterName, body, deps)
					}
				}
			}

			// Handle click event
			if clickEvt, ok := vc.callerEvents["click"]; ok && clickEvt.Body.SNGL != nil {
				vc.line("// TODO: wire click event on persistent widget")
			}
			return
		}
	}

	// Entry (input) pattern — uses existing entry infrastructure
	if _, ok := vn.Props["entry"]; ok {
		idx := vc.entryIndex
		vc.entryIndex++
		vc.line("%s = m.entry%d", resultVar, idx)
		return
	}

	// Fallback: emit as label
	content := `""`
	if v, ok := vn.Props["content"]; ok {
		content = exprToGoValue(v, vc.ec)
	}
	vc.line("%s = widget.NewLabel(fmt.Sprint(%s))", resultVar, content)
}

func (vc *viewContext) renderUserComponent(vn *ast.VisualNode, resultVar string) {
	var comp *ast.Component
	for _, c := range vc.components {
		if c.Name == vn.Component {
			comp = c
			break
		}
	}
	if comp == nil {
		vc.line("%s = widget.NewLabel(\"[unknown: %s]\")", resultVar, vn.Component)
		return
	}

	var args []string
	for _, p := range comp.Params {
		if v, ok := vn.Props[p.Name]; ok {
			args = append(args, exprToGoValue(v, vc.ec))
		} else {
			args = append(args, "nil")
		}
	}

	if comp.ChildrenType != "" && len(vn.Children) > 0 {
		slotVar := resultVar + "Slot"
		vc.renderChildrenToObject(vn.Children, slotVar)
		args = append(args, slotVar)
	}

	vc.line("%s = m.render%s(%s)", resultVar, exportName(vn.Component), strings.Join(args, ", "))
}

func (vc *viewContext) renderChildrenToObject(children []*ast.VisualNode, resultVar string) {
	if len(children) == 1 {
		vc.line("var %s fyne.CanvasObject", resultVar)
		vc.renderNode(children[0], resultVar)
	} else {
		partsVar := resultVar + "Parts"
		vc.line("var %s []fyne.CanvasObject", partsVar)
		for i, child := range children {
			childVar := fmt.Sprintf("%sPart%d", resultVar, i)
			vc.line("var %s fyne.CanvasObject", childVar)
			vc.renderNode(child, childVar)
			vc.line("%s = append(%s, %s)", partsVar, partsVar, childVar)
		}
		vc.line("%s := container.NewVBox(%s...)", resultVar, partsVar)
	}
}
