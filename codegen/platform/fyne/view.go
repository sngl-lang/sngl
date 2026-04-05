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
	slotVar        string // variable holding pre-rendered slot content (for abstract components)
}

func (vc *viewContext) line(format string, args ...any) {
	fmt.Fprintf(vc.buf, "%s"+format+"\n", append([]any{strings.Repeat("\t", vc.indent)}, args...)...)
}

// addField registers a persistent widget field on the Model.
func (vc *viewContext) addField(name, goType string) {
	vc.widgetFields = append(vc.widgetFields, widgetField{name, goType})
}

// addUpdater registers an updater function if there are state dependencies.
func (vc *viewContext) addUpdater(name, body string, deps map[string]bool) {
	if len(deps) == 0 {
		return
	}
	vc.updaters = append(vc.updaters, widgetUpdater{
		name: name,
		body: body,
		deps: deps,
	})
}

// exprDeps extracts expanded deps for an ast.Expr. Returns nil if no info or no deps.
func (vc *viewContext) exprDeps(expr ast.Expr) map[string]bool {
	if vc.info == nil {
		return nil
	}
	return vc.info.depTracker().ExprDeps(expr)
}

// emitEventHandler emits mutation statements and affected updater calls for an event.
func (vc *viewContext) emitEventHandler(evtNode ast.Node) {
	stmts := vc.ec.translateMutation(evtNode)
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

// renderNode generates Go code that creates a fyne.CanvasObject and assigns
// the result to resultVar. If the node has an if-condition, resultVar may
// remain nil.
func (vc *viewContext) renderNode(vn *ast.VisualNode, resultVar string) {
	hasIf := vn.If != nil
	if hasIf {
		// For conditional nodes with info, use a persistent container with Show/Hide
		if vc.info != nil && !hasForLoop(vn) {
			vc.renderConditionalNode(vn, resultVar)
			return
		}
		cond := exprToGoCond(*vn.If, vc.ec)
		vc.line("if %s {", cond)
		vc.indent++
	}

	hasFor := vn.For != nil
	if hasFor {
		vc.renderForLoop(vn, resultVar)
	} else {
		vc.renderNodeInner(vn, resultVar)
	}

	if hasIf {
		vc.indent--
		vc.line("}")
	}
}

func hasForLoop(vn *ast.VisualNode) bool {
	return vn.For != nil
}

// renderConditionalNode renders an if-conditioned node as a persistent container
// with Show/Hide updater.
func (vc *viewContext) renderConditionalNode(vn *ast.VisualNode, resultVar string) {
	id := vc.containerCount
	vc.containerCount++
	fieldName := fmt.Sprintf("ifBox%d", id)
	vc.addField(fieldName, "*fyne.Container")

	// Render the inner content into a temporary var
	innerVar := resultVar + "Inner"
	vc.line("var %s fyne.CanvasObject", innerVar)

	// Temporarily strip the if condition to render just the inner node
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

	// Set initial visibility
	cond := exprToGoCond(*vn.If, vc.ec)
	vc.line("if !(%s) { m.%s.Hide() }", cond, fieldName)
	vc.line("%s = m.%s", resultVar, fieldName)

	// Register visibility updater
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
		vc.ec.localVars[indexVar] = true
		defer func() { delete(vc.ec.localVars, indexVar) }()
	}
	vc.ec.localVars[iterVar] = true
	defer func() { delete(vc.ec.localVars, iterVar) }()

	// For loops with info get a persistent container that rebuilds via updater
	if vc.info != nil {
		id := vc.containerCount
		vc.containerCount++
		fieldName := fmt.Sprintf("forBox%d", id)
		vc.addField(fieldName, "*fyne.Container")

		// Build the loop inline for initial creation
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

		// Register an updater that rebuilds the for-loop container
		deps := vc.exprDeps(vn.For.Iterable)
		if len(deps) > 0 {
			// The updater body rebuilds the container's children
			updaterName := fmt.Sprintf("updateFor%d", id)
			// Build the updater body as multi-line code
			var bodyBuf strings.Builder
			fmt.Fprintf(&bodyBuf, "var items []fyne.CanvasObject\n")
			fmt.Fprintf(&bodyBuf, "\tfor %s, %s := range %s {\n", indexVar, iterVar, iterExpr)
			if indexVar != "_" {
				fmt.Fprintf(&bodyBuf, "\t\t_ = %s\n", indexVar)
			}
			// For the updater, we need a simplified inner render
			// Use doRefresh-style full rebuild of just this container
			fmt.Fprintf(&bodyBuf, "\t\t_ = %s\n", iterVar)
			fmt.Fprintf(&bodyBuf, "\t\titems = append(items, widget.NewLabel(fmt.Sprint(%s)))\n", iterVar)
			fmt.Fprintf(&bodyBuf, "\t}\n")
			fmt.Fprintf(&bodyBuf, "\tm.%s.Objects = items\n", fieldName)
			fmt.Fprintf(&bodyBuf, "\tm.%s.Refresh()", fieldName)
			vc.addUpdater(updaterName, bodyBuf.String(), deps)
		}
		return
	}

	// Fallback: no info context (component renders)
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
	switch vn.Component {
	case "vbox":
		vc.renderBox(vn, resultVar, true)
	case "hbox":
		vc.renderBox(vn, resultVar, false)
	case "stack":
		vc.renderStack(vn, resultVar)
	case "text":
		vc.renderText(vn, resultVar)
	case "button":
		vc.renderButton(vn, resultVar)
	case "input":
		vc.renderInput(vn, resultVar)
	case "checkbox":
		vc.renderCheckbox(vn, resultVar)
	case "spacer":
		vc.line("%s = layout.NewSpacer()", resultVar)
	case "image":
		vc.renderImage(vn, resultVar)
	case "scroll":
		vc.renderScroll(vn, resultVar)
	case "radio":
		vc.renderRadio(vn, resultVar)
	case "toggle":
		vc.renderToggle(vn, resultVar)
	case "select":
		vc.renderSelectComp(vn, resultVar)
	case "textarea":
		vc.renderTextarea(vn, resultVar)
	case "progress":
		vc.renderProgress(vn, resultVar)
	case "spinner":
		vc.renderSpinner(vn, resultVar)
	case "badge":
		vc.renderBadge(vn, resultVar)
	case "tabs":
		vc.renderTabs(vn, resultVar)
	case "link":
		vc.renderLink(vn, resultVar)
	case "divider":
		vc.renderDivider(vn, resultVar)
	case "modal":
		vc.renderConditionalContainer(vn, resultVar)
	case "drawer":
		vc.renderConditionalContainer(vn, resultVar)
	case "tooltip":
		// Fyne has no direct tooltip widget; render child
		if len(vn.Children) > 0 {
			vc.renderNode(vn.Children[0], resultVar)
		}
	case "popover":
		vc.renderConditionalContainer(vn, resultVar)
	case "accordion":
		vc.renderAccordion(vn, resultVar)
	case "splitview":
		vc.renderSplitview(vn, resultVar)
	case "card":
		vc.renderCard(vn, resultVar)
	case "table":
		vc.renderTable(vn, resultVar)
	case "tree":
		vc.renderTree(vn, resultVar)
	case "chip":
		vc.renderChip(vn, resultVar)
	case "avatar":
		vc.renderAvatar(vn, resultVar)
	case "menubar":
		vc.renderMenubar(vn, resultVar)
	case "toolbar":
		vc.renderToolbar(vn, resultVar)
	case "datepicker":
		vc.renderDatepicker(vn, resultVar)
	case "slot":
		if vc.slotVar != "" {
			vc.line(`%s = %s`, resultVar, vc.slotVar)
		} else {
			vc.line(`%s = widget.NewLabel("")`, resultVar)
		}
	default:
		// Try user-defined or abstract component
		vc.renderUserComponent(vn, resultVar)
	}
}

