package fyne

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// viewContext tracks state during BuildUI() code generation.
type viewContext struct {
	ec         *exprContext
	buf        *strings.Builder
	indent     int
	components []*ast.Component
	entryIndex int // next entry index for persistent entry fields
}

func (vc *viewContext) line(format string, args ...any) {
	fmt.Fprintf(vc.buf, "%s"+format+"\n", append([]any{strings.Repeat("\t", vc.indent)}, args...)...)
}

// renderNode generates Go code that creates a fyne.CanvasObject and assigns
// the result to resultVar. If the node has an if-condition, resultVar may
// remain nil.
func (vc *viewContext) renderNode(vn *ast.VisualNode, resultVar string) {
	hasIf := vn.If != nil
	if hasIf {
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

func (vc *viewContext) renderForLoop(vn *ast.VisualNode, resultVar string) {
	iterVar := vn.For.Variable
	iterExpr := exprToGoValue(vn.For.Iterable, vc.ec)
	loopItems := resultVar + "Items"
	indexVar := "_"
	if vn.For.IndexVar != "" {
		indexVar = vn.For.IndexVar
		vc.ec.localVars[indexVar] = true
		defer func() { delete(vc.ec.localVars, indexVar) }()
	}
	vc.ec.localVars[iterVar] = true
	defer func() { delete(vc.ec.localVars, iterVar) }()

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
		vc.line("%s = widget.NewProgressBarInfinite()", resultVar)
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
	default:
		// Try user-defined component
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
	if vn.StyleAttrs != nil {
		if _, ok := vn.StyleAttrs["padding"]; ok {
			hasPadding = true
		}
	}
	if vn.StyleBlock != nil {
		if _, ok := vn.StyleBlock["padding"]; ok {
			hasPadding = true
		}
	}

	if vertical {
		vc.line("%s = container.NewVBox(%s...)", resultVar, childrenVar)
	} else {
		vc.line("%s = container.NewHBox(%s...)", resultVar, childrenVar)
	}

	if hasPadding {
		vc.line("%s = container.NewPadded(%s)", resultVar, resultVar)
	}

	// Wrap with scroll if requested
	if v, ok := vn.Props["scroll"]; ok {
		val := exprToGoValue(v, vc.ec)
		if val == "true" {
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

// renderText renders a text/label widget.
func (vc *viewContext) renderText(vn *ast.VisualNode, resultVar string) {
	val := `""`
	if v, ok := vn.Props["value"]; ok {
		val = exprToGoValue(v, vc.ec)
	}

	// Check for bold/italic from style
	bold := false
	if vn.StyleAttrs != nil {
		if v, ok := vn.StyleAttrs["fontWeight"]; ok {
			if s, ok := v.Literal.(string); ok && s == "bold" {
				bold = true
			}
		}
	}
	if vn.StyleBlock != nil {
		if v, ok := vn.StyleBlock["fontWeight"]; ok {
			if s, ok := v.Literal.(string); ok && s == "bold" {
				bold = true
			}
		}
	}

	if bold {
		vc.line("%s = widget.NewLabelWithStyle(fmt.Sprint(%s), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})", resultVar, val)
	} else {
		vc.line("%s = widget.NewLabel(fmt.Sprint(%s))", resultVar, val)
	}

	// Handle click event
	if clickEvt, ok := vn.Events["click"]; ok && clickEvt.SNGL != nil {
		// Wrap in a tappable button with no visible styling
		vc.line("// text with click handler — use button")
		text := val
		vc.line("%s = widget.NewButton(fmt.Sprint(%s), func() {", resultVar, text)
		vc.indent++
		stmts := vc.ec.translateMutation(clickEvt.SNGL)
		for _, s := range stmts {
			vc.line("%s", s)
		}
		vc.line("m.doRefresh()")
		vc.indent--
		vc.line("})")
	}
}

// renderButton renders a button widget.
func (vc *viewContext) renderButton(vn *ast.VisualNode, resultVar string) {
	text := `""`
	if v, ok := vn.Props["text"]; ok {
		text = exprToGoValue(v, vc.ec)
	}

	if clickEvt, ok := vn.Events["click"]; ok && clickEvt.SNGL != nil {
		vc.line("%s = widget.NewButton(%s, func() {", resultVar, text)
		vc.indent++
		stmts := vc.ec.translateMutation(clickEvt.SNGL)
		for _, s := range stmts {
			vc.line("%s", s)
		}
		vc.line("m.doRefresh()")
		vc.indent--
		vc.line("})")
	} else {
		vc.line("%s = widget.NewButton(%s, nil)", resultVar, text)
	}

	// Disabled
	if v, ok := vn.Props["disabled"]; ok {
		val := exprToGoValue(v, vc.ec)
		tmpVar := resultVar + "Btn"
		vc.line("%s := %s.(*widget.Button)", tmpVar, resultVar)
		vc.line("if %s { %s.Disable() }", val, tmpVar)
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

// renderCheckbox renders a Check widget.
func (vc *viewContext) renderCheckbox(vn *ast.VisualNode, resultVar string) {
	label := `""`
	if v, ok := vn.Props["label"]; ok {
		label = exprToGoValue(v, vc.ec)
	}

	checkedExpr := "false"
	if v, ok := vn.Props["checked"]; ok {
		checkedExpr = exprToGoValue(v, vc.ec)
	}

	tmpVar := resultVar + "Chk"
	if changeEvt, ok := vn.Events["change"]; ok && changeEvt.SNGL != nil {
		vc.line("%s := widget.NewCheck(%s, func(checked bool) {", tmpVar, label)
		vc.indent++
		prevEventVar := vc.ec.eventVar
		vc.ec.eventVar = "checked"
		stmts := vc.ec.translateMutation(changeEvt.SNGL)
		vc.ec.eventVar = prevEventVar
		for _, s := range stmts {
			vc.line("%s", s)
		}
		vc.line("m.doRefresh()")
		vc.indent--
		vc.line("})")
	} else {
		vc.line("%s := widget.NewCheck(%s, nil)", tmpVar, label)
	}
	vc.line("%s.Checked = %s", tmpVar, checkedExpr)
	vc.line("%s = %s", resultVar, tmpVar)
}

// renderRadio renders a RadioGroup widget.
func (vc *viewContext) renderRadio(vn *ast.VisualNode, resultVar string) {
	options := "nil"
	if v, ok := vn.Props["options"]; ok {
		options = exprToGoStringList(v, vc.ec)
	}

	selected := `""`
	if v, ok := vn.Props["value"]; ok {
		selected = exprToGoValue(v, vc.ec)
	}

	tmpVar := resultVar + "Radio"
	if changeEvt, ok := vn.Events["change"]; ok && changeEvt.SNGL != nil {
		vc.line("%s := widget.NewRadioGroup(%s, func(s string) {", tmpVar, options)
		vc.indent++
		prevEventVar := vc.ec.eventVar
		vc.ec.eventVar = "s"
		stmts := vc.ec.translateMutation(changeEvt.SNGL)
		vc.ec.eventVar = prevEventVar
		for _, s := range stmts {
			vc.line("%s", s)
		}
		vc.line("m.doRefresh()")
		vc.indent--
		vc.line("})")
	} else {
		vc.line("%s := widget.NewRadioGroup(%s, nil)", tmpVar, options)
	}
	vc.line("%s.Selected = %s", tmpVar, selected)

	// Horizontal layout
	if v, ok := vn.Props["direction"]; ok {
		if s, ok := v.Literal.(string); ok && s == "horizontal" {
			vc.line("%s.Horizontal = true", tmpVar)
		}
	}

	vc.line("%s = %s", resultVar, tmpVar)
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
	if v, ok := vn.Props["value"]; ok {
		selected = exprToGoValue(v, vc.ec)
	}

	tmpVar := resultVar + "Sel"
	if changeEvt, ok := vn.Events["change"]; ok && changeEvt.SNGL != nil {
		vc.line("%s := widget.NewSelect(%s, func(s string) {", tmpVar, options)
		vc.indent++
		prevEventVar := vc.ec.eventVar
		vc.ec.eventVar = "s"
		stmts := vc.ec.translateMutation(changeEvt.SNGL)
		vc.ec.eventVar = prevEventVar
		for _, s := range stmts {
			vc.line("%s", s)
		}
		vc.line("m.doRefresh()")
		vc.indent--
		vc.line("})")
	} else {
		vc.line("%s := widget.NewSelect(%s, nil)", tmpVar, options)
	}
	vc.line("%s.Selected = %s", tmpVar, selected)

	if v, ok := vn.Props["placeholder"]; ok {
		vc.line("%s.PlaceHolder = %s", tmpVar, exprToGoValue(v, vc.ec))
	}

	vc.line("%s = %s", resultVar, tmpVar)
}

// renderProgress renders a ProgressBar widget.
func (vc *viewContext) renderProgress(vn *ast.VisualNode, resultVar string) {
	tmpVar := resultVar + "Prog"
	vc.line("%s := widget.NewProgressBar()", tmpVar)
	if v, ok := vn.Props["value"]; ok {
		val := exprToGoValue(v, vc.ec)
		maxVal := "1.0"
		if mv, ok := vn.Props["max"]; ok {
			maxVal = exprToGoValue(mv, vc.ec)
		}
		vc.line("%s.SetValue(float64(%s) / float64(%s))", tmpVar, val, maxVal)
	}
	vc.line("%s = %s", resultVar, tmpVar)
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
		stmts := vc.ec.translateMutation(changeEvt.SNGL)
		vc.ec.eventVar = prevEventVar
		for _, s := range stmts {
			vc.line("%s", s)
		}
		vc.line("m.doRefresh()")
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

// renderScroll wraps a child in a scroll container.
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
		switch direction {
		case "horizontal":
			vc.line("%s = container.NewHScroll(%s)", resultVar, childVar)
		default:
			vc.line("%s = container.NewVScroll(%s)", resultVar, childVar)
		}
	}
}

// renderBadge renders a label styled as a badge.
func (vc *viewContext) renderBadge(vn *ast.VisualNode, resultVar string) {
	val := `""`
	if v, ok := vn.Props["value"]; ok {
		val = exprToGoValue(v, vc.ec)
	}
	vc.line("%s = widget.NewLabel(fmt.Sprint(%s))", resultVar, val)
}

// renderConditionalContainer renders modal/drawer/popover as conditional vbox.
func (vc *viewContext) renderConditionalContainer(vn *ast.VisualNode, resultVar string) {
	openExpr := "true"
	if v, ok := vn.Props["open"]; ok {
		openExpr = exprToGoValue(v, vc.ec)
	}

	vc.line("if %s {", openExpr)
	vc.indent++

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

	vc.line("%s = widget.NewCard(\"\", \"\", container.NewVBox(%s...))", resultVar, childrenVar)

	vc.indent--
	vc.line("}")
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
	// Generate a basic label placeholder; full table needs data binding
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
	if clickEvt, ok := vn.Events["click"]; ok && clickEvt.SNGL != nil {
		vc.line("%s = widget.NewButton(%s, func() {", resultVar, label)
		vc.indent++
		stmts := vc.ec.translateMutation(clickEvt.SNGL)
		for _, s := range stmts {
			vc.line("%s", s)
		}
		vc.line("m.doRefresh()")
		vc.indent--
		vc.line("})")
	} else {
		vc.line("%s = widget.NewButton(%s, nil)", resultVar, label)
	}
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
	vc.line("%s = m.render%s(%s)", resultVar, exportName(vn.Component), strings.Join(args, ", "))
}