// renderBox renders a vbox or hbox container.
func (vc *viewContext) renderBox(vn *ast.VisualNode, resultVar string, vertical bool) {
	childrenVar := resultVar + "Children"
	vc.line("var %s []fyne.CanvasObject", childrenVar)

	for i, child := range vn.Children {
		itemVar := fmt.Sprintf("%sC%d", resultVar, i)
		vc.line("var %s fyne.CanvasObject", itemVar)
		vc.renderNode(child, itemVar)
		vc.line("if %s != nil { %s = append(%s, %s) }", itemVar, childrenVar, childrenVar, itemVar)
	}

	// Apply padding if specified
	hasPadding := false
	if sf := vn.StyleFields(); sf != nil {
		if _, ok := sf["padding"]; ok {
			hasPadding = true
		}
	}

	if vertical {
		vc.line("%s = container.NewVBox(%s...)", resultVar, childrenVar)
	} else {
		vc.line("%s = container.NewGridWithColumns(len(%s), %s...)", resultVar, childrenVar, childrenVar)
	}

	if hasPadding {
		vc.line("%s = container.NewPadded(%s)", resultVar, resultVar)
	}

	// Wrap with scroll if requested — store persistently to preserve scroll position
	if v, ok := vn.Props["scroll"]; ok {
		val := exprToGoValue(v, vc.ec)
		if val == "true" && vc.info != nil {
			id := vc.scrollCount
			vc.scrollCount++
			fieldName := fmt.Sprintf("scroll%d", id)
			vc.addField(fieldName, "*container.Scroll")
			vc.line("m.%s = container.NewVScroll(%s)", fieldName, resultVar)
			vc.line("%s = m.%s", resultVar, fieldName)
		} else if val == "true" {
			vc.line("%s = container.NewVScroll(%s)", resultVar, resultVar)
		}
	}
}

// renderStack renders a stack container (overlapping children).
func (vc *viewContext) renderStack(vn *ast.VisualNode, resultVar string) {
	childrenVar := resultVar + "Children"
	vc.line("var %s []fyne.CanvasObject", childrenVar)
	for i, child := range vn.Children {
		itemVar := fmt.Sprintf("%sC%d", resultVar, i)
		vc.line("var %s fyne.CanvasObject", itemVar)
		vc.renderNode(child, itemVar)
		vc.line("if %s != nil { %s = append(%s, %s) }", itemVar, childrenVar, childrenVar, itemVar)
	}
	vc.line("%s = container.NewStack(%s...)", resultVar, childrenVar)
}

// renderText renders a text/label widget. If the value depends on state, it is
// stored persistently with an updater.
func (vc *viewContext) renderText(vn *ast.VisualNode, resultVar string) {
	val := `""`
	var valExpr *ast.Expr
	if v, ok := vn.Props["value"]; ok {
		val = exprToGoValue(v, vc.ec)
		valExpr = &v
	}

	// Check for bold/italic from style
	bold := false
	if sf := vn.StyleFields(); sf != nil {
		if v, ok := sf["fontWeight"]; ok {
			if s, ok := v.Literal.(string); ok && s == "bold" {
				bold = true
			}
		}
	}

	// Handle click event — wrap as button
	if clickEvt, ok := vn.Events["click"]; ok && clickEvt.SNGL != nil {
		id := vc.btnCount
		vc.btnCount++
		fieldName := fmt.Sprintf("btn%d", id)
		vc.addField(fieldName, "*widget.Button")
		vc.line("m.%s = widget.NewButton(fmt.Sprint(%s), func() {", fieldName, val)
		vc.indent++
		vc.emitEventHandler(clickEvt.SNGL)
		vc.indent--
		vc.line("})")
		vc.line("%s = m.%s", resultVar, fieldName)

		// If button text is dynamic, add updater
		if valExpr != nil {
			deps := vc.exprDeps(*valExpr)
			if len(deps) > 0 {
				updaterName := fmt.Sprintf("updateBtn%d", id)
				body := fmt.Sprintf("m.%s.SetText(fmt.Sprint(%s))", fieldName, val)
				vc.addUpdater(updaterName, body, deps)
			}
		}
		return
	}

	// Determine if we need a persistent label
	var deps map[string]bool
	if valExpr != nil {
		deps = vc.exprDeps(*valExpr)
	}

	if vc.info != nil && len(deps) > 0 {
		// Dynamic label: store on Model with updater
		id := vc.labelCount
		vc.labelCount++
		fieldName := fmt.Sprintf("label%d", id)
		vc.addField(fieldName, "*widget.Label")

		if bold {
			vc.line("m.%s = widget.NewLabelWithStyle(fmt.Sprint(%s), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})", fieldName, val)
		} else {
			vc.line("m.%s = widget.NewLabel(fmt.Sprint(%s))", fieldName, val)
		}
		vc.line("%s = m.%s", resultVar, fieldName)

		updaterName := fmt.Sprintf("updateLabel%d", id)
		body := fmt.Sprintf("m.%s.SetText(fmt.Sprint(%s))", fieldName, val)
		vc.addUpdater(updaterName, body, deps)
	} else {
		// Static label: no updater needed
		if bold {
			vc.line("%s = widget.NewLabelWithStyle(fmt.Sprint(%s), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})", resultVar, val)
		} else {
			vc.line("%s = widget.NewLabel(fmt.Sprint(%s))", resultVar, val)
		}
	}
}

// renderButton renders a button widget.
func (vc *viewContext) renderButton(vn *ast.VisualNode, resultVar string) {
	text := `""`
	var textExpr *ast.Expr
	if v, ok := vn.Props["text"]; ok {
		text = exprToGoValue(v, vc.ec)
		textExpr = &v
	}

	id := vc.btnCount
	vc.btnCount++
	fieldName := fmt.Sprintf("btn%d", id)
	vc.addField(fieldName, "*widget.Button")

	if clickEvt, ok := vn.Events["click"]; ok && clickEvt.SNGL != nil {
		vc.line("m.%s = widget.NewButton(%s, func() {", fieldName, text)
		vc.indent++
		vc.emitEventHandler(clickEvt.SNGL)
		vc.indent--
		vc.line("})")
	} else {
		vc.line("m.%s = widget.NewButton(%s, nil)", fieldName, text)
	}
	vc.line("%s = m.%s", resultVar, fieldName)

	// Disabled
	if v, ok := vn.Props["disabled"]; ok {
		val := exprToGoValue(v, vc.ec)
		vc.line("if %s { m.%s.Disable() }", val, fieldName)

		// Add updater for disabled state if dynamic
		deps := vc.exprDeps(v)
		if len(deps) > 0 {
			updaterName := fmt.Sprintf("updateBtn%dDisabled", id)
			body := fmt.Sprintf("if %s { m.%s.Disable() } else { m.%s.Enable() }", val, fieldName, fieldName)
			vc.addUpdater(updaterName, body, deps)
		}
	}

	// Add updater for dynamic button text
	if textExpr != nil {
		deps := vc.exprDeps(*textExpr)
		if len(deps) > 0 {
			updaterName := fmt.Sprintf("updateBtn%dText", id)
			body := fmt.Sprintf("m.%s.SetText(fmt.Sprint(%s))", fieldName, text)
			vc.addUpdater(updaterName, body, deps)
		}
	}
}

// renderInput renders a persistent Entry widget (stored on the model).
func (vc *viewContext) renderInput(vn *ast.VisualNode, resultVar string) {
	idx := vc.entryIndex
	vc.entryIndex++
	vc.line("%s = m.entry%d", resultVar, idx)
}

// renderTextarea renders a persistent MultiLineEntry widget.
func (vc *viewContext) renderTextarea(vn *ast.VisualNode, resultVar string) {
	idx := vc.entryIndex
	vc.entryIndex++
	vc.line("%s = m.entry%d", resultVar, idx)
}

// renderCheckbox renders a Check widget stored persistently.
func (vc *viewContext) renderCheckbox(vn *ast.VisualNode, resultVar string) {
	label := `""`
	if v, ok := vn.Props["label"]; ok {
		label = exprToGoValue(v, vc.ec)
	}

	checkedExpr := "false"
	var checkedProp *ast.Expr
	if v, ok := vn.Props["checked"]; ok {
		checkedExpr = exprToGoValue(v, vc.ec)
		checkedProp = &v
	}

	id := vc.checkCount
	vc.checkCount++
	fieldName := fmt.Sprintf("check%d", id)
	vc.addField(fieldName, "*widget.Check")

	if changeEvt, ok := vn.Events["change"]; ok && changeEvt.SNGL != nil {
		vc.line("m.%s = widget.NewCheck(%s, func(checked bool) {", fieldName, label)
		vc.indent++
		prevEventVar := vc.ec.eventVar
		vc.ec.eventVar = "checked"
		vc.emitEventHandler(changeEvt.SNGL)
		vc.ec.eventVar = prevEventVar
		vc.indent--
		vc.line("})")
	} else {
		vc.line("m.%s = widget.NewCheck(%s, nil)", fieldName, label)
	}
	vc.line("m.%s.Checked = %s", fieldName, checkedExpr)
	vc.line("%s = m.%s", resultVar, fieldName)

	// Updater for checked state
	if checkedProp != nil {
		deps := vc.exprDeps(*checkedProp)
		if len(deps) > 0 {
			updaterName := fmt.Sprintf("updateCheck%d", id)
			body := fmt.Sprintf("m.%s.Checked = %s\nm.%s.Refresh()", fieldName, checkedExpr, fieldName)
			vc.addUpdater(updaterName, body, deps)
		}
	}
}

// renderRadio renders a RadioGroup widget.
func (vc *viewContext) renderRadio(vn *ast.VisualNode, resultVar string) {
	options := "nil"
	if v, ok := vn.Props["options"]; ok {
		options = exprToGoStringList(v, vc.ec)
	}

	selected := `""`
	var selectedProp *ast.Expr
	if v, ok := vn.Props["value"]; ok {
		selected = exprToGoValue(v, vc.ec)
		selectedProp = &v
	}

	id := vc.radioCount
	vc.radioCount++
	fieldName := fmt.Sprintf("radio%d", id)
	vc.addField(fieldName, "*widget.RadioGroup")

	if changeEvt, ok := vn.Events["change"]; ok && changeEvt.SNGL != nil {
		vc.line("m.%s = widget.NewRadioGroup(%s, func(s string) {", fieldName, options)
		vc.indent++
		prevEventVar := vc.ec.eventVar
		vc.ec.eventVar = "s"
		vc.emitEventHandler(changeEvt.SNGL)
		vc.ec.eventVar = prevEventVar
		vc.indent--
		vc.line("})")
	} else {
		vc.line("m.%s = widget.NewRadioGroup(%s, nil)", fieldName, options)
	}
	vc.line("m.%s.Selected = %s", fieldName, selected)

	// Horizontal layout
	if v, ok := vn.Props["direction"]; ok {
		if s, ok := v.Literal.(string); ok && s == "horizontal" {
			vc.line("m.%s.Horizontal = true", fieldName)
		}
	}

	vc.line("%s = m.%s", resultVar, fieldName)

	// Updater for selected value
	if selectedProp != nil {
		deps := vc.exprDeps(*selectedProp)
		if len(deps) > 0 {
			updaterName := fmt.Sprintf("updateRadio%d", id)
			body := fmt.Sprintf("m.%s.Selected = %s\nm.%s.Refresh()", fieldName, selected, fieldName)
			vc.addUpdater(updaterName, body, deps)
		}
	}
}

// renderToggle renders a Check widget (Fyne has no toggle; use check).
func (vc *viewContext) renderToggle(vn *ast.VisualNode, resultVar string) {
	vc.renderCheckbox(vn, resultVar)
}

// renderSelectComp renders a Select widget.
func (vc *viewContext) renderSelectComp(vn *ast.VisualNode, resultVar string) {
	options := "nil"
	if v, ok := vn.Props["options"]; ok {
		options = exprToGoStringList(v, vc.ec)
	}

	selected := `""`
	var selectedProp *ast.Expr
	if v, ok := vn.Props["value"]; ok {
		selected = exprToGoValue(v, vc.ec)
		selectedProp = &v
	}

	id := vc.selectCount
	vc.selectCount++
	fieldName := fmt.Sprintf("sel%d", id)
	vc.addField(fieldName, "*widget.Select")

	if changeEvt, ok := vn.Events["change"]; ok && changeEvt.SNGL != nil {
		vc.line("m.%s = widget.NewSelect(%s, func(s string) {", fieldName, options)
		vc.indent++
		prevEventVar := vc.ec.eventVar
		vc.ec.eventVar = "s"
		vc.emitEventHandler(changeEvt.SNGL)
		vc.ec.eventVar = prevEventVar
		vc.indent--
		vc.line("})")
	} else {
		vc.line("m.%s = widget.NewSelect(%s, nil)", fieldName, options)
	}
	vc.line("m.%s.Selected = %s", fieldName, selected)

	if v, ok := vn.Props["placeholder"]; ok {
		vc.line("m.%s.PlaceHolder = %s", fieldName, exprToGoValue(v, vc.ec))
	}

	vc.line("%s = m.%s", resultVar, fieldName)

	// Updater for selected value
	if selectedProp != nil {
		deps := vc.exprDeps(*selectedProp)
		if len(deps) > 0 {
			updaterName := fmt.Sprintf("updateSel%d", id)
			body := fmt.Sprintf("m.%s.Selected = %s\nm.%s.Refresh()", fieldName, selected, fieldName)
			vc.addUpdater(updaterName, body, deps)
		}
	}
}

// renderProgress renders a ProgressBar widget with updater.
func (vc *viewContext) renderProgress(vn *ast.VisualNode, resultVar string) {
	id := vc.progressCount
	vc.progressCount++
	fieldName := fmt.Sprintf("progress%d", id)
	vc.addField(fieldName, "*widget.ProgressBar")

	vc.line("m.%s = widget.NewProgressBar()", fieldName)
	if v, ok := vn.Props["value"]; ok {
		val := exprToGoValue(v, vc.ec)
		maxVal := "1.0"
		if mv, ok := vn.Props["max"]; ok {
			maxVal = exprToGoValue(mv, vc.ec)
		}
		vc.line("m.%s.SetValue(float64(%s) / float64(%s))", fieldName, val, maxVal)

		// Register updater
		deps := vc.exprDeps(v)
		if mv, ok := vn.Props["max"]; ok {
			maxDeps := vc.exprDeps(mv)
			if deps == nil {
				deps = maxDeps
			} else {
				maps.Copy(deps, maxDeps)
			}
		}
		if len(deps) > 0 {
			updaterName := fmt.Sprintf("updateProgress%d", id)
			body := fmt.Sprintf("m.%s.SetValue(float64(%s) / float64(%s))", fieldName, val, maxVal)
			vc.addUpdater(updaterName, body, deps)
		}
	}
	vc.line("%s = m.%s", resultVar, fieldName)
}

// renderSpinner renders an infinite progress bar stored persistently.
func (vc *viewContext) renderSpinner(vn *ast.VisualNode, resultVar string) {
	id := vc.spinnerCount
	vc.spinnerCount++
	fieldName := fmt.Sprintf("spinner%d", id)
	vc.addField(fieldName, "*widget.ProgressBarInfinite")
	vc.line("m.%s = widget.NewProgressBarInfinite()", fieldName)
	vc.line("%s = m.%s", resultVar, fieldName)
}

// renderTabs renders an AppTabs container.
func (vc *viewContext) renderTabs(vn *ast.VisualNode, resultVar string) {
	tabsVar := resultVar + "Tabs"
	vc.line("var %s []*container.TabItem", tabsVar)

	// If items prop is a list, create tabs from it
	if items, ok := vn.Props["items"]; ok {
		if items.SNGL != nil {
			if list, ok := items.SNGL.(*ast.ListExpr); ok {
				for i, el := range list.Elements {
					label := vc.ec.translateExpr(el)
					if i < len(vn.Children) {
						childVar := fmt.Sprintf("%sTab%d", resultVar, i)
						vc.line("var %s fyne.CanvasObject", childVar)
						vc.renderNode(vn.Children[i], childVar)
						vc.line("if %s == nil { %s = widget.NewLabel(\"\") }", childVar, childVar)
						vc.line("%s = append(%s, container.NewTabItem(%s, %s))", tabsVar, tabsVar, label, childVar)
					} else {
						vc.line("%s = append(%s, container.NewTabItem(%s, widget.NewLabel(\"\")))", tabsVar, tabsVar, label)
					}
				}
			}
		}
	}

	tmpVar := resultVar + "AppTabs"
	vc.line("%s := container.NewAppTabs(%s...)", tmpVar, tabsVar)

	if v, ok := vn.Props["selected"]; ok {
		val := exprToGoValue(v, vc.ec)
		vc.line("if idx := %s; idx >= 0 && idx < len(%s.Items) { %s.SelectIndex(idx) }", val, tmpVar, tmpVar)
	}

	if changeEvt, ok := vn.Events["change"]; ok && changeEvt.SNGL != nil {
		vc.line("%s.OnSelected = func(tab *container.TabItem) {", tmpVar)
		vc.indent++
		vc.line("for i, t := range %s.Items {", tmpVar)
		vc.indent++
		vc.line("if t == tab {")
		vc.indent++
		prevEventVar := vc.ec.eventVar
		vc.ec.eventVar = "i"
		vc.emitEventHandler(changeEvt.SNGL)
		vc.ec.eventVar = prevEventVar
		vc.line("break")
		vc.indent--
		vc.line("}")
		vc.indent--
		vc.line("}")
		vc.indent--
		vc.line("}")
	}

	vc.line("%s = %s", resultVar, tmpVar)
}

// renderLink renders a Hyperlink widget.
func (vc *viewContext) renderLink(vn *ast.VisualNode, resultVar string) {
	text := `""`
	if v, ok := vn.Props["text"]; ok {
		text = exprToGoValue(v, vc.ec)
	}
	href := `""`
	if v, ok := vn.Props["href"]; ok {
		href = exprToGoValue(v, vc.ec)
	}
	vc.line("%sURL, _ := url.Parse(%s)", resultVar, href)
	vc.line("%s = widget.NewHyperlink(%s, %sURL)", resultVar, text, resultVar)
}

// renderDivider renders a Separator widget.
func (vc *viewContext) renderDivider(vn *ast.VisualNode, resultVar string) {
	vc.line("%s = widget.NewSeparator()", resultVar)
}

// renderImage renders a placeholder for images.
func (vc *viewContext) renderImage(vn *ast.VisualNode, resultVar string) {
	alt := `"[image]"`
	if v, ok := vn.Props["alt"]; ok {
		alt = exprToGoValue(v, vc.ec)
	}
	if v, ok := vn.Props["src"]; ok {
		src := exprToGoValue(v, vc.ec)
		vc.line("%s = canvas.NewImageFromFile(%s)", resultVar, src)
		vc.line("%s.(*canvas.Image).FillMode = canvas.ImageFillContain", resultVar)
	} else {
		vc.line("%s = widget.NewLabel(%s)", resultVar, alt)
	}
}

// renderScroll wraps a child in a scroll container, stored persistently.
func (vc *viewContext) renderScroll(vn *ast.VisualNode, resultVar string) {
	if len(vn.Children) > 0 {
		childVar := resultVar + "Content"
		vc.line("var %s fyne.CanvasObject", childVar)
		vc.renderNode(vn.Children[0], childVar)
		vc.line("if %s == nil { %s = widget.NewLabel(\"\") }", childVar, childVar)

		direction := "vertical"
		if v, ok := vn.Props["direction"]; ok {
			if s, ok := v.Literal.(string); ok {
				direction = s
			}
		}

		if vc.info != nil {
			id := vc.scrollCount
			vc.scrollCount++
			fieldName := fmt.Sprintf("scroll%d", id)
			vc.addField(fieldName, "*container.Scroll")
			switch direction {
			case "horizontal":
				vc.line("m.%s = container.NewHScroll(%s)", fieldName, childVar)
			default:
				vc.line("m.%s = container.NewVScroll(%s)", fieldName, childVar)
			}
			vc.line("%s = m.%s", resultVar, fieldName)
		} else {
			switch direction {
			case "horizontal":
				vc.line("%s = container.NewHScroll(%s)", resultVar, childVar)
			default:
				vc.line("%s = container.NewVScroll(%s)", resultVar, childVar)
			}
		}
	}
}

// renderBadge renders a label styled as a badge.
func (vc *viewContext) renderBadge(vn *ast.VisualNode, resultVar string) {
	val := `""`
	var valExpr *ast.Expr
	if v, ok := vn.Props["value"]; ok {
		val = exprToGoValue(v, vc.ec)
		valExpr = &v
	}

	var deps map[string]bool
	if valExpr != nil {
		deps = vc.exprDeps(*valExpr)
	}

	if vc.info != nil && len(deps) > 0 {
		id := vc.badgeCount
		vc.badgeCount++
		fieldName := fmt.Sprintf("badge%d", id)
		vc.addField(fieldName, "*widget.Label")
		vc.line("m.%s = widget.NewLabel(fmt.Sprint(%s))", fieldName, val)
		vc.line("%s = m.%s", resultVar, fieldName)

		updaterName := fmt.Sprintf("updateBadge%d", id)
		body := fmt.Sprintf("m.%s.SetText(fmt.Sprint(%s))", fieldName, val)
		vc.addUpdater(updaterName, body, deps)
	} else {
		vc.line("%s = widget.NewLabel(fmt.Sprint(%s))", resultVar, val)
	}
}

// renderConditionalContainer renders modal/drawer/popover as conditional container
// with Show/Hide.
func (vc *viewContext) renderConditionalContainer(vn *ast.VisualNode, resultVar string) {
	openExpr := "true"
	var openProp *ast.Expr
	if v, ok := vn.Props["open"]; ok {
		openExpr = exprToGoValue(v, vc.ec)
		openProp = &v
	}

	childrenVar := resultVar + "Children"
	vc.line("var %s []fyne.CanvasObject", childrenVar)

	// Add title if present
	if v, ok := vn.Props["title"]; ok {
		title := exprToGoValue(v, vc.ec)
		vc.line("%s = append(%s, widget.NewLabelWithStyle(%s, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))", childrenVar, childrenVar, title)
	}

	for i, child := range vn.Children {
		itemVar := fmt.Sprintf("%sC%d", resultVar, i)
		vc.line("var %s fyne.CanvasObject", itemVar)
		vc.renderNode(child, itemVar)
		vc.line("if %s != nil { %s = append(%s, %s) }", itemVar, childrenVar, childrenVar, itemVar)
	}

	if vc.info != nil {
		id := vc.containerCount
		vc.containerCount++
		fieldName := fmt.Sprintf("condBox%d", id)
		vc.addField(fieldName, "*fyne.Container")
		vc.line("m.%s = container.NewVBox(widget.NewCard(\"\", \"\", container.NewVBox(%s...)))", fieldName, childrenVar)
		vc.line("if !(%s) { m.%s.Hide() }", openExpr, fieldName)
		vc.line("%s = m.%s", resultVar, fieldName)

		if openProp != nil {
			deps := vc.exprDeps(*openProp)
			if len(deps) > 0 {
				updaterName := fmt.Sprintf("updateCond%d", id)
				body := fmt.Sprintf("if %s { m.%s.Show() } else { m.%s.Hide() }", openExpr, fieldName, fieldName)
				vc.addUpdater(updaterName, body, deps)
			}
		}
	} else {
		vc.line("if %s {", openExpr)
		vc.indent++
		vc.line("%s = widget.NewCard(\"\", \"\", container.NewVBox(%s...))", resultVar, childrenVar)
		vc.indent--
		vc.line("}")
	}
}

// renderAccordion renders an Accordion widget.
func (vc *viewContext) renderAccordion(vn *ast.VisualNode, resultVar string) {
	itemsVar := resultVar + "Items"
	vc.line("var %s []*widget.AccordionItem", itemsVar)

	if items, ok := vn.Props["items"]; ok {
		if items.SNGL != nil {
			if list, ok := items.SNGL.(*ast.ListExpr); ok {
				for i, el := range list.Elements {
					label := vc.ec.translateExpr(el)
					if i < len(vn.Children) {
						childVar := fmt.Sprintf("%sAcc%d", resultVar, i)
						vc.line("var %s fyne.CanvasObject", childVar)
						vc.renderNode(vn.Children[i], childVar)
						vc.line("if %s == nil { %s = widget.NewLabel(\"\") }", childVar, childVar)
						vc.line("%s = append(%s, widget.NewAccordionItem(%s, %s))", itemsVar, itemsVar, label, childVar)
					} else {
						vc.line("%s = append(%s, widget.NewAccordionItem(%s, widget.NewLabel(\"\")))", itemsVar, itemsVar, label)
					}
				}
			}
		}
	}

	vc.line("%s = widget.NewAccordion(%s...)", resultVar, itemsVar)
}

// renderSplitview renders an HSplit or VSplit container.
func (vc *viewContext) renderSplitview(vn *ast.VisualNode, resultVar string) {
	direction := "horizontal"
	if v, ok := vn.Props["direction"]; ok {
		if s, ok := v.Literal.(string); ok {
			direction = s
		}
	}

	if len(vn.Children) >= 2 {
		leftVar := resultVar + "Left"
		rightVar := resultVar + "Right"
		vc.line("var %s, %s fyne.CanvasObject", leftVar, rightVar)
		vc.renderNode(vn.Children[0], leftVar)
		vc.renderNode(vn.Children[1], rightVar)
		vc.line("if %s == nil { %s = widget.NewLabel(\"\") }", leftVar, leftVar)
		vc.line("if %s == nil { %s = widget.NewLabel(\"\") }", rightVar, rightVar)

		if direction == "vertical" {
			vc.line("%s = container.NewVSplit(%s, %s)", resultVar, leftVar, rightVar)
		} else {
			vc.line("%s = container.NewHSplit(%s, %s)", resultVar, leftVar, rightVar)
		}
	} else if len(vn.Children) == 1 {
		vc.renderNode(vn.Children[0], resultVar)
	}
}

// renderCard renders a Card widget.
func (vc *viewContext) renderCard(vn *ast.VisualNode, resultVar string) {
	childrenVar := resultVar + "Children"
	vc.line("var %s []fyne.CanvasObject", childrenVar)
	for i, child := range vn.Children {
		itemVar := fmt.Sprintf("%sC%d", resultVar, i)
		vc.line("var %s fyne.CanvasObject", itemVar)
		vc.renderNode(child, itemVar)
		vc.line("if %s != nil { %s = append(%s, %s) }", itemVar, childrenVar, childrenVar, itemVar)
	}
	vc.line("%s = widget.NewCard(\"\", \"\", container.NewVBox(%s...))", resultVar, childrenVar)
}

// renderTable renders a simple Table widget stub.
func (vc *viewContext) renderTable(vn *ast.VisualNode, resultVar string) {
	if v, ok := vn.Props["columns"]; ok {
		cols := exprToGoStringList(v, vc.ec)
		vc.line("%s = widget.NewLabel(fmt.Sprint(\"[table: \", %s, \"]\"))", resultVar, cols)
	} else {
		vc.line("%s = widget.NewLabel(\"[table]\")", resultVar)
	}
}

// renderTree renders a simple Tree widget stub.
func (vc *viewContext) renderTree(vn *ast.VisualNode, resultVar string) {
	if v, ok := vn.Props["items"]; ok {
		items := exprToGoStringList(v, vc.ec)
		vc.line("%s = widget.NewLabel(fmt.Sprint(\"[tree: \", %s, \"]\"))", resultVar, items)
	} else {
		vc.line("%s = widget.NewLabel(\"[tree]\")", resultVar)
	}
}

// renderChip renders a chip as a button-like label.
func (vc *viewContext) renderChip(vn *ast.VisualNode, resultVar string) {
	label := `""`
	if v, ok := vn.Props["label"]; ok {
		label = exprToGoValue(v, vc.ec)
	}

	id := vc.btnCount
	vc.btnCount++
	fieldName := fmt.Sprintf("btn%d", id)
	vc.addField(fieldName, "*widget.Button")

	if clickEvt, ok := vn.Events["click"]; ok && clickEvt.SNGL != nil {
		vc.line("m.%s = widget.NewButton(%s, func() {", fieldName, label)
		vc.indent++
		vc.emitEventHandler(clickEvt.SNGL)
		vc.indent--
		vc.line("})")
	} else {
		vc.line("m.%s = widget.NewButton(%s, nil)", fieldName, label)
	}
	vc.line("%s = m.%s", resultVar, fieldName)
}

// renderAvatar renders a label showing initials.
func (vc *viewContext) renderAvatar(vn *ast.VisualNode, resultVar string) {
	text := `"?"`
	if v, ok := vn.Props["initials"]; ok {
		text = exprToGoValue(v, vc.ec)
	} else if v, ok := vn.Props["alt"]; ok {
		text = exprToGoValue(v, vc.ec)
	}
	vc.line("%s = widget.NewLabel(%s)", resultVar, text)
}

// renderMenubar renders a toolbar with buttons.
func (vc *viewContext) renderMenubar(vn *ast.VisualNode, resultVar string) {
	if v, ok := vn.Props["items"]; ok {
		items := exprToGoStringList(v, vc.ec)
		vc.line("%s = widget.NewLabel(fmt.Sprint(\"[menubar: \", %s, \"]\"))", resultVar, items)
	} else {
		vc.line("%s = widget.NewLabel(\"[menubar]\")", resultVar)
	}
}

// renderToolbar renders child buttons in an HBox.
func (vc *viewContext) renderToolbar(vn *ast.VisualNode, resultVar string) {
	vc.renderBox(vn, resultVar, false)
}

// renderDatepicker renders a date entry as a basic entry.
func (vc *viewContext) renderDatepicker(vn *ast.VisualNode, resultVar string) {
	placeholder := `"YYYY-MM-DD"`
	if v, ok := vn.Props["placeholder"]; ok {
		placeholder = exprToGoValue(v, vc.ec)
	}
	tmpVar := resultVar + "Entry"
	vc.line("%s := widget.NewEntry()", tmpVar)
	vc.line("%s.SetPlaceHolder(%s)", tmpVar, placeholder)
	if v, ok := vn.Props["value"]; ok {
		val := exprToGoValue(v, vc.ec)
		vc.line("%s.SetText(fmt.Sprint(%s))", tmpVar, val)
	}
	vc.line("%s = %s", resultVar, tmpVar)
}

// renderUserComponent renders a user-defined component by calling its render method.
func (vc *viewContext) renderUserComponent(vn *ast.VisualNode, resultVar string) {
	// Look up the component
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

	// Build arguments
	var args []string
	for _, p := range comp.Params {
		if v, ok := vn.Props[p.Name]; ok {
			args = append(args, exprToGoValue(v, vc.ec))
		} else {
			args = append(args, "nil")
		}
	}

	// If component accepts children, pre-render caller's children as slot content
	if comp.ChildrenType != "" && len(vn.Children) > 0 {
		slotVar := resultVar + "Slot"
		vc.renderChildrenToObject(vn.Children, slotVar)
		args = append(args, slotVar)
	}

	vc.line("%s = m.render%s(%s)", resultVar, exportName(vn.Component), strings.Join(args, ", "))
}

// renderChildrenToObject pre-renders children into a single fyne.CanvasObject variable.
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
